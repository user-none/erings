// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"runtime/trace"
	"time"
)

// serviceTraceRequest runs between frames on the emulation goroutine.
// Key 6 toggles a runtime execution trace: the first press starts
// recording into a timestamped file in the working directory, the next
// press stops it. Execution traces record every scheduler event and
// grow by several MB per second, so captures should be kept to the few
// seconds around the scene of interest. Quitting mid-capture leaves a
// truncated file.
func (g *game) serviceTraceRequest() {
	if !g.traceReq.CompareAndSwap(true, false) {
		return
	}

	if g.traceActive {
		trace.Stop()
		name := g.traceFile.Name()
		if err := g.traceFile.Close(); err != nil {
			fmt.Printf("[TRACE] close failed: %v\n", err)
		} else {
			fmt.Printf("[TRACE] wrote %s\n", name)
		}
		g.traceFile = nil
		g.traceActive = false
		return
	}

	name := fmt.Sprintf("trace_%s.out", time.Now().Format("20060102_150405"))
	f, err := os.Create(name)
	if err != nil {
		fmt.Printf("[TRACE] create failed: %v\n", err)
		return
	}
	if err := trace.Start(f); err != nil {
		fmt.Printf("[TRACE] start failed: %v\n", err)
		f.Close()
		os.Remove(name)
		return
	}
	g.traceFile = f
	g.traceActive = true
	fmt.Printf("[TRACE] capturing to %s, press 6 to stop\n", name)
}
