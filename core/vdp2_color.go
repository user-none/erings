// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// rgb555ToRGB converts a Saturn RGB555 color value to RGBA888 components.
// Bit layout: bit 15 ignored, bits 14-10 blue, bits 9-5 green, bits 4-0 red.
func rgb555ToRGB(val uint16) (r, g, b uint8) {
	r5 := uint8(val & 0x1F)
	g5 := uint8((val >> 5) & 0x1F)
	b5 := uint8((val >> 10) & 0x1F)
	return (r5 << 3) | (r5 >> 2),
		(g5 << 3) | (g5 >> 2),
		(b5 << 3) | (b5 >> 2)
}

// readVRAM16 reads a big-endian 16-bit value from VDP2 VRAM at the given byte address.
func (v *VDP2) readVRAM16(addr uint32) uint16 {
	addr &= vdp2VRAMSize - 1
	hi := v.vram[addr]
	lo := v.vram[(addr+1)&(vdp2VRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
}

// readCRAM16 reads a big-endian 16-bit value from VDP2 CRAM.
func (v *VDP2) readCRAM16(addr uint32) uint16 {
	addr &= vdp2CRAMSize - 1
	hi := v.cram[addr]
	lo := v.cram[(addr+1)&(vdp2CRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
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
// the cramCache RGB/CC tables so the per-pixel color path becomes a table
// index. Called from BeginLine, it early-returns while cramCacheValid is
// set and rebuilds only after a CRAM write or a CRAM-mode (RAMCTL) change
// has cleared the flag, so it rebuilds lazily on a later line rather than
// once per frame or every line. Uses the same decode as
// readCRAMColor/readCRAMColorWithCC, so cached output is identical.
func (v *VDP2) buildCRAMCache() {
	if v.cramCacheValid {
		return
	}
	cm := v.frame.cramMode
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
			r, g, b := rgb555ToRGB(val)
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
	r, g, b, _ = v.readCRAMColorWithCC(colorAddr)
	return
}

// readCRAMColorWithCC reads a CRAM entry and also returns the CC bit (MSB).
// In modes 0/1 (RGB555): CC bit is bit 15 of the 16-bit CRAM word.
// In modes 2/3 (RGB888): CC bit is bit 15 of the upper word.
func (v *VDP2) readCRAMColorWithCC(colorAddr uint32) (r, g, b uint8, ccBit bool) {
	if v.cramCacheValid {
		i := colorAddr & v.cramCacheMask
		return v.cramCacheR[i], v.cramCacheG[i], v.cramCacheB[i], v.cramCacheCC[i]
	}
	cm := v.frame.cramMode
	switch cm {
	case 0:
		colorAddr &= 1023
		val := v.readCRAM16(colorAddr * 2)
		ccBit = val&0x8000 != 0
		r, g, b = rgb555ToRGB(val)
	case 1:
		colorAddr &= 2047
		val := v.readCRAM16(colorAddr * 2)
		ccBit = val&0x8000 != 0
		r, g, b = rgb555ToRGB(val)
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
	r, g, b, transparent, _ = v.lookupColorCC(dotColor, palette, cramOffset, colorMode, transpOff)
	return
}
