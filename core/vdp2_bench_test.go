// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"
	"time"
)

// VDP2 render benchmarks. Every scene is programmed through the
// bus-facing entry points (Write, WriteVRAM, WriteCRAM, and the VDP1
// framebuffer writes for the sprite layer) and rendered through the
// frame loop's per-line sequence (BeginFrame, BeginLine, RenderTo,
// EndLine, EndFrame). Nothing here names a renderer-internal function,
// so the numbers stay comparable across refactors of the renderer.
//
// Each benchmark reports nanoseconds per output pixel, which stays
// comparable across display widths and interlace modes; ns/op is the
// per-frame cost.
//
// TestBenchVDP2Scenes pins every scene's output. A benchmark number is
// comparable with an earlier one only while that test passes: a scene
// whose output changed measures different work.

// VRAM layout shared by the scenes. Pattern name pages sit in the low
// 256 KB, tables in the 0x14000-0x1FFFF gap between pages, character
// data from 0x40000, and bitmaps at map offset 2 or 3.
const (
	benchPageNBG0    = 0x00000 // 2-word page 0
	benchPageRBG0    = 0x08000 // 2-word page 2
	benchPageRBG1    = 0x0C000 // 2-word page 3
	benchPageNBG1    = 0x10000 // 2-word page 4
	benchRotTable    = 0x14000 // RPTAL 0xA000
	benchCoefTable   = 0x18000 // KAst 0x6000
	benchLineScroll  = 0x1C000 // LSTA0L 0xE000
	benchLineWindow0 = 0x1C800 // LWTA0L 0xE400
	benchLineWindow1 = 0x1D000 // LWTA1L 0xE800
	benchVCSTable    = 0x1D800 // VCSTAL 0xEC00
	benchBackTable   = 0x1E000 // BKTAL 0xF000
	benchLineColor   = 0x1E800 // LCTAL 0xF400
	benchPageNBG2    = 0x20000 // 1-word page 16
	benchPageNBG3    = 0x30000 // 2-word page 12

	// Character numbers of the tile sets (VRAM 0x40000 onward). A
	// 16-color tile is one 0x20-byte unit, a 256-color tile two, and
	// a 32K-color tile four.
	benchChar4     = 0x2000
	benchChar8     = 0x2010
	benchChar32K   = 0x2030
	benchTileCount = 8

	// PNCN value for a 1-word page of the 256-color tile set: 1-word
	// mode with the character number's upper bits from bits 4:0.
	benchPNCN1Word = 0x8000 | benchChar8>>10
)

func benchReg(v *VDP2, idx int, val uint16) {
	v.Write(uint32(idx)*2, val)
}

func benchRegOr(v *VDP2, idx int, bits uint16) {
	benchReg(v, idx, v.Read(uint32(idx)*2)|bits)
}

func benchCRAM(v *VDP2, entry uint32, val uint16) {
	v.WriteCRAM16(entry*2, val)
}

// benchRot32 writes one 32-bit rotation parameter table field.
func benchRot32(v *VDP2, base, off uint32, hi, lo uint16) {
	v.WriteVRAM16(base+off, hi)
	v.WriteVRAM16(base+off+2, lo)
}

// benchVDP2 returns a VDP2 with the display on, unity NBG0/NBG1
// coordinate increments, CRAM entries 1-255 filled with distinct
// colors, and the three tile sets written.
func benchVDP2() *VDP2 {
	v := NewVDP2(NewSCU())
	benchReg(v, vdp2TVMD, 0x8000)
	benchReg(v, vdp2ZMXIN0, 0x0001)
	benchReg(v, vdp2ZMYIN0, 0x0001)
	benchReg(v, vdp2ZMXIN1, 0x0001)
	benchReg(v, vdp2ZMYIN1, 0x0001)
	for i := uint32(1); i < 256; i++ {
		benchCRAM(v, i, uint16((i*0x0A3B+0x0421)&0x7FFF))
	}
	benchWriteTiles(v)
	return v
}

func benchDot4(x, y, k int) uint8 {
	if (x+y+k)%4 == 0 {
		return 0
	}
	return uint8(1 + (x*(k+1)+y)%15)
}

func benchDot8(x, y, k int) uint8 {
	if (x+y+k)%5 == 0 {
		return 0
	}
	return uint8(16 + (x*3+y*5+k*7)%200)
}

func benchDot32K(x, y, k int) uint16 {
	if (x+y+k)%3 == 0 {
		return 0
	}
	return 0x8000 | uint16((x*4+k)&0x1F) | uint16((y*4)&0x1F)<<5 | uint16((x+y+k*3)&0x1F)<<10
}

// benchWriteTiles writes the 16-color, 256-color, and 32K-color tile
// sets: benchTileCount tiles each with a distinct pattern and
// transparent holes.
func benchWriteTiles(v *VDP2) {
	for k := 0; k < benchTileCount; k++ {
		b4 := uint32(benchChar4+k) * 0x20
		b8 := uint32(benchChar8+2*k) * 0x20
		b32 := uint32(benchChar32K+4*k) * 0x20
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x += 2 {
				v.WriteVRAM(b4+uint32(y*4+x/2), benchDot4(x, y, k)<<4|benchDot4(x+1, y, k))
			}
			for x := 0; x < 8; x++ {
				v.WriteVRAM(b8+uint32(y*8+x), benchDot8(x, y, k))
				v.WriteVRAM16(b32+uint32(y*8+x)*2, benchDot32K(x, y, k))
			}
		}
	}
}

// benchPage2Word fills a 64x64-cell 2-word page with the tile set at
// charBase (tile k at charBase + step*k), palette numbers 1-3 and
// flips varying by cell.
func benchPage2Word(v *VDP2, addr uint32, charBase, step int) {
	for cy := 0; cy < 64; cy++ {
		for cx := 0; cx < 64; cx++ {
			k := (cx*3 + cy) % benchTileCount
			msw := uint16(1+(cx/8+cy/8)%3) | uint16(cx&1)<<15 | uint16(cy&1)<<14
			e := addr + uint32(cy*64+cx)*4
			v.WriteVRAM16(e, msw)
			v.WriteVRAM16(e+2, uint16(charBase+step*k))
		}
	}
}

// benchPage1Word fills a 64x64-cell 1-word page with the 256-color tile
// set: the character number's low 10 bits in the entry (the rest from
// benchPNCN1Word) and flips in bits 11:10.
func benchPage1Word(v *VDP2, addr uint32) {
	for cy := 0; cy < 64; cy++ {
		for cx := 0; cx < 64; cx++ {
			k := (cx*3 + cy) % benchTileCount
			ch := benchChar8 + 2*k
			e := uint16(ch&0x3FF) | uint16(cx&1)<<11 | uint16(cy&1)<<10
			v.WriteVRAM16(addr+uint32(cy*64+cx)*2, e)
		}
	}
}

// benchBitmap8 writes a 512x256 256-color bitmap with transparent
// holes.
func benchBitmap8(v *VDP2, addr uint32) {
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			d := uint8(x*3 + y)
			if (x+y)%7 == 0 {
				d = 0
			}
			v.WriteVRAM(addr+uint32(y*512+x), d)
		}
	}
}

// benchBitmap32K writes a 512x256 32K-color bitmap with transparent
// holes.
func benchBitmap32K(v *VDP2, addr uint32) {
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			v.WriteVRAM16(addr+uint32(y*512+x)*2, benchDot32K(x, y, (x/8+y/8)%benchTileCount))
		}
	}
}

// NBG layers. Each enables one screen as a cell layer on its own page
// with a non-zero scroll so cells straddle the screen edge.

func benchNBG0Cell(v *VDP2, chctla, pri uint16, charBase, step int) {
	benchRegOr(v, vdp2BGON, 0x0001)
	benchRegOr(v, vdp2CHCTLA, chctla)
	benchReg(v, vdp2PNCN0, 0x0000)
	benchReg(v, vdp2MPABN0, 0x0000)
	benchReg(v, vdp2MPCDN0, 0x0000)
	benchRegOr(v, vdp2PRINA, pri)
	benchReg(v, vdp2SCXIN0, 3)
	benchReg(v, vdp2SCXDN0, 0x8000)
	benchReg(v, vdp2SCYIN0, 5)
	benchPage2Word(v, benchPageNBG0, charBase, step)
}

func benchNBG0Cell4(v *VDP2, pri uint16)   { benchNBG0Cell(v, 0x0000, pri, benchChar4, 1) }
func benchNBG0Cell8(v *VDP2, pri uint16)   { benchNBG0Cell(v, 0x0010, pri, benchChar8, 2) }
func benchNBG0Cell32K(v *VDP2, pri uint16) { benchNBG0Cell(v, 0x0030, pri, benchChar32K, 4) }

func benchNBG1Cell8(v *VDP2, pri uint16) {
	benchRegOr(v, vdp2BGON, 0x0002)
	benchRegOr(v, vdp2CHCTLA, 0x1000)
	benchReg(v, vdp2PNCN1, 0x0000)
	benchReg(v, vdp2MPABN1, 0x0404)
	benchReg(v, vdp2MPCDN1, 0x0404)
	benchRegOr(v, vdp2PRINA, pri<<8)
	benchReg(v, vdp2SCXIN1, 7)
	benchReg(v, vdp2SCYIN1, 2)
	benchPage2Word(v, benchPageNBG1, benchChar8, 2)
}

func benchNBG2Cell8OneWord(v *VDP2, pri uint16) {
	benchRegOr(v, vdp2BGON, 0x0004)
	benchRegOr(v, vdp2CHCTLB, 0x0002)
	benchReg(v, vdp2PNCN2, benchPNCN1Word)
	benchReg(v, vdp2MPABN2, 0x1010)
	benchReg(v, vdp2MPCDN2, 0x1010)
	benchRegOr(v, vdp2PRINB, pri)
	benchReg(v, vdp2SCXN2, 1)
	benchReg(v, vdp2SCYN2, 9)
	benchPage1Word(v, benchPageNBG2)
}

func benchNBG3Cell4(v *VDP2, pri uint16) {
	benchRegOr(v, vdp2BGON, 0x0008)
	benchReg(v, vdp2PNCN3, 0x0000)
	benchReg(v, vdp2MPABN3, 0x0C0C)
	benchReg(v, vdp2MPCDN3, 0x0C0C)
	benchRegOr(v, vdp2PRINB, pri<<8)
	benchReg(v, vdp2SCXN3, 4)
	benchReg(v, vdp2SCYN3, 6)
	benchPage2Word(v, benchPageNBG3, benchChar4, 1)
}

// benchFixed is a rotation parameter table field as its two words.
type benchFixed struct{ hi, lo uint16 }

// benchRotation writes a rotation parameter set: matrix A, B, D, E,
// start point (xst, yst), unity DYst, DX, kx, and ky.
func benchRotation(v *VDP2, base uint32, a, b, d, e benchFixed, xst, yst uint16) {
	benchRot32(v, base, 0x00, xst, 0x0000)
	benchRot32(v, base, 0x04, yst, 0x0000)
	benchRot32(v, base, 0x10, 0x0001, 0x0000)
	benchRot32(v, base, 0x14, 0x0001, 0x0000)
	benchRot32(v, base, 0x1C, a.hi, a.lo)
	benchRot32(v, base, 0x20, b.hi, b.lo)
	benchRot32(v, base, 0x28, d.hi, d.lo)
	benchRot32(v, base, 0x2C, e.hi, e.lo)
	benchRot32(v, base, 0x4C, 0x0001, 0x0000)
	benchRot32(v, base, 0x50, 0x0001, 0x0000)
}

// benchRotation30 is a 30-degree rotation: A = E = 0.866, B = -0.5,
// D = 0.5.
func benchRotation30(v *VDP2, base uint32) {
	benchRotation(v, base,
		benchFixed{0x0000, 0xDDC0}, benchFixed{0x000F, 0x8000},
		benchFixed{0x0000, 0x8000}, benchFixed{0x0000, 0xDDC0}, 24, 40)
}

// benchRotationMinus20 is a -20-degree rotation: A = E = 0.940,
// B = 0.342, D = -0.342.
func benchRotationMinus20(v *VDP2, base uint32) {
	benchRotation(v, base,
		benchFixed{0x0000, 0xF092}, benchFixed{0x0000, 0x578E},
		benchFixed{0x000F, 0xA872}, benchFixed{0x0000, 0xF092}, 64, 16)
}

// benchRBG0Common sets the RBG0 registers shared by the cell and bitmap
// forms: parameter A at benchRotTable, no coefficients.
func benchRBG0Common(v *VDP2, pri uint16) {
	benchRegOr(v, vdp2BGON, 0x0010)
	benchReg(v, vdp2PRIR, pri)
	benchReg(v, vdp2RPMD, 0x0000)
	benchReg(v, vdp2RPRCTL, 0x0000)
	benchReg(v, vdp2KTCTL, 0x0000)
	benchReg(v, vdp2RPTAU, 0x0000)
	benchReg(v, vdp2RPTAL, 0xA000)
	benchRotation30(v, benchRotTable)
}

// benchRBG0Cell enables RBG0 as a 16-color 2-word cell screen on page
// 2, rotated 30 degrees.
func benchRBG0Cell(v *VDP2, pri uint16) {
	benchRBG0Common(v, pri)
	benchReg(v, vdp2PNCR, 0x0000)
	benchReg(v, vdp2MPOFR, 0x0000)
	for i := 0; i < 8; i++ {
		benchReg(v, vdp2MPABRA+i, 0x0202)
	}
	benchPage2Word(v, benchPageRBG0, benchChar4, 1)
}

// benchRBG0Bitmap8 enables RBG0 as a 512x256 256-color bitmap at map
// offset 3, rotated 30 degrees.
func benchRBG0Bitmap8(v *VDP2, pri uint16) {
	benchRBG0Common(v, pri)
	benchRegOr(v, vdp2CHCTLB, 0x1200)
	benchReg(v, vdp2BMPNB, 0x0000)
	benchReg(v, vdp2MPOFR, 0x0003)
	benchBitmap8(v, 3*0x20000)
}

// benchRBG0Coefficients enables a per-dot coefficient table on
// parameter A: 512 two-word entries scaling kx and ky from 0.75
// upward, one entry per line plus 1/256 entry per dot, with VRAM
// bank A0 designated coefficient RAM.
func benchRBG0Coefficients(v *VDP2) {
	benchReg(v, vdp2RAMCTL, 0x0001)
	benchReg(v, vdp2KTCTL, 0x0001)
	benchReg(v, vdp2KTAOF, 0x0000)
	benchRot32(v, benchRotTable, 0x54, 0x6000, 0x0000)
	benchRot32(v, benchRotTable, 0x58, 0x0001, 0x0000)
	benchRot32(v, benchRotTable, 0x5C, 0x0000, 0x0100)
	for i := uint32(0); i < 512; i++ {
		coef := uint32(49152 + i*146)
		v.WriteVRAM16(benchCoefTable+i*4, uint16(coef>>16))
		v.WriteVRAM16(benchCoefTable+i*4+2, uint16(coef))
	}
}

// benchRBG1Cell enables RBG1 as a 16-color 2-word cell screen on page
// 3, rotated -20 degrees by parameter B.
func benchRBG1Cell(v *VDP2, pri uint16) {
	benchRegOr(v, vdp2BGON, 0x0020)
	benchReg(v, vdp2PNCN0, 0x0000)
	for i := 0; i < 8; i++ {
		benchReg(v, vdp2MPABRB+i, 0x0303)
	}
	benchRegOr(v, vdp2PRINA, pri)
	benchRotationMinus20(v, benchRotTable|0x80)
	benchPage2Word(v, benchPageRBG1, benchChar4, 1)
}

// benchFBView builds the VDP2 sprite input from a VDP1 the way the
// frame loop does.
func benchFBView(v1 *VDP1) vdp1FBView {
	return vdp1FBView{
		data:    v1.DisplayFB(0),
		is8bpp:  v1.Is8bpp(),
		width:   v1.FBWidth(),
		height:  v1.FBHeight(),
		rotated: v1.FBRotated(),
	}
}

// benchSpritePixel is the type 0 sprite value (PR bits 15:14, CC bits
// 13:11, DC bits 10:0) at (x, y): two thirds of the 8x8 blocks are
// filled, priority and color calculation bits vary by block, and a
// scattering of dots carry the normal-shadow code.
func benchSpritePixel(x, y int) uint16 {
	if (x/8+y/8)%3 == 0 {
		return 0
	}
	pr := uint16(x/32) & 3
	cc := uint16(y/32) & 7
	dc := uint16(1 + (x*7+y*3)%254)
	if (x+y)%37 == 0 {
		dc = 0x7FF
	}
	return pr<<14 | cc<<11 | dc
}

// benchSpriteFB16 fills a 16-bit VDP1 framebuffer with
// benchSpritePixel through the VDP1 framebuffer writes and swaps it to
// the display side.
func benchSpriteFB16() vdp1FBView {
	v1 := NewVDP1(NewSCU())
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			if p := benchSpritePixel(x, y); p != 0 {
				v1.WriteFB16(uint32((y*512+x)*2), p)
			}
		}
	}
	v1.VBlankIn()
	return benchFBView(v1)
}

// benchSpriteFB8 fills an 8-bit (TVM 001, 1024 wide) VDP1 framebuffer
// with type 8 values (PR bit 7, DC bits 6:0).
func benchSpriteFB8() vdp1FBView {
	v1 := NewVDP1(NewSCU())
	v1.Write(0x00, 0x0001)
	for y := 0; y < 256; y++ {
		for x := 0; x < 1024; x++ {
			if (x/8+y/8)%3 == 0 {
				continue
			}
			p := uint8(1 + (x*5+y)%127)
			if (x/32)&1 != 0 {
				p |= 0x80
			}
			v1.WriteFB(uint32(y*1024+x), p)
		}
	}
	v1.VBlankIn()
	return benchFBView(v1)
}

// benchSpriteRegs sets sprite type 0 with priorities 6, 2, 4, 1 in
// registers 0-3.
func benchSpriteRegs(v *VDP2) {
	benchReg(v, vdp2SPCTL, 0x0000)
	benchReg(v, vdp2PRISA, 0x0206)
	benchReg(v, vdp2PRISB, 0x0104)
}

// benchSixLayers builds the six-layer scene: NBG0 16-color (7), NBG1
// 256-color (5), NBG2 256-color 1-word (4), NBG3 16-color (4, ties
// with NBG2), RBG0 rotated (3), and the 16-bit sprite layer.
func benchSixLayers() (*VDP2, vdp1FBView) {
	v := benchVDP2()
	benchNBG0Cell4(v, 7)
	benchNBG1Cell8(v, 5)
	benchNBG2Cell8OneWord(v, 4)
	benchNBG3Cell4(v, 4)
	benchRBG0Cell(v, 3)
	benchSpriteRegs(v)
	return v, benchSpriteFB16()
}

// benchWindowW0 sets W0 to x 40-199, y 20-180 and applies it to NBG0
// (inside), NBG1 (outside), and RBG0 (inside).
func benchWindowW0(v *VDP2) {
	benchReg(v, vdp2WPSX0, 80)
	benchReg(v, vdp2WPEX0, 398)
	benchReg(v, vdp2WPSY0, 20)
	benchReg(v, vdp2WPEY0, 180)
	benchReg(v, vdp2WCTLA, 0x0302)
	benchReg(v, vdp2WCTLC, 0x0002)
}

// benchWindowsW0W1Sprite adds W1 at x 100-300, y 50-200 to the W0
// scene: NBG1 takes W0 AND W1, NBG2 W0 OR W1, NBG3 the sprite window.
func benchWindowsW0W1Sprite(v *VDP2) {
	benchWindowW0(v)
	benchReg(v, vdp2WPSX1, 200)
	benchReg(v, vdp2WPEX1, 600)
	benchReg(v, vdp2WPSY1, 50)
	benchReg(v, vdp2WPEY1, 200)
	benchReg(v, vdp2WCTLA, 0x8A02)
	benchReg(v, vdp2WCTLB, 0x200A)
}

// benchLineWindows switches W0 and W1 to per-line tables with X ranges
// that vary by line; every sixteenth W1 line is disabled (start past
// end).
func benchLineWindows(v *VDP2) {
	benchWindowsW0W1Sprite(v)
	benchReg(v, vdp2LWTA0U, 0x8000)
	benchReg(v, vdp2LWTA0L, 0xE400)
	benchReg(v, vdp2LWTA1U, 0x8000)
	benchReg(v, vdp2LWTA1L, 0xE800)
	for l := uint32(0); l < 512; l++ {
		v.WriteVRAM16(benchLineWindow0+l*4, uint16(2*(20+l/4)))
		v.WriteVRAM16(benchLineWindow0+l*4+2, uint16(2*(300-l/4)))
		sx, ex := uint16(2*(60+l/2)), uint16(2*(250+l/8))
		if l%16 == 15 {
			sx, ex = 600, 200
		}
		v.WriteVRAM16(benchLineWindow1+l*4, sx)
		v.WriteVRAM16(benchLineWindow1+l*4+2, ex)
	}
}

// benchCompositeFeatures enables the remaining composite paths on the
// windowed six-layer scene: color calculation on NBG0, RBG0, and
// sprites (extended, with the sprite condition on priority), line
// color screen per line, color offset A and B, shadow on every layer
// and the back screen, a per-line back screen table, and gradation on
// RBG0.
func benchCompositeFeatures(v *VDP2) {
	benchWindowsW0W1Sprite(v)
	benchReg(v, vdp2CCCTL, 0x9451)
	benchReg(v, vdp2CCRNA, 12)
	benchReg(v, vdp2CCRR, 20)
	benchReg(v, vdp2CCRSA, 0x0A0E)
	benchReg(v, vdp2CCRSB, 0x1206)
	benchReg(v, vdp2SPCTL, 0x2300)
	benchReg(v, vdp2LNCLEN, 0x0011)
	benchReg(v, vdp2LCTAU, 0x8000)
	benchReg(v, vdp2LCTAL, 0xF400)
	benchReg(v, vdp2CLOFEN, 0x003F)
	benchReg(v, vdp2CLOFSL, 0x0012)
	benchReg(v, vdp2COAR, 8)
	benchReg(v, vdp2COAG, 0x1F8)
	benchReg(v, vdp2COAB, 4)
	benchReg(v, vdp2COBR, 0x1F0)
	benchReg(v, vdp2COBG, 6)
	benchReg(v, vdp2COBB, 0x1FC)
	benchReg(v, vdp2SDCTL, 0x013F)
	benchReg(v, vdp2BKTAU, 0x8000)
	benchReg(v, vdp2BKTAL, 0xF000)
	for l := uint32(0); l < 512; l++ {
		v.WriteVRAM16(benchLineColor+l*2, uint16(1+l%200))
		v.WriteVRAM16(benchBackTable+l*2, uint16(l*0x0213)&0x7FFF)
	}
}

type benchVDP2Scene struct {
	name  string
	build func() (*VDP2, vdp1FBView)
	spans int // RenderTo calls per line
}

// benchSixLayersTVMD is the windowed six-layer scene under another
// TVMD value.
func benchSixLayersTVMD(tvmd uint16, pal bool) func() (*VDP2, vdp1FBView) {
	return func() (*VDP2, vdp1FBView) {
		v, fb := benchSixLayers()
		benchWindowsW0W1Sprite(v)
		v.SetPAL(pal)
		benchReg(v, vdp2TVMD, tvmd)
		return v, fb
	}
}

func benchVDP2Scenes() []benchVDP2Scene {
	single := func(layer func(*VDP2, uint16)) func() (*VDP2, vdp1FBView) {
		return func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			layer(v, 1)
			return v, vdp1FBView{}
		}
	}
	sixWith := func(extra func(*VDP2)) func() (*VDP2, vdp1FBView) {
		return func() (*VDP2, vdp1FBView) {
			v, fb := benchSixLayers()
			extra(v)
			return v, fb
		}
	}
	return []benchVDP2Scene{
		{name: "NBG0 cell 4bpp", build: single(benchNBG0Cell4), spans: 1},
		{name: "NBG0 cell 8bpp", build: single(benchNBG0Cell8), spans: 1},
		{name: "NBG0 cell 32K", build: single(benchNBG0Cell32K), spans: 1},
		{name: "NBG2 cell 8bpp 1-word", build: single(benchNBG2Cell8OneWord), spans: 1},
		{name: "NBG0 bitmap 8bpp", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRegOr(v, vdp2BGON, 0x0001)
			benchReg(v, vdp2CHCTLA, 0x0012)
			benchReg(v, vdp2BMPNA, 0x0000)
			benchReg(v, vdp2MPOFN, 0x0003)
			benchReg(v, vdp2PRINA, 0x0001)
			benchBitmap8(v, 3*0x20000)
			return v, vdp1FBView{}
		}},
		{name: "NBG0 bitmap 32K", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRegOr(v, vdp2BGON, 0x0001)
			benchReg(v, vdp2CHCTLA, 0x0032)
			benchReg(v, vdp2BMPNA, 0x0000)
			benchReg(v, vdp2MPOFN, 0x0002)
			benchReg(v, vdp2PRINA, 0x0001)
			benchBitmap32K(v, 2*0x20000)
			return v, vdp1FBView{}
		}},
		{name: "NBG0 line scroll vertical cell scroll", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchNBG0Cell8(v, 1)
			benchReg(v, vdp2SCRCTL, 0x000F)
			benchReg(v, vdp2LSTA0U, 0x0000)
			benchReg(v, vdp2LSTA0L, 0xE000)
			benchReg(v, vdp2VCSTAU, 0x0000)
			benchReg(v, vdp2VCSTAL, 0xEC00)
			for l := uint32(0); l < 512; l++ {
				e := benchLineScroll + l*12
				v.WriteVRAM16(e, uint16(l%16))
				v.WriteVRAM16(e+2, uint16(l*37&0xFF)<<8)
				v.WriteVRAM16(e+4, uint16(l/4))
				v.WriteVRAM16(e+6, 0x0000)
				v.WriteVRAM16(e+8, 0x0001)
				v.WriteVRAM16(e+10, uint16(l%3)<<14)
			}
			for c := uint32(0); c < 128; c++ {
				v.WriteVRAM16(benchVCSTable+c*4, uint16(c*3%64))
				v.WriteVRAM16(benchVCSTable+c*4+2, 0x0000)
			}
			return v, vdp1FBView{}
		}},
		{name: "NBG0 mosaic", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchNBG0Cell8(v, 1)
			benchReg(v, vdp2MZCTL, 0x3301)
			return v, vdp1FBView{}
		}},
		{name: "four NBG layers", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchNBG0Cell4(v, 7)
			benchNBG1Cell8(v, 6)
			benchNBG2Cell8OneWord(v, 4)
			benchNBG3Cell4(v, 4)
			return v, vdp1FBView{}
		}},
		{name: "RBG0 cell", build: single(benchRBG0Cell), spans: 1},
		{name: "RBG0 cell coefficients", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRBG0Cell(v, 1)
			benchRBG0Coefficients(v)
			return v, vdp1FBView{}
		}},
		{name: "RBG0 bitmap 8bpp", build: single(benchRBG0Bitmap8), spans: 1},
		{name: "RBG1 over RBG0", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRBG0Cell(v, 1)
			benchRBG1Cell(v, 2)
			return v, vdp1FBView{}
		}},
		{name: "sprite 16-bit", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchSpriteRegs(v)
			return v, benchSpriteFB16()
		}},
		{name: "sprite 8-bit", spans: 1, build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchSpriteRegs(v)
			benchReg(v, vdp2SPCTL, 0x0008)
			return v, benchSpriteFB8()
		}},
		{name: "six layers", build: benchSixLayers, spans: 1},
		{name: "six layers W0", build: sixWith(benchWindowW0), spans: 1},
		{name: "six layers W0 W1 sprite window", build: sixWith(benchWindowsW0W1Sprite), spans: 1},
		{name: "six layers line windows", build: sixWith(benchLineWindows), spans: 1},
		{name: "six layers composite features", build: sixWith(benchCompositeFeatures), spans: 1},
		{name: "six layers 4 spans", build: sixWith(benchWindowsW0W1Sprite), spans: 4},
		{name: "six layers hi-res 640", build: benchSixLayersTVMD(0x8002, false), spans: 1},
		{name: "six layers LSMD3", build: benchSixLayersTVMD(0x80C0, false), spans: 1},
		{name: "six layers 352", build: benchSixLayersTVMD(0x8001, false), spans: 1},
		{name: "six layers PAL 256", build: benchSixLayersTVMD(0x8020, true), spans: 1},
	}
}

// benchRenderVDP2Frame renders one frame the way the frame loop does:
// BeginFrame, then per line BeginLine, RenderTo in spans pieces, and
// EndLine, then EndFrame.
func benchRenderVDP2Frame(v *VDP2, fb vdp1FBView, spans int) {
	v.BeginFrame()
	width := int(v.ActiveWidth())
	lines := int(v.ActiveLines())
	for y := 0; y < lines; y++ {
		v.BeginLine(y, fb)
		for s := 1; s <= spans; s++ {
			v.RenderTo(width * s / spans)
		}
		v.EndLine()
	}
	v.EndFrame()
}

func BenchmarkVDP2(b *testing.B) {
	for _, s := range benchVDP2Scenes() {
		b.Run(s.name, func(b *testing.B) {
			v, fb := s.build()
			// The first frame after a mode change blanks the output and
			// the vertical counters load from the frame-end capture, so
			// one warm-up frame puts every iteration on the same path.
			benchRenderVDP2Frame(v, fb, s.spans)
			pixels := uint64(v.ActiveWidth()) * uint64(v.ActiveLines())
			b.ReportAllocs()
			b.ResetTimer()
			start := time.Now()
			for i := 0; i < b.N; i++ {
				benchRenderVDP2Frame(v, fb, s.spans)
			}
			elapsed := time.Since(start)
			b.ReportMetric(float64(elapsed.Nanoseconds())/float64(uint64(b.N)*pixels), "ns/pixel")
		})
	}
}

// benchCheckContent fails when a rendered frame is mostly blank or
// nearly uniform, which would make the scene's cost meaningless.
func benchCheckContent(t *testing.T, v *VDP2) {
	t.Helper()
	w := int(v.ActiveWidth())
	h := v.DisplayHeight()
	fb := v.Framebuffer()
	colors := make(map[uint32]struct{})
	lit := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * 4
			c := uint32(fb[off])<<16 | uint32(fb[off+1])<<8 | uint32(fb[off+2])
			if c != 0 {
				lit++
			}
			colors[c] = struct{}{}
		}
	}
	if lit*2 < w*h {
		t.Errorf("only %d of %d pixels are non-black", lit, w*h)
	}
	if len(colors) < 16 {
		t.Errorf("only %d distinct colors", len(colors))
	}
}

// TestBenchVDP2Scenes pins the output of every benchmark scene after
// two frames (the second frame is the steady state the benchmark
// measures) and checks that each scene renders substantial content.
func TestBenchVDP2Scenes(t *testing.T) {
	for _, s := range benchVDP2Scenes() {
		t.Run(s.name, func(t *testing.T) {
			v, fb := s.build()
			benchRenderVDP2Frame(v, fb, s.spans)
			benchRenderVDP2Frame(v, fb, s.spans)
			benchCheckContent(t, v)
			checkBands(t, s.name, vdp2BandHashes(v), benchVDP2Pins[s.name], describeVDP2Band(v))
		})
	}
}

// benchVDP2Pins is the recorded band hash table per scene.
var benchVDP2Pins = map[string][]uint32{
	"NBG0 cell 4bpp": {
		0x878F329B, 0x265E772B, 0x2B592DCD, 0x242AADD5, 0xF8AFE397, 0x5911C4FB, 0x0F908AF2,
		0x23C97875, 0xC0B83A60, 0x5A48C113, 0x880C3EB3, 0x3F1141A3, 0x32D122ED, 0x7A45CA38,
		0x0E988872, 0x02C0BC2B, 0xFB905B36, 0x4929C83B, 0x275E4EBF, 0xDC2917AD, 0x294AB083,
		0x6DFCB58C, 0x4758CC79, 0x54FB9479, 0x878F329B, 0x265E772B, 0x2B592DCD, 0x242AADD5,
	},
	"NBG0 cell 8bpp": {
		0x37E5D2B6, 0x21CD80F6, 0x2C8D257A, 0xC04D5F3A, 0xA51A4F9E, 0x939373EA, 0x30254022,
		0x4996CF1E, 0x37E5D2B6, 0x21CD80F6, 0x2C8D257A, 0xC04D5F3A, 0xA51A4F9E, 0x939373EA,
		0x30254022, 0x4996CF1E, 0x37E5D2B6, 0x21CD80F6, 0x2C8D257A, 0xC04D5F3A, 0xA51A4F9E,
		0x939373EA, 0x30254022, 0x4996CF1E, 0x37E5D2B6, 0x21CD80F6, 0x2C8D257A, 0xC04D5F3A,
	},
	"NBG0 cell 32K": {
		0x6D2554B0, 0x10655D2A, 0x6EBB3F04, 0x78A64836, 0x18B5A030, 0xB6C26336, 0x5CF2ADD8,
		0x3AFAA836, 0x6D2554B0, 0x10655D2A, 0x6EBB3F04, 0x78A64836, 0x18B5A030, 0xB6C26336,
		0x5CF2ADD8, 0x3AFAA836, 0x6D2554B0, 0x10655D2A, 0x6EBB3F04, 0x78A64836, 0x18B5A030,
		0xB6C26336, 0x5CF2ADD8, 0x3AFAA836, 0x6D2554B0, 0x10655D2A, 0x6EBB3F04, 0x78A64836,
	},
	"NBG2 cell 8bpp 1-word": {
		0xE2CFE401, 0x1B5FF3ED, 0x54F9E7B1, 0x6608F17D, 0x72B6CE0D, 0xBB8C50CD, 0xCE99A799,
		0xB6E61069, 0xF86C5E35, 0x1B5FF3ED, 0x54F9E7B1, 0x6608F17D, 0x72B6CE0D, 0xBB8C50CD,
		0xCE99A799, 0xB6E61069, 0xF86C5E35, 0x1B5FF3ED, 0x54F9E7B1, 0x6608F17D, 0x72B6CE0D,
		0xBB8C50CD, 0xCE99A799, 0xB6E61069, 0xF86C5E35, 0x1B5FF3ED, 0x54F9E7B1, 0x6608F17D,
	},
	"NBG0 bitmap 8bpp": {
		0x6302E13A, 0x80D49811, 0x8545C60F, 0xBFF6560F, 0x1F6441E7, 0xCF782528, 0x4CCEA847,
		0xEFE64718, 0xC82E5460, 0x7730A659, 0x6C54041A, 0xBBE4AF51, 0x5A41EE81, 0x504F7553,
		0x5EB1ADCE, 0xB7D6032C, 0x0B589932, 0xC0CC2D93, 0xDA378CB7, 0x33B8BBDB, 0x2464E2D4,
		0x98B3675D, 0x28148056, 0x2558802F, 0xBAC28DBD, 0x2991C556, 0xF11F2B14, 0xA90500CF,
	},
	"NBG0 bitmap 32K": {
		0x90BDC8ED, 0xD00ED6B4, 0xDCB140D3, 0xBEF4C101, 0xA534AB94, 0xE8E138E3, 0x666E467A,
		0x00C08CCC, 0xDBA1CC37, 0x07C82778, 0x69FEE724, 0xB20EF1C6, 0x733D1855, 0x641C5E3A,
		0x94E47C44, 0x54B14A08, 0x13B6025F, 0xE6C4C3CD, 0x35956262, 0xD115579E, 0x3EA1ADA0,
		0x64F26F90, 0x161109D7, 0x886A9E55, 0x90BDC8ED, 0xD00ED6B4, 0xDCB140D3, 0xBEF4C101,
	},
	"NBG0 line scroll vertical cell scroll": {
		0xC2733ED1, 0x2FDECE13, 0xF7871EA6, 0x41A8E47C, 0xA484A362, 0x683BBF42, 0x67F69401,
		0xB59C335D, 0x3AE93E20, 0xAD055AC1, 0xC75FA9B2, 0xDC5B8452, 0x2EBB1148, 0x58FB6489,
		0xAAB70D0E, 0x86CE4833, 0x6D1BDF8F, 0x18F9E751, 0xFD5E07C5, 0x07769D52, 0xD32C32B1,
		0x0CBE8B0C, 0xFFDD5B53, 0x70E5E282, 0xC85337CD, 0xA9679E11, 0x50A7F536, 0xA657E2E1,
	},
	"NBG0 mosaic": {
		0x8D6EF475, 0x8A7C1885, 0x64B23FB5, 0x2FCE1A85, 0xCC5F4FB5, 0x9D9A3505, 0xA1ED7575,
		0x586B1E05, 0x8D6EF475, 0x8A7C1885, 0x64B23FB5, 0x2FCE1A85, 0xCC5F4FB5, 0x9D9A3505,
		0xA1ED7575, 0x586B1E05, 0x8D6EF475, 0x8A7C1885, 0x64B23FB5, 0x2FCE1A85, 0xCC5F4FB5,
		0x9D9A3505, 0xA1ED7575, 0x586B1E05, 0x8D6EF475, 0x8A7C1885, 0x64B23FB5, 0x2FCE1A85,
	},
	"four NBG layers": {
		0x46ABC34B, 0x603A9004, 0x8E94818D, 0x70175DE2, 0xA96255B9, 0x2F3A7C34, 0x85A851C6,
		0x732BE5B4, 0x1856D044, 0x19EE1526, 0xD3BE6EFB, 0xD68AB1DA, 0xA0A731AA, 0xE1A36C05,
		0x1190E462, 0xA112AD96, 0x88C7F6F9, 0x04280FC5, 0xA2596D58, 0x355899DF, 0x219734AD,
		0x14949962, 0xF03796E6, 0x14233791, 0x66CA088B, 0x603A9004, 0x8E94818D, 0x70175DE2,
	},
	"RBG0 cell": {
		0x813B50E3, 0x81EA3B78, 0x9563BADA, 0x74A60B5E, 0xE666B17B, 0xEADF08AF, 0x0E245C15,
		0x92C5367C, 0x2505A644, 0xA8353695, 0xDA765DD1, 0x99583B17, 0x7DF02073, 0x862CFBA3,
		0x7B560129, 0x21154FA0, 0x972F34BA, 0xBD385E38, 0x76CB1D8C, 0x73583602, 0x1FA5FE7A,
		0x04AA0705, 0x4A69228C, 0xC175FCA3, 0xBDD3041B, 0x3DA80F0B, 0x1F1F508C, 0x38AD128B,
	},
	"RBG0 cell coefficients": {
		0x4173E820, 0xDBF72064, 0xE1AA24A0, 0x9531C515, 0x6C870AF6, 0x3A1FA46E, 0x44F24354,
		0xAAFD41C9, 0x9001844B, 0x3239A8DB, 0x9BDA8385, 0x55A17F3B, 0x6B0B7596, 0x24C42EE5,
		0xEF02EA25, 0x62F69252, 0xE72C83BE, 0x595D56FE, 0x90AF8960, 0x2DD012FC, 0xCF8D5D94,
		0xB9C9376C, 0x1363F1EC, 0xB44239AB, 0x94BAC6EC, 0x6D5CB66C, 0x08FA852B, 0x92F0807C,
	},
	"RBG0 bitmap 8bpp": {
		0xB087285D, 0x1472FC27, 0xFA78DE1C, 0xC7BC767C, 0x45F95521, 0xDE18312F, 0xAF458149,
		0xF9091FCF, 0x44728D18, 0x3078F0AB, 0x8BE129B5, 0x4BB6CE2E, 0xF18AB2A2, 0x9B16C763,
		0xF2DE587A, 0x5D72A1ED, 0xDC98CF1F, 0x5B4D863D, 0xA6167F63, 0xE6186F38, 0x4645B9E3,
		0x71A17820, 0x74B565FE, 0x2EDEE3AE, 0x24BCA90E, 0xA48BF158, 0x4F9102D7, 0xC4759F18,
	},
	"RBG1 over RBG0": {
		0xF74051AF, 0x78470BEC, 0x7D563A89, 0x85185FB4, 0x3D150CC7, 0xBF2FF11F, 0x0334E8D5,
		0x543CE898, 0xD7719241, 0x2D4107EC, 0x1F69928D, 0xD116CBF0, 0xB0446457, 0x735684F8,
		0x7F6F70E4, 0x70BB2BFA, 0x6D565865, 0x173BFC25, 0x476CFF26, 0xA4B54EEB, 0xA8425F1F,
		0xB10E4F55, 0x0FB6C283, 0x79B59EBF, 0x7B396A8F, 0x84102C3D, 0x1D57AE34, 0xF3ED68F1,
	},
	"sprite 16-bit": {
		0xA9537D7A, 0x63CCE513, 0x86491D3D, 0x9E6162AB, 0x8D6F6B79, 0x56DACE9A, 0x6AE3FED6,
		0x89C8EE7F, 0xCD8C9611, 0x40188A44, 0xE0D2AD51, 0x4863A116, 0x6C54DE1F, 0x452034B7,
		0x4EF717ED, 0xAA27E781, 0x5726A64B, 0x456C1FB8, 0x495F9B7C, 0x0F788EE1, 0x4C84A85D,
		0x2CDA384A, 0xF3D09C6E, 0xADEB4042, 0x94D41E2B, 0xBC84CD14, 0x6B2A8D4C, 0xF04FAAB5,
	},
	"sprite 8-bit": {
		0x205ACBE2, 0x3981BFC6, 0x7E909079, 0x4EF0D4CB, 0x456F9DFB, 0xB42B0718, 0x03870976,
		0xDB47ADF5, 0x029C8C9A, 0x72711C0C, 0x11D65BC5, 0xC5B52C27, 0x07D6C3B0, 0x5DB8D238,
		0xA1AC90AA, 0xBD5F5A65, 0xED150760, 0xF6DFC4A7, 0x9C14BF44, 0x9EE45F65, 0xB7313C26,
		0x2208E41E, 0xBDE9154C, 0x4C1222DE, 0x581FE506, 0xE72454AA, 0x568AA9C4, 0xF53A70AD,
	},
	"six layers": {
		0x0F2ADF46, 0x8EA1B0F4, 0xF62473C5, 0xA6A16AC4, 0xCC0B53C8, 0xFF8888BA, 0x6B43A754,
		0x7B7E5579, 0x80700E6D, 0x4DF2793B, 0x4D97F7AE, 0x84BBC617, 0xDD6F635D, 0xF40E8300,
		0xF3F8A902, 0x5EBD715E, 0xF82747B4, 0x20D5F3CF, 0x3EA630FB, 0x26D0AD36, 0xA9DFF88B,
		0xCC73FBF7, 0xC95A4A0C, 0x07FE4055, 0x12ED581E, 0x5265EAED, 0x35FF47C5, 0xE7BADD05,
	},
	"six layers W0": {
		0xD6DAEE20, 0x4DF7371E, 0x657BEA70, 0xBBD5675B, 0x1023A1C5, 0xE5D8E261, 0x3DB1547A,
		0x7C50767E, 0xF243F51A, 0x761601E6, 0x247C1124, 0x5C18FB8C, 0x8212B921, 0xC0418518,
		0x1AF6CFAA, 0x05579174, 0x16055CBE, 0xFE382B02, 0x49106363, 0x617CED91, 0x71FDA8DD,
		0x269EB4A8, 0x7496999C, 0x8CDA26A6, 0x01BED00E, 0x2F8DC875, 0xBD6B4FB1, 0xB698936F,
	},
	"six layers W0 W1 sprite window": {
		0x8DEB0618, 0x8401C735, 0x04781A23, 0x0FF10090, 0xFB8B5099, 0x3D189B5A, 0x280DC72D,
		0x2C64662B, 0x0EA13FBE, 0xAB494387, 0xB706617B, 0x22D0370D, 0x5288EFB4, 0x1C92AFB9,
		0xEC728641, 0x8759E05E, 0x323AE059, 0x35A66901, 0x4A726D83, 0xABFFF257, 0xEB48C76D,
		0x3168DE16, 0xB69A0DF1, 0x2DA78950, 0x6E35FB09, 0x3ACECE26, 0xB1D96E58, 0xF9DF2D9A,
	},
	"six layers line windows": {
		0x8DEB0618, 0x8401C735, 0x3F0D2FD2, 0xEF43058E, 0xC5E68486, 0xEA0D2AFC, 0x611E08FE,
		0xEFE6FF9A, 0x50BCA8BD, 0x450C4578, 0x010D201A, 0x3C9B0670, 0x168999B3, 0xA6C26879,
		0xDE51A741, 0xBE9C473D, 0x743B2F60, 0x17639FA2, 0xC9459F78, 0x43BCE40A, 0x1BE3223B,
		0x1E647244, 0x3C19330C, 0x41A205E9, 0x3DA8FFBE, 0xCFED7DF6, 0xB1D96E58, 0xF9DF2D9A,
	},
	"six layers composite features": {
		0x573BEDCA, 0x0815FA1A, 0xF13DDA3B, 0x2C9E79D3, 0x712C6834, 0x684AF063, 0x913FF2E9,
		0xE50DE42D, 0x2FAAD3D4, 0xF1DD050D, 0x18B72489, 0x7C670F3F, 0xE1447549, 0x4CED88FE,
		0x59A87261, 0x4085AF36, 0x5CB79A67, 0x18DD6B16, 0x0D48C214, 0x92D4E5E9, 0x2B8244D9,
		0xA2DA2905, 0x91C8B33A, 0x0212D5F8, 0x10123884, 0x2FDCB660, 0x00DF0B29, 0xE97D63E6,
	},
	"six layers 4 spans": {
		0x8DEB0618, 0x8401C735, 0x04781A23, 0x0FF10090, 0xFB8B5099, 0x3D189B5A, 0x280DC72D,
		0x2C64662B, 0x0EA13FBE, 0xAB494387, 0xB706617B, 0x22D0370D, 0x5288EFB4, 0x1C92AFB9,
		0xEC728641, 0x8759E05E, 0x323AE059, 0x35A66901, 0x4A726D83, 0xABFFF257, 0xEB48C76D,
		0x3168DE16, 0xB69A0DF1, 0x2DA78950, 0x6E35FB09, 0x3ACECE26, 0xB1D96E58, 0xF9DF2D9A,
	},
	"six layers hi-res 640": {
		0xCDBF4F84, 0x219A3118, 0xA6FD5E45, 0x00D28276, 0xF0C4457F, 0x8EA9D7F4, 0x15B818FB,
		0x4F516279, 0x7A917C2E, 0xFD22293E, 0x356E1773, 0x3A57F05C, 0x7013A516, 0x0ED950B7,
		0x52B9BEE6, 0x915C7EA2, 0x7B93439E, 0x9E79EAD2, 0x7640C3F4, 0x1B314CE8, 0x97179612,
		0x3F12F236, 0x8D6CDB2D, 0x2D753FC0, 0x21E42FDB, 0x969EB2B5, 0x887574A0, 0x8DE8C599,
	},
	"six layers LSMD3": {
		0x595D0A8A, 0xF601E38F, 0xCA5BB673, 0x0D209B33, 0x8BDE6D1F, 0x7F6E34AA, 0xC0B12A40,
		0x4B58F73C, 0xAAC237DA, 0x530AC897, 0x65E9E617, 0xB29015B4, 0xAD11E78D, 0x5C2F1D8A,
		0x4105A067, 0x4C613D34, 0x358E82DF, 0x6B3AC6D5, 0x4BFCFC8E, 0xC19FAA89, 0x7BB3AE00,
		0x6968738A, 0x5CF5F3A4, 0x8F558955, 0x27AD9CCB, 0xC174D0F6, 0x9DF63816, 0xC966E0E7,
		0xAD4DB711, 0xF055BEAF, 0xDE982AE1, 0xB7A0C342, 0xBF2A0473, 0x2EF1F936, 0x93FC4E56,
		0xA649A3CD, 0x9FE19836, 0xA88E8182, 0x85B588C4, 0x8236E24F, 0x37ADE329, 0x905C322E,
		0x05368F95, 0x341BF28E, 0x13EA5CFD, 0xCC21230B, 0x06A0A72F, 0x7B3504D3, 0xFDD14429,
		0x90A4404D, 0xCAA9D2B6, 0x84F82830, 0x38C9FA57, 0x32D5A584, 0x911E9504, 0xBEE014DB,
	},
	"six layers 352": {
		0xA0CE30C6, 0xF6F665F2, 0x3642C2AE, 0x06C37DFA, 0x5F7A3A19, 0x69A539C9, 0xDA0EB735,
		0x6A373638, 0x90322EFD, 0x677AFF2D, 0xC758A1DC, 0x3F492FF4, 0x34E8C1EA, 0xD9DD2750,
		0x41E11D1A, 0x2A926F20, 0x135703EC, 0x37893AEC, 0x88741692, 0x9A10EED4, 0x10B3565A,
		0x3BF65204, 0x4206C948, 0x784D693F, 0x005C97F4, 0x233A5789, 0x8F30EC32, 0x88E2C8F6,
	},
	"six layers PAL 256": {
		0x8DEB0618, 0x8401C735, 0x04781A23, 0x0FF10090, 0xFB8B5099, 0x3D189B5A, 0x280DC72D,
		0x2C64662B, 0x0EA13FBE, 0xAB494387, 0xB706617B, 0x22D0370D, 0x5288EFB4, 0x1C92AFB9,
		0xEC728641, 0x8759E05E, 0x323AE059, 0x35A66901, 0x4A726D83, 0xABFFF257, 0xEB48C76D,
		0x3168DE16, 0xB69A0DF1, 0x2DA78950, 0x6E35FB09, 0x3ACECE26, 0xB1D96E58, 0xF9DF2D9A,
		0xEBC8B757, 0xC852AE07, 0x15843747, 0xFBE5B790,
	},
}
