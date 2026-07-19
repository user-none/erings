// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package ui holds the debugger's ebitenui styling and shared widget
// constructors. The debugger uses one fixed dark theme and a monospace
// face everywhere: every panel is addresses, hex, and values.
package ui

import (
	"bytes"
	"image/color"
	"log"

	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/gomono"
)

// Theme colors.
var (
	Background    = color.NRGBA{0x14, 0x14, 0x1a, 0xff}
	Surface       = color.NRGBA{0x20, 0x20, 0x2a, 0xff}
	Primary       = color.NRGBA{0x1e, 0x40, 0x7a, 0xff}
	PrimaryHover  = color.NRGBA{0x2a, 0x50, 0x8a, 0xff}
	Text          = color.NRGBA{0xe6, 0xe6, 0xe6, 0xff}
	TextSecondary = color.NRGBA{0x8a, 0x8a, 0x8a, 0xff}
	Bad           = color.NRGBA{0xe0, 0x50, 0x50, 0xff}
	Border        = color.NRGBA{0x30, 0x30, 0x3e, 0xff}
)

const baseFontSize = 13.0

// dpiScale is the device pixel ratio. All logical pixel values go
// through Px so the UI renders at native resolution on scaled displays.
var dpiScale = 1.0

// Px converts a logical pixel value to physical pixels.
func Px(logical int) int {
	return int(float64(logical) * dpiScale)
}

// SetDPIScale sets the device scale factor and rebuilds the font face.
// The caller rebuilds its widget tree afterwards; existing widgets keep
// measurements from the old face.
func SetDPIScale(scale float64) {
	if scale < 1.0 {
		scale = 1.0
	}
	dpiScale = scale
	buildFontFace()
}

// sharedFontSource is the parsed monospace font, loaded once.
var sharedFontSource *text.GoTextFaceSource

// fontFace is the active face. Widgets hold &fontFace, so rebuilding
// replaces the value in place and never leaves a nil face behind.
var fontFace text.Face

func loadFontSource() *text.GoTextFaceSource {
	if sharedFontSource == nil {
		source, err := text.NewGoTextFaceSource(bytes.NewReader(gomono.TTF))
		if err != nil {
			log.Printf("failed to load font source: %v", err)
			return nil
		}
		sharedFontSource = source
	}
	return sharedFontSource
}

func buildFontFace() {
	source := loadFontSource()
	if source == nil {
		return
	}
	fontFace = &text.GoTextFace{
		Source: source,
		Size:   baseFontSize * dpiScale,
	}
}

// FontFace returns the UI font face.
func FontFace() *text.Face {
	if fontFace == nil {
		buildFontFace()
	}
	return &fontFace
}

// ButtonImage is the standard button image set.
func ButtonImage() *widget.ButtonImage {
	return &widget.ButtonImage{
		Idle:     image.NewNineSliceColor(Surface),
		Hover:    image.NewNineSliceColor(PrimaryHover),
		Pressed:  image.NewNineSliceColor(Primary),
		Disabled: image.NewNineSliceColor(Border),
	}
}

// PrimaryButtonImage is the prominent button image set.
func PrimaryButtonImage() *widget.ButtonImage {
	return &widget.ButtonImage{
		Idle:     image.NewNineSliceColor(Primary),
		Hover:    image.NewNineSliceColor(PrimaryHover),
		Pressed:  image.NewNineSliceColor(Surface),
		Disabled: image.NewNineSliceColor(Border),
	}
}

// ButtonTextColor is the standard button text color set.
func ButtonTextColor() *widget.ButtonTextColor {
	return &widget.ButtonTextColor{
		Idle:     Text,
		Disabled: TextSecondary,
	}
}

// SliderButtonImage is the scrollbar handle image set.
func SliderButtonImage() *widget.ButtonImage {
	return &widget.ButtonImage{
		Idle:     image.NewNineSliceColor(Primary),
		Hover:    image.NewNineSliceColor(PrimaryHover),
		Pressed:  image.NewNineSliceColor(Primary),
		Disabled: image.NewNineSliceColor(Border),
	}
}
