package core

import "testing"

// writeGouraudTable writes the four Gouraud entries (A, B, C, D) at
// 0x2000 and returns the GRDA value for the command.
func writeGouraudTable(v *VDP1, a, b, c, d uint16) uint16 {
	writeCmd16(v, 0x2000, a)
	writeCmd16(v, 0x2002, b)
	writeCmd16(v, 0x2004, c)
	writeCmd16(v, 0x2006, d)
	return 0x2000 / 8
}

// TestDegeneratePolygonLineGouraud verifies the single-line fallback of a
// degenerate polygon (A=D and B=C): the line is rasterized with Gouraud
// interpolation from A to B along steep and shallow lines in every
// direction, including negative X and Y steps.
func TestDegeneratePolygonLineGouraud(t *testing.T) {
	cases := []struct {
		name           string
		ax, ay, bx, by int16
	}{
		{"steep down-right", 10, 10, 13, 20},
		{"steep up-left", 13, 20, 10, 10},
		{"shallow left-down", 20, 10, 10, 13},
		{"shallow right-up", 10, 13, 20, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newDrawTestVDP1()
			grda := writeGouraudTable(v, 0x4210, 0x421F, 0x421F, 0x4210) // A: R+0, B: R+15
			writePolygon(v, 0x00, tc.ax, tc.ay, tc.bx, tc.by, tc.bx, tc.by, tc.ax, tc.ay, 0x8000)
			writeCmd16(v, 0x04, 0x0004) // Gouraud
			writeCmd16(v, 0x1C, grda)
			writeDrawEnd(v, 0x20)
			v.VBlankIn()
			drainDrawing(v)
			if got := readFBPixel(v, int(tc.ax), int(tc.ay)); got&0x8000 == 0 || got&0x1F != 0 {
				t.Errorf("start pixel = 0x%04X, want drawn with R=0", got)
			}
			if got := readFBPixel(v, int(tc.bx), int(tc.by)); got&0x8000 == 0 || got&0x1F != 15 {
				t.Errorf("end pixel = 0x%04X, want drawn with R=15", got)
			}
			// A Bresenham line covers max(|dx|,|dy|)+1 = 11 pixels.
			drawn := 0
			for y := 10; y <= 20; y++ {
				for x := 10; x <= 20; x++ {
					if readFBPixel(v, x, y) != 0 {
						drawn++
					}
				}
			}
			if drawn != 11 {
				t.Errorf("drawn pixels = %d, want 11", drawn)
			}
		})
	}
}

// TestNormalSpriteGouraud verifies bilinear Gouraud shading on a normal
// sprite: the top row interpolates A to B, the bottom row D to C, and
// columns interpolate between them.
func TestNormalSpriteGouraud(t *testing.T) {
	v := newDrawTestVDP1()
	grda := writeGouraudTable(v, 0x4210, 0x421F, 0x4210, 0x421F) // A R+0, B R+15, C R+0, D R+15
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 5<<3|0x0004) // RGB texture, Gouraud
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0108) // 8x8
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x1C, grda)
	writeDrawEnd(v, 0x20)
	for i := 0; i < 64; i++ {
		writeCmd16(v, 0x1000+uint32(i*2), 0x8000) // black RGB
	}
	v.VBlankIn()
	drainDrawing(v)
	for _, tc := range []struct{ x, y, wantR int }{
		{0, 0, 0}, {7, 0, 15}, {0, 7, 15}, {7, 7, 0}, {7, 3, 9}, {3, 0, 6},
	} {
		got := readFBPixel(v, tc.x, tc.y)
		if got&0x8000 == 0 {
			t.Errorf("pixel (%d,%d) not drawn", tc.x, tc.y)
			continue
		}
		if int(got&0x1F) != tc.wantR {
			t.Errorf("pixel (%d,%d) R=%d, want %d", tc.x, tc.y, got&0x1F, tc.wantR)
		}
	}
}

// writeRGBTexture fills a w x h 16bpp texture at addr with val.
func writeRGBTexture(v *VDP1, addr uint32, w, h int, val uint16) {
	for i := 0; i < w*h; i++ {
		writeCmd16(v, addr+uint32(i*2), val)
	}
}

// expectRed asserts the framebuffer pixel is drawn (MSB set) with the
// given 5-bit red channel.
func expectRed(t *testing.T, v *VDP1, x, y, wantR int, what string) {
	t.Helper()
	got := readFBPixel(v, x, y)
	if got&0x8000 == 0 {
		t.Errorf("%s: pixel (%d,%d) = 0x%04X, not drawn", what, x, y, got)
		return
	}
	if int(got&0x1F) != wantR {
		t.Errorf("%s: pixel (%d,%d) R=%d, want %d", what, x, y, got&0x1F, wantR)
	}
}

// TestDistortedSpriteGouraud verifies Gouraud shading on a distorted
// sprite (RGB texture): corners take the table values A, B, C, D and a
// negative correction clamps the channel at 0.
func TestDistortedSpriteGouraud(t *testing.T) {
	v := newDrawTestVDP1()
	// A: R-16 (channel 0), B: R+15, C: R-16, D: R+15 on a texel of R=5.
	grda := writeGouraudTable(v, 0x4200, 0x421F, 0x4200, 0x421F)
	writeDistortedSprite(v, 0x00, 0, 0, 7, 0, 7, 7, 0, 7, 5, 0x0000, 0x1000, 8, 8)
	writeCmd16(v, 0x04, 5<<3|0x0004)
	writeCmd16(v, 0x1C, grda)
	writeDrawEnd(v, 0x20)
	writeRGBTexture(v, 0x1000, 8, 8, 0x8005)
	v.VBlankIn()
	drainDrawing(v)
	expectRed(t, v, 0, 0, 0, "A: 5-16 clamps to 0")
	expectRed(t, v, 7, 7, 0, "C")
	// The distorted path steps the correction in 16.16 fixed point with
	// a truncated per-step delta, so the far vertex can land one level
	// below the table value (19 rather than 20 here).
	for _, p := range [][2]int{{7, 0}, {0, 7}} {
		got := readFBPixel(v, p[0], p[1])
		if r := int(got & 0x1F); got&0x8000 == 0 || r < 19 || r > 20 {
			t.Errorf("B/D vertex (%d,%d) = 0x%04X, want R 19..20", p[0], p[1], got)
		}
	}

	// Strong distortion with Gouraud exercises the gap-fill write path.
	v = newDrawTestVDP1()
	grda = writeGouraudTable(v, 0x4210, 0x421F, 0x421F, 0x4210)
	writeDistortedSprite(v, 0x00, 0, 0, 3, 9, 3, 19, 0, 10, 5, 0x0000, 0x1000, 8, 8)
	writeCmd16(v, 0x04, 5<<3|0x0004)
	writeCmd16(v, 0x1C, grda)
	writeDrawEnd(v, 0x20)
	writeRGBTexture(v, 0x1000, 8, 8, 0x8000)
	v.VBlankIn()
	drainDrawing(v)
	expectRed(t, v, 0, 0, 0, "distorted A")
	if got := readFBPixel(v, 3, 9); got&0x8000 == 0 || got&0x1F < 14 {
		t.Errorf("distorted B (3,9) = 0x%04X, want R 14..15", got)
	}
}

// TestPolylineGouraud verifies Gouraud shading along a polyline's four
// legs (A->B, B->C, C->D, D->A) and the single-dot Gouraud line.
func TestPolylineGouraud(t *testing.T) {
	v := newDrawTestVDP1()
	grda := writeGouraudTable(v, 0x4210, 0x421F, 0x4210, 0x421F)
	writePolyline(v, 0x00, 10, 10, 20, 10, 20, 20, 10, 20, 0x8000)
	writeCmd16(v, 0x04, 0x0004)
	writeCmd16(v, 0x1C, grda)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)
	expectRed(t, v, 20, 10, 15, "B")
	expectRed(t, v, 20, 20, 0, "C")
	expectRed(t, v, 10, 20, 15, "D")
	expectRed(t, v, 10, 10, 0, "A (end of the D->A leg)")
	expectRed(t, v, 15, 10, 7, "midpoint of A->B")

	// A line whose two end points coincide has one dot at the A value.
	v = newDrawTestVDP1()
	grda = writeGouraudTable(v, 0x4218, 0x421F, 0x421F, 0x421F)
	writeLine(v, 5, 5, 5, 5, 0x8000)
	writeCmd16(v, 0x04, 0x0004)
	writeCmd16(v, 0x1C, grda)
	v.VBlankIn()
	drainDrawing(v)
	expectRed(t, v, 5, 5, 8, "single dot")
}

// TestScaledSpriteGouraud verifies bilinear Gouraud on a scaled sprite
// interpolates over the destination size.
func TestScaledSpriteGouraud(t *testing.T) {
	v := newDrawTestVDP1()
	grda := writeGouraudTable(v, 0x4210, 0x421F, 0x4210, 0x421F)
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 5<<3|0x0004)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0108)
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x14, 15)
	writeCmd16(v, 0x16, 15)
	writeCmd16(v, 0x1C, grda)
	writeDrawEnd(v, 0x20)
	writeRGBTexture(v, 0x1000, 8, 8, 0x8000)
	v.VBlankIn()
	drainDrawing(v)
	expectRed(t, v, 0, 0, 0, "A")
	expectRed(t, v, 15, 0, 15, "B")
	expectRed(t, v, 15, 15, 0, "C")
	expectRed(t, v, 0, 15, 15, "D")
	expectRed(t, v, 15, 5, 10, "column 15 row 5")
}

// TestPolygonZeroLengthConnectingLinesGouraud verifies a polygon whose
// left and right edges coincide (A=B, D=C) draws one dot per connecting
// line carrying the left edge's Gouraud value.
func TestPolygonZeroLengthConnectingLinesGouraud(t *testing.T) {
	v := newDrawTestVDP1()
	grda := writeGouraudTable(v, 0x4210, 0x421F, 0x421F, 0x4210)
	writePolygon(v, 0x00, 5, 5, 5, 5, 5, 15, 5, 15, 0x8000)
	writeCmd16(v, 0x04, 0x0004)
	writeCmd16(v, 0x1C, grda)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)
	for y := 5; y <= 15; y++ {
		expectRed(t, v, 5, y, 0, "single-dot connecting line")
	}
	if got := readFBPixel(v, 6, 10); got != 0 {
		t.Errorf("pixel (6,10) = 0x%04X, want 0", got)
	}
}
