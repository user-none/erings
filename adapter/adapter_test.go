// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package adapter

import (
	"errors"
	"testing"

	"github.com/user-none/eblitui/coreif"
)

// fakeDisc is a minimal coreif.DiscReader for exercising the adapter's
// disc handling without a real CHD.
type fakeDisc struct {
	sector0 []byte
	readErr error
	tracks  [][6]any // number, typ, frames, pregap, startLBA, control
}

func (d *fakeDisc) ReadSector(lba int) ([]byte, error) {
	if d.readErr != nil {
		return nil, d.readErr
	}
	if lba == 0 {
		return d.sector0, nil
	}
	return make([]byte, 2352), nil
}

func (d *fakeDisc) NumTracks() int { return len(d.tracks) }

func (d *fakeDisc) Track(i int) (number int, typ string, frames int, pregap int, startLBA int, control uint8) {
	t := d.tracks[i]
	return t[0].(int), t[1].(string), t[2].(int), t[3].(int), t[4].(int), t[5].(uint8)
}

func (d *fakeDisc) NumTrackIndexes(i int) int { return 1 }

func (d *fakeDisc) TrackIndex(i, n int) (indexNumber int, lba int) {
	t := d.tracks[i]
	return 1, t[4].(int) + t[3].(int) // INDEX 01 at startLBA + pregap
}

func (d *fakeDisc) Close() error { return nil }

// makeSaturnSector0 builds a sector-0 image with the given product
// number, device-info field ("CD-n/m" or "" to leave blank/space), and
// game title.
func makeSaturnSector0(serial, deviceInfo, title string) []byte {
	data := make([]byte, 2352)
	user := data[16:]
	copy(user[0x00:0x10], []byte("SEGA SEGASATURN "))
	for i := 0; i < 10; i++ {
		user[0x20+i] = ' '
	}
	copy(user[0x20:0x2A], []byte(serial))
	for i := 0; i < 8; i++ {
		user[0x38+i] = ' '
	}
	copy(user[0x38:0x40], []byte(deviceInfo))
	for i := 0; i < 0x70; i++ {
		user[0x60+i] = ' '
	}
	copy(user[0x60:0x60+0x70], []byte(title))
	return data
}

func TestDiscInfo(t *testing.T) {
	f := &Factory{}

	t.Run("single-disc Saturn", func(t *testing.T) {
		d := &fakeDisc{sector0: makeSaturnSector0("T-31202G", "CD-1/1", "DAYTONA USA")}
		di, ok := f.DiscInfo(d)
		if !ok {
			t.Fatal("DiscInfo ok=false, want true")
		}
		if di.ProductNumber != "T-31202G" {
			t.Errorf("ProductNumber = %q, want T-31202G", di.ProductNumber)
		}
		if di.DiscNumber != 1 || di.DiscTotal != 1 {
			t.Errorf("disc %d/%d, want 1/1", di.DiscNumber, di.DiscTotal)
		}
		if di.Title != "DAYTONA USA" {
			t.Errorf("Title = %q, want DAYTONA USA", di.Title)
		}
	})

	t.Run("multi-disc disc 2 of 4", func(t *testing.T) {
		d := &fakeDisc{sector0: makeSaturnSector0("MK-81307", "CD-2/4", "PANZER DRAGOON SAGA")}
		di, ok := f.DiscInfo(d)
		if !ok {
			t.Fatal("DiscInfo ok=false, want true")
		}
		if di.ProductNumber != "MK-81307" {
			t.Errorf("ProductNumber = %q, want MK-81307", di.ProductNumber)
		}
		if di.DiscNumber != 2 || di.DiscTotal != 4 {
			t.Errorf("disc %d/%d, want 2/4", di.DiscNumber, di.DiscTotal)
		}
	})

	t.Run("malformed device info defaults to 1/1", func(t *testing.T) {
		d := &fakeDisc{sector0: makeSaturnSector0("MK-81307", "GARBAGE", "X")}
		di, ok := f.DiscInfo(d)
		if !ok {
			t.Fatal("DiscInfo ok=false, want true")
		}
		if di.DiscNumber != 1 || di.DiscTotal != 1 {
			t.Errorf("disc %d/%d, want 1/1 for malformed device info", di.DiscNumber, di.DiscTotal)
		}
	})

	t.Run("non-Saturn disc", func(t *testing.T) {
		bad := make([]byte, 2352)
		copy(bad[16:32], []byte("NOT A SATURN HDR"))
		d := &fakeDisc{sector0: bad}
		if _, ok := f.DiscInfo(d); ok {
			t.Error("DiscInfo ok=true, want false for non-Saturn")
		}
	})

	t.Run("short sector", func(t *testing.T) {
		d := &fakeDisc{sector0: make([]byte, 16)}
		if _, ok := f.DiscInfo(d); ok {
			t.Error("DiscInfo ok=true for short sector, want false")
		}
	})

	t.Run("read error", func(t *testing.T) {
		d := &fakeDisc{readErr: errors.New("boom")}
		if _, ok := f.DiscInfo(d); ok {
			t.Error("DiscInfo ok=true on read error, want false")
		}
	})
}

func TestParseDeviceInfo(t *testing.T) {
	tests := []struct {
		in   string
		n, m int
		ok   bool
	}{
		{"CD-1/1", 1, 1, true},
		{"CD-2/4", 2, 4, true},
		{"CD-1/4 ", 1, 4, true},
		{" CD-3/3 ", 3, 3, true},
		{"CD-12/12", 12, 12, true},
		{"", 0, 0, false},
		{"GARBAGE", 0, 0, false},
		{"CD-1", 0, 0, false},
		{"CD-/4", 0, 0, false},
		{"CD-1/", 0, 0, false},
		{"CD-5/4", 0, 0, false},
		{"CD-0/4", 0, 0, false},
	}
	for _, tc := range tests {
		n, m, ok := parseDeviceInfo(tc.in)
		if ok != tc.ok || (ok && (n != tc.n || m != tc.m)) {
			t.Errorf("parseDeviceInfo(%q) = %d,%d,%v want %d,%d,%v", tc.in, n, m, ok, tc.n, tc.m, tc.ok)
		}
	}
}

func TestSetBIOSError(t *testing.T) {
	e := (&Factory{}).CreateEmulator()
	defer e.Close()

	if err := e.SetBIOS("bogus_key", make([]byte, 524288)); err == nil {
		t.Error("SetBIOS with unknown key returned nil, want error")
	}
	if err := e.SetBIOS("main_bios", make([]byte, 10)); err == nil {
		t.Error("SetBIOS with wrong size returned nil, want error")
	}
}

func TestBatterySaver(t *testing.T) {
	e := (&Factory{}).CreateEmulator()
	defer e.Close()

	bs, ok := e.(coreif.BatterySaver)
	if !ok {
		t.Fatal("emulator does not implement coreif.BatterySaver")
	}

	if !bs.HasSRAM() {
		t.Error("HasSRAM() = false, want true (Saturn always has internal backup RAM)")
	}

	// Round-trip the freshly formatted backup RAM.
	snap := bs.GetSRAM()
	if len(snap) == 0 {
		t.Fatal("GetSRAM() returned empty buffer")
	}
	bs.SetSRAM(snap)
	if got := bs.GetSRAM(); len(got) != len(snap) || got[0] != snap[0] {
		t.Errorf("SetSRAM(GetSRAM()) did not round-trip: len %d/%d", len(got), len(snap))
	}

	// A full-size buffer set via SetSRAM is returned by GetSRAM.
	custom := make([]byte, len(snap))
	custom[0] = 0x5A
	custom[len(custom)-1] = 0xA5
	bs.SetSRAM(custom)
	got := bs.GetSRAM()
	if got[0] != 0x5A || got[len(got)-1] != 0xA5 {
		t.Errorf("after SetSRAM, GetSRAM = [0]0x%02X [last]0x%02X, want 0x5A 0xA5",
			got[0], got[len(got)-1])
	}

	// Wrong-size input must not alter contents.
	bs.SetSRAM([]byte{1, 2, 3})
	if after := bs.GetSRAM(); after[0] != 0x5A {
		t.Errorf("wrong-size SetSRAM changed contents: byte 0 = 0x%02X, want 0x5A", after[0])
	}
}

func TestGetTiming(t *testing.T) {
	e := (&Factory{}).CreateEmulator()
	defer e.Close()

	tm := e.GetTiming()
	if tm.FPS != 50 && tm.FPS != 60 {
		t.Errorf("FPS = %d, want 50 or 60", tm.FPS)
	}
	if tm.Scanlines <= 0 {
		t.Errorf("Scanlines = %d, want > 0", tm.Scanlines)
	}
}
