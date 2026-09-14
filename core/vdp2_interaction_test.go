package core

import "testing"

// setupSixLayerScene builds a scene with every layer competing on one
// row, for the composite interaction tests:
//
//	sprite  yellow, priority 6 (PRISB) at x 0-1 with the frame buffer MSB
//	        set (sprite window bit), priority 1 (PRISA) at x 4
//	NBG0    green,   priority 5, x 0-7, masked by W0 (x 0-4)
//	RBG0    magenta, priority 4, x 0-15, masked by W0
//	NBG1    red,     priority 3, x 0-7, masked by W0 AND W1 (x 2-4)
//	NBG2    blue,    priority 3, x 0-7 (loses the tie to NBG1)
//	NBG3    white,   priority 2, x 0-7, masked by the sprite window
//
// W0 covers x 0-4 and W1 x 2-6 on lines 0-15 (displayed lines). The
// frame buffer pattern repeats on rows 0-7. The three-layer fixture's
// 8bpp cells overlap at 32-byte spacing, so only cell rows 0-3 hold
// each layer's own color; assertions stay on lines 0-3.
func setupSixLayerScene(t *testing.T) (*VDP2, vdp1FBView) {
	t.Helper()
	v := setupThreeNBGLayers(t, 5, 3, 3)

	// NBG3 white at page 12.
	v.regs[vdp2BGON] |= 0x0008
	v.regs[vdp2CHCTLB] |= 0x0020
	v.regs[vdp2PNCN3] = 0x0000
	v.regs[vdp2MPABN3] = 0x000C
	v.regs[vdp2MPCDN3] = 0x000C
	v.regs[vdp2PRINB] |= 2 << 8
	writeVRAM16(v, 0x30000, 0x0000)
	writeVRAM16(v, 0x30002, 0x0008)
	for i := 0; i < 64; i++ {
		v.vram[0x100+i] = 40
	}
	writeCRAM16Test(v, 40, 0x7FFF)

	// RBG0 magenta, 16-color cells on page 1, identity rotation with the
	// parameter table at 0x14000.
	v.regs[vdp2BGON] |= 1 << 4
	v.regs[vdp2PNCR] = 0x0000
	v.regs[vdp2PLSZ] = 0x0000
	v.regs[vdp2MPOFR] = 0x0000
	for i := 0; i < 8; i++ {
		v.regs[vdp2MPABRA+i] = 0x0101
	}
	v.regs[vdp2PRIR] = 0x0004
	v.regs[vdp2CRAOFB] = 0x0000
	v.regs[vdp2RPMD] = 0x0000
	v.regs[vdp2RPRCTL] = 0x0000
	v.regs[vdp2KTCTL] = 0x0000
	v.regs[vdp2RPTAU] = 0x0000
	v.regs[vdp2RPTAL] = 0xA000
	p := uint32(0x14000)
	writeRotParam32(v, p, 0x1C, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x2C, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x10, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x14, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x4C, 0x0001, 0x0000)
	writeRotParam32(v, p, 0x50, 0x0001, 0x0000)
	writeVRAM16(v, 0x4000, 0x0001)
	writeVRAM16(v, 0x4002, 0x0404)
	writeVRAM16(v, 0x4004, 0x0001)
	writeVRAM16(v, 0x4006, 0x0404)
	writeRBGTestTile(v, 0x404, 0x7)
	writeCRAM16Test(v, 23, 0x7C1F) // palette 1 entry 7: magenta

	// Sprite: type 0 (PR bits 15:14), PRISA low = register 0 = 1, PRISB
	// low = register 2 = 6, CRAM 5 yellow.
	v.regs[vdp2SPCTL] = 0x0000
	v.regs[vdp2PRISA] = 0x0001
	v.regs[vdp2PRISB] = 0x0006
	writeCRAM16Test(v, 5, 0x03FF)
	fb := vdp1FBView{data: make([]byte, 512*256*2), width: 512, height: 256}
	for y := 0; y < 8; y++ {
		setFBPixel16(fb, 0, y, 0x8005)
		setFBPixel16(fb, 1, y, 0x8005)
		setFBPixel16(fb, 4, y, 0x0005)
	}

	// Windows.
	v.regs[vdp2WPSX0], v.regs[vdp2WPEX0] = 0, 8
	v.regs[vdp2WPSY0], v.regs[vdp2WPEY0] = 0, 15
	v.regs[vdp2WPSX1], v.regs[vdp2WPEX1] = 4, 12
	v.regs[vdp2WPSY1], v.regs[vdp2WPEY1] = 0, 15
	v.regs[vdp2WCTLA] = 0x8A02 // NBG0: W0; NBG1: W0 AND W1
	v.regs[vdp2WCTLB] = 0x2000 // NBG3: sprite window
	v.regs[vdp2WCTLC] = 0x0002 // RBG0: W0
	return v, fb
}

var (
	colYellow  = [3]int{255, 255, 0}
	colGreen   = [3]int{0, 255, 0}
	colRed     = [3]int{255, 0, 0}
	colBlue    = [3]int{0, 0, 255}
	colWhite   = [3]int{255, 255, 255}
	colMagenta = [3]int{255, 0, 255}
)

// expectRow asserts the output row y matches the expected colors for x
// 0..len(want)-1.
func expectRow(t *testing.T, v *VDP2, y int, want [][3]int, what string) {
	t.Helper()
	for x, c := range want {
		expectOut(t, v, x, y, c[0], c[1], c[2], 0, what)
	}
}

// TestCompositeSixLayersWithWindows verifies the full priority, window,
// and sprite-window interaction on one row, and that per-line register
// writes (priority and sprite priority) reshape the layer order from the
// next line: from line 2 NBG3 rises to priority 7 and the window-bit
// sprites drop to priority 0, so NBG3 shows everywhere except where the
// sprite window masks it (there NBG1 shows).
func TestCompositeSixLayersWithWindows(t *testing.T) {
	v, fb := setupSixLayerScene(t)
	v.BeginFrame()
	for y := 0; y < 2; y++ {
		v.RenderLine(y, fb)
	}
	v.Write(uint32(vdp2PRINB*2), 0x0703)
	v.Write(uint32(vdp2PRISB*2), 0x0000)
	for y := 2; y < 4; y++ {
		v.RenderLine(y, fb)
	}
	before := [][3]int{colYellow, colYellow, colBlue, colBlue, colBlue, colGreen, colGreen, colGreen, colMagenta, colMagenta}
	expectRow(t, v, 0, before, "row 0")
	expectRow(t, v, 1, before, "row 1")
	after := [][3]int{colRed, colRed, colWhite, colWhite, colWhite, colWhite, colWhite, colWhite, colMagenta, colMagenta}
	expectRow(t, v, 2, after, "row 2 after the priority writes")
	expectRow(t, v, 3, after, "row 3")
}

// TestCompositeSixLayersHiRes verifies the same scene in 640-dot hi-res:
// window coordinates are in dots, the 512-wide sprite frame buffer is
// sampled at x/2, and RBG0 advances one map dot per dot pair.
func TestCompositeSixLayersHiRes(t *testing.T) {
	v, fb := setupSixLayerScene(t)
	v.regs[vdp2TVMD] = 0x8002 // 640x224
	v.recalcTiming()
	renderTestFrameFB(v, fb)
	// Sprite x 0-3 (frame buffer 0-1), W0 x 0-8, W1 x 4-12. At x 8 the
	// NBG tiles have ended, NBG0/RBG0 are masked by W0 and NBG1 by W0 AND
	// W1, leaving the priority-1 sprite (frame buffer x 4). RBG0 spans
	// x 0-31.
	want := [][3]int{colYellow, colYellow, colYellow, colYellow, colBlue, colBlue, colBlue, colBlue, colYellow, colMagenta, colMagenta}
	expectRow(t, v, 0, want, "hi-res row 0")
}

// TestCompositeSixLayersLSMD3 verifies the scene in double-density
// interlace: window Y bounds are displayed lines with bit 0 dropped, so
// WPEY 1 covers displayed line 0 only. Field 0 line 0 (displayed 0) is
// masked, field 1 line 0 (displayed 1) renders unmasked.
func TestCompositeSixLayersLSMD3(t *testing.T) {
	v, fb := setupSixLayerScene(t)
	v.regs[vdp2WPEY0] = 1
	v.regs[vdp2WPEY1] = 1
	setLSMD3(v, false)
	renderTestFrameFB(v, fb)
	setLSMD3(v, true)
	renderTestFrameFB(v, fb)
	masked := [][3]int{colYellow, colYellow, colBlue, colBlue, colBlue, colGreen, colGreen, colGreen, colMagenta, colMagenta}
	expectRow(t, v, 0, masked, "field 0 line 0 (displayed 0)")
	unmasked := [][3]int{colYellow, colYellow, colGreen, colGreen, colGreen, colGreen, colGreen, colGreen, colMagenta, colMagenta}
	expectRow(t, v, 1, unmasked, "field 1 line 0 (displayed 1)")
}

// TestCompositeCCWindowWithShadow verifies color calculation gated by the
// CC window on the same row as a normal shadow sprite: inside the CC
// window the top layer shows unblended, outside it blends with the
// second image, and the shadow halves the blended result.
func TestCompositeCCWindowWithShadow(t *testing.T) {
	v := setupTwoNBGLayers(t, 5, 3) // NBG0 green over NBG1 red
	v.regs[vdp2CCCTL] = 0x0001
	v.regs[vdp2CCRNA] = 16
	v.regs[vdp2WPSX0], v.regs[vdp2WPEX0] = 0, 8 // x 0..4
	v.regs[vdp2WPSY0], v.regs[vdp2WPEY0] = 0, 15
	v.regs[vdp2WCTLD] = 0x0200 // CC window: W0
	v.regs[vdp2SDCTL] = 0x0001 // shadow onto NBG0
	v.regs[vdp2SPCTL] = 0x0000
	v.regs[vdp2PRISA] = 0x0007
	fb := vdp1FBView{data: make([]byte, 512*256*2), width: 512, height: 256}
	setFBPixel16(fb, 6, 0, 0x07FE) // type 0 normal shadow
	renderTestFrameFB(v, fb)
	expectOut(t, v, 0, 0, 0, 255, 0, 0, "inside CC window: no blend")
	expectOut(t, v, 4, 0, 0, 255, 0, 0, "inside CC window: no blend")
	expectOut(t, v, 5, 0, 135, 119, 0, 2, "outside CC window: blend")
	expectOut(t, v, 6, 0, 67, 59, 0, 2, "shadow over the blend")
	expectOut(t, v, 7, 0, 135, 119, 0, 2, "outside CC window: blend")
}

// TestCompositeBackScreenTableOffsetShadowWindow verifies a per-line back
// screen table with color offset showing through a window-masked layer,
// with a normal shadow sprite darkening the back screen.
func TestCompositeBackScreenTableOffsetShadowWindow(t *testing.T) {
	v := setupNBG0FullTile(t) // red tile x 0..7
	v.regs[vdp2BKTAU] = 0x8002
	v.regs[vdp2BKTAL] = 0xC000 // per-line table at 0x58000
	for line := uint32(0); line < 8; line++ {
		c := uint16(0xFC00) // even lines blue
		if line&1 == 1 {
			c = 0x83E0 // odd lines green
		}
		writeVRAM16(v, 0x58000+line*2, c)
	}
	v.regs[vdp2CLOFEN] = 1 << 5
	v.regs[vdp2COAR], v.regs[vdp2COAG], v.regs[vdp2COAB] = 10, 0, 0
	v.regs[vdp2WPSX0], v.regs[vdp2WPEX0] = 0, 8 // x 0..4
	v.regs[vdp2WPSY0], v.regs[vdp2WPEY0] = 0, 15
	v.regs[vdp2WCTLA] = 0x0002 // NBG0 masked inside W0
	v.regs[vdp2SDCTL] = 1 << 5 // shadow onto the back screen
	v.regs[vdp2SPCTL] = 0x0000
	v.regs[vdp2PRISA] = 0x0007
	fb := vdp1FBView{data: make([]byte, 512*256*2), width: 512, height: 256}
	setFBPixel16(fb, 2, 0, 0x07FE)
	setFBPixel16(fb, 2, 1, 0x07FE)
	renderTestFrameFB(v, fb)
	expectOut(t, v, 0, 0, 10, 0, 255, 0, "line 0 back screen blue + offset")
	expectOut(t, v, 2, 0, 5, 0, 127, 1, "line 0 shadowed back screen")
	expectOut(t, v, 0, 1, 10, 255, 0, 0, "line 1 back screen green + offset")
	expectOut(t, v, 2, 1, 5, 127, 0, 1, "line 1 shadowed back screen")
	expectOut(t, v, 6, 0, 255, 0, 0, 0, "outside the window: NBG0")
}

// TestCompositeSixLayers704 verifies the six-layer scene at 704 dots: the
// same dot-unit window coordinates, sprite x halving, and RBG0 dot pairs
// as 640, on the wider row.
func TestCompositeSixLayers704(t *testing.T) {
	v, fb := setupSixLayerScene(t)
	v.regs[vdp2TVMD] = 0x8003 // 704x224
	v.recalcTiming()
	if v.activeWidth != 704 {
		t.Fatalf("active width %d, want 704", v.activeWidth)
	}
	renderTestFrameFB(v, fb)
	want := [][3]int{colYellow, colYellow, colYellow, colYellow, colBlue, colBlue, colBlue, colBlue, colYellow, colMagenta, colMagenta}
	expectRow(t, v, 0, want, "704-dot row 0")
	expectOut(t, v, 31, 0, 255, 0, 255, 0, "RBG0 dot pairs reach x 31")
	expectOut(t, v, 32, 0, 0, 0, 0, 0, "past the RBG0 cells")
}

// TestPAL256LinePixels verifies the composite on a PAL 256-line frame: a
// bitmap layer covers all 256 rows, the per-line back screen table is
// read at line 250, and a window with a Y range of 240-255 masks the
// layer on those rows.
func TestPAL256LinePixels(t *testing.T) {
	v := newTestVDP2()
	v.SetPAL(true)
	v.regs[vdp2TVMD] = 0x8020 // 320x256
	v.recalcTiming()
	if v.activeLines != 256 {
		t.Fatalf("active lines %d, want 256", v.activeLines)
	}
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0012 // 256-color bitmap 512x256
	v.regs[vdp2PRINA] = 0x0001
	for i := 0; i < 512*256; i++ {
		v.vram[i] = 10
	}
	writeCRAM16Test(v, 10, 0x03E0) // green
	v.regs[vdp2BKTAU] = 0x8002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000+250*2, 0xFC00) // line 250 blue, others black
	v.regs[vdp2WPSX0], v.regs[vdp2WPEX0] = 0, 640
	v.regs[vdp2WPSY0], v.regs[vdp2WPEY0] = 240, 255
	v.regs[vdp2WCTLA] = 0x0002
	renderTestFrame(v)
	expectOut(t, v, 0, 239, 0, 255, 0, 0, "line 239: bitmap")
	expectOut(t, v, 0, 240, 0, 0, 0, 0, "line 240: masked, black back screen")
	expectOut(t, v, 0, 250, 0, 0, 255, 0, "line 250: masked, blue back screen entry")
	expectOut(t, v, 0, 255, 0, 0, 0, 0, "line 255: masked")
}

// setLSMD2 puts the VDP2 into single-density interlace (TVMD LSMD=10).
func setLSMD2(v *VDP2) {
	v.regs[vdp2TVMD] = 0x8080
	v.recalcTiming()
	v.BeginFrame()
}

// TestLSMD2LineTableIndexHalving verifies that in single-density interlace
// the per-line tables are indexed by y/2 (each entry covers a line pair):
// the W0 line window on a scroll layer and the rotation parameter window
// on RBG0.
func TestLSMD2LineTableIndexHalving(t *testing.T) {
	// Line window table at 0x40000: entry 0 masks x 0..4, entry 1 is
	// excluded, entry 2 masks x 0..4.
	writeTable := func(v *VDP2) {
		writeVRAM16(v, 0x40000, 0)
		writeVRAM16(v, 0x40002, 8)
		writeVRAM16(v, 0x40004, 20)
		writeVRAM16(v, 0x40006, 0)
		writeVRAM16(v, 0x40008, 0)
		writeVRAM16(v, 0x4000A, 8)
	}

	v := setupNBG0FullTile(t)
	v.regs[vdp2BKTAU] = 0x0002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000, 0xFC00)
	v.regs[vdp2WCTLA] = 0x0002
	v.regs[vdp2WPSY0], v.regs[vdp2WPEY0] = 0, 15
	v.regs[vdp2LWTA0U] = 0x8002
	v.regs[vdp2LWTA0L] = 0x0000
	writeTable(v)
	setLSMD2(v)
	renderTestFrame(v)
	expectOut(t, v, 2, 0, 0, 0, 255, 0, "lines 0-1 use entry 0: masked")
	expectOut(t, v, 2, 1, 0, 0, 255, 0, "lines 0-1 use entry 0: masked")
	expectOut(t, v, 2, 2, 255, 0, 0, 0, "lines 2-3 use entry 1: excluded")
	expectOut(t, v, 2, 3, 255, 0, 0, 0, "lines 2-3 use entry 1: excluded")
	expectOut(t, v, 2, 4, 0, 0, 255, 0, "lines 4-5 use entry 2: masked")

	// Rotation parameter window with the same table: parameter B inside.
	v = setupRBG0ParamAB(t)
	v.regs[vdp2RPMD] = 0x0003
	v.regs[vdp2WCTLD] = 0x0002
	v.regs[vdp2LWTA0U] = 0x8002
	v.regs[vdp2LWTA0L] = 0x0000
	writeTable(v)
	setLSMD2(v)
	buf := make([]uint32, 352*256)
	renderTestRBG0(v, buf)
	expectRGB(t, v, buf, 2, 1, 0, 255, 0, "RP window lines 0-1: B")
	expectRGB(t, v, buf, 2, 2, 255, 0, 0, "RP window lines 2-3: A")
	expectRGB(t, v, buf, 2, 5, 0, 255, 0, "RP window lines 4-5: B")
}
