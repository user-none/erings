package core

import "testing"

// TestRenderNBG_16MColorCell verifies the NBG cell path in the 16,770,000
// color format (CHCTLA N0CHCN=100): a 32-bit dot whose MSB is set renders
// its RGB888 value (word 0 low byte blue, word 1 high byte green, low
// byte red per VDP2 manual Figure 4.6), a dot with MSB clear is
// transparent, and N0TPON makes it opaque.
func TestRenderNBG_16MColorCell(t *testing.T) {
	v := newTestVDP2()
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0040 // 16.7M colors, 1x1 character
	v.regs[vdp2PNCN0] = 0x0000
	v.regs[vdp2MPABN0] = 0x0000
	v.regs[vdp2MPCDN0] = 0x0000
	v.regs[vdp2PRINA] = 0x0001

	// Pattern (0,0) -> character 0x400 at 0x8000.
	writeVRAM16(v, 0, 0x0000)
	writeVRAM16(v, 2, 0x0400)
	// Dot (0,0): opaque, B=0x10 G=0x20 R=0x30. Dot (1,0): MSB clear.
	v.vram[0x8000], v.vram[0x8001], v.vram[0x8002], v.vram[0x8003] = 0x80, 0x10, 0x20, 0x30
	v.vram[0x8004], v.vram[0x8005], v.vram[0x8006], v.vram[0x8007] = 0x00, 0x10, 0x20, 0x30

	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRGB(t, v, buf, 0, 0, 0x30, 0x20, 0x10, "16M cell opaque dot")
	expectTransparent(t, v, buf, 1, 0, "16M cell MSB-clear dot")

	v.regs[vdp2BGON] |= 1 << 8 // N0TPON
	clear(buf)
	renderTestNBG(v, 0, buf)
	expectRGB(t, v, buf, 1, 0, 0x30, 0x20, 0x10, "16M cell MSB-clear dot with N0TPON")
}

// TestRenderNBG_16MColorBitmap is the bitmap counterpart of
// TestRenderNBG_16MColorCell (CHCTLA N0BMEN with N0CHCN=100).
func TestRenderNBG_16MColorBitmap(t *testing.T) {
	v := newTestVDP2()
	v.regs[vdp2BGON] = 0x0001
	v.regs[vdp2CHCTLA] = 0x0042 // bitmap, 16.7M colors, 512x256
	v.regs[vdp2BMPNA] = 0x0000
	v.regs[vdp2MPOFN] = 0x0000
	v.regs[vdp2PRINA] = 0x0001

	v.vram[0], v.vram[1], v.vram[2], v.vram[3] = 0x80, 0x10, 0x20, 0x30
	v.vram[4], v.vram[5], v.vram[6], v.vram[7] = 0x00, 0x10, 0x20, 0x30

	buf := make([]uint32, 352*256)
	renderTestNBG(v, 0, buf)
	expectRGB(t, v, buf, 0, 0, 0x30, 0x20, 0x10, "16M bitmap opaque dot")
	expectTransparent(t, v, buf, 1, 0, "16M bitmap MSB-clear dot")

	v.regs[vdp2BGON] |= 1 << 8
	clear(buf)
	renderTestNBG(v, 0, buf)
	expectRGB(t, v, buf, 1, 0, 0x30, 0x20, 0x10, "16M bitmap MSB-clear dot with N0TPON")
}

// TestCRAMMode2RenderedColor verifies rendering through the CRAM cache in
// CRAM mode 2 (RGB888, 4 bytes per entry): the low word holds G in its
// high byte and R in its low byte, the high word holds B in its low byte.
func TestCRAMMode2RenderedColor(t *testing.T) {
	v := setupThreeNBGLayers(t, 5, 0, 0)
	v.regs[vdp2RAMCTL] = 0x2000
	// NBG0 dot 10: entry 10 at byte 40.
	v.cram[40], v.cram[41] = 0x00, 0x40 // high word: B = 0x40
	v.cram[42], v.cram[43] = 0x80, 0xC0 // low word: G = 0x80, R = 0xC0
	renderTestFrame(v)
	expectOut(t, v, 0, 0, 0xC0, 0x80, 0x40, 0, "CRAM mode 2 entry")
}
