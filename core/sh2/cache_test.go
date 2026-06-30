// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// Cache model tests against SH7604 hardware manual Section 8.

// cacheFixture returns a CPU with the cache enabled and purged and
// region-0 wait-state costs installed for reads, writes, and line
// fills (each = cost per access beyond the instruction's own access
// cycle).
func cacheFixture(t *testing.T, read, write, fill uint32) (*CPU, *testBus) {
	t.Helper()
	bus := newTestBus(0x10000)
	bus.stallRead = read
	bus.stallWrite = write
	bus.stallFill = fill
	cpu := New(bus, true)
	cpu.SetCCR(ccrCE)
	return cpu, bus
}

// TestCacheMissFillsLineAndCachesData: a read miss fills the whole
// 16-byte line (Section 8.4.1) and later reads are served from the
// cache - a memory change underneath is NOT observed until the line
// is invalidated, since there is no snoop (Section 8.5.3).
func TestCacheMissFillsLineAndCachesData(t *testing.T) {
	cpu, bus := cacheFixture(t, 3, 3, 6)

	bus.Write32(0x100, 0x11223344)
	bus.Write32(0x104, 0x55667788)

	if got := cpu.Read32(0x100); got != 0x11223344 {
		t.Fatalf("miss read = %08X, want 11223344", got)
	}
	// The whole line is resident: the neighbor longword hits.
	cpu.busStall = 0
	if got := cpu.Read32(0x104); got != 0x55667788 {
		t.Errorf("line-neighbor read = %08X, want 55667788", got)
	}
	if cpu.busStall != 0 {
		t.Errorf("hit charged %d stall cycles, want 0", cpu.busStall)
	}

	// Memory changes underneath; the cached line is stale by design.
	bus.Write32(0x100, 0xDEADBEEF)
	if got := cpu.Read32(0x100); got != 0x11223344 {
		t.Errorf("read after external write = %08X, want stale 11223344", got)
	}

	// Sub-line access sizes read the same cached bytes.
	if got := cpu.Read16(0x102); got != 0x3344 {
		t.Errorf("read16 from cached line = %04X, want 3344", got)
	}
	if got := cpu.Read8(0x101); got != 0x22 {
		t.Errorf("read8 from cached line = %02X, want 22", got)
	}
}

// TestCacheMissStallCost: a fill costs the region's 16-byte burst
// cost; a hit costs nothing; reads and writes with the cache disabled
// cost their region's single-access read/write costs.
func TestCacheMissStallCost(t *testing.T) {
	const (
		readStall  = 6
		writeStall = 3
		fillStall  = 6 // e.g. SDRAM: burst pipeline == single read
	)
	cpu, _ := cacheFixture(t, readStall, writeStall, fillStall)

	cpu.busStall = 0
	cpu.Read32(0x200)
	if cpu.busStall != fillStall {
		t.Errorf("miss stall = %d, want %d", cpu.busStall, fillStall)
	}

	cpu.busStall = 0
	cpu.Read32(0x200) // hit
	if cpu.busStall != 0 {
		t.Errorf("hit stall = %d, want 0", cpu.busStall)
	}

	cpu.SetCCR(0) // cache disabled: plain external accesses
	cpu.busStall = 0
	cpu.Read32(0x200)
	if cpu.busStall != readStall {
		t.Errorf("uncached read stall = %d, want %d", cpu.busStall, readStall)
	}
	cpu.busStall = 0
	cpu.Write32(0x200, 0)
	if cpu.busStall != writeStall {
		t.Errorf("uncached write stall = %d, want %d", cpu.busStall, writeStall)
	}

	// Cache-through partition (Section 8.4.3): single access, no fill.
	cpu.SetCCR(ccrCE)
	cpu.busStall = 0
	cpu.Read32(0x20000200)
	if cpu.busStall != readStall {
		t.Errorf("cache-through read stall = %d, want %d", cpu.busStall, readStall)
	}

	// Write-through writes pay the write cost even on a cache hit
	// (Section 8.4.2: memory is always written, no write buffer).
	cpu.Read32(0x300) // fill the line
	cpu.busStall = 0
	cpu.Write32(0x300, 1) // write hit
	if cpu.busStall != writeStall {
		t.Errorf("write-through hit stall = %d, want %d", cpu.busStall, writeStall)
	}
}

// TestCacheWriteThrough: writes always reach memory; the data array is
// updated only on a tag hit, and a write miss does not allocate a line
// (Section 8.4.2).
func TestCacheWriteThrough(t *testing.T) {
	cpu, bus := cacheFixture(t, 0, 0, 0)

	// Write miss: memory updated, nothing cached.
	cpu.Write32(0x300, 0xAABBCCDD)
	if got := bus.Read32(0x300); got != 0xAABBCCDD {
		t.Fatalf("memory after write miss = %08X, want AABBCCDD", got)
	}
	if _, hit := cpu.cacheLookup(0x300); hit {
		t.Error("write miss allocated a line")
	}

	// Fill the line, then a write hit updates both cache and memory.
	cpu.Read32(0x300)
	cpu.Write32(0x300, 0x01020304)
	if got := bus.Read32(0x300); got != 0x01020304 {
		t.Errorf("memory after write hit = %08X, want 01020304", got)
	}
	if got := cpu.Read32(0x300); got != 0x01020304 {
		t.Errorf("cache after write hit = %08X, want 01020304", got)
	}

	// Byte and word write hits update the cached line in place.
	cpu.Write8(0x301, 0xEE)
	if got := cpu.Read32(0x300); got != 0x01EE0304 {
		t.Errorf("cache after write8 hit = %08X, want 01EE0304", got)
	}
	cpu.Write16(0x302, 0xBEEF)
	if got := cpu.Read32(0x300); got != 0x01EEBEEF {
		t.Errorf("cache after write16 hit = %08X, want 01EEBEEF", got)
	}
}

// TestCacheLRUReplacement: after a purge the all-zero LRU fills ways in
// the order 3 -> 2 -> 1 -> 0 (Section 8.4.5); once all ways are valid,
// the least recently used way is replaced, and a hit refreshes a way's
// recency.
func TestCacheLRUReplacement(t *testing.T) {
	cpu, _ := cacheFixture(t, 0, 0, 0)

	// Five addresses sharing entry 1 (A9-A4 = 1), different tags.
	addrs := [5]uint32{0x0010, 0x0410, 0x0810, 0x0C10, 0x1010}
	wantWays := [4]int{3, 2, 1, 0}
	for i, a := range addrs[:4] {
		cpu.Read32(a)
		w := wantWays[i]
		if !cpu.cacheValid[w][1] || cpu.cacheTag[w][1] != cacheTagOf(a) {
			t.Fatalf("fill %d: tag %08X not in way %d", i, cacheTagOf(a), w)
		}
	}

	// All ways valid; way 3 is the oldest. Touch it so way 2 becomes
	// the replacement target, then miss: the new tag lands in way 2.
	cpu.Read32(addrs[0])
	cpu.Read32(addrs[4])
	if cpu.cacheTag[2][1] != cacheTagOf(addrs[4]) {
		t.Errorf("5th tag in way %v, want way 2 (LRU after touching way 3)",
			func() int {
				w, _ := cpu.cacheLookup(addrs[4])
				return w
			}())
	}
	// Way 3 survived the replacement.
	if _, hit := cpu.cacheLookup(addrs[0]); !hit {
		t.Error("recently touched way 3 line was replaced")
	}
}

// TestCachePurgeViaCCR: writing CP=1 clears all valid bits and LRU
// information; CP self-clears and always reads 0 (Section 8.4.6).
func TestCachePurgeViaCCR(t *testing.T) {
	cpu, _ := cacheFixture(t, 0, 0, 0)

	cpu.Read32(0x100)
	cpu.Write8(0xFFFFFE92, ccrCP|ccrCE)
	if got := cpu.Read8(0xFFFFFE92); got != ccrCE {
		t.Errorf("CCR after purge = %02X, want %02X (CP self-clears)", got, ccrCE)
	}
	if _, hit := cpu.cacheLookup(0x100); hit {
		t.Error("line survived CP purge")
	}
	for e, lru := range cpu.cacheLRU {
		if lru != 0 {
			t.Fatalf("LRU[%d] = %02X after purge, want 0", e, lru)
		}
	}
}

// TestAssociativePurge: a write to the purge partition invalidates the
// line whose tag matches the written address; a non-matching address
// purges nothing (Section 8.4.7).
func TestAssociativePurge(t *testing.T) {
	cpu, _ := cacheFixture(t, 0, 0, 0)

	cpu.Read32(0x100)
	cpu.Write32(0x40000000|0x500, 0) // different tag/entry: no effect
	if _, hit := cpu.cacheLookup(0x100); !hit {
		t.Fatal("non-matching associative purge invalidated the line")
	}
	cpu.Write32(0x40000000|0x100, 0)
	if _, hit := cpu.cacheLookup(0x100); hit {
		t.Error("matching associative purge left the line valid")
	}
}

// TestAddressArrayAccess: address-array reads return tag/LRU/valid for
// the way selected by CCR W1-W0; writes set the tag and valid bit from
// the address and the LRU from the data (Section 8.4.9, Figure 8.11).
func TestAddressArrayAccess(t *testing.T) {
	cpu, _ := cacheFixture(t, 0, 0, 0)

	// Write way 1 (W1:W0 = 01), entry 2, tag 0x00000C00, valid.
	cpu.SetCCR(0x40 | ccrCE)
	addr := uint32(0x60000000) | 0x00000C00 | 2<<4 | 1<<2
	cpu.Write32(addr, 5<<4) // LRU = 5
	if !cpu.cacheValid[1][2] || cpu.cacheTag[1][2] != 0x00000C00 {
		t.Fatalf("address-array write: way1 entry2 = valid %v tag %08X",
			cpu.cacheValid[1][2], cpu.cacheTag[1][2])
	}
	if cpu.cacheLRU[2] != 5 {
		t.Errorf("LRU after address-array write = %d, want 5", cpu.cacheLRU[2])
	}

	want := uint32(0x00000C00) | 5<<4 | 1<<2
	if got := cpu.Read32(0x60000000 | 2<<4); got != want {
		t.Errorf("address-array read = %08X, want %08X", got, want)
	}

	// Clearing the valid bit through the address array invalidates.
	cpu.Write32(addr&^(1<<2), 0)
	if cpu.cacheValid[1][2] {
		t.Error("address-array write with V=0 left the entry valid")
	}
}

// TestCacheReplacementDisable: OD=1 means a data-read miss is served
// from memory with no replacement; ID=1 the same for instruction
// fetches. Hits still work (Section 8.2, Section 8.4.5).
func TestCacheReplacementDisable(t *testing.T) {
	cpu, bus := cacheFixture(t, 0, 0, 0)

	cpu.SetCCR(ccrOD | ccrCE)
	bus.Write32(0x100, 0x12345678)
	if got := cpu.Read32(0x100); got != 0x12345678 {
		t.Fatalf("OD miss read = %08X, want 12345678", got)
	}
	if _, hit := cpu.cacheLookup(0x100); hit {
		t.Error("OD=1 data miss filled a line")
	}

	cpu.SetCCR(ccrID | ccrCE)
	bus.Write16(0x200, 0x0009)
	if got := cpu.fetchInstr(0x200); got != 0x0009 {
		t.Fatalf("ID fetch = %04X, want 0009", got)
	}
	if _, hit := cpu.cacheLookup(0x200); hit {
		t.Error("ID=1 fetch miss filled a line")
	}

	cpu.SetCCR(ccrCE)
	cpu.fetchInstr(0x200)
	if _, hit := cpu.cacheLookup(0x200); !hit {
		t.Error("fetch miss with ID=0 did not fill")
	}
}

// TestCacheTwoWayMode: with TW=1 only ways 2 and 3 are replaced
// (Section 8.2, Section 8.4.5); ways 0 and 1 are left for use as RAM
// through the data array.
func TestCacheTwoWayMode(t *testing.T) {
	cpu, _ := cacheFixture(t, 0, 0, 0)
	cpu.SetCCR(ccrTW | ccrCE)

	addrs := [3]uint32{0x0010, 0x0410, 0x0810}
	for _, a := range addrs {
		cpu.Read32(a)
	}
	if cpu.cacheValid[0][1] || cpu.cacheValid[1][1] {
		t.Error("two-way mode replaced way 0 or 1")
	}
	// Three fills into two ways: all landed in ways 2/3.
	for _, a := range addrs[1:] {
		if w, hit := cpu.cacheLookup(a); !hit || w < 2 {
			t.Errorf("tag %08X: way %d hit %v, want hit in way 2 or 3",
				cacheTagOf(a), w, hit)
		}
	}
}

// TestCacheTASInteraction: TAS reads are cache-through even in the
// cache area (no tag compare, no fill); the TAS write updates the data
// array on a hit (Section 8.4.4).
func TestCacheTASInteraction(t *testing.T) {
	cpu, bus := cacheFixture(t, 0, 0, 0)

	bus.Write8(0x180, 0x00)
	cpu.tasRead8(0x180)
	if _, hit := cpu.cacheLookup(0x180); hit {
		t.Error("TAS read filled a line")
	}

	cpu.Read32(0x180) // fill
	cpu.tasWrite8(0x180, 0x80)
	if got := bus.Read8(0x180); got != 0x80 {
		t.Errorf("memory after TAS write = %02X, want 80", got)
	}
	if got := cpu.Read8(0x180); got != 0x80 {
		t.Errorf("cache after TAS write hit = %02X, want 80", got)
	}
}

// TestCacheDataArrayRAM: the data array region reads and writes the
// 4 KB cache storage directly (Section 8.4.8); with the cache disabled
// it is plain on-chip RAM, and code can execute from it.
func TestCacheDataArrayRAM(t *testing.T) {
	bus := newTestBus(0x100)
	cpu := New(bus, true)

	cpu.Write32(0xC0000000, 0xCAFEBABE)
	if got := cpu.Read32(0xC0000000); got != 0xCAFEBABE {
		t.Errorf("data array read32 = %08X, want CAFEBABE", got)
	}
	cpu.Write8(0xC0000FFF, 0x5A)
	if got := cpu.Read8(0xC0000FFF); got != 0x5A {
		t.Errorf("data array read8 = %02X, want 5A", got)
	}

	// Fetch from the data array (code in cache RAM).
	cpu.Write16(0xC0000800, 0x0009) // NOP
	if got := cpu.fetchInstr(0xC0000800); got != 0x0009 {
		t.Errorf("data array fetch = %04X, want 0009", got)
	}

	// Cached lines and the data array share storage: way 0's line for
	// entry 0 occupies bytes 0-15 of the array.
	cpu.SetCCR(ccrCE)
	cpu.cacheFill(0, 0x0000)
	if got, want := cpu.Read32(0xC0000000), bus.Read32(0x0000); got != want {
		t.Errorf("data array view of way0 line = %08X, want %08X", got, want)
	}
}

// TestBusStallDrains: the stall debt drains one cycle per Clock()
// before the next instruction executes.
func TestBusStallDrains(t *testing.T) {
	const stall = 3
	bus := newTestBus(0x100)
	for a := 0; a+1 < len(bus.mem); a += 2 { // NOPs
		bus.mem[a], bus.mem[a+1] = 0x00, 0x09
	}
	cpu := New(bus, true)
	cpu.reg.PC = 0
	cpu.busStall = stall // simulate one access's debt
	start := cpu.Cycles()

	for i := 0; i < stall; i++ {
		cpu.Clock()
		if cpu.reg.PC != 0 {
			t.Fatalf("instruction executed during stall drain at step %d", i)
		}
	}
	cpu.Clock() // debt drained: the NOP executes
	if cpu.reg.PC != 2 {
		t.Error("instruction did not execute after stall drained")
	}
	if got := cpu.Cycles() - start; got != uint64(stall)+1 {
		t.Errorf("cycles = %d, want %d", got, stall+1)
	}
}

// TestFetchLineMemoInvalidation: the fetch-line memo serves sequential
// fetches from the resolved line, observes write hits (it holds a
// location, not data), and never survives eviction of its line or a
// purge.
func TestFetchLineMemoInvalidation(t *testing.T) {
	cpu, bus := cacheFixture(t, 0, 0, 0)

	bus.Write16(0x0010, 0x1111)
	bus.Write16(0x0012, 0x2222)
	if got := cpu.fetchInstr(0x0010); got != 0x1111 {
		t.Fatalf("fetch = %04X, want 1111", got)
	}
	if cpu.fetchLineAddr != 0x0010&^0xF {
		t.Fatal("memo not armed after fetch")
	}
	// Sequential fetch within the line is served via the memo.
	if got := cpu.fetchInstr(0x0012); got != 0x2222 {
		t.Errorf("memoized fetch = %04X, want 2222", got)
	}

	// A write hit updates the line in place; the memoized fetch sees it.
	cpu.Write16(0x0012, 0x3333)
	if got := cpu.fetchInstr(0x0012); got != 0x3333 {
		t.Errorf("memoized fetch after write hit = %04X, want 3333", got)
	}

	// Evict the memoized line: four conflicting fills at the same
	// entry replace all ways. The next fetch must re-resolve, not
	// serve the stale memo.
	for _, a := range []uint32{0x0410, 0x0810, 0x0C10, 0x1010} {
		cpu.Read32(a)
	}
	bus.Write16(0x0012, 0x4444)
	if got := cpu.fetchInstr(0x0012); got != 0x4444 {
		t.Errorf("fetch after eviction = %04X, want 4444 (stale memo served)", got)
	}

	// Associative purge of the line drops the memo.
	cpu.fetchInstr(0x0010)
	cpu.Write32(0x40000000|0x0010, 0)
	if cpu.fetchLineAddr != fetchLineInvalid {
		t.Error("memo survived associative purge")
	}

	// CP purge drops the memo.
	cpu.fetchInstr(0x0010)
	cpu.Write8(0xFFFFFE92, ccrCP|ccrCE)
	if cpu.fetchLineAddr != fetchLineInvalid {
		t.Error("memo survived CP purge")
	}
}

// TestCacheStallExemptions: on-chip registers and data-array accesses
// are internal and never charged external wait states.
func TestCacheStallExemptions(t *testing.T) {
	cpu, _ := cacheFixture(t, 7, 7, 7)

	cpu.busStall = 0
	cpu.Read8(0xFFFFFE92) // CCR: on-chip
	if cpu.busStall != 0 {
		t.Errorf("on-chip read stall = %d, want 0", cpu.busStall)
	}

	cpu.busStall = 0
	cpu.Write32(0xC0000000, 1) // data array: internal
	cpu.Read32(0xC0000000)
	if cpu.busStall != 0 {
		t.Errorf("data array stall = %d, want 0", cpu.busStall)
	}

	// Write-through always pays the external cost (Section 8.4.2).
	cpu.busStall = 0
	cpu.Write32(0x400, 1)
	if cpu.busStall != 7 {
		t.Errorf("write-through stall = %d, want 7", cpu.busStall)
	}
}
