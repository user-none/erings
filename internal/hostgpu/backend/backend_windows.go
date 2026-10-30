// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package backend

import (
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/dx12"
	"github.com/user-none/erings/internal/hostgpu/vulkan"
)

// names lists Direct3D 12 when the build embeds its compiled shaders
// (make dxil), then Vulkan, which compiles its shaders at run time.
func names() []string {
	if dx12.HasRendererShaders() {
		return []string{Direct3D12, Vulkan}
	}
	return []string{Vulkan}
}

// open opens the named backend: DX12 on the first hardware adapter
// with the renderer's embedded shaders, or Vulkan. A build without
// the DX12 shaders reports how to embed them when Direct3D 12 is
// opened by name.
func open(name string) (hostgpu.Device, error) {
	switch name {
	case Direct3D12:
		return dx12.Open(dx12.RendererShaders())
	case Vulkan:
		return vulkan.Open()
	}
	return nil, unknown(name)
}
