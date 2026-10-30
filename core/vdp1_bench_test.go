// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"testing"
	"time"

	"github.com/user-none/erings/internal/hostgpu"
)

// VDP1 draw benchmarks. Every scene is a command list written to VRAM
// through the bus-facing entry points and drawn through the frame
// loop's sequence: VBlankIn (frame buffer change), VBlankOut, the
// PTM=10 auto-trigger, and TickSystemCycles. Nothing here names a
// renderer-internal function, so the numbers stay comparable across
// refactors of the draw path.
//
// Two drivers draw the same lists. BenchmarkVDP1 draws a list in one
// TickSystemCycles call with a budget that covers the whole list and
// isolates the rasterizer inner loops. BenchmarkVDP1Walker draws it
// in one TickSystemCycles call per VDP1 tick of the frame loop until
// the command processor goes idle, the emulator's cadence, so the
// per-call entry, the resume path, the yield checks, and the debt
// carry are inside the number. The walker number is the gate; the
// one-call number is the reference.
//
// Each scene runs under both renderers, as BenchmarkVDP2's do. The
// software sub-benchmark is the walk drawing the list. The gpu
// sub-benchmark is the same frame as the frame loop runs it in GPU
// mode: the walk charges the list's cycles without drawing, the
// renderer takes the entries after each tick and draws them on the
// device, the display's end takes the rest and submits, and the
// frame's end waits. It reports the takes and the submission on the
// walker and the wait per frame beside ns/op. The frame has no VDP2
// rows, so the VDP2's snapshot and rows, which BenchmarkVDP2
// measures, stay out of the number. Without a device it skips.
//
// ns/op is the cost of drawing the scene's list once. The lists are
// fixed, so the unit of work is fixed by the list geometry.
//
// TestBenchVDP1Scenes pins every scene's output under both drivers
// against the same hashes. A benchmark number is comparable with an
// earlier one only while that test passes: a scene whose output
// changed measures different work.

// benchVDP1Budget covers any list in one TickSystemCycles call.
const benchVDP1Budget = 1 << 30

// benchVDP1WalkerTick is the walker driver's tick, one scanline of
// system cycles (the 320-dot NTSC line), the frame loop's cadence.
const benchVDP1WalkerTick = 1708

// benchVDP1WalkerMaxTicks bounds the walker driver's tick loop so a
// list that never reaches its END command fails the test instead of
// spinning.
const benchVDP1WalkerMaxTicks = benchVDP1Budget / benchVDP1WalkerTick

// VDP1 VRAM layout: the command list from 0, textures and tables from
// 0x20000.
const (
	benchTex8    = 0x20000 // 32x32 256-color texture
	benchTex4    = 0x21000 // 16x16 16-color texture
	benchCLUT    = 0x22000 // 16-entry color lookup table
	benchTexRGB  = 0x24000 // 32x32 RGB texture
	benchGouraud = 0x28000 // Gouraud shading table
)

// benchCmd is one command table entry.
type benchCmd struct {
	ctrl, pmod, colr, srca, size   uint16
	xa, ya, xb, yb, xc, yc, xd, yd int16
	grda                           uint16
}

// benchList appends commands to a VDP1 command table.
type benchList struct {
	v    *VDP1
	addr uint32
}

func (l *benchList) add(c benchCmd) {
	v, a := l.v, l.addr
	v.WriteVRAM16(a+0x00, c.ctrl)
	v.WriteVRAM16(a+0x02, 0x0000)
	v.WriteVRAM16(a+0x04, c.pmod)
	v.WriteVRAM16(a+0x06, c.colr)
	v.WriteVRAM16(a+0x08, c.srca)
	v.WriteVRAM16(a+0x0A, c.size)
	v.WriteVRAM16(a+0x0C, uint16(c.xa))
	v.WriteVRAM16(a+0x0E, uint16(c.ya))
	v.WriteVRAM16(a+0x10, uint16(c.xb))
	v.WriteVRAM16(a+0x12, uint16(c.yb))
	v.WriteVRAM16(a+0x14, uint16(c.xc))
	v.WriteVRAM16(a+0x16, uint16(c.yc))
	v.WriteVRAM16(a+0x18, uint16(c.xd))
	v.WriteVRAM16(a+0x1A, uint16(c.yd))
	v.WriteVRAM16(a+0x1C, c.grda)
	l.addr += 0x20
}

func (l *benchList) end() {
	l.v.WriteVRAM16(l.addr, 0x8000)
}

// benchVDP1 returns a VDP1 in 16-bit 512x256 mode with 1-cycle frame
// buffer change and the V-blank auto-trigger, the textures written,
// and a list opened with the system clip at (319,223) and local
// coordinates at the origin.
func benchVDP1() (*VDP1, *benchList) {
	v := NewVDP1(NewSCU())
	v.Write(0x00, 0x0000)
	v.Write(0x02, 0x0000)
	v.Write(0x04, 0x0002)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			d8 := uint8(1 + (x*3+y*5)%250)
			rgb := uint16(0x8000 | (x & 0x1F) | (y&0x1F)<<5 | ((x+y)&0x1F)<<10)
			if (x+y)%9 == 0 {
				d8, rgb = 0, 0
			}
			v.WriteVRAM(benchTex8+uint32(y*32+x), d8)
			v.WriteVRAM16(benchTexRGB+uint32(y*32+x)*2, rgb)
		}
	}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x += 2 {
			d0, d1 := uint8(1+(x+y)%15), uint8(1+(x+1+y)%15)
			if (x*y)%11 == 0 {
				d0 = 0
			}
			v.WriteVRAM(benchTex4+uint32(y*8+x/2), d0<<4|d1)
		}
	}
	for d := uint32(1); d < 16; d++ {
		v.WriteVRAM16(benchCLUT+d*2, uint16(0x8000|(d*0x0C63)&0x7FFF))
	}
	v.WriteVRAM16(benchGouraud+0, 0x4210)
	v.WriteVRAM16(benchGouraud+2, 0x421F)
	v.WriteVRAM16(benchGouraud+4, 0x7C10)
	v.WriteVRAM16(benchGouraud+6, 0x0210)
	l := &benchList{v: v}
	l.add(benchCmd{ctrl: 0x0009, xc: 319, yc: 223})
	l.add(benchCmd{ctrl: 0x000A})
	return v, l
}

const (
	benchSize32 = 4<<8 | 32
	benchSize16 = 2<<8 | 16
	benchGRDA   = benchGouraud / 8
)

// Command builders. pmod carries the color mode in bits 5:3 and the
// color calculation mode in bits 2:0; extra ORs in mesh (bit 8) or
// user clipping (bits 10:9).

func benchNormal8(x, y int16) benchCmd {
	return benchCmd{pmod: 4 << 3, colr: 0x0100, srca: benchTex8 / 8, size: benchSize32, xa: x, ya: y}
}

func benchNormalCLUT(x, y int16) benchCmd {
	return benchCmd{pmod: 1 << 3, colr: benchCLUT / 8, srca: benchTex4 / 8, size: benchSize16, xa: x, ya: y}
}

func benchNormalRGBGouraud(x, y int16) benchCmd {
	return benchCmd{pmod: 5<<3 | 4, srca: benchTexRGB / 8, size: benchSize32, xa: x, ya: y, grda: benchGRDA}
}

// benchScaled is a two-point (zoom point 0) scaled 256-color sprite.
func benchScaled(x, y, w, h int16) benchCmd {
	return benchCmd{ctrl: 0x0001, pmod: 4 << 3, colr: 0x0100, srca: benchTex8 / 8, size: benchSize32,
		xa: x, ya: y, xc: x + w - 1, yc: y + h - 1}
}

// benchDistorted is a mildly skewed 256-color distorted sprite.
func benchDistorted(x, y int16, extra uint16) benchCmd {
	return benchCmd{ctrl: 0x0002, pmod: 4<<3 | extra, colr: 0x0100, srca: benchTex8 / 8, size: benchSize32,
		xa: x, ya: y, xb: x + 60, yb: y + 4, xc: x + 56, yc: y + 64, xd: x - 4, yd: y + 60}
}

// benchDistortedStrong is a trapezoid 96 wide at the top and 16 at
// the bottom with high speed shrink, which drives the connecting-line
// gap fill.
func benchDistortedStrong(x, y int16) benchCmd {
	return benchCmd{ctrl: 0x0002, pmod: 4<<3 | 0x1000, colr: 0x0100, srca: benchTex8 / 8, size: benchSize32,
		xa: x, ya: y, xb: x + 96, yb: y, xc: x + 56, yc: y + 64, xd: x + 40, yd: y + 64}
}

func benchPolygon(x, y int16, extra uint16, color uint16) benchCmd {
	return benchCmd{ctrl: 0x0004, pmod: extra, colr: color, grda: benchGRDA,
		xa: x, ya: y, xb: x + 200, yb: y + 8, xc: x + 190, yc: y + 150, xd: x + 6, yd: y + 140}
}

func benchPolyline(x, y int16) benchCmd {
	return benchCmd{ctrl: 0x0005, colr: 0x83FF, xa: x, ya: y, xb: x + 100, yb: y + 3, xc: x + 96, yc: y + 80, xd: x - 2, yd: y + 76}
}

func benchLineGouraud(x1, y1, x2, y2 int16) benchCmd {
	return benchCmd{ctrl: 0x0006, pmod: 4, colr: 0xFC00, grda: benchGRDA, xa: x1, ya: y1, xb: x2, yb: y2}
}

// benchGrid calls f for a cols x rows grid of positions with the given
// spacing.
func benchGrid(l *benchList, cols, rows int, dx, dy int16, f func(x, y int16) benchCmd) {
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			l.add(f(int16(c)*dx, int16(r)*dy))
		}
	}
}

type benchVDP1Scene struct {
	name  string
	build func() *VDP1
}

func benchVDP1Scenes() []benchVDP1Scene {
	grid := func(cols, rows int, dx, dy int16, f func(x, y int16) benchCmd) func() *VDP1 {
		return func() *VDP1 {
			v, l := benchVDP1()
			benchGrid(l, cols, rows, dx, dy, f)
			l.end()
			return v
		}
	}
	polygons := func(extra uint16) func() *VDP1 {
		return grid(2, 4, 110, 40, func(x, y int16) benchCmd {
			return benchPolygon(x+10, y+10, extra, 0x8000|uint16(x)|uint16(y)<<5)
		})
	}
	return []benchVDP1Scene{
		{name: "normal sprite 8bpp", build: grid(12, 7, 26, 30, benchNormal8)},
		{name: "normal sprite 4bpp CLUT", build: grid(20, 14, 16, 16, benchNormalCLUT)},
		{name: "normal sprite RGB gouraud", build: grid(12, 7, 26, 30, benchNormalRGBGouraud)},
		{name: "scaled sprite enlarge", build: grid(4, 3, 80, 72, func(x, y int16) benchCmd { return benchScaled(x, y, 96, 96) })},
		{name: "scaled sprite shrink", build: grid(20, 14, 16, 16, func(x, y int16) benchCmd { return benchScaled(x, y, 12, 12) })},
		{name: "distorted sprite", build: grid(8, 5, 40, 44, func(x, y int16) benchCmd { return benchDistorted(x+4, y, 0) })},
		{name: "distorted sprite strong HSS", build: grid(4, 3, 80, 72, benchDistortedStrong)},
		{name: "distorted sprite half-transparent", build: grid(8, 5, 40, 44, func(x, y int16) benchCmd { return benchDistorted(x+4, y, 3) })},
		{name: "distorted sprite converging half-transparent", build: grid(8, 5, 40, 44, func(x, y int16) benchCmd {
			c := benchDistorted(x+4, y, 3)
			c.xc, c.yc = c.xb, c.yb
			return c
		})},
		{name: "polygon", build: polygons(0)},
		{name: "polygon half-transparent", build: polygons(3)},
		{name: "polygon mesh", build: polygons(0x0100)},
		{name: "polygon gouraud", build: polygons(4)},
		{name: "polyline and line gouraud", build: func() *VDP1 {
			v, l := benchVDP1()
			benchGrid(l, 8, 5, 40, 44, func(x, y int16) benchCmd { return benchPolyline(x+2, y) })
			for i := int16(0); i < 160; i++ {
				l.add(benchLineGouraud(i*2, 0, 319-i*2, 223))
			}
			l.end()
			return v
		}},
		{name: "mixed", build: func() *VDP1 {
			v, l := benchVDP1()
			benchGrid(l, 5, 2, 60, 40, benchNormal8)
			benchGrid(l, 5, 2, 30, 20, func(x, y int16) benchCmd { return benchNormalCLUT(x+160, y+100) })
			benchGrid(l, 5, 1, 50, 0, benchNormalRGBGouraud)
			benchGrid(l, 5, 1, 60, 0, func(x, y int16) benchCmd { return benchScaled(x, y+150, 48, 48) })
			benchGrid(l, 5, 1, 60, 0, func(x, y int16) benchCmd { return benchDistorted(x+4, y+60, 0) })
			benchGrid(l, 3, 1, 100, 0, func(x, y int16) benchCmd { return benchDistortedStrong(x, y+120) })
			benchGrid(l, 3, 1, 100, 0, func(x, y int16) benchCmd { return benchDistorted(x+10, y+140, 3) })
			l.add(benchCmd{ctrl: 0x0008, xa: 10, ya: 10, xc: 300, yc: 200})
			l.add(benchPolygon(20, 20, 0x0400, 0x801F))
			l.add(benchPolygon(60, 40, 0x0400|3, 0x83E0))
			l.add(benchPolygon(100, 60, 0x0100, 0xFC00))
			l.add(benchPolygon(140, 80, 4, 0x8000))
			benchGrid(l, 5, 1, 60, 0, func(x, y int16) benchCmd { return benchPolyline(x+2, y+30) })
			for i := int16(0); i < 20; i++ {
				l.add(benchLineGouraud(i*16, 0, 319-i*16, 223))
			}
			l.end()
			return v
		}},
		{name: "erase full frame", build: func() *VDP1 {
			v, l := benchVDP1()
			v.Write(0x06, 0x8421)
			v.Write(0x08, 0x0000)
			v.Write(0x0A, 0x80FF)
			l.end()
			return v
		}},
	}
}

// benchVDP1Change runs a frame's VDP1 sequence up to the draw the
// way the frame loop does: the line record reset at the previous
// frame's end, the V-blank IN frame buffer change and erase, V-blank
// OUT, and the PTM=10 auto-trigger on the change.
func benchVDP1Change(v *VDP1) {
	v.restartList()
	v.VBlankIn()
	v.VBlankOut()
	if v.PTM() == 2 && v.ConsumeFBSwap() {
		v.StartAutoDraw()
	}
}

// benchVDP1Frame runs one frame's VDP1 sequence: the change and the
// draw in one call.
func benchVDP1Frame(v *VDP1) {
	benchVDP1Change(v)
	v.TickSystemCycles(benchVDP1Budget)
}

// benchVDP1FrameWalker runs the same frame sequence at the frame
// loop's cadence: one TickSystemCycles per scanline until the
// command processor goes idle.
func benchVDP1FrameWalker(tb testing.TB, v *VDP1) {
	benchVDP1Change(v)
	for i := 0; ; i++ {
		v.TickSystemCycles(benchVDP1WalkerTick)
		if !v.drawActive {
			return
		}
		if i >= benchVDP1WalkerMaxTicks {
			tb.Fatalf("command processor still active after %d ticks", i)
		}
	}
}

// benchVDP1GPU opens the GPU renderer for a scene's VDP1, the walk
// charging cycles without drawing as the frame loop runs it in GPU
// mode, on a VDP2 that renders no row: on the platform's backend,
// skipping without a device, or on the compute sources prog built on
// a test device. One frame change on the VDP2 gives the renderer its
// geometry; the frames after run the VDP1's points alone.
func benchVDP1GPU(tb testing.TB, v *VDP1, prog *hostgpu.Program) *gpuRenderer {
	tb.Helper()
	v2 := NewVDP2(NewSCU())
	var g *gpuRenderer
	var err error
	if prog == nil {
		if g, err = openGPURenderer(v2, v, defaultBackend()); err != nil {
			tb.Skip(err)
		}
	} else if g, err = newGPURenderer(v2, v, prog, gpuBatchLines, false); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(g.close)
	v.setPairKept(false)
	v2.BeginFrame()
	g.beginFrame()
	return g
}

// TestBenchVDP1GPUDrivers holds the gpu sub-benchmarks' drivers to
// the walk: two frames of each scene through each driver on the exec
// device leave the device pair equal to the pair two frames of the
// software driver leave.
func TestBenchVDP1GPUDrivers(t *testing.T) {
	parent, subs := ComputeSources()
	prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	drivers := []struct {
		name  string
		frame func(tb testing.TB, v *VDP1, g *gpuRenderer) (time.Duration, time.Duration)
	}{
		{"one call", func(_ testing.TB, v *VDP1, g *gpuRenderer) (time.Duration, time.Duration) {
			return benchVDP1GPUFrame(v, g)
		}},
		{"walker", benchVDP1GPUFrameWalker},
	}
	for _, s := range benchVDP1Scenes() {
		for _, d := range drivers {
			t.Run(s.name+"/"+d.name, func(t *testing.T) {
				want := s.build()
				benchVDP1Frame(want)
				benchVDP1Frame(want)
				v := s.build()
				g := benchVDP1GPU(t, v, prog)
				d.frame(t, v, g)
				d.frame(t, v, g)
				if err := g.err(); err != nil {
					t.Fatal(err)
				}
				for h := range g.vdp1FB {
					got, ref := deviceBytes(t, g, g.vdp1FB[h]), want.physicalFB(uint32(h))
					if !bytes.Equal(got, ref) {
						t.Errorf("buffer %d differs from the walk at byte %d", h, firstDiff(got, ref))
					}
				}
			})
		}
	}
}

// benchVDP1GPUEnd ends a frame on the GPU renderer as the frame loop
// does: the take of the remaining entries and the submission at the
// display's end, then the frame's wait. It returns the two times.
func benchVDP1GPUEnd(g *gpuRenderer) (take, wait time.Duration) {
	start := time.Now()
	g.endDisplay()
	take = time.Since(start)
	start = time.Now()
	g.endFrame()
	wait = time.Since(start)
	return take, wait
}

// benchVDP1GPUFrame runs one frame on the GPU renderer with the list
// walked in one call, then one take of its entries.
func benchVDP1GPUFrame(v *VDP1, g *gpuRenderer) (take, wait time.Duration) {
	benchVDP1Change(v)
	v.TickSystemCycles(benchVDP1Budget)
	start := time.Now()
	g.vdp1Line(0)
	take = time.Since(start)
	end, wait := benchVDP1GPUEnd(g)
	return take + end, wait
}

// benchVDP1GPUFrameWalker runs one frame on the GPU renderer at the
// frame loop's cadence: a tick per scanline, each followed by the
// renderer's take, until the command processor goes idle.
func benchVDP1GPUFrameWalker(tb testing.TB, v *VDP1, g *gpuRenderer) (take, wait time.Duration) {
	benchVDP1Change(v)
	for i := 0; ; i++ {
		v.TickSystemCycles(benchVDP1WalkerTick)
		start := time.Now()
		g.vdp1Line(i)
		take += time.Since(start)
		if !v.drawActive {
			break
		}
		if i >= benchVDP1WalkerMaxTicks {
			tb.Fatalf("command processor still active after %d ticks", i)
		}
	}
	end, wait := benchVDP1GPUEnd(g)
	return take + end, wait
}

// benchVDP1GPURun times b.N frames of frame on the GPU renderer and
// reports, beside ns/op, the takes and submissions on the walker and
// the frame's wait, per frame.
func benchVDP1GPURun(b *testing.B, g *gpuRenderer, frame func() (take, wait time.Duration)) {
	b.Helper()
	frame()
	if err := g.err(); err != nil {
		b.Fatal(err)
	}
	var take, wait time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t, w := frame()
		take += t
		wait += w
	}
	b.StopTimer()
	if err := g.err(); err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(take.Microseconds())/float64(b.N), "take-us/op")
	b.ReportMetric(float64(wait.Microseconds())/float64(b.N), "wait-us/op")
}

func BenchmarkVDP1(b *testing.B) {
	for _, s := range benchVDP1Scenes() {
		b.Run(s.name, func(b *testing.B) {
			b.Run("software", func(b *testing.B) {
				v := s.build()
				benchVDP1Frame(v)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchVDP1Frame(v)
				}
			})
			b.Run("gpu", func(b *testing.B) {
				v := s.build()
				g := benchVDP1GPU(b, v, nil)
				benchVDP1GPURun(b, g, func() (time.Duration, time.Duration) { return benchVDP1GPUFrame(v, g) })
			})
		})
	}
}

func BenchmarkVDP1Walker(b *testing.B) {
	for _, s := range benchVDP1Scenes() {
		b.Run(s.name, func(b *testing.B) {
			b.Run("software", func(b *testing.B) {
				v := s.build()
				benchVDP1FrameWalker(b, v)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchVDP1FrameWalker(b, v)
				}
			})
			b.Run("gpu", func(b *testing.B) {
				v := s.build()
				g := benchVDP1GPU(b, v, nil)
				benchVDP1GPURun(b, g, func() (time.Duration, time.Duration) { return benchVDP1GPUFrameWalker(b, v, g) })
			})
		})
	}
}

// benchVDP1Drivers is the two frame drivers the pin test runs every
// scene under, against the same hashes.
var benchVDP1Drivers = []struct {
	name  string
	frame func(tb testing.TB, v *VDP1)
}{
	{"one call", func(_ testing.TB, v *VDP1) { benchVDP1Frame(v) }},
	{"walker", benchVDP1FrameWalker},
}

// TestBenchVDP1Scenes pins the draw frame buffer of every benchmark
// scene after three frames (the third draws over the first frame's
// content of the same buffer, the steady state the destination-reading
// modes see in the benchmark loop) under both drivers and checks that
// each scene draws substantial content inside the system clip.
func TestBenchVDP1Scenes(t *testing.T) {
	for _, s := range benchVDP1Scenes() {
		for _, d := range benchVDP1Drivers {
			t.Run(s.name+"/"+d.name, func(t *testing.T) {
				v := s.build()
				d.frame(t, v)
				d.frame(t, v)
				d.frame(t, v)
				drawn := 0
				for y := 0; y < 224; y++ {
					for x := 0; x < 320; x++ {
						if readFBPixel(v, x, y) != 0 {
							drawn++
						}
					}
				}
				if drawn*10 < 320*224 {
					t.Errorf("only %d of %d pixels drawn", drawn, 320*224)
				}
				checkBands(t, s.name, vdp1BandHashes(v), benchVDP1Pins[s.name], describeVDP1Band(v))
			})
		}
	}
}

// benchVDP1Pins is the recorded band hash table per scene.
var benchVDP1Pins = map[string][]uint32{
	"normal sprite 8bpp": {
		0xA03039E1, 0x15181D5F, 0x0D516945, 0xFFA99CD1, 0x9DBCF70F, 0xBE759A6F, 0x9614EE95,
		0xE3BDB915, 0xC67D07AD, 0xCF1ADAA5, 0x14BE3755, 0xD870ACA3, 0x8C2ACBA7, 0xB26BEA15,
		0x9827E545, 0x48AA2FC7, 0x15181D5F, 0x0D516945, 0xFFA99CD1, 0x9DBCF70F, 0xBE759A6F,
		0x9614EE95, 0xE3BDB915, 0xC67D07AD, 0xCF1ADAA5, 0x14BE3755, 0x1CD20773, 0xBCC31DC5,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"normal sprite 4bpp CLUT": {
		0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5,
		0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95,
		0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5,
		0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95, 0x936E04E5, 0xD4DFFE95,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"normal sprite RGB gouraud": {
		0xF2F4A491, 0x2C2DC905, 0x8746931A, 0x66EBA226, 0x0AA7DFB0, 0xBE662A7D, 0x9395A0FA,
		0xBDA143CA, 0xF772F919, 0x8B54CC80, 0x083C1F7B, 0x318163FE, 0xEB3951DD, 0x6816B002,
		0xFFF9ECDE, 0x08937374, 0x2C2DC905, 0x8746931A, 0x66EBA226, 0x0AA7DFB0, 0xBE662A7D,
		0x9395A0FA, 0xBDA143CA, 0xF772F919, 0x8B54CC80, 0x083C1F7B, 0x72EABE94, 0xBCC31DC5,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"scaled sprite enlarge": {
		0xE878E211, 0xC7A4F86F, 0x37D0E3BB, 0xAEFA0CB7, 0xC9DE3C85, 0xDF7D44BF, 0x04DBFEEF,
		0x47964443, 0x5E4D14A7, 0x3F0D7CAF, 0x5656C08D, 0xF763B47D, 0xAEFA0CB7, 0xC9DE3C85,
		0xDF7D44BF, 0x04DBFEEF, 0x47964443, 0x5E4D14A7, 0x3F0D7CAF, 0x5656C08D, 0xF763B47D,
		0xAEFA0CB7, 0xC9DE3C85, 0xDF7D44BF, 0x04DBFEEF, 0x47964443, 0x5E4D14A7, 0x208E30D3,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"scaled sprite shrink": {
		0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5,
		0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD,
		0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5,
		0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD, 0x910ADDB5, 0x02EE96BD,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"distorted sprite": {
		0xCA52263C, 0xF7AC6370, 0xE992126B, 0x1A7B6D3C, 0xD2796435, 0x18921E8B, 0x8D910037,
		0xF24D7D3F, 0xA10A8416, 0x94D0954D, 0x0D2C49FA, 0x94AA4D4A, 0xA7FE18D0, 0xB0289EC0,
		0x1A7B6D3C, 0xD2796435, 0x18921E8B, 0x8D910037, 0xF24D7D3F, 0xA10A8416, 0x94D0954D,
		0x0D2C49FA, 0x94AA4D4A, 0xA7FE18D0, 0xB0289EC0, 0x1A7B6D3C, 0xD2796435, 0xAE8C234C,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"distorted sprite strong HSS": {
		0x52041123, 0x06221D34, 0x69BE0984, 0xCAA5706D, 0xC0A8EC4D, 0xBA7D749D, 0xFE891DBD,
		0x6537CC45, 0x4D41E525, 0x52041123, 0x06221D34, 0x69BE0984, 0xCAA5706D, 0xC0A8EC4D,
		0xBA7D749D, 0xFE891DBD, 0x6537CC45, 0x4D41E525, 0x52041123, 0x06221D34, 0x69BE0984,
		0xCAA5706D, 0xC0A8EC4D, 0xBA7D749D, 0xFE891DBD, 0x6537CC45, 0x4D41E525, 0xBCC31DC5,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"distorted sprite half-transparent": {
		0xCA52263C, 0xF7AC6370, 0xE992126B, 0x1A7B6D3C, 0xD2796435, 0x18921E8B, 0x8D910037,
		0xF24D7D3F, 0xA10A8416, 0x94D0954D, 0x0D2C49FA, 0x94AA4D4A, 0xA7FE18D0, 0xB0289EC0,
		0x1A7B6D3C, 0xD2796435, 0x18921E8B, 0x8D910037, 0xF24D7D3F, 0xA10A8416, 0x94D0954D,
		0x0D2C49FA, 0x94AA4D4A, 0xA7FE18D0, 0xB0289EC0, 0x1A7B6D3C, 0xD2796435, 0xAE8C234C,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"distorted sprite converging half-transparent": {
		0x9E7F3C8D, 0x4AA2D866, 0x7ED4BF7D, 0xDDCFE08C, 0xAA871505, 0xB1290795, 0xF4137E13,
		0x7DB972F2, 0xC1B3026F, 0x800DCDE5, 0xA9346255, 0x7C2CC302, 0x3AAA0710, 0x69DBDC39,
		0xDDCFE08C, 0xAA871505, 0xB1290795, 0xF4137E13, 0x7DB972F2, 0xC1B3026F, 0x800DCDE5,
		0xA9346255, 0x7C2CC302, 0x3AAA0710, 0x69DBDC39, 0xDDCFE08C, 0xAA871505, 0x0C8A3325,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon": {
		0xBCC31DC5, 0x9355692B, 0x0AB1A5EB, 0x5C8B91C5, 0x5C8B91C5, 0xD3F93585, 0x397DD5A0,
		0x527F73A4, 0xA8B42C55, 0xA8B42C55, 0xC5914A19, 0x14BB280E, 0xBB17681A, 0xA8365DC5,
		0xA8365DC5, 0xE0D05AC5, 0x55C35420, 0x45F8BA74, 0xBFBB9635, 0xBFBB9635, 0xA3067489,
		0xC088C4C9, 0xE36E40C5, 0x3672B3F0, 0x4C6B95E5, 0xBD723F91, 0x79703AFD, 0x68B0BE3E,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon half-transparent": {
		0xBCC31DC5, 0x4CACECA9, 0xA0A03CB9, 0x2B89A0A5, 0x976CEC71, 0xEB788D18, 0x22C29D96,
		0x097B8E9D, 0xB5E88AA3, 0xC068F57A, 0x1293CA01, 0x7F3DFE64, 0x48D388FF, 0xBDD29C8E,
		0xDD814E14, 0x99117B90, 0x7DE09236, 0xAF431FDF, 0x6F1BF952, 0xC02F676F, 0x958EA7E1,
		0x87B755B2, 0x3BC0B84F, 0xD273FB44, 0x1233883D, 0xC5459560, 0x4CC70FC4, 0xE9D5AE56,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon mesh": {
		0xBCC31DC5, 0x16659605, 0x8EC7CE6B, 0x960BFFC5, 0x960BFFC5, 0xBFF050AB, 0xBF981EFB,
		0x52774786, 0x709EE84D, 0x709EE84D, 0xBA32EA66, 0x64AC94D2, 0x9CEE3D15, 0xE4E689C5,
		0xE4E689C5, 0xDF9154E1, 0xCFD0A451, 0xBE7BE79C, 0xD0FFFDDD, 0xD0FFFDDD, 0x174B01DC,
		0x15654FA8, 0xD34E14E5, 0x8EBDFE54, 0x32448745, 0xB00FC3BC, 0x3B346D51, 0xB1EE8558,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon gouraud": {
		0xBCC31DC5, 0xF338FC4E, 0xAB85A579, 0x9822A717, 0x4BCA0580, 0xDA7454A8, 0xF605CBDF,
		0xA15761D5, 0x298BEA1F, 0x9D3823B4, 0x5CEC2EA5, 0x2DE2511B, 0x025D37EA, 0x937EAB4F,
		0x8506A0EC, 0x999C6F63, 0xE8DA9037, 0x6CBEEFBE, 0xAC42DC63, 0x09DC76DC, 0xDF1AF125,
		0x125954C1, 0x88E64E67, 0x2B7F5444, 0xB84F26BD, 0x37C87800, 0xF7142DE8, 0x558ACCD2,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polyline and line gouraud": {
		0xCA4A1D9F, 0xACE94846, 0x6FBCFE74, 0x6058ADF9, 0x6D15A4EB, 0x229419DE, 0x5B5A11C9,
		0xA6774795, 0xAED89981, 0xD8FE4D8E, 0x961E410B, 0xE866CEF7, 0x490A15E3, 0xD06712BF,
		0x01C769F7, 0xAE46B0AF, 0x4666EB1D, 0x4A55C2B3, 0x24692E7C, 0x9C10F261, 0x6E7AA14D,
		0x32C5D655, 0xCBE2E019, 0x1752AE77, 0x282F56F1, 0x8B16A802, 0x5061C724, 0xBAAA7191,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"mixed": {
		0x03604CE1, 0x5AACB9A1, 0x9AC595DC, 0x28B42BC1, 0xB9A9706B, 0xCE7B1F26, 0x9623046A,
		0xD46B96E9, 0x97CACBE5, 0xB319AEE8, 0x15E7919C, 0x2E61457B, 0xF0188DB0, 0x64C71E15,
		0xE74165D7, 0x1BA32CA8, 0xC69F4F5B, 0xAE3BC474, 0xDA98EE69, 0x40ED05C4, 0x2667680C,
		0x4F31B7DB, 0x556FFA04, 0x53E2AEED, 0x3AEE9033, 0xCFA13BC7, 0x8DC28CCC, 0xD1526EFF,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"erase full frame": {
		0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5,
		0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5,
		0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5,
		0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5,
		0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5, 0x4A2E1DC5,
	},
}
