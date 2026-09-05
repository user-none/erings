// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Bit manipulation instructions: TAS

// TAS.B @Rn - test and set bit 7
// 4 cycles: EX, MA read, MA internal, MA write. The bus claim taken at
// the read is held until the write (SH7604 Sec 7.10).
func opTAS(c *CPU) {
	addr := c.reg.R[regN(c.ir)]
	c.cycles++ // MA: read, test
	val := c.tasRead8(addr)
	c.reg.SetTVal(val == 0)
	c.cycles += 2 // MA internal, MA: write
	c.tasWrite8(addr, val|0x80)
	c.cycles++
}
