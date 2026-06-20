// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// newContentionTestBus builds a bus with no subsystems. The timing model and
// the Work RAM-H access path touch only b.wramH and the cost tables, so nil
// subsystems are fine for these tests.
func newContentionTestBus() *Bus {
	return NewBus(nil, nil, nil, nil, nil, nil)
}

const contendWRAM = 0x06000000 // Work RAM-H, areaCPUBus

func wramReadCost() int64  { return int64(accessReadSingle[(uint32(contendWRAM)>>20)&0x7F]) }
func wramWriteCost() int64 { return int64(accessWriteSingle[(uint32(contendWRAM)>>20)&0x7F]) }
func wramFillCost() int64  { return int64(accessReadBurst[(uint32(contendWRAM)>>20)&0x7F]) }

// chargeAccess returns region-cost-1 + slave-penalty + contention-wait. A
// master access with no in-flight access on the area pays only cost-1.
func TestChargeAccessBareCost(t *testing.T) {
	b := newContentionTestBus()
	const cost = 7
	if got := b.chargeAccess(areaCPUBus, cost, 100, false); got != cost-1 {
		t.Fatalf("bare master access = %d, want %d (cost-1)", got, cost-1)
	}
}

// A single CPU's consecutive accesses must not contend with themselves: it
// pays its access cost, advancing its clock past its own busyUntil.
func TestContentionSelfConsecutive(t *testing.T) {
	b := newContentionTestBus()
	const cost = 7
	if got := b.chargeAccess(areaCPUBus, cost, 100, false); got != cost-1 {
		t.Fatalf("first access = %d, want %d", got, cost-1)
	}
	// busyUntil is now 100+cost; the CPU's clock advances by cost before the
	// next access, so wait is 0 again.
	if got := b.chargeAccess(areaCPUBus, cost, 100+cost, false); got != cost-1 {
		t.Fatalf("self-consecutive access = %d, want %d", got, cost-1)
	}
}

// The trailing CPU waits the remaining cycles of the leader's in-flight access,
// then pays its own cost.
func TestContentionInterleavedStagger(t *testing.T) {
	b := newContentionTestBus()
	const cost = 7
	const into = 2 // trailing CPU arrives 2 cycles into the leader's access
	b.chargeAccess(areaCPUBus, cost, 100, false)
	got := b.chargeAccess(areaCPUBus, cost, 100+into, false)
	want := (cost - 1) + (cost - into) // own cost + remaining of the leader
	if int(got) != want {
		t.Fatalf("staggered access = %d, want %d", got, want)
	}
}

// A clock skew larger than one transaction is bounded to the access cost.
func TestContentionSkewClamp(t *testing.T) {
	b := newContentionTestBus()
	const cost = 7
	b.chargeAccess(areaCPUBus, cost, 1000, false)
	got := b.chargeAccess(areaCPUBus, cost, 1000-50, false) // far behind: skew
	want := (cost - 1) + cost                               // wait clamped to one transaction
	if int(got) != want {
		t.Fatalf("skew-clamped access = %d, want %d", got, want)
	}
}

// The slave pays the arbitration handshake on every external access, on any
// area, even with no contention.
func TestSlaveArbitrationPenalty(t *testing.T) {
	b := newContentionTestBus()
	const cost = 7
	if got := b.chargeAccess(areaCPUBus, cost, 100, true); got != cost-1+slaveArbCycles {
		t.Fatalf("slave CPU-Bus access = %d, want %d", got, cost-1+slaveArbCycles)
	}
	if got := b.chargeAccess(areaBBus, cost, 100, true); got != cost-1+slaveArbCycles {
		t.Fatalf("slave B-Bus access = %d, want %d (penalty, no contention)", got, cost-1+slaveArbCycles)
	}
}

// Contention is scoped to the CPU-Bus; other areas charge only the cost.
func TestContentionScopedToCPUBus(t *testing.T) {
	b := newContentionTestBus()
	const cost = 8
	b.busyUntil[areaBBus] = 1000 // pretend the B-Bus is busy
	if got := b.chargeAccess(areaBBus, cost, 0, false); got != cost-1 {
		t.Fatalf("non-CPUBus access = %d, want %d (no contention)", got, cost-1)
	}
}

// TAS holds the bus across the read-modify-write; busyUntil is extended to
// cover the write half so the peer waits for the whole RMW.
func TestContentionRMWExtend(t *testing.T) {
	b := newContentionTestBus()
	rcost := wramReadCost()
	wcost := wramWriteCost()
	b.SH2RMWRead(contendWRAM, 200, false) // busyUntil = 200 + rcost, lock held
	b.SH2RMWWrite(contendWRAM, 0xAA, 203) // CPU advanced mid-RMW

	end := int64(200) + rcost
	if 203 > end {
		end = 203
	}
	if got := b.busyUntil[areaCPUBus]; got != end+wcost {
		t.Fatalf("RMW busyUntil = %d, want %d", got, end+wcost)
	}
}

// A 16-byte line fill charges fill contention once for the whole burst.
func TestContentionCacheLineFill(t *testing.T) {
	b := newContentionTestBus()
	fcost := wramFillCost()
	var line [16]byte
	if got := b.SH2FillLine(contendWRAM, &line, 300, false); int64(got) != fcost-1 {
		t.Fatalf("first fill stall = %d, want %d", got, fcost-1)
	}
	if got := b.busyUntil[areaCPUBus]; got != 300+fcost {
		t.Fatalf("fill busyUntil = %d, want %d", got, 300+fcost)
	}
}
