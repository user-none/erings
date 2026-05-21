// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// startScaledSprite runs setup once (zoom-point resolution, dest
// rectangle, HSS state, gouraud table), then enters the chunked
// rasterizer. Iterator walks (dy, dx) over the dest rectangle and
// midpoint-samples the source.
func (v *VDP1) startScaledSprite(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
	s := &v.spriteResume
	s.charAddr = uint32(cmd.srca) * 8
	s.charW = int((cmd.size>>8)&0x3F) * 8
	s.charH = int(cmd.size & 0xFF)
	s.colorMode = (cmd.pmod >> 3) & 0x07
	s.cc = cmd.pmod & 0x07
	s.ecdOff = cmd.pmod&0x0080 != 0
	s.spdOn = cmd.pmod&0x0040 != 0
	s.msbOn = cmd.pmod&0x8000 != 0
	s.mesh = cmd.pmod&0x0100 != 0
	s.userClip = (cmd.pmod >> 9) & 3
	s.flipH = cmd.ctrl&0x0010 != 0
	s.flipV = cmd.ctrl&0x0020 != 0
	hss := cmd.pmod&0x1000 != 0
	s.isScaled = true

	if s.charW == 0 || s.charH == 0 {
		return 0, true
	}

	zp := (cmd.ctrl >> 8) & 0x0F
	lx := int(v.localX)
	ly := int(v.localY)

	var dstX1, dstY1, dstX2, dstY2 int
	var coordFlipH, coordFlipV bool

	if zp == 0 {
		// Two-coordinate mode: A=upper-left, C=lower-right
		ax := int(cmd.xa) + lx
		ay := int(cmd.ya) + ly
		cx := int(cmd.xc) + lx
		cy := int(cmd.yc) + ly

		coordFlipH = cx < ax
		coordFlipV = cy < ay

		if ax <= cx {
			dstX1, dstX2 = ax, cx
		} else {
			dstX1, dstX2 = cx, ax
		}
		if ay <= cy {
			dstY1, dstY2 = ay, cy
		} else {
			dstY1, dstY2 = cy, ay
		}
	} else {
		ax := int(cmd.xa) + lx
		ay := int(cmd.ya) + ly
		dispW := int(cmd.xb)
		dispH := int(cmd.yb)

		if dispW < 0 || dispH < 0 {
			return 0, true
		}

		zpH := zp & 0x3
		zpV := (zp >> 2) & 0x3
		if zpH == 0 || zpV == 0 {
			return 0, true
		}

		switch zpH {
		case 1:
			dstX1, dstX2 = ax, ax+dispW
		case 2:
			dstX1, dstX2 = ax-dispW/2, ax+(dispW+1)/2
		case 3:
			dstX1, dstX2 = ax-dispW, ax
		}

		switch zpV {
		case 1:
			dstY1, dstY2 = ay, ay+dispH
		case 2:
			dstY1, dstY2 = ay-dispH/2, ay+(dispH+1)/2
		case 3:
			dstY1, dstY2 = ay-dispH, ay
		}
	}

	s.effFlipH = s.flipH != coordFlipH
	s.effFlipV = s.flipV != coordFlipV
	s.destW = dstX2 - dstX1 + 1
	s.destH = dstY2 - dstY1 + 1
	s.dstX1 = dstX1
	s.dstY1 = dstY1
	if s.destW <= 0 || s.destH <= 0 {
		return 0, true
	}

	// HSS subsamples the source by parity (per FBCR.EOS) only on
	// axes being shrunk; at 1:1 or enlarged, sampling is unmodified.
	// The end-code-disable side-effect rides on the X-shrink case.
	s.hssShrinkX = hss && s.destW < s.charW
	s.hssShrinkY = hss && s.destH < s.charH
	s.hssOddParity = v.fbcr&0x10 != 0
	s.hssEcdOff = s.hssShrinkX

	s.clipX, s.clipY = v.clipBounds()

	if cmd.pmod&0x0800 == 0 && preClipReject(dstX1, dstY1, dstX2, dstY2, s.clipX, s.clipY) {
		return 0, true
	}

	if s.cc >= 4 {
		s.gt = v.readGouraudTable(cmd.grda)
	}

	s.outerIdx = 0 // dy
	s.innerIdx = 0 // dx
	s.endCodeCount = 0
	s.prevSrcX = -1

	return v.runScaledSprite(budget)
}

func (v *VDP1) resumeScaledSprite(budget int32) (consumed int32, done bool) {
	return v.runScaledSprite(budget)
}

// runScaledSprite walks (dy, dx) in chunks of pixelsPerYieldChunk
// and yields when the budget is reached.
func (v *VDP1) runScaledSprite(budget int32) (consumed int32, done bool) {
	s := &v.spriteResume
	cycles := int32(0)

	for s.outerIdx < s.destH {
		// Midpoint sampling matches hardware: each dest pixel reads from
		// the source position at its center, not its left edge.
		srcY := ((2*s.outerIdx + 1) * s.charH) / (2 * s.destH)
		if s.effFlipV {
			srcY = s.charH - 1 - srcY
		}
		if s.hssShrinkY {
			if s.hssOddParity {
				srcY |= 1
			} else {
				srcY &^= 1
			}
		}

		fbY := s.dstY1 + s.outerIdx
		if fbY < 0 || fbY > s.clipY {
			s.outerIdx++
			s.innerIdx = 0
			s.endCodeCount = 0
			s.prevSrcX = -1
			cycles += 5
			if cycles >= budget && s.outerIdx < s.destH {
				v.cmdPhase = phaseScaledSprite
				return cycles, false
			}
			continue
		}

		for s.innerIdx < s.destW {
			chunkEnd := s.innerIdx + pixelsPerYieldChunk
			if chunkEnd > s.destW {
				chunkEnd = s.destW
			}
			for s.innerIdx < chunkEnd {
				srcX := ((2*s.innerIdx + 1) * s.charW) / (2 * s.destW)
				if s.effFlipH {
					srcX = s.charW - 1 - srcX
				}
				if s.hssShrinkX {
					if s.hssOddParity {
						srcX |= 1
					} else {
						srcX &^= 1
					}
				}

				dot := v.readCharDot(s.charAddr, srcX, srcY, s.charW, s.colorMode)

				if !s.hssEcdOff && !s.ecdOff && v.isEndCode(dot, s.colorMode) {
					if srcX != s.prevSrcX {
						s.prevSrcX = srcX
						s.endCodeCount++
						if s.endCodeCount >= 2 {
							s.innerIdx = s.destW
							break
						}
					}
					s.innerIdx++
					cycles++
					continue
				}
				if !s.spdOn && dot == 0 {
					s.innerIdx++
					cycles++
					continue
				}

				fbX := s.dstX1 + s.innerIdx
				pixel := v.dotToPixel(dot, v.cmdSnapshot.colr, s.colorMode)
				var gouraud uint16
				if s.cc >= 4 {
					top := lerpGouraud(s.gt[0], s.gt[1], s.innerIdx, s.destW)
					bot := lerpGouraud(s.gt[3], s.gt[2], s.innerIdx, s.destW)
					gouraud = lerpGouraud(top, bot, s.outerIdx, s.destH)
				}
				v.writePixel(fbX, fbY, pixel, s.cc, gouraud, s.msbOn, s.mesh, s.userClip, s.clipX, s.clipY)

				s.innerIdx++
				cycles++
			}

			if cycles >= budget {
				v.cmdPhase = phaseScaledSprite
				return cycles, false
			}
		}

		s.outerIdx++
		s.innerIdx = 0
		s.endCodeCount = 0
		s.prevSrcX = -1
		if cycles >= budget && s.outerIdx < s.destH {
			v.cmdPhase = phaseScaledSprite
			return cycles, false
		}
	}

	v.cmdPhase = phaseIdle
	if s.cc >= 4 {
		cycles += 4
	}
	if s.colorMode == 1 {
		cycles += 16
	}
	return cycles, true
}
