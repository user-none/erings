// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"
	"math"
	"strings"
	"testing"

	"github.com/klauspost/compress/s2"
)

type stateHeader struct {
	version  uint32
	gameID   string
	biosHash [sha256.Size]byte
	dataCRC  uint32
}

// parseStateForTest strictly parses a serialized state: header fields,
// CRC check, decompression, then chunk and field framing with exact
// length consumption. Any framing violation fails the test.
func parseStateForTest(t *testing.T, data []byte) (stateHeader, map[string]map[string][]byte) {
	t.Helper()
	var hdr stateHeader

	if len(data) < len(stateMagic)+4+1 {
		t.Fatalf("state too short: %d bytes", len(data))
	}
	pos := 0
	if string(data[:len(stateMagic)]) != stateMagic {
		t.Fatalf("magic = %q, want %q", data[:len(stateMagic)], stateMagic)
	}
	pos += len(stateMagic)
	hdr.version = binary.BigEndian.Uint32(data[pos:])
	pos += 4
	idLen := int(data[pos])
	pos++
	if pos+idLen+sha256.Size+4 > len(data) {
		t.Fatalf("header truncated")
	}
	hdr.gameID = string(data[pos : pos+idLen])
	pos += idLen
	copy(hdr.biosHash[:], data[pos:])
	pos += sha256.Size
	hdr.dataCRC = binary.BigEndian.Uint32(data[pos:])
	pos += 4

	payload := data[pos:]
	if got := crc32.ChecksumIEEE(payload); got != hdr.dataCRC {
		t.Fatalf("dataCRC = %08x, computed %08x", hdr.dataCRC, got)
	}
	// Segmented body: u32 count, then per segment u32 rawLen, u32
	// compLen, S2 block.
	if len(payload) < 4 {
		t.Fatalf("segment table truncated")
	}
	segCount := int(binary.BigEndian.Uint32(payload))
	sp := 4
	var body []byte
	for i := 0; i < segCount; i++ {
		if sp+8 > len(payload) {
			t.Fatalf("segment %d header truncated", i)
		}
		rawLen := int(binary.BigEndian.Uint32(payload[sp:]))
		compLen := int(binary.BigEndian.Uint32(payload[sp+4:]))
		sp += 8
		if sp+compLen > len(payload) {
			t.Fatalf("segment %d overruns payload", i)
		}
		seg, err := s2.Decode(nil, payload[sp:sp+compLen])
		if err != nil {
			t.Fatalf("segment %d decode: %v", i, err)
		}
		if len(seg) != rawLen {
			t.Fatalf("segment %d decompressed to %d bytes, declared %d", i, len(seg), rawLen)
		}
		body = append(body, seg...)
		sp += compLen
	}
	if sp != len(payload) {
		t.Fatalf("%d trailing bytes after segments", len(payload)-sp)
	}

	chunks := make(map[string]map[string][]byte)
	bp := 0
	for bp < len(body) {
		if bp+stateTagLen+4 > len(body) {
			t.Fatalf("chunk header truncated at offset %d", bp)
		}
		rawTag := body[bp : bp+stateTagLen]
		nameEnd := bytes.IndexByte(rawTag, 0)
		if nameEnd <= 0 {
			t.Fatalf("chunk tag at offset %d has no name or no zero fill", bp)
		}
		tag := string(rawTag[:nameEnd])
		for _, b := range rawTag[nameEnd:] {
			if b != 0 {
				t.Fatalf("chunk %q tag not zero-filled", tag)
			}
		}
		bp += stateTagLen
		clen := int(binary.BigEndian.Uint32(body[bp:]))
		bp += 4
		if bp+clen > len(body) {
			t.Fatalf("chunk %q length %d overruns body", tag, clen)
		}
		if _, dup := chunks[tag]; dup {
			t.Fatalf("duplicate chunk %q", tag)
		}

		fields := make(map[string][]byte)
		payload := body[bp : bp+clen]
		fp := 0
		for fp < len(payload) {
			nlen := int(payload[fp])
			fp++
			if nlen == 0 || fp+nlen+4 > len(payload) {
				t.Fatalf("chunk %q: field header invalid at payload offset %d", tag, fp-1)
			}
			name := string(payload[fp : fp+nlen])
			fp += nlen
			fsize := int(binary.BigEndian.Uint32(payload[fp:]))
			fp += 4
			if fp+fsize > len(payload) {
				t.Fatalf("chunk %q field %q size %d overruns chunk", tag, name, fsize)
			}
			if _, dup := fields[name]; dup {
				t.Fatalf("chunk %q: duplicate field %q", tag, name)
			}
			fields[name] = payload[fp : fp+fsize]
			fp += fsize
		}
		if fp != len(payload) {
			t.Fatalf("chunk %q: fields consumed %d of %d payload bytes", tag, fp, len(payload))
		}
		chunks[tag] = fields
		bp += clen
	}
	if bp != len(body) {
		t.Fatalf("body consumed %d of %d bytes", bp, len(body))
	}
	return hdr, chunks
}

// TestSerializeHeader verifies the header of a BIOS-less (HLE) emulator:
// magic, version, empty gameID, zeroed BIOS hash, HLE flag set, valid CRC.
func TestSerializeHeader(t *testing.T) {
	e := NewEmulator()
	data, err := e.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	hdr, _ := parseStateForTest(t, data)
	if hdr.version != stateVersion {
		t.Errorf("version = %d, want %d", hdr.version, stateVersion)
	}
	if hdr.gameID != "" {
		t.Errorf("gameID = %q, want empty (no disc)", hdr.gameID)
	}
	if hdr.biosHash != [sha256.Size]byte{} {
		t.Errorf("biosHash non-zero without a BIOS loaded (all-zero marks HLE)")
	}
}

// TestSerializeChunkSet verifies the serialized chunk set is exactly the
// designed set, with strict framing (verified by the parser).
func TestSerializeChunkSet(t *testing.T) {
	e := NewEmulator()
	data, err := e.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	_, chunks := parseStateForTest(t, data)

	want := []string{
		tagWRAMH, tagWRAML, tagBackup, tagExtRAM,
		tagVDP1VRAM, tagVDP1DrawFB, tagVDP1DispFB,
		tagVDP2VRAM, tagVDP2CRAM, tagSoundRAM,
		tagSH2MCore, tagSH2MCache, tagSH2SCore, tagSH2SCache,
		tagSCUCore, tagSCUDSP,
		tagSCSPRegs, tagSCSPSlots, tagSCSPDSP, tagSCSPTimers, tagSCSPMisc,
		tagM68K,
		tagVDP1Regs, tagVDP1Resume, tagVDP2State,
		tagSMPC,
		tagCDState, tagCDBuffers, tagCDAudioQ, tagCDMpeg,
		tagBusMisc, tagEmu, tagIPImage,
	}
	if len(chunks) != len(want) {
		t.Errorf("chunk count = %d, want %d", len(chunks), len(want))
	}
	for _, tag := range want {
		if _, ok := chunks[tag]; !ok {
			t.Errorf("missing chunk %q", tag)
		}
	}
}

// TestSerializeValues verifies representative field values across
// subsystems round out of the container bytes exactly as set.
func TestSerializeValues(t *testing.T) {
	e := NewEmulator()

	e.bus.wramH[0x1234] = 0xAB
	e.master.SetPC(0x06001234)
	e.master.SetReg(3, 0xCAFEBABE)
	e.slave.SetIRL(6, 0x43)
	e.vdp2.regs[vdp2BGON] = 0x5678
	e.scu.ims = 0xDEAD
	e.scu.irlWithheld = true
	e.smpc.oreg[5] = 0x42
	e.smpc.cmdIREG[2] = 0x77
	e.smpc.pendingOps = append(e.smpc.pendingOps,
		smpcOp{cont: false, val: 0x10, ireg: [7]uint8{0x40, 1, 2, 3, 4, 5, 6}},
		smpcOp{cont: true, val: 0x80})
	e.vdp2.latchedOddField = true
	e.scsp.soundReqPending.Store(true)
	e.pendingSystemReset.Store(true)
	e.lineWidthAccum = 0x123456789A
	e.scsp.slots[7].egLevel = -12345
	e.vdp1.copr = 0x0040
	e.cdblock.getSectorLen = 2
	sec := bufferedSector{
		data:       make([]byte, 2352),
		size:       2352,
		userOffset: 16,
		userSize:   2048,
		fad:        0x00012345,
		isMode2:    false,
	}
	sec.data[100] = 0xCD
	e.cdblock.partitions[3].sectors = append(e.cdblock.partitions[3].sectors, sec)
	e.cdblock.delPart = &e.cdblock.partitions[3]
	e.cdblock.mpeg.active = true
	e.cdblock.mpeg.intMask = 0x00123456
	e.cdblock.mpeg.window[2][1] = 0x0150
	e.cdblock.mpeg.lsi[1][5] = 0xBEEF
	e.cdblock.mpeg.conn[1][0].bufNum = 9
	e.cdblock.mpeg.play = newMpegPlayback()
	e.cdblock.mpeg.play.layers[1].phase = mpegPhaseEnding
	e.cdblock.mpeg.play.layers[1].fed = 42
	latch := &e.cdblock.mpegFrame
	latch.slots[0].w, latch.slots[0].h = 2, 1
	latch.slots[0].rgb = []uint32{0x00112233, 0x00445566}
	latch.consHas = true
	latch.dispOn.Store(true)

	data, err := e.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	_, chunks := parseStateForTest(t, data)

	if got := chunks[tagWRAMH]["data"][0x1234]; got != 0xAB {
		t.Errorf("WRAM_H[0x1234] = %02x, want ab", got)
	}
	if got := binary.BigEndian.Uint32(chunks[tagSH2MCore]["PC"]); got != 0x06001234 {
		t.Errorf("SH2M PC = %08x, want 06001234", got)
	}
	if got := binary.BigEndian.Uint32(chunks[tagSH2MCore]["R"][3*4:]); got != 0xCAFEBABE {
		t.Errorf("SH2M R3 = %08x, want cafebabe", got)
	}
	if got := binary.BigEndian.Uint32(chunks[tagSH2SCore]["irl"]); got != 0x00060043 {
		t.Errorf("SH2S irl = %08x, want 00060043", got)
	}
	for _, want := range []struct {
		field string
		size  int
	}{{"data", 4096}, {"tag", 1024}, {"valid", 256}, {"lru", 64}} {
		if got := len(chunks[tagSH2MCache][want.field]); got != want.size {
			t.Errorf("SH2M_CACHE %s size = %d, want %d", want.field, got, want.size)
		}
	}
	if got := binary.BigEndian.Uint16(chunks[tagVDP2State]["regs"][vdp2BGON*2:]); got != 0x5678 {
		t.Errorf("VDP2 BGON = %04x, want 5678", got)
	}
	if got := binary.BigEndian.Uint32(chunks[tagSCUCore]["ims"]); got != 0xDEAD {
		t.Errorf("SCU ims = %08x, want dead", got)
	}
	if got := chunks[tagSCUCore]["irlWithheld"]; len(got) != 1 || got[0] != 1 {
		t.Errorf("SCU irlWithheld = %v, want [1]", got)
	}
	if got := chunks[tagSMPC]["oreg"][5]; got != 0x42 {
		t.Errorf("SMPC oreg[5] = %02x, want 42", got)
	}
	if got := chunks[tagSMPC]["cmdIREG"][2]; got != 0x77 {
		t.Errorf("SMPC cmdIREG[2] = %02x, want 77", got)
	}
	// pendingOps: u32 count, then per op cont u8, val u8, ireg[7].
	po := chunks[tagSMPC]["pendingOps"]
	if got := binary.BigEndian.Uint32(po); got != 2 {
		t.Fatalf("SMPC pendingOps count = %d, want 2", got)
	}
	if po[4] != 0 || po[5] != 0x10 || po[6] != 0x40 {
		t.Errorf("SMPC pendingOps[0] = cont %d val %02x ireg0 %02x, want 0 10 40", po[4], po[5], po[6])
	}
	if po[13] != 1 || po[14] != 0x80 {
		t.Errorf("SMPC pendingOps[1] = cont %d val %02x, want 1 80", po[13], po[14])
	}
	if got := chunks[tagVDP2State]["latchedOddField"]; len(got) != 1 || got[0] != 1 {
		t.Errorf("VDP2 latchedOddField = %v, want [1]", got)
	}
	if got := chunks[tagSCSPMisc]["soundReqPending"]; len(got) != 1 || got[0] != 1 {
		t.Errorf("SCSP soundReqPending = %v, want [1]", got)
	}
	if got := chunks[tagEmu]["pendingSystemReset"]; len(got) != 1 || got[0] != 1 {
		t.Errorf("EMU pendingSystemReset = %v, want [1]", got)
	}
	if got := binary.BigEndian.Uint64(chunks[tagEmu]["lineWidthAccum"]); got != 0x123456789A {
		t.Errorf("EMU lineWidthAccum = %x, want 123456789a", got)
	}
	if got := int32(binary.BigEndian.Uint32(chunks[tagSCSPSlots]["egLevel"][7*4:])); got != -12345 {
		t.Errorf("SCSP slot7 egLevel = %d, want -12345", got)
	}
	if got := binary.BigEndian.Uint16(chunks[tagVDP1Regs]["copr"]); got != 0x0040 {
		t.Errorf("VDP1 copr = %04x, want 0040", got)
	}
	if got := int64(binary.BigEndian.Uint64(chunks[tagCDState]["getSectorLen"])); got != 2 {
		t.Errorf("CD getSectorLen = %d, want 2", got)
	}
	if got := int64(binary.BigEndian.Uint64(chunks[tagCDState]["delPart"])); got != 3 {
		t.Errorf("CD delPart index = %d, want 3", got)
	}

	mp := chunks[tagCDMpeg]
	if got := mp["active"]; len(got) != 1 || got[0] != 1 {
		t.Errorf("MPEG active = %v, want [1]", got)
	}
	if got := binary.BigEndian.Uint32(mp["intMask"]); got != 0x00123456 {
		t.Errorf("MPEG intMask = %08x, want 00123456", got)
	}
	if got := binary.BigEndian.Uint16(mp["window"][(2*3+1)*2:]); got != 0x0150 {
		t.Errorf("MPEG window[2][1] = %04x, want 0150", got)
	}
	if got := binary.BigEndian.Uint16(mp["lsi"][(128+5)*2:]); got != 0xBEEF {
		t.Errorf("MPEG lsi[1][5] = %04x, want beef", got)
	}
	if got := mp["conn.bufNum"][1*2+0]; got != 9 {
		t.Errorf("MPEG conn[1][0].bufNum = %d, want 9", got)
	}
	if got := mp["playActive"]; len(got) != 1 || got[0] != 1 {
		t.Errorf("MPEG playActive = %v, want [1]", got)
	}
	if got := mp["layer.phase"][1]; got != byte(mpegPhaseEnding) {
		t.Errorf("MPEG layer1 phase = %d, want %d", got, mpegPhaseEnding)
	}
	if got := int64(binary.BigEndian.Uint64(mp["layer.fed"][8:])); got != 42 {
		t.Errorf("MPEG layer1 fed = %d, want 42", got)
	}
	// firstVidPTS initialized to -1 by newMpegPlayback.
	if got := math.Float64frombits(binary.BigEndian.Uint64(mp["firstVidPTS"])); got != -1 {
		t.Errorf("MPEG firstVidPTS = %v, want -1", got)
	}
	// Latch slot 0: w=2, h=1, two pixels.
	ls := mp["latch.slots"]
	if got := binary.BigEndian.Uint32(ls[0:]); got != 2 {
		t.Errorf("latch slot0 w = %d, want 2", got)
	}
	if got := binary.BigEndian.Uint32(ls[4:]); got != 1 {
		t.Errorf("latch slot0 h = %d, want 1", got)
	}
	if got := binary.BigEndian.Uint32(ls[12:]); got != 0x00445566 {
		t.Errorf("latch slot0 px1 = %08x, want 00445566", got)
	}
	if got := mp["latch.dispOn"]; len(got) != 1 || got[0] != 1 {
		t.Errorf("latch dispOn = %v, want [1]", got)
	}

	// Partition composite: partitions 0-2 empty (u32 zero each), then
	// partition 3 with one sector.
	pb := chunks[tagCDBuffers]["partitions"]
	pp := 0
	for i := 0; i < 3; i++ {
		if got := binary.BigEndian.Uint32(pb[pp:]); got != 0 {
			t.Fatalf("partition %d sector count = %d, want 0", i, got)
		}
		pp += 4
	}
	if got := binary.BigEndian.Uint32(pb[pp:]); got != 1 {
		t.Fatalf("partition 3 sector count = %d, want 1", got)
	}
	pp += 4
	dlen := int(binary.BigEndian.Uint32(pb[pp:]))
	pp += 4
	if dlen != 2352 {
		t.Fatalf("sector data length = %d, want 2352", dlen)
	}
	if got := pb[pp+100]; got != 0xCD {
		t.Errorf("sector data[100] = %02x, want cd", got)
	}
	pp += dlen
	if got := int64(binary.BigEndian.Uint64(pb[pp:])); got != 2352 {
		t.Errorf("sector size = %d, want 2352", got)
	}
	pp += 8
	if got := int64(binary.BigEndian.Uint64(pb[pp:])); got != 16 {
		t.Errorf("sector userOffset = %d, want 16", got)
	}
	pp += 8
	if got := int64(binary.BigEndian.Uint64(pb[pp:])); got != 2048 {
		t.Errorf("sector userSize = %d, want 2048", got)
	}
	pp += 8
	if got := binary.BigEndian.Uint32(pb[pp:]); got != 0x00012345 {
		t.Errorf("sector fad = %08x, want 00012345", got)
	}
}

// fillEmulatorState sets representative values across every subsystem,
// exercising the variable-length paths (partitions, pendingOps, MPEG
// playback, frame latch, dataBuf, putBuf) as well as scalars.
func fillEmulatorState(e *Emulator) {
	e.bus.wramH[0x1234] = 0xAB
	e.bus.wramL[7] = 0x11
	e.master.SetPC(0x06001234)
	e.master.SetReg(3, 0xCAFEBABE)
	e.slave.SetIRL(6, 0x43)
	e.vdp2.regs[vdp2BGON] = 0x5678
	e.vdp2.latchedOddField = true
	e.scu.ims = 0xDEAD
	e.scu.irlWithheld = true
	e.scu.dsp.prog[10] = 0x12345678
	e.smpc.oreg[5] = 0x42
	e.smpc.cmdIREG[2] = 0x77
	e.smpc.pendingOps = append(e.smpc.pendingOps,
		smpcOp{cont: false, val: 0x10, ireg: [7]uint8{0x40, 1, 2, 3, 4, 5, 6}},
		smpcOp{cont: true, val: 0x80})
	e.scsp.slots[7].egLevel = -12345
	e.scsp.soundReqPending.Store(true)
	e.vdp1.copr = 0x0040
	e.vdp1.lateEraseFB = e.vdp1.displayFB
	e.vdp1.distortedResume.pxFP = -99887766
	e.pendingSystemReset.Store(true)
	e.lineWidthAccum = 0x123456789A
	e.bus.minitWritten = true
	e.cdblock.getSectorLen = 2
	e.cdblock.dataBuf = []uint16{0x1122, 0x3344}
	e.cdblock.dataPos = 1
	e.cdblock.putBuf = []byte{9, 8, 7}
	sec := bufferedSector{
		data:       make([]byte, 2352),
		size:       2352,
		userOffset: 16,
		userSize:   2048,
		fad:        0x00012345,
	}
	sec.data[100] = 0xCD
	e.cdblock.partitions[3].sectors = append(e.cdblock.partitions[3].sectors, sec)
	e.cdblock.delPart = &e.cdblock.partitions[3]
	e.cdblock.audioQueue[5] = -321
	e.cdblock.audioCount = 6
	e.cdblock.mpeg.active = true
	e.cdblock.mpeg.intMask = 0x00123456
	e.cdblock.mpeg.window[2][1] = 0x0150
	e.cdblock.mpeg.lsi[1][5] = 0xBEEF
	e.cdblock.mpeg.conn[1][0].bufNum = 9
	e.cdblock.mpeg.play = newMpegPlayback()
	e.cdblock.mpeg.play.layers[1].phase = mpegPhaseEnding
	e.cdblock.mpeg.play.layers[1].fed = 42
	latch := &e.cdblock.mpegFrame
	latch.slots[0].w, latch.slots[0].h = 2, 1
	latch.slots[0].rgb = []uint32{0x00112233, 0x00445566}
	latch.consHas = true
	latch.dispOn.Store(true)
}

// TestStateRoundTrip is the correctness gate: Serialize, Deserialize
// into a fresh emulator, Serialize again, and require byte-identical
// output.
func TestStateRoundTrip(t *testing.T) {
	e1 := NewEmulator()
	fillEmulatorState(e1)
	s1, err := e1.Serialize()
	if err != nil {
		t.Fatalf("Serialize e1: %v", err)
	}

	e2 := NewEmulator()
	if err := e2.Deserialize(s1); err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	s2, err := e2.Serialize()
	if err != nil {
		t.Fatalf("Serialize e2: %v", err)
	}
	if !bytes.Equal(s1, s2) {
		// Locate the first differing chunk/field for the failure message.
		_, c1 := parseStateForTest(t, s1)
		_, c2 := parseStateForTest(t, s2)
		for tag, fields := range c1 {
			for name, v1 := range fields {
				if v2, ok := c2[tag][name]; !ok || !bytes.Equal(v1, v2) {
					t.Errorf("round-trip mismatch at %s.%s", tag, name)
				}
			}
		}
		t.Fatalf("round-trip serialization not byte-identical (len %d vs %d)", len(s1), len(s2))
	}

	// Spot-check restored live state beyond the byte compare.
	if got := e2.master.Registers().PC; got != 0x06001234 {
		t.Errorf("restored master PC = %08x", got)
	}
	if e2.cdblock.delPart != &e2.cdblock.partitions[3] {
		t.Errorf("restored delPart does not point at partitions[3]")
	}
	if e2.cdblock.mpeg.play == nil {
		t.Errorf("restored MPEG playback is nil")
	} else if e2.cdblock.mpeg.play.layers[1].phase != mpegPhaseEnding {
		t.Errorf("restored MPEG layer1 phase = %d", e2.cdblock.mpeg.play.layers[1].phase)
	}
	if &e2.vdp1.lateEraseFB[0] != &e2.vdp1.displayFB[0] {
		t.Errorf("restored lateEraseFB does not alias displayFB")
	}
	if !e2.pendingSystemReset.Load() {
		t.Errorf("restored pendingSystemReset not set")
	}
}

// corruptHeaderVersion returns a copy of the state with the header
// version replaced. The CRC covers only the compressed body, so header
// edits stay valid.
func corruptHeaderVersion(data []byte, version uint32) []byte {
	out := append([]byte(nil), data...)
	binary.BigEndian.PutUint32(out[len(stateMagic):], version)
	return out
}

// TestDeserializeErrors exercises the header gates: version window
// (distinct newer/older messages), magic, CRC, and BIOS pairing.
func TestDeserializeErrors(t *testing.T) {
	e := NewEmulator()
	good, err := e.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	fresh := func() *Emulator { return NewEmulator() }

	if err := fresh().Deserialize(corruptHeaderVersion(good, stateVersion+1)); err == nil ||
		!strings.Contains(err.Error(), "newer") {
		t.Errorf("newer version error = %v", err)
	}
	if err := fresh().Deserialize(corruptHeaderVersion(good, stateMinVersion-1)); err == nil ||
		!strings.Contains(err.Error(), "too old") {
		t.Errorf("too-old version error = %v", err)
	}

	bad := append([]byte(nil), good...)
	bad[3] = 'X'
	if err := fresh().Deserialize(bad); err == nil || !strings.Contains(err.Error(), "magic") {
		t.Errorf("bad magic error = %v", err)
	}

	bad = append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xFF
	if err := fresh().Deserialize(bad); err == nil || !strings.Contains(err.Error(), "CRC") {
		t.Errorf("corrupt body error = %v", err)
	}

	if err := fresh().Deserialize(good[:20]); err == nil {
		t.Errorf("truncated file: no error")
	}

	// A state with a BIOS cannot restore onto an HLE emulator and
	// vice versa.
	eb := NewEmulator()
	bios := make([]byte, 512*1024)
	if err := eb.SetBIOS("main_bios", bios); err != nil {
		t.Fatalf("SetBIOS: %v", err)
	}
	biosState, err := eb.Serialize()
	if err != nil {
		t.Fatalf("Serialize BIOS state: %v", err)
	}
	if err := fresh().Deserialize(biosState); err == nil ||
		!strings.Contains(err.Error(), "requires a real BIOS") {
		t.Errorf("BIOS state onto HLE error = %v", err)
	}
	eb2 := NewEmulator()
	if err := eb2.SetBIOS("main_bios", bios); err != nil {
		t.Fatalf("SetBIOS: %v", err)
	}
	if err := eb2.Deserialize(good); err == nil ||
		!strings.Contains(err.Error(), "HLE state") {
		t.Errorf("HLE state onto BIOS error = %v", err)
	}
}

// rebuildState reserializes a valid state after the edit hook mutates
// the parsed chunk fields, keeping framing and CRC valid, with the
// given header version. Fields the hook deletes are omitted from the
// rebuilt body.
func rebuildState(t *testing.T, e *Emulator, version uint32, edit func(chunks map[string]map[string][]byte)) []byte {
	t.Helper()
	body := e.buildStateBody()
	chunks, err := parseStateChunks(body)
	if err != nil {
		t.Fatalf("parse own body: %v", err)
	}
	edit(chunks)

	// Rebuild the body in the original chunk order by re-walking it.
	var out []byte
	bp := 0
	for bp < len(body) {
		rawTag := body[bp : bp+stateTagLen]
		tag := string(rawTag[:bytes.IndexByte(rawTag, 0)])
		clen := int(binary.BigEndian.Uint32(body[bp+stateTagLen:]))
		payload := body[bp+stateTagLen+4 : bp+stateTagLen+4+clen]
		var w fieldWriter
		fp := 0
		for fp < len(payload) {
			nlen := int(payload[fp])
			name := string(payload[fp+1 : fp+1+nlen])
			fsize := int(binary.BigEndian.Uint32(payload[fp+1+nlen:]))
			fp += 1 + nlen + 4 + fsize
			if data, ok := chunks[tag][name]; ok {
				w.raw(name, data)
			}
		}
		out = appendChunk(out, tag, w.b)
		bp += stateTagLen + 4 + clen
	}

	// Single-segment payload (count=1) is valid regardless of size.
	comp := s2.Encode(nil, out)
	payload := binary.BigEndian.AppendUint32(nil, 1)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(out)))
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(comp)))
	payload = append(payload, comp...)
	hdr := make([]byte, 0, 64)
	hdr = append(hdr, stateMagic...)
	hdr = binary.BigEndian.AppendUint32(hdr, version)
	hdr = append(hdr, 0) // empty gameID
	var zeroHash [sha256.Size]byte
	hdr = append(hdr, zeroHash[:]...)
	hdr = binary.BigEndian.AppendUint32(hdr, crc32.ChecksumIEEE(payload))
	return append(hdr, payload...)
}

// rebuildStateWithField reserializes a valid state after replacing one
// field's bytes inside one chunk, so value-level validation paths can
// be exercised.
func rebuildStateWithField(t *testing.T, e *Emulator, chunkTag, fieldName string, newData []byte) []byte {
	t.Helper()
	return rebuildState(t, e, stateVersion, func(chunks map[string]map[string][]byte) {
		if _, ok := chunks[chunkTag][fieldName]; !ok {
			t.Fatalf("field %s.%s not found", chunkTag, fieldName)
		}
		chunks[chunkTag][fieldName] = newData
	})
}

// TestDeserializeOldStateSkipsMissingFields verifies a state from an
// older format version - inside the supported version window but
// missing fields added since - loads cleanly, with the absent fields
// reading as zero values.
func TestDeserializeOldStateSkipsMissingFields(t *testing.T) {
	added := []string{
		"playMode", "tmodA", "tmodV", "decV", "audioMute",
		"vidPaused", "vidFrozen", "layer.esProbed", "layer.esDirect",
	}
	e := NewEmulator()
	old := rebuildState(t, e, stateMinVersion, func(chunks map[string]map[string][]byte) {
		for _, name := range added {
			if _, ok := chunks[tagCDMpeg][name]; !ok {
				t.Fatalf("field %s.%s not found", tagCDMpeg, name)
			}
			delete(chunks[tagCDMpeg], name)
		}
	})

	e2 := NewEmulator()
	// Junk in the live fields shows the load still overwrites them.
	e2.cdblock.mpeg.playMode = 7
	e2.cdblock.mpeg.vidPaused = true
	e2.cdblock.mpeg.vidFrozen = true
	if err := e2.Deserialize(old); err != nil {
		t.Fatalf("Deserialize old-version state: %v", err)
	}
	m := &e2.cdblock.mpeg
	if m.playMode != 0 || m.vidPaused || m.vidFrozen {
		t.Errorf("skipped fields = %d/%v/%v, want 0/false/false",
			m.playMode, m.vidPaused, m.vidFrozen)
	}
}

// TestDeserializeHostileValues verifies range validation on values that
// would be used as indices after restore.
func TestDeserializeHostileValues(t *testing.T) {
	e := NewEmulator()

	delPart50 := binary.BigEndian.AppendUint64(nil, 50)
	bad := rebuildStateWithField(t, e, tagCDState, "delPart", delPart50)
	if err := NewEmulator().Deserialize(bad); err == nil ||
		!strings.Contains(err.Error(), "delPart") {
		t.Errorf("hostile delPart error = %v", err)
	}

	way9 := binary.BigEndian.AppendUint64(nil, 9)
	bad = rebuildStateWithField(t, e, tagSH2MCore, "fetchLineWay", way9)
	if err := NewEmulator().Deserialize(bad); err == nil ||
		!strings.Contains(err.Error(), "fetchLineWay") {
		t.Errorf("hostile fetchLineWay error = %v", err)
	}

	head := binary.BigEndian.AppendUint64(nil, uint64(cdAudioQueueMax))
	bad = rebuildStateWithField(t, e, tagCDAudioQ, "audioHead", head)
	if err := NewEmulator().Deserialize(bad); err == nil ||
		!strings.Contains(err.Error(), "audioHead") {
		t.Errorf("hostile audioHead error = %v", err)
	}

	// prod == cons breaks the latch slot-ownership permutation.
	prod := binary.BigEndian.AppendUint64(nil, 1)
	bad = rebuildStateWithField(t, e, tagCDMpeg, "latch.prod", prod)
	if err := NewEmulator().Deserialize(bad); err == nil ||
		!strings.Contains(err.Error(), "latch") {
		t.Errorf("hostile latch ownership error = %v", err)
	}
}

// TestSerializeBIOSHash verifies the header carries the SHA-256 of the
// loaded BIOS image (non-zero, distinguishing it from an HLE state).
func TestSerializeBIOSHash(t *testing.T) {
	e := NewEmulator()
	bios := make([]byte, 512*1024)
	for i := range bios {
		bios[i] = byte(i)
	}
	if err := e.SetBIOS("main_bios", bios); err != nil {
		t.Fatalf("SetBIOS: %v", err)
	}
	data, err := e.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	hdr, _ := parseStateForTest(t, data)
	if want := sha256.Sum256(bios); hdr.biosHash != want {
		t.Errorf("biosHash mismatch")
	}
}
