// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// MPEG (Video CD) cartridge host commands $90-$AF. The card hangs off
// the CD block (rear expansion slot, SH-1 bus); the host sees it only
// through these commands plus HIRQ bits MPED/MPCM/MPST.
//
// Model coverage: the subsystem implements the command surface and
// decode pipeline that traced movie playback exercises; the rest of
// the card is accepted-but-inert or absent.
//
// Known behavior deliberately not modeled:
//
//   - $95 transfer modes (auto vs force) and the CR4 byte: stored,
//     not acted on. Play mode is modeled only as the A/V start-sync
//     switch (independent playback skips the video start hold); decode
//     always runs and ends on EOR/EOF submode or stream end codes.
//   - $96 slow-play and strobe intervals (pause/freeze-time > 1):
//     treated as normal playback; the interval values are not kept.
//     Mute, pause (0), and freeze (0) are modeled.
//   - $94 modes other than scan mode and decode timing: movie mode
//     (still, hi-res, sector buffer) and output destination (host
//     transfer) are stored but only normal-movie decode to VDP2 EXBG
//     is implemented (VSYNC-paced or $97-stepped per decode timing).
//   - $98 Get Timecode, $99 Get PTS, $A5 (display attribute), $A6
//     (get image), the $A7-$AD sector-buffer group, and $E2 (card
//     boot ROM read): unimplemented; they answer with the standard
//     return like a missing extension handler.
//   - $97 frame-bank parameter: not modeled (no bank model); only the
//     decode step is.
//   - $A1-$A4 output configuration (display window, border color,
//     fade, video effect): stored for the EXBG render path but not
//     applied; the decoded picture maps 1:1 from the screen origin.
//   - $A0 frame bank: stored; decoded pictures go straight to the
//     frame latch with no bank model.
//   - Connection records: the layer byte (a single system-layer
//     connection feeding both decoders) and the picture-search bits
//     are not acted on - each layer needs its own partition.
//     Connection-mode bits 0x01/0x02 (gating the EOR / system-end
//     end conditions and the end switch) and 0x04 (delete sector)
//     are modeled; 0x08 (ignore PTS), 0x10/0x20 (clear VBV), and
//     0x40 (end before back aperture) are not.
//   - Stream records: the audio stream number selects among the first
//     four audio streams; video always takes the first video stream;
//     the mode bits (set vs identify) and channel numbers are ignored.
//   - Status report: the picture-info byte reads 0; operation-status
//     bit 3 (decode stopped) and the video paused/frozen/odd-field/
//     error bits are never set; audio illegal/error are never set and
//     audio buffer-empty is set only on connection teardown, not on
//     underrun or natural end.
//   - Interrupt causes: only picture start, sequence start/end,
//     stream-switch done (both layers), and audio ready are raised.
//   - MPCM asserts only in the $93 tail (the firmware also asserts it
//     from the LSI status handler and $97).
//
// Unknown (untraced) behavior, modeled by assumption where needed:
//
//   - The $95 CR4 low-byte parameter: the wire packing is known (CR1
//     low = playback mode 0 sync/1 independent, CR2 high/low = audio/
//     video transfer mode 0 auto/1 force, bit 7 = keep current), but
//     the CR4 byte's meaning is untraced - the host library always
//     sends 0xFF for it.
//   - When the firmware asserts operation-status bit 3.
//   - The $A1 frame-buffer ratio encoding and its 0x0001 CR2 byte;
//     the $A3 fade gain scale; the $A4 field layout beyond the
//     interpolation byte.
//   - Whether MPST evaluates level or edge against the cause mask
//     (modeled level: pending causes assert on $92 unmask).
//   - The VSYNC operation-interval counter's reset semantics (modeled
//     free-running from $93).
//   - The cartridge's own extension image (not dumped): the ROM
//     default handlers are the documented reference behavior; a real
//     card may override them.

// $A1 Set Window sub-parameter selectors.
const (
	mpegWinFbPos   = 0 // frame-buffer position
	mpegWinFbRatio = 1 // frame-buffer ratio
	mpegWinDispPos = 2 // display position
	mpegWinDispSiz = 3 // display size
)

// cdMpegConn is one decoder-connection record: connection-mode byte,
// layer + picture-search byte, buffer partition number. $9A/$9B carry
// one record for the audio layer and one for the video layer.
type cdMpegConn struct {
	mode   uint8
	layer  uint8
	bufNum uint8
}

// cdMpegStream is one stream-selection record: stream-mode byte,
// stream number, channel number ($9D/$9E, per layer like connections).
type cdMpegStream struct {
	mode   uint8
	stmNum uint8
	chNum  uint8
}

// cdMpeg is the MPEG subsystem state visible through the host commands.
type cdMpeg struct {
	// active is set by $93 MpegInit. The firmware gates every other
	// MPEG command on it (subsystem state bit 7 of $0F000892).
	active bool

	// intStatus is the 24-bit interrupt cause register. $91 returns
	// and clears it and intMask ($92) gates which causes assert HIRQ.
	intStatus uint32
	intMask   uint32

	// $94 Set Mode parameters. 0xFF in a command byte means keep the
	// current value.
	movieMode    uint8 // 0 normal, 1 still, 2 hi-res movie, 3 hi-res still, 4 sector buffer
	decodeTiming uint8 // 0 VSYNC-synchronized, 1 host-synchronized
	outDest      uint8 // 0 VDP2 (EXBG), 1 host transfer
	scanMode     uint8 // 0/1 NTSC, 2/3 PAL; odd = interlaced

	// $95 Play parameters. 0xFF in a command byte means keep the
	// current value.
	playMode uint8 // 0 A/V-synchronized, 1 independent playback
	tmodA    uint8 // audio decoder transfer mode: 0 auto, 1 force
	tmodV    uint8 // video decoder transfer mode: 0 auto, 1 force
	decV     uint8 // CR4 low byte; meaning untraced (hosts send 0xFF)

	// $96 Set Decode Method parameters. 0xFF (mute) / 0xFFFF (times)
	// mean keep the current value.
	audioMute uint8 // bit 0 mute right, bit 1 mute left (0x04 = default, unmuted)
	vidPaused bool  // pause-time 0: picture decode halted
	vidFrozen bool  // freeze-time 0: displayed picture held while decode continues

	// Connections and streams, indexed [selector][layer]: selector 0 =
	// current, 1 = next (CR3 high byte of $9A/$9B/$9D/$9E); layer 0 =
	// audio, 1 = video.
	conn   [2][2]cdMpegConn
	stream [2][2]cdMpegStream

	// $A0 Display.
	dispSwitch uint8
	dispBank   uint8

	// $A1 Set Window sub-parameters, indexed by the CR1 low-byte
	// selector (the mpegWin* constants). Each holds the raw
	// CR2/CR3/CR4; X is CR3 ([1]) and Y is CR4 ([2]).
	window [8][3]uint16

	borderColor uint16 // $A2
	fade        uint16 // $A3 Y/C gain
	videoEffect uint16 // $A4 interpolation/transparency/mosaic/soft

	// Decoder-LSI register shadows for $AE/$AF raw access, indexed
	// [window][register byte-offset >> 1] (window 0 = LSI A at
	// $0A100000, 1 = LSI B at $0A180000). Values written by $AF are
	// stored and read back and the known live status bit is synthesized
	// in mpegLsiRead.
	lsi [2][128]uint16

	// Decoded stream picture size, learned from the sequence header
	// ($9F reports it once a stream has been identified).
	picW uint16
	picH uint16

	// Playback engine state. play is created by $95 Play (nil until
	// then). The run states, status words, and VSYNC counter feed the
	// status report and are driven by mpegTick from decoder progress.
	play         *mpegPlayback
	vidState     uint8  // operation-status bits 0-2
	audState     uint8  // operation-status bits 4-6
	vidStatus    uint16 // video status word (CR3 of the report)
	audStatus    uint8  // audio status byte (CR2 low of the report)
	vsyncCounter uint16 // operation-interval counter (CR4 of the report)
	vsyncAccum   int    // cycle accumulator toward the next VSYNC count
}

// mpegStatusReturn fills the result registers with the MPEG status
// report, the default response of nearly every MPEG command: CR1 =
// CD status (HB) | MPEG operation-status byte (LB), CR2 = picture-info
// (HB) | audio-status (LB), CR3 = video-status word, CR4 = VSYNC
// operation-interval counter.
func (cb *CDBlock) mpegStatusReturn() {
	st := uint16(cb.status)
	if cb.peri {
		st |= 0x20
	}
	if cb.transferActive {
		st |= 0x40
	}
	opStatus := uint16(cb.mpeg.vidState)&0x07 | uint16(cb.mpeg.audState)<<4&0x70
	cb.res[0] = st<<8 | opStatus
	cb.res[1] = uint16(cb.mpeg.audStatus) // picture info (HB) | audio status (LB)
	cb.res[2] = cb.mpeg.vidStatus
	cb.res[3] = cb.mpeg.vsyncCounter
	cb.resultsRead = false
}

// cmdMpeg dispatches one MPEG command.
func (cb *CDBlock) cmdMpeg(op uint16) {
	// MpegInit bypasses the active gate because it is what sets it.
	if op == 0x93 {
		cb.cmdMpegInit()
		return
	}
	if !cb.mpeg.active {
		cb.standardReturn()
		cb.resultsRead = false
		cb.hirqReq |= hirqCMOK
		return
	}
	switch op {
	case 0x90:
		cb.cmdMpegGetStatus()
	case 0x91:
		cb.cmdMpegGetInterrupt()
	case 0x92:
		cb.cmdMpegSetInterruptMask()
	case 0x94:
		cb.cmdMpegSetMode()
	case 0x95:
		cb.cmdMpegPlay()
	case 0x96:
		cb.cmdMpegSetDecodeMethod()
	case 0x97:
		cb.cmdMpegOutDecodingSync()
	case 0x9A:
		cb.cmdMpegSetConnection()
	case 0x9B:
		cb.cmdMpegGetConnection()
	case 0x9C:
		cb.cmdMpegChangeConnection()
	case 0x9D:
		cb.cmdMpegSetStream()
	case 0x9E:
		cb.cmdMpegGetStream()
	case 0x9F:
		cb.cmdMpegGetPictureSize()
	case 0xA0:
		cb.cmdMpegDisplay()
	case 0xA1:
		cb.cmdMpegSetWindow()
	case 0xA2:
		cb.cmdMpegSetBorderColor()
	case 0xA3:
		cb.cmdMpegSetFade()
	case 0xA4:
		cb.cmdMpegSetVideoEffect()
	case 0xAE:
		cb.cmdMpegGetLsi()
	case 0xAF:
		cb.cmdMpegSetLsi()
	default:
		// $98, $99, $A5-$AD: no handler installed.
		cb.standardReturn()
		cb.resultsRead = false
	}
	cb.hirqReq |= hirqCMOK
}

// cmdMpegInit handles $93 MpegInit. Both MPED and MPCM must assert:
// the host-side init polls HIRQ & 0x1800 == 0x1800, MPCM meaning the
// post-init undefined interval is over and the status report is
// valid - true here as soon as the command completes.
func (cb *CDBlock) cmdMpegInit() {
	cb.mpeg = cdMpeg{
		active:    true,
		vidState:  mpegRunStopped,
		audState:  mpegRunStopped,
		audioMute: 0x04, // default: unmuted
	}
	cb.mpegResetLatch()
	// No decoder connections after reset: partition number 0xFF means
	// disconnected, matching the value host drivers use to disconnect.
	for sel := range cb.mpeg.conn {
		for layer := range cb.mpeg.conn[sel] {
			cb.mpeg.conn[sel][layer].bufNum = 0xFF
		}
	}
	cb.mpegStatusReturn()
	cb.hirqReq |= hirqCMOK | hirqMPED | hirqMPCM
}

// cmdMpegPlay handles $95 MpegPlay (byte packing traced from
// CDC_MpPlay: CR1 low = play mode, CR2 = audio/video transfer modes,
// CR4 low = an untraced fourth byte). A spent pipeline is replaced:
// the hardware has no per-play object - the decoder decodes whatever
// the connected partition supplies - so $95 after a $9A reconnection
// starts a new play sequence without a $93 re-init, dropping the
// previous stream's end-status bits.
func (cb *CDBlock) cmdMpegPlay() {
	mpegSetByte(&cb.mpeg.playMode, uint8(cb.cmd[0]))
	mpegSetByte(&cb.mpeg.tmodA, uint8(cb.cmd[1]>>8))
	mpegSetByte(&cb.mpeg.tmodV, uint8(cb.cmd[1]))
	mpegSetByte(&cb.mpeg.decV, uint8(cb.cmd[3]))
	if cb.mpeg.play == nil || cb.mpeg.play.spent() {
		cb.mpeg.play = newMpegPlayback()
		cb.mpeg.vidState = mpegRunStopped
		cb.mpeg.audState = mpegRunStopped
		cb.mpeg.vidStatus = 0
		cb.mpeg.audStatus = 0
	}
	if cb.mpeg.vidState == mpegRunStopped {
		cb.mpeg.vidState = mpegRunAwaitStream
	}
	if cb.mpeg.audState == mpegRunStopped {
		cb.mpeg.audState = mpegRunAwaitStream
	}
	cb.mpegStatusReturn()
}

// cmdMpegOutDecodingSync handles $97 MpegOutDecodingSync, the
// host-synchronized decode step (CR2 low byte = frame bank, not
// modeled). With $94 decode timing 1 each command arms one picture
// decode; the step is held until the decoder has a picture's worth of
// data. Pending steps are capped like the VSYNC pacing accumulator so
// a backlog cannot burst-decode. In VSYNC decode timing the command
// only returns status.
func (cb *CDBlock) cmdMpegOutDecodingSync() {
	if cb.mpeg.decodeTiming == 1 && cb.mpeg.play != nil && cb.mpeg.play.hostSteps < 4 {
		cb.mpeg.play.hostSteps++
	}
	cb.mpegStatusReturn()
}

// cmdMpegSetDecodeMethod handles $96 MpegSetDecodeMethod (byte packing
// traced from CDC_MpSetDec: CR1 low = audio mute, CR2 = pause-time
// word, CR4 = freeze-time word).
// Pause time 0 pauses, 1 plays normally, >1 is a slow-play interval
// (not modeled; treated as normal). Freeze time follows the same
// scheme with strobe playback for >1.
func (cb *CDBlock) cmdMpegSetDecodeMethod() {
	mpegSetByte(&cb.mpeg.audioMute, uint8(cb.cmd[0]))
	if cb.cmd[1] != 0xFFFF {
		cb.mpeg.vidPaused = cb.cmd[1] == 0
	}
	if cb.cmd[3] != 0xFFFF {
		cb.mpeg.vidFrozen = cb.cmd[3] == 0
	}
	cb.mpegStatusReturn()
}

// cmdMpegGetStatus handles $90 MpegGetStatus.
func (cb *CDBlock) cmdMpegGetStatus() {
	cb.mpegStatusReturn()
}

// cmdMpegGetInterrupt handles $91 MpegGetInterrupt: cause bits 23-16
// in CR1's low byte, 15-0 in CR2.
func (cb *CDBlock) cmdMpegGetInterrupt() {
	cb.mpegStatusReturn()
	cb.res[0] = cb.res[0]&0xFF00 | uint16(cb.mpeg.intStatus>>16)&0x00FF
	cb.res[1] = uint16(cb.mpeg.intStatus)
	cb.mpeg.intStatus = 0
}

// cmdMpegSetInterruptMask handles $92 MpegSetInterruptMask, packed
// like the $91 response. Pending causes are evaluated against the new
// mask: after a stream ends nothing further latches, so a host
// unmasking an already-latched cause would otherwise wait forever.
func (cb *CDBlock) cmdMpegSetInterruptMask() {
	cb.mpeg.intMask = uint32(cb.cmd[0]&0x00FF)<<16 | uint32(cb.cmd[1])
	if cb.mpeg.intStatus&cb.mpeg.intMask != 0 {
		cb.hirqReq |= hirqMPST
	}
	cb.mpegStatusReturn()
}

// mpegSetByte applies one keep-current parameter byte ($94/$95/$96):
// 0xFF means keep the current value.
func mpegSetByte(dst *uint8, v uint8) {
	if v != 0xFF {
		*dst = v
	}
}

// cmdMpegSetMode handles $94 MpegSetMode (byte packing traced from
// CDC_MpSetMode).
func (cb *CDBlock) cmdMpegSetMode() {
	mpegSetByte(&cb.mpeg.movieMode, uint8(cb.cmd[0]))
	mpegSetByte(&cb.mpeg.decodeTiming, uint8(cb.cmd[1]>>8))
	mpegSetByte(&cb.mpeg.outDest, uint8(cb.cmd[1]))
	mpegSetByte(&cb.mpeg.scanMode, uint8(cb.cmd[2]>>8))
	cb.mpegStatusReturn()
}

// cmdMpegSetConnection handles $9A MpegSetConnection (CDC_MpSetCon
// packing).
func (cb *CDBlock) cmdMpegSetConnection() {
	sel := uint8(cb.cmd[2]>>8) & 1
	cb.mpeg.conn[sel][0] = cdMpegConn{
		mode:   uint8(cb.cmd[0]),
		layer:  uint8(cb.cmd[1] >> 8),
		bufNum: uint8(cb.cmd[1]),
	}
	cb.mpeg.conn[sel][1] = cdMpegConn{
		mode:   uint8(cb.cmd[2]),
		layer:  uint8(cb.cmd[3] >> 8),
		bufNum: uint8(cb.cmd[3]),
	}
	cb.mpegStatusReturn()
}

// cmdMpegChangeConnection handles $9C MpegChangeConnection: the host
// stages records with $9A (next slot) and commits them here.
// CDC_MpChgCon's per-layer selection bytes are not modeled - both
// layers switch.
func (cb *CDBlock) cmdMpegChangeConnection() {
	cb.mpegApplyNextConnections()
	cb.mpegStatusReturn()
}

// cmdMpegGetConnection handles $9B MpegGetConnection, the mirror of
// $9A.
func (cb *CDBlock) cmdMpegGetConnection() {
	sel := uint8(cb.cmd[2]>>8) & 1
	aud := cb.mpeg.conn[sel][0]
	vid := cb.mpeg.conn[sel][1]
	cb.mpegStatusReturn()
	cb.res[0] = cb.res[0]&0xFF00 | uint16(aud.mode)
	cb.res[1] = uint16(aud.layer)<<8 | uint16(aud.bufNum)
	cb.res[2] = uint16(vid.mode)
	cb.res[3] = uint16(vid.layer)<<8 | uint16(vid.bufNum)
}

// cmdMpegSetStream handles $9D MpegSetStream, packed like $9A with
// stream records.
func (cb *CDBlock) cmdMpegSetStream() {
	sel := uint8(cb.cmd[2]>>8) & 1
	cb.mpeg.stream[sel][0] = cdMpegStream{
		mode:   uint8(cb.cmd[0]),
		stmNum: uint8(cb.cmd[1] >> 8),
		chNum:  uint8(cb.cmd[1]),
	}
	cb.mpeg.stream[sel][1] = cdMpegStream{
		mode:   uint8(cb.cmd[2]),
		stmNum: uint8(cb.cmd[3] >> 8),
		chNum:  uint8(cb.cmd[3]),
	}
	cb.mpegStatusReturn()
}

// cmdMpegGetStream handles $9E MpegGetStream: the mirror of $9D.
func (cb *CDBlock) cmdMpegGetStream() {
	sel := uint8(cb.cmd[2]>>8) & 1
	aud := cb.mpeg.stream[sel][0]
	vid := cb.mpeg.stream[sel][1]
	cb.mpegStatusReturn()
	cb.res[0] = cb.res[0]&0xFF00 | uint16(aud.mode)
	cb.res[1] = uint16(aud.stmNum)<<8 | uint16(aud.chNum)
	cb.res[2] = uint16(vid.mode)
	cb.res[3] = uint16(vid.stmNum)<<8 | uint16(vid.chNum)
}

// cmdMpegGetPictureSize handles $9F MpegGetPictureSize. Until a
// sequence header sets the real size, the normal-movie geometry for
// the configured scan mode is reported.
func (cb *CDBlock) cmdMpegGetPictureSize() {
	w, h := cb.mpeg.picW, cb.mpeg.picH
	if w == 0 || h == 0 {
		w = 352
		h = 240
		if cb.mpeg.scanMode >= 2 {
			h = 288
		}
	}
	cb.mpegStatusReturn()
	cb.res[2] = w
	cb.res[3] = h
}

// cmdMpegDisplay handles $A0 MpegDisplay.
func (cb *CDBlock) cmdMpegDisplay() {
	cb.mpeg.dispSwitch = uint8(cb.cmd[1] >> 8)
	cb.mpeg.dispBank = uint8(cb.cmd[1])
	on := cb.mpeg.dispSwitch != 0
	cb.mpegSetDisplay(on)
	// Displaying = enabled and pictures flowing. Enabled before the
	// first picture, the decode path sets it when one latches.
	if !on {
		cb.mpeg.vidStatus &^= mpegVidDisplaying
	} else if cb.mpeg.vidState == mpegRunPlaying {
		cb.mpeg.vidStatus |= mpegVidDisplaying
	}
	cb.mpegStatusReturn()
}

// cmdMpegSetWindow handles $A1 MpegSetWindow.
func (cb *CDBlock) cmdMpegSetWindow() {
	sub := uint8(cb.cmd[0]) & 7
	cb.mpeg.window[sub] = [3]uint16{cb.cmd[1], cb.cmd[2], cb.cmd[3]}
	cb.mpegStatusReturn()
}

// cmdMpegSetBorderColor handles $A2 MpegSetBorderColor.
func (cb *CDBlock) cmdMpegSetBorderColor() {
	cb.mpeg.borderColor = cb.cmd[1]
	cb.mpegStatusReturn()
}

// cmdMpegSetFade handles $A3 MpegSetFade.
func (cb *CDBlock) cmdMpegSetFade() {
	cb.mpeg.fade = cb.cmd[1]
	cb.mpegStatusReturn()
}

// cmdMpegSetVideoEffect handles $A4 MpegSetVideoEffect.
func (cb *CDBlock) cmdMpegSetVideoEffect() {
	cb.mpeg.videoEffect = cb.cmd[1]
	cb.mpegStatusReturn()
}

// mpegLsiStatusReg is LSI A register byte offset 6, the decoder status
// word, as an index into the word-indexed shadow ($AE/$AF address
// registers by byte offset).
const mpegLsiStatusReg = 6 >> 1

// mpegLsiRead returns the raw LSI register value for $AE/$AF
// read-back. Values come from the write shadow, except the LSI A
// decoder status word: bit 0x4000 reports the video decoder idle (not
// actively playing). Host drivers poll that bit after tearing down the
// decoder connections and wait for it before finishing a stop sequence.
func (cb *CDBlock) mpegLsiRead(win, reg int) uint16 {
	v := cb.mpeg.lsi[win][reg]
	if win == 0 && reg == mpegLsiStatusReg {
		if cb.mpeg.vidState != mpegRunPlaying {
			v |= 0x4000
		} else {
			v &^= 0x4000
		}
	}
	return v
}

// cmdMpegGetLsi handles $AE MpegGetLsi: CR1 bit 1 selects the LSI
// window, CR2 low byte the register (byte offset); CR4 returns the
// value.
func (cb *CDBlock) cmdMpegGetLsi() {
	win := int(cb.cmd[0]>>1) & 1
	reg := int(uint8(cb.cmd[1])&^1) >> 1
	cb.mpegStatusReturn()
	cb.res[3] = cb.mpegLsiRead(win, reg)
}

// cmdMpegSetLsi handles $AF MpegSetLsi: CR4 is written to the
// register; CR1 bit 0 selects read-back of the post-write value.
func (cb *CDBlock) cmdMpegSetLsi() {
	win := int(cb.cmd[0]>>1) & 1
	readBack := cb.cmd[0]&1 != 0
	reg := int(uint8(cb.cmd[1])&^1) >> 1
	cb.mpeg.lsi[win][reg] = cb.cmd[3]
	cb.mpegStatusReturn()
	if readBack {
		cb.res[3] = cb.mpegLsiRead(win, reg)
	}
}
