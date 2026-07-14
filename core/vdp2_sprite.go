// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// Shadow type constants returned by classifyShadow.
const (
	shadowNone      = 0
	shadowNormal    = 1 // Normal shadow: darkens scroll/back per SDCTL
	shadowMSBSprite = 2 // MSB shadow on sprites: darkens normal sprites below
	shadowMSBTransp = 3 // MSB transparent shadow: darkens scroll/back per SDCTL
	shadowMSBNull   = 4 // MSB transparent shadow with TPSDSL=0: dot transparent, no shadow
)

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
	if v.lineFB.data == nil {
		return 0, false
	}
	spX := x
	spY := y
	if v.lineSprRot.enabled {
		// Rotated read (VDP1 manual Sec 1.2): the coordinate is
		// rotation parameter A's line start plus per-dot movement, not
		// the screen position. Out-of-range coordinates read as
		// transparent via the bounds check below. Rotation is
		// prohibited in hi-res, so the X halving never applies.
		spX = int((v.lineSprRot.baseX + v.lineSprRot.dx*int64(x)) >> 10)
		spY = int((v.lineSprRot.baseY + v.lineSprRot.dy*int64(x)) >> 10)
	} else if v.frame.hiRes && !v.lineFB.is8bpp {
		spX = x / 2
	}
	if spX < 0 || spX >= v.lineFB.width || spY < 0 || spY >= v.lineFB.height {
		return 0, false
	}
	if v.lineFB.is8bpp {
		off := spY*v.lineFB.width + spX
		pix := uint16(v.lineFB.data[off])
		return pix, pix != 0
	}
	off := (spY*v.lineFB.width + spX) * 2
	pix := uint16(v.lineFB.data[off])<<8 | uint16(v.lineFB.data[off+1])
	return pix, pix != 0
}

// normalShadowDCMask is the dot-color-data field mask per sprite type
// (Figure 9.1). A Normal shadow has every DC bit set except the LSB, so
// a pixel is a Normal shadow when pixel&mask == mask&^1 (Figure 14.3).
var normalShadowDCMask = [16]uint16{
	0x07FF, 0x07FF, 0x07FF, 0x07FF, // types 0-3
	0x03FF, 0x07FF, 0x03FF, 0x01FF, // types 4-7
	0x007F, 0x003F, 0x003F, 0x003F, // types 8-B
	0x00FF, 0x00FF, 0x00FF, 0x00FF, // types C-F
}

// classifyShadow determines the shadow type of a VDP1 sprite pixel.
// Normal shadow (Figure 14.3) is judged from the dot-color field: every
// DC bit set except the LSB. MSB shadow applies only to types 2-7 with
// the MSB set. Normal shadow takes precedence over MSB shadow per Sec 14.
func (v *VDP2) classifyShadow(pixel uint16) int {
	if pixel == 0 {
		return shadowNone
	}
	spctl := v.frame.regs[vdp2SPCTL]
	sptype := spctl & 0x0F
	mixed := spctl&0x20 != 0

	// In SPCLMD=1 mixed palette+RGB mode, bit 15 is the format
	// discriminator (1=RGB direct, 0=palette), not the SD/MSB bit. Per
	// Sec 9.1 an RGB pixel's shadow bits are considered 0, so it never
	// shadows.
	if mixed && pixel&0x8000 != 0 {
		return shadowNone
	}

	// Normal shadow: per-type DC pattern, all 1 except LSB=0. It reads
	// only the dot-color field, so it still applies to palette pixels in
	// mixed mode - the mixed-mode "MSB as 0" rule (Sec 9.1) suppresses
	// only the MSB shadow bit, not the DC field.
	m := normalShadowDCMask[sptype]
	if pixel&m == m&^1 {
		return shadowNormal
	}

	// MSB shadow: only types 2-7 with MSB bit 15 set. In mixed mode the
	// MSB is the format discriminator and is processed as 0 for palette
	// pixels, so MSB shadow is unavailable.
	if mixed || sptype < 2 || sptype > 7 || pixel&0x8000 == 0 {
		return shadowNone
	}

	// MSB transparent shadow: bits[14:0] all zero (pixel=0x8000). The dot
	// itself is always transparent (Sec 14.1, Figure 14.4); TPSDSL only
	// selects whether it projects a shadow on the screens below. With
	// TPSDSL=0 the shadow is nullified but the dot must not become a
	// displayable palette-0 color.
	if pixel&0x7FFF == 0 {
		if v.frame.regs[vdp2SDCTL]&0x0100 != 0 {
			return shadowMSBTransp
		}
		return shadowMSBNull
	}

	// MSB sprite shadow: bits[14:0] non-zero.
	return shadowMSBSprite
}

func (v *VDP2) decodeSpritePixel(pixel uint16) (priority uint8, ccBits uint8, colorMSB bool, r, g, b uint8) {
	if pixel == 0 {
		return 0, 0, false, 0, 0, 0
	}

	spctl := v.frame.regs[vdp2SPCTL]
	spclmd := spctl&0x20 != 0

	// Check for RGB direct mode
	if spclmd && pixel&0x8000 != 0 {
		// In mixed mode (SPCLMD=1) an MSB-set pixel is RGB, but when the
		// sprite window is enabled (SPCTL SPWINEN, bit 4) the MSB is
		// repurposed as the sprite-window bit for sprite types 2-7 (VDP2
		// manual section 8.1, Sprite Window: the most significant frame
		// buffer bit selects the window when SPWINEN is set). A pixel whose
		// low 15 bits are all zero is then the transparent erase value, not
		// opaque RGB black. Hardware honors this even though the manual
		// discourages pairing SPWINEN with SPCLMD=1. Priority 0 marks the
		// dot transparent so lower layers show through.
		sptype := spctl & 0x0F
		spwinen := spctl&0x10 != 0
		if sptype >= 2 && sptype <= 7 && spwinen && pixel&0x7FFF == 0 {
			return 0, 0, false, 0, 0, 0
		}
		pri := uint8(v.frame.regs[vdp2PRISA]) & 0x07
		r, g, b = rgb555ToRGB(pixel)
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

	// Look up priority from PRISA-PRISD (4 registers, two 3-bit fields each)
	priority = uint8(v.pairedSpriteReg(vdp2PRISA, prBits, 0x07))

	// Apply sprite CRAM offset (SPCAOS at CRAOFB bits 6:4)
	spriteCRAMOff := uint32((v.frame.regs[vdp2CRAOFB] >> 4) & 0x07)
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
	if v.frame.regs[vdp2CCCTL]&(1<<6) == 0 {
		return false
	}
	spctl := v.frame.regs[vdp2SPCTL]
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
	return int(v.pairedSpriteReg(vdp2CCRSA, ccBits, 0x1F))
}

// pairedSpriteReg reads the sub-field selected by a sprite PR or CC bit
// value. sel>>1 picks one of four consecutive registers starting at base
// (PRISA-D or CCRSA-D); sel&1 picks the low field (bits of mask) or the
// high field (bits 8.. of mask). This mirrors Table 9.2's register/byte
// selection used for both priority and color-calculation ratio.
func (v *VDP2) pairedSpriteReg(base int, sel uint8, mask uint16) uint16 {
	reg := v.frame.regs[base+int(sel>>1)]
	if sel&1 == 0 {
		return reg & mask
	}
	return (reg >> 8) & mask
}
