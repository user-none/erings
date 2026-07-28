// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"runtime"
	"sync/atomic"

	"github.com/user-none/erings/core/sh2"
)

// Run loop
//
// RunFrame walks every scanline. The work is split across three
// goroutines, kept coherent by two independent layers. Data safety: the
// per-area bus locks (see bus.go) - every shared access claims its
// hardware bus domain for the duration of that access and releases it, so
// goroutines serialize only where the hardware's buses do, at the access
// itself. Time alignment: the two SH-2 clocks are held within
// syncChunkCycles of each other - each publishes its position every chunk
// and spin-waits for the other to reach it before continuing, so master
// and slave never drift more than a chunk apart. The VDP walker is
// aligned more loosely, scanline-granular against the CPUs - it publishes
// once per line and is waited on at line boundaries.
//
//   Main goroutine: master SH-2 steps plus the VDP2 intra-line position
//   per sync chunk, then publishes its position and waits for the slave
//   before the next chunk. Scanline boundaries advance the raster timebase
//   (VDP2.EndLine/EndFrame), which raises the SCU V-blank/H-blank
//   interrupts. Starts a line only when the VDP walker is within
//   vdpSlackLines; the slave is held within syncChunkCycles per chunk.
//
//   Secondary worker (always running): the bucket - SCU, SCSP, CD,
//   and the slave SH-2 (stepped only when SSHEnabled) - advanced in
//   sync chunks, publishing its position and waiting for the master
//   each chunk. Bounded against the VDP walker transitively, through
//   the master.
//
//   VDP worker: ticks VDP1 incremental command processing in tick
//   chunks and streams the VDP2 row render along the dot clock - each
//   chunk renders the pixels the beam covered, so the render cost
//   spreads across the line instead of bursting at a boundary. It
//   starts a line only when both CPUs have completed it, so VDP1
//   never processes a cycle before the CPUs' writes for that line
//   have landed. Frame events fire at their cycle positions: the VDP1
//   frame-buffer change machinery, the VDP2 frame sample, and row 0
//   at the end of line 0 (VDP1's FBCR latch point per manual
//   Sec 4.2); VDP2 BeginLine at each later active line's cycle 0;
//   late erase and VBlankIn at the start of the first vblank line.
//
// VDP2 rendering reads live state: BeginFrame samples only the
// frame-scoped values (geometry, field parity, DISP, rotation
// parameter tables); BeginLine re-snapshots the registers and the
// VDP1 display-FB view for every row, so register writes land on the
// following row; VRAM and the VDP1 framebuffer are read as the spans
// render, so mid-line writes are visible to the row's later pixels.
//
// recalcTiming refreshes per-scanline and per-frame counts whenever
// VDP2 mode changes. fps is held to integer (60 NTSC / 50 PAL) so
// 44,100/fps and 11,289,600/fps give exact integer samples-per-frame
// and m68k-cycles-per-frame. CDBlock.RecalcTiming is fed the actual
// system clock rate so CD-DA and SCDQ pacing tracks the current mode
// rather than a compile-time constant.

// tickChunkCycles is the VDP walker's step cadence within a scanline: it
// ticks VDP1 incremental command processing and advances the VDP2 row
// render in chunks of this many system cycles. It is a render
// granularity, not a synchronization window - the walker waits on the
// CPUs only at scanline boundaries. The two CPUs sync to each other on
// their own cadence (syncChunkCycles); data safety on every shared access
// is the per-area bus locks regardless of chunk size.
const tickChunkCycles = 10

// syncChunkCycles bounds how far the master (main goroutine) and slave
// (secondary worker) SH-2 clocks may drift apart. Each steps this many
// cycles, publishes its position, then spin-waits until the peer has
// reached that position before the next chunk, so neither runs more than
// one chunk ahead of the other. The MINIT/SINIT cross-CPU FRT capture
// rides this barrier (see sh2Barrier): the writer flags it, the receiver
// latches its FRT at the next sync point, within a chunk of the write.
// The VDP walker is not part of this - it stays scanline-granular against
// the CPUs.
const syncChunkCycles = 16

// vdpSlackLines is how far the VDP walker may fall behind the CPUs, in
// scanlines. The walker itself never starts a line the CPUs have not
// completed; this bounds the other direction, letting the CPUs run
// ahead through the walker's render work and host-scheduler stalls.
// While the walker is behind, VDP1 status and interrupts lag by the
// deficit, bounded by this value.
const vdpSlackLines = 2

// barrierSpinLimit is how many times the master/slave rendezvous busy-waits
// for the peer before yielding the goroutine with runtime.Gosched. With at
// least one host thread per worker the peer arrives within the budget and the
// yield is never reached, so the hot path stays a bare atomic spin. When the
// scheduler is oversubscribed (fewer usable threads than workers, or a GC
// worker has taken a P) the budget runs out and the yield lets the awaited
// goroutine run, trading the tight coupling for forward progress.
const barrierSpinLimit = 1 << 12

// RunFrame executes one complete frame of emulation.
func (e *Emulator) RunFrame() {
	smpc := e.smpc
	scsp := e.scsp
	vdp2 := e.vdp2

	// Both workers are parked here, so a deferred system reset can
	// safely reset their components and rewrite the timebase.
	if e.pendingSystemReset.CompareAndSwap(true, false) {
		e.systemReset()
	}
	// Deliver the derred SMPC CKCHG reset
	if scsp.TakePendingReset() {
		scsp.Reset()
	}
	// Deliver the deferred CKCHG master NMI after systemReset, on the
	// master goroutine (see pendingMasterNMI).
	if e.pendingMasterNMI.CompareAndSwap(true, false) {
		e.master.NMIRequest()
	}

	// Prepare audio mix buffer for this frame (stereo interleaved)
	// ~735 samples NTSC, ~882 PAL, plus margin. * 2 for stereo.
	scsp.ResetMixBuffer(900 * 2)

	// Refresh timing in case VDP2 mode changed (CKCHG, TVMD write)
	e.recalcTiming()

	// Hand SCSP the per-frame targets so it emits an exact count
	// regardless of how the frame is sliced.
	scsp.StartFrame(e.systemCyclesPerFrame, e.samplesPerFrame, e.m68kCyclesPerFrame)

	// Reset the progress counters and publish this frame's walk
	// parameters. Both workers are parked here (the previous frame's
	// end waits below guarantee it), and the channel sends order these
	// writes ahead of the workers' reads.
	e.masterCycles.Store(0)
	e.secondaryCycles.Store(0)
	e.vdpCycles.Store(0)
	e.bus.resetContention()
	e.vdp2.ResetRotArm()
	e.frameLineWidth = e.systemCyclesPerScanline
	e.frameScanlines = e.scanlines
	e.frameTotalCycles = int64(e.frameLineWidth) * int64(e.frameScanlines)
	e.vdpWalkActiveLines = vdp2.ActiveLines()
	e.vdpWalkActiveCyc = vdp2.ActiveSystemCyclesPerLine()
	e.vdpWalkWidth = vdp2.ActiveWidth()

	// Kick the VDP worker's frame walk. The workers are spawned by
	// Start; RunFrame only kicks them.
	e.vdpFramesKicked++
	e.vdpJobCh <- struct{}{}

	// Kick the secondary worker. It always runs - the SCU/SCSP/CD
	// components live there; the slave SH-2 stepping inside it is
	// gated on SSHEnabled per chunk.
	e.secondaryJobCh <- struct{}{}

	lineWidth := e.frameLineWidth
	scanlines := e.frameScanlines
	vdpSlack := int64(vdpSlackLines) * int64(lineWidth)
	pos := int64(0)
	for line := uint16(0); line < scanlines; line++ {
		// Line-boundary sync against the VDP walker only: start the line
		// when the walker is within its slack. The slave is held within
		// syncChunkCycles by the per-chunk barrier below, not here.
		vdpWait := pos + int64(lineWidth) - vdpSlack
		for e.vdpCycles.Load() < vdpWait {
			runtime.Gosched()
		}

		// Walk the line in sync chunks: the master steps, the VDP2
		// intra-line position advances, then the barrier publishes the
		// master's position, delivers any pending SINIT capture, and waits
		// for the slave to reach the position before the next chunk.
		cyc := e.masterTASCarry
		e.masterTASCarry = 0
		for cyc < lineWidth {
			chunk := lineWidth - cyc
			if chunk > syncChunkCycles {
				chunk = syncChunkCycles
			}
			for c := uint32(0); c < chunk; c++ {
				e.stepMaster(pos + int64(cyc) + int64(c))
			}
			vdp2.TickSystemCycles(chunk)
			cyc += chunk
			e.sh2Barrier(&e.masterCycles, &e.secondaryCycles, pos+int64(cyc), e.master, &e.sinitPending, e.master.FRTInputCapture)
		}
		// Finish a TAS that straddled the line boundary before the next
		// line's VDP-slack wait (or, on the last line, the frame-end
		// waits): those can block, and blocking while the TAS bus claim is
		// held would deadlock the slave spinning on it. Extra cycles carry
		// into the next line.
		for e.master.InTAS() {
			e.stepMaster(pos + int64(cyc))
			vdp2.TickSystemCycles(1)
			cyc++
		}
		if cyc > lineWidth {
			e.masterTASCarry = cyc - lineWidth
		}
		pos += int64(lineWidth)

		// Scanline boundary: advance the raster timebase. VDP1's
		// V-blank events fire from the VDP worker's cycle walk.
		if line+1 < scanlines {
			vdp2.EndLine()
		} else {
			vdp2.EndFrame()
		}
	}

	// Wait for both workers to finish the frame: after this the
	// framebuffer is complete and both workers are parked on their
	// kick channels.
	for e.secondaryCycles.Load() < e.frameTotalCycles {
		runtime.Gosched()
	}
	for e.vdpWalkDone.Load() != e.vdpFramesKicked {
		runtime.Gosched()
	}

	smpc.TickFrame()

	e.audioBuffer = scsp.MixBuffer()

	// Enable sound CPU at end of frame
	if smpc.SoundEnabled() && scsp.InReset() {
		scsp.SetInReset(false)
	}
}

// stepMaster advances the master SH-2 by one cycle. The load-use stall is
// modeled inside Clock(). frameCyc is the master's frame-relative cycle,
// published to the bus for inter-CPU contention timing. A MINIT write flags
// the cross-CPU FRT capture; the slave latches its own FRT at its next sync
// barrier, within a chunk.
func (e *Emulator) stepMaster(frameCyc int64) {
	e.master.SetFrameCyc(frameCyc)
	state := e.master.Clock()
	if state.Bus == sh2.BusWrite && e.bus.MINITWritten() {
		e.minitPending.Store(true)
	}
}

// stepSlave advances the slave SH-2 by one cycle, mirroring stepMaster: a
// SINIT write flags the master's FRT capture for the master's next barrier.
func (e *Emulator) stepSlave(frameCyc int64) {
	e.slave.SetFrameCyc(frameCyc)
	state := e.slave.Clock()
	if state.Bus == sh2.BusWrite && e.bus.SINITWritten() {
		e.sinitPending.Store(true)
	}
}

// sh2Barrier publishes this goroutine's clock position and spin-waits until the
// peer reaches it, holding the two SH-2 clocks within syncChunkCycles. It
// also delivers a pending cross-CPU FRT input capture: when the peer wrote
// MINIT/SINIT since the last barrier, the receiving CPU latches its own
// FRT here, on its own goroutine, within a chunk of the write - the tight
// barrier supplies the timing that the loose-drift design needed an
// explicit rendezvous and response window for. The wait is skipped while
// this CPU is mid-TAS (it holds a bus claim across cycles that the peer
// would otherwise spin on at this barrier) and at the frame end (where the
// peer's clock caps and is free to reset); RunFrame's frame-end wait
// covers the boundary.
func (e *Emulator) sh2Barrier(pub, peer *atomic.Int64, myPos int64, cpu *sh2.CPU, pending *atomic.Bool, capture func()) {
	pub.Store(myPos)
	if pending.CompareAndSwap(true, false) {
		capture()
	}
	spins := 0
	for !cpu.InTAS() && myPos < e.frameTotalCycles && peer.Load() < myPos {
		spins++
		if spins >= barrierSpinLimit {
			runtime.Gosched()
			spins = 0
		}
	}
}

// vdpWorker services one frame walk per kick, parking on the job
// channel between frames.
func (e *Emulator) vdpWorker() {
	for range e.vdpJobCh {
		e.walkVDPFrame()
	}
}

// walkVDPFrame cycle-walks the frame in chunks of at most
// tickChunkCycles, starting each line only after both CPUs have
// completed it - VDP1 stays strictly behind the CPUs at line
// granularity, with no waits inside the line. Per chunk it ticks VDP1
// incremental command processing and streams the VDP2 row: the render
// cursor follows the dot clock (one pixel per activeCyc/width cycles
// of the line's active period), so each chunk renders the few pixels
// the beam covered and the row completes as the active period ends.
// Progress publishes once per line.
//
// Frame events fire at their cycle positions:
//
//	End of line 0 (the first H-blank IN after V-blank OUT): VDP1's
//	FBCR latch point per manual Sec 4.2 - VBlankOut swap evaluation,
//	deferred-register latch, the PTM=10 auto-draw gate, the VDP2
//	frame-scoped sample (BeginFrame), and row 0's render in one
//	burst from the post-change state.
//
//	Each later active line's cycle 0: BeginLine - the row's register
//	snapshot and per-line setup, and the VDP1 display-FB view, taken
//	together so the buffer choice and its pixel format stay coherent
//	with the row's registers.
//
//	First vblank line, cycle 0: the manual-mode erase deferred from
//	the PREVIOUS VBlankIn flushes - this frame's rows have
//	composited, and per Sec.4.2 the erase-write progresses behind the
//	beam during the displayed field - then VBlankIn fires (swap,
//	BEF/CEF latch).
//
// vdpWalkDone signals frame completion to main's frame-end wait.
func (e *Emulator) walkVDPFrame() {
	vdp1 := e.vdp1
	vdp2 := e.vdp2
	lineWidth := int64(e.frameLineWidth)
	activeLines := int64(e.vdpWalkActiveLines)
	cpp := int64(4)
	if w := int64(e.vdpWalkWidth); w > 0 && int64(e.vdpWalkActiveCyc) >= w {
		cpp = int64(e.vdpWalkActiveCyc) / w
	}
	fbView := func() vdp1FBView {
		return vdp1FBView{
			data:    vdp1.DisplayFB(vdp2.FrameFieldBit()),
			is8bpp:  vdp1.Is8bpp(),
			width:   vdp1.FBWidth(),
			height:  vdp1.FBHeight(),
			rotated: vdp1.FBRotated(),
		}
	}
	for pos := int64(0); pos < e.frameTotalCycles; {
		line := pos / lineWidth
		lineCyc := pos - line*lineWidth
		if lineCyc == 0 {
			// Line-boundary sync: walk this line only after both CPUs
			// have completed it.
			wait := pos + lineWidth
			for e.masterCycles.Load() < wait || e.secondaryCycles.Load() < wait {
				runtime.Gosched()
			}
			if line == 1 {
				// First H-blank IN after V-blank OUT: VDP1's FBCR
				// latch point. Per VDP1 manual Sec 4.2, FCM/FCT
				// settings are to be made immediately after the
				// V-blank OUT interrupt, and FBCR access is
				// prohibited from the first H-blank IN after V-blank
				// OUT until the next H-blank IN - the window in which
				// VDP1 latches and processes the register. So the
				// manual-change swap, the deferred-register latch,
				// and the PTM=10 auto-draw gate evaluate here, one
				// line after the interrupt: writes the V-blank OUT
				// handler makes during line 0 are included, and the
				// handler reads pre-swap state (e.g. EDSR.CEF) before
				// the change lands. The changed display buffer
				// applies to this field, so row 0 renders here in one
				// burst from the post-change state; later rows
				// stream along the dot clock.
				vdp1.VBlankOut()
				if vdp1.PTM() == 2 && vdp1.ConsumeFBSwap() {
					vdp1.StartAutoDraw()
				}
				vdp2.BeginFrame()
				if activeLines > 0 {
					vdp2.BeginLine(0, fbView())
					vdp2.RenderTo(int(e.vdpWalkWidth))
				}
			}
			if line >= 1 && line < activeLines {
				vdp2.BeginLine(int(line), fbView())
			}
			if line == activeLines {
				vdp1.PerformLateErase()
				vdp1.VBlankIn()
			}
		}
		next := pos + tickChunkCycles
		if lineEnd := (line + 1) * lineWidth; next > lineEnd {
			next = lineEnd
		}
		vdp1.TickSystemCycles(uint32(next - pos))
		if line >= 1 && line < activeLines {
			vdp2.RenderTo(int((next - line*lineWidth) / cpp))
		}
		if next-(line*lineWidth) == lineWidth {
			e.vdpCycles.Store(next)
		}
		pos = next
	}
	e.vdpWalkDone.Add(1)
}

// secondaryWorker services one frame walk per kick, parking on the job
// channel between frames.
func (e *Emulator) secondaryWorker() {
	for range e.secondaryJobCh {
		e.walkSecondaryFrame()
	}
}

// walkSecondaryFrame walks the secondary bucket through the frame. Within a
// line everything in the bucket advances in sync chunks, and after each
// chunk the bucket publishes its position and waits for the master to reach
// it, holding the slave within syncChunkCycles of the master. The bucket is
// the home of the slave SH-2/SCU/SCSP/CD components. The slave SH-2 stepping
// is gated on SSHEnabled. It is bounded against the VDP walker transitively,
// through the master's wait.
func (e *Emulator) walkSecondaryFrame() {
	smpc := e.smpc
	scsp := e.scsp
	lineWidth := int64(e.frameLineWidth)
	for pos := int64(0); pos < e.frameTotalCycles; {
		// SMPC command-dispatch tick, once per scanline. The areaSMPC
		// claim serializes it against register access. The System Manager
		// raise is done after release to keep the SMPC -> SCU order. The
		// slave is held within syncChunkCycles of the master by the
		// per-chunk barrier, so no line-boundary wait is needed here.
		e.bus.lockArea(areaSMPC)
		raiseSysMgr := smpc.TickScanline()
		e.bus.unlockArea(areaSMPC)
		if raiseSysMgr {
			e.scu.RaiseSystemManager()
		}

		lineEnd := pos + lineWidth
		for pos < lineEnd {
			chunk := int64(syncChunkCycles)
			if rem := lineEnd - pos; chunk > rem {
				chunk = rem
			}
			if smpc.SSHEnabled() {
				c := e.slaveTASCarry
				e.slaveTASCarry = 0
				for ; c < chunk; c++ {
					e.stepSlave(pos + c)
				}
				// Finish a TAS that straddled the chunk boundary before
				// the component ticks below. Prevents deadlock if one
				// of the other components needs the bus.
				for e.slave.InTAS() {
					e.stepSlave(pos + c)
					c++
				}
				if c > chunk {
					e.slaveTASCarry = c - chunk
				}
			}
			w := uint32(chunk)
			e.scu.TickSystemCycles(w)
			if !smpc.SoundEnabled() && !scsp.InReset() {
				scsp.SetInReset(true)
			}
			scsp.TickSystemCycles(w)
			e.cdblock.TickSystemCycles(w)
			pos += chunk
			e.sh2Barrier(&e.secondaryCycles, &e.masterCycles, pos, e.slave, &e.minitPending, e.slave.FRTInputCapture)
		}
	}
}
