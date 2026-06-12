// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// layerCCBit is packed into bit 27 of layerBuf/rbg0Buf entries to indicate
// per-pixel color calculation enable. Priority occupies bits 26:24 (3 bits).
const layerCCBit = 1 << 27

// rgb555ToRGBA converts a Saturn RGB555 color value to RGBA8888 components.
// Bit layout: bit 15 ignored, bits 14-10 blue, bits 9-5 green, bits 4-0 red.
func rgb555ToRGBA(val uint16) (r, g, b, a uint8) {
	r5 := uint8(val & 0x1F)
	g5 := uint8((val >> 5) & 0x1F)
	b5 := uint8((val >> 10) & 0x1F)
	return (r5 << 3) | (r5 >> 2),
		(g5 << 3) | (g5 >> 2),
		(b5 << 3) | (b5 >> 2),
		0xFF
}

// readVRAM16 reads a big-endian 16-bit value from VDP2 VRAM at the given byte address.
func (v *VDP2) readVRAM16(addr uint32) uint16 {
	addr &= vdp2VRAMSize - 1
	hi := v.vram[addr]
	lo := v.vram[(addr+1)&(vdp2VRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
}

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

// readCRAM16 reads a big-endian 16-bit value from VDP2 CRAM.
func (v *VDP2) readCRAM16(addr uint32) uint16 {
	addr &= vdp2CRAMSize - 1
	hi := v.cram[addr]
	lo := v.cram[(addr+1)&(vdp2CRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
}

// decodeNBGConfig reads registers for the given screen (0-3) and fills nbgConfig.
func (v *VDP2) decodeNBGConfig(screen int) nbgConfig {
	var cfg nbgConfig
	bgon := v.regs[vdp2BGON]
	cfg.enabled = bgon&(1<<uint(screen)) != 0
	cfg.transpOff = bgon&(1<<uint(8+screen)) != 0

	chctla := v.regs[vdp2CHCTLA]
	chctlb := v.regs[vdp2CHCTLB]

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
		bmpna := v.regs[vdp2BMPNA]
		// BMP6-4 (palette top 3 bits) at BMPNA bits 2:0 per PDF Sec 4.10.
		// Pre-shifted to bits 6:4 so cfg.bmpPalette is the 7-bit palette
		// number with low 4 bits zero, usable by lookupColor in any mode.
		cfg.bmpPalette = uint8(bmpna&0x07) << 4
		cfg.bmpSpecialPri = bmpna&(1<<5) != 0
		cfg.bmpSpecialCC = bmpna&(1<<4) != 0
		cfg.pncnReg = v.regs[vdp2PNCN0]
		cfg.mapOffset = uint32(v.regs[vdp2MPOFN]) & 0x07
		cfg.mapRegs[0] = uint8(v.regs[vdp2MPABN0] & 0xFF)
		cfg.mapRegs[1] = uint8(v.regs[vdp2MPABN0] >> 8)
		cfg.mapRegs[2] = uint8(v.regs[vdp2MPCDN0] & 0xFF)
		cfg.mapRegs[3] = uint8(v.regs[vdp2MPCDN0] >> 8)
		cfg.scrollXFP = int32(v.regs[vdp2SCXIN0]&0x7FF)<<8 | int32(v.regs[vdp2SCXDN0]>>8)
		cfg.scrollYFP = int32(v.regs[vdp2SCYIN0]&0x7FF)<<8 | int32(v.regs[vdp2SCYDN0]>>8)
		cfg.incXFP = int32(v.regs[vdp2ZMXIN0]&0x07)<<8 | int32(v.regs[vdp2ZMXDN0]>>8)
		cfg.incYFP = int32(v.regs[vdp2ZMYIN0]&0x07)<<8 | int32(v.regs[vdp2ZMYDN0]>>8)
		if cfg.incXFP == 0 {
			cfg.incXFP = 0x100
		}
		if cfg.incYFP == 0 {
			cfg.incYFP = 0x100
		}
		// ZMCTL reduction enable clamps horizontal increment
		zmctl := v.regs[vdp2ZMCTL]
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
		cfg.priority = uint8(v.regs[vdp2PRINA]) & 0x07
		cfg.cramOffset = uint8(v.regs[vdp2CRAOFA]) & 0x07
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
		bmpna := v.regs[vdp2BMPNA]
		// BMP6-4 at BMPNA bits 10:8 per PDF Sec 4.10.
		cfg.bmpPalette = uint8((bmpna>>8)&0x07) << 4
		cfg.bmpSpecialPri = bmpna&(1<<13) != 0
		cfg.bmpSpecialCC = bmpna&(1<<12) != 0
		cfg.pncnReg = v.regs[vdp2PNCN1]
		cfg.mapOffset = uint32((v.regs[vdp2MPOFN] >> 4) & 0x07)
		cfg.mapRegs[0] = uint8(v.regs[vdp2MPABN1] & 0xFF)
		cfg.mapRegs[1] = uint8(v.regs[vdp2MPABN1] >> 8)
		cfg.mapRegs[2] = uint8(v.regs[vdp2MPCDN1] & 0xFF)
		cfg.mapRegs[3] = uint8(v.regs[vdp2MPCDN1] >> 8)
		cfg.scrollXFP = int32(v.regs[vdp2SCXIN1]&0x7FF)<<8 | int32(v.regs[vdp2SCXDN1]>>8)
		cfg.scrollYFP = int32(v.regs[vdp2SCYIN1]&0x7FF)<<8 | int32(v.regs[vdp2SCYDN1]>>8)
		cfg.incXFP = int32(v.regs[vdp2ZMXIN1]&0x07)<<8 | int32(v.regs[vdp2ZMXDN1]>>8)
		cfg.incYFP = int32(v.regs[vdp2ZMYIN1]&0x07)<<8 | int32(v.regs[vdp2ZMYDN1]>>8)
		if cfg.incXFP == 0 {
			cfg.incXFP = 0x100
		}
		if cfg.incYFP == 0 {
			cfg.incYFP = 0x100
		}
		// ZMCTL reduction enable clamps horizontal increment
		zmctl := v.regs[vdp2ZMCTL]
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
		cfg.priority = uint8(v.regs[vdp2PRINA]>>8) & 0x07
		cfg.cramOffset = uint8(v.regs[vdp2CRAOFA]>>4) & 0x07
	case 2:
		cfg.charSize1x1 = chctlb&0x01 == 0
		cfg.colorMode = uint8((chctlb >> 1) & 0x01) // NBG2: only 16 or 256
		cfg.pncnReg = v.regs[vdp2PNCN2]
		cfg.mapOffset = uint32((v.regs[vdp2MPOFN] >> 8) & 0x07)
		cfg.mapRegs[0] = uint8(v.regs[vdp2MPABN2] & 0xFF)
		cfg.mapRegs[1] = uint8(v.regs[vdp2MPABN2] >> 8)
		cfg.mapRegs[2] = uint8(v.regs[vdp2MPCDN2] & 0xFF)
		cfg.mapRegs[3] = uint8(v.regs[vdp2MPCDN2] >> 8)
		cfg.scrollXFP = int32(v.regs[vdp2SCXN2]&0x7FF) << 8
		cfg.scrollYFP = int32(v.regs[vdp2SCYN2]&0x7FF) << 8
		cfg.incXFP = 0x100
		cfg.incYFP = 0x100
		cfg.priority = uint8(v.regs[vdp2PRINB]) & 0x07
		cfg.cramOffset = uint8(v.regs[vdp2CRAOFA]>>8) & 0x07
	case 3:
		cfg.charSize1x1 = chctlb&0x10 == 0
		cfg.colorMode = uint8((chctlb >> 5) & 0x01) // NBG3: only 16 or 256
		cfg.pncnReg = v.regs[vdp2PNCN3]
		cfg.mapOffset = uint32((v.regs[vdp2MPOFN] >> 12) & 0x07)
		cfg.mapRegs[0] = uint8(v.regs[vdp2MPABN3] & 0xFF)
		cfg.mapRegs[1] = uint8(v.regs[vdp2MPABN3] >> 8)
		cfg.mapRegs[2] = uint8(v.regs[vdp2MPCDN3] & 0xFF)
		cfg.mapRegs[3] = uint8(v.regs[vdp2MPCDN3] >> 8)
		cfg.scrollXFP = int32(v.regs[vdp2SCXN3]&0x7FF) << 8
		cfg.scrollYFP = int32(v.regs[vdp2SCYN3]&0x7FF) << 8
		cfg.incXFP = 0x100
		cfg.incYFP = 0x100
		cfg.priority = uint8(v.regs[vdp2PRINB]>>8) & 0x07
		cfg.cramOffset = uint8(v.regs[vdp2CRAOFA]>>12) & 0x07
	}

	cfg.pnWord1 = cfg.pncnReg&0x8000 != 0
	cfg.auxMode1 = cfg.pncnReg&0x4000 != 0

	// Plane size from PLSZ register: 2 bits per screen
	plsz := (v.regs[vdp2PLSZ] >> (uint(screen) * 2)) & 0x03
	cfg.planePagesH = 1
	cfg.planePagesV = 1
	if plsz&0x01 != 0 {
		cfg.planePagesH = 2
	}
	if plsz&0x02 != 0 {
		cfg.planePagesV = 2
	}

	// Mosaic from MZCTL register
	mzctl := v.regs[vdp2MZCTL]
	if mzctl&(1<<uint(screen)) != 0 {
		cfg.mosaicH = int((mzctl>>8)&0xF) + 1
		cfg.mosaicV = int((mzctl>>12)&0xF) + 1
	}

	// Line scroll and vertical cell scroll (NBG0/NBG1 only)
	if screen <= 1 {
		scrctl := v.regs[vdp2SCRCTL]
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
				lstaU = v.regs[vdp2LSTA0U]
				lstaL = v.regs[vdp2LSTA0L]
			} else {
				lstaU = v.regs[vdp2LSTA1U]
				lstaL = v.regs[vdp2LSTA1L]
			}
			cfg.lineTableAddr = ((uint32(lstaU&0x07) << 16) | uint32(lstaL&0xFFFE)) * 2
		}

		if cfg.vcscEnabled {
			base := ((uint32(v.regs[vdp2VCSTAU]&0x07) << 16) | uint32(v.regs[vdp2VCSTAL]&0xFFFE)) * 2
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
	sfprmd := v.regs[vdp2SFPRMD]
	cfg.sfprmdMode = uint8((sfprmd >> (uint(screen) * 2)) & 0x03)
	sfccmd := v.regs[vdp2SFCCMD]
	cfg.sfccmdMode = uint8((sfccmd >> (uint(screen) * 2)) & 0x03)

	return cfg
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
	sfsel := v.regs[vdp2SFSEL]
	sfcode := v.regs[vdp2SFCODE]
	if sfsel&(1<<uint(screen)) != 0 {
		return uint8(sfcode >> 8)
	}
	return uint8(sfcode)
}

// decodePattern1Word extracts fields from a 1-word pattern name entry.
// For 2x2 character mode (charSize1x1=false), the character number field
// is shifted left by 2 to make room for the sub-cell index, and the
// supplement character bits are split differently.
func decodePattern1Word(pn uint16, cfg *nbgConfig) (charNum uint32, palette uint8, hflip, vflip bool) {
	suppPalette := uint8((cfg.pncnReg >> 5) & 0x07)
	suppChar := uint8(cfg.pncnReg & 0x1F)

	if cfg.colorMode == 0 {
		if !cfg.auxMode1 {
			// 16-color, aux=0
			palette = uint8(pn>>12) & 0x0F
			palette |= suppPalette << 4
			vflip = pn&0x0800 != 0
			hflip = pn&0x0400 != 0
			if cfg.charSize1x1 {
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
			if cfg.charSize1x1 {
				charNum = uint32(pn & 0x0FFF)
				charNum |= uint32(suppChar&0x1C) << 10
			} else {
				charNum = uint32(pn&0x0FFF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x10) << 10
			}
		}
	} else {
		if !cfg.auxMode1 {
			// not-16-color, aux=0
			palette = (uint8(pn>>12) & 0x07) << 4
			vflip = pn&0x0800 != 0
			hflip = pn&0x0400 != 0
			if cfg.charSize1x1 {
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
			if cfg.charSize1x1 {
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

// readCellPixel4bpp reads one 4-bit pixel from a 4bpp cell in VRAM.
func (v *VDP2) readCellPixel4bpp(cellAddr uint32, dotX, dotY int) uint8 {
	offset := cellAddr + uint32(dotY*4+dotX/2)
	b := v.vram[offset&(vdp2VRAMSize-1)]
	if dotX&1 == 0 {
		return b >> 4
	}
	return b & 0x0F
}

// readCellPixel8bpp reads one 8-bit pixel from an 8bpp cell in VRAM.
func (v *VDP2) readCellPixel8bpp(cellAddr uint32, dotX, dotY int) uint8 {
	offset := cellAddr + uint32(dotY*8+dotX)
	return v.vram[offset&(vdp2VRAMSize-1)]
}

// readCellPixel16bpp reads one 16-bit pixel from a 16bpp cell in VRAM.
func (v *VDP2) readCellPixel16bpp(cellAddr uint32, dotX, dotY int) uint16 {
	offset := cellAddr + uint32((dotY*8+dotX)*2)
	hi := v.vram[offset&(vdp2VRAMSize-1)]
	lo := v.vram[(offset+1)&(vdp2VRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
}

// readCellPixel32bpp reads one 32-bit pixel from a 32bpp cell in VRAM.
// Returns R, G, B components (8-bit each) and opaque, which is the
// transparency bit: per VDP2 manual Figure 4.6 an RGB-format dot is a
// transparent dot when the most significant bit (bit 31) is 0.
func (v *VDP2) readCellPixel32bpp(cellAddr uint32, dotX, dotY int) (r, g, b uint8, opaque bool) {
	offset := cellAddr + uint32((dotY*8+dotX)*4)
	// Word 0: bit 15 = transparency bit, bits 7:0 = Blue
	// Word 1: bits 15:8 = Green, bits 7:0 = Red
	w0hi := v.vram[offset&(vdp2VRAMSize-1)]
	w0lo := v.vram[(offset+1)&(vdp2VRAMSize-1)]
	w1hi := v.vram[(offset+2)&(vdp2VRAMSize-1)]
	w1lo := v.vram[(offset+3)&(vdp2VRAMSize-1)]
	opaque = w0hi&0x80 != 0
	b = w0lo
	g = w1hi
	r = w1lo
	return
}

// buildCRAMCache decodes every CRAM entry for the current CRAM mode into
// the per-frame RGB/CC tables. Called once at the top of RenderFrame so
// the per-pixel color path becomes a table index. Uses the same decode
// as readCRAMColor/readCRAMColorWithCC, so cached output is identical.
func (v *VDP2) buildCRAMCache() {
	cm := v.cramMode()
	var entries uint32
	switch cm {
	case 1:
		entries = 2048
		v.cramCacheMask = 2047
	default:
		entries = 1024
		v.cramCacheMask = 1023
	}
	for i := uint32(0); i < entries; i++ {
		switch cm {
		case 0, 1:
			val := v.readCRAM16(i * 2)
			r, g, b, _ := rgb555ToRGBA(val)
			v.cramCacheR[i] = r
			v.cramCacheG[i] = g
			v.cramCacheB[i] = b
			v.cramCacheCC[i] = val&0x8000 != 0
		default: // Mode 2/3: RGB888
			hiWord := v.readCRAM16(i * 4)
			loWord := v.readCRAM16(i*4 + 2)
			v.cramCacheR[i] = uint8(loWord)
			v.cramCacheG[i] = uint8(loWord >> 8)
			v.cramCacheB[i] = uint8(hiWord)
			v.cramCacheCC[i] = hiWord&0x8000 != 0
		}
	}
	v.cramCacheValid = true
}

// readCRAMColor reads a color entry from CRAM based on the current CRAM mode.
// colorAddr is the entry index (not byte offset).
func (v *VDP2) readCRAMColor(colorAddr uint32) (r, g, b uint8) {
	if v.cramCacheValid {
		i := colorAddr & v.cramCacheMask
		return v.cramCacheR[i], v.cramCacheG[i], v.cramCacheB[i]
	}
	cm := v.cramMode()
	switch cm {
	case 0: // RGB555, 1024 entries (mirrored)
		colorAddr &= 1023
		val := v.readCRAM16(colorAddr * 2)
		r, g, b, _ = rgb555ToRGBA(val)
	case 1: // RGB555, 2048 entries
		colorAddr &= 2047
		val := v.readCRAM16(colorAddr * 2)
		r, g, b, _ = rgb555ToRGBA(val)
	default: // Mode 2/3: RGB888, 1024 entries
		colorAddr &= 1023
		hiWord := v.readCRAM16(colorAddr * 4)
		loWord := v.readCRAM16(colorAddr*4 + 2)
		r = uint8(loWord)
		g = uint8(loWord >> 8)
		b = uint8(hiWord)
	}
	return
}

// readCRAMColorWithCC reads a CRAM entry and also returns the CC bit (MSB).
// In modes 0/1 (RGB555): CC bit is bit 15 of the 16-bit CRAM word.
// In mode 2 (RGB888): CC bit is bit 15 of the upper word.
func (v *VDP2) readCRAMColorWithCC(colorAddr uint32) (r, g, b uint8, ccBit bool) {
	if v.cramCacheValid {
		i := colorAddr & v.cramCacheMask
		return v.cramCacheR[i], v.cramCacheG[i], v.cramCacheB[i], v.cramCacheCC[i]
	}
	cm := v.cramMode()
	switch cm {
	case 0:
		colorAddr &= 1023
		val := v.readCRAM16(colorAddr * 2)
		ccBit = val&0x8000 != 0
		r, g, b, _ = rgb555ToRGBA(val)
	case 1:
		colorAddr &= 2047
		val := v.readCRAM16(colorAddr * 2)
		ccBit = val&0x8000 != 0
		r, g, b, _ = rgb555ToRGBA(val)
	default:
		colorAddr &= 1023
		hiWord := v.readCRAM16(colorAddr * 4)
		loWord := v.readCRAM16(colorAddr*4 + 2)
		ccBit = hiWord&0x8000 != 0
		r = uint8(loWord)
		g = uint8(loWord >> 8)
		b = uint8(hiWord)
	}
	return
}

// lookupColorCC converts a dot color index to RGB and also returns the CRAM CC bit.
func (v *VDP2) lookupColorCC(dotColor uint8, palette uint8, cramOffset uint8, colorMode uint8, transpOff bool) (r, g, b uint8, transparent bool, ccBit bool) {
	if dotColor == 0 && !transpOff {
		return 0, 0, 0, true, false
	}
	var colorAddr uint32
	switch colorMode {
	case 0:
		colorAddr = uint32(palette)*16 + uint32(dotColor)
	case 1:
		colorAddr = uint32(palette>>4)*256 + uint32(dotColor)
	default:
		colorAddr = uint32(dotColor)
	}
	colorAddr += uint32(cramOffset) * 256
	r, g, b, ccBit = v.readCRAMColorWithCC(colorAddr)
	return r, g, b, false, ccBit
}

// lookupColor converts a dot color index + palette info to RGB values.
// Returns transparent=true if dotColor==0 and transparency is enabled.
func (v *VDP2) lookupColor(dotColor uint8, palette uint8, cramOffset uint8, colorMode uint8, transpOff bool) (r, g, b uint8, transparent bool) {
	if dotColor == 0 && !transpOff {
		return 0, 0, 0, true
	}

	var colorAddr uint32
	switch colorMode {
	case 0: // 16-color
		colorAddr = uint32(palette)*16 + uint32(dotColor)
	case 1: // 256-color
		colorAddr = uint32(palette>>4)*256 + uint32(dotColor)
	default:
		colorAddr = uint32(dotColor)
	}
	colorAddr += uint32(cramOffset) * 256

	r, g, b = v.readCRAMColor(colorAddr)
	return r, g, b, false
}

// renderNBG renders one normal scroll screen into the given buffer.
func (v *VDP2) renderNBG(screen int, buf []uint32) {
	cfg := v.decodeNBGConfig(screen)
	if !cfg.enabled || cfg.priority == 0 {
		clear(buf)
		return
	}

	if cfg.bitmapMode {
		v.renderNBGBitmap(buf, &cfg, screen)
		return
	}

	width := int(v.activeWidth)
	height := int(v.activeLines)

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

	for y := 0; y < height; y++ {
		// Per-line scroll overrides. Per VDP2 manual section 5.3, line
		// scroll table values are relative and are added to the base
		// scroll registers. The vertical coordinate increment register
		// is only applied when the line interval is two lines or greater
		// (LSS >= 1); within a single-line entry it contributes zero.
		lineScrollXFP := cfg.scrollXFP
		lineScrollYFP := cfg.scrollYFP
		lineIncXFP := cfg.incXFP
		if hasLineScroll {
			srcY := y
			if v.effectiveInterlace() == 3 {
				srcY = y*2 + v.fieldBit()
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
				lineScrollYFP = cfg.scrollYFP + lsyDelta
				fieldOff += 4
			}
			if cfg.lineZoomX {
				hi := v.readVRAM16(tableOff + fieldOff)
				lo := v.readVRAM16(tableOff + fieldOff + 2)
				lineIncXFP = int32(hi&0x07)<<8 | int32(lo>>8)
				if lineIncXFP == 0 {
					lineIncXFP = 0x100
				}
			}
		}

		// Source-Y for cell-pattern lookup uses displayed-line index so
		// the pattern advances per displayed scanline. Under LSMD=3
		// effective interlace the loop's y is field-line; convert.
		dispY := y
		if v.effectiveInterlace() == 3 {
			dispY = y*2 + v.fieldBit()
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

		for x := 0; x < width; x++ {
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
					cCharNum, cPalette, cHflip, cVflip = decodePattern1Word(pn, &cfg)
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

			if cfg.sfccmdMode == 3 {
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
						r, g, b, _ = rgb555ToRGBA(raw)
					}
				case 4: // 16.7M (32bpp RGB direct)
					var op bool
					r, g, b, op = v.readCellPixel32bpp(cellAddr, dx, dy)
					if !op && !cfg.transpOff {
						transp = true
					}
				}
			} else {
				switch cfg.colorMode {
				case 0:
					dot := v.readCellPixel4bpp(cellAddr, dx, dy)
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
				case 1:
					dot := v.readCellPixel8bpp(cellAddr, dx, dy)
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
				case 2:
					raw := v.readCellPixel16bpp(cellAddr, dx, dy)
					idx := raw & 0x07FF
					dotColor = uint8(idx)
					if idx == 0 && !cfg.transpOff {
						transp = true
					} else {
						colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
						r, g, b = v.readCRAMColor(colorAddr)
					}
				case 3:
					raw := v.readCellPixel16bpp(cellAddr, dx, dy)
					if raw&0x8000 == 0 && !cfg.transpOff {
						transp = true
					} else {
						r, g, b, _ = rgb555ToRGBA(raw)
					}
				case 4: // 16.7M (32bpp RGB direct)
					var op bool
					r, g, b, op = v.readCellPixel32bpp(cellAddr, dx, dy)
					if !op && !cfg.transpOff {
						transp = true
					}
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
				if cfg.colorMode != 3 {
					if sfcodeMatches(v.sfcodeForScreen(screen), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = isCCEnabled(v.regs[vdp2CCCTL], screen)
			case 1:
				ccEnabled = specialCCBit
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = sfcodeMatches(v.sfcodeForScreen(screen), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = true
				} else {
					ccEnabled = cramCCBit
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

// renderNBGBitmap renders a bitmap-mode scroll screen.
func (v *VDP2) renderNBGBitmap(buf []uint32, cfg *nbgConfig, screen int) {
	width := int(v.activeWidth)
	height := int(v.activeLines)

	// Bitmap base address: mapOffset (from MPOFN) * 0x20000
	baseAddr := cfg.mapOffset * 0x20000

	// cfg.bmpPalette is the 7-bit palette pre-shifted at setup;
	// lookupColor handles per-mode CRAM-index math.
	palette := cfg.bmpPalette

	hasLineScroll := cfg.lineScrollX || cfg.lineScrollY || cfg.lineZoomX

	for y := 0; y < height; y++ {
		// Source-Y for bitmap sampling uses displayed-line index so the
		// pattern advances per displayed scanline. Under LSMD=3
		// effective interlace the loop's y is field-line; convert.
		dispY := y
		if v.effectiveInterlace() == 3 {
			dispY = y*2 + v.fieldBit()
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
			if v.effectiveInterlace() == 3 {
				srcY = y*2 + v.fieldBit()
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
				if lineIncXFP == 0 {
					lineIncXFP = 0x100
				}
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
		syFP := cfg.scrollYFP + lsyDelta + yAdvance*cfg.incYFP
		sy := int(syFP >> 8)
		if cfg.bmpHeight > 0 {
			sy = sy % cfg.bmpHeight
			if sy < 0 {
				sy += cfg.bmpHeight
			}
		}
		for x := 0; x < width; x++ {
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

			if cfg.sfccmdMode == 3 {
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
						r, g, b, _ = rgb555ToRGBA(raw)
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
			} else {
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
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
				case 1: // 256-color (8bpp)
					pixOff := baseAddr + uint32(sy*cfg.bmpWidth+sx)
					dot := v.vram[pixOff&(vdp2VRAMSize-1)]
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
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
						r, g, b = v.readCRAMColor(colorAddr)
					}
				case 3: // 32768-color (16bpp RGB direct)
					pixOff := baseAddr + uint32((sy*cfg.bmpWidth+sx)*2)
					hi := v.vram[pixOff&(vdp2VRAMSize-1)]
					lo := v.vram[(pixOff+1)&(vdp2VRAMSize-1)]
					raw := uint16(hi)<<8 | uint16(lo)
					if raw&0x8000 == 0 && !cfg.transpOff {
						transp = true
					} else {
						r, g, b, _ = rgb555ToRGBA(raw)
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
				if cfg.colorMode != 3 {
					if sfcodeMatches(v.sfcodeForScreen(screen), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = isCCEnabled(v.regs[vdp2CCCTL], screen)
			case 1:
				ccEnabled = cfg.bmpSpecialCC
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = sfcodeMatches(v.sfcodeForScreen(screen), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = true
				} else {
					ccEnabled = cramCCBit
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

// --- RBG0 Rotation Scroll ---

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

// decodeRBGConfig reads RBG0 configuration from registers.
func (v *VDP2) decodeRBGConfig() rbgConfig {
	var cfg rbgConfig
	bgon := v.regs[vdp2BGON]
	cfg.enabled = bgon&(1<<4) != 0
	cfg.transpOff = bgon&(1<<12) != 0

	chctlb := v.regs[vdp2CHCTLB]
	cfg.charSize1x1 = chctlb&0x100 == 0
	cfg.colorMode = uint8((chctlb >> 12) & 0x07)
	cfg.bitmapMode = chctlb&0x0200 != 0
	bmsz := (chctlb >> 10) & 0x01
	// Rotation scroll bitmap: width is always 512, height is 256 or 512
	cfg.bmpWidth = 512
	cfg.bmpHeight = 256
	if bmsz&0x01 != 0 {
		cfg.bmpHeight = 512
	}

	bmpnb := v.regs[vdp2BMPNB]
	// BMP6-4 at BMPNB bits 2:0 per PDF Sec 4.10.
	cfg.bmpPalette = uint8(bmpnb&0x07) << 4
	cfg.bmpSpecialPri = bmpnb&(1<<5) != 0
	cfg.bmpSpecialCC = bmpnb&(1<<4) != 0

	cfg.pncnReg = v.regs[vdp2PNCR]
	cfg.pnWord1 = cfg.pncnReg&0x8000 != 0
	cfg.auxMode1 = cfg.pncnReg&0x4000 != 0

	cfg.mapOffset = uint32(v.regs[vdp2MPOFR]) & 0x07

	// 16 planes from 8 register words (Param A)
	for i := 0; i < 8; i++ {
		reg := v.regs[vdp2MPABRA+i]
		cfg.mapRegs[i*2] = uint8(reg)
		cfg.mapRegs[i*2+1] = uint8(reg >> 8)
	}

	plsz := v.regs[vdp2PLSZ]
	plszBits := (plsz >> 8) & 0x03
	switch plszBits {
	case 0:
		cfg.planePagesH = 1
		cfg.planePagesV = 1
	case 1:
		cfg.planePagesH = 2
		cfg.planePagesV = 1
	case 3:
		cfg.planePagesH = 2
		cfg.planePagesV = 2
	default:
		cfg.planePagesH = 1
		cfg.planePagesV = 1
	}

	cfg.screenOver = uint8((plsz >> 10) & 0x03)
	cfg.priority = uint8(v.regs[vdp2PRIR]) & 0x07
	cfg.cramOffset = uint8(v.regs[vdp2CRAOFB]) & 0x07
	cfg.rpMode = uint8(v.regs[vdp2RPMD]) & 0x03

	// Special priority and special color calculation modes (RBG0 = bits 9:8)
	cfg.sfprmdMode = uint8((v.regs[vdp2SFPRMD] >> 8) & 0x03)
	cfg.sfccmdMode = uint8((v.regs[vdp2SFCCMD] >> 8) & 0x03)

	// RBG0 mosaic: bit 4 = R0MZE, horizontal only for rotation screens
	mzctl := v.regs[vdp2MZCTL]
	if mzctl&(1<<4) != 0 {
		cfg.mosaicH = int((mzctl>>8)&0xF) + 1
	}

	return cfg
}

// decodePattern1WordRBG is the RBG0/RBG1 variant of decodePattern1Word.
func decodePattern1WordRBG(pn uint16, cfg *rbgConfig) (charNum uint32, palette uint8, hflip, vflip bool) {
	suppPalette := uint8((cfg.pncnReg >> 5) & 0x07)
	suppChar := uint8(cfg.pncnReg & 0x1F)

	if cfg.colorMode == 0 {
		if !cfg.auxMode1 {
			palette = uint8(pn>>12) & 0x0F
			palette |= suppPalette << 4
			vflip = pn&0x0800 != 0
			hflip = pn&0x0400 != 0
			if cfg.charSize1x1 {
				charNum = uint32(pn & 0x03FF)
				charNum |= uint32(suppChar) << 10
			} else {
				charNum = uint32(pn&0x03FF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x1C) << 10
			}
		} else {
			palette = uint8(pn>>12) & 0x0F
			palette |= suppPalette << 4
			if cfg.charSize1x1 {
				charNum = uint32(pn & 0x0FFF)
				charNum |= uint32(suppChar&0x1C) << 10
			} else {
				charNum = uint32(pn&0x0FFF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x10) << 10
			}
		}
	} else {
		if !cfg.auxMode1 {
			palette = (uint8(pn>>12) & 0x07) << 4
			vflip = pn&0x0800 != 0
			hflip = pn&0x0400 != 0
			if cfg.charSize1x1 {
				charNum = uint32(pn & 0x03FF)
				charNum |= uint32(suppChar) << 10
			} else {
				charNum = uint32(pn&0x03FF) << 2
				charNum |= uint32(suppChar & 0x03)
				charNum |= uint32(suppChar&0x1C) << 10
			}
		} else {
			palette = (uint8(pn>>12) & 0x07) << 4
			if cfg.charSize1x1 {
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

// rbgPerFrame holds precomputed per-frame rotation constants.
type rbgPerFrame struct {
	xp, yp int64 // .10 FP
	dxFP   int64 // .10 FP (per-pixel X increment)
	dyFP   int64 // .10 FP (per-pixel Y increment)
}

// computePerFrame computes per-frame rotation constants from parameters.
func computePerFrame(p *rotParams) rbgPerFrame {
	var pf rbgPerFrame
	// Xp = A*(Px-Cx) + B*(Py-Cy) + C*(Pz-Cz) + Cx + Mx
	// A is .10, (Px-Cx) is integer -> result is .10
	pf.xp = p.a*(p.px-p.cx) + p.b*(p.py-p.cy) + p.c*(p.pz-p.cz) + (p.cx << 10) + p.mx
	pf.yp = p.d*(p.px-p.cx) + p.e*(p.py-p.cy) + p.f*(p.pz-p.cz) + (p.cy << 10) + p.my
	// dX = A*DX + B*DY (.10 * .10 = .20, >>10 = .10)
	pf.dxFP = (p.a*p.dx + p.b*p.dy) >> 10
	pf.dyFP = (p.d*p.dx + p.e*p.dy) >> 10
	return pf
}

// computePerLine computes per-line Xsp/Ysp values.
// vcnt is the current scanline.
// rereadXst/rereadYst: when true, Xst/Yst are re-read per line from VRAM,
// so the DXst*vcnt / DYst*vcnt term is zeroed (the VRAM value already
// incorporates the per-line change).
func computePerLine(p *rotParams, vcnt int64, rereadXst, rereadYst bool) (xsp, ysp int64) {
	// Xsp = A*[(Xst + DXst*vcnt) - Px] + B*[(Yst + DYst*vcnt) - Py] + C*(Zst - Pz)
	sx := p.xst - (p.px << 10)
	if !rereadXst {
		sx += p.dxst * vcnt
	}
	sy := p.yst - (p.py << 10)
	if !rereadYst {
		sy += p.dyst * vcnt
	}
	sz := p.zst - (p.pz << 10) // .10
	// A (.10) * sx (.10) = .20, >>10 = .10
	xsp = (p.a*sx + p.b*sy + p.c*sz) >> 10
	ysp = (p.d*sx + p.e*sy + p.f*sz) >> 10
	return
}

// readCoefficient reads a coefficient table entry from VRAM.
// oneWord: true=1-word format, false=2-word format.
// mode3: true if coefficient data mode is 3 (Xp replacement).
// Returns the coefficient value, the MSB (transparent/switching) flag, and
// the 7-bit line color screen data from bits 14:8 of the 2-word format's
// first word (0 for 1-word format).
// For modes 0-2, value is .16 FP (for kx/ky replacement).
// For mode 3, value is .10 FP (for Xp replacement).
// rbgLineCoefAddr computes the per-line coefficient table address per
// PDF Sec 6.1: KAst + ΔKAst x Vcnt (in .10 FP), then bit-shifted and
// scaled by the coefficient entry size. ktaofBits is the 3-bit KTAOF
// field for this parameter (low 2 bits used in 2-word mode). When
// rereadKAst is true the per-line VRAM re-read has already been
// performed, so the ΔKAst contribution is zero (the just-read KAst
// already incorporates the per-line offset).
func rbgLineCoefAddr(kast, dkast, vcnt int64, ktaofBits uint32, oneWord, rereadKAst bool) uint32 {
	return rbgCoefAddrFromFP(rbgLineKAstFP(kast, dkast, vcnt, rereadKAst), ktaofBits, oneWord)
}

// rbgLineKAstFP returns the line's KAst value as .10 FP per
// VDP2 User's Manual Sec 6.1: KAst + ΔKAst x Vcnt. When rereadKAst is
// true the freshly-read KAst already incorporates the per-line offset
// so ΔKAst is not added. The returned value is the line's KAst before
// the per-pixel ΔKAx x Hcnt term, which callers must accumulate in FP
// (not after bit-shifting) to avoid dropping fractional precision.
func rbgLineKAstFP(kast, dkast, vcnt int64, rereadKAst bool) int64 {
	if rereadKAst {
		return kast
	}
	return kast + dkast*vcnt
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

// rotParamBases returns the byte addresses of rotation parameter
// table A and B in VRAM. Per VDP2 User's Manual section 6.3, RPTAU
// supplies bits 18:16 (low 3 bits) and RPTAL supplies bits 15:1 of
// an 18-bit value; bit 0 of RPTAL is reserved and must be ignored.
// The actual table address is that 18-bit value times 4. RPTA6
// (byte-address bit 7) is forced to 0 for parameter A and 1 for
// parameter B regardless of any value written to it.
func (v *VDP2) rotParamBases() (paramABase, paramBBase uint32) {
	rptau := v.regs[vdp2RPTAU]
	rptal := v.regs[vdp2RPTAL]
	tableBase := ((uint32(rptau&0x07) << 16) | uint32(rptal&0xFFFE)) * 2
	paramABase = tableBase & ^uint32(0x80)
	paramBBase = paramABase | 0x80
	return
}

// renderRBG0 renders the RBG0 rotation scroll screen into the given buffer.
func (v *VDP2) renderRBG0(buf []uint32) {
	cfg := v.decodeRBGConfig()
	if !cfg.enabled || cfg.priority == 0 {
		clear(buf)
		return
	}

	width := int(v.activeWidth)
	height := int(v.activeLines)

	// Read rotation parameter table base address
	paramABase, paramBBase := v.rotParamBases()

	// Read parameter sets based on RPMD mode
	var paramA, paramB rotParams
	var pfA, pfB rbgPerFrame
	needA := cfg.rpMode != 1
	needB := cfg.rpMode >= 1

	if needA {
		paramA = v.readRotParams(paramABase)
		pfA = computePerFrame(&paramA)
	}
	if needB {
		paramB = v.readRotParams(paramBBase)
		pfB = computePerFrame(&paramB)
	}

	// Coefficient table config
	ktctl := v.regs[vdp2KTCTL]
	ktaof := v.regs[vdp2KTAOF]
	crkte := v.regs[vdp2RAMCTL]&0x8000 != 0 && v.cramMode() == 1
	coefEnA := ktctl&0x01 != 0
	coefOneWordA := ktctl&0x02 != 0
	coefModeA := (ktctl >> 2) & 0x03
	coefEnB := ktctl&0x0100 != 0
	coefOneWordB := ktctl&0x0200 != 0
	coefModeB := (ktctl >> 10) & 0x03
	klceA := ktctl&0x10 != 0 && !coefOneWordA
	klceB := ktctl&0x1000 != 0 && !coefOneWordB

	// RPRCTL for per-line re-reading
	rprctl := v.regs[vdp2RPRCTL]
	rereadXstA := rprctl&0x01 != 0
	rereadYstA := rprctl&0x02 != 0
	rereadKAstA := rprctl&0x04 != 0
	rereadXstB := rprctl&0x0100 != 0
	rereadYstB := rprctl&0x0200 != 0
	rereadKAstB := rprctl&0x0400 != 0

	clear(v.rbg0LCBuf[:width*height])

	if cfg.bitmapMode {
		v.renderRBG0Bitmap(buf, &cfg, &paramA, &paramB, &pfA, &pfB,
			needA, needB, coefEnA, coefOneWordA, coefModeA,
			coefEnB, coefOneWordB, coefModeB,
			ktaof, rereadXstA, rereadYstA, rereadKAstA,
			rereadXstB, rereadYstB, rereadKAstB,
			paramABase, paramBBase, width, height)
		return
	}

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

	// Param B map config (for modes 1,2,3)
	var mapRegsB [16]uint8
	var mapOffsetB uint32
	var planePagesHB, planePagesVB int
	var screenOverB uint8
	if needB {
		for i := 0; i < 8; i++ {
			reg := v.regs[vdp2MPABRB+i]
			mapRegsB[i*2] = uint8(reg)
			mapRegsB[i*2+1] = uint8(reg >> 8)
		}
		mapOffsetB = uint32((v.regs[vdp2MPOFR] >> 4) & 0x07)
		plszB := (v.regs[vdp2PLSZ] >> 12) & 0x03
		switch plszB {
		case 0:
			planePagesHB = 1
			planePagesVB = 1
		case 1:
			planePagesHB = 2
			planePagesVB = 1
		case 3:
			planePagesHB = 2
			planePagesVB = 2
		default:
			planePagesHB = 1
			planePagesVB = 1
		}
		screenOverB = uint8((v.regs[vdp2PLSZ] >> 14) & 0x03)
	}

	for y := 0; y < height; y++ {
		vcnt := int64(y)
		if v.effectiveInterlace() == 3 {
			vcnt = int64(y*2 + v.fieldBit())
		}

		// Per-line parameter re-reading for Param A
		if needA {
			if rereadXstA {
				paramA.xst = signExtendFP(v.readVRAM16(paramABase+0x00), v.readVRAM16(paramABase+0x02), 12)
			}
			if rereadYstA {
				paramA.yst = signExtendFP(v.readVRAM16(paramABase+0x04), v.readVRAM16(paramABase+0x06), 12)
			}
			if rereadKAstA {
				paramA.kast = decodeFPkast(v.readVRAM16(paramABase+0x54), v.readVRAM16(paramABase+0x56))
			}
		}
		if needB {
			if rereadXstB {
				paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
			}
			if rereadYstB {
				paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
			}
			if rereadKAstB {
				paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
			}
		}

		// Per-line values
		var xspA, yspA, xspB, yspB int64
		if needA {
			xspA, yspA = computePerLine(&paramA, vcnt, rereadXstA, rereadYstA)
		}
		if needB {
			xspB, yspB = computePerLine(&paramB, vcnt, rereadXstB, rereadYstB)
		}

		// Per-line KAst in .10 FP (PDF Sec 6.1: KAst + ΔKAst x Vcnt unless
		// per-line VRAM re-read has already been done for this scanline).
		// Kept as FP so the per-pixel ΔKAx x Hcnt term can accumulate in
		// FP before integer extraction (the inverse order drops the
		// fractional part of ΔKAx and magnifies it by hcnt).
		var lineKAstFPA, lineKAstFPB int64
		if coefEnA && needA {
			lineKAstFPA = rbgLineKAstFP(paramA.kast, paramA.dkast, vcnt, rereadKAstA)
		}
		if coefEnB && needB {
			lineKAstFPB = rbgLineKAstFP(paramB.kast, paramB.dkast, vcnt, rereadKAstB)
		}

		for x := 0; x < width; x++ {
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
				pf = &pfA
				pp = &paramA
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
				pf = &pfB
				pp = &paramB
				xsp = xspB
				ysp = yspB
				curCoefEn = coefEnB
				curCoefOneWord = coefOneWordB
				curCoefMode = coefModeB
				curLineKAstFP = lineKAstFPB
				curKtaofBits = uint32(ktaof >> 8)
				curKLCE = klceB
				curMapRegs = &mapRegsB
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
				coefVal, coefMSB, coefLC := v.readCoefficient(coefAddr, curCoefOneWord, mode3, crkte)
				if curKLCE {
					v.rbg0LCBuf[y*width+x] = coefLC | 0x80
				}

				// MSB handling
				if cfg.rpMode == 2 && useA {
					// Mode 2: MSB switches parameters
					if coefMSB {
						// Switch to Param B for this pixel
						pf = &pfB
						pp = &paramB
						xsp = xspB
						ysp = yspB
						kx = pp.kx
						ky = pp.ky
						xpVal = pf.xp
						curMapRegs = &mapRegsB
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
						curKLCE = klceB
						goto skipCoefApply
					}
				} else if coefMSB {
					// Transparent
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
						ovpnReg = v.regs[vdp2OVPNRA]
					} else {
						ovpnReg = v.regs[vdp2OVPNRB]
					}
					ovCharNum, ovPalette, ovHflip, ovVflip := decodePattern1WordRBG(ovpnReg, &cfg)
					ovSpecialPri := cfg.pncnReg&(1<<9) != 0
					ovSpecialCC := cfg.pncnReg&(1<<8) != 0
					// Screen-over pattern repeats in displayed-line space; under
					// per-field interleave the loop's y is field-line so convert
					// to displayed-line before taking the modulo.
					ovY := y
					if v.effectiveInterlace() == 3 {
						ovY = y*2 + v.fieldBit()
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

					if cfg.sfccmdMode == 3 {
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
								ovR, ovG, ovB, _ = rgb555ToRGBA(raw)
							}
						}
					} else {
						switch cfg.colorMode {
						case 0:
							dot := v.readCellPixel4bpp(ovCellAddr, ovDotX, ovDotY)
							ovDotColor = dot
							ovR, ovG, ovB, ovTransp = v.lookupColor(dot, ovPalette, cfg.cramOffset, 0, cfg.transpOff)
						case 1:
							dot := v.readCellPixel8bpp(ovCellAddr, ovDotX, ovDotY)
							ovDotColor = dot
							ovR, ovG, ovB, ovTransp = v.lookupColor(dot, ovPalette, cfg.cramOffset, 1, cfg.transpOff)
						case 2:
							raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
							idx := raw & 0x07FF
							ovDotColor = uint8(idx)
							if idx == 0 && !cfg.transpOff {
								ovTransp = true
							} else {
								colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
								ovR, ovG, ovB = v.readCRAMColor(colorAddr)
							}
						case 3:
							raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
							if raw&0x8000 == 0 && !cfg.transpOff {
								ovTransp = true
							} else {
								ovR, ovG, ovB, _ = rgb555ToRGBA(raw)
							}
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
						if cfg.colorMode != 3 {
							if ovDotColor == v.sfcodeForScreen(4) {
								ovPriority = (ovPriority & 0xFE) | 1
							} else {
								ovPriority &= 0xFE
							}
						}
					}
					if ovPriority == 0 && !ovTransp {
						ovTransp = true
					}

					var ovCCEnabled bool
					switch cfg.sfccmdMode {
					case 0:
						ovCCEnabled = isCCEnabled(v.regs[vdp2CCCTL], 4)
					case 1:
						ovCCEnabled = ovSpecialCC
					case 2:
						if cfg.colorMode != 3 {
							ovCCEnabled = ovDotColor == v.sfcodeForScreen(4)
						}
					case 3:
						if cfg.colorMode == 3 {
							ovCCEnabled = true
						} else {
							ovCCEnabled = ovCramCCBit
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
				charNum, palette, hflip, vflip = decodePattern1WordRBG(pn, &cfg)
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

			if cfg.sfccmdMode == 3 {
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
						r, g, b, _ = rgb555ToRGBA(raw)
					}
				}
			} else {
				switch cfg.colorMode {
				case 0:
					dot := v.readCellPixel4bpp(cellAddr, dx, dy)
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
				case 1:
					dot := v.readCellPixel8bpp(cellAddr, dx, dy)
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
				case 2:
					raw := v.readCellPixel16bpp(cellAddr, dx, dy)
					idx := raw & 0x07FF
					dotColor = uint8(idx)
					if idx == 0 && !cfg.transpOff {
						transp = true
					} else {
						colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
						r, g, b = v.readCRAMColor(colorAddr)
					}
				case 3:
					raw := v.readCellPixel16bpp(cellAddr, dx, dy)
					if raw&0x8000 == 0 && !cfg.transpOff {
						transp = true
					} else {
						r, g, b, _ = rgb555ToRGBA(raw)
					}
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
				if cfg.colorMode != 3 {
					if sfcodeMatches(v.sfcodeForScreen(4), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = isCCEnabled(v.regs[vdp2CCCTL], 4)
			case 1:
				ccEnabled = specialCCBit
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = sfcodeMatches(v.sfcodeForScreen(4), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = true
				} else {
					ccEnabled = cramCCBit
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
	wctld := v.regs[vdp2WCTLD]
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
	if v.effectiveInterlace() == 3 {
		dispY = y*2 + v.fieldBit()
	}

	var w0Inside, w1Inside bool
	if w0En {
		if v.regs[vdp2LWTA0U]&0x8000 != 0 {
			// Normal line window (W0LWE): per-scanline X boundaries
			// from a VRAM table (manual Sec 8.1 p.185-186).
			lwta0 := ((uint32(v.regs[vdp2LWTA0U]&0x07) << 16) | uint32(v.regs[vdp2LWTA0L]&0xFFFE)) * 2
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
			sx := v.windowX(v.regs[vdp2WPSX0])
			sy := int(v.regs[vdp2WPSY0] & 0x01FF)
			ex := v.windowX(v.regs[vdp2WPEX0])
			ey := int(v.regs[vdp2WPEY0] & 0x01FF)
			if sx <= ex && sy <= ey {
				w0Inside = x >= sx && x <= ex && dispY >= sy && dispY <= ey
			}
		}
	}
	if w1En {
		if v.regs[vdp2LWTA1U]&0x8000 != 0 {
			// Normal line window (W1LWE).
			lwta1 := ((uint32(v.regs[vdp2LWTA1U]&0x07) << 16) | uint32(v.regs[vdp2LWTA1L]&0xFFFE)) * 2
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
			sx := v.windowX(v.regs[vdp2WPSX1])
			sy := int(v.regs[vdp2WPSY1] & 0x01FF)
			ex := v.windowX(v.regs[vdp2WPEX1])
			ey := int(v.regs[vdp2WPEY1] & 0x01FF)
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

// decodeRBG1Config builds an rbgConfig for RBG1.
// RBG1 always uses parameter B and borrows register fields from NBG0's slots.
func (v *VDP2) decodeRBG1Config() rbgConfig {
	var cfg rbgConfig

	cfg.enabled = v.regs[vdp2BGON]&(1<<5) != 0 && v.regs[vdp2BGON]&(1<<4) != 0
	cfg.transpOff = v.regs[vdp2BGON]&(1<<13) != 0

	// RBG1 borrows character control fields from NBG0's CHCTLA slots.
	chctla := v.regs[vdp2CHCTLA]
	cfg.charSize1x1 = chctla&0x01 == 0
	cfg.colorMode = uint8((chctla >> 4) & 0x07)
	cfg.bitmapMode = chctla&0x02 != 0
	if cfg.bitmapMode {
		bmsz := (chctla >> 2) & 0x03
		cfg.bmpWidth = 512
		cfg.bmpHeight = 256
		if bmsz&0x02 != 0 {
			cfg.bmpWidth = 1024
		}
		if bmsz&0x01 != 0 {
			cfg.bmpHeight = 512
		}
		bmpna := v.regs[vdp2BMPNA]
		// RBG1 reuses NBG0's BMPNA. Same pre-shift convention.
		cfg.bmpPalette = uint8(bmpna&0x07) << 4
		cfg.bmpSpecialPri = bmpna&(1<<5) != 0
		cfg.bmpSpecialCC = bmpna&(1<<4) != 0
	}

	cfg.pncnReg = v.regs[vdp2PNCN0]
	cfg.pnWord1 = cfg.pncnReg&0x8000 != 0
	cfg.auxMode1 = cfg.pncnReg&0x4000 != 0
	cfg.mapOffset = uint32((v.regs[vdp2MPOFR] >> 4) & 0x07)

	for i := 0; i < 8; i++ {
		reg := v.regs[vdp2MPABRB+i]
		cfg.mapRegs[i*2] = uint8(reg)
		cfg.mapRegs[i*2+1] = uint8(reg >> 8)
	}

	plsz := v.regs[vdp2PLSZ]
	switch (plsz >> 12) & 0x03 {
	case 1:
		cfg.planePagesH = 2
		cfg.planePagesV = 1
	case 3:
		cfg.planePagesH = 2
		cfg.planePagesV = 2
	default:
		cfg.planePagesH = 1
		cfg.planePagesV = 1
	}

	cfg.screenOver = uint8((plsz >> 14) & 0x03)
	cfg.priority = uint8(v.regs[vdp2PRINA]) & 0x07
	cfg.cramOffset = uint8(v.regs[vdp2CRAOFA] & 0x07)
	cfg.rpMode = 1

	cfg.sfprmdMode = uint8(v.regs[vdp2SFPRMD] & 0x03)
	cfg.sfccmdMode = uint8(v.regs[vdp2SFCCMD] & 0x03)

	// RBG1 mosaic: bit 0 = N0MZE (shared with NBG0), horizontal only
	mzctl := v.regs[vdp2MZCTL]
	if mzctl&(1<<0) != 0 {
		cfg.mosaicH = int((mzctl>>8)&0xF) + 1
	}

	return cfg
}

// renderRBG1 renders RBG1 using rotation parameter B only.
func (v *VDP2) renderRBG1(buf []uint32) {
	cfg := v.decodeRBG1Config()
	if !cfg.enabled || cfg.priority == 0 {
		clear(buf)
		return
	}

	width := int(v.activeWidth)
	height := int(v.activeLines)

	_, paramBBase := v.rotParamBases()

	paramB := v.readRotParams(paramBBase)
	pfB := computePerFrame(&paramB)

	ktctl := v.regs[vdp2KTCTL]
	ktaof := v.regs[vdp2KTAOF]
	crkteB := v.regs[vdp2RAMCTL]&0x8000 != 0 && v.cramMode() == 1
	coefEn := ktctl&0x0100 != 0
	coefOneWord := ktctl&0x0200 != 0
	coefMode := (ktctl >> 10) & 0x03
	klce := ktctl&0x1000 != 0 && !coefOneWord

	rprctl := v.regs[vdp2RPRCTL]
	rereadXst := rprctl&0x0100 != 0
	rereadYst := rprctl&0x0200 != 0
	rereadKAst := rprctl&0x0400 != 0

	clear(v.rbg1LCBuf[:width*height])

	if cfg.bitmapMode {
		v.renderRBG1Bitmap(buf, &cfg, &paramB, &pfB,
			coefEn, coefOneWord, coefMode, klce,
			ktaof, rereadXst, rereadYst, rereadKAst,
			paramBBase, width, height)
		return
	}

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

	for y := 0; y < height; y++ {
		vcnt := int64(y)
		if v.effectiveInterlace() == 3 {
			vcnt = int64(y*2 + v.fieldBit())
		}

		if rereadXst {
			paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
		}
		if rereadYst {
			paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
		}
		if rereadKAst {
			paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
		}

		xsp, ysp := computePerLine(&paramB, vcnt, rereadXst, rereadYst)

		var lineKAstFP int64
		if coefEn {
			lineKAstFP = rbgLineKAstFP(paramB.kast, paramB.dkast, vcnt, rereadKAst)
		}

		for x := 0; x < width; x++ {
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
				coefVal, coefMSB, coefLC := v.readCoefficient(coefAddr, coefOneWord, mode3, crkteB)
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
					ovpnReg := v.regs[vdp2OVPNRB]
					ovCharNum, ovPalette, ovHflip, ovVflip := decodePattern1WordRBG(ovpnReg, &cfg)
					ovSpecialPri := cfg.pncnReg&(1<<9) != 0
					ovSpecialCC := cfg.pncnReg&(1<<8) != 0
					// Screen-over pattern repeats in displayed-line space; under
					// per-field interleave the loop's y is field-line so convert
					// to displayed-line before taking the modulo.
					ovY := y
					if v.effectiveInterlace() == 3 {
						ovY = y*2 + v.fieldBit()
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

					if cfg.sfccmdMode == 3 {
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
								ovR, ovG, ovB, _ = rgb555ToRGBA(raw)
							}
						}
					} else {
						switch cfg.colorMode {
						case 0:
							dot := v.readCellPixel4bpp(ovCellAddr, ovDotX, ovDotY)
							ovDotColor = dot
							ovR, ovG, ovB, ovTransp = v.lookupColor(dot, ovPalette, cfg.cramOffset, 0, cfg.transpOff)
						case 1:
							dot := v.readCellPixel8bpp(ovCellAddr, ovDotX, ovDotY)
							ovDotColor = dot
							ovR, ovG, ovB, ovTransp = v.lookupColor(dot, ovPalette, cfg.cramOffset, 1, cfg.transpOff)
						case 2:
							raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
							idx := raw & 0x07FF
							ovDotColor = uint8(idx)
							if idx == 0 && !cfg.transpOff {
								ovTransp = true
							} else {
								colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
								ovR, ovG, ovB = v.readCRAMColor(colorAddr)
							}
						case 3:
							raw := v.readCellPixel16bpp(ovCellAddr, ovDotX, ovDotY)
							if raw&0x8000 == 0 && !cfg.transpOff {
								ovTransp = true
							} else {
								ovR, ovG, ovB, _ = rgb555ToRGBA(raw)
							}
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
						if cfg.colorMode != 3 {
							if ovDotColor == v.sfcodeForScreen(0) {
								ovPriority = (ovPriority & 0xFE) | 1
							} else {
								ovPriority &= 0xFE
							}
						}
					}
					if ovPriority == 0 && !ovTransp {
						ovTransp = true
					}

					var ovCCEnabled bool
					switch cfg.sfccmdMode {
					case 0:
						ovCCEnabled = isCCEnabled(v.regs[vdp2CCCTL], 0)
					case 1:
						ovCCEnabled = ovSpecialCC
					case 2:
						if cfg.colorMode != 3 {
							ovCCEnabled = ovDotColor == v.sfcodeForScreen(0)
						}
					case 3:
						if cfg.colorMode == 3 {
							ovCCEnabled = true
						} else {
							ovCCEnabled = ovCramCCBit
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
				charNum, palette, hflip, vflip = decodePattern1WordRBG(pn, &cfg)
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

			if cfg.sfccmdMode == 3 {
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
						r, g, b, _ = rgb555ToRGBA(raw)
					}
				}
			} else {
				switch cfg.colorMode {
				case 0:
					dot := v.readCellPixel4bpp(cellAddr, dx, dy)
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
				case 1:
					dot := v.readCellPixel8bpp(cellAddr, dx, dy)
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
				case 2:
					raw := v.readCellPixel16bpp(cellAddr, dx, dy)
					idx := raw & 0x07FF
					dotColor = uint8(idx)
					if idx == 0 && !cfg.transpOff {
						transp = true
					} else {
						colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
						r, g, b = v.readCRAMColor(colorAddr)
					}
				case 3:
					raw := v.readCellPixel16bpp(cellAddr, dx, dy)
					if raw&0x8000 == 0 && !cfg.transpOff {
						transp = true
					} else {
						r, g, b, _ = rgb555ToRGBA(raw)
					}
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
				if cfg.colorMode != 3 {
					if sfcodeMatches(v.sfcodeForScreen(0), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = isCCEnabled(v.regs[vdp2CCCTL], 0)
			case 1:
				ccEnabled = specialCCBit
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = sfcodeMatches(v.sfcodeForScreen(0), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = true
				} else {
					ccEnabled = cramCCBit
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

// renderRBG1Bitmap renders RBG1 in bitmap mode with rotation (param B only).
func (v *VDP2) renderRBG1Bitmap(buf []uint32, cfg *rbgConfig,
	paramB *rotParams, pfB *rbgPerFrame,
	coefEn, coefOneWord bool, coefMode uint16, klce bool,
	ktaof uint16,
	rereadXst, rereadYst, rereadKAst bool,
	paramBBase uint32,
	width, height int) {

	// Bitmap base address: mapOffset (from MPOFR) * 0x20000
	baseAddr := cfg.mapOffset * 0x20000

	bmpW := cfg.bmpWidth
	bmpH := cfg.bmpHeight

	for y := 0; y < height; y++ {
		vcnt := int64(y)
		if v.effectiveInterlace() == 3 {
			vcnt = int64(y*2 + v.fieldBit())
		}

		if rereadXst {
			paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
		}
		if rereadYst {
			paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
		}
		if rereadKAst {
			paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
		}

		xsp, ysp := computePerLine(paramB, vcnt, rereadXst, rereadYst)

		var lineKAstFP int64
		if coefEn {
			lineKAstFP = rbgLineKAstFP(paramB.kast, paramB.dkast, vcnt, rereadKAst)
		}

		for x := 0; x < width; x++ {
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
				crkteLocal := v.regs[vdp2RAMCTL]&0x8000 != 0 && v.cramMode() == 1
				coefVal, coefMSB, coefLC := v.readCoefficient(coefAddr, coefOneWord, mode3, crkteLocal)
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
			sx := int(mapXFP >> 10)
			sy := int(mapYFP >> 10)

			if sx < 0 || sx >= bmpW || sy < 0 || sy >= bmpH {
				switch cfg.screenOver {
				case 0:
					sx = ((sx % bmpW) + bmpW) % bmpW
					sy = ((sy % bmpH) + bmpH) % bmpH
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

			if cfg.sfccmdMode == 3 {
				switch cfg.colorMode {
				case 0:
					pixOff := baseAddr + uint32((sy*bmpW+sx)/2)
					byt := v.vram[pixOff&(vdp2VRAMSize-1)]
					var dot uint8
					if (sy*bmpW+sx)&1 == 0 {
						dot = byt >> 4
					} else {
						dot = byt & 0x0F
					}
					dotColor = dot
					r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
				case 1:
					pixOff := baseAddr + uint32(sy*bmpW+sx)
					dot := v.vram[pixOff&(vdp2VRAMSize-1)]
					dotColor = dot
					r, g, b, transp, cramCCBit = v.lookupColorCC(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
				case 2:
					pixOff := baseAddr + uint32((sy*bmpW+sx)*2)
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
					pixOff := baseAddr + uint32((sy*bmpW+sx)*2)
					hi := v.vram[pixOff&(vdp2VRAMSize-1)]
					lo := v.vram[(pixOff+1)&(vdp2VRAMSize-1)]
					raw := uint16(hi)<<8 | uint16(lo)
					if raw&0x8000 == 0 && !cfg.transpOff {
						transp = true
					} else {
						r, g, b, _ = rgb555ToRGBA(raw)
					}
				}
			} else {
				switch cfg.colorMode {
				case 0:
					pixOff := baseAddr + uint32((sy*bmpW+sx)/2)
					byt := v.vram[pixOff&(vdp2VRAMSize-1)]
					var dot uint8
					if (sy*bmpW+sx)&1 == 0 {
						dot = byt >> 4
					} else {
						dot = byt & 0x0F
					}
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 0, cfg.transpOff)
				case 1:
					pixOff := baseAddr + uint32(sy*bmpW+sx)
					dot := v.vram[pixOff&(vdp2VRAMSize-1)]
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, palette, cfg.cramOffset, 1, cfg.transpOff)
				case 2:
					pixOff := baseAddr + uint32((sy*bmpW+sx)*2)
					hi := v.vram[pixOff&(vdp2VRAMSize-1)]
					lo := v.vram[(pixOff+1)&(vdp2VRAMSize-1)]
					raw := uint16(hi)<<8 | uint16(lo)
					idx := raw & 0x07FF
					dotColor = uint8(idx)
					if idx == 0 && !cfg.transpOff {
						transp = true
					} else {
						colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
						r, g, b = v.readCRAMColor(colorAddr)
					}
				case 3:
					pixOff := baseAddr + uint32((sy*bmpW+sx)*2)
					hi := v.vram[pixOff&(vdp2VRAMSize-1)]
					lo := v.vram[(pixOff+1)&(vdp2VRAMSize-1)]
					raw := uint16(hi)<<8 | uint16(lo)
					if raw&0x8000 == 0 && !cfg.transpOff {
						transp = true
					} else {
						r, g, b, _ = rgb555ToRGBA(raw)
					}
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
				if cfg.colorMode != 3 {
					if sfcodeMatches(v.sfcodeForScreen(0), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = isCCEnabled(v.regs[vdp2CCCTL], 0)
			case 1:
				ccEnabled = cfg.bmpSpecialCC
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = sfcodeMatches(v.sfcodeForScreen(0), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = true
				} else {
					ccEnabled = cramCCBit
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

// renderRBG0Bitmap renders RBG0 in bitmap mode with rotation.
func (v *VDP2) renderRBG0Bitmap(buf []uint32, cfg *rbgConfig,
	paramA, paramB *rotParams, pfA, pfB *rbgPerFrame,
	needA, needB bool,
	coefEnA, coefOneWordA bool, coefModeA uint16,
	coefEnB, coefOneWordB bool, coefModeB uint16,
	ktaof uint16,
	rereadXstA, rereadYstA, rereadKAstA bool,
	rereadXstB, rereadYstB, rereadKAstB bool,
	paramABase, paramBBase uint32,
	width, height int) {

	// Bitmap base address: mapOffset (from MPOFR) * 0x20000
	baseAddr := cfg.mapOffset * 0x20000

	bmpW := cfg.bmpWidth
	bmpH := cfg.bmpHeight

	for y := 0; y < height; y++ {
		vcnt := int64(y)
		if v.effectiveInterlace() == 3 {
			vcnt = int64(y*2 + v.fieldBit())
		}

		if needA {
			if rereadXstA {
				paramA.xst = signExtendFP(v.readVRAM16(paramABase+0x00), v.readVRAM16(paramABase+0x02), 12)
			}
			if rereadYstA {
				paramA.yst = signExtendFP(v.readVRAM16(paramABase+0x04), v.readVRAM16(paramABase+0x06), 12)
			}
			if rereadKAstA {
				paramA.kast = decodeFPkast(v.readVRAM16(paramABase+0x54), v.readVRAM16(paramABase+0x56))
			}
		}
		if needB {
			if rereadXstB {
				paramB.xst = signExtendFP(v.readVRAM16(paramBBase+0x00), v.readVRAM16(paramBBase+0x02), 12)
			}
			if rereadYstB {
				paramB.yst = signExtendFP(v.readVRAM16(paramBBase+0x04), v.readVRAM16(paramBBase+0x06), 12)
			}
			if rereadKAstB {
				paramB.kast = decodeFPkast(v.readVRAM16(paramBBase+0x54), v.readVRAM16(paramBBase+0x56))
			}
		}

		var xspA, yspA, xspB, yspB int64
		if needA {
			xspA, yspA = computePerLine(paramA, vcnt, rereadXstA, rereadYstA)
		}
		if needB {
			xspB, yspB = computePerLine(paramB, vcnt, rereadXstB, rereadYstB)
		}

		for x := 0; x < width; x++ {
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
			if useA {
				pf = pfA
				pp = paramA
				xsp = xspA
				ysp = yspA
			} else {
				pf = pfB
				pp = paramB
				xsp = xspB
				ysp = yspB
			}

			kx := pp.kx
			ky := pp.ky
			xpVal := pf.xp

			xRaw := xsp + pf.dxFP*hcnt
			yRaw := ysp + pf.dyFP*hcnt
			mapXFP := (kx*xRaw)>>16 + xpVal
			mapYFP := (ky*yRaw)>>16 + pf.yp
			mapX := int(mapXFP >> 10)
			mapY := int(mapYFP >> 10)

			// Screen-over for bitmap
			if mapX < 0 || mapX >= bmpW || mapY < 0 || mapY >= bmpH {
				switch cfg.screenOver {
				case 0: // wrap
					mapX = ((mapX % bmpW) + bmpW) % bmpW
					mapY = ((mapY % bmpH) + bmpH) % bmpH
				case 2, 3:
					buf[y*width+x] = 0
					continue
				default:
					buf[y*width+x] = 0
					continue
				}
			}

			var r, g, b uint8
			var transp bool
			var dotColor uint8
			var cramCCBit bool

			if cfg.sfccmdMode == 3 {
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
						r, g, b, _ = rgb555ToRGBA(raw)
					}
				}
			} else {
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
					r, g, b, transp = v.lookupColor(dot, cfg.bmpPalette, cfg.cramOffset, 0, cfg.transpOff)
				case 1: // 256-color (8bpp)
					pixOff := baseAddr + uint32(mapY*bmpW+mapX)
					dot := v.vram[pixOff&(vdp2VRAMSize-1)]
					dotColor = dot
					r, g, b, transp = v.lookupColor(dot, cfg.bmpPalette, cfg.cramOffset, 1, cfg.transpOff)
				case 2: // 2048-color
					addr := baseAddr + uint32(mapY*bmpW+mapX)*2
					raw := v.readVRAM16(addr)
					idx := raw & 0x07FF
					dotColor = uint8(idx)
					if idx == 0 && !cfg.transpOff {
						transp = true
					} else {
						colorAddr := uint32(idx) + uint32(cfg.cramOffset)*256
						r, g, b = v.readCRAMColor(colorAddr)
					}
				case 3: // 32768-color
					addr := baseAddr + uint32(mapY*bmpW+mapX)*2
					raw := v.readVRAM16(addr)
					if raw&0x8000 == 0 && !cfg.transpOff {
						transp = true
					} else {
						r, g, b, _ = rgb555ToRGBA(raw)
					}
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
				if cfg.colorMode != 3 {
					if sfcodeMatches(v.sfcodeForScreen(4), dotColor) {
						priority = (priority & 0xFE) | 1
					} else {
						priority &= 0xFE
					}
				}
			}
			if priority == 0 && !transp {
				transp = true
			}

			// Compute per-pixel color calculation enable
			var ccEnabled bool
			switch cfg.sfccmdMode {
			case 0:
				ccEnabled = isCCEnabled(v.regs[vdp2CCCTL], 4)
			case 1:
				ccEnabled = cfg.bmpSpecialCC
			case 2:
				if cfg.colorMode != 3 {
					ccEnabled = sfcodeMatches(v.sfcodeForScreen(4), dotColor)
				}
			case 3:
				if cfg.colorMode == 3 {
					ccEnabled = true
				} else {
					ccEnabled = cramCCBit
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

// compositeFrame composites NBG layer buffers onto the framebuffer using priority.
// signExtend9 sign-extends a 9-bit value (from a 16-bit register) to int.
func signExtend9(val uint16) int {
	v := int(val & 0x01FF)
	if v&0x100 != 0 {
		v |= ^0x1FF // sign extend
	}
	return v
}

// windowX converts a raw 10-bit window horizontal coordinate to a pixel
// coordinate based on the current graphics mode per Table 8.1.
func (v *VDP2) windowX(raw uint16) int {
	if v.hiRes {
		// Hi-res: bits[9:0] = H9:H0, all valid
		return int(raw & 0x03FF)
	}
	// Normal: bits[9:1] = H8:H0, bit 0 invalid
	return int(raw&0x03FE) >> 1
}

// lineTableY returns the per-line table index for scanline y.
// Single-density interlace (LSMD=2) halves y since each entry covers
// two field-lines. Double-density interlace (LSMD=3) maps the caller's
// field-line y to the displayed-line index 2*y + fieldBit so the per-
// displayed-line tables (line scroll, line color, back screen, line
// window) are read at the correct offset. LSMD=3 with NBG mosaic
// enabled downgrades to LSMD=2 semantics per VDP2 manual section 4.11.
func (v *VDP2) lineTableY(y int) int {
	switch v.effectiveInterlace() {
	case 2:
		return y / 2
	case 3:
		return y*2 + v.fieldBit()
	}
	return y
}

// isWindowMasked returns true if the pixel at (x,y) should be masked
// (treated as transparent) by window processing for the given layer.
// layerID: 0-3=NBG0-3, 4=RBG0, 5=sprite.
// windowCtl returns the per-layer window control byte for the given
// layerID (0-3=NBG0-3, 4=RBG0, 5=sprite). ok is false for an invalid
// layerID.
func (v *VDP2) windowCtl(layerID int) (wctl uint8, ok bool) {
	switch layerID {
	case 0:
		return uint8(v.regs[vdp2WCTLA]), true
	case 1:
		return uint8(v.regs[vdp2WCTLA] >> 8), true
	case 2:
		return uint8(v.regs[vdp2WCTLB]), true
	case 3:
		return uint8(v.regs[vdp2WCTLB] >> 8), true
	case 4: // RBG0
		return uint8(v.regs[vdp2WCTLC]), true
	case 5: // Sprite
		return uint8(v.regs[vdp2WCTLC] >> 8), true
	}
	return 0, false
}

// buildWindowMaskCache precomputes, per layer, the all-windows-disabled
// result so isWindowMasked can return it without the per-pixel decode.
// When any window is enabled for a layer the slow path still runs, so
// the cached fast path is bit-identical to the full computation.
func (v *VDP2) buildWindowMaskCache() {
	for id := 0; id < 6; id++ {
		wctl, ok := v.windowCtl(id)
		if !ok {
			v.winMaskSkip[id] = true
			v.winMaskVal[id] = false
			continue
		}
		w0En := wctl&0x02 != 0
		w1En := wctl&0x08 != 0
		swEn := wctl&0x20 != 0
		if !w0En && !w1En && !swEn {
			v.winMaskSkip[id] = true
			v.winMaskVal[id] = wctl&0x80 != 0
		} else {
			v.winMaskSkip[id] = false
		}
	}
	v.winMaskValid = true
}

func (v *VDP2) isWindowMasked(x, y, layerID int) bool {
	if v.winMaskValid && uint(layerID) < 6 && v.winMaskSkip[layerID] {
		return v.winMaskVal[layerID]
	}

	// Get per-layer window control bits
	wctl, ok := v.windowCtl(layerID)
	if !ok {
		return false
	}

	w0En := wctl&0x02 != 0
	w1En := wctl&0x08 != 0
	swEn := wctl&0x20 != 0
	logic := wctl&0x80 != 0 // 0=OR, 1=AND

	if !w0En && !w1En && !swEn {
		// Per Sec 8.1: with all enables off, LOG=0 -> whole screen
		// disabled (not masked), LOG=1 -> whole screen enabled (masked).
		return logic
	}

	w0Area := wctl&0x01 != 0 // 0=inside, 1=outside
	w1Area := wctl&0x04 != 0
	swArea := wctl&0x10 != 0

	// Rectangle window Y bounds are in displayed-line space (9-bit fields
	// cover the full 480-line double-density range), while lineTableY and
	// readVDP1Pixel expect the caller's field-line y. Keep both.
	dispY := y
	if v.effectiveInterlace() == 3 {
		dispY = y*2 + v.fieldBit()
	}

	// Check W0: line window (W0LWE bit 15 of LWTA0U) or rectangle
	var w0Inside bool
	if w0En {
		if v.regs[vdp2LWTA0U]&0x8000 != 0 {
			// Line window: per-scanline X boundaries from VRAM table
			lwta0 := ((uint32(v.regs[vdp2LWTA0U]&0x07) << 16) | uint32(v.regs[vdp2LWTA0L]&0xFFFE)) * 2
			entryAddr := lwta0 + uint32(v.lineTableY(y))*4
			sx := v.windowX(v.readVRAM16(entryAddr))
			ex := v.windowX(v.readVRAM16(entryAddr + 2))
			if sx <= ex {
				w0Inside = x >= sx && x <= ex
			}
		} else {
			// Rectangle window
			sx := v.windowX(v.regs[vdp2WPSX0])
			sy := int(v.regs[vdp2WPSY0] & 0x01FF)
			ex := v.windowX(v.regs[vdp2WPEX0])
			ey := int(v.regs[vdp2WPEY0] & 0x01FF)
			if sx <= ex && sy <= ey {
				w0Inside = x >= sx && x <= ex && dispY >= sy && dispY <= ey
			}
		}
	}

	// Check W1: line window (W1LWE bit 15 of LWTA1U) or rectangle
	var w1Inside bool
	if w1En {
		if v.regs[vdp2LWTA1U]&0x8000 != 0 {
			lwta1 := ((uint32(v.regs[vdp2LWTA1U]&0x07) << 16) | uint32(v.regs[vdp2LWTA1L]&0xFFFE)) * 2
			entryAddr := lwta1 + uint32(v.lineTableY(y))*4
			sx := v.windowX(v.readVRAM16(entryAddr))
			ex := v.windowX(v.readVRAM16(entryAddr + 2))
			if sx <= ex {
				w1Inside = x >= sx && x <= ex
			}
		} else {
			sx := v.windowX(v.regs[vdp2WPSX1])
			sy := int(v.regs[vdp2WPSY1] & 0x01FF)
			ex := v.windowX(v.regs[vdp2WPEX1])
			ey := int(v.regs[vdp2WPEY1] & 0x01FF)
			if sx <= ex && sy <= ey {
				w1Inside = x >= sx && x <= ex && dispY >= sy && dispY <= ey
			}
		}
	}

	// Check sprite window
	var swInside bool
	if swEn {
		swPix, swValid := v.readVDP1Pixel(x, y)
		if swValid {
			if v.vdp1FB8bpp {
				swInside = swPix&0x80 != 0
			} else {
				swInside = swPix&0x8000 != 0
			}
		}
	}

	// Apply area inversion
	var w0Active, w1Active, swActive bool
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
	if swEn {
		if swArea {
			swActive = !swInside
		} else {
			swActive = swInside
		}
	}

	// Combine all enabled windows with OR or AND logic
	var active bool
	enabledCount := 0
	if w0En {
		enabledCount++
	}
	if w1En {
		enabledCount++
	}
	if swEn {
		enabledCount++
	}

	if enabledCount == 1 {
		if w0En {
			active = w0Active
		} else if w1En {
			active = w1Active
		} else {
			active = swActive
		}
	} else if logic {
		// AND: all enabled windows must be active
		active = true
		if w0En {
			active = active && w0Active
		}
		if w1En {
			active = active && w1Active
		}
		if swEn {
			active = active && swActive
		}
	} else {
		// OR: any enabled window active
		if w0En {
			active = active || w0Active
		}
		if w1En {
			active = active || w1Active
		}
		if swEn {
			active = active || swActive
		}
	}

	// Transparent control window: the active area is where the transparent
	// process is applied, so pixels in the active area are masked (the layer
	// is hidden there). Pixels outside the active area are drawn normally.
	return active
}

// isCCWindowActive checks if the color calculation window's active area
// includes pixel (x,y). Per VDP2 manual section 8.2, color calculation is
// NOT performed in the active area of the CC window.
// Returns true if CC should be suppressed at this pixel.
func (v *VDP2) isCCWindowActive(x, y int) bool {
	wctl := uint8(v.regs[vdp2WCTLD] >> 8)

	w0En := wctl&0x02 != 0
	w1En := wctl&0x08 != 0
	swEn := wctl&0x20 != 0
	logic := wctl&0x80 != 0

	if !w0En && !w1En && !swEn {
		return logic
	}

	w0Area := wctl&0x01 != 0
	w1Area := wctl&0x04 != 0
	swArea := wctl&0x10 != 0

	// Rectangle window Y bounds are in displayed-line space while
	// lineTableY and readVDP1Pixel expect field-line y under per-field
	// interleave.
	dispY := y
	if v.effectiveInterlace() == 3 {
		dispY = y*2 + v.fieldBit()
	}

	// Check W0: line window (W0LWE bit 15 of LWTA0U) or rectangle
	var w0Inside bool
	if w0En {
		if v.regs[vdp2LWTA0U]&0x8000 != 0 {
			lwta0 := ((uint32(v.regs[vdp2LWTA0U]&0x07) << 16) | uint32(v.regs[vdp2LWTA0L]&0xFFFE)) * 2
			entryAddr := lwta0 + uint32(v.lineTableY(y))*4
			sx := v.windowX(v.readVRAM16(entryAddr))
			ex := v.windowX(v.readVRAM16(entryAddr + 2))
			if sx <= ex {
				w0Inside = x >= sx && x <= ex
			}
		} else {
			sx := v.windowX(v.regs[vdp2WPSX0])
			sy := int(v.regs[vdp2WPSY0] & 0x01FF)
			ex := v.windowX(v.regs[vdp2WPEX0])
			ey := int(v.regs[vdp2WPEY0] & 0x01FF)
			if sx <= ex && sy <= ey {
				w0Inside = x >= sx && x <= ex && dispY >= sy && dispY <= ey
			}
		}
	}

	// Check W1: line window (W1LWE bit 15 of LWTA1U) or rectangle
	var w1Inside bool
	if w1En {
		if v.regs[vdp2LWTA1U]&0x8000 != 0 {
			lwta1 := ((uint32(v.regs[vdp2LWTA1U]&0x07) << 16) | uint32(v.regs[vdp2LWTA1L]&0xFFFE)) * 2
			entryAddr := lwta1 + uint32(v.lineTableY(y))*4
			sx := v.windowX(v.readVRAM16(entryAddr))
			ex := v.windowX(v.readVRAM16(entryAddr + 2))
			if sx <= ex {
				w1Inside = x >= sx && x <= ex
			}
		} else {
			sx := v.windowX(v.regs[vdp2WPSX1])
			sy := int(v.regs[vdp2WPSY1] & 0x01FF)
			ex := v.windowX(v.regs[vdp2WPEX1])
			ey := int(v.regs[vdp2WPEY1] & 0x01FF)
			if sx <= ex && sy <= ey {
				w1Inside = x >= sx && x <= ex && dispY >= sy && dispY <= ey
			}
		}
	}

	// Check sprite window
	var swInside bool
	if swEn {
		swPix, swValid := v.readVDP1Pixel(x, y)
		if swValid {
			if v.vdp1FB8bpp {
				swInside = swPix&0x80 != 0
			} else {
				swInside = swPix&0x8000 != 0
			}
		}
	}

	// Apply area inversion
	var w0Active, w1Active, swActive bool
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
	if swEn {
		if swArea {
			swActive = !swInside
		} else {
			swActive = swInside
		}
	}

	// Combine enabled windows with OR or AND logic
	var active bool
	enabledCount := 0
	if w0En {
		enabledCount++
	}
	if w1En {
		enabledCount++
	}
	if swEn {
		enabledCount++
	}

	if enabledCount == 1 {
		if w0En {
			active = w0Active
		} else if w1En {
			active = w1Active
		} else {
			active = swActive
		}
	} else if logic {
		active = true
		if w0En {
			active = active && w0Active
		}
		if w1En {
			active = active && w1Active
		}
		if swEn {
			active = active && swActive
		}
	} else {
		if w0En {
			active = active || w0Active
		}
		if w1En {
			active = active || w1Active
		}
		if swEn {
			active = active || swActive
		}
	}

	// CC window: active area = CC suppressed (inverted vs transparency window)
	return active
}

// Shadow type constants returned by classifyShadow.
const (
	shadowNone      = 0
	shadowNormal    = 1 // Normal shadow: darkens scroll/back per SDCTL
	shadowMSBSprite = 2 // MSB shadow on sprites: darkens normal sprites below
	shadowMSBTransp = 3 // MSB transparent shadow: darkens scroll/back per SDCTL
)

// classifyShadow determines the shadow type of a VDP1 sprite pixel.
// readVDP1Pixel reads a VDP1 sprite pixel at the caller's field-line y.
// Returns the pixel value and whether the read was valid (in-bounds and
// non-zero). Under per-field interleave rendering, the caller passes a
// field-line y in [0, activeLines) and the VDP1 framebuffer is sized to
// one field's worth of rows, so no Y halving is required here.
//
// Hi-res X halving applies only when VDP1 is in 16bpp mode (FB only 512
// pixels wide) paired with VDP2 hi-res (640 columns); two VDP2 columns
// then share one VDP1 column. When VDP1 is in 8bpp hi-res mode (FB 1024
// pixels wide) the FB matches VDP2 hi-res width directly and the X
// coordinate is used as-is.
func (v *VDP2) readVDP1Pixel(x, y int) (uint16, bool) {
	if v.vdp1DisplayFB == nil {
		return 0, false
	}
	spX := x
	spY := y
	if v.hiRes && !v.vdp1FB8bpp {
		spX = x / 2
	}
	if spX < 0 || spX >= v.vdp1FBWidth || spY < 0 || spY >= v.vdp1FBHeight {
		return 0, false
	}
	if v.vdp1FB8bpp {
		off := spY*v.vdp1FBWidth + spX
		pix := uint16(v.vdp1DisplayFB[off])
		return pix, pix != 0
	}
	off := (spY*v.vdp1FBWidth + spX) * 2
	pix := uint16(v.vdp1DisplayFB[off])<<8 | uint16(v.vdp1DisplayFB[off+1])
	return pix, pix != 0
}

// Normal shadow applies to all sprite types per Sec 14 Figure 14.3:
// DC bits all 1 except LSB=0. MSB shadow applies only to types 2-7
// with MSB=1. Normal shadow takes precedence over MSB shadow.
func (v *VDP2) classifyShadow(pixel uint16) int {
	if pixel == 0 {
		return shadowNone
	}
	spctl := v.regs[vdp2SPCTL]
	sptype := spctl & 0x0F

	// In SPCLMD=1 mixed palette+RGB mode, bit 15 is the format
	// discriminator (1=RGB direct, 0=palette), not the SD/MSB bit.
	// Per Sec 9.1: palette pixels in mixed mode have the sprite type
	// MSB processed as 0, so shadow classification is skipped.
	if spctl&0x20 != 0 {
		return shadowNone
	}

	// Normal shadow: per-type DC pattern, all 1 except LSB=0.
	isNormalShadow := false
	switch sptype {
	case 0, 1, 2, 3, 5:
		isNormalShadow = (pixel & 0x07FF) == 0x07FE
	case 4, 6:
		isNormalShadow = (pixel & 0x03FF) == 0x03FE
	case 7:
		isNormalShadow = (pixel & 0x01FF) == 0x01FE
	case 8:
		isNormalShadow = (pixel & 0x007F) == 0x007E
	case 9, 0xA, 0xB:
		isNormalShadow = (pixel & 0x003F) == 0x003E
	case 0xC, 0xD, 0xE, 0xF:
		isNormalShadow = (pixel & 0x00FF) == 0x00FE
	}
	if isNormalShadow {
		return shadowNormal
	}

	// MSB shadow: only types 2-7 with MSB bit 15 set.
	if sptype < 2 || sptype > 7 {
		return shadowNone
	}
	if pixel&0x8000 == 0 {
		return shadowNone
	}

	// MSB transparent shadow: bits[14:0] all zero (pixel=0x8000).
	if pixel&0x7FFF == 0 {
		if v.regs[vdp2SDCTL]&0x0100 != 0 {
			return shadowMSBTransp
		}
		return shadowNone
	}

	// MSB sprite shadow: bits[14:0] non-zero.
	return shadowMSBSprite
}

func (v *VDP2) decodeSpritePixel(pixel uint16) (priority uint8, ccBits uint8, colorMSB bool, r, g, b uint8) {
	if pixel == 0 {
		return 0, 0, false, 0, 0, 0
	}

	spctl := v.regs[vdp2SPCTL]
	spclmd := spctl&0x20 != 0

	// Check for RGB direct mode
	if spclmd && pixel&0x8000 != 0 {
		pri := uint8(v.regs[vdp2PRISA]) & 0x07
		r, g, b, _ = rgb555ToRGBA(pixel)
		return pri, 0, true, r, g, b
	}

	// Palette format: extract PR, CC, DC bits based on sprite type
	sptype := spctl & 0x0F
	var prBits uint8
	var colorAddr uint32

	switch sptype {
	case 0: // 2PR(b15:14) 3CC(b13:11) 11DC(b10:0)
		prBits = uint8((pixel >> 14) & 0x03)
		ccBits = uint8((pixel >> 11) & 0x07)
		colorAddr = uint32(pixel & 0x07FF)
		colorMSB = pixel&(1<<10) != 0
	case 1: // 3PR(b15:13) 2CC(b12:11) 11DC(b10:0)
		prBits = uint8((pixel >> 13) & 0x07)
		ccBits = uint8((pixel >> 11) & 0x03)
		colorAddr = uint32(pixel & 0x07FF)
		colorMSB = pixel&(1<<10) != 0
	case 2: // SD(b15) 1PR(b14) 3CC(b13:11) 11DC(b10:0)
		prBits = uint8((pixel >> 14) & 0x01)
		ccBits = uint8((pixel >> 11) & 0x07)
		colorAddr = uint32(pixel & 0x07FF)
		colorMSB = pixel&(1<<10) != 0
	case 3: // SD(b15) 2PR(b14:13) 2CC(b12:11) 11DC(b10:0)
		prBits = uint8((pixel >> 13) & 0x03)
		ccBits = uint8((pixel >> 11) & 0x03)
		colorAddr = uint32(pixel & 0x07FF)
		colorMSB = pixel&(1<<10) != 0
	case 4: // SD(b15) 2PR(b14:13) 3CC(b12:10) 10DC(b9:0)
		prBits = uint8((pixel >> 13) & 0x03)
		ccBits = uint8((pixel >> 10) & 0x07)
		colorAddr = uint32(pixel & 0x03FF)
		colorMSB = pixel&(1<<9) != 0
	case 5: // SD(b15) 3PR(b14:12) 1CC(b11) 11DC(b10:0)
		prBits = uint8((pixel >> 12) & 0x07)
		ccBits = uint8((pixel >> 11) & 0x01)
		colorAddr = uint32(pixel & 0x07FF)
		colorMSB = pixel&(1<<10) != 0
	case 6: // SD(b15) 3PR(b14:12) 2CC(b11:10) 10DC(b9:0)
		prBits = uint8((pixel >> 12) & 0x07)
		ccBits = uint8((pixel >> 10) & 0x03)
		colorAddr = uint32(pixel & 0x03FF)
		colorMSB = pixel&(1<<9) != 0
	case 7: // SD(b15) 3PR(b14:12) 3CC(b11:9) 9DC(b8:0)
		prBits = uint8((pixel >> 12) & 0x07)
		ccBits = uint8((pixel >> 9) & 0x07)
		colorAddr = uint32(pixel & 0x01FF)
		colorMSB = pixel&(1<<8) != 0
	case 0x8: // 1PR(b7) 7DC(b6:0)
		prBits = uint8((pixel >> 7) & 0x01)
		colorAddr = uint32(pixel & 0x7F)
		colorMSB = pixel&(1<<6) != 0
	case 0x9: // 1PR(b7) 1CC(b6) 6DC(b5:0)
		prBits = uint8((pixel >> 7) & 0x01)
		ccBits = uint8((pixel >> 6) & 0x01)
		colorAddr = uint32(pixel & 0x3F)
		colorMSB = pixel&(1<<5) != 0
	case 0xA: // 2PR(b7:6) 6DC(b5:0)
		prBits = uint8((pixel >> 6) & 0x03)
		colorAddr = uint32(pixel & 0x3F)
		colorMSB = pixel&(1<<5) != 0
	case 0xB: // 2CC(b7:6) 6DC(b5:0)
		ccBits = uint8((pixel >> 6) & 0x03)
		colorAddr = uint32(pixel & 0x3F)
		colorMSB = pixel&(1<<5) != 0
	case 0xC: // SP0=PR0=DC7(b7) 7DC(b6:0) - shared
		prBits = uint8((pixel >> 7) & 0x01)
		colorAddr = uint32(pixel & 0xFF)
		colorMSB = pixel&(1<<7) != 0
	case 0xD: // SP0=PR0=DC7(b7) SC0=CC0=DC6(b6) 6DC(b5:0) - shared
		prBits = uint8((pixel >> 7) & 0x01)
		ccBits = uint8((pixel >> 6) & 0x01)
		colorAddr = uint32(pixel & 0xFF)
		colorMSB = pixel&(1<<7) != 0
	case 0xE: // SP1=PR1=DC7(b7) SP0=PR0=DC6(b6) 6DC(b5:0) - shared
		prBits = uint8((pixel >> 6) & 0x03)
		colorAddr = uint32(pixel & 0xFF)
		colorMSB = pixel&(1<<7) != 0
	case 0xF: // SC1=CC1=DC7(b7) SC0=CC0=DC6(b6) 6DC(b5:0) - shared
		ccBits = uint8((pixel >> 6) & 0x03)
		colorAddr = uint32(pixel & 0xFF)
		colorMSB = pixel&(1<<7) != 0
	}

	// Look up priority from PRISA-PRISD (8 registers, 3 bits each)
	regIdx := prBits >> 1
	var regVal uint16
	switch regIdx {
	case 0:
		regVal = v.regs[vdp2PRISA]
	case 1:
		regVal = v.regs[vdp2PRISB]
	case 2:
		regVal = v.regs[vdp2PRISC]
	case 3:
		regVal = v.regs[vdp2PRISD]
	}
	if prBits&1 == 0 {
		priority = uint8(regVal) & 0x07
	} else {
		priority = uint8(regVal>>8) & 0x07
	}

	// Apply sprite CRAM offset (SPCAOS at CRAOFB bits 6:4)
	spriteCRAMOff := uint32((v.regs[vdp2CRAOFB] >> 4) & 0x07)
	colorAddr += spriteCRAMOff * 256

	r, g, b = v.readCRAMColor(colorAddr)
	return priority, ccBits, colorMSB, r, g, b
}

// isCCEnabled returns true if color calculation is enabled for the given layer.
func isCCEnabled(ccctl uint16, layerID int) bool {
	switch layerID {
	case 5: // sprite -> CCCTL bit 6
		return ccctl&(1<<6) != 0
	case 4: // RBG0 -> CCCTL bit 4
		return ccctl&(1<<4) != 0
	default: // NBG0-3 -> CCCTL bits 0-3
		return ccctl&(1<<uint(layerID)) != 0
	}
}

// isSpritePixelCCEnabled evaluates the SPCCCS condition for a sprite pixel.
// resolvedPriority is the 3-bit value from the PRISA-D register lookup.
// colorMSB is the MSB of the dot color field (used by SPCCCS mode 3).
func (v *VDP2) isSpritePixelCCEnabled(resolvedPriority uint8, colorMSB bool) bool {
	if v.regs[vdp2CCCTL]&(1<<6) == 0 {
		return false
	}
	spctl := v.regs[vdp2SPCTL]
	spcccs := (spctl >> 12) & 0x03
	spccn := uint8((spctl >> 8) & 0x07)
	switch spcccs {
	case 0:
		return resolvedPriority <= spccn
	case 1:
		return resolvedPriority == spccn
	case 2:
		return resolvedPriority >= spccn
	case 3:
		return colorMSB
	}
	return false
}

// getSpriteCCRatio returns the 5-bit CC ratio for a sprite using its CC bits.
// ccBits (0-7) indexes into CCRSA-CCRSD register pairs, same pattern as priority.
func (v *VDP2) getSpriteCCRatio(ccBits uint8) int {
	regIdx := ccBits >> 1
	var regVal uint16
	switch regIdx {
	case 0:
		regVal = v.regs[vdp2CCRSA]
	case 1:
		regVal = v.regs[vdp2CCRSB]
	case 2:
		regVal = v.regs[vdp2CCRSC]
	case 3:
		regVal = v.regs[vdp2CCRSD]
	}
	if ccBits&1 == 0 {
		return int(regVal) & 0x1F
	}
	return int(regVal>>8) & 0x1F
}

func clampU8(v int) uint8 {
	if v > 255 {
		return 255
	}
	if v < 0 {
		return 0
	}
	return uint8(v)
}

// applyGradation applies a horizontal blur to a layer buffer.
// Formula per pixel: blurred[x] = pixel[x-2]*1/4 + pixel[x-1]*1/4 + pixel[x]*2/4
// Processes right-to-left so left neighbors are always unblurred originals.
func applyGradation(buf []uint32, width, height int) {
	for y := 0; y < height; y++ {
		rowOff := y * width
		for x := width - 1; x >= 0; x-- {
			cur := buf[rowOff+x]
			if cur == 0 {
				continue
			}
			curR := int(uint8(cur >> 16))
			curG := int(uint8(cur >> 8))
			curB := int(uint8(cur))
			pri := cur & 0xFF000000

			var l1r, l1g, l1b, l2r, l2g, l2b int
			if x >= 1 {
				l1 := buf[rowOff+x-1]
				if l1 != 0 {
					l1r = int(uint8(l1 >> 16))
					l1g = int(uint8(l1 >> 8))
					l1b = int(uint8(l1))
				}
			}
			if x >= 2 {
				l2 := buf[rowOff+x-2]
				if l2 != 0 {
					l2r = int(uint8(l2 >> 16))
					l2g = int(uint8(l2 >> 8))
					l2b = int(uint8(l2))
				}
			}

			newR := (l2r + l1r + curR*2) / 4
			newG := (l2g + l1g + curG*2) / 4
			newB := (l2b + l1b + curB*2) / 4

			buf[rowOff+x] = pri | uint32(newR)<<16 | uint32(newG)<<8 | uint32(newB)
		}
	}
}

func (v *VDP2) compositeFrame() {
	width := int(v.activeWidth)
	height := int(v.activeLines)
	ccctl := v.regs[vdp2CCCTL]
	ccmd := ccctl&0x100 != 0   // bit 8: add-as-is mode
	ccrtmd := ccctl&0x200 != 0 // bit 9: ratio from second image
	exccen := ccctl&0x400 != 0 // bit 10: extended color calculation

	// Hi-res CC restrictions
	if v.hiRes {
		exccen = false // extended CC not available in hi-res
	}

	// Per-layer "is RGB direct color format". Used by extended color
	// calculation: per Table 12.2, in CRAM mode 1/2 the 2nd+3rd blend
	// only applies when the 2nd image is RGB format. NBG2/NBG3 only
	// support palette format. Sprite format depends on SPCTL mixed mode
	// per pixel; treat as palette here (the common case).
	chctlaR := v.regs[vdp2CHCTLA]
	chctlbR := v.regs[vdp2CHCTLB]
	nbg0CN := uint8((chctlaR >> 4) & 0x07)
	nbg1CN := uint8((chctlaR >> 12) & 0x03)
	rbg0CN := uint8((chctlbR >> 12) & 0x07)
	var layerIsRGB [6]bool
	layerIsRGB[0] = nbg0CN >= 3
	layerIsRGB[1] = nbg1CN >= 3
	layerIsRGB[4] = rbg0CN >= 3

	// Gradation calculation: horizontal blur on designated screen
	boken := ccctl&0x8000 != 0 // bit 15
	if boken && (v.hiRes || v.cramMode() != 0) {
		boken = false // gradation: normal TV mode + CRAM mode 0 only
	}
	if boken {
		bokn := (ccctl >> 12) & 0x07 // bits 14:12
		exccen = false               // BOKEN overrides EXCCEN

		var gradBuf []uint32
		switch bokn {
		case 1: // RBG0
			gradBuf = v.rbg0Buf
		case 2: // NBG0 (or RBG1 when active)
			if v.regs[vdp2BGON]&(1<<5) != 0 {
				gradBuf = v.rbg1Buf
			} else {
				gradBuf = v.layerBufs[0]
			}
		case 4: // NBG1
			gradBuf = v.layerBufs[1]
		case 5: // NBG2
			gradBuf = v.layerBufs[2]
		case 6: // NBG3
			gradBuf = v.layerBufs[3]
		}
		if gradBuf != nil {
			applyGradation(gradBuf, width, height)
		}
	}

	for y := 0; y < height; y++ {
		fbRow := v.outRow(y)
		for x := 0; x < width; x++ {
			p := y*width + x
			fbP := fbRow*width + x

			// Collect all non-transparent layer pixels with priority + layer ID.
			// Layer IDs: 0-3=NBG0-3, 4=RBG0, 5=sprite
			type candidate struct {
				pri       uint8
				tie       int // higher = wins on equal priority
				layerID   int
				r, g, b   uint8
				ccEnabled bool
				ccBits    uint8 // sprite CC register index (layerID==5 only)
			}
			var candidates [6]candidate
			ncand := 0

			// Sprite layer (layerID=5)
			shadowType := shadowNone
			spPixel, spValid := v.readVDP1Pixel(x, y)
			if spValid {
				stype := v.classifyShadow(spPixel)
				if stype != shadowNone {
					shadowType = stype
				} else {
					pri, spCCBits, colorMSB, sr, sg, sb := v.decodeSpritePixel(spPixel)
					// Per VDP2 manual Sec 11.1 Priority Function:
					// "When the value of the priority number is 0, it
					// is treated as transparent and not displayed."
					// Applies uniformly to NBG/RBG/sprite layers; the
					// NBG/RBG paths already enforce this. The sprite
					// path must too - games disable the sprite layer
					// globally by writing PRISA-D = 0.
					if pri != 0 {
						spCCEn := v.isSpritePixelCCEnabled(pri, colorMSB)
						candidates[ncand] = candidate{pri, 5, 5, sr, sg, sb, spCCEn, spCCBits}
						ncand++
					}
				}
			}

			// RBG0 layer (layerID=4). Tie-break rank 4 per spec Table 11.1
			// (Normal mode order: Sprite > RBG0 > NBG0 > NBG1 > NBG2 > NBG3).
			rbgPx := v.rbg0Buf[p]
			if rbgPx != 0 {
				if !v.isWindowMasked(x, y, 4) {
					pri := uint8((rbgPx >> 24) & 0x07)
					ccEn := rbgPx&layerCCBit != 0
					candidates[ncand] = candidate{pri, 4, 4, uint8(rbgPx >> 16), uint8(rbgPx >> 8), uint8(rbgPx), ccEn, 0}
					ncand++
				}
			}

			// NBG layers (RBG1 replaces NBG0 slot when active).
			// Tie-break ranks per spec Table 11.1 (Normal mode):
			// Sprite=5, RBG0=4, NBG0=3, NBG1=2, NBG2=1, NBG3=0.
			r1on := v.regs[vdp2BGON]&(1<<5) != 0
			for s := 0; s < 4; s++ {
				var px uint32
				if r1on && s == 0 {
					px = v.rbg1Buf[p]
				} else {
					px = v.layerBufs[s][p]
				}
				if px == 0 {
					continue
				}
				if v.isWindowMasked(x, y, s) {
					continue
				}
				pri := uint8((px >> 24) & 0x07)
				ccEn := px&layerCCBit != 0
				candidates[ncand] = candidate{pri, 3 - s, s, uint8(px >> 16), uint8(px >> 8), uint8(px), ccEn, 0}
				ncand++
			}

			if ncand == 0 {
				// Apply color offset to back screen if enabled (CLOFEN bit 5)
				clofen := v.regs[vdp2CLOFEN]
				if clofen&(1<<5) != 0 {
					clofsl := v.regs[vdp2CLOFSL]
					var offR, offG, offB int
					if clofsl&(1<<5) == 0 {
						offR = signExtend9(v.regs[vdp2COAR])
						offG = signExtend9(v.regs[vdp2COAG])
						offB = signExtend9(v.regs[vdp2COAB])
					} else {
						offR = signExtend9(v.regs[vdp2COBR])
						offG = signExtend9(v.regs[vdp2COBG])
						offB = signExtend9(v.regs[vdp2COBB])
					}
					off := fbP * 4
					br := int(v.framebuffer[off])
					bg := int(v.framebuffer[off+1])
					bb := int(v.framebuffer[off+2])
					v.framebuffer[off] = uint8(clampU8(br + offR))
					v.framebuffer[off+1] = uint8(clampU8(bg + offG))
					v.framebuffer[off+2] = uint8(clampU8(bb + offB))
				}

				// Apply shadow to back screen (SDCTL bit 5 = BKSDEN)
				switch shadowType {
				case shadowNormal, shadowMSBTransp:
					sdctl := v.regs[vdp2SDCTL]
					if sdctl&(1<<5) != 0 {
						off := fbP * 4
						v.framebuffer[off] = v.framebuffer[off] / 2
						v.framebuffer[off+1] = v.framebuffer[off+1] / 2
						v.framebuffer[off+2] = v.framebuffer[off+2] / 2
					}
				}

				continue
			}

			// Find top two by priority (descending), then tiebreak (descending)
			topIdx := 0
			for i := 1; i < ncand; i++ {
				c := candidates[i]
				t := candidates[topIdx]
				if c.pri > t.pri || (c.pri == t.pri && c.tie > t.tie) {
					topIdx = i
				}
			}
			top := candidates[topIdx]

			// Find second best (excluding top)
			secIdx := -1
			for i := 0; i < ncand; i++ {
				if i == topIdx {
					continue
				}
				if secIdx < 0 {
					secIdx = i
					continue
				}
				c := candidates[i]
				s := candidates[secIdx]
				if c.pri > s.pri || (c.pri == s.pri && c.tie > s.tie) {
					secIdx = i
				}
			}

			// Find third best (excluding top and second)
			thirdIdx := -1
			for i := 0; i < ncand; i++ {
				if i == topIdx || i == secIdx {
					continue
				}
				if thirdIdx < 0 {
					thirdIdx = i
					continue
				}
				c := candidates[i]
				t := candidates[thirdIdx]
				if c.pri > t.pri || (c.pri == t.pri && c.tie > t.tie) {
					thirdIdx = i
				}
			}

			r, g, b := int(top.r), int(top.g), int(top.b)

			// Color calculation: check if top layer has CC enabled
			topCCEnabled := top.ccEnabled

			// Hi-res palette CC restriction: CRAM mode 1/2 disables palette CC
			if topCCEnabled && v.hiRes && v.cramMode() >= 1 {
				if top.layerID != 5 {
					// Scroll layers always use palette format
					topCCEnabled = false
				} else if v.regs[vdp2SPCTL]&0x10 == 0 {
					// Sprites in all-palette mode
					topCCEnabled = false
				}
			}

			if topCCEnabled && !v.isCCWindowActive(x, y) {
				// Determine second image: line color screen, layer candidate, or back screen
				var secR, secG, secB uint8
				hasSecond := false
				lncInserted := false
				secIsBackScreen := false

				// Line color screen insertion: replaces second image
				lnclen := v.regs[vdp2LNCLEN]
				topLNCBit := uint(top.layerID)
				if top.layerID == 5 { // sprite
					topLNCBit = 5
				}
				if lnclen&(1<<topLNCBit) != 0 {
					lctau := v.regs[vdp2LCTAU]
					lcAddr := ((uint32(lctau&0x07) << 16) | uint32(v.regs[vdp2LCTAL])) * 2
					var lcOffset uint32
					if lctau&0x8000 != 0 { // LCCLMD=1: per-line; else single color
						lcOffset = uint32(v.lineTableY(y)) * 2
					}
					var lcData uint8
					r1on := v.regs[vdp2BGON]&(1<<5) != 0
					if top.layerID == 4 {
						lcData = v.rbg0LCBuf[p]
					} else if top.layerID == 0 && r1on {
						lcData = v.rbg1LCBuf[p]
					}
					if lcData&0x80 != 0 {
						// Coefficient line color: upper 4 bits from table + 7 bits from coefficient
						lcTableVal := v.readVRAM16(lcAddr + lcOffset)
						upperBits := uint32((lcTableVal >> 7) & 0x0F)
						lowerBits := uint32(lcData & 0x7F)
						cramAddr := (upperBits << 7) | lowerBits
						secR, secG, secB = v.readCRAMColor(cramAddr)
					} else {
						// Per VDP2 manual Sec 7.1 / Figure 7.3 the line color
						// screen table holds an 11-bit CRAM address, not
						// an RGB555 color. readCRAMColor masks to the
						// active CRAM-mode width so passing the full 11
						// bits is safe across modes.
						lcTableVal := v.readVRAM16(lcAddr + lcOffset)
						cramAddr := uint32(lcTableVal & 0x07FF)
						secR, secG, secB = v.readCRAMColor(cramAddr)
					}
					hasSecond = true
					lncInserted = true
				} else if secIdx >= 0 {
					sec := candidates[secIdx]
					secR, secG, secB = sec.r, sec.g, sec.b
					hasSecond = true
				} else {
					// Back screen is the second image (lowest in priority stack)
					bkOff := fbP * 4
					secR = v.framebuffer[bkOff]
					secG = v.framebuffer[bkOff+1]
					secB = v.framebuffer[bkOff+2]
					hasSecond = true
					secIsBackScreen = true
				}

				// Extended color calculation: blend second with third (and fourth).
				// Per Table 12.2: in CRAM mode 1 or 2, the blend ratio is 4:0:0
				// (no blend) when the 2nd image is palette format, regardless of
				// 2nd image CCEN. The blend only applies when the 2nd image is
				// RGB format. In CRAM mode 0 the blend follows CCEN as before.
				if exccen && hasSecond {
					// Second image's CC enable controls 2nd<->3rd blending
					var secLayerCCEN bool
					if secIsBackScreen {
						secLayerCCEN = false // back screen has no CC enable bit
					} else if lncInserted {
						// Line color inserted as second; check LCCCEN (bit 5)
						secLayerCCEN = ccctl&(1<<5) != 0
					} else if secIdx >= 0 {
						secLayerCCEN = candidates[secIdx].ccEnabled
					}

					// CRAM mode 1/2 format-dependent gating per Table 12.2.
					// No-LNCL: palette 2nd image -> ratio 4:0:0 (no blend).
					// LNCL: line color is the 2nd image (always palette); the
					// spec "3rd image" is the original 2nd (candidates[secIdx]).
					// If that is palette format -> ratio 4:0:0 (no blend).
					if v.cramMode() >= 1 && secIdx >= 0 {
						if !layerIsRGB[candidates[secIdx].layerID] {
							secLayerCCEN = false
						}
					}

					if secLayerCCEN {
						if !lncInserted {
							// No line color: ratio 2:2:0 (second + third) / 2
							if thirdIdx >= 0 {
								third := candidates[thirdIdx]
								secR = uint8((int(secR) + int(third.r)) / 2)
								secG = uint8((int(secG) + int(third.g)) / 2)
								secB = uint8((int(secB) + int(third.b)) / 2)
							}
						} else {
							// Line color inserted. Original second is at secIdx, original third at thirdIdx.
							// Third image's CC enable controls 3rd<->4th blending
							var thirdLayerCCEN bool
							if secIdx >= 0 {
								thirdLayerCCEN = candidates[secIdx].ccEnabled
							}

							// CRAM mode 1/2 + palette-format 4th image -> ratio
							// 2:2:0 (no 4th contribution) per Table 12.2.
							if v.cramMode() >= 1 && thirdIdx >= 0 {
								if !layerIsRGB[candidates[thirdIdx].layerID] {
									thirdLayerCCEN = false
								}
							}

							if !thirdLayerCCEN {
								// Ratio 2:2:0: (lineColor + original second) / 2
								if secIdx >= 0 {
									orig2nd := candidates[secIdx]
									secR = uint8((int(secR) + int(orig2nd.r)) / 2)
									secG = uint8((int(secG) + int(orig2nd.g)) / 2)
									secB = uint8((int(secB) + int(orig2nd.b)) / 2)
								}
							} else {
								// Ratio 2:1:1: lineColor/2 + original second/4 + original third/4
								var o2r, o2g, o2b, o3r, o3g, o3b int
								if secIdx >= 0 {
									orig2nd := candidates[secIdx]
									o2r = int(orig2nd.r)
									o2g = int(orig2nd.g)
									o2b = int(orig2nd.b)
								}
								if thirdIdx >= 0 {
									orig3rd := candidates[thirdIdx]
									o3r = int(orig3rd.r)
									o3g = int(orig3rd.g)
									o3b = int(orig3rd.b)
								}
								secR = uint8(int(secR)/2 + o2r/4 + o3r/4)
								secG = uint8(int(secG)/2 + o2g/4 + o3g/4)
								secB = uint8(int(secB)/2 + o2b/4 + o3b/4)
							}
						}
					}
				}

				if hasSecond {
					if ccmd {
						r = int(clampU8(r + int(secR)))
						g = int(clampU8(g + int(secG)))
						b = int(clampU8(b + int(secB)))
					} else {
						var ratio int
						if !ccrtmd {
							if top.layerID == 5 {
								ratio = v.getSpriteCCRatio(top.ccBits)
							} else {
								ratio = v.getLayerCCRatio(top.layerID)
							}
						} else if lncInserted {
							// Line color is the second image; use line color ratio
							ratio = int(v.regs[vdp2CCRLB]) & 0x1F
						} else if secIsBackScreen {
							// Back screen is second image; use BKCCRT (bits 12:8)
							ratio = int(v.regs[vdp2CCRLB]>>8) & 0x1F
						} else if secIdx >= 0 {
							if candidates[secIdx].layerID == 5 {
								ratio = v.getSpriteCCRatio(candidates[secIdx].ccBits)
							} else {
								ratio = v.getLayerCCRatio(candidates[secIdx].layerID)
							}
						} else {
							if top.layerID == 5 {
								ratio = v.getSpriteCCRatio(top.ccBits)
							} else {
								ratio = v.getLayerCCRatio(top.layerID)
							}
						}
						r = (r*(31-ratio) + int(secR)*(ratio+1)) / 32
						g = (g*(31-ratio) + int(secG)*(ratio+1)) / 32
						b = (b*(31-ratio) + int(secB)*(ratio+1)) / 32
					}
				}
			}

			// Color offset: apply signed RGB offset per layer
			// CLOFEN bits: 0=N0, 1=N1, 2=N2, 3=N3, 4=R0, 5=Back, 6=Sprite
			clofen := v.regs[vdp2CLOFEN]
			var layerBit uint
			if top.layerID == 5 {
				layerBit = 6 // Sprite is bit 6 in CLOFEN/CLOFSL
			} else {
				layerBit = uint(top.layerID) // 0-4 map directly
			}
			if clofen&(1<<layerBit) != 0 {
				clofsl := v.regs[vdp2CLOFSL]
				var offR, offG, offB int
				if clofsl&(1<<layerBit) == 0 {
					offR = signExtend9(v.regs[vdp2COAR])
					offG = signExtend9(v.regs[vdp2COAG])
					offB = signExtend9(v.regs[vdp2COAB])
				} else {
					offR = signExtend9(v.regs[vdp2COBR])
					offG = signExtend9(v.regs[vdp2COBG])
					offB = signExtend9(v.regs[vdp2COBB])
				}
				r = int(clampU8(r + offR))
				g = int(clampU8(g + offG))
				b = int(clampU8(b + offB))
			}

			// Shadow: darken based on shadow type
			switch shadowType {
			case shadowNormal, shadowMSBTransp:
				// Normal and MSB transparent shadow darken scroll/back per SDCTL
				if top.layerID != 5 {
					sdctl := v.regs[vdp2SDCTL]
					if top.layerID <= 4 && sdctl&(1<<uint(top.layerID)) != 0 {
						r = r / 2
						g = g / 2
						b = b / 2
					}
				}
			case shadowMSBSprite:
				// MSB sprite shadow darkens the sprite below
				if top.layerID == 5 {
					r = r / 2
					g = g / 2
					b = b / 2
				}
			}

			off := fbP * 4
			v.framebuffer[off] = uint8(r)
			v.framebuffer[off+1] = uint8(g)
			v.framebuffer[off+2] = uint8(b)
			v.framebuffer[off+3] = 0xFF
		}
	}
}

// getLayerCCRatio returns the 5-bit color calculation ratio for a layer.
// layerID: 0-3=NBG0-3, 4=sprite (uses register 0 for now).
func (v *VDP2) getLayerCCRatio(layerID int) int {
	switch layerID {
	case 0:
		return int(v.regs[vdp2CCRNA]) & 0x1F
	case 1:
		return int(v.regs[vdp2CCRNA]>>8) & 0x1F
	case 2:
		return int(v.regs[vdp2CCRNB]) & 0x1F
	case 3:
		return int(v.regs[vdp2CCRNB]>>8) & 0x1F
	case 4: // RBG0
		return int(v.regs[vdp2CCRR]) & 0x1F
	case 5: // Sprite
		return int(v.regs[vdp2CCRSA]) & 0x1F
	}
	return 0
}

// RenderFrame produces an RGBA8888 framebuffer from the current VDP2 state.
// reductionDisablesCompanion returns true when NBG0's reduction (ZMHF/ZMQT)
// + color count (CHCN value) suppresses the companion NBG2 layer per PDF
// Sec 5.2 Table 5.2. The same rule with NBG1 inputs governs NBG3. zmBits is
// (ZMQT << 1) | ZMHF for the corresponding scroll. cm is the 3-bit CHCN
// value (0=16-color, 1=256-color, ...).
//
// Disable conditions (PDF Table 5.2):
//
//	ZMQT=1 (any 1/4 reduction)              -> disable
//	ZMHF=1 with cm >= 1 (256+ colors)       -> disable
//	Otherwise                               -> enable
func reductionDisablesCompanion(zmBits, cm uint8) bool {
	zmqt := zmBits & 0x02
	zmhf := zmBits & 0x01
	if zmqt != 0 {
		return true
	}
	if zmhf != 0 && cm >= 1 {
		return true
	}
	return false
}

func (v *VDP2) RenderFrame() {
	width := int(v.activeWidth)
	height := int(v.activeLines)
	stride := width * 4

	// On a geometry change, blank the framebuffer: rows written under
	// the previous stride/height are misinterpreted under the new
	// layout and would display as garbage. Under LSMD=3 only one
	// field's rows are written per frame, so the first frame is also
	// line-doubled (fieldBootstrap below) to fill the other field.
	intl := v.effectiveInterlace()
	fieldBootstrap := false
	if width != v.prevWidth || height != v.prevHeight || intl != v.prevIntl {
		fieldBootstrap = intl == 3
		for i := 0; i < len(v.framebuffer); i += 4 {
			v.framebuffer[i] = 0
			v.framebuffer[i+1] = 0
			v.framebuffer[i+2] = 0
			v.framebuffer[i+3] = 0xFF
		}
	}
	v.prevWidth = width
	v.prevHeight = height
	v.prevIntl = intl

	disp := v.regs[vdp2TVMD] & 0x8000

	// DISP=0: blank output. The manual reads BDCLMD as selecting back
	// screen color over black for the standard display area when DISP
	// is 0, but on real hardware the analog video signal is suppressed
	// while DISP=0, so the picture is black on screen regardless of
	// BDCLMD. Games rely on this when they clear DISP during a
	// transition while still mutating back-screen / palette state in
	// VRAM — honoring BDCLMD here exposes those transient values as a
	// 1-frame color flash that does not occur on hardware. Under
	// LSMD=3 paint every displayed row so the screen-off state is
	// truly blank rather than leaving stale pixels from the prior
	// field visible.
	if disp == 0 {
		displayRows := v.DisplayHeight()
		for row := 0; row < displayRows; row++ {
			off := row * stride
			for x := 0; x < width; x++ {
				v.framebuffer[off] = 0
				v.framebuffer[off+1] = 0
				v.framebuffer[off+2] = 0
				v.framebuffer[off+3] = 0xFF
				off += 4
			}
		}
		return
	}

	// Rebuild the per-frame CRAM color cache. CRAM is written between
	// frames; it is static for the duration of this synchronous render.
	v.cramCacheValid = false
	v.buildCRAMCache()
	v.winMaskValid = false
	v.buildWindowMaskCache()

	// Read back screen registers
	bktau := v.regs[vdp2BKTAU]
	bktal := v.regs[vdp2BKTAL]
	bkclmd := (bktau >> 15) & 1
	bkAddr := ((uint32(bktau&0x07) << 16) | uint32(bktal)) * 2

	if bkclmd == 0 {
		// Single color mode: one color for the whole screen
		val := v.readVRAM16(bkAddr)
		r, g, b, a := rgb555ToRGBA(val)

		for y := 0; y < height; y++ {
			off := v.outRow(y) * stride
			for x := 0; x < width; x++ {
				v.framebuffer[off] = r
				v.framebuffer[off+1] = g
				v.framebuffer[off+2] = b
				v.framebuffer[off+3] = a
				off += 4
			}
		}
	} else {
		// Per-line color mode: one color per scanline
		for y := 0; y < height; y++ {
			val := v.readVRAM16(bkAddr + uint32(v.lineTableY(y))*2)
			r, g, b, a := rgb555ToRGBA(val)

			off := v.outRow(y) * stride
			for x := 0; x < width; x++ {
				v.framebuffer[off] = r
				v.framebuffer[off+1] = g
				v.framebuffer[off+2] = b
				v.framebuffer[off+3] = a
				off += 4
			}
		}
	}

	// When RBG1 is enabled, all NBG screens are disabled
	r1on := v.regs[vdp2BGON]&(1<<5) != 0
	if r1on {
		for s := 0; s < 4; s++ {
			clear(v.layerBufs[s])
		}
	} else {
		// NBG2/NBG3 availability per VDP2 manual: NBG2 cannot be
		// displayed when NBG0 is 2048/32,768/16.77M colors; NBG3 cannot
		// when NBG1 is 2048/32,768 colors or NBG0 is 16.77M colors. The
		// ZMCTL reduction rule (Sec 5.2 Table 5.2) is an additional
		// disable. Hi-res alone does not disable NBG2/NBG3.
		zmctl := v.regs[vdp2ZMCTL]
		chctla := v.regs[vdp2CHCTLA]
		nbg0cm := uint8((chctla >> 4) & 0x07)
		nbg1cm := uint8((chctla >> 12) & 0x03)
		disableNBG2 := nbg0cm >= 2 ||
			reductionDisablesCompanion(uint8(zmctl&0x03), nbg0cm)
		disableNBG3 := nbg1cm >= 2 || nbg0cm == 4 ||
			reductionDisablesCompanion(uint8((zmctl>>8)&0x03), nbg1cm)

		for s := 0; s < 4; s++ {
			if s == 2 && disableNBG2 {
				clear(v.layerBufs[s])
				continue
			}
			if s == 3 && disableNBG3 {
				clear(v.layerBufs[s])
				continue
			}
			v.renderNBG(s, v.layerBufs[s])
		}
	}

	// Render RBG0 if enabled
	if v.regs[vdp2BGON]&(1<<4) != 0 {
		v.renderRBG0(v.rbg0Buf)
	} else {
		clear(v.rbg0Buf)
		clear(v.rbg0LCBuf)
	}

	// Render RBG1 if enabled (requires RBG0 also enabled)
	if r1on {
		v.renderRBG1(v.rbg1Buf)
	} else {
		clear(v.rbg1Buf)
		clear(v.rbg1LCBuf)
	}

	// Composite layers onto framebuffer
	v.compositeFrame()

	// First frame after a geometry change under LSMD=3: copy each
	// rendered field row to its sibling row so the frame displays at
	// full height instead of one field interleaved with blank rows.
	if fieldBootstrap {
		for y := 0; y < height; y++ {
			src := v.outRow(y) * stride
			dst := (v.outRow(y) ^ 1) * stride
			copy(v.framebuffer[dst:dst+stride], v.framebuffer[src:src+stride])
		}
	}
}
