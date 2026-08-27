// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// SCU DMA with a sparse write-add to a B-Bus destination writes one
// 16-bit unit per address stop (SCU manual Sec 4.5: the B-Bus write
// unit is 16 bits), so each halfword of a source long word lands at
// its own write-add step. Replicates the Mega Man X4 NBG2 map column
// stream: dst 0x25E78924, count 0x14, read add +4, write add 128,
// where every halfword is one map row's pattern-name word.
func TestSCUDMASparseBBusHalfwordUnits(t *testing.T) {
	half := []uint16{0x007B, 0x107B, 0x207B, 0x307C, 0x407A, 0x507A, 0x607A, 0x7079, 0x807A, 0x904D}

	setup := func(dst uint32) *fakeSCUBus {
		s := NewSCU()
		fb := newFakeSCUBus()
		s.SetBus(fb)
		src := uint32(0x06000000)
		for i := 0; i < len(half); i += 2 {
			fb.mem[src+uint32(i*2)] = uint32(half[i])<<16 | uint32(half[i+1])
		}
		s.Write(0x00, src)   // D0R
		s.Write(0x04, dst)   // D0W
		s.Write(0x08, 0x14)  // D0C
		s.Write(0x0C, 0x107) // D0AD: read +4, write add idx 7 (=128)
		s.Write(0x14, 0x07)  // D0MD: immediate, direct
		s.Write(0x10, 0x01)  // D0EN triggers
		return fb
	}

	t.Run("Aligned", func(t *testing.T) {
		fb := setup(0x25E78924)
		for row, want := range half {
			addr := uint32(0x25E78924) + uint32(row)*128
			if got := fb.Read16(addr); got != want {
				t.Errorf("row %d @%08X = %04X, want %04X", row, addr, got, want)
			}
		}
	})

	// The paired column stream writes the pattern-name low words at
	// dst+2: halfword-aligned but not longword-aligned destinations
	// must land at their own stops as well, not collapse onto the
	// aligned neighbor.
	t.Run("HalfwordAligned", func(t *testing.T) {
		fb := setup(0x25E78926)
		for row, want := range half {
			addr := uint32(0x25E78926) + uint32(row)*128
			if got := fb.Read16(addr); got != want {
				t.Errorf("row %d @%08X = %04X, want %04X", row, addr, got, want)
			}
		}
	})
}

// DSP DMA to a B-Bus destination follows the same rule (SCU User's
// Manual, DMA [RAM],D0: "Write units are 16 bit; 32 bit data is
// divided in half and written at intervals of 16bit x (0-64)"): with a
// sparse add mode each halfword of a long word lands at its own
// interval, not packed into one long-word store per doubled stop.
func TestSCUDSPDMASparseBBusHalfwordUnits(t *testing.T) {
	s := NewSCU()
	fb := newFakeSCUBus()
	s.SetBus(fb)
	d := &s.dsp

	const dst = 0x05E78924 // VDP2 VRAM (B-Bus)
	d.ct[0] = 0
	d.wa0 = dst >> 2
	d.data[0][0] = 0x007B4014
	d.data[0][1] = 0x007C4054

	// dir=1 (bit 12), addMode=111 (128-byte interval), ramSel=0, count=2
	instr := uint32(1<<12) | (7 << 15) | 2
	d.execDMA(instr)

	want := []uint16{0x007B, 0x4014, 0x007C, 0x4054}
	for i, w := range want {
		addr := uint32(dst) + uint32(i)*128
		if got := fb.Read16(addr); got != w {
			t.Errorf("halfword %d @%08X = %04X, want %04X", i, addr, got, w)
		}
	}
	if wantWA0 := (uint32(dst) + 4*128) >> 2; d.wa0 != wantWA0 {
		t.Errorf("wa0 = 0x%X, want 0x%X", d.wa0, wantWA0)
	}
}
