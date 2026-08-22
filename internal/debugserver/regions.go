// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/user-none/erings/core"
	"github.com/user-none/erings/internal/debugserver/responses"
)

// The server's region knowledge comes from the machine's bus region
// table (Machine.Regions), cached on the Server by setMachine. The
// table drives address validation and memory access, so debugger
// addressing and the machine's own resolution cannot drift.

func regionEnd(r *core.BusRegion) uint32 { return r.Start + r.Size - 1 }

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

// lookupRegion resolves a canonical native address to a region and
// offset. Addresses are canonical only, as the machine's region table
// lists them; mirror and partition spellings are outside the known
// regions.
func (c *Server) lookupRegion(addr uint32) (*core.BusRegion, uint32, error) {
	for i := range c.regions {
		r := &c.regions[i]
		if addr >= r.Start && addr-r.Start < r.Size {
			return r, addr - r.Start, nil
		}
	}
	var ranges []string
	for i := range c.regions {
		r := &c.regions[i]
		ranges = append(ranges, fmt.Sprintf("%s 0x%08X-0x%08X", r.Name, r.Start, regionEnd(r)))
	}
	return nil, 0, fmt.Errorf("address 0x%08X is outside known regions (%s)",
		addr, strings.Join(ranges, ", "))
}

// readRegion copies length bytes at the region offset into a fresh
// buffer via the machine's native memory access.
func (c *Server) readRegion(r *core.BusRegion, off, length uint32) []byte {
	buf := make([]byte, length)
	n := c.machine.ReadMemory(r.Start+off, buf)
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

// readText renders the read response for text mode. The hex string is
// our own encoding of the bytes just read, so the decode cannot fail.
func readText(r responses.ReadResult) string {
	data, err := hex.DecodeString(r.Data)
	if err != nil {
		return "error: corrupt read data"
	}
	return formatHexDump(r.Addr, data)
}

func cmdRead(c *Server, args []string) (any, error) {
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
	r, off, err := c.lookupRegion(addr)
	if err != nil {
		return nil, err
	}
	// Clamp to the region end rather than reading past it.
	if remaining := r.Size - off; length > remaining {
		length = remaining
	}
	data := c.readRegion(r, off, length)
	return responses.ReadResult{Addr: r.Start + off, Data: hex.EncodeToString(data)}, nil
}

const writeMaxLen = 4096

// parseWriteTarget validates the address and hex byte string pair
// shared by write and pin. maxLen bounds the byte count, which differs
// between a one-shot write and a pin re-applied every frame. It
// returns the canonical address and the decoded bytes, which must fit
// inside the address's region.
func (c *Server) parseWriteTarget(addrStr, hexStr string, maxLen int) (uint32, []byte, error) {
	addr, err := parseAddress(addrStr)
	if err != nil {
		return 0, nil, err
	}
	data, err := hex.DecodeString(hexStr)
	if err != nil || len(data) == 0 {
		return 0, nil, fmt.Errorf("data must be an even-length hex byte string")
	}
	if len(data) > maxLen {
		return 0, nil, fmt.Errorf("length must be 1-%d bytes", maxLen)
	}
	r, off, err := c.lookupRegion(addr)
	if err != nil {
		return 0, nil, err
	}
	if uint32(len(data)) > r.Size-off {
		return 0, nil, fmt.Errorf("%d bytes do not fit inside %s", len(data), r.Name)
	}
	return r.Start + off, data, nil
}

// cmdWrite writes hex bytes to memory at a native address. The write is
// validated against the region table and must fit inside its region.
func cmdWrite(c *Server, args []string) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("usage: write <addr> <hex>")
	}
	addr, data, err := c.parseWriteTarget(args[0], args[1], writeMaxLen)
	if err != nil {
		return nil, err
	}
	n := c.machine.WriteMemory(addr, data)
	return fmt.Sprintf("wrote %d bytes at 0x%08X", n, addr), nil
}

// regionListText renders the region list response for text mode.
func regionListText(r responses.RegionList) string {
	var b strings.Builder
	for _, ri := range r.Regions {
		fmt.Fprintf(&b, "%-6s 0x%08X-0x%08X  %dKB\n", ri.Name, ri.Start, ri.End, ri.Size/1024)
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdRegions(c *Server, args []string) (any, error) {
	res := responses.RegionList{Regions: make([]responses.RegionInfo, 0, len(c.regions))}
	for i := range c.regions {
		r := &c.regions[i]
		res.Regions = append(res.Regions, responses.RegionInfo{
			Name: r.Name, Start: r.Start, End: regionEnd(r), Size: r.Size})
	}
	return res, nil
}
