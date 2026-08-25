// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

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
	if v.frame.hiRes {
		// Hi-res: bits[9:0] = H9:H0, all valid
		return int(raw & 0x03FF)
	}
	// Normal: bits[9:1] = H8:H0, bit 0 invalid
	return int(raw&0x03FE) >> 1
}

// windowY converts a raw window vertical coordinate (WPSY/WPEY) to a
// displayed-line value per Table 8.2. In double-density interlace the
// least significant bit is invalid (the value is V7:V0 in bits 8:1), so
// the boundary aligns to an even displayed line; otherwise all 9 bits
// (V8:V0) are valid.
func (v *VDP2) windowY(raw uint16) int {
	if v.frame.lsmd3 {
		return int(raw & 0x01FE)
	}
	return int(raw & 0x01FF)
}

// lineTableY returns the per-line table index for scanline y.
// Single-density interlace (LSMD=2) halves y since each entry covers
// two field-lines. Double-density interlace (LSMD=3) maps the caller's
// field-line y to the displayed-line index 2*y + field so the per-
// displayed-line tables (line scroll, line color, back screen, line
// window) are read at the correct offset. The programmed LSMD decides
// the index space: LSMD=3 with NBG mosaic enabled displays at
// single-density per VDP2 manual section 4.11, but the screen
// coordinate generation (and with it the per-displayed-line table
// walk) stays double-density. Interlace mode and field parity come
// from the BeginFrame latch.
func (v *VDP2) lineTableY(y int) int {
	if v.frame.lsmd3 {
		return y*2 + v.frame.field
	}
	if v.frame.effIntl == 2 {
		return y / 2
	}
	return y
}

// windowCtl returns the per-layer window control byte for the given
// layerID (0-3=NBG0-3, 4=RBG0, 5=sprite). ok is false for an invalid
// layerID.
func (v *VDP2) windowCtl(layerID int) (wctl uint8, ok bool) {
	switch layerID {
	case 0:
		return uint8(v.frame.regs[vdp2WCTLA]), true
	case 1:
		return uint8(v.frame.regs[vdp2WCTLA] >> 8), true
	case 2:
		return uint8(v.frame.regs[vdp2WCTLB]), true
	case 3:
		return uint8(v.frame.regs[vdp2WCTLB] >> 8), true
	case 4: // RBG0
		return uint8(v.frame.regs[vdp2WCTLC]), true
	case 5: // Sprite
		return uint8(v.frame.regs[vdp2WCTLC] >> 8), true
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

// isWindowMasked returns true if the pixel at (x,y) should be masked
// (treated as transparent) by window processing for the given layer.
// layerID: 0-3=NBG0-3, 4=RBG0, 5=sprite. The active window area is the
// masked area.
func (v *VDP2) isWindowMasked(x, y, layerID int) bool {
	if v.winMaskValid && uint(layerID) < 6 && v.winMaskSkip[layerID] {
		return v.winMaskVal[layerID]
	}
	wctl, ok := v.windowCtl(layerID)
	if !ok {
		return false
	}
	return v.evalWindow(wctl, x, y)
}

// isCCWindowActive checks if the color calculation window's active area
// includes pixel (x,y). Per VDP2 manual section 8.2, color calculation is
// NOT performed in the active area of the CC window, so a true return
// means CC should be suppressed at this pixel. The CC window control byte
// is the high byte of WCTLD.
func (v *VDP2) isCCWindowActive(x, y int) bool {
	return v.evalWindow(uint8(v.frame.regs[vdp2WCTLD]>>8), x, y)
}

// evalWindow evaluates the window-overlap logic for a window control byte
// and returns whether pixel (x,y) lies in the active area. The control
// byte layout (Sec 8.1) is shared by every per-layer transparent-process
// window and by the CC/RP windows: W0A=0x01, W0E=0x02, W1A=0x04,
// W1E=0x08, SWA=0x10, SWE=0x20, LOG=0x80 (0=OR, 1=AND). Area bits are
// 0=inside, 1=outside.
func (v *VDP2) evalWindow(wctl uint8, x, y int) bool {
	w0En := wctl&0x02 != 0
	w1En := wctl&0x08 != 0
	swEn := wctl&0x20 != 0
	logic := wctl&0x80 != 0 // 0=OR, 1=AND

	if !w0En && !w1En && !swEn {
		// Per Sec 8.1: with all enables off, LOG=0 -> whole screen
		// inactive, LOG=1 -> whole screen active.
		return logic
	}

	w0Area := wctl&0x01 != 0 // 0=inside, 1=outside
	w1Area := wctl&0x04 != 0
	swArea := wctl&0x10 != 0

	// Rectangle window Y bounds are in displayed-line space (9-bit fields
	// cover the full 480-line double-density range), while lineTableY and
	// readVDP1Pixel expect the caller's field-line y. Keep both.
	dispY := y
	if v.frame.lsmd3 {
		dispY = y*2 + v.frame.field
	}

	// Check W0. Both the line and rectangle windows are bounded
	// vertically by WPSY0/WPEY0 (Sec 8.1); the line window additionally
	// reads per-scanline horizontal bounds from VRAM while the rectangle
	// uses WPSX0/WPEX0. sy>ey means the whole screen is outside.
	var w0Inside bool
	if w0En {
		sy := v.windowY(v.frame.regs[vdp2WPSY0])
		ey := v.windowY(v.frame.regs[vdp2WPEY0])
		inY := sy <= ey && dispY >= sy && dispY <= ey
		if v.frame.regs[vdp2LWTA0U]&0x8000 != 0 {
			// Line window: per-scanline X boundaries from VRAM table
			lwta0 := ((uint32(v.frame.regs[vdp2LWTA0U]&0x07) << 16) | uint32(v.frame.regs[vdp2LWTA0L]&0xFFFE)) * 2
			entryAddr := lwta0 + uint32(v.lineTableY(y))*4
			sx := v.windowX(v.readVRAM16(entryAddr))
			ex := v.windowX(v.readVRAM16(entryAddr + 2))
			if inY && sx <= ex {
				w0Inside = x >= sx && x <= ex
			}
		} else {
			// Rectangle window
			sx := v.windowX(v.frame.regs[vdp2WPSX0])
			ex := v.windowX(v.frame.regs[vdp2WPEX0])
			if inY && sx <= ex {
				w0Inside = x >= sx && x <= ex
			}
		}
	}

	// Check W1, bounded vertically by WPSY1/WPEY1 the same way as W0.
	var w1Inside bool
	if w1En {
		sy := v.windowY(v.frame.regs[vdp2WPSY1])
		ey := v.windowY(v.frame.regs[vdp2WPEY1])
		inY := sy <= ey && dispY >= sy && dispY <= ey
		if v.frame.regs[vdp2LWTA1U]&0x8000 != 0 {
			lwta1 := ((uint32(v.frame.regs[vdp2LWTA1U]&0x07) << 16) | uint32(v.frame.regs[vdp2LWTA1L]&0xFFFE)) * 2
			entryAddr := lwta1 + uint32(v.lineTableY(y))*4
			sx := v.windowX(v.readVRAM16(entryAddr))
			ex := v.windowX(v.readVRAM16(entryAddr + 2))
			if inY && sx <= ex {
				w1Inside = x >= sx && x <= ex
			}
		} else {
			sx := v.windowX(v.frame.regs[vdp2WPSX1])
			ex := v.windowX(v.frame.regs[vdp2WPEX1])
			if inY && sx <= ex {
				w1Inside = x >= sx && x <= ex
			}
		}
	}

	// Check sprite window
	var swInside bool
	if swEn {
		swPix, swValid := v.readVDP1Pixel(x, y)
		if swValid {
			if v.lineFB.is8bpp {
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

	// Combine enabled windows. A single enabled window reduces to its own
	// active state under either operator, so no special case is needed.
	if logic {
		// AND: all enabled windows must be active
		active := true
		if w0En {
			active = active && w0Active
		}
		if w1En {
			active = active && w1Active
		}
		if swEn {
			active = active && swActive
		}
		return active
	}
	// OR: any enabled window active
	return (w0En && w0Active) || (w1En && w1Active) || (swEn && swActive)
}
