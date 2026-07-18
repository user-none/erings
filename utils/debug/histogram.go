// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"sort"
)

// serviceHistRequest arms or dumps the SH-2 PC histogram. Runs between
// frames so SetSH2Trace never mutates TraceFunc while a core is executing.
// The maps are written by the trace closures (called inside RunFrame on the
// emulation goroutine) and read here - no other goroutine touches them.
// First request arms capture, second prints the top PCs and disarms.
func (g *game) serviceHistRequest() {
	if !g.histReq.Swap(false) {
		return
	}
	if !g.histActive {
		g.histMaster = make(map[uint32]uint64, 4096)
		g.histSlave = make(map[uint32]uint64, 4096)
		// op == 0xFFFF is the interrupt-accept synthetic call,
		// not an executed instruction - exclude it so this is a
		// pure instruction histogram.
		counter := func(m map[uint32]uint64) func(uint32, uint16) {
			return func(pc uint32, op uint16) {
				if op != 0xFFFF {
					m[pc]++
				}
			}
		}
		g.emu.SetSH2Trace(counter(g.histMaster), counter(g.histSlave))
		g.histActive = true
		fmt.Println("[HIST] armed - capturing SH-2 master/slave PCs")
	} else {
		g.emu.SetSH2Trace(nil, nil)
		g.histActive = false
		printHistogram("master", g.histMaster)
		printHistogram("slave", g.histSlave)
		g.histMaster, g.histSlave = nil, nil
	}
}

// printHistogram prints the 20 most-executed PCs from a captured SH-2
// trace to stdout, sorted by sample count. The header reports total
// samples and the unique-PC count - a small unique count concentrated
// in a few PCs indicates a tight spin/poll loop, a large one indicates
// the core is making normal progress.
func printHistogram(label string, h map[uint32]uint64) {
	type row struct {
		pc  uint32
		cnt uint64
	}
	rows := make([]row, 0, len(h))
	var total uint64
	for pc, c := range h {
		rows = append(rows, row{pc, c})
		total += c
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].cnt > rows[j].cnt })

	fmt.Printf("[HIST] SH-2 %s  total=%d  uniquePCs=%d  (top 20)\n",
		label, total, len(rows))
	n := 20
	if len(rows) < n {
		n = len(rows)
	}
	for i := 0; i < n; i++ {
		pct := 0.0
		if total > 0 {
			pct = float64(rows[i].cnt) * 100 / float64(total)
		}
		fmt.Printf("  0x%08X  %12d  %6.2f%%\n", rows[i].pc, rows[i].cnt, pct)
	}
}
