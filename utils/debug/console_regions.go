// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
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
	// flatBase locates the region inside the core's ReadMemory window.
	// This is the access point into reading memory.
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
// buffer via the core's ReadMemory window.
func (g *game) readRegion(r *region, off, length uint32) []byte {
	buf := make([]byte, length)
	n := g.emu.ReadMemory(r.flatBase+off, buf)
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
		for _, c := range line {
			if c >= 0x20 && c <= 0x7E {
				b.WriteByte(c)
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

func cmdRead(g *game, args []string) (string, error) {
	if len(args) < 1 || len(args) > 2 {
		return "", fmt.Errorf("usage: read <addr> [len]")
	}
	addr, err := parseAddress(args[0])
	if err != nil {
		return "", err
	}
	length := uint32(readDefaultLen)
	if len(args) == 2 {
		v, err := strconv.ParseUint(args[1], 10, 32)
		if err != nil || v < 1 || v > readMaxLen {
			return "", fmt.Errorf("length must be 1-%d", readMaxLen)
		}
		length = uint32(v)
	}
	r, off, err := lookupRegion(addr)
	if err != nil {
		return "", err
	}
	// Clamp to the region end rather than reading past it.
	if remaining := r.size - off; length > remaining {
		length = remaining
	}
	return formatHexDump(r.start+off, g.readRegion(r, off, length)), nil
}

func cmdRegions(g *game, args []string) (string, error) {
	var b strings.Builder
	for i := range regionTable {
		r := &regionTable[i]
		fmt.Fprintf(&b, "%-6s 0x%08X-0x%08X  %dKB\n", r.name, r.start, r.end(), r.size/1024)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
