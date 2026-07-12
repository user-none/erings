// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/user-none/erings/internal/statedump"
)

// writeStateDump explodes a serialized save state into a timestamped
// subdirectory of baseDir: the state file itself, plus the header.txt
// and per-field binaries statedump.Explode produces. Millisecond
// resolution in the directory name lets multiple dumps within the same
// second coexist (one Saturn frame is ~16.7 ms at 60 fps).
func writeStateDump(state []byte, baseDir string) error {
	stamp := strings.Replace(time.Now().Format("20060102-150405.000"), ".", "-", 1)
	dir := filepath.Join(baseDir, "dump-"+stamp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, "save.state"), state, 0o644); err != nil {
		return err
	}

	chunks, fields, err := statedump.Explode(state, dir)
	if err != nil {
		return err
	}

	fmt.Printf("[DUMP] wrote save.state + %d chunks (%d fields) to %s\n",
		chunks, fields, dir)
	return nil
}
