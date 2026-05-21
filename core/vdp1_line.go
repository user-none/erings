// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// lineResumeState carries the in-progress state of a single line
// or a polyline. polylineLeg < 0 means a single line; 0..3 selects
// which of the four polyline legs is currently in progress.
type lineResumeState struct {
	color        uint16
	cc           uint16
	msbOn, mesh  bool
	userClip     uint16
	clipX, clipY int
	gStart, gEnd uint16

	x, y    int
	dx, dy  int
	sx, sy  int
	err     int
	iter    int
	iterMax int
	xMajor  bool

	polylineLeg  int
	legAx, legAy int
	legBx, legBy int
	legCx, legCy int
	legDx, legDy int
	polylineGt   [4]uint16
}

// initBresenhamState populates lineResume bresenham fields from the
// given endpoints. Called at the start of each line/polyline-leg.
func initBresenhamState(s *lineResumeState, x1, y1, x2, y2 int) {
	s.x = x1
	s.y = y1
	s.dx = x2 - x1
	s.dy = y2 - y1
	s.sx = 1
	if s.dx < 0 {
		s.sx = -1
		s.dx = -s.dx
	}
	s.sy = 1
	if s.dy < 0 {
		s.sy = -1
		s.dy = -s.dy
	}
	if s.dx >= s.dy {
		s.xMajor = true
		s.iterMax = s.dx
		s.err = -s.dx
	} else {
		s.xMajor = false
		s.iterMax = s.dy
		s.err = -s.dy
	}
	s.iter = 0
}

// runBresenhamLine walks the bresenham state in lineResume in
// chunks of pixelsPerYieldChunk. Returns done=true when the current
// line completes (iter > iterMax). Caller is responsible for any
// completion-time bookkeeping (gouraud cost adders, advancing
// polyline leg, clearing cmdPhase).
func (v *VDP1) runBresenhamLine(budget int32) (consumed int32, done bool) {
	s := &v.lineResume
	cycles := int32(0)

	for s.iter <= s.iterMax {
		chunkEnd := s.iter + pixelsPerYieldChunk
		if chunkEnd > s.iterMax+1 {
			chunkEnd = s.iterMax + 1
		}
		for s.iter < chunkEnd {
			var gouraud uint16
			if s.cc >= 4 {
				gouraud = lerpGouraud(s.gStart, s.gEnd, s.iter, s.iterMax+1)
			}
			v.writePixel(s.x, s.y, s.color, s.cc, gouraud, s.msbOn, s.mesh, s.userClip, s.clipX, s.clipY)

			if s.xMajor {
				s.x += s.sx
				s.err += 2 * s.dy
				if s.err >= 0 {
					s.y += s.sy
					s.err -= 2 * s.dx
				}
			} else {
				s.y += s.sy
				s.err += 2 * s.dx
				if s.err >= 0 {
					s.x += s.sx
					s.err -= 2 * s.dy
				}
			}
			s.iter++
			cycles++
		}
		if cycles >= budget && s.iter <= s.iterMax {
			return cycles, false
		}
	}
	return cycles, true
}

// startLine sets up lineResume for a single line (polylineLeg=-1)
// and runs the chunked bresenham walker.
func (v *VDP1) startLine(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	s := &v.lineResume
	s.color = cmd.colr
	s.cc = cmd.pmod & 0x07
	s.msbOn = cmd.pmod&0x8000 != 0
	s.mesh = cmd.pmod&0x0100 != 0
	s.userClip = (cmd.pmod >> 9) & 3
	s.clipX, s.clipY = v.clipBounds()

	x1 := int(cmd.xa) + int(v.localX)
	y1 := int(cmd.ya) + int(v.localY)
	x2 := int(cmd.xb) + int(v.localX)
	y2 := int(cmd.yb) + int(v.localY)

	preClip := cmd.pmod&0x0800 == 0
	if preClip && preClipReject(intMin(x1, x2), intMin(y1, y2), intMax(x1, x2), intMax(y1, y2), s.clipX, s.clipY) {
		return 0, true
	}

	if s.cc >= 4 {
		gt := v.readGouraudTable(cmd.grda)
		s.gStart = gt[0]
		s.gEnd = gt[1]
	} else {
		s.gStart = 0
		s.gEnd = 0
	}

	s.polylineLeg = -1
	initBresenhamState(s, x1, y1, x2, y2)

	v.cmdPhase = phaseLine
	cycles, ldone := v.runBresenhamLine(budget)
	if !ldone {
		return cycles, false
	}

	v.cmdPhase = phaseIdle
	if s.cc >= 4 {
		cycles += 4
	}
	return cycles, true
}

// resumeLine continues an in-progress line.
func (v *VDP1) resumeLine(budget int32) (consumed int32, done bool) {
	s := &v.lineResume
	cycles, ldone := v.runBresenhamLine(budget)
	if !ldone {
		return cycles, false
	}

	v.cmdPhase = phaseIdle
	if s.cc >= 4 {
		cycles += 4
	}
	return cycles, true
}

// startPolyline sets up lineResume for the four legs (A->B, B->C,
// C->D, D->A), initializes bresenham for leg 0, and starts walking.
func (v *VDP1) startPolyline(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	s := &v.lineResume
	s.color = cmd.colr
	s.cc = cmd.pmod & 0x07
	s.msbOn = cmd.pmod&0x8000 != 0
	s.mesh = cmd.pmod&0x0100 != 0
	s.userClip = (cmd.pmod >> 9) & 3
	s.clipX, s.clipY = v.clipBounds()

	lx := int(v.localX)
	ly := int(v.localY)
	s.legAx = int(cmd.xa) + lx
	s.legAy = int(cmd.ya) + ly
	s.legBx = int(cmd.xb) + lx
	s.legBy = int(cmd.yb) + ly
	s.legCx = int(cmd.xc) + lx
	s.legCy = int(cmd.yc) + ly
	s.legDx = int(cmd.xd) + lx
	s.legDy = int(cmd.yd) + ly

	preClip := cmd.pmod&0x0800 == 0
	if preClip {
		minX := intMin(intMin(s.legAx, s.legBx), intMin(s.legCx, s.legDx))
		minY := intMin(intMin(s.legAy, s.legBy), intMin(s.legCy, s.legDy))
		maxX := intMax(intMax(s.legAx, s.legBx), intMax(s.legCx, s.legDx))
		maxY := intMax(intMax(s.legAy, s.legBy), intMax(s.legCy, s.legDy))
		if preClipReject(minX, minY, maxX, maxY, s.clipX, s.clipY) {
			return 0, true
		}
	}

	if s.cc >= 4 {
		s.polylineGt = v.readGouraudTable(cmd.grda)
	} else {
		s.polylineGt = [4]uint16{}
	}

	s.polylineLeg = 0
	v.polylineSetupLeg(0)

	v.cmdPhase = phasePolyline
	return v.runPolyline(budget)
}

// resumePolyline continues an in-progress polyline.
func (v *VDP1) resumePolyline(budget int32) (consumed int32, done bool) {
	return v.runPolyline(budget)
}

// polylineSetupLeg initializes bresenham state and gouraud
// endpoints for the given leg index (0=A->B, 1=B->C, 2=C->D,
// 3=D->A).
func (v *VDP1) polylineSetupLeg(leg int) {
	s := &v.lineResume
	var x1, y1, x2, y2 int
	var gIdxStart, gIdxEnd int
	switch leg {
	case 0:
		x1, y1, x2, y2 = s.legAx, s.legAy, s.legBx, s.legBy
		gIdxStart, gIdxEnd = 0, 1
	case 1:
		x1, y1, x2, y2 = s.legBx, s.legBy, s.legCx, s.legCy
		gIdxStart, gIdxEnd = 1, 2
	case 2:
		x1, y1, x2, y2 = s.legCx, s.legCy, s.legDx, s.legDy
		gIdxStart, gIdxEnd = 2, 3
	case 3:
		x1, y1, x2, y2 = s.legDx, s.legDy, s.legAx, s.legAy
		gIdxStart, gIdxEnd = 3, 0
	}
	if s.cc >= 4 {
		s.gStart = s.polylineGt[gIdxStart]
		s.gEnd = s.polylineGt[gIdxEnd]
	} else {
		s.gStart = 0
		s.gEnd = 0
	}
	initBresenhamState(s, x1, y1, x2, y2)
}

// runPolyline walks the four legs in sequence, yielding when the
// per-call budget is exhausted. Each leg uses runBresenhamLine.
func (v *VDP1) runPolyline(budget int32) (consumed int32, done bool) {
	s := &v.lineResume
	cycles := int32(0)

	for s.polylineLeg < 4 {
		c, ldone := v.runBresenhamLine(budget - cycles)
		cycles += c
		if !ldone {
			v.cmdPhase = phasePolyline
			return cycles, false
		}
		// Per-leg overhead.
		cycles++
		s.polylineLeg++
		if s.polylineLeg < 4 {
			v.polylineSetupLeg(s.polylineLeg)
		}
		if cycles >= budget && s.polylineLeg < 4 {
			v.cmdPhase = phasePolyline
			return cycles, false
		}
	}

	v.cmdPhase = phaseIdle
	if s.cc >= 4 {
		cycles += 4
	}
	return cycles, true
}
