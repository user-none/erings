// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/binary"
	"image/color"
	"sync/atomic"

	"github.com/gen2brain/mpeg"
)

// MPEG playback engine. Sectors routed into the connected buffer
// partitions are drained into per-layer MPEG-PS streams, demuxed, and
// decoded; a stream identified at its head as a raw video elementary
// stream instead feeds the video decoder directly (see mpegFeedLayer).
// Decode is paced against the stream's own picture rate in
// system cycles, mirroring the card's VSYNC-synchronized decode mode.
// Decoded frames are held for the VDP2 EXBG path; decoded audio feeds
// the same queue SCSP EXTS pulls for CD-DA.

const (
	mpegLayerAudio = 0
	mpegLayerVideo = 1

	// MPEG interrupt cause bits. Only the causes currently raised are
	// named here.
	mpegIntPictureStart  = 0x000100
	mpegIntSequenceEnd   = 0x000400
	mpegIntSequenceStart = 0x000800
	mpegIntVidSwitchDone = 0x000002
	mpegIntAudioReady    = 0x010000
	mpegIntAudSwitchDone = 0x020000

	// Video status word bits (MPEG Status Report Format).
	mpegVidDecoding   = 0x0001
	mpegVidDisplaying = 0x0002
	mpegVidLastShown  = 0x0010
	mpegVidUpdated    = 0x0040
	mpegVidOutReady   = 0x0100
	mpegVidFirstShown = 0x0800
	mpegVidBufEmpty   = 0x1000

	// Audio status byte bits.
	mpegAudDecoding = 0x01
	mpegAudBufEmpty = 0x10
	mpegAudLeftOut  = 0x40
	mpegAudRightOut = 0x80

	// Run states for the operation-status byte (video in bits 0-2,
	// audio in bits 4-6). The status report format names values 2 and
	// 3 "prep-1" and "prep-2": the preparation stages before and
	// after the stream is identified.
	mpegRunStopped     = 1 // no decode in progress
	mpegRunAwaitStream = 2 // prep-1: play started, stream not yet identified
	mpegRunStreamFound = 3 // prep-2: sequence header parsed, first picture pending
	mpegRunPlaying     = 4 // transferring/playing: decoded output flowing
)

// mpegLayerPhase is a decoder input path's lifecycle. Done and Halted
// are terminal until a new playback replaces the pipeline.
type mpegLayerPhase uint8

const (
	// mpegPhaseRunning: feeding, demuxing, and decoding normally.
	mpegPhaseRunning mpegLayerPhase = iota
	// mpegPhaseEnding: an end marker arrived (XA EOR/EOF sector, PS
	// end code, or video sequence end code); buffered data is still
	// draining toward the decoder flush. No new sectors are consumed:
	// data past the marker belongs to the next stream (a repeating
	// play range re-delivers the movie from the start).
	mpegPhaseEnding
	// mpegPhaseDone: the decoder flushed its last picture; the end
	// status and the sequence-end interrupt cause have been latched.
	mpegPhaseDone
	// mpegPhaseHalted: the host tore down the connection ($9C); decode
	// stopped where it was.
	mpegPhaseHalted
)

// mpegLayer is one decoder input path: the pack stream drained from a
// buffer partition, demuxed into an elementary stream. A stream
// identified as a raw video elementary stream at its first sector
// (sequence header start code instead of a pack start code) bypasses
// the pack stream and demuxer and feeds the decoder directly.
type mpegLayer struct {
	ps       *mpeg.Buffer   // multiplexed pack stream from the partition
	demux    *mpeg.Demux    // created by mpegPumpDemux or the end-of-stream drain
	es       *mpeg.Buffer   // elementary stream feeding the decoder
	fed      int            // partition sectors already fed (non-delete mode)
	phase    mpegLayerPhase // lifecycle; see the phase constants
	packScan mpegPackScan   // positional program-end scanner over the pack stream
	esScan   int            // matcher state: sequence end code in the video ES
	psEnded  bool           // SignalEnd issued on ps (Ending)
	esEnded  bool           // SignalEnd issued on es (drained, end code, or unidentifiable)
	esProbed bool           // stream type identified from the first fed sector
	esDirect bool           // raw video elementary stream: feed es, bypass ps/demux
}

// mpegPackScan walks the system layer of a pack stream positionally:
// pack and packet headers are parsed and packet payloads are skipped
// via their length fields, so payload bytes can never be mistaken for
// a start code (audio frames are not start-code-free; a traced title
// carries the end-code byte pattern inside its audio payload
// mid-movie). feed reports whether the program-end code (ISO 11172-1
// iso_11172_end_code, 00 00 01 B9) was reached at the system layer.
// The zero value is the scanning state.
type mpegPackScan struct {
	skip   int // bytes left to skip (pack header body or packet payload)
	code   int // start-code matcher: 0-2 = zero bytes matched, 3 = 00 00 01 matched
	length int // packet-length word being assembled
	lenN   int // 0 = not assembling, 1 = awaiting high byte, 2 = awaiting low byte
}

func (s *mpegPackScan) feed(payload []byte) bool {
	ended := false
	for _, b := range payload {
		if s.skip > 0 {
			s.skip--
			continue
		}
		if s.lenN > 0 {
			s.length = s.length<<8 | int(b)
			if s.lenN == 2 {
				s.skip = s.length
				s.length = 0
				s.lenN = 0
			} else {
				s.lenN++
			}
			continue
		}
		switch {
		case s.code < 2:
			if b == 0 {
				s.code++
			} else {
				s.code = 0
			}
		case s.code == 2:
			switch b {
			case 0:
				// A third zero byte still prefixes a start code.
			case 1:
				s.code = 3
			default:
				s.code = 0
			}
		default: // 00 00 01 matched; b is the start code
			s.code = 0
			switch {
			case b == 0xB9:
				ended = true
			case b == 0xBA:
				// MPEG-1 pack header: 8 bytes follow the start code.
				s.skip = 8
			case b >= 0xBB:
				// System header or packet: a 16-bit length of the
				// bytes that follow it comes next.
				s.lenN = 1
			}
			// Any other code is not system-layer: resume scanning.
		}
	}
	return ended
}

// mpegScanStartCode advances a rolling start-code matcher over payload
// looking for 00 00 01 <code>, returning the index just past the match
// or -1. The state survives across calls so codes split over sector or
// packet boundaries still match. States: 0-2 = zeros seen, 3 = 00 00 01
// seen.
func mpegScanStartCode(state *int, payload []byte, code byte) int {
	for i, b := range payload {
		switch {
		case *state == 3:
			if b == code {
				*state = 0
				return i + 1
			}
			if b == 0x00 {
				*state = 1
			} else {
				*state = 0
			}
		case b == 0x00:
			if *state < 2 {
				*state++
			}
		case b == 0x01 && *state == 2:
			*state = 3
		default:
			*state = 0
		}
	}
	return -1
}

// mpegPlayback owns the decode pipeline for one Play sequence.
type mpegPlayback struct {
	layers [2]mpegLayer
	video  *mpeg.Video
	audio  *mpeg.Audio

	pictAccum  int // cycle accumulator toward the next picture (VSYNC decode timing)
	pictCycles int // cycles per picture; 0 until the sequence header
	hostSteps  int // pending $97 decode steps (host-synchronized decode timing)

	// A/V start alignment. The program stream delivers video ahead of
	// its presentation time by the VBV lead while audio is muxed with
	// almost none, and audio is pulled in real time, so presenting
	// pictures on arrival would run video early by that lead. Video
	// pacing is held until the audio clock (origin = first pushed
	// sample) reaches the first picture's PTS.
	firstVidPTS float64 // PTS of the first video PES packet; -1 until seen
	firstAudPTS float64 // PTS of the first audio PES packet; -1 until seen
	audStarted  bool    // first audio samples pushed to the EXTS queue
	vidStarted  bool    // video pacing released
	vidHoldCyc  int     // cycles accumulated toward the start hold
}

// spent reports whether this playback can no longer produce output:
// at least one layer reached a terminal phase (Done or Halted) and no
// layer is still live. A layer is live when it is in a feeding phase
// with its stream actually underway (demuxer created); a connected
// layer that never received data sits in Running forever - a
// video-only movie's audio path - and must not block a restart.
func (p *mpegPlayback) spent() bool {
	terminal := false
	for i := range p.layers {
		l := &p.layers[i]
		switch l.phase {
		case mpegPhaseDone, mpegPhaseHalted:
			terminal = true
		case mpegPhaseRunning, mpegPhaseEnding:
			if l.demux != nil {
				return false
			}
		}
	}
	return terminal
}

// mpegMailboxFresh marks the mailbox slot as holding an unconsumed
// frame (mailbox bits 1:0 = slot index).
const mpegMailboxFresh = 1 << 2

// mpegFrameSlot is one triple-buffer slot: packed 0x00RRGGBB pixels.
type mpegFrameSlot struct {
	rgb  []uint32
	w, h int
}

// mpegFrameLatch is the decoded-picture handoff to the VDP2 EXBG path:
// a wait-free single-producer/single-consumer triple buffer. The CD
// block's tick context (producer) converts into its owned slot and
// publishes with one atomic swap; the render walker (consumer) takes
// the newest slot with one atomic swap and reads it in place. Neither
// side ever blocks: the emulation goroutines spin at cycle barriers,
// so a parked goroutine here would stall the whole lockstep pipeline.
//
// Ownership invariant: at all times prod, cons, and the mailbox hold a
// permutation of the three slot indices. prod is touched only by the
// tick context, cons and consHas only by the render walker.
type mpegFrameLatch struct {
	slots   [3]mpegFrameSlot
	mailbox atomic.Uint32 // in-transit slot index | mpegMailboxFresh
	prod    int           // producer-owned slot (tick context only)
	cons    int           // consumer-owned slot (render walker only)
	consHas bool          // consumer has taken a frame (render walker only)
	dispOn  atomic.Bool   // $A0 display switch
}

// mpegLatchFrame converts a decoded picture into the producer slot and
// publishes it. The decoder reuses its frame structs on the next
// Decode call, so pixels are converted (not referenced) here. While
// the host has the picture frozen ($96 freeze-time 0), decode
// continues but the displayed picture does not update.
func (cb *CDBlock) mpegLatchFrame(f *mpeg.Frame) {
	if cb.mpeg.vidFrozen {
		return
	}
	l := &cb.mpegFrame
	s := &l.slots[l.prod]
	w, h := f.Width, f.Height
	if len(s.rgb) < w*h {
		s.rgb = make([]uint32, w*h)
	}
	s.w, s.h = w, h
	for y := 0; y < h; y++ {
		yRow := f.Y.Data[y*f.Y.Width:]
		cRow := y / 2 * f.Cb.Width
		dst := s.rgb[y*w : y*w+w]
		for x := 0; x < w; x++ {
			r, g, b := color.YCbCrToRGB(yRow[x], f.Cb.Data[cRow+x/2], f.Cr.Data[cRow+x/2])
			dst[x] = uint32(r)<<16 | uint32(g)<<8 | uint32(b)
		}
	}
	// Publish: the converted slot goes into the mailbox, whatever was
	// there becomes the new scratch slot. An unconsumed frame still in
	// the mailbox is simply dropped (latest wins).
	old := l.mailbox.Swap(uint32(l.prod) | mpegMailboxFresh)
	l.prod = int(old & 3)
}

// mpegSetDisplay mirrors the $A0 display switch into the latch.
func (cb *CDBlock) mpegSetDisplay(on bool) {
	cb.mpegFrame.dispOn.Store(on)
}

// mpegResetLatch resets the frame latch output (MpegInit): the display
// switch drops, and an empty frame is published through the normal
// producer path so the consumer's held slot - the previous stream's
// final picture - cannot reappear when the host re-enables the display
// before the new stream's first picture decodes. MpegInit executes in
// the tick context, the latch's producer side.
func (cb *CDBlock) mpegResetLatch() {
	l := &cb.mpegFrame
	l.dispOn.Store(false)
	s := &l.slots[l.prod]
	s.w, s.h = 0, 0
	old := l.mailbox.Swap(uint32(l.prod) | mpegMailboxFresh)
	l.prod = int(old & 3)
}

// MpegFrameRGB returns the newest decoded picture, read in place. The
// returned slice is the consumer-owned slot: valid until the next
// call, never written by the producer while owned. ok is false when
// nothing has been decoded or the host has the display switched off
// ($A0). Called by VDP2 from the render walker.
func (cb *CDBlock) MpegFrameRGB() (rgb []uint32, w, h int, ok bool) {
	l := &cb.mpegFrame
	if l.mailbox.Load()&mpegMailboxFresh != 0 {
		// Trade the consumer slot for the mailbox slot. The producer
		// may have published an even newer frame between the load and
		// the swap; the swap atomically takes whichever is current.
		old := l.mailbox.Swap(uint32(l.cons))
		l.cons = int(old & 3)
		l.consHas = true
	}
	if !l.consHas || !l.dispOn.Load() {
		return nil, 0, 0, false
	}
	s := &l.slots[l.cons]
	if s.w == 0 || s.h == 0 {
		// Empty frame published by mpegResetLatch: nothing to display.
		return nil, 0, 0, false
	}
	return s.rgb, s.w, s.h, true
}

func newMpegPlayback() *mpegPlayback {
	p := &mpegPlayback{firstVidPTS: -1, firstAudPTS: -1}
	for i := range p.layers {
		// NewBuffer(nil) never fails: the error paths are all reader
		// seek failures.
		p.layers[i].ps, _ = mpeg.NewBuffer(nil)
		p.layers[i].es, _ = mpeg.NewBuffer(nil)
	}
	p.video = mpeg.NewVideo(p.layers[mpegLayerVideo].es)
	p.audio = mpeg.NewAudio(p.layers[mpegLayerAudio].es)
	return p
}

// mpegIntCause latches a cause; MPST asserts per the $92 mask.
func (cb *CDBlock) mpegIntCause(cause uint32) {
	cb.mpeg.intStatus |= cause
	if cb.mpeg.intStatus&cb.mpeg.intMask != 0 {
		cb.hirqReq |= hirqMPST
	}
}

// mpegPSLowWater bounds the pack-stream buffer: sectors are fed from
// the partition only while fewer unread bytes than this remain. The
// rest stay buffered in the partition, whose 200-sector limit then
// throttles the drive through the normal BFUL flow control - the
// hardware's pacing. (Unbounded feeding also degrades the buffer:
// every Write memmoves the unread backlog.)
const mpegPSLowWater = 16 * 1024

// mpegESLowWater bounds the elementary-stream buffer the same way at
// the demux stage.
const mpegESLowWater = 64 * 1024

// mpegFeedLayer feeds partition sectors into the layer's pack stream,
// or - for a raw video elementary stream - straight into the decoder's
// elementary stream. The stream type is identified once from the first
// fed sector by its ISO 11172 start code: a video sequence header
// (00 00 01 B3) at the stream head is a raw video ES with no system
// layer to demux; anything else takes the pack-stream/demux path.
// Connection-mode bit 0x04 deletes consumed sectors (the hardware's
// normal streaming mode); without it sectors stay buffered and only
// newly arrived ones are fed.
func (cb *CDBlock) mpegFeedLayer(layer int) {
	l := &cb.mpeg.play.layers[layer]
	conn := &cb.mpeg.conn[0][layer]
	if int(conn.bufNum) >= len(cb.partitions) {
		return
	}
	part := &cb.partitions[conn.bufNum]
	if l.phase != mpegPhaseRunning {
		return
	}
	if l.fed > len(part.sectors) {
		// Host commands shrank the partition underneath us.
		l.fed = len(part.sectors)
	}
	if !l.esProbed && layer == mpegLayerVideo && l.fed < len(part.sectors) {
		sec := &part.sectors[l.fed]
		head := sec.data[sec.userOffset : sec.userOffset+sec.userSize]
		l.esProbed = true
		l.esDirect = len(head) >= 4 && binary.BigEndian.Uint32(head) == 0x000001B3
	}
	buf, lowWater := l.ps, mpegPSLowWater
	if l.esDirect {
		buf, lowWater = l.es, mpegESLowWater
	}
	for l.fed < len(part.sectors) && buf.Remaining() < lowWater {
		sec := &part.sectors[l.fed]
		l.fed++
		payload := sec.data[sec.userOffset : sec.userOffset+sec.userSize]
		if l.esDirect {
			// The video sequence end code (ISO 11172-2
			// sequence_end_code, 00 00 01 B7) terminates the stream;
			// data past it belongs to the next stream and is not fed.
			// The XA submode end marks apply as on the pack path.
			if end := mpegScanStartCode(&l.esScan, payload, 0xB7); end >= 0 {
				l.es.Write(payload[:end])
				mpegEndVideoES(l)
				l.phase = mpegPhaseEnding
				l.psEnded = true
				break
			}
			l.es.Write(payload)
			// EOR gated on connection-mode bit 0x01 as on the pack
			// path; EOF always ends the layer.
			if sec.submode&0x80 != 0 || (sec.submode&0x01 != 0 && conn.mode&0x01 != 0) {
				mpegEndVideoES(l)
				l.phase = mpegPhaseEnding
				l.psEnded = true
				break
			}
			continue
		}
		l.ps.Write(payload)
		// End conditions per the connection mode: XA submode EOR
		// (bit 0) ends the layer only with connection-mode bit 0x01
		// (switch on EOR) - otherwise EOR is a record boundary inside
		// a continuing stream. The program-end code applies with
		// connection-mode bit 0x02 (switch on system end). XA EOF
		// (submode bit 7) always ends the layer.
		b9 := l.packScan.feed(payload) && conn.mode&0x02 != 0
		eor := sec.submode&0x01 != 0 && conn.mode&0x01 != 0
		if eor || b9 || sec.submode&0x80 != 0 {
			l.phase = mpegPhaseEnding
			break
		}
	}
	if conn.mode&0x04 != 0 && l.fed > 0 {
		part.sectors = part.sectors[l.fed:]
		l.fed = 0
	}
}

// mpegPumpDemux demuxes buffered pack data into the layer's elementary
// stream, up to the ES low-water mark. The demuxer is created lazily
// once a sector's worth of data is available so its header probe
// cannot half-consume a pack.
func (cb *CDBlock) mpegPumpDemux(layer int) {
	l := &cb.mpeg.play.layers[layer]
	if l.esDirect {
		// Raw elementary stream: the feed writes the decoder's es
		// directly; there is no system layer to demux.
		return
	}
	if l.phase != mpegPhaseRunning && l.phase != mpegPhaseEnding {
		return
	}
	if l.demux == nil {
		if l.ps.Remaining() < 2048 {
			return
		}
		d, err := mpeg.NewDemux(l.ps)
		if err != nil {
			return
		}
		l.demux = d
	}
	want := mpeg.PacketVideo1
	if layer == mpegLayerAudio {
		want = mpeg.PacketAudio1 + int(cb.mpeg.stream[0][layer].stmNum)&3
	}
	for !l.esEnded && l.es.Remaining() < mpegESLowWater {
		pkt := l.demux.Decode()
		if pkt == nil {
			return
		}
		if pkt.Type != want {
			continue
		}
		if pkt.Pts >= 0 {
			if layer == mpegLayerVideo && cb.mpeg.play.firstVidPTS < 0 {
				cb.mpeg.play.firstVidPTS = pkt.Pts
			}
			if layer == mpegLayerAudio && cb.mpeg.play.firstAudPTS < 0 {
				cb.mpeg.play.firstAudPTS = pkt.Pts
			}
		}
		if layer == mpegLayerVideo {
			// The video sequence end code (ISO 11172-2
			// sequence_end_code, 00 00 01 B7) terminates the video
			// stream: the "sequence end detected" interrupt cause is
			// this bitstream event. Data past it is not the same
			// sequence and is not fed.
			if end := mpegScanStartCode(&l.esScan, pkt.Data, 0xB7); end >= 0 {
				l.es.Write(pkt.Data[:end])
				mpegEndVideoES(l)
				if l.phase == mpegPhaseRunning {
					l.phase = mpegPhaseEnding
				}
				return
			}
		}
		l.es.Write(pkt.Data)
	}
}

// mpegEndVideoES terminates the video elementary stream. A synthetic
// picture start code is appended first: the decoder only decodes a
// picture once it sees the start code of the following one, and its
// end-of-buffer flag cannot latch once SignalEnd interleaves with the
// decoder's internal discards (the buffer's ended test needs the byte
// length to equal the recorded total, which discarding breaks), so
// without a bounding code the true final picture would never decode.
func mpegEndVideoES(l *mpegLayer) {
	l.es.Write([]byte{0x00, 0x00, 0x01, 0x00})
	l.es.SignalEnd()
	l.esEnded = true
}

// mpegEndLayerES terminates a layer's elementary stream (video needs
// the synthetic bounding code; see mpegEndVideoES).
func mpegEndLayerES(layer int, l *mpegLayer) {
	if layer == mpegLayerVideo {
		mpegEndVideoES(l)
	} else {
		l.es.SignalEnd()
		l.esEnded = true
	}
}

// mpegVideoStreamDone latches the video layer's end of stream.
func (cb *CDBlock) mpegVideoStreamDone(l *mpegLayer) {
	l.phase = mpegPhaseDone
	cb.mpeg.vidState = mpegRunStopped
	cb.mpeg.vidStatus &^= mpegVidDecoding
	cb.mpeg.vidStatus |= mpegVidLastShown
	cb.mpegIntCause(mpegIntSequenceEnd)
	cb.mpegStreamEndSwitch(mpegLayerVideo)
}

// mpegAudioStreamDone latches the audio layer's end of stream.
func (cb *CDBlock) mpegAudioStreamDone(l *mpegLayer) {
	l.phase = mpegPhaseDone
	cb.mpeg.audState = mpegRunStopped
	cb.mpeg.audStatus &^= mpegAudDecoding
	cb.mpegStreamEndSwitch(mpegLayerAudio)
}

// mpegStreamEndSwitch applies a layer's end-of-stream connection
// switch: connection-mode bits 0x01 (switch on EOR) and 0x02 (switch
// on system end) promote the next-connection record to current when
// the layer's stream terminates. The host preloads next = disconnected
// so the ended layer releases its buffer partition - which the game
// then reuses (e.g. for loading) without issuing $9C.
func (cb *CDBlock) mpegStreamEndSwitch(layer int) {
	m := &cb.mpeg
	if m.conn[0][layer].mode&0x03 == 0 {
		return
	}
	m.conn[0][layer] = m.conn[1][layer]
	if layer == mpegLayerVideo {
		cb.mpegIntCause(mpegIntVidSwitchDone)
	} else {
		cb.mpegIntCause(mpegIntAudSwitchDone)
	}
}

// mpegApplyNextConnections promotes the next-connection records to
// current ($9C). A layer switched to no partition loses its data
// supply: decode halts and it reports stopped, buffer empty.
func (cb *CDBlock) mpegApplyNextConnections() {
	m := &cb.mpeg
	old := m.conn[0]
	m.conn[0] = m.conn[1]
	if m.play == nil {
		return
	}
	// A rebind to a different partition reads from that partition's
	// start: the non-delete-mode fed index belongs to the old binding.
	// A re-commit of the same partition keeps it (resetting would
	// re-feed duplicates).
	for layer := range m.conn[0] {
		if m.conn[0][layer].bufNum != old[layer].bufNum {
			m.play.layers[layer].fed = 0
		}
	}
	if int(m.conn[0][mpegLayerVideo].bufNum) >= len(cb.partitions) {
		m.play.layers[mpegLayerVideo].phase = mpegPhaseHalted
		m.vidState = mpegRunStopped
		m.vidStatus &^= mpegVidDecoding
		m.vidStatus |= mpegVidBufEmpty
	}
	if int(m.conn[0][mpegLayerAudio].bufNum) >= len(cb.partitions) {
		m.play.layers[mpegLayerAudio].phase = mpegPhaseHalted
		m.audState = mpegRunStopped
		m.audStatus &^= mpegAudDecoding
		m.audStatus |= mpegAudBufEmpty
	}
	cb.mpegIntCause(mpegIntVidSwitchDone | mpegIntAudSwitchDone)
}

// mpegVideoStartDue advances the video start hold by c cycles and
// reports whether pacing may begin. With an audio stream present, the
// hold runs from the first pushed audio sample (the presentation clock
// origin) for the PTS lead of the first picture over the first audio
// frame. Without an audio stream - or if audio never materializes -
// the hold is released after a bounded wait so video-only movies play.
//
// The accumulator carries across the branches below and is reset only
// at the audio clock origin (audStarted): each bound measures total
// elapsed hold, so discovering audio late does not restart the wait.
func (cb *CDBlock) mpegVideoStartDue(c int) bool {
	p := cb.mpeg.play
	if cb.mpeg.playMode != 0 {
		// Independent playback ($95 play mode 1): the decoders run
		// without A/V presentation sync, so there is no start hold.
		return true
	}
	if p.firstAudPTS >= 0 && !p.audStarted {
		// Audio exists but has not produced samples yet; its start is
		// the clock origin, so keep holding (bounded below).
		p.vidHoldCyc += c
		return p.vidHoldCyc >= 2*cb.sysCyclesPerSec
	}
	if p.firstAudPTS < 0 {
		// No audio packet seen: release after a short grace period in
		// case the audio stream simply lags in the mux.
		p.vidHoldCyc += c
		return p.vidHoldCyc >= cb.sysCyclesPerSec/2
	}
	lead := p.firstVidPTS - p.firstAudPTS
	if p.firstVidPTS < 0 || lead <= 0 {
		return true
	}
	if lead > 2 {
		lead = 2
	}
	p.vidHoldCyc += c
	return p.vidHoldCyc >= int(lead*float64(cb.sysCyclesPerSec))
}

// mpegTick advances the MPEG subsystem by c system cycles: VSYNC
// counter, partition drain, demux, and rate-paced decode.
func (cb *CDBlock) mpegTick(c int) {
	m := &cb.mpeg
	if !m.active {
		return
	}

	// Operation-interval counter, one count per VSYNC.
	m.vsyncAccum += c
	for m.vsyncAccum >= cb.mpegVsyncCycles {
		m.vsyncAccum -= cb.mpegVsyncCycles
		m.vsyncCounter++
	}

	if m.play == nil {
		return
	}
	p := m.play

	cb.mpegFeedLayer(mpegLayerAudio)
	cb.mpegFeedLayer(mpegLayerVideo)
	cb.mpegPumpDemux(mpegLayerAudio)
	cb.mpegPumpDemux(mpegLayerVideo)

	// End-of-stream staging: in the Ending phase, end the pack stream
	// so the demuxer runs dry, then end the elementary stream so the
	// decoder flushes its final reference frame.
	for i := range p.layers {
		l := &p.layers[i]
		if l.phase != mpegPhaseEnding || l.esEnded {
			continue
		}
		if !l.psEnded {
			l.ps.SignalEnd()
			l.psEnded = true
		}
		if l.demux == nil {
			// The pump's demux-probe minimum can never be met once
			// the pack stream has ended; probe with what is buffered.
			// A probe failure means the data is not identifiable as a
			// program stream: nothing can decode, so end the
			// elementary stream and let the layer latch Done below.
			d, err := mpeg.NewDemux(l.ps)
			if err != nil {
				mpegEndLayerES(i, l)
				continue
			}
			l.demux = d
		}
		if l.demux.HasEnded() {
			mpegEndLayerES(i, l)
		}
	}

	// A layer whose stream terminated before a decodable header
	// arrived can never produce output: the decode paths below are
	// gated on the header, so latch the end here through the same
	// completion path a decoded stream takes.
	vl := &p.layers[mpegLayerVideo]
	if vl.phase == mpegPhaseEnding && vl.esEnded && !p.video.HasHeader() {
		cb.mpegVideoStreamDone(vl)
	}
	al := &p.layers[mpegLayerAudio]
	if al.phase == mpegPhaseEnding && al.esEnded && !p.audio.HasHeader() {
		cb.mpegAudioStreamDone(al)
	}

	if p.pictCycles == 0 && p.video.HasHeader() {
		if fr := p.video.Framerate(); fr > 0 {
			p.pictCycles = int(float64(cb.sysCyclesPerSec) / fr)
		}
		m.picW = uint16(p.video.Width())
		m.picH = uint16(p.video.Height())
		m.vidState = mpegRunStreamFound
		cb.mpegIntCause(mpegIntSequenceStart)
	}

	// Video: decode paced at the stream's picture rate (VSYNC decode
	// timing) or advanced one picture per $97 (host-synchronized
	// decode timing), halted while the host has decode paused ($96
	// pause-time 0). The accumulator is capped so a data stall does
	// not burst-decode a backlog. Host stepping takes over only after
	// the first picture: the host learns decode is ready from the
	// picture-start cause, so that picture decodes autonomously in
	// both timing modes (traced: a host-synchronized title waits for
	// picture-start before its first $97).
	if p.pictCycles > 0 && !p.vidStarted {
		p.vidStarted = cb.mpegVideoStartDue(c)
		if p.vidStarted {
			p.pictAccum = p.pictCycles // present the first picture now
		}
	}
	if p.pictCycles > 0 && p.vidStarted && !m.vidPaused &&
		(vl.phase == mpegPhaseRunning || vl.phase == mpegPhaseEnding) {
		hostStepped := m.decodeTiming == 1 && m.vidState == mpegRunPlaying
		if !hostStepped {
			p.pictAccum += c
			if limit := 4 * p.pictCycles; p.pictAccum > limit {
				p.pictAccum = limit
			}
		}
		for {
			if hostStepped {
				if p.hostSteps == 0 {
					break
				}
			} else if p.pictAccum < p.pictCycles {
				break
			}
			f := p.video.Decode()
			if f == nil {
				if vl.esEnded {
					// Stream over: esEnded gates the pump, so no more
					// data can ever arrive and a nil decode is
					// terminal. (The decoder's own HasEnded is not
					// usable here; see mpegEndVideoES.)
					cb.mpegVideoStreamDone(vl)
				}
				// Not enough data for a picture: retry a picture
				// period later (a pending host step is held instead).
				// Retrying every tick rescans the buffered stream to
				// no effect.
				p.pictAccum = 0
				break
			}
			if hostStepped {
				p.hostSteps--
			} else {
				p.pictAccum -= p.pictCycles
			}
			cb.mpegLatchFrame(f)
			m.vidState = mpegRunPlaying
			m.vidStatus |= mpegVidDecoding | mpegVidUpdated | mpegVidOutReady | mpegVidFirstShown
			if m.dispSwitch != 0 {
				m.vidStatus |= mpegVidDisplaying
			}
			cb.mpegIntCause(mpegIntPictureStart)
		}
	}

	// Audio: decode a frame whenever the EXTS queue has room for one
	// (1152 stereo pairs). Output is normalized float32; convert to the
	// int16 pairs SCSP EXTS consumes.
	if p.audio.HasHeader() && (al.phase == mpegPhaseRunning || al.phase == mpegPhaseEnding) {
		for cdAudioQueueMax-cb.audioCount >= 2*mpeg.SamplesPerFrame {
			// A full MP2 frame is at most 1728 bytes; below that the
			// decode attempt can only rescan and fail, so wait for
			// more data (unless flushing the stream tail).
			if al.es.Remaining() < 2048 && !al.esEnded {
				break
			}
			smp := p.audio.Decode()
			if smp == nil {
				if al.esEnded {
					cb.mpegAudioStreamDone(al)
				}
				break
			}
			if m.audState != mpegRunPlaying {
				m.audState = mpegRunPlaying
				m.audStatus |= mpegAudDecoding | mpegAudLeftOut | mpegAudRightOut
				cb.mpegIntCause(mpegIntAudioReady)
			}
			if !p.audStarted {
				// Presentation clock origin: the video start hold
				// measures its PTS lead from here.
				p.audStarted = true
				p.vidHoldCyc = 0
			}
			for i := 0; i < len(smp.Interleaved); i += 2 {
				left, right := mpegMuteSample(m.audioMute,
					mpegF32ToS16(smp.Interleaved[i]), mpegF32ToS16(smp.Interleaved[i+1]))
				cb.pushAudioPair(left, right)
			}
		}
	}
}

// mpegMuteSample applies the $96 audio-mute byte to one stereo pair:
// bit 0 mutes the right channel, bit 1 the left.
func mpegMuteSample(mute uint8, left, right int16) (int16, int16) {
	if mute&0x02 != 0 {
		left = 0
	}
	if mute&0x01 != 0 {
		right = 0
	}
	return left, right
}

func mpegF32ToS16(v float32) int16 {
	s := int32(v * 32767)
	if s > 32767 {
		s = 32767
	} else if s < -32768 {
		s = -32768
	}
	return int16(s)
}
