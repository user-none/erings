// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// startWatchdog spawns a goroutine that polls frameTick. If the
// counter stops advancing for stallThreshold, it prints which stage
// the loop was last in and dumps every goroutine's stack to stderr,
// then resets so it can fire again if the lockup intermittently
// recovers.
func (g *game) startWatchdog() {
	const (
		pollInterval   = 1 * time.Second
		stallThreshold = 5 * time.Second
	)
	go func() {
		var lastTick uint64
		var lastChange = time.Now()
		var fired bool
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for range ticker.C {
			cur := g.frameTick.Load()
			if cur != lastTick {
				lastTick = cur
				lastChange = time.Now()
				fired = false
				continue
			}
			if fired || time.Since(lastChange) < stallThreshold {
				continue
			}
			fired = true
			st := g.stage.Load()
			fmt.Fprintf(os.Stderr,
				"\n[WATCHDOG] emulation loop stalled for %v at stage=%s, frameTick=%d\n",
				time.Since(lastChange).Round(time.Millisecond), stageName(st), cur)
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			os.Stderr.Write(buf[:n])
			fmt.Fprintln(os.Stderr, "[WATCHDOG] end of stack dump")
		}
	}()
}
