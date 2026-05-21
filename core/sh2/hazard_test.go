// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// Opcode construction helpers for tests.
// SH-2 opcodes encode register fields as: bits 11-8 = Rn, bits 7-4 = Rm.

func encMOVLL(rm, rn uint16) uint16  { return 0x6002 | (rn << 8) | (rm << 4) } // MOV.L @Rm,Rn
func encMOVLP(rm, rn uint16) uint16  { return 0x6006 | (rn << 8) | (rm << 4) } // MOV.L @Rm+,Rn
func encADD(rm, rn uint16) uint16    { return 0x300C | (rn << 8) | (rm << 4) } // ADD Rm,Rn
func encMOVLS(rm, rn uint16) uint16  { return 0x2002 | (rn << 8) | (rm << 4) } // MOV.L Rm,@Rn
func encMOVBS(rm, rn uint16) uint16  { return 0x2000 | (rn << 8) | (rm << 4) } // MOV.B Rm,@Rn
func encCMPEQ(rm, rn uint16) uint16  { return 0x3000 | (rn << 8) | (rm << 4) } // CMP/EQ Rm,Rn
func encNOP() uint16                 { return 0x0009 }
func encANDI(imm uint16) uint16      { return 0xC900 | (imm & 0xFF) }          // AND #imm,R0
func encMOVBLG(disp uint16) uint16   { return 0xC400 | (disp & 0xFF) }         // MOV.B @(disp,GBR),R0
func encMOVLL0(rm, rn uint16) uint16 { return 0x000E | (rn << 8) | (rm << 4) } // MOV.L @(R0,Rm),Rn

// setupHazardTest creates a CPU with code at 0x100 and data at 0x200+.
// Returns the CPU and bus. No reset vectors needed since we set PC directly.
func setupHazardTest() (*CPU, *testBus) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.R[15] = 0x800
	return cpu, bus
}

// TestLoadUseHazardBasic: MOV.L @Rm,Rn then ADD Rn,R0 -> stall
func TestLoadUseHazardBasic(t *testing.T) {
	cpu, bus := setupHazardTest()

	// R1 = address of data, R0 = 1
	cpu.reg.R[1] = 0x200
	cpu.reg.R[0] = 1
	bus.Write32(0x200, 0x0000000A) // data: 10

	// MOV.L @R1,R2 at 0x100
	bus.Write16(0x100, encMOVLL(1, 2))
	// ADD R2,R0 at 0x102
	bus.Write16(0x102, encADD(2, 0))
	// NOP at 0x104 (for deferred execution)
	bus.Write16(0x104, encNOP())

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	// Step 1: execute MOV.L @R1,R2
	s1 := cpu.Clock()
	if s1.LoadUseStall {
		t.Error("step 1: unexpected LoadUseStall")
	}
	if cpu.reg.R[2] != 10 {
		t.Errorf("step 1: R2 = %d, want 10", cpu.reg.R[2])
	}

	// Step 2: should detect hazard, stall (ADD deferred)
	s2 := cpu.Clock()
	if !s2.LoadUseStall {
		t.Error("step 2: expected LoadUseStall=true")
	}
	// R0 should still be 1 (ADD not yet executed)
	if cpu.reg.R[0] != 1 {
		t.Errorf("step 2: R0 = %d, want 1 (ADD not yet executed)", cpu.reg.R[0])
	}

	// Step 3: deferred ADD R2,R0 executes
	s3 := cpu.Clock()
	if s3.LoadUseStall {
		t.Error("step 3: unexpected LoadUseStall")
	}
	// R0 = 1 + 10 = 11
	if cpu.reg.R[0] != 11 {
		t.Errorf("step 3: R0 = %d, want 11", cpu.reg.R[0])
	}

	// Total: 3 cycles for 2 instructions (1 stall)
	elapsed := cpu.Cycles() - startCycles
	if elapsed != 3 {
		t.Errorf("elapsed cycles = %d, want 3", elapsed)
	}
}

// TestNoHazardDifferentReg: MOV.L @Rm,Rn then ADD Rx,R0 (different reg)
func TestNoHazardDifferentReg(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	cpu.reg.R[3] = 5
	cpu.reg.R[0] = 1
	bus.Write32(0x200, 0x0000000A)

	// MOV.L @R1,R2 then ADD R3,R0 (R3 not loaded, no hazard)
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, encADD(3, 0))

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	s1 := cpu.Clock()
	if s1.LoadUseStall {
		t.Error("step 1: unexpected LoadUseStall")
	}

	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2: unexpected LoadUseStall (different reg)")
	}
	if cpu.reg.R[0] != 6 {
		t.Errorf("R0 = %d, want 6", cpu.reg.R[0])
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 2 {
		t.Errorf("elapsed cycles = %d, want 2", elapsed)
	}
}

// TestForwardingLoadIntoSameReg: MOV.L @Rm,Rn then MOV.L @Rx,Rn (no stall)
func TestForwardingLoadIntoSameReg(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	cpu.reg.R[3] = 0x204
	bus.Write32(0x200, 0x0000000A)
	bus.Write32(0x204, 0x00000014)

	// MOV.L @R1,R2 then MOV.L @R3,R2 (forwarding: load into same reg)
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, encMOVLL(3, 2))

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	cpu.Clock()
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2: unexpected stall (forwarding exception)")
	}
	if cpu.reg.R[2] != 20 {
		t.Errorf("R2 = %d, want 20", cpu.reg.R[2])
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 2 {
		t.Errorf("elapsed cycles = %d, want 2", elapsed)
	}
}

// TestStoreDataForwarding: MOV.L @Rm,Rn then MOV.L Rn,@Rx (no stall)
// Store data source has forwarding on internal bus.
func TestStoreDataForwarding(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	cpu.reg.R[3] = 0x300
	bus.Write32(0x200, 0xDEADBEEF)

	// MOV.L @R1,R2 then MOV.L R2,@R3
	// R2 is store data (Rm), R3 is address (Rn) -> no stall on R2
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, encMOVLS(2, 3))

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	cpu.Clock()
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2: unexpected stall (store data forwarding)")
	}

	stored := bus.Read32(0x300)
	if stored != 0xDEADBEEF {
		t.Errorf("stored value = 0x%08X, want 0xDEADBEEF", stored)
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 2 {
		t.Errorf("elapsed cycles = %d, want 2", elapsed)
	}
}

// TestStoreAddressStall: MOV.L @Rm,Rn then MOV.L Rx,@Rn (stall)
// Using loaded register as store address causes hazard.
func TestStoreAddressStall(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	cpu.reg.R[3] = 0x42
	bus.Write32(0x200, 0x00000300) // loaded value = address 0x300

	// MOV.L @R1,R2 then MOV.L R3,@R2
	// R2 is store address (Rn) -> stall
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, encMOVLS(3, 2))
	bus.Write16(0x104, encNOP())

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	cpu.Clock()
	s2 := cpu.Clock()
	if !s2.LoadUseStall {
		t.Error("step 2: expected LoadUseStall (store address hazard)")
	}

	// Step 3: deferred store executes
	cpu.Clock()
	stored := bus.Read32(0x300)
	if stored != 0x42 {
		t.Errorf("stored value = 0x%08X, want 0x00000042", stored)
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 3 {
		t.Errorf("elapsed cycles = %d, want 3", elapsed)
	}
}

// TestR0ImplicitHazard: MOV.B @(disp,GBR),R0 then AND #imm,R0 (stall)
func TestR0ImplicitHazard(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.GBR = 0x200
	bus.Write8(0x205, 0xFF) // data at GBR+5

	// MOV.B @(5,GBR),R0 then AND #0x0F,R0
	bus.Write16(0x100, encMOVBLG(5))
	bus.Write16(0x102, encANDI(0x0F))
	bus.Write16(0x104, encNOP())

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	cpu.Clock()
	if cpu.reg.R[0] != 0xFFFFFFFF {
		t.Errorf("step 1: R0 = 0x%08X, want 0xFFFFFFFF (sign-extended 0xFF)", cpu.reg.R[0])
	}

	s2 := cpu.Clock()
	if !s2.LoadUseStall {
		t.Error("step 2: expected LoadUseStall (R0 implicit hazard)")
	}

	cpu.Clock()
	if cpu.reg.R[0] != 0x0F {
		t.Errorf("step 3: R0 = 0x%08X, want 0x0000000F", cpu.reg.R[0])
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 3 {
		t.Errorf("elapsed cycles = %d, want 3", elapsed)
	}
}

// TestNoHazardAfterNonLoad: ADD R1,R2 then ADD R2,R0 (no stall)
func TestNoHazardAfterNonLoad(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 5
	cpu.reg.R[2] = 10
	cpu.reg.R[0] = 1

	// ADD R1,R2 then ADD R2,R0
	bus.Write16(0x100, encADD(1, 2))
	bus.Write16(0x102, encADD(2, 0))

	cpu.reg.PC = 0x100

	cpu.Clock()
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2: unexpected stall after non-load instruction")
	}
	if cpu.reg.R[0] != 16 {
		t.Errorf("R0 = %d, want 16", cpu.reg.R[0])
	}
}

// TestMultiCycleOpClearsHazard: load then multi-cycle op clears tracking
func TestMultiCycleOpClearsHazard(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	bus.Write32(0x200, 0x0000000A)

	// MOV.L @R1,R2 at 0x100
	bus.Write16(0x100, encMOVLL(1, 2))
	// TRAPA #0 at 0x102 (multi-cycle, will enter pending state)
	bus.Write16(0x102, 0xC300) // TRAPA #0

	// Set up TRAPA vector
	bus.Write32(0x00, 0x200) // vector 0 -> handler at 0x200
	bus.Write16(0x200, encNOP())

	cpu.reg.PC = 0x100
	cpu.Clock() // MOV.L @R1,R2

	// TRAPA doesn't read any GPR, so no hazard
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2: unexpected stall (TRAPA has no GPR source)")
	}
}

// TestHazardDetectionUnit tests the pure opcode analysis functions.
func TestHazardDetectionUnit(t *testing.T) {
	tests := []struct {
		name    string
		op      uint16
		loadReg uint8
		want    bool
	}{
		// Load then ALU using loaded reg -> stall
		{"ADD Rm,Rn uses Rm", encADD(2, 0), 2, true},
		{"ADD Rm,Rn uses Rn", encADD(0, 2), 2, true},
		{"ADD Rm,Rn no match", encADD(3, 4), 2, false},

		// Load into same register -> forwarding, no stall
		{"load same reg fwd", encMOVLL(1, 2), 2, false},
		{"load same reg fwd R0", encMOVBLG(0), 0, false},

		// Store data forwarding -> no stall
		{"store data fwd", encMOVLS(2, 3), 2, false},
		{"store data fwd byte", encMOVBS(2, 3), 2, false},

		// Store address -> stall
		{"store addr hazard", encMOVLS(3, 2), 2, true},
		{"store addr hazard byte", encMOVBS(3, 2), 2, true},

		// CMP reads both
		{"CMP/EQ Rm", encCMPEQ(2, 0), 2, true},
		{"CMP/EQ Rn", encCMPEQ(0, 2), 2, true},

		// Indexed load address
		{"indexed load R0", encMOVLL0(3, 4), 0, true},
		{"indexed load Rm", encMOVLL0(3, 4), 3, true},
		{"indexed load no match", encMOVLL0(3, 4), 5, false},

		// PC-relative load - no GPR source
		{"PC-rel load", 0xD100, 1, false}, // MOV.L @(disp,PC),R1

		// MOV #imm,Rn - no GPR source
		{"MOV imm", 0xE200, 2, false}, // MOV #0,R2

		// NOP - no GPR
		{"NOP", encNOP(), 0, false},

		// AND #imm,R0 - reads R0
		{"AND imm R0", encANDI(0xFF), 0, true},
		{"AND imm not R0", encANDI(0xFF), 1, false},

		// ADD #imm,Rn - reads Rn
		{"ADD imm Rn", 0x7200, 2, true}, // ADD #0,R2
		{"ADD imm not Rn", 0x7200, 3, false},

		// Branch - no GPR
		{"BRA", 0xA000, 0, false},
		{"BSR", 0xB000, 0, false},
		{"BT", 0x8900, 0, false},
		{"BF", 0x8B00, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loadUseHazard(tt.op, tt.loadReg)
			if got != tt.want {
				t.Errorf("loadUseHazard(0x%04X, R%d) = %v, want %v",
					tt.op, tt.loadReg, got, tt.want)
			}
		})
	}
}

// TestDeferredInstructionPreservesPC verifies that a deferred instruction
// after a stall uses the correct PC state.
func TestDeferredInstructionPreservesPC(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	bus.Write32(0x200, 0x0000000A)

	// MOV.L @R1,R2 at 0x100
	bus.Write16(0x100, encMOVLL(1, 2))
	// ADD R2,R0 at 0x102 (will be deferred)
	bus.Write16(0x102, encADD(2, 0))
	// NOP at 0x104
	bus.Write16(0x104, encNOP())

	cpu.reg.PC = 0x100

	cpu.Clock() // MOV.L
	cpu.Clock() // stall

	// PC should have advanced past the deferred instruction's fetch
	if cpu.reg.PC != 0x104 {
		t.Errorf("PC after stall = 0x%08X, want 0x00000104", cpu.reg.PC)
	}

	cpu.Clock() // deferred ADD executes
	// PC should now be at 0x104 (next instruction after ADD)
	if cpu.reg.PC != 0x104 {
		t.Errorf("PC after deferred = 0x%08X, want 0x00000104", cpu.reg.PC)
	}
}

// TestConsecutiveLoadsNoFalseHazard: two loads to different regs, no stall
func TestConsecutiveLoadsNoFalseHazard(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	cpu.reg.R[3] = 0x204
	bus.Write32(0x200, 0x0000000A)
	bus.Write32(0x204, 0x00000014)

	// MOV.L @R1,R2 then MOV.L @R3,R4 (no hazard: R2 not used)
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, encMOVLL(3, 4))

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	cpu.Clock()
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2: unexpected stall (different regs)")
	}
	if cpu.reg.R[2] != 10 {
		t.Errorf("R2 = %d, want 10", cpu.reg.R[2])
	}
	if cpu.reg.R[4] != 20 {
		t.Errorf("R4 = %d, want 20", cpu.reg.R[4])
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 2 {
		t.Errorf("elapsed cycles = %d, want 2", elapsed)
	}
}

// TestLoadUseHazardOnlyOneStall: hazard inserts exactly 1 stall cycle,
// not multiple.
func TestLoadUseHazardOnlyOneStall(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	cpu.reg.R[0] = 0
	bus.Write32(0x200, 7)

	// MOV.L @R1,R2 -> ADD R2,R0 -> ADD R2,R0
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, encADD(2, 0)) // hazard -> stall
	bus.Write16(0x104, encADD(2, 0)) // no hazard (R2 not from load)
	bus.Write16(0x106, encNOP())

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	cpu.Clock()       // MOV.L
	cpu.Clock()       // stall
	cpu.Clock()       // deferred ADD R2,R0
	s4 := cpu.Clock() // second ADD R2,R0 (no stall)
	if s4.LoadUseStall {
		t.Error("step 4: unexpected stall (load-use tracking cleared)")
	}
	if cpu.reg.R[0] != 14 {
		t.Errorf("R0 = %d, want 14 (7+7)", cpu.reg.R[0])
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 4 {
		t.Errorf("elapsed cycles = %d, want 4 (3 instructions + 1 stall)", elapsed)
	}
}

// TestPostIncrementLoadHazard: MOV.L @Rm+,Rn then use of Rn
func TestPostIncrementLoadHazard(t *testing.T) {
	cpu, bus := setupHazardTest()

	cpu.reg.R[1] = 0x200
	cpu.reg.R[0] = 0
	bus.Write32(0x200, 42)

	// MOV.L @R1+,R2 then ADD R2,R0
	bus.Write16(0x100, encMOVLP(1, 2))
	bus.Write16(0x102, encADD(2, 0))
	bus.Write16(0x104, encNOP())

	cpu.reg.PC = 0x100
	startCycles := cpu.Cycles()

	cpu.Clock() // MOV.L @R1+,R2 (R1 becomes 0x204, R2=42)
	s2 := cpu.Clock()
	if !s2.LoadUseStall {
		t.Error("step 2: expected stall")
	}
	cpu.Clock() // deferred ADD

	if cpu.reg.R[0] != 42 {
		t.Errorf("R0 = %d, want 42", cpu.reg.R[0])
	}
	if cpu.reg.R[1] != 0x204 {
		t.Errorf("R1 = 0x%X, want 0x204", cpu.reg.R[1])
	}

	elapsed := cpu.Cycles() - startCycles
	if elapsed != 3 {
		t.Errorf("elapsed cycles = %d, want 3", elapsed)
	}
}

// ------------------------------------------------------------------------
// Documentation-derived tests (SH-1/SH-2 Programming Manual, Section 7.2).
// These verify the hazard/pipeline code against hardware doc rules rather
// than against current code behavior. Test failures are findings, not
// symptoms to fix here.
// ------------------------------------------------------------------------

// Extra opcode encoding helpers for the doc-derived tests. Opcodes follow
// SH-2 encoding with bits 11-8 = Rn, bits 7-4 = Rm.
func encMOVBL(rm, rn uint16) uint16     { return 0x6000 | (rn << 8) | (rm << 4) } // MOV.B @Rm,Rn
func encMOVWL(rm, rn uint16) uint16     { return 0x6001 | (rn << 8) | (rm << 4) } // MOV.W @Rm,Rn
func encMOVBP(rm, rn uint16) uint16     { return 0x6004 | (rn << 8) | (rm << 4) } // MOV.B @Rm+,Rn
func encMOVWP(rm, rn uint16) uint16     { return 0x6005 | (rn << 8) | (rm << 4) } // MOV.W @Rm+,Rn
func encMOVBL4(d, rm uint16) uint16     { return 0x8400 | ((rm & 0xF) << 4) | (d & 0xF) }
func encMOVWL4(d, rm uint16) uint16     { return 0x8500 | ((rm & 0xF) << 4) | (d & 0xF) }
func encMOVLL4(d, rm, rn uint16) uint16 { return 0x5000 | (rn << 8) | (rm << 4) | (d & 0xF) }
func encMOVBL0(rm, rn uint16) uint16    { return 0x000C | (rn << 8) | (rm << 4) }             // MOV.B @(R0,Rm),Rn
func encMOVWL0(rm, rn uint16) uint16    { return 0x000D | (rn << 8) | (rm << 4) }             // MOV.W @(R0,Rm),Rn
func encMOVWLG(d uint16) uint16         { return 0xC500 | (d & 0xFF) }                        // MOV.W @(disp,GBR),R0
func encMOVLLG(d uint16) uint16         { return 0xC600 | (d & 0xFF) }                        // MOV.L @(disp,GBR),R0
func encMOVWLPC(d, rn uint16) uint16    { return 0x9000 | (rn << 8) | (d & 0xFF) }            // MOV.W @(disp,PC),Rn
func encMOVLLPC(d, rn uint16) uint16    { return 0xD000 | (rn << 8) | (d & 0xFF) }            // MOV.L @(disp,PC),Rn
func encMOVBG(d uint16) uint16          { return 0xC000 | (d & 0xFF) }                        // MOV.B R0,@(disp,GBR)
func encMOVWG(d uint16) uint16          { return 0xC100 | (d & 0xFF) }                        // MOV.W R0,@(disp,GBR)
func encMOVLG(d uint16) uint16          { return 0xC200 | (d & 0xFF) }                        // MOV.L R0,@(disp,GBR)
func encMOVBSD(d, rn uint16) uint16     { return 0x8000 | ((rn & 0xF) << 4) | (d & 0xF) }     // MOV.B R0,@(disp,Rn)
func encMOVWSD(d, rn uint16) uint16     { return 0x8100 | ((rn & 0xF) << 4) | (d & 0xF) }     // MOV.W R0,@(disp,Rn)
func encMOVLSD(d, rm, rn uint16) uint16 { return 0x1000 | (rn << 8) | (rm << 4) | (d & 0xF) } // MOV.L Rm,@(disp,Rn)
func encMOVBS0(rm, rn uint16) uint16    { return 0x0004 | (rn << 8) | (rm << 4) }             // MOV.B Rm,@(R0,Rn)
func encMOVWS0(rm, rn uint16) uint16    { return 0x0005 | (rn << 8) | (rm << 4) }             // MOV.W Rm,@(R0,Rn)
func encMOVLS0(rm, rn uint16) uint16    { return 0x0006 | (rn << 8) | (rm << 4) }             // MOV.L Rm,@(R0,Rn)
func encMOVWS(rm, rn uint16) uint16     { return 0x2001 | (rn << 8) | (rm << 4) }             // MOV.W Rm,@Rn
func encMOVLMD(rm, rn uint16) uint16    { return 0x2006 | (rn << 8) | (rm << 4) }             // MOV.L Rm,@-Rn
func encMOVA(d uint16) uint16           { return 0xC700 | (d & 0xFF) }                        // MOVA @(disp,PC),R0
func encORI(imm uint16) uint16          { return 0xCB00 | (imm & 0xFF) }                      // OR #imm,R0
func encXORI(imm uint16) uint16         { return 0xCA00 | (imm & 0xFF) }                      // XOR #imm,R0
func encTSTI(imm uint16) uint16         { return 0xC800 | (imm & 0xFF) }                      // TST #imm,R0
func encCMPEQI(imm uint16) uint16       { return 0x8800 | (imm & 0xFF) }                      // CMP/EQ #imm,R0
func encANDBG(imm uint16) uint16        { return 0xCD00 | (imm & 0xFF) }                      // AND.B #imm,@(R0,GBR)
func encORBG(imm uint16) uint16         { return 0xCF00 | (imm & 0xFF) }                      // OR.B #imm,@(R0,GBR)
func encXORBG(imm uint16) uint16        { return 0xCE00 | (imm & 0xFF) }                      // XOR.B #imm,@(R0,GBR)
func encTSTBG(imm uint16) uint16        { return 0xCC00 | (imm & 0xFF) }                      // TST.B #imm,@(R0,GBR)
func encBRAF(rm uint16) uint16          { return 0x0023 | (rm << 8) }                         // BRAF Rm
func encBSRF(rm uint16) uint16          { return 0x0003 | (rm << 8) }                         // BSRF Rm
func encJMP(rm uint16) uint16           { return 0x402B | (rm << 8) }                         // JMP @Rm
func encJSR(rm uint16) uint16           { return 0x400B | (rm << 8) }                         // JSR @Rm
func encMULL(rm, rn uint16) uint16      { return 0x0007 | (rn << 8) | (rm << 4) }             // MUL.L Rm,Rn
func encMULSW(rm, rn uint16) uint16     { return 0x200F | (rn << 8) | (rm << 4) }             // MULS.W Rm,Rn
func encMULUW(rm, rn uint16) uint16     { return 0x200E | (rn << 8) | (rm << 4) }             // MULU.W Rm,Rn
func encDMULSL(rm, rn uint16) uint16    { return 0x300D | (rn << 8) | (rm << 4) }             // DMULS.L Rm,Rn
func encDMULUL(rm, rn uint16) uint16    { return 0x3005 | (rn << 8) | (rm << 4) }             // DMULU.L Rm,Rn
func encMACL(rm, rn uint16) uint16      { return 0x000F | (rn << 8) | (rm << 4) }             // MAC.L @Rm+,@Rn+
func encMACW(rm, rn uint16) uint16      { return 0x400F | (rn << 8) | (rm << 4) }             // MAC.W @Rm+,@Rn+
func encCLRMAC() uint16                 { return 0x0028 }
func encLDSMACH(rm uint16) uint16       { return 0x400A | (rm << 8) } // LDS Rm,MACH
func encLDSMACL(rm uint16) uint16       { return 0x401A | (rm << 8) } // LDS Rm,MACL
func encSTSMACH(rn uint16) uint16       { return 0x000A | (rn << 8) } // STS MACH,Rn
func encSTSMACL(rn uint16) uint16       { return 0x001A | (rn << 8) } // STS MACL,Rn
func encLDSLMACH(rm uint16) uint16      { return 0x4006 | (rm << 8) } // LDS.L @Rm+,MACH
func encLDSLMACL(rm uint16) uint16      { return 0x4016 | (rm << 8) } // LDS.L @Rm+,MACL
func encSTSLMACH(rn uint16) uint16      { return 0x4002 | (rn << 8) } // STS.L MACH,@-Rn
func encSTSLMACL(rn uint16) uint16      { return 0x4012 | (rn << 8) } // STS.L MACL,@-Rn
func encTAS(rn uint16) uint16           { return 0x401B | (rn << 8) } // TAS.B @Rn
func encRTE() uint16                    { return 0x002B }

// runLoadStallCase configures a two-instruction sequence at 0x100/0x102
// preceded by pre-run register setup, clocks the load then the follower,
// and returns whether LoadUseStall was observed on the second clock.
func runLoadStallCase(t *testing.T, loadOp uint16, followerOp uint16, setup func(c *CPU, b *testBus)) bool {
	t.Helper()
	cpu, bus := setupHazardTest()
	if setup != nil {
		setup(cpu, bus)
	}
	bus.Write16(0x100, loadOp)
	bus.Write16(0x102, followerOp)
	bus.Write16(0x104, encNOP())
	cpu.reg.PC = 0x100
	cpu.Clock()
	s2 := cpu.Clock()
	return s2.LoadUseStall
}

// TestLoadUseStall_AllLoadForms verifies that every memory-load instruction
// form documented in the SH-1/SH-2 Programming Manual, Section 7.2.2 (p.389),
// triggers a load-use stall when the immediately following instruction reads
// the loaded destination.
//
// Manual rule (quoted):
//
//	"When instruction 2 uses the same destination register as load
//	 instruction 1, the contents of that register will not be ready, so any
//	 slot containing the MA of instruction 1 and EX of instruction 2 will
//	 split."
//
// Follower instruction is chosen per case so the destination register is
// read as an EX-stage source. R0-destination loads use AND #imm,R0 as the
// follower; other destinations use ADD Rn,R0 which reads Rn in regM
// position and R0 in regN position - reading Rn stalls.
func TestLoadUseStall_AllLoadForms(t *testing.T) {
	cases := []struct {
		name     string
		loadOp   uint16
		follower uint16
		setup    func(c *CPU, b *testBus)
	}{
		{
			"MOV.B @Rm,Rn",
			encMOVBL(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write8(0x200, 0x7F) },
		},
		{
			"MOV.W @Rm,Rn",
			encMOVWL(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write16(0x200, 0x1234) },
		},
		{
			"MOV.L @Rm,Rn",
			encMOVLL(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write32(0x200, 0x12345678) },
		},
		{
			"MOV.B @Rm+,Rn",
			encMOVBP(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write8(0x200, 0x42) },
		},
		{
			"MOV.W @Rm+,Rn",
			encMOVWP(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write16(0x200, 0x4242) },
		},
		{
			"MOV.L @Rm+,Rn",
			encMOVLP(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write32(0x200, 0xABCDEF01) },
		},
		{
			"MOV.B @(disp,Rm),R0",
			encMOVBL4(1, 1), encANDI(0xFF),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write8(0x201, 0x55) },
		},
		{
			"MOV.W @(disp,Rm),R0",
			encMOVWL4(1, 1), encANDI(0xFF),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write16(0x202, 0x5555) },
		},
		{
			"MOV.L @(disp,Rm),Rn",
			encMOVLL4(1, 1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) { c.reg.R[1] = 0x200; b.Write32(0x204, 0xCAFEBABE) },
		},
		{
			"MOV.B @(R0,Rm),Rn",
			encMOVBL0(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) {
				c.reg.R[0] = 0x10
				c.reg.R[1] = 0x200
				b.Write8(0x210, 0x77)
			},
		},
		{
			"MOV.W @(R0,Rm),Rn",
			encMOVWL0(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) {
				c.reg.R[0] = 0x10
				c.reg.R[1] = 0x200
				b.Write16(0x210, 0x7777)
			},
		},
		{
			"MOV.L @(R0,Rm),Rn",
			encMOVLL0(1, 2), encADD(2, 0),
			func(c *CPU, b *testBus) {
				c.reg.R[0] = 0x10
				c.reg.R[1] = 0x200
				b.Write32(0x210, 0xDEADBEEF)
			},
		},
		{
			"MOV.B @(disp,GBR),R0",
			encMOVBLG(5), encANDI(0x0F),
			func(c *CPU, b *testBus) { c.reg.GBR = 0x200; b.Write8(0x205, 0x8F) },
		},
		{
			"MOV.W @(disp,GBR),R0",
			encMOVWLG(2), encANDI(0x0F),
			func(c *CPU, b *testBus) { c.reg.GBR = 0x200; b.Write16(0x204, 0x8F8F) },
		},
		{
			"MOV.L @(disp,GBR),R0",
			encMOVLLG(1), encANDI(0x0F),
			func(c *CPU, b *testBus) { c.reg.GBR = 0x200; b.Write32(0x204, 0xDEAD8F8F) },
		},
		{
			"MOV.W @(disp,PC),Rn",
			encMOVWLPC(4, 2), encADD(2, 0),
			// disp*2 + PC+4; PC after fetch = 0x102, so target = 0x102+4+4*2 = 0x10E
			func(c *CPU, b *testBus) { b.Write16(0x10E, 0x1234) },
		},
		{
			"MOV.L @(disp,PC),Rn",
			encMOVLLPC(2, 2), encADD(2, 0),
			// (PC+4 & ~3) + disp*4; PC after fetch = 0x102, (0x102+4)&~3 = 0x104, +8 = 0x10C
			func(c *CPU, b *testBus) { b.Write32(0x10C, 0x12345678) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !runLoadStallCase(t, tc.loadOp, tc.follower, tc.setup) {
				t.Errorf("load 0x%04X -> follower 0x%04X: expected LoadUseStall=true (Section 7.2.2, p.389)",
					tc.loadOp, tc.follower)
			}
		})
	}
}

// TestLoadUseStall_LoadIntoSameRegMatrix covers the first documented
// forwarding exception (SH-1/SH-2 Programming Manual, Section 7.2.2, p.389):
//
//	"No split occurs, however, in the following cases:
//	 * When instruction 2 is a load instruction and its destination is the
//	   same as that of load instruction 1"
//
// Every documented load form is exercised as instruction 2 with its
// destination matching a prior load's destination. None should stall.
func TestLoadUseStall_LoadIntoSameRegMatrix(t *testing.T) {
	type follower struct {
		name  string
		op    uint16
		setup func(c *CPU, b *testBus)
	}
	followers := []follower{
		{"MOV.B @Rm,Rn", encMOVBL(3, 2), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; b.Write8(0x220, 1) }},
		{"MOV.W @Rm,Rn", encMOVWL(3, 2), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; b.Write16(0x220, 1) }},
		{"MOV.L @Rm,Rn", encMOVLL(3, 2), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; b.Write32(0x220, 1) }},
		{"MOV.B @Rm+,Rn", encMOVBP(3, 2), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; b.Write8(0x220, 1) }},
		{"MOV.W @Rm+,Rn", encMOVWP(3, 2), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; b.Write16(0x220, 1) }},
		{"MOV.L @Rm+,Rn", encMOVLP(3, 2), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; b.Write32(0x220, 1) }},
		{"MOV.L @(disp,Rm),Rn", encMOVLL4(0, 3, 2), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; b.Write32(0x220, 1) }},
		{"MOV.B @(R0,Rm),Rn", encMOVBL0(3, 2), func(c *CPU, b *testBus) { c.reg.R[0] = 0; c.reg.R[3] = 0x220; b.Write8(0x220, 1) }},
		{"MOV.W @(R0,Rm),Rn", encMOVWL0(3, 2), func(c *CPU, b *testBus) { c.reg.R[0] = 0; c.reg.R[3] = 0x220; b.Write16(0x220, 1) }},
		{"MOV.L @(R0,Rm),Rn", encMOVLL0(3, 2), func(c *CPU, b *testBus) { c.reg.R[0] = 0; c.reg.R[3] = 0x220; b.Write32(0x220, 1) }},
	}

	for _, f := range followers {
		t.Run(f.name, func(t *testing.T) {
			cpu, bus := setupHazardTest()
			cpu.reg.R[1] = 0x200
			bus.Write32(0x200, 0xAA)
			if f.setup != nil {
				f.setup(cpu, bus)
			}
			bus.Write16(0x100, encMOVLL(1, 2))
			bus.Write16(0x102, f.op)
			bus.Write16(0x104, encNOP())
			cpu.reg.PC = 0x100
			cpu.Clock()
			s2 := cpu.Clock()
			if s2.LoadUseStall {
				t.Errorf("load into R2 -> %s (into R2): expected no stall (forwarding exception, Section 7.2.2, p.389)", f.name)
			}
		})
	}
}

// TestLoadUseStall_MACForwardingException covers the second documented
// forwarding exception (SH-1/SH-2 Programming Manual, Section 7.2.2, p.389):
//
//	"No split occurs, however, in the following cases:
//	 * When instruction 2 is MAC @Rm+,@Rn+ and the destinations of Rm and
//	   load instruction 1 were the same"
//
// When a load targets the register that MAC will use as the first pointer
// (Rm field in assembler, bits 7-4 in encoding), MAC must not stall.
// The second pointer (Rn field, bits 11-8) does NOT have this exception
// per the spec - it still stalls.
func TestLoadUseStall_MACForwardingException(t *testing.T) {
	t.Run("MAC.L Rm-field load forwards", func(t *testing.T) {
		cpu, bus := setupHazardTest()
		cpu.reg.R[1] = 0x200
		cpu.reg.R[3] = 0x220 // Rn pointer of MAC
		bus.Write32(0x200, 0x300)
		bus.Write32(0x300, 5)
		bus.Write32(0x220, 7)
		bus.Write16(0x100, encMOVLL(1, 2)) // load into R2
		bus.Write16(0x102, encMACL(2, 3))  // MAC.L @R2+,@R3+
		bus.Write16(0x104, encNOP())
		cpu.reg.PC = 0x100
		cpu.Clock()
		s2 := cpu.Clock()
		if s2.LoadUseStall {
			t.Error("expected no stall (Rm-field forwarding per Section 7.2.2, p.389)")
		}
	})
	t.Run("MAC.W Rm-field load forwards", func(t *testing.T) {
		cpu, bus := setupHazardTest()
		cpu.reg.R[1] = 0x200
		cpu.reg.R[3] = 0x220
		bus.Write32(0x200, 0x300)
		bus.Write16(0x300, 5)
		bus.Write16(0x220, 7)
		bus.Write16(0x100, encMOVLL(1, 2)) // load into R2
		bus.Write16(0x102, encMACW(2, 3))  // MAC.W @R2+,@R3+
		bus.Write16(0x104, encNOP())
		cpu.reg.PC = 0x100
		cpu.Clock()
		s2 := cpu.Clock()
		if s2.LoadUseStall {
			t.Error("expected no stall (Rm-field forwarding per Section 7.2.2, p.389)")
		}
	})
	t.Run("MAC.L Rn-field load does stall", func(t *testing.T) {
		cpu, bus := setupHazardTest()
		cpu.reg.R[1] = 0x200
		cpu.reg.R[2] = 0x220 // Rm pointer of MAC
		bus.Write32(0x200, 0x300)
		bus.Write32(0x300, 7)
		bus.Write32(0x220, 5)
		bus.Write16(0x100, encMOVLL(1, 3)) // load into R3
		bus.Write16(0x102, encMACL(2, 3))  // MAC.L @R2+,@R3+
		bus.Write16(0x104, encNOP())
		cpu.reg.PC = 0x100
		cpu.Clock()
		s2 := cpu.Clock()
		if !s2.LoadUseStall {
			t.Error("expected stall on Rn-field (exception only applies to Rm, Section 7.2.2, p.389)")
		}
	})
	t.Run("MAC.W Rn-field load does stall", func(t *testing.T) {
		cpu, bus := setupHazardTest()
		cpu.reg.R[1] = 0x200
		cpu.reg.R[2] = 0x220
		bus.Write32(0x200, 0x300)
		bus.Write16(0x300, 7)
		bus.Write16(0x220, 5)
		bus.Write16(0x100, encMOVLL(1, 3)) // load into R3
		bus.Write16(0x102, encMACW(2, 3))  // MAC.W @R2+,@R3+
		bus.Write16(0x104, encNOP())
		cpu.reg.PC = 0x100
		cpu.Clock()
		s2 := cpu.Clock()
		if !s2.LoadUseStall {
			t.Error("expected stall on Rn-field (exception only applies to Rm, Section 7.2.2, p.389)")
		}
	})
}

// TestLoadUseStall_AddressPointerHazard covers the address-pointer rule
// (SH-1/SH-2 Programming Manual, Section 7.2.2, p.389, figure 7.10):
//
//	"When data is loaded to a register in the previous instruction and
//	 the following memory access instruction uses that register as an
//	 address pointer, the memory access is extended until the data load
//	 of the MA stage of the previous instruction ends."
//
// For every memory-access form that uses a register as its address, a
// preceding load into that register must flag LoadUseStall. Reader and
// writer variants are both covered.
func TestLoadUseStall_AddressPointerHazard(t *testing.T) {
	cases := []struct {
		name     string
		follower uint16
		setup    func(c *CPU, b *testBus)
	}{
		{"read MOV.B @Rn,Rx", encMOVBL(2, 4), nil},
		{"read MOV.W @Rn,Rx", encMOVWL(2, 4), nil},
		{"read MOV.L @Rn,Rx", encMOVLL(2, 4), nil},
		{"read MOV.B @Rn+,Rx", encMOVBP(2, 4), nil},
		{"read MOV.W @Rn+,Rx", encMOVWP(2, 4), nil},
		{"read MOV.L @Rn+,Rx", encMOVLP(2, 4), nil},
		{"read MOV.L @(disp,Rn),Rx", encMOVLL4(0, 2, 4), nil},
		{"read MOV.B @(R0,Rn),Rx", encMOVBL0(2, 4), func(c *CPU, b *testBus) { c.reg.R[0] = 0 }},
		{"read MOV.W @(R0,Rn),Rx", encMOVWL0(2, 4), func(c *CPU, b *testBus) { c.reg.R[0] = 0 }},
		{"read MOV.L @(R0,Rn),Rx", encMOVLL0(2, 4), func(c *CPU, b *testBus) { c.reg.R[0] = 0 }},
		{"write MOV.B Rx,@Rn", encMOVBS(4, 2), nil},
		{"write MOV.W Rx,@Rn", encMOVWS(4, 2), nil},
		{"write MOV.L Rx,@Rn", encMOVLS(4, 2), nil},
		{"write MOV.L Rx,@(disp,Rn)", encMOVLSD(0, 4, 2), nil},
		{"write MOV.B R0,@(disp,Rn)", encMOVBSD(0, 2), nil},
		{"write MOV.W R0,@(disp,Rn)", encMOVWSD(0, 2), nil},
		{"write MOV.B Rx,@(R0,Rn)", encMOVBS0(4, 2), func(c *CPU, b *testBus) { c.reg.R[0] = 0 }},
		{"write MOV.W Rx,@(R0,Rn)", encMOVWS0(4, 2), func(c *CPU, b *testBus) { c.reg.R[0] = 0 }},
		{"write MOV.L Rx,@(R0,Rn)", encMOVLS0(4, 2), func(c *CPU, b *testBus) { c.reg.R[0] = 0 }},
		{"write MOV.B Rx,@-Rn", encMOVBS(4, 2) | 0x0004, nil}, // 0x2004 variant
		{"write MOV.W Rx,@-Rn", encMOVWS(4, 2) | 0x0004, nil},
		{"write MOV.L Rx,@-Rn", encMOVLMD(4, 2), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cpu, bus := setupHazardTest()
			cpu.reg.R[1] = 0x200
			bus.Write32(0x200, 0x300) // loaded value used as address
			cpu.reg.R[4] = 0xAA
			if tc.setup != nil {
				tc.setup(cpu, bus)
			}
			bus.Write16(0x100, encMOVLL(1, 2)) // load into R2
			bus.Write16(0x102, tc.follower)
			bus.Write16(0x104, encNOP())
			cpu.reg.PC = 0x100
			cpu.Clock()
			s2 := cpu.Clock()
			if !s2.LoadUseStall {
				t.Errorf("load -> %s (Rn = loaded reg): expected stall (Section 7.2.2, p.389)", tc.name)
			}
		})
	}
}

// TestLoadUseStall_PostIncSameDest covers the degenerate case "MOV.L @Rn+,Rn"
// where the post-increment target equals the load destination. Per the
// SH-2 instruction description (MOV.L @Rm+,Rn: when m==n, post-increment
// is overridden by the load value), the destination holds the loaded
// value and there is no prior instruction to trigger a load-use hazard
// against - this exercises hazard-tracker robustness when a single
// instruction is both the load and its own would-be consumer.
func TestLoadUseStall_PostIncSameDest(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[2] = 0x200
	bus.Write32(0x200, 0xDEADBEEF)
	bus.Write16(0x100, encMOVLP(2, 2)) // MOV.L @R2+,R2
	bus.Write16(0x102, encNOP())
	bus.Write16(0x104, encNOP())
	cpu.reg.PC = 0x100

	s1 := cpu.Clock()
	if s1.LoadUseStall {
		t.Error("step 1 (the load itself): unexpected LoadUseStall")
	}
	if cpu.reg.R[2] != 0xDEADBEEF {
		t.Errorf("R2 after load = 0x%08X, want 0xDEADBEEF (load wins over post-inc when Rm==Rn)", cpu.reg.R[2])
	}

	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2 (NOP): unexpected stall - NOP has no GPR source")
	}
}

// TestLoadUseStall_R0ImplicitSource covers every documented R0 implicit
// source (SH-1/SH-2 Programming Manual Section 2.1, p.5, plus individual
// instruction descriptions in Section 7 and Appendix A):
//
//	"R0 is also used as an index register. Several instructions use R0 as
//	 a fixed source or destination register."
//
// Loading into R0 and following with each documented R0-as-source
// instruction must trigger LoadUseStall.
func TestLoadUseStall_R0ImplicitSource(t *testing.T) {
	cases := []struct {
		name     string
		follower uint16
		setup    func(c *CPU, b *testBus)
	}{
		{"MOV.B @(R0,Rm),Rn index", encMOVBL0(3, 4), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220 }},
		{"MOV.W @(R0,Rm),Rn index", encMOVWL0(3, 4), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220 }},
		{"MOV.L @(R0,Rm),Rn index", encMOVLL0(3, 4), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220 }},
		{"MOV.B Rm,@(R0,Rn) index", encMOVBS0(4, 3), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; c.reg.R[4] = 0xAA }},
		{"MOV.W Rm,@(R0,Rn) index", encMOVWS0(4, 3), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; c.reg.R[4] = 0xAA }},
		{"MOV.L Rm,@(R0,Rn) index", encMOVLS0(4, 3), func(c *CPU, b *testBus) { c.reg.R[3] = 0x220; c.reg.R[4] = 0xAA }},
		{"AND #imm,R0", encANDI(0x0F), nil},
		{"OR #imm,R0", encORI(0x10), nil},
		{"XOR #imm,R0", encXORI(0x20), nil},
		{"TST #imm,R0", encTSTI(0x01), nil},
		{"CMP/EQ #imm,R0", encCMPEQI(0x00), nil},
		{"TST.B #imm,@(R0,GBR)", encTSTBG(0x0F), func(c *CPU, b *testBus) { c.reg.GBR = 0x200 }},
		{"AND.B #imm,@(R0,GBR)", encANDBG(0x0F), func(c *CPU, b *testBus) { c.reg.GBR = 0x200 }},
		{"OR.B #imm,@(R0,GBR)", encORBG(0x10), func(c *CPU, b *testBus) { c.reg.GBR = 0x200 }},
		{"XOR.B #imm,@(R0,GBR)", encXORBG(0x20), func(c *CPU, b *testBus) { c.reg.GBR = 0x200 }},
		{"BRAF R0", encBRAF(0), nil},
		{"BSRF R0", encBSRF(0), nil},
		{"JMP @R0", encJMP(0), nil},
		{"JSR @R0", encJSR(0), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cpu, bus := setupHazardTest()
			cpu.reg.GBR = 0x200
			cpu.reg.R[1] = 0x200
			bus.Write32(0x200, 0x20) // small value safe for R0-index + GBR, also valid branch target
			if tc.setup != nil {
				tc.setup(cpu, bus)
			}
			bus.Write16(0x100, encMOVBLG(0)) // MOV.B @(0,GBR),R0 - load into R0
			bus.Write16(0x102, tc.follower)
			bus.Write16(0x104, encNOP())
			bus.Write16(0x20, encNOP()) // branch target (harmless if not branched)
			cpu.reg.PC = 0x100
			cpu.Clock()
			s2 := cpu.Clock()
			if !s2.LoadUseStall {
				t.Errorf("load into R0 -> %s: expected stall (R0 is implicit source per Section 2.1, p.5)", tc.name)
			}
		})
	}
}

// TestLoadUseStall_R0AsStoreDataForwards covers the store-data forwarding
// rule (Section 7.2.2 p.391, figures 7.13-7.15):
//
//	"When data is loaded from memory to the destination register and the
//	 register is then specified as the source operand for a following
//	 store instruction, the preceding instruction's load is executed in
//	 the WB/DSP stage and the following instruction's store is executed
//	 in the MA stage. These stages are executed in exactly the same cycle.
//	 Nevertheless, they do not contend."
//
// When R0 is the data operand of a store (not the address), no stall
// should occur after a load into R0.
func TestLoadUseStall_R0AsStoreDataForwards(t *testing.T) {
	cases := []struct {
		name     string
		follower uint16
		setup    func(c *CPU, b *testBus)
	}{
		{"MOV.B R0,@(disp,Rn)", encMOVBSD(0, 3), func(c *CPU, b *testBus) { c.reg.R[3] = 0x300 }},
		{"MOV.W R0,@(disp,Rn)", encMOVWSD(0, 3), func(c *CPU, b *testBus) { c.reg.R[3] = 0x300 }},
		{"MOV.B R0,@(disp,GBR)", encMOVBG(0), func(c *CPU, b *testBus) { c.reg.GBR = 0x300 }},
		{"MOV.W R0,@(disp,GBR)", encMOVWG(0), func(c *CPU, b *testBus) { c.reg.GBR = 0x300 }},
		{"MOV.L R0,@(disp,GBR)", encMOVLG(0), func(c *CPU, b *testBus) { c.reg.GBR = 0x300 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cpu, bus := setupHazardTest()
			cpu.reg.GBR = 0x200
			bus.Write32(0x200, 0xAA)
			if tc.setup != nil {
				tc.setup(cpu, bus)
			}
			bus.Write16(0x100, encMOVBLG(0)) // load into R0
			bus.Write16(0x102, tc.follower)
			bus.Write16(0x104, encNOP())
			cpu.reg.PC = 0x100
			cpu.Clock()
			s2 := cpu.Clock()
			if s2.LoadUseStall {
				t.Errorf("load into R0 -> %s (R0 as store data): expected no stall (Section 7.2.2, p.391)", tc.name)
			}
		})
	}
}

// TestLoadUseStall_MOVAIntoR0 covers that MOVA @(disp,PC),R0 is not a
// memory load and does not read R0 as a source (Appendix A: MOVA
// description - "PC-relative address, no register source"). A preceding
// load into R0 must not cause a stall.
func TestLoadUseStall_MOVAIntoR0(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.GBR = 0x200
	bus.Write32(0x200, 0xAA)
	bus.Write16(0x100, encMOVBLG(0))
	bus.Write16(0x102, encMOVA(0))
	bus.Write16(0x104, encNOP())
	cpu.reg.PC = 0x100
	cpu.Clock()
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("load R0 -> MOVA @(disp,PC),R0: unexpected stall (MOVA has no register source)")
	}
}

// ------------------------------------------------------------------------
// Group E: usesRegAsSource classification coverage per opcode group
// (SH-1/SH-2 Programming Manual, Table 7.2 - per-instruction stages and
// cycles; Appendix A - CPU Instructions). Tests call usesRegAsSource()
// directly with synthesized opcodes; each case lists every register the
// doc identifies as a source and every register that must NOT be a source
// (particularly store-data operands that are covered by the MA/WB
// forwarding rule in Section 7.2.2 p.391).
// ------------------------------------------------------------------------

type classifyCase struct {
	name     string
	op       uint16
	positive []uint8 // registers that must be reported as source (reads)
	negative []uint8 // registers that must NOT be reported as source
}

func runClassify(t *testing.T, cases []classifyCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, r := range tc.positive {
				if !usesRegAsSource(tc.op, r) {
					t.Errorf("usesRegAsSource(0x%04X, R%d) = false, want true", tc.op, r)
				}
			}
			for _, r := range tc.negative {
				if usesRegAsSource(tc.op, r) {
					t.Errorf("usesRegAsSource(0x%04X, R%d) = true, want false", tc.op, r)
				}
			}
		})
	}
}

// TestUsesRegAsSource_Group0 enumerates top-nibble 0x0 opcodes.
// Rn sits in bits 11-8, Rm in bits 7-4. Each case picks distinctive
// register numbers so "positive" and "negative" sets are disjoint.
func TestUsesRegAsSource_Group0(t *testing.T) {
	runClassify(t, []classifyCase{
		{"STC SR,Rn", 0x0002 | (5 << 8), nil, []uint8{0, 5, 3}},
		{"STC GBR,Rn", 0x0012 | (5 << 8), nil, []uint8{0, 5, 3}},
		{"STC VBR,Rn", 0x0022 | (5 << 8), nil, []uint8{0, 5, 3}},
		{"BSRF Rm", 0x0003 | (5 << 8), []uint8{5}, []uint8{0, 3, 7}},
		{"BRAF Rm", 0x0023 | (5 << 8), []uint8{5}, []uint8{0, 3, 7}},
		{"MOV.B Rm,@(R0,Rn)", 0x0004 | (5 << 8) | (3 << 4), []uint8{0, 5}, []uint8{3, 7}}, // Rm=3 data forwarded
		{"MOV.W Rm,@(R0,Rn)", 0x0005 | (5 << 8) | (3 << 4), []uint8{0, 5}, []uint8{3, 7}},
		{"MOV.L Rm,@(R0,Rn)", 0x0006 | (5 << 8) | (3 << 4), []uint8{0, 5}, []uint8{3, 7}},
		{"MUL.L Rm,Rn", 0x0007 | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"CLRT", 0x0008, nil, []uint8{0, 5, 15}},
		{"SETT", 0x0018, nil, []uint8{0, 5, 15}},
		{"CLRMAC", 0x0028, nil, []uint8{0, 5, 15}},
		{"NOP", 0x0009, nil, []uint8{0, 5, 15}},
		{"DIV0U", 0x0019, nil, []uint8{0, 5, 15}},
		{"MOVT Rn", 0x0029 | (5 << 8), nil, []uint8{0, 5, 3}},
		{"STS MACH,Rn", 0x000A | (5 << 8), nil, []uint8{0, 5, 3}},
		{"STS MACL,Rn", 0x001A | (5 << 8), nil, []uint8{0, 5, 3}},
		{"STS PR,Rn", 0x002A | (5 << 8), nil, []uint8{0, 5, 3}},
		{"RTS", 0x000B, nil, []uint8{0, 5, 15}},
		{"SLEEP", 0x001B, nil, []uint8{0, 5, 15}},
		{"RTE reads R15", 0x002B, []uint8{15}, []uint8{0, 5, 3}},
		{"MOV.B @(R0,Rm),Rn", 0x000C | (5 << 8) | (3 << 4), []uint8{0, 3}, []uint8{5, 7}}, // Rn=5 destination
		{"MOV.W @(R0,Rm),Rn", 0x000D | (5 << 8) | (3 << 4), []uint8{0, 3}, []uint8{5, 7}},
		{"MOV.L @(R0,Rm),Rn", 0x000E | (5 << 8) | (3 << 4), []uint8{0, 3}, []uint8{5, 7}},
		{"MAC.L @Rm+,@Rn+ (Rm forwards)", 0x000F | (5 << 8) | (3 << 4), []uint8{5}, []uint8{3, 0, 7}},
	})
}

// TestUsesRegAsSource_Group2 enumerates top-nibble 0x2 opcodes.
// Store operations read Rn as address; Rm is store data (forwarded).
// Logic/arithmetic operations read both.
func TestUsesRegAsSource_Group2(t *testing.T) {
	runClassify(t, []classifyCase{
		{"MOV.B Rm,@Rn", 0x2000 | (5 << 8) | (3 << 4), []uint8{5}, []uint8{3, 0, 7}},
		{"MOV.W Rm,@Rn", 0x2001 | (5 << 8) | (3 << 4), []uint8{5}, []uint8{3, 0, 7}},
		{"MOV.L Rm,@Rn", 0x2002 | (5 << 8) | (3 << 4), []uint8{5}, []uint8{3, 0, 7}},
		{"MOV.B Rm,@-Rn", 0x2004 | (5 << 8) | (3 << 4), []uint8{5}, []uint8{3, 0, 7}},
		{"MOV.W Rm,@-Rn", 0x2005 | (5 << 8) | (3 << 4), []uint8{5}, []uint8{3, 0, 7}},
		{"MOV.L Rm,@-Rn", 0x2006 | (5 << 8) | (3 << 4), []uint8{5}, []uint8{3, 0, 7}},
		{"DIV0S Rm,Rn", 0x2007 | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"TST Rm,Rn", 0x2008 | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"AND Rm,Rn", 0x2009 | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"XOR Rm,Rn", 0x200A | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"OR Rm,Rn", 0x200B | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"CMP/STR Rm,Rn", 0x200C | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"XTRCT Rm,Rn", 0x200D | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"MULU.W Rm,Rn", 0x200E | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
		{"MULS.W Rm,Rn", 0x200F | (5 << 8) | (3 << 4), []uint8{5, 3}, []uint8{0, 7}},
	})
}

// TestUsesRegAsSource_Group8 enumerates top-nibble 0x8 opcodes. Note that
// Rn for these store/load forms sits in bits 7-4 (the "m" position in the
// SH-2 general format), not bits 11-8.
func TestUsesRegAsSource_Group8(t *testing.T) {
	runClassify(t, []classifyCase{
		// MOV.B R0,@(disp,Rn): Rn in bits 7-4 = address source; R0 is data forwarded.
		{"MOV.B R0,@(disp,Rn)", 0x8000 | (5 << 4), []uint8{5}, []uint8{0, 3}},
		{"MOV.W R0,@(disp,Rn)", 0x8100 | (5 << 4), []uint8{5}, []uint8{0, 3}},
		{"MOV.B @(disp,Rm),R0", 0x8400 | (5 << 4), []uint8{5}, []uint8{0, 3}},
		{"MOV.W @(disp,Rm),R0", 0x8500 | (5 << 4), []uint8{5}, []uint8{0, 3}},
		{"CMP/EQ #imm,R0", 0x8800, []uint8{0}, []uint8{3, 5, 15}},
		{"BT label", 0x8900 | 0x10, nil, []uint8{0, 3, 5}},
		{"BF label", 0x8B00 | 0x10, nil, []uint8{0, 3, 5}},
		{"BT/S label", 0x8D00 | 0x10, nil, []uint8{0, 3, 5}},
		{"BF/S label", 0x8F00 | 0x10, nil, []uint8{0, 3, 5}},
	})
}

// TestUsesRegAsSource_GroupC enumerates top-nibble 0xC opcodes. GBR-based
// ops do not read any GPR (GBR is not in the GPR hazard set). R0 is
// implicit for several forms.
func TestUsesRegAsSource_GroupC(t *testing.T) {
	runClassify(t, []classifyCase{
		{"MOV.B R0,@(disp,GBR)", 0xC000, nil, []uint8{0, 3, 5}},
		{"MOV.W R0,@(disp,GBR)", 0xC100, nil, []uint8{0, 3, 5}},
		{"MOV.L R0,@(disp,GBR)", 0xC200, nil, []uint8{0, 3, 5}},
		{"TRAPA #imm", 0xC300 | 0x10, nil, []uint8{0, 3, 5, 15}},
		{"MOV.B @(disp,GBR),R0", 0xC400, nil, []uint8{0, 3, 5}}, // R0 is destination, not source
		{"MOV.W @(disp,GBR),R0", 0xC500, nil, []uint8{0, 3, 5}},
		{"MOV.L @(disp,GBR),R0", 0xC600, nil, []uint8{0, 3, 5}},
		{"MOVA @(disp,PC),R0", 0xC700, nil, []uint8{0, 3, 5}},
		{"TST #imm,R0", 0xC800, []uint8{0}, []uint8{3, 5, 15}},
		{"AND #imm,R0", 0xC900, []uint8{0}, []uint8{3, 5, 15}},
		{"XOR #imm,R0", 0xCA00, []uint8{0}, []uint8{3, 5, 15}},
		{"OR #imm,R0", 0xCB00, []uint8{0}, []uint8{3, 5, 15}},
		{"TST.B #imm,@(R0,GBR)", 0xCC00, []uint8{0}, []uint8{3, 5, 15}},
		{"AND.B #imm,@(R0,GBR)", 0xCD00, []uint8{0}, []uint8{3, 5, 15}},
		{"XOR.B #imm,@(R0,GBR)", 0xCE00, []uint8{0}, []uint8{3, 5, 15}},
		{"OR.B #imm,@(R0,GBR)", 0xCF00, []uint8{0}, []uint8{3, 5, 15}},
	})
}

// ------------------------------------------------------------------------
// Group F: delay-slot / branch interaction.
//
// SH7604 Hardware Manual Section 4.6.1 (p.75):
//
//	"When an instruction placed immediately after a delayed branch
//	 instruction (delay slot) is decoded, neither address errors nor
//	 interrupts are accepted. The delayed branch instruction and the
//	 instruction located immediately after it (delay slot) are always
//	 executed consecutively, so no exception handling occurs between the
//	 two."
//
// Programming Manual Section 7.2.2 p.389 rule still applies across
// branch boundaries - the load-use hazard follows pipeline ordering not
// control flow. A load in a delay slot that feeds the branch-target
// instruction still creates an MA-EX overlap.
// ------------------------------------------------------------------------

// TestLoadUseStall_DelaySlotLoadIntoBranchTarget verifies that a load in
// the delay slot causes the branch target instruction to stall if it
// reads the load destination. The load instruction executes last before
// the branch target, so lastLoadReg must carry across the branch.
// Clock sequence (BRA emits popStall(1) per ops_branch.go to model the
// delay-slot ID stall of figure 7.82): BRA | popStall | delay-slot load |
// target stall check | deferred target.
func TestLoadUseStall_DelaySlotLoadIntoBranchTarget(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x400 // data separate from branch target
	cpu.reg.R[0] = 0
	bus.Write32(0x400, 42)

	// 0x100: BRA 0x200 - disp*2 = 0x200 - (0x100+4) = 0xFC -> disp = 0x7E
	bus.Write16(0x100, 0xA000|0x7E)
	bus.Write16(0x102, encMOVLL(1, 2)) // delay slot: load R1->R2
	bus.Write16(0x200, encADD(2, 0))   // target reads R2
	bus.Write16(0x202, encNOP())

	cpu.reg.PC = 0x100
	cpu.Clock() // BRA
	cpu.Clock() // popStall (delay-slot ID stall modeled by opBRA)
	cpu.Clock() // delay-slot load; PC then jumps to 0x200; lastLoadReg=2
	s4 := cpu.Clock()
	if !s4.LoadUseStall {
		t.Error("target ADD R2,R0 after delay-slot load into R2: expected stall (Section 7.2.2, p.389; load-use rule follows pipeline order across branches)")
	}
	cpu.Clock() // deferred ADD executes
	if cpu.reg.R[0] != 42 {
		t.Errorf("R0 = %d after deferred ADD, want 42", cpu.reg.R[0])
	}
}

// TestLoadUseStall_LoadBeforeUnconditionalBranch verifies that a load
// immediately followed by an unconditional branch does not misfire:
// BRA does not read any GPR, so no hazard occurs. Also verifies the
// delay-slot instruction (if non-reading) is not spuriously flagged.
func TestLoadUseStall_LoadBeforeUnconditionalBranch(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x200
	bus.Write32(0x200, 42)

	// load MOV.L @R1,R2; BRA 0x200; delay-slot = NOP; target = NOP
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, 0xA000|0x7E) // BRA 0x200
	bus.Write16(0x104, encNOP())    // delay slot (no source)
	bus.Write16(0x200, encNOP())
	bus.Write16(0x202, encNOP())

	cpu.reg.PC = 0x100
	s1 := cpu.Clock() // load
	if s1.LoadUseStall {
		t.Error("step 1 load: unexpected stall")
	}
	s2 := cpu.Clock() // BRA (no source)
	if s2.LoadUseStall {
		t.Error("step 2 BRA (no GPR source): unexpected stall")
	}
	s3 := cpu.Clock() // delay slot NOP
	if s3.LoadUseStall {
		t.Error("step 3 delay-slot NOP: unexpected stall")
	}
}

// TestLoadUseStall_DelaySlotUsesLoadedReg tests the specific case where
// load-then-branch-then-delay-slot where the delay slot reads the
// previously-loaded register. Per Section 7.4.5 figure 7.82 the delay
// slot ID stalls one cycle, placing the delay-slot EX far enough after
// the load's MA that no additional split is needed. The emulator's
// hazard tracker also clears after the branch instruction (a non-load),
// so no stall should be observed.
func TestLoadUseStall_DelaySlotUsesLoadedReg(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x200
	cpu.reg.R[0] = 0
	bus.Write32(0x200, 42)

	bus.Write16(0x100, encMOVLL(1, 2)) // load into R2
	bus.Write16(0x102, 0xA000|0x7E)    // BRA 0x200
	bus.Write16(0x104, encADD(2, 0))   // delay slot reads R2
	bus.Write16(0x200, encNOP())

	cpu.reg.PC = 0x100
	cpu.Clock() // load
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("step 2 BRA: unexpected stall (BRA has no source)")
	}
	s3 := cpu.Clock()
	if s3.LoadUseStall {
		t.Error("step 3 delay slot: unexpected stall (load-to-delay-slot gap covers MA/EX; non-load BRA between cleared tracker)")
	}
}

// ------------------------------------------------------------------------
// Group G: multi-cycle pending-op vs load-use sequencing.
//
// SH-1/SH-2 Programming Manual Section 7.2.2 (p.389) rule "any slot
// containing the MA of instruction 1 and EX of instruction 2 will split"
// applies when instruction 2 is a multi-cycle instruction too - the
// hazard must be detected BEFORE the multi-cycle pending state machine
// starts, because the pending state only covers cycles after the first
// EX stage. Also: the hazard rule covers only the immediately following
// instruction; after any intervening non-load instruction (including a
// multi-cycle op), the tracker must not fire for later uses.
// ------------------------------------------------------------------------

// TestLoadUseStall_TASAfterLoad covers TAS.B @Rn (multi-cycle popTAS)
// as the follower. TAS reads Rn as the address source. Per Table 7.2
// (TAS pipeline figure 7.76) TAS has stages IF/ID/EX/MA/EX/MA - its
// EX-stage source read overlaps with the previous load's MA when Rn
// matches. A stall is required.
func TestLoadUseStall_TASAfterLoad(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x400
	bus.Write32(0x400, 0x300)          // loaded value = TAS target address
	bus.Write8(0x300, 0x01)            // TAS target byte
	bus.Write16(0x100, encMOVLL(1, 2)) // load into R2
	bus.Write16(0x102, encTAS(2))      // TAS.B @R2
	bus.Write16(0x104, encNOP())
	cpu.reg.PC = 0x100
	cpu.Clock()
	s2 := cpu.Clock()
	if !s2.LoadUseStall {
		t.Error("TAS.B @R2 after load into R2: expected stall (TAS reads Rn as address, Section 7.2.2 p.389)")
	}
}

// TestLoadUseStall_RTEAfterLoadR15 covers the case where R15 (the
// hardware stack pointer used by RTE to pop PC and SR) is the load
// destination. RTE reads R15 as a source during its pending-op
// sequence, so the hazard must be caught before RTE begins.
func TestLoadUseStall_RTEAfterLoadR15(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x400
	bus.Write32(0x400, 0x800)           // load target for R15 (valid stack top)
	bus.Write32(0x800, 0x200)           // stacked PC
	bus.Write32(0x804, 0x00)            // stacked SR
	bus.Write16(0x100, encMOVLL(1, 15)) // load into R15
	bus.Write16(0x102, encRTE())
	bus.Write16(0x200, encNOP())
	cpu.reg.PC = 0x100
	cpu.Clock()
	s2 := cpu.Clock()
	if !s2.LoadUseStall {
		t.Error("RTE after load into R15: expected stall (RTE reads R15 as stack pointer source, Section 7.2.2 p.389)")
	}
}

// TestLoadUseStall_TrapaAfterLoad covers TRAPA #imm as the follower -
// TRAPA reads no GPR (only reads PC/SR and VBR/imm), so the doc rule
// does NOT trigger a split even after a load. No stall is expected.
// This is a negative test (distinct from the R0 implicit-users, which
// DO stall for R0-reading immediates).
func TestLoadUseStall_TrapaAfterLoad(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x400
	bus.Write32(0x400, 0xAA)
	bus.Write32(0x00, 0x200) // vector 0 -> handler at 0x200
	bus.Write16(0x100, encMOVLL(1, 2))
	bus.Write16(0x102, 0xC300) // TRAPA #0
	bus.Write16(0x200, encNOP())
	cpu.reg.PC = 0x100
	cpu.Clock()
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("TRAPA #imm after load: unexpected stall (TRAPA has no GPR source, Section 7.2.2 p.389 rule does not apply)")
	}
}

// TestLoadUseStall_STCLAfterLoadAddressReg covers STC.L GBR,@-Rn where
// Rn matches a prior load destination. STC.L is multi-cycle (popSTCL)
// and uses Rn as the address for pre-decrement memory write, so the
// load's destination feeding Rn must trigger a stall.
func TestLoadUseStall_STCLAfterLoadAddressReg(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x400
	bus.Write32(0x400, 0x500) // loaded value = target base address
	// STC.L GBR,@-Rn: opcode 0100nnnn00010011 = 0x4013 | (n<<8)
	bus.Write16(0x100, encMOVLL(1, 2)) // load into R2
	bus.Write16(0x102, 0x4013|(2<<8))  // STC.L GBR,@-R2
	bus.Write16(0x104, encNOP())
	cpu.reg.PC = 0x100
	cpu.Clock()
	s2 := cpu.Clock()
	if !s2.LoadUseStall {
		t.Error("STC.L GBR,@-R2 after load into R2: expected stall (Rn used as address, Section 7.2.2 p.389)")
	}
}

// TestLoadUseStall_OnlyImmediateFollower verifies that the Section 7.2.2
// rule ("any slot containing the MA of instruction 1 and EX of instruction 2
// will split") applies only to the IMMEDIATELY following instruction,
// not to later instructions in the stream. Placing a non-hazardous
// instruction between the load and the use must clear the hazard.
func TestLoadUseStall_OnlyImmediateFollower(t *testing.T) {
	cpu, bus := setupHazardTest()
	cpu.reg.R[1] = 0x400
	cpu.reg.R[0] = 0
	bus.Write32(0x400, 42)
	bus.Write16(0x100, encMOVLL(1, 2)) // load into R2
	bus.Write16(0x102, encNOP())       // intervening non-load
	bus.Write16(0x104, encADD(2, 0))   // uses R2 - should NOT stall
	bus.Write16(0x106, encNOP())
	cpu.reg.PC = 0x100
	cpu.Clock() // load
	s2 := cpu.Clock()
	if s2.LoadUseStall {
		t.Error("NOP after load: unexpected stall")
	}
	s3 := cpu.Clock()
	if s3.LoadUseStall {
		t.Error("ADD R2,R0 after NOP: unexpected stall (load-use rule covers only immediate follower, Section 7.2.2 p.389)")
	}
	if cpu.reg.R[0] != 42 {
		t.Errorf("R0 = %d, want 42", cpu.reg.R[0])
	}
}

// ------------------------------------------------------------------------
// Group H: variance cases - tests derived from documented hardware
// behavior that may or may not be modeled. Failures here are findings,
// not bugs in the test. Each test cites the doc section and the
// doc-predicted cycle counts; comments note what the emulator's current
// cycle-charging code does for context.
//
// These use cpu.Cycles() deltas to detect whether a contention mechanism
// is modeled at the pipeline level. They do not call loadUseHazard()
// directly - those contentions live outside the hazard.go rule set.
// ------------------------------------------------------------------------

// clocksToReachPC runs Clock() calls starting from PC=0x100 until the
// CPU's PC reaches targetPC or maxClocks is exceeded. Returns the
// number of clocks consumed. The CPU's cycles counter increments by
// exactly 1 per Clock() call (whether the clock runs an instruction,
// a pending-op step, or a hazard stall), so this metric mirrors cycles.
func clocksToReachPC(t *testing.T, targetPC uint32, maxClocks int, prepare func(c *CPU, b *testBus)) int {
	t.Helper()
	cpu, bus := setupHazardTest()
	prepare(cpu, bus)
	cpu.reg.PC = 0x100
	for i := 0; i < maxClocks; i++ {
		if cpu.reg.PC == targetPC {
			return i
		}
		cpu.Clock()
	}
	if cpu.reg.PC != targetPC {
		t.Fatalf("PC never reached 0x%04X within %d clocks (final PC=0x%04X)", targetPC, maxClocks, cpu.reg.PC)
	}
	return maxClocks
}

// TestMultiplierContention_MACL_MACL_BackToBack covers Programming Manual
// Section 7.2.3 (p.392) and figure 7.18: when MAC.L follows MAC.L, the
// second MAC.L's MA stalls until the preceding instruction's mm stages
// complete. Compared to a sequence with a non-multiplier NOP between
// the two MAC.L instructions (which also stalls somewhat in this
// simplified model but less than back-to-back), contending clocks must
// exceed baseline clocks.
func TestMultiplierContention_MACL_MACL_BackToBack(t *testing.T) {
	dataAt := func(c *CPU, b *testBus) {
		c.reg.R[1] = 0x400
		c.reg.R[2] = 0x440
		b.Write32(0x400, 3)
		b.Write32(0x404, 5)
		b.Write32(0x440, 7)
		b.Write32(0x444, 11)
	}
	const sentinel = 0x10A
	baseline := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		dataAt(c, b)
		b.Write16(0x100, encMACL(1, 2)) // MAC.L @R1+,@R2+
		b.Write16(0x102, encNOP())
		b.Write16(0x104, encMACL(1, 2))
		b.Write16(0x106, encNOP())
		b.Write16(0x108, encNOP())
	})
	contending := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		dataAt(c, b)
		b.Write16(0x100, encMACL(1, 2))
		b.Write16(0x102, encMACL(1, 2)) // back-to-back: contends
		b.Write16(0x104, encNOP())
		b.Write16(0x106, encNOP())
		b.Write16(0x108, encNOP())
	})
	t.Logf("clocks: baseline(MAC.L+NOP+MAC.L)=%d contending(MAC.L+MAC.L)=%d", baseline, contending)
	if contending <= baseline {
		t.Errorf("multiplier contention: contending=%d must exceed baseline=%d (Section 7.2.3 p.392, figure 7.18)",
			contending, baseline)
	}
}

// TestMultiplierContention_MULL_MACL covers MUL.L followed immediately
// by MAC.L. MUL.L has 4 mm cycles after its MA; MAC.L's MA contends.
// Programming Manual Section 7.2.3 Table 7.1 lists MAC.L as 2 to 4
// cycles depending on contention.
func TestMultiplierContention_MULL_MACL(t *testing.T) {
	dataAt := func(c *CPU, b *testBus) {
		c.reg.R[1] = 0x400
		c.reg.R[2] = 0x440
		b.Write32(0x400, 3)
		b.Write32(0x440, 5)
	}
	const sentinel = 0x10A
	baseline := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		dataAt(c, b)
		b.Write16(0x100, encMULL(1, 2))
		b.Write16(0x102, encNOP())
		b.Write16(0x104, encMACL(1, 2))
		b.Write16(0x106, encNOP())
		b.Write16(0x108, encNOP())
	})
	contending := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		dataAt(c, b)
		b.Write16(0x100, encMULL(1, 2))
		b.Write16(0x102, encMACL(1, 2)) // back-to-back: contends
		b.Write16(0x104, encNOP())
		b.Write16(0x106, encNOP())
		b.Write16(0x108, encNOP())
	})
	t.Logf("clocks: baseline(MUL.L+NOP+MAC.L)=%d contending(MUL.L+MAC.L)=%d", baseline, contending)
	if contending <= baseline {
		t.Errorf("multiplier contention: contending=%d must exceed baseline=%d (Section 7.2.3 p.392)",
			contending, baseline)
	}
}

// TestMultiplierContention_MACL_MULL covers MAC.L followed immediately
// by MUL.L. MAC.L's mm runs 4 cycles after its second MA; MUL.L's MA
// contends. Programming Manual Section 7.2.3 Table 7.1 lists MUL.L as
// 2 to 4 cycles depending on contention.
func TestMultiplierContention_MACL_MULL(t *testing.T) {
	dataAt := func(c *CPU, b *testBus) {
		c.reg.R[1] = 0x400
		c.reg.R[2] = 0x440
		b.Write32(0x400, 3)
		b.Write32(0x440, 5)
	}
	const sentinel = 0x10A
	baseline := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		dataAt(c, b)
		b.Write16(0x100, encMACL(1, 2))
		b.Write16(0x102, encNOP())
		b.Write16(0x104, encMULL(1, 2))
		b.Write16(0x106, encNOP())
		b.Write16(0x108, encNOP())
	})
	contending := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		dataAt(c, b)
		b.Write16(0x100, encMACL(1, 2))
		b.Write16(0x102, encMULL(1, 2)) // back-to-back: contends
		b.Write16(0x104, encNOP())
		b.Write16(0x106, encNOP())
		b.Write16(0x108, encNOP())
	})
	t.Logf("clocks: baseline(MAC.L+NOP+MUL.L)=%d contending(MAC.L+MUL.L)=%d", baseline, contending)
	if contending <= baseline {
		t.Errorf("multiplier contention: contending=%d must exceed baseline=%d (Section 7.2.3 p.392)",
			contending, baseline)
	}
}

// TestMultiplierContention_NOPsClearContention verifies that non-
// multiplier instructions between two multipliers allow the first
// instruction's mm stages to drain, eliminating stall cycles on the
// second. Programming Manual Section 7.2.3 p.392: "If one or more
// instruction not related to the multiplier is located between the
// multiplier instructions, multiplier contention between MAC
// instructions does not cause stalls."
//
// Uses enough NOPs between two MUL.Ls to fully drain the preceding
// mm window. The two MUL.Ls must then consume exactly their baseline
// 2 cycles each, with no extra stall cycles inserted. A trailing NOP
// after MUL.L2 ensures clocksToReachPC measures MUL.L2's popStall
// cycle before the sentinel is reached.
func TestMultiplierContention_NOPsClearContention(t *testing.T) {
	const nNops = 10
	// Layout: MUL.L at 0x100, nNops NOPs, MUL.L, 1 trailing NOP, sentinel.
	const sentinel = 0x100 + 2*(1+nNops+1+1)
	expected := 2 + nNops + 2 + 1 // clocks: MUL.L (2) + nNops + MUL.L (2) + trailing NOP (1)
	clocks := clocksToReachPC(t, sentinel, 30, func(c *CPU, b *testBus) {
		c.reg.R[1] = 3
		c.reg.R[2] = 5
		b.Write16(0x100, encMULL(1, 2))
		for i := 0; i < nNops; i++ {
			b.Write16(uint32(0x102+i*2), encNOP())
		}
		b.Write16(uint32(0x102+nNops*2), encMULL(1, 2))
		b.Write16(uint32(0x104+nNops*2), encNOP())
	})
	t.Logf("clocks: MUL.L + %d*NOP + MUL.L + NOP took %d, expected %d (no contention; per Section 7.2.3 p.392)",
		nNops, clocks, expected)
	if clocks != expected {
		t.Errorf("clocks = %d, want %d: NOPs between multipliers should fully drain mm contention (Section 7.2.3 p.392)",
			clocks, expected)
	}
}

// TestMACRegAccessNoMultiplierStall_MACL_STSMACL verifies that
// STS MACL,Rn following MAC.L does NOT stall on multiplier contention.
// Per Programming Manual Section 7.2.3, mm-stage contention extends only
// the MA of a subsequent multiplier-type instruction (MUL/MAC). STS to a
// MAC register is not a multiplier-type instruction; Table 7.1 lists it
// as a flat 1 cycle with no parenthesized contention range.
// Expected total = MAC.L(2) + STS(1) + trailing NOP(1) = 4 clocks.
func TestMACRegAccessNoMultiplierStall_MACL_STSMACL(t *testing.T) {
	const sentinel = 0x106
	clocks := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		c.reg.R[1] = 0x400
		c.reg.R[2] = 0x440
		b.Write32(0x400, 3)
		b.Write32(0x440, 5)
		b.Write16(0x100, encMACL(1, 2))
		b.Write16(0x102, encSTSMACL(5))
		b.Write16(0x104, encNOP())
	})
	const expected = 4
	t.Logf("clocks: MAC.L + STS MACL,R5 + NOP took %d, expected %d (Table 7.1: MAC.L=2 + STS=1 + NOP=1)",
		clocks, expected)
	if clocks != expected {
		t.Errorf("clocks = %d, want %d (Section 7.2.3: mm contention applies to subsequent multiplier-type instructions only)", clocks, expected)
	}
}

// TestMACRegAccessNoMultiplierStall_MULL_LDSMACH verifies that
// LDS Rm,MACH following MUL.L does NOT stall on multiplier contention.
// Expected total = MUL.L(2) + LDS(1) + trailing NOP(1) = 4 clocks.
func TestMACRegAccessNoMultiplierStall_MULL_LDSMACH(t *testing.T) {
	const sentinel = 0x106
	clocks := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		c.reg.R[1] = 3
		c.reg.R[2] = 5
		c.reg.R[5] = 0xAA
		b.Write16(0x100, encMULL(1, 2))
		b.Write16(0x102, encLDSMACH(5))
		b.Write16(0x104, encNOP())
	})
	const expected = 4
	t.Logf("clocks: MUL.L + LDS R5,MACH + NOP took %d, expected %d (Table 7.1: MUL.L=2 + LDS=1 + NOP=1)",
		clocks, expected)
	if clocks != expected {
		t.Errorf("clocks = %d, want %d (Section 7.2.3: mm contention applies to subsequent multiplier-type instructions only)", clocks, expected)
	}
}

// TestMACRegAccessNoMultiplierStall_MACL_CLRMAC verifies that CLRMAC
// following MAC.L does NOT stall on multiplier contention.
// Expected total = MAC.L(2) + CLRMAC(1) + trailing NOP(1) = 4 clocks.
func TestMACRegAccessNoMultiplierStall_MACL_CLRMAC(t *testing.T) {
	const sentinel = 0x106
	clocks := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		c.reg.R[1] = 0x400
		c.reg.R[2] = 0x440
		b.Write32(0x400, 3)
		b.Write32(0x440, 5)
		b.Write16(0x100, encMACL(1, 2))
		b.Write16(0x102, encCLRMAC())
		b.Write16(0x104, encNOP())
	})
	const expected = 4
	t.Logf("clocks: MAC.L + CLRMAC + NOP took %d, expected %d (Table 7.1: MAC.L=2 + CLRMAC=1 + NOP=1)",
		clocks, expected)
	if clocks != expected {
		t.Errorf("clocks = %d, want %d (Section 7.2.3: mm contention applies to subsequent multiplier-type instructions only)", clocks, expected)
	}
}

// TestMACRegAccessNoMultiplierStall_MULL_STSLMACL verifies that
// STS.L MACL,@-Rn following MUL.L does NOT stall on multiplier contention.
// Expected total = MUL.L(2) + STS.L(1) + trailing NOP(1) = 4 clocks.
func TestMACRegAccessNoMultiplierStall_MULL_STSLMACL(t *testing.T) {
	const sentinel = 0x106
	clocks := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		c.reg.R[1] = 3
		c.reg.R[2] = 5
		c.reg.R[5] = 0x800
		b.Write16(0x100, encMULL(1, 2))
		b.Write16(0x102, encSTSLMACL(5))
		b.Write16(0x104, encNOP())
	})
	const expected = 4
	t.Logf("clocks: MUL.L + STS.L MACL,@-R5 + NOP took %d, expected %d (Table 7.1: MUL.L=2 + STS.L=1 + NOP=1)",
		clocks, expected)
	if clocks != expected {
		t.Errorf("clocks = %d, want %d (Section 7.2.3: mm contention applies to subsequent multiplier-type instructions only)", clocks, expected)
	}
}

// TestMultiplierContention_ClampDMULSLtoMULSW verifies that the stall
// inserted by a 16-bit-follower (MULS.W) after a 32-bit-preceding
// (DMULS.L) is capped to the Table 7.1 max of 2 extra cycles. Without
// the cap the raw timestamp arithmetic would produce 3 extra stall
// cycles (overshooting MULS.W's 3-cycle max by 1). This test uses an
// exact-clock assertion because only exact values distinguish the
// clamped model from the uncapped one.
//
// Per Programming Manual Table 7.1:
//   - DMULS.L min = 2 cycles (EX + MA)
//   - MULS.W max = 3 cycles (1 + 2 extra)
//   - Plus 1 trailing NOP so the sentinel is reached after MULS.W's
//     pending stall cycles complete.
//     Expected total = 2 + 3 + 1 = 6 clocks.
func TestMultiplierContention_ClampDMULSLtoMULSW(t *testing.T) {
	const sentinel = 0x106
	clocks := clocksToReachPC(t, sentinel, 20, func(c *CPU, b *testBus) {
		c.reg.R[1] = 3
		c.reg.R[2] = 5
		b.Write16(0x100, encDMULSL(1, 2))
		b.Write16(0x102, encMULSW(1, 2))
		b.Write16(0x104, encNOP())
	})
	const expected = 6
	t.Logf("clocks: DMULS.L + MULS.W + NOP took %d, expected %d (Table 7.1 max per-follower; DMULS.L=2 + MULS.W max=3 + NOP=1)",
		clocks, expected)
	if clocks != expected {
		t.Errorf("clocks = %d, want %d: stall should be clamped to Table 7.1 max of 2 extra for MULS.W (Section 7.2.3 Table 7.1)",
			clocks, expected)
	}
}
