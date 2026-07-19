// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package client implements the debugger's connection to a debug
// server. It dials the server's TCP port, switches the connection to
// JSON mode, and then exposes two streams: in-order command responses
// and pushed watch/break events. The package has no UI dependencies.
package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	// dialTimeout bounds the TCP connect.
	dialTimeout = 3 * time.Second
	// handshakeTimeout bounds the wait for the mode-switch response, so
	// dialing a port that is not a debug server fails instead of
	// hanging.
	handshakeTimeout = 3 * time.Second
	// eventBuffer is the pushed-event channel capacity. Events beyond a
	// full buffer are dropped, matching the server's own non-blocking
	// push to a stalled client.
	eventBuffer = 256
)

// Response is the outcome of one command. Err is set for both
// server-reported errors and connection failures; Data is the resp
// envelope payload otherwise.
type Response struct {
	Data json.RawMessage
	Err  error
}

// Event is one pushed watch or break notification. Kind is "watch" or
// "break". Cond is the break condition and is empty for watches. A
// break event means emulation is now paused.
type Event struct {
	Kind  string `json:"type"`
	Frame uint64 `json:"frame"`
	Addr  uint32 `json:"addr"`
	Width int    `json:"width"`
	Prev  uint32 `json:"prev"`
	Cur   uint32 `json:"cur"`
	Cond  string `json:"cond"`
}

// envelope is the server's JSON line shape. Type selects which of the
// remaining fields mean anything: resp carries Data, error carries Msg,
// watch/break lines are decoded again as Event.
type envelope struct {
	Type string          `json:"type"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// Client is one server connection. Commands are matched to responses
// by order: the server executes one command at a time and emits
// exactly one resp or error line per command, so the oldest pending
// command owns the next response line.
type Client struct {
	conn   net.Conn
	events chan Event

	// mu guards pending, closed, and err, and serializes writes so a
	// command's pending slot is always registered before its line hits
	// the wire.
	mu      sync.Mutex
	pending []chan Response
	closed  bool
	err     error

	closeOnce sync.Once
	done      chan struct{}
}

// Dial connects to a debug server, switches it to JSON mode, and
// starts the reader. The mode switch doubles as a handshake: if the
// expected mode response does not arrive within the timeout, the
// endpoint is not a debug server and Dial fails.
func Dial(addr string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:   conn,
		events: make(chan Event, eventBuffer),
		done:   make(chan struct{}),
	}
	go c.readLoop()

	resp := c.Do("mode json")
	select {
	case r := <-resp:
		if r.Err != nil {
			c.Close()
			return nil, fmt.Errorf("server handshake: %w", r.Err)
		}
		var mode string
		if json.Unmarshal(r.Data, &mode) != nil || mode != "mode json" {
			c.Close()
			return nil, fmt.Errorf("server handshake: unexpected response %s", r.Data)
		}
	case <-time.After(handshakeTimeout):
		c.Close()
		return nil, errors.New("server handshake: timed out")
	}
	return c, nil
}

// Do queues one command line and returns a channel that delivers its
// response exactly once. On a dead connection the response is an
// immediate error. A line containing a newline is rejected: it would
// reach the server as multiple commands and desynchronize the
// in-order response matching.
func (c *Client) Do(line string) <-chan Response {
	resp := make(chan Response, 1)
	if strings.ContainsAny(line, "\r\n") {
		resp <- Response{Err: errors.New("command contains a newline")}
		return resp
	}
	c.mu.Lock()
	if c.closed {
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = errors.New("connection closed")
		}
		resp <- Response{Err: err}
		return resp
	}
	c.pending = append(c.pending, resp)
	_, err := io.WriteString(c.conn, line+"\n")
	c.mu.Unlock()
	if err != nil {
		// The reader unwinds on the broken connection and errors out
		// every pending response, including this one.
		c.conn.Close()
	}
	return resp
}

// Events returns the pushed-event stream. The channel is closed when
// the connection ends, which is the disconnect signal for a consumer
// that polls it.
func (c *Client) Events() <-chan Event {
	return c.events
}

// Err reports why the connection ended. It is nil while the connection
// is alive and after a local Close.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close tears the connection down. It is safe to call more than once
// and concurrently with in-flight commands, which complete with errors.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.conn.Close()
		<-c.done
	})
}

// readLoop consumes server lines until the connection dies, then fails
// all pending commands and closes the event channel.
func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// The server writes an interactive "> " prompt before the mode
		// switch lands, so the first response line arrives with prompt
		// prefixes. JSON lines never start with '>'.
		for strings.HasPrefix(line, "> ") {
			line = line[2:]
		}
		var e envelope
		if json.Unmarshal([]byte(line), &e) != nil {
			// Text-mode push lines can arrive in the window between
			// attach and the mode switch (a watch surviving from an
			// earlier client, with emulation running). They carry
			// nothing the connect-time refresh does not re-read.
			continue
		}
		switch e.Type {
		case "resp":
			c.deliver(Response{Data: e.Data})
		case "error":
			c.deliver(Response{Err: errors.New(e.Msg)})
		case "watch", "break":
			var ev Event
			if json.Unmarshal([]byte(line), &ev) == nil {
				select {
				case c.events <- ev:
				default:
				}
			}
		}
	}

	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	c.closed = true
	// A locally initiated Close reads as a "use of closed network
	// connection" error. Report nil in that case: the connection ended
	// because we ended it.
	if !errors.Is(err, net.ErrClosed) {
		c.err = err
	}
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()

	failure := c.err
	if failure == nil {
		failure = errors.New("connection closed")
	}
	for _, resp := range pending {
		resp <- Response{Err: failure}
	}
	close(c.events)
	close(c.done)
}

// deliver hands a response line to the oldest pending command. A
// response with no pending command means the server broke the one-line-
// per-command contract; it is dropped rather than crossing wires.
func (c *Client) deliver(r Response) {
	c.mu.Lock()
	var resp chan Response
	if len(c.pending) > 0 {
		resp = c.pending[0]
		c.pending = c.pending[1:]
	}
	c.mu.Unlock()
	if resp != nil {
		resp <- r
	}
}
