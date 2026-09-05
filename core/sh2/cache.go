// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// SH-2 on-chip cache (SH7604 hardware manual Section 8).
//
// 4 KB mixed instruction/data cache: 4-way set associative, 64 entries
// (sets), 16 bytes per line (Section 8.1, Figure 8.1). An address
// splits per Figure 8.2: A31-A29 select the partition (Section 8.3,
// Table 8.2), A28-A10 are the tag, A9-A4 the entry, A3-A0 the byte
// within the line.
//
// The cache is write-through with no write allocation (Section 8.4.2):
// writes always go to memory; the data array is updated only on a tag
// hit. Reads fill a whole line on a miss (Section 8.4.1). There is no
// snoop function (Section 8.5.3): writes by the other CPU or the DMAC
// do not update or invalidate this CPU's cache - software maintains
// coherency with cache-through accesses and purges, and erings models
// that staleness faithfully.
//
// Storage layout: cacheData (the same 4 KB the data array region
// exposes directly, Section 8.4.8 Figure 8.10) holds the lines
// way-major - way*1024 + entry*16 + byte. Way 0 maps to
// 0xC0000000-0xC00003FF, way 1 to 0x...400-0x...7FF, way 2 to
// 0x...800-0x...BFF, way 3 to 0x...C00-0x...FFF, so direct data-array
// access and cached lines share storage exactly as on hardware. Tags
// live in cacheTags entry-major with the valid bit packed into bit 0,
// so a lookup probes one contiguous row.

// CCR bits (Section 8.2, Table 8.1).
const (
	ccrCE = 0x01 // Cache enable
	ccrID = 0x02 // Instruction replacement disable
	ccrOD = 0x04 // Data replacement disable
	ccrTW = 0x08 // Two-way mode: ways 2-3 cache, ways 0-1 RAM
	ccrCP = 0x10 // Cache purge (write 1: valid+LRU cleared; always reads 0)
)

// fetchLineInvalid marks the fetch-line memo empty. Odd, so it can
// never equal a line address (addr &^ 0xF).
const fetchLineInvalid = 1

// cacheTagOf extracts the tag address bits A28-A10 (Figure 8.2).
func cacheTagOf(addr uint32) uint32 { return addr & 0x1FFFFC00 }

// cacheValidBit is the valid flag packed into bit 0 of a cacheTags
// word, below the tag field.
const cacheValidBit = 1

// cacheEntryOf extracts the entry (set) index bits A9-A4 (Figure 8.2).
func cacheEntryOf(addr uint32) uint32 { return (addr >> 4) & 0x3F }

// cacheLRUTouch is the per-way LRU update from Table 8.3: orMask sets
// the bits recording "this way accessed more recently than the others",
// clearMask clears the opposing direction bits; unlisted bits hold.
var cacheLRUTouch = [4]struct{ orMask, clearMask uint8 }{
	{0x00, 0x38}, // way 0: bits 5,4,3 = 0
	{0x20, 0x06}, // way 1: bit 5 = 1; bits 2,1 = 0
	{0x14, 0x01}, // way 2: bits 4,2 = 1; bit 0 = 0
	{0x0B, 0x00}, // way 3: bits 3,1,0 = 1
}

// cacheTouch records an access to a way for an entry. The LRU
// information is rewritten on read hit, write hit, and replacement
// after a miss (Section 8.4.5).
func (c *CPU) cacheTouch(entry uint32, way int) {
	m := cacheLRUTouch[way]
	c.cacheLRU[entry] = (c.cacheLRU[entry] &^ m.clearMask) | m.orMask
}

// cacheLookup finds the way whose valid line holds addr's tag at addr's
// entry. Tags are compared on all four ways even in two-way mode
// (Section 8.4.5); entries with valid bit 0 never match (Section 8.4.1)
// - the packed valid bit makes an invalid line's word compare unequal.
func (c *CPU) cacheLookup(addr uint32) (int, bool) {
	set := &c.cacheTags[cacheEntryOf(addr)]
	want := cacheTagOf(addr) | cacheValidBit
	switch want {
	case set[0]:
		return 0, true
	case set[1]:
		return 1, true
	case set[2]:
		return 2, true
	case set[3]:
		return 3, true
	}
	return 0, false
}

// cacheReplaceWay selects the way to replace on a miss from the 6-bit
// pseudo-LRU per Table 8.4. After a purge the all-zero LRU selects
// way 3, giving the initial order way 3 -> 2 -> 1 -> 0 (Section 8.4.5).
// In two-way mode only ways 2 and 3 are replaced; bit 0 is the
// way2<->way3 access-order bit (Figure 8.8), so it alone picks between
// them (the full Table 8.4 conditions reference ways 0/1 history that
// two-way operation no longer maintains).
func (c *CPU) cacheReplaceWay(entry uint32) int {
	lru := c.cacheLRU[entry]
	if c.ccr&ccrTW != 0 {
		if lru&0x01 != 0 {
			return 2
		}
		return 3
	}
	switch {
	case lru&0x38 == 0x38:
		return 0
	case lru&0x26 == 0x06:
		return 1
	case lru&0x15 == 0x01:
		return 2
	default:
		return 3
	}
}

// cacheFill replaces a line: the tag and valid bit are written at
// replacement start (Section 8.4.5 - they do not wait for the memory
// reads), then the four longwords of the line are read from memory
// into the data array (Section 8.4.1). Hardware orders the burst so
// the requested longword arrives last; erings fills the line whole via
// SH2FillLine, which charges the region's 16-byte burst cost (one SDRAM
// burst pipeline for Work RAM-H, four singles for non-bursting regions)
// plus any inter-CPU contention the Bus implementation models.
func (c *CPU) cacheFill(way int, addr uint32) {
	// The replacement may evict the memoized fetch line.
	c.fetchLineAddr = fetchLineInvalid
	entry := cacheEntryOf(addr)
	c.cacheTags[entry][way] = cacheTagOf(addr) | cacheValidBit
	base := addr &^ 0xF
	off := uint32(way)*1024 + entry*16
	// Read the 16-byte line as one transaction so it cannot be torn by
	// a concurrent write from another bus master (Section 8.4.1).
	var line [16]byte
	stall := c.bus.SH2FillLine(base, &line, int64(c.cycles), !c.isMaster)
	copy(c.cacheData[off:off+16], line[:])
	c.cacheTouch(entry, way)
	c.busStall += stall
}

// cacheDataOff returns the data-array offset of addr's byte within
// way's line.
func cacheDataOff(way int, addr uint32) uint32 {
	return uint32(way)*1024 + cacheEntryOf(addr)*16 + addr&0xF
}

// cacheRead8/16/32 service a data read in the cache area with the
// cache enabled (Section 8.4.1). Hit: data from the data array, LRU
// updated, no external access. Miss: the line is filled and the read
// served from it, unless OD=1, in which case the missed data is read
// from memory and passed directly to the CPU with no replacement
// (Section 8.4.5).
func (c *CPU) cacheRead8(addr uint32) uint8 {
	if way, hit := c.cacheLookup(addr); hit {
		c.cacheTouch(cacheEntryOf(addr), way)
		return c.cacheData[cacheDataOff(way, addr)]
	}
	if c.ccr&ccrOD != 0 {
		v, stall := c.bus.SH2Read8(addr, int64(c.cycles), !c.isMaster)
		c.busStall += stall
		return v
	}
	way := c.cacheReplaceWay(cacheEntryOf(addr))
	c.cacheFill(way, addr)
	return c.cacheData[cacheDataOff(way, addr)]
}

func (c *CPU) cacheRead16(addr uint32) uint16 {
	if way, hit := c.cacheLookup(addr); hit {
		c.cacheTouch(cacheEntryOf(addr), way)
		off := cacheDataOff(way, addr)
		return uint16(c.cacheData[off])<<8 | uint16(c.cacheData[off+1])
	}
	if c.ccr&ccrOD != 0 {
		v, stall := c.bus.SH2Read16(addr, int64(c.cycles), !c.isMaster)
		c.busStall += stall
		return v
	}
	way := c.cacheReplaceWay(cacheEntryOf(addr))
	c.cacheFill(way, addr)
	off := cacheDataOff(way, addr)
	return uint16(c.cacheData[off])<<8 | uint16(c.cacheData[off+1])
}

func (c *CPU) cacheRead32(addr uint32) uint32 {
	if way, hit := c.cacheLookup(addr); hit {
		c.cacheTouch(cacheEntryOf(addr), way)
		off := cacheDataOff(way, addr)
		return uint32(c.cacheData[off])<<24 | uint32(c.cacheData[off+1])<<16 |
			uint32(c.cacheData[off+2])<<8 | uint32(c.cacheData[off+3])
	}
	if c.ccr&ccrOD != 0 {
		v, stall := c.bus.SH2Read32(addr, int64(c.cycles), !c.isMaster)
		c.busStall += stall
		return v
	}
	way := c.cacheReplaceWay(cacheEntryOf(addr))
	c.cacheFill(way, addr)
	off := cacheDataOff(way, addr)
	return uint32(c.cacheData[off])<<24 | uint32(c.cacheData[off+1])<<16 |
		uint32(c.cacheData[off+2])<<8 | uint32(c.cacheData[off+3])
}

// cacheFetch16 services an instruction fetch in the cache area with
// the cache enabled. Reads and fetches share the cache (mixed
// instruction/data, Section 8.1); ID=1 disables replacement on fetch
// misses the same way OD does for data (Section 8.2).
//
// The fetch-line memo short-circuits the 4-way tag search for
// sequential fetches within the line resolved by the previous fetch.
// The LRU touch is kept on the memo path so the replacement state
// stays bit-identical to an unmemoized lookup.
func (c *CPU) cacheFetch16(addr uint32) uint16 {
	if addr&^0xF == c.fetchLineAddr {
		c.cacheTouch(cacheEntryOf(addr), c.fetchLineWay)
		off := c.fetchLineOff + addr&0xF
		return uint16(c.cacheData[off])<<8 | uint16(c.cacheData[off+1])
	}
	if way, hit := c.cacheLookup(addr); hit {
		c.cacheTouch(cacheEntryOf(addr), way)
		c.memoFetchLine(way, addr)
		off := cacheDataOff(way, addr)
		return uint16(c.cacheData[off])<<8 | uint16(c.cacheData[off+1])
	}
	if c.ccr&ccrID != 0 {
		v, stall := c.bus.SH2Read16(addr, int64(c.cycles), !c.isMaster)
		c.busStall += stall
		return v
	}
	way := c.cacheReplaceWay(cacheEntryOf(addr))
	c.cacheFill(way, addr)
	c.memoFetchLine(way, addr)
	off := cacheDataOff(way, addr)
	return uint16(c.cacheData[off])<<8 | uint16(c.cacheData[off+1])
}

// memoFetchLine records a resolved fetch line for the memo fast path.
func (c *CPU) memoFetchLine(way int, addr uint32) {
	c.fetchLineAddr = addr &^ 0xF
	c.fetchLineWay = way
	c.fetchLineOff = uint32(way)*1024 + cacheEntryOf(addr)*16
}

// cacheWriteHit updates the data array if addr's tag hits (the
// write-through path, Section 8.4.2: memory is always written; the
// data array only on a hit, and the LRU is rewritten on a write hit
// per Section 8.4.5). The caller performs the memory write.
func (c *CPU) cacheWriteHit8(addr uint32, val uint8) {
	if way, hit := c.cacheLookup(addr); hit {
		c.cacheTouch(cacheEntryOf(addr), way)
		c.cacheData[cacheDataOff(way, addr)] = val
	}
}

func (c *CPU) cacheWriteHit16(addr uint32, val uint16) {
	if way, hit := c.cacheLookup(addr); hit {
		c.cacheTouch(cacheEntryOf(addr), way)
		off := cacheDataOff(way, addr)
		c.cacheData[off] = uint8(val >> 8)
		c.cacheData[off+1] = uint8(val)
	}
}

func (c *CPU) cacheWriteHit32(addr uint32, val uint32) {
	if way, hit := c.cacheLookup(addr); hit {
		c.cacheTouch(cacheEntryOf(addr), way)
		off := cacheDataOff(way, addr)
		c.cacheData[off] = uint8(val >> 24)
		c.cacheData[off+1] = uint8(val >> 16)
		c.cacheData[off+2] = uint8(val >> 8)
		c.cacheData[off+3] = uint8(val)
	}
}

// cachePurge clears all valid bits and all LRU information (Section
// 8.4.6). Tags and data are not touched - only validity. Triggered by
// writing 1 to CCR's CP bit.
func (c *CPU) cachePurge() {
	c.fetchLineAddr = fetchLineInvalid
	for entry := range c.cacheTags {
		for way := range c.cacheTags[entry] {
			c.cacheTags[entry][way] &^= cacheValidBit
		}
	}
	clear(c.cacheLRU[:])
}

// CachePurge purges the cache on behalf of emulated BIOS code running
// in Go (HLE): on hardware that code executed on this CPU through the
// cache, so its memory writes were coherent and its footprint evicted
// resident lines. A full purge at the service boundary restores both
// properties. Must be called on the CPU's own thread, between
// instructions.
func (c *CPU) CachePurge() { c.cachePurge() }

// associativePurge invalidates the line holding addr's tag at addr's
// entry, if present (Section 8.4.7): a write to the purge area
// (partition 010) clears the valid bit of a matching line in any way.
// A non-matching address purges nothing.
func (c *CPU) associativePurge(addr uint32) {
	c.fetchLineAddr = fetchLineInvalid
	set := &c.cacheTags[cacheEntryOf(addr)]
	want := cacheTagOf(addr) | cacheValidBit
	for way := range set {
		if set[way] == want {
			set[way] &^= cacheValidBit
		}
	}
}

// addressArrayRead returns an address-array word (Section 8.4.9,
// Figure 8.11): the tag address, LRU information, and valid bit of the
// entry selected by A9-A4, for the way selected by CCR's W1-W0 bits.
// Data layout: bits 28-10 tag, 9-4 LRU, 2 valid.
func (c *CPU) addressArrayRead(addr uint32) uint32 {
	way := (c.ccr >> 6) & 3
	entry := cacheEntryOf(addr)
	stored := c.cacheTags[entry][way]
	v := (stored &^ cacheValidBit) | uint32(c.cacheLRU[entry])<<4
	if stored&cacheValidBit != 0 {
		v |= 1 << 2
	}
	return v
}

// addressArrayWrite writes a tag and valid bit from the ADDRESS (bits
// 28-10 tag, 9-4 entry, bit 2 valid) and the LRU information from the
// DATA (bits 9-4), per Section 8.4.9 Figure 8.11, for the way selected
// by CCR's W1-W0.
func (c *CPU) addressArrayWrite(addr uint32, val uint32) {
	c.fetchLineAddr = fetchLineInvalid
	way := (c.ccr >> 6) & 3
	entry := cacheEntryOf(addr)
	stored := cacheTagOf(addr)
	if addr&(1<<2) != 0 {
		stored |= cacheValidBit
	}
	c.cacheTags[entry][way] = stored
	c.cacheLRU[entry] = uint8(val>>4) & 0x3F
}
