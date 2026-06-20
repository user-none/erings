// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"image/png"
	"testing"
	"time"
)

func TestScreenshotName(t *testing.T) {
	tests := []struct {
		id    string
		ts    int64
		frame int
		want  string
	}{
		{"MK-81088", 1718900000, 0, "MK-81088_1718900000_0.png"},
		{"GS-9100", 1718900000, 42, "GS-9100_1718900000_42.png"},
		{"unknown", 1700000000, 1234, "unknown_1700000000_1234.png"},
	}
	for _, tc := range tests {
		if got := screenshotName(tc.id, tc.ts, tc.frame); got != tc.want {
			t.Errorf("screenshotName(%q, %d, %d) = %q, want %q", tc.id, tc.ts, tc.frame, got, tc.want)
		}
	}
}

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"MK-81088", "MK-81088"},
		{"GS_9100.v2", "GS_9100.v2"},
		{"", "unknown"},
		{"../../etc", ".._.._etc"},
		{"a/b\\c", "a_b_c"},
		{"esc\x1b[0m", "esc__0m"},
	}
	for _, tc := range tests {
		if got := sanitizeID(tc.in); got != tc.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncodeFramebufferPNG(t *testing.T) {
	const (
		width  = 4
		height = 2
		stride = width * 4
	)
	fb := make([]byte, stride*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := y*stride + x*4
			fb[i+0] = byte(10 * x)       // R
			fb[i+1] = byte(20 * y)       // G
			fb[i+2] = byte(30 * (x + y)) // B
			fb[i+3] = 0xFF               // A (opaque, avoids premultiply ambiguity)
		}
	}

	var buf bytes.Buffer
	if err := encodeFramebufferPNG(&buf, fb, stride, height); err != nil {
		t.Fatalf("encodeFramebufferPNG: %v", err)
	}

	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != width || b.Dy() != height {
		t.Fatalf("decoded bounds %v, want %dx%d", b, width, height)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			wantR, wantG, wantB := uint32(10*x), uint32(20*y), uint32(30*(x+y))
			if r>>8 != wantR || g>>8 != wantG || b>>8 != wantB || a>>8 != 0xFF {
				t.Errorf("pixel (%d,%d) = (%d,%d,%d,%d), want (%d,%d,%d,255)",
					x, y, r>>8, g>>8, b>>8, a>>8, wantR, wantG, wantB)
			}
		}
	}
}

func TestEncodeFramebufferPNGShortBuffer(t *testing.T) {
	// Buffer holds only one row but height claims two; the copy must stop at
	// the buffer end without panicking and still produce a valid image.
	const (
		width  = 4
		height = 2
		stride = width * 4
	)
	fb := make([]byte, stride) // one row only
	var buf bytes.Buffer
	if err := encodeFramebufferPNG(&buf, fb, stride, height); err != nil {
		t.Fatalf("encodeFramebufferPNG short buffer: %v", err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != width || b.Dy() != height {
		t.Fatalf("decoded bounds %v, want %dx%d", b, width, height)
	}
}

func TestEncodeFramebufferPNGInvalidDims(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeFramebufferPNG(&buf, nil, 0, 4); err == nil {
		t.Error("expected error for zero stride, got nil")
	}
	if err := encodeFramebufferPNG(&buf, make([]byte, 16), 16, 0); err == nil {
		t.Error("expected error for zero height, got nil")
	}
}

func TestClassifyHealth(t *testing.T) {
	tests := []struct {
		name   string
		gap    time.Duration
		frames uint64
		want   healthState
	}{
		{"healthy fast", 5 * time.Millisecond, 600, healthy},
		{"healthy at floor", 50 * time.Millisecond, healthFloor, healthy},
		{"slow below floor", 200 * time.Millisecond, healthFloor - 1, slow},
		{"slow one frame", 500 * time.Millisecond, 1, slow},
		{"frozen no frames", freezeGap + time.Millisecond, 0, frozen},
		{"frozen overrides slow count", 3 * time.Second, 5, frozen},
		{"gap at threshold not frozen", freezeGap, healthFloor, healthy},
	}
	for _, tc := range tests {
		if got := classifyHealth(tc.gap, tc.frames); got != tc.want {
			t.Errorf("%s: classifyHealth(%v, %d) = %v, want %v", tc.name, tc.gap, tc.frames, got, tc.want)
		}
	}
}
