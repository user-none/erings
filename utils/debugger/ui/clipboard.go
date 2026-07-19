// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"runtime"

	"github.com/hajimehoshi/ebiten/v2"
	"golang.design/x/clipboard"
)

// clipboardReady tracks a successful clipboard.Init. Init can fail (no
// cgo, or no display server); every use retries so a late-appearing
// clipboard still starts working, and the read/write helpers below
// never touch the package while it is uninitialized, which would
// panic.
var clipboardReady bool

func clipboardInit() bool {
	if !clipboardReady {
		if err := clipboard.Init(); err == nil {
			clipboardReady = true
		}
	}
	return clipboardReady
}

// ClipboardWriteText puts s on the system clipboard. It reports false
// when no clipboard is available.
func ClipboardWriteText(s string) bool {
	if !clipboardInit() {
		return false
	}
	clipboard.Write(clipboard.FmtText, []byte(s))
	return true
}

// clipboardReadText returns the clipboard text, or nil when no
// clipboard is available or it holds no text.
func clipboardReadText() []byte {
	if !clipboardInit() {
		return nil
	}
	return clipboard.Read(clipboard.FmtText)
}

// ModPressed reports whether the platform's clipboard modifier is held:
// Cmd on macOS, Ctrl elsewhere.
func ModPressed() bool {
	if runtime.GOOS == "darwin" {
		return ebiten.IsKeyPressed(ebiten.KeyMeta) ||
			ebiten.IsKeyPressed(ebiten.KeyMetaLeft) ||
			ebiten.IsKeyPressed(ebiten.KeyMetaRight)
	}
	return ebiten.IsKeyPressed(ebiten.KeyControl) ||
		ebiten.IsKeyPressed(ebiten.KeyControlLeft) ||
		ebiten.IsKeyPressed(ebiten.KeyControlRight)
}

// ShiftPressed reports whether either shift key is held.
func ShiftPressed() bool {
	return ebiten.IsKeyPressed(ebiten.KeyShift) ||
		ebiten.IsKeyPressed(ebiten.KeyShiftLeft) ||
		ebiten.IsKeyPressed(ebiten.KeyShiftRight)
}
