// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"

	"github.com/user-none/erings/core/sh2"
)

// Timing holds frame rate and scanline count for a region.
type Timing struct {
	FPS       int
	Scanlines int
}

// Timing model
//
// The Saturn system clock is 26.8741 MHz in 320-mode (NTSC) and
// 28.6364 MHz in 352-mode (NTSC); the PAL crystals are slightly
// slower. The SH-2, VDP1, and VDP2 all run at the system clock with
// no division (SMPC manual). The emulator's internal cycle unit is
// one system clock cycle: every subsystem consumes cycle budgets in
// that unit, so systemCyclesPerScanline * scanlines * fps gives the
// system-wide cycles-per-second and stays consistent across mode
// changes (320/352, NTSC/PAL, hi/lo-res).
//
// RunFrame walks every scanline. Each scanline is split by
// recalcSegments into segmentsPerScanline pieces: the first half
// covers the active period, the second half covers HBlank, so the
// HBlank boundary always lands between segments.
//
// Within each segment two tiers run:
//
//   Per cycle: VDP2.TickSystemCycles(1) plus one SH-2 step. With the
//   slave enabled, master and slave alternate based on whose cycle
//   counter is behind, giving a 1:1 per-cycle interleave.
//
//   Per segment (after the per-cycle loop): SCU DMA deferred IRQs,
//   SCSP (drains m68k and emits samples), CD Block, and VDP1
//   incremental command processing. Each receives the segment's
//   system-cycle count as its budget.
//
// Frame-level events are dispatched from RunFrame at boundaries:
// VBlank-IN at the last active scanline; VBlank-OUT and the PTM=10
// auto-draw gate at line 0 / segment 0.
//
// recalcTiming refreshes per-scanline, per-segment, and per-frame
// counts whenever VDP2 mode changes. fps is held to integer
// (60 NTSC / 50 PAL) so 44,100/fps and 11,289,600/fps give exact
// integer samples-per-frame and m68k-cycles-per-frame.
// CDBlock.RecalcTiming is fed the actual system clock rate so CD-DA
// and SCDQ pacing tracks the current mode rather than a compile-time
// constant.

// segmentsPerScanline controls how finely RunFrame slices each
// scanline for component ticking. Must be even so the active and
// HBlank halves can be split evenly. Increase to give other
// subsystems more frequent advances and finer SH-2 interleaving;
// decrease to reduce per-frame overhead.
const segmentsPerScanline = 4

// Emulator ties together all Saturn components and runs one frame at a time.
type Emulator struct {
	bus    *Bus
	master *sh2.CPU
	slave  *sh2.CPU

	// Subsystems.
	scu     *SCU
	smpc    *SMPC
	vdp1    *VDP1
	vdp2    *VDP2
	scsp    *SCSP
	cdblock *CDBlock

	// Per-frame timing derived from VDP2
	systemCyclesPerScanline uint32
	scanlines               uint16

	// System-cycle counters for SH-2 interleaving within a scanline.
	// "master" / "slave" refer to the CPU role, not the unit.
	masterCycles uint32
	slaveCycles  uint32

	// Per-segment system-cycle counts. Allocated once in NewEmulator
	// with length segmentsPerScanline; recalcSegments rewrites the
	// values whenever VDP2 timing changes. Sum is always
	// systemCyclesPerScanline.
	segSystemCycles []uint32

	// Per-frame target counts (recomputed when VDP2 mode changes).
	// fps is integer (60 NTSC / 50 PAL). System cycles per frame is
	// systemCyclesPerScanline * scanlines. Samples and m68k cycles per
	// frame come from dividing their hardware clock rates by fps;
	// integer 60/50 are chosen so 44,100/fps and 11,289,600/fps both
	// yield exact integer counts, giving variance-free output.
	systemCyclesPerFrame uint32
	samplesPerFrame      uint32
	m68kCyclesPerFrame   uint32

	audioBuffer []int16

	// hasBIOS records whether a real BIOS image was loaded via SetBIOS.
	// Start() uses this to decide between the real-BIOS boot (BIOS code
	// runs from $20000200) and the HLE-BIOS boot in hlebios.go.
	hasBIOS bool

	// ipImage caches the disc's Initial Program (16 sectors of user
	// data, 32 KB) read at SetDisc time. Used by region auto-detect
	// and the HLE BIOS boot path. Nil when no disc is attached or
	// the read failed. Refreshed on every SetDisc so future disc-swap
	// support stays simple.
	ipImage []byte
}

// NewEmulator creates a Saturn emulator with all components wired together.
// Call SetBIOS on the bus before running.
func NewEmulator() *Emulator {
	scu := NewSCU()
	smpc := NewSMPC(scu)
	vdp1 := NewVDP1(scu)
	vdp2 := NewVDP2(scu)
	scsp := NewSCSP(scu)
	cdblock := NewCDBlock(scu)
	bus := NewBus(scu, smpc, vdp1, vdp2, scsp, cdblock)
	scu.SetBus(bus)
	scsp.SetCDAudioSource(cdblock)

	master := sh2.New(bus, true)
	slave := sh2.New(bus, false)

	e := &Emulator{
		bus:             bus,
		master:          master,
		slave:           slave,
		scu:             scu,
		smpc:            smpc,
		vdp1:            vdp1,
		vdp2:            vdp2,
		scsp:            scsp,
		cdblock:         cdblock,
		segSystemCycles: make([]uint32, segmentsPerScanline),
	}

	// Wire boundary-crossing callbacks. SCU drives master IRL, SMPC
	// fires system reset (CKCHG) / NMI / slave reset.
	scu.SetIRLHandler(master.SetIRL, master.ClearIRL)
	smpc.systemReset = e.systemReset
	smpc.masterNMI = master.NMI
	smpc.slaveReset = slave.Reset
	master.SetIRLAck(scu.AcknowledgeInterrupt)

	e.recalcTiming()

	return e
}

// systemReset is invoked when SMPC processes a CKCHG / SYSRES command.
// It resets the video and audio subsystems and refreshes timing.
func (e *Emulator) systemReset() {
	e.scsp.Reset()
	e.vdp1.Reset()
	e.vdp2.Reset()
	e.scu.Reset()
	e.recalcTiming()
}

// recalcTiming derives per-scanline system-cycle counts, per-segment
// boundaries, and per-frame target counts from VDP2 state. Per-frame
// targets are exposed to SCSP via StartFrame so it emits exactly the
// right number of samples and m68k cycles per frame regardless of
// how the frame is sliced.
func (e *Emulator) recalcTiming() {
	vdp2 := e.vdp2
	e.systemCyclesPerScanline = vdp2.SystemCyclesPerLine()
	e.scanlines = vdp2.LinesPerFrame()

	e.recalcSegments(vdp2.ActiveSystemCyclesPerLine())

	// Per-frame target counts. Integer fps: 60 NTSC / 50 PAL.
	e.systemCyclesPerFrame = e.systemCyclesPerScanline * uint32(e.scanlines)
	var fps uint32 = 60
	if vdp2.IsPAL() {
		fps = 50
	}
	e.samplesPerFrame = 44100 / fps
	e.m68kCyclesPerFrame = 11289600 / fps

	// CD block sector / SCDQ / boot timings derive from the actual
	// system clock rate, not a fixed compile-time constant. NTSC 320
	// (1708), NTSC 352 (1820), PAL 320, PAL 352 each yield a different
	// cycles-per-second; the CD block was previously hardcoded to the
	// 352 value and ran 6.5% slow in 320 mode (audible as crunchy
	// CD-DA / starved EXTS). Keep this hook in sync with any future
	// timing-currency change.
	e.cdblock.RecalcTiming(e.systemCyclesPerFrame * fps)
}

// recalcSegments rewrites e.segSystemCycles in place to split each
// scanline into segmentsPerScanline pieces. The HBlank boundary
// always lands between two segments: the first half of the slice
// covers the active period, the second half covers the HBlank period.
// Each half is divided as evenly as possible; any odd remainder lands
// in that half's final segment so the sum stays equal to the original
// active / HBlank system-cycle count.
func (e *Emulator) recalcSegments(activeCycles uint32) {
	hblank := e.systemCyclesPerScanline - activeCycles
	half := uint32(segmentsPerScanline / 2)

	if half == 0 {
		// segmentsPerScanline == 1: whole scanline is one segment.
		e.segSystemCycles[0] = e.systemCyclesPerScanline
		return
	}

	activeBase := activeCycles / half
	for i := uint32(0); i < half; i++ {
		e.segSystemCycles[i] = activeBase
	}
	e.segSystemCycles[half-1] += activeCycles - activeBase*half

	hblankBase := hblank / half
	for i := uint32(0); i < half; i++ {
		e.segSystemCycles[half+i] = hblankBase
	}
	e.segSystemCycles[segmentsPerScanline-1] += hblank - hblankBase*half
}

// SetDisc attaches a disc reader to the CD Block, caches the disc's
// IP image for later HLE-BIOS boot, and auto-detects the disc's
// region to set the SMPC area code and VDP2 PAL bit. The region
// step ensures BIOS and SYS_ARE region checks pass regardless of
// which BIOS region is loaded.
func (e *Emulator) SetDisc(d DiscReader) {
	e.cdblock.SetDisc(d)
	e.ipImage = nil
	if d == nil {
		return
	}
	e.ipImage = readIPImage(d)
	e.autoDetectRegion()
}

// autoDetectRegion reads the area-code field from the cached IP
// image's System ID header and sets the SMPC area code to match
// the disc's first compatible region, propagating PAL/NTSC
// selection into VDP2 timing. The area codes are 10 ASCII bytes
// at System ID offset $40: J=Japan, T=Asia NTSC, U=North America,
// E=Europe PAL.
func (e *Emulator) autoDetectRegion() {
	if len(e.ipImage) < 0x4A {
		return
	}
	areaField := e.ipImage[0x40:0x4A]
	for _, ch := range areaField {
		switch ch {
		case 'U':
			e.smpc.areaCode = 0x04
			e.vdp2.SetPAL(false)
			return
		case 'J':
			e.smpc.areaCode = 0x01
			e.vdp2.SetPAL(false)
			return
		case 'E':
			e.smpc.areaCode = 0x0C
			e.vdp2.SetPAL(true)
			return
		}
	}
}

// GetSRAM returns a copy of the internal backup RAM.
func (e *Emulator) GetSRAM() []byte { return e.bus.GetBackupRAM() }

// SetSRAM loads previously persisted internal backup RAM.
func (e *Emulator) SetSRAM(data []byte) { e.bus.SetBackupRAM(data) }

// RunFrame executes one complete frame of emulation.
func (e *Emulator) RunFrame() {
	smpc := e.smpc
	scsp := e.scsp
	vdp2 := e.vdp2

	// SNDOFF sync at frame start
	if !smpc.SoundEnabled() && !scsp.InReset() {
		scsp.SetInReset(true)
	}

	// Prepare audio mix buffer for this frame (stereo interleaved)
	// ~735 samples NTSC, ~882 PAL, plus margin. * 2 for stereo.
	scsp.ResetMixBuffer(900 * 2)

	// Refresh timing in case VDP2 mode changed (CKCHG, TVMD write)
	e.recalcTiming()

	// Hand SCSP the per-frame targets so it emits an exact count
	// regardless of how segments divide the frame.
	scsp.StartFrame(e.systemCyclesPerFrame, e.samplesPerFrame, e.m68kCyclesPerFrame)

	slaveEnabled := smpc.SSHEnabled()
	vdp1 := e.vdp1
	activeLines := vdp2.ActiveLines()

	for line := uint16(0); line < e.scanlines; line++ {
		e.masterCycles = 0
		e.slaveCycles = 0

		// Per-scanline SMPC command-dispatch delay tick.
		smpc.TickScanline()

		for seg := 0; seg < segmentsPerScanline; seg++ {
			segWidth := e.segSystemCycles[seg]

			// -- Tier 1: Per-cycle SH-2/VDP2 --
			for cyc := uint32(0); cyc < segWidth; cyc++ {
				vdp2.TickSystemCycles(1)
				if slaveEnabled {
					if e.masterCycles <= e.slaveCycles {
						e.stepMaster()
						e.stepSlave()
					} else {
						e.stepSlave()
						e.stepMaster()
					}
				} else {
					e.stepMaster()
				}
			}

			// -- Tier 2: SCU DMA deferred interrupts --
			e.scu.TickSystemCycles(segWidth)

			// -- Tier 3: SCSP (drains sound CPU and processes samples) --
			if !smpc.SoundEnabled() && !scsp.InReset() {
				scsp.SetInReset(true)
			}
			scsp.TickSystemCycles(segWidth)

			// -- Tier 4: CD Block --
			e.cdblock.TickSystemCycles(segWidth)

			// -- Tier 5: VDP1 incremental command processing --
			// VBlankOut swap and PTM=10 auto-draw fire at the start of
			// line 0, after tier 1 of seg 0 has run so the SH-2's
			// vbout-IRQ handler had at least a partial segment of
			// cycles to read CEF=1 from the prior draw before VDP1
			// clears it. Bus contention (vdp1WriteStallSystemCycles)
			// handles the during-draw write race for writes that
			// arrive after this gate.
			if line == 0 && seg == 0 {
				vdp1.VBlankOut()
				if vdp1.PTM() == 2 && vdp1.ConsumeFBSwap() {
					vdp1.StartAutoDraw()
				}
			}
			vdp1.TickSystemCycles(segWidth)
		}

		// V-Blank-IN at the boundary of the last active scanline,
		// matching when VDP2.onLineEnd raises RaiseVBlankIN to SCU.
		// VBlankIn handles framebuffer swap, erase, BEF/CEF latch,
		// and LOPR update at FB change. PTM=10 auto-trigger is
		// deferred to end-of-frame below so the game's V-blank ISR
		// (which runs after V-blank-IN and may write ENDR or new
		// commands) lands its register writes before the auto-draw
		// resets and starts processing.
		if line+1 == activeLines {
			vdp1.VBlankIn()
		}
	}

	smpc.TickFrame()

	vdp2.SetVDP1DisplayFB(vdp1.DisplayFB(vdp2.FieldBit()), vdp1.Is8bpp(), vdp1.FBWidth(), vdp1.FBHeight())
	vdp2.RenderFrame()

	// Manual-mode erase deferred from VBlankIn so this frame's display
	// uses pre-erase content. Per Sec.4.2 the erase progresses during
	// active scanlines on hardware after VDP2 reads each line; running
	// it here is the atomic approximation.
	vdp1.PerformLateErase()

	e.audioBuffer = scsp.MixBuffer()

	// Enable sound CPU at end of frame
	if smpc.SoundEnabled() && scsp.InReset() {
		scsp.SetInReset(false)
	}
}

// stepMaster advances the master SH-2 by one cycle, applying corrections.
func (e *Emulator) stepMaster() {
	state := e.master.Clock()
	e.masterCycles++

	// Hazard penalty: inject 1 stall cycle
	if state.LoadUseStall {
		e.masterCycles++
	}

	// Check MINIT write -> trigger slave FRT input capture
	if state.Bus == sh2.BusWrite && e.bus.MINITWritten() {
		e.slave.FRTInputCapture()
	}
}

// stepSlave advances the slave SH-2 by one cycle, applying corrections.
func (e *Emulator) stepSlave() {
	state := e.slave.Clock()
	e.slaveCycles++

	// Hazard penalty: inject 1 stall cycle
	if state.LoadUseStall {
		e.slaveCycles++
	}

	// Check SINIT write -> trigger master FRT input capture
	if state.Bus == sh2.BusWrite && e.bus.SINITWritten() {
		e.master.FRTInputCapture()
	}
}

// GetFramebuffer returns raw RGBA pixel data for the current frame.
func (e *Emulator) GetFramebuffer() []byte {
	return e.vdp2.Framebuffer()
}

// GetFramebufferStride returns the stride (bytes per row) of the framebuffer.
func (e *Emulator) GetFramebufferStride() int {
	return e.vdp2.FramebufferStride()
}

// GetActiveHeight returns the current active display height.
func (e *Emulator) GetActiveHeight() int {
	return e.vdp2.DisplayHeight()
}

// PixelAspectRatio returns the pixel aspect ratio for the current video
// mode. The Saturn active picture is a fixed physical rectangle; the
// horizontal dot count (320/352/640/704) and vertical line count
// (224/240/256, doubled under double-density interlace) only change
// sampling density, not the displayed shape. So the pixel aspect is
// derived to keep the displayed picture aspect constant at the Saturn's
// 4:3 active area across every resolution and interlace mode:
// (W/H) * PAR == 4/3. Computed on demand so it always matches the
// currently reported framebuffer dimensions.
func (e *Emulator) PixelAspectRatio() float64 {
	w := e.vdp2.FramebufferStride() / 4
	h := e.vdp2.DisplayHeight()
	if w <= 0 || h <= 0 {
		return 1.0
	}
	return (4.0 / 3.0) * float64(h) / float64(w)
}

// SetInput maps active-high button bits to SMPC active-low pad data.
func (e *Emulator) SetInput(player int, buttons uint32) {
	if player < 0 || player > 1 {
		return
	}

	// Start with all released (active-low: 1 = released).
	// Byte 2 bits 2-0 are unused and must be 1.
	var b1, b2 uint8
	b1 = 0xFF
	b2 = 0xFF

	// Map active-high bits to SMPC active-low bits.
	// Bits: 0=Up, 1=Down, 2=Left, 3=Right,
	//   4=A, 5=B, 6=C, 7=X, 8=Y, 9=Z, 10=L, 11=R, 12=Start
	if buttons&(1<<0) != 0 {
		b1 &^= 1 << 4 // Up
	}
	if buttons&(1<<1) != 0 {
		b1 &^= 1 << 5 // Down
	}
	if buttons&(1<<2) != 0 {
		b1 &^= 1 << 6 // Left
	}
	if buttons&(1<<3) != 0 {
		b1 &^= 1 << 7 // Right
	}
	if buttons&(1<<4) != 0 {
		b1 &^= 1 << 2 // A
	}
	if buttons&(1<<5) != 0 {
		b1 &^= 1 << 0 // B
	}
	if buttons&(1<<6) != 0 {
		b1 &^= 1 << 1 // C
	}
	if buttons&(1<<7) != 0 {
		b2 &^= 1 << 6 // X
	}
	if buttons&(1<<8) != 0 {
		b2 &^= 1 << 5 // Y
	}
	if buttons&(1<<9) != 0 {
		b2 &^= 1 << 4 // Z
	}
	if buttons&(1<<10) != 0 {
		b2 &^= 1 << 3 // L
	}
	if buttons&(1<<11) != 0 {
		b2 &^= 1 << 7 // R
	}
	if buttons&(1<<12) != 0 {
		b1 &^= 1 << 3 // Start
	}

	e.smpc.SetPadData(player, uint16(b1)<<8|uint16(b2))
}

// GetAudioSamples returns the audio sample buffer for the current frame.
func (e *Emulator) GetAudioSamples() []int16 {
	return e.audioBuffer
}

// VDP1SwapCount returns the monotonic count of VDP1 FB swaps since
// reset. The host UI uses the delta between samples to derive a
// "game fps" value distinct from RunFrame fps - games can request
// fewer swaps than the emulator runs frames (e.g. a 30 fps game on
// a 60 fps emulator), and the swap rate is what the player sees.
func (e *Emulator) VDP1SwapCount() uint64 {
	return e.vdp1.SwapCount()
}

// SetSH2Trace installs per-instruction trace callbacks on the master
// and slave SH-2. Pass nil to clear. Must be called while the cores are
// not executing (between frames) so the assignment cannot race the
// per-instruction call site.
func (e *Emulator) SetSH2Trace(master, slave func(pc uint32, op uint16)) {
	e.master.TraceFunc = master
	e.slave.TraceFunc = slave
}

// GetTiming returns the frame timing derived from VDP2 state.
func (e *Emulator) GetTiming() Timing {
	if e.vdp2.IsPAL() {
		return Timing{FPS: 50, Scanlines: 313}
	}
	return Timing{FPS: 60, Scanlines: 263}
}

// SetBIOS loads a BIOS image by key name. For main_bios, also sets
// the master SH-2 entry point from the reset vectors.
func (e *Emulator) SetBIOS(key string, data []byte) error {
	if key == "main_bios" {
		if err := e.bus.SetBIOS(data); err != nil {
			return err
		}
		e.master.LoadResetVectors()
		// Slave SH-2 uses the same reset vectors. It is held in reset
		// until SMPC SSHON releases it, but its PC/SP must already be
		// at the power-on entry point so it begins executing BIOS code
		// rather than interpreting the vector table as instructions.
		e.slave.LoadResetVectors()
		e.hasBIOS = true
		return nil
	}
	return errors.New("unknown BIOS key: " + key)
}

// Start prepares the emulator for the first RunFrame. When no real
// BIOS was loaded via SetBIOS, it constructs an HLEBIOS in place
// and boots it from the cached disc IP image. The HLEBIOS instance
// is not retained on the emulator: it stays alive only through the
// closures it wires into master.HLEHook / slave.HLEHook, and is
// otherwise invisible to the rest of the emulator. Real-BIOS boots
// construct nothing and leave the CPU hooks nil.
func (e *Emulator) Start() error {
	if !e.hasBIOS {
		hle := NewHLEBIOS(e.bus, e.master, e.slave)
		if err := hle.Boot(e.ipImage); err != nil {
			return err
		}
	}
	return nil
}

// Close releases emulator resources.
func (e *Emulator) Close() {
}
