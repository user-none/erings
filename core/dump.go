// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "slices"

// MemoryDump is a point-in-time snapshot of every emulator memory
// region for debug inspection. Each field is an independent copy of
// the underlying bytes, safe to write to disk while the emulator
// continues running.
//
// BIOS is included so a dump captures the exact ROM image that was
// active. Disc image is intentionally excluded - discs are external
// and identified by hash, not state.
type MemoryDump struct {
	BIOS          []byte // 512 KB system BIOS
	WRAMH         []byte // 1 MB Work RAM-H
	WRAML         []byte // 1 MB Work RAM-L
	BackupRAM     []byte // 32 KB internal backup RAM
	ExtRAM        []byte // 4 MB Extended RAM Cartridge
	VDP1VRAM      []byte // 512 KB VDP1 command/character VRAM
	VDP1DrawFB    []byte // 256 KB VDP1 draw framebuffer
	VDP1DisplayFB []byte // 256 KB VDP1 display framebuffer
	VDP2VRAM      []byte // 512 KB VDP2 VRAM
	VDP2CRAM      []byte // 4 KB VDP2 color RAM
	SoundRAM      []byte // 512 KB SCSP sound RAM
}

// DumpMemory returns a deep-copy snapshot of every memory region.
// Safe to call between frames from the emulation goroutine.
func (e *Emulator) DumpMemory() MemoryDump {
	return MemoryDump{
		BIOS:          slices.Clone(e.bus.bios),
		WRAMH:         slices.Clone(e.bus.wramH),
		WRAML:         slices.Clone(e.bus.wramL),
		BackupRAM:     slices.Clone(e.bus.backup),
		ExtRAM:        slices.Clone(e.bus.extRAM),
		VDP1VRAM:      slices.Clone(e.vdp1.vram),
		VDP1DrawFB:    slices.Clone(e.vdp1.drawFB),
		VDP1DisplayFB: slices.Clone(e.vdp1.displayFB),
		VDP2VRAM:      slices.Clone(e.vdp2.vram),
		VDP2CRAM:      slices.Clone(e.vdp2.cram),
		SoundRAM:      slices.Clone(e.scsp.ram),
	}
}
