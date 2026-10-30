// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package backend

import (
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/vulkan"
)

// names lists Vulkan, the platform's one backend.
func names() []string { return []string{Vulkan} }

// open opens the named backend, which can only be Vulkan.
func open(name string) (hostgpu.Device, error) {
	if name != Vulkan {
		return nil, unknown(name)
	}
	return vulkan.Open()
}
