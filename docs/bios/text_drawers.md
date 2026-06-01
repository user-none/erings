# Security-Screen Text Drawers

sub_511C (BIOS `$0000511C`) and `$0050A0` are the BIOS-ROM
routines that paint the IP's copyright / region-code text into the
NBG0 tile map using the font sub_50EA uploaded to VDP2 VRAM. They
appear to be IP/SYS_SEC-specific - no other caller has been
observed in any captured runtime state. See
[bios_font.md](bios_font.md) for the font itself.

## Security-screen text drawers (sub_511C, $0050A0)

The IP calls sub_511C immediately after sub_50EA returns. sub_511C
paints the copyright / region-string text into the NBG0 tile map at
$25E06000+ using character patterns from the font sub_50EA just
uploaded. The routine is published in the BIOS-ROM table at slot
$0005E8; the per-character helper at $0050A0 is BIOS-internal (no
slot, no literal reference to its absolute address anywhere in the
BIOS image, only reachable via PC-relative BSR from within the BIOS).

Both routines appear specific to IP/SYS_SEC usage. No reference to
$0000511C, $000050A0, or the slot $0005E8 (as a longword) was found
outside the IP image in captured WRAM-H dumps. The hardcoded
destinations and layout (described below) only match the NBG0
character-mode geometry the IP sets up for the security screen, so
the routines aren't general-purpose tile-map drawers usable by
arbitrary game code.

### $0050A0 - generic per-character drawer

Walks a null-terminated input string and writes two 1-word pattern-
name cells per character: char*2 at the destination cell (upper half
of the 8x16 glyph) and char*2+1 one tile-map row below (= dest + 128
bytes, since the NBG0 tile map is 64 cells wide in 1-word PN mode).

- **Entry**: BIOS $0050A0
- **Body**: $0050A0..$0050E8 (74 bytes; 12-byte stack frame)
- **Inputs**: R4 = `dest` (NBG0 tile-map ptr, word-aligned);
  R5 = `src` (null-terminated byte string)
- **Pool**: $510C = $0080 (next-tile-map-row stride, in bytes)

```python
def draw_string(dest, src):
    # dest: uint32 (VDP2 VRAM tile-map address, word-aligned)
    # src:  bytes  (null-terminated ASCII)
    while src[0] != 0:
        ch = src[0]; src += 1
        mem.W[dest]       = ch * 2        # upper-half pattern
        mem.W[dest + 128] = ch * 2 + 1    # lower-half pattern (next row down)
        dest += 2                         # next cell column
```

The cell-pattern numbering (char N -> patterns 2N and 2N+1) is the
hardcoded convention that links this helper to the font sub_5000
produces: each 1bpp source byte expands to 4bpp pattern data sized
exactly two 8x8 cells per ASCII glyph, so writing pattern 2N gives
the upper half and 2N+1 the lower half of the glyph for character N.

### sub_511C - 3-call text painter

- **Entry**: BIOS $00511C (published in BIOS-ROM table at slot $0005E8)
- **Body**: $00511C..$0051D8 (190 bytes), plus pool at $0051E0..$0051E7
- **Inputs**: R4 = `string_a` (long first line); R5 = `string_b`
  (short second line)
- **Stack frame** (40 bytes):
    - `sp[36]` cursor into `string_a` (advanced as bytes are copied)
    - `sp[32]` saved `string_b` pointer
    - `sp[30]` region offset (0 or $0100; +$0100 shifts dest down one row)
    - `sp[28]` local-buffer write index
    - `sp[26]` chars to copy this pass (starts at 20, decrements by 8)
    - `sp[24]` per-pass dest offset along the visual row (starts at 0, +=42)
    - `sp[0..23]` local text buffer (20 chars + null)
- **Pool**:
    - $5150 = $0800       region/PAL flag mask
    - $5152 = $0100       region offset when flag is set
    - $5154 = $06000248   address of region/PAL flag word
    - $51E0 = $25E06988   first dest base  (NBG0 row 19, col 4)
    - $51E4 = $25E06B14   second dest base (NBG0 row 22, col 10)
- **Calls**: `$0050A0` (per-string drawer), invoked 3 times total
  (2x first string, 1x second string)

```python
def painter(string_a, string_b):
    # PAL shifts both dest bases down one row (+0x100 bytes).
    region_offset = 0x100 if (mem.L[0x06000248] & 0x800) else 0

    # First string: two passes side-by-side on a single visual row.
    # Pass 1 writes 20 chars at +0; pass 2 writes 12 chars at +42 (= +21 cells).
    dst_offset = 0
    count      = 20
    while count >= 12:
        # Copy `count` bytes from string_a into a local null-terminated buffer
        # (max 20 chars + null = 21 bytes, fits sp[0..23]).
        buf = string_a[:count] + b'\x00'
        string_a += count
        draw_string(dest = 0x25E06988 + region_offset + dst_offset, src = buf)
        dst_offset += 42
        count      -= 8

    # Second string: single draw at the second hardcoded base.
    draw_string(dest = 0x25E06B14 + region_offset, src = string_b)
```

What's caller-provided: the two source string pointers.

What's hardcoded inside the routine: the destination bases
($25E06988 and $25E06B14), the 20+12 character split for the first
string (loop runs exactly twice: 20 chars at offset 0, then 12 chars
at offset +42 = +21 cells side-by-side on the same visual row), and
the per-line +42-byte stride.

The destinations reference NBG0 tile-map cells at row 19, col 4
($25E06988 - $25E06000 = $988 = 19 * 128 + 8) and row 22, col 10
($25E06B14 - $25E06000 = $B14 = 22 * 128 + $14). They only resolve
to visible text when the NBG0 layer is configured as the IP sets it
up: 1-word pattern names at plane $03, 8x8 cells, 4bpp 16-color,
character pattern data at $25E00000 in the font sub_5000 produced.

The character-to-pattern mapping in $0050A0 (char N -> patterns 2N
and 2N+1) is locked to the font layout: each ASCII glyph fills two
vertically-adjacent 8x8 cells (an 8x16 visual character).
