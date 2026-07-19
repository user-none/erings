// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package console implements the network debug console for the debug
// launcher. It speaks a bare line protocol over a localhost TCP port and
// provides execution control, memory inspection, a cheat-search style
// memory search, and in-memory machine snapshots. The package has no UI
// dependencies. The host wires it to the emulator through the Machine
// interface and calls Service between frames.
package console

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
)

// Machine is the emulator surface the console needs.
type Machine interface {
	// ReadMemory copies guest memory at a flat offset into buf and
	// returns the number of bytes copied.
	ReadMemory(addr uint32, buf []byte) uint32
	Serialize() ([]byte, error)
	Deserialize(data []byte) error
}

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

// Console owns the debug console listener and all console state.
// Connection goroutines read lines and queue consoleCmds. The emulation
// goroutine drains the queue between frames (Service), so command
// handlers run on the emulation goroutine and everything below the
// queue is owned by it.
type Console struct {
	machine  Machine
	paused   *atomic.Bool
	listener net.Listener
	cmds     chan consoleCmd

	// prompt controls the interactive "> " written on connect and after
	// each response (default on).
	prompt bool

	// frame is the RunFrame count passed to the latest Service call.
	// Watch lines reference it.
	frame uint64

	// stepRemaining counts frames still to run while paused. stepResp
	// holds the pending frame-command response channel until the step
	// completes.
	stepRemaining int
	stepResp      chan string

	// out is the attached client's output channel. It is nil when no
	// client is connected and is handed over and cleared through the
	// command queue (attach/bye).
	out chan string

	// watches are the watched addresses. Entries survive console
	// disconnects.
	watches []watchEntry

	// search and searchWidth are the memory-search state. searchWidth
	// zero means the default (8-bit, see currentWidth).
	search      *search
	searchWidth int

	// snapshots holds in-memory machine states by slot name.
	// Session-scoped, never written to disk.
	snapshots map[string][]byte
}

// Start listens on 127.0.0.1:port and serves console clients. The
// paused flag is shared with the host's pause control.
func Start(port int, m Machine, paused *atomic.Bool) (*Console, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	c := &Console{
		machine:  m,
		paused:   paused,
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
func (c *Console) acceptLoop() {
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
func (c *Console) serveConn(conn net.Conn) {
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

// command describes one console command for dispatch and help.
type command struct {
	name    string
	usage   string
	summary string
	fn      func(c *Console, args []string) (string, error)
}

// commands is populated in init. The help handler iterates the table it
// appears in, so a literal initializer would be an initialization cycle.
var commands []command

func init() {
	commands = []command{
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

// Service runs the console's between-frames work: watch reads and
// queued commands. The host calls it once per emulation loop iteration
// with the current RunFrame count.
func (c *Console) Service(frame uint64) {
	c.frame = frame
	c.serviceWatches()
	c.serviceCommands()
}

// TakeStep reports whether a console frame step is pending and consumes
// one frame from it. The host calls it while paused to decide whether
// to run a frame anyway.
func (c *Console) TakeStep() bool {
	if c.stepRemaining > 0 {
		c.stepRemaining--
		return true
	}
	return false
}

// serviceCommands drains queued console commands. While a frame step is
// in flight, queued commands are held so they observe the post-step
// state.
func (c *Console) serviceCommands() {
	if c.stepResp != nil {
		// A pause toggle from the keyboard aborts the step. Complete
		// the response either way.
		if c.stepRemaining > 0 && c.paused.Load() {
			return
		}
		c.stepRemaining = 0
		c.stepResp <- "stepped"
		c.stepResp = nil
	}
	for {
		select {
		case cmd := <-c.cmds:
			c.runCommand(cmd)
			if c.stepResp != nil {
				return
			}
		default:
			return
		}
	}
}

func (c *Console) runCommand(cmd consoleCmd) {
	// A connection attach/bye takes or drops the client's output channel.
	if cmd.attach != nil || cmd.bye {
		if cmd.bye {
			c.out = nil
		} else {
			c.out = cmd.attach
		}
		cmd.resp <- ""
		return
	}

	fields := strings.Fields(cmd.line)
	name := fields[0]
	args := fields[1:]
	for i := range commands {
		if commands[i].name != name {
			continue
		}
		out, err := commands[i].fn(c, args)
		switch {
		case errors.Is(err, errDeferredResponse):
			c.stepResp = cmd.resp
		case err != nil:
			cmd.resp <- "error: " + err.Error()
		default:
			cmd.resp <- out
		}
		return
	}
	cmd.resp <- fmt.Sprintf("error: unknown command %q (try help)", name)
}

func cmdPause(c *Console, args []string) (string, error) {
	c.paused.Store(true)
	return "paused", nil
}

func cmdResume(c *Console, args []string) (string, error) {
	c.paused.Store(false)
	return "resumed", nil
}

func cmdFrame(c *Console, args []string) (string, error) {
	n := 1
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 {
			return "", fmt.Errorf("frame count must be a positive integer")
		}
		n = v
	}
	if !c.paused.Load() {
		return "", fmt.Errorf("not paused")
	}
	c.stepRemaining = n
	return "", errDeferredResponse
}

// cmdPrompt is silent on success. An empty response writes nothing, so
// a scripted "prompt off" leaves the output stream clean from its first
// command onward.
func cmdPrompt(c *Console, args []string) (string, error) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return "", fmt.Errorf("usage: prompt on|off")
	}
	c.prompt = args[0] == "on"
	return "", nil
}

func cmdHelp(c *Console, args []string) (string, error) {
	var b strings.Builder
	for _, cm := range commands {
		fmt.Fprintf(&b, "%-21s %s\n", cm.usage, cm.summary)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
