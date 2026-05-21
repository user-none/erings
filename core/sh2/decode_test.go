// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestNibble(t *testing.T) {
	// 0xABCD -> nibbles: pos3=A, pos2=B, pos1=C, pos0=D
	op := uint16(0xABCD)
	tests := []struct {
		pos  int
		want uint8
	}{
		{3, 0xA},
		{2, 0xB},
		{1, 0xC},
		{0, 0xD},
	}
	for _, tt := range tests {
		got := nibble(op, tt.pos)
		if got != tt.want {
			t.Errorf("nibble(0x%04X, %d) = 0x%X, want 0x%X", op, tt.pos, got, tt.want)
		}
	}
}

func TestRegN(t *testing.T) {
	// regN extracts bits 11-8
	tests := []struct {
		op   uint16
		want uint8
	}{
		{0x0000, 0},
		{0x0F00, 0xF},
		{0x0500, 5},
		{0xF3FF, 3},
	}
	for _, tt := range tests {
		got := regN(tt.op)
		if got != tt.want {
			t.Errorf("regN(0x%04X) = %d, want %d", tt.op, got, tt.want)
		}
	}
}

func TestRegM(t *testing.T) {
	// regM extracts bits 7-4
	tests := []struct {
		op   uint16
		want uint8
	}{
		{0x0000, 0},
		{0x00F0, 0xF},
		{0x0070, 7},
		{0xFF2F, 2},
	}
	for _, tt := range tests {
		got := regM(tt.op)
		if got != tt.want {
			t.Errorf("regM(0x%04X) = %d, want %d", tt.op, got, tt.want)
		}
	}
}

func TestImm8(t *testing.T) {
	tests := []struct {
		op   uint16
		want uint8
	}{
		{0x0000, 0x00},
		{0x00FF, 0xFF},
		{0xFF42, 0x42},
		{0xAB80, 0x80},
	}
	for _, tt := range tests {
		got := imm8(tt.op)
		if got != tt.want {
			t.Errorf("imm8(0x%04X) = 0x%02X, want 0x%02X", tt.op, got, tt.want)
		}
	}
}

func TestImm4(t *testing.T) {
	tests := []struct {
		op   uint16
		want uint8
	}{
		{0x0000, 0},
		{0x000F, 0xF},
		{0xFFF5, 5},
		{0x0008, 8},
	}
	for _, tt := range tests {
		got := imm4(tt.op)
		if got != tt.want {
			t.Errorf("imm4(0x%04X) = 0x%X, want 0x%X", tt.op, got, tt.want)
		}
	}
}

func TestDisp8(t *testing.T) {
	tests := []struct {
		op   uint16
		want int8
	}{
		{0x0000, 0},    // zero
		{0x007F, 127},  // max positive
		{0x0080, -128}, // min negative
		{0x00FF, -1},   // -1
		{0x0001, 1},    // +1
		{0xFF42, 0x42}, // upper bits ignored
		{0xFFFE, -2},   // negative with upper bits set
	}
	for _, tt := range tests {
		got := disp8(tt.op)
		if got != tt.want {
			t.Errorf("disp8(0x%04X) = %d, want %d", tt.op, got, tt.want)
		}
	}
}

func TestDisp12(t *testing.T) {
	tests := []struct {
		op   uint16
		want int16
	}{
		{0x0000, 0},     // zero
		{0x07FF, 2047},  // max positive (0x7FF)
		{0x0800, -2048}, // min negative (0x800 sign-extends)
		{0x0FFF, -1},    // all 12 bits set = -1
		{0x0001, 1},     // +1
		{0xF801, -2047}, // 0x801 = -2047, upper nibble ignored
	}
	for _, tt := range tests {
		got := disp12(tt.op)
		if got != tt.want {
			t.Errorf("disp12(0x%04X) = %d, want %d", tt.op, got, tt.want)
		}
	}
}

// newDecodeTestCPU creates a CPU with a small bus, writes a 16-bit opcode
// at address 0x10, sets PC to 0x10, and returns the CPU.
func newDecodeTestCPU(op uint16) *CPU {
	bus := newTestBus(0x1000)
	// Write reset vectors
	bus.Write32(0x00, 0x10) // PC = 0x10
	bus.Write32(0x04, 0xF0) // SP = 0xF0
	cpu := New(bus, true)
	cpu.LoadResetVectors()
	// Set all general registers to a safe address so pre-decrement
	// store ops don't underflow on the small test bus.
	for i := 0; i < 15; i++ {
		cpu.reg.R[i] = 0x100
	}
	// Write opcode at PC (0x10)
	bus.Write16(0x10, op)
	return cpu
}

func TestDecodeAllGroups(t *testing.T) {
	// One representative opcode per top-nibble group.
	// Verify no panic and PC advances by 2.
	tests := []struct {
		name string
		op   uint16
	}{
		{"group0_NOP", 0x0009},     // NOP
		{"group1", 0x1000},         // MOV.L Rm,@(disp,Rn)
		{"group2", 0x2000},         // MOV.B Rm,@Rn
		{"group3", 0x3000},         // CMP/EQ Rm,Rn
		{"group4", 0x4000},         // SHLL Rn
		{"group5", 0x5000},         // MOV.L @(disp,Rm),Rn
		{"group6", 0x6003},         // MOV Rm,Rn
		{"group7", 0x7000},         // ADD #imm,Rn
		{"group8", 0x8900},         // BT label
		{"group9", 0x9000},         // MOV.W @(disp,PC),Rn
		{"groupA", 0xA000},         // BRA
		{"groupB", 0xB000},         // BSR
		{"groupC", 0xC800},         // TST #imm,R0
		{"groupD", 0xD000},         // MOV.L @(disp,PC),Rn
		{"groupE", 0xE000},         // MOV #imm,Rn
		{"groupF_illegal", 0xF000}, // No FPU
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			if tt.name == "groupF_illegal" {
				// Illegal instructions raise exception vector 4.
				// Move opcode to 0x20 to avoid collision with
				// the vector at VBR+4*4=0x10.
				cpu.bus.Write16(0x20, tt.op)
				cpu.reg.PC = 0x20
				cpu.bus.Write32(0x10, 0x00000080)
				cpu.Clock()
				if cpu.reg.PC != 0x80 {
					t.Errorf("PC = 0x%08X, want 0x80 (exception handler)", cpu.reg.PC)
				}
				return
			}
			cpu.Clock()
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}

func TestDecodeGroup0(t *testing.T) {
	tests := []struct {
		name string
		op   uint16
	}{
		{"STC_SR", 0x0002},     // STC SR,Rn (n0=2, n1=0)
		{"STC_GBR", 0x0012},    // STC GBR,Rn (n0=2, n1=1)
		{"STC_VBR", 0x0022},    // STC VBR,Rn (n0=2, n1=2)
		{"BSRF", 0x0003},       // BSRF Rm (n0=3, n1=0)
		{"BRAF", 0x0023},       // BRAF Rm (n0=3, n1=2)
		{"MOV.B_R0", 0x0004},   // MOV.B Rm,@(R0,Rn)
		{"MOV.W_R0", 0x0005},   // MOV.W Rm,@(R0,Rn)
		{"MOV.L_R0", 0x0006},   // MOV.L Rm,@(R0,Rn)
		{"MUL.L", 0x0007},      // MUL.L Rm,Rn
		{"CLRT", 0x0008},       // CLRT (n0=8, n1=0)
		{"SETT", 0x0018},       // SETT (n0=8, n1=1)
		{"CLRMAC", 0x0028},     // CLRMAC (n0=8, n1=2)
		{"NOP", 0x0009},        // NOP (n0=9, n1=0)
		{"DIV0U", 0x0019},      // DIV0U (n0=9, n1=1)
		{"MOVT", 0x0029},       // MOVT Rn (n0=9, n1=2)
		{"STS_MACH", 0x000A},   // STS MACH,Rn
		{"STS_MACL", 0x001A},   // STS MACL,Rn
		{"STS_PR", 0x002A},     // STS PR,Rn
		{"RTS", 0x000B},        // RTS (n0=B, n1=0)
		{"SLEEP", 0x001B},      // SLEEP (n0=B, n1=1)
		{"RTE", 0x002B},        // RTE (n0=B, n1=2)
		{"MOV.B_load", 0x000C}, // MOV.B @(R0,Rm),Rn
		{"MOV.W_load", 0x000D}, // MOV.W @(R0,Rm),Rn
		{"MOV.L_load", 0x000E}, // MOV.L @(R0,Rm),Rn
		{"MAC.L", 0x000F},      // MAC.L @Rm+,@Rn+
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.Clock()
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}

func TestDecodeGroup2(t *testing.T) {
	tests := []struct {
		name string
		op   uint16
	}{
		{"MOV.B_store", 0x2000},  // n0=0
		{"MOV.W_store", 0x2001},  // n0=1
		{"MOV.L_store", 0x2002},  // n0=2
		{"MOV.B_predec", 0x2004}, // n0=4
		{"MOV.W_predec", 0x2005}, // n0=5
		{"MOV.L_predec", 0x2006}, // n0=6
		{"DIV0S", 0x2007},        // n0=7
		{"TST", 0x2008},          // n0=8
		{"AND", 0x2009},          // n0=9
		{"XOR", 0x200A},          // n0=A
		{"OR", 0x200B},           // n0=B
		{"CMP/STR", 0x200C},      // n0=C
		{"XTRCT", 0x200D},        // n0=D
		{"MULU.W", 0x200E},       // n0=E
		{"MULS.W", 0x200F},       // n0=F
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.Clock()
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}

func TestDecodeGroup3(t *testing.T) {
	tests := []struct {
		name string
		op   uint16
	}{
		{"CMP/EQ", 0x3000},  // n0=0
		{"CMP/HS", 0x3002},  // n0=2
		{"CMP/GE", 0x3003},  // n0=3
		{"DIV1", 0x3004},    // n0=4
		{"DMULU.L", 0x3005}, // n0=5
		{"CMP/HI", 0x3006},  // n0=6
		{"CMP/GT", 0x3007},  // n0=7
		{"SUB", 0x3008},     // n0=8
		{"SUBC", 0x300A},    // n0=A
		{"SUBV", 0x300B},    // n0=B
		{"ADD", 0x300C},     // n0=C
		{"DMULS.L", 0x300D}, // n0=D
		{"ADDC", 0x300E},    // n0=E
		{"ADDV", 0x300F},    // n0=F
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.Clock()
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}

func TestDecodeGroup4(t *testing.T) {
	tests := []struct {
		name string
		op   uint16
	}{
		{"SHLL", 0x4000},       // low=0x00
		{"SHLR", 0x4001},       // low=0x01
		{"STS.L_MACH", 0x4002}, // low=0x02
		{"STC.L_SR", 0x4003},   // low=0x03
		{"ROTL", 0x4004},       // low=0x04
		{"ROTR", 0x4005},       // low=0x05
		{"LDS.L_MACH", 0x4006}, // low=0x06
		{"LDC.L_SR", 0x4007},   // low=0x07
		{"SHLL2", 0x4008},      // low=0x08
		{"SHLR2", 0x4009},      // low=0x09
		{"LDS_MACH", 0x400A},   // low=0x0A
		{"JSR", 0x400B},        // low=0x0B
		{"LDC_SR", 0x400E},     // low=0x0E
		{"DT", 0x4010},         // low=0x10
		{"CMP/PZ", 0x4011},     // low=0x11
		{"STS.L_MACL", 0x4012}, // low=0x12
		{"STC.L_GBR", 0x4013},  // low=0x13
		{"CMP/PL", 0x4015},     // low=0x15
		{"LDS.L_MACL", 0x4016}, // low=0x16
		{"LDC.L_GBR", 0x4017},  // low=0x17
		{"SHLL8", 0x4018},      // low=0x18
		{"SHLR8", 0x4019},      // low=0x19
		{"LDS_MACL", 0x401A},   // low=0x1A
		{"TAS.B", 0x401B},      // low=0x1B
		{"LDC_GBR", 0x401E},    // low=0x1E
		{"SHAL", 0x4020},       // low=0x20
		{"SHAR", 0x4021},       // low=0x21
		{"STS.L_PR", 0x4022},   // low=0x22
		{"STC.L_VBR", 0x4023},  // low=0x23
		{"ROTCL", 0x4024},      // low=0x24
		{"ROTCR", 0x4025},      // low=0x25
		{"LDS.L_PR", 0x4026},   // low=0x26
		{"LDC.L_VBR", 0x4027},  // low=0x27
		{"SHLL16", 0x4028},     // low=0x28
		{"SHLR16", 0x4029},     // low=0x29
		{"LDS_PR", 0x402A},     // low=0x2A
		{"JMP", 0x402B},        // low=0x2B
		{"LDC_VBR", 0x402E},    // low=0x2E
		{"MAC.W", 0x410F},      // n0=F (with Rm=1)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.Clock()
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}

func TestDecodeGroup6(t *testing.T) {
	tests := []struct {
		name string
		op   uint16
	}{
		{"MOV.B_load", 0x6000},    // n0=0
		{"MOV.W_load", 0x6001},    // n0=1
		{"MOV.L_load", 0x6002},    // n0=2
		{"MOV", 0x6003},           // n0=3
		{"MOV.B_postinc", 0x6004}, // n0=4
		{"MOV.W_postinc", 0x6005}, // n0=5
		{"MOV.L_postinc", 0x6006}, // n0=6
		{"NOT", 0x6007},           // n0=7
		{"SWAP.B", 0x6008},        // n0=8
		{"SWAP.W", 0x6009},        // n0=9
		{"NEGC", 0x600A},          // n0=A
		{"NEG", 0x600B},           // n0=B
		{"EXTU.B", 0x600C},        // n0=C
		{"EXTU.W", 0x600D},        // n0=D
		{"EXTS.B", 0x600E},        // n0=E
		{"EXTS.W", 0x600F},        // n0=F
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.Clock()
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}

func TestDecodeGroup8(t *testing.T) {
	tests := []struct {
		name string
		op   uint16
	}{
		{"MOV.B_R0_store", 0x8000}, // n2=0
		{"MOV.W_R0_store", 0x8100}, // n2=1
		{"MOV.B_R0_load", 0x8400},  // n2=4
		{"MOV.W_R0_load", 0x8500},  // n2=5
		{"CMP/EQ_imm", 0x8800},     // n2=8
		{"BT", 0x8900},             // n2=9
		{"BF", 0x8B00},             // n2=B
		{"BT/S", 0x8D00},           // n2=D
		{"BF/S", 0x8F00},           // n2=F
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			// BF branches when T==0 (the default after reset).
			// Set T so BF falls through and PC advances normally.
			if tt.name == "BF" {
				cpu.reg.SetT()
			}
			cpu.Clock()
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}

func TestDecodeGroupC(t *testing.T) {
	tests := []struct {
		name string
		op   uint16
	}{
		{"MOV.B_GBR_store", 0xC000}, // n2=0
		{"MOV.W_GBR_store", 0xC100}, // n2=1
		{"MOV.L_GBR_store", 0xC200}, // n2=2
		{"TRAPA", 0xC300},           // n2=3
		{"MOV.B_GBR_load", 0xC400},  // n2=4
		{"MOV.W_GBR_load", 0xC500},  // n2=5
		{"MOV.L_GBR_load", 0xC600},  // n2=6
		{"MOVA", 0xC700},            // n2=7
		{"TST_imm", 0xC800},         // n2=8
		{"AND_imm", 0xC900},         // n2=9
		{"XOR_imm", 0xCA00},         // n2=A
		{"OR_imm", 0xCB00},          // n2=B
		{"TST.B", 0xCC00},           // n2=C
		{"AND.B", 0xCD00},           // n2=D
		{"XOR.B", 0xCE00},           // n2=E
		{"OR.B", 0xCF00},            // n2=F
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.Clock()
			// TRAPA changes PC to the vector address
			if tt.name == "TRAPA" {
				return
			}
			if cpu.reg.PC != 0x12 {
				t.Errorf("PC = 0x%08X, want 0x12", cpu.reg.PC)
			}
		})
	}
}
