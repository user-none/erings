// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// vdp1FBView describes the VDP1 display framebuffer a line samples:
// pixel data, format, and dimensions. A nil data slice means no
// sprite layer.
type vdp1FBView struct {
	data   []byte
	is8bpp bool
	width  int
	height int
}

// vdp2Frame holds the working render state.
//
// Frame-scoped fields - geometry, field parity, DISP, fieldBootstrap,
// and the RBG configuration and rotation state - are sampled by
// BeginFrame and stay fixed for the frame: every row of one frame must
// share the framebuffer layout, the field cannot change mid-frame, and
// the rotation parameter tables are read from VRAM once per frame
// (RPRCTL selects per-line re-reads of individual fields).
//
// Everything else refreshes per line: BeginLine re-snapshots the
// register file and re-decodes the register-derived state when
// registers were written, so a register write takes effect on the
// following line. VRAM, CRAM tables, and the VDP1 framebuffer are
// read live as spans render.
type vdp2Frame struct {
	disp    bool
	width   int   // active pixels per line at frame start
	height  int   // active field lines; layer buffers indexed y*width
	effIntl uint8 // effectiveInterlace() at frame start
	field   int   // fieldBit() at frame start
	hiRes   bool  // hi-res horizontal mode at frame start

	// Snapshot of the register file, refreshed by BeginFrame and by
	// every BeginLine. Register reads in the decode and render paths
	// go through this copy, so a row's register-derived state is
	// consistent within the row.
	regs     [vdp2RegCount]uint16
	cramMode uint8 // RAMCTL CRAM mode bits

	// BGON bit 5 at frame start: RBG1 occupies the NBG0 slot and all
	// NBG screens are suppressed. The composite span uses this to pick
	// rbg1Buf vs layerBufs[0], matching what was rendered.
	rbg1Active bool

	nbgOn [4]bool
	nbg   [4]nbgConfig

	rbg0On bool
	rbg0   rbgConfig
	rbg0F  rbgFrame

	rbg1On bool
	rbg1   rbgConfig
	rbg1F  rbgFrame

	// fieldBootstrap: this is the first frame after a geometry change
	// under LSMD=3; the composite span copies each produced row to the
	// sibling field row because the other field has no content in the
	// new mode yet.
	fieldBootstrap bool

	// Composite setup: color calculation mode bits (CCCTL), per-layer
	// RGB-format flags for extended color calculation, and the
	// gradation target buffer (nil = gradation off). exccen has the
	// hi-res and BOKEN overrides already applied.
	ccmd       bool
	ccrtmd     bool
	exccen     bool
	layerIsRGB [6]bool
	gradBuf    []uint32
}

func clampU8(v int) uint8 {
	if v > 255 {
		return 255
	}
	if v < 0 {
		return 0
	}
	return uint8(v)
}

// planePages decodes a 2-bit PLSZ plane-size field into the plane
// dimensions in pages: 0=1x1, 1=2x1, 3=2x2. Value 2 is prohibited and
// treated as 1x1.
func planePages(bits uint16) (h, vPages int) {
	switch bits & 0x03 {
	case 1:
		return 2, 1
	case 3:
		return 2, 2
	default:
		return 1, 1
	}
}

// unpackMapRegs expands 8 consecutive map-register words starting at
// register index base into 16 plane bytes: low byte then high byte of
// each word, matching the A..P plane order of the rotation map.
func (v *VDP2) unpackMapRegs(base int) [16]uint8 {
	var regs [16]uint8
	for i := 0; i < 8; i++ {
		reg := v.frame.regs[base+i]
		regs[i*2] = uint8(reg)
		regs[i*2+1] = uint8(reg >> 8)
	}
	return regs
}

// decodeRBGConfig reads RBG0 configuration from registers.
func (v *VDP2) decodeRBGConfig() rbgConfig {
	var cfg rbgConfig
	bgon := v.frame.regs[vdp2BGON]
	cfg.enabled = bgon&(1<<4) != 0
	cfg.transpOff = bgon&(1<<12) != 0

	chctlb := v.frame.regs[vdp2CHCTLB]
	cfg.charSize1x1 = chctlb&0x100 == 0
	cfg.colorMode = uint8((chctlb >> 12) & 0x07)
	cfg.bitmapMode = chctlb&0x0200 != 0
	bmsz := (chctlb >> 10) & 0x01
	// Rotation scroll bitmap: width is always 512, height is 256 or 512
	cfg.bmpWidth = 512
	cfg.bmpHeight = 256
	if bmsz&0x01 != 0 {
		cfg.bmpHeight = 512
	}

	bmpnb := v.frame.regs[vdp2BMPNB]
	// BMP6-4 at BMPNB bits 2:0 per PDF Sec 4.10.
	cfg.bmpPalette = uint8(bmpnb&0x07) << 4
	cfg.bmpSpecialPri = bmpnb&(1<<5) != 0
	cfg.bmpSpecialCC = bmpnb&(1<<4) != 0

	cfg.pncnReg = v.frame.regs[vdp2PNCR]
	cfg.pnWord1 = cfg.pncnReg&0x8000 != 0
	cfg.auxMode1 = cfg.pncnReg&0x4000 != 0

	cfg.mapOffset = uint32(v.frame.regs[vdp2MPOFR]) & 0x07

	// 16 planes from 8 register words (Param A)
	cfg.mapRegs = v.unpackMapRegs(vdp2MPABRA)

	plsz := v.frame.regs[vdp2PLSZ]
	cfg.planePagesH, cfg.planePagesV = planePages(plsz >> 8)

	cfg.screenOver = uint8((plsz >> 10) & 0x03)
	cfg.priority = uint8(v.frame.regs[vdp2PRIR]) & 0x07
	cfg.cramOffset = uint8(v.frame.regs[vdp2CRAOFB]) & 0x07
	cfg.rpMode = uint8(v.frame.regs[vdp2RPMD]) & 0x03

	// Special priority and special color calculation modes (RBG0 = bits 9:8)
	cfg.sfprmdMode = uint8((v.frame.regs[vdp2SFPRMD] >> 8) & 0x03)
	cfg.sfccmdMode = uint8((v.frame.regs[vdp2SFCCMD] >> 8) & 0x03)

	// RBG0 mosaic: bit 4 = R0MZE, horizontal only for rotation screens
	mzctl := v.frame.regs[vdp2MZCTL]
	if mzctl&(1<<4) != 0 {
		cfg.mosaicH = int((mzctl>>8)&0xF) + 1
	}

	return cfg
}

// computePerFrame computes per-frame rotation constants from parameters.
func computePerFrame(p *rotParams) rbgPerFrame {
	var pf rbgPerFrame
	// Xp = A*(Px-Cx) + B*(Py-Cy) + C*(Pz-Cz) + Cx + Mx
	// A is .10, (Px-Cx) is integer -> result is .10
	pf.xp = p.a*(p.px-p.cx) + p.b*(p.py-p.cy) + p.c*(p.pz-p.cz) + (p.cx << 10) + p.mx
	pf.yp = p.d*(p.px-p.cx) + p.e*(p.py-p.cy) + p.f*(p.pz-p.cz) + (p.cy << 10) + p.my
	// dX = A*DX + B*DY (.10 * .10 = .20, >>10 = .10)
	pf.dxFP = (p.a*p.dx + p.b*p.dy) >> 10
	pf.dyFP = (p.d*p.dx + p.e*p.dy) >> 10
	return pf
}

// rotParamBases returns the byte addresses of rotation parameter
// table A and B in VRAM. Per VDP2 User's Manual section 6.3, RPTAU
// supplies bits 18:16 (low 3 bits) and RPTAL supplies bits 15:1 of
// an 18-bit value; bit 0 of RPTAL is reserved and must be ignored.
// The actual table address is that 18-bit value times 4. RPTA6
// (byte-address bit 7) is forced to 0 for parameter A and 1 for
// parameter B regardless of any value written to it.
func (v *VDP2) rotParamBases() (paramABase, paramBBase uint32) {
	rptau := v.frame.regs[vdp2RPTAU]
	rptal := v.frame.regs[vdp2RPTAL]
	tableBase := ((uint32(rptau&0x07) << 16) | uint32(rptal&0xFFFE)) * 2
	paramABase = tableBase & ^uint32(0x80)
	paramBBase = paramABase | 0x80
	return
}

// buildRBG0Frame latches the per-frame rotation state for RBG0:
// rotation parameter tables read from VRAM, derived per-frame
// constants, coefficient table configuration, RPRCTL per-line re-read
// flags, and the Param B map configuration used by RPMD modes 1-3.
func (v *VDP2) buildRBG0Frame(cfg *rbgConfig) rbgFrame {
	var rf rbgFrame

	rf.paramABase, rf.paramBBase = v.rotParamBases()

	// Read parameter sets based on RPMD mode
	rf.needA = cfg.rpMode != 1
	rf.needB = cfg.rpMode >= 1
	if rf.needA {
		rf.paramA = v.readRotParams(rf.paramABase)
		rf.pfA = computePerFrame(&rf.paramA)
	}
	if rf.needB {
		rf.paramB = v.readRotParams(rf.paramBBase)
		rf.pfB = computePerFrame(&rf.paramB)
	}

	// Coefficient table config
	ktctl := v.frame.regs[vdp2KTCTL]
	rf.ktaof = v.frame.regs[vdp2KTAOF]
	rf.crkte = v.frame.regs[vdp2RAMCTL]&0x8000 != 0 && v.frame.cramMode == 1
	rf.coefDotOK = v.rbgCoefDotBanks()
	rf.coefEnA = ktctl&0x01 != 0
	rf.coefOneWordA = ktctl&0x02 != 0
	rf.coefModeA = (ktctl >> 2) & 0x03
	rf.coefEnB = ktctl&0x0100 != 0
	rf.coefOneWordB = ktctl&0x0200 != 0
	rf.coefModeB = (ktctl >> 10) & 0x03
	rf.klceA = ktctl&0x10 != 0 && !rf.coefOneWordA
	rf.klceB = ktctl&0x1000 != 0 && !rf.coefOneWordB

	// Param B map config (for modes 1,2,3)
	if rf.needB {
		rf.mapRegsB = v.unpackMapRegs(vdp2MPABRB)
		rf.mapOffsetB = uint32((v.frame.regs[vdp2MPOFR] >> 4) & 0x07)
		rf.planePagesHB, rf.planePagesVB = planePages(v.frame.regs[vdp2PLSZ] >> 12)
		rf.screenOverB = uint8((v.frame.regs[vdp2PLSZ] >> 14) & 0x03)
	}

	return rf
}

// decodeRBG1Config builds an rbgConfig for RBG1.
// RBG1 always uses parameter B and borrows register fields from NBG0's slots.
func (v *VDP2) decodeRBG1Config() rbgConfig {
	var cfg rbgConfig

	cfg.enabled = v.frame.regs[vdp2BGON]&(1<<5) != 0 && v.frame.regs[vdp2BGON]&(1<<4) != 0
	cfg.transpOff = v.frame.regs[vdp2BGON]&(1<<13) != 0

	// RBG1 borrows character control fields from NBG0's CHCTLA slots.
	chctla := v.frame.regs[vdp2CHCTLA]
	cfg.charSize1x1 = chctla&0x01 == 0
	cfg.colorMode = uint8((chctla >> 4) & 0x07)
	cfg.bitmapMode = chctla&0x02 != 0
	if cfg.bitmapMode {
		bmsz := (chctla >> 2) & 0x03
		cfg.bmpWidth = 512
		cfg.bmpHeight = 256
		if bmsz&0x02 != 0 {
			cfg.bmpWidth = 1024
		}
		if bmsz&0x01 != 0 {
			cfg.bmpHeight = 512
		}
		bmpna := v.frame.regs[vdp2BMPNA]
		// RBG1 reuses NBG0's BMPNA. Same pre-shift convention.
		cfg.bmpPalette = uint8(bmpna&0x07) << 4
		cfg.bmpSpecialPri = bmpna&(1<<5) != 0
		cfg.bmpSpecialCC = bmpna&(1<<4) != 0
	}

	cfg.pncnReg = v.frame.regs[vdp2PNCN0]
	cfg.pnWord1 = cfg.pncnReg&0x8000 != 0
	cfg.auxMode1 = cfg.pncnReg&0x4000 != 0
	cfg.mapOffset = uint32((v.frame.regs[vdp2MPOFR] >> 4) & 0x07)

	cfg.mapRegs = v.unpackMapRegs(vdp2MPABRB)

	plsz := v.frame.regs[vdp2PLSZ]
	cfg.planePagesH, cfg.planePagesV = planePages(plsz >> 12)

	cfg.screenOver = uint8((plsz >> 14) & 0x03)
	cfg.priority = uint8(v.frame.regs[vdp2PRINA]) & 0x07
	cfg.cramOffset = uint8(v.frame.regs[vdp2CRAOFA] & 0x07)
	cfg.rpMode = 1

	cfg.sfprmdMode = uint8(v.frame.regs[vdp2SFPRMD] & 0x03)
	cfg.sfccmdMode = uint8(v.frame.regs[vdp2SFCCMD] & 0x03)

	// RBG1 mosaic: bit 0 = N0MZE (shared with NBG0), horizontal only
	mzctl := v.frame.regs[vdp2MZCTL]
	if mzctl&(1<<0) != 0 {
		cfg.mosaicH = int((mzctl>>8)&0xF) + 1
	}

	return cfg
}

// buildRBG1Frame latches the per-frame rotation state for RBG1. RBG1
// always uses rotation parameter B, so only the B half of rbgFrame is
// populated.
func (v *VDP2) buildRBG1Frame() rbgFrame {
	var rf rbgFrame

	_, rf.paramBBase = v.rotParamBases()
	rf.needB = true
	rf.paramB = v.readRotParams(rf.paramBBase)
	rf.pfB = computePerFrame(&rf.paramB)

	ktctl := v.frame.regs[vdp2KTCTL]
	rf.ktaof = v.frame.regs[vdp2KTAOF]
	rf.crkte = v.frame.regs[vdp2RAMCTL]&0x8000 != 0 && v.frame.cramMode == 1
	rf.coefDotOK = v.rbgCoefDotBanks()
	rf.coefEnB = ktctl&0x0100 != 0
	rf.coefOneWordB = ktctl&0x0200 != 0
	rf.coefModeB = (ktctl >> 10) & 0x03
	rf.klceB = ktctl&0x1000 != 0 && !rf.coefOneWordB

	return rf
}

// BeginFrame samples the frame-scoped render state: output geometry
// (with the geometry-change blank), field parity, DISP (blanking the
// output while the display is off), and the RBG configuration and
// rotation parameter tables. It also refreshes the register snapshot;
// the per-line state is decoded by BeginLine.
func (v *VDP2) BeginFrame() {
	fs := &v.frame
	prevWidth, prevHeight, prevIntl := fs.width, fs.height, fs.effIntl
	fs.regs = v.regs
	fs.hiRes = v.hiRes
	fs.cramMode = v.cramMode()
	fs.disp = fs.regs[vdp2TVMD]&0x8000 != 0
	fs.width = int(v.activeWidth)
	fs.height = int(v.activeLines)
	fs.effIntl = v.effectiveInterlace()
	fs.field = v.fieldBit()

	// A geometry change reinterprets the framebuffer's row layout:
	// pixels written under the previous stride/height are meaningless
	// bytes under the new one. Hardware has no persistent framebuffer
	// (each scanline is generated during the scan), so a mode switch
	// cannot show remnants of the previous mode. Blank the buffer so
	// it does not show them either. Under LSMD=3 the first frame also
	// writes both field rows (fieldBootstrap): only one field has
	// been generated in the new mode, and displaying it interleaved
	// with blank rows would show a half-bright frame that hardware's
	// continuous scan does not produce.
	fs.fieldBootstrap = false
	if fs.width != prevWidth || fs.height != prevHeight || fs.effIntl != prevIntl {
		fs.fieldBootstrap = fs.effIntl == 3
		for i := 0; i < len(v.framebuffer); i += 4 {
			v.framebuffer[i] = 0
			v.framebuffer[i+1] = 0
			v.framebuffer[i+2] = 0
			v.framebuffer[i+3] = 0xFF
		}
	}

	// DISP=0: blank output and skip all render setup; BeginLine and
	// RenderTo no-op for the frame. The manual reads BDCLMD as selecting back
	// screen color over black for the standard display area when DISP
	// is 0, but on real hardware the analog video signal is suppressed
	// while DISP=0, so the picture is black on screen regardless of
	// BDCLMD. Games rely on this when they clear DISP during a
	// transition while still mutating back-screen / palette state in
	// VRAM - honoring BDCLMD here exposes those transient values as a
	// 1-frame color flash that does not occur on hardware. Under
	// LSMD=3 paint every displayed row so the screen-off state is
	// truly blank rather than leaving stale pixels from the prior
	// field visible.
	if !fs.disp {
		stride := fs.width * 4
		displayRows := fs.height
		if fs.effIntl == 3 {
			displayRows = fs.height * 2
		}
		for row := 0; row < displayRows; row++ {
			off := row * stride
			for x := 0; x < fs.width; x++ {
				v.framebuffer[off] = 0
				v.framebuffer[off+1] = 0
				v.framebuffer[off+2] = 0
				v.framebuffer[off+3] = 0xFF
				off += 4
			}
		}
		return
	}

	// RBG0/RBG1: configuration and rotation state are frame-scoped.
	// The rotation parameter tables are read from VRAM once per frame
	// (RPRCTL selects per-line re-reads of individual fields), and the
	// per-line stepping accumulates on the frame state.
	fs.rbg1Active = fs.regs[vdp2BGON]&(1<<5) != 0
	fs.rbg0On = false
	if fs.regs[vdp2BGON]&(1<<4) != 0 {
		cfg := v.decodeRBGConfig()
		if cfg.enabled && cfg.priority != 0 {
			fs.rbg0On = true
			fs.rbg0 = cfg
			fs.rbg0F = v.buildRBG0Frame(&fs.rbg0)
		}
	}
	fs.rbg1On = false
	if fs.rbg1Active {
		cfg := v.decodeRBG1Config()
		if cfg.enabled && cfg.priority != 0 {
			fs.rbg1On = true
			fs.rbg1 = cfg
			fs.rbg1F = v.buildRBG1Frame()
		}
	}

	// Force the line-state decode at the frame's first BeginLine.
	v.regsDirty = true
}

// decodeLineState re-decodes the register-derived line state from the
// current register snapshot: the window mask cache, NBG layer enables
// and configurations, and the composite setup.
func (v *VDP2) decodeLineState() {
	fs := &v.frame

	v.winMaskValid = false
	v.buildWindowMaskCache()

	// When RBG1 is enabled, all NBG screens are disabled.
	if fs.rbg1Active {
		for s := 0; s < 4; s++ {
			fs.nbgOn[s] = false
		}
	} else {
		// NBG2/NBG3 availability per VDP2 manual: NBG2 cannot be
		// displayed when NBG0 is 2048/32,768/16.77M colors; NBG3 cannot
		// when NBG1 is 2048/32,768 colors or NBG0 is 16.77M colors. The
		// ZMCTL reduction rule (Sec 5.2 Table 5.2) is an additional
		// disable. Hi-res alone does not disable NBG2/NBG3.
		zmctl := fs.regs[vdp2ZMCTL]
		chctla := fs.regs[vdp2CHCTLA]
		nbg0cm := uint8((chctla >> 4) & 0x07)
		nbg1cm := uint8((chctla >> 12) & 0x03)
		disableNBG2 := nbg0cm >= 2 ||
			reductionDisablesCompanion(uint8(zmctl&0x03), nbg0cm)
		disableNBG3 := nbg1cm >= 2 || nbg0cm == 4 ||
			reductionDisablesCompanion(uint8((zmctl>>8)&0x03), nbg1cm)

		for s := 0; s < 4; s++ {
			cfg := v.decodeNBGConfig(s)
			on := cfg.enabled && cfg.priority != 0
			if s == 2 && disableNBG2 {
				on = false
			}
			if s == 3 && disableNBG3 {
				on = false
			}
			fs.nbgOn[s] = on
			if on {
				fs.nbg[s] = cfg
			}
		}
	}

	// Composite setup
	ccctl := fs.regs[vdp2CCCTL]
	fs.ccmd = ccctl&0x100 != 0   // bit 8: add-as-is mode
	fs.ccrtmd = ccctl&0x200 != 0 // bit 9: ratio from second image
	fs.exccen = ccctl&0x400 != 0 // bit 10: extended color calculation

	// Hi-res CC restrictions
	if fs.hiRes {
		fs.exccen = false // extended CC not available in hi-res
	}

	// Per-layer "is RGB direct color format". Used by extended color
	// calculation: per Table 12.2, in CRAM mode 1/2 the 2nd+3rd blend
	// only applies when the 2nd image is RGB format. NBG2/NBG3 only
	// support palette format. Sprite format depends on SPCTL mixed mode
	// per pixel; treat as palette here (the common case).
	chctlaR := fs.regs[vdp2CHCTLA]
	chctlbR := fs.regs[vdp2CHCTLB]
	fs.layerIsRGB = [6]bool{}
	fs.layerIsRGB[0] = uint8((chctlaR>>4)&0x07) >= 3
	fs.layerIsRGB[1] = uint8((chctlaR>>12)&0x03) >= 3
	fs.layerIsRGB[4] = uint8((chctlbR>>12)&0x07) >= 3

	// Gradation calculation: horizontal blur on designated screen.
	// Normal TV mode + CRAM mode 0 only.
	fs.gradBuf = nil
	boken := ccctl&0x8000 != 0 // bit 15
	if boken && (fs.hiRes || fs.cramMode != 0) {
		boken = false
	}
	if boken {
		fs.exccen = false             // BOKEN overrides EXCCEN
		switch (ccctl >> 12) & 0x07 { // BOKN bits 14:12
		// BOKN=0 (sprite) is a valid hardware selection but is not
		// modeled: gradation blurs a [priority<<24|RGB] layer buffer in
		// place, and the sprite layer has no such buffer - it is decoded
		// live per pixel from the VDP1 framebuffer during compositing.
		// Values 3 and 7 are invalid per the manual Sec 12.2 table.
		case 1: // RBG0
			fs.gradBuf = v.rbg0Buf
		case 2: // NBG0 (or RBG1 when active)
			if fs.rbg1Active {
				fs.gradBuf = v.rbg1Buf
			} else {
				fs.gradBuf = v.layerBufs[0]
			}
		case 4: // NBG1 (or EXBG)
			fs.gradBuf = v.layerBufs[1]
		case 5: // NBG2
			fs.gradBuf = v.layerBufs[2]
		case 6: // NBG3
			fs.gradBuf = v.layerBufs[3]
		}
	}
}

// BeginLine prepares row y: it refreshes the register snapshot
// (writes since the previous line take effect from this row), rebuilds
// the CRAM cache if CRAM was written, re-decodes the register-derived
// line state if registers were written, and builds the row's span
// renderer chain - enabled layers, gradation, composite - with each
// renderer's per-line setup (line scroll tables, rotation per-line
// stepping, back-screen color) done here. fb is the VDP1 display
// framebuffer view the row's sprite layer samples.
func (v *VDP2) BeginLine(y int, fb vdp1FBView) {
	fs := &v.frame
	v.lineFB = fb
	v.lineSpanX = 0
	v.lineNSpans = 0
	v.lineActive = fs.disp && y >= 0 && y < fs.height
	if !v.lineActive {
		return
	}

	fs.regs = v.regs
	fs.cramMode = v.cramMode()
	v.buildCRAMCache()
	if v.regsDirty {
		v.regsDirty = false
		v.decodeLineState()
	}

	n := 0
	for s := 0; s < 4; s++ {
		if fs.nbgOn[s] {
			v.lineSpans[n] = v.nbgSpanSetup(v.layerBufs[s], &fs.nbg[s], s, y)
			n++
		}
	}
	if fs.rbg0On {
		// The line color buffer's per-pixel writes are conditional on
		// coefficient state; clear the row so unwritten pixels read as
		// no-line-color rather than stale data.
		row := y * fs.width
		clear(v.rbg0LCBuf[row : row+fs.width])
		v.lineSpans[n] = v.rbg0SpanSetup(v.rbg0Buf, &fs.rbg0, &fs.rbg0F, y)
		n++
	}
	if fs.rbg1On {
		row := y * fs.width
		clear(v.rbg1LCBuf[row : row+fs.width])
		v.lineSpans[n] = v.rbg1SpanSetup(v.rbg1Buf, &fs.rbg1, &fs.rbg1F, y)
		n++
	}
	if fs.gradBuf != nil {
		v.lineSpans[n] = gradationSpanSetup(fs.gradBuf, fs.width, y)
		n++
	}
	v.lineSpans[n] = v.compositeSpanSetup(y)
	n++
	v.lineNSpans = n
}

// RenderTo renders the prepared row's pixels up to x (exclusive),
// continuing from the previous RenderTo call: each span renderer in
// the line's chain advances through [lineSpanX, x). Layer pixels and
// VRAM table data are read at call time, so writes landing between
// calls are visible to the row's later pixels.
func (v *VDP2) RenderTo(x int) {
	if !v.lineActive {
		return
	}
	if x > v.frame.width {
		x = v.frame.width
	}
	x0 := v.lineSpanX
	if x <= x0 {
		return
	}
	v.lineSpanX = x
	for i := 0; i < v.lineNSpans; i++ {
		v.lineSpans[i](x0, x)
	}
}

// RenderLine produces the complete framebuffer row y in one call:
// BeginLine plus a full-width RenderTo. A row writes only the layer
// buffers, the RBG line-color buffers, and framebuffer row
// frameOutRow(y).
func (v *VDP2) RenderLine(y int, fb vdp1FBView) {
	v.BeginLine(y, fb)
	v.RenderTo(v.frame.width)
}

// frameOutRow maps a field-line index (0..height-1) to the framebuffer
// row for the field latched by BeginFrame: y*2 + field under LSMD=3,
// identity otherwise.
func (v *VDP2) frameOutRow(y int) int {
	if v.frame.effIntl == 3 {
		return y*2 + v.frame.field
	}
	return y
}

// FrameFieldBit returns the field bit latched by BeginFrame: the
// parity of the field being rendered this frame.
func (v *VDP2) FrameFieldBit() int { return v.frame.field }
