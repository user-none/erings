// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// startNormalSprite runs setup once, then enters the chunked
// rasterizer. On yield, cmdPhase is set to phaseNormalSprite and
// state is preserved on v.spriteResume.
func (v *VDP1) startNormalSprite(cmd *vdp1Command, budget int32) (consumed int32, done bool) {
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
	s.drawX = int(cmd.xa) + int(v.localX)
	s.drawY = int(cmd.ya) + int(v.localY)
	s.isScaled = false

	if s.charW == 0 || s.charH == 0 {
		return 0, true
	}

	s.clipX, s.clipY = v.clipBounds()

	if cmd.pmod&0x0800 == 0 &&
		preClipReject(s.drawX, s.drawY, s.drawX+s.charW-1, s.drawY+s.charH-1, s.clipX, s.clipY) {
		return 0, true
	}

	if s.cc >= 4 {
		s.gt = v.readGouraudTable(cmd.grda)
	}

	s.outerIdx = 0
	s.innerIdx = 0
	s.endCodeCount = 0

	return v.runNormalSprite(budget)
}

// resumeNormalSprite re-enters the chunked rasterizer with state
// already populated on v.spriteResume.
func (v *VDP1) resumeNormalSprite(budget int32) (consumed int32, done bool) {
	return v.runNormalSprite(budget)
}

// runNormalSprite walks the (srcY, srcX) iterator in chunks of
// pixelsPerYieldChunk and yields when the per-call cycle budget is
// reached. Pixel output is identical to the pre-refactor atomic
// rasterizer; only timing differs.
func (v *VDP1) runNormalSprite(budget int32) (consumed int32, done bool) {
	s := &v.spriteResume
	cycles := int32(0)

	for s.outerIdx < s.charH {
		for s.innerIdx < s.charW {
			chunkEnd := s.innerIdx + pixelsPerYieldChunk
			if chunkEnd > s.charW {
				chunkEnd = s.charW
			}
			for s.innerIdx < chunkEnd {
				readX := s.innerIdx
				readY := s.outerIdx
				if s.flipH {
					readX = s.charW - 1 - s.innerIdx
				}
				if s.flipV {
					readY = s.charH - 1 - s.outerIdx
				}
				dot := v.readCharDot(s.charAddr, readX, readY, s.charW, s.colorMode)

				if !s.ecdOff && v.isEndCode(dot, s.colorMode) {
					s.endCodeCount++
					if s.endCodeCount >= 2 {
						// End-code break ends the row early.
						s.innerIdx = s.charW
						break
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

				fbX := s.drawX + s.innerIdx
				fbY := s.drawY + s.outerIdx
				pixel := v.dotToPixel(dot, v.cmdSnapshot.colr, s.colorMode)
				var gouraud uint16
				if s.cc >= 4 {
					top := lerpGouraud(s.gt[0], s.gt[1], s.innerIdx, s.charW)
					bot := lerpGouraud(s.gt[3], s.gt[2], s.innerIdx, s.charW)
					gouraud = lerpGouraud(top, bot, s.outerIdx, s.charH)
				}
				v.writePixel(fbX, fbY, pixel, s.cc, gouraud, s.msbOn, s.mesh, s.userClip, s.clipX, s.clipY)

				s.innerIdx++
				cycles++
			}

			if cycles >= budget {
				v.cmdPhase = phaseNormalSprite
				return cycles, false
			}
		}
		s.outerIdx++
		s.innerIdx = 0
		s.endCodeCount = 0
		if cycles >= budget && s.outerIdx < s.charH {
			v.cmdPhase = phaseNormalSprite
			return cycles, false
		}
	}

	// Completion adders match the existing post-loop accounting.
	v.cmdPhase = phaseIdle
	if s.cc >= 4 {
		cycles += 4
	}
	if s.colorMode == 1 {
		cycles += 16
	}
	return cycles, true
}
