// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package gamedata

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/user-none/eblitui/desktop/content"
	"github.com/user-none/eblitui/rumble"
)

func TestGameMetadataKnownSerial(t *testing.T) {
	m, ok := MetadataSource{}.GameMetadata("GS-9001")
	if !ok {
		t.Fatal("GS-9001 not found")
	}
	if m.Serial != "GS-9001" || m.Title != "Virtua Fighter" {
		t.Fatalf("got serial %q title %q", m.Serial, m.Title)
	}
}

func TestGameMetadataMissingOrInvalid(t *testing.T) {
	for _, serial := range []string{"", "NO-SUCH-SERIAL", "../gamedata", "metadata/GS-9001", "GS-9001/../GS-9001"} {
		if m, ok := (MetadataSource{}).GameMetadata(serial); ok {
			t.Errorf("serial %q: got entry %q, want none", serial, m.Serial)
		}
		if c, ok := (CheatsSource{}).GameCheats(serial); ok {
			t.Errorf("serial %q: got cheats for %q, want none", serial, c.Serial)
		}
	}
}

func TestAllGameMetadata(t *testing.T) {
	src := MetadataSource{}
	entries, err := fs.ReadDir(Metadata, metadataDir)
	if err != nil {
		t.Fatal(err)
	}
	all := src.AllGameMetadata()
	if len(all) != len(entries) {
		t.Fatalf("AllGameMetadata has %d entries, embedded files %d", len(all), len(entries))
	}
	for serial, m := range all {
		if m.Serial != serial {
			t.Errorf("key %q holds serial %q", serial, m.Serial)
		}
		if m.Title == "" || m.Category == "" || len(m.Regions) == 0 {
			t.Errorf("%s: title, category or regions empty", serial)
		}
		one, ok := src.GameMetadata(serial)
		if !ok || !reflect.DeepEqual(one, m) {
			t.Errorf("%s: GameMetadata differs from AllGameMetadata", serial)
		}
	}
}

func TestAllGameMetadataReturnsNewMap(t *testing.T) {
	a := MetadataSource{}.AllGameMetadata()
	delete(a, "GS-9001")
	if _, ok := (MetadataSource{}).AllGameMetadata()["GS-9001"]; !ok {
		t.Fatal("deleting from a returned map changed a later result")
	}
}

// decodeStrict decodes data into v, rejecting fields v does not have
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func TestEmbeddedMetadataFields(t *testing.T) {
	entries, err := fs.ReadDir(Metadata, metadataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := fs.ReadFile(Metadata, metadataDir+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var m content.GameMetadata
		if err := decodeStrict(data, &m); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if m.Serial+".json" != e.Name() {
			t.Errorf("%s: serial %q does not match file name", e.Name(), m.Serial)
		}
		if strings.TrimSpace(m.Title) != m.Title {
			t.Errorf("%s: title has surrounding whitespace", e.Name())
		}
	}
}

func TestEmbeddedCheats(t *testing.T) {
	entries, err := fs.ReadDir(Cheats, cheatsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded cheats")
	}
	for _, e := range entries {
		data, err := fs.ReadFile(Cheats, cheatsDir+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var c content.GameCheats
		if err := decodeStrict(data, &c); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		serial := strings.TrimSuffix(e.Name(), ".json")
		if c.Serial != serial {
			t.Errorf("%s: serial %q does not match file name", e.Name(), c.Serial)
		}
		if len(c.Cheats) == 0 {
			t.Errorf("%s: no cheats", e.Name())
		}
		if got, ok := (CheatsSource{}).GameCheats(serial); !ok || !reflect.DeepEqual(got, c) {
			t.Errorf("%s: GameCheats differs from the file", e.Name())
		}
		if _, ok := (MetadataSource{}).GameMetadata(serial); !ok {
			t.Errorf("%s: no metadata for the cheats' serial", e.Name())
		}
	}
}

// discSuffix matches the disc suffix of a disc-specific rumble file name
var discSuffix = regexp.MustCompile(`\.disc[1-9][0-9]*$`)

func TestEmbeddedRumbles(t *testing.T) {
	entries, err := fs.ReadDir(Rumbles, rumblesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded rumble files")
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".erumble")
		if !ok {
			t.Errorf("%s: not an .erumble file", e.Name())
			continue
		}
		data, err := fs.ReadFile(Rumbles, rumblesDir+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rumble.Parse(data); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
		if got, ok := (RumbleSource{}).GameRumble(name); !ok || !bytes.Equal(got, data) {
			t.Errorf("%s: GameRumble differs from the file", e.Name())
		}
		serial := discSuffix.ReplaceAllString(name, "")
		if _, ok := (MetadataSource{}).GameMetadata(serial); !ok {
			t.Errorf("%s: no metadata for serial %q", e.Name(), serial)
		}
	}
}

func TestGameRumbleMissingOrInvalid(t *testing.T) {
	for _, name := range []string{"", "NO-SUCH-SERIAL", "../rumbles/T-14310G", "rumbles/T-14310G", "T-14310G.erumble", "T-14310G/../T-14310G"} {
		if _, ok := (RumbleSource{}).GameRumble(name); ok {
			t.Errorf("name %q: got a file, want none", name)
		}
	}
	if _, ok := (RumbleSource{}).GameRumble("T-14310G"); !ok {
		t.Error("T-14310G: no rumble file")
	}
}
