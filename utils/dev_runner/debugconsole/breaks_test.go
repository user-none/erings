// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugconsole

import (
	"fmt"
	"strings"
	"testing"
)

func TestBreakValidation(t *testing.T) {
	c := newTestConsole()
	if r := runLine(t, c, "break"); r != "no breaks" {
		t.Fatalf("empty list response %q", r)
	}
	for _, bad := range []string{
		"break 0x06000000",
		"break nonsense eq 0",
		"break 0x05C00000 eq 0",
		"break 0x06000000 bogus",
		"break 0x06000000 eq",
		"break 0x06000000 eq xyz",
		"break 0x06000000 eq 300",
		"break 0x06000000 eq 0 12",
		"break 0x06000001 diff 16",
		"break 0x06000000 eq 0 8 extra",
		"break 0x06000000 dec 5",
	} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if len(c.breaks) != 0 {
		t.Fatalf("invalid commands added breaks: %v", c.breaks)
	}
}

func TestBreakAddListUnbreak(t *testing.T) {
	c := newTestConsole()
	if r := runLine(t, c, "break 0x0605C973 eq 0"); r != "break 0x0605C973 w8 eq 0" {
		t.Fatalf("break response %q", r)
	}
	if r := runLine(t, c, "break 0x06001000 dec 16"); r != "break 0x06001000 w16 dec" {
		t.Fatalf("break response %q", r)
	}
	list := runLine(t, c, "break")
	if !strings.Contains(list, "0x0605C973 w8 eq 0") || !strings.Contains(list, "0x06001000 w16 dec") {
		t.Fatalf("list output:\n%s", list)
	}
	// Re-adding replaces the entry.
	if r := runLine(t, c, "break 0x0605C973 lt 3"); r != "break 0x0605C973 w8 lt 3" {
		t.Fatalf("replace response %q", r)
	}
	if len(c.breaks) != 2 {
		t.Fatalf("replace added a duplicate: %v", c.breaks)
	}
	if r := runLine(t, c, "unbreak 0x06001000"); r != "unbreak 0x06001000" {
		t.Fatalf("unbreak response %q", r)
	}
	if r := runLine(t, c, "unbreak 0x06001000"); !strings.HasPrefix(r, "error:") {
		t.Fatalf("double unbreak response %q", r)
	}
	if r := runLine(t, c, "unbreak all"); r != "removed 1 breaks" {
		t.Fatalf("unbreak all response %q", r)
	}
}

func TestBreakLimit(t *testing.T) {
	c := newTestConsole()
	for i := 0; i < maxBreaks; i++ {
		r := runLine(t, c, fmt.Sprintf("break 0x%08X diff", 0x06000000+i*4))
		if !strings.HasPrefix(r, "break") {
			t.Fatalf("break %d failed: %q", i, r)
		}
	}
	if r := runLine(t, c, "break 0x06001000 diff"); !strings.HasPrefix(r, "error: break limit") {
		t.Fatalf("over-limit response %q", r)
	}
}

// TestBreakFiresOnEdge drives the health-hits-zero scenario: the break
// seeds without firing, fires once when the condition becomes true,
// stays quiet while it holds, and fires again after it clears and
// returns.
func TestBreakFiresOnEdge(t *testing.T) {
	c, m := newFakeConsole()
	c.out = make(chan string, 4)
	m.wram[fakeAddr] = 3

	runLine(t, c, "break 0x06001000 eq 0")
	c.Service(200) // seed
	if c.paused.Load() {
		t.Fatal("seed fired the break")
	}

	m.wram[fakeAddr] = 0
	c.Service(201)
	if !c.paused.Load() {
		t.Fatal("break did not pause")
	}
	select {
	case line := <-c.out:
		want := "[BREAK] frame=201 0x06001000 w8: 3 -> 0 (eq 0) paused\n"
		if line != want {
			t.Fatalf("break line %q, want %q", line, want)
		}
	default:
		t.Fatal("no break line pushed")
	}

	// Held condition does not re-fire.
	c.paused.Store(false)
	c.Service(202)
	if c.paused.Load() {
		t.Fatal("held condition re-fired")
	}

	// Clearing and returning fires again.
	m.wram[fakeAddr] = 3
	c.Service(203)
	m.wram[fakeAddr] = 0
	c.Service(204)
	if !c.paused.Load() {
		t.Fatal("break did not fire after the condition returned")
	}
}

// TestBreakAlreadyTrueAtAdd covers adding a break whose condition
// already holds: it must not fire until the condition clears and comes
// back.
func TestBreakAlreadyTrueAtAdd(t *testing.T) {
	c, m := newFakeConsole()
	m.wram[fakeAddr] = 0

	runLine(t, c, "break 0x06001000 eq 0")
	c.Service(300)
	c.Service(301)
	if c.paused.Load() {
		t.Fatal("already-true condition fired")
	}

	m.wram[fakeAddr] = 5
	c.Service(302)
	m.wram[fakeAddr] = 0
	c.Service(303)
	if !c.paused.Load() {
		t.Fatal("break did not fire on the edge")
	}
}

// TestBreakCancelsStep covers a break firing during a frame step: the
// remaining step frames are cancelled so stepping stops at the break.
func TestBreakCancelsStep(t *testing.T) {
	c, m := newFakeConsole()
	m.wram[fakeAddr] = 1
	runLine(t, c, "break 0x06001000 eq 0")
	c.Service(400) // seed

	c.paused.Store(true)
	c.stepRemaining = 50
	m.wram[fakeAddr] = 0
	c.Service(401)
	if c.stepRemaining != 0 {
		t.Fatalf("stepRemaining = %d, want 0", c.stepRemaining)
	}
	if !c.paused.Load() {
		t.Fatal("break did not pause")
	}
}
