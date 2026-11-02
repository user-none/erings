// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package gamedata

import (
	"encoding/json"
	"io/fs"
	"strings"

	"github.com/user-none/eblitui/desktop/content"
)

// metadataDir and cheatsDir are the directories in Metadata and Cheats
// holding one <product number>.json file per title. rumblesDir is the
// directory in Rumbles holding the .erumble files.
const (
	metadataDir = "metadata"
	cheatsDir   = "cheats"
	rumblesDir  = "rumbles"
)

// MetadataSource supplies the embedded game metadata to the desktop UI.
type MetadataSource struct{}

// GameMetadata decodes the embedded entry named by the product number,
// returning false when there is no entry or it cannot be decoded.
func (MetadataSource) GameMetadata(serial string) (content.GameMetadata, bool) {
	data, err := fs.ReadFile(Metadata, metadataDir+"/"+serial+".json")
	if err != nil {
		return content.GameMetadata{}, false
	}
	var m content.GameMetadata
	if err := json.Unmarshal(data, &m); err != nil {
		return content.GameMetadata{}, false
	}
	return m, true
}

// AllGameMetadata decodes every embedded entry into a new map keyed by
// product number, skipping any entry that cannot be decoded.
func (s MetadataSource) AllGameMetadata() map[string]content.GameMetadata {
	entries, err := fs.ReadDir(Metadata, metadataDir)
	if err != nil {
		return map[string]content.GameMetadata{}
	}
	all := make(map[string]content.GameMetadata, len(entries))
	for _, e := range entries {
		serial, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		if m, ok := s.GameMetadata(serial); ok {
			all[serial] = m
		}
	}
	return all
}

// CheatsSource supplies the embedded cheats to the desktop UI.
type CheatsSource struct{}

// GameCheats decodes the embedded entry named by the product number,
// returning false when there is no entry or it cannot be decoded.
func (CheatsSource) GameCheats(serial string) (content.GameCheats, bool) {
	data, err := fs.ReadFile(Cheats, cheatsDir+"/"+serial+".json")
	if err != nil {
		return content.GameCheats{}, false
	}
	var c content.GameCheats
	if err := json.Unmarshal(data, &c); err != nil {
		return content.GameCheats{}, false
	}
	return c, true
}

// RumbleSource supplies the embedded rumble files to the desktop UI.
type RumbleSource struct{}

// GameRumble returns the embedded rumble file named by a product number,
// or a product number with a disc suffix such as T-1219H.disc2, returning
// false when there is none.
func (RumbleSource) GameRumble(name string) ([]byte, bool) {
	data, err := fs.ReadFile(Rumbles, rumblesDir+"/"+name+".erumble")
	if err != nil {
		return nil, false
	}
	return data, true
}
