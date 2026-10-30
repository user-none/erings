// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"fmt"
	"testing"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/internal/hostgpu"
)

// enhancedGPUVDP1Matches drives the scene's frames through the
// enhanced software renderer and through the enhanced GPU renderer
// open makes, at the shift, each over its own VDP1 and spriteVDP2,
// then the next frame's change and its take, and holds the device
// pair equal to the software renderer's pair and the last frame equal
// to its frame. A host without the device skips.
func enhancedGPUVDP1Matches(t *testing.T, s benchVDP1Scene, shift uint32, open func(v2 *VDP2, v1 *VDP1) (*enhancedGPURenderer, error)) {
	t.Helper()
	v1, v2 := s.build(), spriteVDP2()
	v2.setEnhance(enhanceConfig{shift: shift})
	g, err := open(v2, v1)
	if err != nil {
		t.Skip(err)
	}
	defer g.close()
	e, _ := enhancedVDP1Drive(t, s, shift)
	defer e.close()
	d := &vdp1FrameDrive{v1: v1, v2: v2, g: g}
	d.frames(t)
	vdp1SubmitChange(t, d, g.gpuRenderer)
	for i := range g.vdp1FB {
		if got, want := deviceBytes(t, g.gpuRenderer, g.vdp1FB[i]), []byte(e.vdp1FB[i]); !bytes.Equal(got, want) {
			t.Fatalf("physical buffer %d differs from the enhanced software renderer's at byte %d", i, firstDiff(got, want))
		}
	}
	stride, height := e.framebufferStride(), e.displayHeight()
	if g.framebufferStride() != stride || g.displayHeight() != height {
		t.Fatalf("stride %d height %d, want %d and %d", g.framebufferStride(), g.displayHeight(), stride, height)
	}
	equalRenderBytes(t, "frame", g.framebuffer()[:stride*height], e.framebuffer()[:stride*height])
}

// TestExecEnhancedGPUVDP1MatchesEnhancedSoftware drives every
// enhanced VDP1 scene, every kind of entry among them, through the
// enhanced GPU renderer on the executing device and through the
// enhanced software renderer at scale shifts 0 to 2, and holds the
// device pair and the frame to the software renderer's: every entry
// scaled and drawn in rounds, a destination-reading entry in pieces
// of its native lines (a polygon larger than the capacity, and a
// sprite split across ticks), the CPU writes copied to their blocks,
// the whole CPU pair expanded at the first take, and the composite
// reading the scaled pair.
func TestExecEnhancedGPUVDP1MatchesEnhancedSoftware(t *testing.T) {
	parent, subs := ComputeSources()
	for _, s := range enhancedVDP1Scenes(t) {
		for shift := uint32(0); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				enhancedGPUVDP1Matches(t, s.benchVDP1Scene, shift, func(v2 *VDP2, v1 *VDP1) (*enhancedGPURenderer, error) {
					prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
					if err != nil {
						t.Fatal(err)
					}
					return newEnhancedGPURenderer(v2, v1, prog, gpuBatchLines>>shift)
				})
			})
		}
	}
}

// TestEnhancedGPUVDP1MatchesEnhancedSoftware is the same drive on the
// platform's device, skipped where there is none.
func TestEnhancedGPUVDP1MatchesEnhancedSoftware(t *testing.T) {
	for _, s := range enhancedVDP1Scenes(t) {
		for shift := uint32(0); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				enhancedGPUVDP1Matches(t, s.benchVDP1Scene, shift, func(v2 *VDP2, v1 *VDP1) (*enhancedGPURenderer, error) {
					return openEnhancedGPURenderer(v2, v1, defaultBackend())
				})
			})
		}
	}
}

// TestEnhancedGPUVDP1StateLoadMatchesWalk is
// TestEnhancedVDP1StateLoadMatchesWalk with the enhanced GPU renderer
// at scale shift 1 on the executing device installed: the load marks
// both CPU buffers written, so the next take's copy expands the
// restored buffers whole into the device pair, and the restored
// command's entries follow. Both halves of the device pair are held
// equal to the CPU pair expanded to blocks.
func TestEnhancedGPUVDP1StateLoadMatchesWalk(t *testing.T) {
	parent, subs := ComputeSources()
	e := NewEmulator()
	v1 := e.vdp1
	stateLoadList(v1)
	const shift = 1
	prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	e.renderer.close()
	e.vdp2.setEnhance(enhanceConfig{shift: shift})
	g, err := newEnhancedGPURenderer(e.vdp2, v1, prog, gpuBatchLines>>shift)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	e.renderer = g
	d := &vdp1FrameDrive{v1: v1, v2: e.vdp2, g: g}
	driveStateLoad(t, e, d)
	vdp1SubmitChange(t, d, g.gpuRenderer)
	for i := range g.vdp1FB {
		want := expandFB(v1.physicalFB(uint32(i)), v1.fbWidth(), v1.is8bpp(), shift)
		if got := deviceBytes(t, g.gpuRenderer, g.vdp1FB[i]); !bytes.Equal(got, want) {
			t.Fatalf("physical buffer %d differs from the walk's expanded to blocks at byte %d", i, firstDiff(got, want))
		}
	}
}

// TestGPUStepPieces puts half-transparent entries of every form
// through the enhanced GPU renderer's piece split with the round
// capacity lowered to two native steps' bound, so every native line
// goes in pieces of its steps, at scale shifts 1 to 3: a distorted
// sprite and a scaled sprite whose 256-color character ends half its
// rows on end codes, a polygon, a polyline, a line, and a polygon
// whose side edges are points. The pieces drawn in order in the store
// pass equal the whole scaled entry drawn so, in one color over a
// background it blends with, so a sub-dot drawn by no piece, by two,
// or past a record's end code cut changes the result; each piece
// appends at most its bound (gpuStepFragments) in the tag pass, and
// each record's pieces at most the capacity.
func TestGPUStepPieces(t *testing.T) {
	const fbW, fbH, bg, charW, charH, ec = 128, 64, 0x8000, 32, 8, 0xFF
	vram := new(compute.VDP1VRAM)
	for r := range charH {
		for c := range charW {
			vram[r*charW+c] = 0x10
		}
		if r%2 == 0 {
			vram[r*charW+9], vram[r*charW+20] = ec, ec
		}
	}
	quad := [2][4]int32{{10, 90, 80, 8}, {6, 10, 50, 40}}
	forms := []struct {
		name string
		kind uint32
		x, y [4]int32
	}{
		{"distorted sprite", draw.OpDistortedSprite, quad[0], quad[1]},
		{"scaled sprite", draw.OpScaledSprite, [4]int32{4, 0, 110, 0}, [4]int32{2, 0, 9, 0}},
		{"polygon", draw.OpPolygon, quad[0], quad[1]},
		{"polyline", draw.OpPolyline, quad[0], quad[1]},
		{"line", draw.OpLine, [4]int32{5, 120, 0, 0}, [4]int32{5, 45, 0, 0}},
		{"point-sided polygon", draw.OpPolygon, [4]int32{10, 100, 100, 10}, [4]int32{10, 30, 30, 10}},
	}
	parent, subs := ComputeSources()
	rs := newRoundState()
	for _, f := range forms {
		op := draw.DrawOp{Kind: f.kind, CharW: charW, CharH: charH, ColorMode: 4, Color: 0x0100 | 0x7C1F, X: f.x, Y: f.y}
		op.Pixel = draw.PixelParams{CC: 3, ClipX: fbW - 1, ClipY: fbH - 1, FBWidth: fbW, FBHeight: fbH}
		v1 := NewVDP1(NewSCU())
		v1.lineRecords = walkRecords(vram, make(compute.VDP1FB, fbW*fbH*2), &op)
		for shift := uint32(1); shift <= 3; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", f.name, shift), func(t *testing.T) {
				prog, err := hostgpu.Build(&testDevice{}, parent, subs...)
				if err != nil {
					t.Fatal(err)
				}
				v2 := NewVDP2(NewSCU())
				v2.setEnhance(enhanceConfig{shift: shift})
				g, err := newEnhancedGPURenderer(v2, v1, prog, 1)
				if err != nil {
					t.Fatal(err)
				}
				defer g.close()
				g.fragmentCap = gpuStepFragments(2, shift)
				var dev draw.DrawOp
				scaleOp(&dev, &op, shift)
				class := opClass(&dev)
				s := &g.staging[g.cur]
				rec := g.newVDP1Line(s)
				g.putPieces(s, &rec, &op, &dev, class)
				g.pushVDP1Line(s, &rec)
				for _, r := range s.lines {
					if r.frags > g.fragmentCap {
						t.Fatalf("a record holds %d fragments, over the capacity %d", r.frags, g.fragmentCap)
					}
				}
				n := uint32(1) << shift
				pieces := make(compute.VDP1FB, fbW*fbH*2<<(2*shift))
				whole := make(compute.VDP1FB, len(pieces))
				fillFB(pieces, bg)
				fillFB(whole, bg)
				b := draw.Buffers{VRAM: vram, FB: pieces, Round: rs, Records: v1.lineRecords}
				count := s.opSlots[class]
				for slot := range count {
					piece := *(*draw.DrawOp)(unsafe.Pointer(&s.ops[class].Bytes()[slot*int(gpuOpSize)]))
					steps := piece.LineEnd - piece.LineStart
					if piece.Kind == draw.OpScaledSprite {
						steps = (steps + n - 1) / n
					}
					rs.Pool[draw.FragmentCounter] = 0
					draw.GenerateTag(&b, &piece, 0, 1, 0)
					if got, bound := rs.Pool[draw.FragmentCounter]/draw.FragmentWords, gpuStepFragments(steps, shift); got > bound {
						t.Fatalf("piece %d (line %d, steps %d to %d): %d fragments, over its bound %d", slot, piece.LineFrom, piece.LineStart, piece.LineEnd, got, bound)
					}
					draw.Generate(&b, &piece, 0, 1)
				}
				if count <= int(draw.OpLines(&op)) {
					t.Fatalf("%d pieces over %d native lines, want the lines split by their steps", count, draw.OpLines(&op))
				}
				b.FB = whole
				draw.Generate(&b, &dev, 0, 1)
				if !bytes.Equal(pieces, whole) {
					t.Fatalf("the pieces differ from the whole entry at byte %d", firstDiff(pieces, whole))
				}
			})
		}
	}
}

// TestGPULineFragments draws every native line of destination-reading
// entries of every form as a piece of its own, scaled, in the tag
// pass at scale shifts 0 to 3, and holds the fragments each piece
// appends to the bound the renderer splits entries by
// (gpuLineFragments of the scaled entry): normal and scaled sprites,
// distorted sprites and polygons, a polygon whose connecting lines
// and step-setting edge share their major axis, a polyline, a line,
// and a polygon whose side
// edges are points. Every dot lies inside the clip, so every step
// appends.
func TestGPULineFragments(t *testing.T) {
	const fbW, fbH = 256, 128
	vram := new(compute.VDP1VRAM)
	for i := range vram {
		vram[i] = 0x80 | uint8(i)
	}
	quad := [2][4]int32{{10, 70, 60, 8}, {6, 10, 50, 40}}
	forms := []struct {
		name string
		kind uint32
		x, y [4]int32
	}{
		{"normal sprite", draw.OpNormalSprite, [4]int32{20, 0, 0, 0}, [4]int32{9, 0, 0, 0}},
		{"scaled sprite", draw.OpScaledSprite, [4]int32{20, 0, 90, 0}, [4]int32{9, 0, 50, 0}},
		{"distorted sprite", draw.OpDistortedSprite, quad[0], quad[1]},
		{"polygon", draw.OpPolygon, quad[0], quad[1]},
		{"polygon along one axis", draw.OpPolygon, [4]int32{0, 10, 110, 100}, [4]int32{0, 0, 5, 5}},
		{"polyline", draw.OpPolyline, quad[0], quad[1]},
		{"line", draw.OpLine, [4]int32{5, 100, 0, 0}, [4]int32{5, 45, 0, 0}},
		{"point-sided polygon", draw.OpPolygon, [4]int32{10, 80, 80, 10}, [4]int32{10, 30, 30, 10}},
	}
	rs := newRoundState()
	for _, f := range forms {
		op := draw.DrawOp{Kind: f.kind, CharW: 16, CharH: 8, ColorMode: 5, X: f.x, Y: f.y}
		op.Pixel = draw.PixelParams{CC: 3, ClipX: fbW - 1, ClipY: fbH - 1, FBWidth: fbW, FBHeight: fbH}
		rec := walkRecords(vram, make(compute.VDP1FB, fbW*fbH*2), &op)
		lines := uint32(draw.OpLines(&op))
		for shift := uint32(0); shift <= 3; shift++ {
			var whole, scaled draw.DrawOp
			scaleOp(&whole, &op, shift)
			bound := gpuLineFragments(&whole, shift)
			fb := make(compute.VDP1FB, fbW*fbH*2<<(2*shift))
			b := draw.Buffers{VRAM: vram, FB: fb, Round: rs, Records: rec}
			most := uint32(0)
			for l := range lines {
				piece := op
				piece.LineFrom, piece.LineTo = l, l+1
				scaleOp(&scaled, &piece, shift)
				rs.Pool[draw.FragmentCounter] = 0
				draw.GenerateTag(&b, &scaled, 0, 1, 0)
				n := rs.Pool[draw.FragmentCounter] / draw.FragmentWords
				if n > bound {
					t.Fatalf("%s shift %d line %d: %d fragments, over the bound %d", f.name, shift, l, n, bound)
				}
				most = max(most, n)
			}
			if most == 0 {
				t.Fatalf("%s shift %d: no fragment appended", f.name, shift)
			}
		}
	}
}
