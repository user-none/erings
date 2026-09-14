// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// checkErased checks that target holds EWDR inside the inclusive
// rectangle, in the VDP1's format, and zero outside it.
func checkErased(t *testing.T, v *VDP1, target []byte, x1, y1, x3, y3 int32) {
	t.Helper()
	w, h := int32(v.fbWidth()), int32(v.fbHeight())
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			inside := x >= x1 && x <= x3 && y >= y1 && y <= y3
			if v.is8bpp() {
				want := uint8(0)
				if inside {
					want = uint8(v.ewdr >> 8)
					if x&1 == 1 {
						want = uint8(v.ewdr)
					}
				}
				if got := target[y*w+x]; got != want {
					t.Fatalf("(%d,%d) = %#02x, want %#02x", x, y, got, want)
				}
				continue
			}
			want := uint16(0)
			if inside {
				want = v.ewdr
			}
			off := (y*w + x) * 2
			if got := uint16(target[off])<<8 | uint16(target[off+1]); got != want {
				t.Fatalf("(%d,%d) = %#04x, want %#04x", x, y, got, want)
			}
		}
	}
}

// checkUntouched checks that target holds no erase.
func checkUntouched(t *testing.T, name string, target []byte) {
	t.Helper()
	for i, b := range target {
		if b != 0 {
			t.Fatalf("%s: byte %#x = %#02x, want the buffer untouched", name, i, b)
		}
	}
}

// eraseTestVDP1 is a VDP1 with EWDR 0x1234 over the rectangle (0,0)
// to (319,255), the erase every site below performs.
func eraseTestVDP1() *VDP1 {
	v := NewVDP1(NewSCU())
	v.Write(0x06, 0x1234)
	v.Write(0x08, 0x0000)
	v.Write(0x0A, 0x50FF)
	return v
}

// TestVDP1EraseSites checks that every erase site erases the buffer
// it targets: the 1-cycle change, the V-blank erase of a manual
// change, the latched erase request after a manual change at V-blank
// IN and at V-blank OUT, the late erase of manual mode, and the
// double interlace erase of the DIL-matched buffer.
func TestVDP1EraseSites(t *testing.T) {
	t.Run("1-cycle change", func(t *testing.T) {
		v := eraseTestVDP1()
		before := v.displayFB
		v.VBlankIn()
		if &v.drawFB[0] != &before[0] {
			t.Errorf("the change did not swap before the erase")
		}
		checkErased(t, v, v.drawFB, 0, 0, 319, 255)
	})
	t.Run("VBE manual change", func(t *testing.T) {
		v := eraseTestVDP1()
		v.Write(0x00, 0x0008)
		v.Write(0x02, 0x0003)
		v.VBlankIn()
		// The display buffer is erased ahead of the swap, so the
		// erased buffer is the draw buffer after it.
		checkErased(t, v, v.drawFB, 0, 0, 319, 255)
	})
	t.Run("latched request at V-blank IN", func(t *testing.T) {
		v := eraseTestVDP1()
		v.Write(0x02, 0x0000)
		v.Write(0x02, 0x0003)
		v.VBlankIn()
		// The latched request erases the buffer the change brings
		// in as the display buffer.
		checkErased(t, v, v.displayFB, 0, 0, 319, 255)
	})
	t.Run("latched request at V-blank OUT", func(t *testing.T) {
		v := eraseTestVDP1()
		v.Write(0x02, 0x0002)
		v.VBlankIn()
		checkUntouched(t, "manual mode without a change at V-blank IN, draw buffer", v.drawFB)
		checkUntouched(t, "manual mode without a change at V-blank IN, display buffer", v.displayFB)
		v.Write(0x02, 0x0000)
		v.Write(0x02, 0x0003)
		v.VBlankOut()
		// The FCT=0 write armed a late erase of the display buffer
		// at V-blank IN, which the swap performs first; the erased
		// buffer is the draw buffer after the swap. The latched
		// request then erases the buffer the swap brought in.
		checkErased(t, v, v.drawFB, 0, 0, 319, 255)
		checkErased(t, v, v.displayFB, 0, 0, 319, 255)
	})
	t.Run("late erase", func(t *testing.T) {
		v := eraseTestVDP1()
		v.Write(0x02, 0x0002)
		v.Write(0x02, 0x0002)
		v.VBlankIn()
		checkUntouched(t, "the late erase at V-blank IN, draw buffer", v.drawFB)
		checkUntouched(t, "the late erase at V-blank IN, display buffer", v.displayFB)
		target := v.displayFB
		v.PerformLateErase()
		checkErased(t, v, target, 0, 0, 319, 255)
	})
	t.Run("double interlace", func(t *testing.T) {
		v := eraseTestVDP1()
		v.Write(0x02, 0x0008)
		v.VBlankIn()
		checkErased(t, v, v.drawFB, 0, 0, 319, 255)
		checkUntouched(t, "DIL even, display buffer", v.displayFB)
		v.Write(0x02, 0x000C)
		v.VBlankIn()
		checkErased(t, v, v.displayFB, 0, 0, 319, 255)
	})
}

// TestVDP1EraseRectangle checks the erase rectangle for the
// degenerate single-dot region, the 8-dot run of a rotation mode, the
// clamp to the buffer, a region below the buffer, and the 8-bit
// format's parity fill.
func TestVDP1EraseRectangle(t *testing.T) {
	t.Run("degenerate dot", func(t *testing.T) {
		v := NewVDP1(NewSCU())
		v.Write(0x06, 0x1234)
		v.Write(0x08, 10<<9|7)
		v.Write(0x0A, 5<<9|3)
		v.VBlankIn()
		checkErased(t, v, v.drawFB, 80, 7, 80, 7)
	})
	t.Run("degenerate run in rotation mode", func(t *testing.T) {
		v := NewVDP1(NewSCU())
		v.Write(0x00, 0x0002)
		v.Write(0x06, 0x1234)
		v.Write(0x08, 10<<9|7)
		v.Write(0x0A, 5<<9|3)
		v.VBlankIn()
		checkErased(t, v, v.drawFB, 80, 7, 87, 7)
	})
	t.Run("clamped to the buffer", func(t *testing.T) {
		v := NewVDP1(NewSCU())
		v.Write(0x06, 0x1234)
		v.Write(0x08, 0x0000)
		v.Write(0x0A, 0x7F<<9|0x1FF)
		v.VBlankIn()
		checkErased(t, v, v.drawFB, 0, 0, 511, 255)
	})
	t.Run("row past the buffer", func(t *testing.T) {
		v := NewVDP1(NewSCU())
		v.Write(0x06, 0x1234)
		v.Write(0x08, 300)
		v.Write(0x0A, 0x40<<9|400)
		v.VBlankIn()
		checkUntouched(t, "a region below the buffer", v.drawFB)
	})
	t.Run("8-bit parity", func(t *testing.T) {
		v := NewVDP1(NewSCU())
		v.Write(0x00, 0x0001)
		v.Write(0x06, 0xAABB)
		v.Write(0x08, 0x0000)
		v.Write(0x0A, 2<<9|2)
		v.VBlankIn()
		checkErased(t, v, v.drawFB, 0, 0, 31, 2)
	})
}
