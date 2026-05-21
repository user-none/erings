// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// vdp1FBStride is the default framebuffer width for TVM=000 (normal 16-bit).
// Used by test helpers. Drawing code uses v.fbWidth() for TVM-aware stride.
const vdp1FBStride = 512

// VBlankIn handles frame buffer lifecycle at the VBlank boundary.
// Called once per field. Handles erase and/or swap based on FBCR
// (FCM/FCT) and TVMR (VBE). Also updates EDSR BEF/CEF.
//
// Under DIE=1 (double-density interlace draw mode) the buffers are
// pinned to fixed parity roles (drawFB=even, displayFB=odd) and the
// 1-cycle swap is suppressed. Erase clears only the DIL-matched FB so
// the OTHER field's previous draw is preserved for VDP2 composite this
// field.
func (v *VDP1) VBlankIn() {
	// Latch shadow registers into active per VDP1 manual Sec.3. Fires
	// every vblank-IN, regardless of whether the swap branches below
	// actually swap. Subsequent swap/erase/EDSR logic in this function
	// reads the post-latch values.
	v.latchPending()

	fcm := v.fbcr & 0x02 // bit 1
	fct := v.fbcr & 0x01 // bit 0
	vbe := v.tvmr & 0x08 // bit 3
	if v.vbeLatched {
		vbe = 0x08
		v.vbeLatched = false
	}

	if v.dieDoubled() {
		target := v.drawFB
		if v.dilOdd() {
			target = v.displayFB
		}
		// Spec mandates FCM=FCT=0 (1-cycle) under DIE=1, which erases
		// the draw target each field. Honor FCM=1 manual erase
		// defensively if a game violates the spec; never swap under
		// DIE=1 regardless of FCM/FCT/VBE.
		if fcm == 0 || fct == 0 {
			v.eraseFrameBuffer(target)
		}
		// Per Sec.4.2 "fields are changed every 1/60 second" under
		// DIE; each field is a frame-change for PTM=10 purposes even
		// though buffer pointers stay pinned. Mark for the gate so
		// the auto-trigger fires every field.
		v.fbSwapped = true
		v.swapCount++
		// EDSR transition at field change under DIE=1: BEF latches
		// the prior CEF per manual Sec 4.6. CEF itself is left
		// untouched - the spec specifies CEF clears at "drawing
		// started", which startDraw handles when the PTM=10
		// auto-trigger fires.
		v.edsr = (v.edsr & 0x02) | ((v.edsr >> 1) & 0x01)
		return
	}

	if fcm == 0 {
		// 1-cycle auto mode: swap then erase new drawFB. LOPR latches
		// the most recent COPR at the FB-change boundary per VDP1
		// manual Sec 4.7.
		v.lopr = v.copr
		v.drawFB, v.displayFB = v.displayFB, v.drawFB
		v.eraseFrameBuffer(v.drawFB)
		v.fbSwapped = true
		v.swapCount++
		// Per manual Sec 4.6: BEF latches CEF on FB swap. CEF stays
		// as-is and only clears at drawing-started (startDraw), so
		// PTM=00 with no auto-trigger preserves CEF correctly.
		v.edsr = (v.edsr & 0x02) | ((v.edsr >> 1) & 0x01)
	} else {
		// Manual mode (FCM=1). FCT controls a single swap event;
		// it auto-clears after the swap so a single SH-2 write
		// produces one swap, not a continuous stream. With FCT=0,
		// the V-Blank manual-erases drawFB only if the SH-2
		// explicitly wrote FBCR this frame (or VBE was set);
		// auto-cleared FCT=0 with no write is idle so games that
		// chain multiple draws between swaps accumulate into the
		// same drawFB.
		switch {
		case fct != 0:
			if vbe != 0 {
				v.eraseFrameBuffer(v.displayFB)
			}
			// LOPR latches the most recent COPR at FB-change per
			// VDP1 manual Sec 4.7.
			v.lopr = v.copr
			v.drawFB, v.displayFB = v.displayFB, v.drawFB
			v.fbcr &^= 0x01
			v.fbSwapped = true
			v.swapCount++
			// Per manual Sec 4.6: BEF latches CEF on FB swap. CEF
			// stays as-is; startDraw clears it when the PTM=10
			// auto-trigger fires later this frame.
			v.edsr = (v.edsr & 0x02) | ((v.edsr >> 1) & 0x01)
		case v.fbcrWritten || vbe != 0:
			// Manual mode (erase) per Sec.4.2: "Erase/write of the
			// display frame buffer is performed in the next specified
			// field." Defer the actual erase via lateEraseDisplayFB
			// so this frame's display still reads the pre-erase
			// content; the next FCT=1 swap moves the just-erased
			// buffer to drawFB with a clean slate. Erasing drawFB
			// here would wipe the just-drawn content before the swap
			// can move it to display.
			v.lateEraseDisplayFB = true
		}
	}
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
// VBlankIn already consumed any FCT=1 written before line 224 of the
// previous frame, so when VBlankOut sees FCT=1 here it is necessarily
// a "new" write made by the vbout handler. FCM=0 auto-swap is never
// handled here; it is fully owned by VBlankIn.
func (v *VDP1) VBlankOut() {
	if v.dieDoubled() {
		return
	}

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
	v.lopr = v.copr
	v.drawFB, v.displayFB = v.displayFB, v.drawFB
	v.fbcr &^= 0x01
	v.fbSwapped = true
	v.swapCount++
	v.edsr = (v.edsr & 0x02) | ((v.edsr >> 1) & 0x01)
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
		v.vramWriteStallCycles = 0
		return
	}

	// Bus arbitration: SH-2 / SCU-DMA writes to VRAM during this
	// segment have priority over VDP1's command-table fetches. Charge
	// their cumulative stall against this segment's budget; overshoot
	// carries via systemCycleDebt to the next segment.
	budget := int32(cycles) - v.systemCycleDebt - v.vramWriteStallCycles
	v.systemCycleDebt = 0
	v.vramWriteStallCycles = 0
	if budget <= 0 {
		v.systemCycleDebt = -budget
		return
	}

	consumed, endBit := v.processCommands(budget)
	if consumed > budget {
		v.systemCycleDebt = consumed - budget
	}

	if endBit {
		v.edsr |= 0x02
		v.scu.RaiseSpriteDrawEnd()
	}
}

// eraseFrameBuffer fills the erase region of the given buffer with EWDR.
// In 16-bit modes, X coordinates are in 8-pixel units and each pixel is
// erased with the full 16-bit EWDR value. In 8-bit modes, X coordinates
// are in 16-pixel units and EWDR bits 15:8 fill even X pixels while
// bits 7:0 fill odd X pixels.
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

	hi := uint8(v.ewdr >> 8)
	lo := uint8(v.ewdr)

	if bpp8 {
		for y := y1; y <= y3; y++ {
			for x := x1; x <= x3; x++ {
				offset := y*w + x
				if x&1 == 0 {
					target[offset] = hi
				} else {
					target[offset] = lo
				}
			}
		}
	} else {
		for y := y1; y <= y3; y++ {
			for x := x1; x <= x3; x++ {
				offset := (y*w + x) * 2
				target[offset] = hi
				target[offset+1] = lo
			}
		}
	}
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

		if v.drawEnd {
			// ENDR force-termination: ~30 cycles per VDP1 manual Sec 4.5.
			consumed += 30
			v.drawEnd = false
			v.drawActive = false
			return consumed, false
		}

		cmd := v.readCommand(v.procAddr)
		v.copr = uint16(v.procAddr / 8)
		// Command-table fetch overhead. The manual is silent on the
		// exact value; 16 is a reasonable approximation based on a
		// 16-word table fetched from VRAM. Charged for every command
		// before any control-bit dispatch (end-bit, skip, setup) since
		// the hardware reads the full table to find out what the
		// command is.
		consumed += 16

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
			v.userClipX1 = cmd.xa
			v.userClipY1 = cmd.ya
			v.userClipX2 = cmd.xc
			v.userClipY2 = cmd.yc
			done = true
		case 0x9:
			v.sysClipX = cmd.xc
			v.sysClipY = cmd.yc
			done = true
		case 0xA:
			v.localX = cmd.xa
			v.localY = cmd.ya
			done = true
		default:
			done = true
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

// resumeCommand re-enters the rasterizer matching the current
// cmdPhase to continue an in-progress command. Returns done=true
// when the command completes, at which point cmdPhase is cleared
// inside the per-type resume helper.
//
// Per-command rasterizers follow a startX / resumeX / runX
// pattern. startX runs setup once on a fresh command (charAddr,
// clip bounds, gouraud table, edge initialization) and stashes
// it on the matching resume struct. runX walks the iterator in
// chunks of pixelsPerYieldChunk and yields when the per-call
// budget is reached, leaving cmdPhase non-zero. resumeX
// continues the run from saved state. On completion any
// per-command tail cycle adders are charged and cmdPhase is
// reset to phaseIdle.
func (v *VDP1) resumeCommand(budget int32) (consumed int32, done bool) {
	switch v.cmdPhase {
	case phaseNormalSprite:
		return v.resumeNormalSprite(budget)
	case phaseScaledSprite:
		return v.resumeScaledSprite(budget)
	case phaseDistortedSprite:
		return v.resumeDistortedSprite(budget)
	case phasePolygon:
		return v.resumePolygon(budget)
	case phasePolyline:
		return v.resumePolyline(budget)
	case phaseLine:
		return v.resumeLine(budget)
	}
	v.cmdPhase = phaseIdle
	return 0, true
}
