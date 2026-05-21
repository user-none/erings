// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import (
	"math"
	"testing"
)

// tickWDT advances a standalone WDT by n CPU cycles relative to its
// current lastSync anchor. Production code path is
// CPU.tickPeripherals -> fireDue via deadline compare; tests drive
// syncTo directly for step-by-step exercise.
func tickWDT(w *WDT, n uint32) bool {
	return w.syncTo(w.lastSync + uint64(n))
}

func TestWDTRegisterInit(t *testing.T) {
	var w WDT
	w.Reset()
	if w.wtcsr != 0x18 {
		t.Errorf("WTCSR = 0x%02X, want 0x18", w.wtcsr)
	}
	if w.wtcnt != 0x00 {
		t.Errorf("WTCNT = 0x%02X, want 0x00", w.wtcnt)
	}
	if w.rstcsr != 0x1F {
		t.Errorf("RSTCSR = 0x%02X, want 0x1F", w.rstcsr)
	}
}

// TestWDTReadAddresses verifies byte-read addresses per manual
// Sec 12.1.4 Table 12.2: WTCSR at 80, WTCNT at 81, 82 undefined,
// RSTCSR at 83.
func TestWDTReadAddresses(t *testing.T) {
	var w WDT
	w.Reset()
	w.wtcsr = 0xAA
	w.wtcnt = 0xBB
	w.rstcsr = 0xCC

	if v := w.Read(0xFFFFFE80); v != 0xAA {
		t.Errorf("Read(0xFFFFFE80) WTCSR = 0x%02X, want 0xAA", v)
	}
	if v := w.Read(0xFFFFFE81); v != 0xBB {
		t.Errorf("Read(0xFFFFFE81) WTCNT = 0x%02X, want 0xBB", v)
	}
	if v := w.Read(0xFFFFFE82); v != 0 {
		t.Errorf("Read(0xFFFFFE82) = 0x%02X, want 0 (undefined)", v)
	}
	if v := w.Read(0xFFFFFE83); v != 0xCC {
		t.Errorf("Read(0xFFFFFE83) RSTCSR = 0x%02X, want 0xCC", v)
	}
}

// TestWDTWriteGateWTCSR covers the A5xx key path. Only high byte
// 0xA5 updates WTCSR; other high bytes discard.
func TestWDTWriteGateWTCSR(t *testing.T) {
	var w WDT
	w.Reset()

	// Valid: A5 key, low byte 0x00. Reserved bits 4-3 forced to 1.
	w.WriteWord(0xFFFFFE80, 0xA500)
	if w.wtcsr != 0x18 {
		t.Errorf("A5/00 -> WTCSR = 0x%02X, want 0x18 (reserved bits forced)", w.wtcsr)
	}

	// Valid: A5 key, low byte sets TME=1, WT/IT=1, CKS=5.
	w.WriteWord(0xFFFFFE80, 0xA565) // 0110 0101 -> WT/IT=1, TME=1, CKS=5
	if w.wtcsr&0xE7 != 0x65 {
		t.Errorf("A5/65 -> WTCSR & 0xE7 = 0x%02X, want 0x65", w.wtcsr&0xE7)
	}
	if w.wtcsr&0x18 != 0x18 {
		t.Errorf("A5/65 -> reserved bits 4-3 = 0x%02X, want 0x18", w.wtcsr&0x18)
	}

	// Invalid key: discard.
	old := w.wtcsr
	w.WriteWord(0xFFFFFE80, 0x1234)
	if w.wtcsr != old {
		t.Errorf("invalid key 0x1234 modified WTCSR: got 0x%02X, want 0x%02X", w.wtcsr, old)
	}
}

// TestWDTWriteGateWTCNT covers the 5Axx key path.
func TestWDTWriteGateWTCNT(t *testing.T) {
	var w WDT
	w.Reset()

	w.WriteWord(0xFFFFFE80, 0x5ABC)
	if w.wtcnt != 0xBC {
		t.Errorf("5A/BC -> WTCNT = 0x%02X, want 0xBC", w.wtcnt)
	}

	// Invalid key.
	w.WriteWord(0xFFFFFE80, 0x00FF)
	if w.wtcnt != 0xBC {
		t.Errorf("invalid key modified WTCNT: got 0x%02X, want 0xBC", w.wtcnt)
	}
}

// TestWDTWriteGateRSTCSR covers RSTCSR's two word patterns.
func TestWDTWriteGateRSTCSR(t *testing.T) {
	var w WDT
	w.Reset()

	// Set WOVF manually, then clear via 0xA500.
	w.rstcsr |= rstcsrWOVF
	w.WriteWord(0xFFFFFE82, 0xA500)
	if w.rstcsr&rstcsrWOVF != 0 {
		t.Errorf("0xA500 failed to clear WOVF: RSTCSR = 0x%02X", w.rstcsr)
	}

	// Write RSTE+RSTS via 0x5A60.
	w.WriteWord(0xFFFFFE82, 0x5A60)
	if w.rstcsr&(rstcsrRSTE|rstcsrRSTS) != (rstcsrRSTE | rstcsrRSTS) {
		t.Errorf("0x5A60 -> RSTCSR = 0x%02X, missing RSTE/RSTS", w.rstcsr)
	}
	if w.rstcsr&0x1F != 0x1F {
		t.Errorf("RSTCSR reserved bits 4-0 = 0x%02X, want 0x1F", w.rstcsr&0x1F)
	}

	// Invalid word: discard.
	old := w.rstcsr
	w.WriteWord(0xFFFFFE82, 0x1234)
	if w.rstcsr != old {
		t.Errorf("invalid word 0x1234 modified RSTCSR: got 0x%02X, want 0x%02X", w.rstcsr, old)
	}
}

// TestWDTTickCKSDividers verifies the prescaler shift table:
// for CKS=k, (1 << shift[k]) cycles drive one WTCNT increment.
func TestWDTTickCKSDividers(t *testing.T) {
	for cks := 0; cks < 8; cks++ {
		var w WDT
		w.Reset()
		// Enable TME, interval mode, CKS=cks.
		w.WriteWord(0xFFFFFE80, 0xA500|uint16(wtcsrTME)|uint16(cks))
		step := uint32(1) << wdtPrescalerShift[cks]
		tickWDT(&w, step)
		if w.wtcnt != 1 {
			t.Errorf("CKS=%d: WTCNT after %d cycles = %d, want 1", cks, step, w.wtcnt)
		}
		// One less cycle: no increment.
		var w2 WDT
		w2.Reset()
		w2.WriteWord(0xFFFFFE80, 0xA500|uint16(wtcsrTME)|uint16(cks))
		tickWDT(&w2, step-1)
		if w2.wtcnt != 0 {
			t.Errorf("CKS=%d: WTCNT after %d cycles = %d, want 0", cks, step-1, w2.wtcnt)
		}
	}
}

// TestWDTTickDisabled: TME=0 -> no increment.
func TestWDTTickDisabled(t *testing.T) {
	var w WDT
	w.Reset() // TME=0 by default
	tickWDT(&w, 100000)
	if w.wtcnt != 0 {
		t.Errorf("WTCNT incremented with TME=0: got %d", w.wtcnt)
	}
}

// TestWDTIntervalOverflow exercises end-to-end: overflow in
// interval mode latches OVF, raises IRQ, resolveSource returns
// the configured (level, vector).
func TestWDTIntervalOverflow(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Configure: TME=1, WT/IT=0 (interval), CKS=0 (shift=1 => 2 cycles/tick).
	cpu.writeOnChip(0xFFFFFE80, 0xA520) // A5 key, TME=1, WT/IT=0, CKS=0
	// Pre-load WTCNT to 0xFF so next tick wraps.
	cpu.writeOnChip(0xFFFFFE80, 0x5AFF) // 5A key, WTCNT=0xFF

	// IPRA bits 7-4 = WDT priority. Set level 7.
	cpu.intc.ipra = 0x0070
	// VCRWDT bits 14-8 = ITI vector. Set 0x42.
	cpu.intc.vcrwdt = 0x4200

	if cpu.wdt.IRQAsserted() {
		t.Fatal("WDT should not assert before overflow")
	}

	// Tick two cycles. With CKS=0 (phi/2) the 2nd cycle advances
	// WTCNT from 0xFF to 0x00, hitting the deadline and firing ITI.
	cpu.cycles++
	cpu.tickPeripherals()
	cpu.cycles++
	cpu.tickPeripherals()

	// Read WTCNT via the bus path so the WDT sync surfaces its live
	// value (field access alone would see the stale pre-sync state).
	wtcnt, _ := cpu.readOnChip(0xFFFFFE81)
	if wtcnt != 0 {
		t.Errorf("WTCNT = 0x%02X after overflow, want 0x00", wtcnt)
	}
	if cpu.wdt.wtcsr&wtcsrOVF == 0 {
		t.Error("WTCSR.OVF not latched after overflow")
	}
	if !cpu.wdt.IRQAsserted() {
		t.Error("WDT ITI should be asserted")
	}
	if cpu.intc.pending&(1<<isrcWDT) == 0 {
		t.Error("INTC pending bit for WDT not set")
	}

	lvl, vec, asserted := cpu.resolveSource(isrcWDT)
	if !asserted {
		t.Fatal("resolveSource(isrcWDT) returned not asserted")
	}
	if lvl != 7 {
		t.Errorf("level = %d, want 7", lvl)
	}
	if vec != 0x42 {
		t.Errorf("vec = 0x%02X, want 0x42", vec)
	}
}

// TestWDTWatchdogOverflowNoReset verifies watchdog-mode overflow
// sets RSTCSR.WOVF but does NOT deliver ITI or reset the CPU.
func TestWDTWatchdogOverflowNoReset(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Configure: TME=1, WT/IT=1 (watchdog), CKS=0.
	cpu.writeOnChip(0xFFFFFE80, 0xA560) // A5 key, WT/IT=1, TME=1, CKS=0
	cpu.writeOnChip(0xFFFFFE80, 0x5AFF) // WTCNT=0xFF
	cpu.intc.ipra = 0x0070              // valid priority

	cpu.cycles++
	cpu.tickPeripherals()
	cpu.cycles++
	cpu.tickPeripherals()

	if cpu.wdt.rstcsr&rstcsrWOVF == 0 {
		t.Error("RSTCSR.WOVF not set after watchdog-mode overflow")
	}
	if cpu.wdt.IRQAsserted() {
		t.Error("watchdog-mode should NOT assert ITI")
	}
	if cpu.wdt.wtcsr&wtcsrOVF != 0 {
		t.Error("WTCSR.OVF should not be set in watchdog mode")
	}
}

// TestWDTClearOVFReconciles confirms the INTC pending bit is
// reconciled on the next processInterrupt after software clears OVF.
func TestWDTClearOVFReconciles(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Trigger interval overflow.
	cpu.writeOnChip(0xFFFFFE80, 0xA520)
	cpu.writeOnChip(0xFFFFFE80, 0x5AFF)
	cpu.intc.ipra = 0x0070
	cpu.intc.vcrwdt = 0x4200
	cpu.cycles++
	cpu.tickPeripherals()
	cpu.cycles++
	cpu.tickPeripherals()
	if cpu.intc.pending&(1<<isrcWDT) == 0 {
		t.Fatal("WDT bit should be set after overflow")
	}

	// Software clears OVF via A5-keyed write: preserve TME and
	// mode, force OVF=0.
	cpu.writeOnChip(0xFFFFFE80, 0xA520)
	if cpu.wdt.wtcsr&wtcsrOVF != 0 {
		t.Fatal("OVF should be cleared after A5 write with OVF=0")
	}

	// Block at IMASK=15 to observe reconcile without accept.
	cpu.reg.SetIMASK(15)
	cpu.processInterrupt()
	if cpu.intc.pending&(1<<isrcWDT) != 0 {
		t.Error("WDT bit should be reconciled to 0 after OVF cleared")
	}
}

// TestWDTByteWriteDiscarded: byte writes silently discard.
func TestWDTByteWriteDiscarded(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Seed WTCNT via proper word write.
	cpu.writeOnChip(0xFFFFFE80, 0x5A77)
	if cpu.wdt.wtcnt != 0x77 {
		t.Fatalf("setup failed: WTCNT = 0x%02X", cpu.wdt.wtcnt)
	}

	// Byte write to WTCNT address.
	cpu.write8(0xFFFFFE81, 0xFF)
	if cpu.wdt.wtcnt != 0x77 {
		t.Errorf("byte write to WTCNT modified state: got 0x%02X, want 0x77", cpu.wdt.wtcnt)
	}
}

// Manual Sec 12.2.2: WTCSR bits 4-3 are reserved and always read 1. Any
// valid A5-keyed write must preserve these bits as 1.
func TestWDTWTCSRReservedBitsReadOne(t *testing.T) {
	var w WDT
	w.Reset()

	// Clear the reserved bits via a low-byte value that has 0 in 4-3.
	w.WriteWord(0xFFFFFE80, 0xA500)
	if w.wtcsr&0x18 != 0x18 {
		t.Errorf("bits 4-3 after A5/00 = 0x%02X, want 0x18", w.wtcsr&0x18)
	}
	// Try to clear explicitly with low byte 0xE7 (all non-reserved bits 1).
	w.WriteWord(0xFFFFFE80, 0xA5E7)
	if w.wtcsr&0x18 != 0x18 {
		t.Errorf("bits 4-3 after A5/E7 = 0x%02X, want 0x18", w.wtcsr&0x18)
	}
}

// Manual Sec 12.2.3: RSTCSR bits 4-0 are reserved and always read 1. Any
// valid write must preserve those bits as 1.
func TestWDTRSTCSRReservedBitsReadOne(t *testing.T) {
	var w WDT
	w.Reset()

	// Update RSTE/RSTS via 5A key with low byte 0x00; reserved bits stay 1.
	w.WriteWord(0xFFFFFE82, 0x5A00)
	if w.rstcsr&0x1F != 0x1F {
		t.Errorf("bits 4-0 after 5A/00 = 0x%02X, want 0x1F", w.rstcsr&0x1F)
	}
	// Same after A5/00 (WOVF clear).
	w.WriteWord(0xFFFFFE82, 0xA500)
	if w.rstcsr&0x1F != 0x1F {
		t.Errorf("bits 4-0 after A5/00 = 0x%02X, want 0x1F", w.rstcsr&0x1F)
	}
}

// Manual Sec 12.2.2 bit 7 (OVF): "Only 0 can be written... to clear the
// flag." Writing OVF=1 must not set the latch; only hardware overflow
// sets it.
func TestWDTWTCSRWriteOVFOneNoEffect(t *testing.T) {
	var w WDT
	w.Reset()

	if w.wtcsr&wtcsrOVF != 0 {
		t.Fatalf("setup: OVF should start 0")
	}
	w.WriteWord(0xFFFFFE80, 0xA580) // low byte has OVF=1
	if w.wtcsr&wtcsrOVF != 0 {
		t.Errorf("OVF set by software write: WTCSR = 0x%02X", w.wtcsr)
	}
}

// Manual Sec 12.2.4: RSTCSR write with A5 high byte and 0x00 low byte
// clears WOVF without touching RSTE or RSTS.
func TestWDTRSTCSRClearWOVFPreservesRSTE(t *testing.T) {
	var w WDT
	w.Reset()

	// Seed: set RSTE+RSTS via 5A, set WOVF manually.
	w.WriteWord(0xFFFFFE82, 0x5A60)
	w.rstcsr |= rstcsrWOVF
	// Sanity.
	if w.rstcsr&(rstcsrRSTE|rstcsrRSTS) != (rstcsrRSTE | rstcsrRSTS) {
		t.Fatalf("setup: RSTE/RSTS not set: 0x%02X", w.rstcsr)
	}

	w.WriteWord(0xFFFFFE82, 0xA500)
	if w.rstcsr&rstcsrWOVF != 0 {
		t.Error("WOVF not cleared by A5/00")
	}
	if w.rstcsr&(rstcsrRSTE|rstcsrRSTS) != (rstcsrRSTE | rstcsrRSTS) {
		t.Errorf("A5/00 modified RSTE/RSTS: RSTCSR = 0x%02X", w.rstcsr)
	}
}

// Manual Sec 12.2.4: RSTCSR write with 5A high byte updates RSTE/RSTS
// without clearing WOVF.
func TestWDTRSTCSRUpdate5APreservesWOVF(t *testing.T) {
	var w WDT
	w.Reset()
	w.rstcsr |= rstcsrWOVF

	w.WriteWord(0xFFFFFE82, 0x5A60) // set RSTE+RSTS
	if w.rstcsr&rstcsrWOVF == 0 {
		t.Error("5A/60 cleared WOVF; should preserve")
	}
	if w.rstcsr&(rstcsrRSTE|rstcsrRSTS) != (rstcsrRSTE | rstcsrRSTS) {
		t.Errorf("RSTE/RSTS not updated: RSTCSR = 0x%02X", w.rstcsr)
	}
}

// Manual Sec 12.2.4 Table 12.2 note: WTCSR / WTCNT / RSTCSR cannot be
// written by byte access (must use word). Verify a byte write to WTCSR's
// write address is discarded via the CPU on-chip dispatch.
func TestWDTByteWriteToWTCSRDiscarded(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Seed WTCSR via valid word write.
	cpu.writeOnChip(0xFFFFFE80, 0xA525) // TME=1, CKS=5
	seeded := cpu.wdt.wtcsr
	if seeded&wtcsrTME == 0 {
		t.Fatalf("setup: TME not set, WTCSR=0x%02X", seeded)
	}

	// Byte write to write-address must not alter WTCSR.
	cpu.write8(0xFFFFFE80, 0x00)
	if cpu.wdt.wtcsr != seeded {
		t.Errorf("byte write to 0xFFFFFE80 changed WTCSR: got 0x%02X, want 0x%02X",
			cpu.wdt.wtcsr, seeded)
	}
}

// Manual Sec 12.2.2 bit 5 (TME) description: "Timer disabled: WTCNT is
// initialized to H'00 and count-up stops." Writing WTCSR via the A5 key
// with TME=0 must reset WTCNT to 0.
func TestWDTTMEZeroResetsCounter(t *testing.T) {
	var w WDT
	w.Reset()

	// Enable the timer and let WTCNT advance.
	w.WriteWord(0xFFFFFE80, 0xA520) // A5 key, TME=1, CKS=0
	tickWDT(&w, 10)
	if w.wtcnt == 0 {
		t.Fatalf("setup: WTCNT should have advanced, got 0")
	}

	// Disable the timer via A5 key with TME=0.
	w.WriteWord(0xFFFFFE80, 0xA500)
	if w.wtcnt != 0 {
		t.Errorf("WTCNT = 0x%02X after TME=0, want 0x00 (Sec 12.2.2)", w.wtcnt)
	}
}

// Manual Sec 12.1.4 Table 12.2: 0xFFFFFE82 is the RSTCSR write address;
// the read address is 0xFFFFFE83. A read from 0xFFFFFE82 returns 0 in our
// implementation (undefined in the manual).
func TestWDTReadAddress82ReturnsZero(t *testing.T) {
	var w WDT
	w.Reset()
	w.rstcsr = 0xFF
	if v := w.Read(0xFFFFFE82); v != 0 {
		t.Errorf("Read(0xFFFFFE82) = 0x%02X, want 0 (RSTCSR write-only address)", v)
	}
	// 0xFFFFFE83 should still return the current RSTCSR value.
	if v := w.Read(0xFFFFFE83); v != 0xFF {
		t.Errorf("Read(0xFFFFFE83) RSTCSR = 0x%02X, want 0xFF", v)
	}
}

// HM Sec 4.4.2: CPU accepts an interrupt only when the request's
// priority level is strictly greater than SR.IMASK. WDT interrupt at
// IPRA=15 with SR.IMASK=15 must not be taken; the OVF latch stays
// set. Dropping IMASK to 14 opens the gate on the next check.
func TestWDTOVFLatchedWhileIMASKBlocks(t *testing.T) {
	cpu := setupIntCPU(t)
	cpu.reg.SR = srIMask // IMASK=15
	// IPRA bits 7-4 = 15 for WDT.
	cpu.intc.ipra = (cpu.intc.ipra &^ 0x00F0) | 0x00F0
	// VCRWDT ITI vector.
	cpu.intc.vcrwdt = (0x50 << 8)

	// Enable the timer, drive it to overflow, route. A5-keyed write
	// to set TME=1, CKS=0; preload WTCNT=0xFF; Tick one prescaled
	// period to wrap.
	cpu.wdt.WriteWord(0xFFFFFE80, 0xA520)
	cpu.wdt.WriteWord(0xFFFFFE80, 0x5AFF) // WTCNT=0xFF via 5A-key path
	if !tickWDT(&cpu.wdt, 1<<wdtPrescalerShift[0]) {
		t.Fatalf("setup: syncTo did not overflow WTCNT")
	}
	cpu.routeWDTInterrupt()

	if !cpu.wdt.IRQAsserted() {
		t.Fatal("IRQAsserted false after overflow setup")
	}

	// IMASK=15, priority=15: level is not strictly greater, no accept.
	if cpu.processInterrupt() {
		t.Fatal("WDT interrupt accepted at IMASK=15 with level 15")
	}
	if cpu.wdt.wtcsr&wtcsrOVF == 0 {
		t.Error("OVF cleared by masked processInterrupt; must stay latched")
	}

	// Drop IMASK to 14 and try again.
	cpu.reg.SetIMASK(14)
	if !cpu.processInterrupt() {
		t.Fatal("WDT interrupt not accepted at IMASK=14 with level 15")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("accepted vec = 0x%X, want 0x50 (VCRWDT ITI)", cpu.pendingAddr)
	}
}

// HM Sec 12.2.1: WTCNT "starts counting pulses of an internal clock
// source selected by clock select bits" when TME=1. The OVF latch in
// WTCSR does not stop the counter. Verify that after an overflow
// with OVF left latched, a second WTCNT wrap occurs and OVF stays 1.
func TestWDTCounterContinuesAfterOverflowBeforeClear(t *testing.T) {
	var w WDT
	w.Reset()
	// Enable with CKS=0 (phi/2 -> one tick per 2 CPU cycles).
	w.WriteWord(0xFFFFFE80, 0xA520)
	// Drive first overflow: 256 FRC increments * 2 cycles/tick = 512 cycles.
	overflowed := false
	for i := 0; i < 512; i++ {
		if tickWDT(&w, 1) {
			overflowed = true
		}
	}
	if !overflowed {
		t.Fatalf("setup: WTCNT did not overflow within 512 cycles")
	}
	if w.wtcsr&wtcsrOVF == 0 {
		t.Fatal("setup: OVF not latched after first overflow")
	}
	preCount := w.wtcnt

	// Continue ticking without clearing OVF. WTCNT must advance and
	// eventually wrap again.
	secondOverflow := false
	for i := 0; i < 512; i++ {
		if tickWDT(&w, 1) {
			secondOverflow = true
		}
	}
	if !secondOverflow {
		t.Errorf("WTCNT did not wrap a second time while OVF latched (pre-count=%d)",
			preCount)
	}
	if w.wtcsr&wtcsrOVF == 0 {
		t.Error("OVF bit cleared spontaneously; must stay latched until software clears")
	}
}

// --- Deadline scheduling coverage -----------------------------------

// TestWDTNextEventWhenDisabled: with TME=0 (reset default) no
// autonomous event can fire; nextEvent is MaxUint64.
func TestWDTNextEventWhenDisabled(t *testing.T) {
	var w WDT
	w.Reset()

	if w.wtcsr&wtcsrTME != 0 {
		t.Fatalf("setup: TME=1 after reset, got WTCSR=0x%02X", w.wtcsr)
	}
	if w.nextEvent != math.MaxUint64 {
		t.Errorf("nextEvent with TME=0 = 0x%X, want MaxUint64", w.nextEvent)
	}
}

// TestWDTNextEventAfterEnable: enabling TME with default CKS=0
// (phi/2, step=2) schedules the first wrap 256*2 = 512 cycles from
// lastSync.
func TestWDTNextEventAfterEnable(t *testing.T) {
	var w WDT
	w.Reset()

	w.WriteWord(0xFFFFFE80, 0xA520) // A5 key, TME=1, CKS=0
	w.recomputeNextEvent(w.lastSync)

	if w.nextEvent != w.lastSync+512 {
		t.Errorf("nextEvent after enable = 0x%X, want lastSync+512 (0x%X)",
			w.nextEvent, w.lastSync+512)
	}
}

// TestWDTNextEventRecomputedOnCKSChange: mid-run CKS change
// preserves prescaler and reschedules using the new divider.
func TestWDTNextEventRecomputedOnCKSChange(t *testing.T) {
	var w WDT
	w.Reset()
	w.WriteWord(0xFFFFFE80, 0xA520) // TME=1, CKS=0 (step=2)

	// Advance partway through the prescaler window.
	tickWDT(&w, 1)
	if w.prescaler != 1 {
		t.Fatalf("setup: prescaler = %d, want 1", w.prescaler)
	}

	// Switch to CKS=7 (shift=13, step=8192). Prescaler stays at 1.
	w.WriteWord(0xFFFFFE80, 0xA527)
	w.recomputeNextEvent(w.lastSync)

	if w.prescaler != 1 {
		t.Errorf("prescaler after CKS change = %d, want 1 (preserved)",
			w.prescaler)
	}
	// cyclesToAdv = 8192-1 = 8191; advances = 0x100-0 = 256;
	// cyclesToWrap = 8191 + 255*8192 = 2097151.
	want := w.lastSync + 8191 + 255*8192
	if w.nextEvent != want {
		t.Errorf("nextEvent after CKS=7 = 0x%X, want 0x%X",
			w.nextEvent, want)
	}
}

// TestWDTNextEventRecomputedOnWTCNTWrite: writing WTCNT near its
// wrap shortens the deadline accordingly.
func TestWDTNextEventRecomputedOnWTCNTWrite(t *testing.T) {
	var w WDT
	w.Reset()
	w.WriteWord(0xFFFFFE80, 0xA520) // TME=1, CKS=0

	// WTCNT=0xFE: one more advance goes to 0xFF, then 0x00 wrap.
	// That is 2 WTCNT advances * 2 cycles/advance = 4 cycles away.
	w.WriteWord(0xFFFFFE80, 0x5AFE)
	w.recomputeNextEvent(w.lastSync)

	if w.nextEvent != w.lastSync+4 {
		t.Errorf("nextEvent with WTCNT=0xFE = 0x%X, want lastSync+4",
			w.nextEvent)
	}
}

// TestWDTNextEventWatchdogMode: WT/IT=1 still schedules the wrap;
// wrap sets WOVF, not OVF, and does not raise ITI.
func TestWDTNextEventWatchdogMode(t *testing.T) {
	var w WDT
	w.Reset()
	w.WriteWord(0xFFFFFE80, 0xA560) // TME=1, WT/IT=1 (watchdog)
	w.WriteWord(0xFFFFFE80, 0x5AFF) // WTCNT=0xFF via 5A key
	w.recomputeNextEvent(w.lastSync)

	if w.nextEvent != w.lastSync+2 {
		t.Fatalf("nextEvent in watchdog mode = 0x%X, want lastSync+2",
			w.nextEvent)
	}

	// Fire the deadline (CKS=0 step=2). One wrap fires; WOVF set,
	// OVF not set, IRQAsserted false.
	if !w.fireDue(w.lastSync + 2) {
		t.Fatal("fireDue did not report overflow")
	}
	if w.rstcsr&rstcsrWOVF == 0 {
		t.Error("WOVF not latched after watchdog-mode wrap")
	}
	if w.wtcsr&wtcsrOVF != 0 {
		t.Error("OVF set in watchdog mode; should not be")
	}
	if w.IRQAsserted() {
		t.Error("IRQAsserted true in watchdog mode; should not be")
	}
}

// TestWDTNextEventAfterOVFClear: after software clears OVF, the
// next deadline is another full-cycle wrap away (256 WTCNT advances),
// not immediate. WTCNT continues counting past the wrap.
func TestWDTNextEventAfterOVFClear(t *testing.T) {
	var w WDT
	w.Reset()
	w.WriteWord(0xFFFFFE80, 0xA520) // TME=1, CKS=0
	w.WriteWord(0xFFFFFE80, 0x5AFF) // WTCNT=0xFF
	w.recomputeNextEvent(w.lastSync)

	// Fire the deadline. One wrap; OVF latched. WTCNT=0.
	if !w.fireDue(w.lastSync + 2) {
		t.Fatal("first wrap did not fire")
	}
	if w.wtcsr&wtcsrOVF == 0 {
		t.Fatal("OVF not latched after first wrap")
	}
	if w.wtcnt != 0 {
		t.Fatalf("WTCNT = 0x%02X after wrap, want 0x00", w.wtcnt)
	}

	// Clear OVF via A5-keyed write.
	w.WriteWord(0xFFFFFE80, 0xA520)
	w.recomputeNextEvent(w.lastSync)

	if w.wtcsr&wtcsrOVF != 0 {
		t.Fatal("OVF not cleared by A5/20 write")
	}
	// Next wrap: WTCNT=0, full 256-step cycle away = 512 cycles.
	if w.nextEvent != w.lastSync+512 {
		t.Errorf("nextEvent after OVF clear = 0x%X, want lastSync+512",
			w.nextEvent)
	}
}
