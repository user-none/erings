// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Bit manipulation instructions: TAS

// TAS.B @Rn - test and set bit 7
// 4 cycles: cycle 1 EX, cycle 2 MA read, cycle 3 MA internal, cycle 4 MA write
func opTAS(c *CPU) {
	n := regN(c.ir)
	c.pendingAddr = c.reg.R[n]
	c.cycles++
	c.setPending(popTAS, 3)
}
