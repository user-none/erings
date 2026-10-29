// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/user-none/erings/internal/hostgpu"
)

// TestSetDeinterlace checks the deinterlace option's selection:
// deinterlacing on at 1x selects the enhanced software renderer at
// native scale with the walk keeping the CPU pair, the renderer name
// and info unchanged since the scale is still 1; off returns to the
// accurate renderer; above 1x the enhanced renderer stays whichever
// way the switch goes; a request for the active state does not reopen
// the renderer; and the VDP2 carries the configuration the renderer
// was opened with.
func TestSetDeinterlace(t *testing.T) {
	e := NewEmulator()
	defer e.Close()
	if err := e.SetDeinterlace(false); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.renderer.(*softwareRenderer); !ok {
		t.Fatalf("renderer after a request for the active state is %T", e.renderer)
	}
	if err := e.SetDeinterlace(true); err != nil {
		t.Fatal(err)
	}
	r, ok := e.renderer.(*enhancedRenderer)
	if !ok {
		t.Fatalf("renderer with deinterlacing on at 1x is %T", e.renderer)
	}
	if r.shift != 0 {
		t.Fatalf("shift %d", r.shift)
	}
	if !e.vdp1.pairKept.Load() {
		t.Fatal("the walk does not keep the CPU pair")
	}
	if got := e.RendererName(); got != "Software" {
		t.Fatalf("renderer name %q", got)
	}
	if got := e.RenderInfo(); got != (RenderInfo{Backend: "Software", Scale: 1}) {
		t.Fatalf("render info %+v", got)
	}
	if !e.vdp2.enhance.cfg.deinterlace || e.vdp2.enhance.cfg.shift != 0 {
		t.Fatalf("VDP2 configuration %+v", e.vdp2.enhance.cfg)
	}
	if err := e.SetDeinterlace(true); err != nil {
		t.Fatal(err)
	}
	if e.renderer != r {
		t.Fatal("a request for the active state reopened the renderer")
	}
	if err := e.SetDeinterlace(false); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.renderer.(*softwareRenderer); !ok {
		t.Fatalf("renderer with deinterlacing off at 1x is %T", e.renderer)
	}
	if e.vdp2.enhance.cfg.deinterlace {
		t.Fatal("VDP2 configuration still deinterlacing")
	}

	e.SetOption("upscale", "2x")
	e.SetOption("deinterlace", "true")
	r, ok = e.renderer.(*enhancedRenderer)
	if !ok || r.shift != 1 {
		t.Fatalf("renderer at 2x with deinterlacing on is %T", e.renderer)
	}
	if !e.vdp2.enhance.cfg.deinterlace || e.vdp2.enhance.cfg.shift != 1 {
		t.Fatalf("VDP2 configuration at 2x with deinterlacing on %+v", e.vdp2.enhance.cfg)
	}
	e.SetOption("deinterlace", "false")
	if r, ok = e.renderer.(*enhancedRenderer); !ok || r.shift != 1 {
		t.Fatalf("renderer at 2x with deinterlacing off is %T", e.renderer)
	}
	e.SetOption("deinterlace", "false")
	if e.renderer != r {
		t.Fatal("a request for the active state through the option reopened the renderer")
	}
	e.SetOption("upscale", "1x")
	if _, ok := e.renderer.(*softwareRenderer); !ok {
		t.Fatalf("renderer at 1x with every enhancement off is %T", e.renderer)
	}
}

// TestExecEnhancedGPUScale1MatchesGPU drives every VDP1 frame scene
// and every enhanced VDP1 scene through the accurate GPU renderer and
// through the enhanced GPU renderer at scale 1 with deinterlacing on
// under a non-interlaced display, on the executing device, each over
// its own VDP1, and holds the enhanced renderer's device pair and
// frame equal to the accurate renderer's: with no doubled half the
// enhanced path, which clears its pair at open, fills it from the CPU
// pair at its first take, and draws the walk's entries into it,
// reproduces the accurate renderer, which copies the pair at open and
// draws it in the walk's place, the double interlace scenes included,
// whose DIE under the non-interlaced display keeps their halves
// native.
func TestExecEnhancedGPUScale1MatchesGPU(t *testing.T) {
	parent, subs := ComputeSources()
	scenes := vdp1FrameScenes()
	for _, s := range enhancedVDP1Scenes(t) {
		scenes = append(scenes, s.benchVDP1Scene)
	}
	for _, s := range scenes {
		t.Run(s.name, func(t *testing.T) {
			open := func() (*hostgpu.Program, *VDP1, *VDP2) {
				prog, err := hostgpu.Build(&execDevice{}, parent, subs...)
				if err != nil {
					t.Fatal(err)
				}
				return prog, s.build(), spriteVDP2()
			}
			prog, v1, v2 := open()
			g, err := newGPURenderer(v2, v1, prog, gpuBatchLines, false)
			if err != nil {
				t.Fatal(err)
			}
			defer g.close()
			v1.setPairKept(false)
			ref := &vdp1FrameDrive{v1: v1, v2: v2, g: g}
			frames := ref.frames(t)
			vdp1SubmitChange(t, ref, g)

			prog, w1, w2 := open()
			w2.setEnhance(enhanceConfig{deinterlace: true})
			en, err := newEnhancedGPURenderer(w2, w1, prog, gpuBatchLines)
			if err != nil {
				t.Fatal(err)
			}
			defer en.close()
			d := &vdp1FrameDrive{v1: w1, v2: w2, g: en}
			for frame := 0; frame < frames; frame++ {
				d.frame(frame)
			}
			vdp1SubmitChange(t, d, en.gpuRenderer)
			for i := range en.vdp1FB {
				got, want := deviceBytes(t, en.gpuRenderer, en.vdp1FB[i]), deviceBytes(t, g, g.vdp1FB[i])
				if !bytes.Equal(got, want) {
					t.Fatalf("physical buffer %d differs from the accurate renderer's at byte %d", i, firstDiff(got, want))
				}
			}
			stride, height := g.framebufferStride(), g.displayHeight()
			if en.framebufferStride() != stride || en.displayHeight() != height {
				t.Fatalf("stride %d height %d, want %d and %d", en.framebufferStride(), en.displayHeight(), stride, height)
			}
			equalRenderBytes(t, fmt.Sprintf("%s frame", s.name), en.framebuffer()[:stride*height], g.framebuffer()[:stride*height])
		})
	}
}
