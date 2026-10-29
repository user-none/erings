// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
	"github.com/user-none/erings/internal/hostgpu"
)

// newRoundState allocates a native round state for a Go execution of
// the draw, at the sizes bufferSizes gives for the frame buffer mode
// with the most pixels at their finest addressing, one per byte.
func newRoundState() *compute.RoundState {
	s := bufferSizes(0, 0, 0, 0, compute.VDP1FBSize, 1)
	return &compute.RoundState{
		Stamps: make(compute.OrderStamps, s.stamps),
		Heads:  make(compute.FragmentHeads, s.heads),
		Pool:   make(compute.FragmentPool, s.pool),
	}
}

// execDevice is a device for the renderer's tests on any platform:
// its buffers are host byte slices, and a submission's dispatches run
// as Go loops when a wait reaches it, every submission up to the one
// waited for in the order committed and each one's dispatches in the
// order encoded, as a device's queue runs them. The device buffers
// hold what a device would leave once the wait returns, and a buffer
// the host writes before waiting for a submission that binds it shows
// in that submission's output, as it would on a device. A working
// buffer's Bytes is nil, as on a real device, and its memory starts
// filled with execPoison, so a dispatch that reads it before anything
// wrote it shows in the output; the dispatches reach it through
// execData.
type execDevice struct {
	subs      hostgpu.Submission
	pending   []execCall
	pipes     map[int]string
	draws     int            // op draws run
	threads   int            // a fixed group width, or zero
	widths    map[uint32]int // the op draws run at each group width
	composite hostgpu.Buffer // the sprite input the last composite bound
}

// execCall is one encoded command: a dispatch of pipe over bufs with
// rec, or, when copy is set, a copy of n bytes from bufs[1] at srcOff
// to bufs[0] at dstOff, or, when fill is set, value written to every
// word of n bytes of bufs[0] from dstOff.
type execCall struct {
	sub  hostgpu.Submission
	pipe int
	bufs []hostgpu.Buffer
	rec  hostgpu.Dispatch

	copy, fill     bool
	dstOff, srcOff int
	n              int
	value          uint32
}

type execCommands struct {
	d     *execDevice
	calls []execCall
}

type execBuffer struct {
	data []byte
	kind hostgpu.BufferKind
}

func (b *execBuffer) Bytes() []byte {
	if b.kind == hostgpu.BufferWorking {
		return nil
	}
	return b.data
}
func (b *execBuffer) Size() int                { return len(b.data) }
func (b *execBuffer) Kind() hostgpu.BufferKind { return b.kind }
func (b *execBuffer) Release()                 {}

// execPoison is the byte an execDevice working buffer starts filled
// with, the word 0xA5A5A5A5.
const execPoison = 0xA5

// execData is the memory of an execDevice buffer of any kind, as the
// device's dispatches see it.
func execData(b hostgpu.Buffer) []byte { return b.(*execBuffer).data }

func (d *execDevice) Name() string { return "exec" }

// GroupThreads is the device's fixed group width when threads is set,
// and otherwise hostgpu.GroupWidth with no lane count, 128-thread
// workgroups, and no limit past the dispatch record's bound. A dispatch
// of no ops, or of ops whose largest holds more chunks than all of them
// together, is a caller error.
func (d *execDevice) GroupThreads(load hostgpu.GroupLoad) int {
	switch {
	case load.OpCount <= 0:
		panic("GroupThreads for a dispatch of no ops")
	case load.MaxChunks > load.SumChunks:
		panic("GroupThreads for a dispatch whose largest op exceeds the sum")
	case d.threads > 0:
		return d.threads
	}
	return hostgpu.GroupWidth(load, 0, 128, 1<<31-128)
}

func (d *execDevice) Build(mod *hostgpu.Module) ([]hostgpu.Pipeline, error) {
	pipes := make([]hostgpu.Pipeline, len(mod.Entries))
	d.pipes = make(map[int]string, len(pipes))
	for i, e := range mod.Entries {
		pipes[i] = i
		d.pipes[i] = mod.Funcs[e.Func].Name
	}
	return pipes, nil
}

func (d *execDevice) NewBuffer(size int, kind hostgpu.BufferKind) (hostgpu.Buffer, error) {
	b := &execBuffer{data: make([]byte, size), kind: kind}
	if kind == hostgpu.BufferWorking {
		for i := range b.data {
			b.data[i] = execPoison
		}
	}
	return b, nil
}

func (d *execDevice) Begin() (hostgpu.Commands, error) { return &execCommands{d: d}, nil }
func (d *execDevice) Close()                           {}

// Wait runs the dispatches of every submission up to sub.
func (d *execDevice) Wait(sub hostgpu.Submission) error {
	n := 0
	for n < len(d.pending) && d.pending[n].sub <= sub {
		d.run(d.pending[n])
		n++
	}
	d.pending = d.pending[n:]
	return nil
}

func (c *execCommands) Dispatch(p hostgpu.Pipeline, bufs []hostgpu.Buffer, rec hostgpu.Dispatch, grid [2]int) {
	for _, b := range bufs {
		if b == nil {
			panic("execCommands: nil buffer bound")
		}
	}
	if err := hostgpu.CheckBindings(bufs, len(bufs), 0); err != nil {
		panic(err)
	}
	c.calls = append(c.calls, execCall{pipe: p.(int), bufs: append([]hostgpu.Buffer(nil), bufs...), rec: rec})
}

func (c *execCommands) Copy(dst hostgpu.Buffer, dstOffset int, src hostgpu.Buffer, srcOffset int, n int) {
	if err := hostgpu.CheckCopy(dst, dstOffset, src, srcOffset, n); err != nil {
		panic(err)
	}
	c.calls = append(c.calls, execCall{bufs: []hostgpu.Buffer{dst, src}, copy: true, dstOff: dstOffset, srcOff: srcOffset, n: n})
}

func (c *execCommands) Fill(dst hostgpu.Buffer, offset int, n int, value uint32) {
	if err := hostgpu.CheckFill(dst, offset, n); err != nil {
		panic(err)
	}
	c.calls = append(c.calls, execCall{bufs: []hostgpu.Buffer{dst}, fill: true, dstOff: offset, n: n, value: value})
}

func (c *execCommands) Barrier() {}

func (c *execCommands) Commit() hostgpu.Submission {
	c.d.subs++
	for _, call := range c.calls {
		call.sub = c.d.subs
		c.d.pending = append(c.d.pending, call)
	}
	return c.d.subs
}

// run executes one dispatch as the Go loops of its entry over the
// bound buffers: an index entry once per item, a span entry once per
// thread over its segment of each row, the params row of a span
// entry's row at the params offset plus the row times the stride.
func (d *execDevice) run(c execCall) {
	if c.copy {
		copy(execData(c.bufs[0])[c.dstOff:c.dstOff+c.n], execData(c.bufs[1])[c.srcOff:c.srcOff+c.n])
		return
	}
	if c.fill {
		dst := execData(c.bufs[0])[c.dstOff : c.dstOff+c.n]
		for i := 0; i < len(dst); i += 4 {
			binary.LittleEndian.PutUint32(dst[i:], c.value)
		}
		return
	}
	words := func(b hostgpu.Buffer) []uint32 {
		data := execData(b)
		return unsafe.Slice((*uint32)(unsafe.Pointer(&data[0])), len(data)/4)
	}
	// The round state's regions are bound in the group's field order
	// from binding i.
	roundState := func(i int) *compute.RoundState {
		return &compute.RoundState{Stamps: words(c.bufs[i]), Heads: words(c.bufs[i+1]), Pool: words(c.bufs[i+2])}
	}
	switch d.pipes[c.pipe] {
	case "GenerateTag", "GenerateWrite":
		b := draw.Buffers{
			VRAM:    (*compute.VDP1VRAM)(execData(c.bufs[0])),
			FB:      compute.VDP1FB(execData(c.bufs[1])),
			Round:   roundState(2),
			Records: words(c.bufs[5]),
		}
		ops := unsafe.Slice((*draw.DrawOp)(unsafe.Pointer(&execData(c.bufs[6])[c.rec.ParamsOffset])), c.rec.BoundY)
		n := c.rec.BoundX
		gen := draw.GenerateTag
		if d.pipes[c.pipe] == "GenerateWrite" {
			gen = draw.GenerateWrite
		}
		for gi := range ops {
			for t := range n {
				gen(&b, &ops[gi], t, n, uint32(gi))
			}
		}
		d.draws += len(ops)
		if d.widths == nil {
			d.widths = make(map[uint32]int)
		}
		d.widths[n] += len(ops)
	case "ClearStamps":
		rs := roundState(0)
		for i := uint32(0); i < c.rec.BoundX; i++ {
			draw.ClearStamps(rs, i)
		}
	case "ClearLists":
		rs := roundState(0)
		for i := uint32(0); i < c.rec.BoundX; i++ {
			draw.ClearLists(rs, i)
		}
	case "Resolve":
		fb := compute.VDP1FB(execData(c.bufs[0]))
		rs := roundState(1)
		for i := uint32(0); i < c.rec.BoundX; i++ {
			draw.Resolve(fb, rs, i)
		}
	case "CopyVDP1Pages":
		vram := (*compute.VDP1VRAM)(execData(c.bufs[0]))
		pages := draw.VRAMPages{
			Store: (*compute.VDP1PageStore)(execData(c.bufs[1])),
			Table: (*compute.PageTable)(words(c.bufs[2])),
		}
		p := (*draw.PageCopyParams)(unsafe.Pointer(&execData(c.bufs[3])[c.rec.ParamsOffset]))
		for i := range c.rec.BoundX {
			draw.CopyVDP1Pages(vram, &pages, p, i)
		}
	case "CopyFBPages":
		fb := compute.VDP1FB(execData(c.bufs[0]))
		writes := draw.FBWrites{
			Store: (*compute.VDP1PageStore)(execData(c.bufs[1])),
			Masks: (*compute.MaskStore)(words(c.bufs[2])),
			Table: (*compute.PageTable)(words(c.bufs[3])),
		}
		p := (*draw.PageCopyParams)(unsafe.Pointer(&execData(c.bufs[4])[c.rec.ParamsOffset]))
		for i := range c.rec.BoundX {
			draw.CopyFBPages(fb, &writes, p, i)
		}
	case "BuildPalette":
		p := (*screen.PaletteParams)(unsafe.Pointer(&execData(c.bufs[2])[c.rec.ParamsOffset]))
		for i := range c.rec.BoundX {
			screen.BuildPalette((*compute.CRAM)(execData(c.bufs[0])), (*compute.Palette)(words(c.bufs[1])), p, i)
		}
	case "CopyVRAMPages":
		pages := screen.VRAMPages{Store: (*compute.PageStore)(execData(c.bufs[1])), Table: (*compute.PageTable)(words(c.bufs[2]))}
		p := (*screen.CopyParams)(unsafe.Pointer(&execData(c.bufs[3])[c.rec.ParamsOffset]))
		for i := range c.rec.BoundX {
			screen.CopyVRAMPages((*compute.VDP2VRAM)(execData(c.bufs[0])), &pages, p, i)
		}
	case "CopyCRAMPages":
		pages := screen.CRAMPages{Store: (*compute.CRAMPageStore)(execData(c.bufs[1])), Table: (*compute.PageTable)(words(c.bufs[2]))}
		p := (*screen.CopyParams)(unsafe.Pointer(&execData(c.bufs[3])[c.rec.ParamsOffset]))
		for i := range c.rec.BoundX {
			screen.CopyCRAMPages((*compute.CRAM)(execData(c.bufs[0])), &pages, p, i)
		}
	case "NBGCellSpan", "NBGBitmapSpan", "RBGCellSpan", "RBGBitmapSpan", "GradationSpan", "CompositeSpan":
		d.runSpans(c, words)
	}
}

// runSpans executes a span entry's dispatch: every row's params at
// its offset, and each thread of the row over its segment of the
// row's width with the row state a thread starts from, zero.
func (d *execDevice) runSpans(c execCall, words func(hostgpu.Buffer) []uint32) {
	name := d.pipes[c.pipe]
	width, seg := int32(c.rec.BoundX), int32(c.rec.Segment)
	rows := execData(c.bufs[len(c.bufs)-1])
	vram := func(i int) *compute.VDP2VRAM { return (*compute.VDP2VRAM)(execData(c.bufs[i])) }
	pal := func(i int) *compute.Palette { return (*compute.Palette)(words(c.bufs[i])) }
	if name == "CompositeSpan" {
		d.composite = c.bufs[8]
	}
	for y := range c.rec.BoundY {
		params := unsafe.Pointer(&rows[c.rec.ParamsOffset+y*c.rec.ParamsStride])
		for x0 := int32(0); x0 < width; x0 += seg {
			x1 := min(x0+seg, width)
			switch name {
			case "NBGCellSpan":
				var st screen.NBGCellState
				screen.NBGCellSpan(vram(0), pal(1), words(c.bufs[2]), (*screen.NBGParams)(params), &st, x0, x1)
			case "NBGBitmapSpan":
				screen.NBGBitmapSpan(vram(0), pal(1), words(c.bufs[2]), (*screen.NBGParams)(params), x0, x1)
			case "RBGCellSpan":
				screen.RBGCellSpan(vram(0), (*compute.CRAM)(execData(c.bufs[1])), pal(2), words(c.bufs[3]), execData(c.bufs[4]), (*screen.RBGParams)(params), x0, x1)
			case "RBGBitmapSpan":
				screen.RBGBitmapSpan(vram(0), (*compute.CRAM)(execData(c.bufs[1])), pal(2), words(c.bufs[3]), execData(c.bufs[4]), (*screen.RBGParams)(params), x0, x1)
			case "GradationSpan":
				var st screen.GradationState
				screen.GradationSpan(words(c.bufs[0]), (*screen.GradationParams)(params), &st, x0, x1)
			case "CompositeSpan":
				in := screen.CompositeInputs{
					Sprite:  execData(c.bufs[8]),
					Palette: pal(9),
					VRAM:    vram(10),
				}
				for i := range in.Layers {
					in.Layers[i] = words(c.bufs[i])
				}
				in.LineColors[0], in.LineColors[1] = execData(c.bufs[6]), execData(c.bufs[7])
				screen.CompositeSpan(&in, words(c.bufs[11]), (*screen.CompositeParams)(params), x0, x1)
			}
		}
	}
}

// vdp1FrameDrive walks frames through the renderer's points in the
// frame loop's order, the VDP1 ticked one scanline at a time, with
// a VDP2 scene for the rows. A texture byte is rewritten in the
// second frame while the draw is under way, between two ops, so an
// op drawn before the write and one drawn after read different data,
// which the device reproduces only by splitting the round at the
// band. The write lands between ops: an op the walk is in the middle
// of reads the texture line by line, and the device draws every op
// from the memory as of the op's completion.
type vdp1FrameDrive struct {
	v1    *VDP1
	v2    *VDP2
	g     frameRenderer
	wrote bool

	// A pair request answered at a line: after the VDP1's take of
	// syncLine in syncFrame the drive syncs the pair through the
	// renderer and the walk keeps it from there, as the frame loop's
	// servicePair does at its next line wait.
	syncSet             bool
	syncFrame, syncLine int
}

const vdp1DriveScanlines = 263

func (d *vdp1FrameDrive) change() {
	vdp1DriveChange(d.v1)
	d.v2.BeginFrame()
	d.g.beginFrame()
}

// vdp1DriveChange is the drive's VDP1 side of the frame change: the
// change, the manual-swap evaluation, and the PTM=10 auto-draw gate.
func vdp1DriveChange(v1 *VDP1) {
	v1.VBlankIn()
	v1.VBlankOut()
	if v1.PTM() == 2 && v1.ConsumeFBSwap() {
		v1.StartAutoDraw()
	}
}

// frames drives frames until the scene's draw has completed and at
// least two frames have run, so a draw longer than a frame, which
// the manual change mode keeps in one buffer, is taken whole.
func (d *vdp1FrameDrive) frames(t *testing.T) int {
	t.Helper()
	for frame := 0; ; frame++ {
		d.frame(frame)
		if frame >= 1 && !d.v1.drawActive {
			return frame + 1
		}
		if frame >= 40 {
			t.Fatal("the draw did not complete in 40 frames")
		}
	}
}

func (d *vdp1FrameDrive) frame(frame int) {
	active := int(d.v2.ActiveLines())
	for line := 0; line < vdp1DriveScanlines; line++ {
		if line == 1 {
			d.change()
			beginRow(d.v2, d.g, 0, d.v2.vdp1View(d.v1))
			d.g.renderLine(0)
		}
		if line >= 1 && line < active {
			beginRow(d.v2, d.g, line, d.v2.vdp1View(d.v1))
		}
		if line == active {
			d.g.endDisplay()
		}
		vdp1DriveLine(d.v1, frame, line, active, &d.wrote)
		d.g.vdp1Line(line)
		if d.syncSet && frame == d.syncFrame && line == d.syncLine {
			d.g.syncPair(line)
			d.v1.setPairKept(true)
		}
		if line >= 1 && line < active {
			d.g.renderLine(line)
		}
	}
	d.g.endFrame()
}

// vdp1DriveLine is the drive's VDP1 side of one line: the list
// restart and the late erase at the end of the display, the drive's
// texture write and its CPU frame buffer writes in the second frame,
// and the tick. A walk driven through it alone, with no renderer,
// sees what the drive's walk sees.
func vdp1DriveLine(v1 *VDP1, frame, line, active int, wrote *bool) {
	if line == active {
		v1.restartList()
		v1.PerformLateErase()
	}
	if frame == 1 && line >= 40 && !*wrote && v1.cmdPhase == phaseIdle {
		v1.WriteVRAM(benchTex8+5*32+7, 0x3C)
		*wrote = true
	}
	if frame == 1 && line == 60 {
		// CPU writes into the draw buffer, a word and a byte at
		// two rows, which the device pair must take on its own,
		// and which land under the entry drawn before this
		// line and over the entry drawn after it when an op is
		// in progress across them.
		v1.WriteFB16(100*1024+40, 0xA5C3)
		v1.WriteFB(101*1024+7, 0x5A)
		v1.WriteFB16(30*1024+40, 0x3C96)
		v1.WriteFB(31*1024+7, 0xC3)
	}
	v1.TickSystemCycles(benchVDP1WalkerTick)
}

// vdp1DriveWalk drives a walk alone through frames of the drive's
// VDP1 side, the store reference for a drive on the device, and
// through the change that follows them, as vdp1SubmitChange runs it.
func vdp1DriveWalk(v1 *VDP1, frames, active int) {
	wrote := false
	for frame := 0; frame < frames; frame++ {
		for line := 0; line < vdp1DriveScanlines; line++ {
			if line == 1 {
				vdp1DriveChange(v1)
			}
			vdp1DriveLine(v1, frame, line, active, &wrote)
		}
	}
	vdp1DriveChange(v1)
}

// manualDraw makes a scene draw once in the manual change mode: no
// swap and no restart at the frame change, so a draw longer than a
// frame completes in one buffer instead of restarting every frame.
func manualDraw(s benchVDP1Scene) benchVDP1Scene {
	return benchVDP1Scene{name: s.name, build: func() *VDP1 {
		v := s.build()
		v.Write(0x02, 0x0002)
		v.Write(0x04, 0x0001)
		return v
	}}
}

// fullScreenPolygon is a polygon over the whole 320x224 drawing
// area in color calculation mode cc with the color.
func fullScreenPolygon(cc, color uint16) benchCmd {
	return benchCmd{ctrl: 0x0004, pmod: cc, colr: color, xa: 0, ya: 0, xb: 319, yb: 0, xc: 319, yc: 223, xd: 0, yd: 223}
}

// eraseArea makes a scene set the erase area and data, so the frame
// change's erase is performed and appended to the list: the whole
// drawing area or the part [x1, x3] by [y1, y3], x in 8-dot units.
func eraseArea(s benchVDP1Scene, x1, y1, x3, y3 uint16) benchVDP1Scene {
	return benchVDP1Scene{name: s.name, build: func() *VDP1 {
		v := s.build()
		v.Write(0x06, 0x8210)
		v.Write(0x08, x1<<9|y1)
		v.Write(0x0A, x3<<9|y3)
		return v
	}}
}

// vdp1FrameScenes is the benchmark scenes with ops, the ones whose
// draw outlasts a frame at the walker's cadence in the manual draw
// mode, a half-transparent polygon larger than a round's fragment
// capacity, whose device draw goes in pieces, a half-transparent
// distorted sprite in progress across the end of the display in the
// manual erase mode, so the late erase is appended while the walk is
// inside it, two half-transparent polygons in progress across the
// drive's CPU frame buffer writes, and three scenes in the automatic
// change mode with the erase area set, so every frame change's erase
// is an entry between the vertical blank's draws and the frame's:
// the mixed scene and the half-transparent polygons with the whole
// area erased, and the distorted sprites with a part of it.
func vdp1FrameScenes() []benchVDP1Scene {
	var scenes []benchVDP1Scene
	byName := map[string]benchVDP1Scene{}
	for _, s := range benchVDP1Scenes() {
		byName[s.name] = s
		probe := s.build()
		benchVDP1Change(probe)
		for line := 0; line < vdp1DriveScanlines; line++ {
			probe.TickSystemCycles(benchVDP1WalkerTick)
		}
		if len(probe.ops) == 0 {
			continue
		}
		if probe.drawActive {
			s = manualDraw(s)
		}
		scenes = append(scenes, s)
	}
	scenes = append(scenes, manualDraw(benchVDP1Scene{name: "half-transparent polygon over the capacity", build: func() *VDP1 {
		v, l := benchVDP1()
		l.add(benchCmd{ctrl: 0x0004, pmod: 3, colr: 0x83E0, xa: -300, ya: -300, xb: 500, yb: -290, xc: 510, yc: 500, xd: -290, yd: 510})
		l.end()
		return v
	}}))
	scenes = append(scenes, manualDraw(benchVDP1Scene{name: "half-transparent distorted sprite across the display end", build: func() *VDP1 {
		v, l := benchVDP1()
		benchGrid(l, 5, 1, 40, 44, func(x, y int16) benchCmd { return benchDistorted(x+4, y, 3) })
		l.add(benchCmd{ctrl: 0x0002, pmod: 4<<3 | 3, colr: 0x0100, srca: benchTex8 / 8, size: benchSize32,
			xa: 0, ya: 0, xb: 319, yb: 0, xc: 319, yc: 223, xd: 0, yd: 223})
		l.end()
		v.Write(0x06, 0x0000)
		v.Write(0x0A, 40<<9|223)
		return v
	}}))
	scenes = append(scenes, manualDraw(benchVDP1Scene{name: "half-transparent polygons under frame buffer writes", build: func() *VDP1 {
		v, l := benchVDP1()
		l.add(fullScreenPolygon(3, 0x83E0))
		l.add(fullScreenPolygon(3, 0x83E0))
		l.end()
		return v
	}}))
	mixed, distorted := byName["mixed"], byName["distorted sprite"]
	mixed.name, distorted.name = "mixed with the change's erase", "distorted sprite with a partial erase"
	halfPolygons := benchVDP1Scene{name: "half-transparent polygons with the change's erase", build: func() *VDP1 {
		v, l := benchVDP1()
		l.add(benchPolygon(10, 10, 3, 0x83E0))
		l.add(benchPolygon(60, 40, 3, 0x801F))
		l.end()
		return v
	}}
	return append(scenes,
		eraseArea(mixed, 0, 0, 40, 223),
		eraseArea(halfPolygons, 0, 0, 40, 223),
		eraseArea(distorted, 4, 30, 30, 180),
	)
}

// vdp1FramesMatch drives the scene's frames through the renderer and
// holds both halves of the device pair equal to the CPU pair after
// the next frame's change, which submits the vertical blank's ops
// and the change's erase, waiting for that submission.
func vdp1FramesMatch(t *testing.T, s benchVDP1Scene, g *gpuRenderer, v1 *VDP1, v2 *VDP2) int {
	t.Helper()
	d := &vdp1FrameDrive{v1: v1, v2: v2, g: g}
	frames := d.frames(t)
	vdp1PairMatch(t, s.name, d)
	return frames
}

// vdp1PairMatch runs the next frame's change, which submits the
// vertical blank's ops and the change's erase, waits for that
// submission, and holds both halves of the device pair equal to the
// CPU pair.
func vdp1PairMatch(t *testing.T, name string, d *vdp1FrameDrive) {
	t.Helper()
	g, v1 := d.g.(*gpuRenderer), d.v1
	d.change()
	g.vdp1Line(1)
	g.submitBatch()
	if err := g.err(); err != nil {
		t.Fatal(err)
	}
	if g.sub != 0 {
		if err := g.prog.Device.Wait(g.sub); err != nil {
			t.Fatal(err)
		}
	}
	for i := uint32(0); i < 2; i++ {
		if got, want := deviceBytes(t, g, g.vdp1FB[i]), v1.physicalFB(i); !bytes.Equal(got, want) {
			t.Fatalf("%s: physical buffer %d differs from the walk at byte %d", name, i, firstDiff(got, want))
		}
	}
}

// TestGPUVDP1TransferOverMatchesWalk drives a list of full-screen
// polygons longer than a frame under the automatic change mode with
// PTM=10: every frame change restarts the list and abandons the
// polygon in progress, so the device must draw of it exactly the
// lines the walk drew. Three frames, then both halves of the device
// pair are held equal to the CPU pair.
func TestGPUVDP1TransferOverMatchesWalk(t *testing.T) {
	parent, subs := ComputeSources()
	v1, l := benchVDP1()
	for i := 0; i < 8; i++ {
		l.add(fullScreenPolygon(0, 0x8000|uint16(i*7+1)))
	}
	l.end()
	v2, _ := benchVDP2Scenes()[0].build()
	prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(v2, v1, prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	d := &vdp1FrameDrive{v1: v1, v2: v2, g: g}
	for frame := 0; frame < 3; frame++ {
		d.frame(frame)
	}
	if !v1.drawActive {
		t.Fatal("the list completed inside a frame")
	}
	vdp1PairMatch(t, "transfer-over", d)
}

// TestGPUVDP1FrameMatchesWalk drives every frame scene through the
// renderer on the executing device, on any platform, and holds the
// device pair equal to the CPU pair.
func TestGPUVDP1FrameMatchesWalk(t *testing.T) {
	parent, subs := ComputeSources()
	for _, s := range vdp1FrameScenes() {
		t.Run(s.name, func(t *testing.T) {
			v1 := s.build()
			v2, _ := benchVDP2Scenes()[0].build()
			dev := &execDevice{}
			vdp1FrameOnDevice(t, s, dev, v1, v2, parent, subs)
		})
	}
}

// TestGPUVDP1GroupWidths runs the frame scenes on devices that give
// every op's group one thread and thirteen threads, and holds both to
// the walk, so the frames are the same whatever width a backend gives
// a group, and every op draw ran at the width the device chose.
func TestGPUVDP1GroupWidths(t *testing.T) {
	parent, subs := ComputeSources()
	for _, threads := range []int{1, 13} {
		for _, s := range vdp1FrameScenes() {
			t.Run(fmt.Sprintf("%d/%s", threads, s.name), func(t *testing.T) {
				v1 := s.build()
				v2, _ := benchVDP2Scenes()[0].build()
				dev := &execDevice{threads: threads}
				vdp1FrameOnDevice(t, s, dev, v1, v2, parent, subs)
				if len(dev.widths) != 1 || dev.widths[uint32(threads)] != dev.draws {
					t.Errorf("op draws by group width %v, want all %d at %d", dev.widths, dev.draws, threads)
				}
			})
		}
	}
}

// vdp1FrameOnDevice renders a frame scene's frames on dev and holds
// them to the walk, with the counts of the draws, the submissions, and
// the composite's sprite input.
func vdp1FrameOnDevice(t *testing.T, s benchVDP1Scene, dev *execDevice, v1 *VDP1, v2 *VDP2, parent hostgpu.Source, subs []hostgpu.Source) {
	t.Helper()
	prog, err := hostgpu.Build(dev, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(v2, v1, prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	frames := vdp1FramesMatch(t, s, g, v1, v2)
	if want := 2 * len(v1.ops); dev.draws < want {
		t.Errorf("%d op draws run over the frames of %d ops", dev.draws, want)
	}
	// The two setup submissions of open, four row batches and a
	// frame-change submission per frame, the last change, the final
	// submit, and the two reads of the device pair: the VDP1 work adds
	// no submissions of its own.
	if dev.subs > hostgpu.Submission(2+5*frames+2+2) {
		t.Errorf("%d submissions over %d frames", dev.subs, frames)
	}
	if dev.composite != g.vdp1FB[0] && dev.composite != g.vdp1FB[1] {
		t.Error("the composite did not bind a half of the device pair")
	}
}

// vdp1SubmitChange runs the next frame's change on the drive, whose
// renderer's GPU core is g, submits the vertical blank's ops and the
// change's erase, and waits for that submission, so the device pair
// is complete.
func vdp1SubmitChange(t *testing.T, d *vdp1FrameDrive, g *gpuRenderer) {
	t.Helper()
	d.change()
	g.vdp1Line(1)
	g.submitBatch()
	if err := g.err(); err != nil {
		t.Fatal(err)
	}
	if g.sub != 0 {
		if err := g.prog.Device.Wait(g.sub); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGPUVDP1PairSyncMatchesWalk drives every frame scene through
// the renderer on the executing device with the timing walk, a pair
// request answered at line 100 of the first frame, where most scenes
// have a command in progress, and the store walk alone through the
// same frames as the reference. It holds the timing walk's device
// pair equal to the store walk's CPU pair, since the two walks
// produce the same entries and records, and the timing walk's CPU
// pair equal to it too: the copy from the device brought it current
// and the walk kept it from there, the command in progress included.
func TestGPUVDP1PairSyncMatchesWalk(t *testing.T) {
	parent, subs := ComputeSources()
	for _, s := range vdp1FrameScenes() {
		t.Run(s.name, func(t *testing.T) {
			v1 := s.build()
			v1.setPairKept(false)
			v2, _ := benchVDP2Scenes()[0].build()
			prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
			if err != nil {
				t.Fatal(err)
			}
			g, err := newGPURenderer(v2, v1, prog, gpuBatchLines, false)
			if err != nil {
				t.Fatal(err)
			}
			defer g.close()
			d := &vdp1FrameDrive{v1: v1, v2: v2, g: g, syncSet: true, syncFrame: 0, syncLine: 100}
			frames := d.frames(t)
			vdp1SubmitChange(t, d, g)
			if !v1.pairKept.Load() {
				t.Fatal("the request was not answered")
			}
			store := s.build()
			vdp1DriveWalk(store, frames, int(v2.ActiveLines()))
			for i := uint32(0); i < 2; i++ {
				want := store.physicalFB(i)
				if got := deviceBytes(t, g, g.vdp1FB[i]); !bytes.Equal(got, want) {
					t.Fatalf("device buffer %d differs from the store walk at byte %d", i, firstDiff(got, want))
				}
				if got := v1.physicalFB(i); !bytes.Equal(got, want) {
					t.Fatalf("CPU buffer %d differs from the store walk at byte %d after the sync", i, firstDiff(got, want))
				}
			}
		})
	}
}

// TestGPUVDP1FrameMatchesWalkDevice is the same drive on the
// platform's device, skipped where there is none.
func TestGPUVDP1FrameMatchesWalkDevice(t *testing.T) {
	for _, s := range vdp1FrameScenes() {
		t.Run(s.name, func(t *testing.T) {
			v1 := s.build()
			v2, _ := benchVDP2Scenes()[0].build()
			g, err := openGPURenderer(v2, v1)
			if err != nil {
				t.Skip(err)
			}
			defer g.close()
			_ = vdp1FramesMatch(t, s, g, v1, v2)
		})
	}
}

// TestGPUVDP1SplitOpGrowsRegion fills a region past its initial op
// capacity with the pieces of a destination-reading part alone: the
// large-class parts completing after the frame change, which submits
// the first region, count to a few slots under the capacity, and the
// destination-reading part's eight pieces cross it, so the take must
// grow the arrays as it writes them. The parts sit outside the
// drawing area with pre-clipping disabled, except the last line of
// the destination-reading part, whose one dot inside the area is
// drawn by its last piece, so the list completes inside that batch.
// The region grows and both halves of the device pair are held equal
// to the CPU pair.
func TestGPUVDP1SplitOpGrowsRegion(t *testing.T) {
	scene := manualDraw(benchVDP1Scene{name: "split op past the region capacity", build: func() *VDP1 {
		v, l := benchVDP1()
		for i := 0; i < vdp1OpCapacity+76; i++ {
			l.add(benchCmd{ctrl: 0x0004, pmod: 0x0800, colr: 0x83E0, xa: -3000, ya: -10, xb: -951, yb: -10, xc: -951, yc: -9, xd: -3000, yd: -9})
		}
		l.add(benchCmd{ctrl: 0x0004, pmod: 0x0803, colr: 0x83E0, xa: -375, ya: -4095, xb: 0, yb: -4095, xc: 0, yc: 0, xd: -375, yd: 0})
		l.end()
		return v
	}})
	parent, subs := ComputeSources()
	v1 := scene.build()
	v2, _ := benchVDP2Scenes()[0].build()
	dev := &execDevice{}
	prog, err := hostgpu.Build(dev, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(v2, v1, prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	vdp1FramesMatch(t, scene, g, v1, v2)
	grown := false
	for i := range g.staging {
		grown = grown || g.staging[i].opCap > vdp1OpCapacity
	}
	if !grown {
		t.Error("no region grew past the initial op capacity")
	}
	if got := v1.drawFB[1]; got != 0xE0 {
		t.Errorf("the walk's dot at (0, 0) is %#x, want the part's color", got)
	}
}

// TestGPUVDP1StateLoadMatchesWalk saves an emulator at a frame
// boundary with a half-transparent polygon in progress, in the
// manual change mode so the draw spans frames, restores the state
// into the same emulator, as a rewind does, with the executing device
// installed as its renderer, drives the rest of the draw, and holds
// both halves of the device pair equal to the CPU pair: the load
// keeps the list, whose entries were all taken, so the renderer's
// take continues behind it, and the restored command's entries start
// where the saved walk stopped.
func TestGPUVDP1StateLoadMatchesWalk(t *testing.T) {
	e := NewEmulator()
	v1 := e.vdp1
	v1.Write(0x00, 0x0000)
	v1.Write(0x02, 0x0002)
	v1.Write(0x04, 0x0001)
	l := &benchList{v: v1}
	l.add(benchCmd{ctrl: 0x0009, xc: 319, yc: 223})
	l.add(benchCmd{ctrl: 0x000A})
	l.add(fullScreenPolygon(0, 0x801F))
	l.add(fullScreenPolygon(3, 0x83E0))
	l.add(fullScreenPolygon(3, 0x8000|31<<10))
	l.end()
	parent, subs := ComputeSources()
	prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(e.vdp2, v1, prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	// The executing device stands in for the platform's, which
	// SetRenderer would open.
	e.renderer, e.rendererMode = g, RendererGPU
	d := &vdp1FrameDrive{v1: v1, v2: e.vdp2, g: g}
	d.frame(0)
	if !v1.drawActive || v1.cmdPhase != phaseDraw {
		t.Fatal("the draw is not in progress at the frame boundary")
	}
	state, err := e.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Deserialize(state); err != nil {
		t.Fatal(err)
	}
	if v1.opsTaken != len(v1.ops) {
		t.Fatalf("after the load: %d entries taken of a list of %d", v1.opsTaken, len(v1.ops))
	}
	for frame := 1; v1.drawActive; frame++ {
		if frame > 40 {
			t.Fatal("the draw did not complete in 40 frames")
		}
		d.frame(frame)
	}
	vdp1PairMatch(t, "state load", d)
}

// TestGPUVDP1FrameNoAllocs drives frames of the mixed scene, the
// most ops of the set, through the renderer's points on the test
// device and checks that the frame path allocates nothing once the
// first frame has grown the region's arrays, on the GPU renderer and
// on the enhanced GPU renderer at 2x, which also scales every entry.
func TestGPUVDP1FrameNoAllocs(t *testing.T) {
	parent, subs := ComputeSources()
	var mixed benchVDP1Scene
	for _, s := range benchVDP1Scenes() {
		if s.name == "mixed" {
			mixed = s
		}
	}
	opens := []struct {
		name string
		open func(v2 *VDP2, v1 *VDP1, prog *hostgpu.Program) (frameRenderer, error)
	}{
		{"gpu", func(v2 *VDP2, v1 *VDP1, prog *hostgpu.Program) (frameRenderer, error) {
			return newGPURenderer(v2, v1, prog, gpuBatchLines, false)
		}},
		{"enhanced gpu 2x", func(v2 *VDP2, v1 *VDP1, prog *hostgpu.Program) (frameRenderer, error) {
			v2.setEnhance(enhanceConfig{shift: 1})
			return newEnhancedGPURenderer(v2, v1, prog, gpuBatchLines>>1)
		}},
	}
	for _, o := range opens {
		t.Run(o.name, func(t *testing.T) {
			v1 := mixed.build()
			v2, _ := benchVDP2Scenes()[0].build()
			prog, err := hostgpu.Build(&testDevice{}, parent, subs...)
			if err != nil {
				t.Fatal(err)
			}
			g, err := o.open(v2, v1, prog)
			if err != nil {
				t.Fatal(err)
			}
			defer g.close()
			d := &vdp1FrameDrive{v1: v1, v2: v2, g: g}
			frame := 0
			run := func() {
				d.frame(frame)
				frame++
			}
			run()
			if n := testing.AllocsPerRun(3, run); n != 0 {
				t.Errorf("%.0f allocations per frame", n)
			}
			if err := g.err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRoundStateSizes holds the round state's region lengths to the
// form their helpers need, a power of two each, the stamps at one per
// pixel of the frame buffer mode at its finest addressing and the heads
// at one per 16-bit pixel, both at N squared at scale, the pool native
// at every scale, the fragment cap to the pool's slots after the
// counter's, and the Go execution's round state to the mode with the
// most pixels.
func TestRoundStateSizes(t *testing.T) {
	for shift := uint32(0); shift <= 3; shift++ {
		for _, px := range []int{512 * 256, 1024 * 256, 512 * 512} {
			s := bufferSizes(shift, 0, 0, 0, px, 1)
			for name, n := range map[string]int{"stamps": s.stamps, "heads": s.heads, "pool": s.pool} {
				if n <= 0 || n&(n-1) != 0 {
					t.Errorf("shift %d pixels %d: %s length %d is not a power of two", shift, px, name, n)
				}
			}
			if s.stamps != px<<(2*shift) || s.heads != compute.VDP1FBSize/2<<(2*shift) || s.pool != gpuFragmentPoolWords {
				t.Errorf("shift %d pixels %d: stamps %d heads %d pool %d", shift, px, s.stamps, s.heads, s.pool)
			}
		}
	}
	s := bufferSizes(0, 0, 0, 0, compute.VDP1FBSize, 1)
	if got, want := draw.FragmentCap(uint32(s.pool)), uint32(s.pool/draw.FragmentWords-1); got != want {
		t.Errorf("fragment cap %d, want %d", got, want)
	}
	rs := newRoundState()
	if len(rs.Stamps) != s.stamps || len(rs.Heads) != s.heads || len(rs.Pool) != s.pool {
		t.Errorf("newRoundState lengths %d %d %d, want %d %d %d", len(rs.Stamps), len(rs.Heads), len(rs.Pool), s.stamps, s.heads, s.pool)
	}
}
