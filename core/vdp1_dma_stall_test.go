// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// TestVDP1SH2WriteStallChargedPerAccess verifies an SH-2 store to VDP1
// VRAM during an active draw is charged the per-access contention wait,
// scaled by transaction width, and nothing when no draw is active or the
// target is not VDP1 VRAM.
func TestVDP1SH2WriteStallChargedPerAccess(t *testing.T) {
	bus := newBusForTest()
	v := bus.vdp1
	const vram = uint32(0x05C00000)

	bus.SH2Write16(vram, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("SH2Write16 with no draw active: stall=%d, want 0", got)
	}

	v.drawActive = true

	v.vramWriteStallCycles.Store(0)
	bus.SH2Write16(vram, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != vdp1WriteStallSystemCycles {
		t.Fatalf("SH2Write16 during draw: stall=%d, want %d", got, vdp1WriteStallSystemCycles)
	}

	v.vramWriteStallCycles.Store(0)
	bus.SH2Write32(vram, 0xDEADBEEF, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 2*vdp1WriteStallSystemCycles {
		t.Fatalf("SH2Write32 during draw: stall=%d, want %d", got, 2*vdp1WriteStallSystemCycles)
	}

	v.vramWriteStallCycles.Store(0)
	bus.SH2Write16(0x06000000, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("SH2Write16 to work RAM during draw: stall=%d, want 0", got)
	}
}

// TestVDP1DMABurstChargedBurstRate verifies the DMA-class write path
// (DMAWrite*) charges a VDP1 VRAM write during a draw the continuous-burst
// rate, not the per-access SH-2 wait. A 32-bit write is two B-Bus words;
// the per-word rate accumulates to bytes/2 over a burst.
func TestVDP1DMABurstChargedBurstRate(t *testing.T) {
	bus := newBusForTest()
	v := bus.vdp1
	const vram = uint32(0x05C00000)

	bus.DMAWrite32(vram, 0xDEADBEEF)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("DMAWrite32 to VDP1 VRAM with no draw active: stall=%d, want 0", got)
	}

	v.drawActive = true

	v.vramWriteStallCycles.Store(0)
	bus.DMAWrite32(vram, 0xDEADBEEF)
	if got, want := v.vramWriteStallCycles.Load(), int32(2*vdp1DMABurstStallPerWord); got != want {
		t.Fatalf("DMAWrite32 burst stall: stall=%d, want %d", got, want)
	}

	// A full bytes-sized burst accumulates to bytes/2 system cycles.
	const words = 0x800
	v.vramWriteStallCycles.Store(0)
	for i := uint32(0); i < words; i++ {
		bus.DMAWrite32(vram+i*4, 0)
	}
	if got, want := v.vramWriteStallCycles.Load(), int32(words*4/2); got != want {
		t.Fatalf("burst of %d longwords: stall=%d, want %d (bytes/2)", words, got, want)
	}

	// Outside VDP1 VRAM: no charge.
	v.vramWriteStallCycles.Store(0)
	bus.DMAWrite32(0x06000000, 0)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("DMAWrite32 to work RAM during draw: stall=%d, want 0", got)
	}
}

// TestVDP1GenericWriteNotCharged verifies the generic bus write path
// (Bus.Write*) never charges VDP1 draw contention, even to VDP1 VRAM
// during a draw - contention is the business of the typed access-class
// entries (SH2Write*, DMAWrite*), not the generic path.
func TestVDP1GenericWriteNotCharged(t *testing.T) {
	bus := newBusForTest()
	v := bus.vdp1
	v.drawActive = true
	const vram = uint32(0x05C00000)

	v.vramWriteStallCycles.Store(0)
	bus.Write32(vram, 0xDEADBEEF)
	bus.Write16(vram, 0x1234)
	bus.Write8(vram, 0x55)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("generic Write* to VDP1 VRAM during draw: stall=%d, want 0", got)
	}
}
