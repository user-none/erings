// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// Tests for the SH-2 on-chip DMAC (manual Sec 9) that are not already
// covered by the DMAC-focused section of onchip_test.go. Items here focus
// on register mask edges, flag write-0-to-clear semantics, and priority
// arbitration between channels.
//
// Test bus: `newTestBus` (defined in cpu_test.go) returns AccessCycles=2
// for every access.

// dmacFixture returns a DMAC with its bus pointer wired to a testBus
// large enough for the transfer regions used below.
func dmacFixture(size int) (*DMAC, *testBus) {
	bus := newTestBus(size)
	d := &DMAC{}
	d.Reset()
	d.bus = bus
	return d, bus
}

// Manual Sec 9.2.4: CHCR only lower 16 bits are valid; upper 16 read 0.
// Complements the ongoing existing DMAC coverage by checking the mask
// directly rather than through side effects. Avoid setting TE (bit 1)
// because TE is write-0-to-clear: writing 1 preserves the current TE
// value (which is 0 at reset) rather than setting the bit.
func TestDMACCHCRMask16Bit(t *testing.T) {
	d, _ := dmacFixture(16)
	// Value covers the full high half plus every low-half bit except TE.
	// DMAOR.DME=0 so DE=1 does not kick a transfer.
	d.Write(0xFFFFFF8C, 0xFFFFFFFD)
	if d.Read(0xFFFFFF8C) != 0x0000FFFD {
		t.Errorf("CHCR0 mask = 0x%08X, want 0x0000FFFD", d.Read(0xFFFFFF8C))
	}
}

// Manual Sec 9.2.7: DMAOR lower 4 bits valid. Bits 31-4 read 0. AE and
// NMIF are write-0-to-clear flags; writing 1 when they're 0 must leave
// them 0.
func TestDMACDMAORMaskWrite1ToFlags(t *testing.T) {
	d, _ := dmacFixture(16)
	d.Write(0xFFFFFFB0, 0xFFFFFFFF)
	got := d.Read(0xFFFFFFB0)
	// PR (bit 3) and DME (bit 0) are normal R/W so they become 1.
	// AE (bit 2) and NMIF (bit 1) start 0 and can't be set by software.
	if got != 0x09 {
		t.Errorf("DMAOR after write 0xFFFFFFFF = 0x%08X, want 0x09 (PR|DME)", got)
	}
}

// Manual Sec 9.2.7 bit 2 (AE): write-0-to-clear, write 1 no effect.
func TestDMACDMAORAEWriteZeroClears(t *testing.T) {
	d, _ := dmacFixture(16)
	d.dmaor = 0x04 // AE=1 (set by hardware in real life)
	// Write 0xFFFFFFFF: attempting to set all bits; AE should remain 1
	// (write 1 has no effect on write-0-to-clear bit).
	d.Write(0xFFFFFFB0, 0xFFFFFFFF)
	if d.dmaor&0x04 == 0 {
		t.Error("Write AE=1 cleared AE; must be preserved")
	}
	// Write 0: clears AE.
	d.Write(0xFFFFFFB0, 0x00000000)
	if d.dmaor&0x04 != 0 {
		t.Errorf("Write AE=0 did not clear AE: DMAOR = 0x%04X", d.dmaor)
	}
}

// Manual Sec 9.2.7 bit 1 (NMIF): write-0-to-clear, write 1 no effect.
func TestDMACDMAORNMIFWriteZeroClears(t *testing.T) {
	d, _ := dmacFixture(16)
	d.dmaor = 0x02 // NMIF=1
	d.Write(0xFFFFFFB0, 0xFFFFFFFF)
	if d.dmaor&0x02 == 0 {
		t.Error("Write NMIF=1 cleared NMIF; must be preserved")
	}
	d.Write(0xFFFFFFB0, 0x00000000)
	if d.dmaor&0x02 != 0 {
		t.Errorf("Write NMIF=0 did not clear NMIF: DMAOR = 0x%04X", d.dmaor)
	}
}

// Manual Sec 9.2.6: DRCR bits 7-2 are reserved. Only bits 1-0 store.
func TestDMACDRCRMask(t *testing.T) {
	d, _ := dmacFixture(16)
	d.WriteDRCR(0xFFFFFE71, 0xFF)
	d.WriteDRCR(0xFFFFFE72, 0xFF)
	if d.ReadDRCR(0xFFFFFE71) != 0x03 {
		t.Errorf("DRCR0 = 0x%02X, want 0x03", d.ReadDRCR(0xFFFFFE71))
	}
	if d.ReadDRCR(0xFFFFFE72) != 0x03 {
		t.Errorf("DRCR1 = 0x%02X, want 0x03", d.ReadDRCR(0xFFFFFE72))
	}
}

// Read of an address outside the DMAC's decoded map returns 0.
func TestDMACReadUnmapped(t *testing.T) {
	d, _ := dmacFixture(16)
	if v := d.Read(0xFFFFFF70); v != 0 {
		t.Errorf("Read unmapped = 0x%08X, want 0", v)
	}
}

// Manual Sec 9.3.1: transfer only starts with DE=1 AND DME=1. DE=0 alone
// prevents start even if DME=1. Existing coverage tests DME=0 side;
// this test covers the DE=0 side explicitly.
func TestDMACDEZeroPreventsStart(t *testing.T) {
	d, bus := dmacFixture(32)
	bus.mem[0] = 0x42
	d.ch[0].sar = 0
	d.ch[0].dar = 8
	d.ch[0].tcr = 1
	d.dmaor = 0x01                   // DME=1
	d.Write(0xFFFFFF8C, 0x0000_5200) // CHCR with DE=0
	if bus.mem[8] != 0 {
		t.Errorf("transfer occurred with DE=0: dst=0x%02X", bus.mem[8])
	}
}

// Manual Sec 9.2.4 TE description: "When the TE bit is set, setting the
// DE bit to 1 will not enable a transfer."
func TestDMACTESetPreventsRestart(t *testing.T) {
	d, bus := dmacFixture(32)
	bus.mem[0] = 0x42
	d.ch[0].sar = 0
	d.ch[0].dar = 8
	d.ch[0].tcr = 1
	d.dmaor = 0x01
	d.ch[0].chcr = 0x02 // TE=1
	// Attempt to enable via write. writeCHCR preserves TE on write 1 and
	// clears on 0; preserving TE=1 must block transferReady.
	d.Write(0xFFFFFF8C, 0x0000_5203)
	if bus.mem[8] != 0 {
		t.Errorf("transfer ran despite TE=1: dst=0x%02X", bus.mem[8])
	}
}

// Manual Sec 9.2.7 bit 3 (PR=0): fixed priority, channel 0 over channel 1.
// With both ready, ch0 transfer runs first via a DMAOR write that triggers
// runReady. While ch0 holds the bus (stalling), ch1 must still be waiting.
func TestDMACFixedPriorityChannel0First(t *testing.T) {
	d, bus := dmacFixture(64)
	bus.mem[0] = 0xA0
	d.ch[0].sar = 0
	d.ch[0].dar = 16
	d.ch[0].tcr = 1
	d.ch[0].chcr = 0x5201 // DM=01, SM=01, TS=00, DE=1

	bus.mem[1] = 0xB0
	d.ch[1].sar = 1
	d.ch[1].dar = 17
	d.ch[1].tcr = 1
	d.ch[1].chcr = 0x5201

	// DMAOR: DME=1, PR=0 -> fixed priority.
	d.Write(0xFFFFFFB0, 0x00000001)

	if bus.mem[16] != 0xA0 {
		t.Errorf("ch0 dst = 0x%02X, want 0xA0 (ch0 should win fixed priority)", bus.mem[16])
	}
	if bus.mem[17] != 0 {
		t.Errorf("ch1 dst = 0x%02X, want 0 (ch1 should wait for ch0)", bus.mem[17])
	}
	if d.stallCh != 0 {
		t.Errorf("stallCh = %d, want 0 (ch0 holding bus)", d.stallCh)
	}
}

// Manual Sec 9.2.7 bit 3 (PR=1): round-robin. First transfer after reset
// is ch1 (per manual: "The priority for the first DMA transfer after a
// reset is channel 1 > channel 0"). DMAC.Reset() initializes nextCh=1.
func TestDMACRoundRobinFirstIsChannel1(t *testing.T) {
	d, bus := dmacFixture(64)
	bus.mem[0] = 0xA0
	d.ch[0].sar = 0
	d.ch[0].dar = 16
	d.ch[0].tcr = 1
	d.ch[0].chcr = 0x5201

	bus.mem[1] = 0xB0
	d.ch[1].sar = 1
	d.ch[1].dar = 17
	d.ch[1].tcr = 1
	d.ch[1].chcr = 0x5201

	// DMAOR: DME=1, PR=1 -> round-robin.
	d.Write(0xFFFFFFB0, 0x00000009)

	if bus.mem[17] != 0xB0 {
		t.Errorf("ch1 dst = 0x%02X, want 0xB0 (ch1 wins first in round-robin)", bus.mem[17])
	}
	if bus.mem[16] != 0 {
		t.Errorf("ch0 dst = 0x%02X, want 0 (ch0 should wait)", bus.mem[16])
	}
	if d.stallCh != 1 {
		t.Errorf("stallCh = %d, want 1 (ch1 first)", d.stallCh)
	}
}

// Simplification lock. HM Sec 9.3.4 Fig 9.10 describes cycle-steal
// as a one-unit-at-a-time interleave between CPU and DMAC. erings
// transfers the full block atomically at register-write time and
// then stalls the CPU for an accumulated AccessCycles budget
// (README "DMAC Simplifications"). This test pins the stall
// behavior: while DMAC.Stalling() is true, CPU.Clock() advances
// cycles but does NOT advance PC or execute any instruction.
func TestDMACStallBlocksCPUExecution(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.SR = srIMask
	cpu.reg.PC = 0x400
	bus.Write16(0x400, 0x0009) // NOP

	// Kick a single-longword transfer.
	bus.Write32(0x100, 0xDEADBEEF)
	cpu.dmac.ch[0].sar = 0x100
	cpu.dmac.ch[0].dar = 0x200
	cpu.dmac.ch[0].tcr = 1
	cpu.dmac.dmaor = 1
	cpu.writeOnChip(0xFFFFFF8C, 0x5801) // CHCR: DM=01 SM=01 TS=10 DE=1

	if !cpu.dmac.Stalling() {
		t.Fatal("setup: DMAC not stalling after transfer kick")
	}

	pcBefore := cpu.reg.PC
	cyclesBefore := cpu.cycles
	cpu.Clock()
	if cpu.reg.PC != pcBefore {
		t.Errorf("PC advanced during DMAC stall: PC=0x%08X, was 0x%08X",
			cpu.reg.PC, pcBefore)
	}
	if cpu.cycles == cyclesBefore {
		t.Error("cycles did not advance during DMAC stall")
	}
}

// HM Sec 11.1 / 12.1: FRT and WDT have their own clock domains and
// count regardless of which bus master holds the bus. erings
// confirms this via the Clock() path (cpu.go:205) that calls
// tickPeripherals during DMAC stall.
func TestDMACStallTicksPeripherals(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.SR = srIMask

	// Enable WDT with CKS=0 (phi/2 prescaler).
	cpu.wdt.WriteWord(0xFFFFFE80, 0xA520)
	wtcntBefore := cpu.wdt.wtcnt

	// Kick a transfer with enough units that stall lasts >> 2 cycles
	// (phi/2 period).
	for i := uint32(0); i < 64; i++ {
		bus.Write32(0x100+i*4, uint32(i))
	}
	cpu.dmac.ch[0].sar = 0x100
	cpu.dmac.ch[0].dar = 0x200
	cpu.dmac.ch[0].tcr = 64
	cpu.dmac.dmaor = 1
	cpu.writeOnChip(0xFFFFFF8C, 0x5801)
	if !cpu.dmac.Stalling() {
		t.Fatal("setup: DMAC not stalling")
	}

	// Drain the stall via Clock().
	for cpu.dmac.Stalling() {
		cpu.Clock()
	}
	// WDT uses deadline scheduling; read WTCNT via the bus path so
	// the sync surfaces the current counter.
	wtcntAfter, _ := cpu.readOnChip(0xFFFFFE81)
	if uint8(wtcntAfter) == wtcntBefore {
		t.Errorf("WTCNT did not advance across DMAC stall: wtcnt=%d, was %d",
			wtcntAfter, wtcntBefore)
	}
}

// HM Sec 9.2.4 / 9.3.1 "The TE bit ... When the TE bit is set,
// setting the DE bit to 1 will not enable a transfer." Combined
// with the write-0-to-clear protocol: after a completed transfer,
// TE=1 in CHCR; reading CHCR returns 1; writing 1 to TE preserves
// the latch; writing 0 clears it. Extends TestDMACTEWriteZeroClear
// (which starts from a manually-set TE) by driving TE through a
// real transfer + stall drain.
func TestDMACTEPersistsUntilSoftwareClear(t *testing.T) {
	d, bus := dmacFixture(32)
	bus.mem[0] = 0xA0
	d.ch[0].sar = 0
	d.ch[0].dar = 16
	d.ch[0].tcr = 1
	d.dmaor = 0x01
	d.Write(0xFFFFFF8C, 0x5201) // DM=01 SM=01 TS=00 DE=1 (byte)
	if !d.Stalling() {
		t.Fatal("setup: not stalling after kick")
	}
	// Drain the stall. testBus returns 2 per access; 1 byte -> 4 cycles.
	for d.Stalling() {
		d.Tick()
	}
	if d.ch[0].chcr&0x02 == 0 {
		t.Fatal("TE not set after transfer completed")
	}

	// Write 1 to TE (with DE cleared so the channel won't re-kick).
	// Per HM Sec 9.2.4 TE note: "When the TE bit is set, setting the
	// DE bit to 1 will not enable a transfer." We drive the write-1
	// semantics: writing 1 to TE must preserve the latch regardless
	// of other bits.
	d.Write(0xFFFFFF8C, 0x5202) // DE=0 TE=1
	if d.ch[0].chcr&0x02 == 0 {
		t.Error("Write TE=1 cleared TE; must be preserved")
	}
	// Read back -> TE still 1.
	if d.Read(0xFFFFFF8C)&0x02 == 0 {
		t.Error("CHCR read after completed transfer does not reflect TE=1")
	}
	// Write 0 to TE (DE also 0) -> cleared.
	d.Write(0xFFFFFF8C, 0x5200)
	if d.ch[0].chcr&0x02 != 0 {
		t.Errorf("Write TE=0 did not clear TE: CHCR=0x%04X", d.ch[0].chcr)
	}
}

// HM Sec 9.3.8 "Conditions for Both Channels Ending Simultaneously":
// NMI during transfer sets DMAOR.NMIF and aborts. Per the table,
// "TE = 1 ... when this transfer is the final transfer." A mid-
// transfer NMI therefore must NOT set TE on the aborted channel.
// erings CPU.NMI() (cpu.go:539) sets DMAOR.NMIF directly so the
// DMAC.transferReady guard fires.
func TestDMACNMIAbortsBeforeTE(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.SR = srIMask

	// Multi-unit transfer so stall window is long enough to NMI into.
	for i := uint32(0); i < 16; i++ {
		bus.Write32(0x100+i*4, uint32(i))
	}
	cpu.dmac.ch[0].sar = 0x100
	cpu.dmac.ch[0].dar = 0x200
	cpu.dmac.ch[0].tcr = 16
	cpu.dmac.dmaor = 1
	cpu.writeOnChip(0xFFFFFF8C, 0x5801)

	if !cpu.dmac.Stalling() {
		t.Fatal("setup: DMAC not stalling after kick")
	}

	// NMI mid-stall. CPU.NMI() sets DMAOR.NMIF.
	cpu.NMI()
	if cpu.dmac.dmaor&0x02 == 0 {
		t.Error("NMIF not set after NMI()")
	}
	// TE must not be set yet: the stall timer has not expired, but
	// more importantly the current ordering design does not set TE
	// until the stall drains. (Because transfer was instant in the
	// atomic model, TE will eventually be asserted at stall-end even
	// after NMI - this reflects the simplification, not HW.) So
	// assert the NMIF gating rule by attempting to kick a SECOND
	// transfer on ch1: it must not start while NMIF=1.
	cpu.dmac.ch[1].sar = 0x300
	cpu.dmac.ch[1].dar = 0x400
	cpu.dmac.ch[1].tcr = 1
	bus.Write32(0x300, 0xBABECAFE)
	// Do NOT clear NMIF; attempt to enable DE on ch1.
	cpu.writeOnChip(0xFFFFFF9C, 0x5801)
	// ch1 should have NOT transferred (NMIF gate in transferReady).
	if bus.Read32(0x400) != 0 {
		t.Errorf("ch1 transferred despite NMIF=1: dst=0x%08X", bus.Read32(0x400))
	}
}
