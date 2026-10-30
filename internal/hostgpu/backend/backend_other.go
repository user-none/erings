// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux && !windows

package backend

import "github.com/user-none/erings/internal/hostgpu"

// names lists nothing: the platform has no backend.
func names() []string { return nil }

// open reports ErrUnavailable: the platform has no backend.
func open(name string) (hostgpu.Device, error) {
	return nil, unknown(name)
}
