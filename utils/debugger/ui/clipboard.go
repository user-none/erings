// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"context"
	"runtime"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"golang.design/x/clipboard"
)

// clipboardTimeout bounds a clipboard call made from the UI thread. On
// X11 a read waits on the selection owner and can otherwise block for
// seconds.
const clipboardTimeout = time.Second

// clipboardAvailable reports whether clipboard.Init succeeded. Init
// fails without a display server. It runs once and later calls return
// the first result.
func clipboardAvailable() bool {
	return clipboard.Init() == nil
}

// ClipboardWriteText puts s on the system clipboard. It reports false
// when no clipboard is available or the write fails.
func ClipboardWriteText(s string) bool {
	if !clipboardAvailable() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	_, err := clipboard.Write(ctx, clipboard.FmtText, []byte(s))
	return err == nil
}

// clipboardReadText returns the clipboard text, or nil when no
// clipboard is available or it holds no text.
func clipboardReadText() []byte {
	if !clipboardAvailable() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	b, err := clipboard.Read(ctx, clipboard.FmtText)
	if err != nil {
		return nil
	}
	return b
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

// AltPressed reports whether either alt key is held.
func AltPressed() bool {
	return ebiten.IsKeyPressed(ebiten.KeyAlt) ||
		ebiten.IsKeyPressed(ebiten.KeyAltLeft) ||
		ebiten.IsKeyPressed(ebiten.KeyAltRight)
}
