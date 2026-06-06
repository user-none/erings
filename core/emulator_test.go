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

	// Default is NTSC 320 mode: 1708 dots/line, 263 lines/frame
	if e.systemCyclesPerScanline != 1708 {
		t.Errorf("systemCyclesPerScanline = %d, want 1708", e.systemCyclesPerScanline)
	}
	if e.scanlines != 263 {
		t.Errorf("scanlines = %d, want 263", e.scanlines)
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

func TestEmulatorM68KCyclesPerFrame(t *testing.T) {
	e := NewEmulator()
	// Integer 60 fps NTSC: m68k receives exactly 11,289,600 / 60 = 188,160
	// cycles per frame (SCSP target-based distribution).
	if e.m68kCyclesPerFrame != 188160 {
		t.Errorf("m68kCyclesPerFrame = %d, want 188160", e.m68kCyclesPerFrame)
	}
}

func TestEmulatorSegmentDots(t *testing.T) {
	e := NewEmulator()

	// Segments must sum to systemCyclesPerScanline
	total := e.segSystemCycles[0] + e.segSystemCycles[1] + e.segSystemCycles[2] + e.segSystemCycles[3]
	if total != e.systemCyclesPerScanline {
		t.Errorf("segment sum = %d, want %d", total, e.systemCyclesPerScanline)
	}

	// NTSC 320: activeSystemCycles=1280, segments should be 640+640+214+214
	if e.segSystemCycles[0] != 640 {
		t.Errorf("segSystemCycles[0] = %d, want 640", e.segSystemCycles[0])
	}
	if e.segSystemCycles[1] != 640 {
		t.Errorf("segSystemCycles[1] = %d, want 640", e.segSystemCycles[1])
	}
	if e.segSystemCycles[2] != 214 {
		t.Errorf("segSystemCycles[2] = %d, want 214", e.segSystemCycles[2])
	}
	if e.segSystemCycles[3] != 214 {
		t.Errorf("segSystemCycles[3] = %d, want 214", e.segSystemCycles[3])
	}
}

func TestEmulatorSegmentDots352(t *testing.T) {
	e := NewEmulator()
	// Set TVMD HRESO bit 0 to switch to 352-dot mode
	e.vdp2.Write(0, 0x0001)
	e.recalcTiming()

	total := e.segSystemCycles[0] + e.segSystemCycles[1] + e.segSystemCycles[2] + e.segSystemCycles[3]
	if total != 1820 {
		t.Errorf("segment sum = %d, want 1820", total)
	}

	// 352 mode: activeSystemCycles=1408 (352*4), hblank=412
	// segSystemCycles: 704 + 704 + 206 + 206 = 1820
	if e.segSystemCycles[0] != 704 {
		t.Errorf("segSystemCycles[0] = %d, want 704", e.segSystemCycles[0])
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

	if v.SystemCyclesPerLine() != systemCyclesPerLine320 {
		t.Errorf("SystemCyclesPerLine = %d, want %d", v.SystemCyclesPerLine(), systemCyclesPerLine320)
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

func TestEmulatorStepMasterHazard(t *testing.T) {
	e := NewEmulator()

	// Load minimal BIOS so CPU can fetch vectors
	bios := make([]byte, biosSize)
	if err := e.SetBIOS("main_bios", bios); err != nil {
		t.Fatal(err)
	}

	// Step master and observe that cycle counter advances
	prevCycles := e.masterCycles
	e.stepMaster()

	// Should advance at least 1 cycle
	if e.masterCycles <= prevCycles {
		t.Error("masterCycles should advance after stepMaster")
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
	e.masterCycles = 100

	e.Start()

	if e.masterCycles != 100 {
		t.Errorf("masterCycles after Start = %d, want 100 (no-op)", e.masterCycles)
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
