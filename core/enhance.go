// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "github.com/user-none/erings/core/compute/draw"

// The enhancements: the options that change the output from the
// native picture, which only the enhanced renderers apply. The
// accurate renderers at native resolution are the base and render
// with every enhancement off; the enhanced renderers are selected
// whenever any enhancement is on, including at 1x, and read the
// configuration from the VDP2's enhancement state, which the emulator
// sets before it opens a renderer. The shared derivation code the two
// enhanced renderers run (the row derivation here, the op derivation
// in renderer_enhanced.go) carries the enhancement logic; each
// renderer keeps only where the derived records and ops go and when
// its VDP1 ops execute.

// enhanceConfig is the enhancement configuration: the scale shift,
// log2 of the scale N the frame renders at (0 native), and whether
// deinterlacing is on, which renders both parities of each field under
// double-density interlace.
type enhanceConfig struct {
	shift       uint32
	deinterlace bool
}

// enabled reports whether any enhancement is on, which selects the
// enhanced renderers.
func (c enhanceConfig) enabled() bool { return c.shift != 0 || c.deinterlace }

// fbHalfLayout is the layout of one half of the renderer-owned VDP1
// pair, by physical index. A native half mirrors the CPU buffer row
// for row. A doubled half holds both DIL parities of a field drawn with
// DIE on under effective LSMD=3 with deinterlacing on: doubled row
// 2r + dil is the CPU buffer's row r, and 2r + 1 - dil is the other
// parity's row r. dil is the DIL parity the field drew with.
type fbHalfLayout struct {
	doubled bool
	dil     int
}

// enhanceState is the VDP2's enhancement state: the configuration the
// emulator set, and the layout of each half of the renderer-owned pair
// as the renderer's frame start last recorded it (beginField).
// pairDoubled is set at the session's first doubled half and doubles
// the pair, stamps, and heads from then on (rowFactor). rowsDoubled is
// set at the first frame with two records per line and doubles the row
// stores from then on (maxRowSlots). A game that never draws a doubled
// half or never shows double-density interlace keeps the native sizes.
type enhanceState struct {
	cfg         enhanceConfig
	half        [2]fbHalfLayout
	pairDoubled bool
	rowsDoubled bool
}

// setEnhance takes the configuration the emulator sets before it opens
// a renderer. Turning deinterlacing off resets the halves to native,
// since an accurate renderer never takes and nothing else would.
// rowsDoubled is kept. maxRowSlots ignores it while deinterlacing is
// off, and turning deinterlacing back on sizes for two records again.
func (v *VDP2) setEnhance(cfg enhanceConfig) {
	v.enhance.cfg = cfg
	if !cfg.deinterlace {
		v.enhance.half = [2]fbHalfLayout{}
	}
}

// latchRecordsPerLine runs at BeginFrame, once the frame's effective
// interlace is latched. The first frame with two records per line
// (recordsPerLine) sets rowsDoubled, which doubles the row stores from
// then on (maxRowSlots), and allocates the sibling record store, so a
// session that never shows double-density interlace with deinterlacing
// on allocates neither. That frame changes the effective interlace
// from the frame before, so BeginFrame marks it resized and the
// renderers grow their row stores at its frame start. The options are
// set before the first frame, so a frame with two records per line
// always follows one without.
func (v *VDP2) latchRecordsPerLine() {
	if v.recordsPerLine() != 2 || v.enhance.rowsDoubled {
		return
	}
	v.enhance.rowsDoubled = true
	if v.rows.siblings == nil {
		v.rows.siblings = make([]lineRecord, rowRecordMax)
	}
}

// pageCopyParams returns the params for copying the CPU's frame buffer
// writes into half h of the pair: the configured scale, the frame
// buffer's width and pixel size, and the half's layout. In a doubled
// half each native row goes to both doubled rows. A CPU write under
// double interlace is the same picture in both buffers, so both
// parities show it, as on hardware. The whole-half copy after a layout
// change (beginField) also fills both parities of the rows the field
// does not draw.
func (v *VDP2) pageCopyParams(v1 *VDP1, h uint32) draw.PageCopyParams {
	p := draw.PageCopyParams{ScaleShift: v.enhance.cfg.shift, FBWidth: uint32(v1.fbWidth()), Is8bpp: v1.is8bpp()}
	if v.enhance.half[h&1].doubled {
		p.Doubled = 1
	}
	return p
}

// rowFactor returns the factor the renderer-owned pair, stamps, and
// heads are sized by: 2 once a doubled half has been recorded, 1
// before.
func (v *VDP2) rowFactor() int {
	if v.enhance.pairDoubled {
		return 2
	}
	return 1
}

// beginField runs at an enhanced renderer's frame start, after the
// VDP1 frame change and VDP2.BeginFrame and before the field's first
// tick. It records the draw half's layout from the active FBCR, whose
// DIE and DIL latch at the frame change, and the frame's effective
// interlace. When a half's layout changes, its whole CPU buffer is
// marked written, so the field's first take recopies it in the new
// layout before the field's draws land on it. It returns true at the
// session's first doubled half. The renderer then grows its pair,
// stamps, and heads (rowFactor), and both halves are marked, since the
// grown pair replaces both.
func (v *VDP2) beginField(v1 *VDP1) (grow bool) {
	h := v1.physicalBuffer(v1.drawFB)
	l := fbHalfLayout{doubled: v.enhance.cfg.deinterlace && v1.dieEnabled() && v.frame.effIntl == 3}
	if v1.dilOdd() {
		l.dil = 1
	}
	grow = l.doubled && !v.enhance.pairDoubled
	switch {
	case grow:
		v.enhance.pairDoubled = true
		v1.resyncFB()
	case l.doubled != v.enhance.half[h].doubled:
		v1.resyncFBHalf(h)
	}
	v.enhance.half[h] = l
	return grow
}

// opTake takes the VDP1's list entries for a renderer that draws its
// own pair. It yields the entries not yet taken, up to the first whose
// line records are not final (VDP1.opsReady), or all of them at the
// end of the display. At scale a distorted sprite reads the record of
// the line after its last, which the walk writes a tick or two later,
// so it and the entries after it wait. At native nothing waits. With
// derive set, each entry is also derived for an enhanced pair
// (deriveOp). The accurate GPU renderer takes the entries as they are.
type opTake struct {
	v1     *VDP1
	v2     *VDP2
	shift  uint32
	derive bool
	end    int
}

// beginOps starts a take of the entries at the configured scale,
// derived for an enhanced pair when derive is set, every entry when
// all is set.
func (v *VDP2) beginOps(v1 *VDP1, derive, all bool) opTake {
	shift := v.enhance.cfg.shift
	return opTake{v1: v1, v2: v, shift: shift, derive: derive, end: v1.opsReady(!all && shift != 0)}
}

// next takes the next entry: the entry, and its derived form in dst
// when the take derives, or false when the take is done.
func (t *opTake) next(dst *draw.DrawOp) (*draw.DrawOp, bool) {
	v := t.v1
	if v.opsTaken >= t.end {
		return nil, false
	}
	src := &v.ops[v.opsTaken]
	v.opsTaken++
	if t.derive {
		deriveOp(dst, src, t.shift, t.v2.enhance.half[src.Buffer&1])
	}
	return src, true
}

// recordsPerLine returns the records BeginLine builds per field line:
// 2 (the row's and its sibling's, siblingLineSetup) with deinterlacing
// on under effective LSMD=3, otherwise 1. The NBG mosaic downgrade
// lowers the effective interlace, so it has no sibling.
func (v *VDP2) recordsPerLine() int {
	if v.enhance.cfg.deinterlace && v.frame.effIntl == 3 {
		return 2
	}
	return 1
}

// rowSlots returns the row slots a row of the frame takes: N sub-rows
// per record.
func (v *VDP2) rowSlots() int { return v.recordsPerLine() << v.enhance.cfg.shift }

// maxRowSlots returns the row slots to size row stores and batch slots
// for: two records' worth once a frame has taken two with
// deinterlacing on (latchRecordsPerLine), otherwise one record's.
func (v *VDP2) maxRowSlots() int {
	if v.enhance.cfg.deinterlace && v.enhance.rowsDoubled {
		return 2 << v.enhance.cfg.shift
	}
	return 1 << v.enhance.cfg.shift
}

// deriveRows derives the sub-row records of field line y at the
// configured scale into dst: for each of the line's records, the row's
// and then the sibling's, its N sub-rows in order (scaleRow), so dst
// holds rowSlots() records. It reads the rotation state as the row's
// setup left it, so it runs at the row's setup point. The caller places
// the derived records at its row slots (placeRow) when it knows them:
// the software renderer at once, the GPU renderer at the upload of the
// row's batch.
func (v *VDP2) deriveRows(dst []lineRecord, y int) {
	n := 1 << v.enhance.cfg.shift
	for r := range v.recordsPerLine() {
		rec := v.rowRecord(y)
		if r == 1 {
			rec = v.siblingRecord(y)
		}
		for k := range n {
			v.scaleRow(&dst[r*n+k], rec, int32(k))
		}
	}
}
