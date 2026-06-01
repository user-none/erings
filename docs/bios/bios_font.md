# Saturn Boot Library Font

The bitmap font the BIOS uploads to VDP2 VRAM during the security-screen
boot phase. It is what the IP / SYS_SEC uses to render the copyright
text, the region strings ("For USA and CANADA.", etc.), and any other
8-pixel-wide ASCII / Latin-1 text drawn during boot via the BIOS
helpers sub_511C and $0050A0 (see
[text_drawers.md](text_drawers.md)).

## Source

- Compressed source: BIOS ROM offset `$005240`, 1345 bytes.
- LZSS header `$1001` (compressed flag bit 0 set), block count 311.
- Decompressed by `sub_1F04` into Work RAM-H scratch at `$06000E20`.
- Loop bound in `sub_5000` reads exactly `$1000` (= 4096) source bytes
  from the scratch; this is the source-of-truth size for the font
  data even though `sub_1F04` may write fewer bytes than that (the
  trailing bytes are whatever residue is in WRAM-H scratch at the
  time `sub_5000` runs).
- Verified identical across USA (`BTR_1.000U941115`) and JP
  (`BTR_1.0191941228`) BIOS variants: the 1346-byte compressed body
  at `$5240` and the 4096-byte decompressed output match byte-for-byte
  between the two regions. The IP-side security screen uses only
  region-neutral text (English copyright string, fixed area-code
  strings), so the font does not need region-specific glyphs.

## Format

| Field | Value |
|---|---|
| Bits per pixel | 1 (monochrome) |
| Glyph width | 8 pixels |
| Glyph height | 16 pixels |
| Bytes per row | 1 |
| Bytes per glyph | 16 |
| Number of glyphs | 256 (indexed 0-255) |
| Total bytes | 4096 |
| Character encoding | ISO-8859-1 (Latin-1) |

Each glyph occupies 16 consecutive bytes. Byte N of a glyph encodes
row N of that glyph (top to bottom). Within a byte, bit 7 (MSB) is the
leftmost pixel and bit 0 (LSB) is the rightmost pixel. A set bit is
foreground (color $D after sub_5000 expansion), a cleared bit is
background (color $0).

Glyph N lives at byte offset `N * 16` in the font buffer.

## Pixel encoding example

Glyph for ASCII `A` ($41 = 65), rendered with `#` for set bits and `.`
for clear:

```
........  byte 0 = $00
...##...  byte 1 = $18
..#..#..  byte 2 = $24
.#....#.  byte 3 = $42
.#....#.  byte 4 = $42
.#....#.  byte 5 = $42
.#....#.  byte 6 = $42
.######.  byte 7 = $7E
.#....#.  byte 8 = $42
.#....#.  byte 9 = $42
.#....#.  byte 10 = $42
.#....#.  byte 11 = $42
.#....#.  byte 12 = $42
.#....#.  byte 13 = $42
.#....#.  byte 14 = $42
........  byte 15 = $00
```

## VDP2 layout convention

After `sub_5000` expands the 1bpp data to 4bpp (one 1bpp byte ->
four 4bpp bytes via the lookup `{$00, $0D, $D0, $DD}`), the result
lands in VDP2 VRAM at `$25E00000`. In NBG0 character mode with 8x8
cells and 16-color (4bpp) palette mode (CHCTLA=0, the configuration
the IP sets up), each 8x16 glyph spans two vertically-adjacent 8x8
cells. The BIOS text drawers ($0050A0 and sub_511C) write character
pattern numbers using the convention:

| Cell position | Pattern number |
|---|---|
| Upper half of glyph N | `2*N` |
| Lower half of glyph N | `2*N + 1` |

So glyph N's two cells are reached by writing the half-pattern
indices `2N` (at the upper tile-map cell) and `2N+1` (one tile-map
row below, = +128 bytes in a 64-cell-wide 1-word pattern-name plane).

## Coverage

The 4096-byte font has 256 glyph slots; 189 are populated, 67 are
blank. The blank regions correspond exactly to the ISO-8859-1
unprintable / undefined positions:

| Index range | Count | Status | ISO-8859-1 meaning |
|---|---|---|---|
| `$00`-`$1F` (0-31) | 32 | blank | C0 control characters |
| `$20` (32) | 1 | blank | space (SP) |
| `$21`-`$7E` (33-126) | 94 | data | printable ASCII `!` through `~` |
| `$7F` (127) | 1 | blank | DEL |
| `$80`-`$9F` (128-159) | 32 | blank | C1 control characters |
| `$A0` (160) | 1 | blank | non-breaking space (NBSP) |
| `$A1`-`$FF` (161-255) | 95 | data | Latin-1 supplement `¡` through `ÿ` |

The Latin-1 supplement coverage services every ISO-8859-1 locale:
USA / Canada, the European-language area-code strings the IP
supports, and accented variants for any name fields the BIOS
surfaces. The same font is shipped by both the USA and the JP BIOS
variants (the compressed body at `$5240` is byte-identical between
them); the IP-side security screen draws only region-neutral text,
so no region-specific glyphs are needed at this surface.

## Character map

### ASCII range `$21`-`$7E` (printable)

| Idx | Hex | Char | | Idx | Hex | Char | | Idx | Hex | Char | | Idx | Hex | Char |
|----:|----:|:-----|-|----:|----:|:-----|-|----:|----:|:-----|-|----:|----:|:-----|
| 33 | `$21` | `!` | | 57 | `$39` | `9` | | 81 | `$51` | `Q` | | 105 | `$69` | `i` |
| 34 | `$22` | `"` | | 58 | `$3A` | `:` | | 82 | `$52` | `R` | | 106 | `$6A` | `j` |
| 35 | `$23` | `#` | | 59 | `$3B` | `;` | | 83 | `$53` | `S` | | 107 | `$6B` | `k` |
| 36 | `$24` | `$` | | 60 | `$3C` | `<` | | 84 | `$54` | `T` | | 108 | `$6C` | `l` |
| 37 | `$25` | `%` | | 61 | `$3D` | `=` | | 85 | `$55` | `U` | | 109 | `$6D` | `m` |
| 38 | `$26` | `&` | | 62 | `$3E` | `>` | | 86 | `$56` | `V` | | 110 | `$6E` | `n` |
| 39 | `$27` | `'` | | 63 | `$3F` | `?` | | 87 | `$57` | `W` | | 111 | `$6F` | `o` |
| 40 | `$28` | `(` | | 64 | `$40` | `@` | | 88 | `$58` | `X` | | 112 | `$70` | `p` |
| 41 | `$29` | `)` | | 65 | `$41` | `A` | | 89 | `$59` | `Y` | | 113 | `$71` | `q` |
| 42 | `$2A` | `*` | | 66 | `$42` | `B` | | 90 | `$5A` | `Z` | | 114 | `$72` | `r` |
| 43 | `$2B` | `+` | | 67 | `$43` | `C` | | 91 | `$5B` | `[` | | 115 | `$73` | `s` |
| 44 | `$2C` | `,` | | 68 | `$44` | `D` | | 92 | `$5C` | `\` | | 116 | `$74` | `t` |
| 45 | `$2D` | `-` | | 69 | `$45` | `E` | | 93 | `$5D` | `]` | | 117 | `$75` | `u` |
| 46 | `$2E` | `.` | | 70 | `$46` | `F` | | 94 | `$5E` | `^` | | 118 | `$76` | `v` |
| 47 | `$2F` | `/` | | 71 | `$47` | `G` | | 95 | `$5F` | `_` | | 119 | `$77` | `w` |
| 48 | `$30` | `0` | | 72 | `$48` | `H` | | 96 | `$60` | `` ` `` | | 120 | `$78` | `x` |
| 49 | `$31` | `1` | | 73 | `$49` | `I` | | 97 | `$61` | `a` | | 121 | `$79` | `y` |
| 50 | `$32` | `2` | | 74 | `$4A` | `J` | | 98 | `$62` | `b` | | 122 | `$7A` | `z` |
| 51 | `$33` | `3` | | 75 | `$4B` | `K` | | 99 | `$63` | `c` | | 123 | `$7B` | `{` |
| 52 | `$34` | `4` | | 76 | `$4C` | `L` | | 100 | `$64` | `d` | | 124 | `$7C` | `|` |
| 53 | `$35` | `5` | | 77 | `$4D` | `M` | | 101 | `$65` | `e` | | 125 | `$7D` | `}` |
| 54 | `$36` | `6` | | 78 | `$4E` | `N` | | 102 | `$66` | `f` | | 126 | `$7E` | `~` |
| 55 | `$37` | `7` | | 79 | `$4F` | `O` | | 103 | `$67` | `g` | | | | |
| 56 | `$38` | `8` | | 80 | `$50` | `P` | | 104 | `$68` | `h` | | | | |

### Latin-1 supplement `$A1`-`$FF`

| Idx | Hex | Char | Name |
|----:|----:|:-----|:-----|
| 161 | `$A1` | ¡ | inverted exclamation mark |
| 162 | `$A2` | ¢ | cent sign |
| 163 | `$A3` | £ | pound sign |
| 164 | `$A4` | ¤ | currency sign |
| 165 | `$A5` | ¥ | yen sign |
| 166 | `$A6` | ¦ | broken bar |
| 167 | `$A7` | § | section sign |
| 168 | `$A8` | ¨ | diaeresis |
| 169 | `$A9` | © | copyright sign |
| 170 | `$AA` | ª | feminine ordinal indicator |
| 171 | `$AB` | « | left-pointing double angle quotation mark |
| 172 | `$AC` | ¬ | not sign |
| 173 | `$AD` | ­ | soft hyphen |
| 174 | `$AE` | ® | registered sign |
| 175 | `$AF` | ¯ | macron |
| 176 | `$B0` | ° | degree sign |
| 177 | `$B1` | ± | plus-minus sign |
| 178 | `$B2` | ² | superscript two |
| 179 | `$B3` | ³ | superscript three |
| 180 | `$B4` | ´ | acute accent |
| 181 | `$B5` | µ | micro sign |
| 182 | `$B6` | ¶ | pilcrow sign |
| 183 | `$B7` | · | middle dot |
| 184 | `$B8` | ¸ | cedilla |
| 185 | `$B9` | ¹ | superscript one |
| 186 | `$BA` | º | masculine ordinal indicator |
| 187 | `$BB` | » | right-pointing double angle quotation mark |
| 188 | `$BC` | ¼ | one quarter |
| 189 | `$BD` | ½ | one half |
| 190 | `$BE` | ¾ | three quarters |
| 191 | `$BF` | ¿ | inverted question mark |
| 192 | `$C0` | À | A with grave |
| 193 | `$C1` | Á | A with acute |
| 194 | `$C2` | Â | A with circumflex |
| 195 | `$C3` | Ã | A with tilde |
| 196 | `$C4` | Ä | A with diaeresis |
| 197 | `$C5` | Å | A with ring above |
| 198 | `$C6` | Æ | AE ligature |
| 199 | `$C7` | Ç | C with cedilla |
| 200 | `$C8` | È | E with grave |
| 201 | `$C9` | É | E with acute |
| 202 | `$CA` | Ê | E with circumflex |
| 203 | `$CB` | Ë | E with diaeresis |
| 204 | `$CC` | Ì | I with grave |
| 205 | `$CD` | Í | I with acute |
| 206 | `$CE` | Î | I with circumflex |
| 207 | `$CF` | Ï | I with diaeresis |
| 208 | `$D0` | Ð | Eth |
| 209 | `$D1` | Ñ | N with tilde |
| 210 | `$D2` | Ò | O with grave |
| 211 | `$D3` | Ó | O with acute |
| 212 | `$D4` | Ô | O with circumflex |
| 213 | `$D5` | Õ | O with tilde |
| 214 | `$D6` | Ö | O with diaeresis |
| 215 | `$D7` | × | multiplication sign |
| 216 | `$D8` | Ø | O with stroke |
| 217 | `$D9` | Ù | U with grave |
| 218 | `$DA` | Ú | U with acute |
| 219 | `$DB` | Û | U with circumflex |
| 220 | `$DC` | Ü | U with diaeresis |
| 221 | `$DD` | Ý | Y with acute |
| 222 | `$DE` | Þ | Thorn |
| 223 | `$DF` | ß | sharp s |
| 224 | `$E0` | à | a with grave |
| 225 | `$E1` | á | a with acute |
| 226 | `$E2` | â | a with circumflex |
| 227 | `$E3` | ã | a with tilde |
| 228 | `$E4` | ä | a with diaeresis |
| 229 | `$E5` | å | a with ring above |
| 230 | `$E6` | æ | ae ligature |
| 231 | `$E7` | ç | c with cedilla |
| 232 | `$E8` | è | e with grave |
| 233 | `$E9` | é | e with acute |
| 234 | `$EA` | ê | e with circumflex |
| 235 | `$EB` | ë | e with diaeresis |
| 236 | `$EC` | ì | i with grave |
| 237 | `$ED` | í | i with acute |
| 238 | `$EE` | î | i with circumflex |
| 239 | `$EF` | ï | i with diaeresis |
| 240 | `$F0` | ð | eth |
| 241 | `$F1` | ñ | n with tilde |
| 242 | `$F2` | ò | o with grave |
| 243 | `$F3` | ó | o with acute |
| 244 | `$F4` | ô | o with circumflex |
| 245 | `$F5` | õ | o with tilde |
| 246 | `$F6` | ö | o with diaeresis |
| 247 | `$F7` | ÷ | division sign |
| 248 | `$F8` | ø | o with stroke |
| 249 | `$F9` | ù | u with grave |
| 250 | `$FA` | ú | u with acute |
| 251 | `$FB` | û | u with circumflex |
| 252 | `$FC` | ü | u with diaeresis |
| 253 | `$FD` | ý | y with acute |
| 254 | `$FE` | þ | thorn |
| 255 | `$FF` | ÿ | y with diaeresis |

## Sample glyph bitmaps

A handful of glyphs rendered at full bit resolution for verification.
`#` = set pixel (foreground), `.` = clear pixel (background).

### `$30` (`0`)

```
........
...##...
..#..#..
.#....#.
.#....#.
.#....#.
.#....#.
.#....#.
.#....#.
.#....#.
.#....#.
.#....#.
.#....#.
..#..#..
...##...
........
```

### `$53` (`S`)

```
........
..####..
.#....#.
.#....#.
.#......
..#.....
...#....
....#...
.....#..
......#.
......#.
.#....#.
.#....#.
.#....#.
..####..
........
```

### `$61` (`a`)

```
........
........
........
........
........
........
........
.####...
#....#..
.....#..
.###.#..
#...##..
#....#..
#...##..
.###..#.
........
```

### `$A5` (`¥`)

```
........
#.....#.
.#...#..
..#.#...
...#....
...#....
#######.
...#....
...#....
...#....
#######.
...#....
...#....
...#....
...#....
........
```

The full bitmap for any specific glyph can be inspected by reading
the 16 bytes at offset `index * 16` in the 4096-byte buffer that
`sub_1F04` produces at `$06000E20` when decompressing the compressed
body at BIOS `$5240`, and applying the row/bit encoding above.

## Decompression and upload routines (sub_50EA + sub_5000)

The IP reaches this font path via the BIOS-ROM public routine table:
`mem.L[$05E4] = sub_50EA = $000050EA`. The font upload happens during
the IP's security-screen setup; the routines have no caller-passed
arguments.

### sub_50EA — decompress + upload trampoline

sub_50EA takes no caller arguments. All addresses are BIOS literals
in its own constant pool; whatever the caller has in R4/R5/R6 at
entry is unused and immediately clobbered.

- **Entry**: BIOS $0050EA (published in BIOS-ROM routine table at $0005E4)
- **Body**: $0050EA..$00510A (32 bytes; 4-byte stack frame for the
  in/out dest_ptr slot)
- **Pool** ($510E..$511B):

| Offset | Value | Purpose |
|--------|-------|---------|
| $510E | $05F8 | BIOS WRAM-L slot holding the sub_1F04 function pointer |
| $5110 | $1000 | decompression size cap |
| $5114 | $06000E20 | font scratch destination in WRAM-H |
| $5118 | $00005240 | LZSS source (compressed font body) |

```python
def sub_50EA():
    # No caller arguments — all addresses are BIOS literals in the
    # constant pool. R4/R5/R6 at entry are unused and clobbered.
    dest_ptr = 0x06000E20                   # in/out longword (sub_1F04 advances it)
    sub_1F04 = mem.L[0x000005F8]            # = 0x00001F04 (BIOS LZSS entry)
    sub_1F04(src      = 0x00005240,
             dest_ptr = &dest_ptr,
             max_size = 0x1000)
    sub_5000()                              # 1bpp -> 4bpp expander
```

### sub_5000 — 1bpp → 4bpp expander

sub_5000 also has no parameters. Loop count, source, destination, and
lookup-table base are all literals in the pool at the end of its body.

- **Entry**: BIOS $005000 (BSR-only from sub_50EA)
- **Pool** ($005092..$00509F):

| Offset | Value | Purpose |
|--------|-------|---------|
| $5092 | $1000 | loop count (1bpp source bytes) |
| $5094 | $25E00000 | VDP2 VRAM dest (cache-disabled mirror) |
| $5098 | $06000E20 | 1bpp source buffer (font scratch) |
| $509C | $00005782 | bit-pair → output-byte lookup table |

```python
def sub_5000():
    # No caller arguments — all values come from the literal pool.
    # Expands 4096 bytes of 1bpp glyph data into 16384 bytes of 4bpp
    # VDP2 character pattern data, two pixels per source bit-pair.
    src  = 0x06000E20
    dest = 0x25E00000
    for _ in range(0x1000):                     # 4096 source bytes
        b = mem.B[src]; src += 1
        for shift in (6, 4, 2, 0):              # MSB pair first
            pair = (b >> shift) & 0x3
            mem.B[dest] = mem.B[0x00005782 + pair]
            dest += 1
    # Output: 16384 bytes at $25E00000..$25E03FFF (4 dest bytes per source byte)
```

Lookup at $005782 = `{ $00, $0D, $D0, $DD }`. Each 4bpp output byte
encodes two pixels (high nibble = left, low nibble = right): bit-pair
00 → $00 (two color-0 pixels), 01 → $0D, 10 → $D0, 11 → $DD. Result is
4bpp character pattern data with color-0 background and color-D
foreground.

The earlier measurement of "3765 bytes decompressed" is not consistent
with a captured WRAM-H dump: the region $06001CD5..$06001E1F (offsets
3765..4095 past the buffer head) is not zero in the dump and contains
font-glyph-shaped data (rows like $38 $44 $82 $FE $80 $80 $42 $3C).
Two readings fit:

  - The actual decompressed output is closer to or equal to 4096 bytes
    (the sub_1F04 R6 cap), not 3765. The 3765 figure may have come from
    counting only up to the first long run of zeros.
  - The decompressed output is 3765 bytes and the trailing 331 bytes
    of scratch were populated by other BIOS code before the dump was
    captured.

The correct length is the value sub_1F04 leaves in the dest_ptr slot
(sp[0] after the JSR in sub_50EA); confirming that requires either an
instrumented run or a reference re-implementation of sub_1F04.

The glyph format (1bpp, 8x16, 16 bytes per glyph, ISO-8859-1) is
documented in the "Format" and "Character map" sections above.

## Font and Graphics Data

The $005300-$006F00 region (~7 KB) contains font tiles and graphics
used exclusively by BIOS screens (Saturn logo, CD player, system
settings). This data includes:

- Character glyphs for text display
- UI element tiles for the CD player interface
- "PRODUCED BY or UNDER LICENSE FROM" text at $00686C
- "SEGA ENTERPRISES,LTD." text at $00688D

The $009000-$01D000 region (~80 KB) contains the Saturn logo animation
frame data and additional tile graphics for menu screens.

Games do not access BIOS font data through any published API. The
Saturn has no game-callable font service; titles ship their own font
data.
