// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// Tests in this file exercise the pending-op state machine itself:
// setPending / stepPending field invariants, dispatch ordering,
// per-popXXX corner cases that aren't observable from any single
// instruction, and machine-level interactions (DMAC stall vs
// pending). Tests that exercise a single instruction's full pipeline
// (TRAPA, RTE, MAC.W/.L, TAS, LDC.L/STC.L, AND.B/OR.B/XOR.B) live
// alongside that instruction in ops_*_test.go.

// --- Area A: setPending / stepPending invariants ---

// TestPendingSetPendingInitFields covers A1 - setPending leaves
// pendingOp = op, pendingStep = 0, pendingCount = n for every popXXX
// identifier currently defined. The handler dispatch contract relies
// on pendingStep starting at 0 (stepPending pre-increments before the
// case body, so the first call sees step 1) and on pendingCount
// counting down to 0.
func TestPendingSetPendingInitFields(t *testing.T) {
	cases := []struct {
		name  string
		op    uint8
		count uint8
	}{
		{"popStall", popStall, 1},
		{"popSTCL", popSTCL, 1},
		{"popLDCL", popLDCL, 2},
		{"popMemRMW", popMemRMW, 2},
		{"popTAS", popTAS, 3},
		{"popRTE", popRTE, 3},
		{"popTRAPA", popTRAPA, 7},
		{"popException", popException, 4},
		{"popMACW", popMACW, 1},
		{"popMACL", popMACL, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := newTestBus(0x1000)
			cpu := New(bus, true)
			cpu.pendingOp = popNone
			cpu.pendingStep = 0xAA
			cpu.pendingCount = 0xBB

			cpu.setPending(tc.op, tc.count)

			if cpu.pendingOp != tc.op {
				t.Errorf("pendingOp = %d, want %d", cpu.pendingOp, tc.op)
			}
			if cpu.pendingStep != 0 {
				t.Errorf("pendingStep = %d, want 0", cpu.pendingStep)
			}
			if cpu.pendingCount != tc.count {
				t.Errorf("pendingCount = %d, want %d", cpu.pendingCount, tc.count)
			}
		})
	}
}

// TestPendingStepOrderingPopException covers A2 - the dispatch
// ordering invariants on a popException run. Hardware Manual Sec 5.5
// Table 5.8 specifies a 5-cycle response; the pending machine handles
// the last 4 (the EX cycle was charged in acceptInterrupt). At each
// step boundary, this test asserts:
//   - pendingStep observed by the handler equals the step counter
//     after pre-increment (1 then 2 then 3 then 4).
//   - pendingCount decrements by 1 per stepPending after the case body.
//   - pendingOp clears to popNone exactly when pendingCount hits 0.
func TestPendingStepOrderingPopException(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.PC = 0x200
	cpu.reg.SR = 0
	bus.Write32(0x100+0x40*4, 0x300) // handler

	assertDIVU(cpu, 5, 0x40)
	if !cpu.processInterrupt() {
		t.Fatal("interrupt not accepted")
	}

	// After accept, count = 4, step = 0, op = popException.
	if cpu.pendingCount != 4 {
		t.Fatalf("post-accept count = %d, want 4", cpu.pendingCount)
	}
	if cpu.pendingStep != 0 {
		t.Fatalf("post-accept step = %d, want 0", cpu.pendingStep)
	}

	// Step 1: write SR. After: count=3, step=1, op=popException.
	cpu.stepPending()
	if cpu.pendingStep != 1 || cpu.pendingCount != 3 || cpu.pendingOp != popException {
		t.Errorf("after step 1: step=%d count=%d op=%d, want 1/3/popException",
			cpu.pendingStep, cpu.pendingCount, cpu.pendingOp)
	}

	// Step 2: write PC. After: count=2, step=2.
	cpu.stepPending()
	if cpu.pendingStep != 2 || cpu.pendingCount != 2 || cpu.pendingOp != popException {
		t.Errorf("after step 2: step=%d count=%d op=%d, want 2/2/popException",
			cpu.pendingStep, cpu.pendingCount, cpu.pendingOp)
	}

	// Step 3: read vector. After: count=1, step=3. PC must now be the handler.
	cpu.stepPending()
	if cpu.pendingStep != 3 || cpu.pendingCount != 1 || cpu.pendingOp != popException {
		t.Errorf("after step 3: step=%d count=%d op=%d, want 3/1/popException",
			cpu.pendingStep, cpu.pendingCount, cpu.pendingOp)
	}
	if cpu.reg.PC != 0x300 {
		t.Errorf("after step 3 PC = 0x%08X, want 0x300 (vector read)", cpu.reg.PC)
	}

	// Step 4: refill. After: count=0, step=4, op=popNone.
	cpu.stepPending()
	if cpu.pendingCount != 0 {
		t.Errorf("after step 4 count = %d, want 0", cpu.pendingCount)
	}
	if cpu.pendingOp != popNone {
		t.Errorf("after step 4 op = %d, want popNone", cpu.pendingOp)
	}
}

// TestPendingClockShortCircuitDuringPending covers A3 - while
// pendingOp != popNone, Clock() must not call processInterrupt or fetch
// the next instruction. A higher-priority DIVU interrupt asserted
// mid-TRAPA must not schedule a second popException until TRAPA
// drains (Hardware Manual Sec 4.1.2 Table 4.2: exceptions "start when
// the previous executing instruction finishes executing").
func TestPendingClockShortCircuitDuringPending(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x200

	// TRAPA #0x10 at PC; vector at VBR + 0x10*4 = 0x140
	bus.Write16(0x200, 0xC310)
	bus.Write32(0x140, 0x500)
	// DIVU vector 0x60 -> handler at VBR + 0x60*4 = 0x280 (well clear of
	// the TRAPA opcode at 0x200 and the TRAPA handler at 0x500).
	bus.Write32(0x100+0x60*4, 0x600)

	// Cycle 1: execute TRAPA EX stage; pendingOp becomes popTRAPA.
	cpu.Clock()
	if cpu.pendingOp != popTRAPA {
		t.Fatalf("after TRAPA dispatch: pendingOp = %d, want popTRAPA", cpu.pendingOp)
	}

	// Now assert DIVU mid-TRAPA. While TRAPA drains the new request
	// must NOT preempt; it should only be accepted after popTRAPA
	// clears (and only on a Clock() that re-enters processInterrupt).
	assertDIVU(cpu, 5, 0x60)

	for cpu.pendingOp == popTRAPA {
		cpu.Clock()
		if cpu.pendingOp == popException {
			t.Fatal("DIVU exception scheduled during TRAPA drain (no preemption allowed)")
		}
	}

	// TRAPA fully drained. PC should be at TRAPA vector now.
	if cpu.reg.PC != 0x500 {
		t.Errorf("after TRAPA: PC = 0x%08X, want 0x500", cpu.reg.PC)
	}

	// The DIVU latch is still asserted; the next Clock() should
	// accept it via processInterrupt and schedule popException.
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("after TRAPA drain + Clock: pendingOp = %d, want popException", cpu.pendingOp)
	}
}

// TestPendingClearsLastLoadReg covers A4 - on every Clock() call that
// dispatches into stepPending, lastLoadReg must be reset so a
// load-use hazard cannot survive across pending steps.
func TestPendingClearsLastLoadReg(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.GBR = 0x200
	cpu.reg.R[0] = 0x10
	bus.Write8(0x210, 0x55)

	// Trigger AND.B #0x0F,@(R0,GBR) = 0xCD0F to enter popMemRMW.
	bus.Write16(cpu.reg.PC, 0xCD0F)
	cpu.Clock() // cycle 1: read + setPending(popMemRMW, 2)
	if cpu.pendingOp != popMemRMW {
		t.Fatalf("expected popMemRMW after AND.B dispatch, got %d", cpu.pendingOp)
	}

	// Seed lastLoadReg with a non-sentinel value before the next
	// stepPending; Clock() must clear it.
	cpu.lastLoadReg = 7
	cpu.Clock()
	if cpu.lastLoadReg != 0xFF {
		t.Errorf("after stepPending: lastLoadReg = %d, want 0xFF (cleared)", cpu.lastLoadReg)
	}
}

// --- Area B: per-popXXX step semantics not covered elsewhere ---

// TestPopStallIsInert verifies popStall produces no register or memory
// effect across its lifetime; only a per-cycle BusNone tick. Used by
// BRA/BSR/JMP/JSR/RTS, BT/BF, MUL family tail, TSTB tail, SLEEP tail.
func TestPopStallIsInert(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.PC = 0x200
	cpu.reg.SR = srTMask | srSMask
	for i := 0; i < 16; i++ {
		cpu.reg.R[i] = uint32(0x100 + i)
	}
	macSnapshot := cpu.reg.MACH
	macLSnapshot := cpu.reg.MACL
	gbr := cpu.reg.GBR
	vbr := cpu.reg.VBR

	cpu.setPending(popStall, 3)
	for i := 0; i < 3; i++ {
		state := cpu.stepPending()
		if state.Bus != BusNone {
			t.Errorf("step %d: bus = %d, want BusNone", i+1, state.Bus)
		}
	}

	if cpu.reg.PC != 0x200 {
		t.Errorf("PC mutated by popStall: 0x%X", cpu.reg.PC)
	}
	if cpu.reg.SR != (srTMask | srSMask) {
		t.Errorf("SR mutated by popStall: 0x%X", cpu.reg.SR)
	}
	if cpu.reg.MACH != macSnapshot || cpu.reg.MACL != macLSnapshot {
		t.Errorf("MAC mutated by popStall: H=0x%X L=0x%X", cpu.reg.MACH, cpu.reg.MACL)
	}
	if cpu.reg.GBR != gbr || cpu.reg.VBR != vbr {
		t.Errorf("GBR/VBR mutated by popStall: GBR=0x%X VBR=0x%X", cpu.reg.GBR, cpu.reg.VBR)
	}
	for i := 0; i < 16; i++ {
		if cpu.reg.R[i] != uint32(0x100+i) {
			t.Errorf("R[%d] mutated: 0x%X", i, cpu.reg.R[i])
		}
	}
}

// TestPopMACWStallStepsAreInert covers Programming Manual Sec 7.2.3
// Table 7.1 (MAC.W 2-3 cycles): when multiplier contention extends
// the pending count beyond 1, the leading stall steps must not run
// the second-MA + accumulate body. Only the final step (pendingCount
// == 1 at entry to stepMACW) performs the read, post-increment, and
// MAC update. This isolates the stall semantics described in the
// stepMACW comment ("If pendingCount > 1, this is a stall step").
func TestPopMACWStallStepsAreInert(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	const n, m = 1, 2
	const rmAddr = uint32(0x200)
	bus.Write16(rmAddr, 4)
	cpu.reg.R[n] = 0x100
	cpu.reg.R[m] = rmAddr
	cpu.reg.MACH = 0
	cpu.reg.MACL = 7
	cpu.pendingVal = 5 // emulates the first-MA result already loaded
	cpu.pendingN = n | (m << 4)

	// Force a stall: count = 2 (one stall step + one final step).
	cpu.setPending(popMACW, 2)

	// Step 1 (stall): no MAC update, no Rm post-increment, no bus.
	state := cpu.stepPending()
	if state.Bus != BusNone {
		t.Errorf("stall step: bus = %d, want BusNone", state.Bus)
	}
	if cpu.reg.MACL != 7 || cpu.reg.MACH != 0 {
		t.Errorf("stall step mutated MAC: H=0x%X L=0x%X", cpu.reg.MACH, cpu.reg.MACL)
	}
	if cpu.reg.R[m] != rmAddr {
		t.Errorf("stall step post-incremented Rm: 0x%X", cpu.reg.R[m])
	}

	// Step 2 (final): performs MA read and accumulate.
	state = cpu.stepPending()
	if state.Bus != BusRead {
		t.Errorf("final step: bus = %d, want BusRead", state.Bus)
	}
	// MAC = 7 + 5*4 = 27.
	if cpu.reg.MACL != 27 {
		t.Errorf("final step MACL = %d, want 27", cpu.reg.MACL)
	}
	if cpu.reg.R[m] != rmAddr+2 {
		t.Errorf("final step R[m] = 0x%X, want 0x%X", cpu.reg.R[m], rmAddr+2)
	}
}

// TestPopMACLStallStepsAreInert mirrors the MAC.W variant for MAC.L
// (Programming Manual Sec 7.2.3 Table 7.1: MAC.L 2-4 cycles). With
// up to 2 leading stall steps, none may modify MAC or Rm.
func TestPopMACLStallStepsAreInert(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	const n, m = 1, 2
	const rmAddr = uint32(0x200)
	bus.Write32(rmAddr, 3)
	cpu.reg.R[n] = 0x100
	cpu.reg.R[m] = rmAddr
	cpu.reg.MACH = 0
	cpu.reg.MACL = 1
	cpu.pendingVal = 6
	cpu.pendingN = n | (m << 4)

	// Force max stall: count = 3 (two stall steps + one final).
	cpu.setPending(popMACL, 3)

	for i := 0; i < 2; i++ {
		state := cpu.stepPending()
		if state.Bus != BusNone {
			t.Errorf("stall step %d: bus = %d, want BusNone", i+1, state.Bus)
		}
		if cpu.reg.MACL != 1 || cpu.reg.MACH != 0 {
			t.Errorf("stall step %d mutated MAC: H=0x%X L=0x%X", i+1, cpu.reg.MACH, cpu.reg.MACL)
		}
		if cpu.reg.R[m] != rmAddr {
			t.Errorf("stall step %d post-incremented Rm: 0x%X", i+1, cpu.reg.R[m])
		}
	}

	state := cpu.stepPending()
	if state.Bus != BusRead {
		t.Errorf("final step: bus = %d, want BusRead", state.Bus)
	}
	// MAC = 1 + 6*3 = 19.
	if cpu.reg.MACL != 19 {
		t.Errorf("final step MACL = %d, want 19", cpu.reg.MACL)
	}
	if cpu.reg.R[m] != rmAddr+4 {
		t.Errorf("final step R[m] = 0x%X, want 0x%X", cpu.reg.R[m], rmAddr+4)
	}
}

// TestPopLDCLLoadsCorrectControlRegister covers the popLDCL CR
// dispatch. The pendingN field encodes the destination CR in its
// upper nibble (0=SR, 1=GBR, 2=VBR). Each variant is set up with the
// CR unique to it and verified after stepPending drains. Hardware
// Manual Sec 8.1.7 LDC.L family.
func TestPopLDCLLoadsCorrectControlRegister(t *testing.T) {
	cases := []struct {
		name string
		op   uint16
		cr   uint8
		read func(*CPU) uint32
	}{
		{"SR", 0x4307, 0, func(c *CPU) uint32 { return c.reg.SR }},
		{"GBR", 0x4317, 1, func(c *CPU) uint32 { return c.reg.GBR }},
		{"VBR", 0x4327, 2, func(c *CPU) uint32 { return c.reg.VBR }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := newTestBus(0x1000)
			cpu := New(bus, true)
			const addr = uint32(0x200)
			cpu.reg.R[3] = addr
			bus.Write32(addr, 0xCAFE0000|uint32(tc.cr))

			cpu.ir = tc.op
			handlerTable[tc.op](cpu)
			if cpu.pendingOp != popLDCL {
				t.Fatalf("expected popLDCL after dispatch, got %d", cpu.pendingOp)
			}

			// Drain the 2 pending steps.
			cpu.stepPending()
			cpu.stepPending()

			got := tc.read(cpu)
			want := uint32(0xCAFE0000) | uint32(tc.cr)
			if tc.cr == 0 { // SR variant masks reserved bits
				want &= srMask
			}
			if got != want {
				t.Errorf("%s = 0x%08X, want 0x%08X", tc.name, got, want)
			}
			if cpu.reg.R[3] != addr+4 {
				t.Errorf("R[3] = 0x%08X, want 0x%08X (post-incremented)", cpu.reg.R[3], addr+4)
			}
		})
	}
}

// --- Area F: state machine corner cases ---

// TestPendingSetPendingOverwriteSilent covers F1. setPending performs
// no guard against overwriting an in-progress pending op. In normal
// scheduling Clock() never reaches an instruction handler while
// pendingOp != popNone, so the overwrite is unreachable from the
// instruction stream; this test pins down the field-level behavior so
// any future scheduling change is detectable.
func TestPendingSetPendingOverwriteSilent(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.setPending(popTRAPA, 7)
	// Advance a couple of steps to non-zero state.
	cpu.pendingStep = 3
	cpu.pendingCount = 4

	cpu.setPending(popException, 4)
	if cpu.pendingOp != popException {
		t.Errorf("pendingOp = %d, want popException (overwrite)", cpu.pendingOp)
	}
	if cpu.pendingStep != 0 {
		t.Errorf("pendingStep = %d, want 0 (reset on overwrite)", cpu.pendingStep)
	}
	if cpu.pendingCount != 4 {
		t.Errorf("pendingCount = %d, want 4 (overwrite)", cpu.pendingCount)
	}
}

// TestPendingCountOneBoundary covers F2 - with setPending(op, 1) the
// first stepPending must (a) advance step to 1 and run the handler's
// "step 1" case body, (b) decrement count to 0, and (c) clear
// pendingOp to popNone. Uses popSTCL which has a single observable
// step (the MA write).
func TestPendingCountOneBoundary(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.pendingAddr = 0x200
	cpu.pendingVal = 0xCAFEBABE
	cpu.setPending(popSTCL, 1)

	state := cpu.stepPending()
	if state.Bus != BusWrite {
		t.Errorf("step 1: bus = %d, want BusWrite", state.Bus)
	}
	if got := bus.Read32(0x200); got != 0xCAFEBABE {
		t.Errorf("mem[0x200] = 0x%08X, want 0xCAFEBABE", got)
	}
	if cpu.pendingCount != 0 {
		t.Errorf("after step: pendingCount = %d, want 0", cpu.pendingCount)
	}
	if cpu.pendingOp != popNone {
		t.Errorf("after step: pendingOp = %d, want popNone", cpu.pendingOp)
	}
}

// TestPendingDMACStallVsPendingMutex covers F3. Clock() checks
// dmac.Stalling() only when pendingOp == popNone; the two paths are
// mutually exclusive. With a TRAPA in flight AND the DMAC reporting a
// stall, Clock() must continue to drain the pending op (TRAPA drains
// to popNone) before any DMAC-stall-only cycles run.
func TestPendingDMACStallVsPendingMutex(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x200
	bus.Write16(0x200, 0xC310)
	bus.Write32(0x140, 0x500)

	cpu.Clock() // dispatch TRAPA EX
	if cpu.pendingOp != popTRAPA {
		t.Fatalf("expected popTRAPA, got %d", cpu.pendingOp)
	}

	// Force the DMAC into a stalling state. We rig the stall counter
	// directly instead of triggering a real transfer because the
	// invariant under test is the dispatch precedence in Clock(),
	// not the DMA semantics.
	cpu.dmac.stallCycles = 8

	// Drain the TRAPA pending op. Each Clock() call must continue to
	// step the pending machine, not the DMAC stall path. We confirm
	// by observing that dmac.stallCycles is unchanged across the
	// drain.
	beforeStall := cpu.dmac.stallCycles
	for cpu.pendingOp != popNone {
		cpu.Clock()
	}
	if cpu.dmac.stallCycles != beforeStall {
		t.Errorf("DMAC stallCycles changed during TRAPA drain: %d -> %d",
			beforeStall, cpu.dmac.stallCycles)
	}

	// Pending complete - subsequent Clock() must now bleed the DMAC
	// stall counter down (one cycle per Clock).
	cpu.Clock()
	if cpu.dmac.stallCycles != beforeStall-1 {
		t.Errorf("post-TRAPA Clock: stallCycles = %d, want %d",
			cpu.dmac.stallCycles, beforeStall-1)
	}
}
