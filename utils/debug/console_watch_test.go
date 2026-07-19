// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestBEValue(t *testing.T) {
	cases := []struct {
		in   []byte
		want uint32
	}{
		{[]byte{0x00}, 0},
		{[]byte{0xFF}, 255},
		{[]byte{0x12, 0x34}, 0x1234},
		{[]byte{0x12, 0x34, 0x56, 0x78}, 0x12345678},
	}
	for _, c := range cases {
		if got := beValue(c.in); got != c.want {
			t.Errorf("beValue(% X) = 0x%X, want 0x%X", c.in, got, c.want)
		}
	}
}

func TestWatchValidation(t *testing.T) {
	g := &game{}
	for _, bad := range []string{
		"watch nonsense",
		"watch 0x05C00000",
		"watch 0x06000000 12",
		"watch 0x06000001 16",
		"watch 0x06000002 32",
		"watch 0x06000000 8 extra",
	} {
		if r := runLine(t, g, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if len(g.watches) != 0 {
		t.Fatalf("invalid commands added watches: %v", g.watches)
	}
}

func TestWatchAddListUnwatch(t *testing.T) {
	g := &game{}
	if r := runLine(t, g, "watch"); r != "no watches" {
		t.Fatalf("empty list response %q", r)
	}
	if r := runLine(t, g, "watch 0x060A3C42 16"); r != "watching 0x060A3C42 w16" {
		t.Fatalf("watch response %q", r)
	}
	if r := runLine(t, g, "watch 0x00200010"); r != "watching 0x00200010 w8" {
		t.Fatalf("watch response %q", r)
	}
	list := runLine(t, g, "watch")
	if !strings.Contains(list, "0x060A3C42 w16 = ?") || !strings.Contains(list, "0x00200010 w8 = ?") {
		t.Fatalf("list output:\n%s", list)
	}
	if r := runLine(t, g, "unwatch 0x060A3C42"); r != "unwatched 0x060A3C42" {
		t.Fatalf("unwatch response %q", r)
	}
	if len(g.watches) != 1 || g.watches[0].addr != 0x00200010 {
		t.Fatalf("watch list after unwatch: %v", g.watches)
	}
	if r := runLine(t, g, "unwatch 0x060A3C42"); !strings.HasPrefix(r, "error:") {
		t.Fatalf("double unwatch response %q", r)
	}
	if r := runLine(t, g, "unwatch all"); r != "removed 1 watches" {
		t.Fatalf("unwatch all response %q", r)
	}
	if len(g.watches) != 0 {
		t.Fatalf("watches remain after unwatch all: %v", g.watches)
	}
}

// TestWatchCanonicalization checks that mirror and partition spellings
// watch the same byte. A re-watch through a different spelling replaces
// the entry rather than adding a duplicate. unwatch accepts any
// spelling.
func TestWatchCanonicalization(t *testing.T) {
	g := &game{}
	runLine(t, g, "watch 0x06001000")
	if r := runLine(t, g, "watch 0x26101000 16"); r != "watching 0x06001000 w16" {
		t.Fatalf("re-watch response %q", r)
	}
	if len(g.watches) != 1 || g.watches[0].width != 16 {
		t.Fatalf("expected single replaced entry: %v", g.watches)
	}
	if r := runLine(t, g, "unwatch 0x07F01000"); r != "unwatched 0x06001000" {
		t.Fatalf("unwatch via mirror response %q", r)
	}
}

func TestWatchLimit(t *testing.T) {
	g := &game{}
	for i := 0; i < maxWatches; i++ {
		r := runLine(t, g, fmt.Sprintf("watch 0x%08X", 0x06000000+i*4))
		if !strings.HasPrefix(r, "watching") {
			t.Fatalf("watch %d failed: %q", i, r)
		}
	}
	if r := runLine(t, g, "watch 0x06001000"); !strings.HasPrefix(r, "error: watch limit") {
		t.Fatalf("over-limit response %q", r)
	}
}

func TestConsoleAttachBye(t *testing.T) {
	g := &game{}
	out := make(chan string, 4)

	attach := consoleCmd{attach: out, resp: make(chan string, 1)}
	g.runConsoleCommand(attach)
	if r := <-attach.resp; r != "" {
		t.Fatalf("attach response %q", r)
	}
	if g.consoleOut == nil {
		t.Fatal("attach did not take the output channel")
	}

	bye := consoleCmd{bye: true, resp: make(chan string, 1)}
	g.runConsoleCommand(bye)
	if r := <-bye.resp; r != "" {
		t.Fatalf("bye response %q", r)
	}
	if g.consoleOut != nil {
		t.Fatal("bye did not clear the output channel")
	}
}
