// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package console

import (
	"strings"
	"testing"
)

// fakeMachine implements Machine over a flat 2MB buffer with the same
// window semantics as the core: reads stop at the first out-of-range
// byte.
type fakeMachine struct {
	wram [0x200000]byte
}

func (m *fakeMachine) ReadMemory(addr uint32, buf []byte) uint32 {
	var n uint32
	for i := range buf {
		cur := addr + uint32(i)
		if cur >= uint32(len(m.wram)) {
			break
		}
		buf[i] = m.wram[cur]
		n++
	}
	return n
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

// 0x06001000 is WRAM-H offset 0x1000, flat offset 0x101000.
const fakeAddr = 0x101000

func newFakeConsole() (*Console, *fakeMachine) {
	m := &fakeMachine{}
	c := newTestConsole()
	c.machine = m
	return c, m
}

func TestReadCommandData(t *testing.T) {
	c, m := newFakeConsole()
	copy(m.wram[fakeAddr:], []byte{0xDE, 0xAD, 0xBE, 0xEF})
	r := runLine(t, c, "read 0x06001000 4")
	want := dumpLine(0x06001000, "DE AD BE EF", "....")
	if r != want {
		t.Fatalf("read output mismatch\ngot:\n%s\nwant:\n%s", r, want)
	}
	// A mirror spelling reads the same bytes at the canonical address.
	if r2 := runLine(t, c, "read 0x26101000 4"); r2 != want {
		t.Fatalf("mirror read mismatch:\n%s", r2)
	}
}

func TestSearchNarrowing(t *testing.T) {
	c, m := newFakeConsole()
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

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	c, m := newFakeConsole()
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
	c, m := newFakeConsole()
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
