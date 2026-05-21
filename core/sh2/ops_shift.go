// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Shift and rotate instructions: SHAL, SHAR, SHLL, SHLR, SHLL2, SHLR2,
// SHLL8, SHLR8, SHLL16, SHLR16, ROTL, ROTR, ROTCL, ROTCR

// opSHLL shifts Rn left by 1. T receives the bit shifted out (MSB).
func opSHLL(c *CPU) {
	n := regN(c.ir)
	c.reg.SetTVal(c.reg.R[n]>>31 != 0)
	c.reg.R[n] <<= 1
	c.cycles++
}

// opSHLR shifts Rn right by 1 (logical, zero-fill). T receives the bit shifted out (LSB).
func opSHLR(c *CPU) {
	n := regN(c.ir)
	c.reg.SetTVal(c.reg.R[n]&1 != 0)
	c.reg.R[n] >>= 1
	c.cycles++
}

// opSHAL shifts Rn left by 1 (arithmetic). T receives the bit shifted out (MSB).
// Same operation as SHLL.
func opSHAL(c *CPU) {
	n := regN(c.ir)
	c.reg.SetTVal(c.reg.R[n]>>31 != 0)
	c.reg.R[n] <<= 1
	c.cycles++
}

// opSHAR shifts Rn right by 1 (arithmetic, sign-extending). T receives the bit shifted out (LSB).
func opSHAR(c *CPU) {
	n := regN(c.ir)
	c.reg.SetTVal(c.reg.R[n]&1 != 0)
	c.reg.R[n] = uint32(int32(c.reg.R[n]) >> 1)
	c.cycles++
}

// opSHLL2 shifts Rn left by 2. T is unaffected.
func opSHLL2(c *CPU) {
	c.reg.R[regN(c.ir)] <<= 2
	c.cycles++
}

// opSHLR2 shifts Rn right by 2 (logical). T is unaffected.
func opSHLR2(c *CPU) {
	c.reg.R[regN(c.ir)] >>= 2
	c.cycles++
}

// opSHLL8 shifts Rn left by 8. T is unaffected.
func opSHLL8(c *CPU) {
	c.reg.R[regN(c.ir)] <<= 8
	c.cycles++
}

// opSHLR8 shifts Rn right by 8 (logical). T is unaffected.
func opSHLR8(c *CPU) {
	c.reg.R[regN(c.ir)] >>= 8
	c.cycles++
}

// opSHLL16 shifts Rn left by 16. T is unaffected.
func opSHLL16(c *CPU) {
	c.reg.R[regN(c.ir)] <<= 16
	c.cycles++
}

// opSHLR16 shifts Rn right by 16 (logical). T is unaffected.
func opSHLR16(c *CPU) {
	c.reg.R[regN(c.ir)] >>= 16
	c.cycles++
}

// opROTL rotates Rn left by 1. The MSB wraps to LSB and is also stored in T.
func opROTL(c *CPU) {
	n := regN(c.ir)
	msb := c.reg.R[n] >> 31
	c.reg.SetTVal(msb != 0)
	c.reg.R[n] = (c.reg.R[n] << 1) | msb
	c.cycles++
}

// opROTR rotates Rn right by 1. The LSB wraps to MSB and is also stored in T.
func opROTR(c *CPU) {
	n := regN(c.ir)
	lsb := c.reg.R[n] & 1
	c.reg.SetTVal(lsb != 0)
	c.reg.R[n] = (c.reg.R[n] >> 1) | (lsb << 31)
	c.cycles++
}

// opROTCL rotates Rn left by 1 through the T bit. Old T goes to LSB, MSB goes to T.
func opROTCL(c *CPU) {
	n := regN(c.ir)
	old := c.reg.T()
	c.reg.SetTVal(c.reg.R[n]>>31 != 0)
	c.reg.R[n] = (c.reg.R[n] << 1) | old
	c.cycles++
}

// opROTCR rotates Rn right by 1 through the T bit. Old T goes to MSB, LSB goes to T.
func opROTCR(c *CPU) {
	n := regN(c.ir)
	old := c.reg.T()
	c.reg.SetTVal(c.reg.R[n]&1 != 0)
	c.reg.R[n] = (c.reg.R[n] >> 1) | (old << 31)
	c.cycles++
}
