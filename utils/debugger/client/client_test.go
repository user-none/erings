// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package client

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// startServer runs a one-connection fake console. It performs the
// text-mode connect behavior (prompt, then the mode-switch handshake)
// and hands the connection to script for the test body. The connection
// closes when script returns.
func startServer(t *testing.T, script func(conn net.Conn, r *bufio.Reader)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		io.WriteString(conn, "> ")
		line, err := r.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "mode json" {
			return
		}
		io.WriteString(conn, `{"type":"resp","data":"mode json"}`+"\n")
		if script != nil {
			script(conn, r)
		}
	}()
	return ln.Addr().String()
}

// waitResp receives one response with a timeout so a broken client
// fails the test instead of hanging it.
func waitResp(t *testing.T, ch <-chan Response) Response {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a response")
		return Response{}
	}
}

// waitEvent receives one event, reporting whether the channel is still
// open.
func waitEvent(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}, false
	}
}

func TestDialHandshake(t *testing.T) {
	// The server holds the connection open so the close below is
	// locally initiated.
	addr := startServer(t, func(conn net.Conn, r *bufio.Reader) {
		r.ReadString('\n')
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()
	if err := c.Err(); err != nil {
		t.Fatalf("local close should leave a nil error, got %v", err)
	}
}

func TestDialRefused(t *testing.T) {
	// A listener that is closed before dialing gives a port that
	// refuses connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if _, err := Dial(addr); err == nil {
		t.Fatal("dial to a refused port succeeded")
	}
}

// TestResponseOrdering queues two commands before the server answers
// either, with a pushed event between the answers. Responses must land
// on the commands in send order and the event on the event stream.
func TestResponseOrdering(t *testing.T) {
	addr := startServer(t, func(conn net.Conn, r *bufio.Reader) {
		for i := 0; i < 2; i++ {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
		}
		io.WriteString(conn, `{"type":"resp","data":"first"}`+"\n")
		io.WriteString(conn, `{"type":"watch","frame":9,"addr":100667392,"width":8,"prev":1,"cur":2}`+"\n")
		io.WriteString(conn, `{"type":"resp","data":{"n":2}}`+"\n")
		// Hold the connection open until the client is done reading.
		r.ReadString('\n')
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ra := c.Do("a")
	rb := c.Do("b")

	if r := waitResp(t, ra); r.Err != nil || string(r.Data) != `"first"` {
		t.Fatalf("first response %+v", r)
	}
	if r := waitResp(t, rb); r.Err != nil || string(r.Data) != `{"n":2}` {
		t.Fatalf("second response %+v", r)
	}
	ev, ok := waitEvent(t, c.Events())
	want := Event{Kind: "watch", Frame: 9, Addr: 100667392, Width: 8, Prev: 1, Cur: 2}
	if !ok || ev != want {
		t.Fatalf("event %+v ok=%t, want %+v", ev, ok, want)
	}
}

func TestErrorResponse(t *testing.T) {
	addr := startServer(t, func(conn net.Conn, r *bufio.Reader) {
		if _, err := r.ReadString('\n'); err != nil {
			return
		}
		io.WriteString(conn, `{"type":"error","msg":"not paused"}`+"\n")
		r.ReadString('\n')
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	r := waitResp(t, c.Do("frame"))
	if r.Err == nil || r.Err.Error() != "not paused" {
		t.Fatalf("error response %+v", r)
	}
}

// TestTextLineIgnored covers the attach window where the console can
// still push text-mode lines: they are dropped without consuming a
// pending response slot.
func TestTextLineIgnored(t *testing.T) {
	addr := startServer(t, func(conn net.Conn, r *bufio.Reader) {
		if _, err := r.ReadString('\n'); err != nil {
			return
		}
		io.WriteString(conn, "[WATCH] frame=1 0x06001000 w8: 1 -> 2 (0x01 -> 0x02)\n")
		io.WriteString(conn, `{"type":"resp","data":"ok"}`+"\n")
		r.ReadString('\n')
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if r := waitResp(t, c.Do("x")); r.Err != nil || string(r.Data) != `"ok"` {
		t.Fatalf("response after text line %+v", r)
	}
}

func TestBreakEvent(t *testing.T) {
	addr := startServer(t, func(conn net.Conn, r *bufio.Reader) {
		io.WriteString(conn, `{"type":"break","frame":40,"addr":100667396,"width":16,"prev":0,"cur":3,"cond":"eq 3"}`+"\n")
		r.ReadString('\n')
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ev, ok := waitEvent(t, c.Events())
	want := Event{Kind: "break", Frame: 40, Addr: 100667396, Width: 16, Prev: 0, Cur: 3, Cond: "eq 3"}
	if !ok || ev != want {
		t.Fatalf("event %+v ok=%t, want %+v", ev, ok, want)
	}
}

// TestServerDisconnect drops the connection with a command in flight.
// The pending command errors, the event stream closes, and Err reports
// the failure.
func TestServerDisconnect(t *testing.T) {
	addr := startServer(t, func(conn net.Conn, r *bufio.Reader) {
		r.ReadString('\n')
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if r := waitResp(t, c.Do("x")); r.Err == nil {
		t.Fatal("pending command survived a disconnect")
	}
	if _, ok := waitEvent(t, c.Events()); ok {
		t.Fatal("event stream still open after disconnect")
	}
	if c.Err() == nil {
		t.Fatal("Err is nil after a remote disconnect")
	}
}

func TestDoRejectsNewline(t *testing.T) {
	addr := startServer(t, func(conn net.Conn, r *bufio.Reader) {
		r.ReadString('\n')
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if r := waitResp(t, c.Do("a\nb")); r.Err == nil {
		t.Fatal("multi-line command was accepted")
	}
}

func TestDoAfterClose(t *testing.T) {
	addr := startServer(t, nil)
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()
	if r := waitResp(t, c.Do("x")); r.Err == nil {
		t.Fatal("Do on a closed client returned no error")
	}
}
