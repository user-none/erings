// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// testBus is a minimal Bus implementation backed by a flat byte slice.
// Big-endian byte order matches the SH-2 convention.
type testBus struct {
	mem        []byte
	accessCost uint32 // per-access wait states returned by AccessCycles
}

func newTestBus(size int) *testBus {
	return &testBus{mem: make([]byte, size)}
}

func (b *testBus) Read8(addr uint32) uint8 {
	return b.mem[addr]
}

func (b *testBus) Read16(addr uint32) uint16 {
	return uint16(b.mem[addr])<<8 | uint16(b.mem[addr+1])
}

func (b *testBus) Read32(addr uint32) uint32 {
	return uint32(b.mem[addr])<<24 | uint32(b.mem[addr+1])<<16 |
		uint32(b.mem[addr+2])<<8 | uint32(b.mem[addr+3])
}

func (b *testBus) Write8(addr uint32, val uint8) {
	b.mem[addr] = val
}

func (b *testBus) Write16(addr uint32, val uint16) {
	b.mem[addr] = uint8(val >> 8)
	b.mem[addr+1] = uint8(val)
}

func (b *testBus) Write32(addr uint32, val uint32) {
	b.mem[addr] = uint8(val >> 24)
	b.mem[addr+1] = uint8(val >> 16)
	b.mem[addr+2] = uint8(val >> 8)
	b.mem[addr+3] = uint8(val)
}

// AccessCycles returns the configured per-access wait states. It defaults to
// 0 (zero-wait) so instruction-timing tests are unaffected by bus stall;
// tests that exercise DMAC stall or CPU bus-access stall set accessCost.
func (b *testBus) AccessCycles(addr uint32, size uint32) uint32 {
	return b.accessCost
}

func TestFetchPC(t *testing.T) {
	bus := newTestBus(256)
	// Write a 16-bit value at address 0x10 (big-endian: 0xAB 0xCD = 0xABCD)
	bus.mem[0x10] = 0xAB
	bus.mem[0x11] = 0xCD

	cpu := New(bus, true)
	cpu.reg.PC = 0x10

	got := cpu.fetchPC()
	if got != 0xABCD {
		t.Errorf("fetchPC() value = 0x%04X, want 0xABCD", got)
	}
	if cpu.reg.PC != 0x12 {
		t.Errorf("PC after fetchPC() = 0x%08X, want 0x00000012", cpu.reg.PC)
	}
}

func TestFetchPCOddAddress(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Set up address error vector (vector 9) to point to handler at 0x100
	handlerAddr := uint32(0x100)
	bus.Write32(uint32(vecCPUAddr)*4, handlerAddr)

	// NOP at handler address so Step can execute it
	bus.Write16(handlerAddr, 0x0009) // NOP

	cpu.reg.PC = 0x11 // odd PC
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0

	got := cpu.fetchPC()
	if got != 0 {
		t.Errorf("fetchPC() with odd PC returned 0x%04X, want 0", got)
	}
	if !cpu.addrError {
		t.Error("addrError not set after odd PC fetch")
	}
	if cpu.reg.PC != handlerAddr {
		t.Errorf("PC = 0x%08X, want 0x%08X (handler)", cpu.reg.PC, handlerAddr)
	}
}

func TestFetchPCSequential(t *testing.T) {
	bus := newTestBus(256)
	// Two consecutive instructions
	bus.mem[0x00] = 0x12
	bus.mem[0x01] = 0x34
	bus.mem[0x02] = 0x56
	bus.mem[0x03] = 0x78

	cpu := New(bus, true)
	cpu.reg.PC = 0x00

	first := cpu.fetchPC()
	second := cpu.fetchPC()

	if first != 0x1234 {
		t.Errorf("first fetchPC() = 0x%04X, want 0x1234", first)
	}
	if second != 0x5678 {
		t.Errorf("second fetchPC() = 0x%04X, want 0x5678", second)
	}
	if cpu.reg.PC != 0x04 {
		t.Errorf("PC after two fetches = 0x%08X, want 0x00000004", cpu.reg.PC)
	}
}

// setupAddrErrorTest creates a CPU with address error vector pointing to
// handlerAddr. Returns the CPU and bus. The handler contains a NOP.
func setupAddrErrorTest(handlerAddr uint32) (*CPU, *testBus) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Address error vector (9) -> handler
	bus.Write32(uint32(vecCPUAddr)*4, handlerAddr)

	// NOP at handler
	bus.Write16(handlerAddr, 0x0009)

	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	return cpu, bus
}

func TestAddressErrorWordRead(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	// MOV.W @R1,R0 = 0x6011 (n=0, m=1, op=1)
	// Place instruction at 0x200
	bus.Write16(0x200, 0x6011)
	cpu.reg.PC = 0x200
	cpu.reg.R[1] = 0x301 // odd -> address error

	cpu.Clock()

	if cpu.reg.PC != handler {
		t.Errorf("PC = 0x%08X, want 0x%08X", cpu.reg.PC, handler)
	}
	// Stacked PC should be the next instruction (0x202)
	stackedPC := bus.Read32(cpu.reg.R[15])
	if stackedPC != 0x202 {
		t.Errorf("stacked PC = 0x%08X, want 0x00000202", stackedPC)
	}
}

func TestAddressErrorWordWrite(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	// MOV.W R1,@R0 = 0x2011 (n=0, m=1, op=1)
	bus.Write16(0x200, 0x2011)
	cpu.reg.PC = 0x200
	cpu.reg.R[0] = 0x303 // odd -> address error
	cpu.reg.R[1] = 0xBEEF

	cpu.Clock()

	if cpu.reg.PC != handler {
		t.Errorf("PC = 0x%08X, want 0x%08X", cpu.reg.PC, handler)
	}
}

func TestAddressErrorLongRead(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	// MOV.L @R1,R0 = 0x6012 (n=0, m=1, op=2)
	bus.Write16(0x200, 0x6012)
	cpu.reg.PC = 0x200
	cpu.reg.R[1] = 0x302 // aligned to 2 but not 4 -> address error

	cpu.Clock()

	if cpu.reg.PC != handler {
		t.Errorf("PC = 0x%08X, want 0x%08X", cpu.reg.PC, handler)
	}
}

func TestAddressErrorLongWrite(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	// MOV.L R1,@R0 = 0x2012 (n=0, m=1, op=2)
	bus.Write16(0x200, 0x2012)
	cpu.reg.PC = 0x200
	cpu.reg.R[0] = 0x306 // not 4-aligned -> address error
	cpu.reg.R[1] = 0xDEADBEEF

	cpu.Clock()

	if cpu.reg.PC != handler {
		t.Errorf("PC = 0x%08X, want 0x%08X", cpu.reg.PC, handler)
	}
}

func TestAddressErrorStacking(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	// MOV.W @R1,R0 = 0x6011
	bus.Write16(0x200, 0x6011)
	cpu.reg.PC = 0x200
	cpu.reg.SR = 0x30 // IMASK=3
	cpu.reg.R[1] = 0x301
	sp := uint32(0x800)
	cpu.reg.R[15] = sp

	cpu.Clock()

	// SR and PC should be stacked
	stackedPC := bus.Read32(cpu.reg.R[15])
	stackedSR := bus.Read32(cpu.reg.R[15] + 4)

	if stackedPC != 0x202 {
		t.Errorf("stacked PC = 0x%08X, want 0x00000202", stackedPC)
	}
	if stackedSR != 0x30 {
		t.Errorf("stacked SR = 0x%08X, want 0x00000030", stackedSR)
	}
	// Address error should NOT modify IMASK (unlike interrupts)
	if cpu.reg.IMASK() != 3 {
		t.Errorf("IMASK = %d, want 3 (unchanged)", cpu.reg.IMASK())
	}
}

// TestAddressErrorHandlerFetchFromVBR verifies the vector-9 handler
// address is fetched from VBR + 9*4 (not from absolute 0x24) when an
// address error is raised. HM Sec 4.1.3 Table 4.4 requires non-reset
// exception vectors be read from VBR + (vector number)*4. HM Sec 4.3.2
// step 3 applies this to address errors. HM Table 4.3 row "CPU address
// error" assigns vector 9. Complements TestAddressErrorWordRead and
// TestAddressErrorStacking which both use VBR=0.
func TestAddressErrorHandlerFetchFromVBR(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	vbr := uint32(0x200)
	cpu.reg.VBR = vbr
	handler := uint32(0x500)
	bus.Write32(vbr+uint32(vecCPUAddr)*4, handler)
	bus.Write16(handler, 0x0009) // NOP at handler

	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	cpu.reg.R[1] = 0x301 // odd -> address error

	bus.Write16(0x300, 0x6011) // MOV.W @R1,R0

	cpu.Clock()

	if cpu.reg.PC != handler {
		t.Errorf("PC = 0x%08X, want 0x%08X (handler from VBR + 9*4 = 0x%08X)",
			cpu.reg.PC, handler, vbr+uint32(vecCPUAddr)*4)
	}
	stackedPC := bus.Read32(cpu.reg.R[15])
	if stackedPC != 0x302 {
		t.Errorf("stacked PC = 0x%08X, want 0x00000302", stackedPC)
	}
}

func TestNoAddressErrorByteAccess(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	bus.Write32(uint32(vecCPUAddr)*4, 0x100)

	// MOV.B @R1,R0 = 0x6010 (byte read from odd address - should be fine)
	bus.Write16(0x200, 0x6010)
	cpu.reg.PC = 0x200
	cpu.reg.R[15] = 0x800
	cpu.reg.R[1] = 0x301 // odd, but byte access is OK
	bus.Write8(0x301, 0x42)

	cpu.Clock()

	// Should execute normally, no address error
	if cpu.reg.PC != 0x202 {
		t.Errorf("PC = 0x%08X, want 0x00000202 (no error)", cpu.reg.PC)
	}
	if cpu.reg.R[0] != 0x42 {
		t.Errorf("R0 = 0x%08X, want 0x00000042", cpu.reg.R[0])
	}
}

func TestNoAddressErrorAlignedAccess(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	bus.Write32(uint32(vecCPUAddr)*4, 0x100)

	// MOV.L @R1,R0 = 0x6012 (long read from aligned address)
	bus.Write16(0x200, 0x6012)
	cpu.reg.PC = 0x200
	cpu.reg.R[15] = 0x800
	cpu.reg.R[1] = 0x300 // 4-aligned
	bus.Write32(0x300, 0xCAFEBABE)

	cpu.Clock()

	if cpu.reg.PC != 0x202 {
		t.Errorf("PC = 0x%08X, want 0x00000202 (no error)", cpu.reg.PC)
	}
	if cpu.reg.R[0] != 0xCAFEBABE {
		t.Errorf("R0 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[0])
	}
}

// --- 5.2 Region-dispatch predicates and cache-control regions ---
// Hardware Manual Table 7.3 memory map. Predicates isOnChip,
// isCacheRegion, and isCacheDataArray must match the documented
// address ranges.

func TestIsOnChipBoundaries(t *testing.T) {
	if isOnChip(0xFFFF7FFF) {
		t.Error("0xFFFF7FFF classified as on-chip")
	}
	if !isOnChip(0xFFFF8000) {
		t.Error("0xFFFF8000 not classified as on-chip")
	}
	if !isOnChip(0xFFFFFFFF) {
		t.Error("0xFFFFFFFF not classified as on-chip")
	}
}

func TestIsCacheRegionBoundaries(t *testing.T) {
	if isCacheRegion(0x3FFFFFFF) {
		t.Error("0x3FFFFFFF classified as cache region")
	}
	if !isCacheRegion(0x40000000) {
		t.Error("0x40000000 not classified as cache region")
	}
	if !isCacheRegion(0xBFFFFFFF) {
		t.Error("0xBFFFFFFF not classified as cache region")
	}
	if isCacheRegion(0xC0000000) {
		t.Error("0xC0000000 classified as cache region")
	}
}

func TestIsCacheDataArrayBoundaries(t *testing.T) {
	if isCacheDataArray(0xBFFFFFFF) {
		t.Error("0xBFFFFFFF classified as data array")
	}
	if !isCacheDataArray(0xC0000000) {
		t.Error("0xC0000000 not classified as data array")
	}
	if !isCacheDataArray(0xDFFFFFFF) {
		t.Error("0xDFFFFFFF not classified as data array")
	}
	if isCacheDataArray(0xE0000000) {
		t.Error("0xE0000000 classified as data array")
	}
}

// TestAssociativePurgeRegion covers 0x40000000..0x47FFFFFF (Hardware
// Manual Sec 8.4.7). Writes invalidate a cache line; reads are not
// defined. Current code returns 0 on read and drops writes.
func TestAssociativePurgeRegion(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0x40000000, 0xDEADBEEF)
	if v := cpu.read32(0x40000000); v != 0 {
		t.Errorf("associative purge read = 0x%08X, want 0", v)
	}
}

// TestAddressArrayRegion covers 0x60000000..0x600003FF (Hardware Manual
// Sec 8.4.9). The address array holds tag/LRU/valid bits. Current code
// returns 0 on read and drops writes.
func TestAddressArrayRegion(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0x60000000, 0xDEADBEEF)
	if v := cpu.read32(0x60000000); v != 0 {
		t.Errorf("address array read = 0x%08X, want 0", v)
	}
}

// TestReservedRegion80_BF locks divergence D4 from README: the 1 GB
// range 0x80000000..0xBFFFFFFF is Reserved per Table 7.3. Current code
// returns 0 on read and drops writes.
func TestReservedRegion80_BF(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0x80000000, 0xAAAAAAAA)
	cpu.write32(0xBFFFFFFC, 0xBBBBBBBB)
	if v := cpu.read32(0x80000000); v != 0 {
		t.Errorf("reserved read at 0x80000000 = 0x%08X, want 0", v)
	}
	if v := cpu.read32(0xBFFFFFFC); v != 0 {
		t.Errorf("reserved read at 0xBFFFFFFC = 0x%08X, want 0", v)
	}
}

// TestAssociativePurgeReservedAliasDivergence locks divergence D2 from
// README: hardware reserves 0x48000000..0x5FFFFFFF. Current code folds
// it into the associative-purge no-op.
func TestAssociativePurgeReservedAliasDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0x48000000, 0xCAFECAFE)
	cpu.write32(0x5FFFFFFC, 0xBABEBABE)
	if v := cpu.read32(0x48000000); v != 0 {
		t.Errorf("read at 0x48000000 = 0x%08X, want 0", v)
	}
	if v := cpu.read32(0x5FFFFFFC); v != 0 {
		t.Errorf("read at 0x5FFFFFFC = 0x%08X, want 0", v)
	}
}

// TestAddressArrayReservedAliasDivergence locks divergence D3 from
// README: hardware maps the address array at 0x60000000..0x600003FF
// only. Current code treats the full 512 MB block as one no-op range.
func TestAddressArrayReservedAliasDivergence(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	cpu.write32(0x60000400, 0xCAFECAFE)
	cpu.write32(0x7FFFFFFC, 0xBABEBABE)
	if v := cpu.read32(0x60000400); v != 0 {
		t.Errorf("read at 0x60000400 = 0x%08X, want 0", v)
	}
	if v := cpu.read32(0x7FFFFFFC); v != 0 {
		t.Errorf("read at 0x7FFFFFFC = 0x%08X, want 0", v)
	}
}

// --- 5.6 BCR1 (BSC) ---
// Hardware Manual Sec 7.2.1. BCR1 is a 16-bit register at 0xFFFFFFE0.
// Longword reads return the 16-bit value in the low half; longword
// writes require 0xA55A in the upper 16 bits as a write-protect key.
// Bit 15 is the MASTER bit: read-only, 0 for master, 1 for slave.
// Initial value: H'03F0.

func TestBCR1InitialValue(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	got, _ := cpu.readOnChip(0xFFFFFFE0)
	if got != 0x03F0 {
		t.Errorf("BCR1 initial (master) = 0x%08X, want 0x000003F0", got)
	}
}

func TestBCR1MasterSlaveBit(t *testing.T) {
	bus := newTestBus(0x1000)
	master := New(bus, true)
	slave := New(bus, false)

	if v, _ := master.readOnChip(0xFFFFFFE0); v != 0x03F0 {
		t.Errorf("master BCR1 = 0x%08X, want 0x000003F0 (bit 15 clear)", v)
	}
	if v, _ := slave.readOnChip(0xFFFFFFE0); v != 0x83F0 {
		t.Errorf("slave BCR1 = 0x%08X, want 0x000083F0 (bit 15 set)", v)
	}
}

func TestBCR1WriteWithoutKey(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.writeOnChip(0xFFFFFFE0, 0x0000FFFF)
	got, _ := cpu.readOnChip(0xFFFFFFE0)
	if got != 0x03F0 {
		t.Errorf("BCR1 after bad-key write = 0x%08X, want 0x000003F0 (unchanged)", got)
	}
}

func TestBCR1WriteWithKey(t *testing.T) {
	bus := newTestBus(0x1000)
	master := New(bus, true)
	slave := New(bus, false)

	master.writeOnChip(0xFFFFFFE0, 0xA55A0155)
	got, _ := master.readOnChip(0xFFFFFFE0)
	if got != 0x0155 {
		t.Errorf("master BCR1 after keyed write = 0x%08X, want 0x00000155", got)
	}

	slave.writeOnChip(0xFFFFFFE0, 0xA55A0155)
	got, _ = slave.readOnChip(0xFFFFFFE0)
	if got != 0x8155 {
		t.Errorf("slave BCR1 after keyed write = 0x%08X, want 0x00008155 (bit 15 retained)", got)
	}
}

func TestBCR1UpperHalfZero(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	got, _ := cpu.readOnChip(0xFFFFFFE0)
	if got>>16 != 0 {
		t.Errorf("BCR1 upper 16 = 0x%04X, want 0", got>>16)
	}
}

// TestBCR1Read16AtBase covers 16-bit reads at the 32-bit base address.
// Per manual Table 7.2 Note 1 the 16-bit access address is +2 (see
// TestBCR1Read16AtSpecOffset), but readOnChip surfaces the BCR1 value
// in the low 16 bits of the 32-bit slot, so uint16 truncation of the
// 32-bit read at the base returns the same value.
func TestBCR1Read16AtBase(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	if got := cpu.read16(0xFFFFFFE0); got != 0x03F0 {
		t.Errorf("BCR1 read16 at base = 0x%04X, want 0x03F0", got)
	}
}

// TestBCR1Read16AtSpecOffset asserts the spec-mandated 16-bit access
// at offset +2 from the 32-bit base (manual Table 7.2 Note 1).
func TestBCR1Read16AtSpecOffset(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	if got := cpu.read16(0xFFFFFFE2); got != 0x03F0 {
		t.Errorf("BCR1 read16 at spec +2 = 0x%04X, want 0x03F0 (per Table 7.2 Note 1)", got)
	}
}

// Address-error behavior during the executing instruction (HM Sec
// 4.3.2 / 4.7 / 4.8.3) is intentionally not modeled. See README
// section "Address error during instruction execution (not modeled
// correctly)" for details. Tests that exercised this path were
// removed because they asserted neither HM-correct behavior nor a
// stable divergence we want to lock in.

// HM Sec 4.3.1 Table 4.6: fetch from an odd address raises an
// address error. When a JMP targets an odd address, the delay slot
// runs first, the branch completes, and the next instruction fetch
// at the odd target traps to vector 9. README "Address error during
// instruction execution (not modeled correctly)" notes erings
// services the trap synchronously from fetchPC, so stacked PC is
// the odd fetch address rather than HM Sec 4.7 Table 4.11's
// "address of instruction after executed instruction." This test
// pins the erings-observed stacked PC.
func TestJMPToOddAddressAddressErrors(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	cpu.reg.PC = 0x200
	cpu.reg.R[0] = 0x301       // odd branch target
	bus.Write16(0x200, 0x402B) // JMP @R0
	bus.Write16(0x202, 0x0009) // NOP delay slot

	cpu.Clock()
	// Drain popStall refill and delay slot.
	for cpu.pendingOp == popStall {
		cpu.Clock()
	}
	cpu.Clock() // execute delay slot, take branch
	if cpu.reg.PC != 0x301 {
		t.Fatalf("after delay slot: PC = 0x%08X, want 0x301 (odd target)", cpu.reg.PC)
	}
	// Next Clock fetches at the odd PC and traps.
	cpu.Clock()
	if cpu.reg.PC != handler {
		t.Errorf("address-error handler not taken: PC = 0x%08X, want 0x%X", cpu.reg.PC, handler)
	}
	stackedPC := bus.Read32(cpu.reg.R[15])
	if stackedPC != 0x301 {
		t.Errorf("stacked PC = 0x%08X, want 0x301 (erings observed behavior; "+
			"HM Sec 4.7 Table 4.11 specifies a different value - README documents divergence)",
			stackedPC)
	}
}

// HM Sec 4.3.1 Table 4.6 odd-fetch row via BSR. BSR is a delayed
// branch; PR is updated at dispatch (i.e. BSR completed) before
// the fetch at the odd target traps. Same README-documented stacked-
// PC divergence applies.
func TestBSRToOddAddressAddressErrors(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	cpu.reg.PC = 0x200
	// BSR disp: target = PC+4 + disp*2. PC after fetch = 0x202,
	// target 0x301. disp = (0x301 - 0x204)/2 -- but the target is
	// odd so the halfword encoding of disp ends up truncating. Use
	// BSRF Rn instead so we can aim at an odd target directly:
	// BSRF Rn: target = PC+4 + Rn. Want 0x301, PC+4 = 0x204 -> Rn=0x0FD.
	cpu.reg.R[2] = 0x0FD
	bus.Write16(0x200, 0x0203) // BSRF R2 = 0000 nnnn 0000 0011
	bus.Write16(0x202, 0x0009) // NOP delay slot

	cpu.Clock()
	// PM BSRF: PC (address of instruction after delay slot) -> PR.
	// For BSRF at 0x200 with delay slot at 0x202, the return address
	// is 0x204.
	if cpu.reg.PR != 0x204 {
		t.Fatalf("BSRF PR = 0x%08X, want 0x204 (return addr after delay slot)",
			cpu.reg.PR)
	}
	for cpu.pendingOp == popStall {
		cpu.Clock()
	}
	cpu.Clock() // delay slot + branch
	if cpu.reg.PC != 0x301 {
		t.Fatalf("after delay slot: PC = 0x%08X, want 0x301", cpu.reg.PC)
	}
	cpu.Clock()
	if cpu.reg.PC != handler {
		t.Errorf("handler not entered: PC = 0x%08X", cpu.reg.PC)
	}
	if cpu.reg.PR != 0x204 {
		t.Errorf("PR clobbered by trap: 0x%08X, want 0x204", cpu.reg.PR)
	}
}

// HM Sec 4.3.1 Table 4.6 via RTS. RTS pops PR into PC (delayed
// branch). If PR is odd the post-delay-slot fetch traps.
func TestRTSToOddPRAddressErrors(t *testing.T) {
	handler := uint32(0x100)
	cpu, bus := setupAddrErrorTest(handler)

	cpu.reg.PC = 0x200
	cpu.reg.PR = 0x301
	bus.Write16(0x200, 0x000B) // RTS
	bus.Write16(0x202, 0x0009) // NOP delay slot

	cpu.Clock()
	for cpu.pendingOp == popStall {
		cpu.Clock()
	}
	cpu.Clock() // delay slot + branch
	if cpu.reg.PC != 0x301 {
		t.Fatalf("after delay slot: PC = 0x%08X, want 0x301", cpu.reg.PC)
	}
	cpu.Clock()
	if cpu.reg.PC != handler {
		t.Errorf("handler not entered: PC = 0x%08X", cpu.reg.PC)
	}
}

// sparseBus wraps testBus and returns 0 for any read outside the
// backing slice, matching real-hardware behavior for out-of-range
// reads and letting tests drive PC into the on-chip peripheral
// region without panicking.
type sparseBus struct {
	*testBus
}

func (s *sparseBus) Read16(addr uint32) uint16 {
	if int(addr)+1 >= len(s.testBus.mem) || int(addr) < 0 {
		return 0
	}
	return s.testBus.Read16(addr)
}

func (s *sparseBus) Read32(addr uint32) uint32 {
	if int(addr)+3 >= len(s.testBus.mem) || int(addr) < 0 {
		return 0
	}
	return s.testBus.Read32(addr)
}

func (s *sparseBus) Read8(addr uint32) uint8 {
	if int(addr) >= len(s.testBus.mem) || int(addr) < 0 {
		return 0
	}
	return s.testBus.Read8(addr)
}

// Simplification lock. HM Sec 4.3.1 Table 4.6 row "Instruction
// fetched from on-chip peripheral module space" requires an address
// error (vector 9). README D9 documents this row as unmodeled:
// erings's fetchPC (cpu.go:546) only checks odd-PC, not on-chip
// space. The bus.Read16 is issued directly. This test pins the
// simplified behavior - a future change that adds the trap has to
// revisit this case explicitly.
func TestFetchFromOnChipSpaceNoAddressErrorDivergence(t *testing.T) {
	sp := &sparseBus{testBus: newTestBus(0x1000)}
	cpu := New(sp, true)
	cpu.reg.VBR = 0
	sp.Write32(uint32(vecCPUAddr)*4, 0x100)
	sp.Write16(0x100, 0x0009)
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0xFFFFFE10 // on-chip peripheral space, even-aligned

	cpu.fetchPC()

	if cpu.addrError {
		t.Error("addrError raised on fetch from on-chip space; erings simplification expects no trap")
	}
	if cpu.reg.PC != 0xFFFFFE12 {
		t.Errorf("PC after on-chip fetch = 0x%08X, want 0xFFFFFE12 (advance by 2)",
			cpu.reg.PC)
	}
}

// HM Sec 7.2.2: BCR1 bit 15 MASTER "is a read-only bit." The chip's
// mode-pin latch drives the bit; software cannot flip it via the
// A55A-keyed write path. writeOnChip at cpu.go:908 masks bit 15
// out of the stored value. Existing TestBCR1WriteWithKey only
// writes 0x0155 (bit 15 already 0) so this corner is unexercised.
func TestBCR1MasterBitNotSettableByKeyedWrite(t *testing.T) {
	bus := newTestBus(0x1000)

	master := New(bus, true)
	// Write with bit 15 set; master must still read bit 15 = 0.
	master.writeOnChip(0xFFFFFFE0, 0xA55A_FFFF)
	got, _ := master.readOnChip(0xFFFFFFE0)
	if got&0x8000 != 0 {
		t.Errorf("master BCR1 bit 15 = 1 after keyed write with bit15=1; "+
			"MASTER bit must be read-only (got 0x%08X)", got)
	}

	slave := New(bus, false)
	// Write with bit 15 clear; slave must still read bit 15 = 1.
	slave.writeOnChip(0xFFFFFFE0, 0xA55A_0000)
	got, _ = slave.readOnChip(0xFFFFFFE0)
	if got&0x8000 == 0 {
		t.Errorf("slave BCR1 bit 15 = 0 after keyed write with bit15=0; "+
			"MASTER bit must be read-only (got 0x%08X)", got)
	}
}

// HM Sec 7.2.1: "The BSC is not affected" by manual reset. The
// MASTER bit is tied to the mode pin and therefore survives
// CPU.Reset() on both master and slave.
func TestBCR1MasterBitPersistsAcrossManualReset(t *testing.T) {
	bus := newTestBus(0x1000)

	master := New(bus, true)
	master.Reset()
	got, _ := master.readOnChip(0xFFFFFFE0)
	if got&0x8000 != 0 {
		t.Errorf("master BCR1 bit 15 = 1 after Reset; want 0, got 0x%08X", got)
	}

	slave := New(bus, false)
	slave.Reset()
	got, _ = slave.readOnChip(0xFFFFFFE0)
	if got&0x8000 == 0 {
		t.Errorf("slave BCR1 bit 15 = 0 after Reset; want 1, got 0x%08X", got)
	}
}

// HM Sec 7.2 + per-CPU on-chip structure: each SH-2 has its own
// instance of every on-chip peripheral. Extends
// TestCacheDataArrayMasterSlaveIsolation (which covers the 4 KB
// data array) to DIVU / FRT / WDT registers.
func TestDualCPUIndependentOnChipState(t *testing.T) {
	bus := newTestBus(0x1000)
	master := New(bus, true)
	slave := New(bus, false)

	// DIVU DVSR (0xFFFFFF00): write on master, slave must stay at reset 0.
	master.writeOnChip(0xFFFFFF00, 0xCAFE1234)
	mv, _ := master.readOnChip(0xFFFFFF00)
	sv, _ := slave.readOnChip(0xFFFFFF00)
	if mv != 0xCAFE1234 {
		t.Errorf("master DVSR = 0x%08X, want 0xCAFE1234", mv)
	}
	if sv != 0 {
		t.Errorf("slave DVSR = 0x%08X, want 0 (independent from master)", sv)
	}

	// FRT TCR (0xFFFFFE16): write on slave, master must stay at reset 0.
	slave.writeOnChip(0xFFFFFE16, 0x02)
	if master.frt.tcr != 0 {
		t.Errorf("master FRT TCR = 0x%02X after slave write, want 0", master.frt.tcr)
	}
	if slave.frt.tcr != 0x02 {
		t.Errorf("slave FRT TCR = 0x%02X, want 0x02", slave.frt.tcr)
	}

	// WDT WTCSR via A5-keyed write on master; slave stays at reset 0x18.
	master.writeOnChip(0xFFFFFE80, 0xA520)
	if slave.wdt.wtcsr != 0x18 {
		t.Errorf("slave WTCSR = 0x%02X after master write, want 0x18 (reset)",
			slave.wdt.wtcsr)
	}
	if master.wdt.wtcsr&wtcsrTME == 0 {
		t.Errorf("master WTCSR.TME = 0 after own write, want 1 (self-affecting)")
	}
}

// TestBusAccessStallCharged verifies the per-region bus-access stall: each
// external data read/write adds its region's table cost to the stall debt,
// regions differ, and on-chip registers and instruction fetches do not stall.
func TestBusAccessStallCharged(t *testing.T) {
	bus := newTestBus(0x10000)
	cpu := New(bus, true)
	var tbl [128]uint32
	tbl[0] = 3     // region of addr 0x000xxxxx
	tbl[1] = 7     // region of addr 0x001xxxxx (per-location differs)
	tbl[0x60] = 30 // region of addr 0x060xxxxx (WRAM-H, 0x06000000>>20)
	cpu.SetBusStallTable(tbl)

	// Region-aware lookup distinguishes locations.
	if got := cpu.busStallFor(0x100); got != 3 {
		t.Errorf("busStallFor(0x100) = %d, want 3", got)
	}
	if got := cpu.busStallFor(0x00100100); got != 7 {
		t.Errorf("busStallFor(0x00100100) = %d, want 7", got)
	}
	if got := cpu.busStallFor(0x06001234); got != 30 {
		t.Errorf("busStallFor(0x06001234) = %d, want 30", got)
	}
	if got := cpu.busStallFor(0x26001234); got != 30 {
		t.Errorf("cache-through mirror busStallFor(0x26001234) = %d, want 30", got)
	}

	cpu.busStall = 0
	cpu.read32(0x100) // external data read in region 0 -> cost 3
	if cpu.busStall != 3 {
		t.Errorf("data read stall = %d, want 3", cpu.busStall)
	}

	cpu.busStall = 0
	cpu.write16(0x100, 0) // external data write in region 0 -> cost 3
	if cpu.busStall != 3 {
		t.Errorf("data write stall = %d, want 3", cpu.busStall)
	}

	cpu.busStall = 0
	cpu.read8(0xFFFFFE92) // on-chip register (CCR): not an external bus access
	if cpu.busStall != 0 {
		t.Errorf("on-chip read stall = %d, want 0", cpu.busStall)
	}

	cpu.busStall = 0
	cpu.reg.PC = 0
	cpu.fetchPC() // instruction fetch is not charged
	if cpu.busStall != 0 {
		t.Errorf("fetch stall = %d, want 0", cpu.busStall)
	}

	// Default (no table) charges nothing.
	plain := New(bus, true)
	plain.read32(0x100)
	if plain.busStall != 0 {
		t.Errorf("default stall = %d, want 0", plain.busStall)
	}
}

// TestBusStallDrains verifies the stall debt is drained one cycle per Clock()
// before the next instruction executes.
func TestBusStallDrains(t *testing.T) {
	const stall = 3
	bus := newTestBus(0x100)
	for a := 0; a+1 < len(bus.mem); a += 2 { // NOPs
		bus.mem[a], bus.mem[a+1] = 0x00, 0x09
	}
	cpu := New(bus, true)
	cpu.reg.PC = 0
	cpu.busStall = stall // simulate one data access's debt
	start := cpu.Cycles()

	for i := 0; i < stall; i++ {
		cpu.Clock()
		if cpu.reg.PC != 0 {
			t.Fatalf("instruction executed during stall drain at step %d", i)
		}
	}
	cpu.Clock() // debt drained: the NOP now executes
	if cpu.reg.PC != 2 {
		t.Error("instruction did not execute after stall drained")
	}
	if got := cpu.Cycles() - start; got != uint64(stall)+1 {
		t.Errorf("cycles = %d, want %d", got, stall+1)
	}
}
