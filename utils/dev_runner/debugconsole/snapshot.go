// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugconsole

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
func cmdSnapshot(c *Console, args []string) (any, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("usage: snapshot [name]")
	}
	name := defaultSnapshotName
	if len(args) == 1 {
		name = args[0]
	}
	state, err := c.machine.Serialize()
	if err != nil {
		return nil, fmt.Errorf("serialize failed: %v", err)
	}
	if c.snapshots == nil {
		c.snapshots = make(map[string][]byte)
	}
	c.snapshots[name] = state
	return fmt.Sprintf("snapshot %q saved (%.1fMB)", name, float64(len(state))/(1<<20)), nil
}

// snapshotNames returns the slot names sorted.
func snapshotNames(c *Console) []string {
	var names []string
	for n := range c.snapshots {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// snapshotList is the snapshots command response. The debugger does
// not use snapshots, so the type is console-local rather than shared.
type snapshotList struct {
	Snapshots []snapshotInfo `json:"snapshots"`
}

// snapshotInfo is one snapshot slot. Size is the serialized state size
// in bytes.
type snapshotInfo struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

// snapshotListText renders the snapshot list response for text mode.
func snapshotListText(r snapshotList) string {
	if len(r.Snapshots) == 0 {
		return "no snapshots"
	}
	var b strings.Builder
	for _, s := range r.Snapshots {
		fmt.Fprintf(&b, "%-16s %.1fMB\n", s.Name, float64(s.Size)/(1<<20))
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdSnapshots(c *Console, args []string) (any, error) {
	res := snapshotList{Snapshots: make([]snapshotInfo, 0, len(c.snapshots))}
	for _, n := range snapshotNames(c) {
		res.Snapshots = append(res.Snapshots, snapshotInfo{Name: n, Size: len(c.snapshots[n])})
	}
	return res, nil
}

// cmdRestore loads a named snapshot. Search state, watches, and the
// snapshot slots themselves are tool-side data and deliberately survive
// a restore. After a restore, rebase re-anchors the search baseline so
// the next filter intersects into the same surviving candidate set.
// Watches typically fire on the restored values, which is informative
// rather than spurious.
func cmdRestore(c *Console, args []string) (any, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("usage: restore [name]")
	}
	name := defaultSnapshotName
	if len(args) == 1 {
		name = args[0]
	}
	state, ok := c.snapshots[name]
	if !ok {
		if len(c.snapshots) == 0 {
			return nil, fmt.Errorf("no snapshots taken")
		}
		return nil, fmt.Errorf("no snapshot %q (have: %s)", name, strings.Join(snapshotNames(c), ", "))
	}
	if err := c.machine.Deserialize(state); err != nil {
		return nil, fmt.Errorf("restore failed: %v", err)
	}
	return fmt.Sprintf("restored %q", name), nil
}
