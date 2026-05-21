// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "math"

// DIVU implements the SH-2 on-chip hardware division unit.
// It performs 32/32 -> 32 and 64/32 -> 32 signed division in hardware,
// triggered by writing the dividend register.
type DIVU struct {
	dvsr   uint32 // Divisor register
	dvdnt  uint32 // Dividend register (32-bit) / quotient result
	dvdnth uint32 // Dividend high (64-bit mode) / remainder result
	dvdntl uint32 // Dividend low (64-bit mode) / quotient result

	dvcr   uint32 // Division control register (bit 1=OVFIE, bit 0=OVF)
	vcrdiv uint32 // Interrupt vector number (bits 6-0)
}

// IRQAsserted returns true when the DIVU overflow interrupt line is
// currently driven to the INTC: OVF latch (bit 0) is set AND OVFIE
// enable (bit 1) is set. Latch remains asserted until software clears
// DVCR.OVF.
func (d *DIVU) IRQAsserted() bool {
	return d.dvcr&0x03 == 0x03
}

// Reset returns the division unit to power-on state.
func (d *DIVU) Reset() {
	d.dvsr = 0
	d.dvdnt = 0
	d.dvdnth = 0
	d.dvdntl = 0
	d.dvcr = 0
	d.vcrdiv = 0
}

// Read reads a DIVU register by full address (0xFFFFFF00-0xFFFFFF14).
// The SH-2 also exposes non-destructive aliases of DVDNTH and DVDNTL at
// 0xFFFFFF18 and 0xFFFFFF1C, returning the remainder and quotient of
// the last division without affecting DIVU state. The SH-7604 manual
// documents these addresses as reserved, but the real hardware mirrors
// DVDNTH/DVDNTL there and NiGHTS relies on reading the quotient from
// 0xFFFFFF1C in its vertex projection code.
func (d *DIVU) Read(addr uint32) uint32 {
	switch addr {
	case 0xFFFFFF00:
		return d.dvsr
	case 0xFFFFFF04:
		return d.dvdnt
	case 0xFFFFFF08:
		return d.dvcr
	case 0xFFFFFF0C:
		return d.vcrdiv
	case 0xFFFFFF10, 0xFFFFFF18:
		return d.dvdnth
	case 0xFFFFFF14, 0xFFFFFF1C:
		return d.dvdntl
	}
	return 0
}

// Write writes a DIVU register by full address (0xFFFFFF00-0xFFFFFF14).
// Writing DVDNT triggers 32/32 division. Writing DVDNTL triggers 64/32 division.
// Returns true if a division overflow interrupt should be generated.
func (d *DIVU) Write(addr uint32, val uint32) bool {
	switch addr {
	case 0xFFFFFF00: // DVSR
		d.dvsr = val
	case 0xFFFFFF04: // DVDNT - triggers 32/32 division
		d.dvdnt = val
		// Sign-extend 32-bit dividend to 64-bit
		d.dvdntl = val
		if int32(val) < 0 {
			d.dvdnth = 0xFFFFFFFF
		} else {
			d.dvdnth = 0
		}
		return d.divide()
	case 0xFFFFFF08: // DVCR
		d.dvcr = val & 0x03 // only bits 1-0
	case 0xFFFFFF0C: // VCRDIV
		d.vcrdiv = val & 0x7F // only bits 6-0
	case 0xFFFFFF10, 0xFFFFFF18: // DVDNTH (and non-destructive alias)
		d.dvdnth = val
	case 0xFFFFFF14, 0xFFFFFF1C: // DVDNTL - triggers 64/32 division (and alias)
		d.dvdntl = val
		return d.divide()
	}
	return false
}

// divide performs signed division using dvdnth:dvdntl / dvsr.
// Stores quotient in dvdntl (and dvdnt), remainder in dvdnth.
// Returns true if overflow occurred and OVFIE is enabled.
func (d *DIVU) divide() bool {
	divisor := int64(int32(d.dvsr))
	dividend := int64(uint64(d.dvdnth)<<32 | uint64(d.dvdntl))

	if divisor == 0 {
		// Divide by zero: overflow
		return d.handleOverflow(dividend >= 0)
	}

	quotient := dividend / divisor
	remainder := dividend % divisor

	// Check if quotient fits in int32
	if quotient > math.MaxInt32 || quotient < math.MinInt32 {
		return d.handleOverflow(dividend >= 0 != (divisor < 0))
	}

	d.dvdntl = uint32(int32(quotient))
	d.dvdnt = d.dvdntl
	d.dvdnth = uint32(int32(remainder))
	return false
}

// handleOverflow handles division overflow. Sets OVF flag and clamps
// the quotient if OVFIE is disabled. Returns true if OVFIE is enabled.
func (d *DIVU) handleOverflow(positive bool) bool {
	d.dvcr |= 0x01 // Set OVF

	if d.dvcr&0x02 != 0 {
		// OVFIE enabled - generate interrupt, leave result undefined
		return true
	}

	// OVFIE disabled - clamp quotient
	if positive {
		d.dvdntl = 0x7FFFFFFF
	} else {
		d.dvdntl = 0x80000000
	}
	d.dvdnt = d.dvdntl
	return false
}
