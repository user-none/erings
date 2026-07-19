// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"sort"
	"strings"
)

// defaultSnapshotName is the slot used when snapshot/restore is given no
// name.
const defaultSnapshotName = "default"

// cmdSnapshot captures the full state into an in-memory named slot.
// Slots are session-scoped and nothing touches disk.
func cmdSnapshot(g *game, args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("usage: snapshot [name]")
	}
	name := defaultSnapshotName
	if len(args) == 1 {
		name = args[0]
	}
	state, err := g.emu.Serialize()
	if err != nil {
		return "", fmt.Errorf("serialize failed: %v", err)
	}
	if g.snapshots == nil {
		g.snapshots = make(map[string][]byte)
	}
	g.snapshots[name] = state
	return fmt.Sprintf("snapshot %q saved (%.1fMB)", name, float64(len(state))/(1<<20)), nil
}

// snapshotNames returns the slot names sorted.
func snapshotNames(g *game) []string {
	var names []string
	for n := range g.snapshots {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func cmdSnapshots(g *game, args []string) (string, error) {
	if len(g.snapshots) == 0 {
		return "no snapshots", nil
	}
	var b strings.Builder
	for _, n := range snapshotNames(g) {
		fmt.Fprintf(&b, "%-16s %.1fMB\n", n, float64(len(g.snapshots[n]))/(1<<20))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// cmdRestore loads a named snapshot. Search state, watches, and the
// snapshot slots themselves are tool-side data and deliberately survive
// a restore. After a restore the next filter intersects into the same
// surviving candidate set. Watches typically fire on the restored
// values, which is informative rather than spurious.
func cmdRestore(g *game, args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("usage: restore [name]")
	}
	name := defaultSnapshotName
	if len(args) == 1 {
		name = args[0]
	}
	state, ok := g.snapshots[name]
	if !ok {
		if len(g.snapshots) == 0 {
			return "", fmt.Errorf("no snapshots taken")
		}
		return "", fmt.Errorf("no snapshot %q (have: %s)", name, strings.Join(snapshotNames(g), ", "))
	}
	if err := g.emu.Deserialize(state); err != nil {
		return "", fmt.Errorf("restore failed: %v", err)
	}
	return fmt.Sprintf("restored %q", name), nil
}
