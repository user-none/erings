// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// layerCCBit is packed into bit 27 of layerBuf/rbg0Buf entries to indicate
// per-pixel color calculation enable. Priority occupies bits 26:24 (3 bits).
const layerCCBit = 1 << 27

// gradationSpanSetup prepares the horizontal gradation blur for row y
// of a layer buffer. Formula per pixel:
// blurred[x] = pixel[x-2]*1/4 + pixel[x-1]*1/4 + pixel[x]*2/4
// Reads and writes only row y.
func gradationSpanSetup(buf []uint32, width, y int) func(x0, x1 int) {
	rowOff := y * width
	// The blur is in-place and pixel x reads the PRE-blur values of
	// x-1 and x-2: processing right-to-left within a span keeps the
	// in-span neighbors original, and the carries hold the original
	// values of the two pixels left of the span boundary (zero at the
	// row's left edge, matching the original x>=1/x>=2 guards as long
	// as spans are consumed left to right).
	var carry1, carry2 uint32
	return func(x0, x1 int) {
		var next1, next2 uint32
		if x1-1 >= 0 {
			next1 = buf[rowOff+x1-1]
		}
		if x1-2 >= 0 {
			next2 = buf[rowOff+x1-2]
		}
		for x := x1 - 1; x >= x0; x-- {
			cur := buf[rowOff+x]
			if cur == 0 {
				continue
			}
			curR := int(uint8(cur >> 16))
			curG := int(uint8(cur >> 8))
			curB := int(uint8(cur))
			pri := cur & 0xFF000000

			var l1, l2 uint32
			if x-1 >= x0 {
				l1 = buf[rowOff+x-1]
			} else {
				l1 = carry1
			}
			if x-2 >= x0 {
				l2 = buf[rowOff+x-2]
			} else if x-2 == x0-1 {
				l2 = carry1
			} else {
				l2 = carry2
			}

			var l1r, l1g, l1b, l2r, l2g, l2b int
			if l1 != 0 {
				l1r = int(uint8(l1 >> 16))
				l1g = int(uint8(l1 >> 8))
				l1b = int(uint8(l1))
			}
			if l2 != 0 {
				l2r = int(uint8(l2 >> 16))
				l2g = int(uint8(l2 >> 8))
				l2b = int(uint8(l2))
			}

			newR := (l2r + l1r + curR*2) / 4
			newG := (l2g + l1g + curG*2) / 4
			newB := (l2b + l1b + curB*2) / 4

			buf[rowOff+x] = pri | uint32(newR)<<16 | uint32(newG)<<8 | uint32(newB)
		}
		carry1, carry2 = next1, next2
	}
}

// compositeSpanSetup prepares framebuffer row y for span composition:
// back-screen fill first (the per-pixel composition reads the row back
// as its blend bottom), then the layer composition for pixels [x0,x1).
// y is a field-line index; the output row is frameOutRow(y). Registers
// come from the line snapshot; the back-screen, line-color, and
// line-window tables are read from VRAM at composition time. Layer
// buffers are consulted only for layers enabled this line, so stale
// rows in disabled layers' buffers are never read.
func (v *VDP2) compositeSpanSetup(y int) func(x0, x1 int) {
	width := v.frame.width
	ccmd, ccrtmd, exccen := v.frame.ccmd, v.frame.ccrtmd, v.frame.exccen
	layerIsRGB := v.frame.layerIsRGB
	ccctl := v.frame.regs[vdp2CCCTL]
	rbg0On, rbg1On := v.frame.rbg0On, v.frame.rbg1On
	nbgOn := v.frame.nbgOn
	r1on := v.frame.rbg1Active

	// Back screen color for this row. BKCLMD (BKTAU bit 15) selects one
	// color for the whole screen vs one color per line.
	bktau := v.frame.regs[vdp2BKTAU]
	bkAddr := ((uint32(bktau&0x07) << 16) | uint32(v.frame.regs[vdp2BKTAL])) * 2
	if bktau&0x8000 != 0 {
		bkAddr += uint32(v.lineTableY(y)) * 2
	}
	br, bg, bb := rgb555ToRGB(v.readVRAM16(bkAddr))
	fbRow := v.frameOutRow(y)

	// A candidate is one non-transparent layer pixel competing for the dot.
	// Layer IDs: 0-3=NBG0-3, 4=RBG0, 5=sprite.
	type candidate struct {
		pri       uint8
		tie       int // higher = wins on equal priority
		layerID   int
		r, g, b   uint8
		ccEnabled bool
		ccBits    uint8 // sprite CC register index (layerID==5 only)
	}

	return func(x0, x1 int) {
		fillOff := (fbRow*width + x0) * 4
		for x := x0; x < x1; x++ {
			v.framebuffer[fillOff] = br
			v.framebuffer[fillOff+1] = bg
			v.framebuffer[fillOff+2] = bb
			v.framebuffer[fillOff+3] = 0xFF
			fillOff += 4
		}

		for x := x0; x < x1; x++ {
			p := y*width + x
			fbP := fbRow*width + x

			var candidates [6]candidate
			ncand := 0

			// Sprite layer (layerID=5)
			shadowType := shadowNone
			spPixel, spValid := v.readVDP1Pixel(x, y)
			if spValid {
				stype := v.classifyShadow(spPixel)
				if stype != shadowNone {
					shadowType = stype
				} else {
					pri, spCCBits, colorMSB, sr, sg, sb := v.decodeSpritePixel(spPixel)
					// Per VDP2 manual Sec 11.1 Priority Function:
					// "When the value of the priority number is 0, it
					// is treated as transparent and not displayed."
					// Applies uniformly to NBG/RBG/sprite layers; the
					// NBG/RBG paths already enforce this. The sprite
					// path must too - games disable the sprite layer
					// globally by writing PRISA-D = 0.
					if pri != 0 {
						spCCEn := v.isSpritePixelCCEnabled(pri, colorMSB)
						candidates[ncand] = candidate{pri, 5, 5, sr, sg, sb, spCCEn, spCCBits}
						ncand++
					}
				}
			}

			// RBG0 layer (layerID=4). Tie-break rank 4 per spec Table 11.1
			// (Normal mode order: Sprite > RBG0 > NBG0 > NBG1 > NBG2 > NBG3).
			if rbg0On {
				rbgPx := v.rbg0Buf[p]
				if rbgPx != 0 && !v.isWindowMasked(x, y, 4) {
					pri := uint8((rbgPx >> 24) & 0x07)
					ccEn := rbgPx&layerCCBit != 0
					candidates[ncand] = candidate{pri, 4, 4, uint8(rbgPx >> 16), uint8(rbgPx >> 8), uint8(rbgPx), ccEn, 0}
					ncand++
				}
			}

			// NBG layers (RBG1 replaces NBG0 slot when active).
			// Tie-break ranks per spec Table 11.1 (Normal mode):
			// Sprite=5, RBG0=4, NBG0=3, NBG1=2, NBG2=1, NBG3=0.
			for s := 0; s < 4; s++ {
				var px uint32
				if r1on && s == 0 {
					if !rbg1On {
						continue
					}
					px = v.rbg1Buf[p]
				} else {
					if !nbgOn[s] {
						continue
					}
					px = v.layerBufs[s][p]
				}
				if px == 0 {
					continue
				}
				if v.isWindowMasked(x, y, s) {
					continue
				}
				pri := uint8((px >> 24) & 0x07)
				ccEn := px&layerCCBit != 0
				candidates[ncand] = candidate{pri, 3 - s, s, uint8(px >> 16), uint8(px >> 8), uint8(px), ccEn, 0}
				ncand++
			}

			if ncand == 0 {
				// Apply color offset to back screen if enabled (CLOFEN bit 5)
				clofen := v.frame.regs[vdp2CLOFEN]
				if clofen&(1<<5) != 0 {
					clofsl := v.frame.regs[vdp2CLOFSL]
					var offR, offG, offB int
					if clofsl&(1<<5) == 0 {
						offR = signExtend9(v.frame.regs[vdp2COAR])
						offG = signExtend9(v.frame.regs[vdp2COAG])
						offB = signExtend9(v.frame.regs[vdp2COAB])
					} else {
						offR = signExtend9(v.frame.regs[vdp2COBR])
						offG = signExtend9(v.frame.regs[vdp2COBG])
						offB = signExtend9(v.frame.regs[vdp2COBB])
					}
					off := fbP * 4
					br := int(v.framebuffer[off])
					bg := int(v.framebuffer[off+1])
					bb := int(v.framebuffer[off+2])
					v.framebuffer[off] = uint8(clampU8(br + offR))
					v.framebuffer[off+1] = uint8(clampU8(bg + offG))
					v.framebuffer[off+2] = uint8(clampU8(bb + offB))
				}

				// Apply shadow to back screen (SDCTL bit 5 = BKSDEN)
				switch shadowType {
				case shadowNormal, shadowMSBTransp:
					sdctl := v.frame.regs[vdp2SDCTL]
					if sdctl&(1<<5) != 0 {
						off := fbP * 4
						v.framebuffer[off] = v.framebuffer[off] / 2
						v.framebuffer[off+1] = v.framebuffer[off+1] / 2
						v.framebuffer[off+2] = v.framebuffer[off+2] / 2
					}
				}

				continue
			}

			// Find top two by priority (descending), then tiebreak (descending)
			topIdx := 0
			for i := 1; i < ncand; i++ {
				c := candidates[i]
				t := candidates[topIdx]
				if c.pri > t.pri || (c.pri == t.pri && c.tie > t.tie) {
					topIdx = i
				}
			}
			top := candidates[topIdx]

			// Find second best (excluding top)
			secIdx := -1
			for i := 0; i < ncand; i++ {
				if i == topIdx {
					continue
				}
				if secIdx < 0 {
					secIdx = i
					continue
				}
				c := candidates[i]
				s := candidates[secIdx]
				if c.pri > s.pri || (c.pri == s.pri && c.tie > s.tie) {
					secIdx = i
				}
			}

			// Find third best (excluding top and second)
			thirdIdx := -1
			for i := 0; i < ncand; i++ {
				if i == topIdx || i == secIdx {
					continue
				}
				if thirdIdx < 0 {
					thirdIdx = i
					continue
				}
				c := candidates[i]
				t := candidates[thirdIdx]
				if c.pri > t.pri || (c.pri == t.pri && c.tie > t.tie) {
					thirdIdx = i
				}
			}

			r, g, b := int(top.r), int(top.g), int(top.b)

			// Color calculation: check if top layer has CC enabled
			topCCEnabled := top.ccEnabled

			// Hi-res palette CC restriction: CRAM mode 1/2 disables palette CC
			if topCCEnabled && v.frame.hiRes && v.frame.cramMode >= 1 {
				if top.layerID != 5 {
					// Scroll layers always use palette format
					topCCEnabled = false
				} else if v.frame.regs[vdp2SPCTL]&0x10 == 0 {
					// Sprites in all-palette mode
					topCCEnabled = false
				}
			}

			if topCCEnabled && !v.isCCWindowActive(x, y) {
				// Determine second image: line color screen, layer candidate, or back
				// screen. One of these arms always runs, so a second image always
				// exists - at minimum the back screen, which is the bottom of the
				// priority stack (manual Sec 12.1, Figure 12.2).
				var secR, secG, secB uint8
				lncInserted := false
				secIsBackScreen := false

				// Line color screen insertion: replaces second image
				lnclen := v.frame.regs[vdp2LNCLEN]
				topLNCBit := uint(top.layerID)
				if top.layerID == 5 { // sprite
					topLNCBit = 5
				}
				if lnclen&(1<<topLNCBit) != 0 {
					lctau := v.frame.regs[vdp2LCTAU]
					lcAddr := ((uint32(lctau&0x07) << 16) | uint32(v.frame.regs[vdp2LCTAL])) * 2
					var lcOffset uint32
					if lctau&0x8000 != 0 { // LCCLMD=1: per-line; else single color
						lcOffset = uint32(v.lineTableY(y)) * 2
					}
					var lcData uint8
					if top.layerID == 4 {
						lcData = v.rbg0LCBuf[p]
					} else if top.layerID == 0 && r1on {
						lcData = v.rbg1LCBuf[p]
					}
					if lcData&0x80 != 0 {
						// Coefficient line color: upper 4 bits from table + 7 bits from coefficient
						lcTableVal := v.readVRAM16(lcAddr + lcOffset)
						upperBits := uint32((lcTableVal >> 7) & 0x0F)
						lowerBits := uint32(lcData & 0x7F)
						cramAddr := (upperBits << 7) | lowerBits
						secR, secG, secB = v.readCRAMColor(cramAddr)
					} else {
						// Per VDP2 manual Sec 7.1 / Figure 7.3 the line color
						// screen table holds an 11-bit CRAM address, not
						// an RGB555 color. readCRAMColor masks to the
						// active CRAM-mode width so passing the full 11
						// bits is safe across modes.
						lcTableVal := v.readVRAM16(lcAddr + lcOffset)
						cramAddr := uint32(lcTableVal & 0x07FF)
						secR, secG, secB = v.readCRAMColor(cramAddr)
					}
					lncInserted = true
				} else if secIdx >= 0 {
					sec := candidates[secIdx]
					secR, secG, secB = sec.r, sec.g, sec.b
				} else {
					// Back screen is the second image (lowest in priority stack)
					bkOff := fbP * 4
					secR = v.framebuffer[bkOff]
					secG = v.framebuffer[bkOff+1]
					secB = v.framebuffer[bkOff+2]
					secIsBackScreen = true
				}

				// Extended color calculation: blend second with third (and fourth).
				// Per Table 12.2: in CRAM mode 1 or 2, the blend ratio is 4:0:0
				// (no blend) when the 2nd image is palette format, regardless of
				// 2nd image CCEN. The blend only applies when the 2nd image is
				// RGB format. In CRAM mode 0 the blend follows CCEN as before.
				if exccen {
					// Second image's CC enable controls 2nd<->3rd blending
					var secLayerCCEN bool
					if secIsBackScreen {
						secLayerCCEN = false // back screen has no CC enable bit
					} else if lncInserted {
						// Line color inserted as second; check LCCCEN (bit 5)
						secLayerCCEN = ccctl&(1<<5) != 0
					} else if secIdx >= 0 {
						secLayerCCEN = candidates[secIdx].ccEnabled
					}

					// CRAM mode 1/2 format-dependent gating per Table 12.2.
					// No-LNCL: palette 2nd image -> ratio 4:0:0 (no blend).
					// LNCL: line color is the 2nd image (always palette); the
					// spec "3rd image" is the original 2nd (candidates[secIdx]).
					// If that is palette format -> ratio 4:0:0 (no blend).
					if v.frame.cramMode >= 1 && secIdx >= 0 {
						if !layerIsRGB[candidates[secIdx].layerID] {
							secLayerCCEN = false
						}
					}

					if secLayerCCEN {
						if !lncInserted {
							// No line color: ratio 2:2:0 (second + third) / 2
							if thirdIdx >= 0 {
								third := candidates[thirdIdx]
								secR = uint8((int(secR) + int(third.r)) / 2)
								secG = uint8((int(secG) + int(third.g)) / 2)
								secB = uint8((int(secB) + int(third.b)) / 2)
							}
						} else {
							// Line color inserted. Original second is at secIdx, original third at thirdIdx.
							// Third image's CC enable controls 3rd<->4th blending
							var thirdLayerCCEN bool
							if secIdx >= 0 {
								thirdLayerCCEN = candidates[secIdx].ccEnabled
							}

							// CRAM mode 1/2 + palette-format 4th image -> ratio
							// 2:2:0 (no 4th contribution) per Table 12.2.
							if v.frame.cramMode >= 1 && thirdIdx >= 0 {
								if !layerIsRGB[candidates[thirdIdx].layerID] {
									thirdLayerCCEN = false
								}
							}

							if !thirdLayerCCEN {
								// Ratio 2:2:0: (lineColor + original second) / 2
								if secIdx >= 0 {
									orig2nd := candidates[secIdx]
									secR = uint8((int(secR) + int(orig2nd.r)) / 2)
									secG = uint8((int(secG) + int(orig2nd.g)) / 2)
									secB = uint8((int(secB) + int(orig2nd.b)) / 2)
								}
							} else {
								// Mode 1/2 with RGB 4th image: ratio 2:1:1
								// (lineColor/2 + original second/4 + original third/4).
								// Per Table 12.2 the 4th image is never added in
								// CRAM mode 0 (mode 0 caps at 2:1:0), so the
								// original-third term is dropped there.
								var o2r, o2g, o2b, o3r, o3g, o3b int
								if secIdx >= 0 {
									orig2nd := candidates[secIdx]
									o2r = int(orig2nd.r)
									o2g = int(orig2nd.g)
									o2b = int(orig2nd.b)
								}
								if v.frame.cramMode >= 1 && thirdIdx >= 0 {
									orig3rd := candidates[thirdIdx]
									o3r = int(orig3rd.r)
									o3g = int(orig3rd.g)
									o3b = int(orig3rd.b)
								}
								secR = uint8(int(secR)/2 + o2r/4 + o3r/4)
								secG = uint8(int(secG)/2 + o2g/4 + o3g/4)
								secB = uint8(int(secB)/2 + o2b/4 + o3b/4)
							}
						}
					}
				}

				if ccmd {
					r = int(clampU8(r + int(secR)))
					g = int(clampU8(g + int(secG)))
					b = int(clampU8(b + int(secB)))
				} else {
					var ratio int
					if !ccrtmd {
						if top.layerID == 5 {
							ratio = v.getSpriteCCRatio(top.ccBits)
						} else {
							ratio = v.getLayerCCRatio(top.layerID)
						}
					} else if lncInserted {
						// Line color is the second image; use line color ratio
						ratio = int(v.frame.regs[vdp2CCRLB]) & 0x1F
					} else if secIsBackScreen {
						// Back screen is second image; use BKCCRT (bits 12:8)
						ratio = int(v.frame.regs[vdp2CCRLB]>>8) & 0x1F
					} else {
						// Second image is a scroll/sprite candidate (secIdx>=0
						// is guaranteed here: the second-image selection above
						// always set line color, a candidate, or the back screen).
						if candidates[secIdx].layerID == 5 {
							ratio = v.getSpriteCCRatio(candidates[secIdx].ccBits)
						} else {
							ratio = v.getLayerCCRatio(candidates[secIdx].layerID)
						}
					}
					r = (r*(31-ratio) + int(secR)*(ratio+1)) / 32
					g = (g*(31-ratio) + int(secG)*(ratio+1)) / 32
					b = (b*(31-ratio) + int(secB)*(ratio+1)) / 32
				}
			}

			// Color offset: apply signed RGB offset per layer
			// CLOFEN bits: 0=N0, 1=N1, 2=N2, 3=N3, 4=R0, 5=Back, 6=Sprite
			clofen := v.frame.regs[vdp2CLOFEN]
			var layerBit uint
			if top.layerID == 5 {
				layerBit = 6 // Sprite is bit 6 in CLOFEN/CLOFSL
			} else {
				layerBit = uint(top.layerID) // 0-4 map directly
			}
			if clofen&(1<<layerBit) != 0 {
				clofsl := v.frame.regs[vdp2CLOFSL]
				var offR, offG, offB int
				if clofsl&(1<<layerBit) == 0 {
					offR = signExtend9(v.frame.regs[vdp2COAR])
					offG = signExtend9(v.frame.regs[vdp2COAG])
					offB = signExtend9(v.frame.regs[vdp2COAB])
				} else {
					offR = signExtend9(v.frame.regs[vdp2COBR])
					offG = signExtend9(v.frame.regs[vdp2COBG])
					offB = signExtend9(v.frame.regs[vdp2COBB])
				}
				r = int(clampU8(r + offR))
				g = int(clampU8(g + offG))
				b = int(clampU8(b + offB))
			}

			// Shadow: darken based on shadow type
			switch shadowType {
			case shadowNormal, shadowMSBTransp:
				// Normal and MSB transparent shadow darken scroll/back per SDCTL
				if top.layerID != 5 {
					sdctl := v.frame.regs[vdp2SDCTL]
					if top.layerID <= 4 && sdctl&(1<<uint(top.layerID)) != 0 {
						r = r / 2
						g = g / 2
						b = b / 2
					}
				}
			case shadowMSBSprite:
				// MSB sprite shadow darkens the sprite below
				if top.layerID == 5 {
					r = r / 2
					g = g / 2
					b = b / 2
				}
			}

			off := fbP * 4
			v.framebuffer[off] = uint8(r)
			v.framebuffer[off+1] = uint8(g)
			v.framebuffer[off+2] = uint8(b)
			v.framebuffer[off+3] = 0xFF
		}

		if v.frame.fieldBootstrap {
			src := (fbRow*width + x0) * 4
			dst := ((fbRow^1)*width + x0) * 4
			n := (x1 - x0) * 4
			copy(v.framebuffer[dst:dst+n], v.framebuffer[src:src+n])
		}
	}
}

// getLayerCCRatio returns the 5-bit color calculation ratio for a layer.
// layerID: 0-3=NBG0-3, 4=RBG0. Sprites (layerID 5) take their ratio from
// getSpriteCCRatio instead, so this is never called with 5.
func (v *VDP2) getLayerCCRatio(layerID int) int {
	switch layerID {
	case 0:
		return int(v.frame.regs[vdp2CCRNA]) & 0x1F
	case 1:
		return int(v.frame.regs[vdp2CCRNA]>>8) & 0x1F
	case 2:
		return int(v.frame.regs[vdp2CCRNB]) & 0x1F
	case 3:
		return int(v.frame.regs[vdp2CCRNB]>>8) & 0x1F
	case 4: // RBG0
		return int(v.frame.regs[vdp2CCRR]) & 0x1F
	}
	return 0
}

// reductionDisablesCompanion returns true when NBG0's reduction (ZMHF/ZMQT)
// + color count (CHCN value) suppresses the companion NBG2 layer per PDF
// Sec 5.2 Table 5.2. The same rule with NBG1 inputs governs NBG3. zmBits is
// (ZMQT << 1) | ZMHF for the corresponding scroll. cm is the 3-bit CHCN
// value (0=16-color, 1=256-color, ...).
//
// Disable conditions (PDF Table 5.2):
//
//	ZMQT=1 (any 1/4 reduction)              -> disable
//	ZMHF=1 with cm >= 1 (256+ colors)       -> disable
//	Otherwise                               -> enable
func reductionDisablesCompanion(zmBits, cm uint8) bool {
	zmqt := zmBits & 0x02
	zmhf := zmBits & 0x01
	if zmqt != 0 {
		return true
	}
	if zmhf != 0 && cm >= 1 {
		return true
	}
	return false
}
