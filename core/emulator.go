// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"
	"strings"
	"sync/atomic"

	"github.com/user-none/erings/core/sh2"
)

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

	// Per-frame target counts (recomputed when VDP2 mode changes).
	// fps is integer (60 NTSC / 50 PAL). System cycles per frame is
	// systemCyclesPerScanline * scanlines. Samples and m68k cycles per
	// frame come from dividing their hardware clock rates by fps;
	// integer 60/50 are chosen so 44,100/fps and 11,289,600/fps both
	// yield exact integer counts, giving variance-free output.
	systemCyclesPerFrame uint32
	samplesPerFrame      uint32
	m68kCyclesPerFrame   uint32

	// Per-line cycle-width carry. The per-scanline budget is the documented
	// system clock divided by (fps * scanlines), which is not an integer, so
	// the fractional remainder is accumulated here and dispensed across
	// frames. systemCyclesPerScanline is uniform within a frame and varies by
	// +/-1 between frames; the per-second total converges to the crystal. The
	// accumulator persists across same-mode frames (that is the convergence
	// mechanism) and is reset only when the mode changes, detected via
	// lastTrueClock / lastD.
	lineWidthAccum uint64
	lastTrueClock  uint32
	lastD          uint32

	audioBuffer []int16

	// hasBIOS records whether a real BIOS image was loaded via SetBIOS.
	// Start() uses this to decide between the real-BIOS boot (BIOS code
	// runs from $20000200) and the HLE-BIOS boot in hlebios.go.
	hasBIOS bool

	// Pending option state (applied by Start)
	pendingFastBoot bool

	// ipImage caches the disc's Initial Program (16 sectors of user
	// data, 32 KB) read at SetDisc time. Used by region auto-detect
	// and the HLE BIOS boot path. Nil when no disc is attached or
	// the read failed. Refreshed on every SetDisc so future disc-swap
	// support stays simple.
	ipImage []byte

	// Per-goroutine progress counters: system cycles completed this frame,
	// reset by RunFrame before the workers are kicked (both are parked at
	// that point). Each goroutine stores its counter after every chunk and
	// spin-waits on the counters of the goroutines constraining it (see
	// frame.go's timing-model comment for the constraint graph). The
	// counters bound only WHEN each goroutine's clock sits relative to the
	// others; the ordering and safety of individual shared accesses to
	// component registers, VRAM, and WRAM within that window is handled
	// separately, by the per-area bus locks.
	masterCycles    atomic.Int64
	secondaryCycles atomic.Int64
	vdpCycles       atomic.Int64

	// Per-frame walk parameters for the workers, written before the
	// kick channel sends so both workers see the values the main
	// goroutine's frame loop uses even if a mid-frame SMPC reset rewrites
	// the live timing fields.
	frameLineWidth     uint32
	frameScanlines     uint16
	vdpWalkActiveLines uint16
	vdpWalkActiveCyc   uint32
	vdpWalkWidth       uint16

	// The VDP worker cycle-walks the frame: VDP1 incremental command
	// ticking per chunk, with VDP2 BeginFrame/RenderLine and the VDP1
	// frame events fired at their cycle positions. RunFrame sends on
	// vdpJobChBegin to start the walk and receives on vdpJobChFinished
	// at frame end.
	vdpJobChBegin    chan struct{}
	vdpJobChFinished chan struct{}

	// The secondary bucket (slave SH-2 + SCU/SCSP/CD + SMPC dispatch)
	// runs on its own worker goroutine every frame. Slave SH-2 stepping
	// inside it is gated on SSHEnabled. The master runs on the main
	// goroutine.
	secondaryJobCh chan struct{}

	// closed guards Close so the worker job channels are closed exactly
	// once.
	closed bool

	// pendingSystemReset requests a full system reset (SMPC SYSRES /
	// CKCHG). systemReset resets components owned by the workers and
	// rewrites the timebase, so it is applied at the frame boundary
	// where both workers are parked rather than when requested.
	pendingSystemReset atomic.Bool

	// pendingMasterNMI holds the master NMI raised by a dot-clock-change
	// command (SMPC CKCHG320/CKCHG352) until the frame-boundary
	// systemReset has run, then RunFrame delivers it. The SMPC User's
	// Manual section 2.3 (Resetable System Management Commands) states
	// that CKCHG resets VDP1, VDP2, SCU, and SCSP to their power-on
	// values and then resumes the master. erings resumes the master with
	// the NMI, so it must be delivered after systemReset applies that
	// power-on reset, not before.
	pendingMasterNMI atomic.Bool

	// Cross-CPU FRT input-capture flags. A MINIT write by the master must
	// capture the slave's FRT; a SINIT write by the slave must capture the
	// master's. The writer sets its flag; the receiving CPU clears it and
	// latches its own FRT at the next sync barrier (frame.go barrier),
	// within syncChunkCycles of the write. The tight barrier supplies the
	// relative-timing guarantee that the loose-drift design needed an
	// explicit park-and-respond rendezvous for. A plain bool suffices: a
	// MINIT/SINIT write is a deliberate sync, never spammed within a chunk.
	minitPending atomic.Bool
	sinitPending atomic.Bool

	frameTotalCycles int64
	// Cycle debt from draining a TAS that straddled a stepping-loop
	// boundary: the master carries into its next line, the slave into
	// its next chunk. Each is owned by its stepping goroutine.
	masterTASCarry uint32
	slaveTASCarry  int64

	// Save-state scratch, reused across Serialize calls: rewind
	// captures at up to every-other-frame rates, and fresh 12+ MB
	// allocations per capture caused GC stutter. Serialize runs only
	// at the frame boundary on one goroutine, so plain fields suffice;
	// only the returned state bytes are freshly allocated.
	stateBody    []byte
	statePayload []byte
	stateComp    stateCompressor
}

// NewEmulator creates a Saturn emulator with all components wired together.
// Call SetBIOS on the bus before running.
func NewEmulator() *Emulator {
	scu := NewSCU()
	smpc := NewSMPC()
	vdp1 := NewVDP1(scu)
	vdp2 := NewVDP2(scu)
	scsp := NewSCSP(scu)
	cdblock := NewCDBlock(scu)
	bus := NewBus(scu, smpc, vdp1, vdp2, scsp, cdblock)
	scu.SetBus(bus)
	scsp.SetCDAudioSource(cdblock)
	vdp2.SetEXBGSource(cdblock)

	master := sh2.New(bus, true)
	slave := sh2.New(bus, false)

	e := &Emulator{
		bus:     bus,
		master:  master,
		slave:   slave,
		scu:     scu,
		smpc:    smpc,
		vdp1:    vdp1,
		vdp2:    vdp2,
		scsp:    scsp,
		cdblock: cdblock,
	}

	// Wire boundary-crossing callbacks. SCU drives master IRL. SMPC
	// requests the master NMI, defers its system reset to the frame
	// boundary, and resets the slave directly.
	scu.SetIRLHandler(master.SetIRL, master.ClearIRL)
	smpc.systemReset = func() { e.pendingSystemReset.Store(true) }
	smpc.masterNMI = master.NMIRequest
	smpc.masterNMIDeferred = func() { e.pendingMasterNMI.Store(true) }
	smpc.slaveReset = slave.Reset
	master.SetIRLAck(scu.AcknowledgeInterrupt)

	// VDP worker (VDP1 walk + VDP2 line render) and slave SH-2 worker
	// kick channels.
	e.vdpJobChBegin = make(chan struct{}, 1)
	e.vdpJobChFinished = make(chan struct{}, 1)
	e.secondaryJobCh = make(chan struct{}, 1)

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
	// CKCHG/SYSRES turns the slave SH-2 off (SMPC User's Manual: CKCHG320/
	// CKCHG352 leave the slave OFF) and resets the video/sound subsystems.
	// Drop any in-flight cross-CPU FRT input capture: a SINIT/MINIT issued
	// just before the reset must not leave a peer CPU's ICF latched once the
	// CPU driving it is turned off. Both workers are parked here, so the
	// peer FRT state is reachable without a race.
	e.minitPending.Store(false)
	e.sinitPending.Store(false)
	e.master.ClearFRTInputCapture()
	e.slave.ClearFRTInputCapture()
	// Reset the per-line cycle-width carry so timing restarts deterministically
	// after a clock change; recalcTiming re-detects the mode below.
	e.lineWidthAccum = 0
	e.lastTrueClock = 0
	e.lastD = 0
	e.recalcTiming()
}

// SetDisc attaches a disc reader to the CD Block, caches the disc's
// IP image for later HLE-BIOS boot, and auto-detects the disc's
// region to set the SMPC area code and VDP2 PAL bit. The region
// step ensures BIOS and SYS_ARE region checks pass regardless of
// which BIOS region is loaded.
func (e *Emulator) SetDisc(d DiscReader) {
	e.cdblock.SetDisc(d)
	e.ipImage = nil
	e.bus.ramCartID = ramCartID4MB
	if d == nil {
		return
	}
	e.ipImage = readIPImage(d)
	e.applyRAMCartOverride()
	e.autoDetectRegion()
}

// applyRAMCartOverride sets the bus RAM cartridge ID from the disc's
// product number via ramCartOverrides. Runs at disc load, before any boot
// activity, so the game's cartridge probe sees consistent state.
func (e *Emulator) applyRAMCartOverride() {
	if len(e.ipImage) < 0x2A {
		return
	}
	product := strings.Trim(string(e.ipImage[0x20:0x2A]), " \x00")
	e.bus.ramCartID = ramCartIDForProduct(product)
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

// GetFramebuffer returns raw RGBA pixel data for the most recently
// completed frame. RunFrame waits for the VDP worker's frame walk
// before returning, so the buffer is complete and the worker parked
// while the host reads it.
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

// SetOption applies a core option change identified by key.
// Values are stored and applied when Start() is called.
func (e *Emulator) SetOption(key string, value string) {
	switch key {
	case "fast_boot":
		e.pendingFastBoot = (value == "true")
	}
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
	} else if e.pendingFastBoot {
		e.installFastBoot()
	}

	// Spawn the frame workers. They park on their job channels until
	// RunFrame kicks them, and run until Close closes the channels.
	go e.vdpWorker()
	go e.secondaryWorker()

	return nil
}

// Close stops the frame workers spawned by Start. It must not run
// concurrently with RunFrame, and RunFrame must not be called after
// Close (its job-channel send would panic). Idempotent.
func (e *Emulator) Close() {
	if e.closed {
		return
	}
	e.closed = true
	close(e.vdpJobChBegin)
	close(e.secondaryJobCh)
}
