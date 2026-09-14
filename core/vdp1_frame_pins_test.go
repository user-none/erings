package core

import (
	"fmt"
	"strings"
	"testing"
)

// vdp1BandHashes hashes the VDP1 draw framebuffer (16bpp, 512 wide) in
// bands of goldenBandRows rows over the first 256 rows.
func vdp1BandHashes(v *VDP1) []uint32 {
	var out []uint32
	for y0 := 0; y0 < 256; y0 += goldenBandRows {
		hash := uint32(2166136261)
		for y := y0; y < y0+goldenBandRows; y++ {
			hash = fnv1a32(hash, v.drawFB[y*vdp1FBStride*2:(y+1)*vdp1FBStride*2])
		}
		out = append(out, hash)
	}
	return out
}

// describeVDP1Band returns the first row of the band as run-length
// encoded pixel values.
func describeVDP1Band(v *VDP1) func(band int) string {
	return func(band int) string {
		y := band * goldenBandRows
		var sb strings.Builder
		fmt.Fprintf(&sb, "row %d: ", y)
		run := 0
		var prev uint16
		flush := func() {
			if run > 0 {
				fmt.Fprintf(&sb, "%dx%04X ", run, prev)
			}
		}
		for x := 0; x < vdp1FBStride; x++ {
			p := readFBPixel(v, x, y)
			if run > 0 && p == prev {
				run++
				continue
			}
			flush()
			prev, run = p, 1
		}
		flush()
		return sb.String()
	}
}

// goldenVDP1CommandList draws one of every command type with the
// features the accuracy renderer must keep: Gouraud RGB normal sprite,
// center-zoomed scaled sprite, HSS distorted sprite, half-transparent
// polygon over an opaque fill, mesh polyline, plain line, user clip, and
// an end-coded sprite.
func goldenVDP1CommandList(t *testing.T) *VDP1 {
	v := newDrawTestVDP1()
	grda := writeGouraudTable(v, 0x4210, 0x421F, 0x5E10, 0x4210)
	// 8x8 256-color texture with an end code (0xFF) at column 6.
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			d := uint8(0x10 + y*8 + x)
			if x == 6 {
				d = 0xFF
			}
			v.WriteVRAM(0x1000+uint32(y*8+x), d)
		}
	}
	writeRGBTexture(v, 0x3000, 8, 8, 0x83E0)

	// 0x00: user clip (10,10)-(200,150).
	writeCmd16(v, 0x00, 0x0008)
	writeCmd16(v, 0x14, 10)
	writeCmd16(v, 0x16, 10)
	writeCmd16(v, 0x34, 200)
	writeCmd16(v, 0x36, 150)
	// 0x40: half-transparent polygon filling a region for the RMW draws.
	writePolygon(v, 0x40, 20, 20, 120, 20, 120, 120, 20, 120, 0xFFFF)
	// 0x60: polygon, half-transparent, over the fill.
	writePolygon(v, 0x60, 40, 30, 100, 40, 90, 110, 30, 100, 0x801F)
	writeCmd16(v, 0x64, 0x0003)
	// 0x80: normal sprite, RGB texture with Gouraud.
	writeCmd16(v, 0x80, 0x0000)
	writeCmd16(v, 0x84, 5<<3|0x0004)
	writeCmd16(v, 0x88, 0x3000/8)
	writeCmd16(v, 0x8A, 0x0108)
	writeCmd16(v, 0x8C, 130)
	writeCmd16(v, 0x8E, 20)
	writeCmd16(v, 0x9C, grda)
	// 0xA0: scaled sprite, center zoom point, 24x16 display.
	writeCmd16(v, 0xA0, 0x0001|0x0A00)
	writeCmd16(v, 0xA4, 0x0020)
	writeCmd16(v, 0xA6, 0x0100)
	writeCmd16(v, 0xA8, 0x1000/8)
	writeCmd16(v, 0xAA, 0x0108)
	writeCmd16(v, 0xAC, 160)
	writeCmd16(v, 0xAE, 40)
	writeCmd16(v, 0xB0, 24)
	writeCmd16(v, 0xB2, 16)
	// 0xC0: distorted sprite with HSS, user clip inside mode.
	writeDistortedSprite(v, 0xC0, 130, 60, 190, 64, 180, 100, 140, 96, 4, 0x0100, 0x1000, 8, 8)
	writeCmd16(v, 0xC4, 4<<3|0x1000|0x0600)
	// 0xE0: mesh polyline.
	writePolyline(v, 0xE0, 200, 20, 300, 20, 300, 120, 200, 120, 0x83FF)
	writeCmd16(v, 0xE4, 0x0100)
	// 0x100: line.
	writeCmd16(v, 0x100, 0x0006)
	writeCmd16(v, 0x104, 0x0000)
	writeCmd16(v, 0x106, 0xFC00)
	writeCmd16(v, 0x10C, 5)
	writeCmd16(v, 0x10E, 200)
	writeCmd16(v, 0x110, 310)
	writeCmd16(v, 0x112, 130)
	// 0x120: normal sprite with end codes enabled at the clip's edge.
	writeCmd16(v, 0x120, 0x0000)
	writeCmd16(v, 0x124, 0x0020)
	writeCmd16(v, 0x126, 0x0100)
	writeCmd16(v, 0x128, 0x1000/8)
	writeCmd16(v, 0x12A, 0x0108)
	writeCmd16(v, 0x12C, 196)
	writeCmd16(v, 0x12E, 146)
	writeDrawEnd(v, 0x140)
	v.VBlankIn()
	drainDrawing(v)
	return v
}

// TestGoldenVDP1CommandList pins the VDP1 renderer's output for a command
// list exercising every command type.
func TestGoldenVDP1CommandList(t *testing.T) {
	v := goldenVDP1CommandList(t)
	want := []uint32{
		0xBCC31DC5, 0xBCC31DC5, 0xFC4B2920, 0x955C1FAB, 0xC8063210, 0x7BABC791, 0xD4D39B58, 0xCBE8B13F,
		0xAE50B3AF, 0xD429E382, 0x2FBE23B3, 0x6E95B1A7, 0x80771C8E, 0x5D148F60, 0xD1619CA5, 0xD62AA2A9,
		0x6375BE05, 0x23C13F09, 0xCDBBDDBD, 0xBFA39A7D, 0x8BB59A39, 0xA62103C9, 0xF00EBAD9, 0x8BFA5875,
		0xFCDE2DC9, 0xFF381119, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5, 0xBCC31DC5,
	}
	checkBands(t, "VDP1 command list", vdp1BandHashes(v), want, describeVDP1Band(v))
}
