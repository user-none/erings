// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

func (cb *CDBlock) getPartition(bufNum uint8) *cdPartition {
	if int(bufNum) < len(cb.partitions) {
		return &cb.partitions[bufNum]
	}
	return nil
}

// sectorSlice returns the data slice for a sector based on the requested
// getSectorLen. For raw disc sectors (size 2352), applies the correct
// offset to skip sync/header. For put sectors (size matches requested
// length), data is at offset 0.
func sectorSlice(sec *bufferedSector, getSectorLen int) []byte {
	raw := sec.data

	// Put sectors store data at offset 0 with actual size matching the
	// raw sector size; return as-is.
	if sec.userOffset == 0 && sec.userSize == sec.size {
		return raw
	}

	switch getSectorLen {
	case 0:
		// User data only (2048 Mode 1/Form 1, or 2324 Form 2)
		end := sec.userOffset + sec.userSize
		if end > len(raw) {
			end = len(raw)
		}
		return raw[sec.userOffset:end]
	case 1:
		// 2336: everything after sync+header (bytes 16-2351)
		if len(raw) >= 2352 {
			return raw[16:2352]
		}
	case 2:
		// 2340: everything after sync (bytes 12-2351)
		if len(raw) >= 2352 {
			return raw[12:2352]
		}
	case 3:
		// 2352: full raw sector
		return raw
	}
	return raw
}

func (cb *CDBlock) extractSectorData(part *cdPartition, startIdx, count int) {
	cb.dataBuf = nil
	cb.dataPos = 0
	if part == nil {
		return
	}

	for i := startIdx; i < startIdx+count && i < len(part.sectors); i++ {
		slice := sectorSlice(&part.sectors[i], cb.getSectorLen)
		// Pack as big-endian uint16 words
		for j := 0; j+1 < len(slice); j += 2 {
			cb.dataBuf = append(cb.dataBuf, uint16(slice[j])<<8|uint16(slice[j+1]))
		}
		if len(slice)%2 != 0 {
			cb.dataBuf = append(cb.dataBuf, uint16(slice[len(slice)-1])<<8)
		}
	}
}

func (cb *CDBlock) deleteSectors(part *cdPartition, startIdx, count int) {
	if part == nil {
		return
	}
	end := startIdx + count
	if end > len(part.sectors) {
		end = len(part.sectors)
	}
	if startIdx >= len(part.sectors) {
		return
	}
	part.sectors = append(part.sectors[:startIdx], part.sectors[end:]...)
}

func (cb *CDBlock) cmdCalculateActualSize() {
	bufNum := uint8(cb.cmd[2] >> 8)
	startIdx := int(cb.cmd[1])
	count := int(cb.cmd[3])
	cb.calcSize = 0
	if part := cb.getPartition(bufNum); part != nil {
		end := startIdx + count
		if end > len(part.sectors) {
			end = len(part.sectors)
		}
		for i := startIdx; i < end; i++ {
			sec := &part.sectors[i]
			switch cb.getSectorLen {
			case 0:
				// User data only (1024 words for 2048 bytes, 1162 for Form 2)
				cb.calcSize += uint32(sec.userSize / 2)
			case 1:
				cb.calcSize += 1168 // 2336 bytes
			case 2:
				cb.calcSize += 1170 // 2340 bytes
			case 3:
				cb.calcSize += 1176 // 2352 bytes
			}
		}
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetActualSize() {
	cb.setResponse(uint16(cb.calcSize&0xFFFF), 0, 0)
	cb.res[0] |= uint16((cb.calcSize >> 16) & 0xFF)
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetSectorInfo() {
	bufNum := uint8(cb.cmd[2] >> 8)
	secNum := int(cb.cmd[1] & 0xFF)
	part := cb.getPartition(bufNum)
	if part != nil && secNum < len(part.sectors) {
		sec := part.sectors[secNum]
		cb.setResponse(
			uint16(sec.fad&0xFFFF),
			uint16(sec.fileNum)<<8|uint16(sec.chanNum),
			uint16(sec.submode)<<8|uint16(sec.codinfo),
		)
		cb.res[0] |= uint16((sec.fad >> 16) & 0xFF)
	} else {
		// Reject
		cb.res[0] = 0xFF << 8
		cb.res[1] = 0
		cb.res[2] = 0
		cb.res[3] = 0
	}
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdSetSectorLength() {
	cb.getSectorLen = int(cb.cmd[0] & 0xFF)
	cb.putSectorLen = int(cb.cmd[1] >> 8)
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetSectorData() {
	bufNum := uint8(cb.cmd[2] >> 8)
	startIdx := int(cb.cmd[1])
	count := int(cb.cmd[3])
	cb.extractSectorData(cb.getPartition(bufNum), startIdx, count)
	cb.transferActive = true
	cb.dataTransferType = cdCmdGetSectorData
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqDRDY | hirqEHST
}

func (cb *CDBlock) cmdDeleteSectorData() {
	bufNum := uint8(cb.cmd[2] >> 8)
	startIdx := int(cb.cmd[1])
	count := int(cb.cmd[3])
	part := cb.getPartition(bufNum)
	if part != nil {
		// Handle special values
		if startIdx == 0xFFFF && (count == 1 || count == 0xFFFF) {
			// Delete from end
			if len(part.sectors) > 0 {
				part.sectors = part.sectors[:len(part.sectors)-1]
			}
		} else if count == 0xFFFF {
			// Delete from startIdx to end
			if startIdx < len(part.sectors) {
				part.sectors = part.sectors[:startIdx]
			}
		} else {
			cb.deleteSectors(part, startIdx, count)
		}
	}

	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqEHST
}

func (cb *CDBlock) cmdGetThenDelete() {
	bufNum := uint8(cb.cmd[2] >> 8)
	startIdx := int(cb.cmd[1])
	count := int(cb.cmd[3])
	part := cb.getPartition(bufNum)
	cb.extractSectorData(part, startIdx, count)

	// Defer deletion until EndDataTransfer
	cb.delPart = part
	cb.delStart = startIdx
	cb.delCount = count

	cb.transferActive = true
	cb.dataTransferType = cdCmdGetThenDelete
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqDRDY | hirqEHST
}

func (cb *CDBlock) cmdPutSectorData() {
	bufNum := uint8(cb.cmd[2] >> 8)
	count := int(cb.cmd[3])

	if int(bufNum) >= len(cb.partitions) || cb.totalSectors()+count > cdMaxSectors {
		cb.res[0] = 0xFF << 8
		cb.res[1] = 0
		cb.res[2] = 0
		cb.res[3] = 0
		cb.hirqReq |= hirqCMOK | hirqEHST
		return
	}

	cb.putBuf = nil
	cb.putBufNum = bufNum
	cb.putSectorsRemaining = count
	cb.transferActive = true
	cb.dataTransferType = cdCmdPutSectorData
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqDRDY
}

func (cb *CDBlock) cmdCopySectorData() {
	dstFilter := uint8(cb.cmd[0] & 0xFF)
	srcBuf := uint8(cb.cmd[2] >> 8)
	startIdx := int(cb.cmd[1])
	count := int(cb.cmd[3])
	if cb.totalSectors()+count > cdMaxSectors {
		cb.res[0] = 0xFF << 8
		cb.res[1] = 0
		cb.res[2] = 0
		cb.res[3] = 0
		cb.hirqReq |= hirqCMOK | hirqECPY
		return
	}
	if part := cb.getPartition(srcBuf); part != nil {
		end := startIdx + count
		if end > len(part.sectors) {
			end = len(part.sectors)
		}
		for i := startIdx; i < end; i++ {
			sec := part.sectors[i]
			cp := make([]byte, len(sec.data))
			copy(cp, sec.data)
			sec.data = cp
			cb.routeSector(sec, dstFilter)
		}
	}
	cb.standardReturn()
	cb.resultsRead = false
	if cb.totalSectors() >= cdMaxSectors {
		cb.hirqReq |= hirqBFUL
	}
	cb.hirqReq |= hirqCMOK | hirqECPY
}

func (cb *CDBlock) cmdMoveSectorData() {
	dstFilter := uint8(cb.cmd[0] & 0xFF)
	srcBuf := uint8(cb.cmd[2] >> 8)
	startIdx := int(cb.cmd[1])
	count := int(cb.cmd[3])
	if part := cb.getPartition(srcBuf); part != nil {
		end := startIdx + count
		if end > len(part.sectors) {
			end = len(part.sectors)
		}
		// Remove from source before routing to handle self-referencing filters
		moved := make([]bufferedSector, end-startIdx)
		copy(moved, part.sectors[startIdx:end])
		part.sectors = append(part.sectors[:startIdx], part.sectors[end:]...)
		// Route through filter chain
		for _, sec := range moved {
			cb.routeSector(sec, dstFilter)
		}
	}
	cb.standardReturn()
	cb.resultsRead = false
	if cb.totalSectors() >= cdMaxSectors {
		cb.hirqReq |= hirqBFUL
	}
	cb.hirqReq |= hirqCMOK | hirqECPY
}
