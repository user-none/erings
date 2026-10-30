// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package backend opens one of the platform's backends by name and
// builds the compute sources on it.
package backend

import (
	"fmt"

	"github.com/user-none/erings/internal/hostgpu"
)

// The backends' names, as the renderer option and the overlay show
// them.
const (
	Direct3D12 = "Direct3D 12"
	Vulkan     = "Vulkan"
	Metal      = "Metal"
)

// Names lists the backends this build can open on the platform, the
// platform's default first. A backend the build cannot run, such as
// Direct3D 12 in a Windows build without its compiled shaders, is not
// listed.
func Names() []string { return names() }

// Open opens the named backend (open) and builds the compute sources
// on it. A name Names does not list is ErrUnavailable. A build failure
// closes the device.
func Open(name string, parent hostgpu.Source, subs ...hostgpu.Source) (*hostgpu.Program, error) {
	dev, err := open(name)
	if err != nil {
		return nil, err
	}
	prog, err := hostgpu.Build(dev, parent, subs...)
	if err != nil {
		dev.Close()
		return nil, err
	}
	return prog, nil
}

// unknown is the error for a backend name the platform does not list.
func unknown(name string) error {
	return fmt.Errorf("%w: no backend %q on this platform", hostgpu.ErrUnavailable, name)
}
