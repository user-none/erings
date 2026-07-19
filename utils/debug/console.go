// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// consoleCmd is one line received from a console client. The connection
// goroutine blocks on resp until the emulation goroutine has executed the
// command. resp is buffered so the responder never blocks, even if the
// client has gone away.
//
// attach and bye are internal items the connection sends at connect and
// disconnect. attach hands the emulation goroutine the connection's
// output channel so watch lines can be pushed to the client. bye clears
// the channel when the connection closes.
type consoleCmd struct {
	line   string
	resp   chan string
	attach chan string
	bye    bool
}

// console owns the debug console listener. Connection goroutines read
// lines and queue consoleCmds. The emulation goroutine drains the queue
// between frames (serviceConsole), so command handlers run on the
// emulation goroutine and may freely touch emulation-owned state.
type console struct {
	listener net.Listener
	cmds     chan consoleCmd

	// prompt controls the interactive "> " written on connect and after
	// each response (default on).
	prompt bool
}

func startConsole(port int) (*console, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	c := &console{
		listener: ln,
		cmds:     make(chan consoleCmd, 16),
		prompt:   true,
	}
	go c.acceptLoop()
	return c, nil
}

// acceptLoop serves one client at a time. A second connection
// attempt waits in the listen backlog until the current client
// disconnects.
func (c *console) acceptLoop() {
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			return
		}
		c.serveConn(conn)
	}
}

// serveConn handles one client. The protocol is bare lines. It reads a
// command line and writes a newline-terminated response. An empty
// response writes nothing (silent commands). A "> " prompt is written
// on connect and after each response while the prompt setting is on.
//
// All connection output flows through a single writer goroutine fed by
// the out channel, so command responses and pushed watch lines never
// interleave mid-write. This loop sends responses and prompts. The
// emulation goroutine is the other sender and pushes watch lines into
// the same channel once attached.
func (c *console) serveConn(conn net.Conn) {
	out := make(chan string, 64)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for s := range out {
			if _, err := io.WriteString(conn, s); err != nil {
				for range out {
				}
				return
			}
		}
	}()

	// Hand the emulation goroutine this connection's output channel.
	attach := consoleCmd{attach: out, resp: make(chan string, 1)}
	c.cmds <- attach
	<-attach.resp
	defer func() {
		bye := consoleCmd{bye: true, resp: make(chan string, 1)}
		c.cmds <- bye
		<-bye.resp
		close(out)
		<-writerDone
		conn.Close()
	}()

	prompt := func() {
		if c.prompt {
			out <- "> "
		}
	}

	prompt()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			cmd := consoleCmd{line: line, resp: make(chan string, 1)}
			c.cmds <- cmd
			if r := <-cmd.resp; r != "" {
				out <- r + "\n"
			}
		}
		prompt()
	}
}

// errDeferredResponse is returned by a handler that has arranged for the
// response to be sent later (frame stepping). The dispatcher stashes the
// command's response channel instead of replying immediately.
var errDeferredResponse = errors.New("deferred response")

// consoleCommand describes one console command for dispatch and help.
type consoleCommand struct {
	name    string
	usage   string
	summary string
	fn      func(g *game, args []string) (string, error)
}

// consoleCommands is populated in init. The help handler iterates the
// table it appears in, so a literal initializer would be an
// initialization cycle.
var consoleCommands []consoleCommand

func init() {
	consoleCommands = []consoleCommand{
		{"pause", "pause", "pause emulation", cmdPause},
		{"resume", "resume", "resume emulation", cmdResume},
		{"frame", "frame [n]", "while paused, run n frames (default 1) then re-pause", cmdFrame},
		{"regions", "regions", "list known memory regions", cmdRegions},
		{"read", "read <addr> [len]", "hex dump memory (len 1-4096, default 64)", cmdRead},
		{"watch", "watch [<addr> [w]]", "report value changes each frame (w=8/16/32); no args lists", cmdWatch},
		{"unwatch", "unwatch <addr>|all", "stop watching", cmdUnwatch},
		{"baseline", "baseline [region...]", "start a search over regions (default all)", cmdBaseline},
		{"filter", "filter <op> [value]", "narrow candidates: dec inc same diff | eq ne lt gt <value>", cmdFilter},
		{"width", "width [8|16|32]", "search value width; setting resets the search", cmdWidth},
		{"list", "list [n]", "show surviving candidates (default 20)", cmdList},
		{"reset", "reset", "discard the search", cmdReset},
		{"snapshot", "snapshot [name]", "capture the machine to an in-memory slot", cmdSnapshot},
		{"snapshots", "snapshots", "list snapshot slots", cmdSnapshots},
		{"restore", "restore [name]", "load a snapshot (search/watches survive)", cmdRestore},
		{"prompt", "prompt on|off", "toggle the interactive prompt (default on)", cmdPrompt},
		{"help", "help", "list commands", cmdHelp},
	}
}

// serviceConsole drains queued console commands. While a frame step is in
// flight, queued commands are held so they observe the post-step state.
func (g *game) serviceConsole() {
	if g.console == nil {
		return
	}
	if g.stepResp != nil {
		// A pause toggle from the keyboard aborts the step. Complete
		// the response either way.
		if g.stepRemaining > 0 && g.paused.Load() {
			return
		}
		g.stepRemaining = 0
		g.stepResp <- "stepped"
		g.stepResp = nil
	}
	for {
		select {
		case cmd := <-g.console.cmds:
			g.runConsoleCommand(cmd)
			if g.stepResp != nil {
				return
			}
		default:
			return
		}
	}
}

func (g *game) runConsoleCommand(cmd consoleCmd) {
	// A connection attach/bye takes or drops the client's output channel.
	if cmd.attach != nil || cmd.bye {
		if cmd.bye {
			g.consoleOut = nil
		} else {
			g.consoleOut = cmd.attach
		}
		cmd.resp <- ""
		return
	}

	fields := strings.Fields(cmd.line)
	name := fields[0]
	args := fields[1:]
	for i := range consoleCommands {
		if consoleCommands[i].name != name {
			continue
		}
		out, err := consoleCommands[i].fn(g, args)
		switch {
		case errors.Is(err, errDeferredResponse):
			g.stepResp = cmd.resp
		case err != nil:
			cmd.resp <- "error: " + err.Error()
		default:
			cmd.resp <- out
		}
		return
	}
	cmd.resp <- fmt.Sprintf("error: unknown command %q (try help)", name)
}

func cmdPause(g *game, args []string) (string, error) {
	g.paused.Store(true)
	return "paused", nil
}

func cmdResume(g *game, args []string) (string, error) {
	g.paused.Store(false)
	return "resumed", nil
}

func cmdFrame(g *game, args []string) (string, error) {
	n := 1
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 {
			return "", fmt.Errorf("frame count must be a positive integer")
		}
		n = v
	}
	if !g.paused.Load() {
		return "", fmt.Errorf("not paused")
	}
	g.stepRemaining = n
	return "", errDeferredResponse
}

// cmdPrompt is silent on success. An empty response writes nothing, so
// a scripted "prompt off" leaves the output stream clean from its first
// command onward.
func cmdPrompt(g *game, args []string) (string, error) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return "", fmt.Errorf("usage: prompt on|off")
	}
	g.console.prompt = args[0] == "on"
	return "", nil
}

func cmdHelp(g *game, args []string) (string, error) {
	var b strings.Builder
	for _, c := range consoleCommands {
		fmt.Fprintf(&b, "%-21s %s\n", c.usage, c.summary)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
