// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package adapter

import (
	"slices"
	"testing"

	"github.com/user-none/eblitui/coreif"
	"github.com/user-none/erings/core"
)

// TestRendererOption checks the declared renderer option: a video
// select over the core's renderer names, not per game, whose default
// is GPU.
func TestRendererOption(t *testing.T) {
	var opt *coreif.CoreOption
	opts := (&Factory{}).SystemInfo().CoreOptions
	for i := range opts {
		if opts[i].Key == "renderer" {
			opt = &opts[i]
		}
	}
	if opt == nil {
		t.Fatal("renderer option not declared")
	}
	if opt.Type != coreif.CoreOptionSelect {
		t.Fatalf("type %d", opt.Type)
	}
	if opt.Category != coreif.CoreOptionCategoryVideo {
		t.Fatalf("category %d", opt.Category)
	}
	if opt.PerGame {
		t.Fatal("per game")
	}
	if !slices.Equal(opt.Values, core.RendererNames()) {
		t.Fatalf("values %q", opt.Values)
	}
	if opt.Default != "GPU" {
		t.Fatalf("default %q", opt.Default)
	}
}

// TestRendererOptionSoftware checks that the Software value reaches
// the core: after Start the session renders in software at the
// upscale option's scale, with no GPU failure since no GPU renderer
// was opened.
func TestRendererOptionSoftware(t *testing.T) {
	emu := (&Factory{}).CreateEmulator()
	defer emu.Close()
	emu.SetOption("upscale", "2x")
	emu.SetOption("renderer", "Software")
	if err := emu.SetBIOS("main_bios", make([]byte, 512*1024)); err != nil {
		t.Fatal(err)
	}
	emu.Start()
	p := emu.(coreif.RenderInfoProvider)
	if got := p.RenderInfo(); got != (coreif.RenderInfo{Backend: "Software", Scale: 2}) {
		t.Fatalf("render info %+v", got)
	}
	if err := emu.(*emulator).emu.GPUError(); err != nil {
		t.Fatalf("software renderer with a GPU error: %v", err)
	}
}
