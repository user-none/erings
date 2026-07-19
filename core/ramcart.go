// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// Override default 4MB expanded memory cart
var ramCartOverrides = map[string]uint8{
	"T-16103H": ramCartIDNone, // Die Hard Trilogy: Fails to run with a memory cart inserted
}

// ramCartIDForProduct returns the cartridge ID byte for a disc's product number
// Otherwise returns 4MB as the default memory cart present
func ramCartIDForProduct(product string) uint8 {
	if id, ok := ramCartOverrides[product]; ok {
		return id
	}
	return ramCartID4MB
}
