// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import (
	"fmt"
	"testing"
)

// Instruction timeline tests. Each test runs a short program and
// records, per executed instruction, the PC, the cycle count at the
// start of its EX stage, and the external bus accesses it performed.
// Expected costs come from the SH-1/SH-2 Programming Manual:
//
//   - Section 7.1.4: an instruction's execution time is the interval
//     between the start of its EX stage and the start of the next
//     instruction's EX stage. A pipeline split caused by a load-use
//     dependency is therefore charged to the load.
//   - Section 5 instruction tables and Table 7.1: per-instruction
//     cycle counts, with the parenthesised value applying when the
//     instruction contends with the one before it.
//   - Section 7.4.7 Figures 7.93 and 7.95: TRAPA and interrupt
//     exception sequences.
//
// The only model-dependent code is stepInstruction/idleCycles: they
// are the seam between the harness and the CPU's stepping interface.

// busOp is one external bus access seen by the recording bus.
type busOp struct {
	kind string // "r", "w", "fill", "rmwr", "rmww"
	addr uint32
	size uint32
}

// accessClock is the timing seen by one logged access: the clock
// argument the CPU passed to the bus.
type accessClock struct {
	now int64
}

// recordingBus wraps testBus and logs the timed (instruction path)
// accesses. Instruction fetches (16-bit reads below codeEnd) are not
// logged so the expected sequences hold only data accesses. Cache line
// fills are always logged, including fills triggered by fetches.
// clocks parallels ops. onAccess, when set, runs after each logged
// access so a test can act from inside an instruction.
type recordingBus struct {
	*testBus
	codeEnd  uint32
	ops      []busOp
	clocks   []accessClock
	onAccess func(op busOp)
}

func (b *recordingBus) log(kind string, addr, size uint32, now int64) {
	op := busOp{kind, addr, size}
	b.ops = append(b.ops, op)
	b.clocks = append(b.clocks, accessClock{now})
	if b.onAccess != nil {
		b.onAccess(op)
	}
}

func (b *recordingBus) SH2Read8(addr uint32, now int64, slave bool) (uint8, uint32) {
	b.log("r", addr, 1, now)
	return b.testBus.SH2Read8(addr, now, slave)
}
func (b *recordingBus) SH2Read16(addr uint32, now int64, slave bool) (uint16, uint32) {
	if addr >= b.codeEnd {
		b.log("r", addr, 2, now)
	}
	return b.testBus.SH2Read16(addr, now, slave)
}
func (b *recordingBus) SH2Read32(addr uint32, now int64, slave bool) (uint32, uint32) {
	b.log("r", addr, 4, now)
	return b.testBus.SH2Read32(addr, now, slave)
}
func (b *recordingBus) SH2Write8(addr uint32, val uint8, now int64, slave bool) uint32 {
	b.log("w", addr, 1, now)
	return b.testBus.SH2Write8(addr, val, now, slave)
}
func (b *recordingBus) SH2Write16(addr uint32, val uint16, now int64, slave bool) uint32 {
	b.log("w", addr, 2, now)
	return b.testBus.SH2Write16(addr, val, now, slave)
}
func (b *recordingBus) SH2Write32(addr uint32, val uint32, now int64, slave bool) uint32 {
	b.log("w", addr, 4, now)
	return b.testBus.SH2Write32(addr, val, now, slave)
}
func (b *recordingBus) SH2FillLine(base uint32, dst *[16]byte, now int64, slave bool) uint32 {
	b.log("fill", base, 16, now)
	return b.testBus.SH2FillLine(base, dst, now, slave)
}
func (b *recordingBus) SH2RMWRead(addr uint32, now int64, slave bool) (uint8, uint32) {
	b.log("rmwr", addr, 1, now)
	return b.testBus.SH2RMWRead(addr, now, slave)
}
func (b *recordingBus) SH2RMWWrite(addr uint32, val uint8, now int64) uint32 {
	b.log("rmww", addr, 1, now)
	return b.testBus.SH2RMWWrite(addr, val, now)
}

// instrRecord is one traced instruction (or interrupt acceptance,
// op 0xFFFF) with the cycle count at the start of its EX stage and the
// bus accesses it performed.
type instrRecord struct {
	pc    uint32
	op    uint16
	entry uint64
	ops   []busOp
}

// timeline drives one CPU and collects instrRecords. opCursor is the
// bus log position at the last instruction boundary, so a record's
// accesses include the fetch-side accesses made before its trace.
type timeline struct {
	t        *testing.T
	cpu      *CPU
	bus      *recordingBus
	recs     []instrRecord
	opCursor int
}

// clock advances the CPU one instruction, or one cycle while it is
// halted or bus-stalled.
func (tl *timeline) clock() {
	c := tl.cpu
	if c.halted || c.dmac.Stalling() {
		c.RunUntil(c.cycles + 1)
		return
	}
	c.Step()
}

// attachTrace installs the TraceFunc that opens a record per
// instruction or interrupt acceptance.
func (tl *timeline) attachTrace() {
	tl.cpu.TraceFunc = func(pc uint32, op uint16) {
		tl.recs = append(tl.recs, instrRecord{pc: pc, op: op, entry: tl.cpu.cycles})
	}
}

// newTimeline builds a master CPU on a recording bus with the cache
// disabled, SR = 0 (all interrupt levels accepted), VBR = 0x400,
// R15 = 0x1000, and PC = 0x100. Code is assembled from 0x100; codeEnd
// bounds the fetch filter.
func newTimeline(t *testing.T, codeEnd uint32) *timeline {
	t.Helper()
	bus := &recordingBus{testBus: newTestBus(0x10000), codeEnd: codeEnd}
	cpu := New(bus, true)
	cpu.reg.SR = 0
	cpu.reg.VBR = 0x400
	cpu.reg.R[15] = 0x1000
	cpu.reg.PC = 0x100
	tl := &timeline{t: t, cpu: cpu, bus: bus}
	tl.attachTrace()
	return tl
}

// code writes opcodes consecutively from addr.
func (tl *timeline) code(addr uint32, ops ...uint16) {
	for i, op := range ops {
		tl.bus.Write16(addr+uint32(i)*2, op)
	}
}

// stepInstruction advances the CPU through exactly one traced item (an
// instruction or an interrupt acceptance), then closes the record's
// bus-access slice. An HLE trap executes without a trace, so the loop
// continues past it.
func (tl *timeline) stepInstruction() {
	tl.t.Helper()
	n := len(tl.recs)
	guard := 0
	for len(tl.recs) == n {
		tl.clock()
		guard++
		if guard > 10000 {
			tl.t.Fatalf("stepInstruction: no instruction traced (halted=%v)", tl.cpu.halted)
		}
	}
	r := &tl.recs[len(tl.recs)-1]
	r.ops = append([]busOp(nil), tl.bus.ops[tl.opCursor:]...)
	tl.opCursor = len(tl.bus.ops)
}

// run steps n traced items.
func (tl *timeline) run(n int) {
	tl.t.Helper()
	for i := 0; i < n; i++ {
		tl.stepInstruction()
	}
}

// idleCycles advances a halted CPU by n cycles with no instruction
// executing.
func (tl *timeline) idleCycles(n int) {
	tl.t.Helper()
	if !tl.cpu.halted {
		tl.t.Fatal("idleCycles: CPU is not halted")
	}
	for i := 0; i < n; i++ {
		tl.clock()
	}
}

// idleUntilRecord advances a halted CPU until a new record opens (an
// interrupt acceptance) or max cycles pass, returning the cycles
// advanced. A record that opened is completed to its boundary.
func (tl *timeline) idleUntilRecord(max int) int {
	tl.t.Helper()
	if !tl.cpu.halted {
		tl.t.Fatal("idleUntilRecord: CPU is not halted")
	}
	n := len(tl.recs)
	for i := 0; i < max; i++ {
		tl.clock()
		if len(tl.recs) > n {
			r := &tl.recs[len(tl.recs)-1]
			r.ops = append([]busOp(nil), tl.bus.ops[tl.opCursor:]...)
			tl.opCursor = len(tl.bus.ops)
			return i + 1
		}
	}
	return max
}

// cost returns the execution time of record i per Section 7.1.4: the
// interval to the next record's EX start, or to the current cycle
// count for the last record.
func (tl *timeline) cost(i int) uint64 {
	if i+1 < len(tl.recs) {
		return tl.recs[i+1].entry - tl.recs[i].entry
	}
	return tl.cpu.cycles - tl.recs[i].entry
}

// instrExpect is the expected shape of one record. cost < 0 leaves the
// cost unchecked; entry < 0 leaves the entry cycle unchecked.
type instrExpect struct {
	pc    uint32
	op    uint16
	cost  int
	entry int
	ops   []busOp
}

const (
	unchecked = -1
	opAccept  = 0xFFFF // TraceFunc marker for interrupt acceptance
)

func opsEqual(a, b []busOp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func fmtOps(ops []busOp) string {
	s := "["
	for i, o := range ops {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s@%X/%d", o.kind, o.addr, o.size)
	}
	return s + "]"
}

// check compares the collected records against want in order.
func (tl *timeline) check(want []instrExpect) {
	tl.t.Helper()
	if len(tl.recs) != len(want) {
		tl.t.Fatalf("recorded %d items, want %d", len(tl.recs), len(want))
	}
	for i, w := range want {
		r := tl.recs[i]
		if r.pc != w.pc || r.op != w.op {
			tl.t.Errorf("[%d] pc=%04X op=%04X, want pc=%04X op=%04X", i, r.pc, r.op, w.pc, w.op)
			continue
		}
		if w.cost >= 0 && tl.cost(i) != uint64(w.cost) {
			tl.t.Errorf("[%d] %04X/%04X cost %d cycles, want %d", i, r.pc, r.op, tl.cost(i), w.cost)
		}
		if w.entry >= 0 && r.entry != uint64(w.entry) {
			tl.t.Errorf("[%d] %04X/%04X entry cycle %d, want %d", i, r.pc, r.op, r.entry, w.entry)
		}
		if !opsEqual(r.ops, w.ops) {
			tl.t.Errorf("[%d] %04X/%04X bus ops %s, want %s", i, r.pc, r.op, fmtOps(r.ops), fmtOps(w.ops))
		}
	}
}

func rd(addr, size uint32) busOp { return busOp{"r", addr, size} }
func wr(addr, size uint32) busOp { return busOp{"w", addr, size} }
func fill(addr uint32) busOp     { return busOp{"fill", addr, 16} }
func rmwr(addr uint32) busOp     { return busOp{"rmwr", addr, 1} }
func rmww(addr uint32) busOp     { return busOp{"rmww", addr, 1} }
func plain(pc uint32, op uint16, cost int, ops ...busOp) instrExpect {
	return instrExpect{pc: pc, op: op, cost: cost, entry: unchecked, ops: ops}
}

// Opcode encodings used by the programs (Programming Manual Section 5
// instruction tables).
const (
	opcNOP   = 0x0009
	opcSETT  = 0x0018
	opcCLRT  = 0x0008
	opcRTS   = 0x000B
	opcRTE   = 0x002B
	opcSLEEP = 0x001B
)

func encBT(disp uint8) uint16   { return 0x8900 | uint16(disp) }
func encBRA(disp uint16) uint16 { return 0xA000 | (disp & 0x0FFF) }
func encBSR(disp uint16) uint16 { return 0xB000 | (disp & 0x0FFF) }
func encSTCLSR(n uint16) uint16 { return 0x4003 | (n << 8) }
func encLDCLSR(m uint16) uint16 { return 0x4007 | (m << 8) }
func encANDB(imm uint8) uint16  { return 0xCD00 | uint16(imm) }
func encORB(imm uint8) uint16   { return 0xCF00 | uint16(imm) }
func encXORB(imm uint8) uint16  { return 0xCE00 | uint16(imm) }
func encTSTB(imm uint8) uint16  { return 0xCC00 | uint16(imm) }
func encTRAPA(imm uint8) uint16 { return 0xC300 | uint16(imm) }

// TestTimelineMultiCycleInstructions: each multi-cycle instruction
// completes with its Section 5 table cycle count and performs its
// memory accesses in order. Table 7.1 (Programming Manual Section
// 7.3.1) lists STC.L 2, memory logic operations 3, TAS 4, LDC.L 3.
func TestTimelineMultiCycleInstructions(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.cpu.reg.GBR = 0x800
	tl.cpu.reg.R[0] = 0
	tl.cpu.reg.R[1] = 0x810
	tl.cpu.reg.R[2] = 0x820
	tl.cpu.reg.R[4] = 0x830
	tl.bus.Write8(0x800, 0xA5)
	tl.bus.Write32(0x820, 0x12345678)

	tl.code(0x100,
		encSTCLSR(15),  // STC.L SR,@-R15
		encLDCLSR(15),  // LDC.L @R15+,SR
		encANDB(0x0F),  // AND.B #0x0F,@(R0,GBR)
		encORB(0xF0),   // OR.B #0xF0,@(R0,GBR)
		encXORB(0xFF),  // XOR.B #0xFF,@(R0,GBR)
		encTSTB(0x01),  // TST.B #0x01,@(R0,GBR)
		encTAS(1),      // TAS.B @R1
		encMOVLL(2, 3), // MOV.L @R2,R3
		encMOVLS(3, 4), // MOV.L R3,@R4
		opcNOP,
	)
	tl.run(10)
	tl.check([]instrExpect{
		plain(0x100, encSTCLSR(15), 2, wr(0xFFC, 4)),
		plain(0x102, encLDCLSR(15), 3, rd(0xFFC, 4)),
		plain(0x104, encANDB(0x0F), 3, rd(0x800, 1), wr(0x800, 1)),
		plain(0x106, encORB(0xF0), 3, rd(0x800, 1), wr(0x800, 1)),
		plain(0x108, encXORB(0xFF), 3, rd(0x800, 1), wr(0x800, 1)),
		plain(0x10A, encTSTB(0x01), 3, rd(0x800, 1)),
		plain(0x10C, encTAS(1), 4, rmwr(0x810), rmww(0x810)),
		plain(0x10E, encMOVLL(2, 3), 1, rd(0x820, 4)),
		plain(0x110, encMOVLS(3, 4), 1, wr(0x830, 4)),
		plain(0x112, opcNOP, 1),
	})
	if got := tl.bus.Read8(0x800); got != 0x0A {
		t.Errorf("byte at 0x800 = %02X, want 0A ((A5&0F|F0)^FF)", got)
	}
	if tl.cpu.reg.T() != 1 {
		t.Error("TST.B: T = 0, want 1 (0x0A & 0x01 == 0)")
	}
	if got := tl.bus.Read8(0x810); got != 0x80 {
		t.Errorf("TAS target = %02X, want 80", got)
	}
	if tl.cpu.reg.R[15] != 0x1000 {
		t.Errorf("R15 = %04X after STC.L/LDC.L pair, want 1000", tl.cpu.reg.R[15])
	}
	if got := tl.bus.Read32(0x830); got != 0x12345678 {
		t.Errorf("stored longword = %08X, want 12345678", got)
	}
}

// TestTimelineBranches: Table 7.1 gives unconditional branches 2
// cycles, conditional branches 3 when taken and 1 when not. The delay
// slot instruction is traced between the branch and its target.
func TestTimelineBranches(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.cpu.reg.R[5] = 0x120
	tl.cpu.reg.R[6] = 0x140
	tl.cpu.reg.R[7] = 4
	tl.cpu.reg.R[8] = 0x10

	tl.code(0x100,
		opcSETT,      // 0x100
		encBT(2),     // 0x102: BT 0x10A (taken)
		opcNOP,       // 0x104 (skipped)
		opcNOP,       // 0x106 (skipped)
		opcNOP,       // 0x108 (skipped)
		opcCLRT,      // 0x10A
		encBT(0),     // 0x10C: BT 0x110 (not taken)
		encBRA(1),    // 0x10E: BRA 0x114
		opcNOP,       // 0x110: delay slot
		opcNOP,       // 0x112 (skipped)
		encBSR(0x14), // 0x114: BSR 0x140, PR = 0x118
		opcNOP,       // 0x116: delay slot
		encJMP(5),    // 0x118: JMP @R5 (0x120)
		opcNOP,       // 0x11A: delay slot
		opcNOP,       // 0x11C (skipped)
		opcNOP,       // 0x11E (skipped)
		encJSR(6),    // 0x120: JSR @R6 (0x140), PR = 0x124
		opcNOP,       // 0x122: delay slot
		encBRAF(7),   // 0x124: BRAF R7 -> 0x12C
		opcNOP,       // 0x126: delay slot
		opcNOP,       // 0x128 (skipped)
		opcNOP,       // 0x12A (skipped)
		encBSRF(8),   // 0x12C: BSRF R8 -> 0x140, PR = 0x130
		opcNOP,       // 0x12E: delay slot
		opcNOP,       // 0x130
	)
	tl.code(0x140, opcRTS, opcNOP)

	tl.run(23)
	tl.check([]instrExpect{
		plain(0x100, opcSETT, 1),
		plain(0x102, encBT(2), 3),
		plain(0x10A, opcCLRT, 1),
		plain(0x10C, encBT(0), 1),
		plain(0x10E, encBRA(1), 2),
		plain(0x110, opcNOP, 1),
		plain(0x114, encBSR(0x14), 2),
		plain(0x116, opcNOP, 1),
		plain(0x140, opcRTS, 2),
		plain(0x142, opcNOP, 1),
		plain(0x118, encJMP(5), 2),
		plain(0x11A, opcNOP, 1),
		plain(0x120, encJSR(6), 2),
		plain(0x122, opcNOP, 1),
		plain(0x140, opcRTS, 2),
		plain(0x142, opcNOP, 1),
		plain(0x124, encBRAF(7), 2),
		plain(0x126, opcNOP, 1),
		plain(0x12C, encBSRF(8), 2),
		plain(0x12E, opcNOP, 1),
		plain(0x140, opcRTS, 2),
		plain(0x142, opcNOP, 1),
		plain(0x130, opcNOP, 1),
	})
}

// TestTimelineLoadUse: Programming Manual Section 7.2.2, Figure 7.9: an
// instruction that uses the destination of the immediately preceding
// load splits its slot, adding one cycle. Figure 7.14: a store whose
// source is the preceding load's destination does not split. An
// unrelated instruction between the load and the use removes the split.
func TestTimelineLoadUse(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.cpu.reg.R[1] = 0x800
	tl.cpu.reg.R[3] = 10
	tl.cpu.reg.R[5] = 0x900
	tl.bus.Write32(0x800, 5)

	tl.code(0x100,
		encMOVLL(1, 2), // MOV.L @R1,R2
		encADD(2, 3),   // ADD R2,R3 (uses R2: split)
		encMOVLL(1, 4), // MOV.L @R1,R4
		encMOVLS(4, 5), // MOV.L R4,@R5 (store forwarding: no split)
		encMOVLL(1, 6), // MOV.L @R1,R6
		opcNOP,
		encADD(6, 7), // ADD R6,R7 (one instruction later: no split)
	)
	tl.run(7)
	tl.check([]instrExpect{
		plain(0x100, encMOVLL(1, 2), 2, rd(0x800, 4)),
		plain(0x102, encADD(2, 3), 1),
		plain(0x104, encMOVLL(1, 4), 1, rd(0x800, 4)),
		plain(0x106, encMOVLS(4, 5), 1, wr(0x900, 4)),
		plain(0x108, encMOVLL(1, 6), 1, rd(0x800, 4)),
		plain(0x10A, opcNOP, 1),
		plain(0x10C, encADD(6, 7), 1),
	})
	if tl.cpu.reg.R[3] != 15 {
		t.Errorf("R3 = %d, want 15", tl.cpu.reg.R[3])
	}
	if tl.cpu.reg.R[7] != 5 {
		t.Errorf("R7 = %d, want 5", tl.cpu.reg.R[7])
	}
}

// TestTimelineCacheFills: with the cache enabled, the first fetch from
// a line and the first data read from a line each fill the whole
// 16-byte line in one bus transaction (SH7604 Hardware Manual Section
// 8.4.1) and are charged the fill wait states; later accesses to the
// resident lines make no bus access and cost nothing beyond the
// instruction's own cycle. The total is the instruction cycles plus one
// fill charge per miss.
func TestTimelineCacheFills(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.bus.stallRead = 3
	tl.bus.stallWrite = 3
	tl.bus.stallFill = 6
	tl.cpu.SetCCR(ccrCE)
	tl.cpu.reg.R[1] = 0x800
	tl.bus.Write32(0x800, 0xCAFEF00D)

	tl.code(0x100,
		encMOVLL(1, 2), // 0x100: fetch miss (line 0x100), data miss (line 0x800)
		encMOVLL(1, 3), // 0x102: both hits
		opcNOP,         // 0x104
		opcNOP,         // 0x106
		opcNOP,         // 0x108
		opcNOP,         // 0x10A
		opcNOP,         // 0x10C
		opcNOP,         // 0x10E
		opcNOP,         // 0x110: fetch miss (line 0x110)
	)
	tl.run(9)
	tl.check([]instrExpect{
		plain(0x100, encMOVLL(1, 2), unchecked, fill(0x100), fill(0x800)),
		plain(0x102, encMOVLL(1, 3), unchecked),
		plain(0x104, opcNOP, unchecked),
		plain(0x106, opcNOP, unchecked),
		plain(0x108, opcNOP, unchecked),
		plain(0x10A, opcNOP, unchecked),
		plain(0x10C, opcNOP, unchecked),
		plain(0x10E, opcNOP, unchecked),
		plain(0x110, opcNOP, unchecked, fill(0x110)),
	})
	const want = 9 + 3*6
	if tl.cpu.cycles != want {
		t.Errorf("total cycles = %d, want %d (9 instructions + 3 fills x 6)", tl.cpu.cycles, want)
	}
	if tl.cpu.reg.R[2] != 0xCAFEF00D || tl.cpu.reg.R[3] != 0xCAFEF00D {
		t.Errorf("R2/R3 = %08X/%08X, want CAFEF00D both", tl.cpu.reg.R[2], tl.cpu.reg.R[3])
	}
}

// TestTimelineMultiplierContention: Table 7.1 cycle counts for the
// multiplier instructions, minimum when the multiplier is free and the
// parenthesised maximum when the instruction immediately follows
// another multiplier instruction (Section 7.2.3: the MA is extended
// until the preceding instruction's mm stages end). MUL.L 2 (to 4),
// MULS.W 1 (to 3), DMULS.L 2 (to 4), MAC.L 2 (to 4), MAC.W 2 (to 3).
// Four unrelated instructions between multiplier instructions cover
// the longest (4-cycle) mm run so the follower is charged the minimum.
func TestTimelineMultiplierContention(t *testing.T) {
	tl := newTimeline(t, 0x200)
	tl.cpu.reg.R[1] = 3
	tl.cpu.reg.R[2] = 7
	tl.cpu.reg.R[3] = 0x800
	tl.cpu.reg.R[4] = 0x820

	tl.code(0x100,
		encMULL(1, 2),   // 0x100: 2
		encMULL(1, 2),   // 0x102: 4 (contends)
		opcNOP,          // 0x104
		encMULSW(1, 2),  // 0x106: 1
		encMULSW(1, 2),  // 0x108: 3 (contends)
		opcNOP,          // 0x10A
		encDMULSL(1, 2), // 0x10C: 2
		encDMULSL(1, 2), // 0x10E: 4 (contends)
		opcNOP,          // 0x110
		opcNOP,          // 0x112
		opcNOP,          // 0x114
		opcNOP,          // 0x116
		encMACL(3, 4),   // 0x118: 2
		encMACL(3, 4),   // 0x11A: 4 (contends)
		opcNOP,          // 0x11C
		opcNOP,          // 0x11E
		opcNOP,          // 0x120
		opcNOP,          // 0x122
		encMACW(3, 4),   // 0x124: 2
		encMACW(3, 4),   // 0x126: 3 (contends)
		opcNOP,          // 0x128
	)
	tl.run(21)
	tl.check([]instrExpect{
		plain(0x100, encMULL(1, 2), 2),
		plain(0x102, encMULL(1, 2), 4),
		plain(0x104, opcNOP, 1),
		plain(0x106, encMULSW(1, 2), 1),
		plain(0x108, encMULSW(1, 2), 3),
		plain(0x10A, opcNOP, 1),
		plain(0x10C, encDMULSL(1, 2), 2),
		plain(0x10E, encDMULSL(1, 2), 4),
		plain(0x110, opcNOP, 1),
		plain(0x112, opcNOP, 1),
		plain(0x114, opcNOP, 1),
		plain(0x116, opcNOP, 1),
		plain(0x118, encMACL(3, 4), 2, rd(0x820, 4), rd(0x800, 4)),
		plain(0x11A, encMACL(3, 4), 4, rd(0x824, 4), rd(0x804, 4)),
		plain(0x11C, opcNOP, 1),
		plain(0x11E, opcNOP, 1),
		plain(0x120, opcNOP, 1),
		plain(0x122, opcNOP, 1),
		plain(0x124, encMACW(3, 4), 2, rd(0x828, 2), rd(0x808, 2)),
		plain(0x126, encMACW(3, 4), 3, rd(0x82A, 2), rd(0x80A, 2)),
		plain(0x128, opcNOP, 1),
	})
}

// TestTimelineTRAPAAndRTE: Table 7.1 gives TRAP 8 cycles and RTE 4.
// Figure 7.93: TRAPA saves SR then PC to the stack and reads the
// vector. RTE (Section 5, RTE description) restores PC then SR from
// the stack and is a delayed branch, so the slot instruction executes
// before the return target.
func TestTimelineTRAPAAndRTE(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.bus.Write32(0x400+0x20*4, 0x200) // TRAPA #0x20 vector -> 0x200

	tl.code(0x100, opcNOP, encTRAPA(0x20), opcNOP, opcNOP)
	tl.code(0x200, opcNOP, opcRTE, opcNOP)

	tl.run(7)
	tl.check([]instrExpect{
		plain(0x100, opcNOP, 1),
		plain(0x102, encTRAPA(0x20), 8, wr(0xFFC, 4), wr(0xFF8, 4), rd(0x480, 4)),
		plain(0x200, opcNOP, 1),
		plain(0x202, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
		plain(0x204, opcNOP, 1),
		plain(0x104, opcNOP, 1),
		plain(0x106, opcNOP, 1),
	})
	if got := tl.bus.Read32(0xFF8); got != 0x104 {
		t.Errorf("saved PC = %08X, want 00000104", got)
	}
	if tl.cpu.reg.R[15] != 0x1000 {
		t.Errorf("R15 = %04X after RTE, want 1000", tl.cpu.reg.R[15])
	}
}

// TestTimelineIRLAndNMIAcceptance: an interrupt request present at an
// instruction boundary is accepted there. Figure 7.95 shows the
// interrupt exception sequence as ID EX EX MA MA EX MA EX EX with the
// handler's IF starting in the final EX slot; counted per Section
// 7.1.4 (EX to EX) that is 9 cycles, one more than TRAPA's 8 (Figure
// 7.93, Table 7.1). The sequence saves SR then PC and reads the vector
// (SH7604 Hardware Manual Table 5.8: m1 SR save, m2 PC save, m3
// vector read). NMI uses vector 11 (Section 4, exception vector table).
func TestTimelineIRLAndNMIAcceptance(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.bus.Write32(0x400+0x40*4, 0x600) // IRL vector 0x40 -> 0x600
	tl.bus.Write32(0x400+11*4, 0x700)   // NMI vector 11 -> 0x700
	tl.cpu.SetIRLAck(func(uint16) { tl.cpu.ClearIRL() })

	tl.code(0x100, opcNOP, opcNOP, opcNOP, opcNOP, opcNOP)
	tl.code(0x600, opcNOP, opcRTE, opcNOP)
	tl.code(0x700, opcNOP, opcRTE, opcNOP)

	tl.run(2)
	tl.cpu.SetIRL(5, 0x40)
	tl.run(5) // accept, handler NOP, RTE, slot, return NOP
	tl.cpu.NMI()
	tl.run(5)

	tl.check([]instrExpect{
		plain(0x100, opcNOP, 1),
		plain(0x102, opcNOP, 1),
		plain(0x104, opAccept, 9, wr(0xFFC, 4), wr(0xFF8, 4), rd(0x500, 4)),
		plain(0x600, opcNOP, 1),
		plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
		plain(0x604, opcNOP, 1),
		plain(0x104, opcNOP, 1),
		plain(0x106, opAccept, 9, wr(0xFFC, 4), wr(0xFF8, 4), rd(0x42C, 4)),
		plain(0x700, opcNOP, 1),
		plain(0x702, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
		plain(0x704, opcNOP, 1),
		plain(0x106, opcNOP, 1),
	})
	if tl.cpu.reg.SR != 0 {
		t.Errorf("SR = %08X after RTE, want 0", tl.cpu.reg.SR)
	}
}

// TestTimelineSleepWake: SLEEP costs 3 cycles (Table 7.1) and halts the
// CPU. No instruction executes while halted. An interrupt arriving
// during the halt is accepted with the return address of the
// instruction after SLEEP (Section 5, SLEEP description: on wake the
// exception is processed and the program resumes after SLEEP).
func TestTimelineSleepWake(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.bus.Write32(0x400+0x40*4, 0x600)
	tl.cpu.SetIRLAck(func(uint16) { tl.cpu.ClearIRL() })
	tl.code(0x100, opcNOP, opcSLEEP, opcNOP)
	tl.code(0x600, opcNOP, opcRTE, opcNOP)

	tl.run(2)
	if !tl.cpu.halted {
		t.Fatal("CPU not halted after SLEEP")
	}
	if tl.cpu.cycles != 4 {
		t.Errorf("cycles after NOP; SLEEP = %d, want 4 (1 + 3)", tl.cpu.cycles)
	}
	tl.idleCycles(10)
	if len(tl.recs) != 2 {
		t.Fatalf("instruction traced while halted: %d records", len(tl.recs))
	}
	tl.cpu.SetIRL(5, 0x40)
	tl.run(5)

	tl.check([]instrExpect{
		plain(0x100, opcNOP, 1),
		plain(0x102, opcSLEEP, unchecked),
		{pc: 0x104, op: opAccept, cost: unchecked, entry: 14, ops: []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x500, 4)}},
		plain(0x600, opcNOP, 1),
		plain(0x602, opcRTE, 4, rd(0xFF8, 4), rd(0xFFC, 4)),
		plain(0x604, opcNOP, 1),
		plain(0x104, opcNOP, 1),
	})
	if got := tl.bus.Read32(0xFF8); got != 0x104 {
		t.Errorf("saved PC = %08X, want 00000104 (instruction after SLEEP)", got)
	}
}

// installHandlerVector points vector vec (relative to VBR 0x400) at
// 0x600 and places a NOP there. Peripheral tests stop at the handler's
// first instruction, before the level-sensitive request would need
// clearing.
func (tl *timeline) installHandlerVector(vec uint32) {
	tl.bus.Write32(0x400+vec*4, 0x600)
	tl.code(0x600, opcNOP)
}

// fillNOPs writes n NOPs from addr.
func (tl *timeline) fillNOPs(addr uint32, n int) {
	for i := 0; i < n; i++ {
		tl.bus.Write16(addr+uint32(i)*2, opcNOP)
	}
}

// TestTimelineFRTCompareMatchInterrupt: with TCR CKS = 00 the FRC
// counts phi/8 (SH7604 Hardware Manual Section 11.2.6), so OCRA = 4
// matches at cycle 32. The OCIA request is accepted at the first
// instruction boundary at or after the match, with the interrupted
// instruction's address saved as the return PC. The vector comes from
// VCRC bits 6-0 and the level from IPRB bits 11-8 (Section 5.3).
func TestTimelineFRTCompareMatchInterrupt(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.installHandlerVector(0x41)
	tl.cpu.writeOnChip(0xFFFFFE60, 0x0800) // IPRB: FRT level 8
	tl.cpu.writeOnChip(0xFFFFFE66, 0x0041) // VCRC: OCI vector 0x41
	tl.cpu.writeOnChip(0xFFFFFE14, 0x00)   // OCRA high
	tl.cpu.writeOnChip(0xFFFFFE15, 0x04)   // OCRA low
	tl.cpu.writeOnChip(0xFFFFFE10, 0x08)   // TIER: OCIAE
	tl.fillNOPs(0x100, 64)

	tl.run(32)
	tl.run(2) // acceptance + handler NOP
	want := make([]instrExpect, 0, 34)
	for i := 0; i < 32; i++ {
		want = append(want, instrExpect{pc: 0x100 + uint32(i)*2, op: opcNOP, cost: 1, entry: i})
	}
	want = append(want,
		instrExpect{pc: 0x140, op: opAccept, cost: unchecked, entry: 32, ops: []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x504, 4)}},
		plain(0x600, opcNOP, unchecked),
	)
	tl.check(want)
	if tl.cpu.reg.IMASK() != 8 {
		t.Errorf("IMASK = %d in handler, want 8", tl.cpu.reg.IMASK())
	}
}

// TestTimelineFRTCounterReadIsExact: the FRC value software reads
// reflects the cycle at which the read executes (SH7604 Hardware
// Manual Section 11.4.1: the FRC increments on the selected internal
// clock), independent of when the timer last had an event. Section
// 11.3: the upper byte is read first and that read latches the lower
// byte into TEMP, which the lower-byte read returns. At phi/8 (Section
// 11.2.6, CKS = 00), the upper-byte read at cycle 17 latches 2.
func TestTimelineFRTCounterReadIsExact(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.cpu.reg.R[1] = 0xFFFFFE12 // FRC high byte
	tl.cpu.reg.R[3] = 0xFFFFFE13 // FRC low byte
	tl.fillNOPs(0x100, 17)
	tl.code(0x122, encMOVBL(1, 2), encMOVBL(3, 4)) // reads at entry cycles 17 and 18

	tl.run(19)
	if r := tl.recs[17]; r.pc != 0x122 || r.entry != 17 {
		t.Fatalf("high-byte read at pc=%04X entry=%d, want 0122/17", r.pc, r.entry)
	}
	if tl.cpu.reg.R[2] != 0 || tl.cpu.reg.R[4] != 2 {
		t.Errorf("FRC read at cycle 17 = %02X%02X, want 0002", tl.cpu.reg.R[2], tl.cpu.reg.R[4])
	}
}

// TestTimelineWDTIntervalInterrupt: WTCSR TME with CKS = 000 counts
// phi/2 (SH7604 Hardware Manual Section 12.2.2), so WTCNT = 0xFC
// overflows after 4 counts at cycle 8. In interval mode the overflow
// raises ITI, accepted at the first instruction boundary at or after
// the overflow. The vector is VCRWDT bits 14-8 and the level IPRA bits
// 7-4 (Section 5.3).
func TestTimelineWDTIntervalInterrupt(t *testing.T) {
	tl := newTimeline(t, 0x800)
	tl.installHandlerVector(0x42)
	tl.cpu.writeOnChip(0xFFFFFEE2, 0x0080) // IPRA: WDT level 8
	tl.cpu.writeOnChip(0xFFFFFEE4, 0x4200) // VCRWDT: ITI vector 0x42
	tl.cpu.writeOnChip(0xFFFFFE80, 0x5AFC) // WTCNT = 0xFC
	tl.cpu.writeOnChip(0xFFFFFE80, 0xA520) // WTCSR: TME, interval, CKS 0
	tl.fillNOPs(0x100, 32)

	tl.run(8)
	tl.run(2)
	want := make([]instrExpect, 0, 10)
	for i := 0; i < 8; i++ {
		want = append(want, instrExpect{pc: 0x100 + uint32(i)*2, op: opcNOP, cost: 1, entry: i})
	}
	want = append(want,
		instrExpect{pc: 0x110, op: opAccept, cost: unchecked, entry: 8, ops: []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x508, 4)}},
		plain(0x600, opcNOP, unchecked),
	)
	tl.check(want)
}

// dmacProgram prepares a 4-longword channel 0 transfer from 0x800 to
// 0x900 with DME set, the DEI vector at 0x43 and DMAC level 8, and
// assembles NOP; MOV.L R1,@R2 (CHCR0 write) followed by NOPs. The
// program kicks the transfer at entry cycle 1. The test bus charges 2
// cycles per access, so 4 units cost 16 bus cycles (SH7604 Hardware
// Manual Section 9.5: each unit is one read and one write).
func dmacProgram(t *testing.T, chcr uint32) *timeline {
	t.Helper()
	tl := newTimeline(t, 0x800)
	tl.installHandlerVector(0x43)
	tl.cpu.writeOnChip(0xFFFFFEE2, 0x0800) // IPRA: DMAC level 8
	tl.cpu.dmac.ch[0].sar = 0x800
	tl.cpu.dmac.ch[0].dar = 0x900
	tl.cpu.dmac.ch[0].tcr = 4
	tl.cpu.dmac.ch[0].vcrdma = 0x43
	tl.cpu.dmac.dmaor = 1
	for i := uint32(0); i < 4; i++ {
		tl.bus.Write32(0x800+i*4, 0x1000+i)
	}
	tl.cpu.reg.R[1] = chcr
	tl.cpu.reg.R[2] = 0xFFFFFF8C
	tl.code(0x100, opcNOP, encMOVLS(1, 2))
	tl.fillNOPs(0x104, 40)
	return tl
}

const (
	// CHCR: DM=01 SM=01 (increment), TS=10 (longword), DE=1.
	chcrLongIncr = 0x5801
	chcrTB       = 0x10 // burst mode
	chcrIE       = 0x04 // transfer-end interrupt enable
)

// The bus-occupation window is a model decision: it is the stall count
// of cycles following the kick instruction, so a 16-cycle transfer
// kicked at entry cycle 1 (completing its EX at cycle 2) occupies
// cycles 2 through 17 and TE lands at cycle 18. The Hardware Manual
// gives the per-unit cost but not the cycle at which TE becomes
// visible relative to the kick.
const dmaTECycle = 1 + 1 + 16

// TestTimelineDMACBurstStallsCPU: SH7604 Hardware Manual Section 9.5,
// burst mode holds the bus until the transfer ends, so the kick
// instruction is followed by the whole occupation window before the
// next instruction executes. TE then raises DEI, accepted before the
// next instruction.
func TestTimelineDMACBurstStallsCPU(t *testing.T) {
	tl := dmacProgram(t, chcrLongIncr|chcrTB|chcrIE)
	tl.run(4)
	tl.check([]instrExpect{
		{pc: 0x100, op: opcNOP, cost: 1, entry: 0},
		{pc: 0x102, op: encMOVLS(1, 2), cost: 1 + 16, entry: 1},
		{pc: 0x104, op: opAccept, cost: unchecked, entry: dmaTECycle, ops: []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x50C, 4)}},
		plain(0x600, opcNOP, unchecked),
	})
	for i := uint32(0); i < 4; i++ {
		if got := tl.bus.Read32(0x900 + i*4); got != 0x1000+i {
			t.Errorf("dest[%d] = %08X, want %08X", i, got, 0x1000+i)
		}
	}
}

// TestTimelineDMACCycleStealKeepsExecuting: Section 9.5, cycle-steal
// mode returns the bus after each unit, so the CPU keeps executing at
// full rate during the occupation window. TE lands at the same cycle
// as in burst mode and DEI is accepted at the next boundary.
func TestTimelineDMACCycleStealKeepsExecuting(t *testing.T) {
	tl := dmacProgram(t, chcrLongIncr|chcrIE)
	tl.run(2 + 16 + 2)
	want := []instrExpect{
		{pc: 0x100, op: opcNOP, cost: 1, entry: 0},
		{pc: 0x102, op: encMOVLS(1, 2), cost: 1, entry: 1},
	}
	for i := 0; i < 16; i++ {
		want = append(want, instrExpect{pc: 0x104 + uint32(i)*2, op: opcNOP, cost: 1, entry: 2 + i})
	}
	want = append(want,
		instrExpect{pc: 0x124, op: opAccept, cost: unchecked, entry: dmaTECycle, ops: []busOp{wr(0xFFC, 4), wr(0xFF8, 4), rd(0x50C, 4)}},
		plain(0x600, opcNOP, unchecked),
	)
	tl.check(want)
}

// TestTimelineDMACTEPollIsExact: with DEI disabled, software polling
// CHCR sees TE clear while the occupation window is open and set once
// it has closed, regardless of how the CPU's time is stepped.
func TestTimelineDMACTEPollIsExact(t *testing.T) {
	tl := dmacProgram(t, chcrLongIncr)
	// Poll at entry cycle dmaTECycle-1 (window still open) and at
	// dmaTECycle (closed): NOPs fill entries 2 .. dmaTECycle-2.
	polls := uint32(dmaTECycle-1) * 2
	tl.code(0x100+polls, encMOVLL(2, 3))   // entry dmaTECycle-1 -> R3
	tl.code(0x100+polls+2, encMOVLL(2, 4)) // entry dmaTECycle -> R4

	tl.run(dmaTECycle + 1)
	if r := tl.recs[dmaTECycle-1]; r.pc != 0x100+polls || r.entry != dmaTECycle-1 {
		t.Fatalf("first poll at pc=%04X entry=%d, want %04X/%d", r.pc, r.entry, 0x100+polls, dmaTECycle-1)
	}
	if r := tl.recs[dmaTECycle]; r.entry != dmaTECycle {
		t.Fatalf("second poll entry=%d, want %d", r.entry, dmaTECycle)
	}
	if tl.cpu.reg.R[3]&2 != 0 {
		t.Errorf("CHCR at cycle %d = %08X: TE set while the transfer is in progress", dmaTECycle-1, tl.cpu.reg.R[3])
	}
	if tl.cpu.reg.R[4]&2 == 0 {
		t.Errorf("CHCR at cycle %d = %08X: TE clear after the transfer ended", dmaTECycle, tl.cpu.reg.R[4])
	}
}

// TestTimelineTwoCPUTASLock: two CPUs sharing memory each execute
// TAS.B on the same lock byte. TAS reads through the bus RMW pair
// (SH7604 Hardware Manual Section 7.10: the bus is not released
// between the read and the write). The first CPU sees 0 and sets T;
// the second sees the byte already set and clears T. Each CPU's
// timeline shows exactly one RMW read and one RMW write for the
// instruction, at the Table 7.1 cost of 4 cycles.
func TestTimelineTwoCPUTASLock(t *testing.T) {
	mem := newTestBus(0x10000)
	masterBus := &recordingBus{testBus: mem, codeEnd: 0x200}
	slaveBus := &recordingBus{testBus: mem, codeEnd: 0x200}
	newCPU := func(bus *recordingBus, master bool) *timeline {
		cpu := New(bus, master)
		cpu.reg.SR = 0
		cpu.reg.R[15] = 0x1000
		cpu.reg.PC = 0x100
		cpu.reg.R[1] = 0x800
		tl := &timeline{t: t, cpu: cpu, bus: bus}
		tl.attachTrace()
		return tl
	}
	m := newCPU(masterBus, true)
	s := newCPU(slaveBus, false)
	mem.Write16(0x100, encTAS(1))
	mem.Write16(0x102, opcNOP)

	m.run(2)
	s.run(2)

	m.check([]instrExpect{
		plain(0x100, encTAS(1), 4, rmwr(0x800), rmww(0x800)),
		plain(0x102, opcNOP, 1),
	})
	s.check([]instrExpect{
		plain(0x100, encTAS(1), 4, rmwr(0x800), rmww(0x800)),
		plain(0x102, opcNOP, 1),
	})
	if m.cpu.reg.T() != 1 {
		t.Error("master TAS: T = 0, want 1 (lock was free)")
	}
	if s.cpu.reg.T() != 0 {
		t.Error("slave TAS: T = 1, want 0 (lock already taken)")
	}
	if got := mem.Read8(0x800); got != 0x80 {
		t.Errorf("lock byte = %02X, want 80", got)
	}
}
