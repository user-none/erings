// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package backend

import (
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/metal"
)

// names lists Metal, the platform's one backend.
func names() []string { return []string{Metal} }

// open opens the named backend, which can only be Metal.
func open(name string) (hostgpu.Device, error) {
	if name != Metal {
		return nil, unknown(name)
	}
	return metal.Open()
}
