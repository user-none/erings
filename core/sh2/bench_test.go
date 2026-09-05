// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import (
	"testing"
	"time"
)

// SH-2 performance benchmarks. Each runs a fixed program for a fixed
// number of emulated cycles per iteration and reports nanoseconds per
// emulated cycle, the unit that stays comparable across changes to how
// the CPU is stepped.

const benchCyclesPerIter = 4096

func benchCPU(stallRead, stallWrite, stallFill uint32) (*CPU, *testBus) {
	bus := newTestBus(0x10000)
	bus.stallRead = stallRead
	bus.stallWrite = stallWrite
	bus.stallFill = stallFill
	cpu := New(bus, true)
	cpu.reg.SR = 0
	cpu.reg.VBR = 0x400
	cpu.reg.R[15] = 0x1000
	cpu.reg.PC = 0x100
	return cpu, bus
}

func benchCode(bus *testBus, addr uint32, ops ...uint16) {
	for i, op := range ops {
		bus.Write16(addr+uint32(i)*2, op)
	}
}

// runBench drives the CPU for benchCyclesPerIter cycles per iteration
// and reports the per-cycle cost.
func runBench(b *testing.B, cpu *CPU) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		cpu.RunUntil(cpu.cycles + uint64(benchCyclesPerIter))
	}
	elapsed := time.Since(start)
	if pc := cpu.reg.PC; pc < 0x100 || pc >= 0x700 {
		b.Fatalf("program left its loop: PC = %08X", pc)
	}
	b.ReportMetric(float64(elapsed.Nanoseconds())/float64(uint64(b.N)*benchCyclesPerIter), "ns/cycle")
}

// BenchmarkSH2ALULoop: register arithmetic, a shift, and a delayed
// branch. Dispatch and cycle machinery only.
func BenchmarkSH2ALULoop(b *testing.B) {
	cpu, bus := benchCPU(0, 0, 0)
	benchCode(bus, 0x100,
		encADD(1, 2),  // 0x100: loop
		0x3218,        // SUB R1,R2 (0011nnnnmmmm1000)
		0x2439,        // AND R3,R4 (0010nnnnmmmm1001)
		0x4500,        // SHLL R5
		opcNOP,        // 0x108
		encBRA(0xFF9), // 0x10A: BRA 0x100
		opcNOP,        // 0x10C: delay slot
	)
	runBench(b, cpu)
}

// BenchmarkSH2LoadStoreLoop: loads and stores with no wait states.
func BenchmarkSH2LoadStoreLoop(b *testing.B) {
	cpu, bus := benchCPU(0, 0, 0)
	cpu.reg.R[1] = 0x2000
	cpu.reg.R[3] = 0x3000
	benchCode(bus, 0x100,
		encMOVLL(1, 2), // 0x100: loop
		encMOVLS(2, 3), // 0x102
		encMOVWL(1, 4), // 0x104
		encMOVBS(4, 3), // 0x106
		encBRA(0xFFA),  // 0x108: BRA 0x100
		opcNOP,         // 0x10A
	)
	runBench(b, cpu)
}

// BenchmarkSH2LoadStoreWaitStates: the same loop with 2 wait states
// per read and write, exercising the stall path.
func BenchmarkSH2LoadStoreWaitStates(b *testing.B) {
	cpu, bus := benchCPU(2, 2, 0)
	cpu.reg.R[1] = 0x2000
	cpu.reg.R[3] = 0x3000
	benchCode(bus, 0x100,
		encMOVLL(1, 2),
		encMOVLS(2, 3),
		encMOVWL(1, 4),
		encMOVBS(4, 3),
		encBRA(0xFFA),
		opcNOP,
	)
	runBench(b, cpu)
}

// BenchmarkSH2CachedLoop: the load/store loop with the cache on, so
// fetches and loads hit after the first pass.
func BenchmarkSH2CachedLoop(b *testing.B) {
	cpu, bus := benchCPU(3, 3, 6)
	cpu.SetCCR(ccrCE)
	cpu.reg.R[1] = 0x2000
	cpu.reg.R[3] = 0x3000
	benchCode(bus, 0x100,
		encMOVLL(1, 2),
		encMOVLS(2, 3),
		encMOVWL(1, 4),
		encMOVBS(4, 3),
		encBRA(0xFFA),
		opcNOP,
	)
	runBench(b, cpu)
}

// BenchmarkSH2MixedLoop: load-use, multiply, MAC register access, a
// store, TAS, TRAPA and RTE. Exercises the multi-cycle bodies.
func BenchmarkSH2MixedLoop(b *testing.B) {
	cpu, bus := benchCPU(0, 0, 0)
	cpu.reg.R[1] = 0x2000
	cpu.reg.R[6] = 0x3000
	cpu.reg.R[7] = 0x3100
	bus.Write32(0x400+0x20*4, 0x200)
	benchCode(bus, 0x100,
		encMOVLL(1, 2), // 0x100: loop
		encADD(2, 4),   // 0x102
		encMULL(2, 4),  // 0x104
		encSTSMACL(5),  // 0x106
		encMOVLS(5, 6), // 0x108
		encTAS(7),      // 0x10A
		encTRAPA(0x20), // 0x10C
		encBRA(0xFF7),  // 0x10E: BRA 0x100
		opcNOP,         // 0x110
	)
	benchCode(bus, 0x200, opcRTE, opcNOP)
	runBench(b, cpu)
}

// frtPeriodic arms an OCRA match every 64 cycles (OCRA 8 at phi/8 with
// CCLRA) at level 8 with the OCI vector 0x41 -> 0x600, whose handler
// clears OCFA and returns.
func frtPeriodic(cpu *CPU, bus *testBus) {
	bus.Write32(0x400+0x41*4, 0x600)
	cpu.writeOnChip(0xFFFFFE60, 0x0800) // IPRB: FRT level 8
	cpu.writeOnChip(0xFFFFFE66, 0x0041) // VCRC: OCI vector 0x41
	cpu.writeOnChip(0xFFFFFE14, 0x00)   // OCRA high
	cpu.writeOnChip(0xFFFFFE15, 0x08)   // OCRA low
	cpu.writeOnChip(0xFFFFFE11, 0x01)   // FTCSR: CCLRA
	cpu.writeOnChip(0xFFFFFE10, 0x08)   // TIER: OCIAE
	cpu.reg.R[9] = 0xFFFFFE11           // FTCSR
	cpu.reg.R[11] = 0
	benchCode(bus, 0x600,
		encMOVBL(9, 10), // read FTCSR
		encMOVBS(11, 9), // write 0: clears OCFA
		opcRTE,
		opcNOP,
	)
}

// BenchmarkSH2InterruptLoop: the ALU loop with an FRT interrupt every
// 64 cycles. Exercises acceptance, the handler, RTE, and the deadline
// check.
func BenchmarkSH2InterruptLoop(b *testing.B) {
	cpu, bus := benchCPU(0, 0, 0)
	frtPeriodic(cpu, bus)
	benchCode(bus, 0x100,
		encADD(1, 2),
		0x3218,
		0x2439,
		0x4500,
		opcNOP,
		encBRA(0xFF9),
		opcNOP,
	)
	runBench(b, cpu)
}

// BenchmarkSH2HaltedLoop: SLEEP woken by the periodic FRT interrupt.
// Exercises the idle path.
func BenchmarkSH2HaltedLoop(b *testing.B) {
	cpu, bus := benchCPU(0, 0, 0)
	frtPeriodic(cpu, bus)
	benchCode(bus, 0x100,
		opcSLEEP,      // 0x100: loop
		encBRA(0xFFD), // 0x102: BRA 0x100
		opcNOP,        // 0x104
	)
	runBench(b, cpu)
}
