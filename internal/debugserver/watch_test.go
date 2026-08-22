// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

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
	c := newTestServer()
	for _, bad := range []string{
		"watch nonsense",
		"watch 0x05C00000",
		"watch 0x06000000 12",
		"watch 0x06000001 16",
		"watch 0x06000002 32",
		"watch 0x06000000 8 extra",
	} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if len(c.watches) != 0 {
		t.Fatalf("invalid commands added watches: %v", c.watches)
	}
}

func TestWatchAddListUnwatch(t *testing.T) {
	c := newTestServer()
	if r := runLine(t, c, "watch"); r != "no watches" {
		t.Fatalf("empty list response %q", r)
	}
	if r := runLine(t, c, "watch 0x060A3C42 16"); r != "watching 0x060A3C42 w16" {
		t.Fatalf("watch response %q", r)
	}
	if r := runLine(t, c, "watch 0x00200010"); r != "watching 0x00200010 w8" {
		t.Fatalf("watch response %q", r)
	}
	list := runLine(t, c, "watch")
	if !strings.Contains(list, "0x060A3C42 w16 = ?") || !strings.Contains(list, "0x00200010 w8 = ?") {
		t.Fatalf("list output:\n%s", list)
	}
	if r := runLine(t, c, "unwatch 0x060A3C42"); r != "unwatched 0x060A3C42" {
		t.Fatalf("unwatch response %q", r)
	}
	if len(c.watches) != 1 || c.watches[0].addr != 0x00200010 {
		t.Fatalf("watch list after unwatch: %v", c.watches)
	}
	if r := runLine(t, c, "unwatch 0x060A3C42"); !strings.HasPrefix(r, "error:") {
		t.Fatalf("double unwatch response %q", r)
	}
	if r := runLine(t, c, "unwatch all"); r != "removed 1 watches" {
		t.Fatalf("unwatch all response %q", r)
	}
	if len(c.watches) != 0 {
		t.Fatalf("watches remain after unwatch all: %v", c.watches)
	}
}

// TestWatchRejectsNonCanonical checks that mirror and partition
// spellings are outside the known regions: watch targets are canonical
// addresses only. A re-watch at the canonical address replaces the
// entry rather than adding a duplicate.
func TestWatchRejectsNonCanonical(t *testing.T) {
	c := newTestServer()
	runLine(t, c, "watch 0x06001000")
	for _, bad := range []string{"watch 0x26101000 16", "unwatch 0x07F01000"} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if r := runLine(t, c, "watch 0x06001000 16"); r != "watching 0x06001000 w16" {
		t.Fatalf("re-watch response %q", r)
	}
	if len(c.watches) != 1 || c.watches[0].width != 16 {
		t.Fatalf("expected single replaced entry: %v", c.watches)
	}
	if r := runLine(t, c, "unwatch 0x06001000"); r != "unwatched 0x06001000" {
		t.Fatalf("unwatch response %q", r)
	}
}

func TestWatchLimit(t *testing.T) {
	c := newTestServer()
	for i := 0; i < maxWatches; i++ {
		r := runLine(t, c, fmt.Sprintf("watch 0x%08X", 0x06000000+i*4))
		if !strings.HasPrefix(r, "watching") {
			t.Fatalf("watch %d failed: %q", i, r)
		}
	}
	if r := runLine(t, c, "watch 0x06001000"); !strings.HasPrefix(r, "error: watch limit") {
		t.Fatalf("over-limit response %q", r)
	}
}

func TestServerAttachBye(t *testing.T) {
	c := newTestServer()
	out := make(chan string, 4)

	attach := clientCmd{attach: out, resp: make(chan string, 1)}
	c.runCommand(attach)
	if r := <-attach.resp; r != "" {
		t.Fatalf("attach response %q", r)
	}
	if c.out == nil {
		t.Fatal("attach did not take the output channel")
	}

	bye := clientCmd{bye: true, resp: make(chan string, 1)}
	c.runCommand(bye)
	if r := <-bye.resp; r != "" {
		t.Fatalf("bye response %q", r)
	}
	if c.out != nil {
		t.Fatal("bye did not clear the output channel")
	}
}
