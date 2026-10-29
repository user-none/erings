// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"runtime"
	"sync/atomic"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
)

const (
	vdp1VRAMSize = 512 * 1024 // 512 KB
	vdp1FBSize   = 256 * 1024 // 256 KB
)

// vdp1VRAMPortWordCycles is the system cycles to transfer one 16-bit
// word over the VDP1's VRAM port: the parameter read (command table
// fetch) and the pattern read (character/texture data), both VDP1
// User's Manual Sec 2.1: "performed in a short period of time using
// burst transfer". One system clock per word is the 16-bit port's
// peak transfer rate.
const vdp1VRAMPortWordCycles = 1

// vdp1CmdFetchWords is the number of words the parameter read
// transfers per command. A command table occupies 32 bytes (16
// words), but VDP1 User's Manual Sec 5.4, command table figure: the
// last word is "(Dummy) Skipped during table fetch", so only 15
// words are actually read. Table 2.2 gives the same figure directly
// as the command size, 1EH (30 bytes).
const vdp1CmdFetchWords = 15

// vdp1CLUTWords is the number of words the pattern read transfers for
// the color lookup table in color mode 1, VDP1 User's Manual Sec 5.2:
// 16 colors of one word each. Table 2.2 gives the same size, 20H (32
// bytes).
const vdp1CLUTWords = 16

// vdp1GouraudWords is the number of words read from the Gouraud
// shading table when Gouraud shading is enabled, VDP1 User's Manual
// Sec 6.8: "8H bytes (4 words)". Table layout in Sec 5.3.
const vdp1GouraudWords = 4

// vdp1WriteStallSystemCycles is the system cycles drawing stalls for
// each 16-bit VRAM word a CPU access takes during drawing. A 32-bit
// access counts as two words. VDP1 User's Manual Sec 2.1 (VRAM)
// documents that CPU access is arbitrated with priority over drawing
// and interrupts it, but gives no figure for how long drawing loses.
// Its "more than 10 wait cycles" describes the CPU-side wait (see
// vdp1DrawAccessWaitCycles), and its burst-transfer statement implies
// the port transaction itself is short.
//
// The value is therefore an empirical calibration, not spec-derived.
// It must be large enough that drawing stays behind an SH-2 still
// streaming its command list into VRAM; when drawing catches up, it
// reads a stale end bit left over from an earlier shorter list and
// stops the command list early with the tail sprites dropped. 16 is
// the measured no-flicker value with writes posting through the
// write buffer; 16 flickered when writes also paid a CPU-side
// arbitration wait.
const vdp1WriteStallSystemCycles = 16

// vdp1DrawAccessWaitCycles is the extra wait cycles a CPU VRAM read
// pays while drawing is in progress, for arbitrating the shared port
// with drawing, VDP1 User's Manual Sec 2.1: "more than 10 wait
// cycles". Added on top of the read's normal region cost, which
// already includes ~5 B-Bus cycles, so the wait during drawing
// totals ~13 cycles, above the manual's floor. Writes post through
// the write buffer and continue without waiting; they pay only the
// draw-side stall.
const vdp1DrawAccessWaitCycles = 8

// vdp1DMABurstStallPerWord is the bus-contention penalty (in system
// cycles) charged to VDP1's per-segment cycle budget for each 16-bit
// B-Bus word of a bus-master burst (SCU-DMA) into VRAM while
// drawActive=true. Per the VDP1 User's Manual Section 2.1 (Address Map,
// VRAM) a DMA burst transfers continuously in a short period, so a
// word costs far less than the per-access arbitration wait an SH-2 single
// store pays. The B-Bus is 16-bit and a 32-bit write moves two words. At
// one cycle per word a full burst stalls the draw by bytes/2 system
// cycles - bounded by the transfer size, not by per-word arbitration.
const vdp1DMABurstStallPerWord = 1

// vdp1PixelCycles is the system cycles to draw one pixel of a part.
// Drawing writes "1 pixel ... in sync with" the system clock, VDP1
// User's Manual Sec 2.1, so most modes cost one cycle.
//
// Shadow (cc=1), half-transparency (cc=3), and Gouraud shading +
// half-transparency (cc=7) read the destination pixel back from the
// frame buffer before writing. Mode 7 also tests its MSB to select
// processing. For these, Sec 6.3, Color Calculation: "drawing ...
// takes six times longer than when color calculation is not
// performed".
func vdp1PixelCycles(cc uint16) int32 {
	switch cc {
	case 1, 3, 7:
		return 6
	}
	return 1
}

// Command phase tags: phaseIdle means no command in progress;
// phaseDraw a drawing command in progress, resumed through its draw
// state on the next processCommands entry.
const (
	phaseIdle = 0
	phaseDraw = 1
)

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
	// latchPending() copies pending into active at the frame change
	// (VBlankIn, the first H-blank IN after V-blank OUT). MODR
	// readback also sources from these
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

	// vramWriteStallCycles is the draw stall (in system cycles)
	// accumulated since the last TickSystemCycles. It is added per
	// SH-2 access (chargeDrawStallBounded) or SCU-DMA write
	// (chargeDrawStall) to VRAM while drawActive=true, then consumed
	// and cleared at the top of TickSystemCycles, where it reduces
	// the per-segment cycle budget. Overshoot rolls into
	// systemCycleDebt via the existing carry mechanism. Atomic: the
	// CPU/SCU bus goroutines add to it while the VDP walker reads and
	// clears it, with no common lock between them.
	vramWriteStallCycles atomic.Int32

	// drawTicked and drawStallCharged implement the per-draw cap in
	// chargeDrawStallBounded. The stall charged to one draw cannot
	// exceed the cycles that draw has run. drawTicked is the cycles
	// drawing has advanced since startDraw and drawStallCharged is the
	// stall already charged against it.
	drawTicked       atomic.Int64
	drawStallCharged atomic.Int64

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
	// the emulator's auto-draw gate.
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

	// The drawing command in progress: its op and its progress
	// through Draw. Meaningful while cmdPhase is phaseDraw. Zeroed on
	// Reset.
	cmd drawResume

	// The line records (vdp1_ops.go): the buffer and the words the
	// frame uses.
	lineRecords compute.LineRecords
	recordWords uint32

	// The frame's list (vdp1_ops.go): the entries of the drawing
	// commands' ops and the erases, in the walk's order, each with
	// its ordinal in Seq and the physical frame buffer it draws into
	// in Buffer. opSeq is the next ordinal; it restarts with the list
	// at the end of the display and keeps counting across a save
	// state load. cmdOpen marks the last entry as the open one of the
	// command in progress, closed at the end of every tick. opsTaken
	// is the entries the renderer has taken, in the list's order; it
	// restarts with the list, so a list dropped at a reset drops its
	// untaken entries with it. fb0 is the frame buffer allocation the
	// physical index 0 names.
	ops      []draw.DrawOp
	opSeq    uint32
	cmdOpen  bool
	opsTaken int
	fb0      []byte

	// vramDirty marks the VRAM pages written since a renderer last
	// took them (page delivery, vdp2_delta.go). Always on.
	vramDirty pageDirty

	// The CPU's frame buffer writes, by physical buffer, for a
	// renderer that draws its own pair: fbShadow holds the last byte
	// the CPU wrote at each address, fbMask one bit per byte the CPU
	// has written since a renderer last took it, and fbDirty the
	// pages with such bytes. The drawing never touches them, so a
	// take carries the CPU's bytes alone. Maintained while
	// fbWritesTaken is set.
	fbShadow [2][]byte
	fbMask   [2][vdp1FBSize / 64]atomic.Uint64
	fbDirty  [2]pageDirty

	// Set while a renderer takes the CPU's frame buffer writes into a
	// pair of its own (the GPU renderer's and the enhanced renderer's
	// open and close): the shadow, the masks, and the dirty maps
	// above are maintained only then, since only such a take reads
	// them. The renderer is selected before emulation starts and
	// only ever falls back to the accurate software renderer, so the
	// flag never turns on with writes to catch up on.
	fbWritesTaken bool

	// Whether the walk keeps the CPU pair: the drawing commands store
	// their dots and the frame change erases. A renderer that draws
	// the pair on a device clears it, and the walk then charges every
	// command's cycles without drawing it (draw.DrawState.Timing). A
	// CPU read of the frame buffer while the pair is not kept raises
	// pairRequest and waits (waitPair) for the walker to bring the
	// pair up to date from the device pair and set pairKept for the
	// rest of the session (Emulator.servicePair).
	pairKept    atomic.Bool
	pairRequest atomic.Bool
}

// vdp1VRAMPages and vdp1FBPages are the pages of VDP1 VRAM and of a
// frame buffer.
const (
	vdp1VRAMPages = vdp1VRAMSize / compute.PageBytes
	vdp1FBPages   = vdp1FBSize / compute.PageBytes
)

// NewVDP1 creates a new VDP1 instance with VRAM and frame buffers allocated.
func NewVDP1(scu *SCU) *VDP1 {
	v := &VDP1{
		vram:        make([]byte, vdp1VRAMSize),
		drawFB:      make([]byte, vdp1FBSize),
		displayFB:   make([]byte, vdp1FBSize),
		scu:         scu,
		edsr:        0x0003,
		sysClipX:    319,
		sysClipY:    223,
		lineRecords: make(compute.LineRecords, vdp1RecordCapacity),
	}
	v.ops = make([]draw.DrawOp, 0, vdp1OpCapacity)
	v.fb0 = v.drawFB
	v.vramDirty.markAll(vdp1VRAMPages)
	for i := range v.fbShadow {
		v.fbShadow[i] = make([]byte, vdp1FBSize)
	}
	v.pairKept.Store(true)
	return v
}

// setPairKept sets whether the walk keeps the CPU pair, for the
// command in progress too.
func (v *VDP1) setPairKept(kept bool) {
	v.pairKept.Store(kept)
	v.cmd.st.Timing = !kept
}

// waitPair makes the CPU pair current before a CPU read of the frame
// buffer when the walk is not keeping it: the read raises the request
// and spins until the walker has answered it, which it does at its
// line wait, since the reading CPU cannot finish the line first. The
// caller holds the B-Bus area, so no other master reaches the frame
// buffer meanwhile.
func (v *VDP1) waitPair() {
	if v.pairKept.Load() {
		return
	}
	v.pairRequest.Store(true)
	spins := 0
	for !v.pairKept.Load() {
		spins++
		if spins >= barrierSpinLimit {
			runtime.Gosched()
			spins = 0
		}
	}
}

// noteFBWrite records a CPU write of n bytes at addr of the draw
// buffer, aligned to its width, for a renderer's take, and nothing
// when no renderer takes the writes: the bytes into the shadow when the
// write landed in the pair (fbWriteTarget), their bits into the mask,
// one word since a page is a multiple of every width, and the page
// into the dirty map, in that order, so a take that sees a bit sees
// its byte, and a bit set after the take's mask swap is kept for the
// next take with its page marked.
func (v *VDP1) noteFBWrite(addr, n uint32) {
	if !v.fbWritesTaken {
		return
	}
	h := v.physicalBuffer(v.drawFB)
	if v.pairKept.Load() {
		copy(v.fbShadow[h][addr:addr+n], v.drawFB[addr:addr+n])
	}
	v.fbMask[h][addr>>6].Or((uint64(1)<<n - 1) << (addr & 63))
	v.fbDirty[h].mark(addr / compute.PageBytes)
}

// fbWriteTarget is the buffer a CPU frame buffer write lands in: the
// draw buffer while the walk keeps the pair, else the draw buffer's
// shadow alone, since nothing reads the pair until a sync overwrites
// it from the device.
func (v *VDP1) fbWriteTarget() []byte {
	if !v.fbWritesTaken || v.pairKept.Load() {
		return v.drawFB
	}
	return v.fbShadow[v.physicalBuffer(v.drawFB)]
}

// resyncFB marks every byte of both frame buffers as written by the
// CPU, so a renderer taking the writes into its own pair takes the
// whole pair at its next take: the buffers were replaced outside the
// write funnel (save state load, a renderer's open).
func (v *VDP1) resyncFB() {
	for h := range v.fbShadow {
		v.resyncFBHalf(uint32(h))
	}
}

// resyncFBHalf marks every byte of frame buffer half h as written by
// the CPU (resyncFB), so a renderer takes the whole half at its next
// take: the renderer changed how it lays the half out.
func (v *VDP1) resyncFBHalf(h uint32) {
	if !v.fbWritesTaken {
		return
	}
	copy(v.fbShadow[h], v.physicalFB(h))
	for i := range v.fbMask[h] {
		v.fbMask[h][i].Store(^uint64(0))
	}
	v.fbDirty[h].markAll(vdp1FBPages)
}

// takeFBPages takes the pages of frame buffer half h the CPU has
// written since the last take into a renderer's page store from slot:
// each page's number into table, its mask into masks as two words per
// 64 bytes, the low word first, and its shadow bytes into store, in
// page order. A mask is cleared as it is read, so a bit set after the
// read is kept for the next take with its page marked. It returns the
// slot after the last page and the scratch slice it took the pages
// into, grown when the take needed more.
func (v *VDP1) takeFBPages(h uint32, slot int, table, masks []uint32, store []byte, scratch []uint32) (int, []uint32) {
	scratch = v.fbDirty[h].take(scratch[:0])
	shadow := v.fbShadow[h]
	for _, p := range scratch {
		table[slot] = p
		words := v.fbMask[h][p*compute.PageBytes/64 : (p+1)*compute.PageBytes/64]
		for i := range words {
			m := words[i].Swap(0)
			masks[slot*(compute.PageBytes/32)+2*i] = uint32(m)
			masks[slot*(compute.PageBytes/32)+2*i+1] = uint32(m >> 32)
		}
		copy(store[slot*compute.PageBytes:], shadow[p*compute.PageBytes:(p+1)*compute.PageBytes])
		slot++
	}
	return slot, scratch
}

// physicalBuffer is the index of a frame buffer allocation, 0 for
// the one held at creation as fb0 and 1 for the other. The frame
// change swaps the buffers' roles, never the allocations.
func (v *VDP1) physicalBuffer(fb []byte) uint32 {
	if &fb[0] == &v.fb0[0] {
		return 0
	}
	return 1
}

// physicalFB is the frame buffer allocation at physical index i.
func (v *VDP1) physicalFB(i uint32) []byte {
	if (i == 0) == (&v.drawFB[0] == &v.fb0[0]) {
		return v.drawFB
	}
	return v.displayFB
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
	v.cmd = drawResume{}
	// The list's entries belong to the draw the reset ends: none is
	// drawn after it.
	v.restartList()
}

// latchPending copies shadow register values into the active registers.
// Per VDP1 manual Sec.3 "System Register Settings Switch Timing", this
// fires at every frame change (VBlankIn, the first H-blank IN after
// V-blank OUT), regardless of whether the framebuffers actually swap,
// so DIL/DIE/EOS/EWDR/EWLR/EWRR writes from the SH-2 during the
// previous field become effective for the upcoming draw.
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
		// they latch from pending into active at the frame change. FCM/FCT
		// (bits 1/0) are in the "each field" category - kept immediate
		// here, since both interpretations coincide at the frame change and
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
		// EWDR is deferred - latched at the frame change.
		v.ewdrPending = val
	case 0x08:
		// EWLR is deferred - latched at the frame change.
		v.ewlrPending = val
	case 0x0A:
		// EWRR is deferred - latched at the frame change.
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
	addr &= vdp1VRAMSize - 1
	v.vram[addr] = val
	v.vramDirty.mark(addr / compute.PageBytes)
}

// ReadFB reads a byte from the VDP1 framebuffer. CPU access reaches
// the draw buffer: per VDP1 manual Sec 4.2 only the side being drawn
// can be accessed, the displayed side cannot. A read waits for the
// pair to be current (waitPair).
func (v *VDP1) ReadFB(addr uint32) uint8 {
	v.waitPair()
	return v.drawFB[addr&(vdp1FBSize-1)]
}

// WriteFB writes a byte to the VDP1 framebuffer, the draw buffer
// (fbWriteTarget).
func (v *VDP1) WriteFB(addr uint32, val uint8) {
	addr &= vdp1FBSize - 1
	v.fbWriteTarget()[addr] = val
	v.noteFBWrite(addr, 1)
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
	v.vramDirty.mark(addr / compute.PageBytes)
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
	v.vramDirty.mark(addr / compute.PageBytes)
}

// InitCommandEndCodes writes a draw-end command word (CMDCTRL with the
// end bit set, 0x8000) into the control word of every 0x20-byte command
// slot across all of VDP1 VRAM.
func (v *VDP1) InitCommandEndCodes() {
	for off := 0; off < vdp1VRAMSize; off += 0x20 {
		v.vram[off] = 0x80
		v.vram[off+1] = 0x00
	}
	v.vramDirty.markAll(vdp1VRAMPages)
}

// chargeDrawStall adds `cycles` of draw-contention stall to the VDP1's
// per-segment budget when `addr` lands in VDP1 VRAM during an active
// draw. Per the VDP1 User's Manual Section 2.1 (Address Map, VRAM)
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

// drawingVRAMAccess reports whether addr targets VDP1 VRAM while a
// draw is active.
func (v *VDP1) drawingVRAMAccess(addr uint32) bool {
	if !v.drawActive {
		return false
	}
	m := addr & 0x07FFFFFF
	return m >= 0x05C00000 && m <= 0x05C7FFFF
}

// chargeDrawStallBounded stalls drawing by vdp1WriteStallSystemCycles
// per 16-bit word of a CPU VRAM access during drawing, capped at the
// cycles the draw has run since startDraw. A shared port cannot take
// more time from drawing than has actually elapsed.
//
// A draw that keeps advancing stays ahead of the cap, so occasional
// accesses charge the full penalty. A dense access stream that pins
// drawing in place is held to the elapsed time instead, and drawing
// resumes as soon as the stream stops.
//
// Callers must hold the areaBBus lock: it serializes the two SH-2s
// here, so the slack check and the charge are atomic against the
// other CPU. The VDP walker advances drawTicked concurrently without
// the lock, but it only grows, which can only under-count the slack.
func (v *VDP1) chargeDrawStallBounded(addr uint32, words int32) {
	if !v.drawingVRAMAccess(addr) {
		return
	}
	charge := int64(words) * vdp1WriteStallSystemCycles
	if slack := v.drawTicked.Load() - v.drawStallCharged.Load(); slack < charge {
		charge = slack
	}
	if charge > 0 {
		v.drawStallCharged.Add(charge)
		v.vramWriteStallCycles.Add(int32(charge))
	}
}

// ReadFB16 reads a big-endian 16-bit value from the VDP1 framebuffer,
// the draw buffer.
func (v *VDP1) ReadFB16(addr uint32) uint16 {
	v.waitPair()
	addr &= vdp1FBSize - 2
	fb := v.drawFB
	return uint16(fb[addr])<<8 | uint16(fb[addr+1])
}

// WriteFB16 writes a big-endian 16-bit value to the VDP1 framebuffer,
// the draw buffer (fbWriteTarget).
func (v *VDP1) WriteFB16(addr uint32, val uint16) {
	addr &= vdp1FBSize - 2
	fb := v.fbWriteTarget()
	fb[addr] = uint8(val >> 8)
	fb[addr+1] = uint8(val)
	v.noteFBWrite(addr, 2)
}

// ReadFB32 reads a big-endian 32-bit value from the VDP1 framebuffer,
// the draw buffer.
func (v *VDP1) ReadFB32(addr uint32) uint32 {
	v.waitPair()
	addr &= vdp1FBSize - 4
	fb := v.drawFB
	return uint32(fb[addr])<<24 | uint32(fb[addr+1])<<16 |
		uint32(fb[addr+2])<<8 | uint32(fb[addr+3])
}

// WriteFB32 writes a big-endian 32-bit value to the VDP1 framebuffer,
// the draw buffer (fbWriteTarget).
func (v *VDP1) WriteFB32(addr uint32, val uint32) {
	addr &= vdp1FBSize - 4
	fb := v.fbWriteTarget()
	fb[addr] = uint8(val >> 24)
	fb[addr+1] = uint8(val >> 16)
	fb[addr+2] = uint8(val >> 8)
	fb[addr+3] = uint8(val)
	v.noteFBWrite(addr, 4)
}

// DisplayFB returns the framebuffer VDP2 samples: the display buffer
// the last frame change brought in, complete when the change swapped
// it in. Under double interlace the buffers still swap every field
// (manual Sec 4.2, FCM=FCT=0), so the displayed buffer holds the
// previous field's draw of the parity its DIL selected, and the two
// fields' buffers make one frame (manual Figure 4.1).
func (v *VDP1) DisplayFB() []byte {
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
