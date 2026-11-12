// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
)

// VBlankIn is the frame change: it handles the frame buffer lifecycle
// once per field, at the first H-blank IN after V-blank OUT where the
// VDP1 takes the change mode register in (manual Sec 4.2), so every
// register write made during the vertical blank is in force. Handles
// erase and/or swap based on FBCR (FCM/FCT) and TVMR (VBE). Also
// updates EDSR BEF/CEF.
//
// Double interlace (DIE=1) takes the same paths: per manual Sec 4.2
// the fields are changed every 1/60 second with FCM=FCT=0, so the
// buffers swap every field, the buffer brought in for drawing is
// erased, and the displayed buffer is the previous field's completed
// draw. DIL only selects the parity of the doubled Y range the draw
// lands on (pixelParams).
func (v *VDP1) VBlankIn() {
	// Latch shadow registers into active per VDP1 manual Sec.3. Fires
	// every frame change, regardless of whether the swap branches
	// below actually swap. Subsequent swap/erase/EDSR logic in this function
	// reads the post-latch values.
	v.latchPending()

	fcm := v.fbcr & 0x02 // bit 1
	fct := v.fbcr & 0x01 // bit 0
	vbe := v.tvmr & 0x08 // bit 3
	if v.vbeLatched {
		vbe = 0x08
		v.vbeLatched = false
	}

	if fcm == 0 {
		// 1-cycle auto mode: swap then erase new drawFB. LOPR latches
		// the most recent COPR at the FB-change boundary per VDP1
		// manual Sec 4.7. A pending manual erase completes before the
		// buffer changes roles. Auto mode erases every field on its
		// own, so a leftover erase request is spent.
		v.PerformLateErase()
		v.lopr = v.copr
		v.drawFB, v.displayFB = v.displayFB, v.drawFB
		v.eraseFrameBuffer(v.drawFB)
		v.fbSwapped = true
		v.swapCount++
		// Per manual Sec 4.6: BEF latches CEF on FB swap. CEF stays
		// as-is and only clears at drawing-started (startDraw), so
		// PTM=00 with no auto-trigger preserves CEF correctly.
		v.edsr = (v.edsr & 0x02) | ((v.edsr >> 1) & 0x01)
		v.eraseRequested = false
	} else {
		// Manual mode (FCM=1). FCT controls a single swap event;
		// it auto-clears after the swap so a single SH-2 write
		// produces one swap, not a continuous stream. With FCT=0,
		// the V-Blank manual-erases the display FB only if the SH-2
		// wrote FCT=0 this field, an erase request is still latched,
		// or VBE was set; an auto-cleared FCT=0 with none of those is
		// idle so games that chain multiple draws between swaps
		// accumulate into the same drawFB.
		switch {
		case fct != 0:
			if vbe != 0 {
				v.eraseFrameBuffer(v.displayFB)
			}
			// LOPR latches the most recent COPR at FB-change per
			// VDP1 manual Sec 4.7. A pending manual erase completes
			// before the buffer changes roles.
			v.PerformLateErase()
			v.lopr = v.copr
			v.drawFB, v.displayFB = v.displayFB, v.drawFB
			v.fbcr &^= 0x01
			v.fbSwapped = true
			v.swapCount++
			// Per manual Sec 4.6: BEF latches CEF on FB swap. CEF
			// stays as-is; startDraw clears it when the PTM=10
			// auto-trigger fires later this frame.
			v.edsr = (v.edsr & 0x02) | ((v.edsr >> 1) & 0x01)
			// A latched erase request clears the buffer the change
			// brings in - after the swap it is the display FB of the
			// "next specified field" the Sec 4.2 erase targets.
			// The request stays latched so following changes clear
			// the buffers they bring in too.
			if v.eraseRequested {
				v.eraseFrameBuffer(v.displayFB)
			}
		case v.fbcrWritten || v.eraseRequested || vbe != 0:
			// Manual mode (erase) per Sec.4.2: "Erase/write of the
			// display frame buffer is performed in the next specified
			// field." Capture the buffer to erase now (the display
			// framebuffer as of the request); the erase itself runs
			// at the next VBlankIn boundary or at an earlier swap,
			// so the next field still displays the pre-erase content
			// and the buffer is clean before anything draws into it.
			// This one-shot also consumes any latched erase request.
			v.lateEraseFB = v.displayFB
			v.eraseRequested = false
		}
	}
	// fbcrWritten describes only the field that just ended; every
	// field boundary consumes it.
	v.fbcrWritten = false
}

// VBlankOut handles a deferred FCM=1 + FCT=1 swap at the start of the
// new frame, after the SH-2's vbout handler has had a chance to write
// FBCR FCT=1 in response to the just-raised vbout IRQ. This catches
// the case where the SH-2 sets up "swap + auto-draw" entirely from
// inside its vbout handler at line 0; without this path the swap
// would not occur until the next frame's VBlankIn, costing a Saturn
// frame of latency in the SH-2's drawing pipeline.
//
// VBlankIn already consumed any FCT=1 written before the last vblank,
// so when VBlankOut sees FCT=1 here it is necessarily a "new" write
// made by the vbout handler. FCM=0 auto-swap is never handled here;
// it is fully owned by VBlankIn.
func (v *VDP1) VBlankOut() {
	if v.fbcr&0x02 == 0 || v.fbcr&0x01 == 0 {
		return
	}

	// FCM=1+FCT=1 manual swap is a frame-buffer switch site; per VDP1
	// manual Sec.3, PTM=00/10 (and EOS/DIE/DIL/EWxR) commit here.
	v.latchPending()

	if v.tvmr&0x08 != 0 || v.vbeLatched {
		v.eraseFrameBuffer(v.displayFB)
		v.vbeLatched = false
	}
	// A pending manual erase completes before the buffer changes
	// roles in the swap.
	v.PerformLateErase()
	v.lopr = v.copr
	v.drawFB, v.displayFB = v.displayFB, v.drawFB
	v.fbcr &^= 0x01
	v.fbSwapped = true
	v.swapCount++
	v.edsr = (v.edsr & 0x02) | ((v.edsr >> 1) & 0x01)
	// A latched erase request clears the buffer the change brings in.
	if v.eraseRequested {
		v.eraseFrameBuffer(v.displayFB)
	}
	// The FBCR write that armed this swap consumed its fbcrWritten
	// signal here; clearing prevents VBlankIn from later mistaking
	// the auto-cleared FCT=0 for a fresh "erase only" request.
	v.fbcrWritten = false
}

// startDraw initializes per-draw state at the top of a new draw. Per
// VDP1 manual Sec 4.3 every PTMR or auto-trigger restarts at command
// table address 0; per Sec 3.3 local coordinates are also reset
// because the command list is expected to set them via command 0xA
// before issuing parts. The single return-address register (manual
// Sec 3.2) starts at 0; jp=3 (return) before any jp=2 (call) jumps to
// addr 0, matching hardware. Per manual Sec 4.6 CEF resets at
// "drawing started", so it clears here on every trigger source.
func (v *VDP1) startDraw() {
	v.procAddr = 0
	v.procReturnAddr = 0
	v.systemCycleDebt = 0
	v.drawTicked.Store(0)
	v.drawStallCharged.Store(0)
	v.drawActive = true
	v.edsr &^= 0x02
	// A new draw discards any pending ENDR latched against the
	// previous draw. ENDR is a "terminate the currently active draw"
	// signal; once that draw is replaced (auto-trigger at V-Blank or
	// PTM=01 from the SH-2), the latched termination has nothing to
	// terminate and must be cleared, otherwise the new draw aborts
	// on its first command-processor step before any command runs.
	v.drawEnd = false
	// Abandon any half-done command from the prior draw.
	v.cmdPhase = phaseIdle
}

// TickSystemCycles advances the VDP1 command processor by up to
// `cycles` system cycles. Consumes the drawPending latch (PTM=01
// trigger) on entry. Returns immediately if no draw is active. State
// (current command address, return address, overshoot debt) persists
// across calls so a list spanning multiple segments completes
// naturally. If the end-bit is fetched within budget, EDSR.CEF is set
// and the sprite-end interrupt is raised. Budget exhaustion before
// end-bit leaves CEF clear (transfer-over per VDP1 manual Sec 4.6).
//
// A primitive that consumes more cycles than the remaining budget is
// allowed to complete; the overshoot is recorded in systemCycleDebt
// and absorbed by the next TickSystemCycles call before any new
// commands are processed.
func (v *VDP1) TickSystemCycles(cycles uint32) {
	// PTM=01 register write may have set drawPending. Per VDP1
	// manual Sec 4.3 line 3311-3312, a PTM=01 write *during*
	// drawing restarts processing at the top of the command table,
	// so honor the latch even when a draw is already active.
	if v.drawPending {
		v.startDraw()
		v.drawPending = false
	}
	if !v.drawActive {
		// Bus contention only delays drawing while drawing is happening;
		// discard any stall accumulated from writes that arrived after
		// the prior draw completed.
		v.vramWriteStallCycles.Store(0)
		return
	}
	// Advance the cycles-run counter that caps per-draw stall in
	// chargeDrawStallBounded.
	v.drawTicked.Add(int64(cycles))

	// Bus arbitration: SH-2 / SCU-DMA writes to VRAM during this
	// segment have priority over VDP1's command-table fetches. Charge
	// their cumulative stall against this segment's budget; overshoot
	// carries via systemCycleDebt to the next segment. Swap reads and
	// clears the accumulator in one atomic step against the writers.
	budget := int32(cycles) - v.systemCycleDebt - v.vramWriteStallCycles.Swap(0)
	v.systemCycleDebt = 0
	if budget <= 0 {
		v.systemCycleDebt = -budget
		return
	}

	consumed, endBit := v.processCommands(budget)
	// The tick is the list's unit: the command in progress closes its
	// entry here, so the list holds only complete entries between
	// ticks and every event taken between ticks lands between them.
	v.closeEntry()
	if consumed > budget {
		v.systemCycleDebt = consumed - budget
	}

	if endBit {
		v.edsr |= 0x02
		v.scu.RaiseSpriteDrawEnd()
	}
}

// eraseFrameBuffer fills the erase region of the given buffer with
// EWDR: the region is resolved from EWLR and EWRR into an erase op
// and filled through the compute package's EraseRows. In 16-bit
// modes, X coordinates are in
// 8-pixel units and each pixel is erased with the full 16-bit EWDR
// value. In 8-bit modes, X coordinates are in 16-pixel units and
// EWDR bits 15:8 fill even X pixels while bits 7:0 fill odd X pixels.
func (v *VDP1) eraseFrameBuffer(target []byte) {
	bpp8 := v.is8bpp()
	w := v.fbWidth()
	h := v.fbHeight()

	xMul := 8
	if bpp8 {
		xMul = 16
	}

	x1 := int((v.ewlr>>9)&0x3F) * xMul
	y1 := int(v.ewlr & 0x1FF)
	x3reg := int((v.ewrr >> 9) & 0x7F)
	if x3reg == 0 {
		return
	}
	x3 := x3reg*xMul - 1
	y3 := int(v.ewrr & 0x1FF)

	// Degenerate region: manual Sec 4.4 p.49 - when X1 >= X3 or
	// Y1 > Y3, erase a single dot at (X1, Y1): 1 pixel in normal /
	// hi-res, 8 pixels in rotation (TVM 010/011) or HDTV (TVM 100).
	// Treated as (X3 = X1 + n - 1, Y3 = Y1), horizontal run.
	if x1 >= x3 || y1 > y3 {
		nDots := 1
		switch v.tvm() {
		case 2, 3, 4:
			nDots = 8
		}
		x3 = x1 + nDots - 1
		y3 = y1
	}

	if x3 >= w {
		x3 = w - 1
	}
	if y3 >= h {
		y3 = h - 1
	}
	if x1 > x3 || y1 > y3 {
		return
	}

	op := draw.DrawOp{Kind: draw.OpErase, Color: uint32(v.ewdr)}
	op.Pixel.FBWidth = int32(w)
	op.Pixel.FBHeight = int32(h)
	op.Pixel.Is8bpp = bpp8
	op.X[0], op.Y[0] = int32(x1), int32(y1)
	op.X[2], op.Y[2] = int32(x3), int32(y3)
	v.appendOp(&op, target)
	if !v.pairKept.Load() {
		// The erase reaches the device as the entry; the CPU pair
		// is not kept.
		return
	}
	var st draw.DrawState
	b := draw.Buffers{VRAM: (*compute.VDP1VRAM)(v.vram), FB: compute.VDP1FB(target), Records: v.lineRecords}
	draw.Draw(&b, &op, &st)
}

// drawResume is the drawing command in progress: the op the walk
// resolved, its color calculation bits for the cycle costs, and its
// progress through Draw.
type drawResume struct {
	op draw.DrawOp
	cc uint16
	st draw.DrawState
}

// beginDraw takes the resolved op as the command in progress,
// reserves its line records, and starts drawing it against the
// budget.
func (v *VDP1) beginDraw(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	d := &v.cmd
	d.cc = cmd.pmod & 0x07
	d.st = draw.DrawState{PixCycles: vdp1PixelCycles(d.cc), Timing: !v.pairKept.Load()}
	v.reserveRecords(&d.op)
	v.cmdPhase = phaseDraw
	return v.runDraw(budget)
}

// runDraw draws the command in progress through Draw against the
// budget, and yields with cmdPhase set when the
// budget is reached, with the command's entry in the frame's list
// opened from the walk's position when none is open. The walk runs
// the store pass, which reads no round state. On completion the table
// reads over the VRAM port are charged, an approximation of their
// setup-time cost: the Gouraud shading table when the color
// calculation shades, and the CLUT of a sprite in color mode 1.
func (v *VDP1) runDraw(budget int32) (consumed int32, done bool) {
	d := &v.cmd
	if !v.cmdOpen {
		v.openEntry()
	}
	d.st.Budget = budget
	b := draw.Buffers{VRAM: (*compute.VDP1VRAM)(v.vram), FB: compute.VDP1FB(v.drawFB), Records: v.lineRecords}
	draw.Draw(&b, &d.op, &d.st)
	cycles := d.st.Cycles
	if !d.st.Done {
		return cycles, false
	}
	v.cmdOpen = false
	v.cmdPhase = phaseIdle
	if d.cc >= 4 {
		cycles += vdp1GouraudWords * vdp1VRAMPortWordCycles
	}
	switch d.op.Kind {
	case draw.OpNormalSprite, draw.OpScaledSprite, draw.OpDistortedSprite:
		if d.op.ColorMode == 1 {
			cycles += vdp1CLUTWords * vdp1VRAMPortWordCycles
		}
	}
	return cycles, true
}

// processCommands consumes up to `budget` system cycles of pending
// command-table work. Walks from procAddr until budget is exhausted,
// the end-bit is fetched, or ENDR forces termination. Returns the
// cycles actually consumed and whether the end-bit was fetched.
//
// State (procAddr, procReturnAddr, copr) lives on the struct and
// persists across calls, so a list spanning multiple TickSystemCycles
// invocations completes naturally without re-scanning. There is no
// command-count cap: cycle budget alone bounds the loop, matching
// real hardware behavior where a malformed list with no reachable
// end-bit simply consumes the frame's drawing budget and times out
// (transfer-over per VDP1 manual Sec 4.6).
func (v *VDP1) processCommands(budget int32) (consumed int32, endBit bool) {
	for {
		if consumed >= budget {
			return consumed, false
		}

		// ENDR force-termination, VDP1 manual Sec 4.5: drawing ends
		// within approximately 30 clock cycles of the write, including
		// the drawing currently being processed, and the interrupted
		// drawing cannot be resumed. A command in progress is abandoned
		// where it stands.
		if v.drawEnd {
			consumed += 30
			v.drawEnd = false
			v.drawActive = false
			v.cmdPhase = phaseIdle
			return consumed, false
		}

		// Resume any in-progress command before fetching a new one.
		// The resume helper returns done=true on completion so the
		// dispatcher can advance procAddr per the saved cmd's jp.
		if v.cmdPhase != phaseIdle {
			c, done := v.resumeCommand(budget - consumed)
			consumed += c
			if !done {
				return consumed, false
			}
			v.advanceProcAddrAfterCmd(&v.cmdSnapshot)
			continue
		}

		cmd := v.readCommand(v.procAddr)
		v.copr = uint16(v.procAddr / 8)
		// Parameter read: fetch the command table over the VRAM port.
		// Charged for every command before any control-bit dispatch
		// (end bit, skip, setup) because the hardware reads the table
		// to learn what the command is.
		consumed += vdp1CmdFetchWords * vdp1VRAMPortWordCycles

		if cmd.ctrl&0x8000 != 0 {
			v.drawActive = false
			return consumed, true
		}

		jp := (cmd.ctrl >> 12) & 0x07
		cmdType := cmd.ctrl & 0x000F
		v.cmdSnapshot = cmd

		// Skip mode: jp >= 4 means skip command execution. No
		// rasterization, no character-data fetch beyond the table
		// fetch already charged above.
		if jp >= 4 {
			v.advanceProcAddrAfterCmd(&cmd)
			continue
		}

		var c int32
		var done bool
		switch cmdType {
		case 0x0:
			c, done = v.startNormalSprite(&cmd, budget-consumed)
		case 0x1:
			c, done = v.startScaledSprite(&cmd, budget-consumed)
		case 0x2:
			c, done = v.startDistortedSprite(&cmd, budget-consumed)
		case 0x4:
			c, done = v.startPolygon(&cmd, budget-consumed)
		case 0x5:
			c, done = v.startPolyline(&cmd, budget-consumed)
		case 0x6:
			c, done = v.startLine(&cmd, budget-consumed)
		case 0x8:
			v.userClipX1 = clipCoordX(cmd.xa)
			v.userClipY1 = clipCoordY(cmd.ya)
			v.userClipX2 = clipCoordX(cmd.xc)
			v.userClipY2 = clipCoordY(cmd.yc)
			done = true
		case 0x9:
			v.sysClipX = clipCoordX(cmd.xc)
			v.sysClipY = clipCoordY(cmd.yc)
			done = true
		case 0xA:
			v.localX = cmd.xa
			v.localY = cmd.ya
			done = true
		default:
			// Undefined/prohibited Comm (observed: 0xF). The manual does
			// not define what the VDP1 does with one. Halt was chosen
			// from observed game behavior requiring it.
			v.drawActive = false
			return consumed, true
		}
		consumed += c
		if !done {
			return consumed, false
		}

		v.advanceProcAddrAfterCmd(&cmd)
	}
}

// advanceProcAddrAfterCmd applies the cmd's jp link-field semantics
// to procAddr. Called when a command (drawing or non-drawing)
// completes.
func (v *VDP1) advanceProcAddrAfterCmd(cmd *vdp1Command) {
	jp := (cmd.ctrl >> 12) & 0x07
	switch jp & 0x03 {
	case 0: // next
		v.procAddr += 0x20
	case 1: // assign
		v.procAddr = uint32(cmd.link) * 8
	case 2: // call - single return register, clobbers any prior value
		v.procReturnAddr = v.procAddr + 0x20
		v.procAddr = uint32(cmd.link) * 8
	case 3: // return - jumps to whatever is in the return register
		v.procAddr = v.procReturnAddr
	}
}

// resumeCommand continues the drawing command in progress from its
// draw state. Returns done=true when the command completes, at which
// point cmdPhase is idle.
//
// Each drawing command's startX resolves the command into its draw
// op and enters beginDraw; the op's lines then run through Draw, the
// compute package's one loop over its setup and span functions, which
// check the budget every draw.YieldChunk dots and yield when
// the per-call budget is reached, leaving cmdPhase at phaseDraw.
func (v *VDP1) resumeCommand(budget int32) (consumed int32, done bool) {
	if v.cmdPhase == phaseDraw {
		return v.runDraw(budget)
	}
	v.cmdPhase = phaseIdle
	return 0, true
}
