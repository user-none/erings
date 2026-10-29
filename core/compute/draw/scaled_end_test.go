// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// scaleSprite is the scaled sprite of a native normal or scaled
// sprite at shift: the destination rectangle's corners at N * v and
// N * (v + 1) - 1, the clip likewise.
func scaleSprite(native DrawOp, shift uint32) DrawOp {
	n := int32(1) << shift
	x2, y2 := native.X[2], native.Y[2]
	if native.Kind == OpNormalSprite {
		x2, y2 = native.X[0]+native.CharW-1, native.Y[0]+native.CharH-1
	}
	op := native
	op.Kind = OpScaledSprite
	op.X[0], op.Y[0] = n*native.X[0], n*native.Y[0]
	op.X[2], op.Y[2] = n*(x2+1)-1, n*(y2+1)-1
	op.Pixel.ClipX = n*(native.Pixel.ClipX+1) - 1
	op.Pixel.ClipY = n*(native.Pixel.ClipY+1) - 1
	op.Pixel.FBWidth = native.Pixel.FBWidth << shift
	op.Pixel.FBHeight = native.Pixel.FBHeight << shift
	op.Pixel.ScaleShift = shift
	return op
}

// scaleQuad is the scaled four-vertex op of a native one at shift.
func scaleQuad(native DrawOp, shift uint32) DrawOp {
	n := int32(1) << shift
	op := native
	for k := range 4 {
		op.X[k] = n * native.X[k]
		op.Y[k] = n * native.Y[k]
	}
	op.Pixel.ClipX = n*(native.Pixel.ClipX+1) - 1
	op.Pixel.ClipY = n*(native.Pixel.ClipY+1) - 1
	op.Pixel.FBWidth = native.Pixel.FBWidth << shift
	op.Pixel.FBHeight = native.Pixel.FBHeight << shift
	op.Pixel.ScaleShift = shift
	return op
}

// spriteSrcX is the source column destination column x of a sprite
// row reads.
func spriteSrcX(op *DrawOp, x int32) int32 {
	if op.Kind == OpScaledSprite {
		return ScaledSrcX(op, x)
	}
	return NormalSrcX(op, x)
}

// TestScaledRowEnd holds ScaledRowEnd to a linear search: for every
// recorded end of a native row, the scaled row ends at its first
// column whose source column reaches the native row's ending column
// in the direction of travel, and a row whose record is its width
// does not end. A zero character width, which the command's size
// field can give, samples column 0 everywhere.
func TestScaledRowEnd(t *testing.T) {
	pixel := PixelParams{ClipX: 319, ClipY: 223, FBWidth: 512, FBHeight: 256}
	var natives []DrawOp
	for _, flags := range []uint32{0, OpFlagFlipH} {
		natives = append(natives, DrawOp{Kind: OpNormalSprite, CharW: 16, CharH: 4, ColorMode: 5, Flags: flags, Pixel: pixel})
		for _, cw := range []int32{16, 0} {
			for _, w := range []int32{3, 7, 16, 23, 64} {
				op := DrawOp{Kind: OpScaledSprite, CharW: cw, CharH: 4, ColorMode: 5, Flags: flags, Pixel: pixel}
				op.X = [4]int32{5, 0, 5 + w - 1, 0}
				op.Y = [4]int32{2, 0, 5, 0}
				natives = append(natives, op)
			}
		}
	}
	rec := make(compute.LineRecords, 64)
	for _, native := range natives {
		nativeWidth := SpriteRowWidth(&native)
		for shift := uint32(1); shift <= 3; shift++ {
			op := scaleSprite(native, shift)
			width := SpriteRowWidth(&op)
			flip := op.Flags&OpFlagFlipH != 0
			for end := int32(0); end <= nativeWidth; end++ {
				rec[1] = uint32(end)
				want := width
				if end < nativeWidth {
					c := spriteSrcX(&native, end)
					for x := range width {
						s := spriteSrcX(&op, x)
						if (!flip && s >= c) || (flip && s <= c) {
							want = x
							break
						}
					}
				}
				for k := range int32(1) << shift {
					row := int32(1)<<shift + k
					if got := ScaledRowEnd(rec, &op, row); got != want {
						t.Fatalf("kind %d width %d flags %#x shift %d end %d row %d: %d, want %d", native.Kind, nativeWidth, native.Flags, shift, end, row, got, want)
					}
				}
			}
		}
	}
}

// TestRecordedLineEnd checks the end a line's record gives it per
// kind: a native sprite row's recorded column, a scaled sprite row's
// through ScaledRowEnd, a distorted sprite's recorded step plus one,
// a pending record at or below zero, and no end for a kind with no
// records.
func TestRecordedLineEnd(t *testing.T) {
	pixel := PixelParams{ClipX: 319, ClipY: 223, FBWidth: 512, FBHeight: 256}
	rec := make(compute.LineRecords, 16)
	pending := int32(LineEndPending)
	for i := range rec {
		rec[i] = uint32(pending)
	}
	rec[4+2] = 5
	rec[8+1] = 7
	sprite := DrawOp{Kind: OpNormalSprite, RecordBase: 4, CharW: 16, CharH: 4, ColorMode: 5, Pixel: pixel}
	if got := RecordedLineEnd(rec, &sprite, 2); got != 5 {
		t.Errorf("sprite row 2: %d, want 5", got)
	}
	if got := RecordedLineEnd(rec, &sprite, 3); got > 0 {
		t.Errorf("sprite pending row: %d, want at or below 0", got)
	}
	scaled := scaleSprite(sprite, 1)
	for row := int32(4); row < 6; row++ {
		if got, want := RecordedLineEnd(rec, &scaled, row), ScaledRowEnd(rec, &scaled, row); got != want {
			t.Errorf("scaled row %d: %d, want %d", row, got, want)
		}
	}
	distorted := DrawOp{Kind: OpDistortedSprite, RecordBase: 8, CharW: 16, CharH: 4, ColorMode: 5, Pixel: pixel}
	if got := RecordedLineEnd(rec, &distorted, 1); got != 8 {
		t.Errorf("distorted line 1: %d, want 8", got)
	}
	if got := RecordedLineEnd(rec, &distorted, 2); got > 0 {
		t.Errorf("distorted pending line: %d, want at or below 0", got)
	}
	polygon := DrawOp{Kind: OpPolygon, RecordBase: 8, Pixel: pixel}
	if got := RecordedLineEnd(rec, &polygon, 1); got != -1 {
		t.Errorf("polygon: %d, want -1", got)
	}
}

// fbDot is the 16-bit pixel at (x, y) of fb.
func fbDot(fb compute.VDP1FB, op *DrawOp, x, y int32) uint16 {
	return fb.U16BE(uint32(y*op.Pixel.FBWidth+x) * 2)
}

// TestGenerateScaledEnds draws with generate at scale 2 a shrinking
// sprite row whose natively skipped columns hold end codes and an
// enlarging distorted sprite line that reads an end code column
// more often than the native line, both with opaque dots past their
// second end code, and checks that each draws every non-end-code dot
// up to the scaled end its native record gives and none past it: the
// sprite row's from ScaledRowEnd, and the connecting line's at the
// last sub-step in the block of a native step at or before the
// record. The connecting lines run along X from column 0 one dot per
// native step, so sub-step x is in the block of native step x >> 1.
func TestGenerateScaledEnds(t *testing.T) {
	vram := new(compute.VDP1VRAM)
	for i := range uint32(24) {
		dot := uint16(0x8001 + i)
		if i == 4 || i == 8 || i == 9 || i == 11 {
			dot = 0x7FFF
		}
		vram[i*2], vram[i*2+1] = uint8(dot>>8), uint8(dot)
	}
	pixel := PixelParams{ClipX: 511, ClipY: 255, FBWidth: 512, FBHeight: 256}
	const shift = 1

	sprite := DrawOp{Kind: OpScaledSprite, CharW: 16, CharH: 1, ColorMode: 5, Flags: OpFlagSPDOn, Pixel: pixel}
	sprite.X = [4]int32{0, 0, 7, 0}
	quad := DrawOp{Kind: OpDistortedSprite, CharW: 16, CharH: 1, ColorMode: 5, Flags: OpFlagSPDOn, Pixel: pixel}
	quad.X = [4]int32{0, 40, 40, 0}
	quad.Y = [4]int32{0, 0, 1, 1}

	for _, native := range []DrawOp{sprite, quad} {
		rec := make(compute.LineRecords, 64)
		fb := make(compute.VDP1FB, compute.VDP1FBSize)
		b := Buffers{VRAM: vram, FB: fb, Records: rec}
		var st DrawState
		Draw(&b, &native, &st)
		if !st.Done {
			t.Fatalf("kind %d: the walk did not finish", native.Kind)
		}

		op := scaleQuad(native, shift)
		if native.Kind == OpScaledSprite {
			op = scaleSprite(native, shift)
		}
		sfb := make(compute.VDP1FB, compute.VDP1FBSize<<(2*shift))
		sb := Buffers{VRAM: vram, FB: sfb, Records: rec}
		generate(&sb, &op, PassStore, 0, 1)

		for row := range OpLines(&op) {
			var end, width int32
			var srcOf func(x int32) int32
			if op.Kind == OpScaledSprite {
				end, width = ScaledRowEnd(rec, &op, row), SpriteRowWidth(&op)
				srcOf = func(x int32) int32 { return ScaledSrcX(&op, x) }
			} else {
				var ql QuadLine
				BeginQuadLine(&op, &ql, row)
				end, width = (int32(rec[row>>shift])+1)<<shift, ql.Len+1
				srcOf = func(x int32) int32 { return QuadSrcU(&op, &ql, quadU(&ql, x, shift)>>(16+shift)) }
			}
			if end >= width {
				t.Fatalf("kind %d row %d: the scaled line is not cut", native.Kind, row)
			}
			for x := range width {
				dot := uint16(vram[srcOf(x)*2])<<8 | uint16(vram[srcOf(x)*2+1])
				want := uint16(0)
				if x < end && dot != 0x7FFF {
					want = dot
				}
				if got := fbDot(sfb, &op, x, row); got != want {
					t.Fatalf("kind %d row %d column %d: %#04x, want %#04x (end %d)", native.Kind, row, x, got, want, end)
				}
			}
		}
	}
}

// TestGenerateScaledEntryBounds checks an entry's bounds at scale: a
// sprite entry's first native row starts at LineStart on each of its
// sub-rows and its last native row ends at LineEnd on each of its
// sub-rows, the rows between drawn whole and no other row drawn; a
// polyline entry's legs are not sub-lines and its bounds stay native,
// so LineStart bounds only the first leg, from the sub-dots of its
// native start dot, and the later legs draw whole. Each dot of a leg
// at scale claims its N by N square on its + sides.
func TestGenerateScaledEntryBounds(t *testing.T) {
	vram := new(compute.VDP1VRAM)
	for i := range uint32(16 * 4) {
		vram[i*2], vram[i*2+1] = 0x80, uint8(1+i)
	}
	pixel := PixelParams{ClipX: 511, ClipY: 255, FBWidth: 512, FBHeight: 256, ScaleShift: 1}
	const n = 2

	sprite := DrawOp{Kind: OpScaledSprite, CharW: 16, CharH: 4, ColorMode: 5, Flags: OpFlagECDOff, Pixel: pixel}
	sprite.X = [4]int32{0, 0, n*16 - 1, 0}
	sprite.Y = [4]int32{0, 0, n*4 - 1, 0}
	sprite.LineFrom, sprite.LineTo = n*1, n*3
	sprite.LineStart, sprite.LineEnd = n*2, n*5
	fb := make(compute.VDP1FB, compute.VDP1FBSize*n*n)
	rec := make(compute.LineRecords, 64)
	for i := range 4 {
		// The native rows did not end on end codes.
		rec[i] = 16
	}
	b := Buffers{VRAM: vram, FB: fb, Records: rec}
	generate(&b, &sprite, PassStore, 0, 1)
	for y := range int32(n * 4) {
		for x := range int32(n * 16) {
			drawn := fbDot(fb, &sprite, x, y) != 0
			want := y >= n*1 && y < n*3
			if y>>1 == 1 && x < n*2 {
				want = false
			}
			if y>>1 == 2 && x >= n*5 {
				want = false
			}
			if drawn != want {
				t.Fatalf("sprite row %d column %d: drawn %v, want %v", y, x, drawn, want)
			}
		}
	}

	poly := DrawOp{Kind: OpPolyline, Color: 0x8123, Pixel: pixel}
	poly.X = [4]int32{0, 40, 40, 0}
	poly.Y = [4]int32{0, 0, 40, 40}
	poly.LineFrom, poly.LineTo = 2, 4
	poly.LineStart = 10
	fb = make(compute.VDP1FB, compute.VDP1FBSize*n*n)
	b.FB = fb
	generate(&b, &poly, PassStore, 0, 1)
	for x := range int32(43) {
		// Leg 2, C to D, runs from x 40 to 0 on rows 40 and 41, native
		// dot k at native x 20 - k, so native dots 10 onward hold the
		// steps at x 21 and below, whose squares reach x 22; leg 3, D
		// to A, runs from row 40 to 0 on columns 0 and 1, its first
		// dot's square reaching row 41, whole.
		for y := int32(40); y <= 41; y++ {
			if drawn, want := fbDot(fb, &poly, x, y) != 0, x <= 22; drawn != want {
				t.Fatalf("polyline leg 2 row %d column %d: drawn %v, want %v", y, x, drawn, want)
			}
		}
		for c := int32(0); c <= 1; c++ {
			if x <= 41 && fbDot(fb, &poly, c, x) == 0 {
				t.Fatalf("polyline leg 3 row %d column %d not drawn", x, c)
			}
		}
	}
	for x := range int32(43) {
		if fbDot(fb, &poly, x, 0) != 0 && x > 1 {
			t.Fatalf("polyline leg 0 drew column %d", x)
		}
	}
}

// TestCopyFBPagesScaled checks the page copy at every scale, both
// pixel sizes, and both buffer layouts: each staged byte lands on its
// lane of every pixel of its native pixel's N by N block and nowhere
// else, the block's row being the native row's in a native buffer and
// both doubled rows 2r and 2r + 1 in a doubled one.
func TestCopyFBPagesScaled(t *testing.T) {
	for _, is8 := range []bool{false, true} {
		fbWidth := uint32(512)
		size := uint32(2)
		if is8 {
			fbWidth, size = 1024, 1
		}
		for shift := uint32(0); shift <= 3; shift++ {
			for doubled := uint32(0); doubled <= 1; doubled++ {
				writes := FBWrites{Store: new(compute.VDP1PageStore), Masks: new(compute.MaskStore), Table: new(compute.PageTable)}
				writes.Table[0] = 37
				written := []uint32{0, 1, 2, 1023, 777}
				for _, b := range written {
					writes.Store[b] = uint8(0x40 + b)
					writes.Masks[b/32] |= 1 << (b & 31)
				}
				fb := make(compute.VDP1FB, compute.VDP1FBSize<<doubled<<(2*shift))
				p := PageCopyParams{ScaleShift: shift, FBWidth: fbWidth, Is8bpp: is8, Doubled: doubled}
				for i := range uint32(compute.PageBytes) {
					CopyFBPages(fb, &writes, &p, i)
				}
				want := make([]byte, len(fb))
				n := uint32(1) << shift
				for _, b := range written {
					addr := 37*compute.PageBytes + b
					pixel, lane := addr/size, addr%size
					for q := range 1 + doubled {
						x, y := pixel%fbWidth*n, (pixel/fbWidth<<doubled+q)*n
						for dy := range n {
							for dx := range n {
								want[((y+dy)*fbWidth*n+x+dx)*size+lane] = uint8(0x40 + b)
							}
						}
					}
				}
				for i := range fb {
					if fb[i] != want[i] {
						t.Fatalf("8bpp %v shift %d doubled %d: byte %d = %#02x, want %#02x", is8, shift, doubled, i, fb[i], want[i])
					}
				}
			}
		}
	}
}
