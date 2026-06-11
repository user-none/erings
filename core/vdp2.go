// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

const (
	vdp2RegCount = 144        // 288 bytes / 2
	vdp2VRAMSize = 512 * 1024 // 512 KB
	vdp2CRAMSize = 4 * 1024   // 4 KB

	// Register indices (offset / 2)
	vdp2TVMD   = 0   // 0x0000
	vdp2EXTEN  = 1   // 0x0002 External Signal Enable Register
	vdp2TVSTAT = 2   // 0x0004
	vdp2VRSIZE = 3   // 0x0006
	vdp2HCNT   = 4   // 0x0008
	vdp2VCNT   = 5   // 0x000A
	vdp2RAMCTL = 7   // 0x000E
	vdp2BGON   = 16  // 0x0020
	vdp2MZCTL  = 17  // 0x0022
	vdp2SFSEL  = 18  // 0x0024
	vdp2SFCODE = 19  // 0x0026
	vdp2CHCTLA = 20  // 0x0028
	vdp2CHCTLB = 21  // 0x002A
	vdp2BMPNA  = 22  // 0x002C
	vdp2PNCN0  = 24  // 0x0030
	vdp2PNCN1  = 25  // 0x0032
	vdp2PNCN2  = 26  // 0x0034
	vdp2PNCN3  = 27  // 0x0036
	vdp2PLSZ   = 29  // 0x003A
	vdp2MPOFN  = 30  // 0x003C
	vdp2MPABN0 = 32  // 0x0040
	vdp2MPCDN0 = 33  // 0x0042
	vdp2MPABN1 = 34  // 0x0044
	vdp2MPCDN1 = 35  // 0x0046
	vdp2MPABN2 = 36  // 0x0048
	vdp2MPCDN2 = 37  // 0x004A
	vdp2MPABN3 = 38  // 0x004C
	vdp2MPCDN3 = 39  // 0x004E
	vdp2SCXIN0 = 56  // 0x0070
	vdp2SCXDN0 = 57  // 0x0072
	vdp2SCYIN0 = 58  // 0x0074
	vdp2SCYDN0 = 59  // 0x0076
	vdp2ZMXIN0 = 60  // 0x0078
	vdp2ZMXDN0 = 61  // 0x007A
	vdp2ZMYIN0 = 62  // 0x007C
	vdp2ZMYDN0 = 63  // 0x007E
	vdp2SCXIN1 = 64  // 0x0080
	vdp2SCXDN1 = 65  // 0x0082
	vdp2SCYIN1 = 66  // 0x0084
	vdp2SCYDN1 = 67  // 0x0086
	vdp2ZMXIN1 = 68  // 0x0088
	vdp2ZMXDN1 = 69  // 0x008A
	vdp2ZMYIN1 = 70  // 0x008C
	vdp2ZMYDN1 = 71  // 0x008E
	vdp2SCRCTL = 77  // 0x009A
	vdp2VCSTAU = 78  // 0x009C
	vdp2VCSTAL = 79  // 0x009E
	vdp2LSTA0U = 80  // 0x00A0
	vdp2LSTA0L = 81  // 0x00A2
	vdp2LSTA1U = 82  // 0x00A4
	vdp2LSTA1L = 83  // 0x00A6
	vdp2SCXN2  = 72  // 0x0090
	vdp2SCYN2  = 73  // 0x0092
	vdp2SCXN3  = 74  // 0x0094
	vdp2SCYN3  = 75  // 0x0096
	vdp2ZMCTL  = 76  // 0x0098
	vdp2WPSX0  = 96  // 0x00C0
	vdp2WPSY0  = 97  // 0x00C2
	vdp2WPEX0  = 98  // 0x00C4
	vdp2WPEY0  = 99  // 0x00C6
	vdp2WPSX1  = 100 // 0x00C8
	vdp2WPSY1  = 101 // 0x00CA
	vdp2WPEX1  = 102 // 0x00CC
	vdp2WPEY1  = 103 // 0x00CE
	vdp2WCTLA  = 104 // 0x00D0
	vdp2WCTLB  = 105 // 0x00D2
	vdp2LWTA0U = 108 // 0x00D8
	vdp2LWTA0L = 109 // 0x00DA
	vdp2LWTA1U = 110 // 0x00DC
	vdp2LWTA1L = 111 // 0x00DE
	vdp2SPCTL  = 112 // 0x00E0
	vdp2SDCTL  = 113 // 0x00E2
	vdp2LNCLEN = 116 // 0x00E8
	vdp2SFPRMD = 117 // 0x00EA
	vdp2CRAOFA = 114 // 0x00E4
	vdp2CCCTL  = 118 // 0x00EC
	vdp2SFCCMD = 119 // 0x00EE
	vdp2PRISA  = 120 // 0x00F0
	vdp2PRISB  = 121 // 0x00F2
	vdp2PRISC  = 122 // 0x00F4
	vdp2PRISD  = 123 // 0x00F6
	vdp2PRINA  = 124 // 0x00F8
	vdp2PRINB  = 125 // 0x00FA
	vdp2CCRSA  = 128 // 0x0100
	vdp2CCRSB  = 129 // 0x0102
	vdp2CCRSC  = 130 // 0x0104
	vdp2CCRSD  = 131 // 0x0106
	vdp2CCRNA  = 132 // 0x0108
	vdp2CCRNB  = 133 // 0x010A
	vdp2CLOFEN = 136 // 0x0110
	vdp2CLOFSL = 137 // 0x0112
	vdp2COAR   = 138 // 0x0114
	vdp2COAG   = 139 // 0x0116
	vdp2COAB   = 140 // 0x0118
	vdp2COBR   = 141 // 0x011A
	vdp2COBG   = 142 // 0x011C
	vdp2COBB   = 143 // 0x011E
	vdp2BMPNB  = 23  // 0x002E
	vdp2PNCR   = 28  // 0x0038
	vdp2MPOFR  = 31  // 0x003E
	vdp2MPABRA = 40  // 0x0050
	vdp2MPCDRA = 41  // 0x0052
	vdp2MPEFRA = 42  // 0x0054
	vdp2MPGHRA = 43  // 0x0056
	vdp2MPIJRA = 44  // 0x0058
	vdp2MPKLRA = 45  // 0x005A
	vdp2MPMNRA = 46  // 0x005C
	vdp2MPOPRA = 47  // 0x005E
	vdp2MPABRB = 48  // 0x0060
	vdp2MPCDRB = 49  // 0x0062
	vdp2MPEFRB = 50  // 0x0064
	vdp2MPGHRB = 51  // 0x0066
	vdp2MPIJRB = 52  // 0x0068
	vdp2MPKLRB = 53  // 0x006A
	vdp2MPMNRB = 54  // 0x006C
	vdp2MPOPRB = 55  // 0x006E
	vdp2LCTAU  = 84  // 0x00A8
	vdp2LCTAL  = 85  // 0x00AA
	vdp2BKTAU  = 86  // 0x00AC
	vdp2BKTAL  = 87  // 0x00AE
	vdp2RPMD   = 88  // 0x00B0
	vdp2RPRCTL = 89  // 0x00B2
	vdp2KTCTL  = 90  // 0x00B4
	vdp2KTAOF  = 91  // 0x00B6
	vdp2OVPNRA = 92  // 0x00B8
	vdp2OVPNRB = 93  // 0x00BA
	vdp2RPTAU  = 94  // 0x00BC
	vdp2RPTAL  = 95  // 0x00BE
	vdp2WCTLC  = 106 // 0x00D4
	vdp2WCTLD  = 107 // 0x00D6
	vdp2CRAOFB = 115 // 0x00E6
	vdp2PRIR   = 126 // 0x00FC
	vdp2CCRR   = 134 // 0x010C
	vdp2CCRLB  = 135 // 0x010E

	// TVSTAT bits
	tvstatPAL    = 1 << 0
	tvstatODD    = 1 << 1
	tvstatHBLANK = 1 << 2
	tvstatVBLANK = 1 << 3

	// System cycles per scanline by horizontal mode. Both crystals
	// share the ~15,734 Hz NTSC line rate; the difference is the
	// system clock the crystal drives.
	systemCyclesPerLine320 = 1708
	systemCyclesPerLine352 = 1820

	// Active system cycles per scanline. The pixel clock is
	// system clock / 4 in normal modes, so active = pixels * 4.
	activeSystemCycles320 = 1280
	activeSystemCycles352 = 1408

	// Lines per frame
	linesNTSC = 263
	linesPAL  = 313

	// Maximum framebuffer dimensions (Hi-Res B + double-density interlace + PAL 256)
	maxWidth  = 704
	maxHeight = 512
)

// VDP2 implements the Sega Saturn Video Display Processor 2.
type VDP2 struct {
	regs [vdp2RegCount]uint16
	vram []byte
	cram []byte

	// Rendering output
	framebuffer []byte      // RGBA8888 output, max maxWidth*maxHeight*4
	layerBufs   [4][]uint32 // per-NBG layer, packed priority<<24|R<<16|G<<8|B
	activeWidth uint16      // 320, 352, 640, or 704

	// Display mode state
	hiRes     bool  // HRESO bit 1 set (640/704 mode)
	interlace uint8 // 0=non-interlace, 2=single-density, 3=double-density

	// Timing state
	lineCycle uint32 // Current system clock position within line
	vLine     uint16 // Current scanline
	oddField  bool   // Current field (true=odd)

	// HV counter latch. HCNT/VCNT return latched snapshots captured
	// on CPU read of EXTEN (with EXLTEN=0), not live values.
	latchedLineCycle uint32
	latchedVLine     uint16
	latchedHiRes     bool
	latchedInterlace uint8
	exltfg           bool

	// Cached timing parameters
	systemCyclesPerLine uint32
	activeSystemCycles  uint32
	linesPerFrame       uint16
	activeLines         uint16
	pal                 bool

	// RBG layer buffers
	rbg0Buf   []uint32
	rbg1Buf   []uint32
	rbg0LCBuf []uint8 // per-pixel coefficient line color (bit7=valid, bits6:0=data)
	rbg1LCBuf []uint8

	// VDP1 display buffer for temporary sprite overlay
	vdp1DisplayFB []byte
	vdp1FB8bpp    bool
	vdp1FBWidth   int
	vdp1FBHeight  int

	// Per-frame CRAM color cache. Rebuilt at the top of RenderFrame so
	// the per-pixel color lookup is a table index instead of a CRAM read
	// plus RGB conversion. CRAM is static for the duration of one frame.
	cramCacheR     [2048]uint8
	cramCacheG     [2048]uint8
	cramCacheB     [2048]uint8
	cramCacheCC    [2048]bool
	cramCacheMask  uint32
	cramCacheValid bool

	// Per-frame window-mask fast path. The per-layer window control
	// decode and the all-windows-disabled early-out depend only on
	// registers, so the disabled-window result is precomputed once per
	// frame per layer (0-5) instead of per pixel.
	winMaskSkip  [6]bool
	winMaskVal   [6]bool
	winMaskValid bool

	// SCU reference for interrupt signaling
	scu *SCU
}

// NewVDP2 creates a new VDP2 instance with VRAM and CRAM allocated.
func NewVDP2(scu *SCU) *VDP2 {
	v := &VDP2{
		vram:        make([]byte, vdp2VRAMSize),
		cram:        make([]byte, vdp2CRAMSize),
		framebuffer: make([]byte, maxWidth*maxHeight*4),
		rbg0Buf:     make([]uint32, maxWidth*maxHeight),
		rbg1Buf:     make([]uint32, maxWidth*maxHeight),
		rbg0LCBuf:   make([]uint8, maxWidth*maxHeight),
		rbg1LCBuf:   make([]uint8, maxWidth*maxHeight),
		scu:         scu,
		oddField:    true,
	}
	for i := range v.layerBufs {
		v.layerBufs[i] = make([]uint32, maxWidth*maxHeight)
	}
	v.recalcTiming()
	return v
}

// Reset clears the VDP2 registers, VRAM/CRAM, and field-parity state to
// power-on defaults. Called during CKCHG/SYSRES. The line counter
// (vLine, lineCycle) is intentionally preserved: it represents the
// emulator's continuous scanline-time position, not register state.
// Zeroing it mid-RunFrame would desynchronize it from the outer-loop
// counter that drives per-line tier ticks and VBI/VBO dispatch,
// shifting drawing windows for games that issue CKCHG during boot.
func (v *VDP2) Reset() {
	v.oddField = true
	v.exltfg = false
	for i := range v.regs {
		v.regs[i] = 0
	}
	clear(v.vram)
	clear(v.cram)
	v.recalcTiming()
}

// SetPAL sets the PAL/NTSC mode and recalculates timing parameters.
func (v *VDP2) SetPAL(pal bool) {
	v.pal = pal
	v.recalcTiming()
}

// IsPAL reports whether VDP2 is configured for PAL (vs NTSC).
func (v *VDP2) IsPAL() bool { return v.pal }

// Read returns the 16-bit register value at the given byte offset.
// TVSTAT, HCNT, and VCNT are computed from timing state. Reading
// EXTEN with EXLTEN=0 latches the HV counters. Reading TVSTAT clears
// the EXLTFG flag.
func (v *VDP2) Read(offset uint32) uint16 {
	idx := offset / 2
	if idx >= vdp2RegCount {
		return 0
	}

	switch idx {
	case vdp2EXTEN:
		val := v.regs[idx]
		if val&0x0200 == 0 {
			v.latchHV()
		}
		return val
	case vdp2TVSTAT:
		stat := v.buildTVSTAT()
		v.exltfg = false
		return stat
	case vdp2VRSIZE:
		return 0 // 4Mbit, bit 15 = 0
	case vdp2HCNT:
		return v.buildHCNT()
	case vdp2VCNT:
		return v.buildVCNT()
	default:
		return v.regs[idx]
	}
}

// ReadInternal returns the stored register value with no side effects.
// Used by bus byte-write RMW paths that must not disturb latch state.
func (v *VDP2) ReadInternal(offset uint32) uint16 {
	idx := offset / 2
	if idx >= vdp2RegCount {
		return 0
	}
	return v.regs[idx]
}

// Write stores a 16-bit value at the given byte offset.
// Writes to read-only registers (TVSTAT, HCNT, VCNT, VRSIZE) are ignored.
func (v *VDP2) Write(offset uint32, val uint16) {
	idx := offset / 2
	if idx >= vdp2RegCount {
		return
	}

	switch idx {
	case vdp2TVSTAT, vdp2VRSIZE, vdp2HCNT, vdp2VCNT:
		// Read-only, ignore writes
		return
	case vdp2TVMD:
		v.regs[idx] = val
		v.recalcTiming()
	default:
		v.regs[idx] = val
	}
}

// ReadVRAM reads a byte from VDP2 VRAM.
func (v *VDP2) ReadVRAM(addr uint32) uint8 {
	return v.vram[addr&(vdp2VRAMSize-1)]
}

// WriteVRAM writes a byte to VDP2 VRAM.
func (v *VDP2) WriteVRAM(addr uint32, val uint8) {
	v.vram[addr&(vdp2VRAMSize-1)] = val
}

// ReadVRAM16 reads a big-endian 16-bit value from VDP2 VRAM.
func (v *VDP2) ReadVRAM16(addr uint32) uint16 {
	addr &= vdp2VRAMSize - 2
	return uint16(v.vram[addr])<<8 | uint16(v.vram[addr+1])
}

// WriteVRAM16 writes a big-endian 16-bit value to VDP2 VRAM.
func (v *VDP2) WriteVRAM16(addr uint32, val uint16) {
	addr &= vdp2VRAMSize - 2
	v.vram[addr] = uint8(val >> 8)
	v.vram[addr+1] = uint8(val)
}

// ReadVRAM32 reads a big-endian 32-bit value from VDP2 VRAM.
func (v *VDP2) ReadVRAM32(addr uint32) uint32 {
	addr &= vdp2VRAMSize - 4
	return uint32(v.vram[addr])<<24 | uint32(v.vram[addr+1])<<16 |
		uint32(v.vram[addr+2])<<8 | uint32(v.vram[addr+3])
}

// WriteVRAM32 writes a big-endian 32-bit value to VDP2 VRAM.
func (v *VDP2) WriteVRAM32(addr uint32, val uint32) {
	addr &= vdp2VRAMSize - 4
	v.vram[addr] = uint8(val >> 24)
	v.vram[addr+1] = uint8(val >> 16)
	v.vram[addr+2] = uint8(val >> 8)
	v.vram[addr+3] = uint8(val)
}

// ReadCRAM reads a byte from VDP2 Color RAM.
func (v *VDP2) ReadCRAM(addr uint32) uint8 {
	return v.cram[addr&(vdp2CRAMSize-1)]
}

// WriteCRAM writes a byte to VDP2 Color RAM.
// In CRAM mode 0, writes are mirrored between lower and upper 2KB halves.
func (v *VDP2) WriteCRAM(addr uint32, val uint8) {
	addr &= vdp2CRAMSize - 1
	v.cram[addr] = val
	cramMode := (v.regs[vdp2RAMCTL] >> 12) & 0x03
	if cramMode == 0 {
		v.cram[addr^0x800] = val // mirror to other half
	}
}

// ReadCRAM16 reads a big-endian 16-bit value from VDP2 Color RAM.
func (v *VDP2) ReadCRAM16(addr uint32) uint16 {
	addr &= vdp2CRAMSize - 2
	return uint16(v.cram[addr])<<8 | uint16(v.cram[addr+1])
}

// WriteCRAM16 writes a big-endian 16-bit value to VDP2 Color RAM.
func (v *VDP2) WriteCRAM16(addr uint32, val uint16) {
	v.WriteCRAM(addr, uint8(val>>8))
	v.WriteCRAM(addr+1, uint8(val))
}

// ReadCRAM32 reads a big-endian 32-bit value from VDP2 Color RAM.
func (v *VDP2) ReadCRAM32(addr uint32) uint32 {
	addr &= vdp2CRAMSize - 4
	return uint32(v.cram[addr])<<24 | uint32(v.cram[addr+1])<<16 |
		uint32(v.cram[addr+2])<<8 | uint32(v.cram[addr+3])
}

// WriteCRAM32 writes a big-endian 32-bit value to VDP2 Color RAM.
func (v *VDP2) WriteCRAM32(addr uint32, val uint32) {
	v.WriteCRAM(addr, uint8(val>>24))
	v.WriteCRAM(addr+1, uint8(val>>16))
	v.WriteCRAM(addr+2, uint8(val>>8))
	v.WriteCRAM(addr+3, uint8(val))
}

// cramMode returns the current CRAM mode from RAMCTL bits 13:12.
func (v *VDP2) cramMode() uint8 {
	return uint8((v.regs[vdp2RAMCTL] >> 12) & 0x03)
}

// SetVDP1DisplayFB sets the VDP1 display frame buffer and its format
// for sprite overlay compositing.
func (v *VDP2) SetVDP1DisplayFB(fb []byte, is8bpp bool, fbWidth, fbHeight int) {
	v.vdp1DisplayFB = fb
	v.vdp1FB8bpp = is8bpp
	v.vdp1FBWidth = fbWidth
	v.vdp1FBHeight = fbHeight
}

// SystemCyclesPerLine returns the number of system clock cycles per scanline.
func (v *VDP2) SystemCyclesPerLine() uint32 { return v.systemCyclesPerLine }

// LinesPerFrame returns the total number of scanlines per frame.
func (v *VDP2) LinesPerFrame() uint16 { return v.linesPerFrame }

// ActiveLines returns the number of active display lines.
func (v *VDP2) ActiveLines() uint16 { return v.activeLines }

// effectiveInterlace returns the interlace mode actually in effect for
// display output. Per VDP2 manual section 4.11, when LSMD=3 is programmed
// and any NBG mosaic enable bit is set, the hardware forces the screen
// back to single-density interlace. All renderer surfaces that depend on
// the interlace mode must consult this helper rather than v.interlace.
func (v *VDP2) effectiveInterlace() uint8 {
	if v.interlace == 3 {
		// MZCTL bits 3:0 = N3MZE, N2MZE, N1MZE, N0MZE (NBG3..NBG0).
		// RBG mosaic (bit 4) is horizontal-only and does not trigger the
		// downgrade; only the NBG enables do.
		if v.regs[vdp2MZCTL]&0x000F != 0 {
			return 2
		}
	}
	return v.interlace
}

// DisplayHeight returns the number of rows in the output framebuffer.
// LSMD=3 double-density interlace doubles the displayed vertical
// resolution; LSMD=3 with NBG mosaic enabled falls back to single-density
// (non-doubled) per VDP2 manual section 4.11.
func (v *VDP2) DisplayHeight() int {
	if v.effectiveInterlace() == 3 {
		return int(v.activeLines) * 2
	}
	return int(v.activeLines)
}

// fieldBit returns 1 when the current field to be drawn is odd, 0
// otherwise. Always returns 0 outside effective LSMD=3 so callers can use
// the result unconditionally in row index math.
func (v *VDP2) fieldBit() int {
	if v.effectiveInterlace() == 3 && v.oddField {
		return 1
	}
	return 0
}

// FieldBit exports the current field bit for cross-package consumers
// (notably VDP1, which selects the parity-matched framebuffer under DIE=1).
// Returns 0 outside effective LSMD=3.
func (v *VDP2) FieldBit() int { return v.fieldBit() }

// outRow maps a field-line index (0..activeLines-1) to the framebuffer
// row that should be written for the current field. Identity outside
// effective LSMD=3.
func (v *VDP2) outRow(y int) int {
	if v.effectiveInterlace() == 3 {
		return y*2 + v.fieldBit()
	}
	return y
}

// ActiveWidth returns the number of active pixels per line.
func (v *VDP2) ActiveWidth() uint16               { return v.activeWidth }
func (v *VDP2) ActiveSystemCyclesPerLine() uint32 { return v.activeSystemCycles }

// Framebuffer returns the RGBA8888 pixel output buffer.
func (v *VDP2) Framebuffer() []byte { return v.framebuffer }

// FramebufferStride returns the stride (bytes per row) of the framebuffer.
func (v *VDP2) FramebufferStride() int { return int(v.activeWidth) * 4 }

// TickSystemCycles advances the intra-line cycle position (TVSTAT
// HBLANK, H/V latch). Line advancement is driven by EndLine/EndFrame
// from the emulator frame loop.
func (v *VDP2) TickSystemCycles(cycles uint32) {
	v.lineCycle += cycles
}

// EndLine advances the line counter at a scanline boundary. Called by
// the frame loop for every line except the last of the frame.
func (v *VDP2) EndLine() {
	v.lineCycle = 0
	v.vLine++

	if v.vLine == v.activeLines {
		v.scu.RaiseVBlankIN()
	}

	if v.vLine < v.activeLines {
		v.scu.RaiseHBlankIN(v.vLine)
	}
}

// EndFrame wraps the line counter at the frame boundary. Called by
// the frame loop after the last line of the frame.
func (v *VDP2) EndFrame() {
	v.lineCycle = 0
	v.vLine = 0
	v.oddField = !v.oddField
	v.scu.RaiseVBlankOUT()
	v.scu.RaiseHBlankIN(0)
}

// buildTVSTAT computes the TVSTAT register from current timing state.
func (v *VDP2) buildTVSTAT() uint16 {
	var stat uint16

	if v.pal {
		stat |= tvstatPAL
	}
	if v.interlace == 0 || v.oddField {
		stat |= tvstatODD // always 1 in non-interlace
	}
	if v.lineCycle >= v.activeSystemCycles {
		stat |= tvstatHBLANK
	}
	if v.vLine >= v.activeLines {
		stat |= tvstatVBLANK
	}
	if v.exltfg {
		stat |= 1 << 9
	}

	return stat
}

// latchHV snapshots the current H/V counters and active mode into
// the latched* fields, and raises EXLTFG. Called on CPU read of
// EXTEN with EXLTEN=0 per PDF Sec 2.5 page 19.
func (v *VDP2) latchHV() {
	v.latchedLineCycle = v.lineCycle
	v.latchedVLine = v.vLine
	v.latchedHiRes = v.hiRes
	v.latchedInterlace = v.interlace
	v.exltfg = true
}

// buildHCNT formats the latched H counter per Table 2.3. In normal
// modes the 9-bit H[8:0] value is placed in HCT[9:1] with HCT0 clear.
// In hi-res modes the full 10-bit H[9:0] value occupies HCT[9:0].
// The pixel clock is the system clock / 4 in normal modes and the
// system clock / 2 in hi-res modes, derived from Table 2.3 bit widths
// and the VRAM cycle structure in Sec 3.3.
func (v *VDP2) buildHCNT() uint16 {
	if v.latchedHiRes {
		return uint16((v.latchedLineCycle >> 1) & 0x03FF)
	}
	return uint16(((v.latchedLineCycle >> 2) & 0x01FF) << 1)
}

// buildVCNT formats the latched V counter per Table 2.4. The 9-bit
// V[8:0] value is placed in VCT[9:1]. In double-density interlace,
// VCT0 carries the field flag (0=odd, 1=even) which is inverted
// relative to the TVSTAT ODD bit convention.
func (v *VDP2) buildVCNT() uint16 {
	val := uint16((v.latchedVLine & 0x01FF) << 1)
	if v.latchedInterlace == 3 && !v.oddField {
		val |= 0x0001
	}
	return val
}

// recalcTiming updates cached timing parameters from TVMD and PAL flag.
// Only NTSC/PAL TV modes are supported; exclusive monitor modes (HRESO
// bit 2) are ignored and treated as the equivalent TV mode.
func (v *VDP2) recalcTiming() {
	tvmd := v.regs[vdp2TVMD]
	hreso := tvmd & 0x03

	// Horizontal resolution from HRESO bits 1:0
	switch hreso {
	case 0:
		v.activeWidth = 320
	case 1:
		v.activeWidth = 352
	case 2:
		v.activeWidth = 640
	case 3:
		v.activeWidth = 704
	}

	v.hiRes = hreso&0x02 != 0

	// System cycles per line: 640 uses 320's clock, 704 uses 352's clock
	if hreso&0x01 != 0 {
		v.systemCyclesPerLine = systemCyclesPerLine352
		v.activeSystemCycles = activeSystemCycles352
	} else {
		v.systemCyclesPerLine = systemCyclesPerLine320
		v.activeSystemCycles = activeSystemCycles320
	}

	// Interlace mode from LSMD bits 7:6
	lsmd := (tvmd >> 6) & 0x03
	v.interlace = uint8(lsmd)
	if lsmd == 1 {
		v.interlace = 0 // prohibited setting, treat as non-interlace
	}

	// Vertical resolution from VRESO bits 5:4
	switch (tvmd >> 4) & 0x03 {
	case 0:
		v.activeLines = 224
	case 1:
		v.activeLines = 240
	case 2:
		v.activeLines = 256
	default:
		v.activeLines = 224
	}

	if v.pal {
		v.linesPerFrame = linesPAL
	} else {
		v.linesPerFrame = linesNTSC
	}
}
