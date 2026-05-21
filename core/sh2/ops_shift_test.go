// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestOpShift1(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		wantRn uint32
		wantT  uint32
	}{
		// SHLL R3 (0x4300): T = Rn>>31, Rn <<= 1.
		// Spec: SH-1/SH-2 Programming Manual Sec 6.100 p.247.
		{"SHLL_msb_set", 0x4300, 0x80000001, 0x00000002, 1},
		{"SHLL_msb_clear", 0x4300, 0x7FFFFFFF, 0xFFFFFFFE, 0},
		{"SHLL_zero", 0x4300, 0x00000000, 0x00000000, 0},
		{"SHLL_all_ones", 0x4300, 0xFFFFFFFF, 0xFFFFFFFE, 1},
		{"SHLL_lsb_only", 0x4300, 0x00000001, 0x00000002, 0},

		// SHLR R3 (0x4301): T = Rn&1, Rn >>= 1 (logical - MSB gets 0).
		// Spec: Programming Manual Sec 6.104 p.255.
		{"SHLR_lsb_set", 0x4301, 0x80000001, 0x40000000, 1},
		{"SHLR_lsb_clear", 0x4301, 0x80000000, 0x40000000, 0},
		{"SHLR_zero", 0x4301, 0x00000000, 0x00000000, 0},
		{"SHLR_msb_set_logical", 0x4301, 0x80000000, 0x40000000, 0},
		{"SHLR_all_ones", 0x4301, 0xFFFFFFFF, 0x7FFFFFFF, 1},

		// SHAL R3 (0x4320): T = Rn>>31, Rn <<= 1 (same bit pattern as SHLL).
		// Spec: Programming Manual Sec 6.96 p.241.
		{"SHAL_msb_set", 0x4320, 0x80000001, 0x00000002, 1},
		{"SHAL_msb_clear", 0x4320, 0x7FFFFFFF, 0xFFFFFFFE, 0},
		{"SHAL_matches_SHLL", 0x4320, 0xDEADBEEF, 0xBD5B7DDE, 1},
		{"SHAL_positive", 0x4320, 0x3FFFFFFF, 0x7FFFFFFE, 0},
		{"SHAL_sign_bit_carry", 0x4320, 0xC0000000, 0x80000000, 1},
		{"SHAL_zero", 0x4320, 0x00000000, 0x00000000, 0},

		// SHAR R3 (0x4321): T = Rn&1, Rn = int32(Rn) >> 1 (arithmetic - MSB preserved).
		// Spec: Programming Manual Sec 6.98 p.243.
		{"SHAR_lsb_set_neg", 0x4321, 0x80000001, 0xC0000000, 1},
		{"SHAR_lsb_clear_neg", 0x4321, 0x80000000, 0xC0000000, 0},
		{"SHAR_lsb_set_pos", 0x4321, 0x7FFFFFFF, 0x3FFFFFFF, 1},
		{"SHAR_lsb_clear_pos", 0x4321, 0x7FFFFFFE, 0x3FFFFFFF, 0},
		{"SHAR_all_ones", 0x4321, 0xFFFFFFFF, 0xFFFFFFFF, 1},
		{"SHAR_one", 0x4321, 0x00000001, 0x00000000, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			cpu.reg.R[n] = tt.rn
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpShiftN(t *testing.T) {
	// Fixed-count shifts do not affect T per Programming Manual
	// Sec 6.101-6.107. Each case is run twice - once with T=1 before and
	// once with T=0 before - to verify T is preserved in both
	// directions. Extra cases exercise boundary drops.
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		wantRn uint32
	}{
		// SHLL2 R3 (0x4308) - Programming Manual Sec 6.101 p.249.
		{"SHLL2", 0x4308, 0xC0000001, 0x00000004},
		{"SHLL2_top_drop", 0x4308, 0xC0000000, 0x00000000},
		{"SHLL2_zero", 0x4308, 0x00000000, 0x00000000},
		// SHLR2 R3 (0x4309) - Programming Manual Sec 6.105 p.257.
		{"SHLR2", 0x4309, 0x80000007, 0x20000001},
		{"SHLR2_all_ones", 0x4309, 0xFFFFFFFF, 0x3FFFFFFF},
		// SHLL8 R3 (0x4318) - Programming Manual Sec 6.102 p.251.
		{"SHLL8", 0x4318, 0xFF000001, 0x00000100},
		{"SHLL8_overflow", 0x4318, 0x01000000, 0x00000000},
		// SHLR8 R3 (0x4319) - Programming Manual Sec 6.106 p.259.
		{"SHLR8", 0x4319, 0x800000FF, 0x00800000},
		{"SHLR8_msb_zero_fill", 0x4319, 0x80000000, 0x00800000},
		// SHLL16 R3 (0x4328) - Programming Manual Sec 6.103 p.253.
		{"SHLL16", 0x4328, 0xFFFF0001, 0x00010000},
		{"SHLL16_high_dropped", 0x4328, 0x00010000, 0x00000000},
		// SHLR16 R3 (0x4329) - Programming Manual Sec 6.107 p.261.
		{"SHLR16", 0x4329, 0x8000FFFF, 0x00008000},
		{"SHLR16_msb_zero_fill", 0x4329, 0x80000000, 0x00008000},
	}
	runWith := func(t *testing.T, tBefore bool, wantT uint32) {
		for _, tt := range tests {
			name := tt.name
			if tBefore {
				name += "_tset"
			} else {
				name += "_tclear"
			}
			t.Run(name, func(t *testing.T) {
				cpu := newDecodeTestCPU(tt.op)
				n := regN(tt.op)
				cpu.reg.R[n] = tt.rn
				cpu.reg.SetTVal(tBefore)
				before := cpu.cycles
				cpu.Clock()
				if cpu.reg.R[n] != tt.wantRn {
					t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
				}
				if cpu.reg.T() != wantT {
					t.Errorf("T = %d, want %d (unchanged)", cpu.reg.T(), wantT)
				}
				if int(cpu.cycles-before) != 1 {
					t.Errorf("cycles = %d, want 1", cpu.cycles-before)
				}
			})
		}
	}
	runWith(t, true, 1)
	runWith(t, false, 0)
}

func TestOpRotate(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		wantRn uint32
		wantT  uint32
	}{
		// ROTL R3 (0x4304): T = Rn>>31, Rn = (Rn<<1)|T.
		// Spec: Programming Manual Sec 6.84 p.219.
		{"ROTL_msb_set", 0x4304, 0x80000000, 0x00000001, 1},
		{"ROTL_msb_clear", 0x4304, 0x7FFFFFFF, 0xFFFFFFFE, 0},
		{"ROTL_all_ones", 0x4304, 0xFFFFFFFF, 0xFFFFFFFF, 1},
		{"ROTL_zero", 0x4304, 0x00000000, 0x00000000, 0},
		{"ROTL_pattern", 0x4304, 0x12345678, 0x2468ACF0, 0},

		// ROTR R3 (0x4305): T = Rn&1, Rn = (Rn>>1)|(T<<31).
		// Spec: Programming Manual Sec 6.86 p.223.
		{"ROTR_lsb_set", 0x4305, 0x00000001, 0x80000000, 1},
		{"ROTR_lsb_clear", 0x4305, 0x80000000, 0x40000000, 0},
		{"ROTR_all_ones", 0x4305, 0xFFFFFFFF, 0xFFFFFFFF, 1},
		{"ROTR_zero", 0x4305, 0x00000000, 0x00000000, 0},
		{"ROTR_pattern", 0x4305, 0x12345678, 0x091A2B3C, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			cpu.reg.R[n] = tt.rn
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpRotateCarry(t *testing.T) {
	tests := []struct {
		name    string
		op      uint16
		rn      uint32
		tBefore bool
		wantRn  uint32
		wantT   uint32
	}{
		// ROTCL R3 (0x4324): old=T, T=Rn>>31, Rn=(Rn<<1)|old.
		// Spec: Programming Manual Sec 6.85 p.221.
		{"ROTCL_msb1_T0", 0x4324, 0x80000000, false, 0x00000000, 1},
		{"ROTCL_msb0_T1", 0x4324, 0x00000000, true, 0x00000001, 0},
		{"ROTCL_msb1_T1", 0x4324, 0x80000001, true, 0x00000003, 1},
		{"ROTCL_t_in_1_msb_0", 0x4324, 0x40000000, true, 0x80000001, 0},
		{"ROTCL_zero_t_0", 0x4324, 0x00000000, false, 0x00000000, 0},

		// ROTCR R3 (0x4325): old=T, T=Rn&1, Rn=(Rn>>1)|(old<<31).
		// Spec: Programming Manual Sec 6.87 p.225.
		{"ROTCR_lsb1_T0", 0x4325, 0x00000001, false, 0x00000000, 1},
		{"ROTCR_lsb0_T1", 0x4325, 0x00000000, true, 0x80000000, 0},
		{"ROTCR_lsb1_T1", 0x4325, 0x80000001, true, 0xC0000000, 1},
		{"ROTCR_t_in_1_lsb_0", 0x4325, 0x00000002, true, 0x80000001, 0},
		{"ROTCR_zero_t_0", 0x4325, 0x00000000, false, 0x00000000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			cpu.reg.R[n] = tt.rn
			cpu.reg.SetTVal(tt.tBefore)
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}
