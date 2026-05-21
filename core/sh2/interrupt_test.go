// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// assertDIVU is a test helper that configures DIVU and INTC priority
// so that the next processInterrupt observes a DIVU overflow interrupt
// at the given (level, vec). DIVU is chosen because it has no
// sub-priority and uses simple register layouts.
func assertDIVU(cpu *CPU, level uint8, vec uint16) {
	cpu.divu.dvcr = 0x03 // OVF latch + OVFIE enable
	cpu.divu.vcrdiv = uint32(vec) & 0x7F
	cpu.intc.ipra = (cpu.intc.ipra &^ 0xF000) | (uint16(level&0xF) << 12)
	cpu.intc.AssertSource(isrcDIVU)
}

// clearDIVU clears the DIVU interrupt latch. Simulates what a handler
// would do by writing DVCR to clear OVF.
func clearDIVU(cpu *CPU) {
	cpu.divu.dvcr = 0x02 // keep OVFIE, clear OVF
	cpu.intc.pending &^= 1 << isrcDIVU
}

func TestServiceException(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Set up initial state
	cpu.reg.VBR = 0x0100
	cpu.reg.PC = 0x0400
	cpu.reg.SR = 0x000000F0 // IMASK=15
	cpu.reg.R[15] = 0x0800

	// Write handler address at VBR + vec*4
	// vec=32 (TRAP), so offset = 0x0100 + 32*4 = 0x0100 + 0x80 = 0x0180
	bus.Write32(0x0180, 0x00000600)

	cyclesBefore := cpu.cycles
	cpu.serviceException(vecTRAP)

	// R15 should be decremented by 8
	if cpu.reg.R[15] != 0x07F8 {
		t.Errorf("R15 = 0x%08X, want 0x000007F8", cpu.reg.R[15])
	}

	// SR should be pushed at R15+4
	pushed_sr := bus.Read32(0x07F8 + 4)
	if pushed_sr != 0x000000F0 {
		t.Errorf("pushed SR = 0x%08X, want 0x000000F0", pushed_sr)
	}

	// PC should be pushed at R15
	pushed_pc := bus.Read32(0x07F8)
	if pushed_pc != 0x0400 {
		t.Errorf("pushed PC = 0x%08X, want 0x00000400", pushed_pc)
	}

	// PC should now point to the handler
	if cpu.reg.PC != 0x00000600 {
		t.Errorf("PC = 0x%08X, want 0x00000600", cpu.reg.PC)
	}

	// Should have added 5 cycles
	if cpu.cycles-cyclesBefore != 5 {
		t.Errorf("cycles consumed = %d, want 5", cpu.cycles-cyclesBefore)
	}
}

func TestCheckInterruptMasked(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0 // clear everything
	cpu.reg.SetIMASK(15)

	assertDIVU(cpu, 10, 0x40)

	if cpu.processInterrupt() {
		t.Error("processInterrupt() returned true, want false (level 10 masked by IMASK=15)")
	}

	// Latch must remain asserted: manual says the peripheral flag
	// stays set until software clears it, regardless of whether the
	// INTC accepted the request.
	if !cpu.divu.IRQAsserted() {
		t.Error("DIVU latch should still be asserted after masked check")
	}
	if cpu.intc.pending&(1<<isrcDIVU) == 0 {
		t.Error("intc.pending DIVU bit should still be set after masked check")
	}
}

func TestCheckInterruptAccepted(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0 // IMASK=0
	cpu.reg.PC = 0x0200

	// Write handler at VBR + 0x40*4 = 0x0100 + 0x100 = 0x0200
	bus.Write32(0x0200, 0x00000300)

	assertDIVU(cpu, 5, 0x40)

	if !cpu.processInterrupt() {
		t.Error("processInterrupt() returned false, want true")
	}

	// IMASK should now be 5
	if imask := cpu.reg.IMASK(); imask != 5 {
		t.Errorf("IMASK = %d, want 5", imask)
	}

	// The exception dispatch should be queued.
	if cpu.pendingOp != popException {
		t.Errorf("pendingOp = %d, want popException", cpu.pendingOp)
	}

	// After cycle 1 (processInterrupt), need 4 more cycles for exception
	for i := 0; i < 4; i++ {
		cpu.stepPending()
	}

	// PC should point to handler
	if cpu.reg.PC != 0x00000300 {
		t.Errorf("PC = 0x%08X, want 0x00000300", cpu.reg.PC)
	}
}

func TestCheckInterruptNMI(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	cpu.reg.SetIMASK(15) // Maximum mask
	cpu.reg.PC = 0x0200

	// Write NMI handler at VBR + 11*4 = 0x0100 + 0x2C = 0x012C
	bus.Write32(0x012C, 0x00000500)

	cpu.NMI()

	if !cpu.nmiPending {
		t.Fatal("nmiPending should be true after NMI()")
	}

	if !cpu.processInterrupt() {
		t.Error("processInterrupt() returned false for NMI, want true")
	}

	// IMASK should be capped at 15
	if imask := cpu.reg.IMASK(); imask != 15 {
		t.Errorf("IMASK = %d, want 15", imask)
	}

	// nmiPending should be cleared after acceptance
	if cpu.nmiPending {
		t.Error("nmiPending should be false after NMI accepted")
	}

	// Complete the exception
	for i := 0; i < 4; i++ {
		cpu.stepPending()
	}

	// PC should point to NMI handler
	if cpu.reg.PC != 0x00000500 {
		t.Errorf("PC = 0x%08X, want 0x00000500", cpu.reg.PC)
	}
}

func TestCheckInterruptClearsHalt(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0 // IMASK=0
	cpu.reg.PC = 0x0200
	cpu.halted = true

	// Write handler at VBR + 0x40*4
	bus.Write32(0x0200, 0x00000300)

	assertDIVU(cpu, 5, 0x40)

	if !cpu.processInterrupt() {
		t.Error("processInterrupt() returned false, want true")
	}

	if cpu.halted {
		t.Error("halted should be false after interrupt accepted")
	}
}

func TestInterruptMultiStep(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0x30 // IMASK=3
	cpu.reg.PC = 0x0200

	// Write handler at VBR + 0x40*4 = 0x0200
	bus.Write32(0x0200, 0x00000600)
	// Write NOP at handler
	bus.Write16(0x0600, 0x0009)

	assertDIVU(cpu, 5, 0x40)
	before := cpu.cycles

	// Cycle 1: internal (processInterrupt)
	cpu.Clock()
	// Cycle 2: write SR
	s2 := cpu.Clock()
	if s2.Bus != BusWrite {
		t.Errorf("cycle 2: bus = %d, want BusWrite", s2.Bus)
	}
	// Cycle 3: write PC
	s3 := cpu.Clock()
	if s3.Bus != BusWrite {
		t.Errorf("cycle 3: bus = %d, want BusWrite", s3.Bus)
	}
	// Cycle 4: read vector
	s4 := cpu.Clock()
	if s4.Bus != BusRead {
		t.Errorf("cycle 4: bus = %d, want BusRead", s4.Bus)
	}
	// Cycle 5: pipeline refill
	s5 := cpu.Clock()
	if s5.Bus != BusNone {
		t.Errorf("cycle 5: bus = %d, want BusNone", s5.Bus)
	}

	elapsed := int(cpu.cycles - before)
	if elapsed != 5 {
		t.Errorf("interrupt total cycles = %d, want 5", elapsed)
	}

	if cpu.reg.PC != 0x00000600 {
		t.Errorf("PC = 0x%08X, want 0x00000600", cpu.reg.PC)
	}

	// Stacked SR
	stackedSR := bus.Read32(cpu.reg.R[15] + 4)
	if stackedSR != 0x30 {
		t.Errorf("stacked SR = 0x%08X, want 0x30", stackedSR)
	}
	// Stacked PC
	stackedPC := bus.Read32(cpu.reg.R[15])
	if stackedPC != 0x0200 {
		t.Errorf("stacked PC = 0x%08X, want 0x0200", stackedPC)
	}

	// Manual-correct behavior: handler must clear latch. Simulate
	// that so the test doesn't leave lingering state.
	clearDIVU(cpu)
}

// TestAcceptSnapshotsSRAndPC verifies the accept path captures the
// PRE-accept SR and PC into the pending-op state. Per Sec 5.4.1 the
// sequence is: save SR to stack, save PC to stack, then copy the
// accepted interrupt level to I3-I0. The stacked SR must therefore
// reflect the SR value before the IMASK update.
func TestAcceptSnapshotsSRAndPC(t *testing.T) {
	bus := newTestBus(0x10000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.PC = 0x0200
	// Distinct bits so the snapshot is easy to verify: IMASK=3, T=1,
	// S=1, Q=1, M=1.
	cpu.reg.SR = 0x0333
	seededSR := cpu.reg.SR

	// Handler at VBR + 0x40*4 = 0x0200.
	bus.Write32(0x0200, 0x00000300)

	assertDIVU(cpu, 5, 0x40)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}

	// Immediately after acceptInterrupt, before stepException drains:
	if cpu.pendingVal != seededSR {
		t.Errorf("pendingVal (SR snapshot) = 0x%08X, want 0x%08X", cpu.pendingVal, seededSR)
	}
	if cpu.pendingVal2 != 0x0200 {
		t.Errorf("pendingVal2 (PC snapshot) = 0x%08X, want 0x00000200", cpu.pendingVal2)
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("pendingAddr (vec) = 0x%X, want 0x40", cpu.pendingAddr)
	}
	// IMASK update should already be visible in the live SR.
	if cpu.reg.IMASK() != 5 {
		t.Errorf("IMASK = %d after accept, want 5", cpu.reg.IMASK())
	}

	// Drain the exception and verify stacked SR equals the pre-accept value.
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	stackedSR := bus.Read32(cpu.reg.R[15] + 4)
	if stackedSR != seededSR {
		t.Errorf("stacked SR = 0x%08X, want 0x%08X (pre-accept value)", stackedSR, seededSR)
	}
	stackedPC := bus.Read32(cpu.reg.R[15])
	if stackedPC != 0x0200 {
		t.Errorf("stacked PC = 0x%08X, want 0x00000200", stackedPC)
	}
}

// TestAcceptClampsIMaskAt15ForNMI verifies NMI's internal level of 16
// is clamped to 15 when written to IMASK. Sec 5.2.1: NMI exception
// handling sets I3-I0 to level 15.
func TestAcceptClampsIMaskAt15ForNMI(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x0200

	cpu.NMI()
	if !cpu.processInterrupt() {
		t.Fatal("NMI not accepted")
	}
	if cpu.reg.IMASK() != 15 {
		t.Errorf("IMASK after NMI = %d, want 15 (level 16 must clamp)", cpu.reg.IMASK())
	}
}

// TestAcceptClearsHaltedAllSources verifies every interrupt source
// wakes the CPU from SLEEP-induced halt. Sec 4.4.3 and the Sec 9.1
// note about SLEEP/halt both require acceptance to clear the halted
// state.
func TestAcceptClearsHaltedAllSources(t *testing.T) {
	cases := []struct {
		name  string
		setup func(cpu *CPU)
	}{
		{"DIVU", func(cpu *CPU) { assertDIVU(cpu, 5, 0x40) }},
		{"FRT", func(cpu *CPU) {
			priFRT(cpu, 5)
			assertFRTOCI(cpu, 0x50)
		}},
		{"DMAC0", func(cpu *CPU) {
			priDMAC(cpu, 5)
			assertDMAC(cpu, 0, 0x60)
		}},
		{"DMAC1", func(cpu *CPU) {
			priDMAC(cpu, 5)
			assertDMAC(cpu, 1, 0x70)
		}},
		{"WDT", func(cpu *CPU) {
			priWDT(cpu, 5)
			cpu.wdt.wtcsr |= wtcsrOVF
			cpu.intc.vcrwdt = 0x42 << 8
			cpu.intc.AssertSource(isrcWDT)
		}},
		{"NMI", func(cpu *CPU) { cpu.NMI() }},
		{"IRL", func(cpu *CPU) { cpu.SetIRL(8, 0x30) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cpu := setupIntCPU(t)
			cpu.halted = true
			tc.setup(cpu)

			if !cpu.processInterrupt() {
				t.Fatalf("%s accept failed", tc.name)
			}
			if cpu.halted {
				t.Errorf("%s accept did not clear halted", tc.name)
			}
		})
	}
}

// TestNMIBypassesIntInhibit verifies NMI is still accepted during the
// one-shot intInhibit window set by the instruction after LDC/STC/LDS/
// STS. Sec 4.6.2 restricts inhibit to maskable interrupts; Sec 5.2.1
// declares NMI "always accepted".
func TestNMIBypassesIntInhibit(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x0200

	cpu.intInhibit = true
	cpu.NMI()

	if !cpu.processInterrupt() {
		t.Fatal("NMI blocked by intInhibit; should be unconditional (Sec 5.2.1)")
	}
	if cpu.pendingAddr != uint32(vecNMI) {
		t.Errorf("pendingAddr = 0x%X, want %d (vecNMI)", cpu.pendingAddr, vecNMI)
	}
}

// TestIntInhibitBlocksMaskableOnlyNotNMI exercises the NMI+DIVU
// simultaneous-assert case while intInhibit is set. Manual Sec 4.6.2
// ("interrupts are not accepted" on the instruction after LDC/STC/
// LDS/STS) and Sec 5.2.1 (NMI "is always accepted") together are
// ambiguous about whether NMI acceptance consumes the one-shot: the
// manual does not describe how the inhibit interacts when NMI replaces
// the instruction carrying it.
//
// The implementation at processInterrupt (interrupt.go) returns from the
// NMI fast-path before reaching the intInhibit one-shot clear, so the
// inhibit is NOT consumed by NMI acceptance. It is consumed by the
// next maskable-interrupt check. This test verifies that sequencing.
func TestIntInhibitBlocksMaskableOnlyNotNMI(t *testing.T) {
	cpu := setupIntCPU(t)
	cpu.intInhibit = true

	cpu.NMI()
	assertDIVU(cpu, 5, 0x40)

	// First check: NMI accepted; intInhibit preserved per the
	// NMI fast-path.
	if !cpu.processInterrupt() {
		t.Fatal("NMI not accepted under intInhibit")
	}
	if cpu.pendingAddr != uint32(vecNMI) {
		t.Errorf("first accept vec = 0x%X, want NMI vector %d", cpu.pendingAddr, vecNMI)
	}

	// Drain NMI exception dispatch.
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	// Drop IMASK so DIVU (level 5) can be taken once intInhibit clears.
	cpu.reg.SR = 0

	// Second check: consumes intInhibit, returns false. This is the
	// one-shot semantics the implementation documents.
	if cpu.processInterrupt() {
		t.Fatal("second check accepted DIVU; intInhibit one-shot should have blocked it")
	}
	if cpu.intInhibit {
		t.Error("intInhibit still set after maskable check; one-shot should have cleared it")
	}

	// Third check: intInhibit cleared, DIVU fires.
	if !cpu.processInterrupt() {
		t.Fatal("DIVU not accepted after intInhibit drained")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("third accept vec = 0x%X, want 0x40 (DIVU)", cpu.pendingAddr)
	}
}

// TestAddressErrorAcceptedAfterInterruptDisabledInstr covers the
// address-error half of HM Sec 4.6.2 / Table 4.10: on the instruction
// immediately after an interrupt-disabled instruction
// (LDC/LDC.L/STC/STC.L/LDS/LDS.L/STS/STS.L), interrupts are not
// accepted but ADDRESS ERRORS ARE. Existing coverage
// (TestInterruptInhibitAllVariants, TestInterruptInhibitAfterLDC,
// TestInterruptInhibitAfterLDCL, TestNMIBypassesIntInhibit,
// TestIntInhibitBlocksMaskableOnlyNotNMI) exercises the interrupt
// side; this test exercises the address-error side.
func TestAddressErrorAcceptedAfterInterruptDisabledInstr(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// CPU address error vector (vector 9) -> handler at 0x100.
	handler := uint32(0x100)
	bus.Write32(uint32(vecCPUAddr)*4, handler)
	bus.Write16(handler, 0x0009) // NOP at handler

	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x200
	cpu.reg.R[2] = 0     // value to load into SR via LDC
	cpu.reg.R[1] = 0x301 // odd -> MOV.W will address-error

	// 0x200: LDC R2,SR (interrupt-disabled instruction) = 0x420E
	bus.Write16(0x200, 0x420E)
	// 0x202: MOV.W @R1,R0 (target of the Sec 4.6.2 rule) = 0x6011
	bus.Write16(0x202, 0x6011)

	cpu.Clock()
	if !cpu.intInhibit {
		t.Fatal("intInhibit not set after LDC; precondition for Sec 4.6.2 test failed")
	}

	cpu.Clock()

	if cpu.reg.PC != handler {
		t.Errorf("address error not accepted: PC = 0x%08X, want 0x%08X (handler)",
			cpu.reg.PC, handler)
	}
	stackedPC := bus.Read32(cpu.reg.R[15])
	if stackedPC != 0x204 {
		t.Errorf("stacked PC = 0x%08X, want 0x00000204 (after MOV.W)", stackedPC)
	}
}

// TestNMILatchOneShot verifies NMI() latches exactly one acceptance.
// Sec 5.7 fig 5.10 shows the NMI request is cleared after the decode
// stage of the replaced instruction.
func TestNMILatchOneShot(t *testing.T) {
	cpu := setupIntCPU(t)

	cpu.NMI()
	if !cpu.processInterrupt() {
		t.Fatal("first NMI not accepted")
	}
	if cpu.nmiPending {
		t.Error("nmiPending still set after accept")
	}

	// Drain the dispatch so processInterrupt is reentered cleanly.
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}

	if cpu.processInterrupt() {
		t.Error("second processInterrupt re-fired NMI without new NMI()")
	}
}

// TestNMISetsNMILBit verifies CPU.NMI() raises ICR bit 15 (NMIL)
// alongside latching the pending flag. Complements the existing
// SetNMIL round-trip coverage.
func TestNMISetsNMILBit(t *testing.T) {
	cpu := setupIntCPU(t)

	if cpu.intc.Read(0xFFFFFEE0)&0x8000 == 0 {
		t.Fatal("NMIL clear before NMI; test precondition broken")
	}
	cpu.intc.SetNMIL(false)
	if cpu.intc.Read(0xFFFFFEE0)&0x8000 != 0 {
		t.Fatal("SetNMIL(false) failed to clear NMIL")
	}

	cpu.NMI()
	if cpu.intc.Read(0xFFFFFEE0)&0x8000 == 0 {
		t.Error("NMI() did not raise NMIL bit")
	}
}

// --- Area C: interrupt deferral during multi-cycle pending ops ---
//
// Hardware Manual Sec 4.1.2 Table 4.2: interrupts and address errors
// "start when the previous executing instruction finishes executing."
// While pendingOp != popNone the previous instruction is still
// executing, so a new interrupt request must be held until the op
// drains. acceptInterrupt and serviceException both schedule into
// pendingOp themselves, so any preemption would corrupt the in-flight
// op's state. These tests assert no preemption occurs and that the
// queued request fires on the first Clock() after popOne becomes
// popNone.

// runDeferralTrial drives the body of a "high-priority interrupt
// arrives mid-pending op" trial. After the caller has issued at
// least one Clock() to dispatch the op (so pendingOp is set), this
// helper:
//   - asserts a DIVU interrupt at the given level/vector,
//   - drains the original pendingOp (failing if popException is ever
//     scheduled during the drain),
//   - returns the cycle-count delta consumed by the drain.
//
// The caller is responsible for verifying that the FIRST Clock()
// after the drain accepts the deferred DIVU and schedules popException.
func runDeferralTrial(t *testing.T, cpu *CPU, originalOp uint8, divuLevel uint8, divuVec uint16) uint64 {
	t.Helper()
	if cpu.pendingOp != originalOp {
		t.Fatalf("pre-trial pendingOp = %d, want %d", cpu.pendingOp, originalOp)
	}
	assertDIVU(cpu, divuLevel, divuVec)
	cyclesBefore := cpu.cycles
	for cpu.pendingOp == originalOp {
		cpu.Clock()
		if cpu.pendingOp == popException {
			t.Fatalf("DIVU preempted %d during drain (no preemption allowed)", originalOp)
		}
	}
	return cpu.cycles - cyclesBefore
}

// TestInterruptDeferredMidTRAPA covers C1.
func TestInterruptDeferredMidTRAPA(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write16(0x300, 0xC310)       // TRAPA #0x10
	bus.Write32(0x140, 0x500)        // TRAPA vector handler
	bus.Write32(0x100+0x60*4, 0x600) // DIVU vector 0x60 -> handler 0x600

	cpu.Clock() // dispatch TRAPA
	runDeferralTrial(t, cpu, popTRAPA, 5, 0x60)

	// Original TRAPA reached its handler.
	if cpu.reg.PC != 0x500 {
		t.Errorf("after TRAPA: PC = 0x%08X, want 0x500", cpu.reg.PC)
	}

	// Next Clock() must accept the deferred DIVU.
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("expected popException on Clock after drain, got %d", cpu.pendingOp)
	}
	if cpu.pendingAddr != 0x60 {
		t.Errorf("popException vector = 0x%X, want 0x60 (DIVU)", cpu.pendingAddr)
	}
}

// TestInterruptDeferredMidTAS covers C2 / TAS.
func TestInterruptDeferredMidTAS(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	cpu.reg.R[3] = 0x400
	bus.Write16(0x300, 0x431B) // TAS.B @R3
	bus.Write8(0x400, 0x10)
	bus.Write32(0x100+0x60*4, 0x600)

	cpu.Clock() // dispatch TAS EX
	runDeferralTrial(t, cpu, popTAS, 5, 0x60)

	// TAS completed: target byte's bit 7 is set.
	if got := bus.Read8(0x400); got != 0x90 {
		t.Errorf("TAS target = 0x%02X, want 0x90", got)
	}

	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("expected popException on Clock after TAS drain, got %d", cpu.pendingOp)
	}
}

// TestInterruptDeferredMidLDCL covers C2 / LDC.L. After the LDC.L
// drains, intInhibit (set by the LDC.L family per HM Sec 4.6.2) keeps
// the DIVU blocked for one further processInterrupt; the test stops at
// that boundary because driving past it requires guest code at PC.
// LDC.L destination is GBR here so the LDC.L's own VBR write does not
// disturb the active vector base.
func TestInterruptDeferredMidLDCL(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	cpu.reg.R[3] = 0x400
	bus.Write16(0x300, 0x4317) // LDC.L @R3+,GBR
	bus.Write32(0x400, 0xC0DE)
	bus.Write32(0x100+0x60*4, 0x600)

	cpu.Clock() // dispatch LDC.L EX
	runDeferralTrial(t, cpu, popLDCL, 5, 0x60)

	if cpu.reg.GBR != 0xC0DE {
		t.Errorf("GBR = 0x%08X, want 0xC0DE (LDC.L completed)", cpu.reg.GBR)
	}
	if cpu.reg.R[3] != 0x404 {
		t.Errorf("R[3] = 0x%08X, want 0x404 (post-incremented)", cpu.reg.R[3])
	}
	if !cpu.intInhibit {
		t.Error("intInhibit should still be set after LDC.L drain (Sec 4.6.2 one-shot)")
	}
	// DIVU latch must still be live; INTC pending bit set.
	if !cpu.divu.IRQAsserted() {
		t.Error("DIVU latch cleared during LDC.L drain")
	}
	if cpu.intc.pending&(1<<isrcDIVU) == 0 {
		t.Error("INTC pending bit cleared during LDC.L drain")
	}
}

// TestInterruptDeferredMidMACW covers C2 / MAC.W.
func TestInterruptDeferredMidMACW(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	cpu.reg.R[3] = 0x400
	cpu.reg.R[5] = 0x420
	bus.Write16(0x300, 0x435F) // MAC.W @R5+,@R3+
	bus.Write16(0x400, 7)
	bus.Write16(0x420, 6)
	bus.Write32(0x100+0x60*4, 0x600)

	cpu.Clock() // MAC.W cycle 1 (popMACW set)
	runDeferralTrial(t, cpu, popMACW, 5, 0x60)

	// MAC.W result: 7 * 6 = 42 in MACL.
	if cpu.reg.MACL != 42 {
		t.Errorf("MACL = %d, want 42", cpu.reg.MACL)
	}
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("expected popException after MAC.W drain, got %d", cpu.pendingOp)
	}
}

// TestInterruptDeferredMidMemRMW covers C2 / AND.B.
func TestInterruptDeferredMidMemRMW(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	cpu.reg.R[0] = 0x10
	cpu.reg.GBR = 0x400
	bus.Write16(0x300, 0xCD0F) // AND.B #0x0F,@(R0,GBR)
	bus.Write8(0x410, 0xAB)
	bus.Write32(0x100+0x60*4, 0x600)

	cpu.Clock() // dispatch AND.B EX (read + setPending)
	runDeferralTrial(t, cpu, popMemRMW, 5, 0x60)

	if got := bus.Read8(0x410); got != 0x0B {
		t.Errorf("AND.B target = 0x%02X, want 0x0B", got)
	}
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("expected popException after AND.B drain, got %d", cpu.pendingOp)
	}
}

// TestInterruptDeferredMidRTE covers C2 / RTE. After RTE drains, the
// next instruction is the delay slot. Per HM Sec 4.6.1 no interrupt
// is accepted during the delay slot, so the DIVU acceptance must wait
// for the instruction AFTER the delay slot.
func TestInterruptDeferredMidRTE(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x500
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write16(0x300, 0x002B) // RTE
	bus.Write16(0x302, 0x0009) // delay slot: NOP
	bus.Write32(0x500, 0x700)  // popped PC -> 0x700
	bus.Write32(0x504, 0)      // popped SR
	bus.Write32(0x100+0x60*4, 0x600)

	cpu.Clock() // RTE EX
	runDeferralTrial(t, cpu, popRTE, 5, 0x60)

	// RTE drained. PC should now be the delay-slot instruction.
	if cpu.reg.PC != 0x302 {
		t.Errorf("after RTE: PC = 0x%08X, want 0x302 (delay slot)", cpu.reg.PC)
	}
	if !cpu.inDelay {
		t.Error("inDelay should be true after RTE drains (delay-slot pending)")
	}

	// First Clock after RTE drain runs the delay slot. No interrupt
	// can be accepted during it (HM Sec 4.6.1).
	cpu.Clock()
	if cpu.pendingOp == popException {
		t.Fatal("DIVU accepted during RTE delay slot (Sec 4.6.1 violation)")
	}
	if cpu.inDelay {
		t.Error("inDelay should be false after delay slot ran")
	}
	if cpu.reg.PC != 0x700 {
		t.Errorf("after delay slot: PC = 0x%08X, want 0x700 (branch target)", cpu.reg.PC)
	}

	// Now the DIVU should be accepted on the next Clock.
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("expected popException after RTE+delay slot, got %d", cpu.pendingOp)
	}
}

// TestNMIDeferredMidPending covers C3. HM Sec 4.1.2 / Sec 5.4.1 do
// not exempt NMI from the "previous instruction must finish" rule.
// While popTRAPA is draining, NMI() may set the latch but acceptance
// is deferred.
func TestNMIDeferredMidPending(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write16(0x300, 0xC310)
	bus.Write32(0x140, 0x500)
	bus.Write32(0x100+11*4, 0x600) // NMI vector handler

	cpu.Clock() // TRAPA EX
	if cpu.pendingOp != popTRAPA {
		t.Fatalf("expected popTRAPA, got %d", cpu.pendingOp)
	}

	cpu.NMI()
	if !cpu.nmiPending {
		t.Fatal("NMI() did not latch nmiPending")
	}

	for cpu.pendingOp == popTRAPA {
		cpu.Clock()
		if cpu.pendingOp == popException {
			t.Fatal("NMI preempted TRAPA mid-drain")
		}
	}
	if !cpu.nmiPending {
		t.Error("nmiPending consumed during TRAPA drain (no acceptance possible while draining)")
	}

	// Now NMI accepted on next Clock.
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("NMI not accepted after TRAPA drain, pendingOp = %d", cpu.pendingOp)
	}
	if cpu.pendingAddr != uint32(vecNMI) {
		t.Errorf("dispatched vector = 0x%X, want %d (NMI)", cpu.pendingAddr, vecNMI)
	}
}

// TestNMIDeferredMidPopException covers C4. Even mid-popException,
// no further dispatch can preempt the in-flight one. NMI fires on
// the Clock() after popException drains.
func TestNMIDeferredMidPopException(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x100+0x40*4, 0x500) // DIVU handler
	bus.Write32(0x100+11*4, 0x600)   // NMI handler
	bus.Write16(0x500, 0x0009)       // NOP at DIVU handler

	assertDIVU(cpu, 5, 0x40)
	if !cpu.processInterrupt() {
		t.Fatal("DIVU not accepted")
	}
	if cpu.pendingOp != popException {
		t.Fatalf("expected popException, got %d", cpu.pendingOp)
	}

	// Mid-popException, raise NMI.
	cpu.NMI()

	for cpu.pendingOp == popException {
		cpu.Clock()
	}
	// No second popException scheduled inside the drain (only checked
	// implicitly: pendingOp went to popNone, not back to popException).
	if cpu.reg.PC != 0x500 {
		t.Errorf("after first popException: PC = 0x%08X, want 0x500", cpu.reg.PC)
	}
	if !cpu.nmiPending {
		t.Error("nmiPending consumed during popException drain")
	}

	// Clear the DIVU latch as the handler would, then run one more
	// Clock so the NMI fires.
	clearDIVU(cpu)
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("NMI not dispatched after popException drain, pendingOp = %d", cpu.pendingOp)
	}
	if cpu.pendingAddr != uint32(vecNMI) {
		t.Errorf("vector = 0x%X, want %d (NMI)", cpu.pendingAddr, vecNMI)
	}
}

// --- Area D: stepException dispatch sequence ---

// TestStepExceptionWalkSteps covers D1. Drives popException one step
// at a time, asserting each cycle's BusActivity matches PM Fig 7.95
// (write-write-read-refill).
func TestStepExceptionWalkSteps(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x100+0x40*4, 0x500)

	assertDIVU(cpu, 5, 0x40)
	if !cpu.processInterrupt() {
		t.Fatal("DIVU not accepted")
	}

	wantBus := []BusActivity{BusWrite, BusWrite, BusRead, BusNone}
	for i, want := range wantBus {
		state := cpu.stepPending()
		if state.Bus != want {
			t.Errorf("step %d: bus = %d, want %d", i+1, state.Bus, want)
		}
	}
	if cpu.pendingOp != popNone {
		t.Errorf("after 4 steps: pendingOp = %d, want popNone", cpu.pendingOp)
	}
	if cpu.reg.PC != 0x500 {
		t.Errorf("PC = 0x%08X, want 0x500 (vector handler)", cpu.reg.PC)
	}
}

// TestStepExceptionVectorAddressVBRZero covers D3 with VBR = 0.
func TestStepExceptionVectorAddressVBRZero(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x40*4, 0x500)

	assertDIVU(cpu, 5, 0x40)
	if !cpu.processInterrupt() {
		t.Fatal("DIVU not accepted")
	}
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	if cpu.reg.PC != 0x500 {
		t.Errorf("PC = 0x%08X, want 0x500 (VBR=0 + 0x40*4)", cpu.reg.PC)
	}
}

// TestStepExceptionVectorAddressVBRNonZero covers D3 with VBR = 0x4000.
func TestStepExceptionVectorAddressVBRNonZero(t *testing.T) {
	bus := newTestBus(0x10000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x4000
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x4000+0x40*4, 0x500)

	assertDIVU(cpu, 5, 0x40)
	if !cpu.processInterrupt() {
		t.Fatal("DIVU not accepted")
	}
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	if cpu.reg.PC != 0x500 {
		t.Errorf("PC = 0x%08X, want 0x500 (VBR=0x4000 + 0x40*4)", cpu.reg.PC)
	}
}

// TestStepExceptionNMIVector covers D4. NMI dispatches via vec 11
// and clamps IMASK at 15 (HM Sec 5.2.1 - NMI level 16 cannot be
// represented in I3-I0).
func TestStepExceptionNMIVector(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x100+11*4, 0x500)

	cpu.NMI()
	if !cpu.processInterrupt() {
		t.Fatal("NMI not accepted")
	}
	if cpu.pendingAddr != uint32(vecNMI) {
		t.Errorf("pendingAddr = 0x%X, want %d", cpu.pendingAddr, vecNMI)
	}
	if cpu.reg.IMASK() != 15 {
		t.Errorf("IMASK = %d, want 15 (clamped)", cpu.reg.IMASK())
	}
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	if cpu.reg.PC != 0x500 {
		t.Errorf("PC = 0x%08X, want 0x500", cpu.reg.PC)
	}
}

// TestStepExceptionIRLAck covers D5. When an IRL interrupt is
// accepted, irlAck is invoked exactly once (at acceptance time, not
// per-step).
func TestStepExceptionIRLAck(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x100+0x42*4, 0x500)

	acks := 0
	cpu.SetIRLAck(func() { acks++ })
	cpu.SetIRL(8, 0x42)

	if !cpu.processInterrupt() {
		t.Fatal("IRL not accepted")
	}
	if acks != 1 {
		t.Errorf("ack count after accept = %d, want 1", acks)
	}
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	if acks != 1 {
		t.Errorf("ack count after dispatch drain = %d, want 1 (no extra acks)", acks)
	}
}

// TestStepExceptionNotDelayedBranch covers D6. After the refill
// cycle, inDelay must be false; the next Clock() may accept a fresh
// interrupt without a spurious delay-slot block (HM Sec 4.4.3:
// "the jump is not a delayed branch").
func TestStepExceptionNotDelayedBranch(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x100+0x40*4, 0x500)
	bus.Write16(0x500, 0x0009) // NOP at handler

	assertDIVU(cpu, 5, 0x40)
	if !cpu.processInterrupt() {
		t.Fatal("DIVU not accepted")
	}
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	if cpu.inDelay {
		t.Error("inDelay set after popException dispatch (must not be a delayed branch)")
	}
}

// runBranchDelaySlotInterruptTrial places a delayed-branch instruction
// at 0x300 with a NOP delay slot at 0x302 and a target at 0x700. After
// the branch executes, it asserts a DIVU interrupt, drains the popStall
// (pipeline-refill) cycle(s), then clocks the delay slot. Per HM Sec
// 4.6.1 the delay slot must NOT accept the interrupt; the target fetch
// after must. Returns nothing; failures are reported via t.
func runBranchDelaySlotInterruptTrial(t *testing.T, setup func(bus *testBus, cpu *CPU)) {
	t.Helper()
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write16(0x302, 0x0009)       // NOP delay slot
	bus.Write16(0x700, 0x0009)       // NOP at branch target
	bus.Write32(0x100+0x60*4, 0x600) // DIVU vector 0x60 handler
	setup(bus, cpu)

	// Clock 1: dispatch the branch instruction.
	cpu.Clock()
	if !cpu.inDelay {
		t.Fatalf("inDelay not set after branch dispatch; pendingOp=%d PC=0x%X",
			cpu.pendingOp, cpu.reg.PC)
	}

	assertDIVU(cpu, 5, 0x60)

	// Drain the popStall refill cycle(s). During this window inDelay
	// is still true, so no interrupt may be accepted.
	for cpu.pendingOp == popStall {
		cpu.Clock()
		if cpu.pendingOp == popException {
			t.Fatal("DIVU accepted during branch popStall drain")
		}
	}

	// At this point inDelay is still true and the next Clock() will
	// execute the delay slot. Sec 4.6.1 forbids acceptance here.
	cpu.Clock()
	if cpu.pendingOp == popException {
		t.Fatal("DIVU accepted during delay slot (Sec 4.6.1 violation)")
	}
	if cpu.inDelay {
		t.Error("inDelay still true after delay slot ran")
	}
	if cpu.reg.PC != 0x700 {
		t.Errorf("PC after delay slot = 0x%08X, want 0x700 (branch target)", cpu.reg.PC)
	}

	// The Clock after the delay slot must accept.
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("DIVU not accepted after delay slot; pendingOp=%d", cpu.pendingOp)
	}
}

// HM Sec 4.6.1 coverage for JMP delay slot. Existing coverage
// (TestInterruptDeferredMidRTE) tests the same rule for RTE only.
func TestInterruptNotAcceptedInJMPDelaySlot(t *testing.T) {
	runBranchDelaySlotInterruptTrial(t, func(bus *testBus, cpu *CPU) {
		cpu.reg.R[0] = 0x700
		bus.Write16(0x300, 0x402B) // JMP @R0
	})
}

// HM Sec 4.6.1 coverage for BRA delay slot.
func TestInterruptNotAcceptedInBRADelaySlot(t *testing.T) {
	runBranchDelaySlotInterruptTrial(t, func(bus *testBus, cpu *CPU) {
		// BRA disp12: target = PC+4 + disp*2. PC after fetch = 0x302,
		// want target 0x700 -> disp = (0x700 - 0x304) / 2 = 0x1FE.
		// Opcode: 1010 dddd dddd dddd -> 0xA1FE.
		bus.Write16(0x300, 0xA1FE)
	})
}

// HM Sec 4.6.1 coverage for BRAF delay slot.
func TestInterruptNotAcceptedInBRAFDelaySlot(t *testing.T) {
	runBranchDelaySlotInterruptTrial(t, func(bus *testBus, cpu *CPU) {
		// BRAF Rn: target = PC+4 + Rn. PC after fetch = 0x302,
		// want target 0x700 -> Rn = 0x700 - 0x304 = 0x3FC.
		cpu.reg.R[1] = 0x3FC
		bus.Write16(0x300, 0x0123) // BRAF R1 = 0000 nnnn 0010 0011
	})
}

// HM Sec 4.6.1 coverage for RTS delay slot.
func TestInterruptNotAcceptedInRTSDelaySlot(t *testing.T) {
	runBranchDelaySlotInterruptTrial(t, func(bus *testBus, cpu *CPU) {
		cpu.reg.PR = 0x700
		bus.Write16(0x300, 0x000B) // RTS
	})
}

// Erings internal: the load-use stall inserts one cycle during which
// an instruction whose source depends on the preceding load's dest is
// deferred. processInterrupt (cpu.go:218) gates acceptance on
// !c.hasDeferred so the stall cycle cannot be interrupted. HM treats
// pipeline stalls as invisible to software - this test pins the gate.
// Sequence: MOV.L loads R2, ADD R2,R3 triggers the hazard, then DIVU
// is raised WHILE hasDeferred is set. The next Clock() must see the
// !hasDeferred guard fire and skip processInterrupt.
func TestInterruptNotAcceptedDuringLoadUseStall(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x300
	bus.Write32(0x100+0x60*4, 0x600)

	// MOV.L @R1,R2 then ADD R2,R3. The second consumes R2.
	cpu.reg.R[1] = 0x400
	bus.Write32(0x400, 0xDEADBEEF)
	bus.Write16(0x300, 0x6212) // MOV.L @R1,R2 = 0110 nnnn mmmm 0010
	bus.Write16(0x302, 0x332C) // ADD R2,R3   = 0011 nnnn mmmm 1100 (n=3 m=2)

	// Clock 1: MOV.L runs. lastLoadReg=R2.
	cpu.Clock()
	if cpu.reg.R[2] != 0xDEADBEEF {
		t.Fatalf("setup: MOV.L did not load R2; R2=0x%08X", cpu.reg.R[2])
	}
	if cpu.lastLoadReg != 2 {
		t.Fatalf("setup: lastLoadReg = %d, want 2 after MOV.L", cpu.lastLoadReg)
	}

	// Clock 2: ADD fetched, hazard detected, deferred.
	cpu.Clock()
	if !cpu.hasDeferred {
		t.Fatalf("load-use stall not inserted; hasDeferred=%v", cpu.hasDeferred)
	}

	// Raise DIVU while hasDeferred is set. The next Clock must NOT
	// accept - the pipeline stall is invisible to software.
	assertDIVU(cpu, 5, 0x60)

	cpu.Clock()
	if cpu.pendingOp == popException {
		t.Fatal("DIVU accepted during load-use stall; hasDeferred gate missing")
	}
	// The deferred ADD has now executed.
	if cpu.hasDeferred {
		t.Error("hasDeferred still set after deferred ADD ran")
	}
	if cpu.reg.R[3] != 0xDEADBEEF {
		t.Errorf("ADD did not execute: R3=0x%08X, want 0xDEADBEEF", cpu.reg.R[3])
	}

	// The Clock after that must accept.
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Errorf("DIVU not accepted after load-use stall drained; pendingOp=%d",
			cpu.pendingOp)
	}
}
