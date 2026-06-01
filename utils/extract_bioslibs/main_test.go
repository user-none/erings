// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"testing"
)

func be16(v uint16) []byte {
	return []byte{byte(v >> 8), byte(v)}
}

// tok describes one token within a hand-built block.
type tok struct {
	literal bool
	word    uint16
	extra   []byte // trailing length byte for a $1000 long match
}

// clampStream builds a single-main-block compressed stream from the given
// meaningful tokens. A trailing dummy literal token (selector bit = len(tokens))
// follows so that, once the output reaches maxOut, the block stops on the
// remaining<2 path without emitting any padding. The caller must set maxOut to
// the exact expected output length. trunc and tail are zero.
func clampStream(tokens []tok) []byte {
	var sel uint16
	var body []byte
	for i, tk := range tokens {
		if tk.literal {
			sel |= 1 << uint(i)
		}
		body = append(body, be16(tk.word)...)
		body = append(body, tk.extra...)
	}
	sel |= 1 << uint(len(tokens)) // dummy literal stops the block when full
	body = append(body, be16(0)...)

	s := be16(0x1001)         // header: compressed
	s = append(s, be16(1)...) // block_count = 1
	s = append(s, be16(sel)...)
	s = append(s, body...)
	s = append(s, 0, 0) // trunc, tail
	return s
}

func TestDecompressLiterals(t *testing.T) {
	in := []tok{
		{literal: true, word: 0x4142},
		{literal: true, word: 0x4344},
		{literal: true, word: 0x4546},
		{literal: true, word: 0x4748},
	}
	want := []byte{0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48}
	got, err := decompress(clampStream(in), len(want))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestDecompressStandardMatch(t *testing.T) {
	// Literal "AB", then a standard match: length (token>>12)+1 = 4, offset 2.
	in := []tok{
		{literal: true, word: 0x4142},
		{word: 0x3002},
	}
	want := []byte{0x41, 0x42, 0x41, 0x42, 0x41, 0x42}
	got, err := decompress(clampStream(in), len(want))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestDecompressShortMatchRLE(t *testing.T) {
	// Literal "AB", then a short match: length (token&0xFFF)+3 = 6, offset 1,
	// replicating the previous byte (0x42).
	in := []tok{
		{literal: true, word: 0x4142},
		{word: 0x0003},
	}
	want := []byte{0x41, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42}
	got, err := decompress(clampStream(in), len(want))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestDecompressLongMatch(t *testing.T) {
	// Literal "AB", then a long match: extra byte 0, length 0+17 = 17, offset 1,
	// replicating the previous byte (0x42).
	in := []tok{
		{literal: true, word: 0x4142},
		{word: 0x1001, extra: []byte{0x00}},
	}
	want := []byte{0x41, 0x42}
	for i := 0; i < 17; i++ {
		want = append(want, 0x42)
	}
	got, err := decompress(clampStream(in), len(want))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestDecompressCapClamp(t *testing.T) {
	// Three literal tokens (8 bytes of content) but a 5-byte cap: the third
	// token writes only its high byte before the block stops.
	s := be16(0x1001)
	s = append(s, be16(1)...)      // block_count = 1
	s = append(s, be16(0x0007)...) // selector: tokens 0,1,2 literal
	s = append(s, be16(0x4142)...)
	s = append(s, be16(0x4344)...)
	s = append(s, be16(0x4546)...)
	s = append(s, 0, 0) // trunc, tail

	want := []byte{0x41, 0x42, 0x43, 0x44, 0x45}
	got, err := decompress(s, 5)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestDecompressTailBlockAndTruncation(t *testing.T) {
	// Main block: 16 literal tokens of 0x4142 -> 32 bytes.
	// Tail block: 2 literal tokens of 0x4344 -> 4 bytes (proves the tail ran).
	// trunc 3 trims the last 3 bytes.
	s := be16(0x1001)
	s = append(s, be16(1)...)      // block_count = 1
	s = append(s, be16(0xFFFF)...) // main selector: all literal
	for i := 0; i < 16; i++ {
		s = append(s, be16(0x4142)...)
	}
	s = append(s, 3, 2)            // trunc = 3, tail token count = 2
	s = append(s, be16(0x0003)...) // tail selector: tokens 0,1 literal
	s = append(s, be16(0x4344)...)
	s = append(s, be16(0x4344)...)

	got, err := decompress(s, 1000)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	// 32 + 4 = 36 bytes produced, minus 3 trimmed = 33.
	if len(got) != 33 {
		t.Fatalf("len = %d, want 33", len(got))
	}
	if got[0] != 0x41 || got[31] != 0x42 {
		t.Fatalf("main-block bytes wrong: got[0]=%#x got[31]=%#x", got[0], got[31])
	}
	if got[32] != 0x43 {
		t.Fatalf("tail-block byte wrong: got[32]=%#x, want 0x43", got[32])
	}
}

func TestDecompressBlockCountZero(t *testing.T) {
	s := append(be16(0x1001), be16(0)...)
	if _, err := decompress(s, 100); err == nil {
		t.Fatal("expected error for block_count == 0")
	}
}

func TestDecompressRawHeaderUnsupported(t *testing.T) {
	s := append(be16(0x1000), be16(1)...) // header bit 0 clear = raw
	if _, err := decompress(s, 100); err == nil {
		t.Fatal("expected error for raw (non-compressed) header")
	}
}

func TestDecompressTruncatedStreamNoPanic(t *testing.T) {
	// Header + block_count but no block body: the block read must fail cleanly.
	s := append(be16(0x1001), be16(1)...)
	if _, err := decompress(s, 100); err == nil {
		t.Fatal("expected error for truncated stream")
	}
}

func TestValidateUSBIOSRejects(t *testing.T) {
	if err := validateUSBIOS(make([]byte, 10)); err == nil {
		t.Fatal("expected error for wrong-size input")
	}
	if err := validateUSBIOS(make([]byte, usBIOSSize)); err == nil {
		t.Fatal("expected error for wrong-hash input")
	}
}
