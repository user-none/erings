// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// vdp1FBView describes the VDP1 display framebuffer a line samples:
// pixel data, format, and dimensions. A nil data slice means no
// sprite layer. rotated is set when VDP1 is in a rotation TV mode:
// the sprite layer samples at coordinates from rotation parameter A
// instead of the screen position.
type vdp1FBView struct {
	data    []byte
	is8bpp  bool
	width   int
	height  int
	rotated bool
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
	field   int   // fieldBit() at frame start: output-row parity
	hiRes   bool  // hi-res horizontal mode at frame start

	// Raw odd-field flag under LSMD=3, for VDP1 DIE framebuffer
	// selection.
	vdp1Field int

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

	// Effective vertical-scroll base per NBG screen, 11.8 fixed point:
	// base + dispY*incY gives a line's vertical position (VDP2 manual
	// Sec 5.2 display coordinate formula). A SCYN write during the
	// display sets the position directly from the following line, so
	// BeginLine rebases; without mid-display writes the base equals
	// the SCYN registers.
	nbgYBaseFP [4]int32

	// EXBG occupies NBG1's slot: NBG1's priority, color calculation,
	// and window registers apply, and NBG1's own rendering is
	// suppressed while it is displayed.
	exbgOn  bool
	exbgPri uint8
	exbgCC  bool

	rbg0On bool
	rbg0   rbgConfig
	rbg0F  rbgFrame

	rbg1On bool
	rbg1   rbgConfig
	rbg1F  rbgFrame

	// Sprite framebuffer rotated read: in a VDP1 rotation TV mode the
	// framebuffer read coordinates come from rotation parameter A
	// (VDP1 manual Sec 1.2; VDP2 manual Sec 6). Latched frame-scoped
	// like the RBG state; BeginLine steps it per line and honors the
	// RPRCTL Xst/Yst re-read arms.
	sprRotA                          rotParams
	sprRotBase                       uint32
	sprRotLastVcntX, sprRotLastVcntY int64

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
	fs.vdp1Field = 0
	if fs.effIntl == 3 && v.oddField {
		fs.vdp1Field = 1
	}

	// NBG vertical counters load from the top-of-display SCYN capture;
	// mid-display SCYN writes rebase them per line in BeginLine.
	fs.nbgYBaseFP = v.scynFrame

	// A geometry change reinterprets the framebuffer's row layout:
	// pixels written under the previous stride/height are meaningless
	// bytes under the new one. Hardware has no persistent framebuffer
	// (each scanline is generated during the scan), so a mode switch
	// cannot show remnants of the previous mode. Blank the buffer so
	// it does not show them either. Under interlace the first frame
	// also writes both field rows (fieldBootstrap): only one field has
	// been generated in the new mode, and displaying it interleaved
	// with blank rows would show a half-bright frame that hardware's
	// continuous scan does not produce.
	fs.fieldBootstrap = false
	if fs.width != prevWidth || fs.height != prevHeight || fs.effIntl != prevIntl {
		fs.fieldBootstrap = fs.effIntl >= 2
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
	// interlace paint every displayed row so the screen-off state is
	// truly blank rather than leaving stale pixels from the prior
	// field visible.
	if !fs.disp {
		stride := fs.width * 4
		displayRows := fs.height
		if fs.effIntl >= 2 {
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

	// EXBG: poll the external-image source once per frame. Latched
	// here (render-walker context) so per-line state and spans read
	// stable pixels for the whole frame.
	v.exbgOK = false
	if v.exbgSrc != nil && fs.regs[vdp2EXTEN]&1 != 0 {
		if rgb, w, h, ok := v.exbgSrc.MpegFrameRGB(); ok {
			v.exbgBuf, v.exbgW, v.exbgH = rgb, w, h
			v.exbgOK = true
		}
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

	// Sprite framebuffer rotated read: latch rotation parameter A
	// independently of the RBG0 state - the read is driven by VDP1's
	// TV mode, and RPMD mode 1 leaves the RBG frame's A half unread.
	fs.sprRotBase, _ = v.rotParamBases()
	fs.sprRotA = v.readRotParams(fs.sprRotBase)
	fs.sprRotLastVcntX, fs.sprRotLastVcntY = 0, 0

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

	// EXBG: on when EXBGEN is set, a frame is latched, and NBG1's
	// priority (its slot) is nonzero.
	fs.exbgOn = false
	if v.exbgOK {
		pri := uint8(fs.regs[vdp2PRINA]>>8) & 0x07
		if pri != 0 {
			fs.exbgOn = true
			fs.exbgPri = pri
			fs.exbgCC = isCCEnabled(fs.regs[vdp2CCCTL], 1)
			fs.nbgOn[1] = false
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

	// NBG vertical-scroll counter reloads: a SCYN write during line y-1
	// reloads the screen's vertical counter, and line y displays the
	// written position verbatim. Rebase the effective Y base so
	// base + dispY*incY yields the written position here and keeps
	// stepping by incY on later lines.
	if y > 0 && y-1 < len(v.scynSet) {
		dispY := int32(y)
		if fs.effIntl == 3 {
			dispY = int32(y*2 + fs.field)
		}
		for s := 0; s < 4; s++ {
			if !v.scynSet[y-1][s] {
				continue
			}
			incY := int32(0x100)
			switch s {
			case 0:
				incY = int32(fs.regs[vdp2ZMYIN0]&0x07)<<8 | int32(fs.regs[vdp2ZMYDN0]>>8)
			case 1:
				incY = int32(fs.regs[vdp2ZMYIN1]&0x07)<<8 | int32(fs.regs[vdp2ZMYDN1]>>8)
			}
			fs.nbgYBaseFP[s] = v.scynRec[y-1][s] - dispY*incY
		}
	}

	// Sprite framebuffer rotated read: this line's read coordinate is
	// Xst + dXst*Vcnt, not the full rotation matrix pipeline - per
	// VDP1 manual Sec 1.2 only the read start coordinates and movement
	// values are received from the VDP2. RPRCTL Xst/Yst arms re-read
	// and rebase the advance as in the RBG0 span setup.
	v.lineSprRot.enabled = fb.rotated
	if fb.rotated {
		vcnt := int64(y)
		if fs.effIntl == 3 {
			vcnt = int64(y*2 + fs.field)
		}
		arm := v.rotArmBits(y)
		if arm&0x01 != 0 {
			fs.sprRotA.xst = signExtendFP(v.readVRAM16(fs.sprRotBase+0x00), v.readVRAM16(fs.sprRotBase+0x02), 12)
			fs.sprRotLastVcntX = vcnt
		}
		if arm&0x02 != 0 {
			fs.sprRotA.yst = signExtendFP(v.readVRAM16(fs.sprRotBase+0x04), v.readVRAM16(fs.sprRotBase+0x06), 12)
			fs.sprRotLastVcntY = vcnt
		}
		v.lineSprRot.baseX = fs.sprRotA.xst + fs.sprRotA.dxst*(vcnt-fs.sprRotLastVcntX)
		v.lineSprRot.baseY = fs.sprRotA.yst + fs.sprRotA.dyst*(vcnt-fs.sprRotLastVcntY)
		v.lineSprRot.dx = fs.sprRotA.dx
		v.lineSprRot.dy = fs.sprRotA.dy
	}

	n := 0
	for s := 0; s < 4; s++ {
		if fs.nbgOn[s] {
			v.lineSpans[n] = v.nbgSpanSetup(v.layerBufs[s], &fs.nbg[s], s, y)
			n++
		}
	}
	if fs.exbgOn {
		v.lineSpans[n] = v.exbgSpanSetup(v.layerBufs[1], y)
		n++
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

// exbgSpanSetup prepares row y of the EXBG external-image screen: the
// latched frame's row is copied into the NBG1 layer buffer as direct
// RGB with the screen priority (and color-calculation flag) packed per
// pixel. Pixels outside the frame are transparent. The frame is mapped
// 1:1 from the screen origin; the display-window transforms of the
// $A1-$A4 command set are not applied.
func (v *VDP2) exbgSpanSetup(buf []uint32, y int) func(x0, x1 int) {
	width := v.frame.width
	base := uint32(v.frame.exbgPri) << 24
	if v.frame.exbgCC {
		base |= layerCCBit
	}
	var src []uint32
	if y < v.exbgH {
		src = v.exbgBuf[y*v.exbgW : y*v.exbgW+v.exbgW]
	}
	row := y * width
	return func(x0, x1 int) {
		// Clamp the span to the frame row once; pixels beyond it (and
		// whole rows below the frame, where src is empty) are
		// transparent.
		end := x1
		if end > len(src) {
			end = len(src)
		}
		if end < x0 {
			end = x0
		}
		for x := x0; x < end; x++ {
			buf[row+x] = base | src[x]
		}
		for x := end; x < x1; x++ {
			buf[row+x] = 0
		}
	}
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
// row for the field latched by BeginFrame: y*2 + field under either
// interlace mode (the fields weave into alternating output rows),
// identity for non-interlace.
func (v *VDP2) frameOutRow(y int) int {
	if v.frame.effIntl >= 2 {
		return y*2 + v.frame.field
	}
	return y
}

// FrameFieldBit returns 1 when the frame latched by BeginFrame is the
// odd field under double-density interlace, 0 otherwise. Selects the
// VDP1 display framebuffer for the field being rendered.
func (v *VDP2) FrameFieldBit() int { return v.frame.vdp1Field }
