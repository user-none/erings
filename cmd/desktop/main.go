// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command saturn-ui runs the erings Saturn core under the eblitui
// desktop UI. With no flags it opens the full UI. Passing both -bios
// and -disc runs the given disc directly with the given BIOS.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/user-none/eblitui/desktop"
	"github.com/user-none/erings/adapter"
)

func main() {
	biosPath := flag.String("bios", "", "path to BIOS file (direct run; requires -disc)")
	discPath := flag.String("disc", "", "path to disc file (direct run; requires -bios)")
	flag.Parse()

	factory := &adapter.Factory{}

	if *biosPath != "" || *discPath != "" {
		if *biosPath == "" || *discPath == "" {
			log.Fatal("-bios and -disc must both be specified for direct run")
		}

		biosData, err := os.ReadFile(*biosPath)
		if err != nil {
			log.Fatalf("failed to read BIOS: %v", err)
		}

		bios := map[string][]byte{"main_bios": biosData}
		if err := desktop.RunDirect(factory, *discPath, nil, bios); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := desktop.Run(factory); err != nil {
		log.Fatal(err)
	}
}
