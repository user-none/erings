// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// IntSource identifies an on-chip interrupt source. Values are ordered
// by manual Sec 5.2.5 Table 5.4 fixed tie-break priority (high -> low).
// Iterating the pending bitmask from LSB to MSB picks the higher
// priority source on equal IPR-level ties.
type IntSource uint8

const (
	isrcUBC   IntSource = iota // Sec 6 not yet implemented
	isrcDIVU                   // Sec 10 implemented
	isrcDMAC0                  // Sec 9 ch0
	isrcDMAC1                  // Sec 9 ch1
	isrcWDT                    // Sec 12 not yet implemented
	isrcBSC                    // Sec 7 refresh CMI not yet implemented
	isrcSCI                    // Sec 13 not yet implemented
	isrcFRT                    // Sec 11 implemented
	numIntSources
)

// INTC implements the SH-2 on-chip interrupt controller.
// It manages priority levels for internal interrupt sources
// (FRT, DIVU, etc.) and routes them to the CPU.
type INTC struct {
	// Interrupt priority registers (IPRA, IPRB)
	ipra uint16
	iprb uint16

	// Vector registers (VCRA-VCRD)
	vcra uint16
	vcrb uint16
	vcrc uint16
	vcrd uint16

	// ICR - Interrupt control register
	icr uint16

	// VCRWDT - WDT/BSC vector register
	vcrwdt uint16

	// pending is a bitmask of sources whose request line may currently
	// be asserted (bit n = 1 << IntSource n). This is an optimistic
	// short-circuit: if the bit is set, processInterrupt scans that
	// source's peripheral; if the peripheral's latch is no longer
	// asserted, the bit is lazily cleared. When pending == 0 the
	// O(1) common path skips the full scan.
	pending uint16
}

// Reset returns the INTC to power-on state. Per manual Sec 5.3.8 the
// ICR's initial value is H'8000 when the NMI input pin is high and
// H'0000 when it is low. On the Saturn the NMI pin is pulled high
// at boot (SMPC asserts it briefly only on reset-button presses),
// so the NMIL bit (bit 15) is set.
func (i *INTC) Reset() {
	i.ipra = 0
	i.iprb = 0
	i.vcra = 0
	i.vcrb = 0
	i.vcrc = 0
	i.vcrd = 0
	i.icr = 0x8000
	i.vcrwdt = 0
	i.pending = 0
}

// Read reads an INTC register. The addr parameter is the full
// 32-bit address (0xFFFFFE60-0xFFFFFEE4).
func (i *INTC) Read(addr uint32) uint16 {
	switch addr {
	case 0xFFFFFE60:
		return i.iprb
	case 0xFFFFFE62:
		return i.vcra
	case 0xFFFFFE64:
		return i.vcrb
	case 0xFFFFFE66:
		return i.vcrc
	case 0xFFFFFE68:
		return i.vcrd
	case 0xFFFFFEE0:
		return i.icr
	case 0xFFFFFEE2:
		return i.ipra
	case 0xFFFFFEE4:
		return i.vcrwdt
	}
	return 0
}

// Write writes an INTC register. The addr parameter is the full
// 32-bit address (0xFFFFFE60-0xFFFFFEE4).
//
// Reserved bits are masked to zero per manual Sec 5.3:
//   - IPRA  (0xFFFFFEE2): bits 3-0 reserved     -> 0xFFF0
//   - IPRB  (0xFFFFFE60): bits 7-0 reserved     -> 0xFF00
//   - VCRA  (0xFFFFFE62): bits 15, 7 reserved   -> 0x7F7F
//   - VCRB  (0xFFFFFE64): bits 15, 7 reserved   -> 0x7F7F
//   - VCRC  (0xFFFFFE66): bits 15, 7 reserved   -> 0x7F7F
//   - VCRD  (0xFFFFFE68): bits 15, 7-0 reserved -> 0x7F00
//   - VCRWDT(0xFFFFFEE4): bits 15, 7 reserved   -> 0x7F7F
//   - ICR   (0xFFFFFEE0): bit 15 NMIL read-only, only bits 8 and 0 writable
func (i *INTC) Write(addr uint32, val uint16) {
	switch addr {
	case 0xFFFFFE60:
		i.iprb = val & 0xFF00
	case 0xFFFFFE62:
		i.vcra = val & 0x7F7F
	case 0xFFFFFE64:
		i.vcrb = val & 0x7F7F
	case 0xFFFFFE66:
		i.vcrc = val & 0x7F7F
	case 0xFFFFFE68:
		i.vcrd = val & 0x7F00
	case 0xFFFFFEE0:
		// ICR: bit 15 (NMIL) is read-only on SH7604, bits 8 (NMIE) and 0 (VECMD) writable
		i.icr = (i.icr & 0x8000) | (val & 0x0101)
	case 0xFFFFFEE2:
		i.ipra = val & 0xFFF0
	case 0xFFFFFEE4:
		i.vcrwdt = val & 0x7F7F
	}
}

// AssertSource marks an interrupt source as possibly asserted. Called by
// a peripheral's route function when the peripheral's status flag
// transitions high. processInterrupt reconciles stale-true bits lazily.
func (i *INTC) AssertSource(s IntSource) {
	i.pending |= 1 << s
}

// SetNMIL sets or clears the NMIL bit (bit 15) of the ICR register.
// NMIL is a read-only bit that reflects the NMI input pin level.
func (i *INTC) SetNMIL(level bool) {
	if level {
		i.icr |= 0x8000
	} else {
		i.icr &^= 0x8000
	}
}

// frtPriority returns the FRT interrupt priority level from IPRB bits 11-8.
func (i *INTC) frtPriority() uint8 {
	return uint8((i.iprb >> 8) & 0xF)
}

// divuPriority returns the DIVU interrupt priority level from IPRA bits 15-12.
func (i *INTC) divuPriority() uint8 {
	return uint8((i.ipra >> 12) & 0xF)
}

// frtICIVector returns the FRT input capture interrupt vector from VCRC bits 14-8.
func (i *INTC) frtICIVector() uint16 {
	return (i.vcrc >> 8) & 0x7F
}

// frtOCIVector returns the FRT output compare interrupt vector from VCRC bits 6-0.
// Used for both OCIA and OCIB (single vector per hardware manual Table 5.6).
func (i *INTC) frtOCIVector() uint16 {
	return i.vcrc & 0x7F
}

// frtOVIVector returns the FRT overflow interrupt vector from VCRD bits 14-8.
func (i *INTC) frtOVIVector() uint16 {
	return (i.vcrd >> 8) & 0x7F
}

// dmacPriority returns the DMAC interrupt priority level from IPRA bits 11-8.
func (i *INTC) dmacPriority() uint8 {
	return uint8((i.ipra >> 8) & 0xF)
}

// wdtPriority returns the WDT interrupt priority level from IPRA
// bits 7-4. Shared with BSC REF CMI per manual table 5.5; WDT wins
// on equal-level ties.
func (i *INTC) wdtPriority() uint8 {
	return uint8((i.ipra >> 4) & 0xF)
}

// wdtITIVector returns the WDT interval-interrupt vector from
// VCRWDT bits 14-8.
func (i *INTC) wdtITIVector() uint16 {
	return (i.vcrwdt >> 8) & 0x7F
}
