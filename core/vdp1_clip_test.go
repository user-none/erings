package core

import "testing"

// TestPolygonPreClipLeadingSkipGouraud verifies that a connecting line
// starting off the left of the drawing area skips its leading
// off-screen dots without changing the visible result: the Gouraud
// value at each visible dot equals its interpolation position along
// the full line.
func TestPolygonPreClipLeadingSkipGouraud(t *testing.T) {
	v := newDrawTestVDP1()
	grda := writeGouraudTable(v, 0x4210, 0x421F, 0x421F, 0x4210)
	// A(-20,10) B(20,10) C(20,12) D(-20,12): three 41-dot lines.
	writePolygon(v, 0x00, -20, 10, 20, 10, 20, 12, -20, 12, 0x8000)
	writeCmd16(v, 0x04, 0x0004)
	writeCmd16(v, 0x1C, grda)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)
	for _, y := range []int{10, 11, 12} {
		for _, tc := range []struct{ x, wantR int }{{0, 7}, {10, 11}, {20, 15}} {
			got := readFBPixel(v, tc.x, y)
			if got&0x8000 == 0 {
				t.Errorf("pixel (%d,%d) not drawn", tc.x, y)
				continue
			}
			if int(got&0x1F) != tc.wantR {
				t.Errorf("pixel (%d,%d) R=%d, want %d", tc.x, y, got&0x1F, tc.wantR)
			}
		}
	}
}

// TestDistortedSpritePreClipLeadingSkip verifies the distorted sprite's
// leading off-screen skip (taken only with end codes disabled) yields the
// same visible texels as the un-skipped walk with end codes enabled.
func TestDistortedSpritePreClipLeadingSkip(t *testing.T) {
	run := func(ecdOff bool) *VDP1 {
		v := newDrawTestVDP1()
		// A(-20,0) B(20,0) C(20,3) D(-20,3), 8x4 256-color texture with
		// dot 0x10 + column.
		writeDistortedSprite(v, 0x00, -20, 0, 20, 0, 20, 3, -20, 3, 4, 0x0100, 0x1000, 8, 4)
		if ecdOff {
			writeCmd16(v, 0x04, 4<<3|0x0080) // ECD: end codes disabled
		}
		writeDrawEnd(v, 0x20)
		for y := 0; y < 4; y++ {
			for x := 0; x < 8; x++ {
				v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+x))
			}
		}
		v.VBlankIn()
		drainDrawing(v)
		return v
	}
	skip := run(true)
	full := run(false)
	for y := 0; y < 4; y++ {
		for x := 0; x <= 20; x++ {
			if a, b := readFBPixel(skip, x, y), readFBPixel(full, x, y); a != b {
				t.Errorf("pixel (%d,%d): skip path 0x%04X, full path 0x%04X", x, y, a, b)
			}
		}
	}
	if got := readFBPixel(skip, 0, 0); got != 0x0113 {
		t.Errorf("pixel (0,0) = 0x%04X, want 0x0113 (texel 3 at line position 20/40)", got)
	}
	if got := readFBPixel(skip, 20, 0); got == 0 {
		t.Error("pixel (20,0) not drawn")
	}
}

// TestScaledSpriteRowsOutsideClip verifies destination rows above the
// drawing area and below the system clip are skipped without disturbing
// the rows inside it.
func TestScaledSpriteRowsOutsideClip(t *testing.T) {
	setup := func(ya, yc int16, clipY uint16) *VDP1 {
		v := newDrawTestVDP1()
		writeCmd16(v, 0x00, 0x0009) // system clip
		writeCmd16(v, 0x14, 319)
		writeCmd16(v, 0x16, clipY)
		writeCmd16(v, 0x20, 0x0001) // scaled sprite, two coordinates
		writeCmd16(v, 0x24, 0x0020) // 256-color bank
		writeCmd16(v, 0x26, 0x0100)
		writeCmd16(v, 0x28, 0x1000/8)
		writeCmd16(v, 0x2A, 0x0108) // 8x8
		writeCmd16(v, 0x2C, 5)
		writeCmd16(v, 0x2E, uint16(ya))
		writeCmd16(v, 0x34, 12)
		writeCmd16(v, 0x36, uint16(yc))
		writeDrawEnd(v, 0x40)
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+y))
			}
		}
		v.VBlankIn()
		drainDrawing(v)
		return v
	}

	// Rows -3..-1 are above the drawing area; row 0 samples source row 3.
	v := setup(-3, 4, 223)
	if got := readFBPixel(v, 5, 0); got != 0x0113 {
		t.Errorf("row 0 = 0x%04X, want 0x0113 (source row 3)", got)
	}
	if got := readFBPixel(v, 5, 4); got != 0x0117 {
		t.Errorf("row 4 = 0x%04X, want 0x0117 (source row 7)", got)
	}

	// System clip Y = 2: rows 3..7 are skipped.
	v = setup(0, 7, 2)
	if got := readFBPixel(v, 5, 2); got != 0x0112 {
		t.Errorf("row 2 = 0x%04X, want 0x0112", got)
	}
	if got := readFBPixel(v, 5, 3); got != 0 {
		t.Errorf("row 3 = 0x%04X, want 0 (below the system clip)", got)
	}
}

// TestClipSegParam verifies the parametric segment clip against the
// drawing area: wholly outside segments on each side are rejected,
// segments parallel to an edge and outside it are rejected, and a
// crossing segment returns the visible parameter range.
func TestClipSegParam(t *testing.T) {
	const cx, cy = 100, 50
	rejects := []struct {
		name           string
		x0, y0, x1, y1 int
	}{
		{"left", -30, 10, -10, 20},
		{"right", 110, 10, 130, 20},
		{"above", 10, -30, 20, -10},
		{"below", 10, 60, 20, 80},
		{"horizontal above", 10, -5, 20, -5},
		{"vertical right", 105, 10, 105, 40},
	}
	for _, tc := range rejects {
		if _, _, ok := clipSegParam(tc.x0, tc.y0, tc.x1, tc.y1, cx, cy); ok {
			t.Errorf("%s: segment (%d,%d)-(%d,%d) should be rejected", tc.name, tc.x0, tc.y0, tc.x1, tc.y1)
		}
	}
	t0, t1, ok := clipSegParam(-10, 5, 10, 5, cx, cy)
	if !ok || t0 != 0.5 || t1 != 1 {
		t.Errorf("crossing left edge: t0=%v t1=%v ok=%v, want 0.5 1 true", t0, t1, ok)
	}
	t0, t1, ok = clipSegParam(90, 5, 110, 5, cx, cy)
	if !ok || t0 != 0 || t1 != 0.5 {
		t.Errorf("crossing right edge: t0=%v t1=%v ok=%v, want 0 0.5 true", t0, t1, ok)
	}
	t0, t1, ok = clipSegParam(50, -10, 50, 10, cx, cy)
	if !ok || t0 != 0.5 || t1 != 1 {
		t.Errorf("crossing top edge: t0=%v t1=%v ok=%v, want 0.5 1 true", t0, t1, ok)
	}
	t0, t1, ok = clipSegParam(50, 40, 50, 60, cx, cy)
	if !ok || t0 != 0 || t1 != 0.5 {
		t.Errorf("crossing bottom edge: t0=%v t1=%v ok=%v, want 0 0.5 true", t0, t1, ok)
	}
}

// TestConnectingLinesWhollyOffscreen verifies a distorted sprite and a
// polygon whose upper connecting lines lie entirely above the drawing
// area still draw their visible lines, and a polygon with steep
// connecting lines fills its columns.
func TestConnectingLinesWhollyOffscreen(t *testing.T) {
	v := newDrawTestVDP1()
	writeDistortedSprite(v, 0x00, 0, -20, 7, -20, 7, 10, 0, 10, 4, 0x0100, 0x1000, 8, 8)
	writeDrawEnd(v, 0x20)
	for i := 0; i < 64; i++ {
		v.WriteVRAM(0x1000+uint32(i), 0x11)
	}
	v.VBlankIn()
	drainDrawing(v)
	if got := readFBPixel(v, 3, 0); got != 0x0111 {
		t.Errorf("distorted: pixel (3,0) = 0x%04X, want 0x0111", got)
	}
	if got := readFBPixel(v, 3, 10); got != 0x0111 {
		t.Errorf("distorted: pixel (3,10) = 0x%04X, want 0x0111", got)
	}

	v = newDrawTestVDP1()
	writePolygon(v, 0x00, 0, -20, 7, -20, 7, 10, 0, 10, 0x8123)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)
	if got := readFBPixel(v, 3, 0); got != 0x8123 {
		t.Errorf("polygon: pixel (3,0) = 0x%04X, want 0x8123", got)
	}
	if got := readFBPixel(v, 3, 10); got != 0x8123 {
		t.Errorf("polygon: pixel (3,10) = 0x%04X, want 0x8123", got)
	}

	// Steep connecting lines: A(0,0) B(2,10) C(2,20) D(0,10).
	v = newDrawTestVDP1()
	writePolygon(v, 0x00, 0, 0, 2, 10, 2, 20, 0, 10, 0x8123)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)
	if got := readFBPixel(v, 0, 5); got != 0x8123 {
		t.Errorf("steep polygon: pixel (0,5) = 0x%04X, want 0x8123", got)
	}
	if got := readFBPixel(v, 2, 15); got != 0x8123 {
		t.Errorf("steep polygon: pixel (2,15) = 0x%04X, want 0x8123", got)
	}
}

// TestPreClipRejectAllCommands verifies every drawing command placed
// entirely above the drawing area draws nothing, with pre-clipping
// enabled and disabled.
func TestPreClipRejectAllCommands(t *testing.T) {
	type cmd struct {
		name  string
		write func(v *VDP1)
	}
	cmds := []cmd{
		{"line", func(v *VDP1) {
			writeLine(v, 10, -50, 20, -40, 0x8123)
		}},
		{"polyline", func(v *VDP1) {
			writePolyline(v, 0x00, 10, -50, 20, -50, 20, -40, 10, -40, 0x8123)
			writeDrawEnd(v, 0x20)
		}},
		{"polygon", func(v *VDP1) {
			writePolygon(v, 0x00, 10, -50, 20, -50, 20, -40, 10, -40, 0x8123)
			writeDrawEnd(v, 0x20)
		}},
		{"distorted sprite", func(v *VDP1) {
			writeDistortedSprite(v, 0x00, 10, -50, 17, -50, 17, -43, 10, -43, 4, 0x0100, 0x1000, 8, 8)
			writeDrawEnd(v, 0x20)
		}},
		{"scaled sprite", func(v *VDP1) {
			writeCmd16(v, 0x00, 0x0001)
			writeCmd16(v, 0x04, 0x0020)
			writeCmd16(v, 0x06, 0x0100)
			writeCmd16(v, 0x08, 0x1000/8)
			writeCmd16(v, 0x0A, 0x0108)
			writeCmd16(v, 0x0C, 10)
			writeCmd16(v, 0x0E, 0xFFCE)
			writeCmd16(v, 0x14, 25)
			writeCmd16(v, 0x16, 0xFFD8)
			writeDrawEnd(v, 0x20)
		}},
		{"normal sprite", func(v *VDP1) {
			writeCmd16(v, 0x00, 0x0000)
			writeCmd16(v, 0x04, 0x0020)
			writeCmd16(v, 0x06, 0x0100)
			writeCmd16(v, 0x08, 0x1000/8)
			writeCmd16(v, 0x0A, 0x0108)
			writeCmd16(v, 0x0C, 10)
			writeCmd16(v, 0x0E, 0xFFCE)
			writeDrawEnd(v, 0x20)
		}},
	}
	for _, c := range cmds {
		for _, preclipOff := range []bool{false, true} {
			v := newDrawTestVDP1()
			c.write(v)
			if preclipOff {
				pmod := uint16(v.vram[0x04])<<8 | uint16(v.vram[0x05])
				writeCmd16(v, 0x04, pmod|0x0800)
			}
			for i := 0; i < 64; i++ {
				v.WriteVRAM(0x1000+uint32(i), 0x11)
			}
			v.VBlankIn()
			drainDrawing(v)
			for y := 0; y < 4; y++ {
				for x := 0; x < 32; x++ {
					if got := readFBPixel(v, x, y); got != 0 {
						t.Errorf("%s (pre-clip off=%v): pixel (%d,%d) = 0x%04X, want 0", c.name, preclipOff, x, y, got)
					}
				}
			}
		}
	}
}
