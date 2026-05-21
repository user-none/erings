// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// smpcDispatch writes COMREG and drains the per-scanline command-dispatch
// delay so the command's effects are visible synchronously. The production
// main loop calls s.TickScanline() once per scanline; this helper simulates
// that draining inline. Use it in tests wherever the assertions depend on
// dispatch having run.
func smpcDispatch(s *SMPC, cmd uint8) {
	s.Write(0x1F, cmd)
	for s.cmdDelay > 0 {
		s.TickScanline()
	}
}

func TestSMPCNewValues(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.comreg != 0 {
		t.Errorf("comreg = 0x%02X, want 0", s.comreg)
	}
	if s.sr != 0 {
		t.Errorf("sr = 0x%02X, want 0", s.sr)
	}
	if s.sf != 0 {
		t.Errorf("sf = 0x%02X, want 0", s.sf)
	}
	for i, v := range s.ireg {
		if v != 0 {
			t.Errorf("ireg[%d] = 0x%02X, want 0", i, v)
		}
	}
	for i, v := range s.oreg {
		if v != 0 {
			t.Errorf("oreg[%d] = 0x%02X, want 0", i, v)
		}
	}
	if s.pdr1 != 0 || s.pdr2 != 0 {
		t.Errorf("pdr1=0x%02X pdr2=0x%02X, want 0", s.pdr1, s.pdr2)
	}
	if s.ddr1 != 0 || s.ddr2 != 0 {
		t.Errorf("ddr1=0x%02X ddr2=0x%02X, want 0", s.ddr1, s.ddr2)
	}
	if s.iosel != 0 || s.exle != 0 {
		t.Errorf("iosel=0x%02X exle=0x%02X, want 0", s.iosel, s.exle)
	}
	if s.areaCode != 0x04 {
		t.Errorf("areaCode = 0x%02X, want 0x04", s.areaCode)
	}
	if s.padState[0] != 0xFFFF || s.padState[1] != 0xFFFF {
		t.Errorf("padState = [0x%04X, 0x%04X], want [0xFFFF, 0xFFFF]", s.padState[0], s.padState[1])
	}
}

func TestSMPCIREGWriteOnly(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.Write(0x01, 0xAB)
	if s.ireg[0] != 0xAB {
		t.Errorf("ireg[0] = 0x%02X, want 0xAB", s.ireg[0])
	}
	if got := s.Read(0x01); got != 0 {
		t.Errorf("Read IREG0 = 0x%02X, want 0 (write-only)", got)
	}
}

func TestSMPCIREGIndexing(t *testing.T) {
	s := NewSMPC(NewSCU())
	for i := 0; i < 7; i++ {
		offset := uint8(0x01 + i*2)
		val := uint8(0x10 + i)
		s.Write(offset, val)
	}
	for i := 0; i < 7; i++ {
		if s.ireg[i] != uint8(0x10+i) {
			t.Errorf("ireg[%d] = 0x%02X, want 0x%02X", i, s.ireg[i], 0x10+i)
		}
	}
}

func TestSMPCOREGReadOnly(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.oreg[0] = 0x42
	if got := s.Read(0x21); got != 0x42 {
		t.Errorf("Read OREG0 = 0x%02X, want 0x42", got)
	}
	// Write to OREG should be ignored
	s.Write(0x21, 0xFF)
	if s.oreg[0] != 0x42 {
		t.Errorf("oreg[0] after write = 0x%02X, want 0x42 (unchanged)", s.oreg[0])
	}
}

func TestSMPCOREGIndexing(t *testing.T) {
	s := NewSMPC(NewSCU())
	for i := 0; i < 32; i++ {
		s.oreg[i] = uint8(0x80 + i)
	}
	for i := 0; i < 32; i++ {
		offset := uint8(0x21 + i*2)
		got := s.Read(offset)
		want := uint8(0x80 + i)
		if got != want {
			t.Errorf("Read OREG%d (offset 0x%02X) = 0x%02X, want 0x%02X", i, offset, got, want)
		}
	}
}

func TestSMPCCOMREGWriteOnly(t *testing.T) {
	s := NewSMPC(NewSCU())
	smpcDispatch(s, 0x10)
	if s.comreg != 0x10 {
		t.Errorf("comreg = 0x%02X, want 0x10", s.comreg)
	}
	if got := s.Read(0x1F); got != 0 {
		t.Errorf("Read COMREG = 0x%02X, want 0 (write-only)", got)
	}
}

func TestSMPCSRReadOnly(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.sr = 0xAB
	if got := s.Read(0x61); got != 0xAB {
		t.Errorf("Read SR = 0x%02X, want 0xAB", got)
	}
	// Write to SR should be ignored
	s.Write(0x61, 0xFF)
	if s.sr != 0xAB {
		t.Errorf("sr after write = 0x%02X, want 0xAB (unchanged)", s.sr)
	}
}

func TestSMPCSFReadWrite(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.Write(0x63, 0x01)
	if got := s.Read(0x63); got != 0x01 {
		t.Errorf("Read SF = 0x%02X, want 0x01", got)
	}
	// Writing 0xFF should mask to bit 0
	s.Write(0x63, 0xFF)
	if got := s.Read(0x63); got != 0x01 {
		t.Errorf("Read SF after 0xFF write = 0x%02X, want 0x01", got)
	}
	s.Write(0x63, 0x00)
	if got := s.Read(0x63); got != 0x00 {
		t.Errorf("Read SF after 0x00 write = 0x%02X, want 0x00", got)
	}
}

func TestSMPCPDRReadWrite(t *testing.T) {
	s := NewSMPC(NewSCU())
	// PDR1
	s.Write(0x75, 0xFF)
	if got := s.Read(0x75); got != 0x7F {
		t.Errorf("Read PDR1 = 0x%02X, want 0x7F", got)
	}
	// PDR2
	s.Write(0x77, 0xFF)
	if got := s.Read(0x77); got != 0x7F {
		t.Errorf("Read PDR2 = 0x%02X, want 0x7F", got)
	}
}

func TestSMPCDDRWriteOnly(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.Write(0x79, 0xFF)
	if s.ddr1 != 0x7F {
		t.Errorf("ddr1 = 0x%02X, want 0x7F", s.ddr1)
	}
	if got := s.Read(0x79); got != 0 {
		t.Errorf("Read DDR1 = 0x%02X, want 0 (write-only)", got)
	}
	s.Write(0x7B, 0xFF)
	if s.ddr2 != 0x7F {
		t.Errorf("ddr2 = 0x%02X, want 0x7F", s.ddr2)
	}
	if got := s.Read(0x7B); got != 0 {
		t.Errorf("Read DDR2 = 0x%02X, want 0 (write-only)", got)
	}
}

func TestSMPCIOSELWriteOnly(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.Write(0x7D, 0xFF)
	if s.iosel != 0x03 {
		t.Errorf("iosel = 0x%02X, want 0x03", s.iosel)
	}
	if got := s.Read(0x7D); got != 0 {
		t.Errorf("Read IOSEL = 0x%02X, want 0 (write-only)", got)
	}
}

func TestSMPCEXLEWriteOnly(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.Write(0x7F, 0xFF)
	if s.exle != 0x03 {
		t.Errorf("exle = 0x%02X, want 0x03", s.exle)
	}
	if got := s.Read(0x7F); got != 0 {
		t.Errorf("Read EXLE = 0x%02X, want 0 (write-only)", got)
	}
}

func TestSMPCEvenAddressIgnored(t *testing.T) {
	s := NewSMPC(NewSCU())
	// Even addresses should return 0 on read
	for _, off := range []uint8{0x00, 0x02, 0x20} {
		if got := s.Read(off); got != 0 {
			t.Errorf("Read even offset 0x%02X = 0x%02X, want 0", off, got)
		}
	}
	// Write to even address should have no effect
	s.Write(0x00, 0xFF)
	s.Write(0x02, 0xFF)
	s.Write(0x20, 0xFF)
}

func TestSMPCUnmappedOddAddress(t *testing.T) {
	s := NewSMPC(NewSCU())
	for _, off := range []uint8{0x0F, 0x11, 0x65} {
		if got := s.Read(off); got != 0 {
			t.Errorf("Read unmapped odd offset 0x%02X = 0x%02X, want 0", off, got)
		}
	}
}

func TestSMPCDispatchTypeBClearsSF(t *testing.T) {
	// Type B commands should clear SF after dispatch.
	typeBCmds := []uint8{0x00, 0x02, 0x03, 0x06, 0x07, 0x08, 0x09, 0x19, 0x1A}
	for _, cmd := range typeBCmds {
		s := NewSMPC(NewSCU())
		s.slaveReset = func() {}
		s.sf = 1
		smpcDispatch(s, cmd)
		if s.sf != 0 {
			t.Errorf("cmd 0x%02X: sf = %d, want 0", cmd, s.sf)
		}
	}
}

func TestSMPCDispatchTypeCClearsSF(t *testing.T) {
	// Type C commands should clear SF after dispatch.
	typeCCmds := []uint8{0x16, 0x17}
	for _, cmd := range typeCCmds {
		s := NewSMPC(NewSCU())
		s.sf = 1
		smpcDispatch(s, cmd)
		if s.sf != 0 {
			t.Errorf("cmd 0x%02X: sf = %d, want 0", cmd, s.sf)
		}
	}
}

func TestSMPCDispatchTypeDRaisesInterrupt(t *testing.T) {
	scu := NewSCU()
	s := NewSMPC(scu)
	s.sf = 1
	s.Write(0x01, 0x01)   // IREG0 = system data requested
	smpcDispatch(s, 0x10) // INTBACK
	// IST bit 7 = System Manager
	if scu.ist&(1<<7) == 0 {
		t.Error("INTBACK did not raise System Manager interrupt (IST bit 7)")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after INTBACK", s.sf)
	}
}

func TestSMPCDispatchTypeAClearsSF(t *testing.T) {
	// Type A commands should clear SF after execution.
	typeACmds := []uint8{0x18, 0x0D, 0x0E, 0x0F}
	for _, cmd := range typeACmds {
		s := NewSMPC(NewSCU())
		s.masterNMI = func() {}
		s.systemReset = func() {}
		s.sf = 1
		smpcDispatch(s, cmd)
		if s.sf != 0 {
			t.Errorf("cmd 0x%02X: sf = %d, want 0", cmd, s.sf)
		}
	}
}

func TestSMPCOREG31RetainsCommandCode(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.slaveReset = func() {}
	smpcDispatch(s, 0x00) // MSHON
	if s.oreg[31] != 0x00 {
		t.Errorf("oreg[31] = 0x%02X, want 0x00 after MSHON", s.oreg[31])
	}

	smpcDispatch(s, 0x02) // SSHON
	if s.oreg[31] != 0x02 {
		t.Errorf("oreg[31] = 0x%02X, want 0x02 after SSHON", s.oreg[31])
	}

	smpcDispatch(s, 0x10) // INTBACK
	if s.oreg[31] != 0x10 {
		t.Errorf("oreg[31] = 0x%02X, want 0x10 after INTBACK", s.oreg[31])
	}
}

func TestSMPCDispatchUnknownCommand(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.sf = 1
	smpcDispatch(s, 0xFF) // unknown command
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after unknown command", s.sf)
	}
	if s.oreg[31] != 0xFF {
		t.Errorf("oreg[31] = 0x%02X, want 0xFF after unknown command", s.oreg[31])
	}
}

func TestSMPCNewPreservesReferences(t *testing.T) {
	scu := NewSCU()
	s := NewSMPC(scu)
	if s.scu == nil {
		t.Error("scu nil after NewSMPC")
	}
	if s.scu != scu {
		t.Error("scu does not match")
	}
}

func TestSMPCDispatchWithoutSFSet(t *testing.T) {
	// Dispatch should still run even if SF was not set first.
	scu := NewSCU()
	s := NewSMPC(scu)
	s.Write(0x01, 0x01)   // IREG0 = system data requested
	smpcDispatch(s, 0x10) // INTBACK without SF=1
	if scu.ist&(1<<7) == 0 {
		t.Error("dispatch did not run without SF set")
	}
}

func TestSMPCTypeBSSH(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.slaveReset = func() {}
	if s.SSHEnabled() {
		t.Error("sshEnabled should be false after NewSMPC")
	}

	// SSHON (0x02)
	s.sf = 1
	smpcDispatch(s, 0x02)
	if !s.SSHEnabled() {
		t.Error("sshEnabled should be true after SSHON")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after SSHON", s.sf)
	}
	if s.oreg[31] != 0x02 {
		t.Errorf("oreg[31] = 0x%02X, want 0x02", s.oreg[31])
	}

	// SSHOFF (0x03)
	s.sf = 1
	smpcDispatch(s, 0x03)
	if s.SSHEnabled() {
		t.Error("sshEnabled should be false after SSHOFF")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after SSHOFF", s.sf)
	}
	if s.oreg[31] != 0x03 {
		t.Errorf("oreg[31] = 0x%02X, want 0x03", s.oreg[31])
	}
}

func TestSMPCTypeBSound(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.SoundEnabled() {
		t.Error("soundEnabled should be false after NewSMPC")
	}

	// SNDON (0x06)
	s.sf = 1
	smpcDispatch(s, 0x06)
	if !s.SoundEnabled() {
		t.Error("soundEnabled should be true after SNDON")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after SNDON", s.sf)
	}
	if s.oreg[31] != 0x06 {
		t.Errorf("oreg[31] = 0x%02X, want 0x06", s.oreg[31])
	}

	// SNDOFF (0x07)
	s.sf = 1
	smpcDispatch(s, 0x07)
	if s.SoundEnabled() {
		t.Error("soundEnabled should be false after SNDOFF")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after SNDOFF", s.sf)
	}
	if s.oreg[31] != 0x07 {
		t.Errorf("oreg[31] = 0x%02X, want 0x07", s.oreg[31])
	}
}

func TestSMPCTypeBCD(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.cdEnabled {
		t.Error("cdEnabled should be false after NewSMPC")
	}

	// CDON (0x08)
	s.sf = 1
	smpcDispatch(s, 0x08)
	if !s.cdEnabled {
		t.Error("cdEnabled should be true after CDON")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after CDON", s.sf)
	}
	if s.oreg[31] != 0x08 {
		t.Errorf("oreg[31] = 0x%02X, want 0x08", s.oreg[31])
	}

	// CDOFF (0x09)
	s.sf = 1
	smpcDispatch(s, 0x09)
	if s.cdEnabled {
		t.Error("cdEnabled should be false after CDOFF")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after CDOFF", s.sf)
	}
	if s.oreg[31] != 0x09 {
		t.Errorf("oreg[31] = 0x%02X, want 0x09", s.oreg[31])
	}
}

func TestSMPCTypeBReset(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.resetEnabled {
		t.Error("resetEnabled should be false after NewSMPC")
	}

	// RESENAB (0x19)
	s.sf = 1
	smpcDispatch(s, 0x19)
	if !s.resetEnabled {
		t.Error("resetEnabled should be true after RESENAB")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after RESENAB", s.sf)
	}
	if s.oreg[31] != 0x19 {
		t.Errorf("oreg[31] = 0x%02X, want 0x19", s.oreg[31])
	}

	// RESDISA (0x1A)
	s.sf = 1
	smpcDispatch(s, 0x1A)
	if s.resetEnabled {
		t.Error("resetEnabled should be false after RESDISA")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after RESDISA", s.sf)
	}
	if s.oreg[31] != 0x1A {
		t.Errorf("oreg[31] = 0x%02X, want 0x1A", s.oreg[31])
	}
}

func TestSMPCDotSelectDefault(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.dotsel {
		t.Error("DotSelect should be false (320 mode) after NewSMPC")
	}
}

func TestSMPCNewDotselFalse(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.dotsel {
		t.Error("DotSelect should be false after NewSMPC")
	}
}

func TestSMPCSYSRESClearsFlags(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.systemReset = func() {}
	s.slaveReset = func() {}
	// Enable all subsystem flags
	smpcDispatch(s, 0x02) // SSHON
	smpcDispatch(s, 0x06) // SNDON
	smpcDispatch(s, 0x08) // CDON
	smpcDispatch(s, 0x19) // RESENAB

	// Set dotsel to true to verify it is preserved
	s.dotsel = true

	s.sf = 1
	smpcDispatch(s, 0x0D) // SYSRES
	if s.SSHEnabled() {
		t.Error("sshEnabled should be false after SYSRES")
	}
	if s.SoundEnabled() {
		t.Error("soundEnabled should be false after SYSRES")
	}
	// CD and reset enable are NOT cleared by SYSRES - only CDON/CDOFF
	// and RESENAB/RESDISA affect those flags.
	if !s.cdEnabled {
		t.Error("cdEnabled should be preserved after SYSRES")
	}
	if !s.resetEnabled {
		t.Error("resetEnabled should be preserved after SYSRES")
	}
	if !s.dotsel {
		t.Error("DotSelect should be preserved (true) after SYSRES")
	}
	if s.oreg[31] != 0x0D {
		t.Errorf("oreg[31] = 0x%02X, want 0x0D", s.oreg[31])
	}
}

func TestSMPCCKCHG352(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.masterNMI = func() {}
	s.systemReset = func() {}
	s.slaveReset = func() {}
	// Enable all subsystem flags
	smpcDispatch(s, 0x02) // SSHON
	smpcDispatch(s, 0x06) // SNDON
	smpcDispatch(s, 0x08) // CDON
	smpcDispatch(s, 0x19) // RESENAB

	s.sf = 1
	smpcDispatch(s, 0x0E) // CKCHG352
	if !s.dotsel {
		t.Error("DotSelect should be true after CKCHG352")
	}
	if s.SSHEnabled() {
		t.Error("sshEnabled should be false after CKCHG352")
	}
	if s.SoundEnabled() {
		t.Error("soundEnabled should be false after CKCHG352")
	}
	// CD and reset enable are NOT cleared by CKCHG
	if !s.cdEnabled {
		t.Error("cdEnabled should be preserved after CKCHG352")
	}
	if !s.resetEnabled {
		t.Error("resetEnabled should be preserved after CKCHG352")
	}
	if s.oreg[31] != 0x0E {
		t.Errorf("oreg[31] = 0x%02X, want 0x0E", s.oreg[31])
	}
}

func TestSMPCCKCHG320(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.masterNMI = func() {}
	s.systemReset = func() {}
	s.slaveReset = func() {}
	// Set dotsel true first
	s.dotsel = true

	// Enable all subsystem flags
	smpcDispatch(s, 0x02) // SSHON
	smpcDispatch(s, 0x06) // SNDON
	smpcDispatch(s, 0x08) // CDON
	smpcDispatch(s, 0x19) // RESENAB

	s.sf = 1
	smpcDispatch(s, 0x0F) // CKCHG320
	if s.dotsel {
		t.Error("DotSelect should be false after CKCHG320")
	}
	if s.SSHEnabled() {
		t.Error("sshEnabled should be false after CKCHG320")
	}
	if s.SoundEnabled() {
		t.Error("soundEnabled should be false after CKCHG320")
	}
	// CD and reset enable are NOT cleared by CKCHG
	if !s.cdEnabled {
		t.Error("cdEnabled should be preserved after CKCHG320")
	}
	if !s.resetEnabled {
		t.Error("resetEnabled should be preserved after CKCHG320")
	}
	if s.oreg[31] != 0x0F {
		t.Errorf("oreg[31] = 0x%02X, want 0x0F", s.oreg[31])
	}
}

func TestSMPCCKCHG352ThenCKCHG320(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.masterNMI = func() {}
	s.systemReset = func() {}
	smpcDispatch(s, 0x0E) // CKCHG352
	if !s.dotsel {
		t.Error("DotSelect should be true after CKCHG352")
	}
	smpcDispatch(s, 0x0F) // CKCHG320
	if s.dotsel {
		t.Error("DotSelect should be false after CKCHG320")
	}
}

func TestSMPCTypeBFlagIsolation(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.slaveReset = func() {}
	// Enable all flags
	smpcDispatch(s, 0x02) // SSHON
	smpcDispatch(s, 0x06) // SNDON
	smpcDispatch(s, 0x08) // CDON
	smpcDispatch(s, 0x19) // RESENAB

	// Disable SSH only
	smpcDispatch(s, 0x03) // SSHOFF
	if s.SSHEnabled() {
		t.Error("sshEnabled should be false after SSHOFF")
	}
	if !s.SoundEnabled() {
		t.Error("soundEnabled should still be true")
	}
	if !s.cdEnabled {
		t.Error("cdEnabled should still be true")
	}
	if !s.resetEnabled {
		t.Error("resetEnabled should still be true")
	}
}

func TestSMPCSETTIMECopiesToRTC(t *testing.T) {
	s := NewSMPC(NewSCU())
	// Write IREG0-6 with RTC data: 2024/01/15 Monday 10:30:45
	rtcData := [7]uint8{0x20, 0x24, 0x11, 0x15, 0x10, 0x30, 0x45}
	for i, v := range rtcData {
		s.Write(uint8(0x01+i*2), v)
	}
	s.sf = 1
	smpcDispatch(s, 0x16) // SETTIME
	got := s.rtc
	if got != rtcData {
		t.Errorf("RTC = %v, want %v", got, rtcData)
	}
	if s.oreg[31] != 0x16 {
		t.Errorf("oreg[31] = 0x%02X, want 0x16", s.oreg[31])
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after SETTIME", s.sf)
	}
}

func TestSMPCSETSMEMCopiesToSMEM(t *testing.T) {
	s := NewSMPC(NewSCU())
	smemData := [4]uint8{0xAA, 0xBB, 0xCC, 0xDD}
	for i, v := range smemData {
		s.Write(uint8(0x01+i*2), v)
	}
	s.sf = 1
	smpcDispatch(s, 0x17) // SETSMEM
	got := s.smem
	if got != smemData {
		t.Errorf("SMEM = %v, want %v", got, smemData)
	}
	if s.oreg[31] != 0x17 {
		t.Errorf("oreg[31] = 0x%02X, want 0x17", s.oreg[31])
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after SETSMEM", s.sf)
	}
}

func TestSMPCRTCInitFromHost(t *testing.T) {
	s := NewSMPC(NewSCU())
	// RTC should be initialized to current host time, not zero
	allZero := true
	for _, v := range s.rtc {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("RTC should be initialized to host time, not all zeros")
	}
	// Year century should be 0x20 (2000s)
	if s.rtc[0] != 0x20 {
		t.Errorf("RTC year century = 0x%02X, want 0x20", s.rtc[0])
	}
}

func TestSMPCSMEMDefaultZero(t *testing.T) {
	s := NewSMPC(NewSCU())
	got := s.smem
	for i, v := range got {
		if v != 0 {
			t.Errorf("SMEM()[%d] = 0x%02X, want 0", i, v)
		}
	}
}

func TestSMPCSETTIMEOverwrite(t *testing.T) {
	s := NewSMPC(NewSCU())
	// First SETTIME
	for i, v := range []uint8{0x19, 0x93, 0x5C, 0x31, 0x23, 0x59, 0x59} {
		s.Write(uint8(0x01+i*2), v)
	}
	smpcDispatch(s, 0x16)
	// Second SETTIME with different values
	for i, v := range []uint8{0x20, 0x25, 0x23, 0x16, 0x14, 0x00, 0x00} {
		s.Write(uint8(0x01+i*2), v)
	}
	smpcDispatch(s, 0x16)
	got := s.rtc
	want := [7]uint8{0x20, 0x25, 0x23, 0x16, 0x14, 0x00, 0x00}
	if got != want {
		t.Errorf("RTC after second SETTIME = %v, want %v", got, want)
	}
}

func TestSMPCSETSMEMOverwrite(t *testing.T) {
	s := NewSMPC(NewSCU())
	// First SETSMEM
	for i, v := range []uint8{0x01, 0x02, 0x03, 0x04} {
		s.Write(uint8(0x01+i*2), v)
	}
	smpcDispatch(s, 0x17)
	// Second SETSMEM with different values
	for i, v := range []uint8{0xF0, 0xE0, 0xD0, 0xC0} {
		s.Write(uint8(0x01+i*2), v)
	}
	smpcDispatch(s, 0x17)
	got := s.smem
	want := [4]uint8{0xF0, 0xE0, 0xD0, 0xC0}
	if got != want {
		t.Errorf("SMEM after second SETSMEM = %v, want %v", got, want)
	}
}

func TestSMPCNewFlagsDefault(t *testing.T) {
	s := NewSMPC(NewSCU())

	if s.SSHEnabled() {
		t.Error("sshEnabled should be false after NewSMPC")
	}
	if s.SoundEnabled() {
		t.Error("soundEnabled should be false after NewSMPC")
	}
	if s.cdEnabled {
		t.Error("cdEnabled should be false after NewSMPC")
	}
	if s.resetEnabled {
		t.Error("resetEnabled should be false after NewSMPC")
	}
}

// helper to issue INTBACK with IREG0=0x01 (request system data)
func smpcINTBACK(s *SMPC) {
	s.Write(0x01, 0x01) // IREG0 = 0x01 (system data requested)
	s.sf = 1
	smpcDispatch(s, 0x10) // INTBACK
}

func TestSMPCINTBACKSystemDataDefaults(t *testing.T) {
	s := NewSMPC(NewSCU())

	smpcINTBACK(s)

	// OREG0: STE=1 (RTC initialized=true), RESD=1 (reset disabled by default)
	if s.oreg[0] != 0xC0 {
		t.Errorf("oreg[0] = 0x%02X, want 0xC0", s.oreg[0])
	}
	// OREG1-7: RTC initialized from host time (not zeros)
	// OREG1 = year century, should be 0x20 (2000s)
	if s.oreg[1] != 0x20 {
		t.Errorf("oreg[1] (year century) = 0x%02X, want 0x20", s.oreg[1])
	}
	// OREG8: cartridge code = 0
	if s.oreg[8] != 0 {
		t.Errorf("oreg[8] = 0x%02X, want 0", s.oreg[8])
	}
	// OREG9: area code = 0x04 (North America)
	if s.oreg[9] != 0x04 {
		t.Errorf("oreg[9] = 0x%02X, want 0x04", s.oreg[9])
	}
	// OREG10: SS1 = 0x35 (b5+b4+b2=0x34, SNDRES=1 since sound off)
	if s.oreg[10] != 0x35 {
		t.Errorf("oreg[10] = 0x%02X, want 0x35", s.oreg[10])
	}
	// OREG11: SS2 = 0x40 (CDRES=1 since CD off)
	if s.oreg[11] != 0x40 {
		t.Errorf("oreg[11] = 0x%02X, want 0x40", s.oreg[11])
	}
	// OREG12-15: SMEM all zeros
	for i := 12; i <= 15; i++ {
		if s.oreg[i] != 0 {
			t.Errorf("oreg[%d] = 0x%02X, want 0", i, s.oreg[i])
		}
	}
	// OREG31: command code
	if s.oreg[31] != 0x10 {
		t.Errorf("oreg[31] = 0x%02X, want 0x10", s.oreg[31])
	}
	// SF cleared
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0", s.sf)
	}
	// SR = 0x40 (bit6=1 fixed, PDE=0)
	if s.sr != 0x40 {
		t.Errorf("sr = 0x%02X, want 0x40", s.sr)
	}
}

func TestSMPCINTBACKWithSETTIME(t *testing.T) {
	s := NewSMPC(NewSCU())

	// SETTIME first
	rtcData := [7]uint8{0x20, 0x26, 0x13, 0x16, 0x14, 0x30, 0x00}
	for i, v := range rtcData {
		s.Write(uint8(0x01+i*2), v)
	}
	smpcDispatch(s, 0x16) // SETTIME

	smpcINTBACK(s)

	// OREG0: STE=1 (SETTIME called), RESD=1 (reset disabled)
	if s.oreg[0] != 0xC0 {
		t.Errorf("oreg[0] = 0x%02X, want 0xC0", s.oreg[0])
	}
	// OREG1-7: RTC data
	for i := 0; i < 7; i++ {
		if s.oreg[1+i] != rtcData[i] {
			t.Errorf("oreg[%d] = 0x%02X, want 0x%02X", 1+i, s.oreg[1+i], rtcData[i])
		}
	}
}

func TestSMPCINTBACKWithSETSMEM(t *testing.T) {
	s := NewSMPC(NewSCU())

	// SETSMEM
	smemData := [4]uint8{0xAA, 0xBB, 0xCC, 0xDD}
	for i, v := range smemData {
		s.Write(uint8(0x01+i*2), v)
	}
	smpcDispatch(s, 0x17) // SETSMEM

	smpcINTBACK(s)

	for i := 0; i < 4; i++ {
		if s.oreg[12+i] != smemData[i] {
			t.Errorf("oreg[%d] = 0x%02X, want 0x%02X", 12+i, s.oreg[12+i], smemData[i])
		}
	}
}

func TestSMPCINTBACKAreaCode(t *testing.T) {
	s := NewSMPC(NewSCU())

	s.areaCode = 0x04 // Japan

	smpcINTBACK(s)

	if s.oreg[9] != 0x04 {
		t.Errorf("oreg[9] = 0x%02X, want 0x04", s.oreg[9])
	}
}

func TestSMPCINTBACKRESD(t *testing.T) {
	s := NewSMPC(NewSCU())

	// Enable reset -> RESD=0
	smpcDispatch(s, 0x19) // RESENAB
	smpcINTBACK(s)
	if s.oreg[0]&0x40 != 0 {
		t.Errorf("RESD should be 0 when reset enabled, oreg[0] = 0x%02X", s.oreg[0])
	}

	// Disable reset -> RESD=1
	smpcDispatch(s, 0x1A) // RESDISA
	smpcINTBACK(s)
	if s.oreg[0]&0x40 == 0 {
		t.Errorf("RESD should be 1 when reset disabled, oreg[0] = 0x%02X", s.oreg[0])
	}
}

func TestSMPCINTBACKDOTSEL(t *testing.T) {
	s := NewSMPC(NewSCU())

	// Default 320 mode -> DOTSEL=0
	smpcINTBACK(s)
	if s.oreg[10]&0x40 != 0 {
		t.Errorf("DOTSEL should be 0 in 320 mode, oreg[10] = 0x%02X", s.oreg[10])
	}

	// 352 mode -> DOTSEL=1
	s.dotsel = true
	smpcINTBACK(s)
	if s.oreg[10]&0x40 == 0 {
		t.Errorf("DOTSEL should be 1 in 352 mode, oreg[10] = 0x%02X", s.oreg[10])
	}
}

func TestSMPCINTBACKSNDRES(t *testing.T) {
	s := NewSMPC(NewSCU())

	// Sound off -> SNDRES=1
	smpcINTBACK(s)
	if s.oreg[10]&0x01 == 0 {
		t.Errorf("SNDRES should be 1 when sound off, oreg[10] = 0x%02X", s.oreg[10])
	}

	// Sound on -> SNDRES=0
	smpcDispatch(s, 0x06) // SNDON
	smpcINTBACK(s)
	if s.oreg[10]&0x01 != 0 {
		t.Errorf("SNDRES should be 0 when sound on, oreg[10] = 0x%02X", s.oreg[10])
	}
}

func TestSMPCINTBACKCDRES(t *testing.T) {
	s := NewSMPC(NewSCU())

	// CD off -> CDRES=1
	smpcINTBACK(s)
	if s.oreg[11]&0x40 == 0 {
		t.Errorf("CDRES should be 1 when CD off, oreg[11] = 0x%02X", s.oreg[11])
	}

	// CD on -> CDRES=0
	smpcDispatch(s, 0x08) // CDON
	smpcINTBACK(s)
	if s.oreg[11]&0x40 != 0 {
		t.Errorf("CDRES should be 0 when CD on, oreg[11] = 0x%02X", s.oreg[11])
	}
}

func TestSMPCINTBACKFixedBits(t *testing.T) {
	s := NewSMPC(NewSCU())

	// b5, b4, b2 should always be 1 regardless of state
	smpcINTBACK(s)
	fixed := s.oreg[10] & 0x34
	if fixed != 0x34 {
		t.Errorf("fixed bits = 0x%02X, want 0x34", fixed)
	}

	// Enable everything, fixed bits still set
	smpcDispatch(s, 0x06) // SNDON
	smpcDispatch(s, 0x08) // CDON
	s.dotsel = true
	smpcINTBACK(s)
	fixed = s.oreg[10] & 0x34
	if fixed != 0x34 {
		t.Errorf("fixed bits with all enabled = 0x%02X, want 0x34", fixed)
	}
}

func TestSMPCINTBACKIREG0ZeroSkipsSystemData(t *testing.T) {
	s := NewSMPC(NewSCU())

	// Set some state that would show in OREGs
	s.areaCode = 0x0A
	s.smem[0] = 0xFF

	// INTBACK with IREG0=0x00 (no system data)
	s.Write(0x01, 0x00) // IREG0 = 0x00
	s.sf = 1
	smpcDispatch(s, 0x10) // INTBACK

	// OREGs 0-15 should remain at 0 (never populated)
	for i := 0; i < 16; i++ {
		if s.oreg[i] != 0 {
			t.Errorf("oreg[%d] = 0x%02X, want 0 (system data not requested)", i, s.oreg[i])
		}
	}
}

func TestSMPCINTBACKInterruptAndSF(t *testing.T) {
	scu := NewSCU()
	s := NewSMPC(scu)
	smpcINTBACK(s)
	if scu.ist&(1<<7) == 0 {
		t.Error("INTBACK did not raise System Manager interrupt")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after INTBACK", s.sf)
	}
}

func TestSMPCSettimeDoneDefault(t *testing.T) {
	s := NewSMPC(NewSCU())

	smpcINTBACK(s)
	// RTC initialized is hardcoded true, so STE should be set
	if s.oreg[0]&0x80 == 0 {
		t.Errorf("STE should be 1 (RTC initialized=true), oreg[0] = 0x%02X", s.oreg[0])
	}
}

func TestSMPCNewSettimeDoneTrue(t *testing.T) {
	s := NewSMPC(NewSCU())

	smpcINTBACK(s)
	// RTC initialized starts true (hardcoded), so STE bit should be set
	if s.oreg[0]&0x80 == 0 {
		t.Errorf("STE should be 1 after NewSMPC (RTC initialized=true), oreg[0] = 0x%02X", s.oreg[0])
	}
}

func TestSMPCNewAreaCodeDefault(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.areaCode != 0x04 {
		t.Errorf("areaCode = 0x%02X, want 0x04 after NewSMPC", s.areaCode)
	}
}

// --- Phase 4.7: INTBACK Peripheral Data Mode ---

// helper to issue peripheral-only INTBACK (IREG0=0x00, PEN=1)
func smpcINTBACKPeripheral(s *SMPC, p1md, p2md uint8) {
	s.Write(0x01, 0x00)                     // IREG0 = 0x00 (no system data)
	s.Write(0x03, (p2md<<6)|(p1md<<4)|0x08) // IREG1: P2MD | P1MD | PEN=1
	s.Write(0x05, 0xF0)                     // IREG2 = 0xF0
	s.sf = 1
	smpcDispatch(s, 0x10) // INTBACK
}

// helper to issue system+peripheral INTBACK (IREG0=0x01, PEN=1)
func smpcINTBACKSysPeripheral(s *SMPC, p1md, p2md uint8) {
	s.Write(0x01, 0x01)                     // IREG0 = 0x01 (system data)
	s.Write(0x03, (p2md<<6)|(p1md<<4)|0x08) // IREG1: P2MD | P1MD | PEN=1
	s.Write(0x05, 0xF0)                     // IREG2 = 0xF0
	s.sf = 1
	smpcDispatch(s, 0x10) // INTBACK
}

func TestSMPCINTBACKSystemDataOnlySR(t *testing.T) {
	// System data only (IREG0=0x01, PEN=0): SR=0x40
	s := NewSMPC(NewSCU())

	smpcINTBACK(s) // uses existing helper: IREG0=0x01, PEN=0
	if s.sr != 0x40 {
		t.Errorf("sr = 0x%02X, want 0x40", s.sr)
	}
	if s.intbackActive {
		t.Error("intbackActive should be false with PEN=0")
	}
}

func TestSMPCINTBACKPeripheralBothPorts15Byte(t *testing.T) {
	// Both ports 15-byte mode (P1MD=00, P2MD=00)
	s := NewSMPC(NewSCU())

	smpcINTBACKPeripheral(s, 0, 0)

	// Port 1: status=0xF1, ID=0x02, buttons=0xFF,0xFF (all released)
	if s.oreg[0] != 0xF1 {
		t.Errorf("oreg[0] = 0x%02X, want 0xF1", s.oreg[0])
	}
	if s.oreg[1] != 0x02 {
		t.Errorf("oreg[1] = 0x%02X, want 0x02", s.oreg[1])
	}
	if s.oreg[2] != 0xFF {
		t.Errorf("oreg[2] = 0x%02X, want 0xFF", s.oreg[2])
	}
	if s.oreg[3] != 0xFF {
		t.Errorf("oreg[3] = 0x%02X, want 0xFF", s.oreg[3])
	}
	// Port 2: status=0xF1, ID=0x02, buttons=0xFF,0xFF
	if s.oreg[4] != 0xF1 {
		t.Errorf("oreg[4] = 0x%02X, want 0xF1", s.oreg[4])
	}
	if s.oreg[5] != 0x02 {
		t.Errorf("oreg[5] = 0x%02X, want 0x02", s.oreg[5])
	}
	if s.oreg[6] != 0xFF {
		t.Errorf("oreg[6] = 0x%02X, want 0xFF", s.oreg[6])
	}
	if s.oreg[7] != 0xFF {
		t.Errorf("oreg[7] = 0x%02X, want 0xFF", s.oreg[7])
	}
	// SR: bit7=1, PDL=1, NPE=0, P2MD=00, P1MD=00 = 0xC0
	if s.sr != 0xC0 {
		t.Errorf("sr = 0x%02X, want 0xC0", s.sr)
	}
	if s.intbackActive {
		t.Error("intbackActive should be false after peripheral response")
	}
}

func TestSMPCINTBACKPeripheralPort1ZeroByte(t *testing.T) {
	// Port 1 in 0-byte mode (P1MD=11), port 2 in 15-byte mode (P2MD=00)
	s := NewSMPC(NewSCU())

	smpcINTBACKPeripheral(s, 3, 0)

	// Only port 2 data: OREG0=0xF1 (port status), OREG1=0x02 (ID)
	if s.oreg[0] != 0xF1 {
		t.Errorf("oreg[0] = 0x%02X, want 0xF1", s.oreg[0])
	}
	if s.oreg[1] != 0x02 {
		t.Errorf("oreg[1] = 0x%02X, want 0x02", s.oreg[1])
	}
	if s.oreg[2] != 0xFF {
		t.Errorf("oreg[2] = 0x%02X, want 0xFF", s.oreg[2])
	}
	if s.oreg[3] != 0xFF {
		t.Errorf("oreg[3] = 0x%02X, want 0xFF", s.oreg[3])
	}
	// SR: bit7=1, PDL=1, NPE=0, P2MD=00, P1MD=11 = 0xC3
	if s.sr != 0xC3 {
		t.Errorf("sr = 0x%02X, want 0xC3", s.sr)
	}
}

func TestSMPCINTBACKPeripheralPort2ZeroByte(t *testing.T) {
	// Port 1 in 15-byte mode (P1MD=00), port 2 in 0-byte mode (P2MD=11)
	s := NewSMPC(NewSCU())

	smpcINTBACKPeripheral(s, 0, 3)

	// Only port 1 data: OREG0=0xF1 (port status), OREG1=0x02 (ID)
	if s.oreg[0] != 0xF1 {
		t.Errorf("oreg[0] = 0x%02X, want 0xF1", s.oreg[0])
	}
	if s.oreg[1] != 0x02 {
		t.Errorf("oreg[1] = 0x%02X, want 0x02", s.oreg[1])
	}
	if s.oreg[2] != 0xFF {
		t.Errorf("oreg[2] = 0x%02X, want 0xFF", s.oreg[2])
	}
	if s.oreg[3] != 0xFF {
		t.Errorf("oreg[3] = 0x%02X, want 0xFF", s.oreg[3])
	}
	// SR: bit7=1, PDL=1, NPE=0, P2MD=11, P1MD=00 = 0xCC
	if s.sr != 0xCC {
		t.Errorf("sr = 0x%02X, want 0xCC", s.sr)
	}
}

func TestSMPCINTBACKPeripheralBothZeroByte(t *testing.T) {
	// Both ports 0-byte mode (P1MD=11, P2MD=11)
	s := NewSMPC(NewSCU())

	// Pre-fill OREGs to verify they are untouched
	for i := range s.oreg {
		s.oreg[i] = 0xAA
	}

	smpcINTBACKPeripheral(s, 3, 3)

	// No OREG data written for either port (both 0-byte mode)
	// OREGs should retain pre-fill value (0xAA) for indices 0+
	// (dispatch sets oreg[31]=0x10 though)
	if s.oreg[0] != 0xAA {
		t.Errorf("oreg[0] = 0x%02X, want 0xAA (untouched)", s.oreg[0])
	}
	// SR: bit7=1, PDL=1, NPE=0, P2MD=11, P1MD=11 = 0xCF
	if s.sr != 0xCF {
		t.Errorf("sr = 0x%02X, want 0xCF", s.sr)
	}
}

func TestSMPCINTBACKPeripheral255ByteMode(t *testing.T) {
	// 255-byte mode (P1MD=01, P2MD=01) behaves same as 15-byte
	s := NewSMPC(NewSCU())

	smpcINTBACKPeripheral(s, 1, 1)

	// Port 1: status=0xF1, ID=0x02
	if s.oreg[0] != 0xF1 {
		t.Errorf("oreg[0] = 0x%02X, want 0xF1", s.oreg[0])
	}
	if s.oreg[1] != 0x02 {
		t.Errorf("oreg[1] = 0x%02X, want 0x02", s.oreg[1])
	}
	// Port 2: status=0xF1, ID=0x02
	if s.oreg[4] != 0xF1 {
		t.Errorf("oreg[4] = 0x%02X, want 0xF1", s.oreg[4])
	}
	if s.oreg[5] != 0x02 {
		t.Errorf("oreg[5] = 0x%02X, want 0x02", s.oreg[5])
	}
	// SR: bit7=1, PDL=1, NPE=0, P2MD=01, P1MD=01 = 0xC5
	if s.sr != 0xC5 {
		t.Errorf("sr = 0x%02X, want 0xC5", s.sr)
	}
}

func TestSMPCINTBACKPeripheralRaisesInterrupt(t *testing.T) {
	scu := NewSCU()
	s := NewSMPC(scu)
	smpcINTBACKPeripheral(s, 0, 0)
	if scu.ist&(1<<7) == 0 {
		t.Error("peripheral-only INTBACK did not raise System Manager interrupt")
	}
}

func TestSMPCINTBACKPeripheralClearsActive(t *testing.T) {
	s := NewSMPC(NewSCU())

	smpcINTBACKPeripheral(s, 0, 0)
	if s.intbackActive {
		t.Error("intbackActive should be false after peripheral response (NPE=0)")
	}
}

func TestSMPCINTBACKSysPeripheralSR(t *testing.T) {
	// System data + peripheral (IREG0=0x01, PEN=1): SR=0x60 (PDE=1)
	s := NewSMPC(NewSCU())

	smpcINTBACKSysPeripheral(s, 0, 0)

	if s.sr != 0x60 {
		t.Errorf("sr = 0x%02X, want 0x60", s.sr)
	}
	if !s.intbackActive {
		t.Error("intbackActive should be true after system+peripheral INTBACK")
	}
	// System data should be in OREGs
	if s.oreg[0] != 0xC0 { // STE=1 (RTC initialized=true), RESD=1 (reset disabled)
		t.Errorf("oreg[0] = 0x%02X, want 0xC0", s.oreg[0])
	}
}

func TestSMPCINTBACKSysPeripheralContinue(t *testing.T) {
	t.Skip("SMPC INTBACK under development")
	// System data response, then continue to get peripheral data
	scu := NewSCU()
	s := NewSMPC(scu)
	smpcINTBACKSysPeripheral(s, 0, 0)

	if scu.ist&(1<<7) == 0 {
		t.Error("System Manager interrupt should be raised after system data")
	}
	if s.sr != 0x60 {
		t.Errorf("sr after system data = 0x%02X, want 0x60", s.sr)
	}

	// Clear IST bit 7 to detect the next raise
	scu.ist &^= 1 << 7

	// SH-2 toggles IREG0 bit7 (0->0x80) and sets SF=1 to continue
	s.Write(0x01, 0x80) // IREG0 bit7 toggled
	s.Write(0x63, 0x01) // SF=1

	if scu.ist&(1<<7) == 0 {
		t.Error("System Manager interrupt should be raised after continue")
	}
	// Peripheral data SR: bit7=1, PDL=1, NPE=0, P2MD=00, P1MD=00
	if s.sr != 0xC0 {
		t.Errorf("sr after peripheral = 0x%02X, want 0xC0", s.sr)
	}
	if s.sf != 0 {
		t.Errorf("sf after continue = %d, want 0", s.sf)
	}
	if s.intbackActive {
		t.Error("intbackActive should be false after peripheral response")
	}
	// OREG0-7 should have peripheral data for both ports
	if s.oreg[0] != 0xF1 {
		t.Errorf("oreg[0] = 0x%02X, want 0xF1", s.oreg[0])
	}
	if s.oreg[1] != 0x02 {
		t.Errorf("oreg[1] = 0x%02X, want 0x02", s.oreg[1])
	}
}

func TestSMPCINTBACKSysPeripheralOREG31(t *testing.T) {
	// OREG31 retains 0x10 through both system and peripheral responses
	s := NewSMPC(NewSCU())

	smpcINTBACKSysPeripheral(s, 0, 0)

	if s.oreg[31] != 0x10 {
		t.Errorf("oreg[31] after system data = 0x%02X, want 0x10", s.oreg[31])
	}

	// Continue
	s.Write(0x01, 0x80)
	s.Write(0x63, 0x01)

	if s.oreg[31] != 0x10 {
		t.Errorf("oreg[31] after peripheral = 0x%02X, want 0x10", s.oreg[31])
	}
}

func TestSMPCINTBACKBreak(t *testing.T) {
	t.Skip("SMPC INTBACK under development")
	// Break (IREG0 bit6=1, SF=1) terminates INTBACK
	s := NewSMPC(NewSCU())

	smpcINTBACKSysPeripheral(s, 0, 0)

	if !s.intbackActive {
		t.Error("intbackActive should be true before break")
	}

	// Break: IREG0 bit6=1
	s.Write(0x01, 0x40) // bit6=1 (break)
	s.Write(0x63, 0x01) // SF=1

	if s.intbackActive {
		t.Error("intbackActive should be false after break")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 after break", s.sf)
	}
}

func TestSMPCINTBACKHold(t *testing.T) {
	// Hold (IREG0 unchanged, SF=1) does nothing
	s := NewSMPC(NewSCU())

	smpcINTBACKSysPeripheral(s, 0, 0)

	prevSR := s.sr

	// Hold: IREG0=0x00 (bit7 not toggled from initial 0, bit6=0)
	s.Write(0x01, 0x00)
	s.Write(0x63, 0x01) // SF=1

	if !s.intbackActive {
		t.Error("intbackActive should remain true during hold")
	}
	if s.sf != 1 {
		t.Errorf("sf = %d, want 1 during hold", s.sf)
	}
	// SR unchanged
	if s.sr != prevSR {
		t.Errorf("sr = 0x%02X, want 0x%02X (unchanged during hold)", s.sr, prevSR)
	}
}

func TestSMPCINTBACKContinueWithoutActive(t *testing.T) {
	// SF write when intbackActive=false should not trigger continue logic
	scu := NewSCU()
	s := NewSMPC(scu)

	// Write SF=1 without intbackActive
	s.Write(0x01, 0x80) // would be continue toggle
	s.Write(0x63, 0x01) // SF=1

	if scu.ist&(1<<7) != 0 {
		t.Error("interrupt raised without intbackActive")
	}
}

func TestSMPCINTBACKNoOpIREG0ZeroPENZero(t *testing.T) {
	// IREG0=0x00, PEN=0: no-op
	scu := NewSCU()
	s := NewSMPC(scu)

	// Pre-fill OREGs
	for i := range s.oreg {
		s.oreg[i] = 0xBB
	}
	prevSR := s.sr

	s.Write(0x01, 0x00) // IREG0 = 0x00
	s.Write(0x03, 0x00) // IREG1: PEN=0
	s.sf = 1
	smpcDispatch(s, 0x10) // INTBACK

	// No interrupt raised
	if scu.ist&(1<<7) != 0 {
		t.Error("interrupt raised for no-op INTBACK")
	}
	// OREGs 0-15 untouched (oreg[31] gets command code from dispatch)
	for i := 0; i < 16; i++ {
		if s.oreg[i] != 0xBB {
			t.Errorf("oreg[%d] = 0x%02X, want 0xBB (untouched)", i, s.oreg[i])
		}
	}
	// SR unchanged
	if s.sr != prevSR {
		t.Errorf("sr = 0x%02X, want 0x%02X (unchanged for no-op)", s.sr, prevSR)
	}
}

func TestSMPCNewIntbackActiveDefault(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.intbackActive {
		t.Error("intbackActive should be false after NewSMPC")
	}
}

// --- Phase 9.4: SMPC Controller Input ---

func TestSMPCPadStateDefault(t *testing.T) {
	s := NewSMPC(NewSCU())
	// Default pad state should be all released (0xFFFF)
	if s.padState[0] != 0xFFFF {
		t.Errorf("padState[0] = 0x%04X, want 0xFFFF", s.padState[0])
	}
	if s.padState[1] != 0xFFFF {
		t.Errorf("padState[1] = 0x%04X, want 0xFFFF", s.padState[1])
	}
}

func TestSMPCSetPadData(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.SetPadData(0, 0xFBFF) // A pressed (byte1 bit2 = 0)
	if s.padState[0] != 0xFBFF {
		t.Errorf("padState[0] = 0x%04X, want 0xFBFF", s.padState[0])
	}
	s.SetPadData(1, 0xFF7F) // R pressed (byte2 bit7 = 0)
	if s.padState[1] != 0xFF7F {
		t.Errorf("padState[1] = 0x%04X, want 0xFF7F", s.padState[1])
	}
}

func TestSMPCSetPadDataOutOfRange(t *testing.T) {
	s := NewSMPC(NewSCU())
	// Out of range port should not panic
	s.SetPadData(-1, 0x0000)
	s.SetPadData(2, 0x0000)
	// Pad state unchanged
	if s.padState[0] != 0xFFFF {
		t.Errorf("padState[0] = 0x%04X, want 0xFFFF", s.padState[0])
	}
}

func TestSMPCPeripheralDataWithButtonPress(t *testing.T) {
	s := NewSMPC(NewSCU())

	// Press A on port 1: byte1 bit2 = 0 -> 0xFBFF
	s.SetPadData(0, 0xFBFF)

	smpcINTBACKPeripheral(s, 0, 0)

	// Port 1 button data
	if s.oreg[2] != 0xFB {
		t.Errorf("oreg[2] = 0x%02X, want 0xFB (A pressed)", s.oreg[2])
	}
	if s.oreg[3] != 0xFF {
		t.Errorf("oreg[3] = 0x%02X, want 0xFF", s.oreg[3])
	}
	// Port 2 should still be all released
	if s.oreg[6] != 0xFF {
		t.Errorf("oreg[6] = 0x%02X, want 0xFF", s.oreg[6])
	}
}

func TestSMPCPeripheralDataMultipleButtons(t *testing.T) {
	s := NewSMPC(NewSCU())

	// Press Up (byte1 bit4=0), Start (byte1 bit3=0), L (byte2 bit3=0)
	// byte1 = 0xFF & ~(1<<4) & ~(1<<3) = 0xFF ^ 0x18 = 0xE7
	// byte2 = 0xFF & ~(1<<3) = 0xF7
	s.SetPadData(0, 0xE7F7)

	smpcINTBACKPeripheral(s, 0, 0)

	if s.oreg[2] != 0xE7 {
		t.Errorf("oreg[2] = 0x%02X, want 0xE7", s.oreg[2])
	}
	if s.oreg[3] != 0xF7 {
		t.Errorf("oreg[3] = 0x%02X, want 0xF7", s.oreg[3])
	}
}

func TestSMPCNewPadStateReleased(t *testing.T) {
	s := NewSMPC(NewSCU())
	if s.padState[0] != 0xFFFF {
		t.Errorf("padState[0] after NewSMPC = 0x%04X, want 0xFFFF", s.padState[0])
	}
	if s.padState[1] != 0xFFFF {
		t.Errorf("padState[1] after NewSMPC = 0x%04X, want 0xFFFF", s.padState[1])
	}
}

// --- Pass 2 gap-fill tests ---

func TestSMPCTickFrameAdvancesRTC(t *testing.T) {
	s := NewSMPC(NewSCU())
	before := s.rtcFrames
	s.TickFrame()
	s.TickFrame()
	s.TickFrame()
	if s.rtcFrames != before+3 {
		t.Errorf("rtcFrames = %d, want %d after 3 TickFrame calls", s.rtcFrames, before+3)
	}
}

func TestSMPCMSHONDispatchNoOp(t *testing.T) {
	s := NewSMPC(NewSCU())
	// Drive MSHON (command 0x00) through the full dispatch path so
	// the cmdMSHON stub is executed from its call site.
	before := *s
	before.sf = 0 // dispatch clears SF
	s.Write(0x1F, 0x00)
	s.cmdDelay = 1
	s.TickScanline()
	if s.sshEnabled != before.sshEnabled ||
		s.soundEnabled != before.soundEnabled ||
		s.cdEnabled != before.cdEnabled ||
		s.dotsel != before.dotsel {
		t.Error("MSHON changed state; should be a no-op")
	}
	if s.sf != 0 {
		t.Errorf("sf after MSHON = %d, want 0", s.sf)
	}
}

func TestSMPCNMIREQDispatchNoOp(t *testing.T) {
	s := NewSMPC(NewSCU())
	nmiCalled := false
	s.masterNMI = func() { nmiCalled = true }
	before := *s
	before.sf = 0
	s.Write(0x1F, 0x18)
	s.cmdDelay = 1
	s.TickScanline()
	if s.sshEnabled != before.sshEnabled ||
		s.soundEnabled != before.soundEnabled ||
		s.cdEnabled != before.cdEnabled {
		t.Error("NMIREQ changed SSH/sound/CD state; the stub itself should be a no-op")
	}
	if !nmiCalled {
		t.Error("NMIREQ dispatch did not invoke masterNMI callback")
	}
}

func TestSMPCReadPDRSMPCMode(t *testing.T) {
	s := NewSMPC(NewSCU())
	// IOSEL port 0 = 0 -> SMPC polling mode; returns written pdr & 0x7F.
	s.iosel = 0
	s.pdr1 = 0xAA
	if got := s.readPDR(0); got != (0xAA & 0x7F) {
		t.Errorf("readPDR(0) SMPC mode = 0x%02X, want 0x%02X", got, 0xAA&0x7F)
	}
	// Port 1.
	s.pdr2 = 0x55
	if got := s.readPDR(1); got != 0x55 {
		t.Errorf("readPDR(1) SMPC mode = 0x%02X, want 0x55", got)
	}
}

func TestSMPCReadPDRDirectIOAllTHTR(t *testing.T) {
	s := NewSMPC(NewSCU())
	// Direct I/O mode on port 0; DDR configures TH/TR as outputs
	// (bits 5 and 6 set) so the written values drive the select lines.
	s.iosel = 0x01
	s.ddr1 = 0x60 // TH (bit 6) and TR (bit 5) are outputs
	// Controller state (active-low): all bits 1 = no buttons pressed.
	s.padState[0] = 0xFFFF

	// Sweep TH:TR combinations and verify TL (bit 4) is always high.
	for _, thtr := range []uint8{0x00, 0x20, 0x40, 0x60} {
		s.pdr1 = thtr
		got := s.readPDR(0)
		if got&0x10 == 0 {
			t.Errorf("TH:TR=0x%02X: TL bit not set in 0x%02X", thtr, got)
		}
	}
}

func TestSMPCReadPDRDirectIOButtonData(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.iosel = 0x01
	s.ddr1 = 0x60

	// Press Start (byte1 bit 3 -> active-low). padState[0] high byte
	// bits: [Right, Left, Down, Up, Start, A, C, B].
	// Start pressed = bit 3 cleared.
	s.padState[0] = (0xF7 << 8) | 0xFF

	// TH=1, TR=0 -> byte1 lower nibble.
	s.pdr1 = 0x40
	got := s.readPDR(0)
	// bit 3 (Start) should reflect active-low press: result bit 3 = 0.
	if got&0x08 != 0 {
		t.Errorf("Start pressed with TH=1,TR=0: got 0x%02X, expected bit 3 clear", got)
	}
}

func TestSMPCCmdScanlinesUnknownCommand(t *testing.T) {
	// comreg outside the 0x00-0x1F command table falls through to the
	// default 30us delay.
	s := NewSMPC(NewSCU())
	s.comreg = 0xFF
	lines := s.cmdScanlines()
	if lines < 1 {
		t.Errorf("cmdScanlines unknown cmd returned %d, want >= 1", lines)
	}
}

func TestSMPCContinueINTBACKBreak(t *testing.T) {
	s := NewSMPC(NewSCU())
	s.intbackActive = true
	s.sr = 0x60
	// IREG0 bit 6 = break.
	s.ireg[0] = 0x40
	s.continueINTBACK()
	if s.intbackActive {
		t.Error("intbackActive not cleared on break")
	}
	if s.sf != 0 {
		t.Errorf("sf = %d, want 0 on break", s.sf)
	}
}

func TestSMPCContinueINTBACKNoFlags(t *testing.T) {
	// Hit the early-return path: IREG[0] has neither break (bit 6)
	// nor continue (bit 7). The function should return without
	// touching sr or intbackActive.
	s := NewSMPC(NewSCU())
	s.intbackActive = true
	s.sr = 0xAA
	s.ireg[0] = 0x01 // no bit 6 or bit 7

	s.continueINTBACK()

	if !s.intbackActive {
		t.Error("intbackActive cleared on neutral IREG0 write")
	}
	if s.sr != 0xAA {
		t.Errorf("sr changed on neutral IREG0 write: 0x%02X, want 0xAA", s.sr)
	}
}
