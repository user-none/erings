// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestOpAddSub(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		rm     uint32
		wantRn uint32
	}{
		// ADD R5,R3: 0x335C
		{"ADD_basic", 0x335C, 10, 20, 30},
		{"ADD_overflow", 0x335C, 0xFFFFFFFF, 1, 0},
		// SUB R5,R3: 0x3358
		{"SUB_basic", 0x3358, 30, 10, 20},
		{"SUB_underflow", 0x3358, 0, 1, 0xFFFFFFFF},
		// NEG R5,R3: 0x635B (Rn = 0 - Rm)
		{"NEG_positive", 0x635B, 0, 5, 0xFFFFFFFB},
		{"NEG_negative", 0x635B, 0, 0xFFFFFFFB, 5},
		{"NEG_zero", 0x635B, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetT() // T should be unchanged
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpADDI(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		wantRn uint32
	}{
		// ADD #imm,R3: 0x73ii (sign-extended)
		{"ADDI_positive", 0x730A, 100, 110},
		{"ADDI_negative", 0x73FE, 100, 98}, // #-2
		{"ADDI_max_pos", 0x737F, 0, 127},
		{"ADDI_min_neg", 0x7380, 200, 72}, // #-128
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			cpu.reg.R[n] = tt.rn
			cpu.reg.SetT()
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpDT(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		wantRn uint32
		wantT  uint32
	}{
		{"DT_nonzero", 5, 4, 0},
		{"DT_to_zero", 1, 0, 1},
		{"DT_underflow", 0, 0xFFFFFFFF, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DT R3: 0x4310
			cpu := newDecodeTestCPU(0x4310)
			n := regN(0x4310)
			cpu.reg.R[n] = tt.rn
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpEXT(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rm     uint32
		wantRn uint32
	}{
		// EXTS.B R5,R3: 0x635E
		{"EXTSB_positive", 0x635E, 0x0000007F, 0x0000007F},
		{"EXTSB_negative", 0x635E, 0x00000080, 0xFFFFFF80},
		{"EXTSB_upper_bits", 0x635E, 0xFFFF00FF, 0xFFFFFFFF},
		// EXTS.W R5,R3: 0x635F
		{"EXTSW_positive", 0x635F, 0x00007FFF, 0x00007FFF},
		{"EXTSW_negative", 0x635F, 0x00008000, 0xFFFF8000},
		{"EXTSW_upper_bits", 0x635F, 0xFFFF0001, 0x00000001},
		// EXTU.B R5,R3: 0x635C
		{"EXTUB", 0x635C, 0xDEADBEEF, 0x000000EF},
		{"EXTUB_zero", 0x635C, 0xFFFFFF00, 0x00000000},
		// EXTU.W R5,R3: 0x635D
		{"EXTUW", 0x635D, 0xDEADBEEF, 0x0000BEEF},
		{"EXTUW_zero", 0x635D, 0xFFFF0000, 0x00000000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetT() // T should be unchanged
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpCMPReg(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		rn    uint32
		rm    uint32
		wantT uint32
	}{
		// CMP/EQ R5,R3: 0x3350
		{"CMPEQ_equal", 0x3350, 42, 42, 1},
		{"CMPEQ_not_equal", 0x3350, 42, 43, 0},
		// CMP/HS R5,R3: 0x3352 (unsigned >=)
		{"CMPHS_greater", 0x3352, 10, 5, 1},
		{"CMPHS_equal", 0x3352, 5, 5, 1},
		{"CMPHS_less", 0x3352, 5, 10, 0},
		{"CMPHS_unsigned", 0x3352, 0xFFFFFFFF, 0, 1},
		// CMP/GE R5,R3: 0x3353 (signed >=)
		{"CMPGE_greater", 0x3353, 10, 5, 1},
		{"CMPGE_equal", 0x3353, 5, 5, 1},
		{"CMPGE_less", 0x3353, 5, 10, 0},
		{"CMPGE_signed", 0x3353, 0, 0xFFFFFFFF, 1}, // 0 >= -1
		// CMP/HI R5,R3: 0x3356 (unsigned >)
		{"CMPHI_greater", 0x3356, 10, 5, 1},
		{"CMPHI_equal", 0x3356, 5, 5, 0},
		{"CMPHI_less", 0x3356, 5, 10, 0},
		// CMP/GT R5,R3: 0x3357 (signed >)
		{"CMPGT_greater", 0x3357, 10, 5, 1},
		{"CMPGT_equal", 0x3357, 5, 5, 0},
		{"CMPGT_less", 0x3357, 5, 10, 0},
		{"CMPGT_signed", 0x3357, 0, 0xFFFFFFFF, 1}, // 0 > -1
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpCMPSingle(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		rn    uint32
		wantT uint32
	}{
		// CMP/PL R3: 0x4315 (int32(Rn) > 0)
		{"CMPPL_positive", 0x4315, 1, 1},
		{"CMPPL_zero", 0x4315, 0, 0},
		{"CMPPL_negative", 0x4315, 0xFFFFFFFF, 0},
		// CMP/PZ R3: 0x4311 (int32(Rn) >= 0)
		{"CMPPZ_positive", 0x4311, 1, 1},
		{"CMPPZ_zero", 0x4311, 0, 1},
		{"CMPPZ_negative", 0x4311, 0xFFFFFFFF, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			cpu.reg.R[n] = tt.rn
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}

	// CMP/EQ #imm,R0: 0x88ii
	t.Run("CMPIM_equal", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x880A) // #10
		cpu.reg.R[0] = 10
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
	})
	t.Run("CMPIM_not_equal", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x880A)
		cpu.reg.R[0] = 11
		cpu.Clock()
		if cpu.reg.T() != 0 {
			t.Errorf("T = %d, want 0", cpu.reg.T())
		}
	})
	t.Run("CMPIM_negative_imm", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x88FF) // #-1
		cpu.reg.R[0] = 0xFFFFFFFF
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
	})
}

func TestOpCMPSTR(t *testing.T) {
	tests := []struct {
		name  string
		rn    uint32
		rm    uint32
		wantT uint32
	}{
		// CMP/STR R5,R3: 0x235C
		{"some_bytes_zero", 0x12345678, 0xFF345678, 1}, // xor=0xED000000, bytes 1-3 are zero
		{"all_differ", 0x12345678, 0x9ABCDEF0, 0},      // no byte matches
		{"byte3_match", 0x12345678, 0x00000078, 1},     // low byte matches
		{"byte0_match2", 0x12000000, 0x12FFFFFF, 1},    // high byte matches
		{"all_same", 0xAAAAAAAA, 0xAAAAAAAA, 1},        // all bytes match
		{"byte1_match", 0x00120000, 0xFF12FFFF, 1},     // byte 1 matches
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x235C)
			n := regN(0x235C)
			m := regM(0x235C)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpADDC(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		tIn    bool
		wantRn uint32
		wantT  uint32
	}{
		// ADDC R5,R3: 0x335E
		{"no_carry_in_no_carry_out", 10, 20, false, 30, 0},
		{"carry_in_no_carry_out", 10, 20, true, 31, 0},
		{"no_carry_in_carry_out", 0xFFFFFFFF, 1, false, 0, 1},
		{"carry_in_carry_out", 0xFFFFFFFF, 0, true, 0, 1},
		{"carry_chain", 0xFFFFFFFE, 1, true, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335E)
			n := regN(0x335E)
			m := regM(0x335E)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetTVal(tt.tIn)
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpADDV(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		wantRn uint32
		wantT  uint32
	}{
		// ADDV R5,R3: 0x335F
		{"no_overflow", 10, 20, 30, 0},
		{"positive_overflow", 0x7FFFFFFF, 1, 0x80000000, 1},
		{"negative_no_overflow", 0x80000000, 0x80000000, 0, 1}, // -2G + -2G overflows
		{"mixed_signs", 0x7FFFFFFF, 0x80000000, 0xFFFFFFFF, 0}, // opposite signs never overflow
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335F)
			n := regN(0x335F)
			m := regM(0x335F)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpSUBC(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		tIn    bool
		wantRn uint32
		wantT  uint32
	}{
		// SUBC R5,R3: 0x335A
		{"no_borrow_in_no_borrow_out", 30, 10, false, 20, 0},
		{"borrow_in_no_borrow_out", 30, 10, true, 19, 0},
		{"no_borrow_in_borrow_out", 0, 1, false, 0xFFFFFFFF, 1},
		{"borrow_in_borrow_out", 0, 0, true, 0xFFFFFFFF, 1},
		{"borrow_chain", 1, 1, true, 0xFFFFFFFF, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335A)
			n := regN(0x335A)
			m := regM(0x335A)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetTVal(tt.tIn)
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpSUBV(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		wantRn uint32
		wantT  uint32
	}{
		// SUBV R5,R3: 0x335B
		{"no_overflow", 30, 10, 20, 0},
		{"positive_sub_negative_overflow", 0x7FFFFFFF, 0xFFFFFFFF, 0x80000000, 1}, // MAX - (-1) overflows
		{"negative_sub_positive_overflow", 0x80000000, 1, 0x7FFFFFFF, 1},          // MIN - 1 overflows
		{"same_signs_no_overflow", 10, 5, 5, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335B)
			n := regN(0x335B)
			m := regM(0x335B)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpNEGC(t *testing.T) {
	tests := []struct {
		name   string
		rm     uint32
		tIn    bool
		wantRn uint32
		wantT  uint32
	}{
		// NEGC R5,R3: 0x635A
		{"zero_no_t", 0, false, 0, 0},
		{"zero_with_t", 0, true, 0xFFFFFFFF, 1},
		{"one_no_t", 1, false, 0xFFFFFFFF, 1},
		{"one_with_t", 1, true, 0xFFFFFFFE, 1},
		{"max_no_t", 0xFFFFFFFF, false, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x635A)
			n := regN(0x635A)
			m := regM(0x635A)
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetTVal(tt.tIn)
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpDIV0(t *testing.T) {
	// DIV0U: 0x0019
	t.Run("DIV0U", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0019)
		cpu.reg.SR |= srQMask | srMMask | srTMask
		cpu.Clock()
		if cpu.reg.SR&(srQMask|srMMask|srTMask) != 0 {
			t.Errorf("SR = 0x%08X, want Q=0 M=0 T=0", cpu.reg.SR)
		}
	})

	// DIV0S R5,R3: 0x2357
	t.Run("DIV0S_both_positive", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2357)
		n := regN(0x2357)
		m := regM(0x2357)
		cpu.reg.R[n] = 0x7FFFFFFF // MSB=0 -> Q=0
		cpu.reg.R[m] = 0x7FFFFFFF // MSB=0 -> M=0
		cpu.Clock()
		if cpu.reg.SR&srQMask != 0 {
			t.Error("Q should be 0")
		}
		if cpu.reg.SR&srMMask != 0 {
			t.Error("M should be 0")
		}
		if cpu.reg.T() != 0 {
			t.Errorf("T = %d, want 0 (Q==M)", cpu.reg.T())
		}
	})

	t.Run("DIV0S_different_signs", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2357)
		n := regN(0x2357)
		m := regM(0x2357)
		cpu.reg.R[n] = 0x80000000 // MSB=1 -> Q=1
		cpu.reg.R[m] = 0x7FFFFFFF // MSB=0 -> M=0
		cpu.Clock()
		if cpu.reg.SR&srQMask == 0 {
			t.Error("Q should be 1")
		}
		if cpu.reg.SR&srMMask != 0 {
			t.Error("M should be 0")
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (Q!=M)", cpu.reg.T())
		}
	})

	t.Run("DIV0S_both_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2357)
		n := regN(0x2357)
		m := regM(0x2357)
		cpu.reg.R[n] = 0x80000000 // Q=1
		cpu.reg.R[m] = 0x80000000 // M=1
		cpu.Clock()
		if cpu.reg.SR&srQMask == 0 {
			t.Error("Q should be 1")
		}
		if cpu.reg.SR&srMMask == 0 {
			t.Error("M should be 1")
		}
		if cpu.reg.T() != 0 {
			t.Errorf("T = %d, want 0 (Q==M)", cpu.reg.T())
		}
	})
}

func TestOpDIV1(t *testing.T) {
	// Integration test using Example 2 from SH2 manual:
	// R1:R2 (64 bits) / R0 (32 bits) = R2 (32 bits) : Unsigned
	// For simple 32/32: R1=0, R2=dividend, R0=divisor
	// Sequence: DIV0U; .arepeat 32 { ROTCL R2; DIV1 R0,R1 }; ROTCL R2
	// R2 = quotient
	t.Run("unsigned_48_div_6", func(t *testing.T) {
		bus := newTestBus(0x1000)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		cpu.LoadResetVectors()

		addr := uint32(0x10)
		// DIV0U = 0x0019
		bus.Write16(addr, 0x0019)
		addr += 2
		// 32x { ROTCL R2 (0x4224), DIV1 R0,R1 (0x3104) }
		for i := 0; i < 32; i++ {
			bus.Write16(addr, 0x4224) // ROTCL R2
			addr += 2
			bus.Write16(addr, 0x3104) // DIV1 R0,R1
			addr += 2
		}
		// Final ROTCL R2
		bus.Write16(addr, 0x4224)

		cpu.reg.R[0] = 6  // divisor
		cpu.reg.R[1] = 0  // high 32 bits of dividend
		cpu.reg.R[2] = 48 // low 32 bits of dividend (quotient accumulator)

		// Execute: DIV0U + 32*(ROTCL+DIV1) + ROTCL = 1 + 64 + 1 = 66 steps
		for i := 0; i < 66; i++ {
			cpu.Clock()
		}

		if cpu.reg.R[2] != 8 {
			t.Errorf("R2 (quotient) = %d, want 8", cpu.reg.R[2])
		}
	})

	t.Run("unsigned_100_div_7", func(t *testing.T) {
		bus := newTestBus(0x1000)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		cpu.LoadResetVectors()

		addr := uint32(0x10)
		bus.Write16(addr, 0x0019) // DIV0U
		addr += 2
		for i := 0; i < 32; i++ {
			bus.Write16(addr, 0x4224) // ROTCL R2
			addr += 2
			bus.Write16(addr, 0x3104) // DIV1 R0,R1
			addr += 2
		}
		bus.Write16(addr, 0x4224) // ROTCL R2

		cpu.reg.R[0] = 7   // divisor
		cpu.reg.R[1] = 0   // high dividend
		cpu.reg.R[2] = 100 // low dividend

		for i := 0; i < 66; i++ {
			cpu.Clock()
		}

		if cpu.reg.R[2] != 14 {
			t.Errorf("R2 (quotient) = %d, want 14", cpu.reg.R[2])
		}
	})

	// Test single DIV1 step: verify flag manipulation
	t.Run("single_step_flags", func(t *testing.T) {
		// DIV1 R5,R3: 0x3354
		cpu := newDecodeTestCPU(0x3354)
		n := regN(0x3354)
		m := regM(0x3354)

		// oldQ=0, M=0: subtract path
		// Q = MSB(0x80000000) = 1
		// Rn = (0x80000000 << 1) | 0 = 0x00000000
		// tmp0=0, Rn=0-1=0xFFFFFFFF, borrow=true
		// Q=1: Q = !borrow = false -> Q=0? No:
		// Manual: oldQ=0,M=0,sub: Q=1 -> Q=!tmp1 = !true = 0
		cpu.reg.R[n] = 0x80000000
		cpu.reg.R[m] = 1
		cpu.reg.SR &^= srQMask | srMMask | srTMask

		cpu.Clock()

		// Q should be 0 per manual (Q=1, tmp1=borrow=1, Q=!tmp1=0)
		if cpu.reg.SR&srQMask != 0 {
			t.Error("Q should be 0")
		}
		// T = (Q==M) = (0==0) = 1
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
		if cpu.reg.R[n] != 0xFFFFFFFF {
			t.Errorf("R[%d] = 0x%08X, want 0xFFFFFFFF", n, cpu.reg.R[n])
		}
	})
}

func TestOpMUL(t *testing.T) {
	// MUL.L tests: 2 cycles (1 EX + 1 stall)
	t.Run("MULL_basic", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0357)
		n := regN(0x0357)
		m := regM(0x0357)
		cpu.reg.R[n] = 6
		cpu.reg.R[m] = 7
		cpu.reg.SetT()
		before := cpu.cycles
		cpu.Clock() // EX
		cpu.Clock() // stall
		if cpu.reg.MACL != 42 {
			t.Errorf("MACL = 0x%08X, want 42", cpu.reg.MACL)
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
		}
		if int(cpu.cycles-before) != 2 {
			t.Errorf("cycles = %d, want 2", cpu.cycles-before)
		}
	})

	t.Run("MULL_large", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0357)
		n := regN(0x0357)
		m := regM(0x0357)
		cpu.reg.R[n] = 0x10000
		cpu.reg.R[m] = 0x10000
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACL != 0 {
			t.Errorf("MACL = 0x%08X, want 0", cpu.reg.MACL)
		}
	})

	// MULS.W: 1 cycle (min 1, max 3 with contention)
	t.Run("MULSW_positive", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235F)
		n := regN(0x235F)
		m := regM(0x235F)
		cpu.reg.R[n] = 100
		cpu.reg.R[m] = 200
		before := cpu.cycles
		cpu.Clock()
		if cpu.reg.MACL != 20000 {
			t.Errorf("MACL = 0x%08X, want 20000", cpu.reg.MACL)
		}
		if int(cpu.cycles-before) != 1 {
			t.Errorf("cycles = %d, want 1", cpu.cycles-before)
		}
	})

	t.Run("MULSW_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235F)
		n := regN(0x235F)
		m := regM(0x235F)
		cpu.reg.R[n] = 0xFFFF
		cpu.reg.R[m] = 2
		cpu.Clock()
		if cpu.reg.MACL != 0xFFFFFFFE {
			t.Errorf("MACL = 0x%08X, want 0xFFFFFFFE", cpu.reg.MACL)
		}
	})

	// MULU.W: 1 cycle (min 1, max 3 with contention)
	t.Run("MULUW_basic", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235E)
		n := regN(0x235E)
		m := regM(0x235E)
		cpu.reg.R[n] = 100
		cpu.reg.R[m] = 200
		before := cpu.cycles
		cpu.Clock()
		if cpu.reg.MACL != 20000 {
			t.Errorf("MACL = 0x%08X, want 20000", cpu.reg.MACL)
		}
		if int(cpu.cycles-before) != 1 {
			t.Errorf("cycles = %d, want 1", cpu.cycles-before)
		}
	})

	t.Run("MULUW_unsigned", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235E)
		n := regN(0x235E)
		m := regM(0x235E)
		cpu.reg.R[n] = 0xFFFF
		cpu.reg.R[m] = 2
		cpu.Clock()
		if cpu.reg.MACL != 0x1FFFE {
			t.Errorf("MACL = 0x%08X, want 0x1FFFE", cpu.reg.MACL)
		}
	})
}

func TestOpDMUL(t *testing.T) {
	tests := []struct {
		name     string
		op       uint16
		rn       uint32
		rm       uint32
		wantMACH uint32
		wantMACL uint32
	}{
		// DMULS.L R5,R3: 0x335D
		{"DMULSL_basic", 0x335D, 0x10000, 0x10000, 1, 0},
		{"DMULSL_negative", 0x335D, 0xFFFFFFFF, 2, 0xFFFFFFFF, 0xFFFFFFFE},
		// DMULU.L R5,R3: 0x3355
		{"DMULUL_basic", 0x3355, 0x10000, 0x10000, 1, 0},
		{"DMULUL_large", 0x3355, 0xFFFFFFFF, 2, 1, 0xFFFFFFFE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetT()
			before := cpu.cycles
			// 2 cycles: 1 EX + 1 stall
			cpu.Clock()
			cpu.Clock()
			if cpu.reg.MACH != tt.wantMACH {
				t.Errorf("MACH = 0x%08X, want 0x%08X", cpu.reg.MACH, tt.wantMACH)
			}
			if cpu.reg.MACL != tt.wantMACL {
				t.Errorf("MACL = 0x%08X, want 0x%08X", cpu.reg.MACL, tt.wantMACL)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 2 {
				t.Errorf("cycles = %d, want 2", cpu.cycles-before)
			}
		})
	}
}

func TestOpMAC(t *testing.T) {
	// MAC.L @Rm+,@Rn+: 0x035F (Rn=R3, Rm=R5)
	// 2 cycles: cycle 1 read @Rn, cycle 2 read @Rm + accumulate
	t.Run("MACL_basic", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x035F)
		n := regN(0x035F)
		m := regM(0x035F)

		cpu.bus.Write32(0x100, 10) // @Rn
		cpu.bus.Write32(0x200, 20) // @Rm

		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 5

		cpu.reg.SetT()
		before := cpu.cycles

		// Cycle 1: read @Rn
		s1 := cpu.Clock()
		if s1.Bus != BusRead {
			t.Errorf("cycle 1: bus = %d, want BusRead", s1.Bus)
		}
		// Cycle 2: read @Rm + accumulate
		s2 := cpu.Clock()
		if s2.Bus != BusRead {
			t.Errorf("cycle 2: bus = %d, want BusRead", s2.Bus)
		}

		// 10 * 20 + 5 = 205
		if cpu.reg.MACL != 205 {
			t.Errorf("MACL = %d, want 205", cpu.reg.MACL)
		}
		if cpu.reg.MACH != 0 {
			t.Errorf("MACH = 0x%08X, want 0", cpu.reg.MACH)
		}
		if cpu.reg.R[n] != 0x104 {
			t.Errorf("R[%d] = 0x%08X, want 0x104", n, cpu.reg.R[n])
		}
		if cpu.reg.R[m] != 0x204 {
			t.Errorf("R[%d] = 0x%08X, want 0x204", m, cpu.reg.R[m])
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
		}
		if int(cpu.cycles-before) != 2 {
			t.Errorf("cycles = %d, want 2", cpu.cycles-before)
		}
	})

	t.Run("MACL_saturate", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x035F)
		n := regN(0x035F)
		m := regM(0x035F)

		cpu.bus.Write32(0x100, 0x7FFFFFFF)
		cpu.bus.Write32(0x200, 0x7FFFFFFF)
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 0
		cpu.reg.SR |= srSMask

		cpu.Clock()
		cpu.Clock()

		if cpu.reg.MACH != 0x00007FFF {
			t.Errorf("MACH = 0x%08X, want 0x00007FFF", cpu.reg.MACH)
		}
		if cpu.reg.MACL != 0xFFFFFFFF {
			t.Errorf("MACL = 0x%08X, want 0xFFFFFFFF", cpu.reg.MACL)
		}
	})

	// MAC.W @Rm+,@Rn+: 0x435F (Rn=R3, Rm=R5)
	// 2 cycles: cycle 1 read @Rn, cycle 2 read @Rm + accumulate
	t.Run("MACW_basic", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x435F)
		n := regN(0x435F)
		m := regM(0x435F)

		cpu.bus.Write16(0x100, 10)
		cpu.bus.Write16(0x200, 20)
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 5

		cpu.reg.SetT()
		before := cpu.cycles

		s1 := cpu.Clock()
		if s1.Bus != BusRead {
			t.Errorf("cycle 1: bus = %d, want BusRead", s1.Bus)
		}
		s2 := cpu.Clock()
		if s2.Bus != BusRead {
			t.Errorf("cycle 2: bus = %d, want BusRead", s2.Bus)
		}

		if cpu.reg.MACL != 205 {
			t.Errorf("MACL = %d, want 205", cpu.reg.MACL)
		}
		if cpu.reg.R[n] != 0x102 {
			t.Errorf("R[%d] = 0x%08X, want 0x102", n, cpu.reg.R[n])
		}
		if cpu.reg.R[m] != 0x202 {
			t.Errorf("R[%d] = 0x%08X, want 0x202", m, cpu.reg.R[m])
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
		}
		if int(cpu.cycles-before) != 2 {
			t.Errorf("cycles = %d, want 2", cpu.cycles-before)
		}
	})

	t.Run("MACW_saturate_positive", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x435F)
		n := regN(0x435F)
		m := regM(0x435F)

		cpu.bus.Write16(0x100, 0x7FFF)
		cpu.bus.Write16(0x200, 0x7FFF)
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 0x7FFFFFFF
		cpu.reg.SR |= srSMask

		cpu.Clock()
		cpu.Clock()

		if cpu.reg.MACL != 0x7FFFFFFF {
			t.Errorf("MACL = 0x%08X, want 0x7FFFFFFF", cpu.reg.MACL)
		}
	})
}

// TestOpADDExtras covers ADD Rm,Rn with signed operands, zero, register
// aliasing, and T preservation (PM section 6.1).
func TestOpADDExtras(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		rm     uint32 // ignored when Rn==Rm
		wantRn uint32
	}{
		// ADD R5,R3: 0x335C (-5 + 10 = 5)
		{"add_negative", 0x335C, 0xFFFFFFFB, 10, 5},
		// -5 + -10 = -15
		{"add_both_negative", 0x335C, 0xFFFFFFFB, 0xFFFFFFF6, 0xFFFFFFF1},
		{"add_zero", 0x335C, 0, 0, 0},
		// ADD R3,R3: 0x333C (Rn==Rm doubles operand)
		{"add_rn_equals_rm", 0x333C, 5, 0, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[n] = tt.rn
			if n != m {
				cpu.reg.R[m] = tt.rm
			}
			cpu.reg.SetTVal(false) // ADD must not change T
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 0 {
				t.Errorf("T = %d, want 0 (unchanged)", cpu.reg.T())
			}
		})
	}
}

// TestOpADDIExtras covers ADD #imm,Rn zero immediate, wrap, and T
// preservation (PM section 6.2).
func TestOpADDIExtras(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		wantRn uint32
	}{
		// ADD #0,R3: 0x7300 (Rn unchanged)
		{"addi_zero_imm", 0x7300, 0xDEADBEEF, 0xDEADBEEF},
		// ADD #1,R3: 0x7301 with Rn=0xFFFFFFFF wraps to 0
		{"addi_imm_wraps", 0x7301, 0xFFFFFFFF, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			cpu.reg.R[n] = tt.rn
			cpu.reg.SetTVal(false)
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 0 {
				t.Errorf("T = %d, want 0 (unchanged)", cpu.reg.T())
			}
		})
	}
}

// TestOpSUBExtras covers SUB Rm,Rn Rn==Rm, zero minus positive, and T
// preservation (PM section 6.72).
func TestOpSUBExtras(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		rm     uint32
		wantRn uint32
	}{
		// SUB R3,R3: 0x3338 (Rn==Rm yields 0)
		{"sub_rn_equals_rm", 0x3338, 5, 0, 0},
		// SUB R5,R3: 0x3358 (0 - 5 = 0xFFFFFFFB)
		{"sub_zero_minus_positive", 0x3358, 0, 5, 0xFFFFFFFB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[n] = tt.rn
			if n != m {
				cpu.reg.R[m] = tt.rm
			}
			cpu.reg.SetTVal(false)
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 0 {
				t.Errorf("T = %d, want 0 (unchanged)", cpu.reg.T())
			}
		})
	}
}

// TestOpNEGExtras covers NEG Rm,Rn on INT_MIN (two's complement quirk)
// and T preservation (PM section 6.50).
func TestOpNEGExtras(t *testing.T) {
	// NEG of 0x80000000 produces 0x80000000; -(INT_MIN) is unrepresentable
	// in 32-bit two's complement.
	cpu := newDecodeTestCPU(0x635B)
	n := regN(0x635B)
	m := regM(0x635B)
	cpu.reg.R[m] = 0x80000000
	cpu.reg.SetTVal(false)
	cpu.Clock()
	if cpu.reg.R[n] != 0x80000000 {
		t.Errorf("neg_min_int: R[%d] = 0x%08X, want 0x80000000", n, cpu.reg.R[n])
	}
	if cpu.reg.T() != 0 {
		t.Errorf("T = %d, want 0 (unchanged)", cpu.reg.T())
	}
}

// TestOpADDCExtras covers ADDC corner cases: zero+zero and max+max with
// T carry-in (PM section 6.3).
func TestOpADDCExtras(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		tIn    bool
		wantRn uint32
		wantT  uint32
	}{
		{"zero_plus_zero_t_0", 0, 0, false, 0, 0},
		// 0xFFFFFFFF + 0xFFFFFFFF + 1 = 0x1_FFFFFFFF -> Rn=0xFFFFFFFF, T=1
		{"max_plus_max_t_1", 0xFFFFFFFF, 0xFFFFFFFF, true, 0xFFFFFFFF, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335E)
			n := regN(0x335E)
			m := regM(0x335E)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetTVal(tt.tIn)
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
		})
	}
}

// TestOpADDVExtras covers ADDV zero+zero, max+min (opposite signs no
// overflow), and writeback on overflow (PM section 6.4).
func TestOpADDVExtras(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		wantRn uint32
		wantT  uint32
	}{
		{"zero_plus_zero", 0, 0, 0, 0},
		// 0x7FFFFFFF + 0x80000000 = 0xFFFFFFFF (opposite signs never overflow)
		{"max_plus_min", 0x7FFFFFFF, 0x80000000, 0xFFFFFFFF, 0},
		// Rn gets sum regardless of overflow (0x7FFFFFFF + 1 = 0x80000000)
		{"preserves_rn_on_overflow", 0x7FFFFFFF, 1, 0x80000000, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335F)
			n := regN(0x335F)
			m := regM(0x335F)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
		})
	}
}

// TestOpSUBCExtras covers SUBC 0-0 with T-in variants (PM section 6.73).
func TestOpSUBCExtras(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		tIn    bool
		wantRn uint32
		wantT  uint32
	}{
		{"zero_minus_zero_t_0", 0, 0, false, 0, 0},
		// 0 - 0 - 1 = 0xFFFFFFFF with borrow-out
		{"zero_minus_zero_t_1", 0, 0, true, 0xFFFFFFFF, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335A)
			n := regN(0x335A)
			m := regM(0x335A)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetTVal(tt.tIn)
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
		})
	}
}

// TestOpSUBVExtras covers SUBV signed-overflow edges (PM section 6.74).
func TestOpSUBVExtras(t *testing.T) {
	tests := []struct {
		name   string
		rn     uint32
		rm     uint32
		wantRn uint32
		wantT  uint32
	}{
		// 0x80000000 - 1 = 0x7FFFFFFF: signed overflow (min - 1)
		{"min_minus_one", 0x80000000, 1, 0x7FFFFFFF, 1},
		// 0x7FFFFFFF - 0xFFFFFFFF = 0x80000000: signed overflow (max - (-1))
		{"max_minus_neg_one", 0x7FFFFFFF, 0xFFFFFFFF, 0x80000000, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x335B)
			n := regN(0x335B)
			m := regM(0x335B)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
		})
	}
}

// TestOpNEGCExtras covers NEGC INT_MIN and Rm preservation (PM section 6.51).
func TestOpNEGCExtras(t *testing.T) {
	// 0 - 0x80000000 = 0x80000000 with T=1 (unsigned borrow).
	t.Run("negc_min_int", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x635A)
		n := regN(0x635A)
		m := regM(0x635A)
		cpu.reg.R[m] = 0x80000000
		cpu.reg.SetTVal(false)
		cpu.Clock()
		if cpu.reg.R[n] != 0x80000000 {
			t.Errorf("R[%d] = 0x%08X, want 0x80000000", n, cpu.reg.R[n])
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1", cpu.reg.T())
		}
	})
	// NEGC R5,R3: n=3, m=5 — Rm unchanged after op.
	t.Run("negc_preserves_rm", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x635A)
		m := regM(0x635A)
		cpu.reg.R[m] = 0xCAFEBABE
		cpu.reg.SetTVal(false)
		cpu.Clock()
		if cpu.reg.R[m] != 0xCAFEBABE {
			t.Errorf("R[%d] = 0x%08X, want 0xCAFEBABE (unchanged)", m, cpu.reg.R[m])
		}
	})
}

// TestOpCMPExtras covers boundary cases for compare instructions
// (PM sections 6.14-6.22).
func TestOpCMPExtras(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		rn    uint32
		rm    uint32 // unused for CMP/PL, CMP/PZ
		wantT uint32
	}{
		// CMPEQ boundary
		{"cmpeq_both_zero", 0x3350, 0, 0, 1},
		{"cmpeq_both_max", 0x3350, 0xFFFFFFFF, 0xFFFFFFFF, 1},
		// CMP/EQ R3,R3: 0x3330 - Rn==Rm always T=1
		{"cmpeq_rn_equals_rm", 0x3330, 0xDEADBEEF, 0, 1},
		// CMP/GE signed: positive >= negative
		{"cmpge_pos_vs_neg", 0x3353, 1, 0xFFFFFFFF, 1},
		{"cmpge_neg_vs_pos", 0x3353, 0xFFFFFFFF, 1, 0},
		// CMP/GE min vs max: 0x80000000 >= 0x7FFFFFFF -> false (signed)
		{"cmpge_min_vs_max", 0x3353, 0x80000000, 0x7FFFFFFF, 0},
		// CMP/GT max vs min signed
		{"cmpgt_max_vs_min", 0x3357, 0x7FFFFFFF, 0x80000000, 1},
		// CMP/HI unsigned: 0xFFFFFFFF > 1
		{"cmphi_max_vs_zero", 0x3356, 0xFFFFFFFF, 0, 1},
		{"cmphi_neg_treated_as_large", 0x3356, 0xFFFFFFFF, 1, 1},
		// CMP/HS inclusive unsigned
		{"cmphs_max_vs_low", 0x3352, 0xFFFFFFFF, 1, 1},
		{"cmphs_zero_vs_nonzero", 0x3352, 0, 1, 0},
		// CMP/PL
		{"cmppl_max_pos", 0x4315, 0x7FFFFFFF, 0, 1},
		{"cmppl_min_neg", 0x4315, 0x80000000, 0, 0},
		// CMP/PZ
		{"cmppz_max_pos", 0x4311, 0x7FFFFFFF, 0, 1},
		{"cmppz_min_neg", 0x4311, 0x80000000, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			cpu.reg.R[n] = tt.rn
			// Two-operand compares use format 3nnn mmmm xxxx; CMP/PL
			// and CMP/PZ (4nnn xxxx xxxx) do not reference Rm.
			if tt.op>>12 == 0x3 {
				m := regM(tt.op)
				if n != m {
					cpu.reg.R[m] = tt.rm
				}
			}
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
		})
	}
}

// TestOpCMPIMExtras covers CMP/EQ #imm,R0 boundary cases (PM section 6.22).
func TestOpCMPIMExtras(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		r0    uint32
		wantT uint32
	}{
		// imm=0x80 sign-extends to 0xFFFFFF80 (-128)
		{"cmpim_sign_extension", 0x8880, 0xFFFFFF80, 1},
		// imm=0, T=1 iff R0=0
		{"cmpim_imm_zero_eq", 0x8800, 0, 1},
		{"cmpim_imm_zero_neq", 0x8800, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[0] = tt.r0
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
		})
	}
}

// TestOpCMPSTRExtras covers CMP/STR all-zero and byte-level matching
// (PM section 6.21).
func TestOpCMPSTRExtras(t *testing.T) {
	tests := []struct {
		name  string
		rn    uint32
		rm    uint32
		wantT uint32
	}{
		// Both zero: every byte XOR is zero, T=1.
		{"cmpstr_all_zero", 0, 0, 1},
		// Rn=0x11FFFFFF Rm=0x22FFFFFF -> xor=0x33000000, low 3 bytes zero, T=1.
		{"cmpstr_high_differs_low_matches", 0x11FFFFFF, 0x22FFFFFF, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x235C)
			n := regN(0x235C)
			m := regM(0x235C)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
		})
	}
}

// TestOpDIV0UExtras covers DIV0U non-flag side effects (PM section 6.31).
func TestOpDIV0UExtras(t *testing.T) {
	t.Run("div0u_no_register_change", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0019)
		cpu.reg.R[0] = 0xAAAAAAAA
		cpu.reg.R[1] = 0xBBBBBBBB
		cpu.reg.R[3] = 0xCCCCCCCC
		cpu.Clock()
		if cpu.reg.R[0] != 0xAAAAAAAA || cpu.reg.R[1] != 0xBBBBBBBB || cpu.reg.R[3] != 0xCCCCCCCC {
			t.Errorf("general registers were modified")
		}
	})
	t.Run("div0u_clears_from_dirty_state", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0019)
		cpu.reg.SR |= srQMask | srMMask | srTMask
		cpu.Clock()
		if cpu.reg.SR&(srQMask|srMMask|srTMask) != 0 {
			t.Errorf("SR = 0x%08X, want M=0 Q=0 T=0 after DIV0U", cpu.reg.SR)
		}
	})
}

// TestOpDIV0SExtras covers DIV0S with zero operands (PM section 6.30).
func TestOpDIV0SExtras(t *testing.T) {
	// DIV0S R5,R3: 0x2357. Zero dividend: Q=0, M from divisor sign.
	t.Run("div0s_zero_dividend_neg_divisor", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2357)
		n := regN(0x2357)
		m := regM(0x2357)
		cpu.reg.R[n] = 0
		cpu.reg.R[m] = 0x80000000
		cpu.Clock()
		if cpu.reg.SR&srQMask != 0 {
			t.Error("Q should be 0")
		}
		if cpu.reg.SR&srMMask == 0 {
			t.Error("M should be 1")
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (Q!=M)", cpu.reg.T())
		}
	})
	// Zero divisor: M=0, Q from dividend sign.
	t.Run("div0s_zero_divisor_neg_dividend", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2357)
		n := regN(0x2357)
		m := regM(0x2357)
		cpu.reg.R[n] = 0x80000000
		cpu.reg.R[m] = 0
		cpu.Clock()
		if cpu.reg.SR&srQMask == 0 {
			t.Error("Q should be 1")
		}
		if cpu.reg.SR&srMMask != 0 {
			t.Error("M should be 0")
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (Q!=M)", cpu.reg.T())
		}
	})
}

// TestOpMULExtras covers MUL.L, MULS.W, MULU.W boundary cases
// (PM sections 6.47-6.49).
func TestOpMULExtras(t *testing.T) {
	// MUL.L negative*positive: truncated 32-bit
	t.Run("mull_negative_times_positive", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0357)
		n := regN(0x0357)
		m := regM(0x0357)
		cpu.reg.R[n] = 0xFFFFFFFF // -1
		cpu.reg.R[m] = 5
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACL != 0xFFFFFFFB {
			t.Errorf("MACL = 0x%08X, want 0xFFFFFFFB", cpu.reg.MACL)
		}
	})
	t.Run("mull_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0357)
		n := regN(0x0357)
		m := regM(0x0357)
		cpu.reg.R[n] = 0
		cpu.reg.R[m] = 0xDEADBEEF
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACL != 0 {
			t.Errorf("MACL = 0x%08X, want 0", cpu.reg.MACL)
		}
	})
	// 0xFFFFFFFF * 0xFFFFFFFF = 0xFFFFFFFE_00000001 -> MACL = 0x00000001
	t.Run("mull_max_times_max", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0357)
		n := regN(0x0357)
		m := regM(0x0357)
		cpu.reg.R[n] = 0xFFFFFFFF
		cpu.reg.R[m] = 0xFFFFFFFF
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACL != 1 {
			t.Errorf("MACL = 0x%08X, want 1", cpu.reg.MACL)
		}
	})

	// MULS.W: signed 16x16 -> 32
	t.Run("mulsw_both_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235F)
		n := regN(0x235F)
		m := regM(0x235F)
		cpu.reg.R[n] = 0xFFFF // -1 as int16
		cpu.reg.R[m] = 0xFFFE // -2 as int16
		cpu.Clock()
		if cpu.reg.MACL != 2 {
			t.Errorf("MACL = 0x%08X, want 2", cpu.reg.MACL)
		}
	})
	t.Run("mulsw_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235F)
		n := regN(0x235F)
		m := regM(0x235F)
		cpu.reg.R[n] = 0
		cpu.reg.R[m] = 0x1234
		cpu.Clock()
		if cpu.reg.MACL != 0 {
			t.Errorf("MACL = 0x%08X, want 0", cpu.reg.MACL)
		}
	})
	// Upper bits of Rn/Rm should be ignored for MULS.W.
	t.Run("mulsw_upper_bits_ignored", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235F)
		n := regN(0x235F)
		m := regM(0x235F)
		cpu.reg.R[n] = 0xDEAD_0003 // low 16 = 3
		cpu.reg.R[m] = 0xBEEF_0004 // low 16 = 4
		cpu.Clock()
		if cpu.reg.MACL != 12 {
			t.Errorf("MACL = 0x%08X, want 12", cpu.reg.MACL)
		}
	})

	// MULU.W: unsigned 16x16
	// 0xFFFF * 0xFFFF = 0xFFFE0001
	t.Run("muluw_both_max", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235E)
		n := regN(0x235E)
		m := regM(0x235E)
		cpu.reg.R[n] = 0xFFFF
		cpu.reg.R[m] = 0xFFFF
		cpu.Clock()
		if cpu.reg.MACL != 0xFFFE0001 {
			t.Errorf("MACL = 0x%08X, want 0xFFFE0001", cpu.reg.MACL)
		}
	})
	t.Run("muluw_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235E)
		n := regN(0x235E)
		m := regM(0x235E)
		cpu.reg.R[n] = 0
		cpu.reg.R[m] = 0xFFFF
		cpu.Clock()
		if cpu.reg.MACL != 0 {
			t.Errorf("MACL = 0x%08X, want 0", cpu.reg.MACL)
		}
	})
	t.Run("muluw_upper_bits_ignored", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235E)
		n := regN(0x235E)
		m := regM(0x235E)
		cpu.reg.R[n] = 0xCAFE_0100
		cpu.reg.R[m] = 0xBABE_0003
		cpu.Clock()
		if cpu.reg.MACL != 0x300 {
			t.Errorf("MACL = 0x%08X, want 0x300", cpu.reg.MACL)
		}
	})
}

// TestOpDMULExtras covers DMULS.L and DMULU.L boundary cases
// (PM sections 6.33-6.34).
func TestOpDMULExtras(t *testing.T) {
	// DMULS.L: signed 32x32 -> 64
	t.Run("dmulsl_both_negative", func(t *testing.T) {
		// -2 * -3 = 6
		cpu := newDecodeTestCPU(0x335D)
		n := regN(0x335D)
		m := regM(0x335D)
		cpu.reg.R[n] = 0xFFFFFFFE
		cpu.reg.R[m] = 0xFFFFFFFD
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACH != 0 || cpu.reg.MACL != 6 {
			t.Errorf("MACH:MACL = %08X:%08X, want 0:6", cpu.reg.MACH, cpu.reg.MACL)
		}
	})
	// 0x7FFFFFFF * 0x7FFFFFFF = 0x3FFFFFFF_00000001
	t.Run("dmulsl_max_signed", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x335D)
		n := regN(0x335D)
		m := regM(0x335D)
		cpu.reg.R[n] = 0x7FFFFFFF
		cpu.reg.R[m] = 0x7FFFFFFF
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACH != 0x3FFFFFFF || cpu.reg.MACL != 0x00000001 {
			t.Errorf("MACH:MACL = %08X:%08X, want 3FFFFFFF:00000001",
				cpu.reg.MACH, cpu.reg.MACL)
		}
	})
	t.Run("dmulsl_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x335D)
		n := regN(0x335D)
		m := regM(0x335D)
		cpu.reg.R[n] = 0
		cpu.reg.R[m] = 0x7FFFFFFF
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACH != 0 || cpu.reg.MACL != 0 {
			t.Errorf("MACH:MACL = %08X:%08X, want 0:0", cpu.reg.MACH, cpu.reg.MACL)
		}
	})

	// DMULU.L: unsigned 32x32 -> 64
	// 0xFFFFFFFF * 0xFFFFFFFF = 0xFFFFFFFE_00000001
	t.Run("dmulul_max_unsigned", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x3355)
		n := regN(0x3355)
		m := regM(0x3355)
		cpu.reg.R[n] = 0xFFFFFFFF
		cpu.reg.R[m] = 0xFFFFFFFF
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACH != 0xFFFFFFFE || cpu.reg.MACL != 0x00000001 {
			t.Errorf("MACH:MACL = %08X:%08X, want FFFFFFFE:00000001",
				cpu.reg.MACH, cpu.reg.MACL)
		}
	})
	t.Run("dmulul_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x3355)
		n := regN(0x3355)
		m := regM(0x3355)
		cpu.reg.R[n] = 0xFFFFFFFF
		cpu.reg.R[m] = 0
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACH != 0 || cpu.reg.MACL != 0 {
			t.Errorf("MACH:MACL = %08X:%08X, want 0:0", cpu.reg.MACH, cpu.reg.MACL)
		}
	})
	// DMULU.L treats operands as unsigned: 0x80000000 * 2 = 0x1_00000000
	t.Run("dmulul_sign_bit_unsigned", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x3355)
		n := regN(0x3355)
		m := regM(0x3355)
		cpu.reg.R[n] = 0x80000000
		cpu.reg.R[m] = 2
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACH != 1 || cpu.reg.MACL != 0 {
			t.Errorf("MACH:MACL = %08X:%08X, want 1:0", cpu.reg.MACH, cpu.reg.MACL)
		}
	})
}

// TestOpMACExtras covers MAC.L and MAC.W boundary cases including negative
// accumulation, 48-bit saturation, rm==rn aliasing, word sign-extension
// (PM sections 6.45-6.46).
func TestOpMACExtras(t *testing.T) {
	// MAC.L: negative product accumulates into existing MACL.
	t.Run("macl_negative_accumulation", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x035F)
		n := regN(0x035F)
		m := regM(0x035F)
		cpu.bus.Write32(0x100, 0xFFFFFFFF) // -1
		cpu.bus.Write32(0x200, 1)
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 100
		cpu.Clock()
		cpu.Clock()
		// -1 * 1 = -1; 64-bit(0:100) + -1 = 64-bit(0:99)
		if cpu.reg.MACL != 99 {
			t.Errorf("MACL = %d, want 99", cpu.reg.MACL)
		}
		if cpu.reg.MACH != 0 {
			t.Errorf("MACH = 0x%08X, want 0", cpu.reg.MACH)
		}
	})

	// MAC.L with Rm==Rn: two distinct memory reads (different addrs
	// due to intermediate post-increment); Rn increments twice.
	t.Run("macl_rm_equals_rn", func(t *testing.T) {
		// MAC.L @R3+,@R3+: 0x033F
		cpu := newDecodeTestCPU(0x033F)
		n := regN(0x033F)
		cpu.bus.Write32(0x100, 10)
		cpu.bus.Write32(0x104, 20)
		cpu.reg.R[n] = 0x100
		cpu.reg.MACH = 0
		cpu.reg.MACL = 0
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACL != 200 {
			t.Errorf("MACL = %d, want 200", cpu.reg.MACL)
		}
		if cpu.reg.R[n] != 0x108 {
			t.Errorf("R[%d] = 0x%08X, want 0x108", n, cpu.reg.R[n])
		}
	})

	// MAC.L negative 48-bit saturation (S=1).
	t.Run("macl_saturate_overflow_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x035F)
		n := regN(0x035F)
		m := regM(0x035F)
		cpu.bus.Write32(0x100, 0x7FFFFFFF)
		cpu.bus.Write32(0x200, 0x80000000) // int32 -0x80000000
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 0
		cpu.reg.SR |= srSMask
		cpu.Clock()
		cpu.Clock()
		// Product saturates to -0x800000000000: MACH=0xFFFF8000, MACL=0.
		if cpu.reg.MACH != 0xFFFF8000 {
			t.Errorf("MACH = 0x%08X, want 0xFFFF8000", cpu.reg.MACH)
		}
		if cpu.reg.MACL != 0 {
			t.Errorf("MACL = 0x%08X, want 0", cpu.reg.MACL)
		}
	})

	// MAC.L with S=0: full 64-bit, no saturation.
	t.Run("macl_no_saturate", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x035F)
		n := regN(0x035F)
		m := regM(0x035F)
		cpu.bus.Write32(0x100, 0x7FFFFFFF)
		cpu.bus.Write32(0x200, 0x7FFFFFFF)
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 0
		cpu.reg.SR &^= srSMask
		cpu.Clock()
		cpu.Clock()
		// 0x7FFFFFFF * 0x7FFFFFFF = 0x3FFFFFFF_00000001
		if cpu.reg.MACH != 0x3FFFFFFF {
			t.Errorf("MACH = 0x%08X, want 0x3FFFFFFF", cpu.reg.MACH)
		}
		if cpu.reg.MACL != 1 {
			t.Errorf("MACL = 0x%08X, want 1", cpu.reg.MACL)
		}
	})

	// MAC.W saturate negative (S=1, 32-bit saturation).
	t.Run("macw_saturate_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x435F)
		n := regN(0x435F)
		m := regM(0x435F)
		cpu.bus.Write16(0x100, 1)
		cpu.bus.Write16(0x200, 0xFFFF) // -1
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0xFFFFFFFF
		cpu.reg.MACL = 0x80000000 // acc = -0x80000000
		cpu.reg.SR |= srSMask
		cpu.Clock()
		cpu.Clock()
		// -0x80000000 + -1 saturates to -0x80000000
		if cpu.reg.MACL != 0x80000000 {
			t.Errorf("MACL = 0x%08X, want 0x80000000", cpu.reg.MACL)
		}
	})

	// MAC.W with Rm==Rn: two reads from incrementing addr, Rn advances by 4.
	t.Run("macw_rm_equals_rn", func(t *testing.T) {
		// MAC.W @R3+,@R3+: 0x433F
		cpu := newDecodeTestCPU(0x433F)
		n := regN(0x433F)
		cpu.bus.Write16(0x100, 3)
		cpu.bus.Write16(0x102, 4)
		cpu.reg.R[n] = 0x100
		cpu.reg.MACH = 0
		cpu.reg.MACL = 0
		cpu.Clock()
		cpu.Clock()
		if cpu.reg.MACL != 12 {
			t.Errorf("MACL = %d, want 12", cpu.reg.MACL)
		}
		if cpu.reg.R[n] != 0x104 {
			t.Errorf("R[%d] = 0x%08X, want 0x104", n, cpu.reg.R[n])
		}
	})

	// MAC.W word sign-extension: negative × positive memory values.
	t.Run("macw_sign_extension", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x435F)
		n := regN(0x435F)
		m := regM(0x435F)
		cpu.bus.Write16(0x100, 0xFFFF) // -1 as int16
		cpu.bus.Write16(0x200, 2)
		cpu.reg.R[n] = 0x100
		cpu.reg.R[m] = 0x200
		cpu.reg.MACH = 0
		cpu.reg.MACL = 0
		cpu.reg.SR &^= srSMask
		cpu.Clock()
		cpu.Clock()
		// Product = -1 * 2 = -2
		if cpu.reg.MACL != 0xFFFFFFFE {
			t.Errorf("MACL = 0x%08X, want 0xFFFFFFFE", cpu.reg.MACL)
		}
	})
}

// TestOpDTExtras covers DT with intermediate value and isolation from
// other registers (PM section 6.35).
func TestOpDTExtras(t *testing.T) {
	// Decrement 2 -> 1, T=0.
	t.Run("dt_two_to_one", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x4310)
		n := regN(0x4310)
		cpu.reg.R[n] = 2
		cpu.Clock()
		if cpu.reg.R[n] != 1 {
			t.Errorf("R[%d] = %d, want 1", n, cpu.reg.R[n])
		}
		if cpu.reg.T() != 0 {
			t.Errorf("T = %d, want 0", cpu.reg.T())
		}
	})
	// DT must not affect other general registers.
	t.Run("dt_does_not_affect_other_regs", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x4310)
		n := regN(0x4310)
		cpu.reg.R[n] = 5
		for i := 0; i < 16; i++ {
			if uint8(i) != n {
				cpu.reg.R[i] = uint32(0xA0 + i)
			}
		}
		cpu.Clock()
		for i := 0; i < 16; i++ {
			if uint8(i) != n && cpu.reg.R[i] != uint32(0xA0+i) {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X (untouched)", i, cpu.reg.R[i], uint32(0xA0+i))
			}
		}
	})
}

// TestOpEXTExtras covers EXT[SU].[BW] zero inputs, boundary values,
// and same-register in-place operation (PM sections 6.37-6.40).
func TestOpEXTExtras(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rm     uint32
		wantRn uint32
	}{
		// EXTS.B zero stays 0.
		{"extsb_zero", 0x635E, 0x00000000, 0x00000000},
		// EXTS.B R3,R3 (0x633E) in-place.
		{"extsb_rm_equals_rn", 0x633E, 0xAABBCC80, 0xFFFFFF80},
		// EXTS.W zero.
		{"extsw_zero", 0x635F, 0, 0},
		// EXTS.W 0x8000 -> 0xFFFF8000.
		{"extsw_word_min", 0x635F, 0x00008000, 0xFFFF8000},
		// EXTU.B zero.
		{"extub_zero", 0x635C, 0, 0},
		// EXTU.W zero.
		{"extuw_zero", 0x635D, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			// For rm==rn the same register holds rm first, then gets overwritten.
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetT()
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
		})
	}
}
