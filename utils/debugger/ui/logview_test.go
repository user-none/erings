// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"testing"
)

func fillBuffer(b *LogBuffer, lines ...string) {
	for _, l := range lines {
		b.Append(l)
	}
}

// selectRange sets a selection the way a drag would.
func selectRange(b *LogBuffer, anchorID uint64, anchorCol int, curID uint64, curCol int) {
	b.selActive = true
	b.anchorID = anchorID
	b.anchorCol = anchorCol
	b.curID = curID
	b.curCol = curCol
}

func TestLogBufferSelectedTextSingleLine(t *testing.T) {
	b := NewLogBuffer(10)
	fillBuffer(b, "hello world")
	selectRange(b, 0, 6, 0, 10)
	if got := b.SelectedText(); got != "world" {
		t.Fatalf("selected %q, want %q", got, "world")
	}
	// A reverse drag selects the same range.
	selectRange(b, 0, 10, 0, 6)
	if got := b.SelectedText(); got != "world" {
		t.Fatalf("reverse selected %q, want %q", got, "world")
	}
}

func TestLogBufferSelectedTextMultiLine(t *testing.T) {
	b := NewLogBuffer(10)
	fillBuffer(b, "first", "", "third")
	selectRange(b, 0, 3, 2, 2)
	// From "s" of first through "i" of third, across the empty line.
	if got := b.SelectedText(); got != "st\n\nthi" {
		t.Fatalf("selected %q, want %q", got, "st\n\nthi")
	}
}

func TestLogBufferTrimKeepsSelectionAnchored(t *testing.T) {
	b := NewLogBuffer(3)
	fillBuffer(b, "a0", "b1", "c2")
	// Select all of "b1" and "c2" (IDs 1 and 2).
	selectRange(b, 1, 0, 2, 1)

	// Appending trims "a0"; the selection addresses IDs and must not
	// shift onto different lines.
	b.Append("d3")
	if got := b.SelectedText(); got != "b1\nc2" {
		t.Fatalf("selected after trim %q, want %q", got, "b1\nc2")
	}

	// Trimming past the selection start clamps it to the first
	// retained line.
	b.Append("e4")
	if got := b.SelectedText(); got != "c2" {
		t.Fatalf("selected after second trim %q, want %q", got, "c2")
	}

	// Trimming past the whole selection drops it.
	b.Append("f5")
	b.Append("g6")
	if b.HasSelection() {
		t.Fatal("selection survived being fully trimmed away")
	}
}

func TestLogBufferSelSpan(t *testing.T) {
	b := NewLogBuffer(10)
	fillBuffer(b, "abcdef", "ghijkl", "mnopqr")
	selectRange(b, 0, 4, 2, 1)

	cases := []struct {
		id         uint64
		start, end int
		ok         bool
	}{
		{0, 4, 5, true},
		{1, 0, 5, true},
		{2, 0, 1, true},
	}
	for _, c := range cases {
		start, end, ok := b.selSpan(c.id)
		if start != c.start || end != c.end || ok != c.ok {
			t.Fatalf("selSpan(%d) = (%d,%d,%t), want (%d,%d,%t)",
				c.id, start, end, ok, c.start, c.end, c.ok)
		}
	}
	if _, _, ok := b.selSpan(3); ok {
		t.Fatal("selSpan hit a line outside the selection")
	}
}

func TestLogBufferMaxColsAndCap(t *testing.T) {
	b := NewLogBuffer(2)
	fillBuffer(b, "short", "a much longer line", "x")
	if len(b.lines) != 2 || b.firstID != 1 {
		t.Fatalf("cap not applied: %d lines, firstID %d", len(b.lines), b.firstID)
	}
	if b.maxCols != len("a much longer line") {
		t.Fatalf("maxCols %d", b.maxCols)
	}
}
