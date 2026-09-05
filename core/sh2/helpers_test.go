// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Shared fixtures for the interrupt and DMAC tests.

// Vector markers: fillVectorMarkers writes a distinct handler address
// into every exception vector below VBR so acceptedVector can read
// back which vector was taken from the PC after the sequence ran.
const vectorMarkerBase = 0x800

func fillVectorMarkers(c *CPU) {
	for v := uint32(0); v < 128; v++ {
		if c.bus.Read32(c.reg.VBR+v*4) == 0 {
			c.bus.Write32(c.reg.VBR+v*4, vectorMarkerBase+v*4)
		}
	}
}

// acceptedVector returns the vector whose table entry PC points at,
// or -1 if PC matches no entry (no exception was taken). Entries that
// are zero are ignored; fillVectorMarkers makes every entry distinct.
func acceptedVector(c *CPU) int {
	pc := c.reg.PC
	if pc == 0 {
		return -1
	}
	for v := uint32(0); v < 128; v++ {
		if c.bus.Read32(c.reg.VBR+v*4) == pc {
			return int(v)
		}
	}
	return -1
}

// nopProgram parks the CPU on a loop of NOPs at 0x400-0x7FF with
// interrupts masked, so cycles can be advanced without executing
// test-relevant code.
func nopProgram(c *CPU) {
	c.reg.SR = srIMask
	c.reg.PC = 0x400
	for a := uint32(0x400); a < 0x7FC; a += 2 {
		c.bus.Write16(a, 0x0009)
	}
	c.bus.Write16(0x7FC, 0xAE00) // BRA 0x400
	c.bus.Write16(0x7FE, 0x0009)
}
