// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package spirv

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
	"github.com/user-none/erings/internal/hostgpu/compile"
	"github.com/user-none/erings/internal/hostgpu/conformance"
)

// dumpModules writes every module the tests emit under
// investigation/spirv as <test>_<entry>.spv for inspection with
// external tools.
const dumpModules = false

func dump(t *testing.T, mod *compile.Module, out *Output) {
	t.Helper()
	if !dumpModules {
		return
	}
	dir := filepath.Join("..", "..", "..", "..", "investigation", "spirv")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for ei, k := range out.Entries {
		buf := make([]byte, 4*len(k.Code))
		for i, w := range k.Code {
			binary.LittleEndian.PutUint32(buf[4*i:], w)
		}
		name := mod.Funcs[mod.Entries[ei].Func].Name
		if err := os.WriteFile(filepath.Join(dir, t.Name()+"_"+name+".spv"), buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const parentPath = "example.com/compute"

const parentSrc = `package compute

const BufSize = 1024
const WordCount = 256

type Buf [BufSize]byte
type Words [WordCount]uint32
type Variable []uint32

func (b *Buf) U8(a uint32) uint8 { return b[a&(BufSize-1)] }

func (b *Buf) U16BE(a uint32) uint16 {
	return uint16(b[a&(BufSize-1)])<<8 | uint16(b[(a+1)&(BufSize-1)])
}

func (b *Buf) PutU8(a uint32, v uint8) { b[a&(BufSize-1)] = v }
`

// entrySrc is the fixture with a bool switch and a continue
// inside a switch inside a loop, the two forms the structured
// lowering handles specially.
const entrySrc = `package sub

import "example.com/compute"

var table = [4]uint32{1, 2, 4, 8}

type Inner struct {
	Scale int64
}

type Params struct {
	Base  uint32
	Ratio [2]int32
	Inner Inner
	On    bool
}

type State struct {
	Pos int32
}

type stepper struct {
	x, dx int32
}

func (s *stepper) step() { s.x += s.dx }

func two(a int32) (int32, bool) { return a + 1, a > 0 }

func narrow(a, b uint8, c int16) uint32 {
	d := a + b
	if int16(d)*c == -3 {
		return 1
	}
	return uint32(d&^b) | uint32(c)
}

func Entry(in *compute.Buf, out *compute.Words, p *Params, st *State, x uint32, y int32) {
	var s stepper = stepper{x: int32(x), dx: 1}
	s.step()
	v, ok := two(s.x)
	if ok && p.Ratio[v&1] > 0 || y == 2 {
		st.Pos = v
	}
	acc := uint32(in.U8(x)) + uint32(in.U16BE(x + 2))
	switch y {
	case 0, 1:
		acc += p.Base
	default:
		acc += table[x&3]
	}
	switch ok {
	case true:
		acc++
	}
	for i := int32(0); i < 4; i++ {
		switch i {
		case 2:
			continue
		}
		acc += uint32(min(i, st.Pos)) + narrow(uint8(acc), 3, int16(i))
	}
	acc += uint32(min(max(p.Inner.Scale, -1), 1) + max(p.Inner.Scale, -p.Inner.Scale))
	if p.On {
		acc++
	}
	out[x] = acc
	in.PutU8(x, uint8(acc))
}
`

func emitSrc(t *testing.T) *Output {
	t.Helper()
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(entrySrc)}}, Entries: []string{"Entry"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	dump(t, mod, out)
	return out
}

// TestEmitEntry emits the fixture and checks the entry record, the
// module's structure, the push block, and that the module holds the
// five functions the entry reaches plus the shell.
func TestEmitEntry(t *testing.T) {
	out := emitSrc(t)
	if len(out.Entries) != 1 {
		t.Fatalf("entries = %d", len(out.Entries))
	}
	k := out.Entries[0]
	if k.ParamsBinding != 2 || k.Workgroup != [2]int{8, 8} || len(k.Buffers) != 2 || k.BufferLengths || k.PushSize != 4*PushWords {
		t.Errorf("entry point record %+v", k)
	}
	d := checkModule(t, k)
	pushBlock(t, k, d)
	if n := checkFunctions(t, d); n != 6 {
		t.Errorf("%d functions, want the 5 reached and the shell", n)
	}
	var switches, phis, selects, atomics, notEquals int
	for _, in := range d.insts {
		switch in.opcode {
		case opSwitch:
			switches++
		case opPhi:
			phis++
		case opSelect:
			selects++
		case opAtomicUMax, opAtomicIAdd, opAtomicExchange:
			atomics++
		case opINotEqual:
			notEquals++
		}
	}
	if switches != 3 || selects != 1 || phis != 0 || atomics != 0 {
		t.Errorf("%d switches, %d selects, %d phis, %d atomics", switches, selects, phis, atomics)
	}
	// The fixture compares nothing for inequality: the one comparison
	// is the params row's bool, its byte tested against zero.
	if notEquals != 1 {
		t.Errorf("%d integer inequality tests, want the bool field's one", notEquals)
	}
}

// byteParentSrc and byteEntrySrc use a variable-length byte buffer
// beside a variable-length word buffer: its accessors, its length,
// and passed on to a helper.
const byteParentSrc = `package compute

type Bytes []byte
type Variable []uint32

func (b Bytes) U8(a uint32) uint8 { return b[int(a)&(len(b)-1)] }

func (b Bytes) U16BE(a uint32) uint16 {
	return uint16(b[int(a)&(len(b)-1)])<<8 | uint16(b[int(a+1)&(len(b)-1)])
}

func (b Bytes) PutU16BE(a uint32, v uint16) {
	b[int(a)&(len(b)-1)], b[int(a+1)&(len(b)-1)] = uint8(v>>8), uint8(v)
}
`

const byteEntrySrc = `package sub

import "example.com/compute"

func put(b compute.Bytes, a uint32, v uint16) { b.PutU16BE(a, v) }

func Entry(b compute.Bytes, w compute.Variable, x uint32) {
	n := uint32(len(b))
	w[x] = uint32(b.U8(x)) + uint32(b.U16BE(x+n)) + n
	put(b, x, uint16(x))
}
`

// TestEmitVariableByteBuffers checks a variable-length byte buffer:
// its block is bytes, both bindings take a length word, and the only
// multiply in the module is the shell's scaling of the byte buffer's
// word count to its bytes; the source multiplies nothing.
func TestEmitVariableByteBuffers(t *testing.T) {
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(byteParentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(byteEntrySrc)}}, Entries: []string{"Entry"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	k := out.Entries[0]
	if !k.BufferLengths || k.PushSize != 4*(PushWords+2) {
		t.Fatalf("byte entry lengths %v push size %d", k.BufferLengths, k.PushSize)
	}
	d := checkModule(t, k)
	pushBlock(t, k, d)
	checkFunctions(t, d)
	strides := map[uint32]uint32{}
	for _, in := range d.insts {
		if in.opcode != opVariable {
			continue
		}
		binding, ok := d.bindings[in.operands[1]]
		if !ok {
			continue
		}
		block := d.types[d.types[in.operands[0]].operands[2]]
		strides[binding] = d.strides[block.operands[1]]
	}
	if strides[0] != 1 || strides[1] != 4 {
		t.Errorf("binding strides %v, want the byte buffer 1 and the word buffer 4", strides)
	}
	four := map[uint32]bool{}
	for _, in := range d.insts {
		if in.opcode == opConstant && len(in.operands) == 3 && in.operands[2] == 4 {
			four[in.operands[1]] = true
		}
	}
	var muls, byFour int
	for _, in := range d.insts {
		if in.opcode == opIMul {
			muls++
			if four[in.operands[3]] {
				byFour++
			}
		}
	}
	if muls != 1 || byFour != 1 {
		t.Errorf("%d multiplies, %d by four, want the one length scaling", muls, byFour)
	}
}

// emitFixture emits the fixture's Entry over the parent source and
// decodes its module.
func emitFixture(t *testing.T, src string) (EntryPoint, *decoded) {
	t.Helper()
	prog, err := compile.Load(
		compile.Source{Path: parentPath, Files: fstest.MapFS{"buffers.go": {Data: []byte(parentSrc)}}},
		compile.Source{Path: parentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(src)}}, Entries: []string{"Entry"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	dump(t, mod, out)
	k := out.Entries[0]
	return k, checkModule(t, k)
}

// functionBodies is each named function's instructions, from its
// OpFunction to its OpFunctionEnd.
func functionBodies(d *decoded) map[string][]inst {
	out := map[string][]inst{}
	name := ""
	for _, in := range d.insts {
		switch in.opcode {
		case opFunction:
			name = d.names[in.operands[1]]
		case opFunctionEnd:
			name = ""
		}
		if name != "" {
			out[name] = append(out[name], in)
		}
	}
	return out
}

// constants is the set of ids OpConstant defines.
func constants(d *decoded) map[uint32]bool {
	out := map[uint32]bool{}
	for _, in := range d.insts {
		if in.opcode == opConstant {
			out[in.operands[1]] = true
		}
	}
	return out
}

// indexEntrySrc indexes local arrays at run time: a whole element and a
// field written, an element read by value, an element passed by
// reference to a function that writes through it and to one that only
// reads, and a local copy of a params row array read at a run-time
// index.
const indexEntrySrc = `package sub

import "example.com/compute"

type Cand struct {
	Pri uint32
	On  bool
}

type Params struct {
	Vals  [5]uint32
	Flags [6]bool
	Rows  [3][4]uint32
}

func bump(c *Cand) { c.Pri++ }

func above(a, b *Cand) bool { return a.Pri > b.Pri }

func pick(c Cand) uint32 {
	if c.On {
		return c.Pri
	}
	return 0
}

func Entry(out *compute.Words, p *Params, x uint32) {
	var cs [4]Cand
	i := x & 3
	cs[i] = Cand{Pri: p.Vals[x%5], On: p.Flags[x%6]}
	cs[i].Pri += 2
	row := p.Rows[x%3]
	acc := pick(cs[(i+1)&3]) + row[x&3]
	bump(&cs[i])
	if above(&cs[i], &cs[(i+1)&3]) {
		acc++
	}
	out[x&255] = acc + cs[0].Pri
}
`

// TestEmitRuntimeIndexChains checks that an array of at most selectMax
// elements in a path rooted in a local is never indexed at a run-time
// index through a Function pointer: reads are select chains or
// switches, a reference argument the callee only reads is a copy, and
// writes are switches over the constant indexes. The reference
// argument the callee writes through keeps the indexed form.
func TestEmitRuntimeIndexChains(t *testing.T) {
	_, d := emitFixture(t, indexEntrySrc)
	consts := constants(d)
	entry := functionBodies(d)["Entry"]
	if entry == nil {
		t.Fatal("no Entry function")
	}
	selects, switches, indexed := 0, 0, 0
	for _, in := range entry {
		switch in.opcode {
		case opSelect:
			selects++
		case opSwitch:
			switches++
		case opAccessChain:
			ptr := d.types[in.operands[0]]
			if ptr.operands[1] != storageFunction {
				continue
			}
			for _, idx := range in.operands[3:] {
				if !consts[idx] {
					indexed++
				}
			}
		}
	}
	if selects == 0 || switches == 0 {
		t.Errorf("Entry has %d selects and %d switches, want both", selects, switches)
	}
	if indexed != 1 {
		t.Errorf("%d run-time indexes through Function pointers, want 1 (bump's argument)", indexed)
	}
}

// paramsEntrySrc reads its params row at run-time indexes directly and
// through a helper passed the row, and passes a changed copy of the row
// to a helper that reads it the same way.
const paramsEntrySrc = `package sub

import "example.com/compute"

type Pair struct {
	A, B uint32
}

type Row struct {
	Vals  [4]uint32
	Pairs [2]Pair
	On    [3]bool
	Wide  [2]int64
}

func helper(r *Row, i uint32) uint32 { return r.Vals[i&3] }

func copied(r *Row, i uint32) uint32 { return r.Vals[i&3] }

func Entry(out *compute.Words, p *Row, x uint32) {
	acc := p.Vals[x&3] + helper(p, x)
	q := p.Pairs[x&1]
	acc += q.A
	if p.On[x%3] {
		acc++
	}
	acc += uint32(p.Wide[x&1])
	c := *p
	c.Vals[0] = 7
	acc += copied(&c, x)
	out[x&255] = acc
}
`

// TestEmitParamsInPlace checks that a read of the params row at a
// run-time index is a load through the params buffer's variable, in the
// function the row is passed to as well, while a copy of the row is
// read as the copy, and that the wrapper stores the row's address.
func TestEmitParamsInPlace(t *testing.T) {
	k, d := emitFixture(t, paramsEntrySrc)
	var params, prow uint32
	for v, b := range d.bindings {
		if int(b) == k.ParamsBinding {
			params = v
		}
	}
	for id, name := range d.names {
		if name == "prow" {
			prow = id
		}
	}
	if params == 0 || prow == 0 {
		t.Fatalf("params variable %d, row address variable %d", params, prow)
	}
	bodies := functionBodies(d)
	reads := func(name string) int {
		n := 0
		for _, in := range bodies[name] {
			if in.opcode == opAccessChain && in.operands[2] == params {
				n++
			}
		}
		return n
	}
	if reads("Entry") == 0 {
		t.Error("Entry does not read the row in place")
	}
	if reads("helper") == 0 {
		t.Error("helper, passed the row, does not read it in place")
	}
	if n := reads("copied"); n != 0 {
		t.Errorf("copied, passed a changed copy, reads the params buffer %d times", n)
	}
	stored := false
	for _, in := range bodies["Entry.wrapper"] {
		if in.opcode == opStore && in.operands[0] == prow {
			stored = true
		}
	}
	if !stored {
		t.Error("the wrapper does not store the row's address")
	}
}

// shortCircuitSrc guards an index past selectMax with && and a
// division with ||, right operands without calls.
const shortCircuitSrc = `package sub

import "example.com/compute"

func Entry(out *compute.Words, x uint32) {
	var a [12]uint32
	a[3] = x
	d := x & 7
	n := uint32(0)
	if x < 12 && a[x] > 0 {
		n++
	}
	if d == 0 || x/d > 1 {
		n++
	}
	out[x&255] = n
}
`

// TestEmitShortCircuit checks that && and || evaluate a right operand
// without calls only in the branch the left operand selects: the
// indexed read in the true branch of the &&, the division in the false
// branch of the ||, and no logical operator taking both operands.
func TestEmitShortCircuit(t *testing.T) {
	_, d := emitFixture(t, shortCircuitSrc)
	consts := constants(d)
	entry := functionBodies(d)["Entry"]
	if entry == nil {
		t.Fatal("no Entry function")
	}
	onTrue, onFalse, merges := map[uint32]bool{}, map[uint32]bool{}, map[uint32]bool{}
	for _, in := range entry {
		switch in.opcode {
		case opBranchConditional:
			onTrue[in.operands[1]] = true
			onFalse[in.operands[2]] = true
		case opSelectionMerge:
			merges[in.operands[0]] = true
		}
	}
	label := uint32(0)
	indexed, divided := false, false
	for _, in := range entry {
		switch in.opcode {
		case opLabel:
			label = in.operands[0]
		case opLogicalAnd, opLogicalOr:
			t.Errorf("logical operator %d evaluates both operands", in.opcode)
		case opAccessChain:
			if d.types[in.operands[0]].operands[1] != storageFunction || consts[in.operands[3]] {
				continue
			}
			indexed = true
			if !onTrue[label] || merges[label] {
				t.Error("the read the && guards is outside its true branch")
			}
		case opUDiv:
			divided = true
			if !onFalse[label] || merges[label] {
				t.Error("the division the || guards is outside its false branch")
			}
		}
	}
	if !indexed || !divided {
		t.Fatalf("indexed read %v, division %v, want both", indexed, divided)
	}
}

// TestEmitWideCaseLiteral checks that a uint64 switch case at or past
// 2^63 is the case's bits as two literal words, low word first.
func TestEmitWideCaseLiteral(t *testing.T) {
	const src = `package sub

import "example.com/compute"

type P struct{ V uint64 }

func Entry(out *compute.Words, p *P, x uint32) {
	n := uint32(0)
	switch p.V + uint64(x) {
	case 0x8000000000000001:
		n = 1
	case 2:
		n = 2
	}
	out[x&255] = n
}
`
	_, d := emitFixture(t, src)
	for _, in := range functionBodies(d)["Entry"] {
		if in.opcode != opSwitch {
			continue
		}
		cases := in.operands[2:]
		want := [][2]uint32{{1, 0x80000000}, {2, 0}}
		for _, w := range want {
			found := false
			for i := 0; i+2 < len(cases); i += 3 {
				found = found || cases[i] == w[0] && cases[i+1] == w[1]
			}
			if !found {
				t.Errorf("no case with literal words %#x %#x in %#x", w[0], w[1], cases)
			}
		}
		return
	}
	t.Fatal("Entry has no switch")
}

// TestEmitWideNegation checks that negating a 64-bit integer is a
// subtraction from a 64-bit zero, whose constant decode holds to two
// value words.
func TestEmitWideNegation(t *testing.T) {
	const src = `package sub

import "example.com/compute"

type P struct{ V int64 }

func Entry(out *compute.Words, p *P, x uint32) {
	out[x&255] = uint32(-(p.V + int64(x)))
}
`
	_, d := emitFixture(t, src)
	consts := constants(d)
	for _, in := range functionBodies(d)["Entry"] {
		if in.opcode != opISub {
			continue
		}
		if ty := d.types[in.operands[0]]; ty.opcode == opTypeInt && ty.operands[1] == 64 && consts[in.operands[2]] {
			return
		}
	}
	t.Fatal("Entry has no 64-bit subtraction from a constant")
}

// TestEmitSelectTail checks the then block's trailing assignments of a
// value the block does not change: an if with no else makes them as
// selects on the branch's condition after the block, and the forms the
// rule excludes keep their stores in the block.
func TestEmitSelectTail(t *testing.T) {
	const head = `package sub

import "example.com/compute"

type P struct{ N uint32 }

func set(v *uint32, c uint32, on bool) {
	if on {
		*v = c
	}
}

func bump(v *uint32) { *v++ }

`
	cases := []struct {
		name string
		body string
		want int
	}{
		// The cache's key, through a local declared in the block, and
		// its flag, a constant.
		{"cache", `
	have, last, n := false, uint32(0), uint32(0)
	for i := uint32(0); i < p.N; i++ {
		c := (x + i) >> 2
		if !have || c != last {
			n += c * 3
			k := c
			last, have = k, true
		}
	}
	out[x&255] = n + last`, 2},
		// The value is written in the block.
		{"written", `
	last := uint32(0)
	c := x >> 2
	if c != p.N {
		c++
		last = c
	}
	out[x&255] = last`, 0},
		// The value is written through a call.
		{"call", `
	last := uint32(0)
	c := x >> 2
	if c != p.N {
		bump(&c)
		last = c
	}
	out[x&255] = last`, 0},
		// The if has an else.
		{"else", `
	last, n := uint32(0), uint32(0)
	c := x >> 2
	if c != p.N {
		last = c
	} else {
		n++
	}
	out[x&255] = last + n`, 0},
		// The target is a reference parameter.
		{"reference", `
	last := uint32(0)
	set(&last, x>>2, x&1 != 0)
	out[x&255] = last`, 0},
		// A swap reads values the block writes.
		{"swap", `
	a, b := x, p.N
	if x&1 != 0 {
		a, b = b, a
	}
	out[x&255] = a ^ b<<1`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := head + "func Entry(out *compute.Words, p *P, x uint32) {" + c.body + "\n}\n"
			_, d := emitFixture(t, src)
			conds := map[uint32]bool{}
			var sels []inst
			for _, in := range d.insts {
				switch in.opcode {
				case opBranchConditional:
					conds[in.operands[0]] = true
				case opSelect:
					sels = append(sels, in)
				}
			}
			if len(sels) != c.want {
				t.Fatalf("%d selects, want %d", len(sels), c.want)
			}
			for _, in := range sels {
				if !conds[in.operands[2]] {
					t.Errorf("select %d's condition %d is no branch's", in.operands[1], in.operands[2])
				}
			}
		})
	}
}

func TestEmitDeterministic(t *testing.T) {
	a := emitSrc(t).Entries[0].Code
	b := emitSrc(t).Entries[0].Code
	if len(a) != len(b) {
		t.Fatal("emitted modules differ in length between runs")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("emitted modules differ at word %d", i)
		}
	}
}

// TestEmitConformance emits the conformance module and checks every
// entry's module, the group entry's built-ins, and that the atomic
// entries carry their atomics.
func TestEmitConformance(t *testing.T) {
	parent, sub := conformance.AllSources()
	prog, err := compile.Load(parent, sub)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	dump(t, mod, out)
	if len(out.Entries) != len(mod.Entries) {
		t.Fatalf("entries = %d, want %d", len(out.Entries), len(mod.Entries))
	}
	for i, k := range out.Entries {
		en := mod.Entries[i]
		name := mod.Funcs[en.Func].Name
		t.Run(name, func(t *testing.T) {
			d := checkModule(t, k)
			pushBlock(t, k, d)
			builtins := 0
			for _, storage := range d.globals {
				if storage == storageInput {
					builtins++
				}
			}
			if want := map[bool]int{false: 1, true: 3}[en.Group]; builtins != want {
				t.Errorf("%d built-in inputs, want %d", builtins, want)
			}
			var atomics, barriers int
			for _, in := range d.insts {
				switch in.opcode {
				case opAtomicUMax, opAtomicIAdd, opAtomicExchange:
					atomics++
				case opControlBarrier:
					barriers++
				}
			}
			switch name {
			case "RaiseWords", "AddWords", "SwapChain":
				if atomics == 0 {
					t.Error("no atomics")
				}
			case "Group":
				if barriers == 0 {
					t.Error("no barrier")
				}
			default:
				if !k.BufferLengths || k.PushSize != 4*(PushWords+len(k.Buffers)) {
					t.Errorf("lengths %v push size %d", k.BufferLengths, k.PushSize)
				}
			}
		})
	}
}

// TestEmitComputePackages emits the real compute packages and checks
// every entry's module.
func TestEmitComputePackages(t *testing.T) {
	const root = "github.com/user-none/erings/core/compute"
	prog, err := compile.Load(
		compile.Source{Path: root, Files: compute.Source},
		compile.Source{Path: root + "/screen", Files: screen.Source, Entries: screen.Entries[:]},
		compile.Source{Path: root + "/draw", Files: draw.Source, Entries: draw.Entries[:]},
	)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.Lower(prog)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(mod)
	if err != nil {
		t.Fatal(err)
	}
	dump(t, mod, out)
	if want := len(screen.Entries) + len(draw.Entries); len(out.Entries) != want {
		t.Fatalf("entries = %d, want %d", len(out.Entries), want)
	}
	for i, k := range out.Entries {
		t.Run(mod.Funcs[mod.Entries[i].Func].Name, func(t *testing.T) {
			pushBlock(t, k, checkModule(t, k))
		})
	}
}

// TestBuilderRefusesLaidOutLocal checks that the builder refuses a
// Function pointer to a laid-out type.
func TestBuilderRefusesLaidOutLocal(t *testing.T) {
	b := newBuilder()
	blk := b.block(b.tU32(), 4)
	b.ptr(storageStorageBuffer, blk)
	if b.err != nil {
		t.Fatal(b.err)
	}
	b.ptr(storageFunction, blk)
	if b.err == nil {
		t.Fatal("a Function pointer to a block was accepted")
	}
}
