// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// TestCartIDForProduct verifies override lookup: listed product numbers
// get their override ID, everything else the 4MB default.
func TestCartIDForProduct(t *testing.T) {
	if got := ramCartIDForProduct("T-16103H"); got != ramCartIDNone {
		t.Errorf("T-16103H cart ID = 0x%02X, want 0x%02X (none)", got, ramCartIDNone)
	}
	if got := ramCartIDForProduct("MK-81304"); got != ramCartID4MB {
		t.Errorf("unlisted product cart ID = 0x%02X, want 0x%02X (4MB)", got, ramCartID4MB)
	}
	if got := ramCartIDForProduct(""); got != ramCartID4MB {
		t.Errorf("empty product cart ID = 0x%02X, want 0x%02X (4MB)", got, ramCartID4MB)
	}
}

// TestApplyCartOverride verifies the disc product number drives the bus
// cartridge ID at disc load.
func TestApplyCartOverride(t *testing.T) {
	e := NewEmulator()

	ip := make([]byte, 0x100)
	copy(ip[0x20:], "T-16103H  ")
	e.ipImage = ip
	e.applyRAMCartOverride()
	if e.bus.ramCartID != ramCartIDNone {
		t.Errorf("ramCartID for T-16103H = 0x%02X, want 0x%02X (none)", e.bus.ramCartID, ramCartIDNone)
	}

	copy(ip[0x20:], "T-99901G  ")
	e.applyRAMCartOverride()
	if e.bus.ramCartID != ramCartID4MB {
		t.Errorf("ramCartID for unlisted product = 0x%02X, want 0x%02X (4MB)", e.bus.ramCartID, ramCartID4MB)
	}
}

// TestCartNoneOpenBus verifies an empty slot: the ID byte and the
// extended RAM window read open bus, and a write-then-read probe of the
// window fails (writes land in the backing array but reads stay gated).
func TestCartNoneOpenBus(t *testing.T) {
	bus := newBusForTest()
	bus.ramCartID = ramCartIDNone

	if got := bus.Read8(cartIDAddr); got != 0xFF {
		t.Errorf("cart ID byte = 0x%02X, want 0xFF", got)
	}
	if got := bus.Read16(cartIDAddr &^ 1); got != 0xFFFF {
		t.Errorf("cart ID word = 0x%04X, want 0xFFFF", got)
	}
	if got := bus.Read32(cartIDAddr &^ 3); got != 0xFFFFFFFF {
		t.Errorf("cart ID long = 0x%08X, want 0xFFFFFFFF", got)
	}

	bus.Write8(extRAMBase, 0xAA)
	bus.Write16(extRAMBase+2, 0xBBCC)
	bus.Write32(extRAMBase+4, 0x11223344)
	if got := bus.Read8(extRAMBase); got != 0xFF {
		t.Errorf("extRAM read8 with no cart = 0x%02X, want 0xFF", got)
	}
	if got := bus.Read16(extRAMBase + 2); got != 0xFFFF {
		t.Errorf("extRAM read16 with no cart = 0x%04X, want 0xFFFF", got)
	}
	if got := bus.Read32(extRAMBase + 4); got != 0xFFFFFFFF {
		t.Errorf("extRAM read32 with no cart = 0x%08X, want 0xFFFFFFFF", got)
	}
}
