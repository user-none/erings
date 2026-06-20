// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command capture replays a recorded session against a disc with no display,
// running as fast as possible, and writes framebuffer screenshots for
// regression comparison. It is headless and unattended: a self-aborting
// watchdog kills the run if the emulation freezes or collapses below a
// throughput floor instead of pinning a CPU.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/user-none/eblitui/romloader"
	"github.com/user-none/erings/core"
	"github.com/user-none/erings/internal/replay"
)

// Watchdog thresholds. The run loop has no pacing, so a healthy headless run
// completes frames far above realtime; fewer than floor completions in a
// one-second window means each frame costs ~100ms of pure compute, which is
// already a broken run with wide margin. freezeGap is measured from the last
// completed frame, so a stalled RunFrame grows it monotonically (frozen)
// while a merely slow run keeps it fresh (slow).
const (
	healthFloor = 10              // minimum completed frames per window
	healthWin   = 1 * time.Second // sampling window / tick
	freezeGap   = 2 * time.Second // no completion for this long => frozen
)

// Exit codes. 0 clean, 1 usage/load (via log.Fatal), 2 watchdog abort.
const exitWatchdog = 2

func main() {
	log.SetFlags(0)

	biosPath := flag.String("bios", "", "Path to Saturn BIOS ROM (512KB). Optional - if omitted, the HLE BIOS boots the disc directly.")
	fastBoot := flag.Bool("fast-boot", false, "Skip the real BIOS boot animation and enter the disc IP directly (real BIOS only; no effect with the HLE BIOS).")
	outDir := flag.String("out", "capture_output", "Output root directory for captured artifacts (created if missing).")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: capture [flags] <disc> <replay>")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(1)
	}
	discPath := flag.Arg(0)
	replayPath := flag.Arg(1)

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

	disc, err := romloader.OpenDisc(discPath)
	if err != nil {
		log.Fatalf("failed to open disc: %v", err)
	}
	emu.SetDisc(disc)

	rf, err := replay.Load(replayPath)
	if err != nil {
		log.Fatalf("failed to load replay file %q: %v", replayPath, err)
	}
	if rf.Version != replay.Version {
		log.Printf("Warning: replay file version %d, tool expects %d", rf.Version, replay.Version)
	}
	rawID := readGameID(disc)
	if rf.DiscID != "" && rawID != "" && rf.DiscID != rawID {
		log.Printf("Warning: replay disc %q does not match loaded disc %q", rf.DiscID, rawID)
	}
	player := replay.NewPlayer(rf)

	if *fastBoot {
		emu.SetOption("fast_boot", "true")
	}

	if err := emu.Start(); err != nil {
		log.Fatalf("emulator start failed: %v", err)
	}

	// The product number comes from an arbitrary disc image, so sanitize it
	// before using it in a filename or printing it: a crafted field could
	// contain path separators (traversal out of the output directory) or
	// terminal control bytes.
	id := sanitizeID(rawID)
	ts := time.Now().Unix()

	shotsDir := filepath.Join(*outDir, "screenshots")
	if err := os.MkdirAll(shotsDir, 0o755); err != nil {
		log.Fatalf("failed to create output directory %q: %v", shotsDir, err)
	}

	fmt.Printf("[CAPTURE] replaying %s (%d frames, %d screenshots), disc=%s -> %s\n",
		replayPath, rf.Frames, len(rf.Screenshots), id, shotsDir)

	start := time.Now()
	wd := newWatchdog(start)
	wd.run()

	frameNum := 0
	shots := 0
	for {
		p1, p2, active := player.Next()
		if !active {
			break
		}
		emu.SetInput(0, p1)
		emu.SetInput(1, p2)
		emu.RunFrame()
		wd.frameDone()

		if player.ShouldScreenshot() {
			name := screenshotName(id, ts, frameNum)
			if err := writeScreenshot(filepath.Join(shotsDir, name), emu); err != nil {
				log.Fatalf("failed to write screenshot %q: %v", name, err)
			}
			shots++
		}
		frameNum++
	}

	fmt.Printf("[CAPTURE] done: %d frames, %d screenshots -> %s (%.1fs)\n",
		frameNum, shots, shotsDir, time.Since(start).Seconds())
}

// readGameID reads the 10-byte product number from the disc's IP header
// (offset $20 in the IP user data) and returns it with trailing spaces
// trimmed. Returns "" if the disc is nil, unreadable, or doesn't have a
// Saturn IP header.
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

// sanitizeID reduces a disc product number to a safe filename component,
// replacing anything outside [A-Za-z0-9._-] with '_'. An empty or
// fully-stripped id becomes "unknown".
func sanitizeID(id string) string {
	if id == "" {
		return "unknown"
	}
	out := make([]byte, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
			out[i] = c
		default:
			out[i] = '_'
		}
	}
	return string(out)
}

// screenshotName builds the artifact filename id_ts_framenum.png.
func screenshotName(id string, ts int64, frame int) string {
	return fmt.Sprintf("%s_%d_%d.png", id, ts, frame)
}

// writeScreenshot encodes the emulator's current framebuffer to a PNG file.
func writeScreenshot(path string, emu *core.Emulator) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return encodeFramebufferPNG(f, emu.GetFramebuffer(), emu.GetFramebufferStride(), emu.GetActiveHeight())
}

// encodeFramebufferPNG writes the raw RGBA framebuffer as a lossless PNG.
// The framebuffer byte order matches image.RGBA. Rows are copied honoring the
// source stride, and copying stops if fb is shorter than stride*height.
func encodeFramebufferPNG(w io.Writer, fb []byte, stride, height int) error {
	width := stride / 4
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid framebuffer dimensions %dx%d (stride %d)", width, height, stride)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rowBytes := width * 4
	for y := 0; y < height; y++ {
		src := y * stride
		if src+rowBytes > len(fb) {
			break
		}
		dst := y * img.Stride
		copy(img.Pix[dst:dst+rowBytes], fb[src:src+rowBytes])
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	return enc.Encode(w, img)
}

// healthState is the watchdog's classification of recent throughput.
type healthState int

const (
	healthy healthState = iota
	slow
	frozen
)

// classifyHealth decides run health from the gap since the last completed
// frame and the number of frames completed in the last window. A stale gap
// means nothing is coming out (frozen) regardless of the draining rate; an
// in-range gap with too few completions is merely slow.
func classifyHealth(gap time.Duration, framesInWindow uint64) healthState {
	if gap > freezeGap {
		return frozen
	}
	if framesInWindow < healthFloor {
		return slow
	}
	return healthy
}

// watchdog aborts the process if emulation throughput collapses. The run loop
// updates the atomics after each completed frame; a separate goroutine samples
// them once per window.
type watchdog struct {
	start             time.Time
	completed         atomic.Uint64
	lastCompleteNanos atomic.Int64
}

func newWatchdog(start time.Time) *watchdog {
	wd := &watchdog{start: start}
	// Seed so the gap isn't stale before the first frame completes.
	wd.lastCompleteNanos.Store(start.UnixNano())
	return wd
}

// frameDone records a completed frame. Called once per RunFrame.
func (wd *watchdog) frameDone() {
	wd.completed.Add(1)
	wd.lastCompleteNanos.Store(time.Now().UnixNano())
}

// run starts the sampling goroutine.
func (wd *watchdog) run() {
	go func() {
		ticker := time.NewTicker(healthWin)
		defer ticker.Stop()
		var prev uint64
		for range ticker.C {
			now := time.Now()
			cur := wd.completed.Load()
			d := cur - prev
			prev = cur
			gap := time.Duration(now.UnixNano() - wd.lastCompleteNanos.Load())

			switch classifyHealth(gap, d) {
			case frozen:
				fmt.Fprintf(os.Stderr,
					"\n[WATCHDOG] emulation frozen: no frame completed for %v (%d frames total); aborting\n",
					gap.Round(time.Millisecond), cur)
				buf := make([]byte, 1<<20)
				n := runtime.Stack(buf, true)
				os.Stderr.Write(buf[:n])
				fmt.Fprintln(os.Stderr, "[WATCHDOG] end of stack dump")
				os.Exit(exitWatchdog)
			case slow:
				elapsed := now.Sub(wd.start)
				fps := float64(cur) / elapsed.Seconds()
				fmt.Fprintf(os.Stderr,
					"\n[WATCHDOG] emulation too slow: %d frames in the last %v (%.1f fps avg over %v); aborting\n",
					d, healthWin, fps, elapsed.Round(time.Millisecond))
				os.Exit(exitWatchdog)
			}
		}
	}()
}
