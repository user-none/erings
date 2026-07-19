// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package console

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// maxWatches bounds the per-frame read cost of the watch list.
const maxWatches = 64

// watchEntry is one watched address. Entries survive console
// disconnects.
type watchEntry struct {
	addr  uint32 // canonical native address, for display
	r     *region
	off   uint32
	width int // value width in bits: 8, 16, 32
	prev  uint32
	valid bool // prev has been seeded. The first read never reports.
}

// beValue assembles bytes big-endian, as the SH-2 reads them.
func beValue(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return v
}

// serviceWatches reads every watched address and reports changes. It
// runs between frames on the emulation goroutine. Change lines go to
// stderr and are pushed non-blocking to the connected client. A stalled
// client drops pushes. The stderr copy is complete.
func (c *Console) serviceWatches() {
	for i := range c.watches {
		w := &c.watches[i]
		var buf [4]byte
		n := w.width / 8
		c.machine.ReadMemory(w.r.flatBase+w.off, buf[:n])
		cur := beValue(buf[:n])
		if w.valid && cur != w.prev {
			line := fmt.Sprintf("[WATCH] frame=%d 0x%08X w%d: %d -> %d (0x%0*X -> 0x%0*X)",
				c.frame, w.addr, w.width, w.prev, cur, n*2, w.prev, n*2, cur)
			fmt.Fprintln(os.Stderr, line)
			if c.out != nil {
				select {
				case c.out <- line + "\n":
				default:
				}
			}
		}
		w.prev = cur
		w.valid = true
	}
}

// parseWidth parses a value width argument.
func parseWidth(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil || (v != 8 && v != 16 && v != 32) {
		return 0, fmt.Errorf("width must be 8, 16, or 32")
	}
	return v, nil
}

// resolveTarget validates an address at a width. It returns the
// canonical address, region, and offset.
func resolveTarget(addrStr string, width int) (uint32, *region, uint32, error) {
	addr, err := parseAddress(addrStr)
	if err != nil {
		return 0, nil, 0, err
	}
	r, off, err := lookupRegion(addr)
	if err != nil {
		return 0, nil, 0, err
	}
	if bytes := uint32(width / 8); off%bytes != 0 {
		return 0, nil, 0, fmt.Errorf("address 0x%08X is not %d-byte aligned for w%d",
			r.start+off, bytes, width)
	}
	return r.start + off, r, off, nil
}

// parseWatchTarget validates an address/width argument pair shared by
// watch and unwatch. It returns the canonical address, region, offset,
// and width.
func parseWatchTarget(args []string) (uint32, *region, uint32, int, error) {
	width := 8
	if len(args) == 2 {
		w, err := parseWidth(args[1])
		if err != nil {
			return 0, nil, 0, 0, err
		}
		width = w
	}
	addr, r, off, err := resolveTarget(args[0], width)
	if err != nil {
		return 0, nil, 0, 0, err
	}
	return addr, r, off, width, nil
}

func cmdWatch(c *Console, args []string) (string, error) {
	if len(args) == 0 {
		if len(c.watches) == 0 {
			return "no watches", nil
		}
		var b strings.Builder
		for i := range c.watches {
			w := &c.watches[i]
			val := "?"
			if w.valid {
				val = strconv.FormatUint(uint64(w.prev), 10)
			}
			fmt.Fprintf(&b, "0x%08X w%d = %s\n", w.addr, w.width, val)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	if len(args) > 2 {
		return "", fmt.Errorf("usage: watch [<addr> [width]]")
	}
	addr, r, off, width, err := parseWatchTarget(args)
	if err != nil {
		return "", err
	}
	// Re-watching an address replaces its entry so a width change or
	// reseed takes effect.
	for i := range c.watches {
		if c.watches[i].addr == addr {
			c.watches[i] = watchEntry{addr: addr, r: r, off: off, width: width}
			return fmt.Sprintf("watching 0x%08X w%d", addr, width), nil
		}
	}
	if len(c.watches) >= maxWatches {
		return "", fmt.Errorf("watch limit reached (%d)", maxWatches)
	}
	c.watches = append(c.watches, watchEntry{addr: addr, r: r, off: off, width: width})
	return fmt.Sprintf("watching 0x%08X w%d", addr, width), nil
}

func cmdUnwatch(c *Console, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("usage: unwatch <addr>|all")
	}
	if args[0] == "all" {
		n := len(c.watches)
		c.watches = nil
		return fmt.Sprintf("removed %d watches", n), nil
	}
	addr, err := parseAddress(args[0])
	if err != nil {
		return "", err
	}
	r, off, err := lookupRegion(addr)
	if err != nil {
		return "", err
	}
	canonical := r.start + off
	for i := range c.watches {
		if c.watches[i].addr == canonical {
			c.watches = append(c.watches[:i], c.watches[i+1:]...)
			return fmt.Sprintf("unwatched 0x%08X", canonical), nil
		}
	}
	return "", fmt.Errorf("0x%08X is not watched", canonical)
}
