// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"fmt"
	"testing"
	"unsafe"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/internal/hostgpu"
	"github.com/user-none/erings/internal/hostgpu/backend"
	"github.com/user-none/erings/internal/hostgpu/devicetest"
)

// The generation passes over the benchmark scenes. Each scene is
// drawn by the walk, then its ops are drawn again by the generate
// entries,
// the tag pass and the write pass, and the two frame buffers are
// compared. The scenes whose ops all replace are the ones the two
// passes reproduce; a scene with a destination-reading op is drawn
// for its cost and not compared.

// vdp1GenerateThreads is the threads a device gives a group across
// its op in the device runs, whole workgroups of the group size.
var vdp1GenerateThreads = [...]int{128, 512, 1024}

// vdp1GenerateInput is a benchmark scene drawn by the walk: the VDP1
// after the draw, the draw buffer as the frame change left it before
// the draw, and the frame's ops.
type vdp1GenerateInput struct {
	name string
	v    *VDP1
	pre  []byte
	ops  []draw.DrawOp
}

// vdp1GenerateScene draws a scene through the walk in one call, one
// entry per op, and keeps what a device draw of the same ops starts
// from.
func vdp1GenerateScene(s benchVDP1Scene) vdp1GenerateInput {
	v := s.build()
	benchVDP1Change(v)
	pre := append([]byte(nil), v.drawFB...)
	v.TickSystemCycles(benchVDP1Budget)
	return vdp1GenerateInput{name: s.name, v: v, pre: pre, ops: v.ops}
}

// vdp1GenerateSceneWalker draws a scene through the walk a scanline
// at a time, so the list holds the entries the walk closes at every
// line it is inside an op, and keeps what a device draw of those
// entries starts from.
func vdp1GenerateSceneWalker(s benchVDP1Scene) vdp1GenerateInput {
	v := s.build()
	benchVDP1Change(v)
	pre := append([]byte(nil), v.drawFB...)
	for i := 0; i < benchVDP1WalkerMaxTicks && v.drawActive; i++ {
		v.TickSystemCycles(benchVDP1WalkerTick)
	}
	return vdp1GenerateInput{name: s.name, v: v, pre: pre, ops: v.ops}
}

// vdp1GenerateExtraScenes are scenes the generate tests draw beyond
// the benchmark scenes, each separating one path of the passes from
// the others: an erase on an 8-bit frame buffer stores bytes where
// the benchmark's erase stores halfwords, an RGB sprite without
// Gouraud reads 16-bit texels with no Gouraud step, an 8bpp sprite
// with Gouraud takes the Gouraud step on byte texels, 8bpp sprites
// on an 8-bit frame buffer store bytes in the write pass, and a
// polygon and a distorted sprite whose side edges round a half-way
// position apart draw a connecting line one dot longer than the
// longer of A-B and D-C, which the passes' chunks must still cover.
func vdp1GenerateExtraScenes() []benchVDP1Scene {
	grid := func(setup func(v *VDP1), cols, rows int, dx, dy int16, f func(x, y int16) benchCmd) func() *VDP1 {
		return func() *VDP1 {
			v, l := benchVDP1()
			setup(v)
			benchGrid(l, cols, rows, dx, dy, f)
			l.end()
			return v
		}
	}
	none := func(v *VDP1) {}
	fb8 := func(v *VDP1) { v.Write(0x00, 0x0001) }
	return []benchVDP1Scene{
		{name: "erase full frame 8-bit", build: func() *VDP1 {
			v, l := benchVDP1()
			fb8(v)
			v.Write(0x06, 0x8421)
			v.Write(0x08, 0x0000)
			v.Write(0x0A, 0x80FF)
			l.end()
			return v
		}},
		{name: "normal sprite RGB", build: grid(none, 12, 7, 26, 30, func(x, y int16) benchCmd {
			return benchCmd{pmod: 5 << 3, srca: benchTexRGB / 8, size: benchSize32, xa: x, ya: y}
		})},
		{name: "normal sprite 8bpp gouraud", build: grid(none, 12, 7, 26, 30, func(x, y int16) benchCmd {
			return benchCmd{pmod: 4<<3 | 4, colr: 0x0100, srca: benchTex8 / 8, size: benchSize32, xa: x, ya: y, grda: benchGRDA}
		})},
		{name: "normal sprite 8bpp on 8-bit frame buffer", build: grid(fb8, 12, 7, 26, 30, benchNormal8)},
		{name: "rounded connecting lines", build: func() *VDP1 {
			v, l := benchVDP1()
			l.add(benchCmd{ctrl: 0x0004, colr: 0x83E0, xa: 0, ya: 0, xb: 31, yb: 0, xc: 32, yc: 10, xd: 1, yd: 10})
			l.add(benchCmd{ctrl: 0x0002, pmod: 5 << 3, srca: benchTexRGB / 8, size: benchSize32,
				xa: 40, ya: 0, xb: 71, yb: 0, xc: 72, yc: 10, xd: 41, yd: 10})
			l.end()
			return v
		}},
	}
}

// vdp1GenerateScenes is every scene the generate tests draw: the
// benchmark scenes, then the extra scenes.
func vdp1GenerateScenes() []benchVDP1Scene {
	return append(benchVDP1Scenes(), vdp1GenerateExtraScenes()...)
}

// replaceClass reports whether every op's dots replace, the class the
// two passes draw as the walk does.
func replaceClass(ops []draw.DrawOp) bool {
	for i := range ops {
		if draw.ReadsDestination(&ops[i].Pixel) {
			return false
		}
	}
	return true
}

// firstDiff is the index of the first differing byte, or -1.
func firstDiff(a, b []byte) int {
	for i := range a {
		if i >= len(b) || a[i] != b[i] {
			return i
		}
	}
	if len(b) > len(a) {
		return len(a)
	}
	return -1
}

// TestVDP1GenerateMatchesWalk runs the two passes and the resolve as
// Go loops over every benchmark scene with a group's threads in an
// order a device would never choose, the tag pass backwards and the
// write pass forwards, and holds the frame buffer equal to the
// walk's. The passes are order-free by construction, so any thread
// order must give the walk's picture. The scene is drawn both in one
// call, an entry per op, and a scanline at a time, the entries the
// walk closes at every line it is inside an op.
func TestVDP1GenerateMatchesWalk(t *testing.T) {
	for _, s := range vdp1GenerateScenes() {
		for _, form := range []struct {
			name  string
			scene func(benchVDP1Scene) vdp1GenerateInput
		}{{"one call", vdp1GenerateScene}, {"walker", vdp1GenerateSceneWalker}} {
			in := form.scene(s)
			if len(in.ops) == 0 {
				continue
			}
			t.Run(s.name+"/"+form.name, func(t *testing.T) {
				vdp1GenerateMatch(t, in)
			})
		}
	}
}

// vdp1GenerateMatch runs the passes and the resolve over the input's
// entries and holds the frame buffer equal to the walk's.
func vdp1GenerateMatch(t *testing.T, in vdp1GenerateInput) {
	t.Helper()
	fb := compute.VDP1FB(append([]byte(nil), in.pre...))
	b := draw.Buffers{VRAM: (*compute.VDP1VRAM)(in.v.vram), FB: fb, Round: newRoundState(), Records: in.v.lineRecords}
	const n = 7
	for i := range in.ops {
		for th := n - 1; th >= 0; th-- {
			draw.GenerateTag(&b, &in.ops[i], uint32(th), n, uint32(i))
		}
	}
	for i := range in.ops {
		for th := range uint32(n) {
			draw.GenerateWrite(&b, &in.ops[i], th, n, uint32(i))
		}
	}
	for i := range uint32(compute.VDP1FBSize / 2) {
		draw.Resolve(fb, b.Round, i)
	}
	if !bytes.Equal(fb[:], in.v.drawFB) {
		t.Fatalf("the passes differ from the walk at byte %d", firstDiff(fb[:], in.v.drawFB))
	}
}

// vdp1Device is the platform's device with the generate pipeline and
// the buffers of one scene.
type vdp1Device struct {
	prog  *hostgpu.Program
	pipes [2]hostgpu.Pipeline // the tag pass and the write pass
	bufs  []hostgpu.Buffer

	vram, rec, opBuf    hostgpu.Buffer // upload buffers the passes read
	pre                 hostgpu.Buffer // the frame buffer's pre-draw contents, which reset copies in
	fb                  hostgpu.Buffer // working buffers the passes write
	stamps, heads, pool hostgpu.Buffer // the round state, in field order
	ops                 int
}

// openVDP1Device builds the compute sources on the platform's
// backend, skipping the test where there is none, and resolves the
// generate pipeline.
func openVDP1Device(tb testing.TB) *vdp1Device {
	tb.Helper()
	parent, subs := ComputeSources()
	prog, err := backend.Open(parent, subs...)
	if err != nil {
		tb.Skip(err)
	}
	tb.Logf("device %s", prog.Device.Name())
	d := &vdp1Device{prog: prog}
	tb.Cleanup(func() {
		d.release()
		prog.Device.Close()
	})
	for i, name := range [2]string{"GenerateTag", "GenerateWrite"} {
		if d.pipes[i], err = prog.Pipeline(name); err != nil {
			tb.Fatal(err)
		}
	}
	return d
}

// release frees the scene's buffers.
func (d *vdp1Device) release() {
	for _, b := range d.bufs {
		b.Release()
	}
	d.bufs = nil
}

// load allocates the scene's buffers and uploads what does not
// change between runs: the VRAM, the line records, the ops, and the
// frame buffer's pre-draw contents.
func (d *vdp1Device) load(tb testing.TB, in vdp1GenerateInput) {
	tb.Helper()
	d.release()
	buffer := func(size int, kind hostgpu.BufferKind) hostgpu.Buffer {
		b, err := d.prog.Device.NewBuffer(size, kind)
		if err != nil {
			tb.Fatal(err)
		}
		d.bufs = append(d.bufs, b)
		return b
	}
	d.ops = len(in.ops)
	opSize := int(unsafe.Sizeof(draw.DrawOp{}))
	d.vram = buffer(compute.VDP1VRAMSize, hostgpu.BufferUpload)
	d.pre = buffer(compute.VDP1FBSize, hostgpu.BufferUpload)
	d.fb = buffer(compute.VDP1FBSize, hostgpu.BufferWorking)
	s := bufferSizes(0, 0, 0, 0, compute.VDP1FBSize, 1)
	d.stamps = buffer(s.stamps*4, hostgpu.BufferWorking)
	d.heads = buffer(s.heads*4, hostgpu.BufferWorking)
	d.pool = buffer(s.pool*4, hostgpu.BufferWorking)
	d.rec = buffer(len(in.v.lineRecords)*4, hostgpu.BufferUpload)
	d.opBuf = buffer(max(d.ops, 1)*opSize, hostgpu.BufferUpload)
	copy(d.vram.Bytes(), in.v.vram)
	copy(d.pre.Bytes(), in.pre)
	rec := in.v.lineRecords
	copy(d.rec.Bytes(), unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(rec))), int(in.v.recordWords)*4))
	if len(in.ops) > 0 {
		copy(d.opBuf.Bytes(), unsafe.Slice((*byte)(unsafe.Pointer(&in.ops[0])), len(in.ops)*opSize))
	}
}

// reset puts the scene's frame buffer back to its pre-draw contents
// and clears the round state, in a submission waited for at once.
func (d *vdp1Device) reset(tb testing.TB) {
	tb.Helper()
	cmds, err := d.prog.Device.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	cmds.Copy(d.fb, 0, d.pre, 0, compute.VDP1FBSize)
	for _, b := range []hostgpu.Buffer{d.stamps, d.heads, d.pool} {
		cmds.Fill(b, 0, b.Size(), 0)
	}
	if err := d.prog.Device.Wait(cmds.Commit()); err != nil {
		tb.Fatal(err)
	}
}

// draw encodes the passes over the scene's ops, each op a group of
// threads across, the write pass behind a barrier when both run, and
// waits for the submission.
func (d *vdp1Device) draw(tb testing.TB, threads int, write bool) {
	tb.Helper()
	cmds, err := d.prog.Device.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	rec := hostgpu.Dispatch{BoundX: uint32(threads), BoundY: uint32(d.ops), ParamsStride: uint32(unsafe.Sizeof(draw.DrawOp{}))}
	grid := [2]int{threads, d.ops}
	bufs := []hostgpu.Buffer{d.vram, d.fb, d.stamps, d.heads, d.pool, d.rec, d.opBuf}
	cmds.Dispatch(d.pipes[0], bufs, rec, grid)
	if write {
		cmds.Barrier()
		cmds.Dispatch(d.pipes[1], bufs, rec, grid)
	}
	if err := d.prog.Device.Wait(cmds.Commit()); err != nil {
		tb.Fatal(err)
	}
}

// check holds the device frame buffer equal to the walk's for a
// replace-class scene.
func (d *vdp1Device) check(tb testing.TB, in vdp1GenerateInput) {
	tb.Helper()
	if !replaceClass(in.ops) {
		return
	}
	if got, want := devicetest.ReadBack(tb, d.prog.Device, d.fb), in.v.drawFB; !bytes.Equal(got, want) {
		tb.Fatalf("%s: the device passes differ from the walk at byte %d: %s", in.name, firstDiff(got, want), bytesAround(firstDiff(got, want), got, want, in.pre))
	}
}

// bytesAround shows eight bytes from i of the device buffer, the
// reference, and the pre-draw buffer, so a failure tells a store
// that never landed from one that landed with the wrong value.
func bytesAround(i int, got, want, pre []byte) string {
	window := func(b []byte) []byte {
		return b[i:min(i+8, len(b))]
	}
	return fmt.Sprintf("device % x, want % x, before the draw % x", window(got), window(want), window(pre))
}

// vdp1TagPass runs the tag pass alone over the input's entries as Go
// loops with a group of seven threads and returns the frame buffer
// and the order stamps it leaves: an erase writes the frame buffer
// in the tag pass, every other op raises stamps.
func vdp1TagPass(in vdp1GenerateInput) ([]byte, compute.OrderStamps) {
	fb := compute.VDP1FB(append([]byte(nil), in.pre...))
	b := draw.Buffers{VRAM: (*compute.VDP1VRAM)(in.v.vram), FB: fb, Round: newRoundState(), Records: in.v.lineRecords}
	const n = 7
	for i := range in.ops {
		for th := range uint32(n) {
			draw.GenerateTag(&b, &in.ops[i], th, n, uint32(i))
		}
	}
	return fb, b.Round.Stamps
}

// checkTag holds the device's frame buffer and order stamps after the
// tag pass alone equal to the Go tag pass's for a replace-class
// scene. A stamp difference reports how many stamps each side raised,
// which tells a pass that never stored from one that stored wrongly.
func (d *vdp1Device) checkTag(tb testing.TB, in vdp1GenerateInput, fb []byte, stamps compute.OrderStamps) {
	tb.Helper()
	if !replaceClass(in.ops) {
		return
	}
	if got := devicetest.ReadBack(tb, d.prog.Device, d.fb); !bytes.Equal(got, fb) {
		tb.Fatalf("%s: the device tag pass differs from the Go tag pass at frame buffer byte %d: %s", in.name, firstDiff(got, fb), bytesAround(firstDiff(got, fb), got, fb, in.pre))
	}
	stampBytes := devicetest.ReadBack(tb, d.prog.Device, d.stamps)
	got := unsafe.Slice((*uint32)(unsafe.Pointer(&stampBytes[0])), len(stampBytes)/4)
	for i := range stamps {
		if got[i] != stamps[i] {
			tb.Fatalf("%s: the device tag pass differs from the Go tag pass at stamp %d: device %08x go %08x (device raised %d stamps, go %d)", in.name, i, got[i], stamps[i], countNonzero(got), countNonzero(stamps))
		}
	}
}

// countNonzero is the number of nonzero words.
func countNonzero(words []uint32) int {
	n := 0
	for _, w := range words {
		if w != 0 {
			n++
		}
	}
	return n
}

// TestGPUVDP1Generate runs the passes on the device over every
// benchmark scene at each thread count: the tag pass alone, held
// equal to the Go tag pass, then both passes, held equal to the
// walk, for the replace-class scenes.
func TestGPUVDP1Generate(t *testing.T) {
	d := openVDP1Device(t)
	for _, s := range vdp1GenerateScenes() {
		in := vdp1GenerateScene(s)
		if len(in.ops) == 0 {
			continue
		}
		d.load(t, in)
		tagFB, tagStamps := vdp1TagPass(in)
		for _, threads := range vdp1GenerateThreads {
			t.Run(fmt.Sprintf("%s/%d", s.name, threads), func(t *testing.T) {
				d.reset(t)
				d.draw(t, threads, false)
				d.checkTag(t, in, tagFB, tagStamps)
				d.reset(t)
				d.draw(t, threads, true)
				d.check(t, in)
			})
		}
	}
}

// BenchmarkVDP1Generate measures the device's generation of each
// benchmark scene's ops: the tag pass alone and the tag pass with the
// write pass, at each thread count per op. ns/op is the submission
// of one scene's draw and its wait, comparable with BenchmarkVDP1's
// cost of the walk drawing the same scene. The frame buffer and the
// round state are reset outside the timed region, and the replace-class
// scenes are held equal to the walk after the timed loop.
func BenchmarkVDP1Generate(b *testing.B) {
	d := openVDP1Device(b)
	for _, s := range benchVDP1Scenes() {
		in := vdp1GenerateScene(s)
		if len(in.ops) == 0 {
			continue
		}
		d.load(b, in)
		for _, threads := range vdp1GenerateThreads {
			for _, form := range []struct {
				name  string
				write bool
			}{{"tag", false}, {"tag+write", true}} {
				b.Run(fmt.Sprintf("%s/%d/%s", s.name, threads, form.name), func(b *testing.B) {
					for i := 0; i < b.N; i++ {
						b.StopTimer()
						d.reset(b)
						b.StartTimer()
						d.draw(b, threads, form.write)
					}
					b.StopTimer()
					if form.write {
						d.check(b, in)
					}
				})
			}
		}
	}
}
