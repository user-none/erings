package core

import "testing"

// rbgTestRed, rbgTestGreen, rbgTestBlue are the 4bpp dot values used by
// the parameter A/B fixtures below. Palette 1 maps them to CRAM entries
// 19, 20, and 21.
const (
	rbgTestRed   = 0x3
	rbgTestGreen = 0x4
	rbgTestBlue  = 0x5
)

// writeRBGTestPalette writes palette 1 entries 3, 4, 5 as pure red, green,
// and blue in RGB555.
func writeRBGTestPalette(v *VDP2) {
	v.cram[38], v.cram[39] = 0x00, 0x1F // entry 19: red
	v.cram[40], v.cram[41] = 0x03, 0xE0 // entry 20: green
	v.cram[42], v.cram[43] = 0x7C, 0x00 // entry 21: blue
}

// writeRBGTestTile fills every row of the 4bpp cell for charNum with dot.
func writeRBGTestTile(v *VDP2, charNum uint32, dot uint8) {
	addr := charNum * 0x20
	for i := uint32(0); i < 0x20; i++ {
		v.vram[addr+i] = dot<<4 | dot
	}
}

// writeRBGTestCell writes a 2-word pattern name entry (palette 1, no flip)
// for cell (cx, cy) of a 64x64-cell page at VRAM 0.
func writeRBGTestCell(v *VDP2, cx, cy int, charNum uint16) {
	addr := uint32(cy*64+cx) * 4
	writeVRAM16(v, addr, 0x0001)
	writeVRAM16(v, addr+2, charNum)
}

// writeRBGParamBIdentity writes an identity rotation (A=E=1, DYst=1,
// DX=1, kx=ky=1) to the parameter B table at 0x10080 with Xst set to
// xst whole dots.
func writeRBGParamBIdentity(v *VDP2, xst uint16) {
	paramB := uint32(0x10080)
	writeRotParam32(v, paramB, 0x00, xst, 0x0000)    // Xst
	writeRotParam32(v, paramB, 0x1C, 0x0001, 0x0000) // A = 1.0
	writeRotParam32(v, paramB, 0x2C, 0x0001, 0x0000) // E = 1.0
	writeRotParam32(v, paramB, 0x10, 0x0001, 0x0000) // DYst = 1.0
	writeRotParam32(v, paramB, 0x14, 0x0001, 0x0000) // DX = 1.0
	writeRotParam32(v, paramB, 0x4C, 0x0001, 0x0000) // kx = 1.0
	writeRotParam32(v, paramB, 0x50, 0x0001, 0x0000) // ky = 1.0
}

// setupRBG0ParamAB builds a cell-mode RBG0 scene where parameter A and
// parameter B are distinguishable by color. Both are identity rotations
// on the same page. Parameter A starts at map X 0, parameter B at map X
// 16. Map cells (0,0) and (1,0) are red, cells (2,0) through (5,0) are
// green, and cell (0,1) is blue. Under parameter A screen x in [0,16)
// is red. Under parameter B screen x in [0,32) is green. The tiles are
// fully filled so every cell row renders the same color.
func setupRBG0ParamAB(t *testing.T) *VDP2 {
	t.Helper()
	v := setupRBG0Identity(t)

	// Parameter B map registers point at page 0, plane size 1x1, wrap.
	for i := 0; i < 8; i++ {
		v.regs[vdp2MPABRB+i] = 0x0000
	}
	writeRBGParamBIdentity(v, 16)

	// Character data lives past the page 0 pattern name table (0x4000).
	writeRBGTestTile(v, 0x400, rbgTestRed)
	writeRBGTestTile(v, 0x401, rbgTestGreen)
	writeRBGTestTile(v, 0x402, rbgTestBlue)
	writeRBGTestCell(v, 0, 0, 0x400)
	writeRBGTestCell(v, 1, 0, 0x400)
	for cx := 2; cx <= 5; cx++ {
		writeRBGTestCell(v, cx, 0, 0x401)
	}
	writeRBGTestCell(v, 0, 1, 0x402)
	writeRBGTestPalette(v)
	return v
}

// setupRBG0BitmapParamAB is the bitmap-mode counterpart of
// setupRBG0ParamAB: a 256-color bitmap whose row 0 has dots 0-15 red
// (index 5) and dots 16-47 green (index 6). Parameter B starts at map X
// 16. Both parameters use map offset 0.
func setupRBG0BitmapParamAB(t *testing.T) *VDP2 {
	t.Helper()
	v := setupRBG0BitmapIdentity(t)
	for row := 0; row < 8; row++ {
		for x := 0; x < 16; x++ {
			v.vram[row*512+x] = 5
		}
		for x := 16; x < 48; x++ {
			v.vram[row*512+x] = 6
		}
	}
	v.cram[12], v.cram[13] = 0x03, 0xE0 // index 6: green
	writeRBGParamBIdentity(v, 16)
	return v
}

// expectRGB asserts the rendered layer buffer pixel at (x, y) is opaque
// with the given 8-bit color.
func expectRGB(t *testing.T, v *VDP2, buf []uint32, x, y int, r, g, b uint8, what string) {
	t.Helper()
	px := buf[y*v.frame.width+x]
	if px == 0 {
		t.Errorf("%s: pixel(%d,%d) transparent, want (%d,%d,%d)", what, x, y, r, g, b)
		return
	}
	gr, gg, gb := uint8(px>>16), uint8(px>>8), uint8(px)
	if gr != r || gg != g || gb != b {
		t.Errorf("%s: pixel(%d,%d) = (%d,%d,%d), want (%d,%d,%d)", what, x, y, gr, gg, gb, r, g, b)
	}
}

// expectTransparent asserts the rendered layer buffer pixel at (x, y)
// is transparent.
func expectTransparent(t *testing.T, v *VDP2, buf []uint32, x, y int, what string) {
	t.Helper()
	if px := buf[y*v.frame.width+x]; px != 0 {
		t.Errorf("%s: pixel(%d,%d) = 0x%08X, want transparent", what, x, y, px)
	}
}

// TestRBG0RPMD1ParamBOnlyCell verifies RPMD mode 1 renders every dot from
// rotation parameter B (VDP2 manual Sec 6.1, Table 6.4), using parameter
// B's own start coordinate and map registers, in cell mode.
func TestRBG0RPMD1ParamBOnlyCell(t *testing.T) {
	v := setupRBG0ParamAB(t)
	buf := make([]uint32, 352*256)

	v.regs[vdp2RPMD] = 0x0000
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "RPMD=0 parameter A")
	expectRGB(t, v, buf, 15, 3, 255, 0, 0, "RPMD=0 parameter A")

	v.regs[vdp2RPMD] = 0x0001
	clear(buf)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 255, 0, "RPMD=1 parameter B")
	expectRGB(t, v, buf, 15, 3, 0, 255, 0, "RPMD=1 parameter B")
	expectRGB(t, v, buf, 31, 7, 0, 255, 0, "RPMD=1 parameter B")
}

// TestRBG0RPMD1ParamBOnlyBitmap is the bitmap-mode counterpart of
// TestRBG0RPMD1ParamBOnlyCell.
func TestRBG0RPMD1ParamBOnlyBitmap(t *testing.T) {
	v := setupRBG0BitmapParamAB(t)
	buf := make([]uint32, 352*256)

	v.regs[vdp2RPMD] = 0x0000
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "RPMD=0 parameter A")
	expectRGB(t, v, buf, 15, 0, 255, 0, 0, "RPMD=0 parameter A")

	v.regs[vdp2RPMD] = 0x0001
	clear(buf)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 255, 0, "RPMD=1 parameter B")
	expectRGB(t, v, buf, 31, 0, 0, 255, 0, "RPMD=1 parameter B")
}

// TestRBG0RPMD1ParamBPlaneSize verifies that under RPMD mode 1 the map
// layout uses parameter B's plane size (PLSZ RBPLSZ, bits 13:12) and
// parameter B's map registers. Parameter B starts at map X 512 (cell
// 64). With a 1x1 plane that cell is plane B's first cell (page 0, red).
// With a 2x1 plane it is plane A's second page, where a blue cell is
// placed.
func TestRBG0RPMD1ParamBPlaneSize(t *testing.T) {
	v := setupRBG0ParamAB(t)
	v.regs[vdp2RPMD] = 0x0001
	writeRotParam32(v, 0x10080, 0x00, 512, 0x0000) // Xst_B = 512 dots
	// Page 1 (the next 0x4000 bytes) cell (0,0) is blue.
	writeVRAM16(v, 0x4000, 0x0001)
	writeVRAM16(v, 0x4002, 0x402)

	buf := make([]uint32, 352*256)
	v.regs[vdp2PLSZ] = 0x0000 // B plane 1x1
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "RPMD=1 B plane 1x1: cell 64 is plane B page 0")

	v.regs[vdp2PLSZ] = 0x1000 // B plane 2x1, A plane 1x1
	clear(buf)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "RPMD=1 B plane 2x1: cell 64 is plane A page 1")
}

// TestRBG0RPMD3WindowSelectsParamCell verifies RPMD mode 3 shows
// parameter B inside the rotation parameter window's active area and
// parameter A outside it (VDP2 manual Sec 6.1 Table 6.4 and Sec 6 p.190),
// with the W0 area bit inverting which side is active.
func TestRBG0RPMD3WindowSelectsParamCell(t *testing.T) {
	v := setupRBG0ParamAB(t)
	v.regs[vdp2RPMD] = 0x0003
	// W0 covers x 0..10 (raw units are half dots in normal resolution)
	// and y 0..3, all within cell row 0.
	v.regs[vdp2WPSX0] = 0
	v.regs[vdp2WPEX0] = 20
	v.regs[vdp2WPSY0] = 0
	v.regs[vdp2WPEY0] = 3

	// RPW0E, area = inside.
	v.regs[vdp2WCTLD] = 0x0002
	buf := make([]uint32, 352*256)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 3, 2, 0, 255, 0, "RPMD=3 inside W0 -> parameter B")
	expectRGB(t, v, buf, 12, 2, 255, 0, 0, "RPMD=3 outside W0 -> parameter A")
	expectRGB(t, v, buf, 3, 5, 255, 0, 0, "RPMD=3 below W0 -> parameter A")

	// RPW0E, area = outside.
	v.regs[vdp2WCTLD] = 0x0003
	clear(buf)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 3, 2, 255, 0, 0, "RPMD=3 inverted, inside W0 -> parameter A")
	expectRGB(t, v, buf, 12, 2, 0, 255, 0, "RPMD=3 inverted, outside W0 -> parameter B")
}

// TestRBG0RPMD3WindowSelectsParamBitmap is the bitmap-mode counterpart of
// TestRBG0RPMD3WindowSelectsParamCell.
func TestRBG0RPMD3WindowSelectsParamBitmap(t *testing.T) {
	v := setupRBG0BitmapParamAB(t)
	v.regs[vdp2RPMD] = 0x0003
	v.regs[vdp2WPSX0] = 0
	v.regs[vdp2WPEX0] = 20
	v.regs[vdp2WPSY0] = 0
	v.regs[vdp2WPEY0] = 7

	v.regs[vdp2WCTLD] = 0x0002
	buf := make([]uint32, 352*256)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 3, 0, 0, 255, 0, "RPMD=3 inside W0 -> parameter B")
	expectRGB(t, v, buf, 12, 0, 255, 0, 0, "RPMD=3 outside W0 -> parameter A")

	v.regs[vdp2WCTLD] = 0x0003
	clear(buf)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 3, 0, 255, 0, 0, "RPMD=3 inverted, inside W0 -> parameter A")
	expectRGB(t, v, buf, 12, 0, 0, 255, 0, "RPMD=3 inverted, outside W0 -> parameter B")
}

// TestRBG0RPMD2PerDotSwitchCell verifies the RPMD mode 2 switch is
// evaluated per dot from parameter A's coefficient table (VDP2 manual
// Sec 6.1, Table 6.4): dots whose A coefficient has MSB=0 render
// parameter A, dots with MSB=1 render parameter B, on the same line.
func TestRBG0RPMD2PerDotSwitchCell(t *testing.T) {
	v := setupRBG0ParamAB(t)
	paramA := uint32(0x10000)
	paramB := uint32(0x10080)

	v.regs[vdp2RPMD] = 0x0002
	// 2-word coefficient tables for A and B, mode 0.
	v.regs[vdp2KTCTL] = 0x0101
	v.regs[vdp2KTAOF] = 0x0000
	// Bank A0 designated as coefficient RAM so per-dot reads take place.
	v.regs[vdp2RAMCTL] = 0x0001

	// Parameter A: one coefficient entry per dot at 0x18000.
	writeRotParam32(v, paramA, 0x54, 0x6000, 0x0000) // KAst_A -> 0x18000
	writeRotParam32(v, paramA, 0x58, 0x0000, 0x0000) // dKAst_A = 0
	writeRotParam32(v, paramA, 0x5C, 0x0001, 0x0000) // dKAx_A = 1.0
	// Parameter B: a single line coefficient at 0x18100, MSB=0, kx=1.0.
	writeRotParam32(v, paramB, 0x54, 0x6040, 0x0000) // KAst_B -> 0x18100
	writeRotParam32(v, paramB, 0x58, 0x0000, 0x0000)
	writeVRAM16(v, 0x18100, 0x0001)
	writeVRAM16(v, 0x18102, 0x0000)

	// Dots 0-3 stay on A (MSB=0, kx=1.0), dots 4-7 switch to B (MSB=1).
	for dot := uint32(0); dot < 8; dot++ {
		hi := uint16(0x0001)
		if dot >= 4 {
			hi = 0x8001
		}
		writeVRAM16(v, 0x18000+dot*4, hi)
		writeVRAM16(v, 0x18002+dot*4, 0x0000)
	}

	buf := make([]uint32, 352*256)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "RPMD=2 dot 0 stays on A")
	expectRGB(t, v, buf, 3, 0, 255, 0, 0, "RPMD=2 dot 3 stays on A")
	expectRGB(t, v, buf, 4, 0, 0, 255, 0, "RPMD=2 dot 4 switched to B")
	expectRGB(t, v, buf, 7, 0, 0, 255, 0, "RPMD=2 dot 7 switched to B")
}

// TestRBG0RPRCTLReReadRendering verifies the per-line RPRCTL re-read arms
// (VDP2 manual Sec 6.1, Rotation Parameter Read Control Register) for
// both parameters: a table field rewritten mid-frame does not affect
// rendering until the line whose arm bit is set, and from that line the
// re-read value (Xst, Yst, or KAst) is used.
func TestRBG0RPRCTLReReadRendering(t *testing.T) {
	type field struct {
		name   string
		paramB bool
		arm    uint16
		offset uint32
		newHi  uint16
		// expected color at (0, 5) after the re-read; nil means transparent
		want []uint8
	}
	cases := []field{
		{"A Xst", false, 0x0001, 0x00, 16, []uint8{0, 255, 0}},
		{"A Yst", false, 0x0002, 0x04, 8, []uint8{0, 0, 255}},
		{"A KAst", false, 0x0004, 0x54, 0x6080, nil},
		{"B Xst", true, 0x0100, 0x00, 16, []uint8{0, 255, 0}},
		{"B Yst", true, 0x0200, 0x04, 8, []uint8{0, 0, 255}},
		{"B KAst", true, 0x0400, 0x54, 0x6080, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := setupRBG0ParamAB(t)
			base := uint32(0x10000)
			if tc.paramB {
				v.regs[vdp2RPMD] = 0x0001
				base = 0x10080
				// Parameter B starts at map X 0 like A for this test.
				writeRotParam32(v, base, 0x00, 0x0000, 0x0000)
			}
			if tc.offset == 0x54 {
				// Coefficient table for the parameter under test: the
				// initial KAst points at an MSB=0 entry (renders), the
				// rewritten KAst at an MSB=1 entry (transparent).
				if tc.paramB {
					v.regs[vdp2KTCTL] = 0x0100
				} else {
					v.regs[vdp2KTCTL] = 0x0001
				}
				writeRotParam32(v, base, 0x54, 0x6000, 0x0000) // -> 0x18000
				writeRotParam32(v, base, 0x58, 0x0000, 0x0000)
				writeVRAM16(v, 0x18000, 0x0001)
				writeVRAM16(v, 0x18002, 0x0000)
				writeVRAM16(v, 0x18200, 0x8001) // 0x6080*4
				writeVRAM16(v, 0x18202, 0x0000)
			}

			buf := make([]uint32, 352*256)
			v.BeginFrame()
			v.decodeLineState()
			if !v.frame.rbg0On {
				t.Fatal("RBG0 not enabled")
			}
			v.vLine = 5
			v.Write(uint32(vdp2RPRCTL*2), tc.arm)
			for y := 0; y < 8; y++ {
				if y == 3 {
					writeRotParam32(v, base, tc.offset, tc.newHi, 0x0000)
				}
				v.rbg0SpanSetup(buf, &v.frame.rbg0, &v.frame.rbg0F, y)(0, v.frame.width)
			}

			expectRGB(t, v, buf, 0, 4, 255, 0, 0, "line 4 before the armed line keeps the frame-start value")
			if tc.want == nil {
				expectTransparent(t, v, buf, 0, 5, "armed line re-reads")
				expectTransparent(t, v, buf, 0, 6, "line after the armed line keeps the re-read")
			} else {
				expectRGB(t, v, buf, 0, 5, tc.want[0], tc.want[1], tc.want[2], "armed line re-reads")
				expectRGB(t, v, buf, 0, 6, tc.want[0], tc.want[1], tc.want[2], "line after the armed line keeps the re-read")
			}
		})
	}
}

// renderTestRBG1 drives the production per-line path to render a full
// frame of RBG1 into buf.
func renderTestRBG1(v *VDP2, buf []uint32) {
	v.BeginFrame()
	v.decodeLineState()
	if !v.frame.rbg1On {
		clear(buf)
		return
	}
	for y := 0; y < v.frame.height; y++ {
		v.rbg1SpanSetup(buf, &v.frame.rbg1, &v.frame.rbg1F, y)(0, v.frame.width)
	}
}

// setupRBG1Scene builds the cell-mode RBG1 counterpart of
// setupRBG0ParamAB: identity rotation on parameter B starting at map X
// 0, cells (0,0) and (1,0) red, (2,0) through (5,0) green, (0,1) blue,
// on page 0 with fully filled tiles.
func setupRBG1Scene(t *testing.T) *VDP2 {
	t.Helper()
	v := setupRBG1Identity(t)
	writeRBGTestTile(v, 0x400, rbgTestRed)
	writeRBGTestTile(v, 0x401, rbgTestGreen)
	writeRBGTestTile(v, 0x402, rbgTestBlue)
	writeRBGTestCell(v, 0, 0, 0x400)
	writeRBGTestCell(v, 1, 0, 0x400)
	for cx := 2; cx <= 5; cx++ {
		writeRBGTestCell(v, cx, 0, 0x401)
	}
	writeRBGTestCell(v, 0, 1, 0x402)
	writeRBGTestPalette(v)
	return v
}

// setupRBG1BitmapScene builds a 256-color bitmap RBG1 (CHCTLA NBG0
// fields: bitmap enable, 512x256) at map offset 0 with row 0 dots 0-15
// red (index 5), dots 16-47 green (index 6), and identity rotation on
// parameter B.
func setupRBG1BitmapScene(t *testing.T) *VDP2 {
	t.Helper()
	v := setupRBG1Identity(t)
	v.regs[vdp2CHCTLA] = 0x0012 // N0BMEN, 256 colors, 512x256
	v.regs[vdp2BMPNA] = 0x0000
	for row := 0; row < 8; row++ {
		for x := 0; x < 16; x++ {
			v.vram[row*512+x] = 5
		}
		for x := 16; x < 48; x++ {
			v.vram[row*512+x] = 6
		}
	}
	v.cram[10], v.cram[11] = 0x00, 0x1F // index 5: red
	v.cram[12], v.cram[13] = 0x03, 0xE0 // index 6: green
	v.cram[14], v.cram[15] = 0x7C, 0x00 // index 7: blue
	return v
}

// writeRBGTestOverTile writes an asymmetric 4bpp tile for the screen-over
// pattern tests: rows 0-3 are blue in dots 0-3 and green in dots 4-7,
// rows 4-7 are red.
func writeRBGTestOverTile(v *VDP2, charNum uint32) {
	addr := charNum * 0x20
	for row := uint32(0); row < 8; row++ {
		for pair := uint32(0); pair < 4; pair++ {
			var dot uint8 = rbgTestRed
			if row < 4 {
				dot = rbgTestBlue
				if pair >= 2 {
					dot = rbgTestGreen
				}
			}
			v.vram[addr+row*4+pair] = dot<<4 | dot
		}
	}
}

// rbgXstNeg8 is -8 whole dots as the high word of a sign+12.10 FP
// rotation parameter field (13-bit two's complement integer part).
const rbgXstNeg8 = 0x1FF8

// rbgScreenOverCase describes one rotation surface for the screen-over
// tests: which fixture, which PLSZ over-bits, and which OVPN register
// and pattern name control register apply.
type rbgScreenOverCase struct {
	name    string
	setup   func(t *testing.T) *VDP2
	render  func(v *VDP2, buf []uint32)
	plszSh  uint   // PLSZ shift of the RxOVR bits
	ovpn    int    // register index of the OVPN register
	pnc     int    // register index of the pattern name control register
	xstBase uint32 // parameter table whose Xst is moved off-plane
	pri     int    // priority register index
}

func rbgScreenOverCases() []rbgScreenOverCase {
	return []rbgScreenOverCase{
		{
			name: "RBG0 parameter A",
			setup: func(t *testing.T) *VDP2 {
				return setupRBG0ParamAB(t)
			},
			render: renderTestRBG0, plszSh: 10, ovpn: vdp2OVPNRA, pnc: vdp2PNCR,
			xstBase: 0x10000, pri: vdp2PRIR,
		},
		{
			name: "RBG0 parameter B",
			setup: func(t *testing.T) *VDP2 {
				v := setupRBG0ParamAB(t)
				v.regs[vdp2RPMD] = 0x0001
				return v
			},
			render: renderTestRBG0, plszSh: 14, ovpn: vdp2OVPNRB, pnc: vdp2PNCR,
			xstBase: 0x10080, pri: vdp2PRIR,
		},
		{
			name:   "RBG1",
			setup:  setupRBG1Scene,
			render: renderTestRBG1, plszSh: 14, ovpn: vdp2OVPNRB, pnc: vdp2PNCN0,
			xstBase: 0x10080, pri: vdp2PRINA,
		},
	}
}

// TestRBGScreenOverPatternCell verifies screen-over mode 1 (VDP2 manual
// Sec 4.10 Screen-Over Process, Plane Size Register RxOVR=01): outside
// the plane the character named by OVPNRA/OVPNRB repeats in screen
// space, decoded as a 1-word pattern name with the supplement bits from
// the pattern name control register, including its flip bits.
func TestRBGScreenOverPatternCell(t *testing.T) {
	for _, tc := range rbgScreenOverCases() {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.setup(t)
			v.regs[vdp2PLSZ] = 1 << tc.plszSh
			writeRotParam32(v, tc.xstBase, 0x00, rbgXstNeg8, 0x0000)
			writeRBGTestOverTile(v, 0x403)
			// 1-word 16-color aux 0: palette bits 15:12, vflip 11, hflip
			// 10, character bits 9:0 supplemented by PNC bits 4:0 << 10.
			v.regs[tc.pnc] = 0x0001
			v.regs[tc.ovpn] = 0x1003

			buf := make([]uint32, 352*256)
			tc.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 0, 0, 255, "over pattern dot (0,0)")
			expectRGB(t, v, buf, 5, 0, 0, 255, 0, "over pattern dot (5,0)")
			expectRGB(t, v, buf, 0, 5, 255, 0, 0, "over pattern dot (0,5)")
			expectRGB(t, v, buf, 8, 0, 255, 0, 0, "in-plane cell 0")
			expectRGB(t, v, buf, 8, 5, 255, 0, 0, "in-plane cell 0 row 5")

			v.regs[tc.ovpn] = 0x1003 | 0x0400 // hflip
			clear(buf)
			tc.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 0, 255, 0, "over pattern hflip dot (0,0)")
			expectRGB(t, v, buf, 5, 0, 0, 0, 255, "over pattern hflip dot (5,0)")

			v.regs[tc.ovpn] = 0x1003 | 0x0800 // vflip
			clear(buf)
			tc.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "over pattern vflip dot (0,0)")
			expectRGB(t, v, buf, 0, 5, 0, 0, 255, "over pattern vflip dot (0,5)")
		})
	}
}

// TestRBGScreenOverPatternSpecialBits verifies the screen-over character
// takes its special priority and special color calculation bits from the
// pattern name control register (bits 9 and 8), the same source a 1-word
// pattern name uses, under SFPRMD/SFCCMD per-character mode 1.
func TestRBGScreenOverPatternSpecialBits(t *testing.T) {
	for _, tc := range rbgScreenOverCases() {
		t.Run(tc.name, func(t *testing.T) {
			run := func(pncBits uint16) uint32 {
				v := tc.setup(t)
				v.regs[vdp2PLSZ] = 1 << tc.plszSh
				writeRotParam32(v, tc.xstBase, 0x00, rbgXstNeg8, 0x0000)
				writeRBGTestOverTile(v, 0x403)
				v.regs[tc.pnc] = 0x0001 | pncBits
				v.regs[tc.ovpn] = 0x1003
				// Priority 2 so the special priority bit is visible as
				// bit 0 of the effective priority.
				v.regs[tc.pri] = (v.regs[tc.pri] &^ 0x07) | 0x0002
				// Per-character special priority and CC for RBG0 (bits
				// 9:8) and NBG0/RBG1 (bits 1:0).
				v.regs[vdp2SFPRMD] = 0x0101
				v.regs[vdp2SFCCMD] = 0x0101
				// Screen CC enable for RBG0 (bit 4) and NBG0/RBG1 (bit 0).
				v.regs[vdp2CCCTL] = 0x0011
				buf := make([]uint32, 352*256)
				tc.render(v, buf)
				if buf[0] == 0 {
					t.Fatalf("over pattern dot (0,0) transparent with PNC bits 0x%04X", pncBits)
				}
				return buf[0]
			}

			px := run(0)
			if pri := (px >> 24) & 0x07; pri != 2 {
				t.Errorf("special bits clear: priority %d, want 2", pri)
			}
			if px&layerCCBit != 0 {
				t.Error("special CC bit clear: layer CC bit set")
			}

			px = run(0x0300)
			if pri := (px >> 24) & 0x07; pri != 3 {
				t.Errorf("special priority bit set: priority %d, want 3", pri)
			}
			if px&layerCCBit == 0 {
				t.Error("special CC bit set: layer CC bit clear")
			}
		})
	}
}

// TestRBGScreenOverTransparentAndForce512 verifies screen-over modes 2
// (transparent) and 3 (0-511 display area) leave a dot at a negative map
// coordinate transparent while in-plane dots render, for RBG0 on both
// parameters and for RBG1.
func TestRBGScreenOverTransparentAndForce512(t *testing.T) {
	for _, tc := range rbgScreenOverCases() {
		for _, mode := range []uint16{2, 3} {
			t.Run(tc.name+" mode "+string(rune('0'+mode)), func(t *testing.T) {
				v := tc.setup(t)
				v.regs[vdp2PLSZ] = mode << tc.plszSh
				writeRotParam32(v, tc.xstBase, 0x00, rbgXstNeg8, 0x0000)
				buf := make([]uint32, 352*256)
				tc.render(v, buf)
				expectTransparent(t, v, buf, 0, 0, "over mode "+string(rune('0'+mode))+" negative map X")
				expectTransparent(t, v, buf, 7, 3, "over mode "+string(rune('0'+mode))+" negative map X")
				expectRGB(t, v, buf, 8, 0, 255, 0, 0, "over mode "+string(rune('0'+mode))+" in-plane")
			})
		}
	}
}

// TestRBG0BitmapScreenOverNonWrap verifies that in bitmap mode every
// screen-over setting other than wrap leaves an out-of-bitmap dot
// transparent (the manual restricts the over-pattern mode to cell
// format), for both rotation parameters.
func TestRBG0BitmapScreenOverNonWrap(t *testing.T) {
	for _, mode := range []uint16{1, 2, 3} {
		v := setupRBG0BitmapParamAB(t)
		v.regs[vdp2PLSZ] = mode << 10
		writeRotParam32(v, 0x10000, 0x00, rbgXstNeg8, 0x0000)
		buf := make([]uint32, 352*256)
		renderTestRBG0(v, buf)
		expectTransparent(t, v, buf, 0, 0, "parameter A bitmap over mode "+string(rune('0'+mode)))
		expectRGB(t, v, buf, 8, 0, 255, 0, 0, "parameter A bitmap over mode "+string(rune('0'+mode))+" in-bitmap")

		v = setupRBG0BitmapParamAB(t)
		v.regs[vdp2RPMD] = 0x0001
		v.regs[vdp2PLSZ] = mode << 14
		writeRotParam32(v, 0x10080, 0x00, rbgXstNeg8, 0x0000)
		clear(buf)
		renderTestRBG0(v, buf)
		expectTransparent(t, v, buf, 0, 0, "parameter B bitmap over mode "+string(rune('0'+mode)))
		expectRGB(t, v, buf, 8, 0, 255, 0, 0, "parameter B bitmap over mode "+string(rune('0'+mode))+" in-bitmap")
	}
}

// TestRBG1BitmapScreenOver verifies RBG1 bitmap screen-over: wrap follows
// the configured bitmap width (512 or 1024), and the non-wrap modes leave
// the out-of-bitmap dot transparent.
func TestRBG1BitmapScreenOver(t *testing.T) {
	buf := make([]uint32, 352*256)

	// Wrap at 512: map X -8 reads bitmap dot 504.
	v := setupRBG1BitmapScene(t)
	writeRotParam32(v, 0x10080, 0x00, rbgXstNeg8, 0x0000)
	v.vram[504] = 7
	v.vram[1016] = 5
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "512-wide wrap reads dot 504")
	expectRGB(t, v, buf, 8, 0, 255, 0, 0, "in-bitmap dot 0")

	// Wrap at 1024: map X -8 reads bitmap dot 1016.
	v = setupRBG1BitmapScene(t)
	v.regs[vdp2CHCTLA] = 0x001A // 1024x256
	writeRotParam32(v, 0x10080, 0x00, rbgXstNeg8, 0x0000)
	v.vram[504] = 5
	v.vram[1016] = 7
	clear(buf)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "1024-wide wrap reads dot 1016")

	for _, mode := range []uint16{1, 2, 3} {
		v = setupRBG1BitmapScene(t)
		v.regs[vdp2PLSZ] = mode << 14
		writeRotParam32(v, 0x10080, 0x00, rbgXstNeg8, 0x0000)
		clear(buf)
		renderTestRBG1(v, buf)
		expectTransparent(t, v, buf, 0, 0, "RBG1 bitmap over mode "+string(rune('0'+mode)))
		expectRGB(t, v, buf, 8, 0, 255, 0, 0, "RBG1 bitmap over mode "+string(rune('0'+mode))+" in-bitmap")
	}
}

// fillVRAM16 writes count consecutive big-endian words starting at addr.
func fillVRAM16(v *VDP2, addr uint32, count int, val uint16) {
	for i := 0; i < count; i++ {
		writeVRAM16(v, addr+uint32(i)*2, val)
	}
}

// TestRBG0CellColorModes verifies the RBG0 cell path in the 256-color,
// 2048-color, and 32768-color formats (CHCTLB R0CHCN): an opaque dot
// renders its color, the format's transparent dot (index 0, or MSB 0
// for RGB) is transparent, and R0TPON makes the transparent dot opaque
// (CRAM entry 0 for palette formats, its own RGB for the RGB format).
func TestRBG0CellColorModes(t *testing.T) {
	type mode struct {
		name   string
		chcn   uint16
		fill   func(v *VDP2)
		tpOnRG [3]uint8 // color of the transparent dot under R0TPON
	}
	modes := []mode{
		{"256-color", 1, func(v *VDP2) {
			for i := uint32(0); i < 64; i++ {
				v.vram[0x8000+i] = 5
				v.vram[0x8080+i] = 0
			}
		}, [3]uint8{0, 255, 0}},
		{"2048-color", 2, func(v *VDP2) {
			fillVRAM16(v, 0x8000, 64, 0x0005)
			fillVRAM16(v, 0x8080, 64, 0x0000)
		}, [3]uint8{0, 255, 0}},
		{"32768-color", 3, func(v *VDP2) {
			fillVRAM16(v, 0x8000, 64, 0x801F)
			fillVRAM16(v, 0x8080, 64, 0x001F)
		}, [3]uint8{255, 0, 0}},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			v := setupRBG0Identity(t)
			v.regs[vdp2CHCTLB] = m.chcn << 12
			// Cell (0,0) -> character 0x400 (0x8000), cell (1,0) ->
			// character 0x404 (0x8080); palette 0.
			writeVRAM16(v, 0, 0x0000)
			writeVRAM16(v, 2, 0x0400)
			writeVRAM16(v, 4, 0x0000)
			writeVRAM16(v, 6, 0x0404)
			m.fill(v)
			v.cram[0], v.cram[1] = 0x03, 0xE0   // entry 0: green
			v.cram[10], v.cram[11] = 0x00, 0x1F // entry 5: red

			buf := make([]uint32, 352*256)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, m.name+" opaque dot")
			expectRGB(t, v, buf, 7, 7, 255, 0, 0, m.name+" opaque dot")
			expectTransparent(t, v, buf, 8, 0, m.name+" transparent dot")

			v.regs[vdp2BGON] |= 1 << 12 // R0TPON
			clear(buf)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 8, 0, m.tpOnRG[0], m.tpOnRG[1], m.tpOnRG[2], m.name+" transparent dot with R0TPON")
		})
	}
}

// TestRBG0CellFlips1x1 verifies the 2-word pattern name flip bits (MSW
// bit 15 vertical, bit 14 horizontal) on a 1x1 RBG0 character.
func TestRBG0CellFlips1x1(t *testing.T) {
	cases := []struct {
		name string
		msw  uint16
		// colors at (0,0), (5,0), (0,5)
		c00, c50, c05 [3]uint8
	}{
		{"none", 0x0001, [3]uint8{0, 0, 255}, [3]uint8{0, 255, 0}, [3]uint8{255, 0, 0}},
		{"hflip", 0x4001, [3]uint8{0, 255, 0}, [3]uint8{0, 0, 255}, [3]uint8{255, 0, 0}},
		{"vflip", 0x8001, [3]uint8{255, 0, 0}, [3]uint8{255, 0, 0}, [3]uint8{0, 0, 255}},
		{"both", 0xC001, [3]uint8{255, 0, 0}, [3]uint8{255, 0, 0}, [3]uint8{0, 255, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := setupRBG0Identity(t)
			writeRBGTestOverTile(v, 0x403)
			writeRBGTestPalette(v)
			writeVRAM16(v, 0, tc.msw)
			writeVRAM16(v, 2, 0x0403)
			buf := make([]uint32, 352*256)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, tc.c00[0], tc.c00[1], tc.c00[2], tc.name)
			expectRGB(t, v, buf, 5, 0, tc.c50[0], tc.c50[1], tc.c50[2], tc.name)
			expectRGB(t, v, buf, 0, 5, tc.c05[0], tc.c05[1], tc.c05[2], tc.name)
		})
	}
}

// TestRBG0Cell2x2Flips verifies 2x2 character size (CHCTLB R0CHSZ): the
// four 8x8 sub-cells follow the character number in order top-left,
// top-right, bottom-left, bottom-right, and the flip bits mirror the
// whole 16x16 character.
func TestRBG0Cell2x2Flips(t *testing.T) {
	red := [3]uint8{255, 0, 0}
	green := [3]uint8{0, 255, 0}
	blue := [3]uint8{0, 0, 255}
	white := [3]uint8{255, 255, 255}
	cases := []struct {
		name string
		msw  uint16
		// colors at (0,0), (8,0), (0,8), (8,8)
		tl, tr, bl, br [3]uint8
	}{
		{"none", 0x0001, red, green, blue, white},
		{"hflip", 0x4001, green, red, white, blue},
		{"vflip", 0x8001, blue, white, red, green},
		{"both", 0xC001, white, blue, green, red},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := setupRBG0Identity(t)
			v.regs[vdp2CHCTLB] = 0x0100 // 2x2 characters
			writeRBGTestTile(v, 0x400, rbgTestRed)
			writeRBGTestTile(v, 0x401, rbgTestGreen)
			writeRBGTestTile(v, 0x402, rbgTestBlue)
			writeRBGTestTile(v, 0x403, 0x6)
			writeRBGTestPalette(v)
			v.cram[44], v.cram[45] = 0x7F, 0xFF // entry 22: white
			// 32x32-cell page: entry (0,0) at 0.
			writeVRAM16(v, 0, tc.msw)
			writeVRAM16(v, 2, 0x0400)
			buf := make([]uint32, 352*256)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, tc.tl[0], tc.tl[1], tc.tl[2], tc.name+" top-left")
			expectRGB(t, v, buf, 8, 0, tc.tr[0], tc.tr[1], tc.tr[2], tc.name+" top-right")
			expectRGB(t, v, buf, 0, 8, tc.bl[0], tc.bl[1], tc.bl[2], tc.name+" bottom-left")
			expectRGB(t, v, buf, 8, 8, tc.br[0], tc.br[1], tc.br[2], tc.name+" bottom-right")
		})
	}
}

// TestRBG0BitmapColorModes verifies the RBG0 bitmap path in the 16-color,
// 2048-color, and 32768-color formats: dot 0 opaque, dot 1 the format's
// transparent value, and R0TPON making dot 1 opaque.
func TestRBG0BitmapColorModes(t *testing.T) {
	type mode struct {
		name   string
		chcn   uint16
		fill   func(v *VDP2)
		tpOnRG [3]uint8
	}
	modes := []mode{
		{"16-color", 0, func(v *VDP2) {
			v.vram[0] = 0x50
		}, [3]uint8{0, 255, 0}},
		{"2048-color", 2, func(v *VDP2) {
			writeVRAM16(v, 0, 0x0005)
			writeVRAM16(v, 2, 0x0000)
		}, [3]uint8{0, 255, 0}},
		{"32768-color", 3, func(v *VDP2) {
			writeVRAM16(v, 0, 0x801F)
			writeVRAM16(v, 2, 0x001F)
		}, [3]uint8{255, 0, 0}},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			v := setupRBG0BitmapIdentity(t)
			v.regs[vdp2CHCTLB] = 0x0200 | m.chcn<<12
			m.fill(v)
			v.cram[0], v.cram[1] = 0x03, 0xE0 // entry 0: green

			buf := make([]uint32, 352*256)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, m.name+" opaque dot")
			expectTransparent(t, v, buf, 1, 0, m.name+" transparent dot")

			v.regs[vdp2BGON] |= 1 << 12
			clear(buf)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 1, 0, m.tpOnRG[0], m.tpOnRG[1], m.tpOnRG[2], m.name+" transparent dot with R0TPON")
		})
	}
}

// TestRBG0BitmapHeight512Wrap verifies the RBG0 bitmap size bit (CHCTLB
// R0BMSZ) selects a 512-row bitmap for the wrap: map Y -8 reads row 248
// of a 256-row bitmap and row 504 of a 512-row bitmap.
func TestRBG0BitmapHeight512Wrap(t *testing.T) {
	v := setupRBG0BitmapIdentity(t)
	writeRotParam32(v, 0x10000, 0x04, rbgXstNeg8, 0x0000) // Yst_A = -8
	v.vram[248*512] = 6
	v.vram[504*512] = 7
	v.cram[12], v.cram[13] = 0x03, 0xE0 // index 6: green
	v.cram[14], v.cram[15] = 0x7C, 0x00 // index 7: blue

	buf := make([]uint32, 352*256)
	v.regs[vdp2CHCTLB] = 0x1200 // 512x256
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 255, 0, "256-row bitmap wraps to row 248")

	v.regs[vdp2CHCTLB] = 0x1600 // 512x512
	clear(buf)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "512-row bitmap wraps to row 504")
}

// TestRBG1BitmapIdentity verifies RBG1 in bitmap format renders through
// rotation parameter B from the bitmap at the parameter B map offset,
// with the priority from PRINA.
func TestRBG1BitmapIdentity(t *testing.T) {
	v := setupRBG1BitmapScene(t)
	buf := make([]uint32, 352*256)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "bitmap dot 0")
	expectRGB(t, v, buf, 16, 0, 0, 255, 0, "bitmap dot 16")
	expectTransparent(t, v, buf, 48, 0, "bitmap dot 48 (index 0)")
	if pri := (buf[0] >> 24) & 0x07; pri != 3 {
		t.Errorf("priority %d, want 3 (PRINA)", pri)
	}

	// Parameter B start at map X 16 renders the green run at x 0.
	writeRBGParamBIdentity(v, 16)
	clear(buf)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 255, 0, "Xst_B = 16")
}

// TestRBG1BitmapSizes verifies the four RBG1 bitmap sizes taken from
// CHCTLA N0BMSZ (512x256, 512x512, 1024x256, 1024x512) by sampling a
// dot whose address depends on the row stride and wrap height.
func TestRBG1BitmapSizes(t *testing.T) {
	cases := []struct {
		name   string
		chctla uint16
		w, h   int
	}{
		{"512x256", 0x0012, 512, 256},
		{"512x512", 0x0016, 512, 512},
		{"1024x256", 0x001A, 1024, 256},
		{"1024x512", 0x001E, 1024, 512},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := setupRBG1BitmapScene(t)
			v.regs[vdp2CHCTLA] = tc.chctla
			// Blue dot at the last row and column of the bitmap; the
			// other candidate positions hold index 0.
			v.vram[(tc.h-1)*tc.w+(tc.w-1)] = 7
			// Start parameter B at map (-1, -1) so wrap selects the last
			// row and column for this size.
			writeRotParam32(v, 0x10080, 0x00, 0x1FFF, 0x0000)
			writeRotParam32(v, 0x10080, 0x04, 0x1FFF, 0x0000)
			buf := make([]uint32, 352*256)
			renderTestRBG1(v, buf)
			expectRGB(t, v, buf, 0, 0, 0, 0, 255, tc.name+" last dot")
			expectRGB(t, v, buf, 1, 1, 255, 0, 0, tc.name+" dot (0,0)")
		})
	}
}

// TestRBG1BitmapBMPNA verifies the RBG1 bitmap palette number and special
// bits come from BMPNA (NBG0's bitmap palette register): bits 2:0 select
// the 256-entry palette bank, bit 5 is the special priority bit, bit 4
// the special color calculation bit.
func TestRBG1BitmapBMPNA(t *testing.T) {
	v := setupRBG1BitmapScene(t)
	v.regs[vdp2BMPNA] = 0x0001                          // palette bank 1
	v.cram[(256+5)*2], v.cram[(256+5)*2+1] = 0x7C, 0x00 // entry 261: blue
	buf := make([]uint32, 352*256)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "palette bank 1")

	run := func(bmpna uint16) uint32 {
		v := setupRBG1BitmapScene(t)
		v.regs[vdp2BMPNA] = bmpna
		v.regs[vdp2PRINA] = 0x0002
		v.regs[vdp2SFPRMD] = 0x0001 // NBG0/RBG1 per-character special priority
		v.regs[vdp2SFCCMD] = 0x0001 // NBG0/RBG1 per-character special CC
		v.regs[vdp2CCCTL] = 0x0001  // NBG0/RBG1 CC enable
		clear(buf)
		renderTestRBG1(v, buf)
		if buf[0] == 0 {
			t.Fatalf("BMPNA 0x%04X: dot (0,0) transparent", bmpna)
		}
		return buf[0]
	}
	px := run(0x0000)
	if pri := (px >> 24) & 0x07; pri != 2 {
		t.Errorf("special bits clear: priority %d, want 2", pri)
	}
	if px&layerCCBit != 0 {
		t.Error("special CC clear: layer CC bit set")
	}
	px = run(0x0030)
	if pri := (px >> 24) & 0x07; pri != 3 {
		t.Errorf("special priority set: priority %d, want 3", pri)
	}
	if px&layerCCBit == 0 {
		t.Error("special CC set: layer CC bit clear")
	}
}

// TestRBG1BitmapColorModes verifies the RBG1 bitmap path in the
// 16-color, 2048-color, and 32768-color formats with R1TPON (BGON bit
// 13) controlling the transparent dot.
func TestRBG1BitmapColorModes(t *testing.T) {
	type mode struct {
		name   string
		chcn   uint16
		fill   func(v *VDP2)
		tpOnRG [3]uint8
	}
	modes := []mode{
		{"16-color", 0, func(v *VDP2) {
			v.vram[0] = 0x50
		}, [3]uint8{0, 255, 0}},
		{"2048-color", 2, func(v *VDP2) {
			writeVRAM16(v, 0, 0x0005)
			writeVRAM16(v, 2, 0x0000)
		}, [3]uint8{0, 255, 0}},
		{"32768-color", 3, func(v *VDP2) {
			writeVRAM16(v, 0, 0x801F)
			writeVRAM16(v, 2, 0x001F)
		}, [3]uint8{255, 0, 0}},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			v := setupRBG1BitmapScene(t)
			v.regs[vdp2CHCTLA] = 0x0002 | m.chcn<<4
			m.fill(v)
			v.cram[0], v.cram[1] = 0x03, 0xE0 // entry 0: green

			buf := make([]uint32, 352*256)
			renderTestRBG1(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, m.name+" opaque dot")
			expectTransparent(t, v, buf, 1, 0, m.name+" transparent dot")

			v.regs[vdp2BGON] |= 1 << 13 // R1TPON
			clear(buf)
			renderTestRBG1(v, buf)
			expectRGB(t, v, buf, 1, 0, m.tpOnRG[0], m.tpOnRG[1], m.tpOnRG[2], m.name+" transparent dot with R1TPON")
		})
	}
}

// TestRBG1CellColorModes verifies the RBG1 cell path in the 256-color,
// 2048-color, and 32768-color formats (CHCTLA N0CHCN) with R1TPON.
func TestRBG1CellColorModes(t *testing.T) {
	type mode struct {
		name   string
		chcn   uint16
		fill   func(v *VDP2)
		tpOnRG [3]uint8
	}
	modes := []mode{
		{"256-color", 1, func(v *VDP2) {
			for i := uint32(0); i < 64; i++ {
				v.vram[0x8000+i] = 5
				v.vram[0x8080+i] = 0
			}
		}, [3]uint8{0, 255, 0}},
		{"2048-color", 2, func(v *VDP2) {
			fillVRAM16(v, 0x8000, 64, 0x0005)
			fillVRAM16(v, 0x8080, 64, 0x0000)
		}, [3]uint8{0, 255, 0}},
		{"32768-color", 3, func(v *VDP2) {
			fillVRAM16(v, 0x8000, 64, 0x801F)
			fillVRAM16(v, 0x8080, 64, 0x001F)
		}, [3]uint8{255, 0, 0}},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			v := setupRBG1Identity(t)
			v.regs[vdp2CHCTLA] = m.chcn << 4
			writeVRAM16(v, 0, 0x0000)
			writeVRAM16(v, 2, 0x0400)
			writeVRAM16(v, 4, 0x0000)
			writeVRAM16(v, 6, 0x0404)
			m.fill(v)
			v.cram[0], v.cram[1] = 0x03, 0xE0   // entry 0: green
			v.cram[10], v.cram[11] = 0x00, 0x1F // entry 5: red

			buf := make([]uint32, 352*256)
			renderTestRBG1(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, m.name+" opaque dot")
			expectTransparent(t, v, buf, 8, 0, m.name+" transparent dot")

			v.regs[vdp2BGON] |= 1 << 13
			clear(buf)
			renderTestRBG1(v, buf)
			expectRGB(t, v, buf, 8, 0, m.tpOnRG[0], m.tpOnRG[1], m.tpOnRG[2], m.name+" transparent dot with R1TPON")
		})
	}
}

// TestRBG1CellFlips verifies the 2-word pattern name flip bits on 1x1
// and 2x2 RBG1 characters (CHCTLA N0CHSZ).
func TestRBG1CellFlips(t *testing.T) {
	t.Run("1x1", func(t *testing.T) {
		cases := []struct {
			name          string
			msw           uint16
			c00, c50, c05 [3]uint8
		}{
			{"none", 0x0001, [3]uint8{0, 0, 255}, [3]uint8{0, 255, 0}, [3]uint8{255, 0, 0}},
			{"hflip", 0x4001, [3]uint8{0, 255, 0}, [3]uint8{0, 0, 255}, [3]uint8{255, 0, 0}},
			{"vflip", 0x8001, [3]uint8{255, 0, 0}, [3]uint8{255, 0, 0}, [3]uint8{0, 0, 255}},
			{"both", 0xC001, [3]uint8{255, 0, 0}, [3]uint8{255, 0, 0}, [3]uint8{0, 255, 0}},
		}
		for _, tc := range cases {
			v := setupRBG1Identity(t)
			writeRBGTestOverTile(v, 0x403)
			writeRBGTestPalette(v)
			writeVRAM16(v, 0, tc.msw)
			writeVRAM16(v, 2, 0x0403)
			buf := make([]uint32, 352*256)
			renderTestRBG1(v, buf)
			expectRGB(t, v, buf, 0, 0, tc.c00[0], tc.c00[1], tc.c00[2], tc.name)
			expectRGB(t, v, buf, 5, 0, tc.c50[0], tc.c50[1], tc.c50[2], tc.name)
			expectRGB(t, v, buf, 0, 5, tc.c05[0], tc.c05[1], tc.c05[2], tc.name)
		}
	})
	t.Run("2x2", func(t *testing.T) {
		red := [3]uint8{255, 0, 0}
		green := [3]uint8{0, 255, 0}
		blue := [3]uint8{0, 0, 255}
		white := [3]uint8{255, 255, 255}
		cases := []struct {
			name           string
			msw            uint16
			tl, tr, bl, br [3]uint8
		}{
			{"none", 0x0001, red, green, blue, white},
			{"hflip", 0x4001, green, red, white, blue},
			{"vflip", 0x8001, blue, white, red, green},
			{"both", 0xC001, white, blue, green, red},
		}
		for _, tc := range cases {
			v := setupRBG1Identity(t)
			v.regs[vdp2CHCTLA] = 0x0001 // 2x2 characters
			writeRBGTestTile(v, 0x400, rbgTestRed)
			writeRBGTestTile(v, 0x401, rbgTestGreen)
			writeRBGTestTile(v, 0x402, rbgTestBlue)
			writeRBGTestTile(v, 0x403, 0x6)
			writeRBGTestPalette(v)
			v.cram[44], v.cram[45] = 0x7F, 0xFF // entry 22: white
			writeVRAM16(v, 0, tc.msw)
			writeVRAM16(v, 2, 0x0400)
			buf := make([]uint32, 352*256)
			renderTestRBG1(v, buf)
			expectRGB(t, v, buf, 0, 0, tc.tl[0], tc.tl[1], tc.tl[2], tc.name+" top-left")
			expectRGB(t, v, buf, 8, 0, tc.tr[0], tc.tr[1], tc.tr[2], tc.name+" top-right")
			expectRGB(t, v, buf, 0, 8, tc.bl[0], tc.bl[1], tc.bl[2], tc.name+" bottom-left")
			expectRGB(t, v, buf, 8, 8, tc.br[0], tc.br[1], tc.br[2], tc.name+" bottom-right")
		}
	})
}

// TestRBG1PlaneSize verifies the RBG1 plane size (PLSZ RBPLSZ, bits
// 13:12) through page addressing: cell 64 horizontally is plane B's
// page 0 with a 1x1 plane and plane A's page 1 with a 2x1 plane; cell
// 64 vertically is plane E's page 0 with a 1x1 plane and plane A's
// page 2 with a 2x2 plane.
func TestRBG1PlaneSize(t *testing.T) {
	buf := make([]uint32, 352*256)

	// Horizontal: Xst_B = 512.
	v := setupRBG1Scene(t)
	writeRotParam32(v, 0x10080, 0x00, 512, 0x0000)
	writeVRAM16(v, 0x4000, 0x0001)
	writeVRAM16(v, 0x4002, 0x0402) // page 1 cell (0,0): blue
	v.regs[vdp2PLSZ] = 0x0000
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "1x1: cell 64 is plane B page 0")
	v.regs[vdp2PLSZ] = 0x1000
	clear(buf)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "2x1: cell 64 is plane A page 1")

	// Vertical: Yst_B = 512. Page 2 lives at 0x8000, so this scene keeps
	// its character data at 0x18000 instead.
	v = setupRBG1Identity(t)
	writeRBGTestTile(v, 0xC00, rbgTestRed)
	writeRBGTestTile(v, 0xC02, rbgTestBlue)
	writeRBGTestPalette(v)
	writeRBGTestCell(v, 0, 0, 0xC00)
	writeVRAM16(v, 0x8000, 0x0001)
	writeVRAM16(v, 0x8002, 0x0C02) // page 2 cell (0,0): blue
	writeRotParam32(v, 0x10080, 0x04, 512, 0x0000)
	v.regs[vdp2PLSZ] = 0x0000
	clear(buf)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "1x1: cell (0,64) is plane E page 0")
	v.regs[vdp2PLSZ] = 0x3000
	clear(buf)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "2x2: cell (0,64) is plane A page 2")
}

// TestRBG1Coefficient verifies RBG1 reads parameter B's coefficient
// table (KTCTL bit 8): the MSB is a transparency bit, and the value
// replaces kx and ky (mode 0), kx only (mode 1), or ky only (mode 2) per
// KTCTL bits 11:10. With kx doubled screen x 8 maps to map X 16 (green);
// with ky doubled screen y 4 maps to map Y 8 (blue).
func TestRBG1Coefficient(t *testing.T) {
	setup := func(ktctl uint16, w0 uint16) *VDP2 {
		v := setupRBG1Scene(t)
		v.regs[vdp2KTCTL] = ktctl
		v.regs[vdp2KTAOF] = 0x0000
		writeRotParam32(v, 0x10080, 0x54, 0x6000, 0x0000) // KAst_B -> 0x18000
		writeRotParam32(v, 0x10080, 0x58, 0x0000, 0x0000) // dKAst_B = 0
		writeVRAM16(v, 0x18000, w0)
		writeVRAM16(v, 0x18002, 0x0000)
		return v
	}
	buf := make([]uint32, 352*256)

	v := setup(0x0100, 0x8001) // MSB set: transparent
	renderTestRBG1(v, buf)
	expectTransparent(t, v, buf, 0, 0, "coefficient MSB=1")
	expectTransparent(t, v, buf, 8, 4, "coefficient MSB=1")

	cases := []struct {
		name     string
		ktctl    uint16
		c80, c04 [3]uint8
	}{
		{"mode 0 kx and ky", 0x0100, [3]uint8{0, 255, 0}, [3]uint8{0, 0, 255}},
		{"mode 1 kx", 0x0500, [3]uint8{0, 255, 0}, [3]uint8{255, 0, 0}},
		{"mode 2 ky", 0x0900, [3]uint8{255, 0, 0}, [3]uint8{0, 0, 255}},
	}
	for _, tc := range cases {
		v := setup(tc.ktctl, 0x0002) // value 2.0
		clear(buf)
		renderTestRBG1(v, buf)
		expectRGB(t, v, buf, 0, 0, 255, 0, 0, tc.name+" origin")
		expectRGB(t, v, buf, 8, 0, tc.c80[0], tc.c80[1], tc.c80[2], tc.name+" (8,0)")
		expectRGB(t, v, buf, 0, 4, tc.c04[0], tc.c04[1], tc.c04[2], tc.name+" (0,4)")
	}

	// KLCE (KTCTL bit 12) stores the coefficient's line color bits
	// (word 0 bits 14:8) with bit 7 set into the RBG1 line color buffer.
	v = setup(0x1100, 0x2501)
	clear(buf)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "KLCE dot renders")
	if lc := v.rbg1LCBuf[0]; lc != 0x80|0x25 {
		t.Errorf("rbg1LCBuf[0] = 0x%02X, want 0xA5", lc)
	}
	v = setup(0x0100, 0x2501)
	clear(buf)
	renderTestRBG1(v, buf)
	if lc := v.rbg1LCBuf[0]; lc&0x80 != 0 {
		t.Errorf("KLCE off: rbg1LCBuf[0] = 0x%02X, want bit 7 clear", lc)
	}
}

// TestRBG1RPRCTLReReadRendering verifies the RBG1 span honors the
// parameter B RPRCTL re-read arms for Xst, Yst, and KAst.
func TestRBG1RPRCTLReReadRendering(t *testing.T) {
	cases := []struct {
		name   string
		arm    uint16
		offset uint32
		newHi  uint16
		want   []uint8
	}{
		{"Xst", 0x0100, 0x00, 16, []uint8{0, 255, 0}},
		{"Yst", 0x0200, 0x04, 8, []uint8{0, 0, 255}},
		{"KAst", 0x0400, 0x54, 0x6080, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := setupRBG1Scene(t)
			base := uint32(0x10080)
			if tc.offset == 0x54 {
				v.regs[vdp2KTCTL] = 0x0100
				writeRotParam32(v, base, 0x54, 0x6000, 0x0000)
				writeRotParam32(v, base, 0x58, 0x0000, 0x0000)
				writeVRAM16(v, 0x18000, 0x0001)
				writeVRAM16(v, 0x18002, 0x0000)
				writeVRAM16(v, 0x18200, 0x8001)
				writeVRAM16(v, 0x18202, 0x0000)
			}
			buf := make([]uint32, 352*256)
			v.BeginFrame()
			v.decodeLineState()
			if !v.frame.rbg1Active {
				t.Fatal("RBG1 not active")
			}
			v.vLine = 5
			v.Write(uint32(vdp2RPRCTL*2), tc.arm)
			for y := 0; y < 8; y++ {
				if y == 3 {
					writeRotParam32(v, base, tc.offset, tc.newHi, 0x0000)
				}
				v.rbg1SpanSetup(buf, &v.frame.rbg1, &v.frame.rbg1F, y)(0, v.frame.width)
			}
			expectRGB(t, v, buf, 0, 4, 255, 0, 0, "line 4 keeps the frame-start value")
			if tc.want == nil {
				expectTransparent(t, v, buf, 0, 5, "armed line re-reads")
				expectTransparent(t, v, buf, 0, 6, "line after the armed line keeps the re-read")
			} else {
				expectRGB(t, v, buf, 0, 5, tc.want[0], tc.want[1], tc.want[2], "armed line re-reads")
				expectRGB(t, v, buf, 0, 6, tc.want[0], tc.want[1], tc.want[2], "line after the armed line keeps the re-read")
			}
		})
	}
}

// TestRBG1MatrixTranspose verifies the RBG1 rotation matrix path with a
// non-identity matrix (A=0, B=1, D=1, E=0): screen (x, y) maps to map
// (y, x), so the green cells at map X 16-47 appear at screen rows
// 16-47 and the blue cell at map (0,8) appears at screen (8,0).
func TestRBG1MatrixTranspose(t *testing.T) {
	v := setupRBG1Scene(t)
	paramB := uint32(0x10080)
	writeRotParam32(v, paramB, 0x1C, 0x0000, 0x0000) // A = 0
	writeRotParam32(v, paramB, 0x20, 0x0001, 0x0000) // B = 1.0
	writeRotParam32(v, paramB, 0x28, 0x0001, 0x0000) // D = 1.0
	writeRotParam32(v, paramB, 0x2C, 0x0000, 0x0000) // E = 0
	buf := make([]uint32, 352*256)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "map (0,0)")
	expectRGB(t, v, buf, 7, 7, 255, 0, 0, "map (7,7)")
	expectRGB(t, v, buf, 0, 16, 0, 255, 0, "screen (0,16) -> map (16,0)")
	expectRGB(t, v, buf, 3, 40, 0, 255, 0, "screen (3,40) -> map (40,3)")
	expectRGB(t, v, buf, 8, 0, 0, 0, 255, "screen (8,0) -> map (0,8)")
	expectRGB(t, v, buf, 15, 7, 0, 0, 255, "screen (15,7) -> map (7,15)")
}

// writeBitmapRow8Blue writes bitmap rows 8-15 dots 0-15 as index 7
// (blue) on a 512-wide 256-color bitmap so a doubled or rebased Y step
// is observable, mirroring the cell scenes' blue cell (0,1).
func writeBitmapRow8Blue(v *VDP2) {
	for row := 8; row < 16; row++ {
		for x := 0; x < 16; x++ {
			v.vram[row*512+x] = 7
		}
	}
	v.cram[14], v.cram[15] = 0x7C, 0x00
}

// rbgSurface describes one rotation surface for the table-driven
// coefficient, re-read, mosaic, and hi-res tests: a fixture, a renderer,
// the parameter table under test, the KTCTL bits for that parameter,
// and whether the surface is a bitmap.
type rbgSurface struct {
	name     string
	setup    func(t *testing.T) *VDP2
	render   func(v *VDP2, buf []uint32)
	param    uint32 // rotation parameter table base
	ktEnable uint16 // KTCTL coefficient enable bit for the parameter
	ktMode   uint   // KTCTL shift of the 2-bit coefficient mode field
	ktKLCE   uint16 // KTCTL line color enable bit
	ktaofSh  uint   // KTAOF shift of the offset field
	oneWord  uint16 // KTCTL one-word table bit
	lcBuf    func(v *VDP2) []uint8
	bitmap   bool
	mzctlEn  uint16 // MZCTL mosaic enable bit
	rprArmX  uint16 // RPRCTL arm bits for Xst, Yst, KAst
	rprArmY  uint16
	rprArmK  uint16
	hiResFix bool
}

func rbgSurfaces() []rbgSurface {
	rbg0A := func(bitmap bool) rbgSurface {
		s := rbgSurface{
			name: "RBG0 cell A", param: 0x10000, ktEnable: 0x0001, ktMode: 2, ktKLCE: 0x0010,
			ktaofSh: 0, oneWord: 0x0002, lcBuf: func(v *VDP2) []uint8 { return v.rbg0LCBuf },
			render: renderTestRBG0, mzctlEn: 1 << 4, rprArmX: 0x0001, rprArmY: 0x0002, rprArmK: 0x0004,
		}
		if bitmap {
			s.name = "RBG0 bitmap A"
			s.bitmap = true
			s.setup = func(t *testing.T) *VDP2 {
				v := setupRBG0BitmapParamAB(t)
				writeBitmapRow8Blue(v)
				return v
			}
		} else {
			s.setup = setupRBG0ParamAB
		}
		return s
	}
	rbg0B := func(bitmap bool) rbgSurface {
		s := rbgSurface{
			name: "RBG0 cell B", param: 0x10080, ktEnable: 0x0100, ktMode: 10, ktKLCE: 0x1000,
			ktaofSh: 8, oneWord: 0x0200, lcBuf: func(v *VDP2) []uint8 { return v.rbg0LCBuf },
			render: renderTestRBG0, mzctlEn: 1 << 4, rprArmX: 0x0100, rprArmY: 0x0200, rprArmK: 0x0400,
		}
		if bitmap {
			s.name = "RBG0 bitmap B"
			s.bitmap = true
			s.setup = func(t *testing.T) *VDP2 {
				v := setupRBG0BitmapParamAB(t)
				writeBitmapRow8Blue(v)
				v.regs[vdp2RPMD] = 0x0001
				writeRBGParamBIdentity(v, 0)
				return v
			}
		} else {
			s.setup = func(t *testing.T) *VDP2 {
				v := setupRBG0ParamAB(t)
				v.regs[vdp2RPMD] = 0x0001
				writeRBGParamBIdentity(v, 0)
				return v
			}
		}
		return s
	}
	rbg1 := func(bitmap bool) rbgSurface {
		s := rbgSurface{
			name: "RBG1 cell", param: 0x10080, ktEnable: 0x0100, ktMode: 10, ktKLCE: 0x1000,
			ktaofSh: 8, oneWord: 0x0200, lcBuf: func(v *VDP2) []uint8 { return v.rbg1LCBuf },
			render: renderTestRBG1, mzctlEn: 1 << 0, rprArmX: 0x0100, rprArmY: 0x0200, rprArmK: 0x0400,
		}
		if bitmap {
			s.name = "RBG1 bitmap"
			s.bitmap = true
			s.setup = func(t *testing.T) *VDP2 {
				v := setupRBG1BitmapScene(t)
				writeBitmapRow8Blue(v)
				return v
			}
		} else {
			s.setup = setupRBG1Scene
		}
		return s
	}
	return []rbgSurface{rbg0A(false), rbg0A(true), rbg0B(false), rbg0B(true), rbg1(false), rbg1(true)}
}

// setCoefTable enables the surface's 2-word coefficient table at 0x18000
// (KAst integer 0x6000) with mode, and writes one entry there.
func (s rbgSurface) setCoefTable(v *VDP2, mode uint16, w0, w1 uint16) {
	v.regs[vdp2KTCTL] = s.ktEnable | mode<<s.ktMode
	v.regs[vdp2KTAOF] = 0x0000
	writeRotParam32(v, s.param, 0x54, 0x6000, 0x0000) // KAst -> 0x18000
	writeRotParam32(v, s.param, 0x58, 0x0000, 0x0000) // dKAst = 0
	writeVRAM16(v, 0x18000, w0)
	writeVRAM16(v, 0x18002, w1)
}

// TestRBGCoefficientModesKxKy verifies coefficient modes 1 (replace kx)
// and 2 (replace ky) on every rotation surface (KTCTL bits 3:2 for
// parameter A, 11:10 for parameter B): a coefficient of 2.0 doubles the
// horizontal or the vertical map step only.
func TestRBGCoefficientModesKxKy(t *testing.T) {
	for _, s := range rbgSurfaces() {
		for _, tc := range []struct {
			name     string
			mode     uint16
			c80, c04 [3]uint8
		}{
			{"mode 1 kx", 1, [3]uint8{0, 255, 0}, [3]uint8{255, 0, 0}},
			{"mode 2 ky", 2, [3]uint8{255, 0, 0}, [3]uint8{0, 0, 255}},
		} {
			t.Run(s.name+" "+tc.name, func(t *testing.T) {
				v := s.setup(t)
				s.setCoefTable(v, tc.mode, 0x0002, 0x0000)
				buf := make([]uint32, 352*256)
				s.render(v, buf)
				expectRGB(t, v, buf, 0, 0, 255, 0, 0, "origin")
				expectRGB(t, v, buf, 8, 0, tc.c80[0], tc.c80[1], tc.c80[2], "(8,0)")
				expectRGB(t, v, buf, 0, 4, tc.c04[0], tc.c04[1], tc.c04[2], "(0,4)")
			})
		}
	}
}

// TestRBGCoefficientKLCE verifies the line color enable (KTCTL bit 4 for
// A, bit 12 for B) stores the 2-word coefficient's line color bits with
// bit 7 set into the surface's line color buffer, and leaves bit 7 clear
// when disabled.
func TestRBGCoefficientKLCE(t *testing.T) {
	for _, s := range rbgSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			v := s.setup(t)
			s.setCoefTable(v, 0, 0x2501, 0x0000)
			v.regs[vdp2KTCTL] |= s.ktKLCE
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "dot renders")
			if lc := s.lcBuf(v)[0]; lc != 0xA5 {
				t.Errorf("line color buffer[0] = 0x%02X, want 0xA5", lc)
			}

			v = s.setup(t)
			s.setCoefTable(v, 0, 0x2501, 0x0000)
			clear(buf)
			s.render(v, buf)
			if lc := s.lcBuf(v)[0]; lc&0x80 != 0 {
				t.Errorf("KLCE off: line color buffer[0] = 0x%02X, want bit 7 clear", lc)
			}
		})
	}
}

// TestRBGCoefficientKTAOF verifies the coefficient table address offset
// (KTAOF bits 1:0 / 2:0 for parameter A, 9:8 / 10:8 for B): with 2-word
// tables the offset adds 0x40000 per unit, with 1-word tables 0x20000.
// The offset table holds an MSB=1 (transparent) entry, the unoffset
// table an opaque one.
func TestRBGCoefficientKTAOF(t *testing.T) {
	for _, s := range rbgSurfaces() {
		if s.bitmap {
			continue
		}
		t.Run(s.name+" 2-word", func(t *testing.T) {
			v := s.setup(t)
			s.setCoefTable(v, 0, 0x0001, 0x0000)
			writeVRAM16(v, 0x58000, 0x8001)
			writeVRAM16(v, 0x58002, 0x0000)
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "KTAOF 0 reads 0x18000")
			v.regs[vdp2KTAOF] = 1 << s.ktaofSh
			clear(buf)
			s.render(v, buf)
			expectTransparent(t, v, buf, 0, 0, "KTAOF 1 reads 0x58000")
		})
		t.Run(s.name+" 1-word", func(t *testing.T) {
			v := s.setup(t)
			s.setCoefTable(v, 0, 0x0000, 0x0000)
			v.regs[vdp2KTCTL] |= s.oneWord
			// 1-word entries: KAst 0x6000 * 2 = 0xC000; offset 1 adds
			// 0x20000. Value format is sign+4.10 in bits 14:0.
			writeVRAM16(v, 0xC000, 0x0400)  // 1.0
			writeVRAM16(v, 0x2C000, 0x8400) // MSB set
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "1-word KTAOF 0 reads 0xC000")
			v.regs[vdp2KTAOF] = 1 << s.ktaofSh
			clear(buf)
			s.render(v, buf)
			expectTransparent(t, v, buf, 0, 0, "1-word KTAOF 1 reads 0x2C000")
		})
	}
}

// TestRBGCoefficientOneWordValue verifies the 1-word coefficient value
// format (sign+4.10, VDP2 manual Sec 6.1): 2.0 doubles the map step.
func TestRBGCoefficientOneWordValue(t *testing.T) {
	for _, s := range rbgSurfaces() {
		if s.bitmap {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			v := s.setup(t)
			s.setCoefTable(v, 0, 0x0000, 0x0000)
			v.regs[vdp2KTCTL] |= s.oneWord
			writeVRAM16(v, 0xC000, 0x0800) // 2.0
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "origin")
			expectRGB(t, v, buf, 8, 0, 0, 255, 0, "(8,0) -> map 16")
			expectRGB(t, v, buf, 0, 4, 0, 0, 255, "(0,4) -> map row 8")
		})
	}
}

// TestRBGLineKAst verifies the per-line coefficient address (KAst +
// dKAst * Vcnt, VDP2 manual Sec 6.1): with dKAst = 1.0 line y reads
// entry y, so lines 0-3 render and lines 4-7 are transparent.
func TestRBGLineKAst(t *testing.T) {
	for _, s := range rbgSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			v := s.setup(t)
			s.setCoefTable(v, 0, 0x0001, 0x0000)
			writeRotParam32(v, s.param, 0x58, 0x0001, 0x0000) // dKAst = 1.0
			for line := uint32(0); line < 8; line++ {
				hi := uint16(0x0001)
				if line >= 4 {
					hi = 0x8001
				}
				writeVRAM16(v, 0x18000+line*4, hi)
				writeVRAM16(v, 0x18002+line*4, 0x0000)
			}
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "line 0")
			expectRGB(t, v, buf, 0, 3, 255, 0, 0, "line 3")
			expectTransparent(t, v, buf, 0, 4, "line 4")
			expectTransparent(t, v, buf, 0, 7, "line 7")
		})
	}
}

// TestRBGCoefficientMode3Xp verifies coefficient mode 3 replaces Xp
// (2-word format sign+13.8 in bits 23:0 of the entry): +16 shifts the
// map 16 dots right (green at the origin), -16 shifts it left (red
// reappears at screen x 16).
func TestRBGCoefficientMode3Xp(t *testing.T) {
	for _, s := range rbgSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			v := s.setup(t)
			s.setCoefTable(v, 3, 0x0000, 0x1000) // +16.0
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 0, 255, 0, "Xp +16 origin")
			expectRGB(t, v, buf, 15, 0, 0, 255, 0, "Xp +16 (15,0)")

			v = s.setup(t)
			s.setCoefTable(v, 3, 0x00FF, 0xF000) // -16.0
			clear(buf)
			s.render(v, buf)
			expectRGB(t, v, buf, 16, 0, 255, 0, 0, "Xp -16 (16,0) -> map 0")
			expectRGB(t, v, buf, 24, 0, 255, 0, 0, "Xp -16 (24,0) -> map 8")
			expectRGB(t, v, buf, 32, 0, 0, 255, 0, "Xp -16 (32,0) -> map 16")

			// 1-word mode 3: sign+12.2 in bits 14:0 (value << 8 gives .10).
			v = s.setup(t)
			s.setCoefTable(v, 3, 0x0000, 0x0000)
			v.regs[vdp2KTCTL] |= s.oneWord
			writeVRAM16(v, 0xC000, 0x0040) // +16.0
			clear(buf)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 0, 255, 0, "1-word Xp +16 origin")
			writeVRAM16(v, 0xC000, 0x7FC0) // -16.0
			clear(buf)
			s.render(v, buf)
			expectRGB(t, v, buf, 16, 0, 255, 0, 0, "1-word Xp -16 (16,0) -> map 0")
			expectRGB(t, v, buf, 32, 0, 0, 255, 0, "1-word Xp -16 (32,0) -> map 16")
		})
	}
}

// TestRBGCoefficientNegative verifies sign extension of negative scale
// values: kx = -2.0 from the parameter table (sign+7.16), from a 2-word
// coefficient (sign+7.16), and from a 1-word coefficient (sign+4.10).
// With Mx = 32 the map X is 32 - 2x: green at x 0 and 8, red at 12 and
// 16.
func TestRBGCoefficientNegative(t *testing.T) {
	for _, s := range rbgSurfaces() {
		if s.bitmap {
			continue
		}
		for _, tc := range []struct {
			name  string
			apply func(v *VDP2)
		}{
			{"parameter kx", func(v *VDP2) {
				writeRotParam32(v, s.param, 0x4C, 0x00FE, 0x0000)
			}},
			{"2-word coefficient", func(v *VDP2) {
				s.setCoefTable(v, 1, 0x00FE, 0x0000)
			}},
			{"1-word coefficient", func(v *VDP2) {
				s.setCoefTable(v, 1, 0x0000, 0x0000)
				v.regs[vdp2KTCTL] |= s.oneWord
				writeVRAM16(v, 0xC000, 0x7800)
			}},
		} {
			t.Run(s.name+" "+tc.name, func(t *testing.T) {
				v := s.setup(t)
				writeRotParam32(v, s.param, 0x44, 0x0020, 0x0000) // Mx = 32
				tc.apply(v)
				buf := make([]uint32, 352*256)
				s.render(v, buf)
				expectRGB(t, v, buf, 0, 0, 0, 255, 0, "(0,0) -> map 32")
				expectRGB(t, v, buf, 8, 0, 0, 255, 0, "(8,0) -> map 16")
				expectRGB(t, v, buf, 12, 0, 255, 0, 0, "(12,0) -> map 8")
				expectRGB(t, v, buf, 16, 0, 255, 0, 0, "(16,0) -> map 0")
			})
		}
	}
}

// TestRBGCoefficientFromCRAM verifies CRKTE (RAMCTL bit 15, CRAM mode 1)
// reads the coefficient table from the upper half of CRAM for every
// surface: the entry at CRAM 0x800 is transparent with MSB set and
// doubles the map step with value 2.0.
func TestRBGCoefficientFromCRAM(t *testing.T) {
	for _, s := range rbgSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			run := func(w0 uint16) []uint32 {
				v := s.setup(t)
				v.regs[vdp2RAMCTL] = 0x9000
				v.regs[vdp2KTCTL] = s.ktEnable
				v.regs[vdp2KTAOF] = 0x0000
				writeRotParam32(v, s.param, 0x54, 0x0000, 0x0000) // KAst 0 -> CRAM 0x800
				writeRotParam32(v, s.param, 0x58, 0x0000, 0x0000)
				v.cram[0x800], v.cram[0x801] = uint8(w0>>8), uint8(w0)
				v.cram[0x802], v.cram[0x803] = 0, 0
				buf := make([]uint32, 352*256)
				s.render(v, buf)
				return buf
			}
			v := s.setup(t)
			v.BeginFrame()
			buf := run(0x8001)
			expectTransparent(t, v, buf, 0, 0, "CRAM coefficient MSB=1")
			buf = run(0x0002)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "CRAM coefficient 2.0 origin")
			expectRGB(t, v, buf, 8, 0, 0, 255, 0, "CRAM coefficient 2.0 (8,0)")
		})
	}
}

// TestRBGPerDotCoefficientBank verifies per-dot coefficient reads (dKAx
// nonzero) take place only from a VRAM bank designated as coefficient
// RAM (RAMCTL RDBS = 01, VDP2 manual Sec 6.2) on every surface: dot 1's
// own MSB=1 entry makes it transparent when bank A0 is designated, and
// dot 1 keeps the line-start coefficient otherwise.
func TestRBGPerDotCoefficientBank(t *testing.T) {
	for _, s := range rbgSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			run := func(ramctl uint16) []uint32 {
				v := s.setup(t)
				s.setCoefTable(v, 0, 0x0001, 0x0000)
				writeRotParam32(v, s.param, 0x5C, 0x0001, 0x0000) // dKAx = 1.0
				writeVRAM16(v, 0x18004, 0x8001)
				writeVRAM16(v, 0x18006, 0x0000)
				v.regs[vdp2RAMCTL] = ramctl
				buf := make([]uint32, 352*256)
				s.render(v, buf)
				return buf
			}
			v := s.setup(t)
			v.BeginFrame()
			buf := run(0x0001)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "designated: dot 0")
			expectTransparent(t, v, buf, 1, 0, "designated: dot 1 reads its own entry")
			buf = run(0x0000)
			expectRGB(t, v, buf, 1, 0, 255, 0, 0, "undesignated: dot 1 keeps the line coefficient")
		})
	}
}

// TestRBGRPRCTLReReadAllSurfaces verifies the RPRCTL per-line re-read
// arms on the bitmap surfaces and RBG1 cell (the RBG0 cell case is
// TestRBG0RPRCTLReReadRendering).
func TestRBGRPRCTLReReadAllSurfaces(t *testing.T) {
	for _, s := range rbgSurfaces() {
		for _, tc := range []struct {
			name   string
			arm    uint16
			offset uint32
			newHi  uint16
			want   []uint8
		}{
			{"Xst", s.rprArmX, 0x00, 16, []uint8{0, 255, 0}},
			{"Yst", s.rprArmY, 0x04, 8, []uint8{0, 0, 255}},
			{"KAst", s.rprArmK, 0x54, 0x6080, nil},
		} {
			t.Run(s.name+" "+tc.name, func(t *testing.T) {
				v := s.setup(t)
				if tc.offset == 0x54 {
					s.setCoefTable(v, 0, 0x0001, 0x0000)
					writeVRAM16(v, 0x18200, 0x8001)
					writeVRAM16(v, 0x18202, 0x0000)
				}
				buf := make([]uint32, 352*256)
				v.BeginFrame()
				v.decodeLineState()
				v.vLine = 5
				v.Write(uint32(vdp2RPRCTL*2), tc.arm)
				for y := 0; y < 8; y++ {
					if y == 3 {
						writeRotParam32(v, s.param, tc.offset, tc.newHi, 0x0000)
					}
					if s.name[:4] == "RBG1" {
						v.rbg1SpanSetup(buf, &v.frame.rbg1, &v.frame.rbg1F, y)(0, v.frame.width)
					} else {
						v.rbg0SpanSetup(buf, &v.frame.rbg0, &v.frame.rbg0F, y)(0, v.frame.width)
					}
				}
				expectRGB(t, v, buf, 0, 4, 255, 0, 0, "line 4 keeps the frame-start value")
				if tc.want == nil {
					expectTransparent(t, v, buf, 0, 5, "armed line re-reads")
				} else {
					expectRGB(t, v, buf, 0, 5, tc.want[0], tc.want[1], tc.want[2], "armed line re-reads")
					expectRGB(t, v, buf, 0, 7, tc.want[0], tc.want[1], tc.want[2], "later line keeps the re-read")
				}
			})
		}
	}
}

// TestRBGMosaicHorizontal verifies horizontal mosaic (MZCTL R0MZE bit 4
// for RBG0, N0MZE bit 0 for RBG1, size in bits 11:8) samples the first
// dot of each mosaic cell on the bitmap and RBG1 surfaces.
func TestRBGMosaicHorizontal(t *testing.T) {
	for _, s := range rbgSurfaces() {
		if s.name == "RBG0 cell A" {
			continue // TestRBG0MosaicHorizontal
		}
		t.Run(s.name, func(t *testing.T) {
			v := s.setup(t)
			// A dot inside the second mosaic cell that differs from the
			// cell's first dot.
			var x int
			var plain [3]uint8
			if s.bitmap {
				v.vram[17] = 7
				v.cram[14], v.cram[15] = 0x7C, 0x00
				x = 17
				plain = [3]uint8{0, 0, 255}
				v.regs[vdp2MZCTL] = s.mzctlEn | 0x0F00 // mosaic 16
			} else {
				writeRBGTestOverTile(v, 0x403)
				writeVRAM16(v, 2, 0x0403) // cell (0,0) -> over tile
				x = 5
				plain = [3]uint8{0, 255, 0}
				v.regs[vdp2MZCTL] = s.mzctlEn | 0x0700 // mosaic 8
			}
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			if s.bitmap {
				expectRGB(t, v, buf, x, 0, 0, 255, 0, "mosaic samples dot 16")
			} else {
				expectRGB(t, v, buf, x, 0, 0, 0, 255, "mosaic samples dot 0")
			}
			v.regs[vdp2MZCTL] = 0
			clear(buf)
			s.render(v, buf)
			expectRGB(t, v, buf, x, 0, plain[0], plain[1], plain[2], "no mosaic")
		})
	}
}

// TestRBGHiResDotPairs verifies the hi-res rotation Hcnt (two screen dots
// per rotation coordinate) on the bitmap and RBG1 surfaces (the RBG0
// cell case is TestRBG0HiResHcntDotPairs). With Xst 8, screen x 15
// samples map 15 (red) in hi-res and map 23 (green) otherwise.
func TestRBGHiResDotPairs(t *testing.T) {
	for _, s := range rbgSurfaces() {
		if s.name == "RBG0 cell A" {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			v := s.setup(t)
			writeRotParam32(v, s.param, 0x00, 8, 0x0000)
			v.hiRes = true
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 15, 0, 255, 0, 0, "hi-res x 15 -> map 15")
			expectRGB(t, v, buf, 16, 0, 0, 255, 0, "hi-res x 16 -> map 16")
			v.hiRes = false
			clear(buf)
			s.render(v, buf)
			expectRGB(t, v, buf, 15, 0, 0, 255, 0, "normal x 15 -> map 23")
		})
	}
}

// TestRBG1WrapReadsLastCell verifies RBG1 screen-over wrap (mode 0) reads
// the plane's last cell for a negative map X.
func TestRBG1WrapReadsLastCell(t *testing.T) {
	v := setupRBG1Scene(t)
	writeRotParam32(v, 0x10080, 0x00, rbgXstNeg8, 0x0000)
	// Map cell 255 is plane D (page 0) cell (63,0).
	writeRBGTestCell(v, 63, 0, 0x402)
	buf := make([]uint32, 352*256)
	renderTestRBG1(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 0, 255, "map X -8 wraps to cell 255")
	expectRGB(t, v, buf, 8, 0, 255, 0, 0, "map X 0")
}

// TestRPWindowNoWindowsLogicBit verifies the rotation parameter window
// with neither W0 nor W1 enabled (VDP2 manual Sec 8.1 p.193): the whole
// screen is the active area when the logic bit is 1 (parameter B) and
// none of it when 0 (parameter A).
func TestRPWindowNoWindowsLogicBit(t *testing.T) {
	v := setupRBG0ParamAB(t)
	v.regs[vdp2RPMD] = 0x0003
	buf := make([]uint32, 352*256)
	v.regs[vdp2WCTLD] = 0x0000
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 255, 0, 0, "logic 0: parameter A everywhere")
	expectRGB(t, v, buf, 12, 5, 255, 0, 0, "logic 0: parameter A everywhere")
	v.regs[vdp2WCTLD] = 0x0080
	clear(buf)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 0, 0, 0, 255, 0, "logic 1: parameter B everywhere")
	expectRGB(t, v, buf, 12, 5, 0, 255, 0, "logic 1: parameter B everywhere")
}

// TestRPWindowLineWindowTable verifies the rotation parameter window
// honors the W0 and W1 line window tables (LWTA0/LWTA1 bit 15): each
// line's X range comes from its table entry, and an entry whose start
// exceeds its end excludes the whole line.
func TestRPWindowLineWindowTable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wctld  uint16
		lwtaU  int
		lwtaL  int
		lwtaUV uint16
	}{
		{"W0", 0x0002, vdp2LWTA0U, vdp2LWTA0L, 0x8002},
		{"W1", 0x0008, vdp2LWTA1U, vdp2LWTA1L, 0x8002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := setupRBG0ParamAB(t)
			v.regs[vdp2RPMD] = 0x0003
			v.regs[vdp2WCTLD] = tc.wctld
			v.regs[tc.lwtaU] = tc.lwtaUV // table at 0x40000
			v.regs[tc.lwtaL] = 0x0000
			// Line 0: x 0..10 (raw half-dots 0..20). Line 1: excluded
			// (start > end). Line 2: x 4..20.
			writeVRAM16(v, 0x40000, 0)
			writeVRAM16(v, 0x40002, 20)
			writeVRAM16(v, 0x40004, 20)
			writeVRAM16(v, 0x40006, 0)
			writeVRAM16(v, 0x40008, 8)
			writeVRAM16(v, 0x4000A, 40)
			buf := make([]uint32, 352*256)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 3, 0, 0, 255, 0, "line 0 x 3 inside -> B")
			expectRGB(t, v, buf, 12, 0, 255, 0, 0, "line 0 x 12 outside -> A")
			expectRGB(t, v, buf, 3, 1, 255, 0, 0, "line 1 excluded -> A")
			expectRGB(t, v, buf, 3, 2, 255, 0, 0, "line 2 x 3 outside -> A")
			expectRGB(t, v, buf, 12, 2, 0, 255, 0, "line 2 x 12 inside -> B")
		})
	}
}

// TestRBGOneWordPatternName verifies 1-word pattern names (PNCR / PNCN0
// bit 15) on RBG0 and RBG1: 16-color aux 0 format with palette in bits
// 15:12, flips in bits 11:10, character bits 9:0 supplemented from the
// control register's bits 4:0, and 2-byte entries.
func TestRBGOneWordPatternName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T) *VDP2
		render func(v *VDP2, buf []uint32)
		pnc    int
	}{
		{"RBG0", setupRBG0Identity, renderTestRBG0, vdp2PNCR},
		{"RBG1", setupRBG1Identity, renderTestRBG1, vdp2PNCN0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.setup(t)
			v.regs[tc.pnc] = 0x8001 // 1-word, supplement character bits = 1
			writeRBGTestTile(v, 0x400, rbgTestRed)
			writeRBGTestTile(v, 0x401, rbgTestGreen)
			writeRBGTestOverTile(v, 0x403)
			writeRBGTestPalette(v)
			writeVRAM16(v, 0, 0x1000) // cell (0,0): palette 1, character 0x400
			writeVRAM16(v, 2, 0x1001) // cell (1,0): character 0x401
			writeVRAM16(v, 4, 0x1403) // cell (2,0): character 0x403, hflip
			buf := make([]uint32, 352*256)
			tc.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "cell 0")
			expectRGB(t, v, buf, 8, 0, 0, 255, 0, "cell 1")
			expectRGB(t, v, buf, 16, 0, 0, 255, 0, "cell 2 hflip dot 0")
			expectRGB(t, v, buf, 21, 0, 0, 0, 255, "cell 2 hflip dot 5")
		})
	}
}

// TestRBGScreenOverPatternFormats verifies the screen-over character in
// the 256-color and 32768-color formats and with 2x2 character size
// (including a flipped 2x2 character), and that a zero priority makes
// the screen-over character transparent, for RBG0 (parameter A) and
// RBG1.
func TestRBGScreenOverPatternFormats(t *testing.T) {
	type surface struct {
		name   string
		setup  func(t *testing.T) *VDP2
		render func(v *VDP2, buf []uint32)
		chctl  int
		chShif uint // shift of the color mode field
		chSize uint16
		pnc    int
		ovpn   int
		plsz   uint16
		pri    int
	}
	surfaces := []surface{
		{"RBG0", setupRBG0Identity, renderTestRBG0, vdp2CHCTLB, 12, 0x0100, vdp2PNCR, vdp2OVPNRA, 1 << 10, vdp2PRIR},
		{"RBG1", setupRBG1Identity, renderTestRBG1, vdp2CHCTLA, 4, 0x0001, vdp2PNCN0, vdp2OVPNRB, 1 << 14, vdp2PRINA},
	}
	for _, s := range surfaces {
		base := func(t *testing.T) *VDP2 {
			v := s.setup(t)
			v.regs[vdp2PLSZ] = s.plsz
			v.regs[s.pnc] = 0x0001
			// Parameter A (RBG0) or B (RBG1) starts off-plane at map X -8.
			p := uint32(0x10000)
			if s.name == "RBG1" {
				p = 0x10080
			}
			writeRotParam32(v, p, 0x00, rbgXstNeg8, 0x0000)
			return v
		}
		t.Run(s.name+" 256-color", func(t *testing.T) {
			v := base(t)
			v.regs[s.chctl] = 1 << s.chShif
			for i := uint32(0); i < 64; i++ {
				v.vram[0x8000+i] = 5
			}
			v.cram[10], v.cram[11] = 0x00, 0x1F
			v.regs[s.ovpn] = 0x0000 // palette 0, character 0x400
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "256-color over dot")
			expectRGB(t, v, buf, 7, 7, 255, 0, 0, "256-color over dot")
		})
		t.Run(s.name+" 2048-color", func(t *testing.T) {
			v := base(t)
			v.regs[s.chctl] = 2 << s.chShif
			fillVRAM16(v, 0x8000, 64, 0x0005)
			v.cram[10], v.cram[11] = 0x00, 0x1F
			v.regs[s.ovpn] = 0x0000
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "2048-color over dot")
			fillVRAM16(v, 0x8000, 64, 0x0000) // index 0: transparent
			clear(buf)
			s.render(v, buf)
			expectTransparent(t, v, buf, 0, 0, "2048-color over dot index 0")
		})
		t.Run(s.name+" 32768-color", func(t *testing.T) {
			v := base(t)
			v.regs[s.chctl] = 3 << s.chShif
			fillVRAM16(v, 0x8000, 64, 0x83E0)
			v.regs[s.ovpn] = 0x0000
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 0, 255, 0, "32K over dot")
			fillVRAM16(v, 0x8000, 64, 0x03E0) // MSB clear: transparent
			clear(buf)
			s.render(v, buf)
			expectTransparent(t, v, buf, 0, 0, "32K over dot MSB clear")
		})
		t.Run(s.name+" 2x2", func(t *testing.T) {
			v := base(t)
			v.regs[s.chctl] = s.chSize
			// A 16-dot character needs 16 off-plane dots: start at map X -16.
			p := uint32(0x10000)
			if s.name == "RBG1" {
				p = 0x10080
			}
			writeRotParam32(v, p, 0x00, 0x1FF0, 0x0000)
			writeRBGTestTile(v, 0x400, rbgTestRed)
			writeRBGTestTile(v, 0x401, rbgTestGreen)
			writeRBGTestTile(v, 0x402, rbgTestBlue)
			writeRBGTestTile(v, 0x403, 0x6)
			writeRBGTestPalette(v)
			v.cram[44], v.cram[45] = 0x7F, 0xFF
			// 1-word 2x2 16-color aux 0: character = bits 9:0 << 2 with
			// supplement bits 1:0 in the low two bits, so 0x400 is bits
			// 9:0 = 0x100 with no supplement.
			v.regs[s.pnc] = 0x0000
			v.regs[s.ovpn] = 0x1100 // palette 1, character 0x400
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "2x2 over top-left")
			expectRGB(t, v, buf, 8, 0, 0, 255, 0, "2x2 over top-right")
			expectRGB(t, v, buf, 0, 8, 0, 0, 255, "2x2 over bottom-left")
			expectRGB(t, v, buf, 8, 8, 255, 255, 255, "2x2 over bottom-right")
			v.regs[s.ovpn] = 0x1100 | 0x0C00 // both flips
			clear(buf)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 255, 255, "2x2 over flipped top-left")
			expectRGB(t, v, buf, 8, 8, 255, 0, 0, "2x2 over flipped bottom-right")
		})
		// A screen priority of 1 under special priority mode 1 becomes 0
		// for characters whose special priority bit is clear, and a dot
		// with effective priority 0 is transparent (manual Sec 11.1).
		t.Run(s.name+" effective priority zero", func(t *testing.T) {
			v := base(t)
			writeRBGTestTile(v, 0x400, rbgTestRed)
			writeRBGTestPalette(v)
			writeRBGTestCell(v, 0, 0, 0x400) // in-plane cell, special bit clear
			v.regs[s.ovpn] = 0x1000
			v.regs[s.pri] = (v.regs[s.pri] &^ 0x0007) | 0x0001
			sfShift := uint(8)
			if s.name == "RBG1" {
				sfShift = 0
			}
			v.regs[vdp2SFPRMD] = 1 << sfShift
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectTransparent(t, v, buf, 0, 0, "over dot, special bit clear")
			expectTransparent(t, v, buf, 8, 0, "in-plane dot, special bit clear")

			v.regs[s.pnc] |= 1 << 9   // over character special priority bit
			writeVRAM16(v, 0, 0x2001) // in-plane cell special priority bit
			clear(buf)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "over dot, special bit set")
			expectRGB(t, v, buf, 8, 0, 255, 0, 0, "in-plane dot, special bit set")
			if pri := (buf[0] >> 24) & 7; pri != 1 {
				t.Errorf("over dot priority %d, want 1", pri)
			}
		})
	}
}

// TestRBGSpecialFunctionMode2And3 verifies special priority mode 2 and
// special color calculation modes 2 and 3 (VDP2 manual Tables 11.2 and
// 12.3) on every rotation surface and on the RBG0 screen-over character:
// mode 2 requires the special bit and a special function code match on
// the dot color's nibble pair, mode 3 CC follows the CRAM entry's MSB.
func TestRBGSpecialFunctionMode2And3(t *testing.T) {
	type surface struct {
		name string
		// build sets up the scene with the given special priority and
		// special CC bits and returns the VDP2 plus the sample x.
		build   func(t *testing.T, pri, cc bool) (*VDP2, int)
		render  func(v *VDP2, buf []uint32)
		sfShift uint // SFPRMD/SFCCMD field shift (8 for RBG0, 0 for RBG1)
		ccctl   uint16
		priReg  int
		// nibble pair of the sampled dot's color index
		pair uint8
		// CRAM byte offset of the sampled dot's color entry
		cramOff int
	}
	specialMSW := func(pri, cc bool) uint16 {
		msw := uint16(0x0001)
		if pri {
			msw |= 0x2000
		}
		if cc {
			msw |= 0x1000
		}
		return msw
	}
	specialBMP := func(pri, cc bool) uint16 {
		var r uint16
		if pri {
			r |= 1 << 5
		}
		if cc {
			r |= 1 << 4
		}
		return r
	}
	surfaces := []surface{
		{"RBG0 cell", func(t *testing.T, pri, cc bool) (*VDP2, int) {
			v := setupRBG0ParamAB(t)
			writeVRAM16(v, 0, specialMSW(pri, cc))
			return v, 0
		}, renderTestRBG0, 8, 1 << 4, vdp2PRIR, 1, 38},
		{"RBG0 bitmap", func(t *testing.T, pri, cc bool) (*VDP2, int) {
			v := setupRBG0BitmapParamAB(t)
			v.regs[vdp2BMPNB] = specialBMP(pri, cc)
			return v, 0
		}, renderTestRBG0, 8, 1 << 4, vdp2PRIR, 2, 10},
		{"RBG0 over pattern", func(t *testing.T, pri, cc bool) (*VDP2, int) {
			v := setupRBG0ParamAB(t)
			v.regs[vdp2PLSZ] = 1 << 10
			writeRotParam32(v, 0x10000, 0x00, rbgXstNeg8, 0x0000)
			v.regs[vdp2PNCR] = 0x0001
			if pri {
				v.regs[vdp2PNCR] |= 1 << 9
			}
			if cc {
				v.regs[vdp2PNCR] |= 1 << 8
			}
			v.regs[vdp2OVPNRA] = 0x1000 // character 0x400 (red, dot 3)
			return v, 0
		}, renderTestRBG0, 8, 1 << 4, vdp2PRIR, 1, 38},
		{"RBG1 cell", func(t *testing.T, pri, cc bool) (*VDP2, int) {
			v := setupRBG1Scene(t)
			writeVRAM16(v, 0, specialMSW(pri, cc))
			return v, 0
		}, renderTestRBG1, 0, 1 << 0, vdp2PRINA, 1, 38},
		{"RBG1 bitmap", func(t *testing.T, pri, cc bool) (*VDP2, int) {
			v := setupRBG1BitmapScene(t)
			v.regs[vdp2BMPNA] = specialBMP(pri, cc)
			return v, 0
		}, renderTestRBG1, 0, 1 << 0, vdp2PRINA, 2, 10},
		{"RBG1 over pattern", func(t *testing.T, pri, cc bool) (*VDP2, int) {
			v := setupRBG1Scene(t)
			v.regs[vdp2PLSZ] = 1 << 14
			writeRotParam32(v, 0x10080, 0x00, rbgXstNeg8, 0x0000)
			v.regs[vdp2PNCN0] = 0x0001
			if pri {
				v.regs[vdp2PNCN0] |= 1 << 9
			}
			if cc {
				v.regs[vdp2PNCN0] |= 1 << 8
			}
			v.regs[vdp2OVPNRB] = 0x1000
			return v, 0
		}, renderTestRBG1, 0, 1 << 0, vdp2PRINA, 1, 38},
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			sample := func(pri, cc bool, sfprmd, sfccmd, sfcode uint16, cramMSB bool) uint32 {
				v, x := s.build(t, pri, cc)
				v.regs[s.priReg] = (v.regs[s.priReg] &^ 0x07) | 0x0002
				v.regs[vdp2SFPRMD] = sfprmd << s.sfShift
				v.regs[vdp2SFCCMD] = sfccmd << s.sfShift
				v.regs[vdp2SFCODE] = sfcode
				v.regs[vdp2SFSEL] = 0
				v.regs[vdp2CCCTL] = s.ccctl
				if cramMSB {
					v.cram[s.cramOff] |= 0x80
				}
				buf := make([]uint32, 352*256)
				s.render(v, buf)
				if buf[x] == 0 {
					t.Fatalf("sample dot transparent (pri=%v cc=%v sfprmd=%d sfccmd=%d sfcode=0x%02X)", pri, cc, sfprmd, sfccmd, sfcode)
				}
				return buf[x]
			}
			match := uint16(1) << s.pair
			noMatch := uint16(1) << ((s.pair + 1) & 7)

			// Special priority mode 2.
			if pri := (sample(true, false, 2, 0, match, false) >> 24) & 7; pri != 3 {
				t.Errorf("SFPRMD 2, bit set, code match: priority %d, want 3", pri)
			}
			if pri := (sample(true, false, 2, 0, noMatch, false) >> 24) & 7; pri != 2 {
				t.Errorf("SFPRMD 2, bit set, no code match: priority %d, want 2", pri)
			}
			if pri := (sample(false, false, 2, 0, match, false) >> 24) & 7; pri != 2 {
				t.Errorf("SFPRMD 2, bit clear, code match: priority %d, want 2", pri)
			}

			// Special color calculation mode 2.
			if px := sample(false, true, 0, 2, match, false); px&layerCCBit == 0 {
				t.Error("SFCCMD 2, bit set, code match: CC bit clear")
			}
			if px := sample(false, true, 0, 2, noMatch, false); px&layerCCBit != 0 {
				t.Error("SFCCMD 2, bit set, no code match: CC bit set")
			}
			if px := sample(false, false, 0, 2, match, false); px&layerCCBit != 0 {
				t.Error("SFCCMD 2, bit clear, code match: CC bit set")
			}

			// Special color calculation mode 3.
			if px := sample(false, false, 0, 3, 0, true); px&layerCCBit == 0 {
				t.Error("SFCCMD 3, CRAM MSB set: CC bit clear")
			}
			if px := sample(false, false, 0, 3, 0, false); px&layerCCBit != 0 {
				t.Error("SFCCMD 3, CRAM MSB clear: CC bit set")
			}
		})
	}
}

// TestRBGSpecialCCMode3RGB verifies special color calculation mode 3 on
// an RGB-format (32768-color) surface follows the screen CC enable
// alone, since an RGB dot has no CRAM entry to take the MSB from
// (manual Table 12.3), on every rotation surface and screen-over
// character.
func TestRBGSpecialCCMode3RGB(t *testing.T) {
	type surface struct {
		name   string
		build  func(t *testing.T) *VDP2
		render func(v *VDP2, buf []uint32)
		sfShif uint
		ccctl  uint16
	}
	rgbCell := func(v *VDP2) {
		fillVRAM16(v, 0x8000, 64, 0x801F)
		writeVRAM16(v, 0, 0x0000)
		writeVRAM16(v, 2, 0x0400)
	}
	surfaces := []surface{
		{"RBG0 cell", func(t *testing.T) *VDP2 {
			v := setupRBG0Identity(t)
			v.regs[vdp2CHCTLB] = 0x3000
			rgbCell(v)
			return v
		}, renderTestRBG0, 8, 1 << 4},
		{"RBG0 bitmap", func(t *testing.T) *VDP2 {
			v := setupRBG0BitmapIdentity(t)
			v.regs[vdp2CHCTLB] = 0x3200
			writeVRAM16(v, 0, 0x801F)
			return v
		}, renderTestRBG0, 8, 1 << 4},
		{"RBG0 over pattern", func(t *testing.T) *VDP2 {
			v := setupRBG0Identity(t)
			v.regs[vdp2CHCTLB] = 0x3000
			v.regs[vdp2PLSZ] = 1 << 10
			v.regs[vdp2PNCR] = 0x0001
			v.regs[vdp2OVPNRA] = 0x0000
			fillVRAM16(v, 0x8000, 64, 0x801F)
			writeRotParam32(v, 0x10000, 0x00, rbgXstNeg8, 0x0000)
			return v
		}, renderTestRBG0, 8, 1 << 4},
		{"RBG1 cell", func(t *testing.T) *VDP2 {
			v := setupRBG1Identity(t)
			v.regs[vdp2CHCTLA] = 0x0030
			rgbCell(v)
			return v
		}, renderTestRBG1, 0, 1 << 0},
		{"RBG1 bitmap", func(t *testing.T) *VDP2 {
			v := setupRBG1BitmapScene(t)
			v.regs[vdp2CHCTLA] = 0x0032
			writeVRAM16(v, 0, 0x801F)
			return v
		}, renderTestRBG1, 0, 1 << 0},
		{"RBG1 over pattern", func(t *testing.T) *VDP2 {
			v := setupRBG1Identity(t)
			v.regs[vdp2CHCTLA] = 0x0030
			v.regs[vdp2PLSZ] = 1 << 14
			v.regs[vdp2PNCN0] = 0x0001
			v.regs[vdp2OVPNRB] = 0x0000
			fillVRAM16(v, 0x8000, 64, 0x801F)
			writeRotParam32(v, 0x10080, 0x00, rbgXstNeg8, 0x0000)
			return v
		}, renderTestRBG1, 0, 1 << 0},
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			v := s.build(t)
			v.regs[vdp2SFCCMD] = 3 << s.sfShif
			v.regs[vdp2CCCTL] = s.ccctl
			buf := make([]uint32, 352*256)
			s.render(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "RGB dot")
			if buf[0]&layerCCBit == 0 {
				t.Error("SFCCMD 3 on RGB format with screen CC on: CC bit clear")
			}
			v.regs[vdp2CCCTL] = 0
			clear(buf)
			s.render(v, buf)
			if buf[0]&layerCCBit != 0 {
				t.Error("SFCCMD 3 on RGB format with screen CC off: CC bit set")
			}
		})
	}
}

// TestRBGBitmapSpecialBitsMode1 verifies bitmap special priority and CC
// under per-character mode 1 on RBG0 (BMPNB bits 5 and 4) and the
// effective-priority-zero transparency on both bitmap surfaces (screen
// priority 1, special priority bit clear).
func TestRBGBitmapSpecialBitsMode1(t *testing.T) {
	buf := make([]uint32, 352*256)

	run := func(bmpnb uint16) uint32 {
		v := setupRBG0BitmapParamAB(t)
		v.regs[vdp2BMPNB] = bmpnb
		v.regs[vdp2PRIR] = 0x0002
		v.regs[vdp2SFPRMD] = 0x0100
		v.regs[vdp2SFCCMD] = 0x0100
		v.regs[vdp2CCCTL] = 1 << 4
		clear(buf)
		renderTestRBG0(v, buf)
		if buf[0] == 0 {
			t.Fatalf("BMPNB 0x%04X: dot (0,0) transparent", bmpnb)
		}
		return buf[0]
	}
	px := run(0x0000)
	if pri := (px >> 24) & 7; pri != 2 {
		t.Errorf("RBG0 bitmap special bits clear: priority %d, want 2", pri)
	}
	if px&layerCCBit != 0 {
		t.Error("RBG0 bitmap special CC clear: CC bit set")
	}
	px = run(0x0030)
	if pri := (px >> 24) & 7; pri != 3 {
		t.Errorf("RBG0 bitmap special priority set: priority %d, want 3", pri)
	}
	if px&layerCCBit == 0 {
		t.Error("RBG0 bitmap special CC set: CC bit clear")
	}

	v := setupRBG0BitmapParamAB(t)
	v.regs[vdp2PRIR] = 0x0001
	v.regs[vdp2SFPRMD] = 0x0100
	clear(buf)
	renderTestRBG0(v, buf)
	expectTransparent(t, v, buf, 0, 0, "RBG0 bitmap effective priority 0")

	v = setupRBG1BitmapScene(t)
	v.regs[vdp2PRINA] = 0x0001
	v.regs[vdp2SFPRMD] = 0x0001
	clear(buf)
	renderTestRBG1(v, buf)
	expectTransparent(t, v, buf, 0, 0, "RBG1 bitmap effective priority 0")
}

// TestRBG0RPMD2SwitchedBCoefficient verifies that a dot switched to
// parameter B by RPMD mode 2 applies parameter B's own coefficient
// mode (kx only, Xp), line color enable, and per-dot bank-gated read,
// in cell and bitmap modes.
func TestRBG0RPMD2SwitchedBCoefficient(t *testing.T) {
	for _, surf := range []struct {
		name  string
		setup func(t *testing.T) *VDP2
	}{
		{"cell", setupRBG0ParamAB},
		{"bitmap", func(t *testing.T) *VDP2 {
			v := setupRBG0BitmapParamAB(t)
			writeBitmapRow8Blue(v)
			return v
		}},
	} {
		base := func(t *testing.T, ktctlB uint16) *VDP2 {
			v := surf.setup(t)
			v.regs[vdp2RPMD] = 0x0002
			v.regs[vdp2KTCTL] = 0x0001 | 0x0100 | ktctlB
			v.regs[vdp2KTAOF] = 0x0000
			writeRBGParamBIdentity(v, 0)
			// Parameter A: every dot's coefficient has MSB=1 (switch).
			writeRotParam32(v, 0x10000, 0x54, 0x6000, 0x0000)
			writeRotParam32(v, 0x10000, 0x58, 0x0000, 0x0000)
			writeVRAM16(v, 0x18000, 0x8000)
			writeVRAM16(v, 0x18002, 0x0000)
			// Parameter B table at 0x18100.
			writeRotParam32(v, 0x10080, 0x54, 0x6040, 0x0000)
			writeRotParam32(v, 0x10080, 0x58, 0x0000, 0x0000)
			return v
		}
		buf := make([]uint32, 352*256)

		t.Run(surf.name+" B mode 1 kx and KLCE", func(t *testing.T) {
			v := base(t, 0x0400|0x1000)
			writeVRAM16(v, 0x18100, 0x2502) // lc 0x25, kx 2.0
			writeVRAM16(v, 0x18102, 0x0000)
			clear(buf)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "origin")
			expectRGB(t, v, buf, 8, 0, 0, 255, 0, "(8,0) -> map 16 (kx doubled)")
			expectRGB(t, v, buf, 0, 4, 255, 0, 0, "(0,4) -> map row 4 (ky unchanged)")
			if lc := v.rbg0LCBuf[0]; lc != 0xA5 {
				t.Errorf("rbg0LCBuf[0] = 0x%02X, want 0xA5", lc)
			}
		})
		t.Run(surf.name+" B mode 2 ky", func(t *testing.T) {
			v := base(t, 0x0800)
			writeVRAM16(v, 0x18100, 0x0002) // ky 2.0
			writeVRAM16(v, 0x18102, 0x0000)
			clear(buf)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "origin")
			expectRGB(t, v, buf, 8, 0, 255, 0, 0, "(8,0) -> map 8 (kx unchanged)")
			expectRGB(t, v, buf, 0, 4, 0, 0, 255, "(0,4) -> map row 8 (ky doubled)")
		})
		t.Run(surf.name+" B mode 3 Xp", func(t *testing.T) {
			v := base(t, 0x0C00)
			writeVRAM16(v, 0x18100, 0x0000) // Xp +16
			writeVRAM16(v, 0x18102, 0x1000)
			clear(buf)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, 0, 255, 0, "origin -> map 16")
		})
		t.Run(surf.name+" B per-dot bank read", func(t *testing.T) {
			v := base(t, 0)
			v.regs[vdp2RAMCTL] = 0x0001
			writeRotParam32(v, 0x10080, 0x5C, 0x0001, 0x0000) // dKAx_B = 1.0
			writeVRAM16(v, 0x18100, 0x0001)
			writeVRAM16(v, 0x18102, 0x0000)
			writeVRAM16(v, 0x18104, 0x8001)
			writeVRAM16(v, 0x18106, 0x0000)
			clear(buf)
			renderTestRBG0(v, buf)
			expectRGB(t, v, buf, 0, 0, 255, 0, 0, "dot 0")
			expectTransparent(t, v, buf, 1, 0, "dot 1 reads its own B entry")
		})
	}
}

// setLSMD3 puts the VDP2 into double-density interlace (TVMD LSMD=11)
// on the given field and latches the mode-entry frame, after which the
// per-field render can be tested.
func setLSMD3(v *VDP2, odd bool) {
	v.regs[vdp2TVMD] = 0x80C0
	v.recalcTiming()
	v.oddField = odd
	v.BeginFrame()
}

// TestRBGScreenOverLSMD3 verifies that in double-density interlace the
// screen-over character repeats in displayed-line space (field line y
// is displayed line 2y+field), and the rotation parameter window's Y
// range is compared in displayed lines, for RBG0 and RBG1.
func TestRBGScreenOverLSMD3(t *testing.T) {
	for _, tc := range rbgScreenOverCases() {
		t.Run(tc.name+" over pattern rows", func(t *testing.T) {
			v := tc.setup(t)
			v.regs[vdp2PLSZ] = 1 << tc.plszSh
			writeRotParam32(v, tc.xstBase, 0x00, rbgXstNeg8, 0x0000)
			writeRBGTestOverTile(v, 0x403)
			v.regs[tc.pnc] = 0x0001
			v.regs[tc.ovpn] = 0x1003
			setLSMD3(v, true)
			buf := make([]uint32, 352*256)
			tc.render(v, buf)
			// Field 1: line 0 is displayed line 1 (row 1: blue), line 2
			// is displayed line 5 (row 5: red).
			expectRGB(t, v, buf, 0, 0, 0, 0, 255, "field 1 line 0 -> tile row 1")
			expectRGB(t, v, buf, 0, 2, 255, 0, 0, "field 1 line 2 -> tile row 5")
			setLSMD3(v, false)
			clear(buf)
			tc.render(v, buf)
			// Field 0: line 1 is displayed line 2 (row 2: blue), line 2
			// is displayed line 4 (row 4: red).
			expectRGB(t, v, buf, 0, 1, 0, 0, 255, "field 0 line 1 -> tile row 2")
			expectRGB(t, v, buf, 0, 2, 255, 0, 0, "field 0 line 2 -> tile row 4")
		})
	}

	t.Run("RP window Y in displayed lines", func(t *testing.T) {
		v := setupRBG0ParamAB(t)
		v.regs[vdp2RPMD] = 0x0003
		v.regs[vdp2WCTLD] = 0x0002
		v.regs[vdp2WPSX0] = 0
		v.regs[vdp2WPEX0] = 20
		v.regs[vdp2WPSY0] = 0
		v.regs[vdp2WPEY0] = 1 // displayed lines 0..1
		setLSMD3(v, true)
		buf := make([]uint32, 352*256)
		renderTestRBG0(v, buf)
		expectRGB(t, v, buf, 3, 0, 0, 255, 0, "field 1 line 0 = displayed 1: inside -> B")
		expectRGB(t, v, buf, 3, 1, 255, 0, 0, "field 1 line 1 = displayed 3: outside -> A")
	})
}
