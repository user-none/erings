// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/user-none/eblitui/romloader"
	"github.com/user-none/erings/core"
	"github.com/user-none/erings/internal/replay"
)

// Maximum framebuffer dimensions for buffer allocation.
const (
	maxFBWidth  = 704
	maxFBHeight = 512
)

// Audio constants for Saturn SCSP output.
const (
	audioSampleRate = 44100
	maxPlayers      = 2

	// ringBufferFrames is the audio ring depth in frames. The byte size is
	// derived at runtime from the core's frame rate (see newAudioPlayer).
	// Under timer-paced production the producer no longer blocks on a full
	// ring in steady state; the ring must be deep enough to absorb oto's
	// bursty multi-frame reads (~2 frames pulled every ~33ms, with jitter)
	// without hitting the block-on-full path, which would re-couple it to
	// oto's coarse read cadence.
	ringBufferFrames = 6

	// otoPlayerBufferBytes sizes the oto mux player buffer (~50ms at 44.1kHz
	// stereo int16) via player.SetBufferSize.
	otoPlayerBufferBytes = 19200
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

// Saturn button bit positions (d-pad bits 0-3).
const (
	btnUp    = 0
	btnDown  = 1
	btnLeft  = 2
	btnRight = 3
	btnA     = 4
	btnB     = 5
	btnC     = 6
	btnX     = 7
	btnY     = 8
	btnZ     = 9
	btnL     = 10
	btnR     = 11
	btnStart = 12
)

func main() {
	biosPath := flag.String("bios", "", "Path to Saturn BIOS ROM (512KB). Optional - if omitted, the HLE BIOS boots the disc directly.")
	discPath := flag.String("disc", "", "Path to CHD V5 or cue disc image")
	cpuProfile := flag.String("cpuprofile", "", "Write CPU profile to file")
	dumpDir := flag.String("dump-dir", ".", "Directory to write memory dumps into (created if missing)")
	savePath := flag.String("save", "", "Path to backup-RAM save file. If a directory, uses <gameid>.srm inside it. Loaded on start (if it exists) and written on close.")
	fastBoot := flag.Bool("fast-boot", false, "Skip the real BIOS boot animation and enter the disc IP directly (real BIOS only; no effect with the HLE BIOS).")
	record := flag.String("record", "", "Record a replay file (JSON) of per-frame input and screenshot markers.")
	replayPath := flag.String("replay", "", "Replay a recorded input file (JSON). Recorded input is mixed with live input so you can still press buttons.")
	flag.Parse()

	if *record != "" && *replayPath != "" {
		log.Fatal("-record and -replay are mutually exclusive")
	}

	var cpuProfileFile *os.File
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatalf("failed to create CPU profile: %v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("failed to start CPU profile: %v", err)
		}
		cpuProfileFile = f
	}

	if *biosPath == "" && *discPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: saturn -bios <path> [-disc <path>]  OR  saturn -disc <path>  (HLE BIOS)")
		os.Exit(1)
	}

	emu := core.NewEmulator()

	if *biosPath != "" {
		biosData, err := os.ReadFile(*biosPath)
		if err != nil {
			log.Fatalf("failed to read BIOS: %v", err)
		}
		if err := emu.SetBIOS("main_bios", biosData); err != nil {
			log.Fatalf("failed to set BIOS: %v", err)
		}
	}

	var disc *romloader.Disc
	var err error
	if *discPath != "" {
		disc, err = romloader.OpenDisc(*discPath)
		if err != nil {
			log.Fatalf("failed to open disc: %v", err)
		}
		printDiscInfo(disc)
		emu.SetDisc(disc)
	}

	resolvedSavePath := resolveSavePath(*savePath, disc)
	if resolvedSavePath != "" {
		loadSaveFile(emu, resolvedSavePath)
	}

	var recorder *replay.Recorder
	if *record != "" {
		recorder = replay.NewRecorder()
		fmt.Printf("[REPLAY] recording to %s\n", *record)
	}

	var player *replay.Player
	if *replayPath != "" {
		rf, err := replay.Load(*replayPath)
		if err != nil {
			log.Fatalf("failed to load replay file %q: %v", *replayPath, err)
		}
		if rf.Version != replay.Version {
			log.Printf("Warning: replay file version %d, tool expects %d", rf.Version, replay.Version)
		}
		if id := readGameID(disc); rf.DiscID != "" && id != "" && rf.DiscID != id {
			log.Printf("Warning: replay disc %q does not match loaded disc %q", rf.DiscID, id)
		}
		player = replay.NewPlayer(rf)
		fmt.Printf("[REPLAY] replaying %s (%d frames, %d screenshots)\n", *replayPath, rf.Frames, len(rf.Screenshots))
	}

	// SIGINT/SIGTERM handler. Flushes the save file (if -save was
	// given), the replay file (if -record was given), and stops the CPU
	// profile (if -cpuprofile was given) before exiting. Without this,
	// CTRL-C would skip all three — the normal close-path only runs when
	// ebiten exits cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if resolvedSavePath != "" {
			writeSaveFile(emu, resolvedSavePath)
		}
		if recorder != nil {
			if err := recorder.Write(*record, readGameID(disc)); err != nil {
				log.Printf("Warning: failed to write replay file %q: %v", *record, err)
			}
		}
		if cpuProfileFile != nil {
			pprof.StopCPUProfile()
			cpuProfileFile.Close()
		}
		os.Exit(0)
	}()

	if *fastBoot {
		emu.SetOption("fast_boot", "true")
	}

	if err := emu.Start(); err != nil {
		log.Fatalf("emulator start failed: %v", err)
	}

	ebiten.SetWindowTitle("Saturn")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetTPS(60)

	ebiten.SetWindowSize(800, 600)
	ebiten.SetWindowSizeLimits(400, 300, -1, -1)

	// Frame rate comes from the core's region (60 NTSC / 50 PAL), read once
	// after Start so the audio sizing and pacing match the loaded game.
	fps := emu.GetTiming().FPS

	audioPlayer, err := newAudioPlayer(fps)
	if err != nil {
		log.Printf("Warning: audio initialization failed: %v", err)
	}

	g := &game{
		emu:         emu,
		audioPlayer: audioPlayer,
		fps:         fps,
		sharedInput: &sharedInput{},
		sharedFB:    newSharedFramebuffer(maxFBWidth, maxFBHeight),
		control:     newEmuControl(),
		emuDone:     make(chan struct{}),
		keyMap:      buildKeyMap(),
		padMap:      buildPadMap(),
		dumpDir:     *dumpDir,
		recorder:    recorder,
		player:      player,
	}

	g.startWatchdog()
	go g.emulationLoop()

	runErr := ebiten.RunGame(g)

	// Always run the close path on any clean exit (Cmd+Q, window-X,
	// ebiten.Termination). On macOS Cmd+Q goes through AppKit and
	// does NOT raise a Unix signal, so the SIGINT goroutine never
	// fires for those paths — the save flush + pprof flush + disc
	// release have to happen here.
	g.close()
	if resolvedSavePath != "" {
		writeSaveFile(emu, resolvedSavePath)
	}
	if recorder != nil {
		if err := recorder.Write(*record, readGameID(disc)); err != nil {
			log.Printf("Warning: failed to write replay file %q: %v", *record, err)
		}
	}
	if cpuProfileFile != nil {
		pprof.StopCPUProfile()
		cpuProfileFile.Close()
	}
	if disc != nil {
		disc.Close()
	}

	// ebiten.Termination is the sentinel ebiten returns for a normal
	// window close — not a fatal error. Only escalate other errors.
	if runErr != nil && !errors.Is(runErr, ebiten.Termination) {
		log.Fatal(runErr)
	}
}

// resolveSavePath turns the user's -save argument into the actual file
// path the emulator should read from on start and write to on close.
// If the argument is empty, returns "" (save handling disabled). If
// the argument names an existing directory, the file inside it is
// <gameid>.srm — gameid extracted from the disc's IP header at user-
// offset $20 (10 ASCII bytes per Disc Format Standards ST-040). If
// the argument names anything else (an existing file or a path that
// doesn't yet exist), it is used verbatim.
func resolveSavePath(arg string, disc *romloader.Disc) string {
	if arg == "" {
		return ""
	}
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		id := readGameID(disc)
		if id == "" {
			log.Fatalf("-save points at a directory but the disc has no readable game ID")
		}
		return filepath.Join(arg, id+".srm")
	}
	return arg
}

// readGameID reads the 10-byte product number from the disc's IP
// header (offset $20 in the IP user data) and returns it with
// trailing spaces trimmed. Returns "" if the disc is nil, unreadable,
// or doesn't have a Saturn IP header.
func readGameID(disc *romloader.Disc) string {
	if disc == nil {
		return ""
	}
	data, err := disc.ReadSector(0)
	if err != nil || len(data) < 16+0x2A {
		return ""
	}
	user := data[16:]
	if string(user[0:16]) != "SEGA SEGASATURN " {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(user[0x20:0x2A]), " "))
}

// loadSaveFile attempts to read the file at path and feed it to the
// emulator via SetSRAM. A missing file is fine — the path is still
// remembered so writeSaveFile can create it on close. Any other read
// error or unexpected file size is logged and skipped (without
// SetSRAM the emulator starts with a freshly-formatted backup).
func loadSaveFile(emu *core.Emulator, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("Warning: failed to read save file %q: %v", path, err)
		return
	}
	emu.SetSRAM(data)
}

// writeSaveFile writes the emulator's current backup RAM to the
// given path. Logs and continues on error so close-time failures
// don't mask other shutdown work.
func writeSaveFile(emu *core.Emulator, path string) {
	if err := os.WriteFile(path, emu.GetSRAM(), 0644); err != nil {
		log.Printf("Warning: failed to write save file %q: %v", path, err)
	}
}

// printDiscInfo reads the IP System ID from sector 0 and prints the
// game title, product number, and device-information field in
// "<NAME> | <ID> | <DEVICE>" form. Per Disc Format Standards (ST-040)
// section 4.2, the IP header lives in the first 2048 bytes of user data:
// hardware identifier at 0x00, product number at 0x20 (10 bytes), device
// information at 0x38 (8 bytes, "CD-n/m"), game title at 0x60 (112
// bytes). Empty positions are filled with ASCII spaces. Raw 2352-byte
// sectors place user data at byte 16. Silent on read failure or
// non-Saturn discs.
func printDiscInfo(disc *romloader.Disc) {
	data, err := disc.ReadSector(0)
	if err != nil || len(data) < 16+0xD0 {
		return
	}
	user := data[16:]
	if string(user[0:16]) != "SEGA SEGASATURN " {
		return
	}
	id := strings.TrimRight(string(user[0x20:0x2A]), " ")
	name := strings.TrimRight(string(user[0x60:0xD0]), " ")
	device := strings.TrimSpace(string(user[0x38:0x40]))
	fmt.Printf("%s | %s | %s\n", name, id, device)
}

// ---------------------------------------------------------------------------
// game implements ebiten.Game
// ---------------------------------------------------------------------------

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
	// one frame of silence to keep audio-driven pacing alive.
	paused atomic.Bool

	// frameTick advances on every emulationLoop iteration. The watchdog
	// goroutine reads it to detect a stalled emulation loop and dump
	// goroutine stacks so we can see exactly what is blocked.
	frameTick atomic.Uint64
	// stage tags the most recent point reached inside one iteration of
	// emulationLoop, so the watchdog can report which stage hung when
	// frameTick stops advancing. Updated with atomic store of a small
	// integer enum (stageStart / stageRunFrame / stageQueueAudio /
	// stageUpdateFB).
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
	// reads the memory regions and writes per-region binary files to a
	// timestamped subdirectory under dumpDir.
	dumpReq atomic.Bool
	dumpDir string

	// recorder, when non-nil (-record given), captures per-frame input
	// and screenshot markers. It is written to disk on shutdown.
	// screenshotReq is set from Update (ebiten goroutine) on the space bar
	// and consumed in emulationLoop between frames to append a marker.
	recorder      *replay.Recorder
	screenshotReq atomic.Bool

	// player, when non-nil (-replay given), feeds recorded input each
	// frame. Its input is OR'd with live input so the user can still
	// press buttons during playback. replayDone latches the one-time
	// "playback complete" message. Both are owned by the emulation
	// goroutine.
	player     *replay.Player
	replayDone bool
}

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

// startWatchdog spawns a goroutine that polls frameTick. If the
// counter stops advancing for stallThreshold, it prints which stage
// the loop was last in and dumps every goroutine's stack to stderr,
// then resets so it can fire again if the lockup intermittently
// recovers.
func (g *game) startWatchdog() {
	const (
		pollInterval   = 1 * time.Second
		stallThreshold = 5 * time.Second
	)
	go func() {
		var lastTick uint64
		var lastChange = time.Now()
		var fired bool
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for range ticker.C {
			cur := g.frameTick.Load()
			if cur != lastTick {
				lastTick = cur
				lastChange = time.Now()
				fired = false
				continue
			}
			if fired || time.Since(lastChange) < stallThreshold {
				continue
			}
			fired = true
			st := g.stage.Load()
			fmt.Fprintf(os.Stderr,
				"\n[WATCHDOG] emulation loop stalled for %v at stage=%s, frameTick=%d\n",
				time.Since(lastChange).Round(time.Millisecond), stageName(st), cur)
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			os.Stderr.Write(buf[:n])
			fmt.Fprintln(os.Stderr, "[WATCHDOG] end of stack dump")
		}
	}()
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

	// Path 2 timer pacing. Frames run on an absolute-deadline schedule at the
	// nominal frame interval; a slow proportional controller nudges the
	// interval from ring fill so long-term production locks to the audio
	// device's consumption rate. This replaces the demand-token gate, which
	// slaved the producer to oto's coarse, Go-scheduled ~33ms reads and made
	// frames complete in bursts.
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

		if g.paused.Load() {
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

		// SH-2 PC histogram arm/dump. Toggled here, between frames, so
		// SetSH2Trace never mutates TraceFunc while a core is executing.
		// The maps are written by the trace closures (called inside
		// RunFrame on this same goroutine) and read here - no other
		// goroutine touches them.
		if g.histReq.Swap(false) {
			if !g.histActive {
				g.histMaster = make(map[uint32]uint64, 4096)
				g.histSlave = make(map[uint32]uint64, 4096)
				// op == 0xFFFF is the interrupt-accept synthetic call,
				// not an executed instruction - exclude it so this is a
				// pure instruction histogram.
				counter := func(m map[uint32]uint64) func(uint32, uint16) {
					return func(pc uint32, op uint16) {
						if op != 0xFFFF {
							m[pc]++
						}
					}
				}
				g.emu.SetSH2Trace(counter(g.histMaster), counter(g.histSlave))
				g.histActive = true
				fmt.Println("[HIST] armed - capturing SH-2 master/slave PCs")
			} else {
				g.emu.SetSH2Trace(nil, nil)
				g.histActive = false
				printHistogram("master", g.histMaster)
				printHistogram("slave", g.histSlave)
				g.histMaster, g.histSlave = nil, nil
			}
		}

		// Screenshot marker request. Consumed between frames so it marks
		// the frame just completed (recorder.frame has advanced past it).
		if g.screenshotReq.Swap(false) && g.recorder != nil {
			f := g.recorder.MarkScreenshot()
			fmt.Printf("[REPLAY] screenshot marked at frame %d\n", f)
		}

		// Memory dump request. Snapshot is taken between frames so the
		// region contents are stable while writeMemoryDump runs.
		if g.dumpReq.Swap(false) {
			dump := g.emu.DumpMemory()
			if err := writeMemoryDump(dump, g.dumpDir); err != nil {
				fmt.Fprintf(os.Stderr, "[DUMP] failed: %v\n", err)
			}
		}

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

// printHistogram prints the 20 most-executed PCs from a captured SH-2
// trace to stdout, sorted by sample count. The header reports total
// samples and the unique-PC count - a small unique count concentrated
// in a few PCs indicates a tight spin/poll loop, a large one indicates
// the core is making normal progress.
func printHistogram(label string, h map[uint32]uint64) {
	type row struct {
		pc  uint32
		cnt uint64
	}
	rows := make([]row, 0, len(h))
	var total uint64
	for pc, c := range h {
		rows = append(rows, row{pc, c})
		total += c
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].cnt > rows[j].cnt })

	fmt.Printf("[HIST] SH-2 %s  total=%d  uniquePCs=%d  (top 20)\n",
		label, total, len(rows))
	n := 20
	if len(rows) < n {
		n = len(rows)
	}
	for i := 0; i < n; i++ {
		pct := 0.0
		if total > 0 {
			pct = float64(rows[i].cnt) * 100 / float64(total)
		}
		fmt.Printf("  0x%08X  %12d  %6.2f%%\n", rows[i].pc, rows[i].cnt, pct)
	}
}

// writeMemoryDump writes every region of dump to its own .bin file in
// a timestamped subdirectory of baseDir. Millisecond resolution in the
// directory name lets multiple dumps within the same second coexist
// (one Saturn frame is ~16.7 ms at 60 fps).
func writeMemoryDump(dump core.MemoryDump, baseDir string) error {
	stamp := strings.Replace(time.Now().Format("20060102-150405.000"), ".", "-", 1)
	dir := filepath.Join(baseDir, "dump-"+stamp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	regions := []struct {
		name string
		data []byte
	}{
		{"bios.bin", dump.BIOS},
		{"wramh.bin", dump.WRAMH},
		{"wraml.bin", dump.WRAML},
		{"backup.bin", dump.BackupRAM},
		{"extram.bin", dump.ExtRAM},
		{"vdp1_vram.bin", dump.VDP1VRAM},
		{"vdp1_draw_fb.bin", dump.VDP1DrawFB},
		{"vdp1_display_fb.bin", dump.VDP1DisplayFB},
		{"vdp2_vram.bin", dump.VDP2VRAM},
		{"vdp2_cram.bin", dump.VDP2CRAM},
		{"vdp2_regs.bin", dump.VDP2Regs},
		{"vdp1_regs.bin", dump.VDP1Regs},
		{"vdp1_shadow.bin", dump.VDP1Shadow},
		{"sound_ram.bin", dump.SoundRAM},
		{"scu_regs.bin", dump.SCURegs},
		{"scu_internal.bin", dump.SCUInternal},
		{"scu_dsp.bin", dump.SCUDSP},
		{"scsp_regs.bin", dump.SCSPRegs},
		{"scsp_slots.bin", dump.SCSPSlots},
		{"scsp_dsp.bin", dump.SCSPDSP},
		{"scsp_timers.bin", dump.SCSPTimers},
		{"sh2_master_regs.bin", dump.SH2MasterRegs},
		{"sh2_slave_regs.bin", dump.SH2SlaveRegs},
		{"sh2_master_irq.bin", dump.SH2MasterIRQ},
		{"sh2_slave_irq.bin", dump.SH2SlaveIRQ},
		{"smpc.bin", dump.SMPCState},
		{"coordination.bin", dump.Coordination},
		{"m68k_state.bin", dump.M68KState},
	}
	for _, r := range regions {
		if err := os.WriteFile(filepath.Join(dir, r.name), r.data, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("[DUMP] wrote %d regions to %s\n", len(regions), dir)
	return nil
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

func (g *game) Draw(screen *ebiten.Image) {
	pixels, stride, activeHeight := g.sharedFB.read()
	if activeHeight == 0 || stride == 0 {
		return
	}

	requiredLen := stride * activeHeight
	if len(pixels) < requiredLen {
		return
	}

	pixelWidth := stride / 4
	if g.offscreen == nil || g.offscreen.Bounds().Dx() != pixelWidth || g.offscreen.Bounds().Dy() != activeHeight {
		g.offscreen = ebiten.NewImage(pixelWidth, activeHeight)
	}
	g.offscreen.WritePixels(pixels[:requiredLen])

	screenW, screenH := screen.Bounds().Dx(), screen.Bounds().Dy()
	ratio := 4.0 / 3.0
	displayW := float64(screenW)
	displayH := displayW / ratio
	if displayH > float64(screenH) {
		displayH = float64(screenH)
		displayW = displayH * ratio
	}

	scaleX := displayW / float64(pixelWidth)
	scaleY := displayH / float64(activeHeight)
	scaledW := float64(pixelWidth) * scaleX
	scaledH := float64(activeHeight) * scaleY
	offsetX := (float64(screenW) - scaledW) / 2
	offsetY := (float64(screenH) - scaledH) / 2

	opts := ebiten.DrawImageOptions{}
	opts.GeoM.Scale(scaleX, scaleY)
	opts.GeoM.Translate(offsetX, offsetY)
	opts.Filter = ebiten.FilterNearest
	screen.DrawImage(g.offscreen, &opts)
}

func (g *game) Layout(outsideWidth, outsideHeight int) (int, int) {
	m := ebiten.Monitor()
	s := 1.0
	if m != nil {
		s = m.DeviceScaleFactor()
	}
	return int(float64(outsideWidth) * s), int(float64(outsideHeight) * s)
}

func (g *game) pollInputToShared() {
	gamepadIDs := ebiten.AppendGamepadIDs(nil)
	hasGamepad := len(gamepadIDs) > 0

	var gamepadID ebiten.GamepadID
	if hasGamepad {
		gamepadID = gamepadIDs[0]
	}

	var buttons uint32
	for bitID, key := range g.keyMap {
		if ebiten.IsKeyPressed(key) {
			buttons |= 1 << uint(bitID)
		}
	}
	if hasGamepad {
		for bitID, padBtn := range g.padMap {
			if ebiten.IsStandardGamepadButtonPressed(gamepadID, padBtn) {
				buttons |= 1 << uint(bitID)
			}
		}
		pollAnalogStick(&buttons, g.padMap, gamepadID)
	}
	g.sharedInput.set(0, buttons)

	if len(gamepadIDs) > 1 {
		var p2buttons uint32
		for bitID, padBtn := range g.padMap {
			if ebiten.IsStandardGamepadButtonPressed(gamepadIDs[1], padBtn) {
				p2buttons |= 1 << uint(bitID)
			}
		}
		pollAnalogStick(&p2buttons, g.padMap, gamepadIDs[1])
		g.sharedInput.set(1, p2buttons)
	}
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

// ---------------------------------------------------------------------------
// Input mapping
// ---------------------------------------------------------------------------

func buildKeyMap() map[int]ebiten.Key {
	return map[int]ebiten.Key{
		btnUp:    ebiten.KeyW,
		btnDown:  ebiten.KeyS,
		btnLeft:  ebiten.KeyA,
		btnRight: ebiten.KeyD,
		btnA:     ebiten.KeyJ,
		btnB:     ebiten.KeyK,
		btnC:     ebiten.KeyL,
		btnX:     ebiten.KeyU,
		btnY:     ebiten.KeyI,
		btnZ:     ebiten.KeyO,
		btnL:     ebiten.KeyN,
		btnR:     ebiten.KeyM,
		btnStart: ebiten.KeyEnter,
	}
}

func buildPadMap() map[int]ebiten.StandardGamepadButton {
	return map[int]ebiten.StandardGamepadButton{
		btnUp:    ebiten.StandardGamepadButtonLeftTop,
		btnDown:  ebiten.StandardGamepadButtonLeftBottom,
		btnLeft:  ebiten.StandardGamepadButtonLeftLeft,
		btnRight: ebiten.StandardGamepadButtonLeftRight,
		btnA:     ebiten.StandardGamepadButtonRightBottom,
		btnB:     ebiten.StandardGamepadButtonRightRight,
		btnC:     ebiten.StandardGamepadButtonFrontTopRight,
		btnX:     ebiten.StandardGamepadButtonRightLeft,
		btnY:     ebiten.StandardGamepadButtonRightTop,
		btnZ:     ebiten.StandardGamepadButtonFrontTopLeft,
		btnL:     ebiten.StandardGamepadButtonFrontBottomLeft,
		btnR:     ebiten.StandardGamepadButtonFrontBottomRight,
		btnStart: ebiten.StandardGamepadButtonCenterRight,
	}
}

func pollAnalogStick(buttons *uint32, padMap map[int]ebiten.StandardGamepadButton, gamepadID ebiten.GamepadID) {
	axisX := ebiten.StandardGamepadAxisValue(gamepadID, ebiten.StandardGamepadAxisLeftStickHorizontal)
	axisY := ebiten.StandardGamepadAxisValue(gamepadID, ebiten.StandardGamepadAxisLeftStickVertical)

	for bitID, padBtn := range padMap {
		switch padBtn {
		case ebiten.StandardGamepadButtonLeftLeft:
			if axisX < -0.25 {
				*buttons |= 1 << uint(bitID)
			}
		case ebiten.StandardGamepadButtonLeftRight:
			if axisX > 0.25 {
				*buttons |= 1 << uint(bitID)
			}
		case ebiten.StandardGamepadButtonLeftTop:
			if axisY < -0.25 {
				*buttons |= 1 << uint(bitID)
			}
		case ebiten.StandardGamepadButtonLeftBottom:
			if axisY > 0.25 {
				*buttons |= 1 << uint(bitID)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Shared state types
// ---------------------------------------------------------------------------

type sharedInput struct {
	mu      sync.Mutex
	buttons [maxPlayers]uint32
}

func (si *sharedInput) set(player int, buttons uint32) {
	if player < 0 || player >= maxPlayers {
		return
	}
	si.mu.Lock()
	si.buttons[player] = buttons
	si.mu.Unlock()
}

func (si *sharedInput) read() [maxPlayers]uint32 {
	si.mu.Lock()
	result := si.buttons
	si.mu.Unlock()
	return result
}

type sharedFramebuffer struct {
	mu           sync.Mutex
	writePixels  []byte
	readPixels   []byte
	stride       int
	activeHeight int
}

func newSharedFramebuffer(width, height int) *sharedFramebuffer {
	size := width * height * 4
	return &sharedFramebuffer{
		writePixels: make([]byte, size),
		readPixels:  make([]byte, size),
	}
}

func (sf *sharedFramebuffer) update(pixels []byte, stride, activeHeight int) {
	sf.mu.Lock()
	n := stride * activeHeight
	if n > len(sf.writePixels) {
		n = len(sf.writePixels)
	}
	if n > len(pixels) {
		n = len(pixels)
	}
	copy(sf.writePixels[:n], pixels[:n])
	sf.stride = stride
	sf.activeHeight = activeHeight
	sf.mu.Unlock()
}

func (sf *sharedFramebuffer) read() (pixels []byte, stride, activeHeight int) {
	sf.mu.Lock()
	stride = sf.stride
	activeHeight = sf.activeHeight
	n := stride * activeHeight
	if n > len(sf.writePixels) {
		n = len(sf.writePixels)
	}
	if n > 0 {
		copy(sf.readPixels[:n], sf.writePixels[:n])
	}
	pixels = sf.readPixels
	sf.mu.Unlock()
	return
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

// ---------------------------------------------------------------------------
// Audio ring buffer
// ---------------------------------------------------------------------------

type audioRingBuffer struct {
	buf      []byte
	readPos  int
	writePos int
	count    int
	capacity int
	mu       sync.Mutex
	cond     *sync.Cond
	closed   bool
}

func newAudioRingBuffer(capacity int) *audioRingBuffer {
	rb := &audioRingBuffer{
		buf:      make([]byte, capacity),
		capacity: capacity,
	}
	rb.cond = sync.NewCond(&rb.mu)
	return rb
}

func (rb *audioRingBuffer) Write(p []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.closed {
		return
	}

	n := len(p)
	if n == 0 {
		return
	}

	if n > rb.capacity {
		p = p[n-rb.capacity:]
		n = rb.capacity
	}

	written := 0
	for written < n {
		for !rb.closed && rb.count == rb.capacity {
			rb.cond.Wait()
		}
		if rb.closed {
			return
		}

		// Only signal the reader on the empty->non-empty transition.
		// Signaling when the reader isn't parked still costs a syscall via
		// runtime.pthread_cond_signal, which shows up as scheduling overhead.
		wasEmpty := rb.count == 0

		avail := rb.capacity - rb.count
		chunk := n - written
		if chunk > avail {
			chunk = avail
		}

		firstChunk := rb.capacity - rb.writePos
		if firstChunk >= chunk {
			copy(rb.buf[rb.writePos:], p[written:written+chunk])
		} else {
			copy(rb.buf[rb.writePos:], p[written:written+firstChunk])
			copy(rb.buf[0:], p[written+firstChunk:written+chunk])
		}
		rb.writePos = (rb.writePos + chunk) % rb.capacity
		rb.count += chunk
		written += chunk

		if wasEmpty {
			rb.cond.Signal()
		}
	}
}

func (rb *audioRingBuffer) Read(p []byte) (int, error) {
	rb.mu.Lock()

	for rb.count == 0 {
		if rb.closed {
			rb.mu.Unlock()
			return 0, io.EOF
		}
		rb.cond.Wait()
	}

	// Only signal the writer on the full->non-full transition; avoids a
	// syscall every Read when the writer is not parked.
	wasFull := rb.count == rb.capacity

	n := len(p)
	if n > rb.count {
		n = rb.count
	}

	firstChunk := rb.capacity - rb.readPos
	if firstChunk >= n {
		copy(p, rb.buf[rb.readPos:rb.readPos+n])
	} else {
		copy(p, rb.buf[rb.readPos:])
		copy(p[firstChunk:], rb.buf[:n-firstChunk])
	}
	rb.readPos = (rb.readPos + n) % rb.capacity
	rb.count -= n

	if wasFull {
		rb.cond.Signal()
	}

	rb.mu.Unlock()

	return n, nil
}

func (rb *audioRingBuffer) Buffered() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.count
}

func (rb *audioRingBuffer) Close() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.closed = true
	rb.cond.Broadcast()
}

// ---------------------------------------------------------------------------
// Audio player
// ---------------------------------------------------------------------------

var (
	otoCtx      *oto.Context
	otoInitOnce sync.Once
	otoInitErr  error
)

func ensureOtoContext() (*oto.Context, error) {
	otoInitOnce.Do(func() {
		op := &oto.NewContextOptions{
			SampleRate:   audioSampleRate,
			ChannelCount: 2,
			Format:       oto.FormatSignedInt16LE,
			BufferSize:   50 * time.Millisecond,
		}
		var readyChan chan struct{}
		otoCtx, readyChan, otoInitErr = oto.NewContext(op)
		if otoInitErr != nil {
			return
		}
		<-readyChan
	})
	return otoCtx, otoInitErr
}

// audioPlayer wraps oto and a ring buffer. The producer writes one frame of
// samples per emulated frame into the ring; oto drains it. Pacing is NOT done
// here - the emulation loop paces itself on an absolute-deadline timer and
// uses the ring fill (Buffered) only as a slow rate-lock reference. The ring's
// block-on-full path remains as a backpressure safety net.
type audioPlayer struct {
	player     *oto.Player
	ringBuffer *audioRingBuffer
	audioBytes []byte
	// silentFrame is one frame's worth of zero bytes, written to the ring
	// when the core produces empty audio so oto always has samples to drain
	// and does not underrun on empty cold-start frames.
	silentFrame []byte
}

func newAudioPlayer(fps int) (*audioPlayer, error) {
	ctx, err := ensureOtoContext()
	if err != nil {
		return nil, fmt.Errorf("oto audio not available: %w", err)
	}

	bytesPerFrame := int(math.Round(float64(audioSampleRate) * 4 / float64(fps)))
	rb := newAudioRingBuffer(ringBufferFrames * bytesPerFrame)

	ap := &audioPlayer{
		ringBuffer:  rb,
		audioBytes:  make([]byte, 0, 4096),
		silentFrame: make([]byte, bytesPerFrame),
	}

	player := ctx.NewPlayer(rb)
	player.SetBufferSize(otoPlayerBufferBytes)
	player.SetVolume(1.0)
	player.Play()
	ap.player = player

	return ap, nil
}

// buffered returns the current ring fill in bytes. The timer-pacing
// controller reads this as its rate-lock reference.
func (a *audioPlayer) buffered() int {
	return a.ringBuffer.Buffered()
}

func (a *audioPlayer) queueSamples(samples []int16) {
	if len(samples) == 0 {
		a.ringBuffer.Write(a.silentFrame)
		return
	}

	needed := len(samples) * 2
	if cap(a.audioBytes) < needed {
		a.audioBytes = make([]byte, 0, needed)
	}
	a.audioBytes = a.audioBytes[:0]
	for _, sample := range samples {
		a.audioBytes = append(a.audioBytes, byte(sample), byte(sample>>8))
	}

	a.ringBuffer.Write(a.audioBytes)
}

func (a *audioPlayer) close() {
	// Close the ring first so a producer blocked in ring.Write (full ring)
	// wakes and the emulation loop can observe shouldRun()==false and exit.
	// The timer-paced producer otherwise parks only in time.Sleep (bounded by
	// one frame interval), so no demand-side wake is needed.
	if a.ringBuffer != nil {
		a.ringBuffer.Close()
	}
	if a.player != nil {
		a.player.Close()
	}
}
