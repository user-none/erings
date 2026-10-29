// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

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
		cfg.colorMode = uint8((chctla >> 12) & 0x03) // NBG1: 2 bits, no 16.77M mode
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

// nbgParams fills the render params for row y of NBG screen bg from
// the decoded configuration and the frame state, at field parity
// field (siblingLineSetup), which under double-density interlace
// places the row at display line 2y + field. The row's line scroll
// values are resolved by screen.ResolveNBGLine.
func (v *VDP2) nbgParams(cfg *nbgConfig, bg, y, field int) screen.NBGParams {
	p := screen.NBGParams{
		Width: int32(v.frame.width),
		Y:     int32(y),
		DispY: int32(y),
		Screen: screen.ScreenParams{
			ColorMode:     uint32(cfg.colorMode),
			Char2x2:       !cfg.charSize1x1,
			PNWord1:       cfg.pnWord1,
			AuxMode1:      cfg.auxMode1,
			PNCN:          uint32(cfg.pncnReg),
			Priority:      uint32(cfg.priority),
			CRAMOffset:    uint32(cfg.cramOffset),
			CRAMMode:      uint32(v.frame.cramMode),
			TranspOff:     cfg.transpOff,
			SFPRMD:        uint32(cfg.sfprmdMode),
			SFCCMD:        uint32(cfg.sfccmdMode),
			SFCode:        uint32(v.sfcodeForScreen(bg)),
			ScreenCC:      isCCEnabled(v.frame.regs[vdp2CCCTL], bg),
			Bitmap:        cfg.bitmapMode,
			BmpWidth:      int32(cfg.bmpWidth),
			BmpHeight:     int32(cfg.bmpHeight),
			BmpPalette:    uint32(cfg.bmpPalette),
			BmpSpecialPri: cfg.bmpSpecialPri,
			BmpSpecialCC:  cfg.bmpSpecialCC,
		},
		MapOffset:     cfg.mapOffset,
		PlanePagesH:   int32(cfg.planePagesH),
		PlanePagesV:   int32(cfg.planePagesV),
		MosaicH:       int32(cfg.mosaicH),
		MosaicV:       int32(cfg.mosaicV),
		VCSCEnabled:   cfg.vcscEnabled,
		VCSCTableAddr: cfg.vcscTableAddr,
		VCSCStride:    cfg.vcscStride,
		LineScrollX:   cfg.lineScrollX,
		LineScrollY:   cfg.lineScrollY,
		LineZoomX:     cfg.lineZoomX,
		LineInterval:  int32(cfg.lineInterval),
		LineTableAddr: cfg.lineTableAddr,
		ScrollXFP:     cfg.scrollXFP,
		ScrollYBaseFP: v.nbgYBaseAt(bg, field),
		IncXFP:        cfg.incXFP,
		IncYFP:        cfg.incYFP,
	}
	for i := range cfg.mapRegs {
		p.MapRegs[i] = uint32(cfg.mapRegs[i])
	}
	if v.frame.lsmd3 {
		p.DispY = int32(y*2 + field)
	}
	return p
}

// nbgYBaseAt returns NBG screen bg's vertical scroll base at field
// parity field (siblingLineSetup): the frame's base for the frame's
// field, the sibling base for the other (vdp2Frame.nbgYBaseFPSibling).
func (v *VDP2) nbgYBaseAt(bg, field int) int32 {
	if field != v.frame.field {
		return v.frame.nbgYBaseFPSibling[bg]
	}
	return v.frame.nbgYBaseFP[bg]
}

// nbgSpanSetup prepares row y of an NBG scroll screen: it builds the
// row's render params and resolves the line scroll table. y is a
// field-line index in 0..activeLines-1.
func (v *VDP2) nbgSpanSetup(cfg *nbgConfig, bg, y int) {
	v.nbgSpanSetupInto(&v.rowRecord(y).NBG[bg], cfg, bg, y, v.frame.field)
}

// nbgSpanSetupInto is nbgSpanSetup into pp at field parity field
// (siblingLineSetup).
func (v *VDP2) nbgSpanSetupInto(pp *screen.NBGParams, cfg *nbgConfig, bg, y, field int) {
	*pp = v.nbgParams(cfg, bg, y, field)
	screen.ResolveNBGLine((*compute.VDP2VRAM)(v.vram), pp)
}
