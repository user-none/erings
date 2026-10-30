// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux || windows

package vulkan

import (
	"errors"
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/devicetest"
)

// openDevice opens the device for a test, skipping when no usable
// Vulkan device is available, and closes it when the test ends.
func openDevice(tb testing.TB) *device {
	tb.Helper()
	dev, err := Open()
	if err != nil {
		if errors.Is(err, hostgpu.ErrUnavailable) {
			tb.Skip(err)
		}
		tb.Fatal(err)
	}
	tb.Cleanup(dev.Close)
	tb.Logf("device %s", dev.Name())
	return dev.(*device)
}

// TestRoundTrip runs devicetest.RoundTrip on the device.
func TestRoundTrip(t *testing.T) {
	devicetest.RoundTrip(t, openDevice(t))
}

// TestSubmissionsReleased runs devicetest.SubmissionsReleased on the
// device.
func TestSubmissionsReleased(t *testing.T) {
	devicetest.SubmissionsReleased(t, openDevice(t))
}

// TestCloseWhileEncoding runs devicetest.CloseWhileEncoding on the
// device, which openDevice's cleanup closes.
func TestCloseWhileEncoding(t *testing.T) {
	devicetest.CloseWhileEncoding(t, openDevice(t))
}

// TestEncodeAfterCommit runs devicetest.EncodeAfterCommit on the
// device.
func TestEncodeAfterCommit(t *testing.T) {
	devicetest.EncodeAfterCommit(t, openDevice(t))
}

// TestHostErrorReported runs devicetest.HostErrorReported on the
// device.
func TestHostErrorReported(t *testing.T) {
	devicetest.HostErrorReported(t, openDevice(t))
}

// TestCommandsNoAllocs runs devicetest.CommandsNoAllocs on the
// device.
func TestCommandsNoAllocs(t *testing.T) {
	devicetest.CommandsNoAllocs(t, openDevice(t))
}
