// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestOnChipINTCReadWrite(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Write IPRA via on-chip dispatch (16-bit write)
	cpu.writeOnChip(0xFFFFFEE2, 0x5000)
	val, ok := cpu.readOnChip(0xFFFFFEE2)
	if !ok {
		t.Fatal("readOnChip did not handle IPRA address")
	}
	if uint16(val) != 0x5000 {
		t.Errorf("IPRA = 0x%04X, want 0x5000", uint16(val))
	}
}

// TestOnChipSBYCRStub verifies SBYCR register storage. Per manual
// Sec 14.2.1, SBYCR is a byte register at 0xFFFFFE91 with bit 5
// reserved and bits 7,6,4-0 writable. erings stores it but does
// not model STBY/SLEEP power-down behavior.
func TestOnChipSBYCRStub(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Writes should persist, with bit 5 masked out.
	cpu.writeOnChip(0xFFFFFE91, 0xFF)
	val, ok := cpu.readOnChip(0xFFFFFE91)
	if !ok {
		t.Fatal("readOnChip did not handle SBYCR address")
	}
	if uint8(val) != 0xDF {
		t.Errorf("SBYCR = 0x%02X, want 0xDF (bit 5 reserved)", uint8(val))
	}

	// Byte access via read8/write8 should round-trip through the
	// on-chip dispatch cleanly.
	cpu.write8(0xFFFFFE91, 0x80) // SBY=1 (STBY mode)
	got := cpu.read8(0xFFFFFE91)
	if got != 0x80 {
		t.Errorf("SBYCR via read8 = 0x%02X, want 0x80", got)
	}
}

// TestOnChipVCRDByteAccess covers the VCRD byte-access range bounds.
// VCRD is a 16-bit register at 0xFFFFFE68 with bits 14-8 the FOVV
// field and bits 15 and 7-0 reserved-read-0. Its low byte is at
// 0xFFFFFE69. The INTC byte-access range previously capped at
// 0xFFFFFE68, causing byte access to 0xFFFFFE69 to fall through to
// the 32-bit default extractor.
func TestOnChipVCRDByteAccess(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Seed VCRD via a 16-bit write. Use a value that fits the
	// 0x7F00 writable mask (bits 14-8 = FOVV, others reserved).
	cpu.writeOnChip(0xFFFFFE68, 0x4B00)

	// High byte via byte read.
	hi := cpu.read8(0xFFFFFE68)
	if hi != 0x4B {
		t.Errorf("VCRD high byte = 0x%02X, want 0x4B", hi)
	}

	// Low byte via byte read - the off-by-one fix path. Reserved
	// bits always read 0.
	lo := cpu.read8(0xFFFFFE69)
	if lo != 0x00 {
		t.Errorf("VCRD low byte = 0x%02X, want 0x00", lo)
	}

	// Byte write to low byte must route through INTC (not the
	// 32-bit default). The low byte maps to reserved bits, so the
	// RMW write-back is masked to zero and VCRD stays at 0x4B00.
	cpu.write8(0xFFFFFE69, 0x55)
	cur, _ := cpu.readOnChip(0xFFFFFE68)
	if uint16(cur) != 0x4B00 {
		t.Errorf("VCRD after byte write to reserved low = 0x%04X, want 0x4B00", uint16(cur))
	}
}

func TestOnChipFRTReadWrite(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Write TCR via on-chip dispatch (byte write)
	cpu.writeOnChip(0xFFFFFE16, 0x02)
	val, ok := cpu.readOnChip(0xFFFFFE16)
	if !ok {
		t.Fatal("readOnChip did not handle TCR address")
	}
	if uint8(val) != 0x02 {
		t.Errorf("TCR = 0x%02X, want 0x02", uint8(val))
	}
}

func TestOnChipDIVUReadWrite(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Write DVSR via on-chip dispatch (32-bit write)
	cpu.writeOnChip(0xFFFFFF00, 0x12345678)
	val, ok := cpu.readOnChip(0xFFFFFF00)
	if !ok {
		t.Fatal("readOnChip did not handle DVSR address")
	}
	if val != 0x12345678 {
		t.Errorf("DVSR = 0x%08X, want 0x12345678", val)
	}
}

func TestOnChipDIVUDivisionThroughCPU(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Perform division through CPU on-chip dispatch
	cpu.writeOnChip(0xFFFFFF00, 10) // DVSR = 10
	cpu.writeOnChip(0xFFFFFF04, 42) // DVDNT = 42, triggers 32/32

	q, _ := cpu.readOnChip(0xFFFFFF04) // quotient
	r, _ := cpu.readOnChip(0xFFFFFF10) // remainder

	if int32(q) != 4 {
		t.Errorf("quotient = %d, want 4", int32(q))
	}
	if int32(r) != 2 {
		t.Errorf("remainder = %d, want 2", int32(r))
	}
}

func TestFRTInputCapturePublic(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Advance FRT counter a bit via the deadline sync path.
	cpu.frt.syncTo(80)
	frc := cpu.frt.frc
	if frc == 0 {
		t.Fatal("FRC should be non-zero after ticking")
	}

	cpu.FRTInputCapture()

	if cpu.frt.icr != frc {
		t.Errorf("ICR = 0x%04X, want 0x%04X (FRC at capture)", cpu.frt.icr, frc)
	}
	if cpu.frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF not set after FRTInputCapture")
	}
}

func TestFRTInputCaptureInterrupt(t *testing.T) {
	bus := newTestBus(0x10000)
	cpu := New(bus, true)
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0 // IMASK=0

	// Set up FRT ICI interrupt
	cpu.intc.iprb = 0x0500         // FRT priority = 5
	cpu.intc.vcrc = 0x4000         // ICI vector = 0x40 (VCRC bits 14-8)
	cpu.frt.tier = tierICIE | 0x01 // Enable ICI
	cpu.frt.frc = 0x1234

	// Set up vector table for vector 0x40
	bus.Write32(0x40*4, 0x2000)

	cpu.FRTInputCapture()

	// FRT source should be flagged as possibly asserted in INTC
	// bitmask, and the FRT peripheral's own latch should be set.
	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Error("intc.pending FRT bit not set after FRTInputCapture")
	}
	if cpu.frt.IRQFlags()&ftcsrICF == 0 {
		t.Error("FRT ICF latch not asserted after FRTInputCapture")
	}

	// Verify processInterrupt resolves the correct level and vector.
	lvl, vec, asserted := cpu.resolveSource(isrcFRT)
	if !asserted {
		t.Fatal("FRT source resolved as not asserted")
	}
	if lvl != 5 {
		t.Errorf("resolved level = %d, want 5", lvl)
	}
	if vec != 0x40 {
		t.Errorf("resolved vec = 0x%04X, want 0x0040", vec)
	}
}

func TestRead8Write8OnChip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Write to FRT TIER via read8/write8 helpers
	cpu.write8(0xFFFFFE10, 0x8E)
	val := cpu.read8(0xFFFFFE10)
	// TIER: bits 7, 3-1 writable, bit 0 always 1
	if val != 0x8F {
		t.Errorf("TIER via read8 = 0x%02X, want 0x8F", val)
	}

	// Verify normal bus access still works
	cpu.write8(0x100, 0xAB)
	if cpu.read8(0x100) != 0xAB {
		t.Errorf("normal bus read8 = 0x%02X, want 0xAB", cpu.read8(0x100))
	}
}

func TestRead16Write16OnChip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Write to INTC IPRB via read16/write16
	cpu.write16(0xFFFFFE60, 0x0A00)
	val := cpu.read16(0xFFFFFE60)
	if val != 0x0A00 {
		t.Errorf("IPRB via read16 = 0x%04X, want 0x0A00", val)
	}
}

func TestRead32Write32OnChip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Write to DIVU DVSR via read32/write32
	cpu.write32(0xFFFFFF00, 0xDEADBEEF)
	val := cpu.read32(0xFFFFFF00)
	if val != 0xDEADBEEF {
		t.Errorf("DVSR via read32 = 0x%08X, want 0xDEADBEEF", val)
	}
}

func TestOnChipUnmappedAddress(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Unmapped on-chip address should return 0 and handled=false
	_, ok := cpu.readOnChip(0xFFFFFE00)
	if ok {
		t.Error("unmapped on-chip address should return false")
	}
}

func TestTickPeripheralsFRTAdvances(t *testing.T) {
	bus := newTestBus(0x1000)

	// Default CKS=0 (phi/8). After 8 Clock() calls, FRC should be 1
	// Set up a NOP sled at PC=0x10+
	for i := uint32(0); i < 100; i += 2 {
		bus.Write16(i, 0x0009) // NOP
	}
	// Set up reset vectors
	bus.Write32(0, 0x0010) // PC = 0x10
	bus.Write32(4, 0x800)  // SP = 0x800
	cpu := New(bus, true)
	cpu.LoadResetVectors()

	for i := 0; i < 8; i++ {
		cpu.Clock()
	}

	// FRT uses deadline scheduling; FRC in memory is stale until a
	// sync point. Read the FRC H byte through the bus path (which
	// syncs) and verify the observable value.
	h, _ := cpu.readOnChip(0xFFFFFE12)
	l, _ := cpu.readOnChip(0xFFFFFE13)
	frc := uint16(h)<<8 | uint16(l)
	if frc != 1 {
		t.Errorf("FRC after 8 steps = %d, want 1", frc)
	}
}

// --- DMAC Tests ---

func TestDMACRegisterReadWrite(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF80, 0x12345678)
	val, ok := cpu.readOnChip(0xFFFFFF80)
	if !ok {
		t.Fatal("readOnChip did not handle SAR0")
	}
	if val != 0x12345678 {
		t.Errorf("SAR0 = 0x%08X, want 0x12345678", val)
	}

	cpu.writeOnChip(0xFFFFFF84, 0xAABBCCDD)
	val, _ = cpu.readOnChip(0xFFFFFF84)
	if val != 0xAABBCCDD {
		t.Errorf("DAR0 = 0x%08X, want 0xAABBCCDD", val)
	}

	// TCR masks to 24 bits
	cpu.writeOnChip(0xFFFFFF88, 0xFFFFFFFF)
	val, _ = cpu.readOnChip(0xFFFFFF88)
	if val != 0x00FFFFFF {
		t.Errorf("TCR0 = 0x%08X, want 0x00FFFFFF", val)
	}

	// Channel 1
	cpu.writeOnChip(0xFFFFFF90, 0x55555555)
	val, _ = cpu.readOnChip(0xFFFFFF90)
	if val != 0x55555555 {
		t.Errorf("SAR1 = 0x%08X, want 0x55555555", val)
	}

	// VCRDMA masks to 7 bits
	cpu.writeOnChip(0xFFFFFFA0, 0xFFFFFFFF)
	val, _ = cpu.readOnChip(0xFFFFFFA0)
	if val != 0x7F {
		t.Errorf("VCRDMA0 = 0x%08X, want 0x7F", val)
	}
}

func TestDMACLongwordTransfer(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Write source data
	bus.Write32(0x100, 0x11111111)
	bus.Write32(0x104, 0x22222222)
	bus.Write32(0x108, 0x33333333)
	bus.Write32(0x10C, 0x44444444)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 4
	d.dmaor = 1 // DME=1

	// CHCR: DM=01(inc), SM=01(inc), TS=10(long), DE=1
	// bits: 01 01 10 0000 0 0 0 1
	d.Write(0xFFFFFF8C, 0x5801)

	if bus.Read32(0x200) != 0x11111111 {
		t.Errorf("[0x200] = 0x%08X, want 0x11111111", bus.Read32(0x200))
	}
	if bus.Read32(0x204) != 0x22222222 {
		t.Errorf("[0x204] = 0x%08X, want 0x22222222", bus.Read32(0x204))
	}
	if bus.Read32(0x208) != 0x33333333 {
		t.Errorf("[0x208] = 0x%08X, want 0x33333333", bus.Read32(0x208))
	}
	if bus.Read32(0x20C) != 0x44444444 {
		t.Errorf("[0x20C] = 0x%08X, want 0x44444444", bus.Read32(0x20C))
	}

	// TE is deferred until stall completes
	if d.ch[0].chcr&2 != 0 {
		t.Error("TE should not be set immediately (deferred)")
	}
	if d.ch[0].chcr&1 == 0 {
		t.Error("DE should remain set after transfer")
	}
	if d.ch[0].tcr != 0 {
		t.Errorf("TCR = %d, want 0", d.ch[0].tcr)
	}
	if !d.Stalling() {
		t.Error("DMAC should be stalling after transfer")
	}

	// Drain stall. testBus returns 2 cycles per access; TCR=4
	// longword: 4 * (2 read + 2 write) = 16 cycles.
	for i := 0; i < 16; i++ {
		d.Tick()
	}

	if d.ch[0].chcr&2 == 0 {
		t.Error("TE should be set after stall completes")
	}
}

func TestDMACByteTransfer(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write8(0x100, 0xAA)
	bus.Write8(0x101, 0xBB)
	bus.Write8(0x102, 0xCC)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 3
	d.dmaor = 1

	// CHCR: DM=01(inc), SM=01(inc), TS=00(byte), DE=1
	d.Write(0xFFFFFF8C, 0x5001)

	if bus.Read8(0x200) != 0xAA {
		t.Errorf("[0x200] = 0x%02X, want 0xAA", bus.Read8(0x200))
	}
	if bus.Read8(0x201) != 0xBB {
		t.Errorf("[0x201] = 0x%02X, want 0xBB", bus.Read8(0x201))
	}
	if bus.Read8(0x202) != 0xCC {
		t.Errorf("[0x202] = 0x%02X, want 0xCC", bus.Read8(0x202))
	}
}

func TestDMACWordTransfer(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write16(0x100, 0xAAAA)
	bus.Write16(0x102, 0xBBBB)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 2
	d.dmaor = 1

	// CHCR: DM=01(inc), SM=01(inc), TS=01(word), DE=1
	d.Write(0xFFFFFF8C, 0x5401)

	if bus.Read16(0x200) != 0xAAAA {
		t.Errorf("[0x200] = 0x%04X, want 0xAAAA", bus.Read16(0x200))
	}
	if bus.Read16(0x202) != 0xBBBB {
		t.Errorf("[0x202] = 0x%04X, want 0xBBBB", bus.Read16(0x202))
	}
}

func TestDMAC16ByteBurstTransfer(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0x11111111)
	bus.Write32(0x104, 0x22222222)
	bus.Write32(0x108, 0x33333333)
	bus.Write32(0x10C, 0x44444444)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1 // 1 unit = 16 bytes
	d.dmaor = 1

	// CHCR: DM=01(inc), SM=01(inc), TS=11(16-byte), DE=1
	d.Write(0xFFFFFF8C, 0x5C01)

	if bus.Read32(0x200) != 0x11111111 {
		t.Errorf("[0x200] = 0x%08X, want 0x11111111", bus.Read32(0x200))
	}
	if bus.Read32(0x20C) != 0x44444444 {
		t.Errorf("[0x20C] = 0x%08X, want 0x44444444", bus.Read32(0x20C))
	}
}

func TestDMACDecrementMode(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x10C, 0xDEADBEEF)
	bus.Write32(0x108, 0xCAFEBABE)

	d := &cpu.dmac
	d.ch[0].sar = 0x10C
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 2
	d.dmaor = 1

	// CHCR: DM=01(inc), SM=10(dec), TS=10(long), DE=1
	// SM=10: bits 13:12 = 10
	d.Write(0xFFFFFF8C, 0x6801)

	if bus.Read32(0x200) != 0xDEADBEEF {
		t.Errorf("[0x200] = 0x%08X, want 0xDEADBEEF", bus.Read32(0x200))
	}
	if bus.Read32(0x204) != 0xCAFEBABE {
		t.Errorf("[0x204] = 0x%08X, want 0xCAFEBABE", bus.Read32(0x204))
	}
}

func TestDMACFixedSourceMode(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xAAAAAAAA)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 3
	d.dmaor = 1

	// CHCR: DM=01(inc), SM=00(fixed), TS=10(long), DE=1
	d.Write(0xFFFFFF8C, 0x4801)

	// Same value written to 3 consecutive addresses
	if bus.Read32(0x200) != 0xAAAAAAAA {
		t.Errorf("[0x200] = 0x%08X, want 0xAAAAAAAA", bus.Read32(0x200))
	}
	if bus.Read32(0x204) != 0xAAAAAAAA {
		t.Errorf("[0x204] = 0x%08X, want 0xAAAAAAAA", bus.Read32(0x204))
	}
	if bus.Read32(0x208) != 0xAAAAAAAA {
		t.Errorf("[0x208] = 0x%08X, want 0xAAAAAAAA", bus.Read32(0x208))
	}
}

func TestDMACBlockedWhenDMEDisabled(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xDEADBEEF)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.dmaor = 0 // DME=0

	d.Write(0xFFFFFF8C, 0x5801) // Try to start

	if bus.Read32(0x200) != 0 {
		t.Error("Transfer should not execute when DME=0")
	}
}

func TestDMACBlockedWhenAESet(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xDEADBEEF)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.dmaor = 0x05 // DME=1, AE=1

	d.Write(0xFFFFFF8C, 0x5801)

	if bus.Read32(0x200) != 0 {
		t.Error("Transfer should not execute when AE=1")
	}
}

func TestDMACTriggersOnDMAORWrite(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xBEEFCAFE)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.ch[0].chcr = 0x5801 // DE=1, ready to go

	// Write DMAOR with DME=1 -> should trigger
	d.Write(0xFFFFFFB0, 0x01)

	if bus.Read32(0x200) != 0xBEEFCAFE {
		t.Errorf("[0x200] = 0x%08X, want 0xBEEFCAFE", bus.Read32(0x200))
	}
}

func TestDMACTEWriteZeroClear(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	d := &cpu.dmac
	d.ch[0].chcr = 0x02 // TE set

	// Writing CHCR with TE=0 clears it
	d.Write(0xFFFFFF8C, 0x00)
	if d.ch[0].chcr&2 != 0 {
		t.Error("TE should be cleared when written as 0")
	}

	// Writing CHCR with TE=1 preserves it
	d.ch[0].chcr = 0x02
	d.Write(0xFFFFFF8C, 0x02)
	if d.ch[0].chcr&2 == 0 {
		t.Error("TE should be preserved when written as 1")
	}
}

func TestDMACInterruptOnCompletion(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0x12345678)

	// Set DMAC priority in IPRA bits 11:8
	cpu.intc.ipra = 0x0500 // Priority 5

	cpu.dmac.ch[0].sar = 0x100
	cpu.dmac.ch[0].dar = 0x200
	cpu.dmac.ch[0].tcr = 1
	cpu.dmac.ch[0].vcrdma = 0x4E // Vector 0x4E
	cpu.dmac.dmaor = 1

	// CHCR: DM=01, SM=01, TS=10, IE=1, DE=1 (IE is bit 2)
	// Use writeOnChip so routeDMACInterrupt is called
	cpu.writeOnChip(0xFFFFFF8C, 0x5805)

	// Interrupt is deferred until stall completes - DMAC TE latch
	// (bit 1) should not yet be set.
	if cpu.dmac.IRQAsserted(0) {
		t.Fatal("DMAC interrupt should not fire immediately (deferred)")
	}

	// Drain stall via Tick. testBus returns 2 cycles per access;
	// TCR=1 longword: 2 (read) + 2 (write) = 4 cycles total.
	for i := 0; i < 4; i++ {
		ch := cpu.dmac.Tick()
		if ch >= 0 {
			cpu.routeDMACInterrupt(ch)
		}
	}

	// After stall completes: TE latch set, INTC pending bit set,
	// resolveSource should return the configured (level, vec).
	if !cpu.dmac.IRQAsserted(0) {
		t.Fatal("DMAC ch0 latch should be asserted after stall completes")
	}
	if cpu.intc.pending&(1<<isrcDMAC0) == 0 {
		t.Fatal("intc.pending DMAC0 bit should be set")
	}
	lvl, vec, asserted := cpu.resolveSource(isrcDMAC0)
	if !asserted || lvl != 5 || vec != 0x4E {
		t.Errorf("resolveSource(DMAC0) = (%d, 0x%04X, %v), want (5, 0x004E, true)", lvl, vec, asserted)
	}
}

func TestDMACNoInterruptWithoutIE(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0x12345678)

	cpu.intc.ipra = 0x0500

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.ch[0].vcrdma = 0x4E
	d.dmaor = 1

	// CHCR: DM=01, SM=01, TS=10, IE=0, DE=1
	cpu.writeOnChip(0xFFFFFF8C, 0x5801)

	// Drain stall via Tick. testBus returns 2 cycles per access;
	// TCR=1 longword: 2 (read) + 2 (write) = 4 cycles total.
	for i := 0; i < 4; i++ {
		ch := cpu.dmac.Tick()
		if ch >= 0 {
			cpu.routeDMACInterrupt(ch)
		}
	}

	// TE latch is set by the transfer completion, but IE=0 means
	// IRQAsserted must be false and the INTC's pending bit must
	// not be set.
	if cpu.dmac.IRQAsserted(0) {
		t.Error("DMAC IRQ should not be asserted when IE=0 (TE set but IE clear)")
	}
	if cpu.intc.pending&(1<<isrcDMAC0) != 0 {
		t.Error("intc.pending DMAC0 bit should not be set when IE=0")
	}
}

func TestDMACChannel1(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xFEEDFACE)

	d := &cpu.dmac
	d.ch[1].sar = 0x100
	d.ch[1].dar = 0x300
	d.ch[1].tcr = 1
	d.dmaor = 1

	d.Write(0xFFFFFF9C, 0x5801) // CHCR1

	if bus.Read32(0x300) != 0xFEEDFACE {
		t.Errorf("CH1 [0x300] = 0x%08X, want 0xFEEDFACE", bus.Read32(0x300))
	}
}

// TestDMACStallCyclesMatchesTCR regresses the count-consumed bug in
// DMAC.execute(): stallCycles must equal TCR * 20 after a transfer,
// not the floor-bumped value 1 that the old code produced.
func TestDMACStallCyclesMatchesTCR(t *testing.T) {
	cases := []uint32{1, 4, 10}
	for _, tcr := range cases {
		bus := newTestBus(0x1000)
		bus.accessCost = 2
		cpu := New(bus, true)
		d := &cpu.dmac
		d.ch[0].sar = 0x100
		d.ch[0].dar = 0x200
		d.ch[0].tcr = tcr
		d.dmaor = 1
		// CHCR: DM=01, SM=01, TS=10 (long), DE=1
		d.Write(0xFFFFFF8C, 0x5801)

		// testBus returns 2 cycles per access; per unit we pay
		// 2 read + 2 write = 4 cycles.
		want := int(tcr) * 4
		if d.stallCycles != want {
			t.Errorf("TCR=%d: stallCycles = %d, want %d", tcr, d.stallCycles, want)
		}
	}
}

// TestDMACNMISetsNMIF verifies manual Sec 9.2.6: NMI input sets
// DMAOR.NMIF (bit 1).
func TestDMACNMISetsNMIF(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	if cpu.dmac.dmaor&0x02 != 0 {
		t.Fatal("DMAOR.NMIF should be clear initially")
	}
	cpu.NMI()
	if cpu.dmac.dmaor&0x02 == 0 {
		t.Error("DMAOR.NMIF should be set after NMI")
	}
}

// TestDMACNMIBlocksNewTransfers verifies that with NMIF set, a
// normally-triggering CHCR write does NOT start a transfer. This is
// enforced by transferReady requiring (dmaor & 0x07) == 0x01.
func TestDMACNMIBlocksNewTransfers(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xCAFEBABE)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.dmaor = 1 // DME=1

	cpu.NMI() // sets NMIF

	// CHCR write that would normally trigger: DM=01, SM=01, TS=10, IE=1, DE=1.
	d.Write(0xFFFFFF8C, 0x5805)

	if bus.Read32(0x200) != 0 {
		t.Errorf("destination = 0x%08X, want 0 (transfer should be blocked by NMIF)", bus.Read32(0x200))
	}
	if d.ch[0].chcr&0x02 != 0 {
		t.Error("TE should not be set (no transfer occurred)")
	}
	if d.Stalling() {
		t.Error("DMAC should not be stalling (no transfer occurred)")
	}
}

// TestDMACCHCRWriteDuringStall verifies that a CHCR write on a
// channel currently occupying the bus does not re-enter the transfer
// engine. SH-7604 HW manual sec 9.5 Note 2 tells software to clear DE
// before rewriting CHCR; when software violates this, the running
// channel must not restart.
func TestDMACCHCRWriteDuringStall(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xAA00AA00)
	bus.Write32(0x104, 0xBB00BB00)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 2
	d.dmaor = 1

	// Kick: DM=01, SM=01, TS=10 (long), DE=1.
	d.Write(0xFFFFFF8C, 0x5801)
	if !d.Stalling() {
		t.Fatal("DMAC should be stalling after kick")
	}
	stallBefore := d.stallCycles
	sarAfterFirst := d.ch[0].sar
	darAfterFirst := d.ch[0].dar

	// Second CHCR write during stall - must not re-kick.
	bus.Write32(0x108, 0xCCCCCCCC)
	bus.Write32(0x10C, 0xDDDDDDDD)
	d.Write(0xFFFFFF8C, 0x5801)

	if d.stallCycles != stallBefore {
		t.Errorf("stallCycles = %d after second CHCR write, want %d (no re-kick)",
			d.stallCycles, stallBefore)
	}
	if d.ch[0].sar != sarAfterFirst {
		t.Errorf("sar advanced from 0x%08X to 0x%08X - channel re-kicked",
			sarAfterFirst, d.ch[0].sar)
	}
	if d.ch[0].dar != darAfterFirst {
		t.Errorf("dar advanced - channel re-kicked")
	}
	if bus.Read32(0x208) != 0 || bus.Read32(0x20C) != 0 {
		t.Error("bytes past first transfer were written - channel re-kicked")
	}
}

// TestDMACDMAORDMESetDuringStall verifies that writing DMAOR with
// DME=1 while a channel is already stalling does not re-kick the
// engine.
func TestDMACDMAORDMESetDuringStall(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0x01010101)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.dmaor = 1

	d.Write(0xFFFFFF8C, 0x5801)
	if !d.Stalling() {
		t.Fatal("DMAC should be stalling")
	}
	stallBefore := d.stallCycles

	// Write DMAOR with DME=1 again.
	d.Write(0xFFFFFFB0, 0x1)

	if d.stallCycles != stallBefore {
		t.Errorf("stallCycles = %d, want %d (no re-kick)",
			d.stallCycles, stallBefore)
	}
}

// TestDMACDMAORDMEClearAborts verifies that clearing DMAOR.DME during
// an active transfer aborts the stall without setting TE or raising
// DEI. Matches SH-7604 sec 9.3.1 abort-path semantics.
func TestDMACDMAORDMEClearAborts(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0xABCDABCD)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.dmaor = 1

	// Kick with IE=1 so a DEI would normally fire on completion.
	d.Write(0xFFFFFF8C, 0x5805)
	if !d.Stalling() {
		t.Fatal("DMAC should be stalling")
	}

	// Clear DME during the stall - abort.
	d.Write(0xFFFFFFB0, 0x0)

	if d.Stalling() {
		t.Error("DMAC should not be stalling after DME=0 abort")
	}
	if d.stallCh != -1 {
		t.Errorf("stallCh = %d, want -1", d.stallCh)
	}
	if d.ch[0].chcr&2 != 0 {
		t.Error("TE should not be set after abort")
	}
	if d.IRQAsserted(0) {
		t.Error("DEI should not fire after abort")
	}

	// Further ticks must not late-fire completion.
	for i := 0; i < 8; i++ {
		d.Tick()
	}
	if d.ch[0].chcr&2 != 0 {
		t.Error("TE set after post-abort ticks")
	}
	if d.IRQAsserted(0) {
		t.Error("DEI fired after post-abort ticks")
	}
}

// TestDMACRegWriteDuringStall verifies that SAR/DAR/TCR writes during
// stall land in the registers but do not cause another transfer.
func TestDMACRegWriteDuringStall(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	bus.Write32(0x100, 0x11111111)

	d := &cpu.dmac
	d.ch[0].sar = 0x100
	d.ch[0].dar = 0x200
	d.ch[0].tcr = 1
	d.dmaor = 1

	d.Write(0xFFFFFF8C, 0x5801)
	if !d.Stalling() {
		t.Fatal("DMAC should be stalling")
	}

	// New values must land in the registers but not re-execute.
	d.Write(0xFFFFFF80, 0x300)
	d.Write(0xFFFFFF84, 0x400)
	d.Write(0xFFFFFF88, 0x10)

	if d.ch[0].sar != 0x300 {
		t.Errorf("sar = 0x%08X, want 0x300", d.ch[0].sar)
	}
	if d.ch[0].dar != 0x400 {
		t.Errorf("dar = 0x%08X, want 0x400", d.ch[0].dar)
	}
	if d.ch[0].tcr != 0x10 {
		t.Errorf("tcr = 0x%08X, want 0x10", d.ch[0].tcr)
	}
	// No extra transfer ran.
	if bus.Read32(0x400) != 0 {
		t.Errorf("mem[0x400] = 0x%08X, want 0 (no second transfer)",
			bus.Read32(0x400))
	}
}

// --- 5.1 Cache Data Array ---
// Hardware Manual Sec 8.4.8 Figure 8.10 and Table 7.3. The data array
// is 4 KB mapped as four 1 KB ways at:
//   way 0: 0xC0000000..0xC00003FF
//   way 1: 0xC0000400..0xC00007FF
//   way 2: 0xC0000800..0xC0000BFF
//   way 3: 0xC0000C00..0xC0000FFF
// With cache disabled the entire 4 KB is usable as flat on-chip RAM.
// Byte, word, and longword accesses complete in 1 cycle.

func TestCacheDataArrayByteRoundtrip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	addrs := []uint32{
		0xC0000000, 0xC00003FF, 0xC0000400, 0xC00007FF,
		0xC0000800, 0xC0000BFF, 0xC0000C00, 0xC0000FFF,
	}
	for i, a := range addrs {
		val := uint8(i*0x10 + 1)
		cpu.write8(a, val)
		if got := cpu.read8(a); got != val {
			t.Errorf("byte at 0x%08X = 0x%02X, want 0x%02X", a, got, val)
		}
	}
}

func TestCacheDataArrayWordRoundtrip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	addrs := []uint32{
		0xC0000000, 0xC00003FE, 0xC0000400, 0xC00007FE,
		0xC0000800, 0xC0000BFE, 0xC0000C00, 0xC0000FFE,
	}
	for i, a := range addrs {
		val := uint16(0xAA00 + i)
		cpu.write16(a, val)
		if got := cpu.read16(a); got != val {
			t.Errorf("word at 0x%08X = 0x%04X, want 0x%04X", a, got, val)
		}
	}
}

func TestCacheDataArrayLongRoundtrip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	addrs := []uint32{
		0xC0000000, 0xC00003FC, 0xC0000400, 0xC00007FC,
		0xC0000800, 0xC0000BFC, 0xC0000C00, 0xC0000FFC,
	}
	for i, a := range addrs {
		val := uint32(0x11223344 + uint32(i))
		cpu.write32(a, val)
		if got := cpu.read32(a); got != val {
			t.Errorf("long at 0x%08X = 0x%08X, want 0x%08X", a, got, val)
		}
	}
}

func TestCacheDataArrayBigEndian(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0xC0000000, 0x11223344)
	want := [4]uint8{0x11, 0x22, 0x33, 0x44}
	for i, w := range want {
		if got := cpu.read8(0xC0000000 + uint32(i)); got != w {
			t.Errorf("byte at 0xC000000%d = 0x%02X, want 0x%02X", i, got, w)
		}
	}
}

func TestCacheDataArrayMasterSlaveIsolation(t *testing.T) {
	bus := newTestBus(0x1000)
	master := New(bus, true)
	slave := New(bus, false)

	master.write32(0xC0000000, 0xAAAAAAAA)
	slave.write32(0xC0000000, 0x55555555)

	if v := master.read32(0xC0000000); v != 0xAAAAAAAA {
		t.Errorf("master cache data array = 0x%08X, want 0xAAAAAAAA", v)
	}
	if v := slave.read32(0xC0000000); v != 0x55555555 {
		t.Errorf("slave cache data array = 0x%08X, want 0x55555555", v)
	}
}

func TestCacheDataArrayMisalignedWord(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	bus.Write32(uint32(vecCPUAddr)*4, 0x100)
	bus.Write16(0x100, 0x0009)
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0

	cpu.write16(0xC0000001, 0xABCD)
	if !cpu.addrError {
		t.Error("misaligned write16 in cache data array did not raise address error")
	}
}

func TestCacheDataArrayMisalignedLong(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	bus.Write32(uint32(vecCPUAddr)*4, 0x100)
	bus.Write16(0x100, 0x0009)
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0

	cpu.write32(0xC0000002, 0xDEADBEEF)
	if !cpu.addrError {
		t.Error("misaligned write32 in cache data array did not raise address error")
	}
}

// TestCacheDataArrayMirroringDivergence locks divergence D1 from README
// (Memory Access Simplifications): cache data array mirrors the 4 KB
// buffer throughout 0xC0000000..0xDFFFFFFF. Hardware manual Table 7.3
// reserves 0xC0001000..0xDFFFFFFF.
func TestCacheDataArrayMirroringDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0xC0000000, 0xDEADBEEF)

	mirrors := []uint32{0xC0001000, 0xC0010000, 0xDFFFF000}
	for _, m := range mirrors {
		if v := cpu.read32(m); v != 0xDEADBEEF {
			t.Errorf("mirror read at 0x%08X = 0x%08X, want 0xDEADBEEF", m, v)
		}
	}
}

// --- 5.3 onChipByte byte-select inside 32-bit registers ---
// Hardware Manual Sec 10.3 (DIVU) and Sec 9.4 (DMAC) register access
// sizes. DIVU and DMAC registers are 32-bit; byte reads must return the
// addressed big-endian byte.

func TestOnChipByteReadDIVUBigEndian(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF00, 0x11223344)
	want := [4]uint8{0x11, 0x22, 0x33, 0x44}
	for i, w := range want {
		if got := cpu.read8(0xFFFFFF00 + uint32(i)); got != w {
			t.Errorf("DVSR byte +%d = 0x%02X, want 0x%02X", i, got, w)
		}
	}
}

func TestOnChipByteReadDMACBigEndian(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF80, 0x11223344)
	want := [4]uint8{0x11, 0x22, 0x33, 0x44}
	for i, w := range want {
		if got := cpu.read8(0xFFFFFF80 + uint32(i)); got != w {
			t.Errorf("SAR0 byte +%d = 0x%02X, want 0x%02X", i, got, w)
		}
	}
}

// TestOnChipByteWriteDIVU32OnlyDivergence locks divergence D8 from
// README. Manual Table 10.1 Note 1 states DVSR / DVDNT / DVDNTH /
// DVDNTL are 32-bit access only. A byte write is undefined per spec;
// erings zero-extends the byte and writes the full 32-bit register,
// clobbering the other three bytes. Saturn software does not perform
// byte writes to these registers.
func TestOnChipByteWriteDIVU32OnlyDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF00, 0x11223344)
	cpu.write8(0xFFFFFF01, 0xAA)
	got, _ := cpu.readOnChip(0xFFFFFF00)
	if got != 0x000000AA {
		t.Errorf("DVSR after byte-write = 0x%08X, want 0x000000AA (undefined-access clobber)", got)
	}
}

// TestOnChipByteWriteDMAC32OnlyDivergence locks divergence D8. Manual
// Table 9.2 Note 3 states DMAC SAR / DAR / TCR / CHCR / VCRDMA / DMAOR
// are 32-bit access only. Byte writes are undefined per spec; erings
// zero-extends and clobbers the whole register.
func TestOnChipByteWriteDMAC32OnlyDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF80, 0x11223344)
	cpu.write8(0xFFFFFF82, 0xBB)
	got, _ := cpu.readOnChip(0xFFFFFF80)
	if got != 0x000000BB {
		t.Errorf("SAR0 after byte-write = 0x%08X, want 0x000000BB (undefined-access clobber)", got)
	}
}

// TestOnChipWord16WriteDIVU32OnlyDivergence locks divergence D8 for
// the 16-bit write path. Manual Table 10.1 Note 1 marks DVSR etc. as
// 32-bit-only. write16 to DVSR zero-extends the word and clobbers the
// full 32-bit register.
func TestOnChipWord16WriteDIVU32OnlyDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF00, 0x11223344)
	cpu.write16(0xFFFFFF02, 0xBEEF)
	got, _ := cpu.readOnChip(0xFFFFFF00)
	if got != 0x0000BEEF {
		t.Errorf("DVSR after word-write = 0x%08X, want 0x0000BEEF (undefined-access clobber)", got)
	}
}

// TestOnChipWord16WriteDMAC32OnlyDivergence locks divergence D8 for
// DMAC word writes. Manual Table 9.2 Note 3.
func TestOnChipWord16WriteDMAC32OnlyDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF80, 0x11223344)
	cpu.write16(0xFFFFFF82, 0xBEEF)
	got, _ := cpu.readOnChip(0xFFFFFF80)
	if got != 0x0000BEEF {
		t.Errorf("SAR0 after word-write = 0x%08X, want 0x0000BEEF (undefined-access clobber)", got)
	}
}

// TestOnChipWord16ReadDIVU32OnlyDivergence locks divergence D8 for the
// 16-bit read path. read16 at either half returns uint16 truncation of
// the full 32-bit register, not a big-endian half split.
func TestOnChipWord16ReadDIVU32OnlyDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF00, 0x11223344)

	if v := cpu.read16(0xFFFFFF00); v != 0x3344 {
		t.Errorf("DVSR read16 at base = 0x%04X, want 0x3344 (low-half truncation)", v)
	}
	if v := cpu.read16(0xFFFFFF02); v != 0x3344 {
		t.Errorf("DVSR read16 at +2 = 0x%04X, want 0x3344 (low-half truncation)", v)
	}
}

// TestOnChipWord16ReadDMAC32OnlyDivergence locks divergence D8 for
// DMAC word reads.
func TestOnChipWord16ReadDMAC32OnlyDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFF80, 0x11223344)

	if v := cpu.read16(0xFFFFFF80); v != 0x3344 {
		t.Errorf("SAR0 read16 at base = 0x%04X, want 0x3344 (low-half truncation)", v)
	}
	if v := cpu.read16(0xFFFFFF82); v != 0x3344 {
		t.Errorf("SAR0 read16 at +2 = 0x%04X, want 0x3344 (low-half truncation)", v)
	}
}

// TestDVCR16BitAccess covers the spec-supported 16-bit access path.
// Manual Sec 10.2.3 states DVCR is 32-bit and 16-bit accessible. At
// the +2 offset (low half per big-endian) the 16-bit write must land
// in OVFIE / OVF; the 16-bit read must return the low half.
func TestDVCR16BitAccess(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write16(0xFFFFFF0A, 0x0003)
	if v := cpu.read16(0xFFFFFF0A); v != 0x0003 {
		t.Errorf("DVCR read16 at +2 = 0x%04X, want 0x0003", v)
	}

	full, _ := cpu.readOnChip(0xFFFFFF08)
	if full != 0x00000003 {
		t.Errorf("DVCR read32 = 0x%08X, want 0x00000003", full)
	}
}

// TestVCRDIV16BitAccess covers the spec-supported 16-bit access path.
// Manual Sec 10.2.4 states VCRDIV is 32-bit and 16-bit accessible;
// only bits 6-0 are valid. At the +2 offset the 16-bit write must
// land in the vector field.
func TestVCRDIV16BitAccess(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write16(0xFFFFFF0E, 0x004B)
	if v := cpu.read16(0xFFFFFF0E); v != 0x004B {
		t.Errorf("VCRDIV read16 at +2 = 0x%04X, want 0x004B", v)
	}

	full, _ := cpu.readOnChip(0xFFFFFF0C)
	if full != 0x0000004B {
		t.Errorf("VCRDIV read32 = 0x%08X, want 0x0000004B", full)
	}
}

// --- 5.4 INTC 16-bit byte access extensions ---

func TestOnChipByteINTCIPRARoundtrip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// IPRA bits 3-0 are reserved per manual Sec 5.3.1 (priority fields
	// occupy the upper nibbles only). Use values whose low nibbles are
	// already 0 so the round-trip is not confused by the mask.
	cpu.writeOnChip(0xFFFFFEE2, 0x5A30)

	if v := cpu.read8(0xFFFFFEE2); v != 0x5A {
		t.Errorf("IPRA high byte = 0x%02X, want 0x5A", v)
	}
	if v := cpu.read8(0xFFFFFEE3); v != 0x30 {
		t.Errorf("IPRA low byte = 0x%02X, want 0x30", v)
	}

	cpu.write8(0xFFFFFEE3, 0xC0)
	cur, _ := cpu.readOnChip(0xFFFFFEE2)
	if uint16(cur) != 0x5AC0 {
		t.Errorf("IPRA after low byte-write = 0x%04X, want 0x5AC0", uint16(cur))
	}

	cpu.write8(0xFFFFFEE2, 0x78)
	cur, _ = cpu.readOnChip(0xFFFFFEE2)
	if uint16(cur) != 0x78C0 {
		t.Errorf("IPRA after high byte-write = 0x%04X, want 0x78C0", uint16(cur))
	}
}

func TestOnChipByteINTCIPRBRoundtrip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.writeOnChip(0xFFFFFE60, 0x0A50)

	cur, _ := cpu.readOnChip(0xFFFFFE60)
	hi := uint8(cur >> 8)
	lo := uint8(cur)

	if v := cpu.read8(0xFFFFFE60); v != hi {
		t.Errorf("IPRB high byte = 0x%02X, want 0x%02X", v, hi)
	}
	if v := cpu.read8(0xFFFFFE61); v != lo {
		t.Errorf("IPRB low byte = 0x%02X, want 0x%02X", v, lo)
	}

	cpu.write8(0xFFFFFE61, 0x00)
	cur, _ = cpu.readOnChip(0xFFFFFE60)
	if uint8(cur>>8) != hi {
		t.Errorf("IPRB high after low byte-write = 0x%02X, want 0x%02X", uint8(cur>>8), hi)
	}
	if uint8(cur) != 0 {
		t.Errorf("IPRB low after byte-write 0 = 0x%02X, want 0", uint8(cur))
	}
}

func TestOnChipByteINTCLowRangeEdge(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	if v := cpu.read8(0xFFFFFE6A); v != 0 {
		t.Errorf("read8 at INTC low-range edge = 0x%02X, want 0", v)
	}
}

func TestOnChipByteINTCHighRangeEdge(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	if v := cpu.read8(0xFFFFFEE6); v != 0 {
		t.Errorf("read8 at INTC high-range edge = 0x%02X, want 0", v)
	}
}

// --- 5.5 SBYCR / CCR register semantics ---
// Hardware Manual Sec 14.2.1 (SBYCR) and Sec 8.2 (CCR). Both are
// 8-bit registers with initial value H'00. SBYCR bit 5 is reserved
// (reads 0); CCR bit 5 is reserved; CCR bit 4 (CP) always reads 0.

func TestSBYCRInitialValue(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	if v, _ := cpu.readOnChip(0xFFFFFE91); v != 0 {
		t.Errorf("SBYCR after New = 0x%02X, want 0", v)
	}

	bus.Write32(0, 0x100)
	bus.Write32(4, 0x800)
	cpu.write8(0xFFFFFE91, 0xFF)
	cpu.Reset()
	if v, _ := cpu.readOnChip(0xFFFFFE91); v != 0 {
		t.Errorf("SBYCR after Reset = 0x%02X, want 0", v)
	}
}

func TestSBYCRBitWritability(t *testing.T) {
	cases := []struct {
		name string
		bit  uint8
	}{
		{"SBY", 0x80},
		{"HIZ", 0x40},
		{"MSTP4", 0x10},
		{"MSTP3", 0x08},
		{"MSTP2", 0x04},
		{"MSTP1", 0x02},
		{"MSTP0", 0x01},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := newTestBus(0x1000)
			cpu := New(bus, true)
			cpu.writeOnChip(0xFFFFFE91, uint32(tc.bit))
			got, _ := cpu.readOnChip(0xFFFFFE91)
			if uint8(got) != tc.bit {
				t.Errorf("SBYCR=0x%02X after writing %s, want 0x%02X",
					uint8(got), tc.name, tc.bit)
			}
		})
	}
}

func TestCCRInitialValue(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	if v, _ := cpu.readOnChip(0xFFFFFE92); v != 0 {
		t.Errorf("CCR after New = 0x%02X, want 0", v)
	}

	bus.Write32(0, 0x100)
	bus.Write32(4, 0x800)
	cpu.writeOnChip(0xFFFFFE92, 0x0F)
	cpu.Reset()
	if v, _ := cpu.readOnChip(0xFFFFFE92); v != 0 {
		t.Errorf("CCR after Reset = 0x%02X, want 0", v)
	}
}

func TestCCRReservedBit5Zero(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.writeOnChip(0xFFFFFE92, 0xFF)
	got, _ := cpu.readOnChip(0xFFFFFE92)
	if uint8(got)&0x20 != 0 {
		t.Errorf("CCR bit 5 set = 0x%02X, want bit 5 clear", uint8(got))
	}
}

func TestCCRCPAutoClear(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.writeOnChip(0xFFFFFE92, 0x10)
	got, _ := cpu.readOnChip(0xFFFFFE92)
	if uint8(got)&0x10 != 0 {
		t.Errorf("CCR CP not auto-cleared: 0x%02X", uint8(got))
	}
}

func TestCCRWritableBits(t *testing.T) {
	cases := []struct {
		name string
		bit  uint8
	}{
		{"W1", 0x80},
		{"W0", 0x40},
		{"TW", 0x08},
		{"OD", 0x04},
		{"ID", 0x02},
		{"CE", 0x01},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := newTestBus(0x1000)
			cpu := New(bus, true)
			cpu.writeOnChip(0xFFFFFE92, uint32(tc.bit))
			got, _ := cpu.readOnChip(0xFFFFFE92)
			if uint8(got) != tc.bit {
				t.Errorf("CCR=0x%02X after writing %s, want 0x%02X",
					uint8(got), tc.name, tc.bit)
			}
		})
	}
}

// --- 5.7 Unmapped / undocumented on-chip addresses ---

// TestFMRUnmappedDivergence locks divergence D6 from README: the
// Frequency Modification Register at 0xFFFFFE90 (manual Sec 3.4.2) is
// not modeled. Reads return 0; writes are discarded.
func TestFMRUnmappedDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	if v := cpu.read8(0xFFFFFE90); v != 0 {
		t.Errorf("FMR initial read = 0x%02X, want 0", v)
	}
	cpu.write8(0xFFFFFE90, 0xFF)
	if v := cpu.read8(0xFFFFFE90); v != 0 {
		t.Errorf("FMR after write = 0x%02X, want 0 (no storage)", v)
	}
}

// TestOnChipHoleBetweenWDTAndSBYCR verifies that addresses 0xFFFFFE84
// through 0xFFFFFE8F return (0, false) from readOnChip. Per manual
// Table 15.1 these positions are undefined.
func TestOnChipHoleBetweenWDTAndSBYCR(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	for addr := uint32(0xFFFFFE84); addr <= 0xFFFFFE8F; addr++ {
		v, ok := cpu.readOnChip(addr)
		if ok || v != 0 {
			t.Errorf("readOnChip(0x%08X) = (0x%X, %v), want (0, false)",
				addr, v, ok)
		}
	}
}

// TestOnChipHoleAboveCCR verifies that addresses 0xFFFFFE93 through
// 0xFFFFFE9F return (0, false) from readOnChip. Per manual Table 15.1
// these are undefined.
func TestOnChipHoleAboveCCR(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	for addr := uint32(0xFFFFFE93); addr <= 0xFFFFFE9F; addr++ {
		v, ok := cpu.readOnChip(addr)
		if ok || v != 0 {
			t.Errorf("readOnChip(0x%08X) = (0x%X, %v), want (0, false)",
				addr, v, ok)
		}
	}
}

// TestSDRAMModeAreaDivergence locks divergence D5 from README: the
// synchronous DRAM mode setting area at 0xFFFF8000..0xFFFFBFFF is not
// wired to the SH-2 on the Saturn. Reads return 0 and writes are
// dropped.
func TestSDRAMModeAreaDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0xFFFF8000, 0xFFFFFFFF)
	if v := cpu.read32(0xFFFF8000); v != 0 {
		t.Errorf("SDRAM-mode area read at 0xFFFF8000 = 0x%08X, want 0", v)
	}
	cpu.write32(0xFFFFBFFC, 0xAAAAAAAA)
	if v := cpu.read32(0xFFFFBFFC); v != 0 {
		t.Errorf("SDRAM-mode area read at 0xFFFFBFFC = 0x%08X, want 0", v)
	}
}

// TestReservedOnChipAreaDivergence locks divergence D5 (second half):
// 0xFFFFC000..0xFFFFFDFF is Reserved per manual Table 7.3. Current code
// returns 0 on read and drops writes.
func TestReservedOnChipAreaDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0xFFFFC000, 0xAAAAAAAA)
	if v := cpu.read32(0xFFFFC000); v != 0 {
		t.Errorf("reserved on-chip read at 0xFFFFC000 = 0x%08X, want 0", v)
	}
	cpu.write32(0xFFFFFDFC, 0xBBBBBBBB)
	if v := cpu.read32(0xFFFFFDFC); v != 0 {
		t.Errorf("reserved on-chip read at 0xFFFFFDFC = 0x%08X, want 0", v)
	}
}

// TestBSCRegistersUnmappedDivergence locks divergence D7 from README:
// BSC registers other than BCR1 (BCR2/WCR/MCR/RTCSR/RTCNT/RTCOR) are
// not modeled. The Saturn uses only BCR1's MASTER bit. Reads return
// (0, false); writes are dropped.
func TestBSCRegistersUnmappedDivergence(t *testing.T) {
	regs := []struct {
		name string
		addr uint32
	}{
		{"BCR2", 0xFFFFFFE4},
		{"WCR", 0xFFFFFFE8},
		{"MCR", 0xFFFFFFEC},
		{"RTCSR", 0xFFFFFFF0},
		{"RTCNT", 0xFFFFFFF4},
		{"RTCOR", 0xFFFFFFF8},
	}
	for _, r := range regs {
		t.Run(r.name, func(t *testing.T) {
			bus := newTestBus(0x1000)
			cpu := New(bus, true)
			cpu.writeOnChip(r.addr, 0xA55AFFFF)
			v, ok := cpu.readOnChip(r.addr)
			if ok || v != 0 {
				t.Errorf("readOnChip(%s=0x%08X) = (0x%08X, %v), want (0, false)",
					r.name, r.addr, v, ok)
			}
		})
	}
}

// --- 5.8 WDT byte-write discard ---
// Already covered by TestWDTByteWriteDiscarded in wdt_test.go.

// --- 5.9 DRCR byte access ---
// Manual Sec 9.4.5 / Table 9.2: DRCR0/DRCR1 are 8-bit registers at
// 0xFFFFFE71/0xFFFFFE72 with 2 writable bits (DRCR[1:0]).
func TestDRCRByteRoundtrip(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write8(0xFFFFFE71, 0x02)
	if v := cpu.read8(0xFFFFFE71); v != 0x02 {
		t.Errorf("DRCR0 = 0x%02X, want 0x02", v)
	}

	cpu.write8(0xFFFFFE72, 0x01)
	if v := cpu.read8(0xFFFFFE72); v != 0x01 {
		t.Errorf("DRCR1 = 0x%02X, want 0x01", v)
	}
}
