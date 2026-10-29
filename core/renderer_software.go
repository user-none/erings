// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

// softwareRenderer renders on the walker as the walk goes: each row's
// spans run at the row's render point against the live VRAM and CRAM and
// the row's record, and the composite writes the row of the renderer's
// output frame buffer. It holds one row between the row's setup point
// and its render point, so its layer and line color buffers hold one
// row slot, and the output frame buffer the latched frame. It is the
// renderer on every platform and the reference the GPU renderer is
// compared with.
type softwareRenderer struct {
	vdp2              *VDP2
	in                screen.CompositeInputs // its layer and line color buffers, the VDP2's palette and VRAM
	outputFramebuffer compute.Framebuffer
	row               lineRecord // the row being rendered, placed at slot 0
}

// newSoftwareRenderer opens the software renderer: its buffers hold
// the frame the VDP2 has latched, and the output frame buffer starts
// black.
func newSoftwareRenderer(v *VDP2) *softwareRenderer {
	r := &softwareRenderer{vdp2: v}
	r.in.Sprite = noSpriteFB
	r.in.Palette = &v.palette
	r.in.VRAM = (*compute.VDP2VRAM)(v.vram)
	r.grow()
	return r
}

// grow gives the buffers the lengths the latched frame needs, at one
// row slot.
func (r *softwareRenderer) grow() {
	r.outputFramebuffer, _ = growRenderBuffers(r.vdp2, 1, &r.in, r.outputFramebuffer)
}

// beginFrame grows the buffers when BeginFrame latched a new geometry,
// then blanks the displayed rows of the output frame buffer when the
// VDP2 blanked the frame (a geometry change or the display off).
func (r *softwareRenderer) beginFrame() {
	v := r.vdp2
	if v.frame.resized {
		r.grow()
	}
	if v.frame.blanked {
		blankFramebuffer(r.outputFramebuffer, v.DisplayHeight()*v.frame.width)
	}
}

// beginLine writes the inputs row y's chain names that the row setup
// does not into the renderer's row slot (rowInputs).
func (r *softwareRenderer) beginLine(y int) {
	v := r.vdp2
	if !v.lineActive {
		return
	}
	v.rowInputs(v.rowRecord(y), &r.in.Layers, &r.in.LineColors, 0, y, 0)
}

// renderLine renders the prepared row whole (renderRecord) from a copy
// placed at the renderer's row slot, the sprite layer read from the
// row's VDP1 frame buffer as it is at the render point, or noSpriteFB
// when the row has none; the row's sprite read mode decides whether it
// is read.
func (r *softwareRenderer) renderLine(int) {
	v := r.vdp2
	if !v.lineActive {
		return
	}
	r.in.Sprite = noSpriteFB
	if v.lineFB.data != nil {
		r.in.Sprite = compute.VDP1FB(v.lineFB.data)
	}
	r.row = *v.rowRecord(v.lineY)
	placeRow(&r.row, 0)
	v.renderRecord(&r.row, &r.in, r.outputFramebuffer, int32(v.frame.width))
}

func (r *softwareRenderer) vdp1Line(int) {}

func (r *softwareRenderer) endDisplay() {}

func (r *softwareRenderer) endFrame() {}

func (r *softwareRenderer) syncPair(int) {}

func (r *softwareRenderer) err() error { return nil }

func (r *softwareRenderer) close() {}

func (r *softwareRenderer) backend() string { return softwareBackend }

func (r *softwareRenderer) device() string { return "" }

func (r *softwareRenderer) framebuffer() []byte { return framebufferBytes(r.outputFramebuffer) }

func (r *softwareRenderer) framebufferStride() int { return r.vdp2.FramebufferStride() }

func (r *softwareRenderer) displayHeight() int { return r.vdp2.DisplayHeight() }

// renderRecord renders a row record whole: each span of the row's
// chain runs over the row's width dots on the layer buffer of in it
// names, with the record as the params, against the live VRAM and
// CRAM, and the composite writes the row into out from the buffers of
// in. The chain decided the entry and the layer; this loop only calls
// the entry with that layer's buffers. The row state an entry carries
// (the cell cache, the gradation carries) starts at zero for the row,
// as it does on the device.
func (v *VDP2) renderRecord(r *lineRecord, in *screen.CompositeInputs, out compute.Framebuffer, width int32) {
	cram := (*compute.CRAM)(v.cram)
	for i := 0; i < int(r.NSpans); i++ {
		entry, layer := spanKind(r.Spans[i])
		switch entry {
		case entryNBGCell:
			var cache screen.NBGCellState
			screen.NBGCellSpan(in.VRAM, in.Palette, in.Layers[layer], &r.NBG[layer], &cache, 0, width)
		case entryNBGBitmap:
			screen.NBGBitmapSpan(in.VRAM, in.Palette, in.Layers[layer], &r.NBG[layer], 0, width)
		case entryRBGCell:
			screen.RBGCellSpan(in.VRAM, cram, in.Palette, in.Layers[layer], in.LineColors[layer-screen.LayerRBG0], &r.RBG[layer-screen.LayerRBG0], 0, width)
		case entryRBGBitmap:
			screen.RBGBitmapSpan(in.VRAM, cram, in.Palette, in.Layers[layer], in.LineColors[layer-screen.LayerRBG0], &r.RBG[layer-screen.LayerRBG0], 0, width)
		case entryGradation:
			var carries screen.GradationState
			screen.GradationSpan(in.Layers[layer], &r.Grad, &carries, 0, width)
		case entryComposite:
			screen.CompositeSpan(in, out, &r.Comp, 0, width)
		case entryExternal:
			// Written into the layer buffer at the row's setup point.
		}
	}
}
