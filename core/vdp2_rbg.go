// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// rotParams holds decoded rotation parameter table values.
type rotParams struct {
	xst, yst, zst    int64 // .10 FP
	dxst, dyst       int64 // .10 FP
	dx, dy           int64 // .10 FP
	a, b, c, d, e, f int64 // .10 FP
	px, py, pz       int64 // integer
	cx, cy, cz       int64 // integer
	mx, my           int64 // .10 FP
	kx, ky           int64 // .16 FP
	kast             int64 // .10 FP (unsigned)
	dkast, dkax      int64 // .10 FP
}

// rbgConfig holds decoded configuration for RBG0.
type rbgConfig struct {
	enabled       bool
	transpOff     bool
	colorMode     uint8 // 0=16color,1=256,2=2048,3=32768
	charSize1x1   bool
	pnWord1       bool
	auxMode1      bool
	bitmapMode    bool
	bmpWidth      int
	bmpHeight     int
	bmpPalette    uint8
	mapOffset     uint32
	mapRegs       [16]uint8
	planePagesH   int
	planePagesV   int
	priority      uint8
	cramOffset    uint8
	pncnReg       uint16
	screenOver    uint8 // 0=wrap,1=pattern,2=transparent,3=force512
	rpMode        uint8 // RPMD bits 1:0
	sfprmdMode    uint8 // 0-2: special priority function mode
	sfccmdMode    uint8 // 0-3: special color calculation mode
	bmpSpecialPri bool  // bitmap special priority bit (BMPNB)
	bmpSpecialCC  bool  // bitmap special CC bit (BMPNB)
	mosaicH       int   // horizontal mosaic size (0=disabled, 1-16)
}

// rbgPerFrame holds precomputed per-frame rotation constants.
type rbgPerFrame struct {
	xp, yp int64 // .10 FP
	dxFP   int64 // .10 FP (per-pixel X increment)
	dyFP   int64 // .10 FP (per-pixel Y increment)
}

// lineCoef holds a line-start coefficient table entry.
type lineCoef struct {
	val int64
	msb bool
	lc  uint8
}

// rbgFrame holds the per-frame rotation state for one RBG screen.
// The paramA/paramB xst, yst, and kast fields are overwritten per
// line from VRAM when the corresponding RPRCTL re-read flag is set;
// every other field is constant for the frame. Re-reads must fully
// overwrite their field each line (never update incrementally) so
// rendering a line never depends on which lines ran before it.
// RBG1 uses only the B half.
type rbgFrame struct {
	paramA, paramB         rotParams
	pfA, pfB               rbgPerFrame
	needA, needB           bool
	paramABase, paramBBase uint32

	ktaof uint16
	crkte bool

	coefDotOK [4]bool

	coefEnA, coefOneWordA bool
	coefModeA             uint16
	klceA                 bool
	coefEnB, coefOneWordB bool
	coefModeB             uint16
	klceB                 bool

	// Per-parameter raster line of the most recent re-read, updated as
	// the worker walks lines. An un-armed line steps from here per the
	// VDP2 manual Sec 6.3 formula Xst + dXst*(Vcnt - Vcnt_when_read); a
	// line whose RPRCTL arm bit was set re-reads from VRAM and sets this
	// to its Vcnt (zeroing its own advance). The frame's first line uses
	// the value 0 (base read by buildRBG0Frame).
	lastVcntXA, lastVcntYA, lastVcntKAA int64
	lastVcntXB, lastVcntYB, lastVcntKAB int64

	// Param B map configuration for RBG0 RPMD modes 1-3 cell mode,
	// from MPxxRB/MPOFR/PLSZ.
	mapRegsB                   [16]uint8
	mapOffsetB                 uint32
	planePagesHB, planePagesVB int
	screenOverB                uint8
}

// ResetRotArm clears the per-scanline RPRCTL re-read arm table. Called by
// RunFrame at frame start with both workers parked so it cannot race the
// SH-2's arming writes or the VDP worker's per-line consumption.
func (v *VDP2) ResetRotArm() {
	clear(v.rprArm[:])
}

// signExtendFP decodes a two-word fixed-point value from VRAM.
// hi is the upper word, lo is the lower word.
// signBit is the bit position of the sign bit in the upper word.
// The fractional part is in bits 15:6 of the lower word.
// Returns the value as .10 fixed-point in int64.
func signExtendFP(hi, lo uint16, signBit int) int64 {
	intPart := int64(hi) & ((1 << (signBit + 1)) - 1)
	fracPart := int64(lo>>6) & 0x3FF
	val := (intPart << 10) | fracPart
	totalBits := signBit + 1 + 10
	if val&(1<<(totalBits-1)) != 0 {
		val |= ^((1 << totalBits) - 1) // sign extend
	}
	return val
}

// signExtend14 sign-extends a 14-bit signed integer.
func signExtend14(val uint16) int64 {
	v := int64(val & 0x3FFF)
	if v&0x2000 != 0 {
		v |= ^int64(0x3FFF)
	}
	return v
}

// decodeFPkxy decodes kx/ky (.16 FP) from two VRAM words.
// hi: sign bit in bit 7, integer in bits 6:0. lo: 16-bit fraction.
func decodeFPkxy(hi, lo uint16) int64 {
	intPart := int64(hi & 0xFF)
	val := (intPart << 16) | int64(lo)
	if val&(1<<23) != 0 {
		val |= ^int64(0xFFFFFF) // sign extend from bit 23
	}
	return val
}

// decodeFPkast decodes KAst (unsigned 16.10 FP) from two VRAM words.
func decodeFPkast(hi, lo uint16) int64 {
	return (int64(hi) << 10) | int64(lo>>6)
}

// rbgCoefAddrFromFP converts a .10 FP KAst value to a VRAM byte
// address per VDP2 User's Manual Sec 6.1. The integer part of the FP
// value indexes the table, scaled by entry size and offset by KTAOF.
func rbgCoefAddrFromFP(kastFP int64, ktaofBits uint32, oneWord bool) uint32 {
	kastInt := kastFP >> 10
	if oneWord {
		return (ktaofBits&0x07)*0x20000 + uint32(kastInt)*2
	}
	return (ktaofBits&0x03)*0x40000 + uint32(kastInt)*4
}

// rbgCoefDotBanks decodes which VRAM banks allow per-dot coefficient
// table reads from the RAMCTL rotation data bank select bits. Per VDP2
// User's Manual Sec 6.2, coefficient data needed per dot must be stored
// in a bank designated as coefficient table RAM (RDBSxn = 01), while
// per-line coefficient data can be stored in any bank. An unpartitioned
// VRAM-A or VRAM-B is governed by its bank-0 select bits.
func (v *VDP2) rbgCoefDotBanks() [4]bool {
	ramctl := v.frame.regs[vdp2RAMCTL]
	var ok [4]bool
	for bank := range ok {
		eff := bank
		if bank == 1 && ramctl&0x0100 == 0 {
			eff = 0
		}
		if bank == 3 && ramctl&0x0200 == 0 {
			eff = 2
		}
		ok[bank] = (ramctl>>(eff*2))&3 == 1
	}
	return ok
}

// readRotParams reads a rotation parameter table from VRAM at the given address.
func (v *VDP2) readRotParams(base uint32) rotParams {
	var p rotParams
	p.xst = signExtendFP(v.readVRAM16(base+0x00), v.readVRAM16(base+0x02), 12)
	p.yst = signExtendFP(v.readVRAM16(base+0x04), v.readVRAM16(base+0x06), 12)
	p.zst = signExtendFP(v.readVRAM16(base+0x08), v.readVRAM16(base+0x0A), 12)
	p.dxst = signExtendFP(v.readVRAM16(base+0x0C), v.readVRAM16(base+0x0E), 2)
	p.dyst = signExtendFP(v.readVRAM16(base+0x10), v.readVRAM16(base+0x12), 2)
	p.dx = signExtendFP(v.readVRAM16(base+0x14), v.readVRAM16(base+0x16), 2)
	p.dy = signExtendFP(v.readVRAM16(base+0x18), v.readVRAM16(base+0x1A), 2)
	p.a = signExtendFP(v.readVRAM16(base+0x1C), v.readVRAM16(base+0x1E), 3)
	p.b = signExtendFP(v.readVRAM16(base+0x20), v.readVRAM16(base+0x22), 3)
	p.c = signExtendFP(v.readVRAM16(base+0x24), v.readVRAM16(base+0x26), 3)
	p.d = signExtendFP(v.readVRAM16(base+0x28), v.readVRAM16(base+0x2A), 3)
	p.e = signExtendFP(v.readVRAM16(base+0x2C), v.readVRAM16(base+0x2E), 3)
	p.f = signExtendFP(v.readVRAM16(base+0x30), v.readVRAM16(base+0x32), 3)
	p.px = signExtend14(v.readVRAM16(base + 0x34))
	p.py = signExtend14(v.readVRAM16(base + 0x36))
	p.pz = signExtend14(v.readVRAM16(base + 0x38))
	p.cx = signExtend14(v.readVRAM16(base + 0x3C))
	p.cy = signExtend14(v.readVRAM16(base + 0x3E))
	p.cz = signExtend14(v.readVRAM16(base + 0x40))
	p.mx = signExtendFP(v.readVRAM16(base+0x44), v.readVRAM16(base+0x46), 13)
	p.my = signExtendFP(v.readVRAM16(base+0x48), v.readVRAM16(base+0x4A), 13)
	p.kx = decodeFPkxy(v.readVRAM16(base+0x4C), v.readVRAM16(base+0x4E))
	p.ky = decodeFPkxy(v.readVRAM16(base+0x50), v.readVRAM16(base+0x52))
	p.kast = decodeFPkast(v.readVRAM16(base+0x54), v.readVRAM16(base+0x56))
	p.dkast = signExtendFP(v.readVRAM16(base+0x58), v.readVRAM16(base+0x5A), 9)
	p.dkax = signExtendFP(v.readVRAM16(base+0x5C), v.readVRAM16(base+0x5E), 9)
	return p
}

// computePerLine computes per-line Xsp/Ysp values.
// vcnt is the current scanline. lastVcntX/lastVcntY are the scanlines at
// which Xst/Yst were last read from the rotation parameter table. Per
// VDP2 manual Sec 6.3 the screen coordinate steps as
// Xst + DXst*(Vcnt - Vcnt_when_read): a line that re-read this scanline
// passes lastVcntX==vcnt (zero advance); an un-armed line steps from the
// last read. Xst itself holds the value from that last read.
func computePerLine(p *rotParams, vcnt, lastVcntX, lastVcntY int64) (xsp, ysp int64) {
	// Xsp = A*[(Xst + DXst*(vcnt-lastX)) - Px] + B*[(Yst + DYst*(vcnt-lastY)) - Py] + C*(Zst - Pz)
	sx := p.xst - (p.px << 10) + p.dxst*(vcnt-lastVcntX)
	sy := p.yst - (p.py << 10) + p.dyst*(vcnt-lastVcntY)
	sz := p.zst - (p.pz << 10) // .10
	// A (.10) * sx (.10) = .20, >>10 = .10
	xsp = (p.a*sx + p.b*sy + p.c*sz) >> 10
	ysp = (p.d*sx + p.e*sy + p.f*sz) >> 10
	return
}

// rbgLineKAstFP returns the line's KAst value as .10 FP per
// VDP2 User's Manual Sec 6.1: KAst + ΔKAst x (Vcnt - Vcnt_when_read).
// lastVcntKA is the scanline at which KAst was last read from the table;
// a line that re-read this scanline passes lastVcntKA==vcnt so ΔKAst is
// not added. The returned value is the line's KAst before the per-pixel
// ΔKAx x Hcnt term, which callers must accumulate in FP (not after
// bit-shifting) to avoid dropping fractional precision.
func rbgLineKAstFP(kast, dkast, vcnt, lastVcntKA int64) int64 {
	return kast + dkast*(vcnt-lastVcntKA)
}

// readCoefficient reads a coefficient table entry from VRAM.
// oneWord: true=1-word format, false=2-word format.
// mode3: true if coefficient data mode is 3 (Xp replacement).
// Returns the coefficient value, the MSB (transparent/switching) flag, and
// the 7-bit line color screen data from bits 14:8 of the 2-word format's
// first word (0 for 1-word format).
// For modes 0-2, value is .16 FP (for kx/ky replacement).
// For mode 3, value is .10 FP (for Xp replacement).
func (v *VDP2) readCoefficient(addr uint32, oneWord bool, mode3 bool, fromCRAM bool) (int64, bool, uint8) {
	read16 := func(a uint32) uint16 {
		if fromCRAM {
			return v.readCRAM16(0x800 + (a & 0x7FF))
		}
		return v.readVRAM16(a & (vdp2VRAMSize - 1))
	}
	if !mode3 {
		if oneWord {
			w := read16(addr)
			msb := w&0x8000 != 0
			intPart := int64((w >> 10) & 0x1F)
			fracPart := int64(w & 0x03FF)
			val := (intPart << 10) | fracPart
			if val&(1<<14) != 0 {
				val |= ^int64(0x7FFF)
			}
			val <<= 6
			return val, msb, 0
		}
		w0 := read16(addr)
		w1 := read16(addr + 2)
		msb := w0&0x8000 != 0
		lcBits := uint8((w0 >> 8) & 0x7F)
		intPart := int64(w0 & 0x00FF)
		val := (intPart << 16) | int64(w1)
		if val&(1<<23) != 0 {
			val |= ^int64(0xFFFFFF)
		}
		return val, msb, lcBits
	}
	if oneWord {
		w := read16(addr)
		msb := w&0x8000 != 0
		val := int64(w & 0x7FFF)
		if val&(1<<14) != 0 {
			val |= ^int64(0x7FFF)
		}
		val <<= 8
		return val, msb, 0
	}
	w0 := read16(addr)
	w1 := read16(addr + 2)
	msb := w0&0x8000 != 0
	lcBits := uint8((w0 >> 8) & 0x7F)
	intMSB := int64(w0 & 0x00FF)
	intLSB := int64(w1 >> 8)
	fracPart := int64(w1 & 0xFF)
	val := (intMSB << 16) | (intLSB << 8) | fracPart
	if val&(1<<23) != 0 {
		val |= ^int64(0xFFFFFF)
	}
	val <<= 2
	return val, msb, lcBits
}

// rotArmBits returns the RPRCTL re-read arm bits captured for raster
// line y: bits 0-2 parameter A (Xst/Yst/KAst), bits 8-10 parameter B.
// The renderer consumes these when setting up the line; the SH-2 must
// re-arm every line it wants re-read (the bits self-clear per line).
func (v *VDP2) rotArmBits(y int) uint16 {
	if y < 0 || y >= len(v.rprArm) {
		return 0
	}
	return v.rprArm[y]
}

// rbg0SpanSetup prepares row y of the RBG0 rotation scroll screen for
// span rendering, dispatching on the configured cell or bitmap mode.
// y is a field-line index in 0..activeLines-1; buf rows are
// activeWidth pixels.
func (v *VDP2) rbg0SpanSetup(buf []uint32, cfg *rbgConfig, rf *rbgFrame, y int) func(x0, x1 int) {
	if cfg.bitmapMode {
		return v.rbg0BitmapSpanSetup(buf, cfg, rf, y)
	}
	return v.rbg0CellSpanSetup(buf, cfg, rf, y)
}

// rbg0CellSpanSetup prepares row y of cell-mode RBG0.
func (v *VDP2) rbg0CellSpanSetup(buf []uint32, cfg *rbgConfig, rf *rbgFrame, y int) func(x0, x1 int) {
	width := v.frame.width

	// paramA/paramB stay pointers into rf because the RPRCTL re-reads
	// below overwrite their xst, yst, and kast fields; those writes
	// must persist on the frame state.
	paramA, paramB := &rf.paramA, &rf.paramB
	pfA, pfB := &rf.pfA, &rf.pfB
	needA, needB := rf.needA, rf.needB
	coefEnA, coefOneWordA, coefModeA := rf.coefEnA, rf.coefOneWordA, rf.coefModeA
	coefEnB, coefOneWordB, coefModeB := rf.coefEnB, rf.coefOneWordB, rf.coefModeB
	klceA, klceB := rf.klceA, rf.klceB
	ktaof, crkte := rf.ktaof, rf.crkte
	paramABase, paramBBase := rf.paramABase, rf.paramBBase
	mapRegsB := &rf.mapRegsB
	mapOffsetB := rf.mapOffsetB
	planePagesHB, planePagesVB := rf.planePagesHB, rf.planePagesVB
	screenOverB := rf.screenOverB

	// Cell mode constants
	// Character number always indexes in 0x20-byte units regardless of color depth.
	var cellBytes uint32 = 0x20

	// Sub-cell scale for 2x2 characters: consecutive sub-cells must skip
	// by the actual cell data size divided by 0x20.
	var subCellScale uint32 = 1
	switch cfg.colorMode {
	case 1:
		subCellScale = 2
	case 2, 3:
		subCellScale = 4
	}

	var entrySize uint32
	if cfg.pnWord1 {
		entrySize = 2
	} else {
		entrySize = 4
	}

	charPx := 8
	mapDim := 64
	if !cfg.charSize1x1 {
		charPx = 16
		mapDim = 32
	}
	pageBoundary := uint32(mapDim*mapDim) * entrySize
	planeCellsH := mapDim * cfg.planePagesH
	planeCellsV := mapDim * cfg.planePagesV
	totalCellsH := planeCellsH * 4
	totalCellsV := planeCellsV * 4
	totalPixH := totalCellsH * charPx
	totalPixV := totalCellsV * charPx

	vcnt := int64(y)
	if v.frame.effIntl == 3 {
		vcnt = int64(y*2 + v.frame.field)
	}

	// Per-line parameter re-reading, gated by this scanline's self-clearing
	// RPRCTL arm. An armed parameter re-reads from VRAM and rebases its
	// per-line advance to this scanline; un-armed parameters keep stepping
	// from their last read (computePerLine / rbgLineKAstFP).
	arm := v.rotArmBits(y)
	if needA {
		if arm&0x01 != 0 {
			paramA.xst = signExtendFP(v.readVRAM16(paramABase+0x00), v.readVRAM16(paramABase+0x02), 12)
			rf.lastVcntXA = vcnt
		}
		if arm&0x02 != 0 {
			paramA.yst = signExtendFP(v.readVRAM16(paramABase+0x04), v.readVRAM16(paramABase+0x06), 12)
			rf.lastVcntYA = vcnt
		}
		if arm&0x04 != 0 {
			paramA.kast = decodeFPkast(v.readVRAM16(paramABase+0x54), v.readVRAM16(paramABase+0x56))
			rf.lastVcntKAA = vcnt
		}
	}
	if needB {
		if arm&0x0100 != 0 {
			paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
			rf.lastVcntXB = vcnt
		}
		if arm&0x0200 != 0 {
			paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
			rf.lastVcntYB = vcnt
		}
		if arm&0x0400 != 0 {
			paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
			rf.lastVcntKAB = vcnt
		}
	}

	// Per-line values
	var xspA, yspA, xspB, yspB int64
	if needA {
		xspA, yspA = computePerLine(paramA, vcnt, rf.lastVcntXA, rf.lastVcntYA)
	}
	if needB {
		xspB, yspB = computePerLine(paramB, vcnt, rf.lastVcntXB, rf.lastVcntYB)
	}

	// Per-line KAst in .10 FP (PDF Sec 6.1: KAst + ΔKAst x (Vcnt -
	// Vcnt_when_read)). Kept as FP so the per-pixel ΔKAx x Hcnt term can
	// accumulate in FP before integer extraction (the inverse order drops
	// the fractional part of ΔKAx and magnifies it by hcnt).
	var lineKAstFPA, lineKAstFPB int64
	if coefEnA && needA {
		lineKAstFPA = rbgLineKAstFP(paramA.kast, paramA.dkast, vcnt, rf.lastVcntKAA)
	}
	if coefEnB && needB {
		lineKAstFPB = rbgLineKAstFP(paramB.kast, paramB.dkast, vcnt, rf.lastVcntKAB)
	}

	// Line-start coefficient for each parameter. Per manual Sec 6.2 only
	// per-dot coefficient reads require a designated bank (rf.coefDotOK);
	// the per-line read works from any bank, and a dot whose coefficient
	// address falls in an undesignated bank keeps this value.
	coefDotOK := rf.coefDotOK
	var lineCoefA, lineCoefB lineCoef
	if coefEnA && needA {
		addr := rbgCoefAddrFromFP(lineKAstFPA, uint32(ktaof), coefOneWordA)
		lineCoefA.val, lineCoefA.msb, lineCoefA.lc = v.readCoefficient(addr, coefOneWordA, coefModeA == 3, crkte)
	}
	if coefEnB && needB {
		addr := rbgCoefAddrFromFP(lineKAstFPB, uint32(ktaof>>8), coefOneWordB)
		lineCoefB.val, lineCoefB.msb, lineCoefB.lc = v.readCoefficient(addr, coefOneWordB, coefModeB == 3, crkte)
	}

	// Per-screen color calculation enable (CCCTL) is a precondition for
	// every special CC mode (manual Table 12.3). It is frame-constant, so
	// compute it once per line here instead of per pixel.
	screenCC := isCCEnabled(v.frame.regs[vdp2CCCTL], 4)

	return func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			ex := x
			if cfg.mosaicH > 1 {
				ex = (x / cfg.mosaicH) * cfg.mosaicH
			}
			hcnt := int64(ex)

			// Determine which parameter set to use
			useA := true
			switch cfg.rpMode {
			case 1: // Param B only
				useA = false
			case 3: // Window switches
				if v.isRPWindowB(x, y) {
					useA = false
				}
			}

			var pf *rbgPerFrame
			var pp *rotParams
			var xsp, ysp int64
			var curCoefEn bool
			var curCoefOneWord bool
			var curCoefMode uint16
			var curLineKAstFP int64
			var curKtaofBits uint32
			var curKLCE bool
			var curMapRegs *[16]uint8
			var curMapOffset uint32
			var curPlaneCellsH, curPlaneCellsV int
			var curTotalPixH, curTotalPixV int
			var curScreenOver uint8

			if useA {
				pf = pfA
				pp = paramA
				xsp = xspA
				ysp = yspA
				curCoefEn = coefEnA
				curCoefOneWord = coefOneWordA
				curCoefMode = coefModeA
				curLineKAstFP = lineKAstFPA
				curKtaofBits = uint32(ktaof)
				curKLCE = klceA
				curMapRegs = &cfg.mapRegs
				curMapOffset = cfg.mapOffset
				curPlaneCellsH = planeCellsH
				curPlaneCellsV = planeCellsV
				curTotalPixH = totalPixH
				curTotalPixV = totalPixV
				curScreenOver = cfg.screenOver
			} else {
				pf = pfB
				pp = paramB
				xsp = xspB
				ysp = yspB
				curCoefEn = coefEnB
				curCoefOneWord = coefOneWordB
				curCoefMode = coefModeB
				curLineKAstFP = lineKAstFPB
				curKtaofBits = uint32(ktaof >> 8)
				curKLCE = klceB
				curMapRegs = mapRegsB
				curMapOffset = mapOffsetB
				curPlaneCellsH = planeCellsH
				curPlaneCellsV = planeCellsV
				curTotalPixH = totalPixH
				curTotalPixV = totalPixV
				curScreenOver = screenOverB
				if needB {
					pcH := mapDim * planePagesHB
					pcV := mapDim * planePagesVB
					curPlaneCellsH = pcH
					curPlaneCellsV = pcV
					curTotalPixH = pcH * 4 * charPx
					curTotalPixV = pcV * 4 * charPx
				}
			}

			// Apply coefficient
			kx := pp.kx
			ky := pp.ky
			xpVal := pf.xp

			if curCoefEn {
				mode3 := curCoefMode == 3
				// PDF Sec 6.1: accumulate ΔKAx x Hcnt in .10 FP then
				// extract integer; doing the shift before the multiply
				// drops fractional precision and magnifies the error
				// by Hcnt.
				pixelKAstFP := curLineKAstFP + pp.dkax*hcnt
				coefAddr := rbgCoefAddrFromFP(pixelKAstFP, curKtaofBits, curCoefOneWord)
				var coefVal int64
				var coefMSB bool
				var coefLC uint8
				if crkte || coefDotOK[(coefAddr&(vdp2VRAMSize-1))>>17] {
					coefVal, coefMSB, coefLC = v.readCoefficient(coefAddr, curCoefOneWord, mode3, crkte)
				} else if useA {
					coefVal, coefMSB, coefLC = lineCoefA.val, lineCoefA.msb, lineCoefA.lc
				} else {
					coefVal, coefMSB, coefLC = lineCoefB.val, lineCoefB.msb, lineCoefB.lc
				}

				// In RPMD mode 2 the MSB of parameter A's coefficient selects
				// the rotation parameter (VDP2 manual Sec 6.1, Table 6.4): MSB 0
				// keeps parameter A, MSB 1 switches the dot to parameter B.
				// After switching, parameter B's own coefficient table is read;
				// its MSB is a transparency bit and its value replaces B's kx/ky
				// (or Xp in mode 3). Parameter A's coefficient value and line
				// color do not apply to a switched dot.
				if cfg.rpMode == 2 && useA && coefMSB {
					pf = pfB
					pp = paramB
					xsp = xspB
					ysp = yspB
					kx = pp.kx
					ky = pp.ky
					xpVal = pf.xp
					curMapRegs = mapRegsB
					curMapOffset = mapOffsetB
					if needB {
						pcH := mapDim * planePagesHB
						pcV := mapDim * planePagesVB
						curPlaneCellsH = pcH
						curPlaneCellsV = pcV
						curTotalPixH = pcH * 4 * charPx
						curTotalPixV = pcV * 4 * charPx
					}
					curScreenOver = screenOverB

					if coefEnB {
						mode3B := coefModeB == 3
						pixelKAstFPB := lineKAstFPB + pp.dkax*hcnt
						coefAddrB := rbgCoefAddrFromFP(pixelKAstFPB, uint32(ktaof>>8), coefOneWordB)
						var coefValB int64
						var coefMSBB bool
						var coefLCB uint8
						if crkte || coefDotOK[(coefAddrB&(vdp2VRAMSize-1))>>17] {
							coefValB, coefMSBB, coefLCB = v.readCoefficient(coefAddrB, coefOneWordB, mode3B, crkte)
						} else {
							coefValB, coefMSBB, coefLCB = lineCoefB.val, lineCoefB.msb, lineCoefB.lc
						}
						if klceB {
							v.rbg0LCBuf[y*width+x] = coefLCB | 0x80
						}
						if coefMSBB {
							// Parameter B transparency bit.
							buf[y*width+x] = 0
							continue
						}
						switch coefModeB {
						case 0: // Replace both kx and ky
							kx = coefValB
							ky = coefValB
						case 1: // Replace kx
							kx = coefValB
						case 2: // Replace ky
							ky = coefValB
						case 3: // Replace Xp
							xpVal = coefValB
						}
					}
					goto skipCoefApply
				}

				// Staying on the current parameter.
				if curKLCE {
					v.rbg0LCBuf[y*width+x] = coefLC | 0x80
				}
				if coefMSB {
					// Transparent dot.
					buf[y*width+x] = 0
					continue
				}
				switch curCoefMode {
				case 0: // Replace both kx and ky
					kx = coefVal
					ky = coefVal
				case 1: // Replace kx
					kx = coefVal
				case 2: // Replace ky
					ky = coefVal
				case 3: // Replace Xp
					xpVal = coefVal
				}
			}
		skipCoefApply:

			// Compute map coordinates
			xRaw := xsp + pf.dxFP*hcnt // .10
			yRaw := ysp + pf.dyFP*hcnt // .10
			mapXFP := (kx*xRaw)>>16 + xpVal
			mapYFP := (ky*yRaw)>>16 + pf.yp
			mapX := int(mapXFP >> 10)
			mapY := int(mapYFP >> 10)

			// Screen-over processing
			outOfBounds := mapX < 0 || mapX >= curTotalPixH || mapY < 0 || mapY >= curTotalPixV
			if outOfBounds {
				switch curScreenOver {
				case 0: // Wrap
					mapX = ((mapX % curTotalPixH) + curTotalPixH) % curTotalPixH
					mapY = ((mapY % curTotalPixV) + curTotalPixV) % curTotalPixV
				case 2: // Transparent
					buf[y*width+x] = 0
					continue
				case 3: // Force 512x512
					if mapX < 0 || mapX >= 512 || mapY < 0 || mapY >= 512 {
						buf[y*width+x] = 0
						continue
					}
				case 1: // Screen-over pattern from OVPNRA/OVPNRB
					var ovpnReg uint16
					if useA {
						ovpnReg = v.frame.regs[vdp2OVPNRA]
					} else {
						ovpnReg = v.frame.regs[vdp2OVPNRB]
					}
					ovCharNum, ovPalette, ovHflip, ovVflip := decodePattern1Word(ovpnReg, cfg.pncnReg, cfg.colorMode, cfg.auxMode1, cfg.charSize1x1)
					ovSpecialPri := cfg.pncnReg&(1<<9) != 0
					ovSpecialCC := cfg.pncnReg&(1<<8) != 0
					// Screen-over pattern repeats in displayed-line space; under
					// per-field interleave the loop's y is field-line so convert
					// to displayed-line before taking the modulo.
					ovY := y
					if v.frame.effIntl == 3 {
						ovY = y*2 + v.frame.field
					}
					ovDotX := x % charPx
					ovDotY := ovY % charPx
					if cfg.charSize1x1 {
						if ovHflip {
							ovDotX = 7 - ovDotX
						}
						if ovVflip {
							ovDotY = 7 - ovDotY
						}
					} else {
						if ovHflip {
							ovDotX = charPx - 1 - ovDotX
						}
						if ovVflip {
							ovDotY = charPx - 1 - ovDotY
						}
						subCell := (ovDotY/8)*2 + (ovDotX / 8)
						ovCharNum += uint32(subCell) * subCellScale
						ovDotX = ovDotX & 7
						ovDotY = ovDotY & 7
					}
					ovCellAddr := ovCharNum * cellBytes
					var ovR, ovG, ovB uint8
					var ovTransp bool
					var ovDotColor uint8
					var ovCramCCBit bool

					// Always decode through the CC-returning readers and capture
					// ovCramCCBit; it is consumed only by special CC mode 3 below.
					// The non-CC readers delegate to these, so this adds no cost.
					switch cfg.colorMode {
					case 0:
						dot := v.readCellPixel4bpp(ovCellAddr, ovDotX, ovDotY)
						ovDotColor = dot
						ovR, ovG, ovB, ovTransp, ovCramCCBit = v.lookupColorCC(dot, ovPalette, cfg.cramOffset, 0, cfg.transpOff)
					case 1:
						dot := v.readCellPixel8bpp(ovCellAddr, ovDotX, ovDotY)
						ovDotColor = dot
						ovR, ovG, ovB, ovTransp, ovCramCCBit = v.lookupColorCC(dot, ovPalette, cfg.cramOffset, 1, cfg.transpOff)
					case 2:
						raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
						idx := raw & 0x07FF
						ovDotColor = uint8(idx)
						if idx == 0 && !cfg.transpOff {
							ovTransp = true
						} else {
							colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
							ovR, ovG, ovB, ovCramCCBit = v.readCRAMColorWithCC(colorAddr)
						}
					case 3:
						raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
						if raw&0x8000 == 0 && !cfg.transpOff {
							ovTransp = true
						} else {
							ovR, ovG, ovB = rgb555ToRGB(raw)
						}
					}

					ovPriority := cfg.priority
					switch cfg.sfprmdMode {
					case 1:
						if ovSpecialPri {
							ovPriority = (ovPriority & 0xFE) | 1
						} else {
							ovPriority &= 0xFE
						}
					case 2:
						// Mode 2 needs the special priority bit set and an sfcode
						// match (manual Table 11.2); palette formats only.
						if cfg.colorMode != 3 {
							if ovSpecialPri && sfcodeMatches(v.sfcodeForScreen(4), ovDotColor) {
								ovPriority = (ovPriority & 0xFE) | 1
							} else {
								ovPriority &= 0xFE
							}
						}
					}
					if ovPriority == 0 && !ovTransp {
						ovTransp = true
					}

					// screenCC (per-screen CC enable) gates every mode per Table 12.3.
					var ovCCEnabled bool
					switch cfg.sfccmdMode {
					case 0:
						ovCCEnabled = screenCC
					case 1:
						ovCCEnabled = screenCC && ovSpecialCC
					case 2:
						if cfg.colorMode != 3 {
							ovCCEnabled = screenCC && ovSpecialCC && sfcodeMatches(v.sfcodeForScreen(4), ovDotColor)
						}
					case 3:
						if cfg.colorMode == 3 {
							ovCCEnabled = screenCC
						} else {
							ovCCEnabled = screenCC && ovCramCCBit
						}
					}

					if ovTransp {
						buf[y*width+x] = 0
					} else {
						px := uint32(ovPriority)<<24 | uint32(ovR)<<16 | uint32(ovG)<<8 | uint32(ovB)
						if ovCCEnabled {
							px |= layerCCBit
						}
						buf[y*width+x] = px
					}
					continue
				}
			}

			// 4x4 plane lookup
			cellX := mapX / charPx
			cellY := mapY / charPx
			dotX := mapX & (charPx - 1)
			dotY := mapY & (charPx - 1)

			planeX := (cellX / curPlaneCellsH) % 4
			planeY := (cellY / curPlaneCellsV) % 4
			planeIdx := planeY*4 + planeX

			localCellX := cellX % curPlaneCellsH
			localCellY := cellY % curPlaneCellsV
			pageX := localCellX / mapDim
			pageY := localCellY / mapDim
			pageCellX := localCellX % mapDim
			pageCellY := localCellY % mapDim

			combinedOffset := uint32(curMapRegs[planeIdx]&0x3F) | (curMapOffset << 6)
			planeBase := combinedOffset * pageBoundary
			pagesH := cfg.planePagesH
			if !useA && needB {
				pagesH = planePagesHB
			}
			pageOffset := uint32(pageY*pagesH+pageX) * pageBoundary
			entryOffset := uint32(pageCellY*mapDim+pageCellX) * entrySize
			patternAddr := planeBase + pageOffset + entryOffset

			var charNum uint32
			var palette uint8
			var hflip, vflip bool
			var specialPriBit, specialCCBit bool

			if cfg.pnWord1 {
				pn := v.readVRAM16(patternAddr)
				charNum, palette, hflip, vflip = decodePattern1Word(pn, cfg.pncnReg, cfg.colorMode, cfg.auxMode1, cfg.charSize1x1)
				specialPriBit = cfg.pncnReg&(1<<9) != 0
				specialCCBit = cfg.pncnReg&(1<<8) != 0
			} else {
				msw := v.readVRAM16(patternAddr)
				lsw := v.readVRAM16(patternAddr + 2)
				charNum, palette, hflip, vflip, specialPriBit, specialCCBit = decodePattern2Word(msw, lsw)
			}

			// Apply flips and select sub-cell for 2x2 characters
			var dx, dy int
			if cfg.charSize1x1 {
				dx = dotX
				dy = dotY
				if hflip {
					dx = 7 - dx
				}
				if vflip {
					dy = 7 - dy
				}
			} else {
				fdx := dotX
				fdy := dotY
				if hflip {
					fdx = 15 - fdx
				}
				if vflip {
					fdy = 15 - fdy
				}
				subCell := (fdy/8)*2 + (fdx / 8)
				charNum += uint32(subCell) * subCellScale
				dx = fdx & 7
				dy = fdy & 7
			}

			cellAddr := charNum * cellBytes

			var r, g, b uint8
			var transp bool
			var dotColor uint8
			var cramCCBit bool

			// Always decode through the CC-returning readers and capture
			// cramCCBit; it is consumed only by special CC mode 3 below.
			// The non-CC readers delegate to these, so this adds no cost.
			switch cfg.colorMode {
			case 0:
				dot := v.readCellPixel4bpp(cellAddr, dx, dy)
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
			case 1:
				dot := v.readCellPixel8bpp(cellAddr, dx, dy)
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
			case 2:
				raw := v.readCellPixel16bpp(cellAddr, dx, dy)
				idx := raw & 0x07FF
				dotColor = uint8(idx)
				if idx == 0 && !cfg.transpOff {
					transp = true
				} else {
					colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
					r, g, b, cramCCBit = v.readCRAMColorWithCC(colorAddr)
				}
			case 3:
				raw := v.readCellPixel16bpp(cellAddr, dx, dy)
				if raw&0x8000 == 0 && !cfg.transpOff {
					transp = true
				} else {
					r, g, b = rgb555ToRGB(raw)
				}
			}

			// Compute effective priority with special priority function
			priority := cfg.priority
			switch cfg.sfprmdMode {
			case 1:
				if specialPriBit {
					priority = (priority & 0xFE) | 1
				} else {
					priority &= 0xFE
				}
			case 2:
				// Mode 2 needs the special priority bit set and an sfcode match
				// (manual Table 11.2); palette formats only.
				if cfg.colorMode != 3 {
					if specialPriBit && sfcodeMatches(v.sfcodeForScreen(4), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable. screenCC (per-screen
			// CC enable) gates every mode per manual Table 12.3.
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = screenCC
			case 1:
				ccEnabled = screenCC && specialCCBit
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = screenCC && specialCCBit && sfcodeMatches(v.sfcodeForScreen(4), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = screenCC
				} else {
					ccEnabled = screenCC && cramCCBit
				}
			}

			if transp {
				buf[y*width+x] = 0
			} else {
				px := uint32(priority)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
				if ccEnabled {
					px |= layerCCBit
				}
				buf[y*width+x] = px
			}
		}
	}
}

// rbg0BitmapSpanSetup prepares row y of RBG0 in bitmap mode with
// rotation. Param re-reads and per-line position derivation match the
// cell path; coefficient tables are not applied in this mode.
func (v *VDP2) rbg0BitmapSpanSetup(buf []uint32, cfg *rbgConfig, rf *rbgFrame, y int) func(x0, x1 int) {
	width := v.frame.width

	// paramA/paramB stay pointers into rf because the RPRCTL re-reads
	// below overwrite their xst, yst, and kast fields; those writes
	// must persist on the frame state.
	paramA, paramB := &rf.paramA, &rf.paramB
	pfA, pfB := &rf.pfA, &rf.pfB
	needA, needB := rf.needA, rf.needB
	coefEnA, coefOneWordA, coefModeA := rf.coefEnA, rf.coefOneWordA, rf.coefModeA
	coefEnB, coefOneWordB, coefModeB := rf.coefEnB, rf.coefOneWordB, rf.coefModeB
	klceA, klceB := rf.klceA, rf.klceB
	ktaof, crkte := rf.ktaof, rf.crkte
	screenOverB := rf.screenOverB
	paramABase, paramBBase := rf.paramABase, rf.paramBBase

	// Bitmap base address: mapOffset (from MPOFR) * 0x20000
	baseAddr := cfg.mapOffset * 0x20000

	bmpW := cfg.bmpWidth
	bmpH := cfg.bmpHeight

	vcnt := int64(y)
	if v.frame.effIntl == 3 {
		vcnt = int64(y*2 + v.frame.field)
	}

	// Per-line re-reads gated by this scanline's self-clearing RPRCTL arm
	// (see rbg0CellSpanSetup).
	arm := v.rotArmBits(y)
	if needA {
		if arm&0x01 != 0 {
			paramA.xst = signExtendFP(v.readVRAM16(paramABase+0x00), v.readVRAM16(paramABase+0x02), 12)
			rf.lastVcntXA = vcnt
		}
		if arm&0x02 != 0 {
			paramA.yst = signExtendFP(v.readVRAM16(paramABase+0x04), v.readVRAM16(paramABase+0x06), 12)
			rf.lastVcntYA = vcnt
		}
		if arm&0x04 != 0 {
			paramA.kast = decodeFPkast(v.readVRAM16(paramABase+0x54), v.readVRAM16(paramABase+0x56))
			rf.lastVcntKAA = vcnt
		}
	}
	if needB {
		if arm&0x0100 != 0 {
			paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
			rf.lastVcntXB = vcnt
		}
		if arm&0x0200 != 0 {
			paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
			rf.lastVcntYB = vcnt
		}
		if arm&0x0400 != 0 {
			paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
			rf.lastVcntKAB = vcnt
		}
	}

	var xspA, yspA, xspB, yspB int64
	if needA {
		xspA, yspA = computePerLine(paramA, vcnt, rf.lastVcntXA, rf.lastVcntYA)
	}
	if needB {
		xspB, yspB = computePerLine(paramB, vcnt, rf.lastVcntXB, rf.lastVcntYB)
	}

	// Per-line KAst in .10 FP (see rbg0CellSpanSetup); kept as FP so the
	// per-pixel ΔKAx x Hcnt term accumulates before integer extraction.
	var lineKAstFPA, lineKAstFPB int64
	if coefEnA && needA {
		lineKAstFPA = rbgLineKAstFP(paramA.kast, paramA.dkast, vcnt, rf.lastVcntKAA)
	}
	if coefEnB && needB {
		lineKAstFPB = rbgLineKAstFP(paramB.kast, paramB.dkast, vcnt, rf.lastVcntKAB)
	}

	coefDotOK := rf.coefDotOK
	var lineCoefA, lineCoefB lineCoef
	if coefEnA && needA {
		addr := rbgCoefAddrFromFP(lineKAstFPA, uint32(ktaof), coefOneWordA)
		lineCoefA.val, lineCoefA.msb, lineCoefA.lc = v.readCoefficient(addr, coefOneWordA, coefModeA == 3, crkte)
	}
	if coefEnB && needB {
		addr := rbgCoefAddrFromFP(lineKAstFPB, uint32(ktaof>>8), coefOneWordB)
		lineCoefB.val, lineCoefB.msb, lineCoefB.lc = v.readCoefficient(addr, coefOneWordB, coefModeB == 3, crkte)
	}

	// Per-screen color calculation enable (CCCTL) is a precondition for
	// every special CC mode (manual Table 12.3). It is frame-constant, so
	// compute it once per line here instead of per pixel.
	screenCC := isCCEnabled(v.frame.regs[vdp2CCCTL], 4)

	return func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			ex := x
			if cfg.mosaicH > 1 {
				ex = (x / cfg.mosaicH) * cfg.mosaicH
			}
			hcnt := int64(ex)

			useA := cfg.rpMode != 1
			if cfg.rpMode == 3 && v.isRPWindowB(x, y) {
				useA = false
			}

			var pf *rbgPerFrame
			var pp *rotParams
			var xsp, ysp int64
			var curCoefEn bool
			var curCoefOneWord bool
			var curCoefMode uint16
			var curLineKAstFP int64
			var curKtaofBits uint32
			var curKLCE bool
			var curScreenOver uint8
			if useA {
				pf = pfA
				pp = paramA
				xsp = xspA
				ysp = yspA
				curCoefEn = coefEnA
				curCoefOneWord = coefOneWordA
				curCoefMode = coefModeA
				curLineKAstFP = lineKAstFPA
				curKtaofBits = uint32(ktaof)
				curKLCE = klceA
				curScreenOver = cfg.screenOver
			} else {
				pf = pfB
				pp = paramB
				xsp = xspB
				ysp = yspB
				curCoefEn = coefEnB
				curCoefOneWord = coefOneWordB
				curCoefMode = coefModeB
				curLineKAstFP = lineKAstFPB
				curKtaofBits = uint32(ktaof >> 8)
				curKLCE = klceB
				curScreenOver = screenOverB
			}

			kx := pp.kx
			ky := pp.ky
			xpVal := pf.xp

			// The coefficient table modifies the rotation coordinate transform
			// (kx/ky/Xp) per dot and supplies the transparency / parameter-switch
			// MSB, independently of whether the surface is cell or bitmap (VDP2
			// manual Sec 6.1 p.147). Mirrors rbg0CellSpanSetup.
			if curCoefEn {
				mode3 := curCoefMode == 3
				pixelKAstFP := curLineKAstFP + pp.dkax*hcnt
				coefAddr := rbgCoefAddrFromFP(pixelKAstFP, curKtaofBits, curCoefOneWord)
				var coefVal int64
				var coefMSB bool
				var coefLC uint8
				if crkte || coefDotOK[(coefAddr&(vdp2VRAMSize-1))>>17] {
					coefVal, coefMSB, coefLC = v.readCoefficient(coefAddr, curCoefOneWord, mode3, crkte)
				} else if useA {
					coefVal, coefMSB, coefLC = lineCoefA.val, lineCoefA.msb, lineCoefA.lc
				} else {
					coefVal, coefMSB, coefLC = lineCoefB.val, lineCoefB.msb, lineCoefB.lc
				}

				// RPMD mode 2: parameter A's coefficient MSB selects the
				// parameter; MSB 1 switches the dot to B, then B's own
				// coefficient is read (its MSB is a transparency bit).
				if cfg.rpMode == 2 && useA && coefMSB {
					pf = pfB
					pp = paramB
					xsp = xspB
					ysp = yspB
					kx = pp.kx
					ky = pp.ky
					xpVal = pf.xp
					curScreenOver = screenOverB

					if coefEnB {
						mode3B := coefModeB == 3
						pixelKAstFPB := lineKAstFPB + pp.dkax*hcnt
						coefAddrB := rbgCoefAddrFromFP(pixelKAstFPB, uint32(ktaof>>8), coefOneWordB)
						var coefValB int64
						var coefMSBB bool
						var coefLCB uint8
						if crkte || coefDotOK[(coefAddrB&(vdp2VRAMSize-1))>>17] {
							coefValB, coefMSBB, coefLCB = v.readCoefficient(coefAddrB, coefOneWordB, mode3B, crkte)
						} else {
							coefValB, coefMSBB, coefLCB = lineCoefB.val, lineCoefB.msb, lineCoefB.lc
						}
						if klceB {
							v.rbg0LCBuf[y*width+x] = coefLCB | 0x80
						}
						if coefMSBB {
							buf[y*width+x] = 0
							continue
						}
						switch coefModeB {
						case 0:
							kx = coefValB
							ky = coefValB
						case 1:
							kx = coefValB
						case 2:
							ky = coefValB
						case 3:
							xpVal = coefValB
						}
					}
					goto bmpCoefDone
				}

				// Staying on the current parameter.
				if curKLCE {
					v.rbg0LCBuf[y*width+x] = coefLC | 0x80
				}
				if coefMSB {
					buf[y*width+x] = 0
					continue
				}
				switch curCoefMode {
				case 0:
					kx = coefVal
					ky = coefVal
				case 1:
					kx = coefVal
				case 2:
					ky = coefVal
				case 3:
					xpVal = coefVal
				}
			}
		bmpCoefDone:

			xRaw := xsp + pf.dxFP*hcnt
			yRaw := ysp + pf.dyFP*hcnt
			mapXFP := (kx*xRaw)>>16 + xpVal
			mapYFP := (ky*yRaw)>>16 + pf.yp
			mapX := int(mapXFP >> 10)
			mapY := int(mapYFP >> 10)

			// Screen-over for bitmap: only wrap (mode 0) is meaningful; the
			// cell-mode over-pattern (mode 1) and modes 2/3 all leave the
			// out-of-bounds dot transparent.
			if mapX < 0 || mapX >= bmpW || mapY < 0 || mapY >= bmpH {
				switch curScreenOver {
				case 0: // wrap
					mapX = ((mapX % bmpW) + bmpW) % bmpW
					mapY = ((mapY % bmpH) + bmpH) % bmpH
				default:
					buf[y*width+x] = 0
					continue
				}
			}

			var r, g, b uint8
			var transp bool
			var dotColor uint8
			var cramCCBit bool

			// Always decode through the CC-returning readers and capture
			// cramCCBit; it is consumed only by special CC mode 3 below.
			// The non-CC readers delegate to these, so this adds no cost.
			switch cfg.colorMode {
			case 0: // 16-color (4bpp)
				addr := baseAddr + uint32(mapY*bmpW+mapX)/2
				byt := v.vram[addr&(vdp2VRAMSize-1)]
				var dot uint8
				if mapX&1 == 0 {
					dot = byt >> 4
				} else {
					dot = byt & 0x0F
				}
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, cfg.bmpPalette, cfg.cramOffset, 0, cfg.transpOff)
			case 1: // 256-color (8bpp)
				pixOff := baseAddr + uint32(mapY*bmpW+mapX)
				dot := v.vram[pixOff&(vdp2VRAMSize-1)]
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, cfg.bmpPalette, cfg.cramOffset, 1, cfg.transpOff)
			case 2: // 2048-color
				addr := baseAddr + uint32(mapY*bmpW+mapX)*2
				raw := v.readVRAM16(addr)
				idx := raw & 0x07FF
				dotColor = uint8(idx)
				if idx == 0 && !cfg.transpOff {
					transp = true
				} else {
					colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
					r, g, b, cramCCBit = v.readCRAMColorWithCC(colorAddr)
				}
			case 3: // 32768-color
				addr := baseAddr + uint32(mapY*bmpW+mapX)*2
				raw := v.readVRAM16(addr)
				if raw&0x8000 == 0 && !cfg.transpOff {
					transp = true
				} else {
					r, g, b = rgb555ToRGB(raw)
				}
			}

			// Compute effective priority with special priority function
			priority := cfg.priority
			switch cfg.sfprmdMode {
			case 1:
				if cfg.bmpSpecialPri {
					priority = (priority & 0xFE) | 1
				} else {
					priority &= 0xFE
				}
			case 2:
				// Mode 2 needs the bitmap special priority bit set and an sfcode
				// match (manual Table 11.2); palette formats only.
				if cfg.colorMode != 3 {
					if cfg.bmpSpecialPri && sfcodeMatches(v.sfcodeForScreen(4), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable. screenCC (per-screen
			// CC enable) gates every mode per manual Table 12.3.
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = screenCC
			case 1:
				ccEnabled = screenCC && cfg.bmpSpecialCC
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = screenCC && cfg.bmpSpecialCC && sfcodeMatches(v.sfcodeForScreen(4), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = screenCC
				} else {
					ccEnabled = screenCC && cramCCBit
				}
			}

			if transp {
				buf[y*width+x] = 0
			} else {
				px := uint32(priority)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
				if ccEnabled {
					px |= layerCCBit
				}
				buf[y*width+x] = px
			}
		}
	}
}

// rbg1SpanSetup prepares row y of RBG1 (rotation parameter B only)
// for span rendering, dispatching on the configured cell or bitmap
// mode. y is a field-line index in 0..activeLines-1; buf rows are
// activeWidth pixels.
func (v *VDP2) rbg1SpanSetup(buf []uint32, cfg *rbgConfig, rf *rbgFrame, y int) func(x0, x1 int) {
	if cfg.bitmapMode {
		return v.rbg1BitmapSpanSetup(buf, cfg, rf, y)
	}
	return v.rbg1CellSpanSetup(buf, cfg, rf, y)
}

// rbg1CellSpanSetup prepares row y of cell-mode RBG1.
func (v *VDP2) rbg1CellSpanSetup(buf []uint32, cfg *rbgConfig, rf *rbgFrame, y int) func(x0, x1 int) {
	width := v.frame.width

	// paramB stays a pointer into rf because the RPRCTL re-reads below
	// overwrite its xst, yst, and kast fields; those writes must
	// persist on the frame state.
	paramB := &rf.paramB
	pfB := &rf.pfB
	coefEn, coefOneWord, coefMode := rf.coefEnB, rf.coefOneWordB, rf.coefModeB
	klce := rf.klceB
	ktaof, crkteB := rf.ktaof, rf.crkte
	paramBBase := rf.paramBBase

	var cellBytes uint32 = 0x20

	var subCellScale uint32 = 1
	switch cfg.colorMode {
	case 1:
		subCellScale = 2
	case 2, 3:
		subCellScale = 4
	}

	var entrySize uint32
	if cfg.pnWord1 {
		entrySize = 2
	} else {
		entrySize = 4
	}

	charPx := 8
	mapDim := 64
	if !cfg.charSize1x1 {
		charPx = 16
		mapDim = 32
	}
	pageBoundary := uint32(mapDim*mapDim) * entrySize
	planeCellsH := mapDim * cfg.planePagesH
	planeCellsV := mapDim * cfg.planePagesV
	totalPixH := planeCellsH * 4 * charPx
	totalPixV := planeCellsV * 4 * charPx

	vcnt := int64(y)
	if v.frame.effIntl == 3 {
		vcnt = int64(y*2 + v.frame.field)
	}

	arm := v.rotArmBits(y)
	if arm&0x0100 != 0 {
		paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
		rf.lastVcntXB = vcnt
	}
	if arm&0x0200 != 0 {
		paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
		rf.lastVcntYB = vcnt
	}
	if arm&0x0400 != 0 {
		paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
		rf.lastVcntKAB = vcnt
	}

	xsp, ysp := computePerLine(paramB, vcnt, rf.lastVcntXB, rf.lastVcntYB)

	var lineKAstFP int64
	if coefEn {
		lineKAstFP = rbgLineKAstFP(paramB.kast, paramB.dkast, vcnt, rf.lastVcntKAB)
	}

	coefDotOK := rf.coefDotOK
	var lineCoefB lineCoef
	if coefEn {
		addr := rbgCoefAddrFromFP(lineKAstFP, uint32(ktaof>>8), coefOneWord)
		lineCoefB.val, lineCoefB.msb, lineCoefB.lc = v.readCoefficient(addr, coefOneWord, coefMode == 3, crkteB)
	}

	// Per-screen color calculation enable (CCCTL) is a precondition for
	// every special CC mode (manual Table 12.3). It is frame-constant, so
	// compute it once per line here instead of per pixel.
	screenCC := isCCEnabled(v.frame.regs[vdp2CCCTL], 0)

	return func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			ex := x
			if cfg.mosaicH > 1 {
				ex = (x / cfg.mosaicH) * cfg.mosaicH
			}
			hcnt := int64(ex)

			kx := paramB.kx
			ky := paramB.ky
			xpVal := pfB.xp

			if coefEn {
				mode3 := coefMode == 3
				pixelKAstFP := lineKAstFP + paramB.dkax*hcnt
				coefAddr := rbgCoefAddrFromFP(pixelKAstFP, uint32(ktaof>>8), coefOneWord)
				var coefVal int64
				var coefMSB bool
				var coefLC uint8
				if crkteB || coefDotOK[(coefAddr&(vdp2VRAMSize-1))>>17] {
					coefVal, coefMSB, coefLC = v.readCoefficient(coefAddr, coefOneWord, mode3, crkteB)
				} else {
					coefVal, coefMSB, coefLC = lineCoefB.val, lineCoefB.msb, lineCoefB.lc
				}
				if klce {
					v.rbg1LCBuf[y*width+x] = coefLC | 0x80
				}

				if coefMSB {
					buf[y*width+x] = 0
					continue
				}

				switch coefMode {
				case 0:
					kx = coefVal
					ky = coefVal
				case 1:
					kx = coefVal
				case 2:
					ky = coefVal
				case 3:
					xpVal = coefVal
				}
			}

			xRaw := xsp + pfB.dxFP*hcnt
			yRaw := ysp + pfB.dyFP*hcnt
			mapXFP := (kx*xRaw)>>16 + xpVal
			mapYFP := (ky*yRaw)>>16 + pfB.yp
			mapX := int(mapXFP >> 10)
			mapY := int(mapYFP >> 10)

			outOfBounds := mapX < 0 || mapX >= totalPixH || mapY < 0 || mapY >= totalPixV
			if outOfBounds {
				switch cfg.screenOver {
				case 0:
					mapX = ((mapX % totalPixH) + totalPixH) % totalPixH
					mapY = ((mapY % totalPixV) + totalPixV) % totalPixV
				case 2:
					buf[y*width+x] = 0
					continue
				case 3:
					if mapX < 0 || mapX >= 512 || mapY < 0 || mapY >= 512 {
						buf[y*width+x] = 0
						continue
					}
				case 1: // Screen-over pattern from OVPNRB
					ovpnReg := v.frame.regs[vdp2OVPNRB]
					ovCharNum, ovPalette, ovHflip, ovVflip := decodePattern1Word(ovpnReg, cfg.pncnReg, cfg.colorMode, cfg.auxMode1, cfg.charSize1x1)
					ovSpecialPri := cfg.pncnReg&(1<<9) != 0
					ovSpecialCC := cfg.pncnReg&(1<<8) != 0
					// Screen-over pattern repeats in displayed-line space; under
					// per-field interleave the loop's y is field-line so convert
					// to displayed-line before taking the modulo.
					ovY := y
					if v.frame.effIntl == 3 {
						ovY = y*2 + v.frame.field
					}
					ovDotX := x % charPx
					ovDotY := ovY % charPx
					if cfg.charSize1x1 {
						if ovHflip {
							ovDotX = 7 - ovDotX
						}
						if ovVflip {
							ovDotY = 7 - ovDotY
						}
					} else {
						if ovHflip {
							ovDotX = charPx - 1 - ovDotX
						}
						if ovVflip {
							ovDotY = charPx - 1 - ovDotY
						}
						subCell := (ovDotY/8)*2 + (ovDotX / 8)
						ovCharNum += uint32(subCell) * subCellScale
						ovDotX = ovDotX & 7
						ovDotY = ovDotY & 7
					}
					ovCellAddr := ovCharNum * cellBytes
					var ovR, ovG, ovB uint8
					var ovTransp bool
					var ovDotColor uint8
					var ovCramCCBit bool

					// Always decode through the CC-returning readers and capture
					// ovCramCCBit; it is consumed only by special CC mode 3 below.
					// The non-CC readers delegate to these, so this adds no cost.
					switch cfg.colorMode {
					case 0:
						dot := v.readCellPixel4bpp(ovCellAddr, ovDotX, ovDotY)
						ovDotColor = dot
						ovR, ovG, ovB, ovTransp, ovCramCCBit = v.lookupColorCC(dot, ovPalette, cfg.cramOffset, 0, cfg.transpOff)
					case 1:
						dot := v.readCellPixel8bpp(ovCellAddr, ovDotX, ovDotY)
						ovDotColor = dot
						ovR, ovG, ovB, ovTransp, ovCramCCBit = v.lookupColorCC(dot, ovPalette, cfg.cramOffset, 1, cfg.transpOff)
					case 2:
						raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
						idx := raw & 0x07FF
						ovDotColor = uint8(idx)
						if idx == 0 && !cfg.transpOff {
							ovTransp = true
						} else {
							colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
							ovR, ovG, ovB, ovCramCCBit = v.readCRAMColorWithCC(colorAddr)
						}
					case 3:
						raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
						if raw&0x8000 == 0 && !cfg.transpOff {
							ovTransp = true
						} else {
							ovR, ovG, ovB = rgb555ToRGB(raw)
						}
					}

					ovPriority := cfg.priority
					switch cfg.sfprmdMode {
					case 1:
						if ovSpecialPri {
							ovPriority = (ovPriority & 0xFE) | 1
						} else {
							ovPriority &= 0xFE
						}
					case 2:
						// Mode 2 needs the special priority bit set and an sfcode
						// match (manual Table 11.2); palette formats only.
						if cfg.colorMode != 3 {
							if ovSpecialPri && sfcodeMatches(v.sfcodeForScreen(0), ovDotColor) {
								ovPriority = (ovPriority & 0xFE) | 1
							} else {
								ovPriority &= 0xFE
							}
						}
					}
					if ovPriority == 0 && !ovTransp {
						ovTransp = true
					}

					// screenCC (per-screen CC enable) gates every mode per Table 12.3.
					var ovCCEnabled bool
					switch cfg.sfccmdMode {
					case 0:
						ovCCEnabled = screenCC
					case 1:
						ovCCEnabled = screenCC && ovSpecialCC
					case 2:
						if cfg.colorMode != 3 {
							ovCCEnabled = screenCC && ovSpecialCC && sfcodeMatches(v.sfcodeForScreen(0), ovDotColor)
						}
					case 3:
						if cfg.colorMode == 3 {
							ovCCEnabled = screenCC
						} else {
							ovCCEnabled = screenCC && ovCramCCBit
						}
					}

					if ovTransp {
						buf[y*width+x] = 0
					} else {
						px := uint32(ovPriority)<<24 | uint32(ovR)<<16 | uint32(ovG)<<8 | uint32(ovB)
						if ovCCEnabled {
							px |= layerCCBit
						}
						buf[y*width+x] = px
					}
					continue
				}
			}

			cellX := mapX / charPx
			cellY := mapY / charPx
			dotX := mapX & (charPx - 1)
			dotY := mapY & (charPx - 1)

			planeX := (cellX / planeCellsH) % 4
			planeY := (cellY / planeCellsV) % 4
			planeIdx := planeY*4 + planeX

			localCellX := cellX % planeCellsH
			localCellY := cellY % planeCellsV
			pageX := localCellX / mapDim
			pageY := localCellY / mapDim
			pageCellX := localCellX % mapDim
			pageCellY := localCellY % mapDim

			combinedOffset := uint32(cfg.mapRegs[planeIdx]&0x3F) | (cfg.mapOffset << 6)
			planeBase := combinedOffset * pageBoundary
			pageOffset := uint32(pageY*cfg.planePagesH+pageX) * pageBoundary
			entryOffset := uint32(pageCellY*mapDim+pageCellX) * entrySize
			patternAddr := planeBase + pageOffset + entryOffset

			var charNum uint32
			var palette uint8
			var hflip, vflip bool
			var specialPriBit, specialCCBit bool

			if cfg.pnWord1 {
				pn := v.readVRAM16(patternAddr)
				charNum, palette, hflip, vflip = decodePattern1Word(pn, cfg.pncnReg, cfg.colorMode, cfg.auxMode1, cfg.charSize1x1)
				specialPriBit = cfg.pncnReg&(1<<9) != 0
				specialCCBit = cfg.pncnReg&(1<<8) != 0
			} else {
				msw := v.readVRAM16(patternAddr)
				lsw := v.readVRAM16(patternAddr + 2)
				charNum, palette, hflip, vflip, specialPriBit, specialCCBit = decodePattern2Word(msw, lsw)
			}

			var dx, dy int
			if cfg.charSize1x1 {
				dx = dotX
				dy = dotY
				if hflip {
					dx = 7 - dx
				}
				if vflip {
					dy = 7 - dy
				}
			} else {
				fdx := dotX
				fdy := dotY
				if hflip {
					fdx = 15 - fdx
				}
				if vflip {
					fdy = 15 - fdy
				}
				subCell := (fdy/8)*2 + (fdx / 8)
				charNum += uint32(subCell) * subCellScale
				dx = fdx & 7
				dy = fdy & 7
			}

			cellAddr := charNum * cellBytes

			var r, g, b uint8
			var transp bool
			var dotColor uint8
			var cramCCBit bool

			// Always decode through the CC-returning readers and capture
			// cramCCBit; it is consumed only by special CC mode 3 below.
			// The non-CC readers delegate to these, so this adds no cost.
			switch cfg.colorMode {
			case 0:
				dot := v.readCellPixel4bpp(cellAddr, dx, dy)
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
			case 1:
				dot := v.readCellPixel8bpp(cellAddr, dx, dy)
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
			case 2:
				raw := v.readCellPixel16bpp(cellAddr, dx, dy)
				idx := raw & 0x07FF
				dotColor = uint8(idx)
				if idx == 0 && !cfg.transpOff {
					transp = true
				} else {
					colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
					r, g, b, cramCCBit = v.readCRAMColorWithCC(colorAddr)
				}
			case 3:
				raw := v.readCellPixel16bpp(cellAddr, dx, dy)
				if raw&0x8000 == 0 && !cfg.transpOff {
					transp = true
				} else {
					r, g, b = rgb555ToRGB(raw)
				}
			}

			priority := cfg.priority
			switch cfg.sfprmdMode {
			case 1:
				if specialPriBit {
					priority = (priority & 0xFE) | 1
				} else {
					priority &= 0xFE
				}
			case 2:
				// Mode 2 needs the special priority bit set and an sfcode match
				// (manual Table 11.2); palette formats only.
				if cfg.colorMode != 3 {
					if specialPriBit && sfcodeMatches(v.sfcodeForScreen(0), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// screenCC (per-screen CC enable) gates every mode per Table 12.3.
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = screenCC
			case 1:
				ccEnabled = screenCC && specialCCBit
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = screenCC && specialCCBit && sfcodeMatches(v.sfcodeForScreen(0), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = screenCC
				} else {
					ccEnabled = screenCC && cramCCBit
				}
			}

			if transp {
				buf[y*width+x] = 0
			} else {
				px := uint32(priority)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
				if ccEnabled {
					px |= layerCCBit
				}
				buf[y*width+x] = px
			}
		}
	}
}

// rbg1BitmapSpanSetup prepares row y of RBG1 in bitmap mode with
// rotation (param B only).
func (v *VDP2) rbg1BitmapSpanSetup(buf []uint32, cfg *rbgConfig, rf *rbgFrame, y int) func(x0, x1 int) {
	width := v.frame.width

	// paramB stays a pointer into rf because the RPRCTL re-reads below
	// overwrite its xst, yst, and kast fields; those writes must
	// persist on the frame state.
	paramB := &rf.paramB
	pfB := &rf.pfB
	coefEn, coefOneWord, coefMode := rf.coefEnB, rf.coefOneWordB, rf.coefModeB
	klce := rf.klceB
	ktaof := rf.ktaof
	paramBBase := rf.paramBBase

	// Bitmap base address: mapOffset (from MPOFR) * 0x20000
	baseAddr := cfg.mapOffset * 0x20000

	bmpW := cfg.bmpWidth
	bmpH := cfg.bmpHeight

	vcnt := int64(y)
	if v.frame.effIntl == 3 {
		vcnt = int64(y*2 + v.frame.field)
	}

	arm := v.rotArmBits(y)
	if arm&0x0100 != 0 {
		paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
		rf.lastVcntXB = vcnt
	}
	if arm&0x0200 != 0 {
		paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
		rf.lastVcntYB = vcnt
	}
	if arm&0x0400 != 0 {
		paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
		rf.lastVcntKAB = vcnt
	}

	xsp, ysp := computePerLine(paramB, vcnt, rf.lastVcntXB, rf.lastVcntYB)

	var lineKAstFP int64
	if coefEn {
		lineKAstFP = rbgLineKAstFP(paramB.kast, paramB.dkast, vcnt, rf.lastVcntKAB)
	}

	coefDotOK := rf.coefDotOK
	var lineCoefB lineCoef
	if coefEn {
		addr := rbgCoefAddrFromFP(lineKAstFP, uint32(ktaof>>8), coefOneWord)
		lineCoefB.val, lineCoefB.msb, lineCoefB.lc = v.readCoefficient(addr, coefOneWord, coefMode == 3, rf.crkte)
	}

	// Per-screen color calculation enable (CCCTL) is a precondition for
	// every special CC mode (manual Table 12.3). It is frame-constant, so
	// compute it once per line here instead of per pixel.
	screenCC := isCCEnabled(v.frame.regs[vdp2CCCTL], 0)

	return func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			ex := x
			if cfg.mosaicH > 1 {
				ex = (x / cfg.mosaicH) * cfg.mosaicH
			}
			hcnt := int64(ex)

			kx := paramB.kx
			ky := paramB.ky
			xpVal := pfB.xp

			if coefEn {
				mode3 := coefMode == 3
				pixelKAstFP := lineKAstFP + paramB.dkax*hcnt
				coefAddr := rbgCoefAddrFromFP(pixelKAstFP, uint32(ktaof>>8), coefOneWord)
				var coefVal int64
				var coefMSB bool
				var coefLC uint8
				if rf.crkte || coefDotOK[(coefAddr&(vdp2VRAMSize-1))>>17] {
					coefVal, coefMSB, coefLC = v.readCoefficient(coefAddr, coefOneWord, mode3, rf.crkte)
				} else {
					coefVal, coefMSB, coefLC = lineCoefB.val, lineCoefB.msb, lineCoefB.lc
				}
				if klce {
					v.rbg1LCBuf[y*width+x] = coefLC | 0x80
				}

				if coefMSB {
					buf[y*width+x] = 0
					continue
				}

				switch coefMode {
				case 0:
					kx = coefVal
					ky = coefVal
				case 1:
					kx = coefVal
				case 2:
					ky = coefVal
				case 3:
					xpVal = coefVal
				}
			}

			xRaw := xsp + pfB.dxFP*hcnt
			yRaw := ysp + pfB.dyFP*hcnt
			mapXFP := (kx*xRaw)>>16 + xpVal
			mapYFP := (ky*yRaw)>>16 + pfB.yp
			mapX := int(mapXFP >> 10)
			mapY := int(mapYFP >> 10)

			// Screen-over for bitmap: only wrap (mode 0) is meaningful; the
			// cell-mode over-pattern (mode 1) and modes 2/3 all leave the
			// out-of-bounds dot transparent.
			if mapX < 0 || mapX >= bmpW || mapY < 0 || mapY >= bmpH {
				switch cfg.screenOver {
				case 0:
					mapX = ((mapX % bmpW) + bmpW) % bmpW
					mapY = ((mapY % bmpH) + bmpH) % bmpH
				default:
					buf[y*width+x] = 0
					continue
				}
			}

			var r, g, b uint8
			var transp bool
			var dotColor uint8
			var cramCCBit bool
			palette := cfg.bmpPalette

			// Always decode through the CC-returning readers and capture
			// cramCCBit; it is consumed only by special CC mode 3 below.
			// The non-CC readers delegate to these, so this adds no cost.
			switch cfg.colorMode {
			case 0:
				pixOff := baseAddr + uint32((mapY*bmpW+mapX)/2)
				byt := v.vram[pixOff&(vdp2VRAMSize-1)]
				var dot uint8
				if (mapY*bmpW+mapX)&1 == 0 {
					dot = byt >> 4
				} else {
					dot = byt & 0x0F
				}
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
			case 1:
				pixOff := baseAddr + uint32(mapY*bmpW+mapX)
				dot := v.vram[pixOff&(vdp2VRAMSize-1)]
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
			case 2:
				pixOff := baseAddr + uint32((mapY*bmpW+mapX)*2)
				hi := v.vram[pixOff&(vdp2VRAMSize-1)]
				lo := v.vram[(pixOff+1)&(vdp2VRAMSize-1)]
				raw := uint16(hi)<<8 | uint16(lo)
				idx := raw & 0x07FF
				dotColor = uint8(idx)
				if idx == 0 && !cfg.transpOff {
					transp = true
				} else {
					colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
					r, g, b, cramCCBit = v.readCRAMColorWithCC(colorAddr)
				}
			case 3:
				pixOff := baseAddr + uint32((mapY*bmpW+mapX)*2)
				hi := v.vram[pixOff&(vdp2VRAMSize-1)]
				lo := v.vram[(pixOff+1)&(vdp2VRAMSize-1)]
				raw := uint16(hi)<<8 | uint16(lo)
				if raw&0x8000 == 0 && !cfg.transpOff {
					transp = true
				} else {
					r, g, b = rgb555ToRGB(raw)
				}
			}

			// Compute effective priority with special priority function
			priority := cfg.priority
			switch cfg.sfprmdMode {
			case 1:
				if cfg.bmpSpecialPri {
					priority = (priority & 0xFE) | 1
				} else {
					priority &= 0xFE
				}
			case 2:
				// Mode 2 needs the bitmap special priority bit set and an sfcode
				// match (manual Table 11.2); palette formats only.
				if cfg.colorMode != 3 {
					if cfg.bmpSpecialPri && sfcodeMatches(v.sfcodeForScreen(0), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable. screenCC (per-screen
			// CC enable) gates every mode per manual Table 12.3.
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = screenCC
			case 1:
				ccEnabled = screenCC && cfg.bmpSpecialCC
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = screenCC && cfg.bmpSpecialCC && sfcodeMatches(v.sfcodeForScreen(0), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = screenCC
				} else {
					ccEnabled = screenCC && cramCCBit
				}
			}

			if transp {
				buf[y*width+x] = 0
			} else {
				px := uint32(priority)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
				if ccEnabled {
					px |= layerCCBit
				}
				buf[y*width+x] = px
			}
		}
	}
}

// isRPWindowB checks if the rotation parameter window selects Param B at (x,y).
// Caller passes field-line y; window Y bounds are in displayed-line space so
// under LSMD=3 the comparison uses the displayed row for this field.
func (v *VDP2) isRPWindowB(x, y int) bool {
	wctld := v.frame.regs[vdp2WCTLD]
	w0En := wctld&0x02 != 0
	w1En := wctld&0x08 != 0
	w0Area := wctld&0x01 != 0
	w1Area := wctld&0x04 != 0
	logic := wctld&0x80 != 0

	// Per VDP2 manual Sec 8.1 (p.193): with all window-enable bits off,
	// the active (window-enabled) area is the whole screen when the
	// logic bit is 1 and none of it when 0. Per Sec 6 (p.190) the
	// rotation parameter window shows Param B in the active area, so the
	// result equals the logic bit directly.
	if !w0En && !w1En {
		return logic
	}

	dispY := y
	if v.frame.effIntl == 3 {
		dispY = y*2 + v.frame.field
	}

	var w0Inside, w1Inside bool
	if w0En {
		if v.frame.regs[vdp2LWTA0U]&0x8000 != 0 {
			// Normal line window (W0LWE): per-scanline X boundaries
			// from a VRAM table (manual Sec 8.1 p.185-186).
			lwta0 := ((uint32(v.frame.regs[vdp2LWTA0U]&0x07) << 16) | uint32(v.frame.regs[vdp2LWTA0L]&0xFFFE)) * 2
			entryAddr := lwta0 + uint32(v.lineTableY(y))*4
			rs := v.readVRAM16(entryAddr)
			re := v.readVRAM16(entryAddr + 2)
			// Sec 8.1 p.183: start > end => whole line is outside. The
			// comparison is on the signed coordinate words, so an end of
			// 0xFFFF (-1) marks an excluded line.
			if int16(rs) <= int16(re) {
				sx := v.windowX(rs)
				ex := v.windowX(re)
				w0Inside = x >= sx && x <= ex
			}
		} else {
			sx := v.windowX(v.frame.regs[vdp2WPSX0])
			sy := int(v.frame.regs[vdp2WPSY0] & 0x01FF)
			ex := v.windowX(v.frame.regs[vdp2WPEX0])
			ey := int(v.frame.regs[vdp2WPEY0] & 0x01FF)
			if sx <= ex && sy <= ey {
				w0Inside = x >= sx && x <= ex && dispY >= sy && dispY <= ey
			}
		}
	}
	if w1En {
		if v.frame.regs[vdp2LWTA1U]&0x8000 != 0 {
			// Normal line window (W1LWE).
			lwta1 := ((uint32(v.frame.regs[vdp2LWTA1U]&0x07) << 16) | uint32(v.frame.regs[vdp2LWTA1L]&0xFFFE)) * 2
			entryAddr := lwta1 + uint32(v.lineTableY(y))*4
			rs := v.readVRAM16(entryAddr)
			re := v.readVRAM16(entryAddr + 2)
			// Sec 8.1 p.183: start > end => whole line is outside (signed
			// coordinate words; end 0xFFFF (-1) marks an excluded line).
			if int16(rs) <= int16(re) {
				sx := v.windowX(rs)
				ex := v.windowX(re)
				w1Inside = x >= sx && x <= ex
			}
		} else {
			sx := v.windowX(v.frame.regs[vdp2WPSX1])
			sy := int(v.frame.regs[vdp2WPSY1] & 0x01FF)
			ex := v.windowX(v.frame.regs[vdp2WPEX1])
			ey := int(v.frame.regs[vdp2WPEY1] & 0x01FF)
			if sx <= ex && sy <= ey {
				w1Inside = x >= sx && x <= ex && dispY >= sy && dispY <= ey
			}
		}
	}

	// Window area bit (manual Sec 8.1 p.196): 0 = active inside the
	// window, 1 = active outside.
	var w0Active, w1Active bool
	if w0En {
		if w0Area {
			w0Active = !w0Inside
		} else {
			w0Active = w0Inside
		}
	}
	if w1En {
		if w1Area {
			w1Active = !w1Inside
		} else {
			w1Active = w1Inside
		}
	}

	var active bool
	if w0En && w1En {
		if logic {
			active = w0Active && w1Active
		} else {
			active = w0Active || w1Active
		}
	} else if w0En {
		active = w0Active
	} else {
		active = w1Active
	}

	// Manual Sec 6 (p.190): Param B is shown in the window's active
	// area, Param A outside it. Return true when Param B is selected.
	return active
}
