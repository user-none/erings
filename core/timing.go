// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// Timing model
//
// The Saturn system clock is 26.8741 MHz in 320-mode (NTSC) and
// 28.6364 MHz in 352-mode (NTSC); the PAL crystals are slightly
// slower (SMPC manual Table 1.1). The SH-2, VDP1, and VDP2 all run at
// the system clock with no division. The emulator's internal cycle unit
// is one system clock cycle, and the display runs at integer fps
// (60 NTSC / 50 PAL). The per-scanline cycle width is the documented
// clock divided by (fps * scanlines), which is not an integer, so
// recalcTiming carries the fractional remainder across frames: the
// width is uniform within a frame and varies by +/-1 between frames,
// and the per-second cycle total converges to the documented crystal.
// This holds the SH-2 FRT at the hardware rate relative to the
// fixed-rate SCSP without changing the integer display fps.

// Timing holds frame rate and scanline count for a region.
type Timing struct {
	FPS       int
	Scanlines int
}

// Documented Saturn system clock frequencies (Hz) by region and horizontal
// mode, from the SMPC User's Manual Table 1.1 (Initialization Status During
// Power On). The 320 clock drives 320/640 modes, the 352 clock drives 352/704.
// These divided by the per-line dot counts (1708 for 320, 1820 for 352) give
// the standard line rates 15,734 Hz (NTSC) / 15,625 Hz (PAL).
const (
	systemClockNTSC320 = 26874100
	systemClockNTSC352 = 28636400
	systemClockPAL320  = 26687500
	systemClockPAL352  = 28437500
)

// recalcTiming derives per-scanline system-cycle counts and per-frame
// target counts from VDP2 state. Per-frame targets are exposed to SCSP
// via StartFrame so it emits exactly the right number of samples and
// m68k cycles per frame regardless of how the frame is sliced.
//
// The per-scanline cycle budget is the documented system clock divided by
// (fps * scanlines). That is not an integer (the integer 60/50 fps and the
// integer scanline count do not factor the crystal), so the fractional
// remainder is carried across frames in lineWidthAccum. The result is a
// per-scanline width that is uniform within a frame and varies by +/-1
// between frames; over a second the total converges to the documented clock.
// This keeps the SH-2 FRT (which counts system cycles) at the hardware rate
// relative to the fixed-rate SCSP, without changing the integer display fps.
func (e *Emulator) recalcTiming() {
	vdp2 := e.vdp2
	e.scanlines = vdp2.LinesPerFrame()

	var fps uint32 = 60
	if vdp2.IsPAL() {
		fps = 50
	}

	// Select the documented crystal from region and horizontal-clock family.
	var trueClock uint32
	if vdp2.IsPAL() {
		if vdp2.Is352Clock() {
			trueClock = systemClockPAL352
		} else {
			trueClock = systemClockPAL320
		}
	} else {
		if vdp2.Is352Clock() {
			trueClock = systemClockNTSC352
		} else {
			trueClock = systemClockNTSC320
		}
	}

	d := fps * uint32(e.scanlines)

	// Reset the carry on a mode change: a remainder accumulated against the
	// previous clock/divisor is meaningless under the new one. Within a stable
	// mode the accumulator must persist - that is what makes the per-second
	// total converge to the crystal.
	if trueClock != e.lastTrueClock || d != e.lastD {
		e.lineWidthAccum = 0
		e.lastTrueClock = trueClock
		e.lastD = d
	}

	e.lineWidthAccum += uint64(trueClock)
	e.systemCyclesPerScanline = uint32(e.lineWidthAccum / uint64(d))
	e.lineWidthAccum %= uint64(d)
	e.systemCyclesPerFrame = e.systemCyclesPerScanline * uint32(e.scanlines)

	// Samples and m68k cycles per frame are on the audio crystal and divide
	// the integer fps exactly, so they need no carry.
	e.samplesPerFrame = 44100 / fps
	e.m68kCyclesPerFrame = 11289600 / fps

	// CD block sector / SCDQ / boot timings track the documented system clock
	// directly (constant within a mode), not the per-frame budget which now
	// wobbles by the carry.
	e.cdblock.RecalcTiming(trueClock)
}

// GetTiming returns the frame timing derived from VDP2 state.
func (e *Emulator) GetTiming() Timing {
	if e.vdp2.IsPAL() {
		return Timing{FPS: 50, Scanlines: 313}
	}
	return Timing{FPS: 60, Scanlines: 263}
}
