// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// Additional timeline tests: the remaining multi-cycle bodies, a mixed
// program, the bus clock seen by each access, the instruction-boundary
// rules for interrupts and exceptions, halt and peripheral deadlines,
// DMAC completion timing, and reset and save-state behaviour at
// boundaries. Cycle counts come from the Programming Manual Section 5
// tables and Table 7.1 unless a comment cites otherwise.

func encLDCLGBR(m uint16) uint16 { return 0x4017 | (m << 8) }
func encLDCLVBR(m uint16) uint16 { return 0x4027 | (m << 8) }
func encSTCLGBR(n uint16) uint16 { return 0x4013 | (n << 8) }
func encSTCLVBR(n uint16) uint16 { return 0x4023 | (n << 8) }
func encLDSLPR(m uint16) uint16  { return 0x4026 | (m << 8) }
func encSTSLPR(n uint16) uint16  { return 0x4022 | (n << 8) }
func encBTS(disp uint8) uint16   { return 0x8D00 | uint16(disp) }
func encBFS(disp uint8) uint16   { return 0x8F00 | uint16(disp) }
func encDT(n uint16) uint16      { return 0x4010 | (n << 8) }
func encMOVI(imm uint8, n uint16) uint16 {
	return 0xE000 | (n << 8) | uint16(imm)
}

const opcIllegal = 0x0000

// installIRLHandler points IRL vector 0x40 at 0x600 with NOP; RTE; NOP
// and clears the line on acceptance.
func (tl *timeline) installIRLHandler() {
	tl.bus.Write32(0x400+0x40*4, 0x600)
	tl.code(0x600, opcNOP, opcRTE, opcNOP)
	tl.cpu.SetIRLAck(func(uint16) { tl.cpu.ClearIRL() })
}

var irlAccept = []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x500, 4)}

// TestTimelineControlRegisterMemoryOps: LDC.L is 3 cycles and STC.L 2
// for GBR and VBR as for SR; LDS.L and STS.L to PR, MACH, and MACL are
// 1 cycle (Table 7.1: memory load, memory store, memory to MAC and MAC
// to memory transfers). Each performs exactly one longword access.
func TestTimelineControlRegisterMemoryOps(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.cpu.reg.GBR = 0x1234
	tl.cpu.reg.PR = 0x5678
	tl.cpu.reg.MACH = 0x9ABC
	tl.cpu.reg.MACL = 0xDEF0

	tl.code(0x100,
		encSTCLGBR(15),
		encSTCLVBR(15),
		encLDCLVBR(15),
		encLDCLGBR(15),
		encSTSLPR(15),
		encSTSLMACH(15),
		encSTSLMACL(15),
		encLDSLMACL(15),
		encLDSLMACH(15),
		encLDSLPR(15),
		opcNOP,
	)
	tl.run(11)
	tl.check([]instrExpect{
		plain(0x100, encSTCLGBR(15), 2, wr(0xFFC, 4)),
		plain(0x102, encSTCLVBR(15), 2, wr(0xFF8, 4)),
		plain(0x104, encLDCLVBR(15), 3, rd(0xFF8, 4)),
		plain(0x106, encLDCLGBR(15), 3, rd(0xFFC, 4)),
		plain(0x108, encSTSLPR(15), 1, wr(0xFFC, 4)),
		plain(0x10A, encSTSLMACH(15), 1, wr(0xFF8, 4)),
		plain(0x10C, encSTSLMACL(15), 1, wr(0xFF4, 4)),
		plain(0x10E, encLDSLMACL(15), 1, rd(0xFF4, 4)),
		plain(0x110, encLDSLMACH(15), 1, rd(0xFF8, 4)),
		plain(0x112, encLDSLPR(15), 1, rd(0xFFC, 4)),
		plain(0x114, opcNOP, 1),
	})
	r := tl.cpu.reg
	if r.GBR != 0x1234 || r.VBR != 0x400 || r.PR != 0x5678 || r.MACH != 0x9ABC || r.MACL != 0xDEF0 || r.R[15] != 0x1000 {
		t.Errorf("registers not restored: GBR=%X VBR=%X PR=%X MACH=%X MACL=%X R15=%X",
			r.GBR, r.VBR, r.PR, r.MACH, r.MACL, r.R[15])
	}
}

// TestTimelineDelayedConditionalAndUnsignedMultiply: Table 7.1 gives a
// delayed conditional branch 2 cycles taken and 1 not taken; MULU.W is
// 1 cycle and DMULU.L 2 with the multiplier free (two unrelated
// instructions after MULU.W outlast its 2-cycle mm).
func TestTimelineDelayedConditionalAndUnsignedMultiply(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.cpu.reg.R[1] = 3
	tl.cpu.reg.R[2] = 7
	tl.code(0x100,
		opcSETT,         // 0x100
		encBTS(2),       // 0x102: BT/S 0x10A (taken)
		opcNOP,          // 0x104: delay slot
		opcNOP,          // 0x106 (skipped)
		opcNOP,          // 0x108 (skipped)
		encBFS(1),       // 0x10A: BF/S 0x110, T=1 so not taken
		opcNOP,          // 0x10C
		encMULUW(1, 2),  // 0x10E
		opcNOP,          // 0x110
		opcNOP,          // 0x112
		encDMULUL(1, 2), // 0x114
		opcNOP,          // 0x116
	)
	tl.run(10)
	tl.check([]instrExpect{
		plain(0x100, opcSETT, 1),
		plain(0x102, encBTS(2), 2),
		plain(0x104, opcNOP, 1),
		plain(0x10A, encBFS(1), 1),
		plain(0x10C, opcNOP, 1),
		plain(0x10E, encMULUW(1, 2), 1),
		plain(0x110, opcNOP, 1),
		plain(0x112, opcNOP, 1),
		plain(0x114, encDMULUL(1, 2), 2),
		plain(0x116, opcNOP, 1),
	})
	if tl.cpu.reg.MACL != 21 {
		t.Errorf("MACL = %d, want 21", tl.cpu.reg.MACL)
	}
}

// TestTimelineMixedProgram: a four-iteration loop mixing a load-use
// pair, a multiply, MAC register access, a store, TAS, DT, and a
// delayed conditional branch, followed by TRAPA and RTE. Every entry
// cycle is the sum of the documented costs before it.
func TestTimelineMixedProgram(t *testing.T) {
	tl := newTimeline(t, 0x300)
	tl.cpu.reg.R[1] = 0x800
	tl.cpu.reg.R[6] = 0x900
	tl.bus.Write32(0x800, 3)
	tl.bus.Write32(0x400+0x20*4, 0x200)

	tl.code(0x100,
		encMOVI(4, 3),    // 0x100
		encMOVLL(1, 2),   // 0x102: loop
		encADD(2, 4),     // 0x104: uses R2 (split)
		encMULL(2, 4),    // 0x106
		encSTSMACL(5),    // 0x108
		encMOVLMD(5, 15), // 0x10A: MOV.L R5,@-R15
		encTAS(6),        // 0x10C
		encDT(3),         // 0x10E
		encBFS(0xF7),     // 0x110: BF/S loop (0x102)
		opcNOP,           // 0x112: delay slot
		encTRAPA(0x20),   // 0x114
		opcNOP,           // 0x116
	)
	tl.code(0x200, opcNOP, opcRTE, opcNOP)

	var want []instrExpect
	push := func(pc uint32, op uint16, cost int, ops ...busOp) {
		want = append(want, plain(pc, op, cost, ops...))
	}
	push(0x100, encMOVI(4, 3), 1)
	sp := uint32(0x1000)
	for it := 0; it < 4; it++ {
		push(0x102, encMOVLL(1, 2), 2, rd(0x800, 4))
		push(0x104, encADD(2, 4), 1)
		push(0x106, encMULL(2, 4), 2)
		push(0x108, encSTSMACL(5), 1)
		sp -= 4
		push(0x10A, encMOVLMD(5, 15), 1, wr(sp, 4))
		push(0x10C, encTAS(6), 4, rmwr(0x900), rmww(0x900))
		push(0x10E, encDT(3), 1)
		if it < 3 {
			push(0x110, encBFS(0xF7), 2)
		} else {
			push(0x110, encBFS(0xF7), 1)
		}
		push(0x112, opcNOP, 1)
	}
	push(0x114, encTRAPA(0x20), 8, wr(sp-4, 4), wr(sp-8, 4), rd(0x480, 4))
	push(0x200, opcNOP, 1)
	push(0x202, opcRTE, 4, rd(sp-8, 4), rd(sp-4, 4))
	push(0x204, opcNOP, 1)
	push(0x116, opcNOP, 1)

	tl.run(len(want))
	tl.check(want)
	if tl.cpu.reg.R[4] != 12 || tl.cpu.reg.MACL != 36 {
		t.Errorf("R4 = %d MACL = %d, want 12 and 36 (3 added per iteration, last product 3*12)", tl.cpu.reg.R[4], tl.cpu.reg.MACL)
	}
	if tl.cpu.reg.R[15] != sp {
		t.Errorf("R15 = %04X, want %04X", tl.cpu.reg.R[15], sp)
	}
}

// TestTimelineBusClockPerAccess: every access hands the bus the CPU's
// cycle counter at the cycle it is made in, so inter-CPU contention is
// timed against the moment of the access, not the instruction's start. The
// offsets within multi-cycle instructions follow the pipeline stage the
// access belongs to: TAS reads at its first MA and writes at its third
// (Section 7.4.3 TAS pipeline), STC.L writes and LDC.L reads at their
// MA, RTE reads PC then SR at its two MAs (Figure 7.92), TRAPA writes
// SR and PC then reads the vector (Figure 7.93), and an interrupt
// acceptance does the same at its MAs (Figure 7.95). Single-cycle
// loads, stores, the first MAC operand, and the memory logic read are
// charged in the EX cycle.
func TestTimelineBusClockPerAccess(t *testing.T) {
	tl := newTimeline(t, 0x300)
	tl.installIRLHandler()
	tl.cpu.reg.GBR = 0x800
	tl.cpu.reg.R[1] = 0x800
	tl.cpu.reg.R[3] = 0x900
	tl.cpu.reg.R[4] = 0x820
	tl.cpu.reg.R[5] = 0x830
	tl.bus.Write32(0x400+0x20*4, 0x200)

	tl.code(0x100,
		encMOVLL(1, 2), // 0x100
		encTAS(3),      // 0x102
		encSTCLSR(15),  // 0x104
		encLDCLSR(15),  // 0x106
		encANDB(0x0F),  // 0x108
		encMACW(4, 5),  // 0x10A
		encTRAPA(0x20), // 0x10C
		opcNOP,         // 0x10E
		opcNOP,         // 0x110
	)
	tl.code(0x200, opcRTE, opcNOP)

	tl.run(9) // through the RTE delay slot
	tl.cpu.SetIRL(5, 0x40)
	tl.run(1) // acceptance before the NOP at 0x10E

	wantOffsets := [][]int64{
		{0},       // MOV.L
		{1, 3},    // TAS
		{1},       // STC.L
		{1},       // LDC.L
		{0, 2},    // AND.B read, write
		{0, 1},    // MAC.W
		{1, 2, 4}, // TRAPA SR, PC, vector
		{1, 2},    // RTE PC, SR
		{},        // NOP slot
		{2, 3, 5}, // interrupt SR, PC, vector
	}
	if len(tl.recs) != len(wantOffsets) {
		t.Fatalf("%d records, want %d", len(tl.recs), len(wantOffsets))
	}
	idx := 0
	for i, r := range tl.recs {
		got := make([]int64, len(r.ops))
		for k := range r.ops {
			got[k] = tl.bus.clocks[idx].now - int64(r.entry)
			idx++
		}
		if len(got) != len(wantOffsets[i]) {
			t.Errorf("[%d] %04X: %d accesses, want %d", i, r.pc, len(got), len(wantOffsets[i]))
			continue
		}
		for k := range got {
			if got[k] != wantOffsets[i][k] {
				t.Errorf("[%d] %04X access %d at entry+%d, want +%d", i, r.pc, k, got[k], wantOffsets[i][k])
			}
		}
	}
}

// TestTimelineFetchStallAttribution: with the cache off every fetch is
// an external access. Wait states on a fetch are charged to the
// instruction fetched, after its EX and before the next instruction's
// EX, and a load pays its own data wait states in the same way. With 3
// wait states per read a NOP costs 4 and a longword load 7.
func TestTimelineFetchStallAttribution(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.bus.stallRead = 3
	tl.cpu.reg.R[1] = 0x800
	tl.code(0x100, opcNOP, opcNOP, encMOVLL(1, 2), opcNOP)
	tl.run(4)
	tl.check([]instrExpect{
		{pc: 0x100, op: opcNOP, cost: 4, entry: 0},
		{pc: 0x102, op: opcNOP, cost: 4, entry: 4},
		{pc: 0x104, op: encMOVLL(1, 2), cost: 7, entry: 8, ops: []busOp{rd(0x800, 4)}},
		{pc: 0x106, op: opcNOP, cost: 4, entry: 15},
	})
}

// TestTimelineHLETrap: a jump into the HLE magic range runs the hook
// with no fetch and no cycle charge, and execution resumes at PR as if
// RTS had executed.
func TestTimelineHLETrap(t *testing.T) {
	tl := newTimeline(t, 0x200)
	called := 0
	tl.cpu.HLEHook = func(pc uint32) {
		called++
		if pc != 0xA0000010 {
			t.Errorf("hook pc = %08X, want A0000010", pc)
		}
	}
	tl.cpu.reg.R[0] = 0xA0000010
	tl.code(0x100, encJSR(0), opcNOP, opcNOP)
	tl.run(3)
	tl.check([]instrExpect{
		{pc: 0x100, op: encJSR(0), cost: 2, entry: 0},
		{pc: 0x102, op: opcNOP, cost: 1, entry: 2},
		{pc: 0x104, op: opcNOP, cost: 1, entry: 3},
	})
	if called != 1 {
		t.Errorf("hook called %d times, want 1", called)
	}
}

// hookOnce installs a bus access hook that fires f the first time an
// access of the given kind and address is logged.
func (tl *timeline) hookOnce(kind string, addr uint32, f func()) {
	fired := false
	tl.bus.onAccess = func(op busOp) {
		if !fired && op.kind == kind && op.addr == addr {
			fired = true
			f()
		}
	}
}

// TestTimelineIRLMidInstruction: an IRL asserted while a multi-cycle
// instruction is in progress is accepted at the boundary after it with
// the following instruction as the return address (Hardware Manual
// Section 5.5: the CPU waits for completion of the sequence currently
// being executed). After LDC.L the acceptance is delayed one more
// instruction (Programming Manual Section 4.6.2).
func TestTimelineIRLMidInstruction(t *testing.T) {
	t.Run("TAS", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.installIRLHandler()
		tl.cpu.reg.R[1] = 0x900
		tl.code(0x100, opcNOP, encTAS(1), opcNOP, opcNOP)
		tl.hookOnce("rmwr", 0x900, func() { tl.cpu.SetIRL(5, 0x40) })
		tl.run(7)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, encTAS(1), 4, rmwr(0x900), rmww(0x900)),
			{pc: 0x104, op: opAccept, cost: 9, entry: 5, ops: irlAccept},
			plain(0x600, opcNOP, 1),
			plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
			plain(0x604, opcNOP, 1),
			plain(0x104, opcNOP, 1),
		})
	})
	t.Run("TRAPA", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.installIRLHandler()
		tl.bus.Write32(0x400+0x20*4, 0x200)
		tl.code(0x100, opcNOP, encTRAPA(0x20), opcNOP)
		tl.code(0x200, opcNOP, opcRTE, opcNOP)
		tl.hookOnce("w", 0xFFC, func() { tl.cpu.SetIRL(5, 0x40) })
		tl.run(10)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, encTRAPA(0x20), 8, wr(0xFFC, 4), wr(0xFF8, 4), rd(0x480, 4)),
			{pc: 0x200, op: opAccept, cost: 9, entry: 9, ops: []busOp{wr(0xFF4, 4), wr(0xFF0, 4), rd(0x500, 4)}},
			plain(0x600, opcNOP, 1),
			plain(0x602, opcRTE, 4, rd(0xFF0, 4), rd(0xFF4, 4)),
			plain(0x604, opcNOP, 1),
			plain(0x200, opcNOP, 1),
			plain(0x202, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
			plain(0x204, opcNOP, 1),
			plain(0x104, opcNOP, 1),
		})
	})
	t.Run("LDC.L", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.installIRLHandler()
		tl.cpu.reg.R[15] = 0xFFC
		tl.bus.Write32(0xFFC, 0)
		tl.code(0x100, opcNOP, encLDCLSR(15), opcNOP, opcNOP, opcNOP)
		tl.hookOnce("r", 0xFFC, func() { tl.cpu.SetIRL(5, 0x40) })
		tl.run(8)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, encLDCLSR(15), 3, rd(0xFFC, 4)),
			plain(0x104, opcNOP, 1),
			{pc: 0x106, op: opAccept, cost: 9, entry: 5, ops: irlAccept},
			plain(0x600, opcNOP, 1),
			plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
			plain(0x604, opcNOP, 1),
			plain(0x106, opcNOP, 1),
		})
	})
	t.Run("MAC.L", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.installIRLHandler()
		tl.cpu.reg.R[3] = 0x800
		tl.cpu.reg.R[4] = 0x820
		tl.code(0x100, opcNOP, encMACL(3, 4), opcNOP, opcNOP)
		tl.hookOnce("r", 0x820, func() { tl.cpu.SetIRL(5, 0x40) })
		tl.run(7)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, encMACL(3, 4), 2, rd(0x820, 4), rd(0x800, 4)),
			{pc: 0x104, op: opAccept, cost: 9, entry: 3, ops: irlAccept},
			plain(0x600, opcNOP, 1),
			plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
			plain(0x604, opcNOP, 1),
			plain(0x104, opcNOP, 1),
		})
	})
}

// TestTimelineRequestDuringExceptionSequence: a higher-level IRL or an
// NMI arriving while an interrupt exception sequence is running is
// accepted at the first boundary after it, before the handler's first
// instruction, with the handler address as the return PC.
func TestTimelineRequestDuringExceptionSequence(t *testing.T) {
	t.Run("IRL", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.installIRLHandler()
		tl.bus.Write32(0x400+0x41*4, 0x610)
		tl.code(0x610, opcNOP, opcRTE, opcNOP)
		tl.code(0x100, opcNOP, opcNOP, opcNOP, opcNOP)
		tl.run(2)
		tl.cpu.SetIRL(5, 0x40)
		tl.hookOnce("w", 0xFFC, func() { tl.cpu.SetIRL(7, 0x41) })
		tl.run(9)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, opcNOP, 1),
			{pc: 0x104, op: opAccept, cost: 9, entry: 2, ops: irlAccept},
			{pc: 0x600, op: opAccept, cost: 9, entry: 11, ops: []busOp{wr(0xFF4, 4), wr(0xFF0, 4), rd(0x504, 4)}},
			{pc: 0x610, op: opcNOP, cost: 1, entry: 20},
			plain(0x612, opcRTE, 4, rd(0xFF0, 4), rd(0xFF4, 4)),
			plain(0x614, opcNOP, 1),
			plain(0x600, opcNOP, 1),
			plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
			plain(0x604, opcNOP, 1),
			plain(0x104, opcNOP, 1),
		})
	})
	t.Run("NMI", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.installIRLHandler()
		tl.bus.Write32(0x400+11*4, 0x700)
		tl.code(0x700, opcNOP, opcRTE, opcNOP)
		tl.code(0x100, opcNOP, opcNOP, opcNOP, opcNOP)
		tl.run(2)
		tl.cpu.SetIRL(5, 0x40)
		tl.hookOnce("w", 0xFFC, func() { tl.cpu.NMI() })
		tl.run(9)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, opcNOP, 1),
			{pc: 0x104, op: opAccept, cost: 9, entry: 2, ops: irlAccept},
			{pc: 0x600, op: opAccept, cost: 9, entry: 11, ops: []busOp{wr(0xFF4, 4), wr(0xFF0, 4), rd(0x42C, 4)}},
			{pc: 0x700, op: opcNOP, cost: 1, entry: 20},
			plain(0x702, opcRTE, 4, rd(0xFF0, 4), rd(0xFF4, 4)),
			plain(0x704, opcNOP, 1),
			plain(0x600, opcNOP, 1),
			plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
			plain(0x604, opcNOP, 1),
			plain(0x104, opcNOP, 1),
		})
	})
}

// TestTimelineSynchronousExceptionCost: Figure 7.96 gives the address
// error sequence the same ten stages as an interrupt, so 9 cycles from
// the faulting instruction's EX to the handler's first EX; Figure 7.97
// gives the illegal instruction sequence nine stages, so 8. Hardware
// Manual Table 4.11: the address error stacks the address of the
// instruction after the executed one, the illegal instruction stacks
// its own start address. Both save SR then PC and read the vector.
func TestTimelineSynchronousExceptionCost(t *testing.T) {
	t.Run("AddressError", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.bus.Write32(0x400+9*4, 0x600)
		tl.code(0x600, opcNOP)
		tl.cpu.reg.R[1] = 0x801
		tl.code(0x100, opcNOP, encMOVLL(1, 2), opcNOP)
		tl.run(3)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, encMOVLL(1, 2), 9, wr(0xFFC, 4), wr(0xFF8, 4), rd(0x424, 4)),
			plain(0x600, opcNOP, 1),
		})
		if got := tl.bus.Read32(0xFF8); got != 0x104 {
			t.Errorf("stacked PC = %08X, want 00000104 (instruction after the faulting one)", got)
		}
	})
	t.Run("IllegalInstruction", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.bus.Write32(0x400+4*4, 0x600)
		tl.code(0x600, opcNOP)
		tl.code(0x100, opcNOP, opcIllegal, opcNOP)
		tl.run(3)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			plain(0x102, opcIllegal, 8, wr(0xFFC, 4), wr(0xFF8, 4), rd(0x410, 4)),
			plain(0x600, opcNOP, 1),
		})
		if got := tl.bus.Read32(0xFF8); got != 0x102 {
			t.Errorf("stacked PC = %08X, want 00000102 (start of the illegal instruction)", got)
		}
	})
}

// frtMatchAt4 arms an OCRA match at FRC 4 (cycle 32 at phi/8) with the
// OCI vector 0x41 at level 8 and a NOP handler at 0x600.
func (tl *timeline) frtMatchAt4(enableInterrupt bool) {
	tl.installHandlerVector(0x41)
	tl.cpu.writeOnChip(0xFFFFFE60, 0x0800)
	tl.cpu.writeOnChip(0xFFFFFE66, 0x0041)
	tl.cpu.writeOnChip(0xFFFFFE14, 0x00)
	tl.cpu.writeOnChip(0xFFFFFE15, 0x04)
	if enableInterrupt {
		tl.cpu.writeOnChip(0xFFFFFE10, 0x08)
	}
}

var (
	frtAccept   = []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x504, 4)}
	wdtAccept   = []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x508, 4)}
	dmac0Accept = []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x50C, 4)}
	dmac1Accept = []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x510, 4)}
)

// TestTimelineSleepWakeByPeripheral: a halted CPU is woken by an
// on-chip peripheral interrupt at the cycle its deadline fires, with
// the instruction after SLEEP as the return address.
func TestTimelineSleepWakeByPeripheral(t *testing.T) {
	t.Run("FRT", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.frtMatchAt4(true)
		tl.code(0x100, opcNOP, opcSLEEP)
		tl.run(2)
		tl.idleUntilRecord(100)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			{pc: 0x102, op: opcSLEEP, cost: unchecked, entry: 1},
			{pc: 0x104, op: opAccept, cost: unchecked, entry: 32, ops: frtAccept},
		})
	})
	t.Run("WDT", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.installHandlerVector(0x42)
		tl.cpu.writeOnChip(0xFFFFFEE2, 0x0080)
		tl.cpu.writeOnChip(0xFFFFFEE4, 0x4200)
		tl.cpu.writeOnChip(0xFFFFFE80, 0x5AFC)
		tl.cpu.writeOnChip(0xFFFFFE80, 0xA520)
		tl.code(0x100, opcNOP, opcSLEEP)
		tl.run(2)
		tl.idleUntilRecord(100)
		tl.check([]instrExpect{
			plain(0x100, opcNOP, 1),
			{pc: 0x102, op: opcSLEEP, cost: unchecked, entry: 1},
			{pc: 0x104, op: opAccept, cost: unchecked, entry: 8, ops: wdtAccept},
		})
	})
	t.Run("DMAC", func(t *testing.T) {
		tl := dmacProgram(t, chcrLongIncr|chcrIE)
		tl.code(0x104, opcSLEEP)
		tl.run(3)
		tl.idleUntilRecord(100)
		tl.check([]instrExpect{
			{pc: 0x100, op: opcNOP, cost: 1, entry: 0},
			{pc: 0x102, op: encMOVLS(1, 2), cost: 1, entry: 1},
			{pc: 0x104, op: opcSLEEP, cost: unchecked, entry: 2},
			{pc: 0x106, op: opAccept, cost: unchecked, entry: dmaTECycle, ops: dmac0Accept},
		})
	})
}

// TestTimelineSleepDeadlineWithoutInterruptStaysHalted: a peripheral
// deadline whose interrupt is not enabled sets its flag and leaves the
// halted CPU halted; a later IRL wakes it.
func TestTimelineSleepDeadlineWithoutInterruptStaysHalted(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.frtMatchAt4(false)
	tl.installIRLHandler()
	tl.code(0x100, opcNOP, opcSLEEP)
	tl.run(2)
	if n := tl.idleUntilRecord(60); n != 60 {
		t.Fatalf("record opened after %d idle cycles, want none", n)
	}
	if !tl.cpu.halted {
		t.Fatal("CPU not halted after the unarmed FRT match")
	}
	if tl.cpu.frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA not set by the match while halted")
	}
	tl.cpu.SetIRL(5, 0x40)
	tl.idleUntilRecord(10)
	tl.check([]instrExpect{
		plain(0x100, opcNOP, 1),
		{pc: 0x102, op: opcSLEEP, cost: unchecked, entry: 1},
		{pc: 0x104, op: opAccept, cost: unchecked, entry: 64, ops: irlAccept},
	})
}

// TestTimelineDeadlineInsideInstruction: an FRT match at cycle 32 that
// lands inside a wait-state drain, inside TRAPA, or inside a burst DMA
// stall is accepted at the first instruction boundary after it.
func TestTimelineDeadlineInsideInstruction(t *testing.T) {
	t.Run("WaitStateDrain", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.frtMatchAt4(true)
		tl.bus.stallRead = 4
		tl.cpu.reg.R[1] = 0x800
		// Code runs from the cache data array so fetches make no bus
		// access; only the load pays wait states.
		for i := 0; i < 30; i++ {
			tl.cpu.cacheData[0x100+i*2] = 0x00
			tl.cpu.cacheData[0x101+i*2] = 0x09
		}
		load := encMOVLL(1, 2)
		tl.cpu.cacheData[0x13C] = uint8(load >> 8)
		tl.cpu.cacheData[0x13D] = uint8(load)
		tl.cpu.cacheData[0x13E] = 0x00
		tl.cpu.cacheData[0x13F] = 0x09
		tl.cpu.reg.PC = 0xC0000100
		tl.run(32)
		r := tl.recs[30]
		if r.pc != 0xC000013C || r.entry != 30 || tl.cost(30) != 5 {
			t.Errorf("load at pc=%08X entry=%d cost=%d, want C000013C/30/5", r.pc, r.entry, tl.cost(30))
		}
		a := tl.recs[31]
		if a.op != opAccept || a.pc != 0xC000013E || a.entry != 35 {
			t.Errorf("acceptance pc=%08X op=%04X entry=%d, want C000013E/FFFF/35", a.pc, a.op, a.entry)
		}
	})
	t.Run("TRAPA", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		tl.frtMatchAt4(true)
		tl.bus.Write32(0x400+0x20*4, 0x200)
		tl.code(0x200, opcNOP)
		tl.fillNOPs(0x100, 30)
		tl.code(0x13C, encTRAPA(0x20))
		tl.run(32)
		a := tl.recs[31]
		if a.op != opAccept || a.pc != 0x200 || a.entry != 38 {
			t.Errorf("acceptance pc=%04X op=%04X entry=%d, want 0200/FFFF/38", a.pc, a.op, a.entry)
		}
	})
	t.Run("BurstDMA", func(t *testing.T) {
		tl := dmacProgram(t, chcrLongIncr|chcrTB)
		tl.frtMatchAt4(true)
		tl.fillNOPs(0x100, 30)
		tl.code(0x13C, encMOVLS(1, 2))
		tl.fillNOPs(0x13E, 4)
		tl.run(32)
		k := tl.recs[30]
		if k.pc != 0x13C || tl.cost(30) != 17 {
			t.Errorf("kick at pc=%04X cost=%d, want 013C/17", k.pc, tl.cost(30))
		}
		a := tl.recs[31]
		if a.op != opAccept || a.pc != 0x13E || a.entry != 47 || !opsEqual(a.ops, frtAccept) {
			t.Errorf("acceptance pc=%04X op=%04X entry=%d ops=%s, want 013E/FFFF/47 FRT", a.pc, a.op, a.entry, fmtOps(a.ops))
		}
	})
}

// dmacTwoChannels prepares both channels with DEI vectors 0x43 and
// 0x44 at level 8, R1/R2 for the channel 0 CHCR write, R3/R4 for
// channel 1, and R7 as a CHCR0 value with DE and TE clear. The handler
// at 0x600 clears channel 0's TE then returns.
func dmacTwoChannels(t *testing.T, tcr0, tcr1 uint32, chcr0, chcr1 uint32) *timeline {
	t.Helper()
	tl := newTimeline(t, 0x800)
	tl.bus.Write32(0x400+0x43*4, 0x600)
	tl.bus.Write32(0x400+0x44*4, 0x600)
	tl.code(0x600, encMOVLS(7, 2), opcRTE, opcNOP)
	tl.cpu.writeOnChip(0xFFFFFEE2, 0x0800)
	d := &tl.cpu.dmac
	d.ch[0].sar, d.ch[0].dar, d.ch[0].tcr, d.ch[0].vcrdma = 0x800, 0x900, tcr0, 0x43
	d.ch[1].sar, d.ch[1].dar, d.ch[1].tcr, d.ch[1].vcrdma = 0xA00, 0xB00, tcr1, 0x44
	d.dmaor = 1
	tl.cpu.reg.R[1] = chcr0
	tl.cpu.reg.R[2] = 0xFFFFFF8C
	tl.cpu.reg.R[3] = chcr1
	tl.cpu.reg.R[4] = 0xFFFFFF9C
	tl.cpu.reg.R[7] = chcrLongIncr &^ 1
	return tl
}

// TestTimelineDMACBothChannelsEndSameCycle: two cycle-steal transfers
// whose occupation windows close on the same cycle raise both DEIs.
// Channel 0 is accepted first (fixed priority, Hardware Manual Section
// 9.3.5) and channel 1 after the handler's RTE lowers the mask.
func TestTimelineDMACBothChannelsEndSameCycle(t *testing.T) {
	// ch0: 5 units (20 cycles) kicked at entry 1; ch1: 4 units (16
	// cycles) kicked at entry 5; both windows close at cycle 22.
	tl := dmacTwoChannels(t, 5, 4, chcrLongIncr|chcrIE, chcrLongIncr|chcrIE)
	tl.code(0x100, opcNOP, encMOVLS(1, 2), opcNOP, opcNOP, opcNOP, encMOVLS(3, 4))
	tl.fillNOPs(0x10C, 32)
	tl.run(22 + 6)
	want := []instrExpect{
		{pc: 0x100, op: opcNOP, cost: 1, entry: 0},
		{pc: 0x102, op: encMOVLS(1, 2), cost: 1, entry: 1},
		{pc: 0x104, op: opcNOP, cost: 1, entry: 2},
		{pc: 0x106, op: opcNOP, cost: 1, entry: 3},
		{pc: 0x108, op: opcNOP, cost: 1, entry: 4},
		{pc: 0x10A, op: encMOVLS(3, 4), cost: 1, entry: 5},
	}
	for i := 6; i < 22; i++ {
		want = append(want, instrExpect{pc: 0x100 + uint32(i)*2, op: opcNOP, cost: 1, entry: i})
	}
	want = append(want,
		instrExpect{pc: 0x12C, op: opAccept, cost: 9, entry: 22, ops: dmac0Accept},
		instrExpect{pc: 0x600, op: encMOVLS(7, 2), cost: 1, entry: 31},
		plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
		plain(0x604, opcNOP, 1),
		instrExpect{pc: 0x12C, op: opAccept, cost: 9, entry: 37, ops: dmac1Accept},
		instrExpect{pc: 0x600, op: encMOVLS(7, 2), cost: 1, entry: 46},
	)
	tl.check(want)
}

// TestTimelineDMACMaximumCount: TCR = 0 transfers 16,777,216 units
// (Hardware Manual Section 9.2.3), so the occupation window is four
// bus cycles per byte unit on the test bus and TE stays clear long
// after the kick.
func TestTimelineDMACMaximumCount(t *testing.T) {
	tl := newTimeline(t, 0x800)
	d := &tl.cpu.dmac
	d.ch[0].sar, d.ch[0].dar, d.ch[0].tcr = 0x800, 0x900, 0
	d.dmaor = 1
	tl.cpu.writeOnChip(0xFFFFFF8C, 0x0001) // fixed addresses, byte, cycle-steal, DE
	tl.fillNOPs(0x100, 100)
	tl.run(100)
	if d.ch[0].chcr&2 != 0 || !d.Active() {
		t.Errorf("TE=%d active=%v after 100 cycles, want TE clear and active", d.ch[0].chcr&2, d.Active())
	}
}

// TestTimelineDMACBurstKickInDelaySlot: a burst transfer kicked by the
// delay slot instruction stalls the CPU before the branch target
// executes, and DEI is accepted before the target with the target as
// the return address.
func TestTimelineDMACBurstKickInDelaySlot(t *testing.T) {
	tl := dmacProgram(t, chcrLongIncr|chcrTB|chcrIE)
	tl.code(0x100, opcNOP, encBRA(1), encMOVLS(1, 2), opcNOP, opcNOP, opcNOP)
	tl.run(5)
	tl.check([]instrExpect{
		{pc: 0x100, op: opcNOP, cost: 1, entry: 0},
		{pc: 0x102, op: encBRA(1), cost: 2, entry: 1},
		{pc: 0x104, op: encMOVLS(1, 2), cost: 1 + 16, entry: 3},
		{pc: 0x108, op: opAccept, cost: 9, entry: 20, ops: dmac0Accept},
		plain(0x600, opcNOP, unchecked),
	})
}

// TestTimelineDMACCycleStealEndsDuringBurst: the two channels count
// independently, so a cycle-steal window that closes while the other
// channel's burst is holding the CPU sets TE during the stall, and
// its DEI is accepted at the first boundary after the stall, ahead of
// the burst channel's DEI by priority.
func TestTimelineDMACCycleStealEndsDuringBurst(t *testing.T) {
	// ch0: 2 units (8 cycles) cycle-steal at entry 1, closing at 10.
	// ch1: 4 units (16 cycles) burst at entry 2, stalling 3-18.
	tl := dmacTwoChannels(t, 2, 4, chcrLongIncr|chcrIE, chcrLongIncr|chcrTB|chcrIE)
	tl.code(0x100, opcNOP, encMOVLS(1, 2), encMOVLS(3, 4))
	tl.fillNOPs(0x106, 8)
	tl.run(9)
	tl.check([]instrExpect{
		{pc: 0x100, op: opcNOP, cost: 1, entry: 0},
		{pc: 0x102, op: encMOVLS(1, 2), cost: 1, entry: 1},
		{pc: 0x104, op: encMOVLS(3, 4), cost: 1 + 16, entry: 2},
		{pc: 0x106, op: opAccept, cost: 9, entry: 19, ops: dmac0Accept},
		{pc: 0x600, op: encMOVLS(7, 2), cost: 1, entry: 28},
		plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
		plain(0x604, opcNOP, 1),
		{pc: 0x106, op: opAccept, cost: 9, entry: 34, ops: dmac1Accept},
		{pc: 0x600, op: encMOVLS(7, 2), cost: 1, entry: 43},
	})
}

// TestTimelineResetMidFlight: Reset abandons whatever is in progress
// (a burst DMA stall, a halt) and the next instruction executes from
// the reset vector with no residual state. Instructions complete
// atomically, so a reset after TRAPA finds nothing of it in flight.
func TestTimelineResetMidFlight(t *testing.T) {
	prepareVectors := func(tl *timeline) {
		tl.bus.Write32(0, 0x300)
		tl.bus.Write32(4, 0x1000)
		tl.code(0x300, opcNOP, opcNOP)
	}
	afterReset := func(tl *timeline) {
		tl.t.Helper()
		c := tl.cpu
		if c.halted || c.dmac.Active() {
			tl.t.Errorf("residual state after Reset: halted=%v dma=%v",
				c.halted, c.dmac.Active())
		}
		if c.reg.PC != 0x300 || c.reg.R[15] != 0x1000 {
			tl.t.Errorf("PC=%X R15=%X after Reset, want 300/1000", c.reg.PC, c.reg.R[15])
		}
		tl.opCursor = len(tl.bus.ops)
		n := len(tl.recs)
		tl.run(1)
		r := tl.recs[n]
		if r.pc != 0x300 || r.op != opcNOP || tl.cost(n) != 1 || len(r.ops) != 0 {
			tl.t.Errorf("first instruction after Reset: pc=%X op=%04X cost=%d ops=%s", r.pc, r.op, tl.cost(n), fmtOps(r.ops))
		}
	}
	t.Run("AfterTRAPA", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		prepareVectors(tl)
		tl.bus.Write32(0x400+0x20*4, 0x200)
		tl.code(0x100, opcNOP, encTRAPA(0x20))
		tl.code(0x200, opcNOP)
		tl.run(2)
		tl.cpu.Reset()
		afterReset(tl)
	})
	t.Run("DuringBurstDMA", func(t *testing.T) {
		tl := dmacProgram(t, chcrLongIncr|chcrTB)
		prepareVectors(tl)
		tl.run(2)
		for i := 0; i < 3; i++ {
			tl.clock()
		}
		if !tl.cpu.dmac.Stalling() {
			t.Fatal("DMAC not stalling the CPU before Reset")
		}
		tl.cpu.Reset()
		afterReset(tl)
	})
	t.Run("WhileHalted", func(t *testing.T) {
		tl := newTimeline(t, 0x800)
		prepareVectors(tl)
		tl.code(0x100, opcNOP, opcSLEEP)
		tl.run(2)
		tl.idleCycles(5)
		tl.cpu.Reset()
		afterReset(tl)
	})
}

// TestTimelineSaveStateAtBoundaryContinues: a state captured at an
// instruction boundary, with a DMA countdown and an FRT deadline in
// flight, restored into a fresh CPU on a copy of memory, produces the
// same instruction, cycle, and bus-access sequence as the original.
func TestTimelineSaveStateAtBoundaryContinues(t *testing.T) {
	tl := dmacProgram(t, chcrLongIncr|chcrIE)
	tl.cpu.dmac.ch[0].tcr = 8 // 32-cycle window, TE at 34
	tl.cpu.writeOnChip(0xFFFFFE60, 0x0800)
	tl.cpu.writeOnChip(0xFFFFFE66, 0x0041) // OCI vector 0x41 -> 0x600 as well
	tl.bus.Write32(0x400+0x41*4, 0x600)
	tl.cpu.writeOnChip(0xFFFFFE14, 0x00)
	tl.cpu.writeOnChip(0xFFFFFE15, 0x06) // match at cycle 48
	tl.cpu.writeOnChip(0xFFFFFE10, 0x08)
	tl.cpu.reg.R[6] = 0xA00
	tl.cpu.reg.R[8] = 0xA10
	tl.bus.Write32(0xA10, 5)
	tl.code(0x104,
		encTAS(6),      // 0x104: loop
		encMOVLL(8, 9), // 0x106
		encADD(9, 10),  // 0x108
		opcNOP,         // 0x10A
		encBRA(0xFFA),  // 0x10C: BRA 0x104
		opcNOP,         // 0x10E: delay slot
	)
	tl.code(0x600, opcNOP, opcRTE, opcNOP)

	tl.run(5)
	st := tl.cpu.State()
	memCopy := append([]byte(nil), tl.bus.mem...)

	bus2 := &recordingBus{testBus: &testBus{mem: memCopy}, codeEnd: 0x800}
	cpu2 := New(bus2, true)
	cpu2.SetState(&st)
	tl2 := &timeline{t: t, cpu: cpu2, bus: bus2}
	tl2.attachTrace()

	const n = 40
	tl.run(n)
	tl2.run(n)
	if len(tl2.recs) != n {
		t.Fatalf("restored CPU produced %d records, want %d", len(tl2.recs), n)
	}
	for i := 0; i < n; i++ {
		a, b := tl.recs[5+i], tl2.recs[i]
		if a.pc != b.pc || a.op != b.op || a.entry != b.entry || !opsEqual(a.ops, b.ops) {
			t.Errorf("[%d] original pc=%X op=%04X entry=%d ops=%s; restored pc=%X op=%04X entry=%d ops=%s",
				i, a.pc, a.op, a.entry, fmtOps(a.ops), b.pc, b.op, b.entry, fmtOps(b.ops))
		}
	}
	if tl.cpu.reg != cpu2.reg {
		t.Errorf("register files differ after continuation")
	}
}

// TestTimelineIRLDuringLoadWithDependentFollower: an interrupt
// arriving while a load executes is accepted at the boundary after the
// load, ahead of the dependent instruction that follows it. The
// acceptance ends the load-use tracking, so when the handler returns
// the follower executes at its plain cost with no split.
func TestTimelineIRLDuringLoadWithDependentFollower(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.installIRLHandler()
	tl.cpu.reg.R[1] = 0x800
	tl.bus.Write32(0x800, 0xDEADBEEF)
	tl.code(0x100, opcNOP, encMOVLL(1, 2), encADD(2, 3), opcNOP)
	tl.hookOnce("r", 0x800, func() { tl.cpu.SetIRL(5, 0x40) })
	tl.run(8)
	tl.check([]instrExpect{
		plain(0x100, opcNOP, 1),
		plain(0x102, encMOVLL(1, 2), 1, rd(0x800, 4)),
		{pc: 0x104, op: opAccept, cost: 9, entry: 2, ops: irlAccept},
		plain(0x600, opcNOP, 1),
		plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
		plain(0x604, opcNOP, 1),
		plain(0x104, encADD(2, 3), 1),
		plain(0x106, opcNOP, 1),
	})
	if tl.cpu.reg.R[3] != 0xDEADBEEF {
		t.Errorf("R3 = %08X, want DEADBEEF", tl.cpu.reg.R[3])
	}
}
