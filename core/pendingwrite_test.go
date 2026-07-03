// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// A store with a free write buffer costs the master nothing beyond the
// instruction's own MA cycle: the external cycle drains in the
// background.
func TestChargeWriteBareStall(t *testing.T) {
	b := newContentionTestBus()
	if got := b.chargeWrite(areaCPUBus, uint32(wramWriteCost()), 100, false); got != 0 {
		t.Fatalf("bare master write stall = %d, want 0", got)
	}
}

// The slave pays only the arbitration handshake on a buffered store.
func TestChargeWriteSlaveArb(t *testing.T) {
	b := newContentionTestBus()
	if got := b.chargeWrite(areaCPUBus, uint32(wramWriteCost()), 100, true); got != slaveArbCycles {
		t.Fatalf("bare slave write stall = %d, want %d", got, slaveArbCycles)
	}
}

// A chip-external access issued while the buffered write is still
// draining waits out the remainder; once the drain time has passed it
// waits nothing.
func TestPendingWriteDrainWait(t *testing.T) {
	b := newContentionTestBus()
	cost := wramWriteCost()
	b.chargeWrite(areaCPUBus, uint32(cost), 100, false)
	w := b.pendingWriteOf(false)
	if got := w.wait(101); int64(got) != cost-1 {
		t.Fatalf("wait 1 cycle in = %d, want %d", got, cost-1)
	}
	if got := w.wait(100 + cost); got != 0 {
		t.Fatalf("wait at drain end = %d, want 0", got)
	}
}

// A second store issued before the first drains pays the first's full
// transaction cost, no more.
func TestChargeWriteBackToBack(t *testing.T) {
	b := newContentionTestBus()
	cost := wramWriteCost()
	b.chargeWrite(areaCPUBus, uint32(cost), 100, false)
	if got := b.chargeWrite(areaCPUBus, uint32(cost), 100, false); int64(got) != cost {
		t.Fatalf("back-to-back write stall = %d, want %d", got, cost)
	}
}

// Stores spaced at least a transaction apart never wait: the buffer has
// always drained by the time the next store arrives.
func TestPendingWriteAdvancingClockNoStall(t *testing.T) {
	b := newContentionTestBus()
	cost := wramWriteCost()
	at := int64(100)
	for i := 0; i < 8; i++ {
		if got := b.chargeWrite(areaCPUBus, uint32(cost), at, false); got != 0 {
			t.Fatalf("write %d stall = %d, want 0", i, got)
		}
		at += cost
	}
}

// The wait is clamped to one transaction: successive stores against a
// non-advancing clock each pay at most the transaction cost, so the
// total grows linearly, never compounding.
func TestPendingWriteClampNonAdvancingClock(t *testing.T) {
	b := newContentionTestBus()
	cost := wramWriteCost()
	const n = 50
	var total int64
	for i := 0; i < n; i++ {
		got := int64(b.chargeWrite(areaCPUBus, uint32(cost), 100, false))
		if got > cost {
			t.Fatalf("write %d stall = %d, exceeds one transaction (%d)", i, got, cost)
		}
		total += got
	}
	if want := (n - 1) * cost; total != want {
		t.Fatalf("total stall = %d, want %d (linear in write count)", total, want)
	}
}

// Each CPU has its own write buffer: a master store never charges the
// slave's pending-write wait.
func TestPendingWritePerCPU(t *testing.T) {
	b := newContentionTestBus()
	b.chargeWrite(areaCPUBus, uint32(wramWriteCost()), 100, false)
	if got := b.pendingWriteOf(true).wait(100); got != 0 {
		t.Fatalf("slave wait after master write = %d, want 0", got)
	}
}

// Full read path: a read right behind a store pays the store's drain on
// top of its own region cost.
func TestPendingWriteReadBehindStore(t *testing.T) {
	b := newContentionTestBus()
	wCost := wramWriteCost()
	rCost := wramReadCost()
	if got := b.SH2Write8(contendWRAM, 0xAA, 100, false); got != 0 {
		t.Fatalf("write stall = %d, want 0", got)
	}
	// The read arrives 1 cycle later: it pays the drain remainder
	// (wCost-1), which lands it exactly at the bus-window end, then its
	// own cost-1.
	_, got := b.SH2Read8(contendWRAM, 101, false)
	want := (wCost - 1) + (rCost - 1)
	if int64(got) != want {
		t.Fatalf("read-behind-store stall = %d, want %d", got, want)
	}
	// Well after the drain: only the read's own cost.
	_, got = b.SH2Read8(contendWRAM, 100+10*wCost, false)
	if int64(got) != rCost-1 {
		t.Fatalf("read after drain stall = %d, want %d", got, rCost-1)
	}
}

// A buffered store still occupies the shared CPU bus: the peer's access
// inside that window pays contention.
func TestChargeWritePeerContention(t *testing.T) {
	b := newContentionTestBus()
	cost := wramWriteCost()
	b.chargeWrite(areaCPUBus, uint32(cost), 100, false)
	const into = 2
	got := b.chargeAccess(areaCPUBus, uint32(wramReadCost()), 100+into, true)
	want := (wramReadCost() - 1) + slaveArbCycles + (cost - into)
	if int64(got) != want {
		t.Fatalf("peer access stall = %d, want %d", got, want)
	}
}

// resetContention clears both CPUs' pending writes.
func TestPendingWriteReset(t *testing.T) {
	b := newContentionTestBus()
	b.chargeWrite(areaCPUBus, uint32(wramWriteCost()), 100, false)
	b.chargeWrite(areaCPUBus, uint32(wramWriteCost()), 100, true)
	b.resetContention()
	if got := b.pendingWriteOf(false).wait(0); got != 0 {
		t.Fatalf("master wait after reset = %d, want 0", got)
	}
	if got := b.pendingWriteOf(true).wait(0); got != 0 {
		t.Fatalf("slave wait after reset = %d, want 0", got)
	}
}
