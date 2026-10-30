// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package conformance

import "github.com/user-none/erings/core/compute"

// Probe is the struct layout probe's params: mixed widths, an array,
// a nested struct, and bools between words, laid out by the compile
// stage's rule and by the Go compiler, which must agree.
type Probe struct {
	A uint32
	B int64
	C [3]uint32
	D Inner
	G uint32
	H bool
	K bool
	L uint32
}

// Inner is the probe's nested struct.
type Inner struct {
	E int32
	F uint64
}

// VariableWords checks two independent variable-length bindings: the
// output carries both lengths, so a backend that binds one buffer's
// length for another cannot match Go execution. The output is twice
// the input's length, so the threads past the input's end read it at
// the index masked with its own length, which a backend supplying
// the wrong length or a fixed size fails. The last statement reads
// and writes back the thread's own word.
func VariableWords(in, out compute.LineRecords, p *Probe, i uint32) {
	n := uint32(len(in))
	v := in[i&(n-1)] ^ n ^ uint32(len(out)) ^ p.A
	out[i] = v
	out[i] += n
}

// Views is a buffer group of two variable-length word buffers, the
// array VariableArray reads by a variable index.
type Views struct {
	In [2]compute.LineRecords
}

// VariableArray checks the variable-index read from an array of
// variable-length buffers, the form the composite's layer read takes:
// even threads read the first buffer and odd threads the second, each
// at its half index masked with its own length. The two buffers hold
// different data and lengths, and the value carries both lengths, so
// a backend that reads the wrong buffer for an index or binds one
// buffer's length for the other cannot match the Go execution.
func VariableArray(v *Views, out compute.LineRecords, p *Probe, i uint32) {
	n0 := uint32(len(v.In[0]))
	n1 := uint32(len(v.In[1]))
	k := i & 1
	m := n0 - 1
	if k == 1 {
		m = n1 - 1
	}
	w := v.In[k][(i>>1)&m]
	out[i] = w ^ n0 ^ n1<<1 ^ p.A
}

// VariableBytes checks the byte accessors on two independent
// variable-length byte bindings, one thread per output byte: the
// output is twice the input's length, so the threads past the input's
// end read it at the address masked with its own length, and every
// thread writes its byte at its address plus the output's length,
// which lands on its own byte only through a mask of the output's
// length. The value carries both lengths, so a backend that binds one
// buffer's length for another, a fixed size, or a word count where
// the bytes belong cannot match the Go execution. Neighboring threads
// write neighboring bytes of one word, which holds a backend whose
// byte store is a word update to storing its lane alone.
func VariableBytes(in, out compute.LineColor, p *Probe, i uint32) {
	n := uint32(len(in))
	m := uint32(len(out))
	v := in.U8(i) ^ uint8(n^n>>8^n>>16) ^ uint8(m>>1^m>>9^m>>17) ^ uint8(p.A)
	out.PutU8(i+m, v)
}

// VariableHalfwords checks the 16-bit accessors on two independent
// variable-length byte bindings, one thread per output halfword: the
// output is twice the input's length, every access is at an odd
// address, so its two bytes straddle a halfword and every other one a
// word, and thread i reads the input at 2i+1, past its end for the
// upper half of the threads, and writes the output's bytes 2i+1 and
// 2i+2 through the address plus the output's length, the last thread
// straddling the output's end onto byte zero. No two threads write
// one byte. The value carries both lengths, so a backend that binds
// one buffer's length for another, a fixed size, or a word count
// where the bytes belong cannot match the Go execution.
func VariableHalfwords(in, out compute.VDP1FB, p *Probe, i uint32) {
	n := uint32(len(in))
	m := uint32(len(out))
	a := 2*i + 1
	v := in.U16BE(a) ^ uint16(n^n>>16) ^ uint16(m>>1^m>>17) ^ uint16(p.A)
	out.PutU16BE(a+m, v)
}

// The shared-word rules take the round state, a buffer group of
// three variable-length buffers, one per shared-word operation, so
// each rule also holds the group's binding order and the
// variable-length lowering of its operation.

// RaiseWords checks Raise on the order stamps, a group entry: the
// group's threads raise the group's eight words by stride with
// values that peak in the middle of the range, so no word's greatest
// value is its last offer, and each word must end at the greatest
// value any thread offered it, which is the Go execution's result as
// one thread. A device applying a lesser value after a greater one,
// or losing a raise under contention, leaves a word short.
func RaiseWords(rs *compute.RoundState, p *Probe, t, n, g uint32) {
	for i := t; i < 4096; i += n {
		v := i
		if v > 2048 {
			v = 4096 - v
		}
		rs.Stamps.Raise(g*8+(i&7), p.A+v)
	}
}

// AddWords checks Add on the fragment pool, a group entry: every
// thread reserves a range of the group's stream, four words from the
// group's counter, and marks each word of its range, so a device
// returning a prior twice or losing an add leaves a word unmarked or
// the counter short. The ranges land in any order, marked with the
// same value, so the Go execution as one thread is the reference for
// a device group of many. The prior is used for addressing only.
// Thread 0 records the group's size n beside the counter, so a test
// holds the counter and the ranges to the threads the device ran.
func AddWords(rs *compute.RoundState, p *Probe, t, n, g uint32) {
	if t == 0 {
		rs.Pool[g*8+1] = n
	}
	base := rs.Pool.Add(g*8, 4)
	for i := range uint32(4) {
		rs.Pool[4096+g*65536+base+i] = p.A
	}
}

// SwapChain checks Swap on the fragment list heads, a group entry:
// every thread swaps its own number into the group's head word and
// stores the prior in its slot, so the head and the slots chain
// every thread of the group exactly once, in the order the device
// ran the swaps. A test walks the chain; a device swapping
// non-atomically loses threads from it. Thread 0 records the group's
// size n beside the head, so a test walks the chain against the
// threads the device ran.
func SwapChain(rs *compute.RoundState, p *Probe, t, n, g uint32) {
	if t == 0 {
		rs.Heads[g*8+1] = n
	}
	prev := rs.Heads.Swap(g*8, t+1)
	rs.Heads[4096+g*65536+t] = prev
}

// table is the lookup table rule's data.
var table = [8]uint32{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}

// Wrap8 checks 8-bit wrap on add, subtract, and multiply, signed and
// unsigned, at stores, comparisons, division, remainder, shifts, and
// conversions of unmasked intermediates.
func Wrap8(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	a := int8(w0)
	b := int8(w1)
	ua := uint8(w0)
	ub := uint8(w1)
	s := a + b
	d := a - b
	m := a * b
	us := ua + ub
	ud := ua - ub
	um := ua * ub
	out[i*8+0] = uint32(int32(s))
	out[i*8+1] = uint32(int32(d))
	out[i*8+2] = uint32(int32(m))
	out[i*8+3] = uint32(us)
	out[i*8+4] = uint32(ud)
	out[i*8+5] = uint32(um)
	var flag uint32
	if a+b == s {
		flag |= 1
	}
	if (a*b)/3 == m/3 {
		flag |= 2
	}
	if (ua*ub)%7 == um%7 {
		flag |= 4
	}
	if (a+b)>>1 == s>>1 {
		flag |= 8
	}
	if (ua-ub)>>3 == ud>>3 {
		flag |= 16
	}
	if -a == -s+b {
		flag |= 32
	}
	if ^ua == ^us+ub {
		flag |= 64
	}
	out[i*8+6] = flag
	out[i*8+7] = uint32(int32(int8(a+b)))<<16 | uint32(uint8(ua*ub))
}

// Wrap16 is Wrap8 at 16 bits.
func Wrap16(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	a := int16(w0)
	b := int16(w1)
	ua := uint16(w0)
	ub := uint16(w1)
	s := a + b
	d := a - b
	m := a * b
	us := ua + ub
	ud := ua - ub
	um := ua * ub
	out[i*8+0] = uint32(int32(s))
	out[i*8+1] = uint32(int32(d))
	out[i*8+2] = uint32(int32(m))
	out[i*8+3] = uint32(us)
	out[i*8+4] = uint32(ud)
	out[i*8+5] = uint32(um)
	var flag uint32
	if a+b == s {
		flag |= 1
	}
	if (a*b)/3 == m/3 {
		flag |= 2
	}
	if (ua*ub)%7 == um%7 {
		flag |= 4
	}
	if (a+b)>>1 == s>>1 {
		flag |= 8
	}
	if (ua-ub)>>3 == ud>>3 {
		flag |= 16
	}
	if -a == -s+b {
		flag |= 32
	}
	if ^ua == ^us+ub {
		flag |= 64
	}
	out[i*8+6] = flag
	out[i*8+7] = uint32(uint16(int16(a+b)))<<16 | uint32(uint16(ua*ub))
}

// Wrap32 checks that signed arithmetic past the 32-bit range wraps as
// Go defines it: the sums, differences, products, negations, and
// left shifts of int32 extremes, a 64-bit product past the long
// range, a narrow product chain that leaves the register's range
// before it is masked, and the comparisons a compiler may fold when
// it assumes signed arithmetic never overflows.
func Wrap32(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	a := int32(w0)
	b := int32(w1)
	n := w1 & 31
	s := a + b
	d := a - b
	m := a * b
	l := int64(a) * int64(b) * int64(a)
	h := int16(w0)
	k := int16(w1)
	out[i*8+0] = uint32(s)
	out[i*8+1] = uint32(d)
	out[i*8+2] = uint32(m)
	out[i*8+3] = uint32(-a) ^ uint32(a<<n)
	out[i*8+4] = uint32(l) ^ uint32(l>>32)
	out[i*8+5] = uint32(int32(h*k*h)) ^ uint32(int32(h<<n))<<16
	var flag uint32
	if a+b == s {
		flag |= 1
	}
	if b > 0 && a+b > a {
		flag |= 2
	}
	if a*2/2 == a {
		flag |= 4
	}
	if a-1 < a {
		flag |= 8
	}
	if -a < 0 {
		flag |= 16
	}
	if a<<1 == a+a {
		flag |= 32
	}
	out[i*8+6] = flag
	out[i*8+7] = uint32(s*d) + uint32(m<<3)
}

// Shifts checks arithmetic and logical shifts at every width with
// counts below the width.
func Shifts(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	n := w1 & 31
	x := int32(w0)
	out[i*8+0] = uint32(x >> n)
	out[i*8+1] = uint32(x << n)
	out[i*8+2] = w0 >> n
	a := int8(w0)
	ua := uint8(w0)
	n8 := n & 7
	out[i*8+3] = uint32(int32(a>>n8))<<16 | uint32(int32(a<<n8))&0xFFFF
	out[i*8+4] = uint32(ua>>n8)<<16 | uint32(ua<<n8)
	h := int16(w0)
	uh := uint16(w0)
	n16 := n & 15
	out[i*8+5] = uint32(int32(h>>n16))<<16 | uint32(int32(h<<n16))&0xFFFF
	out[i*8+6] = uint32(uh>>n16)<<16 | uint32(uh<<n16)
	x64 := int64(x)<<32 | int64(uint32(w1))
	n64 := w1 & 63
	out[i*8+7] = uint32(x64>>n64) ^ uint32((x64>>n64)>>32) ^ uint32(uint64(x64)>>n64) ^ uint32(x64<<n64)
}

// DivRem checks truncating division and remainder with negative
// operands at 8, 16, 32, and 64 bits, signed and unsigned. The
// overflowing signed case (minimum divided by minus one) is excluded,
// as the subset requires of authors.
func DivRem(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	a := int32(w0)
	b := int32(w1)
	if b == 0 || b == -1 {
		b = 3
	}
	out[i*8+0] = uint32(a / b)
	out[i*8+1] = uint32(a % b)
	ub := w1 | 1
	out[i*8+2] = w0 / ub
	out[i*8+3] = w0 % ub
	a8 := int8(w0)
	b8 := int8(w1) | 1
	if b8 == -1 {
		b8 = 3
	}
	a16 := int16(w0)
	b16 := int16(w1) | 1
	if b16 == -1 {
		b16 = 3
	}
	out[i*8+4] = uint32(int32(a8/b8))<<16 | uint32(int32(a8%b8))&0xFFFF
	out[i*8+5] = uint32(int32(a16/b16))<<16 | uint32(int32(a16%b16))&0xFFFF
	x := int64(a)<<32 | int64(uint32(w1))
	y := int64(b)<<8 | 5
	out[i*8+6] = uint32(x/y) ^ uint32((x/y)>>32)
	out[i*8+7] = uint32(x%y) ^ uint32((x%y)>>32) ^ uint32(uint64(x)/uint64(y))
}

// Convert checks narrowing and widening conversions: truncation,
// sign extension, and zero extension between every width.
func Convert(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	x := int32(w0)
	out[i*8+0] = uint32(uint8(x)) | uint32(uint16(x))<<8
	out[i*8+1] = uint32(int32(int8(x)))
	out[i*8+2] = uint32(int32(int16(x)))
	out[i*8+3] = uint32(uint64(int64(x)) >> 32)
	out[i*8+4] = uint32(int64(int8(w1))>>32) ^ uint32(uint64(uint8(w1))>>32)<<1
	out[i*8+5] = uint32(uint16(int8(w1))) | uint32(int32(int8(uint16(w1))))<<16
	out[i*8+6] = uint32(uint64(uint32(x))>>32) | uint32(int64(uint32(w1))>>32)<<1 | uint32(uint32(int16(w0)))<<2
	out[i*8+7] = uint32(int8(w0)+int8(w1)) | uint32(uint8(w0)+uint8(w1))<<8 | uint32(int32(int16(uint8(w0))))<<16
}

// abs32 and abs64 are the absolute value, which Go has no integer
// builtin for.
func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Int64 checks 64-bit arithmetic and comparisons on values assembled
// from two words.
func Int64(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	x := int64(int32(in[i*8]))<<32 | int64(in[i*8+1])
	y := int64(int32(in[i*8+2]))<<32 | int64(in[i*8+3])
	s := x + y
	d := x - y
	m := x * y
	out[i*8+0] = uint32(s)
	out[i*8+1] = uint32(s >> 32)
	out[i*8+2] = uint32(d)
	out[i*8+3] = uint32(d >> 32)
	out[i*8+4] = uint32(m)
	out[i*8+5] = uint32(m >> 32)
	var flag uint32
	if x < y {
		flag |= 1
	}
	if x == y {
		flag |= 2
	}
	if uint64(x) < uint64(y) {
		flag |= 4
	}
	if x >= 0 {
		flag |= 8
	}
	if max(x, y) == x {
		flag |= 16
	}
	if min(max(x, -1000), 1000) == x {
		flag |= 32
	}
	if abs64(d) == d {
		flag |= 64
	}
	out[i*8+6] = flag
	out[i*8+7] = uint32(uint64(x)>>7) ^ uint32(x&y) ^ uint32(x|y) ^ uint32(x^y)
}

// Layout checks that the params struct read on the GPU has the fields
// where the Go side put them, the bools included.
func Layout(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	out[i*8+0] = p.A
	out[i*8+1] = uint32(p.B)
	out[i*8+2] = uint32(p.B >> 32)
	out[i*8+3] = p.C[0] ^ p.C[1]<<8 ^ p.C[2]<<16
	out[i*8+4] = uint32(p.D.E)
	out[i*8+5] = uint32(p.D.F)
	out[i*8+6] = uint32(p.D.F >> 32)
	flags := p.L << 2
	if p.H {
		flags |= 1
	}
	if p.K {
		flags |= 2
	}
	out[i*8+7] = p.G + p.C[i&3&1] ^ flags<<8
}

// Bytes checks the byte helpers: big-endian reads that straddle the
// end of the buffer and wrap, and writes in the thread's private
// region read back through the same helpers.
func Bytes(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	end := uint32(compute.VDP2VRAMSize) - 3 + (i & 3)
	out[i*8+0] = uint32(mem.U8(end))
	out[i*8+1] = uint32(mem.U16BE(end))
	out[i*8+2] = mem.U32BE(end)
	region := 1024 + i*16
	mem.PutU8(region, uint8(w0))
	mem.PutU16BE(region+2, uint16(w0))
	mem.PutU32BE(region+4, w1)
	mem.PutU8(region+8+(w0&7), uint8(w1>>8))
	out[i*8+3] = uint32(mem.U8(region))
	out[i*8+4] = uint32(mem.U16BE(region + 2))
	out[i*8+5] = mem.U32BE(region + 4)
	out[i*8+6] = mem.U32BE(region+8) ^ mem.U32BE(region+12)
	out[i*8+7] = uint32(mem.U16BE(region + 1))
}

// Words checks word reads and writes by index: an int32 index, a
// compound assignment and an increment on a buffer word, and the min
// and max builtins over the words read.
func Words(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	out[i*8+0] = w0
	out[i*8+1] = w1
	out[i*8+1] += w0
	out[i*8+2] = w0 ^ w1
	out[i*8+2]++
	out[i*8+3] = in[int32(i)*8+2]
	out[i*8+4] = min(w0, w1) + max(w0, 7)
	out[i*8+5] = uint32(min(int32(w0), 5)) + uint32(max(int32(w0), -5))
	out[i*8+6] = uint32(min(max(int32(w0), -100), 100)) + uint32(abs32(int32(w0)))
	out[i*8+7] = min(w0, 1000, 0xFFFF)
}

// bump counts its calls and is always true.
func bump(n *int32) bool {
	*n++
	return true
}

// ShortCircuit checks that a right operand with a call runs only
// when the left operand leaves the result undecided.
func ShortCircuit(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	a := int32(in[i*8])
	n := int32(0)
	var flag uint32
	if a > 0 && bump(&n) {
		flag |= 1
	}
	if a > 0 || bump(&n) {
		flag |= 2
	}
	if bump(&n) && a < 0 {
		flag |= 4
	}
	if a < 0 && a > -100 && bump(&n) {
		flag |= 8
	}
	if !(a > 0 && bump(&n)) {
		flag |= 16
	}
	// A call in an earlier operand runs before the rewrite of a
	// short-circuit in a later one, in Go's left-to-right order.
	var seq uint32
	flag |= pick(first(&seq), a > 0 && second(&seq)) << 5
	out[i*8+0] = uint32(n)
	out[i*8+1] = flag
	out[i*8+2] = seq
}

// first and second append their number to the sequence of calls;
// pick is x when b holds.
func first(seq *uint32) uint32 {
	*seq = *seq<<4 | 1
	return 1
}

func second(seq *uint32) bool {
	*seq = *seq<<4 | 2
	return true
}

func pick(x uint32, b bool) uint32 {
	if b {
		return x
	}
	return 0
}

// Tables checks lookup table indexing.
func Tables(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	out[i*8+0] = table[i&7]
	out[i*8+1] = table[w0&7] + table[(w0>>3)&7]<<8
	var sum uint32
	for k := range uint32(8) {
		sum += table[k] * (k + 1)
	}
	out[i*8+2] = sum
}

// stepper is a struct with a pointer-receiver method.
type stepper struct {
	x, dx int32
}

func (s *stepper) step() { s.x += s.dx }

func (s stepper) value() int32 { return s.x }

// divmod returns two results.
func divmod(a, b uint32) (q, r uint32) {
	q = a / b
	r = a % b
	return
}

// Control checks the loop forms, range over an integer with and
// without a key, break and continue, switch, method calls, multiple
// results, and named results.
func Control(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	w0 := in[i*8]
	w1 := in[i*8+1]
	sum := uint32(0)
	for k := uint32(0); k < (w0 & 15); k++ {
		if k == 3 {
			continue
		}
		sum += k
	}
	for k := range w1 & 15 {
		if k == 5 {
			continue
		}
		// An assignment to the key leaves the iterations as they are.
		k += 100
		sum += k
	}
	for range 3 {
		sum++
	}
	k := uint32(0)
	for k < (w1 & 31) {
		k += 3
	}
	for {
		sum++
		if sum > (w0 & 63) {
			break
		}
	}
	switch w0 & 3 {
	case 0:
		sum += 10
	case 1, 2:
		sum += 20
	default:
		sum += 30
	}
	var s stepper = stepper{x: int32(w1 & 0xFF), dx: 3}
	s.step()
	s.step()
	q, r := divmod(w0, (w1&0xFF)|1)
	found := uint32(0)
	for y := range uint32(4) {
		for x := range uint32(4) {
			if x*y == (w0 & 7) {
				found = y<<4 | x
				break
			}
		}
	}
	out[i*8+0] = sum
	out[i*8+1] = k
	out[i*8+2] = uint32(s.value())
	out[i*8+3] = q
	out[i*8+4] = r
	out[i*8+5] = found
	// Parallel assignment and compound operators.
	a, b := w0, w1
	a, b = b, a
	a += b
	a ^= b >> 3
	a <<= 2
	a &^= 0xF
	out[i*8+6] = a - b
	out[i*8+7] = w0&^w1 | w0&w1
}

// CacheTail checks a loop-carried cache whose key and flag are set at
// the end of an if with no else, through a parallel assignment from a
// local declared in the block. The key changes every four steps, so
// the cache both misses and hits, and each step records the cached
// value, the key, and the miss count. A swap at the end of a second if
// exchanges two values the block itself reads.
func CacheTail(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	have := false
	last := uint32(0)
	val := uint32(0)
	misses := uint32(0)
	a, b := in[i*8], in[i*8+1]
	for j := uint32(0); j < 8; j++ {
		key := in[i*8+(j>>2)] >> 30
		if !have || key != last {
			val = key*0x9E3779B1 ^ in[i*8+j]
			misses++
			k := key
			last, have = k, true
		}
		if key&1 != 0 {
			a, b = b, a
		}
		out[i*8+j] = val ^ last<<8 ^ misses<<16 ^ a ^ b>>1
	}
}

// Segments is a span entry and checks the wrapper contract: each
// thread receives a segment [x0, x1) clamped at the bound, and the
// row's params, whose G field is the row. Each dot records its
// address, the row's A, and the segment length it was part of.
func Segments(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, x0, x1 int32) {
	segmentDots(out, p, x0, x1)
}

// DoubleSegments is Segments dispatched at twice its segment, the
// record a backend giving each thread two units sends, so the dots
// record the doubled segment length.
func DoubleSegments(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, x0, x1 int32) {
	segmentDots(out, p, x0, x1)
}

// segmentDots records each dot of segment [x0, x1): its address, the
// row's A, and the segment length.
func segmentDots(out compute.Layer, p *Probe, x0, x1 int32) {
	for x := x0; x < x1; x++ {
		out[p.G*4096+uint32(x)] = p.A + uint32(x)*3 + uint32(x1-x0)<<24
	}
}

// WideThreads is the Wide entry's thread count: past the thread groups
// an API lets one dispatch issue per dimension (65535 of the index
// form's 64), so a backend with that limit issues it in two pieces.
const WideThreads = 65535*64 + 4096

// Wide is an index entry dispatched wider than an API's thread group
// limit, and checks that a backend issuing it in pieces gives every
// thread its index across the whole grid: a thread of the last 4096
// writes its index at its place in the tail, so a piece whose base
// were lost or wrong leaves the tail unwritten or misplaced.
func Wide(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, i uint32) {
	const tail = WideThreads - 4096
	if i >= tail {
		out[(i-tail)*8] = i ^ p.A
		out[(i-tail)*8+1] = i - tail
	}
}

// Group is a group entry and checks the wrapper contract for a group
// and the group barrier: each thread receives its index t in the
// group, the group's size n, and the group's index g, with the
// group's params. The threads fill the group's region of mem by
// stride, the barrier orders the fill ahead of the read, and thread
// 0 sums the region into the group's output with the group's G. The
// result is the same for any group size, so the Go execution as one
// thread per group is the reference for a device group of many.
func Group(in compute.Layer, mem *compute.VDP2VRAM, out compute.Layer, p *Probe, t, n, g uint32) {
	base := 0x20000 + g*512
	for i := t; i < 128; i += n {
		mem.PutU32BE(base+i*4, p.A+i*7+g<<16)
	}
	compute.NextPhase()
	if t == 0 {
		sum := uint32(0)
		for i := range uint32(128) {
			sum += mem.U32BE(base + i*4)
		}
		out[g*8] = sum
		out[g*8+1] = p.G
	}
}
