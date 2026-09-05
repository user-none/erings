// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// System control instructions: CLRT, SETT, CLRMAC, NOP, SLEEP,
// LDC, STC, LDS, STS, TRAPA, TAS

func opCLRT(c *CPU) {
	c.reg.ClearT()
	c.cycles++
}

func opSETT(c *CPU) {
	c.reg.SetT()
	c.cycles++
}

func opCLRMAC(c *CPU) {
	c.reg.MACH = 0
	c.reg.MACL = 0
	c.cycles++
}

func opNOP(c *CPU) {
	c.cycles++
}

func opSLEEP(c *CPU) {
	c.halted = true
	c.cycles += 3
}

// The LDC/LDC.L/STC/STC.L/LDS/LDS.L/STS/STS.L families are
// "interrupt-disabled instructions" per manual Sec 4.6.2: the
// instruction immediately following them must not accept a
// maskable interrupt. Each handler calls inhibitInterruptNext()
// to set a one-shot flag consumed by processInterrupt on the next
// Clock cycle.

// LDC Rm,SR - register to SR
func opLDCSR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.SR = c.reg.R[regN(c.ir)] & srMask
	c.cycles++
}

// LDC Rm,GBR - register to GBR
func opLDCGBR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.GBR = c.reg.R[regN(c.ir)]
	c.cycles++
}

// LDC Rm,VBR - register to VBR
func opLDCVBR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.VBR = c.reg.R[regN(c.ir)]
	c.cycles++
}

// LDC.L @Rm+,SR - memory to SR, post-increment
// 3 cycles: EX, MA read, WB
func opLDCMSR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	addr := c.reg.R[n]
	c.cycles++ // MA: read
	v := c.Read32(addr)
	c.reg.SR = v & srMask
	c.reg.R[n] = addr + 4
	c.cycles += 2 // WB
}

// LDC.L @Rm+,GBR - memory to GBR, post-increment
// 3 cycles: EX, MA read, WB
func opLDCMGBR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	addr := c.reg.R[n]
	c.cycles++ // MA: read
	v := c.Read32(addr)
	c.reg.GBR = v
	c.reg.R[n] = addr + 4
	c.cycles += 2 // WB
}

// LDC.L @Rm+,VBR - memory to VBR, post-increment
// 3 cycles: EX, MA read, WB
func opLDCMVBR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	addr := c.reg.R[n]
	c.cycles++ // MA: read
	v := c.Read32(addr)
	c.reg.VBR = v
	c.reg.R[n] = addr + 4
	c.cycles += 2 // WB
}

// STC SR,Rn - SR to register
func opSTCSR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.R[regN(c.ir)] = c.reg.SR
	c.cycles++
}

// STC GBR,Rn - GBR to register
func opSTCGBR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.R[regN(c.ir)] = c.reg.GBR
	c.cycles++
}

// STC VBR,Rn - VBR to register
func opSTCVBR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.R[regN(c.ir)] = c.reg.VBR
	c.cycles++
}

// STC.L SR,@-Rn - SR to memory, pre-decrement
// 2 cycles: EX (pre-decrement), MA write
func opSTCMSR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.R[n] -= 4
	c.cycles++ // MA: write
	c.Write32(c.reg.R[n], c.reg.SR)
	c.cycles++
}

// STC.L GBR,@-Rn - GBR to memory, pre-decrement
// 2 cycles: EX (pre-decrement), MA write
func opSTCMGBR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.R[n] -= 4
	c.cycles++ // MA: write
	c.Write32(c.reg.R[n], c.reg.GBR)
	c.cycles++
}

// STC.L VBR,@-Rn - VBR to memory, pre-decrement
// 2 cycles: EX (pre-decrement), MA write
func opSTCMVBR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.R[n] -= 4
	c.cycles++ // MA: write
	c.Write32(c.reg.R[n], c.reg.VBR)
	c.cycles++
}

// LDS Rm,MACH - register to MACH
func opLDSMACH(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.MACH = c.reg.R[regN(c.ir)]
	c.cycles++
}

// LDS Rm,MACL - register to MACL
func opLDSMACL(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.MACL = c.reg.R[regN(c.ir)]
	c.cycles++
}

// LDS Rm,PR - register to PR
func opLDSPR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.PR = c.reg.R[regN(c.ir)]
	c.cycles++
}

// STS MACH,Rn - MACH to register
func opSTSMACH(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.R[regN(c.ir)] = c.reg.MACH
	c.cycles++
}

// STS MACL,Rn - MACL to register
func opSTSMACL(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.R[regN(c.ir)] = c.reg.MACL
	c.cycles++
}

// STS PR,Rn - PR to register
func opSTSPR(c *CPU) {
	c.inhibitInterruptNext()
	c.reg.R[regN(c.ir)] = c.reg.PR
	c.cycles++
}

// LDS.L @Rm+,MACH - memory to MACH, post-increment
func opLDSMMACH(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.MACH = c.Read32(c.reg.R[n])
	c.reg.R[n] += 4
	c.cycles++
}

// LDS.L @Rm+,MACL - memory to MACL, post-increment
func opLDSMMACL(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.MACL = c.Read32(c.reg.R[n])
	c.reg.R[n] += 4
	c.cycles++
}

// LDS.L @Rm+,PR - memory to PR, post-increment
func opLDSMPR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.PR = c.Read32(c.reg.R[n])
	c.reg.R[n] += 4
	c.cycles++
}

// STS.L MACH,@-Rn - MACH to memory, pre-decrement
func opSTSMMACH(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.R[n] -= 4
	c.Write32(c.reg.R[n], c.reg.MACH)
	c.cycles++
}

// STS.L MACL,@-Rn - MACL to memory, pre-decrement
func opSTSMMACL(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.R[n] -= 4
	c.Write32(c.reg.R[n], c.reg.MACL)
	c.cycles++
}

// STS.L PR,@-Rn - PR to memory, pre-decrement
func opSTSMPR(c *CPU) {
	c.inhibitInterruptNext()
	n := regN(c.ir)
	c.reg.R[n] -= 4
	c.Write32(c.reg.R[n], c.reg.PR)
	c.cycles++
}

// TRAPA #imm - trap always
// 8 cycles (Programming Manual Figure 7.93, Table 7.1): EX, MA write
// SR, MA write PC, EX vector calc, MA read vector, 3 pipeline refill.
func opTRAPA(c *CPU) {
	imm := uint32(imm8(c.ir))
	sr, pc := c.reg.SR, c.reg.PC
	c.cycles++ // MA: write SR
	c.reg.R[15] -= 4
	c.Write32(c.reg.R[15], sr)
	c.cycles++ // MA: write PC
	c.reg.R[15] -= 4
	c.Write32(c.reg.R[15], pc)
	c.cycles += 2 // EX vector calc, MA: read vector
	c.reg.PC = c.Read32(c.reg.VBR + (imm << 2))
	c.cycles += 4
}
