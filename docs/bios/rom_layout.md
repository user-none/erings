# BIOS ROM Layout

Map of what lives where in the 512 KB BIOS ROM image: the SH-2
vector table at the very start, the public routine table at
`$0005D8`, BIOS-resident code that disc/game code can JSR through,
the compressed bodies sub_1F04 decompresses, and a summary of the
key entry points referenced throughout this directory. See
[bios_decompression.md](bios_decompression.md) for the compressed-
body format, [system_services.md](system_services.md) for how the
public-routine-table entries are used.

## ROM Vector Table ($000000-$000187)

The SH-2 fetches exception vectors from address 0. The BIOS ROM vector table
occupies the first 388 bytes. After VBR is relocated to Work RAM-H, these are
only used for power-on/manual reset.

### Exception Vectors ($000000-$0000BF)

| Offset | Vector | Value | Target | Purpose |
|--------|--------|-------|--------|---------|
| $000000 | 0 | $20000200 | $000200 | Power-on PC (entry point) |
| $000004 | 1 | $06002000 | - | Power-on SP (top of Work RAM-H) |
| $000008 | 2 | $20000200 | $000200 | Manual reset PC |
| $00000C | 3 | $06002000 | - | Manual reset SP |
| $000010 | 4 | $20000222 | $000222 | General illegal instruction |
| $000014 | 5 | $20000226 | $000226 | Reserved (default) |
| $000018 | 6 | $20000222 | $000222 | Slot illegal instruction |
| $00001C | 7 | $20000226 | $000226 | Reserved (default) |
| $000020 | 8 | $20000226 | $000226 | Reserved (default) |
| $000024 | 9 | $20000222 | $000222 | CPU address error |
| $000028 | 10 | $20000222 | $000222 | DMA address error |
| $00002C | 11 | $20000534 | $000534 | NMI |
| $000030 | 12 | $20000226 | $000226 | User break |
| $000034-$003C | 13-15 | $20000226 | $000226 | Reserved (default) |

All reserved system vectors (16-31, offsets $000040-$00007C) and all
TRAPA vectors (32-63, offsets $000080-$0000FC) point to $20000226
(default).

All external interrupt vectors (64-97, offsets $000100-$000184) point to
$20000226 (default).

### Default Handlers

| Address | Code | Purpose |
|---------|------|---------|
| $000222 | `BRA $000222` (infinite loop) | Fatal exception handler |
| $000226 | `RTE` | Default ignore handler |

### Accidental `JMP @0` recovery via reset-vector bytes

The reset-vector bytes at $00000000 (`$2000 $0200 $0600 $2000 ...`)
begin with `$2000`, which decodes as the legal instruction
`MOV.B R0,@R0`; the words that follow (`$0200`, `$0600`) are
undefined SH-2 opcodes. They're harmless when executed because the
register state at the moment a stray `JMP @0` lands here typically
has R0=0, so the byte-write `MOV.B R0,@R0` targets ROM (no
effect), and PC walks forward through more benign reset-vector
bytes until it eventually hits an RTS or an exception path that
unwinds back to the caller. Some game code relies on this
accidental recovery — for example NiGHTS' task dispatcher at
$0606C36A does `JMP @(state<<4)` and runs with state=0 for 20+
consecutive calls during a re-entrant init loop while the parent
state machine waits for a VBLANK to advance things. Each `JMP @0`
walks through the reset vector bytes and eventually returns; the
dispatcher fires again, and the loop unwinds once VBLANK fires
and the game's VBI handler advances state.

The practical effect of the reset-vector bytes when reached via
`JMP @0` is "return to caller eventually" - SH-2 keeps decoding
those bytes as instructions, and the trailing reset PC vector
($20000200) ends up redirecting execution back toward the boot
ROM where the next instruction encountered eventually returns.

### Compressed Bodies in BIOS ROM

The BIOS ROM contains four LZSS-compressed bodies. Each starts with
a 2-byte header (low bit = compressed flag); the format is documented
in bios_decompression.md. Three of the bodies are decompressed by
sub_1F04 callers in the main BIOS code; the fourth (PER + BUP
driver) is decompressed by its own subsystem.

Sizes below are measured by running the documented decompressor on
the BIOS image (compressed sizes are stream-content lengths; total
disk footprint for the PER/BUP body is bounded by the available
$07D660-$080000 window):

| ROM Offset | Compressed Size | Decompressed Size | Loader | Contents |
|------------|-----------------|-------------------|--------|----------|
| $005240 | ~1.3 KB | ~3.7 KB | none in BIOS internal code; reached externally via routine-table slot $05E4 (sub_50EA) | Font / glyph bitmaps (8-pixel-wide monochrome). |
| $007000 | ~88 KB | determined by stream; cap $40000 (256 KB) | sub_173C, on no-game / fall-through boot path | Boot library: Saturn logo animation, audio CD player, system-settings screens. Decompressed output begins with a function-dispatch table of `STS.L PR,@-R15; MOV.L pool,R3; JMP @R3; LDS.L @R15+,PR` trampolines. |
| $01D000 | ~28 KB | ~57 KB; cap $40000 (256 KB) | sub_15D4, on CD-game boot path | CD Block firmware: filesystem code, directory traversal, file lookup, sector-transfer management. |
| $07D660 | ~10 KB | ~16 KB | PER subsystem (not invoked through sub_1F04 from main BIOS code) | PER + BUP (peripheral / backup-memory) driver. 11-slot function-pointer table at the decompressed base; provides PER_* and BUP_* services. See peripheral_driver.md and backup_library.md. |

### BIOS-Resident Code Exposed to Game Code

The BIOS leaves the following uncompressed routines and data in place
for game code to use after handoff. All addresses are in BIOS ROM
(cacheable $00000000-$0007FFFF or cache-through $20000000-$2007FFFF).

| Address | Purpose |
|---------|---------|
| $000420 | NMI handler used by Work RAM-H VBR (soft reset via BIOS reinit) |
| $000534 | NMI handler used by ROM vector table (hardware reinit path) |
| $0004C8 | SMPC init / VBR setup / slave SH-2 control entry. Also referenced from Work RAM-H slot $06000328. |
| $001F04 | LZSS decompressor (calling convention below) |
| $002F48 | CD Block sector read helper |
| $0029D4 | CD Block command dispatch helper |
| $003338 | CD status read |
| $003B6E | CD status with PERI check |
| $003BC6 | CR1-CR4 consistency read with retry |
| $0042EC | Read CR1-CR4 into buffer |
| $040000 | Disc security check executable (also copied to $06020000 at boot) |
| $040020 | Copyright string ("COPYRIGHT(C) SEGA ENTERPRISES,LTD. 1994 ALL RIGHTS RESERVED") |
| $004DB0 | "CDBLOCK" presence signature (8 bytes used by CDC_SysIsConnect) |
| ~$005780-$006F00 | Uncompressed font tiles, UI glyphs, "PRODUCED BY..." / "SEGA ENTERPRISES,LTD." strings (after the compressed font body at $5240+) |
| $006F00-$007000 | Region text strings ("For JAPAN.", "For USA and CANADA.", etc.) |
| $040400-$07D5FF | Security check authentication data tables (command table at $040400, reference data from $040438; see security_check.md) |
| $07D600 | PER_Init — peripheral subsystem init trampoline (slot $06000358) |
| $07D660 | PER + BUP driver, $1001-header compressed body (block_count $0137). Decompresses to ~16 KB in WRAM-H; provides peripheral input and backup-memory services. See peripheral_driver.md / backup_library.md. |

The above are confirmed by static disassembly. Whether games typically
JSR into these directly, or whether games include their own copies
linked from outside the BIOS, is a separate question and not in scope
for the BIOS inventory.

## Key Addresses Summary

### BIOS ROM Subroutines

| Address | Purpose |
|---------|---------|
| $000200 | Boot entry point |
| $000222 | Fatal exception (infinite loop) |
| $000226 | Default handler (RTE) |
| $0002AC | Fill routine (count, dest from table, fills with R4) |
| $0002BC | Copy routine (count, dest, src from table) |
| $000380 | Cache purge and enable |
| $000420 | NMI handler (soft reset via Work RAM VBR) |
| $000478 | BSC register initialization |
| $0004C8 | SMPC init, VBR setup, slave SH-2 control |
| $000534 | NMI handler (ROM vector, hardware reinit) |
| $001120 | System init main code (runs from Work RAM-H) |
| $001800 | SCU register initialization |
| $001874 | Cartridge/disc header read |
| $0013C0 | VDP1 register initialization |
| $001408 | VDP2 register initialization |
| $0015D4 | CD Block and SCSP initialization |
| $001A18 | Security code copy and jump |
| $001A3C | "SEGA SEGASATURN " header validation |
| $001B50 | Security area comparison |
| $001BB4 | Region code validation |
| $001D38 | SMPC INTBACK command for system status |
| $002598 | CD Block poll with timeout |
| $0025DC | Disc detection and read |
| $002780 | CD Block initialization sequence |
| $0029D4 | CD Block command dispatch |
| $002F48 | CD Block sector read |
| $003338 | CD Block HIRQ wait/command interface |
| $003AEC | CD Block $75 (Read File) command builder |
| $040000 | Security check code block (copied to $06020000) |
| $040074 | Disc authentication via SMPC |

## BIOS ROM Layout

| Offset | End | Size | Contents |
|--------|-----|------|----------|
| $000000 | $000600 | 1.5 KB | Boot vectors and init code |
| $000600 | $000960 | 864 B | Work RAM-H data tables (copied at boot) |
| $000960 | $001100 | 1.9 KB | VDP/SCU init data, constant pools |
| $001100 | $001800 | 1.8 KB | System boot code (copied to $06001100) |
| $001800 | $005240 | 14 KB | Boot code, CD Block communication, decompressor |
| $005240 | $006F00 | ~7 KB | Font region. Begins with a compressed font body at $5240 (header $1001, block_count $0027 = 39; decompresses to ~3.7 KB of 8-pixel-wide monochrome glyph bitmaps, reached externally via routine-table slot $05E4 / sub_50EA). The remainder of the region (after the compressed body ends, roughly past $5780) holds uncompressed font tiles, UI glyphs, and "PRODUCED BY..." / "SEGA ENTERPRISES,LTD." strings (per the BIOS-Resident table above). |
| $006F00 | $007000 | 256 B | Region text strings ("For JAPAN.", "For USA and CANADA.", etc.) |
| $007000 | $01D000 | ~88 KB | Compressed boot library: Saturn logo animation, audio CD player, system-settings screens. Header $1001 at $7000, block_count = 2534. sub_173C decompresses to $06010000 with output cap $40000 (256 KB); actual decompressed size is determined by the stream. |
| $01D000 | $040000 | ~28 KB used | Compressed CD Block firmware. Header $1001 at $1D000, block_count = 827. sub_15D4 decompresses to $06010000 with output cap $40000 (256 KB). Compressed body consumes ~28 KB starting at $1D000; the remainder of $1D000-$040000 (~115 KB) is unused by this body. |
| $040000 | $040400 | 1 KB | Security check executable code |
| $040400 | $07D600 | ~244 KB | Security check data and authentication tables |
| $07D600 | $07D660 | 96 B | PER_Init trampoline / driver entry table (routine-table slot $06000358) |
| $07D660 | ~$080000 | ~10 KB compressed | PER + BUP driver, $1001-header compressed body (block_count $0137 = 311). Decompresses to ~16 KB providing PER_* and BUP_* services in WRAM-H. |

Compressed-body sizes were measured by running the documented
decompressor on each body.

Executable code occupies approximately 20 KB of the 512 KB BIOS. The
majority of the ROM is compressed library code, font glyphs, and
security check data.


