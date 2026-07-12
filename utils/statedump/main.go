// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command statedump explodes a save state file into a directory of
// per-field binaries without running the emulator: a header.txt with
// the decoded header and one <field>.bin per chunk field under a
// subdirectory named after the chunk tag. It exists to inspect states
// produced by other frontends; the debug tool's in-session dump key
// produces the same layout.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/user-none/erings/internal/statedump"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "Usage: statedump <state-file> <output-dir>")
		os.Exit(1)
	}
	state, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatalf("read state: %v", err)
	}
	if err := os.MkdirAll(os.Args[2], 0o755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}
	chunks, fields, err := statedump.Explode(state, os.Args[2])
	if err != nil {
		log.Fatalf("explode: %v", err)
	}
	fmt.Printf("[STATEDUMP] wrote %d chunks (%d fields) to %s\n", chunks, fields, os.Args[2])
}
