// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

import (
	"strings"
	"testing"
)

func TestRestoreValidation(t *testing.T) {
	c := newTestServer()
	if r := runLine(t, c, "restore"); r != "error: no snapshots taken" {
		t.Fatalf("restore with no slots: %q", r)
	}
	if r := runLine(t, c, "restore a b"); !strings.HasPrefix(r, "error: usage") {
		t.Fatalf("restore arg count: %q", r)
	}
	c.snapshots = map[string][]byte{"boss": nil, "default": nil}
	r := runLine(t, c, "restore missing")
	if !strings.Contains(r, `no snapshot "missing"`) || !strings.Contains(r, "boss, default") {
		t.Fatalf("missing-slot error should list slots sorted: %q", r)
	}
}

func TestSnapshotValidation(t *testing.T) {
	c := newTestServer()
	if r := runLine(t, c, "snapshot a b"); !strings.HasPrefix(r, "error: usage") {
		t.Fatalf("snapshot arg count: %q", r)
	}
}

func TestSnapshotsList(t *testing.T) {
	c := newTestServer()
	if r := runLine(t, c, "snapshots"); r != "no snapshots" {
		t.Fatalf("empty list: %q", r)
	}
	c.snapshots = map[string][]byte{
		"boss":    make([]byte, 2<<20),
		"default": make([]byte, 1<<20),
	}
	r := runLine(t, c, "snapshots")
	lines := strings.Split(r, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "boss") || !strings.HasPrefix(lines[1], "default") {
		t.Fatalf("list output:\n%s", r)
	}
	if !strings.Contains(lines[0], "2.0MB") || !strings.Contains(lines[1], "1.0MB") {
		t.Fatalf("list sizes:\n%s", r)
	}
}
