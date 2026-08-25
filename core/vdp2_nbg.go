// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// nbgConfig holds decoded configuration for a normal scroll screen (NBG0-3).
type nbgConfig struct {
	enabled       bool
	colorMode     uint8  // 0=16-color(4bpp), 1=256(8bpp), 2=2048(16bpp), 3=32768(16bpp RGB)
	charSize1x1   bool   // true=1x1(8x8), false=2x2(16x16)
	pnWord1       bool   // true=1-word pattern name, false=2-word
	auxMode1      bool   // true=aux mode 1 (more char bits, no flip)
	mapOffset     uint32 // from MPOFN, 3 bits
	mapRegs       [4]uint8
	scrollXFP     int32 // 11.8 fixed-point scroll X
	scrollYFP     int32 // 11.8 fixed-point scroll Y
	incXFP        int32 // 3.8 fixed-point X increment (0x100 = 1.0)
	incYFP        int32 // 3.8 fixed-point Y increment (0x100 = 1.0)
	priority      uint8
	cramOffset    uint8
	pncnReg       uint16
	transpOff     bool // true = transparency disabled (xxTPON)
	mosaicH       int  // 0 = disabled, 1-16 = mosaic block width
	mosaicV       int  // 0 = disabled, 1-16 = mosaic block height
	lineScrollX   bool
	lineScrollY   bool
	lineZoomX     bool
	lineInterval  int    // 1, 2, 4, or 8
	lineTableAddr uint32 // VRAM byte address
	vcscEnabled   bool
	vcscTableAddr uint32 // VRAM byte address (NBG1 offset baked in for both-enabled layout)
	vcscStride    uint32 // 4 single-screen, 8 when both NBG0 and NBG1 VCSC active
	planePagesH   int    // 1 or 2 (from PLSZ)
	planePagesV   int    // 1 or 2 (from PLSZ)
	bitmapMode    bool   // BMEN=1: bitmap instead of cell/tile
	bmpWidth      int    // 512 or 1024
	bmpHeight     int    // 256 or 512
	bmpPalette    uint8
	sfprmdMode    uint8 // 0-2: special priority function mode
	sfccmdMode    uint8 // 0-3: special color calculation mode
	bmpSpecialPri bool  // bitmap special priority bit (BMPNA)
	bmpSpecialCC  bool  // bitmap special CC bit (BMPNA)
}

// decodeNBGConfig reads registers for the given screen (0-3) and fills nbgConfig.
func (v *VDP2) decodeNBGConfig(screen int) nbgConfig {
	var cfg nbgConfig
	bgon := v.frame.regs[vdp2BGON]
	cfg.enabled = bgon&(1<<uint(screen)) != 0
	cfg.transpOff = bgon&(1<<uint(8+screen)) != 0

	chctla := v.frame.regs[vdp2CHCTLA]
	chctlb := v.frame.regs[vdp2CHCTLB]

	switch screen {
	case 0:
		cfg.charSize1x1 = chctla&0x01 == 0
		cfg.colorMode = uint8((chctla >> 4) & 0x07)
		if cfg.colorMode > 4 {
			cfg.colorMode = 4
		}
		cfg.bitmapMode = chctla&0x02 != 0
		bmsz := (chctla >> 2) & 0x03
		cfg.bmpWidth = 512
		cfg.bmpHeight = 256
		if bmsz&0x02 != 0 {
			cfg.bmpWidth = 1024
		}
		if bmsz&0x01 != 0 {
			cfg.bmpHeight = 512
		}
		bmpna := v.frame.regs[vdp2BMPNA]
		// BMP6-4 (palette top 3 bits) at BMPNA bits 2:0 per PDF Sec 4.10.
		// Pre-shifted to bits 6:4 so cfg.bmpPalette is the 7-bit palette
		// number with low 4 bits zero, usable by lookupColor in any mode.
		cfg.bmpPalette = uint8(bmpna&0x07) << 4
		cfg.bmpSpecialPri = bmpna&(1<<5) != 0
		cfg.bmpSpecialCC = bmpna&(1<<4) != 0
		cfg.pncnReg = v.frame.regs[vdp2PNCN0]
		cfg.mapOffset = uint32(v.frame.regs[vdp2MPOFN]) & 0x07
		cfg.mapRegs[0] = uint8(v.frame.regs[vdp2MPABN0] & 0xFF)
		cfg.mapRegs[1] = uint8(v.frame.regs[vdp2MPABN0] >> 8)
		cfg.mapRegs[2] = uint8(v.frame.regs[vdp2MPCDN0] & 0xFF)
		cfg.mapRegs[3] = uint8(v.frame.regs[vdp2MPCDN0] >> 8)
		cfg.scrollXFP = int32(v.frame.regs[vdp2SCXIN0]&0x7FF)<<8 | int32(v.frame.regs[vdp2SCXDN0]>>8)
		cfg.scrollYFP = int32(v.frame.regs[vdp2SCYIN0]&0x7FF)<<8 | int32(v.frame.regs[vdp2SCYDN0]>>8)
		cfg.incXFP = int32(v.frame.regs[vdp2ZMXIN0]&0x07)<<8 | int32(v.frame.regs[vdp2ZMXDN0]>>8)
		cfg.incYFP = int32(v.frame.regs[vdp2ZMYIN0]&0x07)<<8 | int32(v.frame.regs[vdp2ZMYDN0]>>8)
		zmctl := v.frame.regs[vdp2ZMCTL]
		zmhf0 := zmctl & 0x01
		zmqt0 := (zmctl >> 1) & 0x01
		var maxIncX0 int32
		switch {
		case zmqt0 != 0:
			maxIncX0 = 0x400 // up to 1/4
		case zmhf0 != 0:
			maxIncX0 = 0x200 // up to 1/2
		default:
			maxIncX0 = 0x100 // no reduction
		}
		if cfg.incXFP > maxIncX0 {
			cfg.incXFP = maxIncX0
		}
		cfg.priority = uint8(v.frame.regs[vdp2PRINA]) & 0x07
		cfg.cramOffset = uint8(v.frame.regs[vdp2CRAOFA]) & 0x07
	case 1:
		cfg.charSize1x1 = chctla&0x0100 == 0
		cfg.colorMode = uint8((chctla >> 12) & 0x03)
		if cfg.colorMode > 4 {
			cfg.colorMode = 4
		}
		cfg.bitmapMode = chctla&0x0200 != 0
		bmsz := (chctla >> 10) & 0x03
		cfg.bmpWidth = 512
		cfg.bmpHeight = 256
		if bmsz&0x02 != 0 {
			cfg.bmpWidth = 1024
		}
		if bmsz&0x01 != 0 {
			cfg.bmpHeight = 512
		}
		bmpna := v.frame.regs[vdp2BMPNA]
		// BMP6-4 at BMPNA bits 10:8 per PDF Sec 4.10.
		cfg.bmpPalette = uint8((bmpna>>8)&0x07) << 4
		cfg.bmpSpecialPri = bmpna&(1<<13) != 0
		cfg.bmpSpecialCC = bmpna&(1<<12) != 0
		cfg.pncnReg = v.frame.regs[vdp2PNCN1]
		cfg.mapOffset = uint32((v.frame.regs[vdp2MPOFN] >> 4) & 0x07)
		cfg.mapRegs[0] = uint8(v.frame.regs[vdp2MPABN1] & 0xFF)
		cfg.mapRegs[1] = uint8(v.frame.regs[vdp2MPABN1] >> 8)
		cfg.mapRegs[2] = uint8(v.frame.regs[vdp2MPCDN1] & 0xFF)
		cfg.mapRegs[3] = uint8(v.frame.regs[vdp2MPCDN1] >> 8)
		cfg.scrollXFP = int32(v.frame.regs[vdp2SCXIN1]&0x7FF)<<8 | int32(v.frame.regs[vdp2SCXDN1]>>8)
		cfg.scrollYFP = int32(v.frame.regs[vdp2SCYIN1]&0x7FF)<<8 | int32(v.frame.regs[vdp2SCYDN1]>>8)
		cfg.incXFP = int32(v.frame.regs[vdp2ZMXIN1]&0x07)<<8 | int32(v.frame.regs[vdp2ZMXDN1]>>8)
		cfg.incYFP = int32(v.frame.regs[vdp2ZMYIN1]&0x07)<<8 | int32(v.frame.regs[vdp2ZMYDN1]>>8)
		// ZMCTL reduction enable clamps horizontal increment
		zmctl := v.frame.regs[vdp2ZMCTL]
		zmhf1 := (zmctl >> 8) & 0x01
		zmqt1 := (zmctl >> 9) & 0x01
		var maxIncX1 int32
		switch {
		case zmqt1 != 0:
			maxIncX1 = 0x400
		case zmhf1 != 0:
			maxIncX1 = 0x200
		default:
			maxIncX1 = 0x100
		}
		if cfg.incXFP > maxIncX1 {
			cfg.incXFP = maxIncX1
		}
		cfg.priority = uint8(v.frame.regs[vdp2PRINA]>>8) & 0x07
		cfg.cramOffset = uint8(v.frame.regs[vdp2CRAOFA]>>4) & 0x07
	case 2:
		cfg.charSize1x1 = chctlb&0x01 == 0
		cfg.colorMode = uint8((chctlb >> 1) & 0x01) // NBG2: only 16 or 256
		cfg.pncnReg = v.frame.regs[vdp2PNCN2]
		cfg.mapOffset = uint32((v.frame.regs[vdp2MPOFN] >> 8) & 0x07)
		cfg.mapRegs[0] = uint8(v.frame.regs[vdp2MPABN2] & 0xFF)
		cfg.mapRegs[1] = uint8(v.frame.regs[vdp2MPABN2] >> 8)
		cfg.mapRegs[2] = uint8(v.frame.regs[vdp2MPCDN2] & 0xFF)
		cfg.mapRegs[3] = uint8(v.frame.regs[vdp2MPCDN2] >> 8)
		cfg.scrollXFP = int32(v.frame.regs[vdp2SCXN2]&0x7FF) << 8
		cfg.scrollYFP = int32(v.frame.regs[vdp2SCYN2]&0x7FF) << 8
		cfg.incXFP = 0x100
		cfg.incYFP = 0x100
		cfg.priority = uint8(v.frame.regs[vdp2PRINB]) & 0x07
		cfg.cramOffset = uint8(v.frame.regs[vdp2CRAOFA]>>8) & 0x07
	case 3:
		cfg.charSize1x1 = chctlb&0x10 == 0
		cfg.colorMode = uint8((chctlb >> 5) & 0x01) // NBG3: only 16 or 256
		cfg.pncnReg = v.frame.regs[vdp2PNCN3]
		cfg.mapOffset = uint32((v.frame.regs[vdp2MPOFN] >> 12) & 0x07)
		cfg.mapRegs[0] = uint8(v.frame.regs[vdp2MPABN3] & 0xFF)
		cfg.mapRegs[1] = uint8(v.frame.regs[vdp2MPABN3] >> 8)
		cfg.mapRegs[2] = uint8(v.frame.regs[vdp2MPCDN3] & 0xFF)
		cfg.mapRegs[3] = uint8(v.frame.regs[vdp2MPCDN3] >> 8)
		cfg.scrollXFP = int32(v.frame.regs[vdp2SCXN3]&0x7FF) << 8
		cfg.scrollYFP = int32(v.frame.regs[vdp2SCYN3]&0x7FF) << 8
		cfg.incXFP = 0x100
		cfg.incYFP = 0x100
		cfg.priority = uint8(v.frame.regs[vdp2PRINB]>>8) & 0x07
		cfg.cramOffset = uint8(v.frame.regs[vdp2CRAOFA]>>12) & 0x07
	}

	cfg.pnWord1 = cfg.pncnReg&0x8000 != 0
	cfg.auxMode1 = cfg.pncnReg&0x4000 != 0

	// Plane size from PLSZ register: 2 bits per screen
	plsz := (v.frame.regs[vdp2PLSZ] >> (uint(screen) * 2)) & 0x03
	cfg.planePagesH = 1
	cfg.planePagesV = 1
	if plsz&0x01 != 0 {
		cfg.planePagesH = 2
	}
	if plsz&0x02 != 0 {
		cfg.planePagesV = 2
	}

	// Mosaic from MZCTL register
	mzctl := v.frame.regs[vdp2MZCTL]
	if mzctl&(1<<uint(screen)) != 0 {
		cfg.mosaicH = int((mzctl>>8)&0xF) + 1
		cfg.mosaicV = int((mzctl>>12)&0xF) + 1
	}

	// Line scroll and vertical cell scroll (NBG0/NBG1 only)
	if screen <= 1 {
		scrctl := v.frame.regs[vdp2SCRCTL]
		shift := uint(screen) * 8
		// Bit layout per screen: bit+0=VCSC, +1=LSCX, +2=LSCY, +3=LZMX, +5:4=LSS
		cfg.vcscEnabled = scrctl&(1<<(shift+0)) != 0
		cfg.lineScrollX = scrctl&(1<<(shift+1)) != 0
		cfg.lineScrollY = scrctl&(1<<(shift+2)) != 0
		cfg.lineZoomX = scrctl&(1<<(shift+3)) != 0
		lss := (scrctl >> (shift + 4)) & 0x03
		cfg.lineInterval = 1 << lss

		if cfg.lineScrollX || cfg.lineScrollY || cfg.lineZoomX {
			var lstaU, lstaL uint16
			if screen == 0 {
				lstaU = v.frame.regs[vdp2LSTA0U]
				lstaL = v.frame.regs[vdp2LSTA0L]
			} else {
				lstaU = v.frame.regs[vdp2LSTA1U]
				lstaL = v.frame.regs[vdp2LSTA1L]
			}
			cfg.lineTableAddr = ((uint32(lstaU&0x07) << 16) | uint32(lstaL&0xFFFE)) * 2
		}

		if cfg.vcscEnabled {
			base := ((uint32(v.frame.regs[vdp2VCSTAU]&0x07) << 16) | uint32(v.frame.regs[vdp2VCSTAL]&0xFFFE)) * 2
			// PDF Sec 5.3 Figure 5.8: when both NBG0 and NBG1 VCSC are
			// enabled, entries interleave as {NBG0, NBG1} pairs with
			// stride 8. NBG1 entries start one longword into each pair.
			n0vcsc := scrctl&0x0001 != 0
			n1vcsc := scrctl&0x0100 != 0
			if n0vcsc && n1vcsc {
				cfg.vcscStride = 8
				if screen == 1 {
					base += 4
				}
			} else {
				cfg.vcscStride = 4
			}
			cfg.vcscTableAddr = base
		}
	}

	// Special priority and special color calculation modes
	sfprmd := v.frame.regs[vdp2SFPRMD]
	cfg.sfprmdMode = uint8((sfprmd >> (uint(screen) * 2)) & 0x03)
	sfccmd := v.frame.regs[vdp2SFCCMD]
	cfg.sfccmdMode = uint8((sfccmd >> (uint(screen) * 2)) & 0x03)

	return cfg
}

// decodePattern1Word extracts fields from a 1-word pattern name entry.
// The pattern-name control fields (pncnReg, colorMode, auxMode1,
// charSize1x1) are passed as scalars so both NBG and RBG screens share
// one decoder regardless of their distinct config struct types. For 2x2
// character mode (charSize1x1=false), the character number field is
// shifted left by 2 to make room for the sub-cell index, and the
// supplement character bits are split differently.
func decodePattern1Word(pn, pncnReg uint16, colorMode uint8, auxMode1, charSize1x1 bool) (charNum uint32, palette uint8, hflip, vflip bool) {
	suppPalette := uint8((pncnReg >> 5) & 0x07)
	suppChar := uint8(pncnReg & 0x1F)

	if colorMode == 0 {
		if !auxMode1 {
			// 16-color, aux=0
			palette = uint8(pn>>12) & 0x0F
			palette |= suppPalette << 4
			vflip = pn&0x0800 != 0
			hflip = pn&0x0400 != 0
			if charSize1x1 {
				charNum = uint32(pn & 0x03FF)
				charNum |= uint32(suppChar) << 10
			} else {
				charNum = uint32(pn&0x03FF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x1C) << 10
			}
		} else {
			// 16-color, aux=1
			palette = uint8(pn>>12) & 0x0F
			palette |= suppPalette << 4
			if charSize1x1 {
				charNum = uint32(pn & 0x0FFF)
				charNum |= uint32(suppChar&0x1C) << 10
			} else {
				charNum = uint32(pn&0x0FFF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x10) << 10
			}
		}
	} else {
		if !auxMode1 {
			// not-16-color, aux=0
			palette = (uint8(pn>>12) & 0x07) << 4
			vflip = pn&0x0800 != 0
			hflip = pn&0x0400 != 0
			if charSize1x1 {
				charNum = uint32(pn & 0x03FF)
				charNum |= uint32(suppChar) << 10
			} else {
				charNum = uint32(pn&0x03FF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x1C) << 10
			}
		} else {
			// not-16-color, aux=1
			palette = (uint8(pn>>12) & 0x07) << 4
			if charSize1x1 {
				charNum = uint32(pn & 0x0FFF)
				charNum |= uint32(suppChar&0x1C) << 10
			} else {
				charNum = uint32(pn&0x0FFF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x10) << 10
			}
		}
	}
	return
}

// decodePattern2Word extracts fields from a 2-word pattern name entry.
// MSW bit 13 = special priority bit, bit 12 = special color calculation bit.
func decodePattern2Word(msw, lsw uint16) (charNum uint32, palette uint8, hflip, vflip, specialPri, specialCC bool) {
	vflip = msw&0x8000 != 0
	hflip = msw&0x4000 != 0
	specialPri = msw&0x2000 != 0
	specialCC = msw&0x1000 != 0
	palette = uint8(msw & 0x7F)
	charNum = uint32(lsw & 0x7FFF)
	return
}

// sfcodeMatches returns true if the dot color's lower 4 bits fall in a
// nibble-pair enabled by the 8-bit sfcode register. Each bit N of sfcode
// enables dots where (dotColor & 0xF) >> 1 == N (pairs {0,1}, {2,3}, ...).
func sfcodeMatches(sfcode, dotColor uint8) bool {
	return sfcode&(1<<((dotColor&0x0F)>>1)) != 0
}

// sfcodeForScreen returns the 8-bit special function code for the given screen.
// SFSEL selects code A (bits 7:0) or code B (bits 15:8) per screen.
func (v *VDP2) sfcodeForScreen(screen int) uint8 {
	sfsel := v.frame.regs[vdp2SFSEL]
	sfcode := v.frame.regs[vdp2SFCODE]
	if sfsel&(1<<uint(screen)) != 0 {
		return uint8(sfcode >> 8)
	}
	return uint8(sfcode)
}

// nbgSpanSetup prepares row y of an NBG scroll screen for span
// rendering, dispatching on the configured cell or bitmap mode. It
// performs the per-line setup (line scroll table reads, derived
// addressing constants) and returns a function that renders pixels
// [x0,x1) of the row into buf. y is a field-line index in
// 0..activeLines-1; buf rows are activeWidth pixels.
func (v *VDP2) nbgSpanSetup(buf []uint32, cfg *nbgConfig, screen, y int) func(x0, x1 int) {
	if cfg.bitmapMode {
		return v.nbgBitmapSpanSetup(buf, cfg, screen, y)
	}
	return v.nbgCellSpanSetup(buf, cfg, screen, y)
}

// nbgCellSpanSetup prepares row y of a cell-mode NBG scroll screen.
func (v *VDP2) nbgCellSpanSetup(buf []uint32, cfg *nbgConfig, screen, y int) func(x0, x1 int) {
	width := v.frame.width

	// Character address unit: always 0x20 bytes per character number.
	// The actual pixel data size varies by color mode, but the character
	// NUMBER indexes in 0x20-byte units regardless of depth.
	var cellBytes uint32 = 0x20

	// Sub-cell scale for 2x2 characters: consecutive sub-cells must skip
	// by the actual cell data size divided by 0x20.
	var subCellScale uint32 = 1
	switch cfg.colorMode {
	case 1:
		subCellScale = 2
	case 2, 3:
		subCellScale = 4
	case 4:
		subCellScale = 8
	}

	var entrySize uint32
	if cfg.pnWord1 {
		entrySize = 2
	} else {
		entrySize = 4
	}

	// Character size: 1x1 = 8x8 pixels (64x64 entries/page)
	//                 2x2 = 16x16 pixels (32x32 entries/page)
	charPx := 8
	mapDim := 64
	if !cfg.charSize1x1 {
		charPx = 16
		mapDim = 32
	}
	charMask := charPx - 1
	mapMask := mapDim - 1
	pageBoundary := uint32(mapDim*mapDim) * entrySize
	planeCellsH := mapDim * cfg.planePagesH
	planeCellsV := mapDim * cfg.planePagesV
	planeSizeH := planeCellsH * charPx
	planeSizeV := planeCellsV * charPx

	hasLineScroll := cfg.lineScrollX || cfg.lineScrollY || cfg.lineZoomX

	// Per-line scroll overrides. Per VDP2 manual section 5.3, line
	// scroll table values are relative and are added to the base
	// scroll registers. The vertical coordinate increment register
	// is only applied when the line interval is two lines or greater
	// (LSS >= 1); within a single-line entry it contributes zero.
	lineScrollXFP := cfg.scrollXFP
	lineScrollYFP := v.frame.nbgYBaseFP[screen]
	lineIncXFP := cfg.incXFP
	if hasLineScroll {
		srcY := y
		if v.frame.lsmd3 {
			srcY = y*2 + v.frame.field
		}
		lineIdx := srcY / cfg.lineInterval
		entryStride := uint32(0)
		if cfg.lineScrollX {
			entryStride += 4
		}
		if cfg.lineScrollY {
			entryStride += 4
		}
		if cfg.lineZoomX {
			entryStride += 4
		}
		tableOff := cfg.lineTableAddr + uint32(lineIdx)*entryStride
		fieldOff := uint32(0)
		if cfg.lineScrollX {
			hi := v.readVRAM16(tableOff + fieldOff)
			lo := v.readVRAM16(tableOff + fieldOff + 2)
			lsxDelta := int32(hi&0x07FF)<<8 | int32(lo>>8)
			lineScrollXFP = cfg.scrollXFP + lsxDelta
			fieldOff += 4
		}
		if cfg.lineScrollY {
			hi := v.readVRAM16(tableOff + fieldOff)
			lo := v.readVRAM16(tableOff + fieldOff + 2)
			lsyDelta := int32(hi&0x07FF)<<8 | int32(lo>>8)
			lineScrollYFP = v.frame.nbgYBaseFP[screen] + lsyDelta
			fieldOff += 4
		}
		if cfg.lineZoomX {
			hi := v.readVRAM16(tableOff + fieldOff)
			lo := v.readVRAM16(tableOff + fieldOff + 2)
			lineIncXFP = int32(hi&0x07)<<8 | int32(lo>>8)
		}
	}

	// Source-Y for cell-pattern lookup uses displayed-line index so
	// the pattern advances per displayed scanline. Under LSMD=3 the
	// loop's y is field-line; convert.
	dispY := y
	if v.frame.lsmd3 {
		dispY = y*2 + v.frame.field
	}
	ey := dispY
	if cfg.mosaicV > 0 {
		ey = (dispY / cfg.mosaicV) * cfg.mosaicV
	}
	// For multi-line entries (LSS >= 1) the coordinate increment
	// advances Y within the current entry. For 1-line entries or
	// when line scroll is disabled, ey is used directly.
	yAdvance := int32(ey)
	if hasLineScroll && cfg.lineScrollY && cfg.lineInterval > 1 {
		yAdvance = int32(ey % cfg.lineInterval)
	} else if hasLineScroll && cfg.lineScrollY {
		yAdvance = 0
	}
	baseSyFP := lineScrollYFP + yAdvance*cfg.incYFP

	// The divide-heavy plane/page addressing and the pattern-name
	// fetch/decode depend only on the cell coordinates (sx/charPx,
	// sy/charPx). Within a run of pixels mapping to the same cell
	// they are identical, so recompute only when the cell changes.
	haveCell := false
	var pSxCell, pSyCell int
	var cCharNum uint32
	var cPalette uint8
	var cHflip, cVflip bool
	var cSpecialPriBit, cSpecialCCBit bool

	return func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			ex := x
			if cfg.mosaicH > 0 {
				ex = (x / cfg.mosaicH) * cfg.mosaicH
			}

			// Per-column vertical cell scroll offset
			syFP := baseSyFP
			if cfg.vcscEnabled {
				col := ex / 8
				vcOff := cfg.vcscTableAddr + uint32(col)*cfg.vcscStride
				hi := v.readVRAM16(vcOff)
				lo := v.readVRAM16(vcOff + 2)
				vcscFP := int32(hi&0x07FF)<<8 | int32(lo>>8)
				syFP += vcscFP
			}
			sy := int(syFP >> 8)
			dotY := sy & charMask

			sxFP := lineScrollXFP + int32(ex)*lineIncXFP
			sx := int(sxFP >> 8)
			dotX := sx & charMask

			sxCell := sx / charPx
			syCell := sy / charPx

			if !haveCell || sxCell != pSxCell || syCell != pSyCell {
				planeV := (sy / planeSizeV) & 1
				planeCellY := (sy / charPx) % planeCellsV
				planeH := (sx / planeSizeH) & 1
				planeCellX := (sx / charPx) % planeCellsH

				// Page within the plane
				pageX := planeCellX / mapDim
				pageY := planeCellY / mapDim
				localCellX := planeCellX & mapMask
				localCellY := planeCellY & mapMask

				planeIdx := planeV*2 + planeH
				// Map offset provides upper bits for the pattern name table address.
				combinedOffset := uint32(cfg.mapRegs[planeIdx]&0x3F) | (cfg.mapOffset << 6)
				planeBase := combinedOffset * pageBoundary
				pageOffset := uint32(pageY*cfg.planePagesH+pageX) * pageBoundary
				entryOffset := uint32(localCellY*mapDim+localCellX) * entrySize
				patternAddr := planeBase + pageOffset + entryOffset

				if cfg.pnWord1 {
					pn := v.readVRAM16(patternAddr)
					cCharNum, cPalette, cHflip, cVflip = decodePattern1Word(pn, cfg.pncnReg, cfg.colorMode, cfg.auxMode1, cfg.charSize1x1)
					cSpecialPriBit = cfg.pncnReg&(1<<9) != 0
					cSpecialCCBit = cfg.pncnReg&(1<<8) != 0
				} else {
					msw := v.readVRAM16(patternAddr)
					lsw := v.readVRAM16(patternAddr + 2)
					cCharNum, cPalette, cHflip, cVflip, cSpecialPriBit, cSpecialCCBit = decodePattern2Word(msw, lsw)
				}

				pSxCell = sxCell
				pSyCell = syCell
				haveCell = true
			}

			charNum := cCharNum
			palette := cPalette
			hflip := cHflip
			vflip := cVflip
			specialPriBit := cSpecialPriBit
			specialCCBit := cSpecialCCBit

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
				// Sub-cell: TL=+0, TR=+1, BL=+2, BR=+3
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
			case 4: // 16.7M (32bpp RGB direct)
				var op bool
				r, g, b, op = v.readCellPixel32bpp(cellAddr, dx, dy)
				if !op && !cfg.transpOff {
					transp = true
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
				// Per manual Table 11.2 mode 2 sets the priority LSB only on
				// dots whose pattern-name special priority bit is 1 and whose
				// color code matches the special function code. RGB formats
				// (colorMode 3 and 4) have no color code, so mode 2 is invalid
				// there and the LSB is left unchanged.
				if cfg.colorMode < 3 {
					if specialPriBit && sfcodeMatches(v.sfcodeForScreen(screen), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable. Per manual Table
			// 12.3 the per-screen color calculation enable bit (CCCTL) is a
			// precondition for every mode; the per-character/dot/MSB term
			// only further restricts it.
			var ccEnabled bool
			screenCC := isCCEnabled(v.frame.regs[vdp2CCCTL], screen)
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = screenCC
			case 1:
				ccEnabled = screenCC && specialCCBit
			case 2:
				// Mode 2 also requires the pattern-name special CC bit and a
				// special-function-code match. RGB formats (colorMode 3 and 4)
				// have no color code, so mode 2 is invalid there.
				if cfg.colorMode < 3 {
					ccEnabled = screenCC && specialCCBit && sfcodeMatches(v.sfcodeForScreen(screen), dotColor)
				}
			case 3:
				// Palette formats gate on the CRAM color MSB; both RGB formats
				// (colorMode 3 and 4) calculate whenever the screen enable bit
				// is set.
				if cfg.colorMode >= 3 {
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

// nbgBitmapSpanSetup prepares row y of a bitmap-mode scroll screen.
func (v *VDP2) nbgBitmapSpanSetup(buf []uint32, cfg *nbgConfig, screen, y int) func(x0, x1 int) {
	width := v.frame.width

	// Bitmap base address: mapOffset (from MPOFN) * 0x20000
	baseAddr := cfg.mapOffset * 0x20000

	// cfg.bmpPalette is the 7-bit palette pre-shifted at setup;
	// lookupColor handles per-mode CRAM-index math.
	palette := cfg.bmpPalette

	hasLineScroll := cfg.lineScrollX || cfg.lineScrollY || cfg.lineZoomX

	// Source-Y for bitmap sampling uses displayed-line index so the
	// pattern advances per displayed scanline. Under LSMD=3 the
	// loop's y is field-line; convert.
	dispY := y
	if v.frame.lsmd3 {
		dispY = y*2 + v.frame.field
	}
	ey := dispY
	if cfg.mosaicV > 0 {
		ey = (dispY / cfg.mosaicV) * cfg.mosaicV
	}

	// Per-line scroll (bitmap mode). Per VDP2 manual section 5.3,
	// line scroll table values are relative and are added to the
	// base scroll registers. The vertical coordinate increment
	// register is only applied when the line interval is two lines
	// or greater; within a single-line entry it contributes zero.
	//
	// For 32bpp NiGHTS-style stride compensation, the X sum needs
	// to be wrapped to 11 bits and sign-extended so negative scroll
	// values (e.g. scrollX=2032 meaning -16) correctly place pixels
	// near the bitmap origin rather than at an aliased position.
	lsxDelta := int32(0)
	lsyDelta := int32(0)
	lineIncXFP := cfg.incXFP
	if hasLineScroll {
		srcY := y
		if v.frame.lsmd3 {
			srcY = y*2 + v.frame.field
		}
		lineIdx := srcY / cfg.lineInterval
		entryStride := uint32(0)
		if cfg.lineScrollX {
			entryStride += 4
		}
		if cfg.lineScrollY {
			entryStride += 4
		}
		if cfg.lineZoomX {
			entryStride += 4
		}
		tableOff := cfg.lineTableAddr + uint32(lineIdx)*entryStride
		fieldOff := uint32(0)
		if cfg.lineScrollX {
			hi := v.readVRAM16(tableOff + fieldOff)
			lo := v.readVRAM16(tableOff + fieldOff + 2)
			lsxDelta = int32(hi&0x07FF)<<8 | int32(lo>>8)
			fieldOff += 4
		}
		if cfg.lineScrollY {
			hi := v.readVRAM16(tableOff + fieldOff)
			lo := v.readVRAM16(tableOff + fieldOff + 2)
			lsyDelta = int32(hi&0x07FF)<<8 | int32(lo>>8)
			fieldOff += 4
		}
		if cfg.lineZoomX {
			hi := v.readVRAM16(tableOff + fieldOff)
			lo := v.readVRAM16(tableOff + fieldOff + 2)
			lineIncXFP = int32(hi&0x07)<<8 | int32(lo>>8)
		}
	}

	// Compute the effective starting scroll X for this line. Sum
	// the base scroll and per-line delta as 11-bit integer + 8-bit
	// fractional, wrap the integer to 11 bits, then sign-extend.
	rawScrollXInt := (cfg.scrollXFP >> 8) + (lsxDelta >> 8)
	rawScrollXFrac := (cfg.scrollXFP & 0xFF) + (lsxDelta & 0xFF)
	rawScrollXInt += rawScrollXFrac >> 8
	rawScrollXInt &= 0x7FF
	if rawScrollXInt >= 0x400 {
		rawScrollXInt -= 0x800 // sign-extend 11-bit signed
	}
	lineScrollXFP := (rawScrollXInt << 8) | (rawScrollXFrac & 0xFF)

	// Y coordinate: base + per-line delta, plus the coordinate
	// increment advance (only used for multi-line intervals).
	yAdvance := int32(ey)
	if hasLineScroll && cfg.lineScrollY && cfg.lineInterval > 1 {
		yAdvance = int32(ey % cfg.lineInterval)
	} else if hasLineScroll && cfg.lineScrollY {
		yAdvance = 0
	}
	syFP := v.frame.nbgYBaseFP[screen] + lsyDelta + yAdvance*cfg.incYFP
	sy := int(syFP >> 8)
	if cfg.bmpHeight > 0 {
		sy = sy % cfg.bmpHeight
		if sy < 0 {
			sy += cfg.bmpHeight
		}
	}
	return func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			ex := x
			if cfg.mosaicH > 0 {
				ex = (x / cfg.mosaicH) * cfg.mosaicH
			}
			sxFP := lineScrollXFP + int32(ex)*lineIncXFP
			// For 32bpp (16.7M color) bitmap mode, read linearly from
			// VRAM without horizontal wrap so that line-scroll-based
			// stride compensation works correctly.
			var sx int
			if cfg.colorMode == 4 {
				sx = int(sxFP >> 8)
			} else {
				sx = int(sxFP>>8) % cfg.bmpWidth
				if sx < 0 {
					sx += cfg.bmpWidth
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
				pixOff := baseAddr + uint32(sy*(cfg.bmpWidth/2)+sx/2)
				raw := v.vram[pixOff&(vdp2VRAMSize-1)]
				var dot uint8
				if sx&1 == 0 {
					dot = raw >> 4
				} else {
					dot = raw & 0x0F
				}
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
			case 1: // 256-color (8bpp)
				pixOff := baseAddr + uint32(sy*cfg.bmpWidth+sx)
				dot := v.vram[pixOff&(vdp2VRAMSize-1)]
				dotColor = dot
				r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
			case 2: // 2048-color (16bpp palette)
				pixOff := baseAddr + uint32((sy*cfg.bmpWidth+sx)*2)
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
			case 3: // 32768-color (16bpp RGB direct)
				pixOff := baseAddr + uint32((sy*cfg.bmpWidth+sx)*2)
				hi := v.vram[pixOff&(vdp2VRAMSize-1)]
				lo := v.vram[(pixOff+1)&(vdp2VRAMSize-1)]
				raw := uint16(hi)<<8 | uint16(lo)
				if raw&0x8000 == 0 && !cfg.transpOff {
					transp = true
				} else {
					r, g, b = rgb555ToRGB(raw)
				}
			case 4: // 16.7M (32bpp RGB direct)
				pixOff := baseAddr + uint32((sy*cfg.bmpWidth+sx)*4)
				w0 := v.readVRAM16(pixOff)
				w1 := v.readVRAM16(pixOff + 2)
				b = uint8(w0)
				g = uint8(w1 >> 8)
				r = uint8(w1)
				if w0&0x8000 == 0 && !cfg.transpOff {
					transp = true
				}
			}

			// Compute effective priority with special priority function. In
			// bitmap mode the special priority bit comes from the bitmap
			// number register (bmpSpecialPri), not pattern name data.
			priority := cfg.priority
			switch cfg.sfprmdMode {
			case 1:
				if cfg.bmpSpecialPri {
					priority = (priority & 0xFE) | 1
				} else {
					priority &= 0xFE
				}
			case 2:
				// Per manual Table 11.2 mode 2 sets the priority LSB only on
				// dots whose special priority bit is 1 and whose color code
				// matches the special function code. RGB formats (colorMode 3
				// and 4) have no color code, so mode 2 is invalid there.
				if cfg.colorMode < 3 {
					if cfg.bmpSpecialPri && sfcodeMatches(v.sfcodeForScreen(screen), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable. Per manual Table
			// 12.3 the per-screen color calculation enable bit (CCCTL) is a
			// precondition for every mode. In bitmap mode the special CC bit
			// comes from the bitmap number register (bmpSpecialCC).
			var ccEnabled bool
			screenCC := isCCEnabled(v.frame.regs[vdp2CCCTL], screen)
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = screenCC
			case 1:
				ccEnabled = screenCC && cfg.bmpSpecialCC
			case 2:
				// Mode 2 also requires the bitmap special CC bit and a
				// special-function-code match. RGB formats (colorMode 3 and 4)
				// have no color code, so mode 2 is invalid there.
				if cfg.colorMode < 3 {
					ccEnabled = screenCC && cfg.bmpSpecialCC && sfcodeMatches(v.sfcodeForScreen(screen), dotColor)
				}
			case 3:
				// Palette formats gate on the CRAM color MSB; both RGB formats
				// (colorMode 3 and 4) calculate whenever the screen enable bit
				// is set.
				if cfg.colorMode >= 3 {
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
