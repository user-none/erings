// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux || windows

package vulkan

import (
	"testing"

	"github.com/user-none/erings/internal/hostgpu/devicetest"
)

// TestConformance runs devicetest.Conformance on the device.
func TestConformance(t *testing.T) {
	devicetest.Conformance(t, openDevice(t))
}
