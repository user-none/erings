// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/sh2"
)

// HLE replacement for the PER (peripheral) side of the 16 KB
// hybrid PER+BUP driver that real BIOS decompresses out of
// $0007D660. PER_Init lays down the slot table at the caller's
// buffer; game code dispatches through that table to slot N via
// JSR @(N*4, driver_base). Slot 0 (driver init / relocator)
// lives here; slots 1-10 are BUP-flavored and live in
// hlebios_bup.go.
//
// See docs/bios/peripheral_driver.md for the per-slot return-code
// contracts and docs/bios/backup_library.md for the BUP side.

// Magic addresses for the 11 driver slots. All inside the
// $A0000000-$A0000FFF range the SH-2 execute loop traps on (see
// sh2/execute.go). Slot 11 at driver_base+$2C is a data pointer
// (= driver_base+$78), not a function, so it has no magic address.
const (
	hlePerDriverSlot0  = 0xA00000C0 // driver init / first peripheral fetch
	hlePerDriverSlot1  = 0xA00000C4 // BUP_SelPart
	hlePerDriverSlot2  = 0xA00000C8 // BUP_Format
	hlePerDriverSlot3  = 0xA00000CC // BUP_Stat
	hlePerDriverSlot4  = 0xA00000D0 // BUP_Write
	hlePerDriverSlot5  = 0xA00000D4 // BUP_Read
	hlePerDriverSlot6  = 0xA00000D8 // BUP_Delete
	hlePerDriverSlot7  = 0xA00000DC // BUP_Dir
	hlePerDriverSlot8  = 0xA00000E0 // BUP_Verify
	hlePerDriverSlot9  = 0xA00000E4 // BUP_GetDate
	hlePerDriverSlot10 = 0xA00000E8 // BUP_SetDate
)

// WRAM-H slot the driver registers itself at. Game code reads
// mem.L[$06000354] to find the driver's base address and then JSRs
// through driver_base+N*4 for slot N.
const wramHPERDriverSlot = 0x06000354

// Driver-state byte offsets (relative to driver_base). The
// disassembly's slot bodies key off these to decide whether a
// peripheral is present and which dispatch path to take.
const (
	perDriverPort1Marker    = 0x54 // 1 = controller-port-1 has a pad
	perDriverPort1AltMarker = 0x55 // field meaning: 1 = slot-3 alt path active. HLE always writes 0 (alt path not implemented).
	perDriverPort2Marker    = 0x56 // 0 = no BUP cart on port 2
	perDriverPerEntryBuffer = 0x78 // start of per-entry working buffer
)

// perDriverBase reads the driver_base address published in WRAM-H
// at $06000354. Returns 0 if the slot hasn't been written yet (i.e.
// PER_Init hasn't run); callers must treat zero as "driver not
// initialized" and skip work.
func perDriverBase(bus *Bus) uint32 {
	off := wramHPERDriverSlot - 0x06000000
	return bus.readWramHU32(uint32(off))
}

// writePerDriverTable lays down the 11 magic-address slots at
// driverBase+0..driverBase+$28 plus the per-entry working buffer
// pointer at driverBase+$2C, initializes the driver-state marker
// bytes at driverBase+$54..+$67, AND registers the driver in
// WRAM-H by writing mem.L[$06000354] = driverBase.
//
// Real BIOS combines decompression (sub_1F04) with relocator
// invocation (slot 0) inside PER_Init: PER_Init's caller-was-R4
// is both the buffer that receives the decompressed driver AND
// the address PER_Init JSRs to as "initFn" — which executes slot 0,
// which writes $06000354 = driver_base. So by the time PER_Init
// returns to the caller in real BIOS, $06000354 has been
// populated. The SDK BUP library reads $06000354 to locate the
// driver and dispatch to its slots; if the slot is still zero
// the library reports "Cartridge Memory not ready" and bails.
//
// Note on the SP-chain trick the disasm doc calls out (game code
// reading mem.L[$06000354]+12 to recover the manual-reset SP): the
// IP performs that read in its boot sequence, before the game
// calls PER_Init. By the time PER_Init runs, the SP-chain use is
// already past, so registering the driver here doesn't break it.
//
// Idempotent: calling this twice with the same driverBase is a
// no-op-equivalent (overwrites with the same bytes).
func writePerDriverTable(bus *Bus, driverBase uint32) {
	magic := [11]uint32{
		hlePerDriverSlot0,
		hlePerDriverSlot1,
		hlePerDriverSlot2,
		hlePerDriverSlot3,
		hlePerDriverSlot4,
		hlePerDriverSlot5,
		hlePerDriverSlot6,
		hlePerDriverSlot7,
		hlePerDriverSlot8,
		hlePerDriverSlot9,
		hlePerDriverSlot10,
	}
	for i, addr := range magic {
		bus.Write32(driverBase+uint32(i)*4, addr)
	}
	// driverBase+$2C holds a pointer to driverBase+$78 — the
	// per-entry working buffer that the BUP slot bodies use as
	// scratch / output area (per-directory-entry status, block
	// lists, packet staging).
	bus.Write32(driverBase+0x2C, driverBase+perDriverPerEntryBuffer)

	// Driver-state markers. Port 1 = controller pad present
	// (positive markers so slot-1/3/etc. fall through to the
	// peripheral-read path). Port 2 = no BUP cart (marker 0 so
	// BUP-flavored slots take the "marker == 0" early-out and
	// return NOT_FOUND).
	bus.Write8(driverBase+perDriverPort1Marker, 1)
	bus.Write8(driverBase+perDriverPort1AltMarker, 0)
	bus.Write8(driverBase+perDriverPort2Marker, 0)
	// Zero the remaining driver-state bytes at +$57..+$67. Real
	// BIOS's slot-0 leaves some of these bytes populated with
	// peripheral-type-specific data; HLE doesn't model the
	// per-type dispatch and zero is the documented marker-absent
	// value.
	for i := uint32(0x57); i < 0x68; i++ {
		bus.Write8(driverBase+i, 0)
	}

	bus.writeWramHU32(wramHPERDriverSlot-0x06000000, driverBase)
}

// writePortRecord writes one 4-byte SDK digital-pad record at the
// given destination address. Layout matches what real BIOS produces
// in SMPC OREG during an INTBACK:
//
//	+0  $F1 (multi-tap=F, connectors=1 — direct connection)
//	+1  $02 (peripheral ID: digital pad, 2 data bytes follow)
//	+2  pad >> 8 (button byte 1, active-low)
//	+3  pad & $FF (button byte 2, active-low)
func writePortRecord(bus *Bus, dst uint32, pad uint16) {
	bus.Write8(dst+0, 0xF1)
	bus.Write8(dst+1, 0x02)
	bus.Write8(dst+2, uint8(pad>>8))
	bus.Write8(dst+3, uint8(pad))
}

// writeBothPortRecords emits port-1 and port-2 records to a
// caller-supplied buffer at the disassembled real-BIOS slot-0
// 8-byte per-port stride (the ADD #8,R4 between port-1 and
// port-2 setup at slot-0 +$026E confirms 8 bytes between record
// starts). Each record itself is 4 bytes (writePortRecord); the
// remaining 4 bytes of each 8-byte slot are left as zeros. Per
// docs/bios/peripheral_driver.md the side-effect summary of
// slot 0 is mem.W[caller_R5 + 0..7] = port 1, mem.W[+8..15] =
// port 2 — this matches that layout.
func writeBothPortRecords(bus *Bus, buf uint32) {
	if buf == 0 {
		return
	}
	for port := uint32(0); port < 2; port++ {
		pad := bus.smpc.PadData(int(port))
		writePortRecord(bus, buf+port*8, pad)
	}
}

// hlePerDriverSlot0Service runs the documented slot-0 effects when
// the game explicitly dispatches through driver_base+$00 (typically
// via "MOV.L @($06000354),Rn ; JSR @(0,Rn)"). Effects:
//
//   - mem.L[$06000354] = driver_base — the driver registration that
//     lets later code find the driver. PER_Init already performs this
//     write; an explicit slot-0 dispatch re-does it idempotently
//     (e.g. after the game relocates the driver buffer).
//   - Idempotent re-build of the magic-address table and state
//     markers at driver_base.
//
// Does NOT write port records to R5. PER_Init's caller wires the
// initial peripheral output buffer; slot 0 re-entry doesn't know
// where the game's current R5 points and writing 4 bytes per port
// to an arbitrary R5 would corrupt game state.
func hlePerDriverSlot0Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	driver := r.R[4]
	if driver < 0x06000000 || driver >= 0x06100000 {
		return
	}
	writePerDriverTable(bus, driver)
	bus.writeWramHU32(wramHPERDriverSlot-0x06000000, driver)
}
