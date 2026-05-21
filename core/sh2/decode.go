// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Instruction field extraction helpers.
// SH-2 instructions are 16 bits wide with various field layouts.

// nibble extracts a 4-bit nibble from a 16-bit instruction.
// Position 3 is bits 15-12, position 0 is bits 3-0.
func nibble(op uint16, pos int) uint8 {
	return uint8((op >> (pos * 4)) & 0xF)
}

// regN extracts the Rn field (bits 11-8) from an instruction.
func regN(op uint16) uint8 {
	return uint8((op >> 8) & 0xF)
}

// regM extracts the Rm field (bits 7-4) from an instruction.
func regM(op uint16) uint8 {
	return uint8((op >> 4) & 0xF)
}

// imm8 extracts an 8-bit immediate (bits 7-0) from an instruction.
func imm8(op uint16) uint8 {
	return uint8(op & 0xFF)
}

// imm4 extracts a 4-bit immediate (bits 3-0) from an instruction.
func imm4(op uint16) uint8 {
	return uint8(op & 0xF)
}

// disp8 extracts a signed 8-bit displacement (bits 7-0).
func disp8(op uint16) int8 {
	return int8(op & 0xFF)
}

// disp12 extracts a signed 12-bit displacement (bits 11-0).
func disp12(op uint16) int16 {
	d := int16(op & 0xFFF)
	if d&0x800 != 0 {
		d |= -0x1000 // sign extend
	}
	return d
}
