// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package replay

import (
	"reflect"
	"testing"
)

func TestRecordFrameFirstAlwaysEmitted(t *testing.T) {
	r := NewRecorder()
	r.RecordFrame(0, 0)
	f := r.snapshot("")
	want := []InputEvent{{F: 0, P1: 0, P2: 0}}
	if !reflect.DeepEqual(f.Input, want) {
		t.Fatalf("input = %v, want %v", f.Input, want)
	}
	if f.Frames != 1 {
		t.Fatalf("frames = %d, want 1", f.Frames)
	}
}

func TestRecordFrameNoDuplicateEvents(t *testing.T) {
	r := NewRecorder()
	r.RecordFrame(0, 0)
	r.RecordFrame(0, 0)
	r.RecordFrame(0, 0)
	f := r.snapshot("")
	if len(f.Input) != 1 {
		t.Fatalf("events = %d, want 1", len(f.Input))
	}
	if f.Frames != 3 {
		t.Fatalf("frames = %d, want 3", f.Frames)
	}
}

func TestRecordFrameEmitsOnChange(t *testing.T) {
	r := NewRecorder()
	r.RecordFrame(0, 0)    // f0 baseline
	r.RecordFrame(0, 0)    // no change
	r.RecordFrame(4096, 0) // f2 change on p1
	r.RecordFrame(4096, 0) // no change
	r.RecordFrame(4096, 7) // f4 change on p2
	f := r.snapshot("")
	want := []InputEvent{
		{F: 0, P1: 0, P2: 0},
		{F: 2, P1: 4096, P2: 0},
		{F: 4, P1: 4096, P2: 7},
	}
	if !reflect.DeepEqual(f.Input, want) {
		t.Fatalf("input = %v, want %v", f.Input, want)
	}
	if f.Frames != 5 {
		t.Fatalf("frames = %d, want 5", f.Frames)
	}
}

func TestMarkScreenshotMarksLastCompletedFrame(t *testing.T) {
	r := NewRecorder()
	for i := 0; i < 3; i++ {
		r.RecordFrame(0, 0)
	}
	if got := r.MarkScreenshot(); got != 2 {
		t.Fatalf("MarkScreenshot = %d, want 2", got)
	}
}

func TestMarkScreenshotBeforeAnyFrame(t *testing.T) {
	r := NewRecorder()
	if got := r.MarkScreenshot(); got != 0 {
		t.Fatalf("MarkScreenshot = %d, want 0", got)
	}
	f := r.snapshot("")
	if !reflect.DeepEqual(f.Screenshots, []int{0}) {
		t.Fatalf("screenshots = %v, want [0]", f.Screenshots)
	}
}

func TestPlayerReproducesHeldState(t *testing.T) {
	f := &File{
		Frames: 6,
		Input: []InputEvent{
			{F: 0, P1: 0, P2: 0},
			{F: 2, P1: 4096, P2: 0},
			{F: 4, P1: 4096, P2: 7},
		},
	}
	p := NewPlayer(f)
	wantP1 := []uint32{0, 0, 4096, 4096, 4096, 4096}
	wantP2 := []uint32{0, 0, 0, 0, 7, 7}
	for i := 0; i < f.Frames; i++ {
		g1, g2, active := p.Next()
		if !active {
			t.Fatalf("frame %d: active = false, want true", i)
		}
		if g1 != wantP1[i] || g2 != wantP2[i] {
			t.Fatalf("frame %d: got (%d,%d), want (%d,%d)", i, g1, g2, wantP1[i], wantP2[i])
		}
	}
}

func TestPlayerInactiveAfterFrames(t *testing.T) {
	f := &File{
		Frames: 2,
		Input:  []InputEvent{{F: 0, P1: 4096, P2: 0}},
	}
	p := NewPlayer(f)
	p.Next()
	p.Next()
	g1, g2, active := p.Next()
	if active || g1 != 0 || g2 != 0 {
		t.Fatalf("after exhaustion: got (%d,%d,active=%v), want (0,0,false)", g1, g2, active)
	}
}

func TestPlayerShouldScreenshot(t *testing.T) {
	f := &File{
		Frames:      4,
		Input:       []InputEvent{{F: 0, P1: 0, P2: 0}},
		Screenshots: []int{1, 3},
	}
	p := NewPlayer(f)
	want := []bool{false, true, false, true}
	for i := 0; i < f.Frames; i++ {
		p.Next()
		if got := p.ShouldScreenshot(); got != want[i] {
			t.Fatalf("frame %d: ShouldScreenshot = %v, want %v", i, got, want[i])
		}
	}
}

func TestSnapshotFields(t *testing.T) {
	r := NewRecorder()
	r.RecordFrame(0, 0)
	r.RecordFrame(1, 0)
	r.MarkScreenshot()
	f := r.snapshot("T-1234G")
	want := File{
		Version: Version,
		DiscID:  "T-1234G",
		Frames:  2,
		Input: []InputEvent{
			{F: 0, P1: 0, P2: 0},
			{F: 1, P1: 1, P2: 0},
		},
		Screenshots: []int{1},
	}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("snapshot = %+v, want %+v", f, want)
	}
}
