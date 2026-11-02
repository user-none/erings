// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package dx12

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/compile"
	"github.com/user-none/erings/internal/hostgpu/compile/hlsl"
)

// missing is the shader set of a build made before the generator ran:
// every open reports how the shaders are generated and embedded.
type missing struct{}

func (missing) Open(name string) (fs.File, error) {
	return nil, errors.New("this build embeds no DX12 shaders; run make dxil and rebuild")
}

// device is the Direct3D 12 device on its adapter, one compute
// queue, the fence whose values are the submission sequence numbers
// and the event Wait blocks on, the pipelines Build made, the one
// encoder Begin hands out with its pool of command allocators and
// lists, and the submissions committed and not yet waited on, in
// commit order. The encoder, the pool, and the in-flight list are
// retained, so the frame path allocates nothing once the first frame
// has run.
type device struct {
	shaders  fs.FS
	factory  uintptr
	adapter  uintptr
	dev      uintptr
	queue    uintptr
	fence    uintptr
	event    windows.Handle
	name     string
	pipes    []*pipeline
	enc      *commands
	fill     *pipeline // the backend's fill kernel (buildFill)
	free     []slot
	inflight hostgpu.Ledger[submission] // the committed slots, by sequence number
	slots    [argSlots]uintptr          // the argument array for calls off the encoder

	// Values the create calls take by pointer, and the out parameters
	// they write, retained here so the addresses stay valid across
	// the call: a local's address taken as a uintptr is not updated
	// when the goroutine's stack moves.
	queueDesc commandQueueDesc
	heap      heapProperties
	desc      resourceDesc
	psoDesc   computePipelineStateDesc
	model     featureDataShaderModel
	arch      featureDataArchitecture
	options   featureDataOptions1
	adesc     adapterDesc1
	out       uintptr        // an object a create call returns
	mapped    unsafe.Pointer // a mapping Map returns
	newSlot   slot           // the slot takeSlot creates

	placement placement // the heaps NewBuffer tries, by kind (choosePlacement)
}

// bufferKinds is the number of buffer kinds, which index a placement.
const bufferKinds = int(hostgpu.BufferOutput) + 1

// placement is, by buffer kind, the heaps NewBuffer tries in order,
// best first, the first that creates the buffer holding it.
type placement [bufferKinds][]heapProperties

// customHeap is the custom heap with the properties of a standard heap
// type on the adapter's architecture: the equivalence
// GetCustomHeapProperties documents, derived here from the
// architecture feature rather than asked of the method, which returns
// its structure by value through a register Go cannot set on arm64.
// An upload heap is write-combined system memory, or write-back on a
// cache-coherent UMA adapter. A readback heap is write-back system
// memory on every adapter. Node mask 1 is the one node.
func customHeap(typ uint32, arch featureDataArchitecture) heapProperties {
	h := heapProperties{typ: heapTypeCustom, cpuPageProperty: cpuPageWriteBack, memoryPool: memoryPoolL0, creationNodeMask: 1, visibleNodeMask: 1}
	if typ == heapTypeUpload && arch.cacheCoherentUMA == 0 {
		h.cpuPageProperty = cpuPageWriteCombine
	}
	return h
}

// choosePlacement builds the placement from the custom heaps with the
// upload and readback heap types' properties (customHeap), which carry
// the CPU page property and the memory pool of the adapter's
// architecture.
//
//   - Working: the default heap, the device's own memory.
//   - Upload: the readback heap's properties, CPU-cached pages, then
//     the upload heap's. The renderer's writes to its upload buffers
//     include small scattered stores, and on both a discrete and an
//     integrated device the frame measured faster with them cached
//     than write-combined.
//   - Readback and output: the readback heap's properties, which the
//     CPU reads.
//
// Every buffer is bound as an unordered access view, which the upload
// and readback heap types do not allow, so the CPU-visible kinds are
// custom heaps with those types' properties. The last heap of every
// kind is one the driver defines on every adapter.
func choosePlacement(upload, readback heapProperties) placement {
	var p placement
	p[hostgpu.BufferWorking] = []heapProperties{{typ: heapTypeDefault, creationNodeMask: 1, visibleNodeMask: 1}}
	// On a cache-coherent UMA adapter the two are the same heap.
	p[hostgpu.BufferUpload] = []heapProperties{readback}
	if upload != readback {
		p[hostgpu.BufferUpload] = append(p[hostgpu.BufferUpload], upload)
	}
	p[hostgpu.BufferReadback] = []heapProperties{readback}
	p[hostgpu.BufferOutput] = []heapProperties{readback}
	return p
}

// slot is a command allocator with the command list recorded on it,
// reused once the submission that used it has completed.
type slot struct {
	allocator uintptr
	list      uintptr
}

// submission is a committed slot with the error its commit reported,
// if any.
type submission struct {
	slot slot
	err  error
}

// pipeline is a compute pipeline state for one entry point with the
// root signature its blob carries, its layout, and its workgroup.
type pipeline struct {
	state     uintptr
	rootSig   uintptr
	workgroup [2]uint32
	layout    hlsl.Layout
}

// buffer is a committed resource: an upload, readback, or output
// buffer on a CPU-visible custom heap, mapped for its lifetime, a
// working buffer on the default heap with no mapping and no data.
type buffer struct {
	res  uintptr
	va   uintptr // the GPU virtual address a root descriptor binds
	size int
	kind hostgpu.BufferKind
	data []byte

	// The buffer's resource state in the command list being recorded
	// whose epoch it carries. A buffer decays to the common state when
	// a command list completes, so in any other list it is common.
	state uint32
	epoch uint64
}

// Warm opens a device and closes it, so the libraries, the adapter's
// driver, and the fill pipeline are loaded and built once before the
// first Open the renderer makes. A failure is returned; Open reports
// the same failure when it is made.
func Warm() error {
	d, err := Open(RendererShaders())
	if err != nil {
		return err
	}
	d.Close()
	return nil
}

// Open creates the device on the first hardware adapter with the
// compiled entry points in shaders, by name: RendererShaders for the
// renderer, a test's own set for the conformance entries. Every
// failure is an error, and one that means no usable device wraps
// ErrUnavailable.
func Open(shaders fs.FS) (hostgpu.Device, error) {
	if err := load(); err != nil {
		return nil, err
	}
	d := &device{shaders: shaders}
	d.enc = &commands{dev: d}
	if err := d.open(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// unavailable is an error that means no usable device.
func unavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", hostgpu.ErrUnavailable, fmt.Sprintf(format, args...))
}

// open creates the factory, takes the adapter, creates the device,
// checks its features, and creates the queue, the fence, and the
// event. A requirement the adapter fails is ErrUnavailable naming the
// adapter and the requirement.
func (d *device) open() error {
	if err := hresult(call(&d.slots, createFactory2, 0, uintptr(unsafe.Pointer(&iidFactory4)), uintptr(unsafe.Pointer(&d.factory))), "DXGI factory"); err != nil {
		return unavailable("%v", err)
	}
	if err := d.hardwareAdapter(); err != nil {
		return err
	}
	adapter := windows.UTF16ToString(d.adesc.description[:])
	if err := hresult(call(&d.slots, createDevice, d.adapter, featureLevel11_0, uintptr(unsafe.Pointer(&iidDevice)), uintptr(unsafe.Pointer(&d.dev))), "device"); err != nil {
		return unavailable("%s: no device at feature level 11_0, which the renderer requires (%v)", adapter, err)
	}
	d.model.highest = shaderModel6_0
	if err := hresult(com(&d.slots, d.dev, deviceCheckFeatureSupport, featureShaderModel, uintptr(unsafe.Pointer(&d.model)), unsafe.Sizeof(d.model)), "shader model query"); err != nil {
		return unavailable("%s: %v", adapter, err)
	}
	if d.model.highest < shaderModel6_0 {
		return unavailable("%s: highest shader model %d.%d, the renderer requires 6.0", adapter, d.model.highest>>4, d.model.highest&0xF)
	}
	if err := hresult(com(&d.slots, d.dev, deviceCheckFeatureSupport, featureOptions1, uintptr(unsafe.Pointer(&d.options)), unsafe.Sizeof(d.options)), "options query"); err != nil {
		return unavailable("%s: %v", adapter, err)
	}
	if d.options.int64ShaderOps == 0 {
		return unavailable("%s: no 64-bit integer shader operations (Int64ShaderOps), which the renderer requires", adapter)
	}
	d.queueDesc = commandQueueDesc{typ: commandListTypeCompute}
	if err := hresult(com(&d.slots, d.dev, deviceCreateCommandQueue, uintptr(unsafe.Pointer(&d.queueDesc)), uintptr(unsafe.Pointer(&iidCommandQueue)), uintptr(unsafe.Pointer(&d.queue))), "command queue"); err != nil {
		return err
	}
	if err := hresult(com(&d.slots, d.dev, deviceCreateFence, 0, 0, uintptr(unsafe.Pointer(&iidFence)), uintptr(unsafe.Pointer(&d.fence))), "fence"); err != nil {
		return err
	}
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return fmt.Errorf("event: %w", err)
	}
	d.event = event
	d.name = adapter
	if err := hresult(com(&d.slots, d.dev, deviceCheckFeatureSupport, featureArchitecture, uintptr(unsafe.Pointer(&d.arch)), unsafe.Sizeof(d.arch)), "architecture query"); err != nil {
		return err
	}
	d.placement = choosePlacement(customHeap(heapTypeUpload, d.arch), customHeap(heapTypeReadback, d.arch))
	return d.buildFill()
}

// buildFill builds the pipeline of the backend's fill kernel from the
// embedded backend set. A build that embeds no backend set has no fill
// and so no usable device: ErrUnavailable.
func (d *device) buildFill() error {
	blob, err := fs.ReadFile(backendBlobs, "dxil/backend/"+hlsl.FillEntry+".dxil")
	if err != nil {
		return unavailable("fill kernel: %v; run make dxil and rebuild", err)
	}
	d.fill, err = d.pipeline(hlsl.FillEntry, blob, hlsl.FillLayout())
	return err
}

// hardwareAdapter takes the first adapter that is not a software
// adapter, its description in d.adesc. The order is the platform's
// (EnumAdapters1: index 0 is the adapter of the primary display), so
// an adapter that fails a requirement is reported, not passed over
// for a later one. None is ErrUnavailable.
func (d *device) hardwareAdapter() error {
	for i := uintptr(0); ; i++ {
		d.out = 0
		r := com(&d.slots, d.factory, factoryEnumAdapters1, i, uintptr(unsafe.Pointer(&d.out)))
		if uint32(r) == dxgiErrorNotFound {
			return unavailable("no hardware adapter")
		}
		if err := hresult(r, "adapter enumeration"); err != nil {
			return unavailable("%v", err)
		}
		d.adesc = adapterDesc1{}
		if hresult(com(&d.slots, d.out, adapterGetDesc1, uintptr(unsafe.Pointer(&d.adesc))), "") != nil || d.adesc.flags&adapterFlagSoftware != 0 {
			release(&d.slots, &d.out)
			continue
		}
		d.adapter, d.out = d.out, 0
		return nil
	}
}

// Name is the adapter's description.
func (d *device) Name() string { return d.name }

// GroupThreads is hostgpu.GroupWidth with the device's SIMD lanes
// (D3D12_FEATURE_DATA_D3D12_OPTIONS1 TotalLaneCount, zero when the
// driver does not report it) and the widest group row one dispatch
// issues.
func (d *device) GroupThreads(load hostgpu.GroupLoad) int {
	return hostgpu.GroupWidth(load, int(d.options.totalLaneCount), hlsl.GroupWorkgroup, dispatchMaxGroups*hlsl.GroupWorkgroup)
}

// Build makes one pipeline per entry point in the module's entry
// order from the embedded blob of the entry's name, with the root
// signature the blob carries and the entry's layout, which carries
// its workgroup.
// A blob the set lacks is ErrUnavailable: the build embeds no
// shaders, or the set is older than the compute sources.
func (d *device) Build(mod *compile.Module) ([]hostgpu.Pipeline, error) {
	pipes := make([]hostgpu.Pipeline, 0, len(mod.Entries))
	for i, en := range mod.Entries {
		name := mod.Funcs[en.Func].Name
		blob, err := fs.ReadFile(d.shaders, name+".dxil")
		if err != nil {
			return nil, unavailable("%s: %v", name, err)
		}
		p, err := d.pipeline(name, blob, hlsl.EntryLayout(mod, i))
		if err != nil {
			return nil, err
		}
		pipes = append(pipes, p)
	}
	return pipes, nil
}

// pipeline creates the root signature the blob carries and the
// compute pipeline state for the entry, and keeps them for Close.
// The workgroup is the layout's, the numthreads the blob was emitted
// with.
func (d *device) pipeline(name string, blob []byte, layout hlsl.Layout) (*pipeline, error) {
	if len(blob) == 0 {
		return nil, fmt.Errorf("%s: empty blob", name)
	}
	p := &pipeline{layout: layout, workgroup: [2]uint32{uint32(layout.Workgroup[0]), uint32(layout.Workgroup[1])}}
	code := uintptr(unsafe.Pointer(&blob[0]))
	r := com(&d.slots, d.dev, deviceCreateRootSignature, 0, code, uintptr(len(blob)), uintptr(unsafe.Pointer(&iidRootSignature)), uintptr(unsafe.Pointer(&p.rootSig)))
	if err := hresult(r, "root signature of "+name); err != nil {
		return nil, err
	}
	d.psoDesc = computePipelineStateDesc{rootSignature: p.rootSig, code: code, codeLength: uintptr(len(blob))}
	r = com(&d.slots, d.dev, deviceCreateComputePipelineState, uintptr(unsafe.Pointer(&d.psoDesc)), uintptr(unsafe.Pointer(&iidPipelineState)), uintptr(unsafe.Pointer(&p.state)))
	runtime.KeepAlive(blob)
	if err := hresult(r, "pipeline of "+name); err != nil {
		release(&d.slots, &p.rootSig)
		return nil, err
	}
	d.pipes = append(d.pipes, p)
	return p, nil
}

// NewBuffer allocates a buffer of size bytes on the first heap of the
// kind's placement (choosePlacement) that creates it. A CPU-visible
// kind is mapped for its lifetime. Every buffer is created in the
// common state, the state a buffer starts in whatever initial state
// is given, and promoted to unordered access at use.
func (d *device) NewBuffer(size int, kind hostgpu.BufferKind) (hostgpu.Buffer, error) {
	if int(kind) >= len(d.placement) {
		return nil, fmt.Errorf("buffer kind %d", kind)
	}
	d.desc = resourceDesc{dimension: resourceDimensionBuffer, width: uint64(size), height: 1, depthOrArraySize: 1, mipLevels: 1, sampleCount: 1, layout: textureLayoutRowMajor, flags: resourceFlagUAV}
	err := fmt.Errorf("no heap for a buffer of kind %d", kind)
	for _, heap := range d.placement[kind] {
		d.heap = heap
		d.out = 0
		r := com(&d.slots, d.dev, deviceCreateCommittedResource, uintptr(unsafe.Pointer(&d.heap)), 0, uintptr(unsafe.Pointer(&d.desc)), resourceStateCommon, 0, uintptr(unsafe.Pointer(&iidResource)), uintptr(unsafe.Pointer(&d.out)))
		if err = hresult(r, fmt.Sprintf("buffer of %d bytes", size)); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	res := d.out
	d.out = 0
	va := com(&d.slots, res, resourceGetGPUVirtualAddress)
	if kind == hostgpu.BufferWorking {
		return &buffer{res: res, va: va, size: size, kind: kind}, nil
	}
	d.mapped = nil
	if err := hresult(com(&d.slots, res, resourceMap, 0, 0, uintptr(unsafe.Pointer(&d.mapped))), "map"); err != nil {
		release(&d.slots, &res)
		return nil, err
	}
	return &buffer{res: res, va: va, size: size, kind: kind, data: unsafe.Slice((*byte)(d.mapped), size)}, nil
}

// Bytes is an upload, readback, or output buffer's memory, valid until
// Release, and nil for a working buffer.
func (b *buffer) Bytes() []byte { return b.data }

// Size is the buffer's byte size.
func (b *buffer) Size() int { return b.size }

// Kind is the kind the buffer was allocated as.
func (b *buffer) Kind() hostgpu.BufferKind { return b.kind }

// Release unmaps a mapped buffer and frees the buffer. A buffer
// already released is left alone.
func (b *buffer) Release() {
	if b.res == 0 {
		return
	}
	var slots [argSlots]uintptr
	if b.data != nil {
		com(&slots, b.res, resourceUnmap, 0, 0)
	}
	release(&slots, &b.res)
	b.data = nil
}

// Close waits for the submissions in flight and releases them, the
// pool, the pipelines, the fence, the event, the queue, the device,
// the adapter, and the factory. An encoder still open is abandoned:
// its list was never executed, so its slot joins the pool released.
// A wait that fails leaves its submissions' slots unreleased, since
// the GPU may still be executing their lists.
func (d *device) Close() {
	if s := d.inflight.Newest(); s != 0 {
		d.Wait(s)
	}
	d.inflight = hostgpu.Ledger[submission]{}
	if c := d.enc; c.open {
		d.free = append(d.free, c.slot)
		c.slot, c.open = slot{}, false
	}
	for i := range d.free {
		release(&d.slots, &d.free[i].list)
		release(&d.slots, &d.free[i].allocator)
	}
	d.free = nil
	for _, p := range d.pipes {
		release(&d.slots, &p.state)
		release(&d.slots, &p.rootSig)
	}
	d.pipes, d.fill = nil, nil
	release(&d.slots, &d.fence)
	if d.event != 0 {
		windows.CloseHandle(d.event)
		d.event = 0
	}
	release(&d.slots, &d.queue)
	release(&d.slots, &d.dev)
	release(&d.slots, &d.adapter)
	release(&d.slots, &d.factory)
}

// commands is the device's encoder: the slot being recorded, open
// from Begin to Commit. Commit hands the slot to the in-flight list
// under its sequence number and leaves the encoder closed for the
// next Begin, so no handle to a submission outlives it: a Submission
// is a number, and the encoder refuses encoding while closed.
//
// The argument array, the root constant block, the barriers, and the
// slot's list are memory that stays put, whose addresses the calls
// take.
type commands struct {
	dev   *device
	open  bool
	err   error // the first host error latched on the encoder (fail)
	slot  slot
	slots [argSlots]uintptr
	push  [hlsl.RootDwords]uint32 // the record, the row thread count, the thread base, the lengths
	bar   resourceBarrier
	trans [hlsl.RootDwords / 2]resourceBarrier // the transitions one command records, a root descriptor per buffer at most
	fill  [hlsl.FillConstants]uint32           // the fill kernel's root constants
	epoch uint64                               // the command list being recorded, one per Begin
}

// transition adds the transition of b into state to the pending ones
// and records b's state for the list, unless b is in it already. A
// buffer in the common state promotes to any state on its first
// access without a transition (Implicit State Transitions, Common
// state promotion), so that access records its state alone.
func (c *commands) transition(b *buffer, state uint32, n int) int {
	from := uint32(resourceStateCommon)
	if b.epoch == c.epoch {
		from = b.state
	}
	b.state, b.epoch = state, c.epoch
	if from == state || from == resourceStateCommon {
		return n
	}
	c.trans[n] = resourceBarrier{typ: barrierTypeTransition, resource: b.res, subresource: barrierAllSubresources, before: from, after: state}
	return n + 1
}

// flushTransitions records the n pending transitions.
func (c *commands) flushTransitions(n int) {
	if n > 0 {
		com(&c.slots, c.slot.list, listResourceBarrier, uintptr(n), uintptr(unsafe.Pointer(&c.trans[0])))
	}
}

// Begin opens the encoder on a slot from the pool, or a new one. An
// encoder still open from an earlier Begin is an error.
func (d *device) Begin() (hostgpu.Commands, error) {
	c := d.enc
	if c.open {
		return nil, errors.New("a submission is already being encoded")
	}
	if err := c.err; err != nil {
		c.err = nil
		return nil, err
	}
	s, err := d.takeSlot()
	if err != nil {
		return nil, err
	}
	if err := hresult(com(&c.slots, s.allocator, allocatorReset), "allocator reset"); err != nil {
		d.free = append(d.free, s)
		return nil, err
	}
	if err := hresult(com(&c.slots, s.list, listReset, s.allocator, 0), "command list reset"); err != nil {
		d.free = append(d.free, s)
		return nil, err
	}
	c.slot, c.open = s, true
	c.epoch++
	return c, nil
}

// takeSlot pops the free list, or creates an allocator and a command
// list of the compute type, closed so Begin resets it like any other.
func (d *device) takeSlot() (slot, error) {
	if n := len(d.free); n > 0 {
		s := d.free[n-1]
		d.free = d.free[:n-1]
		return s, nil
	}
	s := &d.newSlot
	*s = slot{}
	r := com(&d.slots, d.dev, deviceCreateCommandAllocator, commandListTypeCompute, uintptr(unsafe.Pointer(&iidAllocator)), uintptr(unsafe.Pointer(&s.allocator)))
	if err := hresult(r, "command allocator"); err != nil {
		return slot{}, err
	}
	r = com(&d.slots, d.dev, deviceCreateCommandList, 0, commandListTypeCompute, s.allocator, 0, uintptr(unsafe.Pointer(&iidCommandList)), uintptr(unsafe.Pointer(&s.list)))
	if err := hresult(r, "command list"); err != nil {
		release(&d.slots, &s.allocator)
		return slot{}, err
	}
	if err := hresult(com(&d.slots, s.list, listClose), "command list close"); err != nil {
		release(&d.slots, &s.list)
		release(&d.slots, &s.allocator)
		return slot{}, err
	}
	return *s, nil
}

// fail latches the first host error. The commands after it are not
// encoded (encoding), and Commit carries it onto the submission.
func (c *commands) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

// encoding reports whether the next command is encoded: the encoder
// is open and no host error is latched. A command on a closed
// encoder latches ErrClosed, which the next Begin reports.
func (c *commands) encoding() bool {
	if !c.open {
		c.fail(hostgpu.ErrClosed)
		return false
	}
	return c.err == nil
}

// Dispatch encodes one entry point: the buffers as root descriptors
// at parameters 0 to n-1 in order, the record, the group thread
// count, the thread base of a split piece, and the word counts as
// root constants at parameter n, and workgroups of the pipeline's
// size covering the grid rounded up.
// The emitted wrapper guards the overshoot. A buffer count other
// than the entry's root signature declares is a host error
// (CheckBindings), as is a group grid wider than one dispatch or a
// grid with more rows than one dispatch issues; a refused dispatch
// records nothing.
func (c *commands) Dispatch(p hostgpu.Pipeline, buffers []hostgpu.Buffer, rec hostgpu.Dispatch, grid [2]int) {
	if !c.encoding() {
		return
	}
	pipe, ok := p.(*pipeline)
	if !ok {
		c.fail(hostgpu.ErrForeign)
		return
	}
	if err := hostgpu.CheckBindings(buffers, pipe.layout.Buffers, pipe.layout.Dynamic); err != nil {
		c.fail(err)
		return
	}
	groups, err := hostgpu.Groups(grid, pipe.workgroup)
	if err != nil {
		c.fail(err)
		return
	}
	gx, gy := groups[0], groups[1]
	// Rows are never split, so a grid with more rows than one
	// dispatch issues is refused.
	if gy > dispatchMaxGroups {
		c.fail(hostgpu.ErrGridRows)
		return
	}
	// The group shell adds no thread base, so its grid is never split.
	if pipe.layout.Group && gx > dispatchMaxGroups {
		c.fail(hostgpu.ErrGroupWide)
		return
	}
	for _, b := range buffers {
		if _, ok := b.(*buffer); !ok {
			c.fail(hostgpu.ErrForeign)
			return
		}
	}
	l := c.slot.list
	pending := 0
	for _, b := range buffers {
		pending = c.transition(b.(*buffer), resourceStateUAV, pending)
	}
	c.flushTransitions(pending)
	com(&c.slots, l, listSetPipelineState, pipe.state)
	com(&c.slots, l, listSetComputeRootSignature, pipe.rootSig)
	for i, b := range buffers {
		vb := b.(*buffer)
		com(&c.slots, l, listSetComputeRootUnorderedAccessView, uintptr(i), vb.va)
	}
	if pipe.layout.Lengths {
		hostgpu.Lengths(c.push[hlsl.FixedWords:], buffers)
	}
	c.push[0], c.push[1], c.push[2], c.push[3], c.push[4] = rec.BoundX, rec.BoundY, rec.ParamsOffset, rec.ParamsStride, rec.Segment
	c.push[hlsl.RowThreadsWord] = 0
	if pipe.layout.Group {
		// The row's thread count, which the shell has no built-in for.
		c.push[hlsl.RowThreadsWord] = pipe.workgroup[0] * gx
	}
	// An index or span grid wider than the API's thread group limit is
	// issued in pieces, the thread base word carrying each piece's
	// first thread.
	for base := uint32(0); ; {
		n := min(gx-base, dispatchMaxGroups)
		c.push[hlsl.ThreadBaseWord] = base * pipe.workgroup[0]
		com(&c.slots, l, listSetComputeRoot32BitConstants, uintptr(pipe.layout.Buffers), uintptr(pipe.layout.Constants), uintptr(unsafe.Pointer(&c.push[0])), 0)
		com(&c.slots, l, listDispatch, uintptr(n), uintptr(gy), 1)
		base += n
		if base >= gx {
			break
		}
	}
}

// Copy records CopyBufferRegion with the source in copy source and the
// destination in copy dest, by a transition or by promotion from the
// common state (transition). A buffer promoted to
// unordered access stays there until the list completes, and a UAV
// barrier does not change a state, so the next dispatch that binds
// either transitions it back. Under legacy barriers every copy runs
// synchronously, with an implicit sync and flush after it (D3D12
// Enhanced Barriers, Synchronous Copy, Discard and Resolve), so copies
// into one buffer need no barrier between them.
func (c *commands) Copy(dst hostgpu.Buffer, dstOffset int, src hostgpu.Buffer, srcOffset int, n int) {
	if !c.encoding() {
		return
	}
	if err := hostgpu.CheckCopy(dst, dstOffset, src, srcOffset, n); err != nil {
		c.fail(err)
		return
	}
	d, okd := dst.(*buffer)
	s, oks := src.(*buffer)
	if !okd || !oks {
		c.fail(hostgpu.ErrForeign)
		return
	}
	if n == 0 {
		return
	}
	c.flushTransitions(c.transition(d, resourceStateCopyDest, c.transition(s, resourceStateCopySource, 0)))
	com(&c.slots, c.slot.list, listCopyBufferRegion, d.res, uintptr(dstOffset), s.res, uintptr(srcOffset), uintptr(n))
}

// Fill dispatches the backend's fill kernel (hlsl.FillSource): the
// buffer bound as the root UAV at the fill's first byte, one thread
// per word, in pieces of at most the API's thread group limit, each
// with its first word. The buffer is in unordered access for it, as
// for any dispatch.
func (c *commands) Fill(dst hostgpu.Buffer, offset int, n int, value uint32) {
	if !c.encoding() {
		return
	}
	if err := hostgpu.CheckFill(dst, offset, n); err != nil {
		c.fail(err)
		return
	}
	b, ok := dst.(*buffer)
	if !ok {
		c.fail(hostgpu.ErrForeign)
		return
	}
	pipe := c.dev.fill
	words := uint32(n / 4)
	groups, err := hostgpu.Groups([2]int{int(words), 1}, pipe.workgroup)
	if err != nil {
		c.fail(err)
		return
	}
	if n == 0 {
		return
	}
	c.flushTransitions(c.transition(b, resourceStateUAV, 0))
	l := c.slot.list
	com(&c.slots, l, listSetPipelineState, pipe.state)
	com(&c.slots, l, listSetComputeRootSignature, pipe.rootSig)
	com(&c.slots, l, listSetComputeRootUnorderedAccessView, 0, b.va+uintptr(offset))
	c.fill[hlsl.FillCountWord], c.fill[hlsl.FillValueWord] = words, value
	for first := uint32(0); first < groups[0]; {
		g := min(groups[0]-first, dispatchMaxGroups)
		c.fill[hlsl.FillBaseWord] = first * pipe.workgroup[0]
		com(&c.slots, l, listSetComputeRoot32BitConstants, uintptr(pipe.layout.Buffers), uintptr(pipe.layout.Constants), uintptr(unsafe.Pointer(&c.fill[0])), 0)
		com(&c.slots, l, listDispatch, uintptr(g), 1, 1)
		first += g
	}
}

// Barrier orders the unordered accesses of the dispatches encoded
// before it ahead of those encoded after it, on every buffer.
func (c *commands) Barrier() {
	if !c.encoding() {
		return
	}
	c.bar = resourceBarrier{typ: barrierTypeUAV}
	com(&c.slots, c.slot.list, listResourceBarrier, 1, uintptr(unsafe.Pointer(&c.bar)))
}

// Commit closes the command list, executes it, and signals the fence
// with the submission's number, which joins the device's in-flight
// list, and closes the encoder. A list whose close failed is not
// executed, but the fence is still signaled through the queue, which
// orders the signal after every earlier submission's, so Wait
// releases the submission in turn and reports the error kept on it.
// Only when the queue refuses the signal is the fence signaled from
// the CPU: that signal is unordered against the queue's, which a
// removed device makes moot, since its fence is parked at the
// maximum and Wait reports the removal. A host error latched while
// encoding rides on the submission the same way, joined with any
// close or signal error: the commands recorded before it are valid
// and run. A Commit on a closed encoder latches ErrClosed for the
// next Begin and returns the submission that names none.
func (c *commands) Commit() hostgpu.Submission {
	if !c.open {
		c.fail(hostgpu.ErrClosed)
		return 0
	}
	d := c.dev
	seq := d.inflight.Next()
	err := hresult(com(&c.slots, c.slot.list, listClose), "command list close")
	if err == nil {
		com(&c.slots, d.queue, queueExecuteCommandLists, 1, uintptr(unsafe.Pointer(&c.slot.list)))
	}
	if sigErr := hresult(com(&c.slots, d.queue, queueSignal, d.fence, uintptr(seq)), "queue signal"); sigErr != nil {
		err = errors.Join(err, sigErr)
		com(&c.slots, d.fence, fenceSignal, uintptr(seq))
	}
	switch {
	case c.err == nil:
	case err == nil:
		err = c.err
	default:
		err = errors.Join(c.err, err)
	}
	d.inflight.Commit(submission{slot: c.slot, err: err})
	c.slot, c.open, c.err = slot{}, false, nil
	return seq
}

// Wait blocks until the submission completes, then releases it and
// every submission committed before it, since the queue completes
// them in order, reporting the errors of all of them and a removed
// device. A removed device raises the fence's completed value to the
// maximum and signals the event, so the wait returns with the
// removal reason attached. A submission already released by an
// earlier Wait reports nothing. A sequence number no Commit has
// returned is an error.
func (d *device) Wait(s hostgpu.Submission) error {
	subs, target, err := d.inflight.Pending(s)
	if err != nil || len(subs) == 0 {
		return err
	}
	if com(&d.slots, d.fence, fenceGetCompletedValue) < uintptr(target) {
		if err := hresult(com(&d.slots, d.fence, fenceSetEventOnCompletion, uintptr(target), uintptr(d.event)), "fence event"); err != nil {
			return err
		}
		if _, err := windows.WaitForSingleObject(d.event, windows.INFINITE); err != nil {
			return fmt.Errorf("fence wait: %w", err)
		}
	}
	for _, sub := range subs {
		err = errors.Join(err, sub.err)
		d.free = append(d.free, sub.slot)
	}
	d.inflight.Release(len(subs))
	if r := com(&d.slots, d.dev, deviceGetDeviceRemovedReason); int32(r) < 0 {
		err = errors.Join(err, hresult(r, "device removed"))
	}
	return err
}
