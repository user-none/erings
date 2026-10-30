// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
)

// The equivalence test runs through the platform-neutral renderer
// seam. A host without a GPU backend skips; shader build and
// execution errors fail. Device selection stays in hostgpu/backend.
// Every backend runs the same scenes, dispatch encoding, and byte
// comparisons.

func equalRenderBytes(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length %d, want %d", name, len(got), len(want))
	}
	if bytes.Equal(got, want) {
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: byte %#x: %#02x, want %#02x", name, i, got[i], want[i])
		}
	}
}

// renderFrame renders one frame of v through r at the walk's points:
// BeginFrame and beginFrame, then per row beginRow and renderLine,
// then the display and frame ends. beforeRow, when set, runs before
// each row's beginRow.
func renderFrame(v *VDP2, r frameRenderer, fb vdp1FBView, beforeRow func(v *VDP2, y, lines int)) {
	v.BeginFrame()
	r.beginFrame()
	lines := int(v.ActiveLines())
	for y := 0; y < lines; y++ {
		if beforeRow != nil {
			beforeRow(v, y, lines)
		}
		beginRow(v, r, y, fb)
		r.renderLine(y)
		v.EndLine()
	}
	r.endDisplay()
	v.EndFrame()
	r.endFrame()
}

// equivalenceFrames are the frames the renderer comparison tests
// render every scene through, in order. before runs ahead of the
// frame and beforeRow ahead of each row (renderFrame).
var equivalenceFrames = []struct {
	name      string
	before    func(v *VDP2)
	beforeRow func(v *VDP2, y, lines int)
}{
	{name: "first frame"},
	{name: "VRAM, CRAM, and RAMCTL written mid-frame", beforeRow: writeMemoryMidFrame},
	{name: "display off", before: func(v *VDP2) { v.Write(0, v.regs[vdp2TVMD]&^0x8000) }},
	{name: "display back on", before: func(v *VDP2) { v.Write(0, v.regs[vdp2TVMD]|0x8000) }},
	{name: "width changed", before: func(v *VDP2) { v.Write(0, v.regs[vdp2TVMD]^1) }},
	{name: "hi-res double-density interlace", before: func(v *VDP2) { v.Write(0, v.regs[vdp2TVMD]|0x00C3) }},
}

// writeMemoryMidFrame writes a character's VRAM and a CRAM color at
// the rows a third and two thirds down, and toggles RAMCTL's CRAM
// mode bit at the second, so page delivery and palette rebuilds run
// mid-frame.
func writeMemoryMidFrame(v *VDP2, y, lines int) {
	if y != lines/3 && y != 2*lines/3 {
		return
	}
	v.WriteVRAM32(benchChar4*0x20, uint32(y)*0x01010101)
	v.WriteCRAM16(2, uint16(y)*17)
	if y == 2*lines/3 {
		v.Write(vdp2RAMCTL*2, v.regs[vdp2RAMCTL]^0x1000)
	}
}

// TestGPUVDP2MatchesSoftware compares actual device rendering
// with the software schedule across every benchmark scene, through
// the equivalenceFrames. The same inputs go through both schedules
// at the same points. Each scene opens the GPU renderer on the VDP2
// it renders, as the emulator does.
func TestGPUVDP2MatchesSoftware(t *testing.T) {
	for _, s := range benchVDP2Scenes() {
		t.Run(s.name, func(t *testing.T) {
			gpuMatchesSoftware(t, s, func(v *VDP2, v1 *VDP1) (*gpuRenderer, error) {
				return openGPURenderer(v, v1, defaultBackend())
			}, openSoftwareRenderer)
		})
	}
}

// TestEnhancedGPUMatchesEnhancedSoftware compares the enhanced GPU
// renderer on the platform's device with the enhanced software
// renderer at scale shifts 0 to 2 across the enhanced scenes, through
// the equivalenceFrames.
func TestEnhancedGPUMatchesEnhancedSoftware(t *testing.T) {
	for shift := uint32(0); shift <= 2; shift++ {
		for _, s := range enhancedScenes() {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				gpuMatchesSoftware(t, s, func(v *VDP2, v1 *VDP1) (*enhancedGPURenderer, error) {
					v.setEnhance(enhanceConfig{shift: shift})
					return openEnhancedGPURenderer(v, v1, defaultBackend())
				}, openEnhancedRenderer(shift))
			})
		}
	}
}

// TestExecEnhancedGPUMatchesEnhancedSoftware runs the enhanced GPU
// renderer on the exec device against the enhanced software renderer
// at scale shifts 0 to 2 across the enhanced scenes and the
// equivalenceFrames, at batch rows of 1, gpuBatchLines >> shift (the
// renderer's own), and rowRecordMax: the sub-row store and the row
// slots of a ring that wraps through every region many times a frame,
// of the renderer's batches, and of the frame in one submission.
func TestExecEnhancedGPUMatchesEnhancedSoftware(t *testing.T) {
	parent, subs := ComputeSources()
	for shift := uint32(0); shift <= 2; shift++ {
		for _, rows := range []int{1, gpuBatchLines >> shift, rowRecordMax} {
			for _, s := range enhancedScenes() {
				t.Run(fmt.Sprintf("%s shift %d batch %d", s.name, shift, rows), func(t *testing.T) {
					gpuMatchesSoftware(t, s, func(v *VDP2, v1 *VDP1) (*enhancedGPURenderer, error) {
						prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
						if err != nil {
							return nil, err
						}
						v.setEnhance(enhanceConfig{shift: shift})
						return newEnhancedGPURenderer(v, v1, prog, rows)
					}, openEnhancedRenderer(shift))
				})
			}
		}
	}
}

// TestExecEnhancedGPUSubmitWithinRow holds the enhanced GPU renderer
// on the exec device to the enhanced software renderer when a batch
// is submitted between a row's setup point and its render point, as a
// CPU read of the frame buffer does (syncPair), on every third row:
// the row set up before the submit renders in the next region's slots
// from its sub-row records. At 2x, batch rows of 1 and the renderer's
// own, over the enhanced scenes.
func TestExecEnhancedGPUSubmitWithinRow(t *testing.T) {
	const shift = 1
	parent, subs := ComputeSources()
	for _, rows := range []int{1, gpuBatchLines >> shift} {
		for _, s := range enhancedScenes() {
			t.Run(fmt.Sprintf("%s batch %d", s.name, rows), func(t *testing.T) {
				sw, swFB := s.build()
				v, fb := s.build()
				prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
				if err != nil {
					t.Fatal(err)
				}
				v.setEnhance(enhanceConfig{shift: shift})
				g, err := newEnhancedGPURenderer(v, NewVDP1(NewSCU()), prog, rows)
				if err != nil {
					t.Fatal(err)
				}
				defer g.close()
				ref := openEnhancedRenderer(shift)(sw)
				defer ref.close()
				renderFrame(sw, ref, swFB, nil)
				v.BeginFrame()
				g.beginFrame()
				lines := int(v.ActiveLines())
				for y := 0; y < lines; y++ {
					beginRow(v, g, y, fb)
					if y%3 == 1 {
						g.syncPair(y)
					}
					g.renderLine(y)
					v.EndLine()
				}
				g.endDisplay()
				v.EndFrame()
				g.endFrame()
				if err := g.err(); err != nil {
					t.Fatal(err)
				}
				n := ref.framebufferStride() * ref.displayHeight()
				equalRenderBytes(t, "frame", g.framebuffer()[:n], ref.framebuffer()[:n])
			})
		}
	}
}

// openSoftwareRenderer opens the software renderer on v, the
// reference the GPU renderer is compared with.
func openSoftwareRenderer(v *VDP2) frameRenderer { return newSoftwareRenderer(v) }

// openEnhancedRenderer opens the enhanced software renderer at the
// scale shift on v, with a VDP1 of its own, the reference the enhanced
// GPU renderer is compared with over scenes with no sprite layer.
func openEnhancedRenderer(shift uint32) func(v *VDP2) frameRenderer {
	return func(v *VDP2) frameRenderer {
		v.setEnhance(enhanceConfig{shift: shift})
		return newEnhancedRenderer(v, NewVDP1(NewSCU()))
	}
}

// TestExecGPUVDP2MatchesSoftware runs the GPU renderer on the exec
// device, which runs every dispatch in Go when a wait reaches it, as a
// device's queue would, against the software schedule across every
// benchmark scene and the equivalenceFrames, at batch rows of 1,
// gpuBatchLines, and rowRecordMax: a ring of row slots that wraps
// through every region many times a frame, the renderer's own
// batches, and the frame in one submission.
func TestExecGPUVDP2MatchesSoftware(t *testing.T) {
	parent, subs := ComputeSources()
	for _, rows := range []int{1, gpuBatchLines, rowRecordMax} {
		for _, s := range benchVDP2Scenes() {
			t.Run(fmt.Sprintf("%s batch %d", s.name, rows), func(t *testing.T) {
				gpuMatchesSoftware(t, s, func(v *VDP2, v1 *VDP1) (*gpuRenderer, error) {
					prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
					if err != nil {
						return nil, err
					}
					return newGPURenderer(v, v1, prog, rows, false)
				}, openSoftwareRenderer)
			})
		}
	}
}

// gpuMatchesSoftware renders a scene through the GPU renderer open
// makes and through the software renderer software opens, through
// the equivalenceFrames, and compares the frames byte for byte, their
// stride and displayed rows first. A host without the device skips.
func gpuMatchesSoftware[R frameRenderer](t *testing.T, s benchVDP2Scene, open func(v *VDP2, v1 *VDP1) (R, error), software func(v *VDP2) frameRenderer) {
	t.Helper()
	sw, swFB := s.build()
	gpu, gpuFB := s.build()
	// The composite reads the sprite frame buffer from the device pair
	// of the renderer's VDP1, so a scene's sprite frame buffer is
	// placed in that VDP1's display buffer and the view the GPU walk
	// samples is that buffer.
	v1 := NewVDP1(NewSCU())
	if gpuFB.data != nil {
		copy(v1.DisplayFB(), gpuFB.data)
		gpuFB.data = v1.DisplayFB()
	}
	g, err := open(gpu, v1)
	if errors.Is(err, hostgpu.ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	ref := software(sw)
	defer ref.close()
	for _, f := range equivalenceFrames {
		if f.before != nil {
			f.before(sw)
			f.before(gpu)
		}
		renderFrame(sw, ref, swFB, f.beforeRow)
		renderFrame(gpu, g, gpuFB, f.beforeRow)
		if err := g.err(); err != nil {
			t.Fatal(err)
		}
		stride, height := ref.framebufferStride(), ref.displayHeight()
		if g.framebufferStride() != stride || g.displayHeight() != height {
			t.Fatalf("%s: stride %d height %d, want %d and %d", f.name, g.framebufferStride(), g.displayHeight(), stride, height)
		}
		n := stride * height
		equalRenderBytes(t, f.name, g.framebuffer()[:n], ref.framebuffer()[:n])
	}
}
