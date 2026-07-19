// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"image"
	"strings"

	"github.com/ebitenui/ebitenui/widget"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// LogBuffer is the log line store with its selection. It lives for the
// application lifetime while LogView widgets come and go with UI
// rebuilds. Lines carry monotonic IDs (firstID + index) so the
// selection stays put when appends trim the front of the buffer.
type LogBuffer struct {
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

// NewLogBuffer creates a buffer bounded to max lines.
func NewLogBuffer(max int) *LogBuffer {
	return &LogBuffer{max: max}
}

// Append adds one line and trims the front past the cap. A selection
// whose lines are trimmed away shrinks with them.
func (b *LogBuffer) Append(line string) {
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
func (b *LogBuffer) selOrder() (selPos, selPos) {
	a := selPos{b.anchorID, b.anchorCol}
	c := selPos{b.curID, b.curCol}
	if a.id < c.id || (a.id == c.id && a.col <= c.col) {
		return a, c
	}
	return c, a
}

// clampSelStart pulls the earlier selection endpoint up to the first
// retained line after a trim.
func (b *LogBuffer) clampSelStart() {
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

// HasSelection reports whether a selection exists.
func (b *LogBuffer) HasSelection() bool {
	return b.selActive
}

// ClearSelection drops the selection.
func (b *LogBuffer) ClearSelection() {
	b.selActive = false
}

// line returns the text of a line by ID.
func (b *LogBuffer) line(id uint64) string {
	idx := int(id - b.firstID)
	if idx < 0 || idx >= len(b.lines) {
		return ""
	}
	return b.lines[idx]
}

// lastID returns the ID of the newest line. Valid only when the buffer
// is non-empty.
func (b *LogBuffer) lastID() uint64 {
	return b.firstID + uint64(len(b.lines)) - 1
}

// selSpan returns the selected column range of one line, inclusive,
// and whether the line intersects the selection at all.
func (b *LogBuffer) selSpan(id uint64) (int, int, bool) {
	if !b.selActive {
		return 0, 0, false
	}
	lo, hi := b.selOrder()
	if id < lo.id || id > hi.id {
		return 0, 0, false
	}
	line := b.line(id)
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
func (b *LogBuffer) SelectedText() string {
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
		hi = selPos{last, len(b.line(last))}
	}
	var out []string
	for id := lo.id; id <= hi.id; id++ {
		line := b.line(id)
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

// LogView renders a LogBuffer with drag selection. It draws its lines
// directly (per-line highlight fill needs per-character geometry) and
// is designed to sit inside a ScrollView, which positions the full
// content rect and clips rendering to the viewport.
type LogView struct {
	widget *widget.Widget
	buf    *LogBuffer

	// lastClip is the viewport from the previous render. Mouse checks
	// intersect with it so clicks on scrolled-away content, or on
	// widgets that happen to overlap the content rect outside the
	// viewport, do not hit.
	lastClip image.Rectangle

	dragging bool
}

// NewLogView creates a view over buf.
func NewLogView(buf *LogBuffer, opts ...widget.WidgetOpt) *LogView {
	return &LogView{
		widget: widget.NewWidget(opts...),
		buf:    buf,
	}
}

// GetWidget returns the underlying widget.
func (v *LogView) GetWidget() *widget.Widget {
	return v.widget
}

// SetLocation positions the content rect.
func (v *LogView) SetLocation(rect image.Rectangle) {
	v.widget.Rect = rect
}

// PreferredSize reports the full content size.
func (v *LogView) PreferredSize() (int, int) {
	charW, lineH := cellMetrics()
	return int(charW*float64(v.buf.maxCols)) + Px(12), int(lineH)*len(v.buf.lines) + Px(8)
}

// Validate is part of the ebitenui widget contract.
func (v *LogView) Validate() {}

// Dragging reports whether a selection drag is in progress.
func (v *LogView) Dragging() bool {
	return v.dragging
}

// Update tracks the selection drag. Presses only register inside the
// visible viewport; drags keep updating wherever the cursor goes,
// clamped to the nearest character.
func (v *LogView) Update(updObj *widget.UpdateObject) {
	v.widget.Update(updObj)

	mx, my := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if !image.Pt(mx, my).In(v.lastClip) {
			return
		}
		if id, col, ok := v.hitTest(mx, my); ok {
			v.buf.selActive = true
			v.dragging = true
			v.buf.anchorID = id
			v.buf.anchorCol = col
			v.buf.curID = id
			v.buf.curCol = col
		} else {
			v.buf.ClearSelection()
		}
		return
	}
	if v.dragging {
		if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			v.dragging = false
			return
		}
		if id, col, ok := v.hitTestClamped(mx, my); ok {
			v.buf.curID = id
			v.buf.curCol = col
		}
	}
}

// hitTest maps a cursor position to a line ID and column. It only hits
// on an existing character cell.
func (v *LogView) hitTest(mx, my int) (uint64, int, bool) {
	row, col, ok := v.cellAt(mx, my)
	if !ok || row >= len(v.buf.lines) {
		return 0, 0, false
	}
	line := v.buf.lines[row]
	if col >= len(line) {
		return 0, 0, false
	}
	return v.buf.firstID + uint64(row), col, true
}

// hitTestClamped maps a cursor position to the nearest character.
func (v *LogView) hitTestClamped(mx, my int) (uint64, int, bool) {
	if len(v.buf.lines) == 0 {
		return 0, 0, false
	}
	row, col, _ := v.cellAt(mx, my)
	if row < 0 {
		row = 0
	}
	if row >= len(v.buf.lines) {
		row = len(v.buf.lines) - 1
	}
	line := v.buf.lines[row]
	if col < 0 {
		col = 0
	}
	if col >= len(line) {
		col = len(line) - 1
		if col < 0 {
			col = 0
		}
	}
	return v.buf.firstID + uint64(row), col, true
}

// cellAt converts a cursor position to a row index and column in
// content space. ok is false above or left of the content.
func (v *LogView) cellAt(mx, my int) (int, int, bool) {
	charW, lineH := cellMetrics()
	x := float64(mx-v.widget.Rect.Min.X-Px(6)) / charW
	y := float64(my-v.widget.Rect.Min.Y-Px(4)) / lineH
	if x < 0 || y < 0 {
		return int(y), int(x), false
	}
	return int(y), int(x), true
}

// Render draws the lines intersecting the clip viewport, with the
// selection filled behind the text.
func (v *LogView) Render(screen *ebiten.Image) {
	v.widget.Render(screen)
	v.lastClip = screen.Bounds()

	rect := v.widget.Rect
	if rect.Empty() || len(v.buf.lines) == 0 {
		return
	}
	charW, lineH := cellMetrics()
	x0 := float64(rect.Min.X + Px(6))
	top := rect.Min.Y + Px(4)

	first := (v.lastClip.Min.Y - top) / int(lineH)
	if first < 0 {
		first = 0
	}
	last := (v.lastClip.Max.Y-top)/int(lineH) + 1
	if last > len(v.buf.lines) {
		last = len(v.buf.lines)
	}

	for row := first; row < last; row++ {
		y := float64(top) + float64(row)*lineH
		line := v.buf.lines[row]
		id := v.buf.firstID + uint64(row)
		if start, end, ok := v.buf.selSpan(id); ok && len(line) > 0 {
			fillRect(screen, x0+float64(start)*charW, y,
				float64(end-start+1)*charW, lineH, Primary)
		}
		drawString(screen, line, x0, y, Text)
	}
}
