package core

import (
	"testing"

	"github.com/user-none/erings/core/compute/screen"
)

// exbgTestFrame builds a w x h external frame whose pixel at (x, y) is
// the decoded color R = x & 0xFF, G = y & 0xFF, B = x >> 8, so a
// sampled pixel identifies its source position.
func exbgTestFrame(w, h int) []uint32 {
	rgb := make([]uint32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			rgb[y*w+x] = exbgTestPixel(x, y)
		}
	}
	return rgb
}

// exbgTestPixel is the decoded color exbgTestFrame stores at (x, y).
func exbgTestPixel(x, y int) uint32 {
	return screen.PackColor(uint32(x&0xFF), uint32(y&0xFF), uint32(x>>8))
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
func exbgLayerPixel(v *VDP2, swr *softwareRenderer, x, y int) uint32 {
	return renderedLayerDot(v, swr, 1, x, y)
}

// TestEXBGUnsizedMapping verifies an external frame without a display
// window fills the NBG1 slot 1:1 with the slot's priority, the CC flag
// follows CCCTL's NBG1 bit, and pixels past the frame width are
// transparent.
func TestEXBGUnsizedMapping(t *testing.T) {
	src := &fakeEXBGUnsized{rgb: exbgTestFrame(300, 224), w: 300, h: 224}
	v := setupEXBG(t, src)
	swr := newSoftwareRenderer(v)
	renderTestFrame(v, swr)

	base := uint32(5) << 24
	for _, p := range [][2]int{{0, 0}, {17, 3}, {299, 100}, {150, 223}} {
		want := base | exbgTestPixel(p[0], p[1])
		if got := exbgLayerPixel(v, swr, p[0], p[1]); got != want {
			t.Errorf("pixel(%d,%d) = 0x%08X, want 0x%08X", p[0], p[1], got, want)
		}
	}
	if got := exbgLayerPixel(v, swr, 300, 0); got != 0 {
		t.Errorf("pixel(300,0) past the frame = 0x%08X, want transparent", got)
	}

	v.regs[vdp2CCCTL] = 1 << 1
	renderTestFrame(v, swr)
	if got := exbgLayerPixel(v, swr, 0, 0); got&layerCCBit == 0 {
		t.Errorf("CCCTL NBG1 set: pixel(0,0) = 0x%08X, want CC bit", got)
	}
}

// TestEXBGPriorityZeroDisables verifies EXBG is off when the NBG1 slot
// priority is zero and when EXTEN bit 0 is clear.
func TestEXBGPriorityZeroDisables(t *testing.T) {
	src := &fakeEXBGUnsized{rgb: exbgTestFrame(320, 224), w: 320, h: 224}
	v := setupEXBG(t, src)
	swr := newSoftwareRenderer(v)
	v.regs[vdp2PRINA] = 0x0000
	renderTestFrame(v, swr)
	if v.frame.exbgOn {
		t.Error("NBG1 slot priority 0: EXBG should be off")
	}
	if got := exbgLayerPixel(v, swr, 5, 5); got != 0 {
		t.Errorf("priority 0: pixel(5,5) = 0x%08X, want transparent", got)
	}

	v = setupEXBG(t, src)
	swr = newSoftwareRenderer(v)
	v.regs[vdp2EXTEN] = 0x0000
	renderTestFrame(v, swr)
	if v.frame.exbgOn {
		t.Error("EXTEN clear: EXBG should be off")
	}
}

// TestEXBGReplacesNBG1 verifies EXBG takes over NBG1's slot: with EXTEN
// set the slot shows the external frame instead of NBG1's cell data.
func TestEXBGReplacesNBG1(t *testing.T) {
	src := &fakeEXBGUnsized{rgb: exbgTestFrame(320, 224), w: 320, h: 224}
	v := setupEXBG(t, src)
	swr := newSoftwareRenderer(v)
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
	renderTestFrame(v, swr)
	if got := exbgLayerPixel(v, swr, 0, 0); screen.ColorR(got) != 248 || screen.ColorG(got) != 0 {
		t.Fatalf("EXTEN clear: NBG1 pixel(0,0) = 0x%08X, want red", got)
	}

	v.regs[vdp2EXTEN] = 0x0001
	renderTestFrame(v, swr)
	if got := exbgLayerPixel(v, swr, 0, 0); got != uint32(5)<<24|exbgTestPixel(0, 0) {
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
	swr := newSoftwareRenderer(v)
	renderTestFrame(v, swr)
	if v.exbgDX != 24 || v.exbgDY != 41 {
		t.Fatalf("placement = (%d,%d), want (24,41)", v.exbgDX, v.exbgDY)
	}
	base := uint32(5) << 24
	check := func(x, y int, want uint32) {
		t.Helper()
		if got := exbgLayerPixel(v, swr, x, y); got != want {
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
			swr := newSoftwareRenderer(v)
			renderTestFrame(v, swr)
			want := uint32(5)<<24 | exbgTestPixel(tc.sx, tc.sy)
			if got := exbgLayerPixel(v, swr, 24+3, 41+3); got != want {
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
	swr := newSoftwareRenderer(v)
	renderTestFrame(v, swr)
	if v.exbgDX != -30 {
		t.Fatalf("placement X = %d, want -30", v.exbgDX)
	}
	base := uint32(5) << 24
	if got := exbgLayerPixel(v, swr, 0, 41); got != base|exbgTestPixel(30, 0) {
		t.Errorf("pixel(0,41) = 0x%08X, want source column 30", got)
	}
	// Window pixels 0..129 map to source columns 30..159; the source
	// is 100 wide, so screen x 70 and beyond are transparent.
	if got := exbgLayerPixel(v, swr, 69, 41); got != base|exbgTestPixel(99, 0) {
		t.Errorf("pixel(69,41) = 0x%08X, want source column 99", got)
	}
	if got := exbgLayerPixel(v, swr, 70, 41); got != 0 {
		t.Errorf("pixel(70,41) = 0x%08X, want transparent past the source row", got)
	}
	// Source is 60 rows high: window row 60 (screen y 101) is transparent.
	if got := exbgLayerPixel(v, swr, 0, 41+59); got != base|exbgTestPixel(30, 59) {
		t.Errorf("pixel(0,100) = 0x%08X, want source row 59", got)
	}
	if got := exbgLayerPixel(v, swr, 0, 41+60); got != 0 {
		t.Errorf("pixel(0,101) = 0x%08X, want transparent past the source height", got)
	}
}

// TestEXBGScaledBlockCorners holds the external image's scaled rows to
// its native rows at scale shifts 1 to 3: over unsized and sized
// windows, a window whose left edge lies off screen, a video wider
// than its window, and enlarged and reduced windows, the sub-dot at
// (N*x, N*y) is the native dot at (x, y). At a 1:1 ratio every
// sub-dot of a block is its native dot. Otherwise each sub-dot reads
// a video pixel between its block corner's and the next block
// corner's on each axis, and a reduced window's scaled rows read
// video columns its native rows step over.
func TestEXBGScaledBlockCorners(t *testing.T) {
	source := func(px uint32) (col, row int) {
		return int(screen.ColorR(px) | screen.ColorB(px)<<8), int(screen.ColorG(px))
	}
	between := func(v, a, b int) bool { return min(a, b) <= v && v <= max(a, b) }
	for _, tc := range []struct {
		name string
		src  exbgSource
		one  bool // 1:1 ratio
	}{
		{"unsized, video wider than the screen", &fakeEXBGUnsized{rgb: exbgTestFrame(352, 240), w: 352, h: 240}, true},
		{"sized 1:1", &fakeEXBGWindow{rgb: exbgTestFrame(352, 240), w: 352, h: 240,
			dx: 23, dy: 49, dw: 160, dh: 120, sx: 10, sy: 20, rx: 1000, ry: 1000}, true},
		{"left edge off screen", &fakeEXBGWindow{rgb: exbgTestFrame(100, 60), w: 100, h: 60,
			dx: -31, dy: 49, dw: 160, dh: 120, rx: 1300, ry: 1000}, false},
		{"enlarged", &fakeEXBGWindow{rgb: exbgTestFrame(352, 240), w: 352, h: 240,
			dx: 23, dy: 9, dw: 280, dh: 200, sx: 3, sy: 5, rx: 450, ry: 700}, false},
		{"reduced", &fakeEXBGWindow{rgb: exbgTestFrame(352, 240), w: 352, h: 240,
			dx: 40, dy: 30, dw: 120, dh: 90, sx: 7, sy: 2, rx: 2000, ry: 1500}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := setupEXBG(t, tc.src)
			swr := newSoftwareRenderer(v)
			renderTestFrame(v, swr)
			width, height := v.frame.width, v.frame.height
			native := make([]uint32, width*height)
			lit := 0
			for y := range height {
				v.exbgCopyRow(native, y, y, 0)
			}
			for _, px := range native {
				if px != 0 {
					lit++
				}
			}
			if lit == 0 {
				t.Fatal("no external image dot drawn")
			}
			nativeCols, nativeRows := make(map[int]bool), make(map[int]bool)
			for _, px := range native {
				if px != 0 {
					c, r := source(px)
					nativeCols[c], nativeRows[r] = true, true
				}
			}
			for shift := uint32(1); shift <= 3; shift++ {
				n := 1 << shift
				sw := width * n
				scaled := make([]uint32, sw*height*n)
				v.enhance.cfg.shift = shift
				for y := range height {
					for k := range n {
						v.exbgCopyRow(scaled, y*n+k, y, k)
					}
				}
				v.enhance.cfg.shift = 0
				scaledCols, scaledRows := make(map[int]bool), make(map[int]bool)
				for y := range height {
					for x := range width {
						want := native[y*width+x]
						if got := scaled[y*n*sw+x*n]; got != want {
							t.Fatalf("shift %d: sub-dot (%d, %d) = %08x, want the native %08x", shift, x*n, y*n, got, want)
						}
						for k := range n {
							for j := range n {
								got := scaled[(y*n+k)*sw+x*n+j]
								if tc.one {
									if got != want {
										t.Fatalf("shift %d: sub-dot (%d, %d) = %08x, want the native %08x", shift, x*n+j, y*n+k, got, want)
									}
									continue
								}
								if got == 0 || want == 0 || x+1 == width || y+1 == height {
									continue
								}
								right, below := native[y*width+x+1], native[(y+1)*width+x]
								if right == 0 || below == 0 {
									continue
								}
								c, r := source(got)
								scaledCols[c], scaledRows[r] = true, true
								c0, r0 := source(want)
								c1, _ := source(right)
								_, r1 := source(below)
								if !between(c, c0, c1) || !between(r, r0, r1) {
									t.Fatalf("shift %d: sub-dot (%d, %d) reads video (%d, %d), outside (%d, %d) to (%d, %d)", shift, x*n+j, y*n+k, c, r, c0, r0, c1, r1)
								}
							}
						}
					}
				}
				if tc.name == "reduced" {
					extraCols, extraRows := 0, 0
					for c := range scaledCols {
						if !nativeCols[c] {
							extraCols++
						}
					}
					for r := range scaledRows {
						if !nativeRows[r] {
							extraRows++
						}
					}
					if extraCols == 0 || extraRows == 0 {
						t.Errorf("shift %d: the scaled rows read %d video columns and %d video rows the native rows step over, want some of each", shift, extraCols, extraRows)
					}
				}
			}
		})
	}
}
