// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

// scaleRowAt is scaleRow at the scale shift, the VDP2's configuration
// restored after, so a native render around it is unchanged.
func scaleRowAt(v *VDP2, dst, native *lineRecord, k int32, shift uint32) {
	cfg := v.enhance.cfg
	v.enhance.cfg.shift = shift
	v.scaleRow(dst, native, k)
	v.enhance.cfg = cfg
}

// scaledNBGRow is sub-row k of layer's row in the native record at
// the scale shift (scaleRow), resolved again at row 0 so it draws
// into a buffer of one scaled row.
func scaledNBGRow(v *VDP2, native *lineRecord, layer int, k int32, shift uint32) screen.NBGParams {
	var r lineRecord
	scaleRowAt(v, &r, native, k, shift)
	p := r.NBG[layer]
	p.Y = 0
	screen.ResolveNBGLine((*compute.VDP2VRAM)(v.vram), &p)
	return p
}

// drawNBGRow draws the row of p's whole width into a new buffer.
func drawNBGRow(v *VDP2, entry int, p *screen.NBGParams) compute.Layer {
	out := make(compute.Layer, p.Width)
	vram := (*compute.VDP2VRAM)(v.vram)
	if entry == entryNBGCell {
		var st screen.NBGCellState
		screen.NBGCellSpan(vram, &v.palette, out, p, &st, 0, p.Width)
	} else {
		screen.NBGBitmapSpan(vram, &v.palette, out, p, 0, p.Width)
	}
	return out
}

// scaledRBGRow is sub-row k of layer's row in the native record at
// the scale shift (scaleRow), prepared again at row 0 so it draws
// into a buffer of one scaled row. scaleRow reads the rotation state
// as the row's setup left it, so it is taken at the row's setup
// point.
func scaledRBGRow(v *VDP2, native *lineRecord, layer int, k int32, shift uint32) screen.RBGParams {
	var r lineRecord
	scaleRowAt(v, &r, native, k, shift)
	p := r.RBG[layer-screen.LayerRBG0]
	p.Y = 0
	screen.PrepareRBGRow(&p)
	return p
}

// drawRBGRow draws the row of p's whole width into a new layer
// buffer and a new line color buffer.
func drawRBGRow(v *VDP2, entry int, p *screen.RBGParams) (compute.Layer, compute.LineColor) {
	out := make(compute.Layer, p.Width)
	lc := make(compute.LineColor, p.Width)
	vram := (*compute.VDP2VRAM)(v.vram)
	cram := (*compute.CRAM)(v.cram)
	if entry == entryRBGCell {
		screen.RBGCellSpan(vram, cram, &v.palette, out, lc, p, 0, p.Width)
	} else {
		screen.RBGBitmapSpan(vram, cram, &v.palette, out, lc, p, 0, p.Width)
	}
	return out, lc
}

// TestScrollBlockCorners holds the scroll screens' scaled source
// positions to the native ones at every block corner: over every
// benchmark scene's rows, at scale shifts 1 to 3, sub-row 0 of a
// scaled NBG or RBG row puts at dot N*x the native row's dot x, and
// an RBG row's line color the same. A divided step or a lost
// fraction bit moves a corner, and so does a hi-res rotation row
// (HcntShift) whose shared step is not taken on the native column.
// The Xp coefficient scene replaces the viewpoint Xp per dot from a
// coefficient table in data mode 3 (VDP2 manual Sec 6.1), a value the
// scaled row must carry at the row's fraction width.
func TestScrollBlockCorners(t *testing.T) {
	hiRes := 0
	scenes := append(benchVDP2Scenes(), benchVDP2Scene{name: "RBG0 cell Xp coefficients", build: func() (*VDP2, vdp1FBView) {
		v := benchVDP2()
		benchRBG0Cell(v, 1)
		benchRBG0Coefficients(v)
		benchReg(v, vdp2KTCTL, 0x000D)
		for i := uint32(0); i < 512; i++ {
			coef := uint32((i * 37) << 8)
			v.WriteVRAM16(benchCoefTable+i*4, uint16(coef>>16))
			v.WriteVRAM16(benchCoefTable+i*4+2, uint16(coef))
		}
		return v, vdp1FBView{}
	}})
	for _, s := range scenes {
		v, fb := s.build()
		vram := (*compute.VDP2VRAM)(v.vram)
		swr := newSoftwareRenderer(v)
		v.BeginFrame()
		swr.beginFrame()
		lines := int(v.ActiveLines())
		checked := 0
		for y := 0; y < lines; y++ {
			beginRow(v, swr, y, fb)
			r := v.rowRecord(y)
			for i := 0; i < int(r.NSpans); i++ {
				entry, layer := spanKind(r.Spans[i])
				switch entry {
				case entryNBGCell, entryNBGBitmap:
					np := r.NBG[layer]
					np.Y = 0
					screen.ResolveNBGLine(vram, &np)
					width := np.Width
					native := drawNBGRow(v, entry, &np)
					for shift := uint32(1); shift <= 3; shift++ {
						n := int32(1) << shift
						sp := scaledNBGRow(v, r, layer, 0, shift)
						scaled := drawNBGRow(v, entry, &sp)
						for x := range width {
							if got, want := scaled[x*n], native[x]; got != want {
								t.Fatalf("%s row %d NBG%d shift %d: dot %d = %08x, want the native %08x", s.name, y, layer, shift, x, got, want)
							}
						}
						checked++
					}
				case entryRBGCell, entryRBGBitmap:
					rp := r.RBG[layer-screen.LayerRBG0]
					if rp.HcntShift != 0 {
						hiRes++
					}
					rp.Y = 0
					screen.PrepareRBGRow(&rp)
					width := rp.Width
					native, nativeLC := drawRBGRow(v, entry, &rp)
					for shift := uint32(1); shift <= 3; shift++ {
						n := int32(1) << shift
						sp := scaledRBGRow(v, r, layer, 0, shift)
						scaled, scaledLC := drawRBGRow(v, entry, &sp)
						for x := range width {
							if got, want := scaled[x*n], native[x]; got != want {
								t.Fatalf("%s row %d RBG%d shift %d: dot %d = %08x, want the native %08x", s.name, y, layer-screen.LayerRBG0, shift, x, got, want)
							}
							if got, want := scaledLC[x*n], nativeLC[x]; got != want {
								t.Fatalf("%s row %d RBG%d shift %d: line color %d = %02x, want the native %02x", s.name, y, layer-screen.LayerRBG0, shift, x, got, want)
							}
						}
						checked++
					}
				}
			}
			swr.renderLine(y)
			v.EndLine()
		}
		v.EndFrame()
		if checked == 0 && s.name != "sprite 16-bit" && s.name != "sprite 8-bit" {
			t.Errorf("%s: no scroll row checked", s.name)
		}
	}
	if hiRes == 0 {
		t.Error("no hi-res rotation row checked")
	}
}
