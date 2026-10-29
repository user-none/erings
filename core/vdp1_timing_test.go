// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/internal/hostgpu"
)

// The timing walk: a VDP1 whose pair is not kept charges every
// command's cycles without drawing it (draw.DrawState.Timing). It
// must agree with the store walk tick for tick, in every register a
// CPU can read, in the entries a device draws, and in the line
// records those entries take.

// vdp1WalkState is what the two walks are compared on after every
// tick: the command processor's position and its CPU-visible
// registers, the command in progress's position, and the list.
type vdp1WalkState struct {
	debt       int32
	active     bool
	edsr       uint16
	copr, lopr uint16
	procAddr   uint32
	cmdPhase   int
	line, step uint32
	ops        int
}

func walkState(v *VDP1) vdp1WalkState {
	st := vdp1WalkState{
		debt: v.systemCycleDebt, active: v.drawActive, edsr: v.edsr, copr: v.copr, lopr: v.lopr,
		procAddr: v.procAddr, cmdPhase: v.cmdPhase, ops: len(v.ops),
	}
	if v.cmdPhase == phaseDraw {
		st.line, st.step = drawPosition(&v.cmd.st)
	}
	return st
}

// vdp1TimingBudgets are the tick budgets the walks are compared
// under, one per frame in turn: the frame loop's scanline and a
// small one that yields inside most lines, twice each, so a command
// in progress across frames meets both.
var vdp1TimingBudgets = []uint32{benchVDP1WalkerTick, 61, benchVDP1WalkerTick, 61}

// vdp1RuleTex is a 32x32 256-color texture for the texel rule scenes:
// transparent dots on every row, a pair of end codes on every row at
// columns 20 and 24, the second of which ends the row when end codes
// are checked, and drawn dots elsewhere.
const vdp1RuleTex = 0x2A000

func writeVDP1RuleTex(v *VDP1) {
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			d := uint8(1 + (x*7+y*3)%200)
			switch {
			case x == 20 || x == 24:
				d = 0xFF
			case (x+y)%5 == 0:
				d = 0
			}
			v.WriteVRAM(vdp1RuleTex+uint32(y*32+x), d)
		}
	}
}

// vdp1TexelRuleScenes are the scenes that reach every branch of the
// texel rule on every textured form: for normal, scaled enlarged,
// scaled shrunk, and distorted sprites, and the shrunk forms under
// high speed shrink, a sprite per combination of end codes checked
// or disabled, transparent dots drawn or not, and a one-cycle or a
// six-cycle color calculation mode, drawn from the rule texture.
func vdp1TexelRuleScenes() []benchVDP1Scene {
	forms := []struct {
		name string
		cmd  func(x int16, pmod uint16) benchCmd
	}{
		{"normal", func(x int16, pmod uint16) benchCmd {
			return benchCmd{pmod: pmod, colr: 0x0100, srca: vdp1RuleTex / 8, size: benchSize32, xa: x, ya: 20}
		}},
		{"scaled enlarge", func(x int16, pmod uint16) benchCmd {
			return benchCmd{ctrl: 0x0001, pmod: pmod, colr: 0x0100, srca: vdp1RuleTex / 8, size: benchSize32, xa: x, ya: 20, xc: x + 47, yc: 67}
		}},
		{"scaled shrink", func(x int16, pmod uint16) benchCmd {
			return benchCmd{ctrl: 0x0001, pmod: pmod, colr: 0x0100, srca: vdp1RuleTex / 8, size: benchSize32, xa: x, ya: 20, xc: x + 11, yc: 31}
		}},
		{"scaled shrink HSS", func(x int16, pmod uint16) benchCmd {
			return benchCmd{ctrl: 0x0001, pmod: pmod | 0x1000, colr: 0x0100, srca: vdp1RuleTex / 8, size: benchSize32, xa: x, ya: 20, xc: x + 11, yc: 31}
		}},
		{"distorted", func(x int16, pmod uint16) benchCmd {
			return benchCmd{ctrl: 0x0002, pmod: pmod, colr: 0x0100, srca: vdp1RuleTex / 8, size: benchSize32,
				xa: x, ya: 20, xb: x + 36, yb: 24, xc: x + 30, yc: 70, xd: x - 2, yd: 66}
		}},
		{"distorted shrink HSS", func(x int16, pmod uint16) benchCmd {
			return benchCmd{ctrl: 0x0002, pmod: pmod | 0x1000, colr: 0x0100, srca: vdp1RuleTex / 8, size: benchSize32,
				xa: x, ya: 20, xb: x + 40, yb: 20, xc: x + 24, yc: 60, xd: x + 16, yd: 60}
		}},
	}
	var scenes []benchVDP1Scene
	for _, f := range forms {
		scenes = append(scenes, benchVDP1Scene{name: "texel rule " + f.name, build: func() *VDP1 {
			v, l := benchVDP1()
			writeVDP1RuleTex(v)
			i := int16(0)
			for _, ecdOff := range []uint16{0, 0x0080} {
				for _, spdOn := range []uint16{0, 0x0040} {
					for _, cc := range []uint16{0, 3} {
						l.add(f.cmd(i*40, 4<<3|cc|ecdOff|spdOn))
						i++
					}
				}
			}
			l.end()
			return v
		}})
	}
	return scenes
}

// TestVDP1TimingMatchesStore drives every benchmark scene, frame
// scene, and texel rule scene through the store walk and the timing
// walk under the same budgets for four frames and holds the two
// equal after every tick: the walk state, the entries with their
// draw parameters, and the line records reserved so far.
func TestVDP1TimingMatchesStore(t *testing.T) {
	scenes := append(benchVDP1Scenes(), vdp1FrameScenes()...)
	scenes = append(scenes, vdp1TexelRuleScenes()...)
	for _, s := range scenes {
		t.Run(s.name, func(t *testing.T) {
			store, timing := s.build(), s.build()
			timing.setPairKept(false)
			for frame, budget := range vdp1TimingBudgets {
				benchVDP1Change(store)
				benchVDP1Change(timing)
				vdp1WalksEqual(t, store, timing, frame, -1, false)
				for tick := 0; tick < benchVDP1WalkerMaxTicks; tick++ {
					store.TickSystemCycles(budget)
					timing.TickSystemCycles(budget)
					vdp1WalksEqual(t, store, timing, frame, tick, false)
					if !store.drawActive && !timing.drawActive {
						break
					}
				}
				vdp1WalksEqual(t, store, timing, frame, -2, true)
				store.restartList()
				timing.restartList()
			}
		})
	}
}

// vdp1WalksEqual holds the timing walk equal to the store walk: the
// walk state, and the entries and line records, the whole of them
// when whole is set and otherwise the tail the last tick could have
// changed, the last two entries and the records of the command in
// progress, since an entry is fixed once another follows it and a
// command's records are stored as it draws.
func vdp1WalksEqual(t *testing.T, store, timing *VDP1, frame, tick int, whole bool) {
	t.Helper()
	if a, b := walkState(store), walkState(timing); a != b {
		t.Fatalf("frame %d tick %d: walk state differs: store %+v, timing %+v", frame, tick, a, b)
	}
	if len(store.ops) != len(timing.ops) {
		t.Fatalf("frame %d tick %d: entries: store %d, timing %d", frame, tick, len(store.ops), len(timing.ops))
	}
	from := max(len(store.ops)-2, 0)
	if whole {
		from = 0
	}
	for i := from; i < len(store.ops); i++ {
		if store.ops[i] != timing.ops[i] {
			t.Fatalf("frame %d tick %d: entry %d differs: store %+v, timing %+v", frame, tick, i, store.ops[i], timing.ops[i])
		}
	}
	if store.recordWords != timing.recordWords {
		t.Fatalf("frame %d tick %d: records reserved: store %d, timing %d", frame, tick, store.recordWords, timing.recordWords)
	}
	lo, hi := uint32(0), store.recordWords
	if !whole {
		lo, hi = 0, 0
		if store.cmdPhase == phaseDraw {
			lo = store.cmd.op.RecordBase
			hi = lo + draw.OpRecordWords(&store.cmd.op)
		}
	}
	a := store.lineRecords[lo:hi]
	b := timing.lineRecords[lo:hi]
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("frame %d tick %d: line record %d: store %d, timing %d", frame, tick, int(lo)+i, a[i], b[i])
		}
	}
}

// TestVDP1FBWriteFunnel holds where a CPU frame buffer write lands
// and what it records: with no device drawing, the draw buffer alone
// and no shadow, mask, or dirty page; with a device drawing and the
// pair kept, the draw buffer and the shadow, masked and marked; with
// the pair not kept, the shadow alone, masked and marked.
func TestVDP1FBWriteFunnel(t *testing.T) {
	v := NewVDP1(NewSCU())
	h := v.physicalBuffer(v.drawFB)
	check := func(name string, addr uint32, pair, shadow uint8, masked bool) {
		t.Helper()
		if v.drawFB[addr] != pair || v.fbShadow[h][addr] != shadow {
			t.Fatalf("%s: pair %#x shadow %#x, want %#x %#x", name, v.drawFB[addr], v.fbShadow[h][addr], pair, shadow)
		}
		if got := v.fbMask[h][addr>>6].Load()&(1<<(addr&63)) != 0; got != masked {
			t.Fatalf("%s: masked %v, want %v", name, got, masked)
		}
	}
	v.WriteFB(0x10, 0x11)
	check("no device", 0x10, 0x11, 0, false)
	if len(v.fbDirty[h].take(nil)) != 0 {
		t.Fatal("no device: a page was marked")
	}
	v.fbWritesTaken = true
	v.WriteFB(0x20, 0x22)
	check("device, pair kept", 0x20, 0x22, 0x22, true)
	v.setPairKept(false)
	v.WriteFB(0x30, 0x33)
	check("device, pair not kept", 0x30, 0, 0x33, true)
	v.WriteFB16(0x40, 0x4455)
	v.WriteFB32(0x50, 0x66778899)
	if v.fbShadow[h][0x41] != 0x55 || v.fbShadow[h][0x53] != 0x99 || v.drawFB[0x41] != 0 || v.drawFB[0x53] != 0 {
		t.Fatal("device, pair not kept: the word and long writes did not land in the shadow alone")
	}
	if pages := v.fbDirty[h].take(nil); len(pages) != 1 {
		t.Fatalf("device: %d pages marked, want 1", len(pages))
	}
}

// TestVDP1TakeFBPages holds a renderer's take of the CPU's frame
// buffer writes: the written pages land from the given slot in page
// order with their numbers, their mask words (low word first), and
// their shadow bytes, the masks are cleared by the take, and a take
// with nothing written adds no slot.
func TestVDP1TakeFBPages(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.fbWritesTaken = true
	h := v.physicalBuffer(v.drawFB)
	v.WriteFB(0x10, 0x11)
	v.WriteFB(3*compute.PageBytes+0x45, 0x22)
	table := make([]uint32, 8)
	masks := make([]uint32, 8*compute.PageBytes/32)
	store := make([]byte, 8*compute.PageBytes)
	slot, scratch := v.takeFBPages(h, 2, table, masks, store, nil)
	if slot != 4 || len(scratch) != 2 {
		t.Fatalf("take: slot %d, %d pages, want 4 and 2", slot, len(scratch))
	}
	if table[2] != 0 || table[3] != 3 {
		t.Fatalf("table %v, want pages 0 and 3 at slots 2 and 3", table[2:4])
	}
	if masks[2*(compute.PageBytes/32)] != 1<<0x10 || store[2*compute.PageBytes+0x10] != 0x11 {
		t.Fatalf("slot 2: mask word %#x byte %#x, want %#x and 0x11", masks[2*(compute.PageBytes/32)], store[2*compute.PageBytes+0x10], 1<<0x10)
	}
	// Byte 0x45 is bit 5 of the 64-byte group at 0x40, whose low word
	// is mask word 2 of the slot.
	if masks[3*(compute.PageBytes/32)+2] != 1<<5 || store[3*compute.PageBytes+0x45] != 0x22 {
		t.Fatalf("slot 3: mask word %#x byte %#x, want %#x and 0x22", masks[3*(compute.PageBytes/32)+2], store[3*compute.PageBytes+0x45], 1<<5)
	}
	if v.fbMask[h][0].Load() != 0 || v.fbMask[h][3*compute.PageBytes/64+1].Load() != 0 {
		t.Fatal("the take left mask bits set")
	}
	if slot, scratch = v.takeFBPages(h, slot, table, masks, store, scratch); slot != 4 || len(scratch) != 0 {
		t.Fatalf("empty take: slot %d, %d pages, want 4 and 0", slot, len(scratch))
	}
}

// TestVDP1WaitPairHandshake holds the read side of the pair request:
// a read while the pair is kept raises nothing, and a read while it
// is not raises the request and returns once the pair is kept.
func TestVDP1WaitPairHandshake(t *testing.T) {
	v := NewVDP1(NewSCU())
	v.drawFB[0x10] = 0x5A
	if got := v.ReadFB(0x10); got != 0x5A || v.pairRequest.Load() {
		t.Fatalf("a read with the pair kept: value %#x, request %v", got, v.pairRequest.Load())
	}
	v.setPairKept(false)
	done := make(chan uint8)
	go func() { done <- v.ReadFB(0x10) }()
	for !v.pairRequest.Load() {
		runtime.Gosched()
	}
	v.drawFB[0x10] = 0xA5
	v.setPairKept(true)
	v.pairRequest.Store(false)
	if got := <-done; got != 0xA5 {
		t.Fatalf("the read returned %#x before the pair was brought current", got)
	}
}

// TestSerializeSyncsDevicePair holds that a save state captured in
// GPU mode carries the device pair: the renderer's halves are marked
// and the state's frame buffer chunks hold the marks.
func TestSerializeSyncsDevicePair(t *testing.T) {
	e := NewEmulator()
	parent, subs := ComputeSources()
	prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGPURenderer(e.vdp2, e.vdp1, prog, gpuBatchLines, false)
	if err != nil {
		t.Fatal(err)
	}
	e.renderer.close()
	e.renderer, e.rendererMode = g, RendererGPU
	e.vdp1.setPairKept(false)
	execData(g.vdp1FB[0])[0x1234] = 0x3C
	execData(g.vdp1FB[1])[0x4321] = 0xC3
	data, err := e.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	_, chunks := parseStateForTest(t, data)
	drawFB, dispFB := chunks[tagVDP1DrawFB]["data"], chunks[tagVDP1DispFB]["data"]
	if drawFB[0x1234] != 0x3C || dispFB[0x4321] != 0xC3 {
		t.Fatalf("the state's pair does not hold the device pair: %#x, %#x", drawFB[0x1234], dispFB[0x4321])
	}
	if !bytes.Equal(drawFB, e.vdp1.physicalFB(0)) || !bytes.Equal(dispFB, e.vdp1.physicalFB(1)) {
		t.Fatal("the state's pair differs from the synced CPU pair")
	}
	if e.vdp1.pairKept.Load() {
		t.Fatal("a save state capture switched the walk to keeping the pair")
	}
}

func BenchmarkVDP1Timing(b *testing.B) {
	for _, s := range benchVDP1Scenes() {
		b.Run(s.name, func(b *testing.B) {
			v := s.build()
			v.setPairKept(false)
			benchVDP1Frame(v)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchVDP1Frame(v)
			}
		})
	}
}

func BenchmarkVDP1WalkerTiming(b *testing.B) {
	for _, s := range benchVDP1Scenes() {
		b.Run(s.name, func(b *testing.B) {
			v := s.build()
			v.setPairKept(false)
			benchVDP1FrameWalker(b, v)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchVDP1FrameWalker(b, v)
			}
		})
	}
}
