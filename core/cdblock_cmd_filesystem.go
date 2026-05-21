// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "fmt"

// buildFileInfoWords packs a cdcFile into 6 big-endian words (12 bytes):
// FAD(4), size(4), gap(1), unit(1), filenum(1), attr(1).
func buildFileInfoWords(f *cdcFile) [6]uint16 {
	return [6]uint16{
		uint16(f.fad >> 16),
		uint16(f.fad & 0xFFFF),
		uint16(f.size >> 16),
		uint16(f.size & 0xFFFF),
		uint16(f.gapSize)<<8 | uint16(f.unitSize),
		uint16(f.fileNum)<<8 | uint16(f.attr),
	}
}

// parseVolumeDescriptor reads the Primary Volume Descriptor from LBA 16
// and extracts the root directory record location and size.
func (cb *CDBlock) parseVolumeDescriptor() bool {
	if cb.disc == nil {
		return false
	}
	raw, err := cb.disc.ReadSector(16)
	if err != nil {
		fmt.Printf("[CD] parseVolumeDescriptor ReadSector(16) error: %v\n", err)
	}
	if err != nil || len(raw) < 16+256 {
		return false
	}
	ud := raw[16:] // user data starts at offset 16 in raw 2352-byte sector

	// Verify "CD001" identifier at bytes 1-5
	if ud[1] != 'C' || ud[2] != 'D' || ud[3] != '0' || ud[4] != '0' || ud[5] != '1' {
		return false
	}

	// Root directory record is at byte 156 of the volume descriptor
	root := ud[156:]
	cb.fsRootLBA = uint32(root[2]) | uint32(root[3])<<8 | uint32(root[4])<<16 | uint32(root[5])<<24
	cb.fsRootSize = uint32(root[10]) | uint32(root[11])<<8 | uint32(root[12])<<16 | uint32(root[13])<<24
	cb.fsInitialized = true
	return true
}

// parseDirectory reads directory extent sectors and returns file entries.
// The Saturn CD block file info record packs fileNum into a single byte,
// and file IDs 0 and 1 are reserved for the current and parent directory,
// so at most 254 entries (IDs 2..255) can be represented.
func (cb *CDBlock) parseDirectory(lba, size uint32) []cdcFile {
	if cb.disc == nil {
		return nil
	}
	sectorCount := (size + 2047) / 2048
	var files []cdcFile

	for s := uint32(0); s < sectorCount; s++ {
		if len(files) >= 254 {
			break
		}
		raw, err := cb.disc.ReadSector(int(lba + s))
		if err != nil {
			fmt.Printf("[CD] parseDirectory ReadSector(%d) error: %v\n", lba+s, err)
		}
		if err != nil || len(raw) < 16+2048 {
			break
		}
		ud := raw[16 : 16+2048]
		pos := 0
		for pos < 2048 {
			recLen := int(ud[pos])
			if recLen == 0 {
				// Skip to next 2048-byte boundary within this sector
				// (padding at end of sector)
				break
			}
			if pos+recLen > 2048 {
				break
			}
			rec := ud[pos : pos+recLen]
			pos += recLen

			if len(rec) < 34 {
				continue
			}

			idLen := int(rec[32])
			if idLen == 1 && (rec[33] == 0x00 || rec[33] == 0x01) {
				// Skip . and .. entries
				continue
			}

			if len(files) >= 254 {
				break
			}

			extLBA := uint32(rec[2]) | uint32(rec[3])<<8 | uint32(rec[4])<<16 | uint32(rec[5])<<24
			dataLen := uint32(rec[10]) | uint32(rec[11])<<8 | uint32(rec[12])<<16 | uint32(rec[13])<<24
			flags := rec[25]

			files = append(files, cdcFile{
				fad:      extLBA + 150,
				size:     dataLen,
				unitSize: rec[26],
				gapSize:  rec[27],
				fileNum:  uint8(len(files) + 2), // file IDs start at 2
				attr:     flags,
			})
		}
	}
	return files
}

func (cb *CDBlock) fsInit() {
	if !cb.fsInitialized {
		cb.parseVolumeDescriptor()
	}
}

func (cb *CDBlock) cmdChangeDirectory() {
	cb.fsInit()
	if !cb.fsInitialized {
		cb.standardReturn()
		cb.resultsRead = false
		cb.hirqReq |= hirqCMOK | hirqEFLS
		return
	}

	fid := uint32(cb.cmd[2]&0xFF)<<16 | uint32(cb.cmd[3])

	if fid == 0xFFFFFF {
		// Root directory
		cb.fsFiles = cb.parseDirectory(cb.fsRootLBA, cb.fsRootSize)
	} else if fid != 0 {
		idx := int(fid) - 2
		if idx >= 0 && idx < len(cb.fsFiles) && cb.fsFiles[idx].attr&0x02 != 0 {
			dirLBA := cb.fsFiles[idx].fad - 150
			cb.fsFiles = cb.parseDirectory(dirLBA, cb.fsFiles[idx].size)
		}
	}
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqEFLS
}

func (cb *CDBlock) cmdReadDirectory() {
	// Saturn CD block spec defines ReadDirectory (0x71) identically to
	// ChangeDirectory (0x70) - both parse the directory and populate
	// the file list. Some games use one, some the other.
	cb.cmdChangeDirectory()
}

func (cb *CDBlock) cmdGetFileSystemScope() {
	cb.fsInit()
	numFiles := uint16(len(cb.fsFiles))
	cb.setResponse(numFiles, 0x0100, 0x0002)
	cb.hirqReq |= hirqCMOK | hirqEFLS
}

func (cb *CDBlock) cmdGetFileInfo() {
	fid := (uint32(cb.cmd[2]&0xFF) << 16) | uint32(cb.cmd[3])

	if fid == 0x00FFFFFF {
		// Return all files via DATATRNS
		cb.dataBuf = make([]uint16, len(cb.fsFiles)*6)
		cb.dataPos = 0
		cb.transferActive = true
		cb.dataTransferType = cdCmdGetFileInfo
		for i := range cb.fsFiles {
			words := buildFileInfoWords(&cb.fsFiles[i])
			off := i * 6
			copy(cb.dataBuf[off:off+6], words[:])
		}
		cb.setResponse(uint16(len(cb.dataBuf)), 0, 0)
		cb.hirqReq |= hirqCMOK | hirqDRDY
		return
	}

	// Single file via DATATRNS (6 words)
	idx := int(fid) - 2
	if idx >= 0 && idx < len(cb.fsFiles) {
		words := buildFileInfoWords(&cb.fsFiles[idx])
		cb.dataBuf = make([]uint16, 6)
		copy(cb.dataBuf[:], words[:])
		cb.dataPos = 0
		cb.transferActive = true
		cb.dataTransferType = cdCmdGetFileInfo
		cb.setResponse(6, 0, 0)
	} else {
		cb.setResponse(0, 0, 0)
	}
	cb.hirqReq |= hirqCMOK | hirqDRDY
}

func (cb *CDBlock) cmdReadFile() {
	if cb.disc == nil {
		cb.setResponse(0, 0, 0)
		cb.hirqReq |= hirqCMOK | hirqEFLS
		return
	}

	// offset from CR1/CR2
	offset := uint32(cb.cmd[0]&0xFF)<<16 | uint32(cb.cmd[1])
	// fid from CR3/CR4
	fid := (uint32(cb.cmd[2]&0xFF) << 16) | uint32(cb.cmd[3])

	idx := int(fid) - 2
	if idx < 0 || idx >= len(cb.fsFiles) {
		cb.setResponse(0, 0, 0)
		cb.hirqReq |= hirqCMOK | hirqEFLS
		return
	}

	f := cb.fsFiles[idx]
	byteOffset := offset * 2048
	remaining := f.size
	if byteOffset < remaining {
		remaining -= byteOffset
	} else {
		remaining = 0
	}
	sectorCount := int((remaining + 2047) / 2048)

	// Route through the filter specified in CR3 HB
	cb.cdDeviceFilter = uint8(cb.cmd[2] >> 8)

	cb.startFAD = f.fad + offset
	cb.endFAD = cb.startFAD + uint32(sectorCount)
	cb.repeatCount = 0
	cb.playing = true
	cb.fileRead = true
	cb.sectorAccum = 0

	// BUSY -> SEEK -> PLAY
	cb.status = cdStatusBusy
	cb.pendingStatus = cdStatusSeek
	cb.busyDelay = cdCmdDelay
	cb.seekFAD = cb.startFAD
	cb.seeking = true
	cb.seekTarget = cdStatusPlay

	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK
}

func (cb *CDBlock) cmdAbortFile() {
	cb.playing = false
	cb.status = cdStatusPause
	cb.standardReturn()
	cb.resultsRead = false
	cb.hirqReq |= hirqCMOK | hirqEFLS
}
