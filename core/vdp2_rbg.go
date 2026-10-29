// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

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

// ResetRotArm clears the per-scanline register-write capture tables
// (RPRCTL re-read arms and NBG vertical-scroll reloads). Called by
// RunFrame at frame start with both workers parked so it cannot race the
// SH-2's arming writes or the VDP worker's per-line consumption.
func (v *VDP2) ResetRotArm() {
	clear(v.rprArm[:])
	clear(v.scynSet[:])
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

// rbgRPWindow resolves the rotation parameter window (WCTLD low byte)
// for field line y at field parity field (siblingLineSetup): each
// window's enable and area bits and, when the row lies in the window,
// its X range. The rectangle window
// is bounded by its Y registers; the line window reads the row's
// table entry with no Y bound, per the current model. The render
// vdp2.RPWindowB selects parameter B in the active area (Sec 6).
func (v *VDP2) rbgRPWindow(y, field int) screen.RPWindow {
	regs := &v.frame.regs
	wctld := regs[vdp2WCTLD]
	var w screen.RPWindow
	w.En[0] = wctld&(1<<1) != 0
	w.En[1] = wctld&(1<<3) != 0
	w.Area[0] = wctld&1 != 0
	w.Area[1] = wctld&(1<<2) != 0
	w.LogicAnd = wctld&(1<<7) != 0
	wp := v.windowParamsAt(field)
	dispY := screen.DisplayY(wp, int32(y))
	for i := 0; i < 2; i++ {
		if !w.En[i] {
			continue
		}
		var sx, ex int32
		if regs[vdp2LWTA0U+2*i]&0x8000 != 0 {
			lwta := ((uint32(regs[vdp2LWTA0U+2*i]&0x07) << 16) | uint32(regs[vdp2LWTA0L+2*i]&0xFFFE)) * 2
			entry := lwta + uint32(screen.LineTableY(wp, int32(y)))*4
			sx = screen.LineWindowX(wp, uint32(v.readVRAM16(entry)))
			ex = screen.LineWindowX(wp, uint32(v.readVRAM16(entry+2)))
			w.On[i] = sx <= ex
		} else {
			sx = screen.WindowX(wp, uint32(regs[vdp2WPSX0+4*i]))
			sy := int32(regs[vdp2WPSY0+4*i] & 0x01FF)
			ex = screen.WindowX(wp, uint32(regs[vdp2WPEX0+4*i]))
			ey := int32(regs[vdp2WPEY0+4*i] & 0x01FF)
			w.On[i] = sx <= ex && sy <= ey && dispY >= sy && dispY <= ey
		}
		w.SX[i], w.EX[i] = sx, ex
	}
	return w
}

// rbgLineParams applies row y's RPRCTL re-reads to the frame state
// and fills the render params for the row: the stepped parameter
// values, the row coefficient address and line-start coefficient per
// parameter, the map configuration per parameter, and the screen
// configuration. rbg1 selects the RBG1 form: parameter B only, the
// NBG0 slot's color calculation enable and special function code.
//
// Per-line re-reads are gated by this scanline's self-clearing RPRCTL
// arm. An armed parameter re-reads from VRAM and rebases its per-line
// advance to this scanline; un-armed parameters keep stepping from
// their last read (computePerLine / rbgLineKAstFP).
func (v *VDP2) rbgLineParams(cfg *rbgConfig, rf *rbgFrame, y int, rbg1 bool) screen.RBGParams {
	bg := 4
	if rbg1 {
		bg = 0
	}
	p := screen.RBGParams{
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
		MosaicH:   int32(cfg.mosaicH),
		RPMode:    uint32(cfg.rpMode),
		CRKTE:     rf.crkte,
		CoefDotOK: rf.coefDotOK,
	}
	if rbg1 {
		p.RPMode = 1
	}
	if v.frame.hiRes {
		p.HcntShift = 1
	}
	if v.frame.lsmd3 {
		p.DispY = int32(y*2 + v.frame.field)
	}
	if p.RPMode == 3 {
		p.Window = v.rbgRPWindow(y, v.frame.field)
	}

	vcnt := int64(y)
	arm := v.rotArmBits(y)
	needA, needB := rf.needA, rf.needB
	if rbg1 {
		needA, needB = false, true
	}
	if needA {
		if arm&0x01 != 0 {
			rf.paramA.xst = signExtendFP(v.readVRAM16(rf.paramABase+0x00), v.readVRAM16(rf.paramABase+0x02), 12)
			rf.lastVcntXA = vcnt
		}
		if arm&0x02 != 0 {
			rf.paramA.yst = signExtendFP(v.readVRAM16(rf.paramABase+0x04), v.readVRAM16(rf.paramABase+0x06), 12)
			rf.lastVcntYA = vcnt
		}
		if arm&0x04 != 0 {
			rf.paramA.kast = decodeFPkast(v.readVRAM16(rf.paramABase+0x54), v.readVRAM16(rf.paramABase+0x56))
			rf.lastVcntKAA = vcnt
		}
	}
	if needB {
		if arm&0x0100 != 0 {
			rf.paramB.xst = signExtendFP(v.readVRAM16(rf.paramBBase+0x00), v.readVRAM16(rf.paramBBase+0x02), 12)
			rf.lastVcntXB = vcnt
		}
		if arm&0x0200 != 0 {
			rf.paramB.yst = signExtendFP(v.readVRAM16(rf.paramBBase+0x04), v.readVRAM16(rf.paramBBase+0x06), 12)
			rf.lastVcntYB = vcnt
		}
		if arm&0x0400 != 0 {
			rf.paramB.kast = decodeFPkast(v.readVRAM16(rf.paramBBase+0x54), v.readVRAM16(rf.paramBBase+0x56))
			rf.lastVcntKAB = vcnt
		}
	}

	// Cell geometry shared by both parameters.
	charPx := 8
	mapDim := 64
	if !cfg.charSize1x1 {
		charPx = 16
		mapDim = 32
	}
	planeCellsH := mapDim * cfg.planePagesH
	planeCellsV := mapDim * cfg.planePagesV

	// Parameter A (RBG0 only).
	if needA {
		pa := &p.Param[0]
		pa.Xsp, pa.Ysp = computePerLine(&rf.paramA, vcnt, rf.lastVcntXA, rf.lastVcntYA)
		pa.DX, pa.DY = rf.pfA.dxFP, rf.pfA.dyFP
		pa.Xp, pa.Yp = rf.pfA.xp, rf.pfA.yp
		pa.Kx, pa.Ky = rf.paramA.kx, rf.paramA.ky
		pa.DKAx = rf.paramA.dkax
		pa.CoefEn = rf.coefEnA
		pa.CoefOneWord = rf.coefOneWordA
		pa.CoefMode = uint32(rf.coefModeA)
		pa.KLCE = rf.klceA
		pa.KTAOF = uint32(rf.ktaof)
		if rf.coefEnA {
			// Row KAst in .10 FP (Sec 6.1: KAst + dKAst x (Vcnt -
			// Vcnt_when_read)), kept as FP so the per-dot dKAx x Hcnt
			// term accumulates before integer extraction. The
			// line-start coefficient works from any bank (Sec 6.2); a
			// dot whose address falls in an undesignated bank keeps it.
			pa.LineKAst = rbgLineKAstFP(rf.paramA.kast, rf.paramA.dkast, vcnt, rf.lastVcntKAA)
			addr := screen.CoefAddr(pa.LineKAst, uint32(rf.ktaof), rf.coefOneWordA)
			val, msb, lc := screen.Coefficient((*compute.VDP2VRAM)(v.vram), (*compute.CRAM)(v.cram), addr, rf.coefOneWordA, rf.coefModeA == 3, rf.crkte)
			pa.LineCoef, pa.LineCoefMSB, pa.LineCoefLC = val, msb, lc
		}
		pa.ScreenOver = uint32(cfg.screenOver)
		pa.OVPNR = uint32(v.frame.regs[vdp2OVPNRA])
		pa.MapOffset = cfg.mapOffset
		for i := range cfg.mapRegs {
			pa.MapRegs[i] = uint32(cfg.mapRegs[i])
		}
		pa.PlanePagesH = int32(cfg.planePagesH)
		pa.PlaneCellsH, pa.PlaneCellsV = int32(planeCellsH), int32(planeCellsV)
		pa.TotalPixH, pa.TotalPixV = int32(planeCellsH*4*charPx), int32(planeCellsV*4*charPx)
		pa.BitmapBase = cfg.mapOffset * 0x20000
	}

	// Parameter B: RBG0 takes its map configuration from the frame
	// state (MPxxRB, MPOFR, PLSZ), RBG1 from its own configuration.
	pb := &p.Param[1]
	pb.PlanePagesH = int32(cfg.planePagesH)
	pb.PlaneCellsH, pb.PlaneCellsV = int32(planeCellsH), int32(planeCellsV)
	pb.TotalPixH, pb.TotalPixV = int32(planeCellsH*4*charPx), int32(planeCellsV*4*charPx)
	if needB {
		pb.Xsp, pb.Ysp = computePerLine(&rf.paramB, vcnt, rf.lastVcntXB, rf.lastVcntYB)
		pb.DX, pb.DY = rf.pfB.dxFP, rf.pfB.dyFP
		pb.Xp, pb.Yp = rf.pfB.xp, rf.pfB.yp
		pb.Kx, pb.Ky = rf.paramB.kx, rf.paramB.ky
		pb.DKAx = rf.paramB.dkax
		pb.CoefEn = rf.coefEnB
		pb.CoefOneWord = rf.coefOneWordB
		pb.CoefMode = uint32(rf.coefModeB)
		pb.KLCE = rf.klceB
		pb.KTAOF = uint32(rf.ktaof >> 8)
		if rf.coefEnB {
			pb.LineKAst = rbgLineKAstFP(rf.paramB.kast, rf.paramB.dkast, vcnt, rf.lastVcntKAB)
			addr := screen.CoefAddr(pb.LineKAst, uint32(rf.ktaof>>8), rf.coefOneWordB)
			val, msb, lc := screen.Coefficient((*compute.VDP2VRAM)(v.vram), (*compute.CRAM)(v.cram), addr, rf.coefOneWordB, rf.coefModeB == 3, rf.crkte)
			pb.LineCoef, pb.LineCoefMSB, pb.LineCoefLC = val, msb, lc
		}
		if rbg1 {
			pb.ScreenOver = uint32(cfg.screenOver)
			pb.MapOffset = cfg.mapOffset
			for i := range cfg.mapRegs {
				pb.MapRegs[i] = uint32(cfg.mapRegs[i])
			}
			pb.BitmapBase = cfg.mapOffset * 0x20000
		} else {
			pb.ScreenOver = uint32(rf.screenOverB)
			pb.MapOffset = rf.mapOffsetB
			for i := range rf.mapRegsB {
				pb.MapRegs[i] = uint32(rf.mapRegsB[i])
			}
			pb.PlanePagesH = int32(rf.planePagesHB)
			pcH, pcV := mapDim*rf.planePagesHB, mapDim*rf.planePagesVB
			pb.PlaneCellsH, pb.PlaneCellsV = int32(pcH), int32(pcV)
			pb.TotalPixH, pb.TotalPixV = int32(pcH*4*charPx), int32(pcV*4*charPx)
			pb.BitmapBase = rf.mapOffsetB * 0x20000
		}
	} else {
		pb.ScreenOver = uint32(rf.screenOverB)
		pb.MapOffset = rf.mapOffsetB
		for i := range rf.mapRegsB {
			pb.MapRegs[i] = uint32(rf.mapRegsB[i])
		}
		pb.BitmapBase = rf.mapOffsetB * 0x20000
	}
	pb.OVPNR = uint32(v.frame.regs[vdp2OVPNRB])
	return p
}

// rbg0SpanSetup prepares row y of the RBG0 rotation scroll screen: it
// applies the row's re-reads and builds the render params. y is a
// field-line index in 0..activeLines-1.
func (v *VDP2) rbg0SpanSetup(cfg *rbgConfig, rf *rbgFrame, y int) {
	pp := &v.rowRecord(y).RBG[0]
	*pp = v.rbgLineParams(cfg, rf, y, false)
	screen.PrepareRBGRow(pp)
}

// rbg1SpanSetup prepares row y of RBG1, which rotates by parameter B
// only and takes NBG0's slot.
func (v *VDP2) rbg1SpanSetup(cfg *rbgConfig, rf *rbgFrame, y int) {
	pp := &v.rowRecord(y).RBG[1]
	*pp = v.rbgLineParams(cfg, rf, y, true)
	screen.PrepareRBGRow(pp)
}
