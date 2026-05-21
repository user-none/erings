// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Logic instructions: AND, OR, XOR, NOT, TST

// opAND: AND Rm,Rn - Rn &= Rm
func opAND(c *CPU) {
	n := regN(c.ir)
	c.reg.R[n] &= c.reg.R[regM(c.ir)]
	c.cycles++
}

// opOR: OR Rm,Rn - Rn |= Rm
func opOR(c *CPU) {
	n := regN(c.ir)
	c.reg.R[n] |= c.reg.R[regM(c.ir)]
	c.cycles++
}

// opXOR: XOR Rm,Rn - Rn ^= Rm
func opXOR(c *CPU) {
	n := regN(c.ir)
	c.reg.R[n] ^= c.reg.R[regM(c.ir)]
	c.cycles++
}

// opTST: TST Rm,Rn - T = (Rn & Rm == 0) ? 1 : 0
func opTST(c *CPU) {
	c.reg.SetTVal(c.reg.R[regN(c.ir)]&c.reg.R[regM(c.ir)] == 0)
	c.cycles++
}

// opANDI: AND #imm,R0 - R0 &= zero-extend(imm8)
func opANDI(c *CPU) {
	c.reg.R[0] &= uint32(imm8(c.ir))
	c.cycles++
}

// opORI: OR #imm,R0 - R0 |= zero-extend(imm8)
func opORI(c *CPU) {
	c.reg.R[0] |= uint32(imm8(c.ir))
	c.cycles++
}

// opXORI: XOR #imm,R0 - R0 ^= zero-extend(imm8)
func opXORI(c *CPU) {
	c.reg.R[0] ^= uint32(imm8(c.ir))
	c.cycles++
}

// opTSTI: TST #imm,R0 - T = (R0 & zero-extend(imm8) == 0) ? 1 : 0
func opTSTI(c *CPU) {
	c.reg.SetTVal(c.reg.R[0]&uint32(imm8(c.ir)) == 0)
	c.cycles++
}

// opANDB: AND.B #imm,@(R0,GBR) - mem[R0+GBR] &= imm8
// 3 cycles: cycle 1 EX+MA read, cycle 2 EX logic, cycle 3 MA write
func opANDB(c *CPU) {
	addr := c.reg.R[0] + c.reg.GBR
	c.pendingAddr = addr
	c.pendingVal = uint32(c.read8(addr))
	c.pendingImm = uint32(imm8(c.ir))
	c.pendingN = 0 // AND
	c.stepBus = BusRead
	c.cycles++
	c.setPending(popMemRMW, 2)
}

// opORB: OR.B #imm,@(R0,GBR) - mem[R0+GBR] |= imm8
// 3 cycles: cycle 1 EX+MA read, cycle 2 EX logic, cycle 3 MA write
func opORB(c *CPU) {
	addr := c.reg.R[0] + c.reg.GBR
	c.pendingAddr = addr
	c.pendingVal = uint32(c.read8(addr))
	c.pendingImm = uint32(imm8(c.ir))
	c.pendingN = 1 // OR
	c.stepBus = BusRead
	c.cycles++
	c.setPending(popMemRMW, 2)
}

// opXORB: XOR.B #imm,@(R0,GBR) - mem[R0+GBR] ^= imm8
// 3 cycles: cycle 1 EX+MA read, cycle 2 EX logic, cycle 3 MA write
func opXORB(c *CPU) {
	addr := c.reg.R[0] + c.reg.GBR
	c.pendingAddr = addr
	c.pendingVal = uint32(c.read8(addr))
	c.pendingImm = uint32(imm8(c.ir))
	c.pendingN = 2 // XOR
	c.stepBus = BusRead
	c.cycles++
	c.setPending(popMemRMW, 2)
}

// opTSTB: TST.B #imm,@(R0,GBR) - T = (mem[R0+GBR] & imm8 == 0) ? 1 : 0
// 3 cycles: cycle 1 EX+MA read+test, cycles 2-3 idle
func opTSTB(c *CPU) {
	addr := c.reg.R[0] + c.reg.GBR
	c.reg.SetTVal(c.read8(addr)&imm8(c.ir) == 0)
	c.stepBus = BusRead
	c.cycles++
	c.setPending(popStall, 2)
}

// opNOT: NOT Rm,Rn - Rn = ~Rm
func opNOT(c *CPU) {
	c.reg.R[regN(c.ir)] = ^c.reg.R[regM(c.ir)]
	c.cycles++
}
