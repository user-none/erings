// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// VDP1 draw benchmarks. Every scene is a command list written to VRAM
// through the bus-facing entry points and drawn through the frame
// loop's sequence: VBlankIn (frame buffer change), VBlankOut, the
// PTM=10 auto-trigger, and TickSystemCycles with a budget that covers
// the whole list. Nothing here names a renderer-internal function, so
// the numbers stay comparable across refactors of the draw path.
//
// ns/op is the cost of drawing the scene's list once. The lists are
// fixed, so the unit of work is fixed by the list geometry.
//
// TestBenchVDP1Scenes pins every scene's output. A benchmark number is
// comparable with an earlier one only while that test passes: a scene
// whose output changed measures different work.

// benchVDP1Budget covers any list in one TickSystemCycles call.
const benchVDP1Budget = 1 << 30

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

// benchVDP1Frame runs one frame's VDP1 sequence the way the frame loop
// does: the V-blank IN frame buffer change and erase, V-blank OUT, the
// PTM=10 auto-trigger on the change, and the draw.
func benchVDP1Frame(v *VDP1) {
	v.VBlankIn()
	v.VBlankOut()
	if v.PTM() == 2 && v.ConsumeFBSwap() {
		v.StartAutoDraw()
	}
	v.TickSystemCycles(benchVDP1Budget)
}

func BenchmarkVDP1(b *testing.B) {
	for _, s := range benchVDP1Scenes() {
		b.Run(s.name, func(b *testing.B) {
			v := s.build()
			benchVDP1Frame(v)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchVDP1Frame(v)
			}
		})
	}
}

// TestBenchVDP1Scenes pins the draw frame buffer of every benchmark
// scene after three frames (the third draws over the first frame's
// content of the same buffer, the steady state the destination-reading
// modes see in the benchmark loop) and checks that each scene draws
// substantial content inside the system clip.
func TestBenchVDP1Scenes(t *testing.T) {
	for _, s := range benchVDP1Scenes() {
		t.Run(s.name, func(t *testing.T) {
			v := s.build()
			benchVDP1Frame(v)
			benchVDP1Frame(v)
			benchVDP1Frame(v)
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
		0x9350575D, 0x7A42D905, 0xDEA4895D, 0x59F17262, 0x00EF2D50, 0x3EBD9B96, 0xE41F5BF8,
		0x71B42508, 0xFC4F6D5C, 0x089B8C0A, 0xCCC0A54F, 0xBE48CD5B, 0x448A4D14, 0x0180999F,
		0x59F17262, 0x00EF2D50, 0x3EBD9B96, 0xE41F5BF8, 0x71B42508, 0xFC4F6D5C, 0x089B8C0A,
		0xCCC0A54F, 0xBE48CD5B, 0x448A4D14, 0x0180999F, 0x59F17262, 0x00EF2D50, 0x7ACB1F80,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"distorted sprite strong HSS": {
		0x87A93CA7, 0xF5CA0067, 0xFE515718, 0xF1C977AD, 0x2BD0D875, 0x54E9AC15, 0x5F53ED55,
		0x92CA0C25, 0x4D41E525, 0x87A93CA7, 0xF5CA0067, 0xFE515718, 0xF1C977AD, 0x2BD0D875,
		0x54E9AC15, 0x5F53ED55, 0x92CA0C25, 0x4D41E525, 0x87A93CA7, 0xF5CA0067, 0xFE515718,
		0xF1C977AD, 0x2BD0D875, 0x54E9AC15, 0x5F53ED55, 0x92CA0C25, 0x4D41E525, 0xBCC31DC5,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"distorted sprite half-transparent": {
		0x9350575D, 0x7A42D905, 0xDEA4895D, 0x59F17262, 0x00EF2D50, 0x3EBD9B96, 0xE41F5BF8,
		0x71B42508, 0xFC4F6D5C, 0x089B8C0A, 0xCCC0A54F, 0xBE48CD5B, 0x448A4D14, 0x0180999F,
		0x59F17262, 0x00EF2D50, 0x3EBD9B96, 0xE41F5BF8, 0x71B42508, 0xFC4F6D5C, 0x089B8C0A,
		0xCCC0A54F, 0xBE48CD5B, 0x448A4D14, 0x0180999F, 0x59F17262, 0x00EF2D50, 0x7ACB1F80,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon": {
		0xBCC31DC5, 0x3E04232B, 0x0E6849C5, 0x77D6ADC5, 0x0E26D0A5, 0x16579C8B, 0xC9C66498,
		0x01DB6E75, 0x7C6C6F85, 0x42AA6C19, 0xF1F4CEDE, 0xDA736E76, 0x05714951, 0x7F98C0C5,
		0xD831FEA5, 0x8C3DC6E1, 0xB010D3D8, 0xA4787FB5, 0x0F944005, 0xCDC8F729, 0x12BD1A34,
		0x5965DCD5, 0x5B30E5D9, 0x6F1C0035, 0xAA65589C, 0xA0C9F755, 0xF626EFE8, 0x658066F3,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon half-transparent": {
		0xBCC31DC5, 0xAAED15C9, 0x13EBCDEA, 0xDB1797C5, 0xFF7C14BD, 0x0B054514, 0x9A2D7904,
		0x62B09B48, 0x4CFB9E60, 0xA6F21CD8, 0xEB8B9705, 0xFE208EE3, 0xCD1D4D30, 0xB36D6105,
		0x9FC474DE, 0xD1A0E212, 0xB98FF885, 0xB67B7CA3, 0x0C0CEA05, 0xD9B2E21B, 0xEDCE23C5,
		0x2BFF05D1, 0xD9866BFD, 0x2D1F836D, 0x66CA68EF, 0x1EAB0F6B, 0x59BE060F, 0x5CC909E4,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon mesh": {
		0xBCC31DC5, 0xBCD1F1C5, 0x13333105, 0xAFA4E3C5, 0x1097766B, 0x22740B45, 0x61F1B163,
		0xA90CE537, 0x21E514A5, 0x9F06013E, 0x146B9AC5, 0x93DDDE72, 0xA755A576, 0xE1990445,
		0x07C62881, 0x231F43C5, 0xDBD94259, 0x0A57487D, 0x580E6DE5, 0x7E8B4454, 0x7D78A2C5,
		0x91AC345D, 0xE03A0A19, 0x21039AED, 0xD6A44509, 0x62EDAC1D, 0x8E48E0A5, 0x5942A5C4,
		0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	},
	"polygon gouraud": {
		0xBCC31DC5, 0x8CC94923, 0x1FF1EA02, 0x47EFBC64, 0xD43F94B6, 0x82386620, 0x74EA330C,
		0x4CB931F6, 0xFDF8AC9C, 0x162CF966, 0x58E6D119, 0xFFD6CE82, 0x7371640E, 0xEE3CB068,
		0xE6563DAA, 0xD0BC8486, 0x66EF4A78, 0xD2EBD4E2, 0x7AB0B360, 0x2F620022, 0x4E89D0A7,
		0xA385DC3E, 0x00956E5B, 0x21B16138, 0x4CED5B7D, 0x52A6265D, 0x90A4374F, 0x5AE5500B,
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
		0x03604CE1, 0x5AACB9A1, 0x1B69C7D9, 0x419D87E6, 0x55AD7F4E, 0xAE3DA81E, 0x00F98687,
		0xCE76BAAC, 0x1B1C9C8A, 0x7EBD6645, 0x083E9410, 0x8A5EC2C8, 0x11B15C40, 0xE3DF5DBA,
		0xAA220744, 0xB6D06E6A, 0x9301217C, 0x84995CA9, 0x8877512B, 0x42E29F31, 0xCAAE8914,
		0x0DEB6A5E, 0x2BA606AA, 0xB41834B3, 0x131C86DF, 0x7AB5E31B, 0xBE0CAA74, 0x84CC342F,
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
