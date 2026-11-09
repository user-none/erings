// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestPreClipDetectionPerDrawnLine verifies every drawing form charges
// the pre-clipping detection cost on each line it draws when the
// command performs pre-clipping (CMDPMOD Pclp = 0) and nothing extra
// when it does not (VDP1 User's Manual Supplement Sections 1.2 and
// 2.2): the timing walk with OpFlagPreClip set costs PreClipLineCycles
// more per drawn line. Scaled sprite rows outside the clip Y range
// keep their off-screen cost and pay no detection on top.
func TestPreClipDetectionPerDrawnLine(t *testing.T) {
	pixel := PixelParams{ClipX: 319, ClipY: 223, FBWidth: 512, FBHeight: 256}
	cases := []struct {
		name  string
		op    DrawOp
		lines int32
	}{
		{"normal sprite", DrawOp{Kind: OpNormalSprite, CharW: 8, CharH: 4, ColorMode: 5,
			X: [4]int32{10}, Y: [4]int32{10}}, 4},
		{"scaled sprite", DrawOp{Kind: OpScaledSprite, CharW: 8, CharH: 4, ColorMode: 5,
			X: [4]int32{10, 0, 25, 0}, Y: [4]int32{10, 0, 19, 0}}, 10},
		{"scaled sprite rows above clip", DrawOp{Kind: OpScaledSprite, CharW: 8, CharH: 4, ColorMode: 5,
			X: [4]int32{10, 0, 25, 0}, Y: [4]int32{-5, 0, 4, 0}}, 5},
		{"distorted sprite", DrawOp{Kind: OpDistortedSprite, CharW: 8, CharH: 8, ColorMode: 5,
			X: [4]int32{10, 17, 17, 10}, Y: [4]int32{10, 10, 17, 17}}, 8},
		{"polygon", DrawOp{Kind: OpPolygon,
			X: [4]int32{10, 17, 17, 10}, Y: [4]int32{10, 10, 17, 17}}, 8},
		{"line", DrawOp{Kind: OpLine,
			X: [4]int32{10, 30}, Y: [4]int32{10, 20}}, 1},
		{"polyline", DrawOp{Kind: OpPolyline,
			X: [4]int32{10, 17, 17, 10}, Y: [4]int32{10, 10, 17, 17}}, 4},
	}
	vram := new(compute.VDP1VRAM)
	records := make(compute.LineRecords, 256)
	walk := func(op DrawOp) int32 {
		b := Buffers{VRAM: vram, Records: records}
		st := DrawState{PixCycles: 1, Timing: true}
		Draw(&b, &op, &st)
		if !st.Done {
			t.Fatalf("%v: walk did not complete", op.Kind)
		}
		return st.Cycles
	}
	for _, c := range cases {
		op := c.op
		op.Pixel = pixel
		op.Flags = OpFlagECDOff
		off := walk(op)
		op.Flags |= OpFlagPreClip
		on := walk(op)
		if got, want := on-off, c.lines*PreClipLineCycles; got != want {
			t.Errorf("%s: pre-clipping adds %d cycles, want %d (%d lines x %d)", c.name, got, want, c.lines, PreClipLineCycles)
		}
	}
}

// TestEdgeAtMatchesStepping pins the closed forms a line's setup uses
// for the edge positions against stepping the DDA edges, over edges
// of every slope class and step count, so the software schedule and
// a GPU thread compute the same endpoints.
func TestEdgeAtMatchesStepping(t *testing.T) {
	edges := [][4]int32{{0, 0, 100, 37}, {10, 20, -90, 41}, {5, 5, 5, 5}, {-300, 200, 250, -180}, {7, -3, 8, 500}, {0, 0, -1, -1}}
	for _, e := range edges {
		for steps := int32(0); steps < 600; steps += 7 {
			left := NewDDAEdge(e[0], e[1], e[2], e[3], steps)
			right := NewRightDDAEdge(e[0], e[1], e[2], e[3], steps)
			lx, ly := left.X, left.Y
			rx, ry := right.X, right.Y
			for i := int32(0); i <= steps; i++ {
				x, y := EdgeAt(e[0], e[1], e[2], e[3], steps, i)
				if sx, sy := int32(lx>>DDAFraction), int32(ly>>DDAFraction); x != sx || y != sy {
					t.Fatalf("A-D edge %v steps %d step %d: closed form (%d,%d), stepped (%d,%d)", e, steps, i, x, y, sx, sy)
				}
				x, y = RightEdgeAt(e[0], e[1], e[2], e[3], steps, i)
				if sx, sy := int32(rx>>DDAFraction), int32(ry>>DDAFraction); x != sx || y != sy {
					t.Fatalf("B-C edge %v steps %d step %d: closed form (%d,%d), stepped (%d,%d)", e, steps, i, x, y, sx, sy)
				}
				lx += left.DX
				ly += left.DY
				rx += right.DX
				ry += right.DY
			}
		}
	}
}

// TestQuadPosRounding pins a connecting line's dots to the nearest
// dot of its exact position, a position half way between two dots
// taking the dot toward the line's start: the last line of a
// point-sided polygon from (185,136) to (197,147) has y 141.5 at
// step 6, and one from (166,163) to (154,147) has x 164.5 at step 2.
func TestQuadPosRounding(t *testing.T) {
	cases := []struct {
		a, c   [2]int32
		j      int32
		px, py int32
	}{
		{[2]int32{185, 136}, [2]int32{197, 147}, 6, 191, 141},
		{[2]int32{185, 136}, [2]int32{197, 147}, 7, 192, 142},
		{[2]int32{166, 163}, [2]int32{154, 147}, 2, 165, 161},
		{[2]int32{166, 163}, [2]int32{154, 147}, 3, 164, 160},
	}
	for _, c := range cases {
		op := DrawOp{Kind: OpPolygon}
		op.Pixel = PixelParams{ClipX: 351, ClipY: 223, FBWidth: 512, FBHeight: 256}
		op.X = [4]int32{c.a[0], c.a[0], c.c[0], c.a[0]}
		op.Y = [4]int32{c.a[1], c.a[1], c.c[1], c.a[1]}
		var st QuadLine
		BeginQuadLine(&op, &st, QuadSteps(&op))
		if px, py := QuadPos(&st, c.j); px != c.px || py != c.py {
			t.Errorf("line %v-%v step %d: (%d,%d), want (%d,%d)", c.a, c.c, c.j, px, py, c.px, c.py)
		}
	}
}

// TestPointSidedPolygonFans pins the drawing of six polygons whose A-D
// edge is a point, two blue, two red, and two purple drawn last,
// over the dots they share: the diamond of the NiGHTS dream diary
// menu, whose visible dots were matched against the console's output.
// The fans exercise the connecting lines' rounding and the hole fill
// corner in every drawing direction.
func TestPointSidedPolygonFans(t *testing.T) {
	type fan struct {
		color   uint32
		a, b, c [2]int32
	}
	fans := []fan{
		{0xFE00, [2]int32{184, 132}, [2]int32{185, 136}, [2]int32{197, 147}},
		{0xFE00, [2]int32{166, 136}, [2]int32{167, 132}, [2]int32{154, 147}},
		{0x801F, [2]int32{186, 153}, [2]int32{185, 163}, [2]int32{197, 147}},
		{0x801F, [2]int32{166, 163}, [2]int32{165, 153}, [2]int32{154, 147}},
		{0xFC10, [2]int32{185, 136}, [2]int32{186, 153}, [2]int32{197, 147}},
		{0xFC10, [2]int32{165, 153}, [2]int32{166, 136}, [2]int32{154, 147}},
	}
	want := []string{
		".............B................BB............",
		"............BB................BBB...........",
		"...........BB.................BBBB..........",
		"..........BBB..................BBB..........",
		"..........BBP..................PPBB.........",
		".........BBPP..................PPPBB........",
		"........BBPPP..................PPPPBB.......",
		".......BBPPPP..................PPPPPBB......",
		"......BBPPPPP..................PPPPPPBB.....",
		".....BBPPPPPP..................PPPPPPPPB....",
		"....BPPPPPPPP..................PPPPPPPPPB...",
		"...BPPPPPPPPP..................PPPPPPPPPP...",
		"...PPPPPPPPPP..................PPPPPPPPPPP..",
		"..PPPPPPPPPP....................PPPPPPPPPPP.",
		".PPPPPPPPPPP....................PPPPPPPPPPPP",
		"PPPPPPPPPPPP....................PPPPPPPPPPPP",
		"PPPPPPPPPPPP....................PPPPPPPPPPP.",
		".RPPPPPPPPPP....................PPPPPPPPPRR.",
		"..RRPPPPPPPP....................PPPPPPPRRR..",
		"..RRRRPPPPPP....................PPPPPRRRR...",
		"...RRRRRPPPP....................PPPRRRRR....",
		"....RRRRRRPP....................PRRRRRRR....",
		".....RRRRRRR....................RRRRRRR.....",
		".....RRRRRRR....................RRRRRR......",
		"......RRRRRR....................RRRRR.......",
		".......RRRRR....................RRRRR.......",
		"........RRRRR...................RRRR........",
		"........RRRRR..................RRRR.........",
		".........RRRR..................RRR..........",
		"..........RRR..................RRR..........",
		"...........RR..................RR...........",
		"...........RR..................R............",
	}
	var vram compute.VDP1VRAM
	fb := make(compute.VDP1FB, compute.VDP1FBSize)
	b := Buffers{VRAM: &vram, FB: fb}
	for _, f := range fans {
		op := DrawOp{Kind: OpPolygon, Color: f.color}
		op.Pixel = PixelParams{ClipX: 351, ClipY: 223, FBWidth: 512, FBHeight: 256}
		op.X = [4]int32{f.a[0], f.b[0], f.c[0], f.a[0]}
		op.Y = [4]int32{f.a[1], f.b[1], f.c[1], f.a[1]}
		var st DrawState
		Draw(&b, &op, &st)
		if !st.Done {
			t.Fatalf("fan %v not drawn in one unbounded call", f)
		}
	}
	for row, line := range want {
		y := int32(132 + row)
		got := ""
		for x := int32(154); x < 198; x++ {
			c := "."
			switch fb.U16BE(uint32(y*512+x) * 2) {
			case 0xFE00:
				c = "B"
			case 0x801F:
				c = "R"
			case 0xFC10:
				c = "P"
			}
			got += c
		}
		if got != line {
			t.Errorf("row %d:\n got  %s\n want %s", y, got, line)
		}
	}
}

// TestGouraudAdvanceMatchesStepping pins Advance(i) against i Steps.
func TestGouraudAdvanceMatchesStepping(t *testing.T) {
	pairs := [][2]uint32{{0x4210, 0x421F}, {0x7C10, 0x0210}, {0x0000, 0x7FFF}, {0x7FFF, 0x0000}, {0x1234, 0x1234}}
	for _, p := range pairs {
		for n := int32(1); n < 300; n += 13 {
			s := NewGouraudStepper(p[0], p[1], n, 0)
			for i := int32(0); i < n; i++ {
				a := NewGouraudStepper(p[0], p[1], n, 0)
				a.Advance(i)
				if a != s {
					t.Fatalf("pair %#x n %d step %d: Advance %+v, stepped %+v", p, n, i, a, s)
				}
				s.Step()
			}
		}
	}
}
