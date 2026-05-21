// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "math/bits"

// Exception vector numbers.
const (
	vecPowerOnPC    = 0  // Power-on reset PC
	vecPowerOnSP    = 1  // Power-on reset SP
	vecManualPC     = 2  // Manual reset PC
	vecManualSP     = 3  // Manual reset SP
	vecIllegalInstr = 4  // General illegal instruction
	vecSlotIllegal  = 6  // Slot illegal instruction (illegal in delay slot)
	vecCPUAddr      = 9  // CPU address error
	vecDMAAddr      = 10 // DMA address error
	vecNMI          = 11 // NMI
	vecUserBreak    = 12 // User break
	vecTRAP         = 32 // TRAPA #imm (vectors 32-63)
)

// interruptPending reports whether processInterrupt could do anything
// this cycle: an interrupt source is asserted, or a one-shot flag
// (NMI / interrupt-inhibit) still needs servicing. When this is false
// processInterrupt is guaranteed to return false with no side effect, so
// the Clock call site can skip the call entirely. Small enough to
// inline.
func (c *CPU) interruptPending() bool {
	return c.inDelay || c.nmiPending || c.intInhibit || c.intc.pending != 0 || c.irlLevel != 0
}

// processInterrupt resolves the pending interrupt state and services
// the highest priority one if its level exceeds the current mask. The
// Clock call site gates this on interruptPending so it is only entered
// when there is something to act on. It also consumes the NMI and
// interrupt-inhibit one-shots. Sources considered: NMI (always wins),
// on-chip peripherals (via per-source latch bitmask with fixed
// tie-break), and IRL (external, from SCU, level-triggered). Interrupts
// are not accepted during delay slots per manual Sec 4.6.1.
// Returns true if an interrupt was serviced.
func (c *CPU) processInterrupt() bool {
	if c.inDelay {
		return false
	}

	// NMI: unmaskable, always level 16, vector 11. Fast-path out.
	// Checked before intInhibit because NMI is not an "interrupt" in
	// the Sec 4.6.2 sense - it bypasses all masking.
	if c.nmiPending {
		c.nmiPending = false
		return c.acceptInterrupt(16, vecNMI, false)
	}

	// Manual Sec 4.6.2: the instruction immediately following an
	// interrupt-disabled instruction (LDC/LDC.L/STC/STC.L/LDS/
	// LDS.L/STS/STS.L) rejects maskable interrupts. One-shot
	// consume here so the instruction after THAT accepts normally.
	// Address errors are not routed through processInterrupt; they
	// continue to dispatch synchronously via serviceException.
	if c.intInhibit {
		c.intInhibit = false
		return false
	}

	// Common case: nothing pending anywhere.
	if c.intc.pending == 0 && c.irlLevel == 0 {
		return false
	}

	bestLevel := uint8(0)
	bestVec := uint16(0)
	fromIRL := false

	// Scan per-source bitmask in fixed-priority order (low bit first
	// = higher priority per IntSource enum ordering). Strict > on
	// level comparison preserves tie-break via iteration order.
	bits16 := c.intc.pending
	for bits16 != 0 {
		srcBit := bits16 & -bits16
		src := IntSource(bits.TrailingZeros16(bits16))
		bits16 &^= srcBit

		lvl, vec, asserted := c.resolveSource(src)
		if !asserted {
			// Lazy reconcile: latch cleared without notifying INTC.
			c.intc.pending &^= uint16(srcBit)
			continue
		}
		if lvl > bestLevel {
			bestLevel = lvl
			bestVec = vec
		}
	}

	// External IRL (from SCU).
	if c.irlLevel > bestLevel {
		bestLevel = c.irlLevel
		bestVec = c.irlVec
		fromIRL = true
	}

	if bestLevel == 0 || bestLevel <= c.reg.IMASK() {
		return false
	}

	return c.acceptInterrupt(bestLevel, bestVec, fromIRL)
}

// resolveSource returns the (level, vector, asserted) tuple for a
// given on-chip interrupt source by consulting its peripheral's
// latch-and-enable state. Used by processInterrupt.
func (c *CPU) resolveSource(s IntSource) (uint8, uint16, bool) {
	switch s {
	case isrcDIVU:
		if !c.divu.IRQAsserted() {
			return 0, 0, false
		}
		return c.intc.divuPriority(), uint16(c.divu.vcrdiv & 0x7F), true
	case isrcDMAC0:
		if !c.dmac.IRQAsserted(0) {
			return 0, 0, false
		}
		return c.intc.dmacPriority(), uint16(c.dmac.ch[0].vcrdma & 0x7F), true
	case isrcDMAC1:
		if !c.dmac.IRQAsserted(1) {
			return 0, 0, false
		}
		return c.intc.dmacPriority(), uint16(c.dmac.ch[1].vcrdma & 0x7F), true
	case isrcFRT:
		f := c.frt.IRQFlags()
		if f == 0 {
			return 0, 0, false
		}
		var vec uint16
		switch {
		case f&0x80 != 0:
			vec = c.intc.frtICIVector()
		case f&0x08 != 0:
			vec = c.intc.frtOCIVector()
		case f&0x04 != 0:
			vec = c.intc.frtOCIVector()
		case f&0x02 != 0:
			vec = c.intc.frtOVIVector()
		}
		return c.intc.frtPriority(), vec, true
	case isrcWDT:
		if !c.wdt.IRQAsserted() {
			return 0, 0, false
		}
		return c.intc.wdtPriority(), c.intc.wdtITIVector(), true
	}
	return 0, 0, false
}

// acceptInterrupt executes the common accept sequence: save state,
// update IMASK, clear halted, schedule the multi-cycle exception
// dispatch. Returns true.
func (c *CPU) acceptInterrupt(level uint8, vec uint16, fromIRL bool) bool {
	if c.TraceFunc != nil {
		// Use a synthetic opcode $FFFF to mark interrupt acceptance
		c.TraceFunc(c.reg.PC, 0xFFFF)
	}
	c.pendingVal = c.reg.SR
	c.pendingVal2 = c.reg.PC
	c.pendingAddr = uint32(vec)

	ilvl := level
	if ilvl > 15 {
		ilvl = 15
	}
	c.reg.SetIMASK(ilvl)

	if fromIRL && c.irlAck != nil {
		c.irlAck()
	}

	c.halted = false
	c.cycles++
	c.setPending(popException, 4)
	return true
}

// serviceException pushes SR and PC onto the stack and jumps to the
// exception vector handler. Used for synchronous exceptions (address
// errors, illegal instructions). Runs atomically (not decomposed).
func (c *CPU) serviceException(vec uint16) {
	c.reg.R[15] -= 4
	c.bus.Write32(c.reg.R[15], c.reg.SR)
	c.reg.R[15] -= 4
	c.bus.Write32(c.reg.R[15], c.reg.PC)
	c.reg.PC = c.bus.Read32(c.reg.VBR + uint32(vec)*4)
	c.cycles += 5
}
