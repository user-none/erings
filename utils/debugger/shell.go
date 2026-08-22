// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ebitenui/ebitenui"
	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/user-none/erings/utils/debugger/client"
	"github.com/user-none/erings/utils/debugger/ui"
)

// buildMainScreen shows the connected layout: execution controls on
// top, the event log in the middle, and the raw command line at the
// bottom.
func (a *app) buildMainScreen() {
	a.inputs = ui.NewTextInputGroup()
	root := widget.NewContainer(
		widget.ContainerOpts.BackgroundImage(image.NewNineSliceColor(ui.Background)),
		widget.ContainerOpts.Layout(widget.NewAnchorLayout()),
	)

	content := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(1),
			widget.GridLayoutOpts.Stretch([]bool{true}, []bool{false, false, true, false}),
			widget.GridLayoutOpts.Padding(widget.NewInsetsSimple(ui.Px(8))),
			widget.GridLayoutOpts.Spacing(0, ui.Px(8)),
		)),
		widget.ContainerOpts.WidgetOpts(
			widget.WidgetOpts.LayoutData(widget.AnchorLayoutData{
				StretchHorizontal: true,
				StretchVertical:   true,
			}),
		),
	)

	content.AddChild(a.buildTopBar())
	content.AddChild(a.buildMiddle())
	content.AddChild(a.buildLog())
	content.AddChild(a.buildCommandLine())

	root.AddChild(content)
	a.gui = &ebitenui.UI{Container: root}
	a.updateStatus()
}

// buildTopBar creates the execution controls and the status text.
func (a *app) buildTopBar() *widget.Container {
	bar := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(2),
			widget.GridLayoutOpts.Stretch([]bool{true, false}, []bool{true}),
			widget.GridLayoutOpts.Spacing(ui.Px(8), 0),
		)),
	)

	a.stepInput = ui.TextInput("n", 60,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.step()
		}),
	)
	a.stepInput.SetText("1")
	a.inputs.Add(a.stepInput)

	buttons := ui.HRow(ui.Px(6),
		ui.Button("Pause", func(args *widget.ButtonClickedEventArgs) {
			a.sendLogged("pause")
		}),
		ui.Button("Resume", func(args *widget.ButtonClickedEventArgs) {
			a.sendLogged("resume")
		}),
		ui.Button("Step", func(args *widget.ButtonClickedEventArgs) {
			a.step()
		}),
		a.stepInput,
	)
	bar.AddChild(buttons)

	a.statusText = ui.Label("", ui.TextSecondary)
	bar.AddChild(a.statusText)

	return bar
}

// buildMiddle creates the main two-column area, which holds its
// preferred height: every piece inside has a fixed design size, so
// window growth goes to the log below. The left column is locked to
// the hex view's content width, with the memory panel on top and the
// search panel below. The right column absorbs the remaining width and
// splits the same height between the tabbed watches/pins panel and the
// breaks panel.
func (a *app) buildMiddle() *widget.Container {
	middle := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(2),
			widget.GridLayoutOpts.Stretch([]bool{false, true}, []bool{true}),
			widget.GridLayoutOpts.Spacing(ui.Px(8), 0),
		)),
	)

	left := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(1),
			widget.GridLayoutOpts.Stretch([]bool{true}, []bool{false, true}),
			widget.GridLayoutOpts.Spacing(0, ui.Px(8)),
		)),
	)
	left.AddChild(a.buildMemoryPanel())
	left.AddChild(a.buildSearchPanel())
	middle.AddChild(left)

	right := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(1),
			widget.GridLayoutOpts.Stretch([]bool{true}, []bool{true, true}),
			widget.GridLayoutOpts.Spacing(0, ui.Px(8)),
		)),
	)
	right.AddChild(a.buildListTabsPanel())
	right.AddChild(a.buildBreakPanel())
	middle.AddChild(right)

	return middle
}

// buildLog creates the scrollable event log with drag selection. Its
// row is the layout's stretch row, so the log takes all window height
// left over after the fixed sections and is always visible even when
// empty.
func (a *app) buildLog() widget.PreferredSizeLocateableWidget {
	a.logWidget = ui.NewLogView(a.logBuf)
	view, wrapper := ui.Scrollable(a.logWidget, ui.Surface)
	a.logView = view
	if a.logFollow {
		view.SetScrollTop(1)
	}
	return wrapper
}

// buildCommandLine creates the raw server command entry.
func (a *app) buildCommandLine() *widget.Container {
	row := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(2),
			widget.GridLayoutOpts.Stretch([]bool{false, true}, []bool{true}),
			widget.GridLayoutOpts.Spacing(ui.Px(6), 0),
		)),
	)
	row.AddChild(ui.Label(">", ui.TextSecondary))

	a.cmdInput = ui.TextInput("server command (help lists commands)", 200,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			line := strings.TrimSpace(args.InputText)
			if line == "" {
				return
			}
			a.cmdInput.SetText("")
			a.logf("> %s", line)
			a.sendLogged(line)
		}),
	)
	a.inputs.Add(a.cmdInput)
	row.AddChild(a.cmdInput)
	return row
}

// sendLogged sends a command, logs its response, and refreshes the
// status bar in case the command changed execution state.
func (a *app) sendLogged(line string) {
	a.send(line, func(r client.Response) {
		if out := formatResponse(r); out != "" {
			a.logf("%s", out)
		}
		a.pollState()
	})
}

// step runs the frame command with the count from the step input.
func (a *app) step() {
	n := 1
	if s := strings.TrimSpace(a.stepInput.GetText()); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			a.logf("step count must be a positive integer")
			return
		}
		n = v
	}
	a.sendLogged(fmt.Sprintf("frame %d", n))
}

// pollState requests a one-off status refresh outside the periodic
// cadence, used right after commands that change execution state.
func (a *app) pollState() {
	if a.stateInFlight {
		return
	}
	a.stateInFlight = true
	a.send("state", func(r client.Response) {
		a.stateInFlight = false
		a.onState(r)
	})
}

// updateStatus renders the status bar from the mirrored state.
func (a *app) updateStatus() {
	if a.statusText == nil {
		return
	}
	run := "running"
	if a.paused {
		run = "PAUSED"
	}
	a.statusText.Label = fmt.Sprintf("%s  frame %d  %s", a.addr, a.frame, run)
}
