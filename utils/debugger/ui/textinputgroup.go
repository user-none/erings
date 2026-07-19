// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"

	"github.com/ebitenui/ebitenui/widget"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// TextInputGroup adds clipboard support to a set of text inputs. The
// host registers every input on the current screen and calls Update
// each frame; Ctrl/Cmd+A/C/V/X act on whichever registered input has
// focus. Rebuild the group alongside the widget tree so it never holds
// inputs from a torn-down screen.
type TextInputGroup struct {
	inputs []*widget.TextInput
}

// NewTextInputGroup creates an empty group.
func NewTextInputGroup() *TextInputGroup {
	return &TextInputGroup{}
}

// Add registers a text input for clipboard handling.
func (g *TextInputGroup) Add(input *widget.TextInput) {
	g.inputs = append(g.inputs, input)
}

// focusedInput returns the registered input that currently has focus,
// or nil.
func (g *TextInputGroup) focusedInput() *widget.TextInput {
	for _, input := range g.inputs {
		if input != nil && input.IsFocused() {
			return input
		}
	}
	return nil
}

// Focused reports whether any registered input has focus, so other
// keyboard consumers can yield the clipboard shortcuts.
func (g *TextInputGroup) Focused() bool {
	return g.focusedInput() != nil
}

// Update handles clipboard shortcuts for the focused input.
func (g *TextInputGroup) Update() {
	if !ModPressed() {
		return
	}
	focused := g.focusedInput()
	if focused == nil {
		return
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyA) {
		focused.SelectAll()
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyV) {
		if text := clipboardReadText(); text != nil {
			// Every input here is single-line and feeds the one-line
			// console protocol, so line breaks in pasted text become
			// spaces.
			s := strings.ReplaceAll(string(text), "\r", "")
			s = strings.ReplaceAll(s, "\n", " ")
			focused.DeleteSelectedText()
			focused.Insert(strings.TrimSpace(s))
		}
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyC) {
		if selected := focused.SelectedText(); selected != "" {
			ClipboardWriteText(selected)
		}
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyX) {
		if selected := focused.SelectedText(); selected != "" {
			if ClipboardWriteText(selected) {
				focused.DeleteSelectedText()
			}
		}
	}
}
