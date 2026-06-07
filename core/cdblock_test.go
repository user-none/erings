// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"testing"
)

func TestNewCDBlockDefaults(t *testing.T) {
	cb := NewCDBlock(nil)

	if cb.hirqReq != 0 {
		t.Errorf("hirqReq = 0x%04X, want 0x0000", cb.hirqReq)
	}
	if cb.status != cdStatusNoDisc {
		t.Errorf("status = 0x%02X, want 0x%02X (NODISC)", cb.status, cdStatusNoDisc)
	}
	if cb.hirqMask != 0 {
		t.Errorf("hirqMask = 0x%04X, want 0", cb.hirqMask)
	}
	for i, v := range cb.cmd {
		if v != 0 {
			t.Errorf("cmd[%d] = 0x%04X, want 0", i, v)
		}
	}
	// CDBLOCK signature: status in high byte, 'C' in low byte
	wantRes0 := uint16(cdStatusBusy)<<8 | 'C'
	if cb.res[0] != wantRes0 {
		t.Errorf("res[0] = 0x%04X, want 0x%04X", cb.res[0], wantRes0)
	}
}

func TestCDBlockNewInitialState(t *testing.T) {
	cb := NewCDBlock(nil)

	if cb.hirqReq != 0 {
		t.Errorf("hirqReq = 0x%04X, want 0x0000", cb.hirqReq)
	}
	// HIRQMSK = 0
	if cb.hirqMask != 0 {
		t.Errorf("hirqMask = 0x%04X, want 0", cb.hirqMask)
	}
	// No data buffer
	if cb.dataBuf != nil {
		t.Error("dataBuf should be nil")
	}
	if cb.dataPos != 0 {
		t.Errorf("dataPos = %d, want 0", cb.dataPos)
	}
	// Status = NoDisc
	if cb.status != cdStatusNoDisc {
		t.Errorf("status = 0x%02X, want 0x%02X", cb.status, cdStatusNoDisc)
	}
	// Result register 0 contains CDBLOCK signature (status | 'C')
	wantRes0 := uint16(cdStatusBusy)<<8 | 'C'
	if cb.res[0] != wantRes0 {
		t.Errorf("res[0] = 0x%04X, want 0x%04X", cb.res[0], wantRes0)
	}
	// No sectors buffered
	if cb.partitions[0].sectors != nil {
		t.Error("sectors should be nil")
	}
	// Not authenticated
	if cb.authenticated {
		t.Error("authenticated should be false")
	}
	// FS not initialized
	if cb.fsInitialized {
		t.Error("fsInitialized should be false")
	}
}

func TestCDBlockHIRQREQWriteZeroClear(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.hirqReq = hirqCMOK | hirqDRDY | hirqEFLS

	// Clear CMOK by writing 0 to bit 0
	cb.Write(0x0008, cb.hirqReq & ^uint16(hirqCMOK))
	if cb.hirqReq&hirqCMOK != 0 {
		t.Error("CMOK should be cleared")
	}
	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should still be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should still be set")
	}
}

func TestCDBlockHIRQMSKRoundTrip(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.Write(0x000C, 0x0223)
	if got := cb.Read(0x000C); got != 0x0223 {
		t.Errorf("HIRQMSK = 0x%04X, want 0x0223", got)
	}
}

func TestCDBlockCRReadWrite(t *testing.T) {
	cb := NewCDBlock(nil)

	// Writes go to command registers, reads come from result registers.
	// Verify writes reach cmd and reads come from res.
	cb.Write(0x0018, 0x1111)
	cb.Write(0x001C, 0x2222)
	cb.Write(0x0020, 0x3333)

	if cb.cmd[0] != 0x1111 {
		t.Errorf("cmd[0] = 0x%04X, want 0x1111", cb.cmd[0])
	}
	if cb.cmd[1] != 0x2222 {
		t.Errorf("cmd[1] = 0x%04X, want 0x2222", cb.cmd[1])
	}
	if cb.cmd[2] != 0x3333 {
		t.Errorf("cmd[2] = 0x%04X, want 0x3333", cb.cmd[2])
	}

	// Result registers are independent - verify CDBLOCK signature present
	wantRes0 := uint16(cdStatusBusy)<<8 | 'C'
	if cb.res[0] != wantRes0 {
		t.Errorf("res[0] = 0x%04X, want 0x%04X", cb.res[0], wantRes0)
	}
}

func execCommand(cb *CDBlock, cmd uint8) {
	cb.hirqReq &= ^uint16(hirqCMOK)
	cb.cmd[0] = uint16(cmd) << 8
	cb.cmd[1] = 0
	cb.cmd[2] = 0
	cb.cmd[3] = 0
	cb.ExecuteCommand()
}

func TestCDBlockGetCDStatus(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x00)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	// Mask off PERI (0x20) and TRANS (0x40); ExecuteCommand sets peri=true
	// when boot delay is collapsed by the first command, so the status byte
	// reads as cdStatusNoDisc | 0x20.
	if got := uint8(cb.res[0]>>8) &^ uint8(0x60); got != cdStatusNoDisc {
		t.Errorf("cr[0] base status = 0x%02X, want 0x%02X", got, cdStatusNoDisc)
	}
}

func TestCDBlockGetHardwareInfo(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x01)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.res[1] != 0x0001 {
		t.Errorf("CR2 = 0x%04X, want 0x0001", cb.res[1])
	}
	if cb.res[3] != 0x0400 {
		t.Errorf("CR4 = 0x%04X, want 0x0400", cb.res[3])
	}
}

func TestCDBlockGetTOC(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x02)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	if len(cb.dataBuf) != 204 {
		t.Errorf("dataBuf len = %d, want 204", len(cb.dataBuf))
	}
	for i, v := range cb.dataBuf {
		if v != 0xFFFF {
			t.Errorf("dataBuf[%d] = 0x%04X, want 0xFFFF", i, v)
			break
		}
	}
}

func TestCDBlockGetSessionInfo(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x03)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.res[1] != 0 || cb.res[2] != 0 || cb.res[3] != 0 {
		t.Errorf("CR2-4 = %04X %04X %04X, want all 0", cb.res[1], cb.res[2], cb.res[3])
	}
}

func TestCDBlockInitCDSystem(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x04)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}
}

func TestCDBlockEndDataTransfer(t *testing.T) {
	cb := NewCDBlock(nil)
	// Set up some data first
	execCommand(cb, 0x02) // Get TOC populates dataBuf

	// Read a few words
	cb.Read(0x0000)
	cb.Read(0x0000)
	cb.Read(0x0000)

	// Now end data transfer
	cb.hirqReq &= ^uint16(hirqCMOK)
	cb.cmd[0] = 0x0600
	cb.ExecuteCommand()

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	// EndDataTransfer encodes the transferred-word count as
	// res[0] low byte (high 8 bits) | res[1] (low 16 bits).
	words := uint32(cb.res[0]&0xFF)<<16 | uint32(cb.res[1])
	if words != 204 {
		t.Errorf("transferred words = %d, want 204", words)
	}
	if cb.dataBuf != nil {
		t.Error("dataBuf should be nil after end transfer")
	}
}

func TestCDBlockEndDataTransferNoTransfer(t *testing.T) {
	cb := NewCDBlock(nil)
	// No data transfer set up - dataBuf is nil, dataPos is 0

	cb.hirqReq &= ^uint16(hirqCMOK)
	cb.cmd[0] = 0x0600
	cb.ExecuteCommand()

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	// When no transfer active, low byte of res[0] should be $FF
	if cb.res[0]&0xFF != 0xFF {
		t.Errorf("res[0] low byte = %#04x, want 0xFF", cb.res[0]&0xFF)
	}
	// CR2 (res[1]) should be $FFFF
	if cb.res[1] != 0xFFFF {
		t.Errorf("res[1] = %#04x, want 0xFFFF", cb.res[1])
	}
	// CR3 and CR4 should be 0
	if cb.res[2] != 0 {
		t.Errorf("res[2] = %#04x, want 0x0000", cb.res[2])
	}
	if cb.res[3] != 0 {
		t.Errorf("res[3] = %#04x, want 0x0000", cb.res[3])
	}
}

func TestCDBlockAuthenticateDisc(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.SetDisc(newMockDisc2Track())
	// Pre-set a bit not in the post-auth pattern to verify the auth
	// command replaces hirqReq entirely rather than OR-ing into it.
	cb.hirqReq = hirqBFUL
	execCommand(cb, 0xE0)

	want := uint16(hirqCMOK | hirqCSCT | hirqESEL | hirqEHST | hirqECPY | hirqEFLS | hirqSCDQ)
	if cb.hirqReq != want {
		t.Errorf("hirqReq = %#04x, want %#04x", cb.hirqReq, want)
	}

	// SetDisc leaves playFAD=0 and curTrack=0xFF; standardReturn
	// produces the no-track response (ctrlAddr=0xFF, trackNum=0xFF,
	// trackIdx=1, FAD=0).
	if cb.res[1] != 0xFFFF {
		t.Errorf("res[1] = %#04x, want 0xFFFF (no current track)", cb.res[1])
	}
	if cb.res[2] != 0x0100 {
		t.Errorf("res[2] = %#04x, want 0x0100 (trackIdx=1, FAD high=0)", cb.res[2])
	}
	if cb.res[3] != 0x0000 {
		t.Errorf("res[3] = %#04x, want 0x0000 (FAD=0)", cb.res[3])
	}
}

func TestCDBlockGetAuthStatus(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0xE1)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.res[1] != 0 {
		t.Errorf("auth status CR2 = 0x%04X, want 0", cb.res[1])
	}
}

func TestCDBlockChangeDirectory(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x70)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}
}

func TestCDBlockGetBufferSize(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x50)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.res[1] != 0x00C8 {
		t.Errorf("free blocks CR2 = 0x%04X, want 0x00C8", cb.res[1])
	}
	if cb.res[3] != 0x00C8 {
		t.Errorf("total blocks CR4 = 0x%04X, want 0x00C8", cb.res[3])
	}
}

func TestCDBlockGetSectorNumber(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x51)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.res[3] != 0 {
		t.Errorf("sector count CR4 = 0x%04X, want 0", cb.res[3])
	}
}

func TestCDBlockDATATRNSRead(t *testing.T) {
	cb := NewCDBlock(nil)
	// Populate via Get TOC
	execCommand(cb, 0x02)

	// Bus reads of register 0x0000 always return 0; the bus layer
	// instead routes the read through ReadDataTRNS.
	v0 := cb.ReadDataTRNS()
	v1 := cb.ReadDataTRNS()
	if v0 != 0xFFFF || v1 != 0xFFFF {
		t.Errorf("DATATRNS reads = 0x%04X, 0x%04X, want 0xFFFF, 0xFFFF", v0, v1)
	}

	// Read past end
	cb.dataPos = len(cb.dataBuf)
	if got := cb.ReadDataTRNS(); got != 0 {
		t.Errorf("DATATRNS past end = 0x%04X, want 0", got)
	}
}

func TestCDBlockDATASTATEmpty(t *testing.T) {
	cb := NewCDBlock(nil)

	// No data buffer
	if got := cb.Read(0x0004); got != 0x04 {
		t.Errorf("DATASTAT empty = 0x%04X, want 0x04 (EMP)", got)
	}

	// With data
	execCommand(cb, 0x02)
	if got := cb.Read(0x0004); got != 0 {
		t.Errorf("DATASTAT with data = 0x%04X, want 0x00", got)
	}

	// Exhaust data
	cb.dataPos = len(cb.dataBuf)
	if got := cb.Read(0x0004); got != 0x04 {
		t.Errorf("DATASTAT exhausted = 0x%04X, want 0x04 (EMP)", got)
	}
}

func TestCDBlockReadDirectory(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x71)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}
}

func TestCDBlockResetSelector(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0x48)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqESEL == 0 {
		t.Error("ESEL should be set")
	}
}

func TestCDBlockUnknownCommand(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0xFF)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set for unknown command")
	}
}

func TestCDBlockGetCopyError(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.SetDisc(newMockDisc2Track())
	execCommand(cb, 0x67)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	// CR1 has status in high byte, CR2-CR4 should be zero (no error)
	if cb.res[1] != 0 {
		t.Errorf("CR2 = 0x%04X, want 0", cb.res[1])
	}
	if cb.res[2] != 0 {
		t.Errorf("CR3 = 0x%04X, want 0", cb.res[2])
	}
	if cb.res[3] != 0 {
		t.Errorf("CR4 = 0x%04X, want 0", cb.res[3])
	}
}

// trackAt returns the primitive DiscReader.Track tuple for ts[i]. Test
// mocks store their TOC as []TrackInfo and expose it through this.
func trackAt(ts []TrackInfo, i int) (int, string, int, int, int, uint8) {
	t := ts[i]
	return t.Number, t.Type, t.Frames, t.Pregap, t.StartLBA, t.Control
}

// trackIndexAt synthesizes the single implicit-floor index entry (INDEX 01 at
// the track body) for a TrackInfo-backed fake disc.
func trackIndexAt(ts []TrackInfo, i int) (int, int) {
	t := ts[i]
	return 1, t.StartLBA + t.Pregap
}

// mockDisc implements DiscReader for testing.
type mockDisc struct {
	tracks []TrackInfo
}

func (m *mockDisc) ReadSector(lba int) ([]byte, error) {
	// Return 2352-byte sector with MODE1 header and LBA-based pattern.
	total := 0
	for _, tr := range m.tracks {
		total += tr.Frames
	}
	if lba < 0 || lba >= total {
		return nil, fmt.Errorf("lba %d out of range", lba)
	}
	data := make([]byte, 2352)
	// Sync pattern (bytes 0-11)
	data[0] = 0x00
	for i := 1; i <= 10; i++ {
		data[i] = 0xFF
	}
	data[11] = 0x00
	// Header: minute/second/frame/mode (bytes 12-15)
	data[12] = byte(lba / 4500)
	data[13] = byte((lba / 75) % 60)
	data[14] = byte(lba % 75)
	data[15] = 0x01 // MODE1
	// User data: fill with LBA low byte pattern
	pattern := byte(lba & 0xFF)
	for i := 16; i < 2064; i++ {
		data[i] = pattern
	}
	return data, nil
}

func (m *mockDisc) NumTracks() int { return len(m.tracks) }
func (m *mockDisc) Track(i int) (int, string, int, int, int, uint8) {
	return trackAt(m.tracks, i)
}
func (m *mockDisc) NumTrackIndexes(i int) int      { return 1 }
func (m *mockDisc) TrackIndex(i, n int) (int, int) { return trackIndexAt(m.tracks, i) }

func newMockDisc2Track() *mockDisc {
	return &mockDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 300, Pregap: 0, StartLBA: 0, Control: 0x41},
			{Number: 2, Type: "AUDIO", Frames: 500, Pregap: 0, StartLBA: 300, Control: 0x01},
		},
	}
}

func newCDBlockWithDisc() (*CDBlock, *mockDisc) {
	cb := NewCDBlock(nil)
	disc := newMockDisc2Track()
	cb.SetDisc(disc)
	// Wire CD device through filter 0 so data sector reads aren't gated;
	// the partition-model default of 0xFF (disconnected) blocks reads
	// and the older flat-list tests assumed an always-connected drive.
	cb.cdDeviceFilter = 0
	return cb, disc
}

// tickN advances the CD block by n sector periods at 2x speed. Each tick
// is large enough to complete one sector read, a BUSY transition, and any
// pending seek; tests written against the older one-step-per-tick model
// see "n ticks" map to "n sector reads" with all intermediate state
// transitions resolved.
func tickN(cb *CDBlock, n int) {
	for i := 0; i < n; i++ {
		cb.TickSystemCycles(uint32(cdSectorCycles2x))
	}
}

func execCommandFull(cb *CDBlock, cmd uint8, cr1lo uint8, cr2, cr3, cr4 uint16) {
	cb.hirqReq &= ^uint16(hirqCMOK)
	cb.cmd[0] = uint16(cmd)<<8 | uint16(cr1lo)
	cb.cmd[1] = cr2
	cb.cmd[2] = cr3
	cb.cmd[3] = cr4
	cb.ExecuteCommand()
}

func TestCDBlockSetDisc(t *testing.T) {
	cb := NewCDBlock(nil)
	if cb.status != cdStatusNoDisc {
		t.Errorf("initial status = 0x%02X, want 0x%02X", cb.status, cdStatusNoDisc)
	}

	disc := newMockDisc2Track()
	cb.SetDisc(disc)
	if cb.status != cdStatusPause {
		t.Errorf("status after SetDisc = 0x%02X, want 0x%02X", cb.status, cdStatusPause)
	}
	if cb.peri {
		t.Error("peri should be false after SetDisc")
	}

	cb.SetDisc(nil)
	if cb.status != cdStatusNoDisc {
		t.Errorf("status after eject = 0x%02X, want 0x%02X", cb.status, cdStatusNoDisc)
	}
}

func TestCDBlockSetDiscSetsStatus(t *testing.T) {
	cb := NewCDBlock(nil)
	disc := newMockDisc2Track()
	cb.SetDisc(disc)

	if cb.status != cdStatusPause {
		t.Errorf("status after SetDisc = 0x%02X, want 0x%02X", cb.status, cdStatusPause)
	}
	if cb.peri {
		t.Error("peri should be false after SetDisc")
	}
}

func TestCDBlockGetTOCWithDisc(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	execCommand(cb, 0x02)

	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	if len(cb.dataBuf) != 204 {
		t.Fatalf("dataBuf len = %d, want 204", len(cb.dataBuf))
	}

	// Track 1: MODE1_RAW, startLBA=0, FAD=150=0x96
	// Entry 0: [0x41, 0x00, 0x00, 0x96] -> words: 0x4100, 0x0096
	if cb.dataBuf[0] != 0x4100 {
		t.Errorf("track 1 word 0 = 0x%04X, want 0x4100", cb.dataBuf[0])
	}
	if cb.dataBuf[1] != 0x0096 {
		t.Errorf("track 1 word 1 = 0x%04X, want 0x0096", cb.dataBuf[1])
	}

	// Track 2: AUDIO, startLBA=300, FAD=450=0x01C2
	if cb.dataBuf[2] != 0x0100 {
		t.Errorf("track 2 word 0 = 0x%04X, want 0x0100", cb.dataBuf[2])
	}
	if cb.dataBuf[3] != 0x01C2 {
		t.Errorf("track 2 word 1 = 0x%04X, want 0x01C2", cb.dataBuf[3])
	}

	// Entry 99 (first track pointer): high byte = first-track ctrl/adr,
	// low byte = first track number. Track 1 is data (ctrl=0x41), so
	// word 0 = 0x4101. Word 1 is unused (0).
	if cb.dataBuf[198] != 0x4101 {
		t.Errorf("first track entry word 0 = 0x%04X, want 0x4101", cb.dataBuf[198])
	}
	if cb.dataBuf[199] != 0x0000 {
		t.Errorf("first track entry word 1 = 0x%04X, want 0x0000", cb.dataBuf[199])
	}

	// Entry 100 (last track pointer): high byte = last-track ctrl/adr,
	// low byte = last track number. Track 2 is audio (ctrl=0x01), so
	// word 0 = 0x0102. Word 1 is unused (0).
	if cb.dataBuf[200] != 0x0102 {
		t.Errorf("last track entry word 0 = 0x%04X, want 0x0102", cb.dataBuf[200])
	}
	if cb.dataBuf[201] != 0x0000 {
		t.Errorf("last track entry word 1 = 0x%04X, want 0x0000", cb.dataBuf[201])
	}

	// Entry 101 (lead-out): FAD = (300+500)+150 = 950 = 0x03B6
	if cb.dataBuf[202] != 0x4100 {
		t.Errorf("lead-out entry word 0 = 0x%04X, want 0x4100", cb.dataBuf[202])
	}
	if cb.dataBuf[203] != 0x03B6 {
		t.Errorf("lead-out entry word 1 = 0x%04X, want 0x03B6", cb.dataBuf[203])
	}

	// Unused entries should be 0xFFFF
	for i := 4; i < 198; i++ {
		if cb.dataBuf[i] != 0xFFFF {
			t.Errorf("unused entry dataBuf[%d] = 0x%04X, want 0xFFFF", i, cb.dataBuf[i])
			break
		}
	}
}

func TestCDBlockGetSessionInfoWithDisc(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// Query 0: lead-out FAD
	execCommandFull(cb, 0x03, 0x00, 0, 0, 0)
	// Lead-out: (300+500)+150 = 950 = 0x03B6
	// CR3 = (1<<8) | (950>>16) = 0x0100
	// CR4 = 950 & 0xFFFF = 0x03B6
	if cb.res[2] != 0x0100 {
		t.Errorf("session info query 0 CR3 = 0x%04X, want 0x0100", cb.res[2])
	}
	if cb.res[3] != 0x03B6 {
		t.Errorf("session info query 0 CR4 = 0x%04X, want 0x03B6", cb.res[3])
	}

	// Query >0: returns the session number echoed in CR3 high byte.
	// For query=1, CR3 = (1<<8) | 0 = 0x0100.
	execCommandFull(cb, 0x03, 0x01, 0, 0, 0)
	if cb.res[2] != 0x0100 {
		t.Errorf("session info query 1 CR3 = 0x%04X, want 0x0100", cb.res[2])
	}
	if cb.res[3] != 0x0000 {
		t.Errorf("session info query 1 CR4 = 0x%04X, want 0x0000", cb.res[3])
	}
}

func TestCDBlockAuthAndStatus(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// Auth
	execCommand(cb, 0xE0)
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set after auth")
	}

	// Get auth status. mockDisc2Track has no Saturn signature in the
	// first data sector, so detectDiscType returns 0x02 (non-Saturn
	// data) rather than 0x04 (Saturn original).
	execCommand(cb, 0xE1)
	if cb.res[1] != 0x0002 {
		t.Errorf("auth status CR2 = 0x%04X, want 0x0002 (data CD)", cb.res[1])
	}
}

func TestCDBlockAuthStatusNoDisc(t *testing.T) {
	cb := NewCDBlock(nil)
	execCommand(cb, 0xE0)
	execCommand(cb, 0xE1)
	if cb.res[1] != 0 {
		t.Errorf("auth status CR2 no disc = 0x%04X, want 0", cb.res[1])
	}
}

func TestCDBlockSetSectorLength(t *testing.T) {
	cb := NewCDBlock(nil)
	// CR1 low byte = getSectorLen, CR2 high byte = putSectorLen
	execCommandFull(cb, 0x60, 0x03, 0x0200, 0, 0)
	if cb.getSectorLen != 3 {
		t.Errorf("getSectorLen = %d, want 3", cb.getSectorLen)
	}
	if cb.putSectorLen != 2 {
		t.Errorf("putSectorLen = %d, want 2", cb.putSectorLen)
	}
}

func TestCDBlockPlayDiscAndGetSectorNum(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// PlayDisc: FAD mode, start at FAD 150 (LBA 0), 5 sectors
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0005)

	if cb.status != cdStatusBusy {
		t.Errorf("status = 0x%02X, want 0x%02X (BUSY)", cb.status, cdStatusBusy)
	}

	// 1 tick: BUSY->PLAY, 5 ticks: read 5 sectors
	tickN(cb, 6)

	if cb.hirqReq&hirqCSCT == 0 {
		t.Error("CSCT should be set after ticks")
	}

	// GetSectorNumber - count is in CR4 (res[3])
	execCommand(cb, 0x51)
	if cb.res[3] != 5 {
		t.Errorf("sector count = %d, want 5", cb.res[3])
	}
}

func TestCDBlockGetSectorData2048(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.getSectorLen = 0 // 2048 byte mode

	// Play 1 sector at FAD 150 (LBA 0)
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0000, 0x0100)
	tickN(cb, 2) // 1 BUSY->PLAY + 1 sector read

	// GetSectorData: start=0, count=1
	execCommandFull(cb, 0x61, 0x00, 0x0000, 0x0000, 0x0001)

	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	// 2048 bytes = 1024 words
	if len(cb.dataBuf) != 1024 {
		t.Fatalf("dataBuf len = %d, want 1024", len(cb.dataBuf))
	}
	// LBA 0 pattern: all bytes are 0x00
	for i, w := range cb.dataBuf {
		if w != 0x0000 {
			t.Errorf("dataBuf[%d] = 0x%04X, want 0x0000", i, w)
			break
		}
	}
}

func TestCDBlockGetSectorData2352(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.getSectorLen = 3 // 2352 byte mode

	// Play 1 sector at FAD 151 (LBA 1)
	execCommandFull(cb, 0x10, 0x80, 0x0097, 0x0000, 0x0100)
	tickN(cb, 2) // 1 BUSY->PLAY + 1 sector read

	// GetSectorData: start=0, count=1
	execCommandFull(cb, 0x61, 0x00, 0x0000, 0x0000, 0x0001)

	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	// 2352 bytes = 1176 words
	if len(cb.dataBuf) != 1176 {
		t.Fatalf("dataBuf len = %d, want 1176", len(cb.dataBuf))
	}
	// First word should be sync: 0x00FF
	if cb.dataBuf[0] != 0x00FF {
		t.Errorf("dataBuf[0] = 0x%04X, want 0x00FF", cb.dataBuf[0])
	}
}

func TestCDBlockGetThenDelete(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.getSectorLen = 0

	// Play 3 sectors
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0003)
	tickN(cb, 4) // 1 BUSY->PLAY + 3 sector reads

	// GetThenDelete: start=0, count=2
	execCommandFull(cb, 0x63, 0x00, 0x0000, 0x0000, 0x0002)

	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	// Should have 2 sectors of data (2*1024 = 2048 words)
	if len(cb.dataBuf) != 2048 {
		t.Errorf("dataBuf len = %d, want 2048", len(cb.dataBuf))
	}
	// Sector deletion is deferred until EndDataTransfer; all 3
	// sectors are still resident in the partition at this point.
	if len(cb.partitions[0].sectors) != 3 {
		t.Errorf("sectors before EndDataTransfer = %d, want 3 (delete is deferred)", len(cb.partitions[0].sectors))
	}

	execCommand(cb, 0x06)
	if len(cb.partitions[0].sectors) != 1 {
		t.Errorf("sectors after EndDataTransfer = %d, want 1 (3 - 2 deleted)", len(cb.partitions[0].sectors))
	}
}

func TestCDBlockDeleteSectorData(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// Play 5 sectors
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0005)
	tickN(cb, 6) // 1 BUSY->PLAY + 5 sector reads
	if len(cb.partitions[0].sectors) != 5 {
		t.Fatalf("sectors after play = %d, want 5", len(cb.partitions[0].sectors))
	}

	// Delete 3 starting at index 1
	execCommandFull(cb, 0x62, 0x00, 0x0001, 0x0000, 0x0003)
	if len(cb.partitions[0].sectors) != 2 {
		t.Errorf("sectors after delete = %d, want 2", len(cb.partitions[0].sectors))
	}
}

func TestCDBlockResetSelectorClearsBuffer(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// Play some sectors
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0005)
	tickN(cb, 6) // 1 BUSY->PLAY + 5 sector reads
	if len(cb.partitions[0].sectors) == 0 {
		t.Fatal("expected sectors after play")
	}

	execCommand(cb, 0x48)
	if len(cb.partitions[0].sectors) != 0 {
		t.Errorf("sectors after reset selector = %d, want 0", len(cb.partitions[0].sectors))
	}
}

func TestCDBlockGetBufferSizeWithSectors(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// Play 10 sectors (sector-count mode: bit 23 set, count in low bits)
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x000A)
	tickN(cb, 11) // 1 BUSY->PLAY + 10 sector reads

	execCommand(cb, 0x50)
	wantFree := uint16(cdMaxSectors - 10)
	if cb.res[1] != wantFree {
		t.Errorf("free = 0x%04X, want 0x%04X", cb.res[1], wantFree)
	}
	if cb.res[3] != uint16(cdMaxSectors) {
		t.Errorf("total = 0x%04X, want 0x%04X", cb.res[3], uint16(cdMaxSectors))
	}
}

func TestCDBlockGetCDStatusWithDisc(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// Set playFAD to 150 (track 1 area) and curTrack so standardReturn
	// resolves the track.
	cb.playFAD = 150
	cb.curTrack = 1
	execCommand(cb, 0x00)

	// Mask PERI (0x20) and TRANS (0x40); ExecuteCommand sets peri
	// when boot delay collapses, so the status byte includes 0x20.
	statusByte := uint8(cb.res[0]>>8) &^ uint8(0x60)
	if statusByte != cdStatusPause {
		t.Errorf("status = 0x%02X, want 0x%02X", statusByte, cdStatusPause)
	}
	// CR2 = (ctrlAddr<<8) | trackNum = 0x4101
	if cb.res[1] != 0x4101 {
		t.Errorf("CR2 = 0x%04X, want 0x4101", cb.res[1])
	}
	// FAD from CR3 (trackIdx<<8 | FAD high) and CR4 (FAD low)
	// trackIdx=1 so CR3 high byte is 0x01, FAD=150=0x000096
	fad := uint32(cb.res[2]&0xFF)<<16 | uint32(cb.res[3])
	if fad != 150 {
		t.Errorf("FAD = %d, want 150", fad)
	}
}

// isoDisc implements DiscReader with a mock ISO9660 filesystem.
// Sectors are stored by LBA in a map; missing LBAs return zeroed sectors.
type isoDisc struct {
	sectorMap map[int][]byte
	maxLBA    int
}

func newISODisc() *isoDisc {
	return &isoDisc{
		sectorMap: make(map[int][]byte),
		maxLBA:    1000,
	}
}

func (d *isoDisc) ReadSector(lba int) ([]byte, error) {
	if lba < 0 || lba >= d.maxLBA {
		return nil, fmt.Errorf("lba %d out of range", lba)
	}
	if s, ok := d.sectorMap[lba]; ok {
		cp := make([]byte, len(s))
		copy(cp, s)
		return cp, nil
	}
	return make([]byte, 2352), nil
}

func (d *isoDisc) NumTracks() int { return 1 }
func (d *isoDisc) Track(i int) (int, string, int, int, int, uint8) {
	return 1, "MODE1_RAW", d.maxLBA, 0, 0, 0x41
}
func (d *isoDisc) NumTrackIndexes(i int) int      { return 1 }
func (d *isoDisc) TrackIndex(i, n int) (int, int) { return 1, 0 }

// setSector stores a raw 2352-byte sector at the given LBA.
func (d *isoDisc) setSector(lba int, data []byte) {
	cp := make([]byte, 2352)
	copy(cp, data)
	d.sectorMap[lba] = cp
}

// le32 writes a little-endian uint32 into buf at offset.
func le32(buf []byte, offset int, val uint32) {
	buf[offset] = byte(val)
	buf[offset+1] = byte(val >> 8)
	buf[offset+2] = byte(val >> 16)
	buf[offset+3] = byte(val >> 24)
}

// buildPVD builds a Primary Volume Descriptor sector with the given root
// directory LBA and size. Returns a 2352-byte raw sector.
func buildPVD(rootLBA, rootSize uint32) []byte {
	raw := make([]byte, 2352)
	ud := raw[16:] // user data at offset 16

	ud[0] = 0x01 // type: primary
	copy(ud[1:6], []byte("CD001"))
	ud[6] = 0x01 // version

	// Root directory record at byte 156
	root := ud[156:]
	root[0] = 34 // record length (minimum)
	le32(root, 2, rootLBA)
	le32(root, 10, rootSize)
	root[25] = 0x02 // directory flag

	return raw
}

// buildDirEntry creates a single ISO9660 directory entry.
func buildDirEntry(extLBA, dataLen uint32, flags byte, name string) []byte {
	idLen := len(name)
	recLen := 33 + idLen
	if recLen%2 != 0 {
		recLen++ // padding byte
	}
	rec := make([]byte, recLen)
	rec[0] = byte(recLen)
	le32(rec, 2, extLBA)
	le32(rec, 10, dataLen)
	rec[25] = flags
	rec[32] = byte(idLen)
	copy(rec[33:], []byte(name))
	return rec
}

// buildDirSector builds a raw sector containing directory entries.
// Includes . and .. entries followed by the provided entries.
func buildDirSector(selfLBA, parentLBA uint32, entries [][]byte) []byte {
	raw := make([]byte, 2352)
	ud := raw[16:]

	// . entry
	dot := buildDirEntry(selfLBA, 2048, 0x02, "\x00")
	copy(ud[0:], dot)
	pos := len(dot)

	// .. entry
	dotdot := buildDirEntry(parentLBA, 2048, 0x02, "\x01")
	copy(ud[pos:], dotdot)
	pos += len(dotdot)

	for _, e := range entries {
		if pos+len(e) > 2048 {
			break
		}
		copy(ud[pos:], e)
		pos += len(e)
	}

	return raw
}

// newISODiscWithFiles creates a mock disc with:
//   - PVD at LBA 16, root dir at LBA 20
//   - Root dir with 2 files: "GAME.BIN" at LBA 25 (4096 bytes),
//     "README.TXT" at LBA 30 (512 bytes)
//   - File data sectors filled with recognizable patterns
func newISODiscWithFiles() (*isoDisc, uint32, uint32) {
	d := newISODisc()
	rootLBA := uint32(20)
	rootSize := uint32(2048)

	// PVD at LBA 16
	d.setSector(16, buildPVD(rootLBA, rootSize))

	// Root directory at LBA 20
	entries := [][]byte{
		buildDirEntry(25, 4096, 0x00, "GAME.BIN;1"),
		buildDirEntry(30, 512, 0x00, "README.TXT;1"),
	}
	d.setSector(int(rootLBA), buildDirSector(rootLBA, rootLBA, entries))

	// File data: GAME.BIN at LBA 25-26 (4096 bytes = 2 sectors)
	for i := 0; i < 2; i++ {
		raw := make([]byte, 2352)
		for j := 16; j < 2064; j++ {
			raw[j] = byte(0xAA)
		}
		d.setSector(25+i, raw)
	}

	// File data: README.TXT at LBA 30 (512 bytes, 1 sector)
	raw := make([]byte, 2352)
	for j := 16; j < 16+512; j++ {
		raw[j] = byte(0xBB)
	}
	d.setSector(30, raw)

	return d, rootLBA, rootSize
}

func TestCDBlockParseVolumeDescriptor(t *testing.T) {
	disc, rootLBA, rootSize := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	ok := cb.parseVolumeDescriptor()
	if !ok {
		t.Fatal("parseVolumeDescriptor returned false")
	}
	if !cb.fsInitialized {
		t.Error("fsInitialized should be true")
	}
	if cb.fsRootLBA != rootLBA {
		t.Errorf("fsRootLBA = %d, want %d", cb.fsRootLBA, rootLBA)
	}
	if cb.fsRootSize != rootSize {
		t.Errorf("fsRootSize = %d, want %d", cb.fsRootSize, rootSize)
	}
}

func TestCDBlockParseVolumeDescriptorNoDisc(t *testing.T) {
	cb := NewCDBlock(nil)
	if cb.parseVolumeDescriptor() {
		t.Error("parseVolumeDescriptor should return false with no disc")
	}
}

func TestCDBlockParseDirectory(t *testing.T) {
	disc, rootLBA, rootSize := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	files := cb.parseDirectory(rootLBA, rootSize)
	if len(files) != 2 {
		t.Fatalf("parseDirectory returned %d files, want 2", len(files))
	}

	// GAME.BIN: LBA 25 -> FAD 175
	if files[0].fad != 25+150 {
		t.Errorf("files[0].fad = %d, want %d", files[0].fad, 25+150)
	}
	if files[0].size != 4096 {
		t.Errorf("files[0].size = %d, want 4096", files[0].size)
	}
	if files[0].attr != 0 {
		t.Errorf("files[0].attr = 0x%02X, want 0x00", files[0].attr)
	}

	// README.TXT: LBA 30 -> FAD 180
	if files[1].fad != 30+150 {
		t.Errorf("files[1].fad = %d, want %d", files[1].fad, 30+150)
	}
	if files[1].size != 512 {
		t.Errorf("files[1].size = %d, want 512", files[1].size)
	}
}

func TestCDBlockChangeDirectoryRoot(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	// ChangeDirectory with fid=0 (root)
	// CR3 = 0x0000, CR4 = 0x0000 -> fid = 0
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}
	if len(cb.fsFiles) != 2 {
		t.Errorf("fsFiles len = %d, want 2", len(cb.fsFiles))
	}
}

func TestCDBlockReadDirectorySameAsChangeDir(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	// ReadDirectory with fid=0
	execCommandFull(cb, 0x71, 0x00, 0x0000, 0x00FF, 0xFFFF)

	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}
	if len(cb.fsFiles) != 2 {
		t.Errorf("fsFiles len = %d, want 2", len(cb.fsFiles))
	}
}

func TestCDBlockGetFileSystemScope(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	// First populate fsFiles via ChangeDirectory
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)

	// GetFileSystemScope
	execCommand(cb, 0x72)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}

	// CR2 = number of files
	if cb.res[1] != 2 {
		t.Errorf("file count CR2 = %d, want 2", cb.res[1])
	}
	// CR3 = 0x0100 (drive number high byte = 1)
	if cb.res[2] != 0x0100 {
		t.Errorf("CR3 = 0x%04X, want 0x0100", cb.res[2])
	}
	// CR4 = 0x0002 (first file ID; IDs start at 2 since 0/1 are
	// reserved for current/parent directory).
	if cb.res[3] != 0x0002 {
		t.Errorf("first file CR4 = 0x%04X, want 0x0002", cb.res[3])
	}
}

func TestCDBlockGetFileInfoAll(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	// Populate fsFiles
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)

	// GetFileInfo with fid=0xFFFFFF (all files)
	// CR3 = 0x00FF, CR4 = 0xFFFF -> fid = 0x00FFFFFF
	execCommandFull(cb, 0x73, 0x00, 0x0000, 0x00FF, 0xFFFF)

	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	// 2 files * 6 words = 12 words
	if len(cb.dataBuf) != 12 {
		t.Fatalf("dataBuf len = %d, want 12", len(cb.dataBuf))
	}

	// File 0 (GAME.BIN): FAD=175, size=4096, attr=0
	fad0 := uint32(cb.dataBuf[0])<<16 | uint32(cb.dataBuf[1])
	if fad0 != 175 {
		t.Errorf("file 0 FAD = %d, want 175", fad0)
	}
	size0 := uint32(cb.dataBuf[2])<<16 | uint32(cb.dataBuf[3])
	if size0 != 4096 {
		t.Errorf("file 0 size = %d, want 4096", size0)
	}
	if cb.dataBuf[4] != 0x0000 {
		t.Errorf("file 0 attr word = 0x%04X, want 0x0000", cb.dataBuf[4])
	}

	// File 1 (README.TXT): FAD=180, size=512, attr=0
	fad1 := uint32(cb.dataBuf[6])<<16 | uint32(cb.dataBuf[7])
	if fad1 != 180 {
		t.Errorf("file 1 FAD = %d, want 180", fad1)
	}
	size1 := uint32(cb.dataBuf[8])<<16 | uint32(cb.dataBuf[9])
	if size1 != 512 {
		t.Errorf("file 1 size = %d, want 512", size1)
	}
}

func TestCDBlockReadFile(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)
	cb.getSectorLen = 0 // 2048 mode

	// Populate fsFiles via ChangeDirectory
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)

	// ReadFile for file index 0 -> fid = 2 (0-based index + 2)
	execCommandFull(cb, 0x74, 0x00, 0x0000, 0x0000, 0x0002)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}

	// 1 tick BUSY->PLAY + 2 ticks for 2 sectors
	tickN(cb, 3)

	if cb.hirqReq&hirqCSCT == 0 {
		t.Error("CSCT should be set")
	}

	// GAME.BIN is 4096 bytes = 2 sectors
	if len(cb.partitions[0].sectors) != 2 {
		t.Fatalf("sectors = %d, want 2", len(cb.partitions[0].sectors))
	}

	// Verify sector FADs
	if cb.partitions[0].sectors[0].fad != 25+150 {
		t.Errorf("sector[0].fad = %d, want %d", cb.partitions[0].sectors[0].fad, 25+150)
	}
	if cb.partitions[0].sectors[1].fad != 26+150 {
		t.Errorf("sector[1].fad = %d, want %d", cb.partitions[0].sectors[1].fad, 26+150)
	}

	// GetSectorData and verify content
	execCommandFull(cb, 0x61, 0x00, 0x0000, 0x0000, 0x0001)
	if len(cb.dataBuf) != 1024 {
		t.Fatalf("dataBuf len = %d, want 1024", len(cb.dataBuf))
	}
	// All bytes should be 0xAA -> words 0xAAAA
	for i, w := range cb.dataBuf {
		if w != 0xAAAA {
			t.Errorf("dataBuf[%d] = 0x%04X, want 0xAAAA", i, w)
			break
		}
	}
}

func TestCDBlockReadFileNoDisc(t *testing.T) {
	cb := NewCDBlock(nil)

	execCommandFull(cb, 0x75, 0x00, 0x0000, 0x0000, 0x0002)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}
	if len(cb.partitions[0].sectors) != 0 {
		t.Errorf("sectors = %d, want 0", len(cb.partitions[0].sectors))
	}
}

func TestCDBlockReadFileInvalidFID(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	// Populate fsFiles
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)

	// ReadFile with fid=99 (out of range)
	execCommandFull(cb, 0x75, 0x00, 0x0000, 0x0000, 0x0063)

	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS should be set")
	}
	if len(cb.partitions[0].sectors) != 0 {
		t.Errorf("sectors = %d, want 0", len(cb.partitions[0].sectors))
	}
}

func TestCDBlockFSFullSequence(t *testing.T) {
	// Full BIOS-like sequence: ChangeDir -> GetFileSystemScope ->
	// GetFileInfo -> ReadFile -> GetSectorData
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)
	cb.getSectorLen = 0

	// 1. ChangeDirectory to root
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)
	if len(cb.fsFiles) != 2 {
		t.Fatalf("after ChangeDir: fsFiles = %d, want 2", len(cb.fsFiles))
	}

	// 2. GetFileSystemScope - file count is in CR2 (res[1])
	execCommand(cb, 0x72)
	fileCount := cb.res[1]
	if fileCount != 2 {
		t.Fatalf("GetFileSystemScope: count = %d, want 2", fileCount)
	}

	// 3. GetFileInfo (all)
	execCommandFull(cb, 0x73, 0x00, 0x0000, 0x00FF, 0xFFFF)
	if len(cb.dataBuf) != 12 {
		t.Fatalf("GetFileInfo: dataBuf len = %d, want 12", len(cb.dataBuf))
	}
	// End data transfer
	execCommand(cb, 0x06)

	// 4. ReadFile for README.TXT (fid=3, index 1)
	execCommandFull(cb, 0x74, 0x00, 0x0000, 0x0000, 0x0003)
	tickN(cb, 2) // 1 BUSY->PLAY + 1 sector read
	if len(cb.partitions[0].sectors) != 1 {
		t.Fatalf("ReadFile: sectors = %d, want 1", len(cb.partitions[0].sectors))
	}

	// 5. GetSectorData
	execCommandFull(cb, 0x61, 0x00, 0x0000, 0x0000, 0x0001)
	if len(cb.dataBuf) != 1024 {
		t.Fatalf("GetSectorData: dataBuf len = %d, want 1024", len(cb.dataBuf))
	}
	// First 256 words should be 0xBBBB (512 bytes of 0xBB)
	for i := 0; i < 256; i++ {
		if cb.dataBuf[i] != 0xBBBB {
			t.Errorf("dataBuf[%d] = 0x%04X, want 0xBBBB", i, cb.dataBuf[i])
			break
		}
	}
}

func TestCDBlockNewFSDefaults(t *testing.T) {
	cb := NewCDBlock(nil)

	if cb.fsInitialized {
		t.Error("fsInitialized should be false")
	}
	if cb.fsFiles != nil {
		t.Error("fsFiles should be nil")
	}
	if cb.fsRootLBA != 0 {
		t.Errorf("fsRootLBA = %d, want 0", cb.fsRootLBA)
	}
	if cb.fsRootSize != 0 {
		t.Errorf("fsRootSize = %d, want 0", cb.fsRootSize)
	}
}

func TestCDBlockInitCDSystemClearsFSState(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)

	// Populate FS state
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)

	// InitCDSystem
	execCommand(cb, 0x04)
	if cb.fsInitialized {
		t.Error("fsInitialized should be false after InitCDSystem")
	}
	if cb.fsFiles != nil {
		t.Error("fsFiles should be nil after InitCDSystem")
	}
}

func TestCDBlockChangeDirectorySubdir(t *testing.T) {
	d := newISODisc()
	rootLBA := uint32(20)
	rootSize := uint32(2048)
	subLBA := uint32(22)

	// PVD
	d.setSector(16, buildPVD(rootLBA, rootSize))

	// Root dir with one subdirectory
	rootEntries := [][]byte{
		buildDirEntry(subLBA, 2048, 0x02, "SUBDIR;1"),
	}
	d.setSector(int(rootLBA), buildDirSector(rootLBA, rootLBA, rootEntries))

	// Subdir with one file
	subEntries := [][]byte{
		buildDirEntry(40, 1024, 0x00, "FILE.DAT;1"),
	}
	d.setSector(int(subLBA), buildDirSector(subLBA, rootLBA, subEntries))

	cb := NewCDBlock(nil)
	cb.SetDisc(d)

	// ChangeDirectory to root
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)
	if len(cb.fsFiles) != 1 {
		t.Fatalf("root has %d files, want 1", len(cb.fsFiles))
	}
	if cb.fsFiles[0].attr&0x02 == 0 {
		t.Error("SUBDIR should have directory flag")
	}

	// ChangeDirectory to SUBDIR (fid=2, first file)
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x0000, 0x0002)
	if len(cb.fsFiles) != 1 {
		t.Fatalf("subdir has %d files, want 1", len(cb.fsFiles))
	}
	if cb.fsFiles[0].fad != 40+150 {
		t.Errorf("FILE.DAT fad = %d, want %d", cb.fsFiles[0].fad, 40+150)
	}
	if cb.fsFiles[0].size != 1024 {
		t.Errorf("FILE.DAT size = %d, want 1024", cb.fsFiles[0].size)
	}
}

func TestCDBlockTickBusyTransition(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0003)

	if cb.status != cdStatusBusy {
		t.Fatalf("status = 0x%02X, want 0x%02X (BUSY)", cb.status, cdStatusBusy)
	}
	// PlayDisc sets pendingStatus to SEEK first; SEEK->PLAY happens
	// when the seek arrives at seekFAD. With startFAD == playFAD == 150
	// the seek completes on the first tick, transitioning to PLAY.
	if cb.pendingStatus != cdStatusSeek {
		t.Fatalf("pendingStatus = 0x%02X, want 0x%02X (SEEK)", cb.pendingStatus, cdStatusSeek)
	}

	tickN(cb, 1)
	if cb.status != cdStatusPlay {
		t.Errorf("status after tick = 0x%02X, want 0x%02X (PLAY)", cb.status, cdStatusPlay)
	}
}

func TestCDBlockTickSeek(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	// SeekDisc to FAD 200
	execCommandFull(cb, 0x11, 0x80, 0x00C8, 0x0000, 0x0000)

	if cb.status != cdStatusBusy {
		t.Fatalf("status = 0x%02X, want BUSY", cb.status)
	}

	// One sector-period tick is enough cycles for the BUSY transition,
	// the seek across 200 FAD steps, and the SEEK -> PAUSE transition
	// once playFAD reaches the target.
	tickN(cb, 1)
	if cb.status != cdStatusPause {
		t.Errorf("status after tick = 0x%02X, want PAUSE", cb.status)
	}
	if cb.playFAD != 200 {
		t.Errorf("playFAD = %d, want 200", cb.playFAD)
	}
}

func TestCDBlockTickPlayOneSector(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0000, 0x0100)

	// One sector-period tick: BUSY -> PLAY -> read one sector.
	tickN(cb, 1)
	if cb.status != cdStatusPlay {
		t.Fatalf("status = 0x%02X, want PLAY", cb.status)
	}
	if len(cb.partitions[0].sectors) != 1 {
		t.Errorf("sectors = %d, want 1", len(cb.partitions[0].sectors))
	}
	if cb.partitions[0].sectors[0].fad != 150 {
		t.Errorf("sector FAD = %d, want 150", cb.partitions[0].sectors[0].fad)
	}
}

func TestCDBlockTickPlayCompletes(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0003)
	tickN(cb, 4) // 1 BUSY->PLAY + 3 sector reads

	if cb.status != cdStatusPause {
		t.Errorf("status = 0x%02X, want PAUSE", cb.status)
	}
	if cb.hirqReq&hirqPEND == 0 {
		t.Error("PEND should be set when play completes")
	}
	if len(cb.partitions[0].sectors) != 3 {
		t.Errorf("sectors = %d, want 3", len(cb.partitions[0].sectors))
	}
}

func TestCDBlockTickBufferFull(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	// Pre-fill buffer to capacity
	for i := 0; i < cdMaxSectors; i++ {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{data: make([]byte, 2352), fad: uint32(i)})
	}

	// Start a play
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0005)
	tickN(cb, 2)

	// Buffer-full does not change the drive's logical status (it stays
	// PLAY); the BFUL flag is raised and sector reads are gated until
	// the host drains a partition slot.
	if cb.status != cdStatusPlay {
		t.Errorf("status = 0x%02X, want PLAY (BFUL gates reads but not status)", cb.status)
	}
	if cb.hirqReq&hirqBFUL == 0 {
		t.Error("BFUL should be set when buffer full")
	}
}

func TestCDBlockTickBufferFullResume(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	// Pre-fill to max
	for i := 0; i < cdMaxSectors; i++ {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{data: make([]byte, 2352), fad: uint32(i)})
	}

	// Start play; status remains PLAY while BFUL gates reads.
	execCommandFull(cb, 0x10, 0x80, 0x0096, 0x0080, 0x0005)
	tickN(cb, 2)

	if cb.status != cdStatusPlay {
		t.Fatalf("status = 0x%02X, want PLAY (BFUL does not stop the drive)", cb.status)
	}
	if cb.hirqReq&hirqBFUL == 0 {
		t.Fatal("BFUL should be set with full partition")
	}

	// Free up partition slots; the next tick should produce a sector read.
	cb.partitions[0].sectors = cb.partitions[0].sectors[:cdMaxSectors-2]
	before := len(cb.partitions[0].sectors)
	tickN(cb, 1)
	if len(cb.partitions[0].sectors) <= before {
		t.Errorf("no sector read after partition drain: before=%d after=%d", before, len(cb.partitions[0].sectors))
	}
}

func TestCDBlockTickSCDQ(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.bootDelay = 0
	cb.initialized = false
	cb.scdqCounter = 0
	cb.hirqReq = 0

	// Before initialization, SCDQ stays clear regardless of cycle count.
	cb.TickSystemCycles(uint32(cdSCDQCycles2x * 2))
	if cb.hirqReq&hirqSCDQ != 0 {
		t.Error("SCDQ should not be set before initialization")
	}

	// Once initialized, SCDQ asserts on each Q-frame counter expiry.
	// Tick by one full SCDQ period to guarantee a single edge.
	cb.initialized = true
	cb.scdqCounter = cdSCDQCycles2x
	cb.hirqReq = 0
	cb.TickSystemCycles(uint32(cdSCDQCycles2x))
	if cb.hirqReq&hirqSCDQ == 0 {
		t.Error("SCDQ should be set after one Q-frame period when initialized")
	}
}

func TestCDBlockTickNoDisc(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.hirqReq = 0

	cb.TickSystemCycles(210)
	if cb.hirqReq&hirqSCDQ != 0 {
		t.Error("SCDQ should not be set with no disc")
	}
}

func TestCDBlockPERIOnCommandCycle(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// peri starts false right after SetDisc.
	if cb.peri {
		t.Fatal("peri should be false after SetDisc")
	}

	// The first command collapses the boot delay and turns peri on as
	// part of the post-boot state. peri stays asserted until the next
	// periodic-status iteration toggles it back.
	execCommand(cb, 0x00)
	if !cb.peri {
		t.Error("peri should be true after first command (boot collapse)")
	}
}

func TestCDBlockTickResultsReadGatesResponse(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.playFAD = 150

	// Issue a command to set initialized=true
	execCommand(cb, 0x00)

	// With resultsRead=true + initialized=true, Tick writes result registers
	cb.resultsRead = true
	cb.res[0] = 0
	cb.res[1] = 0
	cb.TickSystemCycles(210)
	if cb.res[0] == 0 && cb.res[1] == 0 {
		t.Error("Tick should write result registers when resultsRead and initialized are true")
	}

	// With resultsRead=false, Tick should not write result registers
	cb.resultsRead = false
	cb.res[0] = 0xBEEF
	cb.res[1] = 0xBEEF
	cb.res[2] = 0xBEEF
	cb.res[3] = 0xBEEF
	cb.TickSystemCycles(210)
	if cb.res[0] != 0xBEEF || cb.res[1] != 0xBEEF {
		t.Errorf("Tick should not overwrite result registers when resultsRead is false, got res[0]=0x%04X res[1]=0x%04X", cb.res[0], cb.res[1])
	}

	// SCDQ should be set when initialized with disc present and buffer space available
	if cb.hirqReq&hirqSCDQ == 0 {
		t.Error("SCDQ should be set when initialized with disc and buffer space")
	}
}

func TestCDBlockPERIInCR1(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.playFAD = 150

	// With peri=true, CR1 should have 0x2000 (bit 13)
	cb.peri = true
	cb.standardReturn()
	if cb.res[0]&0x2000 == 0 {
		t.Errorf("CR1 should have 0x2000 when peri is true, got 0x%04X", cb.res[0])
	}

	// With peri=false, CR1 should not have 0x2000
	cb.peri = false
	cb.standardReturn()
	if cb.res[0]&0x2000 != 0 {
		t.Errorf("CR1 should not have 0x2000 when peri is false, got 0x%04X", cb.res[0])
	}
}

func TestCDBlockHIRQReadNoSCDQReassert(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.bootDelay = 0
	cb.initialized = true
	cb.scdqCounter = cdSCDQCycles2x

	// Tick by one Q-frame period to assert SCDQ.
	cb.TickSystemCycles(uint32(cdSCDQCycles2x))
	if cb.hirqReq&hirqSCDQ == 0 {
		t.Fatal("SCDQ should be set after one Q-frame period")
	}

	// Clear SCDQ via write-0-to-clear.
	cb.Write(0x0008, cb.hirqReq & ^uint16(hirqSCDQ))

	// Reading HIRQ must not re-assert SCDQ.
	val := cb.Read(0x0008)
	if val&hirqSCDQ != 0 {
		t.Errorf("HIRQ read re-asserted SCDQ, got 0x%04X", val)
	}
}

func TestCDBlockSignatureProtectedFromTick(t *testing.T) {
	// SetDisc overwrites signature with status when HIRQ flags are set.
	// Test without disc to verify signature protection before SetDisc.
	cb := NewCDBlock(nil)

	// Verify CDBLOCK signature is present
	if cb.Read(0x0018) != 0x0043 {
		t.Fatalf("res[0] = 0x%04X, want 0x0043 ('C')", cb.Read(0x0018))
	}

	// Read CR4 to simulate BIOS signature check (sets resultsRead=true)
	cb.Read(0x0024)
	if !cb.resultsRead {
		t.Fatal("resultsRead should be true after CR4 read")
	}

	// Tick fires before any command - signature must be preserved
	// (no disc, so Tick's periodic section is skipped)
	cb.TickSystemCycles(210)
	cr1 := cb.Read(0x0018)
	if cr1 != 0x0043 {
		t.Errorf("CDBLOCK signature corrupted by Tick: res[0]=0x%04X, want 0x0043", cr1)
	}
}

func TestCDBlockResultsReadGuard(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// resultsRead starts false
	if cb.resultsRead {
		t.Fatal("resultsRead should be false initially")
	}

	// setResponse clears resultsRead
	cb.resultsRead = true
	cb.setResponse(0, 0, 0)
	if cb.resultsRead {
		t.Error("resultsRead should be false after setResponse")
	}

	// CR4 read sets resultsRead
	cb.Read(0x0024)
	if !cb.resultsRead {
		t.Error("resultsRead should be true after CR4 read")
	}

	// Tick with resultsRead=true but initialized=false does NOT write
	// (protects CDBLOCK signature before first command)
	cb.resultsRead = true
	cb.res[0] = 0xBEEF
	cb.TickSystemCycles(210)
	if cb.res[0] != 0xBEEF {
		t.Errorf("Tick should not overwrite before first command, got 0x%04X", cb.res[0])
	}

	// After a command, initialized=true; now Tick writes
	execCommand(cb, 0x00) // GetCDStatus sets initialized=true
	cb.Read(0x0024)       // set resultsRead=true
	cb.res[0] = 0
	cb.TickSystemCycles(210)
	if cb.res[0] == 0 {
		t.Error("Tick should write results when resultsRead and initialized are true")
	}

	// Tick with resultsRead=false does not overwrite
	cb.resultsRead = false
	cb.res[0] = 0xDEAD
	cb.TickSystemCycles(210)
	if cb.res[0] != 0xDEAD {
		t.Errorf("Tick should not overwrite when resultsRead is false, got 0x%04X", cb.res[0])
	}
}

func TestCDBlockCDSpeed(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// Default speed is 2x
	if cb.cdSpeed != 2 {
		t.Errorf("default cdSpeed = %d, want 2", cb.cdSpeed)
	}

	// Bit 5 (0x20) requests a change to the init flags; only then do
	// the speed bits apply (bit 4 = 1x). 0xFF is the no-change sentinel.

	// Change requested (bit5) + bit4 -> 1x
	execCommandFull(cb, 0x04, 0x30, 0, 0, 0)
	if cb.cdSpeed != 1 {
		t.Errorf("cdSpeed after init with bit5+bit4 = %d, want 1", cb.cdSpeed)
	}

	// Change requested (bit5) without bit4 -> 2x
	execCommandFull(cb, 0x04, 0x20, 0, 0, 0)
	if cb.cdSpeed != 2 {
		t.Errorf("cdSpeed after init with bit5 only = %d, want 2", cb.cdSpeed)
	}

	// Bit 4 set but no change bit (bit5) -> speed unchanged (still 2x)
	execCommandFull(cb, 0x04, 0x10, 0, 0, 0)
	if cb.cdSpeed != 2 {
		t.Errorf("cdSpeed after init with bit4 only = %d, want 2 (unchanged)", cb.cdSpeed)
	}

	// 0xFF no-change sentinel preserves the current speed.
	execCommandFull(cb, 0x04, 0x30, 0, 0, 0) // set 1x
	execCommandFull(cb, 0x04, 0xFF, 0, 0, 0) // no-change
	if cb.cdSpeed != 1 {
		t.Errorf("cdSpeed after 0xFF no-change = %d, want 1 (preserved)", cb.cdSpeed)
	}
}

func TestCDBlockSCDQBufferFull(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.bootDelay = 0
	cb.initialized = true

	// SCDQ is the Q-frame (subcode) timer and is independent of the
	// data partition state on real hardware. Fill the partition to
	// capacity and confirm SCDQ still asserts on its normal cadence.
	for i := 0; i < cdMaxSectors; i++ {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{data: make([]byte, 2352), fad: uint32(i)})
	}
	cb.scdqCounter = cdSCDQCycles2x
	cb.hirqReq = 0
	cb.TickSystemCycles(uint32(cdSCDQCycles2x))
	if cb.hirqReq&hirqSCDQ == 0 {
		t.Error("SCDQ should fire even when the partition is full")
	}
}

func TestCDBlockGetSubcodeQ(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	cb.playFAD = 150 // track 1, data track, FAD 150

	// GetSubcode type 0 (Q channel)
	execCommandFull(cb, 0x20, 0x00, 0, 0, 0)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set")
	}
	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	if len(cb.dataBuf) != 5 {
		t.Fatalf("dataBuf len = %d, want 5", len(cb.dataBuf))
	}

	// Word 0: ctrl_adr=0x41 (data), track=0x01 (BCD)
	if cb.dataBuf[0] != 0x4101 {
		t.Errorf("SubQ word 0 = 0x%04X, want 0x4101", cb.dataBuf[0])
	}

	// Word 1: index=0x01, relative minute (BCD)
	// relFAD = 150 - 150 = 0 -> rel M=0 S=0 F=0
	if cb.dataBuf[1] != 0x0100 {
		t.Errorf("SubQ word 1 = 0x%04X, want 0x0100", cb.dataBuf[1])
	}

	// Word 2: relative S=0x00, relative F=0x00
	if cb.dataBuf[2] != 0x0000 {
		t.Errorf("SubQ word 2 = 0x%04X, want 0x0000", cb.dataBuf[2])
	}

	// FAD 150 absolute: 150/75=2s, 150%75=0f -> M=0x00, S=0x02, F=0x00
	// Word 3: 0x00 | abs_M = 0x0000
	if cb.dataBuf[3] != 0x0000 {
		t.Errorf("SubQ word 3 = 0x%04X, want 0x0000", cb.dataBuf[3])
	}
	// Word 4: abs_S=0x02 | abs_F=0x00
	if cb.dataBuf[4] != 0x0200 {
		t.Errorf("SubQ word 4 = 0x%04X, want 0x0200", cb.dataBuf[4])
	}

	// CR2 should report 5 (data length)
	if cb.res[1] != 5 {
		t.Errorf("CR2 = 0x%04X, want 5", cb.res[1])
	}
}

func TestCDBlockGetSubcodeRW(t *testing.T) {
	cb, _ := newCDBlockWithDisc()

	// GetSubcode type 1 (R-W channel)
	execCommandFull(cb, 0x20, 0x01, 0, 0, 0)

	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set")
	}
	if len(cb.dataBuf) != 12 {
		t.Fatalf("dataBuf len = %d, want 12", len(cb.dataBuf))
	}
	for i, w := range cb.dataBuf {
		if w != 0 {
			t.Errorf("dataBuf[%d] = 0x%04X, want 0", i, w)
			break
		}
	}
}

func TestCDBlockGetSubcodeQAudioTrack(t *testing.T) {
	cb, _ := newCDBlockWithDisc()
	// Track 2 is AUDIO, starts at LBA 300 = FAD 450
	// Set playFAD to FAD 525 (75 frames into track 2)
	cb.playFAD = 525

	execCommandFull(cb, 0x20, 0x00, 0, 0, 0)

	if len(cb.dataBuf) != 5 {
		t.Fatalf("dataBuf len = %d, want 5", len(cb.dataBuf))
	}

	// Word 0: ctrl_adr=0x01 (audio), track=0x02 (BCD)
	if cb.dataBuf[0] != 0x0102 {
		t.Errorf("SubQ word 0 = 0x%04X, want 0x0102", cb.dataBuf[0])
	}

	// relFAD = 525 - 450 = 75 -> 1 second exactly -> M=0, S=1, F=0
	// Word 1: index=0x01, rel_M=0x00
	if cb.dataBuf[1] != 0x0100 {
		t.Errorf("SubQ word 1 = 0x%04X, want 0x0100", cb.dataBuf[1])
	}
	// Word 2: rel_S=0x01, rel_F=0x00
	if cb.dataBuf[2] != 0x0100 {
		t.Errorf("SubQ word 2 = 0x%04X, want 0x0100", cb.dataBuf[2])
	}

	// FAD 525 absolute: 525/75=7s, 525%75=0f -> M=0, S=7, F=0
	// Word 3: 0x00 | abs_M=0x00
	if cb.dataBuf[3] != 0x0000 {
		t.Errorf("SubQ word 3 = 0x%04X, want 0x0000", cb.dataBuf[3])
	}
	// Word 4: abs_S=0x07, abs_F=0x00
	if cb.dataBuf[4] != 0x0700 {
		t.Errorf("SubQ word 4 = 0x%04X, want 0x0700", cb.dataBuf[4])
	}
}

func TestFad2BCD(t *testing.T) {
	// FAD 0 = 00:00:00
	m, s, f := fad2bcd(0)
	if m != 0x00 || s != 0x00 || f != 0x00 {
		t.Errorf("fad2bcd(0) = %02X:%02X:%02X, want 00:00:00", m, s, f)
	}

	// FAD 150 = 00:02:00 (150/75=2s)
	m, s, f = fad2bcd(150)
	if m != 0x00 || s != 0x02 || f != 0x00 {
		t.Errorf("fad2bcd(150) = %02X:%02X:%02X, want 00:02:00", m, s, f)
	}

	// FAD 4500 = 01:00:00 (4500/75=60s=1m)
	m, s, f = fad2bcd(4500)
	if m != 0x01 || s != 0x00 || f != 0x00 {
		t.Errorf("fad2bcd(4500) = %02X:%02X:%02X, want 01:00:00", m, s, f)
	}

	// FAD 4576 = 01:01:01 (4576 = 4500 + 75 + 1)
	m, s, f = fad2bcd(4576)
	if m != 0x01 || s != 0x01 || f != 0x01 {
		t.Errorf("fad2bcd(4576) = %02X:%02X:%02X, want 01:01:01", m, s, f)
	}
}

func TestToBCD(t *testing.T) {
	if v := toBCD(0); v != 0x00 {
		t.Errorf("toBCD(0) = 0x%02X, want 0x00", v)
	}
	if v := toBCD(9); v != 0x09 {
		t.Errorf("toBCD(9) = 0x%02X, want 0x09", v)
	}
	if v := toBCD(10); v != 0x10 {
		t.Errorf("toBCD(10) = 0x%02X, want 0x10", v)
	}
	if v := toBCD(99); v != 0x99 {
		t.Errorf("toBCD(99) = 0x%02X, want 0x99", v)
	}
}

func TestCDBlockInitCycleBusy(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.SetDisc(newMockDisc2Track())

	// Init ($04) with disc present sets BUSY with pending Pause.
	execCommand(cb, 0x04)
	if cb.status != cdStatusBusy {
		t.Fatalf("after Init: status = 0x%02X, want 0x%02X (BUSY)", cb.status, cdStatusBusy)
	}

	// GetCopyError ($67) does not mutate status; the BUSY transition
	// is still pending and the response should still report BUSY in the
	// status byte (mask off PERI 0x20 and TRANS 0x40).
	execCommand(cb, 0x67)
	if got := uint8(cb.res[0]>>8) &^ uint8(0x60); got != cdStatusBusy {
		t.Errorf("GetCopyError response base status = 0x%02X, want 0x%02X (BUSY)", got, cdStatusBusy)
	}

	// One sector-period tick is more than enough to expire the init
	// delay; status transitions to Pause.
	tickN(cb, 1)
	if cb.status != cdStatusPause {
		t.Fatalf("after Tick: status = 0x%02X, want 0x%02X (Pause)", cb.status, cdStatusPause)
	}

	// GetCDStatus ($00) should now report Pause (peri may be set).
	execCommand(cb, 0x00)
	if got := uint8(cb.res[0]>>8) &^ uint8(0x60); got != cdStatusPause {
		t.Errorf("GetCDStatus response base status = 0x%02X, want 0x%02X (Pause)", got, cdStatusPause)
	}
}

func TestCDBlockAbortFileBusyTransition(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.SetDisc(newMockDisc2Track())

	// Confirm starting status is Pause.
	if cb.status != cdStatusPause {
		t.Fatalf("initial status = 0x%02X, want 0x%02X (Pause)", cb.status, cdStatusPause)
	}

	// AbortFile from idle: status stays Pause; AbortFile sets EFLS so
	// any in-progress file read terminates with a fired completion bit.
	execCommand(cb, 0x75)
	if cb.status != cdStatusPause {
		t.Fatalf("after idle AbortFile: status = 0x%02X, want 0x%02X (Pause)", cb.status, cdStatusPause)
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("AbortFile should set EFLS")
	}

	// AbortFile from a playing state: drive stops (playing=false) and
	// status is forced to Pause directly (no intermediate Busy).
	cb.playing = true
	cb.status = cdStatusPlay
	execCommand(cb, 0x75)
	if cb.playing {
		t.Error("playing should be false after AbortFile")
	}
	if cb.status != cdStatusPause {
		t.Errorf("after AbortFile from PLAY: status = 0x%02X, want 0x%02X", cb.status, cdStatusPause)
	}
}

// covCDBlock returns a post-boot CD block with a data disc loaded and
// the device wired through filter 0. Tests that need a different
// configuration adjust state directly.
func covCDBlock() *CDBlock {
	cb, _ := newCDBlockWithDisc()
	cb.bootDelay = 0
	return cb
}

// --- Filter configuration commands ---

func TestCDBlockFilterRangeRoundTrip(t *testing.T) {
	cb := covCDBlock()

	// SetFilterRange filter 3: fadStart=0x012345, fadRange=0x0789AB.
	// CR1 low = fadStart high byte, CR2 = fadStart low 16, CR3 high =
	// filter, CR3 low = fadRange high byte, CR4 = fadRange low 16.
	execCommandFull(cb, 0x40, 0x01, 0x2345, 0x0307, 0x89AB)
	if cb.filters[3].fadStart != 0x012345 {
		t.Errorf("fadStart = 0x%X, want 0x012345", cb.filters[3].fadStart)
	}
	if cb.filters[3].fadRange != 0x0789AB {
		t.Errorf("fadRange = 0x%X, want 0x0789AB", cb.filters[3].fadRange)
	}

	// GetFilterRange echoes the values back.
	execCommandFull(cb, 0x41, 0x00, 0x0000, 0x0300, 0x0000)
	if cb.res[1] != 0x2345 {
		t.Errorf("get fadStart low = 0x%04X, want 0x2345", cb.res[1])
	}
	if cb.res[0]&0xFF != 0x01 {
		t.Errorf("get fadStart high = 0x%02X, want 0x01", cb.res[0]&0xFF)
	}
	if cb.res[2]&0xFF != 0x07 {
		t.Errorf("get fadRange high = 0x%02X, want 0x07", cb.res[2]&0xFF)
	}
	if cb.res[3] != 0x89AB {
		t.Errorf("get fadRange low = 0x%04X, want 0x89AB", cb.res[3])
	}
}

func TestCDBlockFilterSubheaderRoundTrip(t *testing.T) {
	cb := covCDBlock()

	// SetFilterSubheaderConditions filter 5: chanNum=0x12, fileNum=0x34,
	// smmask=0x56, cimask=0x78, smval=0x9A, cival=0xBC.
	execCommandFull(cb, 0x42, 0x12, 0x5678, 0x0534, 0x9ABC)
	f := &cb.filters[5]
	if f.chanNum != 0x12 || f.fileNum != 0x34 ||
		f.smmask != 0x56 || f.cimask != 0x78 ||
		f.smval != 0x9A || f.cival != 0xBC {
		t.Errorf("filter[5] subheader = chan=%X file=%X smmask=%X cimask=%X smval=%X cival=%X",
			f.chanNum, f.fileNum, f.smmask, f.cimask, f.smval, f.cival)
	}

	execCommandFull(cb, 0x43, 0x00, 0x0000, 0x0500, 0x0000)
	if cb.res[1] != 0x5678 {
		t.Errorf("get smmask|cimask = 0x%04X, want 0x5678", cb.res[1])
	}
	if cb.res[2] != 0x0034 {
		t.Errorf("get fileNum = 0x%04X, want 0x0034", cb.res[2])
	}
	if cb.res[3] != 0x9ABC {
		t.Errorf("get smval|cival = 0x%04X, want 0x9ABC", cb.res[3])
	}
	if cb.res[0]&0xFF != 0x12 {
		t.Errorf("get chanNum = 0x%02X, want 0x12", cb.res[0]&0xFF)
	}
}

func TestCDBlockFilterModeRoundTrip(t *testing.T) {
	cb := covCDBlock()

	// Set filter 2 mode = 0x0F (all subheader conditions enabled).
	execCommandFull(cb, 0x44, 0x0F, 0x0000, 0x0200, 0x0000)
	if cb.filters[2].mode != 0x0F {
		t.Errorf("filter[2].mode = 0x%02X, want 0x0F", cb.filters[2].mode)
	}

	// Get echoes mode in CR1 low byte.
	execCommandFull(cb, 0x45, 0x00, 0x0000, 0x0200, 0x0000)
	if cb.res[0]&0xFF != 0x0F {
		t.Errorf("get mode = 0x%02X, want 0x0F", cb.res[0]&0xFF)
	}
}

func TestCDBlockFilterModeInitFlag(t *testing.T) {
	cb := covCDBlock()

	// Pre-populate filter 1 with non-default state.
	cb.filters[1].mode = 0x0F
	cb.filters[1].fadStart = 0x100
	cb.filters[1].fadRange = 0x200
	cb.filters[1].fileNum = 0x55
	cb.filters[1].trueConn = 5
	cb.filters[1].falseConn = 6

	// Set mode with bit 7 -> initialize filter (clears conditions but
	// preserves the connection topology).
	execCommandFull(cb, 0x44, 0x80, 0x0000, 0x0100, 0x0000)
	f := &cb.filters[1]
	if f.mode != 0 || f.fadStart != 0 || f.fadRange != 0 || f.fileNum != 0 {
		t.Errorf("filter[1] not cleared by init flag: mode=%X start=%X range=%X file=%X",
			f.mode, f.fadStart, f.fadRange, f.fileNum)
	}
	if f.trueConn != 5 || f.falseConn != 6 {
		t.Errorf("filter[1] connections changed by init: true=%d false=%d", f.trueConn, f.falseConn)
	}
}

func TestCDBlockFilterConnectionRoundTrip(t *testing.T) {
	cb := covCDBlock()

	// SetFilterConnection filter 4: which=0x03 (set both true and false).
	// CR2 high byte = trueConn, CR2 low byte = falseConn.
	execCommandFull(cb, 0x46, 0x03, 0x0708, 0x0400, 0x0000)
	if cb.filters[4].trueConn != 7 {
		t.Errorf("trueConn = %d, want 7", cb.filters[4].trueConn)
	}
	if cb.filters[4].falseConn != 8 {
		t.Errorf("falseConn = %d, want 8", cb.filters[4].falseConn)
	}

	// which=0x01 only updates trueConn.
	execCommandFull(cb, 0x46, 0x01, 0x0A0B, 0x0400, 0x0000)
	if cb.filters[4].trueConn != 0x0A {
		t.Errorf("trueConn after which=0x01 = %d, want 10", cb.filters[4].trueConn)
	}
	if cb.filters[4].falseConn != 8 {
		t.Errorf("falseConn changed by which=0x01: %d, want 8", cb.filters[4].falseConn)
	}

	execCommandFull(cb, 0x47, 0x00, 0x0000, 0x0400, 0x0000)
	if cb.res[1] != (uint16(0x0A)<<8 | uint16(8)) {
		t.Errorf("get connection = 0x%04X, want 0x%04X", cb.res[1], (uint16(0x0A)<<8 | uint16(8)))
	}
}

// --- CD device routing ---

func TestCDBlockCDDeviceConnectionRoundTrip(t *testing.T) {
	cb := covCDBlock()

	// SetCDDeviceConnection: filter number in CR3 high byte.
	execCommandFull(cb, 0x30, 0x00, 0x0000, 0x0500, 0x0000)
	if cb.cdDeviceFilter != 5 {
		t.Errorf("cdDeviceFilter = %d, want 5", cb.cdDeviceFilter)
	}

	execCommandFull(cb, 0x31, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[2]>>8 != 5 {
		t.Errorf("get device connection = %d, want 5", cb.res[2]>>8)
	}
}

func TestCDBlockGetLastBufferDest(t *testing.T) {
	cb := covCDBlock()
	cb.lastBufferDest = 7

	execCommandFull(cb, 0x32, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[2]>>8 != 7 {
		t.Errorf("last buffer dest = %d, want 7", cb.res[2]>>8)
	}
}

// --- Sector data commands ---

func TestCDBlockGetActualSize(t *testing.T) {
	cb := covCDBlock()
	cb.calcSize = 0x012345

	execCommandFull(cb, 0x53, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[1] != 0x2345 {
		t.Errorf("calcSize low = 0x%04X, want 0x2345", cb.res[1])
	}
	if cb.res[0]&0xFF != 0x01 {
		t.Errorf("calcSize high = 0x%02X, want 0x01", cb.res[0]&0xFF)
	}
}

func TestCDBlockGetSectorInfoValid(t *testing.T) {
	cb := covCDBlock()
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
		fad: 0x012345, fileNum: 0x12, chanNum: 0x34,
		submode: 0x56, codinfo: 0x78,
	})

	execCommandFull(cb, 0x54, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[1] != 0x2345 {
		t.Errorf("FAD low = 0x%04X, want 0x2345", cb.res[1])
	}
	if cb.res[0]&0xFF != 0x01 {
		t.Errorf("FAD high = 0x%02X, want 0x01", cb.res[0]&0xFF)
	}
	if cb.res[2] != 0x1234 {
		t.Errorf("file|chan = 0x%04X, want 0x1234", cb.res[2])
	}
	if cb.res[3] != 0x5678 {
		t.Errorf("submode|codinfo = 0x%04X, want 0x5678", cb.res[3])
	}
}

func TestCDBlockGetSectorInfoInvalid(t *testing.T) {
	cb := covCDBlock()

	// Empty partition; secNum=0 is out of range. Reject path sets
	// res[0] to 0xFF00 and clears the rest.
	execCommandFull(cb, 0x54, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[0]>>8 != 0xFF {
		t.Errorf("reject status = 0x%02X, want 0xFF", cb.res[0]>>8)
	}
	if cb.res[1] != 0 || cb.res[2] != 0 || cb.res[3] != 0 {
		t.Errorf("reject CR2-4 = 0x%04X 0x%04X 0x%04X, want all 0", cb.res[1], cb.res[2], cb.res[3])
	}
}

func TestCDBlockPutSectorDataBasic(t *testing.T) {
	cb := covCDBlock()
	cb.putSectorLen = 0 // 2048-byte mode

	// PutSectorData: dest partition in CR3 high byte, count in CR4.
	execCommandFull(cb, 0x64, 0x00, 0x0000, 0x0200, 0x0001)
	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set after PutSectorData accept")
	}
	if cb.putSectorsRemaining != 1 {
		t.Errorf("putSectorsRemaining = %d, want 1", cb.putSectorsRemaining)
	}
	if cb.putBufNum != 2 {
		t.Errorf("putBufNum = %d, want 2", cb.putBufNum)
	}
}

func TestCDBlockPutSectorDataOverflow(t *testing.T) {
	cb := covCDBlock()

	// Fill partition 0 to capacity.
	for i := 0; i < cdMaxSectors; i++ {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors,
			bufferedSector{data: make([]byte, 2352)})
	}

	// PutSectorData asking for 1 more sector exceeds cdMaxSectors and
	// must be rejected.
	execCommandFull(cb, 0x64, 0x00, 0x0000, 0x0100, 0x0001)
	if cb.res[0]>>8 != 0xFF {
		t.Errorf("reject status = 0x%02X, want 0xFF", cb.res[0]>>8)
	}
	if cb.putSectorsRemaining != 0 {
		t.Errorf("putSectorsRemaining = %d, want 0 (rejected)", cb.putSectorsRemaining)
	}
}

func TestCDBlockPutSectorDataCompletes(t *testing.T) {
	cb := covCDBlock()
	cb.putSectorLen = 0 // 2048-byte mode

	execCommandFull(cb, 0x64, 0x00, 0x0000, 0x0300, 0x0001)
	if cb.putSectorsRemaining != 1 {
		t.Fatal("setup: PutSectorData not accepted")
	}

	// Write 2048 bytes via DATATRNS (1024 16-bit words).
	for i := 0; i < 1024; i++ {
		cb.Write(0x0000, uint16(i))
	}

	if cb.putSectorsRemaining != 0 {
		t.Errorf("putSectorsRemaining = %d, want 0 after full sector write", cb.putSectorsRemaining)
	}
	if got := len(cb.partitions[3].sectors); got != 1 {
		t.Errorf("partition 3 sectors = %d, want 1", got)
	}
	if cb.lastBufferDest != 3 {
		t.Errorf("lastBufferDest = %d, want 3", cb.lastBufferDest)
	}
	if cb.hirqReq&hirqCSCT == 0 {
		t.Error("CSCT should be set when put sector completes")
	}
}

func TestCDBlockCopySectorDataBasic(t *testing.T) {
	cb := covCDBlock()
	src := bufferedSector{
		data: make([]byte, 2352), size: 2352, fad: 0x100,
	}
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, src, src, src)

	// CopySectorData: dstFilter in CR1 low, startIdx in CR2,
	// srcBuf in CR3 high, count in CR4.
	execCommandFull(cb, 0x65, 0x05, 0x0001, 0x0000, 0x0002)

	// Source partition still has 3 sectors (copy is non-destructive).
	if got := len(cb.partitions[0].sectors); got != 3 {
		t.Errorf("source sectors = %d, want 3 (copy is non-destructive)", got)
	}
	// Default filter 5 routes to partition 5; 2 sectors copied.
	if got := len(cb.partitions[5].sectors); got != 2 {
		t.Errorf("dest partition 5 sectors = %d, want 2", got)
	}
	if cb.hirqReq&hirqECPY == 0 {
		t.Error("ECPY should be set after copy")
	}
}

func TestCDBlockCopySectorDataOverflow(t *testing.T) {
	cb := covCDBlock()
	for i := 0; i < cdMaxSectors; i++ {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors,
			bufferedSector{data: make([]byte, 2352)})
	}

	// Asking to copy 1 sector when the buffer is full is rejected.
	execCommandFull(cb, 0x65, 0x05, 0x0000, 0x0000, 0x0001)
	if cb.res[0]>>8 != 0xFF {
		t.Errorf("reject status = 0x%02X, want 0xFF", cb.res[0]>>8)
	}
	if cb.hirqReq&hirqECPY == 0 {
		t.Error("ECPY should still be set on overflow reject")
	}
}

func TestCDBlockMoveSectorData(t *testing.T) {
	cb := covCDBlock()
	src := bufferedSector{data: make([]byte, 2352), size: 2352, fad: 0x100}
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, src, src, src, src)

	// Move 3 sectors starting at index 1 from partition 0 through
	// filter 6 (-> partition 6 by default).
	execCommandFull(cb, 0x66, 0x06, 0x0001, 0x0000, 0x0003)

	// Source partition has 1 sector left (kept index 0).
	if got := len(cb.partitions[0].sectors); got != 1 {
		t.Errorf("source sectors after move = %d, want 1", got)
	}
	if got := len(cb.partitions[6].sectors); got != 3 {
		t.Errorf("dest partition 6 sectors = %d, want 3", got)
	}
}

// --- Other 0% commands ---

func TestCDBlockScanDisc(t *testing.T) {
	cb := covCDBlock()
	cb.playing = true

	execCommand(cb, 0x12)

	if cb.playing {
		t.Error("playing should be false after ScanDisc")
	}
	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK should be set after ScanDisc")
	}
}

// --- I/O method gaps ---

func TestCDBlockReadDataTRNS32(t *testing.T) {
	cb := covCDBlock()
	cb.dataBuf = []uint16{0x1234, 0x5678, 0xAABB}
	cb.dataPos = 0

	v := cb.ReadDataTRNS32()
	if v != 0x12345678 {
		t.Errorf("ReadDataTRNS32 = 0x%08X, want 0x12345678", v)
	}
	if cb.dataPos != 2 {
		t.Errorf("dataPos = %d, want 2 after 32-bit read", cb.dataPos)
	}
}

func TestCDBlockWrite32(t *testing.T) {
	cb := covCDBlock()

	// Write32 at CR1 (offset 0x0018) splits into CR1 and CR2.
	cb.Write32(0x0018, 0x11112222)
	if cb.cmd[0] != 0x1111 {
		t.Errorf("cmd[0] = 0x%04X, want 0x1111", cb.cmd[0])
	}
	if cb.cmd[1] != 0x2222 {
		t.Errorf("cmd[1] = 0x%04X, want 0x2222", cb.cmd[1])
	}
}

// --- routeSector filter conditions ---

func TestCDBlockRouteFADRange(t *testing.T) {
	cb := covCDBlock()
	cb.filters[0].mode = 0x40 // FAD range condition
	cb.filters[0].fadStart = 200
	cb.filters[0].fadRange = 100 // covers FAD 200..299
	cb.filters[0].trueConn = 1
	cb.filters[0].falseConn = 0xFF

	in := bufferedSector{fad: 250, data: make([]byte, 2352), size: 2352}
	cb.routeSector(in, 0)
	if got := len(cb.partitions[1].sectors); got != 1 {
		t.Errorf("in-range sector count = %d, want 1", got)
	}

	out := bufferedSector{fad: 999, data: make([]byte, 2352), size: 2352}
	cb.routeSector(out, 0)
	if got := len(cb.partitions[1].sectors); got != 1 {
		t.Errorf("out-of-range sector should not route: count = %d, want 1", got)
	}
}

func TestCDBlockRouteSubheaderFileNum(t *testing.T) {
	cb := covCDBlock()
	cb.filters[0].mode = 0x01 // file number condition (subheader)
	cb.filters[0].fileNum = 5
	cb.filters[0].trueConn = 2
	cb.filters[0].falseConn = 0xFF

	match := bufferedSector{isMode2: true, fileNum: 5, data: make([]byte, 2352)}
	cb.routeSector(match, 0)
	if got := len(cb.partitions[2].sectors); got != 1 {
		t.Errorf("matched sector count = %d, want 1", got)
	}

	miss := bufferedSector{isMode2: true, fileNum: 9, data: make([]byte, 2352)}
	cb.routeSector(miss, 0)
	if got := len(cb.partitions[2].sectors); got != 1 {
		t.Errorf("non-matching sector should not route: count = %d, want 1", got)
	}
}

func TestCDBlockRouteSubheaderChanNum(t *testing.T) {
	cb := covCDBlock()
	cb.filters[0].mode = 0x02
	cb.filters[0].chanNum = 3
	cb.filters[0].trueConn = 2
	cb.filters[0].falseConn = 0xFF

	match := bufferedSector{isMode2: true, chanNum: 3, data: make([]byte, 2352)}
	cb.routeSector(match, 0)
	if got := len(cb.partitions[2].sectors); got != 1 {
		t.Errorf("channel-match sector count = %d, want 1", got)
	}

	miss := bufferedSector{isMode2: true, chanNum: 4, data: make([]byte, 2352)}
	cb.routeSector(miss, 0)
	if got := len(cb.partitions[2].sectors); got != 1 {
		t.Errorf("channel-miss sector should not route: count = %d, want 1", got)
	}
}

func TestCDBlockRouteSubheaderSubmode(t *testing.T) {
	cb := covCDBlock()
	cb.filters[0].mode = 0x04
	cb.filters[0].smmask = 0x60
	cb.filters[0].smval = 0x40
	cb.filters[0].trueConn = 2
	cb.filters[0].falseConn = 0xFF

	match := bufferedSector{isMode2: true, submode: 0x4F, data: make([]byte, 2352)}
	cb.routeSector(match, 0)
	if got := len(cb.partitions[2].sectors); got != 1 {
		t.Errorf("submode-match count = %d, want 1", got)
	}

	miss := bufferedSector{isMode2: true, submode: 0x20, data: make([]byte, 2352)}
	cb.routeSector(miss, 0)
	if got := len(cb.partitions[2].sectors); got != 1 {
		t.Errorf("submode-miss should not route: count = %d, want 1", got)
	}
}

func TestCDBlockRouteSubheaderReverse(t *testing.T) {
	cb := covCDBlock()
	// File number condition + reverse: matching file numbers are
	// rejected, non-matching pass.
	cb.filters[0].mode = 0x01 | 0x10
	cb.filters[0].fileNum = 5
	cb.filters[0].trueConn = 2
	cb.filters[0].falseConn = 0xFF

	rejected := bufferedSector{isMode2: true, fileNum: 5, data: make([]byte, 2352)}
	cb.routeSector(rejected, 0)
	if got := len(cb.partitions[2].sectors); got != 0 {
		t.Errorf("reverse-rejected sector should not route: count = %d, want 0", got)
	}

	passed := bufferedSector{isMode2: true, fileNum: 9, data: make([]byte, 2352)}
	cb.routeSector(passed, 0)
	if got := len(cb.partitions[2].sectors); got != 1 {
		t.Errorf("reverse-passed sector count = %d, want 1", got)
	}
}

func TestCDBlockRouteFalseConnectionChain(t *testing.T) {
	cb := covCDBlock()
	// Filter 0 rejects (file mismatch) and chains to filter 1, which
	// has no conditions and accepts to partition 4.
	cb.filters[0].mode = 0x01
	cb.filters[0].fileNum = 5
	cb.filters[0].trueConn = 0xFF
	cb.filters[0].falseConn = 1

	cb.filters[1].mode = 0
	cb.filters[1].trueConn = 4
	cb.filters[1].falseConn = 0xFF

	sec := bufferedSector{isMode2: true, fileNum: 9, data: make([]byte, 2352)}
	cb.routeSector(sec, 0)
	if got := len(cb.partitions[4].sectors); got != 1 {
		t.Errorf("false-chain target count = %d, want 1", got)
	}
}

// --- resetSelectors flag bits ---

func TestCDBlockResetSelectorAllPartitions(t *testing.T) {
	cb := covCDBlock()
	cb.partitions[0].sectors = []bufferedSector{{}}
	cb.partitions[1].sectors = []bufferedSector{{}, {}}
	cb.partitions[7].sectors = []bufferedSector{{}}

	// Bit 2: clear all partitions.
	execCommandFull(cb, 0x48, 0x04, 0x0000, 0x0000, 0x0000)
	for i := range cb.partitions {
		if len(cb.partitions[i].sectors) != 0 {
			t.Errorf("partition %d not cleared: %d sectors", i, len(cb.partitions[i].sectors))
		}
	}
}

func TestCDBlockResetSelectorFilterConditions(t *testing.T) {
	cb := covCDBlock()
	cb.filters[3].mode = 0x4F
	cb.filters[3].fadStart = 0x100
	cb.filters[3].fadRange = 0x200
	cb.filters[3].fileNum = 5
	cb.filters[3].chanNum = 6
	cb.filters[3].smmask = 0x80
	cb.filters[3].cimask = 0x40

	// Bit 4: reset all filter conditions.
	execCommandFull(cb, 0x48, 0x10, 0x0000, 0x0000, 0x0000)
	f := &cb.filters[3]
	if f.mode != 0 || f.fadStart != 0 || f.fadRange != 0 ||
		f.fileNum != 0 || f.chanNum != 0 || f.smmask != 0 || f.cimask != 0 {
		t.Errorf("filter conditions not cleared: mode=%X start=%X range=%X file=%d chan=%d smmask=%X cimask=%X",
			f.mode, f.fadStart, f.fadRange, f.fileNum, f.chanNum, f.smmask, f.cimask)
	}
}

func TestCDBlockResetSelectorDisconnectCDInput(t *testing.T) {
	cb := covCDBlock()
	cb.cdDeviceFilter = 5

	// Bit 5: disconnect CD input.
	execCommandFull(cb, 0x48, 0x20, 0x0000, 0x0000, 0x0000)
	if cb.cdDeviceFilter != 0xFF {
		t.Errorf("cdDeviceFilter = %d, want 0xFF", cb.cdDeviceFilter)
	}
}

func TestCDBlockResetSelectorResetTrueConn(t *testing.T) {
	cb := covCDBlock()
	for i := range cb.filters {
		cb.filters[i].trueConn = 0xAA
	}

	// Bit 6: reset true-output connectors to default (filter N -> partition N).
	execCommandFull(cb, 0x48, 0x40, 0x0000, 0x0000, 0x0000)
	for i := range cb.filters {
		if cb.filters[i].trueConn != uint8(i) {
			t.Errorf("filter[%d].trueConn = %d, want %d", i, cb.filters[i].trueConn, i)
		}
	}
}

func TestCDBlockResetSelectorDisconnectFalseConn(t *testing.T) {
	cb := covCDBlock()
	for i := range cb.filters {
		cb.filters[i].falseConn = 0x55
	}

	// Bit 7: disconnect all false-output connectors.
	execCommandFull(cb, 0x48, 0x80, 0x0000, 0x0000, 0x0000)
	for i := range cb.filters {
		if cb.filters[i].falseConn != 0xFF {
			t.Errorf("filter[%d].falseConn = %d, want 0xFF", i, cb.filters[i].falseConn)
		}
	}
}

// --- sectorSlice length modes ---

func TestCDBlockSectorSliceMode2336(t *testing.T) {
	cb := covCDBlock()
	cb.getSectorLen = 1 // 2336-byte mode (skip sync, keep header onward)
	raw := make([]byte, 2352)
	for i := range raw {
		raw[i] = byte(i)
	}
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
		data: raw, size: 2352, userOffset: 16, userSize: 2048,
	})

	execCommandFull(cb, 0x61, 0x00, 0x0000, 0x0000, 0x0001)

	// 2336 bytes / 2 = 1168 16-bit words.
	if len(cb.dataBuf) != 1168 {
		t.Errorf("dataBuf len = %d, want 1168 (2336 bytes)", len(cb.dataBuf))
	}
	// First word is bytes 16,17 from raw -> 0x1011 (BE pack).
	if cb.dataBuf[0] != 0x1011 {
		t.Errorf("dataBuf[0] = 0x%04X, want 0x1011", cb.dataBuf[0])
	}
}

func TestCDBlockSectorSliceMode2340(t *testing.T) {
	cb := covCDBlock()
	cb.getSectorLen = 2 // 2340-byte mode (skip sync, keep header onward)
	raw := make([]byte, 2352)
	for i := range raw {
		raw[i] = byte(i)
	}
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
		data: raw, size: 2352, userOffset: 16, userSize: 2048,
	})

	execCommandFull(cb, 0x61, 0x00, 0x0000, 0x0000, 0x0001)

	// 2340 / 2 = 1170 words.
	if len(cb.dataBuf) != 1170 {
		t.Errorf("dataBuf len = %d, want 1170 (2340 bytes)", len(cb.dataBuf))
	}
	// First word is bytes 12,13 -> 0x0C0D.
	if cb.dataBuf[0] != 0x0C0D {
		t.Errorf("dataBuf[0] = 0x%04X, want 0x0C0D", cb.dataBuf[0])
	}
}

func TestCDBlockSectorSliceMode2352(t *testing.T) {
	cb := covCDBlock()
	cb.getSectorLen = 3 // raw 2352-byte mode
	raw := make([]byte, 2352)
	for i := range raw {
		raw[i] = byte(i)
	}
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
		data: raw, size: 2352, userOffset: 16, userSize: 2048,
	})

	execCommandFull(cb, 0x61, 0x00, 0x0000, 0x0000, 0x0001)

	// 2352 / 2 = 1176 words.
	if len(cb.dataBuf) != 1176 {
		t.Errorf("dataBuf len = %d, want 1176 (2352 bytes)", len(cb.dataBuf))
	}
	// First word is bytes 0,1 of raw sync -> 0x0001.
	if cb.dataBuf[0] != 0x0001 {
		t.Errorf("dataBuf[0] = 0x%04X, want 0x0001", cb.dataBuf[0])
	}
}

// --- cmdSeekDisc edges ---

func TestCDBlockSeekDiscFADPause(t *testing.T) {
	cb := covCDBlock()
	cb.playFAD = 300

	// SeekDisc with FAD=0xFFFFFF: pause at current position, no seek.
	execCommandFull(cb, 0x11, 0xFF, 0xFFFF, 0x0000, 0x0000)
	if cb.pendingStatus != cdStatusPause {
		t.Errorf("pendingStatus = 0x%02X, want 0x%02X (Pause)", cb.pendingStatus, cdStatusPause)
	}
	if cb.seeking {
		t.Error("seeking should be false for FAD=0xFFFFFF (pause-in-place)")
	}
	if cb.playFAD != 300 {
		t.Errorf("playFAD = %d, want 300 (unchanged)", cb.playFAD)
	}
}

func TestCDBlockSeekDiscInvalidTrack(t *testing.T) {
	cb := covCDBlock()

	// Track-mode seek to track 99 (does not exist) parks in STANDBY.
	execCommandFull(cb, 0x11, 0x00, 0x6300, 0x0000, 0x0000)
	if cb.pendingStatus != cdStatusStandby {
		t.Errorf("pendingStatus = 0x%02X, want 0x%02X (Standby)", cb.pendingStatus, cdStatusStandby)
	}
	if cb.seeking {
		t.Error("seeking should be false for unknown track")
	}
}

// --- cmdGetFileInfo all-files mode ---

func TestCDBlockGetFileInfoAllFiles(t *testing.T) {
	disc, _, _ := newISODiscWithFiles()
	cb := NewCDBlock(nil)
	cb.SetDisc(disc)
	cb.cdDeviceFilter = 0
	execCommandFull(cb, 0x70, 0x00, 0x0000, 0x00FF, 0xFFFF)

	// GetFileInfo with fid=0xFFFFFF returns all files via DATATRNS.
	execCommandFull(cb, 0x73, 0x00, 0x0000, 0x00FF, 0xFFFF)

	if cb.hirqReq&hirqDRDY == 0 {
		t.Error("DRDY should be set after GetFileInfo all-files")
	}
	// 2 files * 6 words/file = 12 words.
	if len(cb.dataBuf) != 12 {
		t.Errorf("dataBuf len = %d, want 12 (2 files * 6 words)", len(cb.dataBuf))
	}
}

func TestFadToIndex(t *testing.T) {
	// Multi-indexed audio track: INDEX 01 at body, INDEX 02/03 within it.
	tr := &trackEntry{
		index01FAD: 1150,
		indexes: []TrackIndex{
			{Number: 1, FAD: 1150},
			{Number: 2, FAD: 1750},
			{Number: 3, FAD: 2500},
		},
	}
	cases := []struct {
		fad  uint32
		want uint8
	}{
		{1000, 0}, // below INDEX 01 -> implicit pregap floor
		{1150, 1}, // body start
		{1700, 1},
		{1750, 2},
		{2499, 2},
		{2500, 3},
		{9999, 3},
	}
	for _, c := range cases {
		if got := fadToIndex(tr, c.fad); got != c.want {
			t.Errorf("fadToIndex(%d) = %d, want %d", c.fad, got, c.want)
		}
	}

	// A track with no index entries reports the floor.
	empty := &trackEntry{}
	if got := fadToIndex(empty, 5000); got != 0 {
		t.Errorf("fadToIndex(empty) = %d, want 0", got)
	}
}

func TestPregapBoundaryReporting(t *testing.T) {
	// Track 1 data (no pregap); track 2 audio with a 150-frame pregap, so its
	// pregap occupies FAD [1150, 1300) and its body (INDEX 01) is at 1300.
	cb := &CDBlock{}
	cb.trackCache = []trackEntry{
		{index01FAD: 150, pregapStartFAD: 150, number: 1, control: 0x41,
			indexes: []TrackIndex{{Number: 1, FAD: 150}}},
		{index01FAD: 1300, pregapStartFAD: 1150, number: 2, isAudio: true, control: 0x01,
			indexes: []TrackIndex{{Number: 1, FAD: 1300}}},
	}

	// A FAD in track 2's pregap is attributed to track 2 (not track 1).
	tr := cb.trackAt(1150)
	if tr == nil || tr.number != 2 {
		t.Fatalf("trackAt(1150) = %v, want track 2", tr)
	}
	if got := fadToIndex(tr, 1150); got != 0 {
		t.Errorf("pregap index = %d, want 0", got)
	}
	// The body is index 1.
	if got := fadToIndex(cb.trackAt(1300), 1300); got != 1 {
		t.Errorf("body index = %d, want 1", got)
	}

	// SubQ in the pregap: track 2 (0x02), index 0, relative MSF counting down
	// to INDEX 01. 100 frames before the body -> 0:01:25 (1s 25f).
	cb.playFAD = 1200
	q := cb.buildSubCodeQ()
	if q[0] != 0x0102 {
		t.Errorf("SubQ word0 = 0x%04X, want 0x0102 (audio, track 2)", q[0])
	}
	if q[1] != 0x0000 {
		t.Errorf("SubQ word1 = 0x%04X, want 0x0000 (index 0, rel M=0)", q[1])
	}
	if q[2] != 0x0125 {
		t.Errorf("SubQ word2 = 0x%04X, want 0x0125 (rel S=1 F=25, countdown)", q[2])
	}
}
