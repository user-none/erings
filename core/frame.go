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
// its completed-line count and is waited on at line boundaries.
//
//   Main goroutine: master SH-2 steps plus the VDP2 intra-line position
//   per sync chunk (the intra-line tick raises SCU H-blank IN as the
//   line enters horizontal blanking), then publishes its position and
//   waits for the slave before the next chunk. Scanline boundaries
//   advance the raster timebase (VDP2.EndLine/EndFrame), which raises
//   the SCU V-blank interrupts. Starts a line only when the VDP walker
//   trails by no more than vdpSlackLines; the slave is held within
//   syncChunkCycles per chunk.
//
//   Secondary worker (always running): the bucket - SCU, SCSP, CD,
//   and the slave SH-2 (stepped only when SSHEnabled) - advanced in
//   sync chunks, publishing its position and waiting for the master
//   each chunk. Bounded against the VDP walker transitively, through
//   the master.
//
//   VDP worker: walks the frame a line at a time - it advances VDP1
//   incremental command processing by the line's cycles and renders
//   the VDP2 row at the line's end. It starts a line only when both
//   CPUs have completed it, so VDP1 never processes a cycle before
//   the CPUs' writes for that line have landed. Frame events fire at
//   their line positions: the VDP1
//   frame change (register take-in, swap or parity erase, EDSR), the
//   VDP2 frame sample, and row 0 at the end of line 0 (VDP1's FBCR
//   take-in point per manual Sec 4.2); VDP2 BeginLine at each later
//   active line's cycle 0; the deferred manual-mode erase at the
//   start of the first vblank line.
//
// VDP2 rendering reads live state: BeginFrame samples only the
// frame-scoped values (geometry, field parity, DISP, rotation
// parameter tables); BeginLine re-snapshots the registers and the
// VDP1 display-FB view for every row, so register writes land on the
// following row; VRAM and the VDP1 framebuffer are read as the row
// renders at its line's end.
//
// recalcTiming refreshes per-scanline and per-frame counts whenever
// VDP2 mode changes. fps is held to integer (60 NTSC / 50 PAL) so
// 44,100/fps and 11,289,600/fps give exact integer samples-per-frame
// and m68k-cycles-per-frame. CDBlock.RecalcTiming is fed the actual
// system clock rate so CD-DA and SCDQ pacing tracks the current mode
// rather than a compile-time constant.

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

// barrierSpinLimit is how many times a frame-walk wait busy-waits before
// yielding the goroutine with runtime.Gosched. It applies to every wait
// loop in the walk: the master/slave rendezvous, the line and frame-end
// waits, and the VDP walker's line wait.
//
// The budget is chosen once at startup from GOMAXPROCS, because the two
// regimes want opposite values. With a host thread for each of the
// three workers plus slack for the runtime and host app (4+), a large
// budget means normal waits resolve while still spinning and never
// yield - each yield in that regime wakes an idle OS thread, and at
// wait frequency those wakes dominate CPU. With fewer threads than
// workers, every wait is a starvation wait: the awaited goroutine is
// not running, the budget is dead time before the yield lets it run,
// and a small budget's microsecond-scale yield cadence timeslices the
// workers instead (yields are also cheap there - with no idle thread
// to wake, Gosched is just a requeue).
var barrierSpinLimit = spinLimitForProcs(runtime.GOMAXPROCS(0))

// spinLimitForProcs picks the frame-walk spin budget for the number of
// usable host threads.
func spinLimitForProcs(procs int) int {
	if procs >= 4 {
		return 1 << 20
	}
	return 1 << 12
}

// spinWait holds until ctr reaches target: a bare spin for
// barrierSpinLimit iterations, then a runtime.Gosched yield, repeated.
// The frame walk's line and frame-end waits use this; the master/slave
// rendezvous has its own loop in sh2Barrier with its gating conditions.
func spinWait(ctr *atomic.Int64, target int64) {
	spins := 0
	for ctr.Load() < target {
		spins++
		if spins >= barrierSpinLimit {
			runtime.Gosched()
			spins = 0
		}
	}
}

// waitLineEnd waits for both CPUs to complete the line ending at
// target, answering a CPU's request for the frame buffer pair while
// it waits (servicePair): the requesting CPU is spinning inside its
// read and cannot finish the line until the request is answered.
func (e *Emulator) waitLineEnd(target int64, line int) {
	spins := 0
	for e.masterCycles.Load() < target || e.secondaryCycles.Load() < target {
		if e.vdp1.pairRequest.Load() {
			e.servicePair(line)
		}
		spins++
		if spins >= barrierSpinLimit {
			runtime.Gosched()
			spins = 0
		}
	}
}

// servicePair answers a CPU read's request for the CPU pair
// (VDP1.waitPair), on the walker between ticks: the renderer brings
// the pair up to date from the device pair through line, and the
// walk keeps the pair from here on, the command in progress
// included, so the read and every later one are served from it.
func (e *Emulator) servicePair(line int) {
	e.renderer.syncPair(line)
	e.vdp1.setPairKept(true)
	e.vdp1.pairRequest.Store(false)
}

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
	// Deliver the deferred SMPC CKCHG reset
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
	e.vdpLinesWalked.Store(0)
	e.vdp2.ResetRotArm()
	e.frameCyclesPerScanline = e.systemCyclesPerScanline
	e.frameScanlines = e.scanlines
	e.frameTotalCycles = int64(e.frameCyclesPerScanline) * int64(e.frameScanlines)
	e.vdpWalkActiveLines = vdp2.ActiveLines()

	// Kick the VDP worker's frame walk. The workers are spawned by
	// Start; RunFrame only kicks them.
	e.vdpJobChBegin <- struct{}{}

	// Kick the secondary worker. It always runs - the SCU/SCSP/CD
	// components live there; the slave SH-2 stepping inside it is
	// gated on SSHEnabled per chunk.
	e.secondaryJobCh <- struct{}{}

	lineWidth := e.frameCyclesPerScanline
	scanlines := e.frameScanlines
	pos := int64(0)
	for line := uint16(0); line < scanlines; line++ {
		// Line-boundary sync against the VDP walker only: start the line
		// when the walker trails it by no more than vdpSlackLines. The
		// slave is held within syncChunkCycles by the per-chunk barrier
		// below, not here.
		spinWait(&e.vdpLinesWalked, int64(line)+1-vdpSlackLines)

		// Walk the line in sync chunks: the master runs to the chunk's
		// cycle target, the VDP2 intra-line position advances, then the
		// barrier publishes the master's position, delivers any pending
		// SINIT capture, and waits for the slave to reach the position
		// before the next chunk. An instruction that overshoots the
		// target carries into the next chunk through the cycle counter.
		for cyc := uint32(0); cyc < lineWidth; {
			chunk := lineWidth - cyc
			if chunk > syncChunkCycles {
				chunk = syncChunkCycles
			}
			cyc += chunk
			target := e.frameStart + uint64(pos) + uint64(cyc)
			e.master.RunUntil(target)
			// A MINIT write in this chunk flags the cross-CPU FRT
			// capture; the slave latches its own FRT at its next
			// barrier.
			if e.bus.MINITWritten() {
				e.minitPending.Store(true)
			}
			vdp2.TickSystemCycles(chunk)
			e.sh2Barrier(&e.masterCycles, &e.secondaryCycles, pos+int64(cyc), e.master, &e.sinitPending, e.master.FRTInputCapture)
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
	// kick channels. The secondary's completion is its cycle counter
	// reaching the frame total; the walker's is the finished send.
	spinWait(&e.secondaryCycles, e.frameTotalCycles)
	<-e.vdpJobChFinished
	if err := e.renderer.err(); err != nil {
		e.rendererFailed(err)
	}

	// The frame's cycles are behind both CPUs: the next frame's positions
	// start at zero, any overshoot carried in the counters.
	e.frameStart += uint64(e.frameTotalCycles)

	smpc.TickFrame()

	e.audioBuffer = scsp.MixBuffer()

	// Enable sound CPU at end of frame
	if smpc.SoundEnabled() && scsp.InReset() {
		scsp.SetInReset(false)
	}
}

// sh2Barrier publishes this goroutine's clock position and spin-waits until
// the peer reaches it, holding the two SH-2 clocks within syncChunkCycles.
// It also delivers a pending cross-CPU FRT input capture: when the peer
// wrote MINIT/SINIT since the last barrier, the receiving CPU latches its
// own FRT here, on its own goroutine, within a chunk of the write.
//
// The wait is skipped at the frame end (RunFrame's frame-end wait covers
// the boundary).
func (e *Emulator) sh2Barrier(pub, peer *atomic.Int64, myPos int64, cpu *sh2.CPU, pending *atomic.Bool, capture func()) {
	pub.Store(myPos)
	if pending.CompareAndSwap(true, false) {
		capture()
	}
	if myPos >= e.frameTotalCycles {
		return
	}
	spins := 0
	for peer.Load() < myPos {
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
	for range e.vdpJobChBegin {
		e.walkVDPFrame()
		e.vdpJobChFinished <- struct{}{}
	}
}

// walkVDPFrame walks the frame a line at a time, starting each line
// only after both CPUs have completed it - VDP1 stays strictly behind
// the CPUs at line granularity, with no waits inside the line. Per
// line it advances VDP1 incremental command processing by the line's
// cycles, then renders the VDP2 row: the row's one render point is
// the line's end, so the row reads VRAM and CRAM and the VDP1 frame
// buffer after the whole line's draws have landed. Progress publishes
// once per line.
//
// Frame events fire at their line positions:
//
//	Start of line 1 (the first H-blank IN after V-blank OUT): VDP1's
//	FBCR take-in point per manual Sec 4.2 - the VDP1 frame change
//	(VBlankIn: deferred-register latch, swap or parity erase, EDSR),
//	the VBlankOut manual-swap evaluation, the PTM=10 auto-draw gate,
//	the VDP2 frame-scoped sample (BeginFrame), and row 0's render
//	from the post-change state. Every register write the game makes
//	during the vertical blank precedes this point, so the change acts
//	on the last value written.
//
//	Start of each later active line: BeginLine - the row's register
//	snapshot and per-line setup, and the VDP1 frame buffer view, taken
//	together so the buffer choice and its pixel format stay coherent
//	with the row's registers, and the renderer's beginLine right after
//	it, at the row's setup point. The row's render point at the line's
//	end reads that buffer: the software renderer composites from it
//	there, and the GPU renderer binds it at the frame's first row
//	for its later composite. The buffer is the display buffer the
//	frame change brought in, complete, and nothing draws into it
//	during the display in any mode.
//
//	Start of the first vblank line: the manual-mode erase deferred
//	from this frame's change flushes - the frame's rows have
//	composited, and per Sec.4.2 the erase-write progresses behind the
//	beam during the displayed field.
func (e *Emulator) walkVDPFrame() {
	vdp1 := e.vdp1
	vdp2 := e.vdp2
	renderer := e.renderer
	lineWidth := int64(e.frameCyclesPerScanline)
	scanlines := int64(e.frameScanlines)
	activeLines := int64(e.vdpWalkActiveLines)
	for line := int64(0); line < scanlines; line++ {
		// Line-boundary sync: walk this line only after both CPUs
		// have completed it.
		wait := (line + 1) * lineWidth
		e.waitLineEnd(wait, int(line))
		if line == 1 {
			// First H-blank IN after V-blank OUT: VDP1's FBCR
			// latch point. Per VDP1 manual Sec 4.2, FCM/FCT
			// settings are to be made immediately after the
			// V-blank OUT interrupt, and FBCR access is
			// prohibited from the first H-blank IN after V-blank
			// OUT until the next H-blank IN - the window in which
			// VDP1 latches and processes the register. So the
			// frame change (VBlankIn), the manual-change swap,
			// and the PTM=10 auto-draw gate evaluate here, one
			// line after the interrupt: every write made during
			// the vertical blank, including the V-blank OUT
			// handler's during line 0, is included, and the
			// handlers read pre-swap state (e.g. EDSR.CEF)
			// before the change lands. The changed display buffer
			// applies to this field, so row 0 renders here from
			// the post-change state before this line's VDP1
			// cycles run.
			vdp1.VBlankIn()
			vdp1.VBlankOut()
			if vdp1.PTM() == 2 && vdp1.ConsumeFBSwap() {
				vdp1.StartAutoDraw()
			}
			vdp2.BeginFrame()
			renderer.beginFrame()
			if activeLines > 0 {
				beginRow(vdp2, renderer, 0, vdp2.vdp1View(vdp1))
				renderer.renderLine(0)
			}
		}
		if line >= 1 && line < activeLines {
			beginRow(vdp2, renderer, int(line), vdp2.vdp1View(vdp1))
		}
		if line == activeLines {
			renderer.endDisplay()
			vdp1.restartList()
			vdp1.PerformLateErase()
		}
		vdp1.TickSystemCycles(uint32(lineWidth))
		renderer.vdp1Line(int(line))
		if line >= 1 && line < activeLines {
			renderer.renderLine(int(line))
		}
		e.vdpLinesWalked.Store(line + 1)
	}
	renderer.endFrame()
}

// beginRow prepares row y at its setup point: the VDP2's BeginLine,
// then the renderer's beginLine, which reads the state BeginLine set.
func beginRow(v *VDP2, r frameRenderer, y int, fb vdp1FBView) {
	v.BeginLine(y, fb)
	r.beginLine(y)
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
	lineWidth := int64(e.frameCyclesPerScanline)
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
				// A slave held in reset stopped counting: bring it up to
				// the current system cycle so it runs only from this chunk
				// onward and expresses the shared clock to the bus.
				now := e.frameStart + uint64(pos)
				if e.slave.Cycles() < now {
					e.slave.SyncClock(now)
				}
				e.slave.RunUntil(now + uint64(chunk))
				// A SINIT write flags the master's FRT capture for the
				// master's next barrier.
				if e.bus.SINITWritten() {
					e.sinitPending.Store(true)
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
