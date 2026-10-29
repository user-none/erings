// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import (
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestSpritePixelRotatedDoubled holds the rotated read's row in a
// doubled buffer: the coordinate lands on a physical row by its
// integer part, whatever its fraction, and the read takes that row's
// doubled row of the read's parity, the scaled sub-row kept; a native
// buffer reads the physical row itself.
func TestSpritePixelRotatedDoubled(t *testing.T) {
	const width = 4
	for _, tc := range []struct {
		name    string
		shift   uint32
		doubled uint32
		parity  uint32
		baseY   int64 // .10 at native, .10 + shift at scale
		height  int32 // physical rows at the scale
		rows    int32 // rows the buffer holds at the scale
		want    int32 // the row read
	}{
		{"native row 0", 0, 0, 0, 0 << 10, 2, 2, 0},
		{"native fraction stays on its row", 0, 0, 0, 1<<10 + 512, 2, 2, 1},
		{"doubled parity 0", 0, 1, 0, 0 << 10, 2, 4, 0},
		{"doubled parity 1", 0, 1, 1, 0 << 10, 2, 4, 1},
		{"doubled fraction above a half keeps the parity", 0, 1, 1, 512, 2, 4, 1},
		{"doubled second physical row", 0, 1, 0, 1<<10 + 700, 2, 4, 2},
		{"doubled at scale sub-row 1", 1, 1, 1, 1 << 10, 4, 8, 3},
		{"doubled at scale second physical row sub-row 0", 1, 1, 0, 2 << 10, 4, 8, 4},
	} {
		fb := make(compute.VDP1FB, int(tc.rows)*width*2)
		for row := int32(0); row < tc.rows; row++ {
			fb.PutU16BE(uint32(row*width)*2, uint16(0x100+row))
		}
		r := SpriteRow{Mode: SpriteReadRotated, Width: width, Height: tc.height, Shift: tc.shift, Doubled: tc.doubled, Parity: tc.parity, BaseY: tc.baseY}
		if got := SpritePixelRotated(fb, &r, 0); got != uint32(0x100+tc.want) {
			t.Errorf("%s: read row %d, want %d", tc.name, got-0x100, tc.want)
		}
	}
	// Past the physical rows the read is outside the buffer, doubled or not.
	fb := make(compute.VDP1FB, 4*width*2)
	r := SpriteRow{Mode: SpriteReadRotated, Width: width, Height: 2, Doubled: 1, BaseY: 2 << 10}
	if got := SpritePixelRotated(fb, &r, 0); got != 0 {
		t.Errorf("past the physical rows: read %#x", got)
	}
}
