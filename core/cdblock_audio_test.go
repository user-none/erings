// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"testing"
)

// audioMockDisc implements DiscReader with one DATA track and one AUDIO
// track for testing the EXTS audio queue path.
type audioMockDisc struct {
	tracks []TrackInfo
}

func (m *audioMockDisc) ReadSector(lba int) ([]byte, error) {
	total := 0
	for _, tr := range m.tracks {
		total += tr.Frames
	}
	if lba < 0 || lba >= total {
		return nil, fmt.Errorf("lba %d out of range", lba)
	}
	data := make([]byte, 2352)
	// Find which track this LBA is in.
	off := lba
	var inAudio bool
	for _, tr := range m.tracks {
		if off < tr.Frames {
			inAudio = (tr.Type == "AUDIO")
			break
		}
		off -= tr.Frames
	}
	if inAudio {
		// Audio sector: fill with a recognizable LE int16 pattern so
		// decode tests can verify byte order. Sample i: L=i, R=i+0x4000.
		for i := 0; i < 588; i++ {
			data[i*4+0] = byte(i)
			data[i*4+1] = byte(i >> 8)
			data[i*4+2] = byte(i + 0x4000)
			data[i*4+3] = byte((i + 0x4000) >> 8)
		}
	} else {
		// Data sector: MODE1 sync + header + LBA-pattern user data.
		data[0] = 0x00
		for i := 1; i <= 10; i++ {
			data[i] = 0xFF
		}
		data[11] = 0x00
		data[12] = byte(lba / 4500)
		data[13] = byte((lba / 75) % 60)
		data[14] = byte(lba % 75)
		data[15] = 0x01
		pattern := byte(lba & 0xFF)
		for i := 16; i < 2064; i++ {
			data[i] = pattern
		}
	}
	return data, nil
}

func (m *audioMockDisc) NumTracks() int { return len(m.tracks) }
func (m *audioMockDisc) Track(i int) (int, string, int, int, int, uint8) {
	return trackAt(m.tracks, i)
}
func (m *audioMockDisc) NumTrackIndexes(i int) int      { return 1 }
func (m *audioMockDisc) TrackIndex(i, n int) (int, int) { return trackIndexAt(m.tracks, i) }

func newAudioMockDisc() *audioMockDisc {
	return &audioMockDisc{
		tracks: []TrackInfo{
			{Number: 1, Type: "MODE1_RAW", Frames: 300, Pregap: 0, StartLBA: 0, Control: 0x41},
			{Number: 2, Type: "AUDIO", Frames: 500, Pregap: 0, StartLBA: 300, Control: 0x01},
		},
	}
}

func newCDBlockWithAudioDisc() *CDBlock {
	cb := NewCDBlock(nil)
	cb.SetDisc(newAudioMockDisc())
	return cb
}

func TestCDBlockAudioSampleDecodeLE(t *testing.T) {
	cb := NewCDBlock(nil)
	// Build a 2352-byte buffer with two known stereo pairs, little-endian.
	// Sample 0: L=0x1234, R=0x5678. Sample 1: L=-1 (0xFFFF), R=1 (0x0001).
	raw := make([]byte, 2352)
	raw[0] = 0x34
	raw[1] = 0x12
	raw[2] = 0x78
	raw[3] = 0x56
	raw[4] = 0xFF
	raw[5] = 0xFF
	raw[6] = 0x01
	raw[7] = 0x00

	cb.appendAudioSamples(raw)

	if cb.audioCount != 588*2 {
		t.Fatalf("audioCount = %d, want %d", cb.audioCount, 588*2)
	}

	l, r, ok := cb.PopAudioSample()
	if !ok {
		t.Fatal("pop 0: valid=false")
	}
	if l != 0x1234 || r != 0x5678 {
		t.Errorf("pop 0: L=0x%04X R=0x%04X, want 0x1234 0x5678", uint16(l), uint16(r))
	}

	l, r, ok = cb.PopAudioSample()
	if !ok {
		t.Fatal("pop 1: valid=false")
	}
	if l != -1 || r != 1 {
		t.Errorf("pop 1: L=%d R=%d, want -1 1", l, r)
	}
}

func TestCDBlockAudioQueueOverflow(t *testing.T) {
	cb := NewCDBlock(nil)
	// Push more pairs than the queue can hold to trigger drop-oldest.
	totalPairs := cdAudioQueueMax
	for i := 0; i < totalPairs; i++ {
		cb.pushAudioPair(int16(i), int16(i+1))
	}

	if cb.audioCount > cdAudioQueueMax {
		t.Errorf("audioCount %d exceeds cap %d", cb.audioCount, cdAudioQueueMax)
	}

	l, r, ok := cb.PopAudioSample()
	if !ok {
		t.Fatal("pop after overflow: valid=false")
	}
	if l == 0 && r == 1 {
		t.Error("oldest pair was retained; drop-oldest did not run")
	}
}

func TestCDBlockAudioPopEmpty(t *testing.T) {
	cb := NewCDBlock(nil)
	l, r, ok := cb.PopAudioSample()
	if ok {
		t.Errorf("pop on empty: ok=true, want false")
	}
	if l != 0 || r != 0 {
		t.Errorf("pop on empty: L=%d R=%d, want 0 0", l, r)
	}
}

func TestCDBlockAudioDrain(t *testing.T) {
	cb := NewCDBlock(nil)
	cb.pushAudioPair(1, 2)
	cb.pushAudioPair(3, 4)
	cb.DrainAudio()

	if cb.audioCount != 0 {
		t.Errorf("after drain: audioCount = %d, want 0", cb.audioCount)
	}
	if _, _, ok := cb.PopAudioSample(); ok {
		t.Error("pop after drain returned valid=true")
	}
}

func TestCDBlockReadOneSectorAudioRouting(t *testing.T) {
	cb := newCDBlockWithAudioDisc()
	// Track 2 (AUDIO) starts at LBA 300, FAD 450.
	cb.playFAD = 450
	cb.playing = true
	cb.status = cdStatusPlay
	cb.cdDeviceFilter = 0 // required by readOneSector to proceed

	before := cb.totalSectors()
	cb.readOneSector()

	if cb.audioCount != 588*2 {
		t.Errorf("audioCount = %d, want %d", cb.audioCount, 588*2)
	}
	if cb.totalSectors() != before {
		t.Errorf("partition state changed: before=%d after=%d (audio sector should not enter filter chain)", before, cb.totalSectors())
	}
	if cb.playFAD != 451 {
		t.Errorf("playFAD = %d, want 451", cb.playFAD)
	}
}

func TestCDBlockReadOneSectorDataRouting(t *testing.T) {
	cb := newCDBlockWithAudioDisc()
	// Track 1 (DATA) starts at LBA 0, FAD 150.
	cb.playFAD = 150
	cb.playing = true
	cb.status = cdStatusPlay
	cb.cdDeviceFilter = 0

	before := cb.totalSectors()
	cb.readOneSector()

	if cb.audioCount != 0 {
		t.Errorf("audioCount = %d, want 0 (data sector must not enter audio queue)", cb.audioCount)
	}
	if cb.totalSectors() == before {
		t.Errorf("partition state unchanged: before=%d after=%d (data sector should enter filter chain)", before, cb.totalSectors())
	}
}

func TestCDBlockDrainOnInitCDSystem(t *testing.T) {
	cb := newCDBlockWithAudioDisc()
	cb.pushAudioPair(1, 2)
	cb.pushAudioPair(3, 4)
	if cb.audioCount == 0 {
		t.Fatal("setup: audioCount unexpectedly 0")
	}

	cb.cmdInitCDSystem()

	if cb.audioCount != 0 {
		t.Errorf("after cmdInitCDSystem: audioCount = %d, want 0", cb.audioCount)
	}
}

func TestCDBlockDrainOnSetDisc(t *testing.T) {
	cb := newCDBlockWithAudioDisc()
	cb.pushAudioPair(1, 2)
	cb.pushAudioPair(3, 4)

	cb.SetDisc(newAudioMockDisc())

	if cb.audioCount != 0 {
		t.Errorf("after SetDisc: audioCount = %d, want 0", cb.audioCount)
	}
}
