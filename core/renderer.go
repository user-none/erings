// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
	"github.com/user-none/erings/internal/hostgpu/backend"
)

// A frameRenderer produces the frame from the records the walk
// builds: the row records, the memory pages, and the VDP1 buffer.
// The walk calls it at its points, on the walker goroutine, and
// neither renderer changes the walk's schedule.
// The software renderer renders each row at its render point, in
// parallel with the SH-2 goroutines, and has nothing to do at the end
// of the display or the frame. The GPU renderer binds the VDP1 device
// half the rows sample and takes the memory pages at the render
// point, takes the VDP1's entries after every tick, and renders on
// the host GPU in batches of rows submitted during the display,
// waiting for the last during the vertical blank of the same frame.
// Neither is ever a frame behind.
type frameRenderer interface {
	// beginFrame runs at the frame change, after the VDP2's BeginFrame.
	beginFrame()
	// beginLine runs at row y's setup point, right after BeginLine
	// prepared the row, with the VDP2's state and memory as the row
	// setup read them.
	beginLine(y int)
	// renderLine runs at row y's render point, the end of its line,
	// after BeginLine prepared the row.
	renderLine(y int)
	// vdp1Line runs after the VDP1's tick of every line of the frame,
	// active or blank, before the row's render point.
	vdp1Line(line int)
	// endDisplay runs as the last active line ends, before the VDP1's
	// end-of-display work.
	endDisplay()
	// endFrame runs after the walk's last line.
	endFrame()
	// syncPair brings the CPU pair up to date with the device pair
	// through line, on the walker: the entries and pages appended
	// since the last take are taken and submitted, the submissions
	// waited for, and both device halves copied into the buffers they
	// mirror. A renderer with no device pair does nothing.
	syncPair(line int)
	// err reports a runtime failure since the last call and clears it.
	err() error
	// close releases what the renderer holds.
	close()
	// backend names what draws the frame, for display: Software, or
	// the GPU API the device is opened on.
	backend() string
	// device names the renderer's device, or is empty for a renderer
	// with none.
	device() string
	// framebuffer is the RGBA bytes of the frame the frontend reads.
	framebuffer() []byte
	// framebufferStride is the frame's row stride in bytes.
	framebufferStride() int
	// displayHeight is the frame's displayed rows.
	displayHeight() int
}

// framebufferBytes is a byte view of an output frame buffer's words,
// whose bytes are R, G, B, A on a little-endian host.
func framebufferBytes(f compute.Framebuffer) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(&f[0])), len(f)*4)
}

// blankFramebuffer fills the first words of an output frame buffer
// with opaque black.
func blankFramebuffer(f compute.Framebuffer, words int) {
	for i := range f[:words] {
		f[i] = frameBlack
	}
}

// growFramebuffer returns f when it holds words words, and otherwise
// a black output frame buffer of words words in its place.
func growFramebuffer(f compute.Framebuffer, words int) compute.Framebuffer {
	if len(f) >= words {
		return f
	}
	f = make(compute.Framebuffer, words)
	blankFramebuffer(f, words)
	return f
}

// growRenderBuffers gives the host render buffers of a renderer on the
// walker the lengths bufferSizes names for the frame v latched at the
// scale shift and slots row slots: each layer and line color buffer of
// in that is shorter is replaced, and the output frame buffer f is
// returned grown (growFramebuffer). The layer and line color buffers
// hold only the rows in flight, so nothing is copied. It returns the
// frame buffer and the sizes.
func growRenderBuffers(v *VDP2, shift uint32, slots int, in *screen.CompositeInputs, f compute.Framebuffer) (compute.Framebuffer, renderSizes) {
	s := bufferSizes(shift, slots, v.frame.width, v.DisplayHeight(), 0)
	for i := range in.Layers {
		if len(in.Layers[i]) < s.layerWords {
			in.Layers[i] = make(compute.Layer, s.layerWords)
		}
	}
	for i := range in.LineColors {
		if len(in.LineColors[i]) < s.lineColorBytes {
			in.LineColors[i] = make(compute.LineColor, s.lineColorBytes)
		}
	}
	return growFramebuffer(f, s.framebufferWords), s
}

// Renderer selects how frames are produced.
type Renderer int

const (
	// RendererSoftware renders on the CPU as the walker walks.
	RendererSoftware Renderer = iota
	// RendererGPU renders the frame on the host GPU after the display,
	// from the same records.
	RendererGPU
)

// upscaleNames are the upscale option's values in scale order: index
// i names the scale 1 << i, the shift the enhanced renderers take.
var upscaleNames = [...]string{"1x", "2x", "4x", "8x", "16x"}

// UpscaleNames lists the upscale option's values, "1x" through "16x".
func UpscaleNames() []string { return append([]string(nil), upscaleNames[:]...) }

// SetUpscale selects the scale the frame renders at by its option
// value, "1x" through "16x": at 1x the accurate renderers render at
// the native resolution, and above it the enhanced renderers render
// at the scale on both axes. A request for the active scale does
// nothing. A change reopens the active renderer at the new scale, so
// it is made before the first frame; a GPU failure on the reopen
// falls back to software and returns the reason.
func (e *Emulator) SetUpscale(name string) error {
	shift := -1
	for i, n := range upscaleNames {
		if n == name {
			shift = i
		}
	}
	if shift < 0 {
		return fmt.Errorf("unknown upscale %q (1x, 2x, 4x, 8x, or 16x)", name)
	}
	if uint32(shift) == e.renderShift {
		return nil
	}
	e.renderShift = uint32(shift)
	return e.openRenderer(e.rendererMode)
}

// rendererNames are the renderer option's values.
var rendererNames = [...]string{"GPU", "Software"}

// RendererNames lists the renderer option's values, "GPU" and
// "Software".
func RendererNames() []string { return append([]string(nil), rendererNames[:]...) }

// SetRenderer selects the renderer before the first frame. A request
// for the active mode does nothing. Otherwise the mode's renderer
// opens in place of the active one (openRenderer). The mode left
// active becomes the requested mode, so Start keeps it.
func (e *Emulator) SetRenderer(mode Renderer) error {
	var err error
	if mode != e.rendererMode {
		err = e.openRenderer(mode)
	}
	e.requestedRenderer = e.rendererMode
	return err
}

// openRenderer closes the active renderer and opens the mode's at the
// upscale. The active renderer closes before the next opens, so its
// device and buffers are released before another device is opened.
// GPU mode initializes synchronously (device, pipeline build,
// buffers); on any failure software becomes active and the reason is
// returned. Above 1x GPU mode opens the enhanced GPU renderer and
// software mode the enhanced software renderer, which a GPU failure
// falls back to.
func (e *Emulator) openRenderer(mode Renderer) error {
	e.renderer.close()
	e.renderer = nil
	var err error
	if mode == RendererGPU {
		var r frameRenderer
		if e.renderShift == 0 {
			r, err = openGPURenderer(e.vdp2, e.vdp1)
		} else {
			r, err = openEnhancedGPURenderer(e.vdp2, e.vdp1, e.renderShift)
		}
		if err == nil {
			e.renderer, e.rendererMode = r, RendererGPU
			// The GPU renderer's device draws the pair; the walk
			// charges cycles without drawing until a CPU read of the
			// frame buffer asks for the pair (servicePair). The
			// enhanced one draws a pair of its own at scale, and the
			// walk keeps the CPU pair.
			e.vdp1.setPairKept(e.renderShift != 0)
			return nil
		}
		err = gpuError(err)
	}
	e.useSoftwareRenderer()
	return err
}

// useSoftwareRenderer makes the software renderer the active one: the
// accurate renderer at 1x, the enhanced renderer above it. The walk
// keeps the CPU pair from here on. After a runtime failure the pair
// is stale until the next frame change and the draws behind it: the
// device's draws never reached it, and the CPU's frame buffer writes
// made while the pair was not kept went to the shadow alone
// (fbWriteTarget) and are not brought back.
func (e *Emulator) useSoftwareRenderer() {
	if e.renderer != nil {
		e.renderer.close()
	}
	if e.renderShift == 0 {
		e.renderer = newSoftwareRenderer(e.vdp2)
	} else {
		e.renderer = newEnhancedRenderer(e.vdp2, e.vdp1, e.renderShift)
	}
	e.rendererMode = RendererSoftware
	e.vdp1.setPairKept(true)
}

// Renderer reports the active renderer.
func (e *Emulator) Renderer() Renderer { return e.rendererMode }

// SetRendererByName selects the renderer from its flag name, "gpu" or
// "software".
func (e *Emulator) SetRendererByName(name string) error {
	switch name {
	case "gpu":
		return e.SetRenderer(RendererGPU)
	case "software":
		return e.SetRenderer(RendererSoftware)
	}
	return fmt.Errorf("unknown renderer %q (gpu or software)", name)
}

// softwareBackend is the backend name of the software renderers.
const softwareBackend = "Software"

// RendererName describes the active renderer for logs: the backend,
// the scale above native, and the device when there is one, as
// "Software 2x" or "Vulkan (NVIDIA GeForce RTX 5060)".
func (e *Emulator) RendererName() string {
	info := e.RenderInfo()
	name := info.Backend
	if info.Scale > 1 {
		name += fmt.Sprintf(" %dx", info.Scale)
	}
	if info.Device != "" {
		name += " (" + info.Device + ")"
	}
	return name
}

// RenderInfo describes the active renderer for display: what draws
// the frame, Software or the GPU API in use, the device's name (empty
// for software), and the scale the frame renders at, 1 for native.
type RenderInfo struct {
	Backend string
	Device  string
	Scale   int
}

// RenderInfo returns the active renderer's description.
func (e *Emulator) RenderInfo() RenderInfo {
	return RenderInfo{Backend: e.renderer.backend(), Device: e.renderer.device(), Scale: 1 << e.renderShift}
}

// GPUError reports the runtime failure that switched the session to
// software, or nil.
func (e *Emulator) GPUError() error { return e.rendererErr }

// rendererFailed switches the emulator to software for the rest of
// the session after a runtime failure
func (e *Emulator) rendererFailed(err error) {
	e.rendererErr = errors.Join(e.rendererErr, gpuError(err))
	e.useSoftwareRenderer()
}

// gpuError places a GPU renderer's failure, at open or at run time:
// the GPU renderer and the backend it runs on.
func gpuError(err error) error {
	return fmt.Errorf("GPU renderer (%s): %w", backend.Name, err)
}
