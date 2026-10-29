// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

import "github.com/user-none/erings/core/compute"

// LineKey is the draw order key of line line of the op at ordinal
// seq of its batch: the ordinal above the line, one added so no key
// is zero, which is the order stamp's value where no dot has landed.
// Two dots of one line never share a pixel, so the key needs no
// step. The frame's cycle budget bounds the ordinal under 2^16 (a
// command's table fetch costs 15 cycles), and the drawing coordinate
// range bounds the line under 2^16: VDP1 manual Sec 2.1, Frame
// Buffer Plane, gives -1024 to 1023 on both axes and states that
// operation is not guaranteed beyond it, so a part's edge has at
// most 2047 steps, 2047 << ScaleShift at scale, under 2^16 through
// a scale shift of 5. The walk accepts wider coordinates, and a list
// with an edge of 65536 steps or more, outside the guaranteed range
// by a factor of thirty at native and by 65536 >> ScaleShift native
// steps at scale, would let a line index carry into the ordinal; the
// manual defines no result for such a list. So the key orders the
// batch's lines as the walk drew them.
func LineKey(seq uint32, line int32) uint32 {
	return (seq+1)<<16 | uint32(line)
}

// The round state's regions are the RoundState's three buffers, each
// sized by the host: the order stamps, one per pixel at its finest
// addressing, each the key of the last replace-class dot on the pixel
// or zero; the fragment list heads, one per 16-bit pixel, the slot of
// the pixel's last appended fragment or zero; and the fragment pool,
// its word FragmentCounter the fragment counter in words and its
// fragments from slot FragmentWords, FragmentWords each: the dot's
// key, its pixel and Gouraud value packed, the slot of the next
// fragment of the list or zero, and the color calculation mode. Slot
// 0 holds the counter and is never a fragment, so a zero link ends a
// list. Destination-reading dots never land in an 8-bit frame buffer,
// whose modes replace, so the heads cover the 16-bit pixels.
const (
	FragmentCounter = 0
	FragmentWords   = 4
)

// FragmentCap is the fragments a pool of poolWords words holds: its
// slots after the counter's. The host bounds a round to it.
func FragmentCap(poolWords uint32) uint32 {
	return poolWords/FragmentWords - 1
}

// appendFragment appends a destination-reading dot to its pixel's
// list: a fragment slot reserved from the counter, the dot's words
// stored, and the head exchanged for the slot, the prior head
// becoming the fragment's link. A round is bounded by the host to
// the pool's FragmentCap; a reservation past the end is dropped.
func appendFragment(rs *compute.RoundState, index, key, pixel, gouraud, cc uint32) {
	slot := FragmentWords + rs.Pool.Add(FragmentCounter, FragmentWords)
	if slot+FragmentWords > uint32(len(rs.Pool)) {
		return
	}
	rs.Pool[slot] = key
	rs.Pool[slot+1] = pixel | gouraud<<16
	rs.Pool[slot+3] = cc
	rs.Pool[slot+2] = rs.Heads.Swap(index, slot)
}

// ClearStamps zeroes order stamp i, once per frame after the op list
// restarts. Entry point: one thread per stamp.
func ClearStamps(rs *compute.RoundState, i uint32) {
	rs.Stamps[i] = 0
}

// ClearLists zeroes fragment list head i and, on the first thread,
// the fragment counter, once per round with destination-reading ops.
// Entry point: one thread per head.
func ClearLists(rs *compute.RoundState, i uint32) {
	rs.Heads[i] = 0
	if i == 0 {
		rs.Pool[FragmentCounter] = 0
	}
}

// Resolve applies the fragment list of 16-bit pixel i, the round's
// destination-reading dots on the pixel, in draw order, over the
// pixel as the write pass left it: the dots keyed above the pixel's
// replace winner, which is the last replace-class dot of the round
// on the pixel or none, each smallest key next, through the mode's
// color calculation against a register read once and stored once. A
// dot keyed below the winner was drawn over by it and is dead, and
// of dots sharing a key one is applied. Entry point: one thread per
// pixel.
//
// The list is in arrival order, not draw order, so each applied
// fragment is found by a walk of the whole list: about F squared over
// two loads for F fragments on the pixel. That is the cheaper form up
// to about sixteen fragments on a pixel, which covers the measured
// workloads, one destination-reading op per frame. Above that a
// bottom-up merge sort of the list in place through the link words,
// about three memory operations per node per pass over log2(F)
// passes, then one apply pass, costs less, more than ten times less
// at a thousand. The cycle budget bounds a round's fragments, so the
// worst case is a slow frame, never a wrong pixel.
func Resolve(fb compute.VDP1FB, rs *compute.RoundState, i uint32) {
	head := rs.Heads[i]
	if head == 0 {
		return
	}
	last := rs.Stamps[i]
	limit := FragmentCap(uint32(len(rs.Pool)))
	dst := uint32(fb.U16BE(i * 2))
	applied := false
	for {
		best := uint32(0xFFFFFFFF)
		bestSlot := uint32(0)
		steps := uint32(0)
		for s := head; s != 0 && steps < limit; s = rs.Pool[s+2] {
			k := rs.Pool[s]
			if k > last && k < best {
				best = k
				bestSlot = s
			}
			steps++
		}
		if bestSlot == 0 {
			break
		}
		v := rs.Pool[bestSlot+1]
		out := ApplyColorCalc(rs.Pool[bestSlot+3], dst, v&0xFFFF, v>>16)
		if out != NoStore {
			dst = out
		}
		applied = true
		last = best
	}
	if applied {
		fb.PutU16BE(i*2, uint16(dst))
	}
}

// PageCopyParams locates a band of staged VDP1 VRAM pages: the slot
// of its first page in the page store and the page table. Slot s of
// the band holds the bytes of page table entry Base+s, and the copy
// writes them to that page of the memory. A frame buffer band also
// carries the scale its frame buffer is held at (ScaleShift, 0
// native), the native frame buffer's width in pixels and pixel size,
// which place a staged byte's pixel in the scaled buffer, and the
// buffer's layout: Doubled is 1 when the buffer holds every row of
// the doubled Y range under double interlace, where native row r is
// doubled rows 2r and 2r + 1, both of which take the byte.
type PageCopyParams struct {
	Base       uint32
	ScaleShift uint32
	FBWidth    uint32
	Doubled    uint32
	Is8bpp     bool
}

// VRAMPages is a submission's staged VDP1 VRAM pages: the page
// store, a whole page per slot in memory order, and the page table,
// the destination page number of each slot.
type VRAMPages struct {
	Store *compute.VDP1PageStore
	Table *compute.PageTable
}

// FBWrites is a submission's staged pages of the bytes the CPU wrote
// into a frame buffer: the page store, the mask store with a bit per
// byte of each slot that the CPU wrote, and the page table.
type FBWrites struct {
	Store *compute.VDP1PageStore
	Masks *compute.MaskStore
	Table *compute.PageTable
}

// CopyVDP1Pages writes word i of a band's staged pages into VDP1
// VRAM. Entry point: one thread per word.
func CopyVDP1Pages(vram *compute.VDP1VRAM, pages *VRAMPages, p *PageCopyParams, i uint32) {
	slot := p.Base + i/compute.PageWords
	word := i % compute.PageWords
	src := slot*compute.PageBytes + word*4
	dst := pages.Table[slot]*compute.PageBytes + word*4
	vram.PutU32BE(dst, pages.Store.U32BE(src))
}

// CopyFBPages writes byte i of a band's staged pages of CPU frame
// buffer writes into the frame buffer where the page's mask marks
// the byte as written. At scale the byte is one lane of a native
// pixel (the address over two in a 16-bit buffer, the address in an
// 8-bit one), and it is written to the same lane of every pixel of
// that pixel's N by N block at the scaled width; into a doubled
// buffer the pixel's rows are 2r and 2r + 1, both written, since a
// CPU write under double interlace is the game's picture for both
// fields. A native buffer at native scale takes the byte at its
// address. Entry point: one thread per byte.
func CopyFBPages(fb compute.VDP1FB, writes *FBWrites, p *PageCopyParams, i uint32) {
	slot := p.Base + i/compute.PageBytes
	b := i % compute.PageBytes
	if writes.Masks[slot*(compute.PageBytes/32)+b/32]&(1<<(b&31)) == 0 {
		return
	}
	addr := writes.Table[slot]*compute.PageBytes + b
	v := writes.Store.U8(slot*compute.PageBytes + b)
	shift := p.ScaleShift
	if shift == 0 && p.Doubled == 0 {
		fb.PutU8(addr, v)
		return
	}
	if p.FBWidth == 0 {
		return
	}
	pixel, lane, size := addr>>1, addr&1, uint32(2)
	if p.Is8bpp {
		pixel, lane, size = addr, 0, 1
	}
	width := p.FBWidth << shift
	x := pixel % p.FBWidth << shift
	n := uint32(1) << shift
	for q := range 1 + p.Doubled {
		y := (pixel/p.FBWidth<<p.Doubled + q) << shift
		for dy := range n {
			row := (y + dy) * width
			for dx := range n {
				fb.PutU8((row+x+dx)*size+lane, v)
			}
		}
	}
}

// GenChunk is the steps of a line one thread of Generate draws as
// one piece: a thread enters a chunk at its first step through the
// closed forms, so a long line is split across threads instead of
// being one thread's dependent chain.
const GenChunk = 32

// OpChunks is the chunks of op's work Generate's threads take apart,
// the most threads that do work for it: an erase's lines, and a drawn
// op's lines by its runs of GenChunk steps. A part of the op outside
// its line range or its bounds takes fewer, so the count holds every
// chunk a thread can be given.
func OpChunks(op *DrawOp) uint32 {
	lines := OpLines(op)
	if lines <= 0 {
		return 0
	}
	if OpForm(op) == OpErase {
		return uint32(lines)
	}
	perLine := (OpDots(op) + GenChunk - 1) / GenChunk
	if perLine <= 0 {
		return 0
	}
	return uint32(lines) * uint32(perLine)
}

// GenerateTag is the tag pass of Generate, the device's draw of one
// entry of a batch, a group entry point with the entry as the
// group's params row: the group's
// threads take the chunks of the entry's lines [LineFrom, LineTo) by
// stride, the first line from LineStart and the last to LineEnd,
// each entering its line at its chunk's first step through the
// walk's own setup functions and running the walk's span over the
// chunk, with no order between the threads. The pass is the entry
// point dispatched, GenerateTag or GenerateWrite, and says what the
// span's pixel write does with a dot, through the pixel params of the
// thread's copy of the entry: the tag pass raises the pixel's order
// stamp to the line's key and the write pass stores the dot whose
// key the stamp holds. A textured line
// ends where the walk's record says the end code rule ended it: a
// thread entering a line part way has not read the end codes before
// its chunk, and within the recorded range a fresh count never ends
// the line, since the record already cut it at the second end code.
// An entry's last line bounded by LineEnd is the same case: the walk
// had not ended that line when the entry closed. At scale a sprite
// row's end comes from its native row's record through ScaledRowEnd,
// and the entry's bounds, scaled, apply to every sub-row of the
// native rows they bound. A connecting line's or a segment's entry
// keeps its native bounds, and a sub-dot draws when the native line
// and step whose block holds it (quad.go) lie within them and, on a
// textured line, at or before the native line's record.
func GenerateTag(b *Buffers, entry *DrawOp, t, n, g uint32) {
	generate(b, entry, PassTag, t, n)
}

// GenerateWrite is the write pass of Generate.
func GenerateWrite(b *Buffers, entry *DrawOp, t, n, g uint32) {
	generate(b, entry, PassWrite, t, n)
}

// Generate draws the entry's chunks in the store pass, the pass the
// walk draws in, reading no round state: thread t of n takes its
// chunks by stride. Run as thread 0 of 1 it draws the entry's lines
// and steps in order, the walk's own order, so a schedule that draws
// entries one at a time in the list's order needs no rounds.
func Generate(b *Buffers, entry *DrawOp, t, n uint32) {
	generate(b, entry, PassStore, t, n)
}

// entryLineEnd is the end the entry sets on its line boundLine, in
// the bounds' units: LineEnd on the entry's last line when it is set,
// an end the walk had not reached when the entry closed, and -1
// otherwise, where the line's own record ends it.
func entryLineEnd(op *DrawOp, boundLine, lastLine int32) int32 {
	if boundLine == lastLine && op.LineEnd != 0 {
		return int32(op.LineEnd)
	}
	return -1
}

// generate is the body of the two passes: thread t of n draws its
// chunks of the entry in the pass.
func generate(b *Buffers, entry *DrawOp, pass, t, n uint32) {
	op := *entry
	op.Pixel.Pass = pass
	lines := OpLines(&op)
	if lines == 0 {
		return
	}
	// An op whose dots cannot land, wholly outside the system clip,
	// is not walked: at scale a part off the screen by a margin can
	// still hold millions of sub-dots.
	if _, _, _, _, ok := OpBox(&op); !ok {
		return
	}
	form := OpForm(&op)
	if form == OpErase {
		// An erase is dispatched on its own, between the rounds of
		// keyed ops it separates: its rows share no pixel, so the
		// threads take them by stride and store directly.
		for r := int32(t); r < lines; r += int32(n) {
			EraseRows(b.FB, &op, r, r+1)
		}
		return
	}
	perLine := (OpDots(&op) + GenChunk - 1) / GenChunk
	if perLine <= 0 {
		return
	}
	// The entry's bounds. A sprite's are scaled with it, and at scale
	// a sprite row is a sub-row of native row row >> ScaleShift. A
	// connecting line's or a segment's stay native, counting whole
	// blocks (native dots): at scale the lines drawn are the native
	// lines' sub-lines, line l in the block of native line l >>
	// ScaleShift, while a segment form's lines are its legs, which do
	// not multiply.
	shift := op.Pixel.ScaleShift
	blockBounds := shift != 0 && form != OpNormalSprite && form != OpScaledSprite
	var native DrawOp
	boundLines := lines
	boundShift, lineShift := shift, uint32(0)
	if blockBounds {
		native = nativeOp(&op)
		boundShift = 0
		if form == OpDistortedSprite || form == OpPolygon {
			boundLines = OpLines(&native)
			lineShift = shift
		}
	}
	from := op.LineFrom
	to := op.LineTo
	if to == 0 || to > uint32(boundLines) {
		to = uint32(boundLines)
	}
	if from >= to {
		return
	}
	firstLine := int32(from) >> boundShift
	lastLine := (int32(to) - 1) >> boundShift
	lineFrom := from << lineShift
	lineTo := min(to<<lineShift, uint32(lines))
	total := (lineTo - lineFrom) * uint32(perLine)
	for c := t; c < total; c += n {
		line := int32(lineFrom + c/uint32(perLine))
		j0 := int32(c%uint32(perLine)) * GenChunk
		key := LineKey(op.Seq, line)
		// The line's native line in the bounds' units: the line
		// itself, a sprite sub-row's native row, or the native line
		// whose block holds a connecting sub-line.
		boundLine := line >> boundShift
		if lineShift != 0 {
			boundLine = min(line>>lineShift, boundLines-1)
		}
		// The entry's bounds within its first and last lines: the
		// first line starts at LineStart, and the last ends at
		// LineEnd when set (entryLineEnd), so that line's record is
		// not final and is not read.
		start := int32(0)
		if boundLine == firstLine {
			start = int32(op.LineStart)
		}
		end := entryLineEnd(&op, boundLine, lastLine)
		switch form {
		case OpNormalSprite, OpScaledSprite:
			var st SpriteRow
			BeginSpriteRow(&op, &st, line)
			if end >= 0 {
				st.End = min(end, st.End)
			} else {
				st.End = min(RecordedLineEnd(b.Records, &op, line), st.End)
			}
			st.Key = key
			st.X = max(j0, start)
			x1 := min(j0+GenChunk, st.End)
			if st.X < x1 {
				SpriteRowSpan(b.VRAM, b.FB, b.Round, &op, &st, line, x1, UnboundedCycles, 1)
			}
		case OpDistortedSprite, OpPolygon:
			if blockBounds {
				// The line, and the line after it, bounded as the
				// entry bounds it when the entry holds it. A line
				// past the entry whose record is pending has no
				// known extent, so the line before it keeps the
				// sub-dots it may claim, and the entry drawn next
				// writes them a second time.
				var l squareLine
				var c squareGeom
				squareQuadLine(b, &l, &op, &native, form, line, boundLine, start, end)
				cut := false
				if line+1 < lines {
					nl := min((line+1)>>lineShift, boundLines-1)
					squareNextQuad(b, &c, &op, &native, form, line+1, nl, entryLineEnd(&op, nl, lastLine))
					cut = c.AxisX == l.G.AxisX && (!c.Pending || uint32(line+1) < lineTo)
				}
				if op.Kind == OpPolygon && op.Pixel.CC < 4 {
					squareFlatChunk(b, &op, &l, key, j0, cut, &c)
				} else {
					squareStepChunk(b, &op, &l, key, j0, cut, &c)
				}
			} else {
				var st QuadLine
				BeginQuadLine(&op, &st, line)
				if end >= 0 {
					st.JEnd = min(end-1, st.JEnd)
				} else if RecordStride(&op) != 0 {
					st.JEnd = min(RecordedLineEnd(b.Records, &op, line)-1, st.JEnd)
				}
				st.Key = key
				st.J = max(st.J, j0, start)
				j1 := min(j0+GenChunk, st.JEnd+1)
				if st.J < j1 {
					QuadLineSpan(b.VRAM, b.FB, b.Round, &op, &st, j1, UnboundedCycles, 1)
				}
			}
		case OpPolyline, OpLine:
			if blockBounds {
				var l squareLine
				var c squareGeom
				squareSegment(&l, &op, &native, form, line, start, end)
				if op.Pixel.CC < 4 {
					squareFlatChunk(b, &op, &l, key, j0, false, &c)
				} else {
					squareStepChunk(b, &op, &l, key, j0, false, &c)
				}
			} else {
				var st Segment
				x1, y1, x2, y2, gStart, gEnd := op.X[0], op.Y[0], op.X[1], op.Y[1], op.Gouraud[0], op.Gouraud[1]
				if op.Kind == OpPolyline {
					x1, y1, x2, y2, gStart, gEnd = PolylineLeg(&op, line)
				}
				BeginSegment(&st, x1, y1, x2, y2, gStart, gEnd)
				st.Key = key
				st.I = max(j0, start)
				i1 := min(j0+GenChunk, st.IMax+1)
				if end >= 0 {
					i1 = min(i1, end)
				}
				if st.I < i1 {
					SegmentSpan(b.FB, b.Round, &op, &st, i1, UnboundedCycles, 1)
				}
			}
		}
	}
}
