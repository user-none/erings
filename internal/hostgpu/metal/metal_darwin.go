// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package metal

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/compile"
	"github.com/user-none/erings/internal/hostgpu/compile/msl"
)

// Metal constants used here (Metal framework headers).
const (
	resourceStorageModeShared  = 0    // MTLResourceStorageModeShared
	resourceStorageModePrivate = 0x20 // MTLResourceStorageModePrivate: MTLStorageModePrivate (2) << MTLResourceStorageModeShift (4)
	commandBufferStatusError   = 5    // MTLCommandBufferStatusError
	dispatchTypeConcurrent     = 1    // MTLDispatchTypeConcurrent
	barrierScopeBuffers        = 1    // MTLBarrierScopeBuffers
)

// mtlSize is MTLSize: three NSUInteger.
type mtlSize struct {
	width, height, depth uintptr
}

var (
	loadOnce sync.Once
	loadErr  error

	createSystemDefaultDevice uintptr
	msgSend                   uintptr // objc_msgSend
	poolPush                  uintptr // objc_autoreleasePoolPush
	poolPop                   uintptr // objc_autoreleasePoolPop

	selAlloc                   = objc.RegisterName("alloc")
	selInit                    = objc.RegisterName("init")
	selRelease                 = objc.RegisterName("release")
	selRetain                  = objc.RegisterName("retain")
	selName                    = objc.RegisterName("name")
	selUTF8String              = objc.RegisterName("UTF8String")
	selInitWithUTF8String      = objc.RegisterName("initWithUTF8String:")
	selLocalizedDescription    = objc.RegisterName("localizedDescription")
	selNewCommandQueue         = objc.RegisterName("newCommandQueue")
	selSetFastMathEnabled      = objc.RegisterName("setFastMathEnabled:")
	selNewLibraryWithSource    = objc.RegisterName("newLibraryWithSource:options:error:")
	selNewFunctionWithName     = objc.RegisterName("newFunctionWithName:")
	selNewComputePipelineState = objc.RegisterName("newComputePipelineStateWithFunction:error:")
	selNewBufferWithLength     = objc.RegisterName("newBufferWithLength:options:")
	selContents                = objc.RegisterName("contents")
	selCommandBuffer           = objc.RegisterName("commandBuffer")
	selComputeCommandEncoder   = objc.RegisterName("computeCommandEncoderWithDispatchType:")
	selBlitCommandEncoder      = objc.RegisterName("blitCommandEncoder")
	selCopyFromBuffer          = objc.RegisterName("copyFromBuffer:sourceOffset:toBuffer:destinationOffset:size:")
	selMemoryBarrier           = objc.RegisterName("memoryBarrierWithScope:")
	selSetComputePipelineState = objc.RegisterName("setComputePipelineState:")
	selSetBuffer               = objc.RegisterName("setBuffer:offset:atIndex:")
	selSetBytes                = objc.RegisterName("setBytes:length:atIndex:")
	selDispatchThreadgroups    = objc.RegisterName("dispatchThreadgroups:threadsPerThreadgroup:")
	selMaxTotalThreads         = objc.RegisterName("maxTotalThreadsPerThreadgroup")
	selEndEncoding             = objc.RegisterName("endEncoding")
	selCommit                  = objc.RegisterName("commit")
	selWaitUntilCompleted      = objc.RegisterName("waitUntilCompleted")
	selStatus                  = objc.RegisterName("status")
	selError                   = objc.RegisterName("error")
)

// load opens Metal and Foundation and resolves
// MTLCreateSystemDefaultDevice and the Objective-C runtime functions
// the frame path calls directly, once.
func load() error {
	loadOnce.Do(func() {
		metal, err := purego.Dlopen("/System/Library/Frameworks/Metal.framework/Metal", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
			loadErr = err
			return
		}
		createSystemDefaultDevice, err = purego.Dlsym(metal, "MTLCreateSystemDefaultDevice")
		if err != nil {
			loadErr = err
			return
		}
		libobjc, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		for _, s := range []struct {
			dst  *uintptr
			name string
		}{{&msgSend, "objc_msgSend"}, {&poolPush, "objc_autoreleasePoolPush"}, {&poolPop, "objc_autoreleasePoolPop"}} {
			if *s.dst, err = purego.Dlsym(libobjc, s.name); err != nil {
				loadErr = err
				return
			}
		}
	})
	return loadErr
}

// nsString makes an NSString the caller releases.
func nsString(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(selAlloc).Send(selInitWithUTF8String, s)
}

// goString reads an NSString.
func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	return objc.Send[string](s, selUTF8String)
}

// errorText reads an NSError's description.
func errorText(err objc.ID, what string) error {
	if err == 0 {
		return errors.New(what)
	}
	return errors.New(what + ": " + goString(err.Send(selLocalizedDescription)))
}

// device is the system default Metal device, one command queue, the
// pipelines Build made, the one encoder Begin hands out,
// and the submissions committed and not yet waited on, in commit
// order. The encoder and the in-flight list are reused across
// frames, so the frame path allocates nothing once the first frame
// has run.
type device struct {
	dev      objc.ID
	queue    objc.ID
	pipes    []*pipeline
	enc      *commands
	fill     *pipeline                  // the backend's fill kernel (buildFill)
	inflight hostgpu.Ledger[submission] // the committed command buffers
	args     [8]uintptr                 // Wait's argument array, apart from the encoder's
}

// submission is a committed command buffer with the error its
// encoding met, if any: a host error, or an encoder Metal could not
// open mid-way, after either of which the rest of the submission was
// not encoded.
type submission struct {
	cb  objc.ID
	err error
}

// Warm opens the default device and closes it, so the frameworks are
// loaded and the fill kernel compiled once before the first Open the
// renderer makes. A failure is returned; Open reports the same
// failure when it is made.
func Warm() error {
	d, err := Open()
	if err != nil {
		return err
	}
	d.Close()
	return nil
}

// Open opens the default device. A machine without Metal, a device,
// or a command queue is ErrUnavailable.
func Open() (hostgpu.Device, error) {
	if err := load(); err != nil {
		return nil, fmt.Errorf("%w: %v", hostgpu.ErrUnavailable, err)
	}
	var slots [8]uintptr
	pool := pushPool()
	defer popPool(&slots, pool)
	dev, _, _ := purego.SyscallN(createSystemDefaultDevice)
	if dev == 0 {
		return nil, fmt.Errorf("%w: no default device", hostgpu.ErrUnavailable)
	}
	queue := objc.ID(dev).Send(selNewCommandQueue)
	if queue == 0 {
		objc.ID(dev).Send(selRelease)
		return nil, fmt.Errorf("%w: no command queue", hostgpu.ErrUnavailable)
	}
	d := &device{dev: objc.ID(dev), queue: queue}
	d.enc = &commands{dev: d}
	if err := d.buildFill(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// fillSource is the backend's fill kernel: Metal's buffer fill takes
// one byte, so a word value whose bytes differ is written by a kernel.
// The buffer is bound at the fill's first byte, and thread i writes
// word i of the count.
const fillSource = `#include <metal_stdlib>
using namespace metal;
struct FillArgs { uint count; uint value; };
kernel void fill(device uint* b [[buffer(0)]], constant FillArgs& f [[buffer(1)]], uint gid [[thread_position_in_grid]]) {
	if (gid < f.count) {
		b[gid] = f.value;
	}
}
`

// fillWorkgroup is the fill kernel's threadgroup width.
const fillWorkgroup = 64

// buildFill compiles the fill kernel and builds its pipeline.
func (d *device) buildFill() error {
	lib, err := d.compile(fillSource)
	if err != nil {
		return err
	}
	defer lib.Send(selRelease)
	d.fill, err = d.pipeline(lib, "fill", [2]int{fillWorkgroup, 1}, 1)
	return err
}

// Name is the device's name.
func (d *device) Name() string {
	pool := pushPool()
	defer popPool(&d.args, pool)
	return goString(d.dev.Send(selName))
}

// groupWidest is the widest group row: Metal issues a grid of any
// width in one dispatch, so the bound is a row's thread index, which
// a group entry may take as an int32 coordinate.
const groupWidest = 1<<31 - msl.GroupWorkgroup

// GroupThreads is hostgpu.GroupWidth with no lane count, which Metal
// does not report.
func (d *device) GroupThreads(load hostgpu.GroupLoad) int {
	return hostgpu.GroupWidth(load, 0, msl.GroupWorkgroup, groupWidest)
}

// Build emits the lowered module as MSL, compiles it, and builds one
// pipeline per entry point in the module's entry order, each with
// the entry's workgroup.
func (d *device) Build(mod *compile.Module) ([]hostgpu.Pipeline, error) {
	out, err := msl.Emit(mod)
	if err != nil {
		return nil, err
	}
	pool := pushPool()
	defer popPool(&d.args, pool)
	lib, err := d.compile(out.Source)
	if err != nil {
		return nil, err
	}
	defer lib.Send(selRelease)
	pipes := make([]hostgpu.Pipeline, 0, len(out.Entries))
	for _, en := range out.Entries {
		p, err := d.pipeline(lib, en.Name, en.Workgroup, en.DispatchBinding)
		if err != nil {
			return nil, err
		}
		p.bufferLengths = en.BufferLengths
		p.dynamic = compile.DynamicBindings(en.Buffers)
		pipes = append(pipes, p)
	}
	return pipes, nil
}

// compile compiles MSL source with fast math disabled and returns the
// library, which the caller releases once its pipelines are built: a
// pipeline state does not depend on its library or function once
// created. A compile failure returns the compiler's message.
func (d *device) compile(source string) (objc.ID, error) {
	src := nsString(source)
	defer src.Send(selRelease)
	opts := objc.ID(objc.GetClass("MTLCompileOptions")).Send(selAlloc).Send(selInit)
	defer opts.Send(selRelease)
	opts.Send(selSetFastMathEnabled, false)
	var nserr objc.ID
	lib := d.dev.Send(selNewLibraryWithSource, src, opts, unsafe.Pointer(&nserr))
	if lib == 0 {
		return 0, errorText(nserr, "compile")
	}
	return lib, nil
}

// pipeline is a compute pipeline for one entry point, the kernel
// function named for it in the library, with its workgroup size.
type pipeline struct {
	state         objc.ID
	workgroup     mtlSize
	dispatch      int // the argument table index of the dispatch record, after the buffers
	bufferLengths bool
	dynamic       uint32 // the variable-length bindings, by bit
}

// pipeline builds the compute pipeline for the named kernel function,
// whose dispatch record is bound at index dispatch, dispatched in
// threadgroups of its workgroup fitted to the pipeline's threadgroup
// limit (fitWorkgroup), and keeps it for Close.
func (d *device) pipeline(lib objc.ID, name string, workgroup [2]int, dispatch int) (*pipeline, error) {
	n := nsString(name)
	defer n.Send(selRelease)
	fn := lib.Send(selNewFunctionWithName, n)
	if fn == 0 {
		return nil, errors.New("no kernel function " + name)
	}
	defer fn.Send(selRelease)
	var nserr objc.ID
	state := d.dev.Send(selNewComputePipelineState, fn, unsafe.Pointer(&nserr))
	if state == 0 {
		return nil, errorText(nserr, "pipeline "+name)
	}
	p := &pipeline{state: state, workgroup: fitWorkgroup(workgroup, int(state.Send(selMaxTotalThreads))), dispatch: dispatch}
	d.pipes = append(d.pipes, p)
	return p, nil
}

// fitWorkgroup is the threadgroup a pipeline is dispatched with: the
// emitter's workgroup, or, when the pipeline's limit, which Metal
// lowers for a kernel's register use, is below it, the largest power
// of two at or below the limit, keeping a two-dimensional workgroup's
// width while that fits. Every shell takes its coordinates and a
// group's thread count from the dispatch, so any size runs the same
// kernel; a power of two at or below a group entry's width divides the
// rows GroupThreads sizes in multiples of that width, so no row ends
// mid-threadgroup.
func fitWorkgroup(workgroup [2]int, limit int) mtlSize {
	w, h := workgroup[0], workgroup[1]
	if w*h > limit {
		t := 1
		for t*2 <= limit {
			t *= 2
		}
		if h > 1 && w <= t {
			h = t / w
		} else {
			w, h = t, 1
		}
	}
	return mtlSize{width: uintptr(w), height: uintptr(h), depth: 1}
}

// buffer is an upload, readback, or output buffer in shared storage,
// whose bytes the CPU reads and writes directly while the GPU sees
// the same memory, or a working buffer in private storage with no CPU
// mapping and no data.
type buffer struct {
	buf  objc.ID
	size int
	kind hostgpu.BufferKind
	data []byte
}

// NewBuffer allocates a buffer of size bytes by kind: an upload, a
// readback, or an output buffer in shared storage with its contents
// mapped, since the device shares the CPU's memory and a dispatch's
// writes to an output buffer are the bytes the CPU reads, and a
// working buffer in private storage.
func (d *device) NewBuffer(size int, kind hostgpu.BufferKind) (hostgpu.Buffer, error) {
	mode := resourceStorageModeShared
	if kind == hostgpu.BufferWorking {
		mode = resourceStorageModePrivate
	}
	pool := pushPool()
	defer popPool(&d.args, pool)
	buf := d.dev.Send(selNewBufferWithLength, uintptr(size), uintptr(mode))
	if buf == 0 {
		return nil, fmt.Errorf("buffer of %d bytes", size)
	}
	if kind == hostgpu.BufferWorking {
		return &buffer{buf: buf, size: size, kind: kind}, nil
	}
	p := objc.Send[unsafe.Pointer](buf, selContents)
	if p == nil {
		buf.Send(selRelease)
		return nil, errors.New("buffer has no CPU mapping")
	}
	return &buffer{buf: buf, size: size, kind: kind, data: unsafe.Slice((*byte)(p), size)}, nil
}

// Bytes is an upload, readback, or output buffer's memory, valid until
// Release, and nil for a working buffer.
func (b *buffer) Bytes() []byte { return b.data }

// Size is the buffer's byte size.
func (b *buffer) Size() int { return b.size }

// Kind is the kind the buffer was allocated as.
func (b *buffer) Kind() hostgpu.BufferKind { return b.kind }

// Release frees the buffer.
func (b *buffer) Release() {
	var slots [8]uintptr
	pool := pushPool()
	defer popPool(&slots, pool)
	b.buf.Send(selRelease)
	b.buf, b.data = 0, nil
}

// Close waits for the submissions in flight and releases them, the
// pipelines, the queue, and the device. An encoder still open is
// abandoned: its pass encoder is ended and its command buffer, never
// committed, released. The autorelease pool and the OS thread pin
// Begin took belong to Begin's goroutine, which Close may not be on,
// so they are left, and Close runs in a pool of its own.
func (d *device) Close() {
	pool := pushPool()
	defer popPool(&d.args, pool)
	if s := d.inflight.Newest(); s != 0 {
		d.Wait(s)
	}
	d.inflight = hostgpu.Ledger[submission]{}
	if c := d.enc; c.open {
		if c.enc != 0 {
			c.call(c.enc, selEndEncoding)
			c.enc = 0
		}
		if c.blit != 0 {
			c.call(c.blit, selEndEncoding)
			c.blit = 0
		}
		c.call(c.cb, selRelease)
		c.cb, c.err, c.open = 0, nil, false
	}
	for _, p := range d.pipes {
		p.state.Send(selRelease)
		p.state = 0
	}
	d.pipes, d.fill = nil, nil
	d.queue.Send(selRelease)
	d.dev.Send(selRelease)
	d.queue, d.dev = 0, 0
}

// commands is the device's encoder: the command buffer being encoded
// and the one pass encoder open on it, compute or blit, from Begin to
// Commit. Dispatches and fills go on a compute encoder and copies on
// a blit encoder, each opened when the other kind of command follows,
// so a run of copies shares one blit encoder. The buffers are
// allocated with Metal's hazard tracking, which orders the passes of
// one command buffer, and the command buffers of the queue, on the
// buffers they share (MTLHazardTrackingMode: tracked, the default for
// a buffer, on an MTLCommandQueue). The goroutine is pinned to its OS
// thread and an autorelease pool is open for the same window
// (pushPool). Commit hands the command buffer to the in-flight list
// and leaves the encoder closed for the next Begin. A command while
// closed latches ErrClosed, which the next Begin reports (encoding).
//
// The argument array and the values passed to Metal by pointer are
// heap memory that stays put across the calls.
type commands struct {
	dev  *device
	open bool
	cb   objc.ID
	enc  objc.ID // the compute encoder, or zero
	blit objc.ID // the blit encoder, or zero
	err  error   // the first host error, or an encoder that could not be opened, latched on the encoder (fail)
	pool uintptr

	args    [8]uintptr          // receiver, selector, and the message's arguments
	record  hostgpu.Dispatch    // the dispatch record setBytes copies
	fill    [2]uint32           // the fill kernel's word count and value
	lengths [bufferSlots]uint32 // bound word counts, copied only for variable buffers
	groups  mtlSize
	threads mtlSize
}

// call sends a message with scalar arguments through objc_msgSend on
// the encoder's argument array.
func (c *commands) call(recv objc.ID, sel objc.SEL, args ...uintptr) uintptr {
	return send(&c.args, recv, sel, args...)
}

// send sends a message with scalar arguments through objc_msgSend,
// the receiver and selector in the first two slots of the argument
// array, which is reused across calls so the call allocates nothing.
// The array holds the receiver, the selector, and the most arguments
// any message here passes.
func send(slots *[8]uintptr, recv objc.ID, sel objc.SEL, args ...uintptr) uintptr {
	slots[0], slots[1] = uintptr(recv), uintptr(sel)
	n := copy(slots[2:], args)
	r, _, _ := purego.SyscallN(msgSend, slots[:2+n]...)
	return r
}

// Begin opens the encoder on a new command buffer, with no pass open:
// the first command opens its own (computePass, blitPass). An encoder
// still open from an earlier Begin is an error.
func (d *device) Begin() (hostgpu.Commands, error) {
	c := d.enc
	if c.open {
		return nil, errors.New("a submission is already being encoded")
	}
	if err := c.err; err != nil {
		c.err = nil
		return nil, err
	}
	c.pool = pushPool()
	c.cb = objc.ID(c.call(d.queue, selCommandBuffer))
	if c.cb == 0 {
		popPool(&c.args, c.pool)
		return nil, errors.New("no command buffer")
	}
	// The command buffer outlives the pool: it is released when its
	// submission is waited on.
	c.call(c.cb, selRetain)
	c.enc, c.blit = 0, 0
	c.open = true
	return c, nil
}

// fail latches the first error of the encoding, a host error or an
// encoder that could not be opened. The commands after it are not
// encoded (encoding, computePass, blitPass), and Commit carries it
// onto the submission.
func (c *commands) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

// computePass makes the open pass a compute encoder: it ends a blit
// encoder and opens a compute encoder when none is open. It reports
// false once an error is latched, which ends the submission's
// encoding with the error Wait reports.
func (c *commands) computePass() bool {
	if c.err != nil {
		return false
	}
	if c.blit != 0 {
		c.call(c.blit, selEndEncoding)
		c.blit = 0
	}
	if c.enc == 0 {
		c.enc = objc.ID(c.call(c.cb, selComputeCommandEncoder, dispatchTypeConcurrent))
		if c.enc == 0 {
			c.fail(errors.New("no compute encoder"))
			return false
		}
	}
	return true
}

// blitPass makes the open pass a blit encoder, as computePass does a
// compute encoder.
func (c *commands) blitPass() bool {
	if c.err != nil {
		return false
	}
	if c.enc != 0 {
		c.call(c.enc, selEndEncoding)
		c.enc = 0
	}
	if c.blit == 0 {
		c.blit = objc.ID(c.call(c.cb, selBlitCommandEncoder))
		if c.blit == 0 {
			c.fail(errors.New("no blit encoder"))
			return false
		}
	}
	return true
}

// encoding reports whether the next command is encoded: the encoder
// is open and no error is latched. A command on a closed encoder
// latches ErrClosed, which the next Begin reports.
func (c *commands) encoding() bool {
	if !c.open {
		c.fail(hostgpu.ErrClosed)
		return false
	}
	return c.err == nil
}

// pushPool pins the goroutine to its OS thread and pushes an
// autorelease pool. Every call into Metal runs inside one: an object
// autoreleased with no pool in place is never freed, and a pool is
// popped on the thread that pushed it.
func pushPool() uintptr {
	runtime.LockOSThread()
	p, _, _ := purego.SyscallN(poolPush)
	return p
}

// popPool pops a pool pushPool pushed, through the argument array
// slots, and unpins the goroutine.
func popPool(slots *[8]uintptr, pool uintptr) {
	slots[0] = pool
	purego.SyscallN(poolPop, slots[:1]...)
	runtime.UnlockOSThread()
}

// bufferSlots is the buffer argument table's size: a compute function
// binds at indices 0 to 30.
const bufferSlots = 31

// Dispatch encodes one kernel: buffers bound at indices 0 to n-1 in
// order, the 20-byte dispatch record at index n, the emitter's
// dispatch binding, the word counts at index n+1 when the pipeline
// has variable-length buffers, and threadgroups of the pipeline's
// size covering the grid rounded up. The emitted wrapper guards the
// overshoot. A buffer count other than n is a host error
// (CheckBindings); a refused dispatch encodes nothing. The bindings
// stay inside the argument table (bufferSlots), since the Metal
// compiler rejects a kernel binding past it at Build.
func (c *commands) Dispatch(p hostgpu.Pipeline, buffers []hostgpu.Buffer, rec hostgpu.Dispatch, grid [2]int) {
	if !c.encoding() {
		return
	}
	pipe, ok := p.(*pipeline)
	if !ok {
		c.fail(hostgpu.ErrForeign)
		return
	}
	if err := hostgpu.CheckBindings(buffers, pipe.dispatch, pipe.dynamic); err != nil {
		c.fail(err)
		return
	}
	groups, err := hostgpu.Groups(grid, [2]uint32{uint32(pipe.workgroup.width), uint32(pipe.workgroup.height)})
	if err != nil {
		c.fail(err)
		return
	}
	for _, b := range buffers {
		if _, ok := b.(*buffer); !ok {
			c.fail(hostgpu.ErrForeign)
			return
		}
	}
	if !c.computePass() {
		return
	}
	c.call(c.enc, selSetComputePipelineState, uintptr(pipe.state))
	for i, b := range buffers {
		c.call(c.enc, selSetBuffer, uintptr(b.(*buffer).buf), 0, uintptr(i))
	}
	c.record = rec
	c.call(c.enc, selSetBytes, uintptr(unsafe.Pointer(&c.record)), unsafe.Sizeof(c.record), uintptr(pipe.dispatch))
	if pipe.bufferLengths {
		hostgpu.Lengths(c.lengths[:], buffers)
		c.call(c.enc, selSetBytes, uintptr(unsafe.Pointer(&c.lengths)), uintptr(len(buffers))*4, uintptr(pipe.dispatch+1))
	}
	c.dispatch(pipe, groups)
}

// dispatch issues the bound pipeline over the groups, in threadgroups
// of the pipeline's workgroup. MTLSize is 24 bytes, and the arm64
// procedure call standard passes a composite larger than 16 bytes by
// a pointer to a copy the caller owns, so the message carries the
// addresses of the two values held on the command object.
func (c *commands) dispatch(pipe *pipeline, groups [2]uint32) {
	c.threads = pipe.workgroup
	c.groups = mtlSize{width: uintptr(groups[0]), height: uintptr(groups[1]), depth: 1}
	c.call(c.enc, selDispatchThreadgroups, uintptr(unsafe.Pointer(&c.groups)), uintptr(unsafe.Pointer(&c.threads)))
}

// Copy encodes a copy on the blit encoder, opening it when a compute
// encoder is open.
func (c *commands) Copy(dst hostgpu.Buffer, dstOffset int, src hostgpu.Buffer, srcOffset int, n int) {
	if !c.encoding() {
		return
	}
	if err := hostgpu.CheckCopy(dst, dstOffset, src, srcOffset, n); err != nil {
		c.fail(err)
		return
	}
	vd, okd := dst.(*buffer)
	vs, oks := src.(*buffer)
	if !okd || !oks {
		c.fail(hostgpu.ErrForeign)
		return
	}
	if n == 0 || !c.blitPass() {
		return
	}
	c.call(c.blit, selCopyFromBuffer, uintptr(vs.buf), uintptr(srcOffset), uintptr(vd.buf), uintptr(dstOffset), uintptr(n))
}

// Fill dispatches the backend's fill kernel on the compute encoder,
// the buffer bound at the fill's first byte, one thread per word.
func (c *commands) Fill(dst hostgpu.Buffer, offset int, n int, value uint32) {
	if !c.encoding() {
		return
	}
	if err := hostgpu.CheckFill(dst, offset, n); err != nil {
		c.fail(err)
		return
	}
	vd, ok := dst.(*buffer)
	if !ok {
		c.fail(hostgpu.ErrForeign)
		return
	}
	pipe := c.dev.fill
	groups, err := hostgpu.Groups([2]int{n / 4, 1}, [2]uint32{uint32(pipe.workgroup.width), uint32(pipe.workgroup.height)})
	if err != nil {
		c.fail(err)
		return
	}
	if n == 0 || !c.computePass() {
		return
	}
	c.call(c.enc, selSetComputePipelineState, uintptr(pipe.state))
	c.call(c.enc, selSetBuffer, uintptr(vd.buf), uintptr(offset), 0)
	c.fill = [2]uint32{uint32(n / 4), value}
	c.call(c.enc, selSetBytes, uintptr(unsafe.Pointer(&c.fill)), unsafe.Sizeof(c.fill), uintptr(pipe.dispatch))
	c.dispatch(pipe, groups)
}

// Barrier orders the buffer accesses of the commands encoded before
// it ahead of those encoded after it. The compute encoder is
// concurrent, so dispatches between barriers may overlap, and a
// memory barrier orders them. A blit encoder is ended instead, so the
// commands after the barrier go in a later pass, which the hazard
// tracking orders behind it.
func (c *commands) Barrier() {
	if !c.encoding() {
		return
	}
	switch {
	case c.enc != 0:
		c.call(c.enc, selMemoryBarrier, barrierScopeBuffers)
	case c.blit != 0:
		c.call(c.blit, selEndEncoding)
		c.blit = 0
	}
}

// Commit ends encoding and submits the command buffer, which joins
// the device's in-flight list under the next sequence number with
// the error its encoding met, and closes the encoder. The pool is
// popped and the OS thread released; Wait may run on any goroutine.
// A Commit on a closed encoder latches ErrClosed for the next Begin
// and returns the submission that names none.
func (c *commands) Commit() hostgpu.Submission {
	if !c.open {
		c.fail(hostgpu.ErrClosed)
		return 0
	}
	if c.enc != 0 {
		c.call(c.enc, selEndEncoding)
		c.enc = 0
	}
	if c.blit != 0 {
		c.call(c.blit, selEndEncoding)
		c.blit = 0
	}
	c.call(c.cb, selCommit)
	c.open = false
	popPool(&c.args, c.pool)
	seq := c.dev.inflight.Commit(submission{cb: c.cb, err: c.err})
	c.cb, c.err = 0, nil
	return seq
}

// Wait blocks until the submission completes, then releases it and
// every submission committed before it, since the queue completes
// them in order, reporting the errors of all of them. A submission
// already released by an earlier Wait reports nothing; a sequence
// number no Commit has returned is an error.
func (d *device) Wait(s hostgpu.Submission) error {
	subs, _, err := d.inflight.Pending(s)
	if err != nil || len(subs) == 0 {
		return err
	}
	pool := pushPool()
	defer popPool(&d.args, pool)
	send(&d.args, subs[len(subs)-1].cb, selWaitUntilCompleted)
	for _, sub := range subs {
		err = errors.Join(err, sub.err)
		if send(&d.args, sub.cb, selStatus) == commandBufferStatusError {
			err = errors.Join(err, errorText(objc.ID(send(&d.args, sub.cb, selError)), "command buffer failed"))
		}
		send(&d.args, sub.cb, selRelease)
	}
	d.inflight.Release(len(subs))
	return err
}
