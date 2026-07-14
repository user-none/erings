// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/user-none/erings/core/sh2"
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
//
// Layout note: 120 bytes = D=2 with a 24-byte dir-entry payload tail,
// so the first data block holds payload bytes 24..83 - the $00 40
// bytes are placed at payload offset 24 to sit at a block start.
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

	// Payload: the first data block (bytes 24..83 after the dir-entry
	// tail) starts with $00 40, which the old content sniff read as
	// block index 64.
	for i := uint32(0); i < dataSize; i++ {
		bus.Write8(dataAddr+i, byte(i+1))
	}
	bus.Write8(dataAddr+24, 0x00)
	bus.Write8(dataAddr+25, 0x40)

	// BUP_Write(unit=0, dir, data, owsw=0).
	master.SetReg(4, 0)
	master.SetReg(5, dirAddr)
	master.SetReg(6, dataAddr)
	master.SetReg(7, 0)
	hlePerDriverSlot4Service(master)
	if got := master.Registers().R[0]; got != bupOK {
		t.Fatalf("BUP_Write R0 = %d, want %d (bupOK)", got, bupOK)
	}

	// BUP_Verify(unit=0, dir, data).
	master.SetReg(4, 0)
	master.SetReg(5, verAddr)
	master.SetReg(6, dataAddr)
	hlePerDriverSlot8Service(master)
	if got := master.Registers().R[0]; got != bupOK {
		t.Fatalf("BUP_Verify R0 = %d, want %d (bupOK) - data block beginning $00 40 misread as continuation page", got, bupOK)
	}
}

// bupTestWriteFile lays down a caller BupDir + payload and calls
// BUP_Write. The payload byte at position i is pattern(i).
func bupTestWriteFile(bus *Bus, master *sh2.CPU, name string, size uint32, pattern func(uint32) byte) uint32 {
	const (
		dirAddr  uint32 = 0x06070000
		dataAddr uint32 = 0x06072000
	)
	var n [11]byte
	copy(n[:], name)
	for i, b := range n {
		bus.Write8(dirAddr+uint32(i), b)
	}
	bus.Write8(dirAddr+0x17, 0)
	bus.Write32(dirAddr+0x18, 0x0174FF82)
	bus.Write32(dirAddr+0x1C, size)
	for i := uint32(0); i < size; i++ {
		bus.Write8(dataAddr+i, pattern(i))
	}
	master.SetReg(4, 0)
	master.SetReg(5, dirAddr)
	master.SetReg(6, dataAddr)
	master.SetReg(7, 0)
	hlePerDriverSlot4Service(master)
	return master.Registers().R[0]
}

// bupTestVerifyFile byte-compares the named file against pattern via
// BUP_Verify.
func bupTestVerifyFile(bus *Bus, master *sh2.CPU, name string, size uint32, pattern func(uint32) byte) uint32 {
	const (
		dirAddr  uint32 = 0x06070000
		dataAddr uint32 = 0x06072000
	)
	var n [11]byte
	copy(n[:], name)
	for i, b := range n {
		bus.Write8(dirAddr+uint32(i), b)
	}
	for i := uint32(0); i < size; i++ {
		bus.Write8(dataAddr+i, pattern(i))
	}
	master.SetReg(4, 0)
	master.SetReg(5, dirAddr)
	master.SetReg(6, dataAddr)
	hlePerDriverSlot8Service(master)
	return master.Registers().R[0]
}

// TestBUPWriteVerifyEmptyContPage is the WANGAN TRIALLOVE (T-9110G)
// regression. Its 880-byte ranking save uses the D=14, C=1 overflow
// layout whose single continuation page lists ZERO indices - the
// page is a $0000 $0000 sentinel, a $0000 terminator at +$04, and a
// 58-byte payload tail (layout confirmed from a real-BIOS-written
// T-9110G.srm). Content-based cont-page detection rejects the empty
// list ("no valid first index") and misreads the page as a data
// block, so BUP_Verify returned BUP_NO_MATCH right after a
// successful BUP_Write and the game reported the save as failed.
// Every datasize in 841..898 hits this layout.
func TestBUPWriteVerifyEmptyContPage(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	pattern := func(i uint32) byte { return byte(i*7 + 3) }

	if got := bupTestWriteFile(bus, master, "WANGANLOVE0", 880, pattern); got != bupOK {
		t.Fatalf("BUP_Write R0 = %d, want %d (bupOK)", got, bupOK)
	}
	if got := bupTestVerifyFile(bus, master, "WANGANLOVE0", 880, pattern); got != bupOK {
		t.Fatalf("BUP_Verify R0 = %d, want %d (bupOK) - empty cont page misread as data block", got, bupOK)
	}

	// Read back and byte-compare independently of Verify.
	const rdAddr uint32 = 0x06074000
	master.SetReg(4, 0)
	master.SetReg(5, 0x06070000)
	master.SetReg(6, rdAddr)
	hlePerDriverSlot5Service(master)
	if got := master.Registers().R[0]; got != bupOK {
		t.Fatalf("BUP_Read R0 = %d, want %d (bupOK)", got, bupOK)
	}
	for i := uint32(0); i < 880; i++ {
		if got := bus.Read8(rdAddr + i); got != pattern(i) {
			t.Fatalf("read-back byte %d = %02x, want %02x", i, got, pattern(i))
		}
	}
}

// TestBUPWriteVerifyDataBlockIndexLookalike covers the other
// structural-classification failure: an overflow-layout file whose
// in-entry DATA block begins with payload bytes that happen to form
// a valid block index. With the i*7+3 pattern at datasize 1305
// (D=22, C=1), the 7th in-entry data block's payload starts
// $01 08 = block 264, which content sniffing misreads as a
// continuation page (folding garbage indices into the chain). The
// real driver classifies by phase, not content: once the last cont
// page's terminator is seen, every later block is data.
func TestBUPWriteVerifyDataBlockIndexLookalike(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	pattern := func(i uint32) byte { return byte(i*7 + 3) }

	if got := bupTestWriteFile(bus, master, "LOOKALIKE", 1305, pattern); got != bupOK {
		t.Fatalf("BUP_Write R0 = %d, want %d (bupOK)", got, bupOK)
	}
	if got := bupTestVerifyFile(bus, master, "LOOKALIKE", 1305, pattern); got != bupOK {
		t.Fatalf("BUP_Verify R0 = %d, want %d (bupOK) - data block with index-lookalike payload misread as cont page", got, bupOK)
	}
}

// TestBUPDeleteEmptyContPageFreesBlocks checks the claimed-block walk
// (bupExpandBlockList) classifies the empty cont page structurally:
// deleting the D=14, C=1 file must free all 16 blocks (dir entry +
// cont page + 14 data blocks) so the space is reusable.
func TestBUPDeleteEmptyContPageFreesBlocks(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	pattern := func(i uint32) byte { return byte(i*7 + 3) }

	baseline := bupCountUsedBlocks(master)
	if got := bupTestWriteFile(bus, master, "WANGANLOVE0", 880, pattern); got != bupOK {
		t.Fatalf("BUP_Write R0 = %d, want %d (bupOK)", got, bupOK)
	}
	if used := bupCountUsedBlocks(master); used != baseline+16 {
		t.Fatalf("used blocks after write = %d, want %d", used, baseline+16)
	}

	// BUP_Delete(unit=0, fname).
	master.SetReg(4, 0)
	master.SetReg(5, 0x06070000)
	hlePerDriverSlot6Service(master)
	if got := master.Registers().R[0]; got != bupOK {
		t.Fatalf("BUP_Delete R0 = %d, want %d (bupOK)", got, bupOK)
	}
	if used := bupCountUsedBlocks(master); used != baseline {
		t.Fatalf("used blocks after delete = %d, want baseline %d - empty cont page's blocks not reclaimed", used, baseline)
	}
}

// TestBUPWritePureDirTailLayout checks the pure in-entry on-disk
// format against the console-written T-14411G GROOVE_ON_F sample:
// a 202-byte file occupies exactly 4 blocks (dir + 3 data, per the
// real Write's (datasize+29)/58+1 block accounting at +$0B76), with
// payload bytes 0..21 stored in the dir entry after the $0000
// terminator and the data blocks holding bytes 22.. onward.
func TestBUPWritePureDirTailLayout(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	pattern := func(i uint32) byte { return byte(i + 1) }

	if got := bupTestWriteFile(bus, master, "PURETAIL", 202, pattern); got != bupOK {
		t.Fatalf("BUP_Write R0 = %d, want %d (bupOK)", got, bupOK)
	}

	var name [11]byte
	copy(name[:], "PURETAIL")
	entry := bupFindEntry(master, name)
	if entry < 0 {
		t.Fatal("entry not found")
	}
	listBase := entry*bupBlockSize + bupDirListOff
	var blocks []uint16
	for i := 0; i < bupDirListWordSlots; i++ {
		w := bupRAMBE16(master, listBase+i*2)
		if w == 0 {
			break
		}
		blocks = append(blocks, w)
	}
	if len(blocks) != 3 {
		t.Fatalf("data block count = %d, want 3", len(blocks))
	}
	// Dir tail: payload bytes 0..21 at +$22+8 .. +$3F.
	tailOff := entry*bupBlockSize + bupDirListOff + (3+1)*2
	for i := 0; i < 22; i++ {
		if got := bupRAMByte(master, tailOff+i); got != pattern(uint32(i)) {
			t.Fatalf("dir tail byte %d = %02x, want %02x", i, got, pattern(uint32(i)))
		}
	}
	// First data block holds payload bytes 22..81.
	b0 := int(blocks[0]) * bupBlockSize
	for i := 0; i < 60; i++ {
		if got := bupRAMByte(master, b0+bupBlockHeaderSize+i); got != pattern(uint32(22+i)) {
			t.Fatalf("data block byte %d = %02x, want %02x", i, got, pattern(uint32(22+i)))
		}
	}

	if got := bupTestVerifyFile(bus, master, "PURETAIL", 202, pattern); got != bupOK {
		t.Fatalf("BUP_Verify R0 = %d, want %d (bupOK)", got, bupOK)
	}
}

// TestBUPReadConsolePureSave hand-builds a pure-layout file exactly
// as a real console writes it (dir tail carrying the leading payload
// bytes) and checks BUP_Read reassembles the payload - the reader
// must handle real files regardless of what the HLE writer produces.
func TestBUPReadConsolePureSave(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()

	const size = 148 // D=3, 22-byte dir tail + 60 + 60 + 6
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(200 - i)
	}

	// Dir entry at block 2, data blocks 3-5.
	off := 2 * bupBlockSize
	bupRAMSetBE32(master, off, 0x80000000)
	bupRAMWrite(master, off+4, []byte("CONSOLE\x00\x00\x00\x00"))
	bupRAMSetBE32(master, off+0x1E, size)
	for i, blk := range []uint16{3, 4, 5} {
		bupRAMSetBE16(master, off+bupDirListOff+i*2, blk)
	}
	// Terminator at slot 3 is already zero; payload bytes 0..21 go
	// after it.
	bupRAMWrite(master, off+bupDirListOff+8, payload[:22])
	bupRAMWrite(master, 3*bupBlockSize+bupBlockHeaderSize, payload[22:82])
	bupRAMWrite(master, 4*bupBlockSize+bupBlockHeaderSize, payload[82:142])
	bupRAMWrite(master, 5*bupBlockSize+bupBlockHeaderSize, payload[142:])

	const (
		dirAddr uint32 = 0x06070000
		rdAddr  uint32 = 0x06074000
	)
	for i, b := range []byte("CONSOLE\x00\x00\x00\x00") {
		bus.Write8(dirAddr+uint32(i), b)
	}
	master.SetReg(4, 0)
	master.SetReg(5, dirAddr)
	master.SetReg(6, rdAddr)
	hlePerDriverSlot5Service(master)
	if got := master.Registers().R[0]; got != bupOK {
		t.Fatalf("BUP_Read R0 = %d, want %d (bupOK)", got, bupOK)
	}
	for i := 0; i < size; i++ {
		if got := bus.Read8(rdAddr + uint32(i)); got != payload[i] {
			t.Fatalf("payload byte %d = %02x, want %02x", i, got, payload[i])
		}
	}
}

// TestBUPWriteTinySaveDirTailOnly covers datasize <= 28: the block
// accounting yields zero data blocks and the whole payload lives in
// the dir-entry tail after the terminator at +$22.
func TestBUPWriteTinySaveDirTailOnly(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	pattern := func(i uint32) byte { return byte(0x40 + i) }

	baseline := bupCountUsedBlocks(master)
	if got := bupTestWriteFile(bus, master, "TINY", 20, pattern); got != bupOK {
		t.Fatalf("BUP_Write R0 = %d, want %d (bupOK)", got, bupOK)
	}
	if used := bupCountUsedBlocks(master); used != baseline+1 {
		t.Fatalf("used blocks = %d, want %d (dir entry only)", used, baseline+1)
	}
	if got := bupTestVerifyFile(bus, master, "TINY", 20, pattern); got != bupOK {
		t.Fatalf("BUP_Verify R0 = %d, want %d (bupOK)", got, bupOK)
	}
}

// TestBUPWritePureOverflowBoundary pins the 840/841 layout boundary:
// 840 bytes is the largest pure in-entry file (D=14, zero-length dir
// tail) and 841 is the smallest overflow file (D=14 plus an empty
// continuation page whose 58-byte tail leads the payload).
func TestBUPWritePureOverflowBoundary(t *testing.T) {
	pattern := func(i uint32) byte { return byte(i*3 + 7) }
	for _, size := range []uint32{840, 841} {
		_, bus, master, _ := newHLEBIOSForTest()
		if got := bupTestWriteFile(bus, master, "BOUNDARY", size, pattern); got != bupOK {
			t.Fatalf("size %d: BUP_Write R0 = %d, want %d (bupOK)", size, got, bupOK)
		}
		if got := bupTestVerifyFile(bus, master, "BOUNDARY", size, pattern); got != bupOK {
			t.Fatalf("size %d: BUP_Verify R0 = %d, want %d (bupOK)", size, got, bupOK)
		}
	}
}
