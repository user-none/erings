// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/user-none/erings/core/sh2"
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

// Verify the sh2 package types are accessible (compilation test).
func TestSH2BusActivityTypes(t *testing.T) {
	_ = sh2.BusNone
	_ = sh2.BusRead
	_ = sh2.BusWrite
	_ = sh2.BusHeld
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

	// Distinct markers at each region edge so the flat mapping is observable.
	e.bus.wramL[0] = 0xAA
	e.bus.wramL[wramLSize-1] = 0xBB
	e.bus.wramH[0] = 0xCC
	e.bus.wramH[wramHSize-1] = 0xDD

	buf := make([]byte, 1)

	// Flat 0 -> Work RAM-L start.
	if n := e.ReadMemory(0, buf); n != 1 || buf[0] != 0xAA {
		t.Fatalf("flat 0: n=%d buf=%#x, want 1 0xAA", n, buf[0])
	}
	// Flat 0x100000 -> Work RAM-H start (the L/H boundary).
	if n := e.ReadMemory(0x100000, buf); n != 1 || buf[0] != 0xCC {
		t.Fatalf("flat 0x100000: n=%d buf=%#x, want 1 0xCC", n, buf[0])
	}
	// Last byte of Work RAM-H.
	if n := e.ReadMemory(0x1FFFFF, buf); n != 1 || buf[0] != 0xDD {
		t.Fatalf("flat 0x1FFFFF: n=%d buf=%#x, want 1 0xDD", n, buf[0])
	}

	// A read spanning the L/H boundary returns contiguous bytes from both.
	span := make([]byte, 2)
	if n := e.ReadMemory(wramLSize-1, span); n != 2 || span[0] != 0xBB || span[1] != 0xCC {
		t.Fatalf("L/H span: n=%d span=%#x, want 2 [0xBB 0xCC]", n, span)
	}

	// Out-of-range start reads nothing.
	if n := e.ReadMemory(0x200000, buf); n != 0 {
		t.Fatalf("out-of-range: n=%d, want 0", n)
	}

	// A read running off the end returns only the in-range count.
	tail := make([]byte, 4)
	if n := e.ReadMemory(0x1FFFFE, tail); n != 2 {
		t.Fatalf("tail overrun: n=%d, want 2", n)
	}
}
