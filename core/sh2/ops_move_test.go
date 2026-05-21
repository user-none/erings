// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// TestOpMOVReg tests MOV Rm,Rn and MOV #imm,Rn
func TestOpMOVReg(t *testing.T) {
	t.Run("MOV_Rm_Rn", func(t *testing.T) {
		// 0x6353: MOV R5,R3
		cpu := newDecodeTestCPU(0x6353)
		cpu.reg.R[5] = 0xDEADBEEF
		cpu.Clock()
		if cpu.reg.R[3] != 0xDEADBEEF {
			t.Errorf("R3 = 0x%08X, want 0xDEADBEEF", cpu.reg.R[3])
		}
	})

	t.Run("MOVI_positive", func(t *testing.T) {
		// 0xE37F: MOV #0x7F,R3 (positive)
		cpu := newDecodeTestCPU(0xE37F)
		cpu.Clock()
		if cpu.reg.R[3] != 0x0000007F {
			t.Errorf("R3 = 0x%08X, want 0x0000007F", cpu.reg.R[3])
		}
	})

	t.Run("MOVI_negative", func(t *testing.T) {
		// 0xE380: MOV #0x80,R3 (negative, sign-extends to 0xFFFFFF80)
		cpu := newDecodeTestCPU(0xE380)
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFFFF80 {
			t.Errorf("R3 = 0x%08X, want 0xFFFFFF80", cpu.reg.R[3])
		}
	})
}

// TestOpMOVLoad tests MOV.B/W/L @Rm,Rn with sign extension on B/W
func TestOpMOVLoad(t *testing.T) {
	t.Run("MOVBL_positive", func(t *testing.T) {
		// 0x6350: MOV.B @R5,R3
		cpu := newDecodeTestCPU(0x6350)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write8(0x80, 0x42)
		cpu.Clock()
		if cpu.reg.R[3] != 0x00000042 {
			t.Errorf("R3 = 0x%08X, want 0x00000042", cpu.reg.R[3])
		}
	})

	t.Run("MOVBL_negative", func(t *testing.T) {
		// 0x6350: MOV.B @R5,R3 - sign extend 0xFF -> 0xFFFFFFFF
		cpu := newDecodeTestCPU(0x6350)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write8(0x80, 0xFF)
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFFFFFF {
			t.Errorf("R3 = 0x%08X, want 0xFFFFFFFF", cpu.reg.R[3])
		}
	})

	t.Run("MOVWL_positive", func(t *testing.T) {
		// 0x6351: MOV.W @R5,R3
		cpu := newDecodeTestCPU(0x6351)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write16(0x80, 0x1234)
		cpu.Clock()
		if cpu.reg.R[3] != 0x00001234 {
			t.Errorf("R3 = 0x%08X, want 0x00001234", cpu.reg.R[3])
		}
	})

	t.Run("MOVWL_negative", func(t *testing.T) {
		// 0x6351: MOV.W @R5,R3 - sign extend 0x8000 -> 0xFFFF8000
		cpu := newDecodeTestCPU(0x6351)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write16(0x80, 0x8000)
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFF8000 {
			t.Errorf("R3 = 0x%08X, want 0xFFFF8000", cpu.reg.R[3])
		}
	})

	t.Run("MOVLL", func(t *testing.T) {
		// 0x6352: MOV.L @R5,R3
		cpu := newDecodeTestCPU(0x6352)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write32(0x80, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[3] != 0xCAFEBABE {
			t.Errorf("R3 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[3])
		}
	})
}

// TestOpMOVStore tests MOV.B/W/L Rm,@Rn
func TestOpMOVStore(t *testing.T) {
	t.Run("MOVBS", func(t *testing.T) {
		// 0x2350: MOV.B R5,@R3
		cpu := newDecodeTestCPU(0x2350)
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xABCDEF42
		cpu.Clock()
		got := cpu.bus.Read8(0x80)
		if got != 0x42 {
			t.Errorf("@0x80 = 0x%02X, want 0x42", got)
		}
	})

	t.Run("MOVWS", func(t *testing.T) {
		// 0x2351: MOV.W R5,@R3
		cpu := newDecodeTestCPU(0x2351)
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xABCD1234
		cpu.Clock()
		got := cpu.bus.Read16(0x80)
		if got != 0x1234 {
			t.Errorf("@0x80 = 0x%04X, want 0x1234", got)
		}
	})

	t.Run("MOVLS", func(t *testing.T) {
		// 0x2352: MOV.L R5,@R3
		cpu := newDecodeTestCPU(0x2352)
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xDEADBEEF
		cpu.Clock()
		got := cpu.bus.Read32(0x80)
		if got != 0xDEADBEEF {
			t.Errorf("@0x80 = 0x%08X, want 0xDEADBEEF", got)
		}
	})
}

// TestOpMOVPostInc tests MOV.B/W/L @Rm+,Rn including n==m case
func TestOpMOVPostInc(t *testing.T) {
	t.Run("MOVBP", func(t *testing.T) {
		// 0x6354: MOV.B @R5+,R3
		cpu := newDecodeTestCPU(0x6354)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write8(0x80, 0x42)
		cpu.Clock()
		if cpu.reg.R[3] != 0x00000042 {
			t.Errorf("R3 = 0x%08X, want 0x00000042", cpu.reg.R[3])
		}
		if cpu.reg.R[5] != 0x81 {
			t.Errorf("R5 = 0x%08X, want 0x81", cpu.reg.R[5])
		}
	})

	t.Run("MOVWP", func(t *testing.T) {
		// 0x6355: MOV.W @R5+,R3
		cpu := newDecodeTestCPU(0x6355)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write16(0x80, 0x1234)
		cpu.Clock()
		if cpu.reg.R[3] != 0x00001234 {
			t.Errorf("R3 = 0x%08X, want 0x00001234", cpu.reg.R[3])
		}
		if cpu.reg.R[5] != 0x82 {
			t.Errorf("R5 = 0x%08X, want 0x82", cpu.reg.R[5])
		}
	})

	t.Run("MOVLP", func(t *testing.T) {
		// 0x6356: MOV.L @R5+,R3
		cpu := newDecodeTestCPU(0x6356)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write32(0x80, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[3] != 0xCAFEBABE {
			t.Errorf("R3 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[3])
		}
		if cpu.reg.R[5] != 0x84 {
			t.Errorf("R5 = 0x%08X, want 0x84", cpu.reg.R[5])
		}
	})

	t.Run("MOVBP_same_reg", func(t *testing.T) {
		// 0x6554: MOV.B @R5+,R5 (n==m, no increment)
		cpu := newDecodeTestCPU(0x6554)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write8(0x80, 0x42)
		cpu.Clock()
		// R5 gets the loaded value, no post-increment
		if cpu.reg.R[5] != 0x00000042 {
			t.Errorf("R5 = 0x%08X, want 0x00000042", cpu.reg.R[5])
		}
	})
}

// TestOpMOVPreDec tests MOV.B/W/L Rm,@-Rn
func TestOpMOVPreDec(t *testing.T) {
	t.Run("MOVBM", func(t *testing.T) {
		// 0x2354: MOV.B R5,@-R3
		cpu := newDecodeTestCPU(0x2354)
		cpu.reg.R[3] = 0x84
		cpu.reg.R[5] = 0xAB
		cpu.Clock()
		if cpu.reg.R[3] != 0x83 {
			t.Errorf("R3 = 0x%08X, want 0x83", cpu.reg.R[3])
		}
		got := cpu.bus.Read8(0x83)
		if got != 0xAB {
			t.Errorf("@0x83 = 0x%02X, want 0xAB", got)
		}
	})

	t.Run("MOVWM", func(t *testing.T) {
		// 0x2355: MOV.W R5,@-R3
		cpu := newDecodeTestCPU(0x2355)
		cpu.reg.R[3] = 0x84
		cpu.reg.R[5] = 0x1234
		cpu.Clock()
		if cpu.reg.R[3] != 0x82 {
			t.Errorf("R3 = 0x%08X, want 0x82", cpu.reg.R[3])
		}
		got := cpu.bus.Read16(0x82)
		if got != 0x1234 {
			t.Errorf("@0x82 = 0x%04X, want 0x1234", got)
		}
	})

	t.Run("MOVLM", func(t *testing.T) {
		// 0x2356: MOV.L R5,@-R3
		cpu := newDecodeTestCPU(0x2356)
		cpu.reg.R[3] = 0x88
		cpu.reg.R[5] = 0xDEADBEEF
		cpu.Clock()
		if cpu.reg.R[3] != 0x84 {
			t.Errorf("R3 = 0x%08X, want 0x84", cpu.reg.R[3])
		}
		got := cpu.bus.Read32(0x84)
		if got != 0xDEADBEEF {
			t.Errorf("@0x84 = 0x%08X, want 0xDEADBEEF", got)
		}
	})
}

// TestOpMOVR0Idx tests MOV.B/W/L R0-indexed store and load
func TestOpMOVR0Idx(t *testing.T) {
	t.Run("MOVBS0", func(t *testing.T) {
		// 0x0354: MOV.B R5,@(R0,R3)
		cpu := newDecodeTestCPU(0x0354)
		cpu.reg.R[0] = 0x10
		cpu.reg.R[3] = 0x70
		cpu.reg.R[5] = 0xAB
		cpu.Clock()
		got := cpu.bus.Read8(0x80)
		if got != 0xAB {
			t.Errorf("@0x80 = 0x%02X, want 0xAB", got)
		}
	})

	t.Run("MOVWS0", func(t *testing.T) {
		// 0x0355: MOV.W R5,@(R0,R3)
		cpu := newDecodeTestCPU(0x0355)
		cpu.reg.R[0] = 0x10
		cpu.reg.R[3] = 0x70
		cpu.reg.R[5] = 0x1234
		cpu.Clock()
		got := cpu.bus.Read16(0x80)
		if got != 0x1234 {
			t.Errorf("@0x80 = 0x%04X, want 0x1234", got)
		}
	})

	t.Run("MOVLS0", func(t *testing.T) {
		// 0x0356: MOV.L R5,@(R0,R3)
		cpu := newDecodeTestCPU(0x0356)
		cpu.reg.R[0] = 0x10
		cpu.reg.R[3] = 0x70
		cpu.reg.R[5] = 0xDEADBEEF
		cpu.Clock()
		got := cpu.bus.Read32(0x80)
		if got != 0xDEADBEEF {
			t.Errorf("@0x80 = 0x%08X, want 0xDEADBEEF", got)
		}
	})

	t.Run("MOVBL0", func(t *testing.T) {
		// 0x035C: MOV.B @(R0,R5),R3 -- wait, encoding is 0x0nmc
		// Actually: 0x0nnn where n=regN(bits 11-8), m=regM(bits 7-4)
		// 0x035C: regN=3, regM=5, nibble0=C -> MOV.B @(R0,R5),R3
		cpu := newDecodeTestCPU(0x035C)
		cpu.reg.R[0] = 0x10
		cpu.reg.R[5] = 0x70
		cpu.bus.Write8(0x80, 0x90) // 0x90 sign-extends to 0xFFFFFF90
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFFFF90 {
			t.Errorf("R3 = 0x%08X, want 0xFFFFFF90", cpu.reg.R[3])
		}
	})

	t.Run("MOVWL0", func(t *testing.T) {
		// 0x035D: MOV.W @(R0,R5),R3
		cpu := newDecodeTestCPU(0x035D)
		cpu.reg.R[0] = 0x10
		cpu.reg.R[5] = 0x70
		cpu.bus.Write16(0x80, 0x8042)
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFF8042 {
			t.Errorf("R3 = 0x%08X, want 0xFFFF8042", cpu.reg.R[3])
		}
	})

	t.Run("MOVLL0", func(t *testing.T) {
		// 0x035E: MOV.L @(R0,R5),R3
		cpu := newDecodeTestCPU(0x035E)
		cpu.reg.R[0] = 0x10
		cpu.reg.R[5] = 0x70
		cpu.bus.Write32(0x80, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[3] != 0xCAFEBABE {
			t.Errorf("R3 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[3])
		}
	})
}

// TestOpMOVGBR tests MOV.B/W/L @(disp,GBR),R0 and R0,@(disp,GBR)
func TestOpMOVGBR(t *testing.T) {
	t.Run("MOVBSG", func(t *testing.T) {
		// 0xC004: MOV.B R0,@(4,GBR)
		cpu := newDecodeTestCPU(0xC004)
		cpu.reg.GBR = 0x80
		cpu.reg.R[0] = 0xAB
		cpu.Clock()
		got := cpu.bus.Read8(0x84)
		if got != 0xAB {
			t.Errorf("@0x84 = 0x%02X, want 0xAB", got)
		}
	})

	t.Run("MOVWSG", func(t *testing.T) {
		// 0xC104: MOV.W R0,@(4*2,GBR) = @(8,GBR)
		cpu := newDecodeTestCPU(0xC104)
		cpu.reg.GBR = 0x80
		cpu.reg.R[0] = 0x1234
		cpu.Clock()
		got := cpu.bus.Read16(0x88)
		if got != 0x1234 {
			t.Errorf("@0x88 = 0x%04X, want 0x1234", got)
		}
	})

	t.Run("MOVLSG", func(t *testing.T) {
		// 0xC204: MOV.L R0,@(4*4,GBR) = @(16,GBR)
		cpu := newDecodeTestCPU(0xC204)
		cpu.reg.GBR = 0x80
		cpu.reg.R[0] = 0xDEADBEEF
		cpu.Clock()
		got := cpu.bus.Read32(0x90)
		if got != 0xDEADBEEF {
			t.Errorf("@0x90 = 0x%08X, want 0xDEADBEEF", got)
		}
	})

	t.Run("MOVBLG", func(t *testing.T) {
		// 0xC404: MOV.B @(4,GBR),R0
		cpu := newDecodeTestCPU(0xC404)
		cpu.reg.GBR = 0x80
		cpu.bus.Write8(0x84, 0x90)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFFFF90 {
			t.Errorf("R0 = 0x%08X, want 0xFFFFFF90", cpu.reg.R[0])
		}
	})

	t.Run("MOVWLG", func(t *testing.T) {
		// 0xC504: MOV.W @(4*2,GBR),R0 = @(8,GBR)
		cpu := newDecodeTestCPU(0xC504)
		cpu.reg.GBR = 0x80
		cpu.bus.Write16(0x88, 0x8042)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFF8042 {
			t.Errorf("R0 = 0x%08X, want 0xFFFF8042", cpu.reg.R[0])
		}
	})

	t.Run("MOVLLG", func(t *testing.T) {
		// 0xC604: MOV.L @(4*4,GBR),R0 = @(16,GBR)
		cpu := newDecodeTestCPU(0xC604)
		cpu.reg.GBR = 0x80
		cpu.bus.Write32(0x90, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[0] != 0xCAFEBABE {
			t.Errorf("R0 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[0])
		}
	})
}

// TestOpMOVDisp4 tests MOV.B/W/L with 4-bit displacement (store and load, scaling)
func TestOpMOVDisp4(t *testing.T) {
	t.Run("MOVBS4", func(t *testing.T) {
		// 0x8053: MOV.B R0,@(3,R5) -- 80nd: n2=0, n=regM(bits7-4)=5, d=imm4=3
		cpu := newDecodeTestCPU(0x8053)
		cpu.reg.R[5] = 0x80
		cpu.reg.R[0] = 0xAB
		cpu.Clock()
		got := cpu.bus.Read8(0x83)
		if got != 0xAB {
			t.Errorf("@0x83 = 0x%02X, want 0xAB", got)
		}
	})

	t.Run("MOVWS4", func(t *testing.T) {
		// 0x8153: MOV.W R0,@(3*2,R5) = @(6,R5) -- 81nd
		cpu := newDecodeTestCPU(0x8153)
		cpu.reg.R[5] = 0x80
		cpu.reg.R[0] = 0x1234
		cpu.Clock()
		got := cpu.bus.Read16(0x86)
		if got != 0x1234 {
			t.Errorf("@0x86 = 0x%04X, want 0x1234", got)
		}
	})

	t.Run("MOVLS4", func(t *testing.T) {
		// 0x1353: MOV.L R5,@(3*4,R3) = @(12,R3) -- 1nmd
		cpu := newDecodeTestCPU(0x1353)
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xDEADBEEF
		cpu.Clock()
		got := cpu.bus.Read32(0x8C)
		if got != 0xDEADBEEF {
			t.Errorf("@0x8C = 0x%08X, want 0xDEADBEEF", got)
		}
	})

	t.Run("MOVBL4", func(t *testing.T) {
		// 0x8453: MOV.B @(3,R5),R0 -- 84md
		cpu := newDecodeTestCPU(0x8453)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write8(0x83, 0x90)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFFFF90 {
			t.Errorf("R0 = 0x%08X, want 0xFFFFFF90", cpu.reg.R[0])
		}
	})

	t.Run("MOVWL4", func(t *testing.T) {
		// 0x8553: MOV.W @(3*2,R5),R0 = @(6,R5) -- 85md
		cpu := newDecodeTestCPU(0x8553)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write16(0x86, 0x8042)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFF8042 {
			t.Errorf("R0 = 0x%08X, want 0xFFFF8042", cpu.reg.R[0])
		}
	})

	t.Run("MOVLL4", func(t *testing.T) {
		// 0x5353: MOV.L @(3*4,R5),R3 = @(12,R5) -- 5nmd
		cpu := newDecodeTestCPU(0x5353)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write32(0x8C, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[3] != 0xCAFEBABE {
			t.Errorf("R3 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[3])
		}
	})
}

// TestOpMOVPC tests MOV.W/L @(disp,PC),Rn and MOVA (PC-relative, alignment)
func TestOpMOVPC(t *testing.T) {
	t.Run("MOVWI", func(t *testing.T) {
		// 0x9302: MOV.W @(2*2,PC),R3
		// Opcode at 0x10, fetchPC advances to 0x12
		// addr = 0x12 + 2 + 2*2 = 0x18
		cpu := newDecodeTestCPU(0x9302)
		cpu.bus.Write16(0x18, 0x8042)
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFF8042 {
			t.Errorf("R3 = 0x%08X, want 0xFFFF8042", cpu.reg.R[3])
		}
	})

	t.Run("MOVLI_aligned", func(t *testing.T) {
		// 0xD302: MOV.L @(2*4,PC),R3
		// Opcode at 0x10, fetchPC advances to 0x12
		// addr = ((0x12+2) & ~3) + 2*4 = (0x14 & ~3) + 8 = 0x14 + 8 = 0x1C
		cpu := newDecodeTestCPU(0xD302)
		cpu.bus.Write32(0x1C, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[3] != 0xCAFEBABE {
			t.Errorf("R3 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[3])
		}
	})

	t.Run("MOVLI_unaligned_PC", func(t *testing.T) {
		// Test PC alignment masking: put opcode at 0x12 (PC after fetch = 0x14)
		// PC+2 = 0x16, (0x16 & ~3) = 0x14
		// disp=1: addr = 0x14 + 4 = 0x18
		bus := newTestBus(0x1000)
		bus.Write32(0x00, 0x12) // PC = 0x12
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		cpu.LoadResetVectors()
		bus.Write16(0x12, 0xD301) // MOV.L @(1*4,PC),R3
		bus.Write32(0x18, 0x12345678)
		cpu.Clock()
		if cpu.reg.R[3] != 0x12345678 {
			t.Errorf("R3 = 0x%08X, want 0x12345678", cpu.reg.R[3])
		}
	})

	t.Run("MOVA", func(t *testing.T) {
		// 0xC702: MOVA @(2*4,PC),R0
		// Opcode at 0x10, fetchPC advances to 0x12
		// R0 = ((0x12+2) & ~3) + 2*4 = 0x14 + 8 = 0x1C
		cpu := newDecodeTestCPU(0xC702)
		cpu.Clock()
		if cpu.reg.R[0] != 0x1C {
			t.Errorf("R0 = 0x%08X, want 0x1C", cpu.reg.R[0])
		}
	})

	t.Run("MOVA_unaligned_PC", func(t *testing.T) {
		// Opcode at 0x12, PC after fetch = 0x14
		// R0 = ((0x14+2) & ~3) + 1*4 = (0x16 & ~3) + 4 = 0x14 + 4 = 0x18
		bus := newTestBus(0x1000)
		bus.Write32(0x00, 0x12)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		cpu.LoadResetVectors()
		bus.Write16(0x12, 0xC701) // MOVA @(1*4,PC),R0
		cpu.Clock()
		if cpu.reg.R[0] != 0x18 {
			t.Errorf("R0 = 0x%08X, want 0x18", cpu.reg.R[0])
		}
	})
}

// TestOpMOVT tests MOVT Rn
func TestOpMOVT(t *testing.T) {
	t.Run("T_set", func(t *testing.T) {
		// 0x0329: MOVT R3 (regN=3, nibble1=2, nibble0=9)
		cpu := newDecodeTestCPU(0x0329)
		cpu.reg.SetT()
		cpu.Clock()
		if cpu.reg.R[3] != 1 {
			t.Errorf("R3 = %d, want 1", cpu.reg.R[3])
		}
	})

	t.Run("T_clear", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0329)
		cpu.reg.ClearT()
		cpu.Clock()
		if cpu.reg.R[3] != 0 {
			t.Errorf("R3 = %d, want 0", cpu.reg.R[3])
		}
	})
}

// TestOpSWAP tests SWAP.B and SWAP.W
func TestOpSWAP(t *testing.T) {
	t.Run("SWAPB", func(t *testing.T) {
		// 0x6358: SWAP.B R5,R3
		cpu := newDecodeTestCPU(0x6358)
		cpu.reg.R[5] = 0xAABB1234
		cpu.Clock()
		// Upper 16 bits preserved, lower two bytes swapped
		// (0xAABB0000) | ((0x34)<<8) | ((0x12)&0xFF)
		want := uint32(0xAABB3412)
		if cpu.reg.R[3] != want {
			t.Errorf("R3 = 0x%08X, want 0x%08X", cpu.reg.R[3], want)
		}
	})

	t.Run("SWAPW", func(t *testing.T) {
		// 0x6359: SWAP.W R5,R3
		cpu := newDecodeTestCPU(0x6359)
		cpu.reg.R[5] = 0xAABB1234
		cpu.Clock()
		want := uint32(0x1234AABB)
		if cpu.reg.R[3] != want {
			t.Errorf("R3 = 0x%08X, want 0x%08X", cpu.reg.R[3], want)
		}
	})
}

// TestOpXTRCT tests XTRCT Rm,Rn
func TestOpXTRCT(t *testing.T) {
	// 0x235D: XTRCT R5,R3
	cpu := newDecodeTestCPU(0x235D)
	cpu.reg.R[5] = 0xAABBCCDD
	cpu.reg.R[3] = 0x11223344
	cpu.Clock()
	// Rn = (Rm << 16) | (Rn >> 16) = (0xCCDD0000) | (0x1122) = 0xCCDD1122
	want := uint32(0xCCDD1122)
	if cpu.reg.R[3] != want {
		t.Errorf("R3 = 0x%08X, want 0x%08X", cpu.reg.R[3], want)
	}
}

// TestOpNonMemoryBusNone verifies that arithmetic, logic, and shift
// instructions report BusNone (they perform no memory access).
func TestOpNonMemoryBusNone(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		setup func(*CPU)
	}{
		// ADD R5,R3: 0x335C
		{"ADD", 0x335C, func(c *CPU) { c.reg.R[5] = 1 }},
		// AND R5,R3: 0x2359
		{"AND", 0x2359, func(c *CPU) { c.reg.R[3] = 0xFF; c.reg.R[5] = 0x0F }},
		// SHLL R3: 0x4300
		{"SHLL", 0x4300, func(c *CPU) { c.reg.R[3] = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			tt.setup(cpu)
			s := cpu.Clock()
			if s.Bus != BusNone {
				t.Errorf("bus = %d, want BusNone (%d)", s.Bus, BusNone)
			}
		})
	}
}

// TestOpMOVRegExtras covers MOV Rm,Rn with Rn==Rm (effective no-op)
// and MOV R0,Rn (PM section 6.41).
func TestOpMOVRegExtras(t *testing.T) {
	t.Run("mov_rn_equals_rm", func(t *testing.T) {
		// MOV R3,R3: 0x6333
		cpu := newDecodeTestCPU(0x6333)
		cpu.reg.R[3] = 0xDEADBEEF
		cpu.reg.SetT()
		cpu.Clock()
		if cpu.reg.R[3] != 0xDEADBEEF {
			t.Errorf("R3 = 0x%08X, want 0xDEADBEEF", cpu.reg.R[3])
		}
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
		}
	})
	t.Run("mov_r0_to_rn", func(t *testing.T) {
		// MOV R0,R3: 0x6303
		cpu := newDecodeTestCPU(0x6303)
		cpu.reg.R[0] = 0xABCDEF01
		cpu.Clock()
		if cpu.reg.R[3] != 0xABCDEF01 {
			t.Errorf("R3 = 0x%08X, want 0xABCDEF01", cpu.reg.R[3])
		}
	})
}

// TestOpMOVIExtras covers MOV #0,Rn edge (PM section 6.41).
func TestOpMOVIExtras(t *testing.T) {
	// MOV #0,R3: 0xE300
	cpu := newDecodeTestCPU(0xE300)
	cpu.reg.R[3] = 0xDEADBEEF
	cpu.Clock()
	if cpu.reg.R[3] != 0 {
		t.Errorf("R3 = 0x%08X, want 0", cpu.reg.R[3])
	}
}

// TestOpMOVBLExtras covers MOV.B load boundary values (PM section 6.42).
func TestOpMOVBLExtras(t *testing.T) {
	tests := []struct {
		name   string
		mem    uint8
		wantRn uint32
	}{
		{"movbl_zero", 0x00, 0x00000000},
		{"movbl_all_ones", 0xFF, 0xFFFFFFFF},
		{"movbl_msb_only", 0x80, 0xFFFFFF80},
		{"movbl_msb_clear", 0x7F, 0x0000007F},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x6350)
			cpu.reg.R[5] = 0x80
			cpu.bus.Write8(0x80, tt.mem)
			cpu.Clock()
			if cpu.reg.R[3] != tt.wantRn {
				t.Errorf("R3 = 0x%08X, want 0x%08X", cpu.reg.R[3], tt.wantRn)
			}
		})
	}
}

// TestOpMOVWLExtras covers MOV.W load boundary values.
func TestOpMOVWLExtras(t *testing.T) {
	tests := []struct {
		name   string
		mem    uint16
		wantRn uint32
	}{
		{"movwl_zero", 0x0000, 0x00000000},
		{"movwl_all_ones", 0xFFFF, 0xFFFFFFFF},
		{"movwl_msb_only", 0x8000, 0xFFFF8000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x6351)
			cpu.reg.R[5] = 0x80
			cpu.bus.Write16(0x80, tt.mem)
			cpu.Clock()
			if cpu.reg.R[3] != tt.wantRn {
				t.Errorf("R3 = 0x%08X, want 0x%08X", cpu.reg.R[3], tt.wantRn)
			}
		})
	}
}

// TestOpMOVLLExtras covers MOV.L load with MSB set (no sign extension needed).
func TestOpMOVLLExtras(t *testing.T) {
	t.Run("movll_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x6352)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write32(0x80, 0x80000000)
		cpu.Clock()
		if cpu.reg.R[3] != 0x80000000 {
			t.Errorf("R3 = 0x%08X, want 0x80000000", cpu.reg.R[3])
		}
	})
}

// TestOpMOVStoreTruncation covers MOV.B/W stores truncating Rm to byte/word
// and not disturbing adjacent memory bytes.
func TestOpMOVStoreTruncation(t *testing.T) {
	t.Run("movbs_high_bits_truncated", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2350)
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xAABBCCDD
		cpu.bus.Write8(0x7F, 0x11)
		cpu.bus.Write8(0x81, 0x22)
		cpu.Clock()
		if got := cpu.bus.Read8(0x80); got != 0xDD {
			t.Errorf("mem[0x80] = 0x%02X, want 0xDD", got)
		}
		if got := cpu.bus.Read8(0x7F); got != 0x11 {
			t.Errorf("mem[0x7F] = 0x%02X, want 0x11 (neighbor changed)", got)
		}
		if got := cpu.bus.Read8(0x81); got != 0x22 {
			t.Errorf("mem[0x81] = 0x%02X, want 0x22 (neighbor changed)", got)
		}
	})
	t.Run("movws_high_bits_truncated", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2351)
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xAABB1234
		cpu.bus.Write16(0x7E, 0x1111)
		cpu.bus.Write16(0x82, 0x2222)
		cpu.Clock()
		if got := cpu.bus.Read16(0x80); got != 0x1234 {
			t.Errorf("mem[0x80] = 0x%04X, want 0x1234", got)
		}
		if got := cpu.bus.Read16(0x7E); got != 0x1111 {
			t.Errorf("mem[0x7E] = 0x%04X, want 0x1111 (neighbor changed)", got)
		}
		if got := cpu.bus.Read16(0x82); got != 0x2222 {
			t.Errorf("mem[0x82] = 0x%04X, want 0x2222 (neighbor changed)", got)
		}
	})
}

// TestOpMOVBPExtras covers MOV.B @Rm+,Rn sign-extend cases for non-same-reg.
func TestOpMOVBPExtras(t *testing.T) {
	tests := []struct {
		name   string
		mem    uint8
		wantRn uint32
	}{
		{"movbp_sign_extend_positive", 0x42, 0x00000042},
		{"movbp_sign_extend_negative", 0x90, 0xFFFFFF90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x6354)
			cpu.reg.R[5] = 0x80
			cpu.bus.Write8(0x80, tt.mem)
			cpu.Clock()
			if cpu.reg.R[3] != tt.wantRn {
				t.Errorf("R3 = 0x%08X, want 0x%08X", cpu.reg.R[3], tt.wantRn)
			}
			if cpu.reg.R[5] != 0x81 {
				t.Errorf("R5 = 0x%08X, want 0x81", cpu.reg.R[5])
			}
		})
	}
}

// TestOpMOVWPExtras covers MOV.W @Rm+,Rn sign-extension.
func TestOpMOVWPExtras(t *testing.T) {
	t.Run("movwp_sign_extend_negative", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x6355)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write16(0x80, 0x8000)
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFF8000 {
			t.Errorf("R3 = 0x%08X, want 0xFFFF8000", cpu.reg.R[3])
		}
		if cpu.reg.R[5] != 0x82 {
			t.Errorf("R5 = 0x%08X, want 0x82", cpu.reg.R[5])
		}
	})
	t.Run("movwp_same_reg", func(t *testing.T) {
		// MOV.W @R5+,R5: 0x6555 (n==m: no increment, R5 gets loaded value)
		cpu := newDecodeTestCPU(0x6555)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write16(0x80, 0x1234)
		cpu.Clock()
		if cpu.reg.R[5] != 0x00001234 {
			t.Errorf("R5 = 0x%08X, want 0x1234", cpu.reg.R[5])
		}
	})
}

// TestOpMOVLPExtras covers MOV.L @Rm+,Rn same-reg case.
func TestOpMOVLPExtras(t *testing.T) {
	// MOV.L @R5+,R5: 0x6556 (n==m: no increment).
	cpu := newDecodeTestCPU(0x6556)
	cpu.reg.R[5] = 0x80
	cpu.bus.Write32(0x80, 0xCAFEBABE)
	cpu.Clock()
	if cpu.reg.R[5] != 0xCAFEBABE {
		t.Errorf("R5 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[5])
	}
}

// TestOpMOVPreDecExtras covers pre-decrement stores with Rn==Rm and
// high-bit truncation for MOV.B @-Rn.
func TestOpMOVPreDecExtras(t *testing.T) {
	t.Run("movbm_high_bits_truncated", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x2354)
		cpu.reg.R[3] = 0x84
		cpu.reg.R[5] = 0xAABBCCDD
		cpu.Clock()
		if got := cpu.bus.Read8(0x83); got != 0xDD {
			t.Errorf("mem[0x83] = 0x%02X, want 0xDD", got)
		}
	})
	t.Run("movwm_same_reg", func(t *testing.T) {
		// MOV.W R3,@-R3: 0x2355 with n==m=3: 0x2335. Write R3's value
		// (post pre-decrement of R3) to new R3. Order is write first,
		// then decrement Rn.
		// Actually the code writes @Rn-2 = Rm, then Rn -= 2. With n==m,
		// Rm read uses original value.
		cpu := newDecodeTestCPU(0x2335)
		cpu.reg.R[3] = 0x84
		cpu.Clock()
		if cpu.reg.R[3] != 0x82 {
			t.Errorf("R3 = 0x%08X, want 0x82", cpu.reg.R[3])
		}
		if got := cpu.bus.Read16(0x82); got != 0x84 {
			t.Errorf("mem[0x82] = 0x%04X, want 0x84", got)
		}
	})
	t.Run("movlm_same_reg", func(t *testing.T) {
		// MOV.L R3,@-R3: 0x2336
		cpu := newDecodeTestCPU(0x2336)
		cpu.reg.R[3] = 0x88
		cpu.Clock()
		if cpu.reg.R[3] != 0x84 {
			t.Errorf("R3 = 0x%08X, want 0x84", cpu.reg.R[3])
		}
		if got := cpu.bus.Read32(0x84); got != 0x88 {
			t.Errorf("mem[0x84] = 0x%08X, want 0x88", got)
		}
	})
}

// TestOpMOVR0IdxExtras covers R0=0 and R0=0xFFFFFFFF (wraps) cases for
// R0-indexed stores.
func TestOpMOVR0IdxExtras(t *testing.T) {
	t.Run("movbs0_r0_zero", func(t *testing.T) {
		// MOV.B R5,@(R0,R3): 0x0354, with R0=0 -> addr=R3
		cpu := newDecodeTestCPU(0x0354)
		cpu.reg.R[0] = 0
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xAB
		cpu.Clock()
		if got := cpu.bus.Read8(0x80); got != 0xAB {
			t.Errorf("mem[0x80] = 0x%02X, want 0xAB", got)
		}
	})
	t.Run("movls0_r0_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0356)
		cpu.reg.R[0] = 0
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xDEADBEEF
		cpu.Clock()
		if got := cpu.bus.Read32(0x80); got != 0xDEADBEEF {
			t.Errorf("mem[0x80] = 0x%08X, want 0xDEADBEEF", got)
		}
	})
	t.Run("movbl0_sign_extend_positive", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x035C)
		cpu.reg.R[0] = 0x10
		cpu.reg.R[5] = 0x70
		cpu.bus.Write8(0x80, 0x42)
		cpu.Clock()
		if cpu.reg.R[3] != 0x00000042 {
			t.Errorf("R3 = 0x%08X, want 0x42", cpu.reg.R[3])
		}
	})
}

// TestOpMOVGBRExtras covers GBR displacement edges (disp=0 and disp=max).
func TestOpMOVGBRExtras(t *testing.T) {
	t.Run("movbsg_disp_zero", func(t *testing.T) {
		// MOV.B R0,@(0,GBR): 0xC000
		cpu := newDecodeTestCPU(0xC000)
		cpu.reg.GBR = 0x200
		cpu.reg.R[0] = 0x55
		cpu.Clock()
		if got := cpu.bus.Read8(0x200); got != 0x55 {
			t.Errorf("mem[0x200] = 0x%02X, want 0x55", got)
		}
	})
	t.Run("movbsg_disp_max", func(t *testing.T) {
		// MOV.B R0,@(0xFF,GBR): 0xC0FF
		cpu := newDecodeTestCPU(0xC0FF)
		cpu.reg.GBR = 0x200
		cpu.reg.R[0] = 0xAA
		cpu.Clock()
		if got := cpu.bus.Read8(0x2FF); got != 0xAA {
			t.Errorf("mem[0x2FF] = 0x%02X, want 0xAA", got)
		}
	})
	t.Run("movwsg_disp_max_scaled", func(t *testing.T) {
		// MOV.W R0,@(0xFF*2,GBR): 0xC1FF -> addr = GBR + 0x1FE
		cpu := newDecodeTestCPU(0xC1FF)
		cpu.reg.GBR = 0x200
		cpu.reg.R[0] = 0x1234
		cpu.Clock()
		if got := cpu.bus.Read16(0x3FE); got != 0x1234 {
			t.Errorf("mem[0x3FE] = 0x%04X, want 0x1234", got)
		}
	})
	t.Run("movlsg_disp_max_scaled", func(t *testing.T) {
		// MOV.L R0,@(0xFF*4,GBR): 0xC2FF -> addr = GBR + 0x3FC
		cpu := newDecodeTestCPU(0xC2FF)
		cpu.reg.GBR = 0x200
		cpu.reg.R[0] = 0xDEADBEEF
		cpu.Clock()
		if got := cpu.bus.Read32(0x5FC); got != 0xDEADBEEF {
			t.Errorf("mem[0x5FC] = 0x%08X, want 0xDEADBEEF", got)
		}
	})
	t.Run("movblg_sign_extend", func(t *testing.T) {
		// MOV.B @(0,GBR),R0: 0xC400
		cpu := newDecodeTestCPU(0xC400)
		cpu.reg.GBR = 0x200
		cpu.bus.Write8(0x200, 0x80)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFFFF80 {
			t.Errorf("R0 = 0x%08X, want 0xFFFFFF80", cpu.reg.R[0])
		}
	})
	t.Run("movwlg_sign_extend", func(t *testing.T) {
		// MOV.W @(0,GBR),R0: 0xC500
		cpu := newDecodeTestCPU(0xC500)
		cpu.reg.GBR = 0x200
		cpu.bus.Write16(0x200, 0x8000)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFF8000 {
			t.Errorf("R0 = 0x%08X, want 0xFFFF8000", cpu.reg.R[0])
		}
	})
}

// TestOpMOVDisp4Extras covers disp4 edges (disp=0 and disp=15 scaled).
func TestOpMOVDisp4Extras(t *testing.T) {
	t.Run("movbs4_disp_zero", func(t *testing.T) {
		// MOV.B R0,@(0,R5): 0x8050
		cpu := newDecodeTestCPU(0x8050)
		cpu.reg.R[5] = 0x80
		cpu.reg.R[0] = 0xAB
		cpu.Clock()
		if got := cpu.bus.Read8(0x80); got != 0xAB {
			t.Errorf("mem[0x80] = 0x%02X, want 0xAB", got)
		}
	})
	t.Run("movbs4_disp_max", func(t *testing.T) {
		// MOV.B R0,@(15,R5): 0x805F
		cpu := newDecodeTestCPU(0x805F)
		cpu.reg.R[5] = 0x80
		cpu.reg.R[0] = 0xAB
		cpu.Clock()
		if got := cpu.bus.Read8(0x8F); got != 0xAB {
			t.Errorf("mem[0x8F] = 0x%02X, want 0xAB", got)
		}
	})
	t.Run("movws4_disp_max_scaled", func(t *testing.T) {
		// MOV.W R0,@(15*2,R5): 0x815F -> addr = R5 + 30 = 0x9E
		cpu := newDecodeTestCPU(0x815F)
		cpu.reg.R[5] = 0x80
		cpu.reg.R[0] = 0x1234
		cpu.Clock()
		if got := cpu.bus.Read16(0x9E); got != 0x1234 {
			t.Errorf("mem[0x9E] = 0x%04X, want 0x1234", got)
		}
	})
	t.Run("movls4_disp_max_scaled", func(t *testing.T) {
		// MOV.L R5,@(15*4,R3): 0x135F -> addr = R3 + 60 = 0xBC
		cpu := newDecodeTestCPU(0x135F)
		cpu.reg.R[3] = 0x80
		cpu.reg.R[5] = 0xDEADBEEF
		cpu.Clock()
		if got := cpu.bus.Read32(0xBC); got != 0xDEADBEEF {
			t.Errorf("mem[0xBC] = 0x%08X, want 0xDEADBEEF", got)
		}
	})
	t.Run("movbl4_sign_extend", func(t *testing.T) {
		// MOV.B @(0,R5),R0: 0x8450
		cpu := newDecodeTestCPU(0x8450)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write8(0x80, 0x80)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFFFF80 {
			t.Errorf("R0 = 0x%08X, want 0xFFFFFF80", cpu.reg.R[0])
		}
	})
	t.Run("movwl4_sign_extend", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x8550)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write16(0x80, 0x8000)
		cpu.Clock()
		if cpu.reg.R[0] != 0xFFFF8000 {
			t.Errorf("R0 = 0x%08X, want 0xFFFF8000", cpu.reg.R[0])
		}
	})
	t.Run("movll4_disp_zero", func(t *testing.T) {
		// MOV.L @(0,R5),R3: 0x5350
		cpu := newDecodeTestCPU(0x5350)
		cpu.reg.R[5] = 0x80
		cpu.bus.Write32(0x80, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[3] != 0xCAFEBABE {
			t.Errorf("R3 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[3])
		}
	})
}

// TestOpMOVPCExtras covers PC-relative disp edges and sign-extension
// (PM section 6.41, PC-relative).
func TestOpMOVPCExtras(t *testing.T) {
	t.Run("movwi_sign_extend", func(t *testing.T) {
		// MOV.W @(2*2,PC),R3: 0x9302 - load 0x8000 sign-extends.
		cpu := newDecodeTestCPU(0x9302)
		cpu.bus.Write16(0x18, 0x8000)
		cpu.Clock()
		if cpu.reg.R[3] != 0xFFFF8000 {
			t.Errorf("R3 = 0x%08X, want 0xFFFF8000", cpu.reg.R[3])
		}
	})
	t.Run("movwi_disp_zero", func(t *testing.T) {
		// MOV.W @(0,PC),R3: 0x9300; addr = (0x12+2) = 0x14
		cpu := newDecodeTestCPU(0x9300)
		cpu.bus.Write16(0x14, 0x1234)
		cpu.Clock()
		if cpu.reg.R[3] != 0x00001234 {
			t.Errorf("R3 = 0x%08X, want 0x1234", cpu.reg.R[3])
		}
	})
	t.Run("movwi_disp_max", func(t *testing.T) {
		// MOV.W @(0xFF*2,PC),R3: 0x93FF; addr = 0x14 + 0x1FE = 0x212
		cpu := newDecodeTestCPU(0x93FF)
		cpu.bus.Write16(0x212, 0x5555)
		cpu.Clock()
		if cpu.reg.R[3] != 0x00005555 {
			t.Errorf("R3 = 0x%08X, want 0x5555", cpu.reg.R[3])
		}
	})
	t.Run("movli_disp_zero", func(t *testing.T) {
		// MOV.L @(0,PC),R3: 0xD300; addr = (0x14 & ~3) + 0 = 0x14
		cpu := newDecodeTestCPU(0xD300)
		cpu.bus.Write32(0x14, 0xDEADBEEF)
		cpu.Clock()
		if cpu.reg.R[3] != 0xDEADBEEF {
			t.Errorf("R3 = 0x%08X, want 0xDEADBEEF", cpu.reg.R[3])
		}
	})
	t.Run("movli_disp_max", func(t *testing.T) {
		// MOV.L @(0xFF*4,PC),R3: 0xD3FF; addr = 0x14 + 0x3FC = 0x410
		cpu := newDecodeTestCPU(0xD3FF)
		cpu.bus.Write32(0x410, 0xCAFEBABE)
		cpu.Clock()
		if cpu.reg.R[3] != 0xCAFEBABE {
			t.Errorf("R3 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[3])
		}
	})
	t.Run("mova_disp_zero", func(t *testing.T) {
		// MOVA @(0,PC),R0: 0xC700; R0 = (0x14 & ~3) + 0 = 0x14
		cpu := newDecodeTestCPU(0xC700)
		cpu.Clock()
		if cpu.reg.R[0] != 0x14 {
			t.Errorf("R0 = 0x%08X, want 0x14", cpu.reg.R[0])
		}
	})
	t.Run("mova_disp_max", func(t *testing.T) {
		// MOVA @(0xFF*4,PC),R0: 0xC7FF; R0 = 0x14 + 0x3FC = 0x410
		cpu := newDecodeTestCPU(0xC7FF)
		cpu.Clock()
		if cpu.reg.R[0] != 0x410 {
			t.Errorf("R0 = 0x%08X, want 0x410", cpu.reg.R[0])
		}
	})
}

// TestOpMOVTExtras verifies MOVT clears Rn's upper bits and leaves T
// unchanged (PM section 6.42).
func TestOpMOVTExtras(t *testing.T) {
	t.Run("movt_clears_upper_bits", func(t *testing.T) {
		// MOVT R3: 0x0329
		cpu := newDecodeTestCPU(0x0329)
		cpu.reg.R[3] = 0xFFFFFFFF
		cpu.reg.SetT()
		cpu.Clock()
		if cpu.reg.R[3] != 1 {
			t.Errorf("R3 = 0x%08X, want 1 (upper bits should be 0)", cpu.reg.R[3])
		}
	})
	t.Run("movt_preserves_t_after_write", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0329)
		cpu.reg.SetT()
		cpu.Clock()
		if cpu.reg.T() != 1 {
			t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
		}
	})
}

// TestOpSWAPExtras covers SWAP.B upper-half preservation and SWAP.W
// pattern cases (PM sections 6.88-6.89).
func TestOpSWAPExtras(t *testing.T) {
	t.Run("swapb_upper_half_preserved", func(t *testing.T) {
		// SWAP.B R5,R3: 0x6358 - upper 16 bits of Rm preserved in Rn.
		cpu := newDecodeTestCPU(0x6358)
		cpu.reg.R[5] = 0xDEAD1122
		cpu.Clock()
		if cpu.reg.R[3] != 0xDEAD2211 {
			t.Errorf("R3 = 0x%08X, want 0xDEAD2211", cpu.reg.R[3])
		}
	})
	t.Run("swapb_zero", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x6358)
		cpu.reg.R[5] = 0
		cpu.Clock()
		if cpu.reg.R[3] != 0 {
			t.Errorf("R3 = 0x%08X, want 0", cpu.reg.R[3])
		}
	})
	t.Run("swapb_rn_equals_rm", func(t *testing.T) {
		// SWAP.B R3,R3: 0x6338
		cpu := newDecodeTestCPU(0x6338)
		cpu.reg.R[3] = 0xAABB1122
		cpu.Clock()
		if cpu.reg.R[3] != 0xAABB2211 {
			t.Errorf("R3 = 0x%08X, want 0xAABB2211", cpu.reg.R[3])
		}
	})
	t.Run("swapw_zero", func(t *testing.T) {
		// SWAP.W R5,R3: 0x6359
		cpu := newDecodeTestCPU(0x6359)
		cpu.reg.R[5] = 0
		cpu.Clock()
		if cpu.reg.R[3] != 0 {
			t.Errorf("R3 = 0x%08X, want 0", cpu.reg.R[3])
		}
	})
	t.Run("swapw_rn_equals_rm", func(t *testing.T) {
		// SWAP.W R3,R3: 0x6339
		cpu := newDecodeTestCPU(0x6339)
		cpu.reg.R[3] = 0xAABBCCDD
		cpu.Clock()
		if cpu.reg.R[3] != 0xCCDDAABB {
			t.Errorf("R3 = 0x%08X, want 0xCCDDAABB", cpu.reg.R[3])
		}
	})
}

// TestOpXTRCTExtras covers XTRCT zero sources and pattern-based
// extraction (PM section 6.120).
func TestOpXTRCTExtras(t *testing.T) {
	t.Run("xtrct_zero_rm", func(t *testing.T) {
		// XTRCT R5,R3: 0x235D
		cpu := newDecodeTestCPU(0x235D)
		cpu.reg.R[5] = 0
		cpu.reg.R[3] = 0xAABBCCDD
		cpu.Clock()
		// Rn = (0 << 16) | (0xAABBCCDD >> 16) = 0xAABB
		if cpu.reg.R[3] != 0x0000AABB {
			t.Errorf("R3 = 0x%08X, want 0xAABB", cpu.reg.R[3])
		}
	})
	t.Run("xtrct_zero_rn", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x235D)
		cpu.reg.R[5] = 0xAABBCCDD
		cpu.reg.R[3] = 0
		cpu.Clock()
		// Rn = (0xAABBCCDD << 16) | (0 >> 16) = 0xCCDD0000
		if cpu.reg.R[3] != 0xCCDD0000 {
			t.Errorf("R3 = 0x%08X, want 0xCCDD0000", cpu.reg.R[3])
		}
	})
}

func TestOpMOVBusActivity(t *testing.T) {
	tests := []struct {
		name    string
		op      uint16
		setup   func(*CPU)
		wantBus BusActivity
	}{
		// Loads -> BusRead
		{"MOVBL", 0x6350, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write8(0x80, 0x42) }, BusRead},
		{"MOVWL", 0x6351, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write16(0x80, 0x1234) }, BusRead},
		{"MOVLL", 0x6352, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write32(0x80, 0xCAFE) }, BusRead},
		{"MOVBP", 0x6354, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write8(0x80, 0x42) }, BusRead},
		{"MOVWP", 0x6355, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write16(0x80, 0x1234) }, BusRead},
		{"MOVLP", 0x6356, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write32(0x80, 0xCAFE) }, BusRead},
		{"MOVBL0", 0x035C, func(c *CPU) { c.reg.R[0] = 0x10; c.reg.R[5] = 0x70; c.bus.Write8(0x80, 0x42) }, BusRead},
		{"MOVWL0", 0x035D, func(c *CPU) { c.reg.R[0] = 0x10; c.reg.R[5] = 0x70; c.bus.Write16(0x80, 0x1234) }, BusRead},
		{"MOVLL0", 0x035E, func(c *CPU) { c.reg.R[0] = 0x10; c.reg.R[5] = 0x70; c.bus.Write32(0x80, 0xCAFE) }, BusRead},
		{"MOVBLG", 0xC404, func(c *CPU) { c.reg.GBR = 0x80; c.bus.Write8(0x84, 0x42) }, BusRead},
		{"MOVWLG", 0xC504, func(c *CPU) { c.reg.GBR = 0x80; c.bus.Write16(0x88, 0x1234) }, BusRead},
		{"MOVLLG", 0xC604, func(c *CPU) { c.reg.GBR = 0x80; c.bus.Write32(0x90, 0xCAFE) }, BusRead},
		{"MOVBL4", 0x8453, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write8(0x83, 0x42) }, BusRead},
		{"MOVWL4", 0x8553, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write16(0x86, 0x1234) }, BusRead},
		{"MOVLL4", 0x5353, func(c *CPU) { c.reg.R[5] = 0x80; c.bus.Write32(0x8C, 0xCAFE) }, BusRead},
		{"MOVWI", 0x9302, func(c *CPU) { c.bus.Write16(0x18, 0x1234) }, BusRead},
		{"MOVLI", 0xD302, func(c *CPU) { c.bus.Write32(0x1C, 0xCAFE) }, BusRead},
		// Stores -> BusWrite
		{"MOVBS", 0x2350, func(c *CPU) { c.reg.R[3] = 0x80; c.reg.R[5] = 0xAB }, BusWrite},
		{"MOVWS", 0x2351, func(c *CPU) { c.reg.R[3] = 0x80; c.reg.R[5] = 0x1234 }, BusWrite},
		{"MOVLS", 0x2352, func(c *CPU) { c.reg.R[3] = 0x80; c.reg.R[5] = 0xDEAD }, BusWrite},
		{"MOVBM", 0x2354, func(c *CPU) { c.reg.R[3] = 0x84; c.reg.R[5] = 0xAB }, BusWrite},
		{"MOVWM", 0x2355, func(c *CPU) { c.reg.R[3] = 0x84; c.reg.R[5] = 0x1234 }, BusWrite},
		{"MOVLM", 0x2356, func(c *CPU) { c.reg.R[3] = 0x88; c.reg.R[5] = 0xDEAD }, BusWrite},
		{"MOVBS0", 0x0354, func(c *CPU) { c.reg.R[0] = 0x10; c.reg.R[3] = 0x70; c.reg.R[5] = 0xAB }, BusWrite},
		{"MOVWS0", 0x0355, func(c *CPU) { c.reg.R[0] = 0x10; c.reg.R[3] = 0x70; c.reg.R[5] = 0x1234 }, BusWrite},
		{"MOVLS0", 0x0356, func(c *CPU) { c.reg.R[0] = 0x10; c.reg.R[3] = 0x70; c.reg.R[5] = 0xDEAD }, BusWrite},
		{"MOVBSG", 0xC004, func(c *CPU) { c.reg.GBR = 0x80; c.reg.R[0] = 0xAB }, BusWrite},
		{"MOVWSG", 0xC104, func(c *CPU) { c.reg.GBR = 0x80; c.reg.R[0] = 0x1234 }, BusWrite},
		{"MOVLSG", 0xC204, func(c *CPU) { c.reg.GBR = 0x80; c.reg.R[0] = 0xDEAD }, BusWrite},
		{"MOVBS4", 0x8053, func(c *CPU) { c.reg.R[5] = 0x80; c.reg.R[0] = 0xAB }, BusWrite},
		{"MOVWS4", 0x8153, func(c *CPU) { c.reg.R[5] = 0x80; c.reg.R[0] = 0x1234 }, BusWrite},
		{"MOVLS4", 0x1353, func(c *CPU) { c.reg.R[3] = 0x80; c.reg.R[5] = 0xDEAD }, BusWrite},
		// Non-memory -> BusNone
		{"MOV", 0x6353, func(c *CPU) { c.reg.R[5] = 0xDEAD }, BusNone},
		{"MOVI", 0xE37F, func(c *CPU) {}, BusNone},
		{"MOVA", 0xC702, func(c *CPU) {}, BusNone},
		{"MOVT", 0x0329, func(c *CPU) {}, BusNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			tt.setup(cpu)
			s := cpu.Clock()
			if s.Bus != tt.wantBus {
				t.Errorf("bus = %d, want %d", s.Bus, tt.wantBus)
			}
		})
	}
}
