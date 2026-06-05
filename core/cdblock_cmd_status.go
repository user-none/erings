// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

func (cb *CDBlock) leadoutFAD() uint32 {
	if cb.disc == nil {
		return 0
	}
	tracks := cb.disc.Tracks()
	if len(tracks) == 0 {
		return 150
	}
	last := tracks[len(tracks)-1]
	return uint32(last.StartLBA+last.Frames) + 150
}

func (cb *CDBlock) cmdGetCDStatus() {
	if cb.disc != nil {
		fad := cb.playFAD
		if cb.curTrack == 0xFF {
			cb.setResponse(0xFFFF, uint16(fad>>16), uint16(fad&0xFFFF))
		} else {
			trackNum := uint8(1)
			ctrlAddr := uint16(0x41)
			trackIdx := uint8(1)
			tr := cb.trackAt(fad)
			if tr != nil {
				trackNum = tr.number
				ctrlAddr = uint16(tr.control)
				trackIdx = fadToIndex(tr, fad)
			}
			cb.setResponse(
				(ctrlAddr<<8)|uint16(trackNum),
				(uint16(trackIdx)<<8)|uint16(fad>>16),
				uint16(fad&0xFFFF),
			)
		}
		cb.res[0] |= uint16(cb.cdFlag())<<4 | uint16(cb.repeatCount)
	} else {
		cb.setResponse(0, 0, 0)
	}
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdGetHardwareInfo() {
	cb.setResponse(0x0001, 0, 0x0400)
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdGetTOC() {
	// 102 entries x 4 bytes = 408 bytes = 204 words
	cb.dataBuf = make([]uint16, 204)
	for i := range cb.dataBuf {
		cb.dataBuf[i] = 0xFFFF
	}

	if cb.disc != nil {
		tracks := cb.disc.Tracks()
		for _, tr := range tracks {
			idx := tr.Number - 1
			if idx < 0 || idx >= 99 {
				continue
			}
			fad := uint32(tr.StartLBA + 150)
			ctrlAdr := tr.Control
			off := idx * 2
			cb.dataBuf[off] = uint16(ctrlAdr)<<8 | uint16(fad>>16)
			cb.dataBuf[off+1] = uint16(fad & 0xFFFF)
		}
		// Entry 99: first track
		firstCtrl := uint8(0x41)
		if len(tracks) > 0 {
			firstCtrl = tracks[0].Control
		}
		cb.dataBuf[99*2] = uint16(firstCtrl)<<8 | 0x01
		cb.dataBuf[99*2+1] = 0x0000

		// Entry 100: last track
		lastTrack := uint8(1)
		lastCtrl := uint8(0x41)
		if len(tracks) > 0 {
			last := tracks[len(tracks)-1]
			lastTrack = uint8(last.Number)
			lastCtrl = last.Control
		}
		cb.dataBuf[100*2] = uint16(lastCtrl)<<8 | uint16(lastTrack)
		cb.dataBuf[100*2+1] = 0x0000

		// Entry 101: lead-out
		leadoutFAD := cb.leadoutFAD()
		cb.dataBuf[101*2] = 0x4100 | uint16(leadoutFAD>>16)
		cb.dataBuf[101*2+1] = uint16(leadoutFAD & 0xFFFF)
	}

	cb.dataPos = 0
	cb.transferActive = true
	cb.dataTransferType = cdCmdGetTOC
	cb.setResponse(uint16(len(cb.dataBuf)), 0, 0)
	cb.hirqReq |= hirqCMOK | hirqDRDY
}

func (cb *CDBlock) cmdGetSessionInfo() {
	if cb.disc != nil {
		query := cb.cmd[0] & 0xFF
		if query == 0 {
			// Lead-out FAD, session count = 1
			loFAD := cb.leadoutFAD()
			cb.setResponse(0, uint16(1)<<8|uint16(loFAD>>16), uint16(loFAD&0xFFFF))
		} else {
			// Session N: return session number in CR3 HB
			cb.setResponse(0, uint16(query)<<8, 0)
		}
	} else {
		cb.setResponse(0, 0, 0)
	}
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdInitCDSystem() {
	cb.fsFiles = nil
	cb.fsRootLBA = 0
	cb.fsRootSize = 0
	cb.fsInitialized = false
	cb.playing = false
	cb.repeatCount = 0
	cb.DrainAudio()
	cb.startFAD = 0
	cb.endFAD = 0
	cb.cdDeviceFilter = 0xFF
	cb.lastBufferDest = 0xFF
	cb.authenticated = false
	cb.initSelectors()
	// Init flags (CR1 low byte). Bit 5 requests a change to the init
	// flags; bits 0-4 (bit 4 = data-read speed, 1=1x/0=2x) apply only when
	// it is set. 0xFF is the no-change sentinel, as 0xFFFF is for standby
	// and ECC/retry in CR2/CR4.
	initFlag := cb.cmd[0] & 0xFF
	if initFlag != 0xFF && initFlag&0x20 != 0 {
		if initFlag&0x10 != 0 {
			cb.cdSpeed = 1
		} else {
			cb.cdSpeed = 2
		}
	}
	// Reset timing state
	cb.sectorAccum = 0
	cb.seeking = false
	cb.busyDelay = 0
	cb.pendingStatus = 0

	// Reset FAD to disc start with BUSY->PAUSE transition
	if cb.disc != nil {
		cb.playFAD = 150
		cb.curTrack = 0xFF
		cb.status = cdStatusBusy
		cb.pendingStatus = cdStatusPause
		cb.busyDelay = cdInitDelay
	}
	// Clear DRDY, CSCT, BFUL, PEND; set CMOK, ESEL, EFLS, ECPY, EHST
	cb.hirqReq &= 0xFFE5
	cb.hirqReq |= hirqCMOK | hirqESEL | hirqEFLS | hirqECPY | hirqEHST
	cb.standardReturn()
	cb.resultsRead = false
}

func (cb *CDBlock) cmdEndDataTransfer() {
	cb.transferActive = false
	var words uint32
	if cb.dataBuf == nil && cb.dataPos == 0 && cb.putSectorsRemaining == 0 {
		words = 0xFFFFFF
	} else {
		words = uint32(len(cb.dataBuf))
		cb.dataBuf = nil
		cb.dataPos = 0
		cb.putBuf = nil
		cb.putSectorsRemaining = 0
	}
	cb.setResponse(uint16(words&0xFFFF), 0, 0)
	cb.res[0] |= uint16((words >> 16) & 0xFF)
	cb.hirqReq |= hirqCMOK
	if cb.dataTransferType == cdCmdGetSectorData ||
		cb.dataTransferType == cdCmdGetThenDelete ||
		cb.dataTransferType == cdCmdPutSectorData {
		cb.hirqReq |= hirqEHST
	}
	if cb.dataTransferType == cdCmdGetThenDelete && cb.delPart != nil {
		cb.deleteSectors(cb.delPart, cb.delStart, cb.delCount)
		cb.delPart = nil
	}
	cb.dataTransferType = 0
}
