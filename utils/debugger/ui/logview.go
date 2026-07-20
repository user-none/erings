// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"image"
	"math"

	"github.com/ebitenui/ebitenui/widget"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/user-none/erings/utils/debugger/ui/logbuf"
)

// LogView renders a logbuf.Buffer with drag selection. It draws its
// lines directly (per-line highlight fill needs per-character geometry)
// and is designed to sit inside a ScrollView, which positions the full
// content rect and clips rendering to the viewport.
type LogView struct {
	widget *widget.Widget
	buf    *logbuf.Buffer

	// lastClip is the viewport from the previous render. Mouse checks
	// intersect with it so clicks on scrolled-away content, or on
	// widgets that happen to overlap the content rect outside the
	// viewport, do not hit.
	lastClip image.Rectangle

	dragging bool
}

// NewLogView creates a view over buf.
func NewLogView(buf *logbuf.Buffer, opts ...widget.WidgetOpt) *LogView {
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

// PreferredSize reports the full content size. The face's line height
// is fractional, and rows draw at row*lineH in float space, so the
// height must ceil the full product: truncating per line under-reports
// tall content and the bottom rows spill past the reserved rect.
func (v *LogView) PreferredSize() (int, int) {
	charW, lineH := cellMetrics()
	return int(math.Ceil(charW*float64(v.buf.MaxCols()))) + Px(12),
		int(math.Ceil(lineH*float64(v.buf.LineCount()))) + Px(8)
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
			v.dragging = true
			v.buf.StartSelection(id, col)
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
			v.buf.DragTo(id, col)
		}
	}
}

// hitTest maps a cursor position to a line ID and column. It only hits
// on an existing character cell.
func (v *LogView) hitTest(mx, my int) (uint64, int, bool) {
	row, col, ok := v.cellAt(mx, my)
	if !ok || row >= v.buf.LineCount() {
		return 0, 0, false
	}
	id := v.buf.FirstID() + uint64(row)
	if col >= len(v.buf.Line(id)) {
		return 0, 0, false
	}
	return id, col, true
}

// hitTestClamped maps a cursor position to the nearest character.
func (v *LogView) hitTestClamped(mx, my int) (uint64, int, bool) {
	if v.buf.LineCount() == 0 {
		return 0, 0, false
	}
	row, col, _ := v.cellAt(mx, my)
	if row < 0 {
		row = 0
	}
	if row >= v.buf.LineCount() {
		row = v.buf.LineCount() - 1
	}
	id := v.buf.FirstID() + uint64(row)
	line := v.buf.Line(id)
	if col < 0 {
		col = 0
	}
	if col >= len(line) {
		col = len(line) - 1
		if col < 0 {
			col = 0
		}
	}
	return id, col, true
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
	if rect.Empty() || v.buf.LineCount() == 0 {
		return
	}
	charW, lineH := cellMetrics()
	x0 := float64(rect.Min.X + Px(6))
	top := rect.Min.Y + Px(4)

	first := int(float64(v.lastClip.Min.Y-top) / lineH)
	if first < 0 {
		first = 0
	}
	last := int(float64(v.lastClip.Max.Y-top)/lineH) + 1
	if last > v.buf.LineCount() {
		last = v.buf.LineCount()
	}

	for row := first; row < last; row++ {
		y := float64(top) + float64(row)*lineH
		id := v.buf.FirstID() + uint64(row)
		line := v.buf.Line(id)
		if start, end, ok := v.buf.SelSpan(id); ok && len(line) > 0 {
			fillRect(screen, x0+float64(start)*charW, y,
				float64(end-start+1)*charW, lineH, Primary)
		}
		drawString(screen, line, x0, y, Text)
	}
}
