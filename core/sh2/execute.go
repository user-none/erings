// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// execute fetches and executes one instruction, handling delayed branches
// and load-use hazard detection.
func (c *CPU) execute() {
	wasDelay := c.inDelay

	// HLE BIOS trap. The HLE BIOS publishes function-pointer slots
	// holding magic addresses in $A0000xxx; when game code JSRs
	// through one of those slots, PC lands here. Run the registered
	// Go service and act as if an RTS executed (PC = PR). No bus
	// fetch happens — the bus has no special case for the magic
	// range. Skipped inside a delay slot so a (theoretical) delay-
	// slot landing here preserves the parent branch's semantics.
	if !wasDelay && c.HLEHook != nil && c.reg.PC>>12 == 0xA0000 {
		c.prevPC = c.reg.PC
		c.lastLoadReg = 0xFF
		c.HLEHook(c.reg.PC)
		c.reg.PC = c.reg.PR
		return
	}

	var op uint16
	if c.hasDeferred {
		op = c.deferredOp
		c.hasDeferred = false
	} else {
		c.prevPC = c.reg.PC

		op = c.fetchPC()
		if c.addrError {
			return
		}

		// Check load-use hazard before executing
		prevLoad := c.lastLoadReg
		if prevLoad != 0xFF && loadUseHazard(op, prevLoad) {
			c.deferredOp = op
			c.hasDeferred = true
			c.loadUseStall = true
			c.cycles++
			return
		}
	}

	c.lastLoadReg = 0xFF // clear before decode; load ops will set it
	c.ir = op
	if c.TraceFunc != nil {
		c.TraceFunc(c.prevPC, op)
	}
	handlerTable[op](c)

	if c.addrError {
		return
	}

	// After the delay slot instruction executes, jump to the branch target.
	if wasDelay {
		c.reg.PC = c.delayPC
		c.inDelay = false
	}
}

// delayBranch sets up a delayed branch to the target address.
// The next instruction (delay slot) executes before the branch takes effect.
func (c *CPU) delayBranch(target uint32) {
	c.delayPC = target
	c.inDelay = true
}
