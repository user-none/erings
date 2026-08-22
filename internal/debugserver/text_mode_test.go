// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/user-none/erings/core"
)

// fakeMachine implements Machine over a 2MB buffer with the same
// region semantics as the core: WRAM-L backs buffer offsets
// 0x000000-0x0FFFFF, WRAM-H backs 0x100000-0x1FFFFF, mirrors and
// partition views fold, and access clamps at the region end.
type fakeMachine struct {
	wram [0x200000]byte
}

func (m *fakeMachine) Regions() []core.BusRegion {
	return []core.BusRegion{
		{Name: "wraml", Start: 0x00200000, Size: 0x100000},
		{Name: "wramh", Start: 0x06000000, Size: 0x100000},
	}
}

// locate folds a native address onto the backing buffer, returning the
// slice from the byte to its region's end. Folding mirrors and
// partition views here models the machine's bus decode; the server's
// gate only ever passes canonical addresses.
func (m *fakeMachine) locate(addr uint32) ([]byte, bool) {
	addr &= 0x1FFFFFFF
	switch {
	case addr >= 0x00200000 && addr < 0x00300000:
		return m.wram[addr-0x00200000 : 0x100000], true
	case addr >= 0x06000000 && addr < 0x08000000:
		off := (addr - 0x06000000) % 0x100000
		return m.wram[0x100000+off:], true
	}
	return nil, false
}

func (m *fakeMachine) ReadMemory(addr uint32, buf []byte) uint32 {
	ram, ok := m.locate(addr)
	if !ok {
		return 0
	}
	return uint32(copy(buf, ram))
}

func (m *fakeMachine) WriteMemory(addr uint32, data []byte) uint32 {
	ram, ok := m.locate(addr)
	if !ok {
		return 0
	}
	return uint32(copy(ram, data))
}

func (m *fakeMachine) Serialize() ([]byte, error) {
	state := make([]byte, len(m.wram))
	copy(state, m.wram[:])
	return state, nil
}

func (m *fakeMachine) Deserialize(data []byte) error {
	copy(m.wram[:], data)
	return nil
}

// 0x06001000 is WRAM-H offset 0x1000, fake buffer offset 0x101000.
const fakeAddr = 0x101000

func newFakeServer() (*Server, *fakeMachine) {
	m := &fakeMachine{}
	c := &Server{paused: new(atomic.Bool)}
	c.setMachine(m)
	return c, m
}

func TestReadCommandData(t *testing.T) {
	c, m := newFakeServer()
	copy(m.wram[fakeAddr:], []byte{0xDE, 0xAD, 0xBE, 0xEF})
	r := runLine(t, c, "read 0x06001000 4")
	want := dumpLine(0x06001000, "DE AD BE EF", "....")
	if r != want {
		t.Fatalf("read output mismatch\ngot:\n%s\nwant:\n%s", r, want)
	}
	// Non-canonical spellings are outside the known regions.
	if r2 := runLine(t, c, "read 0x26101000 4"); !strings.HasPrefix(r2, "error:") {
		t.Fatalf("mirror read accepted: %q", r2)
	}
}

func TestWriteCommand(t *testing.T) {
	c, m := newFakeServer()
	if r := runLine(t, c, "write 0x06001000 DEAD"); r != "wrote 2 bytes at 0x06001000" {
		t.Fatalf("write response %q", r)
	}
	if m.wram[fakeAddr] != 0xDE || m.wram[fakeAddr+1] != 0xAD {
		t.Fatalf("write did not land: %#x %#x", m.wram[fakeAddr], m.wram[fakeAddr+1])
	}
	// Non-canonical spellings are outside the known regions.
	if r := runLine(t, c, "write 0x26101000 BEEF"); !strings.HasPrefix(r, "error:") {
		t.Fatalf("mirror write accepted: %q", r)
	}
	// The write is visible to a read.
	if r := runLine(t, c, "read 0x06001000 2"); r != dumpLine(0x06001000, "DE AD", "..") {
		t.Fatalf("read after write mismatch:\n%s", r)
	}
}

func TestWriteCommandValidation(t *testing.T) {
	c := newTestServer()
	for _, bad := range []string{
		"write",
		"write 0x06001000",
		"write 0x06001000 DEAD extra",
		"write nonsense DEAD",
		"write 0x05C00000 DEAD",
		"write 0x06001000 XYZ",
		"write 0x06001000 ABC",
		"write 0x060FFFFF DEAD",
	} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
}

func TestSearchNarrowing(t *testing.T) {
	c, m := newFakeServer()
	m.wram[fakeAddr] = 10
	m.wram[fakeAddr+1] = 77

	if r := runLine(t, c, "baseline wramh"); r != "baseline over wramh: 1048576 candidates (w8)" {
		t.Fatalf("baseline response %q", r)
	}
	// Only the health byte decreases.
	m.wram[fakeAddr] = 9
	if r := runLine(t, c, "filter dec"); r != "1048576 -> 1 candidates" {
		t.Fatalf("filter dec response %q", r)
	}
	list := runLine(t, c, "list")
	if !strings.Contains(list, "0x06001000  cur=9 (0x09)  base=9 (0x09)") ||
		!strings.Contains(list, "1 candidates") {
		t.Fatalf("list output:\n%s", list)
	}
	// The unchanged neighbor was eliminated and stays eliminated even
	// if it later decreases.
	m.wram[fakeAddr+1] = 5
	if r := runLine(t, c, "filter dec"); r != "1 -> 0 candidates" {
		t.Fatalf("second filter response %q", r)
	}
}

// TestRebaseCrossTrialIntersection drives the multi-trial workflow
// rebase exists for. Trial one diffs across an event. The memory is
// then rolled back and rebase re-anchors without losing candidates, so
// a control trial can prune values that also change without the event.
func TestRebaseCrossTrialIntersection(t *testing.T) {
	c, m := newFakeServer()
	const flag = fakeAddr      // changes only during the event
	const churn = fakeAddr + 4 // changes in every trial

	if r := runLine(t, c, "rebase"); !strings.Contains(r, "no search active") {
		t.Fatalf("rebase without search: %q", r)
	}

	runLine(t, c, "baseline wramh")
	// Event trial: both the flag and the churn value change.
	m.wram[flag] = 1
	m.wram[churn] = 9
	if r := runLine(t, c, "filter diff"); r != "1048576 -> 2 candidates" {
		t.Fatalf("event trial: %q", r)
	}

	// Roll back (as restore would) and re-anchor.
	m.wram[flag] = 0
	m.wram[churn] = 0
	if r := runLine(t, c, "rebase"); r != "rebased, 2 candidates kept" {
		t.Fatalf("rebase: %q", r)
	}

	// Control trial: only the churn value changes.
	m.wram[churn] = 5
	if r := runLine(t, c, "filter same"); r != "2 -> 1 candidates" {
		t.Fatalf("control trial: %q", r)
	}
	if list := runLine(t, c, "list"); !strings.Contains(list, "0x06001000 ") {
		t.Fatalf("survivor should be the flag:\n%s", list)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	c, m := newFakeServer()
	m.wram[fakeAddr] = 42

	if r := runLine(t, c, "snapshot spot"); !strings.HasPrefix(r, `snapshot "spot" saved`) {
		t.Fatalf("snapshot response %q", r)
	}
	m.wram[fakeAddr] = 7
	if r := runLine(t, c, "restore spot"); r != `restored "spot"` {
		t.Fatalf("restore response %q", r)
	}
	if m.wram[fakeAddr] != 42 {
		t.Fatalf("restore did not roll back memory: %d", m.wram[fakeAddr])
	}
	if r := runLine(t, c, "snapshots"); !strings.Contains(r, "spot") {
		t.Fatalf("snapshots list %q", r)
	}
}

func TestWatchReportsChange(t *testing.T) {
	c, m := newFakeServer()
	c.out = make(chan string, 4)
	m.wram[fakeAddr] = 6

	runLine(t, c, "watch 0x06001000")
	c.Service(100) // seeds silently
	select {
	case line := <-c.out:
		t.Fatalf("seed read reported a change: %q", line)
	default:
	}

	m.wram[fakeAddr] = 5
	c.Service(101)
	select {
	case line := <-c.out:
		want := "[WATCH] frame=101 0x06001000 w8: 6 -> 5 (0x06 -> 0x05)\n"
		if line != want {
			t.Fatalf("watch line %q, want %q", line, want)
		}
	default:
		t.Fatal("no watch line pushed")
	}
}
