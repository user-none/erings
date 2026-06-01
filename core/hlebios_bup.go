// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/sh2"
)

// HLE implementation of the Backup Library (BUP_*) slot services
// the real Saturn BIOS exposes through the PER+BUP hybrid driver.
// Game code dispatches into these via JSR @(N*4, driver_base) once
// PER_Init has installed the slot table. Slot 0 (driver init) stays
// in hlebios_per.go; slots 1-10 live here.
//
// The HLE works against the bus's 32 KB backup RAM byte slice
// (bus.backup) directly. bus.backup[i] is logical byte i — the bus
// translates bus addresses $00180001 + 2*i to backup[i] (odd-byte
// hardware mapping) which makes the on-disk format flat-byte.
//
// On-disk format (32 KB, divided into 512 blocks of 64 bytes):
//
//	block 0 (bytes 0-63):     4x "BackUpRam Format" (16 B each)
//	block 1 (bytes 64-127):   reserved (zero-filled)
//	blocks 2-511:             directory entries + data, packed
//
// Each in-use file consumes:
//
//	1 directory entry block:
//	  +$00  Uint32 BE  status word ($80000000 = in-use, 0 = free)
//	  +$04  11 bytes   filename (ASCII, NUL-padded)
//	  +$0F  Uint8      language (0-5)
//	  +$10  10 bytes   comment (ASCII, NUL-padded)
//	  +$1A  Uint32 BE  date (minutes since 1980-01-01 00:00)
//	  +$1E  Uint32 BE  datasize (bytes)
//	  +$22  varies     block list (15 word slots at +$22..+$3F):
//	                   each non-zero word is a block index claimed
//	                   by the file; a single $0000 word terminates
//	                   the in-entry list.
//	                   If the in-entry list fills all 15 slots with
//	                   no terminator, the list continues at the start
//	                   of the FIRST listed block — see continuation
//	                   pages below.
//	0..15 continuation pages (when the file needs more than 14
//	                   data blocks): the FIRST C in-entry slots
//	                   hold continuation-page block indices; the
//	                   remaining (15 - C) slots hold in-entry
//	                   data blocks. Each cont page has bytes
//	                   +$00..+$03 = $0000 $0000 sentinel; the
//	                   next 30 word slots hold additional block
//	                   indices. Cont pages 0..C-2 fill all 30
//	                   slots (no terminator, no payload tail);
//	                   the last cont page holds the remaining
//	                   indices, a $0000 terminator, then payload
//	                   tail through +$3F. Max single-file size
//	                   = 58*(D+C) + 28 bytes ≈ 26.9 KB at
//	                   D=449, C=15.
//	N data blocks:    bytes +$00..+$03 are a 4-byte header (real
//	                   BIOS writes zeros there; BUP_Read skips
//	                   them); bytes +$04..+$3F hold up to 60
//	                   payload bytes.
//
// Scope: internal console memory only (R4 == 0). External A-bus
// cart (R4 == 2) reports BUP_NON. Serial (R4 == 1) also reports
// BUP_NON since erings doesn't model that device.

// BUP return codes per sega_bup.h (`BUP_NON` through `BUP_BROKEN`)
// and verified against the slot bodies' `MOV #N,R0` sites: BIOS
// slot bodies return these positive values directly and the SDK
// user-library wrappers pass them through verbatim (bup_usr.o
// preProc / slot JSR / postProc with no return-value transform).
const (
	bupOK              uint32 = 0 // success
	bupNon             uint32 = 1 // BUP_NON: device not connected (slot 1 cart absent, slot 3 prelude BUP_NON, etc.)
	bupUnformat        uint32 = 2 // BUP_UNFORMAT: backup RAM lacks the "BackUpRam Format" magic string
	bupWriteProtect    uint32 = 3 // BUP_WRITE_PROTECT: cart status reported write-protect
	bupNotEnoughMemory uint32 = 4 // BUP_NOT_ENOUGH_MEMORY: not enough free blocks to satisfy a write
	bupNotFound        uint32 = 5 // BUP_NOT_FOUND: filename did not match any directory entry
	bupFound           uint32 = 6 // BUP_FOUND: file already exists (Write w/ owsw != 0)
	bupNoMatch         uint32 = 7 // BUP_NO_MATCH: Verify byte mismatch / data does not match
	bupBroken          uint32 = 8 // BUP_BROKEN: backup RAM unreadable / corrupted
)

// Block layout constants. The 32 KB device has 512 blocks of 64
// bytes; logical byte i in bus.backup[i] maps to bus address
// $00180001 + 2*i.
const (
	bupBlockSize         = 64
	bupTotalBlocks       = 512
	bupHeaderBlock       = 0
	bupReservedBlock     = 1
	bupFirstDirBlock     = 2
	bupDirListOff        = 0x22                              // dir entry +$22: start of in-entry block list
	bupDirListWordSlots  = 15                                // (+$3F - +$22 + 1) / 2 = 15 word slots
	bupDirOutStride      = 0x24                              // BUP_Dir output entry stride: sizeof(BupDir) = 34 field bytes + 2 alignment pad; real BIOS strides at 0x24
	bupContPayloadOff    = 4                                 // continuation page indices start after 4-byte sentinel
	bupContWordSlots     = 30                                // (64 - 4) / 2 = 30 word slots per continuation page
	bupBlockHeaderSize   = 4                                 // data block 4-byte header at +$00..+$03 (zero); BUP_Read skips it
	bupBlockPayload      = bupBlockSize - bupBlockHeaderSize // 60 payload bytes per data block at +$04..+$3F
	bupMaxFileDataBlocks = 256                               // safety cap on the recursive expansion (prevents
	// pathological cycles in malformed saves)
)

// BUP magic header. The bus's NewBus calls formatBackupRAM which
// writes 4x this string to bus.backup[0..63]. SelPart and every
// other read path checks for the first occurrence at byte 0.
var bupMagic = [16]byte{
	'B', 'a', 'c', 'k', 'U', 'p', 'R', 'a',
	'm', ' ', 'F', 'o', 'r', 'm', 'a', 't',
}

// bupBE16 reads a big-endian 16-bit value from b[off:off+2].
// Caller is responsible for bounds; out-of-range indexing panics
// in tests, which is the right behavior — silently truncating
// reads from random offsets would mask real bugs.
func bupBE16(b []byte, off int) uint16 {
	return uint16(b[off])<<8 | uint16(b[off+1])
}

// bupBE32 reads a big-endian 32-bit value from b[off:off+4].
func bupBE32(b []byte, off int) uint32 {
	return uint32(b[off])<<24 | uint32(b[off+1])<<16 |
		uint32(b[off+2])<<8 | uint32(b[off+3])
}

// bupWriteBE16 stores a big-endian 16-bit value at b[off:off+2].
func bupWriteBE16(b []byte, off int, v uint16) {
	b[off] = byte(v >> 8)
	b[off+1] = byte(v)
}

// bupWriteBE32 stores a big-endian 32-bit value at b[off:off+4].
func bupWriteBE32(b []byte, off int, v uint32) {
	b[off] = byte(v >> 24)
	b[off+1] = byte(v >> 16)
	b[off+2] = byte(v >> 8)
	b[off+3] = byte(v)
}

// bupValidBlockIdx reports whether idx names a directory/data block
// on disk: idx in [bupFirstDirBlock, bupTotalBlocks). Block 0 is the
// magic header and block 1 is reserved; both are excluded. Idx 0
// (the in-entry list / cont-list terminator) is also rejected by
// this check since 0 < bupFirstDirBlock.
func bupValidBlockIdx(idx uint16) bool {
	return int(idx) >= bupFirstDirBlock && int(idx) < bupTotalBlocks
}

// bupCheckFormat returns true iff the first 16 bytes of backup RAM
// match the "BackUpRam Format" magic string. The full header has
// the magic repeated 4 times across bytes 0-63; checking only the
// first repetition is sufficient because formatBackupRAM and real
// BIOS both write all four atomically.
func bupCheckFormat(bus *Bus) bool {
	if len(bus.backup) < 16 {
		return false
	}
	for i := 0; i < 16; i++ {
		if bus.backup[i] != bupMagic[i] {
			return false
		}
	}
	return true
}

// bupEntryInUse reports whether the directory entry at block index
// blockIdx is marked in-use. Top bit of the 4-byte status word at
// the block start is the in-use flag; any non-zero status counts as
// in-use to be liberal in what we accept. Write/Delete use exactly
// $80000000 (in-use) or $00000000 (free).
func bupEntryInUse(bus *Bus, blockIdx int) bool {
	off := blockIdx * bupBlockSize
	if off+4 > len(bus.backup) {
		return false
	}
	return (bus.backup[off] & 0x80) != 0
}

// bupReadFilename copies the 11-byte filename field from the
// directory entry block into a fixed-size array. NUL padding is
// preserved verbatim.
func bupReadFilename(bus *Bus, blockIdx int) [11]byte {
	var name [11]byte
	off := blockIdx*bupBlockSize + 4
	copy(name[:], bus.backup[off:off+11])
	return name
}

// bupFilenameEqual compares two 11-byte filename buffers. SDK
// callers pass a 12-byte buffer with NUL terminator; the on-disk
// format only stores the first 11 bytes (the NUL slot is repurposed
// for the language byte). Compare exactly 11 bytes.
func bupFilenameEqual(a, b [11]byte) bool {
	return a == b
}

// bupCallerFilename reads an 11-byte filename from the caller's
// buffer at the given bus address. The SDK BUP_Read/Delete/Verify
// API documents a 12-byte filename (11 ASCII + NUL); we read 11
// to match the on-disk layout. Returns the filename and ok=false
// if the buffer address is null or unreachable.
func bupCallerFilename(bus *Bus, addr uint32) (name [11]byte, ok bool) {
	if addr == 0 {
		return name, false
	}
	for i := 0; i < 11; i++ {
		name[i] = bus.Read8(addr + uint32(i))
	}
	return name, true
}

// bupParseExtentList parses a sequence of 16-bit BE block indices
// starting at byte offset `off` in bus.backup, with `maxSlots` word
// slots available. Each non-zero word is a block index appended to
// the result; a single $0000 word terminates the list. If the slot
// window ends without a $0000 having been seen, terminated is false
// and the caller follows the continuation chain (the first listed
// block becomes the continuation page).
//
// Single-zero termination matches the doc (docs/bios/backup_library.md
// Block list semantics) and the verified NIGHTS_01 continuation page
// at byte $0E6, which holds a single $0000 word followed by payload.
func bupParseExtentList(b []byte, off, maxSlots int) (indices []uint16, terminated bool) {
	for i := 0; i < maxSlots; i++ {
		v := bupBE16(b, off+i*2)
		if v == 0 {
			return indices, true
		}
		indices = append(indices, v)
	}
	return indices, false
}

// bupReadChainEntry describes one segment of a file's payload:
// the block to read from, the byte offset within the block where
// the payload begins, and the exclusive end offset. This single
// shape covers data blocks (startOff = 4, endOff = bupBlockSize,
// i.e., bytes +$04..+$3F = 60 payload bytes after the 4-byte
// block header) and the continuation page payload tail
// (startOff = byte after the in-page list terminator).
type bupReadChainEntry struct {
	blockIdx uint16
	startOff uint32
	endOff   uint32
}

// bupBuildReadChain reconstructs the ordered list of (block, byte
// range) tuples that make up a file's on-disk payload, given the
// file's directory-entry block index. Both BUP_Read (slot 5) and
// BUP_Verify (slot 8) consume this chain in the same order: Read
// writes to the caller's buffer, Verify byte-compares against it.
//
// Per docs/bios/backup_library.md "Read chain assembly", the
// algorithm is single-level BFS over the in-entry list. Walk the
// in-entry slots in order. For each slot:
//   - If the named block is a continuation page (sentinel +
//     valid first index), parse its 30-slot list. If terminated
//     within the page, append a payload tail entry. Then enqueue
//     each cont-listed block index (FIFO).
//   - Otherwise it is a data block: append (block, +4, +64).
//
// After the in-entry slots are processed, the queue still holds
// cont-listed indices to process; each is a data block (cont pages
// never name other cont pages per inspection of cart2.srm) so they
// each contribute 60 bytes at +$04..+$3F.
//
// Final chain order:
//
//	[in-entry cont page tails, in in-entry order]
//	[in-entry data blocks, in in-entry order]
//	[cont-listed data blocks, in cont-page order]
//
// This single algorithm covers the three layouts (pure in-entry,
// single-cont, multi-cont).
func bupBuildReadChain(bus *Bus, entry int) []bupReadChainEntry {
	inEntryOff := entry*bupBlockSize + bupDirListOff
	seed, _ := bupParseExtentList(bus.backup, inEntryOff, bupDirListWordSlots)

	var chain []bupReadChainEntry
	var contListed []uint16

	// Phase 1: walk the in-entry list. Each in-entry block is either a
	// continuation page (multi-cont layout) or a data block. Cont-page
	// detection looks at the block's first 4 bytes ($00 00 00 00) AND
	// its first 16-bit index slot being in valid range, so data blocks
	// whose payload bytes happen to start with zeros (e.g., NIGHTS
	// blocks with all-zero header at +$00..+$03) are not misclassified
	// — their first index slot is either zero or out-of-range.
	for _, idx := range seed {
		if !bupValidBlockIdx(idx) {
			continue
		}
		if !bupIsContinuationPage(bus, int(idx)) {
			chain = append(chain, bupReadChainEntry{idx, bupBlockHeaderSize, uint32(bupBlockSize)})
			continue
		}
		// Continuation page: parse its 30-slot list (terminated by
		// $0000 or any out-of-range index), record the payload tail
		// if any, and collect its indices.
		contBase := int(idx) * bupBlockSize
		termIdx := -1
		var contIndices []uint16
		for i := 0; i < bupContWordSlots; i++ {
			off := contBase + bupContPayloadOff + i*2
			v := bupBE16(bus.backup, off)
			if !bupValidBlockIdx(v) {
				termIdx = i
				break
			}
			contIndices = append(contIndices, v)
		}
		if termIdx >= 0 {
			tailStart := uint32(bupContPayloadOff) + uint32(termIdx+1)*2
			if tailStart < uint32(bupBlockSize) {
				chain = append(chain, bupReadChainEntry{idx, tailStart, uint32(bupBlockSize)})
			}
		}
		contListed = append(contListed, contIndices...)
	}

	// Phase 2: append all cont-listed blocks as data blocks. Per the
	// doc, continuation pages never name other continuation pages, so
	// each cont-listed index is a data block — no re-check needed. (The
	// re-check would be wrong: it can false-positive on data blocks
	// whose +$04..+$05 payload bytes happen to form a valid block index,
	// as observed for MK-81020.srm NIGHTS_01 block 27 which holds the
	// game-side word $0020 = 32 at that offset.)
	for _, idx := range contListed {
		if !bupValidBlockIdx(idx) {
			continue
		}
		chain = append(chain, bupReadChainEntry{idx, bupBlockHeaderSize, uint32(bupBlockSize)})
	}
	return chain
}

// bupIsContinuationPage reports whether the block at blockIdx is a
// continuation page. A continuation page must have:
//   - $0000 $0000 sentinel at +$00..+$03
//   - a first index slot at +$04..+$05 that's a VALID block index
//     (in range [bupFirstDirBlock, bupTotalBlocks)). Range-checking
//     the first index distinguishes a real continuation chain entry
//     from payload data that happens to start with zeros: NIGHTS
//     save data like `00 00 00 00 08 08 ...` has $0808 = 2056 at
//     +$04 which is OUT OF BLOCK RANGE → payload, not continuation.
//
// A zero first-index slot also rejects (empty continuation = payload
// with all-zero data).
func bupIsContinuationPage(bus *Bus, blockIdx int) bool {
	base := blockIdx * bupBlockSize
	if base+6 > len(bus.backup) {
		return false
	}
	if bupBE16(bus.backup, base) != 0 || bupBE16(bus.backup, base+2) != 0 {
		return false
	}
	return bupValidBlockIdx(bupBE16(bus.backup, base+4))
}

// bupEntryBlocks walks the directory entry's block list and returns:
//
//   - payload: in-order list of blocks that hold file data (each
//     contributes up to one block's worth of payload bytes).
//   - claimed: full list of blocks consumed by the file (payload +
//     continuation pages). The dir entry block itself is NOT included.
//
// Traversal: read the in-entry list at +$22..+$3F. If the list is
// terminated within the 15 slots (a single $0000 word), every
// listed block is a payload page — no continuation lookup. If all
// 15 slots are filled (no terminator), in-entry slots can be cont
// pages or data blocks (multi-cont layout); bupExpandBlockList
// does the two-phase walk.
//
// Limiting continuation-page detection to the overflow case avoids
// mis-classifying a payload block whose first 4 bytes happen to be
// zero — HLE-written small files terminate in-entry, so their payload
// blocks are never inspected for a sentinel.
func bupEntryBlocks(bus *Bus, dirBlockIdx int) (payload, claimed []uint16) {
	inEntryOff := dirBlockIdx*bupBlockSize + bupDirListOff
	seed, terminated := bupParseExtentList(bus.backup, inEntryOff, bupDirListWordSlots)
	if terminated {
		out := append([]uint16(nil), seed...)
		return out, append([]uint16(nil), seed...)
	}
	return bupExpandBlockList(bus, seed)
}

// bupExpandBlockList walks the block graph rooted at `seed` using
// the same two-phase algorithm as bupBuildReadChain. See
// feedback_bup_read_chain_phase_split.md for why naive BFS is
// wrong (false-positive cont detection on data blocks whose
// payload bytes happen to form a valid block index).
//
// Phase 1 walks `seed` (the in-entry list). Each block is either a
// continuation page (in-entry slot before the cont/data boundary)
// or a data block. Cont pages contribute themselves to `claimed`
// AND collect their listed indices for Phase 2; data blocks
// contribute to both `payload` and `claimed`.
//
// Phase 2 appends every cont-listed index unconditionally to both
// `payload` and `claimed` — per spec, cont pages never name other
// cont pages, so no re-check needed.
func bupExpandBlockList(bus *Bus, seed []uint16) (payload, claimed []uint16) {
	var contListed []uint16
	visited := make(map[uint16]bool)
	addClaimed := func(idx uint16) bool {
		if !bupValidBlockIdx(idx) || visited[idx] {
			return false
		}
		visited[idx] = true
		claimed = append(claimed, idx)
		return true
	}

	// Phase 1: in-entry slots can be cont pages or data blocks.
	for _, idx := range seed {
		if !addClaimed(idx) {
			continue
		}
		if bupIsContinuationPage(bus, int(idx)) {
			base := int(idx) * bupBlockSize
			more, _ := bupParseExtentList(bus.backup, base+bupContPayloadOff, bupContWordSlots)
			contListed = append(contListed, more...)
			continue
		}
		payload = append(payload, idx)
	}

	// Phase 2: cont-listed indices are always data blocks. Add to
	// claimed + payload without re-checking for cont-page status.
	for _, idx := range contListed {
		if len(claimed) >= bupMaxFileDataBlocks {
			break
		}
		if !addClaimed(idx) {
			continue
		}
		payload = append(payload, idx)
	}
	return payload, claimed
}

// bupReadDirEntry parses a directory entry's metadata (excluding
// the block list). All multi-byte fields are big-endian.
type bupDirEntry struct {
	status   uint32
	filename [11]byte
	language uint8
	comment  [10]byte
	date     uint32
	datasize uint32
}

func bupReadDirEntry(bus *Bus, blockIdx int) bupDirEntry {
	off := blockIdx * bupBlockSize
	var e bupDirEntry
	e.status = bupBE32(bus.backup, off)
	copy(e.filename[:], bus.backup[off+4:off+15])
	e.language = bus.backup[off+15]
	copy(e.comment[:], bus.backup[off+16:off+26])
	e.date = bupBE32(bus.backup, off+0x1A)
	e.datasize = bupBE32(bus.backup, off+0x1E)
	return e
}

// bupWalkDir iterates every in-use directory entry, calling fn with
// the block index of each. Stops early when fn returns false. Skips
// blocks 0 (header) and 1 (reserved). Admin/continuation blocks have
// status word $00000000 (top bit clear) and are not iterated as dir
// entries — but bupUsedBlocks tracks them via the entry-walk path.
func bupWalkDir(bus *Bus, fn func(blockIdx int) bool) {
	for blk := bupFirstDirBlock; blk < bupTotalBlocks; blk++ {
		if !bupEntryInUse(bus, blk) {
			continue
		}
		if !fn(blk) {
			return
		}
	}
}

// bupFindEntry searches the directory for a file whose 11-byte
// filename matches name. Returns the dir entry block index (>=
// bupFirstDirBlock) on success, or -1 if no entry matches.
func bupFindEntry(bus *Bus, name [11]byte) int {
	result := -1
	bupWalkDir(bus, func(blockIdx int) bool {
		if bupFilenameEqual(bupReadFilename(bus, blockIdx), name) {
			result = blockIdx
			return false
		}
		return true
	})
	return result
}

// bupUsedBlocks returns a bitset of every block claimed by an
// in-use directory entry, plus blocks 0 (header) and 1 (reserved).
// "Claimed" = dir entry block itself + every block in the entry's
// expanded block list (continuation pages + payload pages).
func bupUsedBlocks(bus *Bus) []bool {
	used := make([]bool, bupTotalBlocks)
	used[bupHeaderBlock] = true
	used[bupReservedBlock] = true
	bupWalkDir(bus, func(blockIdx int) bool {
		used[blockIdx] = true
		_, claimed := bupEntryBlocks(bus, blockIdx)
		for _, idx := range claimed {
			if int(idx) < bupTotalBlocks {
				used[idx] = true
			}
		}
		return true
	})
	return used
}

// bupCountUsedBlocks counts blocks marked in-use across all dir
// entries (including admin + data). Equivalent to summing the
// bupUsedBlocks bitset.
func bupCountUsedBlocks(bus *Bus) int {
	used := bupUsedBlocks(bus)
	n := 0
	for _, u := range used {
		if u {
			n++
		}
	}
	return n
}

// bupAllocBlocks scans for n free blocks and returns their indices
// (in ascending order). Returns nil if not enough free space.
// Blocks 0 (header) and 1 (reserved) are never returned.
func bupAllocBlocks(bus *Bus, n int) []uint16 {
	if n <= 0 {
		return []uint16{}
	}
	used := bupUsedBlocks(bus)
	free := make([]uint16, 0, n)
	for blk := bupFirstDirBlock; blk < bupTotalBlocks && len(free) < n; blk++ {
		if !used[blk] {
			free = append(free, uint16(blk))
		}
	}
	if len(free) < n {
		return nil
	}
	return free
}

// bupIsInternal reports whether the R4 driver port selector targets
// the standard internal backup RAM (mode 0, $20180000) — the only
// device the HLE models with a backing store.
//
// R4 dispatch from the SDK BUP library wrapper:
//
//	R4 == 0: SDK device 0 (internal) — mode 0, RAM at $20180000
//	R4 == 1: SDK device 2 (serial)   — mode 1, alt region $24000000
//	R4 == 2: SDK device 1 (cart)     — external A-bus cart protocol
//
// Per docs/bios/backup_library.md Slot 3, mode 0 and mode 1 are
// distinct: mode 0 magic-scans $20180000, mode 1 magic-scans
// $24000000. The HLE doesn't map the alt region, so mode 1 finds no
// magic and would return BUP_UNFORMAT just like real BIOS does for
// an unattached serial device.
//
// We must return only R4 == 0 as "internal" — if we served R4 == 1
// from the same backup RAM, BR's SDK enumerates all devices and
// finds the same save on two of them, refusing to load it.
func bupIsInternal(r4 uint32) bool {
	return r4 == 0
}

// hlePerDriverSlot1Service implements BUP_SelPart.
//
// SDK function: Sint32 BUP_SelPart(Uint32 device, Uint16 num)
//
//	R4 = device. The real BIOS body short-circuits to R0 = 0
//	     for any R4 != 2 (non-cart devices don't need partition
//	     selection).
//	R4 == 2 selects the external A-bus cart. With no cart
//	     present (port-2 marker zero - set by
//	     writePerDriverTable), the body returns R0 = 1 (BUP_NON).
//	R5 = num (partition number). Not consumed by the HLE since
//	     only the cart path uses it and erings does not model a
//	     cart.
//
// Matches docs/bios/backup_library.md Slot 1 (+$02C4) for the
// driver_base+$56 = 0 (no-cart) configuration.
func hlePerDriverSlot1Service(cpu *sh2.CPU, bus *Bus) {
	_ = bus
	r := cpu.Registers()
	if r.R[4] != 2 {
		cpu.SetReg(0, bupOK)
		return
	}
	cpu.SetReg(0, bupNon)
}

// hlePerDriverSlot3Service implements BUP_Stat.
//
// SDK function: Sint32 BUP_Stat(Uint32 device, Uint32 datasize, BupStat *stat)
//
// The same body is BSR'd as a "validate device + load directory
// state" prelude by slots 2/4/5/6/7/8. Prelude callers pass
// R5 = 0 (throwaway datasize) and R6 = stack-local; the body
// still fills the 24-byte BupStat there but the caller ignores it.
//
// Return codes (per disasm of +$0672 MOV #N,R0 sites 0, 1, 2, 3, -1):
//
//	R4 != 0          -> R0 = 1 (BUP_NON; HLE does not model
//	                            cart/serial devices)
//	header missing   -> R0 = 2 (BUP_UNFORMAT)
//	otherwise        -> R0 = 0 (BupStat filled at *R6)
//
// Real BIOS also emits 3 (BUP_WRITE_PROTECT, cart-status `$23`)
// and -1 (generic) on the cart path; HLE never reaches those
// because it short-circuits non-internal devices to BUP_NON.
//
// HLE does not maintain driver-state caching; each subsequent
// BUP_* call re-checks the header itself.
func hlePerDriverSlot3Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	if !bupIsInternal(r.R[4]) {
		// Real BIOS slot 3 R4!=0 early-out does NOT touch the caller's
		// R6 buffer (trace shows R6 still holds prior call data on
		// return). Leaving R6 unmodified matches that behavior.
		cpu.SetReg(0, bupNon)
		return
	}
	if !bupCheckFormat(bus) {
		cpu.SetReg(0, bupUnformat)
		return
	}
	// Slot 3 R4==0 success path writes the full 24-byte BupStat-shape
	// to the caller's R6 buffer AND populates the driver state fields
	// the BIOS sets up before/after the dir scan. Real BIOS:
	//   +$0768: *R6+0  = $00008000 (totalsize)
	//   +$076C: *R6+4  = $0200     (totalblock)
	//   +$0770: *R6+8  = 64        (blocksize)
	//   +$09B0: *R6+C  = freesize
	//   +$099E: *R6+10 = freeblock
	//   +$09D0: *R6+14 = datanum
	// Driver state (mem.L at driver_base+offset):
	//   +$30 = $20180000 (BUP RAM cache-through base)
	//   +$34 = 0          (mode 0, no IRQ mask)
	//   +$38 = $0200      (512 dir entries)
	//   +$3C = 64         (block size)
	//   +$44 = 2          (partition index)
	//   +$4C = $20180080  (first dir entry address)
	out := r.R[6]
	if out != 0 {
		bupFillStat(bus, out, r.R[5])
	}
	bupSetInternalDriverState(bus)
	cpu.SetReg(0, bupOK)
}

// bupSetInternalDriverState populates the driver state fields that
// real BIOS slot 3 R4=0 path writes during the standard internal-RAM
// setup. Every internal-RAM BUP slot transitively calls slot 3 in
// real BIOS, so the state is always populated whenever any BUP op on
// the internal device runs. The HLE doesn't dispatch slot 3 from
// slot 5/7, so we have to set the same state ourselves.
//
// Also populates the per-entry working buffer at driver_base+$78
// (pointer at driver_base+$2C) with one record per in-use directory
// entry. Real BIOS slot 3 R4!=0 walk does this (backup_library.md
// Slot 3 R4!=2 path step 7: "for each entry ... copies metadata
// bytes from RAM to caller's per-entry state"). BR's BUP_Read SDK
// wrapper reads from this area to locate the entry's metadata
// before invoking slot 5, so an empty buffer makes BR abort the
// load.
func bupSetInternalDriverState(bus *Bus) {
	driver := perDriverBase(bus)
	if driver == 0 {
		return
	}
	bus.Write32(driver+0x30, 0x20180000) // BUP RAM cache-through base
	bus.Write32(driver+0x34, 0)          // mode 0 (no IRQ mask)
	bus.Write32(driver+0x38, 0x00000200) // 512 dir entries
	bus.Write32(driver+0x3C, 0x00000040) // 64-byte blocks
	bus.Write32(driver+0x40, 0x00000080) // per-entry RAM stride (2 * $3C)
	bus.Write32(driver+0x44, 0x00000002) // partition index 2
	bus.Write32(driver+0x4C, 0x20180080) // first dir entry
	bupPopulatePerEntryBuffer(bus, driver)
}

// bupPopulatePerEntryBuffer fills the per-entry working buffer
// starting at driver_base+$78 with one 128-byte record per in-use
// directory entry. Each record holds the file's full BLOCK LIST as
// 16-bit BE indices terminated by a single $0000 word followed by
// a per-entry bus-address pointer table.
//
// Per side-by-side trace with real BIOS, the buffer layout for a
// 1856-byte save (dir block 2 + continuation block 3 + payload
// blocks 4..34, total 33 blocks):
//
//	+$78..+$BA  block list: $0002 $0003 $0004 ... $0021 $0022
//	+$BA..+$BB  $0000 terminator
//	+$BC..+$FF  trailing scratch
//
// This is the format BR's BUP_Read SDK wrapper reads to locate
// the file's blocks before invoking slot 5.
//
// Also writes driver_base+$50 = the FIRST matched entry's block
// index (per "matched entry index" doc — slot 3/7 +$1434 hit).
func bupPopulatePerEntryBuffer(bus *Bus, driverBase uint32) {
	const stride = uint32(128)
	const maxSlots = 127 // (16 KB - $78) / 128 ≈ 127

	bufBase := driverBase + perDriverPerEntryBuffer
	idx := uint32(0)
	firstMatched := -1
	bupWalkDir(bus, func(blockIdx int) bool {
		if idx >= maxSlots {
			return false
		}
		if firstMatched < 0 {
			firstMatched = blockIdx
		}
		packet := bufBase + idx*stride

		// Zero the full slot first so trailing bytes are clean.
		for i := uint32(0); i < stride; i++ {
			bus.Write8(packet+i, 0)
		}

		// Write the full block list: dir entry block first, then
		// every claimed block (continuation pages + payload pages)
		// from bupEntryBlocks.
		_, claimed := bupEntryBlocks(bus, blockIdx)
		fullList := make([]uint16, 0, 1+len(claimed))
		fullList = append(fullList, uint16(blockIdx))
		fullList = append(fullList, claimed...)
		listOff := uint32(0)
		for _, b := range fullList {
			if listOff+2 > 0x42 { // leave room before pointer table at +$44
				break
			}
			bus.Write16(packet+listOff, b)
			listOff += 2
		}
		// $0000 terminator (already zero from the wipe above, but
		// write explicitly for clarity).
		bus.Write16(packet+listOff, 0)

		// Write bus pointers for the LAST 15 blocks of the file at
		// +$44..+$7F. Each pointer = $20180000 + block_idx * 128
		// (bus address of the block's start in BUP RAM cache-through
		// alias). Real BIOS stages these for fast access to the
		// file's later blocks during Read.
		const numPtrs = 15
		ptrStart := 0
		if len(fullList) > numPtrs {
			ptrStart = len(fullList) - numPtrs
		}
		ptrList := fullList[ptrStart:]
		for i, b := range ptrList {
			if i >= numPtrs {
				break
			}
			ptr := uint32(0x20180000) + uint32(b)*128
			bus.Write32(packet+0x44+uint32(i)*4, ptr)
		}

		idx++
		return true
	})

	// driver_base+$50 = matched entry block index. Real BIOS sets
	// this on a +$1434 filename hit (BUP_Dir's per-entry validate).
	// Write the first in-use entry's block index so the SDK has
	// something to reference. If no entries, leave at 0.
	if firstMatched >= 0 {
		bus.Write32(driverBase+0x50, uint32(firstMatched))
	} else {
		bus.Write32(driverBase+0x50, 0)
	}
}

// bupFillStat writes a 24-byte BupStat to dst given a hypothetical
// candidate file datasize.
//
// Formulas match real BIOS slot 3 disasm at +$09A0..+$09D0
// (backup_library.md Slot 3 R4!=2 path Finalize):
//
//	effBytes        = blocksize - 6 = 58
//	freesize        = freeblock * effBytes - 30
//	blocks_per_file = (datasize + 29) / effBytes + 1
//	datanum         = freeblock / blocks_per_file
//
// The +29 in blocks_per_file is the dir-entry overhead the disasm
// adds (`ADD #29,R1` at +$09BC). The 6-byte per-block overhead
// reflects real BIOS reserving 6 bytes per data block. The -30 bytes
// in freesize covers a fixed format overhead. Both BupDir.blocksize
// (in slot 7) and BupStat.datanum/freesize use the same effBytes
// denominator so the SDK's cross-checks see consistent values.
//
// If caller's R5 looks like a pointer (>= $01000000, beyond any
// sane save size), treat it as "no candidate" and use R5 = 0 so
// blocks_per_file = 1 and datanum = freeblock (matches slot 5
// internally calling slot 3 with R5 = 0).
func bupFillStat(bus *Bus, dst uint32, datasize uint32) {
	used := bupCountUsedBlocks(bus)
	freeBlocks := bupTotalBlocks - used
	if freeBlocks < 0 {
		freeBlocks = 0
	}
	effectiveDS := datasize
	if datasize >= 0x01000000 {
		effectiveDS = 0
	}
	effBytes := uint32(bupBlockSize - 6) // 58
	blocksPerFile := (effectiveDS+29)/effBytes + 1
	if blocksPerFile == 0 {
		blocksPerFile = 1
	}
	datanum := uint32(freeBlocks) / blocksPerFile
	freesize := int(freeBlocks)*int(effBytes) - 30
	if freesize < 0 {
		freesize = 0
	}
	bus.Write32(dst+0x00, uint32(bupTotalBlocks*bupBlockSize))
	bus.Write32(dst+0x04, uint32(bupTotalBlocks))
	bus.Write32(dst+0x08, uint32(bupBlockSize))
	bus.Write32(dst+0x0C, uint32(freesize))
	bus.Write32(dst+0x10, uint32(freeBlocks))
	bus.Write32(dst+0x14, datanum)
}

// daysBeforeMonth holds the cumulative days-before-month-N table
// for a non-leap year. Indexed by (month - 2) when month is in
// [2,13). Matches the +$1D96 table {31, 59, 90, 120, 151, 181, 212,
// 243, 273, 304, 334} in the disassembled BIOS driver.
var daysBeforeMonth = [11]uint32{
	31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334,
}

// hlePerDriverSlot5Service implements BUP_Read.
//
// SDK function: Sint32 BUP_Read(Uint32 device, Uint8 *fname, Uint8 *data)
//
// Dispatched via `BUP_HK_READ(device, fname, data)` at
// `BUP_VECTOR_ADDRESS + 20` per sega_bup.h. Looks up the file
// named by R5 in the directory, then copies datasize bytes from
// its data blocks to *R6.
//
// Return codes (per disasm of +$135A MOV #N,R0 sites 0, 1, 2, 3, 5, -1):
//
//	R4 != 0 (non-internal device) -> R0 = 1 (BUP_NON)
//	format magic missing          -> R0 = 2 (BUP_UNFORMAT)
//	filename invalid / not found  -> R0 = 5 (BUP_NOT_FOUND)
//	success                       -> R0 = 0, *R6 filled with datasize bytes
//
// Real BIOS also emits 3 (BUP_WRITE_PROTECT, cart-status `$23`)
// and -1 (generic) on the cart path; HLE never reaches those
// because it short-circuits non-internal devices to BUP_NON.
func hlePerDriverSlot5Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	if !bupIsInternal(r.R[4]) {
		cpu.SetReg(0, bupNon)
		return
	}
	bupSetInternalDriverState(bus)
	if !bupCheckFormat(bus) {
		cpu.SetReg(0, bupUnformat)
		return
	}
	name, ok := bupCallerFilename(bus, r.R[5])
	if !ok {
		cpu.SetReg(0, bupNotFound)
		return
	}
	entry := bupFindEntry(bus, name)
	if entry < 0 {
		cpu.SetReg(0, bupNotFound)
		return
	}
	meta := bupReadDirEntry(bus, entry)
	dst := r.R[6]
	if dst == 0 {
		// Null destination: the file was found, just don't copy.
		// Caller-defensive (real BIOS would deref null); treat as
		// success since the file's existence has been confirmed.
		cpu.SetReg(0, bupOK)
		return
	}
	// Walk the on-disk chain (cont tail + per-data-block 60-byte
	// payload) and copy datasize bytes into the caller's buffer.
	// See bupBuildReadChain for the chain semantics.
	chain := bupBuildReadChain(bus, entry)
	remaining := uint32(meta.datasize)
	dstAddr := dst
	for _, rb := range chain {
		if remaining == 0 {
			break
		}
		if !bupValidBlockIdx(rb.blockIdx) {
			continue
		}
		if rb.startOff >= rb.endOff {
			continue
		}
		avail := rb.endOff - rb.startOff
		chunk := avail
		if chunk > remaining {
			chunk = remaining
		}
		base := int(rb.blockIdx) * bupBlockSize
		for i := uint32(0); i < chunk; i++ {
			bus.Write8(dstAddr+i, bus.backup[base+int(rb.startOff)+int(i)])
		}
		dstAddr += chunk
		remaining -= chunk
	}
	cpu.SetReg(0, bupOK)
}

// hlePerDriverSlot2Service implements BUP_Format.
//
// SDK function: Sint32 BUP_Format(Uint32 device)
//
//	R4 = device. Only the internal device is modeled.
//	R5, R6 = not consumed by the SDK API.
//
// Per docs/bios/backup_library.md Slot 2 (+$03AA), the body writes
// the 4x "BackUpRam Format" magic at bytes 0..63 of backup RAM,
// zeros block 1 (the reserved region at 64..127), and clears every
// directory entry status word. The HLE uses formatBackupRAM (which
// writes the header and zeros bytes 64..end) to achieve the same
// post-format state.
//
// Return codes (per disasm of +$03AA, MOV #N,R0 sites are 0/1/3/8/-1):
//
//	R4 != 0 -> R0 = 1 (BUP_NON; external cart / serial not modeled)
//	otherwise -> R0 = 0 (format complete)
func hlePerDriverSlot2Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	if !bupIsInternal(r.R[4]) {
		cpu.SetReg(0, bupNon)
		return
	}
	formatBackupRAM(bus.backup)
	cpu.SetReg(0, bupOK)
}

// hlePerDriverSlot6Service implements BUP_Delete.
//
// SDK function: Sint32 BUP_Delete(Uint32 device, Uint8 *fname)
//
// Looks up the file named by R5 in the directory; if found, zeros
// the ENTIRE 64-byte dir-entry block — status word, filename,
// comment, language, date, datasize, and the in-entry block list.
// This matches the real-BIOS sub_17BA mode-1 erase path (the path
// NiGHTS's BUP_Init configures); see project_bup_delete_mode_dependent.md
// for the disasm grounding. The file's data blocks themselves are
// left as-is; they become free implicitly because the now-zeroed
// dir entry no longer claims them on the next free-block scan.
// Real-BIOS slot 6 R4!=2 path: slot 3 (Stat prelude), BSR +$1434
// (filename validate), BSR +$17BA (erase).
//
// Return codes (per disasm of +$16DC MOV #N,R0 sites 0, 1, 2, 3, 5, -1):
//
//	R4 != 0 (non-internal device) -> R0 = 1 (BUP_NON)
//	format magic missing          -> R0 = 2 (BUP_UNFORMAT)
//	filename invalid / not found  -> R0 = 5 (BUP_NOT_FOUND)
//	success                       -> R0 = 0 (entry freed)
//
// Real BIOS also emits 3 (BUP_WRITE_PROTECT, cart-status `$23`)
// and -1 (generic) on the cart path; HLE never reaches those
// because it short-circuits non-internal devices to BUP_NON.
func hlePerDriverSlot6Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	if !bupIsInternal(r.R[4]) {
		cpu.SetReg(0, bupNon)
		return
	}
	bupSetInternalDriverState(bus)
	if !bupCheckFormat(bus) {
		cpu.SetReg(0, bupUnformat)
		return
	}
	name, ok := bupCallerFilename(bus, r.R[5])
	if !ok {
		cpu.SetReg(0, bupNotFound)
		return
	}
	entry := bupFindEntry(bus, name)
	if entry < 0 {
		cpu.SetReg(0, bupNotFound)
		return
	}
	// Per sub_17BA disasm: real BIOS Delete clears bit 7 of the
	// status byte AND (mode-1 path at +$186E..+$1892) zeros bytes
	// +$04..+$3F of the dir entry — filename, comment, language,
	// date, datasize, and block list. NiGHTS's BUP_Init configures
	// mode 1, so the full zero path is what real BIOS executes;
	// only zeroing the status word leaves the filename intact and
	// any code that scans by filename (the working buffer's
	// per-entry block list at driver_base+$78+, or game-side save
	// list) still sees the entry.
	off := entry * bupBlockSize
	for i := 0; i < bupBlockSize; i++ {
		bus.backup[off+i] = 0
	}
	cpu.SetReg(0, bupOK)
}

// hlePerDriverSlot8Service implements BUP_Verify.
//
// SDK function: Sint32 BUP_Verify(Uint32 device, Uint8 *fname, Uint8 *data)
//
// Looks up the file named by R5 in the directory; if found,
// byte-compares its on-disk payload against the caller's buffer
// at R6. Walks the same chain that BUP_Read does (continuation
// page tail first, then per-data-block 60 bytes at +$04..+$3F)
// so a Write/Verify round-trip succeeds on real-BIOS-written and
// HLE-written saves alike.
//
// Return codes (per disasm of +$1ACC MOV #N,R0 sites 0, 1, 2, 3, 5, 7, -1):
//
//	R4 != 0 (non-internal device) -> R0 = 1 (BUP_NON)
//	format magic missing          -> R0 = 2 (BUP_UNFORMAT)
//	filename invalid / not found  -> R0 = 5 (BUP_NOT_FOUND)
//	data ptr null / data mismatch -> R0 = 7 (BUP_NO_MATCH)
//	success (all bytes match)     -> R0 = 0
//
// Real BIOS also emits 3 (BUP_WRITE_PROTECT, cart-status `$23`)
// and -1 (generic) on the cart path; HLE never reaches those
// because it short-circuits non-internal devices to BUP_NON.
func hlePerDriverSlot8Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	if !bupIsInternal(r.R[4]) {
		cpu.SetReg(0, bupNon)
		return
	}
	bupSetInternalDriverState(bus)
	if !bupCheckFormat(bus) {
		cpu.SetReg(0, bupUnformat)
		return
	}
	name, ok := bupCallerFilename(bus, r.R[5])
	if !ok {
		cpu.SetReg(0, bupNotFound)
		return
	}
	entry := bupFindEntry(bus, name)
	if entry < 0 {
		cpu.SetReg(0, bupNotFound)
		return
	}
	meta := bupReadDirEntry(bus, entry)
	src := r.R[6]
	if src == 0 {
		// Null source: nothing to compare against, so the verify
		// trivially "fails". Verify differs from Read's null-dst
		// handling because Read can confirm existence without
		// reading, but Verify's whole purpose is the byte-compare.
		cpu.SetReg(0, bupNoMatch)
		return
	}
	chain := bupBuildReadChain(bus, entry)
	remaining := int(meta.datasize)
	srcAddr := src
	for _, rb := range chain {
		if remaining <= 0 {
			break
		}
		if !bupValidBlockIdx(rb.blockIdx) {
			cpu.SetReg(0, bupNoMatch)
			return
		}
		if rb.startOff >= rb.endOff {
			continue
		}
		avail := int(rb.endOff - rb.startOff)
		chunk := avail
		if chunk > remaining {
			chunk = remaining
		}
		base := int(rb.blockIdx) * bupBlockSize
		for i := 0; i < chunk; i++ {
			if bus.Read8(srcAddr+uint32(i)) != bus.backup[base+int(rb.startOff)+i] {
				cpu.SetReg(0, bupNoMatch)
				return
			}
		}
		srcAddr += uint32(chunk)
		remaining -= chunk
	}
	if remaining > 0 {
		cpu.SetReg(0, bupNoMatch)
		return
	}
	cpu.SetReg(0, bupOK)
}

// hlePerDriverSlot7Service implements BUP_Dir.
//
// SDK function: Sint32 BUP_Dir(Uint32 device, Uint8 *fname, Uint16 dirsize, BupDir *dir)
//
// Walks the directory and copies matching BupDir structs to the
// caller's R7 array, up to R6 (= dirsize) entries.
//
// R5 is the filename pattern. Per the doc, a NUL or `*` as the
// first byte is the wildcard "match all"; any other 11-byte
// pattern is an exact-match request.
//
// Return semantics (slot 7's R0 is a signed count, not an SDK
// error code):
//
//	R4 != 0 (non-internal device) -> R0 = 0 (no matches; per +$18E6
//	                                disasm, port-2 fast-path returns
//	                                0 when port-2 marker is zero)
//	format magic missing          -> R0 = 2 (BUP_UNFORMAT)
//	N matches, all fit in R6      -> R0 = N (positive count)
//	N matches, N > R6             -> R0 = -N (negative; only first
//	                                R6 entries written)
//
// BupDir struct on the caller side (34 bytes, per sega_bup.h):
//
//	+$00  filename[12]   ; on-disk filename + NUL
//	+$0C  comment[11]    ; on-disk comment + NUL
//	+$17  language
//	+$18  date (BE u32)
//	+$1C  datasize (BE u32)
//	+$20  blocksize (BE u16) — = (datasize + 29) / 58 + 1
//	                            per slot 7 disasm at +$1A6A..+$1A90
func hlePerDriverSlot7Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	if !bupIsInternal(r.R[4]) {
		// Slot 7 R4!=0 returns 0 matches. The R4=2 disasm at +$18E6
		// confirms the port-2 fast-path returns 0 when the marker is
		// zero. R4=1 alt-extended would magic-scan empty $24000000 and
		// also produce 0 matches.
		cpu.SetReg(0, bupOK)
		return
	}
	bupSetInternalDriverState(bus)
	if !bupCheckFormat(bus) {
		cpu.SetReg(0, bupUnformat)
		return
	}
	pattern, hasPattern := bupCallerFilename(bus, r.R[5])
	wildcard := !hasPattern || pattern[0] == 0 || pattern[0] == '*'
	maxEntries := int(uint16(r.R[6]))
	out := r.R[7]

	// Pre-zero the entire caller-provided BupDir array so that
	// unused slots show as "empty entry" (zero filename, zero
	// status). Otherwise BR would see stale data in the slots
	// past the match count and may misinterpret it as additional
	// (corrupted) entries.
	if out != 0 && maxEntries > 0 {
		for i := uint32(0); i < uint32(maxEntries)*bupDirOutStride; i++ {
			bus.Write8(out+i, 0)
		}
	}

	totalMatches := 0
	written := 0
	bupWalkDir(bus, func(blockIdx int) bool {
		fn := bupReadFilename(bus, blockIdx)
		if !wildcard && !bupFilenameEqual(fn, pattern) {
			return true
		}
		totalMatches++
		if out == 0 || written >= maxEntries {
			return true
		}
		meta := bupReadDirEntry(bus, blockIdx)
		base := out + uint32(written)*bupDirOutStride

		// filename[12]: 11 ASCII + NUL
		for i := 0; i < 11; i++ {
			bus.Write8(base+uint32(i), fn[i])
		}
		bus.Write8(base+11, 0)
		// comment[11]: 10 ASCII + NUL
		for i := 0; i < 10; i++ {
			bus.Write8(base+0x0C+uint32(i), meta.comment[i])
		}
		bus.Write8(base+0x16, 0)
		// language
		bus.Write8(base+0x17, meta.language)
		// date (BE u32)
		bus.Write32(base+0x18, meta.date)
		// datasize (BE u32)
		bus.Write32(base+0x1C, meta.datasize)
		// blocksize: real BIOS formula per slot 7 disasm
		// at +$1A6A..+$1A90: `(datasize + 29) / 58 + 1` using
		// integer (truncating) division. Verified by side-by-side
		// trace with real BIOS for NIGHTS_02 (datasize 12544 →
		// blocksize $D9 = 217). The naive `1 + ceil(datasize/58)`
		// is off-by-one for files where `datasize mod 58` falls in
		// [1, 28] because the +29 in the BIOS formula straddles the
		// 58-byte boundary differently than ceil.
		effBytes := uint32(bupBlockSize - 6) // 58
		blocks := (meta.datasize+29)/effBytes + 1
		bus.Write16(base+0x20, uint16(blocks))

		written++
		return true
	})

	if totalMatches > maxEntries {
		cpu.SetReg(0, uint32(-int32(totalMatches)))
		return
	}
	cpu.SetReg(0, uint32(totalMatches))
}

// hlePerDriverSlot4Service implements BUP_Write.
//
// SDK function: Sint32 BUP_Write(Uint32 device, BupDir *dir, Uint8 *data, Uint8 owsw)
//
// Parses the caller's BupDir struct at R5, validates header,
// looks up existing filename (honors owsw at R7), allocates free
// blocks, writes a fresh directory entry plus continuation page
// (if needed) plus payload to bus.backup in the layout that
// BUP_Read expects.
//
// Return codes (per disasm of +$0A78 MOV #N,R0 sites 0, 1, 2, 3, 4, 6):
//
//	R4 != 0 (non-internal device) -> R0 = 1 (BUP_NON)
//	format magic missing          -> R0 = 2 (BUP_UNFORMAT)
//	(cart path)                   -> R0 = 3 (BUP_WRITE_PROTECT, not reached in HLE)
//	not enough free blocks        -> R0 = 4 (BUP_NOT_ENOUGH_MEMORY)
//	file exists and owsw != 0     -> R0 = 6 (BUP_FOUND)
//	datasize exceeds max layout   -> R0 = 4 (BUP_NOT_ENOUGH_MEMORY;
//	                                 max file size = 58*(D+C)+28 with
//	                                 D=449, C=15 ≈ 26.9 KB)
//	success                       -> R0 = 0
//
// The BupDir struct R5 points at:
//
//	+$00  filename[12]   ; 11 ASCII + NUL
//	+$0C  comment[11]    ; 10 ASCII + NUL
//	+$17  language
//	+$18  date (BE u32)
//	+$1C  datasize (BE u32)
//	+$20  blocksize (BE u16; ignored on write — recomputed)
//
// On-disk format produced (matches what real BIOS Write produces,
// verified against cart.srm / cart2.srm samples + BUP_Read's
// inverse layout):
//
//   - Dir entry block: status word $80000000, filename, comment,
//     language, date, datasize at the documented offsets; the
//     block-list field at +$22..+$3F holds 16-bit BE block
//     indices. Pure in-entry (D ≤ 14, C = 0): D indices then a
//     $0000 terminator. Multi-cont (C ≥ 1): C cont-page indices
//     in slots 0..C-1 then (15 - C) in-entry data block indices
//     in slots C..14 (all 15 slots filled, no terminator).
//   - Continuation pages 0..C-2: 30 indices, no terminator,
//     no payload tail. Bytes +$00..+$03 are the $0000 $0000
//     sentinel; +$04..+$3F holds 30 block indices.
//   - Last continuation page (C-1): X = D + 15 - 29C indices
//     starting at +$04 followed by a $0000 terminator; bytes
//     from +$04+(X+1)*2 through +$3F are payload tail (FIRST
//     bytes of the file payload).
//   - Data block: bytes +$00..+$03 are a 4-byte header (real
//     BIOS writes zeros here; BUP_Read skips them); bytes
//     +$04..+$3F are up to 60 payload bytes.
//
// Total payload capacity per file:
//
//	Pure in-entry (C=0, D≤14):  D * 60 bytes      (max 840 B)
//	Multi-cont (C≥1):           58*(D + C) + 28   (max ~26.9 KB
//	                            at D=449, C=15)
func hlePerDriverSlot4Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	if !bupIsInternal(r.R[4]) {
		cpu.SetReg(0, bupNon)
		return
	}
	bupSetInternalDriverState(bus)
	if !bupCheckFormat(bus) {
		cpu.SetReg(0, bupUnformat)
		return
	}
	dirSrc := r.R[5]
	dataSrc := r.R[6]
	owsw := r.R[7]
	if dirSrc == 0 {
		// Real BIOS slot 4 doesn't have a null-pointer guard (the
		// SDK contract requires R5 to be a valid BupDir pointer)
		// and would deref into garbage. The HLE adds this guard to
		// avoid polluting backup RAM with a garbage-filename entry.
		// -1 is the slot 4 body's catch-all return code (cart-status
		// fall-through path; per +$0AFA disasm).
		cpu.SetReg(0, 0xFFFFFFFF)
		return
	}
	var name [11]byte
	for i := 0; i < 11; i++ {
		name[i] = bus.Read8(dirSrc + uint32(i))
	}
	datasize := bus.Read32(dirSrc + 0x1C)

	// Decide layout: pure in-entry vs multi-page continuation. The
	// algorithm is documented in detail in docs/bios/backup_library.md
	// "Write layout". Summary:
	//
	//   D = number of data blocks holding the 60-byte payload chunks
	//   C = number of continuation pages
	//
	//   Pure in-entry (C == 0): up to D = 14 data blocks fit directly
	//   in the dir-entry's 15-slot block list (14 data + 1 $0000
	//   terminator). Max payload = 14 * 60 = 840 bytes.
	//
	//   Multi-cont (C >= 1): the in-entry list holds C cont page
	//   indices in slots 0..C-1 and (15-C) data block indices in
	//   slots C..14. The first C-1 cont pages each list 30 data
	//   blocks (no terminator); the last lists the remainder plus a
	//   $0000 terminator plus a payload tail of (28 + 58C - 2D)
	//   bytes. Capacity = 58 * (D + C) + 28 bytes.
	maxPureInEntry := bupDirListWordSlots - 1 // 14

	var D, C int
	switch {
	case datasize == 0:
		D, C = 0, 0
	case int(datasize) <= maxPureInEntry*bupBlockPayload:
		// Pure in-entry: D data blocks + $0000 terminator fit in 15 slots.
		D = int((datasize + uint32(bupBlockPayload) - 1) / uint32(bupBlockPayload))
		C = 0
	default:
		// Iterate D upward until the capacity formula is satisfied.
		// C = ceil((D - 14) / 29) is the minimum cont-page count for D
		// data blocks given that earlier cont pages must be exactly full.
		// We start at D = maxPureInEntry (=14) because D=14 with C=1 is
		// a valid layout (1 cont page at slot 0 + 14 data blocks at
		// slots 1..14; cont page has 0 indices + $0000 terminator +
		// 58-byte payload tail) that uses one fewer block than D=15, C=1.
		found := false
		for d := maxPureInEntry; d <= bupTotalBlocks; d++ {
			excess := d - maxPureInEntry                                  // d - 14
			c := (excess + bupContWordSlots - 2) / (bupContWordSlots - 1) // ceil(excess/29)
			if c < 1 {
				c = 1
			}
			if c > bupDirListWordSlots {
				break // can't fit C cont-page indices in 15-slot in-entry list
			}
			capacity := 58*(d+c) + 28
			if capacity >= int(datasize) {
				D, C = d, c
				found = true
				break
			}
		}
		if !found {
			cpu.SetReg(0, bupNotEnoughMemory)
			return
		}
	}
	totalNew := 1 + C + D // dir + cont pages + data blocks

	// If file already exists: honor owsw. R7 == 0 -> error; non-
	// zero -> delete existing first (so free-block math sees the
	// freed space).
	if existing := bupFindEntry(bus, name); existing >= 0 {
		// Per slot 4 disasm at +$B40..+$B54: owsw == 0 means
		// OVERWRITE (BSR sub_17BA to erase the matched entry, then
		// fall through to creation). owsw != 0 means DON'T
		// overwrite (return BUP_FOUND). This is the opposite of
		// what the parameter name "owsw" (overwrite-switch)
		// suggests — read it as "no-overwrite switch".
		if owsw != 0 {
			cpu.SetReg(0, bupFound)
			return
		}
		off := existing * bupBlockSize
		for i := 0; i < bupBlockSize; i++ {
			bus.backup[off+i] = 0
		}
	}

	allocated := bupAllocBlocks(bus, totalNew)
	if allocated == nil {
		cpu.SetReg(0, bupNotEnoughMemory)
		return
	}
	dirBlock := int(allocated[0])
	contPages := allocated[1 : 1+C]
	inEntryDataCount := 15 - C
	if C == 0 {
		inEntryDataCount = D // pure in-entry: all D data blocks
	}
	inEntryData := allocated[1+C : 1+C+inEntryDataCount]
	contListedData := allocated[1+C+inEntryDataCount:]

	// Zero all data blocks (in-entry + cont-listed). Real BIOS leaves
	// the 4-byte block header at +$00..+$03 as zero and BUP_Read skips
	// those bytes. Zeroing the full block also zero-pads beyond the
	// payload end if datasize doesn't fill the last block.
	for _, blk := range inEntryData {
		bOff := int(blk) * bupBlockSize
		for i := 0; i < bupBlockSize; i++ {
			bus.backup[bOff+i] = 0
		}
	}
	for _, blk := range contListedData {
		bOff := int(blk) * bupBlockSize
		for i := 0; i < bupBlockSize; i++ {
			bus.backup[bOff+i] = 0
		}
	}

	// Write each continuation page. Cont pages 0..C-2 carry 30 indices
	// (no terminator, no payload tail). Cont page C-1 (the last) holds
	// X = D + 15 - 29C indices followed by a single $0000 terminator,
	// then bytes from offset 4 + (X+1)*2 to +$40 are payload tail.
	var lastContTailOff, lastContTailEnd int
	for j, contBlk := range contPages {
		cOff := int(contBlk) * bupBlockSize
		for i := 0; i < bupBlockSize; i++ {
			bus.backup[cOff+i] = 0
		}
		// $0000 $0000 sentinel at +$00..+$03 already from zero-fill.
		if j < C-1 {
			// Full cont page: 30 indices, no terminator.
			start := j * (bupContWordSlots) // = j * 30
			for i := 0; i < bupContWordSlots; i++ {
				bupWriteBE16(bus.backup, cOff+bupContPayloadOff+i*2, contListedData[start+i])
			}
		} else {
			// Last cont page: write X remaining indices + $0000 terminator
			// (which the zero-fill already placed at offset bupContPayloadOff+X*2).
			X := D + 15 - 29*C
			start := (C - 1) * bupContWordSlots
			for i := 0; i < X; i++ {
				bupWriteBE16(bus.backup, cOff+bupContPayloadOff+i*2, contListedData[start+i])
			}
			lastContTailOff = cOff + bupContPayloadOff + (X+1)*2
			lastContTailEnd = cOff + bupBlockSize
		}
	}

	// Write the directory entry header.
	off := dirBlock * bupBlockSize
	bupWriteBE32(bus.backup, off, 0x80000000)
	copy(bus.backup[off+4:off+15], name[:])
	bus.backup[off+15] = bus.Read8(dirSrc + 0x17) // language
	for i := 0; i < 10; i++ {
		bus.backup[off+16+i] = bus.Read8(dirSrc + 0x0C + uint32(i))
	}
	bupWriteBE32(bus.backup, off+0x1A, bus.Read32(dirSrc+0x18)) // date
	bupWriteBE32(bus.backup, off+0x1E, datasize)
	// Zero the block list area; unused slots will act as $0000
	// terminators (for pure in-entry) or stay zero where appropriate.
	for i := 0x22; i < bupBlockSize; i++ {
		bus.backup[off+i] = 0
	}
	listBase := off + bupDirListOff
	if C == 0 {
		// Pure in-entry: write D non-zero data block indices; trailing
		// slot acts as the single $0000 terminator from zero-fill.
		for i, idx := range inEntryData {
			bupWriteBE16(bus.backup, listBase+i*2, idx)
		}
	} else {
		// Multi-cont: slots 0..C-1 = cont page indices; slots C..14 =
		// in-entry data block indices. All 15 slots filled (unterminated)
		// so BUP_Read treats the in-entry blocks as cont pages where
		// applicable per the per-block sentinel check.
		for i, idx := range contPages {
			bupWriteBE16(bus.backup, listBase+i*2, idx)
		}
		for i, idx := range inEntryData {
			bupWriteBE16(bus.backup, listBase+(C+i)*2, idx)
		}
	}

	// Write payload bytes in BFS chain order (matches BUP_Read):
	//   1. Last continuation page's payload tail (file bytes [0..T-1])
	//      where T = lastContTailEnd - lastContTailOff.
	//   2. In-entry data blocks at slots C..14 (or 0..D-1 for pure
	//      in-entry), 60 bytes each at +$04..+$3F.
	//   3. Cont-listed data blocks in cont-page order (page 0's list
	//      then page 1's list ... then last page's list), 60 bytes each.
	remaining := int(datasize)
	srcAddr := dataSrc
	writeChunk := func(dst []byte, n int) {
		if dataSrc == 0 {
			return // leave destination zero-filled
		}
		for i := 0; i < n; i++ {
			dst[i] = bus.Read8(srcAddr + uint32(i))
		}
		srcAddr += uint32(n)
	}
	writeData := func(blk uint16) {
		if remaining <= 0 {
			return
		}
		chunk := bupBlockPayload
		if chunk > remaining {
			chunk = remaining
		}
		bOff := int(blk) * bupBlockSize
		writeChunk(bus.backup[bOff+bupBlockHeaderSize:bOff+bupBlockHeaderSize+chunk], chunk)
		remaining -= chunk
	}
	if C > 0 && remaining > 0 {
		tailCap := lastContTailEnd - lastContTailOff
		chunk := tailCap
		if chunk > remaining {
			chunk = remaining
		}
		if chunk > 0 {
			writeChunk(bus.backup[lastContTailOff:lastContTailOff+chunk], chunk)
			remaining -= chunk
		}
	}
	for _, blk := range inEntryData {
		writeData(blk)
	}
	for _, blk := range contListedData {
		writeData(blk)
	}
	cpu.SetReg(0, bupOK)
}

// hlePerDriverSlot10Service implements BUP_SetDate.
//
// SDK function: Uint32 BUP_SetDate(BupDate *date)
//
// Packs a 5-byte BupDate-shaped input at R4 (year, month, day,
// hour, minute - the 6th byte `week` is ignored, per the
// SetDate disasm which never reads R14+5) into a Uint32 count
// of minutes since 1980-01-01 00:00 returned in R0.
//
//	R4 = BupDate pointer (input)
//	R0 = packed Uint32 (output)
//
// Algorithm matches the +$1F64 BIOS routine
// (BSR `+$1D96` to populate the days-before-month table on
// stack; BSRF `+$355C` for signed year/4 division; BSRF
// `+$36B8` for the year % 4 remainder):
//
//	t1   = b0 / 4               ; quad-year cycle index
//	t2   = b0 % 4               ; year within quad
//	hash = t1 * 1461            ; days per 4-year cycle (= $05B5)
//	if t2 != 0:
//	    hash += t2 * 365 + 1    ; non-leap years + the prior leap day
//	if 2 <= b1 < 13:
//	    hash += daysBeforeMonth[b1 - 2]    ; non-leap cumulative
//	    if b1 > 2 and t2 == 0:             ; LEAP year past Feb:
//	        hash += 1                      ;   correct the table
//	hash += b2 - 1              ; day-of-month (1-indexed)
//	hash *= 1440                ; minutes per day (= $05A0)
//	hash += b3 * 60 + b4        ; hour + minute
//
// The `b1 > 2 and t2 == 0` branch matches the disasm at
// `+$1FC6: CMP/GT R1,R6` then `+$1FCA: TST R5,R5` /
// `+$1FCC: BF $1FD0`: `BF` after `TST R5,R5` falls through
// only when R5 == 0 (leap year), at which point `+$1FCE: ADD
// #1,R4` runs. The HLE previously had this branch inverted
// (`t2 != 0`), which would shift dates by one day for
// March-December in either direction (under in leap years,
// over in non-leap). Latent — only matters for SAVE.
//
// b0 = year-of-cycle (year - 1980), b1 = month (1-12),
// b2 = day-of-month (1-31), b3 = hour (0-23), b4 = minute (0-59).
func hlePerDriverSlot10Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	b0 := uint32(bus.Read8(r.R[4] + 0))
	b1 := uint32(bus.Read8(r.R[4] + 1))
	b2 := uint32(bus.Read8(r.R[4] + 2))
	b3 := uint32(bus.Read8(r.R[4] + 3))
	b4 := uint32(bus.Read8(r.R[4] + 4))

	t1 := b0 / 4
	t2 := b0 % 4

	hash := t1 * 0x05B5
	if t2 != 0 {
		hash += t2*0x016D + 1
	}
	if b1 >= 2 && b1 < 13 {
		hash += daysBeforeMonth[b1-2]
		if b1 > 2 && t2 == 0 {
			hash++
		}
	}
	hash += b2 - 1
	hash *= 0x05A0
	hash += b3*60 + b4
	cpu.SetReg(0, hash)
}

// hlePerDriverSlot9Service implements BUP_GetDate.
//
// SDK function: void BUP_GetDate(Uint32 pdate, BupDate *date)
//
// Unpacks a packed Uint32 date (minutes since 1980-01-01 00:00)
// into a 6-byte BupDate struct. The inverse of slot 10
// (BUP_SetDate). Per the +$1E70 disasm:
//
//	R4 = pdate (packed value, not a pointer)
//	R5 = output BupDate pointer
//
// No return value (SDK declares void); R5 == 0 short-circuits
// to a no-op write so a null caller pointer is harmless.
//
// BupDate layout (sega_bup.h):
//
//	+0  year       (year - 1980)
//	+1  month      (1-12)
//	+2  day        (1-31)
//	+3  time       (hour 0-23)
//	+4  min        (0-59)
//	+5  week       (0=Sun .. 6=Sat; 1980-01-01 was Tuesday)
//
// Algorithm matches +$1E70's disasm (BSRF targets `+$3610`,
// `+$3780`, BSR `+$1DC4`): divide by 1440 (= $05A0 minutes/day),
// divide minutes-in-day by 60, divide days by 1461 (= $05B5
// 4-year leap cycle), then leap-aware year/day-of-year split,
// then day-of-year → (month, day) via inline month-iteration
// (equivalent to the BIOS table walk through +$1D96's
// days-before-month constants). Week = (days + 2) mod 7.
func hlePerDriverSlot9Service(cpu *sh2.CPU, bus *Bus) {
	r := cpu.Registers()
	packed := r.R[4]
	out := r.R[5]
	if out == 0 {
		return
	}

	dayMinutes := packed % 1440
	totalDays := packed / 1440
	hour := dayMinutes / 60
	minute := dayMinutes % 60

	// 4-year (quad) cycle = 1461 days = 366 (leap) + 365*3.
	// Year 0 of each quad is the leap year (1980, 1984, ...).
	quad := totalDays / 1461
	daysInQuad := totalDays % 1461

	var yearInQuad uint32
	var dayInYear uint32
	if daysInQuad < 366 {
		yearInQuad = 0
		dayInYear = daysInQuad
	} else {
		afterLeap := daysInQuad - 366
		yearInQuad = 1 + afterLeap/365
		dayInYear = afterLeap % 365
	}
	year := quad*4 + yearInQuad

	isLeap := yearInQuad == 0
	month := uint32(12)
	dayOfMonth := uint32(0)
	cum := uint32(0)
	for m := uint32(1); m <= 12; m++ {
		var monthLen uint32
		switch m {
		case 2:
			if isLeap {
				monthLen = 29
			} else {
				monthLen = 28
			}
		case 4, 6, 9, 11:
			monthLen = 30
		default:
			monthLen = 31
		}
		if dayInYear < cum+monthLen {
			month = m
			dayOfMonth = dayInYear - cum + 1
			break
		}
		cum += monthLen
	}

	// 1980-01-01 (day 0) was a Tuesday. Sunday=0 → Tuesday=2.
	week := (totalDays + 2) % 7

	bus.Write8(out+0, uint8(year))
	bus.Write8(out+1, uint8(month))
	bus.Write8(out+2, uint8(dayOfMonth))
	bus.Write8(out+3, uint8(hour))
	bus.Write8(out+4, uint8(minute))
	bus.Write8(out+5, uint8(week))
}
