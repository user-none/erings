// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux || windows

package vulkan

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/compile"
	"github.com/user-none/erings/internal/hostgpu/compile/spirv"
)

// device is the Vulkan device on its physical device, one compute
// queue, the timeline semaphore whose values are the submission
// sequence numbers, the pipelines Build made, the one encoder Begin
// hands out with its pool of command buffers, and the submissions
// committed and not yet waited on, in commit order. The encoder, the
// pool, and the in-flight list are retained, so the frame path
// allocates nothing once the first frame has run.
type device struct {
	fns      procs
	instance uintptr
	physical uintptr
	dev      uintptr
	queue    uintptr
	family   uint32
	widest   uint32 // workgroups across one dispatch (chosen)
	pool     uint64
	sem      uint64
	name     string
	pipes    []*pipeline
	enc      *commands
	free     []slot
	inflight hostgpu.Ledger[submission] // the committed slots, by sequence number
	slots    [argSlots]uintptr          // the argument array for calls off the encoder

	// Values the calls take by pointer, and the out parameters they
	// write, retained here so the addresses stay valid across the
	// call: a local's address taken as a uintptr is not updated when
	// the goroutine's stack moves.
	props      physicalDeviceProperties
	memory     physicalDeviceMemoryProperties
	features   physicalDeviceFeatures2
	feats11    physicalDeviceVulkan11Features
	feats12    physicalDeviceVulkan12Features
	feats13    physicalDeviceVulkan13Features
	extName    uintptr
	priority   float32
	queueInfo  deviceQueueCreateInfo
	devInfo    deviceCreateInfo
	poolInfo   commandPoolCreateInfo
	moduleInfo shaderModuleCreateInfo
	bindings   [maxBindings]descriptorSetLayoutBinding
	setInfo    descriptorSetLayoutCreateInfo
	pushRange  pushConstantRange
	layoutInfo pipelineLayoutCreateInfo
	pipeInfo   computePipelineCreateInfo
	bufInfo    bufferCreateInfo
	reqs       memoryRequirements
	allocInfo  memoryAllocateInfo
	cbInfo     commandBufferAllocateInfo
	semInfo    semaphoreCreateInfo
	semType    semaphoreTypeCreateInfo
	waitInfo   semaphoreWaitInfo
	signalInfo semaphoreSignalInfo
	appInfo    applicationInfo
	instInfo   instanceCreateInfo
	target     uint64 // the semaphore value a Wait waits for
	version    uint32
	n          uint32         // an enumeration's count
	out        uint64         // a handle a create call returns
	outPtr     uintptr        // a dispatchable handle a call returns
	mapped     unsafe.Pointer // a mapping vkMapMemory returns
}

// slot is a command buffer, reused once the submission that used it
// has completed.
type slot struct {
	cb uintptr
}

// submission is a committed slot with the error its commit reported,
// if any.
type submission struct {
	slot slot
	err  error
}

// pipeline is one entry point's compute pipeline and the dispatch
// parameters its layout fixes.
type pipeline struct {
	pipe, layout, setLayout, module uint64
	workgroup                       [2]uint32
	bindings                        int
	pushSize                        uint32
	bufferLengths                   bool
	group                           bool   // a group entry, whose grid is never split
	dynamic                         uint32 // the variable-length bindings, by bit
}

// buffer is a storage buffer: an upload, readback, or output buffer
// bound to host-visible coherent memory, mapped for its lifetime, a
// working buffer bound to device-local memory with no mapping and no
// data.
type buffer struct {
	dev  *device
	buf  uint64
	mem  uint64
	size uint64
	kind hostgpu.BufferKind
	data []byte
}

// Open creates the device on the first hardware physical device
// that meets the renderer's requirements. Every failure is an error;
// one that means no usable device wraps ErrUnavailable.
func Open() (hostgpu.Device, error) {
	if err := load(); err != nil {
		return nil, err
	}
	d := &device{}
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

// open creates the instance, chooses the physical device, creates
// the device with the features and the extension the shaders and the
// encoder use, and creates the queue, the command pool, and the
// timeline semaphore. The features enabled are the three optional
// ones suitable checked and the ones a 1.3 device is required to
// support: the timeline semaphore, the Vulkan memory model at device
// scope, which the emitted modules declare, and synchronization2,
// whose barrier and submit the encoder records.
func (d *device) open() error {
	if err := resolve(getInstanceProcAddr, 0, d.fns.globalSyms()); err != nil {
		return unavailable("%v", err)
	}
	if err := vkerr(call(&d.slots, d.fns.enumerateInstanceVersion, uintptr(unsafe.Pointer(&d.version))), "instance version"); err != nil {
		return unavailable("%v", err)
	}
	if d.version < apiVersion13 {
		return unavailable("instance version 0x%x, need 1.3", d.version)
	}
	d.appInfo = applicationInfo{sType: stApplicationInfo, apiVersion: apiVersion13}
	d.instInfo = instanceCreateInfo{sType: stInstanceCreateInfo, pApplicationInfo: uintptr(unsafe.Pointer(&d.appInfo))}
	d.outPtr = 0
	if err := vkerr(call(&d.slots, d.fns.createInstance, uintptr(unsafe.Pointer(&d.instInfo)), 0, uintptr(unsafe.Pointer(&d.outPtr))), "instance"); err != nil {
		return unavailable("%v", err)
	}
	d.instance = d.outPtr
	if err := resolve(getInstanceProcAddr, d.instance, d.fns.instanceSyms()); err != nil {
		return unavailable("%v", err)
	}
	if err := d.choose(); err != nil {
		return err
	}
	d.priority = 1
	d.queueInfo = deviceQueueCreateInfo{sType: stDeviceQueueCreateInfo, queueFamilyIndex: d.family, queueCount: 1, pQueuePriorities: uintptr(unsafe.Pointer(&d.priority))}
	d.featureChain()
	d.features.features[featureShaderInt64] = 1
	d.feats11.variablePointersStorageBuffer = 1
	d.feats12.storageBuffer8BitAccess, d.feats12.timelineSemaphore, d.feats12.vulkanMemoryModel, d.feats12.vulkanMemoryModelDeviceScope = 1, 1, 1, 1
	d.feats13.synchronization2 = 1
	d.extName = cstr(cPushDescriptor)
	d.devInfo = deviceCreateInfo{
		sType: stDeviceCreateInfo, pNext: uintptr(unsafe.Pointer(&d.features)),
		queueCreateInfoCount: 1, pQueueCreateInfos: uintptr(unsafe.Pointer(&d.queueInfo)),
		enabledExtensionCount: 1, ppEnabledExtensionNames: uintptr(unsafe.Pointer(&d.extName)),
	}
	d.outPtr = 0
	if err := vkerr(call(&d.slots, d.fns.createDevice, d.physical, uintptr(unsafe.Pointer(&d.devInfo)), 0, uintptr(unsafe.Pointer(&d.outPtr))), "device"); err != nil {
		return unavailable("%v", err)
	}
	d.dev = d.outPtr
	if err := resolve(d.fns.getDeviceProcAddr, d.dev, d.fns.deviceSyms()); err != nil {
		return unavailable("%v", err)
	}
	call(&d.slots, d.fns.getDeviceQueue, d.dev, uintptr(d.family), 0, uintptr(unsafe.Pointer(&d.queue)))
	d.poolInfo = commandPoolCreateInfo{sType: stCommandPoolCreateInfo, flags: commandPoolResetBit, queueFamilyIndex: d.family}
	d.out = 0
	if err := vkerr(call(&d.slots, d.fns.createCommandPool, d.dev, uintptr(unsafe.Pointer(&d.poolInfo)), 0, uintptr(unsafe.Pointer(&d.out))), "command pool"); err != nil {
		return err
	}
	d.pool = d.out
	// The semaphore starts at zero, below the first sequence number.
	d.semType = semaphoreTypeCreateInfo{sType: stSemaphoreTypeCreateInfo, semaphoreType: semaphoreTypeTimeline}
	d.semInfo = semaphoreCreateInfo{sType: stSemaphoreCreateInfo, pNext: uintptr(unsafe.Pointer(&d.semType))}
	d.out = 0
	if err := vkerr(call(&d.slots, d.fns.createSemaphore, d.dev, uintptr(unsafe.Pointer(&d.semInfo)), 0, uintptr(unsafe.Pointer(&d.out))), "semaphore"); err != nil {
		return err
	}
	d.sem = d.out
	call(&d.slots, d.fns.getPhysicalDeviceMemoryProperties, d.physical, uintptr(unsafe.Pointer(&d.memory)))
	return nil
}

// featureChain resets the feature structures to their types, chained
// from features through the 1.1, 1.2, and 1.3 structures, with every
// feature clear: the form a query fills and a create reads.
func (d *device) featureChain() {
	d.features = physicalDeviceFeatures2{sType: stPhysicalDeviceFeatures2, pNext: uintptr(unsafe.Pointer(&d.feats11))}
	d.feats11 = physicalDeviceVulkan11Features{sType: stPhysicalDeviceVulkan11Feats, pNext: uintptr(unsafe.Pointer(&d.feats12))}
	d.feats12 = physicalDeviceVulkan12Features{sType: stPhysicalDeviceVulkan12Feats, pNext: uintptr(unsafe.Pointer(&d.feats13))}
	d.feats13 = physicalDeviceVulkan13Features{sType: stPhysicalDeviceVulkan13Feats}
}

// choose takes the first physical device that is hardware, at least
// Vulkan 1.3, has a compute queue family, advertises the push
// descriptor extension, and supports 64-bit integers, 8-bit storage
// buffer access, and storage buffer variable pointers, the features
// a 1.3 device may still lack. A CPU device is a software
// implementation of the hardware the backend exists to use, which
// the software renderer already is, so it never qualifies. None is
// ErrUnavailable.
func (d *device) choose() error {
	d.n = 0
	if err := vkerr(call(&d.slots, d.fns.enumeratePhysicalDevices, d.instance, uintptr(unsafe.Pointer(&d.n)), 0), "physical devices"); err != nil {
		return unavailable("%v", err)
	}
	if d.n == 0 {
		return unavailable("no physical device")
	}
	devs := make([]uintptr, d.n)
	r := call(&d.slots, d.fns.enumeratePhysicalDevices, d.instance, uintptr(unsafe.Pointer(&d.n)), uintptr(unsafe.Pointer(&devs[0])))
	runtime.KeepAlive(devs)
	if err := vkerr(r, "physical devices"); err != nil {
		return unavailable("%v", err)
	}
	// suitable reuses n, so the count is taken before the loop.
	var chosen candidate
	for _, pd := range devs[:d.n] {
		if c, ok := d.suitable(pd); ok {
			chosen = c
			break
		}
	}
	if chosen.physical == 0 {
		return unavailable("no hardware device meets the shaders' requirements")
	}
	// suitable left the chosen device's properties in props.
	d.physical, d.family = chosen.physical, chosen.family
	d.name = cString(d.props.deviceName[:])
	// The spec's minimum is 65535. The floor of one keeps Dispatch's
	// split advancing on a driver that reports zero.
	d.widest = max(d.props.limits.maxComputeWorkGroupCount[0], 1)
	return nil
}

// candidate is a physical device that meets the requirements, with
// the compute queue family to use.
type candidate struct {
	physical uintptr
	family   uint32
}

// suitable reports whether a physical device meets the requirements,
// leaving its properties in props.
func (d *device) suitable(pd uintptr) (candidate, bool) {
	call(&d.slots, d.fns.getPhysicalDeviceProperties, pd, uintptr(unsafe.Pointer(&d.props)))
	if d.props.deviceType == physicalDeviceTypeCPU || d.props.apiVersion < apiVersion13 {
		return candidate{}, false
	}
	d.featureChain()
	call(&d.slots, d.fns.getPhysicalDeviceFeatures2, pd, uintptr(unsafe.Pointer(&d.features)))
	if d.features.features[featureShaderInt64] == 0 || d.feats12.storageBuffer8BitAccess == 0 || d.feats11.variablePointersStorageBuffer == 0 {
		return candidate{}, false
	}
	if !d.extensions(pd)[cString(cPushDescriptor)] {
		return candidate{}, false
	}
	c := candidate{physical: pd}
	d.n = 0
	call(&d.slots, d.fns.getPhysicalDeviceQueueFamilyProperties, pd, uintptr(unsafe.Pointer(&d.n)), 0)
	if d.n == 0 {
		return candidate{}, false
	}
	families := make([]queueFamilyProperties, d.n)
	call(&d.slots, d.fns.getPhysicalDeviceQueueFamilyProperties, pd, uintptr(unsafe.Pointer(&d.n)), uintptr(unsafe.Pointer(&families[0])))
	runtime.KeepAlive(families)
	for i, f := range families[:d.n] {
		if f.queueFlags&queueComputeBit != 0 && f.queueCount > 0 {
			c.family = uint32(i)
			return c, true
		}
	}
	return candidate{}, false
}

// extensions is the set of device extension names a physical device
// advertises.
func (d *device) extensions(pd uintptr) map[string]bool {
	set := map[string]bool{}
	d.n = 0
	if r := call(&d.slots, d.fns.enumerateDeviceExtensionProperties, pd, 0, uintptr(unsafe.Pointer(&d.n)), 0); int32(r) < 0 || d.n == 0 {
		return set
	}
	props := make([]extensionProperties, d.n)
	r := call(&d.slots, d.fns.enumerateDeviceExtensionProperties, pd, 0, uintptr(unsafe.Pointer(&d.n)), uintptr(unsafe.Pointer(&props[0])))
	runtime.KeepAlive(props)
	if int32(r) < 0 {
		return set
	}
	for _, p := range props[:d.n] {
		set[cString(p.extensionName[:])] = true
	}
	return set
}

// cString reads a NUL-terminated byte array.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// Name is the physical device's name.
func (d *device) Name() string { return d.name }

// GroupThreads is hostgpu.GroupWidth with no lane count, which core
// Vulkan does not report, and the widest group row the device's
// workgroup count limit issues in one dispatch.
func (d *device) GroupThreads(load hostgpu.GroupLoad) int {
	return hostgpu.GroupWidth(load, 0, spirv.GroupWorkgroup, int(d.widest)*spirv.GroupWorkgroup)
}

// Build emits the lowered module as SPIR-V, one module per entry
// point, and builds one pipeline per entry in the module's entry
// order: the shader module, a push descriptor set layout with one
// storage buffer binding per buffer, a pipeline layout with the push
// block, and the compute pipeline. The binding count, the push size,
// and the workgroup are held to the device's limits.
func (d *device) Build(mod *compile.Module) ([]hostgpu.Pipeline, error) {
	out, err := spirv.Emit(mod)
	if err != nil {
		return nil, err
	}
	pipes := make([]hostgpu.Pipeline, 0, len(out.Entries))
	for i, en := range out.Entries {
		p, err := d.pipeline(mod.Funcs[mod.Entries[i].Func].Name, en)
		if err != nil {
			return nil, err
		}
		pipes = append(pipes, p)
	}
	return pipes, nil
}

// pipeline creates one entry's pipeline and keeps it for Close.
func (d *device) pipeline(name string, en spirv.EntryPoint) (*pipeline, error) {
	limits := &d.props.limits
	bindings := len(en.Buffers)
	if en.ParamsBinding >= 0 {
		bindings++
	}
	switch {
	case bindings > maxBindings:
		return nil, fmt.Errorf("%s binds %d buffers, a push descriptor set holds %d", name, bindings, maxBindings)
	case uint32(bindings) > limits.maxPerStageDescriptorStorageBuffers:
		return nil, fmt.Errorf("%s binds %d buffers, the device allows %d", name, bindings, limits.maxPerStageDescriptorStorageBuffers)
	case uint32(en.PushSize) > limits.maxPushConstantsSize:
		return nil, fmt.Errorf("%s pushes %d bytes, the device allows %d", name, en.PushSize, limits.maxPushConstantsSize)
	case uint32(en.Workgroup[0]) > limits.maxComputeWorkGroupSize[0] || uint32(en.Workgroup[1]) > limits.maxComputeWorkGroupSize[1] ||
		uint32(en.Workgroup[0]*en.Workgroup[1]) > limits.maxComputeWorkGroupInvocations:
		return nil, fmt.Errorf("%s workgroup %v exceeds the device's size %v or its %d invocations", name, en.Workgroup, limits.maxComputeWorkGroupSize, limits.maxComputeWorkGroupInvocations)
	}
	p := &pipeline{workgroup: [2]uint32{uint32(en.Workgroup[0]), uint32(en.Workgroup[1])}, bindings: bindings, pushSize: uint32(en.PushSize), bufferLengths: en.BufferLengths, group: en.Group, dynamic: compile.DynamicBindings(en.Buffers)}
	d.moduleInfo = shaderModuleCreateInfo{sType: stShaderModuleCreateInfo, codeSize: uintptr(4 * len(en.Code)), pCode: uintptr(unsafe.Pointer(&en.Code[0]))}
	r := call(&d.slots, d.fns.createShaderModule, d.dev, uintptr(unsafe.Pointer(&d.moduleInfo)), 0, uintptr(unsafe.Pointer(&p.module)))
	runtime.KeepAlive(en.Code)
	if err := vkerr(r, "shader module of "+name); err != nil {
		return nil, err
	}
	d.pipes = append(d.pipes, p)
	for i := 0; i < bindings; i++ {
		d.bindings[i] = descriptorSetLayoutBinding{binding: uint32(i), descriptorType: descriptorStorageBuffer, descriptorCount: 1, stageFlags: shaderStageCompute}
	}
	d.setInfo = descriptorSetLayoutCreateInfo{sType: stDescriptorSetLayoutCreate, flags: setLayoutPushDescriptorBit, bindingCount: uint32(bindings), pBindings: uintptr(unsafe.Pointer(&d.bindings[0]))}
	d.out = 0
	if err := vkerr(call(&d.slots, d.fns.createDescriptorSetLayout, d.dev, uintptr(unsafe.Pointer(&d.setInfo)), 0, uintptr(unsafe.Pointer(&d.out))), "set layout of "+name); err != nil {
		return nil, err
	}
	p.setLayout = d.out
	d.pushRange = pushConstantRange{stageFlags: shaderStageCompute, size: p.pushSize}
	d.layoutInfo = pipelineLayoutCreateInfo{sType: stPipelineLayoutCreateInfo, setLayoutCount: 1, pSetLayouts: uintptr(unsafe.Pointer(&p.setLayout)),
		pushConstantRangeCount: 1, pPushConstantRanges: uintptr(unsafe.Pointer(&d.pushRange))}
	d.out = 0
	if err := vkerr(call(&d.slots, d.fns.createPipelineLayout, d.dev, uintptr(unsafe.Pointer(&d.layoutInfo)), 0, uintptr(unsafe.Pointer(&d.out))), "pipeline layout of "+name); err != nil {
		return nil, err
	}
	p.layout = d.out
	d.pipeInfo = computePipelineCreateInfo{sType: stComputePipelineCreateInfo,
		stage:  pipelineShaderStageCreateInfo{sType: stPipelineShaderStage, stage: shaderStageCompute, module: p.module, pName: cstr(cMain)},
		layout: p.layout}
	if err := vkerr(call(&d.slots, d.fns.createComputePipelines, d.dev, 0, 1, uintptr(unsafe.Pointer(&d.pipeInfo)), 0, uintptr(unsafe.Pointer(&p.pipe))), "pipeline of "+name); err != nil {
		return nil, err
	}
	return p, nil
}

// NewBuffer creates a storage buffer of size bytes in the memory type
// memoryType picks for the kind. A host-visible buffer is mapped for
// its lifetime, so a dispatch writing an output buffer stores into
// the memory the CPU reads, with no copy.
func (d *device) NewBuffer(size int, kind hostgpu.BufferKind) (hostgpu.Buffer, error) {
	if size <= 0 {
		return nil, fmt.Errorf("buffer of %d bytes", size)
	}
	b := &buffer{dev: d, size: uint64(size), kind: kind}
	// Every buffer may be bound to a dispatch and be the source or the
	// destination of a copy or a fill.
	d.bufInfo = bufferCreateInfo{sType: stBufferCreateInfo, size: b.size, usage: bufferUsageStorage | bufferUsageTransferSrc | bufferUsageTransferDst}
	if err := vkerr(call(&d.slots, d.fns.createBuffer, d.dev, uintptr(unsafe.Pointer(&d.bufInfo)), 0, uintptr(unsafe.Pointer(&b.buf))), fmt.Sprintf("buffer of %d bytes", size)); err != nil {
		return nil, err
	}
	call(&d.slots, d.fns.getBufferMemoryRequirements, d.dev, uintptr(b.buf), uintptr(unsafe.Pointer(&d.reqs)))
	typ, ok := d.memoryType(d.reqs.memoryTypeBits, kind)
	if !ok {
		b.Release()
		return nil, errors.New("no memory type for a buffer of its kind")
	}
	d.allocInfo = memoryAllocateInfo{sType: stMemoryAllocateInfo, allocationSize: d.reqs.size, memoryTypeIndex: typ}
	d.out = 0
	if err := vkerr(call(&d.slots, d.fns.allocateMemory, d.dev, uintptr(unsafe.Pointer(&d.allocInfo)), 0, uintptr(unsafe.Pointer(&d.out))), fmt.Sprintf("memory of %d bytes", d.reqs.size)); err != nil {
		b.Release()
		return nil, err
	}
	b.mem = d.out
	if err := vkerr(call(&d.slots, d.fns.bindBufferMemory, d.dev, uintptr(b.buf), uintptr(b.mem), 0), "bind buffer memory"); err != nil {
		b.Release()
		return nil, err
	}
	if kind == hostgpu.BufferWorking {
		return b, nil
	}
	d.mapped = nil
	if err := vkerr(call(&d.slots, d.fns.mapMemory, d.dev, uintptr(b.mem), 0, uintptr(b.size), 0, uintptr(unsafe.Pointer(&d.mapped))), "map memory"); err != nil {
		b.Release()
		return nil, err
	}
	b.data = unsafe.Slice((*byte)(d.mapped), size)
	return b, nil
}

// memoryType is the memory type for a buffer of the kind: for a
// working buffer the first the requirements allow that is device
// local, and for every other kind the first they allow that is host
// visible and coherent, preferring one also host cached.
func (d *device) memoryType(allowed uint32, kind hostgpu.BufferKind) (uint32, bool) {
	if kind == hostgpu.BufferWorking {
		for i := uint32(0); i < d.memory.memoryTypeCount; i++ {
			if allowed&(1<<i) != 0 && d.memory.memoryTypes[i].propertyFlags&memoryDeviceLocal != 0 {
				return i, true
			}
		}
		return 0, false
	}
	const want = memoryHostVisible | memoryHostCoherent
	chosen, ok := uint32(0), false
	for i := uint32(0); i < d.memory.memoryTypeCount; i++ {
		flags := d.memory.memoryTypes[i].propertyFlags
		if allowed&(1<<i) == 0 || flags&want != want {
			continue
		}
		if !ok {
			chosen, ok = i, true
		}
		if flags&memoryHostCached != 0 {
			return i, true
		}
	}
	return chosen, ok
}

// Bytes is an upload, readback, or output buffer's memory, valid until
// Release, and nil for a working buffer.
func (b *buffer) Bytes() []byte { return b.data }

// Size is the buffer's byte size.
func (b *buffer) Size() int { return int(b.size) }

// Kind is the kind the buffer was allocated as.
func (b *buffer) Kind() hostgpu.BufferKind { return b.kind }

// Release unmaps a mapped buffer's memory, frees the buffer's memory,
// and destroys the buffer. A buffer already released is left alone.
func (b *buffer) Release() {
	d := b.dev
	if b.mem != 0 {
		if b.data != nil {
			call(&d.slots, d.fns.unmapMemory, d.dev, uintptr(b.mem))
		}
		call(&d.slots, d.fns.freeMemory, d.dev, uintptr(b.mem), 0)
	}
	if b.buf != 0 {
		call(&d.slots, d.fns.destroyBuffer, d.dev, uintptr(b.buf), 0)
	}
	b.buf, b.mem, b.data = 0, 0, nil
}

// Close waits for the submissions in flight and releases them, the
// slot of an encoder still open, the command pool, the pipelines, the
// semaphore, the device, and the instance. The open slot was never
// committed, so it has nothing to wait for: its command buffer is
// destroyed with the command pool. A wait that fails other than by a
// lost device did not wait, and the semaphore, the command pool, and
// the device are still destroyed, as Close has no later point to
// release them at.
func (d *device) Close() {
	if s := d.inflight.Newest(); s != 0 {
		d.Wait(s)
	}
	d.inflight = hostgpu.Ledger[submission]{}
	if c := d.enc; c.open {
		d.free = append(d.free, c.slot)
		c.slot, c.open = slot{}, false
	}
	d.free = nil
	for _, p := range d.pipes {
		d.destroy(d.fns.destroyPipeline, &p.pipe)
		d.destroy(d.fns.destroyPipelineLayout, &p.layout)
		d.destroy(d.fns.destroyDescriptorSetLayout, &p.setLayout)
		d.destroy(d.fns.destroyShaderModule, &p.module)
	}
	d.pipes = nil
	d.destroy(d.fns.destroyCommandPool, &d.pool)
	d.destroy(d.fns.destroySemaphore, &d.sem)
	if d.dev != 0 {
		call(&d.slots, d.fns.destroyDevice, d.dev, 0)
		d.dev = 0
	}
	if d.instance != 0 {
		call(&d.slots, d.fns.destroyInstance, d.instance, 0)
		d.instance = 0
	}
}

// destroy releases a device object through its destroy function, if
// the handle holds one.
func (d *device) destroy(fn uintptr, handle *uint64) {
	if *handle != 0 {
		call(&d.slots, fn, d.dev, uintptr(*handle), 0)
		*handle = 0
	}
}

// maxBindings is the descriptors a push descriptor set holds:
// maxPushDescriptors' required minimum, which the backend does not
// query.
const maxBindings = 32

// commands is the device's encoder: the slot being recorded, open
// from Begin to Commit. Commit hands the slot to the in-flight list
// under its sequence number and leaves the encoder closed for the
// next Begin, so no handle to a submission outlives it: a Submission
// is a number, and the encoder refuses encoding while closed.
//
// The argument array, the descriptor writes and their buffer
// records, the push block, the barrier and its dependency, and the
// begin and submit records are retained memory the calls take the
// addresses of.
type commands struct {
	dev   *device
	open  bool
	err   error // the first host error latched on the encoder (fail)
	slot  slot
	bound uint64 // the pipeline bound on the command buffer
	slots [argSlots]uintptr

	begin    commandBufferBeginInfo
	writes   [maxBindings]writeDescriptorSet
	infos    [maxBindings]descriptorBufferInfo
	push     [spirv.PushWords + maxBindings]uint32 // the record, then the word counts by binding
	bar      memoryBarrier2
	dep      dependencyInfo
	region   bufferCopy
	submit   submitInfo2
	cbSubmit commandBufferSubmitInfo
	signal   semaphoreSubmitInfo
}

// Begin opens the encoder on a slot from the pool, or a new one,
// and records the ordering barrier: this submission's shader and
// transfer reads and writes follow every earlier submission's shader
// and transfer writes on the queue. An encoder still open from an
// earlier Begin is an error.
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
	c.begin = commandBufferBeginInfo{sType: stCommandBufferBeginInfo, flags: commandBufferOneTimeSubmit}
	if err := vkerr(call(&c.slots, d.fns.beginCommandBuffer, s.cb, uintptr(unsafe.Pointer(&c.begin))), "begin command buffer"); err != nil {
		d.free = append(d.free, s)
		return nil, err
	}
	c.slot, c.open, c.bound = s, true, 0
	c.barrier()
	return c, nil
}

// takeSlot pops the free list, or allocates a command buffer from the
// pool. A command buffer has no release short of the pool's.
func (d *device) takeSlot() (slot, error) {
	if n := len(d.free); n > 0 {
		s := d.free[n-1]
		d.free = d.free[:n-1]
		return s, nil
	}
	d.cbInfo = commandBufferAllocateInfo{sType: stCommandBufferAllocateInfo, commandPool: d.pool, commandBufferCount: 1}
	d.outPtr = 0
	if err := vkerr(call(&d.slots, d.fns.allocateCommandBuffers, d.dev, uintptr(unsafe.Pointer(&d.cbInfo)), uintptr(unsafe.Pointer(&d.outPtr))), "command buffer"); err != nil {
		return slot{}, err
	}
	return slot{cb: d.outPtr}, nil
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

// barrier records a memory barrier between the compute and transfer
// stages, the stages a dispatch and a copy run in: shader and
// transfer writes before it are visible to shader and transfer reads
// and writes after it.
func (c *commands) barrier() {
	const stages = pipelineStageCompute | pipelineStageTransfer
	c.record(stages, accessShaderWrite|accessTransferWrite, stages, accessShaderRead|accessShaderWrite|accessTransferRead|accessTransferWrite)
}

// hostBarrier records a memory barrier from the compute and transfer
// stages to the host: the device's writes in the submission are
// visible to the host's reads once the semaphore is waited on, which
// the semaphore alone does not make them.
func (c *commands) hostBarrier() {
	c.record(pipelineStageCompute|pipelineStageTransfer, accessShaderWrite|accessTransferWrite, pipelineStageHost, accessHostRead)
}

// record records one global memory barrier with the stage and access
// masks through vkCmdPipelineBarrier2.
func (c *commands) record(srcStages, srcAccess, dstStages, dstAccess uint64) {
	c.bar = memoryBarrier2{sType: stMemoryBarrier2, srcStageMask: srcStages, srcAccessMask: srcAccess, dstStageMask: dstStages, dstAccessMask: dstAccess}
	c.dep = dependencyInfo{sType: stDependencyInfo, memoryBarrierCount: 1, pMemoryBarriers: uintptr(unsafe.Pointer(&c.bar))}
	call(&c.slots, c.dev.fns.cmdPipelineBarrier2, c.slot.cb, uintptr(unsafe.Pointer(&c.dep)))
}

// Copy records vkCmdCopyBuffer. It runs in the transfer stage, which
// Barrier orders with the compute stage.
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
	if n == 0 {
		return
	}
	c.region = bufferCopy{srcOffset: uint64(srcOffset), dstOffset: uint64(dstOffset), size: uint64(n)}
	call(&c.slots, c.dev.fns.cmdCopyBuffer, c.slot.cb, uintptr(vs.buf), uintptr(vd.buf), 1, uintptr(unsafe.Pointer(&c.region)))
}

// Fill records vkCmdFillBuffer, which writes the 32-bit value to every
// word of the range. It runs in the transfer stage, as a copy does.
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
	if n == 0 {
		return
	}
	call(&c.slots, c.dev.fns.cmdFillBuffer, c.slot.cb, uintptr(vd.buf), uintptr(offset), uintptr(n), uintptr(value))
}

// Dispatch encodes one entry point: the pipeline when it is not the
// one bound, the buffers pushed as storage buffer descriptors at
// bindings 0 to n-1 in order, the record and the word counts as push
// constants, and workgroups of the pipeline's size covering the grid
// rounded up. The emitted wrapper guards the overshoot. A buffer
// count other than the entry's layout declares is a host error
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
	if err := hostgpu.CheckBindings(buffers, pipe.bindings, pipe.dynamic); err != nil {
		c.fail(err)
		return
	}
	groups, err := hostgpu.Groups(grid, pipe.workgroup)
	if err != nil {
		c.fail(err)
		return
	}
	d := c.dev
	gx, gy := groups[0], groups[1]
	// An index or span grid wider than the device's workgroup count
	// limit is issued in pieces, the thread base word carrying each
	// piece's first thread. The group shell adds no thread base, so a
	// group grid is never split.
	limit := d.widest
	if pipe.group && gx > limit {
		c.fail(hostgpu.ErrGroupWide)
		return
	}
	// Rows are never split, so a grid with more rows than one dispatch
	// issues is refused.
	if gy > d.props.limits.maxComputeWorkGroupCount[1] {
		c.fail(hostgpu.ErrGridRows)
		return
	}
	for i, b := range buffers {
		vb, ok := b.(*buffer)
		if !ok {
			c.fail(hostgpu.ErrForeign)
			return
		}
		c.infos[i] = descriptorBufferInfo{buffer: vb.buf, rng: vb.size}
		c.writes[i] = writeDescriptorSet{sType: stWriteDescriptorSet, dstBinding: uint32(i), descriptorCount: 1, descriptorType: descriptorStorageBuffer, pBufferInfo: uintptr(unsafe.Pointer(&c.infos[i]))}
	}
	if pipe.pipe != c.bound {
		call(&c.slots, d.fns.cmdBindPipeline, c.slot.cb, bindPointCompute, uintptr(pipe.pipe))
		c.bound = pipe.pipe
	}
	if pipe.bufferLengths {
		hostgpu.Lengths(c.push[spirv.PushWords:], buffers)
	}
	// A push takes at least one write: an entry with no bindings has
	// nothing to push.
	if len(buffers) > 0 {
		call(&c.slots, d.fns.cmdPushDescriptorSet, c.slot.cb, bindPointCompute, uintptr(pipe.layout), 0, uintptr(len(buffers)), uintptr(unsafe.Pointer(&c.writes[0])))
	}
	c.push[0], c.push[1], c.push[2], c.push[3], c.push[4] = rec.BoundX, rec.BoundY, rec.ParamsOffset, rec.ParamsStride, rec.Segment
	for base := uint32(0); ; {
		n := min(gx-base, limit)
		c.push[spirv.ThreadBaseWord] = base * pipe.workgroup[0]
		call(&c.slots, d.fns.cmdPushConstants, c.slot.cb, uintptr(pipe.layout), shaderStageCompute, 0, uintptr(pipe.pushSize), uintptr(unsafe.Pointer(&c.push[0])))
		call(&c.slots, d.fns.cmdDispatch, c.slot.cb, uintptr(n), uintptr(gy), 1)
		base += n
		if base >= gx {
			break
		}
	}
}

// Barrier orders the dispatches, copies, and fills encoded before it
// ahead of those encoded after it.
func (c *commands) Barrier() {
	if !c.encoding() {
		return
	}
	c.barrier()
}

// Commit ends the command buffer and submits it with a signal of the
// semaphore to the submission's number, which joins the device's
// in-flight list, and closes the encoder. A command buffer whose end
// failed is not submitted, but the signal still is, with no command
// buffer, which the queue orders after every earlier submission's,
// so Wait releases the submission in turn and reports the error kept
// on it. Only when the queue refuses the submit is the semaphore
// signaled from the host: that signal is unordered against the
// queue's, which a lost device makes moot, since nothing it has
// pending signals and Wait reports the loss. A host error latched
// while encoding rides on the submission the same way, joined with
// any end or submit error: the commands recorded before it are valid
// and run. A Commit on a closed encoder latches ErrClosed for the
// next Begin and returns the submission that names none.
func (c *commands) Commit() hostgpu.Submission {
	if !c.open {
		c.fail(hostgpu.ErrClosed)
		return 0
	}
	d := c.dev
	seq := d.inflight.Next()
	c.hostBarrier()
	err := vkerr(call(&c.slots, d.fns.endCommandBuffer, c.slot.cb), "end command buffer")
	c.cbSubmit = commandBufferSubmitInfo{sType: stCommandBufferSubmitInfo, commandBuffer: c.slot.cb}
	// The signal follows every stage of the submission, the host
	// barrier's included.
	c.signal = semaphoreSubmitInfo{sType: stSemaphoreSubmitInfo, semaphore: d.sem, value: uint64(seq), stageMask: pipelineStageAllCommands}
	c.submit = submitInfo2{sType: stSubmitInfo2, commandBufferInfoCount: 1, pCommandBufferInfos: uintptr(unsafe.Pointer(&c.cbSubmit)),
		signalSemaphoreInfoCount: 1, pSignalSemaphoreInfos: uintptr(unsafe.Pointer(&c.signal))}
	if err != nil {
		c.submit.commandBufferInfoCount = 0
	}
	if subErr := vkerr(call(&c.slots, d.fns.queueSubmit2, d.queue, 1, uintptr(unsafe.Pointer(&c.submit)), 0), "queue submit"); subErr != nil {
		err = errors.Join(err, subErr)
		d.signalInfo = semaphoreSignalInfo{sType: stSemaphoreSignalInfo, semaphore: d.sem, value: uint64(seq)}
		call(&c.slots, d.fns.signalSemaphore, d.dev, uintptr(unsafe.Pointer(&d.signalInfo)))
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
// every submission committed before it, since the queue signals the
// semaphore in order, returning each slot to the pool and reporting
// the errors of all of them and a lost device. A lost device fails
// the wait and releases them, as a lost device executes nothing
// more. A wait that fails otherwise did not wait, so the GPU may
// still be executing them: they stay in flight, their slots
// untouched, for a later Wait or Close to wait on again. A submission
// already released by an earlier Wait reports nothing; a sequence
// number no Commit has returned is an error.
func (d *device) Wait(s hostgpu.Submission) error {
	subs, target, err := d.inflight.Pending(s)
	if err != nil || len(subs) == 0 {
		return err
	}
	d.target = uint64(target)
	d.waitInfo = semaphoreWaitInfo{sType: stSemaphoreWaitInfo, semaphoreCount: 1, pSemaphores: uintptr(unsafe.Pointer(&d.sem)), pValues: uintptr(unsafe.Pointer(&d.target))}
	r := call(&d.slots, d.fns.waitSemaphores, d.dev, uintptr(unsafe.Pointer(&d.waitInfo)), ^uintptr(0))
	if int32(r) < 0 && int32(r) != errorDeviceLost {
		return vkerr(r, "semaphore wait")
	}
	err = vkerr(r, "semaphore wait")
	for i := range subs {
		err = errors.Join(err, subs[i].err)
		d.free = append(d.free, subs[i].slot)
	}
	d.inflight.Release(len(subs))
	return err
}
