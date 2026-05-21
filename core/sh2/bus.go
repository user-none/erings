// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// Bus provides memory access for the SH-2 CPU.
// Addresses are 32-bit. The SH-2 uses big-endian byte ordering.
// Word and longword accesses must be naturally aligned; the CPU
// raises an address error before reaching the bus on misaligned access.
type Bus interface {
	Read8(addr uint32) uint8
	Read16(addr uint32) uint16
	Read32(addr uint32) uint32
	Write8(addr uint32, val uint8)
	Write16(addr uint32, val uint16)
	Write32(addr uint32, val uint32)

	// AccessCycles returns the CPU state count consumed by a single
	// bus transaction of the given size (in bytes: 1, 2, 4, or 16)
	// at the given address. Used by the DMAC to accumulate accurate
	// bus-occupation stall duration.
	AccessCycles(addr uint32, size uint32) uint32
}
