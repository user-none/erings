// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "github.com/user-none/erings/core/compute/screen"

// spriteRowAt describes the VDP1 frame buffer read for field-line y
// (screen.SpriteRow): the direct 16-bit or 8-bit row read, with
// hi-res halving the column for a 16-bit buffer, or the rotated read
// from rotation parameter A. The caller passes a field-line y in
// [0, activeLines). A native half holds one field's rows, so field
// line y reads physical row y with no Y halving. In a doubled half
// (fbHalfLayout) the row reads doubled row 2y + dil, which holds what
// physical row y holds natively. The rotated read keeps its coordinate
// in physical rows and carries the doubled flag and parity, so the
// pixel read takes that parity's row (screen.SpritePixelRotated).
//
// field is the read's field parity (siblingLineSetup). The sibling, at
// the other parity, reads doubled row 2y + 1 - dil in a doubled half,
// the other parity the same command list drew into that half. In a
// native half it reads physical row y as the row does, the native line
// doubling of the sprite layer. Its rotated read starts one display
// line of the per-line step (VDP1 manual Sec 1.2) from the row's,
// forward on field 0 and back on field 1, from the state the row's
// setup armed.
func (v *VDP2) spriteRowAt(y, field int) screen.SpriteRow {
	fb := &v.lineFB
	r := screen.SpriteRow{Width: int32(fb.width), Height: int32(fb.height)}
	lines := field - v.frame.field
	row, parity := y, fb.dil^field^v.frame.field
	if fb.doubled {
		row = 2*y + parity
	}
	switch {
	case fb.data == nil || y >= fb.height:
	case v.lineSprRot.enabled:
		r.Mode = screen.SpriteReadRotated
		r.BaseX = v.lineSprRot.baseX + int64(lines)*v.frame.sprRotA.dxst
		r.BaseY = v.lineSprRot.baseY + int64(lines)*v.frame.sprRotA.dyst
		r.DX, r.DY = v.lineSprRot.dx, v.lineSprRot.dy
		r.Is8bpp = fb.is8bpp
		if fb.doubled {
			r.Doubled, r.Parity = 1, uint32(parity)
		}
	case fb.is8bpp:
		r.Mode = screen.SpriteRead8
		r.Row = uint32(row * fb.width)
	default:
		r.Mode = screen.SpriteRead16
		r.Row = uint32(row*fb.width) * 2
		if v.frame.hiRes {
			r.Halve = 1
		}
	}
	return r
}

// spriteRegs gathers the sprite registers from the line snapshot for
// the render sprite decode.
func (v *VDP2) spriteRegs() screen.SpriteRegs {
	regs := &v.frame.regs
	return screen.SpriteRegs{
		SPCTL:  uint32(regs[vdp2SPCTL]),
		CRAOFB: uint32(regs[vdp2CRAOFB]),
		PRIS:   [4]uint32{uint32(regs[vdp2PRISA]), uint32(regs[vdp2PRISB]), uint32(regs[vdp2PRISC]), uint32(regs[vdp2PRISD])},
		CCRS:   [4]uint32{uint32(regs[vdp2CCRSA]), uint32(regs[vdp2CCRSB]), uint32(regs[vdp2CCRSC]), uint32(regs[vdp2CCRSD])},
		CCCTL:  uint32(regs[vdp2CCCTL]),
		SDCTL:  uint32(regs[vdp2SDCTL]),
	}
}

// buildSpriteDecode resolves the row's sprite decode from the line
// register snapshot.
func (v *VDP2) buildSpriteDecode() screen.SpriteDecode {
	return screen.BuildSpriteDecode(v.spriteRegs())
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
