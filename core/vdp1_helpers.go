// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

func intAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// floorDiv64 performs integer division that rounds toward negative infinity
// instead of Go's default toward-zero truncation.
func floorDiv64(a, b int64) int64 {
	if (a^b) < 0 && a%b != 0 {
		return a/b - 1
	}
	return a / b
}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clampChannel clamps a signed channel value to 0..31.
func clampChannel(v int) int {
	if v < 0 {
		return 0
	}
	if v > 31 {
		return 31
	}
	return v
}

// preClipReject returns true if a bounding box is entirely outside the
// system clip region (0,0)-(clipX,clipY).
func preClipReject(minX, minY, maxX, maxY, clipX, clipY int) bool {
	return maxX < 0 || minX > clipX || maxY < 0 || minY > clipY
}

// clipSegParam intersects the parametric segment (x0,y0)->(x1,y1) with
// the rectangle [0,clipX]x[0,clipY] (Liang-Barsky). Returns the
// visible parameter range [t0,t1] in [0,1] and ok=false if the segment
// is wholly outside. Used to skip connecting-line pixels that fall
// entirely outside the drawing area (VDP1 manual pre-clipping: "for
// lines completely separated from the drawing area ... drawing not be
// started"); output is unchanged because writePixel already discarded
// those pixels.
func clipSegParam(x0, y0, x1, y1, clipX, clipY int) (t0, t1 float64, ok bool) {
	t0, t1 = 0, 1
	dx := float64(x1 - x0)
	dy := float64(y1 - y0)
	p := [4]float64{-dx, dx, -dy, dy}
	q := [4]float64{float64(x0), float64(clipX - x0), float64(y0), float64(clipY - y0)}
	for i := 0; i < 4; i++ {
		if p[i] == 0 {
			if q[i] < 0 {
				return 0, 0, false
			}
			continue
		}
		r := q[i] / p[i]
		if p[i] < 0 {
			if r > t1 {
				return 0, 0, false
			}
			if r > t0 {
				t0 = r
			}
		} else {
			if r < t0 {
				return 0, 0, false
			}
			if r < t1 {
				t1 = r
			}
		}
	}
	return t0, t1, true
}

// clipBounds returns the effective system clip bounds clamped to the
// frame buffer dimensions for the current TVM mode. Under doubled-Y
// DIE mode (TVM=1 + DIE=1) the Y range doubles; writePixel then halves
// surviving Y values to the physical FB row.
func (v *VDP1) clipBounds() (int, int) {
	clipX := int(v.sysClipX)
	clipY := int(v.sysClipY)
	maxX := v.fbWidth() - 1
	maxY := v.fbHeightLogical() - 1
	if clipX > maxX {
		clipX = maxX
	}
	if clipY > maxY {
		clipY = maxY
	}
	return clipX, clipY
}

// readGouraudTable reads 4 Gouraud correction entries from VRAM.
// Returns raw packed values (bits 14:10=B, 9:5=G, 4:0=R).
func (v *VDP1) readGouraudTable(grda uint16) [4]uint16 {
	addr := uint32(grda) * 8
	var t [4]uint16
	for i := 0; i < 4; i++ {
		t[i] = v.readVDP1VRAM16(addr + uint32(i)*2)
	}
	return t
}

// applyColorCalc applies color calculation mode to a source pixel.
// cc = CMDPMOD bits 2:0. gouraud = interpolated correction (packed RGB,
// only used when cc >= 4). fbX, physY = framebuffer position for modes
// that read the existing framebuffer pixel (shadow, half-transparent).
// fb = the destination framebuffer slice (drawFB or, under DIE=1 with
// DIL=1, displayFB). Caller is responsible for resolving fb and physY
// per the current DIE/DIL state.
func (v *VDP1) applyColorCalc(cc uint16, pixel uint16, gouraud uint16, fbX, physY int, fb []byte) uint16 {
	if cc == 0 {
		return pixel
	}

	// Apply Gouraud correction to source pixel (modes 4-7)
	if cc >= 4 {
		sr := int(pixel&0x1F) + int(gouraud&0x1F) - 0x10
		sg := int((pixel>>5)&0x1F) + int((gouraud>>5)&0x1F) - 0x10
		sb := int((pixel>>10)&0x1F) + int((gouraud>>10)&0x1F) - 0x10
		pixel = (pixel & 0x8000) | uint16(clampChannel(sb))<<10 | uint16(clampChannel(sg))<<5 | uint16(clampChannel(sr))
	}

	switch cc & 3 {
	case 0: // Replace (or Gouraud + replace)
		return pixel
	case 1: // Shadow (CC=1/CC=5) - handled in writePixel early return, unreachable
		return pixel
	case 2: // Half luminance (or Gouraud + half lum)
		sr := (pixel & 0x1F) >> 1
		sg := ((pixel >> 5) & 0x1F) >> 1
		sb := ((pixel >> 10) & 0x1F) >> 1
		return (pixel & 0x8000) | sb<<10 | sg<<5 | sr
	case 3: // Half transparent (or Gouraud + half transp)
		off := (physY*v.fbWidth() + fbX) * 2
		dst := uint16(fb[off])<<8 | uint16(fb[off+1])
		if dst&0x8000 == 0 {
			return pixel // replace when MSB not set
		}
		sr := ((pixel & 0x1F) + (dst & 0x1F)) >> 1
		sg := (((pixel >> 5) & 0x1F) + ((dst >> 5) & 0x1F)) >> 1
		sb := (((pixel >> 10) & 0x1F) + ((dst >> 10) & 0x1F)) >> 1
		return (pixel & 0x8000) | sb<<10 | sg<<5 | sr
	}
	return pixel
}

// vdp1Command holds a parsed VDP1 command table entry.
type vdp1Command struct {
	ctrl uint16
	link uint16
	pmod uint16
	colr uint16
	srca uint16
	size uint16
	xa   int16
	ya   int16
	xb   int16
	yb   int16
	xc   int16
	yc   int16
	xd   int16
	yd   int16
	grda uint16
}

// readVDP1VRAM16 reads a big-endian 16-bit value from VDP1 VRAM.
func (v *VDP1) readVDP1VRAM16(addr uint32) uint16 {
	addr &= vdp1VRAMSize - 1
	hi := v.vram[addr]
	lo := v.vram[(addr+1)&(vdp1VRAMSize-1)]
	return uint16(hi)<<8 | uint16(lo)
}

// readCommand reads a command table entry from VRAM at the given byte address.
func (v *VDP1) readCommand(addr uint32) vdp1Command {
	return vdp1Command{
		ctrl: v.readVDP1VRAM16(addr + 0x00),
		link: v.readVDP1VRAM16(addr + 0x02),
		pmod: v.readVDP1VRAM16(addr + 0x04),
		colr: v.readVDP1VRAM16(addr + 0x06),
		srca: v.readVDP1VRAM16(addr + 0x08),
		size: v.readVDP1VRAM16(addr + 0x0A),
		xa:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x0C)),
		ya:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x0E)),
		xb:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x10)),
		yb:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x12)),
		xc:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x14)),
		yc:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x16)),
		xd:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x18)),
		yd:   signExtendCoord13(v.readVDP1VRAM16(addr + 0x1A)),
		grda: v.readVDP1VRAM16(addr + 0x1C),
	}
}

// signExtendCoord13 decodes a CMDXA-CMDYD coordinate field. The
// hardware-effective field is 13 bits (bits 12..0) with the sign at
// bit 12; bits 15..13 are reserved code-extension and ignored. This
// matches games that submit coordinates outside the 11-bit range
// quoted by the manual - without bit 11/12 included, large positive
// values get misread as negatives and degenerate quads spanning the
// visible area get rasterized instead of pre-clip-rejected.
func signExtendCoord13(v uint16) int16 {
	v &= 0x1FFF
	if v&0x1000 != 0 {
		v |= 0xE000
	}
	return int16(v)
}

// writePixel writes a pixel to the draw framebuffer, applying color
// calculation and MSB on processing. This is the common pixel output
// path for all drawing commands.
//
// Under DIE=1 (double-density interlace draw mode), the game writes Y
// values in pre-halved coordinates (per VDP1 manual Sec 4.3 "the actual
// coordinate value should be set to one half"). DIL routes pixels to
// the parity-pinned framebuffer: DIL=0 -> drawFB (even-parity field),
// DIL=1 -> displayFB (odd-parity field). The Y is used as-is for the
// physical FB row; VDP2 doubles each FB row to two displayed rows via
// per-field interleaved scanout.
func (v *VDP1) writePixel(fbX, fbY int, pixel uint16, cc uint16, gouraud uint16, msbOn, mesh bool, userClip uint16, clipX, clipY int) {
	if fbX < 0 || fbX > clipX || fbY < 0 || fbY > clipY {
		return
	}
	if userClip == 2 {
		// Inside: draw only within user clip rect (boundary excluded)
		if fbX <= int(v.userClipX1) || fbX >= int(v.userClipX2) ||
			fbY <= int(v.userClipY1) || fbY >= int(v.userClipY2) {
			return
		}
	} else if userClip == 3 {
		// Outside: draw only outside user clip rect (boundary included)
		if fbX >= int(v.userClipX1) && fbX <= int(v.userClipX2) &&
			fbY >= int(v.userClipY1) && fbY <= int(v.userClipY2) {
			return
		}
	}
	if mesh && (fbX^fbY)&1 != 0 {
		return
	}

	fb := v.drawFB
	if v.dieDoubled() && v.dilOdd() {
		fb = v.displayFB
	}

	// Doubled-Y DIE mode (game wrote Y in 0..2*fbHeight-1): apply DIL
	// parity filter and halve Y to fit the physical FB row count. In
	// non-doubled mode (DIE=0, or DIE=1 with single-density Y range)
	// fbY is used as-is.
	physY := fbY
	if v.dieDoubled() {
		if (fbY&1 == 1) != v.dilOdd() {
			return // DIL gates this scanline parity
		}
		physY = fbY >> 1
	}

	if v.is8bpp() {
		cc = 0
		msbOn = false
	}
	if msbOn {
		cc = 0 // MON=1 requires replace mode; other CC modes are undefined
	}
	if cc&3 == 1 { // Shadow (CC=1 or CC=5); Gouraud has no effect since shadow only modifies destination
		// Shadow: only modify destination if its MSB is set
		off := (physY*v.fbWidth() + fbX) * 2
		dst := uint16(fb[off])<<8 | uint16(fb[off+1])
		if dst&0x8000 == 0 {
			return // no-op
		}
		dr := (dst & 0x1F) >> 1
		dg := ((dst >> 5) & 0x1F) >> 1
		db := ((dst >> 10) & 0x1F) >> 1
		pixel = (dst & 0x8000) | db<<10 | dg<<5 | dr
		fb[off] = uint8(pixel >> 8)
		fb[off+1] = uint8(pixel)
		return
	}
	if cc != 0 {
		pixel = v.applyColorCalc(cc, pixel, gouraud, fbX, physY, fb)
	}
	if msbOn {
		pixel |= 0x8000
	}
	if v.is8bpp() {
		offset := physY*v.fbWidth() + fbX
		fb[offset] = uint8(pixel)
	} else {
		offset := (physY*v.fbWidth() + fbX) * 2
		fb[offset] = uint8(pixel >> 8)
		fb[offset+1] = uint8(pixel)
	}
}

// readCharDot reads one pixel from character data in VRAM.
func (v *VDP1) readCharDot(charAddr uint32, x, y, width int, colorMode uint16) uint16 {
	switch colorMode {
	case 0, 1: // 4bpp
		byteAddr := charAddr + uint32(y*(width/2)+x/2)
		b := v.vram[byteAddr&(vdp1VRAMSize-1)]
		if x&1 == 0 {
			return uint16(b >> 4)
		}
		return uint16(b & 0x0F)
	case 2, 3, 4: // 8bpp
		byteAddr := charAddr + uint32(y*width+x)
		return uint16(v.vram[byteAddr&(vdp1VRAMSize-1)])
	case 5: // 16bpp
		pixAddr := charAddr + uint32((y*width+x)*2)
		return v.readVDP1VRAM16(pixAddr)
	default:
		return 0
	}
}

// isEndCode returns true if the dot value is an end code for the color mode.
func (v *VDP1) isEndCode(dot uint16, colorMode uint16) bool {
	switch colorMode {
	case 0, 1:
		return dot == 0xF
	case 2, 3, 4:
		return dot == 0xFF
	case 5:
		return dot == 0x7FFF
	default:
		return false
	}
}

// dotToPixel converts a dot value to a frame buffer pixel using CMDCOLR.
func (v *VDP1) dotToPixel(dot, cmdcolr uint16, colorMode uint16) uint16 {
	switch colorMode {
	case 0: // 16-color bank
		return (cmdcolr & 0xFFF0) | (dot & 0x000F)
	case 1: // 16-color CLUT
		clutAddr := uint32(cmdcolr) * 8
		return v.readVDP1VRAM16(clutAddr + uint32(dot)*2)
	case 2: // 64-color bank
		return (cmdcolr & 0xFFC0) | (dot & 0x003F)
	case 3: // 128-color bank
		return (cmdcolr & 0xFF80) | (dot & 0x007F)
	case 4: // 256-color bank
		return (cmdcolr & 0xFF00) | (dot & 0x00FF)
	case 5: // 32768-color RGB
		return dot
	default:
		return 0
	}
}

// bresenhamLine draws a non-textured Bresenham line between two screen-space
// points with color calculation support. Both endpoints are inclusive.
func (v *VDP1) bresenhamLine(x1, y1, x2, y2 int, color uint16, cc uint16,
	gStart, gEnd uint16, msbOn, mesh bool, userClip uint16, clipX, clipY int) {

	dx := x2 - x1
	dy := y2 - y1
	sx := 1
	if dx < 0 {
		sx = -1
		dx = -dx
	}
	sy := 1
	if dy < 0 {
		sy = -1
		dy = -dy
	}

	lineLen := dx
	if dy > lineLen {
		lineLen = dy
	}

	if dx >= dy {
		err := -dx
		for i := 0; i <= dx; i++ {
			var gouraud uint16
			if cc >= 4 {
				gouraud = lerpGouraud(gStart, gEnd, i, lineLen+1)
			}
			v.writePixel(x1, y1, color, cc, gouraud, msbOn, mesh, userClip, clipX, clipY)
			x1 += sx
			err += 2 * dy
			if err >= 0 {
				y1 += sy
				err -= 2 * dx
			}
		}
	} else {
		err := -dy
		for i := 0; i <= dy; i++ {
			var gouraud uint16
			if cc >= 4 {
				gouraud = lerpGouraud(gStart, gEnd, i, lineLen+1)
			}
			v.writePixel(x1, y1, color, cc, gouraud, msbOn, mesh, userClip, clipX, clipY)
			y1 += sy
			err += 2 * dx
			if err >= 0 {
				x1 += sx
				err -= 2 * dy
			}
		}
	}
}

// ddaEdge walks from (x1,y1) to (x2,y2) over exactly N steps using
// fixed-point arithmetic. Used for polygon and distorted sprite
// rasterization as a replacement for Bresenham-based edge stepping.
type ddaEdge struct {
	x, y   int64 // 16.16 fixed-point current position
	dx, dy int64 // 16.16 fixed-point delta per step
}

func initDDAEdge(x1, y1, x2, y2, steps int) ddaEdge {
	e := ddaEdge{
		x: int64(x1) << 16,
		y: int64(y1) << 16,
	}
	if steps > 0 {
		e.dx = floorDiv64(int64(x2-x1)<<16, int64(steps))
		e.dy = floorDiv64(int64(y2-y1)<<16, int64(steps))
	}
	return e
}

func (e *ddaEdge) step() {
	e.x += e.dx
	e.y += e.dy
}

func (e *ddaEdge) intX() int { return int(e.x >> 16) }
func (e *ddaEdge) intY() int { return int(e.y >> 16) }
