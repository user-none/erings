// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"
)

// perDriverTestBase is the WRAM-H address used as the driver buffer
// in unit tests. Picked clear of the IP load window ($06002000) and
// the BIOS data tables ($06000000-$06001000).
const perDriverTestBase uint32 = 0x0607C000

func TestPERInitBuildsSlotTable(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	// Per SDK PER_Init(libaddr, workbuff, conf[3]):
	//   R4 = libaddr (driver-code buffer, not used by HLE)
	//   R5 = workbuff (slot table goes here = driver_base)
	//   R6 = conf[3] (peripheral records output buffer)
	master.SetReg(4, 0x06080000) // libaddr (driver-code buffer; HLE ignores)
	master.SetReg(5, perDriverTestBase)
	master.SetReg(6, 0x06080100) // peripheral output buffer

	hleBiosPERInitService(master, bus)

	cases := []struct {
		slot int
		want uint32
	}{
		{0, hlePerDriverSlot0},
		{1, hlePerDriverSlot1},
		{2, hlePerDriverSlot2},
		{3, hlePerDriverSlot3},
		{4, hlePerDriverSlot4},
		{5, hlePerDriverSlot5},
		{6, hlePerDriverSlot6},
		{7, hlePerDriverSlot7},
		{8, hlePerDriverSlot8},
		{9, hlePerDriverSlot9},
		{10, hlePerDriverSlot10},
	}
	for _, c := range cases {
		off := perDriverTestBase + uint32(c.slot)*4
		if got := bus.Read32(off); got != c.want {
			t.Errorf("slot %d @+%X = %08X, want %08X",
				c.slot, c.slot*4, got, c.want)
		}
	}
	// Slot 11 is a data pointer, not a magic-address entry.
	if got := bus.Read32(perDriverTestBase + 0x2C); got != perDriverTestBase+0x78 {
		t.Errorf("slot 11 data ptr = %08X, want %08X",
			got, perDriverTestBase+0x78)
	}
}

func TestPERInitRegistersDriver(t *testing.T) {
	// PER_Init transitively registers driver_base at $06000354. Real
	// BIOS reaches the registration via its JSR through the decompressed
	// relocator (slot 0); HLE collapses the relocator into
	// writePerDriverTable, so the side effect is the same.
	h, bus, master, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	master.SetReg(4, 0)
	master.SetReg(5, perDriverTestBase) // workbuff = driver_base
	master.SetReg(6, 0)

	hleBiosPERInitService(master, bus)

	if got := bus.readWramHU32(wramHPERDriverSlot - 0x06000000); got != perDriverTestBase {
		t.Errorf("after PER_Init, mem.L[$06000354] = %08X, want %08X", got, perDriverTestBase)
	}
}

func TestPERInitRegistersWRAMLDriver(t *testing.T) {
	// Regression: some titles (Cotton Boomerang) hand PER_Init a
	// work-buffer in Work RAM-L ($00200000-$002FFFFF) rather than
	// Work RAM-H. PER_Init must register such a buffer at $06000354
	// too; an earlier WRAM-H-only guard skipped it, leaving the slot
	// zero and faulting the game's first peripheral dispatch.
	const wramLBase uint32 = 0x00206790 // observed Cotton Boomerang workbuff
	h, bus, master, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	master.SetReg(4, 0)
	master.SetReg(5, wramLBase)
	master.SetReg(6, 0)

	hleBiosPERInitService(master, bus)

	if got := bus.readWramHU32(wramHPERDriverSlot - 0x06000000); got != wramLBase {
		t.Errorf("after PER_Init, mem.L[$06000354] = %08X, want %08X", got, wramLBase)
	}
	// The slot table must be laid down in WRAM-L at the buffer too.
	if got := bus.Read32(wramLBase); got != hlePerDriverSlot0 {
		t.Errorf("slot 0 @ %08X = %08X, want %08X", wramLBase, got, hlePerDriverSlot0)
	}
}

func TestPERSlot0RegistersDriver(t *testing.T) {
	// Slot 0 (when explicitly invoked by game code) re-does the
	// driver registration write idempotently.
	_, bus, master, _ := newHLEBIOSForTest()
	master.SetReg(4, perDriverTestBase)
	hlePerDriverSlot0Service(master, bus)

	if got := bus.readWramHU32(wramHPERDriverSlot - 0x06000000); got != perDriverTestBase {
		t.Errorf("after slot 0, mem.L[$06000354] = %08X, want %08X",
			got, perDriverTestBase)
	}
}

func TestPERInitInitializesDriverState(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	master.SetReg(4, 0)
	master.SetReg(5, perDriverTestBase) // workbuff = driver_base
	master.SetReg(6, 0)

	hleBiosPERInitService(master, bus)

	if got := bus.Read8(perDriverTestBase + perDriverPort1Marker); got != 1 {
		t.Errorf("port-1 marker = %d, want 1", got)
	}
	// Port-1 alt marker is 0 in current code (writePerDriverTable
	// at hlebios_per.go: sets port1Marker=1, port1AltMarker=0,
	// port2Marker=0). The alt marker is reserved for slot-3 alt
	// paths and stays zero in the standard internal-RAM config.
	if got := bus.Read8(perDriverTestBase + perDriverPort1AltMarker); got != 0 {
		t.Errorf("port-1 alt marker = %d, want 0", got)
	}
	if got := bus.Read8(perDriverTestBase + perDriverPort2Marker); got != 0 {
		t.Errorf("port-2 marker = %d, want 0 (no BUP cart)", got)
	}
}

func TestPERInitPopulatesPortRecords(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	bus.smpc.SetPadData(0, 0xBFFD) // some pressed buttons on port 0
	bus.smpc.SetPadData(1, 0xFEEF) // and on port 1

	const periphBuf uint32 = 0x06080100
	master.SetReg(4, 0)
	master.SetReg(5, perDriverTestBase) // workbuff
	master.SetReg(6, periphBuf)

	hleBiosPERInitService(master, bus)

	// Port 0 record at periphBuf+0..+3.
	if got := bus.Read8(periphBuf + 0); got != 0xF1 {
		t.Errorf("port0 record[0] = %02X, want F1", got)
	}
	if got := bus.Read8(periphBuf + 1); got != 0x02 {
		t.Errorf("port0 record[1] = %02X, want 02", got)
	}
	if got := bus.Read8(periphBuf + 2); got != 0xBF {
		t.Errorf("port0 record[2] = %02X, want BF", got)
	}
	if got := bus.Read8(periphBuf + 3); got != 0xFD {
		t.Errorf("port0 record[3] = %02X, want FD", got)
	}
	// Port 1 record at periphBuf+8..+11 (8-byte stride, matches the
	// disassembled real-BIOS slot-0 layout with ADD #8,R4 between
	// port-1 and port-2 setup).
	if got := bus.Read8(periphBuf + 8); got != 0xF1 {
		t.Errorf("port1 record[0] = %02X, want F1", got)
	}
	if got := bus.Read8(periphBuf + 9); got != 0x02 {
		t.Errorf("port1 record[1] = %02X, want 02", got)
	}
	if got := bus.Read8(periphBuf + 10); got != 0xFE {
		t.Errorf("port1 record[2] = %02X, want FE", got)
	}
	if got := bus.Read8(periphBuf + 11); got != 0xEF {
		t.Errorf("port1 record[3] = %02X, want EF", got)
	}
}

func TestPERInitExitRegisterState(t *testing.T) {
	// Per side-by-side trace with real BIOS, slot 0 (invoked
	// internally by PER_Init's trampoline) exits with R4 = entry
	// R5 + 8 (peripheral buffer advanced past port-1 record to the
	// port-2 record slot) and R5 = 0. PER_Init's trampoline passes
	// caller's R6 into slot 0 as R5, so slot 0's "entry R5" is our
	// caller's R6.
	_, bus, master, _ := newHLEBIOSForTest()
	master.SetReg(4, 0)
	master.SetReg(5, perDriverTestBase)
	master.SetReg(6, 0xCAFEBABE)

	hleBiosPERInitService(master, bus)

	r := master.Registers()
	if r.R[4] != 0xCAFEBABE+8 {
		t.Errorf("after PER_Init, R4 = %08X, want CAFEBAC6 (entry R6 + 8)", r.R[4])
	}
	if r.R[5] != 0 {
		t.Errorf("after PER_Init, R5 = %08X, want 0", r.R[5])
	}
}

func TestPERSlot0RewritesTable(t *testing.T) {
	// Slot 0 re-entry must re-publish the table without writing
	// port records to caller's R5 (which may not be a buffer on
	// re-entry).
	_, bus, master, _ := newHLEBIOSForTest()
	master.SetReg(4, perDriverTestBase)
	hlePerDriverSlot0Service(master, bus)

	if got := bus.Read32(perDriverTestBase); got != hlePerDriverSlot0 {
		t.Errorf("slot 0 re-entry: slot[0] = %08X, want %08X",
			got, hlePerDriverSlot0)
	}
}

func TestPERDispatchTableSlotsAtBoot(t *testing.T) {
	// PER_Init at $06000358 holds the HLE magic-address sentinel.
	// Slot $06000354 starts as zero (no driver registered yet — game
	// has to call PER_Init first).
	h, bus, _, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if got := bus.readWramHU32(wramHSysTable + 0x58); got != hleBiosPERInit {
		t.Errorf("PER_Init slot = %08X, want %08X", got, hleBiosPERInit)
	}
	if got := bus.readWramHU32(wramHSysTable + 0x54); got != 0 {
		t.Errorf("$06000354 at boot = %08X, want 0 (driver not registered yet)", got)
	}
}
