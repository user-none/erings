// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

func TestRestoreValidation(t *testing.T) {
	g := &game{}
	if r := runLine(t, g, "restore"); r != "error: no snapshots taken" {
		t.Fatalf("restore with no slots: %q", r)
	}
	if r := runLine(t, g, "restore a b"); !strings.HasPrefix(r, "error: usage") {
		t.Fatalf("restore arg count: %q", r)
	}
	g.snapshots = map[string][]byte{"boss": nil, "default": nil}
	r := runLine(t, g, "restore missing")
	if !strings.Contains(r, `no snapshot "missing"`) || !strings.Contains(r, "boss, default") {
		t.Fatalf("missing-slot error should list slots sorted: %q", r)
	}
}

func TestSnapshotValidation(t *testing.T) {
	g := &game{}
	if r := runLine(t, g, "snapshot a b"); !strings.HasPrefix(r, "error: usage") {
		t.Fatalf("snapshot arg count: %q", r)
	}
}

func TestSnapshotsList(t *testing.T) {
	g := &game{}
	if r := runLine(t, g, "snapshots"); r != "no snapshots" {
		t.Fatalf("empty list: %q", r)
	}
	g.snapshots = map[string][]byte{
		"boss":    make([]byte, 2<<20),
		"default": make([]byte, 1<<20),
	}
	r := runLine(t, g, "snapshots")
	lines := strings.Split(r, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "boss") || !strings.HasPrefix(lines[1], "default") {
		t.Fatalf("list output:\n%s", r)
	}
	if !strings.Contains(lines[0], "2.0MB") || !strings.Contains(lines[1], "1.0MB") {
		t.Fatalf("list sizes:\n%s", r)
	}
}
