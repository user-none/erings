// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// PreClipLineCycles is the per-line pre-clipping detection cost. A
// line wholly outside the drawing area costs only this; a line that
// draws pays it on top of its dots when pre-clipping is performed
// (PreClipDetectCycles). Off-screen dots of a partially visible line
// are charged per dot like visible dots, since the DDA iteration runs
// regardless and only the frame buffer write is discarded.
//
// The VDP1 User's Manual Supplement Section 1.2 (Pre-Clipping Disable)
// states the detection overhead is "up to five CPU clock cycles for
// one line", a maximum with no formula for the actual value, and
// Section 2.2 that disabling pre-clipping raises drawing speed, so
// the detection is paid on the lines that draw too. 4 is within that
// maximum.
const PreClipLineCycles = 4

// PreClipDetectCycles is the pre-clipping detection cost a line that
// draws pays: PreClipLineCycles when the command performs
// pre-clipping (CMDPMOD Pclp = 0), else nothing (VDP1 User's Manual
// Supplement Sections 1.2 and 2.2).
func PreClipDetectCycles(op *DrawOp) int32 {
	if op.Flags&OpFlagPreClip != 0 {
		return PreClipLineCycles
	}
	return 0
}

// QuadLine is one connecting line of a four-vertex part (a distorted
// sprite or a polygon): its endpoints and length, the steps still to
// draw, and the per-step deltas. A dot's position, texture column,
// and Gouraud value follow from its step by closed form, so a span
// call derives its state from J. EC and SrcReadU carry the end code
// count and the source advance across span calls.
type QuadLine struct {
	LX, LY, RX, RY int32  // endpoints on the left (A-D) and right (B-C) edges
	Len            int32  // steps along the line, the larger axis distance
	J              int32  // next step
	JEnd           int32  // last step to draw, after the pre-clip
	Key            uint32 // the line's draw order key (LineKey)
	DPX, DPY       int64  // position step, DDAFraction fraction bits, truncated toward zero
	UStart, DU     int32  // texture column at step 0, and the texture position's step at 16 plus the op's scale shift fraction bits
	RowBase        uint32
	EC             EndCodeCounter // end codes seen on the line, counted per step
	SrcReadU       int32          // last source column read, -1 before the first
	HSSShrink      bool           // the line shrinks the texture horizontally under high speed shrink
	CheckECD       bool           // end codes end the line
	Simple         bool           // the pixel write is a plain 16-bit replace under the system clip
	Gouraud        GouraudStepper // at step 0
}

// EdgeAt is the dot of the A-D edge from (x1, y1) to (x2, y2) over
// steps steps at step i: the DDA start plus i deltas (NewDDAEdge).
func EdgeAt(x1, y1, x2, y2, steps, i int32) (x, y int32) {
	e := NewDDAEdge(x1, y1, x2, y2, steps)
	return int32((e.X + e.DX*int64(i)) >> DDAFraction), int32((e.Y + e.DY*int64(i)) >> DDAFraction)
}

// RightEdgeAt is the dot of the B-C edge from (x1, y1) to (x2, y2)
// over steps steps at step i (NewRightDDAEdge).
func RightEdgeAt(x1, y1, x2, y2, steps, i int32) (x, y int32) {
	e := NewRightDDAEdge(x1, y1, x2, y2, steps)
	return int32((e.X + e.DX*int64(i)) >> DDAFraction), int32((e.Y + e.DY*int64(i)) >> DDAFraction)
}

// QuadPos is the dot position at step j of the line: the DDA
// position from the left endpoint, pinned to the right endpoint at
// the last step.
func QuadPos(st *QuadLine, j int32) (px, py int32) {
	if j >= st.Len {
		return st.RX, st.RY
	}
	return int32((ddaStart(st.LX, st.DPX, false) + st.DPX*int64(j)) >> DDAFraction), int32((ddaStart(st.LY, st.DPY, false) + st.DPY*int64(j)) >> DDAFraction)
}

// QuadSrcU is the source column read for texture column u (a texture
// position's integer part): clamped to the character and snapped to
// one parity under high speed shrink.
func QuadSrcU(op *DrawOp, st *QuadLine, u int32) int32 {
	if u < 0 {
		u = 0
	} else if u >= op.CharW {
		u = op.CharW - 1
	}
	if st.HSSShrink {
		if op.Flags&OpFlagHSSOdd != 0 {
			return u | 1
		}
		return u &^ 1
	}
	return u
}

// quadU is the texture position at step j, at 16 plus the scale
// shift fraction bits.
func quadU(st *QuadLine, j int32, shift uint32) int32 {
	return int32(int64(st.UStart)<<(16+shift) + int64(st.DU)*int64(j))
}

// quadLineEnds is the endpoints of connecting line i of a part of
// steps edge steps: the vertices at the first and last step and the
// edge DDA positions between, the endpoints BeginQuadLine sets.
func quadLineEnds(op *DrawOp, steps, i int32) (lx, ly, rx, ry int32) {
	if i == 0 {
		return op.X[0], op.Y[0], op.X[1], op.Y[1]
	}
	if i == steps {
		return op.X[3], op.Y[3], op.X[2], op.Y[2]
	}
	lx, ly = EdgeAt(op.X[0], op.Y[0], op.X[3], op.Y[3], steps, i)
	rx, ry = RightEdgeAt(op.X[1], op.Y[1], op.X[2], op.Y[2], steps, i)
	return lx, ly, rx, ry
}

// At scale each native dot is an N by N block of sub-dots. A native
// step's dot at p covers [p, p+1) along its line's major axis, and
// the step owns the sub-steps whose squares (square.go) end inside
// that dot's block. A sub-line of a connecting line lies in the edge
// interval of native line L >> ScaleShift, and a sub-step of a
// sub-line, of a connecting line or a segment, belongs to the step of
// that native line whose block holds the far corner of its square
// along the native line's major axis (a tie is X-major): the sub-dot
// N - 1 on when the steps advance +, the sub-dot itself when they
// advance -, clamped to the native line's steps. So a line cut short
// at a native step, by an entry's bound or its record, ends at the
// corner of that step's block when it walks +, its last square
// filling the block, and a sub-step belongs to one native step, so
// the entries of an op draw each sub-dot once. The position along an
// axis moves one way along a sub-line, so the sub-steps of a range of
// native steps are one contiguous range, found by bisection
// (squareStepsOwned, square.go).

// stepBlocks locates a native line's steps and their blocks along its
// major axis: the major-axis coordinate of step 0, the direction the
// steps advance along it (1 or -1), the last step, and the axis.
type stepBlocks struct {
	Start int32
	Dir   int32
	Last  int32
	AxisX bool
}

// quadStepBlocks is the step blocks of native connecting line nst.
func quadStepBlocks(nst *QuadLine) stepBlocks {
	dx, dy := nst.RX-nst.LX, nst.RY-nst.LY
	sb := stepBlocks{Start: nst.LY, Dir: 1, Last: nst.Len, AxisX: absI32(dx) >= absI32(dy)}
	d := dy
	if sb.AxisX {
		sb.Start, d = nst.LX, dx
	}
	if d < 0 {
		sb.Dir = -1
	}
	return sb
}

// segmentStepBlocks is the step blocks of native segment nst.
func segmentStepBlocks(nst *Segment) stepBlocks {
	sb := stepBlocks{Start: nst.Y1, Dir: nst.SY, Last: nst.IMax, AxisX: nst.XMajor}
	if sb.AxisX {
		sb.Start, sb.Dir = nst.X1, nst.SX
	}
	return sb
}

// step is the native step the sub-dot at (x, y) belongs to at the
// scale shift: the step whose block holds the far corner of the
// sub-dot's square along the major axis, clamped to the line's steps.
func (sb *stepBlocks) step(x, y int32, shift uint32) int32 {
	p := y
	if sb.AxisX {
		p = x
	}
	if sb.Dir > 0 {
		p += 1<<shift - 1
	}
	return min(max((p>>shift-sb.Start)*sb.Dir, 0), sb.Last)
}

// walkDirection is the major axis of a walk of delta (dx, dy), X on a
// tie, and whether it walks in the - direction along it.
func walkDirection(dx, dy int32) (axisX, backward bool) {
	axisX = absI32(dx) >= absI32(dy)
	d := dy
	if axisX {
		d = dx
	}
	return axisX, d < 0
}

// nativeLineOf is the native line whose block holds sub-line i of
// scaled op: native line k's block holds sub-lines N*k to N*k+N-1.
func nativeLineOf(op *DrawOp, i int32) int32 {
	return i >> op.Pixel.ScaleShift
}

// nativeLineLen is the length of the native line whose block holds
// scaled line i of op (nativeLineOf) in the native op (nativeOp), set
// up as BeginQuadLine sets up a line.
func nativeLineLen(op *DrawOp, i int32) int32 {
	native := nativeOp(op)
	lx, ly, rx, ry := quadLineEnds(&native, QuadSteps(&native), nativeLineOf(op, i))
	return max(absI32(rx-lx), absI32(ry-ly))
}

// quadLineGeometry sets the geometry of connecting line i of op into
// st, the endpoints, the length, and the position step, and returns
// the part's edge steps. The endpoints are the ones quadLineEnds
// gives, set here in line: a call per line costs on the parts of
// many short lines.
func quadLineGeometry(op *DrawOp, st *QuadLine, i int32) int32 {
	steps := QuadSteps(op)
	if i == 0 {
		st.LX, st.LY = op.X[0], op.Y[0]
		st.RX, st.RY = op.X[1], op.Y[1]
	} else if i == steps {
		st.LX, st.LY = op.X[3], op.Y[3]
		st.RX, st.RY = op.X[2], op.Y[2]
	} else {
		st.LX, st.LY = EdgeAt(op.X[0], op.Y[0], op.X[3], op.Y[3], steps, i)
		st.RX, st.RY = RightEdgeAt(op.X[1], op.Y[1], op.X[2], op.Y[2], steps, i)
	}
	dx := st.RX - st.LX
	dy := st.RY - st.LY
	st.Len = absI32(dx)
	if absI32(dy) > st.Len {
		st.Len = absI32(dy)
	}
	st.DPX = ddaStep(dx, st.Len, false)
	st.DPY = ddaStep(dy, st.Len, false)
	return steps
}

// BeginQuadLine sets up connecting line i of op, from the A-D edge to
// the B-C edge at edge step i, and returns the cycles charged. The
// endpoints are the vertices at the first and last step and the edge
// DDA positions between. A single-dot line is step 0 alone. A longer
// line has its off-screen ends pre-clipped: the trailing dots are
// charged and dropped, and the leading dots too unless end codes are
// checked, since a skipped dot could hold the end code that ends the
// line.
func BeginQuadLine(op *DrawOp, st *QuadLine, i int32) int32 {
	cycles := int32(0)
	steps := quadLineGeometry(op, st, i)
	textured := op.Kind == OpDistortedSprite

	// The texture row follows the V DDA; high speed shrink does not
	// parity-snap the vertical axis (VDP1 manual Fig 6.5 retains every
	// source row). The U axis shrinks when the line spans fewer dots
	// than the character is wide, and end codes are disabled on that
	// same horizontal reduce (manual Sec 6.3); end codes are a
	// horizontal-only concept. At scale the texture positions and the
	// Gouraud values carry the scale shift more fraction bits: a
	// sub-line between scaled native endpoints then steps by the native
	// deltas, so its block corner steps read the native line's texels
	// and values.
	shift := op.Pixel.ScaleShift
	st.UStart = 0
	st.DU = 0
	st.RowBase = 0
	if textured {
		tex := NewDistortedTexture(op, steps)
		srcY := (tex.VStart + tex.VStep*i) >> (16 + shift)
		if srcY < 0 {
			srcY = 0
		} else if srcY >= op.CharH {
			srcY = op.CharH - 1
		}
		st.RowBase = op.CharAddr + uint32(srcY*RowStride(op.CharW, op.ColorMode))
		st.UStart = tex.UStart
		if st.Len > 0 {
			st.DU = ((tex.UEnd - tex.UStart) << (16 + shift)) / st.Len
		}
	}
	// The shrink and the end code check are the native line's: its
	// length is what the rules compare with the character width.
	nativeLen := st.Len
	if shift != 0 {
		nativeLen = nativeLineLen(op, i)
	}
	st.HSSShrink = textured && op.Flags&OpFlagHSS != 0 && nativeLen+1 < op.CharW
	// A single-dot line draws its dot whether or not it is an end code.
	st.CheckECD = textured && nativeLen > 0 && !st.HSSShrink && op.Flags&OpFlagECDOff == 0
	st.Simple = SimpleWrite(&op.Pixel)
	st.EC.Reset()
	st.SrcReadU = -1

	if op.Pixel.CC >= 4 {
		left := NewGouraudStepper(op.Gouraud[0], op.Gouraud[3], steps+1, shift)
		left.Advance(i)
		right := NewGouraudStepper(op.Gouraud[1], op.Gouraud[2], steps+1, shift)
		right.Advance(i)
		st.Gouraud = NewGouraudStepper(left.Value(shift), right.Value(shift), st.Len+1, shift)
	}

	st.J = 0
	st.JEnd = st.Len
	if st.Len == 0 {
		return PreClipDetectCycles(op)
	}

	// Pre-clip the connecting line to the drawing area (VDP1 manual
	// pre-clipping). Iterating off-screen spans is pure overdraw that
	// the pixel write discards, so skipping them leaves the output
	// unchanged.
	c0, c1, ok := ClipSteps(st.LX, st.LY, st.RX, st.RY, op.Pixel.ClipX, op.Pixel.ClipY, st.Len)
	if !ok {
		// Wholly off-screen: detection cost only.
		cycles += PreClipLineCycles
		st.J = st.Len + 1
		return cycles
	}
	j1 := c1 + 2
	if j1 < st.JEnd {
		// Off-screen trailing dots still iterate on hardware; charge
		// them like visible dots.
		cycles += st.JEnd - j1
		st.JEnd = j1
	}
	if !st.CheckECD {
		j0 := c0 - 2
		if j0 > 0 {
			cycles += j0
			st.J = j0
		}
	}
	return cycles + PreClipDetectCycles(op)
}

// The anti-alias hole fill of a connecting line (VDP1 manual Sec 1,
// Anti-aliasing, Figure 1.6): when step j's dot at (px, py) shares
// neither coordinate with the previous step's at (prevPx, prevPy),
// the staircase drops a dot, and one corner of the step is filled
// before the step's own dot. The figure gives the corner for each of
// the eight drawing directions: (px, prevPy) when the step advances
// x and y in the same direction, (prevPx, py) when it advances them
// in opposite directions. HoleFills is the condition (step 0 has no
// previous dot), HoleFillDot the corner. Two functions so the
// condition stays a plain branch in the loops.
func HoleFills(j, px, py, prevPx, prevPy int32) bool {
	return j > 0 && px != prevPx && py != prevPy
}

func HoleFillDot(px, py, prevPx, prevPy int32) (gfx, gfy int32) {
	if (px > prevPx) == (py > prevPy) {
		return px, prevPy
	}
	return prevPx, py
}

// QuadLineSpan draws steps [st.J, j1) of the connecting line, where
// j1 is at most st.JEnd+1, or to the first YieldChunk boundary at or
// past the cycle budget with the line unfinished, and returns the
// cycles charged: the source advance per dot (one, or the distance
// between texture samples, halved under high speed shrink) plus the
// balance to pixCycles for a dot drawn. Adjacent connecting lines
// overlap and double-write where they share dots, matching hardware
// (VDP1 manual Sec 1.6 "Anti-aliasing" / Sec 1.7), and each step
// fills its hole-fill dot (HoleFills, HoleFillDot) before its own. A plain
// replace under the system clip (SimpleWrite) stores its dots
// directly, the decision PutDot would reach.
func QuadLineSpan(vram *compute.VDP1VRAM, fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, st *QuadLine, j1, budget int32, pixCycles int32) int32 {
	if op.Kind == OpDistortedSprite {
		return texturedLineSpan(vram, fb, rs, op, st, j1, budget, pixCycles)
	}
	return flatLineSpan(fb, rs, op, st, j1, budget, pixCycles)
}

// flatLineSpan is the polygon's loop: a constant color, every dot
// drawn, pixCycles per dot. The stepping state runs in locals from
// the closed forms at the entry step.
func flatLineSpan(fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, st *QuadLine, j1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	simple := st.Simple
	shaded := op.Pixel.CC >= 4
	shift := op.Pixel.ScaleShift
	pixel := op.Color
	clipX, clipY, fbw := op.Pixel.ClipX, op.Pixel.ClipY, op.Pixel.FBWidth
	rx, ry, ln := st.RX, st.RY, st.Len
	dpx, dpy := st.DPX, st.DPY
	j := st.J
	px32 := ddaStart(st.LX, dpx, false) + dpx*int64(j)
	py32 := ddaStart(st.LY, dpy, false) + dpy*int64(j)
	prevPx, prevPy := st.LX, st.LY
	if j > 0 {
		prevPx, prevPy = QuadPos(st, j-1)
	}
	g := st.Gouraud
	g.Advance(j)
	for j < j1 {
		jc := j + YieldChunk
		if jc > j1 {
			jc = j1
		}
		for j < jc {
			px := int32(px32 >> DDAFraction)
			py := int32(py32 >> DDAFraction)
			if j == ln {
				px = rx
				py = ry
			}
			gouraud := uint32(0)
			if shaded {
				gouraud = g.Value(shift)
			}
			if HoleFills(j, px, py, prevPx, prevPy) {
				gfx, gfy := HoleFillDot(px, py, prevPx, prevPy)
				if simple {
					if gfx >= 0 && gfx <= clipX && gfy >= 0 && gfy <= clipY {
						fb.PutU16BE(uint32(gfy*fbw+gfx)*2, uint16(pixel))
					}
				} else {
					PutDot(fb, rs, &op.Pixel, gfx, gfy, pixel, gouraud, st.Key)
				}
			}
			if simple {
				if px >= 0 && px <= clipX && py >= 0 && py <= clipY {
					fb.PutU16BE(uint32(py*fbw+px)*2, uint16(pixel))
				}
			} else {
				PutDot(fb, rs, &op.Pixel, px, py, pixel, gouraud, st.Key)
			}
			prevPx = px
			prevPy = py
			if shaded {
				g.Step()
			}
			px32 += dpx
			py32 += dpy
			j++
			cycles += pixCycles
		}
		if cycles >= budget && j < j1 {
			break
		}
	}
	st.J = j
	return cycles
}

// texturedLineSpan is the distorted sprite's loop: the texture column
// follows the U DDA, high speed shrink snaps it to one parity, end
// codes (counted per step: the line reads a column more than once
// when it enlarges, and each read counts) and transparent dots are
// skipped, the second end code ends the line, and the source advance
// is charged per dot. The position and texture run incrementally
// from the closed forms at the entry step.
func texturedLineSpan(vram *compute.VDP1VRAM, fb compute.VDP1FB, rs *compute.RoundState, op *DrawOp, st *QuadLine, j1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	simple := st.Simple
	endsOnCount := EndsOnCount(&op.Pixel)
	shift := op.Pixel.ScaleShift
	ufrac := 16 + shift
	j := st.J
	px32 := ddaStart(st.LX, st.DPX, false) + st.DPX*int64(j)
	py32 := ddaStart(st.LY, st.DPY, false) + st.DPY*int64(j)
	u := quadU(st, j, shift)
	prevPx, prevPy := st.LX, st.LY
	if j > 0 {
		prevPx, prevPy = QuadPos(st, j-1)
	}
	g := st.Gouraud
	g.Advance(j)
	for j < j1 {
		jc := j + YieldChunk
		if jc > j1 {
			jc = j1
		}
		for j < jc {
			px := int32(px32 >> DDAFraction)
			py := int32(py32 >> DDAFraction)
			if j == st.Len {
				px = st.RX
				py = st.RY
			}
			srcU := QuadSrcU(op, st, u>>ufrac)
			adv := sourceAdvance(st.SrcReadU, srcU, st.HSSShrink)
			st.SrcReadU = srcU

			var dot uint32
			switch op.ColorMode {
			case 0, 1:
				dot = RowDotNibble(vram, st.RowBase, srcU)
			case 2, 3, 4:
				dot = RowDotByte(vram, st.RowBase, srcU)
			case 5:
				dot = RowDotWord(vram, st.RowBase, srcU)
			}
			draw := TexelDraws(dot, op.ColorMode, op.Flags&OpFlagSPDOn, st.CheckECD, &st.EC, j)
			if !draw && endsOnCount && st.EC.Ended() {
				// The line ends: the last step drawn is the one
				// before, the line's cutoff.
				st.JEnd = j - 1
				j = st.Len + 1
				break
			}
			if draw {
				pixel := DotToPixel(vram, dot, op.Color, op.ColorMode)
				gouraud := uint32(0)
				if op.Pixel.CC >= 4 {
					gouraud = g.Value(shift)
				}
				if HoleFills(j, px, py, prevPx, prevPy) {
					gfx, gfy := HoleFillDot(px, py, prevPx, prevPy)
					if simple {
						if gfx >= 0 && gfx <= op.Pixel.ClipX && gfy >= 0 && gfy <= op.Pixel.ClipY {
							fb.PutU16BE(uint32(gfy*op.Pixel.FBWidth+gfx)*2, uint16(pixel))
						}
					} else {
						PutDot(fb, rs, &op.Pixel, gfx, gfy, pixel, gouraud, st.Key)
					}
				}
				if simple {
					if px >= 0 && px <= op.Pixel.ClipX && py >= 0 && py <= op.Pixel.ClipY {
						fb.PutU16BE(uint32(py*op.Pixel.FBWidth+px)*2, uint16(pixel))
					}
				} else {
					PutDot(fb, rs, &op.Pixel, px, py, pixel, gouraud, st.Key)
				}
				cycles += stepCycles(adv, pixCycles, true)
			} else {
				cycles += stepCycles(adv, pixCycles, false)
			}

			prevPx = px
			prevPy = py
			if op.Pixel.CC >= 4 {
				g.Step()
			}
			px32 += st.DPX
			py32 += st.DPY
			u += st.DU
			j++
		}
		if cycles >= budget && j < j1 {
			break
		}
	}
	st.J = j
	return cycles
}

// QuadLineTime charges steps [st.J, j1) of the line without drawing
// them, as QuadLineSpan would charge them, in the same YieldChunk
// pieces against the same budget, and ends the line where
// QuadLineSpan would: a distorted sprite line whose cycles depend on
// its texels reads them through the same rules and nothing else, any
// other distorted sprite line charges the source advance per step
// from the same closed forms, and a polygon line charges pixCycles
// per step.
func QuadLineTime(vram *compute.VDP1VRAM, op *DrawOp, st *QuadLine, j1, budget int32, pixCycles int32) int32 {
	cycles := int32(0)
	j := st.J
	if op.Kind != OpDistortedSprite {
		for j < j1 {
			jc := j + YieldChunk
			if jc > j1 {
				jc = j1
			}
			cycles += (jc - j) * pixCycles
			j = jc
			if cycles >= budget && j < j1 {
				break
			}
		}
		st.J = j
		return cycles
	}
	checkECD := st.CheckECD
	reads := LineReadsTexels(op, checkECD, pixCycles)
	endsOnCount := EndsOnCount(&op.Pixel)
	ufrac := 16 + op.Pixel.ScaleShift
	u := quadU(st, j, op.Pixel.ScaleShift)
	for j < j1 {
		jc := j + YieldChunk
		if jc > j1 {
			jc = j1
		}
		for j < jc {
			srcU := QuadSrcU(op, st, u>>ufrac)
			adv := sourceAdvance(st.SrcReadU, srcU, st.HSSShrink)
			st.SrcReadU = srcU

			draw := true
			if reads {
				var dot uint32
				switch op.ColorMode {
				case 0, 1:
					dot = RowDotNibble(vram, st.RowBase, srcU)
				case 2, 3, 4:
					dot = RowDotByte(vram, st.RowBase, srcU)
				case 5:
					dot = RowDotWord(vram, st.RowBase, srcU)
				}
				draw = TexelDraws(dot, op.ColorMode, op.Flags&OpFlagSPDOn, checkECD, &st.EC, j)
				if !draw && endsOnCount && st.EC.Ended() {
					st.JEnd = j - 1
					j = st.Len + 1
					break
				}
			}

			u += st.DU
			j++
			cycles += stepCycles(adv, pixCycles, draw)
		}
		if cycles >= budget && j < j1 {
			break
		}
	}
	st.J = j
	return cycles
}
