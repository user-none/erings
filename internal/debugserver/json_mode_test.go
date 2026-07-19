// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/user-none/erings/internal/debugserver/responses"
)

// decodeLine unmarshals one JSON line into v and fails the test on any
// decode error.
func decodeLine(t *testing.T, line string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(line), v); err != nil {
		t.Fatalf("line is not valid JSON: %v\n%s", err, line)
	}
}

// envelope mirrors the response envelope for decoding with the data
// field kept raw so each test decodes its command-specific payload.
type envelope struct {
	Type string          `json:"type"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// runJSON dispatches one command line and decodes the JSON envelope.
func runJSON(t *testing.T, c *Server, line string) envelope {
	t.Helper()
	var e envelope
	decodeLine(t, runLine(t, c, line), &e)
	return e
}

func TestModeValidation(t *testing.T) {
	c := newTestServer()
	for _, bad := range []string{"mode", "mode x", "mode json text"} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
	if c.jsonMode {
		t.Fatal("a rejected mode command changed the mode")
	}
}

// TestModeSwitchResponses covers the mode transition contract: the
// response to a mode command is emitted in the newly selected mode in
// both directions.
func TestModeSwitchResponses(t *testing.T) {
	c := newTestServer()
	e := runJSON(t, c, "mode json")
	if e.Type != "resp" || string(e.Data) != `"mode json"` {
		t.Fatalf("mode json response: %+v", e)
	}
	if !c.jsonMode {
		t.Fatal("mode json did not set the mode")
	}
	if r := runLine(t, c, "mode text"); r != "mode text" {
		t.Fatalf("mode text response %q", r)
	}
	if c.jsonMode {
		t.Fatal("mode text did not clear the mode")
	}
}

func TestJSONErrorEnvelope(t *testing.T) {
	c := newTestServer()
	runLine(t, c, "mode json")
	e := runJSON(t, c, "bogus")
	if e.Type != "error" || !strings.Contains(e.Msg, "unknown command") {
		t.Fatalf("unknown command envelope: %+v", e)
	}
	e = runJSON(t, c, "frame")
	if e.Type != "error" || e.Msg != "not paused" {
		t.Fatalf("handler error envelope: %+v", e)
	}
}

// TestJSONEveryCommandResponds verifies that commands that are silent
// in text mode still produce a response line in JSON mode, which the
// client's in-order response matching depends on.
func TestJSONEveryCommandResponds(t *testing.T) {
	c := newTestServer()
	if r := runLine(t, c, "prompt off"); r != "" {
		t.Fatalf("text-mode prompt off should be silent, got %q", r)
	}
	runLine(t, c, "mode json")
	e := runJSON(t, c, "prompt off")
	if e.Type != "resp" || string(e.Data) != `""` {
		t.Fatalf("json-mode prompt off envelope: %+v", e)
	}
}

func TestStateCommand(t *testing.T) {
	c, m := newFakeServer()
	c.frame = 77

	if r := runLine(t, c, "state"); r != "paused=false frame=77 width=8 search=none" {
		t.Fatalf("state text %q", r)
	}
	if r := runLine(t, c, "state extra"); !strings.HasPrefix(r, "error:") {
		t.Fatalf("state with args: %q", r)
	}

	runLine(t, c, "pause")
	runLine(t, c, "width 16")
	runLine(t, c, "baseline wramh")
	m.wram[fakeAddr] = 3
	runLine(t, c, "filter diff")

	if r := runLine(t, c, "state"); r != "paused=true frame=77 width=16 search=1" {
		t.Fatalf("state text with search %q", r)
	}

	runLine(t, c, "mode json")
	e := runJSON(t, c, "state")
	if e.Type != "resp" {
		t.Fatalf("state envelope type %q", e.Type)
	}
	var s responses.StateResult
	decodeLine(t, string(e.Data), &s)
	want := responses.StateResult{Paused: true, Frame: 77, Width: 16, SearchActive: true, Candidates: 1}
	if s != want {
		t.Fatalf("state data %+v, want %+v", s, want)
	}
}

func TestJSONRead(t *testing.T) {
	c, m := newFakeServer()
	copy(m.wram[fakeAddr:], []byte{0xDE, 0xAD, 0xBE, 0xEF})
	runLine(t, c, "mode json")

	e := runJSON(t, c, "read 0x06001000 4")
	var r responses.ReadResult
	decodeLine(t, string(e.Data), &r)
	if r.Addr != 0x06001000 || r.Data != "deadbeef" {
		t.Fatalf("read data %+v", r)
	}
}

func TestJSONWatchList(t *testing.T) {
	c, m := newFakeServer()
	m.wram[fakeAddr] = 9
	runLine(t, c, "mode json")

	e := runJSON(t, c, "watch")
	if string(e.Data) != `{"watches":[]}` {
		t.Fatalf("empty watch list data %s", e.Data)
	}

	runLine(t, c, "watch 0x06001000 16")
	c.Service(5)
	e = runJSON(t, c, "watch")
	var wl responses.WatchList
	decodeLine(t, string(e.Data), &wl)
	if len(wl.Watches) != 1 {
		t.Fatalf("watch list %+v", wl)
	}
	want := responses.WatchInfo{Addr: 0x06001000, Width: 16, Value: 9 << 8, Valid: true}
	if wl.Watches[0] != want {
		t.Fatalf("watch entry %+v, want %+v", wl.Watches[0], want)
	}
}

func TestJSONBreakList(t *testing.T) {
	c, _ := newFakeServer()
	runLine(t, c, "mode json")

	e := runJSON(t, c, "break")
	if string(e.Data) != `{"breaks":[]}` {
		t.Fatalf("empty break list data %s", e.Data)
	}

	runLine(t, c, "break 0x06001000 dec")
	runLine(t, c, "break 0x06001004 eq 42 32")
	e = runJSON(t, c, "break")
	var bl responses.BreakList
	decodeLine(t, string(e.Data), &bl)
	want := []responses.BreakInfo{
		{Addr: 0x06001000, Width: 8, Op: "dec"},
		{Addr: 0x06001004, Width: 32, Op: "eq", Val: 42, HasVal: true},
	}
	if len(bl.Breaks) != 2 || bl.Breaks[0] != want[0] || bl.Breaks[1] != want[1] {
		t.Fatalf("break list %+v, want %+v", bl.Breaks, want)
	}
}

func TestJSONRegionsAndSnapshots(t *testing.T) {
	c, _ := newFakeServer()
	runLine(t, c, "mode json")

	e := runJSON(t, c, "regions")
	var rl responses.RegionList
	decodeLine(t, string(e.Data), &rl)
	if len(rl.Regions) != len(regionTable) || rl.Regions[0].Name != "wraml" {
		t.Fatalf("regions data %+v", rl)
	}

	e = runJSON(t, c, "snapshots")
	if string(e.Data) != `{"snapshots":[]}` {
		t.Fatalf("empty snapshots data %s", e.Data)
	}
	runLine(t, c, "snapshot spot")
	e = runJSON(t, c, "snapshots")
	var sl snapshotList
	decodeLine(t, string(e.Data), &sl)
	if len(sl.Snapshots) != 1 || sl.Snapshots[0].Name != "spot" || sl.Snapshots[0].Size != 0x200000 {
		t.Fatalf("snapshots data %+v", sl)
	}
}

func TestJSONList(t *testing.T) {
	c, m := newFakeServer()
	runLine(t, c, "mode json")
	runLine(t, c, "baseline wramh")
	m.wram[fakeAddr] = 9
	runLine(t, c, "filter diff")

	e := runJSON(t, c, "list")
	var lr responses.CandidateList
	decodeLine(t, string(e.Data), &lr)
	if lr.Width != 8 || lr.Total != 1 || len(lr.Candidates) != 1 {
		t.Fatalf("list data %+v", lr)
	}
	want := responses.CandidateInfo{Addr: 0x06001000, Cur: 9, Base: 9}
	if lr.Candidates[0] != want {
		t.Fatalf("candidate %+v, want %+v", lr.Candidates[0], want)
	}
}

// TestListOffsetPaging walks pages of a full-region candidate set. A
// fresh baseline keeps every offset as a candidate, so page contents
// are predictable addresses.
func TestListOffsetPaging(t *testing.T) {
	c, _ := newFakeServer()
	runLine(t, c, "mode json")
	runLine(t, c, "baseline wramh")

	e := runJSON(t, c, "list 2 3")
	var cl responses.CandidateList
	decodeLine(t, string(e.Data), &cl)
	if cl.Offset != 3 || cl.Total != 0x100000 || len(cl.Candidates) != 2 {
		t.Fatalf("page shape %+v", cl)
	}
	if cl.Candidates[0].Addr != 0x06000003 || cl.Candidates[1].Addr != 0x06000004 {
		t.Fatalf("page addresses %+v", cl.Candidates)
	}

	// An offset past the set returns an empty page, not an error.
	e = runJSON(t, c, "list 2 2000000")
	decodeLine(t, string(e.Data), &cl)
	if len(cl.Candidates) != 0 || cl.Total != 0x100000 {
		t.Fatalf("past-end page %+v", cl)
	}

	if r := runLine(t, c, "list 2 -1"); !strings.Contains(r, "offset") {
		t.Fatalf("negative offset response %q", r)
	}
}

func TestJSONWatchAndBreakEvents(t *testing.T) {
	c, m := newFakeServer()
	c.out = make(chan string, 4)
	m.wram[fakeAddr] = 6
	runLine(t, c, "mode json")
	runLine(t, c, "watch 0x06001000")
	runLine(t, c, "break 0x06001004 eq 3")
	c.Service(100) // seeds silently

	m.wram[fakeAddr] = 5
	m.wram[fakeAddr+4] = 3
	c.Service(101)

	var we watchEvent
	decodeLine(t, <-c.out, &we)
	wantWatch := watchEvent{Type: "watch", Frame: 101, Addr: 0x06001000, Width: 8, Prev: 6, Cur: 5}
	if we != wantWatch {
		t.Fatalf("watch event %+v, want %+v", we, wantWatch)
	}

	var be breakEvent
	decodeLine(t, <-c.out, &be)
	wantBreak := breakEvent{Type: "break", Frame: 101, Addr: 0x06001004, Width: 8, Prev: 0, Cur: 3, Cond: "eq 3"}
	if be != wantBreak {
		t.Fatalf("break event %+v, want %+v", be, wantBreak)
	}
	if !c.paused.Load() {
		t.Fatal("break event did not pause")
	}
}

// TestAttachResetsMode covers the per-connection mode contract: a new
// client attach (and a disconnect) drops back to text mode so the next
// client never inherits JSON output it did not ask for.
func TestAttachResetsMode(t *testing.T) {
	c := newTestServer()
	runLine(t, c, "mode json")

	attach := clientCmd{attach: make(chan string, 1), resp: make(chan string, 1)}
	c.runCommand(attach)
	if c.jsonMode {
		t.Fatal("attach did not reset the mode")
	}

	runLine(t, c, "mode json")
	bye := clientCmd{bye: true, resp: make(chan string, 1)}
	c.runCommand(bye)
	if c.jsonMode {
		t.Fatal("bye did not reset the mode")
	}
}

// TestJSONSteppedResponse drives a frame step in JSON mode and checks
// the deferred response arrives as an envelope.
func TestJSONSteppedResponse(t *testing.T) {
	c := newTestServer()
	c.cmds = make(chan clientCmd, 16)
	c.paused.Store(true)
	runLine(t, c, "mode json")

	step := clientCmd{line: "frame 1", resp: make(chan string, 1)}
	c.cmds <- step
	c.serviceCommands()
	if !c.TakeStep() {
		t.Fatal("no step pending")
	}
	c.serviceCommands()

	var e envelope
	decodeLine(t, <-step.resp, &e)
	if e.Type != "resp" || string(e.Data) != `"stepped"` {
		t.Fatalf("stepped envelope %+v", e)
	}
}
