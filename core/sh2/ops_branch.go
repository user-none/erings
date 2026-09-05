// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Branch instructions: BRA, BSR, BT, BF, BT/S, BF/S, JMP, JSR, RTS, RTE

// Delayed-branch ops charge 2 cycles per SH-2 Programming Manual
// Appendix A Table A.1: 1 for the branch body + 1 for the delay-slot
// ID-stage stall (Fig 7.82).

func opBRA(c *CPU) {
	disp := int32(disp12(c.ir))
	target := uint32(int32(c.reg.PC+2) + disp*2)
	c.delayBranch(target)
	c.cycles += 2
}

func opBRAF(c *CPU) {
	m := regN(c.ir)
	target := c.reg.PC + 2 + c.reg.R[m]
	c.delayBranch(target)
	c.cycles += 2
}

func opBSR(c *CPU) {
	disp := int32(disp12(c.ir))
	target := uint32(int32(c.reg.PC+2) + disp*2)
	c.reg.PR = c.reg.PC + 2
	c.delayBranch(target)
	c.cycles += 2
}

func opBSRF(c *CPU) {
	m := regN(c.ir)
	target := c.reg.PC + 2 + c.reg.R[m]
	c.reg.PR = c.reg.PC + 2
	c.delayBranch(target)
	c.cycles += 2
}

func opBT(c *CPU) {
	if c.reg.T() != 0 {
		disp := int32(disp8(c.ir))
		c.reg.PC = uint32(int32(c.reg.PC+2) + disp*2)
		c.cycles += 3
	} else {
		c.cycles++
	}
}

func opBF(c *CPU) {
	if c.reg.T() == 0 {
		disp := int32(disp8(c.ir))
		c.reg.PC = uint32(int32(c.reg.PC+2) + disp*2)
		c.cycles += 3
	} else {
		c.cycles++
	}
}

func opBTS(c *CPU) {
	if c.reg.T() != 0 {
		disp := int32(disp8(c.ir))
		target := uint32(int32(c.reg.PC+2) + disp*2)
		c.delayBranch(target)
		c.cycles += 2
	} else {
		c.cycles++
	}
}

func opBFS(c *CPU) {
	if c.reg.T() == 0 {
		disp := int32(disp8(c.ir))
		target := uint32(int32(c.reg.PC+2) + disp*2)
		c.delayBranch(target)
		c.cycles += 2
	} else {
		c.cycles++
	}
}

func opJMP(c *CPU) {
	m := regN(c.ir)
	c.delayBranch(c.reg.R[m])
	c.cycles += 2
}

func opJSR(c *CPU) {
	m := regN(c.ir)
	c.reg.PR = c.reg.PC + 2
	c.delayBranch(c.reg.R[m])
	c.cycles += 2
}

func opRTS(c *CPU) {
	c.delayBranch(c.reg.PR)
	c.cycles += 2
}

func opRTE(c *CPU) {
	// RTE: 4 cycles total per Table A.1 - EX, the two stack reads
	// (Figure 7.92), and the delay-slot ID stall (Figure 7.82).
	sp := c.reg.R[15]
	c.cycles++ // MA: read PC
	pc := c.Read32(sp)
	c.reg.R[15] = sp + 4
	c.cycles++ // MA: read SR
	c.reg.SR = c.Read32(sp+4) & srMask
	c.reg.R[15] = sp + 8
	c.delayBranch(pc)
	c.cycles += 2
}
