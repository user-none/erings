// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package gamedata holds per-title data embedded into erings.
package gamedata

import "embed"

// Metadata holds one JSON file per title under metadata/, named by the
// product number read from the disc's IP header, for example
// metadata/GS-9001.json.
//
//go:embed metadata/*.json
var Metadata embed.FS

// Cheats holds one JSON file per title under cheats/, named by the same
// product number as the title's metadata file, for example
// cheats/GS-9001.json. Each file has the title's serial, the Action
// Replay master code when one is known, and the cheats as name and code
// pairs, one address and value pair per code line.
//
//go:embed cheats/*.json
var Cheats embed.FS

// Rumbles holds the .erumble rumble files under rumbles/, named by the same
// product number as the title's metadata file, for example
// rumbles/T-14310G.erumble. A file for one disc of a multi-disc title adds
// the disc number, for example rumbles/T-1219H.disc2.erumble.
//
//go:embed rumbles/*.erumble
var Rumbles embed.FS
