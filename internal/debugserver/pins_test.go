// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

import (
	"fmt"
	"strings"
	"testing"
)

func TestPinValidation(t *testing.T) {
	c := newTestServer()
	for _, bad := range []string{
		"pin 0x06001000",
		"pin 0x06001000 DEAD extra",
		"pin nonsense DEAD",
		"pin 0x05C00000 DEAD",
		"pin 0x06001000 XYZ",
		"pin 0x06001000 ABC",
		"pin 0x060FFFFF DEAD",
		"pin 0x26101000 DEAD",
		"unpin",
		"unpin 0x06001000",
		"unpin nonsense",
		"unpin 0x05C00000",
	} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if len(c.pins) != 0 {
		t.Fatalf("invalid commands added pins: %v", c.pins)
	}
}

// TestPinMaxLen checks the pin byte cap, which is tighter than the
// one-shot write cap because a pin is re-applied every frame.
func TestPinMaxLen(t *testing.T) {
	c := newTestServer()
	ok := fmt.Sprintf("pin 0x06001000 %s", strings.Repeat("AB", pinMaxLen))
	if r := runLine(t, c, ok); r != fmt.Sprintf("pinned %d bytes at 0x06001000", pinMaxLen) {
		t.Fatalf("max length pin response %q", r)
	}
	over := fmt.Sprintf("pin 0x06002000 %s", strings.Repeat("AB", pinMaxLen+1))
	if r := runLine(t, c, over); !strings.HasPrefix(r, "error: length must be") {
		t.Fatalf("over-length pin response %q", r)
	}
}

func TestPinAddListUnpin(t *testing.T) {
	c, m := newFakeServer()
	if r := runLine(t, c, "pin"); r != "no pins" {
		t.Fatalf("empty list response %q", r)
	}
	if r := runLine(t, c, "pin 0x06001000 dead"); r != "pinned 2 bytes at 0x06001000" {
		t.Fatalf("pin response %q", r)
	}
	// The pin applies immediately rather than at the next Service.
	if m.wram[fakeAddr] != 0xDE || m.wram[fakeAddr+1] != 0xAD {
		t.Fatalf("pin did not write: %#x %#x", m.wram[fakeAddr], m.wram[fakeAddr+1])
	}
	if r := runLine(t, c, "pin 0x00200010 7F"); r != "pinned 1 bytes at 0x00200010" {
		t.Fatalf("pin response %q", r)
	}
	list := runLine(t, c, "pin")
	if !strings.Contains(list, "0x06001000 = DEAD hits=0") ||
		!strings.Contains(list, "0x00200010 = 7F hits=0") {
		t.Fatalf("list output:\n%s", list)
	}

	if r := runLine(t, c, "unpin 0x06001000"); r != "unpinned 0x06001000" {
		t.Fatalf("unpin response %q", r)
	}
	// Releasing a pin leaves the held bytes in memory.
	if m.wram[fakeAddr] != 0xDE {
		t.Fatalf("unpin rolled the write back: %#x", m.wram[fakeAddr])
	}
	if len(c.pins) != 1 || c.pins[0].addr != 0x00200010 {
		t.Fatalf("pin list after unpin: %v", c.pins)
	}
	if r := runLine(t, c, "unpin 0x06001000"); !strings.HasPrefix(r, "error:") {
		t.Fatalf("double unpin response %q", r)
	}
	if r := runLine(t, c, "unpin all"); r != "removed 1 pins" {
		t.Fatalf("unpin all response %q", r)
	}
	if len(c.pins) != 0 {
		t.Fatalf("pins remain after unpin all: %v", c.pins)
	}
}

// TestPinHoldsValue covers the per-frame restore and the hit counter: a
// frame that leaves the location alone costs nothing, one that changes
// it is counted and undone.
func TestPinHoldsValue(t *testing.T) {
	c, m := newFakeServer()
	runLine(t, c, "pin 0x06001000 1234")

	c.Service(1)
	if c.pins[0].hits != 0 {
		t.Fatalf("unchanged memory counted a hit: %d", c.pins[0].hits)
	}

	m.wram[fakeAddr+1] = 0xFF
	c.Service(2)
	if m.wram[fakeAddr] != 0x12 || m.wram[fakeAddr+1] != 0x34 {
		t.Fatalf("pin did not restore: %#x %#x", m.wram[fakeAddr], m.wram[fakeAddr+1])
	}
	if c.pins[0].hits != 1 {
		t.Fatalf("hits after one change: %d", c.pins[0].hits)
	}

	c.Service(3)
	if c.pins[0].hits != 1 {
		t.Fatalf("restored memory counted another hit: %d", c.pins[0].hits)
	}
	if r := runLine(t, c, "pin"); !strings.Contains(r, "hits=1") {
		t.Fatalf("list output:\n%s", r)
	}
}

// TestPinReplace checks that re-pinning an address replaces the entry
// so a data change takes effect and the hit count starts over.
func TestPinReplace(t *testing.T) {
	c, m := newFakeServer()
	runLine(t, c, "pin 0x06001000 1234")
	m.wram[fakeAddr] = 0
	c.Service(1)
	if c.pins[0].hits != 1 {
		t.Fatalf("hits before replace: %d", c.pins[0].hits)
	}

	if r := runLine(t, c, "pin 0x06001000 55"); r != "pinned 1 bytes at 0x06001000" {
		t.Fatalf("re-pin response %q", r)
	}
	if len(c.pins) != 1 {
		t.Fatalf("re-pin added an entry: %v", c.pins)
	}
	if c.pins[0].hits != 0 {
		t.Fatalf("re-pin kept the hit count: %d", c.pins[0].hits)
	}
	// The shortened pin holds its own byte and releases the second.
	if m.wram[fakeAddr] != 0x55 {
		t.Fatalf("re-pin did not write: %#x", m.wram[fakeAddr])
	}
	m.wram[fakeAddr+1] = 0x99
	c.Service(2)
	if m.wram[fakeAddr+1] != 0x99 {
		t.Fatalf("replaced pin still holds the old range: %#x", m.wram[fakeAddr+1])
	}
}

func TestPinOverlapRejected(t *testing.T) {
	c := newTestServer()
	runLine(t, c, "pin 0x06001000 11223344")
	for _, bad := range []string{
		"pin 0x06001002 FF",
		"pin 0x06000FFF FFFF",
		"pin 0x06000FFC FFFFFFFFFF",
	} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") ||
			!strings.Contains(r, "overlaps") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if len(c.pins) != 1 {
		t.Fatalf("overlapping pin was added: %v", c.pins)
	}
	// Ranges that only touch are not overlaps.
	if r := runLine(t, c, "pin 0x06001004 FF"); !strings.HasPrefix(r, "pinned") {
		t.Fatalf("adjacent pin response %q", r)
	}
}

func TestPinLimit(t *testing.T) {
	c := newTestServer()
	for i := 0; i < maxPins; i++ {
		r := runLine(t, c, fmt.Sprintf("pin 0x%08X FF", 0x06000000+i*4))
		if !strings.HasPrefix(r, "pinned") {
			t.Fatalf("pin %d failed: %q", i, r)
		}
	}
	if r := runLine(t, c, "pin 0x06001000 FF"); !strings.HasPrefix(r, "error: pin limit") {
		t.Fatalf("over-limit response %q", r)
	}
	// A replacement is not a new entry, so the limit does not block it.
	if r := runLine(t, c, "pin 0x06000000 EE"); !strings.HasPrefix(r, "pinned") {
		t.Fatalf("at-limit replacement response %q", r)
	}
}

// TestPinPrecedesWatch covers the Service ordering: pins restore before
// the watch and break reads, so a held location reports no change and
// its break never fires.
func TestPinPrecedesWatch(t *testing.T) {
	c, m := newFakeServer()
	c.out = make(chan string, 4)
	m.wram[fakeAddr] = 6

	runLine(t, c, "watch 0x06001000")
	runLine(t, c, "break 0x06001000 diff")
	c.Service(100) // seeds both silently

	runLine(t, c, "pin 0x06001000 06")
	m.wram[fakeAddr] = 5
	c.Service(101)
	select {
	case line := <-c.out:
		t.Fatalf("pinned address reported: %q", line)
	default:
	}
	if c.paused.Load() {
		t.Fatal("pinned address fired a break")
	}
	if c.pins[0].hits != 1 {
		t.Fatalf("hits: %d", c.pins[0].hits)
	}
}
