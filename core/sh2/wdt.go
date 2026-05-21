// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import (
	"fmt"
	"math"
)

// WDT implements the SH-2 on-chip Watchdog Timer (manual Sec 12).
// It consists of an 8-bit free-running counter (WTCNT) clocked from
// the CPU via a prescaler, a control/status register (WTCSR) for
// enable / mode / CKS / overflow flag, and a reset control register
// (RSTCSR). Two modes are selected by WTCSR.WT/IT:
//
//   - Interval mode (WT/IT=0): overflow sets WTCSR.OVF and raises an
//     interval-interrupt (ITI) through the INTC. The latch is sticky
//     until software clears it via the A5-keyed word-write path.
//   - Watchdog mode  (WT/IT=1): overflow sets RSTCSR.WOVF and would
//     trigger a CPU reset on hardware. Reset is NOT modeled here
//     a log line surfaces the event.
//
// All three registers share a word-write protection scheme (manual
// Sec 12.2.4): WTCSR/WTCNT live at 0xFFFFFE80 (word-aligned) with
// high-byte 0xA5 selecting WTCSR and 0x5A selecting WTCNT; RSTCSR
// at 0xFFFFFE82 accepts 0xA500 (clear WOVF) or 0x5A?? (update
// RSTE/RSTS). Any other high byte discards the write.
//
// The CPU drives the WDT via deadline scheduling: nextEvent holds the
// absolute CPU cycle at which the next WTCNT wrap will occur, and
// tickPeripherals compares against that instead of calling into the
// WDT every cycle. syncTo reconciles lastSync->now on demand (register
// access, deadline hit, reset).
type WDT struct {
	wtcsr     uint8
	wtcnt     uint8
	rstcsr    uint8
	prescaler uint32

	// Deadline scheduling. lastSync is the absolute CPU cycle at which
	// wtcnt/prescaler were last reconciled; nextEvent is the absolute
	// cycle at which the next WTCNT wrap can fire. math.MaxUint64
	// means no event can fire (TME=0).
	lastSync  uint64
	nextEvent uint64
}

// WDT register bit masks.
const (
	wtcsrOVF  = 0x80 // Overflow flag (interval mode)
	wtcsrWTIT = 0x40 // 0 = interval mode, 1 = watchdog mode
	wtcsrTME  = 0x20 // Timer enable
	// bits 4-3 reserved, always read 1
	wtcsrCKS = 0x07 // Clock select

	rstcsrWOVF = 0x80
	rstcsrRSTE = 0x40
	rstcsrRSTS = 0x20
)

// wdtPrescalerShift maps WTCSR.CKS (0..7) to a shift value; the
// counter ticks every (1 << shift) CPU cycles
var wdtPrescalerShift = [8]uint32{1, 6, 7, 8, 9, 10, 12, 13}

// Reset returns the WDT to power-on state per manual Sec 12.2:
// WTCSR=0x18 (reserved bits 4-3 set), WTCNT=0x00, RSTCSR=0x1F
// (reserved bits 4-0 set).
func (w *WDT) Reset() {
	w.wtcsr = 0x18
	w.wtcnt = 0x00
	w.rstcsr = 0x1F
	w.prescaler = 0
	w.lastSync = 0
	w.recomputeNextEvent(0)
}

// Read reads a WDT register byte. Read addresses per manual
// Sec 12.1.4 Table 12.2:
//   - 0xFFFFFE80: WTCSR
//   - 0xFFFFFE81: WTCNT
//   - 0xFFFFFE82: undefined; returns 0.
//   - 0xFFFFFE83: RSTCSR
//
// RSTCSR's read address is offset +1 from its write address because
// its word-write places the register value in the low byte of the
// word (big-endian), so byte-wise the register lives at 0xFFFFFE83.
func (w *WDT) Read(addr uint32) uint8 {
	switch addr {
	case 0xFFFFFE80:
		return w.wtcsr
	case 0xFFFFFE81:
		return w.wtcnt
	case 0xFFFFFE83:
		return w.rstcsr
	}
	return 0
}

// WriteWord handles a 16-bit write at the word-aligned address
// 0xFFFFFE80 or 0xFFFFFE82, applying the manual's high-byte key.
func (w *WDT) WriteWord(addr uint32, val uint16) {
	hi := uint8(val >> 8)
	lo := uint8(val)
	switch addr {
	case 0xFFFFFE80:
		switch hi {
		case 0xA5:
			// WTCSR update. OVF is write-0-to-clear; writes of 1
			// to OVF are ignored. Bits 4-3 stay forced to 1.
			ovf := w.wtcsr & wtcsrOVF
			if lo&wtcsrOVF == 0 {
				ovf = 0
			}
			w.wtcsr = (lo & (wtcsrWTIT | wtcsrTME | wtcsrCKS)) | 0x18 | ovf
			// Manual Sec 12.2.2 bit 5 (TME): "Timer disabled:
			// WTCNT is initialized to H'00 and count-up stops."
			if w.wtcsr&wtcsrTME == 0 {
				w.wtcnt = 0
				w.prescaler = 0
			}
		case 0x5A:
			w.wtcnt = lo
		}
	case 0xFFFFFE82:
		switch {
		case val == 0xA500:
			// Clear WOVF only.
			w.rstcsr &^= rstcsrWOVF
		case hi == 0x5A:
			// Update RSTE/RSTS (bits 6-5). Reserved bits 4-0
			// stay set to 1; WOVF (bit 7) unchanged.
			w.rstcsr = (w.rstcsr & rstcsrWOVF) | (lo & (rstcsrRSTE | rstcsrRSTS)) | 0x1F
		}
	}
}

// syncTo advances wtcnt/prescaler from lastSync up to the absolute
// cycle now, latching OVF (interval) or WOVF (watchdog) for any wraps
// encountered. Returns true if at least one wrap occurred during the
// interval. Emits the watchdog-mode log line on each watchdog wrap.
// After this call lastSync == now.
//
// The counter continues past the wrap (Sec 12.2.1) — OVF/WOVF do not
// stop the count. Multiple wraps within one interval all re-set the
// (already sticky) flag.
func (w *WDT) syncTo(now uint64) bool {
	if now <= w.lastSync {
		return false
	}
	delta := now - w.lastSync
	w.lastSync = now

	if w.wtcsr&wtcsrTME == 0 {
		return false
	}

	shift := wdtPrescalerShift[w.wtcsr&wtcsrCKS]
	step := uint32(1) << shift

	total := uint64(w.prescaler) + delta
	stepU64 := uint64(step)
	advances := total / stepU64
	w.prescaler = uint32(total % stepU64)

	overflowed := false
	for i := uint64(0); i < advances; i++ {
		w.wtcnt++
		if w.wtcnt == 0 {
			overflowed = true
			if w.wtcsr&wtcsrWTIT == 0 {
				// Interval mode: latch OVF. Stays set until
				// software clears via A5-keyed write.
				w.wtcsr |= wtcsrOVF
			} else {
				// Watchdog mode: latch WOVF, log, no reset.
				w.rstcsr |= rstcsrWOVF
				fmt.Printf("[WDT] watchdog-mode overflow (reset not modeled)\n")
			}
		}
	}
	return overflowed
}

// recomputeNextEvent sets nextEvent to the absolute CPU cycle on
// which the next WTCNT wrap will fire from the current
// wtcnt/prescaler/tcr state. math.MaxUint64 when TME=0 (no event).
//
// With wtcnt=0 the result is a full 256-step round trip; with
// wtcnt=0xFF it is one FRC advance away (matching the post-increment
// wrap check in syncTo).
func (w *WDT) recomputeNextEvent(now uint64) {
	if w.wtcsr&wtcsrTME == 0 {
		w.nextEvent = math.MaxUint64
		return
	}
	shift := wdtPrescalerShift[w.wtcsr&wtcsrCKS]
	step := uint64(1) << shift
	cyclesToAdv := step - uint64(w.prescaler)
	advances := uint64(0x100) - uint64(w.wtcnt) // 1..0x100
	w.nextEvent = now + cyclesToAdv + (advances-1)*step
}

// fireDue is called when the CPU cycle reaches nextEvent. It syncs
// state to now, recomputes the next deadline, and returns true if an
// overflow occurred during the sync.
func (w *WDT) fireDue(now uint64) bool {
	overflowed := w.syncTo(now)
	w.recomputeNextEvent(now)
	return overflowed
}

// IRQAsserted returns true when the WDT is driving an interval
// interrupt request line: OVF latch set AND interval mode selected.
// Watchdog-mode overflow does not raise ITI.
func (w *WDT) IRQAsserted() bool {
	return w.wtcsr&wtcsrOVF != 0 && w.wtcsr&wtcsrWTIT == 0
}
