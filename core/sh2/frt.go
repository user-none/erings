// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "math"

// FRT implements the SH-2 on-chip free-running timer.
// The FRT provides a 16-bit free-running counter, output compare,
// and input capture. Input capture is used for inter-CPU
// communication in the Saturn (MINIT/SINIT writes).
type FRT struct {
	// Counter and compare
	frc  uint16 // Free-running counter
	ocra uint16 // Output compare register A
	ocrb uint16 // Output compare register B

	// Input capture
	icr uint16 // Input capture register

	// Control/status
	tier  uint8 // Timer interrupt enable register
	ftcsr uint8 // Timer control/status register
	tcr   uint8 // Timer control register
	tocr  uint8 // Timer output compare control register

	// Internal state
	prescaler uint16 // Counts CPU cycles between FRC increments
	temp      uint8  // Temporary register for 16-bit byte-pair access

	// Deadline scheduling. lastSync is the absolute CPU cycle at which
	// frc/prescaler were last reconciled; nextEvent is the absolute
	// cycle at which the next autonomous event (OCRA match, OCRB match,
	// or overflow) can fire. math.MaxUint64 means no event can fire
	// (CKS=3 external clock).
	lastSync  uint64
	nextEvent uint64
}

// Clock divider values indexed by TCR CKS bits (0-2).
// CKS=3 is external clock (not used in Saturn).
var frtDividers = [4]uint16{8, 32, 128, 1}

// FRT register bit masks
const (
	tierICIE  = 0x80 // Input capture interrupt enable
	tierOCIAE = 0x08 // Output compare A interrupt enable
	tierOCIBE = 0x04 // Output compare B interrupt enable
	tierOVIE  = 0x02 // Timer overflow interrupt enable

	ftcsrICF   = 0x80 // Input capture flag
	ftcsrOCFA  = 0x08 // Output compare A flag
	ftcsrOCFB  = 0x04 // Output compare B flag
	ftcsrOVF   = 0x02 // Timer overflow flag
	ftcsrCCLRA = 0x01 // Counter clear on compare A match

	tocrOCRS = 0x10 // OCR select: 0=OCRA, 1=OCRB at OCR address
)

// Reset returns the FRT to power-on state.
func (f *FRT) Reset() {
	f.frc = 0
	f.ocra = 0xFFFF
	f.ocrb = 0xFFFF
	f.icr = 0
	f.tier = 0x01 // bit 0 is always 1
	f.ftcsr = 0
	f.tcr = 0
	f.tocr = 0xE0 // upper 3 bits are always 1
	f.prescaler = 0
	f.temp = 0
	f.lastSync = 0
	f.recomputeNextEvent(0)
}

// syncTo advances frc/prescaler from lastSync up to the absolute cycle
// now, firing any OCRA/OCRB/overflow events encountered along the way.
// Returns the OR of newly triggered interrupt flag bits (flags that are
// both set and enabled). After this call lastSync == now.
//
// CCLRA handling, compare-match-then-CCLRA-reset, and overflow detection
// follow Section 11 of the SH7604 hardware manual (Figures 11.6, 11.7,
// 11.11, 11.12). Each FRC increment is walked individually so mid-stream
// FRC reset (CCLRA=1 on OCRA match) and stacked match/overflow in the
// same count all sequence correctly.
func (f *FRT) syncTo(now uint64) uint8 {
	if now <= f.lastSync {
		return 0
	}
	delta := now - f.lastSync
	f.lastSync = now

	cks := f.tcr & 0x03
	if cks == 3 {
		// External clock: CPU cycles do not advance FRC or prescaler.
		return 0
	}
	div := uint64(frtDividers[cks])
	triggered := uint8(0)

	total := uint64(f.prescaler) + delta
	advances := total / div
	f.prescaler = uint16(total % div)

	for i := uint64(0); i < advances; i++ {
		oldFRC := f.frc
		f.frc++

		// Compare match A
		if f.frc == f.ocra {
			f.ftcsr |= ftcsrOCFA
			if f.tier&tierOCIAE != 0 {
				triggered |= ftcsrOCFA
			}
			if f.ftcsr&ftcsrCCLRA != 0 {
				f.frc = 0
			}
		}

		// Compare match B
		if f.frc == f.ocrb {
			f.ftcsr |= ftcsrOCFB
			if f.tier&tierOCIBE != 0 {
				triggered |= ftcsrOCFB
			}
		}

		// Overflow (0xFFFF -> 0x0000)
		if oldFRC == 0xFFFF && f.frc == 0 {
			f.ftcsr |= ftcsrOVF
			if f.tier&tierOVIE != 0 {
				triggered |= ftcsrOVF
			}
		}
	}

	return triggered
}

// recomputeNextEvent sets nextEvent to the earliest absolute CPU cycle
// on which OCRA match, OCRB match, or overflow can fire from the
// current frc/prescaler/ocra/ocrb/tcr state. If CKS=3 (external clock)
// no autonomous event can fire and nextEvent is math.MaxUint64.
//
// The "cycles to reach target" formula treats target==frc as a full
// 0x10000-step wrap (next arrival, not current), matching syncTo's
// post-increment match check.
func (f *FRT) recomputeNextEvent(now uint64) {
	cks := f.tcr & 0x03
	if cks == 3 {
		f.nextEvent = math.MaxUint64
		return
	}
	div := uint64(frtDividers[cks])
	cyclesToAdv := div - uint64(f.prescaler)

	cyclesTo := func(target uint16) uint64 {
		steps := uint64((target-f.frc-1)&0xFFFF) + 1 // 1..0x10000
		return cyclesToAdv + (steps-1)*div
	}

	c := cyclesTo(f.ocra)
	if cb := cyclesTo(f.ocrb); cb < c {
		c = cb
	}
	if co := cyclesTo(0); co < c {
		c = co
	}

	f.nextEvent = now + c
}

// fireDue is called when the CPU cycle reaches nextEvent. It syncs
// state to now, recomputes the next deadline, and returns the OR of
// newly triggered interrupt flag bits.
func (f *FRT) fireDue(now uint64) uint8 {
	triggered := f.syncTo(now)
	f.recomputeNextEvent(now)
	return triggered
}

// IRQFlags returns the currently asserted FRT interrupt flags masked
// by their enable bits. Bit positions match FTCSR/TIER: ICF=0x80,
// OCFA=0x08, OCFB=0x04, OVF=0x02. Non-zero means an interrupt is
// being asserted to the INTC. Flags remain asserted until software
// writes 0 to the corresponding FTCSR bit.
func (f *FRT) IRQFlags() uint8 {
	return f.ftcsr & f.tier & 0x8E
}

// InputCapture latches the current FRC value into ICR and sets
// the input capture flag. Used by MINIT/SINIT writes.
// Returns true if the ICF interrupt is enabled.
func (f *FRT) InputCapture() bool {
	f.icr = f.frc
	f.ftcsr |= ftcsrICF
	return f.tier&tierICIE != 0
}

// Read reads an FRT register by full address (0xFFFFFE10-0xFFFFFE19).
// FRT registers are byte-access only.
func (f *FRT) Read(addr uint32) uint8 {
	switch addr {
	case 0xFFFFFE10: // TIER
		return f.tier
	case 0xFFFFFE11: // FTCSR
		return f.ftcsr
	case 0xFFFFFE12: // FRC H
		f.temp = uint8(f.frc)
		return uint8(f.frc >> 8)
	case 0xFFFFFE13: // FRC L
		return f.temp
	case 0xFFFFFE14: // OCR H (OCRA or OCRB based on TOCR.OCRS)
		if f.tocr&tocrOCRS != 0 {
			return uint8(f.ocrb >> 8)
		}
		return uint8(f.ocra >> 8)
	case 0xFFFFFE15: // OCR L
		if f.tocr&tocrOCRS != 0 {
			return uint8(f.ocrb)
		}
		return uint8(f.ocra)
	case 0xFFFFFE16: // TCR
		return f.tcr
	case 0xFFFFFE17: // TOCR
		return f.tocr
	case 0xFFFFFE18: // ICR H
		return uint8(f.icr >> 8)
	case 0xFFFFFE19: // ICR L
		return uint8(f.icr)
	}
	return 0
}

// Write writes an FRT register by full address (0xFFFFFE10-0xFFFFFE19).
// FRT registers are byte-access only.
func (f *FRT) Write(addr uint32, val uint8) {
	switch addr {
	case 0xFFFFFE10: // TIER
		f.tier = (val & 0x8E) | 0x01 // bit 0 always 1
	case 0xFFFFFE11: // FTCSR
		// Flags (bits 7, 3-1) can only be cleared (write 0 to clear).
		// Bit 0 (CCLRA) is normal R/W.
		flags := val & 0x8E // flag bits mask
		f.ftcsr = (f.ftcsr & flags) | (val & ftcsrCCLRA)
	case 0xFFFFFE12: // FRC H - write to temp, apply on low byte write
		f.temp = val
	case 0xFFFFFE13: // FRC L
		f.frc = uint16(f.temp)<<8 | uint16(val)
	case 0xFFFFFE14: // OCR H - write to temp
		f.temp = val
	case 0xFFFFFE15: // OCR L
		v := uint16(f.temp)<<8 | uint16(val)
		if f.tocr&tocrOCRS != 0 {
			f.ocrb = v
		} else {
			f.ocra = v
		}
	case 0xFFFFFE16: // TCR
		f.tcr = val & 0x83 // bits 7 (IEDGA) and 1-0 (CKS) are writable
	case 0xFFFFFE17: // TOCR
		f.tocr = (val & 0x10) | 0xE0 // only bit 4 writable, upper 3 always 1
		// ICR (0xFFFFFE18-0xFFFFFE19) is read-only
	}
}
