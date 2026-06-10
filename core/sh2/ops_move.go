// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Data transfer instructions: MOV, MOVA, MOVT, SWAP, XTRCT

// opMOV: MOV Rm,Rn - Rn = Rm
func opMOV(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = c.reg.R[m]
	c.cycles++
}

// opMOVI: MOV #imm,Rn - Rn = sign-extend(imm8)
func opMOVI(c *CPU) {
	n := regN(c.ir)
	c.reg.R[n] = uint32(int32(int8(imm8(c.ir))))
	c.cycles++
}

// opMOVBL: MOV.B @Rm,Rn - Rn = sign-extend(byte @Rm)
func opMOVBL(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int8(c.read8(c.reg.R[m]))))
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVWL: MOV.W @Rm,Rn - Rn = sign-extend(word @Rm)
func opMOVWL(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int16(c.read16(c.reg.R[m]))))
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVLL: MOV.L @Rm,Rn - Rn = long @Rm
func opMOVLL(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = c.read32(c.reg.R[m])
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVBS: MOV.B Rm,@Rn - Write byte Rm to @Rn
func opMOVBS(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write8(c.reg.R[n], uint8(c.reg.R[m]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVWS: MOV.W Rm,@Rn - Write word Rm to @Rn
func opMOVWS(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write16(c.reg.R[n], uint16(c.reg.R[m]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVLS: MOV.L Rm,@Rn - Write long Rm to @Rn
func opMOVLS(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write32(c.reg.R[n], c.reg.R[m])
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVBP: MOV.B @Rm+,Rn - Rn = sign-extend(byte @Rm); if n!=m: Rm+=1
func opMOVBP(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int8(c.read8(c.reg.R[m]))))
	if n != m {
		c.reg.R[m]++
	}
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVWP: MOV.W @Rm+,Rn - Rn = sign-extend(word @Rm); if n!=m: Rm+=2
func opMOVWP(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int16(c.read16(c.reg.R[m]))))
	if n != m {
		c.reg.R[m] += 2
	}
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVLP: MOV.L @Rm+,Rn - Rn = long @Rm; if n!=m: Rm+=4
func opMOVLP(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = c.read32(c.reg.R[m])
	if n != m {
		c.reg.R[m] += 4
	}
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVBM: MOV.B Rm,@-Rn - Write byte Rm to @(Rn-1); Rn-=1
func opMOVBM(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write8(c.reg.R[n]-1, uint8(c.reg.R[m]))
	c.reg.R[n]--
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVWM: MOV.W Rm,@-Rn - Write word Rm to @(Rn-2); Rn-=2
func opMOVWM(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write16(c.reg.R[n]-2, uint16(c.reg.R[m]))
	c.reg.R[n] -= 2
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVLM: MOV.L Rm,@-Rn - Write long Rm to @(Rn-4); Rn-=4
func opMOVLM(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write32(c.reg.R[n]-4, c.reg.R[m])
	c.reg.R[n] -= 4
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVBS0: MOV.B Rm,@(R0,Rn) - Write byte Rm to @(R0+Rn)
func opMOVBS0(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write8(c.reg.R[0]+c.reg.R[n], uint8(c.reg.R[m]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVWS0: MOV.W Rm,@(R0,Rn) - Write word Rm to @(R0+Rn)
func opMOVWS0(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write16(c.reg.R[0]+c.reg.R[n], uint16(c.reg.R[m]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVLS0: MOV.L Rm,@(R0,Rn) - Write long Rm to @(R0+Rn)
func opMOVLS0(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.write32(c.reg.R[0]+c.reg.R[n], c.reg.R[m])
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVBL0: MOV.B @(R0,Rm),Rn - Rn = sign-extend(byte @(R0+Rm))
func opMOVBL0(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int8(c.read8(c.reg.R[0] + c.reg.R[m]))))
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVWL0: MOV.W @(R0,Rm),Rn - Rn = sign-extend(word @(R0+Rm))
func opMOVWL0(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int16(c.read16(c.reg.R[0] + c.reg.R[m]))))
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVLL0: MOV.L @(R0,Rm),Rn - Rn = long @(R0+Rm)
func opMOVLL0(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = c.read32(c.reg.R[0] + c.reg.R[m])
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVBSG: MOV.B R0,@(disp,GBR) - Write byte R0 to @(GBR+disp8)
func opMOVBSG(c *CPU) {
	disp := uint32(imm8(c.ir))
	c.write8(c.reg.GBR+disp, uint8(c.reg.R[0]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVWSG: MOV.W R0,@(disp,GBR) - Write word R0 to @(GBR+disp8*2)
func opMOVWSG(c *CPU) {
	disp := uint32(imm8(c.ir)) * 2
	c.write16(c.reg.GBR+disp, uint16(c.reg.R[0]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVLSG: MOV.L R0,@(disp,GBR) - Write long R0 to @(GBR+disp8*4)
func opMOVLSG(c *CPU) {
	disp := uint32(imm8(c.ir)) * 4
	c.write32(c.reg.GBR+disp, c.reg.R[0])
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVBLG: MOV.B @(disp,GBR),R0 - R0 = sign-extend(byte @(GBR+disp8))
func opMOVBLG(c *CPU) {
	disp := uint32(imm8(c.ir))
	c.reg.R[0] = uint32(int32(int8(c.read8(c.reg.GBR + disp))))
	c.lastLoadReg = 0
	c.stepBus = BusRead
	c.cycles++
}

// opMOVWLG: MOV.W @(disp,GBR),R0 - R0 = sign-extend(word @(GBR+disp8*2))
func opMOVWLG(c *CPU) {
	disp := uint32(imm8(c.ir)) * 2
	c.reg.R[0] = uint32(int32(int16(c.read16(c.reg.GBR + disp))))
	c.lastLoadReg = 0
	c.stepBus = BusRead
	c.cycles++
}

// opMOVLLG: MOV.L @(disp,GBR),R0 - R0 = long @(GBR+disp8*4)
func opMOVLLG(c *CPU) {
	disp := uint32(imm8(c.ir)) * 4
	c.reg.R[0] = c.read32(c.reg.GBR + disp)
	c.lastLoadReg = 0
	c.stepBus = BusRead
	c.cycles++
}

// opMOVBS4: MOV.B R0,@(disp,Rn) - Write byte R0 to @(Rn+disp4)
func opMOVBS4(c *CPU) {
	n := regM(c.ir) // bits 7-4 hold Rn for group 8 instructions
	disp := uint32(imm4(c.ir))
	c.write8(c.reg.R[n]+disp, uint8(c.reg.R[0]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVWS4: MOV.W R0,@(disp,Rn) - Write word R0 to @(Rn+disp4*2)
func opMOVWS4(c *CPU) {
	n := regM(c.ir) // bits 7-4 hold Rn for group 8 instructions
	disp := uint32(imm4(c.ir)) * 2
	c.write16(c.reg.R[n]+disp, uint16(c.reg.R[0]))
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVLS4: MOV.L Rm,@(disp,Rn) - Write long Rm to @(Rn+disp4*4)
func opMOVLS4(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	disp := uint32(imm4(c.ir)) * 4
	c.write32(c.reg.R[n]+disp, c.reg.R[m])
	c.stepBus = BusWrite
	c.cycles++
}

// opMOVBL4: MOV.B @(disp,Rm),R0 - R0 = sign-extend(byte @(Rm+disp4))
func opMOVBL4(c *CPU) {
	m := regM(c.ir) // bits 7-4 hold Rm for group 8 instructions
	disp := uint32(imm4(c.ir))
	c.reg.R[0] = uint32(int32(int8(c.read8(c.reg.R[m] + disp))))
	c.lastLoadReg = 0
	c.stepBus = BusRead
	c.cycles++
}

// opMOVWL4: MOV.W @(disp,Rm),R0 - R0 = sign-extend(word @(Rm+disp4*2))
func opMOVWL4(c *CPU) {
	m := regM(c.ir) // bits 7-4 hold Rm for group 8 instructions
	disp := uint32(imm4(c.ir)) * 2
	c.reg.R[0] = uint32(int32(int16(c.read16(c.reg.R[m] + disp))))
	c.lastLoadReg = 0
	c.stepBus = BusRead
	c.cycles++
}

// opMOVLL4: MOV.L @(disp,Rm),Rn - Rn = long @(Rm+disp4*4)
func opMOVLL4(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	disp := uint32(imm4(c.ir)) * 4
	c.reg.R[n] = c.read32(c.reg.R[m] + disp)
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// pcRelBase returns the PC value seen by PC-relative addressing:
// the address four bytes after the instruction (fetchPC has already
// advanced by 2). In a delay slot the PC has been redirected, so the
// instruction sees the branch destination + 2 instead.
func (c *CPU) pcRelBase() uint32 {
	if c.inDelay {
		return c.delayPC + 2
	}
	return c.reg.PC + 2
}

// opMOVWI: MOV.W @(disp,PC),Rn - Rn = sign-extend(word @(PC+disp8*2))
func opMOVWI(c *CPU) {
	n := regN(c.ir)
	disp := uint32(imm8(c.ir)) * 2
	addr := c.pcRelBase() + disp
	c.reg.R[n] = uint32(int32(int16(c.read16(addr))))
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVLI: MOV.L @(disp,PC),Rn - Rn = long @((PC & ~3) + disp8*4)
func opMOVLI(c *CPU) {
	n := regN(c.ir)
	disp := uint32(imm8(c.ir)) * 4
	addr := (c.pcRelBase() & 0xFFFFFFFC) + disp
	c.reg.R[n] = c.read32(addr)
	c.lastLoadReg = n
	c.stepBus = BusRead
	c.cycles++
}

// opMOVA: MOVA @(disp,PC),R0 - R0 = (PC & ~3) + disp8*4
func opMOVA(c *CPU) {
	disp := uint32(imm8(c.ir)) * 4
	c.reg.R[0] = (c.pcRelBase() & 0xFFFFFFFC) + disp
	c.cycles++
}

// opMOVT: MOVT Rn - Rn = T bit
func opMOVT(c *CPU) {
	n := regN(c.ir)
	c.reg.R[n] = c.reg.T()
	c.cycles++
}

// opSWAPB: SWAP.B Rm,Rn - swap lower two bytes of Rm into Rn
func opSWAPB(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rm := c.reg.R[m]
	c.reg.R[n] = (rm & 0xFFFF0000) | ((rm & 0xFF) << 8) | ((rm >> 8) & 0xFF)
	c.cycles++
}

// opSWAPW: SWAP.W Rm,Rn - swap upper and lower words of Rm into Rn
func opSWAPW(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rm := c.reg.R[m]
	c.reg.R[n] = (rm << 16) | (rm >> 16)
	c.cycles++
}

// opXTRCT: XTRCT Rm,Rn - Rn = middle 32 bits of {Rm,Rn}
func opXTRCT(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = (c.reg.R[m] << 16) | (c.reg.R[n] >> 16)
	c.cycles++
}
