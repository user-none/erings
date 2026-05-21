// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// These tests cover the INTC priority model fixes identified in the
// SH-2 audit: fixed tie-break order for equal-level sources (A5), and
// multi-source latch preservation across preempt (A6). All asserts
// use resolveSource/processInterrupt against peripheral-side latches.

// setupIntCPU returns a CPU with IMASK=0, stack configured, and a
// handler at a simple address. Callers assert peripheral flags after.
func setupIntCPU(t *testing.T) *CPU {
	t.Helper()
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	cpu.reg.PC = 0x0400
	return cpu
}

// priDIVU/priDMAC set IPRA so that DIVU, DMAC0, DMAC1 all land on the
// given level (DMAC0 and DMAC1 share the IPRA[11:8] field).
func priDIVU(cpu *CPU, level uint8) {
	cpu.intc.ipra = (cpu.intc.ipra &^ 0xF000) | (uint16(level&0xF) << 12)
}
func priDMAC(cpu *CPU, level uint8) {
	cpu.intc.ipra = (cpu.intc.ipra &^ 0x0F00) | (uint16(level&0xF) << 8)
}
func priFRT(cpu *CPU, level uint8) {
	cpu.intc.iprb = (cpu.intc.iprb &^ 0x0F00) | (uint16(level&0xF) << 8)
}

// assertDMAC simulates the peripheral transfer-end latching CHCR.TE
// (bit 1) with CHCR.IE (bit 2) enabled, and notifying INTC.
func assertDMAC(cpu *CPU, ch int, vec uint16) {
	cpu.dmac.ch[ch].chcr |= 0x06
	cpu.dmac.ch[ch].vcrdma = uint32(vec) & 0x7F
	if ch == 0 {
		cpu.intc.AssertSource(isrcDMAC0)
	} else {
		cpu.intc.AssertSource(isrcDMAC1)
	}
}

// assertFRTOCI asserts an OCIA interrupt on the FRT with a given
// output-compare vector. VCRC bits 6-0 hold the OCI vector.
func assertFRTOCI(cpu *CPU, vec uint16) {
	cpu.frt.tier |= tierOCIAE
	cpu.frt.ftcsr |= ftcsrOCFA
	cpu.intc.vcrc = (cpu.intc.vcrc &^ 0x007F) | (vec & 0x7F)
	cpu.intc.AssertSource(isrcFRT)
}

// TestEqualLevelDIVUBeatsDMAC0 verifies manual Sec 5.2.5 Table 5.4:
// when DIVU and DMAC0 tie on priority level, DIVU wins.
func TestEqualLevelDIVUBeatsDMAC0(t *testing.T) {
	cpu := setupIntCPU(t)

	priDIVU(cpu, 5)
	priDMAC(cpu, 5)

	assertDMAC(cpu, 0, 0x50)
	assertDIVU(cpu, 5, 0x40)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("accepted vec = 0x%X, want 0x40 (DIVU should beat DMAC0 on tie)", cpu.pendingAddr)
	}
}

// TestEqualLevelDMAC0BeatsDMAC1 verifies DMAC channel ordering:
// ch0 > ch1 at equal level (per manual Sec 5.3.1 note on shared IPRA bits).
func TestEqualLevelDMAC0BeatsDMAC1(t *testing.T) {
	cpu := setupIntCPU(t)

	priDMAC(cpu, 7)

	assertDMAC(cpu, 1, 0x60)
	assertDMAC(cpu, 0, 0x50)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("accepted vec = 0x%X, want 0x50 (DMAC0 should beat DMAC1 on tie)", cpu.pendingAddr)
	}
}

// TestEqualLevelDIVUBeatsFRT verifies manual ordering across
// different IPR registers: DIVU (IPRA high) beats FRT (IPRB) at
// equal priority.
func TestEqualLevelDIVUBeatsFRT(t *testing.T) {
	cpu := setupIntCPU(t)

	priDIVU(cpu, 9)
	priFRT(cpu, 9)

	assertFRTOCI(cpu, 0x70)
	assertDIVU(cpu, 9, 0x30)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x30 {
		t.Errorf("accepted vec = 0x%X, want 0x30 (DIVU should beat FRT on tie)", cpu.pendingAddr)
	}
}

// TestLostRequestPreservedOnPreempt covers audit item A6. When a
// higher-priority source preempts a lower-priority source, the lower
// source's latch must remain asserted; after the high handler clears
// its own latch, the low source must be picked up next.
func TestLostRequestPreservedOnPreempt(t *testing.T) {
	cpu := setupIntCPU(t)

	priDIVU(cpu, 10)
	priDMAC(cpu, 5)

	assertDMAC(cpu, 0, 0x50)  // level 5
	assertDIVU(cpu, 10, 0x40) // level 10

	// Level 10 wins.
	if !cpu.processInterrupt() {
		t.Fatal("first accept failed")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("first accept vec = 0x%X, want 0x40", cpu.pendingAddr)
	}

	// After accept, IMASK is 10; DMAC0 at level 5 still latched
	// but held pending.
	if !cpu.dmac.IRQAsserted(0) {
		t.Error("DMAC0 latch should remain asserted during higher handler")
	}

	// Simulate DIVU handler: clear DIVU latch, drop IMASK back to 0
	// (as RTE would restore).
	clearDIVU(cpu)
	cpu.reg.SR = 0

	// Drain the exception dispatch so pendingOp returns to popNone
	// and processInterrupt will be called by the outer Clock loop.
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}

	// Now DMAC0 at level 5 should fire.
	if !cpu.processInterrupt() {
		t.Fatal("DMAC0 interrupt should be delivered after DIVU handler clears")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("second accept vec = 0x%X, want 0x50 (DMAC0 preserved across preempt)", cpu.pendingAddr)
	}
}

// TestLatchClearReconciliation verifies lazy bitmask cleanup: if a
// peripheral's latch is cleared (simulating a handler write) without
// notifying INTC, the next processInterrupt scans the bit, sees the
// latch deasserted, and clears the bit.
func TestLatchClearReconciliation(t *testing.T) {
	cpu := setupIntCPU(t)

	priFRT(cpu, 8)
	assertFRTOCI(cpu, 0x60)

	if cpu.intc.pending&(1<<isrcFRT) == 0 {
		t.Fatal("FRT bit should be set")
	}

	// Handler clears FTCSR without touching intc.pending.
	cpu.frt.ftcsr &^= ftcsrOCFA

	// IMASK blocks delivery so we can observe the reconcile path.
	cpu.reg.SetIMASK(15)
	if cpu.processInterrupt() {
		t.Error("processInterrupt should not deliver (IMASK=15)")
	}
	if cpu.intc.pending&(1<<isrcFRT) != 0 {
		t.Error("FRT bit should be reconciled to 0 after latch cleared")
	}
}

// TestNMIOverridesIMASK15 verifies NMI delivery even with IMASK=15.
// NMI is unmaskable.
func TestNMIOverridesIMASK15(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	cpu.reg.SetIMASK(15)
	cpu.reg.PC = 0x0200

	// Handler at VBR + 11*4 = 0x012C
	bus.Write32(0x012C, 0x00000500)

	cpu.NMI()
	if !cpu.processInterrupt() {
		t.Fatal("NMI not accepted with IMASK=15")
	}
	if cpu.pendingAddr != uint32(vecNMI) {
		t.Errorf("accepted vec = 0x%X, want 0x%X", cpu.pendingAddr, vecNMI)
	}
}

// TestIRLCoexistsWithInternalHigher verifies IRL beats lower-level
// internal source when its level exceeds the internal winner.
func TestIRLCoexistsWithInternalHigher(t *testing.T) {
	cpu := setupIntCPU(t)

	priDIVU(cpu, 3)
	assertDIVU(cpu, 3, 0x40) // level 3

	cpu.SetIRL(8, 0x50) // level 8 wins

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("accepted vec = 0x%X, want 0x50 (IRL level 8 > DIVU level 3)", cpu.pendingAddr)
	}
}

// TestIRLCoexistsWithInternalLower verifies internal source beats IRL
// when internal level exceeds IRL.
func TestIRLCoexistsWithInternalLower(t *testing.T) {
	cpu := setupIntCPU(t)

	priDIVU(cpu, 10)
	assertDIVU(cpu, 10, 0x40) // level 10

	cpu.SetIRL(3, 0x50) // level 3 loses

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x40 {
		t.Errorf("accepted vec = 0x%X, want 0x40 (DIVU level 10 > IRL level 3)", cpu.pendingAddr)
	}
}

// TestIMaskBoundarySweepDIVU walks every priority level 1..15 and
// verifies the IMASK > comparison at every boundary per Sec 5.4.1 step
// 3: "If the request priority level is equal to or less than the level
// set in I3-I0, the request is held pending. If the request priority
// level is higher than the level in bits I3-I0, the interrupt controller
// accepts the interrupt ..."
func TestIMaskBoundarySweepDIVU(t *testing.T) {
	for level := uint8(1); level <= 15; level++ {
		t.Run("level_"+string(rune('0'+level/10))+string(rune('0'+level%10)), func(t *testing.T) {
			// IMASK = level - 1: must accept.
			cpu := setupIntCPU(t)
			cpu.reg.SetIMASK(level - 1)
			assertDIVU(cpu, level, 0x40)
			if !cpu.processInterrupt() {
				t.Errorf("level=%d IMASK=%d: expected accept", level, level-1)
			}

			// IMASK = level: must reject, latch preserved.
			cpu = setupIntCPU(t)
			cpu.reg.SetIMASK(level)
			assertDIVU(cpu, level, 0x40)
			if cpu.processInterrupt() {
				t.Errorf("level=%d IMASK=%d: expected reject", level, level)
			}
			if !cpu.divu.IRQAsserted() {
				t.Errorf("level=%d IMASK=%d: DIVU latch dropped on reject", level, level)
			}

			// IMASK = 15: must reject for every level < 16.
			cpu = setupIntCPU(t)
			cpu.reg.SetIMASK(15)
			assertDIVU(cpu, level, 0x40)
			if cpu.processInterrupt() {
				t.Errorf("level=%d IMASK=15: expected reject (only NMI bypasses)", level)
			}
		})
	}
}

// TestPriorityZeroMaskedAllSources sweeps every modeled non-NMI source
// at priority 0 and confirms none fires. Manual Sec 5.2: "Giving an
// interrupt a priority level of 0 masks it."
func TestPriorityZeroMaskedAllSources(t *testing.T) {
	cases := []struct {
		name   string
		assert func(cpu *CPU)
	}{
		{"DIVU", func(cpu *CPU) {
			cpu.divu.dvcr = 0x03
			cpu.intc.AssertSource(isrcDIVU)
		}},
		{"DMAC0", func(cpu *CPU) {
			cpu.dmac.ch[0].chcr = 0x06
			cpu.intc.AssertSource(isrcDMAC0)
		}},
		{"DMAC1", func(cpu *CPU) {
			cpu.dmac.ch[1].chcr = 0x06
			cpu.intc.AssertSource(isrcDMAC1)
		}},
		{"FRT", func(cpu *CPU) {
			cpu.frt.tier |= tierOCIAE
			cpu.frt.ftcsr |= ftcsrOCFA
			cpu.intc.AssertSource(isrcFRT)
		}},
		{"WDT", func(cpu *CPU) {
			cpu.wdt.wtcsr |= wtcsrOVF
			cpu.intc.AssertSource(isrcWDT)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cpu := setupIntCPU(t)
			// IPRA/IPRB left at 0 (reset state) -> every priority is 0.
			tc.assert(cpu)
			if cpu.processInterrupt() {
				t.Errorf("%s with priority 0 accepted; should be masked (Sec 5.2)", tc.name)
			}
		})
	}
}

// TestPriorityDowngradeToZeroMasks confirms priority is resolved at
// accept time, not at assertion time: asserting at L=5 and later writing
// the priority field to 0 must prevent acceptance.
func TestPriorityDowngradeToZeroMasks(t *testing.T) {
	cpu := setupIntCPU(t)
	priDIVU(cpu, 5)
	assertDIVU(cpu, 5, 0x40)

	// Software downgrades DIVU priority to 0 before the handler runs.
	priDIVU(cpu, 0)

	if cpu.processInterrupt() {
		t.Error("processInterrupt accepted with priority downgraded to 0")
	}
}

// TestTieBreakDMAC0BeatsWDT confirms DMAC0 wins a tie against WDT per
// Table 5.4 default-priority ordering.
func TestTieBreakDMAC0BeatsWDT(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 6)
	priWDT(cpu, 6)

	cpu.wdt.wtcsr |= wtcsrOVF
	cpu.intc.vcrwdt = 0x42 << 8
	cpu.intc.AssertSource(isrcWDT)

	assertDMAC(cpu, 0, 0x50)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("accepted vec = 0x%X, want 0x50 (DMAC0 should beat WDT on tie)", cpu.pendingAddr)
	}
}

// TestTieBreakDMAC1BeatsWDT: DMAC1 > WDT at equal level (Table 5.4).
func TestTieBreakDMAC1BeatsWDT(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 6)
	priWDT(cpu, 6)

	cpu.wdt.wtcsr |= wtcsrOVF
	cpu.intc.vcrwdt = 0x42 << 8
	cpu.intc.AssertSource(isrcWDT)

	assertDMAC(cpu, 1, 0x60)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x60 {
		t.Errorf("accepted vec = 0x%X, want 0x60 (DMAC1 should beat WDT on tie)", cpu.pendingAddr)
	}
}

// TestTieBreakDMAC0BeatsFRT: DMAC0 > FRT at equal level (Table 5.4).
func TestTieBreakDMAC0BeatsFRT(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 8)
	priFRT(cpu, 8)

	assertFRTOCI(cpu, 0x70)
	assertDMAC(cpu, 0, 0x50)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x50 {
		t.Errorf("accepted vec = 0x%X, want 0x50 (DMAC0 should beat FRT on tie)", cpu.pendingAddr)
	}
}

// TestTieBreakDMAC1BeatsFRT: DMAC1 > FRT at equal level (Table 5.4).
func TestTieBreakDMAC1BeatsFRT(t *testing.T) {
	cpu := setupIntCPU(t)
	priDMAC(cpu, 8)
	priFRT(cpu, 8)

	assertFRTOCI(cpu, 0x70)
	assertDMAC(cpu, 1, 0x60)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x60 {
		t.Errorf("accepted vec = 0x%X, want 0x60 (DMAC1 should beat FRT on tie)", cpu.pendingAddr)
	}
}

// TestTieBreakWDTBeatsFRT: WDT > FRT at equal level (Table 5.4).
func TestTieBreakWDTBeatsFRT(t *testing.T) {
	cpu := setupIntCPU(t)
	priWDT(cpu, 4)
	priFRT(cpu, 4)

	assertFRTOCI(cpu, 0x70)

	cpu.wdt.wtcsr |= wtcsrOVF
	cpu.intc.vcrwdt = 0x42 << 8
	cpu.intc.AssertSource(isrcWDT)

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if cpu.pendingAddr != 0x42 {
		t.Errorf("accepted vec = 0x%X, want 0x42 (WDT should beat FRT on tie)", cpu.pendingAddr)
	}
}

// TestMultiSourceAllMaskedPreserved verifies that when every pending
// source is masked by IMASK=15, all latches remain set. Multi-source
// companion to TestCheckInterruptMasked.
func TestMultiSourceAllMaskedPreserved(t *testing.T) {
	cpu := setupIntCPU(t)
	cpu.reg.SetIMASK(15)

	priDIVU(cpu, 5)
	assertDIVU(cpu, 5, 0x40)
	priFRT(cpu, 7)
	assertFRTOCI(cpu, 0x50)
	priDMAC(cpu, 6)
	assertDMAC(cpu, 0, 0x60)

	if cpu.processInterrupt() {
		t.Fatal("processInterrupt accepted despite IMASK=15")
	}
	for _, src := range []IntSource{isrcDIVU, isrcFRT, isrcDMAC0} {
		if cpu.intc.pending&(1<<src) == 0 {
			t.Errorf("pending bit for source %d cleared after masked check", src)
		}
	}
}

// TestIRLLevelZeroIgnored verifies Sec 5.2 level-0 masking for IRL.
func TestIRLLevelZeroIgnored(t *testing.T) {
	cpu := setupIntCPU(t)
	cpu.SetIRL(0, 0x40)
	if cpu.processInterrupt() {
		t.Error("IRL level 0 accepted; should be masked (Sec 5.2)")
	}
}

// TestIRLClearDeasserts verifies ClearIRL() drops the external request.
func TestIRLClearDeasserts(t *testing.T) {
	cpu := setupIntCPU(t)
	cpu.SetIRL(5, 0x40)
	cpu.ClearIRL()
	if cpu.processInterrupt() {
		t.Error("IRL still fires after ClearIRL()")
	}
}

// TestIRLStaysAssertedAcrossAccept verifies the level-triggered nature
// of IRL: accepting the interrupt must not clear irlLevel/irlVec in the
// CPU state. The SCU is responsible for lowering the line via ClearIRL.
// Sec 5.6 confirms the level is held at the pin until cleared.
func TestIRLStaysAssertedAcrossAccept(t *testing.T) {
	cpu := setupIntCPU(t)
	cpu.SetIRL(5, 0x40)

	if !cpu.processInterrupt() {
		t.Fatal("IRL not accepted")
	}
	if cpu.irlLevel != 5 {
		t.Errorf("irlLevel = %d after accept, want 5 (level-triggered)", cpu.irlLevel)
	}
	if cpu.irlVec != 0x40 {
		t.Errorf("irlVec = 0x%X after accept, want 0x40", cpu.irlVec)
	}
}

// TestIRLAckCalledExactlyOnce verifies the irlAck callback fires once
// per accept.
func TestIRLAckCalledExactlyOnce(t *testing.T) {
	cpu := setupIntCPU(t)

	count := 0
	cpu.SetIRLAck(func() { count++ })
	cpu.SetIRL(8, 0x50)

	if !cpu.processInterrupt() {
		t.Fatal("IRL not accepted")
	}
	if count != 1 {
		t.Errorf("irlAck called %d times, want 1", count)
	}
}

// TestIRLAckNotCalledOnInternalWin verifies irlAck fires only when IRL
// wins the priority race.
func TestIRLAckNotCalledOnInternalWin(t *testing.T) {
	cpu := setupIntCPU(t)

	count := 0
	cpu.SetIRLAck(func() { count++ })

	priDIVU(cpu, 10)
	assertDIVU(cpu, 10, 0x40)
	cpu.SetIRL(3, 0x50) // loses to DIVU level 10

	if !cpu.processInterrupt() {
		t.Fatal("no interrupt accepted")
	}
	if count != 0 {
		t.Errorf("irlAck called %d times on internal win, want 0", count)
	}
}

// TestIRLAckNotCalledWhenMasked verifies irlAck is silent when IMASK
// blocks acceptance.
func TestIRLAckNotCalledWhenMasked(t *testing.T) {
	cpu := setupIntCPU(t)

	count := 0
	cpu.SetIRLAck(func() { count++ })
	cpu.reg.SetIMASK(4)
	cpu.SetIRL(3, 0x50) // level 3 <= IMASK=4 -> reject

	if cpu.processInterrupt() {
		t.Fatal("masked IRL was accepted")
	}
	if count != 0 {
		t.Errorf("irlAck called %d times while masked, want 0", count)
	}
}

// TestIRLAckNilSafe verifies IRL accept does not panic when no ack
// callback has been installed.
func TestIRLAckNilSafe(t *testing.T) {
	cpu := setupIntCPU(t)
	// Do not install an ack callback; irlAck stays nil.
	cpu.SetIRL(7, 0x30)
	if !cpu.processInterrupt() {
		t.Fatal("IRL not accepted")
	}
}

// Manual Sec 5.3.8: SetNMIL toggles only bit 15 of ICR. Other writable bits
// (NMIE, VECMD) must be preserved across SetNMIL transitions. This covers
// the "pin-level update does not disturb config" contract.
func TestINTCSetNMILPreservesOtherBits(t *testing.T) {
	var intc INTC
	intc.Reset()
	// NMIL=1 from reset. Set NMIE and VECMD.
	intc.Write(0xFFFFFEE0, 0x0101)

	intc.SetNMIL(false)
	if intc.Read(0xFFFFFEE0)&0x8000 != 0 {
		t.Error("SetNMIL(false) did not clear bit 15")
	}
	if intc.Read(0xFFFFFEE0)&0x0101 != 0x0101 {
		t.Errorf("SetNMIL(false) disturbed NMIE/VECMD: ICR = 0x%04X", intc.Read(0xFFFFFEE0))
	}

	intc.SetNMIL(true)
	if intc.Read(0xFFFFFEE0)&0x8000 == 0 {
		t.Error("SetNMIL(true) did not set bit 15")
	}
	if intc.Read(0xFFFFFEE0)&0x0101 != 0x0101 {
		t.Errorf("SetNMIL(true) disturbed NMIE/VECMD: ICR = 0x%04X", intc.Read(0xFFFFFEE0))
	}
}

// Manual Sec 5.3.8: SetNMIL(false) followed by SetNMIL(false) must keep
// NMIL at 0 - the operation is idempotent for each level.
func TestINTCSetNMILIdempotent(t *testing.T) {
	var intc INTC
	intc.Reset()
	intc.SetNMIL(false)
	intc.SetNMIL(false)
	if intc.Read(0xFFFFFEE0)&0x8000 != 0 {
		t.Error("NMIL not cleared after two SetNMIL(false) calls")
	}
	intc.SetNMIL(true)
	intc.SetNMIL(true)
	if intc.Read(0xFFFFFEE0)&0x8000 == 0 {
		t.Error("NMIL not set after two SetNMIL(true) calls")
	}
}
