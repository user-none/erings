// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/user-none/eblitui/romloader"
	"github.com/user-none/erings/core"
	"github.com/user-none/erings/internal/replay"
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
	loadState := flag.String("load-state", "", "Path to a save state file to load at startup. Requires the same disc and BIOS the state was captured with; a replay played on top must have been recorded from this state.")
	consolePort := flag.Int("c", 0, "Debug console TCP port, bound to 127.0.0.1 (0 = disabled). Line protocol; connect with nc or telnet.")
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

	// State load happens after Start so the boot path has run (HLE
	// service hooks wired, workers spawned but parked) and before the
	// first RunFrame, satisfying Deserialize's frame-boundary
	// constraint.
	if *loadState != "" {
		stateData, err := os.ReadFile(*loadState)
		if err != nil {
			log.Fatalf("failed to read save state %q: %v", *loadState, err)
		}
		if err := emu.Deserialize(stateData); err != nil {
			log.Fatalf("failed to load save state %q: %v", *loadState, err)
		}
		fmt.Printf("[STATE] loaded %s\n", *loadState)
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

	if *consolePort != 0 {
		c, err := startConsole(*consolePort)
		if err != nil {
			log.Fatalf("failed to start debug console: %v", err)
		}
		g.console = c
		fmt.Printf("[CONSOLE] listening on 127.0.0.1:%d\n", *consolePort)
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
