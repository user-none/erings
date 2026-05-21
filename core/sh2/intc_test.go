// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestINTCInitValues(t *testing.T) {
	var intc INTC
	intc.Reset()

	if intc.ipra != 0 {
		t.Errorf("IPRA = 0x%04X, want 0x0000", intc.ipra)
	}
	if intc.iprb != 0 {
		t.Errorf("IPRB = 0x%04X, want 0x0000", intc.iprb)
	}
	// ICR initial value is H'8000 per manual Sec 5.3.8 when NMI pin is
	// high. Saturn's NMI pin is pulled high at boot.
	if intc.icr != 0x8000 {
		t.Errorf("ICR = 0x%04X, want 0x8000", intc.icr)
	}
}

// TestINTCRegisterReadWrite exercises writes using values that only
// touch the writable bits for each register. Reserved-bit masking
// is covered in TestINTCReservedBitsMasked below.
func TestINTCRegisterReadWrite(t *testing.T) {
	var intc INTC
	intc.Reset()

	tests := []struct {
		name string
		addr uint32
		val  uint16
	}{
		{"IPRB", 0xFFFFFE60, 0x0F00},   // SCIIP=0, FRTIP=0xF, reserved low=0
		{"VCRA", 0xFFFFFE62, 0x4320},   // bits 15 and 7 clear
		{"VCRB", 0xFFFFFE64, 0x4544},   // bits 15 and 7 clear
		{"VCRC", 0xFFFFFE66, 0x1234},   // bits 15 and 7 clear
		{"VCRD", 0xFFFFFE68, 0x5600},   // bits 15 and 7-0 clear
		{"IPRA", 0xFFFFFEE2, 0xA500},   // reserved low nibble = 0
		{"VCRWDT", 0xFFFFFEE4, 0x1A3C}, // bits 15 and 7 clear
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intc.Write(tt.addr, tt.val)
			got := intc.Read(tt.addr)
			if got != tt.val {
				t.Errorf("Read(0x%08X) = 0x%04X, want 0x%04X", tt.addr, got, tt.val)
			}
		})
	}
}

// TestINTCReservedBitsMasked verifies that writes to INTC registers
// mask out reserved bits per manual Sec 5.3. Writing 0xFFFF to each
// register must read back only the writable bits.
func TestINTCReservedBitsMasked(t *testing.T) {
	tests := []struct {
		name string
		addr uint32
		mask uint16
	}{
		{"IPRB", 0xFFFFFE60, 0xFF00},
		{"VCRA", 0xFFFFFE62, 0x7F7F},
		{"VCRB", 0xFFFFFE64, 0x7F7F},
		{"VCRC", 0xFFFFFE66, 0x7F7F},
		{"VCRD", 0xFFFFFE68, 0x7F00},
		{"IPRA", 0xFFFFFEE2, 0xFFF0},
		{"VCRWDT", 0xFFFFFEE4, 0x7F7F},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var intc INTC
			intc.Reset()
			intc.Write(tt.addr, 0xFFFF)
			got := intc.Read(tt.addr)
			if got != tt.mask {
				t.Errorf("%s read-back = 0x%04X, want 0x%04X (mask)", tt.name, got, tt.mask)
			}
		})
	}
}

func TestINTCICRWriteMask(t *testing.T) {
	var intc INTC
	intc.Reset()

	// ICR bit 15 (NMIL) is read-only and preserved from init
	// (H'8000); bits 8 (NMIE) and 0 (VECMD) are the only writable
	// bits.
	intc.Write(0xFFFFFEE0, 0xFFFF)
	got := intc.Read(0xFFFFFEE0)
	if got != 0x8101 {
		t.Errorf("ICR after write 0xFFFF = 0x%04X, want 0x8101", got)
	}
}

func TestINTCPriorityLookup(t *testing.T) {
	var intc INTC
	intc.Reset()

	// Set FRT priority to 5 in IPRB bits 11-8
	intc.Write(0xFFFFFE60, 0x0500)
	if got := intc.frtPriority(); got != 5 {
		t.Errorf("frtPriority() = %d, want 5", got)
	}

	// Set DIVU priority to 10 in IPRA bits 15-12
	intc.Write(0xFFFFFEE2, 0xA000)
	if got := intc.divuPriority(); got != 10 {
		t.Errorf("divuPriority() = %d, want 10", got)
	}
}

func TestINTCVectorLookup(t *testing.T) {
	var intc INTC
	intc.Reset()

	// VCRC: bits 14-8 = FRT ICI vector, bits 6-0 = FRT OCI vector
	intc.Write(0xFFFFFE66, 0x4320) // ICI=0x43, OCI=0x20
	if got := intc.frtICIVector(); got != 0x43 {
		t.Errorf("frtICIVector() = 0x%02X, want 0x43", got)
	}
	if got := intc.frtOCIVector(); got != 0x20 {
		t.Errorf("frtOCIVector() = 0x%02X, want 0x20", got)
	}

	// VCRD: bits 14-8 = FRT OVI vector, bits 6-0 = reserved
	intc.Write(0xFFFFFE68, 0x4400) // OVI=0x44
	if got := intc.frtOVIVector(); got != 0x44 {
		t.Errorf("frtOVIVector() = 0x%02X, want 0x44", got)
	}
}

func TestINTCUnmappedAddress(t *testing.T) {
	var intc INTC
	intc.Reset()

	// Reading an unmapped address should return 0
	got := intc.Read(0xFFFFFE70)
	if got != 0 {
		t.Errorf("Read(unmapped) = 0x%04X, want 0", got)
	}
}

// TestINTCByteAccessRegisters exercises 8-bit read/write round-trips for
// each 16-bit INTC register exposed through the CPU's on-chip dispatch.
// Manual Table 5.2 lists these registers as 8/16 accessible. The CPU
// routes byte writes to writeOnChip8 which uses a read-modify-write to
// preserve the opposite half.
func TestINTCByteAccessRegisters(t *testing.T) {
	tests := []struct {
		name     string
		addr     uint32 // word-aligned base
		hi, lo   uint8  // bytes to write into the high/low halves
		wantWord uint16 // expected word readback after writes are masked
	}{
		// IPRA bits 3-0 reserved -> low byte masked to 0.
		{"IPRA", 0xFFFFFEE2, 0xA5, 0x5A, 0xA550},
		// IPRB bits 7-0 reserved -> low byte always 0.
		{"IPRB", 0xFFFFFE60, 0x3C, 0xFF, 0x3C00},
		// VCRA/VCRB/VCRC: bits 15 and 7 reserved -> 0x7F7F mask.
		{"VCRA", 0xFFFFFE62, 0xFF, 0xFF, 0x7F7F},
		{"VCRB", 0xFFFFFE64, 0xC3, 0x81, 0x4301},
		{"VCRC", 0xFFFFFE66, 0xAA, 0x55, 0x2A55},
		// VCRD: bit 15 and bits 7-0 reserved -> 0x7F00 mask.
		{"VCRD", 0xFFFFFE68, 0xFF, 0xFF, 0x7F00},
		// VCRWDT: bits 15 and 7 reserved -> 0x7F7F mask.
		{"VCRWDT", 0xFFFFFEE4, 0xF0, 0x0F, 0x700F},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := newTestBus(0x1000)
			cpu := New(bus, true)

			cpu.write8(tt.addr, tt.hi)
			cpu.write8(tt.addr+1, tt.lo)

			gotHi := cpu.read8(tt.addr)
			gotLo := cpu.read8(tt.addr + 1)
			gotWord := uint16(gotHi)<<8 | uint16(gotLo)
			if gotWord != tt.wantWord {
				t.Errorf("byte-access word = 0x%04X, want 0x%04X", gotWord, tt.wantWord)
			}
		})
	}
}

// TestINTCByteAccessICR exercises ICR byte access. Per manual Sec 5.3.8
// only bit 8 (NMIE) in the high half and bit 0 (VECMD) in the low half
// are R/W. Bit 15 (NMIL) is R/O and seeded from the NMI pin. Byte
// writes must not corrupt the NMIL bit or the untouched half.
func TestINTCByteAccessICR(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)

	// Reset leaves NMIL=1 (Saturn boot pulls NMI high).
	if cpu.intc.Read(0xFFFFFEE0)&0x8000 == 0 {
		t.Fatalf("ICR initial NMIL = 0, want 1 (Reset() default)")
	}

	// Write NMIE via high byte. Only bit 0 of the high byte (bit 8
	// of the word) is writable, per Sec 5.3.8. NMIL (bit 15) is R/O.
	cpu.write8(0xFFFFFEE0, 0xFF)
	hi := cpu.read8(0xFFFFFEE0)
	if hi&0x01 == 0 {
		t.Errorf("ICR high byte after write 0xFF: bit 0 (NMIE) = 0, want 1")
	}
	if hi&0x80 == 0 {
		t.Errorf("ICR high byte after write 0xFF: NMIL cleared; should persist from reset")
	}

	// Write VECMD via low byte. Only bit 0 is writable.
	cpu.write8(0xFFFFFEE1, 0xFF)
	lo := cpu.read8(0xFFFFFEE1)
	if lo&0x01 == 0 {
		t.Errorf("ICR low byte after write 0xFF: VECMD = 0, want 1")
	}
	if lo&0xFE != 0 {
		t.Errorf("ICR low byte = 0x%02X, want reserved bits 7-1 to read 0", lo)
	}

	// Writing only the low half must not disturb the high half.
	cpu.write8(0xFFFFFEE0, 0x01) // NMIE=1
	cpu.write8(0xFFFFFEE1, 0x00) // clear VECMD
	got := cpu.intc.Read(0xFFFFFEE0)
	if got&0x0100 == 0 {
		t.Errorf("ICR = 0x%04X; NMIE cleared by low-byte write, want preserved", got)
	}
	if got&0x01 != 0 {
		t.Errorf("ICR = 0x%04X; VECMD not cleared", got)
	}
}

// TestINTCICRNMILReadOnly verifies that NMIL tracks only SetNMIL calls
// (i.e., the NMI input pin) and is never writable by software. Sec 5.3.8.
func TestINTCICRNMILReadOnly(t *testing.T) {
	var intc INTC
	intc.Reset()

	intc.SetNMIL(true)
	intc.Write(0xFFFFFEE0, 0x0000)
	if intc.Read(0xFFFFFEE0)&0x8000 == 0 {
		t.Error("NMIL cleared by software write; should be R/O (Sec 5.3.8)")
	}

	intc.SetNMIL(false)
	if intc.Read(0xFFFFFEE0)&0x8000 != 0 {
		t.Error("NMIL not cleared after SetNMIL(false)")
	}

	// Software writes can't re-set NMIL either.
	intc.Write(0xFFFFFEE0, 0xFFFF)
	if intc.Read(0xFFFFFEE0)&0x8000 != 0 {
		t.Error("NMIL set by software write 0xFFFF; should be R/O")
	}
}

// TestINTCICRNMIERoundTrip exercises NMIE (Sec 5.3.8 bit 8: NMI edge
// select). Bit is R/W.
func TestINTCICRNMIERoundTrip(t *testing.T) {
	var intc INTC
	intc.Reset()

	intc.Write(0xFFFFFEE0, 0x0100)
	if intc.Read(0xFFFFFEE0)&0x0100 == 0 {
		t.Error("NMIE not set after write")
	}
	intc.Write(0xFFFFFEE0, 0x0000)
	if intc.Read(0xFFFFFEE0)&0x0100 != 0 {
		t.Error("NMIE not cleared after write")
	}
}

// TestINTCICRVECMDRoundTrip exercises VECMD (Sec 5.3.8 bit 0: IRL
// interrupt vector mode select). Bit is R/W.
func TestINTCICRVECMDRoundTrip(t *testing.T) {
	var intc INTC
	intc.Reset()

	intc.Write(0xFFFFFEE0, 0x0001)
	if intc.Read(0xFFFFFEE0)&0x0001 == 0 {
		t.Error("VECMD not set after write")
	}
	intc.Write(0xFFFFFEE0, 0x0000)
	if intc.Read(0xFFFFFEE0)&0x0001 != 0 {
		t.Error("VECMD not cleared after write")
	}
}

// TestINTCResetClearsPending verifies Reset() scrubs both the register
// file (Sec 5.3 initial values) and the internal pending bitmask, so
// that stale asserts from prior operation cannot survive a reset.
func TestINTCResetClearsPending(t *testing.T) {
	var intc INTC
	intc.Reset()

	intc.AssertSource(isrcDIVU)
	intc.AssertSource(isrcFRT)
	intc.AssertSource(isrcDMAC0)
	if intc.pending == 0 {
		t.Fatal("pending did not latch asserts")
	}

	// Touch a couple of registers too so Reset must visibly scrub them.
	intc.Write(0xFFFFFEE2, 0xA500) // IPRA
	intc.Write(0xFFFFFE60, 0x0F00) // IPRB

	intc.Reset()

	if intc.pending != 0 {
		t.Errorf("pending = 0x%04X after Reset, want 0", intc.pending)
	}
	if intc.ipra != 0 {
		t.Errorf("IPRA = 0x%04X after Reset, want 0", intc.ipra)
	}
	if intc.iprb != 0 {
		t.Errorf("IPRB = 0x%04X after Reset, want 0", intc.iprb)
	}
}
