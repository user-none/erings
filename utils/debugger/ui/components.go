// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"image/color"

	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
)

// Button creates a standard text button.
func Button(label string, handler func(*widget.ButtonClickedEventArgs)) *widget.Button {
	return widget.NewButton(
		widget.ButtonOpts.Image(ButtonImage()),
		widget.ButtonOpts.Text(label, FontFace(), ButtonTextColor()),
		widget.ButtonOpts.TextPadding(widget.NewInsetsSimple(Px(6))),
		widget.ButtonOpts.ClickedHandler(handler),
	)
}

// PrimaryButton creates a prominent text button.
func PrimaryButton(label string, handler func(*widget.ButtonClickedEventArgs)) *widget.Button {
	return widget.NewButton(
		widget.ButtonOpts.Image(PrimaryButtonImage()),
		widget.ButtonOpts.Text(label, FontFace(), ButtonTextColor()),
		widget.ButtonOpts.TextPadding(widget.NewInsetsSimple(Px(6))),
		widget.ButtonOpts.ClickedHandler(handler),
	)
}

// LinkButton creates a text-styled button for clickable list entries:
// transparent at rest so it reads as a plain row, highlighted on
// hover. c is the label color.
func LinkButton(label string, c color.Color, handler func(*widget.ButtonClickedEventArgs)) *widget.Button {
	transparent := image.NewNineSliceColor(color.NRGBA{})
	return widget.NewButton(
		widget.ButtonOpts.Image(&widget.ButtonImage{
			Idle:     transparent,
			Hover:    image.NewNineSliceColor(PrimaryHover),
			Pressed:  image.NewNineSliceColor(Primary),
			Disabled: transparent,
		}),
		widget.ButtonOpts.Text(label, FontFace(), &widget.ButtonTextColor{
			Idle:     c,
			Disabled: TextSecondary,
		}),
		widget.ButtonOpts.TextPadding(widget.NewInsetsSimple(Px(2))),
		widget.ButtonOpts.ClickedHandler(handler),
	)
}

// TextInput creates a single-line text input.
func TextInput(placeholder string, minWidth int, opts ...widget.TextInputOpt) *widget.TextInput {
	all := append([]widget.TextInputOpt{
		widget.TextInputOpts.Image(&widget.TextInputImage{
			Idle:     image.NewNineSliceColor(Surface),
			Disabled: image.NewNineSliceColor(Border),
		}),
		widget.TextInputOpts.Face(FontFace()),
		widget.TextInputOpts.Color(&widget.TextInputColor{
			Idle:          Text,
			Disabled:      TextSecondary,
			Caret:         Text,
			DisabledCaret: TextSecondary,
		}),
		widget.TextInputOpts.Padding(widget.NewInsetsSimple(Px(4))),
		widget.TextInputOpts.Placeholder(placeholder),
		widget.TextInputOpts.AllowDuplicateSubmit(true),
		widget.TextInputOpts.WidgetOpts(
			widget.WidgetOpts.MinSize(Px(minWidth), 0),
		),
	}, opts...)
	return widget.NewTextInput(all...)
}

// Label creates a plain text label. The text centers vertically within
// the widget's rect: in packed rows the rect is the text's own height
// and this is a no-op, while in a stretched layout cell (grid columns,
// bar rows) it keeps the label on the row's centerline. GridLayoutData
// positioning cannot do this: the grid hands every child the full cell,
// so there is no smaller rect for it to position.
func Label(text string, c color.Color) *widget.Text {
	return widget.NewText(
		widget.TextOpts.Text(text, FontFace(), c),
		widget.TextOpts.Position(widget.TextPositionStart, widget.TextPositionCenter),
	)
}

// TextRowsHeight returns the pixel height of n text rows plus the
// standard cell padding, for fixing a list view's design height.
func TextRowsHeight(n int) int {
	_, lineH := cellMetrics()
	return int(lineH)*n + Px(8)
}

// HRow lays out children horizontally with the given spacing and
// centers each on the cross axis. Mixed-height children (buttons,
// text inputs, labels) stay visually aligned this way; a child with
// its own LayoutData keeps it.
func HRow(spacing int, children ...widget.PreferredSizeLocateableWidget) *widget.Container {
	c := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewRowLayout(
			widget.RowLayoutOpts.Direction(widget.DirectionHorizontal),
			widget.RowLayoutOpts.Spacing(spacing),
		)),
	)
	for _, child := range children {
		w := child.GetWidget()
		if w.LayoutData == nil {
			w.LayoutData = widget.RowLayoutData{Position: widget.RowLayoutPositionCenter}
		}
		c.AddChild(child)
	}
	return c
}

// scrollSlider creates the vertical scrollbar paired with a ScrollView.
func scrollSlider(view *ScrollView, needsScroll func() bool) *widget.Slider {
	return widget.NewSlider(
		widget.SliderOpts.TabOrder(-1),
		widget.SliderOpts.Direction(widget.DirectionVertical),
		widget.SliderOpts.MinMax(0, 1000),
		widget.SliderOpts.Images(
			&widget.SliderTrackImage{
				Idle:  image.NewNineSliceColor(Border),
				Hover: image.NewNineSliceColor(Border),
			},
			SliderButtonImage(),
		),
		widget.SliderOpts.FixedHandleSize(Px(40)),
		widget.SliderOpts.PageSizeFunc(func() int {
			if !needsScroll() {
				return 1000
			}
			viewHeight := view.ViewRect().Dy()
			contentHeight := view.ContentRect().Dy()
			return int(float64(viewHeight) / float64(contentHeight) * 1000)
		}),
		widget.SliderOpts.ChangedHandler(func(args *widget.SliderChangedEventArgs) {
			if !needsScroll() {
				view.ScrollTop = 0
				return
			}
			view.ScrollTop = float64(args.Current) / 1000
		}),
	)
}

// Scrollable wraps content in a ScrollView with a paired vertical
// scrollbar and mouse wheel support. It returns the view (for
// programmatic scrolling) and the wrapper to place in a layout.
func Scrollable(content widget.PreferredSizeLocateableWidget, bg color.Color) (*ScrollView, widget.PreferredSizeLocateableWidget) {
	view := NewScrollView(content, bg, true)

	needsScroll := func() bool {
		contentHeight := view.ContentRect().Dy()
		viewHeight := view.ViewRect().Dy()
		return contentHeight > 0 && viewHeight > 0 && contentHeight > viewHeight
	}

	slider := scrollSlider(view, needsScroll)
	view.SetSlider(slider)

	view.GetWidget().ScrolledEvent.AddHandler(func(args interface{}) {
		if !needsScroll() {
			view.ScrollTop = 0
			return
		}
		a := args.(*widget.WidgetScrolledEventArgs)
		view.SetScrollTop(view.ScrollTop + (a.Y * 0.05))
	})

	wrapper := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(2),
			widget.GridLayoutOpts.Stretch([]bool{true, false}, []bool{true}),
			widget.GridLayoutOpts.Spacing(Px(4), 0),
		)),
	)
	wrapper.AddChild(view)
	wrapper.AddChild(slider)
	return view, wrapper
}
