// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package console

import (
	"strings"
	"sync/atomic"
	"testing"
)

// newTestConsole builds a Console with the fields dispatch-only tests
// need. Tests that touch memory set machine to a fakeMachine.
func newTestConsole() *Console {
	return &Console{paused: new(atomic.Bool)}
}

// runLine dispatches one command line and returns the immediate response,
// or "" if the response was deferred.
func runLine(t *testing.T, c *Console, line string) string {
	t.Helper()
	cmd := consoleCmd{line: line, resp: make(chan string, 1)}
	c.runCommand(cmd)
	select {
	case r := <-cmd.resp:
		return r
	default:
		return ""
	}
}

func TestConsoleUnknownCommand(t *testing.T) {
	c := newTestConsole()
	r := runLine(t, c, "bogus")
	if !strings.HasPrefix(r, "error: unknown command") {
		t.Fatalf("unexpected response %q", r)
	}
}

func TestConsolePauseResume(t *testing.T) {
	c := newTestConsole()
	if r := runLine(t, c, "pause"); r != "paused" {
		t.Fatalf("pause response %q", r)
	}
	if !c.paused.Load() {
		t.Fatal("pause did not set the flag")
	}
	if r := runLine(t, c, "resume"); r != "resumed" {
		t.Fatalf("resume response %q", r)
	}
	if c.paused.Load() {
		t.Fatal("resume did not clear the flag")
	}
}

func TestConsoleFrameRequiresPause(t *testing.T) {
	c := newTestConsole()
	if r := runLine(t, c, "frame"); r != "error: not paused" {
		t.Fatalf("unexpected response %q", r)
	}
}

func TestConsoleFrameArgValidation(t *testing.T) {
	c := newTestConsole()
	c.paused.Store(true)
	for _, bad := range []string{"frame 0", "frame -3", "frame x"} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
}

func TestConsoleFrameDefersResponse(t *testing.T) {
	c := newTestConsole()
	c.paused.Store(true)
	cmd := consoleCmd{line: "frame 3", resp: make(chan string, 1)}
	c.runCommand(cmd)
	select {
	case r := <-cmd.resp:
		t.Fatalf("expected deferred response, got %q", r)
	default:
	}
	if c.stepResp == nil {
		t.Fatal("dispatcher did not stash the deferred response channel")
	}
	if c.stepRemaining != 3 {
		t.Fatalf("stepRemaining = %d, want 3", c.stepRemaining)
	}
}

// TestConsoleStepFlow drives the full frame-step sequence the way the
// emulation loop and serviceCommands interleave. The step holds later
// commands until the frames have run. Then the deferred response fires
// and the held commands execute.
func TestConsoleStepFlow(t *testing.T) {
	c := newTestConsole()
	c.cmds = make(chan consoleCmd, 16)
	c.paused.Store(true)

	step := consoleCmd{line: "frame 2", resp: make(chan string, 1)}
	held := consoleCmd{line: "resume", resp: make(chan string, 1)}
	c.cmds <- step
	c.cmds <- held

	// The first service starts the step and holds the resume.
	c.serviceCommands()
	if c.stepRemaining != 2 || c.stepResp == nil {
		t.Fatalf("step not started: remaining=%d resp=%v", c.stepRemaining, c.stepResp)
	}
	select {
	case r := <-held.resp:
		t.Fatalf("held command ran early: %q", r)
	default:
	}

	// The emulation loop consumes one step per iteration while paused.
	for i := 0; i < 2; i++ {
		if !c.TakeStep() {
			t.Fatalf("iteration %d: nothing left to step", i)
		}
		c.serviceCommands()
	}

	if r := <-step.resp; r != "stepped" {
		t.Fatalf("step response %q", r)
	}
	if r := <-held.resp; r != "resumed" {
		t.Fatalf("held command response %q", r)
	}
	if c.paused.Load() {
		t.Fatal("held resume did not clear pause")
	}
}

// TestConsoleStepAbortOnUnpause covers a keyboard unpause arriving while
// a step is in flight. The step completes its response instead of
// holding the queue forever.
func TestConsoleStepAbortOnUnpause(t *testing.T) {
	c := newTestConsole()
	c.cmds = make(chan consoleCmd, 16)
	c.paused.Store(true)

	step := consoleCmd{line: "frame 10", resp: make(chan string, 1)}
	c.cmds <- step
	c.serviceCommands()

	c.paused.Store(false) // keyboard toggle mid-step
	c.serviceCommands()

	if r := <-step.resp; r != "stepped" {
		t.Fatalf("step response %q", r)
	}
	if c.stepRemaining != 0 {
		t.Fatalf("stepRemaining = %d, want 0", c.stepRemaining)
	}
}

func TestConsoleHelpListsCommands(t *testing.T) {
	c := newTestConsole()
	r := runLine(t, c, "help")
	for _, name := range []string{"pause", "resume", "frame", "prompt", "help"} {
		if !strings.Contains(r, name) {
			t.Fatalf("help output missing %q:\n%s", name, r)
		}
	}
}

func TestConsolePromptToggle(t *testing.T) {
	c := newTestConsole()
	c.prompt = true

	if r := runLine(t, c, "prompt off"); r != "" {
		t.Fatalf("prompt off should be silent, got %q", r)
	}
	if c.prompt {
		t.Fatal("prompt off did not clear the setting")
	}
	if r := runLine(t, c, "prompt on"); r != "" {
		t.Fatalf("prompt on should be silent, got %q", r)
	}
	if !c.prompt {
		t.Fatal("prompt on did not set the setting")
	}
	for _, bad := range []string{"prompt", "prompt x", "prompt on off"} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
}
