// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package vulkan

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/user-none/erings/internal/hostgpu"
)

var (
	loadOnce            sync.Once
	loadErr             error
	getInstanceProcAddr uintptr
)

// load opens the loader from the system directory and resolves
// vkGetInstanceProcAddr, once. A missing loader is ErrUnavailable.
func load() error {
	loadOnce.Do(func() {
		h, err := windows.LoadLibraryEx("vulkan-1.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
		if err != nil {
			loadErr = fmt.Errorf("%w: %v", hostgpu.ErrUnavailable, err)
			return
		}
		if getInstanceProcAddr, err = windows.GetProcAddress(h, "vkGetInstanceProcAddr"); err != nil {
			loadErr = fmt.Errorf("%w: %v", hostgpu.ErrUnavailable, err)
		}
	})
	return loadErr
}
