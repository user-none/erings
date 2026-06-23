// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"
)

// TestBUPWriteVerifyDataLooksLikeContPage is the GROOVE ON FIGHT
// regression. The game writes its high-score save (pure in-entry,
// terminated block list) then immediately BUP_Verify's it and only
// proceeds if verify returns 0. One of the save's data blocks begins
// with the bytes $00 40 ($0040 = block index 64). Because every data
// block has a zero 4-byte header, the old read-chain builder's
// content sniff (zero header + in-range word at +$04 => continuation
// page) misclassified that data block as a continuation page, parsed
// its payload as a block-index list, read the wrong bytes, and
// returned BUP_NO_MATCH against data BUP_Write had just stored
// correctly. The real driver classifies blocks by list structure and
// $0000 terminators, never by content, so a terminated list is always
// pure data blocks. This test writes such a file and asserts the
// round-trip verifies clean.
func TestBUPWriteVerifyDataLooksLikeContPage(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()

	const (
		dirAddr  uint32 = 0x06070000 // caller BupDir for Write
		verAddr  uint32 = 0x06070100 // caller BupDir for Verify (filename only)
		dataAddr uint32 = 0x06070200 // payload source
		dataSize uint32 = 120        // two 60-byte data blocks, pure in-entry
	)

	// Caller filename in both dir structs.
	name := []byte("HISCORE\x00\x00\x00\x00") // 11 bytes
	for i, b := range name {
		bus.Write8(dirAddr+uint32(i), b)
		bus.Write8(verAddr+uint32(i), b)
	}
	// Write-dir metadata: language +$17, date +$18, datasize +$1C.
	bus.Write8(dirAddr+0x17, 0)
	bus.Write32(dirAddr+0x18, 0x0174FF82) // arbitrary packed date
	bus.Write32(dirAddr+0x1C, dataSize)

	// Payload: the second data block (bytes 60..119) starts with
	// $00 40, which the old content sniff read as block index 64.
	for i := uint32(0); i < dataSize; i++ {
		bus.Write8(dataAddr+i, byte(i+1))
	}
	bus.Write8(dataAddr+60, 0x00)
	bus.Write8(dataAddr+61, 0x40)

	// BUP_Write(unit=0, dir, data, owsw=0).
	master.SetReg(4, 0)
	master.SetReg(5, dirAddr)
	master.SetReg(6, dataAddr)
	master.SetReg(7, 0)
	hlePerDriverSlot4Service(master, bus)
	if got := master.Registers().R[0]; got != bupOK {
		t.Fatalf("BUP_Write R0 = %d, want %d (bupOK)", got, bupOK)
	}

	// BUP_Verify(unit=0, dir, data).
	master.SetReg(4, 0)
	master.SetReg(5, verAddr)
	master.SetReg(6, dataAddr)
	hlePerDriverSlot8Service(master, bus)
	if got := master.Registers().R[0]; got != bupOK {
		t.Fatalf("BUP_Verify R0 = %d, want %d (bupOK) - data block beginning $00 40 misread as continuation page", got, bupOK)
	}
}
