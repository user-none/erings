// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/s2"
	"github.com/user-none/erings/core/sh2"
	m68k "github.com/user-none/go-chip-m68k"
)

// Save state container. Two-level tagged, length-prefixed, all values
// big-endian:
//
//	header: magic[8] | version u32 | gameIDLen u8 | gameID | biosHash[32] |
//	        dataCRC u32 | compressed body
//	chunk:  tag[16, zero-filled ASCII] | length u32 | payload[length]
//	field:  nameLen u8 | name | size u32 | data[size]
//
// A chunk payload is a sequence of field records; field names are scoped
// by their chunk. Signed integers are two's complement at the stated
// width; Go int fields are written as i64. The BIOS image is not stored:
// the header carries its SHA-256 and the image is reloaded at runtime.
// An all-zero biosHash marks a state captured under the HLE BIOS.
const (
	stateMagic = "ERINGSST"

	// stateVersion is written into every state header. Deserialize
	// rejects a file whose version is greater (written by a newer
	// erings) with a "state is from a newer version" error. Raised
	// when fields are added; older files simply lack the new fields,
	// which read as zero values on load.
	stateVersion = uint32(6)

	// stateMinVersion is the oldest state-file version Deserialize
	// accepts. A file below it is rejected with a distinct "state is
	// too old" error. Raised only when a format change drops
	// compatibility with older files entirely.
	stateMinVersion = uint32(6)

	stateTagLen = 16
)

// Chunk tags. Must be 1-15 ASCII characters (the 16-byte tag field is
// zero-filled past the name).
const (
	tagWRAMH      = "WRAM_H"
	tagWRAML      = "WRAM_L"
	tagBackup     = "BACKUP"
	tagExtRAM     = "EXTRAM"
	tagVDP1VRAM   = "VDP1_VRAM"
	tagVDP1DrawFB = "VDP1_DRAW_FB"
	tagVDP1DispFB = "VDP1_DISP_FB"
	tagVDP2VRAM   = "VDP2_VRAM"
	tagVDP2CRAM   = "VDP2_CRAM"
	tagSoundRAM   = "SOUND_RAM"
	tagSH2MCore   = "SH2M_CORE"
	tagSH2MCache  = "SH2M_CACHE"
	tagSH2SCore   = "SH2S_CORE"
	tagSH2SCache  = "SH2S_CACHE"
	tagSCUCore    = "SCU_CORE"
	tagSCUDSP     = "SCU_DSP"
	tagSCSPRegs   = "SCSP_REGS"
	tagSCSPSlots  = "SCSP_SLOTS"
	tagSCSPDSP    = "SCSP_DSP"
	tagSCSPTimers = "SCSP_TIMERS"
	tagSCSPMisc   = "SCSP_MISC"
	tagM68K       = "M68K"
	tagVDP1Regs   = "VDP1_REGS"
	tagVDP1Resume = "VDP1_CMDRESUME"
	tagVDP2State  = "VDP2_STATE"
	tagSMPC       = "SMPC"
	tagCDState    = "CD_STATE"
	tagCDBuffers  = "CD_BUFFERS"
	tagCDAudioQ   = "CD_AUDIOQ"
	tagCDMpeg     = "CD_MPEG"
	tagBusMisc    = "BUS_MISC"
	tagEmu        = "EMU"
	tagIPImage    = "IPIMAGE"
)

// b2u8 serializes a bool as 1 (true) or 0 (false).
func b2u8(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// fieldWriter accumulates tagged field records for one chunk payload.
type fieldWriter struct {
	b []byte
}

// hdr writes a field header (name length, name, data size). Field names
// are compile-time constants; an invalid one is a programming error.
func (w *fieldWriter) hdr(name string, size int) {
	if len(name) == 0 || len(name) > 255 {
		panic("savestate: invalid field name: " + name)
	}
	w.b = append(w.b, uint8(len(name)))
	w.b = append(w.b, name...)
	w.b = binary.BigEndian.AppendUint32(w.b, uint32(size))
}

func (w *fieldWriter) raw(name string, data []byte) {
	w.hdr(name, len(data))
	w.b = append(w.b, data...)
}

func (w *fieldWriter) u8(name string, v uint8) {
	w.hdr(name, 1)
	w.b = append(w.b, v)
}

func (w *fieldWriter) flag(name string, v bool) {
	w.u8(name, b2u8(v))
}

func (w *fieldWriter) u16(name string, v uint16) {
	w.hdr(name, 2)
	w.b = binary.BigEndian.AppendUint16(w.b, v)
}

func (w *fieldWriter) u32(name string, v uint32) {
	w.hdr(name, 4)
	w.b = binary.BigEndian.AppendUint32(w.b, v)
}

func (w *fieldWriter) u64(name string, v uint64) {
	w.hdr(name, 8)
	w.b = binary.BigEndian.AppendUint64(w.b, v)
}

func (w *fieldWriter) i16(name string, v int16) { w.u16(name, uint16(v)) }
func (w *fieldWriter) i32(name string, v int32) { w.u32(name, uint32(v)) }
func (w *fieldWriter) i64(name string, v int64) { w.u64(name, uint64(v)) }

func (w *fieldWriter) u16s(name string, vs []uint16) {
	w.hdr(name, len(vs)*2)
	for _, v := range vs {
		w.b = binary.BigEndian.AppendUint16(w.b, v)
	}
}

func (w *fieldWriter) u32s(name string, vs []uint32) {
	w.hdr(name, len(vs)*4)
	for _, v := range vs {
		w.b = binary.BigEndian.AppendUint32(w.b, v)
	}
}

func (w *fieldWriter) u64s(name string, vs []uint64) {
	w.hdr(name, len(vs)*8)
	for _, v := range vs {
		w.b = binary.BigEndian.AppendUint64(w.b, v)
	}
}

func (w *fieldWriter) i16s(name string, vs []int16) {
	w.hdr(name, len(vs)*2)
	for _, v := range vs {
		w.b = binary.BigEndian.AppendUint16(w.b, uint16(v))
	}
}

func (w *fieldWriter) i32s(name string, vs []int32) {
	w.hdr(name, len(vs)*4)
	for _, v := range vs {
		w.b = binary.BigEndian.AppendUint32(w.b, uint32(v))
	}
}

func (w *fieldWriter) i64s(name string, vs []int64) {
	w.hdr(name, len(vs)*8)
	for _, v := range vs {
		w.b = binary.BigEndian.AppendUint64(w.b, uint64(v))
	}
}

func (w *fieldWriter) i8s(name string, vs []int8) {
	w.hdr(name, len(vs))
	for _, v := range vs {
		w.b = append(w.b, byte(v))
	}
}

func (w *fieldWriter) flags(name string, vs []bool) {
	w.hdr(name, len(vs))
	for _, v := range vs {
		w.b = append(w.b, b2u8(v))
	}
}

// ddaEdge and gouraudStepper are embedded in the VDP1 resume structs;
// their fields are written with a caller-supplied name prefix.
func (w *fieldWriter) ddaEdge(prefix string, e *ddaEdge) {
	w.i64(prefix+".x", e.x)
	w.i64(prefix+".y", e.y)
	w.i64(prefix+".dx", e.dx)
	w.i64(prefix+".dy", e.dy)
}

func (w *fieldWriter) gouraud(prefix string, g *gouraudStepper) {
	w.i64(prefix+".r", int64(g.r))
	w.i64(prefix+".g", int64(g.g))
	w.i64(prefix+".b", int64(g.b))
	w.i64(prefix+".dr", int64(g.dr))
	w.i64(prefix+".dg", int64(g.dg))
	w.i64(prefix+".db", int64(g.db))
}

func (w *fieldWriter) vdp1Cmd(prefix string, c *vdp1Command) {
	w.u16(prefix+".ctrl", c.ctrl)
	w.u16(prefix+".link", c.link)
	w.u16(prefix+".pmod", c.pmod)
	w.u16(prefix+".colr", c.colr)
	w.u16(prefix+".srca", c.srca)
	w.u16(prefix+".size", c.size)
	w.i16(prefix+".xa", c.xa)
	w.i16(prefix+".ya", c.ya)
	w.i16(prefix+".xb", c.xb)
	w.i16(prefix+".yb", c.yb)
	w.i16(prefix+".xc", c.xc)
	w.i16(prefix+".yc", c.yc)
	w.i16(prefix+".xd", c.xd)
	w.i16(prefix+".yd", c.yd)
	w.u16(prefix+".grda", c.grda)
}

// appendChunk frames one chunk: fixed 16-byte zero-filled ASCII tag,
// u32 payload length, payload. Tags are compile-time constants; an
// invalid one is a programming error.
func appendChunk(out []byte, tag string, payload []byte) []byte {
	if len(tag) == 0 || len(tag) >= stateTagLen {
		panic("savestate: invalid chunk tag: " + tag)
	}
	var t [stateTagLen]byte
	copy(t[:], tag)
	out = append(out, t[:]...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	return append(out, payload...)
}

// chunkAssembler frames chunks directly into one shared buffer: tag,
// a length placeholder backfilled after the builder runs, then the
// builder's field records appended in place. No per-chunk intermediate
// allocation - Serialize reuses the underlying buffer across calls.
type chunkAssembler struct {
	b []byte
}

func (a *chunkAssembler) chunk(tag string, build func(*fieldWriter)) {
	if len(tag) == 0 || len(tag) >= stateTagLen {
		panic("savestate: invalid chunk tag: " + tag)
	}
	var t [stateTagLen]byte
	copy(t[:], tag)
	a.b = append(a.b, t[:]...)
	lenOff := len(a.b)
	a.b = append(a.b, 0, 0, 0, 0)
	w := fieldWriter{b: a.b}
	build(&w)
	a.b = w.b
	binary.BigEndian.PutUint32(a.b[lenOff:], uint32(len(a.b)-lenOff-4))
}

// memChunk emits a memory region as a chunk with a single "data" field,
// keeping the reader uniform across memory and state chunks.
func (a *chunkAssembler) memChunk(tag string, mem []byte) {
	a.chunk(tag, func(w *fieldWriter) { w.raw("data", mem) })
}

// buildSH2Core serializes everything from the CPU snapshot except the
// cache arrays, which go in the per-CPU cache chunk (buildSH2Cache).
// The snapshot is taken once per CPU in buildStateBody and shared by
// both builders.
func buildSH2Core(w *fieldWriter, st *sh2.State) {
	w.u32s("R", st.Reg.R[:])
	w.u32("PC", st.Reg.PC)
	w.u32("PR", st.Reg.PR)
	w.u32("SR", st.Reg.SR)
	w.u32("GBR", st.Reg.GBR)
	w.u32("VBR", st.Reg.VBR)
	w.u32("MACH", st.Reg.MACH)
	w.u32("MACL", st.Reg.MACL)
	w.u64("cycles", st.Cycles)
	w.flag("halted", st.Halted)
	w.u32("irl", st.IRL)

	w.u32("prevPC", st.Exec.PrevPC)
	w.u32("delayPC", st.Exec.DelayPC)
	w.flag("inDelay", st.Exec.InDelay)
	w.flag("nmiPending", st.Exec.NMIPending)
	w.flag("nmiReq", st.Exec.NMIReq)
	w.flag("intInhibit", st.Exec.IntInhibit)
	w.u8("lastLoadReg", st.Exec.LastLoadReg)
	w.u64("multiplierBusyUntil", st.Exec.MultiplierBusyUntil)
	w.u64("nextPeripheralEvent", st.Exec.NextPeripheralEvent)
	w.u32("fetchLineAddr", st.Exec.FetchLineAddr)
	w.i64("fetchLineWay", int64(st.Exec.FetchLineWay))
	w.u32("fetchLineOff", st.Exec.FetchLineOff)
	w.u8("sbycr", st.Exec.SBYCR)
	w.u16("bcr1", st.Exec.BCR1)
	w.u8("ccr", st.Cache.CCR)

	w.u16("intc.ipra", st.INTC.IPRA)
	w.u16("intc.iprb", st.INTC.IPRB)
	w.u16("intc.vcra", st.INTC.VCRA)
	w.u16("intc.vcrb", st.INTC.VCRB)
	w.u16("intc.vcrc", st.INTC.VCRC)
	w.u16("intc.vcrd", st.INTC.VCRD)
	w.u16("intc.icr", st.INTC.ICR)
	w.u16("intc.vcrwdt", st.INTC.VCRWDT)
	w.u16("intc.pending", st.INTC.Pending)

	w.u16("frt.frc", st.FRT.FRC)
	w.u16("frt.ocra", st.FRT.OCRA)
	w.u16("frt.ocrb", st.FRT.OCRB)
	w.u16("frt.icr", st.FRT.ICR)
	w.u8("frt.tier", st.FRT.TIER)
	w.u8("frt.ftcsr", st.FRT.FTCSR)
	w.u8("frt.readFlags", st.FRT.ReadFlags)
	w.u8("frt.tcr", st.FRT.TCR)
	w.u8("frt.tocr", st.FRT.TOCR)
	w.u16("frt.prescaler", st.FRT.Prescaler)
	w.u8("frt.temp", st.FRT.Temp)
	w.u64("frt.lastSync", st.FRT.LastSync)
	w.u64("frt.nextEvent", st.FRT.NextEvent)

	w.u32("divu.dvsr", st.DIVU.DVSR)
	w.u32("divu.dvdnt", st.DIVU.DVDNT)
	w.u32("divu.dvdnth", st.DIVU.DVDNTH)
	w.u32("divu.dvdntl", st.DIVU.DVDNTL)
	w.u32("divu.dvcr", st.DIVU.DVCR)
	w.u32("divu.vcrdiv", st.DIVU.VCRDIV)

	sar := [2]uint32{st.DMAC.Ch[0].SAR, st.DMAC.Ch[1].SAR}
	dar := [2]uint32{st.DMAC.Ch[0].DAR, st.DMAC.Ch[1].DAR}
	tcr := [2]uint32{st.DMAC.Ch[0].TCR, st.DMAC.Ch[1].TCR}
	chcr := [2]uint32{st.DMAC.Ch[0].CHCR, st.DMAC.Ch[1].CHCR}
	vcrdma := [2]uint32{st.DMAC.Ch[0].VCRDMA, st.DMAC.Ch[1].VCRDMA}
	w.u32s("dmac.sar", sar[:])
	w.u32s("dmac.dar", dar[:])
	w.u32s("dmac.tcr", tcr[:])
	w.u32s("dmac.chcr", chcr[:])
	w.u32s("dmac.vcrdma", vcrdma[:])
	w.u16("dmac.dmaor", st.DMAC.DMAOR)
	w.raw("dmac.drcr", st.DMAC.DRCR[:])
	w.i64("dmac.nextCh", int64(st.DMAC.NextCh))
	w.i64s("dmac.completesAt", []int64{int64(st.DMAC.CompletesAt[0]), int64(st.DMAC.CompletesAt[1])})

	w.u8("wdt.wtcsr", st.WDT.WTCSR)
	w.u8("wdt.wtcnt", st.WDT.WTCNT)
	w.u8("wdt.rstcsr", st.WDT.RSTCSR)
	w.u32("wdt.prescaler", st.WDT.Prescaler)
	w.u64("wdt.lastSync", st.WDT.LastSync)
	w.u64("wdt.nextEvent", st.WDT.NextEvent)
}

// buildSH2Cache serializes the four cache arrays. They are one unit:
// data without tags, valid bits, and LRU state is an incoherent cache.
// Tag and valid are flattened way-major (way*64 + entry).
func buildSH2Cache(w *fieldWriter, st *sh2.State) {
	w.raw("data", st.Cache.Data[:])
	var tag [4 * 64]uint32
	var valid [4 * 64]bool
	for way := range st.Cache.Tag {
		copy(tag[way*64:], st.Cache.Tag[way][:])
		copy(valid[way*64:], st.Cache.Valid[way][:])
	}
	w.u32s("tag", tag[:])
	w.flags("valid", valid[:])
	w.raw("lru", st.Cache.LRU[:])
}

func buildSCUCore(w *fieldWriter, s *SCU) {
	w.u32s("dmaR", s.dmaR[:])
	w.u32s("dmaW", s.dmaW[:])
	w.u32s("dmaC", s.dmaC[:])
	w.u32s("dmaAD", s.dmaAD[:])
	w.u32s("dmaEN", s.dmaEN[:])
	w.u32s("dmaMD", s.dmaMD[:])
	w.u32("dstp", s.dstp)
	w.flags("dmaPending", s.dmaPending[:])
	delays := [3]int64{int64(s.dmaDelay[0]), int64(s.dmaDelay[1]), int64(s.dmaDelay[2])}
	w.i64s("dmaDelay", delays[:])
	w.u32("dspCycleCarry", s.dspCycleCarry)
	w.u32("t0c", s.t0c)
	w.u32("t1s", s.t1s)
	w.u32("t1md", s.t1md)
	w.u32("t0cnt", s.t0cnt)
	w.i64("t1Delay", int64(s.t1Delay))
	w.flag("t1Gate", s.t1Gate)
	w.u32("ims", s.ims)
	w.u32("ist", s.ist)
	w.u32("aiak", s.aiak)
	w.u32("asr0", s.asr0)
	w.u32("asr1", s.asr1)
	w.u32("aref", s.aref)
	w.u32("rsel", s.rsel)
	w.i64("pendingBit", int64(s.pendingBit))
	w.flag("irlWithheld", s.irlWithheld)
	w.u32("reqPending", s.reqPending)
}

func buildSCUDSP(w *fieldWriter, d *scuDSP) {
	w.u32s("prog", d.prog[:])
	data := make([]uint32, 0, 4*64)
	for bank := range d.data {
		data = append(data, d.data[bank][:]...)
	}
	w.u32s("data", data)
	w.u8("pc", d.pc)
	w.u8("top", d.top)
	w.u16("lop", d.lop)
	w.raw("ct", []byte{d.ct[0], d.ct[1], d.ct[2], d.ct[3]})
	w.u16("ach", d.ach)
	w.u32("acl", d.acl)
	w.u16("ph", d.ph)
	w.u32("pl", d.pl)
	w.u32("rx", d.rx)
	w.u32("ry", d.ry)
	w.u32("ra0", d.ra0)
	w.u32("wa0", d.wa0)
	w.flag("flagS", d.flagS)
	w.flag("flagZ", d.flagZ)
	w.flag("flagC", d.flagC)
	w.flag("flagV", d.flagV)
	w.flag("flagEnd", d.flagEnd)
	w.flag("flagT0", d.flagT0)
	w.flag("executing", d.executing)
	w.flag("looping", d.looping)
	w.u32("nextInstr", d.nextInstr)
	w.u32("debt", d.debt)
	w.u8("pdaAddr", d.pdaAddr)
}

func buildSCSPRegs(w *fieldWriter, s *SCSP) {
	w.u16s("regs", s.regs[:])
}

// buildSCSPSlots writes the per-slot runtime state as per-member arrays:
// each field holds all 32 slots' values for one member.
func buildSCSPSlots(w *fieldWriter, s *SCSP) {
	var (
		phase    [32]uint32
		egLevel  [32]int32
		egState  [32]byte
		egStep   [32]int32
		egTarget [32]int32
		active   [32]bool
		finished [32]bool
		loopDir  [32]int8
		output   [32]int16
		lfoPhase [32]byte
		lfoStep  [32]uint16
		lfoNoise [32]uint16
	)
	for i := range s.slots {
		sl := &s.slots[i]
		phase[i] = sl.phase
		egLevel[i] = sl.egLevel
		egState[i] = sl.egState
		egStep[i] = sl.egStep
		egTarget[i] = sl.egTarget
		active[i] = sl.active
		finished[i] = sl.finished
		loopDir[i] = sl.loopDir
		output[i] = sl.output
		lfoPhase[i] = sl.lfoPhase
		lfoStep[i] = sl.lfoStep
		lfoNoise[i] = sl.lfoNoise
	}
	w.u32s("phase", phase[:])
	w.i32s("egLevel", egLevel[:])
	w.raw("egState", egState[:])
	w.i32s("egStep", egStep[:])
	w.i32s("egTarget", egTarget[:])
	w.flags("active", active[:])
	w.flags("finished", finished[:])
	w.i8s("loopDir", loopDir[:])
	w.i16s("output", output[:])
	w.raw("lfoPhase", lfoPhase[:])
	w.u16s("lfoStep", lfoStep[:])
	w.u16s("lfoNoise", lfoNoise[:])
}

func buildSCSPDSP(w *fieldWriter, d *scspDSP) {
	w.u64s("mpro", d.mpro[:])
	w.i32s("temp", d.temp[:])
	w.i32s("mems", d.mems[:])
	w.i32s("mixs", d.mixs[:])
	w.i32s("exts", d.exts[:])
	w.i16s("efreg", d.efreg[:])
	w.i16s("coef", d.coef[:])
	w.u16s("madrs", d.madrs[:])
	w.i32("sftReg", d.sftReg)
	w.u16("frcReg", d.frcReg)
	w.i32("yReg", d.yReg)
	w.u16("adrsReg", d.adrsReg)
	w.u16("mdecCT", d.mdecCT)
	w.i32("inputs", d.inputs)
	w.u8("readPending", d.readPending)
	w.i32("readValue", d.readValue)
	w.flag("writePending", d.writePending)
	w.u16("writeValue", d.writeValue)
	w.u32("rwAddr", d.rwAddr)
	w.i64("lastStep", int64(d.lastStep))
}

func buildSCSPTimers(w *fieldWriter, s *SCSP) {
	w.u16s("timerPrescaler", s.timerPrescaler[:])
	w.raw("timerCounter", s.timerCounter[:])
}

func buildSCSPMisc(w *fieldWriter, s *SCSP) {
	w.flag("inReset", s.inReset)
	w.flag("mainIntActive", s.mainIntActive)
	w.u8("soundIntLevel", s.soundIntLevel)
	w.u32("lfsr", s.lfsr)
	w.i16s("fmHistory", s.fmHistory[:])
	w.i64("fmHistoryCur", int64(s.fmHistoryCur))
	w.i16s("fmDelayer", s.fmDelayer[:])
	w.flag("soundReqPending", s.soundReqPending.Load())
	w.flag("pendingReset", s.pendingReset.Load())
}

func buildM68K(w *fieldWriter, s *SCSP) {
	w.raw("blob", s.M68KSerialize())
}

// buildVDP1Regs stores the actual register and drawing-state fields, not
// the hardware-readback view (no MODR rebuild - MODR is derived).
func buildVDP1Regs(w *fieldWriter, v *VDP1) {
	w.u16("tvmr", v.tvmr)
	w.u16("fbcr", v.fbcr)
	w.u16("ptmr", v.ptmr)
	w.u16("ewdr", v.ewdr)
	w.u16("ewlr", v.ewlr)
	w.u16("ewrr", v.ewrr)
	w.u16("edsr", v.edsr)
	w.u16("lopr", v.lopr)
	w.u16("copr", v.copr)
	w.u16("fbcrPending", v.fbcrPending)
	w.u16("ptmrPending", v.ptmrPending)
	w.u16("ewdrPending", v.ewdrPending)
	w.u16("ewlrPending", v.ewlrPending)
	w.u16("ewrrPending", v.ewrrPending)
	w.i16("localX", v.localX)
	w.i16("localY", v.localY)
	w.i16("sysClipX", v.sysClipX)
	w.i16("sysClipY", v.sysClipY)
	w.i16("userClipX1", v.userClipX1)
	w.i16("userClipY1", v.userClipY1)
	w.i16("userClipX2", v.userClipX2)
	w.i16("userClipY2", v.userClipY2)
	w.flag("drawPending", v.drawPending)
	w.flag("drawEnd", v.drawEnd)
	w.flag("drawActive", v.drawActive)
	w.u32("procAddr", v.procAddr)
	w.u32("procReturnAddr", v.procReturnAddr)
	w.i32("systemCycleDebt", v.systemCycleDebt)
	w.i32("vramWriteStall", v.vramWriteStallCycles.Load())
	w.i64("drawTicked", v.drawTicked.Load())
	w.i64("drawStallCharged", v.drawStallCharged.Load())
	w.flag("vbeLatched", v.vbeLatched)
	w.flag("fbcrWritten", v.fbcrWritten)
	w.flag("eraseRequested", v.eraseRequested)
	w.flag("fbSwapped", v.fbSwapped)
	// lateEraseFB, when set, always aliases the current displayFB:
	// every swap site clears it (PerformLateErase) before swapping, so
	// it can never survive a swap or alias drawFB. Stored as a pending
	// flag; restore re-aliases displayFB.
	w.flag("lateErasePending", v.lateEraseFB != nil)
}

func buildVDP1Resume(w *fieldWriter, v *VDP1) {
	w.i64("cmdPhase", int64(v.cmdPhase))
	w.vdp1Cmd("snap", &v.cmdSnapshot)

	sr := &v.spriteResume
	w.u32("sprite.charAddr", sr.charAddr)
	w.i64("sprite.charW", int64(sr.charW))
	w.i64("sprite.charH", int64(sr.charH))
	w.u16("sprite.colorMode", sr.colorMode)
	w.u16("sprite.cc", sr.cc)
	w.flag("sprite.ecdOff", sr.ecdOff)
	w.flag("sprite.spdOn", sr.spdOn)
	w.flag("sprite.msbOn", sr.msbOn)
	w.flag("sprite.mesh", sr.mesh)
	w.u16("sprite.userClip", sr.userClip)
	w.flag("sprite.flipH", sr.flipH)
	w.flag("sprite.flipV", sr.flipV)
	w.i64("sprite.drawX", int64(sr.drawX))
	w.i64("sprite.drawY", int64(sr.drawY))
	w.i64("sprite.clipX", int64(sr.clipX))
	w.i64("sprite.clipY", int64(sr.clipY))
	w.u16s("sprite.gt", sr.gt[:])
	w.flag("sprite.isScaled", sr.isScaled)
	w.i64("sprite.destW", int64(sr.destW))
	w.i64("sprite.destH", int64(sr.destH))
	w.i64("sprite.dstX1", int64(sr.dstX1))
	w.i64("sprite.dstY1", int64(sr.dstY1))
	w.flag("sprite.effFlipH", sr.effFlipH)
	w.flag("sprite.effFlipV", sr.effFlipV)
	w.flag("sprite.hssShrinkX", sr.hssShrinkX)
	w.flag("sprite.hssOddParity", sr.hssOddParity)
	w.flag("sprite.hssEcdOff", sr.hssEcdOff)
	w.i64("sprite.outerIdx", int64(sr.outerIdx))
	w.i64("sprite.innerIdx", int64(sr.innerIdx))
	w.i64("sprite.endCodeCount", int64(sr.endCodeCount))
	w.i64("sprite.prevSrcX", int64(sr.prevSrcX))
	w.i64("sprite.srcReadX", int64(sr.srcReadX))

	dr := &v.distortedResume
	w.u32("dist.charAddr", dr.charAddr)
	w.i64("dist.charW", int64(dr.charW))
	w.i64("dist.charH", int64(dr.charH))
	w.u16("dist.colorMode", dr.colorMode)
	w.u16("dist.cc", dr.cc)
	w.flag("dist.ecdOff", dr.ecdOff)
	w.flag("dist.spdOn", dr.spdOn)
	w.flag("dist.msbOn", dr.msbOn)
	w.flag("dist.mesh", dr.mesh)
	w.u16("dist.userClip", dr.userClip)
	w.flag("dist.hss", dr.hss)
	w.flag("dist.hssOdd", dr.hssOdd)
	w.flag("dist.hssShrinkU", dr.hssShrinkU)
	w.flag("dist.flipH", dr.flipH)
	w.flag("dist.flipV", dr.flipV)
	w.i64("dist.bboxMinX", int64(dr.bboxMinX))
	w.i64("dist.bboxMinY", int64(dr.bboxMinY))
	w.i64("dist.bboxMaxX", int64(dr.bboxMaxX))
	w.i64("dist.bboxMaxY", int64(dr.bboxMaxY))
	w.i64("dist.clipX", int64(dr.clipX))
	w.i64("dist.clipY", int64(dr.clipY))
	w.i64("dist.ax", int64(dr.ax))
	w.i64("dist.ay", int64(dr.ay))
	w.i64("dist.bx", int64(dr.bx))
	w.i64("dist.by", int64(dr.by))
	w.i64("dist.cx", int64(dr.cx))
	w.i64("dist.cy", int64(dr.cy))
	w.i64("dist.dx", int64(dr.dx))
	w.i64("dist.dy", int64(dr.dy))
	w.i64("dist.dmax", int64(dr.dmax))
	w.i64("dist.rowStride", int64(dr.rowStride))
	w.flag("dist.bpp8", dr.bpp8)
	w.flag("dist.simpleMode", dr.simpleMode)
	w.u16s("dist.gt", dr.gt[:])
	w.flag("dist.checkEcd", dr.checkEcd)
	w.u16("dist.colr", dr.colr)
	w.i64("dist.uStart", int64(dr.uStart))
	w.i64("dist.uEnd", int64(dr.uEnd))
	w.i64("dist.dvFP", int64(dr.dvFP))
	w.ddaEdge("dist.leftEdge", &dr.leftEdge)
	w.ddaEdge("dist.rightEdge", &dr.rightEdge)
	w.gouraud("dist.gsOuterLeft", &dr.gsOuterLeft)
	w.gouraud("dist.gsOuterRight", &dr.gsOuterRight)
	w.i64("dist.vFP", int64(dr.vFP))
	w.i64("dist.outerI", int64(dr.outerI))
	w.i64("dist.innerJ", int64(dr.innerJ))
	w.i64("dist.curLx", int64(dr.curLx))
	w.i64("dist.curLy", int64(dr.curLy))
	w.i64("dist.curRx", int64(dr.curRx))
	w.i64("dist.curRy", int64(dr.curRy))
	w.u32("dist.rowBase", dr.rowBase)
	w.i64("dist.lineDx", int64(dr.lineDx))
	w.i64("dist.lineDy", int64(dr.lineDy))
	w.i64("dist.lineLen", int64(dr.lineLen))
	w.i64("dist.jEnd", int64(dr.jEnd))
	w.i64("dist.pxFP", dr.pxFP)
	w.i64("dist.pyFP", dr.pyFP)
	w.i64("dist.dpxFP", dr.dpxFP)
	w.i64("dist.dpyFP", dr.dpyFP)
	w.i64("dist.uFP", int64(dr.uFP))
	w.i64("dist.duFP", int64(dr.duFP))
	w.i64("dist.prevPx", int64(dr.prevPx))
	w.i64("dist.prevPy", int64(dr.prevPy))
	w.i64("dist.endCodeCount", int64(dr.endCodeCount))
	w.i64("dist.srcReadU", int64(dr.srcReadU))
	w.gouraud("dist.gsLine", &dr.gsLine)

	pr := &v.polygonResume
	w.u16("poly.color", pr.color)
	w.u16("poly.cc", pr.cc)
	w.flag("poly.msbOn", pr.msbOn)
	w.flag("poly.mesh", pr.mesh)
	w.u16("poly.userClip", pr.userClip)
	w.i64("poly.clipX", int64(pr.clipX))
	w.i64("poly.clipY", int64(pr.clipY))
	w.flag("poly.simpleMode", pr.simpleMode)
	w.u16s("poly.gt", pr.gt[:])
	w.i64("poly.ax", int64(pr.ax))
	w.i64("poly.ay", int64(pr.ay))
	w.i64("poly.bx", int64(pr.bx))
	w.i64("poly.by", int64(pr.by))
	w.i64("poly.cx", int64(pr.cx))
	w.i64("poly.cy", int64(pr.cy))
	w.i64("poly.dx", int64(pr.dx))
	w.i64("poly.dy", int64(pr.dy))
	w.i64("poly.dmax", int64(pr.dmax))
	w.ddaEdge("poly.leftEdge", &pr.leftEdge)
	w.ddaEdge("poly.rightEdge", &pr.rightEdge)
	w.gouraud("poly.gsOuterLeft", &pr.gsOuterLeft)
	w.gouraud("poly.gsOuterRight", &pr.gsOuterRight)
	w.gouraud("poly.gsLine", &pr.gsLine)
	w.i64("poly.outerI", int64(pr.outerI))
	w.i64("poly.innerJ", int64(pr.innerJ))
	w.i64("poly.curLx", int64(pr.curLx))
	w.i64("poly.curLy", int64(pr.curLy))
	w.i64("poly.curRx", int64(pr.curRx))
	w.i64("poly.curRy", int64(pr.curRy))
	w.i64("poly.lineDx", int64(pr.lineDx))
	w.i64("poly.lineDy", int64(pr.lineDy))
	w.i64("poly.lineLen", int64(pr.lineLen))
	w.i64("poly.jEnd", int64(pr.jEnd))
	w.i64("poly.pxFP", pr.pxFP)
	w.i64("poly.pyFP", pr.pyFP)
	w.i64("poly.dpxFP", pr.dpxFP)
	w.i64("poly.dpyFP", pr.dpyFP)
	w.i64("poly.prevPx", int64(pr.prevPx))
	w.i64("poly.prevPy", int64(pr.prevPy))

	lr := &v.lineResume
	w.u16("line.color", lr.color)
	w.u16("line.cc", lr.cc)
	w.flag("line.msbOn", lr.msbOn)
	w.flag("line.mesh", lr.mesh)
	w.u16("line.userClip", lr.userClip)
	w.i64("line.clipX", int64(lr.clipX))
	w.i64("line.clipY", int64(lr.clipY))
	w.u16("line.gStart", lr.gStart)
	w.u16("line.gEnd", lr.gEnd)
	w.i64("line.x", int64(lr.x))
	w.i64("line.y", int64(lr.y))
	w.i64("line.dx", int64(lr.dx))
	w.i64("line.dy", int64(lr.dy))
	w.i64("line.sx", int64(lr.sx))
	w.i64("line.sy", int64(lr.sy))
	w.i64("line.err", int64(lr.err))
	w.i64("line.iter", int64(lr.iter))
	w.i64("line.iterMax", int64(lr.iterMax))
	w.flag("line.xMajor", lr.xMajor)
	w.i64("line.polylineLeg", int64(lr.polylineLeg))
	w.i64("line.legAx", int64(lr.legAx))
	w.i64("line.legAy", int64(lr.legAy))
	w.i64("line.legBx", int64(lr.legBx))
	w.i64("line.legBy", int64(lr.legBy))
	w.i64("line.legCx", int64(lr.legCx))
	w.i64("line.legCy", int64(lr.legCy))
	w.i64("line.legDx", int64(lr.legDx))
	w.i64("line.legDy", int64(lr.legDy))
	w.u16s("line.polylineGt", lr.polylineGt[:])
}

func buildVDP2State(w *fieldWriter, v *VDP2) {
	w.u16s("regs", v.regs[:])
	w.u16("vLine", v.vLine)
	w.u32("lineCycle", v.lineCycle)
	w.flag("oddField", v.oddField)
	w.u32("latchedLineCycle", v.latchedLineCycle)
	w.u16("latchedVLine", v.latchedVLine)
	w.flag("latchedHiRes", v.latchedHiRes)
	w.u8("latchedInterlace", v.latchedInterlace)
	w.flag("latchedOddField", v.latchedOddField)
	w.flag("exltfg", v.exltfg)
	w.flag("pal", v.pal)
	scynFrame := [4]int64{int64(v.scynFrame[0]), int64(v.scynFrame[1]), int64(v.scynFrame[2]), int64(v.scynFrame[3])}
	w.i64s("scynFrame", scynFrame[:])
}

func buildSMPC(w *fieldWriter, s *SMPC) {
	w.u8("comreg", s.comreg)
	w.u8("sr", s.sr)
	w.u8("sf", s.sf)
	w.raw("ireg", s.ireg[:])
	w.raw("cmdIREG", s.cmdIREG[:])
	w.raw("oreg", s.oreg[:])
	w.u8("pdr1", s.pdr1)
	w.u8("pdr2", s.pdr2)
	w.u8("ddr1", s.ddr1)
	w.u8("ddr2", s.ddr2)
	w.u8("iosel", s.iosel)
	w.u8("exle", s.exle)
	w.raw("rtc", s.rtc[:])
	w.i64("rtcBaseTime", s.rtcBaseTime.UnixNano())
	w.u64("rtcFrames", s.rtcFrames)
	w.raw("smem", s.smem[:])
	w.u8("areaCode", s.areaCode)
	w.flag("intbackActive", s.intbackActive)
	w.u8("intbackP1MD", s.intbackP1MD)
	w.u8("intbackP2MD", s.intbackP2MD)
	w.flag("sshEnabled", s.sshEnabled)
	w.flag("soundEnabled", s.soundEnabled)
	w.flag("cdEnabled", s.cdEnabled)
	w.flag("resetEnabled", s.resetEnabled)
	w.flag("dotsel", s.dotsel)
	w.i64("cmdDelay", int64(s.cmdDelay))
	// pendingOps is variable-length: u32 count, then per op cont u8,
	// val u8, ireg[7].
	pb := binary.BigEndian.AppendUint32(nil, uint32(len(s.pendingOps)))
	for _, op := range s.pendingOps {
		pb = append(pb, b2u8(op.cont), op.val)
		pb = append(pb, op.ireg[:]...)
	}
	w.raw("pendingOps", pb)
}

func buildCDState(w *fieldWriter, cb *CDBlock) {
	w.u16("hirqReq", cb.hirqReq)
	w.u16("hirqMask", cb.hirqMask)
	w.u16s("cmd", cb.cmd[:])
	w.u16s("res", cb.res[:])
	w.u16s("dataBuf", cb.dataBuf)
	w.i64("dataPos", int64(cb.dataPos))
	w.u8("dataTransferType", cb.dataTransferType)
	// delPart is a pointer into partitions[24]; stored as the index,
	// -1 when nil.
	delIdx := int64(-1)
	for i := range cb.partitions {
		if cb.delPart == &cb.partitions[i] {
			delIdx = int64(i)
			break
		}
	}
	w.i64("delPart", delIdx)
	w.i64("delStart", int64(cb.delStart))
	w.i64("delCount", int64(cb.delCount))
	w.u8("status", cb.status)
	w.u8("cdDeviceFilter", cb.cdDeviceFilter)
	w.u8("lastBufferDest", cb.lastBufferDest)
	w.u32("calcSize", cb.calcSize)
	w.u32("playFAD", cb.playFAD)
	w.u32("startFAD", cb.startFAD)
	w.u32("endFAD", cb.endFAD)
	w.u8("curTrack", cb.curTrack)
	w.u8("repeatCount", cb.repeatCount)
	w.flag("playing", cb.playing)
	w.flag("fileRead", cb.fileRead)
	w.i64("getSectorLen", int64(cb.getSectorLen))
	w.i64("putSectorLen", int64(cb.putSectorLen))
	w.raw("putBuf", cb.putBuf)
	w.u8("putBufNum", cb.putBufNum)
	w.i64("putSectorsRemaining", int64(cb.putSectorsRemaining))
	w.u32("putWords", cb.putWords)
	w.flag("authenticated", cb.authenticated)
	w.flag("mpegCardAuth", cb.mpegCardAuth.Load())
	w.flag("peri", cb.peri)
	w.flag("transferActive", cb.transferActive)
	w.flag("resultsRead", cb.resultsRead)
	w.flag("initialized", cb.initialized)
	w.u8("cdSpeed", cb.cdSpeed)
	w.i64("bootDelay", int64(cb.bootDelay))
	w.i64("sectorAccum", int64(cb.sectorAccum))
	w.i64("seekAccum", int64(cb.seekAccum))
	w.i64("cmdDelay", int64(cb.cmdDelay))
	w.flag("seeking", cb.seeking)
	w.u32("seekFAD", cb.seekFAD)
	w.u8("seekTarget", cb.seekTarget)
	w.i64("busyDelay", int64(cb.busyDelay))
	w.u8("pendingStatus", cb.pendingStatus)
	w.i64("scdqCounter", int64(cb.scdqCounter))
	w.i64("periCounter", int64(cb.periCounter))
	w.flag("bufferStall", cb.bufferStall)
}

func buildCDBuffers(w *fieldWriter, cb *CDBlock) {
	var (
		mode      [24]byte
		fadStart  [24]uint32
		fadRange  [24]uint32
		fileNum   [24]byte
		chanNum   [24]byte
		smmask    [24]byte
		smval     [24]byte
		cimask    [24]byte
		cival     [24]byte
		trueConn  [24]byte
		falseConn [24]byte
	)
	for i := range cb.filters {
		f := &cb.filters[i]
		mode[i] = f.mode
		fadStart[i] = f.fadStart
		fadRange[i] = f.fadRange
		fileNum[i] = f.fileNum
		chanNum[i] = f.chanNum
		smmask[i] = f.smmask
		smval[i] = f.smval
		cimask[i] = f.cimask
		cival[i] = f.cival
		trueConn[i] = f.trueConn
		falseConn[i] = f.falseConn
	}
	w.raw("filter.mode", mode[:])
	w.u32s("filter.fadStart", fadStart[:])
	w.u32s("filter.fadRange", fadRange[:])
	w.raw("filter.fileNum", fileNum[:])
	w.raw("filter.chanNum", chanNum[:])
	w.raw("filter.smmask", smmask[:])
	w.raw("filter.smval", smval[:])
	w.raw("filter.cimask", cimask[:])
	w.raw("filter.cival", cival[:])
	w.raw("filter.trueConn", trueConn[:])
	w.raw("filter.falseConn", falseConn[:])

	// partitions is variable-length: per partition u32 sector count,
	// then per sector u32 data length + data, size/userOffset/userSize
	// i64, fad u32, fileNum/chanNum/submode/codinfo u8, isMode2 u8.
	var pb []byte
	for i := range cb.partitions {
		secs := cb.partitions[i].sectors
		pb = binary.BigEndian.AppendUint32(pb, uint32(len(secs)))
		for j := range secs {
			sec := &secs[j]
			pb = binary.BigEndian.AppendUint32(pb, uint32(len(sec.data)))
			pb = append(pb, sec.data...)
			pb = binary.BigEndian.AppendUint64(pb, uint64(int64(sec.size)))
			pb = binary.BigEndian.AppendUint64(pb, uint64(int64(sec.userOffset)))
			pb = binary.BigEndian.AppendUint64(pb, uint64(int64(sec.userSize)))
			pb = binary.BigEndian.AppendUint32(pb, sec.fad)
			pb = append(pb, sec.fileNum, sec.chanNum, sec.submode, sec.codinfo, b2u8(sec.isMode2))
		}
	}
	w.raw("partitions", pb)
}

func buildCDAudioQ(w *fieldWriter, cb *CDBlock) {
	w.i16s("audioQueue", cb.audioQueue[:])
	w.i64("audioHead", int64(cb.audioHead))
	w.i64("audioTail", int64(cb.audioTail))
	w.i64("audioCount", int64(cb.audioCount))
}

// buildCDMpeg serializes the MPEG card subsystem: the command-visible
// cdMpeg state, the playback engine's own fields, and the decoded-frame
// triple-buffer latch. The third-party decoder pipeline objects
// (mpeg.Buffer/Demux/Video/Audio) have no serialization support and are
// not captured; restore semantics for an in-flight playback are decided
// at the Deserialize side (resync from the partitions, else stop).
// The latch is required state, not display scratch: still-picture modes
// decode once and display indefinitely, so a restored still would
// otherwise stay black.
func buildCDMpeg(w *fieldWriter, cb *CDBlock) {
	m := &cb.mpeg
	w.flag("active", m.active)
	w.u32("intStatus", m.intStatus)
	w.u32("intMask", m.intMask)
	w.u8("movieMode", m.movieMode)
	w.u8("decodeTiming", m.decodeTiming)
	w.u8("outDest", m.outDest)
	w.u8("scanMode", m.scanMode)
	w.u8("playMode", m.playMode)
	w.u8("tmodA", m.tmodA)
	w.u8("tmodV", m.tmodV)
	w.u8("decV", m.decV)
	w.u8("audioMute", m.audioMute)
	w.flag("vidPaused", m.vidPaused)
	w.flag("vidFrozen", m.vidFrozen)

	// conn/stream records flattened [selector*2 + layer].
	var connMode, connLayer, connBuf, strMode, strStm, strChn [4]byte
	for sel := 0; sel < 2; sel++ {
		for layer := 0; layer < 2; layer++ {
			i := sel*2 + layer
			connMode[i] = m.conn[sel][layer].mode
			connLayer[i] = m.conn[sel][layer].layer
			connBuf[i] = m.conn[sel][layer].bufNum
			strMode[i] = m.stream[sel][layer].mode
			strStm[i] = m.stream[sel][layer].stmNum
			strChn[i] = m.stream[sel][layer].chNum
		}
	}
	w.raw("conn.mode", connMode[:])
	w.raw("conn.layer", connLayer[:])
	w.raw("conn.bufNum", connBuf[:])
	w.raw("stream.mode", strMode[:])
	w.raw("stream.stmNum", strStm[:])
	w.raw("stream.chNum", strChn[:])

	w.u8("dispSwitch", m.dispSwitch)
	w.u8("dispBank", m.dispBank)
	var win [8 * 3]uint16
	for i := range m.window {
		copy(win[i*3:], m.window[i][:])
	}
	w.u16s("window", win[:])
	w.u16("borderColor", m.borderColor)
	w.u16("fade", m.fade)
	w.u16("videoEffect", m.videoEffect)
	var lsi [2 * 128]uint16
	copy(lsi[:128], m.lsi[0][:])
	copy(lsi[128:], m.lsi[1][:])
	w.u16s("lsi", lsi[:])
	w.u16("picW", m.picW)
	w.u16("picH", m.picH)
	w.u8("vidState", m.vidState)
	w.u8("audState", m.audState)
	w.u16("vidStatus", m.vidStatus)
	w.u8("audStatus", m.audStatus)
	w.u16("vsyncCounter", m.vsyncCounter)
	w.i64("vsyncAccum", int64(m.vsyncAccum))

	// Playback engine. The layer/pacing fields are zero when play is
	// nil and ignored on restore with playActive false.
	w.flag("playActive", m.play != nil)
	var (
		fed                [2]int64
		phase              [2]byte
		esScan             [2]int64
		packSkip, packCode [2]int64
		packLen, packLenN  [2]int64
		psEnded, esEnd     [2]bool
		esProbed, esDirect [2]bool
	)
	var pictAccum, pictCycles, hostSteps, vidHoldCyc int64
	var firstVidPTS, firstAudPTS uint64
	var audStarted, vidStarted bool
	var psTails, audEsTail []byte
	audResume := math.Float64bits(-1)
	if p := m.play; p != nil {
		for i := range p.layers {
			l := &p.layers[i]
			fed[i] = int64(l.fed)
			phase[i] = byte(l.phase)
			esScan[i] = int64(l.esScan)
			packSkip[i] = int64(l.packScan.skip)
			packCode[i] = int64(l.packScan.code)
			packLen[i] = int64(l.packScan.length)
			packLenN[i] = int64(l.packScan.lenN)
			psEnded[i] = l.psEnded
			esEnd[i] = l.esEnded
			esProbed[i] = l.esProbed
			esDirect[i] = l.esDirect
			// Unread pack-stream records: the pipeline data the restore
			// re-seeds so playback resumes at the save point rather
			// than the drive position.
			recs := l.psUnreadRecords()
			psTails = binary.BigEndian.AppendUint32(psTails, uint32(len(recs)))
			for _, rec := range recs {
				psTails = binary.BigEndian.AppendUint32(psTails, uint32(len(rec)))
				psTails = append(psTails, rec...)
			}
		}
		// Unread audio elementary stream and the content time at its
		// head; the video ES is not captured (its decoder can only
		// restart at a sequence header, found in the pack tail, and
		// keeping video restart in the PES path preserves the restart
		// PTS capture).
		al := &p.layers[mpegLayerAudio]
		audEsTail = al.es.Bytes()[al.es.Index():]
		audResume = math.Float64bits(p.audResumePTS())
		pictAccum = int64(p.pictAccum)
		pictCycles = int64(p.pictCycles)
		hostSteps = int64(p.hostSteps)
		firstVidPTS = math.Float64bits(p.firstVidPTS)
		firstAudPTS = math.Float64bits(p.firstAudPTS)
		audStarted = p.audStarted
		vidStarted = p.vidStarted
		vidHoldCyc = int64(p.vidHoldCyc)
	} else {
		// Zero record counts for both layers keep the field parseable.
		psTails = binary.BigEndian.AppendUint32(psTails, 0)
		psTails = binary.BigEndian.AppendUint32(psTails, 0)
	}
	w.i64s("layer.fed", fed[:])
	w.raw("layer.phase", phase[:])
	w.i64s("layer.esScan", esScan[:])
	w.i64s("layer.packSkip", packSkip[:])
	w.i64s("layer.packCode", packCode[:])
	w.i64s("layer.packLen", packLen[:])
	w.i64s("layer.packLenN", packLenN[:])
	w.flags("layer.psEnded", psEnded[:])
	w.flags("layer.esEnded", esEnd[:])
	w.flags("layer.esProbed", esProbed[:])
	w.flags("layer.esDirect", esDirect[:])
	w.i64("pictAccum", pictAccum)
	w.i64("pictCycles", pictCycles)
	w.i64("hostSteps", hostSteps)
	w.u64("firstVidPTS", firstVidPTS)
	w.u64("firstAudPTS", firstAudPTS)
	w.flag("audStarted", audStarted)
	w.flag("vidStarted", vidStarted)
	w.i64("vidHoldCyc", vidHoldCyc)
	w.raw("layer.psTail", psTails)
	w.raw("audEsTail", audEsTail)
	w.u64("audResumePTS", audResume)

	// Frame latch: per slot u32 width, u32 height, then width*height
	// pixels (the slot's backing slice can be larger from a previous
	// bigger frame; only the displayed extent is state).
	l := &cb.mpegFrame
	var sb []byte
	for i := range l.slots {
		s := &l.slots[i]
		n := s.w * s.h
		if n > len(s.rgb) {
			n = len(s.rgb)
		}
		sb = binary.BigEndian.AppendUint32(sb, uint32(s.w))
		sb = binary.BigEndian.AppendUint32(sb, uint32(s.h))
		for _, px := range s.rgb[:n] {
			sb = binary.BigEndian.AppendUint32(sb, px)
		}
	}
	w.raw("latch.slots", sb)
	w.u32("latch.mailbox", l.mailbox.Load())
	w.i64("latch.prod", int64(l.prod))
	w.i64("latch.cons", int64(l.cons))
	w.flag("latch.consHas", l.consHas)
	w.flag("latch.dispOn", l.dispOn.Load())
}

func buildBusMisc(w *fieldWriter, b *Bus) {
	w.flag("minitWritten", b.minitWritten)
	w.flag("sinitWritten", b.sinitWritten)
	w.u16("cdDataTRNSCache", b.cdDataTRNSCache)
}

// buildEmu serializes the emulator-level state that persists across
// frame boundaries: the deferred system-reset and CKCHG master-NMI
// latches (consumed at the TOP of the next RunFrame, so a state saved
// after the requesting frame carries them set), and the per-line
// cycle-width convergence carry with its mode-change detection pair
// (persists across same-mode frames; reset only by systemReset).
func buildEmu(w *fieldWriter, e *Emulator) {
	w.flag("pendingSystemReset", e.pendingSystemReset.Load())
	w.flag("pendingMasterNMI", e.pendingMasterNMI.Load())
	w.u64("lineWidthAccum", e.lineWidthAccum)
	w.u32("lastTrueClock", e.lastTrueClock)
	w.u32("lastD", e.lastD)
	w.u64("frameStart", e.frameStart)
}

// buildStateBody assembles every chunk into the uncompressed container
// body, reusing the emulator's scratch buffer across calls. Chunk order
// is fixed but not semantically meaningful; the reader matches by tag.
// The returned slice is only valid until the next buildStateBody call.
func (e *Emulator) buildStateBody() []byte {
	a := chunkAssembler{b: e.stateBody[:0]}
	a.memChunk(tagWRAMH, e.bus.wramH)
	a.memChunk(tagWRAML, e.bus.wramL)
	a.memChunk(tagBackup, e.bus.backup)
	a.memChunk(tagExtRAM, e.bus.extRAM)
	a.memChunk(tagVDP1VRAM, e.vdp1.vram)
	a.memChunk(tagVDP1DrawFB, e.vdp1.drawFB)
	a.memChunk(tagVDP1DispFB, e.vdp1.displayFB)
	a.memChunk(tagVDP2VRAM, e.vdp2.vram)
	a.memChunk(tagVDP2CRAM, e.vdp2.cram)
	a.memChunk(tagSoundRAM, e.scsp.ram)
	mst := e.master.State()
	sst := e.slave.State()
	a.chunk(tagSH2MCore, func(w *fieldWriter) { buildSH2Core(w, &mst) })
	a.chunk(tagSH2MCache, func(w *fieldWriter) { buildSH2Cache(w, &mst) })
	a.chunk(tagSH2SCore, func(w *fieldWriter) { buildSH2Core(w, &sst) })
	a.chunk(tagSH2SCache, func(w *fieldWriter) { buildSH2Cache(w, &sst) })
	a.chunk(tagSCUCore, func(w *fieldWriter) { buildSCUCore(w, e.scu) })
	a.chunk(tagSCUDSP, func(w *fieldWriter) { buildSCUDSP(w, &e.scu.dsp) })
	a.chunk(tagSCSPRegs, func(w *fieldWriter) { buildSCSPRegs(w, e.scsp) })
	a.chunk(tagSCSPSlots, func(w *fieldWriter) { buildSCSPSlots(w, e.scsp) })
	a.chunk(tagSCSPDSP, func(w *fieldWriter) { buildSCSPDSP(w, &e.scsp.dsp) })
	a.chunk(tagSCSPTimers, func(w *fieldWriter) { buildSCSPTimers(w, e.scsp) })
	a.chunk(tagSCSPMisc, func(w *fieldWriter) { buildSCSPMisc(w, e.scsp) })
	a.chunk(tagM68K, func(w *fieldWriter) { buildM68K(w, e.scsp) })
	a.chunk(tagVDP1Regs, func(w *fieldWriter) { buildVDP1Regs(w, e.vdp1) })
	a.chunk(tagVDP1Resume, func(w *fieldWriter) { buildVDP1Resume(w, e.vdp1) })
	a.chunk(tagVDP2State, func(w *fieldWriter) { buildVDP2State(w, e.vdp2) })
	a.chunk(tagSMPC, func(w *fieldWriter) { buildSMPC(w, e.smpc) })
	a.chunk(tagCDState, func(w *fieldWriter) { buildCDState(w, e.cdblock) })
	a.chunk(tagCDBuffers, func(w *fieldWriter) { buildCDBuffers(w, e.cdblock) })
	a.chunk(tagCDAudioQ, func(w *fieldWriter) { buildCDAudioQ(w, e.cdblock) })
	a.chunk(tagCDMpeg, func(w *fieldWriter) { buildCDMpeg(w, e.cdblock) })
	a.chunk(tagBusMisc, func(w *fieldWriter) { buildBusMisc(w, e.bus) })
	a.chunk(tagEmu, func(w *fieldWriter) { buildEmu(w, e) })
	a.memChunk(tagIPImage, e.ipImage)
	e.stateBody = a.b
	return a.b
}

// stateGameID returns the disc identity for the state header: the IP
// header's product number (0x20:0x2A) and disc number (0x38:0x40),
// trimmed, joined by a space. Empty when no disc IP is cached.
func (e *Emulator) stateGameID() string {
	if len(e.ipImage) < 0x40 {
		return ""
	}
	product := strings.Trim(string(e.ipImage[0x20:0x2A]), " \x00")
	disc := strings.Trim(string(e.ipImage[0x38:0x40]), " \x00")
	if disc == "" {
		return product
	}
	if product == "" {
		return disc
	}
	return product + " " + disc
}

// stateSegmentSize is the uncompressed span each body segment covers.
// Segments compress as independent S2 blocks on parallel workers - a
// single stream over the ~12 MB body costs multiple frame windows and
// stutters rewind capture.
const stateSegmentSize = 2 * 1024 * 1024

// stateCompressor holds per-segment compression scratch reused across
// Serialize calls: one encode destination buffer per segment.
type stateCompressor struct {
	dsts [][]byte // MaxEncodedLen-sized destinations
	outs [][]byte // encoded views into dsts, set per call
}

// compressStateBody compresses each stateSegmentSize span of the body
// as an independent S2 block on its own goroutine (see stateSegmentSize
// for why it is segmented rather than a single stream). The blocks
// assemble in order into the payload that follows the header: u32
// segment count, then per segment u32 uncompressed length, u32
// compressed length, S2 block. The assembled payload lives in the
// emulator's scratch and is only valid until the next call.
func (e *Emulator) compressStateBody(body []byte) []byte {
	n := (len(body) + stateSegmentSize - 1) / stateSegmentSize
	for len(e.stateComp.dsts) < n {
		e.stateComp.dsts = append(e.stateComp.dsts, nil)
		e.stateComp.outs = append(e.stateComp.outs, nil)
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			start := i * stateSegmentSize
			end := start + stateSegmentSize
			if end > len(body) {
				end = len(body)
			}
			if need := s2.MaxEncodedLen(end - start); cap(e.stateComp.dsts[i]) < need {
				e.stateComp.dsts[i] = make([]byte, need)
			}
			e.stateComp.outs[i] = s2.Encode(e.stateComp.dsts[i][:cap(e.stateComp.dsts[i])], body[start:end])
		}(i)
	}
	wg.Wait()

	p := e.statePayload[:0]
	p = binary.BigEndian.AppendUint32(p, uint32(n))
	for i := 0; i < n; i++ {
		rawLen := stateSegmentSize
		if i == n-1 {
			rawLen = len(body) - i*stateSegmentSize
		}
		cb := e.stateComp.outs[i]
		p = binary.BigEndian.AppendUint32(p, uint32(rawLen))
		p = binary.BigEndian.AppendUint32(p, uint32(len(cb)))
		p = append(p, cb...)
	}
	e.statePayload = p
	return p
}

// decompressStateBody reverses compressStateBody with strict bounds:
// segment table validated against the payload, cumulative uncompressed
// size capped at stateMaxBody, segments decompressed concurrently.
func decompressStateBody(payload []byte) ([]byte, error) {
	if len(payload) < 4 {
		return nil, errors.New("savestate: segment table truncated")
	}
	n := int(binary.BigEndian.Uint32(payload))
	if n < 1 || n*8+4 > len(payload) {
		return nil, fmt.Errorf("savestate: segment count %d invalid for payload size %d", n, len(payload))
	}
	type segment struct {
		off, rawLen int
		data        []byte
	}
	segs := make([]segment, n)
	pos, total := 4, 0
	for i := 0; i < n; i++ {
		if pos+8 > len(payload) {
			return nil, fmt.Errorf("savestate: segment %d header truncated", i)
		}
		rawLen := int(binary.BigEndian.Uint32(payload[pos:]))
		compLen := int(binary.BigEndian.Uint32(payload[pos+4:]))
		pos += 8
		if compLen > len(payload)-pos {
			return nil, fmt.Errorf("savestate: segment %d compressed length %d overruns payload", i, compLen)
		}
		if total+rawLen > stateMaxBody {
			return nil, errors.New("savestate: decompressed body exceeds size limit")
		}
		segs[i] = segment{off: total, rawLen: rawLen, data: payload[pos : pos+compLen]}
		total += rawLen
		pos += compLen
	}
	if pos != len(payload) {
		return nil, fmt.Errorf("savestate: %d trailing bytes after segments", len(payload)-pos)
	}

	body := make([]byte, total)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range segs {
		go func(i int) {
			defer wg.Done()
			s := &segs[i]
			// Validate the block's declared size against the segment
			// table before decoding, so a mismatched block cannot
			// decode past its span.
			dlen, err := s2.DecodedLen(s.data)
			if err != nil {
				errs[i] = err
				return
			}
			if dlen != s.rawLen {
				errs[i] = fmt.Errorf("block decodes to %d bytes, segment table declares %d", dlen, s.rawLen)
				return
			}
			if _, err := s2.Decode(body[s.off:s.off+s.rawLen], s.data); err != nil {
				errs[i] = err
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("savestate: segment %d: %w", i, err)
		}
	}
	return body, nil
}

// Serialize captures the complete emulator state as a self-contained
// state file: header followed by the compressed chunk body. The
// returned bytes are the on-disk file format; callers write them
// verbatim. Must be called at a frame boundary (between RunFrame
// calls) with the workers parked, so all component state is stable.
// Not safe for concurrent calls: body assembly and compression reuse
// emulator-owned scratch (only the returned bytes are fresh).
//
// Compression is S2 blocks segmented across parallel workers: front
// ends call Serialize at rewind-capture rates (up to every other
// frame), so the capture must fit inside a frame window.
func (e *Emulator) Serialize() ([]byte, error) {
	body := e.buildStateBody()
	payload := e.compressStateBody(body)

	gameID := e.stateGameID()
	out := make([]byte, 0, len(stateMagic)+4+1+len(gameID)+sha256.Size+4+len(payload))
	out = append(out, stateMagic...)
	out = binary.BigEndian.AppendUint32(out, stateVersion)
	out = append(out, uint8(len(gameID)))
	out = append(out, gameID...)
	var biosHash [sha256.Size]byte
	if e.hasBIOS {
		biosHash = sha256.Sum256(e.bus.bios)
	}
	out = append(out, biosHash[:]...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(payload))
	return append(out, payload...), nil
}

// stateMaxBody bounds the decompressed body size so a hostile file
// cannot act as a decompression bomb. The real body is ~12 MB.
const stateMaxBody = 64 * 1024 * 1024

// SerializeSize returns the per-state size figure front ends use to
// budget rewind ring buffers and estimate rewind duration. Serialize
// output is compressed, so the actual size varies with game content;
// this is a conservative estimate of a typical in-game state (the
// ~12 MB uncompressed body compresses well below this), not a bound.
func SerializeSize() int {
	return 4 * 1024 * 1024
}

// fieldReader reads tagged field records out of one parsed chunk. A
// size mismatch, an invalid value, or a leftover unread field is an
// error; a missing field is not - it reads as the zero value, so a
// file from an older version that predates the field loads cleanly.
// The first error wins; all getters return zero values after it so
// decoders can read straight through.
type fieldReader struct {
	chunk  string
	fields map[string][]byte
	read   map[string]bool
	err    error
}

func newFieldReader(chunk string, fields map[string][]byte) *fieldReader {
	return &fieldReader{chunk: chunk, fields: fields, read: make(map[string]bool, len(fields))}
}

func (r *fieldReader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("savestate: chunk %s: "+format, append([]any{r.chunk}, args...)...)
	}
}

// take returns a field's data, requiring exactly want bytes (want < 0
// accepts any size). A missing field returns nil without error: the
// file predates the field and the value is skipped (getters read it
// as zero). The returned slice aliases the decompressed body; callers
// that retain it must copy.
func (r *fieldReader) take(name string, want int) []byte {
	if r.err != nil {
		return nil
	}
	data, ok := r.fields[name]
	if !ok {
		return nil
	}
	r.read[name] = true
	if want >= 0 && len(data) != want {
		r.fail("field %s size %d, want %d", name, len(data), want)
		return nil
	}
	return data
}

// finish returns the accumulated error, or an error naming a field
// present in the chunk that no decoder read (unknown field).
func (r *fieldReader) finish() error {
	if r.err != nil {
		return r.err
	}
	for name := range r.fields {
		if !r.read[name] {
			return fmt.Errorf("savestate: chunk %s: unknown field %s", r.chunk, name)
		}
	}
	return nil
}

func (r *fieldReader) u8(name string) uint8 {
	d := r.take(name, 1)
	if d == nil {
		return 0
	}
	return d[0]
}

func (r *fieldReader) flag(name string) bool {
	v := r.u8(name)
	if v > 1 {
		r.fail("field %s bool value %d", name, v)
		return false
	}
	return v == 1
}

func (r *fieldReader) u16(name string) uint16 {
	d := r.take(name, 2)
	if d == nil {
		return 0
	}
	return binary.BigEndian.Uint16(d)
}

func (r *fieldReader) u32(name string) uint32 {
	d := r.take(name, 4)
	if d == nil {
		return 0
	}
	return binary.BigEndian.Uint32(d)
}

func (r *fieldReader) u64(name string) uint64 {
	d := r.take(name, 8)
	if d == nil {
		return 0
	}
	return binary.BigEndian.Uint64(d)
}

func (r *fieldReader) i16(name string) int16 { return int16(r.u16(name)) }
func (r *fieldReader) i32(name string) int32 { return int32(r.u32(name)) }
func (r *fieldReader) i64(name string) int64 { return int64(r.u64(name)) }

// iRange reads an i64 field constrained to [lo, hi], for values used
// as indices or loop bounds after restore.
func (r *fieldReader) iRange(name string, lo, hi int64) int {
	v := r.i64(name)
	if r.err == nil && (v < lo || v > hi) {
		r.fail("field %s value %d outside [%d, %d]", name, v, lo, hi)
		return 0
	}
	return int(v)
}

func (r *fieldReader) u16s(name string, n int) []uint16 {
	d := r.take(name, n*2)
	if d == nil {
		return make([]uint16, n)
	}
	out := make([]uint16, n)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(d[i*2:])
	}
	return out
}

func (r *fieldReader) u32s(name string, n int) []uint32 {
	d := r.take(name, n*4)
	if d == nil {
		return make([]uint32, n)
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.BigEndian.Uint32(d[i*4:])
	}
	return out
}

func (r *fieldReader) u64s(name string, n int) []uint64 {
	d := r.take(name, n*8)
	if d == nil {
		return make([]uint64, n)
	}
	out := make([]uint64, n)
	for i := range out {
		out[i] = binary.BigEndian.Uint64(d[i*8:])
	}
	return out
}

func (r *fieldReader) i16s(name string, n int) []int16 {
	u := r.u16s(name, n)
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(u[i])
	}
	return out
}

func (r *fieldReader) i32s(name string, n int) []int32 {
	u := r.u32s(name, n)
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(u[i])
	}
	return out
}

func (r *fieldReader) i64s(name string, n int) []int64 {
	u := r.u64s(name, n)
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(u[i])
	}
	return out
}

func (r *fieldReader) i8s(name string, n int) []int8 {
	d := r.take(name, n)
	out := make([]int8, n)
	for i := range out {
		if d != nil {
			out[i] = int8(d[i])
		}
	}
	return out
}

func (r *fieldReader) flags(name string, n int) []bool {
	d := r.take(name, n)
	out := make([]bool, n)
	for i := range out {
		if d != nil {
			if d[i] > 1 {
				r.fail("field %s[%d] bool value %d", name, i, d[i])
				return out
			}
			out[i] = d[i] == 1
		}
	}
	return out
}

func (r *fieldReader) ddaEdge(prefix string) ddaEdge {
	return ddaEdge{
		x:  r.i64(prefix + ".x"),
		y:  r.i64(prefix + ".y"),
		dx: r.i64(prefix + ".dx"),
		dy: r.i64(prefix + ".dy"),
	}
}

func (r *fieldReader) gouraud(prefix string) gouraudStepper {
	return gouraudStepper{
		r:  int(r.i64(prefix + ".r")),
		g:  int(r.i64(prefix + ".g")),
		b:  int(r.i64(prefix + ".b")),
		dr: int(r.i64(prefix + ".dr")),
		dg: int(r.i64(prefix + ".dg")),
		db: int(r.i64(prefix + ".db")),
	}
}

func (r *fieldReader) vdp1Cmd(prefix string) vdp1Command {
	return vdp1Command{
		ctrl: r.u16(prefix + ".ctrl"),
		link: r.u16(prefix + ".link"),
		pmod: r.u16(prefix + ".pmod"),
		colr: r.u16(prefix + ".colr"),
		srca: r.u16(prefix + ".srca"),
		size: r.u16(prefix + ".size"),
		xa:   r.i16(prefix + ".xa"),
		ya:   r.i16(prefix + ".ya"),
		xb:   r.i16(prefix + ".xb"),
		yb:   r.i16(prefix + ".yb"),
		xc:   r.i16(prefix + ".xc"),
		yc:   r.i16(prefix + ".yc"),
		xd:   r.i16(prefix + ".xd"),
		yd:   r.i16(prefix + ".yd"),
		grda: r.u16(prefix + ".grda"),
	}
}

// parseStateChunks strictly parses the decompressed body into per-chunk
// field maps. All lengths are validated against remaining bytes; a
// malformed file returns an error, never panics.
func parseStateChunks(body []byte) (map[string]map[string][]byte, error) {
	chunks := make(map[string]map[string][]byte)
	bp := 0
	for bp < len(body) {
		if bp+stateTagLen+4 > len(body) {
			return nil, fmt.Errorf("savestate: chunk header truncated at offset %d", bp)
		}
		rawTag := body[bp : bp+stateTagLen]
		nameEnd := bytes.IndexByte(rawTag, 0)
		if nameEnd <= 0 {
			return nil, fmt.Errorf("savestate: malformed chunk tag at offset %d", bp)
		}
		tag := string(rawTag[:nameEnd])
		for _, b := range rawTag[nameEnd:] {
			if b != 0 {
				return nil, fmt.Errorf("savestate: chunk %s tag not zero-filled", tag)
			}
		}
		bp += stateTagLen
		clen := int(binary.BigEndian.Uint32(body[bp:]))
		bp += 4
		if clen < 0 || bp+clen > len(body) {
			return nil, fmt.Errorf("savestate: chunk %s length %d overruns body", tag, clen)
		}
		if _, dup := chunks[tag]; dup {
			return nil, fmt.Errorf("savestate: duplicate chunk %s", tag)
		}

		fields := make(map[string][]byte)
		payload := body[bp : bp+clen]
		fp := 0
		for fp < len(payload) {
			nlen := int(payload[fp])
			fp++
			if nlen == 0 || fp+nlen+4 > len(payload) {
				return nil, fmt.Errorf("savestate: chunk %s: field header invalid at payload offset %d", tag, fp-1)
			}
			name := string(payload[fp : fp+nlen])
			fp += nlen
			fsize := int(binary.BigEndian.Uint32(payload[fp:]))
			fp += 4
			if fsize < 0 || fp+fsize > len(payload) {
				return nil, fmt.Errorf("savestate: chunk %s field %s size %d overruns chunk", tag, name, fsize)
			}
			if _, dup := fields[name]; dup {
				return nil, fmt.Errorf("savestate: chunk %s: duplicate field %s", tag, name)
			}
			fields[name] = payload[fp : fp+fsize]
			fp += fsize
		}
		chunks[tag] = fields
		bp += clen
	}
	return chunks, nil
}

// decodeMem validates a memory chunk against the destination region's
// exact size and returns its applier.
func decodeMem(r *fieldReader, dst []byte) func() {
	data := r.take("data", len(dst))
	return func() { copy(dst, data) }
}

func decodeSH2Core(r *fieldReader, st *sh2.State) {
	copy(st.Reg.R[:], r.u32s("R", 16))
	st.Reg.PC = r.u32("PC")
	st.Reg.PR = r.u32("PR")
	st.Reg.SR = r.u32("SR")
	st.Reg.GBR = r.u32("GBR")
	st.Reg.VBR = r.u32("VBR")
	st.Reg.MACH = r.u32("MACH")
	st.Reg.MACL = r.u32("MACL")
	st.Cycles = r.u64("cycles")
	st.Halted = r.flag("halted")
	st.IRL = r.u32("irl")

	st.Exec.PrevPC = r.u32("prevPC")
	st.Exec.DelayPC = r.u32("delayPC")
	st.Exec.InDelay = r.flag("inDelay")
	st.Exec.NMIPending = r.flag("nmiPending")
	st.Exec.NMIReq = r.flag("nmiReq")
	st.Exec.IntInhibit = r.flag("intInhibit")
	st.Exec.LastLoadReg = r.u8("lastLoadReg")
	st.Exec.MultiplierBusyUntil = r.u64("multiplierBusyUntil")
	st.Exec.NextPeripheralEvent = r.u64("nextPeripheralEvent")
	st.Exec.FetchLineAddr = r.u32("fetchLineAddr")
	st.Exec.FetchLineWay = r.iRange("fetchLineWay", 0, 3)
	st.Exec.FetchLineOff = r.u32("fetchLineOff")
	st.Exec.SBYCR = r.u8("sbycr")
	st.Exec.BCR1 = r.u16("bcr1")
	st.Cache.CCR = r.u8("ccr")

	st.INTC.IPRA = r.u16("intc.ipra")
	st.INTC.IPRB = r.u16("intc.iprb")
	st.INTC.VCRA = r.u16("intc.vcra")
	st.INTC.VCRB = r.u16("intc.vcrb")
	st.INTC.VCRC = r.u16("intc.vcrc")
	st.INTC.VCRD = r.u16("intc.vcrd")
	st.INTC.ICR = r.u16("intc.icr")
	st.INTC.VCRWDT = r.u16("intc.vcrwdt")
	st.INTC.Pending = r.u16("intc.pending")

	st.FRT.FRC = r.u16("frt.frc")
	st.FRT.OCRA = r.u16("frt.ocra")
	st.FRT.OCRB = r.u16("frt.ocrb")
	st.FRT.ICR = r.u16("frt.icr")
	st.FRT.TIER = r.u8("frt.tier")
	st.FRT.FTCSR = r.u8("frt.ftcsr")
	st.FRT.ReadFlags = r.u8("frt.readFlags")
	st.FRT.TCR = r.u8("frt.tcr")
	st.FRT.TOCR = r.u8("frt.tocr")
	st.FRT.Prescaler = r.u16("frt.prescaler")
	st.FRT.Temp = r.u8("frt.temp")
	st.FRT.LastSync = r.u64("frt.lastSync")
	st.FRT.NextEvent = r.u64("frt.nextEvent")

	st.DIVU.DVSR = r.u32("divu.dvsr")
	st.DIVU.DVDNT = r.u32("divu.dvdnt")
	st.DIVU.DVDNTH = r.u32("divu.dvdnth")
	st.DIVU.DVDNTL = r.u32("divu.dvdntl")
	st.DIVU.DVCR = r.u32("divu.dvcr")
	st.DIVU.VCRDIV = r.u32("divu.vcrdiv")

	sar := r.u32s("dmac.sar", 2)
	dar := r.u32s("dmac.dar", 2)
	tcr := r.u32s("dmac.tcr", 2)
	chcr := r.u32s("dmac.chcr", 2)
	vcrdma := r.u32s("dmac.vcrdma", 2)
	for i := 0; i < 2; i++ {
		st.DMAC.Ch[i].SAR = sar[i]
		st.DMAC.Ch[i].DAR = dar[i]
		st.DMAC.Ch[i].TCR = tcr[i]
		st.DMAC.Ch[i].CHCR = chcr[i]
		st.DMAC.Ch[i].VCRDMA = vcrdma[i]
	}
	st.DMAC.DMAOR = r.u16("dmac.dmaor")
	copy(st.DMAC.DRCR[:], r.take("dmac.drcr", 2))
	st.DMAC.NextCh = r.iRange("dmac.nextCh", 0, 1)
	switch d := r.take("dmac.completesAt", -1); len(d) {
	case 16:
		st.DMAC.CompletesAt[0] = binary.BigEndian.Uint64(d)
		st.DMAC.CompletesAt[1] = binary.BigEndian.Uint64(d[8:])
	case 0:
		// Field absent: no transfer in progress on either channel.
	default:
		r.fail("field dmac.completesAt size %d, want 16", len(d))
	}

	st.WDT.WTCSR = r.u8("wdt.wtcsr")
	st.WDT.WTCNT = r.u8("wdt.wtcnt")
	st.WDT.RSTCSR = r.u8("wdt.rstcsr")
	st.WDT.Prescaler = r.u32("wdt.prescaler")
	st.WDT.LastSync = r.u64("wdt.lastSync")
	st.WDT.NextEvent = r.u64("wdt.nextEvent")
}

func decodeSH2Cache(r *fieldReader, st *sh2.State) {
	copy(st.Cache.Data[:], r.take("data", 4096))
	tag := r.u32s("tag", 4*64)
	valid := r.flags("valid", 4*64)
	for way := range st.Cache.Tag {
		copy(st.Cache.Tag[way][:], tag[way*64:])
		copy(st.Cache.Valid[way][:], valid[way*64:])
	}
	copy(st.Cache.LRU[:], r.take("lru", 64))
}

func decodeSCUCore(e *Emulator, r *fieldReader) func() {
	dmaR := r.u32s("dmaR", 3)
	dmaW := r.u32s("dmaW", 3)
	dmaC := r.u32s("dmaC", 3)
	dmaAD := r.u32s("dmaAD", 3)
	dmaEN := r.u32s("dmaEN", 3)
	dmaMD := r.u32s("dmaMD", 3)
	dstp := r.u32("dstp")
	dmaPending := r.flags("dmaPending", 3)
	dmaDelay := r.i64s("dmaDelay", 3)
	dspCycleCarry := r.u32("dspCycleCarry")
	t0c := r.u32("t0c")
	t1s := r.u32("t1s")
	t1md := r.u32("t1md")
	t0cnt := r.u32("t0cnt")
	t1Delay := r.i64("t1Delay")
	t1Gate := r.flag("t1Gate")
	ims := r.u32("ims")
	ist := r.u32("ist")
	aiak := r.u32("aiak")
	asr0 := r.u32("asr0")
	asr1 := r.u32("asr1")
	aref := r.u32("aref")
	rsel := r.u32("rsel")
	pendingBit := r.iRange("pendingBit", -1, 31)
	irlWithheld := r.flag("irlWithheld")
	reqPending := r.u32("reqPending")
	return func() {
		s := e.scu
		copy(s.dmaR[:], dmaR)
		copy(s.dmaW[:], dmaW)
		copy(s.dmaC[:], dmaC)
		copy(s.dmaAD[:], dmaAD)
		copy(s.dmaEN[:], dmaEN)
		copy(s.dmaMD[:], dmaMD)
		s.dstp = dstp
		copy(s.dmaPending[:], dmaPending)
		for i := range s.dmaDelay {
			s.dmaDelay[i] = int(dmaDelay[i])
		}
		s.dspCycleCarry = dspCycleCarry
		s.t0c = t0c
		s.t1s = t1s
		s.t1md = t1md
		s.t0cnt = t0cnt
		s.t1Delay = int(t1Delay)
		s.t1Gate = t1Gate
		s.ims = ims
		s.ist = ist
		s.aiak = aiak
		s.asr0 = asr0
		s.asr1 = asr1
		s.aref = aref
		s.rsel = rsel
		s.pendingBit = pendingBit
		s.irlWithheld = irlWithheld
		s.reqPending = reqPending
	}
}

func decodeSCUDSP(e *Emulator, r *fieldReader) func() {
	prog := r.u32s("prog", 256)
	data := r.u32s("data", 4*64)
	pc := r.u8("pc")
	top := r.u8("top")
	lop := r.u16("lop")
	ct := r.take("ct", 4)
	if r.err == nil {
		for i, v := range ct {
			if v > 0x3F {
				r.fail("field ct[%d] value %d exceeds 0x3F", i, v)
				break
			}
		}
	}
	ach := r.u16("ach")
	acl := r.u32("acl")
	ph := r.u16("ph")
	pl := r.u32("pl")
	rx := r.u32("rx")
	ry := r.u32("ry")
	ra0 := r.u32("ra0")
	wa0 := r.u32("wa0")
	flagS := r.flag("flagS")
	flagZ := r.flag("flagZ")
	flagC := r.flag("flagC")
	flagV := r.flag("flagV")
	flagEnd := r.flag("flagEnd")
	flagT0 := r.flag("flagT0")
	executing := r.flag("executing")
	looping := r.flag("looping")
	nextInstr := r.u32("nextInstr")
	debt := r.u32("debt")
	pdaAddr := r.u8("pdaAddr")
	return func() {
		d := &e.scu.dsp
		copy(d.prog[:], prog)
		for bank := range d.data {
			copy(d.data[bank][:], data[bank*64:])
		}
		d.pc = pc
		d.top = top
		d.lop = lop
		copy(d.ct[:], ct)
		d.ach = ach
		d.acl = acl
		d.ph = ph
		d.pl = pl
		d.rx = rx
		d.ry = ry
		d.ra0 = ra0
		d.wa0 = wa0
		d.flagS = flagS
		d.flagZ = flagZ
		d.flagC = flagC
		d.flagV = flagV
		d.flagEnd = flagEnd
		d.flagT0 = flagT0
		d.executing = executing
		d.looping = looping
		d.nextInstr = nextInstr
		d.debt = debt
		d.pdaAddr = pdaAddr
	}
}

func decodeSCSPRegs(e *Emulator, r *fieldReader) func() {
	regs := r.u16s("regs", scspRegWords)
	return func() { copy(e.scsp.regs[:], regs) }
}

func decodeSCSPSlots(e *Emulator, r *fieldReader) func() {
	phase := r.u32s("phase", 32)
	egLevel := r.i32s("egLevel", 32)
	egState := r.take("egState", 32)
	egStep := r.i32s("egStep", 32)
	egTarget := r.i32s("egTarget", 32)
	active := r.flags("active", 32)
	finished := r.flags("finished", 32)
	loopDir := r.i8s("loopDir", 32)
	output := r.i16s("output", 32)
	lfoPhase := r.take("lfoPhase", 32)
	lfoStep := r.u16s("lfoStep", 32)
	lfoNoise := r.u16s("lfoNoise", 32)
	return func() {
		for i := range e.scsp.slots {
			sl := &e.scsp.slots[i]
			sl.phase = phase[i]
			sl.egLevel = egLevel[i]
			sl.egState = egState[i]
			sl.egStep = egStep[i]
			sl.egTarget = egTarget[i]
			sl.active = active[i]
			sl.finished = finished[i]
			sl.loopDir = loopDir[i]
			sl.output = output[i]
			sl.lfoPhase = lfoPhase[i]
			sl.lfoStep = lfoStep[i]
			sl.lfoNoise = lfoNoise[i]
		}
	}
}

func decodeSCSPDSP(e *Emulator, r *fieldReader) func() {
	mpro := r.u64s("mpro", 128)
	temp := r.i32s("temp", 128)
	mems := r.i32s("mems", 32)
	mixs := r.i32s("mixs", 16)
	exts := r.i32s("exts", 2)
	efreg := r.i16s("efreg", 16)
	coef := r.i16s("coef", 64)
	madrs := r.u16s("madrs", 32)
	sftReg := r.i32("sftReg")
	frcReg := r.u16("frcReg")
	yReg := r.i32("yReg")
	adrsReg := r.u16("adrsReg")
	mdecCT := r.u16("mdecCT")
	inputs := r.i32("inputs")
	readPending := r.u8("readPending")
	readValue := r.i32("readValue")
	writePending := r.flag("writePending")
	writeValue := r.u16("writeValue")
	rwAddr := r.u32("rwAddr")
	// lastStep bounds the microprogram execution loop over mpro[128].
	lastStep := r.iRange("lastStep", 0, 128)
	return func() {
		d := &e.scsp.dsp
		copy(d.mpro[:], mpro)
		copy(d.temp[:], temp)
		copy(d.mems[:], mems)
		copy(d.mixs[:], mixs)
		copy(d.exts[:], exts)
		copy(d.efreg[:], efreg)
		copy(d.coef[:], coef)
		copy(d.madrs[:], madrs)
		d.sftReg = sftReg
		d.frcReg = frcReg
		d.yReg = yReg
		d.adrsReg = adrsReg
		d.mdecCT = mdecCT
		d.inputs = inputs
		d.readPending = readPending
		d.readValue = readValue
		d.writePending = writePending
		d.writeValue = writeValue
		d.rwAddr = rwAddr
		d.lastStep = lastStep
	}
}

func decodeSCSPTimers(e *Emulator, r *fieldReader) func() {
	prescaler := r.u16s("timerPrescaler", 3)
	counter := r.take("timerCounter", 3)
	return func() {
		copy(e.scsp.timerPrescaler[:], prescaler)
		copy(e.scsp.timerCounter[:], counter)
	}
}

func decodeSCSPMisc(e *Emulator, r *fieldReader) func() {
	inReset := r.flag("inReset")
	mainIntActive := r.flag("mainIntActive")
	soundIntLevel := r.u8("soundIntLevel")
	lfsr := r.u32("lfsr")
	fmHistory := r.i16s("fmHistory", 64)
	fmHistoryCur := r.iRange("fmHistoryCur", 0, 63)
	fmDelayer := r.i16s("fmDelayer", 4)
	soundReqPending := r.flag("soundReqPending")
	pendingReset := r.flag("pendingReset")
	return func() {
		s := e.scsp
		s.inReset = inReset
		s.mainIntActive = mainIntActive
		s.soundIntLevel = soundIntLevel
		s.lfsr = lfsr
		copy(s.fmHistory[:], fmHistory)
		s.fmHistoryCur = fmHistoryCur
		copy(s.fmDelayer[:], fmDelayer)
		s.soundReqPending.Store(soundReqPending)
		s.pendingReset.Store(pendingReset)
	}
}

// decodeM68K validates the blob size in the decode phase; the chip's
// own format validation runs in the apply phase and its error is
// surfaced through errOut.
func decodeM68K(e *Emulator, r *fieldReader, errOut *error) func() {
	blob := r.take("blob", m68k.SerializeSize)
	return func() {
		buf := make([]byte, len(blob))
		copy(buf, blob)
		if err := e.scsp.M68KDeserialize(buf); err != nil && *errOut == nil {
			*errOut = fmt.Errorf("savestate: chunk %s: %w", tagM68K, err)
		}
	}
}

func decodeVDP1Regs(e *Emulator, r *fieldReader) func() {
	tvmr := r.u16("tvmr")
	fbcr := r.u16("fbcr")
	ptmr := r.u16("ptmr")
	ewdr := r.u16("ewdr")
	ewlr := r.u16("ewlr")
	ewrr := r.u16("ewrr")
	edsr := r.u16("edsr")
	lopr := r.u16("lopr")
	copr := r.u16("copr")
	fbcrPending := r.u16("fbcrPending")
	ptmrPending := r.u16("ptmrPending")
	ewdrPending := r.u16("ewdrPending")
	ewlrPending := r.u16("ewlrPending")
	ewrrPending := r.u16("ewrrPending")
	localX := r.i16("localX")
	localY := r.i16("localY")
	sysClipX := r.i16("sysClipX")
	sysClipY := r.i16("sysClipY")
	userClipX1 := r.i16("userClipX1")
	userClipY1 := r.i16("userClipY1")
	userClipX2 := r.i16("userClipX2")
	userClipY2 := r.i16("userClipY2")
	drawPending := r.flag("drawPending")
	drawEnd := r.flag("drawEnd")
	drawActive := r.flag("drawActive")
	procAddr := r.u32("procAddr")
	procReturnAddr := r.u32("procReturnAddr")
	systemCycleDebt := r.i32("systemCycleDebt")
	vramWriteStall := r.i32("vramWriteStall")
	drawTicked := r.i64("drawTicked")
	drawStallCharged := r.i64("drawStallCharged")
	vbeLatched := r.flag("vbeLatched")
	fbcrWritten := r.flag("fbcrWritten")
	eraseRequested := r.flag("eraseRequested")
	fbSwapped := r.flag("fbSwapped")
	lateErasePending := r.flag("lateErasePending")
	return func() {
		v := e.vdp1
		v.tvmr = tvmr
		v.fbcr = fbcr
		v.ptmr = ptmr
		v.ewdr = ewdr
		v.ewlr = ewlr
		v.ewrr = ewrr
		v.edsr = edsr
		v.lopr = lopr
		v.copr = copr
		v.fbcrPending = fbcrPending
		v.ptmrPending = ptmrPending
		v.ewdrPending = ewdrPending
		v.ewlrPending = ewlrPending
		v.ewrrPending = ewrrPending
		v.localX = localX
		v.localY = localY
		v.sysClipX = sysClipX
		v.sysClipY = sysClipY
		v.userClipX1 = userClipX1
		v.userClipY1 = userClipY1
		v.userClipX2 = userClipX2
		v.userClipY2 = userClipY2
		v.drawPending = drawPending
		v.drawEnd = drawEnd
		v.drawActive = drawActive
		v.procAddr = procAddr
		v.procReturnAddr = procReturnAddr
		v.systemCycleDebt = systemCycleDebt
		v.vramWriteStallCycles.Store(vramWriteStall)
		v.drawTicked.Store(drawTicked)
		v.drawStallCharged.Store(drawStallCharged)
		v.vbeLatched = vbeLatched
		v.fbcrWritten = fbcrWritten
		v.eraseRequested = eraseRequested
		v.fbSwapped = fbSwapped
		if lateErasePending {
			v.lateEraseFB = v.displayFB
		} else {
			v.lateEraseFB = nil
		}
	}
}

func decodeVDP1Resume(e *Emulator, r *fieldReader) func() {
	cmdPhase := int(r.i64("cmdPhase"))
	cmdSnapshot := r.vdp1Cmd("snap")

	var sr spriteResumeState
	sr.charAddr = r.u32("sprite.charAddr")
	sr.charW = int(r.i64("sprite.charW"))
	sr.charH = int(r.i64("sprite.charH"))
	sr.colorMode = r.u16("sprite.colorMode")
	sr.cc = r.u16("sprite.cc")
	// pixCycles is derived from cc, not stored; recompute on load.
	sr.pixCycles = vdp1PixelCycles(sr.cc)
	sr.ecdOff = r.flag("sprite.ecdOff")
	sr.spdOn = r.flag("sprite.spdOn")
	sr.msbOn = r.flag("sprite.msbOn")
	sr.mesh = r.flag("sprite.mesh")
	sr.userClip = r.u16("sprite.userClip")
	sr.flipH = r.flag("sprite.flipH")
	sr.flipV = r.flag("sprite.flipV")
	sr.drawX = int(r.i64("sprite.drawX"))
	sr.drawY = int(r.i64("sprite.drawY"))
	sr.clipX = int(r.i64("sprite.clipX"))
	sr.clipY = int(r.i64("sprite.clipY"))
	copy(sr.gt[:], r.u16s("sprite.gt", 4))
	sr.isScaled = r.flag("sprite.isScaled")
	sr.destW = int(r.i64("sprite.destW"))
	sr.destH = int(r.i64("sprite.destH"))
	sr.dstX1 = int(r.i64("sprite.dstX1"))
	sr.dstY1 = int(r.i64("sprite.dstY1"))
	sr.effFlipH = r.flag("sprite.effFlipH")
	sr.effFlipV = r.flag("sprite.effFlipV")
	sr.hssShrinkX = r.flag("sprite.hssShrinkX")
	sr.hssOddParity = r.flag("sprite.hssOddParity")
	sr.hssEcdOff = r.flag("sprite.hssEcdOff")
	sr.outerIdx = int(r.i64("sprite.outerIdx"))
	sr.innerIdx = int(r.i64("sprite.innerIdx"))
	sr.endCodeCount = int(r.i64("sprite.endCodeCount"))
	sr.prevSrcX = int(r.i64("sprite.prevSrcX"))
	sr.srcReadX = int(r.i64("sprite.srcReadX"))

	var dr distortedResumeState
	dr.charAddr = r.u32("dist.charAddr")
	dr.charW = int(r.i64("dist.charW"))
	dr.charH = int(r.i64("dist.charH"))
	dr.colorMode = r.u16("dist.colorMode")
	dr.cc = r.u16("dist.cc")
	dr.pixCycles = vdp1PixelCycles(dr.cc)
	dr.ecdOff = r.flag("dist.ecdOff")
	dr.spdOn = r.flag("dist.spdOn")
	dr.msbOn = r.flag("dist.msbOn")
	dr.mesh = r.flag("dist.mesh")
	dr.userClip = r.u16("dist.userClip")
	dr.hss = r.flag("dist.hss")
	dr.hssOdd = r.flag("dist.hssOdd")
	dr.hssShrinkU = r.flag("dist.hssShrinkU")
	dr.flipH = r.flag("dist.flipH")
	dr.flipV = r.flag("dist.flipV")
	dr.bboxMinX = int(r.i64("dist.bboxMinX"))
	dr.bboxMinY = int(r.i64("dist.bboxMinY"))
	dr.bboxMaxX = int(r.i64("dist.bboxMaxX"))
	dr.bboxMaxY = int(r.i64("dist.bboxMaxY"))
	dr.clipX = int(r.i64("dist.clipX"))
	dr.clipY = int(r.i64("dist.clipY"))
	dr.ax = int(r.i64("dist.ax"))
	dr.ay = int(r.i64("dist.ay"))
	dr.bx = int(r.i64("dist.bx"))
	dr.by = int(r.i64("dist.by"))
	dr.cx = int(r.i64("dist.cx"))
	dr.cy = int(r.i64("dist.cy"))
	dr.dx = int(r.i64("dist.dx"))
	dr.dy = int(r.i64("dist.dy"))
	dr.dmax = int(r.i64("dist.dmax"))
	dr.rowStride = int(r.i64("dist.rowStride"))
	dr.bpp8 = r.flag("dist.bpp8")
	dr.simpleMode = r.flag("dist.simpleMode")
	copy(dr.gt[:], r.u16s("dist.gt", 4))
	dr.checkEcd = r.flag("dist.checkEcd")
	dr.colr = r.u16("dist.colr")
	dr.uStart = int(r.i64("dist.uStart"))
	dr.uEnd = int(r.i64("dist.uEnd"))
	dr.dvFP = int(r.i64("dist.dvFP"))
	dr.leftEdge = r.ddaEdge("dist.leftEdge")
	dr.rightEdge = r.ddaEdge("dist.rightEdge")
	dr.gsOuterLeft = r.gouraud("dist.gsOuterLeft")
	dr.gsOuterRight = r.gouraud("dist.gsOuterRight")
	dr.vFP = int(r.i64("dist.vFP"))
	dr.outerI = int(r.i64("dist.outerI"))
	dr.innerJ = int(r.i64("dist.innerJ"))
	dr.curLx = int(r.i64("dist.curLx"))
	dr.curLy = int(r.i64("dist.curLy"))
	dr.curRx = int(r.i64("dist.curRx"))
	dr.curRy = int(r.i64("dist.curRy"))
	dr.rowBase = r.u32("dist.rowBase")
	dr.lineDx = int(r.i64("dist.lineDx"))
	dr.lineDy = int(r.i64("dist.lineDy"))
	dr.lineLen = int(r.i64("dist.lineLen"))
	dr.jEnd = int(r.i64("dist.jEnd"))
	dr.pxFP = r.i64("dist.pxFP")
	dr.pyFP = r.i64("dist.pyFP")
	dr.dpxFP = r.i64("dist.dpxFP")
	dr.dpyFP = r.i64("dist.dpyFP")
	dr.uFP = int(r.i64("dist.uFP"))
	dr.duFP = int(r.i64("dist.duFP"))
	dr.prevPx = int(r.i64("dist.prevPx"))
	dr.prevPy = int(r.i64("dist.prevPy"))
	dr.endCodeCount = int(r.i64("dist.endCodeCount"))
	dr.srcReadU = int(r.i64("dist.srcReadU"))
	dr.gsLine = r.gouraud("dist.gsLine")

	var pr polygonResumeState
	pr.color = r.u16("poly.color")
	pr.cc = r.u16("poly.cc")
	pr.pixCycles = vdp1PixelCycles(pr.cc)
	pr.msbOn = r.flag("poly.msbOn")
	pr.mesh = r.flag("poly.mesh")
	pr.userClip = r.u16("poly.userClip")
	pr.clipX = int(r.i64("poly.clipX"))
	pr.clipY = int(r.i64("poly.clipY"))
	pr.simpleMode = r.flag("poly.simpleMode")
	copy(pr.gt[:], r.u16s("poly.gt", 4))
	pr.ax = int(r.i64("poly.ax"))
	pr.ay = int(r.i64("poly.ay"))
	pr.bx = int(r.i64("poly.bx"))
	pr.by = int(r.i64("poly.by"))
	pr.cx = int(r.i64("poly.cx"))
	pr.cy = int(r.i64("poly.cy"))
	pr.dx = int(r.i64("poly.dx"))
	pr.dy = int(r.i64("poly.dy"))
	pr.dmax = int(r.i64("poly.dmax"))
	pr.leftEdge = r.ddaEdge("poly.leftEdge")
	pr.rightEdge = r.ddaEdge("poly.rightEdge")
	pr.gsOuterLeft = r.gouraud("poly.gsOuterLeft")
	pr.gsOuterRight = r.gouraud("poly.gsOuterRight")
	pr.gsLine = r.gouraud("poly.gsLine")
	pr.outerI = int(r.i64("poly.outerI"))
	pr.innerJ = int(r.i64("poly.innerJ"))
	pr.curLx = int(r.i64("poly.curLx"))
	pr.curLy = int(r.i64("poly.curLy"))
	pr.curRx = int(r.i64("poly.curRx"))
	pr.curRy = int(r.i64("poly.curRy"))
	pr.lineDx = int(r.i64("poly.lineDx"))
	pr.lineDy = int(r.i64("poly.lineDy"))
	pr.lineLen = int(r.i64("poly.lineLen"))
	pr.jEnd = int(r.i64("poly.jEnd"))
	pr.pxFP = r.i64("poly.pxFP")
	pr.pyFP = r.i64("poly.pyFP")
	pr.dpxFP = r.i64("poly.dpxFP")
	pr.dpyFP = r.i64("poly.dpyFP")
	pr.prevPx = int(r.i64("poly.prevPx"))
	pr.prevPy = int(r.i64("poly.prevPy"))

	var lr lineResumeState
	lr.color = r.u16("line.color")
	lr.cc = r.u16("line.cc")
	lr.pixCycles = vdp1PixelCycles(lr.cc)
	lr.msbOn = r.flag("line.msbOn")
	lr.mesh = r.flag("line.mesh")
	lr.userClip = r.u16("line.userClip")
	lr.clipX = int(r.i64("line.clipX"))
	lr.clipY = int(r.i64("line.clipY"))
	lr.gStart = r.u16("line.gStart")
	lr.gEnd = r.u16("line.gEnd")
	lr.x = int(r.i64("line.x"))
	lr.y = int(r.i64("line.y"))
	lr.dx = int(r.i64("line.dx"))
	lr.dy = int(r.i64("line.dy"))
	lr.sx = int(r.i64("line.sx"))
	lr.sy = int(r.i64("line.sy"))
	lr.err = int(r.i64("line.err"))
	lr.iter = int(r.i64("line.iter"))
	lr.iterMax = int(r.i64("line.iterMax"))
	lr.xMajor = r.flag("line.xMajor")
	lr.polylineLeg = int(r.i64("line.polylineLeg"))
	lr.legAx = int(r.i64("line.legAx"))
	lr.legAy = int(r.i64("line.legAy"))
	lr.legBx = int(r.i64("line.legBx"))
	lr.legBy = int(r.i64("line.legBy"))
	lr.legCx = int(r.i64("line.legCx"))
	lr.legCy = int(r.i64("line.legCy"))
	lr.legDx = int(r.i64("line.legDx"))
	lr.legDy = int(r.i64("line.legDy"))
	copy(lr.polylineGt[:], r.u16s("line.polylineGt", 4))

	return func() {
		v := e.vdp1
		v.cmdPhase = cmdPhase
		v.cmdSnapshot = cmdSnapshot
		v.spriteResume = sr
		v.distortedResume = dr
		v.polygonResume = pr
		v.lineResume = lr
	}
}

func decodeVDP2State(e *Emulator, r *fieldReader) func() {
	regs := r.u16s("regs", vdp2RegCount)
	vLine := r.u16("vLine")
	lineCycle := r.u32("lineCycle")
	oddField := r.flag("oddField")
	latchedLineCycle := r.u32("latchedLineCycle")
	latchedVLine := r.u16("latchedVLine")
	latchedHiRes := r.flag("latchedHiRes")
	latchedInterlace := r.u8("latchedInterlace")
	latchedOddField := r.flag("latchedOddField")
	exltfg := r.flag("exltfg")
	pal := r.flag("pal")
	scynFrame := r.i64s("scynFrame", 4)
	return func() {
		v := e.vdp2
		copy(v.regs[:], regs)
		v.vLine = vLine
		v.lineCycle = lineCycle
		v.oddField = oddField
		v.latchedLineCycle = latchedLineCycle
		v.latchedVLine = latchedVLine
		v.latchedHiRes = latchedHiRes
		v.latchedInterlace = latchedInterlace
		v.latchedOddField = latchedOddField
		v.exltfg = exltfg
		v.pal = pal
		for i := range v.scynFrame {
			v.scynFrame[i] = int32(scynFrame[i])
		}
	}
}

func decodeSMPC(e *Emulator, r *fieldReader) func() {
	comreg := r.u8("comreg")
	sr := r.u8("sr")
	sf := r.u8("sf")
	ireg := r.take("ireg", 7)
	cmdIREG := r.take("cmdIREG", 7)
	oreg := r.take("oreg", 32)
	pdr1 := r.u8("pdr1")
	pdr2 := r.u8("pdr2")
	ddr1 := r.u8("ddr1")
	ddr2 := r.u8("ddr2")
	iosel := r.u8("iosel")
	exle := r.u8("exle")
	rtc := r.take("rtc", 7)
	rtcBaseTime := r.i64("rtcBaseTime")
	rtcFrames := r.u64("rtcFrames")
	smem := r.take("smem", 4)
	areaCode := r.u8("areaCode")
	intbackActive := r.flag("intbackActive")
	intbackP1MD := r.u8("intbackP1MD")
	intbackP2MD := r.u8("intbackP2MD")
	sshEnabled := r.flag("sshEnabled")
	soundEnabled := r.flag("soundEnabled")
	cdEnabled := r.flag("cdEnabled")
	resetEnabled := r.flag("resetEnabled")
	dotsel := r.flag("dotsel")
	cmdDelay := r.i64("cmdDelay")

	// pendingOps: u32 count, then per op cont u8, val u8, ireg[7].
	var pendingOps []smpcOp
	if pb := r.take("pendingOps", -1); r.err == nil {
		if len(pb) < 4 {
			r.fail("field pendingOps truncated")
		} else {
			count := int(binary.BigEndian.Uint32(pb))
			if len(pb) != 4+count*9 {
				r.fail("field pendingOps size %d for count %d", len(pb), count)
			} else {
				pendingOps = make([]smpcOp, count)
				for i := range pendingOps {
					rec := pb[4+i*9:]
					if rec[0] > 1 {
						r.fail("field pendingOps[%d] bool value %d", i, rec[0])
						break
					}
					pendingOps[i].cont = rec[0] == 1
					pendingOps[i].val = rec[1]
					copy(pendingOps[i].ireg[:], rec[2:9])
				}
			}
		}
	}
	return func() {
		s := e.smpc
		s.comreg = comreg
		s.sr = sr
		s.sf = sf
		copy(s.ireg[:], ireg)
		copy(s.cmdIREG[:], cmdIREG)
		copy(s.oreg[:], oreg)
		s.pdr1 = pdr1
		s.pdr2 = pdr2
		s.ddr1 = ddr1
		s.ddr2 = ddr2
		s.iosel = iosel
		s.exle = exle
		copy(s.rtc[:], rtc)
		s.rtcBaseTime = time.Unix(0, rtcBaseTime)
		s.rtcFrames = rtcFrames
		copy(s.smem[:], smem)
		s.areaCode = areaCode
		s.intbackActive = intbackActive
		s.intbackP1MD = intbackP1MD
		s.intbackP2MD = intbackP2MD
		s.sshEnabled = sshEnabled
		s.soundEnabled = soundEnabled
		s.cdEnabled = cdEnabled
		s.resetEnabled = resetEnabled
		s.dotsel = dotsel
		s.cmdDelay = int(cmdDelay)
		s.pendingOps = pendingOps
	}
}

func decodeCDState(e *Emulator, r *fieldReader) func() {
	hirqReq := r.u16("hirqReq")
	hirqMask := r.u16("hirqMask")
	cmd := r.u16s("cmd", 4)
	res := r.u16s("res", 4)
	var dataBuf []uint16
	if d := r.take("dataBuf", -1); r.err == nil {
		if len(d)%2 != 0 {
			r.fail("field dataBuf odd size %d", len(d))
		} else if len(d) > 0 {
			dataBuf = make([]uint16, len(d)/2)
			for i := range dataBuf {
				dataBuf[i] = binary.BigEndian.Uint16(d[i*2:])
			}
		}
	}
	dataPos := r.iRange("dataPos", 0, int64(len(dataBuf)))
	dataTransferType := r.u8("dataTransferType")
	delPart := r.iRange("delPart", -1, 23)
	delStart := r.i64("delStart")
	delCount := r.i64("delCount")
	status := r.u8("status")
	cdDeviceFilter := r.u8("cdDeviceFilter")
	lastBufferDest := r.u8("lastBufferDest")
	calcSize := r.u32("calcSize")
	playFAD := r.u32("playFAD")
	startFAD := r.u32("startFAD")
	endFAD := r.u32("endFAD")
	curTrack := r.u8("curTrack")
	repeatCount := r.u8("repeatCount")
	playing := r.flag("playing")
	fileRead := r.flag("fileRead")
	getSectorLen := r.i64("getSectorLen")
	putSectorLen := r.i64("putSectorLen")
	putBufData := r.take("putBuf", -1)
	putBufNum := r.u8("putBufNum")
	putSectorsRemaining := r.i64("putSectorsRemaining")
	putWords := r.u32("putWords")
	authenticated := r.flag("authenticated")
	mpegCardAuth := r.flag("mpegCardAuth")
	peri := r.flag("peri")
	transferActive := r.flag("transferActive")
	resultsRead := r.flag("resultsRead")
	initialized := r.flag("initialized")
	cdSpeed := r.u8("cdSpeed")
	bootDelay := r.i64("bootDelay")
	sectorAccum := r.i64("sectorAccum")
	seekAccum := r.i64("seekAccum")
	cmdDelay := r.i64("cmdDelay")
	seeking := r.flag("seeking")
	seekFAD := r.u32("seekFAD")
	seekTarget := r.u8("seekTarget")
	busyDelay := r.i64("busyDelay")
	pendingStatus := r.u8("pendingStatus")
	scdqCounter := r.i64("scdqCounter")
	periCounter := r.i64("periCounter")
	bufferStall := r.flag("bufferStall")
	return func() {
		cb := e.cdblock
		cb.hirqReq = hirqReq
		cb.hirqMask = hirqMask
		copy(cb.cmd[:], cmd)
		copy(cb.res[:], res)
		cb.dataBuf = dataBuf
		cb.dataPos = dataPos
		cb.dataTransferType = dataTransferType
		if delPart >= 0 {
			cb.delPart = &cb.partitions[delPart]
		} else {
			cb.delPart = nil
		}
		cb.delStart = int(delStart)
		cb.delCount = int(delCount)
		cb.status = status
		cb.cdDeviceFilter = cdDeviceFilter
		cb.lastBufferDest = lastBufferDest
		cb.calcSize = calcSize
		cb.playFAD = playFAD
		cb.startFAD = startFAD
		cb.endFAD = endFAD
		cb.curTrack = curTrack
		cb.repeatCount = repeatCount
		cb.playing = playing
		cb.fileRead = fileRead
		cb.getSectorLen = int(getSectorLen)
		cb.putSectorLen = int(putSectorLen)
		cb.putBuf = append([]byte(nil), putBufData...)
		cb.putBufNum = putBufNum
		cb.putSectorsRemaining = int(putSectorsRemaining)
		cb.putWords = putWords
		cb.authenticated = authenticated
		cb.mpegCardAuth.Store(mpegCardAuth)
		cb.peri = peri
		cb.transferActive = transferActive
		cb.resultsRead = resultsRead
		cb.initialized = initialized
		cb.cdSpeed = cdSpeed
		cb.bootDelay = int(bootDelay)
		cb.sectorAccum = int(sectorAccum)
		cb.seekAccum = int(seekAccum)
		cb.cmdDelay = int(cmdDelay)
		cb.seeking = seeking
		cb.seekFAD = seekFAD
		cb.seekTarget = seekTarget
		cb.busyDelay = int(busyDelay)
		cb.pendingStatus = pendingStatus
		cb.scdqCounter = int(scdqCounter)
		cb.periCounter = int(periCounter)
		cb.bufferStall = bufferStall
	}
}

func decodeCDBuffers(e *Emulator, r *fieldReader) func() {
	mode := r.take("filter.mode", 24)
	fadStart := r.u32s("filter.fadStart", 24)
	fadRange := r.u32s("filter.fadRange", 24)
	fileNum := r.take("filter.fileNum", 24)
	chanNum := r.take("filter.chanNum", 24)
	smmask := r.take("filter.smmask", 24)
	smval := r.take("filter.smval", 24)
	cimask := r.take("filter.cimask", 24)
	cival := r.take("filter.cival", 24)
	trueConn := r.take("filter.trueConn", 24)
	falseConn := r.take("filter.falseConn", 24)

	// partitions composite: per partition u32 sector count, then per
	// sector u32 data length + data, size/userOffset/userSize i64,
	// fad u32, fileNum/chanNum/submode/codinfo/isMode2 u8.
	var parts [24][]bufferedSector
	if pb := r.take("partitions", -1); r.err == nil {
		pp := 0
		total := 0
		for i := 0; i < 24 && r.err == nil; i++ {
			if pp+4 > len(pb) {
				r.fail("field partitions truncated at partition %d", i)
				break
			}
			count := int(binary.BigEndian.Uint32(pb[pp:]))
			pp += 4
			total += count
			if total > cdMaxSectors {
				r.fail("field partitions total sector count %d exceeds %d", total, cdMaxSectors)
				break
			}
			secs := make([]bufferedSector, 0, count)
			for j := 0; j < count; j++ {
				if pp+4 > len(pb) {
					r.fail("field partitions sector header truncated")
					break
				}
				dlen := int(binary.BigEndian.Uint32(pb[pp:]))
				pp += 4
				if dlen > 2448 || pp+dlen+8*3+4+5 > len(pb) {
					r.fail("field partitions sector data length %d invalid", dlen)
					break
				}
				var sec bufferedSector
				sec.data = append([]byte(nil), pb[pp:pp+dlen]...)
				pp += dlen
				sec.size = int(int64(binary.BigEndian.Uint64(pb[pp:])))
				pp += 8
				sec.userOffset = int(int64(binary.BigEndian.Uint64(pb[pp:])))
				pp += 8
				sec.userSize = int(int64(binary.BigEndian.Uint64(pb[pp:])))
				pp += 8
				sec.fad = binary.BigEndian.Uint32(pb[pp:])
				pp += 4
				sec.fileNum = pb[pp]
				sec.chanNum = pb[pp+1]
				sec.submode = pb[pp+2]
				sec.codinfo = pb[pp+3]
				if pb[pp+4] > 1 {
					r.fail("field partitions sector isMode2 value %d", pb[pp+4])
					break
				}
				sec.isMode2 = pb[pp+4] == 1
				pp += 5
				if sec.size != dlen || sec.userOffset < 0 || sec.userSize < 0 ||
					sec.userOffset+sec.userSize > dlen {
					r.fail("field partitions sector geometry invalid (size %d data %d userOffset %d userSize %d)",
						sec.size, dlen, sec.userOffset, sec.userSize)
					break
				}
				secs = append(secs, sec)
			}
			parts[i] = secs
		}
		if r.err == nil && pp != len(pb) {
			r.fail("field partitions has %d trailing bytes", len(pb)-pp)
		}
	}
	return func() {
		cb := e.cdblock
		for i := range cb.filters {
			f := &cb.filters[i]
			f.mode = mode[i]
			f.fadStart = fadStart[i]
			f.fadRange = fadRange[i]
			f.fileNum = fileNum[i]
			f.chanNum = chanNum[i]
			f.smmask = smmask[i]
			f.smval = smval[i]
			f.cimask = cimask[i]
			f.cival = cival[i]
			f.trueConn = trueConn[i]
			f.falseConn = falseConn[i]
		}
		for i := range cb.partitions {
			cb.partitions[i].sectors = parts[i]
		}
	}
}

func decodeCDAudioQ(e *Emulator, r *fieldReader) func() {
	queue := r.i16s("audioQueue", cdAudioQueueMax)
	head := r.iRange("audioHead", 0, cdAudioQueueMax-1)
	tail := r.iRange("audioTail", 0, cdAudioQueueMax-1)
	count := r.iRange("audioCount", 0, cdAudioQueueMax)
	return func() {
		cb := e.cdblock
		copy(cb.audioQueue[:], queue)
		cb.audioHead = head
		cb.audioTail = tail
		cb.audioCount = count
	}
}

func decodeCDMpeg(e *Emulator, r *fieldReader) func() {
	active := r.flag("active")
	intStatus := r.u32("intStatus")
	intMask := r.u32("intMask")
	movieMode := r.u8("movieMode")
	decodeTiming := r.u8("decodeTiming")
	outDest := r.u8("outDest")
	scanMode := r.u8("scanMode")
	playMode := r.u8("playMode")
	tmodA := r.u8("tmodA")
	tmodV := r.u8("tmodV")
	decV := r.u8("decV")
	audioMute := r.u8("audioMute")
	vidPaused := r.flag("vidPaused")
	vidFrozen := r.flag("vidFrozen")
	connMode := r.take("conn.mode", 4)
	connLayer := r.take("conn.layer", 4)
	connBuf := r.take("conn.bufNum", 4)
	strMode := r.take("stream.mode", 4)
	strStm := r.take("stream.stmNum", 4)
	strChn := r.take("stream.chNum", 4)
	dispSwitch := r.u8("dispSwitch")
	dispBank := r.u8("dispBank")
	window := r.u16s("window", 8*3)
	borderColor := r.u16("borderColor")
	fade := r.u16("fade")
	videoEffect := r.u16("videoEffect")
	lsi := r.u16s("lsi", 2*128)
	picW := r.u16("picW")
	picH := r.u16("picH")
	vidState := r.u8("vidState")
	audState := r.u8("audState")
	vidStatus := r.u16("vidStatus")
	audStatus := r.u8("audStatus")
	vsyncCounter := r.u16("vsyncCounter")
	vsyncAccum := r.i64("vsyncAccum")

	playActive := r.flag("playActive")
	fed := r.i64s("layer.fed", 2)
	phase := r.take("layer.phase", 2)
	if r.err == nil {
		for i, p := range phase {
			if p > uint8(mpegPhaseHalted) {
				r.fail("field layer.phase[%d] value %d", i, p)
				break
			}
		}
		for i, f := range fed {
			if f < 0 {
				r.fail("field layer.fed[%d] negative", i)
				break
			}
		}
	}
	// layer.endScan was the pre-positional-scanner end-code matcher
	// state; consumed so states carrying it still load (its scanning
	// position is not recoverable, matching the resync restore).
	r.i64s("layer.endScan", 2)
	esScan := r.i64s("layer.esScan", 2)
	packSkip := r.i64s("layer.packSkip", 2)
	packCode := r.i64s("layer.packCode", 2)
	packLen := r.i64s("layer.packLen", 2)
	packLenN := r.i64s("layer.packLenN", 2)
	psEnded := r.flags("layer.psEnded", 2)
	esEnded := r.flags("layer.esEnded", 2)
	esProbed := r.flags("layer.esProbed", 2)
	esDirect := r.flags("layer.esDirect", 2)
	pictAccum := r.i64("pictAccum")
	pictCycles := r.i64("pictCycles")
	hostSteps := r.i64("hostSteps")
	firstVidPTS := r.u64("firstVidPTS")
	firstAudPTS := r.u64("firstAudPTS")
	audStarted := r.flag("audStarted")
	vidStarted := r.flag("vidStarted")
	vidHoldCyc := r.i64("vidHoldCyc")

	// psTail: per layer u32 record count, then per record u32 length +
	// bytes. Records are whole pack-stream writes (sector payloads or
	// restore-seeded blocks), so lengths are bounded. A state without
	// the field restores with no tails: the demuxer re-locks from the
	// partition backlog alone.
	var psTails [2][][]byte
	if tb := r.take("layer.psTail", -1); r.err == nil && len(tb) > 0 {
		tp := 0
		for i := 0; i < 2 && r.err == nil; i++ {
			if tp+4 > len(tb) {
				r.fail("field layer.psTail truncated at layer %d", i)
				break
			}
			count := int(binary.BigEndian.Uint32(tb[tp:]))
			tp += 4
			if count > 1024 {
				r.fail("field layer.psTail record count %d invalid", count)
				break
			}
			recs := make([][]byte, 0, count)
			for j := 0; j < count && r.err == nil; j++ {
				if tp+4 > len(tb) {
					r.fail("field layer.psTail record header truncated")
					break
				}
				rlen := int(binary.BigEndian.Uint32(tb[tp:]))
				tp += 4
				if rlen == 0 || rlen > 4096 || tp+rlen > len(tb) {
					r.fail("field layer.psTail record length %d invalid", rlen)
					break
				}
				recs = append(recs, tb[tp:tp+rlen])
				tp += rlen
			}
			psTails[i] = recs
		}
		if r.err == nil && tp != len(tb) {
			r.fail("field layer.psTail has %d trailing bytes", len(tb)-tp)
		}
	}
	audEsTail := r.take("audEsTail", -1)
	if len(audEsTail) > 1<<20 {
		r.fail("field audEsTail size %d invalid", len(audEsTail))
	}
	// A missing content time must read as -1 (unknown), not as the
	// zero-bits time 0.0: the alignment falls back to the PES capture.
	audResume := -1.0
	if d := r.take("audResumePTS", 8); d != nil {
		audResume = math.Float64frombits(binary.BigEndian.Uint64(d))
	}

	// Latch: per slot u32 w, u32 h, w*h pixels. prod/cons/mailbox slot
	// must be a permutation of {0,1,2} (the latch ownership invariant;
	// an invalid index would read out of the slot array).
	type slotData struct {
		w, h int
		rgb  []uint32
	}
	var slots [3]slotData
	if sb := r.take("latch.slots", -1); r.err == nil {
		sp := 0
		for i := 0; i < 3 && r.err == nil; i++ {
			if sp+8 > len(sb) {
				r.fail("field latch.slots truncated at slot %d", i)
				break
			}
			w := int(binary.BigEndian.Uint32(sb[sp:]))
			h := int(binary.BigEndian.Uint32(sb[sp+4:]))
			sp += 8
			if w > 4096 || h > 4096 || sp+w*h*4 > len(sb) {
				r.fail("field latch.slots slot %d size %dx%d invalid", i, w, h)
				break
			}
			rgb := make([]uint32, w*h)
			for p := range rgb {
				rgb[p] = binary.BigEndian.Uint32(sb[sp:])
				sp += 4
			}
			slots[i] = slotData{w: w, h: h, rgb: rgb}
		}
		if r.err == nil && sp != len(sb) {
			r.fail("field latch.slots has %d trailing bytes", len(sb)-sp)
		}
	}
	mailbox := r.u32("latch.mailbox")
	prod := r.iRange("latch.prod", 0, 2)
	cons := r.iRange("latch.cons", 0, 2)
	consHas := r.flag("latch.consHas")
	dispOn := r.flag("latch.dispOn")
	if r.err == nil {
		mbSlot := int(mailbox & 3)
		if mailbox&^uint32(3|mpegMailboxFresh) != 0 || mbSlot > 2 ||
			mbSlot == prod || mbSlot == cons || prod == cons {
			r.fail("latch slot ownership invalid (mailbox %#x prod %d cons %d)", mailbox, prod, cons)
		}
	}

	return func() {
		cb := e.cdblock
		m := &cb.mpeg
		m.active = active
		m.intStatus = intStatus
		m.intMask = intMask
		m.movieMode = movieMode
		m.decodeTiming = decodeTiming
		m.outDest = outDest
		m.scanMode = scanMode
		m.playMode = playMode
		m.tmodA = tmodA
		m.tmodV = tmodV
		m.decV = decV
		m.audioMute = audioMute
		m.vidPaused = vidPaused
		m.vidFrozen = vidFrozen
		for sel := 0; sel < 2; sel++ {
			for layer := 0; layer < 2; layer++ {
				i := sel*2 + layer
				m.conn[sel][layer] = cdMpegConn{mode: connMode[i], layer: connLayer[i], bufNum: connBuf[i]}
				m.stream[sel][layer] = cdMpegStream{mode: strMode[i], stmNum: strStm[i], chNum: strChn[i]}
			}
		}
		m.dispSwitch = dispSwitch
		m.dispBank = dispBank
		for i := range m.window {
			copy(m.window[i][:], window[i*3:])
		}
		cb.mpegSyncWindowLatch()
		m.borderColor = borderColor
		m.fade = fade
		m.videoEffect = videoEffect
		copy(m.lsi[0][:], lsi[:128])
		copy(m.lsi[1][:], lsi[128:])
		m.picW = picW
		m.picH = picH
		m.vidState = vidState
		m.audState = audState
		m.vidStatus = vidStatus
		m.audStatus = audStatus
		m.vsyncCounter = vsyncCounter
		m.vsyncAccum = int(vsyncAccum)

		// Resync restore: the decoder pipeline is not serialized, so an
		// in-flight playback gets a fresh pipeline that re-feeds from
		// the restored partitions and resyncs at the next sequence/GOP
		// header. Our own fields restore verbatim; SignalEnd is
		// re-issued where the flags say it already happened so an
		// Ending-phase layer can still drain to Done.
		if playActive {
			p := newMpegPlayback()
			for i := range p.layers {
				l := &p.layers[i]
				l.fed = int(fed[i])
				l.phase = mpegLayerPhase(phase[i])
				l.esScan = int(esScan[i])
				l.packScan.skip = int(packSkip[i])
				l.packScan.code = int(packCode[i])
				l.packScan.length = int(packLen[i])
				l.packScan.lenN = int(packLenN[i])
				l.psEnded = psEnded[i]
				l.esEnded = esEnded[i]
				l.esProbed = esProbed[i]
				l.esDirect = esDirect[i]
				// The re-fed pack stream starts mid-mux, past the
				// pack/system headers the demuxer must parse before it
				// will construct, so seed them (see mpegDemuxPreamble)
				// followed by the captured unread records; the audio
				// elementary stream gets its captured tail back the
				// same way. Seeding must precede SignalEnd: the buffer
				// latches its end position there, and a write after it
				// would break the demux end-of-stream drain.
				if l.phase == mpegPhaseRunning || l.phase == mpegPhaseEnding {
					if !l.esDirect {
						// The preamble is written without a record
						// boundary: it is re-seeded on every restore,
						// so capturing it would grow the state on each
						// save/restore cycle.
						l.ps.Write(mpegDemuxPreamble)
						l.psWritten += len(mpegDemuxPreamble)
						for _, rec := range psTails[i] {
							l.psWrite(rec)
						}
					}
					if i == mpegLayerAudio && len(audEsTail) > 0 {
						l.es.Write(audEsTail)
						p.audEsWritten = len(audEsTail)
					}
				}
				if i == mpegLayerVideo &&
					(l.phase == mpegPhaseRunning || l.phase == mpegPhaseEnding) {
					// The decoder re-enters the stream at a GOP whose
					// leading B-pictures may reference the anchor
					// before the entry point; gate them off the display
					// (see mpegPicScan).
					l.resyncPics = &mpegPicScan{}
				}
				if l.psEnded {
					l.ps.SignalEnd()
				}
				if l.esEnded {
					l.es.SignalEnd()
				}
			}
			p.pictAccum = int(pictAccum)
			p.pictCycles = int(pictCycles)
			p.hostSteps = int(hostSteps)
			p.firstVidPTS = math.Float64frombits(firstVidPTS)
			p.firstAudPTS = math.Float64frombits(firstAudPTS)
			p.audStarted = audStarted
			p.vidStarted = vidStarted
			p.vidHoldCyc = int(vidHoldCyc)
			// Seed the audio content clock at the restored tail's head
			// so a save taken before alignment finishes still maps its
			// read position to a content time.
			if audResume >= 0 {
				p.audEsPTS = append(p.audEsPTS, mpegEsPTS{off: 0, pts: audResume})
			}
			// Restored audio resumes at the save point while video can
			// only restart at a sequence header, so align them (see
			// the resync fields on mpegPlayback). Only a steady-state
			// A/V-synchronized playback is aligned, and the restart
			// PTS capture needs the PS demux path on both layers.
			if playMode == 0 && audStarted && vidStarted &&
				p.layers[mpegLayerAudio].phase == mpegPhaseRunning &&
				p.layers[mpegLayerVideo].phase == mpegPhaseRunning &&
				!p.layers[mpegLayerVideo].esDirect {
				p.resyncHold = true
				p.resyncVidPTS = -1
				p.resyncAudPTS = audResume
			}
			m.play = p
		} else {
			m.play = nil
		}

		l := &cb.mpegFrame
		for i := range l.slots {
			l.slots[i].w = slots[i].w
			l.slots[i].h = slots[i].h
			l.slots[i].rgb = slots[i].rgb
		}
		l.mailbox.Store(mailbox)
		l.prod = prod
		l.cons = cons
		l.consHas = consHas
		l.dispOn.Store(dispOn)
	}
}

func decodeBusMisc(e *Emulator, r *fieldReader) func() {
	minitWritten := r.flag("minitWritten")
	sinitWritten := r.flag("sinitWritten")
	cdDataTRNSCache := r.u16("cdDataTRNSCache")
	return func() {
		e.bus.minitWritten = minitWritten
		e.bus.sinitWritten = sinitWritten
		e.bus.cdDataTRNSCache = cdDataTRNSCache
	}
}

func decodeEmu(e *Emulator, r *fieldReader) func() {
	pendingSystemReset := r.flag("pendingSystemReset")
	pendingMasterNMI := r.flag("pendingMasterNMI")
	lineWidthAccum := r.u64("lineWidthAccum")
	lastTrueClock := r.u32("lastTrueClock")
	lastD := r.u32("lastD")
	frameStart := r.u64("frameStart")
	return func() {
		e.pendingSystemReset.Store(pendingSystemReset)
		e.pendingMasterNMI.Store(pendingMasterNMI)
		e.lineWidthAccum = lineWidthAccum
		e.lastTrueClock = lastTrueClock
		e.lastD = lastD
		e.frameStart = frameStart
	}
}

// Deserialize restores emulator state from a file produced by
// Serialize. Must be called at a frame boundary (between RunFrame
// calls) with the workers parked, the same constraint as Serialize,
// and with the same disc attached and the same BIOS loaded (both
// enforced via the header).
//
// Validation is strict and runs before any mutation: header gates
// (version window, BIOS hash, gameID, CRC), framing, exact chunk and
// field sets, and range checks on every value used as an index after
// restore. Most errors are caught in this pre-mutation phase, but an
// m68k blob whose internal format check fails is detected during apply,
// after other chunks have been restored. On any error the emulator may
// be left partially modified and is in an inconsistent state: the
// caller must not continue running it and should discard it or reload a
// known-good state.
func (e *Emulator) Deserialize(data []byte) error {
	pos := 0
	if len(data) < len(stateMagic)+4+1 {
		return errors.New("savestate: file too short")
	}
	if string(data[:len(stateMagic)]) != stateMagic {
		return errors.New("savestate: not an erings save state (bad magic)")
	}
	pos += len(stateMagic)
	version := binary.BigEndian.Uint32(data[pos:])
	pos += 4
	if version > stateVersion {
		return fmt.Errorf("savestate: state version %d is from a newer erings (max supported %d)", version, stateVersion)
	}
	if version < stateMinVersion {
		return fmt.Errorf("savestate: state version %d is too old (minimum supported %d)", version, stateMinVersion)
	}
	idLen := int(data[pos])
	pos++
	if pos+idLen+sha256.Size+4 > len(data) {
		return errors.New("savestate: header truncated")
	}
	gameID := string(data[pos : pos+idLen])
	pos += idLen
	var biosHash [sha256.Size]byte
	copy(biosHash[:], data[pos:])
	pos += sha256.Size
	dataCRC := binary.BigEndian.Uint32(data[pos:])
	pos += 4

	if want := e.stateGameID(); gameID != want {
		return fmt.Errorf("savestate: state is for %q, loaded disc is %q", gameID, want)
	}
	if biosHash == [sha256.Size]byte{} {
		if e.hasBIOS {
			return errors.New("savestate: HLE state cannot restore onto a real BIOS")
		}
	} else {
		if !e.hasBIOS {
			return errors.New("savestate: state requires a real BIOS but none is loaded")
		}
		if got := sha256.Sum256(e.bus.bios); got != biosHash {
			return errors.New("savestate: loaded BIOS does not match the state's BIOS")
		}
	}

	payload := data[pos:]
	if crc32.ChecksumIEEE(payload) != dataCRC {
		return errors.New("savestate: data CRC mismatch (file corrupt)")
	}
	body, err := decompressStateBody(payload)
	if err != nil {
		return err
	}
	chunks, err := parseStateChunks(body)
	if err != nil {
		return err
	}

	var mState, sState sh2.State
	var m68kErr error
	decoders := []struct {
		tag string
		dec func(*fieldReader) func()
	}{
		{tagWRAMH, func(r *fieldReader) func() { return decodeMem(r, e.bus.wramH) }},
		{tagWRAML, func(r *fieldReader) func() { return decodeMem(r, e.bus.wramL) }},
		{tagBackup, func(r *fieldReader) func() { return decodeMem(r, e.bus.backup) }},
		{tagExtRAM, func(r *fieldReader) func() { return decodeMem(r, e.bus.extRAM) }},
		{tagVDP1VRAM, func(r *fieldReader) func() { return decodeMem(r, e.vdp1.vram) }},
		{tagVDP1DrawFB, func(r *fieldReader) func() { return decodeMem(r, e.vdp1.drawFB) }},
		{tagVDP1DispFB, func(r *fieldReader) func() { return decodeMem(r, e.vdp1.displayFB) }},
		{tagVDP2VRAM, func(r *fieldReader) func() { return decodeMem(r, e.vdp2.vram) }},
		{tagVDP2CRAM, func(r *fieldReader) func() { return decodeMem(r, e.vdp2.cram) }},
		{tagSoundRAM, func(r *fieldReader) func() { return decodeMem(r, e.scsp.ram) }},
		{tagSH2MCore, func(r *fieldReader) func() { decodeSH2Core(r, &mState); return func() {} }},
		{tagSH2MCache, func(r *fieldReader) func() { decodeSH2Cache(r, &mState); return func() {} }},
		{tagSH2SCore, func(r *fieldReader) func() { decodeSH2Core(r, &sState); return func() {} }},
		{tagSH2SCache, func(r *fieldReader) func() { decodeSH2Cache(r, &sState); return func() {} }},
		{tagSCUCore, func(r *fieldReader) func() { return decodeSCUCore(e, r) }},
		{tagSCUDSP, func(r *fieldReader) func() { return decodeSCUDSP(e, r) }},
		{tagSCSPRegs, func(r *fieldReader) func() { return decodeSCSPRegs(e, r) }},
		{tagSCSPSlots, func(r *fieldReader) func() { return decodeSCSPSlots(e, r) }},
		{tagSCSPDSP, func(r *fieldReader) func() { return decodeSCSPDSP(e, r) }},
		{tagSCSPTimers, func(r *fieldReader) func() { return decodeSCSPTimers(e, r) }},
		{tagSCSPMisc, func(r *fieldReader) func() { return decodeSCSPMisc(e, r) }},
		{tagM68K, func(r *fieldReader) func() { return decodeM68K(e, r, &m68kErr) }},
		{tagVDP1Regs, func(r *fieldReader) func() { return decodeVDP1Regs(e, r) }},
		{tagVDP1Resume, func(r *fieldReader) func() { return decodeVDP1Resume(e, r) }},
		{tagVDP2State, func(r *fieldReader) func() { return decodeVDP2State(e, r) }},
		{tagSMPC, func(r *fieldReader) func() { return decodeSMPC(e, r) }},
		{tagCDState, func(r *fieldReader) func() { return decodeCDState(e, r) }},
		{tagCDBuffers, func(r *fieldReader) func() { return decodeCDBuffers(e, r) }},
		{tagCDAudioQ, func(r *fieldReader) func() { return decodeCDAudioQ(e, r) }},
		{tagCDMpeg, func(r *fieldReader) func() { return decodeCDMpeg(e, r) }},
		{tagBusMisc, func(r *fieldReader) func() { return decodeBusMisc(e, r) }},
		{tagEmu, func(r *fieldReader) func() { return decodeEmu(e, r) }},
		// IPIMAGE is capture-only (identity/debug); accepted, not restored.
		{tagIPImage, func(r *fieldReader) func() { r.take("data", -1); return func() {} }},
	}

	appliers := make([]func(), 0, len(decoders)+2)
	for _, d := range decoders {
		fields, ok := chunks[d.tag]
		if !ok {
			return fmt.Errorf("savestate: missing chunk %s", d.tag)
		}
		r := newFieldReader(d.tag, fields)
		apply := d.dec(r)
		if err := r.finish(); err != nil {
			return err
		}
		appliers = append(appliers, apply)
		delete(chunks, d.tag)
	}
	for tag := range chunks {
		return fmt.Errorf("savestate: unknown chunk %s", tag)
	}
	appliers = append(appliers,
		func() { e.master.SetState(&mState) },
		func() { e.slave.SetState(&sState) },
	)

	for _, apply := range appliers {
		apply()
	}
	if m68kErr != nil {
		return m68kErr
	}

	// Rebuild the SKIP-DERIVED caches that are otherwise refreshed by
	// the writes that just bypassed them: VDP2 mode-derived timing and
	// the CRAM/line-state caches, and the CD play-track type cache
	// (zero range never matches, so the next tick re-derives it).
	e.vdp2.recalcTiming()
	e.vdp2.regsDirty = true
	e.vdp2.cramCacheValid = false
	e.cdblock.playTypeFADLow = 0
	e.cdblock.playTypeFADHigh = 0
	return nil
}
