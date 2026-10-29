// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/draw"
	"github.com/user-none/erings/core/compute/screen"
	"github.com/user-none/erings/internal/hostgpu"
)

// walkOps builds the scene's VDP1, runs its frame change and a frame
// of ticks, and returns the VDP1 with the entries the walk appended.
func walkOps(t *testing.T, s benchVDP1Scene) *VDP1 {
	t.Helper()
	v := s.build()
	benchVDP1Change(v)
	for line := 0; line < vdp1DriveScanlines; line++ {
		v.TickSystemCycles(benchVDP1WalkerTick)
	}
	if len(v.ops) == 0 {
		t.Fatalf("%s: no entry", s.name)
	}
	return v
}

// doubledScenes are the enhanced VDP1 scenes that draw under double
// interlace, each with the DIL parity its FBCR selects.
func doubledScenes(t *testing.T) []benchVDP1Scene {
	t.Helper()
	var out []benchVDP1Scene
	for _, s := range enhancedVDP1Scenes(t) {
		if strings.Contains(s.name, "double interlace") {
			out = append(out, s.benchVDP1Scene)
		}
	}
	if len(out) == 0 {
		t.Fatal("no double interlace scene")
	}
	return out
}

// TestDeriveOpDoubledHalf draws the entries of every double interlace
// scene three ways at scale shifts 0 to 2: natively with the parity
// the scene selects, natively with the other parity, and derived for a
// doubled half (deriveOp). It holds the doubled pair equal to the two
// native parities interleaved: doubled row 2r + dil is the scene's
// parity's physical row r and 2r + 1 - dil the other's, the erase
// covering both, so a doubled half holds both parities' draws of the
// same command list.
func TestDeriveOpDoubledHalf(t *testing.T) {
	for _, s := range doubledScenes(t) {
		for shift := uint32(0); shift <= 2; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				v := walkOps(t, s)
				area := 2 * shift
				var parity [2][2]compute.VDP1FB
				var doubled [2]compute.VDP1FB
				for h := range doubled {
					for p := range parity[h] {
						parity[h][p] = make(compute.VDP1FB, compute.VDP1FBSize<<area)
					}
					doubled[h] = make(compute.VDP1FB, 2*compute.VDP1FBSize<<area)
				}
				dil := 0
				if v.dilOdd() {
					dil = 1
				}
				b := draw.Buffers{VRAM: (*compute.VDP1VRAM)(v.vram), Records: v.lineRecords}
				var dev draw.DrawOp
				for i := range v.ops {
					src := &v.ops[i]
					h := src.Buffer & 1
					for p := range parity[h] {
						op := *src
						if op.Kind != draw.OpErase {
							if op.Pixel.DIE == 0 {
								t.Fatalf("entry %d draws outside double interlace", i)
							}
							op.Pixel.DILOdd = uint32(p)
						}
						scaleOp(&dev, &op, shift)
						b.FB = parity[h][p]
						if dev.Kind == draw.OpErase {
							draw.EraseRows(b.FB, &dev, 0, draw.OpLines(&dev))
						} else {
							draw.Generate(&b, &dev, 0, 1)
						}
					}
					deriveOp(&dev, src, shift, fbHalfLayout{doubled: true, dil: dil})
					if dev.Kind != draw.OpErase && dev.Pixel.DIE != 0 {
						t.Fatalf("entry %d keeps the parity gate into a doubled half", i)
					}
					b.FB = doubled[h]
					if dev.Kind == draw.OpErase {
						draw.EraseRows(b.FB, &dev, 0, draw.OpLines(&dev))
					} else {
						draw.Generate(&b, &dev, 0, 1)
					}
				}
				n := 1 << shift
				rowBytes := v.fbWidth() * 2 << shift
				rows := v.fbHeight() << shift
				for h := range doubled {
					for r := 0; r < rows; r++ {
						want := parity[h][dil][r*rowBytes : (r+1)*rowBytes]
						other := parity[h][1-dil][r*rowBytes : (r+1)*rowBytes]
						// Scaled row r is native row r / N, sub-row
						// r mod N; its doubled rows are (2 (r / N) +
						// p) N + r mod N.
						a := (2*(r/n)+dil)*n + r%n
						o := (2*(r/n)+1-dil)*n + r%n
						if got := doubled[h][a*rowBytes : (a+1)*rowBytes]; !bytes.Equal(got, want) {
							t.Fatalf("half %d doubled row %d differs from the scene's parity's row %d at byte %d", h, a, r, firstDiff(got, want))
						}
						if got := doubled[h][o*rowBytes : (o+1)*rowBytes]; !bytes.Equal(got, other) {
							t.Fatalf("half %d doubled row %d differs from the other parity's row %d at byte %d", h, o, r, firstDiff(got, other))
						}
					}
				}
			})
		}
	}
}

// TestDeriveOpNativeHalf holds deriveOp into a native half to scaleOp:
// the parity gate, the erase rectangle, and the frame buffer height
// are the scaled op's.
func TestDeriveOpNativeHalf(t *testing.T) {
	for _, s := range doubledScenes(t) {
		v := walkOps(t, s)
		var want, got draw.DrawOp
		for shift := uint32(0); shift <= 2; shift++ {
			for i := range v.ops {
				scaleOp(&want, &v.ops[i], shift)
				deriveOp(&got, &v.ops[i], shift, fbHalfLayout{dil: 1})
				if got != want {
					t.Fatalf("%s shift %d entry %d: %+v, want %+v", s.name, shift, i, got, want)
				}
			}
		}
	}
}

// TestSpriteRowDoubledHalf holds the sprite row read of a doubled
// half on both fields: field line y reads doubled row 2y + dil in the
// 16-bit and 8-bit direct reads and its sibling 2y + 1 - dil; the
// rotated read keeps its coordinate and step in physical rows and
// carries the doubled flag and the parity, the sibling's start one
// display line of the per-line step from the row's, forward on field
// 0 and back on field 1; a native half's read is unchanged and its
// sibling reads the same row.
func TestSpriteRowDoubledHalf(t *testing.T) {
	v := spriteVDP2()
	v.BeginFrame()
	v.frame.sprRotA.dxst, v.frame.sprRotA.dyst = 2<<10, 3<<10
	fb := make([]byte, 2*compute.VDP1FBSize)
	for _, field := range []int{0, 1} {
		v.frame.field = field
		step := int64(1 - 2*field)
		for _, dil := range []int{0, 1} {
			v.lineFB = vdp1FBView{data: fb, width: 512, height: 256, fbHalfLayout: fbHalfLayout{doubled: true, dil: dil}}
			r := v.spriteRowAt(37, field)
			if r.Mode != screen.SpriteRead16 || r.Row != uint32((2*37+dil)*512*2) || r.Height != 256 || r.Width != 512 {
				t.Fatalf("field %d dil %d: 16-bit mode %d row %d height %d width %d", field, dil, r.Mode, r.Row, r.Height, r.Width)
			}
			if r = v.spriteRowAt(37, 1-field); r.Row != uint32((2*37+1-dil)*512*2) {
				t.Fatalf("field %d dil %d: 16-bit sibling row %d", field, dil, r.Row)
			}
			v.lineFB = vdp1FBView{data: fb, width: 1024, height: 256, is8bpp: true, fbHalfLayout: fbHalfLayout{doubled: true, dil: dil}}
			if r = v.spriteRowAt(5, field); r.Mode != screen.SpriteRead8 || r.Row != uint32((2*5+dil)*1024) {
				t.Fatalf("field %d dil %d: 8-bit mode %d row %d", field, dil, r.Mode, r.Row)
			}
			if r = v.spriteRowAt(5, 1-field); r.Row != uint32((2*5+1-dil)*1024) {
				t.Fatalf("field %d dil %d: 8-bit sibling row %d", field, dil, r.Row)
			}
			v.lineFB = vdp1FBView{data: fb, width: 512, height: 256, rotated: true, fbHalfLayout: fbHalfLayout{doubled: true, dil: dil}}
			v.lineSprRot.enabled = true
			v.lineSprRot.baseX, v.lineSprRot.baseY = 7<<10, 11<<10
			v.lineSprRot.dx, v.lineSprRot.dy = 3, 5
			r = v.spriteRowAt(0, field)
			if r.Mode != screen.SpriteReadRotated || r.BaseX != 7<<10 || r.BaseY != 11<<10 || r.DX != 3 || r.DY != 5 || r.Height != 256 {
				t.Fatalf("field %d dil %d: rotated mode %d base (%d, %d) step (%d, %d) height %d", field, dil, r.Mode, r.BaseX, r.BaseY, r.DX, r.DY, r.Height)
			}
			if r.Doubled != 1 || r.Parity != uint32(dil) || r.Shift != 0 {
				t.Fatalf("field %d dil %d: rotated doubled %d parity %d shift %d", field, dil, r.Doubled, r.Parity, r.Shift)
			}
			r = v.spriteRowAt(0, 1-field)
			if r.BaseX != (7+2*step)<<10 || r.BaseY != (11+3*step)<<10 || r.DY != 5 || r.Doubled != 1 || r.Parity != uint32(1-dil) {
				t.Fatalf("field %d dil %d: rotated sibling base (%d, %d) step y %d doubled %d parity %d", field, dil, r.BaseX, r.BaseY, r.DY, r.Doubled, r.Parity)
			}
			v.lineSprRot.enabled = false
		}
	}
	field := v.frame.field
	v.lineFB = vdp1FBView{data: fb, width: 512, height: 256, fbHalfLayout: fbHalfLayout{dil: 1}}
	if r := v.spriteRowAt(37, field); r.Row != 37*512*2 || r.Height != 256 {
		t.Fatalf("native half: row %d height %d", r.Row, r.Height)
	}
	if r := v.spriteRowAt(37, 1-field); r.Row != 37*512*2 || r.Height != 256 {
		t.Fatalf("native half sibling: row %d height %d", r.Row, r.Height)
	}
	v.lineFB = vdp1FBView{data: fb, width: 512, height: 256, rotated: true, fbHalfLayout: fbHalfLayout{dil: 1}}
	v.lineSprRot.enabled = true
	if r := v.spriteRowAt(0, field); r.Doubled != 0 || r.Parity != 0 {
		t.Fatalf("native half rotated: doubled %d parity %d", r.Doubled, r.Parity)
	}
}

// TestBeginFieldHalfLayout holds the layout beginField records for the
// draw half, the buffer marks a layout change makes, and the one
// growth cue at the session's first doubled half. A half is doubled
// only with deinterlacing on, DIE on, and effective LSMD=3.
func TestBeginFieldHalfLayout(t *testing.T) {
	v2 := spriteVDP2()
	v1, l := benchVDP1()
	l.end()
	v1.Write(0x02, 0x000C)
	vdp1DriveChange(v1)
	h := v1.physicalBuffer(v1.drawFB)
	if !v1.dieEnabled() || !v1.dilOdd() {
		t.Fatal("DIE and DIL not latched")
	}

	benchReg(v2, vdp2TVMD, 0x80C0)
	v2.BeginFrame()
	if v2.frame.effIntl != 3 {
		t.Fatalf("effective interlace %d", v2.frame.effIntl)
	}
	if v2.beginField(v1) || v2.enhance.half[h].doubled || v2.rowFactor() != 1 {
		t.Fatal("a doubled half with deinterlacing off")
	}
	v2.setEnhance(enhanceConfig{deinterlace: true})
	v1.fbWritesTaken = true
	if !v2.beginField(v1) {
		t.Fatal("the first doubled half did not cue growth")
	}
	if got := v2.enhance.half[h]; got != (fbHalfLayout{doubled: true, dil: 1}) {
		t.Fatalf("draw half layout %+v", got)
	}
	if v2.rowFactor() != 2 {
		t.Fatalf("row factor %d", v2.rowFactor())
	}
	if v1.fbMask[h][0].Load() != ^uint64(0) || v1.fbMask[1-h][0].Load() != ^uint64(0) {
		t.Fatal("the first doubled half did not mark both halves' bytes written")
	}
	if p := v2.pageCopyParams(v1, h); p != (draw.PageCopyParams{FBWidth: 512, Doubled: 1}) {
		t.Fatalf("doubled half's copy params %+v", p)
	}
	for i := range v1.fbMask[h] {
		v1.fbMask[h][i].Store(0)
	}
	if v2.beginField(v1) {
		t.Fatal("a second doubled half cued growth")
	}
	if v1.fbMask[h][0].Load() != 0 {
		t.Fatal("bytes marked written with no layout change")
	}
	if v2.enhance.half[1-h].doubled {
		t.Fatal("the display half recorded doubled")
	}
	if p := v2.pageCopyParams(v1, 1-h); p != (draw.PageCopyParams{FBWidth: 512}) {
		t.Fatalf("native half's copy params %+v", p)
	}

	benchReg(v2, vdp2TVMD, 0x8000)
	v2.BeginFrame()
	if v2.beginField(v1) || v2.enhance.half[h].doubled || v2.enhance.half[h].dil != 1 {
		t.Fatalf("DIE under a non-interlaced display: %+v", v2.enhance.half[h])
	}
	if v1.fbMask[h][0].Load() != ^uint64(0) {
		t.Fatal("the half that became native is not marked written")
	}
	if p := v2.pageCopyParams(v1, h); p != (draw.PageCopyParams{FBWidth: 512}) {
		t.Fatalf("native half's copy params after the change %+v", p)
	}
	if v2.rowFactor() != 2 {
		t.Fatal("the row factor shrank")
	}

	benchReg(v2, vdp2TVMD, 0x80C0)
	v2.BeginFrame()
	v2.beginField(v1)
	v2.setEnhance(enhanceConfig{shift: 1})
	if v2.enhance.half != [2]fbHalfLayout{} {
		t.Fatalf("records kept with deinterlacing off: %+v", v2.enhance.half)
	}
	if v2.rowFactor() != 2 {
		t.Fatal("the row factor shrank on a configuration change")
	}
}

// progressiveVDP2 is a spriteVDP2 under double-density interlace with
// deinterlacing on.
func progressiveVDP2() *VDP2 {
	return interlacedSpriteVDP2(enhanceConfig{deinterlace: true})
}

// interlacedSpriteVDP2 is a spriteVDP2 under double-density interlace
// with the enhancement configuration, and rotation parameter A set to
// a 30 degree rotation for a rotated frame buffer read.
func interlacedSpriteVDP2(cfg enhanceConfig) *VDP2 {
	v := spriteVDP2()
	benchReg(v, vdp2TVMD, 0x80C0)
	benchReg(v, vdp2RPTAU, 0x0000)
	benchReg(v, vdp2RPTAL, 0xA000)
	benchRotation30(v, benchRotTable)
	v.setEnhance(cfg)
	return v
}

// progressiveDrive drives the scene through the enhanced software
// renderer at the shift over a progressiveVDP2, then the next frame's
// change and its take, and returns the renderer and the VDP1.
func progressiveDrive(t *testing.T, s benchVDP1Scene, shift uint32) (*enhancedRenderer, *VDP1, *VDP2) {
	t.Helper()
	e, v1, v2, _ := interlacedDrive(t, s, enhanceConfig{shift: shift, deinterlace: true})
	return e, v1, v2
}

// interlacedDrive is progressiveDrive with the enhancement
// configuration. The field alternates each frame, as in the frame
// loop. It runs at least five frames, so both halves shown in the last
// two frames were drawn after the drive's texture write in the second
// frame. It returns the last rendered frame's field.
func interlacedDrive(t *testing.T, s benchVDP1Scene, cfg enhanceConfig) (*enhancedRenderer, *VDP1, *VDP2, int) {
	t.Helper()
	v1 := s.build()
	v2 := interlacedSpriteVDP2(cfg)
	e := newEnhancedRenderer(v2, v1)
	d := &vdp1FrameDrive{v1: v1, v2: v2, g: e}
	interlacedFrames(t, d)
	field := v2.frame.field
	d.change()
	e.vdp1Line(1)
	return e, v1, v2, field
}

// interlacedFrames drives the frames of interlacedDrive.
func interlacedFrames(t *testing.T, d *vdp1FrameDrive) {
	t.Helper()
	for frame := 0; ; frame++ {
		d.frame(frame)
		d.v2.oddField = !d.v2.oddField
		if frame >= 4 && !d.v1.drawActive {
			return
		}
		if frame >= 40 {
			t.Fatal("the draw did not complete in 40 frames")
		}
	}
}

// otherParity is the scene with its DIL parity flipped.
func otherParity(s benchVDP1Scene) benchVDP1Scene {
	return benchVDP1Scene{name: s.name + " other parity", build: func() *VDP1 {
		v := s.build()
		v.Write(0x02, v.fbcrPending^0x0004)
		return v
	}}
}

// rotatedDoubledScene draws normal sprites under double interlace
// into the rotation TV mode's frame buffer, which the VDP2 reads
// through rotation parameter A.
var rotatedDoubledScene = enhancedSpriteScene("normal sprites double interlace rotation read", func(v *VDP1, l *benchList) {
	v.Write(0x00, 0x0002)
	v.Write(0x02, 0x0008)
}, benchNormal8)

// TestEnhancedProgressiveFrameMatchesParities renders every double
// interlace scene, the rotated frame buffer read included, through the
// enhanced software renderer at scale shifts 0 and 1 with
// deinterlacing on, and natively for the scene and for the scene with
// its DIL parity flipped. Each display line of the frame's field must
// match the scene's native frame, and each line of the other field the
// flipped scene's native frame. The rows the drive's CPU writes touch
// are skipped: deinterlacing shows those writes on both parities, while
// the native frames show the written half on alternate fields.
func TestEnhancedProgressiveFrameMatchesParities(t *testing.T) {
	scenes := append(doubledScenes(t), rotatedDoubledScene)
	for _, s := range scenes {
		for shift := uint32(0); shift <= 1; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				e, _, v2, field := interlacedDrive(t, s, enhanceConfig{shift: shift, deinterlace: true})
				defer e.close()
				if v2.rowFactor() != 2 {
					t.Fatal("no doubled half recorded")
				}
				n0, _, _, _ := interlacedDrive(t, s, enhanceConfig{shift: shift})
				defer n0.close()
				n1, _, _, _ := interlacedDrive(t, otherParity(s), enhanceConfig{shift: shift})
				defer n1.close()
				stride, height := e.framebufferStride(), e.displayHeight()
				if n0.framebufferStride() != stride || n0.displayHeight() != height {
					t.Fatalf("stride %d height %d, want %d and %d", n0.framebufferStride(), n0.displayHeight(), stride, height)
				}
				got, want0, want1 := e.framebuffer(), n0.framebuffer(), n1.framebuffer()
				lit := 0
				for R := 0; R < height; R++ {
					line := R >> shift
					parity := line & 1
					if driveCPUWriteRows[line/2] {
						continue
					}
					want := want0
					if parity != field {
						want = want1
					}
					row, ref := got[R*stride:(R+1)*stride], want[R*stride:(R+1)*stride]
					if !bytes.Equal(row, ref) {
						t.Fatalf("display row %d (line %d, parity %d) differs at byte %d", R, line, parity, firstDiff(row, ref))
					}
					for x := 0; x < stride; x += 4 {
						if row[x]|row[x+1]|row[x+2] != 0 {
							lit++
						}
					}
				}
				if lit == 0 {
					t.Fatal("no sprite pixel composited")
				}
			})
		}
	}
}

// driveCPUWriteRows are the native rows the drive's CPU frame buffer
// writes land on (vdp1DriveLine). The writes go into one half, so
// the native frames, which display the halves on alternate fields,
// show them on one field's lines alone, while a doubled half holds
// them on both parities.
var driveCPUWriteRows = map[int]bool{30: true, 31: true, 100: true, 101: true}

// TestEnhancedProgressivePairMatchesParities drives every double
// interlace scene through the enhanced software renderer at scale
// shifts 0 and 1 with deinterlacing on, and holds the pair. It grew
// at the first doubled half, and each half is doubled with the scene's
// parity. Doubled row 2r + dil is the walk's physical row r, and
// 2r + 1 - dil is physical row r of the scene walked with the other
// parity, CPU writes included on both, since they land on both
// parities.
func TestEnhancedProgressivePairMatchesParities(t *testing.T) {
	for _, s := range doubledScenes(t) {
		other := otherParity(s)
		for shift := uint32(0); shift <= 1; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				e, v1, v2 := progressiveDrive(t, s, shift)
				defer e.close()
				_, w1, _ := progressiveDrive(t, other, shift)
				if v2.rowFactor() != 2 {
					t.Fatal("no doubled half recorded")
				}
				area := 2 * shift
				if len(e.vdp1FB[0]) != 2*compute.VDP1FBSize<<area {
					t.Fatalf("pair half holds %d bytes", len(e.vdp1FB[0]))
				}
				dil := 0
				if v1.dilOdd() {
					dil = 1
				}
				n := 1 << shift
				rowBytes := v1.fbWidth() * 2
				for h := uint32(0); h < 2; h++ {
					if got := v2.enhance.half[h]; got != (fbHalfLayout{doubled: true, dil: dil}) {
						t.Fatalf("half %d layout %+v", h, got)
					}
					want := expandFB(v1.physicalFB(h), v1.fbWidth(), false, shift)
					otherWant := expandFB(w1.physicalFB(h), w1.fbWidth(), false, shift)
					scaledRow := rowBytes << shift
					for r := 0; r < v1.fbHeight()<<shift; r++ {
						a := (2*(r/n)+dil)*n + r%n
						if got, w := e.vdp1FB[h][a*scaledRow:(a+1)*scaledRow], want[r*scaledRow:(r+1)*scaledRow]; !bytes.Equal(got, w) {
							t.Fatalf("half %d doubled row %d differs from the walk's row %d at byte %d", h, a, r, firstDiff(got, w))
						}
						o := (2*(r/n)+1-dil)*n + r%n
						if got, w := e.vdp1FB[h][o*scaledRow:(o+1)*scaledRow], otherWant[r*scaledRow:(r+1)*scaledRow]; !bytes.Equal(got, w) {
							t.Fatalf("half %d doubled row %d differs from the other parity's row %d at byte %d", h, o, r, firstDiff(got, w))
						}
					}
				}
			})
		}
	}
}

// TestExecEnhancedGPUProgressiveMatchesSoftware drives every double
// interlace scene through the enhanced GPU renderer on the executing
// device and through the enhanced software renderer, both with
// deinterlacing on at scale shifts 0 and 1, and holds the device pair
// and the frame to the software renderer's. The device pair grew at
// the first doubled half, was filled from the CPU pair, and took the
// entries derived for the doubled halves and the CPU writes on both
// parities.
func TestExecEnhancedGPUProgressiveMatchesSoftware(t *testing.T) {
	parent, subs := ComputeSources()
	for _, s := range doubledScenes(t) {
		for shift := uint32(0); shift <= 1; shift++ {
			t.Run(fmt.Sprintf("%s shift %d", s.name, shift), func(t *testing.T) {
				e, _, _ := progressiveDrive(t, s, shift)
				defer e.close()
				v1, v2 := s.build(), progressiveVDP2()
				v2.enhance.cfg.shift = shift
				prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
				if err != nil {
					t.Fatal(err)
				}
				g, err := newEnhancedGPURenderer(v2, v1, prog, gpuBatchLines>>shift)
				if err != nil {
					t.Fatal(err)
				}
				defer g.close()
				d := &vdp1FrameDrive{v1: v1, v2: v2, g: g}
				interlacedFrames(t, d)
				vdp1SubmitChange(t, d, g.gpuRenderer)
				if v2.rowFactor() != 2 {
					t.Fatal("no doubled half recorded")
				}
				for i := range g.vdp1FB {
					got, want := deviceBytes(t, g.gpuRenderer, g.vdp1FB[i]), []byte(e.vdp1FB[i])
					if len(got) != len(want) {
						t.Fatalf("device half %d holds %d bytes, want %d", i, len(got), len(want))
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("physical buffer %d differs from the enhanced software renderer's at byte %d", i, firstDiff(got, want))
					}
				}
				stride, height := e.framebufferStride(), e.displayHeight()
				if g.framebufferStride() != stride || g.displayHeight() != height {
					t.Fatalf("stride %d height %d, want %d and %d", g.framebufferStride(), g.displayHeight(), stride, height)
				}
				equalRenderBytes(t, "frame", g.framebuffer()[:stride*height], e.framebuffer()[:stride*height])
			})
		}
	}
}
