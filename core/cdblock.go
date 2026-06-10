// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

const (
	cdStatusBusy    = 0x00
	cdStatusPause   = 0x01
	cdStatusStandby = 0x02
	cdStatusPlay    = 0x03
	cdStatusSeek    = 0x04
	cdStatusScan    = 0x05 // Not used
	cdStatusOpen    = 0x06 // Not used
	cdStatusNoDisc  = 0x07
	cdStatusRetry   = 0x08 // Not used
	cdStatusError   = 0x09 // Not used
	cdStatusFatal   = 0x0A // Not used
	cdStatusReject  = 0xFF // Not used

	hirqCMOK = 1 << 0
	hirqDRDY = 1 << 1
	hirqCSCT = 1 << 2
	hirqBFUL = 1 << 3
	hirqPEND = 1 << 4
	hirqESEL = 1 << 6
	hirqEHST = 1 << 7
	hirqECPY = 1 << 8
	hirqEFLS = 1 << 9
	hirqSCDQ = 1 << 10
	hirqMPED = 1 << 11

	cdMaxSectors = 200

	// cdAudioQueueMax holds ~2 sectors of CD-DA stereo samples for the
	// EXTS path. 588 stereo pairs/sector * 2 channels * 2 sectors =
	// 2352 int16 values, ~27 ms at 44.1 kHz - enough latency tolerance
	// for normal frame jitter between CD sector pacing and SCSP sample
	// consumption.
	cdAudioQueueMax = 2352

	// cdSystemClockHz is the default system clock rate used to derive CD
	// Block timing thresholds at construction. The runtime values live
	// as fields on CDBlock and are refreshed by RecalcTiming whenever
	// the emulator's recalcTiming runs (mode change or initial
	// bring-up), so the CD block honors the actual system clock rate
	// rather than the compile-time constant. This default matches NTSC
	// 320 mode; 352 and PAL produce different rates and override via
	// RecalcTiming.
	//
	// NTSC 320: 1708 cycles/line * 263 lines * 60 fps = 26,952,240 Hz
	// NTSC 352: 1820 cycles/line * 263 lines * 60 fps = 28,719,600 Hz
	// PAL  320: 1708 cycles/line * 313 lines * 50 fps = 26,730,200 Hz
	// PAL  352: 1820 cycles/line * 313 lines * 50 fps = 28,483,000 Hz
	cdSystemClockHz = 26952240

	// "As soon as possible" sentinels for command acceptance and
	// InitCDSystem state transitions. Value 1 fires on the next
	// TickSystemCycles call since every call advances by >= 1 cycle.
	cdCmdDelay  = 1
	cdInitDelay = 1

	// Physical rate thresholds as system cycles per event.
	// SCDQ fires one subcode Q frame per sector (75 Hz at 1x, 150 Hz at 2x).
	cdSCDQCycles1x = cdSystemClockHz / 75  // ~381,818
	cdSCDQCycles2x = cdSystemClockHz / 150 // ~190,909
	cdPeriCycles   = cdSystemClockHz / 200 // ~143,182 (periodic status, 200 Hz)

	// Sector reading: 1 sector per SCDQ period at each speed.
	cdSectorCycles1x = cdSystemClockHz / 75
	cdSectorCycles2x = cdSystemClockHz / 150

	// Seek step: cycles per FAD advance. Chosen to complete seeks within
	// a few frames; real hardware seeks are mechanically bounded.
	cdSeekCycles1x = 10
	cdSeekCycles2x = 5

	// Boot and BUSY->PAUSE transitions.
	// SH-1 init: ~109 ms. BUSY->PAUSE after disc detection: ~33 ms.
	cdBootDelayCycles = cdSystemClockHz * 109 / 1000  // ~3,121,367
	cdBusyPauseCycles = cdSystemClockHz * 333 / 10000 // ~953,593

	// Command IDs used for dataTransferType tracking
	cdCmdGetTOC        = 0x02
	cdCmdGetSubcode    = 0x20
	cdCmdGetSectorData = 0x61
	cdCmdGetThenDelete = 0x63
	cdCmdPutSectorData = 0x64
	cdCmdGetFileInfo   = 0x73
)

// TrackInfo describes a single disc track for the CDBlock.
type TrackInfo struct {
	Number   int
	Type     string
	Frames   int
	Pregap   int
	StartLBA int
	Control  uint8        // CTRL/ADR byte (0x41=data, 0x01=audio)
	Indexes  []TrackIndex // index numbers >= 1, ascending by FAD
}

// TrackIndex is one exposed index point within a track: its index number
// (>= 1) and its absolute FAD. Index 0 (pregap) is not stored; it is the
// implied default for any FAD below the first entry.
type TrackIndex struct {
	Number uint8
	FAD    uint32
}

// DiscReader is the disc input to the emulator. It is primitive-only
// (no named aggregate in any signature) so a concrete reader - the
// streaming CHD reader supplied by the host - satisfies it structurally
// without this package and the host's disc/UI packages sharing a type.
// The track TOC is exposed as indexed scalar accessors; the CD block
// materializes it into []TrackInfo once via discTOC.
type DiscReader interface {
	ReadSector(lba int) ([]byte, error)
	NumTracks() int
	Track(i int) (number int, typ string, frames int, pregap int, startLBA int, control uint8)
	// NumTrackIndexes / TrackIndex expose the per-track index map (index
	// numbers >= 1, absolute LBA). Index 0 (pregap) is implied for any FAD
	// below the first entry. n is a 0-based ordinal into the exposed list.
	NumTrackIndexes(i int) int
	TrackIndex(i, n int) (indexNumber int, lba int)
}

// discTOC adapts a primitive DiscReader into the struct-based view the
// CD block consumes, building the TrackInfo slice once when the disc is
// attached rather than rebuilding it on every Tracks() call.
type discTOC struct {
	d      DiscReader
	tracks []TrackInfo
}

func newDiscTOC(d DiscReader) *discTOC {
	n := d.NumTracks()
	t := make([]TrackInfo, n)
	for i := 0; i < n; i++ {
		num, typ, frames, pregap, startLBA, control := d.Track(i)
		ni := d.NumTrackIndexes(i)
		idxs := make([]TrackIndex, ni)
		for j := 0; j < ni; j++ {
			number, lba := d.TrackIndex(i, j)
			idxs[j] = TrackIndex{Number: uint8(number), FAD: uint32(lba + 150)}
		}
		t[i] = TrackInfo{
			Number:   num,
			Type:     typ,
			Frames:   frames,
			Pregap:   pregap,
			StartLBA: startLBA,
			Control:  control,
			Indexes:  idxs,
		}
	}
	return &discTOC{d: d, tracks: t}
}

func (x *discTOC) ReadSector(lba int) ([]byte, error) { return x.d.ReadSector(lba) }
func (x *discTOC) Tracks() []TrackInfo                { return x.tracks }

type bufferedSector struct {
	data       []byte
	size       int // raw sector size in bytes
	userOffset int // offset in data where user data starts
	userSize   int // user data size (2048 Mode 1/Form 1, 2324 Form 2, or matches put size)
	fad        uint32
	fileNum    uint8 // subheader file number
	chanNum    uint8 // subheader channel number
	submode    uint8 // subheader submode
	codinfo    uint8 // subheader coding information
	isMode2    bool  // true if sector byte 15 == 0x02 (Mode 2)
}

type trackEntry struct {
	// index01FAD is the INDEX 01 (body) position: where the track's program
	// begins after any pregap. It is the seek/play landing target and the
	// origin for relative MSF.
	index01FAD uint32
	// pregapStartFAD is the first FAD physically owned by the track (start of
	// its pregap). It is the track-membership boundary used by trackAt. When
	// the track has no pregap it equals index01FAD.
	pregapStartFAD uint32
	number         uint8
	isAudio        bool
	control        uint8        // CTRL/ADR from TrackInfo
	indexes        []TrackIndex // index numbers >= 1, ascending by FAD
}

type cdFilter struct {
	mode      uint8  // active conditions (bits 0-7)
	fadStart  uint32 // FAD range start
	fadRange  uint32 // FAD range sector count
	fileNum   uint8  // file number to match
	chanNum   uint8  // channel number to match
	smmask    uint8  // submode mask
	smval     uint8  // submode value
	cimask    uint8  // coding info mask
	cival     uint8  // coding info value
	trueConn  uint8  // true output -> partition number (0xFF=disconnected)
	falseConn uint8  // false output -> filter number (0xFF=disconnected)
}

type cdPartition struct {
	sectors []bufferedSector
}

type cdcFile struct {
	fad      uint32 // file start FAD (LBA + 150)
	size     uint32 // file size in bytes
	gapSize  uint8  // interleave gap size (ISO9660 byte 27)
	unitSize uint8  // file unit size (ISO9660 byte 26)
	fileNum  uint8  // file number / file ID
	attr     uint8  // attributes flags
}

// CDBlock implements the Saturn CD Block subsystem.
// It handles command/response communication with the SH-2 via
// registers at A-Bus CS2 offset $90000.
type CDBlock struct {
	hirqReq  uint16
	hirqMask uint16
	cmd      [4]uint16 // Command registers (host writes)
	res      [4]uint16 // Result registers (host reads)

	dataBuf          []uint16
	dataPos          int
	dataTransferType uint8 // command that started current transfer (for EHST)
	delPart          *cdPartition
	delStart         int
	delCount         int

	status              uint8
	disc                *discTOC
	trackCache          []trackEntry    // sorted by FAD, built in SetDisc
	filters             [24]cdFilter    // filter conditions and connections
	partitions          [24]cdPartition // per-partition sector storage
	cdDeviceFilter      uint8           // which filter CD device feeds into (0xFF=disconnected)
	lastBufferDest      uint8           // last partition that received a sector (0xFF=none)
	calcSize            uint32          // result of CalculateActualSize
	playFAD             uint32
	startFAD            uint32 // play range start (for repeat loop-back)
	endFAD              uint32 // play range end
	curTrack            uint8  // current track (0xFF = unknown)
	repeatCount         uint8  // remaining repeats (0=done, 0x0F=endless)
	playing             bool   // drive is actively reading into buffer
	fileRead            bool   // true when playing from ReadFile (fires EFLS on completion)
	getSectorLen        int
	putSectorLen        int
	putBuf              []byte // accumulates incoming DATATRNS writes
	putBufNum           uint8  // target partition for put transfer
	putSectorsRemaining int    // sectors left to accept
	authenticated       bool
	discType            uint8 // detected disc type (0=none, 1=audio, 2=data, 4=Saturn)

	fsFiles       []cdcFile
	fsRootLBA     uint32
	fsRootSize    uint32
	fsInitialized bool

	peri           bool  // periodic status flag (bit 5 / 0x20 in status byte, 0x2000 in CR1)
	transferActive bool  // data transfer in progress (bit 6 / 0x40 in status byte)
	resultsRead    bool  // true after host reads CR4; prevents periodic overwrites of unread responses
	initialized    bool  // true after first ExecuteCommand; protects CDBLOCK signature from periodic writes
	cdSpeed        uint8 // 1 = 1x, 2 = 2x (set by InitCDSystem)

	// Internal timing state (all counts in system cycles)
	bootDelay     int    // Cycles until SH-1 init completes (0 = already done)
	sectorAccum   int    // Cycle accumulator for sector read pacing
	seekAccum     int    // Cycle accumulator for seek FAD advancement
	cmdDelay      int    // Cycles until pending command executes (0 = idle)
	seeking       bool   // True when drive is stepping toward seekFAD
	seekFAD       uint32 // Target FAD for seek
	seekTarget    uint8  // Status to transition to when seek arrives (PLAY or PAUSE)
	busyDelay     int    // Cycles until BUSY -> pendingStatus (0 = not in transition)
	pendingStatus uint8  // Target status when busyDelay expires
	scdqCounter   int    // Cycles until next SCDQ flag (75 Hz at 1x, 150 Hz at 2x)
	periCounter   int    // Cycles until next periodic status update

	// CD-DA audio queue feeding SCSP EXTS. Decoded from raw audio
	// sectors during readOneSector() when the current track is AUDIO.
	// SCSP pulls one stereo pair per 44.1 kHz sample tick.
	audioQueue [cdAudioQueueMax]int16
	audioHead  int
	audioTail  int
	audioCount int

	// Runtime CD timing periods, recomputed by RecalcTiming whenever
	// the emulator's system clock rate changes. Initialized from the
	// package constants in NewCDBlock so the CD block has sensible
	// defaults if RecalcTiming is never called (e.g. unit tests).
	sectorCycles1x  int
	sectorCycles2x  int
	scdqCycles1x    int
	scdqCycles2x    int
	periCycles      int
	bootDelayCycles int
	busyPauseCycles int

	scu *SCU // SCU reference for raising external interrupts
}

// NewCDBlock creates a new CD Block in power-on state.
// On real hardware, the CD Block (SH-1) autonomously runs its init
// sequence (~109ms). During this time it shows the "CDBLOCK" signature
// in the result registers. After init completes, it transitions to
// Busy+PERI status (with disc) or NoDisc+PERI (without disc).
func NewCDBlock(scu *SCU) *CDBlock {
	cb := &CDBlock{
		hirqReq:         0,
		hirqMask:        0,
		status:          cdStatusNoDisc,
		curTrack:        0xFF,
		cdDeviceFilter:  0xFF,
		lastBufferDest:  0xFF,
		cdSpeed:         2,
		bootDelay:       cdBootDelayCycles,
		sectorCycles1x:  cdSectorCycles1x,
		sectorCycles2x:  cdSectorCycles2x,
		scdqCycles1x:    cdSCDQCycles1x,
		scdqCycles2x:    cdSCDQCycles2x,
		periCycles:      cdPeriCycles,
		bootDelayCycles: cdBootDelayCycles,
		busyPauseCycles: cdBusyPauseCycles,
		scu:             scu,
	}
	cb.initSelectors()
	cb.res[0] = 0x0043 // status=Busy(0x00) | 'C'
	cb.res[1] = 0x4442 // 'D','B'
	cb.res[2] = 0x4C4F // 'L','O'
	cb.res[3] = 0x434B // 'C','K'
	return cb
}

// initSelectors resets all filters and partitions to default state.
func (cb *CDBlock) initSelectors() {
	for i := range cb.filters {
		cb.filters[i] = cdFilter{
			trueConn:  uint8(i),
			falseConn: 0xFF,
		}
	}
	for i := range cb.partitions {
		cb.partitions[i].sectors = nil
	}
}

// resetSelectors applies the 6-bit reset flags from command 0x48.
func (cb *CDBlock) resetSelectors(flags uint8, bufNum uint8) {
	if flags == 0 {
		// Reset single partition
		if int(bufNum) < len(cb.partitions) {
			cb.partitions[bufNum].sectors = nil
		}
		return
	}
	if flags&0x04 != 0 {
		// Bit 2: Clear all partition data
		for i := range cb.partitions {
			cb.partitions[i].sectors = nil
		}
	}
	if flags&0x10 != 0 {
		// Bit 4: Reset all filter conditions
		for i := range cb.filters {
			cb.filters[i].mode = 0
			cb.filters[i].fadStart = 0
			cb.filters[i].fadRange = 0
			cb.filters[i].fileNum = 0
			cb.filters[i].chanNum = 0
			cb.filters[i].smmask = 0
			cb.filters[i].smval = 0
			cb.filters[i].cimask = 0
			cb.filters[i].cival = 0
		}
	}
	if flags&0x20 != 0 {
		// Bit 5: Disconnect all filter inputs (CD device connection)
		cb.cdDeviceFilter = 0xFF
	}
	if flags&0x40 != 0 {
		// Bit 6: Reset all true output connectors to default (filter N -> partition N)
		for i := range cb.filters {
			cb.filters[i].trueConn = uint8(i)
		}
	}
	if flags&0x80 != 0 {
		// Bit 7: Disconnect all false output connectors
		for i := range cb.filters {
			cb.filters[i].falseConn = 0xFF
		}
	}
}

// totalSectors returns the total number of sectors across all partitions.
func (cb *CDBlock) totalSectors() int {
	total := 0
	for i := range cb.partitions {
		total += len(cb.partitions[i].sectors)
	}
	return total
}

// routeSector routes a sector through the filter chain starting at filterNum.
func (cb *CDBlock) routeSector(sec bufferedSector, filterNum uint8) {
	for depth := 0; depth < 24; depth++ {
		if int(filterNum) >= len(cb.filters) {
			return // invalid filter, discard
		}
		f := &cb.filters[filterNum]
		match := true

		// Check active conditions based on mode bits
		if f.mode&0x40 != 0 {
			// Bit 6: FAD range check
			if sec.fad < f.fadStart || sec.fad >= f.fadStart+f.fadRange {
				match = false
			}
		}

		// Subheader checks only apply to Mode 2 sectors (byte 15 == 0x02)
		subMatch := true
		if sec.isMode2 && (f.mode&0x0F) != 0 {
			if f.mode&0x01 != 0 {
				// Bit 0: file number
				if sec.fileNum != f.fileNum {
					subMatch = false
				}
			}
			if f.mode&0x02 != 0 {
				// Bit 1: channel number
				if sec.chanNum != f.chanNum {
					subMatch = false
				}
			}
			if f.mode&0x04 != 0 {
				// Bit 2: submode
				if sec.submode&f.smmask != f.smval {
					subMatch = false
				}
			}
			if f.mode&0x08 != 0 {
				// Bit 3: coding information
				if sec.codinfo&f.cimask != f.cival {
					subMatch = false
				}
			}
			if f.mode&0x10 != 0 {
				// Bit 4: reverse subheader result
				subMatch = !subMatch
			}
		}
		if !subMatch {
			match = false
		}

		if match {
			// Route to true connection (partition)
			if f.trueConn != 0xFF && int(f.trueConn) < len(cb.partitions) {
				cb.partitions[f.trueConn].sectors = append(cb.partitions[f.trueConn].sectors, sec)
				cb.lastBufferDest = f.trueConn
			}
			return
		}
		// Route to false connection (next filter)
		if f.falseConn == 0xFF {
			return // disconnected, discard
		}
		filterNum = f.falseConn
	}
}

// checkIRQ raises SCU External Interrupt 0 (IST bit 16, vector 0x50,
// level 0x7) when any unmasked HIRQ bit is set.
func (cb *CDBlock) checkIRQ() {
	if cb.scu == nil {
		return
	}
	if cb.hirqReq&cb.hirqMask != 0 {
		cb.scu.RaiseInterrupt(16) // External Interrupt 0
	}
}

// SetDisc attaches a disc reader. Pass nil to eject.
// With a disc present the drive is immediately ready (Pause) since
// emulated reads are instant - no spin-up time needed.
func (cb *CDBlock) SetDisc(d DiscReader) {
	cb.trackCache = nil
	cb.discType = 0
	cb.DrainAudio()
	if d != nil {
		cb.disc = newDiscTOC(d)
		cb.status = cdStatusPause
		tracks := cb.disc.Tracks()
		cb.trackCache = make([]trackEntry, len(tracks))
		for i, tr := range tracks {
			// StartLBA points at the start of the track's pregap (CHTR
			// Frames includes pregap per chd/metadata.go). index01FAD skips
			// the pregap so PlayDisc / SeekDisc lands on the first audio (or
			// data) sample, not the silent inter-track lead-in; pregapStartFAD
			// keeps the pregap so trackAt attributes it to this track. Track 1
			// typically has Pregap=0, making the two equal.
			cb.trackCache[i] = trackEntry{
				index01FAD:     uint32(tr.StartLBA + tr.Pregap + 150),
				pregapStartFAD: uint32(tr.StartLBA + 150),
				number:         uint8(tr.Number),
				isAudio:        tr.Type == "AUDIO",
				control:        tr.Control,
				indexes:        tr.Indexes,
			}
		}
		cb.detectDiscType()
	} else {
		cb.disc = nil
		cb.status = cdStatusNoDisc
	}
}

// detectDiscType examines the disc tracks and first data sector to
// determine the disc category for GetAuthStatus.
//   - 0x01 = audio CD (no data tracks)
//   - 0x02 = data CD, not Saturn (no Saturn signature)
//   - 0x04 = Saturn original (signature matches)
func (cb *CDBlock) detectDiscType() {
	if cb.disc == nil || len(cb.trackCache) == 0 {
		cb.discType = 0
		return
	}

	// Find first data track
	tracks := cb.disc.Tracks()
	hasData := false
	var firstDataLBA int
	for i, tr := range tracks {
		if !cb.trackCache[i].isAudio {
			hasData = true
			firstDataLBA = tr.StartLBA + tr.Pregap
			break
		}
	}

	if !hasData {
		cb.discType = 1 // Audio CD
		return
	}

	// Read first data sector and check for Saturn signature
	raw, err := cb.disc.ReadSector(firstDataLBA)
	if err != nil || len(raw) < 16+16 {
		cb.discType = 2
		return
	}

	// User data starts at byte 16 in a raw 2352-byte sector
	if string(raw[16:16+16]) == "SEGA SEGASATURN " {
		cb.discType = 4 // Saturn original
	} else {
		cb.discType = 2 // Data CD, not Saturn
	}
}

// ReadDataTRNS returns the next 16-bit word from the data transfer buffer.
// Called once per word read by the bus layer, not per byte.
func (cb *CDBlock) ReadDataTRNS() uint16 {
	if cb.dataBuf != nil && cb.dataPos < len(cb.dataBuf) {
		v := cb.dataBuf[cb.dataPos]
		cb.dataPos++
		return v
	}
	return 0
}

// ReadDataTRNS32 returns two consecutive 16-bit FIFO words as a 32-bit value.
func (cb *CDBlock) ReadDataTRNS32() uint32 {
	var hi, lo uint16
	if cb.dataBuf != nil && cb.dataPos < len(cb.dataBuf) {
		hi = cb.dataBuf[cb.dataPos]
		cb.dataPos++
	}
	if cb.dataBuf != nil && cb.dataPos < len(cb.dataBuf) {
		lo = cb.dataBuf[cb.dataPos]
		cb.dataPos++
	}
	return uint32(hi)<<16 | uint32(lo)
}

// Read returns the 16-bit value at the given register offset.
func (cb *CDBlock) Read(offset uint32) uint16 {
	switch offset {
	case 0x0000:
		// DATATRNS - use ReadDataTRNS via bus layer
		return 0
	case 0x0004:
		// DATASTAT: bit0=DIR, bit1=FUL, bit2=EMP
		if cb.dataBuf == nil || cb.dataPos >= len(cb.dataBuf) {
			return 0x04 // EMP
		}
		return 0
	case 0x0008:
		return cb.hirqReq
	case 0x000C:
		return cb.hirqMask
	case 0x0018:
		return cb.res[0]
	case 0x001C:
		return cb.res[1]
	case 0x0020:
		return cb.res[2]
	case 0x0024:
		v := cb.res[3]
		cb.resultsRead = true
		return v
	default:
		return 0
	}
}

// Write sets the 16-bit value at the given register offset.
func (cb *CDBlock) Write(offset uint32, val uint16) {
	switch offset {
	case 0x0000:
		// DATATRNS write - accept data during put transfer
		if cb.putSectorsRemaining > 0 {
			cb.putBuf = append(cb.putBuf, uint8(val>>8), uint8(val))
			cb.checkPutSector()
		}
	case 0x0008:
		// HIRQREQ: write-0-to-clear
		cb.hirqReq &= val
	case 0x000C:
		cb.hirqMask = val
		cb.checkIRQ()
	case 0x0018:
		cb.cmd[0] = val
	case 0x001C:
		cb.cmd[1] = val
	case 0x0020:
		cb.cmd[2] = val
	case 0x0024:
		cb.cmd[3] = val
		cb.QueueCommand()
	}
}

// Write32 handles a 32-bit write as two consecutive 16-bit writes.
func (cb *CDBlock) Write32(offset uint32, val uint32) {
	cb.Write(offset, uint16(val>>16))
	cb.Write(offset+4, uint16(val))
}

// checkPutSector checks if enough data has been written via DATATRNS
// to complete a sector, and if so packages and stores it.
func (cb *CDBlock) checkPutSector() {
	sectorBytes := [4]int{2048, 2336, 2340, 2352}
	needed := sectorBytes[cb.putSectorLen&3]
	if len(cb.putBuf) < needed {
		return
	}

	// Store data at offset 0 with actual size
	raw := make([]byte, needed)
	copy(raw, cb.putBuf[:needed])

	sec := bufferedSector{
		data:       raw,
		size:       needed,
		userOffset: 0,
		userSize:   needed,
	}

	if part := cb.getPartition(cb.putBufNum); part != nil {
		part.sectors = append(part.sectors, sec)
		cb.lastBufferDest = cb.putBufNum
	}

	cb.putBuf = cb.putBuf[needed:]
	cb.putSectorsRemaining--
	cb.hirqReq |= hirqCSCT
}

// ExecuteCommand dispatches the command in CR1 bits 15-8.
func (cb *CDBlock) ExecuteCommand() {
	// If a command arrives during boot delay, complete boot immediately
	if cb.bootDelay > 0 {
		cb.bootDelay = 0
		cb.peri = true
		cb.resultsRead = true
		if cb.disc != nil {
			cb.status = cdStatusPause
		}
	}
	cb.initialized = true
	cmd := cb.cmd[0] >> 8
	switch cmd {
	case 0x00:
		cb.cmdGetCDStatus()
	case 0x01:
		cb.cmdGetHardwareInfo()
	case 0x02:
		cb.cmdGetTOC()
	case 0x03:
		cb.cmdGetSessionInfo()
	case 0x04:
		cb.cmdInitCDSystem()
	case 0x06:
		cb.cmdEndDataTransfer()
	case 0x10:
		cb.cmdPlayDisc()
	case 0x11:
		cb.cmdSeekDisc()
	case 0x12:
		cb.cmdScanDisc()
	case 0x20:
		cb.cmdGetSubcode()
	case 0x30:
		cb.cmdSetCDDeviceConnection()
	case 0x31:
		cb.cmdGetCDDeviceConnection()
	case 0x32:
		cb.cmdGetLastBufferDest()
	case 0x40:
		cb.cmdSetFilterRange()
	case 0x41:
		cb.cmdGetFilterRange()
	case 0x42:
		cb.cmdSetFilterSubheaderConditions()
	case 0x43:
		cb.cmdGetFilterSubheaderConditions()
	case 0x44:
		cb.cmdSetFilterMode()
	case 0x45:
		cb.cmdGetFilterMode()
	case 0x46:
		cb.cmdSetFilterConnection()
	case 0x47:
		cb.cmdGetFilterConnection()
	case 0x48:
		cb.cmdResetSelector()
	case 0x50:
		cb.cmdGetBufferSize()
	case 0x51:
		cb.cmdGetSectorNumber()
	case 0x52:
		cb.cmdCalculateActualSize()
	case 0x53:
		cb.cmdGetActualSize()
	case 0x54:
		cb.cmdGetSectorInfo()
	case 0x60:
		cb.cmdSetSectorLength()
	case 0x61:
		cb.cmdGetSectorData()
	case 0x62:
		cb.cmdDeleteSectorData()
	case 0x63:
		cb.cmdGetThenDelete()
	case 0x64:
		cb.cmdPutSectorData()
	case 0x65:
		cb.cmdCopySectorData()
	case 0x66:
		cb.cmdMoveSectorData()
	case 0x67:
		cb.cmdGetCopyError()
	case 0x70:
		cb.cmdChangeDirectory()
	case 0x71:
		cb.cmdReadDirectory()
	case 0x72:
		cb.cmdGetFileSystemScope()
	case 0x73:
		cb.cmdGetFileInfo()
	case 0x74:
		cb.cmdReadFile()
	case 0x75:
		cb.cmdAbortFile()
	case 0xE0:
		cb.cmdAuthenticateDisc()
	case 0xE1:
		cb.cmdGetAuthStatus()
	default:
		cb.standardReturn()
		cb.resultsRead = false
		cb.hirqReq |= hirqCMOK
	}
	cb.checkIRQ()
}

func (cb *CDBlock) setResponse(cr1, cr2, cr3 uint16) {
	st := uint16(cb.status)
	if cb.peri {
		st |= 0x20
	}
	if cb.transferActive {
		st |= 0x40
	}
	cb.res[0] = st << 8
	cb.res[1] = cr1
	cb.res[2] = cr2
	cb.res[3] = cr3
	cb.resultsRead = false
}

// reportFAD returns the pickup position used in every host-visible
// position report: status reports, GetCDStatus, and subcode. While a
// seek is in progress the destination FAD is reported.
func (cb *CDBlock) reportFAD() uint32 {
	if cb.seeking {
		return cb.seekFAD
	}
	return cb.playFAD
}

// standardReturn fills the result registers with full status info
// including track number and FAD position. This is the standard
// response format for most CD Block commands.
func (cb *CDBlock) standardReturn() {
	fad := cb.reportFAD()
	trackNum := uint8(0xFF)
	ctrlAddr := uint8(0xFF)
	trackIdx := uint8(1)
	if cb.disc != nil && cb.curTrack != 0xFF {
		tr := cb.trackAt(fad)
		if tr != nil {
			trackNum = tr.number
			ctrlAddr = tr.control
			trackIdx = fadToIndex(tr, fad)
		}
	}
	st := uint16(cb.status)
	if cb.peri {
		st |= 0x20
	}
	if cb.transferActive {
		st |= 0x40
	}
	cb.res[0] = st<<8 | uint16(cb.cdFlag())<<4 | uint16(cb.repeatCount)
	cb.res[1] = (uint16(ctrlAddr) << 8) | uint16(trackNum)
	cb.res[2] = (uint16(trackIdx) << 8) | uint16(fad>>16)
	cb.res[3] = uint16(fad & 0xFFFF)
}

// trackAt returns the track entry containing the given FAD, or nil if none.
// Membership uses pregapStartFAD so a track's pregap is attributed to that
// track (not the previous one), matching CD subchannel reporting.
func (cb *CDBlock) trackAt(fad uint32) *trackEntry {
	for i := len(cb.trackCache) - 1; i >= 0; i-- {
		if fad >= cb.trackCache[i].pregapStartFAD {
			return &cb.trackCache[i]
		}
	}
	return nil
}

// fadToIndex returns the reported index number for a FAD within a track. It
// starts at the implicit floor of 0 and rises to the highest exposed index
// (number >= 1) whose FAD the play head has reached. The entries are ascending
// by FAD, so a FAD at or past the track body yields 1, and a multi-indexed
// audio track yields 2+ as playback advances.
func fadToIndex(tr *trackEntry, fad uint32) uint8 {
	idx := uint8(0)
	for _, e := range tr.indexes {
		if fad >= e.FAD {
			idx = e.Number
		} else {
			break
		}
	}
	return idx
}

// cdFlag returns the 4-bit flag field for CR1 status reports.
// Bit 3 (0x8) = CD-ROM data track, 0 = CD-DA audio.
// Shifted left by 4 in standardReturn, putting it at bit 7 of CR1 low byte.
func (cb *CDBlock) cdFlag() uint8 {
	tr := cb.trackAt(cb.reportFAD())
	if tr == nil {
		return 0
	}
	if tr.isAudio {
		return 0x0
	}
	return 0x8
}

// TickSystemCycles advances the CD Block by the given number of system
// cycles. Processes all internal timers: boot delay, command delay,
// BUSY transitions, seek, sector read pacing, SCDQ and periodic status.
func (cb *CDBlock) TickSystemCycles(cycles uint32) {
	c := int(cycles)

	// 0. Boot delay: SH-1 init sequence
	if cb.bootDelay > 0 {
		cb.bootDelay -= c
		if cb.bootDelay <= 0 {
			cb.bootDelay = 0
			cb.initialized = true
			cb.peri = true
			cb.resultsRead = true
			if cb.disc != nil {
				cb.status = cdStatusBusy
				// Transition Busy -> Pause after disc detection (~33 ms)
				cb.busyDelay = cb.busyPauseCycles
				cb.pendingStatus = cdStatusPause
			} else {
				cb.status = cdStatusNoDisc
			}
			cb.hirqReq |= hirqCMOK
			cb.standardReturn()
			cb.periCounter = cb.periCycles
			cb.checkIRQ()
		}
		return
	}

	// 1. Command delay: execute pending command when timer expires
	if cb.cmdDelay > 0 {
		cb.cmdDelay -= c
		if cb.cmdDelay <= 0 {
			cb.cmdDelay = 0
			cb.ExecuteCommand()
		}
	}

	// 2. BUSY transition: change to target status when timer expires
	if cb.busyDelay > 0 {
		cb.busyDelay -= c
		if cb.busyDelay <= 0 {
			cb.busyDelay = 0
			cb.status = cb.pendingStatus
			cb.pendingStatus = 0
		}
	}

	// 3. Seek step: advance playFAD toward seekFAD one FAD per seek period
	if cb.seeking && cb.status == cdStatusSeek {
		period := cdSeekCycles1x
		if cb.cdSpeed == 2 {
			period = cdSeekCycles2x
		}
		cb.seekAccum += c
		for cb.seekAccum >= period && cb.playFAD != cb.seekFAD {
			cb.seekAccum -= period
			if cb.playFAD < cb.seekFAD {
				cb.playFAD++
			} else {
				cb.playFAD--
			}
		}
		if cb.playFAD == cb.seekFAD {
			cb.seeking = false
			cb.status = cb.seekTarget
			cb.sectorAccum = 0
			cb.seekAccum = 0
			if tr := cb.trackAt(cb.playFAD); tr != nil {
				cb.curTrack = tr.number
			}
		}
	}

	// 4. Sector reading: integer-countdown accumulator at CD speed rate.
	// Accumulate before checking play range exhaustion so the last sector
	// is available in the partition before status transitions to Pause.
	// Audio tracks always read at 1x because CD-DA is time-locked to
	// 44.1 kHz; 2x audio reads would outpace SCSP consumption.
	if cb.playing && cb.status == cdStatusPlay {
		period := cb.sectorCycles1x
		if cb.cdSpeed == 2 && !cb.currentTrackIsAudio() {
			period = cb.sectorCycles2x
		}
		cb.sectorAccum += c
		for cb.sectorAccum >= period {
			cb.sectorAccum -= period
			cb.readOneSector()
		}
	}

	// 5. SCDQ: one Q frame per sector, 75 Hz at 1x, 150 Hz at 2x.
	if cb.disc != nil && cb.initialized {
		cb.scdqCounter -= c
		if cb.scdqCounter <= 0 {
			cb.hirqReq |= hirqSCDQ
			if cb.cdSpeed == 2 {
				cb.scdqCounter += cb.scdqCycles2x
			} else {
				cb.scdqCounter += cb.scdqCycles1x
			}
		}
	}

	// 6. Periodic status: update response registers at periodic rate.
	// Skipped when a command is pending to prevent overwriting CR registers
	// between the game's CR1-CR4 write sequence.
	if cb.initialized && cb.resultsRead && cb.cmdDelay <= 0 {
		cb.periCounter -= c
		if cb.periCounter <= 0 {
			cb.peri = true
			cb.standardReturn()
			cb.peri = false
			cb.periCounter += cb.periCycles
		}
	}

	cb.checkIRQ()
}

// currentTrackIsAudio reports whether the track containing playFAD is
// an audio (CD-DA) track.
func (cb *CDBlock) currentTrackIsAudio() bool {
	if tr := cb.trackAt(cb.playFAD); tr != nil {
		return tr.isAudio
	}
	return false
}

// appendAudioSamples decodes 588 stereo LE int16 sample pairs from a
// 2352-byte CD-DA audio sector and appends them to the audio queue.
// Byte order is little-endian (low byte first), the canonical disc-image
// order romloader returns from ReadSector.
func (cb *CDBlock) appendAudioSamples(raw []byte) {
	if len(raw) < 2352 {
		return
	}
	for i := 0; i < 588; i++ {
		base := i * 4
		l := int16(uint16(raw[base]) | uint16(raw[base+1])<<8)
		r := int16(uint16(raw[base+2]) | uint16(raw[base+3])<<8)
		cb.pushAudioPair(l, r)
	}
}

// pushAudioPair appends one stereo sample pair to the queue. Drops
// the oldest pair when full so the CD state machine never stalls.
func (cb *CDBlock) pushAudioPair(l, r int16) {
	if cb.audioCount >= cdAudioQueueMax {
		cb.audioHead = (cb.audioHead + 2) % cdAudioQueueMax
		cb.audioCount -= 2
	}
	cb.audioQueue[cb.audioTail] = l
	cb.audioQueue[(cb.audioTail+1)%cdAudioQueueMax] = r
	cb.audioTail = (cb.audioTail + 2) % cdAudioQueueMax
	cb.audioCount += 2
}

// PopAudioSample returns the next stereo pair from the audio queue.
// Returns valid=false when the queue is empty (caller treats as silence).
// Implements the CDAudioSource interface in core/scsp.go.
func (cb *CDBlock) PopAudioSample() (left, right int16, valid bool) {
	if cb.audioCount < 2 {
		return 0, 0, false
	}
	left = cb.audioQueue[cb.audioHead]
	right = cb.audioQueue[(cb.audioHead+1)%cdAudioQueueMax]
	cb.audioHead = (cb.audioHead + 2) % cdAudioQueueMax
	cb.audioCount -= 2
	return left, right, true
}

// DrainAudio empties the audio queue. Called by SCSP on reset transitions
// and by the CD block itself on cmdInitCDSystem and SetDisc.
func (cb *CDBlock) DrainAudio() {
	cb.audioHead = 0
	cb.audioTail = 0
	cb.audioCount = 0
}

// RecalcTiming refreshes the CD block's cycle thresholds from the
// emulator's current system clock rate. Called by Emulator.recalcTiming
// at construction and whenever VDP2 mode changes, so CD pacing tracks
// the actual system clock rate (NTSC/PAL, 320/352) rather than a fixed
// compile-time assumption.
//
// All physical CD events (sector reads, SCDQ frames, periodic status,
// boot/busy delays) are scaled from the new rate. In-flight countdowns
// (bootDelay, busyDelay, scdqCounter, periCounter, sectorAccum,
// seekAccum) are intentionally left untouched: a mode change mid-flight
// would otherwise warp pending events forward or backward, which is
// worse than letting them complete on the prior rate. New events use
// the refreshed values.
func (cb *CDBlock) RecalcTiming(systemCyclesPerSecond uint32) {
	if systemCyclesPerSecond == 0 {
		return
	}
	// Widen to uint64 so systemCyclesPerSecond * 333 (up to ~9.6e9 at
	// 352 mode) does not overflow before the divide. uint32 would wrap.
	h := uint64(systemCyclesPerSecond)
	cb.sectorCycles1x = int(h / 75)
	cb.sectorCycles2x = int(h / 150)
	cb.scdqCycles1x = int(h / 75)
	cb.scdqCycles2x = int(h / 150)
	cb.periCycles = int(h / 200)
	cb.bootDelayCycles = int(h * 109 / 1000)
	cb.busyPauseCycles = int(h * 333 / 10000)
}

// readOneSector reads a single sector from disc at the current playFAD,
// routes it through the filter chain, and advances playFAD.
func (cb *CDBlock) readOneSector() {
	if cb.disc == nil {
		return
	}

	// End-of-play-range is checked before filter / buffer-full guards
	// because the physical play head advances independently of where
	// data is being routed. A host that disconnects the device filter
	// after issuing a finite-range PlayDisc still expects status to
	// transition to PAUSE when the range completes.
	// Two cases: explicit end position (endFAD > 0) and continuous
	// play to disc end (endFAD == 0).
	endReached := false
	if cb.endFAD > 0 && cb.playFAD >= cb.endFAD {
		endReached = true
	} else if cb.endFAD == 0 && cb.playFAD >= cb.leadoutFAD() {
		endReached = true
	}
	if endReached {
		if cb.repeatCount == 0x0F {
			cb.playFAD = cb.startFAD
		} else if cb.repeatCount > 0 {
			cb.repeatCount--
			cb.playFAD = cb.startFAD
		} else {
			cb.playing = false
			cb.status = cdStatusPause
			cb.hirqReq |= hirqPEND
			if cb.fileRead {
				cb.hirqReq |= hirqEFLS
				cb.fileRead = false
			}
			return
		}
	}

	tr := cb.trackAt(cb.playFAD)
	isAudio := tr != nil && tr.isAudio

	// Data sector with no filter destination: drop it but advance the
	// play head so the next tick can re-evaluate endFAD. Audio sectors
	// bypass the device filter via the SCSP EXTS path.
	if !isAudio && cb.cdDeviceFilter == 0xFF {
		cb.playFAD++
		return
	}
	// Partition buffer full gates data sectors only. The play head
	// stalls until the host drains a sector, matching real hardware.
	// Audio uses its own circular queue with drop-oldest overflow.
	if !isAudio && cb.totalSectors() >= cdMaxSectors {
		cb.hirqReq |= hirqBFUL
		return
	}
	lba := int(cb.playFAD) - 150
	data, err := cb.disc.ReadSector(lba)
	if err != nil {
		cb.playFAD++
		return
	}
	cp := make([]byte, len(data))
	copy(cp, data)

	// Audio track: decode samples into the EXTS audio queue and skip
	// the data partition filter chain. Audio sectors have no header
	// or subheader and must not be routed through routeSector().
	if isAudio {
		cb.appendAudioSamples(cp[:2352])
		cb.hirqReq |= hirqCSCT
		cb.curTrack = tr.number
		cb.playFAD++
		return
	}

	var fileNum, chanNum, submode, codinfo uint8
	var mode2 bool
	if len(cp) >= 20 && cp[15] == 0x02 {
		mode2 = true
		fileNum = cp[16]
		chanNum = cp[17]
		submode = cp[18]
		codinfo = cp[19]
	}

	var userOffset, userSize int
	if mode2 {
		userOffset = 24 // sync(12) + header(4) + subheader(8)
		if submode&0x20 != 0 {
			userSize = 2324 // Form 2
		} else {
			userSize = 2048 // Form 1
		}
	} else {
		userOffset = 16 // sync(12) + header(4)
		userSize = 2048 // Mode 1
	}

	sec := bufferedSector{
		data: cp, size: len(cp), fad: cb.playFAD,
		userOffset: userOffset, userSize: userSize,
		fileNum: fileNum, chanNum: chanNum,
		submode: submode, codinfo: codinfo, isMode2: mode2,
	}
	cb.routeSector(sec, cb.cdDeviceFilter)
	cb.hirqReq |= hirqCSCT
	if tr != nil {
		cb.curTrack = tr.number
	}
	cb.playFAD++
}

// QueueCommand executes the command immediately.
// Called from the bus layer when CR4 is written.
func (cb *CDBlock) QueueCommand() {
	cb.cmdDelay = 1
}
