// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

func (cb *CDBlock) getPartition(bufNum uint8) *cdPartition {
	if int(bufNum) < len(cb.partitions) {
		return &cb.partitions[bufNum]
	}
	return nil
}

// Sector-range validation outcomes shared by the sector commands.
type rangeResult int

const (
	rangeOK rangeResult = iota
	rangeWait
	rangeReject
)

// validateSectorRange applies the parameter check shared by the sector
// get/delete/get-then-delete/copy/move commands: an invalid buffer
// number rejects; an empty partition, an out-of-range offset, a zero
// count, or a range overrunning the partition answer WAIT (retryable).
// Offset 0xFFFF resolves to the last sector, count 0xFFFF to the rest
// of the partition from the offset; the resolved values are returned.
func (cb *CDBlock) validateSectorRange(bufNum uint8, offset, count int) (int, int, rangeResult) {
	if int(bufNum) >= len(cb.partitions) {
		return 0, 0, rangeReject
	}
	n := len(cb.partitions[bufNum].sectors)
	if n == 0 {
		return 0, 0, rangeWait
	}
	if offset >= n {
		if offset != 0xFFFF {
			return 0, 0, rangeWait
		}
		offset = n - 1
	}
	if count == 0 {
		return 0, 0, rangeWait
	}
	if count == 0xFFFF {
		count = n - offset
	} else if offset+count > n {
		return 0, 0, rangeWait
	}
	return offset, count, rangeOK
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
	secNum := int(cb.cmd[1])
	part := cb.getPartition(bufNum)
	if part == nil {
		cb.respondReject()
		return
	}
	// An empty partition or an out-of-range sector number answers
	// WAIT. Sector number 0xFFFF resolves to the last sector.
	if secNum >= len(part.sectors) {
		if secNum != 0xFFFF || len(part.sectors) == 0 {
			cb.respondWait()
			return
		}
		secNum = len(part.sectors) - 1
	}
	sec := part.sectors[secNum]
	cb.setResponse(
		uint16(sec.fad&0xFFFF),
		uint16(sec.fileNum)<<8|uint16(sec.chanNum),
		uint16(sec.submode)<<8|uint16(sec.codinfo),
	)
	cb.res[0] |= uint16((sec.fad >> 16) & 0xFF)
	cb.hirqReq |= hirqCMOK
}

// cmdSetSectorLength sets the get/put transfer sector lengths. 0xFF
// keeps the current value (traced: hosts set one direction at a time
// with 0xFF in the other, the CDC keep-current convention).
func (cb *CDBlock) cmdSetSectorLength() {
	if get := uint8(cb.cmd[0]); get != 0xFF {
		cb.getSectorLen = int(get)
	}
	if put := uint8(cb.cmd[1] >> 8); put != 0xFF {
		cb.putSectorLen = int(put)
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqESEL
}

func (cb *CDBlock) cmdGetSectorData() {
	if cb.transferActive {
		cb.respondWait()
		return
	}
	bufNum := uint8(cb.cmd[2] >> 8)
	startIdx, count, chk := cb.validateSectorRange(bufNum, int(cb.cmd[1]), int(cb.cmd[3]))
	if chk == rangeReject {
		cb.respondReject()
		return
	}
	if chk == rangeWait {
		cb.respondWait()
		return
	}
	cb.extractSectorData(cb.getPartition(bufNum), startIdx, count)
	cb.transferActive = true
	cb.dataTransferType = cdCmdGetSectorData
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqDRDY | hirqEHST
}

func (cb *CDBlock) cmdDeleteSectorData() {
	bufNum := uint8(cb.cmd[2] >> 8)
	startIdx, count, chk := cb.validateSectorRange(bufNum, int(cb.cmd[1]), int(cb.cmd[3]))
	if chk == rangeReject {
		cb.respondReject()
		return
	}
	if chk == rangeWait {
		cb.respondWait()
		return
	}
	cb.deleteSectors(&cb.partitions[bufNum], startIdx, count)
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqEHST
}

func (cb *CDBlock) cmdGetThenDelete() {
	if cb.transferActive {
		cb.respondWait()
		return
	}
	bufNum := uint8(cb.cmd[2] >> 8)
	startIdx, count, chk := cb.validateSectorRange(bufNum, int(cb.cmd[1]), int(cb.cmd[3]))
	if chk == rangeReject {
		cb.respondReject()
		return
	}
	if chk == rangeWait {
		cb.respondWait()
		return
	}
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

	// WAIT (retryable) for a host transfer already in progress, a zero
	// sector count, or a count exceeding the free buffer count; REJECT
	// only for an invalid buffer number.
	if cb.transferActive {
		cb.respondWait()
		return
	}
	if int(bufNum) >= len(cb.partitions) {
		cb.respondReject()
		return
	}
	if count == 0 || cb.totalSectors()+count > cdMaxSectors {
		cb.respondWait()
		return
	}

	cb.putBuf = nil
	cb.putBufNum = bufNum
	cb.putSectorsRemaining = count
	cb.putWords = 0
	cb.transferActive = true
	cb.dataTransferType = cdCmdPutSectorData
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqDRDY
}

func (cb *CDBlock) cmdCopySectorData() {
	dstFilter := uint8(cb.cmd[0] & 0xFF)
	srcBuf := uint8(cb.cmd[2] >> 8)
	if int(dstFilter) >= len(cb.filters) {
		cb.respondReject()
		return
	}
	startIdx, count, chk := cb.validateSectorRange(srcBuf, int(cb.cmd[1]), int(cb.cmd[3]))
	if chk == rangeReject {
		cb.respondReject()
		return
	}
	// A copy exceeding the free buffer count answers WAIT; moves free
	// their source sectors and skip this check.
	if chk == rangeWait || cb.totalSectors()+count > cdMaxSectors {
		cb.respondWait()
		return
	}
	part := &cb.partitions[srcBuf]
	for i := startIdx; i < startIdx+count; i++ {
		sec := part.sectors[i]
		cp := make([]byte, len(sec.data))
		copy(cp, sec.data)
		sec.data = cp
		cb.routeSector(sec, dstFilter)
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
	if int(dstFilter) >= len(cb.filters) {
		cb.respondReject()
		return
	}
	startIdx, count, chk := cb.validateSectorRange(srcBuf, int(cb.cmd[1]), int(cb.cmd[3]))
	if chk == rangeReject {
		cb.respondReject()
		return
	}
	if chk == rangeWait {
		cb.respondWait()
		return
	}
	part := &cb.partitions[srcBuf]
	end := startIdx + count
	// Remove from source before routing to handle self-referencing filters
	moved := make([]bufferedSector, count)
	copy(moved, part.sectors[startIdx:end])
	part.sectors = append(part.sectors[:startIdx], part.sectors[end:]...)
	// Route through filter chain
	for _, sec := range moved {
		cb.routeSector(sec, dstFilter)
	}
	cb.standardReturn()
	cb.resultsRead = false
	if cb.totalSectors() >= cdMaxSectors {
		cb.hirqReq |= hirqBFUL
	}
	cb.hirqReq |= hirqCMOK | hirqECPY
}
