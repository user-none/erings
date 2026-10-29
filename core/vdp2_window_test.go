package core

import "testing"

// TestWindowYLSMD3 verifies rectangle window Y bounds in double-density
// interlace are displayed-line values with the least significant bit
// ignored (VDP2 manual Table 8.2): WPEY0=3 covers displayed lines 0-2.
func TestWindowYLSMD3(t *testing.T) {
	v := setupNBG0FullTile(t)
	swr := newSoftwareRenderer(v)
	v.regs[vdp2BKTAU] = 0x0002
	v.regs[vdp2BKTAL] = 0xC000
	writeVRAM16(v, 0x58000, 0xFC00) // blue back screen
	v.regs[vdp2WCTLA] = 0x0002      // NBG0 W0 enable
	v.regs[vdp2WPSX0] = 0
	v.regs[vdp2WPEX0] = 40
	v.regs[vdp2WPSY0] = 0
	v.regs[vdp2WPEY0] = 3
	setLSMD3(v, true)
	swr.beginFrame() // the frame start where the geometry changed
	renderTestFrame(v, swr)
	// Field 1 line 0 = displayed 1 (inside, masked), line 1 = displayed 3
	// (outside since bit 0 of WPEY0 is dropped: 3 -> 2).
	expectOut(t, v, swr, 3, 1, 0, 0, 248, 0, "displayed line 1 inside W0")
	expectOut(t, v, swr, 3, 3, 248, 0, 0, 0, "displayed line 3 outside W0")
}

// TestSpriteWindowVariants verifies the sprite window on a scroll layer
// with SPWINEN set: an 8bpp framebuffer uses pixel bit 7 as the window bit, the sprite
// window area bit inverts the masked side, the W1 area bit inverts W1,
// and AND logic with W0 requires both windows active.
func TestSpriteWindowVariants(t *testing.T) {
	build := func(wctla uint16, is8bpp bool) (*VDP2, vdp1FBView) {
		v := setupNBG0FullTile(t)
		v.regs[vdp2BKTAU] = 0x0002
		v.regs[vdp2BKTAL] = 0xC000
		writeVRAM16(v, 0x58000, 0xFC00)
		v.regs[vdp2WCTLA] = wctla
		v.regs[vdp2SPCTL] = 0x0012 // SPWINEN, type 2
		var fb vdp1FBView
		if is8bpp {
			v.regs[vdp2SPCTL] = 0x0018 // SPWINEN, type 8
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
	swr := newSoftwareRenderer(v)
	renderTestFrameFB(v, swr, fb)
	expectOut(t, v, swr, 0, 0, 0, 0, 248, 0, "8bpp window bit set: masked")
	expectOut(t, v, swr, 1, 0, 248, 0, 0, 0, "8bpp window bit clear: shown")

	// Sprite window area bit: masked where the window bit is clear.
	v, fb = build(0x0030, false)
	swr = newSoftwareRenderer(v)
	renderTestFrameFB(v, swr, fb)
	expectOut(t, v, swr, 0, 0, 248, 0, 0, 0, "inverted sprite window: bit set shown")
	expectOut(t, v, swr, 1, 0, 0, 0, 248, 0, "inverted sprite window: bit clear masked")
	expectOut(t, v, swr, 2, 0, 0, 0, 248, 0, "inverted sprite window: no sprite masked")

	// W1 rectangle with the area bit: masked outside x 0..4.
	v, fb = build(0x000C, false)
	swr = newSoftwareRenderer(v)
	v.regs[vdp2WPSX1] = 0
	v.regs[vdp2WPEX1] = 8
	v.regs[vdp2WPSY1] = 0
	v.regs[vdp2WPEY1] = 10
	renderTestFrameFB(v, swr, fb)
	expectOut(t, v, swr, 2, 0, 248, 0, 0, 0, "W1 inverted: inside shown")
	expectOut(t, v, swr, 6, 0, 0, 0, 248, 0, "W1 inverted: outside masked")

	// AND logic: W0 (x 0..4) and sprite window both enabled; only x 0
	// (inside W0 with the sprite window bit) is masked.
	v, fb = build(0x0002|0x0020|0x0080, false)
	swr = newSoftwareRenderer(v)
	v.regs[vdp2WPSX0] = 0
	v.regs[vdp2WPEX0] = 8
	v.regs[vdp2WPSY0] = 0
	v.regs[vdp2WPEY0] = 10
	renderTestFrameFB(v, swr, fb)
	expectOut(t, v, swr, 0, 0, 0, 0, 248, 0, "AND: in W0 with sprite bit: masked")
	expectOut(t, v, swr, 1, 0, 248, 0, 0, 0, "AND: in W0 without sprite bit: shown")
	expectOut(t, v, swr, 6, 0, 248, 0, 0, 0, "AND: outside W0: shown")
}

// TestBuildLineWindowsRuns verifies the row split buildLineWindows
// produces: run boundaries at every W0/W1 edge inside the row, the
// inside states per run, and the off cases (row outside the vertical
// range, start past end from the registers or a line table entry,
// edges outside the row from a signed line table entry, and windows no
// layer enables).
func TestBuildLineWindowsRuns(t *testing.T) {
	type run struct {
		x0, x1 int
		w0, w1 bool
	}
	cases := []struct {
		name string
		set  func(v *VDP2)
		y    int
		want []run
	}{
		// At reset both windows are one-dot windows at (0,0), so the
		// cases with no other window use a row past 0.
		{"no windows", func(v *VDP2) {}, 5, []run{{0, 320, false, false}}},
		{"W0 not enabled by any layer", func(v *VDP2) {
			v.Write(vdp2WCTLA*2, 0x0000)
			v.Write(vdp2WPSX0*2, 80)
			v.Write(vdp2WPEX0*2, 398)
			v.Write(vdp2WPEY0*2, 223)
		}, 5, []run{{0, 320, false, false}}},
		{"W0 rectangle", func(v *VDP2) {
			v.Write(vdp2WPSX0*2, 80)
			v.Write(vdp2WPEX0*2, 398)
			v.Write(vdp2WPSY0*2, 20)
			v.Write(vdp2WPEY0*2, 180)
		}, 50, []run{{0, 40, false, false}, {40, 200, true, false}, {200, 320, false, false}}},
		{"W0 row above range", func(v *VDP2) {
			v.Write(vdp2WPSX0*2, 80)
			v.Write(vdp2WPEX0*2, 398)
			v.Write(vdp2WPSY0*2, 20)
			v.Write(vdp2WPEY0*2, 180)
		}, 19, []run{{0, 320, false, false}}},
		{"W0 start past end", func(v *VDP2) {
			v.Write(vdp2WPSX0*2, 400)
			v.Write(vdp2WPEX0*2, 80)
			v.Write(vdp2WPEY0*2, 223)
		}, 5, []run{{0, 320, false, false}}},
		{"W0 and W1 overlapping", func(v *VDP2) {
			v.Write(vdp2WPSX0*2, 80)
			v.Write(vdp2WPEX0*2, 398)
			v.Write(vdp2WPEY0*2, 223)
			v.Write(vdp2WPSX1*2, 200)
			v.Write(vdp2WPEX1*2, 600)
			v.Write(vdp2WPEY1*2, 223)
		}, 0, []run{{0, 40, false, false}, {40, 100, true, false}, {100, 200, true, true}, {200, 301, false, true}, {301, 320, false, false}}},
		{"W1 line table covering the row from a negative start", func(v *VDP2) {
			v.Write(vdp2WPEY1*2, 223)
			v.Write(vdp2LWTA1U*2, 0x8000)
			v.Write(vdp2LWTA1L*2, 0xE800)
			v.WriteVRAM16(0x1D000+4*7, 0xFFFE)
			v.WriteVRAM16(0x1D000+4*7+2, 0x02C0)
		}, 7, []run{{0, 320, false, true}}},
		{"W1 line table excluding the row", func(v *VDP2) {
			v.Write(vdp2WPEY1*2, 223)
			v.Write(vdp2LWTA1U*2, 0x8000)
			v.Write(vdp2LWTA1L*2, 0xE800)
			v.WriteVRAM16(0x1D000+4*7, 0x02C2)
			v.WriteVRAM16(0x1D000+4*7+2, 0xFFFF)
		}, 7, []run{{0, 320, false, false}}},
		{"W0 edge on the last pixel", func(v *VDP2) {
			v.Write(vdp2WPSX0*2, 638)
			v.Write(vdp2WPEX0*2, 638)
			v.Write(vdp2WPEY0*2, 223)
		}, 5, []run{{0, 319, false, false}, {319, 320, true, false}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVDP2()
			// Windows are resolved only when some layer enables them:
			// NBG0 takes W0 and W1 here.
			v.Write(vdp2WCTLA*2, 0x000A)
			tc.set(v)
			v.BeginFrame()
			lw := &v.lineWin
			v.buildLineWindows(lw, tc.y, v.frame.field)
			if int(lw.runs.N) != len(tc.want) {
				t.Fatalf("%d runs, want %d: %v", lw.runs.N, len(tc.want), lw.runs.Runs[:lw.runs.N])
			}
			for i, w := range tc.want {
				got := lw.runs.Runs[i]
				if int(got.X0) != w.x0 || int(got.X1) != w.x1 || got.W0 != w.w0 || got.W1 != w.w1 {
					t.Errorf("run %d = {%d %d %v %v}, want {%d %d %v %v}", i, got.X0, got.X1, got.W0, got.W1, w.x0, w.x1, w.w0, w.w1)
				}
			}
		})
	}
}
