// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux || windows

package vulkan

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Vulkan constants used here, from the public header. The stage and
// access masks are the 64-bit flags of synchronization2, whose low
// bits are the 1.0 flags of the same name.
const (
	stApplicationInfo             = 0
	stInstanceCreateInfo          = 1
	stDeviceQueueCreateInfo       = 2
	stDeviceCreateInfo            = 3
	stMemoryAllocateInfo          = 5
	stSemaphoreCreateInfo         = 9
	stBufferCreateInfo            = 12
	stShaderModuleCreateInfo      = 16
	stPipelineShaderStage         = 18
	stComputePipelineCreateInfo   = 29
	stPipelineLayoutCreateInfo    = 30
	stDescriptorSetLayoutCreate   = 32
	stWriteDescriptorSet          = 35
	stCommandPoolCreateInfo       = 39
	stCommandBufferAllocateInfo   = 40
	stCommandBufferBeginInfo      = 42
	stPhysicalDeviceVulkan11Feats = 49
	stPhysicalDeviceVulkan12Feats = 51
	stPhysicalDeviceVulkan13Feats = 53
	stPhysicalDeviceFeatures2     = 1000059000
	stSemaphoreTypeCreateInfo     = 1000207002
	stSemaphoreWaitInfo           = 1000207004
	stSemaphoreSignalInfo         = 1000207005
	stMemoryBarrier2              = 1000314000
	stDependencyInfo              = 1000314003
	stSubmitInfo2                 = 1000314004
	stSemaphoreSubmitInfo         = 1000314005
	stCommandBufferSubmitInfo     = 1000314006
	queueComputeBit               = 2
	bufferUsageTransferSrc        = 0x1
	bufferUsageTransferDst        = 0x2
	bufferUsageStorage            = 0x20
	memoryDeviceLocal             = 1
	memoryHostVisible             = 2
	memoryHostCoherent            = 4
	memoryHostCached              = 8
	shaderStageCompute            = 0x20
	pipelineStageCompute          = 0x800
	pipelineStageTransfer         = 0x1000
	pipelineStageHost             = 0x4000
	pipelineStageAllCommands      = 0x10000
	accessShaderRead              = 0x20
	accessShaderWrite             = 0x40
	accessTransferRead            = 0x800
	accessTransferWrite           = 0x1000
	accessHostRead                = 0x2000
	semaphoreTypeTimeline         = 1
	descriptorStorageBuffer       = 7
	bindPointCompute              = 1
	commandPoolResetBit           = 2
	commandBufferOneTimeSubmit    = 1
	setLayoutPushDescriptorBit    = 1
	physicalDeviceTypeCPU         = 4
	apiVersion13                  = 1<<22 | 3<<12
	featuresCount                 = 55 // VkPhysicalDeviceFeatures is 55 VkBool32
	featureShaderInt64            = 40 // its index in VkPhysicalDeviceFeatures
	maxMemoryTypes                = 32
	maxMemoryHeaps                = 16
	maxExtensionName              = 256
	maxPhysicalDeviceName         = 256
	physicalDeviceSparseWordCount = 5
	errorDeviceLost               = -4 // VK_ERROR_DEVICE_LOST
)

// The structures passed by pointer, field for field from the header.
// Go's natural alignment reproduces the C layout on the 64-bit
// targets. Dispatchable handles are uintptr, non-dispatchable ones uint64.

type applicationInfo struct {
	sType              uint32
	pNext              uintptr
	pApplicationName   uintptr
	applicationVersion uint32
	pEngineName        uintptr
	engineVersion      uint32
	apiVersion         uint32
}

type instanceCreateInfo struct {
	sType                   uint32
	pNext                   uintptr
	flags                   uint32
	pApplicationInfo        uintptr
	enabledLayerCount       uint32
	ppEnabledLayerNames     uintptr
	enabledExtensionCount   uint32
	ppEnabledExtensionNames uintptr
}

type deviceQueueCreateInfo struct {
	sType            uint32
	pNext            uintptr
	flags            uint32
	queueFamilyIndex uint32
	queueCount       uint32
	pQueuePriorities uintptr
}

type deviceCreateInfo struct {
	sType                   uint32
	pNext                   uintptr
	flags                   uint32
	queueCreateInfoCount    uint32
	pQueueCreateInfos       uintptr
	enabledLayerCount       uint32
	ppEnabledLayerNames     uintptr
	enabledExtensionCount   uint32
	ppEnabledExtensionNames uintptr
	pEnabledFeatures        uintptr
}

type physicalDeviceFeatures2 struct {
	sType    uint32
	pNext    uintptr
	features [featuresCount]uint32
}

// The features of each core version after 1.0, as the header lays
// each structure out: the ones this backend reads or enables by name,
// the rest as counted padding at their positions.
type physicalDeviceVulkan11Features struct {
	sType                         uint32
	pNext                         uintptr
	_                             [7]uint32 // 16-bit storage, multiview
	variablePointersStorageBuffer uint32
	_                             [4]uint32 // variablePointers, protected memory, YCbCr, draw parameters
}

type physicalDeviceVulkan12Features struct {
	sType                        uint32
	pNext                        uintptr
	_                            [2]uint32 // mirror clamp, indirect count
	storageBuffer8BitAccess      uint32
	_                            [34]uint32 // the rest of 8-bit storage through host query reset
	timelineSemaphore            uint32
	_                            [3]uint32 // buffer device address
	vulkanMemoryModel            uint32
	vulkanMemoryModelDeviceScope uint32
	_                            [4]uint32 // availability chains, viewport index, layer, broadcast
}

type physicalDeviceVulkan13Features struct {
	sType            uint32
	pNext            uintptr
	_                [9]uint32 // robust images, inline uniform blocks, cache control, private data, demote, terminate, subgroup size control
	synchronization2 uint32
	_                [5]uint32 // ASTC HDR, zero-initialized workgroup memory, dynamic rendering, dot product, maintenance4
}

type queueFamilyProperties struct {
	queueFlags                  uint32
	queueCount                  uint32
	timestampValidBits          uint32
	minImageTransferGranularity [3]uint32
}

type physicalDeviceLimits struct {
	maxImageDimension1D                             uint32
	maxImageDimension2D                             uint32
	maxImageDimension3D                             uint32
	maxImageDimensionCube                           uint32
	maxImageArrayLayers                             uint32
	maxTexelBufferElements                          uint32
	maxUniformBufferRange                           uint32
	maxStorageBufferRange                           uint32
	maxPushConstantsSize                            uint32
	maxMemoryAllocationCount                        uint32
	maxSamplerAllocationCount                       uint32
	bufferImageGranularity                          uint64
	sparseAddressSpaceSize                          uint64
	maxBoundDescriptorSets                          uint32
	maxPerStageDescriptorSamplers                   uint32
	maxPerStageDescriptorUniformBuffers             uint32
	maxPerStageDescriptorStorageBuffers             uint32
	maxPerStageDescriptorSampledImages              uint32
	maxPerStageDescriptorStorageImages              uint32
	maxPerStageDescriptorInputAttachments           uint32
	maxPerStageResources                            uint32
	maxDescriptorSetSamplers                        uint32
	maxDescriptorSetUniformBuffers                  uint32
	maxDescriptorSetUniformBuffersDynamic           uint32
	maxDescriptorSetStorageBuffers                  uint32
	maxDescriptorSetStorageBuffersDynamic           uint32
	maxDescriptorSetSampledImages                   uint32
	maxDescriptorSetStorageImages                   uint32
	maxDescriptorSetInputAttachments                uint32
	maxVertexInputAttributes                        uint32
	maxVertexInputBindings                          uint32
	maxVertexInputAttributeOffset                   uint32
	maxVertexInputBindingStride                     uint32
	maxVertexOutputComponents                       uint32
	maxTessellationGenerationLevel                  uint32
	maxTessellationPatchSize                        uint32
	maxTessellationControlPerVertexInputComponents  uint32
	maxTessellationControlPerVertexOutputComponents uint32
	maxTessellationControlPerPatchOutputComponents  uint32
	maxTessellationControlTotalOutputComponents     uint32
	maxTessellationEvaluationInputComponents        uint32
	maxTessellationEvaluationOutputComponents       uint32
	maxGeometryShaderInvocations                    uint32
	maxGeometryInputComponents                      uint32
	maxGeometryOutputComponents                     uint32
	maxGeometryOutputVertices                       uint32
	maxGeometryTotalOutputComponents                uint32
	maxFragmentInputComponents                      uint32
	maxFragmentOutputAttachments                    uint32
	maxFragmentDualSrcAttachments                   uint32
	maxFragmentCombinedOutputResources              uint32
	maxComputeSharedMemorySize                      uint32
	maxComputeWorkGroupCount                        [3]uint32
	maxComputeWorkGroupInvocations                  uint32
	maxComputeWorkGroupSize                         [3]uint32
	subPixelPrecisionBits                           uint32
	subTexelPrecisionBits                           uint32
	mipmapPrecisionBits                             uint32
	maxDrawIndexedIndexValue                        uint32
	maxDrawIndirectCount                            uint32
	maxSamplerLodBias                               float32
	maxSamplerAnisotropy                            float32
	maxViewports                                    uint32
	maxViewportDimensions                           [2]uint32
	viewportBoundsRange                             [2]float32
	viewportSubPixelBits                            uint32
	minMemoryMapAlignment                           uintptr
	minTexelBufferOffsetAlignment                   uint64
	minUniformBufferOffsetAlignment                 uint64
	minStorageBufferOffsetAlignment                 uint64
	minTexelOffset                                  int32
	maxTexelOffset                                  uint32
	minTexelGatherOffset                            int32
	maxTexelGatherOffset                            uint32
	minInterpolationOffset                          float32
	maxInterpolationOffset                          float32
	subPixelInterpolationOffsetBits                 uint32
	maxFramebufferWidth                             uint32
	maxFramebufferHeight                            uint32
	maxFramebufferLayers                            uint32
	framebufferColorSampleCounts                    uint32
	framebufferDepthSampleCounts                    uint32
	framebufferStencilSampleCounts                  uint32
	framebufferNoAttachmentsSampleCounts            uint32
	maxColorAttachments                             uint32
	sampledImageColorSampleCounts                   uint32
	sampledImageIntegerSampleCounts                 uint32
	sampledImageDepthSampleCounts                   uint32
	sampledImageStencilSampleCounts                 uint32
	storageImageSampleCounts                        uint32
	maxSampleMaskWords                              uint32
	timestampComputeAndGraphics                     uint32
	timestampPeriod                                 float32
	maxClipDistances                                uint32
	maxCullDistances                                uint32
	maxCombinedClipAndCullDistances                 uint32
	discreteQueuePriorities                         uint32
	pointSizeRange                                  [2]float32
	lineWidthRange                                  [2]float32
	pointSizeGranularity                            float32
	lineWidthGranularity                            float32
	strictLines                                     uint32
	standardSampleLocations                         uint32
	optimalBufferCopyOffsetAlignment                uint64
	optimalBufferCopyRowPitchAlignment              uint64
	nonCoherentAtomSize                             uint64
}

type physicalDeviceProperties struct {
	apiVersion        uint32
	driverVersion     uint32
	vendorID          uint32
	deviceID          uint32
	deviceType        uint32
	deviceName        [maxPhysicalDeviceName]byte
	pipelineCacheUUID [16]byte
	limits            physicalDeviceLimits
	sparse            [physicalDeviceSparseWordCount]uint32
}

type memoryType struct {
	propertyFlags uint32
	heapIndex     uint32
}

type memoryHeap struct {
	size  uint64
	flags uint32
	_     uint32
}

type physicalDeviceMemoryProperties struct {
	memoryTypeCount uint32
	memoryTypes     [maxMemoryTypes]memoryType
	memoryHeapCount uint32
	memoryHeaps     [maxMemoryHeaps]memoryHeap
}

type extensionProperties struct {
	extensionName [maxExtensionName]byte
	specVersion   uint32
}

type bufferCreateInfo struct {
	sType                 uint32
	pNext                 uintptr
	flags                 uint32
	size                  uint64
	usage                 uint32
	sharingMode           uint32
	queueFamilyIndexCount uint32
	pQueueFamilyIndices   uintptr
}

type memoryRequirements struct {
	size           uint64
	alignment      uint64
	memoryTypeBits uint32
}

type memoryAllocateInfo struct {
	sType           uint32
	pNext           uintptr
	allocationSize  uint64
	memoryTypeIndex uint32
}

type shaderModuleCreateInfo struct {
	sType    uint32
	pNext    uintptr
	flags    uint32
	codeSize uintptr
	pCode    uintptr
}

type descriptorSetLayoutBinding struct {
	binding            uint32
	descriptorType     uint32
	descriptorCount    uint32
	stageFlags         uint32
	pImmutableSamplers uintptr
}

type descriptorSetLayoutCreateInfo struct {
	sType        uint32
	pNext        uintptr
	flags        uint32
	bindingCount uint32
	pBindings    uintptr
}

type pushConstantRange struct {
	stageFlags uint32
	offset     uint32
	size       uint32
}

type pipelineLayoutCreateInfo struct {
	sType                  uint32
	pNext                  uintptr
	flags                  uint32
	setLayoutCount         uint32
	pSetLayouts            uintptr
	pushConstantRangeCount uint32
	pPushConstantRanges    uintptr
}

type pipelineShaderStageCreateInfo struct {
	sType               uint32
	pNext               uintptr
	flags               uint32
	stage               uint32
	module              uint64
	pName               uintptr
	pSpecializationInfo uintptr
}

type computePipelineCreateInfo struct {
	sType              uint32
	pNext              uintptr
	flags              uint32
	stage              pipelineShaderStageCreateInfo
	layout             uint64
	basePipelineHandle uint64
	basePipelineIndex  int32
}

type commandPoolCreateInfo struct {
	sType            uint32
	pNext            uintptr
	flags            uint32
	queueFamilyIndex uint32
}

type commandBufferAllocateInfo struct {
	sType              uint32
	pNext              uintptr
	commandPool        uint64
	level              uint32
	commandBufferCount uint32
}

type commandBufferBeginInfo struct {
	sType            uint32
	pNext            uintptr
	flags            uint32
	pInheritanceInfo uintptr
}

type semaphoreCreateInfo struct {
	sType uint32
	pNext uintptr
	flags uint32
}

type semaphoreTypeCreateInfo struct {
	sType         uint32
	pNext         uintptr
	semaphoreType uint32
	initialValue  uint64
}

type semaphoreWaitInfo struct {
	sType          uint32
	pNext          uintptr
	flags          uint32
	semaphoreCount uint32
	pSemaphores    uintptr
	pValues        uintptr
}

type semaphoreSignalInfo struct {
	sType     uint32
	pNext     uintptr
	semaphore uint64
	value     uint64
}

type submitInfo2 struct {
	sType                    uint32
	pNext                    uintptr
	flags                    uint32
	waitSemaphoreInfoCount   uint32
	pWaitSemaphoreInfos      uintptr
	commandBufferInfoCount   uint32
	pCommandBufferInfos      uintptr
	signalSemaphoreInfoCount uint32
	pSignalSemaphoreInfos    uintptr
}

type semaphoreSubmitInfo struct {
	sType       uint32
	pNext       uintptr
	semaphore   uint64
	value       uint64
	stageMask   uint64
	deviceIndex uint32
}

type commandBufferSubmitInfo struct {
	sType         uint32
	pNext         uintptr
	commandBuffer uintptr
	deviceMask    uint32
}

type memoryBarrier2 struct {
	sType         uint32
	pNext         uintptr
	srcStageMask  uint64
	srcAccessMask uint64
	dstStageMask  uint64
	dstAccessMask uint64
}

type dependencyInfo struct {
	sType                    uint32
	pNext                    uintptr
	dependencyFlags          uint32
	memoryBarrierCount       uint32
	pMemoryBarriers          uintptr
	bufferMemoryBarrierCount uint32
	pBufferMemoryBarriers    uintptr
	imageMemoryBarrierCount  uint32
	pImageMemoryBarriers     uintptr
}

type writeDescriptorSet struct {
	sType            uint32
	pNext            uintptr
	dstSet           uint64
	dstBinding       uint32
	dstArrayElement  uint32
	descriptorCount  uint32
	descriptorType   uint32
	pImageInfo       uintptr
	pBufferInfo      uintptr
	pTexelBufferView uintptr
}

type bufferCopy struct {
	srcOffset uint64
	dstOffset uint64
	size      uint64
}

type descriptorBufferInfo struct {
	buffer uint64
	offset uint64
	rng    uint64
}

// The C strings the calls take, as NUL-terminated package memory
// whose addresses stay valid. Push descriptors are the one extension
// the device enables: everything else the shaders and the device use
// is core at Vulkan 1.3.
var (
	cMain           = []byte("main\x00")
	cPushDescriptor = []byte("VK_KHR_push_descriptor\x00")
)

func cstr(b []byte) uintptr { return uintptr(unsafe.Pointer(&b[0])) }

// procs is the function table: the global and instance functions
// resolved through vkGetInstanceProcAddr, the device functions
// through vkGetDeviceProcAddr so the per-frame calls bypass the
// loader's trampolines.
type procs struct {
	enumerateInstanceVersion uintptr
	createInstance           uintptr

	destroyInstance                        uintptr
	enumeratePhysicalDevices               uintptr
	getPhysicalDeviceProperties            uintptr
	getPhysicalDeviceFeatures2             uintptr
	getPhysicalDeviceQueueFamilyProperties uintptr
	getPhysicalDeviceMemoryProperties      uintptr
	enumerateDeviceExtensionProperties     uintptr
	createDevice                           uintptr
	getDeviceProcAddr                      uintptr

	destroyDevice               uintptr
	getDeviceQueue              uintptr
	createShaderModule          uintptr
	destroyShaderModule         uintptr
	createDescriptorSetLayout   uintptr
	destroyDescriptorSetLayout  uintptr
	createPipelineLayout        uintptr
	destroyPipelineLayout       uintptr
	createComputePipelines      uintptr
	destroyPipeline             uintptr
	createBuffer                uintptr
	destroyBuffer               uintptr
	getBufferMemoryRequirements uintptr
	allocateMemory              uintptr
	freeMemory                  uintptr
	bindBufferMemory            uintptr
	mapMemory                   uintptr
	unmapMemory                 uintptr
	createCommandPool           uintptr
	destroyCommandPool          uintptr
	allocateCommandBuffers      uintptr
	createSemaphore             uintptr
	destroySemaphore            uintptr
	waitSemaphores              uintptr
	signalSemaphore             uintptr
	beginCommandBuffer          uintptr
	endCommandBuffer            uintptr
	cmdBindPipeline             uintptr
	cmdPushDescriptorSet        uintptr
	cmdPushConstants            uintptr
	cmdDispatch                 uintptr
	cmdCopyBuffer               uintptr
	cmdFillBuffer               uintptr
	cmdPipelineBarrier2         uintptr
	queueSubmit2                uintptr
}

// sym pairs a table slot with the function's name.
type sym struct {
	dst  *uintptr
	name string
}

func (p *procs) globalSyms() []sym {
	return []sym{{&p.enumerateInstanceVersion, "vkEnumerateInstanceVersion"}, {&p.createInstance, "vkCreateInstance"}}
}

func (p *procs) instanceSyms() []sym {
	return []sym{
		{&p.destroyInstance, "vkDestroyInstance"},
		{&p.enumeratePhysicalDevices, "vkEnumeratePhysicalDevices"},
		{&p.getPhysicalDeviceProperties, "vkGetPhysicalDeviceProperties"},
		{&p.getPhysicalDeviceFeatures2, "vkGetPhysicalDeviceFeatures2"},
		{&p.getPhysicalDeviceQueueFamilyProperties, "vkGetPhysicalDeviceQueueFamilyProperties"},
		{&p.getPhysicalDeviceMemoryProperties, "vkGetPhysicalDeviceMemoryProperties"},
		{&p.enumerateDeviceExtensionProperties, "vkEnumerateDeviceExtensionProperties"},
		{&p.createDevice, "vkCreateDevice"},
		{&p.getDeviceProcAddr, "vkGetDeviceProcAddr"},
	}
}

func (p *procs) deviceSyms() []sym {
	return []sym{
		{&p.destroyDevice, "vkDestroyDevice"},
		{&p.getDeviceQueue, "vkGetDeviceQueue"},
		{&p.createShaderModule, "vkCreateShaderModule"},
		{&p.destroyShaderModule, "vkDestroyShaderModule"},
		{&p.createDescriptorSetLayout, "vkCreateDescriptorSetLayout"},
		{&p.destroyDescriptorSetLayout, "vkDestroyDescriptorSetLayout"},
		{&p.createPipelineLayout, "vkCreatePipelineLayout"},
		{&p.destroyPipelineLayout, "vkDestroyPipelineLayout"},
		{&p.createComputePipelines, "vkCreateComputePipelines"},
		{&p.destroyPipeline, "vkDestroyPipeline"},
		{&p.createBuffer, "vkCreateBuffer"},
		{&p.destroyBuffer, "vkDestroyBuffer"},
		{&p.getBufferMemoryRequirements, "vkGetBufferMemoryRequirements"},
		{&p.allocateMemory, "vkAllocateMemory"},
		{&p.freeMemory, "vkFreeMemory"},
		{&p.bindBufferMemory, "vkBindBufferMemory"},
		{&p.mapMemory, "vkMapMemory"},
		{&p.unmapMemory, "vkUnmapMemory"},
		{&p.createCommandPool, "vkCreateCommandPool"},
		{&p.destroyCommandPool, "vkDestroyCommandPool"},
		{&p.allocateCommandBuffers, "vkAllocateCommandBuffers"},
		{&p.createSemaphore, "vkCreateSemaphore"},
		{&p.destroySemaphore, "vkDestroySemaphore"},
		{&p.waitSemaphores, "vkWaitSemaphores"},
		{&p.signalSemaphore, "vkSignalSemaphore"},
		{&p.beginCommandBuffer, "vkBeginCommandBuffer"},
		{&p.endCommandBuffer, "vkEndCommandBuffer"},
		{&p.cmdBindPipeline, "vkCmdBindPipeline"},
		{&p.cmdPushDescriptorSet, "vkCmdPushDescriptorSetKHR"},
		{&p.cmdPushConstants, "vkCmdPushConstants"},
		{&p.cmdDispatch, "vkCmdDispatch"},
		{&p.cmdCopyBuffer, "vkCmdCopyBuffer"},
		{&p.cmdFillBuffer, "vkCmdFillBuffer"},
		{&p.cmdPipelineBarrier2, "vkCmdPipelineBarrier2"},
		{&p.queueSubmit2, "vkQueueSubmit2"},
	}
}

// resolve fills a table's slots through a getter, an instance or
// device procedure address function, for the object. A name the
// getter does not know is an error. The name's address is converted
// in the call's own argument list, which the call's escape rule
// takes as a pointer: the bytes are placed where a stack move cannot
// reach them and held live until the loader has read them. Converted
// anywhere else, a short name sits in this frame, and a stack growth
// during the call's prologue would leave the loader reading the old
// frame.
func resolve(getter uintptr, object uintptr, syms []sym) error {
	for _, s := range syms {
		name := []byte(s.name + "\x00")
		r, _, _ := purego.SyscallN(getter, object, uintptr(unsafe.Pointer(&name[0])))
		if r == 0 {
			return fmt.Errorf("no %s", s.name)
		}
		*s.dst = r
	}
	return nil
}

// argSlots is the argument array's size: the most arguments any call
// here passes.
const argSlots = 10

// call calls a function on the argument array, which is retained
// memory so the call allocates nothing. The array holds the most
// arguments any call here passes (argSlots).
func call(slots *[argSlots]uintptr, fn uintptr, args ...uintptr) uintptr {
	n := copy(slots[:], args)
	r, _, _ := purego.SyscallN(fn, slots[:n]...)
	return r
}

// vkerr turns a result into an error, the code in decimal as the
// header lists it.
func vkerr(r uintptr, what string) error {
	if int32(r) < 0 {
		return fmt.Errorf("%s: %d", what, int32(r))
	}
	return nil
}
