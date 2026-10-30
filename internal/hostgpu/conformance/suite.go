// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package conformance runs the conformance functions of
// core/compute/conformance: their Go execution over fixed test vectors
// under the entry wrapper contract is the reference, and a backend
// passes when its emitted entry points produce the same output and
// byte buffer contents bit for bit. The package names no backend.
// devicetest runs the entries on each backend's device and compares.
package conformance

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/conformance"
	"github.com/user-none/erings/internal/hostgpu/compile"
)

// IndexFn is an index entry: one element per thread.
type IndexFn func(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *conformance.Probe, i uint32)

// SpanFn is a span entry: a segment [x0, x1) of one row per thread.
type SpanFn func(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *conformance.Probe, x0, x1 int32)

// GroupFn is a group entry: the thread's index in its group, the
// group's size, and the group's index.
type GroupFn func(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *conformance.Probe, t, n, g uint32)

// Entry pairs an entry point's name with its Go function, one of the
// three kinds, and its dispatch: bounds, the params rows, and for a
// span entry the segment length.
type Entry struct {
	Name    string
	Index   IndexFn
	Span    SpanFn
	Group   GroupFn
	BoundX  uint32
	BoundY  uint32
	Rows    int // params rows: the stride is the Probe size when more than one
	Segment uint32
}

// Threads is the number of test vectors for the index entries, one
// per thread.
const Threads = 4096

// LayerWords is the length of the input and output layer buffers a
// suite test binds: a power of two, as every variable-length buffer's
// is, past the Threads by 8 words the inputs and outputs use.
const LayerWords = 1 << 19

func index(name string, fn IndexFn) Entry {
	return Entry{Name: name, Index: fn, BoundX: Threads, BoundY: 1, Rows: 1}
}

// Entries lists the conformance functions in the order the compile
// stage is given them, so entry point i is Entries[i].
var Entries = []Entry{
	index("Wrap8", conformance.Wrap8),
	index("Wrap16", conformance.Wrap16),
	index("Wrap32", conformance.Wrap32),
	index("Shifts", conformance.Shifts),
	index("DivRem", conformance.DivRem),
	index("Convert", conformance.Convert),
	index("Int64", conformance.Int64),
	index("Layout", conformance.Layout),
	index("Bytes", conformance.Bytes),
	index("Words", conformance.Words),
	index("ShortCircuit", conformance.ShortCircuit),
	index("Tables", conformance.Tables),
	index("Control", conformance.Control),
	index("CacheTail", conformance.CacheTail),
	// 1003 dots in segments of 8 over two rows with their own params:
	// the last segment clamps to 3 dots, and the grid of 126 segments
	// overshoots the workgroup it is rounded to.
	{Name: "Segments", Span: conformance.Segments, BoundX: 1003, BoundY: 2, Rows: 2, Segment: 8},
	// The same at twice the segment, the record a backend giving each
	// thread two units sends: the last segment of 16 clamps to 8 dots.
	{Name: "DoubleSegments", Span: conformance.DoubleSegments, BoundX: 1000, BoundY: 2, Rows: 2, Segment: 16},
	// Two groups with their own params.
	{Name: "Group", Group: conformance.Group, BoundY: 2, Rows: 2},
	// More threads than one dispatch may issue on DX12, so the backend
	// issues it in pieces.
	{Name: "Wide", Index: conformance.Wide, BoundX: conformance.WideThreads, BoundY: 1, Rows: 1},
}

// Names returns the entry names in order.
func Names() []string {
	names := make([]string, len(Entries))
	for i, e := range Entries {
		names[i] = e.Name
	}
	return names
}

// Grid is the entry's dispatch grid: threads across the bound, one
// per element or per segment, by rows; for a group entry one across
// and a row per group.
func (e Entry) Grid() [2]int {
	switch {
	case e.Span != nil:
		return [2]int{int((e.BoundX + e.Segment - 1) / e.Segment), int(e.BoundY)}
	case e.Group != nil:
		return [2]int{1, int(e.BoundY)}
	}
	return [2]int{int(e.BoundX), int(e.BoundY)}
}

// Sources are the compile stage's inputs for the conformance package:
// the parent compute package and the conformance package with every
// entry named.
func Sources() (parent, sub compile.Source) {
	const root = "github.com/user-none/erings/core/compute"
	return compile.Source{Path: root, Files: compute.Source},
		compile.Source{Path: root + "/conformance", Files: conformance.Source, Entries: Names()}
}

// AllSources builds every conformance entry as one module: the base
// entries, the atomic entries, and the variable-buffer entries, for a
// backend that compiles its shaders ahead of time and looks each up
// by name.
func AllSources() (parent, sub compile.Source) {
	parent, sub = Sources()
	sub.Entries = append(sub.Entries, RoundStateEntries[:]...)
	sub.Entries = append(sub.Entries, VariableEntries[:]...)
	return
}

// Lower runs the compile stage on the conformance package and returns
// the lowered module, entry i being Entries[i].
func Lower() (*compile.Module, error) {
	parent, sub := Sources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		return nil, err
	}
	return compile.Lower(prog)
}

// edge lists the words every entry sees first, so the extremes of
// each width meet each other before the random vectors start.
var edge = []uint32{
	0, 1, 0xFFFFFFFF, 0x80000000, 0x7FFFFFFF, 0x80, 0x7F, 0xFF, 0xFFFFFF80, 0xFFFFFF7F,
	0x8000, 0x7FFF, 0xFFFF, 0xFFFF8000, 0xFFFF7FFF, 2, 3, 0xFFFFFFFE, 0xFFFFFFFD, 31, 32, 63, 64, 100,
}

// Inputs fills the input words and the byte buffer deterministically:
// the first threads take every pair of edge words, the rest a fixed-
// seed generator.
func Inputs(in compute.Layer, mem *compute.VDP2VRAM) {
	next := uint32(0x2545F491)
	rnd := func() uint32 {
		next ^= next << 13
		next ^= next >> 17
		next ^= next << 5
		return next
	}
	pairs := len(edge) * len(edge)
	for i := 0; i < Threads; i++ {
		for j := 0; j < 8; j++ {
			var w uint32
			switch {
			case i < pairs && j == 0:
				w = edge[i/len(edge)]
			case i < pairs && j == 1:
				w = edge[i%len(edge)]
			default:
				w = rnd()
			}
			in[i*8+j] = w
		}
	}
	for a := 0; a < compute.VDP2VRAMSize; a++ {
		mem[a] = byte(rnd())
	}
}

// Probes returns the params rows: row 0 is the probe every index
// entry receives, row 1 differs in every field for the span entry's
// second row.
func Probes() [2]conformance.Probe {
	return [2]conformance.Probe{
		{
			A: 0xA1B2C3D4,
			B: -0x123456789ABCDEF,
			C: [3]uint32{0x11, 0x22, 0x33},
			D: conformance.Inner{E: -77, F: 0xFEDCBA9876543210},
			G: 0,
			H: true,
			L: 0x5A,
		},
		{
			A: 0x00010000,
			B: 42,
			C: [3]uint32{0x44, 0x55, 0x66},
			D: conformance.Inner{E: 5, F: 7},
			G: 1,
			K: true,
			L: 7,
		},
	}
}

// RunGo executes one entry on the Go side under the wrapper contract,
// over the grid its bounds give: for every grid position, the row's
// params and the segment range or the index. A group entry runs as
// one thread per group, thread 0 of 1, which executes the phases in
// order.
func RunGo(e Entry, in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, probes *[2]conformance.Probe) {
	grid := e.Grid()
	for gy := uint32(0); gy < uint32(grid[1]); gy++ {
		p := &probes[0]
		if e.Rows > 1 {
			p = &probes[gy]
		}
		if e.Group != nil {
			e.Group(in, mem, out, p, 0, 1, gy)
			continue
		}
		for gx := uint32(0); gx < uint32(grid[0]); gx++ {
			if e.Span != nil {
				x0 := gx * e.Segment
				x1 := min(x0+e.Segment, e.BoundX)
				e.Span(in, mem, out, p, int32(x0), int32(x1))
				continue
			}
			e.Index(in, mem, out, p, gx)
		}
	}
}
