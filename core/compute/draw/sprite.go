// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// SpriteRow is one row of a normal or scaled sprite: the next
// destination column, the column the row ends at, the row's draw
// order key, and the end code and source tracking. A dot's source
// column follows from its destination column, so a span call derives
// its state from X.
type SpriteRow struct {
	X        int32          // next destination column
	End      int32          // columns [X, End) remain; cut to the column of the end code that ended the row
	Key      uint32         // the row's draw order key (LineKey)
	EC       EndCodeCounter // end codes seen on the row, counted per source column
	SrcReadX int32          // scaled: last source column read, -1 before the first
}

// OffscreenRowCycles is the cycles a scaled sprite row outside the
// drawing area costs: the row is skipped without reading dots.
const OffscreenRowCycles = 5

// SpriteRows is the rows of a normal or scaled sprite: the character
// height, or the destination rectangle's height.
func SpriteRows(op *DrawOp) int32 {
	if op.Kind == OpScaledSprite {
		return op.Y[2] - op.Y[0] + 1
	}
	return op.CharH
}

// SpriteRowWidth is the destination columns of a row.
func SpriteRowWidth(op *DrawOp) int32 {
	if op.Kind == OpScaledSprite {
		return op.X[2] - op.X[0] + 1
	}
	return op.CharW
}

// spriteRowBase is the character row a sprite row reads: the row
// itself for a normal sprite, the midpoint-sampled source row for a
// scaled one, flipped when the command flips vertically.
func spriteRowBase(op *DrawOp, row int32) uint32 {
	srcY := row
	if op.Kind == OpScaledSprite {
		srcY = ((2*row + 1) * op.CharH) / (2 * SpriteRows(op))
	}
	if op.Flags&OpFlagFlipV != 0 {
		srcY = op.CharH - 1 - srcY
	}
	return op.CharAddr + uint32(srcY*RowStride(op.CharW, op.ColorMode))
}

// NormalSrcX is the source column destination column x of a normal
// sprite row reads: the column itself, from the right when the
// command flips horizontally.
func NormalSrcX(op *DrawOp, x int32) int32 {
	if op.Flags&OpFlagFlipH != 0 {
		return op.CharW - 1 - x
	}
	return x
}

// ScaledSrcX is the source column destination column x of a scaled
// sprite row reads: the source position at the destination dot's
// center (midpoint sampling), from the right when the command flips
// horizontally, and snapped to one parity under high speed shrink
// (FBCR EOS selects the odd columns).
func ScaledSrcX(op *DrawOp, x int32) int32 {
	srcX := ((2*x + 1) * op.CharW) / (2 * SpriteRowWidth(op))
	if op.Flags&OpFlagFlipH != 0 {
		srcX = op.CharW - 1 - srcX
	}
	if op.Flags&OpFlagHSS != 0 {
		if op.Flags&OpFlagHSSOdd != 0 {
			return srcX | 1
		}
		return srcX &^ 1
	}
	return srcX
}

// SpriteGouraud is the Gouraud value of destination column x of row
// row of a sprite: the table's four corners interpolated along the
// top and bottom edges over the row's width, then between them over
// the rows.
func SpriteGouraud(op *DrawOp, x, row int32) uint32 {
	w := SpriteRowWidth(op)
	top := LerpGouraud(op.Gouraud[0], op.Gouraud[1], x, w)
	bot := LerpGouraud(op.Gouraud[3], op.Gouraud[2], x, w)
	return LerpGouraud(top, bot, row, SpriteRows(op))
}

// SpriteRowChecksECD reports whether a sprite's rows check end codes:
// not when the command disables them, and not on a scaled sprite
// under high speed shrink, which disables end codes on the row it
// samples by parity (VDP1 manual Sec 6.3); the walk sets the flag
// only when the row shrinks.
func SpriteRowChecksECD(op *DrawOp) bool {
	return op.Flags&(OpFlagECDOff|OpFlagHSS) == 0
}

// BeginSpriteRow starts row of op and returns the cycles charged. A
// scaled sprite row outside the drawing area is complete on return,
// its end at column 0 so no column of it draws. A row that draws pays
// the pre-clipping detection cost when pre-clipping is performed.
func BeginSpriteRow(op *DrawOp, st *SpriteRow, row int32) int32 {
	st.X = 0
	st.End = SpriteRowWidth(op)
	st.EC.Reset()
	st.SrcReadX = -1
	if op.Kind == OpScaledSprite {
		fbY := op.Y[0] + row
		if fbY < 0 || fbY > op.Pixel.ClipY {
			st.End = 0
			return OffscreenRowCycles
		}
	}
	return PreClipDetectCycles(op)
}

// SpriteRowSpan draws columns [st.X, x1) of row of op, x1 clamped to
// the row's end, or to the first YieldChunk boundary at or past the
// cycle budget, and returns the cycles charged. Normal sprites read
// the character dot for dot: one cycle per end code or transparent
// dot, pixCycles per dot drawn. Scaled sprites midpoint-sample the
// character (each destination dot reads the source position at its
// center), advance the source read by the distance between samples,
// halved under high speed shrink, and charge the larger of that
// advance and pixCycles.
func SpriteRowSpan(vram *compute.VDP1VRAM, fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, st *SpriteRow, row, x1, budget int32, pixCycles int32) int32 {
	if x1 > st.End {
		x1 = st.End
	}
	if op.Kind == OpScaledSprite {
		return scaledRowSpan(vram, fb, rs, op, st, row, x1, budget, pixCycles)
	}
	return normalRowSpan(vram, fb, rs, op, st, row, x1, budget, pixCycles)
}

func normalRowSpan(vram *compute.VDP1VRAM, fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, st *SpriteRow, row, x1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	rowBase := spriteRowBase(op, row)
	fbY := op.Y[0] + row
	checkECD := SpriteRowChecksECD(op)
	endsOnCount := EndsOnCount(&op.Pixel)
	for st.X < x1 {
		xc := st.X + YieldChunk
		if xc > x1 {
			xc = x1
		}
		for st.X < xc {
			readX := NormalSrcX(op, st.X)
			dot := ReadRowDot(vram, rowBase, readX, op.ColorMode)
			if !TexelDraws(dot, op.ColorMode, op.Flags&OpFlagSPDOn, checkECD, &st.EC, readX) {
				if endsOnCount && st.EC.Ended() {
					// The row ends at this column, its cutoff.
					st.End = st.X
					x1 = st.X
					break
				}
				st.X++
				cycles += stepCycles(1, pixCycles, false)
				continue
			}

			pixel := DotToPixel(vram, dot, op.Color, op.ColorMode)
			gouraud := uint32(0)
			if op.Pixel.CC >= 4 {
				gouraud = SpriteGouraud(op, st.X, row)
			}
			PutDot(fb, rs, &op.Pixel, op.X[0]+st.X, fbY, pixel, gouraud, st.Key)

			st.X++
			cycles += stepCycles(1, pixCycles, true)
		}
		if cycles >= budget {
			break
		}
	}
	return cycles
}

func scaledRowSpan(vram *compute.VDP1VRAM, fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, st *SpriteRow, row, x1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	rowBase := spriteRowBase(op, row)
	fbY := op.Y[0] + row
	hss := op.Flags&OpFlagHSS != 0
	checkECD := SpriteRowChecksECD(op)
	endsOnCount := EndsOnCount(&op.Pixel)
	for st.X < x1 {
		xc := st.X + YieldChunk
		if xc > x1 {
			xc = x1
		}
		for st.X < xc {
			srcX := ScaledSrcX(op, st.X)
			adv := sourceAdvance(st.SrcReadX, srcX, hss)
			st.SrcReadX = srcX

			dot := ReadRowDot(vram, rowBase, srcX, op.ColorMode)
			if !TexelDraws(dot, op.ColorMode, op.Flags&OpFlagSPDOn, checkECD, &st.EC, srcX) {
				if endsOnCount && st.EC.Ended() {
					// The row ends at this column, its cutoff.
					st.End = st.X
					x1 = st.X
					break
				}
				st.X++
				cycles += stepCycles(adv, pixCycles, false)
				continue
			}

			pixel := DotToPixel(vram, dot, op.Color, op.ColorMode)
			gouraud := uint32(0)
			if op.Pixel.CC >= 4 {
				gouraud = SpriteGouraud(op, st.X, row)
			}
			PutDot(fb, rs, &op.Pixel, op.X[0]+st.X, fbY, pixel, gouraud, st.Key)

			st.X++
			cycles += stepCycles(adv, pixCycles, true)
		}
		if cycles >= budget {
			break
		}
	}
	return cycles
}

// LineReadsTexels reports whether a textured line's cycles depend on
// its texels: when the line checks end codes, since the second one
// ends it, or when a dot drawn costs more than a dot skipped, in a
// six-cycle mode with transparent dots not drawn. Every other line
// charges from its step count alone. With one cycle per dot a skipped
// dot and a drawn dot cost the same, and with transparent dots drawn
// every dot is drawn.
func LineReadsTexels(op *DrawOp, checkECD bool, pixCycles int32) bool {
	return checkECD || (pixCycles != 1 && op.Flags&OpFlagSPDOn == 0)
}

// SpriteRowTime charges columns [st.X, x1) of row of op without
// drawing them, as SpriteRowSpan would charge them, in the same
// YieldChunk pieces against the same budget, and cuts the row where
// SpriteRowSpan would: a row whose cycles depend on its texels reads
// them through the same rules and reads nothing else, and any other
// row charges from its columns alone.
func SpriteRowTime(vram *compute.VDP1VRAM, op *DrawOp, st *SpriteRow, row, x1, budget int32, pixCycles int32) int32 {
	if x1 > st.End {
		x1 = st.End
	}
	if op.Kind == OpScaledSprite {
		return scaledRowTime(vram, op, st, row, x1, budget, pixCycles)
	}
	return normalRowTime(vram, op, st, row, x1, budget, pixCycles)
}

func normalRowTime(vram *compute.VDP1VRAM, op *DrawOp, st *SpriteRow, row, x1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	checkECD := SpriteRowChecksECD(op)
	if !LineReadsTexels(op, checkECD, pixCycles) {
		for st.X < x1 {
			xc := st.X + YieldChunk
			if xc > x1 {
				xc = x1
			}
			cycles += (xc - st.X) * pixCycles
			st.X = xc
			if cycles >= budget {
				break
			}
		}
		return cycles
	}
	rowBase := spriteRowBase(op, row)
	endsOnCount := EndsOnCount(&op.Pixel)
	for st.X < x1 {
		xc := st.X + YieldChunk
		if xc > x1 {
			xc = x1
		}
		for st.X < xc {
			readX := NormalSrcX(op, st.X)
			dot := ReadRowDot(vram, rowBase, readX, op.ColorMode)
			if !TexelDraws(dot, op.ColorMode, op.Flags&OpFlagSPDOn, checkECD, &st.EC, readX) {
				if endsOnCount && st.EC.Ended() {
					st.End = st.X
					x1 = st.X
					break
				}
				st.X++
				cycles += stepCycles(1, pixCycles, false)
				continue
			}
			st.X++
			cycles += stepCycles(1, pixCycles, true)
		}
		if cycles >= budget {
			break
		}
	}
	return cycles
}

func scaledRowTime(vram *compute.VDP1VRAM, op *DrawOp, st *SpriteRow, row, x1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	hss := op.Flags&OpFlagHSS != 0
	checkECD := SpriteRowChecksECD(op)
	reads := LineReadsTexels(op, checkECD, pixCycles)
	rowBase := spriteRowBase(op, row)
	endsOnCount := EndsOnCount(&op.Pixel)
	for st.X < x1 {
		xc := st.X + YieldChunk
		if xc > x1 {
			xc = x1
		}
		for st.X < xc {
			srcX := ScaledSrcX(op, st.X)
			adv := sourceAdvance(st.SrcReadX, srcX, hss)
			st.SrcReadX = srcX

			if reads {
				dot := ReadRowDot(vram, rowBase, srcX, op.ColorMode)
				if !TexelDraws(dot, op.ColorMode, op.Flags&OpFlagSPDOn, checkECD, &st.EC, srcX) {
					if endsOnCount && st.EC.Ended() {
						st.End = st.X
						x1 = st.X
						break
					}
					st.X++
					cycles += stepCycles(adv, pixCycles, false)
					continue
				}
			}

			st.X++
			cycles += stepCycles(adv, pixCycles, true)
		}
		if cycles >= budget {
			break
		}
	}
	return cycles
}
