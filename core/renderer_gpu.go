// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/backend"
)

// gpuBatchLines is the rows per submission the renderer opens with:
// it submits the rows recorded since its last submission once this
// many have ended, and whatever remains at the end of the display. The
// device renders a batch while the walker records the next, so the
// frame's end waits for the last batch alone. At or above rowRecordMax
// the frame is one submission at the end of the display.
const gpuBatchLines = 64

// gpuStagingRegions is the page staging regions the renderer cycles
// through: a batch's pages stay in their region until the device has
// read them, so the walker fills the next region while the batch
// runs and waits only when it comes back to a region whose batch is
// still in flight.
const gpuStagingRegions = 2

// gpuStoreVRAMPages and gpuStoreCRAMPages are the slots of a region's
// page stores. A region takes a row's pages only while it can still
// hold a whole memory of either, so a take never overflows: a region
// that cannot hold one is submitted first.
const (
	gpuStoreVRAMPages = compute.PageStoreBytes / compute.PageBytes
	gpuStoreCRAMPages = compute.CRAMPageStoreBytes / compute.PageBytes
)

// gpuCellSegment is the unit a cell span's thread takes whole, one
// cell, so the pattern name cache serves a whole cell. The gradation's
// unit is its whole row and the other spans' one dot. The dots a
// thread takes past its unit are the backend's (hostgpu.Dispatch).
const gpuCellSegment = screen.CellDots

// gpuCRAMModes is the CRAM modes RAMCTL selects (bits 13:12) and
// so the palette params rows: the row holds the mode alone, so the
// mode is the row and a frame's rebuilds never ask for another.
const gpuCRAMModes = 4

// gpuExtRow is an external row staged for the batch: the row slot it
// is staged at in extStage and the layer it is copied into at the
// same slot.
type gpuExtRow struct {
	slot, layer int
}

// gpuBand is the pages taken at one row's render point: the
// slot ranges [start, end) of each memory's store in the region, to
// copy before any row at or after line renders.
type gpuBand struct {
	line       int
	vram, cram [2]int
}

// gpuStaging is one page staging region: the page stores and page
// tables of VRAM and CRAM, the slots filled, the bands recorded, and
// the submission that last read the region.
type gpuStaging struct {
	vram, cram           hostgpu.Buffer
	vramTable, cramTable hostgpu.Buffer
	vramBytes, cramBytes []byte
	vramPages, cramPages []uint32
	vramSlots, cramSlots int
	bands                []gpuBand
	sub                  hostgpu.Submission

	// The row inputs of the batch (gpuRowInputs): the external rows
	// staged with the layer each copies into, and the row slots of each
	// rotation layer's line color rows the batch clears.
	extRows        []gpuExtRow
	lineColorSlots [2][]int

	// The VDP1 side (renderer_gpu_vdp1.go): the staged VDP1 VRAM
	// pages with their table, the op arrays by size class with the
	// slots filled and their capacity, the line records the
	// region's rounds bind, the records range the region's ops
	// reserve, the page copy params rows, and the line records the
	// region's batch encodes.
	vdp1, vdp1Table hostgpu.Buffer
	vdp1Bytes       []byte
	vdp1Pages       []uint32
	vdp1Slots       int
	fbStore         hostgpu.Buffer // the staged pages of CPU frame buffer writes, their masks, and their table
	fbMasks         hostgpu.Buffer
	fbTable         hostgpu.Buffer
	fbBytes         []byte
	fbMaskWords     []uint32
	fbPages         []uint32
	fbSlots         int
	ops             [2]hostgpu.Buffer
	opSlots         [2]int
	opCap           int
	rec             hostgpu.Buffer
	recWords        int
	recLo, recHi    uint32
	vdp1Copy        hostgpu.Buffer
	copyRows        int
	lines           []gpuVDP1Line
}

// gpuRenderer renders the frame on the host GPU in batches of rows
// as the walk goes: it owns the built compute program, the pipelines,
// and the buffers, takes each row's memory pages and sprite input at
// the row's render point, and turns the rows' records and pages into
// dispatches every batchRows rows, waiting for the last batch at
// the frame's end. It encodes no VDP behavior; the compute functions
// the entry points were compiled from do, and they are the functions
// the software renderer runs.
//
// The row records, the layer and line color buffers, and the page
// copy params hold row slots, not the frame: each staging region owns
// batchRows * maxRowSlots consecutive slots, and a batch's rows take
// its region's slots in order, a row's derived records in order. The
// page copy params hold two rows per row of the region, at every
// shift. The
// walker waits on a region's last submission before it fills the
// region again, which is also what frees the region's slots.
type gpuRenderer struct {
	vdp2        *VDP2
	vdp1        *VDP1
	prog        *hostgpu.Program
	backendName string                       // the backend prog runs on, as backend.Names lists it
	pipes       [entryCount]hostgpu.Pipeline // by entry constant

	// The VDP1's working buffers (renderer_gpu_vdp1.go): its VRAM,
	// advanced by the staged pages, the frame buffer pair by physical
	// index, drawn by the rounds, and the round state's three regions
	// in the group's field order, the order stamps of every round
	// stale when the op list has restarted since the last round, and
	// at open, since a new working buffer's contents are not defined.
	// fragmentCap is the fragments the pool holds, the bound on a
	// round.
	vdp1VRAM    hostgpu.Buffer
	vdp1FB      [2]hostgpu.Buffer
	stamps      hostgpu.Buffer
	heads       hostgpu.Buffer
	pool        hostgpu.Buffer
	fragmentCap uint32
	stampsStale bool

	// vram and cram are the device copies of VDP2 VRAM and CRAM, kept
	// across frames and advanced by the bands' page copies; rows the
	// row records by row slot; palette the decoded CRAM with its params
	// by CRAM mode; layers the six layer buffers by the chain's layer
	// index; lineColor the RBG coefficient line colors;
	// deviceFramebuffer the output the composite writes; copyParams the
	// page copies' params by row slot and memory. The composite's
	// sprite input is a half of the VDP1 device pair (captureSprite).
	// Batches in flight and the batch being recorded use disjoint row
	// slots, so one of each serves every batch; the page staging
	// cycles through regions. The buffers bufferSizes names are
	// allocated by grow, which replaces one when the frame needs more
	// than it holds. The views are the host's views of the row records
	// and the page copy params, rebuilt with them.
	//
	// Each buffer is allocated by the CPU's access to it. The page
	// stores and tables, the params rows, the op and line record
	// buffers, stage, and extStage are upload buffers: the CPU writes
	// them and the GPU reads them. pairStage is a readback buffer: a
	// copy writes it and the CPU reads it. The device frame buffer is
	// an output buffer: the composite writes it and the frontend reads
	// its bytes after the frame's last wait (framebuffer), and where
	// the bytes live between the two is the backend's. The device
	// copies, the palette, the layers, the line colors, and the VDP1's
	// buffers are working buffers, which the CPU never touches: bytes
	// reach them and leave them through Copy and Fill. stage carries
	// the VDP1's memory and pair to the device and pairStage the pair
	// back; extStage holds the external rows by row slot, laid out as
	// a layer, until the batch copies them into their layer.
	vram, cram        hostgpu.Buffer
	rows              hostgpu.Buffer
	palette           hostgpu.Buffer
	paletteParams     hostgpu.Buffer
	layers            [layerCount]hostgpu.Buffer
	lineColor         [2]hostgpu.Buffer
	deviceFramebuffer hostgpu.Buffer
	copyParams        hostgpu.Buffer
	stage             hostgpu.Buffer
	pairStage         hostgpu.Buffer
	extStage          hostgpu.Buffer
	rowView           []lineRecord
	extView           compute.Layer
	copyView          []screen.CopyParams
	staging           [gpuStagingRegions]gpuStaging
	all               []hostgpu.Buffer

	// batchRows is the rows per submission, at most rowRecordMax, and
	// the rows of each staging region. shift is the scale shift the
	// rows render at, 0 native; the rows render at the scaled width.
	// maxRowSlots is the bound on the row slots a row takes
	// (VDP2.maxRowSlots), one per derived record, 1 at native, which
	// rises once, at the first frame with two records per line
	// (beginFrame): a region owns batchRows * maxRowSlots slots from
	// its index times that, fixed before and after the rise. rowSlots
	// is the slots a row of the frame takes (VDP2.rowSlots), taken at
	// the frame start and at most maxRowSlots: a batch's rows pack by
	// it inside their region, so a run's rows are consecutive slots.
	// enhanced is set on the enhanced renderer's core: the upload
	// places the rows' derived records from subRows in place of the
	// VDP2's row records, and the VDP1's entries are drawn scaled
	// (scaleOp). subRows holds the derived records of the rows set up
	// and not yet submitted, row y's record k at (y mod batchRows) *
	// maxRowSlots + k. fbPixels is the native pixels of the largest
	// VDP1 frame buffer mode an op has drawn in, at their finest
	// addressing, which with the shift sizes the order stamps.
	batchRows   int
	shift       uint32
	maxRowSlots int
	rowSlots    int
	enhanced    bool
	subRows     []lineRecord
	fbPixels    int

	// The frame's progress: the region being filled, the first row of
	// the batch being recorded, the rows ended so far, the CRAM mode
	// the device palette was last built for (-1 when it has to be
	// rebuilt before the next rows), and the half of the VDP1 device
	// pair the frame's composite binds (captureSprite), nil until the
	// frame's first row decides. blankWords is the words of the device
	// frame buffer from its start the next submission blanks before
	// anything else, 0 for none; a frame that submits nothing blanks
	// them in a submission of its own at its end (endFrame).
	cur         int
	batchStart  int
	rowsEnded   int
	paletteMode int
	spriteBind  hostgpu.Buffer
	width       int // the frame's active dots per row at the scale, from BeginFrame
	blankWords  int

	// Per-frame working storage, reused so the frame path allocates
	// nothing once the slices have grown: the pages taken at a row,
	// the boundary list, the rows where the CRAM mode changes, the
	// row runs, and the buffer list bound by one dispatch.
	pages    []uint32
	bounds   []gpuBoundary
	modeRows []int
	runs     []gpuRun
	bufs     [16]hostgpu.Buffer

	sub     hostgpu.Submission // the frame's last submission; 0 when none
	failure error              // a runtime failure; the frame loop reports it
}

// ComputeSources are the compile stage's inputs: the parent compute
// package, then the VDP2 and VDP1 compute packages with their entry
// points. The ahead-of-time shader generator lowers the same list.
func ComputeSources() (parent hostgpu.Source, subs []hostgpu.Source) {
	const root = "github.com/user-none/erings/core/compute"
	return hostgpu.Source{Path: root, Files: compute.Source}, []hostgpu.Source{
		{Path: root + "/screen", Files: screen.Source, Entries: screen.Entries[:]},
		{Path: root + "/draw", Files: draw.Source, Entries: draw.Entries[:]},
	}
}

// openGPURenderer builds the compute sources on the named backend and
// makes the GPU renderer on it.
func openGPURenderer(v *VDP2, v1 *VDP1, name string) (*gpuRenderer, error) {
	parent, subs := ComputeSources()
	prog, err := backend.Open(name, parent, subs...)
	if err != nil {
		return nil, err
	}
	g, err := newGPURenderer(v, v1, prog, gpuBatchLines, false)
	if err != nil {
		return nil, err
	}
	g.backendName = name
	return g, nil
}

// newGPURenderer takes the compute sources built on a device,
// resolves the entry points, allocates the buffers, and copies the
// VDP1's VRAM to the device, and its frame buffers for the accurate
// renderer or, for the enhanced renderer's core (enhanced set),
// clears the device pair, which starts empty there. Every page of
// VDP2 VRAM and CRAM is marked, so the first row take delivers both
// whole into the device copies. It submits every batchRows rows, at
// least 1 and at most rowRecordMax, and renders the rows at the VDP2's
// configured scale shift, which is 0 for the accurate renderer, opened
// with every enhancement off. On failure the device is closed.
func newGPURenderer(v *VDP2, v1 *VDP1, prog *hostgpu.Program, batchRows int, enhanced bool) (*gpuRenderer, error) {
	g := &gpuRenderer{
		vdp2: v, vdp1: v1, prog: prog, paletteMode: -1, stampsStale: true,
		batchRows:   min(max(batchRows, 1), rowRecordMax),
		shift:       v.enhance.cfg.shift,
		enhanced:    enhanced,
		maxRowSlots: v.maxRowSlots(),
		rowSlots:    v.rowSlots(),
		fbPixels:    v1.fbWidth() * v1.fbHeight(),
	}
	var err error
	for i, name := range entryNames {
		if g.pipes[i], err = prog.Pipeline(name); err != nil {
			g.close()
			return nil, err
		}
	}
	dev := prog.Device
	buffer := func(dst *hostgpu.Buffer, size int, kind hostgpu.BufferKind) {
		if err != nil {
			return
		}
		*dst, err = dev.NewBuffer(size, kind)
		if err == nil {
			g.all = append(g.all, *dst)
		}
	}
	slots := g.batchRows * g.maxRowSlots
	buffer(&g.vram, compute.VDP2VRAMSize, hostgpu.BufferWorking)
	buffer(&g.cram, compute.CRAMSize, hostgpu.BufferWorking)
	buffer(&g.palette, compute.PaletteWords*4, hostgpu.BufferWorking)
	buffer(&g.paletteParams, gpuCRAMModes*int(unsafe.Sizeof(screen.PaletteParams{})), hostgpu.BufferUpload)
	buffer(&g.vdp1VRAM, compute.VDP1VRAMSize, hostgpu.BufferWorking)
	buffer(&g.stage, compute.VDP1VRAMSize, hostgpu.BufferUpload)
	buffer(&g.pairStage, 2*compute.VDP1FBSize, hostgpu.BufferReadback)
	for i := range g.staging {
		s := &g.staging[i]
		buffer(&s.vram, compute.PageStoreBytes, hostgpu.BufferUpload)
		buffer(&s.cram, compute.CRAMPageStoreBytes, hostgpu.BufferUpload)
		buffer(&s.vramTable, compute.PageTableWords*4, hostgpu.BufferUpload)
		buffer(&s.cramTable, compute.PageTableWords*4, hostgpu.BufferUpload)
		buffer(&s.vdp1, compute.VDP1PageStoreBytes, hostgpu.BufferUpload)
		buffer(&s.vdp1Table, compute.PageTableWords*4, hostgpu.BufferUpload)
		buffer(&s.fbStore, compute.VDP1PageStoreBytes, hostgpu.BufferUpload)
		buffer(&s.fbMasks, compute.MaskStoreWords*4, hostgpu.BufferUpload)
		buffer(&s.fbTable, compute.PageTableWords*4, hostgpu.BufferUpload)
		buffer(&s.vdp1Copy, gpuVDP1CopyKinds*gpuVDP1Records*int(unsafe.Sizeof(draw.PageCopyParams{})), hostgpu.BufferUpload)
	}
	if err == nil {
		err = g.grow()
	}
	if err != nil {
		g.close()
		return nil, err
	}
	for i := range g.staging {
		s := &g.staging[i]
		s.extRows = make([]gpuExtRow, 0, slots)
		for rbg := range s.lineColorSlots {
			s.lineColorSlots[rbg] = make([]int, 0, slots)
		}
		s.vramBytes, s.cramBytes = s.vram.Bytes(), s.cram.Bytes()
		s.vramPages = unsafe.Slice((*uint32)(unsafe.Pointer(&s.vramTable.Bytes()[0])), compute.PageTableWords)
		s.cramPages = unsafe.Slice((*uint32)(unsafe.Pointer(&s.cramTable.Bytes()[0])), compute.PageTableWords)
		s.bands = make([]gpuBand, 0, g.batchRows)
		s.vdp1Bytes = s.vdp1.Bytes()
		s.vdp1Pages = unsafe.Slice((*uint32)(unsafe.Pointer(&s.vdp1Table.Bytes()[0])), compute.PageTableWords)
		s.fbBytes = s.fbStore.Bytes()
		s.fbMaskWords = unsafe.Slice((*uint32)(unsafe.Pointer(&s.fbMasks.Bytes()[0])), compute.MaskStoreWords)
		s.fbPages = unsafe.Slice((*uint32)(unsafe.Pointer(&s.fbTable.Bytes()[0])), compute.PageTableWords)
		s.copyRows = gpuVDP1CopyKinds * gpuVDP1Records
		s.lines = make([]gpuVDP1Line, 0, gpuVDP1Records)
		s.recLo, s.recHi = ^uint32(0), 0
		if err := g.growVDP1(s, vdp1OpCapacity, len(v1.lineRecords)); err != nil {
			g.close()
			return nil, err
		}
	}
	g.pages = make([]uint32, 0, vdp2VRAMPages)
	v.markAllPages()
	if err := g.setupVDP1(); err != nil {
		g.close()
		return nil, err
	}
	return g, nil
}

// setupVDP1 brings the VDP1's memory to the device at open: the CPU's
// frame buffer writes are shadowed from here on, and the marks are
// cleared before the copies into the staging, so a write landing
// between is marked and delivered again. The VRAM goes through the
// staging in one submission and, for the accurate renderer, the pair
// in a second, each waited for before the staging is written again.
// The enhanced renderer's core clears the device pair instead: it
// draws a pair of its own, which the first take fills from the CPU
// pair (newEnhancedGPURenderer).
func (g *gpuRenderer) setupVDP1() error {
	v1 := g.vdp1
	v1.fbWritesTaken = true
	g.pages = v1.vramDirty.take(g.pages[:0])
	copy(g.stage.Bytes(), v1.vram)
	if err := g.waitedSubmit(func(cmds hostgpu.Commands) {
		cmds.Copy(g.vdp1VRAM, 0, g.stage, 0, compute.VDP1VRAMSize)
	}); err != nil {
		return err
	}
	for h := range g.vdp1FB {
		g.pages = v1.fbDirty[h].take(g.pages[:0])
		for i := range v1.fbMask[h] {
			v1.fbMask[h][i].Store(0)
		}
		if !g.enhanced {
			copy(g.stage.Bytes()[h*compute.VDP1FBSize:], v1.physicalFB(uint32(h)))
		}
	}
	return g.waitedSubmit(func(cmds hostgpu.Commands) {
		for h := range g.vdp1FB {
			if !g.enhanced {
				cmds.Copy(g.vdp1FB[h], 0, g.stage, h*compute.VDP1FBSize, compute.VDP1FBSize)
			} else {
				cmds.Fill(g.vdp1FB[h], 0, g.vdp1FB[h].Size(), 0)
			}
		}
	})
}

// waitedSubmit encodes a submission the walker waits on at once,
// commits it, and waits for it.
func (g *gpuRenderer) waitedSubmit(encode func(cmds hostgpu.Commands)) error {
	cmds, err := g.prog.Device.Begin()
	if err != nil {
		return err
	}
	encode(cmds)
	return g.prog.Device.Wait(cmds.Commit())
}

// gpuGrowth is one buffer grow allocates: where it is held, the size
// bufferSizes gives it, and its kind.
type gpuGrowth struct {
	buf  *hostgpu.Buffer
	size int
	kind hostgpu.BufferKind
}

// grow allocates each buffer bufferSizes names for the latched frame
// at the scale shift and the largest VDP1 frame buffer mode drawn in,
// and replaces one that holds less than it names; at open every one
// is allocated. The submissions in flight bind them, so it waits for
// the last one before it replaces any. A replaced buffer's contents
// are not kept: the row records, layer rows, line color rows, external
// rows, and copy params hold nothing across a frame, a replaced device
// frame buffer is blanked by the next submission, and replaced order
// stamps are stale, since a new working buffer's contents are not
// defined. The pair and the round state's heads depend on the scale
// shift and the pair's row factor, so open allocates them and the
// frame start that records the first doubled half grows them
// (beginFrame), the field's first take filling the new halves from
// the CPU pair; the pool depends on nothing. The host views are
// rebuilt.
func (g *gpuRenderer) grow() error {
	v := g.vdp2
	s := bufferSizes(g.shift, gpuStagingRegions*g.batchRows*g.maxRowSlots, v.frame.width, v.DisplayHeight(), g.fbPixels, v.rowFactor())
	var record lineRecord
	want := []gpuGrowth{
		{&g.rows, s.rowRecords * int(unsafe.Sizeof(record)), hostgpu.BufferUpload},
		{&g.deviceFramebuffer, s.framebufferWords * 4, hostgpu.BufferOutput},
		{&g.copyParams, s.copyRows * int(unsafe.Sizeof(screen.CopyParams{})), hostgpu.BufferUpload},
		{&g.extStage, s.layerWords * 4, hostgpu.BufferUpload},
		{&g.stamps, s.stamps * 4, hostgpu.BufferWorking},
		{&g.heads, s.heads * 4, hostgpu.BufferWorking},
		{&g.pool, s.pool * 4, hostgpu.BufferWorking},
	}
	for i := range g.layers {
		want = append(want, gpuGrowth{&g.layers[i], s.layerWords * 4, hostgpu.BufferWorking})
	}
	for i := range g.lineColor {
		want = append(want, gpuGrowth{&g.lineColor[i], s.lineColorBytes, hostgpu.BufferWorking})
	}
	for i := range g.vdp1FB {
		want = append(want, gpuGrowth{&g.vdp1FB[i], s.pairBytes, hostgpu.BufferWorking})
	}
	waited := false
	for _, w := range want {
		old := *w.buf
		if old != nil && old.Size() >= w.size {
			continue
		}
		if old != nil && !waited && g.sub != 0 {
			if err := g.prog.Device.Wait(g.sub); err != nil {
				return err
			}
			waited = true
		}
		b, err := g.prog.Device.NewBuffer(w.size, w.kind)
		if err != nil {
			return err
		}
		if old != nil {
			g.releaseBuffer(old)
		}
		*w.buf = b
		g.all = append(g.all, b)
		switch w.buf {
		case &g.deviceFramebuffer:
			g.blankWords = s.framebufferWords
		case &g.stamps:
			g.stampsStale = true
		}
	}
	g.fragmentCap = draw.FragmentCap(uint32(g.pool.Size() / 4))
	g.rowView = unsafe.Slice((*lineRecord)(unsafe.Pointer(&g.rows.Bytes()[0])), len(g.rows.Bytes())/int(unsafe.Sizeof(record)))
	g.extView = compute.Layer(unsafe.Slice((*uint32)(unsafe.Pointer(&g.extStage.Bytes()[0])), len(g.extStage.Bytes())/4))
	g.copyView = unsafe.Slice((*screen.CopyParams)(unsafe.Pointer(&g.copyParams.Bytes()[0])), len(g.copyParams.Bytes())/int(unsafe.Sizeof(screen.CopyParams{})))
	return nil
}

// close waits for the frame's submissions when any is in flight,
// since they bind the buffers, then releases the buffers and the
// device.
func (g *gpuRenderer) close() {
	g.vdp1.fbWritesTaken = false
	if g.sub != 0 {
		g.prog.Device.Wait(g.sub)
		g.sub = 0
	}
	for _, b := range g.all {
		b.Release()
	}
	g.all = nil
	g.prog.Device.Close()
}

func (g *gpuRenderer) backend() string { return g.backendName }

func (g *gpuRenderer) device() string { return g.prog.Device.Name() }

// framebuffer is the device frame buffer's bytes, the frame the
// composite wrote, complete once endFrame has waited on the frame's
// last submission.
func (g *gpuRenderer) framebuffer() []byte { return g.deviceFramebuffer.Bytes() }

func (g *gpuRenderer) framebufferStride() int { return g.vdp2.FramebufferStride() << g.shift }

func (g *gpuRenderer) displayHeight() int { return g.vdp2.DisplayHeight() << g.shift }

func (g *gpuRenderer) beginLine(int) {}

// beginFrame runs at the walker's frame start, after BeginFrame. The
// VDP1 work of the vertical blank, taken line by line into the region
// being filled, is submitted first. The device copies of VRAM and CRAM
// carry over from the last frame and differ from the VDP2's memory
// only by the pages marked since the last take, which row 0's take
// delivers at its render point, so row 0 renders against the memory
// as of that point on both schedules. The regions keep cycling across
// frames: a region is emptied when the filling moves to it, after its
// submission. When BeginFrame latched a new geometry the buffers grow
// to it before any row renders, the row slot stores to the session's
// bound on a row's slots (VDP2.maxRowSlots), which only a geometry
// change raises, and the enhanced core's pair, stamps, and heads grow
// at the first doubled half it records (VDP2.beginField). A raised
// bound moves the regions' fixed ranges: the growth waits for the last
// submission before it replaces the buffers in use, so no region is
// still reading its old range. A failed growth leaves buffers too
// small for the frame, so the frame goes no further on the device
// (submitBatch) and the frame loop switches to software.
func (g *gpuRenderer) beginFrame() {
	g.submitBatch()
	v := g.vdp2
	grow := v.frame.resized
	if n := v.maxRowSlots(); n > g.maxRowSlots {
		g.maxRowSlots = n
		if g.enhanced {
			g.subRows = make([]lineRecord, g.batchRows*n)
		}
	}
	// The enhanced core's first doubled half: the pair, stamps, and
	// heads grow to the row factor, and beginField marked every byte
	// of both CPU frame buffers written, so the field's first take
	// fills the new halves from the CPU pair through the layout
	// mapping. The frame's rows bind the display half below, after the
	// growth, and composite after that take, so row 0 reads the filled
	// new display half.
	growPair := g.enhanced && v.beginField(g.vdp1)
	if grow || growPair {
		if err := g.grow(); err != nil {
			g.failure = err
			return
		}
	}
	if v.frame.blanked {
		// A geometry change or the display off: rows this frame does
		// not write must not carry the previous mode's picture to the
		// frontend, so the next submission blanks the device frame
		// buffer ahead of them, or endFrame does when the frame
		// submits nothing.
		n := (v.DisplayHeight() << g.shift) * (v.frame.width << g.shift)
		g.blankWords = max(g.blankWords, n)
	}
	g.batchStart, g.rowsEnded, g.paletteMode = 0, 0, -1
	g.spriteBind = nil
	g.width = v.frame.width << g.shift
	g.rowSlots = v.rowSlots()
}

// resetStaging empties region i for filling, after the wait on the
// submission that last read it.
func (g *gpuRenderer) resetStaging(i int) {
	s := &g.staging[i]
	s.vramSlots, s.cramSlots, s.bands, s.sub = 0, 0, s.bands[:0], 0
	s.vdp1Slots, s.opSlots, s.lines = 0, [2]int{}, s.lines[:0]
	s.fbSlots = 0
	s.recLo, s.recHi = ^uint32(0), 0
	s.extRows = s.extRows[:0]
	for rbg := range s.lineColorSlots {
		s.lineColorSlots[rbg] = s.lineColorSlots[rbg][:0]
	}
}

// renderLine runs at row y's render point: it binds the VDP1 buffer
// the frame's rows sample and takes the pages written since the last
// take, the memory the row reads on both schedules, then counts the
// row and submits the batch once it holds batchRows rows.
func (g *gpuRenderer) renderLine(y int) {
	if g.vdp2.lineActive {
		g.captureSprite()
		g.takePages(y)
	}
	g.rowsEnded = y + 1
	if g.rowsEnded-g.batchStart >= g.batchRows {
		g.submitBatch()
	}
}

// captureSprite binds the half of the device pair the frame's rows
// sample, at the frame's first row. The buffer is the display buffer
// the frame change brought in: the ops that drew it completed on the
// device by the frame-change submission, ahead of the frame's rows,
// and nothing draws into it until the next change, in any mode. A
// frame with no sprite input binds the first half, which the
// composite reads nothing from.
func (g *gpuRenderer) captureSprite() {
	if g.spriteBind != nil {
		return
	}
	fb := &g.vdp2.lineFB
	if fb.data == nil {
		g.spriteBind = g.vdp1FB[0]
		return
	}
	g.spriteBind = g.vdp1FB[g.vdp1.physicalBuffer(fb.data)]
}

// takePages copies the dirty pages of VRAM and CRAM into the region
// being filled as row y's band. A region that cannot hold a whole
// memory of either is submitted first, so the take always fits.
func (g *gpuRenderer) takePages(y int) {
	v := g.vdp2
	s := &g.staging[g.cur]
	if s.vramSlots+vdp2VRAMPages > gpuStoreVRAMPages || s.cramSlots+vdp2CRAMPages > gpuStoreCRAMPages {
		g.submitBatch()
		s = &g.staging[g.cur]
	}
	band := gpuBand{line: y}
	band.vram[0] = s.vramSlots
	g.pages = v.vramDirty.take(g.pages[:0])
	for _, p := range g.pages {
		s.vramPages[s.vramSlots] = p
		copy(s.vramBytes[s.vramSlots*compute.PageBytes:], v.vram[p*compute.PageBytes:(p+1)*compute.PageBytes])
		s.vramSlots++
	}
	band.vram[1] = s.vramSlots
	band.cram[0] = s.cramSlots
	g.pages = v.cramDirty.take(g.pages[:0])
	for _, p := range g.pages {
		s.cramPages[s.cramSlots] = p
		copy(s.cramBytes[s.cramSlots*compute.PageBytes:], v.cram[p*compute.PageBytes:(p+1)*compute.PageBytes])
		s.cramSlots++
	}
	band.cram[1] = s.cramSlots
	if band.vram[1] > band.vram[0] || band.cram[1] > band.cram[0] {
		s.bands = append(s.bands, band)
	}
}

// endDisplay runs at the end of active display: the entries still
// waiting for their records are taken as they stand, and the rows
// ended since the last batch are submitted with the VDP1 work taken.
// The composite runs only for rows of the display, so the frame is
// complete in this submission, and every later one of the frame is
// VDP1 work alone. The VDP1's list restarts right after, every entry
// of it taken.
func (g *gpuRenderer) endDisplay() {
	g.takeVDP1(true)
	g.submitBatch()
	g.stampsStale = true
}

// gpuStage orders a batch's dispatches: the layer spans write the
// layer buffers, the gradation reads and rewrites one, and the
// composite reads them all, so each stage runs behind a barrier
// after the one before.
const (
	gpuStageLayers = iota
	gpuStageGradation
	gpuStageComposite
)

// gpuStageOf is the stage an entry point's runs dispatch in.
func gpuStageOf(entry int) int {
	switch entry {
	case entryGradation:
		return gpuStageGradation
	case entryComposite:
		return gpuStageComposite
	}
	return gpuStageLayers
}

// gpuRun is a run of consecutive rows dispatched together by one
// entry point over one layer buffer: consecutive rows whose chain
// names the same entry and layer.
type gpuRun struct {
	entry int
	stage int
	layer int
	first int
	n     int
}

// submitBatch submits the rows ended since the last batch with the
// bands taken for them and the VDP1 work taken since the last batch:
// each row's record, or its sub-row records when the renderer holds
// them, is placed at its row slots, the region's slots in row order,
// with the inputs the host stages (gpuRowInputs), the VDP1
// line records the batch's ops reserve are uploaded, and the batch is
// encoded, the VDP1 rounds first, then the rows band by band, and
// committed. The next region is then made ready, waiting for the
// submission that last read it. A batch with no active row, no band,
// and no VDP1 line submits nothing. After a failure, whose buffers the
// batch may not fit, the region is emptied without a submission, so
// the takes that submit to make room still find it.
//
// Ahead of everything else the submission blanks the device frame
// buffer when a blank is pending, clears the line color rows the
// batch's rotation spans write, and copies the batch's external rows
// into their layer, then a barrier.
func (g *gpuRenderer) submitBatch() {
	v := g.vdp2
	s := &g.staging[g.cur]
	a, b := g.batchStart, min(g.rowsEnded, rowRecordMax)
	g.batchStart = g.rowsEnded
	if g.failure != nil {
		g.resetStaging(g.cur)
		return
	}
	lines := v.rows.lines
	mode, active := gpuModeFrom(lines, a, b)
	if !active && len(s.bands) == 0 && len(s.lines) == 0 {
		return
	}
	if s.recHi > s.recLo {
		words := g.vdp1.lineRecords[s.recLo:s.recHi]
		copy(s.rec.Bytes()[s.recLo*4:], unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(words))), len(words)*4))
	}
	// Row a is the region's first row; a batch holds at most
	// batchRows rows, and row y's derived records take the rowSlots
	// slots from region + (y - a) * rowSlots, packed inside the
	// region's fixed range, so a run's rows are consecutive slots.
	base := g.cur * g.batchRows
	region := base * g.maxRowSlots
	for y := a; y < b; y++ {
		src := lines[y : y+1]
		if g.enhanced {
			i := (y % g.batchRows) * g.maxRowSlots
			src = g.subRows[i : i+g.rowSlots]
		}
		slot := region + (y-a)*g.rowSlots
		for k := range src {
			g.rowView[slot+k] = src[k]
			placeRowFrom(&g.rowView[slot+k], &src[k], int32(slot+k))
			g.gpuRowInputs(s, &lines[y], slot+k, y, k&(1<<g.shift-1))
		}
	}
	if g.failure != nil {
		g.advanceStaging()
		return
	}

	cmds, err := g.prog.Device.Begin()
	if err != nil {
		g.failure = err
		g.advanceStaging()
		return
	}
	if g.encodeInputs(cmds, s) {
		cmds.Barrier()
	}
	g.encodeVDP1(cmds, s)
	if active && int(mode) != g.paletteMode {
		g.encodePalette(cmds, mode)
		cmds.Barrier()
		g.paletteMode = int(mode)
	}
	prev := a
	for _, bd := range g.boundaries(lines, a, b, s.bands) {
		if bd.line > prev {
			g.encodeRows(cmds, lines, prev, bd.line, region+(prev-a)*g.rowSlots)
			cmds.Barrier()
		}
		cramPages := false
		if bd.band >= 0 {
			band := &s.bands[bd.band]
			g.encodeCopy(cmds, entryCopyVRAM, g.vram, s.vram, s.vramTable, base+band.line-a, band.vram)
			g.encodeCopy(cmds, entryCopyCRAM, g.cram, s.cram, s.cramTable, base+band.line-a, band.cram)
			cmds.Barrier()
			cramPages = band.cram[1] > band.cram[0]
		}
		if cramPages || bd.modeChange {
			if mode, ok := gpuModeFrom(lines, bd.line, b); ok {
				g.encodePalette(cmds, mode)
				cmds.Barrier()
				g.paletteMode = int(mode)
			} else {
				g.paletteMode = -1
			}
		}
		prev = bd.line
	}
	if prev < b {
		g.encodeRows(cmds, lines, prev, b, region+(prev-a)*g.rowSlots)
	}
	s.sub = cmds.Commit()
	g.sub = s.sub
	g.advanceStaging()
}

// gpuRowInputs stages the inputs row y's chain names that no span
// writes, for row slot, the row's sub-row k at the scale shift: the
// external image row into extStage at the row slot (exbgCopyRow),
// which the batch copies into the layer it names, and, for each
// rotation layer, the row slot's line color row, which the batch
// clears, so a dot whose coefficient supplies no line color reads as
// no-line-color rather than an earlier row's data. A region has room
// for one row of each kind per row slot, and a chain names the
// external row and each rotation layer at most once, so a row past
// them is a failure, not an overflow.
func (g *gpuRenderer) gpuRowInputs(s *gpuStaging, r *lineRecord, slot, y, k int) {
	slots := g.batchRows * g.maxRowSlots
	for i := 0; i < int(r.NSpans); i++ {
		switch entry, layer := spanKind(r.Spans[i]); entry {
		case entryExternal:
			n := len(s.extRows)
			if n == slots {
				g.failure = fmt.Errorf("external row %d past the region's rows", n)
				return
			}
			g.vdp2.exbgCopyRow(g.extView, slot, y, k)
			s.extRows = append(s.extRows, gpuExtRow{slot: slot, layer: layer})
		case entryRBGCell, entryRBGBitmap:
			rbg := layer - screen.LayerRBG0
			n := len(s.lineColorSlots[rbg])
			if n == slots {
				g.failure = fmt.Errorf("line color row %d past the region's rows", n)
				return
			}
			s.lineColorSlots[rbg] = append(s.lineColorSlots[rbg], slot)
		}
	}
}

// encodeInputs encodes the transfers ahead of the batch's work: the
// pending blank of the device frame buffer, the clear of the line
// color rows gpuRowInputs recorded, one Fill per run of rows at
// adjacent slots of a rotation layer, and the copies of the external
// rows into their layers, one Copy per run of rows at adjacent slots
// copying into the same layer. A line color row is width bytes at its
// slot, and an external row sits at its slot in extStage and in the
// layer, so a run is one contiguous range. It reports whether it
// encoded any.
func (g *gpuRenderer) encodeInputs(cmds hostgpu.Commands, s *gpuStaging) bool {
	rowWords := g.width
	rowBytes := rowWords * 4
	encoded := false
	if g.blankWords > 0 {
		cmds.Fill(g.deviceFramebuffer, 0, g.blankWords*4, frameBlack)
		g.blankWords = 0
		encoded = true
	}
	for rbg, slots := range s.lineColorSlots {
		for first := 0; first < len(slots); {
			end := first + 1
			for end < len(slots) && slots[end] == slots[first]+end-first {
				end++
			}
			cmds.Fill(g.lineColor[rbg], slots[first]*g.width, (end-first)*g.width, 0)
			encoded = true
			first = end
		}
	}
	for first := 0; first < len(s.extRows); {
		row := s.extRows[first]
		end := first + 1
		for end < len(s.extRows) && s.extRows[end].layer == row.layer && s.extRows[end].slot == row.slot+end-first {
			end++
		}
		offset := row.slot * rowBytes
		cmds.Copy(g.layers[row.layer], offset, g.extStage, offset, (end-first)*rowBytes)
		encoded = true
		first = end
	}
	return encoded
}

// advanceStaging moves filling to the next region, waiting for the
// submission that last read it, and empties it.
func (g *gpuRenderer) advanceStaging() {
	g.cur = (g.cur + 1) % gpuStagingRegions
	s := &g.staging[g.cur]
	if s.sub != 0 {
		if err := g.prog.Device.Wait(s.sub); err != nil {
			g.failure = err
		}
	}
	g.resetStaging(g.cur)
}

// gpuModeFrom is the CRAM mode of the first active row in [y, end),
// and whether there is one.
func gpuModeFrom(lines []lineRecord, y, end int) (uint32, bool) {
	for ; y < end; y++ {
		if lines[y].NSpans > 0 {
			return lines[y].Comp.CRAMMode, true
		}
	}
	return 0, false
}

// gpuBoundary is a line before which a band's pages copy, the
// palette rebuilds on a CRAM mode change, or both. band indexes the
// batch's band list, -1 when the line has no band.
type gpuBoundary struct {
	line       int
	band       int
	modeChange bool
}

// boundaries merges the batch's bands and the rows in [a, b) where
// the CRAM mode changes from the previous active row of the batch
// into one ascending list of boundaries. The bands are in line order
// with distinct lines, every one within [a, b): a band is taken at a
// row's render point, before the row has ended, and a submit forced
// inside a take runs before the band is appended, so a band's line
// is at or after its batch's first row and before its end.
func (g *gpuRenderer) boundaries(lines []lineRecord, a, b int, bands []gpuBand) []gpuBoundary {
	g.modeRows = g.modeRows[:0]
	mode, have := uint32(0), false
	for y := a; y < b; y++ {
		if lines[y].NSpans == 0 {
			continue
		}
		if m := lines[y].Comp.CRAMMode; have && m != mode {
			g.modeRows = append(g.modeRows, y)
		}
		mode, have = lines[y].Comp.CRAMMode, true
	}
	g.bounds = g.bounds[:0]
	bi, mi := 0, 0
	for {
		line := b
		if bi < len(bands) {
			line = min(line, bands[bi].line)
		}
		if mi < len(g.modeRows) {
			line = min(line, g.modeRows[mi])
		}
		if line >= b {
			return g.bounds
		}
		bd := gpuBoundary{line: line, band: -1}
		if bi < len(bands) && bands[bi].line == line {
			bd.band = bi
			bi++
		}
		if mi < len(g.modeRows) && g.modeRows[mi] == line {
			bd.modeChange = true
			mi++
		}
		g.bounds = append(g.bounds, bd)
	}
}

// encodePalette rebuilds the palette for a CRAM mode from the device
// CRAM, over the entries the mode defines, as the software path does.
// The params row is the mode: the row holds nothing else, so a frame
// rebuilding the palette at every row still writes only the rows of
// the modes it used. The mask is the register field's own width
// (gpuCRAMModes), so the row is always inside the buffer.
func (g *gpuRenderer) encodePalette(cmds hostgpu.Commands, mode uint32) {
	mode &= gpuCRAMModes - 1
	params := unsafe.Slice((*screen.PaletteParams)(unsafe.Pointer(&g.paletteParams.Bytes()[0])), gpuCRAMModes)
	params[mode] = screen.PaletteParams{Mode: mode}
	entries := int(screen.PaletteMask(mode)) + 1
	g.dispatchIndex(cmds, entryPalette, g.bind(g.cram, g.palette, g.paletteParams), entries, uintptr(mode), unsafe.Sizeof(params[0]))
}

// encodeCopy copies a band's staged pages of one memory, the store
// slots [start, end), into the device memory: one thread per word.
// The params row is the band's row slot and the memory, the row
// slot's two rows, so the row is inside the buffer and belongs to the
// region the band's batch uses.
func (g *gpuRenderer) encodeCopy(cmds hostgpu.Commands, entry int, mem, store, table hostgpu.Buffer, rowSlot int, slots [2]int) {
	n := slots[1] - slots[0]
	if n == 0 {
		return
	}
	k := 2 * rowSlot
	if entry == entryCopyCRAM {
		k++
	}
	g.copyView[k] = screen.CopyParams{Base: uint32(slots[0])}
	g.dispatchIndex(cmds, entry, g.bind(mem, store, table, g.copyParams), n*compute.PageWords, uintptr(k), unsafe.Sizeof(g.copyView[0]))
}

// dispatchIndex encodes an index entry over n items, one thread each,
// reading params row k of size rowSize from the last bound buffer.
func (g *gpuRenderer) dispatchIndex(cmds hostgpu.Commands, entry int, buffers []hostgpu.Buffer, n int, k, rowSize uintptr) {
	g.dispatch(cmds, entry, buffers, hostgpu.Dispatch{
		BoundX:       uint32(n),
		BoundY:       1,
		ParamsOffset: uint32(k * rowSize),
	}, [2]int{n, 1})
}

// encodeRows renders rows [a, b), whose records are at the row slots
// from slot on, rowSlots slots per row: the layer runs, a barrier,
// the gradation rows, a barrier, and the composite runs.
func (g *gpuRenderer) encodeRows(cmds hostgpu.Commands, lines []lineRecord, a, b, slot int) {
	var record lineRecord
	stride := uint32(unsafe.Sizeof(record))
	width := uint32(g.width)
	runs := g.splitRuns(lines[a:b])
	stage := func(s int) bool {
		encoded := false
		for _, run := range runs {
			if run.stage == s {
				g.encodeRun(cmds, run, slot, width, stride)
				encoded = true
			}
		}
		return encoded
	}
	stage(gpuStageLayers)
	cmds.Barrier()
	if stage(gpuStageGradation) {
		cmds.Barrier()
	}
	stage(gpuStageComposite)
}

// encodeRun dispatches one run of rows whose records start at row slot
// first + run.first * rowSlots, first being the slot of the rows'
// first row, the run's derived records one dispatch row each. The
// segment is the run's unit: a cell at the scale for a cell span, the
// whole row for the gradation, one dot otherwise.
func (g *gpuRenderer) encodeRun(cmds hostgpu.Commands, run gpuRun, first int, width, stride uint32) {
	var record lineRecord
	rows := run.n * g.rowSlots
	rec := hostgpu.Dispatch{
		BoundX:       width,
		BoundY:       uint32(rows),
		ParamsStride: stride,
		Segment:      1,
	}
	var buffers []hostgpu.Buffer
	var offset uintptr
	layer := run.layer
	switch run.entry {
	case entryNBGCell, entryNBGBitmap:
		buffers = g.bind(g.vram, g.palette, g.layers[layer], g.rows)
		offset = unsafe.Offsetof(record.NBG) + uintptr(layer)*unsafe.Sizeof(record.NBG[0])
	case entryRBGCell, entryRBGBitmap:
		rbg := layer - screen.LayerRBG0
		buffers = g.bind(g.vram, g.cram, g.palette, g.layers[layer], g.lineColor[rbg], g.rows)
		offset = unsafe.Offsetof(record.RBG) + uintptr(rbg)*unsafe.Sizeof(record.RBG[0])
	case entryGradation:
		// The unit is the whole row: the blur carries across the row.
		buffers = g.bind(g.layers[layer], g.rows)
		offset = unsafe.Offsetof(record.Grad)
		rec.Segment = width
	case entryComposite:
		sprite := g.spriteBind
		if sprite == nil {
			sprite = g.vdp1FB[0]
		}
		buffers = g.bind(
			g.layers[0], g.layers[1], g.layers[2], g.layers[3], g.layers[4], g.layers[5],
			g.lineColor[0], g.lineColor[1], sprite, g.palette, g.vram, g.deviceFramebuffer, g.rows,
		)
		offset = unsafe.Offsetof(record.Comp)
	}
	if run.entry == entryNBGCell || run.entry == entryRBGCell {
		rec.Segment = gpuCellSegment << g.shift
	}
	rec.ParamsOffset = uint32(offset) + uint32(first+run.first*g.rowSlots)*stride
	seg := int(rec.Segment)
	g.dispatch(cmds, run.entry, buffers, rec, [2]int{(int(width) + seg - 1) / seg, rows})
}

// dispatch encodes one entry point's pipeline.
func (g *gpuRenderer) dispatch(cmds hostgpu.Commands, entry int, buffers []hostgpu.Buffer, rec hostgpu.Dispatch, grid [2]int) {
	cmds.Dispatch(g.pipes[entry], buffers, rec, grid)
}

// bind fills the renderer's binding list for one dispatch, in buffer
// index order, and returns it. Every call site binds a constant list
// inside the list's length; the backend holds the count a dispatch
// binds against its entry (hostgpu.CheckBindings).
func (g *gpuRenderer) bind(buffers ...hostgpu.Buffer) []hostgpu.Buffer {
	n := copy(g.bufs[:], buffers)
	return g.bufs[:n]
}

// splitRuns splits rows into runs per entry point and layer:
// consecutive rows whose chain names the same entry over the same
// layer buffer. Row indices are relative to lines. A run is extended
// only from the row right after its last, so a gap or a change of
// entry starts a new run. The external row is an input the host
// copies, not a run.
func (g *gpuRenderer) splitRuns(lines []lineRecord) []gpuRun {
	g.runs = g.runs[:0]
	var open [entryCount][layerCount]int // index+1 of the latest run per entry point and layer
	for y := range lines {
		r := &lines[y]
		for i := 0; i < int(r.NSpans); i++ {
			entry, layer := spanKind(r.Spans[i])
			if entry == entryExternal {
				continue
			}
			if idx := open[entry][layer] - 1; idx >= 0 && g.runs[idx].first+g.runs[idx].n == y {
				g.runs[idx].n++
				continue
			}
			open[entry][layer] = len(g.runs) + 1
			g.runs = append(g.runs, gpuRun{entry: entry, stage: gpuStageOf(entry), layer: layer, first: y, n: 1})
		}
	}
	return g.runs
}

// endFrame runs at the frame's end. A blank still pending belongs to a
// frame that submitted nothing, so it is written in a submission of
// its own, waited for at once. It then blocks on the frame's last
// submission and records a failure, after which the device frame
// buffer holds the frame the frontend reads. A frame that renders no
// row and has no blank pending leaves the frame buffer as the last
// frame left it.
func (g *gpuRenderer) endFrame() {
	if g.blankWords > 0 && g.failure == nil {
		words := g.blankWords
		g.blankWords = 0
		if err := g.waitedSubmit(func(cmds hostgpu.Commands) {
			cmds.Fill(g.deviceFramebuffer, 0, words*4, frameBlack)
		}); err != nil {
			g.failure = err
		}
	}
	if g.sub == 0 {
		return
	}
	if err := g.prog.Device.Wait(g.sub); err != nil {
		g.failure = err
	}
	g.sub = 0
	for i := range g.staging {
		g.staging[i].sub = 0
	}
}

// err reports a failure since the last check and clears it.
func (g *gpuRenderer) err() error {
	err := g.failure
	g.failure = nil
	return err
}
