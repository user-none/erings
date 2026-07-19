// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Timer-pacing controller gains. The producer runs on an absolute-deadline
// timer at the nominal frame interval; a slow proportional controller nudges
// that interval from the low-passed ring fill so long-term production stays
// locked to the audio device's consumption rate without drift. Gains are
// small and the fill is low-passed so the controller tracks only the long-term
// rate and ignores oto's per-burst fill oscillation.
const (
	pacingFillAlpha = 0.05 // low-pass coefficient on ring fill (~20-frame smoothing)
	pacingGain      = 0.05 // proportional gain on normalized fill error
	pacingMaxAdjust = 0.02 // clamp the interval to +/-2% of nominal
)

const (
	stageIdle int32 = iota
	stagePacing
	stageRunFrame
	stageQueueAudio
	stageUpdateFB
)

func stageName(s int32) string {
	switch s {
	case stageIdle:
		return "idle (between iterations)"
	case stagePacing:
		return "pacing sleep"
	case stageRunFrame:
		return "RunFrame"
	case stageQueueAudio:
		return "queueSamples"
	case stageUpdateFB:
		return "sharedFB.update"
	}
	return "?"
}

func (g *game) emulationLoop() {
	defer close(g.emuDone)

	frameCount := 0
	fpsStart := time.Now()
	swapCountStart := uint64(0)

	// Per-window RunFrame compute-time stats (wall time of RunFrame only,
	// excludes the audio-pacing wait).
	frameTimeMin := time.Duration(math.MaxInt64)
	frameTimeMax := time.Duration(0)
	frameTimeSum := time.Duration(0)
	frameTimeCount := 0

	// Timer pacing. Frames run on an absolute-deadline schedule at the
	// nominal frame interval; a slow proportional controller nudges the
	// interval from ring fill so long-term production locks to the audio
	// device's consumption rate.
	bytesPerFrame := int(math.Round(float64(audioSampleRate) * 4 / float64(g.fps)))
	baseInterval := float64(time.Second) / float64(g.fps)
	frameInterval := baseInterval
	pacingTarget := float64(ringBufferFrames*bytesPerFrame) / 2
	smoothFill := pacingTarget
	nextDeadline := time.Now()

	for {
		if !g.control.shouldRun() {
			return
		}

		g.stage.Store(stageIdle)
		g.frameTick.Add(1)

		// Pace to the next absolute deadline. Absolute deadlines (not
		// Sleep(interval)) keep per-sleep jitter from accumulating into rate
		// drift: N frames span exactly N intervals regardless of individual
		// sleep overshoot. Shutdown is handled by the shouldRun check above -
		// the sleep is bounded by one interval, so a stopped loop exits
		// within a frame.
		g.stage.Store(stagePacing)
		nextDeadline = nextDeadline.Add(time.Duration(frameInterval))
		if d := time.Until(nextDeadline); d > 0 {
			time.Sleep(d)
		} else if d < -time.Duration(baseInterval) {
			// Fell more than a full frame behind (e.g. a long RunFrame).
			// Resync so we don't spiral trying to catch up.
			nextDeadline = time.Now()
		}

		buttons := g.sharedInput.read()
		for player := 0; player < maxPlayers; player++ {
			g.emu.SetInput(player, buttons[player])
		}

		// A pending console frame step runs frames while the pause flag
		// stays set.
		paused := g.paused.Load()
		if paused && g.console != nil && g.console.TakeStep() {
			paused = false
		}

		if paused {
			g.stage.Store(stageQueueAudio)
			g.audioPlayer.queueSamples(nil)
		} else {
			// Mix recorded replay input with the live input read above
			// (bitwise OR), then re-apply, so the user can still press
			// buttons during playback. Advancing the player here, in the
			// non-paused branch, keeps it aligned with the RunFrame count
			// the file was recorded against.
			if g.player != nil {
				if rp1, rp2, active := g.player.Next(); active {
					buttons[0] |= rp1
					buttons[1] |= rp2
					g.emu.SetInput(0, buttons[0])
					g.emu.SetInput(1, buttons[1])
				} else if !g.replayDone {
					g.replayDone = true
					fmt.Printf("[REPLAY] playback complete (%d frames)\n", g.player.Frames())
				}
			}

			g.stage.Store(stageRunFrame)
			runStart := time.Now()
			g.emu.RunFrame()
			runDur := time.Since(runStart)
			g.emuFrame++

			// Record the input applied to this frame. buttons is the
			// snapshot read above and fed to SetInput, so it is exactly
			// this frame's input. Recording only here (non-paused) keeps
			// the recorded timeline a pure RunFrame count.
			if g.recorder != nil {
				g.recorder.RecordFrame(buttons[0], buttons[1])
			}
			if runDur < frameTimeMin {
				frameTimeMin = runDur
			}
			if runDur > frameTimeMax {
				frameTimeMax = runDur
			}
			frameTimeSum += runDur
			frameTimeCount++

			g.stage.Store(stageQueueAudio)
			g.audioPlayer.queueSamples(g.emu.GetAudioSamples())
		}

		g.stage.Store(stageUpdateFB)
		g.sharedFB.update(
			g.emu.GetFramebuffer(),
			g.emu.GetFramebufferStride(),
			g.emu.GetActiveHeight(),
		)

		// Slow proportional pacing feedback. Low-pass the ring fill, then
		// nudge the frame interval from the normalized fill error: a fuller-
		// than-target ring means we are producing faster than the device
		// consumes, so lengthen the interval (and vice versa). The low-pass
		// plus small clamped gain make the controller track only the long-term
		// rate and ignore oto's per-burst fill swing.
		if g.audioPlayer != nil {
			fill := float64(g.audioPlayer.buffered())
			smoothFill += (fill - smoothFill) * pacingFillAlpha
			adjust := pacingGain * (smoothFill - pacingTarget) / pacingTarget
			if adjust > pacingMaxAdjust {
				adjust = pacingMaxAdjust
			} else if adjust < -pacingMaxAdjust {
				adjust = -pacingMaxAdjust
			}
			frameInterval = baseInterval * (1 + adjust)
		}

		g.serviceRequests()

		frameCount++
		if frameCount%g.fps == 0 {
			fpsElapsed := time.Since(fpsStart)
			fps := float64(g.fps) / fpsElapsed.Seconds()
			// Game fps = how many times VDP1 actually swapped its
			// framebuffer over the same window. Games that run their
			// internal logic at half rate (or that fail to advance
			// because of a bug) will show a lower game fps even when
			// the emulator fps is full speed.
			swapCountNow := g.emu.VDP1SwapCount()
			gameFPS := float64(swapCountNow-swapCountStart) / fpsElapsed.Seconds()

			// RunFrame compute time over the window, in milliseconds.
			var fmin, fmax, favg float64
			if frameTimeCount > 0 {
				fmin = float64(frameTimeMin.Microseconds()) / 1000.0
				fmax = float64(frameTimeMax.Microseconds()) / 1000.0
				favg = float64(frameTimeSum.Microseconds()) / 1000.0 / float64(frameTimeCount)
			}
			fmt.Printf("frame %d  fps %.2f  game_fps %.2f | fmin %.3f, fmax %.3f, favg %.3f ms\n",
				frameCount, fps, gameFPS, fmin, fmax, favg)

			fpsStart = time.Now()
			swapCountStart = swapCountNow
			frameTimeMin = time.Duration(math.MaxInt64)
			frameTimeMax = 0
			frameTimeSum = 0
			frameTimeCount = 0
		}
	}
}

// serviceRequests consumes the request flags set on other goroutines. It
// runs between frames on the emulation goroutine, so every handler sees
// stable core state. All between-frames work is added here, not inline in
// emulationLoop.
func (g *game) serviceRequests() {
	g.serviceHistRequest()
	g.serviceScreenshotRequest()
	g.serviceDumpRequest()
	if g.console != nil {
		g.console.Service(g.emuFrame)
	}
}

type emuControl struct {
	mu      sync.Mutex
	running bool
	stopReq bool
}

func newEmuControl() *emuControl {
	return &emuControl{running: true}
}

func (ec *emuControl) shouldRun() bool {
	ec.mu.Lock()
	r := ec.running && !ec.stopReq
	ec.mu.Unlock()
	return r
}

func (ec *emuControl) stop() {
	ec.mu.Lock()
	ec.running = false
	ec.stopReq = true
	ec.mu.Unlock()
}
