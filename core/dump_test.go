// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/binary"
	"testing"
)

// TestDumpVDP2Regs verifies the VDP2 register dump is the full register
// file serialized big-endian, with each register at its hardware byte
// offset (index N at offset N*2).
func TestDumpVDP2Regs(t *testing.T) {
	e := NewEmulator()
	e.vdp2.regs[vdp2BGON] = 0x1234
	e.vdp2.regs[vdp2PLSZ] = 0x00AB

	dump := e.DumpMemory()
	if len(dump.VDP2Regs) != vdp2RegCount*2 {
		t.Fatalf("VDP2Regs length = %d, want %d", len(dump.VDP2Regs), vdp2RegCount*2)
	}
	if got := binary.BigEndian.Uint16(dump.VDP2Regs[vdp2BGON*2:]); got != 0x1234 {
		t.Errorf("BGON at offset 0x%02X = 0x%04X, want 0x1234", vdp2BGON*2, got)
	}
	if got := binary.BigEndian.Uint16(dump.VDP2Regs[vdp2PLSZ*2:]); got != 0x00AB {
		t.Errorf("PLSZ at offset 0x%02X = 0x%04X, want 0x00AB", vdp2PLSZ*2, got)
	}
}

// TestDumpVDP1Regs verifies the VDP1 register block carries the internal
// written (active) values at their hardware offsets, including write-only
// registers that read back as 0 on hardware.
func TestDumpVDP1Regs(t *testing.T) {
	e := NewEmulator()
	e.vdp1.tvmr = 0x0001
	e.vdp1.fbcr = 0x0002
	e.vdp1.ewlr = 0x0102
	e.vdp1.ewrr = 0xABCD
	e.vdp1.copr = 0x0040

	dump := e.DumpMemory()
	if len(dump.VDP1Regs) != 0x18 {
		t.Fatalf("VDP1Regs length = %d, want 24", len(dump.VDP1Regs))
	}
	cases := []struct {
		off  int
		want uint16
		name string
	}{
		{0x00, 0x0001, "TVMR"},
		{0x02, 0x0002, "FBCR"},
		{0x08, 0x0102, "EWLR"},
		{0x0A, 0xABCD, "EWRR"},
		{0x14, 0x0040, "COPR"},
	}
	for _, c := range cases {
		if got := binary.BigEndian.Uint16(dump.VDP1Regs[c.off:]); got != c.want {
			t.Errorf("%s at offset 0x%02X = 0x%04X, want 0x%04X", c.name, c.off, got, c.want)
		}
	}
	// MODR (0x16) is the built status value, not a raw field.
	if got := binary.BigEndian.Uint16(dump.VDP1Regs[0x16:]); got != e.vdp1.buildMODR() {
		t.Errorf("MODR at offset 0x16 = 0x%04X, want 0x%04X", got, e.vdp1.buildMODR())
	}
}

// TestDumpVDP1Shadow verifies the deferred/pending latch values are
// serialized in the documented order: fbcr, ptmr, ewdr, ewlr, ewrr.
func TestDumpVDP1Shadow(t *testing.T) {
	e := NewEmulator()
	e.vdp1.fbcrPending = 0x0010
	e.vdp1.ptmrPending = 0x0002
	e.vdp1.ewdrPending = 0x5555
	e.vdp1.ewlrPending = 0x0203
	e.vdp1.ewrrPending = 0x1234

	dump := e.DumpMemory()
	if len(dump.VDP1Shadow) != 10 {
		t.Fatalf("VDP1Shadow length = %d, want 10", len(dump.VDP1Shadow))
	}
	want := []uint16{0x0010, 0x0002, 0x5555, 0x0203, 0x1234}
	for i, w := range want {
		if got := binary.BigEndian.Uint16(dump.VDP1Shadow[i*2:]); got != w {
			t.Errorf("shadow[%d] = 0x%04X, want 0x%04X", i, got, w)
		}
	}
}

// TestDumpSCURegs verifies the SCU register block places each longword at
// its hardware byte offset and carries write-only register values.
func TestDumpSCURegs(t *testing.T) {
	e := NewEmulator()
	e.scu.dmaR[0] = 0x12345678 // offset 0x00
	e.scu.dmaW[1] = 0x0BADF00D // offset 0x24
	e.scu.ims = 0x0000FFFF     // offset 0xA0 (write-only)

	dump := e.DumpMemory()
	if len(dump.SCURegs) != 0xCC {
		t.Fatalf("SCURegs length = %d, want %d", len(dump.SCURegs), 0xCC)
	}
	cases := []struct {
		off  int
		want uint32
		name string
	}{
		{0x00, 0x12345678, "D0R"},
		{0x24, 0x0BADF00D, "D1W"},
		{0xA0, 0x0000FFFF, "IMS"},
	}
	for _, c := range cases {
		if got := binary.BigEndian.Uint32(dump.SCURegs[c.off:]); got != c.want {
			t.Errorf("%s at offset 0x%02X = 0x%08X, want 0x%08X", c.name, c.off, got, c.want)
		}
	}
}

// TestDumpSCUDSP verifies the SCU DSP block leads with program RAM then
// data RAM at their expected positions.
func TestDumpSCUDSP(t *testing.T) {
	e := NewEmulator()
	e.scu.dsp.prog[0] = 0xAABBCCDD
	e.scu.dsp.prog[255] = 0x11223344
	e.scu.dsp.data[2][1] = 0x55667788

	dump := e.DumpMemory()
	if got := binary.BigEndian.Uint32(dump.SCUDSP[0:]); got != 0xAABBCCDD {
		t.Errorf("prog[0] = 0x%08X, want 0xAABBCCDD", got)
	}
	if got := binary.BigEndian.Uint32(dump.SCUDSP[255*4:]); got != 0x11223344 {
		t.Errorf("prog[255] = 0x%08X, want 0x11223344", got)
	}
	// data RAM follows the 256-word program RAM; bank 2 word 1.
	dataOff := 256*4 + (2*64+1)*4
	if got := binary.BigEndian.Uint32(dump.SCUDSP[dataOff:]); got != 0x55667788 {
		t.Errorf("data[2][1] = 0x%08X, want 0x55667788", got)
	}
}

// TestDumpSCSPRegs verifies the SCSP register file is the full flat array
// serialized big-endian.
func TestDumpSCSPRegs(t *testing.T) {
	e := NewEmulator()
	e.scsp.regs[0x408/2] = 0xBEEF // MSLC common register

	dump := e.DumpMemory()
	if len(dump.SCSPRegs) != scspRegWords*2 {
		t.Fatalf("SCSPRegs length = %d, want %d", len(dump.SCSPRegs), scspRegWords*2)
	}
	if got := binary.BigEndian.Uint16(dump.SCSPRegs[0x408:]); got != 0xBEEF {
		t.Errorf("reg at 0x408 = 0x%04X, want 0xBEEF", got)
	}
}

// TestDumpSCSPSlots verifies per-slot runtime state lands in the dump at
// the documented stride, including the dormant-slot diagnosis fields.
func TestDumpSCSPSlots(t *testing.T) {
	e := NewEmulator()
	e.scsp.slots[5].phase = 0xCAFEBABE
	e.scsp.slots[5].egState = 3
	e.scsp.slots[5].active = true

	dump := e.DumpMemory()
	const stride = 27 // phase4 egLevel4 egState1 egStep4 egTarget4 active1 finished1 loopDir1 output2 lfoPhase1 lfoStep2 lfoNoise2
	if len(dump.SCSPSlots) != 32*stride {
		t.Fatalf("SCSPSlots length = %d, want %d", len(dump.SCSPSlots), 32*stride)
	}
	base := 5 * stride
	if got := binary.BigEndian.Uint32(dump.SCSPSlots[base:]); got != 0xCAFEBABE {
		t.Errorf("slot5.phase = 0x%08X, want 0xCAFEBABE", got)
	}
	// egState is at offset 8 within the slot (after phase u32, egLevel i32).
	if got := dump.SCSPSlots[base+8]; got != 3 {
		t.Errorf("slot5.egState = %d, want 3", got)
	}
	// active is at offset 17 (phase4 egLevel4 egState1 egStep4 egTarget4).
	if got := dump.SCSPSlots[base+17]; got != 1 {
		t.Errorf("slot5.active = %d, want 1", got)
	}
}

// TestDumpSH2Regs verifies the SH-2 register block layout for both cores.
func TestDumpSH2Regs(t *testing.T) {
	e := NewEmulator()
	e.master.SetReg(0, 0xDEADBEEF)
	e.master.SetPC(0x06004000)
	e.slave.SetPC(0x20000200)

	dump := e.DumpMemory()
	wantLen := 16*4 + 7*4 + 8 + 1
	if len(dump.SH2MasterRegs) != wantLen {
		t.Fatalf("SH2MasterRegs length = %d, want %d", len(dump.SH2MasterRegs), wantLen)
	}
	if got := binary.BigEndian.Uint32(dump.SH2MasterRegs[0:]); got != 0xDEADBEEF {
		t.Errorf("master R0 = 0x%08X, want 0xDEADBEEF", got)
	}
	// PC follows R0..R15 (16 longwords).
	if got := binary.BigEndian.Uint32(dump.SH2MasterRegs[64:]); got != 0x06004000 {
		t.Errorf("master PC = 0x%08X, want 0x06004000", got)
	}
	if got := binary.BigEndian.Uint32(dump.SH2SlaveRegs[64:]); got != 0x20000200 {
		t.Errorf("slave PC = 0x%08X, want 0x20000200", got)
	}
}
