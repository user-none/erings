// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command saturn-ui runs the erings Saturn core under the eblitui
// desktop UI.
package main

import (
	_ "embed"
	"log"
	"strings"

	"github.com/user-none/eblitui/desktop"
	"github.com/user-none/erings"
	"github.com/user-none/erings/adapter"
	"github.com/user-none/erings/core"
	"github.com/user-none/erings/gamedata"
)

//go:embed icon.webp
var icon []byte

// updateKeys holds the update signing public keys as a comma separated list
// of algorithm:base64 entries. It is empty in the source and only set at link
// time by the release workflow with -ldflags "-X main.updateKeys=...".
var updateKeys string

// artworkKeys holds the public keys, as algorithm:base64 entries, trusted to
// sign the artwork index. Unlike updateKeys they are part of the source, so
// every build, including go run, verifies artwork the same way.
var artworkKeys = []string{
	"ed25519:ny8kTzx/RE+DHbsX+a280g7MgC2+vhhebwPr4x32/Ck=",
}

// splitKeys returns the keys in a comma separated list, or nil when the
// list is empty
func splitKeys(list string) []string {
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}

func main() {
	core.WarmGPU()
	factory := &adapter.Factory{}

	emu := desktop.Emulator{
		Name:                       erings.Name,
		Version:                    erings.Version,
		DataDirName:                "erings",
		Icon:                       icon,
		RetroAchievementsConsoleID: 39,
		CheatFormat:                "saturn",
		// Release builds have the update signing keys stamped in; other
		// builds have none and the update system stays off
		Update: &desktop.UpdateConfig{
			ManifestURL:    "https://github.com/user-none/erings/releases/latest/download/manifest.json",
			SignatureURL:   "https://github.com/user-none/erings/releases/latest/download/manifest.sig",
			ReleasePageURL: "https://github.com/user-none/erings/releases/tag/v{version}",
			Keys:           splitKeys(updateKeys),
		},
		Artwork: &desktop.ArtworkConfig{
			URL:  "https://artwork.erings.dev/",
			Keys: artworkKeys,
		},
		Metadata: gamedata.MetadataSource{},
		Cheats:   gamedata.CheatsSource{},
		Rumble:   gamedata.RumbleSource{},
	}

	if err := desktop.Run(factory, emu); err != nil {
		log.Fatal(err)
	}
}
