// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux || windows

package vulkan

import (
	"testing"
	"unsafe"
)

// TestStructSizes pins the mirrored structures to the sizes the
// header gives them on a 64-bit target.
func TestStructSizes(t *testing.T) {
	for _, s := range []struct {
		name string
		size uintptr
		want uintptr
	}{
		{"applicationInfo", unsafe.Sizeof(applicationInfo{}), 48},
		{"instanceCreateInfo", unsafe.Sizeof(instanceCreateInfo{}), 64},
		{"deviceQueueCreateInfo", unsafe.Sizeof(deviceQueueCreateInfo{}), 40},
		{"deviceCreateInfo", unsafe.Sizeof(deviceCreateInfo{}), 72},
		{"physicalDeviceFeatures2", unsafe.Sizeof(physicalDeviceFeatures2{}), 240},
		{"physicalDeviceVulkan11Features", unsafe.Sizeof(physicalDeviceVulkan11Features{}), 64},
		{"physicalDeviceVulkan12Features", unsafe.Sizeof(physicalDeviceVulkan12Features{}), 208},
		{"physicalDeviceVulkan13Features", unsafe.Sizeof(physicalDeviceVulkan13Features{}), 80},
		{"queueFamilyProperties", unsafe.Sizeof(queueFamilyProperties{}), 24},
		{"physicalDeviceLimits", unsafe.Sizeof(physicalDeviceLimits{}), 504},
		{"physicalDeviceProperties", unsafe.Sizeof(physicalDeviceProperties{}), 824},
		{"physicalDeviceMemoryProperties", unsafe.Sizeof(physicalDeviceMemoryProperties{}), 520},
		{"extensionProperties", unsafe.Sizeof(extensionProperties{}), 260},
		{"bufferCreateInfo", unsafe.Sizeof(bufferCreateInfo{}), 56},
		{"memoryRequirements", unsafe.Sizeof(memoryRequirements{}), 24},
		{"memoryAllocateInfo", unsafe.Sizeof(memoryAllocateInfo{}), 32},
		{"shaderModuleCreateInfo", unsafe.Sizeof(shaderModuleCreateInfo{}), 40},
		{"descriptorSetLayoutBinding", unsafe.Sizeof(descriptorSetLayoutBinding{}), 24},
		{"descriptorSetLayoutCreateInfo", unsafe.Sizeof(descriptorSetLayoutCreateInfo{}), 32},
		{"pushConstantRange", unsafe.Sizeof(pushConstantRange{}), 12},
		{"pipelineLayoutCreateInfo", unsafe.Sizeof(pipelineLayoutCreateInfo{}), 48},
		{"pipelineShaderStageCreateInfo", unsafe.Sizeof(pipelineShaderStageCreateInfo{}), 48},
		{"computePipelineCreateInfo", unsafe.Sizeof(computePipelineCreateInfo{}), 96},
		{"commandPoolCreateInfo", unsafe.Sizeof(commandPoolCreateInfo{}), 24},
		{"commandBufferAllocateInfo", unsafe.Sizeof(commandBufferAllocateInfo{}), 32},
		{"commandBufferBeginInfo", unsafe.Sizeof(commandBufferBeginInfo{}), 32},
		{"semaphoreCreateInfo", unsafe.Sizeof(semaphoreCreateInfo{}), 24},
		{"semaphoreTypeCreateInfo", unsafe.Sizeof(semaphoreTypeCreateInfo{}), 32},
		{"semaphoreWaitInfo", unsafe.Sizeof(semaphoreWaitInfo{}), 40},
		{"semaphoreSignalInfo", unsafe.Sizeof(semaphoreSignalInfo{}), 32},
		{"submitInfo2", unsafe.Sizeof(submitInfo2{}), 64},
		{"semaphoreSubmitInfo", unsafe.Sizeof(semaphoreSubmitInfo{}), 48},
		{"commandBufferSubmitInfo", unsafe.Sizeof(commandBufferSubmitInfo{}), 32},
		{"memoryBarrier2", unsafe.Sizeof(memoryBarrier2{}), 48},
		{"dependencyInfo", unsafe.Sizeof(dependencyInfo{}), 64},
		{"writeDescriptorSet", unsafe.Sizeof(writeDescriptorSet{}), 64},
		{"descriptorBufferInfo", unsafe.Sizeof(descriptorBufferInfo{}), 24},
		{"bufferCopy", unsafe.Sizeof(bufferCopy{}), 24},
	} {
		if s.size != s.want {
			t.Errorf("%s is %d bytes, want %d", s.name, s.size, s.want)
		}
	}
	var l physicalDeviceLimits
	for _, f := range []struct {
		name string
		off  uintptr
		want uintptr
	}{
		{"maxPushConstantsSize", unsafe.Offsetof(l.maxPushConstantsSize), 32},
		{"maxPerStageDescriptorStorageBuffers", unsafe.Offsetof(l.maxPerStageDescriptorStorageBuffers), 76},
		{"maxComputeWorkGroupCount", unsafe.Offsetof(l.maxComputeWorkGroupCount), 220},
		{"maxComputeWorkGroupInvocations", unsafe.Offsetof(l.maxComputeWorkGroupInvocations), 232},
		{"maxComputeWorkGroupSize", unsafe.Offsetof(l.maxComputeWorkGroupSize), 236},
		{"nonCoherentAtomSize", unsafe.Offsetof(l.nonCoherentAtomSize), 496},
	} {
		if f.off != f.want {
			t.Errorf("limits.%s at offset %d, want %d", f.name, f.off, f.want)
		}
	}
	var f11 physicalDeviceVulkan11Features
	var f12 physicalDeviceVulkan12Features
	var f13 physicalDeviceVulkan13Features
	for _, f := range []struct {
		name string
		off  uintptr
		want uintptr
	}{
		{"11.variablePointersStorageBuffer", unsafe.Offsetof(f11.variablePointersStorageBuffer), 44},
		{"12.storageBuffer8BitAccess", unsafe.Offsetof(f12.storageBuffer8BitAccess), 24},
		{"12.timelineSemaphore", unsafe.Offsetof(f12.timelineSemaphore), 164},
		{"12.vulkanMemoryModel", unsafe.Offsetof(f12.vulkanMemoryModel), 180},
		{"12.vulkanMemoryModelDeviceScope", unsafe.Offsetof(f12.vulkanMemoryModelDeviceScope), 184},
		{"13.synchronization2", unsafe.Offsetof(f13.synchronization2), 52},
	} {
		if f.off != f.want {
			t.Errorf("features %s at offset %d, want %d", f.name, f.off, f.want)
		}
	}
}
