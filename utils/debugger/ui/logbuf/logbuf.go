// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logbuf holds the log line store with its selection state. It
// has no rendering dependencies so its logic is testable headless; the
// ui package draws it.
package logbuf

import "strings"

// Buffer is the log line store with its selection. It lives for the
// application lifetime while the widgets rendering it come and go with
// UI rebuilds. Lines carry monotonic IDs (firstID + index) so the
// selection stays put when appends trim the front of the buffer.
type Buffer struct {
	lines   []string
	firstID uint64
	max     int

	// maxCols tracks the widest line ever held, for preferred width.
	maxCols int

	// Selection endpoints as (line ID, column). anchor is where the
	// drag started; cur is the latest drag position.
	selActive bool
	anchorID  uint64
	anchorCol int
	curID     uint64
	curCol    int
}

// New creates a buffer bounded to max lines.
func New(max int) *Buffer {
	return &Buffer{max: max}
}

// Append adds one line and trims the front past the cap. A selection
// whose lines are trimmed away shrinks with them.
func (b *Buffer) Append(line string) {
	b.lines = append(b.lines, line)
	if len(line) > b.maxCols {
		b.maxCols = len(line)
	}
	if len(b.lines) > b.max {
		drop := len(b.lines) - b.max
		b.lines = b.lines[drop:]
		b.firstID += uint64(drop)
		if b.selActive {
			lo, hi := b.selOrder()
			if hi.id < b.firstID {
				b.selActive = false
			} else if lo.id < b.firstID {
				b.clampSelStart()
			}
		}
	}
}

// selPos is one selection endpoint.
type selPos struct {
	id  uint64
	col int
}

// selOrder returns the selection endpoints in document order.
func (b *Buffer) selOrder() (selPos, selPos) {
	a := selPos{b.anchorID, b.anchorCol}
	c := selPos{b.curID, b.curCol}
	if a.id < c.id || (a.id == c.id && a.col <= c.col) {
		return a, c
	}
	return c, a
}

// clampSelStart pulls the earlier selection endpoint up to the first
// retained line after a trim.
func (b *Buffer) clampSelStart() {
	if b.anchorID < b.curID || (b.anchorID == b.curID && b.anchorCol <= b.curCol) {
		if b.anchorID < b.firstID {
			b.anchorID = b.firstID
			b.anchorCol = 0
		}
	} else if b.curID < b.firstID {
		b.curID = b.firstID
		b.curCol = 0
	}
}

// StartSelection anchors a new selection at one character.
func (b *Buffer) StartSelection(id uint64, col int) {
	b.selActive = true
	b.anchorID = id
	b.anchorCol = col
	b.curID = id
	b.curCol = col
}

// DragTo extends the selection from its anchor to a character.
func (b *Buffer) DragTo(id uint64, col int) {
	b.curID = id
	b.curCol = col
}

// HasSelection reports whether a selection exists.
func (b *Buffer) HasSelection() bool {
	return b.selActive
}

// ClearSelection drops the selection.
func (b *Buffer) ClearSelection() {
	b.selActive = false
}

// FirstID returns the ID of the oldest retained line.
func (b *Buffer) FirstID() uint64 {
	return b.firstID
}

// LineCount returns the number of retained lines.
func (b *Buffer) LineCount() int {
	return len(b.lines)
}

// MaxCols returns the widest line length ever held.
func (b *Buffer) MaxCols() int {
	return b.maxCols
}

// Line returns the text of a line by ID.
func (b *Buffer) Line(id uint64) string {
	idx := int(id - b.firstID)
	if idx < 0 || idx >= len(b.lines) {
		return ""
	}
	return b.lines[idx]
}

// lastID returns the ID of the newest line. Valid only when the buffer
// is non-empty.
func (b *Buffer) lastID() uint64 {
	return b.firstID + uint64(len(b.lines)) - 1
}

// SelSpan returns the selected column range of one line, inclusive,
// and whether the line intersects the selection at all.
func (b *Buffer) SelSpan(id uint64) (int, int, bool) {
	if !b.selActive {
		return 0, 0, false
	}
	lo, hi := b.selOrder()
	if id < lo.id || id > hi.id {
		return 0, 0, false
	}
	line := b.Line(id)
	start, end := 0, len(line)-1
	if id == lo.id {
		start = lo.col
	}
	if id == hi.id {
		end = hi.col
	}
	if end >= len(line) {
		end = len(line) - 1
	}
	if start > end && len(line) > 0 {
		return 0, 0, false
	}
	return start, end, true
}

// SelectedText renders the selection as plain text with newlines.
// Lines are stored in full, so clipped display never truncates a copy.
func (b *Buffer) SelectedText() string {
	if !b.selActive || len(b.lines) == 0 {
		return ""
	}
	lo, hi := b.selOrder()
	if hi.id < b.firstID {
		return ""
	}
	if lo.id < b.firstID {
		lo = selPos{b.firstID, 0}
	}
	last := b.lastID()
	if hi.id > last {
		hi = selPos{last, len(b.Line(last))}
	}
	var out []string
	for id := lo.id; id <= hi.id; id++ {
		line := b.Line(id)
		start, end := 0, len(line)
		if id == lo.id && lo.col < len(line) {
			start = lo.col
		}
		if id == hi.id && hi.col+1 < len(line) {
			end = hi.col + 1
		}
		if start > len(line) {
			start = len(line)
		}
		out = append(out, line[start:end])
	}
	return strings.Join(out, "\n")
}
