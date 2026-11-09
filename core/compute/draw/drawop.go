// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package draw

// Draw op kinds, the VDP1 command types that draw.
const (
	OpNormalSprite    = 0
	OpScaledSprite    = 1
	OpDistortedSprite = 2
	OpPolygon         = 3
	OpPolyline        = 4
	OpLine            = 5
	OpErase           = 6 // an erase of the frame buffer, not a command
)

// Draw op flag bits.
const (
	OpFlagECDOff  = 1 << 0 // end code detection disabled (CMDPMOD ECD)
	OpFlagSPDOn   = 1 << 1 // transparent dots drawn (CMDPMOD SPD)
	OpFlagFlipH   = 1 << 2 // character read right to left
	OpFlagFlipV   = 1 << 3 // character read bottom to top
	OpFlagHSS     = 1 << 4 // high speed shrink (CMDPMOD HSS)
	OpFlagHSSOdd  = 1 << 5 // high speed shrink samples odd source dots (FBCR EOS)
	OpFlagPreClip = 1 << 6 // pre-clipping performed (CMDPMOD Pclp = 0)
)

// DrawOp is one drawing command as the command list walk resolved it:
// the command's kind, the frame buffer it draws into, the pixel write
// parameters in effect (clip state and frame buffer mode included),
// the character and color fields, and the vertices with the local
// coordinates applied. It is self-contained: a rasterizer needs no
// walk state to draw it.
//
// In a frame's list an op appears as one or more entries, each the
// range of the op's steps the walk drew between two points where its
// list took an event: the lines [LineFrom, LineTo), the first from
// step LineStart and the last to step LineEnd. A device draws an
// entry's range and nothing else, so the list orders the op's parts
// against the events between them as the walk did.
//
// An erase (OpErase) is the frame change's or the manual erase's fill
// of a frame buffer rectangle with EWDR, recorded in the list where
// the walk performed it so a renderer drawing the list replays it in
// order against the draws: X[0], Y[0] and X[2], Y[2] hold the
// inclusive rectangle,
// Color holds EWDR, and Pixel holds the frame buffer format (Is8bpp
// and FBWidth); the other fields are zero.
//
// Vertices by kind: a normal sprite carries its draw position in A; a
// scaled sprite the resolved destination rectangle, upper-left in A
// and lower-right in C, with the flip flags already combined with the
// coordinate order; a distorted sprite, polygon, and polyline the
// four vertices A to D; a line its two endpoints in A and B. The
// values a rasterizer derives per command (the edge step count, the
// texture steps, the row stride) are not stored.
type DrawOp struct {
	Kind       uint32
	Seq        uint32 // the entry's index in the frame's list
	LineFrom   uint32 // the lines a device draws of the entry, [LineFrom, LineTo); LineTo 0 draws to the last
	LineTo     uint32
	LineStart  uint32 // the first step drawn of line LineFrom
	LineEnd    uint32 // the exclusive end step of the entry's last line; 0 draws the whole line
	Addr       uint32 // command table address
	Buffer     uint32 // physical frame buffer drawn into, 0 or 1
	RecordBase uint32 // the op's first line record (records.go), when recorded
	Pixel      PixelParams
	CharAddr   uint32
	CharW      int32
	CharH      int32
	ColorMode  uint32 // CMDPMOD bits 5:3
	Color      uint32 // CMDCOLR
	Flags      uint32
	X          [4]int32
	Y          [4]int32
	Gouraud    [4]uint32 // the Gouraud table, read when the color calculation uses it
}
