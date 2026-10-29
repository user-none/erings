// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"math/bits"

	"github.com/user-none/erings/core/compute"
)

// renderSizes is the length of each renderer-owned buffer. A renderer
// allocates the buffers it has at these lengths and grows one when a
// frame needs more than it holds; none shrinks.
type renderSizes struct {
	layerWords       int // a layer buffer, words
	lineColorBytes   int // a rotation screen's line color buffer, bytes
	framebufferWords int // the output frame buffer, words
	rowRecords       int // the GPU renderer's row records, one per row slot
	copyRows         int // the GPU renderer's page copy params rows, two per native row slot
	pairBytes        int // a half of the VDP1 pair, bytes
	stamps           int // the order stamps, words
	heads            int // the fragment list heads, words
	pool             int // the fragment pool, words
}

// bufferSizes gives the length of every renderer-owned buffer at the
// scale shift for a renderer holding slots row slots, a frame width
// dots wide with height displayed rows, a VDP1 frame buffer mode of
// fbPixels pixels at their finest addressing, and the pair's row
// factor (VDP2.rowFactor), 2 when a half holds every row of the
// doubled Y range, which doubles the pair and the stamps and heads
// that hold one entry per pair pixel. A row slot is a row of the
// layer and line color buffers, so those hold the rows a renderer has
// between their setup and their render, never a whole frame. The
// dynamic buffers are rounded up to a power of two, the mask their
// helpers need.
func bufferSizes(shift uint32, slots, width, height, fbPixels, rowFactor int) renderSizes {
	rows := slots * (width << shift)
	area := 2 * shift
	return renderSizes{
		layerWords:       pow2(rows),
		lineColorBytes:   pow2(rows),
		framebufferWords: pow2((width << shift) * (height << shift)),
		rowRecords:       slots,
		copyRows:         2 * (slots >> shift),
		pairBytes:        compute.VDP1FBSize * rowFactor << area,
		stamps:           pow2(fbPixels * rowFactor << area),
		heads:            compute.VDP1FBSize / 2 * rowFactor << area,
		pool:             gpuFragmentPoolWords,
	}
}

// pow2 is the smallest power of two at or above n, 1 for n below 2.
func pow2(n int) int {
	if n < 2 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}
