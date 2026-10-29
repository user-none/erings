// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package screen

import "github.com/user-none/erings/core/compute"

// Shadow types of a sprite pixel (Sec 14).
const (
	ShadowNone      = 0
	ShadowNormal    = 1 // darkens scroll screens and the back screen per SDCTL
	ShadowMSBSprite = 2 // a sprite pixel displayed with a shadow added
	ShadowMSBTransp = 3 // transparent dot projecting a shadow per SDCTL
	ShadowMSBNull   = 4 // transparent dot, no shadow (TPSDSL=0)
)

// spriteTypeLayout gives, per sprite type (SPCTL bits 3:0), the bit
// positions of the priority, color calculation, and dot color fields
// of a palette-format framebuffer pixel (Figure 9.1), packed as
// prShift | prBits<<4 | ccShift<<8 | ccBits<<12 | dcBits<<16 |
// msb<<20. A zero bit count means the type has no such field; msb is
// the dot color field's MSB, the SPCCCS mode 3 condition.
var spriteTypeLayout = [16]uint32{
	14 | 2<<4 | 11<<8 | 3<<12 | 11<<16 | 10<<20, // 0: 2PR 3CC 11DC
	13 | 3<<4 | 11<<8 | 2<<12 | 11<<16 | 10<<20, // 1: 3PR 2CC 11DC
	14 | 1<<4 | 11<<8 | 3<<12 | 11<<16 | 10<<20, // 2: SD 1PR 3CC 11DC
	13 | 2<<4 | 11<<8 | 2<<12 | 11<<16 | 10<<20, // 3: SD 2PR 2CC 11DC
	13 | 2<<4 | 10<<8 | 3<<12 | 10<<16 | 9<<20,  // 4: SD 2PR 3CC 10DC
	12 | 3<<4 | 11<<8 | 1<<12 | 11<<16 | 10<<20, // 5: SD 3PR 1CC 11DC
	12 | 3<<4 | 10<<8 | 2<<12 | 10<<16 | 9<<20,  // 6: SD 3PR 2CC 10DC
	12 | 3<<4 | 9<<8 | 3<<12 | 9<<16 | 8<<20,    // 7: SD 3PR 3CC 9DC
	7 | 1<<4 | 0<<8 | 0<<12 | 7<<16 | 6<<20,     // 8: 1PR 7DC
	7 | 1<<4 | 6<<8 | 1<<12 | 6<<16 | 5<<20,     // 9: 1PR 1CC 6DC
	6 | 2<<4 | 0<<8 | 0<<12 | 6<<16 | 5<<20,     // A: 2PR 6DC
	0 | 0<<4 | 6<<8 | 2<<12 | 6<<16 | 5<<20,     // B: 2CC 6DC
	7 | 1<<4 | 0<<8 | 0<<12 | 8<<16 | 7<<20,     // C: PR0 shared with DC7
	7 | 1<<4 | 6<<8 | 1<<12 | 8<<16 | 7<<20,     // D: PR0 and CC0 shared with DC7:6
	6 | 2<<4 | 0<<8 | 0<<12 | 8<<16 | 7<<20,     // E: PR1:0 shared with DC7:6
	0 | 0<<4 | 6<<8 | 2<<12 | 8<<16 | 7<<20,     // F: CC1:0 shared with DC7:6
}

// SpriteRegs carries the registers the sprite decode derives from.
type SpriteRegs struct {
	SPCTL  uint32
	CRAOFB uint32
	PRIS   [4]uint32 // PRISA-D
	CCRS   [4]uint32 // CCRSA-D
	CCCTL  uint32
	SDCTL  uint32
}

// SpriteDecode is a row's sprite decode: everything the per-pixel
// sprite path derives from registers, resolved once by
// BuildSpriteDecode. Per pixel only the framebuffer value varies.
type SpriteDecode struct {
	Type    uint32
	Mixed   bool // SPCLMD: MSB-set pixels are RGB
	SPWinEn bool // SPWINEN: the MSB is the sprite window bit (types 2-7)

	// Palette-format field extraction.
	PRShift, CCShift uint32
	PRMask, CCMask   uint32
	DCMask, MSBBit   uint32
	CRAMOff          uint32 // sprite CRAM offset (SPCAOS) in entries

	RGBPri uint32    // priority of an RGB pixel: PRISA low field
	Pri    [8]uint32 // priority per PR value (PRISA-D fields)
	Ratio  [8]uint32 // color calculation ratio per CC value (CCRSA-D)

	// Color calculation condition: SPCCCS/SPCCN against the resolved
	// priority (CCByPri) or the dot color MSB (CCMode3), both false
	// when CCCTL disables sprite color calculation.
	CCMode3 bool
	CCByPri [8]bool

	// Shadow rules (Sec 14).
	MSBShadow bool // MSB shadow exists: types 2-7 outside mixed mode
	TPSDSL    bool // SDCTL TPSDSL: MSB transparent shadow projects
}

// pairedField reads the sub-field selected by a sprite PR or CC bit
// value from four consecutive registers (PRISA-D or CCRSA-D): sel>>1
// picks the register, sel&1 the low or high byte's field, per Table
// 9.2.
func pairedField(regs [4]uint32, sel uint32, mask uint32) uint32 {
	reg := regs[sel>>1&3]
	if sel&1 == 0 {
		return reg & mask
	}
	return (reg >> 8) & mask
}

// BuildSpriteDecode resolves the sprite decode from the registers.
func BuildSpriteDecode(r SpriteRegs) SpriteDecode {
	var d SpriteDecode
	d.Type = r.SPCTL & 0x0F
	d.Mixed = r.SPCTL&(1<<5) != 0
	d.SPWinEn = r.SPCTL&(1<<4) != 0
	l := spriteTypeLayout[d.Type]
	d.PRShift = l & 0x0F
	d.PRMask = 1<<(l>>4&0x0F) - 1
	d.CCShift = l >> 8 & 0x0F
	d.CCMask = 1<<(l>>12&0x0F) - 1
	d.DCMask = 1<<(l>>16&0x0F) - 1
	d.MSBBit = 1 << (l >> 20 & 0x0F)
	d.CRAMOff = ((r.CRAOFB >> 4) & 0x07) * 256
	d.RGBPri = r.PRIS[0] & 0x07
	for i := range uint32(8) {
		d.Pri[i] = pairedField(r.PRIS, i, 0x07)
		d.Ratio[i] = pairedField(r.CCRS, i, 0x1F)
	}
	if r.CCCTL&(1<<6) != 0 {
		spcccs := (r.SPCTL >> 12) & 0x03
		spccn := (r.SPCTL >> 8) & 0x07
		d.CCMode3 = spcccs == 3
		for p := range uint32(8) {
			switch spcccs {
			case 0:
				d.CCByPri[p] = p <= spccn
			case 1:
				d.CCByPri[p] = p == spccn
			case 2:
				d.CCByPri[p] = p >= spccn
			}
		}
	}
	// With the sprite window enabled the MSB of types 2-7 is the
	// window bit, and MSB shadow cannot be used (SPCTL SPWINEN
	// description, Sec 14.1 MSB Shadow).
	d.MSBShadow = !d.Mixed && !d.SPWinEn && d.Type >= 2 && d.Type <= 7
	d.TPSDSL = r.SDCTL&(1<<8) != 0
	return d
}

// Shadow classifies a sprite pixel's shadow type. Normal shadow
// (Figure 14.3) is judged from the dot color field (Figure 9.1):
// every bit set except the LSB. MSB shadow applies only to types 2-7
// with the MSB set while the sprite window is off (MSBShadow). Normal
// shadow takes precedence over MSB shadow per Sec 14.
//
// In SPCLMD=1 mixed mode bit 15 is the format discriminator (1=RGB,
// 0=palette), not the shadow bit: per Sec 9.1 an RGB pixel's shadow
// bits are considered 0, so it never shadows, while a palette pixel
// still matches the normal shadow pattern (the mixed-mode "MSB as 0"
// rule suppresses only the MSB shadow bit, not the DC field).
//
// MSB transparent shadow (bits 14:0 zero) is always a transparent
// dot (Sec 14.1, Figure 14.4); TPSDSL only selects whether it
// projects a shadow on the screens below. With TPSDSL=0 the shadow is
// nullified but the dot must not become a displayable palette-0
// color.
func (d *SpriteDecode) Shadow(pixel uint32) uint32 {
	if pixel == 0 {
		return ShadowNone
	}
	if d.Mixed && pixel&0x8000 != 0 {
		return ShadowNone
	}
	if pixel&d.DCMask == d.DCMask&^1 {
		return ShadowNormal
	}
	if !d.MSBShadow || pixel&0x8000 == 0 {
		return ShadowNone
	}
	if pixel&0x7FFF == 0 {
		if d.TPSDSL {
			return ShadowMSBTransp
		}
		return ShadowMSBNull
	}
	return ShadowMSBSprite
}

// Decode resolves a sprite pixel to its priority, color calculation
// bits, dot color MSB, and decoded color. A zero pixel is
// transparent (priority 0).
//
// When the sprite window is enabled (SPWINEN) the MSB of sprite types
// 2-7 is the sprite-window bit (Sec 8.1, Sprite Window), so a pixel
// whose low 15 bits are all zero is a window mark over a transparent
// dot (Table 4.3, palette dot color 0) and not a dot of its own:
// priority 0 marks it transparent so lower layers show through. This
// holds in mixed mode (SPCLMD=1) too, where an MSB-set pixel is
// otherwise RGB and such a pixel would read as opaque RGB black;
// hardware honors it even though the manual discourages pairing
// SPWINEN with SPCLMD=1.
func (d *SpriteDecode) Decode(pal *compute.Palette, cramMode uint32, pixel uint32) (priority, ccBits uint32, colorMSB bool, rgb uint32) {
	if pixel == 0 {
		return 0, 0, false, 0
	}
	if d.SPWinEn && d.Type >= 2 && d.Type <= 7 && pixel&0x7FFF == 0 {
		return 0, 0, false, 0
	}
	if d.Mixed && pixel&0x8000 != 0 {
		return d.RGBPri, 0, true, RGB555To888(pixel)
	}
	prBits := (pixel >> d.PRShift) & d.PRMask
	ccBits = (pixel >> d.CCShift) & d.CCMask
	colorMSB = pixel&d.MSBBit != 0
	rgb, _ = PaletteColor(pal, cramMode, (pixel&d.DCMask)+d.CRAMOff)
	return d.Pri[prBits], ccBits, colorMSB, rgb
}

// CCEnabled evaluates the SPCCCS condition for a sprite pixel with the
// resolved priority and dot color MSB.
func (d *SpriteDecode) CCEnabled(priority uint32, colorMSB bool) bool {
	if d.CCMode3 {
		return colorMSB
	}
	return d.CCByPri[priority&7]
}

// Sprite frame buffer read modes.
const (
	SpriteReadNone    = 0 // no frame buffer, or the row is past it
	SpriteRead16      = 1 // 16-bit pixels, direct row read
	SpriteRead8       = 2 // 8-bit pixels, direct row read
	SpriteReadRotated = 3 // rotation parameter A drives the read coordinate
)

// SpriteRow describes the VDP1 frame buffer read for one row: the
// mode, the buffer geometry, the row's byte offset for the direct
// reads, whether hi-res halves the column (a 512-wide 16-bit buffer
// under 640 columns), and for the rotated read the coordinate at
// column 0 and its per-column step from rotation parameter A (VDP1
// manual Sec 1.2; rotation is prohibited in hi-res). The coordinate
// is .10 at native and carried at 10 + Shift fraction bits at scale,
// Shift being the scale shift, so the shift down by 10 lands in the
// scaled frame buffer. The step stays native and is applied per
// scaled column. Height is the buffer's physical rows at the scale.
// Doubled is 1 when the buffer holds every row of the doubled Y range
// under double interlace, physical row r at doubled rows 2r and
// 2r + 1: the rotated read then takes doubled row 2r + Parity of the
// physical row r its coordinate lands on, with the scaled sub-row kept.
type SpriteRow struct {
	Mode    uint32
	Width   int32
	Height  int32
	Row     uint32
	Halve   uint32
	Shift   uint32
	Doubled uint32
	Parity  uint32
	Is8bpp  bool
	BaseX   int64
	BaseY   int64
	DX      int64
	DY      int64
}

// SpritePixel16 reads column x of a 16-bit direct-read row: zero past
// the buffer width.
func SpritePixel16(fb compute.VDP1FB, r *SpriteRow, x int32) uint32 {
	x >>= r.Halve
	if x < r.Width {
		return uint32(fb.U16BE(r.Row + uint32(x)*2))
	}
	return 0
}

// SpritePixel8 reads column x of an 8-bit direct-read row.
func SpritePixel8(fb compute.VDP1FB, r *SpriteRow, x int32) uint32 {
	if x < r.Width {
		return uint32(fb.U8(r.Row + uint32(x)))
	}
	return 0
}

// SpritePixelRotated reads column x through the rotated read
// coordinate: zero outside the frame buffer. The coordinate lands on
// a physical row; in a doubled buffer the row read is that row's
// doubled row of the read's parity, the scaled sub-row kept.
func SpritePixelRotated(fb compute.VDP1FB, r *SpriteRow, x int32) uint32 {
	sx := int32((r.BaseX + r.DX*int64(x)) >> 10)
	sy := int32((r.BaseY + r.DY*int64(x)) >> 10)
	if sx < 0 || sx >= r.Width || sy < 0 || sy >= r.Height {
		return 0
	}
	sub := sy & (int32(1)<<r.Shift - 1)
	row := ((sy>>r.Shift)<<r.Doubled+int32(r.Parity))<<r.Shift + sub
	if r.Is8bpp {
		return uint32(fb.U8(uint32(row*r.Width + sx)))
	}
	return uint32(fb.U16BE(uint32(row*r.Width+sx) * 2))
}
