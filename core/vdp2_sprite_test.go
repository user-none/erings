package core

import "testing"

// TestDecodeSpritePixel16BitTypes verifies the palette-format field
// layouts of sprite types 2, 3, 4, 6, and 7 (VDP2 manual Figure 9.1):
// priority bits select PRISA-PRISD, the CC bits and dot color field are
// extracted at the type's positions, and the color MSB is the top dot
// color bit.
func TestDecodeSpritePixel16BitTypes(t *testing.T) {
	cases := []struct {
		sptype  uint16
		pixel   uint16
		wantPri uint8
		wantCC  uint8
		wantDC  uint32
		wantMSB bool
	}{
		// type 2: SD b15, PR b14, CC b13:11, DC b10:0
		{2, 1<<14 | 5<<11 | 0x0523, 1, 5, 0x523, true},
		{2, 0<<14 | 2<<11 | 0x0123, 0, 2, 0x123, false},
		// type 3: SD b15, PR b14:13, CC b12:11, DC b10:0
		{3, 2<<13 | 3<<11 | 0x0523, 2, 3, 0x523, true},
		{3, 1<<13 | 1<<11 | 0x0023, 1, 1, 0x023, false},
		// type 4: SD b15, PR b14:13, CC b12:10, DC b9:0
		{4, 3<<13 | 5<<10 | 0x0223, 3, 5, 0x223, true},
		{4, 2<<13 | 1<<10 | 0x0023, 2, 1, 0x023, false},
		// type 6: SD b15, PR b14:12, CC b11:10, DC b9:0
		{6, 6<<12 | 2<<10 | 0x0223, 6, 2, 0x223, true},
		{6, 4<<12 | 3<<10 | 0x0023, 4, 3, 0x023, false},
		// type 7: SD b15, PR b14:12, CC b11:9, DC b8:0
		{7, 7<<12 | 5<<9 | 0x0123, 7, 5, 0x123, true},
		{7, 5<<12 | 6<<9 | 0x0023, 5, 6, 0x023, false},
	}
	for _, tc := range cases {
		v := newTestVDP2()
		v.regs[vdp2SPCTL] = tc.sptype
		// CRAM mode 1 (2048 entries) so an 11-bit dot color addresses
		// its own entry.
		v.regs[vdp2RAMCTL] = 0x1000
		// Priority register n holds priority n.
		v.regs[vdp2PRISA] = 0x0100
		v.regs[vdp2PRISB] = 0x0302
		v.regs[vdp2PRISC] = 0x0504
		v.regs[vdp2PRISD] = 0x0706
		v.cram[tc.wantDC*2], v.cram[tc.wantDC*2+1] = 0x00, 0x1F // red
		v.BeginFrame()
		pri, cc, msb, r, g, b := v.decodeSpritePixel(tc.pixel)
		if pri != tc.wantPri || cc != tc.wantCC || msb != tc.wantMSB {
			t.Errorf("type %d pixel 0x%04X: pri=%d cc=%d msb=%v, want %d %d %v",
				tc.sptype, tc.pixel, pri, cc, msb, tc.wantPri, tc.wantCC, tc.wantMSB)
		}
		if r != 255 || g != 0 || b != 0 {
			t.Errorf("type %d pixel 0x%04X: color (%d,%d,%d), want red from CRAM entry 0x%03X",
				tc.sptype, tc.pixel, r, g, b, tc.wantDC)
		}
	}
}

// TestDecodeSpritePixel8BitSharedTypes verifies sprite types D, E, and F
// (8-bit, shared priority/CC and dot color bits per Figure 9.1): the
// shared bits are read as both the control field and part of the 8-bit
// color address, and the color MSB is bit 7.
func TestDecodeSpritePixel8BitSharedTypes(t *testing.T) {
	cases := []struct {
		sptype  uint16
		pixel   uint16
		wantPri uint8
		wantCC  uint8
		wantMSB bool
	}{
		// type D: PR0 = b7, CC0 = b6
		{0xD, 0xC5, 1, 1, true},
		{0xD, 0x45, 0, 1, false},
		// type E: PR1:0 = b7:6
		{0xE, 0xC5, 3, 0, true},
		{0xE, 0x85, 2, 0, true},
		{0xE, 0x45, 1, 0, false},
		// type F: CC1:0 = b7:6
		{0xF, 0xC5, 0, 3, true},
		{0xF, 0x45, 0, 1, false},
	}
	for _, tc := range cases {
		v := newTestVDP2()
		v.regs[vdp2SPCTL] = tc.sptype
		v.regs[vdp2PRISA] = 0x0100
		v.regs[vdp2PRISB] = 0x0302
		dc := uint32(tc.pixel & 0xFF)
		v.cram[dc*2], v.cram[dc*2+1] = 0x03, 0xE0 // green
		v.BeginFrame()
		pri, cc, msb, r, g, b := v.decodeSpritePixel(tc.pixel)
		if pri != tc.wantPri || cc != tc.wantCC || msb != tc.wantMSB {
			t.Errorf("type %X pixel 0x%02X: pri=%d cc=%d msb=%v, want %d %d %v",
				tc.sptype, tc.pixel, pri, cc, msb, tc.wantPri, tc.wantCC, tc.wantMSB)
		}
		if r != 0 || g != 255 || b != 0 {
			t.Errorf("type %X pixel 0x%02X: color (%d,%d,%d), want green from CRAM entry 0x%02X",
				tc.sptype, tc.pixel, r, g, b, dc)
		}
	}
}

// TestReadVDP1PixelHiRes verifies the framebuffer read under VDP2 hi-res:
// a 16bpp (512-wide) framebuffer is sampled at x/2 so two screen columns
// share one framebuffer column, while an 8bpp (1024-wide) framebuffer is
// sampled at x directly.
func TestReadVDP1PixelHiRes(t *testing.T) {
	v := newTestVDP2()
	v.hiRes = true
	v.BeginFrame()

	fb16 := make([]byte, 512*256*2)
	fb16[3*2+1] = 0x05 // column 3
	fb16[4*2+1] = 0x06 // column 4
	v.lineFB = vdp1FBView{data: fb16, width: 512, height: 256}
	for _, tc := range []struct {
		x    int
		want uint16
	}{{6, 5}, {7, 5}, {8, 6}, {9, 6}} {
		if pix, ok := v.readVDP1Pixel(tc.x, 0); !ok || pix != tc.want {
			t.Errorf("16bpp hi-res x=%d: pixel=0x%04X ok=%v, want 0x%04X", tc.x, pix, ok, tc.want)
		}
	}

	fb8 := make([]byte, 1024*256)
	fb8[6] = 5
	fb8[7] = 6
	v.lineFB = vdp1FBView{data: fb8, is8bpp: true, width: 1024, height: 256}
	if pix, ok := v.readVDP1Pixel(6, 0); !ok || pix != 5 {
		t.Errorf("8bpp hi-res x=6: pixel=0x%04X ok=%v, want 5", pix, ok)
	}
	if pix, ok := v.readVDP1Pixel(7, 0); !ok || pix != 6 {
		t.Errorf("8bpp hi-res x=7: pixel=0x%04X ok=%v, want 6", pix, ok)
	}
	if _, ok := v.readVDP1Pixel(8, 0); ok {
		t.Error("8bpp zero pixel should read as not valid")
	}
	if _, ok := v.readVDP1Pixel(1024, 0); ok {
		t.Error("8bpp x past the framebuffer width should read as not valid")
	}
}

// setupRotatedSpriteScene builds a 320x224 scene with a rotated 16bpp
// VDP1 framebuffer view (sprite type 0, priority register 0 = 5), a red
// sprite dot value 5 and a green value 9, and rotation parameter A at
// VRAM 0x10000 with Xst=Yst=0, DYst=1, and the given per-dot DX/DY.
func setupRotatedSpriteScene(t *testing.T, dx, dy uint16) (*VDP2, vdp1FBView) {
	t.Helper()
	v := newTestVDP2()
	v.regs[vdp2SPCTL] = 0x0000
	v.regs[vdp2PRISA] = 0x0005
	v.regs[vdp2RPTAU] = 0x0000
	v.regs[vdp2RPTAL] = 0x8000
	writeRotParam32(v, 0x10000, 0x10, 0x0001, 0x0000) // DYst = 1.0
	writeRotParam32(v, 0x10000, 0x14, dx, 0x0000)     // DX
	writeRotParam32(v, 0x10000, 0x18, dy, 0x0000)     // DY
	v.cram[10], v.cram[11] = 0x00, 0x1F               // 5: red
	v.cram[18], v.cram[19] = 0x03, 0xE0               // 9: green
	fb := make([]byte, 512*256*2)
	return v, vdp1FBView{data: fb, width: 512, height: 256, rotated: true}
}

// setFBPixel16 writes a 16bpp framebuffer pixel.
func setFBPixel16(fb vdp1FBView, x, y int, val uint16) {
	off := (y*fb.width + x) * 2
	fb.data[off] = uint8(val >> 8)
	fb.data[off+1] = uint8(val)
}

// outRGB returns the composited output color at (x, y).
func outRGB(v *VDP2, x, y int) (r, g, b uint8) {
	fb := v.Framebuffer()
	off := (y*int(v.activeWidth) + x) * 4
	return fb[off], fb[off+1], fb[off+2]
}

// TestReadVDP1PixelRotated verifies the rotated framebuffer read (VDP1
// manual Sec 1.2, VDP1 TVMR rotation): the sample position is rotation
// parameter A's line start (Xst/Yst stepped by DXst/DYst per line) plus
// DX/DY per screen dot, not the screen position, and out-of-range
// positions read transparent.
func TestReadVDP1PixelRotated(t *testing.T) {
	// DX = 0, DY = 1: screen (x, y) reads framebuffer (0, x + y).
	v, fb := setupRotatedSpriteScene(t, 0x0000, 0x0001)
	setFBPixel16(fb, 0, 0, 0x0009) // green
	setFBPixel16(fb, 0, 5, 0x0005) // red
	setFBPixel16(fb, 3, 0, 0x0005) // never sampled (X stays 0)
	renderTestFrameFB(v, fb)
	if r, g, _ := outRGB(v, 0, 0); r != 0 || g != 255 {
		t.Errorf("(0,0) -> fb(0,0): got (%d,%d), want green", r, g)
	}
	if r, g, _ := outRGB(v, 5, 0); r != 255 || g != 0 {
		t.Errorf("(5,0) -> fb(0,5): got (%d,%d), want red", r, g)
	}
	if r, g, _ := outRGB(v, 2, 3); r != 255 || g != 0 {
		t.Errorf("(2,3) -> fb(0,5): got (%d,%d), want red", r, g)
	}
	if r, g, b := outRGB(v, 3, 0); r != 0 || g != 0 || b != 0 {
		t.Errorf("(3,0) -> fb(0,3): got (%d,%d,%d), want transparent (black)", r, g, b)
	}
	if r, g, b := outRGB(v, 300, 0); r != 0 || g != 0 || b != 0 {
		t.Errorf("(300,0) -> fb(0,300) out of range: got (%d,%d,%d), want transparent", r, g, b)
	}
}

// TestReadVDP1PixelRotatedRPRCTL verifies the sprite rotation read
// honors the parameter A RPRCTL Xst re-read arm: Xst rewritten
// mid-frame takes effect from the armed line.
func TestReadVDP1PixelRotatedRPRCTL(t *testing.T) {
	// DX = 0, DY = 0: screen (x, y) reads framebuffer (Xst, y).
	v, fb := setupRotatedSpriteScene(t, 0x0000, 0x0000)
	for y := 0; y < 16; y++ {
		setFBPixel16(fb, 0, y, 0x0005) // column 0 red
		setFBPixel16(fb, 1, y, 0x0009) // column 1 green
	}
	v.BeginFrame()
	v.vLine = 5
	v.Write(uint32(vdp2RPRCTL*2), 0x0001)
	for y := 0; y < 8; y++ {
		if y == 3 {
			writeRotParam32(v, 0x10000, 0x00, 0x0001, 0x0000) // Xst = 1
		}
		v.RenderLine(y, fb)
	}
	if r, _, _ := outRGB(v, 10, 4); r != 255 {
		t.Errorf("line 4 before the arm: red %d, want 255 (column 0)", r)
	}
	if _, g, _ := outRGB(v, 10, 5); g != 255 {
		t.Errorf("armed line 5: green %d, want 255 (column 1)", g)
	}
	if _, g, _ := outRGB(v, 10, 7); g != 255 {
		t.Errorf("line 7 after the arm: green %d, want 255 (column 1)", g)
	}
}

// TestReadVDP1PixelRotatedLSMD3 verifies the rotated read steps the line
// start by the displayed line (2y + field) in double-density interlace.
func TestReadVDP1PixelRotatedLSMD3(t *testing.T) {
	v, fb := setupRotatedSpriteScene(t, 0x0000, 0x0000)
	for y := 0; y < 32; y++ {
		val := uint16(0x0005) // even rows red
		if y&1 == 1 {
			val = 0x0009 // odd rows green
		}
		setFBPixel16(fb, 0, y, val)
	}
	setLSMD3(v, true)
	v.BeginFrame()
	v.RenderLine(0, fb)
	v.RenderLine(1, fb)
	// Field 1 line 0 is displayed line 1: reads framebuffer row 1.
	if _, g, _ := outRGB(v, 10, 1); g != 255 {
		t.Errorf("field 1 line 0 -> fb row 1: green %d, want 255", g)
	}
	if _, g, _ := outRGB(v, 10, 3); g != 255 {
		t.Errorf("field 1 line 1 -> fb row 3: green %d, want 255", g)
	}
}

// TestSpriteRotationYstReRead verifies the sprite rotation read honors
// the parameter A Yst re-read arm (RPRCTL bit 1).
func TestSpriteRotationYstReRead(t *testing.T) {
	v, fb := setupRotatedSpriteScene(t, 0x0000, 0x0000)
	for y := 0; y < 32; y++ {
		val := uint16(0x0005) // rows 0..15 red
		if y >= 16 {
			val = 0x0009 // rows 16.. green
		}
		setFBPixel16(fb, 0, y, val)
	}
	v.BeginFrame()
	v.vLine = 5
	v.Write(uint32(vdp2RPRCTL*2), 0x0002)
	for y := 0; y < 8; y++ {
		if y == 3 {
			writeRotParam32(v, 0x10000, 0x04, 16, 0x0000) // Yst = 16
		}
		v.RenderLine(y, fb)
	}
	if r, _, _ := outRGB(v, 10, 4); r != 255 {
		t.Errorf("line 4 before the arm: red %d, want 255 (row 4)", r)
	}
	if _, g, _ := outRGB(v, 10, 5); g != 255 {
		t.Errorf("armed line 5: green %d, want 255 (row 16)", g)
	}
}
