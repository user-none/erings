// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugserver

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/user-none/erings/internal/debugserver/responses"
)

// maxPins bounds the per-frame write cost of the pin list.
const maxPins = 16

// pinMaxLen bounds one pin's byte count. A pin is re-applied every
// frame, so it is held well below the one-shot write limit.
const pinMaxLen = 64

// pinEntry is one held memory location, addressed the way write is: a
// canonical address and a byte string, with no value width. The bytes
// are restored between frames, so a store by emulated code survives at
// most to the end of the frame that made it. Entries survive client
// disconnects.
type pinEntry struct {
	addr uint32 // canonical native address
	data []byte
	hits uint64 // frames the location was found changed and restored
}

// end returns the address one past the pin's last byte.
func (p *pinEntry) end() uint32 { return p.addr + uint32(len(p.data)) }

// servicePins restores every pinned location. It runs between frames on
// the emulation goroutine ahead of the watch and break reads, so a
// client's read, a watch line, and a break condition all see the held
// value instead of disagreeing over what the frame left behind. A held
// location therefore never reports a watch change and never fires a
// break: hits is what records that something changed it. hits counts
// frames, not writes. It rises by at most one per frame however many
// times the frame wrote, and not at all for a write that put the held
// bytes back before the frame ended.
func (c *Server) servicePins() {
	// cmdPin is the only writer of the list and caps every entry at
	// pinMaxLen, so the compare buffer always holds a whole pin.
	var buf [pinMaxLen]byte
	for i := range c.pins {
		p := &c.pins[i]
		cur := buf[:len(p.data)]
		n := c.machine.ReadMemory(p.addr, cur)
		if int(n) == len(cur) && bytes.Equal(cur, p.data) {
			continue
		}
		p.hits++
		c.machine.WriteMemory(p.addr, p.data)
	}
}

// pinListText renders the pin list response for text mode. Bytes are
// upper case to match the hex dump.
func pinListText(r responses.PinList) string {
	if len(r.Pins) == 0 {
		return "no pins"
	}
	var b strings.Builder
	for _, p := range r.Pins {
		fmt.Fprintf(&b, "0x%08X = %s hits=%d\n", p.Addr, strings.ToUpper(p.Data), p.Hits)
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdPin(c *Server, args []string) (any, error) {
	if len(args) == 0 {
		r := responses.PinList{Pins: make([]responses.PinInfo, 0, len(c.pins))}
		for i := range c.pins {
			p := &c.pins[i]
			r.Pins = append(r.Pins, responses.PinInfo{
				Addr: p.addr, Data: hex.EncodeToString(p.data), Hits: p.hits})
		}
		return r, nil
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("usage: pin [<addr> <hex>]")
	}
	addr, data, err := c.parseWriteTarget(args[0], args[1], pinMaxLen)
	if err != nil {
		return nil, err
	}
	// Pins cover byte ranges rather than single addresses, so equal
	// start addresses are not the only way two of them can collide.
	// Overlap is rejected instead of resolved: with two pins writing the
	// same byte, which one holds it would come down to list order.
	replace := -1
	for i := range c.pins {
		p := &c.pins[i]
		if p.addr == addr {
			replace = i
			continue
		}
		if addr < p.end() && p.addr < addr+uint32(len(data)) {
			return nil, fmt.Errorf("0x%08X overlaps the pin at 0x%08X", addr, p.addr)
		}
	}
	if replace < 0 && len(c.pins) >= maxPins {
		return nil, fmt.Errorf("pin limit reached (%d)", maxPins)
	}
	// Apply now rather than at the next Service so the command behaves
	// like write plus a hold.
	c.machine.WriteMemory(addr, data)
	entry := pinEntry{addr: addr, data: data}
	if replace >= 0 {
		c.pins[replace] = entry
	} else {
		c.pins = append(c.pins, entry)
	}
	return fmt.Sprintf("pinned %d bytes at 0x%08X", len(data), addr), nil
}

// cmdUnpin drops a pin. The held bytes stay in memory: unpinning
// releases the location, it does not roll the write back.
func cmdUnpin(c *Server, args []string) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("usage: unpin <addr>|all")
	}
	if args[0] == "all" {
		n := len(c.pins)
		c.pins = nil
		return fmt.Sprintf("removed %d pins", n), nil
	}
	addr, err := parseAddress(args[0])
	if err != nil {
		return nil, err
	}
	if _, _, err := c.lookupRegion(addr); err != nil {
		return nil, err
	}
	for i := range c.pins {
		if c.pins[i].addr == addr {
			c.pins = append(c.pins[:i], c.pins[i+1:]...)
			return fmt.Sprintf("unpinned 0x%08X", addr), nil
		}
	}
	return nil, fmt.Errorf("0x%08X is not pinned", addr)
}
