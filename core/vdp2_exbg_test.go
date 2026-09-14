package core

import "testing"

// exbgTestFrame builds a w x h external frame whose pixel at (x, y) is
// packed as R = x & 0xFF, G = y & 0xFF, B = x >> 8, so a sampled pixel
// identifies its source position.
func exbgTestFrame(w, h int) []uint32 {
	rgb := make([]uint32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rgb[y*w+x] = uint32(x&0xFF)<<16 | uint32(y&0xFF)<<8 | uint32(x>>8)
		}
	}
	return rgb
}

// exbgTestPixel is the packed value exbgTestFrame stores at (x, y).
func exbgTestPixel(x, y int) uint32 {
	return uint32(x&0xFF)<<16 | uint32(y&0xFF)<<8 | uint32(x>>8)
}

// fakeEXBGUnsized provides a frame with no display window (the whole
// frame maps 1:1 onto the screen).
type fakeEXBGUnsized struct {
	rgb  []uint32
	w, h int
}

func (f *fakeEXBGUnsized) MpegFrameRGB() ([]uint32, int, int, bool) {
	return f.rgb, f.w, f.h, true
}

// fakeEXBGWindow provides a frame with a fully configurable display
// window: decoder placement (dx, dy), window size (dw, dh), source
// offset (sx, sy), and source step per screen pixel in thousandths.
type fakeEXBGWindow struct {
	rgb                            []uint32
	w, h                           int
	dx, dy, dw, dh, sx, sy, rx, ry int
}

func (f *fakeEXBGWindow) MpegFrameRGB() ([]uint32, int, int, bool) {
	return f.rgb, f.w, f.h, true
}

func (f *fakeEXBGWindow) MpegWindow() (dx, dy, dw, dh, sx, sy, ratX, ratY int, sized bool) {
	return f.dx, f.dy, f.dw, f.dh, f.sx, f.sy, f.rx, f.ry, true
}

// setupEXBG enables EXBG (EXTEN bit 0) in NBG1's slot at priority 5 on
// a 320x224 NTSC frame with the given source.
func setupEXBG(t *testing.T, src exbgSource) *VDP2 {
	t.Helper()
	v := newTestVDP2()
	v.regs[vdp2EXTEN] = 0x0001
	v.regs[vdp2PRINA] = 0x0500 // NBG1 slot priority 5
	v.SetEXBGSource(src)
	return v
}

// exbgLayerPixel returns the NBG1 slot layer buffer pixel at (x, y).
func exbgLayerPixel(v *VDP2, x, y int) uint32 {
	return v.layerBufs[1][y*v.frame.width+x]
}

// TestEXBGUnsizedMapping verifies an external frame without a display
// window fills the NBG1 slot 1:1 with the slot's priority, the CC flag
// follows CCCTL's NBG1 bit, and pixels past the frame width are
// transparent.
func TestEXBGUnsizedMapping(t *testing.T) {
	src := &fakeEXBGUnsized{rgb: exbgTestFrame(300, 224), w: 300, h: 224}
	v := setupEXBG(t, src)
	renderTestFrame(v)

	base := uint32(5) << 24
	for _, p := range [][2]int{{0, 0}, {17, 3}, {299, 100}, {150, 223}} {
		want := base | exbgTestPixel(p[0], p[1])
		if got := exbgLayerPixel(v, p[0], p[1]); got != want {
			t.Errorf("pixel(%d,%d) = 0x%08X, want 0x%08X", p[0], p[1], got, want)
		}
	}
	if got := exbgLayerPixel(v, 300, 0); got != 0 {
		t.Errorf("pixel(300,0) past the frame = 0x%08X, want transparent", got)
	}

	v.regs[vdp2CCCTL] = 1 << 1
	renderTestFrame(v)
	if got := exbgLayerPixel(v, 0, 0); got&layerCCBit == 0 {
		t.Errorf("CCCTL NBG1 set: pixel(0,0) = 0x%08X, want CC bit", got)
	}
}

// TestEXBGPriorityZeroDisables verifies EXBG is off when the NBG1 slot
// priority is zero and when EXTEN bit 0 is clear.
func TestEXBGPriorityZeroDisables(t *testing.T) {
	src := &fakeEXBGUnsized{rgb: exbgTestFrame(320, 224), w: 320, h: 224}
	v := setupEXBG(t, src)
	v.regs[vdp2PRINA] = 0x0000
	renderTestFrame(v)
	if v.frame.exbgOn {
		t.Error("NBG1 slot priority 0: EXBG should be off")
	}
	if got := exbgLayerPixel(v, 5, 5); got != 0 {
		t.Errorf("priority 0: pixel(5,5) = 0x%08X, want transparent", got)
	}

	v = setupEXBG(t, src)
	v.regs[vdp2EXTEN] = 0x0000
	renderTestFrame(v)
	if v.frame.exbgOn {
		t.Error("EXTEN clear: EXBG should be off")
	}
}

// TestEXBGReplacesNBG1 verifies EXBG takes over NBG1's slot: with EXTEN
// set the slot shows the external frame instead of NBG1's cell data.
func TestEXBGReplacesNBG1(t *testing.T) {
	src := &fakeEXBGUnsized{rgb: exbgTestFrame(320, 224), w: 320, h: 224}
	v := setupEXBG(t, src)
	// NBG1: 16-color 1x1 cells, 2-word names, plane A page 0, a red tile
	// at cell (0,0).
	v.regs[vdp2BGON] |= 0x0002
	v.regs[vdp2CHCTLA] = 0x0000
	v.regs[vdp2PNCN1] = 0x0000
	v.regs[vdp2MPABN1] = 0x0000
	v.regs[vdp2MPCDN1] = 0x0000
	writeVRAM16(v, 0, 0x0001)
	writeVRAM16(v, 2, 0x0400)
	writeRBGTestTile(v, 0x400, rbgTestRed)
	writeRBGTestPalette(v)

	v.regs[vdp2EXTEN] = 0x0000
	renderTestFrame(v)
	if got := exbgLayerPixel(v, 0, 0); uint8(got>>16) != 255 || uint8(got>>8) != 0 {
		t.Fatalf("EXTEN clear: NBG1 pixel(0,0) = 0x%08X, want red", got)
	}

	v.regs[vdp2EXTEN] = 0x0001
	renderTestFrame(v)
	if got := exbgLayerPixel(v, 0, 0); got != uint32(5)<<24|exbgTestPixel(0, 0) {
		t.Errorf("EXTEN set: slot pixel(0,0) = 0x%08X, want the external frame", got)
	}
	if v.frame.nbgOn[1] {
		t.Error("EXTEN set: NBG1 should be suppressed")
	}
}

// TestEXBGSizedWindow verifies the display-window mapping: the window's
// decoder placement converts to frame coordinates (X+1, Y-(raster-lines)/2
// on 224-line NTSC), the window shows source pixels from the source
// offset, and everything outside the window is transparent.
func TestEXBGSizedWindow(t *testing.T) {
	src := &fakeEXBGWindow{
		rgb: exbgTestFrame(352, 240), w: 352, h: 240,
		dx: 23, dy: 49, dw: 160, dh: 120, sx: 10, sy: 20, rx: 1000, ry: 1000,
	}
	v := setupEXBG(t, src)
	renderTestFrame(v)
	if v.exbgDX != 24 || v.exbgDY != 41 {
		t.Fatalf("placement = (%d,%d), want (24,41)", v.exbgDX, v.exbgDY)
	}
	base := uint32(5) << 24
	check := func(x, y int, want uint32) {
		t.Helper()
		if got := exbgLayerPixel(v, x, y); got != want {
			t.Errorf("pixel(%d,%d) = 0x%08X, want 0x%08X", x, y, got, want)
		}
	}
	check(24, 41, base|exbgTestPixel(10, 20))
	check(24+159, 41, base|exbgTestPixel(169, 20))
	check(24, 41+119, base|exbgTestPixel(10, 139))
	check(23, 41, 0)
	check(24+160, 41, 0)
	check(24, 40, 0)
	check(24, 41+120, 0)
}

// TestEXBGWindowRatios verifies the source step ratios: 2000 skips every
// other source pixel, 500 repeats each source pixel twice, in X and Y.
func TestEXBGWindowRatios(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rx, ry int
		// source position sampled at window pixel (3, 3)
		sx, sy int
	}{
		{"2x step", 2000, 2000, 6, 6},
		{"half step", 500, 500, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeEXBGWindow{
				rgb: exbgTestFrame(352, 240), w: 352, h: 240,
				dx: 23, dy: 49, dw: 160, dh: 120, rx: tc.rx, ry: tc.ry,
			}
			v := setupEXBG(t, src)
			renderTestFrame(v)
			want := uint32(5)<<24 | exbgTestPixel(tc.sx, tc.sy)
			if got := exbgLayerPixel(v, 24+3, 41+3); got != want {
				t.Errorf("window pixel(3,3) = 0x%08X, want 0x%08X", got, want)
			}
		})
	}
}

// TestEXBGWindowClippedLeftAndSourceEnd verifies a window whose left
// edge lies off-screen starts at screen x 0 with the source advanced by
// the hidden width, and window pixels past the source row's end or
// past the source height are transparent.
func TestEXBGWindowClippedLeftAndSourceEnd(t *testing.T) {
	src := &fakeEXBGWindow{
		rgb: exbgTestFrame(100, 60), w: 100, h: 60,
		dx: -31, dy: 49, dw: 160, dh: 120, rx: 1000, ry: 1000,
	}
	v := setupEXBG(t, src)
	renderTestFrame(v)
	if v.exbgDX != -30 {
		t.Fatalf("placement X = %d, want -30", v.exbgDX)
	}
	base := uint32(5) << 24
	if got := exbgLayerPixel(v, 0, 41); got != base|exbgTestPixel(30, 0) {
		t.Errorf("pixel(0,41) = 0x%08X, want source column 30", got)
	}
	// Window pixels 0..129 map to source columns 30..159; the source
	// is 100 wide, so screen x 70 and beyond are transparent.
	if got := exbgLayerPixel(v, 69, 41); got != base|exbgTestPixel(99, 0) {
		t.Errorf("pixel(69,41) = 0x%08X, want source column 99", got)
	}
	if got := exbgLayerPixel(v, 70, 41); got != 0 {
		t.Errorf("pixel(70,41) = 0x%08X, want transparent past the source row", got)
	}
	// Source is 60 rows high: window row 60 (screen y 101) is transparent.
	if got := exbgLayerPixel(v, 0, 41+59); got != base|exbgTestPixel(30, 59) {
		t.Errorf("pixel(0,100) = 0x%08X, want source row 59", got)
	}
	if got := exbgLayerPixel(v, 0, 41+60); got != 0 {
		t.Errorf("pixel(0,101) = 0x%08X, want transparent past the source height", got)
	}
}
