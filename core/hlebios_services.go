// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"

	"github.com/user-none/erings/core/sh2"
)

// registerServices wires each SYS_*/PER_* magic address to its Go
// implementation. Called from HLEBIOS.Boot before the CPU hooks are
// armed.
func (h *HLEBIOS) registerServices() {
	h.register(hleSentinel, hleSentinelService)

	h.register(hleSysSetUint, hleSysSetUintService)
	h.register(hleSysGetUint, hleSysGetUintService)
	h.register(hleSysSetSint, hleSysSetSintService)
	h.register(hleSysGetSint, hleSysGetSintService)
	h.register(hleSysTassem, hleSysTassemService)
	h.register(hleSysClrsem, hleSysClrsemService)
	h.register(hleSysSetScuim, hleSysSetScuimService)
	h.register(hleSysChgScuim, hleSysChgScuimService)
	h.register(hleSysChgSysCk, hleSysChgSysCkService)

	// BIOS-published WRAM-H routine replacements.
	h.register(hleBiosFill, hleBiosFillService)
	h.register(hleBiosCopy, hleBiosCopyService)
	h.register(hleBiosScuISTClear, hleBiosScuISTClearService)
	h.register(hleBiosByteExpand, hleBiosByteExpandService)
	// Warning-only handlers for routines we haven't implemented yet.
	h.register(hleBiosWarnSMPCInit, hleWarnHandler("sub_04C8 SMPC init"))
	h.register(hleBiosWarnGBRZero, hleWarnHandler("sub_1800 GBR zero-fill"))
	h.register(hleBiosWarnRegionInit, hleWarnHandler("sub_1A18 region-init copy"))
	h.register(hleBiosWarnCDBlock, hleWarnHandler("CD-block BIOS helper"))

	// sub_1C90: real Go impl that reads the game executable from
	// disc into $06004000 (HLE shortcut bypassing the BIOS CD-block
	// API).
	h.register(hleBiosSub1C90Load, hleBiosSub1C90LoadGameService)

	// PER_Init: peripheral subsystem init. Lays down the 11-slot
	// driver function-pointer table at the caller's R4 buffer
	// (magic addresses dispatched by hlebios_per.go services),
	// registers driver_base in WRAM-H at $06000354, and writes the
	// initial BupConfig to the caller's R6 buffer — matching the
	// side effect of real BIOS PER_Init invoking its decompressed
	// driver's slot-0 relocator.
	h.register(hleBiosPERInit, hleBiosPERInitService)

	// Slave SH-2 init. Reached when slave.Reset (via SMPC SSHON)
	// reloads PC from the BIOS reset vector at $00000000, which HLE
	// has pointed at hleSlaveInit. Reproduces the real-BIOS slave
	// init effects ($00000200 + $06000600 path).
	h.register(hleSlaveInit, hleSlaveInitService)

	// 11 PER driver slots accessible via the function-pointer
	// table PER_Init builds at driver_base. Game code does
	//   MOV.L @($06000354),Rn ; JSR @(N*4,Rn)
	// to dispatch to slot N. See hlebios_per.go for the per-slot
	// behavior and return-code contracts.
	h.register(hlePerDriverSlot0, hlePerDriverSlot0Service)
	h.register(hlePerDriverSlot1, hlePerDriverSlot1Service)
	h.register(hlePerDriverSlot2, hlePerDriverSlot2Service)
	h.register(hlePerDriverSlot3, hlePerDriverSlot3Service)
	h.register(hlePerDriverSlot4, hlePerDriverSlot4Service)
	h.register(hlePerDriverSlot5, hlePerDriverSlot5Service)
	h.register(hlePerDriverSlot6, hlePerDriverSlot6Service)
	h.register(hlePerDriverSlot7, hlePerDriverSlot7Service)
	h.register(hlePerDriverSlot8, hlePerDriverSlot8Service)
	h.register(hlePerDriverSlot9, hlePerDriverSlot9Service)
	h.register(hlePerDriverSlot10, hlePerDriverSlot10Service)
}

// hleSentinelService is the default empty handler. Used as the
// installed value for every dispatch slot that has no real
// implementation, and for the per-vector "no handler" default in
// the SCU table. The trap in sh2/execute.go sets PC := PR after
// the service returns, so an empty body is a clean no-op call.
func hleSentinelService(cpu *sh2.CPU, bus *Bus) {}

// hleSysSetUintService implements SYS_SETUINT.
//
// Install a user interrupt handler in the SCU dispatch table.
//
//	R4 = vector number (real BIOS does SHLL2 R4 then stores at
//	     $06000900 + R4*4; for SCU vectors $40-$5F that lands at
//	     $06000A00-$06000A7F).
//	R5 = handler address
//
// No return value. Bounds are the caller's responsibility, same as
// on hardware.
func hleSysSetUintService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	// Match real BIOS behavior at $06000794: if the caller passes
	// handler=0 ("clear handler"), substitute the default no-op
	// handler at $0600083C so an IRQ firing through this slot
	// returns cleanly instead of jumping through a null pointer.
	// The address has to be the real-BIOS address ($0600083C) and
	// not a magic-address sentinel because game code reads slot
	// values as 16-bit halves; a magic-address ($A0...) sign-
	// extends negative and breaks downstream pointer arithmetic.
	handler := r.R[5]
	if handler == 0 {
		handler = wramHNoopHandlerAddr
	}
	bus.writeWramHU32(wramHUIntTable+r.R[4]*4, handler)
}

// hleSysGetUintService implements SYS_GETUINT.
//
// Read the currently-installed user interrupt handler.
//
//	R4 = vector number (same indexing as SETUINT)
//	R0 = handler address
func hleSysGetUintService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	cpu.SetReg(0, bus.readWramHU32(wramHUIntTable+r.R[4]*4))
}

// hleSysSetSintService implements SYS_SETSINT.
//
// Install an SH-2-level interrupt vector via the CPU's VBR table.
//
//	R4 = vector index (in 32-bit units relative to VBR)
//	R5 = handler address; 0 selects the per-vector default from the
//	     table at BIOS $0600 (SCU vectors $40-$5F default to their
//	     dispatcher trampoline). See docs/bios/system_services.md.
//
// No return value.
func hleSysSetSintService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	idx := r.R[4]
	handler := r.R[5]
	if handler == 0 {
		off := wramHIntDefaultTable + idx*4
		if int(off)+4 <= len(bus.bios) {
			handler = uint32(bus.bios[off])<<24 | uint32(bus.bios[off+1])<<16 |
				uint32(bus.bios[off+2])<<8 | uint32(bus.bios[off+3])
		}
	}
	bus.Write32(r.VBR+idx*4, handler)
}

// hleSysGetSintService implements SYS_GETSINT.
//
// Read the SH-2-level interrupt vector at VBR + R4*4.
//
//	R4 = vector index
//	R0 = current handler address
func hleSysGetSintService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	cpu.SetReg(0, bus.Read32(r.VBR+r.R[4]*4))
}

// hleSysTassemService implements SYS_TASSEM.
//
// Test-and-set a byte semaphore.
//
//	R4 = semaphore index (0..255)
//
// Returns in R0:
//
//	1 = semaphore was free, now acquired
//	0 = semaphore was already held
func hleSysTassemService(cpu *sh2.CPU, bus *Bus) {
	idx := cpu.Registers().R[4] & 0xFF
	off := wramHSemArray + idx
	if bus.wramH[off] == 0 {
		bus.wramH[off] = 0x80
		cpu.SetReg(0, 1)
	} else {
		cpu.SetReg(0, 0)
	}
}

// hleSysClrsemService implements SYS_CLRSEM.
//
// Release a byte semaphore.
//
//	R4 = semaphore index (0..255)
//
// No return value.
func hleSysClrsemService(cpu *sh2.CPU, bus *Bus) {
	idx := cpu.Registers().R[4] & 0xFF
	bus.wramH[wramHSemArray+idx] = 0
}

// hleSysSetScuimService implements SYS_SETSCUIM.
//
// Overwrite the SCU IMS (Interrupt Mask Set) register and the
// shadow at $06000348 that SDK code reads for SYS_GETSCUIM (the
// live IMS register is write-only on hardware).
//
//	R4 = new IMS value (low 16 bits drive the live mask; the full
//	     32-bit value is kept in the shadow, matching the BIOS)
//
// No return value.
func hleSysSetScuimService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	val := r.R[4]
	bus.scu.Write(0xA0, val)
	bus.writeWramHU32(wramHIMSShadow, val)
}

// hleSysChgScuimService implements SYS_CHGSCUIM.
//
// Modify the SCU IMS register: new = (current & R4) | R5. Lets
// callers clear specific mask bits and set others atomically.
//
//	R4 = AND mask (bits to keep)
//	R5 = OR mask (bits to add)
//
// No return value. The "current" value comes from the shadow at
// $06000348 since the live IMS is write-only.
func hleSysChgScuimService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	cur := bus.readWramHU32(wramHIMSShadow)
	newVal := (cur & r.R[4]) | r.R[5]
	bus.scu.Write(0xA0, newVal)
	bus.writeWramHU32(wramHIMSShadow, newVal)
}

// hleSysChgSysCkService implements SYS_CHGSYSCK.
//
// Reproduces the bus-visible effects of the real routine ($060006B0), in
// order: the clock-mode word at $06000324 and VDP2 TVMD take the requested
// mode (R4 bit 0); the SCU is reset (DMA disabled, A-bus reprogrammed,
// timers stopped); and the SCU IMS is reloaded from its $06000348 shadow
// exactly as SYS_SETSCUIM does (sign-extended IST, conditional AIACK). The
// SMPC clock command and its NMI are not issued - their register effects
// are written here directly.
//
//	R4 = clock mode (bit 0: 0 = 320, 1 = 352)
func hleSysChgSysCkService(cpu *sh2.CPU, bus *Bus) {
	mode := cpu.Registers().R[4] & 1

	// SMPC half: clock-mode word, transient AIACK/AREF clears, SCU RSEL,
	// and the VDP2 TVMD resolution change.
	bus.Write32(0x06000324, mode)
	bus.Write32(0x05FE00A8, 0)
	bus.Write32(0x05FE00B8, 0)
	bus.Write32(0x05FE00C4, 1)
	bus.Write16(0x05F80000, uint16(mode))

	// SCU half (sub_1800): disable DMA, reprogram A-bus, stop timers.
	for lvl := uint32(0); lvl < 3; lvl++ {
		b := uint32(0x05FE0000) + lvl*0x20
		bus.Write32(b+0x00, 0)
		bus.Write32(b+0x04, 0)
		bus.Write32(b+0x08, 0)
		bus.Write32(b+0x0C, 0)
		bus.Write32(b+0x10, 0)
		bus.Write32(b+0x14, 7)
	}
	bus.Write32(0x05FE0060, 0)
	bus.Write32(0x05FE0080, 0)
	bus.Write32(0x05FE00B0, 0x1FF01FF0)
	bus.Write32(0x05FE00B4, 0x1FF01FF0)
	bus.Write32(0x05FE00B8, 0x1F)
	bus.Write32(0x05FE00A8, 1)
	bus.Write32(0x05FE0090, 0x3FF)
	bus.Write32(0x05FE0094, 0x1FF)
	bus.Write32(0x05FE0098, 0)

	// SETSCUIM tail: reload the SCU IMS from the $06000348 shadow.
	shadow := bus.readWramHU32(wramHIMSShadow)
	bus.Write32(0x06000348, shadow)
	bus.Write32(0x05FE00A0, shadow)
	ist := uint32(int32(int16(uint16(shadow))))
	bus.Write32(0x05FE00A4, ist)
	if int32(ist) >= 0 {
		bus.Write32(0x05FE00A8, 1)
	}
}

// hleBiosFillService replaces BIOS sub_02AC.
//
// Reads `[count:32, dest:32]` from the address in R0, then writes
// R4 (the fill value) to `count` consecutive longwords starting at
// dest. Advances R0 past the two consumed table entries so the
// caller can chain into the next routine.
//
// Caller convention (matches real BIOS):
//
//	R0 (in)  = pointer to table entries
//	R4 (in)  = fill value
//	R0 (out) = pointer past the consumed [count, dest] pair
func hleBiosFillService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	r0 := r.R[0]
	count := bus.Read32(r0)
	dest := bus.Read32(r0 + 4)
	val := r.R[4]
	for i := uint32(0); i < count; i++ {
		bus.Write32(dest+i*4, val)
	}
	cpu.SetReg(0, r0+8)
}

// hleBiosCopyService replaces BIOS sub_02BC.
//
// Reads `[count:32, dest:32, src:32]` from the address in R0, then
// copies `count` longwords from src to dest. Advances R0 past the
// three consumed table entries.
//
// Caller convention:
//
//	R0 (in)  = pointer to table entries
//	R0 (out) = pointer past the consumed [count, dest, src] triple
func hleBiosCopyService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	r0 := r.R[0]
	count := bus.Read32(r0)
	dest := bus.Read32(r0 + 4)
	src := bus.Read32(r0 + 8)
	for i := uint32(0); i < count; i++ {
		bus.Write32(dest+i*4, bus.Read32(src+i*4))
	}
	cpu.SetReg(0, r0+12)
}

// hleBiosScuISTClearService replaces the BIOS WRAM-H routine at
// $06000664. The real BIOS code is two instructions:
//
//	MOV.L @($C,PC),R0  ; R0 = $25FE00A0  (SCU IMS base)
//	MOV.L R4,@(4,R0)   ; mem.L[$25FE00A4] = R4   (clear IST bits set in R4)
//
// Writing to SCU IST with bits set clears those bits (SCU IST is
// write-1-to-clear). Used for interrupt acknowledgment by routines
// that need to clear their pending bit.
func hleBiosScuISTClearService(cpu *sh2.CPU, bus *Bus) {
	bus.scu.Write(0xA4, cpu.Registers().R[4])
}

// hleBiosByteExpandService replaces the BIOS WRAM-H routine at
// $06000810. Sign-extends 32 source bytes from @R4 (post-increment)
// to 32-bit values and writes them as 32 longwords to a fixed
// destination at $06000A7C in WRAM-H. Used by the BIOS to update a
// per-vector data block from a packed byte representation.
//
// The real BIOS code masks all interrupts (SR=$00F0) around the
// loop for atomicity; HLE doesn't need that since the entire Go
// function runs uninterrupted between SH-2 cycles.
//
// Caller convention:
//
//	R4 (in)  = source byte array (32 bytes)
//	R4 (out) = R4 + 32  (advanced past consumed bytes)
//	R0 (out) = $06000A7C + 0x80  (advanced past 32 longwords written)
func hleBiosByteExpandService(cpu *sh2.CPU, bus *Bus) {
	src := cpu.Registers().R[4]
	const dest = uint32(0x06000A7C)
	for i := uint32(0); i < 32; i++ {
		b := bus.Read8(src + i)
		// Sign-extend byte to 32-bit (matches MOV.B which sign-extends).
		v := uint32(int32(int8(b)))
		bus.Write32(dest+i*4, v)
	}
	cpu.SetReg(4, src+32)
	cpu.SetReg(0, dest+32*4)
}

// hleWarnHandler returns a service that prints a warning when
// invoked. Used for BIOS routines we haven't reimplemented yet but
// want to know about: if an IP turns out to call one, the warning
// surfaces the slot and we can prioritize a real implementation.
func hleWarnHandler(name string) hleFunc {
	return func(cpu *sh2.CPU, bus *Bus) {
		r := cpu.Registers()
		fmt.Printf("[HLE][WARN] %s called (PR=$%08X R0=$%08X R4=$%08X R5=$%08X)\n",
			name, r.PR, r.R[0], r.R[4], r.R[5])
	}
}

// hleBiosSub1C90LoadGameService is the HLE-shortcut replacement for
// BIOS sub_1C90 ($06000284 in the WRAM-H function-pointer table).
//
// On real BIOS, sub_1C90 installs a BIOS-side VBLANK handler for
// the loading-screen animation and issues a CD-block read of the
// game executable into the address from System ID +$F0. The IP's
// loading-screen loop runs while the CD-block transfers in the
// background. Per the disassembly of sub_2F48 + sub_3A26, the BIOS
// uses CD-block command $73 (Get File Info) hard-coded to file ID 2
// followed by sub_2DA6 to drain the data buffer.
//
// HLE version: route through CDBlock.LoadFileByFID(2, dst) - the
// CD-block owns ISO9660 parsing and the file-ID table, so the BIOS
// layer just asks "give me file ID 2". This keeps disc parsing in
// one place. Synchronous (Boot-time / hook-time) rather than spread
// across multiple VBLANK ticks, but transfers the same bytes the
// real BIOS would have.
//
// Load destination = mem.L[$060002B0] (the BIOS set this to System
// ID +$F0 = $06004000 for NiGHTS before handoff).
func hleBiosSub1C90LoadGameService(cpu *sh2.CPU, bus *Bus) {
	if bus.cdblock == nil {
		fmt.Println("[HLE][ERROR] sub_1C90: no CDBlock")
		return
	}
	loadAddr := bus.readWramHU32(0x2B0)
	if loadAddr == 0 {
		fmt.Println("[HLE][ERROR] sub_1C90: $060002B0 load destination is zero")
		return
	}
	if loadAddr < 0x06000000 || loadAddr >= 0x06100000 {
		fmt.Printf("[HLE][ERROR] sub_1C90: load destination $%08X outside WRAM-H\n", loadAddr)
		return
	}
	wramOff := loadAddr - 0x06000000
	dst := bus.wramH[wramOff:]
	const biosFID = 2
	_, err := bus.cdblock.LoadFileByFID(biosFID, dst)
	if err != nil {
		fmt.Printf("[HLE][ERROR] sub_1C90: LoadFileByFID(%d): %v\n", biosFID, err)
		return
	}
	//fmt.Printf("[HLE] sub_1C90: loaded %d bytes of CD file ID %d into $%08X-$%08X\n",
	//	n, biosFID, loadAddr, loadAddr+uint32(n)-1)
}

// hleBiosPERInitService is the HLE handler for BIOS PER_Init
// (slot $06000358, real-BIOS body at $0007D600).
//
// Real BIOS PER_Init is a thin trampoline: it decompresses the
// 16 KB hybrid PER + BUP driver from $0007D660 into the buffer at
// caller's R4 via sub_1F04, then JSRs into the driver's slot-0
// relocator, which builds an 11-entry function-pointer table at
// the workbuff (caller's R5), writes the initial BupConfig to
// caller's R6, and exits with R4/R5 in a specific state.
//
// SDK signature: PER_Init(libaddr, workbuff, conf[3]):
//
//	R4 = libaddr   — driver-code buffer (real BIOS decompress
//	                 destination; HLE ignores)
//	R5 = workbuff  — slot-table address. The HLE lays down 11
//	                 magic-address slots at workbuff+$00..+$28
//	                 plus the per-entry working buffer pointer
//	                 at workbuff+$2C, plus driver-state markers
//	                 at workbuff+$54..+$67.
//	R6 = conf[3]   — BupConfig buffer. Slot-0 writes the device
//	                 descriptor {unit_id=1, partition=1} followed
//	                 by four zero words (12 bytes); see the body.
//
// Game code subsequently dispatches to slot N via
// JSR @(N*4, workbuff); the magic addresses land in the SH-2
// magic-address trap and the hlePerDriverSlotN services run.
//
// Exit register state (matches a side-by-side trace of real BIOS
// slot 0, which the PER_Init trampoline invokes internally):
//
//	R4 = entry R6 + 8
//	R5 = 0
//
// Registers the driver by writing mem.L[$06000354] = workbuff (the
// final step of writePerDriverTable), matching real BIOS PER_Init,
// which JSRs its decompressed slot-0 relocator internally and that
// relocator writes $06000354 before PER_Init returns. The SP-chain
// trick (mem.L[mem.L[$06000354]+12] → reset SP) is performed by the
// IP during boot, before any game calls PER_Init, so registering
// here does not disturb it.
//
// The workbuff may live in Work RAM-L or Work RAM-H depending on the
// title; see isPerBufferRAM. A workbuff outside RAM is ignored.
func hleBiosPERInitService(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	workbuff := r.R[5]
	periphBuf := r.R[6]
	if isPerBufferRAM(workbuff) {
		writePerDriverTable(bus, workbuff)
	}
	if isPerBufferRAM(periphBuf) {
		// Slot-0 writes the initial BupConfig into the caller's R6
		// buffer: unit_id=1 (internal backup device present), partition=1,
		// then four zero words (12 bytes total). Games read mem.W[R6+0]
		// to confirm a backup device is present before issuing BUP_* calls.
		bus.Write16(periphBuf+0, 1)
		bus.Write16(periphBuf+2, 1)
		bus.Write16(periphBuf+4, 0)
		bus.Write16(periphBuf+6, 0)
		bus.Write16(periphBuf+8, 0)
		bus.Write16(periphBuf+10, 0)
	}
	cpu.SetReg(4, r.R[6]+8)
	cpu.SetReg(5, 0)
}

// isPerBufferRAM reports whether addr is a plausible PER_Init buffer
// pointer (work-buffer / peripheral-output buffer). Games may place
// these in either Work RAM-L ($00200000-$002FFFFF) or Work RAM-H
// ($06000000-$060FFFFF): NiGHTS uses WRAM-H, Cotton Boomerang uses
// WRAM-L. Earlier code accepted only WRAM-H, so a WRAM-L workbuff
// silently skipped driver registration ($06000354 stayed zero) and
// the game's first peripheral dispatch faulted on the null driver.
func isPerBufferRAM(addr uint32) bool {
	return (addr >= 0x00200000 && addr < 0x00300000) ||
		(addr >= 0x06000000 && addr < 0x06100000)
}

// hleSlaveInitService is the HLE handler for slave SH-2 init. The
// slave reaches the magic address by way of its reset-vector PC:
// SMPC SSHON pulses slave.Reset, which calls LoadResetVectors, which
// loads PC from mem.L[$00000000] (= hleSlaveInit per
// installVirtualBIOS) and R15 from mem.L[$00000004] (= $06002000).
//
// Real-BIOS slave init runs at $00000200 (cache + BSC init + warm-
// boot poll for "2RDY") then JMPs to $06000600 in WRAM-H, where it
// sets the slave VBR, signals master via "2RDS", sets the slave SP,
// clears GPRs, and JMPs to the game-installed slave entry at
// mem.L[$06000250]. See docs/bios/slave_sh2_init.md "Slave
// SH-2 Init" for the full disassembly.
//
// HLE skips the cache/BSC init (our SH-2 emulator's reset state is
// already workable) and the "2RDY" poll (HLE master never runs the
// cold-boot init that would set "2RDY"). The structurally meaningful
// effects we reproduce:
//
//  1. Slave VBR = $06000400 (SDK convention; real BIOS $0600060E).
//  2. mem.L[$06000244] = "2RDS" ($32524453) — master's slave-ready
//     signal (real BIOS $06000614).
//  3. Slave R15 = game override at mem.L[$060002AC] if non-zero,
//     else $06001000 (SDK default; real BIOS $06000600/$06000632).
//  4. PR = mem.L[$06000250] (game's slave entry). After this
//     service returns, the magic-address trap in sh2/execute.go
//     sets PC := PR, landing the slave at the game's entry.
//
// If $06000250 holds hleSentinel ($A0000020) — its default since
// the game hasn't installed its slave entry yet — the code below
// falls back to PR = $2000020C (a BRA-self halt loop in the
// virtual BIOS ROM) so the slave parks cleanly while still being
// able to take IRQs (FRT input-capture, etc.) and run their
// game-installed handlers.
func hleSlaveInitService(cpu *sh2.CPU, bus *Bus) {
	cpu.SetVBR(0x06000400)
	// "2RDS" ($32524453) slave-ready signal at $06000244. Real BIOS
	// slave-init code at $06000614 writes this after VBR is set;
	// master code that polls $06000244 ("is the slave running?")
	// proceeds only after seeing it. Writing it here matches that
	// timing — this service IS the post-SSHON slave-startup path,
	// so by the time it returns the slave is truly up.
	bus.writeWramHU32(0x244, 0x32524453)
	sp := bus.Read32(0x060002AC)
	if sp == 0 {
		sp = 0x06001000
	}
	cpu.SetReg(15, sp)
	// Drop slave SR to 0 (SR.I = 0). CPU.Reset sets SR = $F0 (all
	// IRQs masked); real-BIOS slave-init code at $0600061C does
	// LDC R0,SR with R0=0 to unmask. Without this, slave at the
	// halt loop cannot accept any maskable IRQ.
	cpu.SetSR(0)

	entry := bus.readWramHU32(0x250)
	if entry == 0 || entry == hleSentinel {
		// No game-installed slave entry. WW7 (and likely other titles)
		// use the slave as a coprocessor that runs an FRT-Input-Capture
		// IRQ handler installed in the slave VBR table; the handler at
		// e.g. $06012AA0 polls FTCSR.ICF and dispatches commands sent
		// by master via MINIT writes. Configure slave's on-chip
		// FRT/INTC registers so the ICI IRQ can fire. These are
		// CPU-private regs only writable by slave itself; real BIOS's
		// $06000600/$06000690 chain writes them — our HLE skips that
		// code and pokes the regs in Go.
		//   INTC VCRC bits 14-8 = ICI vector ($64 — matches game's
		//     handler installed at slave VBR + $64*4 = $06000590).
		//   INTC IPRB bits 11-8 = FRT priority ($F — game handler's
		//     prologue at $06012AAC clears IPRB; it expects max
		//     priority on entry).
		//   FRT TIER bit 7 = ICIE (input capture interrupt enable).
		cpu.INTC().Write(0xFFFFFE66, 0x6400)
		cpu.INTC().Write(0xFFFFFE60, 0x0F00)
		cpu.FRT().Write(0xFFFFFE10, 0x80)
		// These pokes bypass the CPU on-chip write path, so re-evaluate the
		// level-sensitive on-chip interrupt lines explicitly: a master MINIT
		// can arrive before this runs (slave reset by SSHON, master writes
		// MINIT while the slave is still in early boot), latching FTCSR.ICF
		// with ICIE off. Enabling ICIE here must deliver the pending capture.
		cpu.RefreshOnChipInterrupts()
		// Slave halts at BIOS-ROM BRA-self ($2000020C) while waiting
		// for IRQs. Slot $06000250 stays at hleSentinel — only the
		// slave's PR is redirected.
		entry = 0x2000020C
	}
	cpu.SetPR(entry)
}
