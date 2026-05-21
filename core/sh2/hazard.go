// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// hazardSources and hazardLoadDest precompute the per-opcode inputs
// to the load-use hazard check. They are populated once at package
// init from the authoritative decode helpers below (isLoadToReg,
// usesRegAsSource). The hot path (execute -> loadUseHazard) then
// resolves each check with two array loads and a handful of bitops,
// avoiding the nested-switch decode on every instruction that
// follows a load.
//
//	hazardSources[op]  - bit k set iff op reads Rk as a source
//	hazardLoadDest[op] - load destination register (0..15), or 0xFF
//	                     if op is not a memory load
var (
	hazardSources  [65536]uint16
	hazardLoadDest [65536]uint8
)

func init() {
	for op := 0; op < 65536; op++ {
		var sources uint16
		for r := uint8(0); r < 16; r++ {
			if usesRegAsSource(uint16(op), r) {
				sources |= 1 << r
			}
		}
		hazardSources[op] = sources

		loadDest := uint8(0xFF)
		for r := uint8(0); r < 16; r++ {
			if isLoadToReg(uint16(op), r) {
				loadDest = r
				break
			}
		}
		hazardLoadDest[op] = loadDest
	}
}

// loadUseHazard returns true if executing op after a load into loadReg
// would cause a 1-cycle pipeline stall. Returns false for forwarding
// exceptions where the hardware can resolve the dependency without
// stalling.
//
// Backed by the precomputed hazardSources/hazardLoadDest tables. The
// authoritative decode lives in isLoadToReg / usesRegAsSource below;
// init seeds the tables from those functions.
func loadUseHazard(op uint16, loadReg uint8) bool {
	if hazardLoadDest[op] == loadReg {
		return false // forwarding: load into same register
	}
	return hazardSources[op]&(uint16(1)<<loadReg) != 0
}

// isLoadToReg returns true if op is a memory load whose GPR destination
// matches reg. Used for the load-into-same-register forwarding exception.
func isLoadToReg(op uint16, reg uint8) bool {
	n := regN(op)

	switch nibble(op, 3) {
	case 0x0:
		switch nibble(op, 0) {
		case 0xC, 0xD, 0xE: // MOV.B/W/L @(R0,Rm),Rn
			return reg == n
		}
	case 0x5: // MOV.L @(disp,Rm),Rn
		return reg == n
	case 0x6:
		switch nibble(op, 0) {
		case 0, 1, 2, 4, 5, 6: // MOV.B/W/L @Rm,Rn or @Rm+,Rn
			return reg == n
		}
	case 0x8:
		switch nibble(op, 2) {
		case 4, 5: // MOV.B/W @(disp,Rm),R0
			return reg == 0
		}
	case 0x9: // MOV.W @(disp,PC),Rn
		return reg == n
	case 0xC:
		switch nibble(op, 2) {
		case 4, 5, 6: // MOV.B/W/L @(disp,GBR),R0
			return reg == 0
		}
	case 0xD: // MOV.L @(disp,PC),Rn
		return reg == n
	}
	return false
}

// usesRegAsSource returns true if op reads the given GPR in a way that
// requires the value during the EX pipeline stage. Store data operands
// are excluded per the store forwarding rule: WB and MA execute
// simultaneously on the internal bus for store data.
func usesRegAsSource(op uint16, reg uint8) bool {
	n := regN(op)
	m := regM(op)

	switch nibble(op, 3) {
	case 0x0:
		return usesRegGroup0(op, reg, n, m)
	case 0x1:
		// MOV.L Rm,@(disp,Rn) - store: Rn=address, Rm=data(forwarded)
		return reg == n
	case 0x2:
		return usesRegGroup2(op, reg, n, m)
	case 0x3:
		// All group 3 read both Rn and Rm
		return reg == n || reg == m
	case 0x4:
		// All group 4 read Rn.
		// MAC.W also reads Rm but with forwarding exception.
		return reg == n
	case 0x5:
		// MOV.L @(disp,Rm),Rn - load: Rm=address source
		return reg == m
	case 0x6:
		// All group 6 read Rm only (Rn is write-only destination)
		return reg == m
	case 0x7:
		// ADD #imm,Rn - reads Rn
		return reg == n
	case 0x8:
		return usesRegGroup8(op, reg, m)
	case 0x9, 0xA, 0xB, 0xD, 0xE:
		// No GPR source: PC-relative loads, BRA, BSR, MOV #imm
		return false
	case 0xC:
		return usesRegGroupC(op, reg)
	}
	return false
}

func usesRegGroup0(op uint16, reg, n, m uint8) bool {
	switch nibble(op, 0) {
	case 0x2:
		// STC SR/GBR/VBR,Rn - no GPR read
		return false
	case 0x3:
		// BSRF/BRAF Rm - reads register in regN position
		return reg == n
	case 0x4, 0x5, 0x6:
		// MOV.B/W/L Rm,@(R0,Rn) - store indexed
		// Address: R0+Rn (hazard), Data: Rm (forwarded)
		return reg == 0 || reg == n
	case 0x7:
		// MUL.L Rm,Rn
		return reg == n || reg == m
	case 0x8, 0x9, 0xA:
		// CLRT/SETT/CLRMAC/NOP/DIV0U/MOVT/STS - no GPR read
		return false
	case 0xB:
		// RTS(0)/SLEEP(1)/RTE(2)
		if nibble(op, 1) == 0x2 {
			return reg == 15 // RTE reads R15
		}
		return false
	case 0xC, 0xD, 0xE:
		// MOV.B/W/L @(R0,Rm),Rn - load indexed
		// Address: R0+Rm
		return reg == 0 || reg == m
	case 0xF:
		// MAC.L @Rm+,@Rn+ - Rn=address(hazard), Rm forwarded
		return reg == n
	}
	return false
}

func usesRegGroup2(op uint16, reg, n, m uint8) bool {
	switch nibble(op, 0) {
	case 0, 1, 2, 4, 5, 6:
		// MOV.B/W/L Rm,@Rn or Rm,@-Rn - store
		// Rn=address (hazard), Rm=data (forwarded)
		return reg == n
	default:
		// DIV0S/TST/AND/XOR/OR/CMP-STR/XTRCT/MULU/MULS
		return reg == n || reg == m
	}
}

func usesRegGroup8(op uint16, reg, m uint8) bool {
	switch nibble(op, 2) {
	case 0, 1:
		// MOV.B/W R0,@(disp,Rn) - store
		// Rn in regM position = address (hazard), R0=data (forwarded)
		return reg == m
	case 4, 5:
		// MOV.B/W @(disp,Rm),R0 - load: Rm=address source
		return reg == m
	case 8:
		// CMP/EQ #imm,R0 - reads R0
		return reg == 0
	}
	// BT/BF/BT-S/BF-S - no GPR
	return false
}

func usesRegGroupC(op uint16, reg uint8) bool {
	switch nibble(op, 2) {
	case 0, 1, 2:
		// MOV.B/W/L R0,@(disp,GBR) - R0=data(forwarded), GBR not GPR
		return false
	case 3, 4, 5, 6, 7:
		// TRAPA, loads from GBR, MOVA - no GPR source
		return false
	case 8, 9, 0xA, 0xB:
		// TST/AND/XOR/OR #imm,R0 - reads R0
		return reg == 0
	default:
		// TST.B/AND.B/XOR.B/OR.B #imm,@(R0,GBR) - R0 is address
		return reg == 0
	}
}
