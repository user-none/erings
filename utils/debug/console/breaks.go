// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package console

import (
	"fmt"
	"os"
	"strings"

	"github.com/user-none/erings/internal/debugconsoletypes"
)

// maxBreaks bounds the per-frame read cost of the break list.
const maxBreaks = 16

// breakEntry is one value break. The condition uses the filter operator
// vocabulary: dec/inc/same/diff compare against the previous frame's
// value, eq/ne/lt/gt against a constant. Breaks are edge-triggered. A
// break fires only when its condition goes from false to true, so a
// held condition does not re-fire every frame and a condition that is
// already true when the break is added does not fire until it clears
// and comes back.
type breakEntry struct {
	addr   uint32 // canonical native address, for display
	r      *region
	off    uint32
	width  int // value width in bits: 8, 16, 32
	op     *filterOp
	val    uint32
	prev   uint32
	met    bool
	seeded bool // prev and met are initialized. The first read never fires.
}

func (b *breakEntry) describe() string {
	if b.op.needsVal {
		return fmt.Sprintf("%s %d", b.op.name, b.val)
	}
	return b.op.name
}

// serviceBreaks evaluates every break and pauses emulation on the first
// frame a condition becomes true. It runs between frames on the
// emulation goroutine. A fire also cancels an in-flight frame step so
// stepping stops at the break. Break lines go to stderr and are pushed
// non-blocking to the connected client.
func (c *Console) serviceBreaks() {
	for i := range c.breaks {
		b := &c.breaks[i]
		var buf [4]byte
		n := b.width / 8
		c.machine.ReadMemory(b.r.flatBase+b.off, buf[:n])
		cur := beValue(buf[:n])
		if !b.seeded {
			b.prev = cur
			b.met = b.op.keep(cur, cur, b.val)
			b.seeded = true
			continue
		}
		met := b.op.keep(b.prev, cur, b.val)
		if met && !b.met {
			c.paused.Store(true)
			c.stepRemaining = 0
			line := fmt.Sprintf("[BREAK] frame=%d 0x%08X w%d: %d -> %d (%s) paused",
				c.frame, b.addr, b.width, b.prev, cur, b.describe())
			fmt.Fprintln(os.Stderr, line)
			push := line
			if c.jsonMode {
				push = marshalLine(breakEvent{Type: "break", Frame: c.frame,
					Addr: b.addr, Width: b.width, Prev: b.prev, Cur: cur,
					Cond: b.describe()})
			}
			if c.out != nil {
				select {
				case c.out <- push + "\n":
				default:
				}
			}
		}
		b.met = met
		b.prev = cur
	}
}

// breakCond renders an entry's condition the way breakEntry.describe
// does.
func breakCond(b debugconsoletypes.BreakInfo) string {
	if b.HasVal {
		return fmt.Sprintf("%s %d", b.Op, b.Val)
	}
	return b.Op
}

// breakListText renders the break list response for text mode.
func breakListText(r debugconsoletypes.BreakList) string {
	if len(r.Breaks) == 0 {
		return "no breaks"
	}
	var b strings.Builder
	for _, e := range r.Breaks {
		fmt.Fprintf(&b, "0x%08X w%d %s\n", e.Addr, e.Width, breakCond(e))
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdBreak(c *Console, args []string) (any, error) {
	if len(args) == 0 {
		r := debugconsoletypes.BreakList{Breaks: make([]debugconsoletypes.BreakInfo, 0, len(c.breaks))}
		for i := range c.breaks {
			e := &c.breaks[i]
			r.Breaks = append(r.Breaks, debugconsoletypes.BreakInfo{
				Addr: e.addr, Width: e.width, Op: e.op.name,
				Val: e.val, HasVal: e.op.needsVal})
		}
		return r, nil
	}
	if len(args) < 2 {
		return nil, fmt.Errorf("usage: break [<addr> <op> [value] [width]]")
	}
	op := findFilterOp(args[1])
	if op == nil {
		return nil, fmt.Errorf("unknown condition %q", args[1])
	}
	rest := args[2:]
	var val uint32
	if op.needsVal {
		if len(rest) < 1 {
			return nil, fmt.Errorf("usage: break <addr> %s <value> [width]", op.name)
		}
		v, err := parseNumber(rest[0])
		if err != nil {
			return nil, fmt.Errorf("invalid value %q", rest[0])
		}
		val = v
		rest = rest[1:]
	}
	width := 8
	if len(rest) > 1 {
		return nil, fmt.Errorf("usage: break [<addr> <op> [value] [width]]")
	}
	if len(rest) == 1 {
		w, err := parseWidth(rest[0])
		if err != nil {
			return nil, err
		}
		width = w
	}
	if op.needsVal && val > widthMax(width) {
		return nil, fmt.Errorf("value %d exceeds w%d range", val, width)
	}
	addr, r, off, err := resolveTarget(args[0], width)
	if err != nil {
		return nil, err
	}
	entry := breakEntry{addr: addr, r: r, off: off, width: width, op: op, val: val}
	// Re-adding an address replaces its entry so a condition change or
	// reseed takes effect.
	for i := range c.breaks {
		if c.breaks[i].addr == addr {
			c.breaks[i] = entry
			return fmt.Sprintf("break 0x%08X w%d %s", addr, width, entry.describe()), nil
		}
	}
	if len(c.breaks) >= maxBreaks {
		return nil, fmt.Errorf("break limit reached (%d)", maxBreaks)
	}
	c.breaks = append(c.breaks, entry)
	return fmt.Sprintf("break 0x%08X w%d %s", addr, width, entry.describe()), nil
}

func cmdUnbreak(c *Console, args []string) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("usage: unbreak <addr>|all")
	}
	if args[0] == "all" {
		n := len(c.breaks)
		c.breaks = nil
		return fmt.Sprintf("removed %d breaks", n), nil
	}
	addr, err := parseAddress(args[0])
	if err != nil {
		return nil, err
	}
	r, off, err := lookupRegion(addr)
	if err != nil {
		return nil, err
	}
	canonical := r.start + off
	for i := range c.breaks {
		if c.breaks[i].addr == canonical {
			c.breaks = append(c.breaks[:i], c.breaks[i+1:]...)
			return fmt.Sprintf("unbreak 0x%08X", canonical), nil
		}
	}
	return nil, fmt.Errorf("0x%08X has no break", canonical)
}
