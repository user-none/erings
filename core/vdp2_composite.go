// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

// layerCCBit is packed into bit 31 of layer buffer entries to indicate
// per-pixel color calculation enable. Priority occupies bits 26:24 (3 bits).
const layerCCBit = screen.LayerCCBit

// compositeSpanSetup prepares framebuffer row y for span composition
// by screen.CompositeSpan, which composes pixels [x0,x1) of the row:
// the back screen color is the blend bottom, held as a value, and the
// layers composite over it. y is a field-line index; the output row is
// frameOutRowAt(y, field). Registers come from the line snapshot; the
// back-screen, line-window, and line-color table addresses are
// resolved here at line start. Layer buffers are consulted only for
// layers enabled this line, so stale rows in disabled layers' buffers
// are never read.
func (v *VDP2) compositeSpanSetup(y int) {
	v.compositeSpanSetupInto(&v.rowRecord(y).Comp, &v.lineWin, y, v.frame.field)
}

// compositeSpanSetupInto is compositeSpanSetup into cp with the row's
// window state built into lw, for the display line of field line y at
// field parity field (siblingLineSetup): the line's per-line tables,
// windows, output row, and sprite read are that parity's.
func (v *VDP2) compositeSpanSetupInto(cp *screen.CompositeParams, lw *lineWindows, y, field int) {
	fs := &v.frame
	regs := &fs.regs
	*cp = screen.CompositeParams{
		Width:          int32(fs.width),
		Y:              int32(y),
		FBRow:          int32(v.frameOutRowAt(y, field)),
		FieldBootstrap: flag32(fs.fieldBootstrap),
		CCMD:           fs.ccmd,
		CCRTMD:         fs.ccrtmd,
		EXCCEN:         fs.exccen,
		LCCCEN:         regs[vdp2CCCTL]&(1<<5) != 0,
		CCRLB:          uint32(regs[vdp2CCRLB]),
		LNCLEN:         uint32(regs[vdp2LNCLEN]),
		R1On:           fs.rbg1Active,
		HiRes:          fs.hiRes,
		CRAMMode:       uint32(fs.cramMode),
		CLOFEN:         uint32(regs[vdp2CLOFEN]),
		CLOFSL:         uint32(regs[vdp2CLOFSL]),
		COA:            [3]int32{int32(signExtend9(regs[vdp2COAR])), int32(signExtend9(regs[vdp2COAG])), int32(signExtend9(regs[vdp2COAB]))},
		COB:            [3]int32{int32(signExtend9(regs[vdp2COBR])), int32(signExtend9(regs[vdp2COBG])), int32(signExtend9(regs[vdp2COBB]))},
		SDCTL:          uint32(regs[vdp2SDCTL]),
		LayerRatio: [5]uint32{
			uint32(regs[vdp2CCRNA]) & 0x1F,
			uint32(regs[vdp2CCRNA]>>8) & 0x1F,
			uint32(regs[vdp2CCRNB]) & 0x1F,
			uint32(regs[vdp2CCRNB]>>8) & 0x1F,
			uint32(regs[vdp2CCRR]) & 0x1F,
		},
	}
	cp.LayerIsRGB = fs.layerIsRGB

	// Back screen color for this row. BKCLMD (BKTAU bit 15) selects one
	// color for the whole screen vs one color per line. Bit 15 of the
	// back screen data itself is ignored (VDP2 manual Figure 7.5); the
	// color fields display regardless of its value.
	wp := v.windowParamsAt(field)
	bktau := regs[vdp2BKTAU]
	bkAddr := ((uint32(bktau&0x07) << 16) | uint32(regs[vdp2BKTAL])) * 2
	if bktau&0x8000 != 0 {
		bkAddr += uint32(screen.LineTableY(wp, int32(y))) * 2
	}
	cp.BackRGB = screen.RGB555To888(uint32(v.readVRAM16(bkAddr)))

	// Line color screen table entry for the row: LCCLMD (LCTAU bit 15)
	// selects one entry per line, else a single entry.
	lctau := regs[vdp2LCTAU]
	cp.LCAddr = ((uint32(lctau&0x07) << 16) | uint32(regs[vdp2LCTAL])) * 2
	if lctau&0x8000 != 0 {
		cp.LCAddr += uint32(screen.LineTableY(wp, int32(y))) * 2
	}

	// Scroll layers enabled for the row: buffer, layer ID, tie rank
	// (Table 11.1: RBG0=4, NBG0=3, NBG1=2, NBG2=1, NBG3=0; RBG1 takes
	// NBG0's slot and rank), and the screen's register priority. A
	// disabled layer costs the row nothing. EXBG renders into NBG1's
	// buffer and competes in NBG1's slot; decodeLineState forced NBG1
	// off so NBG1's own span did not run.
	//
	// A layer's packed pixel priority equals its register priority
	// unless special priority mode (SFPRMD) rewrites the low bit per
	// dot. When no enabled layer uses it the row is static: the list
	// is sorted by priority then tie rank, so the candidates a pixel
	// gathers arrive in display order and the sprite candidate is
	// inserted at its rank instead of searching the candidates.
	nbgOn := fs.nbgOn
	nbgOn[1] = nbgOn[1] || fs.exbgOn
	var pris [5]int
	n := 0
	static := true
	if fs.rbg0On {
		cp.LayerBuf[0], cp.LayerID[0], cp.LayerTie[0] = screen.LayerRBG0, 4, 4
		pris[0] = int(fs.rbg0.priority)
		static = fs.rbg0.sfprmdMode == 0
		n = 1
	}
	for s := 0; s < 4; s++ {
		switch {
		case fs.rbg1Active && s == 0:
			if !fs.rbg1On {
				continue
			}
			cp.LayerBuf[n] = screen.LayerRBG1
			pris[n] = int(fs.rbg1.priority)
			static = static && fs.rbg1.sfprmdMode == 0
		case s == 1 && fs.exbgOn:
			cp.LayerBuf[n] = screen.LayerNBG1
			pris[n] = int(fs.exbgPri)
		default:
			if !nbgOn[s] {
				continue
			}
			cp.LayerBuf[n] = uint32(s)
			pris[n] = int(fs.nbg[s].priority)
			static = static && fs.nbg[s].sfprmdMode == 0
		}
		cp.LayerID[n], cp.LayerTie[n] = uint32(s), int32(3-s)
		n++
	}
	cp.NLayers = int32(n)
	cp.Static = static
	if static {
		for i := 1; i < n; i++ {
			for j := i; j > 0; j-- {
				a, c := j-1, j
				if pris[a] > pris[c] || (pris[a] == pris[c] && cp.LayerTie[a] > cp.LayerTie[c]) {
					break
				}
				cp.LayerBuf[a], cp.LayerBuf[c] = cp.LayerBuf[c], cp.LayerBuf[a]
				cp.LayerID[a], cp.LayerID[c] = cp.LayerID[c], cp.LayerID[a]
				cp.LayerTie[a], cp.LayerTie[c] = cp.LayerTie[c], cp.LayerTie[a]
				pris[a], pris[c] = pris[c], pris[a]
			}
		}
	}

	// Window state for the row: W0 and W1 split the row into runs over
	// which each layer's window result is constant; only the sprite
	// window term varies per pixel.
	v.buildLineWindows(lw, y, field)
	cp.Runs = lw.runs
	for run := int32(0); run < cp.Runs.N; run++ {
		rr := cp.Runs.Runs[run]
		for i, ctl := range lw.ctl {
			cp.RunState[run][i] = screen.WindowRunState(uint32(ctl), rr.W0, rr.W1)
		}
	}

	// Sprite decode and frame buffer row read for the row.
	v.buildCRAMCache()
	cp.Sprite = v.buildSpriteDecode()
	cp.SpriteRow = v.spriteRowAt(y, field)
	// The sprite window bit is the frame buffer MSB while SPWINEN
	// selects the sprite window; with it clear no pixel is inside.
	cp.SWBit = 0
	if regs[vdp2SPCTL]&0x0010 != 0 {
		cp.SWBit = 0x8000
		if v.lineFB.is8bpp {
			cp.SWBit = 0x80
		}
	}
	cp.SpriteCCOff = fs.hiRes && fs.cramMode >= 1 && !cp.Sprite.Mixed

}

// noSpriteFB is the buffer the composite binds for a row with no
// VDP1 frame buffer. The row's sprite read mode is off, so it is
// never read.
var noSpriteFB = make(compute.VDP1FB, compute.VDP1FBSize)

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
