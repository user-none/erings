// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
)

// progressiveScene builds the bench scene under double-density
// interlace with the enhancement configuration, for the enhanced
// renderers at the shift.
func progressiveScene(s benchVDP2Scene, shift uint32, deinterlace bool) (*VDP2, vdp1FBView) {
	v, fb := s.build()
	v.Write(0, v.regs[vdp2TVMD]|0x00C0)
	v.setEnhance(enhanceConfig{shift: shift, deinterlace: deinterlace})
	return v, fb
}

// TestProgressiveFrameMatchesWeave renders every bench scene under
// double-density interlace through the enhanced software renderer at
// scale shifts 0 to 2 two ways, after a frame that settles the
// per-frame captures: one frame with deinterlacing on, and two frames,
// one per field, with it off. The deinterlaced frame must equal the
// weave of the two fields, so every sibling line (scroll screens,
// rotation screens, windows, per-line tables, and the sprite layer) is
// what a native render of the other parity from the same state
// produces. The mosaic scene downgrades to single density, has no
// sibling, and renders the same both ways.
func TestProgressiveFrameMatchesWeave(t *testing.T) {
	for _, s := range benchVDP2Scenes() {
		for shift := uint32(0); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				v, fb := progressiveScene(s, shift, true)
				r := newEnhancedRenderer(v, NewVDP1(NewSCU()))
				defer r.close()
				renderFrame(v, r, fb, nil)
				renderFrame(v, r, fb, nil)
				if v.frame.effIntl == 3 && v.recordsPerLine() != 2 {
					t.Fatal("no sibling records with deinterlacing on")
				}

				w, wfb := progressiveScene(s, shift, false)
				ref := newEnhancedRenderer(w, NewVDP1(NewSCU()))
				defer ref.close()
				renderFrame(w, ref, wfb, nil)
				renderFrame(w, ref, wfb, nil)
				field := w.frame.field
				renderFrame(w, ref, wfb, nil)
				if w.frame.effIntl == 3 && w.frame.field == field {
					t.Fatal("the two weave frames are the same field")
				}

				stride, height := ref.framebufferStride(), ref.displayHeight()
				if r.framebufferStride() != stride || r.displayHeight() != height {
					t.Fatalf("stride %d height %d, want %d and %d", r.framebufferStride(), r.displayHeight(), stride, height)
				}
				equalRenderBytes(t, "frame", r.framebuffer()[:stride*height], ref.framebuffer()[:stride*height])
			})
		}
	}
}

// TestProgressiveSiblingRecords holds the sibling record of a row with
// deinterlacing on: the chain is the row's, a scroll screen's sibling
// is at the other parity's display line, a rotation screen's sibling
// keeps the row's rotation state with its screen-over row at the other
// display line, and the composite's output row is the other parity's.
func TestProgressiveSiblingRecords(t *testing.T) {
	for _, name := range []string{"six layers LSMD3", "RBG0 cell coefficients"} {
		var scene benchVDP2Scene
		for _, s := range benchVDP2Scenes() {
			if s.name == name {
				scene = s
			}
		}
		v, fb := progressiveScene(scene, 0, true)
		v.BeginFrame()
		if v.recordsPerLine() != 2 || v.rowSlots() != 2 || v.maxRowSlots() != 2 {
			t.Fatalf("%s: records per line %d, row slots %d, bound %d", name, v.recordsPerLine(), v.rowSlots(), v.maxRowSlots())
		}
		if v.frame.fieldBootstrap {
			t.Fatalf("%s: field bootstrap with deinterlacing on", name)
		}
		y := 17
		v.BeginLine(y, fb)
		native, sib := v.rowRecord(y), v.siblingRecord(y)
		if sib.NSpans != native.NSpans || sib.Spans != native.Spans {
			t.Fatalf("%s: sibling chain %v, want %v", name, sib.Spans[:sib.NSpans], native.Spans[:native.NSpans])
		}
		field := v.frame.field
		for i := 0; i < int(native.NSpans); i++ {
			switch entry, layer := spanKind(native.Spans[i]); entry {
			case entryNBGCell, entryNBGBitmap:
				if native.NBG[layer].DispY != int32(2*y+field) || sib.NBG[layer].DispY != int32(2*y+1-field) {
					t.Fatalf("%s: NBG%d display lines %d and %d", name, layer, native.NBG[layer].DispY, sib.NBG[layer].DispY)
				}
			case entryRBGCell, entryRBGBitmap:
				n, s := native.RBG[layer-4], sib.RBG[layer-4]
				if n.DispY != int32(2*y+field) || s.DispY != int32(2*y+1-field) {
					t.Fatalf("%s: RBG display lines %d and %d", name, n.DispY, s.DispY)
				}
				s.DispY = n.DispY
				if s != n {
					t.Fatalf("%s: the sibling's rotation state differs from the row's", name)
				}
			case entryComposite:
				if native.Comp.FBRow != int32(2*y+field) || sib.Comp.FBRow != int32(2*y+1-field) {
					t.Fatalf("%s: output rows %d and %d", name, native.Comp.FBRow, sib.Comp.FBRow)
				}
				if sib.Comp.FieldBootstrap != 0 {
					t.Fatalf("%s: sibling composite bootstraps", name)
				}
			}
		}
	}
}

// TestExecEnhancedGPUProgressiveModeChange renders the six-layer
// scene through the enhanced GPU renderer on the executing device and
// through the enhanced software renderer, both with deinterlacing on
// at scale shift 1, over frames whose display starts without
// double-density interlace and switches it on, off, and on, and holds
// every frame equal: the slots a row takes change between the frames;
// the row stores of both renderers stay at one record per line until
// the first interlaced frame and grow to two there, with the frame
// before still submitted to the device; and the device's staging
// regions keep their fixed ranges across every later change.
func TestExecEnhancedGPUProgressiveModeChange(t *testing.T) {
	const shift = 1
	var scene benchVDP2Scene
	for _, s := range benchVDP2Scenes() {
		if s.name == "six layers" {
			scene = s
		}
	}
	parent, subs := ComputeSources()
	sw, swFB := progressiveScene(scene, shift, true)
	ref := newEnhancedRenderer(sw, NewVDP1(NewSCU()))
	defer ref.close()
	v, fb := progressiveScene(scene, shift, true)
	prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newEnhancedGPURenderer(v, NewVDP1(NewSCU()), prog, gpuBatchLines>>shift)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	interlaced := false
	for i, lsmd := range []uint16{0x0000, 0x00C0, 0x0000, 0x00C0} {
		tvmd := sw.regs[vdp2TVMD]&^0x00C0 | lsmd
		sw.Write(0, tvmd)
		v.Write(0, tvmd)
		renderFrame(sw, ref, swFB, nil)
		renderFrame(v, g, fb, nil)
		if err := g.err(); err != nil {
			t.Fatal(err)
		}
		want := 1 << shift
		if (lsmd != 0 && g.rowSlots != 2*want) || (lsmd == 0 && g.rowSlots != want) {
			t.Fatalf("frame %d: row slots %d", i, g.rowSlots)
		}
		interlaced = interlaced || lsmd != 0
		bound := want
		if interlaced {
			bound = 2 * want
		}
		if g.maxRowSlots != bound || len(g.subRows) != g.batchRows*bound || len(ref.rows) != bound {
			t.Fatalf("frame %d: row slot bound %d, records %d, software rows %d, want bound %d", i, g.maxRowSlots, len(g.subRows), len(ref.rows), bound)
		}
		if (sw.rows.siblings != nil) != interlaced {
			t.Fatalf("frame %d: sibling store allocated %v", i, sw.rows.siblings != nil)
		}
		stride, height := ref.framebufferStride(), ref.displayHeight()
		if g.framebufferStride() != stride || g.displayHeight() != height {
			t.Fatalf("frame %d: stride %d height %d, want %d and %d", i, g.framebufferStride(), g.displayHeight(), stride, height)
		}
		equalRenderBytes(t, fmt.Sprintf("frame %d", i), g.framebuffer()[:stride*height], ref.framebuffer()[:stride*height])
	}
}

// TestExecEnhancedGPUProgressiveFrameMatchesSoftware renders every
// bench scene under double-density interlace with deinterlacing on
// through the enhanced GPU renderer on the executing device and the
// enhanced software renderer at scale shifts 0 to 2, and holds the
// frames equal: the device takes two records per line, the sibling's
// slots placed and their inputs staged like the row's.
func TestExecEnhancedGPUProgressiveFrameMatchesSoftware(t *testing.T) {
	parent, subs := ComputeSources()
	for _, s := range benchVDP2Scenes() {
		for shift := uint32(0); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				sw, swFB := progressiveScene(s, shift, true)
				ref := newEnhancedRenderer(sw, NewVDP1(NewSCU()))
				defer ref.close()
				renderFrame(sw, ref, swFB, nil)

				v, fb := progressiveScene(s, shift, true)
				prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
				if err != nil {
					t.Fatal(err)
				}
				g, err := newEnhancedGPURenderer(v, NewVDP1(NewSCU()), prog, gpuBatchLines>>shift)
				if err != nil {
					t.Fatal(err)
				}
				defer g.close()
				renderFrame(v, g, fb, nil)
				if err := g.err(); err != nil {
					t.Fatal(err)
				}
				stride, height := ref.framebufferStride(), ref.displayHeight()
				if g.framebufferStride() != stride || g.displayHeight() != height {
					t.Fatalf("stride %d height %d, want %d and %d", g.framebufferStride(), g.displayHeight(), stride, height)
				}
				equalRenderBytes(t, "frame", g.framebuffer()[:stride*height], ref.framebuffer()[:stride*height])
			})
		}
	}
}
