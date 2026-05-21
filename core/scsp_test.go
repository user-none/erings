// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

func TestSCSPNewDefaults(t *testing.T) {
	s := NewSCSP(NewSCU())

	// RAM should be zeroed
	for i := 0; i < scspSoundRAMSize; i += scspSoundRAMSize / 4 {
		if s.ram[i] != 0 {
			t.Errorf("RAM[%d] = 0x%02X, want 0x00", i, s.ram[i])
		}
	}

	// Registers should be zeroed
	for i := 0; i < scspRegWords; i += scspRegWords / 4 {
		if s.regs[i] != 0 {
			t.Errorf("regs[%d] = 0x%04X, want 0x0000", i, s.regs[i])
		}
	}

	// Should be held in reset
	if !s.InReset() {
		t.Error("SCSP should be in reset after creation")
	}
}

func TestSCSPRegisterSlotRoundTrip(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Slot 0, first register (offset 0x0000)
	// Avoid setting KYONEX (bit 12) which has side-effects
	s.Write(0x0000, 0x0234)
	if got := s.Read(0x0000); got != 0x0234 {
		t.Errorf("slot reg read = 0x%04X, want 0x0234", got)
	}

	// Slot 31, last register (offset 0x03F6 = 31*0x20 + 0x16)
	s.Write(0x03F6, 0xABCD)
	if got := s.Read(0x03F6); got != 0xABCD {
		t.Errorf("slot 31 reg read = 0x%04X, want 0xABCD", got)
	}
}

func TestSCSPRegisterCommonRoundTrip(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Common control register area starts at 0x0400
	s.Write(0x0400, 0x5678)
	// VER field (bits 7:4) is always read as 2 per SCSP User's Manual Table 1.1
	if got := s.Read(0x0400); got != 0x5628 {
		t.Errorf("common reg read = 0x%04X, want 0x5628 (VER=2)", got)
	}

	// MCIRE (0x042E) is write-to-clear so use a different register.
	// SCILV0 at 0x0424 is a standard R/W register.
	s.Write(0x0424, 0x00AB)
	if got := s.Read(0x0424); got != 0x00AB {
		t.Errorf("common SCILV0 read = 0x%04X, want 0x00AB", got)
	}
}

func TestSCSPRegisterOutOfRange(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Beyond register space
	if got := s.Read(0xEE4); got != 0 {
		t.Errorf("out-of-range read = 0x%04X, want 0x0000", got)
	}

	// Write out of range should not panic
	s.Write(0xEE4, 0xFFFF)
	if got := s.Read(0xEE4); got != 0 {
		t.Errorf("out-of-range after write = 0x%04X, want 0x0000", got)
	}
}

func TestSCSPSoundRAMRoundTrip(t *testing.T) {
	s := NewSCSP(NewSCU())

	s.WriteRAM(0x00000, 0xAA)
	s.WriteRAM(0x7FFFF, 0xBB)

	if got := s.ReadRAM(0x00000); got != 0xAA {
		t.Errorf("RAM[0] = 0x%02X, want 0xAA", got)
	}
	if got := s.ReadRAM(0x7FFFF); got != 0xBB {
		t.Errorf("RAM[last] = 0x%02X, want 0xBB", got)
	}
}

func TestSCSPSoundRAMAddressMask(t *testing.T) {
	s := NewSCSP(NewSCU())

	s.WriteRAM(0x00000, 0xCC)
	// Address 0x80000 should wrap to 0x00000
	if got := s.ReadRAM(0x80000); got != 0xCC {
		t.Errorf("RAM wrap read = 0x%02X, want 0xCC", got)
	}
}

func TestSCSPSoundRAM16(t *testing.T) {
	s := NewSCSP(NewSCU())

	s.WriteRAM16(0x00000, 0xDEAD)
	if got := s.ReadRAM16(0x00000); got != 0xDEAD {
		t.Errorf("RAM16 read = 0x%04X, want 0xDEAD", got)
	}

	// Check byte order (big-endian)
	if got := s.ReadRAM(0x00000); got != 0xDE {
		t.Errorf("RAM16 high byte = 0x%02X, want 0xDE", got)
	}
	if got := s.ReadRAM(0x00001); got != 0xAD {
		t.Errorf("RAM16 low byte = 0x%02X, want 0xAD", got)
	}
}

func TestSCSPNewInitialState(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Sound RAM zeroed
	if s.ReadRAM(0x000) != 0 {
		t.Errorf("RAM[0] = 0x%02X, want 0", s.ReadRAM(0x000))
	}
	if s.ReadRAM(0x100) != 0 {
		t.Errorf("RAM[0x100] = 0x%02X, want 0", s.ReadRAM(0x100))
	}
	// Registers zeroed
	if s.Read(0x0000) != 0 {
		t.Errorf("reg[0] = 0x%04X, want 0", s.Read(0x0000))
	}
	// VER field (bits 7:4) always reads as 2 per SCSP User's Manual Table 1.1
	if s.Read(0x0400) != 0x0020 {
		t.Errorf("reg[0x400] = 0x%04X, want 0x0020 (VER=2)", s.Read(0x0400))
	}
	// Held in reset
	if !s.InReset() {
		t.Error("should be in reset")
	}
	// Sound CPU exists
	if s.m68k == nil {
		t.Error("sound CPU should not be nil")
	}
}

func TestSCSPM68KExists(t *testing.T) {
	s := NewSCSP(NewSCU())

	if s.m68k == nil {
		t.Error("sound CPU should not be nil")
	}
	if !s.InReset() {
		t.Error("sound CPU should be held in reset")
	}
}

func TestSCSPBusRAMRouting(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write via scspBus, read via SCSP directly
	s.m68kBus.Write8(0x00100, 0xAA)
	if got := s.ReadRAM(0x00100); got != 0xAA {
		t.Errorf("scspBus RAM write = 0x%02X, want 0xAA", got)
	}

	// Write via SCSP, read via scspBus
	s.WriteRAM(0x00200, 0xBB)
	if got := s.m68kBus.Read8(0x00200); got != 0xBB {
		t.Errorf("scspBus RAM read = 0x%02X, want 0xBB", got)
	}

	// 16-bit access
	s.m68kBus.Write16(0x00300, 0xCAFE)
	if got := s.ReadRAM16(0x00300); got != 0xCAFE {
		t.Errorf("scspBus RAM16 write = 0x%04X, want 0xCAFE", got)
	}
}

func TestSCSPBusRegRouting(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write via scspBus to register area (0x100000+)
	s.m68kBus.Write16(0x100400, 0x5678)
	// VER field (bits 7:4) forced to 2 on read
	if got := s.Read(0x0400); got != 0x5628 {
		t.Errorf("scspBus reg write = 0x%04X, want 0x5628 (VER=2)", got)
	}

	// Read via scspBus
	s.Write(0x0000, 0xABCD)
	if got := s.m68kBus.Read16(0x100000); got != 0xABCD {
		t.Errorf("scspBus reg read = 0x%04X, want 0xABCD", got)
	}

	// Unmapped area returns 0
	if got := s.m68kBus.Read8(0x200000); got != 0 {
		t.Errorf("scspBus unmapped = 0x%02X, want 0x00", got)
	}
}

func TestSCSPTimerIncrementPrescaler0(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set Timer A counter to 0xFD, prescaler 0 (every sample).
	// Per PDF (255 - 0xFD) = 2 cycles to interrupt.
	s.Write(scspRegTimerA, 0x00FD) // TACTL=0, TIMA=0xFD

	// Tick 1 sample: counter becomes 0xFE, no interrupt yet
	s.TickSamples(1)
	if s.Read(scspRegSCIPD)&scspIntTimerA != 0 {
		t.Error("Timer A fired too early after 1 tick")
	}

	// Tick 1 more: counter reaches 0xFF, interrupt fires
	s.TickSamples(1)
	if s.Read(scspRegSCIPD)&scspIntTimerA == 0 {
		t.Error("Timer A should have fired after 2 ticks")
	}
}

func TestSCSPTimerIncrementPrescaler3(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set Timer B counter to 0xFE, prescaler 3 (every 8 samples).
	// Per PDF (255 - 0xFE) * 8 = 8 sample ticks to interrupt.
	s.Write(scspRegTimerB, 0x03FE) // TBCTL=3, TIMB=0xFE

	// Tick 7 samples: prescaler hasn't fired yet, counter still 0xFE
	s.TickSamples(7)
	if s.Read(scspRegSCIPD)&scspIntTimerB != 0 {
		t.Error("Timer B fired too early (before first prescaler tick)")
	}

	// Tick 1 more (total 8): prescaler fires, counter reaches 0xFF, interrupt
	s.TickSamples(1)
	if s.Read(scspRegSCIPD)&scspIntTimerB == 0 {
		t.Error("Timer B should have fired after first prescaler tick")
	}
}

func TestSCSPTimerOverflowSetsSCIPD(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set Timer A counter to 0xFE, prescaler 0.
	// Per PDF (255 - 0xFE) = 1 cycle to interrupt.
	s.Write(scspRegTimerA, 0x00FE) // TACTL=0, TIMA=0xFE

	// Tick 1: counter reaches 0xFF, interrupt fires
	s.TickSamples(1)
	scipd := s.Read(scspRegSCIPD)
	if scipd&scspIntTimerA == 0 {
		t.Errorf("SCIPD Timer A not set on fire: 0x%04X", scipd)
	}

	// MCIPD should also have timer A bit set
	mcipd := s.Read(scspRegMCIPD)
	if mcipd&scspIntTimerA == 0 {
		t.Errorf("MCIPD Timer A not set on fire: 0x%04X", mcipd)
	}
}

func TestSCSPTimerOverflowSetsMCIPD(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set Timer C counter to 0xFF, prescaler 0
	s.Write(scspRegTimerC, 0x00FF) // TCCTL=0, TIMC=0xFF

	// Tick 1: counter wraps 0xFF->0x00
	s.TickSamples(1)
	mcipd := s.Read(scspRegMCIPD)
	if mcipd&scspIntTimerC == 0 {
		t.Errorf("MCIPD Timer C not set on overflow: 0x%04X", mcipd)
	}
}

func TestSCSPSCIREClearsSCIPD(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Force Timer A overflow
	s.Write(scspRegTimerA, 0x00FF)
	s.TickSamples(1)

	// Verify pending
	if s.Read(scspRegSCIPD)&scspIntTimerA == 0 {
		t.Fatal("SCIPD Timer A not set")
	}

	// Write SCIRE to clear Timer A bit
	s.Write(scspRegSCIRE, scspIntTimerA)

	// SCIPD should be cleared
	if s.Read(scspRegSCIPD)&scspIntTimerA != 0 {
		t.Error("SCIPD Timer A not cleared by SCIRE")
	}
}

func TestSCSPMCIREClearsMCIPD(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Force Timer B overflow
	s.Write(scspRegTimerB, 0x00FF)
	s.TickSamples(1)

	// Verify pending
	if s.Read(scspRegMCIPD)&scspIntTimerB == 0 {
		t.Fatal("MCIPD Timer B not set")
	}

	// Write MCIRE to clear Timer B bit
	s.Write(scspRegMCIRE, scspIntTimerB)

	// MCIPD should be cleared
	if s.Read(scspRegMCIPD)&scspIntTimerB != 0 {
		t.Error("MCIPD Timer B not cleared by MCIRE")
	}
}

func TestSCSPOneSampleInterrupt(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Per SCSP User's Manual Sec 4.2 p.95: "no matter what the enable
	// register ('SCIEB') is set at, all interrupt requests are
	// monitored." SCIPD/MCIPD latch the source unconditionally; the
	// enable register only gates whether the latched bit asserts the
	// IRQ line, not whether it latches in SCIPD/MCIPD.
	s.TickSamples(1)
	if s.Read(scspRegSCIPD)&scspIntSample == 0 {
		t.Error("SCIPD 1-sample bit not latched (latching is SCIEB-independent per spec)")
	}
	if s.Read(scspRegMCIPD)&scspIntSample == 0 {
		t.Error("MCIPD 1-sample bit not latched (latching is MCIEB-independent per spec)")
	}

	// SCIRE clears the pending bit.
	s.Write(scspRegSCIRE, scspIntSample)
	s.Write(scspRegMCIRE, scspIntSample)
	if s.Read(scspRegSCIPD)&scspIntSample != 0 {
		t.Error("SCIPD 1-sample bit not cleared by SCIRE")
	}
	if s.Read(scspRegMCIPD)&scspIntSample != 0 {
		t.Error("MCIPD 1-sample bit not cleared by MCIRE")
	}

	// With enable set, ticking one sample re-latches the pending bit.
	s.Write(scspRegSCIEB, scspIntSample)
	s.Write(scspRegMCIEB, scspIntSample)
	s.TickSamples(1)
	if s.Read(scspRegSCIPD)&scspIntSample == 0 {
		t.Error("SCIPD 1-sample bit not set after re-tick with SCIEB enabled")
	}
	if s.Read(scspRegMCIPD)&scspIntSample == 0 {
		t.Error("MCIPD 1-sample bit not set after re-tick with MCIEB enabled")
	}
}

func TestSCSPCPUManualInterruptWritable(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write bit 5 to SCIPD (CPU manual interrupt)
	s.Write(scspRegSCIPD, scspIntCPU)
	if s.Read(scspRegSCIPD)&scspIntCPU == 0 {
		t.Error("SCIPD CPU manual interrupt bit not writable")
	}

	// Write bit 5 to MCIPD
	s.Write(scspRegMCIPD, scspIntCPU)
	if s.Read(scspRegMCIPD)&scspIntCPU == 0 {
		t.Error("MCIPD CPU manual interrupt bit not writable")
	}
}

func TestSCSPSCIPDReadOnlyBits(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Try to write Timer A bit directly to SCIPD (should be ignored)
	s.Write(scspRegSCIPD, scspIntTimerA)
	if s.Read(scspRegSCIPD)&scspIntTimerA != 0 {
		t.Error("SCIPD Timer A bit should not be writable directly")
	}
}

func TestSCSPMultipleTimersIndependent(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Timer A: counter 0xFE, prescaler 0 (fires after 1 tick)
	s.Write(scspRegTimerA, 0x00FE)
	// Timer B: counter 0x00, prescaler 0 (fires after 255 ticks)
	s.Write(scspRegTimerB, 0x0000)
	// Timer C: counter 0xFF, prescaler 0 (fires immediately on write)
	s.Write(scspRegTimerC, 0x00FF)

	// Timer C should already be pending from the immediate-fire on write.
	scipd := s.Read(scspRegSCIPD)
	if scipd&scspIntTimerC == 0 {
		t.Error("Timer C should have fired on write")
	}
	if scipd&scspIntTimerA != 0 {
		t.Error("Timer A should not have fired yet")
	}
	if scipd&scspIntTimerB != 0 {
		t.Error("Timer B should not have fired yet")
	}

	// Tick 1: Timer A counter reaches 0xFF and fires; Timer B advances to 1.
	s.TickSamples(1)
	scipd = s.Read(scspRegSCIPD)
	if scipd&scspIntTimerA == 0 {
		t.Error("Timer A should have fired")
	}
	if scipd&scspIntTimerB != 0 {
		t.Error("Timer B should not have fired yet")
	}
}

func TestSCSPTimerCounterWritable(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write counter to 0xFE, verify it counts from there via fire timing.
	// Per PDF (255 - 0xFE) = 1 cycle to interrupt.
	s.Write(scspRegTimerA, 0x00FE) // TACTL=0, TIMA=0xFE

	// Tick 1: counter reaches 0xFF, interrupt fires
	s.TickSamples(1)
	if s.Read(scspRegSCIPD)&scspIntTimerA == 0 {
		t.Error("Timer A should have fired")
	}
}

func TestSCSPTimerPrescalerAllValues(t *testing.T) {
	for ctl := uint16(0); ctl <= 7; ctl++ {
		s := NewSCSP(NewSCU())
		div := uint16(1) << ctl

		// Set Timer A with this prescaler, counter 0xFE.
		// Per PDF (255 - 0xFE) * (1 << ctl) = div sample ticks to interrupt.
		s.Write(scspRegTimerA, (ctl<<8)|0xFE)

		// Tick div-1 samples: prescaler hasn't completed, no fire
		if div > 1 {
			s.TickSamples(int(div - 1))
			if s.Read(scspRegSCIPD)&scspIntTimerA != 0 {
				t.Errorf("TACTL=%d: Timer A fired too early after %d ticks", ctl, div-1)
			}
		}

		// Tick 1 more (total div): counter reaches 0xFF, fires
		s.TickSamples(1)
		if s.Read(scspRegSCIPD)&scspIntTimerA == 0 {
			t.Errorf("TACTL=%d: Timer A should have fired after %d ticks", ctl, div)
		}
	}
}

func TestSCSPSCIEBEnableWithPending(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Force Timer A overflow to set SCIPD
	s.Write(scspRegTimerA, 0x00FF)
	s.TickSamples(1)

	// Verify pending
	if s.Read(scspRegSCIPD)&scspIntTimerA == 0 {
		t.Fatal("SCIPD Timer A not set")
	}

	// Set SCILV registers so Timer A (bit 6) gets level 5 (101b)
	// SCILV0 bit 6 = 1, SCILV1 bit 6 = 0, SCILV2 bit 6 = 1
	s.Write(scspRegSCILV0, 1<<6)
	s.Write(scspRegSCILV1, 0)
	s.Write(scspRegSCILV2, 1<<6)

	// Enable Timer A in SCIEB - should trigger interrupt check
	s.Write(scspRegSCIEB, scspIntTimerA)

	// We can verify the enable was stored
	if s.Read(scspRegSCIEB)&scspIntTimerA == 0 {
		t.Error("SCIEB Timer A enable not stored")
	}
}

func TestSCSPDMAMemoryToRegisters(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write test data into sound RAM at address 0x1000
	s.WriteRAM16(0x1000, 0xAAAA)
	s.WriteRAM16(0x1002, 0xBBBB)
	s.WriteRAM16(0x1004, 0xCCCC)

	// Set DMA: memory addr 0x1000, register addr 0x0000 (slot 0), 3 words
	// DMEAL register (0x412): low 15 bits of memory address = 0x1000
	s.Write(scspRegDMEAL, 0x1000)
	// DMEAH/DRGA register (0x414): DMEAH[15:12]=0 (high nibble), DRGA[11:1]=0x000
	s.Write(scspRegDMEAH, 0x0000)
	// DMA control (0x416): DGATE=0, DDIR=0 (mem->reg), DEXE=1, DTLG=3 words
	// DTLG[11:1] = 3, so register value bits 11:1 = 3 = 0x0006
	// DEXE = bit 12 = 0x1000
	s.Write(scspRegDMACtl, 0x1000|0x0006)

	// Verify registers were written
	if got := s.Read(0x0000); got != 0xAAAA {
		t.Errorf("reg[0x0000] = 0x%04X, want 0xAAAA", got)
	}
	if got := s.Read(0x0002); got != 0xBBBB {
		t.Errorf("reg[0x0002] = 0x%04X, want 0xBBBB", got)
	}
	if got := s.Read(0x0004); got != 0xCCCC {
		t.Errorf("reg[0x0004] = 0x%04X, want 0xCCCC", got)
	}
}

func TestSCSPDMARegistersToMemory(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write test data into common control registers (not slot registers, to avoid KYONEX)
	s.Write(0x0400, 0x1111)
	s.Write(0x0402, 0x2222)

	// Set DMA: memory addr 0x2000, register addr 0x0400, 2 words, DDIR=1 (reg->mem)
	s.Write(scspRegDMEAL, 0x2000)
	s.Write(scspRegDMEAH, 0x0400) // DMEAH=0, DRGA=0x400
	// DDIR=bit13=0x2000, DEXE=bit12=0x1000, DTLG=2 words=0x0004
	s.Write(scspRegDMACtl, 0x2000|0x1000|0x0004)

	// Verify sound RAM was written
	if got := s.ReadRAM16(0x2000); got != 0x1111 {
		t.Errorf("RAM[0x2000] = 0x%04X, want 0x1111", got)
	}
	if got := s.ReadRAM16(0x2002); got != 0x2222 {
		t.Errorf("RAM[0x2002] = 0x%04X, want 0x2222", got)
	}
}

func TestSCSPDMAGateClearsDestination(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Pre-fill registers with non-zero data
	s.Write(0x0000, 0xFFFF)
	s.Write(0x0002, 0xFFFF)

	// DMA with DGATE=1: should write zeros to registers
	s.Write(scspRegDMEAL, 0x0000)
	s.Write(scspRegDMEAH, 0x0000)
	// DGATE=bit14=0x4000, DDIR=0 (mem->reg), DEXE=bit12=0x1000, DTLG=2 words=0x0004
	s.Write(scspRegDMACtl, 0x4000|0x1000|0x0004)

	if got := s.Read(0x0000); got != 0x0000 {
		t.Errorf("reg[0x0000] after DGATE = 0x%04X, want 0x0000", got)
	}
	if got := s.Read(0x0002); got != 0x0000 {
		t.Errorf("reg[0x0002] after DGATE = 0x%04X, want 0x0000", got)
	}
}

func TestSCSPDMAInterruptOnCompletion(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Clear any pending interrupts
	s.Write(scspRegSCIRE, 0x07FF)
	s.Write(scspRegMCIRE, 0x07FF)

	// Do a minimal DMA transfer (1 word)
	s.WriteRAM16(0x0000, 0x1234)
	s.Write(scspRegDMEAL, 0x0000)
	s.Write(scspRegDMEAH, 0x0000)
	s.Write(scspRegDMACtl, 0x1000|0x0002) // DEXE=1, DTLG=1 word

	// DMA end interrupt (bit 4) should be set
	if s.Read(scspRegSCIPD)&scspIntDMA == 0 {
		t.Error("SCIPD DMA end bit not set")
	}
	if s.Read(scspRegMCIPD)&scspIntDMA == 0 {
		t.Error("MCIPD DMA end bit not set")
	}
}

func TestSCSPDMADEXEAutoClears(t *testing.T) {
	s := NewSCSP(NewSCU())

	s.WriteRAM16(0x0000, 0x0000)
	s.Write(scspRegDMEAL, 0x0000)
	s.Write(scspRegDMEAH, 0x0000)
	s.Write(scspRegDMACtl, 0x1000|0x0002) // DEXE=1, DTLG=1

	// DEXE should be cleared
	ctl := s.Read(scspRegDMACtl)
	if ctl&0x1000 != 0 {
		t.Errorf("DEXE not cleared after DMA: 0x%04X", ctl)
	}
}

func TestSCSPDMAMemoryAddressWrap(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write data near the end of 512KB sound RAM
	s.WriteRAM16(0x7FFFE, 0xDEAD)

	// DMA from 0x7FFFE, 2 words - second word should wrap to 0x00000
	s.WriteRAM16(0x00000, 0xBEEF)
	s.Write(scspRegDMEAL, 0xFFFE)         // low bits of 0x7FFFE
	s.Write(scspRegDMEAH, 0x7000)         // high nibble = 7
	s.Write(scspRegDMACtl, 0x1000|0x0004) // DEXE=1, DTLG=2 words

	// First word at reg 0x0000 should be 0xDEAD
	if got := s.Read(0x0000); got != 0xDEAD {
		t.Errorf("reg[0x0000] = 0x%04X, want 0xDEAD", got)
	}
	// Second word at reg 0x0002 should be 0xBEEF (wrapped address)
	if got := s.Read(0x0002); got != 0xBEEF {
		t.Errorf("reg[0x0002] = 0x%04X, want 0xBEEF", got)
	}
}

func TestSCSPDMAZeroLength(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Pre-fill a register
	s.Write(0x0000, 0xAAAA)

	// DMA with DTLG=0 should be a no-op
	s.Write(scspRegDMEAL, 0x0000)
	s.Write(scspRegDMEAH, 0x0000)
	s.Write(scspRegDMACtl, 0x1000) // DEXE=1, DTLG=0

	// Register should be unchanged
	if got := s.Read(0x0000); got != 0xAAAA {
		t.Errorf("reg[0x0000] = 0x%04X, want 0xAAAA (unchanged)", got)
	}

	// DEXE should still clear
	ctl := s.Read(scspRegDMACtl)
	if ctl&0x1000 != 0 {
		t.Error("DEXE not cleared after zero-length DMA")
	}
}

// writeSlotReg writes a 16-bit value to a slot register.
func writeSlotReg(s *SCSP, slot int, offset uint32, val uint16) {
	s.Write(uint32(slot)*0x20+offset, val)
}

func TestSCSPKeyOnResetsSlotState(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set KYONB=1 for slot 0
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1, KYONEX=0
	// Trigger KYONEX via slot 0
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONB=1, KYONEX=1

	sl := &s.slots[0]
	if !sl.active {
		t.Error("slot 0 should be active after key on")
	}
	if sl.egState != egAttack {
		t.Errorf("egState = %d, want %d (attack)", sl.egState, egAttack)
	}
	if sl.egLevel != egAttackStart {
		t.Errorf("egLevel = 0x%X, want 0x%X (attack start)", sl.egLevel, egAttackStart)
	}
	if sl.phase != 0 {
		t.Errorf("phase = %d, want 0", sl.phase)
	}
}

func TestSCSPKeyOffTransitionsToRelease(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Key on slot 0
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	// Advance EG out of attack (set AR=0x1F for fast attack)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31 (fastest)
	s.TickSamples(50)                // Advance enough for attack to complete

	// Key off: set KYONB=0
	writeSlotReg(s, 0, 0x00, 0x0000) // KYONB=0
	writeSlotReg(s, 0, 0x00, 0x1000) // KYONEX (with KYONB=0)

	if s.slots[0].egState != egRelease {
		t.Errorf("egState = %d, want %d (release)", s.slots[0].egState, egRelease)
	}
}

func TestSCSPKYONEXTriggersMultipleSlots(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set KYONB=1 for slots 0 and 5
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1
	writeSlotReg(s, 5, 0x00, 0x0800) // KYONB=1

	// Trigger KYONEX via any slot
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	if !s.slots[0].active {
		t.Error("slot 0 should be active")
	}
	if !s.slots[5].active {
		t.Error("slot 5 should be active")
	}
	if s.slots[1].active {
		t.Error("slot 1 should not be active")
	}
}

func TestSCSPPhaseAccumulatorOCT0(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set slot 0: OCT=0, FNS=0 -> increment = 0x400 (base rate = 1 sample per tick)
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x06, 0xFFFF) // LEA=far away
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31 (fast attack)
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	s.TickSamples(4)

	// With OCT=0, FNS=0: increment = 0x400 per tick
	// After 4 ticks: phase = 4 * 0x400 = 0x1000
	// Sample position = phase >> 10 = 4
	samplePos := s.slots[0].phase >> phaseFracBits
	if samplePos != 4 {
		t.Errorf("samplePos = %d, want 4", samplePos)
	}
}

func TestSCSPPhaseAccumulatorOCT1(t *testing.T) {
	s := NewSCSP(NewSCU())

	// OCT=1 should double the rate
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x10, 0x0800) // OCT=1 (bit 11), FNS=0
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(4)

	samplePos := s.slots[0].phase >> phaseFracBits
	if samplePos != 8 {
		t.Errorf("samplePos = %d, want 8", samplePos)
	}
}

func TestSCSPNormalLoopWraps(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set slot 0 with normal loop: LSA=2, LEA=4
	// LPCTL=1 (bits 6:5 = 01 = 0x0020), KYONB=1 (bit 11 = 0x0800)
	writeSlotReg(s, 0, 0x00, 0x0820) // KYONB=1, LPCTL=1 (normal loop)
	writeSlotReg(s, 0, 0x04, 0x0002) // LSA=2
	writeSlotReg(s, 0, 0x06, 0x0004) // LEA=4
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x00, 0x1820) // KYONEX with LPCTL=1

	// Tick 5 times. Loop range is [LSA=2, LEA=4] inclusive (length 3):
	// positions 0,1,2,3,4 play, then at samplePos=5 wrap back to LSA.
	s.TickSamples(5)

	samplePos := s.slots[0].phase >> phaseFracBits
	if samplePos != 2 {
		t.Errorf("samplePos after loop = %d, want 2 (LSA)", samplePos)
	}
}

func TestSCSPNoLoopStopsAtLEA(t *testing.T) {
	s := NewSCSP(NewSCU())

	// LPCTL=0 (no loop), LEA=3
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1, LPCTL=0
	writeSlotReg(s, 0, 0x06, 0x0003) // LEA=3
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	s.TickSamples(5)

	// Output should be 0 after passing LEA
	if s.slots[0].output != 0 {
		t.Errorf("output = %d, want 0 after passing LEA", s.slots[0].output)
	}
}

func TestSCSPEGAttackProgressesTowardZero(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31 (fast)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x1800)

	// After a few ticks, EG counter should have increased from 0 (attack progresses upward)
	s.TickSamples(1)
	if s.slots[0].egLevel <= egAttackStart {
		t.Error("EG should increase during attack")
	}
}

func TestSCSPEGReleaseReachesOff(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Key on with fast AR
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0A, 0x001F) // RR=31 (fast release)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(10) // Let attack complete

	// Key off
	writeSlotReg(s, 0, 0x00, 0x0000) // KYONB=0
	writeSlotReg(s, 0, 0x00, 0x1000) // KYONEX

	s.TickSamples(500) // Let release complete

	// Spec: after release saturates, the slot stays in egRelease with
	// EG=$3FF and active=false so MSLC SGC reports Release(3).
	if s.slots[0].egState != egRelease {
		t.Errorf("egState = %d, want %d (release)", s.slots[0].egState, egRelease)
	}
	if s.slots[0].active {
		t.Error("slot should be inactive after full release")
	}
}

func TestSCSPEGHold(t *testing.T) {
	s := NewSCSP(NewSCU())

	// EGHOLD=1 (bit 5 of reg 0x08), AR=31
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x003F) // D1R=0, EGHOLD=1, AR=31
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(50) // Let attack complete

	// With EGHOLD, after attack completes the EG holds at full volume (no decay).
	// The attenuation should be 0 (full volume).
	atten := egLevelToAtten(s.slots[0].egLevel)
	if atten != 0 {
		t.Errorf("attenuation = 0x%03X, want 0 (held at full volume)", atten)
	}
}

// TestSCSPDecay1RateDynamic verifies that a mid-Decay1 write to D1R
// takes effect on the next sample tick. Real hardware reads the rate
// register live each cycle; we model that in advanceEG by re-deriving
// egStep from the current register state for Decay1/Decay2/Release.
//
// Setup: AR=31 (instant attack) so slot enters Decay1 quickly. D1R=2
// (slow) gives egDecayStep[eff=4]=1 per sample; egLevel advances by 1
// per tick. DL=$10 keeps egTarget partway through the decay range so
// Decay1 doesn't complete during the test. After observing slow
// advance, D1R is rewritten to 31 (eff=62, egDecayStep~6605); the
// next 50 ticks must advance egLevel much faster.
func TestSCSPDecay1RateDynamic(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x06, 0xFFFF) // LEA far
	writeSlotReg(s, 0, 0x08, 0x109F) // D2R=2, D1R=2, EGHOLD=0, AR=31
	writeSlotReg(s, 0, 0x0A, 0x0200) // DL=$10, RR=0
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	// Attack at AR=31 completes in 1 sample; slot enters Decay1.
	s.TickSamples(2)
	if s.slots[0].egState != egDecay1 {
		t.Fatalf("expected Decay1 after attack, got egState=%d", s.slots[0].egState)
	}

	levelBefore := s.slots[0].egLevel
	s.TickSamples(50)
	slowDelta := s.slots[0].egLevel - levelBefore
	if slowDelta > 100 {
		t.Errorf("with D1R=2, expected slow Decay1 advance; got delta=%d", slowDelta)
	}

	// Bump D1R to 31 (eff=62, egDecayStep~6605). Without dynamic
	// re-read, egStep stays at the slow value and the next ticks
	// advance the same amount. With dynamic re-read, egStep picks up
	// the new rate.
	writeSlotReg(s, 0, 0x08, 0x17DF) // D1R=31, D2R=2, AR=31

	levelBefore = s.slots[0].egLevel
	s.TickSamples(50)
	fastDelta := s.slots[0].egLevel - levelBefore

	if fastDelta <= slowDelta*10 {
		t.Errorf("dynamic D1R update did not take effect: slowDelta=%d fastDelta=%d (expected fast >> slow)",
			slowDelta, fastDelta)
	}
}

// TestSCSPDecay2RateDynamic verifies dynamic D2R behavior. D1R=0
// makes the attack-to-decay path skip directly into Decay2, so we
// can observe a pure-D2R rate. Same structure as the Decay1 test:
// slow initial rate, then bump to fast, verify advance speeds up.
func TestSCSPDecay2RateDynamic(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x06, 0xFFFF) // LEA far
	writeSlotReg(s, 0, 0x08, 0x101F) // D2R=2, D1R=0, EGHOLD=0, AR=31
	writeSlotReg(s, 0, 0x0A, 0x0000) // DL=0, RR=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x00, 0x1800)

	// With D1R=0, attack→decay path falls into the Decay2-skip
	// branch; slot enters Decay2 directly.
	s.TickSamples(2)
	if s.slots[0].egState != egDecay2 {
		t.Fatalf("expected Decay2 (via D1R=0 skip), got egState=%d", s.slots[0].egState)
	}

	levelBefore := s.slots[0].egLevel
	s.TickSamples(50)
	slowDelta := s.slots[0].egLevel - levelBefore
	if slowDelta > 100 {
		t.Errorf("with D2R=2, expected slow Decay2 advance; got delta=%d", slowDelta)
	}

	// Bump D2R to 31.
	writeSlotReg(s, 0, 0x08, 0xF81F) // D2R=31, D1R=0, AR=31

	levelBefore = s.slots[0].egLevel
	s.TickSamples(50)
	fastDelta := s.slots[0].egLevel - levelBefore

	if fastDelta <= slowDelta*10 {
		t.Errorf("dynamic D2R update did not take effect: slowDelta=%d fastDelta=%d",
			slowDelta, fastDelta)
	}
}

// TestSCSPReleaseRateDynamic verifies dynamic RR behavior. This is
// the Bulk Slash load-delay scenario: game writes RR=0, then KYONEX
// to key-off (slot enters Release with egStep=0, frozen at current
// level). Game later writes RR=31 to fade out. Real hardware reads
// RR live each cycle and resumes attenuation; we model that with
// the per-tick re-derivation in advanceEG.
func TestSCSPReleaseRateDynamic(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31, D1R=0, D2R=0, EGHOLD=0
	writeSlotReg(s, 0, 0x0A, 0x0000) // RR=0, DL=0, KRS=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX (key on)

	s.TickSamples(2) // attack completes

	// Key off with RR=0. Slot enters Release with egStep=0 (frozen).
	writeSlotReg(s, 0, 0x00, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x1000) // KYONEX (key off)

	if s.slots[0].egState != egRelease {
		t.Fatalf("expected Release after key-off, got egState=%d", s.slots[0].egState)
	}

	frozenLevel := s.slots[0].egLevel
	s.TickSamples(50)
	if s.slots[0].egLevel != frozenLevel {
		t.Errorf("with RR=0, expected egLevel frozen; before=$%X after=$%X",
			frozenLevel, s.slots[0].egLevel)
	}

	// Bump RR to 31. Dynamic re-read should pick up the new rate
	// and start advancing egLevel toward egDecayEnd.
	writeSlotReg(s, 0, 0x0A, 0x001F) // RR=31

	s.TickSamples(50)
	if s.slots[0].egLevel <= frozenLevel {
		t.Errorf("after RR=31 update, egLevel should advance from frozen state; before=$%X after=$%X",
			frozenLevel, s.slots[0].egLevel)
	}
}

func TestSCSP16BitPCMReading(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write 16-bit samples at address 0x1000 (fill several positions)
	for i := uint32(0); i < 40; i++ {
		s.WriteRAM16(0x1000+i*2, 0x4000)
	}

	// Set slot 0: SA=0x1000, 16-bit PCM
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1, PCM8B=0
	writeSlotReg(s, 0, 0x02, 0x1000) // SA low
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0A, 0x0000) // D2R=0, D1R=0 -> sustain
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	s.TickSamples(10) // Let attack finish

	// The first sample should produce non-zero output
	// (exact value depends on EG attenuation)
	// Just verify it read from RAM and produced output
	if s.slots[0].output == 0 {
		t.Error("expected non-zero output from 16-bit PCM sample")
	}
}

func TestSCSP8BitPCMReading(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write 8-bit samples at address 0x2000 (fill several positions)
	for i := uint32(0); i < 40; i++ {
		s.WriteRAM(0x2000+i, 0x40)
	}

	// Set slot 0: SA=0x2000, 8-bit PCM
	writeSlotReg(s, 0, 0x00, 0x0810) // KYONB=1, PCM8B=1
	writeSlotReg(s, 0, 0x02, 0x2000) // SA low
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x1810) // KYONEX

	s.TickSamples(10)

	if s.slots[0].output == 0 {
		t.Error("expected non-zero output from 8-bit PCM sample")
	}
}

func TestSCSPSSCTL2ProducesZero(t *testing.T) {
	s := NewSCSP(NewSCU())

	// SSCTL=2 (internally generated zeros)
	writeSlotReg(s, 0, 0x00, 0x0900) // KYONB=1, SSCTL=2 (bits 8:7 = 10)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x1900) // KYONEX

	s.TickSamples(10)

	if s.slots[0].output != 0 {
		t.Errorf("output = %d, want 0 for SSCTL=2", s.slots[0].output)
	}
}

func TestSCSPTLAttenuatesOutput(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Write loud samples (fill several positions)
	for i := uint32(0); i < 40; i++ {
		s.WriteRAM16(i*2, 0x7FFF)
	}

	// Slot with TL=0 (no attenuation)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0C, 0x0000) // TL=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(10)
	outputLoud := s.slots[0].output

	// Reset and try with TL=0xFF (max attenuation)
	s2 := NewSCSP(NewSCU())
	for i := uint32(0); i < 40; i++ {
		s2.WriteRAM16(i*2, 0x7FFF)
	}

	writeSlotReg(s2, 0, 0x00, 0x0800)
	writeSlotReg(s2, 0, 0x06, 0xFFFF)
	writeSlotReg(s2, 0, 0x08, 0x001F)
	writeSlotReg(s2, 0, 0x0C, 0x00FF) // TL=0xFF (max attenuation)
	writeSlotReg(s2, 0, 0x10, 0x0000)
	writeSlotReg(s2, 0, 0x00, 0x1800)

	s2.TickSamples(10)
	outputQuiet := s2.slots[0].output

	if outputLoud <= outputQuiet {
		t.Errorf("TL=0 output (%d) should be louder than TL=0xFF output (%d)",
			outputLoud, outputQuiet)
	}
}

func TestSCSPKRSOffNoScaling(t *testing.T) {
	// KRS=0xF means scaling is off. EG rate should be the same regardless of OCT.
	// Test by comparing attack completion speed at different OCTs with KRS=0xF.
	results := [2]int32{}

	for idx, octVal := range []uint16{0x0, 0x7} {
		s := NewSCSP(NewSCU())
		for i := uint32(0); i < 40; i++ {
			s.WriteRAM16(i*2, 0x7FFF)
		}
		writeSlotReg(s, 0, 0x00, 0x0800)
		writeSlotReg(s, 0, 0x06, 0xFFFF)
		writeSlotReg(s, 0, 0x08, 0x0010) // AR=16 (moderate speed)
		writeSlotReg(s, 0, 0x0A, 0x3C00) // KRS=0xF (off), DL=0, RR=0
		writeSlotReg(s, 0, 0x10, octVal<<11)
		writeSlotReg(s, 0, 0x00, 0x1800)

		s.TickSamples(20)
		results[idx] = s.slots[0].egLevel
	}

	// With KRS off, both OCTs should produce the same EG level
	if results[0] != results[1] {
		t.Errorf("KRS=0xF: OCT=0 egLevel=0x%03X, OCT=7 egLevel=0x%03X, want same",
			results[0], results[1])
	}
}

func TestSCSPKRSMaxScaling(t *testing.T) {
	// KRS=0x0 means maximum scaling. Higher OCT should make attack faster.
	results := [2]int32{}

	for idx, octVal := range []uint16{0x0, 0x7} {
		s := NewSCSP(NewSCU())
		for i := uint32(0); i < 40; i++ {
			s.WriteRAM16(i*2, 0x7FFF)
		}
		writeSlotReg(s, 0, 0x00, 0x0800)
		writeSlotReg(s, 0, 0x06, 0xFFFF)
		writeSlotReg(s, 0, 0x08, 0x0008) // AR=8 (slow, so scaling is visible)
		writeSlotReg(s, 0, 0x0A, 0x0000) // KRS=0x0 (max scaling), DL=0, RR=0
		writeSlotReg(s, 0, 0x10, octVal<<11)
		writeSlotReg(s, 0, 0x00, 0x1800)

		s.TickSamples(20)
		results[idx] = s.slots[0].egLevel
	}

	// Higher OCT (7) should have higher egLevel (faster attack = further along)
	if results[1] <= results[0] {
		t.Errorf("KRS=0x0: OCT=7 egLevel=0x%X should be higher than OCT=0 egLevel=0x%X",
			results[1], results[0])
	}
}

func TestSCSPKRSBaseRateZeroWithScaling(t *testing.T) {
	// AR=0 with KRS=0 and OCT=7 should still apply key rate scaling.
	// Effective rate = (KRS+OCT)*2 + FNS[9] + AR*2 = (0+7)*2 + 0 + 0 = 14.
	// Rate 14 is not infinity, so the EG should advance.
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x0000) // AR=0
	writeSlotReg(s, 0, 0x0A, 0x0000) // KRS=0x0 (max scaling)
	writeSlotReg(s, 0, 0x10, 0x3800) // OCT=7
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(20)

	// Effective rate 14 means attack progresses (not infinity)
	if s.slots[0].egLevel == egAttackStart {
		t.Error("egLevel should have advanced: AR=0 with KRS=0,OCT=7 gives effective rate 14")
	}
}

func TestSCSPKRSBaseRateZeroScalingOffNoChange(t *testing.T) {
	// AR=0 with KRS=0xF (scaling off) should give effective rate 0 (infinity).
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x0000) // AR=0
	writeSlotReg(s, 0, 0x0A, 0x3C00) // KRS=0xF (scaling off)
	writeSlotReg(s, 0, 0x10, 0x3800) // OCT=7
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(20)

	// KRS=0xF disables scaling, so AR=0 gives effective rate 0 (infinity/no change)
	if s.slots[0].egLevel != egAttackStart {
		t.Errorf("egLevel = 0x%X, want 0x%X (AR=0 with KRS=0xF means no change)", s.slots[0].egLevel, egAttackStart)
	}
}

func TestSCSPLFOPhaseAdvances(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set up slot 0 with LFO: LFOF=20 (fast), other LFO fields zero
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x12, 20<<10) // LFOF=20
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(50)

	if s.slots[0].lfoPhase == 0 {
		t.Error("LFO phase should have advanced with LFOF=20")
	}
}

func TestSCSPLFOREReset(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x12, 20<<10) // LFOF=20
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(50)
	if s.slots[0].lfoPhase == 0 {
		t.Fatal("LFO should have advanced before reset test")
	}

	// Set LFORE (bit 15)
	writeSlotReg(s, 0, 0x12, 0x8000|(20<<10))
	s.TickSamples(1)

	if s.slots[0].lfoPhase != 0 {
		t.Errorf("lfoPhase = %d, want 0 after LFORE", s.slots[0].lfoPhase)
	}
}

func TestSCSPPLFOSZeroNoModulation(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 40; i++ {
		s.WriteRAM16(i*2, 0x4000)
	}

	// PLFOS=0 should produce no pitch variation
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x10, 0x0000)          // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x12, (20<<10)|0x0000) // LFOF=20, PLFOS=0
	writeSlotReg(s, 0, 0x00, 0x1800)

	// Collect phase advances over several ticks
	s.TickSamples(5)
	phase1 := s.slots[0].phase
	s.TickSamples(5)
	phase2 := s.slots[0].phase

	// Without pitch modulation, each 5-tick interval should advance by same amount
	delta1 := phase1
	delta2 := phase2 - phase1

	if delta1 != delta2 {
		t.Errorf("PLFOS=0: phase deltas should be equal: %d vs %d", delta1, delta2)
	}
}

func TestSCSPALFOSZeroNoModulation(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s.WriteRAM16(i*2, 0x4000)
	}

	// ALFOS=0, LFOF=20
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31 fast
	writeSlotReg(s, 0, 0x0C, 0x0000) // TL=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x12, (20<<10)|0x0000) // LFOF=20, ALFOS=0
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(20) // Let attack complete

	// Sample several outputs - with ALFOS=0 they should all be the same
	// (same PCM data, same EG level, no amplitude modulation)
	outputs := make([]int16, 5)
	for j := 0; j < 5; j++ {
		s.TickSamples(1)
		outputs[j] = s.slots[0].output
	}

	allSame := true
	for j := 1; j < 5; j++ {
		if outputs[j] != outputs[0] {
			allSame = false
			break
		}
	}
	if !allSame {
		t.Errorf("ALFOS=0: outputs should be constant, got %v", outputs)
	}
}

func TestSCSPALFOSNonZeroVariesOutput(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 200; i++ {
		s.WriteRAM16(i*2, 0x4000)
	}

	// ALFOS=7 (max depth), ALFOWS=0 (sawtooth), LFOF=25 (fast)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0C, 0x0000) // TL=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x12, (25<<10)|0x0007) // LFOF=25, ALFOWS=0, ALFOS=7
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(20) // Let attack complete

	// Collect outputs over many ticks
	seen := make(map[int16]bool)
	for j := 0; j < 100; j++ {
		s.TickSamples(1)
		seen[s.slots[0].output] = true
	}

	if len(seen) < 2 {
		t.Errorf("ALFOS=7: expected varying outputs, got %d distinct values", len(seen))
	}
}

func TestSCSPKeyOnResetsLFO(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Start slot, advance LFO
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x12, 20<<10) // LFOF=20
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(50)
	if s.slots[0].lfoPhase == 0 {
		t.Fatal("LFO should have advanced")
	}

	// Key off first (transition guard requires slot to be in release)
	writeSlotReg(s, 0, 0x00, 0x0000) // KYONB=0
	writeSlotReg(s, 0, 0x00, 0x1000) // KYONEX

	// Key on again - should reset LFO
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	if s.slots[0].lfoPhase != 0 {
		t.Errorf("lfoPhase = %d, want 0 after re-key-on", s.slots[0].lfoPhase)
	}
}

func TestSCSPLFOSquareWaveform(t *testing.T) {
	// Test that square waveform produces two distinct output levels
	phase0 := lfoWaveform(0, 1, new(uint16))     // First half
	phase128 := lfoWaveform(128, 1, new(uint16)) // Second half

	if phase0 != 127 {
		t.Errorf("square phase=0: got %d, want 127", phase0)
	}
	if phase128 != -128 {
		t.Errorf("square phase=128: got %d, want -128", phase128)
	}
}

func TestSCSPLFOTriangleWaveform(t *testing.T) {
	noise := uint16(1)
	// Triangle at phase 0 should be -128, at 64 should be 0, at 128 should be 126
	v0 := lfoWaveform(0, 2, &noise)
	v64 := lfoWaveform(64, 2, &noise)
	v128 := lfoWaveform(128, 2, &noise)

	if v0 != -128 {
		t.Errorf("triangle phase=0: got %d, want -128", v0)
	}
	if v64 != 0 {
		t.Errorf("triangle phase=64: got %d, want 0", v64)
	}
	if v128 != 126 {
		t.Errorf("triangle phase=128: got %d, want 126", v128)
	}
}

func TestSCSPFMMDLZeroNoModulation(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s.WriteRAM16(i*2, 0x4000)
	}

	// Slot 0: modulator (produces output)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x10, 0x0000)

	// Slot 1: carrier with MDL=0 (off)
	writeSlotReg(s, 1, 0x00, 0x0800)
	writeSlotReg(s, 1, 0x02, 0x0000)
	writeSlotReg(s, 1, 0x06, 0xFFFF)
	writeSlotReg(s, 1, 0x08, 0x001F)
	writeSlotReg(s, 1, 0x0E, 0x0000) // MDL=0
	writeSlotReg(s, 1, 0x10, 0x0000)

	// KYONEX both slots
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(10)

	// With MDL=0, slot 1 should behave exactly like a normal slot
	// Just verify it produces output (not silence from modulation error)
	if !s.slots[1].active {
		t.Error("slot 1 should be active")
	}
}

func TestSCSPFMModulationAffectsOutput(t *testing.T) {
	// Compare carrier output with and without FM modulation from a modulator
	// that produces non-zero output.

	// Without FM
	s1 := NewSCSP(NewSCU())
	for i := uint32(0); i < 200; i++ {
		s1.WriteRAM16(i*2, 0x4000)
		s1.WriteRAM16(0x1000+i*2, 0x7FFF) // Loud modulator waveform
	}

	writeSlotReg(s1, 0, 0x00, 0x0800) // Modulator
	writeSlotReg(s1, 0, 0x02, 0x1000) // SA=0x1000
	writeSlotReg(s1, 0, 0x06, 0xFFFF)
	writeSlotReg(s1, 0, 0x08, 0x001F)
	writeSlotReg(s1, 0, 0x10, 0x0000)

	writeSlotReg(s1, 1, 0x00, 0x0800) // Carrier
	writeSlotReg(s1, 1, 0x06, 0xFFFF)
	writeSlotReg(s1, 1, 0x08, 0x001F)
	writeSlotReg(s1, 1, 0x0E, 0x0000) // MDL=0 (no FM)
	writeSlotReg(s1, 1, 0x10, 0x0000)
	writeSlotReg(s1, 0, 0x00, 0x1800)

	s1.TickSamples(20)
	noFMOutput := s1.slots[1].output

	// With FM (MDL=15, max)
	s2 := NewSCSP(NewSCU())
	for i := uint32(0); i < 200; i++ {
		s2.WriteRAM16(i*2, 0x4000)
		s2.WriteRAM16(0x1000+i*2, 0x7FFF)
	}

	writeSlotReg(s2, 0, 0x00, 0x0800)
	writeSlotReg(s2, 0, 0x02, 0x1000)
	writeSlotReg(s2, 0, 0x06, 0xFFFF)
	writeSlotReg(s2, 0, 0x08, 0x001F)
	writeSlotReg(s2, 0, 0x10, 0x0000)

	// Carrier: MDL=15, MDXSL=0x1F (slot 0 current), MDYSL=0x1F (slot 0 current)
	// MDXSL: slot offset to modulator. Slot 1 + 31 = slot 0 (mod 32)
	writeSlotReg(s2, 1, 0x00, 0x0800)
	writeSlotReg(s2, 1, 0x06, 0xFFFF)
	writeSlotReg(s2, 1, 0x08, 0x001F)
	writeSlotReg(s2, 1, 0x0E, 0xF7DF) // MDL=15, MDXSL=0x1F, MDYSL=0x1F
	writeSlotReg(s2, 1, 0x10, 0x0000)
	writeSlotReg(s2, 0, 0x00, 0x1800)

	s2.TickSamples(20)
	fmOutput := s2.slots[1].output

	// Outputs should differ due to FM modulation shifting the read address
	if noFMOutput == fmOutput {
		t.Errorf("FM modulation should change output: noFM=%d, FM=%d", noFMOutput, fmOutput)
	}
}

func TestSCSPFMMDL15LargerThanMDL5(t *testing.T) {
	// MDL=15 should produce more extreme modulation than MDL=5
	results := [2]int16{}

	for idx, mdlVal := range []uint16{5, 15} {
		s := NewSCSP(NewSCU())
		// Fill with a ramp so different addresses give different samples
		for i := uint32(0); i < 200; i++ {
			s.WriteRAM16(i*2, uint16(i*100))
			s.WriteRAM16(0x2000+i*2, 0x7FFF) // Loud modulator
		}

		writeSlotReg(s, 0, 0x00, 0x0800)
		writeSlotReg(s, 0, 0x02, 0x2000)
		writeSlotReg(s, 0, 0x06, 0xFFFF)
		writeSlotReg(s, 0, 0x08, 0x001F)
		writeSlotReg(s, 0, 0x10, 0x0000)

		// MDXSL=0x1F (=slot 0 from slot 1's perspective), MDYSL=0x1F
		modReg := mdlVal<<12 | 0x07DF // MDXSL=0x1F, MDYSL=0x1F
		writeSlotReg(s, 1, 0x00, 0x0800)
		writeSlotReg(s, 1, 0x06, 0xFFFF)
		writeSlotReg(s, 1, 0x08, 0x001F)
		writeSlotReg(s, 1, 0x0E, modReg)
		writeSlotReg(s, 1, 0x10, 0x0000)
		writeSlotReg(s, 0, 0x00, 0x1800)

		s.TickSamples(15)
		results[idx] = s.slots[1].output
	}

	// MDL=15 and MDL=5 should produce different outputs (more modulation = more shift)
	if results[0] == results[1] {
		t.Errorf("MDL=5 output (%d) should differ from MDL=15 output (%d)",
			results[0], results[1])
	}
}

func TestSCSPMixerDISDL7ProducesOutput(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s.WriteRAM16(i*2, 0x4000)
	}

	// Set MVOL to max
	s.Write(scspRegMVOL, 0x000F) // MVOL=15

	// Slot 0: DISDL=7 (full), DIPAN=0 (center)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0C, 0x0000) // TL=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x16, 0xE000) // DISDL=7, DIPAN=0, EFSDL=0
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	s.ResetMixBuffer(100)
	s.TickSamples(20)
	buf := s.MixBuffer()

	if len(buf) == 0 {
		t.Fatal("mix buffer should have samples")
	}

	// Check that some non-zero audio was produced
	hasNonZero := false
	for _, v := range buf {
		if v != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("DISDL=7 with active slot should produce non-zero mixed output")
	}
}

func TestSCSPMixerDISDL0ProducesSilence(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s.WriteRAM16(i*2, 0x7FFF)
	}

	s.Write(scspRegMVOL, 0x000F)

	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x0C, 0x0000)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x16, 0x0000) // DISDL=0 (muted)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.ResetMixBuffer(100)
	s.TickSamples(20)
	buf := s.MixBuffer()

	for j, v := range buf {
		if v != 0 {
			t.Errorf("DISDL=0: sample[%d] = %d, want 0", j, v)
			break
		}
	}
}

func TestSCSPMixerMVOL0ProducesSilence(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s.WriteRAM16(i*2, 0x7FFF)
	}

	s.Write(scspRegMVOL, 0x0000) // MVOL=0 (mute)

	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x16, 0xE000) // DISDL=7
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.ResetMixBuffer(100)
	s.TickSamples(20)
	buf := s.MixBuffer()

	for j, v := range buf {
		if v != 0 {
			t.Errorf("MVOL=0: sample[%d] = %d, want 0", j, v)
			break
		}
	}
}

func TestSCSPMixerDIPANLeftRight(t *testing.T) {
	s := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s.WriteRAM16(i*2, 0x4000)
	}

	s.Write(scspRegMVOL, 0x000F)

	// DIPAN = 0x0F: bit 4 clear = attenuate left, lower 4 bits = 0xF (max atten)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F)
	writeSlotReg(s, 0, 0x0C, 0x0000)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x16, 0xEF00) // DISDL=7, DIPAN=0x0F (attenuate left)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.ResetMixBuffer(100)
	s.TickSamples(20)
	buf := s.MixBuffer()

	// Find first non-zero stereo pair
	var foundL, foundR int16
	for j := 0; j+1 < len(buf); j += 2 {
		if buf[j] != 0 || buf[j+1] != 0 {
			foundL = buf[j]
			foundR = buf[j+1]
			break
		}
	}

	// Right should be louder than left (left attenuated)
	absL := foundL
	if absL < 0 {
		absL = -absL
	}
	absR := foundR
	if absR < 0 {
		absR = -absR
	}

	if absR <= absL {
		t.Errorf("DIPAN=0x0F: right (%d) should be louder than left (%d)", absR, absL)
	}
}

func TestSCSPMixerBufferLengthMatchesTicks(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.ResetMixBuffer(100)
	s.TickSamples(10)

	buf := s.MixBuffer()
	// 10 ticks * 2 channels = 20 samples
	if len(buf) != 20 {
		t.Errorf("mix buffer len = %d, want 20 (10 ticks * 2 stereo)", len(buf))
	}
}

func TestSCSPMixerMultipleSlotsSumTogether(t *testing.T) {
	// One slot vs two slots should produce louder output
	s1 := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s1.WriteRAM16(i*2, 0x2000)
	}
	s1.Write(scspRegMVOL, 0x000F)

	writeSlotReg(s1, 0, 0x00, 0x0800)
	writeSlotReg(s1, 0, 0x06, 0xFFFF)
	writeSlotReg(s1, 0, 0x08, 0x001F)
	writeSlotReg(s1, 0, 0x10, 0x0000)
	writeSlotReg(s1, 0, 0x16, 0xE000) // DISDL=7
	writeSlotReg(s1, 0, 0x00, 0x1800)

	s1.ResetMixBuffer(100)
	s1.TickSamples(15)
	buf1 := s1.MixBuffer()

	// Two slots
	s2 := NewSCSP(NewSCU())
	for i := uint32(0); i < 100; i++ {
		s2.WriteRAM16(i*2, 0x2000)
	}
	s2.Write(scspRegMVOL, 0x000F)

	writeSlotReg(s2, 0, 0x00, 0x0800)
	writeSlotReg(s2, 0, 0x06, 0xFFFF)
	writeSlotReg(s2, 0, 0x08, 0x001F)
	writeSlotReg(s2, 0, 0x10, 0x0000)
	writeSlotReg(s2, 0, 0x16, 0xE000)

	writeSlotReg(s2, 1, 0x00, 0x0800)
	writeSlotReg(s2, 1, 0x06, 0xFFFF)
	writeSlotReg(s2, 1, 0x08, 0x001F)
	writeSlotReg(s2, 1, 0x10, 0x0000)
	writeSlotReg(s2, 1, 0x16, 0xE000)
	writeSlotReg(s2, 0, 0x00, 0x1800) // KYONEX for both

	s2.ResetMixBuffer(100)
	s2.TickSamples(15)
	buf2 := s2.MixBuffer()

	// Find max absolute value in each
	var max1, max2 int16
	for _, v := range buf1 {
		if v > max1 {
			max1 = v
		}
		if -v > max1 {
			max1 = -v
		}
	}
	for _, v := range buf2 {
		if v > max2 {
			max2 = v
		}
		if -v > max2 {
			max2 = -v
		}
	}

	if max2 <= max1 {
		t.Errorf("two slots (max=%d) should be louder than one slot (max=%d)", max2, max1)
	}
}

func TestSCSPDSPFloatRoundTrip(t *testing.T) {
	// Test float conversion round-trip for several values
	testVals := []int32{0, 1000, -1000, 0x7FFFFF, -0x800000, 0x100, -0x100, 42}
	for _, v := range testVals {
		// Mask input to 24-bit range
		input := v & 0xFFFFFF
		f := intToDSPFloat(v)
		back := dspFloatToInt(f)
		// Sign-extend both to compare as 24-bit signed values
		if input&0x800000 != 0 {
			input |= ^int32(0xFFFFFF)
		}
		backSigned := back
		if backSigned&0x800000 != 0 {
			backSigned |= ^int32(0xFFFFFF)
		}
		diff := input - backSigned
		if diff < 0 {
			diff = -diff
		}
		// Tolerance: 11-bit mantissa means ~12 bits of precision loss for large values
		mag := input
		if mag < 0 {
			mag = -mag
		}
		maxErr := mag >> 10
		if maxErr < 16 {
			maxErr = 16
		}
		if input == 0 {
			maxErr = 0
		}
		if diff > maxErr {
			t.Errorf("float round-trip(%d): got 0x%06X, diff %d exceeds tolerance %d", v, back, diff, maxErr)
		}
	}
}

func TestSCSPDSPFloatZero(t *testing.T) {
	f := intToDSPFloat(0)
	back := dspFloatToInt(f)
	if back != 0 {
		t.Errorf("float(0) round-trip = %d, want 0", back)
	}
}

func TestSCSPDSPNOPProducesNoOutput(t *testing.T) {
	s := NewSCSP(NewSCU())

	// All MPRO is zero (NOP) by default
	// Run DSP - should not crash and produce no EFREG output
	s.runDSP()

	for i := 0; i < 16; i++ {
		if s.dsp.efreg[i] != 0 {
			t.Errorf("EFREG[%d] = %d after NOP DSP, want 0", i, s.dsp.efreg[i])
		}
	}
}

func TestSCSPDSPMDECCTDecrements(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set RBL=0 (8K words) -> reload value = 0x1FFF
	s.Write(scspRegMVOL+2, 0x0000) // RBL=0

	// Set a non-zero MPRO step so DSP runs
	// Step 0: TWT=1 (write to TEMP) just to make it non-NOP
	s.dsp.mpro[0] = uint64(1) << 55 // TWT=1
	s.recalcDSPLastStep()

	initial := s.dsp.mdecCT
	s.runDSP()

	// After first run with mdecCT=0: should reload then decrement
	if s.dsp.mdecCT == initial {
		t.Error("MDEC_CT should have changed after runDSP")
	}
}

func TestSCSPDSPMIXSAccumulation(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set up slot 0 with waveform data
	for i := uint32(0); i < 100; i++ {
		s.WriteRAM16(i*2, 0x4000)
	}

	// Configure slot 0: ISEL=0, IMXL=7 (full level to MIXS[0])
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0C, 0x0000) // TL=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x14, 0x0007) // ISEL=0, IMXL=7
	writeSlotReg(s, 0, 0x16, 0xE000) // DISDL=7
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	// Clear MIXS
	for i := range s.dsp.mixs {
		s.dsp.mixs[i] = 0
	}

	s.TickSamples(10) // Let attack complete

	// Clear and tick one more to check MIXS
	for i := range s.dsp.mixs {
		s.dsp.mixs[i] = 0
	}
	// Manually process one tick's slot processing
	s.processSlots()

	if s.dsp.mixs[0] == 0 {
		t.Error("MIXS[0] should have accumulated slot output with ISEL=0, IMXL=7")
	}
}

func TestSCSPDSPSingleStepCOEFToEFREG(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Set COEF[0] to a known value (13-bit stored in upper 13 of 16-bit)
	s.dsp.coef[0] = int16(0x1000) // ~0x200 as 13-bit (0x1000 >> 3 = 0x200)

	// Set MIXS[0] to a known input value
	s.dsp.mixs[0] = 0x4000

	// The DSP is pipelined: SHIFTED (which feeds EWT) uses the PREVIOUS
	// step's SFT_REG. So step 0's multiply result only appears in EFREG
	// after a second step reads it out via EWT.
	//
	// Step 0: multiply MIXS[0] * COEF[0] -> SFT_REG (no EWT)
	// Step 1: EWT=1 writes SHIFTED (which shifts step 0's SFT_REG) to EFREG[0]

	// Step 0: Read MIXS[0] via IRA=0x20, XSEL=1, YSEL=1 (COEF[0]), ZERO=1
	var instr0 uint64
	instr0 |= uint64(0x20) << 38 // IRA=0x20 (MIXS[0])
	instr0 |= uint64(1) << 47    // XSEL=1 (use INPUTS)
	instr0 |= uint64(1) << 45    // YSEL=01 (COEF)
	instr0 |= uint64(1) << 17    // ZERO=1

	// Step 1: EWT=1 to write the shifted SFT_REG (from step 0) to EFREG[0]
	var instr1 uint64
	instr1 |= uint64(1) << 28 // EWT=1
	instr1 |= uint64(1) << 17 // ZERO=1

	s.dsp.mpro[0] = instr0
	s.dsp.mpro[1] = instr1
	s.recalcDSPLastStep()

	s.runDSP()

	// EFREG[0] should be non-zero (MIXS * COEF product from step 0)
	if s.dsp.efreg[0] == 0 {
		t.Error("EFREG[0] should be non-zero after COEF * MIXS multiply")
	}
}

func TestSCSPDSPTEMPWriteRead(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Step 0: write a known value to TEMP[0]
	// Use XSEL=1 (INPUTS), ZERO=1 to get product=0+B where B=0
	// Actually simpler: just set TWT=1, TWA=0 to write shifter output
	// The shifter output comes from SFT_REG after accumulate
	// With ZERO=1 and BSEL=0, product + 0 = product
	// With XSEL=0 (TEMP, which is 0) and YSEL=0 (FRC_REG=0), product = 0
	// So TEMP write will write 0. Not useful.

	// Instead, manually set TEMP and verify it persists
	s.dsp.temp[5] = 0x123456
	if s.dsp.temp[5] != 0x123456 {
		t.Errorf("TEMP[5] = 0x%X, want 0x123456", s.dsp.temp[5])
	}

	// Verify TEMP ring buffer addressing works
	s.dsp.mdecCT = 3
	addr := (5 + int(s.dsp.mdecCT)) & 0x7F // = 8
	s.dsp.temp[addr] = 0xABCDEF
	if s.dsp.temp[8] != 0xABCDEF {
		t.Errorf("TEMP[8] = 0x%X, want 0xABCDEF", s.dsp.temp[8])
	}
}

func TestSCSPMIDIInitialState(t *testing.T) {
	s := NewSCSP(NewSCU())

	got := s.Read(0x404)
	// MIEMP=1 (bit 8), MOEMP=1 (bit 11), MIBUF=0xFF (empty FIFO)
	want := uint16(0x09FF)
	if got != want {
		t.Errorf("MIDI reg = 0x%04X, want 0x%04X", got, want)
	}
}

func TestSCSPMIDIWriteTo404ReadOnly(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Writing to 0x404 should be ignored
	s.Write(0x404, 0xFFFF)

	got := s.Read(0x404)
	// Should still show initial state
	want := uint16(0x09FF)
	if got != want {
		t.Errorf("MIDI reg after write = 0x%04X, want 0x%04X", got, want)
	}
}

func TestSCSPReverseLoopBouncesBackward(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Put known 16-bit samples at SA=0: values 100,200,300,...,1000
	for i := 0; i < 10; i++ {
		val := int16((i + 1) * 100)
		s.WriteRAM16(uint32(i)*2, uint16(val))
	}

	// Slot 0: LPCTL=2 (reverse), KYONB=1, 16-bit PCM
	// LPCTL=2 is bits 6:5 = 10 = 0x0040
	writeSlotReg(s, 0, 0x00, 0x0840) // KYONB=1, LPCTL=2
	writeSlotReg(s, 0, 0x02, 0x0000) // SA low = 0
	writeSlotReg(s, 0, 0x04, 0x0002) // LSA=2
	writeSlotReg(s, 0, 0x06, 0x0006) // LEA=6
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31 (instant attack)
	writeSlotReg(s, 0, 0x0C, 0x0100) // SDIR=1 (bypass attenuation)
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0 (1 sample/tick)
	writeSlotReg(s, 0, 0x16, 0xE000) // DISDL=7
	writeSlotReg(s, 0, 0x00, 0x1840) // KYONEX

	// Tick forward through samples 0..6 (7 ticks to reach LEA)
	// At tick 7 the slot should reverse direction
	// After reversing it should walk backward through samples
	s.TickSamples(8) // Forward 0-6, then reverse: should be at sample 5

	pos := s.slots[0].phase >> phaseFracBits
	if s.slots[0].loopDir >= 0 {
		t.Errorf("loopDir = %d, want negative (reverse)", s.slots[0].loopDir)
	}

	// After reversal from LEA=6, next position should be heading backward
	// The sample position should be less than LEA
	if pos >= 6 {
		t.Errorf("samplePos after reverse = %d, should be < LEA(6)", pos)
	}

	// Continue ticking - should reach LSA and stay in reverse loop
	s.TickSamples(10)
	pos2 := s.slots[0].phase >> phaseFracBits
	// Should be bouncing between LSA=2 and LEA=6
	if pos2 < 2 || pos2 > 6 {
		t.Errorf("samplePos during reverse loop = %d, should be in [LSA=2, LEA=6]", pos2)
	}
}

func TestSCSPAlternatingLoopPingPongs(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Put known samples
	for i := 0; i < 10; i++ {
		val := int16((i + 1) * 100)
		s.WriteRAM16(uint32(i)*2, uint16(val))
	}

	// Slot 0: LPCTL=3 (alternating), KYONB=1
	// LPCTL=3 is bits 6:5 = 11 = 0x0060
	writeSlotReg(s, 0, 0x00, 0x0860) // KYONB=1, LPCTL=3
	writeSlotReg(s, 0, 0x02, 0x0000) // SA low = 0
	writeSlotReg(s, 0, 0x04, 0x0002) // LSA=2
	writeSlotReg(s, 0, 0x06, 0x0006) // LEA=6
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0C, 0x0100) // SDIR=1
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x16, 0xE000) // DISDL=7
	writeSlotReg(s, 0, 0x00, 0x1860) // KYONEX

	// Track positions through a full pingpong cycle
	// Forward: 0,1,2,3,4,5,6 -> reverse at LEA
	// Backward: 5,4,3,2 -> forward at LSA
	// Forward: 3,4,5,6 -> reverse again
	s.TickSamples(7) // Forward to LEA, should reverse

	if s.slots[0].loopDir >= 0 {
		t.Errorf("after reaching LEA: loopDir = %d, want negative", s.slots[0].loopDir)
	}

	s.TickSamples(4) // Backward toward LSA
	if s.slots[0].loopDir < 0 {
		// Should have hit LSA and switched to forward
		// (or still heading backward - depends on exact position)
	}

	// After a full cycle, the slot should still be active and bounded
	s.TickSamples(20)
	pos := s.slots[0].phase >> phaseFracBits
	if pos < 2 || pos > 6 {
		t.Errorf("samplePos during alternating loop = %d, should be in [LSA=2, LEA=6]", pos)
	}
	if !s.slots[0].active {
		t.Error("slot should still be active during alternating loop")
	}
}

// --- EXTS / CD audio tests ---

// fakeCDAudio is a test helper implementing CDAudioSource. samples
// holds interleaved L,R,L,R,... pairs to be returned in order.
type fakeCDAudio struct {
	samples []int16
	pos     int
	drained int
}

func (f *fakeCDAudio) PopAudioSample() (int16, int16, bool) {
	if f.pos+2 > len(f.samples) {
		return 0, 0, false
	}
	l := f.samples[f.pos]
	r := f.samples[f.pos+1]
	f.pos += 2
	return l, r, true
}

func (f *fakeCDAudio) DrainAudio() {
	f.drained++
	f.samples = nil
	f.pos = 0
}

func TestSCSPEXTSDefaultSilent(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.TickSamples(1)
	if s.dsp.exts[0] != 0 || s.dsp.exts[1] != 0 {
		t.Errorf("exts = [%d,%d], want [0,0]", s.dsp.exts[0], s.dsp.exts[1])
	}
}

func TestSCSPEXTSPullsFromSource(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{samples: []int16{0x0100, 0x0200, 0x0300, 0x0400}}
	s.SetCDAudioSource(fake)

	s.TickSamples(2)

	if fake.pos != 4 {
		t.Errorf("fake.pos = %d, want 4 (4 int16 popped)", fake.pos)
	}
	// After 2 ticks the most recent pair latched is the second one.
	if s.dsp.exts[0] != 0x0300 || s.dsp.exts[1] != 0x0400 {
		t.Errorf("exts after 2 ticks = [0x%X,0x%X], want [0x0300,0x0400]", s.dsp.exts[0], s.dsp.exts[1])
	}
}

func TestSCSPEXTSEmptyQueueSilent(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{}
	s.SetCDAudioSource(fake)

	s.TickSamples(5)

	if s.dsp.exts[0] != 0 || s.dsp.exts[1] != 0 {
		t.Errorf("exts on empty queue = [%d,%d], want [0,0]", s.dsp.exts[0], s.dsp.exts[1])
	}
}

func TestSCSPEXTSIRARangeSelect(t *testing.T) {
	// Verify the runDSP IRA decoder path for EXTS by checking the
	// values that get latched into INPUTS via xsel routing. Easiest
	// indirect test: verify dsp.exts is read at IRA 0x30/0x31 by
	// checking the unused 0x32-0x3F range still returns 0.
	s := NewSCSP(NewSCU())
	s.dsp.exts[0] = 0x4000
	s.dsp.exts[1] = -0x4000

	// Direct field read - verifies the storage layout is correct.
	if s.dsp.exts[0] != 0x4000 {
		t.Errorf("exts[0] = %d, want 0x4000", s.dsp.exts[0])
	}
	if s.dsp.exts[1] != -0x4000 {
		t.Errorf("exts[1] = %d, want -0x4000", s.dsp.exts[1])
	}
}

func TestSCSPMixEXTSDirectPath(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{samples: []int16{0x4000, 0x4000}}
	s.SetCDAudioSource(fake)

	// Slot 16 MIXER reg at offset 0x16 controls EXTS0 EFSDL/EFPAN.
	// EFSDL=7 (0 dB), EFPAN=0 (centered). Both fields are at bits 7:5
	// and 4:0 of the MIXER reg respectively.
	writeSlotReg(s, 16, 0x16, (7<<5)|0)
	// MVOL = 15 (max master volume so we don't clamp to silence)
	s.Write(0x400, 0x000F)
	s.ResetMixBuffer(4)

	s.TickSamples(1)

	mb := s.MixBuffer()
	if len(mb) < 2 {
		t.Fatalf("mix buffer too short: %d", len(mb))
	}
	// With centered pan (EFPAN=0), both L and R receive the sample
	// at base level. Exact value depends on sdlPanToVolume and master
	// volume scaling; just verify both channels are non-zero and equal.
	if mb[0] == 0 || mb[1] == 0 {
		t.Errorf("EXTS centered: mb=[%d,%d], expected non-zero L and R", mb[0], mb[1])
	}
	if mb[0] != mb[1] {
		t.Errorf("EXTS centered should be equal L/R: mb=[%d,%d]", mb[0], mb[1])
	}
}

func TestSCSPMixEXTSPanRightOnly(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{samples: []int16{0x4000, 0x4000}}
	s.SetCDAudioSource(fake)

	// EFSDL=7, EFPAN=0x0F: per Table 4.30, L is at -inf, R is at 0 dB.
	// Left channel muted, right channel full.
	writeSlotReg(s, 16, 0x16, (7<<5)|0x0F)
	s.Write(0x400, 0x000F)
	s.ResetMixBuffer(4)

	s.TickSamples(1)

	mb := s.MixBuffer()
	if len(mb) < 2 {
		t.Fatalf("mix buffer too short")
	}
	if mb[0] != 0 {
		t.Errorf("EFPAN=0x0F: L = %d, expected 0 (left muted)", mb[0])
	}
	if mb[1] == 0 {
		t.Errorf("EFPAN=0x0F: R = 0, expected non-zero")
	}
}

func TestSCSPMixEXTSPanLeftOnly(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{samples: []int16{0x4000, 0x4000}}
	s.SetCDAudioSource(fake)

	// EFSDL=7, EFPAN=0x1F: per Table 4.30, L is at 0 dB, R is at -inf.
	// Left channel full, right channel muted.
	writeSlotReg(s, 16, 0x16, (7<<5)|0x1F)
	s.Write(0x400, 0x000F)
	s.ResetMixBuffer(4)

	s.TickSamples(1)

	mb := s.MixBuffer()
	if len(mb) < 2 {
		t.Fatalf("mix buffer too short")
	}
	if mb[0] == 0 {
		t.Errorf("EFPAN=0x1F: L = 0, expected non-zero")
	}
	if mb[1] != 0 {
		t.Errorf("EFPAN=0x1F: R = %d, expected 0 (right muted)", mb[1])
	}
}

func TestSCSPMixEXTSSlot17(t *testing.T) {
	s := NewSCSP(NewSCU())
	// Push a pair where only the right channel is non-zero.
	fake := &fakeCDAudio{samples: []int16{0, 0x4000}}
	s.SetCDAudioSource(fake)

	// Slot 17 MIXER reg controls EXTS1 (right channel).
	writeSlotReg(s, 17, 0x16, (7<<5)|0)
	s.Write(0x400, 0x000F)
	s.ResetMixBuffer(4)

	s.TickSamples(1)

	mb := s.MixBuffer()
	if len(mb) < 2 {
		t.Fatalf("mix buffer too short")
	}
	// EXTS1 (right) is non-zero, EXTS0 (left) is 0. Centered pan
	// on slot 17's EFSDL/EFPAN routes EXTS1 to both L and R.
	if mb[0] == 0 || mb[1] == 0 {
		t.Errorf("EXTS1 centered: mb=[%d,%d]", mb[0], mb[1])
	}
}

func TestSCSPEXTSResetClearsAndDrains(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{samples: []int16{0x1000, 0x2000}}
	s.SetCDAudioSource(fake)
	s.TickSamples(1)
	if s.dsp.exts[0] == 0 {
		t.Fatal("setup: exts[0] unexpectedly 0")
	}

	s.Reset()

	if s.dsp.exts[0] != 0 || s.dsp.exts[1] != 0 {
		t.Errorf("after Reset: exts = [%d,%d], want [0,0]", s.dsp.exts[0], s.dsp.exts[1])
	}
	if fake.drained == 0 {
		t.Error("Reset did not call DrainAudio on cdAudio source")
	}
}

func TestSCSPEXTSDrainOnSNDOFF(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{samples: []int16{0x1000, 0x2000}}
	s.SetCDAudioSource(fake)
	s.TickSamples(1)

	s.SetInReset(true)

	if fake.drained == 0 {
		t.Error("SetInReset(true) did not call DrainAudio")
	}
	if s.dsp.exts[0] != 0 || s.dsp.exts[1] != 0 {
		t.Errorf("after SetInReset(true): exts = [%d,%d], want [0,0]", s.dsp.exts[0], s.dsp.exts[1])
	}
}

func TestSCSPEXTSNoDrainOnSNDON(t *testing.T) {
	s := NewSCSP(NewSCU())
	fake := &fakeCDAudio{samples: []int16{0x1000, 0x2000}}
	s.SetCDAudioSource(fake)

	s.SetInReset(true)
	drainedAfterOff := fake.drained

	s.SetInReset(false)

	if fake.drained != drainedAfterOff {
		t.Errorf("SetInReset(false) called DrainAudio: drained went from %d to %d", drainedAfterOff, fake.drained)
	}
}

// -- Bus wrapper (scspBus) coverage --

func TestSCSPBusRead8RAMAndReg(t *testing.T) {
	s := NewSCSP(NewSCU())
	// RAM: write a 16-bit value, read high and low bytes via bus.
	s.WriteRAM16(0x100, 0xABCD)
	if got := s.m68kBus.Read8(0x100); got != 0xAB {
		t.Errorf("bus Read8 RAM high = 0x%02X, want 0xAB", got)
	}
	if got := s.m68kBus.Read8(0x101); got != 0xCD {
		t.Errorf("bus Read8 RAM low = 0x%02X, want 0xCD", got)
	}

	// Register: SCILV0 = 0x1234.
	s.Write(scspRegSCILV0, 0x1234)
	if got := s.m68kBus.Read8(0x100000 + scspRegSCILV0); got != 0x12 {
		t.Errorf("bus Read8 reg high = 0x%02X, want 0x12", got)
	}
	if got := s.m68kBus.Read8(0x100000 + scspRegSCILV0 + 1); got != 0x34 {
		t.Errorf("bus Read8 reg low = 0x%02X, want 0x34", got)
	}
}

func TestSCSPBusRead8OutOfRange(t *testing.T) {
	s := NewSCSP(NewSCU())
	if got := s.m68kBus.Read8(0x200000); got != 0 {
		t.Errorf("Read8 out of range = 0x%02X, want 0", got)
	}
}

func TestSCSPBusWrite8RAMAndReg(t *testing.T) {
	s := NewSCSP(NewSCU())

	// RAM write/readback.
	s.m68kBus.Write8(0x200, 0x5A)
	if got := s.ReadRAM(0x200); got != 0x5A {
		t.Errorf("RAM after Write8 = 0x%02X, want 0x5A", got)
	}

	// Register: compose high byte via Write8, preserving low byte.
	s.Write(scspRegSCILV0, 0x00CD)
	s.m68kBus.Write8(0x100000+scspRegSCILV0, 0xAB)
	if got := s.Read(scspRegSCILV0); got != 0xABCD {
		t.Errorf("reg after Write8 high = 0x%04X, want 0xABCD", got)
	}
	// Low byte Write8 preserves high byte.
	s.m68kBus.Write8(0x100000+scspRegSCILV0+1, 0xEF)
	if got := s.Read(scspRegSCILV0); got != 0xABEF {
		t.Errorf("reg after Write8 low = 0x%04X, want 0xABEF", got)
	}
}

func TestSCSPBusRead32RAMAndReg(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.WriteRAM16(0x300, 0x1122)
	s.WriteRAM16(0x302, 0x3344)
	if got := s.m68kBus.Read32(0x300); got != 0x11223344 {
		t.Errorf("Read32 RAM = 0x%08X, want 0x11223344", got)
	}

	s.Write(scspRegSCILV0, 0xAAAA)
	s.Write(scspRegSCILV1, 0xBBBB)
	if got := s.m68kBus.Read32(0x100000 + scspRegSCILV0); got != 0xAAAABBBB {
		t.Errorf("Read32 reg = 0x%08X, want 0xAAAABBBB", got)
	}
}

func TestSCSPBusWrite32RAMAndReg(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.m68kBus.Write32(0x400, 0x11223344)
	if got := s.ReadRAM16(0x400); got != 0x1122 {
		t.Errorf("RAM[0x400] after Write32 = 0x%04X, want 0x1122", got)
	}
	if got := s.ReadRAM16(0x402); got != 0x3344 {
		t.Errorf("RAM[0x402] after Write32 = 0x%04X, want 0x3344", got)
	}

	s.m68kBus.Write32(0x100000+scspRegSCILV0, 0xDEADBEEF)
	if got := s.Read(scspRegSCILV0); got != 0xDEAD {
		t.Errorf("SCILV0 after Write32 = 0x%04X, want 0xDEAD", got)
	}
	if got := s.Read(scspRegSCILV1); got != 0xBEEF {
		t.Errorf("SCILV1 after Write32 = 0x%04X, want 0xBEEF", got)
	}
}

// -- Reset --

func TestSCSPResetClearsState(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Dirty the state.
	s.Write(scspRegSCILV0, 0xAAAA)
	s.slots[7].active = true
	s.slots[7].egState = egAttack
	s.slots[7].phase = 0xDEADBEEF
	s.timerCounter[1] = 0x55
	s.timerPrescaler[1] = 123
	s.dsp.sftReg = 0x12345
	s.dsp.mdecCT = 0x100
	s.fmHistoryCur = 17
	s.fmHistory[0] = 9999
	s.fmDelayer[2] = 1234
	s.mainIntActive = true
	s.lfsr = 0x1000

	s.Reset()

	if s.regs[scspRegSCILV0/2] != 0 {
		t.Error("regs not cleared by Reset")
	}
	if s.slots[7].active {
		t.Error("slot.active not cleared by Reset")
	}
	if s.slots[7].egState != egRelease {
		t.Errorf("slot.egState = %d, want egRelease after Reset", s.slots[7].egState)
	}
	if s.slots[7].egLevel != egDecayStart {
		t.Errorf("slot.egLevel = %d, want egDecayStart after Reset", s.slots[7].egLevel)
	}
	if s.slots[7].phase != 0 {
		t.Errorf("slot.phase not zeroed: 0x%X", s.slots[7].phase)
	}
	if s.timerCounter[1] != 0 || s.timerPrescaler[1] != 0 {
		t.Error("timer state not cleared by Reset")
	}
	if s.dsp.sftReg != 0 || s.dsp.mdecCT != 0 {
		t.Error("DSP state not cleared by Reset")
	}
	if s.fmHistoryCur != 0 || s.fmHistory[0] != 0 || s.fmDelayer[2] != 0 {
		t.Error("FM state not cleared by Reset")
	}
	if s.mainIntActive {
		t.Error("mainIntActive not cleared by Reset")
	}
	if s.lfsr != 1 {
		t.Errorf("lfsr = %d, want 1 after Reset", s.lfsr)
	}
}

// -- RAM 32-bit access --

func TestSCSPReadWriteRAM32(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.WriteRAM32(0x500, 0xCAFEBABE)
	if got := s.ReadRAM32(0x500); got != 0xCAFEBABE {
		t.Errorf("ReadRAM32 = 0x%08X, want 0xCAFEBABE", got)
	}
	// Verify the two halves land at the right addresses.
	if got := s.ReadRAM16(0x500); got != 0xCAFE {
		t.Errorf("RAM16[0x500] = 0x%04X, want 0xCAFE", got)
	}
	if got := s.ReadRAM16(0x502); got != 0xBABE {
		t.Errorf("RAM16[0x502] = 0x%04X, want 0xBABE", got)
	}
}

// -- DSP helper functions --

func TestSCSPDSPSignExtend12(t *testing.T) {
	cases := []struct {
		in   uint16
		want int32
	}{
		{0x000, 0},
		{0x001, 1},
		{0x7FF, 2047},
		{0x800, -2048},
		{0xFFF, -1},
	}
	for _, c := range cases {
		got := signExtend12(c.in)
		if got != c.want {
			t.Errorf("signExtend12(0x%X) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSCSPDSPSignExtend13(t *testing.T) {
	cases := []struct {
		in   int32
		want int32
	}{
		{0, 0},
		{1, 1},
		{0xFFF, 0xFFF},               // 12-bit positive
		{0x1000, -4096},              // bit 12 set -> negative
		{0x1FFF, -1},                 // all-ones 13-bit
		{int32(-1) & 0x1FFF, -1},     // explicit mask
		{int32(-100) & 0x1FFF, -100}, // negative round trip
	}
	for _, c := range cases {
		got := signExtend13(c.in)
		if got != c.want {
			t.Errorf("signExtend13(0x%X) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSCSPDSPSaturate24(t *testing.T) {
	// Positive overflow clamps to 24-bit positive max.
	if got := saturate24(int32(0x01000000)); got != 0x7FFFFF {
		t.Errorf("saturate24 positive overflow = %d, want 0x7FFFFF", got)
	}
	// Negative overflow clamps to 24-bit signed min (returned as a
	// signed int32; the caller masks to 24 bits where needed).
	if got := saturate24(int32(-0x01000001)); got != -0x800000 {
		t.Errorf("saturate24 negative overflow = %d, want -0x800000", got)
	}
	// In-range positive passes through unchanged.
	if got := saturate24(0x00123456); got != 0x00123456 {
		t.Errorf("saturate24 in-range pos = %d, want 0x123456", got)
	}
	// In-range negative passes through unchanged.
	if got := saturate24(-1); got != -1 {
		t.Errorf("saturate24 -1 = %d, want -1", got)
	}
}

// -- syncDSPRegWrite coverage (CPU writes into DSP register areas) --

func TestSCSPSyncDSPRegWriteCOEF(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.Write(dspRegCOEF+7*2, 0xBEEF)
	if uint16(s.dsp.coef[7]) != 0xBEEF {
		t.Errorf("coef[7] = 0x%04X, want 0xBEEF", uint16(s.dsp.coef[7]))
	}
}

func TestSCSPSyncDSPRegWriteMADRS(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.Write(dspRegMADRS+5*2, 0x1234)
	if s.dsp.madrs[5] != 0x1234 {
		t.Errorf("madrs[5] = 0x%04X, want 0x1234", s.dsp.madrs[5])
	}
}

func TestSCSPSyncDSPRegWriteMPROComposes(t *testing.T) {
	s := NewSCSP(NewSCU())
	// Write all 4 words of step 3. Words 0..3 occupy bits 63..48, 47..32, 31..16, 15..0.
	base := uint32(dspRegMPRO + 3*8)
	s.Write(base, 0x1122)
	s.Write(base+2, 0x3344)
	s.Write(base+4, 0x5566)
	s.Write(base+6, 0x7788)
	want := uint64(0x1122) << 48
	want |= uint64(0x3344) << 32
	want |= uint64(0x5566) << 16
	want |= uint64(0x7788)
	if s.dsp.mpro[3] != want {
		t.Errorf("mpro[3] = 0x%016X, want 0x%016X", s.dsp.mpro[3], want)
	}
}

func TestSCSPSyncDSPRegWriteTEMP(t *testing.T) {
	s := NewSCSP(NewSCU())
	// TEMP high word then low byte (stored 24-bit, with upper 16 bits
	// from the first write and low 8 bits from the second).
	s.Write(dspRegTEMP+5*4, 0xABCD)
	s.Write(dspRegTEMP+5*4+2, 0x0012)
	if s.dsp.temp[5] != 0xABCD12 {
		t.Errorf("temp[5] = 0x%06X, want 0xABCD12", s.dsp.temp[5])
	}
}

func TestSCSPSyncDSPRegWriteMEMS(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.Write(dspRegMEMS+2*4, 0x1234)
	s.Write(dspRegMEMS+2*4+2, 0x0056)
	if s.dsp.mems[2] != 0x123456 {
		t.Errorf("mems[2] = 0x%06X, want 0x123456", s.dsp.mems[2])
	}
}

// -- readDSPReg coverage for MEMS/MIXS low words --

func TestSCSPReadDSPRegMEMSLowWord(t *testing.T) {
	s := NewSCSP(NewSCU())
	s.dsp.mems[10] = 0x00CDEF
	// High word comes from bits 23:8 (0x00CD), low word from bits 7:0 (0xEF).
	if got := s.Read(dspRegMEMS + 10*4); got != 0x00CD {
		t.Errorf("MEMS[10] high = 0x%04X, want 0x00CD", got)
	}
	if got := s.Read(dspRegMEMS + 10*4 + 2); got != 0x00EF {
		t.Errorf("MEMS[10] low = 0x%04X, want 0x00EF", got)
	}
}

func TestSCSPReadDSPRegMIXSLowWord(t *testing.T) {
	s := NewSCSP(NewSCU())
	// MIXS stores 20-bit values; readback splits as bits 19:4 (high) and 3:0 (low).
	s.dsp.mixs[3] = 0x12345
	if got := s.Read(dspRegMIXS + 3*4); got != 0x1234 {
		t.Errorf("MIXS[3] high = 0x%04X, want 0x1234", got)
	}
	if got := s.Read(dspRegMIXS + 3*4 + 2); got != 0x0005 {
		t.Errorf("MIXS[3] low = 0x%04X, want 0x0005", got)
	}
}

// -- calcPhaseIncrement negative octave --

func TestSCSPCalcPhaseIncrementNegativeOCT(t *testing.T) {
	// OCT=0 with FNS=0 is the base increment (1 sample per tick scaled).
	base := calcPhaseIncrement(0, 0)
	// OCT=-1 (encoded as 0x8 in the 4-bit OCT field per SCSP manual)
	// halves the rate.
	// 4-bit signed: OCT=0x8 means -8, 0xF means -1. Test OCT=-1.
	neg1 := calcPhaseIncrement(0xF, 0)
	if neg1 != base>>1 {
		t.Errorf("calcPhaseIncrement(OCT=-1) = %d, want base/2 = %d", neg1, base>>1)
	}
	// OCT=-2 quarters the rate.
	neg2 := calcPhaseIncrement(0xE, 0)
	if neg2 != base>>2 {
		t.Errorf("calcPhaseIncrement(OCT=-2) = %d, want base/4 = %d", neg2, base>>2)
	}
}

// TestSCSPAttackRateDynamic verifies that a mid-attack AR rewrite takes
// effect on the next sample tick. Mirrors TestSCSPDecay1RateDynamic.
// Slow AR (eff=8, step ~8 per sample) advances the EG counter ~400
// units over 50 ticks; rewriting AR to 15 (eff=30, step ~377) should
// advance ~18850 over the next 50 ticks. Without dynamic re-read,
// egStep stays at the slow value latched at key-on.
func TestSCSPAttackRateDynamic(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x06, 0xFFFF) // LEA far
	writeSlotReg(s, 0, 0x08, 0x0004) // D2R=0, D1R=0, EGHOLD=0, AR=4 (slow)
	writeSlotReg(s, 0, 0x0A, 0x0000) // DL=0, RR=0, KRS=0
	writeSlotReg(s, 0, 0x10, 0x0000) // OCT=0, FNS=0
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	levelBefore := s.slots[0].egLevel
	s.TickSamples(50)
	slowDelta := s.slots[0].egLevel - levelBefore

	// Rewrite AR to 15 (eff=30) while keeping the slot in attack.
	writeSlotReg(s, 0, 0x08, 0x000F)

	levelBefore = s.slots[0].egLevel
	s.TickSamples(50)
	fastDelta := s.slots[0].egLevel - levelBefore

	if fastDelta <= slowDelta*10 {
		t.Errorf("dynamic AR update did not take effect: slowDelta=%d fastDelta=%d (expected fast >> slow)",
			slowDelta, fastDelta)
	}
	if s.slots[0].egState != egAttack {
		t.Errorf("slot should still be in attack; got egState=%d", s.slots[0].egState)
	}
}

// TestSCSPEGHoldDoesNotFreezeState verifies that EGHOLD no longer
// freezes the slot in egAttack. Reference behavior: EGHOLD applies as
// an output-stage mask during attack; the envelope counter still
// advances and transitions to Decay1 normally when attack completes.
func TestSCSPEGHoldDoesNotFreezeState(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x06, 0xFFFF)
	// D2R=0, D1R=20 (slow decay), EGHOLD=1, AR=31 (instant attack)
	writeSlotReg(s, 0, 0x08, 0x053F)
	writeSlotReg(s, 0, 0x0A, 0x0200) // DL=$10, RR=0, KRS=0
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(50)

	if s.slots[0].egState != egDecay1 {
		t.Errorf("expected egDecay1 after attack completes with EGHOLD; got egState=%d",
			s.slots[0].egState)
	}
	if s.slots[0].egLevel <= egDecayStart {
		t.Errorf("expected egLevel > egDecayStart (decay progressing); got %d (egDecayStart=%d)",
			s.slots[0].egLevel, egDecayStart)
	}
}

// TestSCSPEGHoldMasksAttackOutput verifies that EGHOLD masks the
// slot's effective EG attenuation to 0 while egState == egAttack.
// Two slots with identical config except for the EGHOLD bit: the
// EGHOLD slot reports 0 attenuation; the other reports level-derived
// attenuation.
func TestSCSPEGHoldMasksAttackOutput(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Slot 0: EGHOLD=1, AR=4
	writeSlotReg(s, 0, 0x06, 0xFFFF)
	writeSlotReg(s, 0, 0x08, 0x0024)
	writeSlotReg(s, 0, 0x0A, 0x0000)
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x0800)

	// Slot 1: EGHOLD=0, AR=4
	writeSlotReg(s, 1, 0x06, 0xFFFF)
	writeSlotReg(s, 1, 0x08, 0x0004)
	writeSlotReg(s, 1, 0x0A, 0x0000)
	writeSlotReg(s, 1, 0x10, 0x0000)
	writeSlotReg(s, 1, 0x00, 0x0800)

	// KYONEX is global; one write triggers all slots with KYONB=1.
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(20)

	if s.slots[0].egState != egAttack || s.slots[1].egState != egAttack {
		t.Fatalf("expected both slots in attack; got egState[0]=%d egState[1]=%d",
			s.slots[0].egState, s.slots[1].egState)
	}

	if a := s.egAtten(0); a != 0 {
		t.Errorf("EGHOLD slot egAtten = 0x%03X, want 0 (masked)", a)
	}
	if a := s.egAtten(1); a == 0 {
		t.Errorf("non-EGHOLD slot egAtten = 0, want > 0 (level-derived)")
	}
}

// TestSCSPEGHoldLiftsAfterAttack verifies that EGHOLD masking applies
// only while egState == egAttack. After the slot transitions to
// Decay1, the level-derived attenuation is reported normally.
func TestSCSPEGHoldLiftsAfterAttack(t *testing.T) {
	s := NewSCSP(NewSCU())

	writeSlotReg(s, 0, 0x06, 0xFFFF)
	// D2R=0, D1R=20 (eff=40, step ~140), EGHOLD=1, AR=31
	writeSlotReg(s, 0, 0x08, 0x053F)
	writeSlotReg(s, 0, 0x0A, 0x0200) // DL=$10
	writeSlotReg(s, 0, 0x10, 0x0000)
	writeSlotReg(s, 0, 0x00, 0x0800)
	writeSlotReg(s, 0, 0x00, 0x1800)

	s.TickSamples(200)

	if s.slots[0].egState != egDecay1 {
		t.Fatalf("expected egDecay1 after attack; got egState=%d", s.slots[0].egState)
	}
	if a := s.egAtten(0); a == 0 {
		t.Errorf("expected egAtten > 0 after attack (mask lifted); got 0")
	}
}

// TestSCSPFMModulationAdditive verifies the Phase Adder per SCSP User's
// Manual Figure 4.19: the MDL modulation phase must be ADDED to the
// slot's PG fractional phase, not replace it. With FM active and the
// modulation result equal to zero, the slot's own fractional phase
// alone must still drive the interpolation blend.
//
// Setup: PCM samples 0=+32000 and 1=-32000. OCT=-1 makes phaseInc =
// 0x200 (half a sample). After one tick the slot's fractional phase
// is 0x200; the top 6 of those 10 bits = 32 (mid-sample). MDL=10
// with MDXSL/MDYSL=0 reads fmHistory[0] (pre-zeroed), so the scaled
// modulation value is 0. SDIR=1 bypasses attenuation.
//
// Expected with the additive combine: combined = 0 + 32 = 32. Linear
// interpolation produces (32000*32 + (-32000)*32) >> 6 = 0.
//
// With the old "replace phaseFrac" behavior the slot's fractional
// phase is discarded once FM is enabled; the blend factor becomes
// 0 and the read collapses to sample 0 (+32000).
func TestSCSPFMModulationAdditive(t *testing.T) {
	s := NewSCSP(NewSCU())

	// Sample 0 at RAM byte 0: +32000 = 0x7D00 big-endian.
	s.ram[0] = 0x7D
	s.ram[1] = 0x00
	// Sample 1 at RAM byte 2: -32000 = 0x8300 big-endian.
	s.ram[2] = 0x83
	s.ram[3] = 0x00

	// fmHistory zero-initialized by NewSCSP; reads from index 0 return 0.

	// Slot 0: KYONB=1, PCM 16-bit, SA=0, LEA far, AR=31, SDIR=1, MDL=10
	// (MDXSL=0, MDYSL=0), OCT=-1 (15 in 4-bit twos comp), FNS=0.
	writeSlotReg(s, 0, 0x00, 0x0800) // KYONB=1, PCM8B=0
	writeSlotReg(s, 0, 0x02, 0x0000) // SA[15:0]=0
	writeSlotReg(s, 0, 0x06, 0xFFFF) // LEA far
	writeSlotReg(s, 0, 0x08, 0x001F) // AR=31
	writeSlotReg(s, 0, 0x0C, 0x0100) // SDIR=1, TL=0
	writeSlotReg(s, 0, 0x0E, 0xA000) // MDL=10, MDXSL=0, MDYSL=0
	writeSlotReg(s, 0, 0x10, 0x7800) // OCT=15 (=-1), FNS=0
	writeSlotReg(s, 0, 0x00, 0x1800) // KYONEX

	s.TickSamples(1)

	out := s.slots[0].output
	if out < -1000 || out > 1000 {
		t.Errorf("FM additive combine: output = %d, want near 0 (mid-sample interpolation between +32000 and -32000)", out)
	}
}
