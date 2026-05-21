// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

func (cb *CDBlock) cmdSetCDDeviceConnection() {
	cb.cdDeviceFilter = uint8(cb.cmd[2] >> 8)
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetCDDeviceConnection() {
	cb.setResponse(0, uint16(cb.cdDeviceFilter)<<8, 0)
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdGetLastBufferDest() {
	cb.setResponse(0, uint16(cb.lastBufferDest)<<8, 0)
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdSetFilterRange() {
	fNum := uint8(cb.cmd[2] >> 8)
	if int(fNum) < len(cb.filters) {
		cb.filters[fNum].fadStart = (uint32(cb.cmd[0]&0xFF) << 16) | uint32(cb.cmd[1])
		cb.filters[fNum].fadRange = (uint32(cb.cmd[2]&0xFF) << 16) | uint32(cb.cmd[3])
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetFilterRange() {
	fNum := uint8(cb.cmd[2] >> 8)
	if int(fNum) < len(cb.filters) {
		f := &cb.filters[fNum]
		cb.setResponse(
			uint16(f.fadStart&0xFFFF),
			uint16(fNum)<<8|uint16(f.fadRange>>16),
			uint16(f.fadRange&0xFFFF),
		)
		cb.res[0] |= uint16(f.fadStart>>16) & 0xFF
	} else {
		cb.setResponse(0, 0, 0)
	}
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdSetFilterSubheaderConditions() {
	fNum := uint8(cb.cmd[2] >> 8)
	if int(fNum) < len(cb.filters) {
		f := &cb.filters[fNum]
		f.chanNum = uint8(cb.cmd[0] & 0xFF)
		f.fileNum = uint8(cb.cmd[2] & 0xFF)
		f.smmask = uint8(cb.cmd[1] >> 8)
		f.cimask = uint8(cb.cmd[1] & 0xFF)
		f.smval = uint8(cb.cmd[3] >> 8)
		f.cival = uint8(cb.cmd[3] & 0xFF)
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetFilterSubheaderConditions() {
	fNum := uint8(cb.cmd[2] >> 8)
	if int(fNum) < len(cb.filters) {
		f := &cb.filters[fNum]
		cb.setResponse(
			uint16(f.smmask)<<8|uint16(f.cimask),
			uint16(f.fileNum),
			uint16(f.smval)<<8|uint16(f.cival),
		)
		cb.res[0] |= uint16(f.chanNum)
	} else {
		cb.setResponse(0, 0, 0)
	}
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdSetFilterMode() {
	fNum := uint8(cb.cmd[2] >> 8)
	mode := uint8(cb.cmd[0] & 0xFF)
	if int(fNum) < len(cb.filters) {
		if mode&0x80 != 0 {
			// Bit 7: initialize filter to defaults
			cb.filters[fNum] = cdFilter{
				trueConn:  cb.filters[fNum].trueConn,
				falseConn: cb.filters[fNum].falseConn,
			}
		} else {
			cb.filters[fNum].mode = mode
		}
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetFilterMode() {
	fNum := uint8(cb.cmd[2] >> 8)
	if int(fNum) < len(cb.filters) {
		cb.setResponse(0, 0, 0)
		cb.res[0] |= uint16(cb.filters[fNum].mode)
	} else {
		cb.setResponse(0, 0, 0)
	}
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdSetFilterConnection() {
	fNum := uint8(cb.cmd[2] >> 8)
	which := uint8(cb.cmd[0] & 0xFF)
	if int(fNum) < len(cb.filters) {
		if which&0x01 != 0 {
			cb.filters[fNum].trueConn = uint8(cb.cmd[1] >> 8)
		}
		if which&0x02 != 0 {
			cb.filters[fNum].falseConn = uint8(cb.cmd[1] & 0xFF)
		}
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetFilterConnection() {
	fNum := uint8(cb.cmd[2] >> 8)
	if int(fNum) < len(cb.filters) {
		f := &cb.filters[fNum]
		cb.setResponse(uint16(f.trueConn)<<8|uint16(f.falseConn), 0, 0)
	} else {
		cb.setResponse(0, 0, 0)
	}
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdResetSelector() {
	flags := uint8(cb.cmd[0] & 0xFF)
	bufNum := uint8(cb.cmd[2] >> 8)
	cb.resetSelectors(flags, bufNum)
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetBufferSize() {
	free := uint16(cdMaxSectors - cb.totalSectors())
	cb.setResponse(free, 0x1800, uint16(cdMaxSectors))
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdGetSectorNumber() {
	bufNum := uint8(cb.cmd[2] >> 8)
	count := uint16(0)
	if part := cb.getPartition(bufNum); part != nil {
		count = uint16(len(part.sectors))
	}
	cb.setResponse(0, 0, count)
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdGetCopyError() {
	cb.setResponse(0, 0, 0)
	cb.hirqReq |= hirqCMOK
}
