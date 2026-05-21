// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestOpTAS(t *testing.T) {
	// TAS.B @Rn: T = (byte == 0); byte |= 0x80; cycle = 4.
	// Spec: SH-1/SH-2 Programming Manual Sec 6.115 p.283.
	tests := []struct {
		name     string
		initByte uint8
		wantT    uint32
		wantByte uint8
	}{
		{"zero", 0x00, 1, 0x80},
		{"nonzero", 0x55, 0, 0xD5},
		{"bit7_already_set", 0x80, 0, 0x80},
		{"all_ones", 0xFF, 0, 0xFF},
		{"byte_0x7F", 0x7F, 0, 0xFF},
		{"byte_lsb_only", 0x01, 0, 0x81},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TAS.B @R0: 0x401B (n=0)
			cpu := newDecodeTestCPU(0x401B)
			n := regN(0x401B)
			addr := cpu.reg.R[n] // use default address in R0
			cpu.bus.Write8(addr, tt.initByte)
			before := cpu.cycles

			// Cycle 1: EX
			cpu.Clock()
			// Cycle 2: MA read (bus held)
			s2 := cpu.Clock()
			if s2.Bus != BusHeld {
				t.Errorf("cycle 2: bus = %d, want BusHeld", s2.Bus)
			}
			// Cycle 3: MA internal (bus held)
			s3 := cpu.Clock()
			if s3.Bus != BusHeld {
				t.Errorf("cycle 3: bus = %d, want BusHeld", s3.Bus)
			}
			// Cycle 4: MA write (bus held)
			s4 := cpu.Clock()
			if s4.Bus != BusHeld {
				t.Errorf("cycle 4: bus = %d, want BusHeld", s4.Bus)
			}

			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			got := cpu.bus.Read8(addr)
			if got != tt.wantByte {
				t.Errorf("mem[0x%X] = 0x%02X, want 0x%02X", addr, got, tt.wantByte)
			}
			if cpu.reg.R[n] != addr {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X (unchanged)", n, cpu.reg.R[n], addr)
			}
			if int(cpu.cycles-before) != 4 {
				t.Errorf("cycles = %d, want 4", cpu.cycles-before)
			}
		})
	}
}

// TestOpTASPreservesNeighbors verifies that TAS.B only modifies the
// target byte and leaves adjacent bytes at Rn-1 and Rn+1 intact.
// TAS.B is documented as a byte read-modify-write; any wider access
// would corrupt surrounding memory. Spec: Programming Manual Sec 6.115.
func TestOpTASPreservesNeighbors(t *testing.T) {
	cases := []struct {
		name   string
		offset uint32 // alignment of the target byte within a longword
	}{
		{"byte_addr_aligned", 0},
		{"byte_addr_plus_1", 1},
		{"byte_addr_plus_2", 2},
		{"byte_addr_plus_3", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(0x401B) // TAS.B @R0
			n := regN(0x401B)
			base := cpu.reg.R[n] &^ 3
			target := base + c.offset
			cpu.reg.R[n] = target

			// Prime neighbors with a distinct pattern.
			cpu.bus.Write8(base+0, 0xA1)
			cpu.bus.Write8(base+1, 0xB2)
			cpu.bus.Write8(base+2, 0xC3)
			cpu.bus.Write8(base+3, 0xD4)
			// Target byte = 0x10 (nonzero so T=0 after TAS, and bit 7 gets set).
			cpu.bus.Write8(target, 0x10)

			for i := 0; i < 4; i++ {
				cpu.Clock()
			}

			wantTarget := byte(0x10 | 0x80)
			if got := cpu.bus.Read8(target); got != wantTarget {
				t.Errorf("target byte 0x%X = 0x%02X, want 0x%02X", target, got, wantTarget)
			}
			neighbors := [4]byte{0xA1, 0xB2, 0xC3, 0xD4}
			for i := uint32(0); i < 4; i++ {
				addr := base + i
				want := neighbors[i]
				if addr == target {
					want = wantTarget
				}
				if got := cpu.bus.Read8(addr); got != want {
					t.Errorf("byte 0x%X = 0x%02X, want 0x%02X", addr, got, want)
				}
			}
		})
	}
}
