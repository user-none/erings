// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/user-none/erings/internal/debugserver/responses"
	"github.com/user-none/erings/utils/debugger/client"
	"github.com/user-none/erings/utils/debugger/ui"
)

// sidePollTicks is the watch/break list refresh cadence in 60Hz ticks.
const sidePollTicks = 15

// rowHeatTicks is how long a fired watch or break row stays
// highlighted.
const rowHeatTicks = 45

// sideListRows is each list's baseline visible height in text rows.
const sideListRows = 8

// side is the watches/breaks panel state. The row lists are rebuilt
// from the last server response; nothing here is authoritative.
type side struct {
	watchRows *widget.Container
	breakRows *widget.Container

	watches []responses.WatchInfo
	breaks  []responses.BreakInfo

	// watchHeat and breakHeat hold per-address highlight ticks set by
	// pushed events.
	watchHeat map[uint32]int
	breakHeat map[uint32]int

	// One request per list is outstanding at a time; a forced refresh
	// arriving mid-flight queues exactly one follow-up.
	watchInFlight bool
	watchQueued   bool
	breakInFlight bool
	breakQueued   bool

	// Quick-add rows, one per list. The breaks row adds the condition
	// operator (cycled) and its comparison value.
	watchAddr     *widget.TextInput
	watchAddWidth int

	breakAddr     *widget.TextInput
	breakVal      *widget.TextInput
	breakOp       string
	breakAddWidth int
}

// ensureSideDefaults initializes the side panel state shared by the
// watches and breaks panels.
func (a *app) ensureSideDefaults() {
	if a.side.watchHeat == nil {
		a.side.watchHeat = map[uint32]int{}
		a.side.breakHeat = map[uint32]int{}
	}
	if a.side.watchAddWidth == 0 {
		a.side.watchAddWidth = 8
	}
	if a.side.breakAddWidth == 0 {
		a.side.breakAddWidth = 8
	}
	if a.side.breakOp == "" {
		a.side.breakOp = "dec"
	}
}

// sideListPanel builds one list panel: a header, the stretching row
// list, and the quick-add row pinned beneath it.
func sideListPanel(title string, rows *widget.Container, addRow *widget.Container) *widget.Container {
	panel := widget.NewContainer(
		widget.ContainerOpts.BackgroundImage(image.NewNineSliceColor(ui.Surface)),
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(1),
			widget.GridLayoutOpts.Stretch([]bool{true}, []bool{false, true, false}),
			widget.GridLayoutOpts.Padding(widget.NewInsetsSimple(ui.Px(6))),
			widget.GridLayoutOpts.Spacing(0, ui.Px(6)),
		)),
	)
	panel.AddChild(ui.Label(title, ui.TextSecondary))
	view, wrap := ui.Scrollable(rows, ui.Surface)
	// The list's preferred height is a fixed baseline so the middle
	// section's height stays deterministic. The left column is taller,
	// so the panel's stretch row grows the list past this to match.
	view.MinHeight = ui.TextRowsHeight(sideListRows)
	view.MaxHeight = view.MinHeight
	panel.AddChild(wrap)
	panel.AddChild(addRow)
	return panel
}

func listRowsContainer() *widget.Container {
	return widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewRowLayout(
			widget.RowLayoutOpts.Direction(widget.DirectionVertical),
			widget.RowLayoutOpts.Spacing(ui.Px(2)),
		)),
	)
}

// buildWatchPanel creates the watches list panel.
func (a *app) buildWatchPanel() *widget.Container {
	a.ensureSideDefaults()
	a.side.watchRows = listRowsContainer()
	panel := sideListPanel("Watches", a.side.watchRows, a.buildWatchAddRow())
	a.rebuildWatchRows()
	return panel
}

// buildBreakPanel creates the breaks list panel.
func (a *app) buildBreakPanel() *widget.Container {
	a.ensureSideDefaults()
	a.side.breakRows = listRowsContainer()
	panel := sideListPanel("Breaks", a.side.breakRows, a.buildBreakAddRow())
	a.rebuildBreakRows()
	return panel
}

// widthCycleButton creates a button cycling *width through 8/16/32.
func widthCycleButton(width *int) *widget.Button {
	var btn *widget.Button
	btn = ui.Button(widthLabel(*width), func(args *widget.ButtonClickedEventArgs) {
		switch *width {
		case 8:
			*width = 16
		case 16:
			*width = 32
		default:
			*width = 8
		}
		btn.Text().Label = widthLabel(*width)
	})
	return btn
}

// buildWatchAddRow creates the watch quick-add controls: address
// entry, a width cycle button, and the add button.
func (a *app) buildWatchAddRow() *widget.Container {
	a.side.watchAddr = ui.TextInput("address", 130,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.addWatch()
		}),
	)
	a.inputs.Add(a.side.watchAddr)

	return ui.HRow(ui.Px(4),
		a.side.watchAddr,
		widthCycleButton(&a.side.watchAddWidth),
		ui.Button("+Watch", func(args *widget.ButtonClickedEventArgs) {
			a.addWatch()
		}),
	)
}

// breakOps is the condition cycle order for the break quick-add row,
// matching the server's operator vocabulary.
var breakOps = []string{"dec", "inc", "same", "diff", "eq", "ne", "lt", "gt"}

// breakOpNeedsVal reports whether the operator compares against a
// constant.
func breakOpNeedsVal(op string) bool {
	switch op {
	case "eq", "ne", "lt", "gt":
		return true
	}
	return false
}

// buildBreakAddRow creates the break quick-add controls: address
// entry, a condition cycle button, the comparison value, a width cycle
// button, and the add button. The value only applies to comparison
// operators.
func (a *app) buildBreakAddRow() *widget.Container {
	a.side.breakAddr = ui.TextInput("address", 130,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.addBreak()
		}),
	)
	a.inputs.Add(a.side.breakAddr)

	var opBtn *widget.Button
	opBtn = ui.Button(a.side.breakOp, func(args *widget.ButtonClickedEventArgs) {
		for i, op := range breakOps {
			if op == a.side.breakOp {
				a.side.breakOp = breakOps[(i+1)%len(breakOps)]
				break
			}
		}
		opBtn.Text().Label = a.side.breakOp
	})

	a.side.breakVal = ui.TextInput("value", 70,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.addBreak()
		}),
	)
	a.inputs.Add(a.side.breakVal)

	return ui.HRow(ui.Px(4),
		a.side.breakAddr,
		opBtn,
		a.side.breakVal,
		widthCycleButton(&a.side.breakAddWidth),
		ui.Button("+Break", func(args *widget.ButtonClickedEventArgs) {
			a.addBreak()
		}),
	)
}

func widthLabel(w int) string {
	return fmt.Sprintf("w%d", w)
}

// addWatch sends a watch command for the quick-add entry.
func (a *app) addWatch() {
	addr := strings.TrimSpace(a.side.watchAddr.GetText())
	if addr == "" {
		return
	}
	a.addWatchAt(addr)
}

// addWatchAt sends a watch command for addr using the quick-add row's
// width and refreshes the list.
func (a *app) addWatchAt(addr string) {
	a.send(fmt.Sprintf("watch %s %d", addr, a.side.watchAddWidth), func(r client.Response) {
		if out := formatResponse(r); out != "" {
			a.logf("%s", out)
		}
		a.pollWatches(true)
	})
}

// addBreak sends a break command for the quick-add entry.
func (a *app) addBreak() {
	addr := strings.TrimSpace(a.side.breakAddr.GetText())
	if addr == "" {
		return
	}
	a.addBreakAt(addr)
}

// addBreakAt sends a break command for addr using the quick-add row's
// condition controls and refreshes the list.
func (a *app) addBreakAt(addr string) {
	cmd := fmt.Sprintf("break %s %s", addr, a.side.breakOp)
	if breakOpNeedsVal(a.side.breakOp) {
		v := strings.TrimSpace(a.side.breakVal.GetText())
		if v == "" {
			a.logf("break %s needs a value", a.side.breakOp)
			return
		}
		cmd += " " + v
	}
	cmd += fmt.Sprintf(" %d", a.side.breakAddWidth)
	a.send(cmd, func(r client.Response) {
		if out := formatResponse(r); out != "" {
			a.logf("%s", out)
		}
		a.pollBreaks(true)
	})
}

// pollSide refreshes both lists on their cadence.
func (a *app) pollSide() {
	if a.tick%sidePollTicks != 0 {
		return
	}
	a.pollWatches(false)
	a.pollBreaks(false)
}

// pollWatches re-reads the watch list. force asks for a fresh read
// even mid-flight: the current response still lands, and one follow-up
// read runs right after it.
func (a *app) pollWatches(force bool) {
	if !a.connected {
		return
	}
	if a.side.watchInFlight {
		if force {
			a.side.watchQueued = true
		}
		return
	}
	a.side.watchInFlight = true
	a.send("watch", func(r client.Response) {
		a.side.watchInFlight = false
		queued := a.side.watchQueued
		a.side.watchQueued = false
		if r.Err == nil {
			var wl responses.WatchList
			if json.Unmarshal(r.Data, &wl) == nil {
				a.side.watches = wl.Watches
				a.rebuildWatchRows()
			}
		}
		if queued {
			a.pollWatches(true)
		}
	})
}

func (a *app) pollBreaks(force bool) {
	if !a.connected {
		return
	}
	if a.side.breakInFlight {
		if force {
			a.side.breakQueued = true
		}
		return
	}
	a.side.breakInFlight = true
	a.send("break", func(r client.Response) {
		a.side.breakInFlight = false
		queued := a.side.breakQueued
		a.side.breakQueued = false
		if r.Err == nil {
			var bl responses.BreakList
			if json.Unmarshal(r.Data, &bl) == nil {
				a.side.breaks = bl.Breaks
				a.rebuildBreakRows()
			}
		}
		if queued {
			a.pollBreaks(true)
		}
	})
}

// listRow creates one list entry: a remove button that sends removeCmd
// and the entry itself, which jumps the hex view to addr when clicked.
func (a *app) listRow(label string, hot bool, addr uint32, removeCmd string, after func()) *widget.Container {
	c := ui.Text
	if hot {
		c = ui.Bad
	}
	return ui.HRow(ui.Px(6),
		ui.Button("x", func(args *widget.ButtonClickedEventArgs) {
			a.send(removeCmd, func(r client.Response) {
				if out := formatResponse(r); out != "" {
					a.logf("%s", out)
				}
				after()
			})
		}),
		ui.LinkButton(label, c, func(args *widget.ButtonClickedEventArgs) {
			a.jumpTo(addr)
		}),
	)
}

func (a *app) rebuildWatchRows() {
	c := a.side.watchRows
	if c == nil {
		return
	}
	c.RemoveChildren()
	if len(a.side.watches) == 0 {
		c.AddChild(ui.Label("(none)", ui.TextSecondary))
		return
	}
	for _, w := range a.side.watches {
		val := "?"
		if w.Valid {
			val = fmt.Sprintf("%d (0x%0*X)", w.Value, w.Width/4, w.Value)
		}
		label := fmt.Sprintf("0x%08X w%-2d = %s", w.Addr, w.Width, val)
		hot := a.side.watchHeat[w.Addr] > 0
		c.AddChild(a.listRow(label, hot, w.Addr, fmt.Sprintf("unwatch 0x%08X", w.Addr),
			func() { a.pollWatches(true) }))
	}
}

func (a *app) rebuildBreakRows() {
	c := a.side.breakRows
	if c == nil {
		return
	}
	c.RemoveChildren()
	if len(a.side.breaks) == 0 {
		c.AddChild(ui.Label("(none)", ui.TextSecondary))
		return
	}
	for _, b := range a.side.breaks {
		cond := b.Op
		if b.HasVal {
			cond = fmt.Sprintf("%s %d", b.Op, b.Val)
		}
		label := fmt.Sprintf("0x%08X w%-2d %s", b.Addr, b.Width, cond)
		hot := a.side.breakHeat[b.Addr] > 0
		c.AddChild(a.listRow(label, hot, b.Addr, fmt.Sprintf("unbreak 0x%08X", b.Addr),
			func() { a.pollBreaks(true) }))
	}
}

// decayRowHeat steps the highlight timers down each tick.
func (a *app) decayRowHeat() {
	for addr, h := range a.side.watchHeat {
		if h <= 1 {
			delete(a.side.watchHeat, addr)
		} else {
			a.side.watchHeat[addr] = h - 1
		}
	}
	for addr, h := range a.side.breakHeat {
		if h <= 1 {
			delete(a.side.breakHeat, addr)
		} else {
			a.side.breakHeat[addr] = h - 1
		}
	}
}
