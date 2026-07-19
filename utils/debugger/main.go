// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// The debugger is a standalone viewer for the debug launcher's console
// (utils/debug -c <port>). It connects over TCP and renders the
// console's state; it holds no authoritative state of its own.
package main

import (
	"errors"
	"flag"
	"log"

	"github.com/hajimehoshi/ebiten/v2"
)

func main() {
	addr := flag.String("connect", "127.0.0.1:5000", "console address to prefill in the connect panel")
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
