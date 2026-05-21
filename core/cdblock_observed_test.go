// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"testing"
)

// This file contains regression tests that pin observed hardware behavior
// of the CD block. Each test exercises a specific behavior that diverges
// either from the official CD block manual or from a naive register-shape
// reading of the spec. The descriptive comment on each test states what
// is being pinned and why a regression would matter (typical game-visible
// symptom or hardware nuance).

// obsDisc is a flexible mock DiscReader for observed-behavior tests.
// Synthesized sectors are MODE1 raw layout for data tracks and silent
// raw audio for AUDIO tracks. A sectorMap may override individual LBAs
// (used to inject Saturn signatures, ISO9660 volume descriptors, or
// directory extents).
type obsDisc struct {
	tracks    []TrackInfo
	sectorMap map[int][]byte
}

func (d *obsDisc) NumTracks() int { return len(d.tracks) }
func (d *obsDisc) Track(i int) (int, string, int, int, int, uint8) {
	return trackAt(d.tracks, i)
}

func (d *obsDisc) ReadSector(lba int) ([]byte, error) {
	if d.sectorMap != nil {
		if raw, ok := d.sectorMap[lba]; ok {
			out := make([]byte, 2352)
			copy(out, raw)
			return out, nil
		}
	}
	total := 0
	for _, tr := range d.tracks {
		total += tr.Frames
	}
	if lba < 0 || lba >= total {
		return nil, fmt.Errorf("lba %d out of range", lba)
	}
	tr := d.trackAt(lba)
	data := make([]byte, 2352)
	if tr != nil && tr.Type == "AUDIO" {
		return data, nil
	}
	data[0] = 0x00
	for i := 1; i <= 10; i++ {
		data[i] = 0xFF
	}
	data[11] = 0x00
	data[12] = byte(lba / 4500)
	data[13] = byte((lba / 75) % 60)
	data[14] = byte(lba % 75)
	data[15] = 0x01
	return data, nil
}

func (d *obsDisc) trackAt(lba int) *TrackInfo {
	off := lba
	for i := range d.tracks {
		if off < d.tracks[i].Frames {
			return &d.tracks[i]
		}
		off -= d.tracks[i].Frames
	}
	return nil
}

// obsDataOnly returns a 2-track mock with a single MODE1 data track and
// nothing else. Used by tests that just need a data disc loaded.
func obsDataOnly() *obsDisc {
	return &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 600, Pregap: 0, StartLBA: 0, Control: 0x41},
		},
	}
}

// obsAudioPlusData returns a 2-track mock: data track 1 then audio track 2.
func obsAudioPlusData() *obsDisc {
	return &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 300, Pregap: 0, StartLBA: 0, Control: 0x41},
			{Number: 2, Type: "AUDIO", Frames: 500, Pregap: 0, StartLBA: 300, Control: 0x01},
		},
	}
}

// obsThreeTrack returns a 3-track mock used to verify track-mode end
// position resolves to the start of the *next* track, not the start of
// the addressed track.
func obsThreeTrack() *obsDisc {
	return &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 200, Pregap: 0, StartLBA: 0, Control: 0x41},
			{Number: 2, Type: "AUDIO", Frames: 300, Pregap: 0, StartLBA: 200, Control: 0x01},
			{Number: 3, Type: "AUDIO", Frames: 400, Pregap: 0, StartLBA: 500, Control: 0x01},
		},
	}
}

// obsMixedCtrl returns a disc with mixed CTRL/ADR values (audio + data
// + audio) so TOC tests can verify the per-track control byte rather
// than a hardcoded value.
func obsMixedCtrl() *obsDisc {
	return &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "AUDIO", Frames: 100, Pregap: 0, StartLBA: 0, Control: 0x01},
			{Number: 2, Type: "MODE1_RAW", Frames: 200, Pregap: 0, StartLBA: 100, Control: 0x41},
			{Number: 3, Type: "AUDIO", Frames: 100, Pregap: 0, StartLBA: 300, Control: 0x01},
		},
	}
}

// obsAudioOnly returns an all-audio (CD-DA) disc.
func obsAudioOnly() *obsDisc {
	return &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "AUDIO", Frames: 500, Pregap: 0, StartLBA: 0, Control: 0x01},
			{Number: 2, Type: "AUDIO", Frames: 500, Pregap: 0, StartLBA: 500, Control: 0x01},
		},
	}
}

// obsSaturnDisc returns a single-track data disc with the Saturn ID
// signature in the first user-data sector at the expected location.
func obsSaturnDisc() *obsDisc {
	d := &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 600, Pregap: 0, StartLBA: 0, Control: 0x41},
		},
		sectorMap: map[int][]byte{},
	}
	raw := make([]byte, 2352)
	raw[15] = 0x01
	copy(raw[16:], []byte("SEGA SEGASATURN "))
	d.sectorMap[0] = raw
	return d
}

// obsPregapDisc returns a 2-track disc where track 2's TrackInfo.Pregap
// is non-zero and StartLBA already accounts for the pregap. TOC FAD math
// must use only StartLBA + 150 and not add Pregap a second time.
func obsPregapDisc() *obsDisc {
	return &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 200, Pregap: 0, StartLBA: 0, Control: 0x41},
			{Number: 2, Type: "MODE1_RAW", Frames: 300, Pregap: 150, StartLBA: 200, Control: 0x41},
		},
	}
}

// obsBigDirDisc returns a disc with an ISO9660 volume descriptor and a
// root directory that contains entryCount real file entries (plus the
// '.' and '..' entries). Used to verify directory parse stops at 254
// real entries to keep fileNum (uint8) from wrapping.
func obsBigDirDisc(entryCount int) *obsDisc {
	const rootLBA = 20
	d := &obsDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 4000, Pregap: 0, StartLBA: 0, Control: 0x41},
		},
		sectorMap: map[int][]byte{},
	}

	pvd := make([]byte, 2352)
	pvd[15] = 0x01
	ud := pvd[16:]
	ud[1] = 'C'
	ud[2] = 'D'
	ud[3] = '0'
	ud[4] = '0'
	ud[5] = '1'
	root := ud[156:]
	root[2] = byte(rootLBA)
	root[3] = byte(rootLBA >> 8)
	root[4] = byte(rootLBA >> 16)
	root[5] = byte(rootLBA >> 24)
	dirSize := uint32((entryCount + 2) * 40)
	root[10] = byte(dirSize)
	root[11] = byte(dirSize >> 8)
	root[12] = byte(dirSize >> 16)
	root[13] = byte(dirSize >> 24)
	d.sectorMap[16] = pvd

	entries := make([]byte, 0, dirSize)
	dot := make([]byte, 40)
	dot[0] = 40
	dot[32] = 1
	dot[33] = 0x00
	dotdot := make([]byte, 40)
	dotdot[0] = 40
	dotdot[32] = 1
	dotdot[33] = 0x01
	entries = append(entries, dot...)
	entries = append(entries, dotdot...)
	for i := 0; i < entryCount; i++ {
		ent := make([]byte, 40)
		ent[0] = 40
		extLBA := uint32(100 + i)
		ent[2] = byte(extLBA)
		ent[3] = byte(extLBA >> 8)
		ent[4] = byte(extLBA >> 16)
		ent[5] = byte(extLBA >> 24)
		ent[10] = 0x00
		ent[11] = 0x08
		ent[25] = 0x00
		ent[32] = 4
		copy(ent[33:], []byte("F   "))
		entries = append(entries, ent...)
	}

	const entriesPerSector = 51
	for s := 0; s*entriesPerSector*40 < len(entries); s++ {
		raw := make([]byte, 2352)
		raw[15] = 0x01
		off := s * entriesPerSector * 40
		end := off + entriesPerSector*40
		if end > len(entries) {
			end = len(entries)
		}
		copy(raw[16:], entries[off:end])
		d.sectorMap[rootLBA+s] = raw
	}
	return d
}

// obsCDBlock returns a fresh CD block with bootDelay already cleared
// and initialized=true so tests start in the post-boot state. Drive is
// in NoDisc until SetDisc is called.
func obsCDBlock() *CDBlock {
	cb := NewCDBlock(nil)
	cb.bootDelay = 0
	cb.initialized = true
	cb.peri = true
	cb.resultsRead = true
	return cb
}

// obsCDBlockWith returns a CD block with the given disc loaded and the
// drive in Pause (the post-SetDisc steady state).
func obsCDBlockWith(d DiscReader) *CDBlock {
	cb := obsCDBlock()
	cb.SetDisc(d)
	return cb
}

// obsExec issues a command by writing CR1-CR4 and dispatching directly.
// Tests that need to exercise the QueueCommand+tick path use obsQueue.
func obsExec(cb *CDBlock, cmd uint8, cr1lo uint8, cr2, cr3, cr4 uint16) {
	cb.cmd[0] = uint16(cmd)<<8 | uint16(cr1lo)
	cb.cmd[1] = cr2
	cb.cmd[2] = cr3
	cb.cmd[3] = cr4
	cb.hirqReq &^= hirqCMOK
	cb.ExecuteCommand()
}

// obsQueue queues a command via the CR4 write path so the command-delay
// timer is engaged. Caller advances the tick to trigger execution.
func obsQueue(cb *CDBlock, cmd uint8, cr1lo uint8, cr2, cr3, cr4 uint16) {
	cb.cmd[0] = uint16(cmd)<<8 | uint16(cr1lo)
	cb.cmd[1] = cr2
	cb.cmd[2] = cr3
	cb.cmd[3] = cr4
	cb.hirqReq &^= hirqCMOK
	cb.QueueCommand()
}

// TestCDBlockPlayDiscTrackModeZeroIgnored verifies that a PlayDisc command
// with a track-mode start position whose track number is 0 is treated as
// invalid and ignored, leaving drive state intact. CD tracks are 1-based
// on real hardware. Some games issue this malformed command mid-playback
// and expect the current sector stream to keep running; if drive state
// is reset the game audio cuts out or the data stream stalls.
func TestCDBlockPlayDiscTrackModeZeroIgnored(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.startFAD = 450
	cb.endFAD = 600
	cb.playFAD = 470
	cb.repeatCount = 5
	cb.status = cdStatusPlay
	cb.playing = true

	startFAD := cb.startFAD
	endFAD := cb.endFAD
	playFAD := cb.playFAD
	repeatCount := cb.repeatCount
	status := cb.status
	playing := cb.playing

	obsExec(cb, 0x10, 0x00, 0x0000, 0x0000, 0x0000)

	if cb.startFAD != startFAD {
		t.Errorf("startFAD = %d, want %d (unchanged)", cb.startFAD, startFAD)
	}
	if cb.endFAD != endFAD {
		t.Errorf("endFAD = %d, want %d (unchanged)", cb.endFAD, endFAD)
	}
	if cb.playFAD != playFAD {
		t.Errorf("playFAD = %d, want %d (unchanged)", cb.playFAD, playFAD)
	}
	if cb.repeatCount != repeatCount {
		t.Errorf("repeatCount = %d, want %d (unchanged)", cb.repeatCount, repeatCount)
	}
	if cb.status != status {
		t.Errorf("status = 0x%02X, want 0x%02X (unchanged)", cb.status, status)
	}
	if cb.playing != playing {
		t.Errorf("playing = %v, want %v (unchanged)", cb.playing, playing)
	}
	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK not set after invalid PlayDisc")
	}
}

// TestCDBlockPlayDiscStartFFFFFFKeepsPrev verifies that PlayDisc with
// start=0xFFFFFF preserves the previously latched startFAD, and that
// end=0xFFFFFF preserves the previously latched endFAD. Without this,
// games that re-issue PlayDisc to resume the same range fail.
func TestCDBlockPlayDiscStartFFFFFFKeepsPrev(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())

	obsExec(cb, 0x10, 0x80, 0x012C, 0x0000, 0x01F4)
	prevStart := cb.startFAD
	prevEnd := cb.endFAD
	if prevStart == 0 || prevEnd == 0 {
		t.Fatalf("setup failed: startFAD=%d endFAD=%d", prevStart, prevEnd)
	}

	obsExec(cb, 0x10, 0xFF, 0xFFFF, 0x00FF, 0xFFFF)

	if cb.startFAD != prevStart {
		t.Errorf("startFAD = %d, want %d (preserved by 0xFFFFFF)", cb.startFAD, prevStart)
	}
	if cb.endFAD != prevEnd {
		t.Errorf("endFAD = %d, want %d (preserved by 0xFFFFFF)", cb.endFAD, prevEnd)
	}
}

// TestCDBlockPlayDiscEndPosTrackModeUsesEndFAD verifies that a track-mode
// end position resolves to the FAD of the *next* track (or leadout for
// the last track), not the start of the addressed track. Without this
// the last sector of the addressed track is never played, which game
// audio and FMV streams depend on.
func TestCDBlockPlayDiscEndPosTrackModeUsesEndFAD(t *testing.T) {
	cb := obsCDBlockWith(obsThreeTrack())

	obsExec(cb, 0x10, 0x00, 0x0100, 0x0000, 0x0200)

	wantEnd := cb.trackCache[2].startFAD
	if cb.endFAD != wantEnd {
		t.Errorf("endFAD = %d, want %d (start of track 3)", cb.endFAD, wantEnd)
	}

	obsExec(cb, 0x10, 0x00, 0x0100, 0x0000, 0x0300)
	wantEnd = cb.leadoutFAD()
	if cb.endFAD != wantEnd {
		t.Errorf("endFAD for last track = %d, want %d (leadout)", cb.endFAD, wantEnd)
	}
}

// TestCDBlockPlayDiscFollowingSeekDiscPreservesSeek verifies that when
// the BIOS issues SeekDisc (entering an in-flight seek) and immediately
// follows with PlayDisc using the "pickup doesn't move" mode bit, the
// existing seek target is preserved rather than overwritten with the
// current playFAD. The seek must continue toward its original target
// before play begins.
func TestCDBlockPlayDiscFollowingSeekDiscPreservesSeek(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())

	obsExec(cb, 0x11, 0x80, 0x012C, 0x0000, 0x0000)
	if !cb.seeking {
		t.Fatalf("setup: SeekDisc did not enter seeking state")
	}
	seekTarget := cb.seekFAD

	obsExec(cb, 0x10, 0xFF, 0xFFFF, 0x8000, 0x0000)

	if cb.seekFAD != seekTarget {
		t.Errorf("seekFAD = %d, want %d (preserved from in-flight SeekDisc)", cb.seekFAD, seekTarget)
	}
	if cb.seekTarget != cdStatusPlay {
		t.Errorf("seekTarget = 0x%02X, want 0x%02X (PLAY)", cb.seekTarget, cdStatusPlay)
	}
	if !cb.seeking {
		t.Error("seeking flag cleared, want still seeking")
	}
}

// TestCDBlockPosToFADTrackNumberDecoding pins the layout of the 24-bit
// position value used by PlayDisc/SeekDisc. Bit 23 set means the lower
// 23 bits are an FAD. Bit 23 clear means bits 15:8 are the 1-based
// track number and bits 7:0 are an index within the track. Misreading
// the track number from the wrong byte was a real past bug.
func TestCDBlockPosToFADTrackNumberDecoding(t *testing.T) {
	cb := obsCDBlockWith(obsThreeTrack())

	if got := cb.posToFAD(0x800000 | 0x000ABC); got != 0x000ABC {
		t.Errorf("FAD-mode posToFAD = 0x%X, want 0x000ABC", got)
	}

	want := cb.trackCache[1].startFAD
	if got := cb.posToFAD(0x000200); got != want {
		t.Errorf("track-mode posToFAD(track=2) = %d, want %d", got, want)
	}

	if got := cb.posToFAD(0x009900); got != 150 {
		t.Errorf("unknown-track posToFAD = %d, want 150 (lead-in fallback)", got)
	}
}

// TestCDBlockSeekDiscPos0ParksStandby verifies SeekDisc with pos=0
// parks the drive in STANDBY with curTrack=0xFF. The BIOS CD player
// uses GetCDStatus on this state to detect "stopped"; if the value
// is wrong the player fails to display the total-tracks/total-time
// readout that real hardware shows when stopped.
func TestCDBlockSeekDiscPos0ParksStandby(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.curTrack = 1

	obsExec(cb, 0x11, 0x00, 0x0000, 0x0000, 0x0000)

	if cb.pendingStatus != cdStatusStandby {
		t.Errorf("pendingStatus = 0x%02X, want 0x%02X (STANDBY)", cb.pendingStatus, cdStatusStandby)
	}
	if cb.curTrack != 0xFF {
		t.Errorf("curTrack = 0x%02X, want 0xFF (no track)", cb.curTrack)
	}
	if cb.seeking {
		t.Error("seeking should be false for SeekDisc(pos=0)")
	}

	cb.status = cdStatusStandby
	obsExec(cb, 0x00, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[1] != 0xFFFF {
		t.Errorf("GetCDStatus CR2 = 0x%04X, want 0xFFFF (no-track sentinel)", cb.res[1])
	}
}

// TestCDBlockPlayEndOfDiscStopsWhenEndFADZero verifies that PlayDisc
// with end=0 (continuous play to leadout) actually stops at the leadout
// FAD. Without this the drive runs past the end of the disc and reads
// invalid sectors. PEND fires on stop; EFLS fires only when the play
// originated from a ReadFile request.
func TestCDBlockPlayEndOfDiscStopsWhenEndFADZero(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cdDeviceFilter = 0
	cb.startFAD = cb.leadoutFAD() - 2
	cb.endFAD = 0
	cb.playFAD = cb.leadoutFAD() - 2
	cb.status = cdStatusPlay
	cb.playing = true
	cb.fileRead = true
	cb.hirqReq = 0

	for i := 0; i < 10 && cb.playing; i++ {
		cb.readOneSector()
	}

	if cb.playing {
		t.Error("playing should be false after end-of-disc")
	}
	if cb.status != cdStatusPause {
		t.Errorf("status = 0x%02X, want 0x%02X (PAUSE)", cb.status, cdStatusPause)
	}
	if cb.hirqReq&hirqPEND == 0 {
		t.Error("PEND not set on end-of-disc")
	}
	if cb.hirqReq&hirqEFLS == 0 {
		t.Error("EFLS not set on end-of-disc with fileRead=true")
	}
}

// TestCDBlockRepeatAllWrapsToStart verifies that repeat-all
// (repeatCount=0x0F) wraps playFAD back to startFAD when end is
// reached, without decrementing the counter and without setting PEND.
// This is the "infinite loop" mode used by background music tracks.
func TestCDBlockRepeatAllWrapsToStart(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cdDeviceFilter = 0
	cb.startFAD = 200
	cb.endFAD = 210
	cb.playFAD = 210
	cb.repeatCount = 0x0F
	cb.status = cdStatusPlay
	cb.playing = true
	cb.hirqReq = 0

	cb.readOneSector()

	if cb.playFAD != 201 {
		t.Errorf("playFAD = %d, want 201 (wrapped to start, then advanced one sector)", cb.playFAD)
	}
	if cb.repeatCount != 0x0F {
		t.Errorf("repeatCount = 0x%02X, want 0x0F (unchanged on repeat-all)", cb.repeatCount)
	}
	if cb.hirqReq&hirqPEND != 0 {
		t.Error("PEND set on wrap, want clear (repeat-all does not stop)")
	}
}

// TestCDBlockRepeatNDecrements verifies that a numeric repeat count
// decrements once per end-reach and then stops with PEND when the
// counter is exhausted. With N=3 the play range cycles four times
// (initial + 3 repeats), then stops.
func TestCDBlockRepeatNDecrements(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cdDeviceFilter = 0
	cb.startFAD = 200
	cb.endFAD = 210
	cb.playFAD = 210
	cb.repeatCount = 3
	cb.status = cdStatusPlay
	cb.playing = true

	for i := 3; i >= 1; i-- {
		cb.hirqReq = 0
		cb.readOneSector()
		if cb.repeatCount != uint8(i-1) {
			t.Errorf("after wrap %d: repeatCount = %d, want %d", 4-i, cb.repeatCount, i-1)
		}
		if cb.hirqReq&hirqPEND != 0 {
			t.Errorf("after wrap %d: PEND set, want clear", 4-i)
		}
		if !cb.playing {
			t.Fatalf("after wrap %d: playing cleared too early", 4-i)
		}
		cb.playFAD = 210
	}

	cb.hirqReq = 0
	cb.readOneSector()
	if cb.playing {
		t.Error("playing should be false after final repeat")
	}
	if cb.hirqReq&hirqPEND == 0 {
		t.Error("PEND not set after final repeat exhausted")
	}
}

// TestCDBlockEndOfPlayFiresEFLSOnceOnly verifies EFLS fires exactly
// once at end-of-play (when fileRead was set) and is not re-asserted
// by subsequent ticks or sector reads. A re-asserted EFLS would cause
// the BIOS file API to misinterpret a single read as multiple completions.
func TestCDBlockEndOfPlayFiresEFLSOnceOnly(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cdDeviceFilter = 0
	cb.startFAD = 200
	cb.endFAD = 205
	cb.playFAD = 205
	cb.repeatCount = 0
	cb.status = cdStatusPlay
	cb.playing = true
	cb.fileRead = true
	cb.hirqReq = 0

	cb.readOneSector()
	if cb.hirqReq&hirqEFLS == 0 {
		t.Fatal("EFLS not set on initial end-of-play")
	}

	cb.hirqReq = 0
	for i := 0; i < 5; i++ {
		cb.readOneSector()
		cb.TickSystemCycles(1000)
	}
	if cb.hirqReq&hirqEFLS != 0 {
		t.Error("EFLS re-asserted after initial end-of-play")
	}
}

// TestCDBlockAudioSectorRateLockedAt1x verifies that audio (CD-DA)
// tracks read at the 1x sector period regardless of cdSpeed, because
// CD-DA is time-locked to 44.1 kHz and 2x audio reads would outpace
// SCSP sample consumption, producing audible drop-outs.
func TestCDBlockAudioSectorRateLockedAt1x(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cdSpeed = 2
	cb.cdDeviceFilter = 0
	cb.playFAD = 450
	cb.curTrack = 2
	cb.status = cdStatusPlay
	cb.playing = true
	cb.DrainAudio()

	cb.TickSystemCycles(uint32(cdSectorCycles2x))
	if cb.audioCount != 0 {
		t.Errorf("after 2x-period tick at 2x: audioCount = %d, want 0 (audio paced at 1x)", cb.audioCount)
	}

	// Tick the remainder up to a full 1x sector period and verify a
	// sector now arrives. Splitting into 1x = 2x + (1x - 2x) avoids the
	// integer-division edge case where 2 * (h/150) is one cycle short of
	// (h/75) for cdSystemClockHz values not divisible by 150.
	cb.TickSystemCycles(uint32(cdSectorCycles1x - cdSectorCycles2x))
	if cb.audioCount != 588*2 {
		t.Errorf("after reaching 1x period: audioCount = %d, want %d", cb.audioCount, 588*2)
	}
}

// TestCDBlockRecalcTimingScalesAllPeriods verifies RecalcTiming
// recomputes every CD timing field from the supplied cycles-per-second
// so the CD block stays in sync with VDP2 mode changes (NTSC 320, NTSC
// 352, PAL variants) instead of using a fixed compile-time rate.
func TestCDBlockRecalcTimingScalesAllPeriods(t *testing.T) {
	cb := obsCDBlock()
	const ntsc352 uint32 = 28719600
	cb.RecalcTiming(ntsc352)

	cases := []struct {
		name string
		got  int
		want int
	}{
		{"sectorCycles1x", cb.sectorCycles1x, int(ntsc352) / 75},
		{"sectorCycles2x", cb.sectorCycles2x, int(ntsc352) / 150},
		{"scdqCycles1x", cb.scdqCycles1x, int(ntsc352) / 75},
		{"scdqCycles2x", cb.scdqCycles2x, int(ntsc352) / 150},
		{"periCycles", cb.periCycles, int(ntsc352) / 200},
		{"bootDelayCycles", cb.bootDelayCycles, int(ntsc352) * 109 / 1000},
		{"busyPauseCycles", cb.busyPauseCycles, int(ntsc352) * 333 / 10000},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// TestCDBlockRecalcTimingZeroIsNoOp guards against a 0 cycles-per-second
// from divide-by-zero or uninitialized timing producing meaningless
// thresholds. The previous values must be preserved so a subsequent
// real call can still set things up.
func TestCDBlockRecalcTimingZeroIsNoOp(t *testing.T) {
	cb := obsCDBlock()
	before := cb.sectorCycles1x
	cb.RecalcTiming(0)
	if cb.sectorCycles1x != before {
		t.Errorf("RecalcTiming(0) mutated sectorCycles1x: %d, want %d", cb.sectorCycles1x, before)
	}
}

// TestCDBlockAudioSectorPacingAcrossModes verifies that after
// RecalcTiming for either NTSC 320 (1708 dots/line) or NTSC 352 (1820)
// rates, the audio sector pacing matches the expected one-per-1x-period
// cadence: ticking the per-mode 1x sector period delivers exactly one
// CD-DA sector. Iterates several sectors with intermediate DrainAudio
// to keep the 2-sector queue from masking subsequent productions. This
// guards the WW7 char-select regression where a hardcoded 352-mode rate
// caused 320-mode games to produce ~70 sectors/sec instead of 75.
func TestCDBlockAudioSectorPacingAcrossModes(t *testing.T) {
	modes := []struct {
		name             string
		cyclesPerSecond  uint32
		expectedPeriod1x int
	}{
		{"NTSC 320", 26952240, 26952240 / 75},
		{"NTSC 352", 28719600, 28719600 / 75},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			cb := obsCDBlockWith(obsAudioPlusData())
			cb.RecalcTiming(m.cyclesPerSecond)
			if cb.sectorCycles1x != m.expectedPeriod1x {
				t.Fatalf("sectorCycles1x = %d, want %d", cb.sectorCycles1x, m.expectedPeriod1x)
			}

			cb.cdSpeed = 1
			cb.cdDeviceFilter = 0
			cb.playFAD = 450
			cb.curTrack = 2
			cb.status = cdStatusPlay
			cb.playing = true
			cb.DrainAudio()

			for i := 0; i < 5; i++ {
				cb.TickSystemCycles(uint32(m.expectedPeriod1x))
				if cb.audioCount != 588*2 {
					t.Errorf("iter %d: audioCount = %d, want %d (one sector per 1x period)",
						i, cb.audioCount, 588*2)
				}
				cb.DrainAudio()
			}
		})
	}
}

// TestCDBlockAudioReadsWithoutDeviceFilter verifies that audio sectors
// bypass the partition filter chain. cdDeviceFilter=0xFF would normally
// block data sector reads, but audio sectors must still flow into the
// EXTS audio queue or CD-DA playback breaks during BIOS startup before
// any filter has been set up.
func TestCDBlockAudioReadsWithoutDeviceFilter(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cdDeviceFilter = 0xFF
	cb.playFAD = 450
	cb.curTrack = 2
	cb.status = cdStatusPlay
	cb.playing = true
	cb.DrainAudio()

	cb.readOneSector()

	if cb.audioCount != 588*2 {
		t.Errorf("audioCount = %d, want %d (audio must not require filter)", cb.audioCount, 588*2)
	}
}

// TestCDBlockAudioBypassesPartitionBufferFull verifies that a full data
// partition does not stall audio playback. Audio uses its own circular
// queue with drop-oldest overflow; the BFUL flag must not be raised
// when audio reads while data partitions are full.
func TestCDBlockAudioBypassesPartitionBufferFull(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cdDeviceFilter = 0
	for i := 0; i < cdMaxSectors; i++ {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
			data: make([]byte, 2352), size: 2352, fad: uint32(150 + i),
		})
	}
	cb.playFAD = 450
	cb.curTrack = 2
	cb.status = cdStatusPlay
	cb.playing = true
	cb.DrainAudio()
	cb.hirqReq = 0

	cb.readOneSector()

	if cb.audioCount != 588*2 {
		t.Errorf("audioCount = %d, want %d (audio must not gate on data BFUL)", cb.audioCount, 588*2)
	}
	if cb.hirqReq&hirqBFUL != 0 {
		t.Error("BFUL set during audio read with full data partition")
	}
}

// TestCDBlockSCDQFrameRate verifies SCDQ (Q-frame interrupt) fires at
// 75 Hz at 1x speed and 150 Hz at 2x speed - the actual subcode Q-frame
// rate of the CD physical layer. Games use SCDQ as a heartbeat for
// audio-locked timing; the wrong rate causes incorrect sync.
func TestCDBlockSCDQFrameRate(t *testing.T) {
	count := func(speed uint8) int {
		cb := obsCDBlockWith(obsAudioPlusData())
		cb.cdSpeed = speed
		if speed == 2 {
			cb.scdqCounter = cdSCDQCycles2x
		} else {
			cb.scdqCounter = cdSCDQCycles1x
		}
		cb.hirqReq = 0
		const chunk = 40000
		ticks := cdSystemClockHz / chunk
		got := 0
		for i := 0; i < ticks; i++ {
			cb.TickSystemCycles(chunk)
			if cb.hirqReq&hirqSCDQ != 0 {
				got++
				cb.hirqReq &^= hirqSCDQ
			}
		}
		return got
	}

	g1 := count(1)
	if g1 < 73 || g1 > 77 {
		t.Errorf("1x SCDQ count = %d, want ~75", g1)
	}
	g2 := count(2)
	if g2 < 148 || g2 > 152 {
		t.Errorf("2x SCDQ count = %d, want ~150", g2)
	}
}

// TestCDBlockCalculateActualSizeMode1 verifies a Mode 1 (2048-byte
// user data) sector is reported as 2048 bytes / 1024 words rather than
// the raw 2352 / 1176 figure. A regression here over-reports file
// transfer size and corrupts file loads.
func TestCDBlockCalculateActualSizeMode1(t *testing.T) {
	cb := obsCDBlock()
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
		data: make([]byte, 2352), size: 2352,
		userOffset: 16, userSize: 2048, isMode2: false,
	})
	cb.getSectorLen = 0

	obsExec(cb, 0x52, 0x00, 0x0000, 0x0000, 0x0001)

	if cb.calcSize != 1024 {
		t.Errorf("calcSize = %d words, want 1024 (2048 bytes Mode 1 user data)", cb.calcSize)
	}
}

// TestCDBlockCalculateActualSizeForm2 verifies a Mode 2 Form 2 sector
// (subheader submode bit 5 set) is reported as 2324 bytes / 1162 words
// of user data. Form 2 carries audio/video streams in CD-XA discs and
// has a different user-data size from Form 1.
func TestCDBlockCalculateActualSizeForm2(t *testing.T) {
	cb := obsCDBlock()
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
		data: make([]byte, 2352), size: 2352,
		userOffset: 24, userSize: 2324, isMode2: true,
		submode: 0x20,
	})
	cb.getSectorLen = 0

	obsExec(cb, 0x52, 0x00, 0x0000, 0x0000, 0x0001)

	if cb.calcSize != 1162 {
		t.Errorf("calcSize = %d words, want 1162 (2324 bytes Form 2 user data)", cb.calcSize)
	}
}

// TestCDBlockGetThenDeleteDefersDelete verifies that GetThenDelete
// keeps the sector(s) in the partition until EndDataTransfer is
// issued. This lets the host retry a partial DATATRNS read; if the
// delete fired immediately, an interrupted transfer would lose data.
func TestCDBlockGetThenDeleteDefersDelete(t *testing.T) {
	cb := obsCDBlock()
	for i := 0; i < 3; i++ {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors, bufferedSector{
			data: make([]byte, 2352), size: 2352,
			userOffset: 16, userSize: 2048, fad: uint32(150 + i),
		})
	}
	cb.getSectorLen = 0

	obsExec(cb, 0x63, 0x00, 0x0000, 0x0000, 0x0003)

	if got := len(cb.partitions[0].sectors); got != 3 {
		t.Errorf("after GetThenDelete: sectors = %d, want 3 (delete deferred)", got)
	}

	obsExec(cb, 0x06, 0x00, 0x0000, 0x0000, 0x0000)

	if got := len(cb.partitions[0].sectors); got != 0 {
		t.Errorf("after EndDataTransfer: sectors = %d, want 0 (delete applied)", got)
	}
}

// TestCDBlockParseDirectoryCapped254 verifies the directory parse
// truncates at 254 real entries. The CD block file info record packs
// fileNum into a single byte and reserves IDs 0 and 1 for current and
// parent directories, leaving IDs 2..255 available - i.e. 254 entries.
// Without the cap, fileNum wraps to 0 and the BIOS file API confuses
// a real file with the current-directory sentinel.
func TestCDBlockParseDirectoryCapped254(t *testing.T) {
	cb := obsCDBlockWith(obsBigDirDisc(260))

	if !cb.parseVolumeDescriptor() {
		t.Fatal("parseVolumeDescriptor returned false")
	}
	files := cb.parseDirectory(cb.fsRootLBA, cb.fsRootSize)

	if len(files) != 254 {
		t.Errorf("parseDirectory returned %d entries, want 254", len(files))
	}
}

// TestCDBlockGetTOCTrackStartExcludesPregap verifies a track's TOC
// entry FAD equals StartLBA + 150 - it does NOT add Pregap a second
// time. StartLBA on the source TrackInfo already accounts for the
// pregap; double-counting it places the TOC entry past the actual
// audio start and games seek to the wrong location.
func TestCDBlockGetTOCTrackStartExcludesPregap(t *testing.T) {
	cb := obsCDBlockWith(obsPregapDisc())

	obsExec(cb, 0x02, 0x00, 0x0000, 0x0000, 0x0000)

	wantFAD := uint32(200 + 150)
	gotFAD := uint32(cb.dataBuf[2]&0x00FF)<<16 | uint32(cb.dataBuf[3])
	if gotFAD != wantFAD {
		t.Errorf("track 2 TOC FAD = %d, want %d (StartLBA+150 only)", gotFAD, wantFAD)
	}
}

// TestCDBlockTOCCtrlAdrFromTrackMetadata verifies each TOC entry's
// ctrl/adr byte comes from the source track's Control field rather
// than a hardcoded data-track value. A disc with mixed audio and data
// tracks must report each track's true control byte for the BIOS CD
// player to distinguish them.
func TestCDBlockTOCCtrlAdrFromTrackMetadata(t *testing.T) {
	cb := obsCDBlockWith(obsMixedCtrl())

	obsExec(cb, 0x02, 0x00, 0x0000, 0x0000, 0x0000)

	want := []uint8{0x01, 0x41, 0x01}
	for i, w := range want {
		got := uint8(cb.dataBuf[i*2] >> 8)
		if got != w {
			t.Errorf("track %d TOC ctrl/adr = 0x%02X, want 0x%02X", i+1, got, w)
		}
	}
}

// TestCDBlockGetAuthStatusDiscType verifies the auth status response
// distinguishes Saturn original (0x04), non-Saturn data CD (0x02), and
// audio-only CD (0x01). The BIOS branches on this value to pick the
// game vs CD player startup path.
func TestCDBlockGetAuthStatusDiscType(t *testing.T) {
	check := func(name string, d DiscReader, want uint16) {
		cb := obsCDBlockWith(d)
		obsExec(cb, 0xE0, 0x00, 0x0000, 0x0000, 0x0000)
		obsExec(cb, 0xE1, 0x00, 0x0000, 0x0000, 0x0000)
		if cb.res[1] != want {
			t.Errorf("%s: GetAuthStatus CR2 = 0x%04X, want 0x%04X", name, cb.res[1], want)
		}
	}

	check("audio-only", obsAudioOnly(), 0x0001)
	check("non-Saturn data", obsDataOnly(), 0x0002)
	check("Saturn original", obsSaturnDisc(), 0x0004)
}

// TestCDBlockCDROMFlagBit7 verifies the CD-ROM flag in standardReturn
// status responses lives at bit 7 of the CR1 low byte (0x80, derived
// from the cdFlag value 0x8 shifted into position). Earlier code put
// it at bit 6 (0x40), which the BIOS would interpret as the periodic
// status (PERI) flag.
func TestCDBlockCDROMFlagBit7(t *testing.T) {
	cb := obsCDBlockWith(obsDataOnly())
	cb.playFAD = 150
	cb.curTrack = 1

	obsExec(cb, 0x00, 0x00, 0x0000, 0x0000, 0x0000)

	if cb.res[0]&0x0080 == 0 {
		t.Errorf("CR1 low byte = 0x%02X, want bit 7 (0x80) set for data track", cb.res[0]&0xFF)
	}
}

// TestCDBlockHIRQMaskDefaultZero verifies the HIRQ mask defaults to 0
// at power-on. A non-zero default would gate interrupts the BIOS
// expects to deliver during early initialization, hanging the boot
// before InitCDSystem ever runs.
func TestCDBlockHIRQMaskDefaultZero(t *testing.T) {
	cb := NewCDBlock(nil)
	if cb.hirqMask != 0 {
		t.Errorf("power-on hirqMask = 0x%04X, want 0", cb.hirqMask)
	}
}

// TestCDBlockPERIBitDuringCommandPending verifies the periodic status
// update is suppressed while a command is pending in cmdDelay. If the
// periodic update ran during the gap between the host's CR1-CR3 writes
// and the CR4 trigger, it would overwrite the response register the
// host is about to read.
func TestCDBlockPERIBitDuringCommandPending(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.cmdDelay = 999999
	cb.periCounter = 1
	cb.resultsRead = true
	cb.res[0] = 0xDEAD
	cb.res[1] = 0xBEEF
	cb.res[2] = 0xCAFE
	cb.res[3] = 0xF00D

	cb.TickSystemCycles(uint32(cdPeriCycles + 100))

	if cb.cmdDelay <= 0 {
		t.Fatal("cmdDelay reached 0 during tick; test setup invalid")
	}
	if cb.res[0] != 0xDEAD || cb.res[1] != 0xBEEF || cb.res[2] != 0xCAFE || cb.res[3] != 0xF00D {
		t.Errorf("response overwritten while command pending: res=[%04X %04X %04X %04X]",
			cb.res[0], cb.res[1], cb.res[2], cb.res[3])
	}

	cb.cmdDelay = 0
	cb.periCounter = 1
	cb.resultsRead = true
	cb.res[0] = 0xDEAD
	cb.TickSystemCycles(uint32(cdPeriCycles + 100))
	if cb.res[0] == 0xDEAD {
		t.Error("periodic status did not resume after command completed")
	}
}

// TestCDBlockInitCDSystemHIRQValues verifies InitCDSystem sets the
// HIRQ flag pattern documented as the post-init state: CMOK + ESEL +
// EFLS + ECPY + EHST. Boot code polls these specific bits to detect
// init completion.
func TestCDBlockInitCDSystemHIRQValues(t *testing.T) {
	cb := obsCDBlockWith(obsAudioPlusData())
	cb.hirqReq = 0

	obsExec(cb, 0x04, 0x00, 0x0000, 0x0000, 0x0000)

	wantSet := uint16(hirqCMOK | hirqESEL | hirqEFLS | hirqECPY | hirqEHST)
	if cb.hirqReq&wantSet != wantSet {
		t.Errorf("hirqReq after InitCDSystem = 0x%04X, want bits 0x%04X set", cb.hirqReq, wantSet)
	}
	wantClear := uint16(hirqDRDY | hirqBFUL | hirqPEND)
	if cb.hirqReq&wantClear != 0 {
		t.Errorf("hirqReq after InitCDSystem = 0x%04X, want bits 0x%04X clear", cb.hirqReq, wantClear)
	}
}

// TestCDBlockAuthenticateDiscHIRQValues verifies disc authentication
// with a valid Saturn disc replaces the HIRQ register with the
// hardware-observed value: CMOK + CSCT + ESEL + EHST + ECPY + EFLS +
// SCDQ. The BIOS waits for this exact pattern after issuing the auth
// command.
func TestCDBlockAuthenticateDiscHIRQValues(t *testing.T) {
	cb := obsCDBlockWith(obsSaturnDisc())
	cb.hirqReq = 0

	obsExec(cb, 0xE0, 0x00, 0x0000, 0x0000, 0x0000)

	want := uint16(hirqCMOK | hirqCSCT | hirqESEL | hirqEHST | hirqECPY | hirqEFLS | hirqSCDQ)
	if cb.hirqReq != want {
		t.Errorf("hirqReq after AuthenticateDisc = 0x%04X, want 0x%04X", cb.hirqReq, want)
	}
	if !cb.authenticated {
		t.Error("authenticated flag not set")
	}
}

// TestCDBlockMPEGAuthStub verifies the MPEG card authentication
// request (sub-command 1 of AuthenticateDisc) does not hang and
// returns the no-MPEG-hardware response with MPED in HIRQ. The BIOS
// issues this at boot to probe for the optional MPEG card; if it
// hangs, boot stalls.
func TestCDBlockMPEGAuthStub(t *testing.T) {
	cb := obsCDBlockWith(obsSaturnDisc())
	cb.hirqReq = 0

	obsExec(cb, 0xE0, 0x00, 0x0001, 0x0000, 0x0000)

	if cb.hirqReq&hirqCMOK == 0 {
		t.Error("CMOK not set after MPEG auth")
	}
	if cb.hirqReq&hirqMPED == 0 {
		t.Error("MPED not set after MPEG auth (expected to indicate no MPEG card)")
	}
}

// TestCDBlockGetHardwareInfoValues verifies GetHardwareInfo returns
// the byte pattern the BIOS checks at boot to confirm the CD block
// hardware revision. The values are CR2=0x0001 (MPEG-card bit cleared,
// no MPEG card present), CR4=0x0400; mismatch causes the BIOS to
// refuse to use the CD block.
func TestCDBlockGetHardwareInfoValues(t *testing.T) {
	cb := obsCDBlock()

	obsExec(cb, 0x01, 0x00, 0x0000, 0x0000, 0x0000)

	if cb.res[1] != 0x0001 {
		t.Errorf("GetHardwareInfo CR2 = 0x%04X, want 0x0001", cb.res[1])
	}
	if cb.res[3] != 0x0400 {
		t.Errorf("GetHardwareInfo CR4 = 0x%04X, want 0x0400", cb.res[3])
	}
}
