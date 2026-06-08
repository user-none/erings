// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// Fast boot skips the real BIOS boot animation (the HLE BIOS has none). It lets
// the real BIOS run the init that builds the WRAM-H dispatch tables and vectors
// the disc relies on, then seizes control just before the BIOS would start the
// disc check / animation and enters the disc IP directly. See
// docs/bios/boot_sequence.md for the boot flow these hooks sit in.
//
// Two hooks, both via the SH-2 magic-address trap:
//   - installFastBoot patches the dispatch pointer at the "init done" marker so
//     the BIOS diverts to fastBootEnterIP instead of the disc check / animation.
//   - fastBootEnterIP patches the IP's game-load pointer so fastBootLoadGame
//     stands in for the BIOS CD read pump fast boot skipped.

// Magic addresses in the SH-2 HLE trap range ($A0000xxx); patched over BIOS
// pointers so the BIOS/IP divert into the trap.
//
// biosInitDonePoolOff / biosInitDonePoolVal: the "init done" marker. The BIOS
// reaches this pool pointer (a JSR to sub_25DC at $060013B0 in the copied
// system code) right after the init fast boot keeps and right before the disc
// check / animation, so it is where fast boot cuts to the IP. The routine it
// points at is incidental; the value is a guard against an unrecognized BIOS.
//
// sub_25DC cassl CD status poll which is far enough in the boot process to
// know everything is setup and it's going to start running things we can skip.
//
// wramHGameLoadSlot: the IP's game-load function-pointer slot ($06000284).
const (
	fastBootRedirect    = 0xA00000FC
	fastBootLoadGame    = 0xA00000FD
	biosInitDonePoolOff = 0x13B0
	biosInitDonePoolVal = 0x000025DC
	wramHGameLoadSlot   = 0x284
)

// installFastBoot patches the init-done marker to divert the BIOS into
// fastBootEnterIP. It engages only for a Saturn game disc on a BIOS whose
// dispatch layout matches the guard; otherwise the BIOS boots normally.
// Both the USA and Japan official BIOS files work.
func (e *Emulator) installFastBoot() {
	ip := e.ipImage
	if len(ip) < 16 || string(ip[0:16]) != "SEGA SEGASATURN " {
		return // not a game disc; nothing to skip
	}
	b := e.bus.bios
	if len(b) <= biosInitDonePoolOff+3 {
		return
	}
	got := uint32(b[biosInitDonePoolOff])<<24 | uint32(b[biosInitDonePoolOff+1])<<16 |
		uint32(b[biosInitDonePoolOff+2])<<8 | uint32(b[biosInitDonePoolOff+3])
	if got != biosInitDonePoolVal {
		return // unrecognized BIOS dispatch layout; leave the normal boot intact
	}
	b[biosInitDonePoolOff] = byte(fastBootRedirect >> 24)
	b[biosInitDonePoolOff+1] = byte((fastBootRedirect >> 16) & 0xFF)
	b[biosInitDonePoolOff+2] = byte((fastBootRedirect >> 8) & 0xFF)
	b[biosInitDonePoolOff+3] = byte(fastBootRedirect & 0xFF)

	e.master.HLEHook = func(pc uint32) {
		switch pc {
		case fastBootRedirect:
			e.fastBootEnterIP()
		case fastBootLoadGame:
			e.fastBootLoadGame()
		}
	}
}

// fastBootEnterIP supplies the disc-side state the skipped BIOS code would have
// produced (IP image, System ID cache, 1st-read address), patches the game-load
// hook, and enters the IP at $06002100 with the handoff registers from
// docs/bios/handoff_state.md.
func (e *Emulator) fastBootEnterIP() {
	ip := e.ipImage

	// IP image into the $06002000 load window (the disc check would have
	// read it from the disc; it is already cached from SetDisc).
	copy(e.bus.wramH[ipLoadAddr-0x06000000:], ip)

	// Per-disc System ID cache: first 256 B of the IP at $06000C00.
	copy(e.bus.wramH[wramHSysIDCache:wramHSysIDCache+0x100], ip[:0x100])

	// IP System ID block (IP+$E0..$FF) in the system-variable area at $060002A0..$060002BF.
	copyIPSysBlock(e.bus, ip)

	// Divert the IP's game-load pointer to fastBootLoadGame.
	e.bus.writeWramHU32(wramHGameLoadSlot, fastBootLoadGame)

	m := e.master
	m.SetPR(ipEntry) // execute loop does PC = PR on magic-trap return
	m.SetReg(0, 0x00000358)
	m.SetReg(1, 0x06002100)
	m.SetReg(2, 0x0600021C)
	m.SetReg(3, 0x06001800)
	m.SetReg(4, 0x00000000)
	m.SetReg(5, 0xFFFF7FFF)
	m.SetReg(6, 0x00000000)
	m.SetReg(7, 0x060012FE)
	for r := 8; r <= 14; r++ {
		m.SetReg(r, 0)
	}
	m.SetReg(15, ipStack)
	m.SetVBR(0x06000000)
	m.SetSR(0x00000001)
	m.SetGBR(0x25D00000)
}

// fastBootLoadGame reads the application binary (CD file ID 2) into the 1st-read
// address ($060002B0), standing in for the BIOS CD read pump fast boot skipped.
// Like the HLE, it defers ISO9660 parsing to the CD block (LoadFileByFID).
func (e *Emulator) fastBootLoadGame() {
	if e.bus.cdblock == nil {
		return
	}
	loadAddr := e.bus.readWramHU32(0x2B0)
	if loadAddr < 0x06000000 || loadAddr >= 0x06100000 {
		return
	}
	dst := e.bus.wramH[loadAddr-0x06000000:]
	e.bus.cdblock.LoadFileByFID(2, dst)
}
