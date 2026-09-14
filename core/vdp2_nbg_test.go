package core

import "testing"

// setupNBG0LineScrollScene builds NBG0 as a 256-color 2-word cell screen
// with one tile at cell (0,0) whose dot is 10 + column on rows 0-3 and
// 20 + column on rows 4-7. CRAM entries 10-17 are red shades (raw value
// column + 1) and entries 20-27 green shades (raw (column + 1) << 5).
// The line scroll table is placed at VRAM 0x20000.
func setupNBG0LineScrollScene(t *testing.T) *VDP2 {
	t.Helper()
	v := newTestVDP2()
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0010
	v.regs[vdp2PNCN0] = 0x0000
	v.regs[vdp2MPABN0] = 0x0000
	v.regs[vdp2MPCDN0] = 0x0000
	v.regs[vdp2PRINA] = 0x0001
	writeVRAM16(v, 0, 0x0000)
	writeVRAM16(v, 2, 0x0001)
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			dot := uint8(10 + col)
			if row >= 4 {
				dot = uint8(20 + col)
			}
			v.vram[0x20+row*8+col] = dot
		}
	}
	for i := 0; i < 8; i++ {
		writeCRAM16Test(v, uint32(10+i), uint16(i+1))
		writeCRAM16Test(v, uint32(20+i), uint16(i+1)<<5)
	}
	v.regs[vdp2LSTA0U] = 0x0001
	v.regs[vdp2LSTA0L] = 0x0000
	return v
}

// writeCRAM16Test writes a 16-bit CRAM entry (mode 0 layout).
func writeCRAM16Test(v *VDP2, entry uint32, val uint16) {
	v.cram[entry*2] = uint8(val >> 8)
	v.cram[entry*2+1] = uint8(val)
}

// writeLineScrollEntry writes one 32-bit line scroll table field: the
// integer part in the high word (bits 10:0) and the fraction in the low
// word (bits 15:8). Zoom fields use only bits 2:0 of the integer.
func writeLineScrollEntry(v *VDP2, addr uint32, intPart uint16, frac uint8) {
	writeVRAM16(v, addr, intPart)
	writeVRAM16(v, addr+2, uint16(frac)<<8)
}

// expectRedShade asserts the layer pixel shows red shade column (raw
// CRAM value column + 1).
func expectRedShade(t *testing.T, v *VDP2, buf []uint32, x, y, column int, what string) {
	t.Helper()
	wantR, _, _ := rgb555ToRGB(uint16(column + 1))
	px := buf[y*v.frame.width+x]
	if px == 0 {
		t.Errorf("%s: pixel(%d,%d) transparent, want red shade %d", what, x, y, column)
		return
	}
	if r, g := uint8(px>>16), uint8(px>>8); r != wantR || g != 0 {
		t.Errorf("%s: pixel(%d,%d) = (%d,%d), want red shade %d (%d,0)", what, x, y, r, g, column, wantR)
	}
}

// expectGreenShade asserts the layer pixel shows green shade column (raw
// CRAM value (column + 1) << 5).
func expectGreenShade(t *testing.T, v *VDP2, buf []uint32, x, y, column int, what string) {
	t.Helper()
	_, wantG, _ := rgb555ToRGB(uint16(column+1) << 5)
	px := buf[y*v.frame.width+x]
	if px == 0 {
		t.Errorf("%s: pixel(%d,%d) transparent, want green shade %d", what, x, y, column)
		return
	}
	if r, g := uint8(px>>16), uint8(px>>8); g != wantG || r != 0 {
		t.Errorf("%s: pixel(%d,%d) = (%d,%d), want green shade %d (0,%d)", what, x, y, r, g, column, wantG)
	}
}

// TestLineScrollY verifies the vertical line scroll field (SCRCTL N0LSCY):
// each line's table value is that line's vertical coordinate (added to
// the screen scroll, not to the line index), so line 1 with a table
// value of 4 samples row 4.
func TestLineScrollY(t *testing.T) {
	v := setupNBG0LineScrollScene(t)
	v.regs[vdp2SCRCTL] = 0x0004
	writeLineScrollEntry(v, 0x20000, 0, 0) // line 0: row 0
	writeLineScrollEntry(v, 0x20004, 4, 0) // line 1: row 4
	writeLineScrollEntry(v, 0x20008, 1, 0) // line 2: row 1
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 0, 0, 0, "line 0 row 0")
	expectRedShade(t, v, buf, 2, 0, 2, "line 0 row 0 column 2")
	expectGreenShade(t, v, buf, 0, 1, 0, "line 1 row 4")
	expectGreenShade(t, v, buf, 2, 1, 2, "line 1 row 4 column 2")
	expectRedShade(t, v, buf, 0, 2, 0, "line 2 row 1")
}

// TestLineZoomX verifies the horizontal line zoom field (SCRCTL N0LZMX):
// the table's 3.8 coordinate increment replaces ZMXIN0/ZMXDN0 per line.
func TestLineZoomX(t *testing.T) {
	v := setupNBG0LineScrollScene(t)
	v.regs[vdp2SCRCTL] = 0x0008
	writeLineScrollEntry(v, 0x20000, 1, 0)    // line 0: 1.0
	writeLineScrollEntry(v, 0x20004, 2, 0)    // line 1: 2.0
	writeLineScrollEntry(v, 0x20008, 0, 0x80) // line 2: 0.5
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 1, 0, 1, "line 0 zoom 1.0 x 1")
	expectRedShade(t, v, buf, 1, 1, 2, "line 1 zoom 2.0 x 1")
	expectRedShade(t, v, buf, 3, 1, 6, "line 1 zoom 2.0 x 3")
	expectRedShade(t, v, buf, 3, 2, 1, "line 2 zoom 0.5 x 3")
}

// TestLineScrollAllFields verifies the 12-byte table entry stride when
// the X, Y, and zoom fields are all enabled: line 1's entry supplies X
// +2, vertical coordinate 4, and zoom 1.0.
func TestLineScrollAllFields(t *testing.T) {
	v := setupNBG0LineScrollScene(t)
	v.regs[vdp2SCRCTL] = 0x000E
	writeLineScrollEntry(v, 0x20000, 0, 0) // line 0 X
	writeLineScrollEntry(v, 0x20004, 0, 0) // line 0 Y
	writeLineScrollEntry(v, 0x20008, 1, 0) // line 0 zoom
	writeLineScrollEntry(v, 0x2000C, 2, 0) // line 1 X
	writeLineScrollEntry(v, 0x20010, 4, 0) // line 1 Y
	writeLineScrollEntry(v, 0x20014, 1, 0) // line 1 zoom
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 0, 0, 0, "line 0")
	expectGreenShade(t, v, buf, 0, 1, 2, "line 1 X+2 row 4")
	expectGreenShade(t, v, buf, 1, 1, 3, "line 1 X+2 row 4 column 1")
}

// TestLineScrollLSMD3 verifies that in double-density interlace the line
// scroll table is indexed by the displayed line (2y + field).
func TestLineScrollLSMD3(t *testing.T) {
	v := setupNBG0LineScrollScene(t)
	// Uniform rows so the doubled Y does not change the column colors.
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			v.vram[0x20+row*8+col] = uint8(10 + col)
		}
	}
	v.regs[vdp2SCRCTL] = 0x0002
	for line := uint32(0); line < 8; line++ {
		writeLineScrollEntry(v, 0x20000+line*4, uint16(line), 0) // displayed line d: X +d
	}
	setLSMD3(v, true)
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 0, 0, 1, "field 1 line 0 = displayed 1")
	expectRedShade(t, v, buf, 0, 1, 3, "field 1 line 1 = displayed 3")
	setLSMD3(v, false)
	clear(buf)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 0, 1, 2, "field 0 line 1 = displayed 2")
}

// TestLineScrollBitmap verifies line scroll X, Y, and zoom on an NBG
// bitmap screen (256-color, 512x256): bitmap dot (x, y) is x + 10 on
// rows 0-3 and x + 20 on rows 4-7.
func TestLineScrollBitmap(t *testing.T) {
	v := newTestVDP2()
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0012
	v.regs[vdp2BMPNA] = 0x0000
	v.regs[vdp2MPOFN] = 0x0000
	v.regs[vdp2PRINA] = 0x0001
	for row := 0; row < 8; row++ {
		for x := 0; x < 8; x++ {
			dot := uint8(10 + x)
			if row >= 4 {
				dot = uint8(20 + x)
			}
			v.vram[row*512+x] = dot
		}
	}
	for i := 0; i < 8; i++ {
		writeCRAM16Test(v, uint32(10+i), uint16(i+1))
		writeCRAM16Test(v, uint32(20+i), uint16(i+1)<<5)
	}
	v.regs[vdp2LSTA0U] = 0x0001
	v.regs[vdp2LSTA0L] = 0x0000
	v.regs[vdp2SCRCTL] = 0x000E
	writeLineScrollEntry(v, 0x20000, 0, 0)
	writeLineScrollEntry(v, 0x20004, 0, 0)
	writeLineScrollEntry(v, 0x20008, 1, 0)
	writeLineScrollEntry(v, 0x2000C, 2, 0) // line 1 X +2
	writeLineScrollEntry(v, 0x20010, 4, 0) // line 1 vertical coordinate 4
	writeLineScrollEntry(v, 0x20014, 2, 0) // line 1 zoom 2.0
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 1, 0, 1, "line 0")
	expectGreenShade(t, v, buf, 0, 1, 2, "line 1 x 0 -> dot 2 row 4")
	expectGreenShade(t, v, buf, 1, 1, 4, "line 1 x 1 -> dot 4 row 4 (zoom 2)")

	// LSMD3: field 1 line 0 uses displayed line 1's entry.
	setLSMD3(v, true)
	clear(buf)
	renderTestNBG(v, 0, buf)
	expectGreenShade(t, v, buf, 0, 0, 2, "LSMD3 field 1 line 0 = displayed 1")
}

// TestDecodePattern1WordAllModes verifies every 1-word pattern name
// layout (VDP2 manual Sec 4.6, Table 4.x): character size 1x1/2x2,
// 16-color/other, and character number supplement mode 0/1, with
// non-zero supplement bits in the pattern name control register (palette
// supplement bits 7:5 and character supplement bits 4:0).
func TestDecodePattern1WordAllModes(t *testing.T) {
	const pn = uint16(0xABCD)
	const pnc = uint16(0x00FF) // supp palette 7, supp character 0x1F
	cases := []struct {
		name        string
		colorMode   uint8
		aux1        bool
		charSize1x1 bool
		wantChar    uint32
		wantPal     uint8
		wantH       bool
		wantV       bool
	}{
		{"16-color aux0 1x1", 0, false, true, 0x7FCD, 0x7A, false, true},
		{"16-color aux0 2x2", 0, false, false, 0x7F37, 0x7A, false, true},
		{"16-color aux1 1x1", 0, true, true, 0x7BCD, 0x7A, false, false},
		{"16-color aux1 2x2", 0, true, false, 0x6F37, 0x7A, false, false},
		{"256-color aux0 1x1", 1, false, true, 0x7FCD, 0x20, false, true},
		{"256-color aux0 2x2", 1, false, false, 0x7F37, 0x20, false, true},
		{"256-color aux1 1x1", 1, true, true, 0x7BCD, 0x20, false, false},
		{"256-color aux1 2x2", 1, true, false, 0x6F37, 0x20, false, false},
	}
	for _, tc := range cases {
		charNum, palette, hflip, vflip := decodePattern1Word(pn, pnc, tc.colorMode, tc.aux1, tc.charSize1x1)
		if charNum != tc.wantChar || palette != tc.wantPal || hflip != tc.wantH || vflip != tc.wantV {
			t.Errorf("%s: char=0x%X pal=0x%02X h=%v v=%v, want 0x%X 0x%02X %v %v",
				tc.name, charNum, palette, hflip, vflip, tc.wantChar, tc.wantPal, tc.wantH, tc.wantV)
		}
	}
	// Horizontal flip bit 10 in the aux 0 formats.
	if _, _, hflip, _ := decodePattern1Word(0x0400, 0, 0, false, true); !hflip {
		t.Error("16-color aux0: bit 10 should set hflip")
	}
	if _, _, hflip, _ := decodePattern1Word(0x0400, 0, 1, false, true); !hflip {
		t.Error("256-color aux0: bit 10 should set hflip")
	}
}

// TestRenderNBG_Bitmap2048And32K verifies the NBG bitmap path in the
// 2048-color and 32768-color formats: dot 0 opaque, dot 1 the format's
// transparent value (index 0 / MSB clear), and N0TPON making it opaque.
func TestRenderNBG_Bitmap2048And32K(t *testing.T) {
	for _, tc := range []struct {
		name       string
		chctla     uint16
		w0, w1     uint16
		tpOn       [3]uint8
		wantOpaque [3]uint8
	}{
		{"2048-color", 0x0022, 0x0005, 0x0000, [3]uint8{0, 255, 0}, [3]uint8{255, 0, 0}},
		{"32768-color", 0x0032, 0x801F, 0x001F, [3]uint8{255, 0, 0}, [3]uint8{255, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVDP2()
			v.regs[vdp2BGON] = 0x0001
			v.regs[vdp2CHCTLA] = tc.chctla
			v.regs[vdp2BMPNA] = 0x0000
			v.regs[vdp2MPOFN] = 0x0000
			v.regs[vdp2PRINA] = 0x0001
			writeVRAM16(v, 0, tc.w0)
			writeVRAM16(v, 2, tc.w1)
			writeCRAM16Test(v, 0, 0x03E0) // entry 0: green
			writeCRAM16Test(v, 5, 0x001F) // entry 5: red
			buf := make([]uint32, 352*256)
			renderTestNBG(v, 0, buf)
			expectRGB(t, v, buf, 0, 0, tc.wantOpaque[0], tc.wantOpaque[1], tc.wantOpaque[2], tc.name+" opaque dot")
			expectTransparent(t, v, buf, 1, 0, tc.name+" transparent dot")
			v.regs[vdp2BGON] |= 1 << 8
			clear(buf)
			renderTestNBG(v, 0, buf)
			expectRGB(t, v, buf, 1, 0, tc.tpOn[0], tc.tpOn[1], tc.tpOn[2], tc.name+" transparent dot with N0TPON")
		})
	}
}

// TestNBGBitmap1024Wide verifies the 1024-dot bitmap width (BMSZ bit 1)
// for NBG0 and NBG1: horizontal scroll 600 reads dot 600 rather than
// wrapping at 512.
func TestNBGBitmap1024Wide(t *testing.T) {
	for _, tc := range []struct {
		name   string
		screen int
		setup  func(v *VDP2, wide bool)
	}{
		{"NBG0", 0, func(v *VDP2, wide bool) {
			v.regs[vdp2BGON] = 0x0001
			v.regs[vdp2CHCTLA] = 0x0012
			if wide {
				v.regs[vdp2CHCTLA] |= 0x0008
			}
			v.regs[vdp2PRINA] = 0x0001
			v.regs[vdp2SCXIN0] = 600
		}},
		{"NBG1", 1, func(v *VDP2, wide bool) {
			v.regs[vdp2BGON] = 0x0002
			v.regs[vdp2CHCTLA] = 0x1200
			if wide {
				v.regs[vdp2CHCTLA] |= 0x0800
			}
			v.regs[vdp2PRINA] = 0x0100
			v.regs[vdp2SCXIN1] = 600
		}},
	} {
		for _, wide := range []bool{false, true} {
			v := newTestVDP2()
			tc.setup(v, wide)
			v.regs[vdp2BMPNA] = 0
			v.regs[vdp2MPOFN] = 0
			v.vram[600] = 5 // dot 600 of a 1024-wide row 0
			v.vram[88] = 6  // dot 88 of a 512-wide row 0 (600 mod 512)
			writeCRAM16Test(v, 5, 0x001F)
			writeCRAM16Test(v, 6, 0x03E0)
			buf := make([]uint32, 352*256)
			renderTestNBG(v, tc.screen, buf)
			if wide {
				expectRGB(t, v, buf, 0, 0, 255, 0, 0, tc.name+" 1024 wide")
			} else {
				expectRGB(t, v, buf, 0, 0, 0, 255, 0, tc.name+" 512 wide")
			}
		}
	}
}

// setupNBG1ColumnScene builds NBG1 as a 256-color cell screen (page 4)
// with one tile whose dot is 10 + column, red shades in CRAM 10-17.
func setupNBG1ColumnScene(t *testing.T) *VDP2 {
	t.Helper()
	v := newTestVDP2()
	v.regs[vdp2BGON] = 0x0002
	v.regs[vdp2CHCTLA] = 0x1000
	v.regs[vdp2PNCN1] = 0x0000
	v.regs[vdp2MPABN1] = 0x0004
	v.regs[vdp2MPCDN1] = 0x0004
	v.regs[vdp2PRINA] = 0x0100
	writeVRAM16(v, 0x10000, 0x0000)
	writeVRAM16(v, 0x10002, 0x0400)
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			v.vram[0x8000+row*8+col] = uint8(10 + col)
		}
	}
	for i := 0; i < 8; i++ {
		writeCRAM16Test(v, uint32(10+i), uint16(i+1))
	}
	return v
}

// TestZMCTLQuarterClampNBG1 verifies the NBG1 horizontal increment clamp
// (VDP2 manual Sec 5.2 Table 5.2): without a reduction enable the
// increment is limited to 1.0, with the quarter reduction enable (ZMCTL
// bit 9) to 4.0.
func TestZMCTLQuarterClampNBG1(t *testing.T) {
	v := setupNBG1ColumnScene(t)
	v.regs[vdp2ZMXIN1] = 0x0006
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 1, buf)
	expectRedShade(t, v, buf, 1, 0, 1, "no reduction enable: increment clamped to 1.0")
	v.regs[vdp2ZMCTL] = 0x0200
	clear(buf)
	renderTestNBG(v, 1, buf)
	expectRedShade(t, v, buf, 1, 0, 4, "quarter reduction enable: increment clamped to 4.0")
}

// TestLineScrollNBG1Table verifies NBG1's line scroll uses its own table
// address registers (LSTA1U/LSTA1L) and SCRCTL bits 11:9.
func TestLineScrollNBG1Table(t *testing.T) {
	v := setupNBG1ColumnScene(t)
	v.regs[vdp2SCRCTL] = 0x0200 // N1LSCX
	v.regs[vdp2LSTA1U] = 0x0001
	v.regs[vdp2LSTA1L] = 0x0000 // table at 0x20000
	writeLineScrollEntry(v, 0x20000, 0, 0)
	writeLineScrollEntry(v, 0x20004, 2, 0)
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 1, buf)
	expectRedShade(t, v, buf, 0, 0, 0, "line 0")
	expectRedShade(t, v, buf, 0, 1, 2, "line 1 X+2")
}

// TestLineScrollYInterval verifies the vertical line scroll with a
// two-line table interval (SCRCTL LSS=01): both lines of an interval
// share the entry's vertical coordinate and advance within it.
func TestLineScrollYInterval(t *testing.T) {
	v := setupNBG0LineScrollScene(t)
	v.regs[vdp2SCRCTL] = 0x0004 | 0x0010   // N0LSCY, interval 2
	writeLineScrollEntry(v, 0x20000, 0, 0) // lines 0-1: rows 0, 1
	writeLineScrollEntry(v, 0x20004, 4, 0) // lines 2-3: rows 4, 5
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 0, 0, 0, "line 0 row 0")
	expectRedShade(t, v, buf, 0, 1, 0, "line 1 row 1")
	expectGreenShade(t, v, buf, 0, 2, 0, "line 2 row 4")
	expectGreenShade(t, v, buf, 0, 3, 0, "line 3 row 5")

	// Bitmap counterpart.
	v = newTestVDP2()
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0012
	v.regs[vdp2PRINA] = 0x0001
	for row := 0; row < 8; row++ {
		dot := uint8(10)
		if row >= 4 {
			dot = 20
		}
		v.vram[row*512] = dot
	}
	writeCRAM16Test(v, 10, 1)
	writeCRAM16Test(v, 20, 1<<5)
	v.regs[vdp2LSTA0U] = 0x0001
	v.regs[vdp2LSTA0L] = 0x0000
	v.regs[vdp2SCRCTL] = 0x0004 | 0x0010
	writeLineScrollEntry(v, 0x20000, 0, 0)
	writeLineScrollEntry(v, 0x20004, 4, 0)
	clear(buf)
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 0, 1, 0, "bitmap line 1 row 1")
	expectGreenShade(t, v, buf, 0, 3, 0, "bitmap line 3 row 5")
}

// TestRenderNBG_2x2VerticalFlip verifies the vertical flip of a 2x2 NBG
// character swaps the sub-cell rows.
func TestRenderNBG_2x2VerticalFlip(t *testing.T) {
	v := setupNBG0_4bpp_2word(t)
	v.regs[vdp2CHCTLA] = 0x0001 // 2x2 characters
	writeRBGTestTile(v, 0x400, rbgTestRed)
	writeRBGTestTile(v, 0x401, rbgTestGreen)
	writeRBGTestTile(v, 0x402, rbgTestBlue)
	writeRBGTestTile(v, 0x403, 0x6)
	writeRBGTestPalette(v)
	v.cram[44], v.cram[45] = 0x7F, 0xFF
	writeVRAM16(v, 0, 0x8001) // vflip, palette 1
	writeVRAM16(v, 2, 0x0400)
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "vflip top-left shows bottom-left sub-cell")
	expectRGB(t, v, buf, 8, 0, 255, 255, 255, "vflip top-right shows bottom-right sub-cell")
	expectRGB(t, v, buf, 0, 8, 255, 0, 0, "vflip bottom-left shows top-left sub-cell")
}

// TestNBGSpecialCCMode2And3 verifies NBG special color calculation mode 2
// (special CC bit and special function code match) on a cell screen and
// mode 3 on RGB-format cell and bitmap screens (screen CC enable alone).
func TestNBGSpecialCCMode2And3(t *testing.T) {
	// Mode 2 on the 256-color cell of setupThreeNBGLayers (dot 10, pair 5).
	run := func(msw, sfcode uint16) uint32 {
		v := setupThreeNBGLayers(t, 5, 0, 0)
		writeVRAM16(v, 0, msw)
		v.regs[vdp2SFCCMD] = 0x0002
		v.regs[vdp2SFCODE] = sfcode
		v.regs[vdp2CCCTL] = 0x0001
		buf := make([]uint32, 352*256)
		renderTestNBG(v, 0, buf)
		return buf[0]
	}
	if px := run(0x1000, 0x0020); px == 0 || px&layerCCBit == 0 {
		t.Errorf("mode 2 bit set, code match: 0x%08X, want CC bit", px)
	}
	if px := run(0x1000, 0x0001); px == 0 || px&layerCCBit != 0 {
		t.Errorf("mode 2 bit set, no code match: 0x%08X, want no CC bit", px)
	}
	if px := run(0x0000, 0x0020); px == 0 || px&layerCCBit != 0 {
		t.Errorf("mode 2 bit clear: 0x%08X, want no CC bit", px)
	}

	// Mode 3 on RGB formats.
	v := newTestVDP2()
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0030 // 32768-color cell
	v.regs[vdp2PRINA] = 0x0001
	writeVRAM16(v, 0, 0x0000)
	writeVRAM16(v, 2, 0x0400)
	fillVRAM16(v, 0x8000, 64, 0x801F)
	v.regs[vdp2SFCCMD] = 0x0003
	v.regs[vdp2CCCTL] = 0x0001
	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	if buf[0] == 0 || buf[0]&layerCCBit == 0 {
		t.Errorf("mode 3 RGB cell: 0x%08X, want CC bit", buf[0])
	}

	v = newTestVDP2()
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0032 // 32768-color bitmap
	v.regs[vdp2PRINA] = 0x0001
	writeVRAM16(v, 0, 0x801F)
	v.regs[vdp2SFCCMD] = 0x0003
	v.regs[vdp2CCCTL] = 0x0001
	clear(buf)
	renderTestNBG(v, 0, buf)
	if buf[0] == 0 || buf[0]&layerCCBit == 0 {
		t.Errorf("mode 3 RGB bitmap: 0x%08X, want CC bit", buf[0])
	}
}

// TestNBGBitmapMosaicAndNegativeScroll verifies bitmap horizontal and
// vertical mosaic (MZCTL sizes) and negative bitmap scroll wrapping
// (11-bit signed integer part: 0x7FF is -1, reading the last column and
// row).
func TestNBGBitmapMosaicAndNegativeScroll(t *testing.T) {
	setup := func() *VDP2 {
		v := newTestVDP2()
		v.regs[vdp2BGON] = 0x0001
		v.regs[vdp2CHCTLA] = 0x0012
		v.regs[vdp2PRINA] = 0x0001
		// Rows 0-3: red shades by column; rows 4-7: green shades.
		for row := 0; row < 8; row++ {
			for x := 0; x < 8; x++ {
				dot := uint8(10 + x)
				if row >= 4 {
					dot = uint8(20 + x)
				}
				v.vram[row*512+x] = dot
			}
		}
		for i := 0; i < 8; i++ {
			writeCRAM16Test(v, uint32(10+i), uint16(i+1))
			writeCRAM16Test(v, uint32(20+i), uint16(i+1)<<5)
		}
		return v
	}
	buf := make([]uint32, 352*256)

	v := setup()
	v.regs[vdp2MZCTL] = 0x0001 | 0x0300 | 0x3000 // NBG0 mosaic 4x4
	renderTestNBG(v, 0, buf)
	expectRedShade(t, v, buf, 3, 3, 0, "mosaic 4x4: (3,3) samples dot (0,0)")
	expectRedShade(t, v, buf, 4, 3, 4, "mosaic 4x4: (4,3) samples dot (4,0)")
	expectGreenShade(t, v, buf, 3, 4, 0, "mosaic 4x4: (3,4) samples dot (0,4)")
	expectGreenShade(t, v, buf, 3, 7, 0, "mosaic 4x4: (3,7) samples dot (0,4)")

	v = setup()
	v.regs[vdp2SCXIN0] = 0x07FF // -1
	v.regs[vdp2SCYIN0] = 0x07FF // -1
	// The vertical scroll is captured at the frame boundary.
	v.EndFrame()
	v.vram[255*512+511] = 7
	writeCRAM16Test(v, 7, 0x7C00)
	clear(buf)
	renderTestNBG(v, 0, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "scroll (-1,-1) reads dot (511,255)")
}

// TestNBGMapOffsetOneWord2x2 verifies the NBG0 map offset (MPOFN bits 2:0)
// adds 64 pages to the map register page, and the rendered 1-word 2x2
// character path: with 2x2 characters and 1-word names a page is 0x800
// bytes, so map offset 1 puts plane A's page at 0x20000. The 1-word
// name's character bits 9:0 are shifted left by 2 for 2x2 characters.
func TestNBGMapOffsetOneWord2x2(t *testing.T) {
	build := func(mpofn uint16) *VDP2 {
		v := newTestVDP2()
		v.regs[vdp2BGON] = 0x0001
		v.regs[vdp2CHCTLA] = 0x0001 // 16-color, 2x2 characters
		v.regs[vdp2PNCN0] = 0x8000  // 1-word names, no supplement bits
		v.regs[vdp2MPABN0] = 0x0000
		v.regs[vdp2MPCDN0] = 0x0000
		v.regs[vdp2MPOFN] = mpofn
		v.regs[vdp2PRINA] = 0x0001
		writeRBGTestTile(v, 0x400, rbgTestRed)
		writeRBGTestTile(v, 0x401, rbgTestGreen)
		writeRBGTestTile(v, 0x402, rbgTestBlue)
		writeRBGTestTile(v, 0x403, 0x6)
		writeRBGTestPalette(v)
		v.cram[44], v.cram[45] = 0x7F, 0xFF
		// Cell (0,0) of the page at 0x20000: palette 1, character 0x400.
		writeVRAM16(v, 0x20000, 0x1100)
		return v
	}
	buf := make([]uint32, 352*256)

	v := build(0x0001)
	renderTestNBG(v, 0, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "map offset 1: top-left sub-cell")
	expectRGB(t, v, buf, 8, 0, 0, 255, 0, "map offset 1: top-right sub-cell")
	expectRGB(t, v, buf, 0, 8, 0, 0, 255, "map offset 1: bottom-left sub-cell")
	expectRGB(t, v, buf, 8, 8, 255, 255, 255, "map offset 1: bottom-right sub-cell")

	v = build(0x0000)
	clear(buf)
	renderTestNBG(v, 0, buf)
	expectTransparent(t, v, buf, 0, 0, "map offset 0: page at 0 is empty")
}
