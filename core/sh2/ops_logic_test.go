// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestOpLogicReg(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		rm     uint32
		wantRn uint32
	}{
		// AND R5,R3: 0x2359
		{"AND", 0x2359, 0xFF00FF00, 0x0F0F0F0F, 0x0F000F00},
		// OR R5,R3: 0x235B
		{"OR", 0x235B, 0xFF00FF00, 0x0F0F0F0F, 0xFF0FFF0F},
		// XOR R5,R3: 0x235A
		{"XOR", 0x235A, 0xFF00FF00, 0x0F0F0F0F, 0xF00FF00F},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[n] = tt.rn
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetT() // T should be unchanged
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

// TestOpANDBoundaries covers AND Rm,Rn zero, identity, register aliasing
// with bidirectional T preservation (PM section 6.7).
func TestOpANDBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		rm     uint32 // ignored when Rn==Rm
		wantRn uint32
	}{
		// AND R5,R3: 0x2359
		{"and_all_zero", 0x2359, 0xDEADBEEF, 0x00000000, 0x00000000},
		{"and_identity", 0x2359, 0xDEADBEEF, 0xFFFFFFFF, 0xDEADBEEF},
		// AND R3,R3: 0x2339 (Rn==Rm)
		{"and_rn_equals_rm", 0x2339, 0xDEADBEEF, 0, 0xDEADBEEF},
	}
	runWith := func(t *testing.T, tBefore bool) {
		wantT := uint32(0)
		suffix := "_tclear"
		if tBefore {
			wantT = 1
			suffix = "_tset"
		}
		for _, tt := range tests {
			t.Run(tt.name+suffix, func(t *testing.T) {
				cpu := newDecodeTestCPU(tt.op)
				n := regN(tt.op)
				m := regM(tt.op)
				cpu.reg.R[n] = tt.rn
				if n != m {
					cpu.reg.R[m] = tt.rm
				}
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
	runWith(t, false)
	runWith(t, true)
}

// TestOpORBoundaries covers OR Rm,Rn zero, all-ones, register aliasing
// with bidirectional T preservation (PM section 6.54).
func TestOpORBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		rm     uint32
		wantRn uint32
	}{
		// OR R5,R3: 0x235B
		{"or_zero", 0x235B, 0xDEADBEEF, 0x00000000, 0xDEADBEEF},
		{"or_all_ones", 0x235B, 0xDEADBEEF, 0xFFFFFFFF, 0xFFFFFFFF},
		// OR R3,R3: 0x233B
		{"or_rn_equals_rm", 0x233B, 0xDEADBEEF, 0, 0xDEADBEEF},
	}
	runWith := func(t *testing.T, tBefore bool) {
		wantT := uint32(0)
		suffix := "_tclear"
		if tBefore {
			wantT = 1
			suffix = "_tset"
		}
		for _, tt := range tests {
			t.Run(tt.name+suffix, func(t *testing.T) {
				cpu := newDecodeTestCPU(tt.op)
				n := regN(tt.op)
				m := regM(tt.op)
				cpu.reg.R[n] = tt.rn
				if n != m {
					cpu.reg.R[m] = tt.rm
				}
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
	runWith(t, false)
	runWith(t, true)
}

// TestOpXORBoundaries covers XOR Rm,Rn zero, all-ones, Rn==Rm yields 0
// with bidirectional T preservation (PM section 6.121).
func TestOpXORBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rn     uint32
		rm     uint32
		wantRn uint32
	}{
		// XOR R5,R3: 0x235A
		{"xor_with_zero", 0x235A, 0xDEADBEEF, 0x00000000, 0xDEADBEEF},
		{"xor_with_all_ones", 0x235A, 0xDEADBEEF, 0xFFFFFFFF, 0x21524110},
		// XOR R3,R3: 0x233A (Rn==Rm yields zero)
		{"xor_self_is_zero", 0x233A, 0xDEADBEEF, 0, 0x00000000},
	}
	runWith := func(t *testing.T, tBefore bool) {
		wantT := uint32(0)
		suffix := "_tclear"
		if tBefore {
			wantT = 1
			suffix = "_tset"
		}
		for _, tt := range tests {
			t.Run(tt.name+suffix, func(t *testing.T) {
				cpu := newDecodeTestCPU(tt.op)
				n := regN(tt.op)
				m := regM(tt.op)
				cpu.reg.R[n] = tt.rn
				if n != m {
					cpu.reg.R[m] = tt.rm
				}
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
	runWith(t, false)
	runWith(t, true)
}

func TestOpTST(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		rn    uint32
		rm    uint32
		wantT uint32
	}{
		// TST R5,R3: 0x2358
		{"overlapping_bits", 0x2358, 0xFF00FF00, 0x0F000000, 0},
		{"no_overlap", 0x2358, 0xFF00FF00, 0x00FF00FF, 1},
		{"both_zero", 0x2358, 0x00000000, 0x00000000, 1},
		// TST R3,R3: 0x2338 (Rn==Rm)
		{"rn_equals_rm_nonzero", 0x2338, 0xDEADBEEF, 0, 0},
		{"rn_equals_rm_zero", 0x2338, 0x00000000, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[n] = tt.rn
			if n != m {
				cpu.reg.R[m] = tt.rm
			}
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			// TST does not modify registers.
			if cpu.reg.R[n] != tt.rn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X (unchanged)", n, cpu.reg.R[n], tt.rn)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpNOT(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		rm     uint32
		wantRn uint32
	}{
		// NOT R5,R3: 0x6357
		{"all_zero", 0x6357, 0x00000000, 0xFFFFFFFF},
		{"pattern", 0x6357, 0xFF00FF00, 0x00FF00FF},
		{"all_ones", 0x6357, 0xFFFFFFFF, 0x00000000},
		// NOT R3,R3: 0x6337 (destination overwrites source)
		{"rn_equals_rm", 0x6337, 0xDEADBEEF, 0x21524110},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			n := regN(tt.op)
			m := regM(tt.op)
			cpu.reg.R[m] = tt.rm
			cpu.reg.SetT() // T should be unchanged
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[n] != tt.wantRn {
				t.Errorf("R[%d] = 0x%08X, want 0x%08X", n, cpu.reg.R[n], tt.wantRn)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpNOTPreservesTClear(t *testing.T) {
	// NOT R5,R3: 0x6357 with T initially clear.
	cpu := newDecodeTestCPU(0x6357)
	cpu.reg.R[5] = 0x5A5A5A5A
	cpu.reg.SetTVal(false)
	cpu.Clock()
	if cpu.reg.R[3] != 0xA5A5A5A5 {
		t.Errorf("R3 = 0x%08X, want 0xA5A5A5A5", cpu.reg.R[3])
	}
	if cpu.reg.T() != 0 {
		t.Errorf("T = %d, want 0 (unchanged)", cpu.reg.T())
	}
}

func TestOpLogicImm(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		r0     uint32
		wantR0 uint32
	}{
		// ANDI #0x0F,R0: 0xC90F
		{"ANDI", 0xC90F, 0xDEADBEEF, 0x0000000F},
		// ORI #0xF0,R0: 0xCBF0
		{"ORI", 0xCBF0, 0x0000000A, 0x000000FA},
		// XORI #0xFF,R0: 0xCAFF
		{"XORI", 0xCAFF, 0x000000A5, 0x0000005A},
		// ANDI variants: immediate is zero-extended to 32 bits, so upper
		// 24 bits of R0 are ANDed with 0 and cleared (PM section 6.8).
		{"andi_zero_mask", 0xC900, 0xDEADBEEF, 0x00000000},
		{"andi_full_mask", 0xC9FF, 0xDEADBEEF, 0x000000EF},
		{"andi_zeros_upper", 0xC90F, 0xAABBCCDD, 0x0000000D},
		// ORI variants: imm zero-extended so upper 24 bits of R0 are
		// preserved (OR with 0) (PM section 6.55).
		{"ori_zero_imm", 0xCB00, 0xDEADBEEF, 0xDEADBEEF},
		{"ori_full_imm", 0xCBFF, 0xAABBCC00, 0xAABBCCFF},
		// XORI variants: imm zero-extended so upper 24 bits of R0 are
		// preserved (XOR with 0) (PM section 6.122).
		{"xori_zero_imm", 0xCA00, 0xDEADBEEF, 0xDEADBEEF},
		{"xori_self_toggles_low_byte", 0xCAFF, 0xDEADBE55, 0xDEADBEAA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[0] = tt.r0
			cpu.reg.SetT() // T should be unchanged
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[0] != tt.wantR0 {
				t.Errorf("R0 = 0x%08X, want 0x%08X", cpu.reg.R[0], tt.wantR0)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpTSTI(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		r0    uint32
		wantT uint32
	}{
		// TSTI #0x80,R0: 0xC880
		{"matching_bits", 0xC880, 0x00000080, 0},
		{"no_match", 0xC880, 0x0000007F, 1},
		// imm=0xFF zero-extended; R0=0xFFFFFFFF has low-byte bits set.
		{"all_ones_mask", 0xC8FF, 0xFFFFFFFF, 0},
		// R0=0 yields T=1 regardless of imm.
		{"r0_zero", 0xC8FF, 0x00000000, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[0] = tt.r0
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			if cpu.reg.R[0] != tt.r0 {
				t.Errorf("R0 = 0x%08X, want 0x%08X (unchanged)", cpu.reg.R[0], tt.r0)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("cycles = %d, want 1", cpu.cycles-before)
			}
		})
	}
}

func TestOpLogicByte(t *testing.T) {
	tests := []struct {
		name     string
		op       uint16
		initByte uint8
		wantByte uint8
	}{
		// AND.B #0x0F,@(R0,GBR): 0xCD0F
		{"ANDB", 0xCD0F, 0xAB, 0x0B},
		// OR.B #0xF0,@(R0,GBR): 0xCFF0
		{"ORB", 0xCFF0, 0x0A, 0xFA},
		// XOR.B #0xFF,@(R0,GBR): 0xCEFF
		{"XORB", 0xCEFF, 0xA5, 0x5A},
		// imm=0 clears target byte (PM section 6.9).
		{"andb_imm_zero", 0xCD00, 0xAB, 0x00},
		// imm=0xFF sets target byte (PM section 6.56).
		{"orb_imm_full", 0xCFFF, 0x00, 0xFF},
		// imm=0xFF flips all bits (PM section 6.123).
		{"xorb_self_toggles", 0xCEFF, 0x00, 0xFF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[0] = 0x10
			cpu.reg.GBR = 0x200
			addr := uint32(0x210)
			cpu.bus.Write8(addr, tt.initByte)
			cpu.reg.SetT() // T should be unchanged
			before := cpu.cycles

			// Cycle 1: read
			s1 := cpu.Clock()
			if s1.Bus != BusRead {
				t.Errorf("cycle 1: bus = %d, want BusRead", s1.Bus)
			}
			// Cycle 2: logic
			s2 := cpu.Clock()
			if s2.Bus != BusNone {
				t.Errorf("cycle 2: bus = %d, want BusNone", s2.Bus)
			}
			// Cycle 3: write
			s3 := cpu.Clock()
			if s3.Bus != BusWrite {
				t.Errorf("cycle 3: bus = %d, want BusWrite", s3.Bus)
			}

			got := cpu.bus.Read8(addr)
			if got != tt.wantByte {
				t.Errorf("mem[0x%X] = 0x%02X, want 0x%02X", addr, got, tt.wantByte)
			}
			if cpu.reg.T() != 1 {
				t.Errorf("T = %d, want 1 (unchanged)", cpu.reg.T())
			}
			if int(cpu.cycles-before) != 3 {
				t.Errorf("cycles = %d, want 3", cpu.cycles-before)
			}
		})
	}
}

// TestOpLogicBytePreservesNeighbors verifies AND.B/OR.B/XOR.B modify only
// the target byte at (R0+GBR) and leave adjacent bytes untouched.
func TestOpLogicBytePreservesNeighbors(t *testing.T) {
	tests := []struct {
		name     string
		op       uint16
		initByte uint8
		wantByte uint8
	}{
		{"andb_preserves_neighbors", 0xCD0F, 0xAB, 0x0B},
		{"orb_preserves_neighbors", 0xCFF0, 0x0A, 0xFA},
		{"xorb_preserves_neighbors", 0xCEFF, 0xA5, 0x5A},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[0] = 0x10
			cpu.reg.GBR = 0x200
			addr := uint32(0x210)
			cpu.bus.Write8(addr-1, 0x11)
			cpu.bus.Write8(addr, tt.initByte)
			cpu.bus.Write8(addr+1, 0x22)
			cpu.bus.Write8(addr+2, 0x33)

			cpu.Clock()
			cpu.Clock()
			cpu.Clock()

			if got := cpu.bus.Read8(addr); got != tt.wantByte {
				t.Errorf("mem[0x%X] = 0x%02X, want 0x%02X", addr, got, tt.wantByte)
			}
			if got := cpu.bus.Read8(addr - 1); got != 0x11 {
				t.Errorf("mem[0x%X] = 0x%02X, want 0x11 (neighbor changed)", addr-1, got)
			}
			if got := cpu.bus.Read8(addr + 1); got != 0x22 {
				t.Errorf("mem[0x%X] = 0x%02X, want 0x22 (neighbor changed)", addr+1, got)
			}
			if got := cpu.bus.Read8(addr + 2); got != 0x33 {
				t.Errorf("mem[0x%X] = 0x%02X, want 0x33 (neighbor changed)", addr+2, got)
			}
		})
	}
}

func TestOpTSTB(t *testing.T) {
	tests := []struct {
		name    string
		op      uint16
		gbr     uint32
		r0      uint32
		memByte uint8
		wantT   uint32
	}{
		// TST.B #0x80,@(R0,GBR): 0xCC80
		{"matching_bits", 0xCC80, 0x200, 0x10, 0x80, 0},
		{"no_match", 0xCC80, 0x200, 0x10, 0x7F, 1},
		// GBR+R0 = 0x100, crosses the 0x100 page boundary from (0xFE+0x02).
		{"gbr_plus_r0_boundary", 0xCC80, 0x00FE, 0x02, 0x80, 0},
		// imm=0xFF masks every bit in the target byte.
		{"imm_byte_mask", 0xCCFF, 0x200, 0x10, 0x55, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[0] = tt.r0
			cpu.reg.GBR = tt.gbr
			addr := tt.gbr + tt.r0
			cpu.bus.Write8(addr, tt.memByte)
			before := cpu.cycles

			// Cycle 1: read + test
			s1 := cpu.Clock()
			if s1.Bus != BusRead {
				t.Errorf("cycle 1: bus = %d, want BusRead", s1.Bus)
			}
			// Cycles 2-3: idle
			cpu.Clock()
			cpu.Clock()

			if cpu.reg.T() != tt.wantT {
				t.Errorf("T = %d, want %d", cpu.reg.T(), tt.wantT)
			}
			// Memory unchanged
			got := cpu.bus.Read8(addr)
			if got != tt.memByte {
				t.Errorf("mem[0x%X] = 0x%02X, want 0x%02X (unchanged)", addr, got, tt.memByte)
			}
			if int(cpu.cycles-before) != 3 {
				t.Errorf("cycles = %d, want 3", cpu.cycles-before)
			}
		})
	}
}
