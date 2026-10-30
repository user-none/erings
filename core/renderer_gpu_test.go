// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/backend"
)

// TestEntryNames holds every entry constant against the compute
// function it names. The GPU renderer resolves a constant's
// pipeline through entryNames while the software renderer calls the
// function directly, so a row naming another function makes the two
// renderers draw different things from the same chain word, which
// only a device run would show. The names are then held against the
// entry points the compute package declares, so one added or removed
// there fails here too.
func TestEntryNames(t *testing.T) {
	fns := [entryCount]any{
		entryNBGCell:       screen.NBGCellSpan,
		entryNBGBitmap:     screen.NBGBitmapSpan,
		entryRBGCell:       screen.RBGCellSpan,
		entryRBGBitmap:     screen.RBGBitmapSpan,
		entryGradation:     screen.GradationSpan,
		entryComposite:     screen.CompositeSpan,
		entryPalette:       screen.BuildPalette,
		entryCopyVRAM:      screen.CopyVRAMPages,
		entryCopyCRAM:      screen.CopyCRAMPages,
		entryGenerateTag:   draw.GenerateTag,
		entryGenerateWrite: draw.GenerateWrite,
		entryClearStamps:   draw.ClearStamps,
		entryClearLists:    draw.ClearLists,
		entryResolve:       draw.Resolve,
		entryCopyVDP1:      draw.CopyVDP1Pages,
		entryCopyFB:        draw.CopyFBPages,
	}
	for i, fn := range fns {
		if fn == nil {
			t.Errorf("entry %d has no compute function here", i)
			continue
		}
		if got := renderFuncName(fn); got != entryNames[i] {
			t.Errorf("entry %d names %q, its compute function is %s", i, entryNames[i], got)
		}
	}
	dispatched := append([]string{}, entryNames[:]...)
	declared := append([]string{}, screen.Entries[:]...)
	declared = append(declared, draw.Entries[:]...)
	slices.Sort(dispatched)
	slices.Sort(declared)
	if !slices.Equal(dispatched, declared) {
		t.Errorf("the core dispatches %v, the compute package declares %v", dispatched, declared)
	}
}

// renderFuncName is a compute function's name without its package.
func renderFuncName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return full[strings.LastIndexByte(full, '.')+1:]
}

// TestDefaultRenderer checks the renderer before Start: the emulator
// starts on software with register capture on.
func TestDefaultRenderer(t *testing.T) {
	e := NewEmulator()
	if e.Renderer() != RendererSoftware {
		t.Fatal("default renderer is not software")
	}
	if _, ok := e.renderer.(*softwareRenderer); !ok {
		t.Fatalf("default renderer is %T", e.renderer)
	}
	if !e.vdp2.regCapture {
		t.Fatal("software renderer without register capture")
	}
	if e.RendererName() != "Software" {
		t.Fatalf("software renderer name %q", e.RendererName())
	}
}

// TestRendererNames checks the renderer option's values: the
// platform's backends, the default first, then Software.
func TestRendererNames(t *testing.T) {
	names := RendererNames()
	if !slices.Equal(names, append(backend.Names(), "Software")) {
		t.Fatalf("renderer names %q, backends %q", names, backend.Names())
	}
	if b := backend.Names(); len(b) > 0 && b[0] != defaultBackend() {
		t.Fatalf("default backend %q, backends %q", defaultBackend(), b)
	}
	if slices.Contains(names, "GPU") {
		t.Fatal("GPU is a renderer value")
	}
}

// TestRendererOption checks the renderer option: the requested mode
// is GPU on the default backend by default, the option's values
// change it without opening a renderer, an unknown value such as the
// former GPU keeps it, and Start selects it. A requested Software
// leaves the software renderer from construction in place.
func TestRendererOption(t *testing.T) {
	e := NewEmulator()
	defer e.Close()
	if e.requestedRenderer != RendererGPU || e.requestedBackend != defaultBackend() {
		t.Fatalf("default requested renderer %d on %q", e.requestedRenderer, e.requestedBackend)
	}
	sw := e.renderer
	e.SetOption("renderer", "Software")
	if e.requestedRenderer != RendererSoftware {
		t.Fatal("Software not requested")
	}
	for _, b := range backend.Names() {
		e.SetOption("renderer", b)
		if e.requestedRenderer != RendererGPU || e.requestedBackend != b {
			t.Fatalf("%q requested renderer %d on %q", b, e.requestedRenderer, e.requestedBackend)
		}
	}
	e.SetOption("renderer", "Software")
	for _, v := range []string{"neither", "GPU"} {
		e.SetOption("renderer", v)
		if e.requestedRenderer != RendererSoftware {
			t.Fatalf("unknown value %q changed the requested renderer", v)
		}
	}
	if e.renderer != sw {
		t.Fatal("the option opened a renderer")
	}
	if err := e.SetBIOS("main_bios", make([]byte, biosSize)); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(); err != nil {
		t.Fatal(err)
	}
	if e.Renderer() != RendererSoftware || e.renderer != sw {
		t.Fatalf("after Start renderer %T mode %d", e.renderer, e.Renderer())
	}
}

// orderLog records the order of the calls a GPU renderer makes on
// its submission, buffers, and device.
type orderLog struct {
	calls []string
}

func (l *orderLog) add(call string) {
	if l != nil {
		l.calls = append(l.calls, call)
	}
}

// testDevice is a device for the renderer's own tests on any
// platform: Build makes one pipeline per entry, NewBuffer a host
// byte slice for a host buffer and no memory for a device buffer,
// whose Bytes is nil as on a real device, so a CPU touch of one fails
// the test (or fails when noBuffers is set), Begin a submission that
// accepts every call and does nothing, and Wait and Close record
// themselves.
type testDevice struct {
	log       *orderLog
	closed    bool
	noBuffers bool
	begins    int // submissions begun
}

func (d *testDevice) Name() string                       { return "test" }
func (d *testDevice) GroupThreads(hostgpu.GroupLoad) int { return 128 }

func (d *testDevice) Build(mod *hostgpu.Module) ([]hostgpu.Pipeline, error) {
	pipes := make([]hostgpu.Pipeline, len(mod.Entries))
	for i := range pipes {
		pipes[i] = i
	}
	return pipes, nil
}

func (d *testDevice) NewBuffer(size int, kind hostgpu.BufferKind) (hostgpu.Buffer, error) {
	if d.noBuffers {
		return nil, errors.New("test device: no buffers")
	}
	b := &testBuffer{size: size, kind: kind, log: d.log}
	if kind != hostgpu.BufferWorking {
		b.data = make([]byte, size)
	}
	return b, nil
}

func (d *testDevice) Begin() (hostgpu.Commands, error) {
	d.begins++
	return nopCommands{}, nil
}

func (d *testDevice) Wait(hostgpu.Submission) error {
	d.log.add("wait")
	return nil
}

func (d *testDevice) Close() {
	d.closed = true
	d.log.add("close")
}

type testBuffer struct {
	data []byte
	size int
	kind hostgpu.BufferKind
	log  *orderLog
}

func (b *testBuffer) Bytes() []byte            { return b.data }
func (b *testBuffer) Size() int                { return b.size }
func (b *testBuffer) Kind() hostgpu.BufferKind { return b.kind }
func (b *testBuffer) Release()                 { b.log.add("release") }

// deviceBytes reads a working buffer of the renderer back: a copy of
// its bytes into a readback buffer, in a submission waited for at
// once, which the device runs behind every submission before it.
func deviceBytes(t *testing.T, g *gpuRenderer, b hostgpu.Buffer) []byte {
	t.Helper()
	host, err := g.prog.Device.NewBuffer(b.Size(), hostgpu.BufferReadback)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Release()
	if err := g.waitedSubmit(func(cmds hostgpu.Commands) {
		cmds.Copy(host, 0, b, 0, b.Size())
	}); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(host.Bytes())
}

// TestNewGPURenderer builds the compute sources on the test device
// and checks the GPU renderer's construction on any platform:
// every entry point resolves, the buffers and the staging regions
// are allocated, and close closes the device; a device that refuses
// buffers fails the construction with the device closed.
func TestNewGPURenderer(t *testing.T) {
	parent, subs := ComputeSources()
	e := NewEmulator()
	dev := &testDevice{}
	prog, err := hostgpu.Build(dev, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(e.vdp2, e.vdp1, prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range g.pipes {
		if p == nil {
			t.Errorf("entry %s has no pipeline", entryNames[i])
		}
	}
	sizes := bufferSizes(0, gpuStagingRegions*g.batchRows, e.vdp2.frame.width, e.vdp2.DisplayHeight(), g.fbPixels, 1)
	if len(g.all) == 0 || g.deviceFramebuffer == nil || g.deviceFramebuffer.Size() != sizes.framebufferWords*4 {
		t.Error("the buffers were not allocated")
	}
	for i := range g.staging {
		s := &g.staging[i]
		if len(s.vramBytes) != compute.PageStoreBytes || len(s.cramBytes) != compute.CRAMPageStoreBytes ||
			len(s.vramPages) != compute.PageTableWords || len(s.cramPages) != compute.PageTableWords || cap(s.bands) == 0 {
			t.Errorf("staging region %d was not allocated", i)
		}
		// The copy params rows the region records are the rows its
		// buffer holds, gpuVDP1CopyKinds per line record it starts
		// with, so encoding a region full of records replaces nothing.
		if rows := len(s.vdp1Copy.Bytes()) / int(unsafe.Sizeof(draw.PageCopyParams{})); s.copyRows != rows || s.copyRows < gpuVDP1CopyKinds*cap(s.lines) {
			t.Errorf("staging region %d records %d copy params rows of a buffer holding %d, for %d line records", i, s.copyRows, rows, cap(s.lines))
		}
	}
	// Each buffer is allocated by the CPU's access to it: the stages
	// the CPU fills are upload buffers, the one a copy fills for the
	// CPU to read is a readback buffer, the frame buffer the composite
	// writes for the frontend to read is an output buffer, and the
	// ones only the device touches are working buffers.
	kinds := []struct {
		name string
		b    hostgpu.Buffer
		want hostgpu.BufferKind
	}{
		{"stage", g.stage, hostgpu.BufferUpload},
		{"extStage", g.extStage, hostgpu.BufferUpload},
		{"rows", g.rows, hostgpu.BufferUpload},
		{"copyParams", g.copyParams, hostgpu.BufferUpload},
		{"vram store", g.staging[0].vram, hostgpu.BufferUpload},
		{"pairStage", g.pairStage, hostgpu.BufferReadback},
		{"vram", g.vram, hostgpu.BufferWorking},
		{"palette", g.palette, hostgpu.BufferWorking},
		{"layer", g.layers[0], hostgpu.BufferWorking},
		{"line color", g.lineColor[0], hostgpu.BufferWorking},
		{"device frame buffer", g.deviceFramebuffer, hostgpu.BufferOutput},
		{"vdp1 pair", g.vdp1FB[0], hostgpu.BufferWorking},
		{"stamps", g.stamps, hostgpu.BufferWorking},
	}
	for _, k := range kinds {
		if k.b.Kind() != k.want {
			t.Errorf("%s allocated as kind %d, want %d", k.name, k.b.Kind(), k.want)
		}
	}
	if g.device() != "test" {
		t.Errorf("device %q", g.device())
	}
	g.close()
	if !dev.closed || g.all != nil {
		t.Error("close left the device open or the buffers held")
	}

	dev = &testDevice{noBuffers: true}
	if prog, err = hostgpu.Build(dev, parent, subs...); err != nil {
		t.Fatal(err)
	}
	if _, err := newGPURenderer(e.vdp2, e.vdp1, prog, gpuBatchLines, false); err == nil {
		t.Fatal("construction succeeded without buffers")
	}
	if !dev.closed {
		t.Error("a failed construction left the device open")
	}
}

// TestSetRendererSwitch checks the switch on any platform: a request
// for the active mode, on the active backend for GPU mode, does
// nothing; a request for software or for another backend closes the
// active GPU renderer's device before the new renderer opens; and a
// backend that cannot open falls back to software with an error
// naming it.
func TestSetRendererSwitch(t *testing.T) {
	install := func(e *Emulator) *testDevice {
		dev := &testDevice{}
		e.renderer = &gpuRenderer{vdp2: e.vdp2, vdp1: e.vdp1, prog: &hostgpu.Program{Device: dev}, backendName: "First"}
		e.rendererMode = RendererGPU
		return dev
	}

	e := NewEmulator()
	defer e.Close()
	if err := e.setRenderer(RendererSoftware, ""); err != nil || e.Renderer() != RendererSoftware {
		t.Fatalf("a software request on the software renderer: err %v, renderer %d", err, e.Renderer())
	}
	dev := install(e)
	if err := e.setRenderer(RendererGPU, "First"); err != nil || dev.closed || e.Renderer() != RendererGPU {
		t.Fatalf("a request on the active backend: err %v, closed %v, renderer %d", err, dev.closed, e.Renderer())
	}

	if err := e.setRenderer(RendererSoftware, ""); err != nil {
		t.Fatal(err)
	}
	if !dev.closed {
		t.Fatal("a software request left the active GPU renderer's device open")
	}
	if e.Renderer() != RendererSoftware {
		t.Fatalf("after a software request: renderer %d", e.Renderer())
	}

	dev = install(e)
	err := e.setRenderer(RendererGPU, "Missing")
	if !dev.closed {
		t.Fatal("a request on another backend left the active device open")
	}
	if err == nil || !strings.Contains(err.Error(), "GPU renderer (Missing)") {
		t.Fatalf("error %v", err)
	}
	if e.Renderer() != RendererSoftware {
		t.Fatalf("after the failure: renderer %d", e.Renderer())
	}
}

// TestEmulatorCloseReleasesRenderer checks that closing the emulator
// closes the active GPU renderer's device, once: a second Close does
// nothing.
func TestEmulatorCloseReleasesRenderer(t *testing.T) {
	e := NewEmulator()
	dev := &testDevice{log: &orderLog{}}
	e.renderer = &gpuRenderer{vdp2: e.vdp2, vdp1: e.vdp1, prog: &hostgpu.Program{Device: dev}}
	e.rendererMode = RendererGPU
	e.Close()
	if !dev.closed {
		t.Fatal("Close left the GPU renderer's device open")
	}
	if n := len(dev.log.calls); n != 1 {
		t.Fatalf("device calls after Close %v", dev.log.calls)
	}
	e.Close()
	if n := len(dev.log.calls); n != 1 {
		t.Fatalf("device calls after a second Close %v", dev.log.calls)
	}
}

// TestGPUCloseWaitsFirst checks that closing a GPU renderer
// with a submission in flight waits on it before releasing the
// buffers it binds, and closes the device last.
func TestGPUCloseWaitsFirst(t *testing.T) {
	log := &orderLog{}
	v := NewVDP2(NewSCU())
	g := &gpuRenderer{
		vdp2: v,
		vdp1: NewVDP1(NewSCU()),
		prog: &hostgpu.Program{Device: &testDevice{log: log}},
		all:  []hostgpu.Buffer{&testBuffer{log: log}, &testBuffer{log: log}},
		sub:  1,
	}
	g.close()
	want := []string{"wait", "release", "release", "close"}
	if len(log.calls) != len(want) {
		t.Fatalf("close made %v, want %v", log.calls, want)
	}
	for i := range want {
		if log.calls[i] != want[i] {
			t.Fatalf("close made %v, want %v", log.calls, want)
		}
	}
	if g.sub != 0 || g.all != nil {
		t.Fatal("close left the submission or the buffers held")
	}
}

// TestBeginFrameBlanked checks the flag the GPU renderer mirrors: set
// on the frame that changes geometry and on a display-off frame,
// clear on a steady frame with the display on.
func TestBeginFrameBlanked(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.Write(0x0000, 0x8000) // DISP=1, 320x224
	v.BeginFrame()
	v.Write(0x0000, 0x8001) // DISP=1, 352x224
	v.BeginFrame()
	if !v.frame.blanked {
		t.Fatal("geometry change frame not blanked")
	}
	v.BeginFrame()
	if v.frame.blanked {
		t.Fatal("steady frame blanked")
	}
	v.Write(0x0000, 0x0001) // DISP=0
	v.BeginFrame()
	if !v.frame.blanked {
		t.Fatal("display-off frame not blanked")
	}
	v.Write(0x0000, 0x8001)
	v.BeginFrame()
	if v.frame.blanked {
		t.Fatal("display-on frame without a geometry change blanked")
	}
}

// TestGPUBoundaries checks the boundary merge of a batch: page bands
// and CRAM mode changes in line order, a band and a mode change on
// one line merged, a band at the batch's first row kept, a band at
// or past the batch's end dropped, a mode change from a row before
// the batch not counted, and the mode lookup skipping inactive rows
// and reporting none past the end.
func TestGPUBoundaries(t *testing.T) {
	lines := make([]lineRecord, 6)
	for y := range lines {
		lines[y].NSpans = 1
	}
	lines[0].Comp.CRAMMode = 2
	lines[2].NSpans = 0
	lines[3].Comp.CRAMMode = 1
	lines[4].Comp.CRAMMode = 1
	lines[5].Comp.CRAMMode = 3
	bands := []gpuBand{{line: 1, vram: [2]int{0, 2}}, {line: 3, cram: [2]int{0, 1}}, {line: 5, vram: [2]int{2, 3}}}
	g := &gpuRenderer{}
	got := g.boundaries(lines, 1, 5, bands)
	want := []gpuBoundary{{line: 1, band: 0}, {line: 3, band: 1, modeChange: true}}
	if len(got) != len(want) {
		t.Fatalf("boundaries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("boundary %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if mode, ok := gpuModeFrom(lines, 2, 6); !ok || mode != 1 {
		t.Errorf("mode from row 2 = %d, %v; want 1 from row 3", mode, ok)
	}
	if _, ok := gpuModeFrom(lines, 2, 3); ok {
		t.Error("mode reported from an inactive row")
	}
	if _, ok := gpuModeFrom(lines, 6, 6); ok {
		t.Error("mode reported past the end")
	}
}

// TestGPURuns checks the row-run split: consecutive rows naming
// the same entry over the same layer join, a change of entry or a
// gap splits, layers are kept apart, and the external row makes no
// run.
func TestGPURuns(t *testing.T) {
	lines := make([]lineRecord, 6)
	set := func(y int, spans ...uint32) {
		lines[y].NSpans = uint32(len(spans))
		copy(lines[y].Spans[:], spans)
	}
	composite := spanWord(entryComposite, 0)
	set(0, spanWord(entryNBGCell, 0), spanWord(entryNBGCell, 2), composite)
	set(1, spanWord(entryNBGCell, 0), spanWord(entryNBGCell, 2), composite)
	lines[2].NSpans = 0
	set(3, spanWord(entryNBGCell, 0), spanWord(entryExternal, 1), spanWord(entryRBGCell, screen.LayerRBG0), composite)
	set(4, spanWord(entryNBGBitmap, 0), spanWord(entryRBGCell, screen.LayerRBG0), composite)
	set(5, spanWord(entryNBGBitmap, 0), spanWord(entryRBGBitmap, screen.LayerRBG0), spanWord(entryGradation, screen.LayerNBG3), composite)

	want := []gpuRun{
		{entryNBGCell, gpuStageLayers, 0, 0, 2},
		{entryNBGCell, gpuStageLayers, 2, 0, 2},
		{entryComposite, gpuStageComposite, 0, 0, 2},
		{entryNBGCell, gpuStageLayers, 0, 3, 1},
		{entryRBGCell, gpuStageLayers, screen.LayerRBG0, 3, 2},
		{entryComposite, gpuStageComposite, 0, 3, 3},
		{entryNBGBitmap, gpuStageLayers, 0, 4, 2},
		{entryRBGBitmap, gpuStageLayers, screen.LayerRBG0, 5, 1},
		{entryGradation, gpuStageGradation, screen.LayerNBG3, 5, 1},
	}
	g := &gpuRenderer{}
	got := g.splitRuns(lines)
	if len(got) != len(want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("run %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// copyLog is a submission that records its copies and fills and
// accepts every other call.
type copyLog struct {
	nopCommands
	copies []loggedCopy
	fills  []loggedFill
}

type loggedCopy struct {
	dst, src       hostgpu.Buffer
	dstOff, srcOff int
	n              int
}

type loggedFill struct {
	dst       hostgpu.Buffer
	offset, n int
	value     uint32
}

func (c *copyLog) Copy(dst hostgpu.Buffer, dstOffset int, src hostgpu.Buffer, srcOffset int, n int) {
	if err := hostgpu.CheckCopy(dst, dstOffset, src, srcOffset, n); err != nil {
		panic(err)
	}
	c.copies = append(c.copies, loggedCopy{dst, src, dstOffset, srcOffset, n})
}

func (c *copyLog) Fill(dst hostgpu.Buffer, offset int, n int, value uint32) {
	if err := hostgpu.CheckFill(dst, offset, n); err != nil {
		panic(err)
	}
	c.fills = append(c.fills, loggedFill{dst, offset, n, value})
}

// TestGPUInputFills checks the fills a batch encodes ahead of its
// work: the pending blank of the device frame buffer, once, with the
// opaque black of an output pixel, and the line color rows of each
// rotation layer, one fill of zero per run of rows at adjacent slots,
// a gap in the slots starting a new run.
func TestGPUInputFills(t *testing.T) {
	const width, slots = 16, 8
	g := &gpuRenderer{width: width, batchRows: slots, blankWords: 40}
	g.deviceFramebuffer = &testBuffer{size: 64 * 4, kind: hostgpu.BufferWorking}
	for i := range g.lineColor {
		g.lineColor[i] = &testBuffer{size: slots * width, kind: hostgpu.BufferWorking}
	}
	s := &gpuStaging{lineColorSlots: [2][]int{{0, 1, 2, 5}, {7}}}
	cmds := &copyLog{}
	if !g.encodeInputs(cmds, s) {
		t.Fatal("encodeInputs reported nothing encoded")
	}
	want := []loggedFill{
		{g.deviceFramebuffer, 0, 40 * 4, frameBlack},
		{g.lineColor[0], 0, 3 * width, 0},
		{g.lineColor[0], 5 * width, width, 0},
		{g.lineColor[1], 7 * width, width, 0},
	}
	if len(cmds.fills) != len(want) {
		t.Fatalf("fills = %+v, want %+v", cmds.fills, want)
	}
	for i := range want {
		if cmds.fills[i] != want[i] {
			t.Errorf("fill %d = %+v, want %+v", i, cmds.fills[i], want[i])
		}
	}
	if g.blankWords != 0 {
		t.Errorf("blank words %d after the batch, want 0", g.blankWords)
	}
}

// TestGPUExternalRowCopies checks the external row copies a batch
// encodes: one copy per run of rows at adjacent slots into the same
// layer, from the run's first slot in extStage to the same slot of the
// layer, a gap in the slots or a change of layer starting a new run.
func TestGPUExternalRowCopies(t *testing.T) {
	const width, slots = 16, 8
	rowBytes := width * 4
	g := &gpuRenderer{width: width, batchRows: slots}
	for i := range g.layers {
		g.layers[i] = &testBuffer{size: slots * rowBytes, kind: hostgpu.BufferWorking}
	}
	g.extStage = &testBuffer{size: slots * rowBytes, kind: hostgpu.BufferUpload}
	s := &gpuStaging{extRows: []gpuExtRow{
		{slot: 0, layer: 2}, {slot: 1, layer: 2}, {slot: 3, layer: 2}, {slot: 4, layer: 1}, {slot: 5, layer: 1},
	}}
	cmds := &copyLog{}
	if !g.encodeInputs(cmds, s) {
		t.Fatal("encodeInputs reported nothing encoded")
	}
	want := []loggedCopy{
		{g.layers[2], g.extStage, 0, 0, 2 * rowBytes},
		{g.layers[2], g.extStage, 3 * rowBytes, 3 * rowBytes, rowBytes},
		{g.layers[1], g.extStage, 4 * rowBytes, 4 * rowBytes, 2 * rowBytes},
	}
	if len(cmds.copies) != len(want) {
		t.Fatalf("copies = %+v, want %+v", cmds.copies, want)
	}
	for i := range want {
		if cmds.copies[i] != want[i] {
			t.Errorf("copy %d = %+v, want %+v", i, cmds.copies[i], want[i])
		}
	}
}

// benchRowRecords renders one frame of a benchmark scene and returns
// the VDP2 holding the frame's row records.
func benchRowRecords(s benchVDP2Scene) (*VDP2, vdp1FBView) {
	v, fb := s.build()
	renderFrame(v, newSoftwareRenderer(v), fb, nil)
	return v, fb
}

// TestGPUEncodeNoAllocs checks that deriving a frame's boundaries and
// row runs from the records allocates nothing once the renderer's
// slices have grown, for every benchmark scene, with a band and a
// mode change on every row.
func TestGPUEncodeNoAllocs(t *testing.T) {
	for _, s := range benchVDP2Scenes() {
		t.Run(s.name, func(t *testing.T) {
			v, _ := benchRowRecords(s)
			lines := v.rows.lines[:int(v.ActiveLines())]
			bands := make([]gpuBand, len(lines))
			for y := range lines {
				bands[y] = gpuBand{line: y, vram: [2]int{y, y + 1}}
				lines[y].Comp.CRAMMode = uint32(y & 1)
			}
			g := &gpuRenderer{}
			g.boundaries(lines, 0, len(lines), bands)
			g.splitRuns(lines)
			if n := testing.AllocsPerRun(10, func() { g.boundaries(lines, 0, len(lines), bands) }); n != 0 {
				t.Errorf("boundaries: %.0f allocations per frame", n)
			}
			if n := testing.AllocsPerRun(10, func() { g.splitRuns(lines) }); n != 0 {
				t.Errorf("splitRuns: %.0f allocations per frame", n)
			}
		})
	}
}

// TestGPUEncodeParamsBounds drives a batch encode at the row limit,
// the renderer submitting the frame as one batch, with a band of each
// memory and a CRAM mode change on every row, the most palette
// rebuilds and page copies a frame can ask for, and checks that the
// params rows land where the buffers hold them: the palette by CRAM
// mode, the copies by row slot and memory, the last row in its slot's
// two copy rows.
func TestGPUEncodeParamsBounds(t *testing.T) {
	parent, subs := ComputeSources()
	v := NewVDP2(NewSCU())
	prog, err := hostgpu.Build(&testDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(v, NewVDP1(NewSCU()), prog, rowRecordMax, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()

	g.width = 320
	s := &g.staging[g.cur]
	for y := 0; y < rowRecordMax; y++ {
		r := &v.rows.lines[y]
		*r = lineRecord{NSpans: 1}
		r.Spans[0] = spanWord(entryComposite, 0)
		// Alternating modes make every row a mode change as well as
		// a band boundary.
		r.Comp.CRAMMode = uint32(y & 1)
		s.bands = append(s.bands, gpuBand{line: y, vram: [2]int{y, y + 1}, cram: [2]int{y, y + 1}})
	}
	s.vramSlots, s.cramSlots = rowRecordMax, rowRecordMax
	g.rowsEnded = rowRecordMax
	g.endDisplay()
	if g.failure != nil {
		t.Fatal(g.failure)
	}

	pal := unsafe.Slice((*screen.PaletteParams)(unsafe.Pointer(&g.paletteParams.Bytes()[0])), gpuCRAMModes)
	for mode := uint32(0); mode < 2; mode++ {
		if pal[mode].Mode != mode {
			t.Errorf("palette params row %d holds mode %d", mode, pal[mode].Mode)
		}
	}
	copies := g.copyView
	last := uint32(rowRecordMax - 1)
	if copies[2*last].Base != last || copies[2*last+1].Base != last {
		t.Errorf("the last row's copy params hold %d and %d, want %d in both", copies[2*last].Base, copies[2*last+1].Base, last)
	}
}

// TestGPUGrowFailureEndsFrame opens the GPU renderer on the test
// device at a 320-dot frame, then has the device refuse buffers and
// renders a frame in the 704-dot hi-res double-density interlaced
// mode, which needs every frame-sized buffer to grow: the frame
// completes without writing past the old buffers, and the renderer
// reports the failure, which the frame loop takes to switch to
// software.
func TestGPUGrowFailureEndsFrame(t *testing.T) {
	parent, subs := ComputeSources()
	v, fb := benchVDP2Scenes()[0].build()
	dev := &testDevice{}
	prog, err := hostgpu.Build(dev, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(v, NewVDP1(NewSCU()), prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	renderFrame(v, g, fb, nil)
	if err := g.err(); err != nil {
		t.Fatal(err)
	}
	dev.noBuffers = true
	v.Write(0, v.regs[vdp2TVMD]|0x00C3)
	renderFrame(v, g, fb, nil)
	if err := g.err(); err == nil {
		t.Fatal("a frame whose buffers could not grow reported no failure")
	}
}

// nopCommands is a submission that accepts every call and does
// nothing, for exercising the encode paths without a backend, some
// of them on a renderer with no buffers at all. A copy is held to the
// device contract's ranges, as a backend holds it.
type nopCommands struct{}

func (nopCommands) Barrier()                   {}
func (nopCommands) Commit() hostgpu.Submission { return 1 }

// Dispatch accepts the binding list of a renderer with no buffers,
// which the encode tests build, and refuses a readback buffer as a
// backend does.
func (nopCommands) Dispatch(_ hostgpu.Pipeline, buffers []hostgpu.Buffer, _ hostgpu.Dispatch, _ [2]int) {
	for _, b := range buffers {
		if b != nil && b.Kind() == hostgpu.BufferReadback {
			panic(hostgpu.ErrReadbackBound)
		}
	}
}

func (nopCommands) Copy(dst hostgpu.Buffer, dstOffset int, src hostgpu.Buffer, srcOffset int, n int) {
	if err := hostgpu.CheckCopy(dst, dstOffset, src, srcOffset, n); err != nil {
		panic(err)
	}
}

func (nopCommands) Fill(dst hostgpu.Buffer, offset int, n int, value uint32) {
	if err := hostgpu.CheckFill(dst, offset, n); err != nil {
		panic(err)
	}
}

// TestGPUFrameNoAllocs drives whole frames through the GPU
// renderer's points on the test device, the frame loop's order, with
// a VRAM and a CRAM write before every row, and checks that the frame
// path allocates nothing once the first frame has run: the snapshot,
// the row captures, the page takes, the uploads, the batch encodes,
// and the readback, for every benchmark scene, on the GPU renderer
// and on the enhanced GPU renderer at 2x, whose rows also derive their
// sub-rows.
func TestGPUFrameNoAllocs(t *testing.T) {
	parent, subs := ComputeSources()
	opens := []struct {
		name string
		open func(v *VDP2, prog *hostgpu.Program) (frameRenderer, error)
	}{
		{"gpu", func(v *VDP2, prog *hostgpu.Program) (frameRenderer, error) {
			return newGPURenderer(v, NewVDP1(NewSCU()), prog, gpuBatchLines, false)
		}},
		{"enhanced gpu 2x", func(v *VDP2, prog *hostgpu.Program) (frameRenderer, error) {
			v.setEnhance(enhanceConfig{shift: 1})
			return newEnhancedGPURenderer(v, NewVDP1(NewSCU()), prog, gpuBatchLines>>1)
		}},
	}
	for _, o := range opens {
		for _, s := range benchVDP2Scenes() {
			t.Run(o.name+" "+s.name, func(t *testing.T) {
				gpuFrameNoAllocs(t, s, func(v *VDP2) (frameRenderer, error) {
					prog, err := hostgpu.Build(&testDevice{}, parent, subs...)
					if err != nil {
						return nil, err
					}
					return o.open(v, prog)
				})
			})
		}
	}
}

// gpuFrameNoAllocs opens a renderer on the scene's VDP2 and checks
// that its frames allocate nothing after the first.
func gpuFrameNoAllocs(t *testing.T, s benchVDP2Scene, open func(v *VDP2) (frameRenderer, error)) {
	t.Helper()
	v, fb := s.build()
	g, err := open(v)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	frame := func() {
		v.BeginFrame()
		g.beginFrame()
		lines := int(v.ActiveLines())
		for y := 0; y < lines; y++ {
			v.WriteVRAM16(uint32(y)*compute.PageBytes, uint16(y))
			v.WriteCRAM16(uint32(y&0x3FF)*2, uint16(y))
			beginRow(v, g, y, fb)
			g.renderLine(y)
			v.EndLine()
		}
		g.endDisplay()
		v.EndFrame()
		g.endFrame()
	}
	frame()
	if err := g.err(); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(5, frame); n != 0 {
		t.Errorf("%.0f allocations per frame", n)
	}
	if err := g.err(); err != nil {
		t.Fatal(err)
	}
}

// TestGPUEncodeRowsNoAllocs runs the frame's row encode against a
// no-op command buffer and checks that the run split, the binding
// lists, and the dispatch records allocate nothing.
func TestGPUEncodeRowsNoAllocs(t *testing.T) {
	for _, s := range benchVDP2Scenes() {
		t.Run(s.name, func(t *testing.T) {
			v, _ := benchRowRecords(s)
			lines := v.rows.lines[:int(v.ActiveLines())]
			g := &gpuRenderer{vdp2: v, width: int(v.ActiveWidth())}
			var cmds nopCommands
			g.encodeRows(cmds, lines, 0, len(lines), 0)
			if n := testing.AllocsPerRun(10, func() { g.encodeRows(cmds, lines, 0, len(lines), 0) }); n != 0 {
				t.Errorf("encodeRows: %.0f allocations per frame", n)
			}
		})
	}
}

// TestGPUTakePages drives rows through the GPU renderer's points on
// the exec device and checks the page delivery: open marks every page,
// so row 0's take stages the whole of VRAM and CRAM as one band
// stamped with the row, each page's bytes and page number in the
// store and the table, written before the frame or not; a row's
// render point after it takes the pages written since the last take,
// a row without writes takes nothing; the device copies hold the
// VDP2's memory once the frame has run; and the sprite input captured
// for the frame reaches the device buffer with the batch. The rows
// are checked before their row point can submit a batch, so the
// region inspected is the one the take filled.
func TestGPUTakePages(t *testing.T) {
	parent, subs := ComputeSources()
	v := NewVDP2(NewSCU())
	v.Write(0, 0x8000)           // DISP=1, 320x224
	v.regs[vdp2RAMCTL] = 1 << 12 // CRAM mode 1: no mirror
	prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	v1 := NewVDP1(NewSCU())
	v1.displayFB[100] = 0xAB
	g, err := newGPURenderer(v, v1, prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	fb := vdp1FBView{data: v1.DisplayFB(), width: 512, height: 256}

	v.WriteVRAM16(3*compute.PageBytes, 1)
	v.BeginFrame()
	g.beginFrame()
	v.WriteVRAM32(5*compute.PageBytes+8, 0x11223344)
	v.WriteCRAM16(2*compute.PageBytes+4, 0x7FFF)
	v.BeginLine(0, fb)
	s := &g.staging[g.cur]
	g.takePages(0)
	if len(s.bands) != 1 || s.bands[0] != (gpuBand{line: 0, vram: [2]int{0, vdp2VRAMPages}, cram: [2]int{0, vdp2CRAMPages}}) {
		t.Fatalf("bands after row 0's take = %+v", s.bands)
	}
	for slot := range vdp2VRAMPages {
		p := s.vramPages[slot]
		if !bytes.Equal(s.vramBytes[slot*compute.PageBytes:(slot+1)*compute.PageBytes], v.vram[p*compute.PageBytes:(p+1)*compute.PageBytes]) {
			t.Fatalf("VRAM page %d was not staged whole at slot %d", p, slot)
		}
	}
	for slot := range vdp2CRAMPages {
		p := s.cramPages[slot]
		if !bytes.Equal(s.cramBytes[slot*compute.PageBytes:(slot+1)*compute.PageBytes], v.cram[p*compute.PageBytes:(p+1)*compute.PageBytes]) {
			t.Fatalf("CRAM page %d was not staged whole at slot %d", p, slot)
		}
	}
	for _, page := range []uint32{3, 5} {
		if !slices.Contains(s.vramPages[:s.vramSlots], page) {
			t.Fatalf("VRAM page %d was not taken at row 0", page)
		}
	}
	g.renderLine(0)
	v.EndLine()

	v.BeginLine(1, fb)
	s = &g.staging[g.cur]
	g.takePages(1)
	for _, b := range s.bands {
		if b.line == 1 {
			t.Fatal("a row without writes recorded a band")
		}
	}
	g.renderLine(1)
	v.EndLine()

	v.WriteVRAM(5*compute.PageBytes, 9)
	v.BeginLine(2, fb)
	s = &g.staging[g.cur]
	g.takePages(2)
	last := s.bands[len(s.bands)-1]
	if last.line != 2 || last.vram[1]-last.vram[0] != 1 || last.cram[1] != last.cram[0] || s.vramPages[last.vram[0]] != 5 {
		t.Fatalf("row 2's band = %+v, want VRAM page 5 alone", last)
	}
	g.renderLine(2)
	v.EndLine()
	g.endDisplay()
	if g.sub == 0 {
		t.Fatal("the display ended without a submission")
	}
	if half := g.vdp1FB[v1.physicalBuffer(v1.DisplayFB())]; g.spriteBind != half || deviceBytes(t, g, half)[100] != 0xAB {
		t.Fatal("the composite did not bind the display half holding the sprite input")
	}
	v.EndFrame()
	g.endFrame()
	if g.sub != 0 {
		t.Fatal("endFrame left the submission held")
	}
	if !bytes.Equal(deviceBytes(t, g, g.vram), v.vram) || !bytes.Equal(deviceBytes(t, g, g.cram), v.cram) {
		t.Fatal("the device copies do not hold VRAM and CRAM after the frame")
	}
}

// TestGPUEndFrameWithoutSubmission drives a frame whose first
// submission comes after the display on the exec device: the display
// is off, so no row is active and nothing is pending from the frame
// before, then a draw started in the vertical blank and a pair read
// submit the VDP1 work, which carries the pending blank to the device
// frame buffer ahead of it, so endFrame's wait leaves the frame
// buffer black. A display-off frame with no VDP1 work either submits
// nothing until endFrame, which writes the blank in a submission of
// its own.
func TestGPUEndFrameWithoutSubmission(t *testing.T) {
	parent, subs := ComputeSources()
	v1 := vdp1FrameScenes()[0].build()
	v1.setPairKept(false)
	v2, fb := benchVDP2Scenes()[0].build()
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
	active := int(v2.ActiveLines())
	frame := func() {
		v2.BeginFrame()
		g.beginFrame()
		for y := 0; y < active; y++ {
			beginRow(v2, g, y, fb)
			g.renderLine(y)
			v2.EndLine()
		}
		g.endDisplay()
	}
	// A displayed frame first, so the device holds a picture.
	frame()
	for line := active; line < vdp1DriveScanlines; line++ {
		v1.TickSystemCycles(benchVDP1WalkerTick)
		g.vdp1Line(line)
	}
	v2.EndFrame()
	g.endFrame()
	n := g.displayHeight() * g.framebufferStride()
	want := make(compute.Framebuffer, n/4)
	blankFramebuffer(want, n/4)
	if got := g.framebuffer()[:n]; bytes.Equal(got, framebufferBytes(want)) {
		t.Fatal("the displayed frame is black")
	}
	checkBlack := func(what string) {
		t.Helper()
		if err := g.err(); err != nil {
			t.Fatal(err)
		}
		if got := g.framebuffer()[:n]; !bytes.Equal(got, framebufferBytes(want)) {
			t.Fatalf("%s differs from black at byte %d", what, firstDiff(got, framebufferBytes(want)))
		}
	}

	v2.Write(0, v2.regs[vdp2TVMD]&^0x8000)
	frame()
	if !v2.frame.blanked || g.sub != 0 {
		t.Fatalf("blanked %v, submission %d after the display, want a blanked frame that submitted nothing", v2.frame.blanked, g.sub)
	}
	v1.Write(0x04, 0x0001)
	for line := active; line < vdp1DriveScanlines; line++ {
		v1.TickSystemCycles(benchVDP1WalkerTick)
		g.vdp1Line(line)
		if line == active+20 {
			g.syncPair(line)
			v1.setPairKept(true)
		}
	}
	if g.sub == 0 {
		t.Fatal("the vertical blank submitted nothing")
	}
	v2.EndFrame()
	g.endFrame()
	checkBlack("the output of a frame whose only submission is VDP1 work")

	// The device frame buffer holds a picture again, and a display-off
	// frame with no VDP1 work of its own submits nothing after
	// beginFrame, which submits the last vertical blank's work ahead
	// of the blank, so the blank is still pending at endFrame, which
	// writes it in one submission.
	data := execData(g.deviceFramebuffer)
	for i := range data {
		data[i] = 0x5A
	}
	frame()
	if !v2.frame.blanked || g.blankWords == 0 {
		t.Fatalf("blanked %v, blank words %d after the display, want a blanked frame with its blank pending", v2.frame.blanked, g.blankWords)
	}
	before := dev.subs
	v2.EndFrame()
	g.endFrame()
	if g.blankWords != 0 || dev.subs != before+1 {
		t.Fatalf("blank words %d and %d submissions at endFrame, want 0 and 1", g.blankWords, dev.subs-before)
	}
	checkBlack("the output of a frame that submitted nothing")
}

// TestGPUBatches counts the submissions a frame makes on the test
// device for a scene with every row active, after the setup
// submissions of open: one per gpuBatchLines rows plus the remainder,
// and, with every VRAM page written before every row, one per the
// rows a staging region holds when that is fewer.
func TestGPUBatches(t *testing.T) {
	parent, subs := ComputeSources()
	s := benchVDP2Scenes()[0]
	for _, heavy := range []bool{false, true} {
		v, fb := s.build()
		dev := &testDevice{}
		prog, err := hostgpu.Build(dev, parent, subs...)
		if err != nil {
			t.Fatal(err)
		}
		g, err := newGPURenderer(v, NewVDP1(NewSCU()), prog, gpuBatchLines, false)
		if err != nil {
			t.Fatal(err)
		}
		dev.begins = 0
		lines := int(v.ActiveLines())
		v.BeginFrame()
		g.beginFrame()
		for y := 0; y < lines; y++ {
			if heavy {
				for p := 0; p < vdp2VRAMPages; p++ {
					v.WriteVRAM(uint32(p)*compute.PageBytes, byte(y))
				}
			}
			v.BeginLine(y, fb)
			g.renderLine(y)
			v.EndLine()
		}
		g.endDisplay()
		v.EndFrame()
		g.endFrame()
		if err := g.err(); err != nil {
			t.Fatal(err)
		}
		per := gpuBatchLines
		if heavy {
			per = min(per, gpuStoreVRAMPages/vdp2VRAMPages)
		}
		if want := (lines + per - 1) / per; dev.begins != want {
			t.Errorf("heavy %v: %d submissions for %d rows, want %d", heavy, dev.begins, lines, want)
		}
		g.close()
	}
}
