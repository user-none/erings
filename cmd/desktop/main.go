// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command saturn-ui runs the erings Saturn core under the eblitui
// desktop UI.
package main

import (
	_ "embed"
	"log"

	"github.com/user-none/eblitui/desktop"
	"github.com/user-none/erings"
	"github.com/user-none/erings/adapter"
	"github.com/user-none/erings/core"
	"github.com/user-none/erings/gamedata"
)

//go:embed icon.webp
var icon []byte

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
			AppName:        erings.Name,
			Version:        erings.Version,
			Keys:           erings.UpdateKeys(),
		},
		Artwork: &desktop.ArtworkConfig{
			URL:  "https://artwork.erings.dev/",
			Keys: erings.ArtworkKeys(),
		},
		Metadata: gamedata.MetadataSource{},
		Cheats:   gamedata.CheatsSource{},
		Rumble:   gamedata.RumbleSource{},
	}

	if err := desktop.Run(factory, emu); err != nil {
		log.Fatal(err)
	}
}
