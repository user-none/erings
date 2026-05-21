// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// writeCmd16 writes a big-endian 16-bit value to VDP1 VRAM at the given address.
func writeCmd16(v *VDP1, addr uint32, val uint16) {
	v.WriteVRAM(addr, uint8(val>>8))
	v.WriteVRAM(addr+1, uint8(val))
}

// writeDrawEnd writes a draw-end command at the given VRAM byte address.
func writeDrawEnd(v *VDP1, addr uint32) {
	writeCmd16(v, addr+0x00, 0x8000) // CMDCTRL with end bit
}

// readFBPixel reads a 16-bit pixel from the draw frame buffer at (x,y).
func readFBPixel(v *VDP1, x, y int) uint16 {
	off := (y*vdp1FBStride + x) * 2
	return uint16(v.drawFB[off])<<8 | uint16(v.drawFB[off+1])
}

// readDisplayFBPixel reads a 16-bit pixel from the display frame buffer at (x,y).
func readDisplayFBPixel(v *VDP1, x, y int) uint16 {
	off := (y*vdp1FBStride + x) * 2
	return uint16(v.displayFB[off])<<8 | uint16(v.displayFB[off+1])
}

// drainDrawing drives a VDP1 draw to completion. Test-only helper for
// the bulk of tests whose assertion is on the final framebuffer
// contents and which therefore want the equivalent of "process the
// whole list now". Mirrors the production V-Blank-IN PTM=10 entry plus
// drawPending consumption; subsequent TickSystemCycles drains the list.
//
// Latches shadow registers into active first so tests that set up
// register state via Write() (which lands in pending) see the values
// reflected in active before the draw fires.
func drainDrawing(v *VDP1) {
	v.latchPending()
	if v.ptmr == 2 && !v.drawActive && !v.drawPending {
		v.startDraw()
	}
	v.TickSystemCycles(1 << 30)
}

// seedShadows mirrors the active register values into the shadow
// (pending) registers. Used by tests that simulate "post-latch" VDP1
// state via direct active-field assignment so that a subsequent
// VBlankIn() call (whose latchPending step would otherwise clobber
// the test setup with zero pending values) preserves the test
// scenario. On real hardware the active register only ever takes a
// value via the Write -> pending -> latch path, so production code
// has no equivalent of this helper.
func seedShadows(v *VDP1) {
	v.fbcrPending = v.fbcr
	v.ptmrPending = v.ptmr
	v.ewdrPending = v.ewdr
	v.ewlrPending = v.ewlr
	v.ewrrPending = v.ewrr
}

// --- Frame Buffer Erase Tests ---

func TestEraseFullScreen(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2) // auto draw
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)
	v.Write(0x06, 0x1234)

	// Place draw end at VRAM[0]
	writeDrawEnd(v, 0)

	v.VBlankIn()
	drainDrawing(v)

	// After draw drains, buffers swapped: what was drawFB is now displayFB.
	// The erased buffer was the old drawFB, now displayFB.
	if got := readFBPixel(v, 0, 0); got != 0x1234 {
		t.Errorf("pixel (0,0) = 0x%04X, want 0x1234", got)
	}
	if got := readFBPixel(v, 319, 0); got != 0x1234 {
		t.Errorf("pixel (319,0) = 0x%04X, want 0x1234", got)
	}
	if got := readFBPixel(v, 0, 255); got != 0x1234 {
		t.Errorf("pixel (0,255) = 0x%04X, want 0x1234", got)
	}
	if got := readFBPixel(v, 319, 255); got != 0x1234 {
		t.Errorf("pixel (319,255) = 0x%04X, want 0x1234", got)
	}
	// Pixel at (320,0) should NOT be erased (outside erase region)
	if got := readFBPixel(v, 320, 0); got != 0 {
		t.Errorf("pixel (320,0) = 0x%04X, want 0x0000", got)
	}
}

func TestErasePartialRegion(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	// Erase from (8,1) to (23,2): EWLR x1_reg=1, y1=1, EWRR x3_reg=3, y3=2
	v.Write(0x08, (1<<9)|1) // x1_reg=1 -> x1=8, y1=1
	v.Write(0x0A, (3<<9)|2) // x3_reg=3 -> x3=23, y3=2
	v.Write(0x06, 0xABCD)

	writeDrawEnd(v, 0)
	v.VBlankIn()
	drainDrawing(v)

	// (8,1) should be filled
	if got := readFBPixel(v, 8, 1); got != 0xABCD {
		t.Errorf("pixel (8,1) = 0x%04X, want 0xABCD", got)
	}
	// (23,2) should be filled
	if got := readFBPixel(v, 23, 2); got != 0xABCD {
		t.Errorf("pixel (23,2) = 0x%04X, want 0xABCD", got)
	}
	// (7,1) should NOT be filled
	if got := readFBPixel(v, 7, 1); got != 0 {
		t.Errorf("pixel (7,1) = 0x%04X, want 0x0000", got)
	}
	// (24,1) should NOT be filled
	if got := readFBPixel(v, 24, 1); got != 0 {
		t.Errorf("pixel (24,1) = 0x%04X, want 0x0000", got)
	}
	// (8,0) should NOT be filled
	if got := readFBPixel(v, 8, 0); got != 0 {
		t.Errorf("pixel (8,0) = 0x%04X, want 0x0000", got)
	}
}

// TestEraseDegenerateX1GEX3Normal: manual Sec 4.4 p.49 - X1 >= X3 in
// normal mode erases a single dot at (X1, Y1).
func TestEraseDegenerateX1GEX3Normal(t *testing.T) {
	v := NewVDP1(NewSCU())
	// TVM=0 (default normal 16-bit). x1_reg=2 -> x1=16; x3_reg=1 ->
	// x3=7. x1 >= x3 is degenerate. y1=5.
	v.Write(0x08, uint16(2<<9)|5)  // EWLR: x1_reg=2, y1=5
	v.Write(0x0A, uint16(1<<9)|10) // EWRR: x3_reg=1, y3=10
	v.Write(0x06, 0x1234)          // EWDR

	v.latchPending()
	v.eraseFrameBuffer(v.drawFB)

	if got := readFBPixel(v, 16, 5); got != 0x1234 {
		t.Errorf("px(16,5) = 0x%04X, want 0x1234 (single dot)", got)
	}
	for _, p := range [][2]int{{15, 5}, {17, 5}, {16, 4}, {16, 6}} {
		if got := readFBPixel(v, p[0], p[1]); got != 0 {
			t.Errorf("px(%d,%d) = 0x%04X, want 0x0000 (outside single dot)", p[0], p[1], got)
		}
	}
}

// TestEraseDegenerateY1GTY3Normal: manual Sec 4.4 p.49 - Y1 > Y3 (with
// X1 < X3) in normal mode erases a single dot at (X1, Y1).
func TestEraseDegenerateY1GTY3Normal(t *testing.T) {
	v := NewVDP1(NewSCU())
	// x1_reg=1 -> x1=8; x3_reg=3 -> x3=23 (X not degenerate). y1=10,
	// y3=4: Y1 > Y3 is degenerate.
	v.Write(0x08, uint16(1<<9)|10) // EWLR: x1_reg=1, y1=10
	v.Write(0x0A, uint16(3<<9)|4)  // EWRR: x3_reg=3, y3=4
	v.Write(0x06, 0xABCD)          // EWDR

	v.latchPending()
	v.eraseFrameBuffer(v.drawFB)

	if got := readFBPixel(v, 8, 10); got != 0xABCD {
		t.Errorf("px(8,10) = 0x%04X, want 0xABCD (single dot)", got)
	}
	for _, p := range [][2]int{{7, 10}, {9, 10}, {8, 9}, {8, 11}} {
		if got := readFBPixel(v, p[0], p[1]); got != 0 {
			t.Errorf("px(%d,%d) = 0x%04X, want 0x0000 (outside single dot)", p[0], p[1], got)
		}
	}
}

// TestEraseDegenerateRotation8Dots: manual Sec 4.4 p.49 - in rotation
// (TVM 010/011) or HDTV (TVM 100) a degenerate region erases 8 dots.
func TestEraseDegenerateRotation8Dots(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x00, 0x02) // TVM=010 rotation 16-bit
	// x1_reg=2 -> x1=16; x3_reg=1 -> x3=7. x1 >= x3 degenerate.
	// nDots=8 -> erase x in [16,23] at y=5.
	v.Write(0x08, uint16(2<<9)|5)  // EWLR: x1_reg=2, y1=5
	v.Write(0x0A, uint16(1<<9)|10) // EWRR: x3_reg=1, y3=10
	v.Write(0x06, 0x4321)          // EWDR

	v.latchPending()
	v.eraseFrameBuffer(v.drawFB)

	for x := 16; x <= 23; x++ {
		if got := readFBPixel(v, x, 5); got != 0x4321 {
			t.Errorf("px(%d,5) = 0x%04X, want 0x4321 (8-dot run)", x, got)
		}
	}
	for _, p := range [][2]int{{15, 5}, {24, 5}, {16, 4}, {16, 6}} {
		if got := readFBPixel(v, p[0], p[1]); got != 0 {
			t.Errorf("px(%d,%d) = 0x%04X, want 0x0000 (outside 8-dot run)", p[0], p[1], got)
		}
	}
}

// TestEraseDegenerateOutOfBoundsSafe: a degenerate point whose Y1
// exceeds the framebuffer height must not write out of bounds or
// panic; the post-degenerate w/h clamp leaves y3 < y1 so the fill
// loop performs zero iterations.
func TestEraseDegenerateOutOfBoundsSafe(t *testing.T) {
	v := NewVDP1(NewSCU())
	// TVM=0 -> fbHeight=256. x1_reg=2 (x1=16) >= x3 (x3_reg=1 -> 7):
	// degenerate. y1=300 is beyond the 256-row framebuffer.
	v.Write(0x08, uint16(2<<9)|300) // EWLR: x1_reg=2, y1=300
	v.Write(0x0A, uint16(1<<9)|10)  // EWRR: x3_reg=1, y3=10
	v.Write(0x06, 0x5678)           // EWDR

	v.latchPending()
	v.eraseFrameBuffer(v.drawFB) // must not panic

	// Nothing should have been written anywhere in the buffer.
	for i := 0; i < len(v.drawFB); i++ {
		if v.drawFB[i] != 0 {
			t.Fatalf("drawFB[%d] = 0x%02X, want 0x00 (no erase when Y1 out of bounds)", i, v.drawFB[i])
		}
	}
}

// --- Command Processing Tests ---

func TestDrawEndImmediate(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	writeDrawEnd(v, 0)

	v.VBlankIn()
	drainDrawing(v)

	// CEF should be set
	if v.edsr&0x02 == 0 {
		t.Error("CEF should be set after draw end")
	}
}

func TestJumpNext(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Command 0: local coordinate set (type 0xA), jp=0 (next)
	writeCmd16(v, 0x00, 0x000A) // CMDCTRL
	writeCmd16(v, 0x0C, 10)     // XA = 10
	writeCmd16(v, 0x0E, 20)     // YA = 20

	// Command 1 at 0x20: draw end
	writeDrawEnd(v, 0x20)

	v.VBlankIn()
	drainDrawing(v)

	// Local coords should be set from first command
	if v.localX != 10 || v.localY != 20 {
		t.Errorf("local coords = (%d,%d), want (10,20)", v.localX, v.localY)
	}
}

func TestJumpAssign(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Command 0: local coord set, jp=1 (assign), link points to 0x100
	writeCmd16(v, 0x00, 0x100A)  // jp=1, type=0xA
	writeCmd16(v, 0x02, 0x100/8) // CMDLINK = 0x100/8 = 0x20
	writeCmd16(v, 0x0C, 5)       // XA = 5
	writeCmd16(v, 0x0E, 6)       // YA = 6

	// Draw end at 0x100
	writeDrawEnd(v, 0x100)

	v.VBlankIn()
	drainDrawing(v)

	if v.localX != 5 || v.localY != 6 {
		t.Errorf("local coords = (%d,%d), want (5,6)", v.localX, v.localY)
	}
}

func TestJumpCallReturn(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Command 0: call to 0x100 (jp=2), type=local coord
	writeCmd16(v, 0x00, 0x200A)  // jp=2, type=0xA
	writeCmd16(v, 0x02, 0x100/8) // link to 0x100
	writeCmd16(v, 0x0C, 1)       // XA = 1
	writeCmd16(v, 0x0E, 2)       // YA = 2

	// Command at 0x100: return (jp=3), type=local coord
	writeCmd16(v, 0x100, 0x300A) // jp=3, type=0xA
	writeCmd16(v, 0x10C, 3)      // XA = 3
	writeCmd16(v, 0x10E, 4)      // YA = 4

	// Command at 0x20 (return target): draw end
	writeDrawEnd(v, 0x20)

	v.VBlankIn()
	drainDrawing(v)

	// Last local coord set should be from the subroutine at 0x100
	if v.localX != 3 || v.localY != 4 {
		t.Errorf("local coords = (%d,%d), want (3,4)", v.localX, v.localY)
	}
}

func TestSignExtendCoord13(t *testing.T) {
	cases := []struct {
		raw  uint16
		want int16
	}{
		{0x0000, 0},
		{0x0001, 1},
		{0x0FFF, 4095},  // max positive (bit 12 = 0)
		{0x1000, -4096}, // min negative (bit 12 = 1, bits 11..0 = 0)
		{0x1FFF, -1},    // -1 (all 13 bits set)
		{0x1429, -3031}, // arbitrary negative (bit 12 set)
		{0xF000, -4096}, // value with proper sign-extension top bits
		{0xE000, 0},     // top three bits set, low 13 bits zero -> 0
		{0x801D, 29},    // burning rangers case: low 13 bits = 0x001D = +29
		{0xC400, 1024},  // top bits ignored; low 13 bits = 0x0400, bit 12 clear -> +1024
	}
	for _, c := range cases {
		got := signExtendCoord13(c.raw)
		if got != c.want {
			t.Errorf("signExtendCoord13(0x%04X) = %d, want %d", c.raw, got, c.want)
		}
	}
}

func TestLocalCoordinateSet(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x000A) // type=0xA
	writeCmd16(v, 0x0C, 100)    // XA
	writeCmd16(v, 0x0E, 50)     // YA

	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	if v.localX != 100 || v.localY != 50 {
		t.Errorf("local = (%d,%d), want (100,50)", v.localX, v.localY)
	}
}

// TestLocalCoordinatePersistsAcrossDraws verifies that local coordinates
// set by a LocalCoord command remain in effect after a subsequent
// startDraw. The VDP1 manual lists no register reset at draw start for
// LocalCoord, so games that program it once and rely on it across
// frames must see the value preserved.
func TestLocalCoordinatePersistsAcrossDraws(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x000A) // type=0xA
	writeCmd16(v, 0x0C, 0xFFF0) // XA = -16
	writeCmd16(v, 0x0E, 0x0008) // YA = 8

	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	if v.localX != -16 || v.localY != 8 {
		t.Fatalf("after first draw: local = (%d,%d), want (-16,8)", v.localX, v.localY)
	}

	// Trigger another draw with no LocalCoord command in the list.
	// localX/localY must carry over from the previous draw.
	writeCmd16(v, 0x00, 0x0000) // no-op control
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	if v.localX != -16 || v.localY != 8 {
		t.Errorf("after second draw: local = (%d,%d), want (-16,8) preserved",
			v.localX, v.localY)
	}
}

func TestSystemClipSet(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0009) // type=0x9
	writeCmd16(v, 0x14, 255)    // XC
	writeCmd16(v, 0x16, 200)    // YC

	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	if v.sysClipX != 255 || v.sysClipY != 200 {
		t.Errorf("sysClip = (%d,%d), want (255,200)", v.sysClipX, v.sysClipY)
	}
}

func TestSkipMode(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Command 0: jp=4 (skip next), type=0xA (local coord)
	writeCmd16(v, 0x00, 0x400A) // jp=4, type=0xA
	writeCmd16(v, 0x0C, 99)     // XA = 99
	writeCmd16(v, 0x0E, 88)     // YA = 88

	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Command should be skipped, local coords should remain at reset defaults
	if v.localX != 0 || v.localY != 0 {
		t.Errorf("local coords = (%d,%d), want (0,0) after skip", v.localX, v.localY)
	}
}

// --- Normal Sprite Drawing Tests ---

func TestDrawNormalSprite4bpp(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 sprite, mode 0 (16-color bank), CMDCOLR=0x0010
	writeCmd16(v, 0x00, 0x0000)   // type=0, jp=0
	writeCmd16(v, 0x04, 0x0000)   // CMDPMOD: mode 0, SPD off, ECD off
	writeCmd16(v, 0x06, 0x0010)   // CMDCOLR
	writeCmd16(v, 0x08, 0x1000/8) // CMDSRCA -> char at 0x1000
	writeCmd16(v, 0x0A, 0x0101)   // CMDSIZE: width=1*8=8, height=1
	writeCmd16(v, 0x0C, 0)        // XA
	writeCmd16(v, 0x0E, 0)        // YA

	writeDrawEnd(v, 0x20)

	// Character data at 0x1000: 4bpp, 8 pixels = 4 bytes
	// Pixels: 0, 1, 2, 3, 4, 5, 6, 7
	v.WriteVRAM(0x1000, 0x01) // px0=0, px1=1
	v.WriteVRAM(0x1001, 0x23) // px2=2, px3=3
	v.WriteVRAM(0x1002, 0x45) // px4=4, px5=5
	v.WriteVRAM(0x1003, 0x67) // px6=6, px7=7

	v.VBlankIn()
	drainDrawing(v)

	// Pixel 0 is dot=0, transparent (SPD off), so not written
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0000 (transparent)", got)
	}
	// Pixel 1: dot=1, color = 0x0010 | 1 = 0x0011
	if got := readFBPixel(v, 1, 0); got != 0x0011 {
		t.Errorf("px(1,0) = 0x%04X, want 0x0011", got)
	}
	// Pixel 7: dot=7, color = 0x0010 | 7 = 0x0017
	if got := readFBPixel(v, 7, 0); got != 0x0017 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0017", got)
	}
}

func TestDrawNormalSprite8bpp(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 sprite, mode 4 (256-color bank), CMDCOLR=0x0100
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0020) // CMDPMOD: color mode 4 (bits 5-3 = 100)
	writeCmd16(v, 0x06, 0x0100) // CMDCOLR
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// 8bpp: 8 bytes for 8 pixels
	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}

	v.VBlankIn()
	drainDrawing(v)

	// Pixel 0: dot=0x10, color = 0x0100 | 0x10 = 0x0110
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// Pixel 7: dot=0x17, color = 0x0100 | 0x17 = 0x0117
	if got := readFBPixel(v, 7, 0); got != 0x0117 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0117", got)
	}
}

func TestDrawNormalSprite64Color(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 sprite, mode 2 (64-color bank), CMDCOLR=0x0240
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0010) // CMDPMOD: color mode 2 (bits 5-3 = 010)
	writeCmd16(v, 0x06, 0x0240) // CMDCOLR (upper 10 bits: 0x0240 & 0xFFC0 = 0x0240)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// 8bpp: 8 bytes for 8 pixels
	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}

	v.VBlankIn()
	drainDrawing(v)

	// Pixel 0: dot=0x10, color = (0x0240 & 0xFFC0) | (0x10 & 0x3F) = 0x0240 | 0x10 = 0x0250
	if got := readFBPixel(v, 0, 0); got != 0x0250 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0250", got)
	}
	// Pixel 7: dot=0x17, color = 0x0240 | 0x17 = 0x0257
	if got := readFBPixel(v, 7, 0); got != 0x0257 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0257", got)
	}
}

func TestDrawNormalSprite128Color(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 sprite, mode 3 (128-color bank), CMDCOLR=0x0380
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0018) // CMDPMOD: color mode 3 (bits 5-3 = 011)
	writeCmd16(v, 0x06, 0x0380) // CMDCOLR (upper 9 bits: 0x0380 & 0xFF80 = 0x0380)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// 8bpp: 8 bytes for 8 pixels
	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}

	v.VBlankIn()
	drainDrawing(v)

	// Pixel 0: dot=0x10, color = (0x0380 & 0xFF80) | (0x10 & 0x7F) = 0x0380 | 0x10 = 0x0390
	if got := readFBPixel(v, 0, 0); got != 0x0390 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0390", got)
	}
	// Pixel 7: dot=0x17, color = 0x0380 | 0x17 = 0x0397
	if got := readFBPixel(v, 7, 0); got != 0x0397 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0397", got)
	}
}

func TestDrawNormalSprite16bpp(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 sprite, mode 5 (32768-color RGB)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0028) // CMDPMOD: color mode 5 (bits 5-3 = 101)
	writeCmd16(v, 0x06, 0x0000)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// 16bpp: 2 bytes per pixel, 8 pixels = 16 bytes
	writeCmd16(v, 0x1000, 0x0001) // pixel 0 (non-zero so not transparent)
	writeCmd16(v, 0x1002, 0x7C00) // pixel 1
	writeCmd16(v, 0x100E, 0x03E0) // pixel 7

	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 0, 0); got != 0x0001 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0001", got)
	}
	if got := readFBPixel(v, 1, 0); got != 0x7C00 {
		t.Errorf("px(1,0) = 0x%04X, want 0x7C00", got)
	}
	if got := readFBPixel(v, 7, 0); got != 0x03E0 {
		t.Errorf("px(7,0) = 0x%04X, want 0x03E0", got)
	}
}

func TestDrawNormalSpriteCLUT(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 sprite, mode 1 (16-color CLUT)
	// CLUT at 0x2000 (CMDCOLR = 0x2000/8 = 0x400)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0008) // CMDPMOD: color mode 1 (bits 5-3 = 001)
	writeCmd16(v, 0x06, 0x0400) // CMDCOLR = 0x2000/8
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// CLUT entries at 0x2000
	writeCmd16(v, 0x2000, 0x0000) // color 0 (transparent)
	writeCmd16(v, 0x2002, 0x001F) // color 1 = red
	writeCmd16(v, 0x2004, 0x03E0) // color 2 = green

	// Character data: 4bpp
	v.WriteVRAM(0x1000, 0x01) // px0=0, px1=1
	v.WriteVRAM(0x1001, 0x20) // px2=2, px3=0

	v.VBlankIn()
	drainDrawing(v)

	// px0: dot=0, transparent
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0000 (transparent)", got)
	}
	// px1: dot=1, CLUT[1] = 0x001F
	if got := readFBPixel(v, 1, 0); got != 0x001F {
		t.Errorf("px(1,0) = 0x%04X, want 0x001F", got)
	}
	// px2: dot=2, CLUT[2] = 0x03E0
	if got := readFBPixel(v, 2, 0); got != 0x03E0 {
		t.Errorf("px(2,0) = 0x%04X, want 0x03E0", got)
	}
}

func TestTransparentPixel(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Mode 0, SPD off (default)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0000) // SPD=0
	writeCmd16(v, 0x06, 0x0010)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	v.WriteVRAM(0x1000, 0x01) // px0=0 (transparent), px1=1

	v.VBlankIn()
	drainDrawing(v)

	// Dot=0 should NOT be written
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("transparent pixel = 0x%04X, want 0x0000", got)
	}

	// Now test with SPD on: dot=0 should be written
	v2 := NewVDP1(NewSCU())
	v2.Write(0x04, 2)

	writeCmd16(v2, 0x00, 0x0000)
	writeCmd16(v2, 0x04, 0x0040) // SPD=1 (bit 6)
	writeCmd16(v2, 0x06, 0x0010)
	writeCmd16(v2, 0x08, 0x1000/8)
	writeCmd16(v2, 0x0A, 0x0101)
	writeCmd16(v2, 0x0C, 0)
	writeCmd16(v2, 0x0E, 0)

	writeDrawEnd(v2, 0x20)

	v2.WriteVRAM(0x1000, 0x01) // px0=0, px1=1

	v2.VBlankIn()
	drainDrawing(v2)

	// dot=0 with SPD on: should be written as (0x0010 | 0) = 0x0010
	if got := readFBPixel(v2, 0, 0); got != 0x0010 {
		t.Errorf("SPD pixel = 0x%04X, want 0x0010", got)
	}
}

func TestEndCode4bpp(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Mode 0, ECD off (default)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0000)
	writeCmd16(v, 0x06, 0x0010)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// Pixels: 1, EC, 2, 3, EC, 4, 5, 6
	v.WriteVRAM(0x1000, 0x1F) // px0=1, px1=0xF (end code)
	v.WriteVRAM(0x1001, 0x23) // px2=2, px3=3
	v.WriteVRAM(0x1002, 0xF4) // px4=0xF (end code), px5=4
	v.WriteVRAM(0x1003, 0x56) // px6=5, px7=6

	v.VBlankIn()
	drainDrawing(v)

	// px0: dot=1, drawn
	if got := readFBPixel(v, 0, 0); got != 0x0011 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0011", got)
	}
	// px1: first end code, not drawn (transparent)
	if got := readFBPixel(v, 1, 0); got != 0 {
		t.Errorf("px(1,0) = 0x%04X, want 0x0000 (first end code)", got)
	}
	// px2: dot=2, drawn (row continues after first end code)
	if got := readFBPixel(v, 2, 0); got != 0x0012 {
		t.Errorf("px(2,0) = 0x%04X, want 0x0012", got)
	}
	// px3: dot=3, drawn
	if got := readFBPixel(v, 3, 0); got != 0x0013 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0013", got)
	}
	// px4: second end code, terminates row
	if got := readFBPixel(v, 4, 0); got != 0 {
		t.Errorf("px(4,0) = 0x%04X, want 0x0000 (second end code)", got)
	}
	// px5: should NOT be drawn (after second end code)
	if got := readFBPixel(v, 5, 0); got != 0 {
		t.Errorf("px(5,0) = 0x%04X, want 0x0000 (after second end code)", got)
	}
}

func TestEndCodeDisable(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Mode 0, ECD on (bit 7)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0080) // ECD=1
	writeCmd16(v, 0x06, 0x0010)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// px0=1, px1=0xF, px2=2
	v.WriteVRAM(0x1000, 0x1F)
	v.WriteVRAM(0x1001, 0x20)

	v.VBlankIn()
	drainDrawing(v)

	// With ECD on, 0xF should be treated as a normal color, not end code
	// dot=0xF -> (0x0010 | 0xF) = 0x001F
	if got := readFBPixel(v, 1, 0); got != 0x001F {
		t.Errorf("px(1,0) = 0x%04X, want 0x001F (ECD disabled)", got)
	}
	// px2 should also be drawn
	if got := readFBPixel(v, 2, 0); got != 0x0012 {
		t.Errorf("px(2,0) = 0x%04X, want 0x0012", got)
	}
}

func TestEndCodeSingleDoesNotTerminate(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Mode 0 (4bpp), ECD off. Single end code should NOT terminate row.
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0000)
	writeCmd16(v, 0x06, 0x0010)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeDrawEnd(v, 0x20)

	// Pixels: 1, 2, EC, 3, 4, 5, 6, 7
	v.WriteVRAM(0x1000, 0x12) // px0=1, px1=2
	v.WriteVRAM(0x1001, 0xF3) // px2=0xF (end code), px3=3
	v.WriteVRAM(0x1002, 0x45) // px4=4, px5=5
	v.WriteVRAM(0x1003, 0x67) // px6=6, px7=7

	v.VBlankIn()
	drainDrawing(v)

	// px2 is first (and only) end code: skipped but row continues
	if got := readFBPixel(v, 2, 0); got != 0 {
		t.Errorf("px(2,0) = 0x%04X, want 0x0000 (end code transparent)", got)
	}
	// px3 drawn (row continues after single end code)
	if got := readFBPixel(v, 3, 0); got != 0x0013 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0013", got)
	}
	// px7 drawn (entire row rendered)
	if got := readFBPixel(v, 7, 0); got != 0x0017 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0017", got)
	}
}

func TestSpriteFlipH(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 sprite, mode 0, flipH
	writeCmd16(v, 0x00, 0x0010) // bit 4 = flipH
	writeCmd16(v, 0x04, 0x0000)
	writeCmd16(v, 0x06, 0x0000)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// Pixels: 0,1,2,3,4,5,6,7
	v.WriteVRAM(0x1000, 0x01) // 0,1
	v.WriteVRAM(0x1001, 0x23) // 2,3
	v.WriteVRAM(0x1002, 0x45) // 4,5
	v.WriteVRAM(0x1003, 0x67) // 6,7

	v.VBlankIn()
	drainDrawing(v)

	// With flipH, screen pixel 0 reads from char pixel 7 (dot=7)
	// dot=7, color = 0x0000 | 7 = 0x0007
	if got := readFBPixel(v, 0, 0); got != 0x0007 {
		t.Errorf("flipH px(0,0) = 0x%04X, want 0x0007", got)
	}
	// Screen pixel 7 reads from char pixel 0 (dot=0, transparent)
	if got := readFBPixel(v, 7, 0); got != 0 {
		t.Errorf("flipH px(7,0) = 0x%04X, want 0x0000 (transparent)", got)
	}
	// Screen pixel 1 reads from char pixel 6 (dot=6)
	if got := readFBPixel(v, 1, 0); got != 0x0006 {
		t.Errorf("flipH px(1,0) = 0x%04X, want 0x0006", got)
	}
}

func TestSpriteFlipV(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x2 sprite, mode 0, flipV
	writeCmd16(v, 0x00, 0x0020) // bit 5 = flipV
	writeCmd16(v, 0x04, 0x0000)
	writeCmd16(v, 0x06, 0x0000)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// Row 0: pixels 0,1,0,0,0,0,0,0
	v.WriteVRAM(0x1000, 0x01)
	v.WriteVRAM(0x1001, 0x00)
	v.WriteVRAM(0x1002, 0x00)
	v.WriteVRAM(0x1003, 0x00)
	// Row 1: pixels 0,2,0,0,0,0,0,0
	v.WriteVRAM(0x1004, 0x02)
	v.WriteVRAM(0x1005, 0x00)
	v.WriteVRAM(0x1006, 0x00)
	v.WriteVRAM(0x1007, 0x00)

	v.VBlankIn()
	drainDrawing(v)

	// With flipV, screen row 0 reads from char row 1
	// Screen (1,0) should have dot=2 from char row 1
	if got := readFBPixel(v, 1, 0); got != 0x0002 {
		t.Errorf("flipV px(1,0) = 0x%04X, want 0x0002", got)
	}
	// Screen (1,1) should have dot=1 from char row 0
	if got := readFBPixel(v, 1, 1); got != 0x0001 {
		t.Errorf("flipV px(1,1) = 0x%04X, want 0x0001", got)
	}
}

func TestSpriteClipping(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// System clip to 319x223 (default)
	// Draw 8x1 sprite at x=-2 (partially off left edge)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0000)
	writeCmd16(v, 0x06, 0x0010)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	// XA = -2 as signed 16-bit = 0xFFFE
	writeCmd16(v, 0x0C, 0xFFFE)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	// All non-zero pixels
	v.WriteVRAM(0x1000, 0x11)
	v.WriteVRAM(0x1001, 0x11)
	v.WriteVRAM(0x1002, 0x11)
	v.WriteVRAM(0x1003, 0x11)

	v.VBlankIn()
	drainDrawing(v)

	// Pixels at x=-2 and x=-1 should be clipped
	// Pixel at x=0 should be drawn (srcX=2, dot=1)
	if got := readFBPixel(v, 0, 0); got != 0x0011 {
		t.Errorf("clipped px(0,0) = 0x%04X, want 0x0011", got)
	}
	// Pixel at x=5 should be drawn (srcX=7, dot=1)
	if got := readFBPixel(v, 5, 0); got != 0x0011 {
		t.Errorf("clipped px(5,0) = 0x%04X, want 0x0011", got)
	}
}

func TestSpriteMSBOn(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Mode 0, MSB on (bit 15 of CMDPMOD)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x8000) // MSB on
	writeCmd16(v, 0x06, 0x0010)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)

	writeDrawEnd(v, 0x20)

	v.WriteVRAM(0x1000, 0x01) // px0=0(transp), px1=1

	v.VBlankIn()
	drainDrawing(v)

	// px1: color = 0x0010 | 1 = 0x0011, with MSB = 0x8011
	if got := readFBPixel(v, 1, 0); got != 0x8011 {
		t.Errorf("MSB px(1,0) = 0x%04X, want 0x8011", got)
	}
}

func TestLocalCoordinateOffset(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// First command: set local coords to (10, 20)
	writeCmd16(v, 0x00, 0x000A) // type=0xA
	writeCmd16(v, 0x0C, 10)
	writeCmd16(v, 0x0E, 20)

	// Second command at 0x20: draw 8x1 sprite at XA=5, YA=3
	writeCmd16(v, 0x20, 0x0000)
	writeCmd16(v, 0x24, 0x0000)
	writeCmd16(v, 0x26, 0x0010)
	writeCmd16(v, 0x28, 0x1000/8)
	writeCmd16(v, 0x2A, 0x0101) // 8x1
	writeCmd16(v, 0x2C, 5)      // XA=5
	writeCmd16(v, 0x2E, 3)      // YA=3

	writeDrawEnd(v, 0x40)

	v.WriteVRAM(0x1000, 0x11) // all dot=1

	v.VBlankIn()
	drainDrawing(v)

	// Sprite should be at (5+10, 3+20) = (15, 23)
	if got := readFBPixel(v, 15, 23); got != 0x0011 {
		t.Errorf("offset px(15,23) = 0x%04X, want 0x0011", got)
	}
	// (14, 23) should be empty (before sprite)
	if got := readFBPixel(v, 14, 23); got != 0 {
		t.Errorf("offset px(14,23) = 0x%04X, want 0x0000", got)
	}
}

// --- Frame Buffer Swap Tests ---

func TestFBSwap(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)
	v.Write(0x06, 0x5555)

	writeDrawEnd(v, 0)

	// First frame: VBlankIn swaps (both empty), erases new drawFB
	v.VBlankIn()
	drainDrawing(v)

	// drawFB was erased with 0x5555
	if got := readFBPixel(v, 0, 0); got != 0x5555 {
		t.Errorf("draw (0,0) = 0x%04X, want 0x5555", got)
	}
	// displayFB has old drawFB content (zeros)
	if got := readDisplayFBPixel(v, 0, 0); got != 0 {
		t.Errorf("display (0,0) = 0x%04X, want 0x0000", got)
	}

	// Second frame: VBlankIn swaps erased content to display
	v.VBlankIn()
	drainDrawing(v)

	// displayFB now has the previously erased content
	if got := readDisplayFBPixel(v, 0, 0); got != 0x5555 {
		t.Errorf("display (0,0) after swap = 0x%04X, want 0x5555", got)
	}
}

func TestAutoModeSwapAndErase(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x06, 0xAAAA)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)

	// Draw a sprite that writes pixel at (0,0)
	writeCmd16(v, 0x00, 0x0000) // normal sprite
	writeCmd16(v, 0x04, 0x0068) // SPD=1, ECD=1, mode 5
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeDrawEnd(v, 0x20)
	writeCmd16(v, 0x1000, 0x8001)

	// Frame 1: swap+erase, draw
	v.VBlankIn()
	drainDrawing(v)

	// drawFB: erased to 0xAAAA then sprite drawn at (0,0)
	if got := readFBPixel(v, 0, 0); got != 0x8001 {
		t.Errorf("frame1 draw(0,0) = 0x%04X, want 0x8001", got)
	}
	// drawFB at non-sprite pixel has erase color
	if got := readFBPixel(v, 100, 100); got != 0xAAAA {
		t.Errorf("frame1 draw(100,100) = 0x%04X, want 0xAAAA", got)
	}

	// Frame 2: swap moves drawn content to display, erase new drawFB
	v.VBlankIn()
	drainDrawing(v)

	// displayFB now has frame 1's drawn content
	if got := readDisplayFBPixel(v, 0, 0); got != 0x8001 {
		t.Errorf("frame2 display(0,0) = 0x%04X, want 0x8001", got)
	}
}

func TestManualErase(t *testing.T) {
	// Per VDP1 manual Sec.4.2: "Erase/write of the display frame
	// buffer is performed in the next specified field." So FCM=1
	// FCT=0 with an SH-2 FBCR write schedules a deferred erase of
	// the display buffer; the actual erase fires after that field's
	// VDP2 read is done (PerformLateErase). drawFB stays untouched
	// this field; the next FCT=1 swap moves the now-erased buffer
	// over to drawFB so the game writes into a clean buffer.
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x06, 0xBBBB)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)

	v.Write(0x02, 0x02) // FCM=1, FCT=0; fbcrWritten=true

	writeDrawEnd(v, 0)

	v.drawFB[0] = 0x12
	v.drawFB[1] = 0x34
	v.displayFB[0] = 0x56
	v.displayFB[1] = 0x78

	v.VBlankIn()
	drainDrawing(v)

	// VBlankIn schedules the erase but does not perform it yet.
	if !v.lateEraseDisplayFB {
		t.Error("lateEraseDisplayFB should be set after FCM=1 FCT=0 + fbcrWritten")
	}
	// Both buffers are still untouched at this point.
	if got := readFBPixel(v, 0, 0); got != 0x1234 {
		t.Errorf("pre-late-erase draw(0,0) = 0x%04X, want 0x1234", got)
	}
	if got := readDisplayFBPixel(v, 0, 0); got != 0x5678 {
		t.Errorf("pre-late-erase display(0,0) = 0x%04X, want 0x5678", got)
	}

	// PerformLateErase runs after VDP2 has consumed this field's
	// display buffer; only then is the displayFB erased to EWDR.
	v.PerformLateErase()

	if v.lateEraseDisplayFB {
		t.Error("lateEraseDisplayFB should be cleared after PerformLateErase")
	}
	if got := readDisplayFBPixel(v, 0, 0); got != 0xBBBB {
		t.Errorf("post-late-erase display(0,0) = 0x%04X, want 0xBBBB", got)
	}
	if got := readFBPixel(v, 0, 0); got != 0x1234 {
		t.Errorf("post-late-erase draw(0,0) = 0x%04X, want 0x1234 (untouched)", got)
	}
}

func TestManualChange(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x06, 0xCCCC)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)

	writeDrawEnd(v, 0)

	// First: draw something into drawFB using auto mode
	v.VBlankIn()
	v.drawFB[0] = 0xAA
	v.drawFB[1] = 0xBB
	drainDrawing(v)

	// Switch to manual change: FCM=1, FCT=1 (swap, no erase)
	v.Write(0x02, 0x03)

	v.VBlankIn()
	drainDrawing(v)

	// After manual change: buffers swapped
	// displayFB should have our 0xAABB marker
	if got := readDisplayFBPixel(v, 0, 0); got != 0xAABB {
		t.Errorf("manual change display(0,0) = 0x%04X, want 0xAABB", got)
	}
	// drawFB should NOT be erased (manual change doesn't erase)
	// It contains whatever was in the old displayFB
}

func TestManualEraseAndChange(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x06, 0xDDDD)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)

	writeDrawEnd(v, 0)

	// Draw a marker into drawFB via auto mode first
	v.VBlankIn()
	v.drawFB[0] = 0x11
	v.drawFB[1] = 0x22
	drainDrawing(v)

	// VBE=1, FCM=1, FCT=1: erase display buffer then swap
	v.tvmr = 0x08       // VBE=1
	v.Write(0x02, 0x03) // FCM=1, FCT=1

	v.VBlankIn()
	drainDrawing(v)

	// After erase+change: displayFB was erased then swapped
	// displayFB should have our 0x1122 marker (from drawFB before swap)
	if got := readDisplayFBPixel(v, 0, 0); got != 0x1122 {
		t.Errorf("erase+change display(0,0) = 0x%04X, want 0x1122", got)
	}
	// drawFB should have the erased content (0xDDDD was written to old displayFB before swap)
	if got := readFBPixel(v, 0, 0); got != 0xDDDD {
		t.Errorf("erase+change draw(0,0) = 0x%04X, want 0xDDDD", got)
	}
}

// TestManualChangePulsedVBE reproduces the smearing pattern observed in
// games that toggle TVMR.VBE high-then-low across the VBlank boundary.
// Real hardware samples VBE during V-blank; the spec only requires VBE=1
// to be set when FCM=FCT=1. Because the emulator batches all system cycles
// before VBlankIn, both the rising and falling edges of VBE land in the
// same batch and a naive read of TVMR at VBlankIn sees VBE=0. The fix
// latches the rising edge so the next VBlankIn still triggers the erase.
func TestManualChangePulsedVBE(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x06, 0xDEAD)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)

	writeDrawEnd(v, 0)

	// Bootstrap: auto mode draw to plant a marker into drawFB.
	v.VBlankIn()
	v.drawFB[0] = 0x11
	v.drawFB[1] = 0x22
	drainDrawing(v)

	// Switch to manual change (FCM=1 FCT=1) and pulse VBE high then low.
	v.Write(0x02, 0x03)
	v.Write(0x00, 0x0008) // VBE=1
	v.Write(0x00, 0x0000) // VBE=0 - both writes happen before VBlankIn

	v.VBlankIn()
	drainDrawing(v)

	// Expected: hardware-equivalent VBE+change behavior - erase the
	// outgoing displayFB then swap. After swap, the new displayFB
	// holds the 0x1122 marker (was drawFB) and the new drawFB holds
	// the 0xDEAD erase fill (was the erased displayFB).
	if got := readDisplayFBPixel(v, 0, 0); got != 0x1122 {
		t.Errorf("VBE-pulse display(0,0) = 0x%04X, want 0x1122", got)
	}
	if got := readFBPixel(v, 0, 0); got != 0xDEAD {
		t.Errorf("VBE-pulse draw(0,0) = 0x%04X, want 0xDEAD", got)
	}
}

func TestManualModeNoSwapOnErase(t *testing.T) {
	// FCM=1 FCT=0 + fbcrWritten triggers a deferred displayFB erase
	// without swapping. Verify that no swap occurred (drawFB and
	// displayFB pointers stay the same) - the deferred erase is for
	// the display buffer; the draw buffer is left alone.
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x06, 0xEEEE)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)

	v.Write(0x02, 0x02) // FCM=1, FCT=0; fbcrWritten=true

	writeDrawEnd(v, 0)

	preDraw := v.drawFB
	preDisplay := v.displayFB
	v.drawFB[0] = 0xAA
	v.drawFB[1] = 0xBB
	v.displayFB[0] = 0xCC
	v.displayFB[1] = 0xDD

	v.VBlankIn()
	drainDrawing(v)

	// No swap should have occurred: pointers identical to pre-VBlankIn.
	if &v.drawFB[0] != &preDraw[0] {
		t.Error("drawFB pointer changed; FCM=1 FCT=0 should not swap")
	}
	if &v.displayFB[0] != &preDisplay[0] {
		t.Error("displayFB pointer changed; FCM=1 FCT=0 should not swap")
	}
	// drawFB content untouched (no erase, no draw output).
	if got := readFBPixel(v, 0, 0); got != 0xAABB {
		t.Errorf("no-swap draw(0,0) = 0x%04X, want 0xAABB", got)
	}
	// Deferred-erase flag set, displayFB still untouched until
	// PerformLateErase runs.
	if !v.lateEraseDisplayFB {
		t.Error("lateEraseDisplayFB should be set")
	}
	if got := readDisplayFBPixel(v, 0, 0); got != 0xCCDD {
		t.Errorf("pre-late-erase display(0,0) = 0x%04X, want 0xCCDD", got)
	}
}

// --- VBlankOut (vbout-handler manual-swap path) tests ---

// TestVBlankOutManualSwap covers the FCM=1+FCT=1 swap that VBlankOut
// catches when the SH-2's vbout handler writes FBCR=0003 at line 0
// of a new frame. VBlankIn already cleared any FCT=1 written before
// line 224 of the previous frame, so an FCT still set when VBlankOut
// runs is necessarily a fresh write from the handler. The swap must
// fire here so PTM=10 auto-draw can run within the same Saturn frame.
func TestVBlankOutManualSwap(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x02, 0x03) // FCM=1, FCT=1 written by vbout handler

	preDraw := &v.drawFB[0]
	preDisplay := &v.displayFB[0]
	preSwapCount := v.swapCount

	v.VBlankOut()

	if &v.drawFB[0] == preDraw {
		t.Error("VBlankOut did not swap drawFB pointer")
	}
	if &v.displayFB[0] == preDisplay {
		t.Error("VBlankOut did not swap displayFB pointer")
	}
	if v.fbcr&0x01 != 0 {
		t.Errorf("VBlankOut left FCT set: fbcr=0x%02X, want bit 0 clear", v.fbcr)
	}
	if !v.fbSwapped {
		t.Error("VBlankOut did not set fbSwapped=true")
	}
	if v.swapCount != preSwapCount+1 {
		t.Errorf("swapCount = %d, want %d (+1)", v.swapCount, preSwapCount+1)
	}
}

// TestVBlankOutClearsFbcrWritten verifies VBlankOut clears
// fbcrWritten after a swap. Without this, the trailing VBlankIn would
// see FCM=1+FCT=0+fbcrWritten=true and incorrectly schedule a
// lateEraseDisplayFB - the FCT auto-clear inside VBlankOut consumed
// the same FBCR write that drove the swap.
func TestVBlankOutClearsFbcrWritten(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x02, 0x03) // sets fbcrWritten=true

	v.VBlankOut()

	if v.fbcrWritten {
		t.Error("VBlankOut did not clear fbcrWritten after swap")
	}

	// Confirm the trailing VBlankIn does not schedule a late erase.
	v.VBlankIn()
	if v.lateEraseDisplayFB {
		t.Error("VBlankIn after VBlankOut wrongly scheduled lateEraseDisplayFB; FCT auto-clear should not look like a fresh erase request")
	}
}

// TestVBlankOutNoSwapFCM0 ensures FCM=0 (auto swap mode) is left
// entirely to VBlankIn. VBlankOut must not double-swap when the
// game uses auto-swap.
func TestVBlankOutNoSwapFCM0(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x02, 0x01) // FCM=0, FCT=1 - irrelevant pairing under auto mode

	preDraw := &v.drawFB[0]
	preDisplay := &v.displayFB[0]
	preSwapCount := v.swapCount

	v.VBlankOut()

	if &v.drawFB[0] != preDraw {
		t.Error("VBlankOut swapped under FCM=0; auto-swap must be VBlankIn-only")
	}
	if &v.displayFB[0] != preDisplay {
		t.Error("VBlankOut swapped under FCM=0; auto-swap must be VBlankIn-only")
	}
	if v.swapCount != preSwapCount {
		t.Errorf("swapCount changed = %d, want %d (no swap)", v.swapCount, preSwapCount)
	}
}

// TestVBlankOutNoSwapFCT0 confirms FCT=0 is a no-op at VBlankOut.
// Either the swap already happened at the prior VBlankIn (FCT was
// auto-cleared there) or the game never asked for one - both cases
// should leave VBlankOut idle.
func TestVBlankOutNoSwapFCT0(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x02, 0x02) // FCM=1, FCT=0

	preDraw := &v.drawFB[0]
	preDisplay := &v.displayFB[0]
	preSwapCount := v.swapCount

	v.VBlankOut()

	if &v.drawFB[0] != preDraw {
		t.Error("VBlankOut swapped despite FCT=0")
	}
	if &v.displayFB[0] != preDisplay {
		t.Error("VBlankOut swapped despite FCT=0")
	}
	if v.swapCount != preSwapCount {
		t.Errorf("swapCount changed = %d, want %d", v.swapCount, preSwapCount)
	}
}

// TestVBlankOutDIESkip confirms DIE=1 (double-density interlace) does
// not swap at VBlankOut; that path is owned by VBlankIn for
// per-field bookkeeping. DIE bit (FBCR bit 3) is shadow-deferred so
// latchPending() simulates the line-224 commit before the line-0
// VBlankOut runs.
func TestVBlankOutDIESkip(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.tvmr = 0x0001     // TVM=1 (required for dieDoubled)
	v.Write(0x02, 0x0B) // DIE=1, FCM=1, FCT=1
	v.latchPending()    // commit DIE into active fbcr

	preDraw := &v.drawFB[0]
	preDisplay := &v.displayFB[0]

	v.VBlankOut()

	if &v.drawFB[0] != preDraw || &v.displayFB[0] != preDisplay {
		t.Error("VBlankOut swapped under DIE=1; DIE bookkeeping must stay in VBlankIn")
	}
}

// TestVBlankOutNoDoubleSwap is the integration scenario for a game
// that writes FBCR=0003 mid-frame (BIOS-animation style): VBlankIn
// at line 224 of frame N already swapped and cleared FCT, so when
// VBlankOut runs at line 0 of frame N+1 it must observe FCT=0 and
// do nothing.
func TestVBlankOutNoDoubleSwap(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x02, 0x03) // FCM=1, FCT=1 written mid-frame

	v.VBlankIn() // VBlankIn handles the swap for mid-frame FCT=1

	if v.fbcr&0x01 != 0 {
		t.Fatalf("VBlankIn did not auto-clear FCT: fbcr=0x%02X", v.fbcr)
	}
	preDraw := &v.drawFB[0]
	preDisplay := &v.displayFB[0]
	preSwapCount := v.swapCount

	v.VBlankOut() // line 0 of next frame; FCT already cleared, no swap

	if &v.drawFB[0] != preDraw || &v.displayFB[0] != preDisplay {
		t.Error("VBlankOut double-swapped after VBlankIn already swapped")
	}
	if v.swapCount != preSwapCount {
		t.Errorf("swapCount changed = %d, want %d", v.swapCount, preSwapCount)
	}
}

// TestVBlankOutEDSRTransition checks the BEF/CEF latch on the
// VBlankOut-driven swap. Per VDP1 manual Sec.4.6, BEF latches the
// prior CEF when the framebuffer changes. Set CEF=1 pre-swap and
// expect EDSR = 0x0003 post-swap (BEF<-old CEF=1, CEF retained).
func TestVBlankOutEDSRTransition(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.edsr = 0x02       // CEF=1, BEF=0
	v.Write(0x02, 0x03) // FCM=1, FCT=1

	v.VBlankOut()

	if v.edsr != 0x03 {
		t.Errorf("post-swap EDSR = 0x%02X, want 0x03 (BEF<-old CEF=1, CEF retained)", v.edsr)
	}
}

// TestVBlankOutVBEErase verifies the FCT=1+VBE=1 erase-then-swap
// path. Mirrors VBlankIn's behavior for the same combo, but at the
// vblank-OUT site.
func TestVBlankOutVBEErase(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x06, 0xBEEF) // EWDR
	v.Write(0x08, 0x0000) // EWLR (top-left)
	v.Write(0x0A, 0x50FF) // EWRR (bottom-right)
	v.latchPending()      // commit erase params

	// Plant markers we expect to see survive the swap.
	v.drawFB[0] = 0x12
	v.drawFB[1] = 0x34
	// The pre-swap displayFB will be erased then swapped, becoming the
	// new drawFB; its previous content is gone.

	v.tvmr = 0x08       // VBE=1
	v.Write(0x02, 0x03) // FCM=1, FCT=1

	v.VBlankOut()

	// Old drawFB content is now on display.
	if got := readDisplayFBPixel(v, 0, 0); got != 0x1234 {
		t.Errorf("VBE+FCT=1 display(0,0) = 0x%04X, want 0x1234", got)
	}
	// Old displayFB was erased to EWDR; it's now the drawFB.
	if got := readFBPixel(v, 0, 0); got != 0xBEEF {
		t.Errorf("VBE+FCT=1 draw(0,0) = 0x%04X, want 0xBEEF (erased to EWDR)", got)
	}
}

// --- PTMR write all-immediate (matches mednafen's direct write) ---

// TestPTMRPTM10Deferred verifies that a PTM=10 write lands in
// ptmrPending and does NOT update the live ptmr immediately. Per VDP1
// manual Sec.3, PTM=01 is the only immediate value; PTM=00 and PTM=10
// are committed at the next FB switch via latchPending().
func TestPTMRPTM10Deferred(t *testing.T) {
	v := NewVDP1(NewSCU())

	v.Write(0x04, 0x01) // PTM=01 manual - immediate
	if v.ptmr != 1 {
		t.Fatalf("after PTMR=01, ptmr=%d, want 1 (immediate)", v.ptmr)
	}
	v.Write(0x04, 0x02) // PTM=10 auto - deferred
	if v.ptmrPending != 2 {
		t.Errorf("after PTMR=02, ptmrPending=%d, want 2", v.ptmrPending)
	}
	if v.ptmr != 1 {
		t.Errorf("after PTMR=02, live ptmr=%d, want 1 (PTM=10 is deferred)", v.ptmr)
	}
}

// TestPTMRPTM00Deferred is the PTM=00 counterpart to
// TestPTMRPTM10Deferred: PTM=00 is also deferred to FB switch.
func TestPTMRPTM00Deferred(t *testing.T) {
	v := NewVDP1(NewSCU())

	v.Write(0x04, 0x01) // PTM=01 to give ptmr a known live value
	if v.ptmr != 1 {
		t.Fatalf("after PTMR=01, ptmr=%d, want 1", v.ptmr)
	}
	v.Write(0x04, 0x00) // PTM=00 idle - deferred
	if v.ptmrPending != 0 {
		t.Errorf("after PTMR=00, ptmrPending=%d, want 0", v.ptmrPending)
	}
	if v.ptmr != 1 {
		t.Errorf("after PTMR=00, live ptmr=%d, want 1 (PTM=00 is deferred)", v.ptmr)
	}
}

func TestEDSRBEFCEFCycle(t *testing.T) {
	v := NewVDP1(NewSCU())

	writeDrawEnd(v, 0)

	// Initial EDSR = 0x0003 (BEF=1, CEF=1)
	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("initial EDSR = 0x%04X, want 0x0003", got)
	}

	// After VBlankIn: BEF = old CEF (1), CEF preserved (1) -> EDSR = 0x0003.
	// CEF tracks "drawing currently completed" and persists until a new
	// draw is triggered, NOT cleared every V-Blank.
	v.VBlankIn()
	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("after VBlankIn EDSR = 0x%04X, want 0x0003 (CEF retained)", got)
	}

	// PTM=01 write starts a new draw: CEF clears, BEF unchanged.
	v.Write(0x04, 1)
	if got := v.Read(0x10); got != 0x0001 {
		t.Errorf("after PTM=01 write EDSR = 0x%04X, want 0x0001 (CEF cleared)", got)
	}

	// drainDrawing completes the draw: CEF=1 set again -> EDSR = 0x0003.
	drainDrawing(v)
	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("after draw drain EDSR = 0x%04X, want 0x0003", got)
	}

	// Next VBlankIn: BEF = old CEF (1), CEF preserved (1) -> EDSR = 0x0003.
	v.VBlankIn()
	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("after second VBlankIn EDSR = 0x%04X, want 0x0003 (CEF retained)", got)
	}
}

func TestSpriteDrawEndInterrupt(t *testing.T) {
	scu := NewSCU()
	v := NewVDP1(scu)
	v.Write(0x04, 2)

	writeDrawEnd(v, 0)
	v.VBlankIn()
	drainDrawing(v)

	// IST bit 13 = Sprite Draw End
	if scu.ist&(1<<13) == 0 {
		t.Error("sprite draw end interrupt not raised (IST bit 13)")
	}
}

func TestEDSRStatus(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Initially EDSR = 0x0003
	if got := v.edsr; got != 0x0003 {
		t.Errorf("initial EDSR = 0x%04X, want 0x0003", got)
	}

	writeDrawEnd(v, 0)
	v.VBlankIn()
	drainDrawing(v)

	// After draw: CEF should be set, BEF should be set
	if v.edsr&0x02 == 0 {
		t.Error("CEF should be set after draw")
	}
	if v.edsr&0x01 == 0 {
		t.Error("BEF should be set after draw")
	}
}

func TestEDSRCEFNotSetOnENDRAbort(t *testing.T) {
	// ENDR fired mid-draw must abort without setting CEF or raising the
	// sprite-end interrupt (PDF Sec 4.6). startDraw() now clears
	// drawEnd so a stale latch can't kill the next draw — this test
	// exercises the in-flight termination directly: drawActive is set,
	// drawEnd is latched (as Write(0x0C) would do for an active draw),
	// and processCommands runs without going through startDraw.
	scu := NewSCU()
	v := NewVDP1(scu)
	v.Write(0x04, 2)

	writeDrawEnd(v, 0x20)

	v.VBlankIn()
	v.edsr = 0
	scu.ist = 0
	v.drawActive = true
	v.drawEnd = true
	v.TickSystemCycles(1 << 30)

	if v.edsr&0x02 != 0 {
		t.Error("CEF set after ENDR-aborted draw; should remain 0 per PDF Sec 4.6")
	}
	if scu.ist&(1<<13) != 0 {
		t.Error("sprite-end interrupt raised after ENDR-aborted draw; should not fire per PDF Sec 4.6")
	}
}

// --- Integration ---

func TestBIOSInitialState(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.tvmr = 0x0000
	v.Write(0x02, 0x0000)
	v.Write(0x04, 0x0002)
	v.Write(0x06, 0x0000)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)

	// VRAM[0] = 0x8000 (draw end)
	writeDrawEnd(v, 0)

	// Should complete without panic
	v.VBlankIn()
	drainDrawing(v)

	// Display FB should be erased to 0 (EWDR=0)
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("BIOS initial pixel = 0x%04X, want 0x0000", got)
	}
}

func TestPTMR0NoDraw(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 0)

	// Set up a normal sprite command that would write a visible pixel
	writeCmd16(v, 0x00, 0x0000)   // type=0 normal sprite
	writeCmd16(v, 0x04, 0x0068)   // CMDPMOD: SPD=1, ECD=1, mode 5 (RGB)
	writeCmd16(v, 0x08, 0x1000/8) // CMDSRCA
	writeCmd16(v, 0x0A, 0x0101)   // 8x1
	writeCmd16(v, 0x0C, 0)        // XA=0
	writeCmd16(v, 0x0E, 0)        // YA=0
	writeDrawEnd(v, 0x20)
	writeCmd16(v, 0x1000, 0x8001) // pixel 0: RGB red=1
	writeCmd16(v, 0x1002, 0x8002) // pixel 1

	v.VBlankIn()
	drainDrawing(v)

	// With PTMR=0, commands should not be processed
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("PTMR=0 pixel = 0x%04X, want 0x0000", got)
	}
}

func TestPTMR1ManualDraw(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 1)
	v.drawPending = false

	// Set up a normal sprite that writes a visible pixel at (0,0)
	writeCmd16(v, 0x00, 0x0000)   // type=0 normal sprite
	writeCmd16(v, 0x04, 0x0068)   // CMDPMOD: SPD=1, ECD=1, mode 5 (RGB)
	writeCmd16(v, 0x08, 0x1000/8) // CMDSRCA
	writeCmd16(v, 0x0A, 0x0101)   // 8x1
	writeCmd16(v, 0x0C, 0)        // XA=0
	writeCmd16(v, 0x0E, 0)        // YA=0
	writeDrawEnd(v, 0x20)
	writeCmd16(v, 0x1000, 0x8001) // pixel 0: RGB value

	// Without drawPending, commands should not be processed
	v.VBlankIn()
	drainDrawing(v)
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("manual no pending = 0x%04X, want 0x0000", got)
	}

	// Set drawPending via PTMR write
	v.Write(0x04, 0x0001)
	v.VBlankIn()
	drainDrawing(v)
	if got := readFBPixel(v, 0, 0); got != 0x8001 {
		t.Errorf("manual with pending = 0x%04X, want 0x8001", got)
	}
}

// TestPTMR01To00BeforeDraw covers the spec-mandated edge-trigger
// behavior of PTM=01: writing 01B starts drawing, and a subsequent
// rewrite to 00B does not cancel the already-issued draw (the mode
// change applies from the next frame). Because the emulator batches
// system cycles before TickSystemCycles runs, both writes can land before the
// draw is processed; drawPending must be honored regardless of the
// final PTMR value.
func TestPTMR01To00BeforeDraw(t *testing.T) {
	v := NewVDP1(NewSCU())

	writeCmd16(v, 0x00, 0x0000)   // type=0 normal sprite
	writeCmd16(v, 0x04, 0x0068)   // CMDPMOD: SPD=1, ECD=1, mode 5 (RGB)
	writeCmd16(v, 0x08, 0x1000/8) // CMDSRCA
	writeCmd16(v, 0x0A, 0x0101)   // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeDrawEnd(v, 0x20)
	writeCmd16(v, 0x1000, 0x8001)

	// Game pulses PTMR=01 (trigger) then PTMR=00 (idle) within one
	// SH-2 batch. The 01 write fires the trigger; the 00 write only
	// affects subsequent frames per spec.
	v.Write(0x04, 0x0001) // PTM=01: trigger draw
	v.Write(0x04, 0x0000) // PTM=00: idle from next frame

	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 0, 0); got != 0x8001 {
		t.Errorf("PTM=01 then PTM=00 should still draw: pixel = 0x%04X, want 0x8001", got)
	}
}

// --- Line command (0x6) tests ---

// writeLine sets up a line command at VRAM address 0x00 with the given endpoints and color.
func writeLine(v *VDP1, x1, y1, x2, y2 int16, color uint16) {
	writeCmd16(v, 0x00, 0x0006) // CMDCTRL: command 0x6
	writeCmd16(v, 0x04, 0x0000) // CMDPMOD: color mode 0, ECD=0, SPD=0
	writeCmd16(v, 0x06, color)  // CMDCOLR
	writeCmd16(v, 0x0C, uint16(x1))
	writeCmd16(v, 0x0E, uint16(y1))
	writeCmd16(v, 0x10, uint16(x2))
	writeCmd16(v, 0x12, uint16(y2))
	writeDrawEnd(v, 0x20)
}

func TestDrawLineHorizontal(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeLine(v, 10, 50, 20, 50, 0x1234)
	v.VBlankIn()
	drainDrawing(v)

	// All 11 pixels from x=10 to x=20 should be drawn
	for x := 10; x <= 20; x++ {
		if got := readFBPixel(v, x, 50); got != 0x1234 {
			t.Errorf("px(%d,50) = 0x%04X, want 0x1234", x, got)
		}
	}
	// Adjacent pixels should be empty
	if got := readFBPixel(v, 9, 50); got != 0 {
		t.Errorf("px(9,50) = 0x%04X, want 0x0000", got)
	}
	if got := readFBPixel(v, 21, 50); got != 0 {
		t.Errorf("px(21,50) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawLineVertical(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeLine(v, 50, 10, 50, 20, 0x5678)
	v.VBlankIn()
	drainDrawing(v)

	for y := 10; y <= 20; y++ {
		if got := readFBPixel(v, 50, y); got != 0x5678 {
			t.Errorf("px(50,%d) = 0x%04X, want 0x5678", y, got)
		}
	}
	if got := readFBPixel(v, 50, 9); got != 0 {
		t.Errorf("px(50,9) = 0x%04X, want 0x0000", got)
	}
	if got := readFBPixel(v, 50, 21); got != 0 {
		t.Errorf("px(50,21) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawLineDiagonal(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeLine(v, 0, 0, 7, 7, 0xABCD)
	v.VBlankIn()
	drainDrawing(v)

	// 45-degree line: 8 pixels along the diagonal
	for i := 0; i <= 7; i++ {
		if got := readFBPixel(v, i, i); got != 0xABCD {
			t.Errorf("px(%d,%d) = 0x%04X, want 0xABCD", i, i, got)
		}
	}
	// Pixel off the diagonal should be empty
	if got := readFBPixel(v, 1, 0); got != 0 {
		t.Errorf("px(1,0) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawLineSinglePixel(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeLine(v, 5, 5, 5, 5, 0x1111)
	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 5, 5); got != 0x1111 {
		t.Errorf("px(5,5) = 0x%04X, want 0x1111", got)
	}
	if got := readFBPixel(v, 4, 5); got != 0 {
		t.Errorf("px(4,5) = 0x%04X, want 0x0000", got)
	}
	if got := readFBPixel(v, 6, 5); got != 0 {
		t.Errorf("px(6,5) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawLineNegativeDirection(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Draw right-to-left: same pixels as left-to-right
	writeLine(v, 20, 50, 10, 50, 0x2222)
	v.VBlankIn()
	drainDrawing(v)

	for x := 10; x <= 20; x++ {
		if got := readFBPixel(v, x, 50); got != 0x2222 {
			t.Errorf("px(%d,50) = 0x%04X, want 0x2222", x, got)
		}
	}
}

func TestDrawLineClipping(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set system clip to 15x223
	writeCmd16(v, 0x00, 0x0009) // sys clip command
	writeCmd16(v, 0x14, 15)     // XC
	writeCmd16(v, 0x16, 223)    // YC

	// Line from x=10 to x=20, but clip at x=15
	writeCmd16(v, 0x20, 0x0006) // line command
	writeCmd16(v, 0x24, 0x0000)
	writeCmd16(v, 0x26, 0x3333) // CMDCOLR
	writeCmd16(v, 0x2C, 10)     // XA
	writeCmd16(v, 0x2E, 50)     // YA
	writeCmd16(v, 0x30, 20)     // XB
	writeCmd16(v, 0x32, 50)     // YB

	writeDrawEnd(v, 0x40)
	v.VBlankIn()
	drainDrawing(v)

	// Pixels 10-15 should be drawn
	for x := 10; x <= 15; x++ {
		if got := readFBPixel(v, x, 50); got != 0x3333 {
			t.Errorf("px(%d,50) = 0x%04X, want 0x3333", x, got)
		}
	}
	// Pixel 16 should be clipped
	if got := readFBPixel(v, 16, 50); got != 0 {
		t.Errorf("px(16,50) = 0x%04X, want 0x0000 (clipped)", got)
	}
}

func TestDrawLineMSBOn(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006) // line command
	writeCmd16(v, 0x04, 0x8000) // CMDPMOD: MSB on
	writeCmd16(v, 0x06, 0x0010) // CMDCOLR (no MSB in color)
	writeCmd16(v, 0x0C, 5)      // XA
	writeCmd16(v, 0x0E, 5)      // YA
	writeCmd16(v, 0x10, 10)     // XB
	writeCmd16(v, 0x12, 5)      // YB
	writeDrawEnd(v, 0x20)

	v.VBlankIn()
	drainDrawing(v)

	// All pixels should have MSB set: 0x0010 | 0x8000 = 0x8010
	for x := 5; x <= 10; x++ {
		if got := readFBPixel(v, x, 5); got != 0x8010 {
			t.Errorf("px(%d,5) = 0x%04X, want 0x8010", x, got)
		}
	}
}

func TestDrawLineLocalCoords(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set local coords to (100, 50)
	writeCmd16(v, 0x00, 0x000A) // local coord command
	writeCmd16(v, 0x0C, 100)
	writeCmd16(v, 0x0E, 50)

	// Line from (0,0) to (5,0) relative to local coords
	writeCmd16(v, 0x20, 0x0006)
	writeCmd16(v, 0x24, 0x0000)
	writeCmd16(v, 0x26, 0x4444) // CMDCOLR
	writeCmd16(v, 0x2C, 0)      // XA
	writeCmd16(v, 0x2E, 0)      // YA
	writeCmd16(v, 0x30, 5)      // XB
	writeCmd16(v, 0x32, 0)      // YB

	writeDrawEnd(v, 0x40)
	v.VBlankIn()
	drainDrawing(v)

	// Line should be at screen (100,50) to (105,50)
	for x := 100; x <= 105; x++ {
		if got := readFBPixel(v, x, 50); got != 0x4444 {
			t.Errorf("px(%d,50) = 0x%04X, want 0x4444", x, got)
		}
	}
	if got := readFBPixel(v, 99, 50); got != 0 {
		t.Errorf("px(99,50) = 0x%04X, want 0x0000", got)
	}
}

// --- Polyline command (0x5) tests ---

// writePolyline sets up a polyline command at the given VRAM address.
func writePolyline(v *VDP1, addr uint32, ax, ay, bx, by, cx, cy, dx, dy int16, color uint16) {
	writeCmd16(v, addr+0x00, 0x0005) // CMDCTRL: command 0x5
	writeCmd16(v, addr+0x04, 0x0000) // CMDPMOD
	writeCmd16(v, addr+0x06, color)  // CMDCOLR
	writeCmd16(v, addr+0x0C, uint16(ax))
	writeCmd16(v, addr+0x0E, uint16(ay))
	writeCmd16(v, addr+0x10, uint16(bx))
	writeCmd16(v, addr+0x12, uint16(by))
	writeCmd16(v, addr+0x14, uint16(cx))
	writeCmd16(v, addr+0x16, uint16(cy))
	writeCmd16(v, addr+0x18, uint16(dx))
	writeCmd16(v, addr+0x1A, uint16(dy))
}

func TestDrawPolylineRectangle(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Rectangle: (10,10)-(20,10)-(20,20)-(10,20)
	writePolyline(v, 0x00, 10, 10, 20, 10, 20, 20, 10, 20, 0x1234)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Top edge: y=10, x=10..20
	for x := 10; x <= 20; x++ {
		if got := readFBPixel(v, x, 10); got != 0x1234 {
			t.Errorf("top px(%d,10) = 0x%04X, want 0x1234", x, got)
		}
	}
	// Right edge: x=20, y=10..20
	for y := 10; y <= 20; y++ {
		if got := readFBPixel(v, 20, y); got != 0x1234 {
			t.Errorf("right px(20,%d) = 0x%04X, want 0x1234", y, got)
		}
	}
	// Bottom edge: y=20, x=10..20
	for x := 10; x <= 20; x++ {
		if got := readFBPixel(v, x, 20); got != 0x1234 {
			t.Errorf("bottom px(%d,20) = 0x%04X, want 0x1234", x, got)
		}
	}
	// Left edge: x=10, y=10..20
	for y := 10; y <= 20; y++ {
		if got := readFBPixel(v, 10, y); got != 0x1234 {
			t.Errorf("left px(10,%d) = 0x%04X, want 0x1234", y, got)
		}
	}
	// Interior should be empty
	if got := readFBPixel(v, 15, 15); got != 0 {
		t.Errorf("interior px(15,15) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawPolylineVertexOverlap(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Small rectangle: vertices at corners are shared by two segments
	writePolyline(v, 0x00, 5, 5, 8, 5, 8, 8, 5, 8, 0xAAAA)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Corner (5,5) is endpoint of A-B and D-A: should be drawn
	if got := readFBPixel(v, 5, 5); got != 0xAAAA {
		t.Errorf("corner px(5,5) = 0x%04X, want 0xAAAA", got)
	}
	// Corner (8,8) is endpoint of B-C and C-D: should be drawn
	if got := readFBPixel(v, 8, 8); got != 0xAAAA {
		t.Errorf("corner px(8,8) = 0x%04X, want 0xAAAA", got)
	}
}

func TestDrawPolylineSinglePoint(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// All 4 vertices at same point
	writePolyline(v, 0x00, 50, 50, 50, 50, 50, 50, 50, 50, 0x5555)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 50, 50); got != 0x5555 {
		t.Errorf("px(50,50) = 0x%04X, want 0x5555", got)
	}
	if got := readFBPixel(v, 49, 50); got != 0 {
		t.Errorf("px(49,50) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawPolylineClipping(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set system clip to 15x223
	writeCmd16(v, 0x00, 0x0009)
	writeCmd16(v, 0x14, 15)
	writeCmd16(v, 0x16, 223)

	// Polyline rectangle that extends beyond clip on right side
	writePolyline(v, 0x20, 10, 10, 20, 10, 20, 20, 10, 20, 0x7777)
	writeDrawEnd(v, 0x40)
	v.VBlankIn()
	drainDrawing(v)

	// Top edge at x=15 should be drawn (within clip)
	if got := readFBPixel(v, 15, 10); got != 0x7777 {
		t.Errorf("px(15,10) = 0x%04X, want 0x7777", got)
	}
	// Top edge at x=16 should be clipped
	if got := readFBPixel(v, 16, 10); got != 0 {
		t.Errorf("px(16,10) = 0x%04X, want 0x0000 (clipped)", got)
	}
}

// --- Polygon command (0x4) tests ---

// writePolygon sets up a polygon command at the given VRAM address.
func writePolygon(v *VDP1, addr uint32, ax, ay, bx, by, cx, cy, dx, dy int16, color uint16) {
	writeCmd16(v, addr+0x00, 0x0004) // CMDCTRL: command 0x4
	writeCmd16(v, addr+0x04, 0x0000) // CMDPMOD
	writeCmd16(v, addr+0x06, color)  // CMDCOLR
	writeCmd16(v, addr+0x0C, uint16(ax))
	writeCmd16(v, addr+0x0E, uint16(ay))
	writeCmd16(v, addr+0x10, uint16(bx))
	writeCmd16(v, addr+0x12, uint16(by))
	writeCmd16(v, addr+0x14, uint16(cx))
	writeCmd16(v, addr+0x16, uint16(cy))
	writeCmd16(v, addr+0x18, uint16(dx))
	writeCmd16(v, addr+0x1A, uint16(dy))
}

func TestDDAEdgeStartExact(t *testing.T) {
	// Start position must always be exact
	cases := []struct {
		name                  string
		x1, y1, x2, y2, steps int
	}{
		{"horizontal", 0, 0, 10, 0, 10},
		{"vertical", 0, 0, 0, 10, 10},
		{"diagonal", 0, 0, 10, 10, 10},
		{"negative", 10, 10, 0, 0, 10},
		{"steep", 0, 0, 3, 10, 10},
		{"wide", 0, 0, 10, 3, 10},
		{"single_step", 5, 5, 8, 12, 1},
		{"zero_steps", 5, 5, 8, 12, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := initDDAEdge(tc.x1, tc.y1, tc.x2, tc.y2, tc.steps)
			if e.intX() != tc.x1 || e.intY() != tc.y1 {
				t.Errorf("start=(%d,%d), want (%d,%d)", e.intX(), e.intY(), tc.x1, tc.y1)
			}
		})
	}
}

func TestDDAEdgeEndClose(t *testing.T) {
	// After N steps, endpoint should be within 1 pixel of target.
	// Rendering code explicitly records start/end vertices for exact coverage.
	cases := []struct {
		name                  string
		x1, y1, x2, y2, steps int
	}{
		{"horizontal", 0, 0, 10, 0, 10},
		{"vertical", 0, 0, 0, 10, 10},
		{"diagonal", 0, 0, 10, 10, 10},
		{"negative", 10, 10, 0, 0, 10},
		{"steep", 0, 0, 3, 10, 10},
		{"wide", 0, 0, 10, 3, 10},
		{"large", 0, 0, 200, 150, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := initDDAEdge(tc.x1, tc.y1, tc.x2, tc.y2, tc.steps)
			for i := 0; i < tc.steps; i++ {
				e.step()
			}
			dx := e.intX() - tc.x2
			dy := e.intY() - tc.y2
			if dx < -1 || dx > 1 || dy < -1 || dy > 1 {
				t.Errorf("end=(%d,%d), want within 1 of (%d,%d)", e.intX(), e.intY(), tc.x2, tc.y2)
			}
		})
	}
}

func TestDrawPolygonAxisAligned(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Rectangle: A(10,10) B(20,10) C(20,20) D(10,20)
	writePolygon(v, 0x00, 10, 10, 20, 10, 20, 20, 10, 20, 0x1234)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// All interior and edge pixels should be filled
	for y := 10; y <= 20; y++ {
		for x := 10; x <= 20; x++ {
			if got := readFBPixel(v, x, y); got != 0x1234 {
				t.Errorf("px(%d,%d) = 0x%04X, want 0x1234", x, y, got)
			}
		}
	}
	// Pixel outside should be empty
	if got := readFBPixel(v, 9, 15); got != 0 {
		t.Errorf("outside px(9,15) = 0x%04X, want 0x0000", got)
	}
	if got := readFBPixel(v, 21, 15); got != 0 {
		t.Errorf("outside px(21,15) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawPolygonTriangle(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Triangle: C and D at same position (collapse to triangle)
	// A(10,10) B(20,10) C(15,20) D(15,20)
	writePolygon(v, 0x00, 10, 10, 20, 10, 15, 20, 15, 20, 0x5678)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Top-left vertex should be drawn
	if got := readFBPixel(v, 10, 10); got != 0x5678 {
		t.Errorf("px(10,10) = 0x%04X, want 0x5678", got)
	}
	// Top-right vertex should be drawn
	if got := readFBPixel(v, 20, 10); got != 0x5678 {
		t.Errorf("px(20,10) = 0x%04X, want 0x5678", got)
	}
	// Bottom point should be drawn
	if got := readFBPixel(v, 15, 20); got != 0x5678 {
		t.Errorf("px(15,20) = 0x%04X, want 0x5678", got)
	}
	// Center should be filled
	if got := readFBPixel(v, 15, 15); got != 0x5678 {
		t.Errorf("px(15,15) = 0x%04X, want 0x5678", got)
	}
}

func TestDrawPolygonSinglePixel(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writePolygon(v, 0x00, 50, 50, 50, 50, 50, 50, 50, 50, 0xAAAA)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 50, 50); got != 0xAAAA {
		t.Errorf("px(50,50) = 0x%04X, want 0xAAAA", got)
	}
	if got := readFBPixel(v, 49, 50); got != 0 {
		t.Errorf("px(49,50) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawPolygonClipping(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set system clip to 15x223
	writeCmd16(v, 0x00, 0x0009)
	writeCmd16(v, 0x14, 15)
	writeCmd16(v, 0x16, 223)

	// Rectangle extending beyond clip
	writePolygon(v, 0x20, 10, 10, 20, 10, 20, 20, 10, 20, 0x3333)
	writeDrawEnd(v, 0x40)
	v.VBlankIn()
	drainDrawing(v)

	// Pixel at (15,15) should be drawn (within clip)
	if got := readFBPixel(v, 15, 15); got != 0x3333 {
		t.Errorf("px(15,15) = 0x%04X, want 0x3333", got)
	}
	// Pixel at (16,15) should be clipped
	if got := readFBPixel(v, 16, 15); got != 0 {
		t.Errorf("px(16,15) = 0x%04X, want 0x0000 (clipped)", got)
	}
}

func TestDrawPolygonMSBOn(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0004) // polygon command
	writeCmd16(v, 0x04, 0x8000) // CMDPMOD: MSB on
	writeCmd16(v, 0x06, 0x0010) // CMDCOLR
	writeCmd16(v, 0x0C, 5)      // XA
	writeCmd16(v, 0x0E, 5)      // YA
	writeCmd16(v, 0x10, 8)      // XB
	writeCmd16(v, 0x12, 5)      // YB
	writeCmd16(v, 0x14, 8)      // XC
	writeCmd16(v, 0x16, 8)      // YC
	writeCmd16(v, 0x18, 5)      // XD
	writeCmd16(v, 0x1A, 8)      // YD
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Center pixel should have MSB set
	if got := readFBPixel(v, 6, 6); got != 0x8010 {
		t.Errorf("px(6,6) = 0x%04X, want 0x8010", got)
	}
}

func TestDrawPolygonDegenerateThinSliver(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Chained "line as polygon" pattern: two pairs of vertices coincide
	// or differ by 1 px so the quad collapses to a thin diagonal sliver.
	// Bulk Slash uses chains of these to draw curved orbital paths;
	// the union of slivers must form a continuous stroke, not a chain
	// of detached endpoint dots.
	//   A=(20,30) B=(29,31) C=(30,31) D=(20,30)
	// dmax = max(cheb(A,D), cheb(B,C)) = max(0, 1) = 1.
	writePolygon(v, 0x00, 20, 30, 29, 31, 30, 31, 20, 30, 0xCDEF)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Both control edges are walked and the connecting lines (A->B at
	// step 0, D->C at step 1) are X-dominant Bresenham lines. Their
	// pixels form the diagonal sliver.
	// Expected coverage (from the X-dominant connecting-line DDA):
	//   y=30: x in {20..29} from line A->B and {20..28} from line D->C
	//          union = {20..29} -> 10 pixels
	//   y=31: x = 29 (line A->B endpoint) and x = 30 (line D->C endpoint)
	//          range [29..30] -> 2 pixels
	for x := 20; x <= 29; x++ {
		if got := readFBPixel(v, x, 30); got != 0xCDEF {
			t.Errorf("px(%d,30) = 0x%04X, want 0xCDEF", x, got)
		}
	}
	for x := 29; x <= 30; x++ {
		if got := readFBPixel(v, x, 31); got != 0xCDEF {
			t.Errorf("px(%d,31) = 0x%04X, want 0xCDEF", x, got)
		}
	}
	// Outside the sliver should be empty.
	if got := readFBPixel(v, 19, 30); got != 0 {
		t.Errorf("px(19,30) = 0x%04X, want 0x0000", got)
	}
	if got := readFBPixel(v, 28, 31); got != 0 {
		t.Errorf("px(28,31) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawPolygonDiamond(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Diamond: A(15,10) B(20,15) C(15,20) D(10,15)
	writePolygon(v, 0x00, 15, 10, 20, 15, 15, 20, 10, 15, 0xBBBB)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Center should be filled
	if got := readFBPixel(v, 15, 15); got != 0xBBBB {
		t.Errorf("center px(15,15) = 0x%04X, want 0xBBBB", got)
	}
	// Top vertex
	if got := readFBPixel(v, 15, 10); got != 0xBBBB {
		t.Errorf("top px(15,10) = 0x%04X, want 0xBBBB", got)
	}
	// Right vertex
	if got := readFBPixel(v, 20, 15); got != 0xBBBB {
		t.Errorf("right px(20,15) = 0x%04X, want 0xBBBB", got)
	}
	// Outside corner should be empty
	if got := readFBPixel(v, 10, 10); got != 0 {
		t.Errorf("outside px(10,10) = 0x%04X, want 0x0000", got)
	}
}

// TestDrawPolygonSlantedEdgeGapFill verifies the per-connecting-line
// rasterizer plots the anti-alias perpendicular pixel at a diagonal
// step (manual Sec 1.6) and does NOT fill the whole bounding box
// (which the old span-fill did). Quad A(0,0) D(1,0) / B(4,2) C(5,2):
// dmax=1. Outer step 0 draws connecting line A->B = (0,0)->(4,2);
// the X-major DDA takes a diagonal step between j=1 and j=2, so the
// gap-fill pixel (2,0) is written. (1,1) lies inside the bbox but on
// no connecting line and must remain empty.
func TestDrawPolygonSlantedEdgeGapFill(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writePolygon(v, 0x00, 0, 0, 4, 2, 5, 2, 1, 0, 0x1234)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 0, 0); got != 0x1234 {
		t.Errorf("vertex px(0,0) = 0x%04X, want 0x1234", got)
	}
	if got := readFBPixel(v, 2, 1); got != 0x1234 {
		t.Errorf("connecting-line px(2,1) = 0x%04X, want 0x1234", got)
	}
	// Anti-alias gap-fill pixel at the diagonal step.
	if got := readFBPixel(v, 2, 0); got != 0x1234 {
		t.Errorf("gap-fill px(2,0) = 0x%04X, want 0x1234 (anti-alias)", got)
	}
	// Inside the bounding box but on no connecting line: must be empty
	// (proves connecting-line draw, not bounding-box span fill).
	if got := readFBPixel(v, 1, 1); got != 0 {
		t.Errorf("px(1,1) = 0x%04X, want 0x0000 (not a bbox fill)", got)
	}
}

// TestDrawPolygonHalfTransparentDoubleWrite verifies that adjacent
// connecting lines overlap and double-write, producing the
// characteristic half-transparent banding the manual warns about
// (Sec 1.7). The old span-fill blended every pixel exactly once
// (uniform). A fan quad A(0,0)=B(0,0), D(0,1), C(8,8) gives dmax=8
// with the left edge pinned near the origin, so (0,0) is on the
// connecting line of every outer step and is blended many times,
// while the far vertex (8,8) is on exactly one line (single blend).
func TestDrawPolygonHalfTransparentDoubleWrite(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0004) // polygon
	writeCmd16(v, 0x04, 0x0003) // CMDPMOD: CC=3 half-transparent
	writeCmd16(v, 0x06, 0x8008) // CMDCOLR: RGB, MSB set, R=8
	writeCmd16(v, 0x0C, 0)      // A(0,0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 0) // B(0,0)
	writeCmd16(v, 0x12, 0)
	writeCmd16(v, 0x14, 8) // C(8,8)
	writeCmd16(v, 0x16, 8)
	writeCmd16(v, 0x18, 0) // D(0,1)
	writeCmd16(v, 0x1A, 1)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill the quad region with an MSB-set RGB background (R=20)
	// after VBlankIn so half-transparent blends instead of replacing.
	for y := 0; y <= 8; y++ {
		for x := 0; x <= 8; x++ {
			off := (y*vdp1FBStride + x) * 2
			v.drawFB[off] = 0x80
			v.drawFB[off+1] = 0x14
		}
	}

	drainDrawing(v)

	// Single connecting line covers (8,8): one blend of src R=8 with
	// bg R=20 -> R=(8+20)/2=14, MSB preserved -> 0x800E.
	if got := readFBPixel(v, 8, 8); got != 0x800E {
		t.Errorf("single-write px(8,8) = 0x%04X, want 0x800E", got)
	}
	// (0,0) is on every outer step's connecting line: blended more
	// than once, so it differs from the single-blend value and from
	// the untouched background, and trends toward src (R<14, R>=8).
	got := readFBPixel(v, 0, 0)
	r := int(got & 0x1F)
	if got == 0x800E {
		t.Errorf("double-write px(0,0) = 0x%04X, want != 0x800E (banding)", got)
	}
	if got&0x8000 == 0 || r >= 14 || r < 8 {
		t.Errorf("double-write px(0,0) = 0x%04X (R=%d), want MSB set and 8<=R<14", got, r)
	}
}

// --- Scaled sprite command (0x1) tests ---

func TestDrawScaledSpriteTwoCoord(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 texture, mode 4 (256-color bank), scaled to 16x1 via two-coord mode
	writeCmd16(v, 0x00, 0x0001) // command 0x1
	writeCmd16(v, 0x04, 0x0020) // CMDPMOD: color mode 4
	writeCmd16(v, 0x06, 0x0100) // CMDCOLR
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)      // XA=0
	writeCmd16(v, 0x0E, 0)      // YA=0
	writeCmd16(v, 0x14, 15)     // XC=15
	writeCmd16(v, 0x16, 0)      // YC=0
	writeDrawEnd(v, 0x20)

	// 8 source pixels: 0x10..0x17
	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// 16 destination pixels, each source texel covers 2 dest pixels
	// Pixel 0: srcX = 0*8/16 = 0, dot=0x10 -> 0x0110
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// Pixel 1: srcX = 1*8/16 = 0, dot=0x10 -> 0x0110
	if got := readFBPixel(v, 1, 0); got != 0x0110 {
		t.Errorf("px(1,0) = 0x%04X, want 0x0110", got)
	}
	// Pixel 14: srcX = 14*8/16 = 7, dot=0x17 -> 0x0117
	if got := readFBPixel(v, 14, 0); got != 0x0117 {
		t.Errorf("px(14,0) = 0x%04X, want 0x0117", got)
	}
	// Pixel 16 should be empty (outside dest rect)
	if got := readFBPixel(v, 16, 0); got != 0 {
		t.Errorf("px(16,0) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawScaledSpriteTwoCoordShrink(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 texture scaled to 4x1
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x0020) // color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)      // XA=0
	writeCmd16(v, 0x0E, 0)      // YA=0
	writeCmd16(v, 0x14, 3)      // XC=3
	writeCmd16(v, 0x16, 0)      // YC=0
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// 4 dest pixels, midpoint sampling: srcX = (2*dx+1)*8/(2*4) = 1,3,5,7
	// Pixel 0: srcX = 1, dot=0x11
	if got := readFBPixel(v, 0, 0); got != 0x0111 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0111", got)
	}
	// Pixel 3: srcX = 7, dot=0x17
	if got := readFBPixel(v, 3, 0); got != 0x0117 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0117", got)
	}
	// Pixel 4 should be empty
	if got := readFBPixel(v, 4, 0); got != 0 {
		t.Errorf("px(4,0) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawScaledSpriteTwoCoordFlip(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 texture, XC < XA causes horizontal coordinate inversion
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x0020) // color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 7)      // XA=7 (> XC, triggers flip)
	writeCmd16(v, 0x0E, 0)      // YA=0
	writeCmd16(v, 0x14, 0)      // XC=0
	writeCmd16(v, 0x16, 0)      // YC=0
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// Flipped: pixel 0 gets last source texel, pixel 7 gets first
	// Pixel 0: srcX flipped = 7, dot=0x17
	if got := readFBPixel(v, 0, 0); got != 0x0117 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0117", got)
	}
	// Pixel 7: srcX flipped = 0, dot=0x10
	if got := readFBPixel(v, 7, 0); got != 0x0110 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0110", got)
	}
}

func TestDrawScaledSpriteZoomTopLeft(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Fixed-point mode, ZP=0x5 (top-left anchor)
	// Anchor at (10,20), display 16x8
	writeCmd16(v, 0x00, 0x0501) // command 0x1, ZP=0x5
	writeCmd16(v, 0x04, 0x0020) // color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1 source
	writeCmd16(v, 0x0C, 10)     // XA=10 (anchor)
	writeCmd16(v, 0x0E, 20)     // YA=20 (anchor)
	writeCmd16(v, 0x10, 16)     // XB=display width
	writeCmd16(v, 0x12, 8)      // YB=display height
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// Top-left anchor: sprite starts at (10,20), extends to (25,27)
	if got := readFBPixel(v, 10, 20); got != 0x0110 {
		t.Errorf("px(10,20) = 0x%04X, want 0x0110", got)
	}
	if got := readFBPixel(v, 9, 20); got != 0 {
		t.Errorf("px(9,20) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawScaledSpriteZoomCenter(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Fixed-point mode, ZP=0xA (center-center anchor)
	// Anchor at (50,50), display 10x10
	writeCmd16(v, 0x00, 0x0A01) // command 0x1, ZP=0xA
	writeCmd16(v, 0x04, 0x0020) // color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1 source
	writeCmd16(v, 0x0C, 50)     // XA=50 (anchor)
	writeCmd16(v, 0x0E, 50)     // YA=50 (anchor)
	writeCmd16(v, 0x10, 10)     // XB=display width
	writeCmd16(v, 0x12, 10)     // YB=display height
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// Per VDP1 manual §6 Figure 6.2: left=XA-XB/2, right=XA+(XB+1)/2.
	// With XA=50, XB=10: left=45, right=55. Both edges inclusive, so the
	// sprite covers (45,45) to (55,55) = 11x11 pixels.
	if got := readFBPixel(v, 45, 45); got != 0x0110 {
		t.Errorf("px(45,45) = 0x%04X, want 0x0110", got)
	}
	if got := readFBPixel(v, 44, 50); got != 0 {
		t.Errorf("px(44,50) = 0x%04X, want 0x0000 (outside left edge)", got)
	}
	if got := readFBPixel(v, 55, 50); got == 0 {
		t.Errorf("px(55,50) = 0x%04X, want non-zero (right edge)", got)
	}
	if got := readFBPixel(v, 56, 50); got != 0 {
		t.Errorf("px(56,50) = 0x%04X, want 0x0000 (outside right edge)", got)
	}
	if got := readFBPixel(v, 50, 55); got == 0 {
		t.Errorf("px(50,55) = 0x%04X, want non-zero (bottom edge)", got)
	}
	if got := readFBPixel(v, 50, 56); got != 0 {
		t.Errorf("px(50,56) = 0x%04X, want 0x0000 (outside bottom edge)", got)
	}
}

func TestDrawScaledSpriteZoomCenterOddDisplay(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Asymmetric centering with odd display width. Per manual §6 Fig 6.2,
	// for ZP=0xA: left = XA - XB/2 (floor), right = XA + (XB+1)/2 (ceil).
	// With XA=50, XB=7: left=50-3=47, right=50+4=54. Sprite is 8 pixels
	// wide. Source charW=8 maps 1:1 to destination - exactly the regime
	// where a count-based misreading would trigger a 1-pixel shrink and
	// HSS subsampling for HSS-flagged sprites.
	writeCmd16(v, 0x00, 0x0A01) // ZP=0xA scaled sprite
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1 source
	writeCmd16(v, 0x0C, 50)     // XA=50
	writeCmd16(v, 0x0E, 50)     // YA=50
	writeCmd16(v, 0x10, 7)      // XB=7
	writeCmd16(v, 0x12, 0)      // YB=0
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// destW = 8 = charW: HSS gate is destW < charW, so no parity snap.
	// All 8 source columns map 1:1 to dest x=47..54.
	for i := 0; i < 8; i++ {
		want := uint16(0x0110 | i)
		if got := readFBPixel(v, 47+i, 50); got != want {
			t.Errorf("px(%d,50) = 0x%04X, want 0x%04X", 47+i, got, want)
		}
	}
	if got := readFBPixel(v, 46, 50); got != 0 {
		t.Errorf("px(46,50) = 0x%04X, want 0x0000 (outside left edge)", got)
	}
	if got := readFBPixel(v, 55, 50); got != 0 {
		t.Errorf("px(55,50) = 0x%04X, want 0x0000 (outside right edge)", got)
	}
}

func TestDrawScaledSpriteSinglePixel(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Two-coord: same point = 1 pixel
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x0020)
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 5)      // XA=5
	writeCmd16(v, 0x0E, 5)      // YA=5
	writeCmd16(v, 0x14, 5)      // XC=5
	writeCmd16(v, 0x16, 5)      // YC=5
	writeDrawEnd(v, 0x20)

	// Fill all 8 source bytes so the midpoint sample (srcX=4) lands on data.
	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), 0x10)
	}
	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 5, 5); got != 0x0110 {
		t.Errorf("px(5,5) = 0x%04X, want 0x0110", got)
	}
	if got := readFBPixel(v, 6, 5); got != 0 {
		t.Errorf("px(6,5) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawScaledSpriteClipping(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set system clip to 10x223
	writeCmd16(v, 0x00, 0x0009)
	writeCmd16(v, 0x14, 10)
	writeCmd16(v, 0x16, 223)

	// Scaled sprite from (5,0) to (15,0) - extends past clip at x=10
	writeCmd16(v, 0x20, 0x0001)
	writeCmd16(v, 0x24, 0x0020)
	writeCmd16(v, 0x26, 0x0100)
	writeCmd16(v, 0x28, 0x1000/8)
	writeCmd16(v, 0x2A, 0x0101) // 8x1
	writeCmd16(v, 0x2C, 5)      // XA=5
	writeCmd16(v, 0x2E, 0)      // YA=0
	writeCmd16(v, 0x34, 15)     // XC=15
	writeCmd16(v, 0x36, 0)      // YC=0
	writeDrawEnd(v, 0x40)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// Pixel at x=10 should be drawn
	if got := readFBPixel(v, 10, 0); got == 0 {
		t.Errorf("px(10,0) = 0x%04X, want non-zero", got)
	}
	// Pixel at x=11 should be clipped
	if got := readFBPixel(v, 11, 0); got != 0 {
		t.Errorf("px(11,0) = 0x%04X, want 0x0000 (clipped)", got)
	}
}

// TestDrawScaledSpriteEnlargeEndCodeDedupPerSourceX verifies end codes
// are counted in character-pattern (source) space, not per destination
// read (manual Sec 6.3 p.86: "an end code is only processed in the
// horizontal direction of the character pattern"). An 8x1 source with a
// single end code at source X=4 enlarged 2x to 16x1: midpoint sampling
// re-reads source X=4 at dest X=8 and X=9, but the dedup counts that
// single source end code ONCE, so the row is NOT terminated and pixels
// past it draw.
func TestDrawScaledSpriteEnlargeEndCodeDedupPerSourceX(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 source, single end code at X=4, scaled to 16x1 (2x enlarge).
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x0020) // color mode 4, ECD=0, HSS=0, SPD=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)      // XA=0
	writeCmd16(v, 0x0E, 0)      // YA=0
	writeCmd16(v, 0x14, 15)     // XC=15 (16 wide = enlargement)
	writeCmd16(v, 0x16, 0)      // YC=0
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.WriteVRAM(0x1004, 0xFF) // single end code at source X=4
	v.VBlankIn()
	drainDrawing(v)

	// dest X=0 -> srcX=0 -> 0x10, drawn before the end code.
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// dest 8 and 9 both sample srcX=4 (the end code): counted ONCE via
	// source-X dedup, end-code pixel is transparent.
	if got := readFBPixel(v, 8, 0); got != 0 {
		t.Errorf("px(8,0) = 0x%04X, want 0x0000 (end code transparent)", got)
	}
	// Single source end code counts once -> row NOT terminated.
	// dest 10 -> srcX=5 -> 0x15; dest 14 -> srcX=7 -> 0x17.
	if got := readFBPixel(v, 10, 0); got != 0x0115 {
		t.Errorf("px(10,0) = 0x%04X, want 0x0115 (row not terminated; dedup)", got)
	}
	if got := readFBPixel(v, 14, 0); got != 0x0117 {
		t.Errorf("px(14,0) = 0x%04X, want 0x0117 (row not terminated; dedup)", got)
	}
}

func TestDrawScaledSpriteEndCode(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x1 texture with end code at position 4, scaled to 16x1
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x0020) // color mode 4, ECD enabled (bit 7=0)
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x14, 15) // XC=15 (16 pixels wide)
	writeCmd16(v, 0x16, 0)
	writeDrawEnd(v, 0x20)

	for i := 0; i < 4; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.WriteVRAM(0x1004, 0xFF) // end code at source position 4
	v.VBlankIn()
	drainDrawing(v)

	// Pixels mapping to source 0-3 should be drawn
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// Pixels mapping to source 4+ should not be drawn (end code hit)
	// Source pixel 4 maps to dest pixel 8 (4*16/8=8)
	if got := readFBPixel(v, 8, 0); got != 0 {
		t.Errorf("px(8,0) = 0x%04X, want 0x0000 (after end code)", got)
	}
}

func TestDrawScaledSpriteHSSEvenCoords(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x00) // EOS=0: even coordinates

	// 8x1 texture shrunk to 4x1 with HSS=1. Parity-snap only applies
	// when shrinking, so this verifies the EOS=0 sampling path.
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)      // XA=0
	writeCmd16(v, 0x0E, 0)      // YA=0
	writeCmd16(v, 0x14, 3)      // XC=3 (4 dest = shrink)
	writeCmd16(v, 0x16, 0)      // YC=0
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// HW samples srcX = 0, 2, 4, 6 (manual figure 6.5 X4/8 HSS=1 EOS=0).
	want := []uint16{0x0110, 0x0112, 0x0114, 0x0116}
	for dx := 0; dx < 4; dx++ {
		if got := readFBPixel(v, dx, 0); got != want[dx] {
			t.Errorf("px(%d,0) = 0x%04X, want 0x%04X", dx, got, want[dx])
		}
	}
}

func TestDrawScaledSpriteHSSOddCoords(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x10) // EOS=1: odd coordinates

	// 8x1 shrunk to 4x1 with HSS=1, EOS=1.
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x14, 3) // XC=3 (4 dest = shrink)
	writeCmd16(v, 0x16, 0) // YC=0
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// HW samples srcX = 1, 3, 5, 7 (manual figure 6.5 X4/8 HSS=1 EOS=1).
	want := []uint16{0x0111, 0x0113, 0x0115, 0x0117}
	for dx := 0; dx < 4; dx++ {
		if got := readFBPixel(v, dx, 0); got != want[dx] {
			t.Errorf("px(%d,0) = 0x%04X, want 0x%04X", dx, got, want[dx])
		}
	}
}

func TestDrawScaledSpriteHSSOneToOneNoSnap(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x00) // EOS=0

	// 8x1 source at 1:1 with HSS=1. Snap must NOT apply since the axis
	// isn't being shrunk; all 8 source columns must be readable.
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x14, 7) // XC=7 (8 dest = 1:1)
	writeCmd16(v, 0x16, 0)
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	for dx := 0; dx < 8; dx++ {
		want := uint16(0x0110 | dx)
		if got := readFBPixel(v, dx, 0); got != want {
			t.Errorf("px(%d,0) = 0x%04X, want 0x%04X", dx, got, want)
		}
	}
}

func TestDrawScaledSpriteHSSReduceIgnoresEndCode(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x00) // EOS=0

	// 8x2 texture with end code at position 4, scaled to 4x2 (reduction)
	// HSS=1+reduce: end codes disabled, treated as normal pixel data
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4, ECD=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x14, 3) // XC=3 (4 pixels wide = reduction)
	writeCmd16(v, 0x16, 1) // YC=1
	writeDrawEnd(v, 0x20)

	for y := 0; y < 2; y++ {
		for i := 0; i < 8; i++ {
			v.WriteVRAM(0x1000+uint32(y*8+i), uint8(0x10+i))
		}
	}
	// Place end code (0xFF for 8bpp) at position 4 in both rows
	v.WriteVRAM(0x1004, 0xFF)
	v.WriteVRAM(0x100C, 0xFF)
	v.VBlankIn()
	drainDrawing(v)

	// HSS=1 + reduction (destW=4 < charW=8): end codes disabled.
	// EOS=0 forces even coords. dx=2 -> srcX=2*8/4=4 -> forced even=4 -> dot=0xFF
	// End code treated as normal pixel: (0x0100 & 0xFF00) | (0xFF & 0xFF) = 0x01FF
	if got := readFBPixel(v, 2, 0); got != 0x01FF {
		t.Errorf("px(2,0) = 0x%04X, want 0x01FF (end code treated as pixel)", got)
	}
	// dx=3 -> srcX=3*8/4=6 -> forced even=6 -> dot=0x16
	if got := readFBPixel(v, 3, 0); got != 0x0116 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0116 (drawn past end code)", got)
	}
}

// TestDrawScaledSpriteHSSEnlargeKeepsEndCodes: per manual Sec 6.3 p.86
// HSS/ECD table, HSS=1 with enlargement keeps end codes ENABLED (HSS
// disables end codes only when reducing). Two distinct source end codes
// (at X=2 and X=5) still reach count 2 via the source-X dedup and
// terminate the row.
func TestDrawScaledSpriteHSSEnlargeKeepsEndCodes(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x00) // EOS=0

	// 8x2 texture scaled to 16x2 (enlargement). HSS=1 + enlarge: end codes still active.
	// Two end codes at positions 2 and 5.
	writeCmd16(v, 0x00, 0x0001)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4, ECD=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x14, 15) // XC=15 (16 pixels wide = enlargement)
	writeCmd16(v, 0x16, 1)  // YC=1
	writeDrawEnd(v, 0x20)

	for y := 0; y < 2; y++ {
		for i := 0; i < 8; i++ {
			v.WriteVRAM(0x1000+uint32(y*8+i), uint8(0x10+i))
		}
	}
	// End codes at positions 2 and 5
	v.WriteVRAM(0x1002, 0xFF)
	v.WriteVRAM(0x1005, 0xFF)
	v.WriteVRAM(0x100A, 0xFF)
	v.WriteVRAM(0x100D, 0xFF)
	v.VBlankIn()
	drainDrawing(v)

	// HSS=1 + enlarge (destW=16 > charW=8): no parity snap, end codes
	// still processed. Midpoint sampling: srcX = (2*dx+1)*8/32.
	// dx=4 -> srcX=2 (first EC, transparent).
	if got := readFBPixel(v, 4, 0); got != 0 {
		t.Errorf("px(4,0) = 0x%04X, want 0x0000 (first end code, transparent)", got)
	}
	// dx=8 -> srcX=4, dot=0x14 (drawn between end codes).
	if got := readFBPixel(v, 8, 0); got != 0x0114 {
		t.Errorf("px(8,0) = 0x%04X, want 0x0114 (drawn between end codes)", got)
	}
	// dx=10 -> srcX=5 (second EC, terminates the row).
	// dx=14 must be empty since the row terminated.
	if got := readFBPixel(v, 14, 0); got != 0 {
		t.Errorf("px(14,0) = 0x%04X, want 0x0000 (row terminated by 2nd EC)", got)
	}
}

// --- Distorted sprite command (0x2) tests ---

// writeDistortedSprite sets up a distorted sprite command at the given VRAM address.
func writeDistortedSprite(v *VDP1, addr uint32,
	ax, ay, bx, by, cx, cy, dx, dy int16,
	colorMode uint16, colr uint16, charAddr uint32, w, h int) {
	ctrl := uint16(0x0002) // command 0x2
	writeCmd16(v, addr+0x00, ctrl)
	writeCmd16(v, addr+0x04, colorMode<<3) // CMDPMOD
	writeCmd16(v, addr+0x06, colr)
	writeCmd16(v, addr+0x08, uint16(charAddr/8))
	writeCmd16(v, addr+0x0A, uint16((w/8)<<8|h))
	writeCmd16(v, addr+0x0C, uint16(ax))
	writeCmd16(v, addr+0x0E, uint16(ay))
	writeCmd16(v, addr+0x10, uint16(bx))
	writeCmd16(v, addr+0x12, uint16(by))
	writeCmd16(v, addr+0x14, uint16(cx))
	writeCmd16(v, addr+0x16, uint16(cy))
	writeCmd16(v, addr+0x18, uint16(dx))
	writeCmd16(v, addr+0x1A, uint16(dy))
}

func TestDrawDistortedSpriteIdentity(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x4 texture, mode 4 (256-color bank), quad matches texture size
	// A(0,0) B(7,0) C(7,3) D(0,3)
	writeDistortedSprite(v, 0x00, 0, 0, 7, 0, 7, 3, 0, 3, 4, 0x0100, 0x1000, 8, 4)
	writeDrawEnd(v, 0x20)

	// Fill 8x4 texture: row*8+col as dot value
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+x))
		}
	}
	v.VBlankIn()
	drainDrawing(v)

	// Top-left corner: should have first texel
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// Top-right corner
	if got := readFBPixel(v, 7, 0); got != 0x0117 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0117", got)
	}
	// Bottom-left
	if got := readFBPixel(v, 0, 3); got != 0x0110 {
		t.Errorf("px(0,3) = 0x%04X, want 0x0110", got)
	}
	// Outside should be empty
	if got := readFBPixel(v, 8, 0); got != 0 {
		t.Errorf("px(8,0) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawDistortedSpriteScaleUp(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x2 texture scaled to 16x4 quad
	// A(0,0) B(15,0) C(15,3) D(0,3)
	writeDistortedSprite(v, 0x00, 0, 0, 15, 0, 15, 3, 0, 3, 4, 0x0100, 0x1000, 8, 2)
	writeDrawEnd(v, 0x20)

	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+x))
		}
	}
	v.VBlankIn()
	drainDrawing(v)

	// First texel should appear at start
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// Last texel column should appear at right edge (fixed-point U
	// interpolation rounding may differ by one texel at boundary)
	got := readFBPixel(v, 15, 0)
	if got != 0x0117 && got != 0x0116 {
		t.Errorf("px(15,0) = 0x%04X, want 0x0117 or 0x0116", got)
	}
	// Outside should be empty
	if got := readFBPixel(v, 16, 0); got != 0 {
		t.Errorf("px(16,0) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawDistortedSpriteTriangle(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Triangle: C and D at same point
	// A(10,10) B(20,10) C(15,20) D(15,20)
	writeDistortedSprite(v, 0x00, 10, 10, 20, 10, 15, 20, 15, 20, 4, 0x0100, 0x1000, 8, 4)
	writeDrawEnd(v, 0x20)

	for i := 0; i < 32; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+(i%8)))
	}
	v.VBlankIn()
	drainDrawing(v)

	// Top-left vertex
	if got := readFBPixel(v, 10, 10); got == 0 {
		t.Errorf("px(10,10) = 0x%04X, want non-zero", got)
	}
	// Bottom point
	if got := readFBPixel(v, 15, 20); got == 0 {
		t.Errorf("px(15,20) = 0x%04X, want non-zero", got)
	}
	// Center should be filled
	if got := readFBPixel(v, 15, 15); got == 0 {
		t.Errorf("px(15,15) = 0x%04X, want non-zero", got)
	}
}

func TestDrawDistortedSpriteSinglePixel(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// All vertices at same point
	writeDistortedSprite(v, 0x00, 50, 50, 50, 50, 50, 50, 50, 50, 4, 0x0100, 0x1000, 8, 1)
	writeDrawEnd(v, 0x20)

	v.WriteVRAM(0x1000, 0x10)
	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 50, 50); got != 0x0110 {
		t.Errorf("px(50,50) = 0x%04X, want 0x0110", got)
	}
	if got := readFBPixel(v, 51, 50); got != 0 {
		t.Errorf("px(51,50) = 0x%04X, want 0x0000", got)
	}
}

func TestDrawDistortedSpriteFlipH(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x2 texture with horizontal flip, identity quad
	ctrl := uint16(0x0012) // command 0x2 + Dir bit 4 (flipH)
	writeCmd16(v, 0x00, ctrl)
	writeCmd16(v, 0x04, 0x0020) // color mode 4
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)      // XA=0
	writeCmd16(v, 0x0E, 0)      // YA=0
	writeCmd16(v, 0x10, 7)      // XB=7
	writeCmd16(v, 0x12, 0)      // YB=0
	writeCmd16(v, 0x14, 7)      // XC=1
	writeCmd16(v, 0x16, 1)      // YC=1
	writeCmd16(v, 0x18, 0)      // XD=0
	writeCmd16(v, 0x1A, 1)      // YD=1
	writeDrawEnd(v, 0x20)

	for y := 0; y < 2; y++ {
		for i := 0; i < 8; i++ {
			v.WriteVRAM(0x1000+uint32(y*8+i), uint8(0x10+i))
		}
	}
	v.VBlankIn()
	drainDrawing(v)

	// Flipped: pixel 0 gets last texel, pixel 7 gets first
	if got := readFBPixel(v, 0, 0); got != 0x0117 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0117", got)
	}
	if got := readFBPixel(v, 7, 0); got != 0x0110 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0110", got)
	}
}

func TestDrawDistortedSpriteEndCode(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x2 texture with end code at position 4, identity quad
	writeCmd16(v, 0x00, 0x0002) // command 0x2
	writeCmd16(v, 0x04, 0x0020) // color mode 4, ECD enabled (bit 7=0)
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)      // A(0,0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 7) // B(7,0)
	writeCmd16(v, 0x12, 0)
	writeCmd16(v, 0x14, 7) // C(7,1)
	writeCmd16(v, 0x16, 1)
	writeCmd16(v, 0x18, 0) // D(0,1)
	writeCmd16(v, 0x1A, 1)
	writeDrawEnd(v, 0x20)

	// Row 0: pixels 0-3 normal, pixel 4 = end code
	for i := 0; i < 4; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.WriteVRAM(0x1004, 0xFF) // end code
	// Row 1: same pattern
	for i := 0; i < 4; i++ {
		v.WriteVRAM(0x1008+uint32(i), uint8(0x10+i))
	}
	v.WriteVRAM(0x100C, 0xFF)
	v.VBlankIn()
	drainDrawing(v)

	// Pixels 0-3 should be drawn
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	if got := readFBPixel(v, 3, 0); got != 0x0113 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0113", got)
	}
}

func TestDrawDistortedSpriteClipping(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set system clip to 10x223
	writeCmd16(v, 0x00, 0x0009)
	writeCmd16(v, 0x14, 10)
	writeCmd16(v, 0x16, 223)

	// 8x2 texture mapped to quad extending beyond clip
	writeDistortedSprite(v, 0x20, 5, 0, 15, 0, 15, 1, 5, 1, 4, 0x0100, 0x1000, 8, 2)
	writeDrawEnd(v, 0x40)

	for i := 0; i < 16; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+(i%8)))
	}
	v.VBlankIn()
	drainDrawing(v)

	// Pixel at x=10 should be drawn
	if got := readFBPixel(v, 10, 0); got == 0 {
		t.Errorf("px(10,0) = 0x%04X, want non-zero", got)
	}
	// Pixel at x=11 should be clipped
	if got := readFBPixel(v, 11, 0); got != 0 {
		t.Errorf("px(11,0) = 0x%04X, want 0x0000 (clipped)", got)
	}
}

func TestDrawDistortedSpriteHSSEvenCoords(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x00) // EOS=0: even coordinates

	// 8x2 texture, identity quad A(0,0) B(7,0) C(7,1) D(0,1)
	// HSS=1 via CMDPMOD bit 12
	ctrl := uint16(0x0002)
	writeCmd16(v, 0x00, ctrl)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4
	writeCmd16(v, 0x06, 0x0100) // CMDCOLR
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)      // AX=0
	writeCmd16(v, 0x0E, 0)      // AY=0
	writeCmd16(v, 0x10, 7)      // BX=7
	writeCmd16(v, 0x12, 0)      // BY=0
	writeCmd16(v, 0x14, 7)      // CX=7
	writeCmd16(v, 0x16, 1)      // CY=1
	writeCmd16(v, 0x18, 0)      // DX=0
	writeCmd16(v, 0x1A, 1)      // DY=1
	writeDrawEnd(v, 0x20)

	// Fill 8x2 texture with distinct values per column
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+x))
		}
	}
	v.VBlankIn()
	drainDrawing(v)

	// With EOS=0, U coordinates forced to even: 0->0, 1->0, 2->2, 3->2, ...
	// Check a few positions on the first row
	// px(0,0): U forced to 0 -> dot=0x10
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// px(1,0): U forced to 0 -> dot=0x10
	if got := readFBPixel(v, 1, 0); got != 0x0110 {
		t.Errorf("px(1,0) = 0x%04X, want 0x0110", got)
	}
	// px(2,0): U forced to 2 -> dot=0x12
	if got := readFBPixel(v, 2, 0); got != 0x0112 {
		t.Errorf("px(2,0) = 0x%04X, want 0x0112", got)
	}
	// px(3,0): U forced to 2 -> dot=0x12
	if got := readFBPixel(v, 3, 0); got != 0x0112 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0112", got)
	}
}

func TestDrawDistortedSpriteHSSReduceIgnoresEndCode(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x00) // EOS=0

	// 8x2 texture with end code at position 4, identity quad (lineLen=7 < charW=8)
	// HSS=1+reduce: end codes disabled, treated as normal pixel data
	ctrl := uint16(0x0002)
	writeCmd16(v, 0x00, ctrl)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4, ECD=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)      // AX=0
	writeCmd16(v, 0x0E, 0)      // AY=0
	writeCmd16(v, 0x10, 7)      // BX=7
	writeCmd16(v, 0x12, 0)      // BY=0
	writeCmd16(v, 0x14, 7)      // CX=7
	writeCmd16(v, 0x16, 1)      // CY=1
	writeCmd16(v, 0x18, 0)      // DX=0
	writeCmd16(v, 0x1A, 1)      // DY=1
	writeDrawEnd(v, 0x20)

	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+x))
		}
	}
	// Place end code (0xFF for 8bpp) at position 4 in both rows
	v.WriteVRAM(0x1004, 0xFF)
	v.WriteVRAM(0x100C, 0xFF)
	v.VBlankIn()
	drainDrawing(v)

	// With HSS=1, end codes are ignored.
	// EOS=0 forces even coords, so U=4 stays 4 -> dot=0xFF (end code value, drawn as pixel)
	if got := readFBPixel(v, 4, 0); got != 0x01FF {
		t.Errorf("px(4,0) = 0x%04X, want 0x01FF (end code treated as pixel)", got)
	}
	// U=6 -> dot=0x16, should be drawn (not stopped by end code)
	if got := readFBPixel(v, 6, 0); got != 0x0116 {
		t.Errorf("px(6,0) = 0x%04X, want 0x0116 (drawn past end code)", got)
	}
}

// TestDrawDistortedSpriteHSSEnlargeKeepsEndCode: per manual Sec 6.3 p.86
// HSS/ECD table, HSS=1 with enlargement keeps end codes ENABLED (HSS
// disables end codes only when reducing). The shrink proxy for distorted
// is dmax < charH; here dmax=3, charH=2 so checkEcd stays true. The end
// code is therefore NOT rendered as its color value, and once it is
// counted twice along the connecting line the row terminates.
func TestDrawDistortedSpriteHSSEnlargeKeepsEndCode(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)
	v.Write(0x02, 0x00) // EOS=0

	// 8x2 source drawn into a 16x4 quad (enlargement).
	ctrl := uint16(0x0002)
	writeCmd16(v, 0x00, ctrl)
	writeCmd16(v, 0x04, 0x1020) // CMDPMOD: HSS=1, color mode 4, ECD=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)      // AX=0
	writeCmd16(v, 0x0E, 0)      // AY=0
	writeCmd16(v, 0x10, 15)     // BX=15
	writeCmd16(v, 0x12, 0)      // BY=0
	writeCmd16(v, 0x14, 15)     // CX=15
	writeCmd16(v, 0x16, 3)      // CY=3
	writeCmd16(v, 0x18, 0)      // DX=0
	writeCmd16(v, 0x1A, 3)      // DY=3
	writeDrawEnd(v, 0x20)

	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+x))
		}
	}
	// End code (0xFF for 8bpp) at source position 4 in both rows
	v.WriteVRAM(0x1004, 0xFF)
	v.WriteVRAM(0x100C, 0xFF)
	v.VBlankIn()
	drainDrawing(v)

	// HSS=1 + enlarge keeps end codes enabled (p.86 table). The end-code
	// source value must NOT appear as a drawn color, and the row
	// terminates once the end code is counted twice along the line.
	for x := 0; x < 16; x++ {
		if got := readFBPixel(v, x, 0); got == 0x01FF {
			t.Errorf("px(%d,0) = 0x01FF (end code drawn as color; should be enabled/transparent)", x)
		}
	}
	// Pre-end-code source pixels still draw (line starts normally).
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110 (drawn before end code)", got)
	}
	// Far end of the line is past the terminated end code -> not drawn.
	if got := readFBPixel(v, 15, 0); got != 0 {
		t.Errorf("px(15,0) = 0x%04X, want 0x0000 (row terminated by end code)", got)
	}
}

func TestDrawDistortedSpriteEndCodeSecondOccurrence(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// 8x2 texture with two end codes per row at positions 1 and 6, identity quad
	ctrl := uint16(0x0002)
	writeCmd16(v, 0x00, ctrl)
	writeCmd16(v, 0x04, 0x0020) // CMDPMOD: color mode 4, ECD=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0102) // 8x2
	writeCmd16(v, 0x0C, 0)      // A(0,0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 7) // B(7,0)
	writeCmd16(v, 0x12, 0)
	writeCmd16(v, 0x14, 7) // C(7,1)
	writeCmd16(v, 0x16, 1)
	writeCmd16(v, 0x18, 0) // D(0,1)
	writeCmd16(v, 0x1A, 1)
	writeDrawEnd(v, 0x20)

	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			v.WriteVRAM(0x1000+uint32(y*8+x), uint8(0x10+x))
		}
	}
	// End codes at positions 1 and 6 in both rows
	v.WriteVRAM(0x1001, 0xFF)
	v.WriteVRAM(0x1006, 0xFF)
	v.WriteVRAM(0x1009, 0xFF)
	v.WriteVRAM(0x100E, 0xFF)
	v.VBlankIn()
	drainDrawing(v)

	// px(0,0): dot=0x10, drawn (before first EC)
	if got := readFBPixel(v, 0, 0); got != 0x0110 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0110", got)
	}
	// px(1,0): first end code, transparent
	if got := readFBPixel(v, 1, 0); got != 0 {
		t.Errorf("px(1,0) = 0x%04X, want 0x0000 (first end code)", got)
	}
	// px(3,0): dot=0x13, drawn (between first and second EC)
	if got := readFBPixel(v, 3, 0); got != 0x0113 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0113", got)
	}
	// px(6,0): second end code terminates, not drawn
	if got := readFBPixel(v, 6, 0); got != 0 {
		t.Errorf("px(6,0) = 0x%04X, want 0x0000 (second end code terminates)", got)
	}
	// px(7,0): should NOT be drawn (after second end code)
	if got := readFBPixel(v, 7, 0); got != 0 {
		t.Errorf("px(7,0) = 0x%04X, want 0x0000 (after second end code)", got)
	}
}

// --- Color calculation and Gouraud shading tests ---

func TestColorCalcHalfLuminance(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006) // line
	writeCmd16(v, 0x04, 0x0002) // CMDPMOD: CC=2
	writeCmd16(v, 0x06, 0x83E0) // CMDCOLR: RGB green=31 + MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Green 31>>1 = 15. MSB preserved. 0x8000 | 15<<5 = 0x81E0
	if got := readFBPixel(v, 5, 5); got != 0x81E0 {
		t.Errorf("half lum px = 0x%04X, want 0x81E0", got)
	}
}

func TestColorCalcShadow(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0001) // CC=1 (shadow)
	writeCmd16(v, 0x06, 0x8000)
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill FB pixel after VBlankIn: 0xFC1F = MSB + B=31, G=0, R=31
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0xFC
	v.drawFB[off+1] = 0x1F

	drainDrawing(v)

	// Shadow halves FB: B=15, R=15. 0x8000 | 15<<10 | 15 = 0xBC0F
	if got := readFBPixel(v, 5, 5); got != 0xBC0F {
		t.Errorf("shadow px = 0x%04X, want 0xBC0F", got)
	}
}

func TestColorCalcHalfTransparent(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0003) // CC=3
	writeCmd16(v, 0x06, 0x800A) // R=10 + MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill FB after VBlankIn: R=20 + MSB = 0x8014
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0x80
	v.drawFB[off+1] = 0x14

	drainDrawing(v)

	// R = (10+20)/2 = 15. 0x8000 | 15 = 0x800F
	if got := readFBPixel(v, 5, 5); got != 0x800F {
		t.Errorf("half transp px = 0x%04X, want 0x800F", got)
	}
}

func TestGouraudLineBasic(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Gouraud at 0x2000: A=neutral(0x10), B=R+15(0x1F)
	writeCmd16(v, 0x2000, 0x4210) // A: all neutral
	writeCmd16(v, 0x2002, 0x421F) // B: R=+15

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0004) // CC=4
	writeCmd16(v, 0x06, 0x8000) // RGB black + MSB
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 10)
	writeCmd16(v, 0x10, 10)
	writeCmd16(v, 0x12, 10)
	writeCmd16(v, 0x1C, 0x2000/8)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// x=0: correction=0, R=0
	if r := readFBPixel(v, 0, 10) & 0x1F; r != 0 {
		t.Errorf("gouraud px(0,10) R=%d, want 0", r)
	}
	// x=10: correction=+15, R=15
	if r := readFBPixel(v, 10, 10) & 0x1F; r != 15 {
		t.Errorf("gouraud px(10,10) R=%d, want 15", r)
	}
}

func TestGouraudClamp(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x2000, 0x421F) // R=+15
	writeCmd16(v, 0x2002, 0x421F)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0004)
	writeCmd16(v, 0x06, 0x801F) // R=31
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeCmd16(v, 0x1C, 0x2000/8)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// clamp(31+15) = 31
	if r := readFBPixel(v, 5, 5) & 0x1F; r != 31 {
		t.Errorf("gouraud clamp R=%d, want 31", r)
	}
}

func TestGouraudPolygon(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x2000, 0x4210) // A: neutral
	writeCmd16(v, 0x2002, 0x4210) // B: neutral
	writeCmd16(v, 0x2004, 0x421F) // C: R=+15
	writeCmd16(v, 0x2006, 0x421F) // D: R=+15

	writeCmd16(v, 0x00, 0x0004) // polygon
	writeCmd16(v, 0x04, 0x0004) // CC=4
	writeCmd16(v, 0x06, 0x800A) // R=10 + MSB
	writeCmd16(v, 0x0C, 10)
	writeCmd16(v, 0x0E, 10)
	writeCmd16(v, 0x10, 20)
	writeCmd16(v, 0x12, 10)
	writeCmd16(v, 0x14, 20)
	writeCmd16(v, 0x16, 20)
	writeCmd16(v, 0x18, 10)
	writeCmd16(v, 0x1A, 20)
	writeCmd16(v, 0x1C, 0x2000/8)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Top: neutral correction. R=10+0=10
	if r := readFBPixel(v, 15, 10) & 0x1F; r != 10 {
		t.Errorf("gouraud poly top R=%d, want 10", r)
	}
	// Bottom: +15 correction. R=10+15=25
	if r := readFBPixel(v, 15, 20) & 0x1F; r != 25 {
		t.Errorf("gouraud poly bottom R=%d, want 25", r)
	}
}

// --- Gouraud+CC mode and MSB On tests ---

func TestColorCalcGouraudShadow(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Gouraud table at VRAM 0x2000: both vertices have R+15 correction
	writeCmd16(v, 0x2000, 0x421F) // A: B=neutral, G=neutral, R=max(0x1F)
	writeCmd16(v, 0x2002, 0x421F) // B: same

	writeCmd16(v, 0x00, 0x0006) // line
	writeCmd16(v, 0x04, 0x0005) // CC=5 (Gouraud+shadow)
	writeCmd16(v, 0x06, 0x8000) // CMDCOLR: black + MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeCmd16(v, 0x1C, 0x2000/8) // CMDGRDA
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill destination pixel at (5,5) after VBlankIn with MSB set: 0xFC1F
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0xFC
	v.drawFB[off+1] = 0x1F

	drainDrawing(v)

	// Shadow halves dest RGB, Gouraud has no effect.
	// B=31>>1=15, R=31>>1=15 -> 0x8000|15<<10|15 = 0xBC0F
	if got := readFBPixel(v, 5, 5); got != 0xBC0F {
		t.Errorf("gouraud+shadow px = 0x%04X, want 0xBC0F", got)
	}
}

func TestColorCalcGouraudShadowNoMSB(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x2000, 0x421F)
	writeCmd16(v, 0x2002, 0x421F)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0005) // CC=5
	writeCmd16(v, 0x06, 0x8000)
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeCmd16(v, 0x1C, 0x2000/8)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill destination after VBlankIn WITHOUT MSB: 0x7C1F
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0x7C
	v.drawFB[off+1] = 0x1F

	drainDrawing(v)

	// Shadow no-op: dest unchanged
	if got := readFBPixel(v, 5, 5); got != 0x7C1F {
		t.Errorf("gouraud+shadow nomsb px = 0x%04X, want 0x7C1F", got)
	}
}

func TestColorCalcGouraudHalfLuminance(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Gouraud table: A=neutral, B=R+15
	writeCmd16(v, 0x2000, 0x4210)
	writeCmd16(v, 0x2002, 0x421F)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0006) // CC=6 (Gouraud+half lum)
	writeCmd16(v, 0x06, 0x800A) // R=10 + MSB
	writeCmd16(v, 0x0C, 0)      // XA=0
	writeCmd16(v, 0x0E, 10)     // YA=10
	writeCmd16(v, 0x10, 10)     // XB=10
	writeCmd16(v, 0x12, 10)     // YB=10
	writeCmd16(v, 0x1C, 0x2000/8)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// x=0: Gouraud correction=0, R=10, halved=5. Pixel=0x8005
	if got := readFBPixel(v, 0, 10); got != 0x8005 {
		t.Errorf("gouraud+halflum px(0,10) = 0x%04X, want 0x8005", got)
	}
	// x=10: Gouraud correction=+15, R=clamp(25)=25, halved=12. Pixel=0x800C
	if got := readFBPixel(v, 10, 10); got != 0x800C {
		t.Errorf("gouraud+halflum px(10,10) = 0x%04X, want 0x800C", got)
	}
}

func TestColorCalcGouraudHalfTransparent(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Gouraud table: uniform R+5 correction
	writeCmd16(v, 0x2000, 0x4215)
	writeCmd16(v, 0x2002, 0x4215)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0007) // CC=7 (Gouraud+half transp)
	writeCmd16(v, 0x06, 0x800A) // R=10 + MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeCmd16(v, 0x1C, 0x2000/8)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill destination at (5,5) after VBlankIn so it's in the draw buffer
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0x80
	v.drawFB[off+1] = 0x14

	drainDrawing(v)

	// Gouraud: R=10+5=15. Blend: R=(15+20)/2=17. Pixel=0x8011
	if got := readFBPixel(v, 5, 5); got != 0x8011 {
		t.Errorf("gouraud+halftransp px = 0x%04X, want 0x8011", got)
	}
}

func TestColorCalcGouraudHalfTransparentNoMSB(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x2000, 0x4210)
	writeCmd16(v, 0x2002, 0x4210)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0007) // CC=7
	writeCmd16(v, 0x06, 0x800A) // R=10 + MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeCmd16(v, 0x1C, 0x2000/8)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill destination after VBlankIn WITHOUT MSB: R=20 = 0x0014
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0x00
	v.drawFB[off+1] = 0x14

	drainDrawing(v)

	// Dest has no MSB: replace with Gouraud-corrected src = 0x800A
	if got := readFBPixel(v, 5, 5); got != 0x800A {
		t.Errorf("gouraud+halftransp nomsb px = 0x%04X, want 0x800A", got)
	}
}

func TestMSBOnForcesReplace(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x8002) // MON=1 (bit15), CC=2 (half-lum)
	writeCmd16(v, 0x06, 0x03E0) // green=31, no source MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// MON forces CC=0 (replace), then sets MSB: 0x03E0|0x8000 = 0x83E0
	if got := readFBPixel(v, 5, 5); got != 0x83E0 {
		t.Errorf("msbon+halflum px = 0x%04X, want 0x83E0", got)
	}
}

func TestMSBOnSuppressesShadow(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x8001) // MON=1, CC=1 (shadow)
	writeCmd16(v, 0x06, 0x8000) // black + MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill destination after VBlankIn with MSB-set pixel: 0xFC1F
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0xFC
	v.drawFB[off+1] = 0x1F

	drainDrawing(v)

	// MON forces CC=0, shadow skipped. src=0x8000, MSB set -> 0x8000
	if got := readFBPixel(v, 5, 5); got != 0x8000 {
		t.Errorf("msbon+shadow px = 0x%04X, want 0x8000", got)
	}
}

func TestMSBOnSuppressesHalfTransparent(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x8003) // MON=1, CC=3 (half-transp)
	writeCmd16(v, 0x06, 0x800A) // R=10 + MSB
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x10, 5)
	writeCmd16(v, 0x12, 5)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()

	// Pre-fill destination after VBlankIn: R=20 + MSB = 0x8014
	off := (5*vdp1FBStride + 5) * 2
	v.drawFB[off] = 0x80
	v.drawFB[off+1] = 0x14

	drainDrawing(v)

	// MON forces CC=0, no blending. src=0x800A, MSB set -> 0x800A
	if got := readFBPixel(v, 5, 5); got != 0x800A {
		t.Errorf("msbon+halftransp px = 0x%04X, want 0x800A", got)
	}
}

// --- Mesh processing tests ---

func TestMeshLine(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Horizontal line with mesh enabled at y=10
	writeCmd16(v, 0x00, 0x0006) // line
	writeCmd16(v, 0x04, 0x0100) // CMDPMOD: mesh=1 (bit 8)
	writeCmd16(v, 0x06, 0x1234) // CMDCOLR
	writeCmd16(v, 0x0C, 0)      // XA=0
	writeCmd16(v, 0x0E, 10)     // YA=10
	writeCmd16(v, 0x10, 7)      // XB=7
	writeCmd16(v, 0x12, 10)     // YB=10
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// y=10 is even. Mesh draws where (x^y)&1 == 0, i.e. even x only.
	for x := 0; x <= 7; x++ {
		got := readFBPixel(v, x, 10)
		if (x^10)&1 == 0 {
			if got != 0x1234 {
				t.Errorf("mesh px(%d,10) = 0x%04X, want 0x1234 (drawn)", x, got)
			}
		} else {
			if got != 0 {
				t.Errorf("mesh px(%d,10) = 0x%04X, want 0x0000 (skipped)", x, got)
			}
		}
	}
}

func TestMeshPolygon(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Small polygon with mesh
	writeCmd16(v, 0x00, 0x0004) // polygon
	writeCmd16(v, 0x04, 0x0100) // CMDPMOD: mesh=1
	writeCmd16(v, 0x06, 0x5678) // CMDCOLR
	writeCmd16(v, 0x0C, 0)      // A(0,0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 3) // B(3,0)
	writeCmd16(v, 0x12, 0)
	writeCmd16(v, 0x14, 3) // C(3,3)
	writeCmd16(v, 0x16, 3)
	writeCmd16(v, 0x18, 0) // D(0,3)
	writeCmd16(v, 0x1A, 3)
	writeDrawEnd(v, 0x20)
	v.VBlankIn()
	drainDrawing(v)

	// Check checkerboard: drawn where (x^y)&1 == 0
	for y := 0; y <= 3; y++ {
		for x := 0; x <= 3; x++ {
			got := readFBPixel(v, x, y)
			if (x^y)&1 == 0 {
				if got != 0x5678 {
					t.Errorf("mesh px(%d,%d) = 0x%04X, want 0x5678 (drawn)", x, y, got)
				}
			} else {
				if got != 0 {
					t.Errorf("mesh px(%d,%d) = 0x%04X, want 0x0000 (skipped)", x, y, got)
				}
			}
		}
	}
}

// --- Pre-clipping tests ---

func TestPreClipRejectsOffscreen(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// System clip default 319x223. Sprite at x=400 (fully offscreen).
	// Pclp=0 (bit 11 clear = pre-clip enabled, which is the default)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0020) // CMDPMOD: color mode 4, Pclp=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 400)    // XA=400 (offscreen)
	writeCmd16(v, 0x0E, 0)
	writeDrawEnd(v, 0x20)

	v.WriteVRAM(0x1000, 0x10)
	v.VBlankIn()
	drainDrawing(v)

	// No pixels should be drawn at x=400 (offscreen and pre-clipped)
	// Check a few onscreen pixels are empty too
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0000", got)
	}
}

func TestPreClipDisabled(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Same sprite offscreen, but Pclp=1 (bit 11 set = pre-clip disabled)
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0820) // CMDPMOD: color mode 4, Pclp=1 (bit 11)
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 400)    // XA=400 (offscreen)
	writeCmd16(v, 0x0E, 0)
	writeDrawEnd(v, 0x20)

	v.WriteVRAM(0x1000, 0x10)
	v.VBlankIn()
	drainDrawing(v)

	// Still no pixels drawn (individual clip catches it), but no crash
	if got := readFBPixel(v, 0, 0); got != 0 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0000", got)
	}
}

func TestPreClipPartiallyOnscreen(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Sprite at x=-4, partially onscreen. Pclp=0 (pre-clip enabled).
	// bbox (-4,0) to (3,0) overlaps clip (0,0)-(319,223), NOT rejected.
	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x04, 0x0020) // Pclp=0
	writeCmd16(v, 0x06, 0x0100)
	writeCmd16(v, 0x08, 0x1000/8)
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 0xFFFC) // XA=-4 (signed)
	writeCmd16(v, 0x0E, 0)
	writeDrawEnd(v, 0x20)

	for i := 0; i < 8; i++ {
		v.WriteVRAM(0x1000+uint32(i), uint8(0x10+i))
	}
	v.VBlankIn()
	drainDrawing(v)

	// Pixels at x=0..3 should be drawn (srcX 4..7)
	if got := readFBPixel(v, 0, 0); got != 0x0114 {
		t.Errorf("px(0,0) = 0x%04X, want 0x0114", got)
	}
	if got := readFBPixel(v, 3, 0); got != 0x0117 {
		t.Errorf("px(3,0) = 0x%04X, want 0x0117", got)
	}
}

// --- User clipping tests ---

func TestUserClipInside(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set user clip to (5,5)-(15,15)
	writeCmd16(v, 0x00, 0x0008) // user clip command
	writeCmd16(v, 0x0C, 5)      // XA
	writeCmd16(v, 0x0E, 5)      // YA
	writeCmd16(v, 0x14, 15)     // XC
	writeCmd16(v, 0x16, 15)     // YC

	// Horizontal line y=10, x=0..20 with inside clip mode
	// CMDPMOD: Clip=1, Cmod=0 -> bits 10:9 = 10 -> 0x0400
	writeCmd16(v, 0x20, 0x0006) // line
	writeCmd16(v, 0x24, 0x0400) // CMDPMOD: user clip inside
	writeCmd16(v, 0x26, 0x1234) // CMDCOLR
	writeCmd16(v, 0x2C, 0)      // XA=0
	writeCmd16(v, 0x2E, 10)     // YA=10
	writeCmd16(v, 0x30, 20)     // XB=20
	writeCmd16(v, 0x32, 10)     // YB=10
	writeDrawEnd(v, 0x40)
	v.VBlankIn()
	drainDrawing(v)

	// Inside mode, boundary excluded: pixels at x=6..14 drawn (strictly inside 5..15)
	// x=5 is boundary -> excluded
	if got := readFBPixel(v, 5, 10); got != 0 {
		t.Errorf("inside clip boundary px(5,10) = 0x%04X, want 0x0000", got)
	}
	// x=6 is inside
	if got := readFBPixel(v, 6, 10); got != 0x1234 {
		t.Errorf("inside clip px(6,10) = 0x%04X, want 0x1234", got)
	}
	// x=14 is inside
	if got := readFBPixel(v, 14, 10); got != 0x1234 {
		t.Errorf("inside clip px(14,10) = 0x%04X, want 0x1234", got)
	}
	// x=15 is boundary -> excluded
	if got := readFBPixel(v, 15, 10); got != 0 {
		t.Errorf("inside clip boundary px(15,10) = 0x%04X, want 0x0000", got)
	}
	// x=3 is outside
	if got := readFBPixel(v, 3, 10); got != 0 {
		t.Errorf("inside clip outside px(3,10) = 0x%04X, want 0x0000", got)
	}
}

func TestUserClipOutside(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set user clip to (5,5)-(15,15)
	writeCmd16(v, 0x00, 0x0008)
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x14, 15)
	writeCmd16(v, 0x16, 15)

	// Horizontal line y=10, x=0..20 with outside clip mode
	// CMDPMOD: Clip=1, Cmod=1 -> bits 10:9 = 11 -> 0x0600
	writeCmd16(v, 0x20, 0x0006)
	writeCmd16(v, 0x24, 0x0600) // CMDPMOD: user clip outside
	writeCmd16(v, 0x26, 0x1234)
	writeCmd16(v, 0x2C, 0)
	writeCmd16(v, 0x2E, 10)
	writeCmd16(v, 0x30, 20)
	writeCmd16(v, 0x32, 10)
	writeDrawEnd(v, 0x40)
	v.VBlankIn()
	drainDrawing(v)

	// Outside mode, boundary included: pixels INSIDE rect (5..15 inclusive) are skipped
	// x=4 is outside -> drawn
	if got := readFBPixel(v, 4, 10); got != 0x1234 {
		t.Errorf("outside clip px(4,10) = 0x%04X, want 0x1234", got)
	}
	// x=5 is boundary -> skipped (boundary included in exclusion zone)
	if got := readFBPixel(v, 5, 10); got != 0 {
		t.Errorf("outside clip boundary px(5,10) = 0x%04X, want 0x0000", got)
	}
	// x=10 is inside -> skipped
	if got := readFBPixel(v, 10, 10); got != 0 {
		t.Errorf("outside clip inside px(10,10) = 0x%04X, want 0x0000", got)
	}
	// x=15 is boundary -> skipped
	if got := readFBPixel(v, 15, 10); got != 0 {
		t.Errorf("outside clip boundary px(15,10) = 0x%04X, want 0x0000", got)
	}
	// x=16 is outside -> drawn
	if got := readFBPixel(v, 16, 10); got != 0x1234 {
		t.Errorf("outside clip px(16,10) = 0x%04X, want 0x1234", got)
	}
}

func TestUserClipDisabled(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set user clip coords but mode=0 (disabled)
	writeCmd16(v, 0x00, 0x0008)
	writeCmd16(v, 0x0C, 5)
	writeCmd16(v, 0x0E, 5)
	writeCmd16(v, 0x14, 15)
	writeCmd16(v, 0x16, 15)

	// Line with user clip disabled (bits 10:9 = 00 -> 0x0000)
	writeCmd16(v, 0x20, 0x0006)
	writeCmd16(v, 0x24, 0x0000) // CMDPMOD: user clip disabled
	writeCmd16(v, 0x26, 0x1234)
	writeCmd16(v, 0x2C, 0)
	writeCmd16(v, 0x2E, 10)
	writeCmd16(v, 0x30, 20)
	writeCmd16(v, 0x32, 10)
	writeDrawEnd(v, 0x40)
	v.VBlankIn()
	drainDrawing(v)

	// All pixels 0..20 should be drawn (no user clip applied)
	for x := 0; x <= 20; x++ {
		if got := readFBPixel(v, x, 10); got != 0x1234 {
			t.Errorf("disabled clip px(%d,10) = 0x%04X, want 0x1234", x, got)
		}
	}
}

// --- ENDR register tests ---

func TestENDRStopsDrawing(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	// Set up a line command that would draw pixels
	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0000)
	writeCmd16(v, 0x06, 0x1234)
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 10)
	writeCmd16(v, 0x12, 0)
	writeDrawEnd(v, 0x20)

	// Write ENDR before draw. drawActive is false (no draw in flight),
	// so the write is ignored and the next frame draws normally.
	v.Write(0x0C, 0x0000)

	v.VBlankIn()
	drainDrawing(v)

	if got := readFBPixel(v, 5, 0); got != 0x1234 {
		t.Errorf("px(5,0) = 0x%04X, want 0x1234 (ENDR write ignored while idle)", got)
	}
}

func TestENDRRespectsDrawActiveGate(t *testing.T) {
	v := NewVDP1(NewSCU())

	// Idle: ENDR write must be a no-op.
	v.drawActive = false
	v.Write(0x0C, 0x0000)
	if v.drawEnd {
		t.Error("ENDR write while idle set drawEnd; expected no-op")
	}

	// Active: ENDR write must latch drawEnd so processCommands aborts.
	v.drawActive = true
	v.Write(0x0C, 0x0000)
	if !v.drawEnd {
		t.Error("ENDR write while drawActive did not set drawEnd")
	}
}

func TestDrawEndClearedAfterDrawDrains(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.Write(0x04, 2)

	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x04, 0x0000)
	writeCmd16(v, 0x06, 0x1234)
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 10)
	writeCmd16(v, 0x12, 0)
	writeDrawEnd(v, 0x20)

	v.VBlankIn()
	drainDrawing(v)

	if v.drawActive {
		t.Error("drawActive should be false after the draw drains")
	}
	if v.drawEnd {
		t.Error("drawEnd should be false after the draw drains")
	}
	if got := readFBPixel(v, 5, 0); got != 0x1234 {
		t.Errorf("px(5,0) = 0x%04X, want 0x1234", got)
	}
}

// readFBPixel8 reads a single 8-bit pixel from the draw framebuffer.
func readFBPixel8(v *VDP1, x, y int) uint8 {
	off := y*v.fbWidth() + x
	return v.drawFB[off]
}

func TestClipBounds_TVM001(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x01) // TVM=001 hi-res 8-bit

	v.sysClipX = 2000
	v.sysClipY = 300
	clipX, clipY := v.clipBounds()
	if clipX != 1023 {
		t.Errorf("clipX = %d, want 1023", clipX)
	}
	if clipY != 255 {
		t.Errorf("clipY = %d, want 255", clipY)
	}
}

func TestClipBounds_TVM010(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x02) // TVM=010 rotation 16-bit

	v.sysClipX = 600
	v.sysClipY = 300
	clipX, clipY := v.clipBounds()
	if clipX != 511 {
		t.Errorf("clipX = %d, want 511", clipX)
	}
	if clipY != 255 {
		t.Errorf("clipY = %d, want 255", clipY)
	}
}

func TestClipBounds_TVM011(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x03) // TVM=011 rotation 8-bit

	v.sysClipX = 600
	v.sysClipY = 600
	clipX, clipY := v.clipBounds()
	if clipX != 511 {
		t.Errorf("clipX = %d, want 511", clipX)
	}
	if clipY != 511 {
		t.Errorf("clipY = %d, want 511", clipY)
	}
}

func TestEraseFrameBuffer8bpp_TVM001(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x01)   // TVM=001 hi-res 8-bit
	v.Write(0x06, 0xAABB) // EWDR: even=0xAA, odd=0xBB

	// X units are 16 pixels in 8-bit mode
	// Region: x1=0 (reg 0), y1=0, x3=31 (reg 2, 2*16-1=31), y3=2
	v.Write(0x08, 0x0000)                 // EWLR: x1_reg=0, y1=0
	v.Write(0x0A, uint16(2<<9)|uint16(2)) // EWRR: x3_reg=2, y3=2

	v.latchPending()
	v.eraseFrameBuffer(v.drawFB)

	// Verify even/odd byte pattern
	for y := 0; y <= 2; y++ {
		for x := 0; x <= 31; x++ {
			off := y*1024 + x
			got := v.drawFB[off]
			var want uint8
			if x&1 == 0 {
				want = 0xAA
			} else {
				want = 0xBB
			}
			if got != want {
				t.Errorf("(%d,%d) = 0x%02X, want 0x%02X", x, y, got, want)
			}
		}
	}
	// Verify outside region is untouched
	off := 0*1024 + 32
	if v.drawFB[off] != 0 {
		t.Errorf("pixel at (32,0) should be 0, got 0x%02X", v.drawFB[off])
	}
}

func TestEraseFrameBuffer8bpp_TVM011(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x03)   // TVM=011 rotation 8-bit
	v.Write(0x06, 0xCCDD) // EWDR: even=0xCC, odd=0xDD

	// Region using 16-pixel X units, test Y can go beyond 255
	v.Write(0x08, 0x0000)                   // EWLR: x1_reg=0, y1=0
	v.Write(0x0A, uint16(1<<9)|uint16(300)) // EWRR: x3_reg=1, y3=300

	v.latchPending()
	v.eraseFrameBuffer(v.drawFB)

	// Verify at y=300 (beyond 255 limit of non-rotation modes)
	off := 300*512 + 0
	if v.drawFB[off] != 0xCC {
		t.Errorf("(0,300) = 0x%02X, want 0xCC", v.drawFB[off])
	}
	off = 300*512 + 1
	if v.drawFB[off] != 0xDD {
		t.Errorf("(1,300) = 0x%02X, want 0xDD", v.drawFB[off])
	}
}

func TestWritePixel8bpp(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x01) // TVM=001 hi-res 8-bit
	v.Write(0x04, 0x02) // PTMR=2 auto draw

	// Write a 256-color (mode 4) normal sprite at (10, 5)
	// CMDCOLR = 0x1200 (color bank upper bits)
	// Character data: single pixel with value 0x34
	charAddr := uint32(0x20) // CMDSRCA * 8 = 0x100
	v.WriteVRAM(0x100, 0x34) // 8bpp pixel value

	// Command at address 0
	writeCmd16(v, 0x00, 0x0000)           // CMDCTRL: normal sprite, jp=next
	writeCmd16(v, 0x02, 0x0000)           // CMDLINK
	writeCmd16(v, 0x04, 0x0060)           // CMDPMOD: SPD=1, ECD=1, colorMode=4 (256-color), CC=0
	writeCmd16(v, 0x06, 0x1200)           // CMDCOLR
	writeCmd16(v, 0x08, uint16(charAddr)) // CMDSRCA
	writeCmd16(v, 0x0A, 0x0101)           // CMDSIZE: 8x1
	writeCmd16(v, 0x0C, 10)               // CMDXA = 10
	writeCmd16(v, 0x0E, 5)                // CMDYA = 5
	writeDrawEnd(v, 0x20)

	v.sysClipX = 1023
	v.sysClipY = 255
	drainDrawing(v)

	// Expected pixel value: (0x1200 & 0xFF00) | (0x34 & 0xFF) = 0x1234
	// In 8-bit mode, low 8 bits are written: 0x34
	got := readFBPixel8(v, 10, 5)
	if got != 0x34 {
		t.Errorf("pixel at (10,5) = 0x%02X, want 0x34", got)
	}

	// Verify offset is at y*1024 + x (stride 1024 for TVM=001)
	rawOff := 5*1024 + 10
	if v.drawFB[rawOff] != 0x34 {
		t.Errorf("raw offset %d = 0x%02X, want 0x34", rawOff, v.drawFB[rawOff])
	}
}

func TestWritePixel8bpp_TVM011(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x03) // TVM=011 rotation 8-bit
	v.Write(0x04, 0x02) // PTMR=2 auto draw

	// 256-color sprite at (10, 300) - beyond 256 height limit of non-rotation
	charAddr := uint32(0x20)
	v.WriteVRAM(0x100, 0x42)

	writeCmd16(v, 0x00, 0x0000)
	writeCmd16(v, 0x02, 0x0000)
	writeCmd16(v, 0x04, 0x0060) // colorMode=4, SPD=1, ECD=1
	writeCmd16(v, 0x06, 0xFF00) // CMDCOLR
	writeCmd16(v, 0x08, uint16(charAddr))
	writeCmd16(v, 0x0A, 0x0101) // 8x1
	writeCmd16(v, 0x0C, 10)     // x=10
	writeCmd16(v, 0x0E, 300)    // y=300
	writeDrawEnd(v, 0x20)

	v.sysClipX = 511
	v.sysClipY = 511
	drainDrawing(v)

	got := readFBPixel8(v, 10, 300)
	if got != 0x42 {
		t.Errorf("pixel at (10,300) = 0x%02X, want 0x42", got)
	}

	// Verify offset uses stride 512 (TVM=011)
	rawOff := 300*512 + 10
	if v.drawFB[rawOff] != 0x42 {
		t.Errorf("raw offset %d = 0x%02X, want 0x42", rawOff, v.drawFB[rawOff])
	}
}

func TestWritePixel8bpp_RestrictionsEnforced(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)
	v.Write(0x00, 0x01) // TVM=001 hi-res 8-bit
	v.Write(0x04, 0x02) // PTMR=2

	// Draw a line with CC=3 (half-transparent) - should be forced to CC=0 (replace)
	writeCmd16(v, 0x00, 0x0006) // CMDCTRL: line command
	writeCmd16(v, 0x02, 0x0000)
	writeCmd16(v, 0x04, 0x0063) // CMDPMOD: SPD=1, ECD=1, colorMode=4, CC=3
	writeCmd16(v, 0x06, 0x00AB) // CMDCOLR = 0x00AB
	writeCmd16(v, 0x08, 0x0000)
	writeCmd16(v, 0x0A, 0x0000)
	writeCmd16(v, 0x0C, 5) // xa=5
	writeCmd16(v, 0x0E, 5) // ya=5
	writeCmd16(v, 0x10, 5) // xb=5 (single pixel line)
	writeCmd16(v, 0x12, 5) // yb=5
	writeDrawEnd(v, 0x20)

	v.sysClipX = 1023
	v.sysClipY = 255
	drainDrawing(v)

	// CC was forced to 0 (replace), so pixel should be written as-is
	got := readFBPixel8(v, 5, 5)
	if got != 0xAB {
		t.Errorf("pixel at (5,5) = 0x%02X, want 0xAB (CC forced to replace)", got)
	}

	// Test MSB On - should be forced off in 8-bit mode
	v2 := NewVDP1(scu)
	v2.Write(0x00, 0x01) // TVM=001
	v2.Write(0x04, 0x02)

	writeCmd16(v2, 0x00, 0x0006) // line
	writeCmd16(v2, 0x02, 0x0000)
	writeCmd16(v2, 0x04, 0x8060) // CMDPMOD: MSB ON, SPD=1, ECD=1, colorMode=4, CC=0
	writeCmd16(v2, 0x06, 0x0055)
	writeCmd16(v2, 0x08, 0x0000)
	writeCmd16(v2, 0x0A, 0x0000)
	writeCmd16(v2, 0x0C, 3)
	writeCmd16(v2, 0x0E, 3)
	writeCmd16(v2, 0x10, 3)
	writeCmd16(v2, 0x12, 3)
	writeDrawEnd(v2, 0x20)

	v2.sysClipX = 1023
	v2.sysClipY = 255
	drainDrawing(v2)

	// MSB is forced off, so pixel should be 0x55 not 0xD5 (0x55|0x80)
	got2 := readFBPixel8(v2, 3, 3)
	if got2 != 0x55 {
		t.Errorf("pixel at (3,3) = 0x%02X, want 0x55 (MSB forced off)", got2)
	}
}

func TestFloorDiv64(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{10, 3, 3},   // positive: 10/3 = 3.33 -> 3
		{-10, 3, -4}, // negative dividend: -10/3 = -3.33 -> -4
		{10, -3, -4}, // negative divisor: 10/-3 = -3.33 -> -4
		{-10, -3, 3}, // both negative: -10/-3 = 3.33 -> 3
		{9, 3, 3},    // exact division
		{-9, 3, -3},  // exact negative
		{0, 5, 0},    // zero
		{7, 1, 7},    // divisor 1
		{-7, 1, -7},  // negative with divisor 1
	}
	for _, tc := range cases {
		got := floorDiv64(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("floorDiv64(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDDAEdgeFloorDivision(t *testing.T) {
	// Verify DDA with negative delta uses floor division.
	// Edge from (7, 0) to (0, 0) in 3 steps.
	// floorDiv64(-7<<16, 3) = floorDiv64(-458752, 3) = -152918
	// Step 0: x = 7
	// Step 1: xFP = 7<<16 + -152918 = 305818, x = 305818>>16 = 4
	// Step 2: xFP = 305818 + -152918 = 152900, x = 152900>>16 = 2
	// Step 3: xFP = 152900 + -152918 = -18, x = -18>>16 = -1
	// (rendering code forces exact endpoint on last step)
	e := initDDAEdge(7, 0, 0, 0, 3)
	if e.intX() != 7 {
		t.Errorf("step 0: x=%d, want 7", e.intX())
	}
	e.step()
	if e.intX() != 4 {
		t.Errorf("step 1: x=%d, want 4", e.intX())
	}
	e.step()
	if e.intX() != 2 {
		t.Errorf("step 2: x=%d, want 2", e.intX())
	}
}

// --- VDP1 DIE=1 Double-Density Interlace Tests ---

// dieTestVDP1 returns a VDP1 with DIE=1 and the given DIL bit in TVM=0
// (normal 16bpp FB). DIE=1 with TVM=0 falls back to the standard swap
// path: DIL does not route writes between FBs, VBlankIn swaps as
// usual, and writePixel applies neither parity filter nor Y halving.
// Tests for the FB-pinning / parity behavior use a TVM=1 setup
// directly (see TestDIEDoubledYRoutesByParity etc.).
func dieTestVDP1(dil bool) (*VDP1, int, int) {
	v := NewVDP1(NewSCU())
	v.Write(0x02, 0x08)
	if dil {
		v.fbcr |= 0x04
	}
	clipX := v.fbWidth() - 1
	clipY := v.fbHeightLogical() - 1
	return v, clipX, clipY
}

func TestDIEDilRoutesToFB(t *testing.T) {
	// DIE=1 + TVM=0: DIL bit does not reroute writes — both DIL=0
	// and DIL=1 land in drawFB (the swap-managed buffer). DIL-based
	// routing is only active under dieDoubled() (TVM=1, see
	// TestDIEDoubledYRoutesByParity for the routing semantics).
	for _, dil := range []bool{false, true} {
		v, clipX, clipY := dieTestVDP1(dil)
		v.writePixel(0, 5, 0x1F00, 0, 0, false, false, 0, clipX, clipY)
		if readFBPixel(v, 0, 5) != 0x1F00 || readDisplayFBPixel(v, 0, 5) != 0 {
			t.Errorf("DIE=1 TVM=0 DIL=%v: drawFB=0x%04X displayFB=0x%04X, want write in drawFB only",
				dil, readFBPixel(v, 0, 5), readDisplayFBPixel(v, 0, 5))
		}
	}
}

func TestDIECPUFBIORouting(t *testing.T) {
	// DIE=1 + TVM=0: CPU framebuffer I/O always targets drawFB
	// regardless of DIL. DIL-based routing is dieDoubled() only.
	for _, dil := range []bool{false, true} {
		v, _, _ := dieTestVDP1(dil)
		v.WriteFB(0x0010, 0xAB)
		if v.drawFB[0x0010] != 0xAB || v.displayFB[0x0010] != 0 {
			t.Errorf("DIE=1 TVM=0 DIL=%v WriteFB: drawFB[0x10]=0x%02X displayFB[0x10]=0x%02X, want write in drawFB only",
				dil, v.drawFB[0x0010], v.displayFB[0x0010])
		}
		if got := v.ReadFB(0x0010); got != 0xAB {
			t.Errorf("DIE=1 TVM=0 DIL=%v ReadFB(0x10) = 0x%02X, want 0xAB", dil, got)
		}
	}

	// 16/32-bit variants also target drawFB (no DIL routing).
	v3, _, _ := dieTestVDP1(true)
	v3.WriteFB16(0x0020, 0xBEEF)
	v3.WriteFB32(0x0040, 0xDEADBEEF)
	if v3.drawFB[0x0020] != 0xBE || v3.drawFB[0x0021] != 0xEF {
		t.Error("DIE=1 TVM=0 WriteFB16 did not target drawFB")
	}
	if v3.drawFB[0x0040] != 0xDE || v3.drawFB[0x0043] != 0xEF {
		t.Error("DIE=1 TVM=0 WriteFB32 did not target drawFB")
	}
	if v3.displayFB[0x0020] != 0 || v3.displayFB[0x0040] != 0 {
		t.Error("DIE=1 TVM=0 WriteFB16/32 leaked into displayFB")
	}
}

func TestDIEVBlankInNoSwap(t *testing.T) {
	// Pinning + no-swap behavior is dieDoubled() only (TVM=1 + DIE=1).
	v := NewVDP1(NewSCU())
	v.tvmr = 0x0001     // TVM=1
	v.Write(0x02, 0x08) // DIE=1, DIL=0, FCM=0, FCT=0

	v.drawFB[0] = 0xAB
	v.displayFB[0] = 0xCD

	// Snapshot slice headers to verify no swap.
	preDraw := &v.drawFB[0]
	preDisplay := &v.displayFB[0]

	v.VBlankIn()

	if &v.drawFB[0] != preDraw || &v.displayFB[0] != preDisplay {
		t.Error("DIE=1 + TVM=1 VBlankIn must not swap drawFB/displayFB roles")
	}
}

func TestDIEVBlankInErasesOnlyDilMatchedFB(t *testing.T) {
	// Erase-only-the-DIL-target behavior is dieDoubled() only (TVM=1).
	// DIL=0: erase drawFB only; displayFB preserved.
	v := NewVDP1(NewSCU())
	v.tvmr = 0x0001     // TVM=1
	v.Write(0x02, 0x08) // DIE=1 DIL=0 FCM=0 FCT=0
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x80FF) // 128 H-units * 8 px = 1024 wide (TVM=1 FB)
	v.Write(0x06, 0x1234)

	// Marker in displayFB that must survive.
	v.displayFB[0x100] = 0x99

	v.VBlankIn()

	// drawFB filled with EWDR (8bpp: bit 15..8 = even-X, 7..0 = odd-X).
	if v.drawFB[0] != 0x12 || v.drawFB[1] != 0x34 {
		t.Errorf("DIL=0: drawFB head = 0x%02X%02X, want 0x1234", v.drawFB[0], v.drawFB[1])
	}
	// displayFB marker preserved.
	if v.displayFB[0x100] != 0x99 {
		t.Errorf("DIL=0: displayFB marker = 0x%02X, want 0x99 (untouched)", v.displayFB[0x100])
	}

	// DIL=1: erase displayFB only; drawFB preserved.
	v2 := NewVDP1(NewSCU())
	v2.tvmr = 0x0001
	v2.Write(0x02, 0x0C) // DIE=1 DIL=1
	v2.Write(0x08, 0x0000)
	v2.Write(0x0A, 0x80FF)
	v2.Write(0x06, 0x5678)
	v2.drawFB[0x100] = 0x88

	v2.VBlankIn()

	if v2.displayFB[0] != 0x56 || v2.displayFB[1] != 0x78 {
		t.Errorf("DIL=1: displayFB head = 0x%02X%02X, want 0x5678",
			v2.displayFB[0], v2.displayFB[1])
	}
	if v2.drawFB[0x100] != 0x88 {
		t.Errorf("DIL=1: drawFB marker = 0x%02X, want 0x88 (untouched)", v2.drawFB[0x100])
	}
}

func TestDIEPolygonSpanClipLogicalHeight(t *testing.T) {
	// Under doubled-Y DIE mode (TVM=1 + DIE=1) the clip Y range
	// doubles so commands targeting the lower half of the displayed
	// image survive into writePixel for halving.
	v := NewVDP1(NewSCU())
	v.tvmr = 0x0001     // TVM=1 (hi-res 8bpp)
	v.Write(0x02, 0x08) // DIE=1
	v.latchPending()
	v.sysClipY = 511
	v.sysClipX = 1023

	_, clipY := v.clipBounds()
	if clipY < 447 {
		t.Errorf("doubled-Y DIE clipBounds Y = %d, want >= 447 (logical)", clipY)
	}

	// Without TVM=1 the Y range is physical (single density).
	v2 := NewVDP1(NewSCU())
	v2.Write(0x02, 0x08) // DIE=1 only
	v2.latchPending()
	v2.sysClipY = 511
	v2.sysClipX = 1023
	_, clipY2 := v2.clipBounds()
	if clipY2 != v2.fbHeight()-1 {
		t.Errorf("DIE=1 + TVM=0 clipBounds Y = %d, want %d (physical)", clipY2, v2.fbHeight()-1)
	}
}

func TestDIEDoubledYHalvesAndFiltersParity(t *testing.T) {
	// Under TVM=1 + DIE=1 the renderer halves Y for FB row addressing
	// and applies the DIL parity filter. Use 8bpp pixel format for the
	// hi-res FB.
	v := NewVDP1(NewSCU())
	v.tvmr = 0x0001     // TVM=1 (hi-res 8bpp)
	v.Write(0x02, 0x08) // DIE=1, DIL=0
	v.latchPending()
	clipX, clipY := v.clipBounds()

	// DIL=0 even-Y plot: Y=10 -> drawFB row 5 col 7.
	v.writePixel(7, 10, 0x42, 0, 0, false, false, 0, clipX, clipY)
	off := 5*v.fbWidth() + 7
	if v.drawFB[off] != 0x42 {
		t.Errorf("DIL=0 even-Y: drawFB[row=5,col=7] = 0x%02X, want 0x42", v.drawFB[off])
	}

	// DIL=0 odd-Y plot rejected.
	v.writePixel(7, 11, 0x99, 0, 0, false, false, 0, clipX, clipY)
	for row := 0; row < 12; row++ {
		if row == 5 {
			continue
		}
		if v.drawFB[row*v.fbWidth()+7] != 0 {
			t.Errorf("DIL=0 odd-Y plot leaked into drawFB row %d", row)
		}
	}
}

func TestDIEDoubledYRoutesByParity(t *testing.T) {
	// TVM=1 + DIE=1 + DIL=1: odd-Y plots go to displayFB row Y/2.
	v := NewVDP1(NewSCU())
	v.tvmr = 0x0001
	v.Write(0x02, 0x0C) // DIE=1, DIL=1
	v.latchPending()
	clipX, clipY := v.clipBounds()

	v.writePixel(7, 11, 0xAA, 0, 0, false, false, 0, clipX, clipY)
	off := 5*v.fbWidth() + 7
	if v.displayFB[off] != 0xAA {
		t.Errorf("DIL=1 odd-Y: displayFB[row=5,col=7] = 0x%02X, want 0xAA", v.displayFB[off])
	}
	if v.drawFB[off] != 0 {
		t.Error("DIL=1 odd-Y plot leaked into drawFB")
	}

	// Even-Y plot rejected under DIL=1.
	v.writePixel(7, 10, 0xBB, 0, 0, false, false, 0, clipX, clipY)
	if v.displayFB[off] != 0xAA {
		t.Errorf("DIL=1 even-Y: displayFB[row=5,col=7] = 0x%02X, want unchanged 0xAA", v.displayFB[off])
	}
}

func TestDIENonDoubledKeepsLiteralY(t *testing.T) {
	// DIE=1 with TVM=0 (Sonic Jam game-select pattern): the FB-pinning
	// special case does NOT apply — writes go to the swap-managed
	// drawFB at literal Y, with no parity filter and no Y halving.
	// (DIL is a no-op here; pinning is dieDoubled() only.)
	v, clipX, clipY := dieTestVDP1(true) // DIL=1, TVM=0
	v.writePixel(7, 10, 0x1234, 0, 0, false, false, 0, clipX, clipY)
	if got := readFBPixel(v, 7, 10); got != 0x1234 {
		t.Errorf("TVM=0 DIE=1: drawFB row 10 = 0x%04X, want 0x1234 (literal Y)", got)
	}
	if got := readFBPixel(v, 7, 5); got != 0 {
		t.Errorf("TVM=0 DIE=1: row 5 should be empty (no Y/2): 0x%04X", got)
	}
	v.writePixel(7, 11, 0x5678, 0, 0, false, false, 0, clipX, clipY)
	if got := readFBPixel(v, 7, 11); got != 0x5678 {
		t.Errorf("TVM=0 DIE=1 odd-Y: drawFB row 11 = 0x%04X, want 0x5678 (no parity filter)", got)
	}
	// displayFB must remain untouched — DIL=1 does not reroute under TVM=0.
	if readDisplayFBPixel(v, 7, 10) != 0 || readDisplayFBPixel(v, 7, 11) != 0 {
		t.Error("TVM=0 DIE=1: writes leaked into displayFB; DIL routing should be inactive")
	}
}

// TestMidCommandYieldPixelOutputMatches verifies that the chunked
// rasterizer produces the same framebuffer regardless of how many
// times it yields. For each command type, two VDP1 instances are
// configured identically; one is drained with a tiny budget that
// forces many yields, the other with a single large budget. The
// final framebuffers must match. cmdPhase must be phaseIdle and
// drawActive must be false on both after completion.
func TestMidCommandYieldPixelOutputMatches(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*VDP1)
	}{
		{
			name: "normal_sprite_8x4_4bpp",
			setup: func(v *VDP1) {
				writeCmd16(v, 0x00, 0x0000)
				writeCmd16(v, 0x04, 0x0040)
				writeCmd16(v, 0x06, 0x0010)
				writeCmd16(v, 0x08, 0x1000/8)
				writeCmd16(v, 0x0A, 0x0104)
				writeCmd16(v, 0x0C, 5)
				writeCmd16(v, 0x0E, 7)
				writeDrawEnd(v, 0x20)
				for i := uint32(0); i < 16; i++ {
					v.WriteVRAM(0x1000+i, uint8(0x12+i))
				}
			},
		},
		{
			name: "scaled_sprite_8x4_to_16x8_4bpp",
			setup: func(v *VDP1) {
				writeCmd16(v, 0x00, 0x0001)
				writeCmd16(v, 0x04, 0x0040)
				writeCmd16(v, 0x06, 0x0020)
				writeCmd16(v, 0x08, 0x1000/8)
				writeCmd16(v, 0x0A, 0x0104)
				writeCmd16(v, 0x0C, 0)
				writeCmd16(v, 0x0E, 0)
				writeCmd16(v, 0x14, 15)
				writeCmd16(v, 0x16, 7)
				writeDrawEnd(v, 0x20)
				for i := uint32(0); i < 16; i++ {
					v.WriteVRAM(0x1000+i, uint8(0xA0+i))
				}
			},
		},
		{
			name: "polygon_quad",
			setup: func(v *VDP1) {
				writeCmd16(v, 0x00, 0x0004)
				writeCmd16(v, 0x04, 0x0000)
				writeCmd16(v, 0x06, 0x1234)
				writeCmd16(v, 0x0C, 10)
				writeCmd16(v, 0x0E, 10)
				writeCmd16(v, 0x10, 30)
				writeCmd16(v, 0x12, 12)
				writeCmd16(v, 0x14, 32)
				writeCmd16(v, 0x16, 28)
				writeCmd16(v, 0x18, 8)
				writeCmd16(v, 0x1A, 26)
				writeDrawEnd(v, 0x20)
			},
		},
		{
			name: "line",
			setup: func(v *VDP1) {
				writeCmd16(v, 0x00, 0x0006)
				writeCmd16(v, 0x06, 0x5678)
				writeCmd16(v, 0x0C, 2)
				writeCmd16(v, 0x0E, 3)
				writeCmd16(v, 0x10, 50)
				writeCmd16(v, 0x12, 28)
				writeDrawEnd(v, 0x20)
			},
		},
		{
			name: "polyline_quad",
			setup: func(v *VDP1) {
				writeCmd16(v, 0x00, 0x0005)
				writeCmd16(v, 0x06, 0xABCD)
				writeCmd16(v, 0x0C, 5)
				writeCmd16(v, 0x0E, 5)
				writeCmd16(v, 0x10, 25)
				writeCmd16(v, 0x12, 7)
				writeCmd16(v, 0x14, 27)
				writeCmd16(v, 0x16, 23)
				writeCmd16(v, 0x18, 4)
				writeCmd16(v, 0x1A, 21)
				writeDrawEnd(v, 0x20)
			},
		},
		{
			name: "distorted_sprite_quad",
			setup: func(v *VDP1) {
				writeCmd16(v, 0x00, 0x0002)
				writeCmd16(v, 0x04, 0x0040)
				writeCmd16(v, 0x06, 0x0010)
				writeCmd16(v, 0x08, 0x1000/8)
				writeCmd16(v, 0x0A, 0x0104)
				writeCmd16(v, 0x0C, 8)
				writeCmd16(v, 0x0E, 8)
				writeCmd16(v, 0x10, 28)
				writeCmd16(v, 0x12, 6)
				writeCmd16(v, 0x14, 30)
				writeCmd16(v, 0x16, 24)
				writeCmd16(v, 0x18, 6)
				writeCmd16(v, 0x1A, 22)
				writeDrawEnd(v, 0x20)
				for i := uint32(0); i < 16; i++ {
					v.WriteVRAM(0x1000+i, uint8(0x30+i))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Atomic reference: single large budget.
			ref := NewVDP1(NewSCU())
			ref.ptmr = 2
			ref.startDraw()
			tc.setup(ref)
			ref.TickSystemCycles(1 << 30)
			if ref.drawActive {
				t.Fatalf("ref drawActive after large-budget drain")
			}
			if ref.cmdPhase != phaseIdle {
				t.Fatalf("ref cmdPhase=%d after completion, want phaseIdle", ref.cmdPhase)
			}

			// Yielding: tiny budget repeatedly.
			yld := NewVDP1(NewSCU())
			yld.ptmr = 2
			yld.startDraw()
			tc.setup(yld)
			const tinyBudget = 4
			const maxIterations = 200000
			for i := 0; yld.drawActive && i < maxIterations; i++ {
				yld.TickSystemCycles(tinyBudget)
			}
			if yld.drawActive {
				t.Fatalf("yld drawActive after %d tinyBudget iterations", maxIterations)
			}
			if yld.cmdPhase != phaseIdle {
				t.Fatalf("yld cmdPhase=%d after completion, want phaseIdle", yld.cmdPhase)
			}

			// Compare both framebuffers byte-for-byte.
			for i := range ref.drawFB {
				if ref.drawFB[i] != yld.drawFB[i] {
					t.Fatalf("drawFB byte %d: ref=0x%02X yld=0x%02X", i, ref.drawFB[i], yld.drawFB[i])
				}
			}
		})
	}
}

func TestDIEDisplayFBAccessor(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.drawFB[0] = 0x11
	v.displayFB[0] = 0x22

	// DIE=0: both fields return displayFB.
	v.Write(0x02, 0)
	v.latchPending()
	if v.DisplayFB(0)[0] != 0x22 || v.DisplayFB(1)[0] != 0x22 {
		t.Errorf("DIE=0: DisplayFB(0)[0]=0x%02X DisplayFB(1)[0]=0x%02X, want both 0x22",
			v.DisplayFB(0)[0], v.DisplayFB(1)[0])
	}

	// DIE=1 + TVM=0: field-based pinning is inactive; both fields
	// return the swap-managed displayFB.
	v.Write(0x02, 0x08)
	v.latchPending()
	if v.DisplayFB(0)[0] != 0x22 || v.DisplayFB(1)[0] != 0x22 {
		t.Errorf("DIE=1 TVM=0: DisplayFB(0)[0]=0x%02X DisplayFB(1)[0]=0x%02X, want both 0x22 (swap path)",
			v.DisplayFB(0)[0], v.DisplayFB(1)[0])
	}

	// DIE=1 + TVM=1: field-based pinning active; field=0 -> drawFB,
	// field=1 -> displayFB.
	v.tvmr = 0x0001
	if v.DisplayFB(0)[0] != 0x11 {
		t.Errorf("DIE=1 TVM=1 field=0: DisplayFB[0]=0x%02X, want 0x11 (drawFB)", v.DisplayFB(0)[0])
	}
	if v.DisplayFB(1)[0] != 0x22 {
		t.Errorf("DIE=1 TVM=1 field=1: DisplayFB[0]=0x%02X, want 0x22 (displayFB)", v.DisplayFB(1)[0])
	}
}
