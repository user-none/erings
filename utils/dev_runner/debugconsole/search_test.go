// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugconsole

import (
	"math/bits"
	"strings"
	"testing"
)

func bitmapCount(bm []uint64) int {
	n := 0
	for _, w := range bm {
		n += bits.OnesCount64(w)
	}
	return n
}

func TestNewBitmap(t *testing.T) {
	for _, n := range []int{1, 63, 64, 65, 100, 128} {
		bm := newBitmap(n)
		if got := bitmapCount(bm); got != n {
			t.Errorf("newBitmap(%d): %d bits set", n, got)
		}
		// No bit beyond n may be set.
		set := 0
		forEachBit(bm, func(idx int) bool {
			if idx >= n {
				t.Errorf("newBitmap(%d): stray bit %d", n, idx)
			}
			set++
			return true
		})
		if set != n {
			t.Errorf("newBitmap(%d): forEachBit visited %d", n, set)
		}
	}
}

func TestForEachBitStops(t *testing.T) {
	bm := newBitmap(100)
	visited := 0
	forEachBit(bm, func(idx int) bool {
		visited++
		return visited < 5
	})
	if visited != 5 {
		t.Errorf("visited %d, want 5", visited)
	}
}

func TestFilterBitmap8(t *testing.T) {
	// The baseline is 8 byte values 10,20,30,40,50,60,70,80.
	base := []byte{10, 20, 30, 40, 50, 60, 70, 80}
	cur := []byte{9, 20, 31, 40, 50, 60, 70, 80} // idx0 dec, idx2 inc

	bm := newBitmap(8)
	n := filterBitmap(bm, base, cur, 1, func(p, c uint32) bool { return c < p })
	if n != 1 || bitmapCount(bm) != 1 {
		t.Fatalf("dec: n=%d bits=%d", n, bitmapCount(bm))
	}
	forEachBit(bm, func(idx int) bool {
		if idx != 0 {
			t.Errorf("dec survivor at %d, want 0", idx)
		}
		return true
	})

	bm = newBitmap(8)
	if n := filterBitmap(bm, base, cur, 1, func(p, c uint32) bool { return c == p }); n != 6 {
		t.Fatalf("same: n=%d, want 6", n)
	}

	// Chained filters intersect. diff leaves idx0 and idx2. A constant
	// compare against value 9 then leaves idx0 only.
	bm = newBitmap(8)
	filterBitmap(bm, base, cur, 1, func(p, c uint32) bool { return c != p })
	n = filterBitmap(bm, cur, cur, 1, func(p, c uint32) bool { return c == 9 })
	if n != 1 {
		t.Fatalf("chained: n=%d, want 1", n)
	}
}

func TestFilterBitmap16BigEndian(t *testing.T) {
	// The buffers hold two 16-bit values. 0x0100 -> 0x00FF is a
	// decrease only when read big-endian (little-endian would read
	// 0x0001 -> 0xFF00, an increase). The second value is unchanged.
	base := []byte{0x01, 0x00, 0x12, 0x34}
	cur := []byte{0x00, 0xFF, 0x12, 0x34}
	bm := newBitmap(2)
	n := filterBitmap(bm, base, cur, 2, func(p, c uint32) bool { return c < p })
	if n != 1 {
		t.Fatalf("n=%d, want 1", n)
	}
	forEachBit(bm, func(idx int) bool {
		if idx != 0 {
			t.Errorf("survivor at %d, want 0", idx)
		}
		return true
	})
}

func TestFilterBitmap32(t *testing.T) {
	base := []byte{0x00, 0x00, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF}
	cur := []byte{0x00, 0x00, 0x00, 0x02, 0xFF, 0xFF, 0xFF, 0xFF}
	bm := newBitmap(2)
	if n := filterBitmap(bm, base, cur, 4, func(p, c uint32) bool { return c > p }); n != 1 {
		t.Fatalf("inc: n=%d, want 1", n)
	}
}

func TestFilterOnlySurvivorsAreTested(t *testing.T) {
	// A cleared bit must stay cleared even if its value would now pass.
	base := []byte{5, 5}
	cur1 := []byte{5, 4}
	bm := newBitmap(2)
	filterBitmap(bm, base, cur1, 1, func(p, c uint32) bool { return c < p }) // idx1 survives
	cur2 := []byte{4, 4}
	n := filterBitmap(bm, cur1, cur2, 1, func(p, c uint32) bool { return c < p })
	if n != 0 {
		t.Fatalf("n=%d, want 0 (idx0 decreased but was already eliminated)", n)
	}
}

func TestWidthMax(t *testing.T) {
	if widthMax(8) != 0xFF || widthMax(16) != 0xFFFF || widthMax(32) != 0xFFFFFFFF {
		t.Fatalf("widthMax wrong: %X %X %X", widthMax(8), widthMax(16), widthMax(32))
	}
}

func TestSearchCommandValidation(t *testing.T) {
	c := newTestConsole()
	// No active search.
	for _, cmd := range []string{"filter dec", "list"} {
		if r := runLine(t, c, cmd); !strings.Contains(r, "no search active") {
			t.Fatalf("%q: unexpected response %q", cmd, r)
		}
	}
	if r := runLine(t, c, "reset"); r != "no search active" {
		t.Fatalf("reset response %q", r)
	}
	// Bad region names fail before any memory access.
	if r := runLine(t, c, "baseline bogus"); !strings.Contains(r, "unknown region") {
		t.Fatalf("baseline response %q", r)
	}
	if r := runLine(t, c, "baseline wraml wraml"); !strings.Contains(r, "listed twice") {
		t.Fatalf("baseline response %q", r)
	}

	// Operator/argument validation against an empty active search.
	c.search = &search{width: 8}
	for _, bad := range []string{
		"filter",
		"filter bogus",
		"filter eq",
		"filter dec 5",
		"filter eq 5 6",
		"filter eq 300",
		"filter eq xyz",
	} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if r := runLine(t, c, "filter eq 255"); r != "0 -> 0 candidates" {
		t.Fatalf("empty-search filter response %q", r)
	}
	if r := runLine(t, c, "list"); r != "0 candidates" {
		t.Fatalf("empty-search list response %q", r)
	}
	if r := runLine(t, c, "reset"); r != "search reset" {
		t.Fatalf("reset response %q", r)
	}
	if c.search != nil {
		t.Fatal("reset did not clear the search")
	}
}

func TestWidthCommand(t *testing.T) {
	c := newTestConsole()
	if r := runLine(t, c, "width"); r != "width 8" {
		t.Fatalf("default width response %q", r)
	}
	if r := runLine(t, c, "width 16"); r != "width 16" {
		t.Fatalf("set width response %q", r)
	}
	if r := runLine(t, c, "width"); r != "width 16" {
		t.Fatalf("width after set response %q", r)
	}
	for _, bad := range []string{"width 12", "width x", "width 8 16"} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	// Changing width discards an active search. Re-setting the same
	// width does not.
	c.search = &search{width: 16}
	if r := runLine(t, c, "width 16"); r != "width 16" || c.search == nil {
		t.Fatalf("same-width set: %q, search=%v", r, c.search)
	}
	if r := runLine(t, c, "width 32"); r != "width 32 (search reset)" || c.search != nil {
		t.Fatalf("width change: %q, search=%v", r, c.search)
	}
}

func TestListValidation(t *testing.T) {
	c := newTestConsole()
	c.search = &search{width: 8}
	for _, bad := range []string{"list 0", "list -1", "list 1001", "list x", "list 5 x", "list 5 -1", "list 5 6 7"} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
}
