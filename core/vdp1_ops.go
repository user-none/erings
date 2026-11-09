// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
)

// vdp1RecordCapacity is the initial reservation, not a limit. The
// retained buffer grows when a workload reserves more line records.
const vdp1RecordCapacity = 1 << 20

// vdp1OpCapacity is the op list's initial capacity, not a limit. The
// retained list grows when a frame starts more ops.
const vdp1OpCapacity = 4096

// The frame's list: the entries of the drawing commands' ops and the
// erases, in the walk's order, each with its ordinal in the frame
// and the physical frame buffer it draws into. The list restarts with
// the line records at the end of the display. A renderer drawing the
// frame on a device takes the entries from here in the walk's order.
//
// A command's op is in the list as entries (draw.DrawOp): the walk
// opens one as it advances the command (openEntry) and closes it at
// the end of every tick (closeEntry), so every entry is complete
// when a renderer takes it, and every event the list takes between
// ticks, an erase, a frame buffer change, an abandoned command, lands
// between the entries of the command it interrupts in the walk's
// order. The next tick's advance opens the remainder as a new entry,
// drawn into the buffer the walk draws into then.

// appendOp gives op the next ordinal and the physical index of the
// frame buffer it draws into and appends it.
func (v *VDP1) appendOp(op *draw.DrawOp, fb []byte) {
	op.Seq = v.opSeq
	v.opSeq++
	op.Buffer = v.physicalBuffer(fb)
	v.ops = append(v.ops, *op)
}

// openEntry appends an entry for the command in progress from the
// walk's position: its lines from the state's line, the first from
// the state's step, to the op's last.
func (v *VDP1) openEntry() {
	e := v.cmd.op
	e.LineFrom, e.LineStart = drawPosition(&v.cmd.st)
	e.LineTo, e.LineEnd = 0, 0
	v.appendOp(&e, v.drawFB)
	v.cmdOpen = true
}

// closeEntry ends the open entry of the command in progress at the
// walk's position: through the line before the state's line when the
// state is at a line's start, else through the state's line to its
// step. A completed command's entry already ends at the op's last
// line. An entry the walk did not advance is dropped.
func (v *VDP1) closeEntry() {
	if !v.cmdOpen {
		return
	}
	v.cmdOpen = false
	if v.cmd.st.Done {
		return
	}
	e := &v.ops[len(v.ops)-1]
	line, step := drawPosition(&v.cmd.st)
	if line == e.LineFrom && step == e.LineStart {
		v.ops = v.ops[:len(v.ops)-1]
		return
	}
	if step == 0 {
		e.LineTo = line
		return
	}
	e.LineTo, e.LineEnd = line+1, step
}

// drawPosition is the walk's position in the command in progress:
// the next line and, when the line's state is set up, the next step
// of it, the sprite column, the connecting line step, or the segment
// dot by the op's form.
func drawPosition(st *draw.DrawState) (line, step uint32) {
	if !st.Begun {
		return uint32(st.Line), 0
	}
	switch st.Form {
	case draw.OpNormalSprite, draw.OpScaledSprite:
		step = uint32(st.Sprite.X)
	case draw.OpDistortedSprite, draw.OpPolygon:
		step = uint32(st.Quad.J)
	case draw.OpPolyline, draw.OpLine:
		step = uint32(st.Seg.I)
	}
	return uint32(st.Line), step
}

// The draw op. The command list walk resolves every drawing command
// into a DrawOp, the record the compute package's Draw takes, and
// draws it as the walk starts it, in budgeted pieces.

// beginOp fills op with the fields every drawing command shares: the
// kind, the command address (procAddr advances only after the command
// completes), CMDCOLR, the pixel write parameters from the mode bits
// and the clip state, and the Gouraud table when the color
// calculation mode uses it.
func (v *VDP1) beginOp(op *draw.DrawOp, kind uint32, cmd *vdp1Command) {
	clipX, clipY := v.clipBounds()
	*op = draw.DrawOp{Kind: kind, Addr: v.procAddr, Color: uint32(cmd.colr)}
	op.Pixel = v.pixelParams(cmd.pmod&0x07, cmd.pmod&0x8000 != 0, cmd.pmod&0x0100 != 0, (cmd.pmod>>9)&3, clipX, clipY)
	if cmd.pmod&0x0800 == 0 {
		op.Flags |= draw.OpFlagPreClip
	}
	if cmd.pmod&0x07 >= 4 {
		gt := v.readGouraudTable(cmd.grda)
		for i := range gt {
			op.Gouraud[i] = uint32(gt[i])
		}
	}
}

// characterOp fills the sprite kinds' character fields and the flags
// the command carries: the character address and size, the color
// mode, end code detection, transparent dot drawing, and the flips.
func characterOp(op *draw.DrawOp, cmd *vdp1Command) {
	op.CharAddr = uint32(cmd.srca) * 8
	op.CharW = int32((cmd.size>>8)&0x3F) * 8
	op.CharH = int32(cmd.size & 0xFF)
	op.ColorMode = uint32((cmd.pmod >> 3) & 0x07)
	if cmd.pmod&0x0080 != 0 {
		op.Flags |= draw.OpFlagECDOff
	}
	if cmd.pmod&0x0040 != 0 {
		op.Flags |= draw.OpFlagSPDOn
	}
	if cmd.ctrl&0x0010 != 0 {
		op.Flags |= draw.OpFlagFlipH
	}
	if cmd.ctrl&0x0020 != 0 {
		op.Flags |= draw.OpFlagFlipV
	}
}

// quadOp fills the four vertices with the local coordinates applied.
func (v *VDP1) quadOp(op *draw.DrawOp, cmd *vdp1Command) {
	lx, ly := int32(v.localX), int32(v.localY)
	op.X = [4]int32{int32(cmd.xa) + lx, int32(cmd.xb) + lx, int32(cmd.xc) + lx, int32(cmd.xd) + lx}
	op.Y = [4]int32{int32(cmd.ya) + ly, int32(cmd.yb) + ly, int32(cmd.yc) + ly, int32(cmd.yd) + ly}
}

// The line records: Draw stores, for each line of an op that reads
// texels, where the line ended, at the op's record base as the line
// ends. Each op is reserved its own records as the walk starts
// drawing it, the bases the running sum of OpRecordWords over the
// frame's ops in order, and the reservations restart once per frame
// at the end of the display. The buffer grows before reservation when
// necessary. Reservations cover whole ops, including lines a
// restarted command never executes, so executed cycles cannot bound
// the required capacity.

// reserveRecords assigns the op being started its record base when
// the op has records, each record pending until the walk ends its
// line.
func (v *VDP1) reserveRecords(op *draw.DrawOp) {
	n := draw.OpRecordWords(op)
	if n == 0 {
		return
	}
	end := uint64(v.recordWords) + uint64(n)
	v.ensureRecords(end)
	op.RecordBase = v.recordWords
	v.recordWords = uint32(end)
	pendingRecords(v.lineRecords[op.RecordBase:end])
}

// entryRecordsFinal reports whether the records a renderer drawing
// the entry at scale reads are final. A distorted sprite's line at
// scale reads the record of the line after it, whose steps claim
// sub-dots of its own (square.go), so an entry needs the record of
// the line after its last, which the walk writes after the last's
// own, or of the last itself when it is the op's last line and the
// entry leaves it unfinished. An op the walk has left, complete or
// abandoned, writes no more records, so its entries are ready as they
// stand. Every other entry reads only the records of its own lines,
// which are final when the entry closes.
func (v *VDP1) entryRecordsFinal(op *draw.DrawOp) bool {
	if op.Kind != draw.OpDistortedSprite {
		return true
	}
	lines := draw.OpLines(op)
	if lines == 0 {
		return true
	}
	last := lines - 1
	if op.LineTo != 0 {
		last = int32(op.LineTo) - 1
	}
	need := last + 1
	if need >= lines {
		if op.LineEnd == 0 {
			return true
		}
		need = last
	}
	if draw.LoadLineEnd(v.lineRecords, op.RecordBase, need) >= 0 {
		return true
	}
	return v.cmdPhase != phaseDraw || v.cmd.op.RecordBase != op.RecordBase
}

// opsReady is the index past the last entry a renderer may take now:
// every entry when gate is clear, and otherwise the entries from
// opsTaken up to the first whose records are not final
// (entryRecordsFinal). The entries after that one wait with it, so a
// renderer takes the list in its order.
func (v *VDP1) opsReady(gate bool) int {
	if !gate {
		return len(v.ops)
	}
	end := v.opsTaken
	for end < len(v.ops) && v.entryRecordsFinal(&v.ops[end]) {
		end++
	}
	return end
}

// pendingRecords marks the records as pending.
func pendingRecords(rec compute.LineRecords) {
	pending := int32(draw.LineEndPending)
	for i := range rec {
		rec[i] = uint32(pending)
	}
}

// ensureRecords preserves all existing records and offsets on growth.
// Calls within retained capacity allocate nothing. The buffer doubles
// from its initial capacity, so its length stays a power of two, the
// mask its helpers need. Offsets are uint32 in the render contract,
// and a frame's reservations stay far inside them: records exist
// only for sprite ops, one per line, and restart every frame; every
// op costs at least its command fetch (vdp1CmdFetchWords cycles), so
// a frame of about 480k cycles starts at most about 32k ops; and an
// op's lines are bounded, 255 rows for a normal sprite, the clip
// height plus a charged row for each row outside it for a scaled
// sprite, and a cycle or more per line for a distorted sprite. The
// most a frame can reserve, scaled sprites with zero-width rows over
// the whole clip height at 8x, is about 2^28 words, an eighth of the
// offsets' range.
func (v *VDP1) ensureRecords(words uint64) {
	size := uint64(len(v.lineRecords))
	if words <= size {
		return
	}
	for size < words {
		size *= 2
	}
	buf := make(compute.LineRecords, int(size))
	copy(buf, v.lineRecords)
	v.lineRecords = buf
}

// restartList restarts the frame's list at the end of the display,
// with its record reservations and the count of its entries taken.
// The command in progress has no open entry between ticks; its
// remainder enters the new list as the entry the next advance opens.
func (v *VDP1) restartList() {
	v.dropEntries()
	v.opSeq = 0
	v.resetRecords()
}

// restartRestoredList restarts the list for a restored command: the
// entries of the frame before the load are dropped and the command's
// records reserved pending. The ordinals keep counting, so a
// renderer's winner keys from before the load, which no display end
// has cleared, rank below the restored command's entries.
func (v *VDP1) restartRestoredList() {
	v.dropEntries()
	v.reserveRestoredRecords()
}

// dropEntries empties the list, the open entry with it.
func (v *VDP1) dropEntries() {
	v.ops = v.ops[:0]
	v.opsTaken = 0
	v.cmdOpen = false
}

// resetRecords restarts the frame's record reservations at the end of
// the display. An unfinished command keeps its whole range and its
// completed line records: the range, reserved by reserveRecords within
// the buffer, moves to the front and the command's base follows it, so
// the frame that follows reserves from the end of that range and no
// storage below it is stranded.
func (v *VDP1) resetRecords() {
	v.recordWords = 0
	if v.cmdPhase != phaseDraw {
		return
	}
	op := &v.cmd.op
	n := draw.OpRecordWords(op)
	if n > 0 {
		base := op.RecordBase
		copy(v.lineRecords[:n], v.lineRecords[base:base+n])
	}
	op.RecordBase = 0
	v.recordWords = n
}

// reserveRestoredRecords reserves a restored command's range at the
// front of the buffer, growing a smaller buffer to hold it. A save
// state carries no records and no base, so every record of the range
// is pending until the resumed walk ends its line.
func (v *VDP1) reserveRestoredRecords() {
	v.recordWords = 0
	if v.cmdPhase != phaseDraw {
		return
	}
	op := &v.cmd.op
	n := draw.OpRecordWords(op)
	v.ensureRecords(uint64(n))
	pendingRecords(v.lineRecords[:n])
	op.RecordBase = 0
	v.recordWords = n
}
