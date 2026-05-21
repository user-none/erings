// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// scuDSP holds all state for the SCU's internal DSP microcode processor.
// The DSP is a 32-bit VLIW processor with 256 words of program RAM,
// 4 banks of 64-word data RAM, a 48-bit accumulator, and a 48-bit
// multiplier. Programs execute synchronously to completion when the
// EX bit is set via the PPAF register.
type scuDSP struct {
	// Program RAM: 256 x 32-bit instructions
	prog [256]uint32
	// Data RAM: 4 banks x 64 x 32-bit words
	data [4][64]uint32

	// Program counter and flow control
	pc  uint8  // 8-bit program counter
	top uint8  // Saved PC for loop/subroutine return
	lop uint16 // 12-bit loop counter (only bits 11:0 used)

	// Data RAM counters (6-bit each, wrap at 0x3F)
	ct [4]uint8

	// Accumulator (48-bit: ACH upper 16, ACL lower 32)
	ach uint16
	acl uint32

	// P register (48-bit: multiplier output)
	ph uint16
	pl uint32

	// Multiplier inputs
	rx uint32
	ry uint32

	// DMA address registers (internal longword-addressed values)
	ra0 uint32 // Read address
	wa0 uint32 // Write address

	// Flags
	flagS   bool // Sign
	flagZ   bool // Zero
	flagC   bool // Carry
	flagV   bool // Overflow (sticky, cleared on PPAF read)
	flagEnd bool // Program ended (sticky, cleared on PPAF read)
	flagT0  bool // DMA active flag

	// Execution state
	executing bool   // EX flag
	looping   bool   // LPS active (suppress prefetch)
	nextInstr uint32 // Prefetched instruction held across Step calls
	debt      uint32 // Cycles a multi-cycle op (DMA) overshot the prior budget

	// Data RAM address for PDA/PDD port access
	// bits 7:6 = bank, bits 5:0 = address
	pdaAddr uint8

	// Parent SCU reference for DMA bus access and interrupt delivery
	scu *SCU
}

// dspDMAAddRAMtoD0 maps the 3-bit add mode to byte increment for RAM-to-D0.
var dspDMAAddRAMtoD0 = [8]uint32{0, 4, 8, 16, 32, 64, 128, 256}

// --- Register Port Methods ---

// readPPAF returns the program control port value (offset 0x80).
// Reading clears V and E flags per hardware specification.
func (d *scuDSP) readPPAF() uint32 {
	var val uint32
	val = uint32(d.pc)
	if d.flagT0 {
		val |= 1 << 23
	}
	if d.flagS {
		val |= 1 << 22
	}
	if d.flagZ {
		val |= 1 << 21
	}
	if d.flagC {
		val |= 1 << 20
	}
	if d.flagV {
		val |= 1 << 19
	}
	if d.flagEnd {
		val |= 1 << 18
	}
	if d.executing {
		val |= 1 << 16
	}
	d.flagV = false
	d.flagEnd = false
	return val
}

// readPPAFInternal returns 0 for byte-write composition purposes.
// PPAF is mostly write-only control bits (LE, ES, EP, PR) and read-only
// status flags (T0, S, Z, C, V, E). Returning 0 ensures byte-write
// composition only sends the bits the host actually wrote, preventing
// the auto-incremented PC from corrupting subsequent EX writes.
func (d *scuDSP) readPPAFInternal() uint32 {
	return 0
}

// writePPAF handles writes to the program control port (offset 0x80).
// Setting EX arms the DSP; stepping happens from SCU.Tick at 1/2 the
// SH-2 rate. A write of EX=1 while already executing is ignored.
func (d *scuDSP) writePPAF(val uint32) {
	// Bit 15: LE (Load Enable) - load PC from bits 7:0
	if val&(1<<15) != 0 {
		d.pc = uint8(val)
	}
	// Bit 16: EX (Execute) - arm program execution
	if val&(1<<16) != 0 && !d.executing {
		d.flagEnd = false
		d.executing = true
		d.debt = 0
		d.nextInstr = d.prog[d.pc]
		d.pc++
	}
}

// writePPD stores an instruction to program RAM at current PC (offset 0x84).
func (d *scuDSP) writePPD(val uint32) {
	if !d.executing {
		d.prog[d.pc] = val
		d.pc++
	}
}

// writePDA sets the data RAM address for PDD access (offset 0x88).
func (d *scuDSP) writePDA(val uint32) {
	d.pdaAddr = uint8(val)
}

// readPDD returns data RAM contents at the PDA-selected address (offset 0x8C).
func (d *scuDSP) readPDD() uint32 {
	bank := (d.pdaAddr >> 6) & 3
	addr := d.pdaAddr & 0x3F
	val := d.data[bank][addr]
	d.pdaAddr++
	return val
}

// writePDD stores data to data RAM at the PDA-selected address (offset 0x8C).
func (d *scuDSP) writePDD(val uint32) {
	if !d.executing {
		bank := (d.pdaAddr >> 6) & 3
		addr := d.pdaAddr & 0x3F
		d.data[bank][addr] = val
		d.pdaAddr++
	}
}

// --- Execution ---

// Step advances DSP execution by up to budget DSP cycles. Returns when
// the budget is exhausted or the program halts. The pre-fetched
// instruction is held in d.nextInstr across calls so the delay-slot
// semantics for JMP/BTM/END are preserved across tick boundaries.
//
// Most instructions cost 1 cycle. DMA costs count cycles; if a DMA
// overshoots the remaining budget the transfer still completes in this
// call (instant data movement), and the overshoot is carried as debt
// that the next Step pays down before fetching new instructions.
func (d *scuDSP) Step(budget uint32) {
	if d.debt >= budget {
		d.debt -= budget
		return
	}
	budget -= d.debt
	d.debt = 0

	var consumed uint32
	for consumed < budget && d.executing {
		instr := d.nextInstr

		if !d.looping || d.lop == 0 {
			d.nextInstr = d.prog[d.pc]
			d.pc++
		}
		if d.looping {
			if d.lop == 0 {
				d.lop = 0x0FFF
				d.looping = false
			} else {
				d.lop = (d.lop - 1) & 0x0FFF
			}
		}

		consumed += d.dispatch(instr)
	}

	if consumed > budget {
		d.debt = consumed - budget
	}
}

// dispatch executes one DSP instruction and returns its cycle cost.
// All instructions cost 1 cycle except DMA, which returns its transfer
// count (see execDMA).
func (d *scuDSP) dispatch(instr uint32) uint32 {
	top2 := instr >> 30
	switch top2 {
	case 0, 1:
		d.execOperation(instr)
	case 2:
		d.execMVI(instr)
	case 3:
		switch (instr >> 28) & 3 {
		case 0:
			return d.execDMA(instr)
		case 1:
			d.execJMP(instr)
		case 2:
			d.execLoop(instr)
		case 3:
			d.execEnd(instr)
		}
	}
	return 1
}

// --- Operation Commands (bits 31:30 = 00) ---

func (d *scuDSP) execOperation(instr uint32) {
	aluOp := (instr >> 26) & 0xF

	aluACH, aluACL := d.ach, d.acl
	d.execALU(aluOp, &aluACH, &aluACL)

	var drRead, ctInc uint8

	// X-Bus
	xSrc := (instr >> 25) & 1
	xPOp := (instr >> 23) & 3
	xRAM := (instr >> 20) & 7

	if xPOp == 2 {
		mul := int64(int32(d.rx)) * int64(int32(d.ry))
		d.ph = uint16(mul >> 32)
		d.pl = uint32(mul)
	}

	var xData uint32
	if xSrc == 1 || xPOp == 3 {
		bank := xRAM & 3
		xData = d.data[bank][d.ct[bank]]
		drRead |= 1 << bank
		if xRAM&4 != 0 {
			ctInc |= 1 << bank
		}
	}
	if xPOp == 3 {
		d.pl = xData
		if int32(xData) < 0 {
			d.ph = 0xFFFF
		} else {
			d.ph = 0
		}
	}
	if xSrc == 1 {
		d.rx = xData
	}

	// Y-Bus
	ySrc := (instr >> 19) & 1
	yAOp := (instr >> 17) & 3
	yRAM := (instr >> 14) & 7

	var yData uint32
	if ySrc == 1 || yAOp == 3 {
		bank := yRAM & 3
		yData = d.data[bank][d.ct[bank]]
		drRead |= 1 << bank
		if yRAM&4 != 0 {
			ctInc |= 1 << bank
		}
	}

	switch yAOp {
	case 1:
		d.ach = 0
		d.acl = 0
	case 2:
		d.ach = aluACH
		d.acl = aluACL
	case 3:
		d.acl = yData
		if int32(yData) < 0 {
			d.ach = 0xFFFF
		} else {
			d.ach = 0
		}
	}
	if ySrc == 1 {
		d.ry = yData
	}

	// D1-Bus
	d1Op := (instr >> 12) & 3
	d.execD1Bus(d1Op, instr, aluACH, aluACL, drRead, &ctInc)

	for i := uint8(0); i < 4; i++ {
		if ctInc&(1<<i) != 0 {
			d.ct[i] = (d.ct[i] + 1) & 0x3F
		}
	}
}

// --- ALU ---

func (d *scuDSP) execALU(op uint32, ach *uint16, acl *uint32) {
	switch op {
	case 0:
		return
	case 1: // AND
		*acl &= d.pl
		d.flagC = false
		d.setZS32(*acl)
	case 2: // OR
		*acl |= d.pl
		d.flagC = false
		d.setZS32(*acl)
	case 3: // XOR
		*acl ^= d.pl
		d.flagC = false
		d.setZS32(*acl)
	case 4: // ADD
		a, b := *acl, d.pl
		result := uint64(a) + uint64(b)
		d.flagC = result>>32 != 0
		d.flagV = d.flagV || ((^(a^b))&(a^uint32(result)))>>31 != 0
		*acl = uint32(result)
		d.setZS32(*acl)
	case 5: // SUB
		a, b := *acl, d.pl
		result := uint64(a) - uint64(b)
		d.flagC = result>>32 != 0
		d.flagV = d.flagV || ((a^b)&(a^uint32(result)))>>31 != 0
		*acl = uint32(result)
		d.setZS32(*acl)
	case 6: // AD2 (48-bit)
		ac := (uint64(*ach) << 32) | uint64(*acl)
		p := (uint64(d.ph) << 32) | uint64(d.pl)
		ac &= 0xFFFFFFFFFFFF
		p &= 0xFFFFFFFFFFFF
		result := ac + p
		d.flagC = result>>48 != 0
		d.flagV = d.flagV || ((^(ac^p))&(ac^result))>>47&1 != 0
		*ach = uint16(result >> 32)
		*acl = uint32(result)
		d.setZS48(result & 0xFFFFFFFFFFFF)
	case 8: // SR
		d.flagC = *acl&1 != 0
		*acl = uint32(int32(*acl) >> 1)
		d.setZS32(*acl)
	case 9: // RR
		bit0 := *acl & 1
		*acl = (*acl >> 1) | (bit0 << 31)
		d.flagC = bit0 != 0
		d.setZS32(*acl)
	case 0xA: // SL
		d.flagC = *acl>>31 != 0
		*acl <<= 1
		d.setZS32(*acl)
	case 0xB: // RL
		bit31 := *acl >> 31
		*acl = (*acl << 1) | bit31
		d.flagC = bit31 != 0
		d.setZS32(*acl)
	case 0xF: // RL8
		d.flagC = (*acl>>24)&1 != 0
		*acl = (*acl << 8) | (*acl >> 24)
		d.setZS32(*acl)
	}
}

func (d *scuDSP) setZS32(val uint32) {
	d.flagZ = val == 0
	d.flagS = int32(val) < 0
}

func (d *scuDSP) setZS48(val uint64) {
	d.flagZ = val == 0
	d.flagS = val>>47&1 != 0
}

// --- D1-Bus ---

func (d *scuDSP) execD1Bus(d1Op uint32, instr uint32,
	aluACH uint16, aluACL uint32, drRead uint8, ctInc *uint8) {
	if d1Op == 0 || d1Op == 2 {
		return
	}

	dst := (instr >> 8) & 0xF
	var val uint32

	if d1Op == 1 {
		val = uint32(int32(int8(instr & 0xFF)))
	} else {
		src := instr & 0xF
		switch {
		case src <= 3:
			val = d.data[src][d.ct[src]]
			drRead |= 1 << src
		case src <= 7:
			bank := src & 3
			val = d.data[bank][d.ct[bank]]
			drRead |= 1 << bank
			if uint32(bank) != dst {
				*ctInc |= 1 << bank
			}
		case src == 9:
			val = aluACL
		case src == 0xA:
			val = (uint32(aluACH) << 16) | (aluACL >> 16)
		default:
			return
		}
	}

	if dst <= 3 {
		if drRead&(1<<dst) != 0 {
			return
		}
		d.data[dst][d.ct[dst]] = val
		*ctInc |= 1 << dst
		return
	}

	d.writeDestReg(dst, val)
}

func (d *scuDSP) writeDestReg(dst uint32, val uint32) {
	switch dst {
	case 4:
		d.rx = val
	case 5:
		d.pl = val
		if int32(val) < 0 {
			d.ph = 0xFFFF
		} else {
			d.ph = 0
		}
	case 6:
		d.ra0 = val
	case 7:
		d.wa0 = val
	case 0xA:
		d.lop = uint16(val) & 0x0FFF
	case 0xB:
		d.top = uint8(val)
	case 0xC:
		d.ct[0] = uint8(val) & 0x3F
	case 0xD:
		d.ct[1] = uint8(val) & 0x3F
	case 0xE:
		d.ct[2] = uint8(val) & 0x3F
	case 0xF:
		d.ct[3] = uint8(val) & 0x3F
	}
}

// --- MVI (bits 31:30 = 10) ---

func (d *scuDSP) execMVI(instr uint32) {
	dest := (instr >> 26) & 0xF

	var imm uint32
	var cond uint32
	if instr&(1<<25) != 0 {
		cond = 0x40 | ((instr >> 19) & 0x3F)
		imm = signExtend(instr&0x7FFFF, 19)
	} else {
		cond = 0
		imm = signExtend(instr&0x1FFFFFF, 25)
	}

	if !d.testCond(cond) {
		return
	}

	if dest == 0xC {
		d.top = d.pc - 1
		d.pc = uint8(imm)
		return
	}

	if dest <= 3 {
		d.data[dest][d.ct[dest]] = imm
		d.ct[dest] = (d.ct[dest] + 1) & 0x3F
		return
	}

	d.writeDestReg(dest, imm)
}

func signExtend(val uint32, bits uint) uint32 {
	shift := 32 - bits
	return uint32(int32(val<<shift) >> shift)
}

// --- Condition Code ---

func (d *scuDSP) testCond(cond uint32) bool {
	if cond&0x40 == 0 {
		return true
	}
	var result bool
	if cond&0x01 != 0 {
		result = result || d.flagZ
	}
	if cond&0x02 != 0 {
		result = result || d.flagS
	}
	if cond&0x04 != 0 {
		result = result || d.flagC
	}
	if cond&0x08 != 0 {
		result = result || d.flagT0
	}
	return result == (cond&0x20 != 0)
}

// --- JMP (bits 31:28 = 1101) ---

func (d *scuDSP) execJMP(instr uint32) {
	cond := (instr >> 19) & 0x7F
	if d.testCond(cond) {
		d.pc = uint8(instr)
	}
}

// --- Loop (bits 31:28 = 1110) ---

func (d *scuDSP) execLoop(instr uint32) {
	if instr&(1<<27) != 0 {
		d.looping = true
	} else {
		if d.lop != 0 {
			d.pc = d.top
		}
		d.lop = (d.lop - 1) & 0x0FFF
	}
}

// --- END (bits 31:28 = 1111) ---

func (d *scuDSP) execEnd(instr uint32) {
	d.executing = false
	if instr&(1<<27) != 0 {
		d.flagEnd = true
		if d.scu != nil {
			d.scu.RaiseInterrupt(5)
		}
	}
}

// --- DMA (bits 31:28 = 1100) ---

// execDMA performs the DMA command. Transfers are executed instantly
// (matching the SCU/SH-2 DMAC pattern). Returns the count of longwords
// transferred as the instruction's DSP cycle cost, so the Step loop can
// account for DSP time spent on the D0 bus. A minimum of 1 cycle is
// charged so bailed-out commands still advance the budget.
func (d *scuDSP) execDMA(instr uint32) uint32 {
	if d.scu == nil || d.scu.bus == nil {
		return 1
	}

	hold := instr&(1<<14) != 0
	format := instr&(1<<13) != 0
	dir := instr&(1<<12) != 0
	addMode := (instr >> 15) & 7
	ramSel := (instr >> 8) & 7

	var count int
	if format {
		src := instr & 7
		bank := src & 3
		count = int(d.data[bank][d.ct[bank]])
		if src&4 != 0 {
			d.ct[bank] = (d.ct[bank] + 1) & 0x3F
		}
	} else {
		count = int(instr & 0xFF)
	}
	if count == 0 {
		count = 256
	}

	if dir {
		addrAdd := dspDMAAddRAMtoD0[addMode]
		addr := (d.wa0 << 2) & 0x07FFFFFF
		bank := ramSel & 3

		for i := 0; i < count; i++ {
			val := d.data[bank][d.ct[bank]]
			d.ct[bank] = (d.ct[bank] + 1) & 0x3F
			d.scu.bus.Write32(addr, val)
			addr += addrAdd
		}
		if !hold {
			d.wa0 = addr >> 2
		}
	} else {
		var addrAdd uint32
		if addMode&2 != 0 {
			addrAdd = 4
		}
		addr := (d.ra0 << 2) & 0x07FFFFFF

		for i := 0; i < count; i++ {
			val := d.scu.bus.Read32(addr)
			addr += addrAdd

			if ramSel&4 != 0 {
				d.prog[d.pc] = val
				d.pc++
			} else {
				bank := ramSel & 3
				d.data[bank][d.ct[bank]] = val
				d.ct[bank] = (d.ct[bank] + 1) & 0x3F
			}
		}
		if !hold {
			d.ra0 = addr >> 2
		}
	}

	d.flagT0 = false
	return uint32(count)
}
