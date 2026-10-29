// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
)

// enhancedVDP1Scene is a VDP1 frame scene, and whether its pair at
// scale is the walk's pair expanded to blocks: a scene of erases and
// normal sprites without Gouraud shading. A Gouraud gradient
// interpolates over the scaled sprite, a scaled sprite resamples its
// character at the scaled destination, and the other drawing
// commands' geometry refines.
type enhancedVDP1Scene struct {
	benchVDP1Scene
	blocks bool
}

// enhancedSpriteScene is a scene of a grid of 256-color normal
// sprites made by f, with setup run on the VDP1 first.
func enhancedSpriteScene(name string, setup func(v *VDP1, l *benchList), f func(x, y int16) benchCmd) benchVDP1Scene {
	return benchVDP1Scene{name: name, build: func() *VDP1 {
		v, l := benchVDP1()
		if setup != nil {
			setup(v, l)
		}
		benchGrid(l, 12, 7, 26, 30, f)
		l.end()
		return v
	}}
}

// enhancedVDP1Scenes are the frame scenes, every kind of entry among
// them, and normal
// sprites over the pixel write's options: both flips, mesh, user clip
// modes 2 and 3, both parities under double interlace, and an 8-bit
// frame buffer; partial erases in a 16-bit and an 8-bit frame buffer;
// and a half-transparent normal sprite over sprites, which the walk
// draws across many ticks, so its entries split it and a dot drawn
// twice would blend twice.
func enhancedVDP1Scenes(t *testing.T) []enhancedVDP1Scene {
	t.Helper()
	normal8 := benchNormal8
	flipped := func(x, y int16) benchCmd {
		c := benchNormal8(x, y)
		c.ctrl = uint16(x/26%4) << 4
		return c
	}
	withPmod := func(bits uint16) func(x, y int16) benchCmd {
		return func(x, y int16) benchCmd {
			c := benchNormal8(x, y)
			c.pmod |= bits
			return c
		}
	}
	userClip := func(v *VDP1, l *benchList) {
		l.add(benchCmd{ctrl: 0x0008, xa: 40, ya: 30, xc: 250, yc: 180})
	}
	fbcr := func(val uint16) func(v *VDP1, l *benchList) {
		return func(v *VDP1, l *benchList) { v.Write(0x02, val) }
	}
	hiRes8 := func(v *VDP1, l *benchList) { v.Write(0x00, 0x0001) }
	extra := []benchVDP1Scene{
		enhancedSpriteScene("normal sprites flipped", nil, flipped),
		enhancedSpriteScene("normal sprites mesh", nil, withPmod(0x0100)),
		enhancedSpriteScene("normal sprites inside the user clip", userClip, withPmod(0x0400)),
		enhancedSpriteScene("normal sprites outside the user clip", userClip, withPmod(0x0600)),
		enhancedSpriteScene("normal sprites double interlace even", fbcr(0x0008), normal8),
		enhancedSpriteScene("normal sprites double interlace odd", fbcr(0x000C), normal8),
		enhancedSpriteScene("normal sprites 8-bit", hiRes8, flipped),
		eraseArea(enhancedSpriteScene("normal sprites with a partial erase", nil, normal8), 4, 30, 30, 180),
		eraseArea(enhancedSpriteScene("normal sprites 8-bit with a partial erase", hiRes8, normal8), 1, 20, 17, 200),
		manualDraw(benchVDP1Scene{name: "half-transparent normal sprite over sprites", build: func() *VDP1 {
			v, l := benchVDP1()
			benchGrid(l, 10, 7, 32, 32, func(x, y int16) benchCmd {
				return benchCmd{pmod: 5 << 3, srca: benchTexRGB / 8, size: benchSize32, xa: x, ya: y}
			})
			l.add(benchCmd{pmod: 5<<3 | 3, srca: benchTexRGB / 8, size: 63<<8 | 200, xa: 4, ya: 8})
			l.end()
			return v
		}}),
	}
	var out []enhancedVDP1Scene
	for _, s := range append(vdp1FrameScenes(), extra...) {
		probe := s.build()
		benchVDP1Change(probe)
		blocks, entries, seen, die := true, 0, 0, 0
		for line := 0; line < vdp1DriveScanlines; line++ {
			probe.TickSystemCycles(benchVDP1WalkerTick)
			for i := seen; i < len(probe.ops); i++ {
				op := &probe.ops[i]
				die += int(op.Pixel.DIE)
				if op.Kind != draw.OpErase && op.Kind != draw.OpNormalSprite || op.Pixel.CC >= 4 {
					blocks = false
				}
				if op.Pixel.CC == 3 {
					entries++
				}
			}
			seen = len(probe.ops)
		}
		if seen == 0 {
			t.Fatalf("%s: no entry", s.name)
		}
		if s.name == "half-transparent normal sprite over sprites" && entries < 2 {
			t.Fatalf("%s: the half-transparent sprite is in %d entries, want it split", s.name, entries)
		}
		if strings.Contains(s.name, "double interlace") && die == 0 {
			t.Fatalf("%s: no entry draws under double interlace", s.name)
		}
		out = append(out, enhancedVDP1Scene{benchVDP1Scene: s, blocks: blocks})
	}
	return out
}

// spriteVDP2 is a VDP2 with a 16-bit sprite layer and no scroll
// screen, every CRAM entry a color, since the sprite pixels' dot
// colors index the whole of it.
func spriteVDP2() *VDP2 {
	v := benchVDP2()
	benchSpriteRegs(v)
	for i := uint32(256); i < 1024; i++ {
		benchCRAM(v, i, uint16((i*0x0A3B+0x0421)&0x7FFF))
	}
	return v
}

// enhancedVDP1Drive drives the scene's frames through an enhanced
// renderer at the shift over a spriteVDP2, then the next frame's
// change and its take, so the pair holds every entry and CPU write
// the walk's pair does.
func enhancedVDP1Drive(t *testing.T, s benchVDP1Scene, shift uint32) (*enhancedRenderer, *VDP1) {
	t.Helper()
	v1 := s.build()
	v2 := spriteVDP2()
	v2.setEnhance(enhanceConfig{shift: shift})
	e := newEnhancedRenderer(v2, v1)
	d := &vdp1FrameDrive{v1: v1, v2: v2, g: e}
	d.frames(t)
	d.change()
	e.vdp1Line(1)
	return e, v1
}

// expandFB is a frame buffer's bytes at the scale shift with every
// native pixel an N by N block: each byte to the same lane of every
// pixel of its pixel's block.
func expandFB(native []byte, width int, is8bpp bool, shift uint32) []byte {
	n, size := 1<<shift, 2
	if is8bpp {
		size = 1
	}
	out := make([]byte, len(native)<<(2*shift))
	for a, b := range native {
		pixel, lane := a/size, a%size
		x, y := pixel%width, pixel/width
		for dy := range n {
			for dx := range n {
				out[((y*n+dy)*width*n+x*n+dx)*size+lane] = b
			}
		}
	}
	return out
}

// TestEnhancedVDP1Scale1MatchesWalk drives every enhanced VDP1 scene
// through the enhanced renderer at scale 1 and holds both halves of
// its pair equal to the walk's CPU pair.
func TestEnhancedVDP1Scale1MatchesWalk(t *testing.T) {
	for _, s := range enhancedVDP1Scenes(t) {
		t.Run(s.name, func(t *testing.T) {
			e, v1 := enhancedVDP1Drive(t, s.benchVDP1Scene, 0)
			for i := uint32(0); i < 2; i++ {
				if got, want := []byte(e.vdp1FB[i]), v1.physicalFB(i); !bytes.Equal(got, want) {
					t.Fatalf("physical buffer %d differs from the walk at byte %d", i, firstDiff(got, want))
				}
			}
		})
	}
}

// TestEnhancedVDP1BlocksMatchWalk drives the enhanced VDP1 scenes
// without Gouraud shading through the enhanced renderer at scale
// shifts 1 and 2 and holds both halves of its pair equal to the
// walk's CPU pair expanded to blocks: every normal sprite dot, erased
// dot, and CPU write lands on every byte of its block and nowhere
// else, a split sprite's dots once each.
func TestEnhancedVDP1BlocksMatchWalk(t *testing.T) {
	for _, s := range enhancedVDP1Scenes(t) {
		if !s.blocks {
			continue
		}
		for shift := uint32(1); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				e, v1 := enhancedVDP1Drive(t, s.benchVDP1Scene, shift)
				for i := uint32(0); i < 2; i++ {
					want := expandFB(v1.physicalFB(i), v1.fbWidth(), v1.is8bpp(), shift)
					if got := []byte(e.vdp1FB[i]); !bytes.Equal(got, want) {
						t.Fatalf("physical buffer %d differs from the walk's expanded to blocks at byte %d", i, firstDiff(got, want))
					}
				}
			})
		}
	}
}

// TestEnhancedVDP1CompositeBlocks drives the enhanced VDP1 scenes
// without Gouraud shading through the accurate software renderer and
// through the enhanced renderer, each over its own spriteVDP2, and
// holds the enhanced frame, the composite's direct read of its pair,
// equal to the software frame at scale 1 and to the software frame
// expanded to blocks at scale shifts 1 and 2. A scene drawn in the
// manual change mode never swaps its draw in, so its frames are the
// empty display on both; the other scenes composite sprite pixels.
func TestEnhancedVDP1CompositeBlocks(t *testing.T) {
	checked, litScenes := 0, 0
	for _, s := range enhancedVDP1Scenes(t) {
		if !s.blocks {
			continue
		}
		for shift := uint32(0); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				sw1, sw2 := s.build(), spriteVDP2()
				software := newSoftwareRenderer(sw2)
				(&vdp1FrameDrive{v1: sw1, v2: sw2, g: software}).frames(t)
				e, _ := enhancedVDP1Drive(t, s.benchVDP1Scene, shift)
				n := 1 << shift
				native, stride, height := software.framebuffer(), software.framebufferStride(), software.displayHeight()
				scaled, scaledStride := e.framebuffer(), e.framebufferStride()
				if scaledStride != stride*n || e.displayHeight() != height*n {
					t.Fatalf("stride %d height %d, want %d and %d", scaledStride, e.displayHeight(), stride*n, height*n)
				}
				lit := 0
				for y := range height * n {
					for x := 0; x < scaledStride/4; x++ {
						want := native[(y/n)*stride+(x/n)*4:][:4]
						got := scaled[y*scaledStride+x*4:][:4]
						if !bytes.Equal(got, want) {
							t.Fatalf("pixel (%d, %d) = % x, want the native % x", x, y, got, want)
						}
						if want[0]|want[1]|want[2] != 0 {
							lit++
						}
					}
				}
				if lit != 0 {
					litScenes++
				}
			})
			checked++
		}
	}
	if checked == 0 || litScenes == 0 {
		t.Errorf("%d scenes checked, %d with sprite pixels composited", checked, litScenes)
	}
}

// TestEnhancedVDP1StateLoadMatchesWalk saves an emulator at a frame
// boundary with half-transparent normal sprites in progress, in the
// manual change mode so the draw spans frames, drives another frame,
// restores the state into the same emulator with the enhanced
// renderer at scale shift 1 installed, as a rewind does, drives the
// rest of the draw, and holds both halves of the pair equal to the
// CPU pair expanded to blocks: the load marks both CPU buffers
// written, so the next take replaces the frame the rewind undid with
// the restored buffers expanded whole, and the restored command's
// entries follow.
func TestEnhancedVDP1StateLoadMatchesWalk(t *testing.T) {
	e := NewEmulator()
	v1 := e.vdp1
	stateLoadList(v1)
	const shift = 1
	e.renderer.close()
	e.vdp2.setEnhance(enhanceConfig{shift: shift})
	r := newEnhancedRenderer(e.vdp2, v1)
	defer r.close()
	e.renderer = r
	d := &vdp1FrameDrive{v1: v1, v2: e.vdp2, g: r}
	driveStateLoad(t, e, d)
	d.change()
	r.vdp1Line(1)
	for i := uint32(0); i < 2; i++ {
		want := expandFB(v1.physicalFB(i), v1.fbWidth(), v1.is8bpp(), shift)
		if got := []byte(r.vdp1FB[i]); !bytes.Equal(got, want) {
			t.Fatalf("physical buffer %d differs from the walk's expanded to blocks at byte %d", i, firstDiff(got, want))
		}
	}
}

// TestEnhancedVDP1ResetDropsWaitingEntry drives a frame whose draw
// of distorted sprites crosses the frame boundary with the enhanced
// renderer at scale shift 1, so that the entry closed at the last
// tick waits for the record of the line after its last, then resets
// the VDP1 as a system reset does. The reset ends the draw, so the
// list restarts with nothing taken and the waiting entry is never
// drawn: the pair after the next take equals the pair before the
// reset.
func TestEnhancedVDP1ResetDropsWaitingEntry(t *testing.T) {
	e := NewEmulator()
	v1 := e.vdp1
	resetList(v1)
	const shift = 1
	e.renderer.close()
	e.vdp2.setEnhance(enhanceConfig{shift: shift})
	r := newEnhancedRenderer(e.vdp2, v1)
	defer r.close()
	e.renderer = r
	d := &vdp1FrameDrive{v1: v1, v2: e.vdp2, g: r}
	d.frame(0)
	if !v1.drawActive || v1.cmdPhase != phaseDraw {
		t.Fatal("the draw is not in progress at the frame boundary")
	}
	if v1.opsTaken >= len(v1.ops) {
		t.Fatalf("no entry waiting at the frame boundary: %d taken of %d", v1.opsTaken, len(v1.ops))
	}
	var before [2][]byte
	for i := range before {
		before[i] = bytes.Clone(r.vdp1FB[i])
	}
	v1.Reset()
	if len(v1.ops) != 0 || v1.opsTaken != 0 {
		t.Fatalf("after the reset: %d entries, %d taken", len(v1.ops), v1.opsTaken)
	}
	d.change()
	r.vdp1Line(1)
	for i := range before {
		if got := []byte(r.vdp1FB[i]); !bytes.Equal(got, before[i]) {
			t.Fatalf("physical buffer %d changed after the reset at byte %d", i, firstDiff(got, before[i]))
		}
	}
}

// resetList writes the reset test's list into v1: in the manual
// change mode, opaque distorted sprites as large as a sprite can be,
// a draw longer than a frame.
func resetList(v1 *VDP1) {
	v1.Write(0x00, 0x0000)
	v1.Write(0x02, 0x0002)
	v1.Write(0x04, 0x0001)
	const bigTex = 0x20000
	for i := uint32(0); i < 504*200; i++ {
		v1.WriteVRAM16(bigTex+i*2, uint16(0x8000|(i*0x0A3B)&0x7FFF))
	}
	l := &benchList{v: v1}
	l.add(benchCmd{ctrl: 0x0009, xc: 319, yc: 223})
	l.add(benchCmd{ctrl: 0x000A})
	big := benchCmd{ctrl: 0x0002, pmod: 5 << 3, srca: bigTex / 8, size: 63<<8 | 200, xb: 300, yb: 12, xc: 296, yc: 210, xd: 8, yd: 206}
	for i := range int16(12) {
		big.xa, big.ya = 4+i*3, 8+i*2
		l.add(big)
	}
	l.end()
}

// stateLoadList writes the state load tests' list into v1: in the
// manual change mode, opaque normal sprites as large as a sprite can
// be, then half-transparent ones over them, a draw longer than a
// frame.
func stateLoadList(v1 *VDP1) {
	v1.Write(0x00, 0x0000)
	v1.Write(0x02, 0x0002)
	v1.Write(0x04, 0x0001)
	// An opaque RGB character as large as a sprite can be, so every
	// dot of every sprite draws.
	const bigTex = 0x20000
	for i := uint32(0); i < 504*200; i++ {
		v1.WriteVRAM16(bigTex+i*2, uint16(0x8000|(i*0x0A3B)&0x7FFF))
	}
	l := &benchList{v: v1}
	l.add(benchCmd{ctrl: 0x0009, xc: 319, yc: 223})
	l.add(benchCmd{ctrl: 0x000A})
	big := benchCmd{pmod: 5 << 3, srca: bigTex / 8, size: 63<<8 | 200, xa: 4, ya: 8}
	l.add(big)
	big.pmod |= 3
	for i := range int16(8) {
		big.xa, big.ya = 4+i*9, 8+i*3
		l.add(big)
	}
	l.end()
}

// driveStateLoad drives the first frame of the drive, whose renderer
// is e's, saves e with the draw in progress, drives another frame,
// restores the state, and drives the rest of the draw.
func driveStateLoad(t *testing.T, e *Emulator, d *vdp1FrameDrive) {
	t.Helper()
	v1 := d.v1
	d.frame(0)
	if !v1.drawActive || v1.cmdPhase != phaseDraw {
		t.Fatal("the draw is not in progress at the frame boundary")
	}
	state, err := e.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	d.frame(1)
	if err := e.Deserialize(state); err != nil {
		t.Fatal(err)
	}
	for frame := 1; v1.drawActive; frame++ {
		if frame > 40 {
			t.Fatal("the draw did not complete in 40 frames")
		}
		d.frame(frame)
	}
}

// TestScaleSpriteRowSubRows holds the composite's scaled sprite read
// to the pair's scaled rows at scale shifts 1 to 3, over a pair whose
// every scaled pixel is distinct: sub-row k of row y reads column x'
// of pair row N*y+k in a direct read (x' halved in hi-res), in a
// 16-bit and an 8-bit buffer, and in a rotated read the pair pixel at
// the native read's coordinate at line y+k/N and column x'/N, times
// N: the start, its per-line advance, and the per-column step all at
// sub-dot precision.
func TestScaleSpriteRowSubRows(t *testing.T) {
	v := NewVDP2(NewSCU())
	rot := &v.frame.sprRotA
	rot.dxst, rot.dyst = 3<<8, 1<<10+37
	const xst, yst, dx, dy = 5 << 10, 7 << 10, 1<<10 - 50, 90
	pixel := func(x, y int) uint16 { return uint16(y*1031 + x*17) }
	for shift := uint32(1); shift <= 3; shift++ {
		n := 1 << shift
		v.setEnhance(enhanceConfig{shift: shift})
		for _, tc := range []struct {
			name   string
			mode   uint32
			width  int
			is8bpp bool
			halve  uint32
		}{
			{"16-bit", screen.SpriteRead16, 512, false, 0},
			{"16-bit hi-res", screen.SpriteRead16, 512, false, 1},
			{"8-bit", screen.SpriteRead8, 1024, true, 0},
			{"rotated", screen.SpriteReadRotated, 512, false, 0},
		} {
			height, size := 256, 2
			if tc.is8bpp {
				size = 1
			}
			sw, sh := tc.width*n, height*n
			fb := make(compute.VDP1FB, compute.VDP1FBSize<<(2*shift))
			for y := range sh {
				for x := range sw {
					if tc.is8bpp {
						fb[y*sw+x] = uint8(pixel(x, y))
					} else {
						fb[(y*sw+x)*2] = uint8(pixel(x, y) >> 8)
						fb[(y*sw+x)*2+1] = uint8(pixel(x, y))
					}
				}
			}
			for _, y := range []int{0, 1, 37, 110, 223} {
				native := screen.SpriteRow{Mode: tc.mode, Width: int32(tc.width), Height: int32(height), Is8bpp: tc.is8bpp,
					Row: uint32(y * tc.width * size), Halve: tc.halve,
					BaseX: xst + rot.dxst*int64(y), BaseY: yst + rot.dyst*int64(y), DX: dx, DY: dy}
				for k := range n {
					r := native
					v.scaleSpriteRow(&r, int32(k))
					for _, x := range []int{0, 1, n - 1, n, 3*n + 1, 150 * n, sw/(1<<tc.halve) - 1} {
						var got, want uint32
						switch tc.mode {
						case screen.SpriteRead16:
							got, want = screen.SpritePixel16(fb, &r, int32(x)), uint32(pixel(x>>tc.halve, y*n+k))
						case screen.SpriteRead8:
							got, want = screen.SpritePixel8(fb, &r, int32(x)), uint32(uint8(pixel(x, y*n+k)))
						case screen.SpriteReadRotated:
							sx := (int64(n)*xst + rot.dxst*int64(y*n+k) + dx*int64(x)) >> 10
							sy := (int64(n)*yst + rot.dyst*int64(y*n+k) + dy*int64(x)) >> 10
							got = screen.SpritePixelRotated(fb, &r, int32(x))
							if sx >= 0 && sx < int64(sw) && sy >= 0 && sy < int64(sh) {
								want = uint32(pixel(int(sx), int(sy)))
							}
						}
						if got != want {
							t.Fatalf("%s shift %d row %d sub-row %d column %d: read %04x, want %04x", tc.name, shift, y, k, x, got, want)
						}
					}
				}
			}
		}
	}
}

// walkRecords draws op natively through the walk's loop into fb and
// returns the line records it stored at base 0.
func walkRecords(vram *compute.VDP1VRAM, fb compute.VDP1FB, op *draw.DrawOp) compute.LineRecords {
	rec := make(compute.LineRecords, 256)
	st := draw.DrawState{PixCycles: 1}
	draw.Draw(&draw.Buffers{VRAM: vram, FB: fb, Records: rec}, op, &st)
	return rec
}

// fillFB fills a 16-bit frame buffer with one pixel value.
func fillFB(fb compute.VDP1FB, px uint16) {
	for i := 0; i < len(fb); i += 2 {
		fb[i], fb[i+1] = uint8(px>>8), uint8(px)
	}
}

// TestScaleOpSpriteEntriesBlocks splits a half-transparent normal
// or scaled sprite, enlarging, shrinking, or shrinking at high speed,
// unflipped and flipped both ways, into two entries at rows and
// columns across it, as the walk's list does at a tick, and draws the
// entries at scale shifts 1 to 3 over a background they blend with.
// Every dot of the character draws (transparent dots drawn, no end
// codes, the sprite inside the clips) and changes the black
// background (every texel's red is 16), so the first entry at scale
// changes exactly the blocks of the dots the native first entry
// changes, and the two entries together equal the whole op drawn
// once: each sub-dot is drawn by the entry that drew the native dot
// whose block holds it, and once.
func TestScaleOpSpriteEntriesBlocks(t *testing.T) {
	vram := new(compute.VDP1VRAM)
	const charW, charH = 32, 16
	for r := range charH {
		for c := range charW {
			px := uint16(0x8000 | (c*37+r*101)<<5&0x7FE0 | 0x10)
			vram[(r*charW+c)*2], vram[(r*charW+c)*2+1] = uint8(px>>8), uint8(px)
		}
	}
	const fbW, fbH, bg = 128, 64, 0x8000
	forms := []struct {
		name   string
		kind   uint32
		x1, y1 int32
		flags  uint32
	}{
		{"normal", draw.OpNormalSprite, 0, 0, 0},
		{"enlarge", draw.OpScaledSprite, 10 + 79, 6 + 39, 0},
		{"shrink", draw.OpScaledSprite, 10 + 11, 6 + 9, 0},
		{"shrink HSS", draw.OpScaledSprite, 10 + 11, 6 + 9, draw.OpFlagHSS},
	}
	for _, f := range forms {
		for _, flip := range []uint32{0, draw.OpFlagFlipH, draw.OpFlagFlipV, draw.OpFlagFlipH | draw.OpFlagFlipV} {
			op := draw.DrawOp{Kind: f.kind, CharW: charW, CharH: charH, ColorMode: 5,
				Flags: draw.OpFlagSPDOn | draw.OpFlagECDOff | f.flags | flip}
			op.X[0], op.Y[0], op.X[2], op.Y[2] = 10, 6, f.x1, f.y1
			op.Pixel = draw.PixelParams{CC: 3, ClipX: fbW - 1, ClipY: fbH - 1, FBWidth: fbW, FBHeight: fbH}
			native := make(compute.VDP1FB, fbW*fbH*2)
			fillFB(native, bg)
			rec := walkRecords(vram, native, &op)
			lines, width := uint32(draw.OpLines(&op)), uint32(draw.SpriteRowWidth(&op))
			for _, l := range []uint32{0, 1, lines / 2, lines - 1} {
				for _, s := range []uint32{0, 1, width / 2, width - 1} {
					if l == 0 && s == 0 {
						continue
					}
					first, second := op, op
					first.LineTo = l
					if s != 0 {
						first.LineTo, first.LineEnd = l+1, s
					}
					second.LineFrom, second.LineStart = l, s
					fillFB(native, bg)
					draw.Generate(&draw.Buffers{VRAM: vram, FB: native, Records: rec}, &first, 0, 1)
					for shift := uint32(1); shift <= 3; shift++ {
						n := 1 << shift
						sw := fbW * n
						pieces := make(compute.VDP1FB, len(native)<<(2*shift))
						whole := make(compute.VDP1FB, len(pieces))
						fillFB(pieces, bg)
						fillFB(whole, bg)
						var scaled draw.DrawOp
						b := draw.Buffers{VRAM: vram, FB: pieces, Records: rec}
						scaleOp(&scaled, &first, shift)
						draw.Generate(&b, &scaled, 0, 1)
						for y := range fbH * n {
							for x := range sw {
								got := pieces[(y*sw+x)*2] != bg>>8 || pieces[(y*sw+x)*2+1] != 0
								want := native[((y/n)*fbW+x/n)*2] != bg>>8 || native[((y/n)*fbW+x/n)*2+1] != 0
								if got != want {
									t.Fatalf("%s flip %d split %d.%d shift %d: sub-dot (%d, %d) changed %v by the first entry, its native dot %v", f.name, flip, l, s, shift, x, y, got, want)
								}
							}
						}
						scaleOp(&scaled, &second, shift)
						draw.Generate(&b, &scaled, 0, 1)
						scaleOp(&scaled, &op, shift)
						b.FB = whole
						draw.Generate(&b, &scaled, 0, 1)
						if !bytes.Equal(pieces, whole) {
							t.Fatalf("%s flip %d split %d.%d shift %d: the entries differ from the whole op at byte %d", f.name, flip, l, s, shift, firstDiff(pieces, whole))
						}
					}
				}
			}
		}
	}
}

// TestScaleOpBlockBoundEntriesDrawOnce splits a distorted sprite, a
// polygon, a polyline, a line, and a polygon whose side edges are
// points into two entries at native lines and steps across them, as
// the walk's list does at a tick, and draws the entries and the whole
// op at scale shifts 1 to 3, half-transparent in one color over a
// background they blend with. The entries keep their native bounds,
// and each sub-dot draws in the entry that holds the native step
// whose block holds it, so the two entries equal the whole op: a
// sub-dot drawn by neither or by both changes the blend, whatever the
// order.
func TestScaleOpBlockBoundEntriesDrawOnce(t *testing.T) {
	const fbW, fbH, bg, color = 128, 64, 0x8000, 0x8000 | 20<<10 | 12<<5 | 18
	vram := new(compute.VDP1VRAM)
	for i := 0; i < 16*8; i++ {
		vram[i*2], vram[i*2+1] = uint8(color>>8), uint8(color&0xFF)
	}
	quad := [2][4]int32{{10, 70, 60, 8}, {6, 10, 50, 40}}
	forms := []struct {
		name string
		kind uint32
		x, y [4]int32
	}{
		{"distorted sprite", draw.OpDistortedSprite, quad[0], quad[1]},
		{"polygon", draw.OpPolygon, quad[0], quad[1]},
		{"polyline", draw.OpPolyline, quad[0], quad[1]},
		{"line", draw.OpLine, [4]int32{5, 100, 0, 0}, [4]int32{5, 45, 0, 0}},
		{"point-sided polygon", draw.OpPolygon, [4]int32{10, 80, 80, 10}, [4]int32{10, 30, 30, 10}},
	}
	checked := 0
	for _, f := range forms {
		op := draw.DrawOp{Kind: f.kind, CharW: 16, CharH: 8, ColorMode: 5, Color: color, X: f.x, Y: f.y}
		op.Pixel = draw.PixelParams{CC: 3, ClipX: fbW - 1, ClipY: fbH - 1, FBWidth: fbW, FBHeight: fbH}
		native := make(compute.VDP1FB, fbW*fbH*2)
		rec := walkRecords(vram, native, &op)
		lines := uint32(draw.OpLines(&op))
		for _, l := range []uint32{0, 1, lines / 2, lines - 1} {
			for _, s := range []uint32{0, 1, 7, 30} {
				if l == 0 && s == 0 || l >= lines {
					continue
				}
				first, second := op, op
				first.LineTo = l
				if s != 0 {
					first.LineTo, first.LineEnd = l+1, s
				}
				second.LineFrom, second.LineStart = l, s
				for shift := uint32(1); shift <= 3; shift++ {
					pieces := make(compute.VDP1FB, len(native)<<(2*shift))
					whole := make(compute.VDP1FB, len(pieces))
					fillFB(pieces, bg)
					fillFB(whole, bg)
					var scaled draw.DrawOp
					b := draw.Buffers{VRAM: vram, FB: pieces, Records: rec}
					scaleOp(&scaled, &first, shift)
					draw.Generate(&b, &scaled, 0, 1)
					scaleOp(&scaled, &second, shift)
					draw.Generate(&b, &scaled, 0, 1)
					scaleOp(&scaled, &op, shift)
					b.FB = whole
					draw.Generate(&b, &scaled, 0, 1)
					if !bytes.Equal(pieces, whole) {
						t.Fatalf("%s split %d.%d shift %d: the entries differ from the whole op at byte %d", f.name, l, s, shift, firstDiff(pieces, whole))
					}
					checked++
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no split checked")
	}
}

// TestScaleOpAxisAlignedQuads draws axis-aligned distorted sprites and
// polygons, flat and Gouraud shaded, in all eight vertex orders, every
// flip, and texture ratios that enlarge, keep, and shrink the
// character, natively through the walk's loop and at scale shifts 1
// to 3. An axis-aligned part's scaled edges land on N times the
// native edges, and each native dot is its N by N block: every
// sub-dot is drawn where its native dot is and nowhere else, the
// extensions completing the blocks of the last line and the last
// step; every native dot's value is at the corner where the walk
// enters its block, the low corner along an axis walked + and the
// high corner along an axis walked -; and where the values are
// constant over a block, a flat polygon or an unflipped distorted
// sprite at a 1:1 texture, every sub-dot is its native dot, in every
// vertex order. A flipped texture's coordinates fall along the walk,
// so a sub-dot past a block's entry takes the whole part of a value
// below the native one, the next texel at a 1:1 texture.
func TestScaleOpAxisAlignedQuads(t *testing.T) {
	const fbW, fbH, charW, charH = 128, 64, 16, 8
	vram := new(compute.VDP1VRAM)
	for r := range charH {
		for c := range charW {
			px := uint16(0x8000 | (c*37+r*101)<<5&0x7FE0 | (c+r)&0x1F | 1)
			vram[(r*charW+c)*2], vram[(r*charW+c)*2+1] = uint8(px>>8), uint8(px)
		}
	}
	checked, whole := 0, 0
	for _, kind := range []uint32{draw.OpDistortedSprite, draw.OpPolygon} {
		for _, cc := range []uint32{0, 4} {
			for _, size := range [][2]int32{{32, 16}, {16, 8}, {8, 16}, {8, 5}, {5, 11}} {
				x0, y0 := int32(10), int32(5)
				x1, y1 := x0+size[0]-1, y0+size[1]-1
				corners := [4][2]int32{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
				for order := range 8 {
					for _, flip := range []uint32{0, draw.OpFlagFlipH, draw.OpFlagFlipV, draw.OpFlagFlipH | draw.OpFlagFlipV} {
						op := draw.DrawOp{Kind: kind, CharW: charW, CharH: charH, ColorMode: 5, Color: 0x8000 | 9<<10 | 14<<5 | 20,
							Flags: flip | draw.OpFlagECDOff | draw.OpFlagSPDOn, Gouraud: [4]uint32{0x0210, 0x7C1F, 0x03E0, 0x4A52}}
						for k := range 4 {
							c := (order + k) % 4
							if order >= 4 {
								c = (order - k + 4) % 4
							}
							op.X[k], op.Y[k] = corners[c][0], corners[c][1]
						}
						op.Pixel = draw.PixelParams{CC: cc, ClipX: fbW - 1, ClipY: fbH - 1, FBWidth: fbW, FBHeight: fbH}
						native := make(compute.VDP1FB, fbW*fbH*2)
						rec := walkRecords(vram, native, &op)
						// The lines run A to B and the edges A to D, on
						// the other axis; the walk enters a block at its
						// high corner along an axis it walks -.
						lineLen := max(absInt32(op.X[1]-op.X[0]), absInt32(op.Y[1]-op.Y[0]))
						steps := max(absInt32(op.X[3]-op.X[0]), absInt32(op.Y[3]-op.Y[0]))
						entryX := op.X[1] < op.X[0] || op.X[3] < op.X[0]
						entryY := op.Y[1] < op.Y[0] || op.Y[3] < op.Y[0]
						blocks := cc == 0 && (kind == draw.OpPolygon || flip == 0 && lineLen+1 == charW && steps+1 == charH)
						for shift := uint32(1); shift <= 3; shift++ {
							n := int32(1) << shift
							sw := fbW * n
							fb := make(compute.VDP1FB, len(native)<<(2*shift))
							var scaled draw.DrawOp
							scaleOp(&scaled, &op, shift)
							draw.Generate(&draw.Buffers{VRAM: vram, FB: fb, Records: rec}, &scaled, 0, 1)
							ox, oy := int32(0), int32(0)
							if entryX {
								ox = n - 1
							}
							if entryY {
								oy = n - 1
							}
							for y := range int32(fbH) {
								for x := range int32(fbW) {
									want := native.U16BE(uint32(y*fbW+x) * 2)
									if got := fb.U16BE(uint32((n*y+oy)*sw+n*x+ox) * 2); got != want {
										t.Fatalf("kind %d cc %d size %v order %d flip %d shift %d: entry corner of (%d, %d) = %04x, want the native %04x", kind, cc, size, order, flip, shift, x, y, got, want)
									}
									for sy := range n {
										for sx := range n {
											got := fb.U16BE(uint32((n*y+sy)*sw+n*x+sx) * 2)
											if (got != 0) != (want != 0) || blocks && got != want {
												t.Fatalf("kind %d cc %d size %v order %d flip %d shift %d: sub-dot (%d, %d) of (%d, %d) = %04x, native %04x", kind, cc, size, order, flip, shift, sx, sy, x, y, got, want)
											}
										}
									}
								}
							}
							checked++
							if blocks {
								whole++
							}
						}
					}
				}
			}
		}
	}
	if checked == 0 || whole == 0 {
		t.Errorf("%d parts checked, %d of them whole blocks", checked, whole)
	}
}

// drawScaledOps draws ops natively through the walk's loop and at the
// scale shift through generate, each op over the one before in its
// frame buffer, with its pixel write mode and the frame buffer's clip
// and geometry, and returns the two frame buffers. fbW * fbH * 2 must
// be a power of two, as a frame buffer's length is.
func drawScaledOps(ops []draw.DrawOp, fbW, fbH int32, shift uint32) (native, scaled compute.VDP1FB) {
	vram := new(compute.VDP1VRAM)
	for i := range 64 {
		vram[i*2], vram[i*2+1] = 0x80|uint8(i), uint8(i*29)
	}
	native = make(compute.VDP1FB, fbW*fbH*2)
	scaled = make(compute.VDP1FB, len(native)<<(2*shift))
	for i := range ops {
		op := ops[i]
		op.Pixel.ClipX, op.Pixel.ClipY, op.Pixel.FBWidth, op.Pixel.FBHeight = fbW-1, fbH-1, fbW, fbH
		rec := walkRecords(vram, native, &op)
		var s draw.DrawOp
		scaleOp(&s, &op, shift)
		draw.Generate(&draw.Buffers{VRAM: vram, FB: scaled, Records: rec}, &s, 0, 1)
	}
	return native, scaled
}

// TestScaleOpSegmentsAndCollinearQuadsBlocks draws axis-aligned
// polylines in all eight vertex orders, axis-aligned lines both ways
// and a line of one dot, a polygon whose side edges are points, and
// collinear polygons along each axis and in each direction, in one
// color, natively and at scale shifts 1 to 3, and holds the scaled
// frame buffer to the native one expanded to blocks: each dot of a
// segment or a collinear quad's lines claims its N by N square on its
// + sides, which along an axis is the dot's block.
func TestScaleOpSegmentsAndCollinearQuadsBlocks(t *testing.T) {
	const fbW, fbH = 128, 64
	rect := [4][2]int32{{10, 8}, {50, 8}, {50, 30}, {10, 30}}
	type scene struct {
		name string
		ops  []draw.DrawOp
	}
	var scenes []scene
	for order := range 8 {
		op := draw.DrawOp{Kind: draw.OpPolyline, Color: 0x83E5}
		for k := range 4 {
			c := (order + k) % 4
			if order >= 4 {
				c = (order - k + 4) % 4
			}
			op.X[k], op.Y[k] = rect[c][0], rect[c][1]
		}
		scenes = append(scenes, scene{fmt.Sprintf("polyline order %d", order), []draw.DrawOp{op}})
	}
	line := func(x1, y1, x2, y2 int32) draw.DrawOp {
		return draw.DrawOp{Kind: draw.OpLine, Color: 0xFC1F, X: [4]int32{x1, x2}, Y: [4]int32{y1, y2}}
	}
	quad := func(kind uint32, xs, ys [4]int32) draw.DrawOp {
		return draw.DrawOp{Kind: kind, Color: 0x9234, CharW: 8, CharH: 8, ColorMode: 5,
			Flags: draw.OpFlagECDOff | draw.OpFlagSPDOn, X: xs, Y: ys}
	}
	scenes = append(scenes,
		scene{"lines", []draw.DrawOp{line(5, 5, 60, 5), line(60, 12, 5, 12), line(20, 20, 20, 50), line(30, 50, 30, 20), line(70, 40, 70, 40)}},
		scene{"point-sided polygon", []draw.DrawOp{quad(draw.OpPolygon, [4]int32{12, 60, 60, 12}, [4]int32{20, 20, 20, 20})}},
		scene{"collinear polygon along X", []draw.DrawOp{quad(draw.OpPolygon, [4]int32{10, 40, 60, 30}, [4]int32{10, 10, 10, 10})}},
		scene{"collinear polygon along Y", []draw.DrawOp{quad(draw.OpPolygon, [4]int32{40, 40, 40, 40}, [4]int32{50, 20, 5, 30})}},
		scene{"collinear polygon along X walking -", []draw.DrawOp{quad(draw.OpPolygon, [4]int32{60, 30, 10, 40}, [4]int32{40, 40, 40, 40})}},
	)
	for _, s := range scenes {
		for shift := uint32(1); shift <= 3; shift++ {
			native, scaled := drawScaledOps(s.ops, fbW, fbH, shift)
			want := expandFB(native, fbW, false, shift)
			if !bytes.Equal(scaled, want) {
				i := firstDiff(scaled, want) / 2
				t.Fatalf("%s shift %d: sub-dot (%d, %d) differs from the native expanded to blocks", s.name, shift, i%(fbW<<shift), i/(fbW<<shift))
			}
		}
	}
}

// TestScaleOpDiagonalLinesSquares draws diagonal lines in all four
// directions along each major axis at scale shifts 1 to 3 and holds
// each to the scaled segment's dots, each claiming its N by N square
// on its + sides.
func TestScaleOpDiagonalLinesSquares(t *testing.T) {
	const fbW, fbH = 128, 64
	for _, l := range [][4]int32{{5, 5, 60, 30}, {60, 30, 5, 5}, {5, 30, 60, 5}, {60, 5, 5, 30}, {10, 5, 30, 60}, {30, 60, 10, 5}, {30, 5, 10, 60}, {10, 60, 30, 5}} {
		op := draw.DrawOp{Kind: draw.OpLine, Color: 0xFC1F, X: [4]int32{l[0], l[2]}, Y: [4]int32{l[1], l[3]}}
		for shift := uint32(1); shift <= 3; shift++ {
			n := int32(1) << shift
			_, scaled := drawScaledOps([]draw.DrawOp{op}, fbW, fbH, shift)
			sw := int32(fbW) * n
			want := make(compute.VDP1FB, len(scaled))
			var s draw.Segment
			draw.BeginSegment(&s, l[0]*n, l[1]*n, l[2]*n, l[3]*n, 0, 0)
			for i := int32(0); i <= s.IMax; i++ {
				x, y, _ := draw.SegmentAt(&s, i)
				for dy := range n {
					for dx := range n {
						want.PutU16BE(uint32((y+dy)*sw+x+dx)*2, uint16(op.Color))
					}
				}
			}
			if !bytes.Equal(scaled, want) {
				i := firstDiff(scaled, want) / 2
				t.Fatalf("line %v shift %d: sub-dot (%d, %d) differs from the squares", l, shift, int32(i)%sw, int32(i)/sw)
			}
		}
	}
}

// TestScaleOpFarLinesSquares draws lines running thousands of dots
// from off screen through the frame buffer, both ways, at scale
// shifts 1 to 3, and holds the sub-dots landing on screen to the
// scaled segment's dots claiming their squares: at 8x the on-screen
// steps are past the 32-bit range of the minor advance's product.
func TestScaleOpFarLinesSquares(t *testing.T) {
	const fbW, fbH = 128, 64
	for _, l := range [][4]int32{{-4000, -4000, 1000, 1000}, {1000, 1000, -4000, -4000}, {-3000, 5000, 900, -300}, {900, -300, -3000, 5000}} {
		op := draw.DrawOp{Kind: draw.OpLine, Color: 0xFC1F, X: [4]int32{l[0], l[2]}, Y: [4]int32{l[1], l[3]}}
		for shift := uint32(1); shift <= 3; shift++ {
			n := int32(1) << shift
			_, scaled := drawScaledOps([]draw.DrawOp{op}, fbW, fbH, shift)
			sw, sh := int32(fbW)*n, int32(fbH)*n
			want := make(compute.VDP1FB, len(scaled))
			var s draw.Segment
			draw.BeginSegment(&s, l[0]*n, l[1]*n, l[2]*n, l[3]*n, 0, 0)
			for i := int32(0); i <= s.IMax; i++ {
				x, y, _ := draw.SegmentAt(&s, i)
				for dy := range n {
					for dx := range n {
						if cx, cy := x+dx, y+dy; cx >= 0 && cx < sw && cy >= 0 && cy < sh {
							want.PutU16BE(uint32(cy*sw+cx)*2, uint16(op.Color))
						}
					}
				}
			}
			if !bytes.Equal(scaled, want) {
				i := firstDiff(scaled, want) / 2
				t.Fatalf("line %v shift %d: sub-dot (%d, %d) differs from the squares", l, shift, int32(i)%sw, int32(i)/sw)
			}
		}
	}
}

// TestScaleOpShadedSegmentsCoverFlat draws diagonal lines in every
// direction, at several slopes, and a diagonal polyline, each flat
// and Gouraud-shaded, natively and at scale shifts 1 to 3, and holds
// the shaded draw's covered sub-dots to the flat draw's, sub-dot for
// sub-dot: Gouraud shading is a color calculation, so it changes no
// dot's coverage, and the per-step draw of a shaded segment has to
// write every sub-dot the per-column draw of a flat one writes.
func TestScaleOpShadedSegmentsCoverFlat(t *testing.T) {
	const fbW, fbH = 128, 64
	lines := [][4]int32{{5, 5, 60, 40}, {60, 40, 5, 5}, {10, 50, 40, 10}, {40, 10, 10, 50}, {8, 8, 100, 20}, {100, 20, 8, 8}, {20, 8, 30, 58}, {30, 58, 20, 8}, {3, 3, 4, 5}}
	var ops []draw.DrawOp
	for _, l := range lines {
		ops = append(ops, draw.DrawOp{Kind: draw.OpLine, Color: 0x83E5, X: [4]int32{l[0], l[2]}, Y: [4]int32{l[1], l[3]}})
	}
	ops = append(ops, draw.DrawOp{Kind: draw.OpPolyline, Color: 0x83E5, X: [4]int32{10, 70, 90, 30}, Y: [4]int32{10, 20, 55, 45}})
	for _, op := range ops {
		shaded := op
		shaded.Pixel.CC = 4
		shaded.Gouraud = [4]uint32{0x0210, 0x7C1F, 0x03E0, 0x7C00}
		for shift := uint32(0); shift <= 3; shift++ {
			_, flat := drawScaledOps([]draw.DrawOp{op}, fbW, fbH, shift)
			_, got := drawScaledOps([]draw.DrawOp{shaded}, fbW, fbH, shift)
			w := int(fbW) << shift
			for i := 0; i < len(flat); i += 2 {
				f := flat[i] != 0 || flat[i+1] != 0
				s := got[i] != 0 || got[i+1] != 0
				if f != s {
					t.Fatalf("%v %v..%v shift %d: sub-dot (%d, %d) covered by the flat draw %v, by the shaded draw %v", op.Kind, op.X, op.Y, shift, (i/2)%w, (i/2)/w, f, s)
				}
			}
		}
	}
}

// TestScaleOpSegmentGouraudEntryCorners draws Gouraud-shaded lines
// both ways along each axis at scale shifts 1 to 3, and holds every
// native dot's value at the corner where the walk enters its block:
// along the major axis the low corner for a line walking + and the
// high corner for one walking -, along the minor axis the low corner,
// the - side of the dot's square. A diagonal line's scaled path refines
// its minor-axis position between block corners, so the axis-aligned
// lines are the ones whose dots land on them.
func TestScaleOpSegmentGouraudEntryCorners(t *testing.T) {
	const fbW, fbH = 128, 64
	for _, l := range [][4]int32{{5, 5, 60, 5}, {60, 12, 5, 12}, {20, 5, 20, 50}, {30, 50, 30, 5}} {
		op := draw.DrawOp{Kind: draw.OpLine, Color: 0x8000 | 12<<10 | 20<<5 | 6, X: [4]int32{l[0], l[2]}, Y: [4]int32{l[1], l[3]}}
		op.Gouraud = [4]uint32{0x0210, 0x7C1F}
		xMajor := absInt32(l[2]-l[0]) >= absInt32(l[3]-l[1])
		for shift := uint32(1); shift <= 3; shift++ {
			n := int32(1) << shift
			ops := []draw.DrawOp{op}
			ops[0].Pixel.CC = 4
			native, scaled := drawScaledOps(ops, fbW, fbH, shift)
			ox, oy := int32(0), int32(0)
			if xMajor && l[2] < l[0] {
				ox = n - 1
			}
			if !xMajor && l[3] < l[1] {
				oy = n - 1
			}
			sw := int32(fbW) * n
			for y := range int32(fbH) {
				for x := range int32(fbW) {
					want := native.U16BE(uint32(y*fbW+x) * 2)
					if want == 0 {
						continue
					}
					if got := scaled.U16BE(uint32((n*y+oy)*sw+n*x+ox) * 2); got != want {
						t.Fatalf("line %v shift %d: entry corner of (%d, %d) = %04x, want the native %04x", l, shift, x, y, got, want)
					}
				}
			}
		}
	}
}

// absInt32 is the absolute value of v.
func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// TestScaledSpriteEndCodesAtScale draws a shrinking 256-color scaled
// sprite with end codes, unflipped and flipped, natively through the
// walk's loop for its line records and at scale shifts 1 to 3. Half
// its rows hold end codes only in columns the native row skips, so
// the native row does not end, and the other half two end codes the
// native row reads, the second ending it, with a third between them
// that only a scaled row reads. A scaled row draws every dot up to
// the first column whose source sample reaches the native row's
// ending column, except the end codes, which never draw, and none
// from there on; a row that did not end natively does not end.
func TestScaledSpriteEndCodesAtScale(t *testing.T) {
	const charW, charH, ec = 32, 8, 0xFF
	vram := new(compute.VDP1VRAM)
	endCodes := [][]int{{2, 3}, {9, 10, 14}}
	for r := range charH {
		for c := range charW {
			vram[r*charW+c] = uint8(0x10 + c)
		}
		for _, c := range endCodes[r%2] {
			vram[r*charW+c], vram[r*charW+charW-1-c] = ec, ec
		}
	}
	const fbW, fbH = 64, 16
	for _, flip := range []uint32{0, draw.OpFlagFlipH} {
		op := draw.DrawOp{Kind: draw.OpScaledSprite, CharW: charW, CharH: charH, ColorMode: 4, Color: 0x0100, Flags: flip}
		op.X[0], op.Y[0], op.X[2], op.Y[2] = 4, 2, 4+11, 2+charH-1
		op.Pixel = draw.PixelParams{ClipX: fbW - 1, ClipY: fbH - 1, FBWidth: fbW, FBHeight: fbH}
		native := make(compute.VDP1FB, fbW*fbH*2)
		rec := walkRecords(vram, native, &op)
		width := draw.SpriteRowWidth(&op)
		ended := 0
		for shift := uint32(1); shift <= 3; shift++ {
			n := int32(1) << shift
			var scaled draw.DrawOp
			scaleOp(&scaled, &op, shift)
			fb := make(compute.VDP1FB, len(native)<<(2*shift))
			draw.Generate(&draw.Buffers{VRAM: vram, FB: fb, Records: rec}, &scaled, 0, 1)
			sw := int32(fbW) * n
			for y := range charH * n {
				end := draw.LoadLineEnd(rec, 0, y/n)
				uEnd := int32(charW)
				if end < width {
					uEnd = ((2*end + 1) * charW) / (2 * width)
					if shift == 1 {
						ended++
					}
				}
				for x := range width * n {
					u := ((2*x + 1) * charW) / (2 * width * n)
					src := draw.ScaledSrcX(&scaled, x)
					want := u < uEnd && vram[(y/n)*charW+src] != ec
					i := ((scaled.Y[0]+y)*sw + scaled.X[0] + x) * 2
					if got := fb[i]|fb[i+1] != 0; got != want {
						t.Fatalf("flip %d shift %d row %d column %d (source %d, ending source %d): drawn %v, want %v", flip, shift, y, x, u, uEnd, got, want)
					}
				}
			}
		}
		if ended == 0 {
			t.Fatalf("flip %d: no native row ended on its end codes", flip)
		}
	}
}

// BenchmarkEnhancedVDP1Generate draws the benchmark scenes' ops, as
// the walk lists them in one call, through generate on the software
// schedule at scale shifts 1 and 2, the enhanced software renderer's
// VDP1 draw.
func BenchmarkEnhancedVDP1Generate(b *testing.B) {
	for _, s := range benchVDP1Scenes() {
		in := vdp1GenerateScene(s)
		if len(in.ops) == 0 {
			continue
		}
		for shift := uint32(1); shift <= 2; shift++ {
			scaled := make([]draw.DrawOp, len(in.ops))
			for i := range in.ops {
				scaleOp(&scaled[i], &in.ops[i], shift)
			}
			fb := make(compute.VDP1FB, len(in.pre)<<(2*shift))
			bufs := draw.Buffers{VRAM: (*compute.VDP1VRAM)(in.v.vram), FB: fb, Records: in.v.lineRecords}
			b.Run(fmt.Sprintf("%s/shift %d", s.name, shift), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					for i := range scaled {
						if scaled[i].Kind == draw.OpErase {
							draw.EraseRows(fb, &scaled[i], 0, draw.OpLines(&scaled[i]))
							continue
						}
						draw.Generate(&bufs, &scaled[i], 0, 1)
					}
				}
			})
		}
	}
}
