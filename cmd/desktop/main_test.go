// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"slices"
	"testing"

	"github.com/user-none/eblitui/desktop/signing"
)

func TestArtworkKeysParse(t *testing.T) {
	keys, errs := signing.ParseKeys(artworkKeys)
	if len(errs) != 0 {
		t.Errorf("invalid artwork keys: %v", errs)
	}
	if keys.Empty() {
		t.Error("no artwork keys")
	}
}

func TestSplitKeys(t *testing.T) {
	if got := splitKeys(""); got != nil {
		t.Errorf("no keys stamped: %v, want nil", got)
	}
	if got := splitKeys("ed25519:AAA,ed25519:BBB"); !slices.Equal(got, []string{"ed25519:AAA", "ed25519:BBB"}) {
		t.Errorf("two keys: %v", got)
	}
}
