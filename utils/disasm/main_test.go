// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
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
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 6); err != nil {
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
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

// TestDisassembleLinesSlotMOVL covers PC-relative MOV.L in a delay slot: the
// base is the branch destination + 2, not the slot instruction's own address.
// The pool marker must follow the corrected target.
func TestDisassembleLinesSlotMOVL(t *testing.T) {
	data := []byte{
		0xA0, 0x02, // 0x00 BRA 0x08 (0x00 + 4 + 2*2)
		0xD3, 0x01, // 0x02 MOV.L @(1,PC),R3 delay slot -> ((0x08+2)&~3)+4 = 0x0C
		0x00, 0x09, // 0x04 NOP
		0x00, 0x09, // 0x06 NOP
		0x00, 0x09, // 0x08 NOP (branch target)
		0x00, 0x09, // 0x0A NOP
		0x12, 0x34, // 0x0C .data.l high
		0x56, 0x78, // 0x0E .data.l low -> 0x12345678
	}
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 8); err != nil {
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("got %d lines, want 8: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "MOV.L @(H'0000000C),R3") || !strings.Contains(lines[1], "; delay slot") {
		t.Errorf("slot MOV.L not resolved against branch target: %q", lines[1])
	}
	// Straight-line (wrong) target 0x08 must stay an instruction, and the
	// corrected target 0x0C must be rendered as pool data.
	if !strings.Contains(lines[4], "NOP") {
		t.Errorf("branch target wrongly consumed as pool data: %q", lines[4])
	}
	if !strings.Contains(lines[6], ".data.l H'12345678") {
		t.Errorf("corrected pool word not rendered: %q", lines[6])
	}
}

// TestDisassembleLinesSlotMOVW covers PC-relative MOV.W in the slot of a
// delayed conditional branch (BF/S).
func TestDisassembleLinesSlotMOVW(t *testing.T) {
	data := []byte{
		0x8F, 0x03, // 0x00 BF/S 0x0A (0x00 + 4 + 3*2)
		0x93, 0x01, // 0x02 MOV.W @(1,PC),R3 delay slot -> (0x0A+2)+2 = 0x0E
		0x00, 0x09, // 0x04 NOP
		0x00, 0x09, // 0x06 NOP
		0x00, 0x09, // 0x08 NOP
		0x00, 0x09, // 0x0A NOP (branch target)
		0x00, 0x09, // 0x0C NOP
		0x80, 0x42, // 0x0E .data.w
	}
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 8); err != nil {
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.Contains(lines[1], "MOV.W @(H'0000000E),R3") || !strings.Contains(lines[1], "; delay slot") {
		t.Errorf("slot MOV.W not resolved against branch target: %q", lines[1])
	}
	if !strings.Contains(lines[7], ".data.w H'8042") {
		t.Errorf("corrected pool word not rendered: %q", lines[7])
	}
}

// TestDisassembleLinesSlotMOVA covers MOVA in a delay slot.
func TestDisassembleLinesSlotMOVA(t *testing.T) {
	data := []byte{
		0xA0, 0x02, // 0x00 BRA 0x08
		0xC7, 0x01, // 0x02 MOVA @(1,PC),R0 delay slot -> ((0x08+2)&~3)+4 = 0x0C
		0x00, 0x09, // 0x04 NOP
		0x00, 0x09, // 0x06 NOP
		0x00, 0x09, // 0x08 NOP
	}
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 5); err != nil {
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.Contains(lines[1], "MOVA @(H'0000000C),R0") || !strings.Contains(lines[1], "; delay slot") {
		t.Errorf("slot MOVA not resolved against branch target: %q", lines[1])
	}
}

// TestDisassembleLinesSlotUnknownBase covers a PC-relative load in the slot of
// a branch whose destination is not statically known (RTS): no absolute target
// may be printed, no pool entry seeded, and the register must not feed a later
// register-indirect resolution.
func TestDisassembleLinesSlotUnknownBase(t *testing.T) {
	data := []byte{
		0x00, 0x0B, // 0x00 RTS
		0xD3, 0x01, // 0x02 MOV.L @(1,PC),R3 delay slot, base unknown
		0x43, 0x0B, // 0x04 JSR @R3 (must not resolve)
		0x00, 0x09, // 0x06 NOP
		0x12, 0x34, // 0x08 straight-line (wrong) pool target: must stay code
		0x56, 0x78, // 0x0A
	}
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 6); err != nil {
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if strings.Contains(lines[1], "@(H'") {
		t.Errorf("slot MOV.L with unknown base printed an absolute target: %q", lines[1])
	}
	if !strings.Contains(lines[1], "MOV.L @(4,PC),R3") || !strings.Contains(lines[1], "PC-rel base unknown") {
		t.Errorf("slot MOV.L with unknown base not rendered as raw form: %q", lines[1])
	}
	if strings.Contains(lines[2], "; ->") {
		t.Errorf("JSR resolved through an unknown slot load: %q", lines[2])
	}
	if strings.Contains(lines[4], ".data") {
		t.Errorf("straight-line target wrongly seeded as pool data: %q", lines[4])
	}
}

// TestDisassembleLinesSlotViaJSR covers a PC-relative load in the slot of a
// register-indirect branch whose target was resolved from a prior pool load.
func TestDisassembleLinesSlotViaJSR(t *testing.T) {
	data := []byte{
		0xD1, 0x01, // 0x00 MOV.L @(1,PC),R1 -> pool 0x08 = 0x00000020
		0x41, 0x0B, // 0x02 JSR @R1 (-> 0x20)
		0xD3, 0x02, // 0x04 MOV.L @(2,PC),R3 delay slot -> ((0x20+2)&~3)+8 = 0x28
		0x00, 0x09, // 0x06 NOP
		0x00, 0x00, // 0x08 .data.l high
		0x00, 0x20, // 0x0A .data.l low
	}
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 4); err != nil {
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.Contains(lines[2], "MOV.L @(H'00000028),R3") || !strings.Contains(lines[2], "; delay slot") {
		t.Errorf("slot MOV.L not resolved against JSR target: %q", lines[2])
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
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 5); err != nil { // must not panic
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
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
	var buf bytes.Buffer
	if err := disassembleLines(&buf, data, 0, 0, 4); err != nil {
		t.Fatalf("disassembleLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
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
