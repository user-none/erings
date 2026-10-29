// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/internal/hostgpu"
)

// The VDP1 on the device.
//
// The walk charges every op's cycles and stores its line records
// without drawing it (draw.DrawState.Timing), and the device draws
// the ops into its own pair from the VDP1's list; the walk draws
// into the CPU pair only after a CPU read of the frame buffer has
// asked for it (syncPair). After the VDP1's tick of every line the renderer
// takes the entries appended since its last take and the VRAM pages
// written since, into the region being filled, as one line record:
// the pages before the entries, since the tick read the memory after
// the line's writes landed. Every entry is complete at the take: the
// VDP1 closes the entry of the command in progress at the end of the
// tick, so an op the walk is inside spans entries, one per line,
// and the events the list takes between ticks land between them. The
// records of a region are encoded into its batch ahead of the VDP2
// rows.
//
// A batch's ops are drawn in rounds. A round is a run of ops drawn
// into one frame buffer by the two passes of the draw package's
// generate entries, GenerateTag and GenerateWrite: every
// op's dots tag their pixels with the op's line keys, and every op's
// dots are drawn again where the tag is theirs, so a pixel takes the
// round's last dot, which is the walk's order. The threads of a
// dispatch draw the round's ops of one size class, so a small sprite
// is not given the threads a full-screen part needs. A round ends
// where the order between ops is not in the stamps: at an erase, which
// is dispatched alone between its rounds; at a change of the frame
// buffer drawn into; and at a page band that holds a page an op of
// the round reads, since an op reads VRAM as of its own line, so the
// ops before the band draw before its copy and the ops after it draw
// after. A band no pending op reads copies at once, ahead of the
// round, which reads none of it.
//
// The order stamps are one buffer for every round and are cleared
// once per frame, after the op list restarts. An op's ordinal grows
// over the frame's list, so a stamp left by an earlier round is below
// every key of a later one: the tag pass raises it as if it were
// clear, and the write pass, which stores only where the stamp holds
// one of its own ops' keys, never takes it for a dot. The same holds
// for a stamp cleared between rounds, so the stamps can be replaced
// and cleared anywhere in the list: they hold one stamp per pixel of
// the frame buffer mode with the most pixels an op has drawn in, and
// grow when an op draws in a mode with more (grow).
//
// The bytes the CPU writes into a frame buffer reach the device pair
// on their own: the VDP1 shadows them with a per-byte mask, and a
// take carries the marked pages, applied under their masks ahead of
// the line's ops and behind the rounds before, since the walk landed
// them before the line's tick. A page copy of the CPU pair would
// carry the ops' own dots with it and blend the destination-reading
// ones twice.
//
// The composite binds the display half of the device pair
// (captureSprite). A CPU read of the frame buffer brings the CPU
// pair up to date from the device pair first (VDP1.waitPair,
// syncPair) and the walk keeps it from then on.
//
// The enhanced renderer's core (renderer_enhanced_gpu.go) draws the
// same takes into a pair at N times each axis while the walk keeps
// the CPU pair: each entry scaled (scaleOp), a destination-reading
// entry split on its native lines before it is scaled, and each CPU
// write's byte copied to its block.

// gpuVDP1LargeDots is the dots (lines by dots) above which an op is
// large. The size classes keep ops of like size in one dispatch, so
// the threads the device gives a dispatch's ops (GroupThreads) fit
// all of them.
const gpuVDP1LargeDots = 4096

// gpuStoreVDP1Pages is the slots of a region's VDP1 page store. A
// region takes a line's pages only while it can still hold a whole
// memory, so a take never overflows: a region that cannot is
// submitted first.
const gpuStoreVDP1Pages = compute.VDP1PageStoreBytes / compute.PageBytes

// gpuVDP1Records is the line records a region starts with: a record
// per line of the frame, with room for the splits at erases and
// buffer changes. The page copy params rows are gpuVDP1CopyKinds per
// record, the VRAM band and the frame buffer band of each half. Both
// grow when a frame's lines exceed them.
const (
	gpuVDP1Records   = 2 * rowRecordMax
	gpuVDP1CopyKinds = 3
)

// gpuOpSize is the bytes of an op in the device arrays, the Go
// struct's size, which the compile stage holds the device to.
const gpuOpSize = unsafe.Sizeof(draw.DrawOp{})

// gpuVDP1Line is one line record of a region: the VDP1 page slots
// taken at it, the op slots of each size class taken at it, the
// physical frame buffer those ops draw into, and, for the record an
// erase op is, the erase's slot in the large arrays (-1 otherwise).
// The pages and the chunks of its ops are gathered as the ops are put,
// so the encode never reads the op arrays, which the CPU writes and
// the device reads.
type gpuVDP1Line struct {
	vram   [2]int
	fb     [2][2]int // the slots of the CPU frame buffer writes taken at the line, by physical buffer
	ops    [2][2]int
	erase  int
	buffer uint32
	frags  uint32 // the fragments the record's destination-reading ops can append, at most the capacity
	// fbCopy is the params of the record's copies of CPU frame buffer
	// writes into each half but the base: the scale shift, the frame
	// buffer's native width and pixel size as the take found them,
	// and the half's layout (VDP2.pageCopyParams).
	fbCopy [2]draw.PageCopyParams
	band   gpuPageSet   // the VDP1 pages of the VRAM band taken at the line
	pages  gpuPageSet   // the pages the record's ops read (addOpPages)
	chunks [2]gpuChunks // by class, the chunks of the record's ops, an erase record's in the large class
}

// gpuChunks is the chunks of a set of ops (draw.OpChunks): the largest
// op's and every op's together, each held under 2^31.
type gpuChunks struct {
	most, sum int
}

// add counts an op of n chunks.
func (c *gpuChunks) add(n uint32) {
	m := int(min(n, math.MaxInt32))
	c.most = max(c.most, m)
	c.sum = min(c.sum+m, math.MaxInt32)
}

// union counts every op o counts.
func (c *gpuChunks) union(o gpuChunks) {
	c.most = max(c.most, o.most)
	c.sum = min(c.sum+o.sum, math.MaxInt32)
}

// hasOps reports whether the record holds ops of either class.
func (r *gpuVDP1Line) hasOps() bool {
	return r.ops[0][1] > r.ops[0][0] || r.ops[1][1] > r.ops[1][0]
}

// addOp gathers into the record the pages and the chunks of op, put in
// the class's array.
func (r *gpuVDP1Line) addOp(op *draw.DrawOp, class int) {
	r.pages.addOpPages(op)
	r.chunks[class].add(draw.OpChunks(op))
}

// gpuPageSet is a set of VDP1 VRAM pages.
type gpuPageSet [vdp1VRAMPages / 64]uint64

func (s *gpuPageSet) add(page uint32) { s[page>>6] |= 1 << (page & 63) }

// union adds every page of o.
func (s *gpuPageSet) union(o *gpuPageSet) {
	for i := range s {
		s[i] |= o[i]
	}
}

// intersects reports whether a page is in both s and o.
func (s *gpuPageSet) intersects(o *gpuPageSet) bool {
	for i := range s {
		if s[i]&o[i] != 0 {
			return true
		}
	}
	return false
}

// addRange adds the pages of the byte range [addr, addr+size) of
// VDP1 VRAM, wrapped to the memory: from the page of the first byte
// to the page of the last, every page when the range covers the
// memory.
func (s *gpuPageSet) addRange(addr, size uint32) {
	if size == 0 {
		return
	}
	if size >= vdp1VRAMSize {
		for i := range s {
			s[i] = ^uint64(0)
		}
		return
	}
	first := (addr & (vdp1VRAMSize - 1)) / compute.PageBytes
	last := ((addr + size - 1) & (vdp1VRAMSize - 1)) / compute.PageBytes
	for p := first; ; p = (p + 1) & (vdp1VRAMPages - 1) {
		s.add(p)
		if p == last {
			return
		}
	}
}

// addOpPages adds the pages an op reads: its character data and its
// color lookup table (draw.OpCharData, draw.OpColorTable).
func (s *gpuPageSet) addOpPages(op *draw.DrawOp) {
	s.addRange(draw.OpCharData(op))
	s.addRange(draw.OpColorTable(op))
}

// opClass is the size class whose arrays op goes in: large (1) above
// gpuVDP1LargeDots dots by lines, small (0) otherwise. An erase,
// dispatched alone, goes in the large arrays.
func opClass(op *draw.DrawOp) int {
	if op.Kind == draw.OpErase || int64(draw.OpLines(op))*int64(draw.OpDots(op)) > gpuVDP1LargeDots {
		return 1
	}
	return 0
}

// gpuVDP1Round is the round being gathered by the encode: its op
// slot ranges by class, the frame buffer it draws into, the pages
// its ops read, the fragments its ops can append, and by class the
// chunks of its ops.
type gpuVDP1Round struct {
	open   bool
	ops    [2][2]int
	buffer uint32
	pages  gpuPageSet
	frags  uint32
	chunks [2]gpuChunks
}

// growVDP1 gives region s op arrays of at least ops slots per class
// and a line record buffer of at least recWords words, releasing the
// ones it had. The region is idle: its buffers are grown as it is
// filled or submitted, after the wait on the submission that last
// read it.
func (g *gpuRenderer) growVDP1(s *gpuStaging, ops, recWords int) error {
	if ops > s.opCap {
		for c := range s.ops {
			b, err := g.prog.Device.NewBuffer(ops*int(gpuOpSize), hostgpu.BufferUpload)
			if err != nil {
				return err
			}
			if old := s.ops[c]; old != nil {
				copy(b.Bytes(), old.Bytes())
				g.releaseBuffer(old)
			}
			s.ops[c] = b
			g.all = append(g.all, b)
		}
		s.opCap = ops
	}
	if recWords > s.recWords {
		b, err := g.prog.Device.NewBuffer(recWords*4, hostgpu.BufferUpload)
		if err != nil {
			return err
		}
		if s.rec != nil {
			g.releaseBuffer(s.rec)
		}
		s.rec = b
		s.recWords = recWords
		g.all = append(g.all, b)
	}
	return nil
}

// releaseBuffer releases a buffer the renderer replaced and drops it
// from the list close releases.
func (g *gpuRenderer) releaseBuffer(b hostgpu.Buffer) {
	for i, held := range g.all {
		if held == b {
			g.all = append(g.all[:i], g.all[i+1:]...)
			break
		}
	}
	b.Release()
}

// vdp1Line runs after the VDP1's tick of a line: it takes the VRAM
// pages written since the last take and the ops completed since the
// last take into the region being filled, as the line's records. A
// region that cannot hold a whole memory of pages is submitted first.
func (g *gpuRenderer) vdp1Line(int) {
	g.takeVDP1(false)
}

// takeVDP1 is the take of vdp1Line: the VRAM pages and CPU frame
// buffer writes since the last take, then the entries the shared take
// yields (opTake), staged into the region as the line's records. The
// batch's line records are copied when it is submitted, so the take's
// wait for final records holds for the device as it does for the
// software renderer. At the end of the display, before the list
// restarts, every entry is taken as it stands.
func (g *gpuRenderer) takeVDP1(all bool) {
	v, v2 := g.vdp1, g.vdp2
	s := &g.staging[g.cur]
	if s.vdp1Slots+vdp1VRAMPages > gpuStoreVDP1Pages || s.fbSlots+2*vdp1FBPages > gpuStoreVDP1Pages {
		g.submitBatch()
		s = &g.staging[g.cur]
	}
	take := v2.beginOps(v, g.enhanced, all)
	if take.end > v.opsTaken && len(v.lineRecords) > s.recWords {
		if err := g.growVDP1(s, s.opCap, len(v.lineRecords)); err != nil {
			g.failure = err
			return
		}
	}
	rec := g.newVDP1Line(s)
	g.pages = v.vramDirty.take(g.pages[:0])
	for _, p := range g.pages {
		s.vdp1Pages[s.vdp1Slots] = p
		copy(s.vdp1Bytes[s.vdp1Slots*compute.PageBytes:], v.vram[p*compute.PageBytes:(p+1)*compute.PageBytes])
		s.vdp1Slots++
		rec.band.add(p)
	}
	rec.vram[1] = s.vdp1Slots
	for h := range rec.fb {
		rec.fb[h][0] = s.fbSlots
		s.fbSlots, g.pages = v.takeFBPages(uint32(h), s.fbSlots, s.fbPages, s.fbMaskWords, s.fbBytes, g.pages)
		rec.fb[h][1] = s.fbSlots
	}
	var scaled draw.DrawOp
	for op, ok := take.next(&scaled); ok; op, ok = take.next(&scaled) {
		// A frame buffer holds at most one pixel per byte, which bounds
		// the op's pixels whatever its params hold.
		if px := min(int(op.Pixel.FBWidth)*int(op.Pixel.FBHeight), compute.VDP1FBSize); px > g.fbPixels {
			g.fbPixels = px
			if err := g.grow(); err != nil {
				g.failure = err
				return
			}
		}
		// The enhanced renderer draws the entry as the take derived
		// it for its pair (deriveOp); the line records stay the native
		// op's.
		dev := op
		if g.enhanced {
			dev = &scaled
		}
		class := opClass(dev)
		if op.Kind == draw.OpErase {
			g.pushVDP1Line(s, &rec)
			slot := g.putOp(s, dev, class)
			erase := gpuVDP1Line{erase: slot, buffer: op.Buffer}
			erase.chunks[class].add(draw.OpChunks(dev))
			s.lines = append(s.lines, erase)
			rec = g.newVDP1Line(s)
			continue
		}
		if rec.hasOps() && rec.buffer != op.Buffer {
			g.pushVDP1Line(s, &rec)
			rec = g.newVDP1Line(s)
		}
		rec.buffer = op.Buffer
		if n := draw.OpRecordWords(op); n > 0 {
			s.recLo = min(s.recLo, op.RecordBase)
			s.recHi = max(s.recHi, op.RecordBase+n)
		}
		if !draw.ReadsDestination(&op.Pixel) {
			g.putOp(s, dev, class)
			rec.ops[class][1] = s.opSlots[class]
			rec.addOp(dev, class)
			continue
		}
		g.putPieces(s, &rec, op, dev, class)
	}
	g.pushVDP1Line(s, &rec)
}

// syncPair brings the CPU pair up to date with the device pair
// through line: the entries and pages appended since the last take
// go into the region as one more record, the batch is submitted,
// every submission is waited for, both device halves are copied into
// pairStage in a submission waited for at once, and from it into
// the buffers they mirror. It runs at shift 0 alone, where both halves
// fit pairStage. The frame's last submission stays recorded, so
// endFrame still waits and reads the frame back.
func (g *gpuRenderer) syncPair(line int) {
	g.vdp1Line(line)
	g.submitBatch()
	if g.sub != 0 {
		if err := g.prog.Device.Wait(g.sub); err != nil {
			g.failure = err
		}
	}
	if g.failure != nil {
		return
	}
	if err := g.waitedSubmit(func(cmds hostgpu.Commands) {
		for h := range g.vdp1FB {
			cmds.Copy(g.pairStage, h*compute.VDP1FBSize, g.vdp1FB[h], 0, compute.VDP1FBSize)
		}
	}); err != nil {
		g.failure = err
		return
	}
	for h := range g.vdp1FB {
		copy(g.vdp1.physicalFB(uint32(h)), g.pairStage.Bytes()[h*compute.VDP1FBSize:(h+1)*compute.VDP1FBSize])
	}
}

// gpuLineFragments is the bound on the fragments one native line of an
// entry appends, op being the entry as the device draws it at the
// scale shift: two per step of each line drawn in the native line's
// place (generate) at native, a sprite row's N sub-rows at scale, and
// at scale a connecting line's or a segment's dots' squares: a
// connecting line is N sub-lines, each writing at most 2N - 1
// sub-dots per column of its OpDots columns (square.go), within
// 2N times its dots for each sub-line, and a segment is one such line.
func gpuLineFragments(op *draw.DrawOp, shift uint32) uint32 {
	n := uint32(1) << shift
	per := 2 * uint32(max(draw.OpDots(op), 1))
	switch draw.OpForm(op) {
	case draw.OpDistortedSprite, draw.OpPolygon:
		return per * n * n
	}
	return per * n
}

// gpuStepFragments is the bound on the fragments k native steps of one
// native line of an entry append at the scale shift, the steps being a
// connecting line's or a segment's native steps or a sprite row's
// native columns: 2N per column of each of the N sub-lines, the most
// sub-dots a column's squares hold (square.go), over at most 2N
// sub-steps in each step's block plus 5N more. A sub-step belongs to
// the native step whose block holds the far corner of its square
// along the native line's major axis, and the block is N sub-dots
// along it. A sub-line's end points lie in the blocks of the native
// line's, so when the native line spans at least 4 dots along its
// major axis the sub-line moves at least 3N/5 along that axis per N
// sub-steps and a block holds at most 2N of them; a shorter line has
// at most 7N - 4 sub-steps in all. The first and last steps take the
// sub-steps before and after the native line, which the 5N holds
// with the N - 1 columns past the last dot. A segment's leg and a
// sprite row hold N sub-dots per native step, well under it.
func gpuStepFragments(k, shift uint32) uint32 {
	n := uint32(1) << shift
	return 2 * n * n * (2*n*k + 5*n)
}

// putPieces puts a destination-reading entry op, dev as the device
// draws it, into the region in pieces whose fragment bound fits the
// round capacity, pushing the record rec and starting another where
// its pieces would pass it, so the encode can hold every round under
// the capacity. The pieces are pieces of the native entry, scaled
// when the renderer is enhanced, so each keeps the native line bounds
// generate reads. A native line fits the capacity at every scale for
// the ops the walk makes at native, and pieces are runs of whole
// lines, the entry's first-line start and last-line end staying with
// the pieces holding those lines. A native line whose bound passes the
// capacity, which only a scaled entry reaches, goes in pieces of its
// native steps (gpuStepFragments), each bounded by LineStart and
// LineEnd and ended where the walk's record ends the line
// (pieceEnd).
func (g *gpuRenderer) putPieces(s *gpuStaging, rec *gpuVDP1Line, op, dev *draw.DrawOp, class int) {
	lines := uint32(draw.OpLines(op))
	first, last := op.LineFrom, op.LineTo
	if last == 0 || last > lines {
		last = lines
	}
	var piece, scaled draw.DrawOp
	scale := func(p *draw.DrawOp) *draw.DrawOp {
		if !g.enhanced {
			return p
		}
		deriveOp(&scaled, p, g.shift, g.vdp2.enhance.half[p.Buffer&1])
		return &scaled
	}
	if perLine := gpuLineFragments(dev, g.shift); perLine <= g.fragmentCap {
		per := g.fragmentCap / perLine
		for from := first; from < last; from += per {
			to := min(from+per, last)
			piece = *op
			piece.LineFrom, piece.LineTo = from, to
			if from != first {
				piece.LineStart = 0
			}
			if to != last {
				piece.LineEnd = 0
			}
			g.putPiece(s, rec, scale(&piece), class, (to-from)*perLine)
		}
		return
	}
	n := uint32(1) << g.shift
	per := uint32(1)
	if c := g.fragmentCap / (2 * n * n); c > 7*n {
		per = (c - 5*n) / (2 * n)
	}
	steps := uint32(draw.OpDots(op))
	for l := first; l < last; l++ {
		a, end := uint32(0), steps
		if l == first {
			a = op.LineStart
		}
		open := l == last-1 && op.LineEnd != 0
		if open {
			end = min(op.LineEnd, steps)
		}
		for ; a < end; a += per {
			piece = *op
			piece.LineFrom, piece.LineTo = l, l+1
			piece.LineStart, piece.LineEnd = a, min(a+per, end)
			d := scale(&piece)
			if !open && !g.pieceEnd(d) {
				break
			}
			g.putPiece(s, rec, d, class, gpuStepFragments(piece.LineEnd-a, g.shift))
		}
	}
}

// pieceEnd ends d, a one-line piece of an entry split by its steps as
// the device draws it, where the walk's record ends the line, a line
// the walk completed: generate takes a piece's LineEnd in place of
// the record, so the record's cut at the second end code is applied
// here (draw.RecordedLineEnd, the line being the piece's own, the
// scaled row of a sprite). A kind with no records keeps its end. It
// reports whether the piece still holds a step; the pieces after one
// that does not hold none either.
func (g *gpuRenderer) pieceEnd(d *draw.DrawOp) bool {
	if draw.RecordStride(d) != 0 {
		end := draw.RecordedLineEnd(g.vdp1.lineRecords, d, int32(d.LineFrom))
		d.LineEnd = uint32(max(min(int32(d.LineEnd), end), 0))
	}
	return d.LineEnd > d.LineStart
}

// putPiece puts piece, whose fragment bound is bound, into the region
// in record rec, pushing rec and starting another first when the
// piece would pass the capacity.
func (g *gpuRenderer) putPiece(s *gpuStaging, rec *gpuVDP1Line, piece *draw.DrawOp, class int, bound uint32) {
	if rec.frags+bound > g.fragmentCap && rec.hasOps() {
		g.pushVDP1Line(s, rec)
		*rec = g.newVDP1Line(s)
		rec.buffer = piece.Buffer
	}
	g.putOp(s, piece, class)
	rec.ops[class][1] = s.opSlots[class]
	rec.addOp(piece, class)
	rec.frags += bound
}

// newVDP1Line starts a line record at the region's current slots.
func (g *gpuRenderer) newVDP1Line(s *gpuStaging) gpuVDP1Line {
	v := g.vdp1
	rec := gpuVDP1Line{erase: -1}
	rec.vram = [2]int{s.vdp1Slots, s.vdp1Slots}
	for h := range rec.fb {
		rec.fbCopy[h] = g.vdp2.pageCopyParams(v, uint32(h))
		rec.fb[h] = [2]int{s.fbSlots, s.fbSlots}
	}
	for c := range rec.ops {
		rec.ops[c] = [2]int{s.opSlots[c], s.opSlots[c]}
	}
	return rec
}

// hasBands reports whether the record holds a VRAM band or a frame
// buffer band.
func (r *gpuVDP1Line) hasBands() bool {
	return r.vram[1] > r.vram[0] || r.fb[0][1] > r.fb[0][0] || r.fb[1][1] > r.fb[1][0]
}

// pushVDP1Line appends the record when it holds a band or ops.
func (g *gpuRenderer) pushVDP1Line(s *gpuStaging, rec *gpuVDP1Line) {
	if rec.hasBands() || rec.hasOps() {
		s.lines = append(s.lines, *rec)
	}
}

// putOp writes op into the region's array of its class, once, the
// pass being the dispatch's (dispatchGenerate), and returns its
// slot. An array with no slot free is grown first; when that fails
// the failure is recorded, nothing is written, and the slot is -1.
func (g *gpuRenderer) putOp(s *gpuStaging, op *draw.DrawOp, class int) int {
	slot := s.opSlots[class]
	if slot == s.opCap {
		if err := g.growVDP1(s, 2*s.opCap, s.recWords); err != nil {
			g.failure = err
			return -1
		}
	}
	s.opSlots[class]++
	*(*draw.DrawOp)(unsafe.Pointer(&s.ops[class].Bytes()[slot*int(gpuOpSize)])) = *op
	return slot
}

// encodeVDP1 encodes the region's line records: the page copies and
// the rounds, in the records' order, splitting a round at an erase, a
// buffer change, or a band an op of the round reads.
func (g *gpuRenderer) encodeVDP1(cmds hostgpu.Commands, s *gpuStaging) {
	if len(s.lines) == 0 {
		return
	}
	if gpuVDP1CopyKinds*len(s.lines) > s.copyRows {
		rows := gpuVDP1CopyKinds * len(s.lines)
		b, err := g.prog.Device.NewBuffer(rows*int(unsafe.Sizeof(draw.PageCopyParams{})), hostgpu.BufferUpload)
		if err != nil {
			g.failure = err
			return
		}
		g.releaseBuffer(s.vdp1Copy)
		s.vdp1Copy, s.copyRows = b, rows
		g.all = append(g.all, b)
	}
	var round gpuVDP1Round
	for i := range s.lines {
		r := &s.lines[i]
		if r.erase >= 0 {
			g.flushVDP1(cmds, s, &round)
			g.dispatchGenerate(cmds, s, 1, 1, r.erase, r.erase+1, r.buffer, r.chunks[1])
			cmds.Barrier()
			continue
		}
		if r.vram[1] > r.vram[0] {
			if round.open && r.band.intersects(&round.pages) {
				g.flushVDP1(cmds, s, &round)
			}
			g.encodeVDP1Copy(cmds, s, gpuVDP1CopyKinds*i, r.vram)
			cmds.Barrier()
		}
		// The CPU's frame buffer writes of the line land before the
		// line's ops, which drew over them on the walk, and after the
		// rounds before them, whose dots they may have written over.
		if r.fb[0][1] > r.fb[0][0] || r.fb[1][1] > r.fb[1][0] {
			g.flushVDP1(cmds, s, &round)
			for h := range r.fb {
				g.encodeFBCopy(cmds, s, gpuVDP1CopyKinds*i+1+h, uint32(h), r.fb[h], r.fbCopy[h])
			}
			cmds.Barrier()
		}
		if !r.hasOps() {
			continue
		}
		if round.open && round.buffer != r.buffer {
			g.flushVDP1(cmds, s, &round)
		}
		if round.open && round.frags+r.frags > g.fragmentCap {
			g.flushVDP1(cmds, s, &round)
		}
		if !round.open {
			round = gpuVDP1Round{open: true, ops: r.ops, buffer: r.buffer}
		} else {
			for c := range r.ops {
				round.ops[c][1] = r.ops[c][1]
			}
		}
		round.frags += r.frags
		round.pages.union(&r.pages)
		for c := range r.chunks {
			round.chunks[c].union(r.chunks[c])
		}
	}
	g.flushVDP1(cmds, s, &round)
}

// flushVDP1 encodes the round: the stamp clear when the frame's list
// has restarted since the last round and the list clear when the
// round has destination-reading ops, the tag pass over each class,
// the write pass over each class, and the resolve when the round
// has fragments, a barrier between the stages, and closes it.
func (g *gpuRenderer) flushVDP1(cmds hostgpu.Commands, s *gpuStaging, round *gpuVDP1Round) {
	if !round.open {
		return
	}
	cleared := false
	if g.stampsStale {
		g.dispatchIndex(cmds, entryClearStamps, g.bind(g.stamps, g.heads, g.pool), g.stamps.Size()/4, 0, 0)
		g.stampsStale = false
		cleared = true
	}
	if round.frags > 0 {
		g.dispatchIndex(cmds, entryClearLists, g.bind(g.stamps, g.heads, g.pool), g.heads.Size()/4, 0, 0)
		cleared = true
	}
	if cleared {
		cmds.Barrier()
	}
	for pass := 0; pass < 2; pass++ {
		g.dispatchGenerate(cmds, s, 0, pass, round.ops[0][0], round.ops[0][1], round.buffer, round.chunks[0])
		g.dispatchGenerate(cmds, s, 1, pass, round.ops[1][0], round.ops[1][1], round.buffer, round.chunks[1])
		cmds.Barrier()
	}
	if round.frags > 0 {
		g.dispatchIndex(cmds, entryResolve, g.bind(g.vdp1FB[round.buffer&1], g.stamps, g.heads, g.pool), g.heads.Size()/4, 0, 0)
		cmds.Barrier()
	}
	round.open = false
}

// dispatchGenerate draws the ops at slots [from, to) of the class's
// array into the frame buffer in the pass, the tag pass (0) or the
// write pass (1) being the entry point dispatched, a group of
// threads across per op, the op as the group's params row. chunks is
// the ops' chunks, from which and the op count the device sizes the
// group (GroupThreads).
func (g *gpuRenderer) dispatchGenerate(cmds hostgpu.Commands, s *gpuStaging, class, pass, from, to int, buffer uint32, chunks gpuChunks) {
	if to <= from {
		return
	}
	threads := g.prog.Device.GroupThreads(hostgpu.GroupLoad{OpCount: to - from, MaxChunks: chunks.most, SumChunks: chunks.sum})
	rec := hostgpu.Dispatch{
		BoundX:       uint32(threads),
		BoundY:       uint32(to - from),
		ParamsOffset: uint32(from) * uint32(gpuOpSize),
		ParamsStride: uint32(gpuOpSize),
	}
	entry := entryGenerateTag
	if pass == 1 {
		entry = entryGenerateWrite
	}
	g.dispatch(cmds, entry, g.bind(g.vdp1VRAM, g.vdp1FB[buffer&1], g.stamps, g.heads, g.pool, s.rec, s.ops[class]), rec, [2]int{threads, to - from})
}

// gpuFragmentPoolWords is the fragment pool's length, the capacity
// of a round's destination-reading dots.
const gpuFragmentPoolWords = 1 << 21

// encodeVDP1Copy copies the band's staged VDP1 pages, the store slots
// [start, end), into the device VRAM: one thread per word, the params
// row the record's index.
func (g *gpuRenderer) encodeVDP1Copy(cmds hostgpu.Commands, s *gpuStaging, row int, slots [2]int) {
	n := slots[1] - slots[0]
	if n == 0 {
		return
	}
	if row >= s.copyRows {
		g.failure = fmt.Errorf("VDP1 line record %d past the copy params rows", row)
		return
	}
	params := unsafe.Slice((*draw.PageCopyParams)(unsafe.Pointer(&s.vdp1Copy.Bytes()[0])), s.copyRows)
	params[row] = draw.PageCopyParams{Base: uint32(slots[0])}
	g.dispatchIndex(cmds, entryCopyVDP1, g.bind(g.vdp1VRAM, s.vdp1, s.vdp1Table, s.vdp1Copy), n*compute.PageWords, uintptr(row), unsafe.Sizeof(params[0]))
}

// encodeFBCopy copies the bytes the CPU wrote into frame buffer half
// half, the staged pages at store slots [start, end) under their
// masks, into the device half: one thread per native byte, which
// writes its block at scale, the params row the record's own, p with
// the band's base.
func (g *gpuRenderer) encodeFBCopy(cmds hostgpu.Commands, s *gpuStaging, row int, half uint32, slots [2]int, p draw.PageCopyParams) {
	n := slots[1] - slots[0]
	if n == 0 {
		return
	}
	if row >= s.copyRows {
		g.failure = fmt.Errorf("VDP1 line record %d past the copy params rows", row)
		return
	}
	params := unsafe.Slice((*draw.PageCopyParams)(unsafe.Pointer(&s.vdp1Copy.Bytes()[0])), s.copyRows)
	p.Base = uint32(slots[0])
	params[row] = p
	g.dispatchIndex(cmds, entryCopyFB, g.bind(g.vdp1FB[half&1], s.fbStore, s.fbMasks, s.fbTable, s.vdp1Copy), n*compute.PageBytes, uintptr(row), unsafe.Sizeof(params[0]))
}
