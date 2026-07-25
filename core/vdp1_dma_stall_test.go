// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// TestVDP1SH2WriteStallChargedPerAccess verifies an SH-2 access to
// VDP1 VRAM during an active draw stalls drawing by the per-word
// penalty, scaled by transaction width, for stores and loads alike.
// It charges nothing when no draw is active or the target is not
// VDP1 VRAM. The draw's cycles-run counter is set high so the
// per-draw cap stays out of these assertions - the cap itself is
// covered by TestVDP1SH2WriteStallBoundedByDrawLifetime.
func TestVDP1SH2WriteStallChargedPerAccess(t *testing.T) {
	bus := newBusForTest()
	v := bus.vdp1
	const vram = uint32(0x05C00000)

	bus.SH2Write16(vram, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("SH2Write16 with no draw active: stall=%d, want 0", got)
	}

	v.drawActive = true
	v.drawTicked.Store(1 << 20)

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
	bus.SH2Read16(vram, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != vdp1WriteStallSystemCycles {
		t.Fatalf("SH2Read16 during draw: stall=%d, want %d", got, vdp1WriteStallSystemCycles)
	}

	v.vramWriteStallCycles.Store(0)
	bus.SH2Read32(vram, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 2*vdp1WriteStallSystemCycles {
		t.Fatalf("SH2Read32 during draw: stall=%d, want %d", got, 2*vdp1WriteStallSystemCycles)
	}

	v.vramWriteStallCycles.Store(0)
	bus.SH2Write16(0x06000000, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("SH2Write16 to work RAM during draw: stall=%d, want 0", got)
	}
}

// TestVDP1SH2WriteStallBoundedByDrawLifetime verifies the per-draw
// cap: the stall charged to a draw cannot exceed the cycles it has
// run. A store stream against a draw with no elapsed time charges
// nothing. A partial remainder is consumed exactly, and it
// replenishes as the draw's cycles-run counter advances.
func TestVDP1SH2WriteStallBoundedByDrawLifetime(t *testing.T) {
	bus := newBusForTest()
	v := bus.vdp1
	const vram = uint32(0x05C00000)

	v.drawActive = true

	// Fresh draw, no elapsed time: nothing to take.
	bus.SH2Write16(vram, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 0 {
		t.Fatalf("write with zero draw lifetime: stall=%d, want 0", got)
	}

	// A remainder smaller than the full penalty clamps the charge.
	v.drawTicked.Store(10)
	bus.SH2Write16(vram, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 10 {
		t.Fatalf("write with 10 cycles remaining: stall=%d, want 10", got)
	}

	// Remainder used up: further writes charge nothing.
	bus.SH2Write16(vram, 0x1234, 0, false)
	if got := v.vramWriteStallCycles.Load(); got != 10 {
		t.Fatalf("write with remainder used up: stall=%d, want 10", got)
	}

	// Advancing the cycles-run counter replenishes the remainder.
	v.drawTicked.Add(1 << 20)
	bus.SH2Write16(vram, 0x1234, 0, false)
	if got, want := v.vramWriteStallCycles.Load(), int32(10+vdp1WriteStallSystemCycles); got != want {
		t.Fatalf("write after clock advance: stall=%d, want %d", got, want)
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
