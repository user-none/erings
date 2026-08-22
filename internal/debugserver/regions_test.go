// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in   string
		want uint32
		ok   bool
	}{
		{"0x06001000", 0x06001000, true},
		{"0X06001000", 0x06001000, true},
		{"0x0", 0, true},
		{"2097152", 0x00200000, true},
		{"0xFFFFFFFF", 0xFFFFFFFF, true},
		{"", 0, false},
		{"0x", 0, false},
		{"xyz", 0, false},
		{"0x1G", 0, false},
		{"-5", 0, false},
		{"0x100000000", 0, false},
	}
	for _, c := range cases {
		got, err := parseAddress(c.in)
		if c.ok != (err == nil) {
			t.Errorf("%q: err = %v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("%q: got 0x%08X, want 0x%08X", c.in, got, c.want)
		}
	}
}

func TestLookupRegion(t *testing.T) {
	cases := []struct {
		addr    uint32
		region  string
		off     uint32
		outside bool
	}{
		// Canonical range boundaries.
		{0x00200000, "wraml", 0x00000, false},
		{0x002FFFFF, "wraml", 0xFFFFF, false},
		{0x06000000, "wramh", 0x00000, false},
		{0x060FFFFF, "wramh", 0xFFFFF, false},
		// Addresses are canonical only: partition spellings and mirror
		// spellings are outside the known regions.
		{0x26001000, "", 0, true},
		{0x20200010, "", 0, true},
		{0x06100000, "", 0, true},
		{0x07FFFFFF, "", 0, true},
		// Adjacent addresses are outside.
		{0x001FFFFF, "", 0, true},
		{0x00300000, "", 0, true},
		// Other hardware areas are not regions.
		{0x05C00000, "", 0, true},
		{0x05E00000, "", 0, true},
		{0x00000000, "", 0, true},
	}
	srv := newTestServer()
	for _, c := range cases {
		r, off, err := srv.lookupRegion(c.addr)
		if c.outside {
			if err == nil {
				t.Errorf("0x%08X: expected outside-region error, got %s+0x%X", c.addr, r.Name, off)
			} else if !strings.Contains(err.Error(), "wraml") || !strings.Contains(err.Error(), "wramh") {
				t.Errorf("0x%08X: error should name the valid ranges: %v", c.addr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("0x%08X: unexpected error %v", c.addr, err)
			continue
		}
		if r.Name != c.region || off != c.off {
			t.Errorf("0x%08X: got %s+0x%05X, want %s+0x%05X", c.addr, r.Name, off, c.region, c.off)
		}
	}
}

// dumpLine builds one expected hex-dump line. The hex field is 49
// columns (16 x 3 plus the mid-line gap) so the ASCII gutter always
// aligns.
func dumpLine(addr uint32, hex, ascii string) string {
	return fmt.Sprintf("0x%08X  %-49s |%s|", addr, hex, ascii)
}

func TestFormatHexDump(t *testing.T) {
	data := make([]byte, 20)
	for i := range data {
		data[i] = byte(i)
	}
	data[4] = 'A'
	got := formatHexDump(0x06001000, data)
	want := dumpLine(0x06001000, "00 01 02 03 41 05 06 07  08 09 0A 0B 0C 0D 0E 0F", "....A...........") + "\n" +
		dumpLine(0x06001010, "10 11 12 13", "....")
	if got != want {
		t.Errorf("hex dump mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatHexDumpSingleByte(t *testing.T) {
	got := formatHexDump(0x00200000, []byte{0x7E})
	want := dumpLine(0x00200000, "7E", "~")
	if got != want {
		t.Errorf("hex dump mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestReadCommandValidation(t *testing.T) {
	c := newTestServer()
	for _, bad := range []string{
		"read",
		"read 0x06000000 64 extra",
		"read nonsense",
		"read 0x05C00000",
		"read 0x06000000 0",
		"read 0x06000000 4097",
		"read 0x06000000 -1",
	} {
		if r := runLine(t, c, bad); !strings.HasPrefix(r, "error:") {
			t.Fatalf("%q: unexpected response %q", bad, r)
		}
	}
}

func TestRegionsCommand(t *testing.T) {
	c := newTestServer()
	r := runLine(t, c, "regions")
	for _, want := range []string{"wraml", "wramh", "0x00200000-0x002FFFFF", "0x06000000-0x060FFFFF", "1024KB"} {
		if !strings.Contains(r, want) {
			t.Fatalf("regions output missing %q:\n%s", want, r)
		}
	}
}
