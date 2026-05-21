// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// distortedResumeState carries the in-progress state of a distorted
// sprite rasterizer. It has nested iteration: outerI walks edge
// steps, innerJ walks pixels along the current connecting line.
// innerJ < 0 means begin-line setup has not yet run for the current
// outerI value.
type distortedResumeState struct {
	charAddr           uint32
	charW, charH       int
	colorMode          uint16
	cc                 uint16
	ecdOff, spdOn      bool
	msbOn, mesh        bool
	userClip           uint16
	hss, hssOdd        bool
	flipH, flipV       bool
	bboxMinX, bboxMinY int
	bboxMaxX, bboxMaxY int
	clipX, clipY       int
	ax, ay             int
	bx, by             int
	cx, cy             int
	dx, dy             int
	dmax               int
	rowStride          int
	bpp8               bool
	simpleMode         bool
	gt                 [4]uint16
	checkEcd           bool
	colr               uint16
	uStart, uEnd       int
	dvFP               int

	leftEdge     ddaEdge
	rightEdge    ddaEdge
	gsOuterLeft  gouraudStepper
	gsOuterRight gouraudStepper
	vFP          int
	outerI       int
	innerJ       int
	curLx, curLy int
	curRx, curRy int
	rowBase      uint32
	lineDx       int
	lineDy       int
	lineLen      int
	jEnd         int // pre-clipped inner-loop upper bound (<= lineLen)
	pxFP, pyFP   int64
	dpxFP, dpyFP int64
	uFP          int
	duFP         int
	prevPx       int
	prevPy       int
	endCodeCount int
	gsLine       gouraudStepper
}

// startDistortedSprite runs setup once: coords, bbox, dmax, edges,
// gouraud outers, V-DDA delta, U range, row stride, color-mode
// dispatch helpers. Then enters the chunked rasterizer at outerI=0
// with innerJ=-1 (begin-line setup not yet run).
func (v *VDP1) startDistortedSprite(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	d := &v.distortedResume
	d.charAddr = uint32(cmd.srca) * 8
	d.charW = int((cmd.size>>8)&0x3F) * 8
	d.charH = int(cmd.size & 0xFF)
	d.colorMode = (cmd.pmod >> 3) & 0x07
	d.cc = cmd.pmod & 0x07
	d.ecdOff = cmd.pmod&0x0080 != 0
	d.spdOn = cmd.pmod&0x0040 != 0
	d.msbOn = cmd.pmod&0x8000 != 0
	d.mesh = cmd.pmod&0x0100 != 0
	d.userClip = (cmd.pmod >> 9) & 3
	d.hss = cmd.pmod&0x1000 != 0
	d.hssOdd = d.hss && v.fbcr&0x10 != 0
	d.flipH = cmd.ctrl&0x0010 != 0
	d.flipV = cmd.ctrl&0x0020 != 0

	if d.charW == 0 || d.charH == 0 {
		return 0, true
	}

	d.clipX, d.clipY = v.clipBounds()

	lx := int(v.localX)
	ly := int(v.localY)
	d.ax = int(cmd.xa) + lx
	d.ay = int(cmd.ya) + ly
	d.bx = int(cmd.xb) + lx
	d.by = int(cmd.yb) + ly
	d.cx = int(cmd.xc) + lx
	d.cy = int(cmd.yc) + ly
	d.dx = int(cmd.xd) + lx
	d.dy = int(cmd.yd) + ly

	d.bboxMinX = intMin(intMin(d.ax, d.bx), intMin(d.cx, d.dx))
	d.bboxMinY = intMin(intMin(d.ay, d.by), intMin(d.cy, d.dy))
	d.bboxMaxX = intMax(intMax(d.ax, d.bx), intMax(d.cx, d.dx))
	d.bboxMaxY = intMax(intMax(d.ay, d.by), intMax(d.cy, d.dy))

	preClip := cmd.pmod&0x0800 == 0
	if preClip && preClipReject(d.bboxMinX, d.bboxMinY, d.bboxMaxX, d.bboxMaxY, d.clipX, d.clipY) {
		return 0, true
	}

	chebAD := intAbs(d.dx - d.ax)
	if intAbs(d.dy-d.ay) > chebAD {
		chebAD = intAbs(d.dy - d.ay)
	}
	chebBC := intAbs(d.cx - d.bx)
	if intAbs(d.cy-d.by) > chebBC {
		chebBC = intAbs(d.cy - d.by)
	}
	d.dmax = chebAD
	if chebBC > d.dmax {
		d.dmax = chebBC
	}

	if d.cc >= 4 {
		d.gt = v.readGouraudTable(cmd.grda)
	}

	if d.dmax == 0 {
		// Degenerate quad collapses to a single pixel at (ax, ay).
		dot := v.readCharDot(d.charAddr, 0, 0, d.charW, d.colorMode)
		if !d.spdOn && dot == 0 {
			return 1, true
		}
		pixel := v.dotToPixel(dot, cmd.colr, d.colorMode)
		var gouraud uint16
		if d.cc >= 4 {
			gouraud = d.gt[0]
		}
		v.writePixel(d.ax, d.ay, pixel, d.cc, gouraud, d.msbOn, d.mesh, d.userClip, d.clipX, d.clipY)
		return 1, true
	}

	d.bpp8 = v.is8bpp()
	d.simpleMode = !v.dieDoubled() && d.cc == 0 && !d.msbOn && !d.mesh && d.userClip == 0 && !d.bpp8
	d.checkEcd = !(d.hss && d.dmax < d.charH) && !d.ecdOff
	d.colr = cmd.colr

	// Texture V interpolation using fixed-point DDA.
	vStart := 0
	vEnd := d.charH - 1
	if d.flipV {
		vStart, vEnd = vEnd, vStart
	}
	d.vFP = vStart << 16
	d.dvFP = ((vEnd - vStart) << 16) / d.dmax

	// Texture U range
	d.uStart = 0
	d.uEnd = d.charW - 1
	if d.flipH {
		d.uStart, d.uEnd = d.uEnd, d.uStart
	}

	switch d.colorMode {
	case 0, 1:
		d.rowStride = d.charW / 2
	case 2, 3, 4:
		d.rowStride = d.charW
	case 5:
		d.rowStride = d.charW * 2
	}

	d.leftEdge = initDDAEdge(d.ax, d.ay, d.dx, d.dy, d.dmax)
	d.rightEdge = initDDAEdge(d.bx, d.by, d.cx, d.cy, d.dmax)

	if d.cc >= 4 {
		d.gsOuterLeft = initGouraudStepper(d.gt[0], d.gt[3], d.dmax+1)
		d.gsOuterRight = initGouraudStepper(d.gt[1], d.gt[2], d.dmax+1)
	}

	d.outerI = 0
	d.innerJ = -1 // begin-line setup not yet run for current outerI

	v.cmdPhase = phaseDistortedSprite
	return v.runDistortedSprite(budget)
}

func (v *VDP1) resumeDistortedSprite(budget int32) (consumed int32, done bool) {
	return v.runDistortedSprite(budget)
}

// runDistortedSprite walks outerI=0..dmax. For each outerI, runs a
// begin-line setup (when innerJ==-1) that computes the connecting
// line endpoints, srcY, rowBase, line DDA deltas, gouraud line
// stepper, etc. Then the inner loop walks innerJ in chunks of
// pixelsPerYieldChunk, plotting along the connecting line. After
// the inner loop completes, outer edges and outer gouraud step,
// outerI advances, innerJ resets to -1.
func (v *VDP1) runDistortedSprite(budget int32) (consumed int32, done bool) {
	d := &v.distortedResume
	cycles := int32(0)
	fb := v.currentDrawFB()
	fbw := v.fbWidth()
	vram := v.vram
	vramMask := uint32(vdp1VRAMSize - 1)

	for d.outerI <= d.dmax {
		if d.innerJ < 0 {
			// Begin-line setup for this outerI.
			if d.outerI == 0 {
				d.curLx, d.curLy = d.ax, d.ay
				d.curRx, d.curRy = d.bx, d.by
			} else {
				d.curLx, d.curLy = d.leftEdge.intX(), d.leftEdge.intY()
				d.curRx, d.curRy = d.rightEdge.intX(), d.rightEdge.intY()
				if d.outerI == d.dmax {
					d.curLx, d.curLy = d.dx, d.dy
					d.curRx, d.curRy = d.cx, d.cy
				}
			}

			srcY := d.vFP >> 16
			if srcY < 0 {
				srcY = 0
			} else if srcY >= d.charH {
				srcY = d.charH - 1
			}
			if d.hss {
				if d.hssOdd {
					srcY |= 1
				} else {
					srcY &^= 1
				}
			}
			d.rowBase = d.charAddr + uint32(srcY*d.rowStride)

			d.lineDx = d.curRx - d.curLx
			d.lineDy = d.curRy - d.curLy
			d.lineLen = intAbs(d.lineDx)
			if intAbs(d.lineDy) > d.lineLen {
				d.lineLen = intAbs(d.lineDy)
			}

			if d.cc >= 4 {
				d.gsLine = initGouraudStepper(d.gsOuterLeft.value(), d.gsOuterRight.value(), d.lineLen+1)
			}

			if d.lineLen == 0 {
				// Single-pixel line. Plot it inline; no inner loop.
				u := d.uStart
				srcU := u
				if d.hss {
					if d.hssOdd {
						srcU |= 1
					} else {
						srcU &^= 1
					}
				}
				dot := v.readCharDot(d.charAddr, srcU, srcY, d.charW, d.colorMode)
				if d.spdOn || dot != 0 {
					pixel := v.dotToPixel(dot, d.colr, d.colorMode)
					var gouraud uint16
					if d.cc >= 4 {
						gouraud = d.gsLine.value()
					}
					v.writePixel(d.curLx, d.curLy, pixel, d.cc, gouraud, d.msbOn, d.mesh, d.userClip, d.clipX, d.clipY)
				}
				cycles++

				// Skip directly to outer-step bookkeeping.
				d.jEnd = d.lineLen
				d.innerJ = d.lineLen + 1
			} else {
				// Multi-pixel DDA line setup.
				d.pxFP = int64(d.curLx) << 16
				d.pyFP = int64(d.curLy) << 16
				d.dpxFP = (int64(d.lineDx) << 16) / int64(d.lineLen)
				d.dpyFP = (int64(d.lineDy) << 16) / int64(d.lineLen)
				d.uFP = d.uStart << 16
				d.duFP = ((d.uEnd - d.uStart) << 16) / d.lineLen
				d.prevPx = d.curLx
				d.prevPy = d.curLy
				d.endCodeCount = 0
				d.innerJ = 0
				d.jEnd = d.lineLen

				// Pre-clip the connecting line to the drawing area
				// (VDP1 manual pre-clipping). Iterating wholly /
				// partially off-screen spans is pure overdraw that
				// writePixel discards anyway, so skipping them leaves
				// output unchanged. Trailing skip is always safe;
				// leading skip is gated on !checkEcd because a
				// skipped span could hold a row-terminating end code
				// that affects visible pixels.
				if t0, t1, ok := clipSegParam(d.curLx, d.curLy, d.curRx, d.curRy, d.clipX, d.clipY); !ok {
					d.innerJ = d.lineLen + 1
				} else {
					if j1 := int(t1*float64(d.lineLen)) + 2; j1 < d.jEnd {
						d.jEnd = j1
					}
					if !d.checkEcd {
						if j0 := int(t0*float64(d.lineLen)) - 2; j0 > 0 {
							d.prevPx = int((d.pxFP + d.dpxFP*int64(j0-1)) >> 16)
							d.prevPy = int((d.pyFP + d.dpyFP*int64(j0-1)) >> 16)
							d.pxFP += d.dpxFP * int64(j0)
							d.pyFP += d.dpyFP * int64(j0)
							d.uFP += d.duFP * j0
							d.gsLine.advance(j0)
							d.innerJ = j0
						}
					}
				}
			}
		}

		// Inner DDA-line loop. Plots along the connecting line with
		// gap fill, end-code handling. Yields every pixelsPerYieldChunk
		// pixels.
		for d.innerJ <= d.jEnd {
			chunkEnd := d.innerJ + pixelsPerYieldChunk
			if chunkEnd > d.jEnd+1 {
				chunkEnd = d.jEnd + 1
			}
			for d.innerJ < chunkEnd {
				j := d.innerJ
				px := int(d.pxFP >> 16)
				py := int(d.pyFP >> 16)
				if j == d.lineLen {
					px = d.curRx
					py = d.curRy
				}

				u := d.uFP >> 16
				if u < 0 {
					u = 0
				} else if u >= d.charW {
					u = d.charW - 1
				}

				srcU := u
				if d.hss {
					if d.hssOdd {
						srcU |= 1
					} else {
						srcU &^= 1
					}
				}

				var dot uint16
				switch d.colorMode {
				case 0, 1:
					b := vram[(d.rowBase+uint32(srcU/2))&vramMask]
					if srcU&1 == 0 {
						dot = uint16(b >> 4)
					} else {
						dot = uint16(b & 0x0F)
					}
				case 2, 3, 4:
					dot = uint16(vram[(d.rowBase+uint32(srcU))&vramMask])
				case 5:
					a := (d.rowBase + uint32(srcU*2)) & vramMask
					dot = uint16(vram[a])<<8 | uint16(vram[(a+1)&vramMask])
				}

				if d.checkEcd && v.isEndCode(dot, d.colorMode) {
					d.endCodeCount++
					if d.endCodeCount >= 2 {
						// End-code break terminates the line early.
						d.innerJ = d.lineLen + 1
						break
					}
				} else if d.spdOn || dot != 0 {
					pixel := v.dotToPixel(dot, d.colr, d.colorMode)

					// Anti-alias hole fill. At a staircase step the
					// dropped pixel relative to the ADJACENT connecting
					// line lies along the outer-edge (inter-line)
					// direction, not the connecting line's own major
					// axis. Selecting the L-corner by the side-edge
					// (A->D) dominant axis fills toward the neighbour
					// line, closing the inter-line dropout (overdraw is
					// permitted).
					if j > 0 && px != d.prevPx && py != d.prevPy {
						var gfx, gfy int
						if intAbs(d.dx-d.ax) >= intAbs(d.dy-d.ay) {
							gfx, gfy = px, d.prevPy
						} else {
							gfx, gfy = d.prevPx, py
						}
						if gfx >= 0 && gfx <= d.clipX && gfy >= 0 && gfy <= d.clipY {
							if d.simpleMode {
								off := (gfy*fbw + gfx) * 2
								fb[off] = uint8(pixel >> 8)
								fb[off+1] = uint8(pixel)
							} else {
								var gouraud uint16
								if d.cc >= 4 {
									gouraud = d.gsLine.value()
								}
								v.writePixel(gfx, gfy, pixel, d.cc, gouraud, d.msbOn, d.mesh, d.userClip, d.clipX, d.clipY)
							}
						}
					}

					// Draw main pixel.
					if px >= 0 && px <= d.clipX && py >= 0 && py <= d.clipY {
						if d.simpleMode {
							off := (py*fbw + px) * 2
							fb[off] = uint8(pixel >> 8)
							fb[off+1] = uint8(pixel)
						} else {
							var gouraud uint16
							if d.cc >= 4 {
								gouraud = d.gsLine.value()
							}
							v.writePixel(px, py, pixel, d.cc, gouraud, d.msbOn, d.mesh, d.userClip, d.clipX, d.clipY)
						}
					}
				}

				d.prevPx = px
				d.prevPy = py
				if d.cc >= 4 {
					d.gsLine.step()
				}
				d.pxFP += d.dpxFP
				d.pyFP += d.dpyFP
				d.uFP += d.duFP

				d.innerJ++
				cycles++
			}

			if cycles >= budget && d.innerJ <= d.jEnd {
				v.cmdPhase = phaseDistortedSprite
				return cycles, false
			}
		}

		// Inner loop complete. Step outer state and advance outerI.
		if d.outerI < d.dmax {
			d.leftEdge.step()
			d.rightEdge.step()
			d.vFP += d.dvFP
		}
		if d.cc >= 4 {
			d.gsOuterLeft.step()
			d.gsOuterRight.step()
		}
		d.outerI++
		d.innerJ = -1
		if cycles >= budget && d.outerI <= d.dmax {
			v.cmdPhase = phaseDistortedSprite
			return cycles, false
		}
	}

	v.cmdPhase = phaseIdle
	if d.cc >= 4 {
		cycles += 4
	}
	if d.colorMode == 1 {
		cycles += 16
	}
	return cycles, true
}
