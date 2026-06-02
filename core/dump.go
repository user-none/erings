// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/binary"
	"slices"

	"github.com/user-none/erings/core/sh2"
)

// MemoryDump is a point-in-time snapshot of every emulator memory
// region for debug inspection. Each field is an independent copy of
// the underlying bytes, safe to write to disk while the emulator
// continues running.
//
// BIOS is included so a dump captures the exact ROM image that was
// active. Disc image is intentionally excluded - discs are external
// and identified by hash, not state.
type MemoryDump struct {
	BIOS          []byte // 512 KB system BIOS
	WRAMH         []byte // 1 MB Work RAM-H
	WRAML         []byte // 1 MB Work RAM-L
	BackupRAM     []byte // 32 KB internal backup RAM
	ExtRAM        []byte // 4 MB Extended RAM Cartridge
	VDP1VRAM      []byte // 512 KB VDP1 command/character VRAM
	VDP1DrawFB    []byte // 256 KB VDP1 draw framebuffer
	VDP1DisplayFB []byte // 256 KB VDP1 display framebuffer
	VDP2VRAM      []byte // 512 KB VDP2 VRAM
	VDP2CRAM      []byte // 4 KB VDP2 color RAM
	VDP2Regs      []byte // 288 B - 144 VDP2 registers, big-endian at hardware offsets
	VDP1Regs      []byte // 24 B - VDP1 register block (0x00-0x16), internal written values
	VDP1Shadow    []byte // 10 B - VDP1 deferred/pending latch values (no hardware address)
	SoundRAM      []byte // 512 KB SCSP sound RAM

	SCURegs     []byte // SCU register space 0x00-0xC8, big-endian longwords at hw offsets
	SCUInternal []byte // SCU emulation-internal DMA/IRQ/timer state (no hardware address)
	SCUDSP      []byte // SCU DSP program RAM, data RAM, and control/flag state

	SCSPRegs   []byte // SCSP register file, flat big-endian, maps to hardware offsets
	SCSPSlots  []byte // SCSP per-slot runtime playback/envelope state (32 slots)
	SCSPDSP    []byte // SCSP DSP coefficient/program/work state
	SCSPTimers []byte // SCSP timer counters and prescalers (not register-readable)

	SH2MasterRegs []byte // Master SH-2 programmer registers + cycles + halted
	SH2SlaveRegs  []byte // Slave SH-2 programmer registers + cycles + halted
}

// dumpVDP2Regs serializes the VDP2 register file to a big-endian byte
// block whose offset N holds the register at hardware byte-offset N
// (register index N/2). This is the internal stored state - for the
// configuration registers it is exactly what was last written, and it
// decodes 1:1 against the VDP2 register address map.
func dumpVDP2Regs(v *VDP2) []byte {
	out := make([]byte, vdp2RegCount*2)
	for i, r := range v.regs {
		binary.BigEndian.PutUint16(out[i*2:], r)
	}
	return out
}

// dumpVDP1Regs assembles the VDP1 register block at hardware offsets
// 0x00-0x16, populated with the internal active (driving) values rather
// than hardware-readback (which returns 0 for the write-only registers).
// This preserves what was written to the write-only registers. MODR
// (0x16) is built as the game would read it.
func dumpVDP1Regs(v *VDP1) []byte {
	out := make([]byte, 0x18)
	binary.BigEndian.PutUint16(out[0x00:], v.tvmr)
	binary.BigEndian.PutUint16(out[0x02:], v.fbcr)
	binary.BigEndian.PutUint16(out[0x04:], v.ptmr)
	binary.BigEndian.PutUint16(out[0x06:], v.ewdr)
	binary.BigEndian.PutUint16(out[0x08:], v.ewlr)
	binary.BigEndian.PutUint16(out[0x0A:], v.ewrr)
	// 0x0C ENDR and 0x0E reserved have no stored value - left zero.
	binary.BigEndian.PutUint16(out[0x10:], v.edsr)
	binary.BigEndian.PutUint16(out[0x12:], v.lopr)
	binary.BigEndian.PutUint16(out[0x14:], v.copr)
	binary.BigEndian.PutUint16(out[0x16:], v.buildMODR())
	return out
}

// dumpVDP1Shadow serializes the VDP1 deferred/pending latch registers,
// which hold the latest written value before it latches into the active
// register at vblank-IN. These have no hardware address; the order is
// fbcr, ptmr, ewdr, ewlr, ewrr.
func dumpVDP1Shadow(v *VDP1) []byte {
	out := make([]byte, 10)
	binary.BigEndian.PutUint16(out[0:], v.fbcrPending)
	binary.BigEndian.PutUint16(out[2:], v.ptmrPending)
	binary.BigEndian.PutUint16(out[4:], v.ewdrPending)
	binary.BigEndian.PutUint16(out[6:], v.ewlrPending)
	binary.BigEndian.PutUint16(out[8:], v.ewrrPending)
	return out
}

// b2u8 serializes a bool as 1 (true) or 0 (false) for dump blocks.
func b2u8(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// dumpSCURegs serializes the SCU register space (offsets 0x00-0xC8, stride
// 4) as big-endian longwords whose position equals the hardware byte
// offset. It uses SCU.ReadInternal, the side-effect-free path that returns
// the stored value of write-only registers too, so the block reflects what
// was written. DSP port registers (0x80/0x8C) read as their internal value;
// the full DSP state is captured separately by dumpSCUDSP.
func dumpSCURegs(s *SCU) []byte {
	const lastOffset = 0xC8
	out := make([]byte, lastOffset+4)
	for off := uint32(0); off <= lastOffset; off += 4 {
		binary.BigEndian.PutUint32(out[off:], s.ReadInternal(off))
	}
	return out
}

// dumpSCUInternal serializes SCU state that has no hardware register
// address: per-level DMA arm/delay tracking, the IRL-asserted interrupt
// bit, the Timer-0 H-Blank counter, and the DSP cycle carry.
// Layout: dmaPending[3] (u8), dmaDelay[3] (i32), pendingBit (i32),
// t0cnt (u32), dspCycleCarry (u32).
func dumpSCUInternal(s *SCU) []byte {
	out := make([]byte, 0, 3+3*4+4+4+4)
	for i := 0; i < 3; i++ {
		out = append(out, b2u8(s.dmaPending[i]))
	}
	for i := 0; i < 3; i++ {
		out = binary.BigEndian.AppendUint32(out, uint32(int32(s.dmaDelay[i])))
	}
	out = binary.BigEndian.AppendUint32(out, uint32(int32(s.pendingBit)))
	out = binary.BigEndian.AppendUint32(out, s.t0cnt)
	out = binary.BigEndian.AppendUint32(out, s.dspCycleCarry)
	return out
}

// dumpSCUDSP serializes the SCU DSP: program RAM (256 longwords), data RAM
// (4 banks x 64 longwords), then the control/accumulator/flag state. All
// big-endian. Flags are packed into one byte in the order S,Z,C,V,End,T0.
func dumpSCUDSP(d *scuDSP) []byte {
	out := make([]byte, 0, 256*4+4*64*4+64)
	for _, w := range d.prog {
		out = binary.BigEndian.AppendUint32(out, w)
	}
	for bank := 0; bank < 4; bank++ {
		for _, w := range d.data[bank] {
			out = binary.BigEndian.AppendUint32(out, w)
		}
	}
	out = append(out, d.pc, d.top)
	out = binary.BigEndian.AppendUint16(out, d.lop)
	out = append(out, d.ct[0], d.ct[1], d.ct[2], d.ct[3])
	out = binary.BigEndian.AppendUint16(out, d.ach)
	out = binary.BigEndian.AppendUint32(out, d.acl)
	out = binary.BigEndian.AppendUint16(out, d.ph)
	out = binary.BigEndian.AppendUint32(out, d.pl)
	out = binary.BigEndian.AppendUint32(out, d.rx)
	out = binary.BigEndian.AppendUint32(out, d.ry)
	out = binary.BigEndian.AppendUint32(out, d.ra0)
	out = binary.BigEndian.AppendUint32(out, d.wa0)
	out = append(out, d.pdaAddr)
	var flags byte
	for i, f := range []bool{d.flagS, d.flagZ, d.flagC, d.flagV, d.flagEnd, d.flagT0} {
		if f {
			flags |= 1 << uint(i)
		}
	}
	out = append(out, flags)
	out = append(out, b2u8(d.executing), b2u8(d.looping))
	out = binary.BigEndian.AppendUint32(out, d.nextInstr)
	out = binary.BigEndian.AppendUint32(out, d.debt)
	return out
}

// dumpSCSPRegs serializes the SCSP register file big-endian. Index N sits
// at hardware byte offset N*2, so the block decodes 1:1 against the SCSP
// register map (slot N at byte N*0x20, common control at fixed offsets).
func dumpSCSPRegs(s *SCSP) []byte {
	out := make([]byte, scspRegWords*2)
	for i, r := range s.regs {
		binary.BigEndian.PutUint16(out[i*2:], r)
	}
	return out
}

// dumpSCSPSlots serializes the per-slot runtime playback and envelope
// state for all 32 slots. This is emulation-internal state (no hardware
// address) and is the data needed to diagnose a dormant slot. Per-slot
// layout: phase (u32), egLevel (i32), egState (u8), egStep (i32),
// egTarget (i32), active (u8), finished (u8), loopDir (i8), output (i16),
// lfoPhase (u8), lfoStep (u16), lfoNoise (u16).
func dumpSCSPSlots(s *SCSP) []byte {
	out := make([]byte, 0, 32*30)
	for i := range s.slots {
		sl := &s.slots[i]
		out = binary.BigEndian.AppendUint32(out, sl.phase)
		out = binary.BigEndian.AppendUint32(out, uint32(sl.egLevel))
		out = append(out, sl.egState)
		out = binary.BigEndian.AppendUint32(out, uint32(sl.egStep))
		out = binary.BigEndian.AppendUint32(out, uint32(sl.egTarget))
		out = append(out, b2u8(sl.active), b2u8(sl.finished), byte(sl.loopDir))
		out = binary.BigEndian.AppendUint16(out, uint16(sl.output))
		out = append(out, sl.lfoPhase)
		out = binary.BigEndian.AppendUint16(out, sl.lfoStep)
		out = binary.BigEndian.AppendUint16(out, sl.lfoNoise)
	}
	return out
}

// dumpSCSPDSP serializes the SCSP DSP processor state: the microprogram,
// coefficient/address tables, work/memory buffers, and pipeline latches.
// All big-endian. These hold the live DSP values, which are not mirrored
// back into the SCSP register file.
func dumpSCSPDSP(d *scspDSP) []byte {
	out := make([]byte, 0, 128*8+128*4+32*4+16*4+2*4+16*2+64*2+32*2+64)
	for _, w := range d.mpro {
		out = binary.BigEndian.AppendUint64(out, w)
	}
	for _, w := range d.temp {
		out = binary.BigEndian.AppendUint32(out, uint32(w))
	}
	for _, w := range d.mems {
		out = binary.BigEndian.AppendUint32(out, uint32(w))
	}
	for _, w := range d.mixs {
		out = binary.BigEndian.AppendUint32(out, uint32(w))
	}
	for _, w := range d.exts {
		out = binary.BigEndian.AppendUint32(out, uint32(w))
	}
	for _, w := range d.efreg {
		out = binary.BigEndian.AppendUint16(out, uint16(w))
	}
	for _, w := range d.coef {
		out = binary.BigEndian.AppendUint16(out, uint16(w))
	}
	for _, w := range d.madrs {
		out = binary.BigEndian.AppendUint16(out, w)
	}
	out = binary.BigEndian.AppendUint32(out, uint32(d.sftReg))
	out = binary.BigEndian.AppendUint16(out, d.frcReg)
	out = binary.BigEndian.AppendUint32(out, uint32(d.yReg))
	out = binary.BigEndian.AppendUint16(out, d.adrsReg)
	out = binary.BigEndian.AppendUint16(out, d.mdecCT)
	out = binary.BigEndian.AppendUint32(out, uint32(d.inputs))
	out = append(out, d.readPending)
	out = binary.BigEndian.AppendUint32(out, uint32(d.readValue))
	out = append(out, b2u8(d.writePending))
	out = binary.BigEndian.AppendUint16(out, d.writeValue)
	out = binary.BigEndian.AppendUint32(out, d.rwAddr)
	out = binary.BigEndian.AppendUint32(out, uint32(int32(d.lastStep)))
	return out
}

// dumpSCSPTimers serializes the SCSP timer prescalers and counters, which
// are advanced by the sample tick and are not readable through registers.
// Layout: timerPrescaler[3] (u16), timerCounter[3] (u8).
func dumpSCSPTimers(s *SCSP) []byte {
	out := make([]byte, 0, 3*2+3)
	for _, p := range s.timerPrescaler {
		out = binary.BigEndian.AppendUint16(out, p)
	}
	out = append(out, s.timerCounter[0], s.timerCounter[1], s.timerCounter[2])
	return out
}

// dumpSH2Regs serializes the programmer-visible SH-2 register file plus the
// total cycle count and halted (SLEEP) flag, via the existing exported
// accessors. Layout: R0..R15 (u32), PC, PR, SR, GBR, VBR, MACH, MACL
// (u32), cycles (u64), halted (u8). All big-endian.
func dumpSH2Regs(c *sh2.CPU) []byte {
	r := c.Registers()
	out := make([]byte, 0, 16*4+7*4+8+1)
	for _, gr := range r.R {
		out = binary.BigEndian.AppendUint32(out, gr)
	}
	out = binary.BigEndian.AppendUint32(out, r.PC)
	out = binary.BigEndian.AppendUint32(out, r.PR)
	out = binary.BigEndian.AppendUint32(out, r.SR)
	out = binary.BigEndian.AppendUint32(out, r.GBR)
	out = binary.BigEndian.AppendUint32(out, r.VBR)
	out = binary.BigEndian.AppendUint32(out, r.MACH)
	out = binary.BigEndian.AppendUint32(out, r.MACL)
	out = binary.BigEndian.AppendUint64(out, c.Cycles())
	out = append(out, b2u8(c.Halted()))
	return out
}

// DumpMemory returns a deep-copy snapshot of every memory region.
// Safe to call between frames from the emulation goroutine.
func (e *Emulator) DumpMemory() MemoryDump {
	return MemoryDump{
		BIOS:          slices.Clone(e.bus.bios),
		WRAMH:         slices.Clone(e.bus.wramH),
		WRAML:         slices.Clone(e.bus.wramL),
		BackupRAM:     slices.Clone(e.bus.backup),
		ExtRAM:        slices.Clone(e.bus.extRAM),
		VDP1VRAM:      slices.Clone(e.vdp1.vram),
		VDP1DrawFB:    slices.Clone(e.vdp1.drawFB),
		VDP1DisplayFB: slices.Clone(e.vdp1.displayFB),
		VDP2VRAM:      slices.Clone(e.vdp2.vram),
		VDP2CRAM:      slices.Clone(e.vdp2.cram),
		VDP2Regs:      dumpVDP2Regs(e.vdp2),
		VDP1Regs:      dumpVDP1Regs(e.vdp1),
		VDP1Shadow:    dumpVDP1Shadow(e.vdp1),
		SoundRAM:      slices.Clone(e.scsp.ram),
		SCURegs:       dumpSCURegs(e.scu),
		SCUInternal:   dumpSCUInternal(e.scu),
		SCUDSP:        dumpSCUDSP(&e.scu.dsp),
		SCSPRegs:      dumpSCSPRegs(e.scsp),
		SCSPSlots:     dumpSCSPSlots(e.scsp),
		SCSPDSP:       dumpSCSPDSP(&e.scsp.dsp),
		SCSPTimers:    dumpSCSPTimers(e.scsp),
		SH2MasterRegs: dumpSH2Regs(e.master),
		SH2SlaveRegs:  dumpSH2Regs(e.slave),
	}
}
