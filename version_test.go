// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package erings

import (
	"testing"

	"github.com/user-none/eblitui/desktop/signing"
)

func TestArtworkKeysParse(t *testing.T) {
	keys, errs := signing.ParseKeys(ArtworkKeys())
	if len(errs) != 0 {
		t.Errorf("invalid artwork keys: %v", errs)
	}
	if keys.Empty() {
		t.Error("no artwork keys")
	}
	k := ArtworkKeys()
	k[0] = "changed"
	if ArtworkKeys()[0] == "changed" {
		t.Error("artwork keys changed through a returned slice")
	}
}
