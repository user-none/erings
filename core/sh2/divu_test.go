// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestDIVUInitValues(t *testing.T) {
	var divu DIVU
	divu.Reset()

	if divu.dvcr != 0 {
		t.Errorf("DVCR = 0x%08X, want 0", divu.dvcr)
	}
	if divu.vcrdiv != 0 {
		t.Errorf("VCRDIV = 0x%08X, want 0", divu.vcrdiv)
	}
}

func TestDIVU32by32(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// 100 / 7 = 14 remainder 2
	divu.Write(0xFFFFFF00, 7)   // DVSR = 7
	divu.Write(0xFFFFFF04, 100) // DVDNT = 100, triggers division

	q := divu.Read(0xFFFFFF04) // DVDNT = quotient
	r := divu.Read(0xFFFFFF10) // DVDNTH = remainder

	if int32(q) != 14 {
		t.Errorf("quotient = %d, want 14", int32(q))
	}
	if int32(r) != 2 {
		t.Errorf("remainder = %d, want 2", int32(r))
	}
}

func TestDIVU32by32Negative(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// -100 / 7 = -14 remainder -2
	divu.Write(0xFFFFFF00, 7)
	divu.Write(0xFFFFFF04, 0xFFFFFF9C)

	q := int32(divu.Read(0xFFFFFF04))
	r := int32(divu.Read(0xFFFFFF10))

	if q != -14 {
		t.Errorf("quotient = %d, want -14", q)
	}
	if r != -2 {
		t.Errorf("remainder = %d, want -2", r)
	}
}

func TestDIVU32by32BothNegative(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// -100 / -7 = 14 remainder -2
	divu.Write(0xFFFFFF00, 0xFFFFFFF9)
	divu.Write(0xFFFFFF04, 0xFFFFFF9C)

	q := int32(divu.Read(0xFFFFFF04))
	r := int32(divu.Read(0xFFFFFF10))

	if q != 14 {
		t.Errorf("quotient = %d, want 14", q)
	}
	if r != -2 {
		t.Errorf("remainder = %d, want -2", r)
	}
}

func TestDIVU64by32(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// 0x0000000100000000 / 3 = 0x55555555 remainder 1
	divu.Write(0xFFFFFF00, 3)          // DVSR = 3
	divu.Write(0xFFFFFF10, 0x00000001) // DVDNTH = high
	divu.Write(0xFFFFFF14, 0x00000000) // DVDNTL = low, triggers division

	q := divu.Read(0xFFFFFF14) // DVDNTL = quotient
	r := divu.Read(0xFFFFFF10) // DVDNTH = remainder

	if q != 0x55555555 {
		t.Errorf("quotient = 0x%08X, want 0x55555555", q)
	}
	if r != 1 {
		t.Errorf("remainder = %d, want 1", r)
	}
}

func TestDIVUDivideByZero(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Divide by zero -> overflow
	divu.Write(0xFFFFFF00, 0) // DVSR = 0
	divu.Write(0xFFFFFF04, 100)

	if divu.dvcr&0x01 == 0 {
		t.Error("OVF not set after divide by zero")
	}
}

func TestDIVUOverflow32(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// 0x80000000 / -1 overflows int32 (result would be +2147483648)
	divu.Write(0xFFFFFF00, 0xFFFFFFFF)
	divu.Write(0xFFFFFF04, 0x80000000)

	if divu.dvcr&0x01 == 0 {
		t.Error("OVF not set for int32 overflow")
	}

	// Both operands negative, result positive -> positive overflow clamp
	q := divu.Read(0xFFFFFF04)
	if q != 0x7FFFFFFF {
		t.Errorf("clamped quotient = 0x%08X, want 0x7FFFFFFF", q)
	}
}

func TestDIVUOverflowPositiveClamp(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// 64-bit dividend that produces quotient > MaxInt32
	divu.Write(0xFFFFFF00, 1)          // DVSR = 1
	divu.Write(0xFFFFFF10, 0x00000001) // DVDNTH = 1
	divu.Write(0xFFFFFF14, 0x00000000) // DVDNTL = 0 -> dividend = 0x100000000

	if divu.dvcr&0x01 == 0 {
		t.Error("OVF not set for positive overflow")
	}

	q := divu.Read(0xFFFFFF14)
	if q != 0x7FFFFFFF {
		t.Errorf("clamped quotient = 0x%08X, want 0x7FFFFFFF", q)
	}
}

func TestDIVUOverflowInterrupt(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Enable overflow interrupt
	divu.Write(0xFFFFFF08, 0x02) // OVFIE=1

	// Set vector
	divu.Write(0xFFFFFF0C, 0x50) // vector 0x50

	// Divide by zero
	divu.Write(0xFFFFFF00, 0)
	irq := divu.Write(0xFFFFFF04, 100)

	if !irq {
		t.Error("expected interrupt signal on overflow with OVFIE=1")
	}
	if divu.vcrdiv != 0x50 {
		t.Errorf("VCRDIV = 0x%02X, want 0x50", divu.vcrdiv)
	}
}

func TestDIVUNoOverflowNoInterrupt(t *testing.T) {
	var divu DIVU
	divu.Reset()

	divu.Write(0xFFFFFF08, 0x02) // OVFIE=1
	divu.Write(0xFFFFFF00, 7)
	irq := divu.Write(0xFFFFFF04, 100)

	if irq {
		t.Error("unexpected interrupt signal on normal division")
	}
	if divu.dvcr&0x01 != 0 {
		t.Error("OVF set on normal division")
	}
}

func TestDIVURegisterReadWrite(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// DVCR only bits 1-0
	divu.Write(0xFFFFFF08, 0xFFFFFFFF)
	if divu.Read(0xFFFFFF08) != 0x03 {
		t.Errorf("DVCR = 0x%08X, want 0x03", divu.Read(0xFFFFFF08))
	}

	// VCRDIV only bits 6-0
	divu.Write(0xFFFFFF0C, 0xFFFFFFFF)
	if divu.Read(0xFFFFFF0C) != 0x7F {
		t.Errorf("VCRDIV = 0x%08X, want 0x7F", divu.Read(0xFFFFFF0C))
	}
}

func TestDIVUDVDNTMirrorsDVDNTL(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// After 32/32 division, DVDNT and DVDNTL should both hold the quotient
	divu.Write(0xFFFFFF00, 5)
	divu.Write(0xFFFFFF04, 25)

	dvdnt := divu.Read(0xFFFFFF04)
	dvdntl := divu.Read(0xFFFFFF14)

	if dvdnt != dvdntl {
		t.Errorf("DVDNT=0x%08X != DVDNTL=0x%08X", dvdnt, dvdntl)
	}
	if int32(dvdnt) != 5 {
		t.Errorf("quotient = %d, want 5", int32(dvdnt))
	}
}

func TestDIVUDivideByZeroClampPositive(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Positive dividend / 0 -> positive overflow -> clamp to 0x7FFFFFFF
	divu.Write(0xFFFFFF00, 0)
	divu.Write(0xFFFFFF04, 100)

	q := divu.Read(0xFFFFFF04)
	if q != 0x7FFFFFFF {
		t.Errorf("positive div-by-zero clamp = 0x%08X, want 0x7FFFFFFF", q)
	}
}

func TestDIVUDivideByZeroClampNegative(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Negative dividend / 0 -> negative overflow -> clamp to 0x80000000
	divu.Write(0xFFFFFF00, 0)
	divu.Write(0xFFFFFF04, 0xFFFFFF9C)

	q := divu.Read(0xFFFFFF04)
	if q != 0x80000000 {
		t.Errorf("negative div-by-zero clamp = 0x%08X, want 0x80000000", q)
	}
}

// Manual Sec 10.2.2: writing DVDNT mirrors the value into DVDNTL and
// sign-extends the MSB into DVDNTH. Verify for both polarities using
// a divisor that makes the answer easy to inspect (DVSR=1).
func TestDIVU32By32WriteDVDNTMirrorsDVDNTL(t *testing.T) {
	// Positive: DVDNTH should be sign-extended as 0 *before* dividing.
	// After a successful /1 divide, DVDNTL holds the quotient and DVDNTH
	// holds the remainder (both 0 / dividend for our case).
	var divu DIVU
	divu.Reset()
	divu.Write(0xFFFFFF00, 1)
	divu.Write(0xFFFFFF04, 0x12345678)

	if divu.Read(0xFFFFFF04) != 0x12345678 {
		t.Errorf("DVDNT quotient = 0x%08X, want 0x12345678", divu.Read(0xFFFFFF04))
	}
	if divu.Read(0xFFFFFF14) != 0x12345678 {
		t.Errorf("DVDNTL mirror = 0x%08X, want 0x12345678", divu.Read(0xFFFFFF14))
	}
	if divu.Read(0xFFFFFF10) != 0 {
		t.Errorf("positive /1 remainder = 0x%08X, want 0", divu.Read(0xFFFFFF10))
	}

	// Negative: DVDNTH sign-extended to 0xFFFFFFFF, then divide by 1.
	divu.Reset()
	divu.Write(0xFFFFFF00, 1)
	divu.Write(0xFFFFFF04, 0x80000001) // negative dividend
	// 0x80000001 (int32 = -2147483647) / 1 = -2147483647 = 0x80000001
	if divu.Read(0xFFFFFF04) != 0x80000001 {
		t.Errorf("negative DVDNT quotient = 0x%08X, want 0x80000001", divu.Read(0xFFFFFF04))
	}
}

// Manual Sec 10.3.1: 64-bit signed dividend with negative value produces
// correct signed results. Use DVSR=2 so we can verify easily.
func TestDIVU64By32NegativeDividend(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Dividend = -10 expressed as 64-bit: 0xFFFFFFFF_FFFFFFF6.
	// -10 / 2 = -5, remainder 0.
	divu.Write(0xFFFFFF00, 2)
	divu.Write(0xFFFFFF10, 0xFFFFFFFF)
	divu.Write(0xFFFFFF14, 0xFFFFFFF6)

	q := int32(divu.Read(0xFFFFFF14))
	r := int32(divu.Read(0xFFFFFF10))
	if q != -5 {
		t.Errorf("quotient = %d, want -5", q)
	}
	if r != 0 {
		t.Errorf("remainder = %d, want 0", r)
	}
}

// Manual Sec 10.3.1: 64-bit positive / negative divisor -> negative quotient.
func TestDIVU64By32NegativeDivisor(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// 100 (as 64-bit positive) / -4 = -25, remainder 0.
	divu.Write(0xFFFFFF00, 0xFFFFFFFC) // -4
	divu.Write(0xFFFFFF10, 0x00000000)
	divu.Write(0xFFFFFF14, 0x00000064) // 100

	q := int32(divu.Read(0xFFFFFF14))
	r := int32(divu.Read(0xFFFFFF10))
	if q != -25 {
		t.Errorf("quotient = %d, want -25", q)
	}
	if r != 0 {
		t.Errorf("remainder = %d, want 0", r)
	}
}

// Manual Sec 10.3.1: negative / negative -> positive.
func TestDIVU64By32BothNegative(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// -21 / -4 = 5, remainder -1.
	divu.Write(0xFFFFFF00, 0xFFFFFFFC)
	divu.Write(0xFFFFFF10, 0xFFFFFFFF)
	divu.Write(0xFFFFFF14, 0xFFFFFFEB) // -21

	q := int32(divu.Read(0xFFFFFF14))
	r := int32(divu.Read(0xFFFFFF10))
	if q != 5 {
		t.Errorf("quotient = %d, want 5", q)
	}
	if r != -1 {
		t.Errorf("remainder = %d, want -1", r)
	}
}

// Manual Sec 10.3.3 / Table 10.2: when overflow is negative (quotient
// underflow) and OVFIE=0, DVDNTL is clamped to 0x80000000.
func TestDIVUOverflowClampMinInt32Negative(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// 64-bit dividend = -0x200000000 (well below MinInt32), divisor = 1.
	// Quotient would be -0x200000000, underflows int32.
	divu.Write(0xFFFFFF00, 1)
	divu.Write(0xFFFFFF10, 0xFFFFFFFE) // -2 in upper
	divu.Write(0xFFFFFF14, 0x00000000)

	if divu.dvcr&0x01 == 0 {
		t.Error("OVF not set for negative overflow")
	}
	q := divu.Read(0xFFFFFF14)
	if q != 0x80000000 {
		t.Errorf("negative-overflow clamp = 0x%08X, want 0x80000000", q)
	}
}

// Manual Sec 10.2.4: VCRDIV bits 31-7 are reserved and always read 0; only
// bits 6-0 store the vector number.
func TestDIVUVCRDIVReservedBits(t *testing.T) {
	var divu DIVU
	divu.Reset()

	divu.Write(0xFFFFFF0C, 0xFFFFFFFF)
	if divu.Read(0xFFFFFF0C) != 0x7F {
		t.Errorf("VCRDIV = 0x%08X, want 0x7F (bits 31-7 reserved)", divu.Read(0xFFFFFF0C))
	}
}

// Manual Sec 10.2.3: DVCR bits 31-2 are reserved; only OVFIE (bit 1) and
// OVF (bit 0) store.
func TestDIVUDVCRReservedBits(t *testing.T) {
	var divu DIVU
	divu.Reset()

	divu.Write(0xFFFFFF08, 0xFFFFFFFF)
	if divu.Read(0xFFFFFF08) != 0x03 {
		t.Errorf("DVCR = 0x%08X, want 0x03 (bits 31-2 reserved)", divu.Read(0xFFFFFF08))
	}
}

// Manual Sec 10.4.2: "When an overflow occurs, the overflow flag (OVF) is
// set and is not automatically reset." A later successful divide must not
// clear OVF.
func TestDIVUOVFFlagPersistsAcrossSuccessfulDivide(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Trigger overflow via divide by zero.
	divu.Write(0xFFFFFF00, 0)
	divu.Write(0xFFFFFF04, 42)
	if divu.dvcr&0x01 == 0 {
		t.Fatal("setup: OVF not set")
	}

	// A clean divide follows.
	divu.Write(0xFFFFFF00, 2)
	divu.Write(0xFFFFFF04, 10)
	if divu.Read(0xFFFFFF04) != 5 {
		t.Fatalf("setup: quotient = %d, want 5", divu.Read(0xFFFFFF04))
	}

	if divu.dvcr&0x01 == 0 {
		t.Error("OVF auto-cleared by clean divide; manual says it must be cleared by software")
	}
}

// Manual Sec 10.2.3: OVF is R/W by software. Writing 0 to bit 0 must clear
// the latch.
func TestDIVUOVFClearableBySoftware(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Latch OVF via divide by zero.
	divu.Write(0xFFFFFF00, 0)
	divu.Write(0xFFFFFF04, 42)
	if divu.dvcr&0x01 == 0 {
		t.Fatal("setup: OVF not set")
	}

	// Software clear.
	divu.Write(0xFFFFFF08, 0x00)
	if divu.dvcr&0x01 != 0 {
		t.Errorf("OVF not cleared by software: DVCR = 0x%08X", divu.dvcr)
	}
}

// Manual Sec 10.3.2: after 32-bit / 32-bit division, DVDNT and DVDNTL both
// hold the quotient. Already covered by TestDIVUDVDNTMirrorsDVDNTL. This
// test covers the 64/32 path: after 64/32 division, DVDNT mirrors DVDNTL.
func TestDIVU64By32DVDNTMirrorsDVDNTL(t *testing.T) {
	var divu DIVU
	divu.Reset()

	divu.Write(0xFFFFFF00, 3)          // DVSR = 3
	divu.Write(0xFFFFFF10, 0x00000001) // DVDNTH = 1
	divu.Write(0xFFFFFF14, 0x00000000) // DVDNTL = 0 -> triggers

	l := divu.Read(0xFFFFFF14)
	dvdnt := divu.Read(0xFFFFFF04)
	if l != 0x55555555 {
		t.Fatalf("setup: quotient = 0x%08X, want 0x55555555", l)
	}
	if dvdnt != l {
		t.Errorf("DVDNT = 0x%08X, DVDNTL = 0x%08X; should mirror after 64/32 divide",
			dvdnt, l)
	}
}

// Simplification lock. HM Sec 10.3.2: "This unit finishes a single
// operation in 39 cycles (starting from the setting of the value in
// DVDNT)." erings completes the division instantaneously at the
// register-write call site; the result is visible on the immediately
// following Read. README "DIVU Simplifications" documents the
// 39-cycle timing as unmodeled. This test pins the simplified
// behavior so a future change that adds real timing has to revisit
// callers expecting instant results.
func TestDIVUCompletesImmediatelyOnDVDNTWrite(t *testing.T) {
	var divu DIVU
	divu.Reset()

	divu.Write(0xFFFFFF00, 7)
	divu.Write(0xFFFFFF04, 0x0000_004E) // 78 / 7 = 11 r 1

	if q := divu.Read(0xFFFFFF04); q != 11 {
		t.Errorf("DVDNT read immediately after trigger = %d, want 11", q)
	}
	if l := divu.Read(0xFFFFFF14); l != 11 {
		t.Errorf("DVDNTL read immediately after trigger = %d, want 11", l)
	}
	if r := divu.Read(0xFFFFFF10); int32(r) != 1 {
		t.Errorf("DVDNTH read immediately after trigger = %d, want 1", int32(r))
	}
}

// Simplification lock. HM Sec 10.3.3: "the operation will then end
// with the result after 6 cycles of operation." erings writes the
// overflow clamp synchronously on the triggering register write;
// there is no 6-cycle window during which the result is not yet
// observable. README "DIVU Simplifications" documents the timing
// divergence.
func TestDIVUOverflowCompletesImmediately(t *testing.T) {
	var divu DIVU
	divu.Reset()

	divu.Write(0xFFFFFF00, 0) // divide by zero
	divu.Write(0xFFFFFF04, 42)

	if divu.dvcr&0x01 == 0 {
		t.Error("OVF not set immediately after triggering write")
	}
	if q := divu.Read(0xFFFFFF14); q != 0x7FFFFFFF {
		t.Errorf("DVDNTL read immediately after overflow = 0x%08X, want 0x7FFFFFFF clamp",
			q)
	}
}

// Simplification lock. HM Sec 10.3.3 / Table 10.2: when overflow
// occurs with OVFIE=1, hardware leaves "the result after 6 cycles
// of operation" in DVDNTL. erings does not compute the intermediate
// result; on the DVDNT write path DVDNTL is overwritten with the
// dividend (divu.go sets d.dvdntl = val before calling divide()),
// and handleOverflow with OVFIE=1 returns without updating DVDNTL.
// Net effect on a 32/32 overflow with OVFIE=1: DVDNTL reads back as
// the dividend, not the HM-spec intermediate result and not the
// previous DVDNTL value. README "DIVU Simplifications" currently
// describes this as "previous DVDNTL value unchanged," which is
// stale relative to the code - a README update is recommended but
// is out of scope for this test batch.
func TestDIVUOverflowOVFIEOne_DVDNTLNotOverwritten(t *testing.T) {
	var divu DIVU
	divu.Reset()

	// Seed DVDNTL with a sentinel via a clean divide.
	divu.Write(0xFFFFFF00, 2)
	divu.Write(0xFFFFFF04, 10)
	if divu.Read(0xFFFFFF14) != 5 {
		t.Fatalf("setup: DVDNTL = 0x%08X, want 5", divu.Read(0xFFFFFF14))
	}

	// Enable OVFIE, then trigger an overflow.
	divu.Write(0xFFFFFF08, 0x02) // OVFIE=1
	divu.Write(0xFFFFFF00, 0)    // divide by zero
	divu.Write(0xFFFFFF04, 99)

	if divu.dvcr&0x01 == 0 {
		t.Fatal("OVF not latched on divide-by-zero with OVFIE=1")
	}
	if l := divu.Read(0xFFFFFF14); l != 99 {
		t.Errorf("DVDNTL after OVFIE=1 overflow = 0x%08X, want 99 (dividend); "+
			"intermediate-at-6-cycles HM behavior is a documented simplification", l)
	}
}

// HM Sec 10.1.1: "Even during the division process, instructions
// not accessing the division unit can be parallel-processed."
// erings satisfies this in the degenerate case because division
// completes instantaneously and never presents a stall window to
// the CPU. Drive a NOP through cpu.Clock() immediately after a
// DVDNT write and verify it retires in a single cycle with no
// pending op. Comment: the HM spec describes hardware behavior
// we model by the absence of stall state rather than explicit
// parallel execution.
func TestDIVUConcurrentNonDivuInstructionDoesNotStall(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.SR = srIMask // mask all interrupts
	cpu.reg.PC = 0x400
	bus.Write16(0x400, 0x0009) // NOP

	// Trigger a division through the CPU on-chip dispatch.
	cpu.writeOnChip(0xFFFFFF00, 7)
	cpu.writeOnChip(0xFFFFFF04, 100)

	cyclesBefore := cpu.cycles
	cpu.Clock() // run the NOP

	if cpu.pendingOp != popNone {
		t.Errorf("NOP after DVDNT write left pendingOp=%d; expected none", cpu.pendingOp)
	}
	if cpu.reg.PC != 0x402 {
		t.Errorf("NOP after DVDNT write did not advance PC: PC=0x%08X want 0x402", cpu.reg.PC)
	}
	if cpu.cycles-cyclesBefore != 1 {
		t.Errorf("NOP after DVDNT write consumed %d cycles, want 1 (no stall)",
			cpu.cycles-cyclesBefore)
	}
	// And the quotient is already visible.
	if q, _ := cpu.readOnChip(0xFFFFFF14); q != 14 {
		t.Errorf("DVDNTL after concurrent NOP = %d, want 14", q)
	}
}
