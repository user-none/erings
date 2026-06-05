// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// fad2bcd converts a FAD value to BCD-encoded minute, second, frame.
func fad2bcd(fad uint32) (m, s, f byte) {
	frames := fad
	f = byte(frames % 75)
	frames /= 75
	s = byte(frames % 60)
	m = byte(frames / 60)
	f = toBCD(f)
	s = toBCD(s)
	m = toBCD(m)
	return
}

// toBCD converts a byte value (0-99) to BCD encoding.
func toBCD(v byte) byte {
	return (v/10)<<4 | (v % 10)
}

// posToFAD decodes a 24-bit CD position value. Bit 23 set means the
// remaining 23 bits are an FAD. Bit 23 clear means bits 15:8 are a
// track number (1-based) and bits 7:0 are an index number within
// that track. Unknown tracks fall back to FAD 150 (disc lead-in).
func (cb *CDBlock) posToFAD(pos uint32) uint32 {
	if pos&0x800000 != 0 {
		return pos & 0x7FFFFF
	}
	trackNum := int((pos >> 8) & 0xFF)
	for _, tr := range cb.trackCache {
		if int(tr.number) == trackNum {
			return tr.index01FAD
		}
	}
	return 150
}

// posToEndFAD decodes a 24-bit CD position as an end boundary. When
// specified as a track number (bit 23 clear), this returns the FAD
// just past the end of the track (start of the next track, or leadout
// for the last track). This differs from posToFAD which returns the
// start of the track.
func (cb *CDBlock) posToEndFAD(pos uint32) uint32 {
	if pos&0x800000 != 0 {
		return pos & 0x7FFFFF
	}
	trackNum := int((pos >> 8) & 0xFF)
	for i, tr := range cb.trackCache {
		if int(tr.number) == trackNum {
			if i+1 < len(cb.trackCache) {
				return cb.trackCache[i+1].index01FAD
			}
			return cb.leadoutFAD()
		}
	}
	return 150
}

// buildSubCodeQ computes a 5-word SubQ buffer from current playFAD and track info.
func (cb *CDBlock) buildSubCodeQ() []uint16 {
	var ctrlAdr byte = 0x01 // audio default
	var trackNum byte = 1
	var relFAD uint32

	indexNum := byte(1)
	tr := cb.trackAt(cb.playFAD)
	if tr != nil {
		trackNum = byte(tr.number)
		ctrlAdr = tr.control
		if cb.playFAD >= tr.index01FAD {
			relFAD = cb.playFAD - tr.index01FAD
		} else {
			// In the pregap the relative MSF counts down to INDEX 01.
			relFAD = tr.index01FAD - cb.playFAD
		}
		indexNum = fadToIndex(tr, cb.playFAD)
	}

	rm, rs, rf := fad2bcd(relFAD)
	am, as, af := fad2bcd(cb.playFAD)
	trackBCD := toBCD(trackNum)
	indexBCD := toBCD(indexNum)

	buf := make([]uint16, 5)
	buf[0] = uint16(ctrlAdr)<<8 | uint16(trackBCD)
	buf[1] = uint16(indexBCD)<<8 | uint16(rm)
	buf[2] = uint16(rs)<<8 | uint16(rf)
	buf[3] = uint16(0x00)<<8 | uint16(am)
	buf[4] = uint16(as)<<8 | uint16(af)
	return buf
}

func (cb *CDBlock) cmdPlayDisc() {
	if cb.disc == nil {
		cb.setResponse(0, 0, 0)
		cb.hirqReq |= hirqCMOK
		return
	}

	// Track-mode start position with track number 0 is invalid:
	// tracks are 1-based. Real hardware appears to ignore the
	// command and leave drive state unchanged; some games issue
	// this mid-play and expect the current sector stream to
	// continue. Return CMOK without touching any state.
	startPosCheck := (uint32(cb.cmd[0]&0xFF) << 16) | uint32(cb.cmd[1])
	if startPosCheck != 0xFFFFFF && startPosCheck&0x800000 == 0 &&
		(startPosCheck>>8)&0xFF == 0 {
		cb.standardReturn()
		cb.hirqReq |= hirqCMOK
		return
	}

	// Parse start position from CR1(LB):CR2
	// 0xFFFFFF means keep previous start position
	startPos := (uint32(cb.cmd[0]&0xFF) << 16) | uint32(cb.cmd[1])
	if startPos != 0xFFFFFF {
		cb.startFAD = cb.posToFAD(startPos)
	}

	// Parse play mode from CR3 HB
	playMode := cb.cmd[2] >> 8

	// Bit 7: pickup movement (0=move to start, 1=stay at current position)
	// Don't set playFAD here - the seek step model will move it progressively

	repeatVal := playMode & 0x7F
	if repeatVal != 0x7F {
		cb.repeatCount = uint8(repeatVal)
	}

	// Parse end position from CR3(LB):CR4
	// 0xFFFFFF means keep previous end position
	// When bit 23 is set, the value is a sector count from startFAD
	endPos := (uint32(cb.cmd[2]&0xFF) << 16) | uint32(cb.cmd[3])
	if endPos == 0xFFFFFF {
		// Keep current endFAD
	} else if endPos&0x800000 != 0 {
		cb.endFAD = cb.startFAD + (endPos & 0x7FFFFF)
	} else if endPos == 0 {
		cb.endFAD = 0 // continuous play to end of disc
	} else {
		cb.endFAD = cb.posToEndFAD(endPos)
	}

	tr := cb.trackAt(cb.startFAD)
	if tr != nil {
		cb.curTrack = tr.number
	}

	cb.playing = true
	cb.fileRead = false
	cb.sectorAccum = 0

	// BUSY -> SEEK -> PLAY transition
	cb.status = cdStatusBusy
	cb.pendingStatus = cdStatusSeek
	cb.busyDelay = cdCmdDelay
	if playMode&0x80 == 0 {
		// Pickup moves: seek to startFAD
		cb.seekFAD = cb.startFAD
		cb.seeking = true
	} else if !cb.seeking {
		// Pickup doesn't move and no seek in progress: play from
		// current playFAD.
		cb.seekFAD = cb.playFAD
		cb.seeking = true
	}
	// Otherwise pickup doesn't move and a seek is already in progress
	// (BIOS issued SeekDisc followed by PlayDisc); let that seek
	// complete to its existing seekFAD target.
	cb.seekTarget = cdStatusPlay

	cb.standardReturn()
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdSeekDisc() {
	cb.playing = false
	if cb.disc != nil {
		pos := (uint32(cb.cmd[0]&0xFF) << 16) | uint32(cb.cmd[1])
		cb.status = cdStatusBusy
		cb.busyDelay = cdCmdDelay
		if pos&0x800000 != 0 && (pos&0x7FFFFF) == 0x7FFFFF {
			// FAD 0xFFFFFF means pause at current position
			cb.pendingStatus = cdStatusPause
		} else if pos&0x800000 != 0 {
			// FAD seek - step model moves playFAD toward target
			cb.seekFAD = pos & 0x7FFFFF
			cb.pendingStatus = cdStatusSeek
			cb.seeking = true
			cb.seekTarget = cdStatusPause
		} else if pos == 0 {
			// Pos 0x000000: Stop (CD_SEEK_home per PDF). Park in
			// STANDBY with curTrack=0xFF so cmdGetCDStatus returns
			// the "no track" response (0xFFFF) which the BIOS CD
			// player interprets as "show total tracks / total time".
			cb.playFAD = 150
			if len(cb.trackCache) > 0 {
				cb.playFAD = cb.trackCache[0].index01FAD
			}
			cb.seekFAD = cb.playFAD
			cb.pendingStatus = cdStatusStandby
			cb.seeking = false
			cb.curTrack = 0xFF
		} else {
			// Track seek - track number in bits 15:8, invalid track sets STANDBY
			trackNum := int((pos >> 8) & 0xFF)
			found := false
			var targetFAD uint32
			for _, tr := range cb.trackCache {
				if int(tr.number) == trackNum {
					targetFAD = tr.index01FAD
					found = true
					break
				}
			}
			if found {
				cb.seekFAD = targetFAD
				cb.pendingStatus = cdStatusSeek
				cb.seeking = true
				cb.seekTarget = cdStatusPause
			} else {
				cb.pendingStatus = cdStatusStandby
			}
		}
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdScanDisc() {
	cb.playing = false
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdGetSubcode() {
	subcodeType := cb.cmd[0] & 0xFF
	switch subcodeType {
	case 0:
		// Q channel: build 10-byte SubQ from current state
		q := cb.buildSubCodeQ()
		cb.dataBuf = q
		cb.dataPos = 0
		cb.dataTransferType = cdCmdGetSubcode
		cb.setResponse(5, 0, 0)
		cb.hirqReq |= hirqCMOK | hirqDRDY
	case 1:
		// R-W channel: return 12 zero words
		cb.dataBuf = make([]uint16, 12)
		cb.dataPos = 0
		cb.dataTransferType = cdCmdGetSubcode
		cb.setResponse(12, 0, 0)
		cb.hirqReq |= hirqCMOK | hirqDRDY
	default:
		cb.setResponse(0, 0, 0)
		cb.hirqReq |= hirqCMOK
	}
}
