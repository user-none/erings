// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package debugserver implements the emulator debug server. It speaks a
// bare line protocol over a localhost TCP port and provides execution
// control, memory inspection, held memory locations, a cheat-search
// style memory search, and in-memory machine snapshots. The package has
// no UI dependencies. A
// host wires it to the emulator through the Machine interface and
// calls Service between frames.
package debugserver

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/user-none/erings/core"
	"github.com/user-none/erings/internal/debugserver/responses"
)

// Machine is the emulator surface the server needs. Memory access is
// canonical native bus addressed and gated by the machine's region
// table, the same table the memory access methods serve from.
type Machine interface {
	// ReadMemory reads guest memory at a canonical native bus address
	// into buf and returns the number of bytes read.
	ReadMemory(addr uint32, buf []byte) uint32
	// WriteMemory writes data to guest memory at a canonical native
	// bus address and returns the number of bytes written.
	WriteMemory(addr uint32, data []byte) uint32
	// Regions is the machine's accessible bus region table.
	Regions() []core.BusRegion
	Serialize() ([]byte, error)
	Deserialize(data []byte) error
}

// clientCmd is one line received from a client. The connection
// goroutine blocks on resp until the emulation goroutine has executed the
// command. resp is buffered so the responder never blocks, even if the
// client has gone away.
//
// attach and bye are internal items the connection sends at connect and
// disconnect. attach hands the emulation goroutine the connection's
// output channel so watch lines can be pushed to the client. bye clears
// the channel when the connection closes.
type clientCmd struct {
	line   string
	resp   chan string
	attach chan string
	bye    bool
}

// Server owns the listener and all debug state (watches, breaks, pins,
// searches, snapshots). Connection goroutines read lines and queue
// clientCmds. The emulation
// goroutine drains the queue between frames (Service), so command
// handlers run on the emulation goroutine and everything below the
// queue is owned by it.
type Server struct {
	machine  Machine
	paused   *atomic.Bool
	listener net.Listener
	cmds     chan clientCmd

	// regions caches the machine's bus region table. Static per
	// machine.
	regions []core.BusRegion

	// prompt controls the interactive "> " written on connect and after
	// each response (default on).
	prompt bool

	// jsonMode switches responses and pushed events to one-line JSON
	// envelopes (see format.go). It resets to text on client attach.
	jsonMode bool

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

	// watches are the watched addresses. Entries survive client
	// disconnects.
	watches []watchEntry

	// breaks are the value breaks. Entries survive client disconnects.
	breaks []breakEntry

	// pins are the held memory locations, restored every frame. Entries
	// survive client disconnects.
	pins []pinEntry

	// search and searchWidth are the memory-search state. searchWidth
	// zero means the default (8-bit, see currentWidth).
	search      *search
	searchWidth int

	// snapshots holds in-memory machine states by slot name.
	// Session-scoped, never written to disk.
	snapshots map[string][]byte
}

// Start listens on 127.0.0.1:port and serves clients. The
// paused flag is shared with the host's pause control.
func Start(port int, m Machine, paused *atomic.Bool) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	c := &Server{
		paused:   paused,
		listener: ln,
		cmds:     make(chan clientCmd, 16),
		prompt:   true,
	}
	c.setMachine(m)
	go c.acceptLoop()
	return c, nil
}

// setMachine wires the machine and caches its region table.
func (c *Server) setMachine(m Machine) {
	c.machine = m
	c.regions = m.Regions()
}

// acceptLoop serves one client at a time. A second connection
// attempt waits in the listen backlog until the current client
// disconnects.
func (c *Server) acceptLoop() {
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
func (c *Server) serveConn(conn net.Conn) {
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
	attach := clientCmd{attach: out, resp: make(chan string, 1)}
	c.cmds <- attach
	<-attach.resp
	defer func() {
		bye := clientCmd{bye: true, resp: make(chan string, 1)}
		c.cmds <- bye
		<-bye.resp
		close(out)
		<-writerDone
		conn.Close()
	}()

	// The prompt reads server state owned by the emulation goroutine.
	// Every read happens after a command response has been received, so
	// the response channel orders it after the command that could have
	// changed the setting. JSON mode never prompts: the client is a
	// program, and a bare "> " is not a JSON line.
	prompt := func() {
		if c.prompt && !c.jsonMode {
			out <- "> "
		}
	}

	prompt()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			cmd := clientCmd{line: line, resp: make(chan string, 1)}
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

// command describes one command for dispatch and help.
type command struct {
	name    string
	usage   string
	summary string
	fn      func(c *Server, args []string) (any, error)
}

// commands is populated in init. The help handler iterates the table it
// appears in, so a literal initializer would be an initialization cycle.
var commands []command

func init() {
	commands = []command{
		{"pause", "pause", "pause emulation", cmdPause},
		{"resume", "resume", "resume emulation", cmdResume},
		{"frame", "frame [n]", "while paused, run n frames (default 1) then re-pause", cmdFrame},
		{"state", "state", "report pause, frame, width, and search candidates", cmdState},
		{"regions", "regions", "list known memory regions", cmdRegions},
		{"read", "read <addr> [len]", "hex dump memory (len 1-4096, default 64)", cmdRead},
		{"write", "write <addr> <hex>", "write hex bytes to memory", cmdWrite},
		{"pin", "pin [<addr> <hex>]", "hold hex bytes in memory every frame; no args lists", cmdPin},
		{"unpin", "unpin <addr>|all", "release a pin", cmdUnpin},
		{"watch", "watch [<addr> [w]]", "report value changes each frame (w=8/16/32); no args lists", cmdWatch},
		{"unwatch", "unwatch <addr>|all", "stop watching", cmdUnwatch},
		{"break", "break [<addr> <op> [v] [w]]", "pause when the condition becomes true; no args lists", cmdBreak},
		{"unbreak", "unbreak <addr>|all", "remove a break", cmdUnbreak},
		{"baseline", "baseline [region...]", "start a search over regions (default all)", cmdBaseline},
		{"filter", "filter <op> [value]", "narrow candidates: dec inc same diff | eq ne lt gt <value>", cmdFilter},
		{"rebase", "rebase", "re-read the baseline without filtering (keeps candidates)", cmdRebase},
		{"width", "width [8|16|32]", "search value width; setting resets the search", cmdWidth},
		{"list", "list [n [offset]]", "show surviving candidates (default 20, from offset)", cmdList},
		{"reset", "reset", "discard the search", cmdReset},
		{"snapshot", "snapshot [name]", "capture the machine to an in-memory slot", cmdSnapshot},
		{"snapshots", "snapshots", "list snapshot slots", cmdSnapshots},
		{"restore", "restore [name]", "load a snapshot (search/watches survive)", cmdRestore},
		{"prompt", "prompt on|off", "toggle the interactive prompt (default on)", cmdPrompt},
		{"mode", "mode text|json", "set the response format (default text)", cmdMode},
		{"help", "help", "list commands", cmdHelp},
	}
}

// Service runs the server's between-frames work: pin restores, watch
// reads, and queued commands. The host calls it once per emulation loop
// iteration with the current RunFrame count. Pins are restored first so
// everything downstream observes the held values.
func (c *Server) Service(frame uint64) {
	c.frame = frame
	c.servicePins()
	c.serviceWatches()
	c.serviceBreaks()
	c.serviceCommands()
}

// TakeStep reports whether a client frame step is pending and consumes
// one frame from it. The host calls it while paused to decide whether
// to run a frame anyway.
func (c *Server) TakeStep() bool {
	if c.stepRemaining > 0 {
		c.stepRemaining--
		return true
	}
	return false
}

// serviceCommands drains queued client commands. While a frame step is
// in flight, queued commands are held so they observe the post-step
// state.
func (c *Server) serviceCommands() {
	if c.stepResp != nil {
		// A pause toggle from the keyboard aborts the step. Complete
		// the response either way.
		if c.stepRemaining > 0 && c.paused.Load() {
			return
		}
		c.stepRemaining = 0
		c.stepResp <- c.formatResp("stepped")
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

func (c *Server) runCommand(cmd clientCmd) {
	// A connection attach/bye takes or drops the client's output channel.
	// Either transition resets the output format: mode is per-connection
	// state, and a fresh client (interactive nc or the GUI) must always
	// start in text mode regardless of what the previous client set.
	if cmd.attach != nil || cmd.bye {
		if cmd.bye {
			c.out = nil
		} else {
			c.out = cmd.attach
		}
		c.jsonMode = false
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
			cmd.resp <- c.formatErr(err)
		default:
			cmd.resp <- c.formatResp(out)
		}
		return
	}
	cmd.resp <- c.formatErr(fmt.Errorf("unknown command %q (try help)", name))
}

func cmdPause(c *Server, args []string) (any, error) {
	c.paused.Store(true)
	return "paused", nil
}

func cmdResume(c *Server, args []string) (any, error) {
	c.paused.Store(false)
	return "resumed", nil
}

func cmdFrame(c *Server, args []string) (any, error) {
	n := 1
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 {
			return nil, fmt.Errorf("frame count must be a positive integer")
		}
		n = v
	}
	if !c.paused.Load() {
		return nil, fmt.Errorf("not paused")
	}
	c.stepRemaining = n
	return nil, errDeferredResponse
}

// stateText renders the state response for text mode.
func stateText(s responses.StateResult) string {
	search := "none"
	if s.SearchActive {
		search = strconv.Itoa(s.Candidates)
	}
	return fmt.Sprintf("paused=%t frame=%d width=%d search=%s",
		s.Paused, s.Frame, s.Width, search)
}

// cmdState reports the execution and search state in one response. A
// reconnecting client uses it to rebuild its view without inferring
// anything from earlier traffic.
func cmdState(c *Server, args []string) (any, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("usage: state")
	}
	s := responses.StateResult{Paused: c.paused.Load(), Frame: c.frame, Width: c.currentWidth()}
	if c.search != nil {
		s.SearchActive = true
		s.Candidates = c.search.total()
	}
	return s, nil
}

// cmdPrompt is silent on success in text mode: an empty response
// writes nothing, so a scripted "prompt off" leaves the output stream
// clean from its first command onward. In JSON mode the empty message
// still produces a response envelope, because a client matches
// responses to commands in order and every command must answer.
func cmdPrompt(c *Server, args []string) (any, error) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return nil, fmt.Errorf("usage: prompt on|off")
	}
	c.prompt = args[0] == "on"
	return "", nil
}

func cmdHelp(c *Server, args []string) (any, error) {
	var b strings.Builder
	for _, cm := range commands {
		fmt.Fprintf(&b, "%-27s %s\n", cm.usage, cm.summary)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
