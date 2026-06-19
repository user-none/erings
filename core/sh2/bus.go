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
	// read transaction of the given size (in bytes: 1, 2, 4, or 16)
	// at the given address; WriteAccessCycles the same for a write
	// (some devices cost differently per direction - SDRAM reads run
	// a full burst pipeline while writes are single). Used by the
	// DMAC to accumulate accurate bus-occupation stall duration.
	AccessCycles(addr uint32, size uint32) uint32
	WriteAccessCycles(addr uint32, size uint32) uint32

	// ReadCacheLine fills dst from the 16-byte-aligned line at base in
	// a single bus tenure, so another bus master cannot interleave a
	// write between the four longwords. Per SH7604 Sec 8.4.1 a cache
	// line fill reads four longwords consecutively, and for Work RAM-H
	// that is one SDRAM burst (Sec 7.5.3-7.5.4) - an indivisible
	// transaction the other CPU/DMAC cannot break into. base is the
	// big-endian line; dst[0] is base's high byte.
	ReadCacheLine(base uint32, dst *[16]byte)

	// ReadRMW8 begins a TAS.B read-modify-write: it claims the
	// address's bus area and returns with the claim held. WriteRMW8
	// completes the sequence and releases it. Per SH7604 Sec 7.10 the
	// bus is not released between the read and write cycles of TAS.
	ReadRMW8(addr uint32) uint8
	WriteRMW8(addr uint32, val uint8)
}
