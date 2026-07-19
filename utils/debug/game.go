// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"sync/atomic"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/user-none/erings/core"
	"github.com/user-none/erings/internal/replay"
)

type game struct {
	emu         *core.Emulator
	audioPlayer *audioPlayer
	fps         int // core frame rate (60 NTSC / 50 PAL), for pacing
	sharedInput *sharedInput
	sharedFB    *sharedFramebuffer
	control     *emuControl
	emuDone     chan struct{}
	keyMap      map[int]ebiten.Key
	padMap      map[int]ebiten.StandardGamepadButton
	offscreen   *ebiten.Image

	// paused, when true, makes emulationLoop skip RunFrame and queue
	// one frame of silence so the audio ring stays fed and oto does not
	// underrun.
	paused atomic.Bool

	// frameTick advances on every emulationLoop iteration. The watchdog
	// goroutine reads it to detect a stalled emulation loop and dump
	// goroutine stacks so we can see exactly what is blocked.
	frameTick atomic.Uint64
	// stage tags the most recent point reached inside one iteration of
	// emulationLoop, so the watchdog can report which stage hung when
	// frameTick stops advancing. Updated with an atomic store of a small
	// integer enum (see stageName).
	stage atomic.Int32

	// histReq is set from Update (ebiten goroutine) on key 9 and
	// consumed in emulationLoop. It is the only histogram state shared
	// across goroutines; histActive and the maps below are owned solely
	// by the emulation goroutine. First press arms capture, second press
	// prints the top PCs and disarms. Repeatable.
	histReq    atomic.Bool
	histActive bool
	histMaster map[uint32]uint64
	histSlave  map[uint32]uint64

	// dumpReq is set from Update (ebiten goroutine) on key 8 and
	// consumed in emulationLoop between frames. The emulation goroutine
	// serializes the machine state and explodes it per chunk field into
	// a timestamped subdirectory under dumpDir.
	dumpReq atomic.Bool
	dumpDir string

	// recorder, when non-nil (-record given), captures per-frame input
	// and screenshot markers. It is written to disk on shutdown.
	// screenshotReq is set from Update (ebiten goroutine) on the space bar
	// and consumed in emulationLoop between frames to append a marker.
	recorder      *replay.Recorder
	screenshotReq atomic.Bool

	// console, when non-nil (-c given), is the network debug console.
	// Its command queue is drained between frames (serviceConsole), so
	// command handlers run on the emulation goroutine and may touch
	// emulation-owned state directly.
	console *console

	// stepRemaining and stepResp implement the console frame command.
	// Both are owned by the emulation goroutine: stepRemaining counts
	// frames still to run while paused, and stepResp holds the pending
	// response channel until the step completes.
	stepRemaining int
	stepResp      chan string

	// emuFrame counts RunFrame calls; watch lines reference it. Owned
	// by the emulation goroutine.
	emuFrame uint64

	// watches and consoleOut are console watch state, owned by the
	// emulation goroutine. consoleOut is the attached client's output
	// channel (nil when no client is connected), handed over and
	// cleared through the command queue (attach/bye).
	watches    []watchEntry
	consoleOut chan string

	// search and searchWidth are the console memory-search state, owned
	// by the emulation goroutine. searchWidth zero means the default
	// (8-bit, see currentWidth).
	search      *search
	searchWidth int

	// snapshots holds the console's in-memory machine states by slot
	// name. Owned by the emulation goroutine; session-scoped, never
	// written to disk.
	snapshots map[string][]byte

	// player, when non-nil (-replay given), feeds recorded input each
	// frame. Its input is OR'd with live input so the user can still
	// press buttons during playback. replayDone latches the one-time
	// "playback complete" message. Both are owned by the emulation
	// goroutine.
	player     *replay.Player
	replayDone bool
}

func (g *game) Update() error {
	g.pollInputToShared()

	if inpututil.IsKeyJustPressed(ebiten.KeyF11) {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyDigit0) {
		g.paused.Store(!g.paused.Load())
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyDigit9) {
		g.histReq.Store(true)
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyDigit8) {
		g.dumpReq.Store(true)
	}

	// Space bar marks a screenshot point, only while recording.
	if g.recorder != nil && inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.screenshotReq.Store(true)
	}

	return nil
}

func (g *game) Layout(outsideWidth, outsideHeight int) (int, int) {
	m := ebiten.Monitor()
	s := 1.0
	if m != nil {
		s = m.DeviceScaleFactor()
	}
	return int(float64(outsideWidth) * s), int(float64(outsideHeight) * s)
}

func (g *game) close() {
	g.control.stop()

	// Close the audio player before waiting on emuDone so a writer blocked
	// inside ringBuffer.Write wakes up and the emulation goroutine can exit.
	if g.audioPlayer != nil {
		g.audioPlayer.close()
	}

	<-g.emuDone
	g.emu.Close()
}
