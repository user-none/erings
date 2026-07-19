// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package debugconsole

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"

	"github.com/user-none/erings/internal/debugconsoletypes"
)

// searchRegionState is one region's share of an active search. It holds
// the region's baseline snapshot and the candidate bitmap with one bit
// per width-aligned offset.
type searchRegionState struct {
	r        *region
	baseline []byte
	cand     []uint64
	count    int
}

// search is an active memory search owned by the emulation goroutine.
// The candidate set and baselines survive snapshot restores by design.
// After a restore, rebase re-anchors the baseline to current memory so
// the next filter intersects into the same surviving set.
type search struct {
	width   int // value width in bits
	regions []searchRegionState
}

func (s *search) total() int {
	t := 0
	for i := range s.regions {
		t += s.regions[i].count
	}
	return t
}

// currentWidth returns the search value width, defaulting to 8 bits.
func (c *Console) currentWidth() int {
	if c.searchWidth == 0 {
		return 8
	}
	return c.searchWidth
}

// newBitmap returns a bitmap of n bits, all set, with the tail bits of
// the last word clear.
func newBitmap(n int) []uint64 {
	bm := make([]uint64, (n+63)/64)
	for i := range bm {
		bm[i] = ^uint64(0)
	}
	if tail := n % 64; tail != 0 {
		bm[len(bm)-1] = (uint64(1) << tail) - 1
	}
	return bm
}

// filterBitmap clears every candidate bit whose value fails keep. The
// prev argument comes from base and cur comes from the live snapshot.
// Both are read big-endian at the candidate's offset. It returns the
// surviving count.
func filterBitmap(cand []uint64, base, cur []byte, stride int, keep func(prev, cur uint32) bool) int {
	count := 0
	for wi, word := range cand {
		if word == 0 {
			continue
		}
		var kept uint64
		for b := 0; b < 64; b++ {
			if word&(1<<b) == 0 {
				continue
			}
			off := (wi*64 + b) * stride
			if keep(beValue(base[off:off+stride]), beValue(cur[off:off+stride])) {
				kept |= 1 << b
			}
		}
		cand[wi] = kept
		count += bits.OnesCount64(kept)
	}
	return count
}

// forEachBit calls fn with each set bit index in ascending order until
// fn returns false.
func forEachBit(bm []uint64, fn func(idx int) bool) {
	for wi, word := range bm {
		for word != 0 {
			b := bits.TrailingZeros64(word)
			if !fn(wi*64 + b) {
				return
			}
			word &^= 1 << b
		}
	}
}

// filterOp is one filter operator. Comparison ops (needsVal) test the
// current value against a constant. The others test it against the
// baseline. Every filter re-baselines to the values it just read.
type filterOp struct {
	name     string
	needsVal bool
	keep     func(prev, cur, val uint32) bool
}

func findFilterOp(name string) *filterOp {
	for i := range filterOps {
		if filterOps[i].name == name {
			return &filterOps[i]
		}
	}
	return nil
}

var filterOps = []filterOp{
	{"dec", false, func(p, c, _ uint32) bool { return c < p }},
	{"inc", false, func(p, c, _ uint32) bool { return c > p }},
	{"same", false, func(p, c, _ uint32) bool { return c == p }},
	{"diff", false, func(p, c, _ uint32) bool { return c != p }},
	{"eq", true, func(_, c, v uint32) bool { return c == v }},
	{"ne", true, func(_, c, v uint32) bool { return c != v }},
	{"lt", true, func(_, c, v uint32) bool { return c < v }},
	{"gt", true, func(_, c, v uint32) bool { return c > v }},
}

func widthMax(width int) uint32 {
	if width == 32 {
		return ^uint32(0)
	}
	return (uint32(1) << width) - 1
}

func cmdBaseline(c *Console, args []string) (any, error) {
	var selected []*region
	if len(args) == 0 {
		for i := range regionTable {
			selected = append(selected, &regionTable[i])
		}
	} else {
		for _, name := range args {
			var r *region
			for i := range regionTable {
				if regionTable[i].name == name {
					r = &regionTable[i]
					break
				}
			}
			if r == nil {
				return nil, fmt.Errorf("unknown region %q (see regions)", name)
			}
			for _, prev := range selected {
				if prev == r {
					return nil, fmt.Errorf("region %q listed twice", name)
				}
			}
			selected = append(selected, r)
		}
	}

	width := c.currentWidth()
	stride := width / 8
	s := &search{width: width}
	var names []string
	for _, r := range selected {
		n := int(r.size) / stride
		st := searchRegionState{
			r:        r,
			baseline: c.readRegion(r, 0, r.size),
			cand:     newBitmap(n),
			count:    n,
		}
		s.regions = append(s.regions, st)
		names = append(names, r.name)
	}
	c.search = s
	return fmt.Sprintf("baseline over %s: %d candidates (w%d)",
		strings.Join(names, ","), s.total(), width), nil
}

func cmdFilter(c *Console, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("usage: filter dec|inc|same|diff | filter eq|ne|lt|gt <value>")
	}
	op := findFilterOp(args[0])
	if op == nil {
		return nil, fmt.Errorf("unknown filter %q", args[0])
	}
	var val uint32
	if op.needsVal {
		if len(args) != 2 {
			return nil, fmt.Errorf("usage: filter %s <value>", op.name)
		}
		v, err := parseNumber(args[1])
		if err != nil {
			return nil, fmt.Errorf("invalid value %q", args[1])
		}
		val = v
	} else if len(args) != 1 {
		return nil, fmt.Errorf("usage: filter %s", op.name)
	}
	if c.search == nil {
		return nil, fmt.Errorf("no search active (run baseline)")
	}
	if op.needsVal && val > widthMax(c.search.width) {
		return nil, fmt.Errorf("value %d exceeds w%d range", val, c.search.width)
	}

	before := c.search.total()
	stride := c.search.width / 8
	keep := func(prev, cur uint32) bool { return op.keep(prev, cur, val) }
	for i := range c.search.regions {
		st := &c.search.regions[i]
		live := c.readRegion(st.r, 0, st.r.size)
		st.count = filterBitmap(st.cand, st.baseline, live, stride, keep)
		// The live snapshot just read becomes the new baseline.
		st.baseline = live
	}
	return fmt.Sprintf("%d -> %d candidates", before, c.search.total()), nil
}

// cmdRebase re-reads the baseline for the active search without
// filtering. It re-anchors comparisons to current memory while keeping
// the candidate set, which is what makes cross-trial intersection work:
// restore, rebase, act, filter.
func cmdRebase(c *Console, args []string) (any, error) {
	if c.search == nil {
		return nil, fmt.Errorf("no search active (run baseline)")
	}
	for i := range c.search.regions {
		st := &c.search.regions[i]
		st.baseline = c.readRegion(st.r, 0, st.r.size)
	}
	return fmt.Sprintf("rebased, %d candidates kept", c.search.total()), nil
}

func cmdWidth(c *Console, args []string) (any, error) {
	if len(args) == 0 {
		return fmt.Sprintf("width %d", c.currentWidth()), nil
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("usage: width [8|16|32]")
	}
	v, err := parseWidth(args[0])
	if err != nil {
		return nil, err
	}
	reset := ""
	if c.search != nil && c.search.width != v {
		c.search = nil
		reset = " (search reset)"
	}
	c.searchWidth = v
	return fmt.Sprintf("width %d%s", v, reset), nil
}

const listDefault = 20

// candidateListText renders the list response for text mode.
func candidateListText(r debugconsoletypes.CandidateList) string {
	digits := r.Width / 8 * 2
	var b strings.Builder
	for _, cd := range r.Candidates {
		fmt.Fprintf(&b, "0x%08X  cur=%d (0x%0*X)  base=%d (0x%0*X)\n",
			cd.Addr, cd.Cur, digits, cd.Cur, cd.Base, digits, cd.Base)
	}
	fmt.Fprintf(&b, "%d candidates", r.Total)
	return b.String()
}

func cmdList(c *Console, args []string) (any, error) {
	n := listDefault
	offset := 0
	if len(args) > 2 {
		return nil, fmt.Errorf("usage: list [n [offset]]")
	}
	if len(args) >= 1 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 || v > 1000 {
			return nil, fmt.Errorf("count must be 1-1000")
		}
		n = v
	}
	if len(args) == 2 {
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 0 {
			return nil, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = v
	}
	if c.search == nil {
		return nil, fmt.Errorf("no search active (run baseline)")
	}

	stride := c.search.width / 8
	res := debugconsoletypes.CandidateList{Width: c.search.width, Total: c.search.total(),
		Offset: offset, Candidates: make([]debugconsoletypes.CandidateInfo, 0)}
	skip := offset
	for i := range c.search.regions {
		st := &c.search.regions[i]
		if len(res.Candidates) >= n {
			break
		}
		// Whole regions before the offset are skipped by count without
		// walking their bitmaps.
		if skip >= st.count {
			skip -= st.count
			continue
		}
		forEachBit(st.cand, func(idx int) bool {
			if skip > 0 {
				skip--
				return true
			}
			off := uint32(idx * stride)
			var buf [4]byte
			c.machine.ReadMemory(st.r.flatBase+off, buf[:stride])
			res.Candidates = append(res.Candidates, debugconsoletypes.CandidateInfo{
				Addr: st.r.start + off,
				Cur:  beValue(buf[:stride]),
				Base: beValue(st.baseline[off : off+uint32(stride)]),
			})
			return len(res.Candidates) < n
		})
	}
	return res, nil
}

func cmdReset(c *Console, args []string) (any, error) {
	if c.search == nil {
		return "no search active", nil
	}
	c.search = nil
	return "search reset", nil
}
