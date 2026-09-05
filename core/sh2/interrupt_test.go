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
	cpu.serviceException(vecTRAP, exceptionAddrErrorCycles)

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

	// Charges the cycles the caller passes for its sequence.
	if cpu.cycles-cyclesBefore != exceptionAddrErrorCycles {
		t.Errorf("cycles consumed = %d, want %d", cpu.cycles-cyclesBefore, exceptionAddrErrorCycles)
	}
}

// TestServiceExceptionCacheCoherency verifies that exception stacking
// updates a valid cached stack line. The pushes are ordinary
// write-through stores (SH7604 manual Section 8.4.2: a write hit
// updates the data array), so a handler's RTE popping through the
// cache must see the pushed PC/SR, not stale line contents.
func TestServiceExceptionCacheCoherency(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.SetCCR(ccrCE)

	cpu.reg.VBR = 0x0100
	cpu.reg.PC = 0x0400
	cpu.reg.SR = 0x000000F0
	cpu.reg.R[15] = 0x0800

	bus.Write32(0x0180, 0x00000600) // vecTRAP handler address

	// Warm the cache line covering the stack slots the exception
	// pushes to (0x07F8 and 0x07FC share the 16-byte line at 0x07F0).
	bus.Write32(0x07F8, 0xCAFEBABE)
	bus.Write32(0x07FC, 0xDEADBEEF)
	_ = cpu.Read32(0x07F8)

	cpu.serviceException(vecTRAP, exceptionAddrErrorCycles)

	if got := cpu.Read32(0x07F8); got != 0x0400 {
		t.Errorf("cached pushed PC = 0x%08X, want 0x00000400", got)
	}
	if got := cpu.Read32(0x07FC); got != 0x000000F0 {
		t.Errorf("cached pushed SR = 0x%08X, want 0x000000F0", got)
	}
	if got := bus.Read32(0x07F8); got != 0x0400 {
		t.Errorf("memory pushed PC = 0x%08X, want 0x00000400", got)
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
	if acceptedVector(cpu) < 0 {
		t.Error("interrupt not taken")
	}

	// After cycle 1 (processInterrupt), 8 more cycles complete the
	// exception sequence.

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

	if acceptedVector(cpu) != 0x40 {
		t.Errorf("accepted vector = 0x%X, want 0x40", acceptedVector(cpu))
	}
	// IMASK update should already be visible in the live SR.
	if cpu.reg.IMASK() != 5 {
		t.Errorf("IMASK = %d after accept, want 5", cpu.reg.IMASK())
	}

	// Drain the exception and verify stacked SR equals the pre-accept value.
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
			cpu.wdt.wtcsr |= wtcsrTME | wtcsrOVF
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

	fillVectorMarkers(cpu)
	cpu.intInhibit = true
	cpu.NMI()

	if !cpu.processInterrupt() {
		t.Fatal("NMI blocked by intInhibit; should be unconditional (Sec 5.2.1)")
	}
	if acceptedVector(cpu) != vecNMI {
		t.Errorf("accepted vector = 0x%X, want %d (vecNMI)", acceptedVector(cpu), vecNMI)
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
	if acceptedVector(cpu) != vecNMI {
		t.Errorf("first accept vec = 0x%X, want NMI vector %d", acceptedVector(cpu), vecNMI)
	}

	// Drain NMI exception dispatch.
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
	if acceptedVector(cpu) != 0x40 {
		t.Errorf("third accept vec = 0x%X, want 0x40 (DIVU)", acceptedVector(cpu))
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

	cpu.RunUntil(cpu.cycles + uint64(1))
	if !cpu.intInhibit {
		t.Fatal("intInhibit not set after LDC; precondition for Sec 4.6.2 test failed")
	}

	cpu.RunUntil(cpu.cycles + uint64(1))

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
// While the previous instruction is still
// executing, so a new interrupt request must be held until the op
// drains. acceptInterrupt and serviceException both schedule into
// their own multi-cycle state, so any preemption would corrupt the in-flight
// op's state. These tests assert no preemption occurs and that the
// queued request fires on the first Clock() after popOne becomes
// popNone.

// --- Area D: stepException dispatch sequence ---

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
	if acceptedVector(cpu) != vecNMI {
		t.Errorf("accepted vector = 0x%X, want %d", acceptedVector(cpu), vecNMI)
	}
	if cpu.reg.IMASK() != 15 {
		t.Errorf("IMASK = %d, want 15 (clamped)", cpu.reg.IMASK())
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
	cpu.SetIRLAck(func(vec uint16) { acks++ })
	cpu.SetIRL(8, 0x42)

	if !cpu.processInterrupt() {
		t.Fatal("IRL not accepted")
	}
	if acks != 1 {
		t.Errorf("ack count after accept = %d, want 1", acks)
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
	bus.Write16(0x302, 0x0009) // NOP delay slot
	bus.Write16(0x700, 0x0009) // NOP at branch target
	fillVectorMarkers(cpu)
	setup(bus, cpu)

	// The branch executes; its delay slot is next.
	cpu.Step()
	if !cpu.inDelay {
		t.Fatalf("inDelay not set after the branch; PC=0x%X", cpu.reg.PC)
	}

	assertDIVU(cpu, 5, 0x60)

	// Sec 4.6.1 forbids acceptance between the branch and its delay
	// slot: the slot executes and the branch takes effect.
	cpu.Step()
	if v := acceptedVector(cpu); v >= 0 {
		t.Fatalf("DIVU accepted before the delay slot ran (vector 0x%X)", v)
	}
	if cpu.inDelay {
		t.Error("inDelay still true after delay slot ran")
	}
	if cpu.reg.PC != 0x700 {
		t.Errorf("PC after delay slot = 0x%08X, want 0x700 (branch target)", cpu.reg.PC)
	}

	// The boundary after the delay slot accepts.
	cpu.Step()
	if v := acceptedVector(cpu); v != 0x60 {
		t.Errorf("DIVU not accepted after delay slot; accepted vector = %d", v)
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
