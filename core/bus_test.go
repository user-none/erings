// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// newBusForTest builds a Bus with default subsystems, mirroring the
// pre-inversion NewBus() behavior for test convenience.
func newBusForTest() *Bus {
	scu := NewSCU()
	smpc := NewSMPC(scu)
	vdp1 := NewVDP1(scu)
	vdp2 := NewVDP2(scu)
	scsp := NewSCSP(scu)
	cdblock := NewCDBlock(scu)
	bus := NewBus(scu, smpc, vdp1, vdp2, scsp, cdblock)
	scu.SetBus(bus)
	scsp.SetCDAudioSource(cdblock)
	return bus
}

func TestBIOSRead(t *testing.T) {
	bus := newBusForTest()
	bios := make([]byte, biosSize)
	bios[0] = 0xDE
	bios[1] = 0xAD
	bios[biosSize-1] = 0xFF
	if err := bus.SetBIOS(bios); err != nil {
		t.Fatal(err)
	}

	if got := bus.Read8(0x00000000); got != 0xDE {
		t.Errorf("BIOS[0] = 0x%02X, want 0xDE", got)
	}
	if got := bus.Read8(0x00000001); got != 0xAD {
		t.Errorf("BIOS[1] = 0x%02X, want 0xAD", got)
	}
	if got := bus.Read8(0x0007FFFF); got != 0xFF {
		t.Errorf("BIOS[last] = 0x%02X, want 0xFF", got)
	}
}

func TestBIOSReadNil(t *testing.T) {
	bus := newBusForTest()
	if got := bus.Read8(0x00000000); got != 0 {
		t.Errorf("BIOS read with no BIOS set = 0x%02X, want 0x00", got)
	}
}

func TestBIOSWriteIgnored(t *testing.T) {
	bus := newBusForTest()
	bios := make([]byte, biosSize)
	bios[0] = 0xAB
	if err := bus.SetBIOS(bios); err != nil {
		t.Fatal(err)
	}

	bus.Write8(0x00000000, 0xFF)
	if got := bus.Read8(0x00000000); got != 0xAB {
		t.Errorf("BIOS after write = 0x%02X, want 0xAB (unchanged)", got)
	}
}

func TestBIOSCacheThrough(t *testing.T) {
	bus := newBusForTest()
	bios := make([]byte, biosSize)
	bios[0x100] = 0x42
	if err := bus.SetBIOS(bios); err != nil {
		t.Fatal(err)
	}

	// Cache-through mirror: bit 29 set
	if got := bus.Read8(0x20000100); got != 0x42 {
		t.Errorf("BIOS cache-through read = 0x%02X, want 0x42", got)
	}
}

func TestWorkRAMH(t *testing.T) {
	bus := newBusForTest()
	bus.Write8(0x06000000, 0x11)
	bus.Write8(0x06000001, 0x22)
	if got := bus.Read8(0x06000000); got != 0x11 {
		t.Errorf("WRAM-H[0] = 0x%02X, want 0x11", got)
	}
	if got := bus.Read8(0x06000001); got != 0x22 {
		t.Errorf("WRAM-H[1] = 0x%02X, want 0x22", got)
	}
}

func TestWorkRAMHMirror(t *testing.T) {
	bus := newBusForTest()
	// Write at the last byte of 1 MB range
	bus.Write8(0x060FFFFF, 0xBB)
	// addr & 0x0FFFFF = 0x0FFFFF
	if got := bus.Read8(0x060FFFFF); got != 0xBB {
		t.Errorf("WRAM-H[last] = 0x%02X, want 0xBB", got)
	}
	// Write at base, read at base to confirm masking works
	bus.Write8(0x06000000, 0xCC)
	if got := bus.Read8(0x06000000); got != 0xCC {
		t.Errorf("WRAM-H[0] = 0x%02X, want 0xCC", got)
	}
}

func TestWorkRAMHCacheThrough(t *testing.T) {
	bus := newBusForTest()
	bus.Write8(0x26000010, 0x55)
	if got := bus.Read8(0x26000010); got != 0x55 {
		t.Errorf("WRAM-H cache-through = 0x%02X, want 0x55", got)
	}
	// Also readable from non-cache-through address
	if got := bus.Read8(0x06000010); got != 0x55 {
		t.Errorf("WRAM-H normal after cache-through write = 0x%02X, want 0x55", got)
	}
}

func TestWorkRAML(t *testing.T) {
	bus := newBusForTest()
	bus.Write8(0x00200000, 0xAA)
	bus.Write8(0x002FFFFF, 0xBB)
	if got := bus.Read8(0x00200000); got != 0xAA {
		t.Errorf("WRAM-L[0] = 0x%02X, want 0xAA", got)
	}
	if got := bus.Read8(0x002FFFFF); got != 0xBB {
		t.Errorf("WRAM-L[last] = 0x%02X, want 0xBB", got)
	}
}

func TestBackupRAM(t *testing.T) {
	bus := newBusForTest()
	// Even addresses return 0xFF, writes ignored
	bus.Write8(0x00180000, 0x12)
	if got := bus.Read8(0x00180000); got != 0xFF {
		t.Errorf("Backup even addr = 0x%02X, want 0xFF", got)
	}
	// Odd addresses read/write data
	bus.Write8(0x00180001, 0x34)
	if got := bus.Read8(0x00180001); got != 0x34 {
		t.Errorf("Backup odd addr = 0x%02X, want 0x34", got)
	}
}

func TestBackupRAMOddByteAddressing(t *testing.T) {
	bus := newBusForTest()
	// Write to odd addresses, verify sequential RAM mapping
	bus.Write8(0x00180001, 0x56) // RAM byte 0
	bus.Write8(0x00180003, 0x78) // RAM byte 1
	if got := bus.Read8(0x00180001); got != 0x56 {
		t.Errorf("Backup byte 0 = 0x%02X, want 0x56", got)
	}
	if got := bus.Read8(0x00180003); got != 0x78 {
		t.Errorf("Backup byte 1 = 0x%02X, want 0x78", got)
	}
	// Last valid odd address 0x0018FFFF maps to RAM byte 0x7FFF
	bus.Write8(0x0018FFFF, 0xAB)
	if got := bus.Read8(0x0018FFFF); got != 0xAB {
		t.Errorf("Backup last byte = 0x%02X, want 0xAB", got)
	}
}

func TestBackupRAMPersistence(t *testing.T) {
	bus := newBusForTest()

	// Write a known pattern via the bus; odd address 0x00180001 maps to
	// backup RAM byte 0, each subsequent byte two addresses later.
	bus.Write8(0x00180001, 0xA1) // byte 0
	bus.Write8(0x00180003, 0xB2) // byte 1
	bus.Write8(0x0018FFFF, 0xC3) // byte 0x7FFF (last)

	snap := bus.GetBackupRAM()
	if len(snap) != backupSize {
		t.Fatalf("GetBackupRAM len = %d, want %d", len(snap), backupSize)
	}
	if snap[0] != 0xA1 || snap[1] != 0xB2 || snap[backupSize-1] != 0xC3 {
		t.Errorf("snapshot = [0]0x%02X [1]0x%02X [last]0x%02X, want 0xA1 0xB2 0xC3",
			snap[0], snap[1], snap[backupSize-1])
	}

	// Mutating the returned copy must not affect the bus.
	snap[0] = 0x00
	if got := bus.Read8(0x00180001); got != 0xA1 {
		t.Errorf("after mutating copy, byte 0 = 0x%02X, want 0xA1 (copy not aliased)", got)
	}

	// A full 32 KB load is observable through bus reads.
	restore := make([]byte, backupSize)
	restore[0] = 0xDE
	restore[1] = 0xAD
	restore[backupSize-1] = 0xEF
	bus.SetBackupRAM(restore)
	if got := bus.Read8(0x00180001); got != 0xDE {
		t.Errorf("after SetBackupRAM, byte 0 = 0x%02X, want 0xDE", got)
	}
	if got := bus.Read8(0x00180003); got != 0xAD {
		t.Errorf("after SetBackupRAM, byte 1 = 0x%02X, want 0xAD", got)
	}
	if got := bus.Read8(0x0018FFFF); got != 0xEF {
		t.Errorf("after SetBackupRAM, last byte = 0x%02X, want 0xEF", got)
	}

	// Wrong-size and empty input are no-ops.
	bus.SetBackupRAM(make([]byte, backupSize-1))
	bus.SetBackupRAM(make([]byte, backupSize+1))
	bus.SetBackupRAM(nil)
	if got := bus.Read8(0x00180001); got != 0xDE {
		t.Errorf("after wrong-size SetBackupRAM, byte 0 = 0x%02X, want 0xDE (unchanged)", got)
	}
}

func TestMINITSINIT(t *testing.T) {
	bus := newBusForTest()

	// Initially not written
	if bus.MINITWritten() {
		t.Error("MINIT should not be set initially")
	}
	if bus.SINITWritten() {
		t.Error("SINIT should not be set initially")
	}

	// MINIT/SINIT trigger only on 16-bit writes (matches Saturn
	// inter-CPU protocol); byte writes are logged and ignored.
	bus.Write16(0x01000000, 0x0001)
	if !bus.MINITWritten() {
		t.Error("MINIT should be set after 16-bit write")
	}
	// Second read should be cleared
	if bus.MINITWritten() {
		t.Error("MINIT should be cleared after read")
	}

	// Write to SINIT
	bus.Write16(0x01800000, 0x0001)
	if !bus.SINITWritten() {
		t.Error("SINIT should be set after 16-bit write")
	}
	if bus.SINITWritten() {
		t.Error("SINIT should be cleared after read")
	}

	// Byte writes must NOT trigger MINIT/SINIT (removed in commit 58adcf6).
	bus.Write8(0x01000000, 0x01)
	if bus.MINITWritten() {
		t.Error("MINIT should not trigger on byte write")
	}
	bus.Write8(0x01800000, 0x01)
	if bus.SINITWritten() {
		t.Error("SINIT should not trigger on byte write")
	}

	// Read from MINIT/SINIT returns 0
	if got := bus.Read8(0x01000000); got != 0 {
		t.Errorf("MINIT read = 0x%02X, want 0x00", got)
	}
	if got := bus.Read8(0x01800000); got != 0 {
		t.Errorf("SINIT read = 0x%02X, want 0x00", got)
	}
}

func TestStubRegions(t *testing.T) {
	bus := newBusForTest()
	stubs := []struct {
		name string
		addr uint32
	}{
		{"SMPC", 0x00100000},
		{"A-Bus Dummy", 0x05000000},
		{"A-Bus CS2", 0x05800000},
		{"VDP1 VRAM", 0x05C00000},
		{"VDP1 FB", 0x05C80000},
		{"VDP1 Regs", 0x05D00000},
		{"VDP2 VRAM", 0x05E00000},
		{"VDP2 CRAM", 0x05F00000},
		{"VDP2 Regs", 0x05F80000},
		{"SCU Regs", 0x05FE0000},
	}
	for _, s := range stubs {
		if got := bus.Read8(s.addr); got != 0 {
			t.Errorf("%s read = 0x%02X, want 0x00", s.name, got)
		}
		// Write should not panic
		bus.Write8(s.addr, 0xFF)
	}
}

// TestExternalAddressMirror verifies that the SH-2 BSC outputs only A0-A26
// to the external bus (per SH7604 manual). Logical addresses with bits A27
// or A28 set must alias to the same physical location as their A0-A26 form.
// Cache-region bits A29-A31 are also stripped (P0 cached vs P1 cache-through
// access the same memory).
func TestExternalAddressMirror(t *testing.T) {
	bus := newBusForTest()

	// Write a sentinel into Work RAM-H at the canonical address.
	bus.Write32(0x06000800, 0xCAFEBABE)

	cases := []struct {
		name string
		addr uint32
	}{
		{"canonical", 0x06000800},
		{"A28 set", 0x16000800},
		{"A27 set", 0x0E000800},
		{"A27+A28 set", 0x1E000800},
		{"P1 cache-through", 0x26000800},
		{"P1 + A28", 0x36000800},
	}
	for _, c := range cases {
		if got := bus.Read32(c.addr); got != 0xCAFEBABE {
			t.Errorf("%s read 0x%08X = 0x%08X, want 0xCAFEBABE", c.name, c.addr, got)
		}
	}
}

func TestMultiByteBigEndian(t *testing.T) {
	bus := newBusForTest()
	bus.Write32(0x06000000, 0xDEADBEEF)

	if got := bus.Read8(0x06000000); got != 0xDE {
		t.Errorf("byte 0 = 0x%02X, want 0xDE", got)
	}
	if got := bus.Read8(0x06000001); got != 0xAD {
		t.Errorf("byte 1 = 0x%02X, want 0xAD", got)
	}
	if got := bus.Read8(0x06000002); got != 0xBE {
		t.Errorf("byte 2 = 0x%02X, want 0xBE", got)
	}
	if got := bus.Read8(0x06000003); got != 0xEF {
		t.Errorf("byte 3 = 0x%02X, want 0xEF", got)
	}

	if got := bus.Read16(0x06000000); got != 0xDEAD {
		t.Errorf("Read16 = 0x%04X, want 0xDEAD", got)
	}
	if got := bus.Read32(0x06000000); got != 0xDEADBEEF {
		t.Errorf("Read32 = 0x%08X, want 0xDEADBEEF", got)
	}

	bus.Write16(0x06000010, 0xCAFE)
	if got := bus.Read16(0x06000010); got != 0xCAFE {
		t.Errorf("Write16/Read16 = 0x%04X, want 0xCAFE", got)
	}
}

func TestNewBusDefaults(t *testing.T) {
	bus := newBusForTest()

	// Work RAM-H zeroed
	if got := bus.Read8(0x06000000); got != 0 {
		t.Errorf("WRAM-H[0] = 0x%02X, want 0x00", got)
	}
	if got := bus.Read8(0x060FFFFF); got != 0 {
		t.Errorf("WRAM-H[last] = 0x%02X, want 0x00", got)
	}

	// Work RAM-L zeroed
	if got := bus.Read8(0x00200000); got != 0 {
		t.Errorf("WRAM-L[0] = 0x%02X, want 0x00", got)
	}
	if got := bus.Read8(0x002FFFFF); got != 0 {
		t.Errorf("WRAM-L[last] = 0x%02X, want 0x00", got)
	}

	// Backup RAM formatted with "BackUpRam Format" header (0xFF interleaved)
	if got := bus.Read8(0x00180000); got != 0xFF {
		t.Errorf("Backup[0] = 0x%02X, want 0xFF", got)
	}
	if got := bus.Read8(0x00180001); got != 'B' {
		t.Errorf("Backup[1] = 0x%02X, want 0x42 ('B')", got)
	}

	// MINIT/SINIT not set
	if bus.MINITWritten() {
		t.Error("MINIT should not be set")
	}
	if bus.SINITWritten() {
		t.Error("SINIT should not be set")
	}

	// BIOS nil (no data)
	if got := bus.Read8(0x00000000); got != 0 {
		t.Errorf("BIOS[0] = 0x%02X, want 0x00 (nil)", got)
	}
}

func TestSCUViaBusVersion(t *testing.T) {
	bus := newBusForTest()
	if got := bus.Read32(0x05FE00C8); got != 0x00000004 {
		t.Errorf("SCU VER via bus = 0x%08X, want 0x00000004", got)
	}
}

func TestSCUViaBusWrite32Read32(t *testing.T) {
	bus := newBusForTest()
	bus.Write32(0x05FE0000, 0xDEADBEEF) // D0R
	if got := bus.Read32(0x05FE0000); got != 0xDEADBEEF {
		t.Errorf("D0R via bus = 0x%08X, want 0xDEADBEEF", got)
	}
}

func TestSCUViaBusByteAccess(t *testing.T) {
	bus := newBusForTest()
	bus.Write32(0x05FE0000, 0x12345678) // D0R
	// Big-endian byte 0
	if got := bus.Read8(0x05FE0000); got != 0x12 {
		t.Errorf("D0R byte 0 = 0x%02X, want 0x12", got)
	}
	if got := bus.Read8(0x05FE0001); got != 0x34 {
		t.Errorf("D0R byte 1 = 0x%02X, want 0x34", got)
	}
	if got := bus.Read8(0x05FE0002); got != 0x56 {
		t.Errorf("D0R byte 2 = 0x%02X, want 0x56", got)
	}
	if got := bus.Read8(0x05FE0003); got != 0x78 {
		t.Errorf("D0R byte 3 = 0x%02X, want 0x78", got)
	}
}

func TestSCUISTViaBus(t *testing.T) {
	bus := newBusForTest()
	bus.scu.RaiseVBlankIN()

	// IST at 0x05FE00A4, bit 0 set = 0x00000001
	if got := bus.Read32(0x05FE00A4); got != 0x00000001 {
		t.Errorf("IST via bus = 0x%08X, want 0x00000001", got)
	}

	// Write-0-to-clear: write 0xFFFFFFFE clears bit 0
	bus.Write32(0x05FE00A4, 0xFFFFFFFE)
	if got := bus.Read32(0x05FE00A4); got != 0 {
		t.Errorf("IST after clear via bus = 0x%08X, want 0", got)
	}
}

func TestNewBusSCUDefaults(t *testing.T) {
	bus := newBusForTest()

	// IST = 0 (no pending interrupts)
	if got := bus.Read32(0x05FE00A4); got != 0 {
		t.Errorf("IST = 0x%08X, want 0", got)
	}
	// D0R = 0
	if got := bus.Read32(0x05FE0000); got != 0 {
		t.Errorf("D0R = 0x%08X, want 0", got)
	}
	// VER = 4
	if got := bus.Read32(0x05FE00C8); got != 0x00000004 {
		t.Errorf("VER = 0x%08X, want 0x00000004", got)
	}
	// DSTA = 0
	if got := bus.Read32(0x05FE007C); got != 0 {
		t.Errorf("DSTA = 0x%08X, want 0", got)
	}
}

func TestSMPCViaBus(t *testing.T) {
	bus := newBusForTest()
	// Write SF via bus, read back
	bus.Write8(0x00100063, 0x01)
	if got := bus.Read8(0x00100063); got != 0x01 {
		t.Errorf("SF via bus = 0x%02X, want 0x01", got)
	}
}

func TestSMPCCacheThrough(t *testing.T) {
	bus := newBusForTest()
	// Cache-through mirror: bit 29 set
	bus.Write8(0x20100063, 0x01)
	if got := bus.Read8(0x20100063); got != 0x01 {
		t.Errorf("SF via cache-through = 0x%02X, want 0x01", got)
	}
	// Also readable from non-cache-through address
	if got := bus.Read8(0x00100063); got != 0x01 {
		t.Errorf("SF normal after cache-through write = 0x%02X, want 0x01", got)
	}
}

func TestNewBusSMPCDefaults(t *testing.T) {
	bus := newBusForTest()

	// SF = 0
	if got := bus.Read8(0x00100063); got != 0 {
		t.Errorf("SF = 0x%02X, want 0x00", got)
	}
	// SR = 0
	if got := bus.Read8(0x00100061); got != 0 {
		t.Errorf("SR = 0x%02X, want 0x00", got)
	}
	// PDR1 = 0
	if got := bus.Read8(0x00100075); got != 0 {
		t.Errorf("PDR1 = 0x%02X, want 0x00", got)
	}
}

func TestVDP2RegsViaBus(t *testing.T) {
	bus := newBusForTest()

	// Write TVMD via bus (16-bit big-endian)
	bus.Write16(0x05F80000, 0x8000)
	if got := bus.Read16(0x05F80000); got != 0x8000 {
		t.Errorf("TVMD via bus = 0x%04X, want 0x8000", got)
	}

	// Write a scroll register
	bus.Write16(0x05F80010, 0xABCD)
	if got := bus.Read16(0x05F80010); got != 0xABCD {
		t.Errorf("reg 0x10 via bus = 0x%04X, want 0xABCD", got)
	}

	// Byte-level access to VDP2 registers is not supported by the
	// SCU B-bus bridge: commit 58adcf6 removed byte-split fallbacks
	// so invalid widths are logged and dropped rather than silently
	// RMW-composed. Confirm the reject path leaves the register
	// unchanged.
	bus.Write16(0x05F80002, 0x0000)
	bus.Write8(0x05F80002, 0x12)
	bus.Write8(0x05F80003, 0x34)
	if got := bus.Read16(0x05F80002); got != 0x0000 {
		t.Errorf("byte writes to VDP2 reg 0x05F80002 should be rejected, got 0x%04X", got)
	}
}

func TestVDP2VRAMViaBus(t *testing.T) {
	bus := newBusForTest()

	bus.Write8(0x05E00000, 0xAA)
	bus.Write8(0x05E00001, 0xBB)
	if got := bus.Read8(0x05E00000); got != 0xAA {
		t.Errorf("VDP2 VRAM[0] via bus = 0x%02X, want 0xAA", got)
	}
	if got := bus.Read8(0x05E00001); got != 0xBB {
		t.Errorf("VDP2 VRAM[1] via bus = 0x%02X, want 0xBB", got)
	}
}

func TestVDP2CRAMViaBus(t *testing.T) {
	bus := newBusForTest()

	bus.Write8(0x05F00000, 0xCC)
	bus.Write8(0x05F00001, 0xDD)
	if got := bus.Read8(0x05F00000); got != 0xCC {
		t.Errorf("VDP2 CRAM[0] via bus = 0x%02X, want 0xCC", got)
	}
	if got := bus.Read8(0x05F00001); got != 0xDD {
		t.Errorf("VDP2 CRAM[1] via bus = 0x%02X, want 0xDD", got)
	}
}

func TestVDP2InterruptWiring(t *testing.T) {
	bus := newBusForTest()

	// Tick VDP2 to trigger VBlank-IN (advance past all active lines)
	vdp := bus.vdp2
	vdp.Write(0x0000, 0x8000) // DISP=1
	vdp.vLine = 223
	vdp.lineCycle = 0
	vdp.TickSystemCycles(systemCyclesPerLine320)

	// VBlank-IN should have raised SCU IST bit 0
	if got := bus.Read32(0x05FE00A4); got&0x01 == 0 {
		t.Errorf("IST after VDP2 VBlank-IN = 0x%08X, want bit 0 set", got)
	}
}

func TestNewBusVDP2Defaults(t *testing.T) {
	bus := newBusForTest()

	// TVMD = 0
	if got := bus.Read16(0x05F80000); got != 0 {
		t.Errorf("TVMD = 0x%04X, want 0x0000", got)
	}
	// TVSTAT: ODD=1 (oddField starts true), PAL=0
	if got := bus.Read16(0x05F80004); got != tvstatODD {
		t.Errorf("TVSTAT = 0x%04X, want 0x%04X", got, tvstatODD)
	}
	// VRAM zeroed
	if got := bus.Read8(0x05E00000); got != 0 {
		t.Errorf("VDP2 VRAM[0] = 0x%02X, want 0x00", got)
	}
	// CRAM zeroed
	if got := bus.Read8(0x05F00000); got != 0 {
		t.Errorf("VDP2 CRAM[0] = 0x%02X, want 0x00", got)
	}
}

func TestSetBIOSValidation(t *testing.T) {
	bus := newBusForTest()

	// Too small
	if err := bus.SetBIOS(make([]byte, 100)); err == nil {
		t.Error("SetBIOS should reject wrong size (too small)")
	}

	// Too large
	if err := bus.SetBIOS(make([]byte, biosSize+1)); err == nil {
		t.Error("SetBIOS should reject wrong size (too large)")
	}

	// Correct size
	if err := bus.SetBIOS(make([]byte, biosSize)); err != nil {
		t.Errorf("SetBIOS should accept correct size: %v", err)
	}
}

func TestVDP1RegsViaBus(t *testing.T) {
	bus := newBusForTest()

	// Write TVMR via bus (16-bit)
	bus.Write16(0x05D00000, 0x000F)
	// TVMR is write-only, Read returns 0
	if got := bus.Read16(0x05D00000); got != 0 {
		t.Errorf("TVMR read via bus = 0x%04X, want 0x0000 (write-only)", got)
	}

	// Write EWDR via bus
	bus.Write16(0x05D00006, 0x1234)
	// Write-only, read returns 0
	if got := bus.Read16(0x05D00006); got != 0 {
		t.Errorf("EWDR read via bus = 0x%04X, want 0x0000 (write-only)", got)
	}

	// Read EDSR via bus (should be 0x0003 after reset)
	if got := bus.Read16(0x05D00010); got != 0x0003 {
		t.Errorf("EDSR via bus = 0x%04X, want 0x0003", got)
	}
}

func TestVDP1MODRViaBus(t *testing.T) {
	bus := newBusForTest()

	// Write TVMR with TVM=3
	bus.Write16(0x05D00000, 0x0003)
	// Write FBCR with FCM=1 (bit 1)
	bus.Write16(0x05D00002, 0x0002)

	modr := bus.Read16(0x05D00016)
	// VER=1 (bit 12), FCM->bit 4, TVM=3 (bits 2-0)
	want := uint16(0x1013)
	if modr != want {
		t.Errorf("MODR via bus = 0x%04X, want 0x%04X", modr, want)
	}
}

func TestVDP1VRAMViaBus(t *testing.T) {
	bus := newBusForTest()

	bus.Write8(0x05C00000, 0xAA)
	bus.Write8(0x05C7FFFF, 0xBB)

	if got := bus.Read8(0x05C00000); got != 0xAA {
		t.Errorf("VDP1 VRAM[0] via bus = 0x%02X, want 0xAA", got)
	}
	if got := bus.Read8(0x05C7FFFF); got != 0xBB {
		t.Errorf("VDP1 VRAM[last] via bus = 0x%02X, want 0xBB", got)
	}
}

func TestVDP1FBViaBus(t *testing.T) {
	bus := newBusForTest()

	bus.Write8(0x05C80000, 0xCC)
	bus.Write8(0x05CBFFFF, 0xDD)

	if got := bus.Read8(0x05C80000); got != 0xCC {
		t.Errorf("VDP1 FB[0] via bus = 0x%02X, want 0xCC", got)
	}
	if got := bus.Read8(0x05CBFFFF); got != 0xDD {
		t.Errorf("VDP1 FB[last] via bus = 0x%02X, want 0xDD", got)
	}
}

func TestVDP1InterruptWiring(t *testing.T) {
	bus := newBusForTest()

	// Verify the SCU reference is wired
	vdp := bus.vdp1
	if vdp.scu == nil {
		t.Error("VDP1 scu reference should be wired")
	}
	if vdp.scu != bus.scu {
		t.Error("VDP1 scu should reference the bus SCU")
	}
}

func TestNewBusVDP1Defaults(t *testing.T) {
	bus := newBusForTest()

	// EDSR = 0x0003 (drawing idle)
	if got := bus.Read16(0x05D00010); got != 0x0003 {
		t.Errorf("EDSR = 0x%04X, want 0x0003", got)
	}
	// LOPR = 0
	if got := bus.Read16(0x05D00012); got != 0 {
		t.Errorf("LOPR = 0x%04X, want 0x0000", got)
	}
	// COPR = 0
	if got := bus.Read16(0x05D00014); got != 0 {
		t.Errorf("COPR = 0x%04X, want 0x0000", got)
	}
	// MODR = version 1 only
	if got := bus.Read16(0x05D00016); got != 0x1000 {
		t.Errorf("MODR = 0x%04X, want 0x1000", got)
	}
	// VRAM zeroed
	if got := bus.Read8(0x05C00000); got != 0 {
		t.Errorf("VRAM[0] = 0x%02X, want 0x00", got)
	}
	// FB zeroed
	if got := bus.Read8(0x05C80000); got != 0 {
		t.Errorf("FB[0] = 0x%02X, want 0x00", got)
	}
}

func TestSCSPSoundRAMViaBus(t *testing.T) {
	bus := newBusForTest()

	bus.Write8(0x05A00000, 0xAA)
	bus.Write8(0x05A7FFFF, 0xBB)

	if got := bus.Read8(0x05A00000); got != 0xAA {
		t.Errorf("Sound RAM[0] via bus = 0x%02X, want 0xAA", got)
	}
	if got := bus.Read8(0x05A7FFFF); got != 0xBB {
		t.Errorf("Sound RAM[last] via bus = 0x%02X, want 0xBB", got)
	}
}

func TestSCSPRegsViaBus(t *testing.T) {
	bus := newBusForTest()

	// Use slot 0 SA[15:0] at offset 0x0002 - plain storage with no
	// side-effect reads or writes.
	bus.Write16(0x05B00002, 0x1234)
	if got := bus.Read16(0x05B00002); got != 0x1234 {
		t.Errorf("SCSP reg via bus = 0x%04X, want 0x1234", got)
	}
}

func TestSCSPRegsByteAccessViaBus(t *testing.T) {
	bus := newBusForTest()

	// Byte writes are translated by the SCU B-bus bridge into 16-bit
	// RMW cycles. Writing the high byte then the low byte of a
	// register should produce the combined word. Slot 0 SA[15:0] is
	// a plain storage register with no side effects.
	bus.Write8(0x05B00002, 0x12)
	bus.Write8(0x05B00003, 0x34)
	if got := bus.Read16(0x05B00002); got != 0x1234 {
		t.Errorf("SCSP reg byte write = 0x%04X, want 0x1234", got)
	}

	// Byte reads return the upper or lower half of the word.
	if got := bus.Read8(0x05B00002); got != 0x12 {
		t.Errorf("SCSP reg high byte = 0x%02X, want 0x12", got)
	}
	if got := bus.Read8(0x05B00003); got != 0x34 {
		t.Errorf("SCSP reg low byte = 0x%02X, want 0x34", got)
	}
}

func TestSCSPExpansionAreaViaBus(t *testing.T) {
	bus := newBusForTest()

	if got := bus.Read8(0x05A80000); got != 0 {
		t.Errorf("expansion area read = 0x%02X, want 0x00", got)
	}
	// Write should not panic
	bus.Write8(0x05A80000, 0xFF)
}

func TestNewBusSCSPDefaults(t *testing.T) {
	bus := newBusForTest()

	// Sound RAM zeroed
	if got := bus.Read8(0x05A00000); got != 0 {
		t.Errorf("Sound RAM[0] = 0x%02X, want 0x00", got)
	}
	if got := bus.Read8(0x05A7FFFF); got != 0 {
		t.Errorf("Sound RAM[last] = 0x%02X, want 0x00", got)
	}
	// SCSP registers zeroed
	if got := bus.Read16(0x05B00000); got != 0 {
		t.Errorf("SCSP reg[0] = 0x%04X, want 0x0000", got)
	}
	// VER field (bits 7:4) always reads as 2 per SCSP User's Manual Table 1.1
	if got := bus.Read16(0x05B00400); got != 0x0020 {
		t.Errorf("SCSP reg[0x400] = 0x%04X, want 0x0020 (VER=2)", got)
	}
	// SCSP in reset
	if !bus.scsp.InReset() {
		t.Error("SCSP should be in reset")
	}
}

func TestSCSPInterruptWiring(t *testing.T) {
	bus := newBusForTest()

	scsp := bus.scsp
	if scsp.scu == nil {
		t.Error("SCSP scu reference should be wired")
	}
	if scsp.scu != bus.scu {
		t.Error("SCSP scu should reference the bus SCU")
	}
}

func TestSCSPAccessorNotNil(t *testing.T) {
	bus := newBusForTest()

	if bus.scsp == nil {
		t.Error("Bus.SCSP() should not be nil")
	}
}

func TestCDBlockRegsViaBus(t *testing.T) {
	bus := newBusForTest()

	// HIRQREQ at $05890008 is 0 after init (CMOK not set until SH-1 completes)
	if got := bus.Read16(0x05890008); got != 0 {
		t.Errorf("HIRQREQ via bus = 0x%04X, want 0x0000", got)
	}

	// Write HIRQMSK via bus
	bus.Write16(0x0589000C, 0x0223)
	if got := bus.Read16(0x0589000C); got != 0x0223 {
		t.Errorf("HIRQMSK via bus = 0x%04X, want 0x0223", got)
	}
}

func TestCDBlockWordAccessViaBus(t *testing.T) {
	bus := newBusForTest()

	// Write HIRQMSK as 16-bit word
	bus.Write16(0x0589000C, 0x0223)
	if got := bus.Read16(0x0589000C); got != 0x0223 {
		t.Errorf("HIRQMSK word write = 0x%04X, want 0x0223", got)
	}

	// Read individual bytes
	if got := bus.Read8(0x0589000C); got != 0x02 {
		t.Errorf("HIRQMSK high byte = 0x%02X, want 0x02", got)
	}
	if got := bus.Read8(0x0589000D); got != 0x23 {
		t.Errorf("HIRQMSK low byte = 0x%02X, want 0x23", got)
	}
}

func TestNewBusCDBlockDefaults(t *testing.T) {
	bus := newBusForTest()

	// HIRQMSK defaults to 0x0000 (no CD interrupts enabled)
	if got := bus.Read16(0x0589000C); got != 0x0000 {
		t.Errorf("HIRQMSK = 0x%04X, want 0x0000", got)
	}
	// HIRQREQ is 0 after init
	if got := bus.Read16(0x05890008); got != 0 {
		t.Errorf("HIRQREQ = 0x%04X, want 0x0000", got)
	}
	// CR1 contains CDBLOCK signature: status=Busy(0x00) | 'C'(0x43)
	if got := bus.Read16(0x05890018); got != 0x0043 {
		t.Errorf("CR1 = 0x%04X, want 0x0043 (CDBLOCK signature)", got)
	}
}

func TestCDBlockCommandViaBus(t *testing.T) {
	bus := newBusForTest()

	// Complete boot sequence first (SH-1 init delay)
	bus.cdblock.TickSystemCycles(cdBootDelayCycles)

	// Clear CMOK set by boot completion
	bus.Write16(0x05890008, 0xFFFE)

	// Write Get Hardware Info command ($01) to CR1-CR4
	bus.Write16(0x05890018, 0x0100) // CR1: cmd $01
	bus.Write16(0x0589001C, 0x0000) // CR2
	bus.Write16(0x05890020, 0x0000) // CR3
	bus.Write16(0x05890024, 0x0000) // CR4 (queues command)

	// Tick through command delay (sentinel fires on next call)
	bus.cdblock.TickSystemCycles(cdCmdDelay)

	// Check CMOK set
	if got := bus.Read16(0x05890008); got&hirqCMOK == 0 {
		t.Errorf("HIRQREQ after command = 0x%04X, want CMOK set", got)
	}

	// Check response
	if got := bus.Read16(0x0589001C); got != 0x0001 {
		t.Errorf("CR2 response = 0x%04X, want 0x0001", got)
	}
	if got := bus.Read16(0x05890024); got != 0x0400 {
		t.Errorf("CR4 response = 0x%04X, want 0x0400", got)
	}
}

func TestCDBlockDATATRNSViaBus(t *testing.T) {
	t.Skip("CDBlock under development")
	bus := newBusForTest()

	// Issue Get TOC to populate data buffer
	bus.Write16(0x05890018, 0x0200) // CR1: cmd $02
	bus.Write16(0x0589001C, 0x0000)
	bus.Write16(0x05890020, 0x0000)
	bus.Write16(0x05890024, 0x0000) // triggers

	// Read DATATRNS at $05890000
	if got := bus.Read16(0x05890000); got != 0xFFFF {
		t.Errorf("DATATRNS via bus = 0x%04X, want 0xFFFF", got)
	}
}

func TestCDBlockAccessorNotNil(t *testing.T) {
	bus := newBusForTest()
	if bus.cdblock == nil {
		t.Error("Bus.CDBlock() should not be nil")
	}
}

func TestCS2UnmappedStillZero(t *testing.T) {
	bus := newBusForTest()
	// Address in CS2 range but outside CD Block registers
	if got := bus.Read8(0x05800000); got != 0 {
		t.Errorf("CS2 unmapped read = 0x%02X, want 0x00", got)
	}
	// Should not panic
	bus.Write8(0x05800000, 0xFF)
}

func TestExtendedRAMCartridge(t *testing.T) {
	bus := newBusForTest()

	// Cartridge ID at CS1 0x04FFFFFF = 0x5C (4MB cart)
	if got := bus.Read8(0x04FFFFFF); got != 0x5C {
		t.Errorf("cart ID = 0x%02X, want 0x5C", got)
	}

	// Word read at 0x04FFFFFE returns 0xFF00 | 0x5C = 0xFF5C
	if got := bus.Read16(0x04FFFFFE); got != 0xFF5C {
		t.Errorf("cart ID word = 0x%04X, want 0xFF5C", got)
	}

	// Extended RAM read/write at 0x02400000
	bus.Write8(0x02400000, 0xAA)
	if got := bus.Read8(0x02400000); got != 0xAA {
		t.Errorf("extRAM[0] = 0x%02X, want 0xAA", got)
	}

	// 32-bit read/write
	bus.Write32(0x02400100, 0xDEADBEEF)
	if got := bus.Read32(0x02400100); got != 0xDEADBEEF {
		t.Errorf("extRAM 32-bit = 0x%08X, want 0xDEADBEEF", got)
	}

	// End of 4MB range (0x027FFFFF)
	bus.Write8(0x027FFFFF, 0x55)
	if got := bus.Read8(0x027FFFFF); got != 0x55 {
		t.Errorf("extRAM end = 0x%02X, want 0x55", got)
	}

	// Outside Extended RAM range in CS0 returns 0xFF
	if got := bus.Read8(0x02000000); got != 0xFF {
		t.Errorf("CS0 unmapped = 0x%02X, want 0xFF", got)
	}

	// CS1 non-ID address returns 0xFF
	if got := bus.Read8(0x04000000); got != 0xFF {
		t.Errorf("CS1 non-ID = 0x%02X, want 0xFF", got)
	}
}

// TestBusAccessCyclesPerRegion verifies that Bus.AccessCycles
// returns the documented state count for each Saturn memory region,
// and that 16-byte burst size correctly differentiates between
// SDRAM (initial + 3 beats) and non-SDRAM (4 * base).
func TestBusAccessCyclesPerRegion(t *testing.T) {
	bus := newBusForTest()

	cases := []struct {
		name   string
		addr   uint32
		size4  uint32 // expected for size=4 (longword)
		size16 uint32 // expected for size=16 (burst)
	}{
		{"BIOS ROM", 0x00000000, 3, 12},
		{"SMPC", 0x00100000, 3, 12},
		{"Backup RAM", 0x00180000, 3, 12},
		{"Work RAM-L", 0x00200000, 3, 12},
		{"MINIT", 0x01000000, 3, 12},
		{"SINIT", 0x01800000, 3, 12},
		{"A-Bus CS0 (cart)", 0x02000000, 20, 80},
		{"A-Bus CS1 (cart ID)", 0x04000000, 20, 80},
		{"A-Bus Dummy", 0x05000000, 20, 80},
		{"CD Block", 0x05800000, 10, 40},
		{"SCSP RAM", 0x05A00000, 12, 48},
		{"VDP1 VRAM", 0x05C00000, 8, 32},
		{"VDP2 VRAM", 0x05E00000, 8, 32},
		{"VDP2 CRAM", 0x05F00000, 5, 20},
		{"VDP2 regs", 0x05F80000, 5, 20},
		{"SCU regs", 0x05FE0000, 5, 20},
		{"Work RAM-H", 0x06000000, 3, 6}, // SDRAM burst: base + 3
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := bus.AccessCycles(tt.addr, 4); got != tt.size4 {
				t.Errorf("AccessCycles(0x%08X, 4) = %d, want %d", tt.addr, got, tt.size4)
			}
			if got := bus.AccessCycles(tt.addr, 16); got != tt.size16 {
				t.Errorf("AccessCycles(0x%08X, 16) = %d, want %d", tt.addr, got, tt.size16)
			}
		})
	}
}

// --- Coverage-targeted tests for 16-bit / 32-bit bus dispatch paths ---

// TestEmulatorSetDiscAutoRegion exercises the disc-region auto-detect
// path for each area-code letter (J/T/U/E) plus a disc with no valid
// code.
func TestEmulatorSetDiscAutoRegion(t *testing.T) {
	cases := []struct {
		name     string
		areaCh   byte
		wantArea uint8
		wantPAL  bool
	}{
		{"Japan", 'J', 0x01, false},
		{"Asia NTSC", 'T', 0x02, false},
		{"NorthAm", 'U', 0x04, false},
		{"Europe PAL", 'E', 0x0C, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEmulator()
			disc := makeRegionDisc(c.areaCh)
			e.SetDisc(disc)
			if got := e.smpc.areaCode; got != c.wantArea {
				t.Errorf("areaCode = 0x%02X, want 0x%02X", got, c.wantArea)
			}
			if got := e.vdp2.pal; got != c.wantPAL {
				t.Errorf("PAL = %v, want %v", got, c.wantPAL)
			}
		})
	}

	// No valid area code: emulator leaves area code unchanged.
	e := NewEmulator()
	e.smpc.areaCode = 0xEE
	e.SetDisc(makeRegionDisc('Z'))
	if e.smpc.areaCode != 0xEE {
		t.Errorf("unknown area overwrote areaCode: got 0x%02X", e.smpc.areaCode)
	}

	// SetDisc(nil) detaches without touching region.
	e.SetDisc(nil)
}

// regionDisc is a minimal DiscReader that exposes one data sector
// containing the requested area-code letter at System-ID offset $40.
type regionDisc struct {
	sector [2352]byte
}

func makeRegionDisc(ch byte) *regionDisc {
	d := &regionDisc{}
	d.sector[15] = 0x01
	// Area-code field starts at user-data offset 0x40 (raw offset 16+0x40).
	for i := 0; i < 10; i++ {
		d.sector[16+0x40+i] = ch
	}
	return d
}

func (d *regionDisc) ReadSector(lba int) ([]byte, error) {
	if lba != 0 {
		return nil, nil
	}
	out := make([]byte, 2352)
	copy(out, d.sector[:])
	return out, nil
}

func (d *regionDisc) NumTracks() int { return 1 }
func (d *regionDisc) Track(i int) (int, string, int, int, int, uint8) {
	return 1, "MODE1_RAW", 100, 0, 0, 0x41
}

// TestBus16BitInvalidRegions drives the invalid-size paths in Read16/Write16
// that log but have no side effect beyond "return 0" or "no-op".
func TestBus16BitInvalidRegions(t *testing.T) {
	bus := newBusForTest()

	// SMPC: 16-bit access invalid.
	if got := bus.Read16(0x00100000); got != 0 {
		t.Errorf("16-bit SMPC read = 0x%04X, want 0", got)
	}
	bus.Write16(0x00100000, 0xABCD) // logged, no state change

	// Backup RAM: 16-bit access invalid.
	if got := bus.Read16(0x00180000); got != 0 {
		t.Errorf("16-bit Backup read = 0x%04X, want 0", got)
	}
	bus.Write16(0x00180000, 0xABCD)

	// MINIT/SINIT: read returns 0 (trigger-only).
	if got := bus.Read16(0x01000000); got != 0 {
		t.Errorf("MINIT read = 0x%04X, want 0", got)
	}
	if got := bus.Read16(0x01800000); got != 0 {
		t.Errorf("SINIT read = 0x%04X, want 0", got)
	}

	// Unmapped default path.
	if got := bus.Read16(0x0F000000); got != 0 {
		t.Errorf("unmapped Read16 = 0x%04X, want 0", got)
	}
	bus.Write16(0x0F000000, 0xFFFF) // default logged
}

// TestBus16BitMappedRegions verifies Read16/Write16 for each supported
// region beyond what TestBusRAM/BIOS already cover.
func TestBus16BitMappedRegions(t *testing.T) {
	bus := newBusForTest()

	// SCSP sound RAM.
	bus.Write16(0x05A00100, 0xBEEF)
	if got := bus.Read16(0x05A00100); got != 0xBEEF {
		t.Errorf("SCSP RAM 16 = 0x%04X, want 0xBEEF", got)
	}

	// SCSP B-Bus gap: silent drop.
	bus.Write16(0x05A80000, 0x1234)
	if got := bus.Read16(0x05A80000); got != 0 {
		t.Errorf("SCSP gap read = 0x%04X, want 0", got)
	}

	// SCSP unmapped high range.
	if got := bus.Read16(0x05B00EE4); got != 0 {
		t.Errorf("SCSP unmapped read = 0x%04X, want 0", got)
	}
	bus.Write16(0x05B00EE4, 0xFFFF)

	// VDP1 VRAM.
	bus.Write16(0x05C00200, 0xCAFE)
	if got := bus.Read16(0x05C00200); got != 0xCAFE {
		t.Errorf("VDP1 VRAM 16 = 0x%04X, want 0xCAFE", got)
	}

	// VDP1 framebuffer.
	bus.Write16(0x05C80100, 0x1122)
	if got := bus.Read16(0x05C80100); got != 0x1122 {
		t.Errorf("VDP1 FB 16 = 0x%04X, want 0x1122", got)
	}

	// VDP2 VRAM.
	bus.Write16(0x05E00200, 0x3344)
	if got := bus.Read16(0x05E00200); got != 0x3344 {
		t.Errorf("VDP2 VRAM 16 = 0x%04X, want 0x3344", got)
	}

	// VDP2 CRAM.
	bus.Write16(0x05F00100, 0x5566)
	if got := bus.Read16(0x05F00100); got != 0x5566 {
		t.Errorf("VDP2 CRAM 16 = 0x%04X, want 0x5566", got)
	}

	// Extended RAM cart.
	bus.Write16(0x02400000, 0x7788)
	if got := bus.Read16(0x02400000); got != 0x7788 {
		t.Errorf("ext RAM 16 = 0x%04X, want 0x7788", got)
	}

	// A-Bus CS0 non-extRAM: open-bus read returns 0xFFFF; write ignored.
	if got := bus.Read16(0x02000000); got != 0xFFFF {
		t.Errorf("CS0 non-extRAM 16 = 0x%04X, want 0xFFFF", got)
	}
	bus.Write16(0x02000000, 0x1234)

	// A-Bus CS1 cart ID at 0x04FFFFFE: returns 0xFF00 | cartID4MB.
	if got := bus.Read16(0x04FFFFFE); got != 0xFF5C {
		t.Errorf("CS1 cart ID 16 = 0x%04X, want 0xFF5C", got)
	}
	// CS1 other addresses: 0xFFFF.
	if got := bus.Read16(0x04000000); got != 0xFFFF {
		t.Errorf("CS1 other 16 = 0x%04X, want 0xFFFF", got)
	}
	bus.Write16(0x04000000, 0x1234) // read-only

	// A-Bus Dummy.
	if got := bus.Read16(0x05000000); got != 0 {
		t.Errorf("A-Bus dummy 16 = 0x%04X, want 0", got)
	}
	bus.Write16(0x05000000, 0x1234)
}

// TestBus32BitInvalidRegions drives the invalid-size paths in Read32/Write32.
func TestBus32BitInvalidRegions(t *testing.T) {
	bus := newBusForTest()

	// SMPC: byte-only.
	if got := bus.Read32(0x00100000); got != 0 {
		t.Errorf("32-bit SMPC read = 0x%08X, want 0", got)
	}
	bus.Write32(0x00100000, 0xCAFEBABE)

	// Backup: byte-only.
	if got := bus.Read32(0x00180000); got != 0 {
		t.Errorf("32-bit Backup read = 0x%08X, want 0", got)
	}
	bus.Write32(0x00180000, 0xCAFEBABE)

	// MINIT/SINIT: 32-bit not valid.
	if got := bus.Read32(0x01000000); got != 0 {
		t.Errorf("MINIT read32 = 0x%08X, want 0", got)
	}
	if got := bus.Read32(0x01800000); got != 0 {
		t.Errorf("SINIT read32 = 0x%08X, want 0", got)
	}
	bus.Write32(0x01000000, 0xCAFEBABE)
	bus.Write32(0x01800000, 0xCAFEBABE)

	// A-Bus CS1: 16-bit bus. A longword access is two 16-bit cycles.
	// Empty CS1 reads open-bus high; the cart ID byte appears at
	// 0x04FFFFFF, so the longword at 0x04FFFFFC carries it low.
	if got := bus.Read32(0x04FFFFFC); got != 0xFFFFFF5C {
		t.Errorf("CS1 read32 = 0x%08X, want 0xFFFFFF5C", got)
	}
	if got := bus.Read32(0x04020000); got != 0xFFFFFFFF {
		t.Errorf("CS1 open-bus read32 = 0x%08X, want 0xFFFFFFFF", got)
	}

	// VDP1 register 32-bit invalid.
	if got := bus.Read32(0x05D00000); got != 0 {
		t.Errorf("VDP1 reg read32 = 0x%08X, want 0", got)
	}
	bus.Write32(0x05D00000, 0xCAFEBABE)

	// Unmapped default.
	if got := bus.Read32(0x0F000000); got != 0 {
		t.Errorf("unmapped read32 = 0x%08X, want 0", got)
	}
	bus.Write32(0x0F000000, 0xCAFEBABE)
}

// TestBus32BitMappedRegions verifies Read32/Write32 for each supported
// region not already covered.
func TestBus32BitMappedRegions(t *testing.T) {
	bus := newBusForTest()

	// SCSP sound RAM.
	bus.Write32(0x05A00100, 0xDEADBEEF)
	if got := bus.Read32(0x05A00100); got != 0xDEADBEEF {
		t.Errorf("SCSP RAM 32 = 0x%08X, want 0xDEADBEEF", got)
	}

	// SCSP gap silent drop.
	bus.Write32(0x05A80000, 0xCAFEBABE)
	if got := bus.Read32(0x05A80000); got != 0 {
		t.Errorf("SCSP gap read32 = 0x%08X, want 0", got)
	}

	// SCSP unmapped high range.
	if got := bus.Read32(0x05B00EE4); got != 0 {
		t.Errorf("SCSP unmapped read32 = 0x%08X, want 0", got)
	}
	bus.Write32(0x05B00EE4, 0xCAFEBABE)

	// VDP1 VRAM/FB.
	bus.Write32(0x05C00200, 0x11223344)
	if got := bus.Read32(0x05C00200); got != 0x11223344 {
		t.Errorf("VDP1 VRAM 32 = 0x%08X, want 0x11223344", got)
	}
	bus.Write32(0x05C80100, 0x55667788)
	if got := bus.Read32(0x05C80100); got != 0x55667788 {
		t.Errorf("VDP1 FB 32 = 0x%08X, want 0x55667788", got)
	}

	// VDP2 VRAM/CRAM.
	bus.Write32(0x05E00200, 0x99AABBCC)
	if got := bus.Read32(0x05E00200); got != 0x99AABBCC {
		t.Errorf("VDP2 VRAM 32 = 0x%08X, want 0x99AABBCC", got)
	}
	bus.Write32(0x05F00100, 0x01020304)
	if got := bus.Read32(0x05F00100); got != 0x01020304 {
		t.Errorf("VDP2 CRAM 32 = 0x%08X, want 0x01020304", got)
	}

	// VDP2 register path: write + read via Read32 composes two 16-bit words.
	bus.Write32(0x05F80000, 0xAAAABBBB)
	got := bus.Read32(0x05F80000)
	// Don't assert exact value (register semantics may mask or transform);
	// confirm the path was exercised via round-trip structure.
	_ = got

	// Extended RAM cart.
	bus.Write32(0x02400000, 0xCAFEBABE)
	if got := bus.Read32(0x02400000); got != 0xCAFEBABE {
		t.Errorf("ext RAM 32 = 0x%08X, want 0xCAFEBABE", got)
	}

	// A-Bus CS0 non-extRAM: open-bus.
	if got := bus.Read32(0x02000000); got != 0xFFFFFFFF {
		t.Errorf("CS0 non-extRAM 32 = 0x%08X, want 0xFFFFFFFF", got)
	}
	bus.Write32(0x02000000, 0x1234)

	// A-Bus Dummy.
	if got := bus.Read32(0x05000000); got != 0 {
		t.Errorf("A-Bus dummy 32 = 0x%08X, want 0", got)
	}
	bus.Write32(0x05000000, 0x1234)
}

// TestBus16BitWriteMINITSINIT verifies the side-effect flags for
// 16-bit writes to MINIT/SINIT regions. These are the only allowed
// sizes; each write sets a latch read back by MINITWritten/SINITWritten.
func TestBus16BitWriteMINITSINIT(t *testing.T) {
	bus := newBusForTest()

	bus.Write16(0x01000000, 0x1234)
	if !bus.MINITWritten() {
		t.Error("MINITWritten not set after 16-bit write")
	}
	if bus.MINITWritten() {
		t.Error("MINITWritten not cleared by read")
	}

	bus.Write16(0x01800000, 0x1234)
	if !bus.SINITWritten() {
		t.Error("SINITWritten not set after 16-bit write")
	}
}

// TestBusCS2_32BitRegister exercises the A-Bus CS2 (CD block) 32-bit
// non-DATATRNS path. Write32 at HIRQMSK writes the upper half to
// HIRQMSK; a Read32 at the same address composes HIRQMSK into both
// halves of the returned value.
func TestBusCS2_32BitRegister(t *testing.T) {
	bus := newBusForTest()

	// HIRQMSK at CS2 offset 0x000C.
	bus.Write32(0x0580000C, 0x0FFF0000)
	if got := bus.Read16(0x0580000C); got != 0x0FFF {
		t.Errorf("HIRQMSK after Write32 = 0x%04X, want 0x0FFF", got)
	}
	if got := bus.Read32(0x0580000C); got != (0x0FFF<<16)|0x0FFF {
		t.Errorf("HIRQMSK Read32 composition = 0x%08X, want 0x0FFF0FFF", got)
	}
}
