// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

// The row records carry each row's render state from the row setup
// to the renderer: the row's span chain and the params structs the
// compute functions consume, built in place by BeginLine. Both
// renderers render from the same record. The software renderer reads
// row y's record at the row's render point along the walk; the GPU
// renderer uploads the records of each batch of rows as the walk
// goes.
//
// Records have a fixed stride and are indexed by field line, so an
// entry point finds row y's params at a constant byte offset within
// record y. Every field is a 32 or 64-bit scalar or an array of them,
// the buffer-visible rule the params structs already follow.

// rowRecordMax bounds the field lines a frame can have.
const rowRecordMax = 512

// The VDP2 compute entry points by index: the span entries a row's
// chain names, then the frame-level ones the GPU renderer
// dispatches between rows.
const (
	entryNBGCell = iota
	entryNBGBitmap
	entryRBGCell
	entryRBGBitmap
	entryGradation
	entryComposite
	entryPalette
	entryCopyVRAM
	entryCopyCRAM
	entryGenerateTag
	entryGenerateWrite
	entryClearStamps
	entryClearLists
	entryResolve
	entryCopyVDP1
	entryCopyFB
	entryCount

	// entryExternal marks the external image row in a chain. It is
	// not a compute entry point and no renderer dispatches it: the row
	// setup captures the row into a layer buffer, and the chain
	// carries it so the layer it competes in is named the same way
	// every other layer is. It takes the number past the entry
	// points, so a chain word holds it like any other.
	entryExternal = entryCount
)

// entryNames is the compute function each entry constant runs. The
// GPU renderer resolves its pipelines through it and the
// software renderer calls the same functions directly, so a constant
// must name the function that constant means. The rows are written
// at their constants rather than in list order, so the constants and
// the VDP2 compute package's own Entries declaration share no
// ordering. TestEntryNames holds each row against its function and
// the whole set against what the compute packages declare.
var entryNames = [entryCount]string{
	entryNBGCell:       "NBGCellSpan",
	entryNBGBitmap:     "NBGBitmapSpan",
	entryRBGCell:       "RBGCellSpan",
	entryRBGBitmap:     "RBGBitmapSpan",
	entryGradation:     "GradationSpan",
	entryComposite:     "CompositeSpan",
	entryPalette:       "BuildPalette",
	entryCopyVRAM:      "CopyVRAMPages",
	entryCopyCRAM:      "CopyCRAMPages",
	entryGenerateTag:   "GenerateTag",
	entryGenerateWrite: "GenerateWrite",
	entryClearStamps:   "ClearStamps",
	entryClearLists:    "ClearLists",
	entryResolve:       "Resolve",
	entryCopyVDP1:      "CopyVDP1Pages",
	entryCopyFB:        "CopyFBPages",
}

// layerCount is the layer buffers a chain's spans write and the
// composite reads: NBG0 to NBG3, RBG0, and RBG1, indexed by the
// compute package's layer constants.
const layerCount = screen.LayerRBG1 + 1

// spanWord packs a chain entry: the compute entry point the span runs
// and the layer buffer it writes, the NBG and RBG spans their own
// layer, the gradation the layer it blurs, the composite 0, and the
// external row the NBG1 buffer it was captured into. The row setup
// decides both once; each renderer looks them up.
func spanWord(entry, layer int) uint32 {
	return uint32(entry)<<8 | uint32(layer)
}

// rbgEntry is the entry point a rotation layer's row runs, from its
// prepared params.
func rbgEntry(pp *screen.RBGParams) int {
	if pp.Screen.Bitmap {
		return entryRBGBitmap
	}
	return entryRBGCell
}

// spanKind unpacks a chain entry.
func spanKind(w uint32) (entry, layer int) {
	return int(w >> 8), int(w & 0xFF)
}

// lineRecord is one row. Spans holds the chain as spanWord packs it;
// NSpans is 0 for a row outside the display. Only the params of the
// row's enabled layers are current; the others hold whatever an
// earlier row left. The row's width is the frame's.
//
// Spans is sized past the largest chain: 4 NBG + RBG0 + gradation +
// composite = 7. RBG1 never adds an eighth, since BGON bit 5 (RBG1
// active) forces every NBG screen off in decodeLineState, so the NBG
// and RBG1 spans are mutually exclusive.
type lineRecord struct {
	NSpans uint32
	Spans  [8]uint32
	Pad    uint32 // keeps the params structs at 8-byte offsets
	NBG    [4]screen.NBGParams
	RBG    [2]screen.RBGParams // RBG0, RBG1
	Comp   screen.CompositeParams
	Grad   screen.GradationParams
}

// frameRows is the VDP2's records for the frame, indexed by field
// line. BeginLine writes row y's record for every line the walk sets
// up, the lines below the active count, with NSpans 0 for one the
// display does not show. A row at or past the active count keeps the
// record of the last frame that set it up, so a renderer reading rows
// there by index finds the chains of a taller display mode until that
// mode returns.
type frameRows struct {
	lines []lineRecord

	// siblings holds, by field line, the sibling record BeginLine
	// builds beside the row's with deinterlacing on
	// (siblingLineSetup): the other display line of the field line
	// pair, from the same line state. It is allocated by the first
	// frame that takes two records per line (latchRecordsPerLine).
	// Only the enhanced renderers read it, through deriveRows, and
	// only while recordsPerLine is 2.
	siblings []lineRecord
}

// rowRecord is row y's record.
func (v *VDP2) rowRecord(y int) *lineRecord { return &v.rows.lines[y] }

// siblingRecord is row y's sibling record.
func (v *VDP2) siblingRecord(y int) *lineRecord { return &v.rows.siblings[y] }

// placeRow places a copy of a row record at a row slot, the row of the
// layer and line color buffers its spans write and the composite
// reads: the Y of every span the chain names. The VDP2's own record
// keeps the frame row, which the sub-row derivations read; a renderer
// renders a placed copy. The output frame row is the composite's
// FBRow, which placing leaves as it is.
func placeRow(r *lineRecord, slot int32) {
	placeRowFrom(r, r, slot)
}

// placeRowFrom places dst, a copy of src, at a row slot, reading the
// chain from src and writing only the Ys into dst, so a copy in
// memory the CPU writes and does not read is placed without a read
// of it.
func placeRowFrom(dst, src *lineRecord, slot int32) {
	for i := 0; i < int(src.NSpans); i++ {
		switch entry, layer := spanKind(src.Spans[i]); entry {
		case entryNBGCell, entryNBGBitmap:
			dst.NBG[layer].Y = slot
		case entryRBGCell, entryRBGBitmap:
			dst.RBG[layer-screen.LayerRBG0].Y = slot
		case entryGradation:
			dst.Grad.Y = slot
		case entryComposite:
			dst.Comp.Y = slot
		}
	}
}

// rowInputs writes the inputs row y's chain names that no span writes
// into row slot of the layer and line color buffers, at the
// configuration's scale shift the buffers' rows are at: sub-row k of
// the external image row
// into the NBG1 layer buffer (exbgCopyRow), and an empty line color
// row for each rotation layer. A rotation span writes a dot's line
// color only where the coefficient supplies one, so the row starts
// empty and a dot without one reads as no-line-color rather than an
// earlier row's data. The software renderers write a row's inputs
// through it; the GPU renderer stages the same inputs for the device
// (gpuRowInputs).
func (v *VDP2) rowInputs(r *lineRecord, layers *[layerCount]compute.Layer, lineColors *[2]compute.LineColor, slot, y, k int) {
	width := v.frame.width << v.enhance.cfg.shift
	row := slot * width
	for i := 0; i < int(r.NSpans); i++ {
		switch entry, layer := spanKind(r.Spans[i]); entry {
		case entryExternal:
			v.exbgCopyRow(layers[layer], slot, y, k)
		case entryRBGCell, entryRBGBitmap:
			clear(lineColors[layer-screen.LayerRBG0][row : row+width])
		}
	}
}

func flag32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
