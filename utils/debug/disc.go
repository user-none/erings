// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"strings"

	"github.com/user-none/eblitui/romloader"
)

// readGameID reads the 10-byte product number from the disc's IP
// header (offset $20 in the IP user data) and returns it with
// trailing spaces trimmed. Returns "" if the disc is nil, unreadable,
// or doesn't have a Saturn IP header.
func readGameID(disc *romloader.Disc) string {
	if disc == nil {
		return ""
	}
	data, err := disc.ReadSector(0)
	if err != nil || len(data) < 16+0x2A {
		return ""
	}
	user := data[16:]
	if string(user[0:16]) != "SEGA SEGASATURN " {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(user[0x20:0x2A]), " "))
}

// printDiscInfo reads the IP System ID from sector 0 and prints the
// game title, product number, and device-information field in
// "<NAME> | <ID> | <DEVICE>" form. Per Disc Format Standards (ST-040)
// section 4.2, the IP header lives in the first 2048 bytes of user data:
// hardware identifier at 0x00, product number at 0x20 (10 bytes), device
// information at 0x38 (8 bytes, "CD-n/m"), game title at 0x60 (112
// bytes). Empty positions are filled with ASCII spaces. Raw 2352-byte
// sectors place user data at byte 16. Silent on read failure or
// non-Saturn discs.
func printDiscInfo(disc *romloader.Disc) {
	data, err := disc.ReadSector(0)
	if err != nil || len(data) < 16+0xD0 {
		return
	}
	user := data[16:]
	if string(user[0:16]) != "SEGA SEGASATURN " {
		return
	}
	id := strings.TrimRight(string(user[0x20:0x2A]), " ")
	name := strings.TrimRight(string(user[0x60:0xD0]), " ")
	device := strings.TrimSpace(string(user[0x38:0x40]))
	fmt.Printf("%s | %s | %s\n", name, id, device)
}
