package core

import "testing"

// expectOut asserts the composited output pixel at (x, y) is within tol
// of (r, g, b).
func expectOut(t *testing.T, v *VDP2, x, y int, r, g, b, tol int, what string) {
	t.Helper()
	gr, gg, gb := outRGB(v, x, y)
	d := func(a uint8, w int) int {
		if int(a) > w {
			return int(a) - w
		}
		return w - int(a)
	}
	if d(gr, r) > tol || d(gg, g) > tol || d(gb, b) > tol {
		t.Errorf("%s: pixel(%d,%d) = (%d,%d,%d), want (%d,%d,%d) +-%d", what, x, y, gr, gg, gb, r, g, b, tol)
	}
}

// TestBackScreenColorOffsetB verifies the back screen takes color offset
// B when CLOFSL bit 5 selects it.
func TestBackScreenColorOffsetB(t *testing.T) {
	v := newTestVDP2()
	writeVRAM16(v, 0, 0x801F) // back screen red
	v.regs[vdp2BGON] = 0x0000
	v.regs[vdp2CLOFEN] = 1 << 5
	v.regs[vdp2CLOFSL] = 1 << 5
	v.regs[vdp2COAR], v.regs[vdp2COAG], v.regs[vdp2COAB] = 1, 2, 3
	v.regs[vdp2COBR], v.regs[vdp2COBG], v.regs[vdp2COBB] = 0x1F0, 40, 60 // R-16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 239, 40, 60, 0, "back screen offset B")
}

// TestHiResPaletteCCRestriction verifies that in hi-res with CRAM mode 1
// a palette-format scroll layer does not color-calculate (VDP2 manual
// Sec 12, color calculation restrictions), while CRAM mode 0 blends.
func TestHiResPaletteCCRestriction(t *testing.T) {
	v := setupTwoNBGLayers(t, 5, 3)
	v.regs[vdp2CCCTL] = 0x0001
	v.regs[vdp2CCRNA] = 16
	v.regs[vdp2RAMCTL] = 0x1000 // CRAM mode 1
	v.hiRes = true
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 0, 255, 0, 0, "hi-res CRAM mode 1: no palette CC")

	v = setupTwoNBGLayers(t, 5, 3)
	v.regs[vdp2CCCTL] = 0x0001
	v.regs[vdp2CCRNA] = 16
	v.hiRes = true
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 135, 119, 0, 2, "hi-res CRAM mode 0: blends")

	// Sprites: the restriction applies to palette-format sprite data,
	// selected by SPCTL SPCLMD (bit 5). SPWINEN (bit 4) has no bearing.
	sprite := func(spctl uint16, pixel uint16) *VDP2 {
		v := newTestVDP2()
		v.regs[vdp2SPCTL] = spctl | 0x0700 // all priorities calculate
		v.regs[vdp2PRISA] = 0x0005
		v.regs[vdp2CCCTL] = 1 << 6
		v.regs[vdp2CCRSA] = 16
		v.regs[vdp2RAMCTL] = 0x1000
		v.regs[vdp2BKTAU] = 0x0002
		v.regs[vdp2BKTAL] = 0xC000
		writeVRAM16(v, 0x58000, 0xFC00) // blue back screen
		writeCRAM16Test(v, 5, 0x001F)
		v.hiRes = true
		data := make([]byte, 512*256*2)
		data[0], data[1] = uint8(pixel>>8), uint8(pixel)
		renderTestFrameFB(v, vdp1FBView{data: data, width: 512, height: 256})
		return v
	}
	v = sprite(0x0000, 0x0005) // palette only
	expectOut(t, v, 0, 0, 255, 0, 0, 0, "hi-res CRAM mode 1 palette sprite (SPCLMD=0): no CC")
	v = sprite(0x0010, 0x0005) // palette only, sprite window enabled
	expectOut(t, v, 0, 0, 255, 0, 0, 0, "hi-res CRAM mode 1 palette sprite (SPWINEN set): no CC")
	v = sprite(0x0020, 0x801F) // mixed mode, RGB pixel
	expectOut(t, v, 0, 0, 119, 0, 135, 2, "hi-res CRAM mode 1 RGB sprite (SPCLMD=1): blends")
}

// TestMSBSpriteShadowOnTopSprite verifies the sprite shadow (VDP2 manual
// Sec 14.1 MSB Shadow): a type 2-7 sprite pixel with the MSB set and
// non-zero dot color is the already-written sprite with a shadow added,
// displayed at half brightness when it is the top image, and it does not
// affect a higher-priority layer above it.
func TestMSBSpriteShadowOnTopSprite(t *testing.T) {
	build := func(nbgPri uint8) (*VDP2, vdp1FBView) {
		v := setupThreeNBGLayers(t, nbgPri, 0, 0) // NBG0 green at x 0..7
		v.regs[vdp2SPCTL] = 0x0006
		v.regs[vdp2PRISA] = 0x0005
		writeCRAM16Test(v, 5, 0x001F)
		fb := vdp1FBView{data: make([]byte, 512*256*2), width: 512, height: 256}
		setFBPixel16(fb, 0, 0, 0x8005) // sprite shadow, red
		setFBPixel16(fb, 1, 0, 0x0005) // plain sprite, red
		setFBPixel16(fb, 9, 0, 0x8005) // sprite shadow over the back screen
		return v, fb
	}
	v, fb := build(3)
	renderTestFrameFB(v, fb)
	expectOut(t, v, 0, 0, 127, 0, 0, 1, "sprite shadow on top: half brightness")
	expectOut(t, v, 1, 0, 255, 0, 0, 0, "plain sprite on top")
	expectOut(t, v, 9, 0, 127, 0, 0, 1, "sprite shadow over the back screen")

	v, fb = build(7)
	renderTestFrameFB(v, fb)
	expectOut(t, v, 0, 0, 0, 255, 0, 0, "NBG0 above the sprite shadow is unaffected")
}

// TestCoefficientLineColorInsertion verifies the line color screen taken
// from a rotation coefficient (KLCE): the CRAM address is the line color
// table entry's bits 10:7 over the coefficient's 7 line color bits
// (VDP2 manual Sec 7.1), for RBG0 and for RBG1.
func TestCoefficientLineColorInsertion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T) *VDP2
		param  uint32
		ktKLCE uint16
		ktEn   uint16
		lnclen uint16
		ccctl  uint16
		ratio  int
	}{
		{"RBG0", setupRBG0ParamAB, 0x10000, 0x0010, 0x0001, 1 << 4, 1 << 4, vdp2CCRR},
		{"RBG1", setupRBG1Scene, 0x10080, 0x1000, 0x0100, 1 << 0, 1 << 0, vdp2CCRNA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func(klce bool) *VDP2 {
				v := tc.setup(t)
				v.regs[vdp2KTCTL] = tc.ktEn
				if klce {
					v.regs[vdp2KTCTL] |= tc.ktKLCE
				}
				v.regs[vdp2KTAOF] = 0
				writeRotParam32(v, tc.param, 0x54, 0x6000, 0x0000)
				writeRotParam32(v, tc.param, 0x58, 0x0000, 0x0000)
				writeVRAM16(v, 0x18000, 0x2501) // lc 0x25, kx 1.0
				writeVRAM16(v, 0x18002, 0x0000)
				// Line color table, single color, at 0x50000: entry 0x0180
				// (bits 10:7 = 3).
				v.regs[vdp2LNCLEN] = tc.lnclen
				v.regs[vdp2LCTAU] = 0x0002
				v.regs[vdp2LCTAL] = 0x8000
				writeVRAM16(v, 0x50000, 0x0180)
				writeCRAM16Test(v, 0x180, 0x03E0) // table address alone: green
				writeCRAM16Test(v, 0x1A5, 0x7C00) // 3<<7 | 0x25: blue
				v.regs[vdp2CCCTL] = tc.ccctl
				v.regs[tc.ratio] = 16
				return v
			}
			v := build(true)
			renderTestFrame(v)
			expectOut(t, v, 0, 0, 119, 0, 135, 2, "KLCE: line color from coefficient")
			v = build(false)
			renderTestFrame(v)
			expectOut(t, v, 0, 0, 119, 135, 0, 2, "no KLCE: line color from table")
		})
	}
}

// setupExtendedCCRGBScene builds NBG0 (palette, green, priority 5) over
// NBG1 (32768-color RGB, red, priority 3) over RBG0 (32768-color RGB
// bitmap, blue, priority 1), with a white line color screen.
func setupExtendedCCRGBScene(t *testing.T) *VDP2 {
	t.Helper()
	v := setupThreeNBGLayers(t, 5, 3, 1)
	// Drop NBG2, enable RBG0.
	v.regs[vdp2BGON] = 0x0003 | 1<<4
	// NBG1 32768-color: character 0x400 at 0x8000.
	v.regs[vdp2CHCTLA] = 0x3010
	writeVRAM16(v, 0x10002, 0x0400)
	fillVRAM16(v, 0x8000, 64, 0x801F)
	// RBG0 32768-color bitmap at map offset 3 (0x60000), identity
	// rotation with the parameter table at 0x14000.
	v.regs[vdp2CHCTLB] = 0x3200
	v.regs[vdp2MPOFR] = 0x0003
	v.regs[vdp2PRIR] = 0x0001
	v.regs[vdp2RPMD] = 0
	v.regs[vdp2RPTAU] = 0x0000
	v.regs[vdp2RPTAL] = 0xA000
	p := uint32(0x14000)
	writeRotParam32(v, p, 0x1C, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x2C, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x10, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x14, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x4C, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x50, 0x0001, 0x0000)
	fillVRAM16(v, 0x60000, 8, 0xFC00) // blue, MSB set
	// White line color for NBG0 at 0x50000 -> CRAM entry 40.
	v.regs[vdp2LNCLEN] = 0x0001
	v.regs[vdp2LCTAU] = 0x0002
	v.regs[vdp2LCTAL] = 0x8000
	writeVRAM16(v, 0x50000, 40)
	writeCRAM16Test(v, 40, 0x7FFF)
	return v
}

// TestExtendedCCLineColorRatio211 verifies extended color calculation
// with a line color screen inserted and an RGB fourth image (VDP2
// manual Table 12.2): in CRAM mode 1 the second image is line color/2
// + original second/4 + original third/4 (2:1:1); in CRAM mode 0 the
// fourth image is never added (2:1:0).
func TestExtendedCCLineColorRatio211(t *testing.T) {
	// Second image (line color white, NBG1 red, RBG0 blue):
	//   mode 1: (127+63, 127, 127+63) = (190,127,190)
	//   mode 0: (190,127,127)
	// Output: green*15/32 + second*17/32.
	// CCCTL: EXCCEN (bit 10), LCCCEN (bit 5, the inserted line color
	// blends with the image below it), N1CCEN, N0CCEN.
	v := setupExtendedCCRGBScene(t)
	v.regs[vdp2RAMCTL] = 0x1000
	v.regs[vdp2CCCTL] = 0x0423
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 101, 187, 101, 3, "CRAM mode 1: 2:1:1")

	v = setupExtendedCCRGBScene(t)
	v.regs[vdp2CCCTL] = 0x0423
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 101, 187, 67, 3, "CRAM mode 0: 2:1:0")

	// Without LCCCEN the line color is the whole second image.
	v = setupExtendedCCRGBScene(t)
	v.regs[vdp2CCCTL] = 0x0403
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 135, 255, 135, 2, "LCCCEN clear: line color alone")
}

// TestLayerCCRatioRegisters verifies the color calculation ratio source
// per top layer: NBG1 from CCRNA bits 12:8, NBG2 from CCRNB bits 4:0,
// NBG3 from CCRNB bits 12:8, RBG0 from CCRR.
func TestLayerCCRatioRegisters(t *testing.T) {
	// NBG1 (red) over NBG0 (green).
	v := setupThreeNBGLayers(t, 3, 5, 1)
	v.regs[vdp2CCCTL] = 0x0002
	v.regs[vdp2CCRNA] = 16 << 8
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 119, 135, 0, 2, "NBG1 ratio from CCRNA high byte")

	// NBG2 (blue) over NBG1 (red).
	v = setupThreeNBGLayers(t, 1, 3, 5)
	v.regs[vdp2CCCTL] = 0x0004
	v.regs[vdp2CCRNB] = 16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 135, 0, 119, 2, "NBG2 ratio from CCRNB low byte")

	// NBG3 (white, 256-color at page 12) over NBG0 (green).
	v = setupThreeNBGLayers(t, 5, 3, 1)
	v.regs[vdp2BGON] |= 0x0008
	v.regs[vdp2CHCTLB] |= 0x0020
	v.regs[vdp2PNCN3] = 0x0000
	v.regs[vdp2MPABN3] = 0x000C
	v.regs[vdp2MPCDN3] = 0x000C
	v.regs[vdp2PRINB] |= 7 << 8
	writeVRAM16(v, 0x30000, 0x0000)
	writeVRAM16(v, 0x30002, 0x0004)
	for i := 0; i < 64; i++ {
		v.vram[0x80+i] = 40
	}
	writeCRAM16Test(v, 40, 0x7FFF)
	v.regs[vdp2CCCTL] = 0x0008
	v.regs[vdp2CCRNB] = 16 << 8
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 119, 255, 119, 2, "NBG3 ratio from CCRNB high byte")

	// RBG0 (red) over a blue back screen at 0x58000.
	v = setupRBG0ParamAB(t)
	v.regs[vdp2BKTAU] = 0x0002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000, 0xFC00)
	v.regs[vdp2CCCTL] = 0x0010
	v.regs[vdp2CCRR] = 16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 119, 0, 135, 2, "RBG0 ratio from CCRR")
}

// TestW1LineWindowComposite verifies the W1 line window table on a
// scroll layer: each line's X range comes from its LWTA1 entry, an entry
// with start > end excludes the line, and the range only applies on
// lines inside WPSY1..WPEY1.
func TestW1LineWindowComposite(t *testing.T) {
	build := func(wpey1 uint16) *VDP2 {
		v := setupNBG0FullTile(t)
		// Blue back screen at 0x58000 so masked pixels are identifiable.
		v.regs[vdp2BKTAU] = 0x0002
		v.regs[vdp2BKTAL] = 0xC000
		writeVRAM16(v, 0x58000, 0xFC00)
		v.regs[vdp2WCTLA] = 0x0008 // NBG0 W1 enable, area inside
		v.regs[vdp2WPSY1] = 0
		v.regs[vdp2WPEY1] = wpey1
		v.regs[vdp2LWTA1U] = 0x8002
		v.regs[vdp2LWTA1L] = 0x0000 // table at 0x40000
		// The red tile covers x 0..7 only.
		writeVRAM16(v, 0x40000, 0)
		writeVRAM16(v, 0x40002, 8) // line 0: x 0..4
		writeVRAM16(v, 0x40004, 20)
		writeVRAM16(v, 0x40006, 0) // line 1: excluded
		writeVRAM16(v, 0x40008, 8)
		writeVRAM16(v, 0x4000A, 40) // line 2: x 4..20
		return v
	}
	v := build(1)
	renderTestFrame(v)
	expectOut(t, v, 3, 0, 0, 0, 255, 0, "line 0 x 3 masked")
	expectOut(t, v, 4, 0, 0, 0, 255, 0, "line 0 x 4 masked")
	expectOut(t, v, 6, 0, 255, 0, 0, 0, "line 0 x 6 outside the range")
	expectOut(t, v, 3, 1, 255, 0, 0, 0, "line 1 excluded entry")
	expectOut(t, v, 5, 2, 255, 0, 0, 0, "line 2 outside WPEY1")

	v = build(5)
	renderTestFrame(v)
	expectOut(t, v, 3, 2, 255, 0, 0, 0, "line 2 x 3 before the range")
	expectOut(t, v, 5, 2, 0, 0, 255, 0, "line 2 x 5 masked")
}

// TestGradationSourceSelection verifies the gradation (BOKEN) source
// layer selection (CCCTL bits 14:12): 1 RBG0, 2 NBG0 (RBG1 when RBG1 is
// active), 4 NBG1, 5 NBG2, 6 NBG3.
func TestGradationSourceSelection(t *testing.T) {
	same := func(a, b []uint32) bool {
		return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
	}
	for _, tc := range []struct {
		name  string
		bokn  uint16
		rbg1  bool
		wantf func(v *VDP2) []uint32
	}{
		{"RBG0", 1, false, func(v *VDP2) []uint32 { return v.rbg0Buf }},
		{"NBG0", 2, false, func(v *VDP2) []uint32 { return v.layerBufs[0] }},
		{"RBG1", 2, true, func(v *VDP2) []uint32 { return v.rbg1Buf }},
		{"NBG1", 4, false, func(v *VDP2) []uint32 { return v.layerBufs[1] }},
		{"NBG2", 5, false, func(v *VDP2) []uint32 { return v.layerBufs[2] }},
		{"NBG3", 6, false, func(v *VDP2) []uint32 { return v.layerBufs[3] }},
	} {
		v := newTestVDP2()
		if tc.rbg1 {
			v.regs[vdp2BGON] = 1<<4 | 1<<5
		}
		v.regs[vdp2CCCTL] = 0x8000 | tc.bokn<<12
		v.BeginFrame()
		v.decodeLineState()
		if !same(v.frame.gradBuf, tc.wantf(v)) {
			t.Errorf("%s: gradation source not the expected layer buffer", tc.name)
		}
	}
}

// TestExtendedCCSecondImageTie verifies that with two candidates at the
// same priority below the top layer, the lower screen number is the
// second image: NBG1 (CC on) rather than NBG2 (CC off), so the second and
// third images blend 2:2:0.
func TestExtendedCCSecondImageTie(t *testing.T) {
	v := setupThreeNBGLayers(t, 5, 3, 3)
	v.regs[vdp2CCCTL] = 0x0403 // EXCCEN, N0CCEN, N1CCEN
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	// second = (red + blue)/2 = (127,0,127); out = green*15/32 + second*17/32.
	expectOut(t, v, 0, 0, 67, 119, 67, 2, "NBG1 wins the tie as second image")
}

// TestSpriteCCRatioAndLineColor verifies color calculation with the
// sprite as the top image: the ratio comes from the sprite's CC ratio
// register, the line color screen inserts for sprites through LNCLEN
// bit 5, and with CCRTMD the sprite as the second image supplies its
// own ratio.
func TestSpriteCCRatioAndLineColor(t *testing.T) {
	spriteFB := func(pixel uint16) vdp1FBView {
		data := make([]byte, 512*256*2)
		data[0], data[1] = uint8(pixel>>8), uint8(pixel)
		return vdp1FBView{data: data, width: 512, height: 256}
	}
	// Sprite (red, CC bits 0) over a blue back screen, sprite ratio 16.
	v := newTestVDP2()
	v.regs[vdp2SPCTL] = 0x0700 // type 0, SPCCCS 0, SPCCN 7: all priorities calculate
	v.regs[vdp2PRISA] = 0x0005
	v.regs[vdp2CCCTL] = 1 << 6
	v.regs[vdp2CCRSA] = 16
	v.regs[vdp2BKTAU] = 0x0002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000, 0xFC00)
	writeCRAM16Test(v, 5, 0x001F)
	renderTestFrameFB(v, spriteFB(0x0005))
	expectOut(t, v, 0, 0, 119, 0, 135, 2, "sprite ratio from CCRSA")

	// Line color inserted below the sprite (LNCLEN bit 5), white. A fresh
	// instance so the CRAM cache is built after the entry is written.
	v = newTestVDP2()
	v.regs[vdp2SPCTL] = 0x0700
	v.regs[vdp2PRISA] = 0x0005
	v.regs[vdp2CCCTL] = 1 << 6
	v.regs[vdp2CCRSA] = 16
	writeCRAM16Test(v, 5, 0x001F)
	v.regs[vdp2LNCLEN] = 1 << 5
	v.regs[vdp2LCTAU] = 0x0002
	v.regs[vdp2LCTAL] = 0x8000
	writeVRAM16(v, 0x50000, 40)
	writeCRAM16Test(v, 40, 0x7FFF)
	renderTestFrameFB(v, spriteFB(0x0005))
	expectOut(t, v, 0, 0, 255, 135, 135, 2, "sprite over line color")

	// CCRTMD: NBG0 (green, priority 5, CC on) over the sprite (priority 3);
	// the ratio comes from the sprite's register (8).
	v = setupThreeNBGLayers(t, 5, 0, 0)
	v.regs[vdp2SPCTL] = 0x0000
	v.regs[vdp2PRISA] = 0x0003
	v.regs[vdp2CCCTL] = 0x0201 // CCRTMD, N0CCEN
	v.regs[vdp2CCRSA] = 8
	writeCRAM16Test(v, 5, 0x001F)
	renderTestFrameFB(v, spriteFB(0x0005))
	expectOut(t, v, 0, 0, 71, 183, 0, 2, "second-image sprite ratio with CCRTMD")
}

// TestExtendedCCGating verifies the extended color calculation edge cases
// (VDP2 manual Table 12.2): the back screen as second image never blends
// further, a palette-format second image in CRAM mode 1 blends 4:0:0,
// a palette-format fourth image in CRAM mode 1 caps the line color blend
// at 2:2:0, and two candidates tied for third resolve to the lower
// screen number.
func TestExtendedCCGating(t *testing.T) {
	v := setupNBG0WithBackScreen(t)
	v.regs[vdp2CCCTL] = 0x0401 // EXCCEN, N0CCEN
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 119, 135, 0, 2, "back screen second image")

	v = setupThreeNBGLayers(t, 5, 3, 1)
	v.regs[vdp2RAMCTL] = 0x1000
	v.regs[vdp2CCCTL] = 0x0403
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 135, 119, 0, 2, "CRAM mode 1 palette second: 4:0:0")

	// NBG1 RGB second, NBG2 palette third, line color white inserted.
	v = setupThreeNBGLayers(t, 5, 3, 1)
	v.regs[vdp2CHCTLA] = 0x3010
	writeVRAM16(v, 0x10002, 0x0400)
	fillVRAM16(v, 0x8000, 64, 0x801F)
	v.regs[vdp2LNCLEN] = 0x0001
	v.regs[vdp2LCTAU] = 0x0002
	v.regs[vdp2LCTAL] = 0x8000
	writeVRAM16(v, 0x50000, 40)
	writeCRAM16Test(v, 40, 0x7FFF)
	v.regs[vdp2RAMCTL] = 0x1000
	v.regs[vdp2CCCTL] = 0x0423
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	// second = (white + red)/2 = (255,127,127); out = green*15/32 + second*17/32.
	expectOut(t, v, 0, 0, 135, 186, 67, 3, "CRAM mode 1 palette fourth: 2:2:0")

	// Third-image tie: NBG2 (blue) and NBG3 (white) both at priority 1.
	v = setupThreeNBGLayers(t, 5, 3, 1)
	v.regs[vdp2BGON] |= 0x0008
	v.regs[vdp2CHCTLB] |= 0x0020
	v.regs[vdp2PNCN3] = 0x0000
	v.regs[vdp2MPABN3] = 0x000C
	v.regs[vdp2MPCDN3] = 0x000C
	v.regs[vdp2PRINB] |= 1 << 8
	writeVRAM16(v, 0x30000, 0x0000)
	writeVRAM16(v, 0x30002, 0x0004)
	for i := 0; i < 64; i++ {
		v.vram[0x80+i] = 40
	}
	writeCRAM16Test(v, 40, 0x7FFF)
	v.regs[vdp2CCCTL] = 0x0403
	v.regs[vdp2CCRNA] = 16
	renderTestFrame(v)
	// second = (red + blue)/2 = (127,0,127); out = (67,119,67).
	expectOut(t, v, 0, 0, 67, 119, 67, 2, "third image tie resolves to NBG2")
}

// TestGradationDisabledInHiResAndCRAMModes verifies gradation (BOKEN) is
// ignored in hi-res and in CRAM modes other than 0.
func TestGradationDisabledInHiResAndCRAMModes(t *testing.T) {
	v := newTestVDP2()
	v.regs[vdp2CCCTL] = 0x9000 // BOKEN, RBG0 source
	v.hiRes = true
	v.BeginFrame()
	v.decodeLineState()
	if v.frame.gradBuf != nil {
		t.Error("hi-res: gradation source should be nil")
	}
	v = newTestVDP2()
	v.regs[vdp2CCCTL] = 0x9000
	v.regs[vdp2RAMCTL] = 0x1000
	v.BeginFrame()
	v.decodeLineState()
	if v.frame.gradBuf != nil {
		t.Error("CRAM mode 1: gradation source should be nil")
	}
}
