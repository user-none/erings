// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"slices"
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

// listTab identifies which list the tabbed panel shows.
type listTab int

const (
	tabWatches listTab = iota
	tabPins
)

// tabTitles are the tab strip labels in listTab order.
var tabTitles = []string{"Watches", "Pins"}

// side is the watches/pins/breaks panel state. The row lists are
// rebuilt from the last server response; nothing here is
// authoritative.
type side struct {
	// tab is the list the tabbed panel shows. Watches and pins share
	// one row container, one scroll view, and one quick-add slot, so
	// only the active list is built and polled. Pins hold a value
	// rather than report one, so nothing is missed while the tab is
	// away: a watch change still reaches the event log.
	tab      listTab
	tabBtns  []*widget.Button
	listRows *widget.Container
	listView *ui.ScrollView
	addHost  *widget.Container
	addRows  []*widget.Container

	breakRows *widget.Container

	// The row signatures identify the rows currently built (address,
	// width, condition, heat). A poll whose signature matches leaves the
	// widgets alone instead of rebuilding, so widget identity survives
	// and in-progress hovers and clicks are not dropped. The button
	// slices hold each list's link buttons in row order for in-place
	// label updates. nil signatures force a build for a fresh container,
	// which is also how a tab switch discards the other list's rows.
	watchRowSig  []string
	watchRowBtns []*widget.Button
	pinRowSig    []string
	pinRowBtns   []*widget.Button
	breakRowSig  []string

	watches []responses.WatchInfo
	pins    []responses.PinInfo
	breaks  []responses.BreakInfo

	// watchHeat, pinHeat, and breakHeat hold per-address highlight
	// ticks. Watch and break heat is set by pushed events. Pins have no
	// event, so pinHits records the last count seen per address and heat
	// is set when a poll shows it moved.
	watchHeat map[uint32]int
	pinHeat   map[uint32]int
	breakHeat map[uint32]int
	pinHits   map[uint32]uint64

	// One request per list is outstanding at a time; a forced refresh
	// arriving mid-flight queues exactly one follow-up.
	watchInFlight bool
	watchQueued   bool
	pinInFlight   bool
	pinQueued     bool
	breakInFlight bool
	breakQueued   bool

	// Quick-add rows, one per list. The breaks row adds the condition
	// operator (cycled) and its comparison value. Pins hold a byte
	// string rather than a value at a width, so their row has no width
	// control.
	watchAddr     *widget.TextInput
	watchAddWidth int

	pinAddr *widget.TextInput
	pinVal  *widget.TextInput

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
		a.side.pinHeat = map[uint32]int{}
		a.side.breakHeat = map[uint32]int{}
		a.side.pinHits = map[uint32]uint64{}
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
// list, and the quick-add row pinned beneath it. The header is a widget
// rather than a title so a panel can carry a tab strip instead of a
// label. It returns the panel and the list's scroll view.
func sideListPanel(header widget.PreferredSizeLocateableWidget, rows *widget.Container, addRow widget.PreferredSizeLocateableWidget) (*widget.Container, *ui.ScrollView) {
	panel := widget.NewContainer(
		widget.ContainerOpts.BackgroundImage(image.NewNineSliceColor(ui.Surface)),
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(1),
			widget.GridLayoutOpts.Stretch([]bool{true}, []bool{false, true, false}),
			widget.GridLayoutOpts.Padding(widget.NewInsetsSimple(ui.Px(6))),
			widget.GridLayoutOpts.Spacing(0, ui.Px(6)),
		)),
	)
	panel.AddChild(header)
	view, wrap := ui.Scrollable(rows, ui.Surface)
	// The list's preferred height is a fixed baseline so the middle
	// section's height stays deterministic. The left column is taller,
	// so the panel's stretch row grows the list past this to match.
	view.MinHeight = ui.TextRowsHeight(sideListRows)
	view.MaxHeight = view.MinHeight
	panel.AddChild(wrap)
	panel.AddChild(addRow)
	return panel, view
}

func listRowsContainer() *widget.Container {
	return widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewRowLayout(
			widget.RowLayoutOpts.Direction(widget.DirectionVertical),
			widget.RowLayoutOpts.Spacing(ui.Px(2)),
		)),
	)
}

// buildListTabsPanel creates the tabbed watches/pins panel. Both lists
// share the row container and the quick-add slot, and the tab strip
// selects which one fills them.
func (a *app) buildListTabsPanel() *widget.Container {
	a.ensureSideDefaults()
	a.side.listRows = listRowsContainer()
	a.side.watchRowSig = nil
	a.side.watchRowBtns = nil
	a.side.pinRowSig = nil
	a.side.pinRowBtns = nil

	a.side.addRows = []*widget.Container{a.buildWatchAddRow(), a.buildPinAddRow()}
	a.side.addHost = widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewAnchorLayout()),
	)
	a.side.addHost.AddChild(a.side.addRows[a.side.tab])

	panel, view := sideListPanel(a.buildTabStrip(), a.side.listRows, a.side.addHost)
	a.side.listView = view
	a.rebuildActiveRows()
	return panel
}

// buildTabStrip creates the watches/pins selector. The active tab is
// bracketed, the same convention the region selector uses.
func (a *app) buildTabStrip() *widget.Container {
	a.side.tabBtns = nil
	items := make([]widget.PreferredSizeLocateableWidget, 0, len(tabTitles))
	for i := range tabTitles {
		tab := listTab(i)
		btn := ui.Button(tabLabel(tab, a.side.tab), func(args *widget.ButtonClickedEventArgs) {
			a.setTab(tab)
		})
		a.side.tabBtns = append(a.side.tabBtns, btn)
		items = append(items, btn)
	}
	return ui.HRow(ui.Px(4), items...)
}

func tabLabel(tab, active listTab) string {
	if tab == active {
		return "[" + tabTitles[tab] + "]"
	}
	return tabTitles[tab]
}

// setTab switches the tabbed panel to tab. Both lists' row signatures
// are dropped so the incoming list builds from scratch over the
// outgoing one, and the scroll position resets: the two lists have
// unrelated lengths, so carrying a scroll fraction across is
// meaningless.
func (a *app) setTab(tab listTab) {
	if a.side.tab == tab {
		return
	}
	// The outgoing quick-add row leaves the widget tree. Clear its
	// focus first: a detached input that still reports itself focused
	// would hold the clipboard shortcuts away from the hex view and the
	// log for the rest of the session.
	for _, in := range a.tabInputs(a.side.tab) {
		if in != nil {
			in.Focus(false)
		}
	}
	a.side.tab = tab
	a.side.watchRowSig = nil
	a.side.pinRowSig = nil
	for i, btn := range a.side.tabBtns {
		btn.Text().Label = tabLabel(listTab(i), tab)
	}
	a.side.addHost.RemoveChildren()
	a.side.addHost.AddChild(a.side.addRows[tab])
	if a.side.listView != nil {
		a.side.listView.SetScrollTop(0)
	}
	a.rebuildActiveRows()
	a.pollActiveList(true)
}

// tabInputs returns the quick-add text inputs belonging to tab.
func (a *app) tabInputs(tab listTab) []*widget.TextInput {
	if tab == tabPins {
		return []*widget.TextInput{a.side.pinAddr, a.side.pinVal}
	}
	return []*widget.TextInput{a.side.watchAddr}
}

// rebuildActiveRows rebuilds whichever list the tab strip is showing.
func (a *app) rebuildActiveRows() {
	if a.side.tab == tabPins {
		a.rebuildPinRows()
		return
	}
	a.rebuildWatchRows()
}

// pollActiveList re-reads whichever list the tab strip is showing. The
// hidden list is not polled: it is not on screen, and its rows are
// rebuilt from a forced read on the next switch.
func (a *app) pollActiveList(force bool) {
	if a.side.tab == tabPins {
		a.pollPins(force)
		return
	}
	a.pollWatches(force)
}

// buildBreakPanel creates the breaks list panel. Breaks stay outside
// the tab strip: a break fires on its own and pauses emulation, so its
// list is never hidden.
func (a *app) buildBreakPanel() *widget.Container {
	a.ensureSideDefaults()
	a.side.breakRows = listRowsContainer()
	a.side.breakRowSig = nil
	panel, _ := sideListPanel(ui.Label("Breaks", ui.TextSecondary), a.side.breakRows, a.buildBreakAddRow())
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

// buildPinAddRow creates the pin quick-add controls: address entry,
// the held bytes as a hex string, and the add button. A pin holds a
// byte string rather than a value at a width, so there is no width
// control.
func (a *app) buildPinAddRow() *widget.Container {
	a.side.pinAddr = ui.TextInput("address", 130,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.addPin()
		}),
	)
	a.inputs.Add(a.side.pinAddr)

	a.side.pinVal = ui.TextInput("hex", 90,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.addPin()
		}),
	)
	a.inputs.Add(a.side.pinVal)

	return ui.HRow(ui.Px(4),
		a.side.pinAddr,
		a.side.pinVal,
		ui.Button("+Pin", func(args *widget.ButtonClickedEventArgs) {
			a.addPin()
		}),
	)
}

// addPin sends a pin command for the quick-add entry and refreshes the
// list.
func (a *app) addPin() {
	addr := strings.TrimSpace(a.side.pinAddr.GetText())
	val := strings.TrimSpace(a.side.pinVal.GetText())
	if addr == "" {
		return
	}
	if val == "" {
		a.logf("pin needs the bytes to hold")
		return
	}
	a.send(fmt.Sprintf("pin %s %s", addr, val), func(r client.Response) {
		if out := formatResponse(r); out != "" {
			a.logf("%s", out)
		}
		a.pollPins(true)
	})
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

// pollSide refreshes the visible lists on their cadence.
func (a *app) pollSide() {
	if a.tick%sidePollTicks != 0 {
		return
	}
	a.pollActiveList(false)
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

// pollPins re-reads the pin list. A pin has no pushed event, so the
// response is also where a hit is noticed: an entry whose count moved
// since the last read gets its row highlighted, the same signal a
// watch row gets when it fires.
func (a *app) pollPins(force bool) {
	if !a.connected {
		return
	}
	if a.side.pinInFlight {
		if force {
			a.side.pinQueued = true
		}
		return
	}
	a.side.pinInFlight = true
	a.send("pin", func(r client.Response) {
		a.side.pinInFlight = false
		queued := a.side.pinQueued
		a.side.pinQueued = false
		if r.Err == nil {
			var pl responses.PinList
			if json.Unmarshal(r.Data, &pl) == nil {
				a.side.pins = pl.Pins
				a.markPinHits()
				a.rebuildPinRows()
			}
		}
		if queued {
			a.pollPins(true)
		}
	})
}

// markPinHits highlights the rows whose hit count moved since the last
// read and re-seeds the counts. The map is rebuilt from the response so
// removed pins do not accumulate in it. An address seen for the first
// time only seeds: a pin added to a location the emulator is already
// writing would otherwise light up before it has done anything.
func (a *app) markPinHits() {
	hits := make(map[uint32]uint64, len(a.side.pins))
	for _, p := range a.side.pins {
		if prev, ok := a.side.pinHits[p.Addr]; ok && p.Hits != prev {
			a.side.pinHeat[p.Addr] = rowHeatTicks
		}
		hits[p.Addr] = p.Hits
	}
	a.side.pinHits = hits
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
// The entry's link button is returned alongside the row for in-place
// label updates.
func (a *app) listRow(label string, hot bool, addr uint32, removeCmd string, after func()) (*widget.Container, *widget.Button) {
	c := ui.Text
	if hot {
		c = ui.Bad
	}
	link := ui.LinkButton(label, c, func(args *widget.ButtonClickedEventArgs) {
		a.jumpTo(addr)
	})
	row := ui.HRow(ui.Px(6),
		ui.Button("x", func(args *widget.ButtonClickedEventArgs) {
			a.send(removeCmd, func(r client.Response) {
				if out := formatResponse(r); out != "" {
					a.logf("%s", out)
				}
				after()
			})
		}),
		link,
	)
	return row, link
}

// watchRowLabel renders one watch list entry.
func watchRowLabel(w responses.WatchInfo) string {
	val := "?"
	if w.Valid {
		val = fmt.Sprintf("%d (0x%0*X)", w.Value, w.Width/4, w.Value)
	}
	return fmt.Sprintf("0x%08X w%-2d = %s", w.Addr, w.Width, val)
}

// rebuildWatchRows reconciles the watch list with the last response.
// While the row set and heat are unchanged only the labels update, on
// the existing buttons; recreating the rows would drop the hover and
// any in-progress click under the cursor on every poll.
func (a *app) rebuildWatchRows() {
	c := a.side.listRows
	if c == nil || a.side.tab != tabWatches {
		return
	}
	sig := make([]string, 0, len(a.side.watches))
	for _, w := range a.side.watches {
		sig = append(sig, fmt.Sprintf("%08X/%d/%t", w.Addr, w.Width, a.side.watchHeat[w.Addr] > 0))
	}
	if a.side.watchRowSig != nil && slices.Equal(sig, a.side.watchRowSig) {
		for i, w := range a.side.watches {
			a.side.watchRowBtns[i].Text().Label = watchRowLabel(w)
		}
		c.RequestRelayout()
		return
	}
	a.side.watchRowSig = sig
	a.side.watchRowBtns = nil
	c.RemoveChildren()
	if len(a.side.watches) == 0 {
		c.AddChild(ui.Label("(none)", ui.TextSecondary))
		return
	}
	for _, w := range a.side.watches {
		hot := a.side.watchHeat[w.Addr] > 0
		row, link := a.listRow(watchRowLabel(w), hot, w.Addr, fmt.Sprintf("unwatch 0x%08X", w.Addr),
			func() { a.pollWatches(true) })
		a.side.watchRowBtns = append(a.side.watchRowBtns, link)
		c.AddChild(row)
	}
}

// pinRowLabel renders one pin list entry. Bytes are upper case to
// match the hex dump.
func pinRowLabel(p responses.PinInfo) string {
	return fmt.Sprintf("0x%08X = %s hits=%d", p.Addr, strings.ToUpper(p.Data), p.Hits)
}

// rebuildPinRows reconciles the pin list with the last response, with
// the same identity preservation as rebuildWatchRows. The hit count is
// part of the label but not the signature, so a counting pin updates
// its label in place instead of rebuilding its row every poll.
func (a *app) rebuildPinRows() {
	c := a.side.listRows
	if c == nil || a.side.tab != tabPins {
		return
	}
	sig := make([]string, 0, len(a.side.pins))
	for _, p := range a.side.pins {
		sig = append(sig, fmt.Sprintf("%08X/%s/%t", p.Addr, p.Data, a.side.pinHeat[p.Addr] > 0))
	}
	if a.side.pinRowSig != nil && slices.Equal(sig, a.side.pinRowSig) {
		for i, p := range a.side.pins {
			a.side.pinRowBtns[i].Text().Label = pinRowLabel(p)
		}
		c.RequestRelayout()
		return
	}
	a.side.pinRowSig = sig
	a.side.pinRowBtns = nil
	c.RemoveChildren()
	if len(a.side.pins) == 0 {
		c.AddChild(ui.Label("(none)", ui.TextSecondary))
		return
	}
	for _, p := range a.side.pins {
		hot := a.side.pinHeat[p.Addr] > 0
		row, link := a.listRow(pinRowLabel(p), hot, p.Addr, fmt.Sprintf("unpin 0x%08X", p.Addr),
			func() { a.pollPins(true) })
		a.side.pinRowBtns = append(a.side.pinRowBtns, link)
		c.AddChild(row)
	}
}

// breakRowLabel renders one break list entry.
func breakRowLabel(b responses.BreakInfo) string {
	cond := b.Op
	if b.HasVal {
		cond = fmt.Sprintf("%s %d", b.Op, b.Val)
	}
	return fmt.Sprintf("0x%08X w%-2d %s", b.Addr, b.Width, cond)
}

// rebuildBreakRows reconciles the break list with the last response,
// with the same identity preservation as rebuildWatchRows. Break labels
// are static, so a matching signature means nothing to update at all.
func (a *app) rebuildBreakRows() {
	c := a.side.breakRows
	if c == nil {
		return
	}
	sig := make([]string, 0, len(a.side.breaks))
	for _, b := range a.side.breaks {
		sig = append(sig, fmt.Sprintf("%s/%t", breakRowLabel(b), a.side.breakHeat[b.Addr] > 0))
	}
	if a.side.breakRowSig != nil && slices.Equal(sig, a.side.breakRowSig) {
		return
	}
	a.side.breakRowSig = sig
	c.RemoveChildren()
	if len(a.side.breaks) == 0 {
		c.AddChild(ui.Label("(none)", ui.TextSecondary))
		return
	}
	for _, b := range a.side.breaks {
		hot := a.side.breakHeat[b.Addr] > 0
		row, _ := a.listRow(breakRowLabel(b), hot, b.Addr, fmt.Sprintf("unbreak 0x%08X", b.Addr),
			func() { a.pollBreaks(true) })
		c.AddChild(row)
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
	for addr, h := range a.side.pinHeat {
		if h <= 1 {
			delete(a.side.pinHeat, addr)
		} else {
			a.side.pinHeat[addr] = h - 1
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
