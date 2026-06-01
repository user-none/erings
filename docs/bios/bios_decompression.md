# BIOS Decompression (sub_1F04)

sub_1F04 at BIOS `$00001F04` is the only LZSS decompression entry
in the BIOS. All three compressed bodies in the ROM (font at
`$5240`, boot library at `$7000`, CD-block firmware at `$1D000`)
expand through it. This file documents the LZSS format,
sub_1F04's calling convention, and the load sites that invoke
it.

### BIOS Load / Decompression Mechanism

#### LZSS decompressor (sub_1F04)

The only general-purpose decompression entry in the BIOS. All three
compressed bodies and any future game-issued decompression go through
this routine.

| Reg | Use |
|-----|-----|
| R4 | Source address (compressed data in BIOS ROM) |
| R5 | Pointer to a longword that holds the destination address |
| R6 | Maximum output size |

Format details (header, block layout, token encoding, reference types)
are documented in "BIOS Decompression Format (sub_1F04)".

#### BIOS-driven loads during boot

Two of the three compressed bodies are loaded automatically by the
BIOS before game code runs. The game does not request these:

1. **CD Block firmware** ($01D000 -> $06010000). Loaded on the CD-game
   boot path, max-output cap $00040000 (generous upper bound; actual
   decompressed size is determined by the stream content).
   Unconditional on this path.
2. **Boot library** ($007000 -> $06010000). Loaded on the no-game /
   fall-through path, max-output cap $00040000 (same generous bound).
   Drives the Saturn logo, audio CD player, and system-settings
   screens.

Both target the same Work RAM-H region; only one is resident at a time
because the boot library path and the CD-game path are mutually
exclusive.

#### Game-driven loads after handoff

The backup library at $005240 is not decompressed by the BIOS during
boot. Game code must trigger its decompression on demand. Static
disassembly has not yet identified the BIOS-ROM entry that performs
this load (its calling convention is presumably "destination address
+ work buffer" since the body is 16 KB and game code chooses where
it lands).

A separate Work RAM-H slot at $06000300 is used by the boot library as
an indirect-call target during the Saturn logo animation (R4 selects an
entry; observed values 64 and 65 correspond to SCU V-Blank IN / OUT
vector numbers). The layout of the table behind $06000300 and whether
game code uses it after handoff is undocumented; see "Key State
Variables" under Saturn Logo Animation in boot_library.md.

## BIOS Decompression Format (sub_1F04)

sub_1F04 is the BIOS decompression entry. It reads a 2-byte header to
determine raw vs compressed, then dispatches to a worker that handles
either case. The compressed format is an LZSS variant with a 16-bit
selector per block, three reference types, and a trailing tail block.

Format details below are verified against the BIOS code itself
(sub_1F04, worker sub_1E0C, compressed-block handler sub_1F60, length
helper sub_2522, literal helper sub_252A).

### Calling Convention

```
R4 = source address (compressed data in BIOS ROM)
R5 = pointer to a longword that holds the destination address
R6 = max output size (bytes)
```

R5 is dereferenced. The current destination address is read from the
longword at R5; output bytes are written there and the longword is
rewritten to point at the next byte position. The caller's "current
dest" therefore advances in lockstep with the output.

Post-conditions on return:

- `R0 = 0` indicates success. The longword at R5 holds
  `original_dest + decompressed_size` (i.e. one past the last written
  byte, after the tail-truncation step).
- `R0 = 1` indicates failure. See "Edge cases" under Compressed Stream
  Structure for the conditions that raise it. The longword at R5 holds
  the position reached when the error was detected; any output already
  written before that point remains in the destination buffer.

In all cases the destination buffer is written only within the
half-open range `[original_dest, original_dest + R6)`. Per-token byte
counts are truncated against the remaining room so the buffer is
never overrun. A stream that demands more bytes than `R6` allows
returns failure once the cap is reached; the bytes already written
up to that point remain in the buffer.

### Compressed-Body Loader Sites in BIOS

Only two sub_1F04 invocations exist in the BIOS, both reachable from
boot:

| Called From | Source | Dest | Max Size |
|-------------|--------|------|----------|
| sub_173C ($00173C) | $007000 | $06010000 | $00040000 (256 KB cap) |
| sub_15D4 ($0015D4) | $01D000 | $06010000 | $00040000 (256 KB cap) |

Both pull pool entries from $17B8/$17BC/$17C0/$17C4/$17C8. The cap is
a generous upper bound; the actual decompressed size of each body is
determined by its stream content.

A third caller exists outside the BIOS: the disc's IP/SYS_SEC code
reaches sub_1F04 via the BIOS routine table slot at $0005E4
(sub_50EA, which sets up R4=$005240/R5=&$06000E20/R6=$1000 and
JSRs into LZSS). $005240 IS a compressed body - the boot-library
bitmap font - but only external (disc-side) code decompresses it,
so a BIOS-internal grep finds no callers. See "Boot Library Font"
section for details.

### Stream Header (2 bytes, big-endian)

| Bits | Field |
|------|-------|
| 0 | Type: 0 = raw transfer, 1 = compressed |
| 15-1 | Unused for compressed bodies (reserved / arbitrary) |

The three compressed bodies in the BIOS all have header = `$1001`
(bit 0 set, upper bits = `$1000`). The dispatcher at $1F4A only checks
that the AND-1 result equals 0 (raw) or 1 (compressed); other values
return immediately.

### Compressed Stream Structure

After the 2-byte header:

```
[2: block_count]                       Number of full blocks
[block_count x 34-byte main blocks]    Each block = selector + 16 tokens
[1: trailing_truncation_count]         Bytes to chop off output tail
[1: trailing_block_token_count]        0 = no tail block, 1-16 = run one more block
[optional trailing block]              Same format as main, with this many tokens
```

block_count is a 16-bit big-endian word.

After the `block_count` main blocks, the worker reads the two-byte
trailing pair. If `trailing_block_token_count >= 1`, one more block is
processed with that many tokens (same block layout as main blocks).
The output is then trimmed by `trailing_truncation_count` bytes: the
destination longword at R5 is decremented by that count. This lets
the encoder produce output that ends partway through a token without
padding.

This is a single tail block, not a repeating loop.

#### Edge cases

The trailing pair is read unconditionally on every compressed stream;
its bytes are interpreted as follows:

- `trailing_block_token_count` is read as a signed 8-bit value. Values
  `1..127` enable the tail block; the byte determines its token count.
  Valid encoders only use `0..16`. Values `0` and `>= 0x80` (negative
  when sign-extended) skip the tail block.
- `trailing_truncation_count` is applied even when the tail block is
  skipped. The destination pointer is rewound by that many bytes; the
  rewound bytes remain in the buffer but are excluded from the final
  output range.

Failure (R0 = 1) is raised in these cases:

- `block_count == 0`. The function returns failure regardless of the
  trailing pair. Every valid compressed body in the BIOS has
  `block_count >= 1`.
- A token requires output bytes that would advance the destination
  past `original_dest + R6`. The buffer is never written past that
  bound; failure may be returned after some bytes are already in the
  buffer.

### Block Format (main and tail)

Each block has the same layout:

```
[2: selector]       Big-endian. Bit N (LSB-first) describes token N.
[2: token_0]        ...
[2: token_1]        ...
...                 EXACTLY N tokens where N = R7 input to handler
                    (16 for main blocks, byte1 of trailing pair for tail)
```

The block handler at sub_1F60 unconditionally reads N tokens. There is
no early termination based on selector bits.

The selector is scanned from bit 0 to bit 15. The bit pattern lookup
table is at Work RAM-H `$06000380` (populated by Phase 4 copy from
BIOS `$000980`), holding 16 words `$0001, $0002, $0004, ..., $8000`.

Selector bit clear (`= 0`) -> back-reference.
Selector bit set   (`= 1`) -> literal.

### Token Encoding

**Literal (selector bit = 1):**

Writes BOTH bytes of the 16-bit token to output: token's high byte
first (at current dest), then token's low byte (at current dest + 1).
Dest advances by 2.

Implemented at sub_1F60: $1FE4 writes the high byte
(`token >> 8`, produced by sub_252A); $1FFE writes the low byte
(`token & $FF`).

**Reference (selector bit = 0):**

The top 4 bits of the token select one of three reference types:

| Top nibble (token & $F000) | Type | Length | Offset (from dest) |
|----------------------------|------|--------|---------------------|
| $0000 | Short match (RLE of previous byte) | `(token & $0FFF) + 3` | 1 |
| $1000 | Long match (extra length byte) | `next_byte + 17` | `token & $0FFF` |
| Everything else ($2000-$FFFF) | Standard match | `(token >> 12) + 1` (range 3-16) | `token & $0FFF` |

- Short match: replicates the byte just written by the previous output
  step. Length = `(token & $0FFF) + 3`, so 3-4098 bytes.
- Long match: consumes one additional byte from the stream after the
  token. Length = `extra_byte + 17`, so 17-272 bytes.
- Standard match: length is `(token >> 12) + 1`. sub_2522 performs the
  arithmetic shift (11 SHAR R0 + 1 in the RTS delay slot = right shift
  by 12) and the handler then adds 1 (sub_1F60 $20A6 ADD #1,R4). Range
  is `$2000 -> 3` ... `$F000 -> 16`.

For all three types, the produced length is clamped against
`max_out - current_dest` so the handler never overruns the dest
buffer (sub_1F60 $201A, $2074, $20AE).

### Validated Parameters

| Parameter | Value | Source |
|-----------|-------|--------|
| Window size (max back-offset) | 4095 bytes | `$0FFF` mask at $211C |
| Selector | 16-bit big-endian, LSB-first | bit-pattern table $06000380 |
| Tokens per main block | 16 | R7 = 16 at sub_1E0C $1E52 / $1E7C |
| Tokens per tail block | 1-16 | R7 = trailing byte1 at sub_1E0C $1EB0 |
| Short-match length range | 3 - 4098 | `(token + 3)`, token in $0000-$0FFF |
| Long-match length range | 17 - 272 | `next_byte + 17` |
| Standard-match length range | 3 - 16 | `(token >> 12) + 1`, token in $2000-$FFFF |
| Literal output | 2 bytes per token (hi then lo) | sub_1F60 $1FE4 + $1FFE |

### Validation

A Python implementation of this format successfully decompresses the
boot-library body at $7000. The output begins with the trampoline
sequence `4F 22 D3 01 43 2B 4F 26` (STS.L PR,@-R15; MOV.L pool,R3;
JMP @R3; LDS.L @R15+,PR) - a typical SH-2 function-dispatch table
entry. This confirms the format above produces real SH-2 code.

### Reference Pseudo-code

`src` is treated as a sliding window: `src[i]` reads the byte i ahead
of the current position, and `src += N` drops the leading N bytes
(equivalent to advancing a source pointer in C). Back-references can
reach before the start of the output: the boot-library body does this
early in the stream, while `len(out)` is still smaller than the maximum
back-offset. On real hardware the handler reads RAM at `dest - offset`,
so those reads return whatever the destination buffer was pre-loaded
with - the zero-initialized Work RAM-H region, i.e. 0. The `back`
helper models this by returning 0 for any index before the start.

```
def decompress(src, max_out):
    out = bytearray()
    def back(i):
        # reads before the start of output hit zero-initialized dest RAM
        return out[i] if i >= 0 else 0
    header = read_word_be(src); src += 2
    if header & 1 == 0:
        # raw transfer (not used by any of the three BIOS bodies)
        ...
    block_count = read_word_be(src); src += 2
    if block_count == 0:
        return None  # failure: no main blocks
    def run_block(token_count):
        selector = read_word_be(src); src += 2
        for bit in range(token_count):
            tok_hi = src[0]; tok_lo = src[1]; src += 2
            token = (tok_hi << 8) | tok_lo
            remaining = max_out - len(out)
            if selector & (1 << bit):
                # Literal: write up to 2 bytes, clamped to remaining.
                if remaining >= 1:
                    out.append(tok_hi)
                if remaining >= 2:
                    out.append(tok_lo)
                if remaining < 2:
                    return
            else:
                top = token & 0xF000
                if top == 0x0000:
                    length = (token & 0x0FFF) + 3
                    for i in range(min(length, remaining)):
                        out.append(back(len(out) - 1))
                elif top == 0x1000:
                    extra = src[0]; src += 1
                    length = extra + 17
                    offset = token & 0x0FFF
                    for i in range(min(length, remaining)):
                        out.append(back(len(out) - offset))
                else:
                    length = (token >> 12) + 1
                    offset = token & 0x0FFF
                    for i in range(min(length, remaining)):
                        out.append(back(len(out) - offset))
    for _ in range(block_count):
        run_block(16)
    trunc = src[0]
    tail_tokens = signed_8(src[1])  # signed: 0x80..0xFF treated as negative
    src += 2
    if tail_tokens >= 1:
        run_block(tail_tokens)
    if trunc > 0:
        del out[-trunc:]
    return out
```


## Compressed Library Bodies

Three compressed bodies exist in the BIOS ROM. Sizes are measured by
running the documented decompressor on each body.

### $005240 - Font / Glyph Bitmaps

- Header: $1001 at $5240, block_count: 39
- Compressed size: ~1.3 KB (consumed by decompressor before reaching
  the trailing-byte pair)
- Decompressed size: 4,096 bytes ($1000)
- Loader: none in BIOS code. No static disassembly site calls sub_1F04
  with source $5240.

Contents: 8-pixel-wide monochrome glyph bitmaps. Sample at +$237:
`44 FE 44 44 44 44 44 44 FE 44` is a letter-shape pattern. Used by
BIOS screens (Saturn logo, audio CD player, system settings) to render
text.

### $007000 - Boot Library

- Header: $1001 at $7000, block_count: 2534
- Compressed size: ~88 KB (BIOS bytes $7000 through ~$1CA00)
- Decompressed size: 180,224 bytes ($2C000); the `R6 = $00040000`
  cap is a generous upper bound the stream stays under
- Loader: sub_173C ($00173C), called on the no-game / fall-through
  boot path

Contents: Saturn logo animation, audio CD player, system-settings
screens, backup-RAM memory-manager UI. The decompressed output starts
with an SH-2 trampoline at $06010000
(`STS.L PR,@-R15; MOV.L pool,R3; JMP @R3; LDS.L @R15+,PR`) that
dispatches to $0601066C.

### $01D000 - CD Block Firmware

- Header: $1001 at $1D000, block_count: 827
- Compressed size: ~28 KB
- Decompressed size: 59,136 bytes ($E700); the `R6 = $40000` cap from
  sub_15D4 is a generous upper bound, not the actual decompressed length
- Loader: sub_15D4 ($0015D4), called on the CD-game boot path

Contents: CD Block filesystem code - directory traversal, file lookup,
sector-transfer management. Decompressed to $06010000, overwriting any
prior boot-library copy that may have been loaded.

The CD Block firmware drives the CR1-CR4 register interface documented
in [cd_block_interface.md](cd_block_interface.md). The firmware itself
is regular SH-2 code that runs out of WRAM-H once decompressed.
