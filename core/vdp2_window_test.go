package core

import "testing"

// TestWindowYLSMD3 verifies rectangle window Y bounds in double-density
// interlace are displayed-line values with the least significant bit
// ignored (VDP2 manual Table 8.2): WPEY0=3 covers displayed lines 0-2.
func TestWindowYLSMD3(t *testing.T) {
	v := setupNBG0FullTile(t)
	v.regs[vdp2BKTAU] = 0x0002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000, 0xFC00) // blue back screen
	v.regs[vdp2WCTLA] = 0x0002      // NBG0 W0 enable
	v.regs[vdp2WPSX0] = 0
	v.regs[vdp2WPEX0] = 40
	v.regs[vdp2WPSY0] = 0
	v.regs[vdp2WPEY0] = 3
	setLSMD3(v, true)
	renderTestFrame(v)
	// Field 1 line 0 = displayed 1 (inside, masked), line 1 = displayed 3
	// (outside since bit 0 of WPEY0 is dropped: 3 -> 2).
	expectOut(t, v, 3, 1, 0, 0, 255, 0, "displayed line 1 inside W0")
	expectOut(t, v, 3, 3, 255, 0, 0, 0, "displayed line 3 outside W0")
}

// TestSpriteWindowVariants verifies the sprite window on a scroll layer:
// an 8bpp framebuffer uses pixel bit 7 as the window bit, the sprite
// window area bit inverts the masked side, the W1 area bit inverts W1,
// and AND logic with W0 requires both windows active.
func TestSpriteWindowVariants(t *testing.T) {
	build := func(wctla uint16, is8bpp bool) (*VDP2, vdp1FBView) {
		v := setupNBG0FullTile(t)
		v.regs[vdp2BKTAU] = 0x0002
		v.regs[vdp2BKTAL] = 0xC000
		writeVRAM16(v, 0x58000, 0xFC00)
		v.regs[vdp2WCTLA] = wctla
		var fb vdp1FBView
		if is8bpp {
			data := make([]byte, 1024*256)
			data[0] = 0x80 // x 0: window bit set
			data[1] = 0x01 // x 1: valid, window bit clear
			fb = vdp1FBView{data: data, is8bpp: true, width: 1024, height: 256}
		} else {
			data := make([]byte, 512*256*2)
			data[0], data[1] = 0x80, 0x00 // x 0: window bit set
			data[2], data[3] = 0x00, 0x01 // x 1: valid, window bit clear
			fb = vdp1FBView{data: data, width: 512, height: 256}
		}
		return v, fb
	}

	// 8bpp sprite window, area inside.
	v, fb := build(0x0020, true)
	renderTestFrameFB(v, fb)
	expectOut(t, v, 0, 0, 0, 0, 255, 0, "8bpp window bit set: masked")
	expectOut(t, v, 1, 0, 255, 0, 0, 0, "8bpp window bit clear: shown")

	// Sprite window area bit: masked where the window bit is clear.
	v, fb = build(0x0030, false)
	renderTestFrameFB(v, fb)
	expectOut(t, v, 0, 0, 255, 0, 0, 0, "inverted sprite window: bit set shown")
	expectOut(t, v, 1, 0, 0, 0, 255, 0, "inverted sprite window: bit clear masked")
	expectOut(t, v, 2, 0, 0, 0, 255, 0, "inverted sprite window: no sprite masked")

	// W1 rectangle with the area bit: masked outside x 0..4.
	v, fb = build(0x000C, false)
	v.regs[vdp2WPSX1] = 0
	v.regs[vdp2WPEX1] = 8
	v.regs[vdp2WPSY1] = 0
	v.regs[vdp2WPEY1] = 10
	renderTestFrameFB(v, fb)
	expectOut(t, v, 2, 0, 255, 0, 0, 0, "W1 inverted: inside shown")
	expectOut(t, v, 6, 0, 0, 0, 255, 0, "W1 inverted: outside masked")

	// AND logic: W0 (x 0..4) and sprite window both enabled; only x 0
	// (inside W0 with the sprite window bit) is masked.
	v, fb = build(0x0002|0x0020|0x0080, false)
	v.regs[vdp2WPSX0] = 0
	v.regs[vdp2WPEX0] = 8
	v.regs[vdp2WPSY0] = 0
	v.regs[vdp2WPEY0] = 10
	renderTestFrameFB(v, fb)
	expectOut(t, v, 0, 0, 0, 0, 255, 0, "AND: in W0 with sprite bit: masked")
	expectOut(t, v, 1, 0, 255, 0, 0, 0, "AND: in W0 without sprite bit: shown")
	expectOut(t, v, 6, 0, 255, 0, 0, 0, "AND: outside W0: shown")
}
