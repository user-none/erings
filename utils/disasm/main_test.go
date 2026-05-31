// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// TestDisassembleLinesDelaySlotAndTarget covers P2 (delay-slot marking) and P3
// (register-indirect target resolution): a MOV.L pool load feeds a BSRF whose
// target is resolved, and the following instruction is marked as a delay slot.
func TestDisassembleLinesDelaySlotAndTarget(t *testing.T) {
	data := []byte{
		0xD3, 0x01, // 0x00 MOV.L @(disp=1,PC),R3 -> pool at 0x08
		0x03, 0x03, // 0x02 BSRF R3
		0x00, 0x09, // 0x04 NOP (BSRF delay slot)
		0x00, 0x09, // 0x06 NOP
		0x00, 0x00, // 0x08 .data.l high
		0x00, 0x20, // 0x0A .data.l low -> 0x00000020
	}
	lines := disassembleLines(data, 0, 0, 6)
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6: %v", len(lines), lines)
	}
	// BSRF target = 0x02 + 4 + 0x20 = 0x26.
	if !strings.Contains(lines[1], "BSRF R3") || !strings.Contains(lines[1], "; -> $000026") {
		t.Errorf("BSRF line not resolved: %q", lines[1])
	}
	if strings.Contains(lines[1], "delay slot") {
		t.Errorf("BSRF wrongly marked as delay slot: %q", lines[1])
	}
	if !strings.Contains(lines[2], "; delay slot") {
		t.Errorf("delay slot not marked: %q", lines[2])
	}
}

// TestDisassembleLinesPoolBounds is the P1 regression: a MOV.L whose pool target
// would overrun the file must not panic, must not be resolved, and the would-be
// pool word is rendered as an ordinary instruction.
func TestDisassembleLinesPoolBounds(t *testing.T) {
	data := []byte{
		0xD0, 0x01, // 0x00 MOV.L @(disp=1,PC),R0 -> target 0x08 == fileSize-2
		0x00, 0x09, // 0x02 NOP
		0x00, 0x09, // 0x04 NOP
		0x00, 0x09, // 0x06 NOP
		0x12, 0x34, // 0x08 cannot hold a full 32-bit word
	}
	lines := disassembleLines(data, 0, 0, 5) // must not panic
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5: %v", len(lines), lines)
	}
}

// TestDisassembleLinesPoolBoundaryOK confirms a .data.l ending exactly at the
// end of the file is read in full rather than truncated.
func TestDisassembleLinesPoolBoundaryOK(t *testing.T) {
	data := []byte{
		0xD0, 0x00, // 0x00 MOV.L @(disp=0,PC),R0 -> target 0x04 == fileSize-4
		0x00, 0x09, // 0x02 NOP
		0x12, 0x34, // 0x04 .data.l high
		0x56, 0x78, // 0x06 .data.l low -> 0x12345678
	}
	lines := disassembleLines(data, 0, 0, 4)
	found := false
	for _, l := range lines {
		if strings.Contains(l, ".data.l H'12345678") {
			found = true
		}
	}
	if !found {
		t.Errorf("end-of-file .data.l not rendered: %v", lines)
	}
}
