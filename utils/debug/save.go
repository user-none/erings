// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/user-none/eblitui/romloader"
	"github.com/user-none/erings/core"
)

// resolveSavePath turns the user's -save argument into the actual file
// path the emulator should read from on start and write to on close.
// If the argument is empty, returns "" (save handling disabled). If
// the argument names an existing directory, the file inside it is
// <gameid>.srm — gameid extracted from the disc's IP header at user-
// offset $20 (10 ASCII bytes per Disc Format Standards ST-040). If
// the argument names anything else (an existing file or a path that
// doesn't yet exist), it is used verbatim.
func resolveSavePath(arg string, disc *romloader.Disc) string {
	if arg == "" {
		return ""
	}
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		id := readGameID(disc)
		if id == "" {
			log.Fatalf("-save points at a directory but the disc has no readable game ID")
		}
		return filepath.Join(arg, id+".srm")
	}
	return arg
}

// loadSaveFile attempts to read the file at path and feed it to the
// emulator via SetSRAM. A missing file is fine — the path is still
// remembered so writeSaveFile can create it on close. Any other read
// error or unexpected file size is logged and skipped (without
// SetSRAM the emulator starts with a freshly-formatted backup).
func loadSaveFile(emu *core.Emulator, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("Warning: failed to read save file %q: %v", path, err)
		return
	}
	emu.SetSRAM(data)
}

// writeSaveFile writes the emulator's current backup RAM to the
// given path. Logs and continues on error so close-time failures
// don't mask other shutdown work.
func writeSaveFile(emu *core.Emulator, path string) {
	if err := os.WriteFile(path, emu.GetSRAM(), 0644); err != nil {
		log.Printf("Warning: failed to write save file %q: %v", path, err)
	}
}
