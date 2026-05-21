// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

// TestNewInitialState verifies the CPU power-on state set by New()
// (cpu.go:123). Covers both master and slave construction.
func TestNewInitialState(t *testing.T) {
	for _, master := range []bool{true, false} {
		suffix := "_slave"
		if master {
			suffix = "_master"
		}
		t.Run("sr_masks_interrupts"+suffix, func(t *testing.T) {
			cpu := New(newTestBus(0x100), master)
			if cpu.reg.SR != srIMask {
				t.Errorf("SR = 0x%08X, want 0x%08X", cpu.reg.SR, uint32(srIMask))
			}
		})
		t.Run("bcr1_initial_value"+suffix, func(t *testing.T) {
			cpu := New(newTestBus(0x100), master)
			if cpu.bcr1 != 0x03F0 {
				t.Errorf("bcr1 = 0x%04X, want 0x03F0 (HM 7.2.1)", cpu.bcr1)
			}
		})
		t.Run("lastLoadReg_sentinel"+suffix, func(t *testing.T) {
			cpu := New(newTestBus(0x100), master)
			if cpu.lastLoadReg != 0xFF {
				t.Errorf("lastLoadReg = 0x%02X, want 0xFF", cpu.lastLoadReg)
			}
		})
		t.Run("no_pending_op"+suffix, func(t *testing.T) {
			cpu := New(newTestBus(0x100), master)
			if cpu.pendingOp != popNone {
				t.Errorf("pendingOp = %d, want popNone", cpu.pendingOp)
			}
		})
		t.Run("not_halted"+suffix, func(t *testing.T) {
			cpu := New(newTestBus(0x100), master)
			if cpu.Halted() {
				t.Error("Halted() = true, want false")
			}
		})
		t.Run("cycles_zero"+suffix, func(t *testing.T) {
			cpu := New(newTestBus(0x100), master)
			if cpu.Cycles() != 0 {
				t.Errorf("Cycles() = %d, want 0", cpu.Cycles())
			}
		})
		t.Run("master_flag_preserved"+suffix, func(t *testing.T) {
			cpu := New(newTestBus(0x100), master)
			if cpu.isMaster != master {
				t.Errorf("isMaster = %v, want %v", cpu.isMaster, master)
			}
		})
		t.Run("dmac_bus_wired"+suffix, func(t *testing.T) {
			bus := newTestBus(0x100)
			cpu := New(bus, master)
			if cpu.dmac.bus != bus {
				t.Error("dmac.bus not wired to constructor bus")
			}
		})
		t.Run("pc_sp_zero_pre_vectors"+suffix, func(t *testing.T) {
			bus := newTestBus(0x100)
			bus.Write32(0x00, 0xAAAABBBB)
			bus.Write32(0x04, 0xCCCCDDDD)
			cpu := New(bus, master)
			if cpu.reg.PC != 0 {
				t.Errorf("PC = 0x%08X before LoadResetVectors, want 0", cpu.reg.PC)
			}
			if cpu.reg.R[15] != 0 {
				t.Errorf("R15 = 0x%08X before LoadResetVectors, want 0", cpu.reg.R[15])
			}
		})
	}
}

// TestLoadResetVectors verifies PC/SP are loaded from the vector table
// at 0x00000000 and 0x00000004 (cpu.go:138).
func TestLoadResetVectors(t *testing.T) {
	t.Run("loads_pc_from_address_0", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0x12345678)
		bus.Write32(0x04, 0x000000F0)
		cpu := New(bus, true)
		cpu.LoadResetVectors()
		if cpu.reg.PC != 0x12345678 {
			t.Errorf("PC = 0x%08X, want 0x12345678", cpu.reg.PC)
		}
	})
	t.Run("loads_sp_from_address_4", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xCAFEBABE)
		cpu := New(bus, true)
		cpu.LoadResetVectors()
		if cpu.reg.R[15] != 0xCAFEBABE {
			t.Errorf("R15 = 0x%08X, want 0xCAFEBABE", cpu.reg.R[15])
		}
	})
	t.Run("preserves_other_gp_regs", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		for i := 0; i < 15; i++ {
			cpu.reg.R[i] = uint32(0xA000 + i)
		}
		cpu.LoadResetVectors()
		for i := 0; i < 15; i++ {
			if cpu.reg.R[i] != uint32(0xA000+i) {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", i, cpu.reg.R[i], uint32(0xA000+i))
			}
		}
	})
	t.Run("preserves_control_regs", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		cpu.reg.GBR = 0x11111111
		cpu.reg.VBR = 0x22222222
		cpu.reg.PR = 0x33333333
		cpu.reg.MACH = 0x44444444
		cpu.reg.MACL = 0x55555555
		cpu.LoadResetVectors()
		if cpu.reg.GBR != 0x11111111 {
			t.Errorf("GBR = 0x%08X, want 0x11111111", cpu.reg.GBR)
		}
		if cpu.reg.VBR != 0x22222222 {
			t.Errorf("VBR = 0x%08X, want 0x22222222", cpu.reg.VBR)
		}
		if cpu.reg.PR != 0x33333333 {
			t.Errorf("PR = 0x%08X, want 0x33333333", cpu.reg.PR)
		}
		if cpu.reg.MACH != 0x44444444 {
			t.Errorf("MACH = 0x%08X, want 0x44444444", cpu.reg.MACH)
		}
		if cpu.reg.MACL != 0x55555555 {
			t.Errorf("MACL = 0x%08X, want 0x55555555", cpu.reg.MACL)
		}
	})
	t.Run("big_endian_decode", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.mem[0] = 0x01
		bus.mem[1] = 0x02
		bus.mem[2] = 0x03
		bus.mem[3] = 0x04
		bus.mem[4] = 0x05
		bus.mem[5] = 0x06
		bus.mem[6] = 0x07
		bus.mem[7] = 0x08
		cpu := New(bus, true)
		cpu.LoadResetVectors()
		if cpu.reg.PC != 0x01020304 {
			t.Errorf("PC = 0x%08X, want 0x01020304 (big-endian)", cpu.reg.PC)
		}
		if cpu.reg.R[15] != 0x05060708 {
			t.Errorf("R15 = 0x%08X, want 0x05060708", cpu.reg.R[15])
		}
	})
	t.Run("callable_multiple_times", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0xAAAA0000)
		bus.Write32(0x04, 0xBBBB0000)
		cpu := New(bus, true)
		cpu.LoadResetVectors()
		// Overwrite vectors then reload.
		bus.Write32(0x00, 0xCCCC0000)
		bus.Write32(0x04, 0xDDDD0000)
		cpu.LoadResetVectors()
		if cpu.reg.PC != 0xCCCC0000 {
			t.Errorf("PC after second load = 0x%08X, want 0xCCCC0000", cpu.reg.PC)
		}
		if cpu.reg.R[15] != 0xDDDD0000 {
			t.Errorf("R15 after second load = 0x%08X, want 0xDDDD0000", cpu.reg.R[15])
		}
	})
}

// dirtyResettableFields sets every field that Reset() is supposed to
// clear to a distinctive non-default value. Helper for TestReset.
func dirtyResettableFields(c *CPU) {
	for i := 0; i < 16; i++ {
		c.reg.R[i] = uint32(0xDEAD0000 + i)
	}
	c.reg.PC = 0xCAFEBABE
	c.reg.PR = 0x11111111
	c.reg.GBR = 0x22222222
	c.reg.VBR = 0x33333333
	c.reg.MACH = 0x44444444
	c.reg.MACL = 0x55555555
	c.reg.SR = 0x000003FF

	c.ir = 0x1234
	c.halted = true
	c.addrError = true
	c.prevPC = 0xABCD1234
	c.inDelay = true
	c.delayPC = 0x87654321
	c.nmiPending = true
	c.intInhibit = true
	c.irlLevel = 7
	c.irlVec = 0x3F
	c.pendingOp = popTAS
	c.pendingStep = 2
	c.pendingCount = 3
	c.pendingN = 9
	c.pendingAddr = 0xBEEF
	c.pendingVal = 0x1234
	c.pendingVal2 = 0x5678
	c.pendingImm = 0x20
	c.lastLoadReg = 5
	c.deferredOp = 0xABCD
	c.hasDeferred = true
	c.loadUseStall = true
	c.multiplierBusyUntil = 0x12345678
	c.stepBus = BusWrite
	c.branchTaken = true
	c.ccr = 0xFF
	c.sbycr = 0xDF
	c.bcr1 = 0xABCD
}

// TestReset verifies field-by-field clearing and peripheral reset
// (cpu.go:149). Every resettable field gets its own subtest so
// failures are easy to localize.
func TestReset(t *testing.T) {
	// Shared setup: a bus with reset vectors so Reset's LoadResetVectors
	// call at the end has something sensible to load.
	setup := func() *CPU {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		dirtyResettableFields(cpu)
		return cpu
	}

	t.Run("clears_gp_regs_r0_r14", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		for i := 0; i < 15; i++ {
			if cpu.reg.R[i] != 0 {
				t.Errorf("R[%d] = 0x%08X, want 0", i, cpu.reg.R[i])
			}
		}
	})
	t.Run("loads_sp_from_vector_post_reset", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.reg.R[15] != 0xF0 {
			t.Errorf("R15 = 0x%08X, want 0xF0", cpu.reg.R[15])
		}
	})
	t.Run("loads_pc_from_vector_post_reset", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.reg.PC != 0x10 {
			t.Errorf("PC = 0x%08X, want 0x10", cpu.reg.PC)
		}
	})
	t.Run("clears_pr_gbr_vbr_mach_macl", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.reg.PR != 0 {
			t.Errorf("PR = 0x%08X, want 0", cpu.reg.PR)
		}
		if cpu.reg.GBR != 0 {
			t.Errorf("GBR = 0x%08X, want 0", cpu.reg.GBR)
		}
		if cpu.reg.VBR != 0 {
			t.Errorf("VBR = 0x%08X, want 0", cpu.reg.VBR)
		}
		if cpu.reg.MACH != 0 {
			t.Errorf("MACH = 0x%08X, want 0", cpu.reg.MACH)
		}
		if cpu.reg.MACL != 0 {
			t.Errorf("MACL = 0x%08X, want 0", cpu.reg.MACL)
		}
	})
	t.Run("clears_ir", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.ir != 0 {
			t.Errorf("ir = 0x%04X, want 0", cpu.ir)
		}
	})
	t.Run("sr_imask_after_reset", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.reg.SR != srIMask {
			t.Errorf("SR = 0x%08X, want 0x%08X", cpu.reg.SR, uint32(srIMask))
		}
	})
	t.Run("clears_halted", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.halted {
			t.Error("halted = true, want false")
		}
	})
	t.Run("clears_addr_error", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.addrError {
			t.Error("addrError = true, want false")
		}
	})
	t.Run("clears_delay_state", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.inDelay {
			t.Error("inDelay = true, want false")
		}
		if cpu.delayPC != 0 {
			t.Errorf("delayPC = 0x%08X, want 0", cpu.delayPC)
		}
	})
	t.Run("clears_prev_pc", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.prevPC != 0 {
			t.Errorf("prevPC = 0x%08X, want 0", cpu.prevPC)
		}
	})
	t.Run("clears_nmi_pending", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.nmiPending {
			t.Error("nmiPending = true, want false")
		}
	})
	t.Run("clears_int_inhibit", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.intInhibit {
			t.Error("intInhibit = true, want false")
		}
	})
	t.Run("clears_irl_level_and_vec", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.irlLevel != 0 {
			t.Errorf("irlLevel = %d, want 0", cpu.irlLevel)
		}
		if cpu.irlVec != 0 {
			t.Errorf("irlVec = 0x%04X, want 0", cpu.irlVec)
		}
	})
	t.Run("clears_pending_op", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.pendingOp != popNone {
			t.Errorf("pendingOp = %d, want popNone", cpu.pendingOp)
		}
		if cpu.pendingStep != 0 {
			t.Errorf("pendingStep = %d, want 0", cpu.pendingStep)
		}
		if cpu.pendingCount != 0 {
			t.Errorf("pendingCount = %d, want 0", cpu.pendingCount)
		}
	})
	t.Run("clears_deferred_op", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.hasDeferred {
			t.Error("hasDeferred = true, want false")
		}
		if cpu.deferredOp != 0 {
			t.Errorf("deferredOp = 0x%04X, want 0", cpu.deferredOp)
		}
	})
	t.Run("clears_load_use_state", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.loadUseStall {
			t.Error("loadUseStall = true, want false")
		}
		if cpu.lastLoadReg != 0xFF {
			t.Errorf("lastLoadReg = 0x%02X, want 0xFF", cpu.lastLoadReg)
		}
	})
	t.Run("clears_multiplier_busy_until", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.multiplierBusyUntil != 0 {
			t.Errorf("multiplierBusyUntil = %d, want 0", cpu.multiplierBusyUntil)
		}
	})
	t.Run("clears_step_bus_and_branch_taken", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.stepBus != BusNone {
			t.Errorf("stepBus = %d, want BusNone", cpu.stepBus)
		}
		if cpu.branchTaken {
			t.Error("branchTaken = true, want false")
		}
	})
	t.Run("clears_ccr_and_sbycr", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.ccr != 0 {
			t.Errorf("ccr = 0x%02X, want 0", cpu.ccr)
		}
		if cpu.sbycr != 0 {
			t.Errorf("sbycr = 0x%02X, want 0", cpu.sbycr)
		}
	})
	t.Run("restores_bcr1_default", func(t *testing.T) {
		cpu := setup()
		cpu.Reset()
		if cpu.bcr1 != 0x03F0 {
			t.Errorf("bcr1 = 0x%04X, want 0x03F0", cpu.bcr1)
		}
	})

	t.Run("preserves_isMaster", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, false) // slave
		dirtyResettableFields(cpu)
		cpu.Reset()
		if cpu.isMaster {
			t.Error("isMaster flipped by Reset()")
		}
	})
	t.Run("preserves_cycles_counter", func(t *testing.T) {
		cpu := setup()
		cpu.cycles = 12345
		cpu.Reset()
		if cpu.Cycles() != 12345 {
			t.Errorf("Cycles() = %d after Reset, want 12345 (preserved)", cpu.Cycles())
		}
	})
	t.Run("preserves_cache_data", func(t *testing.T) {
		cpu := setup()
		cpu.cacheData[0] = 0xAA
		cpu.cacheData[100] = 0xBB
		cpu.cacheData[4095] = 0xCC
		cpu.Reset()
		if cpu.cacheData[0] != 0xAA || cpu.cacheData[100] != 0xBB || cpu.cacheData[4095] != 0xCC {
			t.Error("cacheData should survive manual reset (scratch RAM)")
		}
	})
	t.Run("preserves_trace_func", func(t *testing.T) {
		cpu := setup()
		fired := false
		cpu.TraceFunc = func(pc uint32, op uint16) { fired = true }
		cpu.Reset()
		if cpu.TraceFunc == nil {
			t.Fatal("TraceFunc cleared by Reset()")
		}
		cpu.TraceFunc(0, 0)
		if !fired {
			t.Error("TraceFunc callback invocation lost")
		}
	})

	t.Run("peripheral_frt_reset", func(t *testing.T) {
		cpu := setup()
		cpu.frt.ocra = 0x1234
		cpu.Reset()
		if cpu.frt.ocra != 0xFFFF {
			t.Errorf("FRT.ocra = 0x%04X, want 0xFFFF", cpu.frt.ocra)
		}
	})
	t.Run("peripheral_divu_reset", func(t *testing.T) {
		cpu := setup()
		cpu.divu.dvcr = 0x3
		cpu.Reset()
		if cpu.divu.dvcr != 0 {
			t.Errorf("DIVU.dvcr = 0x%08X, want 0", cpu.divu.dvcr)
		}
	})
	t.Run("peripheral_dmac_reset", func(t *testing.T) {
		cpu := setup()
		cpu.dmac.dmaor = 0x0001
		cpu.Reset()
		if cpu.dmac.dmaor != 0 {
			t.Errorf("DMAC.dmaor = 0x%04X, want 0", cpu.dmac.dmaor)
		}
	})
	t.Run("peripheral_wdt_reset", func(t *testing.T) {
		cpu := setup()
		cpu.wdt.wtcsr = 0x55
		cpu.Reset()
		if cpu.wdt.wtcsr != 0x18 {
			t.Errorf("WDT.wtcsr = 0x%02X, want 0x18", cpu.wdt.wtcsr)
		}
	})
	t.Run("peripheral_intc_reset", func(t *testing.T) {
		cpu := setup()
		cpu.intc.icr = 0x1234
		cpu.Reset()
		if cpu.intc.icr != 0x8000 {
			t.Errorf("INTC.icr = 0x%04X, want 0x8000", cpu.intc.icr)
		}
	})

	t.Run("reset_during_pending_op", func(t *testing.T) {
		cpu := setup()
		cpu.pendingOp = popTAS
		cpu.pendingStep = 1
		cpu.pendingCount = 3
		cpu.Reset()
		if cpu.pendingOp != popNone || cpu.pendingStep != 0 || cpu.pendingCount != 0 {
			t.Errorf("pending state not cleared: op=%d step=%d count=%d",
				cpu.pendingOp, cpu.pendingStep, cpu.pendingCount)
		}
	})
	t.Run("reset_during_delay_slot", func(t *testing.T) {
		cpu := setup()
		cpu.inDelay = true
		cpu.delayPC = 0xDEADBEEF
		cpu.Reset()
		if cpu.inDelay {
			t.Error("inDelay = true after Reset, want false")
		}
		if cpu.delayPC != 0 {
			t.Errorf("delayPC = 0x%08X after Reset, want 0", cpu.delayPC)
		}
	})
}

// TestCycles verifies the cycle counter accessor.
func TestCycles(t *testing.T) {
	t.Run("starts_at_zero", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		if cpu.Cycles() != 0 {
			t.Errorf("Cycles() = %d, want 0", cpu.Cycles())
		}
	})
	t.Run("increments_on_clock", func(t *testing.T) {
		// NOP is 0x0009 and completes in 1 cycle.
		cpu := newDecodeTestCPU(nopOpcode)
		// Fill the next few instructions with NOPs too so multi-step
		// progression stays on NOP.
		for addr := uint32(0x12); addr < 0x20; addr += 2 {
			cpu.bus.Write16(addr, nopOpcode)
		}
		start := cpu.Cycles()
		for i := 0; i < 5; i++ {
			cpu.Clock()
		}
		if cpu.Cycles()-start != 5 {
			t.Errorf("Cycles delta = %d, want 5", cpu.Cycles()-start)
		}
	})
	t.Run("monotonic_across_resets", func(t *testing.T) {
		bus := newTestBus(0x100)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		cpu.cycles = 777
		cpu.Reset()
		if cpu.Cycles() != 777 {
			t.Errorf("Cycles() = %d after Reset, want 777 (preserved)", cpu.Cycles())
		}
	})
}

// TestHalted verifies the Halted() accessor across SLEEP and wake.
func TestHalted(t *testing.T) {
	t.Run("false_before_sleep", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		if cpu.Halted() {
			t.Error("Halted() = true at startup, want false")
		}
	})
	t.Run("true_after_sleep_ex_cycle", func(t *testing.T) {
		// SLEEP is 0x001B.
		cpu := newDecodeTestCPU(0x001B)
		cpu.Clock()
		if !cpu.Halted() {
			t.Error("Halted() = false after SLEEP executed, want true")
		}
	})
	t.Run("false_after_nmi_wakes", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x001B)
		// Fill delay-slot and exception-dispatch memory. SLEEP + pending
		// stalls + NMI acceptance require a small runway.
		for addr := uint32(0x12); addr < 0x40; addr += 2 {
			cpu.bus.Write16(addr, nopOpcode)
		}
		// Set up VBR and a plausible NMI vector (vector 11).
		cpu.reg.VBR = 0x40
		cpu.bus.Write32(0x40+11*4, 0x80)
		// NMI vector handler starts at 0x80 with NOPs.
		for addr := uint32(0x80); addr < 0xA0; addr += 2 {
			cpu.bus.Write16(addr, nopOpcode)
		}
		// Unmask everything so NMI (level 16) is accepted.
		cpu.reg.SR = 0

		// First Clock runs SLEEP's EX stage.
		cpu.Clock()
		if !cpu.Halted() {
			t.Fatal("CPU not halted after SLEEP EX")
		}
		// Fire NMI and clock until halted is cleared by acceptInterrupt.
		cpu.NMI()
		for i := 0; i < 16 && cpu.Halted(); i++ {
			cpu.Clock()
		}
		if cpu.Halted() {
			t.Error("Halted() still true after NMI; expected wake")
		}
	})
}

// TestRegisters verifies the Registers() snapshot accessor.
func TestRegisters(t *testing.T) {
	t.Run("returns_current_values", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.reg.R[3] = 0xDEADBEEF
		cpu.reg.PC = 0x12345678
		cpu.reg.PR = 0xAABBCCDD
		snap := cpu.Registers()
		if snap.R[3] != 0xDEADBEEF {
			t.Errorf("snap.R[3] = 0x%08X, want 0xDEADBEEF", snap.R[3])
		}
		if snap.PC != 0x12345678 {
			t.Errorf("snap.PC = 0x%08X, want 0x12345678", snap.PC)
		}
		if snap.PR != 0xAABBCCDD {
			t.Errorf("snap.PR = 0x%08X, want 0xAABBCCDD", snap.PR)
		}
	})
	t.Run("returns_value_copy", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.reg.R[0] = 0x11111111
		snap := cpu.Registers()
		snap.R[0] = 0xFFFFFFFF
		snap.PC = 0xDEADBEEF
		if cpu.reg.R[0] != 0x11111111 {
			t.Errorf("cpu.reg.R[0] = 0x%08X, want 0x11111111 (snapshot mutation leaked)", cpu.reg.R[0])
		}
		if cpu.reg.PC != 0 {
			t.Errorf("cpu.reg.PC = 0x%08X, want 0 (snapshot mutation leaked)", cpu.reg.PC)
		}
	})
}

// TestSetPC verifies the SetPC setter.
func TestSetPC(t *testing.T) {
	t.Run("sets_pc", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.SetPC(0x12345678)
		if cpu.reg.PC != 0x12345678 {
			t.Errorf("PC = 0x%08X, want 0x12345678", cpu.reg.PC)
		}
	})
	t.Run("odd_address_accepted_without_error", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.SetPC(0x11)
		if cpu.reg.PC != 0x11 {
			t.Errorf("PC = 0x%08X, want 0x11", cpu.reg.PC)
		}
		if cpu.addrError {
			t.Error("SetPC must not raise address error by itself (fetch raises it)")
		}
	})
	t.Run("max_address", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.SetPC(0xFFFFFFFE)
		if cpu.reg.PC != 0xFFFFFFFE {
			t.Errorf("PC = 0x%08X, want 0xFFFFFFFE", cpu.reg.PC)
		}
	})
	t.Run("does_not_clear_pending_op", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.pendingOp = popTAS
		cpu.pendingCount = 2
		cpu.SetPC(0x100)
		if cpu.pendingOp != popTAS {
			t.Errorf("pendingOp = %d, want popTAS (SetPC should not touch pending state)", cpu.pendingOp)
		}
	})
}

// TestSetIRL verifies IRL level/vector storage.
func TestSetIRL(t *testing.T) {
	t.Run("stores_level_and_vector", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.SetIRL(5, 0x40)
		if cpu.irlLevel != 5 {
			t.Errorf("irlLevel = %d, want 5", cpu.irlLevel)
		}
		if cpu.irlVec != 0x40 {
			t.Errorf("irlVec = 0x%04X, want 0x40", cpu.irlVec)
		}
	})
	t.Run("level_zero_allowed", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.SetIRL(0, 0)
		if cpu.irlLevel != 0 || cpu.irlVec != 0 {
			t.Errorf("irlLevel=%d irlVec=0x%04X, want 0/0", cpu.irlLevel, cpu.irlVec)
		}
	})
	t.Run("level_max", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.SetIRL(15, 0x2F)
		if cpu.irlLevel != 15 {
			t.Errorf("irlLevel = %d, want 15", cpu.irlLevel)
		}
		if cpu.irlVec != 0x2F {
			t.Errorf("irlVec = 0x%04X, want 0x2F", cpu.irlVec)
		}
	})
}

// TestClearIRL verifies IRL clear.
func TestClearIRL(t *testing.T) {
	t.Run("clears_level_and_vec", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.SetIRL(10, 0x20)
		cpu.ClearIRL()
		if cpu.irlLevel != 0 {
			t.Errorf("irlLevel = %d, want 0", cpu.irlLevel)
		}
		if cpu.irlVec != 0 {
			t.Errorf("irlVec = 0x%04X, want 0", cpu.irlVec)
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.ClearIRL()
		cpu.ClearIRL()
		if cpu.irlLevel != 0 || cpu.irlVec != 0 {
			t.Errorf("after double-clear: irlLevel=%d irlVec=0x%04X", cpu.irlLevel, cpu.irlVec)
		}
	})
}

// TestSetIRLAck verifies the IRL accept callback.
func TestSetIRLAck(t *testing.T) {
	t.Run("callback_stored", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cb := func() {}
		cpu.SetIRLAck(cb)
		if cpu.irlAck == nil {
			t.Error("irlAck nil after SetIRLAck")
		}
	})
	t.Run("callback_fired_on_accept", func(t *testing.T) {
		// Arrange a CPU that will accept an IRL level-10 interrupt.
		bus := newTestBus(0x200)
		bus.Write32(0x00, 0x10)
		bus.Write32(0x04, 0xF0)
		cpu := New(bus, true)
		cpu.LoadResetVectors()
		// Unmask everything.
		cpu.reg.SR = 0
		// Set up VBR with a vector at IRL vec 0x40 -> handler at 0x100.
		cpu.reg.VBR = 0
		bus.Write32(0x40*4, 0x100)
		for addr := uint32(0x10); addr < 0x20; addr += 2 {
			bus.Write16(addr, nopOpcode)
		}
		for addr := uint32(0x100); addr < 0x110; addr += 2 {
			bus.Write16(addr, nopOpcode)
		}
		count := 0
		cpu.SetIRLAck(func() { count++ })
		cpu.SetIRL(10, 0x40)

		// Clock through until the IRL callback has had a chance to fire.
		// acceptInterrupt calls irlAck synchronously inside Clock() when
		// processInterrupt returns true.
		for i := 0; i < 8 && count == 0; i++ {
			cpu.Clock()
		}
		if count != 1 {
			t.Errorf("irlAck called %d times, want 1", count)
		}
	})
	t.Run("nil_callback_safe_if_never_accepted", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.reg.SR = srIMask // mask everything, IRL never accepted
		cpu.SetIRL(5, 0x40)  // level 5 <= IMASK 15, never accepts
		// Running a Clock cycle must not panic with nil irlAck.
		// The bus has no program, but processInterrupt happens before fetch.
		cpu.SetPC(0x10)
		cpu.bus.Write16(0x10, nopOpcode)
		cpu.Clock()
	})
	t.Run("callback_replaceable", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		firstCalled := false
		secondCalled := false
		cpu.SetIRLAck(func() { firstCalled = true })
		cpu.SetIRLAck(func() { secondCalled = true })
		cpu.irlAck() // invoke whatever is currently stored
		if firstCalled {
			t.Error("first callback fired after replacement")
		}
		if !secondCalled {
			t.Error("replacement callback did not fire")
		}
	})
}

// TestNMI verifies the NMI() API side effects (INTC.NMIL, DMAOR.NMIF,
// nmiPending). Does not test interrupt acceptance (that lives in
// interrupt_test.go).
func TestNMI(t *testing.T) {
	t.Run("sets_nmi_pending", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.nmiPending = false
		cpu.NMI()
		if !cpu.nmiPending {
			t.Error("nmiPending = false after NMI(), want true")
		}
	})
	t.Run("sets_intc_nmil", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		// Clear NMIL first so we observe NMI() setting it.
		cpu.intc.SetNMIL(false)
		cpu.NMI()
		if cpu.intc.icr&0x8000 == 0 {
			t.Errorf("INTC.icr MSB not set after NMI (icr=0x%04X)", cpu.intc.icr)
		}
	})
	t.Run("sets_dmaor_nmif", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.dmac.dmaor = 0
		cpu.NMI()
		if cpu.dmac.dmaor&0x02 == 0 {
			t.Errorf("DMAC.dmaor NMIF bit not set after NMI (dmaor=0x%04X)", cpu.dmac.dmaor)
		}
	})
	t.Run("idempotent_while_pending", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.NMI()
		cpu.NMI()
		if !cpu.nmiPending {
			t.Error("nmiPending cleared by second NMI()")
		}
		if cpu.dmac.dmaor&0x02 == 0 {
			t.Error("DMAOR.NMIF cleared by second NMI()")
		}
	})
	t.Run("callable_while_halted", func(t *testing.T) {
		cpu := New(newTestBus(0x100), true)
		cpu.halted = true
		cpu.NMI()
		if !cpu.nmiPending {
			t.Error("NMI() did not set nmiPending while halted")
		}
	})
}
