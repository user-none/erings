// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package adapter

import (
	"testing"

	"github.com/user-none/eblitui/coreif"
)

// TestDeinterlaceOption checks the declared deinterlace option: a
// per-game video switch, off by default, which the adapter passes
// through to the core without changing the reported scale.
func TestDeinterlaceOption(t *testing.T) {
	var opt *coreif.CoreOption
	opts := (&Factory{}).SystemInfo().CoreOptions
	for i := range opts {
		if opts[i].Key == "deinterlace" {
			opt = &opts[i]
		}
	}
	if opt == nil {
		t.Fatal("deinterlace option not declared")
	}
	if opt.Type != coreif.CoreOptionBool {
		t.Fatalf("type %d", opt.Type)
	}
	if opt.Category != coreif.CoreOptionCategoryVideo {
		t.Fatalf("category %d", opt.Category)
	}
	if !opt.PerGame {
		t.Fatal("not per game")
	}
	if opt.Default != "false" {
		t.Fatalf("default %q", opt.Default)
	}

	e := (&Factory{}).CreateEmulator()
	defer e.Close()
	p := e.(coreif.RenderInfoProvider)
	e.SetOption("deinterlace", "true")
	if got := p.RenderInfo(); got != (coreif.RenderInfo{Backend: "Software", Scale: 1}) {
		t.Fatalf("render info with deinterlacing on %+v", got)
	}
	e.SetOption("deinterlace", "false")
	if got := p.RenderInfo(); got != (coreif.RenderInfo{Backend: "Software", Scale: 1}) {
		t.Fatalf("render info with deinterlacing off %+v", got)
	}
}
