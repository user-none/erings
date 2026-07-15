// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "sync/atomic"

const (
	vdp1VRAMSize = 512 * 1024 // 512 KB
	vdp1FBSize   = 256 * 1024 // 256 KB
)

// pixelsPerYieldChunk controls how often per-command rasterizers
// check the remaining cycle budget. Smaller values give finer
// SH-2 / VDP1 interleaving at the cost of more in-loop branch
// overhead; larger values reduce overhead but allow more system
// cycles of overshoot per yield. Must be at least 1.
const pixelsPerYieldChunk = 8

// vdp1WriteStallSystemCycles is the bus-contention penalty (in system
// cycles) charged to VDP1's per-segment cycle budget for each 16-bit
// VRAM port transaction by an SH-2 store while drawActive=true. The
// VDP1 port is 16-bit, so a 32-bit write costs this twice. Drawing
// slows when the SH-2 is busy writing, keeping the SH-2 ahead of
// drawing's read position so each command lands before VDP1 reads it.
//
// The VDP1 User's Manual Section 2.1 (Address Map, VRAM) gives only
// "more than 10 wait cycles" per arbitrated access, with no exact value.
// 24 sits above that floor so VDP1's command-table reads stay behind the
// SH-2's command-list writes. We've seen below ~22 the draw can latch
// a stale end-bit left by a prior shorter list and truncate the command
// list, dropping the tail sprites. A slightly larger value is used to
// allow for some head room.
const vdp1WriteStallSystemCycles = 24

// vdp1DMABurstStallPerWord is the bus-contention penalty (in system
// cycles) charged to VDP1's per-segment cycle budget for each 16-bit
// B-Bus word of a bus-master burst (SCU-DMA) into VRAM while
// drawActive=true. Per the VDP1 User's Manual Section 2.1 (Address Map,
// VRAM, p.19) a DMA burst transfers continuously in a short period, so a
// word costs far less than the per-access arbitration wait an SH-2 single
// store pays. The B-Bus is 16-bit and a 32-bit write moves two words. At
// one cycle per word a full burst stalls the draw by bytes/2 system
// cycles - bounded by the transfer size, not by per-word arbitration.
const vdp1DMABurstStallPerWord = 1

// vdp1PreClipLineCycles is the per-line pre-clipping detection cost
// charged when a connecting line or whole command is wholly outside
// the drawing area. Off-screen dots of a partially-visible line are
// charged per-dot like visible dots, since the DDA iteration runs
// regardless and only the framebuffer write is discarded.
//
// The VDP1 User's Manual Supplement Section 1.2 (Pre-Clipping Disable)
// states the detection overhead is "up to five CPU clock cycles for
// one line" - a maximum, with no formula for the actual value. 3 is
// chosen to approximate an average over the distribution of detection
// cases (trivial reject through full clip-edge intersection) rather
// than charging the worst-case 5 every time.
const vdp1PreClipLineCycles = 3

// Command phase tags. Non-zero values identify which rasterizer
// holds in-progress state and must be resumed on the next
// processCommands entry. phaseIdle means no command in progress.
const (
	phaseIdle            = 0
	phaseNormalSprite    = 1
	phaseScaledSprite    = 2
	phaseDistortedSprite = 3
	phasePolygon         = 4
	phasePolyline        = 5
	phaseLine            = 6
)

// spriteResumeState carries the in-progress state of a normal or
// scaled sprite rasterizer across yields. isScaled distinguishes
// the two; setup-time constants live alongside iterator fields so
// resume entry needs no recomputation.
type spriteResumeState struct {
	charAddr      uint32
	charW, charH  int
	colorMode     uint16
	cc            uint16
	ecdOff, spdOn bool
	msbOn, mesh   bool
	userClip      uint16
	flipH, flipV  bool
	drawX, drawY  int
	clipX, clipY  int
	gt            [4]uint16

	isScaled           bool
	destW, destH       int
	dstX1, dstY1       int
	effFlipH, effFlipV bool
	hssShrinkX         bool
	hssOddParity       bool
	hssEcdOff          bool

	outerIdx     int
	innerIdx     int
	endCodeCount int
	prevSrcX     int
}

// VDP1 implements the Sega Saturn Video Display Processor 1.
type VDP1 struct {
	// Write-only registers (stored for MODR readback). These hold the
	// active values driving internal signals (drawing, swap, erase).
	tvmr uint16 // TV mode selection
	fbcr uint16 // Frame buffer change mode
	ptmr uint16 // Plot trigger
	ewdr uint16 // Erase/write data
	ewlr uint16 // Erase/write upper-left coordinate
	ewrr uint16 // Erase/write lower-right coordinate

	// Shadow registers per VDP1 manual Sec.3 "System Register Settings
	// Switch Timing". SH-2 writes to deferred bits/registers go here;
	// latchPending() copies pending into active at vblank-IN (the
	// field-change boundary). MODR readback also sources from these
	// because the manual specifies MODR shows the written value, not
	// the active internal-signal value.
	fbcrPending uint16 // EOS/DIE/DIL bits (4/3/2). FCM/FCT immediate.
	ptmrPending uint16 // PTM=00 or 10. PTM=01 is immediate.
	ewdrPending uint16
	ewlrPending uint16
	ewrrPending uint16

	// Read-only status
	edsr uint16 // Transfer end status
	lopr uint16 // Last operation command address
	copr uint16 // Current operation command address

	// Memory
	vram      []byte // 512 KB
	drawFB    []byte // 256 KB - CPU writes here, VDP1 draws here
	displayFB []byte // 256 KB - VDP2 reads from here

	// Drawing state
	localX      int16
	localY      int16
	sysClipX    int16 // system clip right edge (inclusive)
	sysClipY    int16 // system clip bottom edge (inclusive)
	userClipX1  int16
	userClipY1  int16
	userClipX2  int16
	userClipY2  int16
	drawPending bool // for manual draw trigger (PTMR=1)
	drawEnd     bool // ENDR written during active draw: force terminate command processing
	drawActive  bool // true only while the command processor is walking the command list

	// Incremental command processing state. Persists across
	// TickSystemCycles calls so a list spanning multiple segments
	// completes naturally. Reset at the top of each draw via
	// startDraw().
	procAddr        uint32 // next command-table address to fetch
	procReturnAddr  uint32 // single return-address register (manual Sec 3.2)
	systemCycleDebt int32  // system cycles owed from prior overshoot

	// vramWriteStallCycles is bus-contention stall (in system cycles)
	// accumulated since the last TickSystemCycles. It is charged by
	// accumulated since the last TickSystemCycles. Incremented per SH-2
	// or SCU-DMA write to VRAM while drawActive=true; consumed and
	// cleared at the top of TickSystemCycles where it reduces the
	// per-segment cycle budget. Overshoot rolls into systemCycleDebt
	// via the existing carry mechanism. Atomic: writers (CPU/SCU bus
	// goroutines) increment while the VDP walker reads-and-clears, with
	// no common lock between them.
	vramWriteStallCycles atomic.Int32

	// SCU reference for interrupt signaling
	scu *SCU

	// VBE rising-edge latch. The emulator runs all system cycles for a
	// frame before VBlankIn samples TVMR, so games that set VBE=1 then
	// clear it back to 0 within the same frame would otherwise be
	// invisible. We latch any 0->1 transition of TVMR.VBE and treat
	// VBlankIn as if VBE=1, then clear the latch.
	vbeLatched bool

	// Set when the SH-2 writes FBCR with FCT=0 within the current
	// field, distinguishing a game-held FCT=0 at the boundary (Sec
	// 4.2 "manual mode (erase)") from an auto-cleared FCT=0 left
	// over from a swap, which is idle. Consumed at every field
	// boundary: FCM/FCT are last-write-wins, so a later FCT=1 write
	// in the same field replaces the erase mode with a plain change.
	fbcrWritten bool

	// Erase request latch, armed only by an all-zero FBCR write -
	// Sec 4.2's erase encoding ("writing 0 to the VBE, FCM and
	// FCT registers"), which reads as 1-cycle mode at the boundary
	// and so must be remembered as a request rather than a held mode
	// value. It survives a later FCT=1 write in the same field, and
	// while latched every manual change erases the buffer it brings
	// in. Spent by a manual-idle field's one-shot display erase or
	// by a 1-cycle boundary. Software-derived, not hardware-measured.
	eraseRequested bool

	// Set true by VBlankIn when an FB swap actually occurred. Per VDP1
	// manual Sec.4.3, PTM=10 fires "automatically at the start of the
	// frame" where "frame" is the FB-swap interval per Sec.4.2 - so the
	// auto-trigger is gated on a real swap. Consumed at vblank-OUT by
	// the emulator's auto-draw gate. Under double interlace the buffer
	// pointers do not swap but each field is a frame-change per Sec.4.2
	// ("the fields are changed every 1/60 second") so the flag is set
	// every field there too.
	fbSwapped bool

	// Monotonic count of FB swaps that have actually fired. Read by the
	// host UI to derive a "game fps" value distinct from the emulator's
	// RunFrame fps - games can run their internal logic at half rate
	// (or worse) while the host is keeping up at full speed, and only
	// the swap rate reflects what the player actually sees.
	swapCount uint64

	// lateEraseFB is the buffer captured by VBlankIn when a
	// manual-mode erase (FCM=1+FCT=0 with a deferred-bit FBCR write
	// or VBE rising) is requested: the display framebuffer as of the
	// request. Per Sec.4.2 "Erase/write of the display frame buffer
	// is performed in the next specified field": on hardware the
	// erase-write progresses behind the beam, so the next field still
	// shows the pre-erase content, and the buffer is clean before a
	// swap makes it the draw target. The atomic approximation erases
	// it at the next VBlankIn boundary, or earlier at a swap that is
	// about to hand it over for drawing. Nil when no erase is pending.
	lateEraseFB []byte

	// cmdPhase is the resume tag. When non-zero, processCommands
	// dispatches to the matching resume helper instead of fetching a
	// new command. cmdSnapshot stores the cmd-table entry as fetched
	// at command entry; subsequent yields/resumes reuse this snapshot
	// rather than re-reading VRAM.
	cmdPhase    int
	cmdSnapshot vdp1Command

	// Per-command-type resume state. Only the field matching the
	// current cmdPhase is meaningful at any given time. Zeroed on
	// Reset and startDraw.
	spriteResume    spriteResumeState
	distortedResume distortedResumeState
	polygonResume   polygonResumeState
	lineResume      lineResumeState
}

// NewVDP1 creates a new VDP1 instance with VRAM and frame buffers allocated.
func NewVDP1(scu *SCU) *VDP1 {
	return &VDP1{
		vram:      make([]byte, vdp1VRAMSize),
		drawFB:    make([]byte, vdp1FBSize),
		displayFB: make([]byte, vdp1FBSize),
		scu:       scu,
		edsr:      0x0003,
		sysClipX:  319,
		sysClipY:  223,
	}
}

// Reset clears VDP1 registers to power-on defaults. Called during
// CKCHG to match real hardware behavior.
func (v *VDP1) Reset() {
	v.tvmr = 0
	v.fbcr = 0
	v.ptmr = 0
	v.fbcrPending = 0
	v.ptmrPending = 0
	v.edsr = 0x0003
	v.sysClipX = 319
	v.sysClipY = 223
	v.localX = 0
	v.localY = 0
	v.userClipX1 = 0
	v.userClipY1 = 0
	v.userClipX2 = 0
	v.userClipY2 = 0
	v.drawEnd = false
	v.vbeLatched = false
	v.fbcrWritten = false
	v.eraseRequested = false
	v.fbSwapped = false
	// swapCount is intentionally NOT reset here. It's a host-side
	// diagnostic counter consumed by the UI's game-fps metric, not
	// real VDP1 register state. Resetting it on SystemReset (CKCHG
	// or user reset) would cause the host's delta calculation to
	// underflow uint64.
	v.lateEraseFB = nil
	v.procAddr = 0
	v.procReturnAddr = 0
	v.systemCycleDebt = 0
	v.cmdPhase = phaseIdle
	v.cmdSnapshot = vdp1Command{}
	v.spriteResume = spriteResumeState{}
	v.distortedResume = distortedResumeState{}
	v.polygonResume = polygonResumeState{}
	v.lineResume = lineResumeState{}
}

// latchPending copies shadow register values into the active registers.
// Per VDP1 manual Sec.3 "System Register Settings Switch Timing", this
// fires at every field change (vblank-IN), regardless of whether the
// framebuffers actually swap. DIE-mode (no swap) still needs the latch
// every field so DIL/DIE/EOS/EWDR/EWLR/EWRR writes from the SH-2 during
// the previous field become effective for the upcoming draw.
func (v *VDP1) latchPending() {
	// FBCR EOS/DIE/DIL bits (mask 0x1C). FCM/FCT are immediate-active.
	v.fbcr = (v.fbcr &^ 0x1C) | (v.fbcrPending & 0x1C)
	v.ptmr = v.ptmrPending
	v.ewdr = v.ewdrPending
	v.ewlr = v.ewlrPending
	v.ewrr = v.ewrrPending
}

// Read returns the 16-bit register value at the given byte offset.
// Write-only registers return 0.
func (v *VDP1) Read(offset uint32) uint16 {
	switch offset {
	case 0x10:
		return v.edsr
	case 0x12:
		return v.lopr
	case 0x14:
		return v.copr
	case 0x16:
		return v.buildMODR()
	default:
		return 0
	}
}

// Write stores a 16-bit value at the given byte offset.
// Read-only registers (0x10-0x16) ignore writes.
func (v *VDP1) Write(offset uint32, val uint16) {
	switch offset {
	case 0x00:
		// Rising edge of VBE: latch so VBlankIn still triggers the
		// requested V-blank erase even if the SH-2 has already cleared
		// VBE back to 0 within the same frame.
		if (val&0x08) != 0 && (v.tvmr&0x08) == 0 {
			v.vbeLatched = true
		}
		v.tvmr = val
	case 0x02:
		// FBCR EOS/DIE/DIL bits (4/3/2) are deferred per VDP1 manual Sec.3;
		// they latch from pending into active at vblank-IN. FCM/FCT
		// (bits 1/0) are in the "each field" category - kept immediate
		// here, since both interpretations coincide at vblank-IN and
		// existing logic depends on them being readable as soon as a
		// game writes them in the prior field.
		//
		// FCT=0 writes mark fbcrWritten; only the all-zero erase
		// encoding also latches eraseRequested.
		v.fbcrPending = val
		v.fbcr = (v.fbcr &^ 0x03) | (val & 0x03)
		if val&0x01 == 0 {
			v.fbcrWritten = true
		}
		if val&0x03 == 0 {
			v.eraseRequested = true
		}
	case 0x04:
		// Per VDP1 manual Sec.3: PTM=01 changes immediately; PTM=00
		// and PTM=10 change with the switching of the frame buffer.
		// Deferred values land in ptmrPending and are committed into
		// the active ptmr by latchPending() at FB-switch sites.
		v.ptmrPending = val & 0x03
		if val&0x03 == 1 {
			v.ptmr = 1
			v.drawPending = true
			// New draw is starting: CEF (Current End Flag) clears to
			// indicate "drawing is currently in progress, not yet
			// complete". TickSystemCycles will set CEF=1 again when the
			// end-bit is fetched.
			v.edsr &^= 0x02
		}
	case 0x06:
		// EWDR is deferred - latched at vblank-IN.
		v.ewdrPending = val
	case 0x08:
		// EWLR is deferred - latched at vblank-IN.
		v.ewlrPending = val
	case 0x0A:
		// EWRR is deferred - latched at vblank-IN.
		v.ewrrPending = val
	case 0x0C:
		// ENDR has effect only while the command processor is running.
		// Writes while idle are no-ops on hardware; latching here would
		// incorrectly abort the next draw under cycle-accurate timing.
		if v.drawActive {
			v.drawEnd = true
		}
	}
}

// buildMODR constructs the MODR status register. Per VDP1 manual page 57,
// MODR shows the *written* (pending) value of write-only registers, which
// "may be different from the values taken in as internal signals" (the
// active register). Deferred bits/registers therefore source from pending;
// FCM and TVM/VBE are immediate-active so pending == active for them.
func (v *VDP1) buildMODR() uint16 {
	var modr uint16
	modr |= 0x1000                      // VER = 1
	modr |= (v.ptmrPending & 0x02) << 7 // PTM1 -> bit 8
	modr |= (v.fbcrPending & 0x10) << 3 // EOS -> bit 7
	modr |= (v.fbcrPending & 0x08) << 3 // DIE -> bit 6
	modr |= (v.fbcrPending & 0x04) << 3 // DIL -> bit 5
	modr |= (v.fbcr & 0x02) << 3        // FCM -> bit 4
	modr |= v.tvmr & 0x08               // VBE -> bit 3
	modr |= v.tvmr & 0x07               // TVM -> bits 2-0
	return modr
}

// ReadVRAM reads a byte from VDP1 VRAM.
func (v *VDP1) ReadVRAM(addr uint32) uint8 {
	return v.vram[addr&(vdp1VRAMSize-1)]
}

// WriteVRAM writes a byte to VDP1 VRAM.
func (v *VDP1) WriteVRAM(addr uint32, val uint8) {
	v.vram[addr&(vdp1VRAMSize-1)] = val
}

// ReadFB reads a byte from the VDP1 framebuffer. Under DIE=1 it targets
// the parity-pinned FB matching the current DIL setting.
func (v *VDP1) ReadFB(addr uint32) uint8 {
	return v.currentDrawFB()[addr&(vdp1FBSize-1)]
}

// WriteFB writes a byte to the VDP1 framebuffer. Under DIE=1 it targets
// the parity-pinned FB matching the current DIL setting.
func (v *VDP1) WriteFB(addr uint32, val uint8) {
	v.currentDrawFB()[addr&(vdp1FBSize-1)] = val
}

// ReadVRAM16 reads a big-endian 16-bit value from VDP1 VRAM.
func (v *VDP1) ReadVRAM16(addr uint32) uint16 {
	addr &= vdp1VRAMSize - 2
	return uint16(v.vram[addr])<<8 | uint16(v.vram[addr+1])
}

// WriteVRAM16 writes a big-endian 16-bit value to VDP1 VRAM.
func (v *VDP1) WriteVRAM16(addr uint32, val uint16) {
	addr &= vdp1VRAMSize - 2
	v.vram[addr] = uint8(val >> 8)
	v.vram[addr+1] = uint8(val)
}

// ReadVRAM32 reads a big-endian 32-bit value from VDP1 VRAM.
func (v *VDP1) ReadVRAM32(addr uint32) uint32 {
	addr &= vdp1VRAMSize - 4
	return uint32(v.vram[addr])<<24 | uint32(v.vram[addr+1])<<16 |
		uint32(v.vram[addr+2])<<8 | uint32(v.vram[addr+3])
}

// WriteVRAM32 writes a big-endian 32-bit value to VDP1 VRAM.
func (v *VDP1) WriteVRAM32(addr uint32, val uint32) {
	addr &= vdp1VRAMSize - 4
	v.vram[addr] = uint8(val >> 24)
	v.vram[addr+1] = uint8(val >> 16)
	v.vram[addr+2] = uint8(val >> 8)
	v.vram[addr+3] = uint8(val)
}

// InitCommandEndCodes writes a draw-end command word (CMDCTRL with the
// end bit set, 0x8000) into the control word of every 0x20-byte command
// slot across all of VDP1 VRAM.
func (v *VDP1) InitCommandEndCodes() {
	for off := 0; off < vdp1VRAMSize; off += 0x20 {
		v.vram[off] = 0x80
		v.vram[off+1] = 0x00
	}
}

// chargeDrawStall adds `cycles` of draw-contention stall to the VDP1's
// per-segment budget when `addr` lands in VDP1 VRAM during an active
// draw. Per the VDP1 User's Manual Section 2.1 (Address Map, VRAM, p.19)
// VRAM access is arbitrated against drawing (priority: system
// controller > drawing).
func (v *VDP1) chargeDrawStall(addr uint32, cycles int32) {
	if !v.drawActive {
		return
	}
	if m := addr & 0x07FFFFFF; m >= 0x05C00000 && m <= 0x05C7FFFF {
		v.vramWriteStallCycles.Add(cycles)
	}
}

// ReadFB16 reads a big-endian 16-bit value from the VDP1 framebuffer.
// Under DIE=1 it targets the parity-pinned FB matching DIL.
func (v *VDP1) ReadFB16(addr uint32) uint16 {
	addr &= vdp1FBSize - 2
	fb := v.currentDrawFB()
	return uint16(fb[addr])<<8 | uint16(fb[addr+1])
}

// WriteFB16 writes a big-endian 16-bit value to the VDP1 framebuffer.
// Under DIE=1 it targets the parity-pinned FB matching DIL.
func (v *VDP1) WriteFB16(addr uint32, val uint16) {
	addr &= vdp1FBSize - 2
	fb := v.currentDrawFB()
	fb[addr] = uint8(val >> 8)
	fb[addr+1] = uint8(val)
}

// ReadFB32 reads a big-endian 32-bit value from the VDP1 framebuffer.
// Under DIE=1 it targets the parity-pinned FB matching DIL.
func (v *VDP1) ReadFB32(addr uint32) uint32 {
	addr &= vdp1FBSize - 4
	fb := v.currentDrawFB()
	return uint32(fb[addr])<<24 | uint32(fb[addr+1])<<16 |
		uint32(fb[addr+2])<<8 | uint32(fb[addr+3])
}

// WriteFB32 writes a big-endian 32-bit value to the VDP1 framebuffer.
// Under DIE=1 it targets the parity-pinned FB matching DIL.
func (v *VDP1) WriteFB32(addr uint32, val uint32) {
	addr &= vdp1FBSize - 4
	fb := v.currentDrawFB()
	fb[addr] = uint8(val >> 24)
	fb[addr+1] = uint8(val >> 16)
	fb[addr+2] = uint8(val >> 8)
	fb[addr+3] = uint8(val)
}

// DisplayFB returns the framebuffer VDP2 should sample for the given
// field. Under double interlace (DIE=1) each FB stores one parity of
// half-density rows and both are combined to form the frame (manual
// Table 1.2 and Sec 4.2): the even field samples the buffer drawn with
// DIL=0 and the odd field the buffer drawn with DIL=1. Without DIE the
// swap-managed displayFB is used like normal double buffering.
func (v *VDP1) DisplayFB(field int) []byte {
	if v.dieEnabled() {
		if field == 1 {
			return v.displayFB
		}
		return v.drawFB
	}
	return v.displayFB
}

// dieEnabled reports whether double-density interlace draw mode is set
// in FBCR (bit 3). Per the VDP1 manual Table 1.2 the Y coordinate range
// doubles under double interlace (e.g. 352x240 gives 0<=Y<=479) for both
// standard 16bpp and high-resolution 8bpp, so the doubled-Y coordinate
// model follows from DIE=1 alone and is independent of TVM/color depth:
// the game writes Y in 0..2*fbHeight-1 and the hardware halves to the
// physical FB row, selecting the parity given by DIL.
func (v *VDP1) dieEnabled() bool { return v.fbcr&0x08 != 0 }

// fbHeightLogical returns the drawable Y range. Under double interlace
// the game writes Y values up to 2*fbHeight-1 expecting hardware to
// halve them for FB row addressing.
func (v *VDP1) fbHeightLogical() int {
	if v.dieEnabled() {
		return v.fbHeight() * 2
	}
	return v.fbHeight()
}

// dilOdd reports whether DIL selects odd-numbered display lines
// (FBCR bit 2). Only meaningful when dieEnabled() is true.
func (v *VDP1) dilOdd() bool { return v.fbcr&0x04 != 0 }

// currentDrawFB returns the framebuffer that draw commands and CPU
// framebuffer I/O should target. Under double interlace (DIE=1) the
// DIL=1 field is drawn into the alternate buffer; without DIE the
// swap-managed drawFB is used so normal double-buffering works.
func (v *VDP1) currentDrawFB() []byte {
	if v.dieEnabled() && v.dilOdd() {
		return v.displayFB
	}
	return v.drawFB
}

// tvm returns the 3-bit TV mode value from TVMR.
func (v *VDP1) tvm() int {
	return int(v.tvmr & 0x07)
}

// is8bpp returns true when TVM bit 0 is set (TVM=001 or TVM=011).
func (v *VDP1) is8bpp() bool {
	return v.tvmr&0x01 != 0
}

// fbWidth returns the framebuffer width in pixels for the current TVM.
// TVM=001 (hi-res 8-bit) uses 1024 pixels wide; all others use 512.
func (v *VDP1) fbWidth() int {
	if v.tvm() == 1 {
		return 1024
	}
	return 512
}

// fbHeight returns the framebuffer height in pixels for the current TVM.
// TVM=011 (rotation 8-bit) uses 512 pixels tall; all others use 256.
func (v *VDP1) fbHeight() int {
	if v.tvm() == 3 {
		return 512
	}
	return 256
}

// Is8bpp returns true when the framebuffer uses 8-bit pixels.
func (v *VDP1) Is8bpp() bool {
	return v.is8bpp()
}

// PTM returns the current plot trigger mode (PTMR bits 1:0). Used by
// the emulator to detect PTM=10 auto-trigger at V-Blank-IN.
func (v *VDP1) PTM() uint16 {
	return v.ptmr
}

// StartAutoDraw is invoked by the emulator at vblank-OUT when PTM=10
// is set AND an FB swap actually occurred during the prior VBlankIn.
// Per VDP1 manual Sec.4.3 this restarts command processing at address 0;
// an in-progress draw from the previous frame (transfer-over) is
// abandoned.
func (v *VDP1) StartAutoDraw() {
	v.startDraw()
}

// ConsumeFBSwap returns whether VBlankIn performed an FB swap since
// the last consume, and clears the flag. The emulator gates the
// PTM=10 auto-trigger on this per VDP1 manual Sec.4.3.
func (v *VDP1) ConsumeFBSwap() bool {
	s := v.fbSwapped
	v.fbSwapped = false
	return s
}

// SwapCount returns the monotonic count of FB swaps performed since
// reset. Used by the host UI to derive a "game fps" metric distinct
// from the emulator's RunFrame fps.
func (v *VDP1) SwapCount() uint64 {
	return v.swapCount
}

// PerformLateErase performs the pending manual-mode erase on the
// buffer captured at request time. Called by the emulator at the next
// frame's VBlankIn boundary (after that frame's lines have composited
// and before the swap), and by the swap sites when the captured
// buffer is about to become the draw target.
func (v *VDP1) PerformLateErase() {
	if v.lateEraseFB == nil {
		return
	}
	target := v.lateEraseFB
	v.lateEraseFB = nil
	v.eraseFrameBuffer(target)
}

// FBWidth returns the framebuffer width in pixels.
func (v *VDP1) FBWidth() int {
	return v.fbWidth()
}

// FBHeight returns the framebuffer height in pixels.
func (v *VDP1) FBHeight() int {
	return v.fbHeight()
}

// FBRotated returns true when TVMR selects a rotation TV mode
// (TVM=010 rotation 16-bit or TVM=011 rotation 8-bit). Per VDP1
// manual Sec 1.2, in these modes the display framebuffer read
// coordinates come from the VDP2.
func (v *VDP1) FBRotated() bool {
	return v.tvm() == 2 || v.tvm() == 3
}
