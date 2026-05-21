// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// polygonResumeState carries the in-progress state of a polygon
// rasterizer. Polygon draws as a quad of connecting lines, identical
// in structure to a distorted sprite but with a flat non-textured
// color instead of sampled character data.
type polygonResumeState struct {
	color        uint16
	cc           uint16
	msbOn, mesh  bool
	userClip     uint16
	clipX, clipY int
	simpleMode   bool
	gt           [4]uint16

	ax, ay int
	bx, by int
	cx, cy int
	dx, dy int
	dmax   int

	leftEdge     ddaEdge
	rightEdge    ddaEdge
	gsOuterLeft  gouraudStepper
	gsOuterRight gouraudStepper
	gsLine       gouraudStepper
	outerI       int
	innerJ       int
	curLx, curLy int
	curRx, curRy int
	lineDx       int
	lineDy       int
	lineLen      int
	jEnd         int // pre-clipped inner-loop upper bound (<= lineLen)
	pxFP, pyFP   int64
	dpxFP, dpyFP int64
	prevPx       int
	prevPy       int
}

// startPolygon runs setup once: bbox/clip, dmax computation,
// gouraud table, span-buffer reset, edge initialization. Then
// enters the edge-walk phase. dmax==0 degenerate is rasterized
// inline via bresenhamLine since it produces a single line.
func (v *VDP1) startPolygon(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	p := &v.polygonResume
	p.color = cmd.colr
	p.cc = cmd.pmod & 0x07
	p.msbOn = cmd.pmod&0x8000 != 0
	p.mesh = cmd.pmod&0x0100 != 0
	p.userClip = (cmd.pmod >> 9) & 3
	p.clipX, p.clipY = v.clipBounds()

	lx := int(v.localX)
	ly := int(v.localY)
	p.ax = int(cmd.xa) + lx
	p.ay = int(cmd.ya) + ly
	p.bx = int(cmd.xb) + lx
	p.by = int(cmd.yb) + ly
	p.cx = int(cmd.xc) + lx
	p.cy = int(cmd.yc) + ly
	p.dx = int(cmd.xd) + lx
	p.dy = int(cmd.yd) + ly

	bboxMinX := intMin(intMin(p.ax, p.bx), intMin(p.cx, p.dx))
	bboxMinY := intMin(intMin(p.ay, p.by), intMin(p.cy, p.dy))
	bboxMaxX := intMax(intMax(p.ax, p.bx), intMax(p.cx, p.dx))
	bboxMaxY := intMax(intMax(p.ay, p.by), intMax(p.cy, p.dy))

	preClip := cmd.pmod&0x0800 == 0
	if preClip && preClipReject(bboxMinX, bboxMinY, bboxMaxX, bboxMaxY, p.clipX, p.clipY) {
		return 0, true
	}

	chebAD := intAbs(p.dx - p.ax)
	if intAbs(p.dy-p.ay) > chebAD {
		chebAD = intAbs(p.dy - p.ay)
	}
	chebBC := intAbs(p.cx - p.bx)
	if intAbs(p.cy-p.by) > chebBC {
		chebBC = intAbs(p.cy - p.by)
	}
	p.dmax = chebAD
	if chebBC > p.dmax {
		p.dmax = chebBC
	}

	if p.cc >= 4 {
		p.gt = v.readGouraudTable(cmd.grda)
	}

	if p.dmax == 0 {
		// Degenerate quad collapses to A->B segment. Rasterize via
		// the existing atomic bresenhamLine. This is bounded by
		// max(|bx-ax|, |by-ay|) pixels which is small for any quad
		// flat enough to hit dmax=0.
		var gStart, gEnd uint16
		if p.cc >= 4 {
			gStart = p.gt[0]
			gEnd = p.gt[1]
		}
		v.bresenhamLine(p.ax, p.ay, p.bx, p.by, p.color, p.cc, gStart, gEnd, p.msbOn, p.mesh, p.userClip, p.clipX, p.clipY)
		cycles := int32(intMax(intAbs(p.bx-p.ax), intAbs(p.by-p.ay)) + 1)
		if p.cc >= 4 {
			cycles += 4
		}
		return cycles, true
	}

	// simpleMode takes the inline framebuffer-write fast path. It
	// excludes doubled-Y DIE (needs the writePixel parity/Y-halve
	// path), any color calc, MSB-on, mesh, user clip, and 8bpp -
	// identical predicate to the distorted-sprite path.
	p.simpleMode = !v.dieDoubled() && p.cc == 0 && !p.msbOn &&
		!p.mesh && p.userClip == 0 && !v.is8bpp()

	p.leftEdge = initDDAEdge(p.ax, p.ay, p.dx, p.dy, p.dmax)
	p.rightEdge = initDDAEdge(p.bx, p.by, p.cx, p.cy, p.dmax)

	if p.cc >= 4 {
		p.gsOuterLeft = initGouraudStepper(p.gt[0], p.gt[3], p.dmax+1)
		p.gsOuterRight = initGouraudStepper(p.gt[1], p.gt[2], p.dmax+1)
	}

	p.outerI = 0
	p.innerJ = -1 // begin-line setup not yet run for current outerI

	v.cmdPhase = phasePolygon
	return v.runPolygon(budget)
}

func (v *VDP1) resumePolygon(budget int32) (consumed int32, done bool) {
	return v.runPolygon(budget)
}

// runPolygon walks outerI=0..dmax. For each outerI, begin-line setup
// (when innerJ==-1) computes the connecting-line endpoints and DDA
// deltas, then the inner loop plots along the connecting line with
// anti-alias gap fill. Adjacent connecting lines overlap and
// double-write where they share pixels, matching hardware (manual
// Sec 1.6 "Anti-aliasing" / Sec 1.7). This is the flat-color polygon
// analogue of runDistortedSprite; the only differences are the
// constant p.color (no character read / texture / U / HSS / end-code /
// SPD) and the absent CLUT cycle adder.
func (v *VDP1) runPolygon(budget int32) (consumed int32, done bool) {
	p := &v.polygonResume
	cycles := int32(0)
	fb := v.currentDrawFB()
	fbw := v.fbWidth()
	pixel := p.color

	for p.outerI <= p.dmax {
		if p.innerJ < 0 {
			// Begin-line setup for this outerI. Endpoints are pinned
			// to the exact quad vertices at the first/last step to
			// prevent fixed-point drift.
			if p.outerI == 0 {
				p.curLx, p.curLy = p.ax, p.ay
				p.curRx, p.curRy = p.bx, p.by
			} else {
				p.curLx, p.curLy = p.leftEdge.intX(), p.leftEdge.intY()
				p.curRx, p.curRy = p.rightEdge.intX(), p.rightEdge.intY()
				if p.outerI == p.dmax {
					p.curLx, p.curLy = p.dx, p.dy
					p.curRx, p.curRy = p.cx, p.cy
				}
			}

			p.lineDx = p.curRx - p.curLx
			p.lineDy = p.curRy - p.curLy
			p.lineLen = intAbs(p.lineDx)
			if intAbs(p.lineDy) > p.lineLen {
				p.lineLen = intAbs(p.lineDy)
			}

			if p.cc >= 4 {
				p.gsLine = initGouraudStepper(
					p.gsOuterLeft.value(), p.gsOuterRight.value(),
					p.lineLen+1)
			}

			if p.lineLen == 0 {
				var gouraud uint16
				if p.cc >= 4 {
					gouraud = p.gsLine.value()
				}
				if p.simpleMode &&
					p.curLx >= 0 && p.curLx <= p.clipX &&
					p.curLy >= 0 && p.curLy <= p.clipY {
					off := (p.curLy*fbw + p.curLx) * 2
					fb[off] = uint8(pixel >> 8)
					fb[off+1] = uint8(pixel)
				} else if !p.simpleMode {
					v.writePixel(p.curLx, p.curLy, pixel, p.cc,
						gouraud, p.msbOn, p.mesh, p.userClip,
						p.clipX, p.clipY)
				}
				cycles++
				p.jEnd = p.lineLen
				p.innerJ = p.lineLen + 1
			} else {
				p.pxFP = int64(p.curLx) << 16
				p.pyFP = int64(p.curLy) << 16
				p.dpxFP = (int64(p.lineDx) << 16) / int64(p.lineLen)
				p.dpyFP = (int64(p.lineDy) << 16) / int64(p.lineLen)
				p.prevPx = p.curLx
				p.prevPy = p.curLy
				p.innerJ = 0
				p.jEnd = p.lineLen

				// Pre-clip the connecting line to the drawing area
				// (VDP1 manual pre-clipping). Polygons have no end
				// codes, so both leading and trailing off-screen
				// spans are safe to skip; output is unchanged because
				// writePixel already discarded those pixels.
				if t0, t1, ok := clipSegParam(p.curLx, p.curLy, p.curRx, p.curRy, p.clipX, p.clipY); !ok {
					p.innerJ = p.lineLen + 1
				} else {
					if j1 := int(t1*float64(p.lineLen)) + 2; j1 < p.jEnd {
						p.jEnd = j1
					}
					if j0 := int(t0*float64(p.lineLen)) - 2; j0 > 0 {
						p.prevPx = int((p.pxFP + p.dpxFP*int64(j0-1)) >> 16)
						p.prevPy = int((p.pyFP + p.dpyFP*int64(j0-1)) >> 16)
						p.pxFP += p.dpxFP * int64(j0)
						p.pyFP += p.dpyFP * int64(j0)
						p.gsLine.advance(j0)
						p.innerJ = j0
					}
				}
			}
		}

		for p.innerJ <= p.jEnd {
			chunkEnd := p.innerJ + pixelsPerYieldChunk
			if chunkEnd > p.jEnd+1 {
				chunkEnd = p.jEnd + 1
			}
			for p.innerJ < chunkEnd {
				j := p.innerJ
				px := int(p.pxFP >> 16)
				py := int(p.pyFP >> 16)
				if j == p.lineLen {
					px = p.curRx
					py = p.curRy
				}

				var gouraud uint16
				if p.cc >= 4 {
					gouraud = p.gsLine.value()
				}

				// Anti-alias hole fill. The dropped pixel relative to
				// the adjacent connecting line lies along the
				// outer-edge (inter-line) direction, so select the
				// L-corner by the side-edge (A->D) dominant axis to
				// fill toward the neighbour line and close the
				// inter-line dropout (overdraw is permitted).
				if j > 0 && px != p.prevPx && py != p.prevPy {
					var gfx, gfy int
					if intAbs(p.dx-p.ax) >= intAbs(p.dy-p.ay) {
						gfx, gfy = px, p.prevPy
					} else {
						gfx, gfy = p.prevPx, py
					}
					if p.simpleMode {
						if gfx >= 0 && gfx <= p.clipX &&
							gfy >= 0 && gfy <= p.clipY {
							off := (gfy*fbw + gfx) * 2
							fb[off] = uint8(pixel >> 8)
							fb[off+1] = uint8(pixel)
						}
					} else {
						v.writePixel(gfx, gfy, pixel, p.cc, gouraud,
							p.msbOn, p.mesh, p.userClip,
							p.clipX, p.clipY)
					}
				}

				// Draw main pixel.
				if p.simpleMode {
					if px >= 0 && px <= p.clipX &&
						py >= 0 && py <= p.clipY {
						off := (py*fbw + px) * 2
						fb[off] = uint8(pixel >> 8)
						fb[off+1] = uint8(pixel)
					}
				} else {
					v.writePixel(px, py, pixel, p.cc, gouraud,
						p.msbOn, p.mesh, p.userClip,
						p.clipX, p.clipY)
				}

				p.prevPx = px
				p.prevPy = py
				if p.cc >= 4 {
					p.gsLine.step()
				}
				p.pxFP += p.dpxFP
				p.pyFP += p.dpyFP
				p.innerJ++
				cycles++
			}
			if cycles >= budget && p.innerJ <= p.jEnd {
				v.cmdPhase = phasePolygon
				return cycles, false
			}
		}

		// Inner loop complete. Step outer state and advance outerI.
		if p.outerI < p.dmax {
			p.leftEdge.step()
			p.rightEdge.step()
		}
		if p.cc >= 4 {
			p.gsOuterLeft.step()
			p.gsOuterRight.step()
		}
		p.outerI++
		p.innerJ = -1
		if cycles >= budget && p.outerI <= p.dmax {
			v.cmdPhase = phasePolygon
			return cycles, false
		}
	}

	v.cmdPhase = phaseIdle
	if p.cc >= 4 {
		cycles += 4
	}
	return cycles, true
}
