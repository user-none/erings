// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// DSP register base offsets (byte offsets from SCSP register base 0x100000)
const (
	dspRegCOEF  = 0x700 // 64 x 16-bit coefficients
	dspRegMADRS = 0x780 // 32 x 16-bit address registers
	dspRegMPRO  = 0x800 // 128 steps x 4 words = 512 words
	dspRegTEMP  = 0xC00 // 128 x 2 words (24-bit values)
	dspRegMEMS  = 0xE00 // 32 x 2 words (24-bit values)
	dspRegMIXS  = 0xE80 // 16 x 2 words (20-bit values)
	dspRegEFREG = 0xEC0 // 16 x 16-bit effect outputs
)

// scspDSP holds all state for the SCSP internal DSP effects processor.
type scspDSP struct {
	mpro  [128]uint64 // 128 x 64-bit microprogram instructions
	temp  [128]int32  // 24-bit work buffer (ring buffer)
	mems  [32]int32   // 24-bit sound memory data
	mixs  [16]int32   // 20-bit mixer input from slots
	exts  [2]int32    // 16-bit external audio input (EXTS[0]=L, EXTS[1]=R), per SCSP User's Manual Sec 5.2
	efreg [16]int16   // 16-bit effect output registers
	coef  [64]int16   // 13-bit coefficients (stored in 16-bit, use >> 3)
	madrs [32]uint16  // 16-bit memory address registers

	sftReg  int32  // 26-bit shift register / accumulator (persists across runs)
	frcReg  uint16 // 13-bit fraction register
	yReg    int32  // 24-bit Y bus latch
	adrsReg uint16 // 12-bit address register
	mdecCT  uint16 // Ring buffer decay counter
	inputs  int32  // 24-bit INPUTS latch (persists across steps)

	// Memory access pipeline (2-step delay per hardware timing)
	readPending  uint8  // 0=none, 1=pending float, 2=pending raw (NOFL=1)
	readValue    int32  // result of completed read, available to IWT
	writePending bool   // true=write pending from previous step
	writeValue   uint16 // value to write
	rwAddr       uint32 // word address for pending read/write

	lastStep int // Last non-NOP step (optimization)
}

// dspFloatToInt converts a 16-bit DSP float to a 24-bit signed integer.
//
// Format: bit 15 = sign, bits 14:11 = exponent (0-15), bits 10:0 = mantissa.
// For exponent < 12 there is an implicit leading 1 above the mantissa,
// forming a 12-bit significand. The exponent controls the right shift.
// Sign is applied as two's complement via the upper bits.
func dspFloatToInt(val uint16) int32 {
	sign := val >> 15
	exp := (val >> 11) & 0xF
	mantissa := uint32(val & 0x7FF)

	// Build 24-bit signed value: [sign][implicit_or_sign][mantissa<<11][zeros]
	// Bit 23 = sign, bit 22 = implicit 1 (when exp<12) or sign (when exp>=12)
	var raw uint32
	if exp > 11 {
		exp = 11
		raw = (uint32(sign) << 23) | (uint32(sign) << 22) | (mantissa << 11)
	} else {
		raw = (uint32(sign) << 23) | (uint32(sign^1) << 22) | (mantissa << 11)
	}

	// Sign-extend to 32-bit then arithmetic right shift by exponent
	result := int32(raw<<8) >> 8 // sign-extend 24-bit to 32-bit
	result >>= exp

	return result & 0xFFFFFF
}

// intToDSPFloat converts a 24-bit signed integer to a 16-bit DSP float.
//
// Finds the exponent by counting leading redundant sign bits in the
// 24-bit value (shifted left 8 to align to bit 31), then extracts the
// 11-bit mantissa from the appropriate position.
func intToDSPFloat(val int32) uint16 {
	// Work in 32-bit with the 24-bit value left-justified at bit 31
	v := uint32(val) << 8

	// Get sign extension mask (all 1s if negative, all 0s if positive)
	signMask := uint32(int32(v) >> 31)

	// XOR with sign to get absolute-ish value, shift left 1 to skip sign bit,
	// then set a sentinel bit to bound the leading-zero count
	abs := ((v ^ signMask) << 1) | (1 << 19)

	// Count leading zeros to find exponent
	exp := uint32(0)
	for bit := uint32(31); bit > 0; bit-- {
		if abs&(1<<bit) != 0 {
			exp = 31 - bit
			break
		}
	}

	// Adjust shift for exponent 12 boundary
	shift := exp
	if exp == 12 {
		shift--
	}

	// Extract mantissa via arithmetic right shift from the original value
	ret := uint32(int32(v) >> (19 - shift))
	ret &= 0x87FF    // Keep sign (bit 15) and mantissa (bits 10:0)
	ret |= exp << 11 // Insert exponent

	return uint16(ret)
}

// signExtend24to32 sign-extends a 24-bit value to int32.
func signExtend24to32(v int32) int32 {
	if v&0x800000 != 0 {
		return v | ^int32(0xFFFFFF)
	}
	return v & 0xFFFFFF
}

// signExtend13 sign-extends a 13-bit value to int32.
func signExtend13(v int32) int32 {
	if v&0x1000 != 0 {
		return v | ^int32(0x1FFF)
	}
	return v & 0x1FFF
}

// signExtend12 sign-extends a 12-bit value to int32.
func signExtend12(v uint16) int32 {
	if v&0x800 != 0 {
		return int32(v) | ^int32(0xFFF)
	}
	return int32(v & 0xFFF)
}

// saturate24 clamps a value to 24-bit signed range.
func saturate24(v int32) int32 {
	if v > 0x7FFFFF {
		return 0x7FFFFF
	}
	if v < -0x800000 {
		return -0x800000
	}
	return v
}

// readDSPReg returns the live DSP state for CPU reads of the DSP
// register area (0x700-0xEE3). The DSP updates TEMP, MEMS, and EFREG
// internally without syncing to the main register file, so CPU reads
// must come from the DSP state directly.
func (s *SCSP) readDSPReg(offset uint32) uint16 {
	switch {
	case offset >= dspRegTEMP && offset < dspRegMEMS:
		relOff := offset - dspRegTEMP
		idx := relOff / 4
		isLow := (relOff%4)/2 == 1
		if idx < 128 {
			if isLow {
				return uint16(s.dsp.temp[idx] & 0xFF)
			}
			return uint16(s.dsp.temp[idx] >> 8)
		}
	case offset >= dspRegMEMS && offset < dspRegMIXS:
		relOff := offset - dspRegMEMS
		idx := relOff / 4
		isLow := (relOff%4)/2 == 1
		if idx < 32 {
			if isLow {
				return uint16(s.dsp.mems[idx] & 0xFF)
			}
			return uint16(s.dsp.mems[idx] >> 8)
		}
	case offset >= dspRegMIXS && offset < dspRegEFREG:
		relOff := offset - dspRegMIXS
		idx := relOff / 4
		isLow := (relOff%4)/2 == 1
		if idx < 16 {
			if isLow {
				return uint16(s.dsp.mixs[idx] & 0xF)
			}
			return uint16(s.dsp.mixs[idx] >> 4)
		}
	case offset >= dspRegEFREG && offset < dspRegEFREG+32:
		idx := (offset - dspRegEFREG) / 2
		if idx < 16 {
			return uint16(s.dsp.efreg[idx])
		}
	case offset >= dspRegEFREG+32 && offset < scspRegSize:
		// EXTS registers at 0xEE0-0xEE3 (2 x 16-bit). The PDF says
		// these "cannot be accessed" but the BIOS M68K sound driver
		// reads them at 0x100EE0/0x100EE2 for VU meter computation.
		idx := (offset - (dspRegEFREG + 32)) / 2
		if idx < 2 {
			return uint16(s.dsp.exts[idx])
		}
	}
	return s.regs[offset/2]
}

// syncDSPRegWrite handles writes to DSP register areas, copying data
// into the typed DSP struct fields.
func (s *SCSP) syncDSPRegWrite(offset uint32, val uint16) {
	switch {
	case offset >= dspRegCOEF && offset < dspRegMADRS:
		idx := (offset - dspRegCOEF) / 2
		if idx < 64 {
			s.dsp.coef[idx] = int16(val)
		}
	case offset >= dspRegMADRS && offset < dspRegMPRO:
		idx := (offset - dspRegMADRS) / 2
		if idx < 32 {
			s.dsp.madrs[idx] = val
		}
	case offset >= dspRegMPRO && offset < dspRegTEMP:
		// MPRO: 128 steps x 4 words. Each step = 8 bytes.
		relOff := offset - dspRegMPRO
		step := relOff / 8
		wordIdx := (relOff % 8) / 2 // 0-3 within step
		if step < 128 {
			// Word 0 = bits 63:48, word 1 = 47:32, word 2 = 31:16, word 3 = 15:0
			shift := uint((3 - wordIdx) * 16)
			mask := uint64(0xFFFF) << shift
			s.dsp.mpro[step] = (s.dsp.mpro[step] &^ mask) | (uint64(val) << shift)
			s.recalcDSPLastStep()
		}
	case offset >= dspRegTEMP && offset < dspRegMEMS:
		// TEMP: 128 entries x 2 words (high then low)
		relOff := offset - dspRegTEMP
		idx := relOff / 4
		isLow := (relOff%4)/2 == 1
		if idx < 128 {
			if isLow {
				s.dsp.temp[idx] = (s.dsp.temp[idx] & ^int32(0xFF)) | int32(val&0xFF)
			} else {
				s.dsp.temp[idx] = (s.dsp.temp[idx] & 0xFF) | (int32(val) << 8)
			}
		}
	case offset >= dspRegMEMS && offset < dspRegMIXS:
		relOff := offset - dspRegMEMS
		idx := relOff / 4
		isLow := (relOff%4)/2 == 1
		if idx < 32 {
			if isLow {
				s.dsp.mems[idx] = (s.dsp.mems[idx] & ^int32(0xFF)) | int32(val&0xFF)
			} else {
				s.dsp.mems[idx] = (s.dsp.mems[idx] & 0xFF) | (int32(val) << 8)
			}
		}
	case offset >= dspRegEFREG && offset < scspRegSize:
		idx := (offset - dspRegEFREG) / 2
		if idx < 16 {
			s.dsp.efreg[idx] = int16(val)
		}
	}
}

// recalcDSPLastStep finds the last non-NOP instruction in MPRO.
func (s *SCSP) recalcDSPLastStep() {
	s.dsp.lastStep = 0
	for i := 127; i >= 0; i-- {
		if s.dsp.mpro[i] != 0 {
			s.dsp.lastStep = i + 1
			return
		}
	}
}

// runDSP executes the 128-step DSP microprogram for one sample tick.
//
// Pipeline order per step with 2-step memory
// delay: MRD at step N sets readPending, read completes at step N+1
// (updating readValue), IWT at step N+2 can use that readValue.
//
//  1. Load INPUTS latch from MEMS/MIXS/EXTS via IRA
//  2. Select operands (B from TEMP/SFT_REG, X from TEMP/INPUTS, Y from COEF/FRC/Y_REG)
//  3. YRL: latch Y_REG from INPUTS
//  4. SHIFTED = shift(SFT_REG) -- previous step's accumulator
//  5. FRCL: latch FRC_REG from SHIFTED
//  6. Multiply-accumulate: SFT_REG = (X * Y) >> 12 + B
//  7. EWT: EFREG[EWA] = SHIFTED >> 8
//  8. TWT: TEMP[TWA+MDEC_CT] = SHIFTED
//  9. IWT: MEMS[IWA] = readValue (from completed read)
//
// 10. Complete pending memory read/write from previous step
// 11. Calculate address, start new MRD/MWT pending
// 12. ADRL: latch ADRS_REG from SHIFTED or INPUTS
func (s *SCSP) runDSP() {
	if s.dsp.lastStep == 0 {
		return // No program loaded
	}

	// Read RBL/RBP from common control register 0x402 per SCSP User's
	// Manual 4.3: RBL at bits 8:7 (2 bits), RBP at bits 6:0 (7 bits,
	// per 4K-word boundary).
	rbReg := s.regs[(scspRegMVOL+2)/2]
	rbl := (rbReg >> 7) & 3
	rbp := uint32(rbReg & 0x7F)
	rbMask := uint32((0x2000 << rbl) - 1)
	rbBase := rbp << 12

	for step := 0; step < s.dsp.lastStep; step++ {
		instr := s.dsp.mpro[step]

		// Decode instruction fields
		tra := int((instr >> 56) & 0x7F)
		twt := (instr>>55)&1 != 0
		twa := int((instr >> 48) & 0x7F)
		xsel := (instr>>47)&1 != 0
		ysel := (instr >> 45) & 3
		ira := int((instr >> 38) & 0x3F)
		iwt := (instr>>37)&1 != 0
		iwa := int((instr >> 32) & 0x1F)
		table := (instr>>31)&1 != 0
		mwt := (instr>>30)&1 != 0
		mrd := (instr>>29)&1 != 0
		ewt := (instr>>28)&1 != 0
		ewa := int((instr >> 24) & 0xF)
		adrl := (instr>>23)&1 != 0
		frcl := (instr>>22)&1 != 0
		shft := (instr >> 20) & 3
		yrl := (instr>>19)&1 != 0
		negb := (instr>>18)&1 != 0
		zero := (instr>>17)&1 != 0
		bsel := (instr>>16)&1 != 0
		nofl := (instr>>15)&1 != 0
		cra := int((instr >> 9) & 0x3F)
		masa := int((instr >> 2) & 0x1F)
		adreb := (instr>>1)&1 != 0
		nxadr := (instr>>0)&1 != 0

		// -- 1. Load INPUTS latch from MEMS, MIXS, or EXTS --
		// IRA selects from three input sources. Reserved addresses
		// (0x32-0x3F) leave the INPUTS latch unchanged.
		switch {
		case ira < 0x20:
			s.dsp.inputs = s.dsp.mems[ira&0x1F]
		case ira < 0x30:
			s.dsp.inputs = s.dsp.mixs[ira&0xF] << 4
		case ira < 0x32:
			s.dsp.inputs = s.dsp.exts[ira&0x1] << 8
		}
		inputs := signExtend24to32(s.dsp.inputs)

		// -- 2. Select operands --
		tempAddr := (tra + int(s.dsp.mdecCT)) & 0x7F
		tempVal := signExtend24to32(s.dsp.temp[tempAddr])

		var bOp int32
		if !zero {
			if bsel {
				bOp = s.dsp.sftReg
			} else {
				bOp = tempVal
			}
			if negb {
				bOp = -bOp
			}
		}

		var xOp int32
		if xsel {
			xOp = inputs
		} else {
			xOp = tempVal
		}

		var yOp int32
		switch ysel {
		case 0:
			yOp = int32(s.dsp.frcReg)
		case 1:
			if cra < 64 {
				yOp = int32(s.dsp.coef[cra]) >> 3
			}
		case 2:
			yOp = (s.dsp.yReg >> 11) & 0x1FFF
		case 3:
			yOp = (s.dsp.yReg >> 4) & 0x0FFF
		}

		// -- 3. YRL: latch Y_REG from INPUTS --
		if yrl {
			s.dsp.yReg = inputs & 0xFFFFFF
		}

		// -- 4. SHIFTED from previous step's SFT_REG --
		var shifted int32
		switch shft {
		case 0: // x1, saturate
			shifted = saturate24(s.dsp.sftReg)
		case 1: // x2, saturate
			shifted = saturate24(s.dsp.sftReg << 1)
		case 2: // x2, no saturation
			shifted = (s.dsp.sftReg << 1) & 0xFFFFFF
		case 3: // x1, no saturation
			shifted = s.dsp.sftReg & 0xFFFFFF
		}

		// -- 5. FRCL: latch FRC_REG from SHIFTED --
		if frcl {
			if shft == 3 {
				s.dsp.frcReg = uint16(shifted) & 0x0FFF
			} else {
				s.dsp.frcReg = uint16((shifted >> 11) & 0x1FFF)
			}
		}

		// -- 6. Multiply-accumulate -> new SFT_REG --
		yOp = signExtend13(yOp)
		product := (int64(xOp) * int64(yOp)) >> 12
		s.dsp.sftReg = int32(int64(product)+int64(bOp)) & 0x3FFFFFF
		if s.dsp.sftReg&0x2000000 != 0 {
			s.dsp.sftReg |= ^int32(0x3FFFFFF)
		}

		// -- 7. EWT: write SHIFTED to EFREG --
		if ewt && ewa < 16 {
			s.dsp.efreg[ewa] = int16(shifted >> 8)
		}

		// -- 8. TWT: write SHIFTED to TEMP --
		if twt {
			writeAddr := (twa + int(s.dsp.mdecCT)) & 0x7F
			s.dsp.temp[writeAddr] = shifted & 0xFFFFFF
		}

		// -- 9. IWT: write completed read result to MEMS --
		// Uses readValue from a previously completed read (2-step
		// pipeline: MRD at step N, read completes at N+1, IWT at
		// N+2 sees the result).
		if iwt && iwa < 32 {
			s.dsp.mems[iwa] = s.dsp.readValue
		}

		// -- 10. Complete pending memory operations --
		if s.dsp.readPending != 0 {
			raw := s.ReadRAM16(s.dsp.rwAddr * 2)
			if s.dsp.readPending == 2 {
				s.dsp.readValue = int32(int16(raw)) << 8
			} else {
				s.dsp.readValue = dspFloatToInt(raw)
			}
			s.dsp.readPending = 0
		} else if s.dsp.writePending {
			if s.dsp.rwAddr&0x40000 == 0 {
				s.WriteRAM16(s.dsp.rwAddr*2, s.dsp.writeValue)
			}
			s.dsp.writePending = false
		}

		// -- 11. Address calculation and new MRD/MWT request --
		{
			addr := uint32(s.dsp.madrs[masa&0x1F])
			if nxadr {
				addr++
			}
			if adreb {
				addr += uint32(signExtend12(s.dsp.adrsReg))
			}
			if !table {
				addr += uint32(s.dsp.mdecCT)
				addr &= rbMask
			}
			addr = (addr + rbBase) & 0x7FFFF
			s.dsp.rwAddr = addr

			if mrd {
				s.dsp.readPending = 1
				if nofl {
					s.dsp.readPending = 2
				}
			}
			if mwt {
				s.dsp.writePending = true
				if nofl {
					s.dsp.writeValue = uint16(shifted >> 8)
				} else {
					s.dsp.writeValue = intToDSPFloat(shifted)
				}
			}
		}

		// -- 12. ADRL: latch ADRS_REG --
		if adrl {
			if shft == 3 {
				s.dsp.adrsReg = uint16((shifted >> 12) & 0xFFF)
			} else {
				s.dsp.adrsReg = uint16((inputs >> 16) & 0xFFF)
			}
		}
	}

	// Update MDEC_CT at end of DSP cycle
	if s.dsp.mdecCT == 0 {
		s.dsp.mdecCT = uint16(0x2000 << rbl)
	}
	s.dsp.mdecCT--
}
