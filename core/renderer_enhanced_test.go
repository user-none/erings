// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/user-none/erings/core/compute/screen"
)

// packedBitmapScene is an NBG0 256-color bitmap whose display rows
// are 320-dot rows packed end to end in the 512-dot bitmap, each line
// scroll table entry addressing one packed row by its X and Y, as a
// decoded video is displayed: entry l's row starts at bitmap offset
// 320*l, X 320*l mod 512 and Y 320*l/512 with its fraction. lss is
// SCRCTL's N0LSS, the lines per entry 1 << lss.
func packedBitmapScene(lss uint16) func() (*VDP2, vdp1FBView) {
	return func() (*VDP2, vdp1FBView) {
		v := benchVDP2()
		benchRegOr(v, vdp2BGON, 0x0001)
		benchReg(v, vdp2CHCTLA, 0x0012)
		benchReg(v, vdp2BMPNA, 0x0000)
		benchReg(v, vdp2MPOFN, 0x0003)
		benchReg(v, vdp2PRINA, 0x0001)
		benchBitmap8(v, 3*0x20000)
		benchReg(v, vdp2SCRCTL, 0x0006|lss<<4)
		benchReg(v, vdp2LSTA0U, 0x0000)
		benchReg(v, vdp2LSTA0L, 0xE000)
		for l := uint32(0); l < 512; l++ {
			x, sy := l*320%512, l*160
			e := benchLineScroll + l*8
			v.WriteVRAM16(e, uint16(x))
			v.WriteVRAM16(e+2, 0x0000)
			v.WriteVRAM16(e+4, uint16(sy>>8))
			v.WriteVRAM16(e+6, uint16(sy&0xFF)<<8)
		}
		return v, vdp1FBView{}
	}
}

// enhancedScenes are the scenes the enhanced renderer draws whole:
// the benchmark scenes of the normal and rotation scroll screens,
// with no sprite layer, plus bitmaps whose rows the line scroll table
// packs, a gradation scene, and an external image scene. The external
// image's window has its left edge off screen and ratios other than
// 1:1 on both axes, so its sub-dots read video pixels the native dots
// step over.
func enhancedScenes() []benchVDP2Scene {
	var out []benchVDP2Scene
	for _, s := range benchVDP2Scenes() {
		if strings.HasPrefix(s.name, "NBG") || strings.HasPrefix(s.name, "RBG") || s.name == "four NBG layers" {
			out = append(out, s)
		}
	}
	return append(out,
		benchVDP2Scene{name: "NBG0 bitmap rows packed by line scroll", build: packedBitmapScene(0)},
		benchVDP2Scene{name: "NBG0 bitmap rows packed by line scroll every 2 lines", build: packedBitmapScene(1)},
		benchVDP2Scene{name: "RBG0 cell gradation", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchRBG0Cell(v, 1)
			benchReg(v, vdp2CCCTL, 0x9000)
			return v, vdp1FBView{}
		}},
		benchVDP2Scene{name: "external image over NBG0", build: func() (*VDP2, vdp1FBView) {
			v := benchVDP2()
			benchNBG0Cell4(v, 3)
			benchReg(v, vdp2EXTEN, 0x0001)
			benchRegOr(v, vdp2PRINA, 0x0500)
			v.SetEXBGSource(&fakeEXBGWindow{
				rgb: exbgTestFrame(352, 240), w: 352, h: 240,
				dx: -31, dy: 49, dw: 300, dh: 150, sx: 10, sy: 20, rx: 1300, ry: 700,
			})
			return v, vdp1FBView{}
		}},
	)
}

// TestEnhancedScale1MatchesSoftware holds the enhanced renderer at
// scale 1 to the accurate software renderer byte for byte over the
// displayed frame, through the equivalenceFrames.
func TestEnhancedScale1MatchesSoftware(t *testing.T) {
	for _, s := range enhancedScenes() {
		t.Run(s.name, func(t *testing.T) {
			sw, swFB := s.build()
			en, enFB := s.build()
			software := newSoftwareRenderer(sw)
			enhanced := newEnhancedRenderer(en, NewVDP1(NewSCU()))
			for _, f := range equivalenceFrames {
				if f.before != nil {
					f.before(sw)
					f.before(en)
				}
				renderFrame(sw, software, swFB, f.beforeRow)
				renderFrame(en, enhanced, enFB, f.beforeRow)
				stride, height := enhanced.framebufferStride(), enhanced.displayHeight()
				if stride != software.framebufferStride() || height != software.displayHeight() {
					t.Fatalf("%s: stride %d height %d, want %d and %d", f.name, stride, height, software.framebufferStride(), software.displayHeight())
				}
				equalRenderBytes(t, f.name, enhanced.framebuffer()[:stride*height], software.framebuffer()[:stride*height])
			}
		})
	}
}

// TestEnhancedFrameBlockCorners holds the enhanced frame at scale
// shifts 1 and 2 to the native frame at every block corner: the
// scaled pixel at (N*x, N*y) is the native pixel at (x, y) over the
// displayed frame, through the equivalenceFrames.
func TestEnhancedFrameBlockCorners(t *testing.T) {
	for _, s := range enhancedScenes() {
		for shift := uint32(1); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				sw, swFB := s.build()
				en, enFB := s.build()
				software := newSoftwareRenderer(sw)
				en.setEnhance(enhanceConfig{shift: shift})
				enhanced := newEnhancedRenderer(en, NewVDP1(NewSCU()))
				n := 1 << shift
				for _, f := range equivalenceFrames {
					if f.before != nil {
						f.before(sw)
						f.before(en)
					}
					renderFrame(sw, software, swFB, f.beforeRow)
					renderFrame(en, enhanced, enFB, f.beforeRow)
					native, stride, height := software.framebuffer(), software.framebufferStride(), software.displayHeight()
					scaled, scaledStride, scaledHeight := enhanced.framebuffer(), enhanced.framebufferStride(), enhanced.displayHeight()
					if scaledStride != stride*n || scaledHeight != height*n {
						t.Fatalf("%s: stride %d height %d, want %d and %d", f.name, scaledStride, scaledHeight, stride*n, height*n)
					}
					for y := range height {
						for x := 0; x < stride/4; x++ {
							want := native[y*stride+x*4:][:4]
							got := scaled[y*n*scaledStride+x*n*4:][:4]
							if !bytes.Equal(got, want) {
								t.Fatalf("%s: pixel (%d, %d) = % x, want the native % x", f.name, x, y, got, want)
							}
						}
					}
				}
			})
		}
	}
}

// TestScaleRowNBGSubRowsStep holds a normal scroll screen's sub-rows
// to its rows: where the source Y steps by the vertical increment
// from one row to the next (no line scroll in Y, no vertical mosaic,
// no scroll write rebasing the counter between the two rows),
// sub-row N of row y, one sub-row past the last, draws row y+1's
// sub-row 0 at scale shifts 1 to 3. A sub-row that does not advance,
// or advances by a divided or rounded step, draws another source row.
func TestScaleRowNBGSubRowsStep(t *testing.T) {
	checked := 0
	for _, s := range enhancedScenes() {
		v, fb := s.build()
		// The vertical counters load from the previous frame's end,
		// so the second frame is the first whose rows step alike.
		swr := newSoftwareRenderer(v)
		renderFrame(v, swr, fb, nil)
		renderFrame(v, swr, fb, nil)
		lines := int(v.ActiveLines())
		for y := 0; y+1 < lines; y++ {
			r, next := v.rowRecord(y), v.rowRecord(y+1)
			for i := 0; i < int(r.NSpans); i++ {
				entry, layer := spanKind(r.Spans[i])
				if entry != entryNBGCell && entry != entryNBGBitmap || !slices.Contains(next.Spans[:next.NSpans], r.Spans[i]) {
					continue
				}
				if np := &r.NBG[layer]; np.LineScrollY || np.MosaicV != 0 || np.ScrollYBaseFP != next.NBG[layer].ScrollYBaseFP {
					continue
				}
				for shift := uint32(1); shift <= 3; shift++ {
					p := scaledNBGRow(v, r, layer, 1<<shift, shift)
					q := scaledNBGRow(v, next, layer, 0, shift)
					if !slices.Equal(drawNBGRow(v, entry, &p), drawNBGRow(v, entry, &q)) {
						t.Fatalf("%s row %d NBG%d shift %d: sub-row N differs from the next row's sub-row 0", s.name, y, layer, shift)
					}
					checked++
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no row checked")
	}
}

// TestScaleRowNBGLineScrollYSubRows holds a normal scroll screen's
// sub-rows under line scroll Y at scale shifts 1 to 3: on the last
// line of a table entry, every line at an interval of 1, every
// sub-row draws the row, since the next entry sets the next line's
// source Y freely (a packed bitmap's next row lies elsewhere in the
// bitmap); on any other line of an entry, sub-row N draws the next
// line's sub-row 0.
func TestScaleRowNBGLineScrollYSubRows(t *testing.T) {
	repeated, stepped := 0, 0
	for _, s := range enhancedScenes() {
		v, fb := s.build()
		swr := newSoftwareRenderer(v)
		renderFrame(v, swr, fb, nil)
		renderFrame(v, swr, fb, nil)
		lines := int(v.ActiveLines())
		for y := 0; y+1 < lines; y++ {
			r, next := v.rowRecord(y), v.rowRecord(y+1)
			for i := 0; i < int(r.NSpans); i++ {
				entry, layer := spanKind(r.Spans[i])
				if entry != entryNBGCell && entry != entryNBGBitmap || !r.NBG[layer].LineScrollY {
					continue
				}
				np := &r.NBG[layer]
				last := np.DispY%np.LineInterval == np.LineInterval-1
				for shift := uint32(1); shift <= 3; shift++ {
					n := int32(1) << shift
					q := scaledNBGRow(v, r, layer, 0, shift)
					want := drawNBGRow(v, entry, &q)
					if !last {
						q = scaledNBGRow(v, next, layer, 0, shift)
						p := scaledNBGRow(v, r, layer, n, shift)
						if !slices.Equal(drawNBGRow(v, entry, &p), drawNBGRow(v, entry, &q)) {
							t.Fatalf("%s row %d NBG%d shift %d: sub-row N differs from the next row's sub-row 0", s.name, y, layer, shift)
						}
						stepped++
						continue
					}
					for k := int32(1); k < n; k++ {
						p := scaledNBGRow(v, r, layer, k, shift)
						if !slices.Equal(drawNBGRow(v, entry, &p), want) {
							t.Fatalf("%s row %d NBG%d shift %d: sub-row %d differs from sub-row 0 on an entry's last line", s.name, y, layer, shift, k)
						}
					}
					repeated++
				}
			}
		}
	}
	if repeated == 0 || stepped == 0 {
		t.Errorf("%d entry-end rows and %d in-entry rows checked, want some of each", repeated, stepped)
	}
}

// TestScaleRowRBGSubRowsStep holds a rotation screen's sub-rows to
// its rows: over every benchmark scene's rotation rows whose next row
// does not re-read, the sub-row one field row past the first (sub-row
// N, or 2N under double-density interlace, where a field's rows are
// two display lines apart) draws the next row's sub-row 0 at scale
// shifts 1 to 3, the layer and the line color. Each sub-row is taken
// at its row's setup point, where scaleRow reads the rotation state.
// A sub-row that advances by a divided or rounded step draws other
// map positions. The line coefficient scene designates no bank for
// per-dot coefficient reads, so every dot takes the row's line-start
// coefficient (VDP2 manual Sec 6.2), which a sub-row must read at its
// own row coefficient address.
func TestScaleRowRBGSubRowsStep(t *testing.T) {
	type subRow struct {
		ok    bool
		entry int
		p     screen.RBGParams
	}
	scenes := append(benchVDP2Scenes(), benchVDP2Scene{name: "RBG0 cell line coefficients", build: func() (*VDP2, vdp1FBView) {
		v := benchVDP2()
		benchRBG0Cell(v, 1)
		benchRBG0Coefficients(v)
		benchReg(v, vdp2RAMCTL, 0x0000)
		return v, vdp1FBView{}
	}})
	checked, interlaced := 0, 0
	for _, s := range scenes {
		v, fb := s.build()
		// The second frame is the first whose rows step alike.
		swr := newSoftwareRenderer(v)
		renderFrame(v, swr, fb, nil)
		var next [2][4]subRow // by rotation layer and shift
		v.BeginFrame()
		swr.beginFrame()
		lines := int(v.ActiveLines())
		for y := 0; y < lines; y++ {
			beginRow(v, swr, y, fb)
			r := v.rowRecord(y)
			var seen [2]bool
			for i := 0; i < int(r.NSpans); i++ {
				entry, layer := spanKind(r.Spans[i])
				if entry != entryRBGCell && entry != entryRBGBitmap {
					continue
				}
				l := layer - screen.LayerRBG0
				seen[l] = true
				for shift := uint32(1); shift <= 3; shift++ {
					if n := &next[l][shift]; n.ok && n.entry == entry && v.rotArmBits(y) == 0 {
						q := scaledRBGRow(v, r, layer, 0, shift)
						gotL, gotLC := drawRBGRow(v, entry, &n.p)
						wantL, wantLC := drawRBGRow(v, entry, &q)
						if !slices.Equal(gotL, wantL) || !slices.Equal(gotLC, wantLC) {
							t.Fatalf("%s row %d RBG%d shift %d: the sub-row a field row past row %d differs from row %d's sub-row 0", s.name, y, l, shift, y-1, y)
						}
						checked++
						if v.frame.lsmd3 {
							interlaced++
						}
					}
					k := int32(1) << shift
					if v.frame.lsmd3 {
						k <<= 1
					}
					next[l][shift] = subRow{ok: true, entry: entry, p: scaledRBGRow(v, r, layer, k, shift)}
				}
			}
			for l := range seen {
				if !seen[l] {
					next[l] = [4]subRow{}
				}
			}
			swr.renderLine(y)
			v.EndLine()
		}
		swr.endDisplay()
		v.EndFrame()
		swr.endFrame()
	}
	if checked == 0 {
		t.Error("no row checked")
	}
	if interlaced == 0 {
		t.Error("no double-density interlace row checked")
	}
}

// TestEnhancedRendererNoAllocs checks that the enhanced renderer's
// frame allocates nothing after the first.
func TestEnhancedRendererNoAllocs(t *testing.T) {
	for _, s := range enhancedScenes() {
		t.Run(s.name, func(t *testing.T) {
			v, fb := s.build()
			v.setEnhance(enhanceConfig{shift: 1})
			e := newEnhancedRenderer(v, NewVDP1(NewSCU()))
			frame := func() { renderFrame(v, e, fb, nil) }
			frame()
			if n := testing.AllocsPerRun(3, frame); n != 0 {
				t.Errorf("%.0f allocations per frame", n)
			}
		})
	}
}
