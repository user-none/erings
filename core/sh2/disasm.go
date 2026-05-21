// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "fmt"

// Disassemble decodes a 16-bit SH-2 instruction and returns a
// human-readable string. The pc parameter is the address of the
// instruction, used for PC-relative address calculation.
func Disassemble(pc uint32, op uint16) string {
	switch nibble(op, 3) {
	case 0x0:
		return disasmGroup0(op)
	case 0x1:
		return disasmMOVLS4(op)
	case 0x2:
		return disasmGroup2(op)
	case 0x3:
		return disasmGroup3(op)
	case 0x4:
		return disasmGroup4(op)
	case 0x5:
		return disasmMOVLL4(op)
	case 0x6:
		return disasmGroup6(op)
	case 0x7:
		return disasmADDI(op)
	case 0x8:
		return disasmGroup8(pc, op)
	case 0x9:
		return disasmMOVWI(pc, op)
	case 0xA:
		return disasmBRA(pc, op)
	case 0xB:
		return disasmBSR(pc, op)
	case 0xC:
		return disasmGroupC(pc, op)
	case 0xD:
		return disasmMOVLI(pc, op)
	case 0xE:
		return disasmMOVI(op)
	}
	return illegal(op)
}

func reg(n uint8) string {
	return fmt.Sprintf("R%d", n)
}

func illegal(op uint16) string {
	return fmt.Sprintf(".word H'%04X", op)
}

func disasmGroup0(op uint16) string {
	switch nibble(op, 0) {
	case 0x2:
		switch nibble(op, 1) {
		case 0x0:
			return fmt.Sprintf("STC SR,%s", reg(regN(op)))
		case 0x1:
			return fmt.Sprintf("STC GBR,%s", reg(regN(op)))
		case 0x2:
			return fmt.Sprintf("STC VBR,%s", reg(regN(op)))
		}
	case 0x3:
		switch nibble(op, 1) {
		case 0x0:
			return fmt.Sprintf("BSRF %s", reg(regN(op)))
		case 0x2:
			return fmt.Sprintf("BRAF %s", reg(regN(op)))
		}
	case 0x4:
		return fmt.Sprintf("MOV.B %s,@(R0,%s)", reg(regM(op)), reg(regN(op)))
	case 0x5:
		return fmt.Sprintf("MOV.W %s,@(R0,%s)", reg(regM(op)), reg(regN(op)))
	case 0x6:
		return fmt.Sprintf("MOV.L %s,@(R0,%s)", reg(regM(op)), reg(regN(op)))
	case 0x7:
		return fmt.Sprintf("MUL.L %s,%s", reg(regM(op)), reg(regN(op)))
	case 0x8:
		switch nibble(op, 1) {
		case 0x0:
			return "CLRT"
		case 0x1:
			return "SETT"
		case 0x2:
			return "CLRMAC"
		}
	case 0x9:
		switch nibble(op, 1) {
		case 0x0:
			return "NOP"
		case 0x1:
			return "DIV0U"
		case 0x2:
			return fmt.Sprintf("MOVT %s", reg(regN(op)))
		}
	case 0xA:
		switch nibble(op, 1) {
		case 0x0:
			return fmt.Sprintf("STS MACH,%s", reg(regN(op)))
		case 0x1:
			return fmt.Sprintf("STS MACL,%s", reg(regN(op)))
		case 0x2:
			return fmt.Sprintf("STS PR,%s", reg(regN(op)))
		}
	case 0xB:
		switch nibble(op, 1) {
		case 0x0:
			return "RTS"
		case 0x1:
			return "SLEEP"
		case 0x2:
			return "RTE"
		}
	case 0xC:
		return fmt.Sprintf("MOV.B @(R0,%s),%s", reg(regM(op)), reg(regN(op)))
	case 0xD:
		return fmt.Sprintf("MOV.W @(R0,%s),%s", reg(regM(op)), reg(regN(op)))
	case 0xE:
		return fmt.Sprintf("MOV.L @(R0,%s),%s", reg(regM(op)), reg(regN(op)))
	case 0xF:
		return fmt.Sprintf("MAC.L @%s+,@%s+", reg(regM(op)), reg(regN(op)))
	}
	return illegal(op)
}

func disasmGroup2(op uint16) string {
	n, m := reg(regN(op)), reg(regM(op))
	switch nibble(op, 0) {
	case 0x0:
		return fmt.Sprintf("MOV.B %s,@%s", m, n)
	case 0x1:
		return fmt.Sprintf("MOV.W %s,@%s", m, n)
	case 0x2:
		return fmt.Sprintf("MOV.L %s,@%s", m, n)
	case 0x4:
		return fmt.Sprintf("MOV.B %s,@-%s", m, n)
	case 0x5:
		return fmt.Sprintf("MOV.W %s,@-%s", m, n)
	case 0x6:
		return fmt.Sprintf("MOV.L %s,@-%s", m, n)
	case 0x7:
		return fmt.Sprintf("DIV0S %s,%s", m, n)
	case 0x8:
		return fmt.Sprintf("TST %s,%s", m, n)
	case 0x9:
		return fmt.Sprintf("AND %s,%s", m, n)
	case 0xA:
		return fmt.Sprintf("XOR %s,%s", m, n)
	case 0xB:
		return fmt.Sprintf("OR %s,%s", m, n)
	case 0xC:
		return fmt.Sprintf("CMP/STR %s,%s", m, n)
	case 0xD:
		return fmt.Sprintf("XTRCT %s,%s", m, n)
	case 0xE:
		return fmt.Sprintf("MULU.W %s,%s", m, n)
	case 0xF:
		return fmt.Sprintf("MULS.W %s,%s", m, n)
	}
	return illegal(op)
}

func disasmGroup3(op uint16) string {
	n, m := reg(regN(op)), reg(regM(op))
	switch nibble(op, 0) {
	case 0x0:
		return fmt.Sprintf("CMP/EQ %s,%s", m, n)
	case 0x2:
		return fmt.Sprintf("CMP/HS %s,%s", m, n)
	case 0x3:
		return fmt.Sprintf("CMP/GE %s,%s", m, n)
	case 0x4:
		return fmt.Sprintf("DIV1 %s,%s", m, n)
	case 0x5:
		return fmt.Sprintf("DMULU.L %s,%s", m, n)
	case 0x6:
		return fmt.Sprintf("CMP/HI %s,%s", m, n)
	case 0x7:
		return fmt.Sprintf("CMP/GT %s,%s", m, n)
	case 0x8:
		return fmt.Sprintf("SUB %s,%s", m, n)
	case 0xA:
		return fmt.Sprintf("SUBC %s,%s", m, n)
	case 0xB:
		return fmt.Sprintf("SUBV %s,%s", m, n)
	case 0xC:
		return fmt.Sprintf("ADD %s,%s", m, n)
	case 0xD:
		return fmt.Sprintf("DMULS.L %s,%s", m, n)
	case 0xE:
		return fmt.Sprintf("ADDC %s,%s", m, n)
	case 0xF:
		return fmt.Sprintf("ADDV %s,%s", m, n)
	}
	return illegal(op)
}

func disasmGroup4(op uint16) string {
	n := reg(regN(op))
	switch imm8(op) {
	case 0x00:
		return fmt.Sprintf("SHLL %s", n)
	case 0x01:
		return fmt.Sprintf("SHLR %s", n)
	case 0x02:
		return fmt.Sprintf("STS.L MACH,@-%s", n)
	case 0x03:
		return fmt.Sprintf("STC.L SR,@-%s", n)
	case 0x04:
		return fmt.Sprintf("ROTL %s", n)
	case 0x05:
		return fmt.Sprintf("ROTR %s", n)
	case 0x06:
		return fmt.Sprintf("LDS.L @%s+,MACH", n)
	case 0x07:
		return fmt.Sprintf("LDC.L @%s+,SR", n)
	case 0x08:
		return fmt.Sprintf("SHLL2 %s", n)
	case 0x09:
		return fmt.Sprintf("SHLR2 %s", n)
	case 0x0A:
		return fmt.Sprintf("LDS %s,MACH", n)
	case 0x0B:
		return fmt.Sprintf("JSR @%s", n)
	case 0x0E:
		return fmt.Sprintf("LDC %s,SR", n)
	case 0x10:
		return fmt.Sprintf("DT %s", n)
	case 0x11:
		return fmt.Sprintf("CMP/PZ %s", n)
	case 0x12:
		return fmt.Sprintf("STS.L MACL,@-%s", n)
	case 0x13:
		return fmt.Sprintf("STC.L GBR,@-%s", n)
	case 0x15:
		return fmt.Sprintf("CMP/PL %s", n)
	case 0x16:
		return fmt.Sprintf("LDS.L @%s+,MACL", n)
	case 0x17:
		return fmt.Sprintf("LDC.L @%s+,GBR", n)
	case 0x18:
		return fmt.Sprintf("SHLL8 %s", n)
	case 0x19:
		return fmt.Sprintf("SHLR8 %s", n)
	case 0x1A:
		return fmt.Sprintf("LDS %s,MACL", n)
	case 0x1B:
		return fmt.Sprintf("TAS.B @%s", n)
	case 0x1E:
		return fmt.Sprintf("LDC %s,GBR", n)
	case 0x20:
		return fmt.Sprintf("SHAL %s", n)
	case 0x21:
		return fmt.Sprintf("SHAR %s", n)
	case 0x22:
		return fmt.Sprintf("STS.L PR,@-%s", n)
	case 0x23:
		return fmt.Sprintf("STC.L VBR,@-%s", n)
	case 0x24:
		return fmt.Sprintf("ROTCL %s", n)
	case 0x25:
		return fmt.Sprintf("ROTCR %s", n)
	case 0x26:
		return fmt.Sprintf("LDS.L @%s+,PR", n)
	case 0x27:
		return fmt.Sprintf("LDC.L @%s+,VBR", n)
	case 0x28:
		return fmt.Sprintf("SHLL16 %s", n)
	case 0x29:
		return fmt.Sprintf("SHLR16 %s", n)
	case 0x2A:
		return fmt.Sprintf("LDS %s,PR", n)
	case 0x2B:
		return fmt.Sprintf("JMP @%s", n)
	case 0x2E:
		return fmt.Sprintf("LDC %s,VBR", n)
	default:
		if nibble(op, 0) == 0xF {
			return fmt.Sprintf("MAC.W @%s+,@%s+", reg(regM(op)), n)
		}
	}
	return illegal(op)
}

func disasmGroup6(op uint16) string {
	n, m := reg(regN(op)), reg(regM(op))
	switch nibble(op, 0) {
	case 0x0:
		return fmt.Sprintf("MOV.B @%s,%s", m, n)
	case 0x1:
		return fmt.Sprintf("MOV.W @%s,%s", m, n)
	case 0x2:
		return fmt.Sprintf("MOV.L @%s,%s", m, n)
	case 0x3:
		return fmt.Sprintf("MOV %s,%s", m, n)
	case 0x4:
		return fmt.Sprintf("MOV.B @%s+,%s", m, n)
	case 0x5:
		return fmt.Sprintf("MOV.W @%s+,%s", m, n)
	case 0x6:
		return fmt.Sprintf("MOV.L @%s+,%s", m, n)
	case 0x7:
		return fmt.Sprintf("NOT %s,%s", m, n)
	case 0x8:
		return fmt.Sprintf("SWAP.B %s,%s", m, n)
	case 0x9:
		return fmt.Sprintf("SWAP.W %s,%s", m, n)
	case 0xA:
		return fmt.Sprintf("NEGC %s,%s", m, n)
	case 0xB:
		return fmt.Sprintf("NEG %s,%s", m, n)
	case 0xC:
		return fmt.Sprintf("EXTU.B %s,%s", m, n)
	case 0xD:
		return fmt.Sprintf("EXTU.W %s,%s", m, n)
	case 0xE:
		return fmt.Sprintf("EXTS.B %s,%s", m, n)
	case 0xF:
		return fmt.Sprintf("EXTS.W %s,%s", m, n)
	}
	return illegal(op)
}

func disasmGroup8(pc uint32, op uint16) string {
	switch nibble(op, 2) {
	case 0x0:
		d := uint32(imm4(op))
		return fmt.Sprintf("MOV.B R0,@(%d,%s)", d, reg(regM(op)))
	case 0x1:
		d := uint32(imm4(op)) * 2
		return fmt.Sprintf("MOV.W R0,@(%d,%s)", d, reg(regM(op)))
	case 0x4:
		d := uint32(imm4(op))
		return fmt.Sprintf("MOV.B @(%d,%s),R0", d, reg(regM(op)))
	case 0x5:
		d := uint32(imm4(op)) * 2
		return fmt.Sprintf("MOV.W @(%d,%s),R0", d, reg(regM(op)))
	case 0x8:
		return fmt.Sprintf("CMP/EQ #%d,R0", disp8(op))
	case 0x9:
		target := pc + 4 + uint32(int32(disp8(op))*2)
		return fmt.Sprintf("BT H'%08X", target)
	case 0xB:
		target := pc + 4 + uint32(int32(disp8(op))*2)
		return fmt.Sprintf("BF H'%08X", target)
	case 0xD:
		target := pc + 4 + uint32(int32(disp8(op))*2)
		return fmt.Sprintf("BT/S H'%08X", target)
	case 0xF:
		target := pc + 4 + uint32(int32(disp8(op))*2)
		return fmt.Sprintf("BF/S H'%08X", target)
	}
	return illegal(op)
}

// MOV.L Rm,@(disp,Rn) - group 1
func disasmMOVLS4(op uint16) string {
	d := uint32(imm4(op)) * 4
	return fmt.Sprintf("MOV.L %s,@(%d,%s)", reg(regM(op)), d, reg(regN(op)))
}

// MOV.L @(disp,Rm),Rn - group 5
func disasmMOVLL4(op uint16) string {
	d := uint32(imm4(op)) * 4
	return fmt.Sprintf("MOV.L @(%d,%s),%s", d, reg(regM(op)), reg(regN(op)))
}

// ADD #imm,Rn - group 7
func disasmADDI(op uint16) string {
	return fmt.Sprintf("ADD #%d,%s", disp8(op), reg(regN(op)))
}

// MOV.W @(disp,PC),Rn - group 9
func disasmMOVWI(pc uint32, op uint16) string {
	target := pc + 4 + uint32(imm8(op))*2
	return fmt.Sprintf("MOV.W @(H'%08X),%s", target, reg(regN(op)))
}

// BRA - group A
func disasmBRA(pc uint32, op uint16) string {
	target := pc + 4 + uint32(int32(disp12(op))*2)
	return fmt.Sprintf("BRA H'%08X", target)
}

// BSR - group B
func disasmBSR(pc uint32, op uint16) string {
	target := pc + 4 + uint32(int32(disp12(op))*2)
	return fmt.Sprintf("BSR H'%08X", target)
}

// MOV.L @(disp,PC),Rn - group D
func disasmMOVLI(pc uint32, op uint16) string {
	target := (pc & 0xFFFFFFFC) + 4 + uint32(imm8(op))*4
	return fmt.Sprintf("MOV.L @(H'%08X),%s", target, reg(regN(op)))
}

// MOV #imm,Rn - group E
func disasmMOVI(op uint16) string {
	return fmt.Sprintf("MOV #%d,%s", disp8(op), reg(regN(op)))
}

func disasmGroupC(pc uint32, op uint16) string {
	d := uint32(imm8(op))
	switch nibble(op, 2) {
	case 0x0:
		return fmt.Sprintf("MOV.B R0,@(%d,GBR)", d)
	case 0x1:
		return fmt.Sprintf("MOV.W R0,@(%d,GBR)", d*2)
	case 0x2:
		return fmt.Sprintf("MOV.L R0,@(%d,GBR)", d*4)
	case 0x3:
		return fmt.Sprintf("TRAPA #%d", d)
	case 0x4:
		return fmt.Sprintf("MOV.B @(%d,GBR),R0", d)
	case 0x5:
		return fmt.Sprintf("MOV.W @(%d,GBR),R0", d*2)
	case 0x6:
		return fmt.Sprintf("MOV.L @(%d,GBR),R0", d*4)
	case 0x7:
		target := (pc & 0xFFFFFFFC) + 4 + d*4
		return fmt.Sprintf("MOVA @(H'%08X),R0", target)
	case 0x8:
		return fmt.Sprintf("TST #%d,R0", d)
	case 0x9:
		return fmt.Sprintf("AND #%d,R0", d)
	case 0xA:
		return fmt.Sprintf("XOR #%d,R0", d)
	case 0xB:
		return fmt.Sprintf("OR #%d,R0", d)
	case 0xC:
		return fmt.Sprintf("TST.B #%d,@(R0,GBR)", d)
	case 0xD:
		return fmt.Sprintf("AND.B #%d,@(R0,GBR)", d)
	case 0xE:
		return fmt.Sprintf("XOR.B #%d,@(R0,GBR)", d)
	case 0xF:
		return fmt.Sprintf("OR.B #%d,@(R0,GBR)", d)
	}
	return illegal(op)
}
