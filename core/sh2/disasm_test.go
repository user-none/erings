// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import (
	"fmt"
	"testing"
)

func TestDisasmNoOperand(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		{0x0009, "NOP"},
		{0x0008, "CLRT"},
		{0x0018, "SETT"},
		{0x0028, "CLRMAC"},
		{0x0019, "DIV0U"},
		{0x000B, "RTS"},
		{0x001B, "SLEEP"},
		{0x002B, "RTE"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmRegReg(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		{0x6123, "MOV R2,R1"},
		{0x312C, "ADD R2,R1"},
		{0x3120, "CMP/EQ R2,R1"},
		{0x3128, "SUB R2,R1"},
		{0x2129, "AND R2,R1"},
		{0x212B, "OR R2,R1"},
		{0x212A, "XOR R2,R1"},
		{0x6127, "NOT R2,R1"},
		{0x61BB, "NEG R11,R1"},
		{0x61BA, "NEGC R11,R1"},
		{0x212D, "XTRCT R2,R1"},
		{0x6128, "SWAP.B R2,R1"},
		{0x6129, "SWAP.W R2,R1"},
		{0x612C, "EXTU.B R2,R1"},
		{0x612D, "EXTU.W R2,R1"},
		{0x612E, "EXTS.B R2,R1"},
		{0x612F, "EXTS.W R2,R1"},
		{0x2128, "TST R2,R1"},
		{0x2127, "DIV0S R2,R1"},
		{0x3124, "DIV1 R2,R1"},
		{0x312E, "ADDC R2,R1"},
		{0x312F, "ADDV R2,R1"},
		{0x312A, "SUBC R2,R1"},
		{0x312B, "SUBV R2,R1"},
		{0x3122, "CMP/HS R2,R1"},
		{0x3123, "CMP/GE R2,R1"},
		{0x3126, "CMP/HI R2,R1"},
		{0x3127, "CMP/GT R2,R1"},
		{0x212C, "CMP/STR R2,R1"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmSingleReg(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		{0x4310, "DT R3"},
		{0x4311, "CMP/PZ R3"},
		{0x4315, "CMP/PL R3"},
		{0x0329, "MOVT R3"},
		{0x4304, "ROTL R3"},
		{0x4305, "ROTR R3"},
		{0x4324, "ROTCL R3"},
		{0x4325, "ROTCR R3"},
		{0x4320, "SHAL R3"},
		{0x4321, "SHAR R3"},
		{0x4300, "SHLL R3"},
		{0x4301, "SHLR R3"},
		{0x4308, "SHLL2 R3"},
		{0x4318, "SHLL8 R3"},
		{0x4328, "SHLL16 R3"},
		{0x4309, "SHLR2 R3"},
		{0x4319, "SHLR8 R3"},
		{0x4329, "SHLR16 R3"},
		{0x431B, "TAS.B @R3"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmImmediate(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		{0xE30A, "MOV #10,R3"},
		{0xE3FF, "MOV #-1,R3"},
		{0x730A, "ADD #10,R3"},
		{0x73FF, "ADD #-1,R3"},
		{0x7305, "ADD #5,R3"},
		{0x88FF, "CMP/EQ #-1,R0"}, // 0x88FF: signed -1
		{0x8805, "CMP/EQ #5,R0"},
		{0xC805, "TST #5,R0"}, // 0xC805: imm=5
		{0xC905, "AND #5,R0"},
		{0xCA05, "XOR #5,R0"},
		{0xCB05, "OR #5,R0"},
		{0xC305, "TRAPA #5"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmMemory(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		// @Rm,Rn loads
		{0x6120, "MOV.B @R2,R1"},
		{0x6121, "MOV.W @R2,R1"},
		{0x6122, "MOV.L @R2,R1"},
		// Rm,@Rn stores
		{0x2120, "MOV.B R2,@R1"},
		{0x2121, "MOV.W R2,@R1"},
		{0x2122, "MOV.L R2,@R1"},
		// @Rm+,Rn post-increment
		{0x6124, "MOV.B @R2+,R1"},
		{0x6125, "MOV.W @R2+,R1"},
		{0x6126, "MOV.L @R2+,R1"},
		// Rm,@-Rn pre-decrement
		{0x2124, "MOV.B R2,@-R1"},
		{0x2125, "MOV.W R2,@-R1"},
		{0x2126, "MOV.L R2,@-R1"},
		// @(R0,Rm),Rn
		{0x012C, "MOV.B @(R0,R2),R1"},
		{0x012D, "MOV.W @(R0,R2),R1"},
		{0x012E, "MOV.L @(R0,R2),R1"},
		// Rm,@(R0,Rn)
		{0x0124, "MOV.B R2,@(R0,R1)"},
		{0x0125, "MOV.W R2,@(R0,R1)"},
		{0x0126, "MOV.L R2,@(R0,R1)"},
		// @(disp,Rm),Rn / Rm,@(disp,Rn) - 4-bit disp
		{0x8412, "MOV.B @(2,R1),R0"}, // disp=2, Rm=R1
		{0x8512, "MOV.W @(4,R1),R0"}, // disp=2*2=4, Rm=R1
		{0x5132, "MOV.L @(8,R3),R1"}, // disp=2*4=8, Rm=R3
		{0x8012, "MOV.B R0,@(2,R1)"}, // disp=2, Rn=R1
		{0x8112, "MOV.W R0,@(4,R1)"}, // disp=2*2=4, Rn=R1
		{0x1132, "MOV.L R3,@(8,R1)"}, // disp=2*4=8, Rn=R1
		// @(disp,GBR)
		{0xC002, "MOV.B R0,@(2,GBR)"}, // disp=2
		{0xC102, "MOV.W R0,@(4,GBR)"}, // disp=2*2=4
		{0xC202, "MOV.L R0,@(8,GBR)"}, // disp=2*4=8
		{0xC402, "MOV.B @(2,GBR),R0"},
		{0xC502, "MOV.W @(4,GBR),R0"},
		{0xC602, "MOV.L @(8,GBR),R0"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmBranch(t *testing.T) {
	// pc=0x1000 for all tests
	// BRA: target = pc + 4 + disp12*2
	// 0xA005: disp12=5, target=0x1000+4+10=0x100E
	tests := []struct {
		op   uint16
		want string
	}{
		{0xA005, "BRA H'0000100E"},
		{0xB005, "BSR H'0000100E"},
		// BT: 0x8905, disp8=5, target=0x1000+4+10=0x100E
		{0x8905, "BT H'0000100E"},
		{0x8B05, "BF H'0000100E"},
		{0x8D05, "BT/S H'0000100E"},
		{0x8F05, "BF/S H'0000100E"},
		// Negative displacement: BRA 0xAFFE: disp12=0xFFE sign-extended=-2, target=0x1000+4-4=0x1000
		{0xAFFE, "BRA H'00001000"},
		// BT negative: 0x89FE: disp8=0xFE sign-extended=-2, target=0x1000+4-4=0x1000
		{0x89FE, "BT H'00001000"},
		// JMP @Rm: 0x432B = JMP @R3
		{0x432B, "JMP @R3"},
		// JSR @Rm: 0x430B = JSR @R3
		{0x430B, "JSR @R3"},
		// BRAF Rm: 0x0323 = BRAF R3
		{0x0323, "BRAF R3"},
		// BSRF Rm: 0x0303 = BSRF R3
		{0x0303, "BSRF R3"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmPCRelative(t *testing.T) {
	// pc=0x1000
	// MOV.W @(disp,PC),Rn: target = pc + 4 + disp*2
	// 0x9105: n=1, disp=5, target=0x1000+4+10=0x100E
	tests := []struct {
		op   uint16
		want string
	}{
		{0x9105, "MOV.W @(H'0000100E),R1"},
		// MOV.L @(disp,PC),Rn: target = (pc & 0xFFFFFFFC) + 4 + disp*4
		// 0xD105: n=1, disp=5, target=(0x1000&0xFFFFFFFC)+4+20=0x1018
		{0xD105, "MOV.L @(H'00001018),R1"},
		// MOVA: target = (pc & 0xFFFFFFFC) + 4 + disp*4
		// 0xC705: disp=5, target=(0x1000)+4+20=0x1018
		{0xC705, "MOVA @(H'00001018),R0"},
		// MOV.L with unaligned PC: pc=0x1002
		// target = (0x1002 & 0xFFFFFFFC) + 4 + 5*4 = 0x1000+4+20 = 0x1018
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}

	// Test unaligned PC for MOV.L
	got := Disassemble(0x1002, 0xD105)
	want := "MOV.L @(H'00001018),R1"
	if got != want {
		t.Errorf("Disassemble(0x1002, 0xD105) = %q, want %q", got, want)
	}
}

func TestDisasmSystem(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		// STC
		{0x0102, "STC SR,R1"},
		{0x0112, "STC GBR,R1"},
		{0x0122, "STC VBR,R1"},
		// STS
		{0x010A, "STS MACH,R1"},
		{0x011A, "STS MACL,R1"},
		{0x012A, "STS PR,R1"},
		// LDC
		{0x410E, "LDC R1,SR"},
		{0x411E, "LDC R1,GBR"},
		{0x412E, "LDC R1,VBR"},
		// LDS
		{0x410A, "LDS R1,MACH"},
		{0x411A, "LDS R1,MACL"},
		{0x412A, "LDS R1,PR"},
		// STC.L @-Rn
		{0x4103, "STC.L SR,@-R1"},
		{0x4113, "STC.L GBR,@-R1"},
		{0x4123, "STC.L VBR,@-R1"},
		// STS.L @-Rn
		{0x4102, "STS.L MACH,@-R1"},
		{0x4112, "STS.L MACL,@-R1"},
		{0x4122, "STS.L PR,@-R1"},
		// LDC.L @Rm+
		{0x4107, "LDC.L @R1+,SR"},
		{0x4117, "LDC.L @R1+,GBR"},
		{0x4127, "LDC.L @R1+,VBR"},
		// LDS.L @Rm+
		{0x4106, "LDS.L @R1+,MACH"},
		{0x4116, "LDS.L @R1+,MACL"},
		{0x4126, "LDS.L @R1+,PR"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmMultiply(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		{0x0127, "MUL.L R2,R1"},
		{0x212E, "MULU.W R2,R1"},
		{0x212F, "MULS.W R2,R1"},
		{0x3125, "DMULU.L R2,R1"},
		{0x312D, "DMULS.L R2,R1"},
		{0x012F, "MAC.L @R2+,@R1+"},
		{0x412F, "MAC.W @R2+,@R1+"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmGBRByte(t *testing.T) {
	tests := []struct {
		op   uint16
		want string
	}{
		{0xCC05, "TST.B #5,@(R0,GBR)"},
		{0xCD05, "AND.B #5,@(R0,GBR)"},
		{0xCE05, "XOR.B #5,@(R0,GBR)"},
		{0xCF05, "OR.B #5,@(R0,GBR)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := Disassemble(0x1000, tt.op)
			if got != tt.want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestDisasmIllegal(t *testing.T) {
	tests := []uint16{0xF000, 0xF123, 0xFFFF}
	for _, op := range tests {
		t.Run(fmt.Sprintf("0x%04X", op), func(t *testing.T) {
			got := Disassemble(0x1000, op)
			want := fmt.Sprintf(".word H'%04X", op)
			if got != want {
				t.Errorf("Disassemble(0x1000, 0x%04X) = %q, want %q", op, got, want)
			}
		})
	}
}

func TestIsDelayedBranch(t *testing.T) {
	tests := []struct {
		op   uint16
		want bool
	}{
		// Delayed branches.
		{0x000B, true}, // RTS
		{0x002B, true}, // RTE
		{0x0303, true}, // BSRF R3
		{0x0E03, true}, // BSRF R14
		{0x0323, true}, // BRAF R3
		{0x440B, true}, // JSR @R4
		{0x4F0B, true}, // JSR @R15
		{0x442B, true}, // JMP @R4
		{0x8D05, true}, // BT/S +disp
		{0x8F05, true}, // BF/S +disp
		{0xA123, true}, // BRA
		{0xB123, true}, // BSR
		// Not delayed branches.
		{0x8905, false}, // BT (non-delayed)
		{0x8B05, false}, // BF (non-delayed)
		{0x0009, false}, // NOP
		{0x6013, false}, // MOV R1,R0
		{0xD32A, false}, // MOV.L @(disp,PC),R3
		{0x0013, false}, // group 0, nibble0 == 3 but nibble1 == 1 (not BSRF/BRAF)
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("0x%04X", tt.op), func(t *testing.T) {
			if got := IsDelayedBranch(tt.op); got != tt.want {
				t.Errorf("IsDelayedBranch(0x%04X) = %v, want %v", tt.op, got, tt.want)
			}
		})
	}
}
