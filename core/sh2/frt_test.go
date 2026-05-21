// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import (
	"math"
	"testing"
)

// tick advances the FRT by n CPU cycles relative to its current lastSync
// anchor. The production code path uses CPU.tickPeripherals ->
// FRT.fireDue on deadline; tests drive syncTo directly for readable
// step-by-step exercise.
func tick(f *FRT, n int) uint8 {
	return f.syncTo(f.lastSync + uint64(n))
}

func TestFRTInitValues(t *testing.T) {
	var frt FRT
	frt.Reset()

	if frt.frc != 0 {
		t.Errorf("FRC = 0x%04X, want 0", frt.frc)
	}
	if frt.ocra != 0xFFFF {
		t.Errorf("OCRA = 0x%04X, want 0xFFFF", frt.ocra)
	}
	if frt.ocrb != 0xFFFF {
		t.Errorf("OCRB = 0x%04X, want 0xFFFF", frt.ocrb)
	}
	if frt.tier != 0x01 {
		t.Errorf("TIER = 0x%02X, want 0x01", frt.tier)
	}
	if frt.tocr != 0xE0 {
		t.Errorf("TOCR = 0x%02X, want 0xE0", frt.tocr)
	}
}

func TestFRTTickBasic(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Default CKS=0 means phi/8 divider
	// After 8 cycles, FRC should increment by 1
	tick(&frt, 7)
	if frt.frc != 0 {
		t.Errorf("FRC after 7 cycles = %d, want 0", frt.frc)
	}

	tick(&frt, 1)
	if frt.frc != 1 {
		t.Errorf("FRC after 8 cycles = %d, want 1", frt.frc)
	}
}

func TestFRTTickClockDividers(t *testing.T) {
	tests := []struct {
		name    string
		cks     uint8
		cycles  int
		wantFRC uint16
	}{
		{"phi/8", 0, 16, 2},
		{"phi/32", 1, 64, 2},
		{"phi/128", 2, 256, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var frt FRT
			frt.Reset()
			frt.tcr = tt.cks

			tick(&frt, tt.cycles)
			if frt.frc != tt.wantFRC {
				t.Errorf("FRC after %d cycles with CKS=%d = %d, want %d",
					tt.cycles, tt.cks, frt.frc, tt.wantFRC)
			}
		})
	}
}

func TestFRTCompareMatchA(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 3
	frt.tier = tierOCIAE | 0x01 // enable OCIA interrupt

	// Tick until FRC reaches 3 (3 * 8 = 24 cycles)
	triggered := uint8(0)
	for i := 0; i < 24; i++ {
		triggered |= tick(&frt, 1)
	}

	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA not set after compare match A")
	}
	if triggered&ftcsrOCFA == 0 {
		t.Error("OCIA interrupt not triggered")
	}
}

func TestFRTCompareMatchAClear(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 2
	frt.ftcsr = ftcsrCCLRA // Enable counter clear on compare A

	// Tick 24 cycles (enough for FRC to reach 2, then clear)
	for i := 0; i < 24; i++ {
		tick(&frt, 1)
	}

	// FRC should have been cleared after reaching 2 and continued
	if frt.frc >= 2 {
		t.Errorf("FRC = %d, expected < 2 (should have been cleared on match)", frt.frc)
	}
}

func TestFRTCompareMatchB(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocrb = 2
	frt.tier = tierOCIBE | 0x01 // enable OCIB interrupt

	triggered := uint8(0)
	for i := 0; i < 16; i++ {
		triggered |= tick(&frt, 1)
	}

	if frt.ftcsr&ftcsrOCFB == 0 {
		t.Error("OCFB not set after compare match B")
	}
	if triggered&ftcsrOCFB == 0 {
		t.Error("OCIB interrupt not triggered")
	}
}

func TestFRTOverflow(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.frc = 0xFFFE
	frt.tier = tierOVIE | 0x01 // enable overflow interrupt

	// Need 2 FRC increments to overflow: 0xFFFE -> 0xFFFF -> 0x0000
	triggered := uint8(0)
	for i := 0; i < 16; i++ {
		triggered |= tick(&frt, 1)
	}

	if frt.ftcsr&ftcsrOVF == 0 {
		t.Error("OVF not set after counter overflow")
	}
	if triggered&ftcsrOVF == 0 {
		t.Error("OVI interrupt not triggered")
	}
	if frt.frc != 0 {
		t.Errorf("FRC after overflow = 0x%04X, want 0", frt.frc)
	}
}

func TestFRTInputCapture(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.frc = 0x1234
	frt.tier = tierICIE | 0x01 // enable input capture interrupt

	enabled := frt.InputCapture()

	if frt.icr != 0x1234 {
		t.Errorf("ICR = 0x%04X, want 0x1234", frt.icr)
	}
	if frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF not set after input capture")
	}
	if !enabled {
		t.Error("InputCapture() returned false, want true (ICIE enabled)")
	}
}

func TestFRTInputCaptureDisabled(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.frc = 0x5678

	enabled := frt.InputCapture()

	if frt.icr != 0x5678 {
		t.Errorf("ICR = 0x%04X, want 0x5678", frt.icr)
	}
	if frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF should still be set even when interrupt is disabled")
	}
	if enabled {
		t.Error("InputCapture() returned true, want false (ICIE disabled)")
	}
}

func TestFRTRegisterReadFRC(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.frc = 0xABCD

	// Reading high byte should latch low byte into temp
	h := frt.Read(0xFFFFFE12)
	if h != 0xAB {
		t.Errorf("FRC H = 0x%02X, want 0xAB", h)
	}
	l := frt.Read(0xFFFFFE13)
	if l != 0xCD {
		t.Errorf("FRC L = 0x%02X, want 0xCD", l)
	}
}

func TestFRTRegisterWriteFRC(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Write high byte to temp, then low byte applies both
	frt.Write(0xFFFFFE12, 0x12)
	frt.Write(0xFFFFFE13, 0x34)

	if frt.frc != 0x1234 {
		t.Errorf("FRC = 0x%04X, want 0x1234", frt.frc)
	}
}

func TestFRTRegisterOCR(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Default TOCR.OCRS=0, so OCR address maps to OCRA
	frt.Write(0xFFFFFE14, 0xAA)
	frt.Write(0xFFFFFE15, 0xBB)
	if frt.ocra != 0xAABB {
		t.Errorf("OCRA = 0x%04X, want 0xAABB", frt.ocra)
	}

	// Set TOCR.OCRS=1, now OCR address maps to OCRB
	frt.Write(0xFFFFFE17, 0x10) // set OCRS bit
	frt.Write(0xFFFFFE14, 0xCC)
	frt.Write(0xFFFFFE15, 0xDD)
	if frt.ocrb != 0xCCDD {
		t.Errorf("OCRB = 0x%04X, want 0xCCDD", frt.ocrb)
	}
	// OCRA should be unchanged
	if frt.ocra != 0xAABB {
		t.Errorf("OCRA after OCRB write = 0x%04X, want 0xAABB", frt.ocra)
	}
}

func TestFRTRegisterOCRRead(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 0x1122
	frt.ocrb = 0x3344

	// TOCR.OCRS=0 -> reads OCRA
	h := frt.Read(0xFFFFFE14)
	l := frt.Read(0xFFFFFE15)
	if h != 0x11 || l != 0x22 {
		t.Errorf("OCR read (OCRA) = 0x%02X%02X, want 0x1122", h, l)
	}

	// Set OCRS=1 -> reads OCRB
	frt.Write(0xFFFFFE17, 0x10)
	h = frt.Read(0xFFFFFE14)
	l = frt.Read(0xFFFFFE15)
	if h != 0x33 || l != 0x44 {
		t.Errorf("OCR read (OCRB) = 0x%02X%02X, want 0x3344", h, l)
	}
}

func TestFRTFTCSRClearFlags(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Set all flags
	frt.ftcsr = 0x8F

	// Write to clear only ICF (bit 7) - write 0 to clear, 1 to keep
	// To clear bit 7 and keep bits 3-1: write 0x0E
	frt.Write(0xFFFFFE11, 0x0E)
	if frt.ftcsr&ftcsrICF != 0 {
		t.Error("ICF should be cleared")
	}
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA should still be set")
	}
}

func TestFRTTIERMask(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.Write(0xFFFFFE10, 0xFF)
	// TIER writable bits: 7, 3-1, bit 0 always 1
	if frt.tier != 0x8F {
		t.Errorf("TIER = 0x%02X, want 0x8F", frt.tier)
	}
}

func TestFRTTCRMask(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.Write(0xFFFFFE16, 0xFF)
	// TCR bits 7 (IEDGA) and 1-0 (CKS) writable, bits 6-2 reserved
	if frt.tcr != 0x83 {
		t.Errorf("TCR = 0x%02X, want 0x83", frt.tcr)
	}
}

func TestFRTTOCRMask(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.Write(0xFFFFFE17, 0xFF)
	// TOCR bit 4 writable, upper 3 always 1
	if frt.tocr != 0xF0 {
		t.Errorf("TOCR = 0x%02X, want 0xF0", frt.tocr)
	}
}

func TestFRTICRReadOnly(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.icr = 0x5678

	// Writing to ICR addresses should have no effect
	frt.Write(0xFFFFFE18, 0xAA)
	frt.Write(0xFFFFFE19, 0xBB)

	if frt.icr != 0x5678 {
		t.Errorf("ICR changed after write = 0x%04X, want 0x5678", frt.icr)
	}
}

func TestFRTExternalClock(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.tcr = 3 // External clock

	// Ticking should not advance the counter
	tick(&frt, 1000)
	if frt.frc != 0 {
		t.Errorf("FRC with external clock = %d, want 0", frt.frc)
	}
}

func TestFRTNoTriggerWithoutEnable(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 1
	// TIER: no enables set (just bit 0 which is reserved)

	triggered := uint8(0)
	for i := 0; i < 8; i++ {
		triggered |= tick(&frt, 1)
	}

	// Flag should be set but trigger should be empty
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA flag not set")
	}
	if triggered != 0 {
		t.Errorf("triggered = 0x%02X, want 0 (interrupts disabled)", triggered)
	}
}

// Manual Sec 11.2.4 bit 7 (ICIE): with ICIE=0, an ICF setting event must
// not assert the ICI interrupt request. InputCapture() returns the enable
// state and callers use it to gate routing.
func TestFRTTIERICIEDisabledNoTrigger(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.frc = 0x4242
	// TIER default after Reset is 0x01 (ICIE=0).
	enabled := frt.InputCapture()

	if frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF should be set by InputCapture regardless of ICIE")
	}
	if enabled {
		t.Error("InputCapture returned true with ICIE=0")
	}
}

// Manual Sec 11.2.4 bit 1 (OVIE): with OVIE=0, the overflow flag is still
// latched on wrap but the sync path does not include OVF in its triggered
// bitmask.
func TestFRTTIEROVIEDisabledNoTrigger(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.frc = 0xFFFE
	// No OVIE set.
	triggered := uint8(0)
	for i := 0; i < 16; i++ {
		triggered |= tick(&frt, 1)
	}
	if frt.ftcsr&ftcsrOVF == 0 {
		t.Error("OVF should latch on overflow regardless of OVIE")
	}
	if triggered&ftcsrOVF != 0 {
		t.Errorf("triggered=0x%02X includes OVF with OVIE=0", triggered)
	}
}

// Manual Sec 11.2.7 bit 4 (OCRS): OCRA and OCRB share address 0xFFFFFE14/15;
// OCRS selects which is accessed. Writes to the OCR address with OCRS=1 must
// update OCRB only, leaving OCRA intact.
func TestFRTOCRSelectSharedAddressB(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Seed OCRA via OCRS=0 write.
	frt.Write(0xFFFFFE14, 0x11)
	frt.Write(0xFFFFFE15, 0x22)
	if frt.ocra != 0x1122 {
		t.Fatalf("setup: OCRA = 0x%04X, want 0x1122", frt.ocra)
	}

	// Switch to OCRS=1 and write a different value.
	frt.Write(0xFFFFFE17, 0x10)
	frt.Write(0xFFFFFE14, 0x33)
	frt.Write(0xFFFFFE15, 0x44)

	if frt.ocrb != 0x3344 {
		t.Errorf("OCRB = 0x%04X, want 0x3344", frt.ocrb)
	}
	if frt.ocra != 0x1122 {
		t.Errorf("OCRA modified by OCRS=1 write: 0x%04X, want 0x1122", frt.ocra)
	}

	// Read-back through the shared address while OCRS=1 returns OCRB.
	h := frt.Read(0xFFFFFE14)
	l := frt.Read(0xFFFFFE15)
	if h != 0x33 || l != 0x44 {
		t.Errorf("shared-address read with OCRS=1 = 0x%02X%02X, want 0x3344", h, l)
	}
}

// Manual Sec 11.2.5 bit 0 (CCLRA): counter clear on compare-match A is
// enabled only when CCLRA=1. With CCLRA=0 the counter must continue past
// the match value.
func TestFRTCCLRADisabled(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 2
	// CCLRA left at 0.

	// Tick enough cycles to drive FRC past OCRA (need > 2*8 cycles).
	for i := 0; i < 32; i++ {
		tick(&frt, 1)
	}
	if frt.frc < 3 {
		t.Errorf("FRC = %d with CCLRA=0; counter must continue past match", frt.frc)
	}
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA should be latched even with CCLRA=0")
	}
}

// Manual Sec 11.4.6 / 11.2.5: OCFA is set when FRC equals OCRA regardless
// of the TIER.OCIAE enable bit. Only the interrupt-trigger bitmask should
// differ by enable.
func TestFRTCompareMatchASetsOCFAEvenWithoutEnable(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 1
	// TIER default (no OCIAE).

	triggered := uint8(0)
	for i := 0; i < 8; i++ {
		triggered |= tick(&frt, 1)
	}
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA not set on match when OCIAE=0")
	}
	if triggered&ftcsrOCFA != 0 {
		t.Errorf("triggered includes OCFA with OCIAE=0: 0x%02X", triggered)
	}
}

// IRQFlags returns ftcsr & tier & 0x8E per the implementation contract used
// by INTC resolveSource. Verify across several combinations.
func TestFRTIRQFlagsMasking(t *testing.T) {
	cases := []struct {
		ftcsr uint8
		tier  uint8
		want  uint8
	}{
		{0x00, 0x00, 0x00},
		{0x8E, 0x00, 0x00},
		{0x00, 0x8E, 0x00},
		{0x8E, 0x8E, 0x8E},
		{ftcsrOCFA, tierOCIAE | 0x01, ftcsrOCFA},
		{ftcsrOVF, tierOVIE | 0x01, ftcsrOVF},
		{ftcsrICF, tierICIE | 0x01, ftcsrICF},
		// CCLRA (bit 0) is not an interrupt bit; must be masked out.
		{0xFF, 0xFF, 0x8E},
	}
	for _, c := range cases {
		var frt FRT
		frt.ftcsr = c.ftcsr
		frt.tier = c.tier
		got := frt.IRQFlags()
		if got != c.want {
			t.Errorf("IRQFlags(ftcsr=0x%02X,tier=0x%02X) = 0x%02X, want 0x%02X",
				c.ftcsr, c.tier, got, c.want)
		}
	}
}

// Manual Sec 11.3: 16-bit writes to FRC must be done upper byte first (into
// TEMP) then lower byte (which combines TEMP with the low value and lands
// in FRC). Writing only the low byte leaves TEMP at its previous value,
// producing (oldTEMP<<8 | low) in FRC. This test documents that ordering
// requirement.
func TestFRTFRCWriteTempFlow(t *testing.T) {
	var frt FRT
	frt.Reset()

	// After Reset, temp = 0. A low-byte-only write produces high=0.
	frt.Write(0xFFFFFE13, 0x77)
	if frt.frc != 0x0077 {
		t.Errorf("low-only FRC write = 0x%04X, want 0x0077 (temp was 0)", frt.frc)
	}

	// Write high byte to temp, then low byte applies combined value.
	frt.Write(0xFFFFFE12, 0xAB)
	frt.Write(0xFFFFFE13, 0xCD)
	if frt.frc != 0xABCD {
		t.Errorf("paired FRC write = 0x%04X, want 0xABCD", frt.frc)
	}

	// Next low-only write uses the lingering temp=0xAB from the previous
	// pair.
	frt.Write(0xFFFFFE13, 0x11)
	if frt.frc != 0xAB11 {
		t.Errorf("low-only FRC write after pair = 0x%04X, want 0xAB11", frt.frc)
	}
}

// Manual Sec 5.3.6 Table 5.6 / Sec 11.5: VCRC bits 6-0 hold a single
// output-compare interrupt vector shared by OCIA and OCIB. The FRT has
// three vector slots (ICI, OCI, OVI), not four; OCIA and OCIB route through
// the same OCI vector. INTC exposes this via frtOCIVector().
func TestFRTOCIASubPriorityVectorShared(t *testing.T) {
	var intc INTC
	intc.Reset()
	// VCRC bits 14-8 = ICI vector (0x5A); bits 6-0 = OCI vector (0x33).
	intc.Write(0xFFFFFE66, (0x5A<<8)|0x33)

	if intc.frtICIVector() != 0x5A {
		t.Errorf("frtICIVector = 0x%02X, want 0x5A", intc.frtICIVector())
	}
	if intc.frtOCIVector() != 0x33 {
		t.Errorf("frtOCIVector = 0x%02X, want 0x33", intc.frtOCIVector())
	}
}

// HM Sec 11.4.5 Fig 11.10: input capture snapshots the current FRC
// value into ICR at the capture signal and simultaneously sets ICF.
// ICR is a read-only register in erings, so subsequent FRC ticks
// must not disturb the captured value. Simplification note: the
// FTI pin signal phasing described in Sec 11.4.4 Fig 11.9 (one-cycle
// delay on capture-coincident-with-ICR-upper-read) is not modeled.
// MINIT/SINIT drives InputCapture() as a direct method call.
func TestFRTInputCaptureAtomicSnapshot(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Preload FRC=0x1234 via the temp-register byte-pair path.
	frt.Write(0xFFFFFE12, 0x12)
	frt.Write(0xFFFFFE13, 0x34)
	if frt.frc != 0x1234 {
		t.Fatalf("setup: FRC = 0x%04X, want 0x1234", frt.frc)
	}

	frt.InputCapture()
	if frt.icr != 0x1234 {
		t.Fatalf("ICR after capture = 0x%04X, want 0x1234", frt.icr)
	}

	// Tick many cycles. FRC advances; ICR must not.
	// CKS=0 divides phi/8 so 800 cycles -> +100 FRC ticks.
	tick(&frt, 800)
	if frt.icr != 0x1234 {
		t.Errorf("ICR after 800 FRC ticks = 0x%04X, want 0x1234 (atomic snapshot)",
			frt.icr)
	}
	if frt.frc == 0x1234 {
		t.Errorf("FRC frozen at 0x1234 after 800 ticks; setup flawed (FRC should advance)")
	}
}

// HM Sec 11.2.5: "This flag is cleared by software and set by
// hardware." ICF is a status bit; setting it does not depend on
// TIER.ICIE (that gate only controls whether the interrupt request
// is raised). Existing TestFRTInputCaptureDisabled checks the
// InputCapture return value; this test inspects the FTCSR flag
// directly to pin the set-by-hardware semantics.
func TestFRTInputCaptureICFSetRegardlessOfICIE(t *testing.T) {
	var frt FRT
	frt.Reset()

	// TIER.ICIE = 0.
	if frt.tier&tierICIE != 0 {
		t.Fatalf("setup: TIER.ICIE = 1 after Reset; test precondition broken")
	}

	fired := frt.InputCapture()
	if fired {
		t.Error("InputCapture returned true despite ICIE=0")
	}
	if frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF not set after capture with ICIE=0; hardware sets ICF unconditionally")
	}

	// Read FTCSR via the public path to confirm the bit surfaces.
	if v := frt.Read(0xFFFFFE11); v&ftcsrICF == 0 {
		t.Errorf("FTCSR read = 0x%02X; ICF bit (0x80) missing", v)
	}
}

// HM Sec 11.4.5 / 11.4.6: capture (ICF) and compare (OCFA) signals
// are independent. If FRC already equals OCRA when a capture event
// occurs, both ICF and OCFA must be 1 afterwards. Verifies the two
// latches do not race or mask each other inside InputCapture().
func TestFRTInputCaptureWhileFRCEqualsOCRA(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Force FRC=OCRA=0x5500 via byte-pair writes and a ticked compare.
	frt.ocra = 0x5500
	frt.Write(0xFFFFFE12, 0x54)
	frt.Write(0xFFFFFE13, 0xFF)
	frt.tier = tierICIE | tierOCIAE | 0x01
	// Tick phi/8 once -> FRC 0x54FF -> 0x5500 on the prescaler divide.
	tick(&frt, 8)
	if frt.frc != 0x5500 {
		t.Fatalf("setup: FRC = 0x%04X after tick, want 0x5500", frt.frc)
	}
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Fatalf("setup: OCFA not set by compare match; test precondition broken")
	}

	// Now input capture fires.
	fired := frt.InputCapture()
	if !fired {
		t.Error("InputCapture did not return true despite ICIE=1")
	}
	if frt.ftcsr&ftcsrICF == 0 {
		t.Error("ICF not set after capture")
	}
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA cleared by capture; capture and compare flags must be independent")
	}
	// Both enabled - IRQFlags must surface both.
	flags := frt.IRQFlags()
	if flags&ftcsrICF == 0 {
		t.Errorf("IRQFlags = 0x%02X; ICF not asserted", flags)
	}
	if flags&ftcsrOCFA == 0 {
		t.Errorf("IRQFlags = 0x%02X; OCFA not asserted", flags)
	}
}

// --- Deadline scheduling coverage -----------------------------------

// TestFRTNextEventAfterReset: at reset the default state (CKS=0, FRC=0,
// OCRA=OCRB=0xFFFF) means the first autonomous event is the OCRA/OCRB
// match at cycle 0xFFFF * 8 = 524280.
func TestFRTNextEventAfterReset(t *testing.T) {
	var frt FRT
	frt.Reset()

	if frt.nextEvent != 0xFFFF*8 {
		t.Errorf("nextEvent after reset = %d, want %d", frt.nextEvent, 0xFFFF*8)
	}
}

// TestFRTNextEventRecomputedOnOCRAWrite: writing OCRA to a value closer
// to the current FRC shortens the deadline. The write path recomputes
// nextEvent in production via the CPU's writeOnChip call; tests use
// Write then recomputeNextEvent directly.
func TestFRTNextEventRecomputedOnOCRAWrite(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Move OCRA to 10 and recompute. Next match should be in 10*8=80 cycles.
	frt.Write(0xFFFFFE14, 0x00)
	frt.Write(0xFFFFFE15, 0x0A)
	frt.recomputeNextEvent(0)

	if frt.nextEvent != 80 {
		t.Errorf("nextEvent after OCRA=10 = %d, want 80", frt.nextEvent)
	}
}

// TestFRTNextEventRecomputedOnTCRCKSChange: changing the prescaler
// divider reschedules. prescaler is preserved across the TCR write
// (Section 11 does not reset the prescaler on TCR change; current code
// matches).
func TestFRTNextEventRecomputedOnTCRCKSChange(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Start CKS=0 (div 8), advance so prescaler is partway through.
	tick(&frt, 3)
	if frt.prescaler != 3 {
		t.Fatalf("setup: prescaler = %d, want 3", frt.prescaler)
	}

	// Switch to CKS=2 (div 128). Prescaler stays at 3.
	frt.Write(0xFFFFFE16, 0x02)
	frt.recomputeNextEvent(frt.lastSync)

	if frt.prescaler != 3 {
		t.Errorf("prescaler after CKS change = %d, want 3 (preserved)", frt.prescaler)
	}
	// Next OCRA match: FRC=0 -> 0xFFFF, needs 0xFFFF advances at div 128
	// starting with prescaler=3. cyclesToAdv = 128-3 = 125. steps = 0xFFFF.
	// cycles = 125 + (0xFFFF-1)*128 = 125 + 0xFFFE*128 = 125 + 8388352 = 8388477.
	want := uint64(125) + uint64(0xFFFE)*128
	if frt.nextEvent != frt.lastSync+want {
		t.Errorf("nextEvent after CKS=2 = %d, want %d",
			frt.nextEvent-frt.lastSync, want)
	}
}

// TestFRTNextEventRecomputedOnFRCWrite: writing FRC directly moves the
// counter; the deadline must reflect the new distance to next match.
// Prescaler is preserved.
func TestFRTNextEventRecomputedOnFRCWrite(t *testing.T) {
	var frt FRT
	frt.Reset()

	// Advance so prescaler is partway through.
	tick(&frt, 5)
	if frt.prescaler != 5 {
		t.Fatalf("setup: prescaler = %d, want 5", frt.prescaler)
	}

	// Write FRC to 0xFFFE. OCRA is still 0xFFFF; next match is 1 FRC
	// advance away. prescaler stays at 5 per current semantics.
	frt.Write(0xFFFFFE12, 0xFF)
	frt.Write(0xFFFFFE13, 0xFE)
	frt.recomputeNextEvent(frt.lastSync)

	if frt.prescaler != 5 {
		t.Errorf("prescaler after FRC write = %d, want 5 (preserved)", frt.prescaler)
	}
	// cyclesToAdv = 8-5 = 3. Next OCRA match (FRC=0xFFFE -> 0xFFFF): 1 step.
	// cycles = 3 + 0*8 = 3.
	want := frt.lastSync + 3
	if frt.nextEvent != want {
		t.Errorf("nextEvent after FRC=0xFFFE = %d, want %d", frt.nextEvent, want)
	}
}

// TestFRTNextEventAfterCCLRAReset: with CCLRA=1, OCRA match resets FRC
// to 0. The deadline after the match reflects FRC=0, not the stale
// pre-match value.
func TestFRTNextEventAfterCCLRAReset(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 3
	frt.ftcsr = ftcsrCCLRA

	// Tick past OCRA match. With CCLRA=1, FRC cleared to 0 on match.
	// fireDue also runs recomputeNextEvent, simulating the CPU path.
	frt.fireDue(24)

	if frt.frc != 0 {
		t.Errorf("FRC after CCLRA match = %d, want 0", frt.frc)
	}
	// Next OCRA match: FRC=0 -> 3 = 3 steps * 8 = 24 cycles from lastSync.
	want := frt.lastSync + 24
	if frt.nextEvent != want {
		t.Errorf("nextEvent after CCLRA reset = %d, want %d", frt.nextEvent, want)
	}
}

// TestFRTNextEventCKS3IsInfinite: with external clock selected, no
// autonomous event can fire from CPU cycles. nextEvent is MaxUint64.
func TestFRTNextEventCKS3IsInfinite(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.Write(0xFFFFFE16, 0x03) // CKS=3
	frt.recomputeNextEvent(0)

	if frt.nextEvent != math.MaxUint64 {
		t.Errorf("nextEvent with CKS=3 = %d, want MaxUint64", frt.nextEvent)
	}

	// Confirm syncTo does nothing FRC-wise even for large deltas.
	triggered := tick(&frt, 100000)
	if triggered != 0 {
		t.Errorf("triggered=0x%02X with external clock; want 0", triggered)
	}
	if frt.frc != 0 {
		t.Errorf("FRC=%d with external clock; want 0", frt.frc)
	}
}

// TestFRTReFireAfterFlagClear: after OCRA match fires and software
// clears OCFA, the flag does not re-fire immediately — the next OCRA
// match requires a full 0x10000-step wrap back to the OCRA value.
// Verifies two observables: the post-clear deadline is strictly in the
// future, and OCFA does eventually re-latch after the full wrap.
func TestFRTReFireAfterFlagClear(t *testing.T) {
	var frt FRT
	frt.Reset()

	frt.ocra = 3
	frt.tier = tierOCIAE | 0x01

	// First match.
	tick(&frt, 24)
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Fatal("setup: first OCRA match didn't fire")
	}
	if frt.frc != 3 {
		t.Fatalf("setup: FRC after first match = %d, want 3", frt.frc)
	}

	// Software clears OCFA and the deadline is recomputed (production
	// path: CPU writeOnChip calls recomputeNextEvent after Write).
	frt.Write(0xFFFFFE11, 0x00)
	frt.recomputeNextEvent(frt.lastSync)

	if frt.ftcsr&ftcsrOCFA != 0 {
		t.Fatal("setup: flag clear did not clear OCFA")
	}
	if frt.nextEvent <= frt.lastSync {
		t.Errorf("nextEvent=0x%X <= lastSync=0x%X; flag clear caused immediate re-fire",
			frt.nextEvent, frt.lastSync)
	}

	// Drive FRC through a full wrap. OCFA must re-latch when FRC
	// reaches 3 again. 0x10000 FRC advances * 8 cycles = 0x80000.
	tick(&frt, 0x80000)
	if frt.ftcsr&ftcsrOCFA == 0 {
		t.Error("OCFA did not re-fire after full FRC wrap back to OCRA")
	}
}
