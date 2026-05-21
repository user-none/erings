// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// runDSP drains an armed DSP program by ticking the SCU at a healthy
// chunk size until execution stops. Fails the test if the program
// refuses to terminate within a generous budget.
func runDSP(t *testing.T, s *SCU) {
	t.Helper()
	for i := 0; i < 10000 && s.dsp.executing; i++ {
		s.TickSystemCycles(1000)
	}
	if s.dsp.executing {
		t.Fatal("DSP program did not terminate within budget")
	}
}

// --- Register Port Tests ---

func TestDSPPPDWriteLoadsProgramRAM(t *testing.T) {
	s := NewSCU()

	// PC starts at 0. Write two instructions.
	s.dsp.writePPD(0xDEADBEEF)
	if s.dsp.prog[0] != 0xDEADBEEF {
		t.Errorf("prog[0] = 0x%08X, want 0xDEADBEEF", s.dsp.prog[0])
	}
	if s.dsp.pc != 1 {
		t.Errorf("pc = %d, want 1", s.dsp.pc)
	}

	s.dsp.writePPD(0xCAFEBABE)
	if s.dsp.prog[1] != 0xCAFEBABE {
		t.Errorf("prog[1] = 0x%08X, want 0xCAFEBABE", s.dsp.prog[1])
	}
	if s.dsp.pc != 2 {
		t.Errorf("pc = %d, want 2", s.dsp.pc)
	}
}

func TestDSPPDAWriteSetsPDDAddress(t *testing.T) {
	s := NewSCU()

	// Write data via PDA+PDD
	s.dsp.writePDA(0x00) // Bank 0, addr 0
	s.dsp.writePDD(0x12345678)
	if s.dsp.data[0][0] != 0x12345678 {
		t.Errorf("data[0][0] = 0x%08X, want 0x12345678", s.dsp.data[0][0])
	}

	// pdaAddr auto-increments
	s.dsp.writePDD(0xAAAABBBB)
	if s.dsp.data[0][1] != 0xAAAABBBB {
		t.Errorf("data[0][1] = 0x%08X, want 0xAAAABBBB", s.dsp.data[0][1])
	}

	// Bank 2 (bits 7:6 = 10 = 0x80)
	s.dsp.writePDA(0x80)
	s.dsp.writePDD(0x55555555)
	if s.dsp.data[2][0] != 0x55555555 {
		t.Errorf("data[2][0] = 0x%08X, want 0x55555555", s.dsp.data[2][0])
	}
}

func TestDSPPDDReadReturnsDataRAM(t *testing.T) {
	s := NewSCU()

	s.dsp.data[1][5] = 0xFEEDFACE
	s.dsp.writePDA(0x45) // Bank 1 (01), addr 5
	val := s.dsp.readPDD()
	if val != 0xFEEDFACE {
		t.Errorf("readPDD = 0x%08X, want 0xFEEDFACE", val)
	}
	// pdaAddr should have incremented to 0x46
	if s.dsp.pdaAddr != 0x46 {
		t.Errorf("pdaAddr = 0x%02X, want 0x46", s.dsp.pdaAddr)
	}
}

func TestDSPPPAFReadFlagLayout(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.pc = 0x42
	d.flagT0 = true
	d.flagS = true
	d.flagZ = true
	d.flagC = true
	d.flagV = true
	d.flagEnd = true

	val := d.readPPAF()

	if val&0xFF != 0x42 {
		t.Errorf("PC = 0x%02X, want 0x42", val&0xFF)
	}
	if val&(1<<23) == 0 {
		t.Error("T0 flag should be set")
	}
	if val&(1<<22) == 0 {
		t.Error("S flag should be set")
	}
	if val&(1<<21) == 0 {
		t.Error("Z flag should be set")
	}
	if val&(1<<20) == 0 {
		t.Error("C flag should be set")
	}
	if val&(1<<19) == 0 {
		t.Error("V flag should be set")
	}
	if val&(1<<18) == 0 {
		t.Error("E flag should be set")
	}
}

func TestDSPPPAFReadClearsVAndE(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.flagV = true
	d.flagEnd = true

	d.readPPAF()

	if d.flagV {
		t.Error("V flag should be cleared after PPAF read")
	}
	if d.flagEnd {
		t.Error("E flag should be cleared after PPAF read")
	}
}

func TestDSPPPAFWriteLELoadsPC(t *testing.T) {
	s := NewSCU()

	// LE bit (15) + PC value 0x10
	// Also write a NOP at address 0x10 so exec doesn't run forever
	s.dsp.prog[0x10] = 0xF0000000 // END
	s.dsp.writePPAF((1 << 15) | 0x10)

	if s.dsp.pc != 0x10 {
		t.Errorf("pc = 0x%02X, want 0x10", s.dsp.pc)
	}
}

func TestDSPWritePPDIgnoredWhileExecuting(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.executing = true
	d.pc = 5
	d.writePPD(0x12345678)

	if d.prog[5] != 0 {
		t.Error("PPD write should be ignored while executing")
	}
	if d.pc != 5 {
		t.Error("PC should not change from PPD write while executing")
	}
	d.executing = false
}

func TestDSPWritePDDIgnoredWhileExecuting(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.executing = true
	d.writePDA(0x00)
	d.writePDD(0xAAAAAAAA)

	if d.data[0][0] != 0 {
		t.Error("PDD write should be ignored while executing")
	}
	d.executing = false
}

// --- ALU Tests ---

func TestDSPALUAnd(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0xFF00FF00
	d.pl = 0x0F0F0F0F
	var ach uint16
	acl := d.acl
	d.execALU(1, &ach, &acl)
	if acl != 0x0F000F00 {
		t.Errorf("AND: ACL = 0x%08X, want 0x0F000F00", acl)
	}
	if d.flagC {
		t.Error("AND should clear C flag")
	}
}

func TestDSPALUOr(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0xFF000000
	d.pl = 0x000000FF
	var ach uint16
	acl := d.acl
	d.execALU(2, &ach, &acl)
	if acl != 0xFF0000FF {
		t.Errorf("OR: ACL = 0x%08X, want 0xFF0000FF", acl)
	}
}

func TestDSPALUXor(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0xAAAAAAAA
	d.pl = 0x55555555
	var ach uint16
	acl := d.acl
	d.execALU(3, &ach, &acl)
	if acl != 0xFFFFFFFF {
		t.Errorf("XOR: ACL = 0x%08X, want 0xFFFFFFFF", acl)
	}
}

func TestDSPALUAdd(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x00000005
	d.pl = 0x00000003
	var ach uint16
	acl := d.acl
	d.execALU(4, &ach, &acl)
	if acl != 0x00000008 {
		t.Errorf("ADD: ACL = 0x%08X, want 0x00000008", acl)
	}
	if d.flagZ {
		t.Error("Z should not be set")
	}
	if d.flagC {
		t.Error("C should not be set")
	}
}

func TestDSPALUAddCarry(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0xFFFFFFFF
	d.pl = 0x00000002
	var ach uint16
	acl := d.acl
	d.execALU(4, &ach, &acl)
	if acl != 0x00000001 {
		t.Errorf("ADD carry: ACL = 0x%08X, want 0x00000001", acl)
	}
	if !d.flagC {
		t.Error("C should be set on carry")
	}
}

func TestDSPALUAddOverflow(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x7FFFFFFF
	d.pl = 0x00000001
	var ach uint16
	acl := d.acl
	d.execALU(4, &ach, &acl)
	if acl != 0x80000000 {
		t.Errorf("ADD overflow: ACL = 0x%08X, want 0x80000000", acl)
	}
	if !d.flagV {
		t.Error("V should be set on signed overflow")
	}
}

func TestDSPALUSub(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x00000005
	d.pl = 0x00000003
	var ach uint16
	acl := d.acl
	d.execALU(5, &ach, &acl)
	if acl != 0x00000002 {
		t.Errorf("SUB: ACL = 0x%08X, want 0x00000002", acl)
	}
}

func TestDSPALUSubBorrow(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x00000000
	d.pl = 0x00000001
	var ach uint16
	acl := d.acl
	d.execALU(5, &ach, &acl)
	if acl != 0xFFFFFFFF {
		t.Errorf("SUB borrow: ACL = 0x%08X, want 0xFFFFFFFF", acl)
	}
	if !d.flagC {
		t.Error("C should be set on borrow")
	}
}

func TestDSPALUAD2(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.ach = 0x0001
	d.acl = 0x00000000
	d.ph = 0x0000
	d.pl = 0xFFFFFFFF
	ach := d.ach
	acl := d.acl
	d.execALU(6, &ach, &acl)
	// 0x000100000000 + 0x0000FFFFFFFF = 0x0001FFFFFFFF
	if ach != 0x0001 || acl != 0xFFFFFFFF {
		t.Errorf("AD2: ACH:ACL = %04X:%08X, want 0001:FFFFFFFF", ach, acl)
	}
}

func TestDSPALUAD2Carry(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.ach = 0xFFFF
	d.acl = 0xFFFFFFFF
	d.ph = 0x0000
	d.pl = 0x00000001
	ach := d.ach
	acl := d.acl
	d.execALU(6, &ach, &acl)
	if ach != 0x0000 || acl != 0x00000000 {
		t.Errorf("AD2 carry: ACH:ACL = %04X:%08X, want 0000:00000000", ach, acl)
	}
	if !d.flagC {
		t.Error("C should be set on 48-bit carry")
	}
	if !d.flagZ {
		t.Error("Z should be set on zero result")
	}
}

func TestDSPALUSR(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x80000003
	var ach uint16
	acl := d.acl
	d.execALU(8, &ach, &acl)
	// Arithmetic shift right: MSB preserved
	if acl != 0xC0000001 {
		t.Errorf("SR: ACL = 0x%08X, want 0xC0000001", acl)
	}
	if !d.flagC {
		t.Error("C should be set (bit 0 was 1)")
	}
	if !d.flagS {
		t.Error("S should be set (result is negative)")
	}
}

func TestDSPALURR(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x00000001
	var ach uint16
	acl := d.acl
	d.execALU(9, &ach, &acl)
	if acl != 0x80000000 {
		t.Errorf("RR: ACL = 0x%08X, want 0x80000000", acl)
	}
	if !d.flagC {
		t.Error("C should be set")
	}
}

func TestDSPALUSL(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x80000000
	var ach uint16
	acl := d.acl
	d.execALU(0xA, &ach, &acl)
	if acl != 0x00000000 {
		t.Errorf("SL: ACL = 0x%08X, want 0x00000000", acl)
	}
	if !d.flagC {
		t.Error("C should be set (bit 31 was 1)")
	}
	if !d.flagZ {
		t.Error("Z should be set (result is zero)")
	}
}

func TestDSPALURL(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x80000000
	var ach uint16
	acl := d.acl
	d.execALU(0xB, &ach, &acl)
	if acl != 0x00000001 {
		t.Errorf("RL: ACL = 0x%08X, want 0x00000001", acl)
	}
	if !d.flagC {
		t.Error("C should be set")
	}
}

func TestDSPALURL8(t *testing.T) {
	s := NewSCU()
	d := &s.dsp
	d.acl = 0x12345678
	var ach uint16
	acl := d.acl
	d.execALU(0xF, &ach, &acl)
	if acl != 0x34567812 {
		t.Errorf("RL8: ACL = 0x%08X, want 0x34567812", acl)
	}
	// C = old bit 24 of 0x12345678: (0x12345678 >> 24) & 1 = 0x12 & 1 = 0
	if d.flagC {
		t.Error("C should not be set (bit 24 was 0)")
	}
}

func TestDSPALUVFlagSticky(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	// Cause overflow
	d.acl = 0x7FFFFFFF
	d.pl = 0x00000001
	var ach uint16
	acl := d.acl
	d.execALU(4, &ach, &acl) // ADD overflow
	if !d.flagV {
		t.Fatal("V should be set after overflow")
	}

	// Non-overflow ADD should not clear V
	d.acl = 0x00000001
	d.pl = 0x00000001
	acl = d.acl
	d.execALU(4, &ach, &acl)
	if !d.flagV {
		t.Error("V should remain set (sticky)")
	}
}

// --- Bus Operation Tests ---

func TestDSPXBusMultiply(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.rx = 3
	d.ry = 7

	// Operation: ALU=NOP, X-Bus P-op=MOV MUL,P (bits 24:23=10), rest NOP
	// bits 24:23 = 10 -> xPOp=2
	instr := uint32(0x00) | (2 << 23)
	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)   // Execute
	runDSP(t, s)

	if d.pl != 21 || d.ph != 0 {
		t.Errorf("MUL: PH:PL = %04X:%08X, want 0000:00000015", d.ph, d.pl)
	}
}

func TestDSPXBusMultiplyUsesOldValues(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.rx = 4
	d.ry = 5
	d.data[0][0] = 99 // New RX value
	d.data[1][0] = 88 // New RY value

	// Operation: MUL,P + load RX from RAM0 + load RY from RAM1
	// xPOp=2 (MUL), xSrc=1 (load RX), xRAM=0b100 (RAM0, CT++)
	// ySrc=1 (load RY), yRAM=0b101 (RAM1, CT++)
	instr := uint32(0)
	instr |= 1 << 25       // xSrc=1
	instr |= 2 << 23       // xPOp=2 (MOV MUL,P)
	instr |= 4 << 20       // xRAM=100 (RAM0, CT0++)
	instr |= 1 << 19       // ySrc=1
	instr |= (4 + 1) << 14 // yRAM=101 (RAM1, CT1++)

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	// Multiply should use old RX=4, RY=5 -> PL=20
	if d.pl != 20 {
		t.Errorf("MUL used new values: PL = %d, want 20", d.pl)
	}
	// RX and RY should now have the new values
	if d.rx != 99 {
		t.Errorf("RX = %d, want 99", d.rx)
	}
	if d.ry != 88 {
		t.Errorf("RY = %d, want 88", d.ry)
	}
}

func TestDSPXBusLoadRXWithCTInc(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.data[2][0] = 0x11223344
	d.ct[2] = 0

	// xSrc=1, xRAM=110 (RAM2, CT2++)
	instr := uint32(0)
	instr |= 1 << 25 // xSrc=1
	instr |= 6 << 20 // xRAM=110 (RAM2, inc)

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.rx != 0x11223344 {
		t.Errorf("RX = 0x%08X, want 0x11223344", d.rx)
	}
	if d.ct[2] != 1 {
		t.Errorf("CT2 = %d, want 1", d.ct[2])
	}
}

func TestDSPYBusClearA(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.ach = 0x1234
	d.acl = 0x56789ABC

	// yAOp=1 (CLR A): bits 18:17=01
	instr := uint32(0)
	instr |= 1 << 17

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.ach != 0 || d.acl != 0 {
		t.Errorf("CLR A: ACH:ACL = %04X:%08X, want 0000:00000000", d.ach, d.acl)
	}
}

func TestDSPYBusMovALUToA(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.acl = 0x00000005
	d.pl = 0x00000003

	// ALU=ADD (0100), yAOp=2 (MOV ALU,A): bits 18:17=10
	instr := uint32(0)
	instr |= 4 << 26 // ALU ADD
	instr |= 2 << 17 // MOV ALU,A

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.acl != 0x00000008 {
		t.Errorf("MOV ALU,A: ACL = 0x%08X, want 0x00000008", d.acl)
	}
}

func TestDSPD1BusMovSrcToDst(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.data[0][0] = 0xDEADBEEF
	d.ct[0] = 0
	d.ct[1] = 0

	// D1: MOV [s],[d] -> MOV RAM0[CT0],RAM1[CT1]
	// d1Op=3 (bits 13:12=11), dst=1 (bits 11:8=0001), src=0 (bits 3:0=0000)
	instr := uint32(0)
	instr |= 3 << 12 // d1Op=3
	instr |= 1 << 8  // dst=1 (RAM1)
	instr |= 0       // src=0 (RAM0, no CT inc)

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.data[1][0] != 0xDEADBEEF {
		t.Errorf("D1 MOV: data[1][0] = 0x%08X, want 0xDEADBEEF", d.data[1][0])
	}
}

func TestDSPD1BusSameBankWriteSuppressed(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.data[0][0] = 0x11111111
	d.data[0][1] = 0x22222222
	d.ct[0] = 0

	// X-Bus reads from RAM0 (drRead sets bank 0)
	// D1-Bus tries to write to RAM0 -> should be suppressed
	// xSrc=1, xRAM=0 (RAM0, no inc)
	// d1Op=1 (SImm), dst=0 (RAM0), imm=0x42
	instr := uint32(0)
	instr |= 1 << 25 // xSrc=1 (read from RAM0)
	instr |= 1 << 12 // d1Op=1 (MOV SImm)
	instr |= 0 << 8  // dst=0 (RAM0)
	instr |= 0x42    // imm

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	// RAM0[0] should NOT have been overwritten
	if d.data[0][0] != 0x11111111 {
		t.Errorf("same-bank write was not suppressed: data[0][0] = 0x%08X, want 0x11111111", d.data[0][0])
	}
}

func TestDSPD1BusSrcCTIncSuppressedSameBank(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.data[0][0] = 0xAAAAAAAA
	d.ct[0] = 0

	// D1: MOV MC0(CT++),MC0 -> src=4 (RAM0 with CT++), dst=0 (RAM0)
	// Source read sets drRead for bank 0. Since src bank == dst, source CT
	// increment is suppressed. Then the dst write is also suppressed because
	// drRead has bank 0 set. So CT0 stays at 0.
	instr := uint32(0)
	instr |= 3 << 12 // d1Op=3
	instr |= 0 << 8  // dst=0 (RAM0)
	instr |= 4       // src=4 (RAM0, CT0++)

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	// Both source CT inc (suppressed: src bank == dst) and destination
	// write+CT inc (suppressed: drRead has bank 0) are suppressed.
	if d.ct[0] != 0 {
		t.Errorf("CT0 = %d, want 0 (both CT incs suppressed for same-bank)", d.ct[0])
	}
}

func TestDSPCTWrapAt63(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.ct[0] = 63
	d.data[0][63] = 0xBEEF

	// xSrc=1, xRAM=100 (RAM0, CT0++)
	instr := uint32(0)
	instr |= 1 << 25 // xSrc=1
	instr |= 4 << 20 // xRAM=100

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.rx != 0xBEEF {
		t.Errorf("RX = 0x%08X, want 0x0000BEEF", d.rx)
	}
	if d.ct[0] != 0 {
		t.Errorf("CT0 = %d, want 0 (wrapped from 63)", d.ct[0])
	}
}

// --- MVI Tests ---

func TestDSPMVIUnconditional(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	// MVI #0x42, RX: bits 31:30=10, dest=0100 (RX), bit25=0, imm=0x42
	instr := uint32(0x80000000) | (4 << 26) | 0x42

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.rx != 0x42 {
		t.Errorf("MVI RX: rx = 0x%08X, want 0x00000042", d.rx)
	}
}

func TestDSPMVISignExtend25(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	// MVI #-1 (25-bit), RX: all 25 bits set
	instr := uint32(0x80000000) | (4 << 26) | 0x1FFFFFF

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.rx != 0xFFFFFFFF {
		t.Errorf("MVI sign extend: rx = 0x%08X, want 0xFFFFFFFF", d.rx)
	}
}

func TestDSPMVIConditionalZ(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.flagZ = true

	// MVI Z, #0x10, RX
	// bits 31:30=10, dest=0100 (RX), bit25=1 (conditional)
	// cond bits 24:19 = 100001 (Z: bit5=1, bit0=1) = 0x21 << 19
	// imm = 0x10 (19-bit)
	instr := uint32(0x80000000) | (4 << 26) | (1 << 25) | (0x21 << 19) | 0x10

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.rx != 0x10 {
		t.Errorf("MVI Z (taken): rx = 0x%08X, want 0x10", d.rx)
	}
}

func TestDSPMVIConditionalNZNotTaken(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.flagZ = true
	d.rx = 0xBEEF

	// MVI NZ, #0x10, RX
	// cond bits 24:19 = 000001 (NZ: bit5=0, bit0=1) = 0x01 << 19
	instr := uint32(0x80000000) | (4 << 26) | (1 << 25) | (0x01 << 19) | 0x10

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.rx != 0xBEEF {
		t.Errorf("MVI NZ (not taken): rx = 0x%08X, want 0x0000BEEF", d.rx)
	}
}

func TestDSPMVIToPC(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	// MVI #0x20, PC at address 0
	// dest=0xC (PC), bit25=0 (unconditional), imm=0x20
	instr := uint32(0x80000000) | (0xC << 26) | 0x20

	d.prog[0] = instr
	// Delay slot (address 1) - NOP (will be executed via prefetch)
	d.prog[1] = 0x00000000
	// END at target address 0x20
	d.prog[0x20] = 0xF0000000

	d.writePPAF(1 << 16)
	runDSP(t, s)

	// TOP should be saved as PC-1 at the time of MVI execution.
	// MVI is at prog[0], prefetch advances PC to 2, MVI runs and sets top = pc-1 = 1.
	// Then delay slot at prog[1] executes, then we jump to 0x20.
	if d.top != 1 {
		t.Errorf("MVI PC: top = %d, want 1", d.top)
	}
}

// --- Flow Control Tests ---

func TestDSPJMPUnconditionalWithDelaySlot(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	// prog[0]: MVI #0x42, RX (delay slot marker)
	d.prog[0] = uint32(0x80000000) | (4 << 26) | 0x42
	// prog[1]: JMP unconditional to 0x10
	d.prog[1] = 0xD0000010 // 1101 0000...0001 0000
	// prog[2]: MVI #0x99, RX (delay slot - should execute)
	d.prog[2] = uint32(0x80000000) | (4 << 26) | 0x99
	// prog[0x10]: END
	d.prog[0x10] = 0xF0000000

	d.writePPAF(1 << 16)
	runDSP(t, s)

	// Delay slot (prog[2]) should have executed
	if d.rx != 0x99 {
		t.Errorf("JMP delay slot: rx = 0x%08X, want 0x00000099", d.rx)
	}
}

func TestDSPJMPConditionalNotTaken(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.flagZ = false

	// prog[0]: JMP Z, 0x10 (should not be taken since Z=false)
	// cond = 0x61 (bit6=1 enable, bit5=1 positive, bit0=1 Z)
	d.prog[0] = 0xD0000000 | (0x61 << 19) | 0x10
	// prog[1]: END
	d.prog[1] = 0xF0000000

	d.writePPAF(1 << 16)
	runDSP(t, s)

	// Should have fallen through to END at prog[1], not jumped to 0x10
	// PC after END should be 2 (prefetched prog[2] but discarded)
	// The key test: we didn't hang (would if jumped to 0x10 with no END there)
}

func TestDSPBTMLoop(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	// Set up a simple loop that increments RX
	// prog[0]: MVI #3, LOP
	d.prog[0] = uint32(0x80000000) | (0xA << 26) | 3
	// prog[1]: MVI #0, RX
	d.prog[1] = uint32(0x80000000) | (4 << 26) | 0
	// prog[2]: MVI #1, TOP (save loop start)
	// Actually TOP is set via D1-Bus or directly. Let's use D1-Bus approach.
	// Simpler: set TOP and LOP directly, then loop body + BTM
	d.data[0][0] = 0
	d.ct[0] = 0

	// prog[0]: MVI #3, LOP (sets loop counter to 3)
	d.prog[0] = uint32(0x80000000) | (0xA << 26) | 3
	// prog[1]: D1: MOV SImm(1), MC0 (increment counter in data RAM)
	// d1Op=1, dst=0 (RAM0), imm=1
	d.prog[1] = uint32(1<<12) | (0 << 8) | 1
	// prog[2]: BTM (loop back if LOP > 0)
	d.prog[2] = 0xE0000000
	// prog[3]: END (delay slot for BTM when LOP reaches 0, then falls through)
	d.prog[3] = 0xF0000000

	// Set TOP to 1 (loop body start)
	d.top = 1

	d.writePPAF(1 << 16)
	runDSP(t, s)

	// BTM with LOP=3: decrements LOP and branches when LOP>0
	// Iteration 1: LOP=3>0, branch to TOP=1, LOP becomes 2
	//   delay slot (prog[3]=END but BTM's delay slot is prog[3])
	//   Wait - BTM is at prog[2]. Prefetch fetches prog[3].
	//   BTM sets pc=top=1. Delay slot prog[3] executes (END).
	//   END stops execution.
	// So with delay slot, the loop only runs once before END executes.
	// This test verifies delay slot behavior for BTM.

	// prog[1] wrote 1 to data[0][0], CT0=1
	// BTM branches, delay slot is prog[3] = END -> stops
	if d.data[0][0] != 1 {
		t.Errorf("BTM loop body executed: data[0][0] = %d, want 1", d.data[0][0])
	}
}

func TestDSPLPSLoop(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.ct[0] = 0

	// prog[0]: MVI #4, LOP (loop 5 times total = LOP+1)
	d.prog[0] = uint32(0x80000000) | (0xA << 26) | 4
	// prog[1]: LPS
	d.prog[1] = 0xE8000000
	// prog[2]: D1: MOV SImm(1), MC0 (write 1 to data RAM, CT0 increments)
	d.prog[2] = uint32(1<<12) | (0 << 8) | 1
	// prog[3]: END
	d.prog[3] = 0xF0000000

	d.writePPAF(1 << 16)
	runDSP(t, s)

	// LPS repeats prog[2] LOP+1=5 times. Each writes 1 to data[0][CT0++].
	// CT0 should be 5 after 5 writes.
	if d.ct[0] != 5 {
		t.Errorf("LPS: CT0 = %d, want 5 (LOP+1 iterations)", d.ct[0])
	}
}

func TestDSPENDStops(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.prog[0] = 0xF0000000 // END

	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.executing {
		t.Error("executing should be false after END")
	}
	if d.flagEnd {
		t.Error("E flag should not be set by END (only ENDI)")
	}
}

func TestDSPENDIStopsAndInterrupts(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	var gotVec uint16
	called := false
	s.SetIRLHandler(func(level uint8, vec uint16) {
		gotVec = vec
		called = true
	}, func() {})
	s.ims = s.ims &^ (1 << 5) // Unmask DSP End (bit 5)

	s.dsp.prog[0] = 0xF8000000 // ENDI

	s.dsp.writePPAF(1 << 16)
	runDSP(t, s)

	if s.dsp.executing {
		t.Error("executing should be false after ENDI")
	}
	if !s.dsp.flagEnd {
		t.Error("E flag should be set by ENDI")
	}
	if !called {
		t.Fatal("DSP End interrupt not raised")
	}
	if gotVec != 0x45 {
		t.Errorf("vec = 0x%04X, want 0x0045", gotVec)
	}
}

// --- DMA Tests ---

func TestDSPDMAD0ToRAM(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	// Write test data to system memory
	mb.Write32(0x1000, 0x11111111)
	mb.Write32(0x1004, 0x22222222)
	mb.Write32(0x1008, 0x33333333)

	d := &s.dsp
	d.ra0 = 0x1000 >> 2 // RA0 in longword units
	d.ct[0] = 0

	// DMA D0,[RAM0],3: dir=0, format=0, hold=0, addMode=2 (bit1=1 -> add 4 bytes), ramSel=0, count=3
	// D0-to-RAM only uses bit 1 of addMode for address increment
	instr := uint32(0xC0000000)
	instr |= 2 << 15 // addMode=010 (bit1=1 -> add 4 bytes)
	instr |= 3       // count=3

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.data[0][0] != 0x11111111 {
		t.Errorf("DMA: data[0][0] = 0x%08X, want 0x11111111", d.data[0][0])
	}
	if d.data[0][1] != 0x22222222 {
		t.Errorf("DMA: data[0][1] = 0x%08X, want 0x22222222", d.data[0][1])
	}
	if d.data[0][2] != 0x33333333 {
		t.Errorf("DMA: data[0][2] = 0x%08X, want 0x33333333", d.data[0][2])
	}
}

func TestDSPDMARAMToD0(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	d := &s.dsp
	d.data[1][0] = 0xAAAABBBB
	d.data[1][1] = 0xCCCCDDDD
	d.wa0 = 0x2000 >> 2
	d.ct[1] = 0

	// DMA [RAM1],D0,2: dir=1, format=0, hold=0, addMode=1 (add 4), ramSel=1, count=2
	instr := uint32(0xC0000000)
	instr |= 1 << 15 // addMode=001
	instr |= 1 << 12 // dir=1 (RAM-to-D0)
	instr |= 1 << 8  // ramSel=1 (RAM1)
	instr |= 2       // count=2

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if mb.Read32(0x2000) != 0xAAAABBBB {
		t.Errorf("DMA: mem[0x2000] = 0x%08X, want 0xAAAABBBB", mb.Read32(0x2000))
	}
	if mb.Read32(0x2004) != 0xCCCCDDDD {
		t.Errorf("DMA: mem[0x2004] = 0x%08X, want 0xCCCCDDDD", mb.Read32(0x2004))
	}
}

func TestDSPDMAHoldPreservesAddress(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	mb.Write32(0x1000, 0xDEADBEEF)

	d := &s.dsp
	d.ra0 = 0x1000 >> 2
	d.ct[0] = 0

	// DMAH D0,[RAM0],1: hold=1, dir=0, addMode=1, count=1
	instr := uint32(0xC0000000)
	instr |= 1 << 15 // addMode=001
	instr |= 1 << 14 // hold=1
	instr |= 1       // count=1

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if d.data[0][0] != 0xDEADBEEF {
		t.Errorf("DMAH: data[0][0] = 0x%08X, want 0xDEADBEEF", d.data[0][0])
	}
	// RA0 should be unchanged (hold)
	if d.ra0 != 0x1000>>2 {
		t.Errorf("DMAH: RA0 = 0x%08X, want 0x%08X (held)", d.ra0, 0x1000>>2)
	}
}

func TestDSPDMACount0Means256(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	d := &s.dsp
	d.ra0 = 0
	d.ct[0] = 0

	// DMA with count=0 should transfer 256 words
	// addMode=0 (no address add - reads same address)
	instr := uint32(0xC0000000) // count=0

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	// CT0 should have wrapped: 256 increments from 0 = 0 (256 mod 64 = 0)
	if d.ct[0] != 0 {
		t.Errorf("DMA count=0: CT0 = %d, want 0 (256 mod 64)", d.ct[0])
	}
}

func TestDSPDMAAddModeRAMToD0(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	d := &s.dsp
	d.data[0][0] = 0xAAAAAAAA
	d.data[0][1] = 0xBBBBBBBB
	d.wa0 = 0x2000 >> 2
	d.ct[0] = 0

	// DMA [RAM0],D0,2: addMode=2 (add 8 bytes)
	instr := uint32(0xC0000000)
	instr |= 2 << 15 // addMode=010
	instr |= 1 << 12 // dir=1
	instr |= 2       // count=2

	d.prog[0] = instr
	d.prog[1] = 0xF0000000 // END
	d.writePPAF(1 << 16)
	runDSP(t, s)

	if mb.Read32(0x2000) != 0xAAAAAAAA {
		t.Errorf("DMA add8: mem[0x2000] = 0x%08X, want 0xAAAAAAAA", mb.Read32(0x2000))
	}
	if mb.Read32(0x2008) != 0xBBBBBBBB {
		t.Errorf("DMA add8: mem[0x2008] = 0x%08X, want 0xBBBBBBBB", mb.Read32(0x2008))
	}
}

// --- Integration Test ---

func TestDSPIntegrationLoadExecuteRead(t *testing.T) {
	s := NewSCU()
	mb := newMockBus()
	s.SetBus(mb)

	// Load a simple program through register ports:
	// prog[0]: MVI #100, MC0 (write 100 to data RAM bank 0)
	// prog[1]: MVI #200, MC1 (write 200 to data RAM bank 1)
	// prog[2]: END

	// Use SCU Write interface (as the SH-2 would)
	s.Write(0x80, 1<<15) // LE + PC=0

	// MVI #100, MC0: bits 31:30=10, dest=0000 (MC0), bit25=0, imm=100
	s.Write(0x84, uint32(0x80000000)|(0<<26)|100)
	// MVI #200, MC1: dest=0001
	s.Write(0x84, uint32(0x80000000)|(1<<26)|200)
	// END
	s.Write(0x84, 0xF0000000)

	// Load PC to 0 and execute
	s.Write(0x80, (1<<16)|(1<<15)|0)
	runDSP(t, s)

	// Read results via PDA+PDD
	s.Write(0x88, 0x00) // PDA: bank 0, addr 0
	val := s.Read(0x8C) // PDD read
	if val != 100 {
		t.Errorf("data[0][0] via PDD = %d, want 100", val)
	}

	s.Write(0x88, 0x40) // PDA: bank 1, addr 0
	val = s.Read(0x8C)
	if val != 200 {
		t.Errorf("data[1][0] via PDD = %d, want 200", val)
	}

	// Verify EX=0 via PPAF read
	ppaf := s.Read(0x80)
	if ppaf&(1<<16) != 0 {
		t.Error("EX should be 0 after program completes")
	}
}

// --- Cycle Budget Tests ---

func TestDSPCycleRatioHalvesSH2(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	// 50 NOPs + END: completes in 51 DSP cycles = 102 system cycles.
	for i := 0; i < 50; i++ {
		d.prog[i] = 0
	}
	d.prog[50] = 0xF0000000
	d.writePPAF(1 << 16)

	if !d.executing {
		t.Fatal("expected DSP armed after writePPAF")
	}

	s.TickSystemCycles(100) // 50 DSP cycles - one short of completion
	if !d.executing {
		t.Fatalf("DSP finished after %d system cycles; expected >= 102", 100)
	}

	s.TickSystemCycles(2) // 1 more DSP cycle - END dispatched
	if d.executing {
		t.Error("DSP should have finished after 102 system cycles")
	}
}

func TestDSPCycleRatioOddCycleCarry(t *testing.T) {
	s := NewSCU()
	d := &s.dsp

	d.prog[0] = 0xF0000000 // END at position 0
	d.writePPAF(1 << 16)

	// Single system cycle: half a DSP cycle, not enough to step.
	s.TickSystemCycles(1)
	if !d.executing {
		t.Fatal("DSP should not have run on 1 system cycle (carry only)")
	}
	if s.dspCycleCarry != 1 {
		t.Errorf("dspCycleCarry = %d, want 1", s.dspCycleCarry)
	}

	// Second system cycle tips the carry over: 1 DSP cycle, END runs.
	s.TickSystemCycles(1)
	if d.executing {
		t.Error("DSP should have finished after 2 system cycles")
	}
	if s.dspCycleCarry != 0 {
		t.Errorf("dspCycleCarry = %d, want 0 after even total", s.dspCycleCarry)
	}
}

// --- Pass 2 gap-fill tests ---

func TestSCUDSPReadPPAFInternalReturnsZero(t *testing.T) {
	d := &scuDSP{}
	d.pc = 0x42
	d.flagS = true
	d.flagZ = true
	d.executing = true
	if got := d.readPPAFInternal(); got != 0 {
		t.Errorf("readPPAFInternal = 0x%X, want 0", got)
	}
}

func TestSCUDSPWriteDestRegAllDests(t *testing.T) {
	d := &scuDSP{}

	// 4: RX
	d.writeDestReg(4, 0x12345678)
	if d.rx != 0x12345678 {
		t.Errorf("RX = 0x%X, want 0x12345678", d.rx)
	}
	// 5: PL with positive -> ph=0
	d.writeDestReg(5, 0x00000001)
	if d.pl != 0x00000001 || d.ph != 0 {
		t.Errorf("PL/PH = 0x%X/0x%X, want 0x1/0x0 for positive", d.pl, d.ph)
	}
	// 5: PL with negative -> ph=0xFFFF (sign-extend)
	d.writeDestReg(5, 0x80000000)
	if d.pl != 0x80000000 || d.ph != 0xFFFF {
		t.Errorf("PL/PH = 0x%X/0x%X, want 0x80000000/0xFFFF for negative", d.pl, d.ph)
	}
	// 6: RA0
	d.writeDestReg(6, 0xABCDEF)
	if d.ra0 != 0xABCDEF {
		t.Errorf("RA0 = 0x%X, want 0xABCDEF", d.ra0)
	}
	// 7: WA0
	d.writeDestReg(7, 0x123456)
	if d.wa0 != 0x123456 {
		t.Errorf("WA0 = 0x%X, want 0x123456", d.wa0)
	}
	// 0xA: LOP (12-bit)
	d.writeDestReg(0xA, 0xFFFF)
	if d.lop != 0x0FFF {
		t.Errorf("LOP = 0x%X, want 0x0FFF (12-bit mask)", d.lop)
	}
	// 0xB: TOP (8-bit)
	d.writeDestReg(0xB, 0xAB)
	if d.top != 0xAB {
		t.Errorf("TOP = 0x%X, want 0xAB", d.top)
	}
	// 0xC-0xF: CT0-CT3 (6-bit each)
	d.writeDestReg(0xC, 0xFF)
	d.writeDestReg(0xD, 0xFE)
	d.writeDestReg(0xE, 0xFD)
	d.writeDestReg(0xF, 0xFC)
	if d.ct[0] != 0x3F || d.ct[1] != 0x3E || d.ct[2] != 0x3D || d.ct[3] != 0x3C {
		t.Errorf("CT0-3 = %X %X %X %X, want 3F 3E 3D 3C (6-bit masks)",
			d.ct[0], d.ct[1], d.ct[2], d.ct[3])
	}
	// Unmapped dst: no-op (exercise default branch).
	d.writeDestReg(0x8, 0xDEADBEEF)
}

func TestSCUDSPTestCondAll(t *testing.T) {
	d := &scuDSP{}

	// cond bit 6 clear: always true.
	if !d.testCond(0) {
		t.Error("cond=0 (bit 6 clear) should be unconditionally true")
	}

	// bit 6 set, no flag bits selected, no inversion: the function
	// compares `result (false) == (inversion==true)` which is
	// `false == false` => true. This is the documented pass-through
	// when no flag matches and inversion is off.
	if !d.testCond(0x40) {
		t.Error("cond=0x40 (no flags, no inversion) should pass through")
	}

	// Guaranteed-false: cond requires a flag that is clear, with
	// inversion set -> `result(false) == true` => false.
	d.flagZ = false
	if d.testCond(0x40 | 0x01 | 0x20) {
		t.Error("cond Z clear with inversion should be false")
	}

	// Z flag selection.
	d.flagZ = true
	if !d.testCond(0x40 | 0x01 | 0x20) { // bit 5 = inversion; (result==true) == (true)
		t.Error("cond Z with flag set and inversion bit = false")
	}
	d.flagZ = false
	if d.testCond(0x40 | 0x01 | 0x20) {
		t.Error("cond Z with flag clear = true, want false (false != true)")
	}
	// S flag.
	d.flagS = true
	if !d.testCond(0x40 | 0x02 | 0x20) {
		t.Error("cond S with flag set = false")
	}
	// C flag.
	d.flagS = false
	d.flagC = true
	if !d.testCond(0x40 | 0x04 | 0x20) {
		t.Error("cond C with flag set = false")
	}
	// T0 flag.
	d.flagC = false
	d.flagT0 = true
	if !d.testCond(0x40 | 0x08 | 0x20) {
		t.Error("cond T0 with flag set = false")
	}
}

func TestSCUDSPExecD1BusImmediate(t *testing.T) {
	d := &scuDSP{}

	// D1 Op = 1: immediate-to-register. dst=4 (RX), imm=0xFE (sign-extended to -2).
	instr := uint32((1 << 12) | (4 << 8) | 0xFE) // d1Op at [13:12] or similar
	// Actually execD1Bus takes d1Op separately.
	var ctInc uint8
	d.execD1Bus(1, (4<<8)|0xFE, 0, 0, 0, &ctInc)
	if int32(d.rx) != -2 {
		t.Errorf("RX after immediate = %d, want -2 (sign-extended)", int32(d.rx))
	}
	_ = instr
}

func TestSCUDSPExecD1BusRAMRead(t *testing.T) {
	d := &scuDSP{}

	// D1 Op = 3, src = 0 (data[0][ct[0]]), dst = 4 (RX).
	d.data[0][5] = 0x12345678
	d.ct[0] = 5
	var ctInc uint8
	d.execD1Bus(3, (4<<8)|0, 0, 0, 0, &ctInc)
	if d.rx != 0x12345678 {
		t.Errorf("RX from RAM read = 0x%X, want 0x12345678", d.rx)
	}
}

func TestSCUDSPExecD1BusACLALU(t *testing.T) {
	d := &scuDSP{}

	// D1 Op = 3, src = 9 (aluACL).
	var ctInc uint8
	d.execD1Bus(3, (4<<8)|9, 0, 0xCAFEBABE, 0, &ctInc)
	if d.rx != 0xCAFEBABE {
		t.Errorf("RX from ALU ACL = 0x%X, want 0xCAFEBABE", d.rx)
	}

	// src = 0xA (combined ACH<<16 | ACL>>16).
	d.execD1Bus(3, (4<<8)|0xA, 0x1234, 0x56780000, 0, &ctInc)
	want := uint32(0x12345678)
	if d.rx != want {
		t.Errorf("RX from combined ACH/ACL = 0x%X, want 0x%X", d.rx, want)
	}
}

func TestSCUDSPExecDMADirection(t *testing.T) {
	s := NewSCU()
	bus := newFakeSCUBus()
	s.SetBus(bus)
	d := &s.dsp

	// Pre-load source memory.
	for i := uint32(0); i < 4; i++ {
		bus.Write32(0x200000+i*4, 0xA0000000|i)
	}

	// DMA D0->RAM bank 0, count=4. addMode bit 1 controls read-side +4.
	d.ra0 = 0x200000 >> 2
	d.ct[0] = 0
	// dir=0, format=0, hold=0, addMode=2 (bit1 set -> +4), ramSel=0, count=4
	instr := uint32(0) | (2 << 15) | 4
	d.execDMA(instr)
	for i := uint32(0); i < 4; i++ {
		if d.data[0][i] != (0xA0000000 | i) {
			t.Errorf("data[0][%d] = 0x%X, want 0x%X", i, d.data[0][i], 0xA0000000|i)
		}
	}

	// DMA RAM bank 0 -> D0, count=4. dir=1 uses dspDMAAddRAMtoD0
	// table; addMode=1 selects +4 bytes per write.
	d.ct[0] = 0
	d.wa0 = 0x300000 >> 2
	for i := uint32(0); i < 4; i++ {
		d.data[0][i] = 0xB0000000 | i
	}
	// dir=1 (bit 12), addMode=1, ramSel=0, count=4
	instr = (1 << 12) | (1 << 15) | 4
	d.execDMA(instr)
	for i := uint32(0); i < 4; i++ {
		got := bus.Read32(0x300000 + i*4)
		if got != (0xB0000000 | i) {
			t.Errorf("bus[0x300000+%d*4] = 0x%X, want 0x%X", i, got, 0xB0000000|i)
		}
	}
}

func TestSCUDSPExecDMAFormat(t *testing.T) {
	s := NewSCU()
	bus := newFakeSCUBus()
	s.SetBus(bus)
	d := &s.dsp

	// format=1: count from data RAM bank 0 at current CT.
	d.data[0][0] = 2 // transfer 2 longwords
	d.ct[0] = 0

	bus.Write32(0x200000, 0xDEADBEEF)
	bus.Write32(0x200004, 0xCAFEBABE)
	d.ra0 = 0x200000 >> 2

	// dir=0, format=1 (bit13), addMode=2 (read +4), ramSel=1 (bank 1),
	// src-from-bank=0 (bits 2:0 = 0)
	instr := uint32(0) | (1 << 13) | (2 << 15) | (1 << 8)
	d.execDMA(instr)
	if d.data[1][0] != 0xDEADBEEF || d.data[1][1] != 0xCAFEBABE {
		t.Errorf("format DMA result: data[1] = [0x%X, 0x%X]", d.data[1][0], d.data[1][1])
	}
}

func TestSCUDSPStepDebtCarry(t *testing.T) {
	d := &scuDSP{}
	d.debt = 10
	// budget < debt: debt decrements, no instructions executed.
	d.executing = true
	d.Step(3)
	if d.debt != 7 {
		t.Errorf("debt = %d, want 7 after Step(3)", d.debt)
	}
	// budget == debt: debt goes to 0, no instructions.
	d.Step(7)
	if d.debt != 0 {
		t.Errorf("debt = %d, want 0 after Step(7)", d.debt)
	}
}

func TestSCUDSPStepNoProgramHalts(t *testing.T) {
	d := &scuDSP{}
	d.executing = true
	// Without a program, the END instruction (opcode 0 with top2=0 is an
	// operation; we need an actual END). Load an END at pc=0.
	d.prog[0] = 0xF0000000 // top2=11, sub=11 = END
	d.nextInstr = d.prog[0]
	d.pc = 1

	d.Step(10)
	// END clears d.executing
	if d.executing {
		t.Error("executing still true after END dispatch")
	}
}

func TestSCUDSPLoopingSkipsPrefetch(t *testing.T) {
	d := &scuDSP{}
	d.executing = true
	d.looping = true
	d.lop = 3
	d.nextInstr = 0 // NOP-style operation
	d.prog[0] = 0
	d.pc = 1

	// While looping with lop > 0, the loop counter decrements and pc does
	// not advance.
	startPC := d.pc
	d.Step(2)
	if d.pc != startPC {
		t.Errorf("pc = %d, want %d while looping", d.pc, startPC)
	}
	if d.lop >= 3 {
		t.Errorf("lop = 0x%X, want to have decremented", d.lop)
	}
}
