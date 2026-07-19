// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/ebitenui/ebitenui"
	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/user-none/erings/utils/debugger/ui"
)

// buildConnectScreen shows the centered connect panel: address entry, a
// Connect button, and a status/error line. status carries a connect
// failure or disconnect reason into the fresh widget tree.
func (a *app) buildConnectScreen(status string) {
	a.inputs = ui.NewTextInputGroup()
	root := widget.NewContainer(
		widget.ContainerOpts.BackgroundImage(image.NewNineSliceColor(ui.Background)),
		widget.ContainerOpts.Layout(widget.NewAnchorLayout()),
	)

	panel := widget.NewContainer(
		widget.ContainerOpts.BackgroundImage(image.NewNineSliceColor(ui.Surface)),
		widget.ContainerOpts.Layout(widget.NewRowLayout(
			widget.RowLayoutOpts.Direction(widget.DirectionVertical),
			widget.RowLayoutOpts.Spacing(ui.Px(10)),
			widget.RowLayoutOpts.Padding(widget.NewInsetsSimple(ui.Px(20))),
		)),
		widget.ContainerOpts.WidgetOpts(
			widget.WidgetOpts.LayoutData(widget.AnchorLayoutData{
				HorizontalPosition: widget.AnchorLayoutPositionCenter,
				VerticalPosition:   widget.AnchorLayoutPositionCenter,
			}),
		),
	)

	panel.AddChild(ui.Label("Saturn Debugger", ui.Text))
	panel.AddChild(ui.Label("debug server address (host:port)", ui.TextSecondary))

	a.addrInput = ui.TextInput("host:port", 260,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.startConnect()
		}),
	)
	a.addrInput.SetText(a.addr)
	a.inputs.Add(a.addrInput)
	panel.AddChild(a.addrInput)

	panel.AddChild(ui.PrimaryButton("Connect", func(args *widget.ButtonClickedEventArgs) {
		a.startConnect()
	}))

	a.connectErr = ui.Label(status, ui.Bad)
	panel.AddChild(a.connectErr)

	root.AddChild(panel)
	a.gui = &ebitenui.UI{Container: root}
}
