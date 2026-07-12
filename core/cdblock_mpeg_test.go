// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"image/color"
	"testing"

	"github.com/gen2brain/mpeg"
)

// mpegInit issues $93 MpegInit, the gate for every other MPEG command.
func mpegInit(cb *CDBlock) {
	execCommandFull(cb, 0x93, 0x00, 0x0001, 0x0000, 0x0000)
}

func TestCDBlockMpegInit(t *testing.T) {
	cb := NewCDBlock(nil)
	// The host-side init (CDC_MpInit) clears CMOK+MPED, issues $93,
	// then polls for MPED and MPCM together.
	cb.hirqReq &^= hirqCMOK | hirqMPED
	mpegInit(cb)

	want := uint16(hirqCMOK | hirqMPED | hirqMPCM)
	if cb.hirqReq&want != want {
		t.Errorf("hirqReq = 0x%04X, want CMOK|MPED|MPCM set", cb.hirqReq)
	}
	if !cb.mpeg.active {
		t.Error("mpeg.active should be set after $93")
	}
	// Status report: operation-status byte = video stopped (1) |
	// audio stopped (0x10); picture/audio/video status and the VSYNC
	// counter all zero.
	if got := cb.res[0] & 0x00FF; got != 0x0011 {
		t.Errorf("op status = 0x%02X, want 0x11 (stopped/stopped)", got)
	}
	for i, want := range []uint16{0, 0, 0} {
		if cb.res[i+1] != want {
			t.Errorf("res[%d] = 0x%04X, want 0x%04X", i+1, cb.res[i+1], want)
		}
	}
	// Connections reset to disconnected (partition 0xFF) in both the
	// current and next slots.
	for sel := range cb.mpeg.conn {
		for layer := range cb.mpeg.conn[sel] {
			if cb.mpeg.conn[sel][layer].bufNum != 0xFF {
				t.Errorf("conn[%d][%d].bufNum = 0x%02X, want 0xFF",
					sel, layer, cb.mpeg.conn[sel][layer].bufNum)
			}
		}
	}
}

func TestCDBlockMpegCommandsRejectBeforeInit(t *testing.T) {
	cb := NewCDBlock(nil)
	// $90 before $93: the firmware gates the MPEG commands on the
	// subsystem being active; the reject path answers with the
	// standard CD return (no-track res[1] = 0xFFFF), not the MPEG
	// status report.
	execCommandFull(cb, 0x90, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[1] != 0xFFFF {
		t.Errorf("pre-init $90 res[1] = 0x%04X, want 0xFFFF (standard return)", cb.res[1])
	}

	mpegInit(cb)
	execCommandFull(cb, 0x90, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[1] != 0x0000 {
		t.Errorf("post-init $90 res[1] = 0x%04X, want 0x0000 (status report)", cb.res[1])
	}
}

func TestCDBlockMpegInterruptMaskAndStatus(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	// $92: cause mask packed as CR1 low byte = bits 23-16, CR2 =
	// bits 15-0.
	execCommandFull(cb, 0x92, 0x12, 0x0500, 0x0000, 0x0000)
	if cb.mpeg.intMask != 0x120500 {
		t.Errorf("intMask = 0x%06X, want 0x120500", cb.mpeg.intMask)
	}

	// A masked cause asserts MPST; $91 returns the cause register in
	// the same packing and clears it.
	cb.hirqReq &^= hirqMPST
	cb.mpegIntCause(0x000400)
	if cb.hirqReq&hirqMPST == 0 {
		t.Error("masked cause should assert MPST")
	}
	execCommandFull(cb, 0x91, 0x00, 0x0000, 0x0000, 0x0000)
	if got := cb.res[0] & 0x00FF; got != 0x00 {
		t.Errorf("$91 CR1 low = 0x%02X, want 0x00 (cause bits 23-16)", got)
	}
	if cb.res[1] != 0x0400 {
		t.Errorf("$91 CR2 = 0x%04X, want 0x0400", cb.res[1])
	}
	if cb.mpeg.intStatus != 0 {
		t.Errorf("intStatus = 0x%06X, want 0 (cleared by $91)", cb.mpeg.intStatus)
	}

	// An unmasked cause latches without MPST.
	cb.hirqReq &^= hirqMPST
	cb.mpegIntCause(0x000002)
	if cb.hirqReq&hirqMPST != 0 {
		t.Error("unmasked cause should not assert MPST")
	}

	// $92 enabling an already-latched cause asserts MPST immediately:
	// the host must not have to wait for an unrelated later cause to
	// learn about a pending one it just unmasked.
	execCommandFull(cb, 0x92, 0x00, 0x0002, 0x0000, 0x0000)
	if cb.hirqReq&hirqMPST == 0 {
		t.Error("$92 unmasking a pending cause should assert MPST")
	}

	// $92 with no pending match does not assert.
	cb.hirqReq &^= hirqMPST
	execCommandFull(cb, 0x92, 0x00, 0x1000, 0x0000, 0x0000)
	if cb.hirqReq&hirqMPST != 0 {
		t.Error("$92 with no matching pending cause should not assert MPST")
	}
}

func TestCDBlockMpegConnectionRoundTrip(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	// $9A: audio record in CR1 low/CR2, video record in CR3 low/CR4,
	// current/next selector in CR3 high.
	execCommandFull(cb, 0x9A, 0x06, 0x0001, 0x0026, 0xC000)
	aud := cb.mpeg.conn[0][mpegLayerAudio]
	vid := cb.mpeg.conn[0][mpegLayerVideo]
	if aud.mode != 0x06 || aud.layer != 0x00 || aud.bufNum != 0x01 {
		t.Errorf("audio conn = %+v, want {mode:06 layer:00 buf:01}", aud)
	}
	if vid.mode != 0x26 || vid.layer != 0xC0 || vid.bufNum != 0x00 {
		t.Errorf("video conn = %+v, want {mode:26 layer:C0 buf:00}", vid)
	}

	// $9B echoes the records in the same packing.
	execCommandFull(cb, 0x9B, 0x00, 0x0000, 0x0000, 0x0000)
	if got := cb.res[0] & 0x00FF; got != 0x0006 {
		t.Errorf("$9B CR1 low = 0x%02X, want 0x06", got)
	}
	if cb.res[1] != 0x0001 || cb.res[2] != 0x0026 || cb.res[3] != 0xC000 {
		t.Errorf("$9B CR2-4 = %04X %04X %04X, want 0001 0026 C000",
			cb.res[1], cb.res[2], cb.res[3])
	}

	// Next slot stays disconnected and reads back independently.
	execCommandFull(cb, 0x9B, 0x00, 0x0000, 0x0100, 0x0000)
	if cb.res[1] != 0x00FF || cb.res[3] != 0x00FF {
		t.Errorf("$9B next CR2/CR4 = %04X/%04X, want 00FF/00FF (disconnected)",
			cb.res[1], cb.res[3])
	}
}

func TestCDBlockMpegChangeConnectionPromotesNext(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	// Start playback, connect current, write a disconnect into next,
	// then $9C: the next records become current (the teardown path
	// games use mid-playback).
	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	execCommandFull(cb, 0x9A, 0x06, 0x0001, 0x0026, 0xC000)
	execCommandFull(cb, 0x9A, 0x00, 0x00FF, 0x01FF, 0x00FF)
	execCommandFull(cb, 0x9C, 0x00, 0x00FF, 0x0000, 0x0000)

	for layer := range cb.mpeg.conn[0] {
		if cb.mpeg.conn[0][layer].bufNum != 0xFF {
			t.Errorf("conn[0][%d].bufNum = 0x%02X, want 0xFF after $9C",
				layer, cb.mpeg.conn[0][layer].bufNum)
		}
	}
	// The switch is acknowledged with both stream-switch-done causes.
	want := uint32(mpegIntVidSwitchDone | mpegIntAudSwitchDone)
	if cb.mpeg.intStatus&want != want {
		t.Errorf("intStatus = 0x%06X, want switch-done causes latched", cb.mpeg.intStatus)
	}
}

func TestCDBlockMpegStreamRoundTrip(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	execCommandFull(cb, 0x9D, 0x01, 0x0203, 0x0004, 0x0506)
	execCommandFull(cb, 0x9E, 0x00, 0x0000, 0x0000, 0x0000)
	if got := cb.res[0] & 0x00FF; got != 0x0001 {
		t.Errorf("$9E CR1 low = 0x%02X, want 0x01", got)
	}
	if cb.res[1] != 0x0203 || cb.res[2] != 0x0004 || cb.res[3] != 0x0506 {
		t.Errorf("$9E CR2-4 = %04X %04X %04X, want 0203 0004 0506",
			cb.res[1], cb.res[2], cb.res[3])
	}
}

func TestCDBlockMpegPictureSize(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	// Default scan mode 0 (NTSC): 352x240.
	execCommandFull(cb, 0x9F, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[2] != 352 || cb.res[3] != 240 {
		t.Errorf("picture size = %dx%d, want 352x240", cb.res[2], cb.res[3])
	}

	// $94 scan mode 2 (PAL non-interlaced) in CR3 high: 352x288.
	execCommandFull(cb, 0x94, 0xFF, 0xFFFF, 0x0200, 0x0000)
	execCommandFull(cb, 0x9F, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[2] != 352 || cb.res[3] != 288 {
		t.Errorf("PAL picture size = %dx%d, want 352x288", cb.res[2], cb.res[3])
	}

	// Once a stream's sequence header has set the decoded picture
	// size, $9F reports it instead of the scan-mode geometry.
	cb.mpeg.picW = 320
	cb.mpeg.picH = 224
	execCommandFull(cb, 0x9F, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[2] != 320 || cb.res[3] != 224 {
		t.Errorf("stream picture size = %dx%d, want 320x224", cb.res[2], cb.res[3])
	}

	// $93 MpegInit clears the learned size back to the default.
	mpegInit(cb)
	execCommandFull(cb, 0x9F, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[2] != 352 || cb.res[3] != 240 {
		t.Errorf("post-init picture size = %dx%d, want 352x240", cb.res[2], cb.res[3])
	}
}

func TestCDBlockMpegSetModeNoChange(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	execCommandFull(cb, 0x94, 0x01, 0x0100, 0x0300, 0x0000)
	m := &cb.mpeg
	if m.movieMode != 1 || m.decodeTiming != 1 || m.outDest != 0 || m.scanMode != 3 {
		t.Errorf("mode = %d/%d/%d/%d, want 1/1/0/3",
			m.movieMode, m.decodeTiming, m.outDest, m.scanMode)
	}

	// 0xFF bytes keep the current values.
	execCommandFull(cb, 0x94, 0xFF, 0xFFFF, 0xFF00, 0x0000)
	if m.movieMode != 1 || m.decodeTiming != 1 || m.outDest != 0 || m.scanMode != 3 {
		t.Errorf("mode after no-change = %d/%d/%d/%d, want 1/1/0/3",
			m.movieMode, m.decodeTiming, m.outDest, m.scanMode)
	}
}

func TestCDBlockMpegLsiShadow(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	// $AF write with read-back (CR1 bit 0), window A register 0x1A.
	execCommandFull(cb, 0xAF, 0x01, 0x001A, 0x0000, 0xBEEF)
	if cb.res[3] != 0xBEEF {
		t.Errorf("$AF read-back CR4 = 0x%04X, want 0xBEEF", cb.res[3])
	}
	// $AE reads the shadow.
	execCommandFull(cb, 0xAE, 0x00, 0x001A, 0x0000, 0x0000)
	if cb.res[3] != 0xBEEF {
		t.Errorf("$AE CR4 = 0x%04X, want 0xBEEF", cb.res[3])
	}

	// LSI A register 6 synthesizes the decoder-idle bit 0x4000 while
	// the video decoder is not playing (host stop paths poll it).
	execCommandFull(cb, 0xAF, 0x01, 0x0006, 0x0000, 0x0000)
	if cb.res[3]&0x4000 == 0 {
		t.Errorf("$AF reg 6 read-back = 0x%04X, want idle bit 0x4000", cb.res[3])
	}
	cb.mpeg.vidState = mpegRunPlaying
	execCommandFull(cb, 0xAF, 0x01, 0x0006, 0x0000, 0x0000)
	if cb.res[3]&0x4000 != 0 {
		t.Errorf("$AF reg 6 while playing = 0x%04X, want idle bit clear", cb.res[3])
	}
}

func TestCDBlockMpegCardAuth(t *testing.T) {
	cb := NewCDBlock(nil)

	// $E1 subcommand 1 (CR2 low byte) before auth: result 0.
	execCommandFull(cb, 0xE1, 0x00, 0x0001, 0x0000, 0x0000)
	if cb.res[1] != 0 {
		t.Errorf("pre-auth $E1 sub1 CR2 = 0x%04X, want 0", cb.res[1])
	}

	// $E0 subcommand 1 authenticates the card and asserts MPED.
	cb.hirqReq &^= hirqMPED
	execCommandFull(cb, 0xE0, 0x00, 0x0001, 0x0000, 0x0000)
	if cb.hirqReq&hirqMPED == 0 {
		t.Error("$E0 sub1 should assert MPED")
	}
	if !cb.mpegCardAuth.Load() {
		t.Error("$E0 sub1 should set mpegCardAuth")
	}

	// $E1 subcommand 1 after auth: result 2 (BIOS SYS_CHKMPEG
	// requires exactly 2).
	execCommandFull(cb, 0xE1, 0x00, 0x0001, 0x0000, 0x0000)
	if cb.res[1] != 2 {
		t.Errorf("post-auth $E1 sub1 CR2 = 0x%04X, want 2", cb.res[1])
	}

	// Get Hardware Info reports MPEG version 1 once authenticated.
	execCommandFull(cb, 0x01, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.res[2] != 0x0001 {
		t.Errorf("post-auth hardware info CR3 = 0x%04X, want 0x0001", cb.res[2])
	}
}

func TestCDBlockMpegScanStartCode(t *testing.T) {
	// Match split across calls, with a three-zero run before the
	// prefix (the matcher must not lose the 00 00 01 state).
	state := 0
	if got := mpegScanStartCode(&state, []byte{0x12, 0x00, 0x00, 0x00}, 0xB7); got != -1 {
		t.Errorf("partial prefix matched at %d", got)
	}
	if got := mpegScanStartCode(&state, []byte{0x01, 0xB7, 0x99}, 0xB7); got != 2 {
		t.Errorf("split match = %d, want 2 (index past 0xB7)", got)
	}

	// A different start code resets the state and does not match.
	state = 0
	if got := mpegScanStartCode(&state, []byte{0x00, 0x00, 0x01, 0xB3, 0x00, 0x00, 0x01}, 0xB7); got != -1 {
		t.Errorf("wrong code matched at %d", got)
	}
	if got := mpegScanStartCode(&state, []byte{0xB7}, 0xB7); got != 1 {
		t.Errorf("continuation match = %d, want 1", got)
	}
}

// mpegTestFrame builds a minimal uniform-color 4:2:0 frame for latch
// tests.
func mpegTestFrame(w, h int, yv, cbv, crv byte) *mpeg.Frame {
	cw, ch := (w+1)/2, (h+1)/2
	return &mpeg.Frame{
		Width:  w,
		Height: h,
		Y:      mpeg.Plane{Width: w, Height: h, Data: bytes.Repeat([]byte{yv}, w*h)},
		Cb:     mpeg.Plane{Width: cw, Height: ch, Data: bytes.Repeat([]byte{cbv}, cw*ch)},
		Cr:     mpeg.Plane{Width: cw, Height: ch, Data: bytes.Repeat([]byte{crv}, cw*ch)},
	}
}

// TestCDBlockMpegFrameLatch exercises the decoded-picture triple
// buffer: display gating, pixel conversion, latest-wins publishing,
// consumer slot stability, and the slot-ownership invariant.
func TestCDBlockMpegFrameLatch(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	// Nothing decoded: not displayable even with the display on.
	cb.mpegSetDisplay(true)
	if _, _, _, ok := cb.MpegFrameRGB(); ok {
		t.Fatal("no frame latched, MpegFrameRGB should not be ok")
	}

	cb.mpegLatchFrame(mpegTestFrame(2, 2, 0x50, 0x90, 0x70))
	rgb, w, h, ok := cb.MpegFrameRGB()
	if !ok || w != 2 || h != 2 {
		t.Fatalf("frame = %dx%d ok=%v, want 2x2 true", w, h, ok)
	}
	r, g, b := color.YCbCrToRGB(0x50, 0x90, 0x70)
	want := uint32(r)<<16 | uint32(g)<<8 | uint32(b)
	for i, px := range rgb[:w*h] {
		if px != want {
			t.Fatalf("pixel %d = 0x%06X, want 0x%06X", i, px, want)
		}
	}

	// Display off hides the held frame without consuming it.
	cb.mpegSetDisplay(false)
	if _, _, _, ok := cb.MpegFrameRGB(); ok {
		t.Error("display off should hide the frame")
	}
	cb.mpegSetDisplay(true)

	// Two publishes without a consume: the consumer gets the newest,
	// and the slice it already holds is never written by the producer.
	held := rgb[0]
	cb.mpegLatchFrame(mpegTestFrame(2, 2, 0xA0, 0x80, 0x80))
	cb.mpegLatchFrame(mpegTestFrame(4, 2, 0xF0, 0x80, 0x80))
	if _, w2, _, ok := cb.MpegFrameRGB(); !ok || w2 != 4 {
		t.Fatalf("latest frame width = %d ok=%v, want 4 true", w2, ok)
	}
	if rgb[0] != held {
		t.Error("consumer-owned slice mutated by later publishes")
	}

	// Ownership invariant: prod, cons, and the mailbox always hold a
	// permutation of the three slot indices.
	l := &cb.mpegFrame
	seen := map[int]bool{l.prod: true, l.cons: true, int(l.mailbox.Load() & 3): true}
	if len(seen) != 3 {
		t.Errorf("slot ownership not a permutation: prod=%d cons=%d mailbox=%d",
			l.prod, l.cons, l.mailbox.Load()&3)
	}
}

// TestCDBlockMpegResetLatchHidesHeldFrame verifies $93's latch reset:
// the previous stream's final picture must not reappear when the host
// re-enables the display before the new stream's first picture.
func TestCDBlockMpegResetLatchHidesHeldFrame(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	cb.mpegSetDisplay(true)
	cb.mpegLatchFrame(mpegTestFrame(2, 2, 0x80, 0x80, 0x80))
	if _, _, _, ok := cb.MpegFrameRGB(); !ok {
		t.Fatal("frame should display before reset")
	}

	cb.mpegResetLatch()
	cb.mpegSetDisplay(true)
	if _, _, _, ok := cb.MpegFrameRGB(); ok {
		t.Error("reset must hide the previous stream's final picture")
	}

	// A new stream's picture displays normally after the reset.
	cb.mpegLatchFrame(mpegTestFrame(2, 2, 0x40, 0x80, 0x80))
	if _, _, _, ok := cb.MpegFrameRGB(); !ok {
		t.Error("new frame after reset should display")
	}
}

// TestCDBlockMpegDisplayTracksDisplayingBit verifies $A0 maintains the
// video-status Displaying bit (0x0002): on while pictures are flowing
// sets it, off clears it, and on before any picture leaves it for the
// decode path to set when the first picture latches.
func TestCDBlockMpegDisplayTracksDisplayingBit(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	cb.mpeg.vidState = mpegRunPlaying
	execCommandFull(cb, 0xA0, 0x00, 0x0100, 0x0000, 0x0000)
	if cb.mpeg.vidStatus&mpegVidDisplaying == 0 {
		t.Error("display on while playing should set Displaying")
	}

	execCommandFull(cb, 0xA0, 0x00, 0x0000, 0x0000, 0x0000)
	if cb.mpeg.vidStatus&mpegVidDisplaying != 0 {
		t.Error("display off should clear Displaying")
	}

	cb.mpeg.vidState = mpegRunAwaitStream
	execCommandFull(cb, 0xA0, 0x00, 0x0100, 0x0000, 0x0000)
	if cb.mpeg.vidStatus&mpegVidDisplaying != 0 {
		t.Error("display on with no pictures should not set Displaying")
	}
}

// TestCDBlockMpegGarbageStreamEnds verifies a stream that terminates
// before it is identifiable (no demuxable pack, no decodable header)
// still ends cleanly: both layers latch Done through the normal
// completion path (sequence-end cause fires, pipeline reports spent)
// instead of wedging in Ending until $93. Covers sub-sector streams
// (the demux probe minimum can never be met) and corrupt data.
func TestCDBlockMpegGarbageStreamEnds(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	p := cb.mpeg.play
	junk := make([]byte, 512)
	for i := range junk {
		junk[i] = 0xAA
	}
	for i := range p.layers {
		p.layers[i].ps.Write(junk)
		p.layers[i].phase = mpegPhaseEnding
	}

	for i := 0; i < 4 && !p.spent(); i++ {
		cb.mpegTick(1000)
	}

	for i := range p.layers {
		if p.layers[i].phase != mpegPhaseDone {
			t.Errorf("layer %d phase = %d, want Done", i, p.layers[i].phase)
		}
	}
	if !p.spent() {
		t.Error("pipeline should be spent after garbage stream end")
	}
	if cb.mpeg.intStatus&mpegIntSequenceEnd == 0 {
		t.Error("sequence-end cause should latch")
	}
	if cb.mpeg.vidState != mpegRunStopped || cb.mpeg.audState != mpegRunStopped {
		t.Errorf("run states = %d/%d, want stopped/stopped",
			cb.mpeg.vidState, cb.mpeg.audState)
	}
}

// TestCDBlockMpegChangeConnectionResetsFeedIndex verifies a $9C
// rebind to a different partition restarts feeding from that
// partition's start: the non-delete-mode fed index belongs to the old
// binding and must not carry over. A layer re-committed to the same
// partition keeps its index (resetting would re-feed duplicates).
func TestCDBlockMpegChangeConnectionResetsFeedIndex(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	// Current: audio partition 1, video partition 0 (non-delete mode).
	execCommandFull(cb, 0x9A, 0x00, 0x0001, 0x0000, 0x0000)
	cb.mpeg.play.layers[mpegLayerAudio].fed = 5
	cb.mpeg.play.layers[mpegLayerVideo].fed = 7

	// Next: audio moves to partition 2, video re-commits partition 0.
	execCommandFull(cb, 0x9A, 0x00, 0x0002, 0x0100, 0x0000)
	execCommandFull(cb, 0x9C, 0x00, 0x0000, 0x0000, 0x0000)

	if got := cb.mpeg.play.layers[mpegLayerAudio].fed; got != 0 {
		t.Errorf("audio fed = %d, want 0 (partition changed)", got)
	}
	if got := cb.mpeg.play.layers[mpegLayerVideo].fed; got != 7 {
		t.Errorf("video fed = %d, want 7 (same partition)", got)
	}
}

// TestCDBlockMpegPlayRebuildsSpentPlayback verifies $95 Play replaces
// a spent pipeline: after a stream ends ($9A reconnection, no $93
// re-init), a new play sequence must start. The audio layer is left
// connected-but-never-fed (phase Running, no demuxer) - a video-only
// movie's audio path - which must not block the restart.
func TestCDBlockMpegPlayRebuildsSpentPlayback(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	old := cb.mpeg.play
	if old == nil {
		t.Fatal("$95 should create the playback pipeline")
	}

	// Natural end of a video-only movie: video layer terminal, audio
	// layer never received data.
	old.layers[mpegLayerVideo].phase = mpegPhaseDone
	old.layers[mpegLayerAudio].phase = mpegPhaseRunning
	cb.mpeg.vidState = mpegRunStopped
	cb.mpeg.audState = mpegRunStopped
	cb.mpeg.vidStatus = mpegVidLastShown | mpegVidBufEmpty
	cb.mpeg.audStatus = mpegAudBufEmpty

	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	if cb.mpeg.play == old {
		t.Fatal("$95 should rebuild a spent playback pipeline")
	}
	if cb.mpeg.vidState != mpegRunAwaitStream || cb.mpeg.audState != mpegRunAwaitStream {
		t.Errorf("run states = %d/%d, want await-stream", cb.mpeg.vidState, cb.mpeg.audState)
	}
	if cb.mpeg.vidStatus != 0 || cb.mpeg.audStatus != 0 {
		t.Errorf("statuses = 0x%04X/0x%02X, want 0/0 (end bits cleared)",
			cb.mpeg.vidStatus, cb.mpeg.audStatus)
	}
}

// TestCDBlockMpegPlayKeepsLivePlayback verifies $95 Play does not
// destroy an in-flight pipeline: with one layer terminal (audio ended
// first) but the video layer still decoding, a mid-movie $95
// (pause/resume) must keep the pipeline and its status words.
func TestCDBlockMpegPlayKeepsLivePlayback(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	old := cb.mpeg.play

	old.layers[mpegLayerAudio].phase = mpegPhaseDone
	old.layers[mpegLayerVideo].phase = mpegPhaseEnding
	old.layers[mpegLayerVideo].demux = new(mpeg.Demux)
	cb.mpeg.vidStatus = mpegVidDecoding

	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	if cb.mpeg.play != old {
		t.Fatal("$95 should keep a live playback pipeline")
	}
	if cb.mpeg.vidStatus != mpegVidDecoding {
		t.Errorf("vidStatus = 0x%04X, want 0x%04X (preserved)",
			cb.mpeg.vidStatus, uint16(mpegVidDecoding))
	}
}

// TestCDBlockMpegPlayStoresParameters verifies $95 stores the play
// parameters with 0xFF keep-current semantics (packing from the host
// library's command builder: CR1 low = play mode, CR2 high/low =
// audio/video transfer modes, CR4 low = the untraced fourth byte).
func TestCDBlockMpegPlayStoresParameters(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	execCommandFull(cb, 0x95, 0x01, 0xFF00, 0x0000, 0x00FF)
	if cb.mpeg.playMode != 1 {
		t.Errorf("playMode = %d, want 1", cb.mpeg.playMode)
	}
	if cb.mpeg.tmodA != 0 || cb.mpeg.tmodV != 0 {
		t.Errorf("tmod = %d/%d, want 0/0 (audio kept, video set)",
			cb.mpeg.tmodA, cb.mpeg.tmodV)
	}

	// 0xFF keeps every current value; explicit bytes update.
	execCommandFull(cb, 0x95, 0xFF, 0x01FF, 0x0000, 0x0012)
	if cb.mpeg.playMode != 1 {
		t.Errorf("playMode = %d, want 1 (kept)", cb.mpeg.playMode)
	}
	if cb.mpeg.tmodA != 1 || cb.mpeg.tmodV != 0 {
		t.Errorf("tmod = %d/%d, want 1/0", cb.mpeg.tmodA, cb.mpeg.tmodV)
	}
	if cb.mpeg.decV != 0x12 {
		t.Errorf("decV = 0x%02X, want 0x12", cb.mpeg.decV)
	}
}

// TestCDBlockMpegIndependentPlaySkipsStartHold verifies play mode 1
// (independent playback) releases the video start hold immediately:
// there is no A/V presentation sync to wait for. Synchronized mode
// keeps the bounded hold.
func TestCDBlockMpegIndependentPlaySkipsStartHold(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	if cb.mpegVideoStartDue(1) {
		t.Error("synchronized play should hold video at first")
	}

	execCommandFull(cb, 0x95, 0x01, 0xFF00, 0x0000, 0x00FF)
	if !cb.mpegVideoStartDue(1) {
		t.Error("independent play should not hold video")
	}
}

// TestCDBlockMpegSetDecodeMethodStoresParameters verifies $96 stores
// mute and the pause/freeze states with keep-current semantics (0xFF
// mute byte, 0xFFFF time words) over the $93 defaults. Time value 0 =
// pause/freeze on; 1 or a slow/strobe interval = normal playback.
func TestCDBlockMpegSetDecodeMethodStoresParameters(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	if cb.mpeg.audioMute != 0x04 || cb.mpeg.vidPaused || cb.mpeg.vidFrozen {
		t.Errorf("defaults = 0x%02X/%v/%v, want 0x04/false/false",
			cb.mpeg.audioMute, cb.mpeg.vidPaused, cb.mpeg.vidFrozen)
	}

	execCommandFull(cb, 0x96, 0x03, 0x0000, 0x0000, 0x0000)
	if cb.mpeg.audioMute != 0x03 || !cb.mpeg.vidPaused || !cb.mpeg.vidFrozen {
		t.Errorf("stored = 0x%02X/%v/%v, want 0x03/true/true",
			cb.mpeg.audioMute, cb.mpeg.vidPaused, cb.mpeg.vidFrozen)
	}

	execCommandFull(cb, 0x96, 0xFF, 0xFFFF, 0x0000, 0xFFFF)
	if cb.mpeg.audioMute != 0x03 || !cb.mpeg.vidPaused || !cb.mpeg.vidFrozen {
		t.Errorf("after keep = 0x%02X/%v/%v, want 0x03/true/true (unchanged)",
			cb.mpeg.audioMute, cb.mpeg.vidPaused, cb.mpeg.vidFrozen)
	}

	// A slow/strobe interval (>1) resumes normal playback.
	execCommandFull(cb, 0x96, 0xFF, 0x0002, 0x0000, 0x0002)
	if cb.mpeg.vidPaused || cb.mpeg.vidFrozen {
		t.Errorf("interval = %v/%v, want false/false (normal playback)",
			cb.mpeg.vidPaused, cb.mpeg.vidFrozen)
	}
}

// TestCDBlockMpegMuteSample verifies the $96 mute byte gates the
// stereo channels: bit 0 mutes right, bit 1 mutes left, 0x04 is the
// unmuted default.
func TestCDBlockMpegMuteSample(t *testing.T) {
	cases := []struct {
		mute         uint8
		wantL, wantR int16
	}{
		{0x04, 100, -100},
		{0x01, 100, 0},
		{0x02, 0, -100},
		{0x03, 0, 0},
	}
	for _, c := range cases {
		l, r := mpegMuteSample(c.mute, 100, -100)
		if l != c.wantL || r != c.wantR {
			t.Errorf("mute 0x%02X = %d/%d, want %d/%d", c.mute, l, r, c.wantL, c.wantR)
		}
	}
}

// TestCDBlockMpegOutDecodingSyncArmsSteps verifies $97 arms one
// decode step per command in host-synchronized decode timing, capped
// at the anti-burst limit, and is accepted-but-ignored in VSYNC
// decode timing or with no playback.
func TestCDBlockMpegOutDecodingSyncArmsSteps(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)

	// No playback yet: accepted, nothing to arm.
	execCommandFull(cb, 0x94, 0xFF, 0x01FF, 0xFF00, 0x0000)
	execCommandFull(cb, 0x97, 0x00, 0x0000, 0x0000, 0x0000)

	execCommandFull(cb, 0x95, 0x01, 0xFF00, 0x0000, 0x00FF)
	for i := 0; i < 6; i++ {
		execCommandFull(cb, 0x97, 0x00, 0x0000, 0x0000, 0x0000)
	}
	if got := cb.mpeg.play.hostSteps; got != 4 {
		t.Errorf("hostSteps = %d, want 4 (capped)", got)
	}

	// VSYNC decode timing: $97 arms nothing.
	execCommandFull(cb, 0x94, 0xFF, 0x00FF, 0xFF00, 0x0000)
	execCommandFull(cb, 0x97, 0x00, 0x0000, 0x0000, 0x0000)
	if got := cb.mpeg.play.hostSteps; got != 4 {
		t.Errorf("hostSteps = %d, want 4 (VSYNC mode ignores $97)", got)
	}
}

// TestCDBlockMpegHostSyncHoldsSteps verifies host-synchronized decode
// pacing: once playing, the picture accumulator does not free-run, and
// a pending step is held (not consumed) while the decoder is starved
// of data.
func TestCDBlockMpegHostSyncHoldsSteps(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x94, 0xFF, 0x01FF, 0xFF00, 0x0000)
	execCommandFull(cb, 0x95, 0x01, 0xFF00, 0x0000, 0x00FF)
	p := cb.mpeg.play
	p.pictCycles = 1000
	p.vidStarted = true
	cb.mpeg.vidState = mpegRunPlaying

	cb.mpegTick(5000)
	if p.pictAccum != 0 {
		t.Errorf("pictAccum = %d, want 0 (no free-run in host-sync mode)", p.pictAccum)
	}

	execCommandFull(cb, 0x97, 0x00, 0x0000, 0x0000, 0x0000)
	execCommandFull(cb, 0x97, 0x00, 0x0000, 0x0000, 0x0000)
	cb.mpegTick(5000)
	if p.hostSteps != 2 {
		t.Errorf("hostSteps = %d, want 2 (steps held while starved)", p.hostSteps)
	}
}

// TestCDBlockMpegPauseHaltsDecodePacing verifies pause-time 0 stops
// the picture-rate accumulator (decode pauses) and pause-time 1
// resumes it.
func TestCDBlockMpegPauseHaltsDecodePacing(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	p := cb.mpeg.play
	p.pictCycles = 1000
	p.vidStarted = true

	execCommandFull(cb, 0x96, 0xFF, 0x0000, 0x0000, 0xFFFF)
	cb.mpegTick(500)
	if p.pictAccum != 0 {
		t.Errorf("paused pictAccum = %d, want 0", p.pictAccum)
	}

	execCommandFull(cb, 0x96, 0xFF, 0x0001, 0x0000, 0xFFFF)
	cb.mpegTick(500)
	if p.pictAccum != 500 {
		t.Errorf("resumed pictAccum = %d, want 500", p.pictAccum)
	}
}

// TestCDBlockMpegFreezeHoldsPicture verifies freeze-time 0 keeps the
// displayed picture: decoded frames are not published while frozen,
// and publishing resumes when freeze lifts.
func TestCDBlockMpegFreezeHoldsPicture(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	cb.mpegSetDisplay(true)
	cb.mpegLatchFrame(mpegTestFrame(2, 2, 0x50, 0x90, 0x70))

	execCommandFull(cb, 0x96, 0xFF, 0xFFFF, 0x0000, 0x0000)
	cb.mpegLatchFrame(mpegTestFrame(4, 2, 0x80, 0x80, 0x80))
	if _, w, _, ok := cb.MpegFrameRGB(); !ok || w != 2 {
		t.Errorf("frozen frame = width %d ok=%v, want held 2x2 true", w, ok)
	}

	execCommandFull(cb, 0x96, 0xFF, 0xFFFF, 0x0000, 0x0001)
	cb.mpegLatchFrame(mpegTestFrame(4, 2, 0x80, 0x80, 0x80))
	if _, w, _, ok := cb.MpegFrameRGB(); !ok || w != 4 {
		t.Errorf("unfrozen frame = width %d ok=%v, want 4x2 true", w, ok)
	}
}

// mpegTestSector builds a data-track buffered sector whose 2048-byte
// user area starts with payload.
func mpegTestSector(payload []byte, submode uint8) bufferedSector {
	data := make([]byte, 2048)
	copy(data, payload)
	return bufferedSector{data: data, size: 2048, userOffset: 0, userSize: 2048, submode: submode}
}

// mpegConnectVideoDelete binds the video decoder to partition 0 in
// delete mode (conn-mode 0x07) with the audio layer disconnected - the
// video-only connection shape traced from a raw-ES title.
func mpegConnectVideoDelete(cb *CDBlock) {
	execCommandFull(cb, 0x9A, 0xFF, 0x00FF, 0x0007, 0x0000)
}

// TestCDBlockMpegRawESIdentification verifies stream-type
// identification on the first fed sector: a video sequence header
// start code (00 00 01 B3) at the stream head marks a raw video
// elementary stream, which feeds the decoder's elementary stream
// directly with no pack-stream/demux stage.
func TestCDBlockMpegRawESIdentification(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x01, 0xFF00, 0x0000, 0x00FF)
	mpegConnectVideoDelete(cb)
	cb.partitions[0].sectors = append(cb.partitions[0].sectors,
		mpegTestSector([]byte{0x00, 0x00, 0x01, 0xB3, 0x16, 0x00, 0xF0, 0xC4}, 0))
	cb.mpegFeedLayer(mpegLayerVideo)

	vl := &cb.mpeg.play.layers[mpegLayerVideo]
	if !vl.esProbed || !vl.esDirect {
		t.Fatalf("probe = probed:%v direct:%v, want both", vl.esProbed, vl.esDirect)
	}
	if vl.es.Remaining() != 2048 {
		t.Errorf("es remaining = %d, want 2048 (payload fed directly)", vl.es.Remaining())
	}
	if vl.ps.Remaining() != 0 || vl.demux != nil {
		t.Errorf("pack path touched: ps=%d demux=%v, want untouched", vl.ps.Remaining(), vl.demux)
	}
	if len(cb.partitions[0].sectors) != 0 {
		t.Error("delete mode should consume the fed sector")
	}
}

// TestCDBlockMpegPackStreamKeepsDemuxPath verifies the identification
// leaves a program stream (pack start code 00 00 01 BA) on the
// pack-stream/demux path.
func TestCDBlockMpegPackStreamKeepsDemuxPath(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
	mpegConnectVideoDelete(cb)
	cb.partitions[0].sectors = append(cb.partitions[0].sectors,
		mpegTestSector([]byte{0x00, 0x00, 0x01, 0xBA, 0x21, 0x00, 0x01, 0x00}, 0))
	cb.mpegFeedLayer(mpegLayerVideo)

	vl := &cb.mpeg.play.layers[mpegLayerVideo]
	if !vl.esProbed || vl.esDirect {
		t.Fatalf("probe = probed:%v direct:%v, want probed, not direct", vl.esProbed, vl.esDirect)
	}
	if vl.ps.Remaining() != 2048 {
		t.Errorf("ps remaining = %d, want 2048 (pack path)", vl.ps.Remaining())
	}
	if vl.es.Remaining() != 0 {
		t.Errorf("es remaining = %d, want 0", vl.es.Remaining())
	}
}

// TestCDBlockMpegRawESEndsOnSubmode verifies a raw elementary stream
// terminates through the normal completion path on the XA submode end
// mark: the elementary stream ends, the layer latches Done, and the
// sequence-end cause fires.
func TestCDBlockMpegRawESEndsOnSubmode(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x01, 0xFF00, 0x0000, 0x00FF)
	mpegConnectVideoDelete(cb)
	cb.partitions[0].sectors = append(cb.partitions[0].sectors,
		mpegTestSector([]byte{0x00, 0x00, 0x01, 0xB3, 0x16, 0x00, 0xF0, 0xC4}, 0),
		mpegTestSector(nil, 0x01)) // EOR sector
	cb.mpegFeedLayer(mpegLayerVideo)

	vl := &cb.mpeg.play.layers[mpegLayerVideo]
	if vl.phase != mpegPhaseEnding || !vl.esEnded || !vl.psEnded {
		t.Fatalf("after EOR: phase=%d esEnded=%v psEnded=%v, want Ending/true/true",
			vl.phase, vl.esEnded, vl.psEnded)
	}

	for i := 0; i < 8 && vl.phase != mpegPhaseDone; i++ {
		cb.mpegTick(cb.sysCyclesPerSec)
	}
	if vl.phase != mpegPhaseDone {
		t.Fatalf("phase = %d, want Done", vl.phase)
	}
	if cb.mpeg.vidState != mpegRunStopped {
		t.Errorf("vidState = %d, want stopped", cb.mpeg.vidState)
	}
	if cb.mpeg.intStatus&mpegIntSequenceEnd == 0 {
		t.Error("sequence-end cause should latch")
	}
}

// TestCDBlockMpegRawESEndsOnSequenceEndCode verifies the video
// sequence end code (00 00 01 B7) inside the raw elementary stream
// terminates it: data past the code is not fed.
func TestCDBlockMpegRawESEndsOnSequenceEndCode(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0x95, 0x01, 0xFF00, 0x0000, 0x00FF)
	mpegConnectVideoDelete(cb)
	sec := mpegTestSector([]byte{0x00, 0x00, 0x01, 0xB3, 0x16, 0x00, 0xF0, 0xC4}, 0)
	copy(sec.data[8:], []byte{0x00, 0x00, 0x01, 0xB7, 0xDE, 0xAD})
	cb.partitions[0].sectors = append(cb.partitions[0].sectors, sec)
	cb.mpegFeedLayer(mpegLayerVideo)

	vl := &cb.mpeg.play.layers[mpegLayerVideo]
	if vl.phase != mpegPhaseEnding || !vl.esEnded {
		t.Fatalf("after end code: phase=%d esEnded=%v, want Ending/true", vl.phase, vl.esEnded)
	}
	// Fed bytes: 12 through the end code, plus the 4-byte synthetic
	// bounding picture start code; the DE AD tail is not fed.
	if got := vl.es.Remaining(); got != 16 {
		t.Errorf("es remaining = %d, want 16 (ends at the sequence end code)", got)
	}
}

// TestCDBlockMpegPackScanPositional verifies the program-end scanner
// parses the system layer positionally: an end-code byte pattern
// inside a packet payload is skipped as payload, while a genuine
// system-layer end code reports, including when split across feeds.
func TestCDBlockMpegPackScanPositional(t *testing.T) {
	packHdr := []byte{0x00, 0x00, 0x01, 0xBA, 0x21, 0x00, 0x01, 0x00, 0x01, 0x80, 0x1F, 0x40}
	// Audio packet whose 6-byte payload embeds the end-code pattern.
	pes := []byte{0x00, 0x00, 0x01, 0xC0, 0x00, 0x06, 0x00, 0x00, 0x01, 0xB9, 0xAA, 0xBB}
	end := []byte{0x00, 0x00, 0x01, 0xB9}

	var s mpegPackScan
	if s.feed(packHdr) || s.feed(pes) {
		t.Fatal("end code inside packet payload must not report")
	}
	if !s.feed(end) {
		t.Fatal("system-layer end code should report")
	}

	// The same stream split mid-packet-length still skips the payload.
	s = mpegPackScan{}
	stream := append(append(append([]byte{}, packHdr...), pes...), end...)
	if s.feed(stream[:len(packHdr)+5]) {
		t.Fatal("split feed: no end code yet")
	}
	if !s.feed(stream[len(packHdr)+5:]) {
		t.Fatal("split feed: end code should report")
	}
}

// TestCDBlockMpegEndConditionsGatedByConnMode verifies the layer end
// conditions follow the connection-mode bits: EOR (submode bit 0)
// ends the layer only with mode bit 0x01, the system-end code only
// with mode bit 0x02, and EOF (submode bit 7) unconditionally.
func TestCDBlockMpegEndConditionsGatedByConnMode(t *testing.T) {
	endCode := []byte{0x00, 0x00, 0x01, 0xB9}
	setup := func(cmod uint16) *CDBlock {
		cb := NewCDBlock(nil)
		mpegInit(cb)
		execCommandFull(cb, 0x95, 0x00, 0xFF00, 0x0000, 0x00FF)
		// Video record: partition 0, the given conn-mode; audio
		// disconnected. A pack header keeps the ES probe on the
		// demux path.
		execCommandFull(cb, 0x9A, 0xFF, 0x00FF, cmod, 0x0000)
		return cb
	}
	feed := func(cb *CDBlock, payload []byte, submode uint8) mpegLayerPhase {
		cb.partitions[0].sectors = append(cb.partitions[0].sectors, mpegTestSector(payload, submode))
		cb.mpegFeedLayer(mpegLayerVideo)
		return cb.mpeg.play.layers[mpegLayerVideo].phase
	}

	// EOR with bit 0x01 clear: a record boundary, stream continues.
	cb := setup(0x0006)
	if got := feed(cb, nil, 0x01); got != mpegPhaseRunning {
		t.Errorf("EOR with mode 06: phase = %d, want Running", got)
	}
	// System-end code with bit 0x02 set ends it.
	if got := feed(cb, endCode, 0x00); got != mpegPhaseEnding {
		t.Errorf("end code with mode 06: phase = %d, want Ending", got)
	}

	// EOR with bit 0x01 set ends the layer.
	cb = setup(0x0007)
	if got := feed(cb, nil, 0x01); got != mpegPhaseEnding {
		t.Errorf("EOR with mode 07: phase = %d, want Ending", got)
	}

	// System-end code with bit 0x02 clear is ignored.
	cb = setup(0x0005)
	if got := feed(cb, endCode, 0x00); got != mpegPhaseRunning {
		t.Errorf("end code with mode 05: phase = %d, want Running", got)
	}

	// EOF ends the layer regardless of the mode bits.
	cb = setup(0x0004)
	if got := feed(cb, nil, 0x80); got != mpegPhaseEnding {
		t.Errorf("EOF with mode 04: phase = %d, want Ending", got)
	}
}

// TestCDBlockMpegSetWindowStoresSelectors verifies $A1 stores CR2-CR4
// under the CR1 low-byte selector (numbering pinned by the host
// library's builder stubs: 0 fb-position, 1 fb-ratio, 2 display
// position, 3 display size, 4 display offset).
func TestCDBlockMpegSetWindowStoresSelectors(t *testing.T) {
	cb := NewCDBlock(nil)
	mpegInit(cb)
	execCommandFull(cb, 0xA1, mpegWinFbPos, 0x0001, 22, 0)
	execCommandFull(cb, 0xA1, mpegWinFbRatio, 0x0001, 15, 1)
	execCommandFull(cb, 0xA1, mpegWinDispPos, 0x0001, 0, 8)
	execCommandFull(cb, 0xA1, mpegWinDispSiz, 0x0001, 320, 224)

	want := map[int][3]uint16{
		mpegWinFbPos:   {0x0001, 22, 0},
		mpegWinFbRatio: {0x0001, 15, 1},
		mpegWinDispPos: {0x0001, 0, 8},
		mpegWinDispSiz: {0x0001, 320, 224},
	}
	for sub, w := range want {
		if cb.mpeg.window[sub] != w {
			t.Errorf("window[%d] = %v, want %v", sub, cb.mpeg.window[sub], w)
		}
	}
}
