// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Arithmetic instructions: ADD, ADDC, ADDV, SUB, SUBC, SUBV, NEG, NEGC,
// CMP, DIV0U, DIV0S, DIV1, MULS.W, MULU.W, MUL.L, MAC.L, MAC.W,
// DMULS.L, DMULU.L, DT, EXTS, EXTU

// opADD: ADD Rm,Rn - Rn += Rm
func opADD(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] += c.reg.R[m]
	c.cycles++
}

// opADDI: ADD #imm,Rn - Rn += sign-extend(imm8)
func opADDI(c *CPU) {
	n := regN(c.ir)
	c.reg.R[n] += uint32(int32(int8(imm8(c.ir))))
	c.cycles++
}

// opADDC: ADDC Rm,Rn - Rn = Rn + Rm + T; T = carry
func opADDC(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rn := c.reg.R[n]
	rm := c.reg.R[m]
	t := c.reg.T()
	tmp1 := rn + rm
	c0 := tmp1 < rn
	tmp2 := tmp1 + t
	c1 := tmp2 < tmp1
	c.reg.R[n] = tmp2
	c.reg.SetTVal(c0 || c1)
	c.cycles++
}

// opADDV: ADDV Rm,Rn - Rn += Rm; T = signed overflow
func opADDV(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rn := c.reg.R[n]
	rm := c.reg.R[m]
	result := rn + rm
	c.reg.R[n] = result
	// Overflow when operands have same sign but result has different sign
	c.reg.SetTVal(int32(rn^rm) >= 0 && int32(rn^result) < 0)
	c.cycles++
}

// opSUB: SUB Rm,Rn - Rn -= Rm
func opSUB(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] -= c.reg.R[m]
	c.cycles++
}

// opSUBC: SUBC Rm,Rn - Rn = Rn - Rm - T; T = borrow
func opSUBC(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rn := c.reg.R[n]
	rm := c.reg.R[m]
	t := c.reg.T()
	tmp1 := rn - rm
	b0 := rn < rm
	tmp2 := tmp1 - t
	b1 := tmp1 < t
	c.reg.R[n] = tmp2
	c.reg.SetTVal(b0 || b1)
	c.cycles++
}

// opSUBV: SUBV Rm,Rn - Rn -= Rm; T = signed overflow
func opSUBV(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rn := c.reg.R[n]
	rm := c.reg.R[m]
	result := rn - rm
	c.reg.R[n] = result
	// Overflow when operands have different sign and result sign differs from Rn
	c.reg.SetTVal(int32(rn^rm) < 0 && int32(rn^result) < 0)
	c.cycles++
}

// opNEG: NEG Rm,Rn - Rn = 0 - Rm
func opNEG(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = 0 - c.reg.R[m]
	c.cycles++
}

// opNEGC: NEGC Rm,Rn - Rn = 0 - Rm - T; T = borrow
func opNEGC(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rm := c.reg.R[m]
	t := c.reg.T()
	tmp := uint32(0) - rm
	b0 := rm != 0
	result := tmp - t
	b1 := tmp < t
	c.reg.R[n] = result
	c.reg.SetTVal(b0 || b1)
	c.cycles++
}

// opCMPEQ: CMP/EQ Rm,Rn - T = (Rn == Rm)
func opCMPEQ(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.SetTVal(c.reg.R[n] == c.reg.R[m])
	c.cycles++
}

// opCMPGE: CMP/GE Rm,Rn - T = (int32(Rn) >= int32(Rm))
func opCMPGE(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.SetTVal(int32(c.reg.R[n]) >= int32(c.reg.R[m]))
	c.cycles++
}

// opCMPGT: CMP/GT Rm,Rn - T = (int32(Rn) > int32(Rm))
func opCMPGT(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.SetTVal(int32(c.reg.R[n]) > int32(c.reg.R[m]))
	c.cycles++
}

// opCMPHI: CMP/HI Rm,Rn - T = (Rn > Rm) unsigned
func opCMPHI(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.SetTVal(c.reg.R[n] > c.reg.R[m])
	c.cycles++
}

// opCMPHS: CMP/HS Rm,Rn - T = (Rn >= Rm) unsigned
func opCMPHS(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.SetTVal(c.reg.R[n] >= c.reg.R[m])
	c.cycles++
}

// opCMPPL: CMP/PL Rn - T = (int32(Rn) > 0)
func opCMPPL(c *CPU) {
	n := regN(c.ir)
	c.reg.SetTVal(int32(c.reg.R[n]) > 0)
	c.cycles++
}

// opCMPPZ: CMP/PZ Rn - T = (int32(Rn) >= 0)
func opCMPPZ(c *CPU) {
	n := regN(c.ir)
	c.reg.SetTVal(int32(c.reg.R[n]) >= 0)
	c.cycles++
}

// opCMPSTR: CMP/STR Rm,Rn - T = 1 if any byte of Rn^Rm is zero
func opCMPSTR(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	xor := c.reg.R[n] ^ c.reg.R[m]
	c.reg.SetTVal(
		(xor&0xFF000000) == 0 ||
			(xor&0x00FF0000) == 0 ||
			(xor&0x0000FF00) == 0 ||
			(xor&0x000000FF) == 0)
	c.cycles++
}

// opCMPIM: CMP/EQ #imm,R0 - T = (R0 == sign-extend(imm8))
func opCMPIM(c *CPU) {
	c.reg.SetTVal(c.reg.R[0] == uint32(int32(int8(imm8(c.ir)))))
	c.cycles++
}

// opDIV0U: DIV0U - M=0, Q=0, T=0
func opDIV0U(c *CPU) {
	c.reg.SR &^= srQMask | srMMask | srTMask
	c.cycles++
}

// opDIV0S: DIV0S Rm,Rn - Q=MSB(Rn), M=MSB(Rm), T=(Q!=M)
func opDIV0S(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	// Q = MSB of Rn
	if c.reg.R[n]&0x80000000 != 0 {
		c.reg.SR |= srQMask
	} else {
		c.reg.SR &^= srQMask
	}
	// M = MSB of Rm
	if c.reg.R[m]&0x80000000 != 0 {
		c.reg.SR |= srMMask
	} else {
		c.reg.SR &^= srMMask
	}
	// T = (Q != M)
	q := (c.reg.SR & srQMask) != 0
	mVal := (c.reg.SR & srMMask) != 0
	c.reg.SetTVal(q != mVal)
	c.cycles++
}

// opDIV1: DIV1 Rm,Rn - single-step division
// Algorithm from SH-1/SH-2 Programming Manual section 6.1.19
func opDIV1(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	rn := c.reg.R[n]
	rm := c.reg.R[m]

	oldQ := (c.reg.SR & srQMask) != 0
	mBit := (c.reg.SR & srMMask) != 0

	// Q = MSB(Rn)
	q := rn >> 31
	// Rn = (Rn << 1) | T
	rn = (rn << 1) | c.reg.T()

	var tmp1 uint32
	switch {
	case !oldQ && !mBit:
		// oldQ=0, M=0: subtract
		tmp0 := rn
		rn -= rm
		if rn > tmp0 {
			tmp1 = 1
		}
		if q == 0 {
			q = tmp1
		} else {
			q = tmp1 ^ 1
		}
	case !oldQ && mBit:
		// oldQ=0, M=1: add
		tmp0 := rn
		rn += rm
		if rn < tmp0 {
			tmp1 = 1
		}
		if q == 0 {
			q = tmp1 ^ 1
		} else {
			q = tmp1
		}
	case oldQ && !mBit:
		// oldQ=1, M=0: add
		tmp0 := rn
		rn += rm
		if rn < tmp0 {
			tmp1 = 1
		}
		if q == 0 {
			q = tmp1
		} else {
			q = tmp1 ^ 1
		}
	case oldQ && mBit:
		// oldQ=1, M=1: subtract
		tmp0 := rn
		rn -= rm
		if rn > tmp0 {
			tmp1 = 1
		}
		if q == 0 {
			q = tmp1 ^ 1
		} else {
			q = tmp1
		}
	}

	c.reg.R[n] = rn
	if q != 0 {
		c.reg.SR |= srQMask
	} else {
		c.reg.SR &^= srQMask
	}
	// T = (Q == M)
	c.reg.SetTVal((q != 0) == mBit)
	c.cycles++
}

// opMULL: MUL.L Rm,Rn - MACL = Rn * Rm (low 32 bits).
// Baseline 2 cycles (EX + MA). mm runs 4 cycles after MA; if the next
// multiplier instruction begins before mm completes, that instruction
// stalls. Programming Manual Section 7.2.3, Table 7.1 (2-4 cycles).
func opMULL(c *CPU) {
	extraStall := uint8(0)
	if c.cycles < c.multiplierBusyUntil {
		delta := c.multiplierBusyUntil - c.cycles
		if delta > 2 {
			extraStall = uint8(delta - 2)
		}
	}
	if extraStall > 2 { // Table 7.1 max: MUL.L 2 to 4
		extraStall = 2
	}
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.MACL = c.reg.R[n] * c.reg.R[m]
	c.cycles++
	c.setPending(popStall, 1+extraStall)
	c.multiplierBusyUntil = c.cycles + 5
}

// opMULSW: MULS.W Rm,Rn - MACL = int16(Rn) * int16(Rm) (signed).
// Baseline 1 cycle. mm runs 2 cycles after MA; if the next multiplier
// instruction begins before mm completes, that instruction stalls.
// Programming Manual Section 7.2.3, Table 7.1 (1-3 cycles).
func opMULSW(c *CPU) {
	extraStall := uint8(0)
	if c.cycles < c.multiplierBusyUntil {
		delta := c.multiplierBusyUntil - c.cycles
		if delta > 1 {
			extraStall = uint8(delta - 1)
		}
	}
	if extraStall > 2 { // Table 7.1 max: MULS.W 1 to 3
		extraStall = 2
	}
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.MACL = uint32(int32(int16(c.reg.R[n])) * int32(int16(c.reg.R[m])))
	c.cycles++
	if extraStall > 0 {
		c.setPending(popStall, extraStall)
	}
	c.multiplierBusyUntil = c.cycles + 3
}

// opMULUW: MULU.W Rm,Rn - MACL = uint16(Rn) * uint16(Rm) (unsigned).
// Baseline 1 cycle. Same contention contract as opMULSW.
// Programming Manual Section 7.2.3, Table 7.1 (1-3 cycles).
func opMULUW(c *CPU) {
	extraStall := uint8(0)
	if c.cycles < c.multiplierBusyUntil {
		delta := c.multiplierBusyUntil - c.cycles
		if delta > 1 {
			extraStall = uint8(delta - 1)
		}
	}
	if extraStall > 2 { // Table 7.1 max: MULU.W 1 to 3
		extraStall = 2
	}
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.MACL = uint32(uint16(c.reg.R[n])) * uint32(uint16(c.reg.R[m]))
	c.cycles++
	if extraStall > 0 {
		c.setPending(popStall, extraStall)
	}
	c.multiplierBusyUntil = c.cycles + 3
}

// opDMULSL: DMULS.L Rm,Rn - MACH:MACL = int32(Rn) * int32(Rm) (signed).
// Baseline 2 cycles. Same contention contract as opMULL.
// Programming Manual Section 7.2.3, Table 7.1 (2-4 cycles).
func opDMULSL(c *CPU) {
	extraStall := uint8(0)
	if c.cycles < c.multiplierBusyUntil {
		delta := c.multiplierBusyUntil - c.cycles
		if delta > 2 {
			extraStall = uint8(delta - 2)
		}
	}
	if extraStall > 2 { // Table 7.1 max: DMULS.L 2 to 4
		extraStall = 2
	}
	n := regN(c.ir)
	m := regM(c.ir)
	result := int64(int32(c.reg.R[n])) * int64(int32(c.reg.R[m]))
	c.reg.MACL = uint32(result)
	c.reg.MACH = uint32(result >> 32)
	c.cycles++
	c.setPending(popStall, 1+extraStall)
	c.multiplierBusyUntil = c.cycles + 5
}

// opDMULUL: DMULU.L Rm,Rn - MACH:MACL = uint32(Rn) * uint32(Rm) (unsigned).
// Baseline 2 cycles. Same contention contract as opMULL.
// Programming Manual Section 7.2.3, Table 7.1 (2-4 cycles).
func opDMULUL(c *CPU) {
	extraStall := uint8(0)
	if c.cycles < c.multiplierBusyUntil {
		delta := c.multiplierBusyUntil - c.cycles
		if delta > 2 {
			extraStall = uint8(delta - 2)
		}
	}
	if extraStall > 2 { // Table 7.1 max: DMULU.L 2 to 4
		extraStall = 2
	}
	n := regN(c.ir)
	m := regM(c.ir)
	result := uint64(c.reg.R[n]) * uint64(c.reg.R[m])
	c.reg.MACL = uint32(result)
	c.reg.MACH = uint32(result >> 32)
	c.cycles++
	c.setPending(popStall, 1+extraStall)
	c.multiplierBusyUntil = c.cycles + 5
}

// opMACL: MAC.L @Rm+,@Rn+ - multiply-accumulate long.
// Baseline 2 cycles. mm runs 4 cycles after the second MA (set in
// stepMACL). If the multiplier is still busy from a preceding MUL/MAC,
// MAC.L's pending-op count is extended so stepMACL idles for the
// required stall cycles before running the real second-MA body.
// Programming Manual Section 7.2.3, Table 7.1 (2-4 cycles).
// Cycle 1: EX+MA read @Rn, post-increment Rn
// Cycle 2 (final step): MA read @Rm, post-increment Rm, accumulate
func opMACL(c *CPU) {
	extraStall := uint8(0)
	if c.cycles < c.multiplierBusyUntil {
		delta := c.multiplierBusyUntil - c.cycles
		if delta > 2 {
			extraStall = uint8(delta - 2)
		}
	}
	if extraStall > 2 { // Table 7.1 max: MAC.L 2 to 4
		extraStall = 2
	}
	n := regN(c.ir)
	m := regM(c.ir)

	// Cycle 1: read @Rn, post-increment
	c.pendingVal = uint32(c.read32(c.reg.R[n]))
	c.reg.R[n] += 4
	c.pendingN = n | (m << 4)
	c.stepBus = BusRead
	c.cycles++
	c.setPending(popMACL, 1+extraStall)
}

// opMACW: MAC.W @Rm+,@Rn+ - multiply-accumulate word.
// Baseline 2 cycles. mm runs 2 cycles after the second MA (set in
// stepMACW). Same contention contract as opMACL.
// Programming Manual Section 7.2.3, Table 7.1 (2-3 cycles).
// Cycle 1: EX+MA read @Rn, post-increment Rn
// Cycle 2 (final step): MA read @Rm, post-increment Rm, accumulate
func opMACW(c *CPU) {
	extraStall := uint8(0)
	if c.cycles < c.multiplierBusyUntil {
		delta := c.multiplierBusyUntil - c.cycles
		if delta > 2 {
			extraStall = uint8(delta - 2)
		}
	}
	if extraStall > 1 { // Table 7.1 max: MAC.W 2 to 3 (only 1 extra allowed)
		extraStall = 1
	}
	n := regN(c.ir)
	m := regM(c.ir)

	// Cycle 1: read @Rn, post-increment
	c.pendingVal = uint32(c.read16(c.reg.R[n]))
	c.reg.R[n] += 2
	c.pendingN = n | (m << 4)
	c.stepBus = BusRead
	c.cycles++
	c.setPending(popMACW, 1+extraStall)
}

// opDT: DT Rn - Rn--; T = (Rn == 0) ? 1 : 0
func opDT(c *CPU) {
	n := regN(c.ir)
	c.reg.R[n]--
	c.reg.SetTVal(c.reg.R[n] == 0)
	c.cycles++
}

// opEXTSB: EXTS.B Rm,Rn - sign-extend byte
func opEXTSB(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int8(c.reg.R[m])))
	c.cycles++
}

// opEXTSW: EXTS.W Rm,Rn - sign-extend word
func opEXTSW(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = uint32(int32(int16(c.reg.R[m])))
	c.cycles++
}

// opEXTUB: EXTU.B Rm,Rn - zero-extend byte
func opEXTUB(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = c.reg.R[m] & 0xFF
	c.cycles++
}

// opEXTUW: EXTU.W Rm,Rn - zero-extend word
func opEXTUW(c *CPU) {
	n := regN(c.ir)
	m := regM(c.ir)
	c.reg.R[n] = c.reg.R[m] & 0xFFFF
	c.cycles++
}
