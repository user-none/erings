// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// These tests cover the CPU-side glue that routes on-chip peripheral
// interrupt events into the INTC's pending bitmask, and the
// sub-priority resolution performed by resolveSource when processInterrupt
// scans the bitmask. They exercise:
//
//   - routeFRTInterrupts  (cpu.go): triggered-mask gate + priority gate
//   - routeDIVUInterrupt  (cpu.go): priority gate
//   - routeDMACInterrupt  (cpu.go): IE gate + priority gate + ch0/ch1 dispatch
//   - routeWDTInterrupt   (cpu.go): priority gate
//   - FRTInputCapture     (cpu.go): InputCapture public entry
//   - tickPeripherals -> FRT/WDT advance and route invariants
//   - writeOnChip    -> routeDIVUInterrupt path
//   - resolveSource  -> FRT sub-priority ICI > OCIA > OCIB > OVI

// assertFRTOVI configures FRT so an OVI interrupt is asserted with the
// given VCRD bits 14-8 vector.
func assertFRTOVI(cpu *CPU, vec uint16) {
	cpu.frt.tier |= tierOVIE
	cpu.frt.ftcsr |= ftcsrOVF
	cpu.intc.vcrd = (cpu.intc.vcrd &^ 0x7F00) | ((vec & 0x7F) << 8)
	cpu.intc.AssertSource(isrcFRT)
}

// --- routeFRTInterrupts direct tests -----------------------------------

// TestRouteFRTInterruptsZeroTriggered verifies the triggered=0 early
// return path: even with priority configured the pending bit must not
// be set.
func TestRouteFRTInterruptsZeroTriggered(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)

	cpu.routeFRTInterrupts(0)

	if cpu.intc.pending&(1<<isrcFRT) != 0 {
		t.Error("FRT pending bit set despite triggered=0")
	}
}

// TestRouteFRTInterruptsZeroPriority verifies the priority gate: even
// with triggered bits present, FRT with priority 0 must not be routed.
func TestRouteFRTInterruptsZeroPriority(t *testing.T) {
	cpu := setupIntCPU(t)
	// Leave IPRB FRT field (bits 11-8) as 0.

	cpu.routeFRTInterrupts(ftcsrOCFA)

	if cpu.intc.pending&(1<<isrcFRT) != 0 {
		t.Error("FRT pending bit set despite priority=0")
	}
}

// TestRouteFRTInterruptsAssertsPending verifies the normal path: non-zero
// triggered mask and non-zero priority -> isrcFRT bit set.
func TestRouteFRTInterruptsAssertsPending(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)

	cpu.routeFRTInterrupts(ftcsrOCFA)

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Error("FRT pending bit not set after route with priority and triggered bits")
	}
}

// --- FRT sub-priority resolution via resolveSource ---------------------

// TestFRTSubPriorityICIBeatsOCIA verifies that ICI wins over OCIA when
// both flags are set with both enables.
func TestFRTSubPriorityICIBeatsOCIA(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 7)

	// ICI vec 0x40 (VCRC bits 14-8), OCI vec 0x50 (VCRC bits 6-0)
	cpu.intc.vcrc = (0x40 << 8) | 0x50
	cpu.frt.tier |= tierICIE | tierOCIAE
	cpu.frt.ftcsr |= ftcsrICF | ftcsrOCFA
	cpu.intc.AssertSource(isrcFRT)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("accepted vec = 0x%X, want 0x40 (ICI should beat OCIA)", cpu.pendingAddr)
	}
}

// TestFRTOCIABOCIBShareOCIVector verifies manual Sec 5.3.6 / Table 5.6
// (shared OCI vector) and Sec 5.2.5 Table 5.4 (only three FRT sub-
// priority ranks: ICI, OCI, OVI). OCIA and OCIB occupy the same OCI
// rank and share the VCRC[6:0] vector. With both flags and both
// enables set, the resolved vector must be the shared OCI vector.
func TestFRTOCIABOCIBShareOCIVector(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 7)

	cpu.intc.vcrc = 0x0050
	cpu.frt.tier |= tierOCIAE | tierOCIBE
	cpu.frt.ftcsr |= ftcsrOCFA | ftcsrOCFB
	cpu.intc.AssertSource(isrcFRT)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("accepted vec = 0x%X, want 0x50", cpu.pendingAddr)
	}
}

// TestFRTSubPriorityOCIBBeatsOVI covers the f&0x04 branch: OCIB asserted
// alongside OVI with both enables and flags, OCIB wins.
func TestFRTSubPriorityOCIBBeatsOVI(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 8)

	// OCI vec 0x50 (VCRC bits 6-0), OVI vec 0x60 (VCRD bits 14-8)
	cpu.intc.vcrc = 0x0050
	cpu.intc.vcrd = 0x60 << 8
	cpu.frt.tier |= tierOCIBE | tierOVIE
	cpu.frt.ftcsr |= ftcsrOCFB | ftcsrOVF
	cpu.intc.AssertSource(isrcFRT)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("accepted vec = 0x%X, want 0x50 (OCIB should beat OVI)", cpu.pendingAddr)
	}
}

// TestFRTSubPriorityOVIOnly covers the f&0x02 branch: only OVF set with
// OVIE enabled, VCRD OVI vector delivered.
func TestFRTSubPriorityOVIOnly(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 4)

	assertFRTOVI(cpu, 0x60)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x60 {
		t.Errorf("accepted vec = 0x%X, want 0x60", cpu.pendingAddr)
	}
}

// --- End-to-end via tickPeripherals ------------------------------------

// TestFRTTickToInterruptOCIA exercises the natural caller path:
// tickPeripherals advances the CPU cycle counter until the FRT
// deadline fires a compare match; that call returns the triggered
// mask; routeFRTInterrupts sets the pending bit; processInterrupt
// accepts with the OCI vector.
func TestFRTTickToInterruptOCIA(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 7)

	cpu.frt.ocra = 3
	cpu.frt.tier = tierOCIAE | 0x01
	cpu.frt.recomputeNextEvent(cpu.cycles)
	cpu.intc.vcrc = 0x0052 // OCI vector 0x52

	// Default CKS=0 means phi/8 divider, so 24 cycles -> FRC=3.
	for i := 0; i < 24; i++ {
		cpu.cycles++
		cpu.tickPeripherals()
	}

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Fatal("FRT pending bit not set after compare match")
	}
	if !cpu.processInterrupt() {
		t.Fatal("FRT interrupt not accepted")
	}
	if cpu.pendingAddr != 0x52 {
		t.Errorf("accepted vec = 0x%X, want 0x52", cpu.pendingAddr)
	}
	if cpu.reg.IMASK() != 7 {
		t.Errorf("IMASK after accept = %d, want 7", cpu.reg.IMASK())
	}
}

// TestFRTTickToInterruptOverflow covers the overflow path end-to-end.
func TestFRTTickToInterruptOverflow(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 3)

	cpu.frt.frc = 0xFFFE
	cpu.frt.tier = tierOVIE | 0x01
	cpu.frt.recomputeNextEvent(cpu.cycles)
	cpu.intc.vcrd = 0x60 << 8 // OVI vector 0x60

	// 16 cycles at phi/8 = 2 FRC increments: 0xFFFE -> 0xFFFF -> 0x0000.
	for i := 0; i < 16; i++ {
		cpu.cycles++
		cpu.tickPeripherals()
	}

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Fatal("FRT pending bit not set after overflow")
	}
	if !cpu.processInterrupt() {
		t.Fatal("FRT overflow interrupt not accepted")
	}
	if cpu.pendingAddr != 0x60 {
		t.Errorf("accepted vec = 0x%X, want 0x60", cpu.pendingAddr)
	}
}

// TestFRTTickLatchClearReconciles verifies the reconcile path: after a
// compare match has set the pending bit, clearing the FTCSR flag without
// informing the INTC must cause processInterrupt to lazily clear the bit.
func TestFRTTickLatchClearReconciles(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 7)

	cpu.frt.ocra = 3
	cpu.frt.tier = tierOCIAE | 0x01
	cpu.frt.recomputeNextEvent(cpu.cycles)
	cpu.intc.vcrc = 0x0052

	for i := 0; i < 24; i++ {
		cpu.cycles++
		cpu.tickPeripherals()
	}
	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Fatal("FRT pending bit not set after compare match")
	}

	// Simulate handler clearing FTCSR without touching intc.pending.
	cpu.frt.ftcsr &^= ftcsrOCFA

	// IMASK blocks delivery so the reconcile path is observable.
	cpu.reg.SetIMASK(15)
	if cpu.processInterrupt() {
		t.Error("processInterrupt should not deliver (IMASK=15)")
	}
	if cpu.intc.pending&(1<<isrcFRT) != 0 {
		t.Error("FRT pending bit should be reconciled to 0 after latch cleared")
	}
}

// --- FRTInputCapture public API ----------------------------------------

// TestFRTInputCaptureAssertsWhenEnabled verifies the full path: ICR is
// latched, ICF is set, the INTC pending bit is set, and processInterrupt
// accepts the ICI vector.
func TestFRTInputCaptureAssertsWhenEnabled(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)

	cpu.frt.frc = 0x1234
	cpu.frt.tier = tierICIE | 0x01
	cpu.intc.vcrc = 0x40 << 8 // ICI vector 0x40

	cpu.FRTInputCapture()

	if cpu.frt.icr != 0x1234 {
		t.Errorf("ICR = 0x%04X, want 0x1234", cpu.frt.icr)
	}
	if cpu.frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF not set after input capture")
	}
	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Fatal("FRT pending bit not set")
	}
	if !cpu.processInterrupt() {
		t.Fatal("ICI interrupt not accepted")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("accepted vec = 0x%X, want 0x40", cpu.pendingAddr)
	}
}

// TestFRTInputCaptureNoAssertWhenDisabled verifies the ICIE=0 path:
// ICR is still latched and ICF is still set (InputCapture sets both
// unconditionally), but the INTC is not notified.
func TestFRTInputCaptureNoAssertWhenDisabled(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)

	cpu.frt.frc = 0x5678
	cpu.frt.tier = 0x01 // ICIE clear

	cpu.FRTInputCapture()

	if cpu.frt.icr != 0x5678 {
		t.Errorf("ICR = 0x%04X, want 0x5678", cpu.frt.icr)
	}
	if cpu.frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF should still be set even when ICIE disabled")
	}
	if cpu.intc.pending&(1<<isrcFRT) != 0 {
		t.Error("FRT pending bit should not be set when ICIE disabled")
	}
}

// TestFRTInputCaptureNoAssertWhenPriorityZero verifies the priority gate
// inside FRTInputCapture: ICIE enabled but priority 0 must not assert.
func TestFRTInputCaptureNoAssertWhenPriorityZero(t *testing.T) {
	cpu := setupIntCPU(t)
	// Leave IPRB FRT field at 0.

	cpu.frt.tier = tierICIE | 0x01

	cpu.FRTInputCapture()

	if cpu.intc.pending&(1<<isrcFRT) != 0 {
		t.Error("FRT pending bit should not be set when priority=0")
	}
}

// --- routeDIVUInterrupt direct tests -----------------------------------

// TestRouteDIVUInterruptZeroPriority verifies the priority gate: even
// with DVCR.OVF and OVFIE both set, priority 0 must not route.
func TestRouteDIVUInterruptZeroPriority(t *testing.T) {
	cpu := setupIntCPU(t)
	cpu.divu.dvcr = 0x03

	cpu.routeDIVUInterrupt()

	if cpu.intc.pending&(1<<isrcDIVU) != 0 {
		t.Error("DIVU pending bit set despite priority=0")
	}
}

// TestRouteDIVUInterruptAssertsPending verifies the normal path: priority
// > 0 causes isrcDIVU to be set in intc.pending. Note that route does
// not consult DIVU latch - only priority.
func TestRouteDIVUInterruptAssertsPending(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 7)

	cpu.routeDIVUInterrupt()

	if cpu.intc.pending&(1<<isrcDIVU) == 0 {
		t.Error("DIVU pending bit not set after route with priority > 0")
	}
}

// --- End-to-end via writeOnChip ----------------------------------------

// TestDIVUDivideByZeroRoutesInterrupt exercises the natural caller path
// at cpu.go writeOnChip: a DVSR=0 / DVDNT write with OVFIE enabled
// triggers divide-by-zero; DIVU.Write returns true; writeOnChip invokes
// routeDIVUInterrupt; processInterrupt accepts with the VCRDIV vector.
func TestDIVUDivideByZeroRoutesInterrupt(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 6)

	cpu.write32(0xFFFFFF08, 0x02) // OVFIE=1
	cpu.write32(0xFFFFFF0C, 0x48) // VCRDIV = 0x48
	cpu.write32(0xFFFFFF00, 0)    // DVSR = 0
	cpu.write32(0xFFFFFF04, 100)  // DVDNT = 100 -> triggers divide-by-zero

	if cpu.intc.pending&(1<<isrcDIVU) == 0 {
		t.Fatal("DIVU pending bit not set after divide-by-zero")
	}
	if !cpu.processInterrupt() {
		t.Fatal("DIVU interrupt not accepted")
	}
	if cpu.pendingAddr != 0x48 {
		t.Errorf("accepted vec = 0x%X, want 0x48", cpu.pendingAddr)
	}
	if cpu.reg.IMASK() != 6 {
		t.Errorf("IMASK after accept = %d, want 6", cpu.reg.IMASK())
	}
}

// TestDIVUSignedOverflowRoutesInterrupt covers the quotient-overflow
// overflow path: 0x80000000 / -1 doesn't fit in int32.
func TestDIVUSignedOverflowRoutesInterrupt(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 9)

	cpu.write32(0xFFFFFF08, 0x02)       // OVFIE=1
	cpu.write32(0xFFFFFF0C, 0x52)       // VCRDIV = 0x52
	cpu.write32(0xFFFFFF00, 0xFFFFFFFF) // DVSR = -1
	cpu.write32(0xFFFFFF04, 0x80000000) // DVDNT = INT32_MIN -> overflow

	if cpu.intc.pending&(1<<isrcDIVU) == 0 {
		t.Fatal("DIVU pending bit not set after signed overflow")
	}
	if !cpu.processInterrupt() {
		t.Fatal("DIVU interrupt not accepted")
	}
	if cpu.pendingAddr != 0x52 {
		t.Errorf("accepted vec = 0x%X, want 0x52", cpu.pendingAddr)
	}
}

// TestDIVU64By32OverflowRoutesInterrupt covers the 64/32 overflow path.
func TestDIVU64By32OverflowRoutesInterrupt(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 4)

	cpu.write32(0xFFFFFF08, 0x02)       // OVFIE=1
	cpu.write32(0xFFFFFF0C, 0x3C)       // VCRDIV = 0x3C
	cpu.write32(0xFFFFFF00, 1)          // DVSR = 1
	cpu.write32(0xFFFFFF10, 0x00000001) // DVDNTH = 1
	cpu.write32(0xFFFFFF14, 0x00000000) // DVDNTL = 0 -> 0x100000000 / 1 overflow

	if cpu.intc.pending&(1<<isrcDIVU) == 0 {
		t.Fatal("DIVU pending bit not set after 64/32 overflow")
	}
	if !cpu.processInterrupt() {
		t.Fatal("DIVU interrupt not accepted")
	}
	if cpu.pendingAddr != 0x3C {
		t.Errorf("accepted vec = 0x%X, want 0x3C", cpu.pendingAddr)
	}
}

// TestDIVUOverflowNoOVFIENoRoute verifies that with OVFIE disabled, the
// overflow is clamped inside DIVU, DIVU.Write returns false, and
// routeDIVUInterrupt is never called.
func TestDIVUOverflowNoOVFIENoRoute(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 7)

	cpu.write32(0xFFFFFF08, 0x00) // OVFIE=0
	cpu.write32(0xFFFFFF00, 0)    // DVSR=0
	cpu.write32(0xFFFFFF04, 100)  // triggers divide-by-zero

	if cpu.divu.dvcr&0x01 == 0 {
		t.Error("OVF latch should be set even with OVFIE disabled")
	}
	if cpu.intc.pending&(1<<isrcDIVU) != 0 {
		t.Error("DIVU pending bit should not be set when OVFIE=0")
	}
}

// TestDIVUNoOverflowNoRoute verifies that a normal division (no overflow)
// never calls routeDIVUInterrupt, even with OVFIE enabled.
func TestDIVUNoOverflowNoRoute(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 7)

	cpu.write32(0xFFFFFF08, 0x02) // OVFIE=1
	cpu.write32(0xFFFFFF00, 7)
	cpu.write32(0xFFFFFF04, 100) // 100/7 = 14 rem 2, no overflow

	if cpu.divu.dvcr&0x01 != 0 {
		t.Error("OVF latch should not be set for normal division")
	}
	if cpu.intc.pending&(1<<isrcDIVU) != 0 {
		t.Error("DIVU pending bit should not be set for normal division")
	}
}

// TestDIVULatchClearReconciles verifies the reconcile path for DIVU:
// after the pending bit is set by an overflow, clearing DVCR.OVF must
// cause the next processInterrupt to lazily clear the pending bit.
func TestDIVULatchClearReconciles(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 5)

	cpu.write32(0xFFFFFF08, 0x02) // OVFIE=1
	cpu.write32(0xFFFFFF00, 0)
	cpu.write32(0xFFFFFF04, 100) // divide-by-zero

	if cpu.intc.pending&(1<<isrcDIVU) == 0 {
		t.Fatal("DIVU pending bit not set after overflow")
	}

	// Simulate handler clearing DVCR.OVF (keep OVFIE). DIVU.Write
	// returns false for DVCR writes, so routeDIVUInterrupt is not
	// called and the pending bit remains set until reconciled.
	cpu.write32(0xFFFFFF08, 0x02)

	cpu.reg.SetIMASK(15)
	if cpu.processInterrupt() {
		t.Error("processInterrupt should not deliver (IMASK=15)")
	}
	if cpu.intc.pending&(1<<isrcDIVU) != 0 {
		t.Error("DIVU pending bit should be reconciled to 0 after latch cleared")
	}
}

// priWDT sets the WDT priority in IPRA bits 7-4.
func priWDT(cpu *CPU, level uint8) {
	cpu.intc.ipra = (cpu.intc.ipra &^ 0x00F0) | (uint16(level&0xF) << 4)
}

// --- routeDMACInterrupt direct tests -----------------------------------

// TestRouteDMACInterruptIEClear verifies the IE gate: when CHCR.IE (bit 2)
// is clear, routeDMACInterrupt returns without setting the pending bit,
// even if priority is configured.
func TestRouteDMACInterruptIEClear(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 5)

	// CHCR.TE set, CHCR.IE clear (0x02, not 0x06).
	cpu.dmac.ch[0].chcr = 0x02

	cpu.routeDMACInterrupt(0)

	if cpu.intc.pending&(1<<isrcDMAC0) != 0 {
		t.Error("DMAC0 pending bit set despite IE=0")
	}
}

// TestRouteDMACInterruptZeroPriority verifies the priority gate: with
// CHCR.IE set but IPRA DMAC field = 0, no assert.
func TestRouteDMACInterruptZeroPriority(t *testing.T) {
	cpu := setupIntCPU(t)
	// Leave IPRA DMAC field (bits 11-8) as 0.

	cpu.dmac.ch[0].chcr = 0x06 // TE | IE

	cpu.routeDMACInterrupt(0)

	if cpu.intc.pending&(1<<isrcDMAC0) != 0 {
		t.Error("DMAC0 pending bit set despite priority=0")
	}
}

// TestRouteDMACInterruptChannel0AssertsPending is a direct-call regression
// anchor for ch=0 success path.
func TestRouteDMACInterruptChannel0AssertsPending(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 5)

	cpu.dmac.ch[0].chcr = 0x06 // TE | IE

	cpu.routeDMACInterrupt(0)

	if cpu.intc.pending&(1<<isrcDMAC0) == 0 {
		t.Error("DMAC0 pending bit not set")
	}
	if cpu.intc.pending&(1<<isrcDMAC1) != 0 {
		t.Error("DMAC1 pending bit should not be set when routing ch=0")
	}
}

// TestRouteDMACInterruptChannel1AssertsPending covers the ch=1 dispatch
// path that asserts isrcDMAC1 instead of isrcDMAC0.
func TestRouteDMACInterruptChannel1AssertsPending(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 5)

	cpu.dmac.ch[1].chcr = 0x06 // TE | IE

	cpu.routeDMACInterrupt(1)

	if cpu.intc.pending&(1<<isrcDMAC1) == 0 {
		t.Error("DMAC1 pending bit not set")
	}
	if cpu.intc.pending&(1<<isrcDMAC0) != 0 {
		t.Error("DMAC0 pending bit should not be set when routing ch=1")
	}
}

// --- routeWDTInterrupt direct tests ------------------------------------

// TestRouteWDTInterruptZeroPriority verifies the priority gate: with WDT
// overflow state present but IPRA WDT field = 0, no assert.
func TestRouteWDTInterruptZeroPriority(t *testing.T) {
	cpu := setupIntCPU(t)
	// Leave IPRA WDT field (bits 7-4) as 0.

	// Simulate a WDT interval-mode overflow state.
	cpu.wdt.wtcsr |= wtcsrOVF

	cpu.routeWDTInterrupt()

	if cpu.intc.pending&(1<<isrcWDT) != 0 {
		t.Error("WDT pending bit set despite priority=0")
	}
}

// TestRouteWDTInterruptAssertsPending is a direct-call regression anchor
// for the WDT success path.
func TestRouteWDTInterruptAssertsPending(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 5)

	cpu.wdt.wtcsr |= wtcsrOVF

	cpu.routeWDTInterrupt()

	if cpu.intc.pending&(1<<isrcWDT) == 0 {
		t.Error("WDT pending bit not set after route with priority > 0")
	}
}

// --- tickPeripherals invariants ----------------------------------------

// TestTickPeripheralsAdvancesFRTAndWDTOneCycle verifies that after one
// cycle of CPU advance the peripherals observably reflect that cycle.
// FRT uses deadline scheduling, so its prescaler only advances lazily
// when a register read/write or the deadline forces a sync; the test
// triggers a sync via syncTo and asserts on the observable prescaler.
// Both FRT and WDT use deadline scheduling, so their prescalers only
// advance on register access or deadline hit; the test triggers a
// sync via syncTo and asserts on the observable prescaler.
func TestTickPeripheralsAdvancesFRTAndWDTOneCycle(t *testing.T) {
	cpu := setupIntCPU(t)

	// Enable WDT in interval mode, CKS=0 (step=2), WTCNT=0. After
	// reset FRT is already in phi/8 mode with prescaler=0.
	cpu.wdt.wtcsr = (cpu.wdt.wtcsr & 0x18) | wtcsrTME
	cpu.wdt.recomputeNextEvent(cpu.cycles)

	cpu.cycles++
	cpu.tickPeripherals()

	// Force both peripherals to reconcile so their prescalers are
	// observable. In production this happens on register access or
	// deadline hit.
	cpu.frt.syncTo(cpu.cycles)
	cpu.wdt.syncTo(cpu.cycles)

	if cpu.frt.prescaler != 1 {
		t.Errorf("FRT prescaler after 1 tick = %d, want 1", cpu.frt.prescaler)
	}
	if cpu.wdt.prescaler != 1 {
		t.Errorf("WDT prescaler after 1 tick = %d, want 1", cpu.wdt.prescaler)
	}
}

// --- resolveSource direct-call asserted=false paths --------------------

// TestResolveDIVULatchClearDirect asserts the DIVU pending bit while
// the DIVU's DVCR.OVF latch is clear, then calls resolveSource and
// verifies it reports asserted=false. Mirrors the lazy-reconcile
// contract described in processInterrupt.
func TestResolveDIVULatchClearDirect(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 5)
	cpu.intc.AssertSource(isrcDIVU)
	// DVCR stays at 0 -> OVF clear.

	_, _, asserted := cpu.resolveSource(isrcDIVU)
	if asserted {
		t.Error("resolveSource(DIVU) returned asserted=true with DVCR.OVF clear")
	}
}

// TestResolveDMAC0LatchClearDirect: pending bit set but CHCR=0, so
// neither TE nor IE are set. Sec 9.2 / resolveSource must report
// asserted=false.
func TestResolveDMAC0LatchClearDirect(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 5)
	cpu.intc.AssertSource(isrcDMAC0)
	cpu.dmac.ch[0].chcr = 0

	_, _, asserted := cpu.resolveSource(isrcDMAC0)
	if asserted {
		t.Error("resolveSource(DMAC0) returned asserted=true with CHCR=0")
	}
}

// TestResolveDMAC1LatchClearDirect: same as above for channel 1.
func TestResolveDMAC1LatchClearDirect(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 5)
	cpu.intc.AssertSource(isrcDMAC1)
	cpu.dmac.ch[1].chcr = 0

	_, _, asserted := cpu.resolveSource(isrcDMAC1)
	if asserted {
		t.Error("resolveSource(DMAC1) returned asserted=true with CHCR=0")
	}
}

// TestResolveWDTLatchClearDirect: pending bit set but WTCSR.OVF is
// clear. WDT.IRQAsserted must return false, causing resolveSource to
// report asserted=false.
func TestResolveWDTLatchClearDirect(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 5)
	cpu.intc.AssertSource(isrcWDT)
	// wtcsr stays at its reset value (OVF clear).

	_, _, asserted := cpu.resolveSource(isrcWDT)
	if asserted {
		t.Error("resolveSource(WDT) returned asserted=true with WTCSR.OVF clear")
	}
}

// TestResolveFRTLatchClearDirect: pending bit set, but FTCSR flags are
// all clear. IRQFlags() returns 0, and resolveSource must report
// asserted=false for the FRT case (interrupt.go interrupts switch).
func TestResolveFRTLatchClearDirect(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)
	cpu.intc.AssertSource(isrcFRT)
	// FTCSR left at 0, TIER left at reset (0x01).

	_, _, asserted := cpu.resolveSource(isrcFRT)
	if asserted {
		t.Error("resolveSource(FRT) returned asserted=true with FTCSR flags clear")
	}
}

// TestTickPeripheralsBothOverflowSameCall arranges FRT and WDT so that
// both overflow inside the same tickPeripherals call, and verifies both
// pending bits end up set. The FRT internal prescaler is primed to 7 so
// that the 8th cycle (this call) increments FRC from 0xFFFF to 0x0000
// (OVI). The WDT internal prescaler is primed to 1 so the 2nd cycle
// (this call, step=2) increments WTCNT from 0xFF to 0x00 (ITI).
func TestTickPeripheralsBothOverflowSameCall(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 4)
	priWDT(cpu, 6)

	// FRT: overflow enable, FRC at 0xFFFF, prescaler primed so the
	// next cycle triggers FRC++ and overflow.
	cpu.frt.tier = tierOVIE | 0x01
	cpu.frt.frc = 0xFFFF
	cpu.frt.prescaler = 7
	cpu.frt.recomputeNextEvent(cpu.cycles)
	cpu.intc.vcrd = 0x60 << 8 // OVI vector 0x60

	// WDT: interval mode, TME=1, CKS=0 (step=2), WTCNT=0xFF,
	// prescaler primed so the next cycle triggers WTCNT++ and
	// overflow.
	cpu.wdt.wtcsr = 0x18 | wtcsrTME // interval mode (WT/IT=0), CKS=0
	cpu.wdt.wtcnt = 0xFF
	cpu.wdt.prescaler = 1
	cpu.wdt.recomputeNextEvent(cpu.cycles)
	cpu.intc.vcrwdt = 0x42 << 8 // ITI vector 0x42

	cpu.cycles++
	cpu.tickPeripherals()

	if cpu.frt.ftcsr&ftcsrOVF == 0 {
		t.Error("FRT OVF flag not set after overflow tick")
	}
	if cpu.wdt.wtcsr&wtcsrOVF == 0 {
		t.Error("WDT OVF flag not set after overflow tick")
	}
	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Error("FRT pending bit not set after simultaneous overflow")
	}
	if cpu.intc.pending&(1<<isrcWDT) == 0 {
		t.Error("WDT pending bit not set after simultaneous overflow")
	}

	lvl, vec, asserted := cpu.resolveSource(isrcFRT)
	if !asserted || lvl != 4 || vec != 0x60 {
		t.Errorf("resolveSource(FRT) = (%d, 0x%X, %v), want (4, 0x60, true)", lvl, vec, asserted)
	}
	lvl, vec, asserted = cpu.resolveSource(isrcWDT)
	if !asserted || lvl != 6 || vec != 0x42 {
		t.Errorf("resolveSource(WDT) = (%d, 0x%X, %v), want (6, 0x42, true)", lvl, vec, asserted)
	}
}

// --- FRT sub-priority completion (Sec 5.2.5 Table 5.4: ICI>OCI>OVI) ---

// TestFRTSubPriorityICIBeatsOVI verifies Sec 5.2.5 Table 5.4: FRT
// sub-priority ICI (rank 2) > OVI (rank 0). With ICF and OVF both
// set and their enables on, the resolved vector is the VCRC[14:8]
// ICI vector, not the VCRD[14:8] OVI vector.
func TestFRTSubPriorityICIBeatsOVI(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 7)

	cpu.intc.vcrc = 0x40 << 8 // ICI vector 0x40
	cpu.intc.vcrd = 0x60 << 8 // OVI vector 0x60
	cpu.frt.tier |= tierICIE | tierOVIE
	cpu.frt.ftcsr |= ftcsrICF | ftcsrOVF
	cpu.intc.AssertSource(isrcFRT)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("accepted vec = 0x%X, want 0x40 (ICI should beat OVI)", cpu.pendingAddr)
	}
}

// TestFRTSubPriorityICIBeatsOCIB verifies Sec 5.2.5 Table 5.4: FRT
// sub-priority ICI (rank 2) > OCI (rank 1). With ICF and OCFB both
// set and their enables on, the resolved vector is the VCRC[14:8]
// ICI vector, not the VCRC[6:0] OCI vector.
func TestFRTSubPriorityICIBeatsOCIB(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 7)

	cpu.intc.vcrc = (0x40 << 8) | 0x50 // ICI=0x40, OCI=0x50
	cpu.frt.tier |= tierICIE | tierOCIBE
	cpu.frt.ftcsr |= ftcsrICF | ftcsrOCFB
	cpu.intc.AssertSource(isrcFRT)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("accepted vec = 0x%X, want 0x40 (ICI should beat OCIB)", cpu.pendingAddr)
	}
}

// TestFRTSubPriorityICIWinsWhenAllFlagsSet covers the worst-case tie
// from Sec 5.2.5 Table 5.4: all four FTCSR flags (ICF, OCFA, OCFB,
// OVF) set with all four TIER enables on. ICI has the highest rank
// and must be delivered.
func TestFRTSubPriorityICIWinsWhenAllFlagsSet(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 9)

	cpu.intc.vcrc = (0x40 << 8) | 0x50 // ICI=0x40, OCI=0x50
	cpu.intc.vcrd = 0x60 << 8          // OVI=0x60
	cpu.frt.tier |= tierICIE | tierOCIAE | tierOCIBE | tierOVIE
	cpu.frt.ftcsr |= ftcsrICF | ftcsrOCFA | ftcsrOCFB | ftcsrOVF
	cpu.intc.AssertSource(isrcFRT)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("accepted vec = 0x%X, want 0x40 (ICI wins all-set tie)", cpu.pendingAddr)
	}
}

// --- routeFRTInterrupts triggered-mask variants (Sec 11.2.4 / 11.2.5) ---

// TestRouteFRTInterruptsICFOnly verifies that a triggered mask of ICF
// alone (Sec 11.2.5: ICF set by input capture) routes when priority
// > 0. Companion to TestRouteFRTInterruptsAssertsPending which uses
// OCFA.
func TestRouteFRTInterruptsICFOnly(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)

	cpu.routeFRTInterrupts(ftcsrICF)

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Error("FRT pending bit not set for triggered=ICF")
	}
}

// TestRouteFRTInterruptsOCFBOnly verifies that a triggered mask of
// OCFB alone (Sec 11.2.5: OCFB set by FRC==OCRB match) routes when
// priority > 0.
func TestRouteFRTInterruptsOCFBOnly(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)

	cpu.routeFRTInterrupts(ftcsrOCFB)

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Error("FRT pending bit not set for triggered=OCFB")
	}
}

// TestRouteFRTInterruptsOVFOnly verifies that a triggered mask of OVF
// alone (Sec 11.2.5: OVF set by FRC wrap 0xFFFF -> 0x0000) routes
// when priority > 0.
func TestRouteFRTInterruptsOVFOnly(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)

	cpu.routeFRTInterrupts(ftcsrOVF)

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Error("FRT pending bit not set for triggered=OVF")
	}
}

// --- Vector-field masking (Sec 5.3.3, 9.2.5, 10.2.4) ---

// TestDIVUVCRDIVHighBitsIgnored verifies Sec 10.2.4: "only the last 7
// bits (bits 6-0) are valid" for the VCRDIV vector number. Writing
// 0xFF via the MMIO path must produce an accepted vector of 0x7F,
// not 0xFF.
func TestDIVUVCRDIVHighBitsIgnored(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 6)

	cpu.write32(0xFFFFFF08, 0x02) // OVFIE=1
	cpu.write32(0xFFFFFF0C, 0xFF) // VCRDIV with bit 7 set
	cpu.write32(0xFFFFFF00, 0)    // DVSR = 0
	cpu.write32(0xFFFFFF04, 1)    // trigger divide-by-zero

	if !cpu.processInterrupt() {
		t.Fatal("DIVU interrupt not accepted")
	}
	if cpu.pendingAddr != 0x7F {
		t.Errorf("accepted vec = 0x%X, want 0x7F (VCRDIV bits 15-7 masked)", cpu.pendingAddr)
	}
}

// TestDMACVCRDMAHighBitIgnored verifies Sec 9.2.5: DMAC transfer-end
// interrupt vector numbers are 0-127 ("Always write 0 to VC7").
// Writing 0xFF to VCRDMA0 via the MMIO path must produce an accepted
// vector of 0x7F.
func TestDMACVCRDMAHighBitIgnored(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 5)

	cpu.write32(0xFFFFFFA0, 0xFF) // VCRDMA0 with bit 7 set
	cpu.dmac.ch[0].chcr = 0x06    // TE | IE (Sec 9.2.4)

	cpu.routeDMACInterrupt(0)

	if !cpu.processInterrupt() {
		t.Fatal("DMAC interrupt not accepted")
	}
	if cpu.pendingAddr != 0x7F {
		t.Errorf("accepted vec = 0x%X, want 0x7F (VCRDMA bit 7 masked)", cpu.pendingAddr)
	}
}

// TestWDTVCRWDTLowByteDoesNotAffectITI verifies Sec 5.3.3: VCRWDT bits
// 14-8 hold the WDT interval-interrupt (ITI) vector (WITV) and bits
// 6-0 hold the BSC compare-match vector (BCMV). The BCMV field must
// not leak into the ITI vector. With VCRWDT=0x4250 (WITV=0x42,
// BCMV=0x50) the delivered ITI vector must be 0x42.
func TestWDTVCRWDTLowByteDoesNotAffectITI(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 5)

	cpu.intc.vcrwdt = 0x4250
	cpu.wdt.wtcsr |= wtcsrOVF

	cpu.routeWDTInterrupt()

	if !cpu.processInterrupt() {
		t.Fatal("WDT interrupt not accepted")
	}
	if cpu.pendingAddr != 0x42 {
		t.Errorf("accepted vec = 0x%X, want 0x42 (BCMV must not leak)", cpu.pendingAddr)
	}
}

// --- routeWDTInterrupt source isolation (Sec 5.3.1 / Table 5.5) ---

// TestRouteWDTInterruptDoesNotAssertBSCSource verifies Sec 5.3.1 and
// Table 5.5: WDT and BSC REF CMI share IPRA[7:4] but are distinct
// interrupt sources. routeWDTInterrupt must set only isrcWDT in the
// pending bitmask, never isrcBSC.
func TestRouteWDTInterruptDoesNotAssertBSCSource(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 5)
	cpu.wdt.wtcsr |= wtcsrOVF

	cpu.routeWDTInterrupt()

	if cpu.intc.pending&(1<<isrcWDT) == 0 {
		t.Error("isrcWDT pending bit not set")
	}
	if cpu.intc.pending&(1<<isrcBSC) != 0 {
		t.Error("isrcBSC pending bit must not be set by routeWDTInterrupt")
	}
}

// --- WDT mode gate in tickPeripherals (Sec 12.2.2) ---

// TestTickPeripheralsWDTWatchdogModeDoesNotRouteITI verifies Sec 12.2.2:
// in watchdog timer mode (WT/IT=1) the WTCSR.OVF flag is not set and
// the interval interrupt (ITI) is not raised; only WDTOVF / RSTCSR.
// WOVF are affected. After priming WTCNT so that one tick overflows,
// tickPeripherals must not assert isrcWDT and must not set WTCSR.OVF.
func TestTickPeripheralsWDTWatchdogModeDoesNotRouteITI(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 5)

	// Watchdog mode (WT/IT=1), TME=1, reserved bits 4-3 = 11, CKS=0
	// (step=2). Prime WTCNT=0xFF and prescaler=1 so next tick wraps.
	cpu.wdt.wtcsr = 0x18 | wtcsrWTIT | wtcsrTME
	cpu.wdt.wtcnt = 0xFF
	cpu.wdt.prescaler = 1

	cpu.tickPeripherals()

	if cpu.intc.pending&(1<<isrcWDT) != 0 {
		t.Error("isrcWDT set after watchdog-mode overflow (ITI is interval-mode only per Sec 12.2.2)")
	}
	if cpu.wdt.wtcsr&wtcsrOVF != 0 {
		t.Error("WTCSR.OVF set in watchdog mode (Sec 12.2.2: not set in watchdog timer mode)")
	}
}

// --- tickPeripherals per-peripheral isolation ---

// TestTickPeripheralsOnlyFRTOverflows verifies that when FRT overflows
// in a tick but WDT does not, only isrcFRT is asserted. Routing is
// per-peripheral independent.
func TestTickPeripheralsOnlyFRTOverflows(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)
	priWDT(cpu, 5)

	// FRT primed so the next cycle overflows.
	cpu.frt.tier = tierOVIE | 0x01
	cpu.frt.frc = 0xFFFF
	cpu.frt.prescaler = 7
	cpu.frt.recomputeNextEvent(cpu.cycles)
	cpu.intc.vcrd = 0x60 << 8

	// WDT disabled (TME=0, default after reset) so it does not tick.

	cpu.cycles++
	cpu.tickPeripherals()

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Error("isrcFRT should be set after FRT overflow")
	}
	if cpu.intc.pending&(1<<isrcWDT) != 0 {
		t.Error("isrcWDT must not be set when WDT did not overflow")
	}
}

// TestTickPeripheralsOnlyWDTOverflows verifies the symmetric case:
// WDT overflows in a tick but FRT does not; only isrcWDT is asserted.
func TestTickPeripheralsOnlyWDTOverflows(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 5)
	priWDT(cpu, 5)

	// WDT interval mode, TME=1, primed to overflow on next cycle.
	cpu.wdt.wtcsr = 0x18 | wtcsrTME
	cpu.wdt.wtcnt = 0xFF
	cpu.wdt.prescaler = 1
	cpu.wdt.recomputeNextEvent(cpu.cycles)
	cpu.intc.vcrwdt = 0x42 << 8

	// FRT left at reset: FRC=0, prescaler=0, tier=0x01 (no enables).
	// One cycle at CKS=0 (div=8) is not enough to fire any FRT event.

	cpu.cycles++
	cpu.tickPeripherals()

	if cpu.intc.pending&(1<<isrcWDT) == 0 {
		t.Error("isrcWDT should be set after WDT overflow")
	}
	if cpu.intc.pending&(1<<isrcFRT) != 0 {
		t.Error("isrcFRT must not be set when FRT did not overflow")
	}
}

// --- Priority boundaries (Sec 5.2: levels 0-15, 0 masks) ---

// TestRouteFRTInterruptsPriorityLevel1Routes verifies Sec 5.2: priority
// level 1 is the lowest non-masked level and must produce a routed,
// accepted interrupt when IMASK=0. Also verifies the accept path
// copies the level into IMASK per Sec 5.4.1 step 6.
func TestRouteFRTInterruptsPriorityLevel1Routes(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 1)

	cpu.frt.tier |= tierOCIAE
	cpu.frt.ftcsr |= ftcsrOCFA
	cpu.intc.vcrc = 0x0060

	cpu.routeFRTInterrupts(ftcsrOCFA)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted at level 1")
	}
	if cpu.pendingAddr != 0x60 {
		t.Errorf("accepted vec = 0x%X, want 0x60", cpu.pendingAddr)
	}
	if cpu.reg.IMASK() != 1 {
		t.Errorf("IMASK after accept = %d, want 1", cpu.reg.IMASK())
	}
}

// TestRouteFRTInterruptsPriorityLevel15Routes verifies Sec 5.2:
// priority level 15 (maximum) routes and sets IMASK=15.
func TestRouteFRTInterruptsPriorityLevel15Routes(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 15)

	cpu.frt.tier |= tierOCIAE
	cpu.frt.ftcsr |= ftcsrOCFA
	cpu.intc.vcrc = 0x0060

	cpu.routeFRTInterrupts(ftcsrOCFA)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted at level 15")
	}
	if cpu.reg.IMASK() != 15 {
		t.Errorf("IMASK after accept = %d, want 15", cpu.reg.IMASK())
	}
}

// HM Sec 9.3.8 + Sec 5 Table 5.4 end-to-end: DMA kick -> stall
// countdown -> Tick() completion -> CHCR.TE latch + DEI pending ->
// CPU accepts on next Clock(). Existing coverage asserts the
// pending-bit intermediate state; this test clocks through the
// full acceptance path via the CPU instead of calling resolveSource
// directly.
func TestDMACDEIAcceptedAfterStallEnds(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x100
	cpu.reg.R[15] = 0x800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x400
	bus.Write16(0x400, 0x0009) // NOP
	bus.Write32(0x100+0x4E*4, 0x600)

	bus.Write32(0x200, 0xDEADBEEF)
	cpu.dmac.ch[0].sar = 0x200
	cpu.dmac.ch[0].dar = 0x300
	cpu.dmac.ch[0].tcr = 1
	cpu.dmac.ch[0].vcrdma = 0x4E
	cpu.dmac.dmaor = 1
	priDMAC(cpu, 7)
	cpu.writeOnChip(0xFFFFFF8C, 0x5805) // IE=1, DE=1
	if !cpu.dmac.Stalling() {
		t.Fatal("setup: DMAC not stalling")
	}

	// Drain stall via Clock(). CPU should not advance while stalling.
	for cpu.dmac.Stalling() {
		cpu.Clock()
	}

	// After stall drains, TE is latched and INTC pending bit is set.
	if !cpu.dmac.IRQAsserted(0) {
		t.Fatal("DMAC ch0 not asserted after stall drained")
	}
	// Next Clock accepts the DEI.
	cpu.Clock()
	if cpu.pendingOp != popException {
		t.Fatalf("DEI not accepted after stall end; pendingOp=%d", cpu.pendingOp)
	}
	if cpu.pendingAddr != 0x4E {
		t.Errorf("accepted vec = 0x%X, want 0x4E (VCRDMA0)", cpu.pendingAddr)
	}
	if cpu.reg.IMASK() != 7 {
		t.Errorf("IMASK after accept = %d, want 7", cpu.reg.IMASK())
	}
}

// HM Sec 4.6.2: the instruction immediately after an interrupt-
// disabled instruction rejects maskable interrupts. WDT overflow
// arriving during that one-shot must be held - OVF latch stays
// set, processInterrupt consumes the inhibit and returns false; the
// next processInterrupt accepts. Existing delay-slot coverage uses
// DIVU (TestInterruptDeferredMidRTE); this test fills the WDT gap.
func TestWDTOVFLatchedThroughIntInhibit(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 5)
	cpu.intc.vcrwdt = (0x58 << 8)

	// Assert WDT with OVF latched.
	cpu.wdt.wtcsr |= wtcsrOVF
	cpu.intc.AssertSource(isrcWDT)

	cpu.intInhibit = true
	if cpu.processInterrupt() {
		t.Fatal("WDT accepted while intInhibit set; Sec 4.6.2 violated")
	}
	if cpu.intInhibit {
		t.Error("intInhibit not consumed by the blocked check")
	}
	if cpu.wdt.wtcsr&wtcsrOVF == 0 {
		t.Error("OVF latch cleared by blocked check; must be preserved")
	}

	if !cpu.processInterrupt() {
		t.Fatal("WDT not accepted on the check after inhibit drained")
	}
	if cpu.pendingAddr != 0x58 {
		t.Errorf("accepted vec = 0x%X, want 0x58", cpu.pendingAddr)
	}
}

// HM Sec 12.2.2 + erings lazy reconcile: after software clears
// WTCSR.OVF via the A5-keyed write path, the next processInterrupt
// scan sees WDT.IRQAsserted()==false and drops the INTC.pending
// bit. Existing TestResolveWDTLatchClearDirect covers the direct
// resolveSource query; this test drives through processInterrupt's
// scan loop.
func TestWDTClearingOVFDeassertsINTC(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 5)
	cpu.intc.vcrwdt = (0x58 << 8)

	// Overflow + route. IMASK is 0 so the interrupt would normally accept.
	cpu.wdt.wtcsr |= wtcsrOVF
	cpu.intc.AssertSource(isrcWDT)

	if cpu.intc.pending&(1<<isrcWDT) == 0 {
		t.Fatal("setup: WDT pending bit not set")
	}

	// Raise IMASK above WDT level so the interrupt is seen but blocked,
	// then clear OVF via the A5-keyed WriteWord path (software clear protocol
	// per Sec 12.2.2).
	cpu.reg.SetIMASK(6)
	cpu.wdt.WriteWord(0xFFFFFE80, 0xA500) // A5 key, OVF=0 clears the latch
	if cpu.wdt.wtcsr&wtcsrOVF != 0 {
		t.Fatalf("setup: OVF not cleared by A5/00 write, wtcsr=0x%02X", cpu.wdt.wtcsr)
	}

	// The next processInterrupt must reconcile the stale pending bit.
	if cpu.processInterrupt() {
		t.Fatal("processInterrupt accepted WDT despite OVF cleared")
	}
	if cpu.intc.pending&(1<<isrcWDT) != 0 {
		t.Errorf("INTC.pending[WDT] not cleared after OVF software clear: pending=0x%04X",
			cpu.intc.pending)
	}
}

// HM Sec 11.2.3 / 11.2.4 / Sec 5.2.5 Table 5.4: input capture
// signal -> ICF latch -> ICIE gate -> IPRB priority -> VCRC[14:8]
// vector -> CPU acceptance. Covers the path driven by the VBL
// MINIT/SINIT hook: CPU.FRTInputCapture (cpu.go:975) -> FRT.InputCapture
// -> INTC.AssertSource -> processInterrupt -> acceptInterrupt. Existing
// coverage stops at the pending-bit assertion; this test drives a
// full Clock() and asserts the exception dispatch is scheduled with
// the VCRC ICI vector.
func TestFRTInputCaptureEndToEndAccepted(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 0xB)
	// VCRC: ICI vector in bits 14-8 = 0x4A.
	cpu.intc.vcrc = (0x4A << 8)
	cpu.frt.tier = tierICIE | 0x01

	cpu.FRTInputCapture()
	cpu.Clock()

	if cpu.pendingOp != popException {
		t.Fatalf("pendingOp = %d, want popException", cpu.pendingOp)
	}
	if cpu.pendingAddr != 0x4A {
		t.Errorf("pendingAddr = 0x%X, want 0x4A (VCRC ICI)", cpu.pendingAddr)
	}
	if cpu.reg.IMASK() != 0xB {
		t.Errorf("IMASK after accept = %d, want 0xB", cpu.reg.IMASK())
	}
}

// HM Sec 5.2.5 / Table 5.4 plus README "INTC" sub-priority:
// inside the FRT source, ICI > OCIA > OCIB > OVI. Existing
// TestFRTSubPriorityICIBeatsOCIA covers the pure resolveSource
// query; this test drives the same scenario through Clock() to
// confirm the accept path picks the ICI vector when both flags
// are latched at the same IPRB priority level.
func TestFRTInputCapturePriorityOverrideByOCIA(t *testing.T) {
	cpu := setupIntCPU(t)
	priFRT(cpu, 7)
	// VCRC: ICI vector 0x51, OCI vector 0x52 - different so the
	// acceptance result is unambiguous.
	cpu.intc.vcrc = (0x51 << 8) | 0x52
	cpu.frt.tier = tierICIE | tierOCIAE | 0x01

	// OCFA already latched (compare match happened earlier).
	cpu.frt.ftcsr |= ftcsrOCFA
	cpu.intc.AssertSource(isrcFRT)
	// Then input capture fires.
	cpu.FRTInputCapture()

	cpu.Clock()

	if cpu.pendingOp != popException {
		t.Fatalf("pendingOp = %d, want popException", cpu.pendingOp)
	}
	if cpu.pendingAddr != 0x51 {
		t.Errorf("pendingAddr = 0x%X, want 0x51 (ICI beats OCIA)", cpu.pendingAddr)
	}
}
