// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// SH-2 bus-access timing. Turns a CPU memory access into wait-state cycles.
// Three components sum to the stall the SH-2 adds to its bus-access debt:
//
//  1. Region cost (penalty + run). The access penalty (SH-2 BSC / SDRAM setup)
//     and the data-transfer/run time are one combined per-region number - see
//     computeAccessCycles / computeWriteAccessCycles below for the derivation.
//     Reads and writes differ where the device does (WRAM-H SDRAM read 7 =
//     Tr+Tc+CAS+Td1..Td4, write 4 = Tr+Tc+TRWL+TRP, SH7604 Sec 7.5; VDP2 VRAM
//     read 8 / write 5). The instruction's own MA cycle covers 1 of these, so
//     the table value minus 1 is charged as stall.
//
//  2. Slave arbitration. The slave SH-2 has no bus rights (SH7604 manual
//     Sec 7.10.2, total slave mode; Saturn dual-CPU guide Sec 1.1 "complete
//     shared mode"); it runs a BREQ/BACK handshake to capture the bus on every
//     external access. The manual gives that as an edge protocol, not a cycle
//     count, so a conservative 1-cycle estimate is charged to slave accesses.
//
//  3. Contention wait. While one SH-2 owns the shared CPU-Bus, the other's next
//     access "is not released until the bus cycle ends" (SH7604 Sec 7.10) - the
//     waiting CPU pays the remaining cycles of the in-flight access. Modeled on
//     areaCPUBus (Work RAM) via busyUntil, clamped to one transaction (the two
//     CPUs' frame clocks are only equal within syncChunkCycles).
//
// Per-region penalty + run = total (single read), from AccessCycles:
//
//	CPU-Bus CS0 (BIOS/SMPC/Backup/WRAM-L)   2 + 1  = 3
//	A-Bus CS0/CS1/dummy (cartridge)         3 + 17 = 20
//	CD block (CS2)                          3 + 7  = 10
//	SCSP                                    3 + 9  = 12
//	VDP1                                    3 + 5  = 8
//	VDP2 VRAM                               3 + 5  = 8   (write 5)
//	VDP2 CRAM/regs, SCU regs                3 + 2  = 5
//	WRAM-H (CS3 SDRAM)                      3 + 4  = 7   (write 4)

// slaveArbCycles is the conservative per-access bus-arbitration handshake cost
// charged to the slave SH-2. The master holds the bus by default and pays none.
const slaveArbCycles = 1

// Per-region access-cost tables, indexed by masked megabyte (addr>>20)&0x7F
// and built once at package init from the switch derivations in
// computeAccessCycles / computeWriteAccessCycles below. AccessCycles,
// WriteAccessCycles, and the SH2* timing read these rather than re-deriving
// per access. Read and write, single (1/2/4 byte) and 16-byte burst are kept
// separate, since the memory device distinguishes them. The cost is a pure
// function of address (the BSC/SCU register values are fixed at boot), so the
// tables are program-global, not per-bus.
var (
	accessReadSingle  [128]uint32
	accessReadBurst   [128]uint32
	accessWriteSingle [128]uint32
	accessWriteBurst  [128]uint32
)

func init() {
	for i := uint32(0); i < 128; i++ {
		addr := i << 20
		accessReadSingle[i] = computeAccessCycles(addr, 4)
		accessReadBurst[i] = computeAccessCycles(addr, 16)
		accessWriteSingle[i] = computeWriteAccessCycles(addr, 4)
		accessWriteBurst[i] = computeWriteAccessCycles(addr, 16)
	}
}

// computeAccessCycles derives a region's read cost from the boot-time BSC/SCU
// register values:
//
//	SH-2 BSC  WCR  = 0x5555            (SH7604 manual Sec 7.2.3)
//	SH-2 BSC  MCR  = 0x78              (SH7604 manual Sec 7.2.4)
//	SCU       ASR0 = 0x1FF01FF0        (SCU manual Sec 3, Fig 3.24)
//	SCU       ASR1 = 0x1FF01FF0
//
// combined with the per-field formulas from the SH7604 and SCU user manuals.
// See each region's comment for the exact arithmetic. The B-Bus device costs
// (VDP1/VDP2/SCSP/CD) have no register-driven fixed number on hardware - they
// are arbitration (a VDP2 VRAM read stalls until a CPU-allotted slot in the
// 8-slot VRAM cycle pattern, VDP2 manual Sec 3, "Read/Write Access by the
// CPU"), carried here as fixed-point estimates until the slot machinery is
// modeled.
func computeAccessCycles(addr uint32, size uint32) uint32 {
	masked := addr & 0x07FFFFFF

	var base uint32
	switch {
	// CS0 partitions: BIOS ROM, SMPC, Backup RAM, Work RAM-L,
	// MINIT/SINIT. Ordinary-space with SH-2 WCR wait_CS0 = 1:
	//   states = 2 (base) + 1 (wait) = 3
	// (SH7604 Sec 7.2.3 + observed WCR=0x5555.)
	case masked < 0x02000000:
		base = 3

	// A-Bus CS0 (cartridge ROM / extended RAM).
	// SH-2 BSC: 3 states (WCR wait_CS1 = 1).
	// SCU A-Bus: 2 base + 15 waits = 17 states
	// (SCU manual Sec 3 + observed ASR0 upper half = 0x1FF0:
	//  A0EWT=1, A0BW=0xF=15, A0NW=0xF=15).
	// Total: 3 + 17 = 20 states per access.
	case masked < 0x04000000:
		base = 20

	// A-Bus CS1 (cartridge ID, A-Bus dummy high).
	// Same derivation from ASR0 lower half: A1EWT=1, A1BW=0xF=15,
	// A1NW=0xF=15. Total: 3 + 17 = 20.
	case masked < 0x05000000:
		base = 20

	// A-Bus Dummy region. SCU ASR1 observed = 0x1FF01FF0, same
	// decoding as ASR0. Total: 20.
	case masked < 0x05800000:
		base = 20

	// CD Block (A-Bus CS2 via SCU). CS2 is an A-Bus device configured
	// through ASR1, but the cost here is an estimate of the CD-block
	// interface response, not the raw ASR1 wait derivation used for
	// CS0/CS1 - the CD block's own access latency dominates.
	// BSC 3 + ~7 CD-block response = 10 states.
	case masked < 0x05900000:
		base = 10

	// SCSP sound RAM (512 KB) + registers (B-Bus). SCSP runs at
	// 11.3 MHz vs SH-2 28.6 MHz; B-Bus arbitration adds synchronization
	// cost. BSC 3 + B-Bus ~9 = 12 states. The 0x05A80000-0x05AFFFFF
	// gap between sound RAM and SCSP registers is unmapped on real
	// hardware; writes drop silently and fall to the default
	// unmapped cost.
	case masked >= 0x05A00000 && masked < 0x05A80000:
		base = 12
	case masked >= 0x05B00000 && masked < 0x05C00000:
		base = 12

	// VDP1 VRAM / framebuffer / registers (B-Bus). Contends with
	// VDP1 command-list DMA. BSC 3 + B-Bus ~5 = 8 states typical
	// idle.
	case masked >= 0x05C00000 && masked < 0x05D00020:
		base = 8

	// VDP2 VRAM (B-Bus). A CPU read stalls until a CPU-allotted
	// T-slot in the 8-slot VRAM cycle pattern arrives (VDP2 manual
	// Sec 3): 0 to a full rotation depending on CYCA/CYCB and
	// blanking. BSC 3 + ~5 average slot wait = 8 states as the
	// fixed-point estimate until the slot machinery is modeled.
	case masked >= 0x05E00000 && masked < 0x05E80000:
		base = 8

	// VDP2 CRAM and registers. Less contended than VRAM.
	// BSC 3 + B-Bus ~2 = 5 states.
	case masked >= 0x05F00000 && masked < 0x05F80120:
		base = 5

	// SCU registers (direct, no B-Bus arbitration).
	// BSC 3 + negligible = 5 states.
	case masked >= 0x05FE0000 && masked < 0x05FE0100:
		base = 5

	// Work RAM-H on SH-2 CS3, synchronous DRAM (MCR=0x78: RCD=1
	// cycle, TRP=1 cycle, auto-precharge; WCR=0x5555: W31/W30=01 =
	// CAS latency 2). Read pipeline per SH7604 Sec 7.5.3 Fig 7.15:
	//   Tr (ACTV) + Tc (READA) + 1 CAS-latency wait + Td1..Td4
	//   + Tap (= TRP-1 = 0 at CL>=2)            = 7 states.
	// A single (cache-through) read costs the SAME 7: the SDRAM is
	// in burst mode, so the device completes all four data cycles
	// and the unneeded 12 bytes are discarded (Sec 7.5.4).
	case masked >= 0x06000000 && masked < 0x08000000:
		base = 7

	default:
		// Unmapped or reserved. Nominal 4 states.
		base = 4
	}

	// 16-byte burst (cache line fill, 16-byte DMA unit).
	if size == 16 {
		switch {
		case masked >= 0x06000000 && masked < 0x08000000:
			// SDRAM: the burst read IS the single-read pipeline with
			// all four Td cycles fetched (Sec 7.5.3) = 7 states.
			return base
		case masked >= 0x02000000 && masked < 0x05800000:
			// A-Bus: first access pays the normal-cycle cost (20);
			// the three remaining beats pay the SCU burst-cycle wait
			// (ASR0/ASR1 burst field 0xF = 15 waits + 1) = 16 each.
			// 20 + 3*16 = 68 states (SCU manual Sec 3, Fig 3.24).
			return base + 3*16
		default:
			// Ordinary space and B-Bus devices have no burst mode:
			// 4 successive longword accesses.
			return base * 4
		}
	}
	return base
}

// computeWriteAccessCycles derives a region's write cost. Writes differ
// from reads only where the memory device distinguishes them:
//
//   - Work RAM-H SDRAM writes are single writes (SH7604 Sec 7.5.5
//     Fig 7.18): Tr (ACTV) + Tc (WRITA, data out with the command),
//     then TRWL (1) + precharge (TRP = 1) before the next command to
//     the bank = 4 states. The SH-2 has no write buffer - the CPU
//     waits for the internal bus write to complete (Sec 8.4.2) - so
//     the full cost lands on the writing instruction.
//   - VDP2 VRAM/CRAM writes are accepted without the cycle-pattern
//     slot wait that reads incur (VDP2 manual Sec 3, "Read/Write
//     Access by the CPU": "The write access wait cycle will not be
//     entered if the two word write access is at least two times"),
//     so they cost only the BSC + B-Bus handoff.
//
// Everything else matches the read cost: ordinary-space and A-Bus
// cycles are direction-symmetric in the BSC, and the remaining B-Bus
// device costs are arbitration estimates with no documented
// read/write asymmetry.
func computeWriteAccessCycles(addr uint32, size uint32) uint32 {
	masked := addr & 0x07FFFFFF
	switch {
	case masked >= 0x06000000 && masked < 0x08000000:
		return 4
	case masked >= 0x05E00000 && masked < 0x05F80120:
		return 5
	}
	return computeAccessCycles(addr, size)
}

// AccessCycles returns the CPU state count consumed by a single read
// transaction of the given size (bytes: 1/2/4, or 16 for a burst) at addr -
// a table lookup. Used by the SH-2 DMAC to accumulate per-unit bus-occupation
// stall. The 1 MB table granularity collapses the few sub-megabyte region
// tails to their block's cost.
func (b *Bus) AccessCycles(addr uint32, size uint32) uint32 {
	if size == 16 {
		return accessReadBurst[(addr>>20)&0x7F]
	}
	return accessReadSingle[(addr>>20)&0x7F]
}

// WriteAccessCycles is the write-direction companion to AccessCycles.
func (b *Bus) WriteAccessCycles(addr uint32, size uint32) uint32 {
	if size == 16 {
		return accessWriteBurst[(addr>>20)&0x7F]
	}
	return accessWriteSingle[(addr>>20)&0x7F]
}

// resetContention clears the per-frame contention window. Called from
// RunFrame's top with both CPU workers parked, so there is no concurrent
// access to busyUntil.
func (b *Bus) resetContention() {
	clear(b.busyUntil[:])
}

// chargeAccess returns the wait-state cycles an SH-2 access adds to busStall -
// region cost (minus the instruction's own MA cycle) + slave arbitration +
// inter-CPU contention wait - and advances the CPU-Bus busy window. cost is the
// full transaction tenure (AccessCycles). Must be called inside the area lock so
// the busyUntil read-modify-write shares the data access's bus tenure.
func (b *Bus) chargeAccess(area uint8, cost uint32, frameCyc int64, slave bool) uint32 {
	stall := cost - 1
	if slave {
		stall += slaveArbCycles
	}
	if area == areaCPUBus {
		c := int64(cost)
		wait := b.busyUntil[area] - frameCyc
		if wait < 0 {
			wait = 0
		} else if wait > c {
			wait = c
		}
		b.busyUntil[area] = frameCyc + wait + c
		stall += uint32(wait)
	}
	return stall
}

// The SH2* methods are the SH-2 instruction-execution access path: a data
// access and chargeAccess folded into one bus tenure, returning the stall the
// CPU adds to busStall (reads also return the value). frameCyc is the accessing
// CPU's frame-relative cycle; slave selects the arbitration penalty. Width is
// in the method name, matching the plain Read*/Write* in bus.go, which are the
// non-timed path (on-chip DMAC, HLE, SCU).
func (b *Bus) SH2Read32(addr uint32, frameCyc int64, slave bool) (uint32, uint32) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessReadSingle[(addr>>20)&0x7F], frameCyc, slave)
	val := b.read32Impl(addr)
	b.unlockArea(area)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 4, val)
	}
	return val, stall
}

func (b *Bus) SH2Read16(addr uint32, frameCyc int64, slave bool) (uint16, uint32) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessReadSingle[(addr>>20)&0x7F], frameCyc, slave)
	val := b.read16Impl(addr)
	b.unlockArea(area)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 2, uint32(val))
	}
	return val, stall
}

func (b *Bus) SH2Read8(addr uint32, frameCyc int64, slave bool) (uint8, uint32) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessReadSingle[(addr>>20)&0x7F], frameCyc, slave)
	val := b.read8Impl(addr)
	b.unlockArea(area)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 1, uint32(val))
	}
	return val, stall
}

func (b *Bus) SH2Write32(addr uint32, val uint32, frameCyc int64, slave bool) uint32 {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessWriteSingle[(addr>>20)&0x7F], frameCyc, slave)
	b.write32Impl(addr, val)
	b.unlockArea(area)
	return stall
}

func (b *Bus) SH2Write16(addr uint32, val uint16, frameCyc int64, slave bool) uint32 {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessWriteSingle[(addr>>20)&0x7F], frameCyc, slave)
	b.write16Impl(addr, val)
	b.unlockArea(area)
	return stall
}

func (b *Bus) SH2Write8(addr uint32, val uint8, frameCyc int64, slave bool) uint32 {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessWriteSingle[(addr>>20)&0x7F], frameCyc, slave)
	b.write8Impl(addr, val)
	b.unlockArea(area)
	return stall
}

func (b *Bus) SH2FillLine(base uint32, dst *[16]byte, frameCyc int64, slave bool) uint32 {
	area := busAreaOf(base & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessReadBurst[(base>>20)&0x7F], frameCyc, slave)
	for i := uint32(0); i < 16; i += 4 {
		v := b.read32Impl(base + i)
		dst[i] = uint8(v >> 24)
		dst[i+1] = uint8(v >> 16)
		dst[i+2] = uint8(v >> 8)
		dst[i+3] = uint8(v)
	}
	b.unlockArea(area)
	return stall
}

// SH2RMWRead begins a TAS.B: it charges the read access (cost + slave + wait)
// and holds the area. SH2RMWWrite completes it, re-extending busyUntil to cover
// the write so the peer waits for the whole RMW, and charges only the write
// cost (the slave handshake and contention wait were already paid on the read).
func (b *Bus) SH2RMWRead(addr uint32, frameCyc int64, slave bool) (uint8, uint32) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	stall := b.chargeAccess(area, accessReadSingle[(addr>>20)&0x7F], frameCyc, slave)
	val := b.read8Impl(addr)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 1, uint32(val))
	}
	return val, stall
}

func (b *Bus) SH2RMWWrite(addr uint32, val uint8, frameCyc int64) uint32 {
	area := busAreaOf(addr & 0x07FFFFFF)
	cost := accessWriteSingle[(addr>>20)&0x7F]
	if area == areaCPUBus {
		end := b.busyUntil[area]
		if frameCyc > end {
			end = frameCyc
		}
		b.busyUntil[area] = end + int64(cost)
	}
	b.write8Impl(addr, val)
	b.unlockArea(area)
	return cost - 1
}
