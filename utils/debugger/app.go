// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ebitenui/ebitenui"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/user-none/erings/internal/debugconsoletypes"
	"github.com/user-none/erings/utils/debugger/client"
	"github.com/user-none/erings/utils/debugger/ui"
)

// statePollTicks is how often the connected app refreshes the status
// bar with the state command, in 60Hz update ticks.
const statePollTicks = 30

// maxLogLines bounds the event log scrollback.
const maxLogLines = 1000

// pendingCmd is one in-flight console command. Update polls ch and
// calls done with the response when it arrives. done runs on the
// ebiten goroutine, so handlers touch app and widget state freely, but
// must not mutate the pending list itself.
type pendingCmd struct {
	ch   <-chan client.Response
	done func(client.Response)
}

// dialResult carries a background Dial's outcome to the update loop.
type dialResult struct {
	cl  *client.Client
	err error
}

// app is the debugger application. Everything runs on the ebiten
// goroutine except client.Dial (its own goroutine, reporting through
// dialCh) and the client's internals.
type app struct {
	gui *ebitenui.UI

	// inputs is the clipboard handler for the current screen's text
	// inputs. Each build*Screen replaces it so it only holds live
	// widgets.
	inputs *ui.TextInputGroup

	// connected is true when cl is usable. cl is nil otherwise.
	connected bool
	cl        *client.Client
	addr      string

	// Connect screen widgets.
	addrInput  *widget.TextInput
	connectErr *widget.Text
	connecting bool
	dialCh     chan dialResult

	// Main screen widgets.
	statusText *widget.Text
	logView    *ui.ScrollView
	logWidget  *ui.LogView
	cmdInput   *widget.TextInput
	stepInput  *widget.TextInput

	// logBuf holds the log lines and their selection for the app
	// lifetime; it survives screen rebuilds and reconnects as history.
	// logFollow tracks whether the log sticks to the bottom on append;
	// it disengages while scrolled up or selecting.
	logBuf    *ui.LogBuffer
	logFollow bool

	// mem, side, and search are the panel states (memory.go,
	// panels.go, search.go).
	mem    memory
	side   side
	search searchPanel

	// paused and frame mirror the last state response (or a break
	// event) for the status bar.
	paused bool
	frame  uint64

	pending []pendingCmd

	tick          uint64
	stateInFlight bool

	// scale is the device scale the current widget tree was built at.
	// Layout detects changes and requests a rebuild.
	scale   float64
	rescale bool
}

func newApp(addr string) *app {
	a := &app{
		addr:      addr,
		dialCh:    make(chan dialResult, 1),
		scale:     1.0,
		logBuf:    ui.NewLogBuffer(maxLogLines),
		logFollow: true,
	}
	ui.SetDPIScale(1.0)
	a.buildConnectScreen("")
	return a
}

func (a *app) close() {
	if a.cl != nil {
		a.cl.Close()
	}
}

func (a *app) Update() error {
	if a.rescale {
		a.rescale = false
		ui.SetDPIScale(a.scale)
		a.rebuild()
	}

	if a.connected {
		a.updateMain()
	} else {
		a.updateConnect()
	}

	a.inputs.Update()
	a.gui.Update()
	a.reconcileSelections()
	a.updateLogFollow()
	return nil
}

// handleCopyShortcut routes Ctrl/Cmd+C to whichever panel owns the
// current selection: the hex view (with Shift for a raw hex string) or
// the log. At most one selection exists at a time (see
// reconcileSelections), and focused text inputs keep the shortcut for
// their own text.
func (a *app) handleCopyShortcut() {
	if a.inputs.Focused() || !ui.ModPressed() || !inpututil.IsKeyJustPressed(ebiten.KeyC) {
		return
	}
	var s string
	switch {
	case a.mem.hexView != nil && a.mem.hexView.HasSelection():
		if ui.ShiftPressed() {
			s = a.mem.hexView.SelectedHex()
		} else {
			s = a.mem.hexView.SelectedDump()
		}
	case a.logBuf.HasSelection():
		s = a.logBuf.SelectedText()
	}
	if s == "" {
		return
	}
	if !ui.ClipboardWriteText(s) {
		a.logf("clipboard unavailable")
	}
}

// reconcileSelections keeps one selection alive at a time: starting a
// drag in the hex view or the log clears the other's selection, so the
// copy shortcut is never ambiguous.
func (a *app) reconcileSelections() {
	if a.mem.hexView == nil || a.logWidget == nil {
		return
	}
	if a.logWidget.Dragging() {
		a.mem.hexView.ClearSelection()
	} else if a.mem.hexView.Dragging() {
		a.logBuf.ClearSelection()
	}
}

// updateLogFollow recomputes stick-to-bottom: following while the log
// is at the bottom (or fits) and no selection drag is in progress.
func (a *app) updateLogFollow() {
	if a.logView == nil || a.logWidget == nil {
		return
	}
	fits := a.logView.ContentRect().Dy() <= a.logView.ViewRect().Dy()
	a.logFollow = (fits || a.logView.ScrollTop >= 0.999) && !a.logWidget.Dragging()
}

func (a *app) Draw(screen *ebiten.Image) {
	a.gui.Draw(screen)
}

func (a *app) Layout(outsideWidth, outsideHeight int) (int, int) {
	s := 1.0
	if m := ebiten.Monitor(); m != nil {
		s = m.DeviceScaleFactor()
	}
	if s != a.scale {
		a.scale = s
		a.rescale = true
	}
	return int(float64(outsideWidth) * s), int(float64(outsideHeight) * s)
}

// rebuild reconstructs the current screen's widget tree, preserving the
// app-level state it renders.
func (a *app) rebuild() {
	if a.connected {
		a.buildMainScreen()
	} else {
		a.buildConnectScreen(a.connectErr.Label)
	}
}

// updateConnect polls for the outcome of a background dial.
func (a *app) updateConnect() {
	select {
	case r := <-a.dialCh:
		a.connecting = false
		if r.err != nil {
			a.connectErr.Label = r.err.Error()
			return
		}
		a.cl = r.cl
		a.connected = true
		a.buildMainScreen()
		a.logf("connected to %s", a.addr)
		a.refresh()
	default:
	}
}

// startConnect kicks off a background dial with the entered address.
func (a *app) startConnect() {
	if a.connecting {
		return
	}
	addr := strings.TrimSpace(a.addrInput.GetText())
	if addr == "" {
		a.connectErr.Label = "enter host:port"
		return
	}
	a.addr = addr
	a.connecting = true
	a.connectErr.Label = "connecting..."
	go func() {
		cl, err := client.Dial(addr)
		a.dialCh <- dialResult{cl: cl, err: err}
	}()
}

// updateMain drains pushed events, polls pending command responses, and
// keeps the status bar fresh.
func (a *app) updateMain() {
drain:
	for {
		select {
		case ev, ok := <-a.cl.Events():
			if !ok {
				a.disconnect()
				return
			}
			a.handleEvent(ev)
		default:
			break drain
		}
	}

	a.pollPending()
	a.handleCopyShortcut()

	a.tick++
	a.pollMemory()
	a.pollSide()
	a.pollSearch()
	a.decayRowHeat()
	if a.tick%statePollTicks == 0 && !a.stateInFlight {
		a.stateInFlight = true
		a.send("state", func(r client.Response) {
			a.stateInFlight = false
			a.onState(r)
		})
	}
}

// disconnect drops back to the connect screen. All server-derived state
// is cleared: the runner may come back as a different session. The
// event log is history, not server state, and survives so the reason
// for a drop stays visible after reconnecting.
func (a *app) disconnect() {
	reason := "connection closed"
	if err := a.cl.Err(); err != nil {
		reason = err.Error()
	}
	a.cl.Close()
	a.cl = nil
	a.connected = false
	a.pending = nil
	a.stateInFlight = false
	a.paused = false
	a.frame = 0
	a.mem = memory{}
	a.side = side{}
	a.search = searchPanel{}
	a.logf("disconnected: %s", reason)
	a.buildConnectScreen(reason)
}

func (a *app) handleEvent(ev client.Event) {
	switch ev.Kind {
	case "watch":
		a.logf("[WATCH] frame=%d 0x%08X w%d: %d -> %d",
			ev.Frame, ev.Addr, ev.Width, ev.Prev, ev.Cur)
		a.side.watchHeat[ev.Addr] = rowHeatTicks
		a.pollWatches(true)
	case "break":
		a.paused = true
		a.updateStatus()
		a.logf("[BREAK] frame=%d 0x%08X w%d: %d -> %d (%s) paused",
			ev.Frame, ev.Addr, ev.Width, ev.Prev, ev.Cur, ev.Cond)
		a.side.breakHeat[ev.Addr] = rowHeatTicks
		a.rebuildBreakRows()
	}
}

// send queues a console command. done (optional) runs on the ebiten
// goroutine when the response arrives.
func (a *app) send(line string, done func(client.Response)) {
	if !a.connected {
		return
	}
	a.pending = append(a.pending, pendingCmd{ch: a.cl.Do(line), done: done})
}

func (a *app) pollPending() {
	// Compact first, run callbacks after: a done handler may send a
	// follow-up command, which appends to a.pending, and running it
	// mid-compaction would lose that entry when the compacted slice is
	// assigned back.
	type completed struct {
		fn   func(client.Response)
		resp client.Response
	}
	var ready []completed
	kept := a.pending[:0]
	for _, p := range a.pending {
		select {
		case r := <-p.ch:
			if p.done != nil {
				ready = append(ready, completed{fn: p.done, resp: r})
			}
		default:
			kept = append(kept, p)
		}
	}
	a.pending = kept
	for _, c := range ready {
		c.fn(c.resp)
	}
}

// refresh re-reads all server state after a connect. The console keeps
// watches, breaks, and search state across client sessions, so the view
// is rebuilt from its answers, never from anything remembered locally.
func (a *app) refresh() {
	a.stateInFlight = true
	a.send("state", func(r client.Response) {
		a.stateInFlight = false
		a.onState(r)
	})
	a.pollWatches(true)
	a.pollBreaks(true)
	a.fetchRegions()
}

func (a *app) onState(r client.Response) {
	if r.Err != nil {
		return
	}
	var s debugconsoletypes.StateResult
	if json.Unmarshal(r.Data, &s) != nil {
		return
	}
	a.paused = s.Paused
	a.frame = s.Frame
	a.updateStatus()
	a.setSearchState(s.SearchActive, s.Candidates)
}

// logf appends to the event log. Multi-line messages become one log
// line each so the scrollback cap counts what is actually displayed.
// The view sticks to the bottom only while follow mode is engaged.
func (a *app) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	for _, l := range strings.Split(line, "\n") {
		a.logBuf.Append(l)
	}
	if a.logView != nil && a.logFollow {
		a.logView.SetScrollTop(1)
	}
}

// formatResponse renders a command response for the event log: plain
// messages as-is, structured payloads as indented JSON.
func formatResponse(r client.Response) string {
	if r.Err != nil {
		return "error: " + r.Err.Error()
	}
	var s string
	if json.Unmarshal(r.Data, &s) == nil {
		return s
	}
	var buf bytes.Buffer
	if json.Indent(&buf, r.Data, "", "  ") != nil {
		return string(r.Data)
	}
	return buf.String()
}
