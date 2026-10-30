// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package spirv

import (
	"go/constant"
	"math"

	"github.com/user-none/erings/internal/hostgpu/compile"
)

// fn is one function under emission: its body words, the Function
// variables collected for the entry block, whether the current block
// has been terminated, the enclosing loops, and the ids of the
// locals: a variable per local and value parameter, the parameter's
// pointer for a reference parameter, and for a buffer parameter its
// pointer and, when variable-length, its hidden word count.
type fn struct {
	m    *module
	b    *builder
	f    *compile.Func
	fi   int // the function's index, for rowLocals
	decl fnDecl

	body       []uint32
	vars       []uint32
	terminated bool
	loops      []loop

	locals []uint32
	bufs   []uint32
	lens   []uint32
}

// loop is an enclosing loop's break and continue targets.
type loop struct {
	merge, cont uint32
}

// function emits one function: the declaration, the parameters, the
// entry block with every variable first, the value parameters copied
// into their variables, the body, and the terminator the last block
// needs when the body leaves it open.
func (m *module) function(fi int) {
	b := m.b
	f := &fn{m: m, b: b, f: &m.e.m.Funcs[fi], fi: fi, decl: m.fns[fi]}
	f.locals = make([]uint32, len(f.f.Locals))
	f.bufs = make([]uint32, len(f.f.Locals))
	f.lens = make([]uint32, len(f.f.Locals))

	var head []uint32
	op(&head, opFunction, f.decl.ret, f.decl.id, 0, f.decl.typ)
	pi := 0
	values := make([]uint32, f.f.NumParams) // value parameters, copied into their variables
	for p := 0; p < f.f.NumParams; p++ {
		l := f.f.Locals[p]
		id := b.id()
		op(&head, opFunctionParameter, f.decl.params[pi], id)
		pi++
		switch l.Kind {
		case compile.RefParam:
			f.locals[p] = id
		case compile.BufferParam:
			f.bufs[p] = id
			if l.Type.(compile.BufferType).Dynamic {
				f.lens[p] = b.id()
				op(&head, opFunctionParameter, f.decl.params[pi], f.lens[p])
				pi++
			}
		default:
			values[p] = id
		}
	}
	entry := b.id()
	op(&head, opLabel, entry)
	for p, v := range values {
		if v != 0 {
			f.store(f.varOf(p), v)
		}
	}
	f.stmts(f.f.Body.Stmts)
	if !f.terminated {
		if f.decl.ret == b.tVoid() {
			f.emit(opReturn)
		} else {
			f.emit(opUnreachable)
		}
	}
	b.funcs = append(b.funcs, head...)
	b.funcs = append(b.funcs, f.vars...)
	b.funcs = append(b.funcs, f.body...)
	op(&b.funcs, opFunctionEnd)
}

func (f *fn) emit(opcode uint32, operands ...uint32) {
	op(&f.body, opcode, operands...)
}

// result emits an instruction with a result of the type and returns
// its id.
func (f *fn) result(opcode uint32, typ uint32, operands ...uint32) uint32 {
	id := f.b.id()
	op(&f.body, opcode, append([]uint32{typ, id}, operands...)...)
	return id
}

// block starts a block with the label.
func (f *fn) block(label uint32) {
	f.emit(opLabel, label)
	f.terminated = false
}

// jump branches to the label unless the block is already terminated.
func (f *fn) jump(label uint32) {
	if !f.terminated {
		f.emit(opBranch, label)
		f.terminated = true
	}
}

// newVar declares a Function variable of the type in the entry block.
func (f *fn) newVar(t uint32) uint32 {
	id := f.b.id()
	op(&f.vars, opVariable, f.b.ptr(storageFunction, t), id, storageFunction)
	return id
}

// varOf is the variable of a local, declared on first use.
func (f *fn) varOf(i int) uint32 {
	if f.locals[i] == 0 {
		f.locals[i] = f.newVar(f.m.typeOf(f.f.Locals[i].Type))
	}
	return f.locals[i]
}

func (f *fn) load(t uint32, ptr uint32) uint32 { return f.result(opLoad, t, ptr) }
func (f *fn) store(ptr uint32, v uint32)       { f.emit(opStore, ptr, v) }

// bufferLoad and bufferStore access a storage buffer as non-private
// accesses, the ones the Vulkan memory model lets a barrier's
// availability and visibility operations order between invocations.
// A load or store without the operand is private to its invocation,
// the form every Function, Private, and push constant access takes.
func (f *fn) bufferLoad(t uint32, ptr uint32) uint32 {
	return f.result(opLoad, t, ptr, memoryOperandNonPrivatePointer)
}

func (f *fn) bufferStore(ptr uint32, v uint32) {
	f.emit(opStore, ptr, v, memoryOperandNonPrivatePointer)
}

// stmts emits a statement list. A statement after a terminator
// opens a fresh block, so no instruction follows a terminator.
func (f *fn) stmts(list []compile.Stmt) {
	for _, s := range list {
		if f.terminated {
			f.block(f.b.id())
		}
		f.stmt(s)
	}
}

func (f *fn) stmt(s compile.Stmt) {
	switch s := s.(type) {
	case *compile.Block:
		f.stmts(s.Stmts)
	case *compile.VarDecl:
		if ls, ok := s.Init.(*compile.LoadStruct); ok {
			// The row's address, for the reads in place.
			a := f.expr(ls.Addr)
			f.store(f.varOf(s.Local), f.loadStructAt(ls, a))
			f.store(f.m.prow(), a)
			return
		}
		v := f.expr(s.Init)
		f.store(f.varOf(s.Local), v)
	case *compile.Assign:
		if len(s.Targets) > 1 {
			c := s.Values[0].(*compile.Call)
			r := f.call(c)
			tt := c.T.(compile.Tuple)
			for i, t := range s.Targets {
				v := f.result(opCompositeExtract, f.m.typeOf(tt.Elems[i]), r, uint32(i))
				f.chainStore(t, v)
			}
			return
		}
		v := f.expr(s.Values[0])
		f.chainStore(s.Targets[0], v)
	case *compile.ExprStmt:
		f.expr(s.X)
	case *compile.If:
		f.ifStmt(s)
	case *compile.Switch:
		f.switchStmt(s)
	case *compile.For:
		f.forStmt(s)
	case *compile.Break:
		f.emit(opBranch, f.loops[len(f.loops)-1].merge)
		f.terminated = true
	case *compile.Continue:
		f.emit(opBranch, f.loops[len(f.loops)-1].cont)
		f.terminated = true
	case *compile.Return:
		switch len(s.Values) {
		case 0:
			f.emit(opReturn)
		case 1:
			f.emit(opReturnValue, f.expr(s.Values[0]))
		default:
			vals := make([]uint32, len(s.Values))
			for i, v := range s.Values {
				vals[i] = f.expr(v)
			}
			f.emit(opReturnValue, f.result(opCompositeConstruct, f.decl.ret, vals...))
		}
		f.terminated = true
	default:
		f.b.fail("unsupported statement %T", s)
	}
}

// ifStmt emits a selection construct: the condition, the then block,
// the else block when there is one, and the merge block. An else-if
// chain nests as a selection inside the else block.
func (f *fn) ifStmt(s *compile.If) {
	cond := f.expr(s.Cond)
	then, merge := f.b.id(), f.b.id()
	els := merge
	if s.Else != nil {
		els = f.b.id()
	}
	f.emit(opSelectionMerge, merge, 0)
	f.emit(opBranchConditional, cond, then, els)
	f.terminated = true
	f.block(then)
	f.stmts(s.Then.Stmts)
	f.jump(merge)
	if s.Else != nil {
		f.block(els)
		f.stmt(s.Else)
		f.jump(merge)
	}
	f.block(merge)
}

// switchStmt emits a switch construct: the selector, a bool tag as
// one or zero since the selector must be an integer, the case blocks
// in order each branching to the merge, and the default block or the
// merge as the default target.
func (f *fn) switchStmt(s *compile.Switch) {
	tag := s.Tag.Type().(compile.Scalar)
	sel := f.expr(s.Tag)
	wide := tag.Width() == 64
	if compile.Kind(tag) == compile.Bool {
		sel = f.result(opSelect, f.b.tU32(), sel, f.b.constU32(1), f.b.constU32(0))
	}
	merge := f.b.id()
	def := merge
	if s.Default != nil {
		def = f.b.id()
	}
	operands := []uint32{sel, def}
	labels := make([]uint32, len(s.Cases))
	for i, c := range s.Cases {
		labels[i] = f.b.id()
		for _, v := range c.Values {
			operands = append(operands, caseLiteral(v.(*compile.Const), wide)...)
			operands = append(operands, labels[i])
		}
	}
	f.emit(opSelectionMerge, merge, 0)
	f.emit(opSwitch, operands...)
	f.terminated = true
	for i, c := range s.Cases {
		f.block(labels[i])
		f.stmts(c.Body.Stmts)
		f.jump(merge)
	}
	if s.Default != nil {
		f.block(def)
		f.stmts(s.Default.Stmts)
		f.jump(merge)
	}
	f.block(merge)
}

// caseLiteral is a case value's literal words: one for a 32-bit
// selector, two for a 64-bit one, low word first, a bool as one or
// zero. A uint64 value is read as unsigned, as one past the int64
// range has no int64 value.
func caseLiteral(c *compile.Const, wide bool) []uint32 {
	if c.T == compile.Scalar(compile.Bool) {
		if constant.BoolVal(c.Value) {
			return []uint32{1}
		}
		return []uint32{0}
	}
	var n uint64
	if c.T == compile.Scalar(compile.U64) {
		n, _ = constant.Uint64Val(constant.ToInt(c.Value))
	} else {
		s, _ := constant.Int64Val(constant.ToInt(c.Value))
		n = uint64(s)
	}
	if wide {
		return []uint32{uint32(n), uint32(n >> 32)}
	}
	return []uint32{uint32(n)}
}

// forStmt emits a loop construct: the header with the merge and
// continue targets, the condition block branching to the body or the
// merge, the body, and the continue block running the post statement
// and branching back.
func (f *fn) forStmt(s *compile.For) {
	header, body, cont, merge := f.b.id(), f.b.id(), f.b.id(), f.b.id()
	f.jump(header)
	f.block(header)
	f.emit(opLoopMerge, merge, cont, 0)
	if s.Cond != nil {
		cond := f.b.id()
		f.jump(cond)
		f.block(cond)
		c := f.expr(s.Cond)
		f.emit(opBranchConditional, c, body, merge)
		f.terminated = true
	} else {
		f.jump(body)
	}
	f.block(body)
	f.loops = append(f.loops, loop{merge: merge, cont: cont})
	f.stmts(s.Body.Stmts)
	f.loops = f.loops[:len(f.loops)-1]
	f.jump(cont)
	f.block(cont)
	if s.Post != nil {
		f.stmt(s.Post)
	}
	f.jump(header)
	f.block(merge)
}

// address is a Function pointer to an assignable path: a local's
// variable or a reference parameter, a field or element through an
// access chain, and for a path into a value that has no storage, a
// temporary holding the value.
func (f *fn) address(x compile.Expr) uint32 {
	switch x := x.(type) {
	case *compile.LocalRef:
		return f.varOf(x.Index)
	case *compile.FieldRef:
		return f.result(opAccessChain, f.b.ptr(storageFunction, f.m.typeOf(x.T)), f.address(x.X), f.b.constU32(uint32(x.Field)))
	case *compile.IndexRef:
		return f.result(opAccessChain, f.b.ptr(storageFunction, f.m.typeOf(x.T)), f.address(x.X), f.expr(x.Index))
	}
	tmp := f.newVar(f.m.typeOf(x.Type()))
	f.store(tmp, f.expr(x))
	return tmp
}

// expr emits an expression and returns its value, of the register
// type of the node's type.
func (f *fn) expr(x compile.Expr) uint32 {
	switch x := x.(type) {
	case *compile.Const:
		return f.m.constant(x.T.(compile.Scalar), x.Value)
	case *compile.LocalRef:
		if f.f.Locals[x.Index].Kind == compile.BufferParam {
			return f.bufs[x.Index]
		}
		return f.load(f.m.typeOf(x.T), f.varOf(x.Index))
	case *compile.FieldRef, *compile.IndexRef:
		return f.read(x)
	case *compile.TableRef:
		t := f.m.typeOf(x.T)
		ptr := f.result(opAccessChain, f.b.ptr(storagePrivate, t), f.m.table(x.Table), f.expr(x.Index))
		return f.load(t, ptr)
	case *compile.Zero:
		return f.b.null(f.m.typeOf(x.T))
	case *compile.Composite:
		elems := make([]uint32, len(x.Elems))
		for i, el := range x.Elems {
			elems[i] = f.expr(el)
		}
		return f.result(opCompositeConstruct, f.m.typeOf(x.T), elems...)
	case *compile.Call:
		return f.call(x)
	case *compile.OpCall:
		return f.operation(x)
	case *compile.Unary:
		t := f.m.typeOf(x.T)
		v := f.expr(x.X)
		switch x.Op {
		case compile.Neg:
			if x.T == compile.Scalar(compile.F32) {
				return f.result(opFNegate, t, v)
			}
			// Integer negation is a subtraction from zero. The SPIR-V
			// specification defines OpISub's result as the low-order
			// bits of the exact result, so negating the minimum wraps
			// to the minimum; OpSNegate's description states no such
			// wrap.
			zero := []uint32{0}
			if x.T.(compile.Scalar).Width() > 32 {
				zero = append(zero, 0)
			}
			return f.result(opISub, t, f.b.constant(t, zero...), v)
		case compile.Not:
			return f.result(opLogicalNot, t, v)
		case compile.BitComplement:
			return f.result(opNot, t, v)
		}
	case *compile.Binary:
		return f.binary(x)
	case *compile.Convert:
		return f.convert(x.T.(compile.Scalar), x.X)
	case *compile.Mask:
		return f.mask(x.T.(compile.Scalar), f.expr(x.X), x.X.Type().(compile.Scalar))
	case *compile.LoadStruct:
		return f.loadStruct(x)
	}
	f.b.fail("unsupported expression %T", x)
	return f.b.constU32(0)
}

// call emits a function call: value arguments by value, buffers
// with their hidden word counts, and reference arguments as the
// local's own variable or parameter when the argument is a bare
// local, else as a temporary copied in before the call and copied
// back after it when the callee writes that parameter.
func (f *fn) call(c *compile.Call) uint32 {
	callee := &f.m.e.m.Funcs[c.Func]
	d := f.m.fns[c.Func]
	written := f.m.e.writes(c.Func)
	var args []uint32
	var backs []func()
	for j, a := range c.Args {
		switch callee.Locals[j].Kind {
		case compile.RefParam:
			p, back := f.refArg(a, written[j])
			args = append(args, p)
			if back != nil {
				backs = append(backs, back)
			}
		case compile.BufferParam:
			lr := a.(*compile.LocalRef)
			args = append(args, f.bufs[lr.Index])
			if callee.Locals[j].Type.(compile.BufferType).Dynamic {
				args = append(args, f.lens[lr.Index])
			}
		default:
			args = append(args, f.expr(a))
		}
	}
	r := f.result(opFunctionCall, d.ret, append([]uint32{d.id}, args...)...)
	for _, back := range backs {
		back()
	}
	return r
}

// refArg is a reference argument's pointer and, for a path copied
// into a temporary that the callee writes, the copy back. A path the
// callee only reads is copied in through read, so it takes the chain
// form or the read in place; a path it writes keeps the indexed form,
// its pointer taken once for the copy in and the copy back.
func (f *fn) refArg(a compile.Expr, written bool) (uint32, func()) {
	if l, ok := a.(*compile.LocalRef); ok {
		return f.varOf(l.Index), nil
	}
	t := f.m.typeOf(a.Type())
	tmp := f.newVar(t)
	if !written {
		f.store(tmp, f.read(a))
		return tmp, nil
	}
	chain := f.address(a)
	f.store(tmp, f.load(t, chain))
	return tmp, func() { f.store(chain, f.load(t, tmp)) }
}

// The chain form of a run-time index. A driver compiler keeps an
// aggregate in registers only when every index into it is a constant,
// and an access at an index known only at run time places the whole
// aggregate in per-thread local memory. An access path rooted in a
// local, through arrays of at most selectMax elements at side-effect-
// free indexes, is emitted over the constant indexes instead, so no
// index into the aggregate is left at run time: a scalar read as a
// select chain, an aggregate read and a write as a switch over the
// index. An index out of the array's range takes the last element,
// where the indexed form's result was undefined. A reference argument
// the callee writes through keeps the indexed form (refArg). A read in
// an entry's params row is read in place instead (paramsInPlace).
const (
	selectMax    = 8  // the longest array a run-time index into is chained
	selectCombos = 64 // the most element paths one access expands to
)

// read is the value of an access path: read in place from the params
// buffer, in the chain form, or loaded through the indexed form.
func (f *fn) read(x compile.Expr) uint32 {
	if f.paramsInPlace(x) {
		return f.loadAt(f.m.params, x.Type(), f.paramsAddr(x))
	}
	if at := f.chainIndex(x); at != nil {
		return f.chainRead(x, at)
	}
	return f.load(f.m.typeOf(x.Type()), f.address(x))
}

// chainIndex is the run-time index of an access path that takes the
// chain form, the one nearest the path's root, or nil when the path
// keeps the indexed form: its root is not a local, an index has side
// effects, an array is longer than selectMax, or the run-time indexes
// expand to more than selectCombos element paths.
func (f *fn) chainIndex(x compile.Expr) *compile.IndexRef {
	var at *compile.IndexRef
	combos := 1
	for {
		switch n := x.(type) {
		case *compile.LocalRef:
			return at
		case *compile.FieldRef:
			x = n.X
		case *compile.IndexRef:
			if _, ok := n.Index.(*compile.Const); !ok {
				a, ok := n.X.Type().(compile.ArrayType)
				if !ok || a.Len == 0 || a.Len > selectMax || !pure(n.Index) {
					return nil
				}
				combos *= a.Len
				if combos > selectCombos {
					return nil
				}
				at = n
			}
			x = n.X
		default:
			return nil
		}
	}
}

// pure reports an expression without side effects, which a chain may
// evaluate once per element.
func pure(x compile.Expr) bool {
	switch x := x.(type) {
	case *compile.Const, *compile.LocalRef:
		return true
	case *compile.FieldRef:
		return pure(x.X)
	case *compile.IndexRef:
		return pure(x.X) && pure(x.Index)
	case *compile.TableRef:
		return pure(x.Index)
	case *compile.Unary:
		return pure(x.X)
	case *compile.Binary:
		return pure(x.X) && pure(x.Y)
	case *compile.Convert:
		return pure(x.X)
	case *compile.Mask:
		return pure(x.X)
	case *compile.OpCall:
		if !x.Op.Pure() {
			return false
		}
		for _, a := range x.Args {
			if !pure(a) {
				return false
			}
		}
		return true
	}
	return false
}

// atIndex is the access path x with the index at replaced by the
// constant k, the nodes below at shared with x.
func atIndex(x compile.Expr, at *compile.IndexRef, k int) compile.Expr {
	switch n := x.(type) {
	case *compile.IndexRef:
		if n == at {
			return &compile.IndexRef{T: n.T, X: n.X, Index: &compile.Const{T: n.Index.Type(), Value: constant.MakeInt64(int64(k))}}
		}
		return &compile.IndexRef{T: n.T, X: atIndex(n.X, at, k), Index: n.Index}
	case *compile.FieldRef:
		return &compile.FieldRef{T: n.T, X: atIndex(n.X, at, k), Field: n.Field}
	}
	return x
}

// chainRead reads the access path x over the constant indexes of its
// run-time index at, the index evaluated once: a scalar as a select
// chain with the last element the fallback, an aggregate through a
// switch into a temporary.
func (f *fn) chainRead(x compile.Expr, at *compile.IndexRef) uint32 {
	n := at.X.Type().(compile.ArrayType).Len
	t := f.m.typeOf(x.Type())
	idx := f.expr(at.Index)
	if _, ok := x.Type().(compile.Scalar); ok {
		vals := make([]uint32, n)
		for k := range vals {
			vals[k] = f.read(atIndex(x, at, k))
		}
		r := vals[n-1]
		for k := n - 2; k >= 0; k-- {
			cond := f.result(opIEqual, f.b.tBool(), idx, f.m.constant(at.Index.Type().(compile.Scalar), constant.MakeInt64(int64(k))))
			r = f.result(opSelect, t, cond, vals[k], r)
		}
		return r
	}
	tmp := f.newVar(t)
	f.chain(at, idx, func(k int) { f.store(tmp, f.read(atIndex(x, at, k))) })
	return f.load(t, tmp)
}

// chainStore stores v at the access path target, through a switch over
// each run-time index the path holds in the chain form.
func (f *fn) chainStore(target compile.Expr, v uint32) {
	at := f.chainIndex(target)
	if at == nil {
		f.store(f.address(target), v)
		return
	}
	idx := f.expr(at.Index)
	f.chain(at, idx, func(k int) { f.chainStore(atIndex(target, at, k), v) })
}

// chain emits a switch construct over the value idx of the index at:
// a case per element but the last, which is the default, each case's
// block holding leaf's instructions for its element.
func (f *fn) chain(at *compile.IndexRef, idx uint32, leaf func(k int)) {
	n := at.X.Type().(compile.ArrayType).Len
	it := at.Index.Type().(compile.Scalar)
	merge := f.b.id()
	labels := make([]uint32, n)
	for k := range labels {
		labels[k] = f.b.id()
	}
	operands := []uint32{idx, labels[n-1]}
	for k := 0; k < n-1; k++ {
		operands = append(operands, caseLiteral(&compile.Const{T: it, Value: constant.MakeInt64(int64(k))}, it.Width() == 64)...)
		operands = append(operands, labels[k])
	}
	f.emit(opSelectionMerge, merge, 0)
	f.emit(opSwitch, operands...)
	f.terminated = true
	for k := range labels {
		f.block(labels[k])
		leaf(k)
		f.jump(merge)
	}
	f.block(merge)
}

// The params row read in place. A read from an entry's params row at a
// run-time index is a load from the params buffer's variable at the
// row's byte address, held in a Private variable the wrapper sets when
// it loads the row. The wrapper's copy of the row holds every element
// of an array read this way as a live value per thread, which the
// chain form would read in full; the load reads the one element. A
// read at a constant path keeps the copy.

// paramsInPlace reports an access path rooted in a reference to an
// entry's params row that holds a run-time index.
func (f *fn) paramsInPlace(x compile.Expr) bool {
	r, ok := root(x)
	// The entry shell's fn has no IR function.
	if !ok || f.f == nil || f.m.params == 0 || f.f.Locals[r].Kind != compile.RefParam || !f.m.e.rowLocals()[[2]int{f.fi, r}] {
		return false
	}
	for {
		switch n := x.(type) {
		case *compile.FieldRef:
			x = n.X
		case *compile.IndexRef:
			if _, ok := n.Index.(*compile.Const); !ok {
				return true
			}
			x = n.X
		default:
			return false
		}
	}
}

// paramsAddr is the byte address in the params buffer of an access
// path rooted in the params row: the row's address, the constant
// offsets, and each run-time index by its element size.
func (f *fn) paramsAddr(x compile.Expr) uint32 {
	b := f.b
	off := 0
	var parts []uint32
	for done := false; !done; {
		switch n := x.(type) {
		case *compile.FieldRef:
			off += f.m.e.m.Structs[n.X.Type().(compile.StructRef).Index].Fields[n.Field].Offset
			x = n.X
		case *compile.IndexRef:
			stride := size(f.m.e.m, n.T)
			if c, ok := n.Index.(*compile.Const); ok {
				k, _ := constant.Int64Val(constant.ToInt(c.Value))
				off += int(k) * stride
			} else {
				i := f.convert(compile.Scalar(compile.U32), n.Index)
				parts = append(parts, f.result(opIMul, b.tU32(), i, b.constU32(uint32(stride))))
			}
			x = n.X
		default:
			done = true
		}
	}
	a := f.addU32(f.load(b.tU32(), f.m.prow()), uint32(off))
	for _, p := range parts {
		a = f.result(opIAdd, b.tU32(), a, p)
	}
	return a
}

// loadAt loads a value of type t at the byte address a of a word
// buffer: a word per 32-bit scalar reinterpreted by kind, two per
// 64-bit scalar, the byte of a bool out of the word holding it tested
// against zero, elements and fields at their laid-out offsets. It
// gathers an entry's params row from the params buffer and reads a
// field of the row in place.
func (f *fn) loadAt(buf uint32, t compile.Type, a uint32) uint32 {
	b := f.b
	var base uint32 // the word index, formed at the first word
	word := func(k uint32) uint32 {
		if base == 0 {
			base = f.result(opShiftRightLogical, b.tU32(), a, b.constU32(2))
		}
		i := base
		if k > 0 {
			i = f.addU32(i, k)
		}
		return f.bufferLoad(b.tU32(), f.wordPtr(buf, i))
	}
	switch u := t.(type) {
	case compile.Scalar:
		switch compile.Kind(u) {
		case compile.Bool:
			shift := f.result(opShiftLeftLogical, b.tU32(), f.result(opBitwiseAnd, b.tU32(), a, b.constU32(3)), b.constU32(3))
			v := f.result(opShiftRightLogical, b.tU32(), word(0), shift)
			v = f.result(opBitwiseAnd, b.tU32(), v, b.constU32(0xff))
			return f.result(opINotEqual, b.tBool(), v, b.constU32(0))
		case compile.U32:
			return word(0)
		case compile.I32, compile.F32:
			return f.result(opBitcast, b.scalar(u), word(0))
		case compile.I64, compile.U64:
			lo := f.result(opUConvert, b.tU64(), word(0))
			hi := f.result(opUConvert, b.tU64(), word(1))
			v := f.result(opBitwiseOr, b.tU64(), lo, f.result(opShiftLeftLogical, b.tU64(), hi, b.constU32(32)))
			return f.bitcast(v, b.tU64(), b.scalar(u))
		}
	case compile.ArrayType:
		stride := size(f.m.e.m, u.Elem)
		elems := make([]uint32, u.Len)
		for i := range elems {
			elems[i] = f.loadAt(buf, u.Elem, f.addU32(a, uint32(i*stride)))
		}
		return f.result(opCompositeConstruct, f.m.typeOf(t), elems...)
	case compile.StructRef:
		st := f.m.e.m.Structs[u.Index]
		elems := make([]uint32, len(st.Fields))
		for i, fl := range st.Fields {
			elems[i] = f.loadAt(buf, fl.Type, f.addU32(a, uint32(fl.Offset)))
		}
		return f.result(opCompositeConstruct, f.m.typeOf(t), elems...)
	}
	b.fail("params field of type %s has no buffer form", t)
	return b.constU32(0)
}

// binary emits an operator in the form the operand type selects:
// signed, unsigned, or float arithmetic and comparison, logical
// operators on bools. && and || are a selection (shortCircuit).
func (f *fn) binary(x *compile.Binary) uint32 {
	if x.Op == compile.LogicalAnd || x.Op == compile.LogicalOr {
		return f.shortCircuit(x)
	}
	t := f.m.typeOf(x.T)
	xs := x.X.Type().(compile.Scalar)
	a, b := f.expr(x.X), f.expr(x.Y)
	k := compile.Kind(xs)
	float := k == compile.F32
	signed := k == compile.I8 || k == compile.I16 || k == compile.I32 || k == compile.I64
	pick := func(s, u, fl uint32) uint32 {
		switch {
		case float:
			return fl
		case signed:
			return s
		}
		return u
	}
	switch x.Op {
	case compile.Add:
		return f.result(pick(opIAdd, opIAdd, opFAdd), t, a, b)
	case compile.Sub:
		return f.result(pick(opISub, opISub, opFSub), t, a, b)
	case compile.Mul:
		return f.result(pick(opIMul, opIMul, opFMul), t, a, b)
	case compile.Div:
		return f.result(pick(opSDiv, opUDiv, opFDiv), t, a, b)
	case compile.Rem:
		// Go has no % on floats, so the integer forms are the only ones.
		op := uint32(opUMod)
		if signed {
			op = opSRem
		}
		return f.result(op, t, a, b)
	case compile.BitAnd:
		return f.result(opBitwiseAnd, t, a, b)
	case compile.BitOr:
		return f.result(opBitwiseOr, t, a, b)
	case compile.BitXor:
		return f.result(opBitwiseXor, t, a, b)
	case compile.AndNot:
		return f.result(opBitwiseAnd, t, a, f.result(opNot, t, b))
	case compile.Shl:
		return f.result(opShiftLeftLogical, t, a, b)
	case compile.Shr:
		// Go has no >> on floats either.
		op := uint32(opShiftRightLogical)
		if signed {
			op = opShiftRightArith
		}
		return f.result(op, t, a, b)
	case compile.Eq:
		if k == compile.Bool {
			return f.result(opLogicalEqual, t, a, b)
		}
		return f.result(pick(opIEqual, opIEqual, opFOrdEqual), t, a, b)
	case compile.Ne:
		if k == compile.Bool {
			return f.result(opLogicalNotEqual, t, a, b)
		}
		return f.result(pick(opINotEqual, opINotEqual, opFUnordNotEqual), t, a, b)
	case compile.Lt:
		return f.result(pick(opSLessThan, opULessThan, opFOrdLessThan), t, a, b)
	case compile.Le:
		return f.result(pick(opSLessThanEqual, opULessThanEqual, opFOrdLessThanEqual), t, a, b)
	case compile.Gt:
		return f.result(pick(opSGreaterThan, opUGreaterThan, opFOrdGreaterThan), t, a, b)
	case compile.Ge:
		return f.result(pick(opSGreaterThanEqual, opUGreaterThanEqual, opFOrdGreaterThanEqua), t, a, b)
	}
	f.b.fail("unsupported operator %s", x.Op)
	return a
}

// shortCircuit emits && or || as a selection construct over the left
// operand, the right operand evaluated only in the branch that needs
// it: OpLogicalAnd and OpLogicalOr take both operands evaluated, and a
// right operand the left one guards may divide by zero or index out of
// range. The result is held in a Function variable, so the right
// operand may emit blocks of its own.
func (f *fn) shortCircuit(x *compile.Binary) uint32 {
	t := f.m.typeOf(x.T)
	tmp := f.newVar(t)
	l := f.expr(x.X)
	f.store(tmp, l)
	right, merge := f.b.id(), f.b.id()
	f.emit(opSelectionMerge, merge, 0)
	if x.Op == compile.LogicalAnd {
		f.emit(opBranchConditional, l, right, merge)
	} else {
		f.emit(opBranchConditional, l, merge, right)
	}
	f.terminated = true
	f.block(right)
	f.store(tmp, f.expr(x.Y))
	f.jump(merge)
	f.block(merge)
	return f.load(t, tmp)
}

// bitcast reinterprets a value between two register types of one
// width, a no-op when they are the same.
func (f *fn) bitcast(v uint32, from, to uint32) uint32 {
	if from == to {
		return v
	}
	return f.result(opBitcast, to, v)
}

// mask re-canonicalizes a narrow value held in a 32-bit register:
// shifts up and back down arithmetically for a signed type, an AND
// for an unsigned one. The value is first brought to the register
// type of the target's signedness.
func (f *fn) mask(t compile.Scalar, v uint32, from compile.Scalar) uint32 {
	b := f.b
	rt := b.scalar(t)
	v = f.bitcast(v, b.scalar(from), rt)
	switch compile.Kind(t) {
	case compile.I8, compile.I16:
		n := b.constU32(uint32(32 - t.Width()))
		return f.result(opShiftRightArith, rt, f.result(opShiftLeftLogical, rt, v, n), n)
	case compile.U8, compile.U16:
		return f.result(opBitwiseAnd, rt, v, b.constU32(uint32(1<<t.Width()-1)))
	}
	return v
}

// convert emits a scalar conversion with Go's semantics: integers
// widen by the source's signedness and truncate, floats convert by
// the integer side's signedness, and a narrow target canonicalizes.
func (f *fn) convert(t compile.Scalar, x compile.Expr) uint32 {
	b := f.b
	s := x.Type().(compile.Scalar)
	v := f.expr(x)
	sk, tk := compile.Kind(s), compile.Kind(t)
	signed := func(k compile.Kind) bool {
		return k == compile.I8 || k == compile.I16 || k == compile.I32 || k == compile.I64
	}
	if tk == compile.F32 {
		if sk == compile.F32 {
			return v
		}
		if signed(sk) {
			return f.result(opConvertSToF, b.tF32(), v)
		}
		return f.result(opConvertUToF, b.tF32(), v)
	}
	var r uint32
	switch {
	case sk == compile.F32:
		if signed(tk) {
			r = f.result(opConvertFToS, b.scalar(t), v)
		} else {
			r = f.result(opConvertFToU, b.scalar(t), v)
		}
	case s.Width() == 64 && t.Width() == 64, s.Width() < 64 && t.Width() < 64:
		r = f.bitcast(v, b.scalar(s), b.scalar(t))
	case t.Width() == 64:
		if signed(sk) {
			r = f.result(opSConvert, b.tI64(), f.bitcast(v, b.scalar(s), b.tI32()))
			r = f.bitcast(r, b.tI64(), b.scalar(t))
		} else {
			r = f.result(opUConvert, b.tU64(), f.bitcast(v, b.scalar(s), b.tU32()))
			r = f.bitcast(r, b.tU64(), b.scalar(t))
		}
	default:
		r = f.result(opUConvert, b.tU32(), f.bitcast(v, b.scalar(s), b.tU64()))
		r = f.bitcast(r, b.tU32(), b.scalar(t))
	}
	if t.Narrow() {
		return f.mask(t, r, t)
	}
	return r
}

// constant is the id of a scalar constant.
func (m *module) constant(t compile.Scalar, v constant.Value) uint32 {
	b := m.b
	switch compile.Kind(t) {
	case compile.Bool:
		return b.constBool(constant.BoolVal(v))
	case compile.F32:
		fv, _ := constant.Float64Val(v)
		return b.constant(b.tF32(), math.Float32bits(float32(fv)))
	case compile.I64:
		n, _ := constant.Int64Val(constant.ToInt(v))
		return b.constI64(n)
	case compile.U64:
		n, _ := constant.Uint64Val(constant.ToInt(v))
		return b.constU64(n)
	case compile.U8, compile.U16, compile.U32:
		n, _ := constant.Uint64Val(constant.ToInt(v))
		return b.constU32(uint32(n))
	}
	n, _ := constant.Int64Val(constant.ToInt(v))
	return b.constI32(int32(n))
}
