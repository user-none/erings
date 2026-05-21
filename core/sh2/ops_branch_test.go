// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// nopOpcode is 0x0009 (NOP) used as delay slot filler.
const nopOpcode = 0x0009

// TestOpBT tests BT (branch if true, non-delayed).
func TestOpBT(t *testing.T) {
	t.Run("taken_positive", func(t *testing.T) {
		// BT +4 -> disp=2, target = (0x10+4) + 2*2 = 0x18
		// Encoding: 1000 1001 dddddddd, disp=2 -> 0x8902
		cpu := newDecodeTestCPU(0x8902)
		cpu.reg.SetT()
		// Step 1: EX (branch taken), steps 2-3: pipeline refill
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set on EX cycle")
		}
		// 2 more stall cycles
		for i := 0; i < 2; i++ {
			cpu.Clock()
		}
		if cpu.reg.PC != 0x18 {
			t.Errorf("PC = 0x%08X, want 0x18", cpu.reg.PC)
		}
		// Total cycles consumed should be 3
		if cpu.cycles != 3 {
			t.Errorf("total cycles = %d, want 3", cpu.cycles)
		}
	})

	t.Run("taken_negative", func(t *testing.T) {
		// BT -4 -> disp=-2 (0xFE), target = (0x10+4) + (-2)*2 = 0x10
		cpu := newDecodeTestCPU(0x89FE)
		cpu.reg.SetT()
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.PC != 0x10 {
			t.Errorf("PC = 0x%08X, want 0x10", cpu.reg.PC)
		}
	})

	t.Run("not_taken", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8902)
		cpu.reg.ClearT()
		s := cpu.Clock()
		// PC should just advance past the instruction
		if cpu.reg.PC != 0x12 {
			t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		if s.BranchTaken {
			t.Error("BranchTaken should not be set when not taken")
		}
	})
}

// TestOpBF tests BF (branch if false, non-delayed).
func TestOpBF(t *testing.T) {
	t.Run("taken_positive", func(t *testing.T) {
		// BF +6 -> disp=3, target = (0x10+4) + 3*2 = 0x1A
		cpu := newDecodeTestCPU(0x8B03)
		cpu.reg.ClearT()
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.PC != 0x1A {
			t.Errorf("PC = 0x%08X, want 0x1A", cpu.reg.PC)
		}
	})

	t.Run("taken_negative", func(t *testing.T) {
		// BF -6 -> disp=-3 (0xFD), target = (0x10+4) + (-3)*2 = 0x0E
		cpu := newDecodeTestCPU(0x8BFD)
		cpu.reg.ClearT()
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.PC != 0x0E {
			t.Errorf("PC = 0x%08X, want 0x0E", cpu.reg.PC)
		}
	})

	t.Run("not_taken", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8B03)
		cpu.reg.SetT()
		cpu.Clock()
		if cpu.reg.PC != 0x12 {
			t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
	})
}

// TestOpBTS tests BT/S (branch if true with delay slot).
func TestOpBTS(t *testing.T) {
	t.Run("taken", func(t *testing.T) {
		// BT/S +4 -> disp=2, target = (0x10+4) + 2*2 = 0x18
		cpu := newDecodeTestCPU(0x8D02)
		cpu.reg.SetT()
		// Write NOP at 0x12 as delay slot
		cpu.bus.Write16(0x12, nopOpcode)
		// Step 1: executes BT/S, sets up delayed branch + popStall(1)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		// PC should be at delay slot (0x12) after fetch
		if cpu.reg.PC != 0x12 {
			t.Errorf("after BT/S step: PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		// Step 2: delay-slot ID stall (popStall)
		cpu.Clock()
		// Step 3: executes delay slot NOP, then branch takes effect
		cpu.Clock()
		if cpu.reg.PC != 0x18 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x18", cpu.reg.PC)
		}
	})

	t.Run("not_taken", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8D02)
		cpu.reg.ClearT()
		s := cpu.Clock()
		if cpu.reg.PC != 0x12 {
			t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		if cpu.inDelay {
			t.Error("inDelay should be false when not taken")
		}
		if s.BranchTaken {
			t.Error("BranchTaken should not be set when not taken")
		}
	})
}

// TestOpBFS tests BF/S (branch if false with delay slot).
func TestOpBFS(t *testing.T) {
	t.Run("taken", func(t *testing.T) {
		// BF/S +4 -> disp=2, target = (0x10+4) + 2*2 = 0x18
		cpu := newDecodeTestCPU(0x8F02)
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, nopOpcode)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		if cpu.reg.PC != 0x12 {
			t.Errorf("after BF/S step: PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		// Step 2: delay-slot ID stall (popStall)
		cpu.Clock()
		// Step 3: delay slot executes, branch takes effect
		cpu.Clock()
		if cpu.reg.PC != 0x18 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x18", cpu.reg.PC)
		}
	})

	t.Run("not_taken", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8F02)
		cpu.reg.SetT()
		cpu.Clock()
		if cpu.reg.PC != 0x12 {
			t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		if cpu.inDelay {
			t.Error("inDelay should be false when not taken")
		}
	})
}

// TestOpBRA tests BRA (unconditional delayed branch with 12-bit displacement).
func TestOpBRA(t *testing.T) {
	t.Run("positive_disp", func(t *testing.T) {
		// BRA +20 -> disp=10 (0x00A), target = (0x10+4) + 10*2 = 0x28
		cpu := newDecodeTestCPU(0xA00A)
		cpu.bus.Write16(0x12, nopOpcode)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		if cpu.reg.PC != 0x12 {
			t.Errorf("after BRA step: PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		// Step 2: delay-slot ID stall (popStall)
		cpu.Clock()
		// Step 3: delay slot executes, branch takes effect
		cpu.Clock()
		if cpu.reg.PC != 0x28 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x28", cpu.reg.PC)
		}
	})

	t.Run("negative_disp", func(t *testing.T) {
		// BRA -4 -> disp=-2 (0xFFE), target = (0x10+4) + (-2)*2 = 0x10
		cpu := newDecodeTestCPU(0xAFFE)
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock() // BRA
		cpu.Clock() // stall
		cpu.Clock() // delay slot
		if cpu.reg.PC != 0x10 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x10", cpu.reg.PC)
		}
	})
}

// TestOpBSR tests BSR (branch to subroutine with delay slot).
func TestOpBSR(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// BSR +20 -> disp=10, target = (0x10+4) + 10*2 = 0x28
		cpu := newDecodeTestCPU(0xB00A)
		cpu.bus.Write16(0x12, nopOpcode)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		// PR should be set to instruction after delay slot = 0x10+4 = 0x14
		if cpu.reg.PR != 0x14 {
			t.Errorf("PR = 0x%08X, want 0x14", cpu.reg.PR)
		}
		cpu.Clock() // stall
		cpu.Clock() // delay slot
		if cpu.reg.PC != 0x28 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x28", cpu.reg.PC)
		}
	})
}

// TestOpBRAF tests BRAF Rm (register-relative delayed branch).
func TestOpBRAF(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// BRAF R3: target = (0x10+4) + R3
		cpu := newDecodeTestCPU(0x0323)
		cpu.reg.R[3] = 0x20
		cpu.bus.Write16(0x12, nopOpcode)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		if cpu.reg.PC != 0x12 {
			t.Errorf("after BRAF step: PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		cpu.Clock() // stall
		cpu.Clock() // delay slot
		// target = 0x14 + 0x20 = 0x34
		if cpu.reg.PC != 0x34 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x34", cpu.reg.PC)
		}
	})
}

// TestOpBSRF tests BSRF Rm (register-relative call with delay slot).
func TestOpBSRF(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// BSRF R3: target = (0x10+4) + R3, PR = 0x10+4
		cpu := newDecodeTestCPU(0x0303)
		cpu.reg.R[3] = 0x20
		cpu.bus.Write16(0x12, nopOpcode)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		if cpu.reg.PR != 0x14 {
			t.Errorf("PR = 0x%08X, want 0x14", cpu.reg.PR)
		}
		cpu.Clock() // stall
		cpu.Clock() // delay slot
		// target = 0x14 + 0x20 = 0x34
		if cpu.reg.PC != 0x34 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x34", cpu.reg.PC)
		}
	})
}

// TestOpJMP tests JMP @Rm (register indirect delayed branch).
func TestOpJMP(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// JMP @R3: target = R3
		cpu := newDecodeTestCPU(0x432B)
		cpu.reg.R[3] = 0x200
		cpu.bus.Write16(0x12, nopOpcode)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		if cpu.reg.PC != 0x12 {
			t.Errorf("after JMP step: PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		cpu.Clock() // stall
		cpu.Clock() // delay slot
		if cpu.reg.PC != 0x200 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x200", cpu.reg.PC)
		}
	})
}

// TestOpJSR tests JSR @Rm (register indirect call with delay slot).
func TestOpJSR(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// JSR @R3: target = R3, PR = instruction after delay slot
		cpu := newDecodeTestCPU(0x430B)
		cpu.reg.R[3] = 0x200
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		// PR = 0x10+4 = 0x14
		if cpu.reg.PR != 0x14 {
			t.Errorf("PR = 0x%08X, want 0x14", cpu.reg.PR)
		}
		cpu.Clock() // stall
		cpu.Clock() // delay slot
		if cpu.reg.PC != 0x200 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x200", cpu.reg.PC)
		}
	})
}

// TestOpRTS tests RTS (return from subroutine with delay slot).
func TestOpRTS(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// RTS: delayed branch to PR
		cpu := newDecodeTestCPU(0x000B)
		cpu.reg.PR = 0x300
		cpu.bus.Write16(0x12, nopOpcode)
		s := cpu.Clock()
		if !s.BranchTaken {
			t.Error("BranchTaken not set")
		}
		if cpu.reg.PC != 0x12 {
			t.Errorf("after RTS step: PC = 0x%08X, want 0x12", cpu.reg.PC)
		}
		cpu.Clock() // stall
		cpu.Clock() // delay slot
		if cpu.reg.PC != 0x300 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x300", cpu.reg.PC)
		}
	})
}

// TestOpRTE tests RTE (return from exception with delay slot).
// Per SH-2 Programming Manual Appendix A, RTE = 4 cycles. Our
// implementation uses 1 (opRTE) + 3 (popRTE steps: MA read PC,
// MA read SR, delay-slot ID stall) = 4 cycles, followed by the
// delay-slot instruction (1 cycle) = 5 Clock() calls total.
func TestOpRTE(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		// RTE: pop PC then SR from stack, delayed branch to popped PC
		cpu := newDecodeTestCPU(0x002B)
		// Set up stack matching serviceException layout:
		// [R15] = PC, [R15+4] = SR
		sp := uint32(0x100)
		cpu.reg.R[15] = sp
		cpu.bus.Write32(sp, 0x400)             // saved PC
		cpu.bus.Write32(sp+4, srTMask|srSMask) // saved SR with T=1, S=1
		cpu.bus.Write16(0x12, nopOpcode)

		// Cycle 1: EX
		cpu.Clock()

		// Cycle 2: MA read PC
		s2 := cpu.Clock()
		if s2.Bus != BusRead {
			t.Errorf("cycle 2: bus = %d, want BusRead", s2.Bus)
		}

		// Cycle 3: MA read SR, set up delay branch
		s3 := cpu.Clock()
		if s3.Bus != BusRead {
			t.Errorf("cycle 3: bus = %d, want BusRead", s3.Bus)
		}

		// Cycle 4: delay-slot ID stall
		cpu.Clock()

		// After pending clears, PC should be at delay slot
		if cpu.reg.PC != 0x12 {
			t.Errorf("after RTE pending: PC = 0x%08X, want 0x12", cpu.reg.PC)
		}

		// Cycle 5: delay slot
		cpu.Clock()
		if cpu.reg.PC != 0x400 {
			t.Errorf("after delay slot: PC = 0x%08X, want 0x400", cpu.reg.PC)
		}
		// SR should be restored
		if cpu.reg.T() != 1 {
			t.Error("T bit not restored")
		}
		if !cpu.reg.S() {
			t.Error("S bit not restored")
		}
		// SP should be restored (popped 2 longs)
		if cpu.reg.R[15] != sp+8 {
			t.Errorf("SP = 0x%08X, want 0x%08X", cpu.reg.R[15], sp+8)
		}
	})

	t.Run("sr_masked", func(t *testing.T) {
		// Verify SR bits outside srMask are cleared
		cpu := newDecodeTestCPU(0x002B)
		sp := uint32(0x100)
		cpu.reg.R[15] = sp
		cpu.bus.Write32(sp, 0x400)
		cpu.bus.Write32(sp+4, 0xFFFFFFFF) // all bits set
		cpu.bus.Write16(0x12, nopOpcode)
		// 4 cycles for RTE + 1 for delay slot
		for i := 0; i < 5; i++ {
			cpu.Clock()
		}
		if cpu.reg.SR != srMask {
			t.Errorf("SR = 0x%08X, want 0x%08X", cpu.reg.SR, srMask)
		}
	})

	t.Run("total_cycles", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x002B)
		sp := uint32(0x100)
		cpu.reg.R[15] = sp
		cpu.bus.Write32(sp, 0x400)
		cpu.bus.Write32(sp+4, 0)
		cpu.bus.Write16(0x12, nopOpcode)
		// RTE = 4 cycles + 1 delay slot = 5 total
		for i := 0; i < 5; i++ {
			cpu.Clock()
		}
		if cpu.cycles != 5 {
			t.Errorf("total cycles = %d, want 5", cpu.cycles)
		}
	})
}

// settOpcode is the SETT opcode (0x0018) used as a delay-slot filler
// whose side effect is observable via T.
const settOpcode = 0x0018

// TestOpBTBoundaries covers BT with disp=0, max positive (+127), and
// max negative (-128) displacements (PM section 6.12).
func TestOpBTBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		wantPC uint32
	}{
		{"bt_disp_zero", 0x8900, 0x14},
		{"bt_disp_max_positive", 0x897F, 0x0112},     // 0x14 + 127*2
		{"bt_disp_max_negative", 0x8980, 0xFFFFFF14}, // 0x14 - 128*2
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.SetT()
			before := cpu.cycles
			cpu.Clock()
			cpu.Clock()
			cpu.Clock()
			if cpu.reg.PC != tt.wantPC {
				t.Errorf("PC = 0x%08X, want 0x%08X", cpu.reg.PC, tt.wantPC)
			}
			if int(cpu.cycles-before) != 3 {
				t.Errorf("cycles = %d, want 3 (taken)", cpu.cycles-before)
			}
		})
	}
}

// TestOpBTCyclesNotTaken covers BT 1-cycle not-taken case (PM section 6.12).
func TestOpBTCyclesNotTaken(t *testing.T) {
	cpu := newDecodeTestCPU(0x8902)
	cpu.reg.ClearT()
	before := cpu.cycles
	cpu.Clock()
	if int(cpu.cycles-before) != 1 {
		t.Errorf("cycles = %d, want 1 (not taken)", cpu.cycles-before)
	}
}

// TestOpBFBoundaries covers BF with disp=0, max positive, and max
// negative displacements (PM section 6.10).
func TestOpBFBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		wantPC uint32
	}{
		{"bf_disp_zero", 0x8B00, 0x14},
		{"bf_disp_max_positive", 0x8B7F, 0x0112},
		{"bf_disp_max_negative", 0x8B80, 0xFFFFFF14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.ClearT()
			before := cpu.cycles
			cpu.Clock()
			cpu.Clock()
			cpu.Clock()
			if cpu.reg.PC != tt.wantPC {
				t.Errorf("PC = 0x%08X, want 0x%08X", cpu.reg.PC, tt.wantPC)
			}
			if int(cpu.cycles-before) != 3 {
				t.Errorf("cycles = %d, want 3 (taken)", cpu.cycles-before)
			}
		})
	}
}

// TestOpBFCyclesNotTaken verifies BF consumes 1 cycle when not taken.
func TestOpBFCyclesNotTaken(t *testing.T) {
	cpu := newDecodeTestCPU(0x8B02)
	cpu.reg.SetT()
	before := cpu.cycles
	cpu.Clock()
	if int(cpu.cycles-before) != 1 {
		t.Errorf("cycles = %d, want 1 (not taken)", cpu.cycles-before)
	}
}

// TestOpBTSDelaySlot verifies that the delay-slot instruction executes
// when BT/S is taken and is not executed when not taken (PM section 6.13).
func TestOpBTSDelaySlot(t *testing.T) {
	t.Run("taken_delay_slot_executes", func(t *testing.T) {
		// BT/S +4: 0x8D02, delay slot = SETT
		cpu := newDecodeTestCPU(0x8D02)
		cpu.reg.SetT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (SETT in delay slot)", cpu.reg.T())
		}
		if cpu.reg.PC != 0x18 {
			t.Errorf("PC = 0x%08X, want 0x18", cpu.reg.PC)
		}
	})
	t.Run("not_taken_delay_slot_does_not_execute", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8D02)
		cpu.reg.ClearT()
		// If BT/S were to execute the delay slot, SETT would set T=1.
		cpu.bus.Write16(0x12, settOpcode)
		before := cpu.cycles
		cpu.Clock()
		if cpu.reg.T() != 0 {
			t.Errorf("T = %d, want 0 (delay slot must not execute)", cpu.reg.T())
		}
		if int(cpu.cycles-before) != 1 {
			t.Errorf("cycles = %d, want 1 (not taken)", cpu.cycles-before)
		}
	})
}

// TestOpBFSDelaySlot verifies BF/S delay-slot execution semantics
// (PM section 6.11).
func TestOpBFSDelaySlot(t *testing.T) {
	t.Run("taken_delay_slot_executes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8F02)
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (SETT in delay slot)", cpu.reg.T())
		}
		if cpu.reg.PC != 0x18 {
			t.Errorf("PC = 0x%08X, want 0x18", cpu.reg.PC)
		}
	})
	t.Run("not_taken_delay_slot_does_not_execute", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8F02)
		cpu.reg.SetT()
		cpu.bus.Write16(0x12, 0x0008) // CLRT would clear T if it ran
		before := cpu.cycles
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (delay slot must not execute)", cpu.reg.T())
		}
		if int(cpu.cycles-before) != 1 {
			t.Errorf("cycles = %d, want 1", cpu.cycles-before)
		}
	})
}

// TestOpBRABoundaries covers BRA with max positive (+2047) and max
// negative (-2048) 12-bit displacements (PM section 6.5).
func TestOpBRABoundaries(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		wantPC uint32
	}{
		// disp = 0x7FF, target = 0x14 + 0x7FF*2 = 0x1012
		{"bra_disp_max_positive", 0xA7FF, 0x1012},
		// disp = -2048 (0x800 sign-extended), target = 0x14 + (-2048)*2 = 0xFFFFF014
		{"bra_disp_max_negative", 0xA800, 0xFFFFF014},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.bus.Write16(0x12, nopOpcode)
			before := cpu.cycles
			cpu.Clock()
			cpu.Clock()
			cpu.Clock()
			if cpu.reg.PC != tt.wantPC {
				t.Errorf("PC = 0x%08X, want 0x%08X", cpu.reg.PC, tt.wantPC)
			}
			if int(cpu.cycles-before) != 3 {
				t.Errorf("cycles = %d, want 3", cpu.cycles-before)
			}
		})
	}
}

// TestOpBRADelaySlotExecutes verifies BRA executes its delay-slot
// instruction (PM section 6.5).
func TestOpBRADelaySlotExecutes(t *testing.T) {
	// BRA +20 with SETT in delay slot.
	cpu := newDecodeTestCPU(0xA00A)
	cpu.reg.ClearT()
	cpu.bus.Write16(0x12, settOpcode)
	cpu.Clock()
	cpu.Clock()
	cpu.Clock()
	if cpu.reg.T() != 1 {
		t.Errorf("T = %d, want 1 (SETT in delay slot)", cpu.reg.T())
	}
}

// TestOpBSRExtras covers BSR PR semantics and max displacements
// (PM section 6.6).
func TestOpBSRExtras(t *testing.T) {
	// PR should hold the return address (instruction after delay slot).
	t.Run("bsr_pr_is_return_address", func(t *testing.T) {
		cpu := newDecodeTestCPU(0xB00A)
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		if cpu.reg.PR != 0x14 {
			t.Errorf("PR = 0x%08X, want 0x14", cpu.reg.PR)
		}
	})
	t.Run("bsr_delay_slot_executes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0xB00A)
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (SETT in delay slot)", cpu.reg.T())
		}
	})
	t.Run("bsr_disp_max_positive", func(t *testing.T) {
		cpu := newDecodeTestCPU(0xB7FF)
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.PC != 0x1012 {
			t.Errorf("PC = 0x%08X, want 0x1012", cpu.reg.PC)
		}
	})
	t.Run("bsr_disp_max_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0xB800)
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.PC != 0xFFFFF014 {
			t.Errorf("PC = 0x%08X, want 0xFFFFF014", cpu.reg.PC)
		}
	})
}

// TestOpBRAFExtras covers BRAF with Rm=0 and delay-slot execution
// (PM section 6.5, register variant).
func TestOpBRAFExtras(t *testing.T) {
	// Rm=0: target = PC+4 exactly.
	t.Run("braf_rm_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0323) // BRAF R3
		cpu.reg.R[3] = 0
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.PC != 0x14 {
			t.Errorf("PC = 0x%08X, want 0x14", cpu.reg.PC)
		}
	})
	t.Run("braf_delay_slot_executes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0323)
		cpu.reg.R[3] = 0x20
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
	})
}

// TestOpBSRFExtras verifies BSRF PR holds the return address (not the
// target) and that the delay-slot executes (PM section 6.6 variant).
func TestOpBSRFExtras(t *testing.T) {
	t.Run("bsrf_pr_is_return_address_not_target", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0303) // BSRF R3
		cpu.reg.R[3] = 0x200
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		// PR must be 0x14 (return address), not the target 0x214.
		if cpu.reg.PR != 0x14 {
			t.Errorf("PR = 0x%08X, want 0x14 (return address, not target)", cpu.reg.PR)
		}
	})
	t.Run("bsrf_delay_slot_executes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0303)
		cpu.reg.R[3] = 0x20
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
	})
}

// TestOpJMPExtras verifies JMP preserves PR and executes the delay slot
// (PM section 6.43).
func TestOpJMPExtras(t *testing.T) {
	t.Run("jmp_preserves_pr", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x432B) // JMP @R3
		cpu.reg.R[3] = 0x200
		cpu.reg.PR = 0xDEADBEEF
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.PR != 0xDEADBEEF {
			t.Errorf("PR = 0x%08X, want 0xDEADBEEF (unchanged)", cpu.reg.PR)
		}
	})
	t.Run("jmp_delay_slot_executes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x432B)
		cpu.reg.R[3] = 0x200
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
	})
}

// TestOpJSRExtras verifies JSR sets PR to the return address and runs
// the delay slot (PM section 6.44).
func TestOpJSRExtras(t *testing.T) {
	t.Run("jsr_delay_slot_executes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x430B) // JSR @R3
		cpu.reg.R[3] = 0x200
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
	})
}

// TestOpRTSExtras verifies RTS pulls PC from PR, preserves SR, and runs
// the delay slot (PM section 6.70).
func TestOpRTSExtras(t *testing.T) {
	t.Run("rts_delay_slot_executes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x000B)
		cpu.reg.PR = 0x300
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
		if cpu.reg.PC != 0x300 {
			t.Errorf("PC = 0x%08X, want 0x300", cpu.reg.PC)
		}
	})
	t.Run("rts_sr_unchanged", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x000B)
		cpu.reg.PR = 0x300
		cpu.reg.SR = srTMask | srSMask
		cpu.bus.Write16(0x12, nopOpcode)
		cpu.Clock()
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.SR != srTMask|srSMask {
			t.Errorf("SR = 0x%08X, want 0x%08X (unchanged)", cpu.reg.SR, srTMask|srSMask)
		}
	})
}

// TestOpRTEExtras verifies RTE pops PC then SR, advances SP by 8, and
// executes its delay slot (PM section 6.69).
func TestOpRTEExtras(t *testing.T) {
	t.Run("rte_sp_increments_by_8", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x002B)
		sp := uint32(0x100)
		cpu.reg.R[15] = sp
		cpu.bus.Write32(sp, 0x400)
		cpu.bus.Write32(sp+4, 0)
		cpu.bus.Write16(0x12, nopOpcode)
		for i := 0; i < 5; i++ {
			cpu.Clock()
		}
		if cpu.reg.R[15] != sp+8 {
			t.Errorf("SP = 0x%08X, want 0x%08X (advanced by 8)", cpu.reg.R[15], sp+8)
		}
	})
	t.Run("rte_pc_from_stack_top", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x002B)
		sp := uint32(0x100)
		cpu.reg.R[15] = sp
		// Stack layout: [SP]=PC, [SP+4]=SR
		cpu.bus.Write32(sp, 0xABCD1234)
		cpu.bus.Write32(sp+4, 0)
		cpu.bus.Write16(0x12, nopOpcode)
		for i := 0; i < 5; i++ {
			cpu.Clock()
		}
		if cpu.reg.PC != 0xABCD1234 {
			t.Errorf("PC = 0x%08X, want 0xABCD1234", cpu.reg.PC)
		}
	})
	t.Run("rte_delay_slot_executes_before_pc_load", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x002B)
		sp := uint32(0x100)
		cpu.reg.R[15] = sp
		cpu.bus.Write32(sp, 0x400)
		cpu.bus.Write32(sp+4, 0)
		cpu.reg.ClearT()
		cpu.bus.Write16(0x12, settOpcode)
		for i := 0; i < 5; i++ {
			cpu.Clock()
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (SETT in delay slot)", cpu.reg.T())
		}
		if cpu.reg.PC != 0x400 {
			t.Errorf("PC = 0x%08X, want 0x400", cpu.reg.PC)
		}
	})
}

// HM Sec 2 programming model: "Reserved bits: Always reads as 0,
// and should always be written with 0." SH-2 SR valid bits are
// M(9), Q(8), I3-I0(7-4), S(1), T(0), mask 0x3F3 (see registers.go
// srMask). When RTE pops SR from the stack it must apply the mask.
// Verify with stacked SR = 0xFFFFFFFF - all reserved bits high -
// that restored SR reads back 0x3F3.
func TestOpRTEMasksSRReservedBits(t *testing.T) {
	cpu := newDecodeTestCPU(0x002B) // RTE
	sp := uint32(0x100)
	cpu.reg.R[15] = sp
	cpu.bus.Write32(sp, 0x400)        // stacked PC
	cpu.bus.Write32(sp+4, 0xFFFFFFFF) // stacked SR with reserved bits poisoned
	cpu.bus.Write16(0x12, nopOpcode)  // delay slot NOP

	for i := 0; i < 5; i++ {
		cpu.Clock()
	}

	if cpu.reg.SR != 0x3F3 {
		t.Errorf("restored SR = 0x%08X, want 0x000003F3 (srMask)", cpu.reg.SR)
	}
}

// HM Sec 2 programming model via a realistic stack frame built by
// software: an interrupt handler STC-pushes SR to the stack, then
// a later path (e.g. buggy code) stores junk over the reserved
// bits in the stacked word. On RTE those reserved bits must be
// discarded. Exercises the WB-stage mask in cpu.go:365.
func TestOpRTEMasksSRFromArbitraryStackFrame(t *testing.T) {
	cpu := newDecodeTestCPU(0x002B)
	sp := uint32(0x100)
	cpu.reg.R[15] = sp
	cpu.bus.Write32(sp, 0x400)
	// Stacked SR: T=1, S=1, IMASK=0xA, Q=1, M=1, reserved bits all 1.
	// Valid portion = 0x3F3; stack word = 0xAAAA_FBF3.
	cpu.bus.Write32(sp+4, 0xAAAAFBF3)
	cpu.bus.Write16(0x12, nopOpcode)

	for i := 0; i < 5; i++ {
		cpu.Clock()
	}

	wantSR := uint32(0x3F3)
	if cpu.reg.SR != wantSR {
		t.Errorf("restored SR = 0x%08X, want 0x%08X (reserved bits cleared, valid bits preserved)",
			cpu.reg.SR, wantSR)
	}
}
