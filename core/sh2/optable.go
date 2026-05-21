// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

type opHandler func(*CPU)

var handlerTable [65536]opHandler

func init() {
	for i := range handlerTable {
		handlerTable[i] = opIllegal
	}
	registerGroup0()
	registerGroup1()
	registerGroup2()
	registerGroup3()
	registerGroup4()
	registerGroup5()
	registerGroup6()
	registerGroup7()
	registerGroup8()
	registerGroup9()
	registerGroupA()
	registerGroupB()
	registerGroupC()
	registerGroupD()
	registerGroupE()
}

func opIllegal(c *CPU) {
	c.serviceException(vecIllegalInstr)
}

// fillRange assigns handler to all opcodes in [base, base+count).
func fillRange(base int, count int, handler opHandler) {
	for i := 0; i < count; i++ {
		handlerTable[base|i] = handler
	}
}

// registerGroup0 handles opcodes 0x0nnn.
// Secondary dispatch on nibble 0 (bits 3-0), some tertiary on nibble 1.
func registerGroup0() {
	// nibble0 = 0x2: STC - tertiary on nibble 1
	for n := 0; n < 16; n++ {
		handlerTable[0x0000|n<<8|0x0<<4|0x2] = opSTCSR
		handlerTable[0x0000|n<<8|0x1<<4|0x2] = opSTCGBR
		handlerTable[0x0000|n<<8|0x2<<4|0x2] = opSTCVBR
	}

	// nibble0 = 0x3: BSRF/BRAF - tertiary on nibble 1
	for n := 0; n < 16; n++ {
		handlerTable[0x0000|n<<8|0x0<<4|0x3] = opBSRF
		handlerTable[0x0000|n<<8|0x2<<4|0x3] = opBRAF
	}

	// nibble0 = 0x4: MOV.B Rm,@(R0,Rn)
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0x4] = opMOVBS0
		}
	}

	// nibble0 = 0x5: MOV.W Rm,@(R0,Rn)
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0x5] = opMOVWS0
		}
	}

	// nibble0 = 0x6: MOV.L Rm,@(R0,Rn)
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0x6] = opMOVLS0
		}
	}

	// nibble0 = 0x7: MUL.L Rm,Rn
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0x7] = opMULL
		}
	}

	// nibble0 = 0x8: CLRT/SETT/CLRMAC - tertiary on nibble 1
	for n := 0; n < 16; n++ {
		handlerTable[0x0000|n<<8|0x0<<4|0x8] = opCLRT
		handlerTable[0x0000|n<<8|0x1<<4|0x8] = opSETT
		handlerTable[0x0000|n<<8|0x2<<4|0x8] = opCLRMAC
	}

	// nibble0 = 0x9: NOP/DIV0U/MOVT - tertiary on nibble 1
	for n := 0; n < 16; n++ {
		handlerTable[0x0000|n<<8|0x0<<4|0x9] = opNOP
		handlerTable[0x0000|n<<8|0x1<<4|0x9] = opDIV0U
		handlerTable[0x0000|n<<8|0x2<<4|0x9] = opMOVT
	}

	// nibble0 = 0xA: STS - tertiary on nibble 1
	for n := 0; n < 16; n++ {
		handlerTable[0x0000|n<<8|0x0<<4|0xA] = opSTSMACH
		handlerTable[0x0000|n<<8|0x1<<4|0xA] = opSTSMACL
		handlerTable[0x0000|n<<8|0x2<<4|0xA] = opSTSPR
	}

	// nibble0 = 0xB: RTS/SLEEP/RTE - tertiary on nibble 1
	for n := 0; n < 16; n++ {
		handlerTable[0x0000|n<<8|0x0<<4|0xB] = opRTS
		handlerTable[0x0000|n<<8|0x1<<4|0xB] = opSLEEP
		handlerTable[0x0000|n<<8|0x2<<4|0xB] = opRTE
	}

	// nibble0 = 0xC: MOV.B @(R0,Rm),Rn
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0xC] = opMOVBL0
		}
	}

	// nibble0 = 0xD: MOV.W @(R0,Rm),Rn
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0xD] = opMOVWL0
		}
	}

	// nibble0 = 0xE: MOV.L @(R0,Rm),Rn
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0xE] = opMOVLL0
		}
	}

	// nibble0 = 0xF: MAC.L @Rm+,@Rn+
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x0000|n<<8|m<<4|0xF] = opMACL
		}
	}
}

// registerGroup1 handles opcodes 0x1nnn - all MOV.L Rm,@(disp,Rn).
func registerGroup1() {
	fillRange(0x1000, 4096, opMOVLS4)
}

// registerGroup2 handles opcodes 0x2nnn.
// Secondary dispatch on nibble 0 (bits 3-0).
func registerGroup2() {
	type entry struct {
		low     int
		handler opHandler
	}
	entries := []entry{
		{0x0, opMOVBS},
		{0x1, opMOVWS},
		{0x2, opMOVLS},
		// 0x3 = illegal
		{0x4, opMOVBM},
		{0x5, opMOVWM},
		{0x6, opMOVLM},
		{0x7, opDIV0S},
		{0x8, opTST},
		{0x9, opAND},
		{0xA, opXOR},
		{0xB, opOR},
		{0xC, opCMPSTR},
		{0xD, opXTRCT},
		{0xE, opMULUW},
		{0xF, opMULSW},
	}
	for _, e := range entries {
		for n := 0; n < 16; n++ {
			for m := 0; m < 16; m++ {
				handlerTable[0x2000|n<<8|m<<4|e.low] = e.handler
			}
		}
	}
}

// registerGroup3 handles opcodes 0x3nnn.
// Secondary dispatch on nibble 0 (bits 3-0).
func registerGroup3() {
	type entry struct {
		low     int
		handler opHandler
	}
	entries := []entry{
		{0x0, opCMPEQ},
		// 0x1 = illegal
		{0x2, opCMPHS},
		{0x3, opCMPGE},
		{0x4, opDIV1},
		{0x5, opDMULUL},
		{0x6, opCMPHI},
		{0x7, opCMPGT},
		{0x8, opSUB},
		// 0x9 = illegal
		{0xA, opSUBC},
		{0xB, opSUBV},
		{0xC, opADD},
		{0xD, opDMULSL},
		{0xE, opADDC},
		{0xF, opADDV},
	}
	for _, e := range entries {
		for n := 0; n < 16; n++ {
			for m := 0; m < 16; m++ {
				handlerTable[0x3000|n<<8|m<<4|e.low] = e.handler
			}
		}
	}
}

// registerGroup4 handles opcodes 0x4nnn.
// Secondary dispatch on low byte (bits 7-0).
// MAC.W uses nibble 0 == 0xF with variable nibble 1.
func registerGroup4() {
	type entry struct {
		lowByte int
		handler opHandler
	}
	entries := []entry{
		{0x00, opSHLL},
		{0x01, opSHLR},
		{0x02, opSTSMMACH},
		{0x03, opSTCMSR},
		{0x04, opROTL},
		{0x05, opROTR},
		{0x06, opLDSMMACH},
		{0x07, opLDCMSR},
		{0x08, opSHLL2},
		{0x09, opSHLR2},
		{0x0A, opLDSMACH},
		{0x0B, opJSR},
		{0x0E, opLDCSR},
		{0x10, opDT},
		{0x11, opCMPPZ},
		{0x12, opSTSMMACL},
		{0x13, opSTCMGBR},
		{0x15, opCMPPL},
		{0x16, opLDSMMACL},
		{0x17, opLDCMGBR},
		{0x18, opSHLL8},
		{0x19, opSHLR8},
		{0x1A, opLDSMACL},
		{0x1B, opTAS},
		{0x1E, opLDCGBR},
		{0x20, opSHAL},
		{0x21, opSHAR},
		{0x22, opSTSMPR},
		{0x23, opSTCMVBR},
		{0x24, opROTCL},
		{0x25, opROTCR},
		{0x26, opLDSMPR},
		{0x27, opLDCMVBR},
		{0x28, opSHLL16},
		{0x29, opSHLR16},
		{0x2A, opLDSPR},
		{0x2B, opJMP},
		{0x2E, opLDCVBR},
	}
	for _, e := range entries {
		for n := 0; n < 16; n++ {
			handlerTable[0x4000|n<<8|e.lowByte] = e.handler
		}
	}

	// MAC.W: nibble0=0xF, variable nibble1 (m)
	for n := 0; n < 16; n++ {
		for m := 0; m < 16; m++ {
			handlerTable[0x4000|n<<8|m<<4|0xF] = opMACW
		}
	}
}

// registerGroup5 handles opcodes 0x5nnn - all MOV.L @(disp,Rm),Rn.
func registerGroup5() {
	fillRange(0x5000, 4096, opMOVLL4)
}

// registerGroup6 handles opcodes 0x6nnn.
// Secondary dispatch on nibble 0 (bits 3-0). All 16 values assigned.
func registerGroup6() {
	entries := [16]opHandler{
		opMOVBL, opMOVWL, opMOVLL, opMOV,
		opMOVBP, opMOVWP, opMOVLP, opNOT,
		opSWAPB, opSWAPW, opNEGC, opNEG,
		opEXTUB, opEXTUW, opEXTSB, opEXTSW,
	}
	for low, handler := range entries {
		for n := 0; n < 16; n++ {
			for m := 0; m < 16; m++ {
				handlerTable[0x6000|n<<8|m<<4|low] = handler
			}
		}
	}
}

// registerGroup7 handles opcodes 0x7nnn - all ADD #imm,Rn.
func registerGroup7() {
	fillRange(0x7000, 4096, opADDI)
}

// registerGroup8 handles opcodes 0x8nnn.
// Secondary dispatch on nibble 2 (bits 11-8).
func registerGroup8() {
	type entry struct {
		nibble2 int
		handler opHandler
	}
	entries := []entry{
		{0x0, opMOVBS4},
		{0x1, opMOVWS4},
		{0x4, opMOVBL4},
		{0x5, opMOVWL4},
		{0x8, opCMPIM},
		{0x9, opBT},
		{0xB, opBF},
		{0xD, opBTS},
		{0xF, opBFS},
	}
	for _, e := range entries {
		fillRange(0x8000|e.nibble2<<8, 256, e.handler)
	}
}

// registerGroup9 handles opcodes 0x9nnn - all MOV.W @(disp,PC),Rn.
func registerGroup9() {
	fillRange(0x9000, 4096, opMOVWI)
}

// registerGroupA handles opcodes 0xAnnn - all BRA.
func registerGroupA() {
	fillRange(0xA000, 4096, opBRA)
}

// registerGroupB handles opcodes 0xBnnn - all BSR.
func registerGroupB() {
	fillRange(0xB000, 4096, opBSR)
}

// registerGroupC handles opcodes 0xCnnn.
// Secondary dispatch on nibble 2 (bits 11-8). All 16 values assigned.
func registerGroupC() {
	entries := [16]opHandler{
		opMOVBSG, opMOVWSG, opMOVLSG, opTRAPA,
		opMOVBLG, opMOVWLG, opMOVLLG, opMOVA,
		opTSTI, opANDI, opXORI, opORI,
		opTSTB, opANDB, opXORB, opORB,
	}
	for nibble2, handler := range entries {
		fillRange(0xC000|nibble2<<8, 256, handler)
	}
}

// registerGroupD handles opcodes 0xDnnn - all MOV.L @(disp,PC),Rn.
func registerGroupD() {
	fillRange(0xD000, 4096, opMOVLI)
}

// registerGroupE handles opcodes 0xEnnn - all MOV #imm,Rn.
func registerGroupE() {
	fillRange(0xE000, 4096, opMOVI)
}
