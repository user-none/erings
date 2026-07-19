// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// The debugger renders the state served by a debug server over TCP. It
// switches the session to JSON mode and holds no authoritative state
// of its own; every panel is a view of the server's answers.
package main

import (
	"errors"
	"flag"
	"log"

	"github.com/hajimehoshi/ebiten/v2"
)

func main() {
	addr := flag.String("connect", "127.0.0.1:5000", "debug server address to prefill in the connect panel")
	flag.Parse()

	ebiten.SetWindowTitle("Saturn Debugger")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowSize(1150, 880)
	ebiten.SetWindowSizeLimits(950, 780, -1, -1)
	ebiten.SetTPS(60)

	app := newApp(*addr)
	err := ebiten.RunGame(app)
	app.close()

	// ebiten.Termination is the sentinel for a normal window close.
	if err != nil && !errors.Is(err, ebiten.Termination) {
		log.Fatal(err)
	}
}
