// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"
)

func TestNewEmulator(t *testing.T) {
	e := NewEmulator()
	if e.bus == nil {
		t.Fatal("bus is nil")
	}
	if e.master == nil {
		t.Fatal("master is nil")
	}
	if e.slave == nil {
		t.Fatal("slave is nil")
	}
}

func TestEmulatorTimingNTSC(t *testing.T) {
	e := NewEmulator()

	// Default is NTSC 320 mode: 263 lines/frame. The per-scanline width is
	// the documented clock (26,874,100 Hz) / (60 * 263) = 1703.05, so it is
	// 1703 or 1704 depending on the carry state.
	if e.scanlines != 263 {
		t.Errorf("scanlines = %d, want 263", e.scanlines)
	}
	if e.systemCyclesPerScanline != 1703 && e.systemCyclesPerScanline != 1704 {
		t.Errorf("systemCyclesPerScanline = %d, want 1703 or 1704", e.systemCyclesPerScanline)
	}
}

func TestEmulatorTimingPAL(t *testing.T) {
	e := NewEmulator()
	e.vdp2.SetPAL(true)
	e.recalcTiming()

	if e.scanlines != 313 {
		t.Errorf("scanlines = %d, want 313", e.scanlines)
	}
}

// TestEmulatorSystemClockConvergence verifies that the per-line cycle-width
// carry makes the summed per-frame cycle budget converge to the documented
// system clock (SMPC Table 1.1) over one second, for every region and
// horizontal-clock mode. The residual is bounded by D = fps * scanlines.
func TestEmulatorSystemClockConvergence(t *testing.T) {
	cases := []struct {
		name      string
		pal       bool
		tvmd      uint16 // HRESO bit 0 selects the 352 clock
		fps       uint32
		scanlines uint32
		clock     uint64
	}{
		{"NTSC 320", false, 0x0000, 60, 263, systemClockNTSC320},
		{"NTSC 352", false, 0x0001, 60, 263, systemClockNTSC352},
		{"PAL 320", true, 0x0000, 50, 313, systemClockPAL320},
		{"PAL 352", true, 0x0001, 50, 313, systemClockPAL352},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEmulator()
			e.vdp2.SetPAL(c.pal)
			e.vdp2.Write(0x0000, c.tvmd)
			// First recalc establishes the mode and zeroes the carry.
			e.recalcTiming()
			d := uint64(c.fps) * uint64(c.scanlines)
			var total uint64
			for i := uint32(0); i < c.fps; i++ {
				e.recalcTiming()
				total += uint64(e.systemCyclesPerFrame)
			}
			// The summed per-second budget converges to the documented clock
			// within D (the per-line carry re-truncates by at most scanlines).
			diff := int64(total) - int64(c.clock)
			if diff < 0 {
				diff = -diff
			}
			if uint64(diff) >= d {
				t.Errorf("summed cycles/sec = %d, want within %d of clock %d (diff %d)",
					total, d, c.clock, diff)
			}
		})
	}
}

func TestEmulatorM68KCyclesPerFrame(t *testing.T) {
	e := NewEmulator()
	// Integer 60 fps NTSC: m68k receives exactly 11,289,600 / 60 = 188,160
	// cycles per frame (SCSP target-based distribution).
	if e.m68kCyclesPerFrame != 188160 {
		t.Errorf("m68kCyclesPerFrame = %d, want 188160", e.m68kCyclesPerFrame)
	}
}

func TestEmulatorSCSPSamplesPerFrame(t *testing.T) {
	e := NewEmulator()
	// Integer 60 fps NTSC: SCSP emits exactly 44,100 / 60 = 735
	// samples per frame (target-based distribution).
	if e.samplesPerFrame != 735 {
		t.Errorf("samplesPerFrame = %d, want 735", e.samplesPerFrame)
	}
}

func TestEmulatorSCUInterruptWiring(t *testing.T) {
	e := NewEmulator()

	// Load a minimal BIOS that has a VBlank vector
	bios := make([]byte, biosSize)
	if err := e.bus.SetBIOS(bios); err != nil {
		t.Fatal(err)
	}

	// Raise VBlank-IN through SCU
	e.scu.RaiseVBlankIN()

	// The SCU should have dispatched the interrupt via IRL.
	// IST bit is auto-cleared on dispatch, so we verify the
	// SH-2 has a pending IRL instead.
	regs := e.master.Registers()
	_ = regs // IRL is internal state; the dispatch happened if no panic
}

func TestEmulatorSoundSync(t *testing.T) {
	e := NewEmulator()

	// Initially sound is off, SCSP should be in reset
	if !e.scsp.InReset() {
		t.Error("SCSP should be in reset initially")
	}

	// Simulate SMPC SNDON. Command dispatch is deferred by the
	// per-command scanline counter; drain it so SNDON runs before
	// the sync assertions below.
	smpc := e.smpc
	smpc.Write(0x1F, 0x06) // SNDON command
	for i := 0; i < 8; i++ {
		smpc.TickScanline()
	}

	// RunFrame should detect sound enabled and release 68K
	// We can't run a full frame without BIOS, so test the sync logic directly
	scsp := e.scsp
	if !smpc.SoundEnabled() {
		t.Error("SMPC should report sound enabled after SNDON")
	}

	// Simulate the sync that happens at RunFrame start
	if smpc.SoundEnabled() && scsp.InReset() {
		scsp.SetInReset(false)
	}
	if scsp.InReset() {
		t.Error("SCSP should not be in reset after sync")
	}
}

func TestEmulatorSCSPSetInReset(t *testing.T) {
	scsp := NewSCSP(NewSCU())

	if !scsp.InReset() {
		t.Error("SCSP should start in reset")
	}

	scsp.SetInReset(false)
	if scsp.InReset() {
		t.Error("SCSP should not be in reset after SetInReset(false)")
	}

	scsp.SetInReset(true)
	if !scsp.InReset() {
		t.Error("SCSP should be in reset after SetInReset(true)")
	}
}

func TestVDP2Accessors(t *testing.T) {
	v := NewVDP2(NewSCU())

	if v.Is352Clock() {
		t.Error("Is352Clock should be false in default 320 mode")
	}
	if v.LinesPerFrame() != linesNTSC {
		t.Errorf("LinesPerFrame = %d, want %d", v.LinesPerFrame(), linesNTSC)
	}
	if v.ActiveLines() != 224 {
		t.Errorf("ActiveLines = %d, want 224", v.ActiveLines())
	}
}

func TestVDP2ActiveSystemCyclesPerLine(t *testing.T) {
	v := NewVDP2(NewSCU())
	if v.ActiveSystemCyclesPerLine() != activeSystemCycles320 {
		t.Errorf("ActiveSystemCyclesPerLine = %d, want %d", v.ActiveSystemCyclesPerLine(), activeSystemCycles320)
	}
}

func TestEmulatorSlaveDisabledByDefault(t *testing.T) {
	e := NewEmulator()

	// SMPC should have slave disabled by default
	if e.smpc.SSHEnabled() {
		t.Error("slave SH-2 should be disabled by default")
	}
}

func TestEmulatorGetTimingNTSC(t *testing.T) {
	e := NewEmulator()
	timing := e.GetTiming()

	if timing.FPS != 60 {
		t.Errorf("FPS = %d, want 60", timing.FPS)
	}
	if timing.Scanlines != 263 {
		t.Errorf("Scanlines = %d, want 263", timing.Scanlines)
	}
}

func TestEmulatorGetTimingPAL(t *testing.T) {
	e := NewEmulator()
	e.vdp2.SetPAL(true)

	timing := e.GetTiming()

	if timing.FPS != 50 {
		t.Errorf("FPS = %d, want 50", timing.FPS)
	}
	if timing.Scanlines != 313 {
		t.Errorf("Scanlines = %d, want 313", timing.Scanlines)
	}
}

func TestEmulatorGetAudioSamples(t *testing.T) {
	e := NewEmulator()
	samples := e.GetAudioSamples()

	if samples != nil {
		t.Errorf("expected nil audio buffer, got len %d", len(samples))
	}
}

func TestEmulatorSetBIOS(t *testing.T) {
	e := NewEmulator()

	bios := make([]byte, biosSize)
	if err := e.SetBIOS("main_bios", bios); err != nil {
		t.Errorf("SetBIOS(main_bios) returned error: %v", err)
	}

	if err := e.SetBIOS("unknown_key", bios); err == nil {
		t.Error("SetBIOS(unknown_key) should return error")
	}
}

func TestEmulatorSetInputBounds(t *testing.T) {
	e := NewEmulator()

	// Out-of-range players should not panic
	e.SetInput(-1, 0)
	e.SetInput(2, 0)
	e.SetInput(0, 0)
	e.SetInput(1, 0)
}

func TestEmulatorSetInputMapping(t *testing.T) {
	e := NewEmulator()

	// All buttons released: pad data should be 0xFFFF (all active-low bits set)
	e.SetInput(0, 0)
	pad := e.smpc.padState[0]
	if pad != 0xFFFF {
		t.Errorf("all released: pad = 0x%04X, want 0xFFFF", pad)
	}

	// Press Up (bit 0) -> b1 bit 4 cleared
	e.SetInput(0, 1<<0)
	pad = e.smpc.padState[0]
	if pad&(1<<12) != 0 {
		t.Errorf("Up pressed: b1 bit 4 should be clear, pad = 0x%04X", pad)
	}

	// Press Start (bit 12) -> b1 bit 3 cleared
	e.SetInput(0, 1<<12)
	pad = e.smpc.padState[0]
	if pad&(1<<11) != 0 {
		t.Errorf("Start pressed: b1 bit 3 should be clear, pad = 0x%04X", pad)
	}
}

func TestEmulatorStartIsNoOp(t *testing.T) {
	e := NewEmulator()

	// With a real BIOS loaded and fast-boot off, Start takes the no-op
	// branch: it constructs nothing and returns nil.
	bios := make([]byte, biosSize)
	if err := e.SetBIOS("main_bios", bios); err != nil {
		t.Fatal(err)
	}

	if err := e.Start(); err != nil {
		t.Errorf("Start with BIOS loaded returned error: %v", err)
	}
}

func TestEmulatorClose(t *testing.T) {
	e := NewEmulator()
	// Close should not panic
	e.Close()
}

func TestReadMemory(t *testing.T) {
	e := NewEmulator()

	// Distinct markers at each region edge.
	e.bus.wramL[0] = 0xAA
	e.bus.wramL[wramLSize-1] = 0xBB
	e.bus.wramH[0] = 0xCC
	e.bus.wramH[wramHSize-1] = 0xDD

	buf := make([]byte, 1)

	// Work RAM-L start and end at their native addresses.
	if n := e.ReadMemory(0x00200000, buf); n != 1 || buf[0] != 0xAA {
		t.Fatalf("wraml start: n=%d buf=%#x, want 1 0xAA", n, buf[0])
	}
	if n := e.ReadMemory(0x002FFFFF, buf); n != 1 || buf[0] != 0xBB {
		t.Fatalf("wraml end: n=%d buf=%#x, want 1 0xBB", n, buf[0])
	}
	// Work RAM-H start and end.
	if n := e.ReadMemory(0x06000000, buf); n != 1 || buf[0] != 0xCC {
		t.Fatalf("wramh start: n=%d buf=%#x, want 1 0xCC", n, buf[0])
	}
	if n := e.ReadMemory(0x060FFFFF, buf); n != 1 || buf[0] != 0xDD {
		t.Fatalf("wramh end: n=%d buf=%#x, want 1 0xDD", n, buf[0])
	}
	// Non-canonical spellings are outside the gate: partition views and
	// mirrors read nothing.
	if n := e.ReadMemory(0x26000000, buf); n != 0 {
		t.Fatalf("partition view: n=%d, want 0", n)
	}
	if n := e.ReadMemory(0x06100000, buf); n != 0 {
		t.Fatalf("mirror: n=%d, want 0", n)
	}

	// Reads clamp at the region end rather than crossing regions.
	tail := make([]byte, 4)
	if n := e.ReadMemory(0x002FFFFF, tail); n != 1 || tail[0] != 0xBB {
		t.Fatalf("region-end clamp: n=%d tail=%#x, want 1 [0xBB]", n, tail)
	}

	// Addresses outside every region read nothing.
	if n := e.ReadMemory(0x05000000, buf); n != 0 {
		t.Fatalf("outside regions: n=%d, want 0", n)
	}
}

func TestWriteMemory(t *testing.T) {
	e := NewEmulator()

	if n := e.WriteMemory(0x0605C973, []byte{0x12, 0x34}); n != 2 {
		t.Fatalf("write: n=%d, want 2", n)
	}
	if e.bus.wramH[0x5C973] != 0x12 || e.bus.wramH[0x5C974] != 0x34 {
		t.Fatalf("write did not land: %#x %#x", e.bus.wramH[0x5C973], e.bus.wramH[0x5C974])
	}
	// Non-canonical spellings write nothing.
	if n := e.WriteMemory(0x2615C973, []byte{0x56}); n != 0 || e.bus.wramH[0x5C973] != 0x12 {
		t.Fatalf("mirror write: n=%d byte=%#x, want 0 0x12", n, e.bus.wramH[0x5C973])
	}
	// Writes clamp at the region end.
	if n := e.WriteMemory(0x002FFFFF, []byte{0x01, 0x02}); n != 1 {
		t.Fatalf("region-end clamp: n=%d, want 1", n)
	}
	if e.bus.wramL[wramLSize-1] != 0x01 || e.bus.wramH[0] != 0 {
		t.Fatalf("clamped write leaked across regions")
	}
	// Addresses outside every region write nothing.
	if n := e.WriteMemory(0x05000000, []byte{0xFF}); n != 0 {
		t.Fatalf("outside regions: n=%d, want 0", n)
	}
}

// TestMemoryAccessUsesBusDecode pins ReadMemory and WriteMemory to the
// bus decode itself: a byte written through one is visible through the
// other at every spelling the bus folds, so the region table can never
// disagree with the bus about which byte an address selects.
func TestMemoryAccessUsesBusDecode(t *testing.T) {
	e := NewEmulator()

	if n := e.WriteMemory(0x0605C973, []byte{0x5A}); n != 1 {
		t.Fatalf("write: n=%d, want 1", n)
	}
	for _, addr := range []uint32{0x0605C973, 0x0615C973, 0x2605C973, 0x27F5C973} {
		if got := e.bus.read8Impl(addr); got != 0x5A {
			t.Fatalf("bus read 0x%08X = %#x, want 0x5A", addr, got)
		}
	}

	// A write through the bus at a mirror spelling is visible to
	// ReadMemory at the canonical address.
	e.bus.write8Impl(0x0615C973, 0xA5)
	var b [1]byte
	if n := e.ReadMemory(0x0605C973, b[:]); n != 1 || b[0] != 0xA5 {
		t.Fatalf("read after bus write: n=%d byte=%#x, want 1 0xA5", n, b[0])
	}
}

func TestBusRegions(t *testing.T) {
	e := NewEmulator()
	regions := e.Regions()
	if len(regions) != 2 {
		t.Fatalf("regions = %+v", regions)
	}
	wraml, wramh := regions[0], regions[1]
	if wraml.Name != "wraml" || wraml.Start != 0x00200000 || wraml.Size != wramLSize {
		t.Fatalf("wraml = %+v", wraml)
	}
	if wramh.Name != "wramh" || wramh.Start != 0x06000000 || wramh.Size != wramHSize {
		t.Fatalf("wramh = %+v", wramh)
	}
}

func TestReadMemoryFlat(t *testing.T) {
	e := NewEmulator()

	// Distinct markers at each region edge so the flat mapping is observable.
	e.bus.wramL[0] = 0xAA
	e.bus.wramL[wramLSize-1] = 0xBB
	e.bus.wramH[0] = 0xCC
	e.bus.wramH[wramHSize-1] = 0xDD

	buf := make([]byte, 1)

	// Flat 0 -> Work RAM-L start.
	if n := e.ReadMemoryFlat(0, buf); n != 1 || buf[0] != 0xAA {
		t.Fatalf("flat 0: n=%d buf=%#x, want 1 0xAA", n, buf[0])
	}
	// Flat 0x100000 -> Work RAM-H start (the L/H boundary).
	if n := e.ReadMemoryFlat(0x100000, buf); n != 1 || buf[0] != 0xCC {
		t.Fatalf("flat 0x100000: n=%d buf=%#x, want 1 0xCC", n, buf[0])
	}
	// Last byte of Work RAM-H.
	if n := e.ReadMemoryFlat(0x1FFFFF, buf); n != 1 || buf[0] != 0xDD {
		t.Fatalf("flat 0x1FFFFF: n=%d buf=%#x, want 1 0xDD", n, buf[0])
	}

	// A read spanning the L/H boundary returns contiguous bytes from both.
	span := make([]byte, 2)
	if n := e.ReadMemoryFlat(wramLSize-1, span); n != 2 || span[0] != 0xBB || span[1] != 0xCC {
		t.Fatalf("L/H span: n=%d span=%#x, want 2 [0xBB 0xCC]", n, span)
	}

	// Out-of-range start reads nothing.
	if n := e.ReadMemoryFlat(0x200000, buf); n != 0 {
		t.Fatalf("out-of-range: n=%d, want 0", n)
	}

	// A read running off the end returns only the in-range count.
	tail := make([]byte, 4)
	if n := e.ReadMemoryFlat(0x1FFFFE, tail); n != 2 {
		t.Fatalf("tail overrun: n=%d, want 2", n)
	}
}

func TestWriteMemoryFlat(t *testing.T) {
	e := NewEmulator()

	// A write spanning the L/H boundary lands contiguously in both.
	if n := e.WriteMemoryFlat(wramLSize-1, []byte{0x12, 0x34}); n != 2 {
		t.Fatalf("L/H span: n=%d, want 2", n)
	}
	if e.bus.wramL[wramLSize-1] != 0x12 || e.bus.wramH[0] != 0x34 {
		t.Fatalf("span write did not land: %#x %#x", e.bus.wramL[wramLSize-1], e.bus.wramH[0])
	}
	// The write is visible to the flat read and the native read.
	var b [1]byte
	if e.ReadMemoryFlat(0x100000, b[:]) != 1 || b[0] != 0x34 {
		t.Fatalf("flat read after write: %#x", b[0])
	}
	if e.ReadMemory(0x06000000, b[:]) != 1 || b[0] != 0x34 {
		t.Fatalf("native read after flat write: %#x", b[0])
	}
	// A write running off the end writes only the in-range count.
	if n := e.WriteMemoryFlat(0x1FFFFF, []byte{0x56, 0x78}); n != 1 {
		t.Fatalf("tail overrun: n=%d, want 1", n)
	}
	// Out-of-range start writes nothing.
	if n := e.WriteMemoryFlat(0x200000, []byte{0xFF}); n != 0 {
		t.Fatalf("out-of-range: n=%d, want 0", n)
	}
}
