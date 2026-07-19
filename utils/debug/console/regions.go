// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package console

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// region is one entry in the memory-region registry. The registry drives
// console address validation and read access. New memory areas become
// table entries. Regions are memories only. Register banks never get an
// entry because their reads can have side effects.
type region struct {
	name string
	// start and size describe the canonical physical range. Window is
	// the full bus decode window. The region repeats (mirrors) through
	// it, exactly as the bus folds addresses onto the backing storage.
	// window == size when the region is not mirrored.
	start  uint32
	size   uint32
	window uint32
	// flatBase locates the region inside the machine's ReadMemory
	// window. This is the access point into reading memory.
	flatBase uint32
}

// regionTable mirrors the bus decode. WRAM-L is exactly 1MB with no
// mirror. WRAM-H repeats through 0x06000000-0x07FFFFFF.
var regionTable = []region{
	{name: "wraml", start: 0x00200000, size: 0x100000, window: 0x100000, flatBase: 0x000000},
	{name: "wramh", start: 0x06000000, size: 0x100000, window: 0x2000000, flatBase: 0x100000},
}

func (r *region) end() uint32 { return r.start + r.size - 1 }

// parseNumber parses a uint32 given as hex with a 0x prefix or as
// decimal.
func parseNumber(s string) (uint32, error) {
	var v uint64
	var err error
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err = strconv.ParseUint(s[2:], 16, 32)
	} else {
		v, err = strconv.ParseUint(s, 10, 32)
	}
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

// parseAddress parses a native address given as hex with a 0x prefix or
// as decimal.
func parseAddress(s string) (uint32, error) {
	v, err := parseNumber(s)
	if err != nil {
		return 0, fmt.Errorf("invalid address %q", s)
	}
	return v, nil
}

// lookupRegion normalizes a native address and resolves it to a region
// and canonical offset. Normalization masks the SH-2 partition bits (the
// top 3 address bits select cached/cache-through/purge views of the same
// physical space) and folds mirror images within a region's decode window.
func lookupRegion(addr uint32) (*region, uint32, error) {
	physical := addr & 0x1FFFFFFF
	for i := range regionTable {
		r := &regionTable[i]
		if physical >= r.start && physical < r.start+r.window {
			return r, (physical - r.start) & (r.size - 1), nil
		}
	}
	var ranges []string
	for i := range regionTable {
		r := &regionTable[i]
		ranges = append(ranges, fmt.Sprintf("%s 0x%08X-0x%08X", r.name, r.start, r.end()))
	}
	return nil, 0, fmt.Errorf("address 0x%08X is outside known regions (%s)",
		addr, strings.Join(ranges, ", "))
}

// readRegion copies length bytes at the region offset into a fresh
// buffer via the machine's ReadMemory window.
func (c *Console) readRegion(r *region, off, length uint32) []byte {
	buf := make([]byte, length)
	n := c.machine.ReadMemory(r.flatBase+off, buf)
	return buf[:n]
}

// formatHexDump renders data as a classic hex dump with 16 bytes per
// line, the native address, a mid-line gap, and an ASCII gutter.
func formatHexDump(addr uint32, data []byte) string {
	var b strings.Builder
	for base := 0; base < len(data); base += 16 {
		line := data[base:min(base+16, len(data))]
		fmt.Fprintf(&b, "0x%08X  ", addr+uint32(base))
		for i := 0; i < 16; i++ {
			if i == 8 {
				b.WriteByte(' ')
			}
			if i < len(line) {
				fmt.Fprintf(&b, "%02X ", line[i])
			} else {
				b.WriteString("   ")
			}
		}
		b.WriteString(" |")
		for _, ch := range line {
			if ch >= 0x20 && ch <= 0x7E {
				b.WriteByte(ch)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

const (
	readDefaultLen = 64
	readMaxLen     = 4096
)

// ReadResult is the read command response. Data is the bytes hex
// encoded. raw keeps the bytes for the text rendering without a decode
// round trip.
type ReadResult struct {
	Addr uint32 `json:"addr"`
	Data string `json:"data"`
	raw  []byte
}

func (r ReadResult) text() string { return formatHexDump(r.Addr, r.raw) }

func cmdRead(c *Console, args []string) (result, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("usage: read <addr> [len]")
	}
	addr, err := parseAddress(args[0])
	if err != nil {
		return nil, err
	}
	length := uint32(readDefaultLen)
	if len(args) == 2 {
		v, err := strconv.ParseUint(args[1], 10, 32)
		if err != nil || v < 1 || v > readMaxLen {
			return nil, fmt.Errorf("length must be 1-%d", readMaxLen)
		}
		length = uint32(v)
	}
	r, off, err := lookupRegion(addr)
	if err != nil {
		return nil, err
	}
	// Clamp to the region end rather than reading past it.
	if remaining := r.size - off; length > remaining {
		length = remaining
	}
	data := c.readRegion(r, off, length)
	return ReadResult{Addr: r.start + off, Data: hex.EncodeToString(data), raw: data}, nil
}

// RegionList is the regions command response.
type RegionList struct {
	Regions []RegionInfo `json:"regions"`
}

// RegionInfo is one region list entry. Start and End bound the
// canonical range inclusive; Size is in bytes. Window is the full bus
// decode window the region mirrors through (Window == Size when not
// mirrored), so a client can fold mirror spellings the way the console
// does.
type RegionInfo struct {
	Name   string `json:"name"`
	Start  uint32 `json:"start"`
	End    uint32 `json:"end"`
	Size   uint32 `json:"size"`
	Window uint32 `json:"window"`
}

func (r RegionList) text() string {
	var b strings.Builder
	for _, ri := range r.Regions {
		fmt.Fprintf(&b, "%-6s 0x%08X-0x%08X  %dKB\n", ri.Name, ri.Start, ri.End, ri.Size/1024)
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdRegions(c *Console, args []string) (result, error) {
	res := RegionList{Regions: make([]RegionInfo, 0, len(regionTable))}
	for i := range regionTable {
		r := &regionTable[i]
		res.Regions = append(res.Regions, RegionInfo{
			Name: r.name, Start: r.start, End: r.end(), Size: r.size, Window: r.window})
	}
	return res, nil
}
