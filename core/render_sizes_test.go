// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"testing"

	"github.com/user-none/erings/core/compute"
)

// TestBufferSizes holds every size bufferSizes gives to its form: the
// dynamic buffers a power of two, at least what the geometry needs and
// less than twice it, and the fixed ones at their formulas, at every
// scale, slot count, display, VDP1 frame buffer mode, and pair row
// factor, which doubles the pair, the stamps, and the heads alone.
func TestBufferSizes(t *testing.T) {
	pow2At := func(name string, got, need int) {
		t.Helper()
		if got <= 0 || got&(got-1) != 0 || got < need || (need > 1 && got >= 2*need) {
			t.Fatalf("%s %d for a need of %d", name, got, need)
		}
	}
	for shift := uint32(0); shift <= 3; shift++ {
		for _, slots := range []int{0, 1, 2, 8, 2 * gpuBatchLines, 2 * rowRecordMax << 3} {
			for _, width := range []int{0, 320, 352, 640, 704} {
				for _, height := range []int{0, 224, 240, 256, 448, 480, 512} {
					for _, px := range []int{0, 512 * 256, 1024 * 256, 512 * 512} {
						for rowFactor := 1; rowFactor <= 2; rowFactor++ {
							s := bufferSizes(shift, slots, width, height, px, rowFactor)
							rows := slots * (width << shift)
							pow2At("layer words", s.layerWords, rows)
							pow2At("line color bytes", s.lineColorBytes, rows)
							pow2At("frame buffer words", s.framebufferWords, (width<<shift)*(height<<shift))
							pow2At("stamps", s.stamps, px*rowFactor<<(2*shift))
							if s.rowRecords != slots || s.copyRows != 2*(slots>>shift) {
								t.Fatalf("shift %d slots %d: row records %d copy rows %d", shift, slots, s.rowRecords, s.copyRows)
							}
							if s.pairBytes != compute.VDP1FBSize*rowFactor<<(2*shift) || s.heads != compute.VDP1FBSize/2*rowFactor<<(2*shift) || s.pool != gpuFragmentPoolWords {
								t.Fatalf("shift %d factor %d: pair %d heads %d pool %d", shift, rowFactor, s.pairBytes, s.heads, s.pool)
							}
						}
					}
				}
			}
		}
	}
}

// TestStateLoadIntoLargerModeRendersWhole loads a state saved in the
// 704-dot hi-res mode into an emulator whose renderer was sized by a
// 320 by 224 frame, and holds the loaded emulator's first frame to the
// frame the saving emulator renders next: the frame start grows the
// buffers before any row renders. The back screen takes a color per
// displayed line, so every row of the frame differs. The mode is not
// interlaced, since a double-density frame writes one field's rows and
// the other field's come from a frame no state carries.
func TestStateLoadIntoLargerModeRendersWhole(t *testing.T) {
	scene := func(f *walkFixture) {
		f.code(walkCode, wopBRASelf, wopNOP)
		v := f.e.vdp2
		for i := uint32(0); i < 512; i++ {
			v.WriteVRAM16(i*2, uint16(i*37)|0x8000)
		}
		v.Write(vdp2BKTAU*2, 0x8000)
		v.Write(0, 0x8000)
	}
	frame := func(e *Emulator) []byte {
		n := e.GetFramebufferStride() * e.GetActiveHeight()
		return append([]byte(nil), e.GetFramebuffer()[:n]...)
	}

	saver := newWalkFixture(t)
	scene(saver)
	saver.runFrames(1)
	saver.e.vdp2.Write(0, 0x8003)
	saver.runFrames(2)
	state, err := saver.e.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	saver.runFrames(1)
	want := frame(saver.e)
	wantStride := saver.e.GetFramebufferStride()

	loader := newWalkFixture(t)
	scene(loader)
	loader.runFrames(1)
	if n := len(loader.e.GetFramebuffer()); n >= len(want) {
		t.Fatalf("the 320 by 224 frame buffer holds %d bytes, not less than the %d of the larger mode", n, len(want))
	}
	if err := loader.e.Deserialize(state); err != nil {
		t.Fatal(err)
	}
	loader.runFrames(1)
	if stride := loader.e.GetFramebufferStride(); stride != wantStride {
		t.Fatalf("stride %d, want %d", stride, wantStride)
	}
	if got := frame(loader.e); !bytes.Equal(got, want) {
		equalRenderBytes(t, "loaded frame", got, want)
	}
}
