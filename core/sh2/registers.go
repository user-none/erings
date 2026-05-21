// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// SR flag bit positions.
const (
	srT = 0 // True/false condition bit
	srS = 1 // Specifies saturation for MAC instruction
	srI = 4 // Interrupt mask bits (I3-I0), 4 bits wide
	srQ = 8 // State for divide step (DIV0U/DIV0S/DIV1)
	srM = 9 // State for divide step (DIV0U/DIV0S/DIV1)
)

// SR bitmasks.
const (
	srTMask = 1 << srT   // 0x00000001
	srSMask = 1 << srS   // 0x00000002
	srIMask = 0xF << srI // 0x000000F0
	srQMask = 1 << srQ   // 0x00000100
	srMMask = 1 << srM   // 0x00000200
	srMask  = srTMask | srSMask | srIMask | srQMask | srMMask
)

// Registers holds the programmer-visible state of the SH-2.
type Registers struct {
	R    [16]uint32 // General purpose registers (R15 is stack pointer)
	PC   uint32     // Program counter
	PR   uint32     // Procedure register (return address for BSR/JSR)
	SR   uint32     // Status register
	GBR  uint32     // Global base register
	VBR  uint32     // Vector base register
	MACH uint32     // Multiply-accumulate high
	MACL uint32     // Multiply-accumulate low
}

// T returns the T bit from SR.
func (r *Registers) T() uint32 {
	return r.SR & srTMask
}

// SetT sets the T bit in SR.
func (r *Registers) SetT() {
	r.SR |= srTMask
}

// ClearT clears the T bit in SR.
func (r *Registers) ClearT() {
	r.SR &^= srTMask
}

// SetTVal sets the T bit to 1 if v is true, 0 otherwise.
func (r *Registers) SetTVal(v bool) {
	if v {
		r.SR |= srTMask
	} else {
		r.SR &^= srTMask
	}
}

// S returns the S bit from SR.
func (r *Registers) S() bool {
	return r.SR&srSMask != 0
}

// IMASK returns the interrupt mask level (0-15) from SR.
func (r *Registers) IMASK() uint8 {
	return uint8((r.SR & srIMask) >> srI)
}

// SetIMASK sets the interrupt mask bits in SR.
func (r *Registers) SetIMASK(level uint8) {
	r.SR = (r.SR &^ srIMask) | (uint32(level&0xF) << srI)
}
