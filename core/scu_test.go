// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

func TestSCUResetValues(t *testing.T) {
	s := NewSCU()

	// IMS initial value
	if s.ims != 0xBFFF {
		t.Errorf("ims = 0x%08X, want 0x0000BFFF", s.ims)
	}

	// IST starts at 0
	if s.ist != 0 {
		t.Errorf("ist = 0x%08X, want 0", s.ist)
	}

	// DSTA starts at 0
	if got := s.Read(0x7C); got != 0 {
		t.Errorf("DSTA = 0x%08X, want 0", got)
	}

	// Version register
	if got := s.Read(0xC8); got != 0x00000004 {
		t.Errorf("VER = 0x%08X, want 0x00000004", got)
	}

	// DMA initial values
	for lvl := 0; lvl < 3; lvl++ {
		if s.dmaAD[lvl] != 0x101 {
			t.Errorf("dmaAD[%d] = 0x%08X, want 0x00000101", lvl, s.dmaAD[lvl])
		}
		if s.dmaMD[lvl] != 0x07 {
			t.Errorf("dmaMD[%d] = 0x%08X, want 0x00000007", lvl, s.dmaMD[lvl])
		}
		if s.dmaR[lvl] != 0 {
			t.Errorf("dmaR[%d] = 0x%08X, want 0", lvl, s.dmaR[lvl])
		}
	}
}

func TestSCUDMAReadWrite(t *testing.T) {
	s := NewSCU()

	// Write and read back D0R, D0W, D0C
	s.Write(0x00, 0xDEADBEEF)
	if got := s.Read(0x00); got != 0xDEADBEEF {
		t.Errorf("D0R = 0x%08X, want 0xDEADBEEF", got)
	}

	s.Write(0x04, 0x12345678)
	if got := s.Read(0x04); got != 0x12345678 {
		t.Errorf("D0W = 0x%08X, want 0x12345678", got)
	}

	s.Write(0x08, 0x00001000)
	if got := s.Read(0x08); got != 0x00001000 {
		t.Errorf("D0C = 0x%08X, want 0x00001000", got)
	}

	// Level 1 DMA
	s.Write(0x20, 0xAAAAAAAA)
	if got := s.Read(0x20); got != 0xAAAAAAAA {
		t.Errorf("D1R = 0x%08X, want 0xAAAAAAAA", got)
	}

	// Level 2 DMA
	s.Write(0x40, 0xBBBBBBBB)
	if got := s.Read(0x40); got != 0xBBBBBBBB {
		t.Errorf("D2R = 0x%08X, want 0xBBBBBBBB", got)
	}
}

func TestSCUWriteOnlyRegisters(t *testing.T) {
	s := NewSCU()

	// D0AD, D0EN, D0MD are write-only; read returns 0
	s.Write(0x0C, 0x12345678)
	if got := s.Read(0x0C); got != 0 {
		t.Errorf("D0AD read = 0x%08X, want 0 (write-only)", got)
	}

	s.Write(0x10, 0x00000001)
	if got := s.Read(0x10); got != 0 {
		t.Errorf("D0EN read = 0x%08X, want 0 (write-only)", got)
	}

	s.Write(0x14, 0x00000007)
	if got := s.Read(0x14); got != 0 {
		t.Errorf("D0MD read = 0x%08X, want 0 (write-only)", got)
	}
}

func TestSCUReadOnlyRegisters(t *testing.T) {
	s := NewSCU()

	// DSTA is read-only
	s.Write(0x7C, 0xFFFFFFFF)
	if got := s.Read(0x7C); got != 0 {
		t.Errorf("DSTA after write = 0x%08X, want 0 (read-only)", got)
	}

	// VER is read-only
	s.Write(0xC8, 0xFFFFFFFF)
	if got := s.Read(0xC8); got != 0x00000004 {
		t.Errorf("VER after write = 0x%08X, want 0x00000004 (read-only)", got)
	}
}

func TestSCUISTWriteZeroClear(t *testing.T) {
	s := NewSCU()

	// Raise VBlankIN to set bit 0
	s.RaiseVBlankIN()
	if s.ist&1 == 0 {
		t.Fatal("IST bit 0 should be set after RaiseVBlankIN")
	}

	// Write with bit 0 = 0 clears it
	s.Write(0xA4, 0xFFFFFFFE)
	if s.ist&1 != 0 {
		t.Error("IST bit 0 should be cleared after writing 0")
	}

	// Raise again, then write all 1s - should not clear
	s.RaiseVBlankIN()
	s.Write(0xA4, 0xFFFFFFFF)
	if s.ist&1 == 0 {
		t.Error("IST bit 0 should remain set after writing 1")
	}
}

func TestSCUIMSUnmaskDelivers(t *testing.T) {
	s := NewSCU()

	var gotLevel uint8
	var gotVec uint16
	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotLevel = level
		gotVec = vec
		called = true
	}, func() {})

	// Unmask bit 0 (clear bit 0 in IMS, 1=masked 0=unmasked)
	s.Write(0xA0, 0xBFFE)
	s.RaiseVBlankIN()

	if !called {
		t.Fatal("handler should have been called")
	}
	if gotLevel != 0xF {
		t.Errorf("level = 0x%X, want 0xF", gotLevel)
	}
	if gotVec != 0x40 {
		t.Errorf("vec = 0x%04X, want 0x0040", gotVec)
	}
}

func TestSCUIMSBlocksDelivery(t *testing.T) {
	s := NewSCU()

	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		called = true
	}, func() {})

	// IMS has bit 0 set (masked) by default ($BFFF)
	s.RaiseVBlankIN()

	if called {
		t.Error("handler should not be called when interrupt is masked")
	}
	// IST bit should still be set
	if s.ist&1 == 0 {
		t.Error("IST bit 0 should be set even when masked")
	}
}

func TestSCUPriorityOrdering(t *testing.T) {
	s := NewSCU()

	var gotLevel uint8
	var gotVec uint16
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotLevel = level
		gotVec = vec
	}, func() {})

	// Unmask bits 0 and 2 (clear in IMS)
	s.Write(0xA0, 0xBFFA)

	// Set both bits directly in IST
	s.ist = (1 << 0) | (1 << 2) // VBlankIN (0xF) + HBlankIN (0xD)
	s.checkInterrupts()

	if gotLevel != 0xF {
		t.Errorf("level = 0x%X, want 0xF (VBlankIN wins)", gotLevel)
	}
	if gotVec != 0x40 {
		t.Errorf("vec = 0x%04X, want 0x0040", gotVec)
	}
}

func TestSCUSameLevelTie(t *testing.T) {
	s := NewSCU()

	var gotVec uint16
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotVec = vec
	}, func() {})

	// Unmask bits 7 and 8 (both level 0x8)
	s.Write(0xA0, 0xBE7F)

	// Set both bits
	s.ist = (1 << 7) | (1 << 8) // SystemManager + PAD, both level 8
	s.checkInterrupts()

	// Lower bit number (7) wins on tie
	if gotVec != 0x47 {
		t.Errorf("vec = 0x%04X, want 0x0047 (SystemManager wins tie)", gotVec)
	}
}

func TestSCURaiseMethods(t *testing.T) {
	tests := []struct {
		name  string
		raise func(s *SCU)
		bit   int
		level uint8
		vec   uint16
	}{
		{"VBlankIN", (*SCU).RaiseVBlankIN, 0, 0xF, 0x40},
		{"VBlankOUT", (*SCU).RaiseVBlankOUT, 1, 0xE, 0x41},
		{"HBlankIN", func(s *SCU) { s.RaiseHBlankIN(0) }, 2, 0xD, 0x42},
		{"Timer0", (*SCU).RaiseTimer0, 3, 0xC, 0x43},
		{"Timer1", (*SCU).RaiseTimer1, 4, 0xB, 0x44},
		{"SystemManager", (*SCU).RaiseSystemManager, 7, 0x8, 0x47},
		{"SoundRequest", (*SCU).RaiseSoundRequest, 6, 0x9, 0x46},
		{"SpriteDrawEnd", (*SCU).RaiseSpriteDrawEnd, 13, 0x2, 0x4D},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSCU()
			var gotLevel uint8
			var gotVec uint16
			called := false
			s.SetIRLHandler(func(level uint8, vec uint16) {
				gotLevel = level
				gotVec = vec
				called = true
			}, func() {})

			// Unmask the bit (clear it, 1=masked 0=unmasked)
			s.ims = s.ims &^ uint32(1<<tc.bit)
			tc.raise(s)

			if !called {
				t.Fatal("handler not called")
			}
			if gotLevel != tc.level {
				t.Errorf("level = 0x%X, want 0x%X", gotLevel, tc.level)
			}
			if gotVec != tc.vec {
				t.Errorf("vec = 0x%04X, want 0x%04X", gotVec, tc.vec)
			}
		})
	}
}

func TestSCUExternalInterrupts(t *testing.T) {
	s := NewSCU()

	var gotLevel uint8
	var gotVec uint16
	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotLevel = level
		gotVec = vec
		called = true
	}, func() {})

	// External interrupts (bit 16+) are not maskable via IMS
	s.RaiseInterrupt(16) // External Int 0

	if !called {
		t.Fatal("external interrupt should be delivered regardless of IMS")
	}
	if gotLevel != 0x7 {
		t.Errorf("level = 0x%X, want 0x7", gotLevel)
	}
	if gotVec != 0x50 {
		t.Errorf("vec = 0x%04X, want 0x0050", gotVec)
	}

	// IST bit 16 is auto-cleared on dispatch when handler is set.
	// The handler was called, which is the important check.
}

func TestSCUNoHandlerNoPanic(t *testing.T) {
	s := NewSCU()
	// No handler set - should not panic
	s.ims = 0 // unmask all (0=unmasked)
	s.RaiseVBlankIN()

	if s.ist&1 == 0 {
		t.Error("IST bit 0 should still be set with no handler")
	}
}

func TestSCUNewDefaults(t *testing.T) {
	s := NewSCU()

	// IST = 0
	if s.ist != 0 {
		t.Errorf("ist = 0x%08X, want 0", s.ist)
	}
	// IMS = 0xBFFF (all masked except bit 14)
	if s.ims != 0xBFFF {
		t.Errorf("ims = 0x%08X, want 0xBFFF", s.ims)
	}
	// pendingBit = -1
	if s.pendingBit != -1 {
		t.Errorf("pendingBit = %d, want -1", s.pendingBit)
	}
	// DMA defaults
	for i := range 3 {
		if s.dmaR[i] != 0 {
			t.Errorf("dmaR[%d] = 0x%08X, want 0", i, s.dmaR[i])
		}
		if s.dmaW[i] != 0 {
			t.Errorf("dmaW[%d] = 0x%08X, want 0", i, s.dmaW[i])
		}
		if s.dmaC[i] != 0 {
			t.Errorf("dmaC[%d] = 0x%08X, want 0", i, s.dmaC[i])
		}
		if s.dmaAD[i] != 0x101 {
			t.Errorf("dmaAD[%d] = 0x%08X, want 0x101", i, s.dmaAD[i])
		}
		if s.dmaMD[i] != 0x07 {
			t.Errorf("dmaMD[%d] = 0x%08X, want 0x07", i, s.dmaMD[i])
		}
		if s.dmaEN[i] != 0 {
			t.Errorf("dmaEN[%d] = 0x%08X, want 0", i, s.dmaEN[i])
		}
		if s.dmaPending[i] {
			t.Errorf("dmaPending[%d] should be false", i)
		}
	}
	// DSTA = 0 at power-on (no DMA active)
	if got := s.Read(0x7C); got != 0 {
		t.Errorf("DSTA = 0x%08X, want 0", got)
	}
}

// --- Phase 9.3: SCU DMA ---

// mockBus provides a simple memory for DMA testing.
type mockBus struct {
	mem map[uint32]uint8
}

func newMockBus() *mockBus {
	return &mockBus{mem: make(map[uint32]uint8)}
}

func (m *mockBus) Read8(addr uint32) uint8 { return m.mem[addr] }
func (m *mockBus) Read16(addr uint32) uint16 {
	return uint16(m.mem[addr])<<8 | uint16(m.mem[addr+1])
}
func (m *mockBus) Read32(addr uint32) uint32 {
	return uint32(m.mem[addr])<<24 | uint32(m.mem[addr+1])<<16 |
		uint32(m.mem[addr+2])<<8 | uint32(m.mem[addr+3])
}
func (m *mockBus) Write8(addr uint32, val uint8) { m.mem[addr] = val }
func (m *mockBus) Write16(addr uint32, val uint16) {
	m.mem[addr] = uint8(val >> 8)
	m.mem[addr+1] = uint8(val)
}
func (m *mockBus) Write32(addr uint32, val uint32) {
	m.mem[addr] = uint8(val >> 24)
	m.mem[addr+1] = uint8(val >> 16)
	m.mem[addr+2] = uint8(val >> 8)
	m.mem[addr+3] = uint8(val)
}

func TestSCUDMADirectImmediate(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	// Write test data to source
	mb.Write32(0x1000, 0xDEADBEEF)
	mb.Write32(0x1004, 0xCAFEBABE)
	mb.Write32(0x1008, 0x12345678)
	mb.Write32(0x100C, 0xAAAABBBB)

	// Configure Level 0 DMA: src=0x1000, dst=0x2000, count=16 bytes
	s.Write(0x00, 0x1000) // D0R (source)
	s.Write(0x04, 0x2000) // D0W (dest)
	s.Write(0x08, 16)     // D0C (byte count)
	s.Write(0x0C, 0x102)  // D0AD: read index=1 (+4), write index=2 (+4)
	s.Write(0x14, 0x07)   // D0MD: start factor=7 (immediate), direct mode

	// Enable DMA - should trigger immediately
	s.Write(0x10, 0x01) // D0EN

	// Both read and write increment by 4, producing a clean 1:1 copy.
	if mb.Read32(0x2000) != 0xDEADBEEF {
		t.Errorf("dest[0x2000] = 0x%08X, want 0xDEADBEEF", mb.Read32(0x2000))
	}
	if mb.Read32(0x2004) != 0xCAFEBABE {
		t.Errorf("dest[0x2004] = 0x%08X, want 0xCAFEBABE", mb.Read32(0x2004))
	}

	// Count register preserved after transfer
	if s.dmaC[0] != 16 {
		t.Errorf("dmaC[0] = %d, want 16 (preserved)", s.dmaC[0])
	}

	// Enable register preserved after transfer
	if s.dmaEN[0] != 1 {
		t.Errorf("dmaEN[0] = %d, want 1 (preserved)", s.dmaEN[0])
	}
}

func TestSCUDMADirectImmediateMatchingInc(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	// Write test data
	mb.Write32(0x1000, 0x11111111)
	mb.Write32(0x1004, 0x22222222)
	mb.Write32(0x1008, 0x33333333)
	mb.Write32(0x100C, 0x44444444)

	// Read add index=1 (+4), Write add index=2 (+4)
	s.Write(0x00, 0x1000) // D0R
	s.Write(0x04, 0x2000) // D0W
	s.Write(0x08, 16)     // D0C
	s.Write(0x0C, 0x102)  // D0AD: read=1(+4), write=2(+4)
	s.Write(0x14, 0x07)   // D0MD: factor=7, direct

	s.Write(0x10, 0x01) // D0EN

	// Both inc by 4, so clean copy
	if mb.Read32(0x2000) != 0x11111111 {
		t.Errorf("dest[0x2000] = 0x%08X, want 0x11111111", mb.Read32(0x2000))
	}
	if mb.Read32(0x2004) != 0x22222222 {
		t.Errorf("dest[0x2004] = 0x%08X, want 0x22222222", mb.Read32(0x2004))
	}
	if mb.Read32(0x2008) != 0x33333333 {
		t.Errorf("dest[0x2008] = 0x%08X, want 0x33333333", mb.Read32(0x2008))
	}
	if mb.Read32(0x200C) != 0x44444444 {
		t.Errorf("dest[0x200C] = 0x%08X, want 0x44444444", mb.Read32(0x200C))
	}
}

func TestSCUDMAEndInterrupt(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	var gotLevel uint8
	var gotVec uint16
	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotLevel = level
		gotVec = vec
		called = true
	}, func() {})

	// Unmask Level 0 DMA End (bit 11)
	s.ims = s.ims &^ (1 << 11)

	mb.Write32(0x1000, 0xABCDABCD)

	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 4)
	s.Write(0x0C, 0x101) // read=+4, write=+4
	s.Write(0x14, 0x07)
	s.Write(0x10, 0x01)

	// Interrupt is deferred, should not fire immediately
	if called {
		t.Fatal("DMA end interrupt should be deferred, not immediate")
	}

	// Tick enough cycles to drain the delay
	s.TickSystemCycles(4)

	if !called {
		t.Fatal("DMA end interrupt not raised after Tick")
	}
	if gotLevel != 0x5 {
		t.Errorf("level = 0x%X, want 0x5", gotLevel)
	}
	if gotVec != 0x4B {
		t.Errorf("vec = 0x%04X, want 0x004B", gotVec)
	}
}

func TestSCUDMALevel1EndInterrupt(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	var gotVec uint16
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotVec = vec
	}, func() {})
	s.ims = s.ims &^ (1 << 10) // Unmask Level 1 DMA End (bit 10)

	mb.Write32(0x1000, 0x12345678)

	// Level 1 DMA (offset 0x20)
	s.Write(0x20, 0x1000)
	s.Write(0x24, 0x2000)
	s.Write(0x28, 4)
	s.Write(0x2C, 0x201)
	s.Write(0x34, 0x07)
	s.Write(0x30, 0x01)

	s.TickSystemCycles(4)

	if gotVec != 0x4A {
		t.Errorf("vec = 0x%04X, want 0x004A (Level 1 DMA End)", gotVec)
	}
}

func TestSCUDMALevel2EndInterrupt(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	var gotVec uint16
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotVec = vec
	}, func() {})
	s.ims = s.ims &^ (1 << 9) // Unmask Level 2 DMA End (bit 9)

	mb.Write32(0x1000, 0x12345678)

	// Level 2 DMA (offset 0x40)
	s.Write(0x40, 0x1000)
	s.Write(0x44, 0x2000)
	s.Write(0x48, 4)
	s.Write(0x4C, 0x201)
	s.Write(0x54, 0x07)
	s.Write(0x50, 0x01)

	s.TickSystemCycles(4)

	if gotVec != 0x49 {
		t.Errorf("vec = 0x%04X, want 0x0049 (Level 2 DMA End)", gotVec)
	}
}

func TestSCUDMAFixedAddresses(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0xAAAABBBB)

	// Read add=0 (fixed), Write add=0 (fixed)
	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 8) // 2 longwords
	s.Write(0x0C, 0x000)
	s.Write(0x14, 0x07)
	s.Write(0x10, 0x01)

	// Both addresses fixed: reads same source twice, writes same dest twice
	if mb.Read32(0x2000) != 0xAAAABBBB {
		t.Errorf("dest[0x2000] = 0x%08X, want 0xAAAABBBB", mb.Read32(0x2000))
	}
	// Source address should still be 0x1000 (fixed)
	if s.dmaR[0] != 0x1000 {
		t.Errorf("dmaR[0] = 0x%08X, want 0x1000", s.dmaR[0])
	}
	if s.dmaW[0] != 0x2000 {
		t.Errorf("dmaW[0] = 0x%08X, want 0x2000", s.dmaW[0])
	}
}

func TestSCUDMAPendingVBlankIN(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0xDEADC0DE)

	// Start factor=0 (V-Blank-IN)
	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 4)
	s.Write(0x0C, 0x101)
	s.Write(0x14, 0x0000) // factor=0

	s.Write(0x10, 0x01) // Enable - should NOT execute yet

	// Data should NOT be transferred yet
	if mb.Read32(0x2000) != 0 {
		t.Errorf("dest should be 0 before trigger, got 0x%08X", mb.Read32(0x2000))
	}
	if !s.dmaPending[0] {
		t.Error("dmaPending[0] should be true")
	}

	// Trigger VBlankIN
	s.RaiseVBlankIN()

	// Now data should be transferred
	if mb.Read32(0x2000) != 0xDEADC0DE {
		t.Errorf("dest[0x2000] = 0x%08X, want 0xDEADC0DE", mb.Read32(0x2000))
	}
	if s.dmaPending[0] {
		t.Error("dmaPending[0] should be false after trigger")
	}
}

func TestSCUDMADeferredDelayProportional(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		called = true
	}, func() {})
	s.ims = s.ims &^ (1 << 11)

	// 16 bytes = 4 longwords = delay of 4 cycles
	for i := uint32(0); i < 4; i++ {
		mb.Write32(0x1000+i*4, 0x11111111)
	}

	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 16) // 16 bytes
	s.Write(0x0C, 0x101)
	s.Write(0x14, 0x07)
	s.Write(0x10, 0x01)

	// Tick 3 cycles - should NOT fire yet (delay = 16/4 = 4)
	s.TickSystemCycles(3)
	if called {
		t.Fatal("interrupt fired too early at 3 cycles, expected delay of 4")
	}

	// Tick 1 more - should fire now
	s.TickSystemCycles(1)
	if !called {
		t.Fatal("interrupt should have fired at 4 cycles")
	}
}

func TestSCUDMANoBusNoPanic(t *testing.T) {
	s := NewSCU()
	// No bus set - should not panic
	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 4)
	s.Write(0x0C, 0x101)
	s.Write(0x14, 0x07)
	s.Write(0x10, 0x01)
}

// --- Indirect DMA Tests ---

func TestSCUDMAIndirectSingleEntry(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	// Source data
	mb.Write32(0x1000, 0xDEADBEEF)
	mb.Write32(0x1004, 0xCAFEBABE)

	// Transfer table at 0x3000: one entry (count, dst, src)
	mb.Write32(0x3000, 8)          // count: 8 bytes (2 longwords)
	mb.Write32(0x3004, 0x2000)     // dst
	mb.Write32(0x3008, 0x80001000) // src with end marker (bit 31)

	// Configure Level 0 indirect DMA
	s.Write(0x04, 0x3000)     // D0W = table address
	s.Write(0x0C, 0x102)      // D0AD: read=1(+4), write=2(+4)
	s.Write(0x14, 0x01000007) // D0MD: factor=7, indirect mode (bit 24)
	s.Write(0x10, 0x01)       // D0EN: trigger

	if mb.Read32(0x2000) != 0xDEADBEEF {
		t.Errorf("indirect[0x2000] = 0x%08X, want 0xDEADBEEF", mb.Read32(0x2000))
	}
	if mb.Read32(0x2004) != 0xCAFEBABE {
		t.Errorf("indirect[0x2004] = 0x%08X, want 0xCAFEBABE", mb.Read32(0x2004))
	}
}

func TestSCUDMAIndirectMultipleEntries(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	// Source data at two locations
	mb.Write32(0x1000, 0x11111111)
	mb.Write32(0x1004, 0x22222222)
	mb.Write32(0x5000, 0x33333333)

	// Transfer table at 0x3000: two entries
	// Entry 0: 8 bytes from 0x1000 to 0x2000
	mb.Write32(0x3000, 8)      // count
	mb.Write32(0x3004, 0x2000) // dst
	mb.Write32(0x3008, 0x1000) // src (no end marker)

	// Entry 1: 4 bytes from 0x5000 to 0x2008 (end marker set)
	mb.Write32(0x300C, 4)          // count
	mb.Write32(0x3010, 0x2008)     // dst
	mb.Write32(0x3014, 0x80005000) // src with end marker

	s.Write(0x04, 0x3000)     // D0W = table
	s.Write(0x0C, 0x102)      // read=1(+4), write=2(+4)
	s.Write(0x14, 0x01000007) // factor=7, indirect
	s.Write(0x10, 0x01)       // trigger

	if mb.Read32(0x2000) != 0x11111111 {
		t.Errorf("entry0[0x2000] = 0x%08X, want 0x11111111", mb.Read32(0x2000))
	}
	if mb.Read32(0x2004) != 0x22222222 {
		t.Errorf("entry0[0x2004] = 0x%08X, want 0x22222222", mb.Read32(0x2004))
	}
	if mb.Read32(0x2008) != 0x33333333 {
		t.Errorf("entry1[0x2008] = 0x%08X, want 0x33333333", mb.Read32(0x2008))
	}
}

func TestSCUDMAIndirectEndInterrupt(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	var gotVec uint16
	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotVec = vec
		called = true
	}, func() {})
	s.ims = s.ims &^ (1 << 11) // Unmask Level 0 DMA End

	mb.Write32(0x1000, 0xABCD)
	mb.Write32(0x3000, 4)
	mb.Write32(0x3004, 0x2000)
	mb.Write32(0x3008, 0x80001000)

	s.Write(0x04, 0x3000)
	s.Write(0x0C, 0x101)
	s.Write(0x14, 0x01000007) // indirect
	s.Write(0x10, 0x01)

	if called {
		t.Fatal("indirect DMA end interrupt should be deferred")
	}

	s.TickSystemCycles(4)

	if !called {
		t.Fatal("indirect DMA end interrupt not raised after Tick")
	}
	if gotVec != 0x4B {
		t.Errorf("vec = 0x%04X, want 0x004B", gotVec)
	}
}

func TestSCUDMAIndirectPendingVBlankIN(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0xFEEDFACE)
	mb.Write32(0x3000, 4)
	mb.Write32(0x3004, 0x2000)
	mb.Write32(0x3008, 0x80001000)

	// Start factor=0 (V-Blank-IN), indirect mode
	s.Write(0x04, 0x3000)
	s.Write(0x0C, 0x101)
	s.Write(0x14, 0x01000000) // factor=0, indirect (bit 24)
	s.Write(0x10, 0x01)       // Enable - should NOT execute yet

	if mb.Read32(0x2000) != 0 {
		t.Errorf("should not transfer before trigger, got 0x%08X", mb.Read32(0x2000))
	}

	s.RaiseVBlankIN()

	if mb.Read32(0x2000) != 0xFEEDFACE {
		t.Errorf("after trigger: 0x%08X, want 0xFEEDFACE", mb.Read32(0x2000))
	}
}

// --- SCU Timer Tests ---

func TestSCUTimer0FiresOnMatch(t *testing.T) {
	s := NewSCU()

	var gotVec uint16
	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotVec = vec
		called = true
	}, func() {})
	s.ims = s.ims &^ (1 << 3) // Unmask Timer 0 (bit 3)

	// Set T0C = 3 (fire after 3 H-Blanks)
	s.Write(0x90, 3)
	// Enable timers via T1MD bit 0
	s.Write(0x98, 1)

	// H-Blank 1, 2: no fire
	s.RaiseHBlankIN(0)
	s.RaiseHBlankIN(1)
	if called {
		t.Fatal("Timer 0 should not fire before match")
	}

	// H-Blank 3: counter reaches 3, should fire
	s.RaiseHBlankIN(2)
	if !called {
		t.Fatal("Timer 0 should fire when counter matches T0C")
	}
	if gotVec != 0x43 {
		t.Errorf("vec = 0x%04X, want 0x0043", gotVec)
	}
}

func TestSCUTimer0ResetsAtVBlankIN(t *testing.T) {
	s := NewSCU()

	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		if vec == 0x43 {
			called = true
		}
	}, func() {})
	s.ims = s.ims &^ (1 << 3)

	s.Write(0x90, 3) // T0C = 3
	s.Write(0x98, 1) // Enable

	// Count up to 2
	s.RaiseHBlankIN(0)
	s.RaiseHBlankIN(1)

	// VBlankOUT resets counter to 0
	s.RaiseVBlankOUT()
	// Acknowledge the VBlankOUT interrupt to clear pendingBit
	s.AcknowledgeInterrupt()

	// Need 3 more H-Blanks to reach T0C again
	s.RaiseHBlankIN(0)
	s.RaiseHBlankIN(1)
	if called {
		t.Fatal("Timer 0 should not fire - counter was reset by VBlank")
	}

	s.RaiseHBlankIN(2)
	if !called {
		t.Error("Timer 0 should fire after 3 H-Blanks post-reset")
	}
}

func TestSCUTimer0DisabledByDefault(t *testing.T) {
	s := NewSCU()

	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		if vec == 0x43 {
			called = true
		}
	}, func() {})
	s.ims = s.ims &^ (1 << 3)

	s.Write(0x90, 1) // T0C = 1
	// T1MD not set (timers disabled)

	s.RaiseHBlankIN(0)
	s.RaiseHBlankIN(1)
	s.RaiseHBlankIN(2)

	if called {
		t.Error("Timer 0 should not fire when timers are disabled")
	}
}

func TestSCUTimer1Mode0FiresOnLine(t *testing.T) {
	s := NewSCU()

	var gotVec uint16
	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		if vec == 0x44 {
			gotVec = vec
			called = true
		}
	}, func() {})
	s.ims = s.ims &^ (1 << 4) // Unmask Timer 1 (bit 4)

	s.Write(0x94, 5) // T1S = line 5
	s.Write(0x98, 1) // Enable, mode 0 (bit 8 = 0)

	// Lines 0-4: no fire
	for i := uint16(0); i < 5; i++ {
		s.RaiseHBlankIN(i)
	}
	if called {
		t.Fatal("Timer 1 should not fire before line match")
	}

	// Line 5: should fire
	s.RaiseHBlankIN(5)
	if !called {
		t.Fatal("Timer 1 should fire on line match")
	}
	if gotVec != 0x44 {
		t.Errorf("vec = 0x%04X, want 0x0044", gotVec)
	}
}

func TestSCUTimer1Mode1RequiresTimer0(t *testing.T) {
	s := NewSCU()

	s.SetIRLHandler(func(level uint8, vec uint16) {}, func() {})
	s.ims = s.ims &^ ((1 << 3) | (1 << 4)) // Unmask Timer 0 and Timer 1

	s.Write(0x90, 5)        // T0C = 5 (Timer 0 fires after 5 H-Blanks)
	s.Write(0x94, 4)        // T1S = line 4
	s.Write(0x98, 1|(1<<8)) // Enable, mode 1 (bit 8 = 1)

	// Counter increments: 1,2,3,4,5 after 5 calls to RaiseHBlankIN
	for i := uint16(0); i < 5; i++ {
		s.RaiseHBlankIN(i)
	}
	// On 5th call (line 4): counter=5=T0C so Timer 0 fires.
	// Line 4 also matches T1S=4. Mode 1 requires both -> Timer 1 fires.
	// Check IST directly since Timer 0 has higher IRL priority.
	if s.ist&(1<<4) == 0 {
		t.Error("Timer 1 mode 1: IST bit 4 should be set when Timer 0 fires and line matches")
	}
}

func TestSCUTimer1Mode1BlockedWithoutTimer0(t *testing.T) {
	s := NewSCU()

	t1Called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		if vec == 0x44 {
			t1Called = true
		}
	}, func() {})
	s.ims = s.ims &^ ((1 << 3) | (1 << 4))

	s.Write(0x90, 100)      // T0C = 100 (won't fire during this test)
	s.Write(0x94, 2)        // T1S = line 2
	s.Write(0x98, 1|(1<<8)) // Enable, mode 1

	// Line 2 matches T1S but Timer 0 won't fire (T0C=100)
	for i := uint16(0); i < 5; i++ {
		s.RaiseHBlankIN(i)
	}
	if t1Called {
		t.Error("Timer 1 mode 1 should NOT fire without Timer 0 match")
	}
}

// TestSCUDMARegWritesDuringOperationDrain verifies that a CPU write
// to any per-level DMA register while the level is operating drains
// the in-flight transfer (raises the end interrupt, clears dmaDelay)
// before the new value lands. SCU User's Manual section 3.2 states
// each per-level register "prohibits writing while DMA is operating";
// real HW completes the transfer before the CPU's next bus-side write
// reaches the SCU.
func TestSCUDMARegWritesDuringOperationDrain(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0x11111111)
	mb.Write32(0x1004, 0x22222222)

	// Kick a Level 0 direct DMA, count=8 -> dmaDelay[0]=2.
	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 8)
	s.Write(0x0C, 0x101)
	s.Write(0x14, 0x07)
	s.Write(0x10, 0x01)

	if s.dmaDelay[0] < 0 {
		t.Fatal("dmaDelay[0] should be set after kick")
	}

	// First non-DEN write during operation drains the level.
	s.Write(0x00, 0xDEADBEEF)
	if s.dmaDelay[0] >= 0 {
		t.Errorf("dmaDelay[0] = %d, want -1 after drain by reg write", s.dmaDelay[0])
	}
	if s.ist&(1<<dmaEndBit[0]) == 0 {
		t.Error("DMA-end interrupt not raised after drain by reg write")
	}

	// All five non-DEN writes still land verbatim.
	s.Write(0x04, 0xCAFEBABE)
	s.Write(0x08, 0xFFFFFFFF)
	s.Write(0x0C, 0xA5A5A5A5)
	s.Write(0x14, 0x1234)

	if s.dmaR[0] != 0xDEADBEEF {
		t.Errorf("dmaR[0] = 0x%08X, want 0xDEADBEEF", s.dmaR[0])
	}
	if s.dmaW[0] != 0xCAFEBABE {
		t.Errorf("dmaW[0] = 0x%08X, want 0xCAFEBABE", s.dmaW[0])
	}
	if s.dmaC[0] != 0xFFFFFFFF {
		t.Errorf("dmaC[0] = 0x%08X, want 0xFFFFFFFF", s.dmaC[0])
	}
	if s.dmaAD[0] != 0xA5A5A5A5 {
		t.Errorf("dmaAD[0] = 0x%08X, want 0xA5A5A5A5", s.dmaAD[0])
	}
	if s.dmaMD[0] != 0x1234 {
		t.Errorf("dmaMD[0] = 0x%08X, want 0x1234", s.dmaMD[0])
	}
}

// TestSCUDMARegWriteDrainAndRetrigger verifies that a re-trigger
// sequence (programming + DEN=1) issued while the prior transfer is
// in flight drains that transfer, lands the new programming, and
// fires the second transfer inline with the new parameters. Replaces
// the earlier held-DEN-only model.
func TestSCUDMARegWriteDrainAndRetrigger(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0xAAAA0001)
	mb.Write32(0x1004, 0xAAAA0002)
	mb.Write32(0x1008, 0xBBBB0001)
	mb.Write32(0x100C, 0xBBBB0002)

	// First transfer: 0x1000->0x2000, count=8 -> dmaDelay[0]=2
	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 8)
	s.Write(0x0C, 0x102) // read +4, write +4
	s.Write(0x14, 0x07)
	s.Write(0x10, 0x01)

	if s.dmaDelay[0] < 0 {
		t.Fatal("dmaDelay[0] should be set after first kick")
	}
	if s.dmaR[0] != 0x1008 || s.dmaW[0] != 0x2008 {
		t.Fatalf("post-transfer addrs R=0x%08X W=0x%08X, want 0x1008/0x2008",
			s.dmaR[0], s.dmaW[0])
	}

	// Re-program every register and re-trigger while still operating.
	// Each write drains the level on first contact; subsequent writes
	// of this block see an idle level.
	s.Write(0x00, 0x1008)
	s.Write(0x04, 0x2008)
	s.Write(0x08, 8)
	s.Write(0x0C, 0x102)
	s.Write(0x14, 0x07)
	s.Write(0x10, 0x01)

	// Second transfer must have already executed inline.
	if mb.Read32(0x2008) != 0xBBBB0001 {
		t.Errorf("mem[0x2008] = 0x%08X, want 0xBBBB0001 (second transfer)",
			mb.Read32(0x2008))
	}
	if s.dmaR[0] != 0x1010 {
		t.Errorf("dmaR[0] = 0x%08X, want 0x1010 (second transfer advanced)",
			s.dmaR[0])
	}
	if s.dmaDelay[0] < 0 {
		t.Error("dmaDelay[0] should be set for the second transfer")
	}
}

// TestSCUDMARegWriteDrainPerRegister verifies the drain is triggered
// by a write to any of the six per-level DMA registers, not only DEN.
// Each subtest kicks an in-flight transfer, issues a single write to
// one register, then verifies the level was drained (countdown cleared,
// end interrupt asserted).
func TestSCUDMARegWriteDrainPerRegister(t *testing.T) {
	cases := []struct {
		name   string
		offset uint32
		val    uint32
	}{
		{"DR", 0x00, 0xDEADBEEF},
		{"DW", 0x04, 0xCAFEBABE},
		{"DC", 0x08, 0x10},
		{"DAD", 0x0C, 0x101},
		{"DEN_clear", 0x10, 0x00}, // bit-0 = 0 still drains, no retrigger
		{"DMD", 0x14, 0x07},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSCU()
			mb := newMockBus()
			s.SetBus(mb)

			mb.Write32(0x1000, 0xAA)
			mb.Write32(0x1004, 0xBB)

			// Kick Level 0 direct DMA, count=8 -> dmaDelay[0]=2.
			s.Write(0x00, 0x1000)
			s.Write(0x04, 0x2000)
			s.Write(0x08, 8)
			s.Write(0x0C, 0x101)
			s.Write(0x14, 0x07)
			s.Write(0x10, 0x01)
			if s.dmaDelay[0] < 0 {
				t.Fatalf("setup: dmaDelay[0] not set")
			}
			s.ist &^= 1 << dmaEndBit[0]

			s.Write(tc.offset, tc.val)

			if s.dmaDelay[0] >= 0 {
				t.Errorf("dmaDelay[0] = %d after %s write, want -1",
					s.dmaDelay[0], tc.name)
			}
			if s.ist&(1<<dmaEndBit[0]) == 0 {
				t.Errorf("DMA-end interrupt not raised after %s write", tc.name)
			}
		})
	}
}

// TestSCUDMARegWriteDrainLevel1 verifies the drain logic targets the
// correct level when a write hits a non-zero level group. Level 1
// register block lives at offset 0x20-0x34.
func TestSCUDMARegWriteDrainLevel1(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0x12345678)

	// Kick Level 1, count=4 -> dmaDelay[1]=1.
	s.Write(0x20, 0x1000)
	s.Write(0x24, 0x2000)
	s.Write(0x28, 4)
	s.Write(0x2C, 0x101)
	s.Write(0x34, 0x07)
	s.Write(0x30, 0x01)
	if s.dmaDelay[1] < 0 {
		t.Fatal("dmaDelay[1] should be set after kick")
	}
	if s.dmaDelay[0] >= 0 || s.dmaDelay[2] >= 0 {
		t.Fatal("only level 1 should be active")
	}

	s.Write(0x20, 0x9999) // any Level 1 reg write

	if s.dmaDelay[1] >= 0 {
		t.Errorf("dmaDelay[1] = %d, want -1 after drain", s.dmaDelay[1])
	}
	if s.ist&(1<<dmaEndBit[1]) == 0 {
		t.Error("Level-1 DMA-end interrupt not raised after drain")
	}
	if s.ist&((1<<dmaEndBit[0])|(1<<dmaEndBit[2])) != 0 {
		t.Error("only Level-1 end interrupt should be raised")
	}
}

// TestSCUDMARegWriteDrainFiresPendingFactor verifies that when a
// start-factor trigger arrived while the level was in flight (held
// in dmaPending), a subsequent CPU register write that drains the
// level fires the pending factor-driven transfer using the old
// programming, then the new register value lands at the now-idle
// channel.
func TestSCUDMARegWriteDrainFiresPendingFactor(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0xAAAA0001)
	mb.Write32(0x1004, 0xAAAA0002)
	mb.Write32(0x1008, 0xBBBB0001)
	mb.Write32(0x100C, 0xBBBB0002)

	// Level 0 with VBlank-IN factor; arm and run the first transfer.
	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 8)
	s.Write(0x0C, 0x102)
	s.Write(0x14, 0x00) // factor=0 (VBlank-IN)
	s.Write(0x10, 0x01) // arm
	s.RaiseVBlankIN()
	if s.dmaDelay[0] < 0 {
		t.Fatal("dmaDelay[0] should be set after factor triggered first transfer")
	}

	// Re-arm; raise the factor again while still in flight - it must
	// be held in dmaPending per Final Spec No. 22.
	s.dmaPending[0] = true
	s.RaiseVBlankIN()
	if !s.dmaPending[0] {
		t.Fatal("dmaPending[0] should stay armed while level in flight")
	}
	if mb.Read32(0x2008) != 0 {
		t.Fatal("second transfer must not have run yet")
	}

	// CPU write to any Level-0 reg drains the in-flight transfer,
	// which fires the held factor trigger inline. After that the
	// new register value lands.
	s.Write(0x00, 0xDEADBEEF)

	if mb.Read32(0x2008) != 0xBBBB0001 {
		t.Errorf("mem[0x2008] = 0x%08X, want 0xBBBB0001 (factor-driven second transfer)",
			mb.Read32(0x2008))
	}
	if s.dmaPending[0] {
		t.Error("dmaPending[0] should be consumed after factor trigger fired")
	}
	if s.dmaR[0] != 0xDEADBEEF {
		t.Errorf("dmaR[0] = 0x%08X, want 0xDEADBEEF (post-drain reg write must land)",
			s.dmaR[0])
	}
}

// TestSCUDMAHeldFactorRetrigger verifies that a start-factor event
// that arrives while the matching level is operating stays pending
// and fires after the current DMA ends.
func TestSCUDMAHeldFactorRetrigger(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0x01010101)
	mb.Write32(0x1004, 0x02020202)

	// Level 0 DMA with start factor 0 (VBlank-IN).
	s.Write(0x00, 0x1000)
	s.Write(0x04, 0x2000)
	s.Write(0x08, 8)
	s.Write(0x0C, 0x102) // read +4, write +4
	s.Write(0x14, 0x00)  // factor=0 (VBlank-IN), direct
	s.Write(0x10, 0x01)  // arm

	if !s.dmaPending[0] {
		t.Fatal("dmaPending[0] should be set after arming with factor 0")
	}

	// First VBlank-IN: transfer runs.
	s.RaiseVBlankIN()
	if s.dmaDelay[0] < 0 {
		t.Fatal("dmaDelay[0] should be set after factor triggered DMA")
	}
	if mb.Read32(0x2000) != 0x01010101 {
		t.Errorf("first transfer missing: 0x%08X", mb.Read32(0x2000))
	}
	// After execute, pending is cleared per existing checkDMATrigger.
	if s.dmaPending[0] {
		t.Fatal("dmaPending[0] should be cleared after factor match consumed it")
	}

	// Re-arm and re-raise the factor while still operating.
	s.dmaPending[0] = true
	mb.Write32(0x1008, 0xCCCC0001)
	mb.Write32(0x100C, 0xCCCC0002)
	s.RaiseVBlankIN()

	if !s.dmaPending[0] {
		t.Error("dmaPending[0] should remain armed while level is operating")
	}
	// No second transfer yet.
	if mb.Read32(0x2008) != 0 {
		t.Errorf("mem[0x2008] = 0x%08X, want 0 (held trigger not fired yet)",
			mb.Read32(0x2008))
	}

	// Drain the delay and confirm held factor trigger runs.
	s.TickSystemCycles(2)
	if s.dmaPending[0] {
		t.Error("dmaPending[0] should be consumed after held trigger fires")
	}
	if mb.Read32(0x2008) != 0xCCCC0001 {
		t.Errorf("mem[0x2008] = 0x%08X, want 0xCCCC0001", mb.Read32(0x2008))
	}
}

// TestSCUDSTALiveMV verifies the DMA Status Register returns a live
// D*MV "in operation" bit per level.
func TestSCUDSTALiveMV(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	if got := s.Read(0x7C); got != 0 {
		t.Errorf("DSTA at idle = 0x%08X, want 0", got)
	}

	mb.Write32(0x1000, 0x12345678)

	// Kick Level 1.
	s.Write(0x20, 0x1000)
	s.Write(0x24, 0x2000)
	s.Write(0x28, 4)
	s.Write(0x2C, 0x101)
	s.Write(0x34, 0x07)
	s.Write(0x30, 0x01)

	// D1MV is bit 8.
	if got := s.Read(0x7C); got&(1<<8) == 0 {
		t.Errorf("DSTA = 0x%08X during Level 1 op, want bit 8 set", got)
	}
	// Other level bits must be clear.
	if got := s.Read(0x7C); got&((1<<4)|(1<<12)) != 0 {
		t.Errorf("DSTA = 0x%08X, only bit 8 should be set", got)
	}

	// Drain and confirm the bit clears.
	s.TickSystemCycles(1)
	if got := s.Read(0x7C); got != 0 {
		t.Errorf("DSTA after Tick = 0x%08X, want 0", got)
	}
}

// --- Pass 2 gap-fill tests ---

func TestSCUResetClearsAllState(t *testing.T) {
	s := NewSCU()
	s.ist = 0x1234
	s.ims = 0x0000
	s.pendingBit = 5
	s.dmaEN[0] = 1
	s.dmaAD[0] = 0x000
	s.dmaR[1] = 0x12345
	s.dmaDelay[2] = 42
	s.dmaPending[0] = true
	s.dspCycleCarry = 1

	clearCalls := 0
	s.SetIRLHandler(func(level uint8, vec uint16) {}, func() { clearCalls++ })
	s.Reset()

	if s.ist != 0 {
		t.Errorf("ist = 0x%X, want 0 after Reset", s.ist)
	}
	if s.ims != 0xBFFF {
		t.Errorf("ims = 0x%X, want 0xBFFF after Reset", s.ims)
	}
	if s.pendingBit != -1 {
		t.Errorf("pendingBit = %d, want -1 after Reset", s.pendingBit)
	}
	for lvl := 0; lvl < 3; lvl++ {
		if s.dmaEN[lvl] != 0 || s.dmaR[lvl] != 0 || s.dmaC[lvl] != 0 {
			t.Errorf("dma level %d regs not zeroed", lvl)
		}
		if s.dmaAD[lvl] != 0x101 || s.dmaMD[lvl] != 0x07 {
			t.Errorf("dma level %d AD/MD not reset to defaults", lvl)
		}
		if s.dmaDelay[lvl] != -1 {
			t.Errorf("dma level %d delay = %d, want -1", lvl, s.dmaDelay[lvl])
		}
		if s.dmaPending[lvl] {
			t.Errorf("dma level %d pending not cleared", lvl)
		}
	}
	if s.dspCycleCarry != 0 {
		t.Error("dspCycleCarry not cleared")
	}
	if clearCalls != 1 {
		t.Errorf("clearIRL called %d times, want 1", clearCalls)
	}
}

func TestSCUReadInternalDMARegisters(t *testing.T) {
	s := NewSCU()
	s.dmaR[0] = 0x0AAA
	s.dmaW[0] = 0x0BBB
	s.dmaC[0] = 0x0CCC
	s.dmaAD[0] = 0x0DDD
	s.dmaEN[0] = 0x0EEE
	s.dmaMD[0] = 0x0FFF

	if got := s.ReadInternal(0x00); got != 0x0AAA {
		t.Errorf("ReadInternal DMA R = 0x%X, want 0x0AAA", got)
	}
	if got := s.ReadInternal(0x04); got != 0x0BBB {
		t.Errorf("ReadInternal DMA W = 0x%X, want 0x0BBB", got)
	}
	if got := s.ReadInternal(0x08); got != 0x0CCC {
		t.Errorf("ReadInternal DMA C = 0x%X, want 0x0CCC", got)
	}
	if got := s.ReadInternal(0x0C); got != 0x0DDD {
		t.Errorf("ReadInternal DMA AD = 0x%X, want 0x0DDD", got)
	}
	if got := s.ReadInternal(0x10); got != 0x0EEE {
		t.Errorf("ReadInternal DMA EN = 0x%X, want 0x0EEE", got)
	}
	if got := s.ReadInternal(0x14); got != 0x0FFF {
		t.Errorf("ReadInternal DMA MD = 0x%X, want 0x0FFF", got)
	}
}

func TestSCUReadInternalControlRegisters(t *testing.T) {
	s := NewSCU()
	s.dstp = 0x12345
	s.t0c = 0x0100
	s.t1s = 0x0080
	s.t1md = 0x0101
	s.ims = 0x0FFF
	s.ist = 0x0010
	s.aiak = 0x0001
	s.asr0 = 0xAAAA
	s.asr1 = 0xBBBB
	s.aref = 0xCCCC
	s.rsel = 0x0001

	cases := []struct {
		off  uint32
		want uint32
	}{
		{0x60, 0x12345},
		{0x84, 0},
		{0x90, 0x0100},
		{0x94, 0x0080},
		{0x98, 0x0101},
		{0xA0, 0x0FFF},
		{0xA4, 0x0010},
		{0xA8, 0x0001},
		{0xB0, 0xAAAA},
		{0xB4, 0xBBBB},
		{0xB8, 0xCCCC},
		{0xC4, 0x0001},
		{0xC8, 0x00000004},
	}
	for _, c := range cases {
		if got := s.ReadInternal(c.off); got != c.want {
			t.Errorf("ReadInternal(0x%02X) = 0x%X, want 0x%X", c.off, got, c.want)
		}
	}
	// Default path.
	if got := s.ReadInternal(0xDC); got != 0 {
		t.Errorf("ReadInternal(0xDC) = 0x%X, want 0 (unmapped)", got)
	}
}

func TestSCUAcknowledgeInterruptClearsPending(t *testing.T) {
	s := NewSCU()
	s.SetIRLHandler(func(level uint8, vec uint16) {}, func() {})
	s.ims = 0x0000
	s.RaiseInterrupt(3) // Timer 0
	if s.pendingBit != 3 {
		t.Fatalf("pendingBit = %d, want 3 after RaiseInterrupt", s.pendingBit)
	}
	if s.ist&(1<<3) == 0 {
		t.Fatal("ist bit 3 not set after raise")
	}

	s.AcknowledgeInterrupt()
	if s.pendingBit != -1 {
		t.Errorf("pendingBit = %d, want -1 after ack", s.pendingBit)
	}
	if s.ist&(1<<3) != 0 {
		t.Error("ist bit 3 still set after ack")
	}

	// Ack with no pending bit: no-op, no panic.
	s.AcknowledgeInterrupt()
}

func TestSCURaiseInterruptOutOfRange(t *testing.T) {
	s := NewSCU()
	before := s.ist
	s.RaiseInterrupt(-1)
	s.RaiseInterrupt(32)
	s.RaiseInterrupt(100)
	if s.ist != before {
		t.Errorf("ist changed by out-of-range RaiseInterrupt: 0x%X -> 0x%X", before, s.ist)
	}
}

func TestSCUWriteControlRegisters(t *testing.T) {
	s := NewSCU()

	s.Write(0x60, 0xDEADBEEF)
	if s.dstp != 0xDEADBEEF {
		t.Errorf("dstp = 0x%X after write", s.dstp)
	}
	s.Write(0x90, 0x0123)
	if s.t0c != 0x0123 {
		t.Errorf("t0c = 0x%X after write", s.t0c)
	}
	s.Write(0x94, 0x0080)
	if s.t1s != 0x0080 {
		t.Errorf("t1s = 0x%X after write", s.t1s)
	}
	s.Write(0x98, 0x0101)
	if s.t1md != 0x0101 {
		t.Errorf("t1md = 0x%X after write", s.t1md)
	}
	s.Write(0xA8, 0x0001)
	if s.aiak != 0x0001 {
		t.Errorf("aiak = 0x%X after write", s.aiak)
	}
	s.Write(0xB0, 0x11110000)
	if s.asr0 != 0x11110000 {
		t.Errorf("asr0 = 0x%X after write", s.asr0)
	}
	s.Write(0xB4, 0x22220000)
	if s.asr1 != 0x22220000 {
		t.Errorf("asr1 = 0x%X after write", s.asr1)
	}
	s.Write(0xB8, 0x33330000)
	if s.aref != 0x33330000 {
		t.Errorf("aref = 0x%X after write", s.aref)
	}
	s.Write(0xC4, 0x0001)
	if s.rsel != 0x0001 {
		t.Errorf("rsel = 0x%X after write", s.rsel)
	}

	// Read-only registers: writes are ignored.
	s.Write(0x7C, 0xFFFFFFFF) // DSTA read-only
	s.Write(0xC8, 0xFFFFFFFF) // VER read-only
	if got := s.Read(0xC8); got != 0x00000004 {
		t.Errorf("VER = 0x%X after write (should be ignored), want 0x4", got)
	}
}

func TestSCUReadVersionAndAIAK(t *testing.T) {
	s := NewSCU()
	if got := s.Read(0xC8); got != 0x00000004 {
		t.Errorf("VER = 0x%X, want 0x4", got)
	}
	s.aiak = 0x0001
	if got := s.Read(0xA8); got != 0x0001 {
		t.Errorf("AIAK = 0x%X, want 0x1", got)
	}
	s.rsel = 0x0001
	if got := s.Read(0xC4); got != 0x0001 {
		t.Errorf("RSEL = 0x%X, want 0x1", got)
	}
	// Unmapped offset returns 0.
	if got := s.Read(0xDC); got != 0 {
		t.Errorf("Read(0xDC) = 0x%X, want 0 (unmapped)", got)
	}
	// DMA level 3+ returns 0.
	if got := s.Read(0x60); got != 0 {
		t.Errorf("Read(0x60) write-only = 0x%X, want 0", got)
	}
}

func TestSCUDSPCycleBudgetHalfRate(t *testing.T) {
	s := NewSCU()
	// Prime the DSP with a program so it executes.
	s.Write(0x80, 1<<16) // PPAF: set EX bit.
	if !s.dsp.executing {
		t.Fatal("DSP not executing after EX write")
	}

	// Start with cycleCarry=0, give 1 cycle -> budget=0, carry=1.
	s.dspCycleCarry = 0
	before := s.dsp.pc
	s.TickSystemCycles(1)
	if s.dspCycleCarry != 1 {
		t.Errorf("after 1 cycle: carry = %d, want 1", s.dspCycleCarry)
	}
	// A second odd cycle should sum to 2 system cycles -> 1 DSP cycle.
	s.TickSystemCycles(1)
	if s.dspCycleCarry != 0 {
		t.Errorf("after 2 cycles total: carry = %d, want 0", s.dspCycleCarry)
	}
	_ = before // value of pc depends on the program; the key invariant is carry accounting
}

func TestSCUDMATriggerDMAENZeroClearsPending(t *testing.T) {
	s := NewSCU()
	s.dmaPending[1] = true
	s.dmaEN[1] = 0 // disabled
	s.dmaMD[1] = 1 // start factor = V-Blank-OUT
	s.checkDMATrigger(1)
	if s.dmaPending[1] {
		t.Error("dmaPending not cleared when DMAEN=0 on trigger")
	}
}

func TestSCUIndirectDMAZeroCountMaxLevel0(t *testing.T) {
	s := NewSCU()
	bus := newFakeSCUBus()
	s.SetBus(bus)

	// Indirect table at address 0x1000:
	//   entry 0: count=0 (treated as max), dst=0x2000, src=0x3000, last=1.
	tableAddr := uint32(0x1000)
	bus.Write32(tableAddr+0, 0) // count=0
	bus.Write32(tableAddr+4, 0x00002000)
	bus.Write32(tableAddr+8, 0x00003000|0x80000000) // src with final bit

	s.dmaW[0] = tableAddr
	s.dmaMD[0] = 0x07 | (1 << 24) // start factor 7 (immediate) + indirect
	s.dmaAD[0] = 0x101            // read +4, write +2
	s.dmaEN[0] = 1
	s.Write(0x00, 0) // level 0 R
	s.Write(0x04, tableAddr)
	s.Write(0x10, 1) // DMAEN=1 triggers

	// Delay should be (0x100000 / 4) = 0x40000 system cycles; at least that.
	if s.dmaDelay[0] < 0x1000 {
		t.Errorf("level 0 indirect count=0 delay = %d, want at least 0x1000 (~1MB/4)", s.dmaDelay[0])
	}
}

func TestSCUIndirectDMAZeroCountMaxLevel1(t *testing.T) {
	s := NewSCU()
	bus := newFakeSCUBus()
	s.SetBus(bus)

	tableAddr := uint32(0x1000)
	bus.Write32(tableAddr+0, 0) // count=0
	bus.Write32(tableAddr+4, 0x00002000)
	bus.Write32(tableAddr+8, 0x00003000|0x80000000)

	// Level 1
	s.Write(0x20+0x04, tableAddr)
	s.Write(0x20+0x14, 0x07|(1<<24))
	s.Write(0x20+0x0C, 0x101)
	s.Write(0x20+0x10, 1)

	// Level 1 count=0 -> 0x2000, delay = 0x2000/4 = 0x800.
	if s.dmaDelay[1] < 0x400 {
		t.Errorf("level 1 indirect count=0 delay = %d, want at least 0x400 (~8KB/4)", s.dmaDelay[1])
	}
}

// fakeSCUBus is a simple in-memory bus for DMA tests.
type fakeSCUBus struct {
	mem map[uint32]uint32
}

func newFakeSCUBus() *fakeSCUBus {
	return &fakeSCUBus{mem: make(map[uint32]uint32)}
}

func (b *fakeSCUBus) Read8(addr uint32) uint8 {
	w := b.mem[addr&^3]
	shift := (3 - (addr & 3)) * 8
	return uint8(w >> shift)
}
func (b *fakeSCUBus) Read16(addr uint32) uint16 {
	w := b.mem[addr&^3]
	if addr&2 != 0 {
		return uint16(w)
	}
	return uint16(w >> 16)
}
func (b *fakeSCUBus) Read32(addr uint32) uint32 {
	return b.mem[addr&^3]
}
func (b *fakeSCUBus) Write8(addr uint32, val uint8) {
	w := b.mem[addr&^3]
	shift := (3 - (addr & 3)) * 8
	mask := uint32(0xFF) << shift
	b.mem[addr&^3] = (w &^ mask) | (uint32(val) << shift)
}
func (b *fakeSCUBus) Write16(addr uint32, val uint16) {
	w := b.mem[addr&^3]
	if addr&2 != 0 {
		b.mem[addr&^3] = (w & 0xFFFF0000) | uint32(val)
	} else {
		b.mem[addr&^3] = (w & 0x0000FFFF) | (uint32(val) << 16)
	}
}
func (b *fakeSCUBus) Write32(addr uint32, val uint32) {
	b.mem[addr&^3] = val
}

// runDirectDMA programs SCU level 0 for an immediate direct-mode
// transfer with the given parameters and triggers it. Helper for the
// alignment-and-partial-count tests below.
func runDirectDMA(s *SCU, src, dst, count, d0ad uint32) {
	s.Write(0x00, src)
	s.Write(0x04, dst)
	s.Write(0x08, count)
	s.Write(0x0C, d0ad)
	s.Write(0x14, 0x07) // factor=7 (immediate), direct mode
	s.Write(0x10, 0x01) // D0EN
}

// TestSCUDMAUnalignedAndPartial covers the matrix of count alignment
// vs source alignment vs destination alignment vs bus type vs read/
// write add encoding for the buffered DMA per SCU User's Manual Sec
// 2.1. The fast aligned-multiple-of-4 path and the byte-streaming
// slow path are both exercised.
func TestSCUDMAUnalignedAndPartial(t *testing.T) {
	const (
		// D0AD encodings: bit 8 = read add (1=+4), bits 0-2 = write
		// add index (0=disable, 1=2, 2=4, 3=8, ...).
		readInc4WriteInc2 uint32 = 0x101 // LOO: readInc=4, writeInc=2 → BBus dstStep=4
		readInc4WriteInc4 uint32 = 0x102 // RAM: readInc=4, writeInc=4
		readInc0WriteInc4 uint32 = 0x002 // fixed src: readInc=0, writeInc=4
		readInc4WriteInc8 uint32 = 0x103 // sparse: writeInc=8 → dstStep=8 RAM, 16 BBus
	)
	const (
		ramSrcBase = uint32(0x00001000)
		ramDstBase = uint32(0x00002000)
		bbusDst    = uint32(0x05C00000) // VDP1 VRAM (B-Bus)
	)

	type row struct {
		name       string
		srcOff     uint32 // offset added to src base for unaligned starts
		dstAddr    uint32 // absolute dst (lets us choose RAM or B-Bus)
		dstOff     uint32 // offset added to dst (for unaligned dst within RAM range)
		count      uint32
		d0ad       uint32
		fixedSrc   bool // for readInc=0 cases: do not advance src in expected
		bbusDouble bool // dst is on B-Bus and writeInc!=0
	}

	cases := []row{
		{name: "AlignedMultiple4_RAM", count: 16, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "Count1", count: 1, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "Count2", count: 2, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "Count3", count: 3, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "Count5", count: 5, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "Count6", count: 6, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "Count7", count: 7, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "UnalignedSrcOff1", srcOff: 1, count: 8, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "UnalignedSrcOff2", srcOff: 2, count: 8, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "UnalignedSrcOff3", srcOff: 3, count: 8, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "UnalignedDstOff2", dstOff: 2, count: 8, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "ManualWorkedExample", srcOff: 1, dstOff: 2, count: 80, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "UnalignedSrcDstSameOff1", srcOff: 1, dstOff: 1, count: 16, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
		{name: "BBusUnalignedTail", count: 6, d0ad: readInc4WriteInc2, dstAddr: bbusDst, bbusDouble: true},
		{name: "FixedSrcCount3", srcOff: 1, count: 3, d0ad: readInc0WriteInc4, dstAddr: ramDstBase, fixedSrc: true},
		{name: "SparseWriteInc8Count16_RAM", count: 16, d0ad: readInc4WriteInc8, dstAddr: ramDstBase},
		{name: "ZeroCount", count: 0, d0ad: readInc4WriteInc4, dstAddr: ramDstBase},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSCU()
			mb := newMockBus()
			s.SetBus(mb)

			src := ramSrcBase + tc.srcOff
			dst := tc.dstAddr + tc.dstOff

			// Fill 256 bytes at src with values 0x01..0x100 so each
			// position is uniquely identifiable.
			for i := uint32(0); i < 256; i++ {
				mb.Write8(ramSrcBase+i, byte(i+1))
			}
			// Place a sentinel byte just before and after the dst
			// window to verify no over-run/under-run.
			mb.Write8(dst-1, 0xAA)
			mb.Write8(dst+tc.count, 0xBB)

			runDirectDMA(s, src, dst, tc.count, tc.d0ad)

			// Compute expected dst bytes.
			expected := make([]byte, tc.count)
			for i := uint32(0); i < tc.count; i++ {
				if tc.fixedSrc {
					// readInc=0: every byte read returns the byte at
					// src (mockBus is plain memory, not a FIFO).
					expected[i] = byte(tc.srcOff + 1)
				} else {
					expected[i] = byte(tc.srcOff + i + 1)
				}
			}

			// Sparse-stride path truncates count to whole long-words
			// per the manual's silent-on-byte-tail-with-sparse-stride
			// case. dstStep>4 means consecutive long-words are
			// non-contiguous; expected layout is a sparse copy of
			// units count/4 long-words.
			dstStep := writeIncFromAD(tc.d0ad)
			if tc.bbusDouble {
				dstStep *= 2
			}
			if dstStep > 4 {
				units := tc.count / 4
				for i := uint32(0); i < units; i++ {
					srcLW := src + i*4
					dstLW := dst + i*dstStep
					for k := uint32(0); k < 4; k++ {
						got := mb.Read8(dstLW + k)
						want := mb.Read8(srcLW + k)
						if got != want {
							t.Errorf("sparse: dst[%X+%d] = 0x%02X, want 0x%02X", dstLW, k, got, want)
						}
					}
				}
				return
			}

			// Normal path: dst[0..count) should match expected.
			for i, want := range expected {
				got := mb.Read8(dst + uint32(i))
				if got != want {
					t.Errorf("dst[%d] = 0x%02X, want 0x%02X", i, got, want)
				}
			}
			// Sentinels untouched.
			if got := mb.Read8(dst - 1); got != 0xAA {
				t.Errorf("under-run: dst[-1] = 0x%02X, want 0xAA", got)
			}
			if got := mb.Read8(dst + tc.count); got != 0xBB {
				t.Errorf("over-run: dst[%d] = 0x%02X, want 0xBB", tc.count, got)
			}
		})
	}
}

// writeIncFromAD decodes the long-word write stride from the D*AD
// register's bits 0-2 per Table 3.3.
func writeIncFromAD(ad uint32) uint32 {
	return dmaWriteAdd[ad&0x07]
}

// TestSCUDMALOOFMVTail is a focused regression test for the Legend
// of Oasis FMV black-box bug: a per-frame command-list refresh DMA
// programs count=$42 (66 bytes) so the trailing 2 bytes of the
// command list (the $8000 end-bit ctrl word) land at VDP1 VRAM
// $05C00040..$05C00041. Before the fix the trailing 2 bytes were
// dropped by truncating count to 64 and the end-bit was missing.
func TestSCUDMALOOFMVTail(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	const (
		src   uint32 = 0x06097A8C
		dst   uint32 = 0x05C00000
		count uint32 = 0x42 // 66 bytes
	)

	// Fill the source: 64 bytes of pattern, then $80, $00 at the tail
	// to represent the end-bit ctrl word.
	for i := uint32(0); i < 64; i++ {
		mb.Write8(src+i, byte(i+1))
	}
	mb.Write8(src+64, 0x80)
	mb.Write8(src+65, 0x00)

	// Sentinel just past the expected dst window.
	mb.Write8(dst+0x42, 0xCC)

	// LOO encoding: D0AD = 0x101 (read-add=+4, write-add=2 = 2 bytes).
	// On B-Bus (VDP1 VRAM) dstStep is doubled to 4.
	runDirectDMA(s, src, dst, count, 0x101)

	for i := uint32(0); i < 64; i++ {
		got := mb.Read8(dst + i)
		want := byte(i + 1)
		if got != want {
			t.Errorf("dst[%d] = 0x%02X, want 0x%02X", i, got, want)
		}
	}
	if got := mb.Read8(dst + 0x40); got != 0x80 {
		t.Errorf("dst[$40] = 0x%02X, want 0x80 (end-bit hi byte)", got)
	}
	if got := mb.Read8(dst + 0x41); got != 0x00 {
		t.Errorf("dst[$41] = 0x%02X, want 0x00 (end-bit lo byte)", got)
	}
	// Reading the ctrl word as a big-endian uint16 should give $8000.
	if got := mb.Read16(dst + 0x40); got != 0x8000 {
		t.Errorf("dst[$40..41] as u16 = 0x%04X, want 0x8000", got)
	}
	if got := mb.Read8(dst + 0x42); got != 0xCC {
		t.Errorf("over-run: dst[$42] = 0x%02X, want 0xCC", got)
	}
}

// TestSCUDMAManualWorkedExample reproduces the alignment shape from
// SCU User's Manual Sec 2.1: source with a 3-byte unaligned head
// (offset 1) and destination with a 2-byte unaligned head (offset
// 2), 80 bytes total. The 4-byte controller buffer mediates between
// the two independent alignments. The byte stream from src to dst
// must be identity: byte at src+i lands at dst+i. Addresses are
// chosen so src and dst regions do not overlap (the manual's
// literal 1H/6H addresses with 80-byte count would overlap and the
// outcome under self-overlap is timing-dependent).
func TestSCUDMAManualWorkedExample(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	const (
		src   uint32 = 0x1001 // 3-byte unaligned head (matches manual's 1H)
		dst   uint32 = 0x2006 // 2-byte unaligned head (matches manual's 6H)
		count uint32 = 0x50
	)

	for i := uint32(0); i < count; i++ {
		mb.Write8(src+i, byte((i+1)&0xFF))
	}
	mb.Write8(dst-1, 0xAA)
	mb.Write8(dst+count, 0xBB)

	// readInc=4, writeInc=4 (RAM target).
	runDirectDMA(s, src, dst, count, 0x102)

	for i := uint32(0); i < count; i++ {
		got := mb.Read8(dst + i)
		want := byte((i + 1) & 0xFF)
		if got != want {
			t.Errorf("dst[%d] = 0x%02X, want 0x%02X", i, got, want)
		}
	}
	if got := mb.Read8(dst - 1); got != 0xAA {
		t.Errorf("under-run: dst[-1] = 0x%02X, want 0xAA", got)
	}
	if got := mb.Read8(dst + count); got != 0xBB {
		t.Errorf("over-run: dst[%d] = 0x%02X, want 0xBB", count, got)
	}
}

// TestSCUDMAFastPathRegression guards the aligned-multiple-of-4 path
// against drift. This case must produce bit-identical bus traffic to
// the pre-refactor implementation (one Read32 + one Write32 per
// long-word, no byte fallbacks).
func TestSCUDMAFastPathRegression(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0xDEADBEEF)
	mb.Write32(0x1004, 0xCAFEBABE)
	mb.Write32(0x1008, 0x12345678)
	mb.Write32(0x100C, 0xAAAABBBB)

	runDirectDMA(s, 0x1000, 0x2000, 16, 0x102)

	cases := []struct {
		addr uint32
		want uint32
	}{
		{0x2000, 0xDEADBEEF},
		{0x2004, 0xCAFEBABE},
		{0x2008, 0x12345678},
		{0x200C, 0xAAAABBBB},
	}
	for _, c := range cases {
		got := mb.Read32(c.addr)
		if got != c.want {
			t.Errorf("dst[0x%X] = 0x%08X, want 0x%08X", c.addr, got, c.want)
		}
	}
}
