// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// runLine dispatches one command line and returns the immediate response,
// or "" if the response was deferred.
func runLine(t *testing.T, g *game, line string) string {
	t.Helper()
	cmd := consoleCmd{line: line, resp: make(chan string, 1)}
	g.runConsoleCommand(cmd)
	select {
	case r := <-cmd.resp:
		return r
	default:
		return ""
	}
}

func TestConsoleUnknownCommand(t *testing.T) {
	g := &game{}
	r := runLine(t, g, "bogus")
	if !strings.HasPrefix(r, "error: unknown command") {
		t.Fatalf("unexpected response %q", r)
	}
}

func TestConsolePauseResume(t *testing.T) {
	g := &game{}
	if r := runLine(t, g, "pause"); r != "paused" {
		t.Fatalf("pause response %q", r)
	}
	if !g.paused.Load() {
		t.Fatal("pause did not set the flag")
	}
	if r := runLine(t, g, "resume"); r != "resumed" {
		t.Fatalf("resume response %q", r)
	}
	if g.paused.Load() {
		t.Fatal("resume did not clear the flag")
	}
}

func TestConsoleFrameRequiresPause(t *testing.T) {
	g := &game{}
	if r := runLine(t, g, "frame"); r != "error: not paused" {
		t.Fatalf("unexpected response %q", r)
	}
}

func TestConsoleFrameArgValidation(t *testing.T) {
	g := &game{}
	g.paused.Store(true)
	for _, bad := range []string{"frame 0", "frame -3", "frame x"} {
		if r := runLine(t, g, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
}

func TestConsoleFrameDefersResponse(t *testing.T) {
	g := &game{}
	g.paused.Store(true)
	cmd := consoleCmd{line: "frame 3", resp: make(chan string, 1)}
	g.runConsoleCommand(cmd)
	select {
	case r := <-cmd.resp:
		t.Fatalf("expected deferred response, got %q", r)
	default:
	}
	if g.stepResp == nil {
		t.Fatal("dispatcher did not stash the deferred response channel")
	}
	if g.stepRemaining != 3 {
		t.Fatalf("stepRemaining = %d, want 3", g.stepRemaining)
	}
}

// TestConsoleStepFlow drives the full frame-step sequence the way the
// emulation loop and serviceConsole interleave. The step holds later
// commands until the frames have run. Then the deferred response fires
// and the held commands execute.
func TestConsoleStepFlow(t *testing.T) {
	g := &game{console: &console{cmds: make(chan consoleCmd, 16)}}
	g.paused.Store(true)

	step := consoleCmd{line: "frame 2", resp: make(chan string, 1)}
	held := consoleCmd{line: "resume", resp: make(chan string, 1)}
	g.console.cmds <- step
	g.console.cmds <- held

	// The first service starts the step and holds the resume.
	g.serviceConsole()
	if g.stepRemaining != 2 || g.stepResp == nil {
		t.Fatalf("step not started: remaining=%d resp=%v", g.stepRemaining, g.stepResp)
	}
	select {
	case r := <-held.resp:
		t.Fatalf("held command ran early: %q", r)
	default:
	}

	// Emulation loop runs one frame per iteration while stepping.
	for i := 0; i < 2; i++ {
		if g.stepRemaining <= 0 {
			t.Fatalf("iteration %d: nothing left to step", i)
		}
		g.stepRemaining--
		g.serviceConsole()
	}

	if r := <-step.resp; r != "stepped" {
		t.Fatalf("step response %q", r)
	}
	if r := <-held.resp; r != "resumed" {
		t.Fatalf("held command response %q", r)
	}
	if g.paused.Load() {
		t.Fatal("held resume did not clear pause")
	}
}

// TestConsoleStepAbortOnUnpause covers a keyboard unpause arriving while
// a step is in flight. The step completes its response instead of
// holding the queue forever.
func TestConsoleStepAbortOnUnpause(t *testing.T) {
	g := &game{console: &console{cmds: make(chan consoleCmd, 16)}}
	g.paused.Store(true)

	step := consoleCmd{line: "frame 10", resp: make(chan string, 1)}
	g.console.cmds <- step
	g.serviceConsole()

	g.paused.Store(false) // keyboard toggle mid-step
	g.serviceConsole()

	if r := <-step.resp; r != "stepped" {
		t.Fatalf("step response %q", r)
	}
	if g.stepRemaining != 0 {
		t.Fatalf("stepRemaining = %d, want 0", g.stepRemaining)
	}
}

func TestConsoleHelpListsCommands(t *testing.T) {
	g := &game{}
	r := runLine(t, g, "help")
	for _, name := range []string{"pause", "resume", "frame", "prompt", "help"} {
		if !strings.Contains(r, name) {
			t.Fatalf("help output missing %q:\n%s", name, r)
		}
	}
}

func TestConsolePromptToggle(t *testing.T) {
	g := &game{console: &console{prompt: true}}

	if r := runLine(t, g, "prompt off"); r != "" {
		t.Fatalf("prompt off should be silent, got %q", r)
	}
	if g.console.prompt {
		t.Fatal("prompt off did not clear the setting")
	}
	if r := runLine(t, g, "prompt on"); r != "" {
		t.Fatalf("prompt on should be silent, got %q", r)
	}
	if !g.console.prompt {
		t.Fatal("prompt on did not set the setting")
	}
	for _, bad := range []string{"prompt", "prompt x", "prompt on off"} {
		if r := runLine(t, g, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
}
