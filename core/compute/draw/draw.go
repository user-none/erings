// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// Buffers are the buffers an op draw reads and writes: the VDP1 VRAM
// its texels come from, the frame buffer it draws into, the round
// state of the device's passes, and the line records. The walk's
// store pass reads no round state, and passes none.
type Buffers struct {
	VRAM    *compute.VDP1VRAM
	FB      compute.VDP1FB
	Round   *compute.RoundState
	Records compute.LineRecords
}

// DrawState is an op's progress through Draw. The walk calls Draw in
// budgeted pieces along its schedule and resumes from this state.
// Cycles is what the last call charged. Budget bounds a call's
// cycles, 0 for no bound. PixCycles is the cycles per dot drawn,
// the color calculation mode's cost, which the walk charges and a
// device does not count. Line is the next line: a sprite's row, a
// four-vertex part's connecting line, a polyline's leg, a line's or
// a point-sided polygon's one segment, or an erase's row; Begun
// marks the line's state below as set up; Done marks the op drawn.
// Lines and Form are the op's line count and the loop it runs,
// derived from the op on the first call and kept, since the walk
// re-enters every few cycles. Timing marks a walk that charges the
// op's cycles without drawing it: the spans are the timing forms,
// which read texels only where the cycles depend on them and store
// the same line ends.
type DrawState struct {
	Cycles    int32
	Budget    int32
	PixCycles int32
	Lines     int32
	Form      uint32
	Line      int32
	Begun     bool
	Done      bool
	Timing    bool
	Sprite    SpriteRow
	Quad      QuadLine
	Seg       Segment
}

// Draw is the walk's loop over a draw op: it draws the op's lines in
// order and every step of a line in order, through the walk's own
// setup and span functions, under its cycle budget, yielding with
// the state kept, and as it reads a textured line's dots in order it
// finds where the end code rule ended the line and records it. The
// device does not run Draw; its entry point is Generate, which takes
// each textured line's end from the walk's record.
//
// Draw runs the pixel write's store pass, which reads no key word,
// so the walk's buffers hold no round state. Each line's state carries the
// line's draw order key, as the spans expect, though the store pass
// never reads it. A walk with Timing set runs the timing form of
// every span instead: the same cycles in the same pieces, the same
// line ends, texels read only where the cycles depend on them, and
// no dot drawn.
func Draw(b *Buffers, op *DrawOp, st *DrawState) {
	st.Cycles = 0
	if st.Lines == 0 {
		st.Lines = OpLines(op)
		if st.Lines == 0 {
			st.Done = true
			return
		}
		st.Form = OpForm(op)
	}
	lines := st.Lines
	budget := st.Budget
	if budget == 0 {
		budget = UnboundedCycles
	}
	switch st.Form {
	case OpErase:
		EraseRows(b.FB, op, st.Line, lines)
	case OpNormalSprite, OpScaledSprite:
		width := SpriteRowWidth(op)
		for st.Line < lines {
			if !st.Begun {
				st.Cycles += BeginSpriteRow(op, &st.Sprite, st.Line)
				st.Begun = true
				st.Sprite.Key = LineKey(op.Seq, st.Line)
			}
			for st.Sprite.X < st.Sprite.End {
				if st.Timing {
					st.Cycles += SpriteRowTime(b.VRAM, op, &st.Sprite, st.Line, width, budget-st.Cycles, st.PixCycles)
				} else {
					st.Cycles += SpriteRowSpan(b.VRAM, b.FB, b.Round, op, &st.Sprite, st.Line, width, budget-st.Cycles, st.PixCycles)
				}
				if st.Cycles >= budget && st.Sprite.X < st.Sprite.End {
					return
				}
			}
			StoreLineEnd(b.Records, op.RecordBase, st.Line, st.Sprite.End)
			st.Line++
			st.Begun = false
			if st.Cycles >= budget && st.Line < lines {
				return
			}
		}
	case OpDistortedSprite, OpPolygon:
		for st.Line < lines {
			if !st.Begun {
				st.Cycles += BeginQuadLine(op, &st.Quad, st.Line)
				st.Begun = true
				st.Quad.Key = LineKey(op.Seq, st.Line)
			}
			for st.Quad.J <= st.Quad.JEnd {
				if st.Timing {
					st.Cycles += QuadLineTime(b.VRAM, op, &st.Quad, st.Quad.JEnd+1, budget-st.Cycles, st.PixCycles)
				} else {
					st.Cycles += QuadLineSpan(b.VRAM, b.FB, b.Round, op, &st.Quad, st.Quad.JEnd+1, budget-st.Cycles, st.PixCycles)
				}
				if st.Cycles >= budget && st.Quad.J <= st.Quad.JEnd {
					return
				}
			}
			if RecordStride(op) != 0 {
				StoreLineEnd(b.Records, op.RecordBase, st.Line, st.Quad.JEnd)
			}
			st.Line++
			st.Begun = false
			if st.Cycles >= budget && st.Line < lines {
				return
			}
		}
	case OpPolyline, OpLine:
		for st.Line < lines {
			if !st.Begun {
				x1, y1, x2, y2, gStart, gEnd := op.X[0], op.Y[0], op.X[1], op.Y[1], op.Gouraud[0], op.Gouraud[1]
				if op.Kind == OpPolyline {
					x1, y1, x2, y2, gStart, gEnd = PolylineLeg(op, st.Line)
				}
				BeginSegment(&st.Seg, x1, y1, x2, y2, gStart, gEnd)
				st.Cycles += PreClipDetectCycles(op)
				st.Begun = true
				st.Seg.Key = LineKey(op.Seq, st.Line)
			}
			for st.Seg.I <= st.Seg.IMax {
				if st.Timing {
					st.Cycles += SegmentTime(&st.Seg, st.Seg.IMax+1, budget-st.Cycles, st.PixCycles)
				} else {
					st.Cycles += SegmentSpan(b.FB, b.Round, op, &st.Seg, st.Seg.IMax+1, budget-st.Cycles, st.PixCycles)
				}
				if st.Cycles >= budget && st.Seg.I <= st.Seg.IMax {
					return
				}
			}
			if op.Kind == OpPolyline {
				// Per-leg overhead.
				st.Cycles++
			}
			st.Line++
			st.Begun = false
			if st.Cycles >= budget && st.Line < lines {
				return
			}
		}
	}
	st.Done = true
}
