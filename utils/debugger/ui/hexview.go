// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/ebitenui/ebitenui/widget"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
)

// hexBytesPerRow is the classic 16-byte hex dump row.
const hexBytesPerRow = 16

// heatFrames is how many update ticks a changed byte stays highlighted.
const heatFrames = 45

// HexView renders a window of guest memory as a hex dump with an
// address gutter and an ASCII column. Bytes that changed between
// SetData calls at the same address are highlighted and fade back over
// heatFrames ticks. The widget draws its rows directly; ebitenui has no
// grid-of-text widget with per-cell color.
type HexView struct {
	widget *widget.Widget

	// Rows is the number of visible rows. The host sizes its reads to
	// Rows * 16 bytes.
	Rows int

	baseAddr uint32
	data     []byte
	heat     []int

	// Selection state. The range is anchored to addresses, not window
	// positions, so scrolling within the region leaves it in place and
	// the values under it are whatever the current refresh holds.
	selActive bool
	selAnchor uint32
	selCur    uint32
	dragging  bool
}

// NewHexView creates a hex view showing rows rows.
func NewHexView(rows int, opts ...widget.WidgetOpt) *HexView {
	return &HexView{
		widget: widget.NewWidget(opts...),
		Rows:   rows,
	}
}

// SetData replaces the displayed window. When the base address matches
// the previous window, differing bytes get change heat; any move or
// resize resets the heat so a scroll does not light the whole view.
func (h *HexView) SetData(addr uint32, data []byte) {
	sameWindow := addr == h.baseAddr && len(data) == len(h.data)
	if sameWindow {
		for i := range data {
			if data[i] != h.data[i] {
				h.heat[i] = heatFrames
			}
		}
	} else {
		h.heat = make([]int, len(data))
	}
	h.baseAddr = addr
	h.data = append(h.data[:0], data...)
}

// GetWidget returns the underlying widget.
func (h *HexView) GetWidget() *widget.Widget {
	return h.widget
}

// SetLocation positions the view.
func (h *HexView) SetLocation(rect image.Rectangle) {
	h.widget.Rect = rect
}

// cellMetrics reports the character advance and line height of the
// monospace face.
func cellMetrics() (charW, lineH float64) {
	face := *FontFace()
	charW = text.Advance("0", face)
	m := face.Metrics()
	lineH = m.HAscent + m.HDescent + m.HLineGap
	return charW, lineH
}

// Row character-column layout. The address gutter is "0x%08X" plus two
// spaces; hex byte i starts at hexStartCol + i*3, with one extra gap
// character after byte 8; the ASCII gutter is "|" + 2 columns in, with
// one character per byte.
const (
	hexStartCol   = 12
	hexEndCol     = hexStartCol + hexBytesPerRow*3 + 1 // one past the last hex char
	asciiBarCol   = hexEndCol
	asciiStartCol = asciiBarCol + 2
	rowChars      = asciiStartCol + hexBytesPerRow + 1
)

// PreferredSize reports the size of the full row grid. Cell metrics are
// fractional and rows draw at row*lineH in float space, so both
// dimensions ceil the full product rather than truncating per cell.
func (h *HexView) PreferredSize() (int, int) {
	charW, lineH := cellMetrics()
	return int(math.Ceil(charW*rowChars)) + Px(8),
		int(math.Ceil(lineH*float64(h.Rows))) + Px(8)
}

// Validate is part of the ebitenui widget contract.
func (h *HexView) Validate() {}

// Update decays the change heat and tracks the selection drag.
func (h *HexView) Update(updObj *widget.UpdateObject) {
	h.widget.Update(updObj)
	for i := range h.heat {
		if h.heat[i] > 0 {
			h.heat[i]--
		}
	}
	h.updateSelection()
}

// HasSelection reports whether a selection exists.
func (h *HexView) HasSelection() bool {
	return h.selActive
}

// Dragging reports whether a selection drag is in progress.
func (h *HexView) Dragging() bool {
	return h.dragging
}

// ClearSelection drops the selection. The host calls it when the view
// jumps somewhere unrelated (region switch, goto).
func (h *HexView) ClearSelection() {
	h.selActive = false
	h.dragging = false
}

// selRange returns the selection as an ordered inclusive address pair.
func (h *HexView) selRange() (uint32, uint32) {
	if h.selAnchor <= h.selCur {
		return h.selAnchor, h.selCur
	}
	return h.selCur, h.selAnchor
}

// updateSelection handles the press-drag-release selection gesture. A
// press on a byte cell anchors a new selection; a press elsewhere in
// the view clears it. The drag keeps updating while the button is held
// even if the cursor leaves the widget, clamping to the nearest byte.
func (h *HexView) updateSelection() {
	mx, my := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if !image.Pt(mx, my).In(h.widget.Rect) {
			return
		}
		if idx, ok := h.hitTest(mx, my); ok {
			h.selActive = true
			h.dragging = true
			h.selAnchor = h.baseAddr + uint32(idx)
			h.selCur = h.selAnchor
		} else {
			h.ClearSelection()
		}
		return
	}
	if h.dragging {
		if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			h.dragging = false
			return
		}
		h.selCur = h.baseAddr + uint32(h.hitTestClamped(mx, my))
	}
}

// hitTest maps a cursor position to a byte index in the window. It
// accepts hits on a hex cell (including its trailing space) or an
// ASCII cell.
func (h *HexView) hitTest(mx, my int) (int, bool) {
	charW, lineH := cellMetrics()
	x := float64(mx-h.widget.Rect.Min.X-Px(4)) / charW
	y := float64(my-h.widget.Rect.Min.Y-Px(4)) / lineH
	if y < 0 || x < 0 {
		return 0, false
	}
	row := int(y)
	col := int(x)

	i := -1
	switch {
	case col >= hexStartCol && col < hexStartCol+8*3:
		i = (col - hexStartCol) / 3
	case col >= hexStartCol+8*3+1 && col < hexEndCol:
		i = 8 + (col-hexStartCol-8*3-1)/3
	case col >= asciiStartCol && col < asciiStartCol+hexBytesPerRow:
		i = col - asciiStartCol
	}
	if i < 0 {
		return 0, false
	}
	idx := row*hexBytesPerRow + i
	if idx >= len(h.data) {
		return 0, false
	}
	return idx, true
}

// hitTestClamped maps a cursor position to the nearest byte index, for
// drag updates that wander off the cells.
func (h *HexView) hitTestClamped(mx, my int) int {
	if len(h.data) == 0 {
		return 0
	}
	charW, lineH := cellMetrics()
	x := float64(mx-h.widget.Rect.Min.X-Px(4)) / charW
	y := float64(my-h.widget.Rect.Min.Y-Px(4)) / lineH

	row := int(y)
	if y < 0 {
		row = 0
	}
	lastRow := (len(h.data) - 1) / hexBytesPerRow
	if row > lastRow {
		row = lastRow
	}

	col := int(x)
	i := 0
	switch {
	case col < hexStartCol:
		i = 0
	case col < hexStartCol+8*3:
		i = (col - hexStartCol) / 3
	case col < hexStartCol+8*3+1:
		i = 8
	case col < hexEndCol:
		i = 8 + (col-hexStartCol-8*3-1)/3
	case col < asciiStartCol:
		i = hexBytesPerRow - 1
	case col < asciiStartCol+hexBytesPerRow:
		i = col - asciiStartCol
	default:
		i = hexBytesPerRow - 1
	}

	idx := row*hexBytesPerRow + i
	if idx >= len(h.data) {
		idx = len(h.data) - 1
	}
	return idx
}

// selected reports whether the byte at window index idx is inside the
// selection.
func (h *HexView) selected(idx int) bool {
	if !h.selActive {
		return false
	}
	lo, hi := h.selRange()
	addr := h.baseAddr + uint32(idx)
	return addr >= lo && addr <= hi
}

// SelectedDump renders the selection's visible bytes in the viewer's
// dump format: row-aligned address gutter, hex cells, and ASCII, with
// unselected positions blanked so columns stay aligned.
func (h *HexView) SelectedDump() string {
	first, last, ok := h.selWindowBounds()
	if !ok {
		return ""
	}
	var b strings.Builder
	for row := first / hexBytesPerRow; row <= last/hexBytesPerRow; row++ {
		var hexPart, asciiPart strings.Builder
		for i := 0; i < hexBytesPerRow; i++ {
			if i == 8 {
				hexPart.WriteByte(' ')
			}
			idx := row*hexBytesPerRow + i
			if idx < len(h.data) && h.selected(idx) {
				fmt.Fprintf(&hexPart, "%02X ", h.data[idx])
				ch := byte('.')
				if h.data[idx] >= 0x20 && h.data[idx] <= 0x7e {
					ch = h.data[idx]
				}
				asciiPart.WriteByte(ch)
			} else {
				hexPart.WriteString("   ")
				asciiPart.WriteByte(' ')
			}
		}
		fmt.Fprintf(&b, "0x%08X  %s |%s|\n",
			h.baseAddr+uint32(row*hexBytesPerRow), hexPart.String(), asciiPart.String())
	}
	return strings.TrimRight(b.String(), "\n")
}

// SelectedHex renders the selection's visible bytes as one contiguous
// hex string.
func (h *HexView) SelectedHex() string {
	first, last, ok := h.selWindowBounds()
	if !ok {
		return ""
	}
	var b strings.Builder
	for idx := first; idx <= last; idx++ {
		fmt.Fprintf(&b, "%02X", h.data[idx])
	}
	return b.String()
}

// selWindowBounds intersects the selection with the current window and
// returns the first and last selected window indexes.
func (h *HexView) selWindowBounds() (int, int, bool) {
	if !h.selActive || len(h.data) == 0 {
		return 0, 0, false
	}
	lo, hi := h.selRange()
	winLo := h.baseAddr
	winHi := h.baseAddr + uint32(len(h.data)-1)
	if hi < winLo || lo > winHi {
		return 0, 0, false
	}
	if lo < winLo {
		lo = winLo
	}
	if hi > winHi {
		hi = winHi
	}
	return int(lo - h.baseAddr), int(hi - h.baseAddr), true
}

// heatColor blends the text color toward the highlight color by the
// remaining heat.
func heatColor(heat int) color.Color {
	if heat <= 0 {
		return Text
	}
	f := float64(heat) / heatFrames
	blend := func(a, b uint8) uint8 {
		return uint8(float64(a) + (float64(b)-float64(a))*f)
	}
	return color.NRGBA{
		R: blend(Text.R, Bad.R),
		G: blend(Text.G, Bad.G),
		B: blend(Text.B, Bad.B),
		A: 0xff,
	}
}

func drawString(screen *ebiten.Image, s string, x, y float64, c color.Color) {
	face := *FontFace()
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(c)
	text.Draw(screen, s, face, op)
}

// Render draws the visible rows.
func (h *HexView) Render(screen *ebiten.Image) {
	h.widget.Render(screen)
	rect := h.widget.Rect
	if rect.Empty() {
		return
	}

	charW, lineH := cellMetrics()
	x0 := float64(rect.Min.X + Px(4))
	y := float64(rect.Min.Y + Px(4))

	for row := 0; row < h.Rows; row++ {
		off := row * hexBytesPerRow
		if off >= len(h.data) {
			break
		}
		line := h.data[off:min(off+hexBytesPerRow, len(h.data))]

		drawString(screen, fmt.Sprintf("0x%08X", h.baseAddr+uint32(off)), x0, y, TextSecondary)

		// Hex cells, with the classic extra gap after byte 8. Selected
		// cells get a background fill behind the text.
		x := x0 + hexStartCol*charW
		for i, b := range line {
			if i == 8 {
				x += charW
			}
			if h.selected(off + i) {
				fillRect(screen, x, y, 2*charW, lineH, Primary)
			}
			drawString(screen, fmt.Sprintf("%02X", b), x, y, heatColor(h.heat[off+i]))
			x += 3 * charW
		}

		// ASCII gutter at a fixed column so short tail rows still align.
		x = x0 + asciiBarCol*charW
		drawString(screen, "|", x, y, TextSecondary)
		x = x0 + asciiStartCol*charW
		for i, b := range line {
			if h.selected(off + i) {
				fillRect(screen, x+float64(i)*charW, y, charW, lineH, Primary)
			}
			ch := "."
			if b >= 0x20 && b <= 0x7e {
				ch = string(rune(b))
			}
			drawString(screen, ch, x+float64(i)*charW, y, heatColor(h.heat[off+i]))
		}
		x += float64(hexBytesPerRow) * charW
		drawString(screen, "|", x, y, TextSecondary)

		y += lineH
	}
}

// fillRect fills a cell background using a clipped SubImage, the same
// allocation-free clipping the scroll view uses.
func fillRect(screen *ebiten.Image, x, y, w, ht float64, c color.Color) {
	r := image.Rect(int(x), int(y), int(x+w), int(y+ht))
	r = r.Intersect(screen.Bounds())
	if r.Empty() {
		return
	}
	screen.SubImage(r).(*ebiten.Image).Fill(c)
}
