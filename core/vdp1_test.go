// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// readWriteOnlyReg fetches the most-recently-written value of a VDP1
// write-only register for test assertions. Mirrors what a hypothetical
// hardware introspection path would return: the shadow value for
// deferred registers, the active value for the immediate ones.
func readWriteOnlyReg(v *VDP1, offset uint32) uint16 {
	switch offset {
	case 0x00:
		return v.tvmr
	case 0x02:
		return (v.fbcr & 0x03) | (v.fbcrPending &^ 0x03)
	case 0x04:
		return v.ptmrPending
	case 0x06:
		return v.ewdrPending
	case 0x08:
		return v.ewlrPending
	case 0x0A:
		return v.ewrrPending
	}
	return 0
}

func TestVDP1Defaults(t *testing.T) {
	v := NewVDP1(NewSCU())

	// EDSR defaults to 0x0003 (drawing idle)
	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("EDSR default = 0x%04X, want 0x0003", got)
	}

	// PTMR defaults to 0
	if got := readWriteOnlyReg(v, 0x04); got != 0 {
		t.Errorf("PTMR default = 0x%04X, want 0x0000", got)
	}

	// All write-only registers default to 0
	for _, off := range []uint32{0x00, 0x02, 0x04, 0x06, 0x08, 0x0A} {
		if got := readWriteOnlyReg(v, off); got != 0 {
			t.Errorf("register 0x%02X default = 0x%04X, want 0x0000", off, got)
		}
	}

	// LOPR and COPR default to 0
	if got := v.Read(0x12); got != 0 {
		t.Errorf("LOPR default = 0x%04X, want 0x0000", got)
	}
	if got := v.Read(0x14); got != 0 {
		t.Errorf("COPR default = 0x%04X, want 0x0000", got)
	}
}

func TestVDP1TVMRViaMODR(t *testing.T) {
	v := NewVDP1(NewSCU())

	// Write TVMR with TVM=5, VBE=1 (bit 3)
	v.Write(0x00, 0x000D) // bits 3,2,0 = VBE + TVM
	modr := v.Read(0x16)

	// TVM bits 2-0 should be 5
	if got := modr & 0x07; got != 0x05 {
		t.Errorf("MODR TVM = 0x%X, want 0x5", got)
	}
	// VBE bit 3 should be set
	if modr&0x08 == 0 {
		t.Error("MODR VBE bit should be set")
	}
}

func TestVDP1FBCRViaMODR(t *testing.T) {
	v := NewVDP1(NewSCU())

	// Write FBCR with EOS=1 (bit 4), DIE=1 (bit 3), DIL=1 (bit 2), FCM=1 (bit 1)
	v.Write(0x02, 0x001E)
	modr := v.Read(0x16)

	if modr&0x0080 == 0 {
		t.Error("MODR EOS (bit 7) should be set")
	}
	if modr&0x0040 == 0 {
		t.Error("MODR DIE (bit 6) should be set")
	}
	if modr&0x0020 == 0 {
		t.Error("MODR DIL (bit 5) should be set")
	}
	if modr&0x0010 == 0 {
		t.Error("MODR FCM (bit 4) should be set")
	}
}

func TestVDP1PTMRViaMODR(t *testing.T) {
	v := NewVDP1(NewSCU())

	// Write PTMR with PTM=2 (bit 1 set)
	v.Write(0x04, 0x0002)
	modr := v.Read(0x16)

	// PTM1 should appear at bit 8
	if modr&0x0100 == 0 {
		t.Error("MODR PTM1 (bit 8) should be set")
	}

	// PTM=1 (bit 0 only) should NOT set bit 8
	v.Write(0x04, 0x0001)
	modr = v.Read(0x16)
	if modr&0x0100 != 0 {
		t.Error("MODR PTM1 (bit 8) should not be set for PTM=1")
	}
}

func TestVDP1PTMRMask(t *testing.T) {
	v := NewVDP1(NewSCU())

	// PTMR should only keep bits 1-0. The masked value lands in the
	// shadow register since PTM=11 is not the immediate-trigger code.
	v.Write(0x04, 0xFFFF)
	if got := v.ptmrPending; got != 0x03 {
		t.Errorf("ptmrPending after 0xFFFF = 0x%04X, want 0x0003", got)
	}
}

func TestVDP1WriteOnlyReturnZero(t *testing.T) {
	v := NewVDP1(NewSCU())

	writeOnlyRegs := []uint32{0x00, 0x02, 0x04, 0x06, 0x08, 0x0A, 0x0C}
	for _, off := range writeOnlyRegs {
		v.Write(off, 0xFFFF)
		if got := v.Read(off); got != 0 {
			t.Errorf("Read(0x%02X) = 0x%04X, want 0x0000 (write-only)", off, got)
		}
	}
}

func TestVDP1ReadOnlyIgnoreWrites(t *testing.T) {
	v := NewVDP1(NewSCU())

	// EDSR starts at 0x0003
	v.Write(0x10, 0x0000) // attempt to write read-only
	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("EDSR after write = 0x%04X, want 0x0003 (unchanged)", got)
	}

	// LOPR starts at 0
	v.Write(0x12, 0xFFFF)
	if got := v.Read(0x12); got != 0 {
		t.Errorf("LOPR after write = 0x%04X, want 0x0000 (unchanged)", got)
	}

	// COPR starts at 0
	v.Write(0x14, 0xFFFF)
	if got := v.Read(0x14); got != 0 {
		t.Errorf("COPR after write = 0x%04X, want 0x0000 (unchanged)", got)
	}

	// MODR - write should be ignored
	v.Write(0x16, 0xFFFF)
	// MODR should still be version 1 with all zeros
	if got := v.Read(0x16); got != 0x1000 {
		t.Errorf("MODR after write = 0x%04X, want 0x1000 (unchanged)", got)
	}
}

func TestVDP1MODRVersion(t *testing.T) {
	v := NewVDP1(NewSCU())

	modr := v.Read(0x16)
	// Version should be 1 (bits 15-12)
	if got := (modr >> 12) & 0x0F; got != 1 {
		t.Errorf("MODR version = %d, want 1", got)
	}
}

func TestVDP1MODRConstruction(t *testing.T) {
	v := NewVDP1(NewSCU())

	// Set all fields
	v.Write(0x00, 0x000F) // TVMR: VBE=1, TVM=7
	v.Write(0x02, 0x001E) // FBCR: EOS=1, DIE=1, DIL=1, FCM=1
	v.Write(0x04, 0x0002) // PTMR: PTM=2

	modr := v.Read(0x16)
	want := uint16(0x11FF) // VER=1, PTM1=1, EOS=1, DIE=1, DIL=1, FCM=1, VBE=1, TVM=7
	if modr != want {
		t.Errorf("MODR = 0x%04X, want 0x%04X", modr, want)
	}
}

func TestVDP1EDSR(t *testing.T) {
	v := NewVDP1(NewSCU())

	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("EDSR = 0x%04X, want 0x0003", got)
	}
}

func TestVDP1ENDRAccepted(t *testing.T) {
	v := NewVDP1(NewSCU())
	// ENDR write should not panic
	v.Write(0x0C, 0xFFFF)
}

func TestVDP1VRAMReadWrite(t *testing.T) {
	v := NewVDP1(NewSCU())

	v.WriteVRAM(0x00000, 0xAA)
	v.WriteVRAM(0x7FFFF, 0xBB)

	if got := v.ReadVRAM(0x00000); got != 0xAA {
		t.Errorf("VRAM[0] = 0x%02X, want 0xAA", got)
	}
	if got := v.ReadVRAM(0x7FFFF); got != 0xBB {
		t.Errorf("VRAM[last] = 0x%02X, want 0xBB", got)
	}
}

func TestVDP1FBReadWrite(t *testing.T) {
	v := NewVDP1(NewSCU())

	v.WriteFB(0x00000, 0xCC)
	v.WriteFB(0x3FFFF, 0xDD)

	if got := v.ReadFB(0x00000); got != 0xCC {
		t.Errorf("FB[0] = 0x%02X, want 0xCC", got)
	}
	if got := v.ReadFB(0x3FFFF); got != 0xDD {
		t.Errorf("FB[last] = 0x%02X, want 0xDD", got)
	}
}

func TestVDP1VRAMAddressMasking(t *testing.T) {
	v := NewVDP1(NewSCU())

	v.WriteVRAM(0x00000, 0x42)
	// Address 0x80000 should wrap to 0x00000
	if got := v.ReadVRAM(0x80000); got != 0x42 {
		t.Errorf("VRAM wrap = 0x%02X, want 0x42", got)
	}
}

func TestVDP1FBAddressMasking(t *testing.T) {
	v := NewVDP1(NewSCU())

	v.WriteFB(0x00000, 0x55)
	// Address 0x40000 should wrap to 0x00000
	if got := v.ReadFB(0x40000); got != 0x55 {
		t.Errorf("FB wrap = 0x%02X, want 0x55", got)
	}
}

func TestVDP1NewInitialState(t *testing.T) {
	v := NewVDP1(NewSCU())

	// Write-only registers = 0
	for _, off := range []uint32{0x00, 0x02, 0x04, 0x06, 0x08, 0x0A} {
		if got := readWriteOnlyReg(v, off); got != 0 {
			t.Errorf("register 0x%02X = 0x%04X, want 0x0000", off, got)
		}
	}

	// EDSR = 0x0003 (drawing idle/complete)
	if got := v.Read(0x10); got != 0x0003 {
		t.Errorf("EDSR = 0x%04X, want 0x0003", got)
	}
	// LOPR = 0
	if got := v.Read(0x12); got != 0 {
		t.Errorf("LOPR = 0x%04X, want 0x0000", got)
	}
	// COPR = 0
	if got := v.Read(0x14); got != 0 {
		t.Errorf("COPR = 0x%04X, want 0x0000", got)
	}
	// MODR = VER=1 only
	if got := v.Read(0x16); got != 0x1000 {
		t.Errorf("MODR = 0x%04X, want 0x1000", got)
	}
	// System clip defaults
	if v.sysClipX != 319 {
		t.Errorf("sysClipX = %d, want 319", v.sysClipX)
	}
	if v.sysClipY != 223 {
		t.Errorf("sysClipY = %d, want 223", v.sysClipY)
	}
	// VRAM zeroed
	if got := v.ReadVRAM(0x00000); got != 0 {
		t.Errorf("VRAM[0] = 0x%02X, want 0x00", got)
	}
	if got := v.ReadVRAM(vdp1VRAMSize - 1); got != 0 {
		t.Errorf("VRAM[last] = 0x%02X, want 0x00", got)
	}
	// FB zeroed
	if got := v.ReadFB(0x00000); got != 0 {
		t.Errorf("FB[0] = 0x%02X, want 0x00", got)
	}
	// drawPending = false
	if v.drawPending {
		t.Error("drawPending should be false")
	}
}

func TestTVMHelpers(t *testing.T) {
	scu := &SCU{}
	v := NewVDP1(scu)

	tests := []struct {
		tvm      uint16
		is8bpp   bool
		fbWidth  int
		fbHeight int
	}{
		{0, false, 512, 256}, // Normal 16-bit
		{1, true, 1024, 256}, // Hi-res 8-bit
		{2, false, 512, 256}, // Rotation 16-bit
		{3, true, 512, 512},  // Rotation 8-bit
		{4, false, 512, 256}, // HDTV stub (same as normal)
	}

	for _, tt := range tests {
		v.Write(0x00, tt.tvm)
		if got := v.is8bpp(); got != tt.is8bpp {
			t.Errorf("TVM=%d: is8bpp() = %v, want %v", tt.tvm, got, tt.is8bpp)
		}
		if got := v.fbWidth(); got != tt.fbWidth {
			t.Errorf("TVM=%d: fbWidth() = %d, want %d", tt.tvm, got, tt.fbWidth)
		}
		if got := v.fbHeight(); got != tt.fbHeight {
			t.Errorf("TVM=%d: fbHeight() = %d, want %d", tt.tvm, got, tt.fbHeight)
		}
		// Exported accessors should match
		if got := v.Is8bpp(); got != tt.is8bpp {
			t.Errorf("TVM=%d: Is8bpp() = %v, want %v", tt.tvm, got, tt.is8bpp)
		}
		if got := v.FBWidth(); got != tt.fbWidth {
			t.Errorf("TVM=%d: FBWidth() = %d, want %d", tt.tvm, got, tt.fbWidth)
		}
		if got := v.FBHeight(); got != tt.fbHeight {
			t.Errorf("TVM=%d: FBHeight() = %d, want %d", tt.tvm, got, tt.fbHeight)
		}
	}
}
