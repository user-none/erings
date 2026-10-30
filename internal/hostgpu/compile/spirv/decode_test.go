// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package spirv

import (
	"testing"
)

// inst is one decoded instruction.
type inst struct {
	opcode   uint32
	operands []uint32
}

// decoded is a module decoded far enough to check its structure.
type decoded struct {
	bound    uint32
	insts    []inst
	caps     map[uint32]bool
	exts     []string
	entry    *inst
	local    [3]uint32
	bindings map[uint32]uint32 // variable id to binding
	sets     map[uint32]uint32 // variable id to descriptor set
	offsets  map[uint32][]uint32
	strides  map[uint32]uint32
	names    map[uint32]string
	types    map[uint32]inst
	values   map[uint32]uint32 // typed result id to its type id
	globals  map[uint32]uint32 // module-scope variable id to its storage class
	ids      map[uint32]bool
}

// decode splits the module into instructions, checking the header,
// the word counts, and that every result id is unique and below the
// bound.
func decode(t *testing.T, code []uint32) *decoded {
	t.Helper()
	if len(code) < 5 || code[0] != magic || code[1] != version {
		t.Fatalf("bad header %x", code[:min(5, len(code))])
	}
	d := &decoded{bound: code[3], caps: map[uint32]bool{}, bindings: map[uint32]uint32{}, sets: map[uint32]uint32{},
		offsets: map[uint32][]uint32{}, strides: map[uint32]uint32{}, names: map[uint32]string{}, types: map[uint32]inst{}, values: map[uint32]uint32{}, globals: map[uint32]uint32{}, ids: map[uint32]bool{}}
	for i := 5; i < len(code); {
		n := int(code[i] >> 16)
		if n == 0 || i+n > len(code) {
			t.Fatalf("instruction at word %d has count %d past the module's %d words", i, n, len(code))
		}
		in := inst{opcode: code[i] & 0xffff, operands: code[i+1 : i+n]}
		d.insts = append(d.insts, in)
		i += n
	}
	inFunc := false
	for _, in := range d.insts {
		switch in.opcode {
		case opFunction:
			inFunc = true
		case opFunctionEnd:
			inFunc = false
		case opVariable:
			if !inFunc {
				d.globals[in.operands[1]] = in.operands[2]
			}
		}
		if id, typed, ok := resultID(in); ok {
			if id == 0 || id >= d.bound {
				t.Fatalf("result id %d outside 1..%d", id, d.bound-1)
			}
			if d.ids[id] {
				t.Fatalf("result id %d defined twice", id)
			}
			d.ids[id] = true
			if typed {
				d.values[id] = in.operands[0]
			}
		}
		switch in.opcode {
		case opCapability:
			d.caps[in.operands[0]] = true
		case opExtension:
			d.exts = append(d.exts, literal(in.operands))
		case opEntryPoint:
			e := in
			d.entry = &e
		case opExecutionMode:
			if in.operands[1] == executionModeLocalSize {
				copy(d.local[:], in.operands[2:5])
			}
		case opDecorate:
			switch in.operands[1] {
			case decorationBinding:
				d.bindings[in.operands[0]] = in.operands[2]
			case decorationDescriptorSet:
				d.sets[in.operands[0]] = in.operands[2]
			case decorationArrayStride:
				d.strides[in.operands[0]] = in.operands[2]
			}
		case opMemberDecorate:
			if in.operands[2] == decorationOffset {
				d.offsets[in.operands[0]] = append(d.offsets[in.operands[0]], in.operands[3])
			}
		case opName:
			d.names[in.operands[0]] = literal(in.operands[1:])
		case opTypeVoid, opTypeBool, opTypeInt, opTypeFloat, opTypeVector, opTypeArray, opTypeRuntimeArray, opTypeStruct, opTypePointer, opTypeFunction:
			d.types[in.operands[0]] = in
		case opConstant:
			// A scalar constant's value is one word per 32 bits of its
			// type's width, after the type and the result id.
			ty, ok := d.types[in.operands[0]]
			if !ok || ty.opcode != opTypeInt && ty.opcode != opTypeFloat {
				t.Fatalf("constant %d of type %d, not a declared scalar", in.operands[1], in.operands[0])
			}
			if want := int(ty.operands[1]+31) / 32; len(in.operands)-2 != want {
				t.Fatalf("constant %d of width %d has %d value words, want %d", in.operands[1], ty.operands[1], len(in.operands)-2, want)
			}
		}
	}
	return d
}

// resultID is the instruction's result id, for the opcodes that
// have one, and whether the id follows a result type.
func resultID(in inst) (id uint32, typed, ok bool) {
	switch in.opcode {
	case opExtInstImport, opTypeVoid, opTypeBool, opTypeInt, opTypeFloat, opTypeVector, opTypeArray, opTypeRuntimeArray, opTypeStruct, opTypePointer, opTypeFunction, opLabel:
		return in.operands[0], false, true
	case opConstantTrue, opConstantFalse, opConstant, opConstantComposite, opConstantNull, opFunction, opFunctionParameter, opFunctionCall, opVariable, opLoad, opAccessChain,
		opCompositeConstruct, opCompositeExtract, opConvertFToU, opConvertFToS, opConvertSToF, opConvertUToF, opUConvert, opSConvert, opBitcast, opSNegate, opFNegate,
		opIAdd, opFAdd, opISub, opFSub, opIMul, opFMul, opUDiv, opSDiv, opFDiv, opUMod, opSRem, opFRem, opLogicalEqual, opLogicalNotEqual, opLogicalOr, opLogicalAnd, opLogicalNot,
		opSelect, opIEqual, opINotEqual, opUGreaterThan, opSGreaterThan, opUGreaterThanEqual, opSGreaterThanEqual, opULessThan, opSLessThan, opULessThanEqual, opSLessThanEqual,
		opFOrdEqual, opFUnordNotEqual, opFOrdLessThan, opFOrdGreaterThan, opFOrdLessThanEqual, opFOrdGreaterThanEqua, opShiftRightLogical, opShiftRightArith, opShiftLeftLogical,
		opBitwiseOr, opBitwiseXor, opBitwiseAnd, opNot, opAtomicExchange, opAtomicIAdd, opAtomicUMax, opPhi, opExtInst:
		return in.operands[1], true, true
	}
	return 0, false, false
}

// section ranks an instruction outside a function by the logical
// layout of a module: capabilities, extensions, imports, the memory
// model, entry points, execution modes, debug names, annotations,
// then types, constants, and global variables, then functions. Zero
// is an instruction that belongs inside a function.
func section(opcode uint32) int {
	switch opcode {
	case opCapability:
		return 1
	case opExtension:
		return 2
	case opExtInstImport:
		return 3
	case opMemoryModel:
		return 4
	case opEntryPoint:
		return 5
	case opExecutionMode:
		return 6
	case opName:
		return 7
	case opDecorate, opMemberDecorate:
		return 8
	case opTypeVoid, opTypeBool, opTypeInt, opTypeFloat, opTypeVector, opTypeArray, opTypeRuntimeArray, opTypeStruct, opTypePointer, opTypeFunction,
		opConstantTrue, opConstantFalse, opConstant, opConstantComposite, opConstantNull, opVariable:
		return 9
	case opFunction:
		return 10
	}
	return 0
}

// checkOrder holds the module's instructions to the logical layout:
// every section after the one before, nothing of the preamble after
// the first function, and no instruction of a function's body outside
// one.
func checkOrder(t *testing.T, d *decoded) {
	t.Helper()
	last := 0
	inFunc := false
	for i, in := range d.insts {
		if inFunc {
			if in.opcode == opFunctionEnd {
				inFunc = false
			}
			continue
		}
		s := section(in.opcode)
		if s == 0 {
			t.Fatalf("instruction %d (opcode %d) outside a function", i, in.opcode)
		}
		if s < last {
			t.Fatalf("instruction %d (opcode %d) of section %d after section %d", i, in.opcode, s, last)
		}
		last = s
		inFunc = in.opcode == opFunction
	}
}

// literal decodes a string literal.
func literal(w []uint32) string {
	var b []byte
	for _, x := range w {
		for i := 0; i < 4; i++ {
			c := byte(x >> (8 * i))
			if c == 0 {
				return string(b)
			}
			b = append(b, c)
		}
	}
	return string(b)
}

func terminator(opcode uint32) bool {
	switch opcode {
	case opBranch, opBranchConditional, opSwitch, opReturn, opReturnValue, opUnreachable:
		return true
	}
	return false
}

// switchTargets is the labels an OpSwitch branches to: the default
// and every case's, the case literals one word for a 32-bit selector
// and two for a 64-bit one.
func switchTargets(t *testing.T, d *decoded, in inst) []uint32 {
	t.Helper()
	sel := d.types[d.values[in.operands[0]]]
	if sel.opcode != opTypeInt {
		t.Fatalf("switch selector %d is not an integer", in.operands[0])
	}
	words := int(sel.operands[1] / 32)
	targets := []uint32{in.operands[1]}
	for i := 2; i < len(in.operands); i += words + 1 {
		targets = append(targets, in.operands[i+words])
	}
	return targets
}

// checkFunctions walks every function: parameters then the entry
// label, every variable first in the entry block, a label at the
// start of every block, a terminator at the end of each, no
// instruction after a terminator, every branch target a label of the
// same function, and every merge instruction directly ahead of the
// terminator of its kind with labels of the function as its merge
// and continue targets. It returns the function count.
func checkFunctions(t *testing.T, d *decoded) int {
	t.Helper()
	funcs := 0
	for i := 0; i < len(d.insts); i++ {
		if d.insts[i].opcode != opFunction {
			continue
		}
		funcs++
		i++
		for d.insts[i].opcode == opFunctionParameter {
			i++
		}
		labels := map[uint32]bool{}
		for j := i; d.insts[j].opcode != opFunctionEnd; j++ {
			if d.insts[j].opcode == opLabel {
				labels[d.insts[j].operands[0]] = true
			}
		}
		first := true
		inBlock := false
		var merge *inst // the merge instruction the next instruction must terminate
		for ; d.insts[i].opcode != opFunctionEnd; i++ {
			in := d.insts[i]
			if merge != nil {
				ok := false
				switch merge.opcode {
				case opSelectionMerge:
					ok = in.opcode == opBranchConditional || in.opcode == opSwitch
				case opLoopMerge:
					ok = in.opcode == opBranch || in.opcode == opBranchConditional
				}
				if !ok {
					t.Fatalf("merge instruction %d followed by opcode %d, not its terminator", merge.opcode, in.opcode)
				}
				merge = nil
			}
			switch {
			case in.opcode == opLabel:
				if inBlock {
					t.Fatalf("label %d inside an unterminated block", in.operands[0])
				}
				inBlock = true
				if first {
					first = false
					for d.insts[i+1].opcode == opVariable {
						i++
					}
				}
			case !inBlock:
				t.Fatalf("instruction %d outside a block", in.opcode)
			case in.opcode == opVariable:
				t.Fatalf("variable %d outside the entry block's head", in.operands[1])
			case in.opcode == opSelectionMerge || in.opcode == opLoopMerge:
				m := in
				merge = &m
				n := 1
				if in.opcode == opLoopMerge {
					n = 2
					if in.operands[0] == in.operands[1] {
						t.Fatalf("loop merge %d is its own continue target", in.operands[0])
					}
				}
				for _, l := range in.operands[:n] {
					if !labels[l] {
						t.Fatalf("merge target %d, not a label of the function", l)
					}
				}
			case terminator(in.opcode):
				inBlock = false
				var targets []uint32
				switch in.opcode {
				case opBranch:
					targets = in.operands[:1]
				case opBranchConditional:
					targets = in.operands[1:3]
				case opSwitch:
					targets = switchTargets(t, d, in)
				}
				for _, l := range targets {
					if !labels[l] {
						t.Fatalf("branch to %d, not a label of the function", l)
					}
				}
			}
		}
		if inBlock {
			t.Fatal("function ends inside a block")
		}
	}
	return funcs
}

// checkLayout holds that no Function or Private pointer targets a
// type carrying or containing a layout decoration.
func checkLayout(t *testing.T, d *decoded) {
	t.Helper()
	var laidOut func(id uint32) bool
	laidOut = func(id uint32) bool {
		if _, ok := d.strides[id]; ok {
			return true
		}
		if _, ok := d.offsets[id]; ok {
			return true
		}
		ty := d.types[id]
		switch ty.opcode {
		case opTypeArray, opTypeRuntimeArray:
			return laidOut(ty.operands[1])
		case opTypeStruct:
			for _, m := range ty.operands[1:] {
				if laidOut(m) {
					return true
				}
			}
		}
		return false
	}
	for id, ty := range d.types {
		if ty.opcode == opTypePointer && (ty.operands[1] == storageFunction || ty.operands[1] == storagePrivate) && laidOut(ty.operands[2]) {
			t.Errorf("pointer %d of storage class %d to a laid-out type", id, ty.operands[1])
		}
	}
}

// checkModule runs every structural check on one emitted entry.
func checkModule(t *testing.T, k EntryPoint) *decoded {
	t.Helper()
	d := decode(t, k.Code)
	for _, c := range []uint32{capShader, capInt64, capStorageBuffer8BitAccess, capVariablePointersStorageBuffer, capVulkanMemoryModel, capVulkanMemoryModelDeviceScope} {
		if !d.caps[c] {
			t.Errorf("capability %d missing", c)
		}
	}
	if len(d.exts) != 0 {
		t.Errorf("extensions %v, want none at SPIR-V 1.5", d.exts)
	}
	var model *inst
	for i := range d.insts {
		if d.insts[i].opcode == opMemoryModel {
			model = &d.insts[i]
		}
	}
	if model == nil || model.operands[0] != addressingLogical || model.operands[1] != memoryVulkan {
		t.Errorf("memory model %v", model)
	}
	if d.entry == nil || d.entry.operands[0] != executionModelGLCompute || literal(d.entry.operands[2:]) != "main" {
		t.Fatalf("entry point %v", d.entry)
	}
	// From SPIR-V 1.4 the interface is every module-scope variable
	// the entry references, which here is every one declared.
	iface := map[uint32]bool{}
	for _, v := range d.entry.operands[2+len(words("main")):] {
		if _, ok := d.globals[v]; !ok {
			t.Errorf("interface lists %d, not a module-scope variable", v)
		}
		iface[v] = true
	}
	for v, storage := range d.globals {
		if !iface[v] {
			t.Errorf("variable %d of storage class %d missing from the interface", v, storage)
		}
	}
	if d.local != [3]uint32{uint32(k.Workgroup[0]), uint32(k.Workgroup[1]), 1} {
		t.Errorf("local size %v, workgroup %v", d.local, k.Workgroup)
	}
	want := len(k.Buffers)
	if k.ParamsBinding >= 0 {
		want++
	}
	seen := map[uint32]bool{}
	for v, b := range d.bindings {
		if d.sets[v] != 0 {
			t.Errorf("variable %d in set %d", v, d.sets[v])
		}
		seen[b] = true
	}
	for b := 0; b < want; b++ {
		if !seen[uint32(b)] {
			t.Errorf("no variable bound at %d", b)
		}
	}
	if len(seen) != want {
		t.Errorf("%d bindings, want %d", len(seen), want)
	}
	checkOrder(t, d)
	checkFunctions(t, d)
	checkLayout(t, d)
	checkPrivacy(t, d)
	return d
}

// checkPrivacy holds every load and store to the non-private form the
// Vulkan memory model orders between invocations exactly when the
// pointer is a storage buffer pointer: the operand on every storage
// buffer access, and on no access of another storage class.
func checkPrivacy(t *testing.T, d *decoded) {
	t.Helper()
	for _, in := range d.insts {
		var ptr uint32
		var operands []uint32
		switch in.opcode {
		case opLoad:
			ptr, operands = in.operands[2], in.operands[3:]
		case opStore:
			ptr, operands = in.operands[0], in.operands[2:]
		default:
			continue
		}
		pt := d.types[d.values[ptr]]
		if pt.opcode != opTypePointer {
			t.Fatalf("access through %d, whose type is not a pointer", ptr)
		}
		nonPrivate := len(operands) > 0 && operands[0]&memoryOperandNonPrivatePointer != 0
		if storage := pt.operands[1]; nonPrivate != (storage == storageStorageBuffer) {
			t.Errorf("access through pointer %d of storage class %d: non-private %v", ptr, storage, nonPrivate)
		}
	}
}

// pushBlock finds the push block's type and checks its member
// offsets: the PushWords words at 0 upward by four and the length
// array after them when the entry has one.
func pushBlock(t *testing.T, k EntryPoint, d *decoded) {
	t.Helper()
	var found bool
	for id, ty := range d.types {
		if ty.opcode != opTypePointer || ty.operands[1] != storagePushConstant || d.types[ty.operands[2]].opcode != opTypeStruct {
			continue
		}
		st := ty.operands[2]
		offs := d.offsets[st]
		n := PushWords
		if k.BufferLengths {
			n++
		}
		if len(offs) != n {
			t.Errorf("push block %d has %d offsets, want %d", id, len(offs), n)
		}
		for j, o := range offs {
			if o != uint32(4*j) {
				t.Errorf("push member %d at offset %d", j, o)
			}
		}
		if k.BufferLengths {
			arr := d.types[st].operands[PushWords+1]
			if d.strides[arr] != 4 {
				t.Errorf("length array stride %d", d.strides[arr])
			}
		}
		found = true
	}
	if !found {
		t.Error("no push block")
	}
}
