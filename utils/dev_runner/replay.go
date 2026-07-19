// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import "fmt"

// serviceScreenshotRequest appends a screenshot marker to the active
// recording. Consumed between frames so it marks the frame just completed
// (recorder.frame has advanced past it).
func (g *game) serviceScreenshotRequest() {
	if !g.screenshotReq.Swap(false) || g.recorder == nil {
		return
	}
	f := g.recorder.MarkScreenshot()
	fmt.Printf("[REPLAY] screenshot marked at frame %d\n", f)
}
