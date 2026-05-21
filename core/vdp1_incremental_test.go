// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// Tests for VDP1 incremental command processing. These exercise
// behavior that has no synchronous-batch counterpart: cycle budget
// respect, mid-list COPR observability, transfer-over semantics, and
// trigger-latch consumption inside TickSystemCycles.

// TestTickSystemCyclesRespectsBudget verifies that a small budget
// completes only part of a multi-command list and that a follow-up
// large budget completes the rest. Final framebuffer must match the
// all-at-once result.
func TestTickSystemCyclesRespectsBudget(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.ptmr = 2
	v.startDraw()

	// Two non-textured lines and an end-bit. Each line is type 0x6,
	// jp=0 (next). Lines stretch from (0,0) to (10,0) and (0,1) to
	// (10,1).
	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x06, 0x1234)
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 10)
	writeCmd16(v, 0x12, 0)

	writeCmd16(v, 0x20, 0x0006)
	writeCmd16(v, 0x26, 0x5678)
	writeCmd16(v, 0x2C, 0)
	writeCmd16(v, 0x2E, 1)
	writeCmd16(v, 0x30, 10)
	writeCmd16(v, 0x32, 1)

	writeDrawEnd(v, 0x40)

	// Clear CEF so the assertion below is meaningful (default EDSR
	// has BEF=CEF=1; CEF is preserved across drawing-start in the
	// pre-refactor compat model — see startDraw()).
	v.edsr &^= 0x02

	// First call: tiny budget. Should make some progress but not
	// finish the list (16-cycle fetch alone exceeds budget).
	v.TickSystemCycles(8)

	if !v.drawActive {
		t.Fatal("drawActive should still be true after a partial budget")
	}
	if v.edsr&0x02 != 0 {
		t.Error("CEF set after partial budget; should be 0 until end-bit")
	}

	// Second call: large budget. Should complete the list.
	v.TickSystemCycles(1 << 30)

	if v.drawActive {
		t.Error("drawActive should be false after end-bit fetch")
	}
	if v.edsr&0x02 == 0 {
		t.Error("CEF should be set after end-bit fetch within budget")
	}
	if got := readFBPixel(v, 5, 0); got != 0x1234 {
		t.Errorf("first line px(5,0) = 0x%04X, want 0x1234", got)
	}
	if got := readFBPixel(v, 5, 1); got != 0x5678 {
		t.Errorf("second line px(5,1) = 0x%04X, want 0x5678", got)
	}
}

// TestCycleDebtAbsorbsOvershoot verifies that a primitive whose cost
// exceeds the segment budget is allowed to complete, with the
// overshoot recorded in systemCycleDebt for the next call to absorb before
// any new commands run.
func TestCycleDebtAbsorbsOvershoot(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.ptmr = 2
	v.startDraw()

	// One large untextured line: (0,0)-(50,0). Cost = 51 + maybe
	// gouraud overhead.
	writeCmd16(v, 0x00, 0x0006)
	writeCmd16(v, 0x06, 0x1234)
	writeCmd16(v, 0x0C, 0)
	writeCmd16(v, 0x0E, 0)
	writeCmd16(v, 0x10, 50)
	writeCmd16(v, 0x12, 0)
	writeDrawEnd(v, 0x20)

	// Tiny budget: well under one primitive's cost.
	v.TickSystemCycles(8)

	if v.systemCycleDebt <= 0 {
		t.Errorf("systemCycleDebt should be positive after budget overshoot, got %d", v.systemCycleDebt)
	}

	// Drain the rest. Whatever debt the previous call left must be
	// absorbed before new commands run.
	v.TickSystemCycles(1 << 30)

	if v.systemCycleDebt != 0 {
		t.Errorf("systemCycleDebt should be 0 after large drain, got %d", v.systemCycleDebt)
	}
	if got := readFBPixel(v, 25, 0); got != 0x1234 {
		t.Errorf("line px(25,0) = 0x%04X, want 0x1234", got)
	}
}

// TestCOPRObservableMidList verifies COPR advances during command
// processing rather than only being visible at end-of-frame.
func TestCOPRObservableMidList(t *testing.T) {
	v := NewVDP1(NewSCU())

	// Build a list of 5 local-coord commands followed by an end-bit
	// at 0xA0. Local-coord is cheap (16 cycles fetch + 0 cost), so
	// COPR visibly advances. Writes happen before startDraw so the
	// drawActive=true VRAM bus-contention stall does not consume the
	// per-tick budget reserved for command processing.
	for i := 0; i < 5; i++ {
		writeCmd16(v, uint32(i)*0x20, 0x000A)
	}
	writeDrawEnd(v, 0xA0)

	v.ptmr = 2
	v.startDraw()

	// Run with a budget that covers a few commands but not the
	// whole list.
	v.TickSystemCycles(40)

	if !v.drawActive {
		t.Fatal("drawActive should still be true mid-list")
	}
	if v.copr == 0 {
		t.Error("COPR did not advance from initial 0; expected mid-list value")
	}
	endAddr := uint16(0xA0 / 8)
	if v.copr == endAddr {
		t.Error("COPR advanced all the way to end-bit; budget too generous")
	}
}

// TestPTM01ImmediateTriggerOnRegisterWrite verifies a PTM=01 register
// write latches drawPending and that the next TickSystemCycles call
// consumes the latch and starts processing.
func TestPTM01ImmediateTriggerOnRegisterWrite(t *testing.T) {
	v := NewVDP1(NewSCU())
	writeDrawEnd(v, 0)

	v.Write(0x04, 0x0001) // PTMR = 01

	if !v.drawPending {
		t.Fatal("drawPending should be set after PTM=01 register write")
	}
	if v.drawActive {
		t.Error("drawActive should not yet be true; ticking has not happened")
	}

	v.TickSystemCycles(1 << 30)

	if v.drawPending {
		t.Error("drawPending should be cleared after TickSystemCycles consumes it")
	}
	if v.drawActive {
		t.Error("drawActive should be false after end-bit fetch")
	}
	if v.edsr&0x02 == 0 {
		t.Error("CEF should be set after end-bit fetch")
	}
}

// TestPTMRReTriggerRestartsAtAddrZero verifies that a PTM=01 write
// during a partial draw restarts processing at command address 0
// (manual Sec 4.3 line 3334-3337).
func TestPTMRReTriggerRestartsAtAddrZero(t *testing.T) {
	v := NewVDP1(NewSCU())

	// A small list of local-coord commands then end-bit. Writes happen
	// before startDraw so the drawActive=true VRAM bus-contention stall
	// does not consume the per-tick budget.
	for i := 0; i < 3; i++ {
		writeCmd16(v, uint32(i)*0x20, 0x000A)
	}
	writeDrawEnd(v, 0x60)

	v.ptmr = 2
	v.startDraw()

	v.TickSystemCycles(40) // partial: should advance procAddr off zero
	if v.procAddr == 0 {
		t.Fatal("procAddr should have advanced off 0 before re-trigger")
	}

	// PTM=01 register write while drawing: drawPending latches and
	// the next tick restarts at addr 0.
	v.Write(0x04, 0x0001)
	v.TickSystemCycles(1 << 30)

	// After completion, procAddr should reflect the final fetched
	// command (the end-bit at 0x60 / 8 = 0xC).
	if v.copr != 0x0C {
		t.Errorf("COPR after re-triggered drain = 0x%04X, want 0x000C", v.copr)
	}
}

// TestInfiniteLoopBoundedByCycleBudget verifies that a malformed
// list with no reachable end-bit (jp=1 assign back to address 0)
// terminates after consuming the budget. Replaces the deleted
// TestMaxCommandLimit which tested the now-removed count cap.
func TestInfiniteLoopBoundedByCycleBudget(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.ptmr = 2
	v.startDraw()

	// Single local-coord command with jp=1 (assign), link points
	// back to address 0. Walks: 0 -> 0 -> 0 -> ...
	writeCmd16(v, 0x00, 0x100A)
	writeCmd16(v, 0x02, 0x0000)

	// Clear CEF so the assertion is meaningful (see comment in
	// TestTickSystemCyclesRespectsBudget).
	v.edsr &^= 0x02

	// Bounded budget. Function must return without hanging.
	v.TickSystemCycles(10000)

	// drawActive remains true because no end-bit was reached;
	// the next tick can resume.
	if !v.drawActive {
		t.Error("drawActive should still be true after transfer-over")
	}
	if v.edsr&0x02 != 0 {
		t.Error("CEF should not be set on transfer-over")
	}
}
