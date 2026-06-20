// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package replay defines the on-disk format for recorded play sessions
// along with a recorder that captures per-frame controller input and
// screenshot markers and a player that reproduces them.
package replay

import (
	"encoding/json"
	"os"
	"sync"
)

// Version is the replay file format version.
const Version = 1

// InputEvent is a controller-state change at a given frame. The state
// holds until the next event, so a held input is a single entry.
type InputEvent struct {
	F  int    `json:"f"`
	P1 uint32 `json:"p1"`
	P2 uint32 `json:"p2"`
}

// File is the full replay document.
type File struct {
	Version     int          `json:"version"`
	DiscID      string       `json:"discId"`
	Frames      int          `json:"frames"`
	Input       []InputEvent `json:"input"`
	Screenshots []int        `json:"screenshots"`
}

// Recorder accumulates input change-events and screenshot markers while a
// session plays, then writes them to a replay file. All indices are frame
// counts, incremented once per recorded frame.
type Recorder struct {
	mu          sync.Mutex
	frame       int
	started     bool
	lastP1      uint32
	lastP2      uint32
	input       []InputEvent
	screenshots []int
}

// NewRecorder returns an empty recorder positioned at frame 0.
func NewRecorder() *Recorder {
	return &Recorder{}
}

// RecordFrame records the input applied to the next frame and advances
// the frame counter. An event is appended only when the state differs
// from the previous frame, plus always on the first frame so the baseline
// state is captured at frame 0.
func (r *Recorder) RecordFrame(p1, p2 uint32) {
	r.mu.Lock()
	if !r.started || p1 != r.lastP1 || p2 != r.lastP2 {
		r.input = append(r.input, InputEvent{F: r.frame, P1: p1, P2: p2})
		r.lastP1 = p1
		r.lastP2 = p2
		r.started = true
	}
	r.frame++
	r.mu.Unlock()
}

// MarkScreenshot marks the most-recently-completed frame as a screenshot
// point and returns its index. Before any frame has been recorded it marks
// frame 0.
func (r *Recorder) MarkScreenshot() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.frame - 1
	if f < 0 {
		f = 0
	}
	r.screenshots = append(r.screenshots, f)
	return f
}

// snapshot copies the current state into a File under the lock.
func (r *Recorder) snapshot(discID string) File {
	r.mu.Lock()
	defer r.mu.Unlock()
	return File{
		Version:     Version,
		DiscID:      discID,
		Frames:      r.frame,
		Input:       append([]InputEvent(nil), r.input...),
		Screenshots: append([]int(nil), r.screenshots...),
	}
}

// Write snapshots the recorder and writes it as indented JSON to path.
func (r *Recorder) Write(path, discID string) error {
	data, err := json.MarshalIndent(r.snapshot(discID), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Load reads and parses a replay file from path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Player walks a File's input change-events one frame at a time. It
// reproduces the recorded controller state: each change-event applies at
// its frame and holds until the next one. Frames are produced in order
// from 0; the player stops contributing input once the recorded frame
// count is reached.
type Player struct {
	input       []InputEvent
	frames      int
	screenshots map[int]bool
	cursor      int
	frame       int
	p1          uint32
	p2          uint32
}

// NewPlayer returns a Player positioned at frame 0 for the given file.
func NewPlayer(f *File) *Player {
	shots := make(map[int]bool, len(f.Screenshots))
	for _, s := range f.Screenshots {
		shots[s] = true
	}
	return &Player{input: f.Input, frames: f.Frames, screenshots: shots}
}

// Frames returns the total number of recorded frames.
func (p *Player) Frames() int {
	return p.frames
}

// Next returns the recorded input for the next frame and advances the
// player. It must be called once per frame in order. active is false once
// the recorded frame count is exhausted, at which point p1/p2 are zero and
// the caller should fall back to live input only.
func (p *Player) Next() (p1, p2 uint32, active bool) {
	if p.frame >= p.frames {
		return 0, 0, false
	}
	for p.cursor < len(p.input) && p.input[p.cursor].F <= p.frame {
		p.p1 = p.input[p.cursor].P1
		p.p2 = p.input[p.cursor].P2
		p.cursor++
	}
	p.frame++
	return p.p1, p.p2, true
}

// ShouldScreenshot reports whether the frame most recently produced by Next
// is a marked screenshot point.
func (p *Player) ShouldScreenshot() bool {
	return p.screenshots[p.frame-1]
}
