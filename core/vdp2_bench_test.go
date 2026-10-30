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
// frame loop's per-line sequence (renderFrame: BeginFrame, then per
// line beginRow and the render point, then the display and frame
// ends). Nothing here names a renderer-internal function, so the
// numbers stay comparable across refactors of the renderer.
//
// Each benchmark reports nanoseconds per output pixel, which stays
// comparable across display widths and interlace modes; ns/op is the
// per-frame cost. Every scene renders each row in one RenderTo call,
// as the frame loop does at the line's end.
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
		data:    v1.DisplayFB(),
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
// scene: NBG1 takes W0 AND W1, NBG2 W0 OR W1, NBG3 the sprite window,
// enabled by SPWINEN so the frame buffer MSB is the window bit.
func benchWindowsW0W1Sprite(v *VDP2) {
	benchWindowW0(v)
	benchReg(v, vdp2SPCTL, 0x0010)
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
		{name: "NBG0 cell 4bpp", build: single(benchNBG0Cell4)},
		{name: "NBG0 cell 8bpp", build: single(benchNBG0Cell8)},
		{name: "NBG0 cell 32K", build: single(benchNBG0Cell32K)},
		{name: "NBG2 cell 8bpp 1-word", build: single(benchNBG2Cell8OneWord)},
		{name: "NBG0 bitmap 8bpp", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRegOr(v, vdp2BGON, 0x0001)
			benchReg(v, vdp2CHCTLA, 0x0012)
			benchReg(v, vdp2BMPNA, 0x0000)
			benchReg(v, vdp2MPOFN, 0x0003)
			benchReg(v, vdp2PRINA, 0x0001)
			benchBitmap8(v, 3*0x20000)
			return v, vdp1FBView{}
		}},
		{name: "NBG0 bitmap 32K", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRegOr(v, vdp2BGON, 0x0001)
			benchReg(v, vdp2CHCTLA, 0x0032)
			benchReg(v, vdp2BMPNA, 0x0000)
			benchReg(v, vdp2MPOFN, 0x0002)
			benchReg(v, vdp2PRINA, 0x0001)
			benchBitmap32K(v, 2*0x20000)
			return v, vdp1FBView{}
		}},
		{name: "NBG0 line scroll vertical cell scroll", build: func() (*VDP2, vdp1FBView) {
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
		{name: "NBG0 mosaic", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchNBG0Cell8(v, 1)
			benchReg(v, vdp2MZCTL, 0x3301)
			return v, vdp1FBView{}
		}},
		{name: "four NBG layers", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchNBG0Cell4(v, 7)
			benchNBG1Cell8(v, 6)
			benchNBG2Cell8OneWord(v, 4)
			benchNBG3Cell4(v, 4)
			return v, vdp1FBView{}
		}},
		{name: "RBG0 cell", build: single(benchRBG0Cell)},
		{name: "RBG0 cell coefficients", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRBG0Cell(v, 1)
			benchRBG0Coefficients(v)
			return v, vdp1FBView{}
		}},
		{name: "RBG0 bitmap 8bpp", build: single(benchRBG0Bitmap8)},
		{name: "RBG1 over RBG0", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRBG0Cell(v, 1)
			benchRBG1Cell(v, 2)
			return v, vdp1FBView{}
		}},
		{name: "sprite 16-bit", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchSpriteRegs(v)
			return v, benchSpriteFB16()
		}},
		{name: "sprite 8-bit", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchSpriteRegs(v)
			benchReg(v, vdp2SPCTL, 0x0008)
			return v, benchSpriteFB8()
		}},
		{name: "six layers", build: benchSixLayers},
		{name: "six layers W0", build: sixWith(benchWindowW0)},
		{name: "six layers W0 W1 sprite window", build: sixWith(benchWindowsW0W1Sprite)},
		{name: "six layers line windows", build: sixWith(benchLineWindows)},
		{name: "six layers composite features", build: sixWith(benchCompositeFeatures)},
		{name: "six layers hi-res 640", build: benchSixLayersTVMD(0x8002, false)},
		{name: "six layers LSMD3", build: benchSixLayersTVMD(0x80C0, false)},
		{name: "six layers 352", build: benchSixLayersTVMD(0x8001, false)},
		{name: "six layers PAL 256", build: benchSixLayersTVMD(0x8020, true)},
	}
}

// BenchmarkVDP2 runs every scene under both renderers. The software
// sub-benchmark is the frame as the walker renders it. The gpu
// sub-benchmark is the frame as the walker drives it with the GPU
// renderer: the same per-line calls through the renderer's points in
// the frame loop's positions, beginFrame after BeginFrame, renderLine
// after each BeginLine, endDisplay after the last line, endFrame
// after the frame, which waits for the frame the frontend reads. It
// reports the time of the renderLine and endDisplay calls on their
// own, the page takes, batch uploads, and submissions that run on the
// walker. Without a device it skips.
func BenchmarkVDP2(b *testing.B) {
	for _, s := range benchVDP2Scenes() {
		b.Run(s.name, func(b *testing.B) {
			b.Run("software", func(b *testing.B) {
				v, fb := s.build()
				r := newSoftwareRenderer(v)
				// The first frame after a mode change blanks the output and
				// the vertical counters load from the frame-end capture, so
				// one warm-up frame puts every iteration on the same path.
				renderFrame(v, r, fb, nil)
				pixels := uint64(v.ActiveWidth()) * uint64(v.ActiveLines())
				b.ReportAllocs()
				b.ResetTimer()
				start := time.Now()
				for i := 0; i < b.N; i++ {
					renderFrame(v, r, fb, nil)
				}
				elapsed := time.Since(start)
				b.ReportMetric(float64(elapsed.Nanoseconds())/float64(uint64(b.N)*pixels), "ns/pixel")
			})
			b.Run("gpu", func(b *testing.B) {
				v, fb := s.build()
				g, err := openGPURenderer(v, NewVDP1(NewSCU()), defaultBackend())
				if err != nil {
					b.Skip(err)
				}
				defer g.close()
				renderFrame(v, g, fb, nil)
				submitTime := time.Duration(0)
				frame := func() {
					v.BeginFrame()
					g.beginFrame()
					lines := int(v.ActiveLines())
					for y := 0; y < lines; y++ {
						beginRow(v, g, y, fb)
						start := time.Now()
						g.renderLine(y)
						submitTime += time.Since(start)
						v.EndLine()
					}
					v.EndFrame()
					start := time.Now()
					g.endDisplay()
					submitTime += time.Since(start)
					g.endFrame()
				}
				frame()
				if err := g.err(); err != nil {
					b.Fatal(err)
				}
				pixels := uint64(v.ActiveWidth()) * uint64(v.ActiveLines())
				b.ReportAllocs()
				b.ResetTimer()
				submitTime = 0
				start := time.Now()
				for i := 0; i < b.N; i++ {
					frame()
				}
				elapsed := time.Since(start)
				if err := g.err(); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(elapsed.Nanoseconds())/float64(uint64(b.N)*pixels), "ns/pixel")
				b.ReportMetric(float64(submitTime.Microseconds())/float64(b.N), "submit-us/op")
			})
		})
	}
}

// benchCheckContent fails when a rendered frame is mostly blank or
// nearly uniform, which would make the scene's cost meaningless.
func benchCheckContent(t *testing.T, v *VDP2, r *softwareRenderer) {
	t.Helper()
	w := int(v.ActiveWidth())
	h := v.DisplayHeight()
	fb := r.framebuffer()
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
			r := newSoftwareRenderer(v)
			renderFrame(v, r, fb, nil)
			renderFrame(v, r, fb, nil)
			benchCheckContent(t, v, r)
			checkBands(t, s.name, vdp2BandHashes(v, r), benchVDP2Pins[s.name], describeVDP2Band(v, r))
		})
	}
}

// benchVDP2Pins is the recorded band hash table per scene.
var benchVDP2Pins = map[string][]uint32{
	"NBG0 cell 4bpp": {
		0xBB187A3D, 0x8911DD0D, 0xDA827F4D, 0x5DC9D55D, 0xCCDC267D, 0x8C57E40D, 0x4E9A4E25,
		0x0E243B15, 0xC9F66215, 0x3340566D, 0x410E2705, 0xF561A415, 0xDE20CBAD, 0x3BF10FE5,
		0x6CE6B1CD, 0xC50F7905, 0xF5BFEB05, 0x8A90CD4D, 0x437E2D05, 0x2BF2D565, 0x542C339D,
		0x9BCE31C5, 0xB1162925, 0x24217D7D, 0xBB187A3D, 0x8911DD0D, 0xDA827F4D, 0x5DC9D55D,
	},
	"NBG0 cell 8bpp": {
		0xEB79A1CD, 0x8DD1F25D, 0xB4CB30ED, 0xCF916CBD, 0x7EEF008D, 0x9690FABD, 0x2B81236D,
		0xDB7E377D, 0xEB79A1CD, 0x8DD1F25D, 0xB4CB30ED, 0xCF916CBD, 0x7EEF008D, 0x9690FABD,
		0x2B81236D, 0xDB7E377D, 0xEB79A1CD, 0x8DD1F25D, 0xB4CB30ED, 0xCF916CBD, 0x7EEF008D,
		0x9690FABD, 0x2B81236D, 0xDB7E377D, 0xEB79A1CD, 0x8DD1F25D, 0xB4CB30ED, 0xCF916CBD,
	},
	"NBG0 cell 32K": {
		0x6DD50C15, 0x78EE5EF5, 0xEBFC7E35, 0xFF224CD5, 0x604579F5, 0xCB9B5775, 0x34B38C15,
		0xA04CABB5, 0x6DD50C15, 0x78EE5EF5, 0xEBFC7E35, 0xFF224CD5, 0x604579F5, 0xCB9B5775,
		0x34B38C15, 0xA04CABB5, 0x6DD50C15, 0x78EE5EF5, 0xEBFC7E35, 0xFF224CD5, 0x604579F5,
		0xCB9B5775, 0x34B38C15, 0xA04CABB5, 0x6DD50C15, 0x78EE5EF5, 0xEBFC7E35, 0xFF224CD5,
	},
	"NBG2 cell 8bpp 1-word": {
		0xDB3336C5, 0xF88BC7FD, 0x3DCD111D, 0x30516FBD, 0x65AC9E5D, 0xF6EA31DD, 0x98E5D97D,
		0x33F5DFDD, 0x7119081D, 0xF88BC7FD, 0x3DCD111D, 0x30516FBD, 0x65AC9E5D, 0xF6EA31DD,
		0x98E5D97D, 0x33F5DFDD, 0x7119081D, 0xF88BC7FD, 0x3DCD111D, 0x30516FBD, 0x65AC9E5D,
		0xF6EA31DD, 0x98E5D97D, 0x33F5DFDD, 0x7119081D, 0xF88BC7FD, 0x3DCD111D, 0x30516FBD,
	},
	"NBG0 bitmap 8bpp": {
		0x24806AED, 0x61F460F5, 0x0B13698D, 0x7044EB7D, 0x09C0195D, 0xC6200C45, 0x90293705,
		0x98C897C5, 0x999394F5, 0x4C9FD4CD, 0x9FD8DA05, 0x9EF9B5C5, 0xFBBEE905, 0x5E5CA60D,
		0x7A6AF3F5, 0xDE85A4AD, 0xF2D52675, 0x3E8FF17D, 0x3018E72D, 0x154BD435, 0x9FF47C8D,
		0x31B34E55, 0x93D7CF35, 0xDC66177D, 0x0E856235, 0xD1D5787D, 0xE0A77C35, 0x8937E765,
	},
	"NBG0 bitmap 32K": {
		0x66F63B45, 0xFB2E2ED5, 0x5CC8D465, 0xFCBC8675, 0x86E28345, 0xCBB2CE95, 0x767DC825,
		0x849BAEF5, 0x81C40EC5, 0xCEE19535, 0x2836BFE5, 0x50EFC1F5, 0x3F461F85, 0x17DE1BB5,
		0xFB4C5A25, 0x8E75A8F5, 0xF5E28545, 0x0EF1C3C5, 0x9D807905, 0x18004505, 0x827DF305,
		0xD4A70F85, 0xB82921C5, 0x206B1445, 0x66F63B45, 0xFB2E2ED5, 0x5CC8D465, 0xFCBC8675,
	},
	"NBG0 line scroll vertical cell scroll": {
		0x1BA0C2BD, 0x99A8033D, 0x8425698D, 0xB2C241FD, 0x0001167D, 0x5375D7B5, 0xF949C505,
		0xB99B0A5D, 0xF4A108DD, 0x5639646D, 0xB3619E8D, 0x5B6DB4FD, 0xE588E6ED, 0xDA3F5305,
		0x3DA5CBC5, 0x1BDB49B5, 0xD0917C75, 0x1D47C2C5, 0x602E68C5, 0x972CB79D, 0xA932CB95,
		0x0845327D, 0x29F5ED65, 0x041A2455, 0x3C1D8CF5, 0x73725F5D, 0xEE5D63C5, 0xF21F9655,
	},
	"NBG0 mosaic": {
		0x437C17C5, 0x46978745, 0x24709BC5, 0xE8768545, 0xDC6717C5, 0x10580D45, 0xE60BEBC5,
		0x08B77D45, 0x437C17C5, 0x46978745, 0x24709BC5, 0xE8768545, 0xDC6717C5, 0x10580D45,
		0xE60BEBC5, 0x08B77D45, 0x437C17C5, 0x46978745, 0x24709BC5, 0xE8768545, 0xDC6717C5,
		0x10580D45, 0xE60BEBC5, 0x08B77D45, 0x437C17C5, 0x46978745, 0x24709BC5, 0xE8768545,
	},
	"four NBG layers": {
		0x0F75651D, 0xA4385495, 0x66EF6DE5, 0xAD2575E5, 0x79F6FB25, 0x1A49B6B5, 0xDAFC468D,
		0xFBA808AD, 0x4D3D2F6D, 0xE136E805, 0xC57B4C9D, 0x883F451D, 0x5F06BE8D, 0x289F3E8D,
		0x1B802515, 0xE54E468D, 0x72CC9055, 0xCAB361BD, 0x82565EF5, 0xAA52BF75, 0x8467A7E5,
		0xBCD98015, 0x8CF44FB5, 0x8383DD2D, 0xFAB21035, 0xA4385495, 0x66EF6DE5, 0xAD2575E5,
	},
	"RBG0 cell": {
		0x96203BD5, 0x06F5584D, 0x8C61AF6D, 0x9B7DDC35, 0xF470F2BD, 0xD7291645, 0x07E88235,
		0x295DF24D, 0x3212131D, 0x87C5F785, 0x3CAA7B15, 0x6AF85D2D, 0x31005B3D, 0xD7440EE5,
		0x31F76395, 0x987D816D, 0x16EA5FD5, 0x6903460D, 0xD9F67C35, 0x4FA58AA5, 0x4B2A922D,
		0xFFAAEF05, 0x17B1C21D, 0x2876024D, 0xF526ABA5, 0xA4058B2D, 0xCDFC82B5, 0x87A0D675,
	},
	"RBG0 cell coefficients": {
		0x324FB3A5, 0x3E0C4165, 0x0F9DB055, 0xBDFD4055, 0x5F3A771D, 0x606FF8A5, 0x9B8C340D,
		0x6C935335, 0x2402736D, 0xA08E07F5, 0x084B433D, 0x737F40BD, 0x81F62885, 0xECBFF4BD,
		0xE674507D, 0xC96386E5, 0x4791B02D, 0x214BC5B5, 0xBF6FC74D, 0x471E4615, 0xB30A95ED,
		0x8043650D, 0x97DAD0FD, 0x2720DE65, 0x8C1743F5, 0x18BD7CDD, 0x91C8BDD5, 0x09A6C2C5,
	},
	"RBG0 bitmap 8bpp": {
		0xAC12777D, 0x68F31065, 0x97E13F8D, 0x04BC167D, 0x5E21F965, 0x94FDA4BD, 0x1CC260D5,
		0x414BEB15, 0x383307CD, 0x5F0E98FD, 0xF2AE636D, 0xB6DFBC5D, 0x15541ADD, 0x2DDB8395,
		0x525530C5, 0x167E63C5, 0xF509554D, 0x4799D845, 0x761BCDA5, 0x82D3342D, 0xA478C265,
		0x9475E15D, 0xFB81078D, 0x24C09125, 0xD1BBA49D, 0x7FCD8315, 0xDF1FCA35, 0x2037C79D,
	},
	"RBG1 over RBG0": {
		0x68C6EDAD, 0x9FC6B4FD, 0xC8C0E0A5, 0xD1DFF38D, 0xFEFDB05D, 0xC16095CD, 0x9BB352C5,
		0x1098EDFD, 0xEF1FCDC5, 0x4FC5F37D, 0x159021DD, 0x72C05925, 0x91726B6D, 0x9FC8E3CD,
		0x1CA3629D, 0x096B550D, 0x8D0C87F5, 0xC1BE868D, 0xF09F90D5, 0x721FD99D, 0xA473AD5D,
		0xA1022525, 0x3B364FD5, 0x16A5F295, 0x536B17DD, 0xD2252B05, 0xF6000AB5, 0xDB75D265,
	},
	"sprite 16-bit": {
		0xE449767D, 0x34BA9145, 0xB14DAAFD, 0x2F1A4E45, 0xFC2DAA95, 0xE2D50BED, 0xFEA1FCFD,
		0x522E5DF5, 0x84FB0565, 0xAD4BA9C5, 0xA27FFFBD, 0xED30C295, 0xF4D878A5, 0xD70308AD,
		0x755B9EDD, 0x806314A5, 0x84C8A915, 0xC767D125, 0x3E095D25, 0x4BF0811D, 0xFB224C4D,
		0x006364D5, 0x28085FC5, 0x37177F3D, 0x59284B0D, 0x28FFF295, 0x1283A3F5, 0xB79ABB25,
	},
	"sprite 8-bit": {
		0xE085C7C5, 0x8611C49D, 0x2319EF85, 0x550189DD, 0x28EDA35D, 0x968F2845, 0xF1CB29CD,
		0x10B1BF15, 0xC964BC4D, 0x8F76C755, 0x78536C4D, 0x4E1070ED, 0xB26A0DB5, 0xD6E6A46D,
		0xA213E445, 0xDE004B9D, 0xDDF66E35, 0xDA7404A5, 0x629F7775, 0xD39CAFD5, 0x9B13C015,
		0xD7BCD84D, 0x6E9D91FD, 0x1ED3D815, 0x9145AB4D, 0xA337300D, 0xF6940B6D, 0x7A48C9A5,
	},
	"six layers": {
		0xF838EAF5, 0x85D2BB2D, 0x4EF8876D, 0xF7A98AFD, 0xCB53DB85, 0xFFDB7A75, 0xE29551F5,
		0x23A99305, 0x7332733D, 0xD24E3F95, 0x3248A035, 0x33B14D25, 0x85FC8145, 0x5FCD0395,
		0x3B6B0CA5, 0xFF483235, 0x8E843C85, 0x3F42149D, 0x2D34B90D, 0x32EE040D, 0x75EB7A8D,
		0x95E0E385, 0x2178EB5D, 0xA70A065D, 0x15F6F32D, 0xD1D63D4D, 0x3FBD27ED, 0x2F995E5D,
	},
	"six layers W0": {
		0x90E0F90D, 0x366C4355, 0xD9A5AC95, 0x19E1593D, 0xC1A57405, 0x8B0648B5, 0x0EEBE985,
		0xB8AB01AD, 0xA85907BD, 0x83FF4C35, 0x551585AD, 0xB5C783FD, 0x0ED10DB5, 0x4DF821DD,
		0x26808E65, 0x7F655D6D, 0xFC797CED, 0xEDD2DB0D, 0xA3410E65, 0x60539D4D, 0x9DDA7C75,
		0xF4AB6E95, 0xAB31A825, 0x1D4FA18D, 0x635640E5, 0xB2A4129D, 0xA4F5C3A5, 0xBEA276F5,
	},
	"six layers W0 W1 sprite window": {
		0x70459CC5, 0xE466CCE5, 0x6B2ED485, 0x5E8B305D, 0xDAA93C55, 0xA0FF1515, 0x6DDA28FD,
		0x4026CDFD, 0xCC66174D, 0x131D247D, 0x48008E5D, 0x28B4EE3D, 0x1A43289D, 0x537930DD,
		0x41EE1EDD, 0x3A405045, 0x2DF817D5, 0x2B3040F5, 0x4CBABA15, 0xD682523D, 0x77397C45,
		0x2BB696E5, 0x0777387D, 0x06EA374D, 0x2699F425, 0x3DA3CCFD, 0x33C9C2CD, 0xBEDC2EA5,
	},
	"six layers line windows": {
		0x70459CC5, 0xE466CCE5, 0x9E035385, 0xDF97B51D, 0x948CDCBD, 0x9C3012BD, 0xB64F9F6D,
		0xCD0D1C75, 0x9419CC05, 0x43B7BC35, 0x029A7345, 0x48295875, 0xE5BAE7AD, 0x47CABE95,
		0x0A6C024D, 0x123CAC7D, 0x6E02696D, 0xDF29D0DD, 0xD40ECA8D, 0xA9528295, 0xD64AB1BD,
		0x609D6F1D, 0x0D5E52A5, 0x954BB2BD, 0xE5122E85, 0xC3A64435, 0x33C9C2CD, 0xBEDC2EA5,
	},
	"six layers composite features": {
		0x0AC4B645, 0x05FEFB37, 0x9D0A32D9, 0xBB248BC2, 0xE7F336AA, 0xFBD1338F, 0xB3D5E7DA,
		0xF38604B8, 0xFE01A8C2, 0x476B26E9, 0x87E5F3E0, 0x6447197F, 0xEC09A614, 0x895A4A8F,
		0xA901FAC5, 0xB94B42B3, 0x2CF51028, 0xDBC4F362, 0x7C143FFF, 0x37044082, 0x8A92362D,
		0x3168C803, 0xAC8F8EE9, 0x4A52CF4B, 0x5915CF16, 0xFB105A27, 0xD084D7CC, 0xFF4BA120,
	},
	"six layers hi-res 640": {
		0x40C39365, 0x04F4F3ED, 0xAE5314FD, 0x43634315, 0x6A900D65, 0xF6F70D25, 0x89034CCD,
		0x8C40AA95, 0x994CCF4D, 0x32B2FDED, 0x3E96CAA5, 0x305F4C75, 0x3591143D, 0x0C61E3AD,
		0x12F6360D, 0x1CB344F5, 0xE98F07E5, 0x81069C35, 0xE2987FA5, 0x5391BF25, 0xB079440D,
		0xB5B72985, 0x8700D9DD, 0x9D6817FD, 0x292F32DD, 0x5114B0D5, 0x189B3CC5, 0x89A9F6D5,
	},
	"six layers LSMD3": {
		0xBBF6840D, 0xCE685F75, 0x6C3DFF75, 0x8551EECD, 0x8E8A13ED, 0x92CDDE55, 0x5801B965,
		0x944992D5, 0xFB37A385, 0x2D695AB5, 0x774F5835, 0x74C8ABFD, 0x83453B0D, 0xDF2EFE1D,
		0x765D571D, 0x4F0709F5, 0x3E5F4005, 0xCEAD2B2D, 0x01644175, 0x098A6B1D, 0xE21CE5AD,
		0xEA6004C5, 0x65F534FD, 0xF6987B2D, 0x391BEB0D, 0x8871A32D, 0x81E48E6D, 0x64EDBDA5,
		0x532AE26D, 0xCC34618D, 0x931597AD, 0x7F4D20DD, 0x94E2C3B5, 0x87345E9D, 0x2A98DB55,
		0x1BB21D4D, 0xB68D5F65, 0xFD3F1985, 0xD995D2B5, 0xD544B405, 0x66150C5D, 0xFCADD695,
		0x51C0EA2D, 0x13CB0535, 0x3E52DDD5, 0x841CCF95, 0xAE87E7A5, 0x6F0AE8AD, 0x9A36417D,
		0xB410E4BD, 0x916591D5, 0xD100B375, 0x128370FD, 0x5603758D, 0xB5B432BD, 0x0A88B035,
	},
	"six layers 352": {
		0x6E7DC0CD, 0xEEC9E0ED, 0x84D9EB75, 0x6BBE260D, 0x040313C5, 0xE3E22D45, 0x4C0AF8C5,
		0x60EF21ED, 0xD12C32C5, 0xB0EA61BD, 0x512F3AB5, 0x786A212D, 0x170EA20D, 0x4BAE8C05,
		0xF27BBCFD, 0xADB10D7D, 0x84B1C5A5, 0xCDBE8075, 0x322F1475, 0x418E794D, 0xE4943A1D,
		0xD933A01D, 0x4A6C855D, 0xAC8B6F2D, 0xD69954AD, 0xE57C599D, 0xE9D11975, 0xF966F94D,
	},
	"six layers PAL 256": {
		0x70459CC5, 0xE466CCE5, 0x6B2ED485, 0x5E8B305D, 0xDAA93C55, 0xA0FF1515, 0x6DDA28FD,
		0x4026CDFD, 0xCC66174D, 0x131D247D, 0x48008E5D, 0x28B4EE3D, 0x1A43289D, 0x537930DD,
		0x41EE1EDD, 0x3A405045, 0x2DF817D5, 0x2B3040F5, 0x4CBABA15, 0xD682523D, 0x77397C45,
		0x2BB696E5, 0x0777387D, 0x06EA374D, 0x2699F425, 0x3DA3CCFD, 0x33C9C2CD, 0xBEDC2EA5,
		0x3B49A7D5, 0xF6B5DB3D, 0xD437D30D, 0x9B941E45,
	},
}
