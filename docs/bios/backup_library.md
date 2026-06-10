# Backup RAM Library

SDK reference for the BUP_* function set, plus the BIOS-side
implementation that backs it. The BUP code is NOT a standalone
decompressed library; it lives inside the PER+BUP hybrid driver
that BIOS decompresses out of `$0007D660`. The PER side (peripheral
input, slot table, relocator, date helpers) is documented in
[peripheral_driver.md](peripheral_driver.md). This file covers the
BUP side: the SDK API plus the implementation subs.

## Backup RAM Library (SDK API)

Source: Backup Library User's Manual in System Library User's Guide (ST-162-R1)

### Overview

The backup library provides file-level read/write access to the Saturn's
battery-backed save RAM. Game code reaches the BUP_* functions through
the PER+BUP hybrid driver's slot table once PER_Init has run; there is
no separate decompression step (the SDK manual describes one, but in
the published BIOS the BUP code is part of the PER driver image).

### How Games Use the BUP Library

1. Game calls `PER_Init` (BIOS slot `$06000358`, BIOS body at
   `$0007D600`). PER_Init decompresses the PER+BUP hybrid driver from
   BIOS ROM `$0007D660` into a game-allocated buffer (16 KB). Per
   `sega_bup.h`, `$06000358` is also `BUP_LIB_ADDRESS` (read via
   `BUP_HK_INIT`), reflecting that the SDK's `BUP_Init` enters the
   hybrid driver's slot 0 at the same point PER_Init does.
2. The driver's slot 0 runs the unified PER+BUP init: it
   builds the 11-entry function-pointer table at the buffer
   head, registers the driver-base at WRAM-H `$06000354`
   (which `sega_bup.h` calls `BUP_VECTOR_ADDRESS`), seeds the
   per-entry working buffer pointer at `driver_base+$2C`,
   sets the port-1 peripheral marker at `driver_base+$54 = 1`
   (PER side), and writes the initial `BupConfig` entry
   (`unit_id = 1, partition = 1`, then four zero words) into the
   caller's `conf[]` buffer (the R6 argument; BUP side). The same
   routine is reached by `PER_Init` and
   by `BUP_HK_INIT` because the hybrid driver shares one
   init entry.
3. Game calls BUP_* by dispatching through the table:
   - Slot index N is at `mem.L[driver_base + N*4]`.
   - Slots 1..10 cover the ten BUP_* SDK functions (`BUP_SelPart`,
     `BUP_Format`, `BUP_Stat`, `BUP_Write`, `BUP_Read`, `BUP_Delete`,
     `BUP_Dir`, `BUP_Verify`, `BUP_GetDate`, `BUP_SetDate`). See the
     slot mapping table below.
4. The slot bodies dispatch on `R4` (the backup device number:
   `1` = internal, `2` = cartridge) to one of two BUP code paths.
   When `R4 == 2` (external A-bus BUP cart) the slot bodies
   reach the "cart-protocol" subs that talk to the cart via
   memory-mapped cart command registers; for the internal
   backup RAM the slot bodies take a different code path
   (see BIOS-side Implementation below).

### Data Structures

```
BupConfig {
    Uint16 unit_id;       // 0=not connected, 1=internal, 2=cartridge
    Uint16 partition;     // number of partitions
}

BupStat {
    Uint32 totalsize;     // total capacity (bytes)
    Uint32 totalblock;    // number of blocks
    Uint32 blocksize;     // size of one block (bytes)
    Uint32 freesize;      // free space (bytes)
    Uint32 freeblock;     // number of free blocks
    Uint32 datanum;       // number of files that fit in datasize
}

BupDir {
    Uint8  filename[12];  // 11 ASCII chars + NUL
    Uint8  comment[11];   // 10 ASCII chars + NUL
    Uint8  language;      // 0=Japanese, 1=English, 2=French,
                          // 3=German, 4=Spanish, 5=Italian
    Uint32 date;          // date/time (BUP_SetDate format)
    Uint32 datasize;      // data size in bytes
    Uint16 blocksize;     // data size in blocks
}

BupDate {
    Uint8 year;           // year - 1980
    Uint8 month;          // 1-12
    Uint8 day;            // 1-31
    Uint8 time;           // hour 0-23
    Uint8 min;            // minute 0-59
    Uint8 week;           // day of week (0=Sunday, 6=Saturday)
}
```

### Function List

| No. | Function | Name | Signature |
|-----|----------|------|-----------|
| 1 | Initialize | BUP_Init | `void BUP_Init(Uint32 *libaddr, Uint32 *workbuff, BupConfig conf[])` |
| 2 | Select partition | BUP_SelPart | `Sint32 BUP_SelPart(Uint32 device, Uint16 num)` |
| 3 | Format device | BUP_Format | `Sint32 BUP_Format(Uint32 device)` |
| 4 | Get status | BUP_Stat | `Sint32 BUP_Stat(Uint32 device, Uint32 datasize, BupStat *stat)` |
| 5 | Write data | BUP_Write | `Sint32 BUP_Write(Uint32 device, BupDir *dir, Uint8 *data, Uint8 owsw)` |
| 6 | Read data | BUP_Read | `Sint32 BUP_Read(Uint32 device, Uint8 *fname, Uint8 *data)` |
| 7 | Delete data | BUP_Delete | `Sint32 BUP_Delete(Uint32 device, Uint8 *fname)` |
| 8 | Get directory | BUP_Dir | `Sint32 BUP_Dir(Uint32 device, Uint8 *fname, Uint16 dirsize, BupDir *dir)` |
| 9 | Verify data | BUP_Verify | `Sint32 BUP_Verify(Uint32 device, Uint8 *fname, Uint8 *data)` |
| 10 | Get date | BUP_GetDate | `void BUP_GetDate(Uint32 pdate, BupDate *date)` |
| 11 | Set date | BUP_SetDate | `Uint32 BUP_SetDate(BupDate *date)` |

### Return Codes

Verified against `sega_bup.h` (`#define BUP_NON (1)` through
`#define BUP_BROKEN (8)`) and against the slot bodies, which
move these values literally into R0 (`MOV #1,R0` for BUP_NON,
`MOV #2,R0` for BUP_UNFORMAT, etc.).

| Code | Name | Meaning |
|------|------|---------|
| 0 | - | Success |
| 1 | BUP_NON | Device not connected |
| 2 | BUP_UNFORMAT | Device not formatted |
| 3 | BUP_WRITE_PROTECT | Write protected |
| 4 | BUP_NOT_ENOUGH_MEMORY | Not enough free blocks to satisfy a write |
| 5 | BUP_NOT_FOUND | Filename did not match any directory entry |
| 6 | BUP_FOUND | File already exists (Write with `owsw != 0`) |
| 7 | BUP_NO_MATCH | Verify mismatch / data does not match |
| 8 | BUP_BROKEN | Backup RAM unreadable / corrupted |
| -1 (`$FFFFFFFF`) | - | General device error (cart-status byte fell through the slot body's known-code switch tables) |

## Backup RAM on-disk format

This section documents the byte layout that the BIOS writes
to the 32 KB internal backup RAM at `$00180000` for save data.
Cross-verified against three BIOS-created save images: a
single Burning Rangers save, two NiGHTS saves (NIGHTS___01 +
NIGHTS___02), and a combined image with all three files
(B_RANGERS__ + NIGHTS___01 + NIGHTS___02).

### File-level layout

| Offset | Size | Contents |
|--------|------|----------|
| `$0000-$003F` | 64 B | Header: "BackUpRam Format" string repeated 4 times (16 bytes each) |
| `$0040-$007F` | 64 B | Reserved / free-block-bitmap region (zero-filled in all 3 samples; no allocated saves use this region as data) |
| `$0080+` | varies | Directory entries + their data blocks, packed sequentially |

The whole image is **flat-byte** (no every-other-byte odd-address
encoding even though real-hardware RAM exposes only odd
addresses through the SH-2 bus). The `+$09EA` magic-scan sub
applies the odd-address mapping at read time via
`mem.B[$20180001 + 2*i]` indexing - the on-disk `.srm` collapses
that into a flat byte stream so each "logical byte" occupies one
host byte.

### Block addressing

The RAM is divided into 64-byte blocks indexed from 0:

- Block 0 (`$0000-$003F`): header
- Block 1 (`$0040-$007F`): reserved (always zero in observed
  samples; see "Free-block tracking" below)
- Block 2 (`$0080-$00BF`): first directory entry (the entry's
  fixed 34-byte metadata plus the first portion of its block
  list fits in one 64-byte block)
- Block 3+: data blocks (and overflow block-list bytes when
  the directory entry's list exceeds one 64-byte block)

A file's directory entry occupies one block (or more if its
block list is large) and the data is stored in additional
blocks pinned by 16-bit indices recorded in the entry. Block
index to offset mapping is `offset = block_index * 64`.

### Directory entry layout

Cross-confirmed against 4 entries (BR cart.srm + cart3, NIGHTS_01 +
NIGHTS_02 in cart2/cart3). All field offsets are relative to the
start of the directory entry block. The layout matches the SDK
BupDir struct (34 bytes) except that the on-disk format drops
the `blocksize` field and prefixes a 4-byte status word, then
appends the variable-length block list inline.

| Offset | Size | Field |
|--------|------|-------|
| `+$00` | 4 B | Status word: `$80000000` for in-use first-entry block; `$00000000` for free entry |
| `+$04` | 11 B | Filename (ASCII; no NUL terminator stored - the SDK's `filename[12]` 12th-byte slot is reused for language at +$0F) |
| `+$0F` | 1 B | Language code (0=JP, 1=EN, 2=FR, 3=DE, 4=ES, 5=IT) |
| `+$10` | 10 B | Comment (ASCII; may have embedded NUL terminator + uninitialized trailing bytes) |
| `+$1A` | 4 B | Date: BE Uint32, **minutes since 1980-01-01 00:00 UTC** - the same packed code produced by slot 10 (BUP_SetDate) |
| `+$1E` | 4 B | Datasize: file payload size in bytes (BE Uint32) |
| `+$22+` | varies | Block list: 16-bit BE block indices, `$0000` separates extents, `$0000 $0000` (2 zero words) terminates the in-block portion |

### Decoded sample field values

| Field | cart.srm BR | cart2 N1 | cart2 N2 | cart3 BR |
|-------|-------------|----------|----------|----------|
| filename | `B_RANGERS__` | `NIGHTS___01` | `NIGHTS___02` | `B_RANGERS__` |
| language | EN (1) | JP (0) | JP (0) | EN (1) |
| comment | `BR data` | `Score data` | `A-life` | `BR data` |
| date | `0x0174367E` = 2026-05-18 19:42 | `0x01745693` = 2026-05-24 12:35 | `0x01743741` = 2026-05-18 22:57 | `0x01745695` = 2026-05-24 12:37 |
| datasize | 1856 | 1856 | 12544 | 1856 |
| first block | 3 | 3 | 36 | 253 |

Cross-sample observations:

- **Status** is `$80000000` for every in-use first-block entry.
- **Filename** is fixed 11 bytes (not 12; the 12th byte of the
  SDK `filename[12]` field is repurposed on disk for the
  language byte at +$0F). Filenames shorter than 11 chars are
  NUL-padded.
- **Language** at +$0F: NIGHTS saves have `00` (Japanese; NiGHTS
  is JP-developed and apparently hardcodes JP language metadata
  regardless of cart region), BR saves have `01` (English).
- **Comment** at +$10-19 (10 bytes): variable-length with
  embedded NUL terminator if shorter than 10. Bytes past the
  NUL may carry uninitialized memory (e.g., cart2 N2's
  `'A-life\0F4...'` shows a leaked `$F4` byte).
- **Date** at +$1A-$1D (4 bytes BE): packed Uint32 minutes
  since 1980-01-01 00:00. Cross-verified: all 4 samples decode
  to May 2026, matching when the user created them. This is the
  same format produced by slot 10 (BUP_SetDate) which we
  documented as `total_min = days*1440 + hour*60 + minute`.
- **Datasize** at +$1E-$21 (4 bytes BE): file payload size.
- **Block list** starts at +$22 with 16-bit BE block indices.

### Block list semantics

A file's payload is reached via two kinds of block lists:

1. **In-entry list** at dir-entry +$22..+$3F: 15 16-bit BE word
   slots. Each slot can name **either** a continuation page or
   a data block (see detection rule below).
2. **Continuation-page list** at cont-page +$04..+$3F: 30
   16-bit BE word slots. Each slot names a data block. Per
   inspection of cart2.srm NIGHTS_02's cont-listed blocks
   (51..80, 81..110, ..., 231..251), continuation pages
   **never name other continuation pages** - the chain is at
   most one level deep.

Both lists terminate on a single `$0000` word, or run to the
end of their slot window if all slots are filled (unterminated).
A list is "unterminated" only when every slot has a non-zero,
in-range index.

#### Continuation page detection

A block at index B is a **continuation page** if and only if:
1. Bytes `mem.B[B*64+$00..+$03]` are all `$00` (the sentinel), AND
2. The 16-bit BE word at bytes `mem.B[B*64+$04..+$05]` is a valid
   block index in `[2, 512)`.

The second condition (valid first index) is necessary because
data blocks can also start with four zero bytes - for example,
NIGHTS_02's blocks 43..50 hold zero data, and NIGHTS data blocks
generally have a 4-byte zero header (real BIOS writes zeros at
+$00..+$03 of every data block; see *data block* below). The
distinguishing feature of a real continuation page is that
bytes +$04..+$05 hold a non-zero, in-range block index.

#### Data block

Every block listed by a file (other than continuation pages) is
a data block:
- Bytes +$00..+$03: 4-byte header, written as zero by real BIOS.
  Skipped on read.
- Bytes +$04..+$3F: 60 bytes of file payload.

#### Continuation page payload tail

When a cont-page list has a `$0000` terminator at slot index
`T` (where `T < 30`), the bytes from offset `4 + (T+1)*2` to
offset `64` of that block are **payload tail** - the first
bytes of the file's payload that the cont page contributes.
Tail size = `64 - 4 - (T+1)*2 = 58 - 2*T` bytes.

If the cont list fills all 30 slots without a terminator, the
cont page has no payload tail (capacity = 0 bytes).

#### Read chain assembly

To read a file, traverse the in-entry list breadth-first:

```
queue = in-entry list slots (in order, up to first $0000 or all 15)
chain = []  // ordered list of (block, startOff, endOff) tuples

while queue not empty:
    block = queue.pop_front()
    if is_continuation_page(block):
        parse cont list (30 slots, single-$0000 terminated)
        if terminated:
            chain.append(block, tail_start, 64)  // payload tail
        for each cont-listed index in order:
            queue.push_back(index)
    else:
        chain.append(block, 4, 64)  // 60-byte data payload

copy `datasize` bytes from `chain` into the caller's buffer.
```

The resulting chain order is:
- **All in-entry cont-page tails first**, in in-entry order (typically only the last cont page in the chain has a non-empty tail, since earlier cont pages fill all 30 slots).
- **Then all in-entry data blocks**, in in-entry order (60 bytes each).
- **Then all cont-listed data blocks**, in the order they were enqueued (cont page 0's list, then cont page 1's list, then ...).

This is single-level BFS - it works for both single-cont chains and multi-cont chains, and reduces to "all data blocks" when the in-entry list is terminated.

#### Capacity formula

For a file with `D` data blocks and `C` continuation pages:

- **Pure in-entry** (`C = 0`, `D <= 14`): capacity = `D * 60` bytes.
- **With continuation pages** (`D > 14`):
  - The in-entry list holds `C` cont-page slots and `15-C` data-block slots, so cont pages list `D - (15-C) = D + C - 15` indices.
  - The first `C-1` cont pages fill all 30 slots (no terminator, no tail) so they list 30 indices each = `30*(C-1)` indices.
  - The last cont page holds the remaining `D + C - 15 - 30*(C-1) = D + 15 - 29C` indices, followed by a terminator and a `58 - 2*(D + 15 - 29C) = 28 + 58C - 2D` byte payload tail.
  - Total payload capacity: `D * 60 + (28 + 58C - 2D)` = **`58 * (D + C) + 28`** bytes.
  - Minimum cont pages: `C = ceil((D - 14) / 29)`.
  - Maximum single-file size (D + C <= 510 free blocks, assuming a fresh device): roughly 29 KB.

Worked NIGHTS_02 example: `D = 209`, `C = 7`, capacity = `58 * 216 + 28 = 12556` bytes >= datasize `12544` OK.

#### Write layout

To write a file with byte count `datasize`:

1. Compute `D` and `C`:
   - If `datasize == 0`: `D = 0`, `C = 0`.
   - Else if `datasize <= 14 * 60 = 840`: `D = ceil(datasize / 60)`, `C = 0` (pure in-entry).
   - Else: solve `58 * (D + C) + 28 >= datasize` with `C = ceil((D - 14) / 29)`. The minimum `D` is approximately `(datasize - 28) / 60 + 14/30`; iterate D upward until the capacity formula is satisfied.
2. Allocate `1 + C + D` free blocks (dir entry + cont pages + data blocks).
3. Lay out the in-entry list:
   - Slots `0..C-1`: cont-page block indices.
   - Slots `C..14`: first `15-C` data-block indices.
   - If pure in-entry (`C = 0`): write a single `$0000` terminator after the last data-block index. Otherwise leave all 15 slots filled (unterminated -> triggers cont-page interpretation on read).
4. Lay out the continuation pages:
   - Cont pages `0..C-2`: fill all 30 cont-list slots with data-block indices `(15-C+0..29)`, `(15-C+30..59)`, etc. - no terminator, no payload tail.
   - Cont page `C-1` (the last): write the remaining `D + 15 - 29C` data-block indices, then a single `$0000` terminator, then write the **first** `28 + 58C - 2D` bytes of file payload as the page's tail.
5. Lay out the data blocks: bytes +$00..+$03 = zero header; bytes +$04..+$3F = up to 60 payload bytes; remaining destination bytes zero-padded.
6. The byte order in which file content is distributed across the cont-page tail and data blocks matches the **Read chain assembly** order above: last cont page's tail first, then in-entry data blocks in order, then cont-listed data blocks in cont-page order.

**Worked NIGHTS_02 layout** (datasize 12544, verified against
cart2.srm):
- Dir entry at block 35.
- In-entry list: `[$0024 $0025 ... $0032]` = blocks 36..50.
  - Slots 0..6: cont pages (blocks 36..42).
  - Slots 7..14: in-entry data blocks (blocks 43..50).
- Cont page 0 (block 36) at `$0900..$093F`:
  - Sentinel `$00000000` + 30 indices (blocks 51..80) - list fills the page, no terminator, no tail.
- Cont pages 1..5 (blocks 37..41): same pattern, listing blocks 81..110, 111..140, 141..170, 171..200, 201..230 respectively.
- Cont page 6 (block 42, the LAST cont page) at `$0A80..$0ABF`:
  - Sentinel `$00000000` (4 B) + 21 indices (blocks 231..251, 42 B) + `$0000` terminator (2 B) + 16-byte payload tail (file bytes [0..15], observed as `FF FF FF FF 00 00 00 00 00 00 00 00 00 00 00 00`).
- Data blocks 43..50 then 51..80 then 81..110 then 111..140 then 141..170 then 171..200 then 201..230 then 231..251 contribute 60 bytes of payload each (skipping their 4-byte zero header).

Total layout: 1 dir entry + 7 cont pages + 209 data blocks = 217 blocks claimed.

### Block size and packing

Block size = **64 bytes**, confirmed multiple ways:

- The header / dir-entry / bitmap regions all align to 64-byte
  boundaries
- NIGHTS___01 occupies blocks 2-34 (1 dir-block + 1
  continuation page (block 3) + 31 data blocks); NIGHTS___02's
  dir entry starts at block 35 (`$08C0`), exactly the next
  available block
- B_RANGERS__ in cart3's dir entry starts at block 252
  (`$3F00`); its first listed block 253 (`$3F40`) is a
  continuation page, so its first data block is 254 (`$3F80`),
  well past the NIGHTS data + extension

### Free-block tracking

The 64-byte region at `$0040-$007F` is zero in all three
samples. Two interpretations:

1. **Free-block bitmap** (64 bytes = 512 bits, one per 64-byte
   block): zero bits = "free". A blank region would mean "all
   blocks free", which contradicts the fact that blocks 2-34+
   are clearly allocated. So this region isn't an active bitmap.

2. **Reserved / format pad**: BIOS writes nothing here after
   format. Free-block tracking is implicit (walk all directory
   entries, mark listed blocks as used; the rest are free).

Observation #2 is consistent with all three samples. The BIOS
probably reformats the region to zeros and treats it as a
permanent reserved block (so block-1 indices in directory
chains can serve as "no block" sentinels without colliding
with a real data block).

## BIOS-side Implementation in the PER Driver

The BUP_* SDK functions dispatch into slot bodies in the
decompressed PER+BUP driver buffer. Each BUP slot body has
**two parallel paths** keyed off `R4`:

- **R4 == 2: external A-bus BUP cartridge path.** Goes
  through the "cart-protocol" sub family (`+$2FD0`, `+$2C08`,
  `+$32A6`, `+$279C`, `+$21D8`, `+$22AA`, ...) that talks to
  an inserted A-bus BUP cart via memory-mapped command /
  status / data registers (the pool stores 16-bit words
  `$FE02..$FE05` loaded by `MOV.W`). When the port-2 marker
  at `driver_base+$56` is zero, the BUP-flavored slots take a
  "no cart" early-out before reaching the cart-protocol subs;
  in practice (no BUP cart inserted), the cart-protocol subs
  never execute.

- **R4 != 2: internal backup RAM path.** This is the path the
  SDK BUP library calls into for the everyday save operations
  that produced our `cart.srm` samples. The slot bodies
  delegate into slot 3 (which sets up `driver_base+$30` to the
  internal-RAM cache-through alias `$20180000` via pool
  `+$07F8` and BSRs `+$09EA` to scan for the
  `"BackUpRam Format"` header) and a cluster of helper subs
  (`+$1434` validate, `+$17BA` cleanup, `+$1BB4` transform,
  plus `+$0EBC` / `+$1004` for slot 4's write path). These
  helpers operate on internal backup RAM at `$00180000` via
  the byte-mapped bus address directly - no command
  registers, no controller, no protocol. The on-disk format
  documented
  above is what this internal-RAM code reads and writes.

The `$00A00000` value that appears as R6 in cart-protocol
sub calls is **not a memory address** - inside `+$22AA` the
`CMP/HI R6,R5` against R6 = 10M effectively disables the
loop's retry timeout. It's a sentinel for "no timeout", only
ever seen on the external-cart path.

The SCU IMS register is masked around the external-cart
critical sections via the BIOS function pointer at
`$06000340` (an in-WRAM trampoline). This masking happens
only on the R4==2 path.

The slot bodies receive the SDK `device` argument directly in
R4. The `BUP_HK_*` hook macros in `sega_bup.h` pass `device` as
the first parameter to the vector function. `device` is a
0-based index into the `BupConfig[]` table that `BUP_Init`
fills, not a `unit_id` value: index 0 is the internal main-unit
backup RAM and index 2 is the external A-bus cartridge (the
`R4==2` path); index 1 is a further `BupConfig` slot. The
slot's `R4==2` test selects the cartridge; any other index
(0 = internal, 1 = the additional slot) falls through to the
internal-RAM path in the slot body. (`BUP_MAIN_UNIT (1)` and
`BUP_CURTRIDGE (2)` are the `unit_id` values stored in each
`BupConfig` entry, describing what device occupies an index,
not the `device` argument itself.)

**Scope of the per-sub documentation below:** the cart-protocol
subs (`+$21D8`, `+$22AA`, `+$2484`, `+$2568`, `+$25C8`,
`+$2670`, `+$2734`, `+$279C`, `+$2C08`, `+$2FD0`, `+$32A6`,
`+$2140`) are documented in full below. **They are the
external-cart path only, NOT what backs internal-RAM save
operations.** The internal-RAM helpers (slot 3's body,
`+$1434`, `+$17BA`, `+$1BB4`, `+$0EBC`, `+$1004`) are
documented in the "Internal-RAM BUP path" section below.

### Slot -> BUP function mapping

Slot -> SDK function binding is verified against `sega_bup.h`
(the ten data-function `BUP_HK_*` macros - `BUP_HK_SELPART`
through `BUP_HK_SETDATE`, slots 1-10 - dispatch through
`mem.L[VECTOR_ADDRESS + N*4]`, where `VECTOR_ADDRESS = mem.L[$06000354]`;
`BUP_HK_INIT` is the exception, dispatching through the single
`BUP_LIB_ADDRESS = mem.L[$06000358]` pointer rather than the N*4
vector table)
and against the driver image's own slot-table initialization code
at file offset `+$014..+$078`, which writes each body's address to
`driver_base + N*4`.

| Slot | Offset | BUP function | External-cart sub (R4==2) |
|------|--------|--------------|---------------------------|
| 1 | `+$02C4` | BUP_SelPart | `+$266E` (shared with slot 3) |
| 2 | `+$03AA` | BUP_Format | `+$2732` (cart-ID poll wrapper) |
| 3 | `+$0672` | BUP_Stat | `+$25C8`, `+$266E` |
| 4 | `+$0A78` | BUP_Write | `+$32A6` |
| 5 | `+$135A` | BUP_Read | `+$2FD0` |
| 6 | `+$16DC` | BUP_Delete | `+$279C` |
| 7 | `+$18B4` | BUP_Dir | `+$2C08` |
| 8 | `+$1ACC` | BUP_Verify | `+$2FD0` (shared with slot 5) |
| 9 | `+$1E70` | BUP_GetDate (packed Uint32 -> BupDate) | (slot body only) |
| 10 | `+$1F64` | BUP_SetDate (BupDate -> packed Uint32) | (slot body only) |

Slots 1, 7, 9, 10 are documented in full here (calling
convention + R4==2 cart path + R4!=2 internal-RAM body in one
place). Slots 2/3/4/5/6/8 are split: the calling convention +
R4==2 path inner-sub list is in the per-slot section below,
and the R4!=2 internal-RAM body is in the **Internal-RAM BUP
path** section further down. The external-cart inner subs
(`+$2732`, `+$25C8`, `+$266E`, `+$32A6`, `+$2FD0`, `+$279C`, `+$2C08`)
are documented under **External A-bus BUP cart subs** at the
end of this file.

### Driver state structure (at driver_base+$00..+$78+)

Once PER_Init has decompressed the driver and slot 0 has set
`mem.L[$06000354] = driver_base`, the driver buffer holds the
following layout. All multi-byte fields are big-endian. Field
roles are derived from the disassembly sections later in this
document; each row notes the offset, size, access width, and
representative use.

| Offset | Size | Access | Role |
|--------|------|--------|------|
| `+$00` | 4 B | mem.L | Slot 0 function pointer - unified PER+BUP driver init / relocator. Same entry is reached by `PER_Init` (BIOS ROM `$0007D600`) and by `BUP_HK_INIT` (`mem.L[$06000358]`); sets up the slot table, PER port-1 marker (`+$54`), and initial `BupConfig` in the caller's `conf[]` buffer (R6) |
| `+$04` | 4 B | mem.L | Slot 1 function pointer (BUP_SelPart) |
| `+$08` | 4 B | mem.L | Slot 2 function pointer (BUP_Format) |
| `+$0C` | 4 B | mem.L | Slot 3 function pointer (BUP_Stat) |
| `+$10` | 4 B | mem.L | Slot 4 function pointer (BUP_Write) |
| `+$14` | 4 B | mem.L | Slot 5 function pointer (BUP_Read) |
| `+$18` | 4 B | mem.L | Slot 6 function pointer (BUP_Delete) |
| `+$1C` | 4 B | mem.L | Slot 7 function pointer (BUP_Dir) |
| `+$20` | 4 B | mem.L | Slot 8 function pointer (BUP_Verify) |
| `+$24` | 4 B | mem.L | Slot 9 function pointer (BUP_GetDate) |
| `+$28` | 4 B | mem.L | Slot 10 function pointer (BUP_SetDate) |
| `+$30` | 4 B | mem.L | Backup RAM base address. Set to `$20180000` for internal-RAM scan (cache-through alias of `$00180000`); `$24000000` for the slot-3 alt extended-setup path |
| `+$34` | 4 B | mem.L | Mode flag. `0` = no IRQ masking; `1` = SCU IRQs are masked across the RAM operation - internal subs gate on `+$34 == 1` (e.g. `+$0BA4`, `+$1828`) and call the BIOS set-IMS fn at `$06000340` (R4 = -1), saving the prior IMS (read via `$06000348`) to a local. Standard setup writes `0`; the alt-extended setup writes the value from `+$64`. The external-cart path's IMS shadow at `+$6C` is separate |
| `+$38` | 4 B | mem.L | Total directory entry count for the current device (`$0200` = 512 for internal RAM on the standard setup path) |
| `+$3C` | 4 B | mem.L | Block size in bytes (`64` for internal RAM); also used as the scan length per `+$09EA` pass |
| `+$40` | 4 B | mem.L | Per-entry RAM stride in bytes. Slot 3 setup writes `2 * driver_base+$3C` here, accounting for odd-byte addressing where a 64-byte logical block needs 128 bus bytes |
| `+$44` | 4 B | mem.L | Partition / mode index. Slot 3 standard setup writes `2` here. Re-read by `+$1434` (which compares R5 input against this) and by slot 3's mode-1 alt-path setup |
| `+$48` | 4 B | mem.L | Mismatch-entry sequence ID. Written by `+$1BB4` (per-entry transform / BUP_Verify compare) when a byte differs - stores the entry word so the caller can report which file mismatched |
| `+$4C` | 4 B | mem.L | First directory entry address (`$20180080` for internal RAM). Used as the base for bitmask-gated dir-entry status reads in `+$1434`, `+$1004`, and slot 3's per-entry walk |
| `+$50` | 4 B | mem.L | Current entry sequence counter. Incremented by `+$1434` mode-2 dispatch; reset to `mem.L[driver_base+$44]` by `+$1434` mode-1 dispatch; written with the matched entry index on a `+$1434` filename hit |
| `+$54` | 1 B | mem.B | Port-1 peripheral marker (set by PER side of driver during INTBACK). Slot 3 internal-RAM standard path checks this; if zero, the body's `+$0760` path runs `MOV #1,R0` and returns BUP_NON |
| `+$55` | 1 B | mem.B | Port-1 alt marker. Slot 3 alt-extended-setup path checks this at +$07E2 |
| `+$56` | 1 B | mem.B | Port-2 marker (`1` = external A-bus BUP cart present, `0` = absent). All BUP-flavored slots check this in their R4==2 path; zero -> "no cart" early-out without invoking cart-protocol subs |
| `+$58` | 4 B | mem.L | Alt-pool entry: overrides `driver_base+$38` total-entry count when slot 3's mode-1 alt-extended-setup path runs |
| `+$5C` | 4 B | mem.L | Alt-pool entry: overrides `driver_base+$3C` block size |
| `+$60` | 4 B | mem.L | Alt-pool entry: overrides `driver_base+$44` partition/mode index |
| `+$64` | 4 B | mem.L | Alt-pool entry: overrides `driver_base+$34` mode flag |
| `+$6C` | 4 B | mem.L | Saved SCU IMS shadow for the external-cart (R4==2) path. Cart-protocol subs store the current IMS (read via `$06000348`) here before masking through `$06000340`, and restore it through `$06000340` when the critical section ends |
| `+$74` | 1 B | mem.B | Port-2 byte parameter. Written by the cart-query subs at `+$02EC` and `+$20EA` before/around invoking `+$266E`. Read by `+$2140` (at `+$21B2`: `ADD #116,R5` ; `MOV.B @R5,R5`) to supply the request byte to `+$266E`; `+$2140` does not write this field |

Standard slot 3 setup values (R4!=2 path, observed in
`+$0766..+$07A6` disasm):

```
driver_base+$30 = $20180000   ; BUP RAM cache-through base
driver_base+$34 = 0           ; mode 0 (no IRQ mask)
driver_base+$38 = $0200       ; 512 dir entries
driver_base+$3C = 64          ; 64-byte blocks
driver_base+$40 = $0080       ; 2 * blocksize (128-byte stride)
driver_base+$44 = 2           ; partition 2
driver_base+$4C = $20180080   ; first dir entry
```

Alt-extended-setup values (R4!=2 mode-1, observed in
`+$0804..+$0860`):

```
driver_base+$30 = $24000000           ; alt RAM region
driver_base+$34 = mem.L[+$64]         ; mode from extended pool
driver_base+$38 = mem.L[+$58]         ; entry count from extended pool
driver_base+$3C = mem.L[+$5C]         ; block size from extended pool
driver_base+$40 = 2 * driver_base+$3C ; computed stride
driver_base+$44 = mem.L[+$60]         ; partition from extended pool
driver_base+$4C = $30 + computed offset
```

### BUP sub call graph (external-cart path only)

The call graph below is the **R4 == 2 external A-bus BUP cart
path**. These cart-protocol subs are not what handles internal
save memory - that lives in the R4 != 2 path of slots
2/3/4/5/6/8, documented in the **Internal-RAM BUP path**
section below.

All paths below bottom out at memory-mapped cart-register
accesses loaded from pool words `$FE02..$FE05`.

```
slot 2  (BUP_Format) -> +$2732 -> +$2484 / +$2568
slot 3  (BUP_Stat)   -> +$25C8 -> +$2484 / +$22AA / +$2568
                     -> +$266E -> +$2484 / +$22AA / +$2568
slot 4  (BUP_Write)  -> +$32A6 -> +$2484 / +$21D8 / +$22AA / +$2568 / +$30DE
slot 5  (BUP_Read)   -> +$2FD0 -> +$2484 / +$21D8 / +$22AA / +$2D82
slot 6  (BUP_Delete) -> +$279C -> +$2484 / +$21D8 / +$22AA / +$2568
slot 7  (BUP_Dir)    -> +$2C08 -> +$2484 / +$21D8 / +$22AA / +$2860
                     -> +$3610 (peripheral_driver.md)
slot 8  (BUP_Verify) -> +$2FD0 (same body as slot 5)
slot 9  (BUP_GetDate)-> +$1DC4 / +$3610 / +$3780
slot 10 (BUP_SetDate)-> +$1D96 / +$355C / +$36B8 (peripheral_driver.md)

Cart-protocol primitives (all bottom-level subs use the cart
register interface; external-cart path only):
  +$21D8  send N bytes via cart-write reg + cart-status polling
  +$22AA  read N bytes via cart-data reg + cart-status polling
  +$2484  send 6-byte identify cmd + read 6-byte response
  +$2568  read 6 bytes + check magic
  +$2732  cart-ID poll wrapper for slot 2's R4==2 path
  +$266E  cart-read variant for slots 1/3's R4==2 path
  +$25C8  multi-field stat read for slot 3 (BUP_Stat external)
  +$279C  slot 6 cmd/ack (BUP_Delete external)
  +$2C08  slot 7 multi-entry directory walk (BUP_Dir external)
  +$2FD0  slot 5/8 status-extract walker (BUP_Read / BUP_Verify external)
  +$32A6  slot 4 multi-block write (BUP_Write external)
  +$30DE  cart-register helper for slot 4's R4==2 write path

Foundational helpers (called by the above):
  +$21D8 -> +$3478, +$2140
  +$22AA -> +$2140 (via JSR *$06000340 for IMS gating)
  +$2140 -> +$266E
  +$30DE -> +$2140
  +$09EA -> +$033E (load BUP-magic string)
  +$1DC4 -> +$1D96

Shared math primitives (documented in peripheral_driver.md):
  +$3610 / +$3612 (32/32 unsigned division)
    BUP callers: slots 3, 4, 7, 9 + +$32A6
  +$36B8 (signed-quotient adjust)
    BUP callers: slot 10
```

### Slot 1 (`+$02C4`) - BUP_SelPart

- **SDK function**: `Sint32 BUP_SelPart(Uint32 device, Uint16 num)`
- **Body**: `+$02C4` to `+$033C` (120 bytes), 4 RTS exit points
- **Inputs**:
  - R4 = device. The body only does work when R4 == 2 (cart);
    for any other value the body short-circuits to R0 = 0
    (internal RAM and serial don't need partition selection).
  - R5 = `num` (partition number, Uint16). The body stores
    `R5` byte-truncated at `mem.B[driver_base + $74]` before
    invoking the cart sub - the cart-side protocol only accepts
    one byte of partition.
- **Returns** (R0):
  - `0` = success (either R4 != 2 short-circuit, or R4 == 2
    cart-sub returned 0)
  - `1` = BUP_NON (R4 == 2 and port-2 marker is zero - no cart -
    OR cart-sub returned the "device unusable" codes `$0101` /
    `$0106`)
  - `-1` (`$FFFFFFFF`) = generic failure (cart-sub returned an
    unrecognized code)
- **Calls**: sub-routine at driver `+$266E` (via BSRF with R3
  from pool[+$031C] = `$00002374`)

```python
def bup_selpart(device, num):                   # R4, R5
    if device != 2:
        return 0                                # internal RAM / serial: no-op success

    if mem.B[driver_base + 0x56] == 0:          # port-2 marker
        return 1                                # BUP_NON: no cart

    # Cart protocol accepts one byte of partition number
    mem.B[driver_base + 0x74] = num & 0xFF
    status = sub_266E(stack_frame_ptr)          # BSRF via pool +$031C = $00002374

    if status == 0:        return 0             # success
    if status == 0x0101:   return 1             # device unusable -> BUP_NON
    if status == 0x0106:   return 1             # device unusable -> BUP_NON
    return -1                                    # generic failure
```

Pool constants:
- `+$0318`: `$06000354` (driver-registration WRAM-H slot)
- `+$031C`: `$00002374` (BSRF offset to sub-routine at driver `+$266E`)
- `+$0384`: `$0101` (cart status code -> BUP_NON)
- `+$0386`: `$0106` (cart status code -> BUP_NON)

**Behavior**: queries the external A-bus BUP cart (R4==2). Reads/writes a
state byte at `driver_base + $74` and passes a stack frame to a
sub-routine that performs the actual hardware/state inspection.
The sub at `+$266E` returns a status code; slot 1 maps `0` to
success (`0`), `$0101` / `$0106` to BUP_NON (`1`), and any other
code to `-1` (generic failure).

### Slot 2 (`+$03AA`) - BUP_Format calling convention

Slot body at `+$03AA..+$0670`. Verified against `sega_bup.h`
(`BUP_HK_FORMAT` dispatches through `mem.L[VECTOR_ADDRESS+8]`)
and against the body's R4!=2 path, which BSRs to `+$0388`
(bitmask-table init) and `+$033E` (stages the 16-byte
"BackUpRam Format" magic once into a stack scratch buffer; the
slot body's later commit loop at `+$04C0..+$04F2` replicates it
4x into the backup-RAM header) - the operations that define
BUP_Format's job. The R-register convention game code
uses when calling via `JSR @($08,driver_base)`:

- **SDK function**: `Sint32 BUP_Format(Uint32 device)`
- **R4** = driver channel (R4==2 selects external-cart path;
  other values select internal-RAM path)
- **R5**, **R6** = not used by the SDK API; the body saves them
  for its internal frame management but the SDK macro
  `BUP_HK_FORMAT(device)` does not provide them
- **R0** return:
  - `0` = success
  - `1` = BUP_NON (no cart inserted on R4==2 path, or slot 3
    prelude reported BUP_NON on R4!=2 path)
  - `3` = BUP_WRITE_PROTECT (cart status reported write-protect,
    or slot 3 prelude returned BUP_WRITE_PROTECT)
  - `8` = BUP_BROKEN (post-format read-back compare failed, or
    backup RAM is unreliable)
  - `-1` (`$FFFFFFFF`) = generic device error
- Slot 3's `2` (BUP_UNFORMAT) is **not** propagated: the R4!=2
  path treats UNFORMAT from the prelude as the expected state
  ("the device isn't formatted yet, which is why Format is
  being called") and proceeds with the format write.
- **Memory effect**: internal-RAM path rewrites the
  "BackUpRam Format" magic header at `$00180000..$0018003F`
  (4 repetitions of the 16-byte string), zeros the reserved
  block at `$00180040..$0018007F`, and marks every directory
  entry's status word as `$00000000` (free)
- **Inner subs**: R4==2 path calls `+$2732` (cart-ID poll
  wrapper) via BSRF disp `$233A` at +$03F4; R4!=2 path calls
  `+$0388` (bitmask init), `+$033E` (magic-string writer), and
  `+$0672` (slot 3 / BUP_Stat prelude) before walking the dir
  to zero status fields. R4!=2 details are in the Internal-RAM
  BUP path section.

### Slot 3 (`+$0672`) - BUP_Stat calling convention

Slot body at `+$0672..+$09E8` (888 bytes - the longest BUP
slot). Verified against `sega_bup.h` (`BUP_HK_STAT` dispatches
through `mem.L[VECTOR_ADDRESS+12]`) and against the body's
behavior: the R4==2 path BSRs to `+$266E` then BSRF to `+$25C8`
(a multi-field stat reader) and copies 24 bytes total (the size
of the `BupStat` struct) into the buffer pointed to by the
caller's R6.

Slot 3 is also BSR'd internally by slots 2/4/5/6/7/8 as a
"validate device + measure state" prelude. Those callers pass
throwaway R5/R6 (usually `R5=0`, `R6=stack-local`) just to run
the format-check + state-load; they ignore the BupStat output.

- **SDK function**: `Sint32 BUP_Stat(Uint32 device, Uint32 datasize, BupStat *stat)`
- **R4** = driver channel (saved at `stack[4]`)
- **R5** = hypothetical datasize for a planned write, used to
  compute the `datanum` field (saved at `stack[28]`)
- **R6** = pointer to caller's 24-byte `BupStat` output struct
  (saved to R13 in the body):
  ```
  +$00  Uint32 totalsize      // BE; total bytes in device
  +$04  Uint32 totalblock     // BE; total 64-byte blocks
  +$08  Uint32 blocksize      // BE; 64 (internal) or varies
  +$0C  Uint32 freesize       // BE; free bytes
  +$10  Uint32 freeblock      // BE; free blocks
  +$14  Uint32 datanum        // BE; # files of `R5` size
                              //     that fit in free space
  ```
- **R0** return:
  - `0` = success, `*R6` filled with 24 bytes of stat data
  - `1` = BUP_NON (R4==2: no cart inserted)
  - `2` = BUP_UNFORMAT (R4!=2: backup RAM lacks the magic
    string)
  - `3` = BUP_WRITE_PROTECT (R4==2 cart-status code `$23`)
  - `-1` (`$FFFFFFFF`) = generic device error
- **Memory read**: internal backup RAM at `$00180000` - walks
  all directory entries to count active blocks
- **Memory written**: 24 bytes at `*R6`
- **BIOS computation** (internal-RAM path; formulas verified
  against the slot 3 body's finalize section at +$09A0..+$09D0):
  ```python
  total_block = 512                                # 32 KB / 64
  block_size  = 64
  total_size  = total_block * block_size           # 32768

  used        = 2 + sum(len(entry.block_list) for entry in dir)  # +2 = header + reserved
  free_block  = total_block - used
  eff_bytes   = block_size - 6                     # = 58 ($09A4 ADD #-6,R2)
  free_size   = free_block * eff_bytes - 30        # ($09A8 MUL.L; $09AC ADD #-30,R2)

  blocks_per_file = (datasize + 29) // eff_bytes + 1   # ($09BC/$09C6/$09C8)
  datanum         = free_block // blocks_per_file       # ($09CC BSRF division)
  ```
  The `-6` per-block overhead and `-30` total format overhead
  are constants subtracted by the body; their precise meaning
  is not fully reverse-engineered, but the numbers match the
  BUP layout (4-byte block header plus 2-byte slack per block,
  and 30-byte in-entry block-list slack per file). Slot 7's
  per-entry `BupDir.blocksize` calculation uses the same
  `effBytes` denominator so SDK cross-checks stay consistent.
- The first 2 blocks (header at block 0 + reserved at block 1)
  are always "used"; each file's dir entry counts as a block in
  its allocation.
- **Inner subs**: R4==2 path calls `+$266E` (BSRF disp `$1FB6`
  at +$06B4) and then `+$25C8` (BSRF disp `$1EE8` at +$06DC) for
  multi-field response decoding; R4!=2
  path is the slot 3 internal-RAM scanning code documented in
  the Internal-RAM BUP path section.

### Slot 4 (`+$0A78`) - BUP_Write calling convention

This slot's body is shared with the PER driver's slot 4
(peripheral packet build) - the same body code handles both
purposes, dispatching on R4. The peripheral-mode behavior is
documented in [peripheral_driver.md](peripheral_driver.md); the
BUP_Write internal-RAM (R4 != 2) path is documented under
Internal-RAM BUP path below, and the external-cart (R4 == 2)
inner sub `+$32A6` is documented in External A-bus BUP cart
subs at the end of this file.

- **SDK function**: `Sint32 BUP_Write(Uint32 device, BupDir *dir, Uint8 *data, Uint8 owsw)`
- **R4** = driver channel
- **R5** = pointer to 34-byte `BupDir` struct:
  ```
  +$00  Uint8  filename[12]   // 11 ASCII + NUL
  +$0C  Uint8  comment[11]    // 10 ASCII + NUL
  +$17  Uint8  language       // 0-5
  +$18  Uint32 date           // BE; from BUP_SetDate (slot 10)
  +$1C  Uint32 datasize       // BE; bytes in *R6
  +$20  Uint16 blocksize      // BE; output field, 0 in a write request
  ```
- **R6** = pointer to caller's data buffer (`datasize` bytes)
- **R7** = overwrite switch. **Semantics are inverted from the
  parameter name**: per slot 4 disasm at `+$B40..+$B54`, real
  BIOS treats this as a "no-overwrite" flag:
  - `0` = OVERWRITE if the file already exists (BSR `+$17BA`
    to erase the matched entry, then fall through to creation)
  - non-zero = DON'T OVERWRITE; return `6` (BUP_FOUND)
  This is what NiGHTS relies on: it issues all `BUP_Write`
  calls with R7=0 and expects updates to existing saves to
  proceed by replacing the entry.
- **R0** return:
  - `0` = success, file written
  - `1` = BUP_NON (device not connected)
  - `2` = BUP_UNFORMAT (backup RAM lacks magic header)
  - `3` = BUP_WRITE_PROTECT
  - `4` = BUP_NOT_ENOUGH_MEMORY (not enough free blocks for the
    requested datasize)
  - `6` = BUP_FOUND (file already exists and `owsw != 0`)
  - `-1` (`$FFFFFFFF`) = generic device error (cart-status fall-through)
- **Memory read**: caller's BupDir struct at `*R5`, payload
  at `*R6`
- **Memory written**: internal RAM at `$00180000` - either
  a new directory entry at the first free 64-byte block in the
  entry chain, or - if the filename matches an existing entry
  and `owsw == 0` - erasing the existing entry first (via the
  same `+$17BA` routine that BUP_Delete uses) and then writing
  the new entry in its place.
- **Inner subs**: R4==2 path calls `+$32A6` (cart-protocol);
  R4!=2 path delegates through slot 3 + helpers `+$1434`,
  `+$17BA`, `+$0EBC`, `+$1004` (documented in Internal-RAM
  BUP path section).

### Slot 5 (`+$135A`) - BUP_Read calling convention

Slot body at `+$135A..+$1432`. Verified against `sega_bup.h`
(`BUP_HK_READ` dispatches through `mem.L[VECTOR_ADDRESS+20]`)
and against the body, which saves caller's R5 and R6 onto its
own stack frame and passes R5 (the filename pointer) to
`+$1434` for per-entry filename matching - the classic Read
flow.

- **SDK function**: `Sint32 BUP_Read(Uint32 device, Uint8 *fname, Uint8 *data)`
- **R4** = driver channel (R4==2 selects external-cart path;
  other values select internal-RAM path)
- **R5** = pointer to 12-byte filename buffer (11 ASCII + NUL),
  saved at `stack[4]` on entry
- **R6** = pointer to caller's data buffer (caller-sized via a
  prior BUP_Stat to at least the matching BupDir entry's
  `datasize`), saved at `stack[0]` on entry
- **R0** return:
  - `0` = success, `*R6` filled with file's payload bytes
  - `1` = BUP_NON (device not connected)
  - `2` = BUP_UNFORMAT (RAM lacks the "BackUpRam Format" header)
  - `3` = BUP_WRITE_PROTECT
  - `5` = BUP_NOT_FOUND (no directory entry matches filename)
  - `-1` (`$FFFFFFFF`) = general device error (cart-status byte
    fell through the slot's known-code switch)
- **Memory read**: internal backup RAM at `$00180000` - the
  matching dir entry's block chain (in-entry list + optional
  continuation page)
- **Memory written**: bytes `0..datasize-1` at `*R6`, copied
  60 bytes per data block (skipping each block's 4-byte
  header) plus the trailing payload portion of any
  continuation page
- **Inner subs**: R4==2 path BSRFs to `+$2FD0` (cart-protocol
  status walker, shared with slot 8 BUP_Verify) via disp
  `$1C48` at +$1384; R4!=2 path BSRs `+$0672` (slot 3 /
  BUP_Stat prelude) then `+$1434` (per-entry filename match)
  then `+$157C` (data copy from dir-entry block chain to
  caller buffer). R4!=2 details in Internal-RAM BUP path
  section.

### Slot 6 (`+$16DC`) - BUP_Delete calling convention

Slot body at `+$16DC..+$17B8`. Verified against `sega_bup.h`
(`BUP_HK_DELETE` dispatches through `mem.L[VECTOR_ADDRESS+24]`)
and against the body, which saves caller R5 (filename pointer)
onto stack[0] and passes it to `+$1434` for per-entry filename
matching followed by `+$17BA` (entry erase). BUP_Format
is at slot 2 (`+$03AA`), not here - the body has no magic-string
write, no bitmap rewrite, and no Format branch.

- **SDK function**: `Sint32 BUP_Delete(Uint32 device, Uint8 *fname)`
- **R4** = driver channel
- **R5** = filename pointer (12-byte NUL-terminated, like
  BUP_Read), saved at `stack[0]` on entry
- **R6** = not used by the SDK API
- **R0** return:
  - `0` = success, matching directory entry's status byte
    bit 7 cleared (plus full body zero in mode 1)
  - `1` = BUP_NON (R4==2: no cart inserted)
  - `2` = BUP_UNFORMAT
  - `3` = BUP_WRITE_PROTECT (R4==2 cart-status code `$23`)
  - `5` = BUP_NOT_FOUND (filename did not match any entry)
  - `-1` (`$FFFFFFFF`) = generic device error
- **Memory effect**: depends on `driver_base+$34` (BUP_Init mode):
  - **Mode 0** (`+$34 == 0`): clears bit 7 of byte `+$00` of
    the matched dir entry; bytes `+$01..+$03` are read then
    written back unchanged. The status word goes from
    `$80000000` to `$00000000` for BIOS-active entries (since
    bytes 1-3 are already 0). Filename, comment, and block list
    at `+$04..+$3F` are LEFT IN PLACE.
  - **Mode 1** (`+$34 == 1`): same status-byte clear, AND zeros
    backup bytes `+$04..+$3F` of the dir entry (filename,
    comment, language, date, datasize, and the in-entry block
    list). The dir entry block is functionally all-zero after.
    This is the path NiGHTS's `BUP_Init` configures.
  - Continuation pages and payload blocks claimed by the
    deleted entry are NOT touched. They become free implicitly
    on the next free-block scan because the dir entry's status
    bit 7 is now zero (so the directory walk skips it and its
    claimed block set is empty).
- **Inner subs**: R4==2 path BSRFs to `+$279C` (cart-protocol
  cmd/ack) via disp `$1098` at +$1700; R4!=2 path BSRs `+$0672`
  (slot 3 / BUP_Stat prelude), `+$1434` (per-entry filename
  match), and `+$17BA` (clear active-bits). R4!=2 details in
  Internal-RAM BUP path section.

### Slot 8 (`+$1ACC`) - BUP_Verify calling convention

This slot's body is shared with the PER driver's slot 8; the
peripheral-mode behavior is documented in
[peripheral_driver.md](peripheral_driver.md). The BUP_Verify
internal-RAM (R4 != 2) path is in Internal-RAM BUP path below,
and the external-cart (R4 == 2) inner sub `+$2FD0` (shared with
slot 5 BUP_Read) is in External A-bus BUP cart subs at the end
of this file.

- **SDK function**: `Sint32 BUP_Verify(Uint32 device, Uint8 *fname, Uint8 *data)`
- **R4** = driver channel
- **R5** = filename pointer
- **R6** = pointer to data buffer to compare against
- **R0** return:
  - `0` = success, file data matches `*R6` byte-for-byte
  - `1` = BUP_NON (device not connected)
  - `2` = BUP_UNFORMAT
  - `3` = BUP_WRITE_PROTECT (R4==2 cart-status code `$23`)
  - `5` = BUP_NOT_FOUND (R4!=2 path: filename did not match any
    directory entry)
  - `7` = BUP_NO_MATCH (entry found but bytes do not match)
  - `8` = BUP_BROKEN (matched entry's block list contains an
    out-of-range block index; via `+$1BB4`)
  - `-1` (`$FFFFFFFF`) = generic device error
- **Memory read**: internal RAM `$00180000`; no writes
- **Inner subs**: R4==2 path calls `+$2FD0` (shared with slot 5);
  R4!=2 path delegates through slot 3 + helpers `+$1434`
  (filename match) and `+$1BB4` (byte compare), both documented
  below.

### Slot 7 (`+$18B4`) - BUP_Dir (full slot body)

The slot 7 body is documented in full here because it's BUP-only
(no peripheral-input variant). Also called inline by slot 4 (via
BSR `+$18B4`).

**Calling convention** (game-side view):

- **SDK function**: `Sint32 BUP_Dir(Uint32 device, Uint8 *fname, Uint16 dirsize, BupDir *dir)`
- **R4** = driver channel
- **R5** = filename pattern (12 bytes; NUL or `*` for wildcard
  match; pattern is matched against the filename of each
  directory entry)
- **R6** = `dirsize` = max number of `BupDir` entries to write
  (size of caller's `dir` array)
- **R7** = pointer to caller's `BupDir` array (`dirsize * 0x24`
  bytes; entries are written at the 0x24 (36)-byte `BupDir`
  stride - the 34 field bytes plus 2 alignment-padding bytes -
  see Slot 4 above)
- **R0** return:
  - `0` = no matching files
  - `N` (positive) = `N` matching files found, `*R7` filled
    with up to `min(N, R6)` entries
  - `-N` (negative) = more than `R6` matched (`|N|` is the
    total match count; only `R6` entries filled in `*R7`)
- **Memory read**: walks dir entries at `mem[$0080+]`,
  matches each filename against pattern
- **Memory written**: up to `R6 * 0x24` bytes at `*R7`

**BIOS implementation**:

- **Body**: `+$18B4` to `+$1ACA` (534 bytes)
- **Internal R-register usage (slot body level)**: R4 = port
  selector (2 = port-2 path); R5 = saved to stack[8]; R6 =
  saved to R8 (word-extended); R7 = caller's output array
  (saved to R12)
- **Returns** (R0): `0` = no matches (slot-3 prelude failed,
  port-2 marker clear, or zero matching entries); `N` = match
  count (positive, all fit); `-N` (NEG R14) = more than dirsize
  matched (only the first dirsize entries written)
- **Calls**:
  - BSRF `$0000130E` -> driver `+$2C08` (port-2 specialized read)
  - BSRF `$FFFFED38` (signed -$12C8) -> **slot 3 entry**
  - BSR `+$1434` (the directory iterator - R5=1 once to start,
    then R5=2 once per matched entry)
  - BSRF `$00001B88` (added to anchor `$0607DA88` = `$0607F610`
    = driver `+$3610` - shared division primitive, see
    [peripheral_driver.md](peripheral_driver.md))

```python
def bup_dir(device, fname, dirsize, dir_array): # R4, R5, R6, R7
    # Port-2 fast path
    if device == 2:
        if mem.B[driver_base + 0x56] == 0:      # port-2 marker
            return 0                             # no cart -> 0 matches

        # +$2C08 is handed fname, the output array, and a pointer to a
        # count cell preset to dirsize; its +$2860 per-entry chain writes
        # a value back through that cell.
        count_cell = dirsize
        status = sub_2C08(fname, dir_array, &count_cell)  # BSRF $130E at +$18F6
        if status == 0:
            return count_cell                    # callee-updated count
        if status == 0x0108:                    # "OK with count" indicator
            return -count_cell                   # more matches than buffer
        return 0                                 # other status -> 0

    # Non-port-2 path: validate device via slot 3 prelude.
    if slot3_stat(device=device, datasize=0, stat=stack_local) != 0:
        return 0                                 # slot 3 failed -> drop error, return 0

    # Walk matching dir entries via the +$1434 iterator and copy each
    # BupDir into the caller's output array (R7, held in R12) at the
    # 0x24-byte stride. R14 counts matches; entries are written only
    # while the count is below dirsize.
    match_count = 0                                  # R14
    entry = validate_1434(buf=stack[8], mode=1)      # +$1942: first match's block index
    while entry != 0:                                # +$1A9C loop top
        if match_count < dirsize:                    # +$195E gate
            out_base   = dir_array + match_count * 0x24
            entry_base = backup_base + entry * 0x80   # matched entry in internal RAM

            # Copy the dir entry's fields into the BupDir packet
            # (source read from the odd backup-RAM bytes):
            copy_entry_bytes(out_base + 0x00, count=11)   # filename
            mem.B[out_base + 0x0B] = 0                     # filename NUL
            mem.B[out_base + 0x17] = entry_language        # source language field
            copy_entry_bytes(out_base + 0x0C, count=10)   # comment
            mem.B[out_base + 0x16] = 0                     # comment NUL
            copy_entry_bytes(out_base + 0x18, count=4)    # date (BE u32)
            copy_entry_bytes(out_base + 0x1C, count=4)    # datasize (BE u32)

            # blocksize word at +0x20 via shared division sub +$3610:
            #   (datasize + 29) / (block_size - 6) + 1
            datasize  = mem.L[out_base + 0x1C]
            eff_block = mem.L[driver_base + 0x3C] - 6
            mem.W[out_base + 0x20] = (unsigned_div(datasize + 29, eff_block) + 1) & 0xFFFF

        match_count += 1                             # +$1A92
        entry = validate_1434(buf=stack[8], mode=2)  # +$1A96: next match's block index

    # positive count if within bound, negative if more matched than
    # the caller's buffer could hold
    if match_count > dirsize:
        return -match_count
    return match_count
```

Pool:
- `+$191C`: `$0108` (port-2 sub "OK with count" indicator)
- `+$1924`: `$06000340` (BIOS function-pointer slot)
- `+$1928`: `$06000354` (driver-registration slot)
- `+$192C`: `$0000130E` (BSRF to driver `+$2C08`)
- `+$1958`: `$FFFFED38` (BSRF to slot 3 - relative)
- `+$1AB0`: `$00001B88` (BSRF disp from +$1A84 -> driver `+$3610`)

**External references made by slot 7**:

| Source | Target | Purpose |
|--------|--------|---------|
| BSRF `$130E` | driver `+$2C08` | Port-2 specialized read |
| BSRF `$FFFFED38` (relative) | **slot 3 entry** | Data fetch via SMPC INTBACK |
| BSR `+$1434` | sub at `+$1434` | Directory iterator (R5=1 to start, R5=2 per match) |
| BSRF `$1B88` | driver `+$3610` | Division (per-packet header) |

**Behavior summary**: slot 7 implements BUP_Dir. The port-2 fast
path bottoms out at `+$2C08` and translates its return code (R0 == 0
-> return the callee-updated count cell; R0 == $108 -> return its
negation; else -> return 0).
The non-port-2 (internal-RAM) path calls slot 3 once, then walks
matching directory entries via the `+$1434` iterator:
1. `+$1434` (R5=1) returns the first matching entry's block index;
   the loop continues while the returned index is non-zero,
   re-calling `+$1434` (R5=2) at the end of each pass to get the
   next match
2. For each match (while the match counter R14 < dirsize) it copies
   the entry's BupDir fields into the caller's output array (R7) at
   the `0x24`-byte stride: filename/comment/language/date/datasize
   at +0/+0x0C/+0x17/+0x18/+0x1C
3. Sets the BupDir blocksize word at +0x20 via the `+$3610` division
   sub: `(datasize + 29) / (block_size - 6) + 1`
4. Returns R14 (negated if it exceeded dirsize)

Notes:
- Slot 4's `BSR +$18B4` call **is** this function - the same body
  serves both as slot 7's table entry and as a callable
  sub-routine within slot 4's flow.
- Pool constants `$0108`, `$06000340`, `$06000354` at +$191C-$1928
  identify port-2 status / WRAM-H slots.

### Slot 9 (`+$1E70`) - BUP_GetDate (packed Uint32 -> BupDate)

- **SDK function**: `void BUP_GetDate(Uint32 pdate, BupDate *date)`
- **Body**: `+$1E70` to `+$1F46` (216 bytes, ~85 instructions)
- **Inputs**:
  - R4 = packed date as Uint32 (minutes since 1980-01-01 00:00)
  - R5 = output `BupDate *` pointer (saved to R14):
    ```
    BupDate { Uint8 year; Uint8 month; Uint8 day;
              Uint8 time; Uint8 min; Uint8 week; }
    ```
- **Returns** (R0): packed value used for the final byte write
  (caller can ignore; primary outputs are at R5)
- **Calls** (all via BSRF to entry-points around `+$3610` /
  `+$3780`):
  - `+$3610` (unsigned 32/32 division) at +$1E80 (days),
    +$1E94, +$1F00, +$1F32 (day-of-year split)
  - `+$3780` (unsigned division, R4-preserving variant) at
    +$1E8C, +$1E9E, +$1EB4, +$1EDE, +$1EEA, +$1F0A
  - BSR `+$1DC4` (day-of-year -> month/day-of-month) at
    +$1F18, +$1F2A
- **Side effects** (writes to BupDate at R14):
  - `R14+0` = year (years since 1980)
  - `R14+1` = month (1-12)
  - `R14+2` = day-of-month (1-31)
  - `R14+3` = hour (0-23)
  - `R14+4` = minute (0-59)
  - `R14+5` = week (day of week, Sun=0..Sat=6; `(total days
    since 1980-01-01 + 2) % 7`, less 1 once days > 43889)

**Algorithm** (date code is total minutes since 1980-01-01):

```python
def bup_getdate(packed, date):                  # R4 = Uint32 minutes-since-1980, R5 = BupDate*
    # Pool values: 1440 (min/day), 1461 (days/4-year cycle), 365, 366.
    days, minutes_in_day = divmod(packed, 1440)             # +$1E80 (3610) / +$1E8C (3780)
    hour                 = minutes_in_day // 60             # +$1E94 (3610)
    minute               = packed % 60                      # +$1E9E (3780; == minutes_in_day % 60)

    # Day-of-week from total days since 1980-01-01 (a Tuesday). Offset is +2,
    # dropping to +1 once days > 43889 (= 2100-03-01), since the year decode
    # below uses uniform 1461-day cycles and does not skip the 2100 leap day.
    week = (days + (1 if days > 43889 else 2)) % 7          # +$1EA8 cmp; +$1EB4 / +$1EDE (3780)

    # Year via 4-year cycle (1461 = 4 * 365 + 1 leap day). The first year of
    # each cycle is the leap year (366 days).
    quad         = days // 1461                             # +$1F32 (3610)
    days_in_quad = days % 1461                              # +$1EEA (3780)
    if days_in_quad <= 366:                                 # +$1EF2 CMP/HI 366
        year_in_quad, day_in_yr, leap = 0, days_in_quad, 1  # leap year of the cycle
    else:
        year_in_quad = (days_in_quad - 1) // 365            # +$1F00 (3610)
        day_in_yr    = (days_in_quad - 1) %  365            # +$1F0A (3780)
        leap         = 0

    month, day_of_month = sub_1DC4(day_in_yr, leap)         # sub_1D96 table; leap picks Feb length

    mem.B[date + 0] = quad * 4 + year_in_quad                # year (years since 1980)
    mem.B[date + 1] = month                                  # 1-12
    mem.B[date + 2] = day_of_month                           # 1-31
    mem.B[date + 3] = hour
    mem.B[date + 4] = minute
    mem.B[date + 5] = week
```

Pool values used:
- `$05A0` = 1440 (minutes per day)
- `$05B5` = 1461 (days in 4-year cycle, leap-aware)
- `$016D` = 365 (days per normal year)
- `$016E` = 366 (days per leap year)
- `$AB71` = 43889 (days since 1980-01-01 = 2100-03-01; week-offset
  switch threshold)

#### Leap-rollover boundary and the SDK correction

The `+$1E70` routine above selects the leap year of each 4-year
cycle with `days_in_quad <= 366` (CMP/HI #366 at +$1EF2). The
`== 366` case is Jan 1 of the year following each leap year
(1981, 1985, 1989, ...): the routine keeps `year_in_quad = 0` and
passes day-of-year 366 into `+$1DC4`, which walks past the
December table entry and produces an out-of-range month/day (it
reports month 12, day 32 rather than year+1, Jan 1).

SEGA later corrected this in the backup-library distribution: the
`sega_bup.h` revision history records "Ver.1.23 (1997-01-08):
patch applied for a bug in BUP_GetDate's date calculation". The
corrected algorithm gates the leap year with a strict `< 366`
comparison so the boundary day rolls into the next year:

```python
    quad         = days // 1461
    days_in_quad = days % 1461
    if days_in_quad < 366:                      # corrected: strict <
        year_in_quad, day_in_yr, leap = 0, days_in_quad, 1
    else:
        after_leap   = days_in_quad - 366
        year_in_quad = 1 + after_leap // 365
        day_in_yr    = after_leap %  365
        leap         = 0
    year = quad * 4 + year_in_quad
```

The non-leap arithmetic is algebraically identical to the ROM's
`(days_in_quad - 1)` form for `days_in_quad > 366`; the only
behavioral difference is the boundary day. `day_in_yr` and the
leap flag then feed the same `+$1DC4` month/day split. The
week field is `(days + 2) % 7` for all dates in the corrected
form (the ROM's `> 43889` drop to `+1` compensated for its
uniform-cycle decode and is not part of the correction).

### Slot 10 (`+$1F64`) - BUP_SetDate (BupDate -> packed Uint32)

- **SDK function**: `Uint32 BUP_SetDate(BupDate *date)`
- **Body**: `+$1F64` to `+$2000` (158 bytes, ~78 instructions)
- **Inputs**:
  - R4 = `BupDate *` pointer (saved to R14):
    - `R14+0` = year (years since 1980)
    - `R14+1` = month (1-12)
    - `R14+2` = day-of-month (1-31)
    - `R14+3` = hour (0-23)
    - `R14+4` = minute (0-59)
    - `R14+5` = week (ignored; BUP_GetDate recomputes during
      packed-to-BupDate round-trip)
- **Returns** (R0): packed Uint32 = total minutes since 1980-01-01
  00:00 UTC
- **Calls**:
  - BSR `+$1D96` (fills days-before-month table on stack at
    +$1F6E)
  - BSRF `+$355C` (signed 32/32 division at +$1F7C) - year ->
    days conversion
  - BSRF `+$36B8` (signed division alternate at +$1F8A) -
    leap-year remainder
- **Frame**: 44-byte stack (holds 11-longword days-before-month
  table that `+$1D96` fills)

Pool ($2002..$200F):

| Offset | Value | Purpose |
|--------|-------|---------|
| $2002 | $05B5 | 1461 (4-year cycle days) |
| $2004 | $016D | 365 |
| $2006 | $05A0 | 1440 (minutes per day) |
| $2008 | $000015DC | BSRF disp -> +$355C (signed division) |
| $200C | $0000172A | BSRF disp -> +$36B8 (signed division alt entry) |

```python
def bup_setdate(date):                          # R4 = BupDate*
    year   = mem.B[date + 0]                     # years since 1980
    month  = mem.B[date + 1]                     # 1-12
    day    = mem.B[date + 2]                     # 1-31
    hour   = mem.B[date + 3]
    minute = mem.B[date + 4]
    # date+5 (week) is ignored on encode.

    # sub_1D96 fills an 11-entry days-before-month table on the local
    # 44-byte stack frame (Feb..Dec inclusive; January is the CMP/EQ #1
    # short-circuit at +$1FA6 (-> +$1FD0)).
    days_before_month = [0] * 11
    sub_1D96(days_before_month)

    # Step 1: year decomposition
    #   days = (year / 4) * 1461 + (year mod 4) * 365 + 1
    # The +1 is the leap day from year 0; year 1980 has year_in_quad = 0,
    # so the if-condition is false and no adjustment is added - that
    # makes 1980 itself the leap year of the first 4-year cycle.
    quad_year_count = signed_div(year, 4)                    # +$355C
    days            = quad_year_count * 1461
    year_in_quad    = signed_mod(year, 4)                    # +$36B8
    if year_in_quad != 0:
        days += year_in_quad * 365 + 1

    # Step 2: month bucket (skip table for January and invalid months >=13)
    if 2 <= month <= 12:
        days += days_before_month[month - 2]
        if month > 2 and year_in_quad == 0:
            days += 1            # leap year of cycle: non-leap table needs +1 for Mar-Dec

    # Step 3: day-of-month, hour, minute -> total minutes since epoch
    days += day - 1
    return days * 1440 + hour * 60 + minute
```

## Internal-RAM BUP path

This is the BIOS code path that the SDK BUP library actually
calls into for game saves to internal backup RAM, and the path
that wrote the `cart.srm` samples documented above. The path is
the `R4 != 2` branch in each BUP slot body.

### Dispatch pattern

Every BUP slot (2/3/4/5/6/7/8) opens with the same dispatch:

```python
def bup_slot(R4, R5, R6, R7):
    save_callee_registers()
    if R4 == 2:
        # External A-bus BUP cart path
        if mem.B[driver_base + 0x56] == 0:       # port-2 marker
            return 1                              # BUP_NON: no cart inserted
        result = cart_protocol_sub(...)          # BSRF to cart-side helper
        return translate_cart_status(result)
    else:
        # Internal-RAM (or peripheral, for slot 3) path
        ...
```

The R4!=2 path is the internal-RAM handler. The exact entry
offset per slot:

| Slot | Body entry | R4!=2 dispatch | R4!=2 first instruction |
|------|-----------|----------------|-------------------------|
| 2 | `+$03AA` | BF/S `+$042C` at `+$03DA` | `BSR +$0388` (bitmask init) |
| 3 | `+$0672` | BF/S `+$0748` at `+$0696` | `BSR +$0388`, then BRA `+$0888` |
| 4 | `+$0A78` | BF/S `+$0B20` at `+$0A9A` | `BSR +$0672` (calls slot 3) |
| 5 | `+$135A` | BF `+$13F0` at `+$1366` | `BSR +$0672` (calls slot 3) |
| 6 | `+$16DC` | BF `+$176C` at `+$16E6` | `BSRF $FFFFEEFC` (= slot 3) |
| 7 | `+$18B4` | BF/S `+$1930` at `+$18D8` | `BSRF $FFFFED38` (= slot 3) |
| 8 | `+$1ACC` | BF `+$1B66` at `+$1AD8` | `BSRF $FFFFEB02` (= slot 3) |

Most BUP slot R4!=2 paths immediately call **slot 3** (BSR or
BSRF), making slot 3 the central work routine for internal-RAM
operations. Slots 2 and 3 also use bitmask helper `+$0388`.

### Slot 3 (`+$0672`) - BUP_Stat (also called as internal-RAM prelude)

- **Body**: `+$0672` to `+$09E8` (888 bytes, 444 instructions
  - longest BUP slot)
- **Inputs** (per SDK contract for `BUP_Stat`):
  - R4 = device code (2 = R4==2 path for external cart; 0 or 1
    take the internal-RAM path, see the $0888 dispatch; any other
    value returns BUP_NON)
  - R5 = planned-write datasize (used to compute the `datanum`
    field of BupStat; saved at stack[28]). Internal-slot
    callers using slot 3 as a prelude pass R5=0 because they
    do not care about `datanum`.
  - R6 = caller's 24-byte BupStat output pointer (saved to R13).
    Internal-slot callers pass a stack-local pointer; the body
    still writes 24 bytes there but the caller ignores them.
- **Returns** (R0): 0 = success, 1 = BUP_NON (R4==2 path, no
  port-2 marker = no cart inserted), 2 = BUP_UNFORMAT (magic
  string missing on internal RAM), 3 / -1 = device error paths
- **Calls** (R4==2 path): BSRF to `+$266E` (external cart read);
  BSRF to `+$25C8` (external cart stat)
- **Calls** (R4!=2 path): BSR `+$0388` (bitmask init); BSR
  `+$09EA` (magic scan, called twice); BSRF disp `$1576` at
  `+$07B6` -> `+$1D30` (count active bits)
- **Frame**: 68-byte stack

Slot 3 is also BSR'd as a "validate device + load directory
state" prelude by slots 2 (BUP_Format), 4 (BUP_Write), 5
(BUP_Read), 6 (BUP_Delete), 7 (BUP_Dir), and 8 (BUP_Verify).
Those callers want the side effects (device validation +
driver-state population) and discard the BupStat output.

Pool (longwords/words referenced from this slot body):

| Offset | Value | Purpose |
|--------|-------|---------|
| `+$0762` | `$0101` | cart status code -> BUP_NON |
| `+$0764` | `$0106` | cart status code -> BUP_NON |
| `+$06F4` | `$06000354` | driver-base registration slot |
| `+$06F8` | `$00001FB6` | BSRF disp -> `+$266E` (cart read) |
| `+$06FC` | `$00001EE8` | BSRF disp -> `+$25C8` (cart stat) |
| `+$07EE` | `$0200` | mode / entry-count hint |
| `+$07F0` | `$0080` | first dir-entry word offset |
| `+$07F2` | `$01FE` | scan sentinel |
| `+$07F4` | `$00008000` | totalsize (32 KB total backup RAM) |
| `+$07F8` | `$20180000` | BUP RAM base (cache-through) |
| `+$07FC` | `$20180080` | first dir entry |
| `+$0800` | `$00001576` | BSRF disp -> `+$1D30` (popcount) |
| `+$08E0` | `$24000000` | extended RAM base (sub-mode 1) |
| `+$08E4` | `$06000354` | driver-base slot (duplicate) |
| `+$09B4` | `$06000354` | driver-base slot (duplicate) |
| `+$0A6C` | `$00002C48` | BSRF disp -> `+$3610` (division) |
| `+$0A70` | `$00002C40` | BSRF disp -> `+$3610` (division, 2nd) |

```python
def bup_stat(device, datasize, stat):           # R4, R5, R6
    # R4==2: External A-bus BUP cart path
    if device == 2:
        if mem.B[driver_base + 0x56] == 0:       # port-2 marker
            return 1                              # BUP_NON: no cart

        rc = sub_266E(stack_local)               # cart read; BSRF disp $1FB6 -> +$266E
        if rc == 0x23:               return 3    # BUP_WRITE_PROTECT
        if rc == 0x24:               return 2    # BUP_UNFORMAT
        if rc in (0x0101, 0x0106):   return 1    # device unusable -> BUP_NON
        if rc != 0:                  return -1   # generic error

        # rc == 0: fill size fields from the cart read, then cart stat
        stat[0] = stack[52]; stat[4] = stack[56]; stat[8] = word(stack[60])
        rc2 = sub_25C8(stat)                     # cart stat; BSRF disp $1EE8 -> +$25C8
        if rc2 == 0x21:              return 1    # BUP_NON
        if rc2 == 0x23:              return 3    # BUP_WRITE_PROTECT
        if rc2 == 0x24:              return 2    # BUP_UNFORMAT
        if rc2 != 0:                 return -1   # generic error
        stat[0x14] = stack[20]                   # datanum
        return 0

    # R4!=2: Internal-RAM path

    # Init 8-byte bitmask table at stack+40 via shared helper.
    bitmask = [0x80, 0x40, 0x20, 0x10, 0x08, 0x04, 0x02, 0x01]    # sub_0388

    # Sub-mode dispatch ($0888) is on the device code (R4): 0 or 1
    # select the driver-state layout; any other value returns BUP_NON.
    #   device 0 -> internal backup RAM at $20180000
    #   device 1 -> extended RAM at $24000000 (parameters supplied via
    #               driver_base+$58/$5C/$60/$64 from the caller)
    if sub_mode == 0:
        if mem.B[driver_base + 0x54] == 0:       # port-1 marker
            return 1                              # BUP_NON
        stat[0] = 0x8000                          # totalsize (32 KB)
        stat[4] = 0x0200                          # totalblock (512)
        stat[8] = 64                              # blocksize
        mem.L[driver_base + 0x30] = 0x20180000   # BUP RAM cache-through base
        mem.L[driver_base + 0x34] = 0            # mode flag (no IRQ masking)
        mem.L[driver_base + 0x38] = 0x0200       # entry count
        mem.L[driver_base + 0x3C] = 64           # block size
        mem.L[driver_base + 0x40] = 0x0080       # per-entry stride (2 * block size)
        mem.L[driver_base + 0x44] = 2            # first scanned entry index
        mem.L[driver_base + 0x4C] = 0x20180080   # first dir entry
    else:                                         # sub_mode == 1
        if mem.B[driver_base + 0x55] == 0:       # port-1 alt marker
            return 1
        mem.L[driver_base + 0x38] = mem.L[driver_base + 0x58]
        mem.L[driver_base + 0x3C] = mem.L[driver_base + 0x5C]
        mem.L[driver_base + 0x44] = mem.L[driver_base + 0x60]
        mem.L[driver_base + 0x34] = mem.L[driver_base + 0x64]
        mem.L[driver_base + 0x30] = 0x24000000
        mem.L[driver_base + 0x40] = mem.L[driver_base + 0x3C] * 2
        mem.L[driver_base + 0x4C] = (mem.L[driver_base + 0x30]
                                       + mem.L[driver_base + 0x40])
        stat[0] = mem.L[driver_base + 0x38] * mem.L[driver_base + 0x3C]  # totalsize
        stat[4] = mem.L[driver_base + 0x38]      # totalblock (entry count)
        stat[8] = mem.L[driver_base + 0x3C]      # blocksize

    # Scan for "BackUpRam Format" magic at the configured RAM base.
    if sub_09EA() != 0:
        return 2                                  # BUP_UNFORMAT

    # Count active entries via the popcount sub.
    file_count = sub_1D30()                       # BSRF disp $1576 -> +$1D30

    # Per-entry walk: emit metadata for each entry that is marked
    # active in its status byte AND has its bitmap bit clear. i runs
    # from driver_base+$44 (first scanned index) to the entry count;
    # the address advances by the stride driver_base+$40.
    stride       = mem.L[driver_base + 0x40]
    used_blocks  = 0
    output_count = 0
    for i in range(mem.L[driver_base + 0x44], mem.L[driver_base + 0x38]):
        entry_ram = mem.L[driver_base + 0x30] + i * stride
        if not (mem.B[entry_ram + 1] & 0x80):    # high bit of status byte
            continue
        bitmap_byte = mem.B[mem.L[driver_base + 0x4C] + (i >> 3) * 2 + 1]
        if bitmap_byte & bitmask[i & 7]:         # skip when the bitmap bit is set
            continue

        used_blocks += 1
        # Copy per-entry metadata (filename/date/datasize/block list)
        # into the working buffer at mem.L[driver_base+$2C].
        copy_entry_metadata(entry_ram, output_index=output_count)
        output_count += 1

    # Finalize BupStat output at *stat = R13.
    scan_length = stack[36]                      # set during driver-state setup
    free_block  = scan_length - used_blocks - file_count
    stat[0x10]  = free_block                      # freeblock

    if free_block > 0:
        eff_bytes  = mem.L[driver_base + 0x3C] - 6    # = 58 for block_size 64
        stat[0x0C] = free_block * eff_bytes - 30      # freesize
    else:
        stat[0x0C] = 0

    # `datanum` = how many files of size `datasize` fit in free space.
    blocks_per_file = (datasize + 29) // eff_bytes + 1     # +$3610 (BSRF)
    stat[0x14]      = free_block // blocks_per_file         # +$3610 (BSRF, 2nd)

    return 0
```

All byte-level RAM access uses the odd-byte pattern:
`mem.B[$20180001 + 2*offset]` (where the SH-2 sees the
backup-RAM SRAM through the lower byte of each word).

### Slot 2 (`+$03AA`) R4!=2 path - BUP_Format internal-RAM

- **Body**: `+$042C` to `+$066E` (580 bytes - shares slot 2's
  epilogue at +$065C with the R4==2 path)
- **Inputs** (as called from slot 2 body's dispatch):
  - R10 = stack-frame base + 28 (per-entry scratch area)
  - R12 = stack-frame base + 8 (filename buffer)
  - R13 = 0 (zero constant)
  - R14 = `$06000354` (driver_base WRAM-H slot)
  - R11 = `$06000340` (BIOS function-pointer slot)
- **Returns** (R0): `0` = format written and verified; `8` =
  BUP_BROKEN (status-byte readback verify failed); `1`/`3`/`-1`
  passed through from the slot 3 prelude (BUP_NON /
  BUP_WRITE_PROTECT / error) when its rc is not 0 or 2
- **Calls**: `+$0388` (bitmask init), `+$033E` (write
  "BackUpRam Format" string to stack scratch for compare),
  `+$0672` (slot 3 = internal-RAM scan), JSR through
  `*$06000340` (BIOS IMS mask/restore), JSR `@R9` -> `+$2010`
  (busy-wait delay between commit phases, mode 1 only)

```python
def bup_format_internal():                       # entered at +$042C
    # Init bitmask {$80..$01} at stack+28.
    sub_0388(stack + 28)                         # +$042C
    # Write "BackUpRam Format" magic string to stack+8 scratch.
    sub_033E(stack + 8)                          # +$0430
    # Validate device + prime driver state via slot 3 (datasize=0).
    rc = bup_stat(R4, 0, stack + 36)             # +$043A -> slot 3
    if rc not in (0, 2):                          # 0 = OK; 2 = unformatted (expected)
        return rc

    # Zero per-entry status bytes in the driver's working buffer.
    for i in range(mem.L[driver_base + 0x38]):
        mem.B[mem.L[driver_base + 0x2C] + i] = 0

    # Mode 1: mask all SCU IRQs around the commit phase.
    if mem.L[driver_base + 0x34] == 1:
        saved_ims = mem.L[0x06000348]
        bios_ims_call(-1)                         # JSR *$06000340, R4 = $FFFFFFFF

    # Commit: write the 16-byte "BackUpRam Format" magic 4x to
    # fill block 0 (64 bytes), zero reserved block 1, and write
    # status bytes for all dir entries at $20180001 + 2*offset
    # (odd-byte SRAM bus pattern). In mode 1 each commit phase is
    # followed by a JSR @R9 busy-wait delay (+$2010).
    commit_format_to_backup_ram()

    # Read the status bytes back and verify they match the working
    # buffer. Restore IMS (mode 1) before returning on either path.
    if not status_bytes_readback_ok():
        if mem.L[driver_base + 0x34] == 1:
            bios_ims_call(saved_ims)              # restore IMS
        return 8                                  # BUP_BROKEN: write-verify failed
    if mem.L[driver_base + 0x34] == 1:
        bios_ims_call(saved_ims)                  # restore IMS
    return 0
```

### Slot 4 (`+$0A78`) R4!=2 path - BUP_Write internal-RAM

- **Body**: `+$0B20` to `+$0EB0` (912 bytes - shares slot 4's
  epilogue with the R4==2 path)
- **Inputs** (as called from slot 4 body's dispatch):
  - R11 = port selector (was caller's R4; only reached here if
    R11 != 2)
  - R13 = caller's R5 (BupDir struct ptr)
  - stack[24] = caller's R6 (data buffer)
  - mem.B[stack+0] = caller's R7 (overwrite-switch byte)
  - R14 = `$06000354` (driver_base WRAM-H slot)
- **Returns** (R0): per SDK BUP_Write codes
- **Calls**: `+$0672` (slot 3, called twice), `+$1434`
  (validate), `+$17BA` (cleanup), `+$1004` (post-fetch sort),
  `+$1BB4` (transform), `+$0EBC` (per-entry buffer commit), BSRF
  `$00002A8C` (= `+$3610` division helper, see
  peripheral_driver.md), JSR through `*$06000340` (BIOS IMS
  mask/restore, mode 1 only)

```python
def bup_write_internal(dir_ptr, data, owsw):     # entered at +$0B20
    # owsw semantics inverted from the parameter name: R7 == 0 means
    # "overwrite OK"; R7 != 0 means "do not overwrite, return BUP_FOUND".

    rc = bup_stat(R4, 0, stack + 76)             # +$0B26 -> slot 3 (first call)
    if rc != 0:
        return rc

    matched = validate_1434(buf=dir_ptr, mode=0) # +$0B3C
    if matched != 0:
        if owsw != 0:                            # R7 != 0 -> no-overwrite
            return 6                              # BUP_FOUND
        sub_17BA(matched)                        # +$0B54: erase existing entry
        bup_stat(R4, 0, stack + 76)              # +$0B58: re-scan after erase

    # Block-count math via shared +$3610 division.
    datasize        = mem.L[dir_ptr + 0x1C]
    eff_bytes       = mem.L[driver_base + 0x3C] - 6
    blocks_per_file = (datasize + 29) // eff_bytes + 1

    if blocks_per_file > mem.L[stack + 92]:
        return 4                                  # BUP_NOT_ENOUGH_MEMORY

    sub_1004(...)                                # +$0B96: sort + commit packet

    # Mode 1: mask SCU IRQs around the per-block payload write.
    if mem.L[driver_base + 0x34] == 1:
        saved_ims = mem.L[0x06000348]
        bios_ims_call(-1)                        # +$0BB4: JSR *$06000340, R4 = -1

    write_payload_to_blocks(data, datasize)      # $20180001 + 2*offset per-block

    if mem.L[driver_base + 0x34] == 1:
        bios_ims_call(saved_ims)                 # +$0E60: restore IMS

    result = sub_1BB4(...)                       # +$0E68: transform / finalize
    if mem.L[driver_base + 0x48] != 0:
        rc2 = sub_0EBC(...)                      # +$0E7E: per-entry buffer commit
        if rc2 != 0:
            result = rc2
    if mem.W[stack + 28] != 0:
        sub_17BA(...)                            # +$0E96: cleanup active-bits
    return result                                # sub_1BB4 result (0 on success)
```

### Slot 5 (`+$135A`) R4!=2 path - BUP_Read internal-RAM

- **Body**: spans `+$13F0` through the `+$1430` RTS (delay slot
  at `+$1432`); exits through three RTS sites (`+$1400`,
  `+$1422`, `+$1430`). A 4-byte unreachable gap sits at `+$1426`
  (after the `+$1422` RTS and its delay slot, no branch targets
  it)
- **Inputs** (as called from slot 5 body's dispatch):
  - stack[0] = caller's R6 (data buffer pointer)
  - stack[4] = caller's R5 (filename pointer)
- **Returns** (R0): `0` = OK (data copied), `5` = BUP_NOT_FOUND
  (filename did not match), `8` = BUP_BROKEN (block chain
  unreadable - propagated from `+$157C`), or propagates a slot-3
  error code
- **Calls**: `+$0672` (slot 3 / BUP_Stat prelude), `+$1434`
  (per-entry filename match), `+$157C` (data copy)

```python
def bup_read_internal(filename, data_buf):       # entered at +$13F0
    rc = bup_stat(R4, 0, stack + 8)              # +$13F4 -> slot 3
    if rc != 0:
        return rc
    matched = validate_1434(filename, mode=0)    # +$140E
    if matched == 0:
        return 5                                  # BUP_NOT_FOUND
    return sub_157C(matched, data_buf)           # +$141A: R4=matched, R5=data_buf; R0 propagated (0 OK / 8 BUP_BROKEN)
```

### Slot 6 (`+$16DC`) R4!=2 path - BUP_Delete internal-RAM

- **Body**: `+$176C` to `+$17B6` (~74 bytes)
- **Inputs**: stack[0] = caller's R5 (filename pointer)
- **Returns** (R0): `0` = OK, `5` = BUP_NOT_FOUND (filename did
  not match any entry), propagates slot-3 error otherwise
- **Calls**: `+$0672` (slot 3 / BUP_Stat prelude), `+$1434`
  (per-entry filename match), `+$17BA` (clear active-bit
  flags on the matched entry)

```python
def bup_delete_internal(filename):               # entered at +$176C
    rc = bup_stat(R4, 0, stack + 4)              # +$1772 BSRF -> slot 3 (stat buffer at R15+4)
    if rc != 0:
        return rc
    matched = validate_1434(filename, mode=0)    # +$178C
    if matched == 0:
        return 5                                  # BUP_NOT_FOUND
    sub_17BA(matched)                            # +$1798: erase matched entry
    return 0
```

**What `+$17BA` writes to the dir entry** (the actual erase):

The erase step has TWO paths gated on `driver_base+$34` (the
"mode" field set by `BUP_Init`):

1. **Status word** (both modes): reads logical bytes `+$00..+$03`
   (the 4-byte status word, stored at bus offsets +1/+3/+5/+7)
   into a local buffer and clears bit 7 of byte `+$00`. In
   **mode 0** the four bytes are written back verbatim, so
   `+$01..+$03` are preserved. In **mode != 0** the local copy is
   first rewritten as a big-endian longword
   `status = (status + 1) & $7F` (`+$181E..+$1826`); for a
   standard `$80000000` entry this makes the written-back status
   word `$00000001` (byte `+$03` becomes `$01`), so `+$01..+$03`
   are NOT preserved. Either way the four bytes are written back
   at `+$1840..+$1862`.

2. **Mode 1 only** (`+$186E..+$1892` loop): zeros backup bytes
   `+$04..+$3F` of the matched dir entry - filename, comment,
   language, date, datasize, and the full in-entry block list.
   Each iteration writes `R11 = 0` to the odd-byte bus address
   at `R13 + R14`, advancing `R14` from 9 to 127 in steps of 2.

In mode 1 the dir entry is functionally deleted afterward: bytes
4..63 are wiped and the status word is rewritten to `$00000001` -
bit 7 of byte 0 cleared (which marks the entry inactive), with
the low byte left holding `$01` from the longword increment
rather than `$00` for a former `$80000000` entry. The cleared
bit 7 is what classifies the entry as free; readers key off that
high byte, not the residual low byte. In mode 0 only the status byte's
bit 7 is cleared; the filename, comment, and block list survive
in place and are NOT considered "deleted" by callers that scan
by filename (such as the per-entry working buffer populator at
`driver_base+$78+` or the game-side save list).

NiGHTS's `BUP_Init` configures mode 1, so the full-erase path is
what real BIOS executes - this is why a BIOS-driven Delete makes
the save fully disappear from the game's save browser.

Continuation pages and payload blocks claimed by the deleted
entry are NOT touched by `+$17BA`. Those blocks are reclaimed
implicitly the next time `BUP_Write` runs the free-block scan
(any dir entry whose status bit 7 is now zero contributes no
"claimed" blocks).

### Slot 7 (`+$18B4`) R4!=2 path - BUP_Dir internal-RAM

- **Body**: `+$1930` to `+$1AB4` (~390 bytes)
- **Inputs**: per slot 7's R4-R7 contract (R5 = filename
  pattern, R6 = dirsize, R7 = output array)
- **Returns** (R0): match count (positive = N matched, all
  fit; negative = -N for too-many). A slot-3 prelude failure
  returns 0 - the error code is dropped, not propagated.
- **Calls**: `+$0672` (slot 3, once), `+$1434` (the directory
  iterator - R5=1 once to start, then R5=2 once per matched
  entry), `+$3610` (per-entry block-count division)

Per-instruction disassembly is captured under "Slot 7
(`+$18B4`) - BUP_Dir (full slot body)" above
- that section walks slot 7's complete body since slot 7
is single-purpose (the R4==2 path has no inline cart-protocol
code of its own - it just delegates to `+$2C08` when the
port-2 marker is set).

The R4!=2 entry point `+$1930` runs the match-and-copy loop.
Filename matching is done inside `+$1434` (which skips free
entries via the status bit-7 test and compares each entry's
filename against the pattern); the loop body only copies the
entry `+$1434` returns:

1. Initialises R14 = 0 (output / match counter)
2. BSR `+$1434` with R5=1 to start the scan; it returns the
   first matching entry's block index (0 = no match)
3. While the returned index is non-zero:
   - Reads that matched entry's metadata from internal RAM and
     copies the BupDir fields (filename, comment, language,
     date, datasize, plus a computed blocksize) into output
     slot R14 at the `$24`-byte stride. The copy is gated on
     R14 < R6 (dirsize); past that, R14 keeps counting but
     nothing more is written.
   - Bumps R14
   - BSR `+$1434` with R5=2 to advance; it returns the next
     matching entry's block index (0 = stop the loop)
4. Returns R14 (NEG if it exceeded R6 dirsize)

### Slot 8 (`+$1ACC`) R4!=2 path - BUP_Verify internal-RAM

- **Body**: `+$1B66` to `+$1BA4` (~62 bytes)
- **Inputs**: stack[4] = caller's R5 (filename), stack[0] =
  caller's R6 (data buffer to compare)
- **Returns** (R0): `0` = match, `5` = BUP_NOT_FOUND (filename
  matched no entry), `7` = BUP_NO_MATCH (data mismatch, from
  `+$1BB4`), `8` = BUP_BROKEN (block chain unreadable, from
  `+$1BB4`), or propagates a slot-3 error
- **Calls**: `+$0672` (slot 3), `+$1434` (validate), `+$1BB4`
  (transform/compare)

```python
def bup_verify_internal(filename, data_buf):     # entered at +$1B66
    rc = bup_stat(R4, 0, stack + 8)              # +$1B6C BSRF -> slot 3 (stat buffer at R15+8)
    if rc != 0:
        return rc
    matched = validate_1434(filename, mode=0)    # +$1B86
    if matched == 0:
        return 5                                  # BUP_NOT_FOUND
    return sub_1BB4(matched, data_buf)           # +$1B92: R4=matched, R5=data_buf; compare; returns 0 / 7 / 8
```

### Internal-RAM helper subs

These helpers are called from the slot R4!=2 paths to mutate
driver state or per-entry data during internal-RAM operations.

#### Sub `+$0388` - Write 8-byte bitmask table

- **Body**: `+$0388` to `+$03A8` (34 bytes, 16 instructions
  to RTS + 1 delay-slot store)
- **Called by**: slot 2 R4!=2 (+$042C), slot 3 R4!=2
  (+$074A), helpers `+$0EBC` (+$0ED6), `+$1004` (+$1024),
  `+$1434` (+$1452), and a helper sub at `+$1D30` (call at
  +$1D42)
- **Inputs**: R4 = destination buffer pointer (8 bytes
  writable)
- **Returns**: nothing
- **Side effect**: writes `{$80, $40, $20, $10, $08, $04,
  $02, $01}` (the 8 single-bit masks, MSB first) to
  `mem.B[R4..R4+7]`
- **No frame, leaf sub** - no PR push, no callee-save

```python
def sub_0388(dest):                              # R4
    # Writes the 8 single-bit masks (MSB first) to mem.B[R4..R4+7].
    for i in range(8):
        mem.B[dest + i] = 0x80 >> i              # {$80, $40, $20, ..., $01}
```

**Purpose**: builds a per-bit-index mask table in 8 bytes.
Each consumer indexes with `(index & 7)` to retrieve the
corresponding bit mask, then ANDs against a per-entry status
byte to test that specific bit. The table sits on the caller's
stack (typical layout: stack+40 for slot 3, stack+32 for
`+$1004`).

#### Sub `+$1434` - Per-entry validate / compare

- **Body**: `+$1434` to `+$1576` (324 bytes, 158 instructions)
- **Called by**: slot 4 R4!=2 (`+$0B3C`), slot 5 R4!=2
  (`+$140E`), slot 6 R4!=2 (`+$178C`), slot 7 R4!=2 (`+$1942`,
  `+$1A96`), slot 8 R4!=2 (`+$1B86`)
- **Inputs**:
  - R4 = caller's per-entry source buffer pointer (gets saved
    to R12; this is the filename/data buffer being validated)
  - R5 = mode byte (saved to stack[0]; 0 / 1 / 2 select
    sub-operation variants)
- **Returns** (R0): `0` = no match (no active, non-excluded
  entry matched the filename/pattern); non-zero = the matched
  entry's block index (also written to `driver_base+$50`)
- **Calls**:
  - BSRF `$FFFFEF32` at `+$1452` -> driver `+$0388` (bitmask
    init at stack+4)
- **Frame**: 12-byte stack (stack[0] = R5 mode, stack+4 =
  bitmask table from `+$0388`)

Pool:

| Offset | Value | Purpose |
|--------|-------|---------|
| `+$14A8` | `$06000354` | driver_base slot |
| `+$14AC` | `$FFFFEF32` | BSRF disp -> `+$0388` |
| `+$1578` | `$06000354` | driver_base (duplicate at end-of-body) |

```python
def validate_1434(caller_buf, mode):             # R4 = buf, R5 = mode (0/1/2)
    bitmask = [0x80 >> i for i in range(8)]      # sub_0388 at stack+4

    # Mode dispatch sets up the per-entry counter at driver_base+$50.
    if mode == 2:
        mem.L[driver_base + 0x50] += 1            # bump counter (find NEXT)
    else:                                        # mode 0 or 1: reset to first
        mem.L[driver_base + 0x50] = mem.L[driver_base + 0x44]

    # Walk dir entries starting at the counter.
    R5         = mem.L[driver_base + 0x50]
    stride     = mem.L[driver_base + 0x40]
    entry_addr = mem.L[driver_base + 0x30] + R5 * stride
    wildcard   = (mode != 0)                      # mode 1/2 (BUP_Dir): NUL pattern byte matches any

    while R5 < mem.L[driver_base + 0x38]:
        status = mem.B[entry_addr + 1]           # odd-byte status
        if status & 0x80:                         # entry active?
            bitmap_byte = mem.B[mem.L[driver_base + 0x4C]
                                + ((R5 >> 3) << 1) + 1]
            if (bitmap_byte & bitmask[R5 & 7]) == 0:   # bit set => entry excluded, skip
                # Compare 12 bytes of filename (odd-byte RAM access).
                for i in range(12):
                    ram_byte    = mem.B[entry_addr + 8 + i*2 + 1]
                    caller_byte = mem.B[caller_buf + i]
                    if ram_byte == caller_byte:
                        if i == 11 or caller_byte == 0:
                            mem.L[driver_base + 0x50] = R5
                            return R5 & 0xFFFF    # match found
                    else:
                        # Mismatch: in wildcard mode (mode != 0) a NUL
                        # caller byte still matches; byte 11 always completes.
                        if (caller_byte == 0 and wildcard) or i == 11:
                            mem.L[driver_base + 0x50] = R5
                            return R5 & 0xFFFF
                        break                     # next entry

        entry_addr += stride
        R5         += 1

    return 0                                      # no match
```

#### Sub `+$17BA` - Clear active dir-entry state (directory erase)

- **Body**: `+$17BA` to `+$18B2`
- **Called by**: slot 4 R4!=2 (`+$0B54`, `+$0E96`), slot 6
  R4!=2 (`+$1798`)

This is the directory-entry erase. Its full behavior - the
mode-0 vs mode-1 paths, the status-word rewrite, and the
mode-1 zeroing of bytes `+$04..+$3F` - is documented under the
Slot 6 (`+$16DC`) section, "What `+$17BA` writes to the dir
entry".

#### Sub `+$1BB4` - Per-entry transform / commit (BUP_Verify compare)

- **Body**: `+$1BB4` to `+$1D2E` (378 bytes, 189 instructions)
- **Called by**: slot 4 R4!=2 (`+$0E68`), slot 8 R4!=2
  (`+$1B92`)
- **Inputs**:
  - R4 = word value (entry sequence ID; saved into
    `mem.W[driver_base+$2C... + 0]` first to mark current
    entry)
  - R5 = caller's data buffer pointer (for byte-compare in
    BUP_Verify path)
- **Returns** (R0):
  - `0` = full match (BUP_Verify success)
  - `7` = byte mismatch (BUP_Verify failure)
  - `8` = BUP_BROKEN (entry word > block bound at
    `driver_base+$38`; out-of-range block index)
- **Frame**: 12-byte stack

Pool: `+$1CA8 = $06000354` (driver_base slot).

```python
def sub_1BB4(seq_id, data_buf):                  # R4 = entry seq ID, R5 = compare buf
    mem.L[driver_base + 0x48] = 0
    entry_table = mem.L[driver_base + 0x2C]
    mem.W[entry_table] = seq_id                  # publish current seq ID

    mode = 0                                      # self-progressive: 0 -> 1 -> 2
    R13  = 0                                      # outer entry index
    R10  = 0                                      # compare-buffer cursor

    while True:
        # End-of-list sentinel: zero word in the entry table.
        entry_word = mem.W[entry_table + R13 * 2]
        if entry_word == 0:
            return 0                              # full match / list exhausted

        if entry_word > mem.L[driver_base + 0x38]:
            return 8                              # out of bounds

        entry_addr = (mem.L[driver_base + 0x30]
                      + entry_word * mem.L[driver_base + 0x40])

        if mode == 0:
            # Stage entry bytes 60..(stride) into stack scratch.
            stage_bytes_to_scratch(entry_addr, 60, mem.L[driver_base + 0x40])
            mode = 1
        elif mode == 1:
            # Write stack scratch back to RAM odd-byte slots.
            commit_scratch_to_ram(entry_addr)
            mode = 2
        else:                                     # mode == 2: BUP_Verify compare
            for j in range(mem.L[driver_base + 0x40]):
                ram_byte    = mem.B[entry_addr + 1 + j * 2]
                caller_byte = mem.B[data_buf + R10]
                R10 += 1
                if ram_byte != caller_byte:
                    mem.L[driver_base + 0x48] = entry_word    # record mismatch idx
                    return 7                                   # BUP_NO_MATCH

        R13 += 1
```

#### Sub `+$0EBC` - Per-entry buffer commit / per-entry write

- **Body**: `+$0EBC` to `+$1002` (326 bytes, 163 instructions)
- **Called by**: slot 4 R4!=2 (`+$0E7E`)
- **Inputs**: R4 = entry index word
- **Returns** (R0):
  - `0` = success
  - `8` = byte mismatch on verify pass
- **Calls**:
  - BSR `+$0388` (init bitmask table at stack+4)
  - BSRF disp `$2790` at `+$0F24` -> driver `+$36B8` (signed
    divide / mod helper - see peripheral_driver.md)
  - BSRF disp `$107E` at `+$0F8E` -> driver `+$2010` (in-WRAM
    busy-wait delay)
  - JSR through `*$06000340` twice (BIOS IMS mask / restore)
- **Frame**: 12-byte stack (stack[0] = R4 input word,
  stack+4 = bitmask table from `+$0388`)

Pool:

| Offset | Value | Purpose |
|--------|-------|---------|
| `+$0F98` | `$06000340` | BIOS IMS-mask fn slot |
| `+$0F9C` | `$00002790` | BSRF disp -> `+$36B8` (signed div/mod) |
| `+$0FA0` | `$06000348` | SCU IMS shadow |
| `+$0FA4` | `$0000107E` | BSRF disp -> `+$2010` (delay) |

```python
def sub_0EBC(entry_idx):                          # R4 = entry index word
    bitmask = [0x80 >> i for i in range(8)]       # sub_0388 at stack+4
    saved_idx = entry_idx & 0xFFFF

    entry_RAM = (mem.L[driver_base + 0x30]
                 + mem.L[driver_base + 0x40])    # $20180000 + stride
    table     = mem.L[driver_base + 0x2C]         # entry_table mirror
    count8    = mem.L[driver_base + 0x38] // 8    # bytes in status bitmap

    # Pass 1: read-back current RAM status bytes into the table mirror.
    for i in range(count8):
        mem.B[table + i] = mem.B[entry_RAM + 1 + i * 2]

    # Pass 2: OR the bit indexed by entry_idx into table[idx >> 3].
    byte_idx = saved_idx >> 3                     # inline SHAR (arithmetic >>3)
    bit_idx  = saved_idx % 8                       # via +$36B8 signed div/mod
    mem.B[table + byte_idx] |= bitmask[bit_idx]

    # Mode 1: mask SCU IRQs around the write+verify phase.
    mode = mem.L[driver_base + 0x34]
    if mode == 1:
        saved_ims = mem.L[0x06000348]
        bios_ims_call(-1)

    # Pass 3: write mirror back to RAM odd-byte slots up to scan_length.
    for i in range(mem.L[driver_base + 0x3C]):
        mem.B[entry_RAM + 1 + i * 2] = mem.B[table + i] if i < count8 else 0

    if mode == 1:
        sub_2010_delay()                          # ~10 ms busy-wait

    # Pass 4: verify each RAM byte matches the mirror.
    for i in range(count8):
        if mem.B[entry_RAM + 1 + i * 2] != mem.B[table + i]:
            if mode == 1:
                bios_ims_call(saved_ims)
            return 8                              # BUP_BROKEN

    if mode == 1:
        bios_ims_call(saved_ims)
    return 0                                      # success
```

#### Sub `+$1004` - Post-fetch packet sort + dir-entry commit

- **Body**: `+$1004` to `+$1358` (854 bytes, 418 instructions -
  largest single sub in the driver)
- **Called by**: slot 4 R4!=2 (`+$0B96`)
- **Inputs**: R4 = caller's R6 (data buffer; saved to R9 as
  cursor base for per-entry extract)
- **Returns** (R0): always `0` (single exit at `+$1356`; R0 set
  at `+$133E`). There is no error return - the `MOV #8,R0` at
  `+$114A` is the divisor for the `+$36B8` div/mod call in the
  BSRF delay slot, not a return code.
- **Calls**:
  - BSR `+$0388` (init bitmask table at stack+32)
  - BSRF disp `$256C` at `+$1148` -> driver `+$36B8` (signed
    div/mod helper, see peripheral_driver.md)
- **Frame**: 40-byte stack (stack[0] = word ID, stack[4] = mode
  flag, stack+8/12/16/20 = scratch pointers, stack+24/28 =
  loop counters, stack+32 = bitmask table)

This is the bulk processor that lays down a new directory
entry plus its full block-list in internal backup RAM for
BUP_Write. The work splits into 4 passes:

Pool:

| Offset | Value | Purpose |
|--------|-------|---------|
| `+$1080` | `$06000354` | driver_base slot |
| `+$1120` | `$06000354` | (duplicate) |
| `+$11D8` | `$0000256C` | BSRF disp -> `+$36B8` (signed div/mod) |

```python
def sub_1004(data_buf):                          # R4 = caller's data buffer
    # Builds a new directory entry + its full block list in internal
    # backup RAM for BUP_Write. Four passes operate against the entry
    # table mirror at mem.L[driver_base+$2C]:

    bitmask = [0x80 >> i for i in range(8)]       # sub_0388 at stack+32
    table   = mem.L[driver_base + 0x2C]
    bup_dir = mem.L[driver_base + 0x4C]          # $20180080 (dir table base)
    count8  = mem.L[driver_base + 0x38] // 8

    # Pass 1 (+$1028..+$104E): read-back current RAM status bytes
    # into the entry_table mirror.
    for i in range(count8):
        mem.B[table + i] = mem.B[bup_dir + 1 + i * 2]

    # Pass 2 (+$1084..+$11B4): walk active entries; for each entry
    # whose status high-bit is set AND whose bitmap bit is set,
    # OR the bit into the mirror, then extract entry bytes through a
    # self-progressive inner state machine (mode 34 -> mode 4 at
    # +$1196). The block-list addresses are stored at
    # (entry_table + count8)[counter * 4] for later access. The
    # per-byte loop uses +$36B8 (signed div/mod helper) to map
    # entry indices to byte index + bit number.
    walk_and_extract_active_entries(table, bup_dir, bitmask)

    # Pass 3 (+$11CC..+$1238): bubble-sort prep - for each entry
    # with the relevant bit set, reads 4 bytes from RAM (odd-byte at
    # $20180001 + entry_stride*idx + 1) and tracks the smallest
    # sequence ID seen so far (accumulator seeded to $FFFFFFFF via
    # MOV #-1,R6; replaced only when CMP/HI shows it exceeds the
    # candidate).
    find_min_sequence_id()

    # Pass 4 (+$1242..+$12CA): commit-and-bubble-sort. Writes the
    # min-matching entries' sequence words to the output buffer at
    # R11 + R8*2, then bubble-sorts the result ascending
    # (+$12DC..+$1316), copies the sorted list back into the entry_table
    # mirror (+$131E..+$1336), and writes a $0000 terminator at the
    # end of the sorted list.
    commit_and_sort()

    return 0                                      # success
```

### Memory targets

For the internal-RAM path, all reads/writes hit
`$00180000-$0018FFFF` via the cache-through alias
`$20180000-$2018FFFF`. The Saturn bus maps both ranges to the
same 32 KB of physical backup SRAM with odd-byte addressing
(`addr & 1 != 0` is the active byte; even addresses read $FF
and writes to even addresses are ignored). The BIOS code
deals with the odd-byte mapping by using `MOV.B @(1,R2),R0`
patterns and `R2 = $20180000 + 2*i` indexing (see `+$09EA`
disasm above for an example).

### Sub `+$033E` - Write ASCII "BackUpRam Format" magic string

- **Body**: `+$033E` to `+$0382` (70 bytes, 35 instructions)
- **Called by**: slot 2 (BSR at +$0430), sub `+$09EA` (BSR at +$09FE)
- **Inputs**: R4 = destination buffer pointer (must hold >= 16 bytes)
- **Returns**: no value (R0 holds last byte written = $74)
- **Side effect**: writes 16-byte ASCII `"BackUpRam Format"`
  to `mem.B[R4..R4+15]`
- **No frame, no PR push, no callee-save** - leaf sub; RTS uses
  delay-slot to write the final byte
- **Clobbers**: R0, R3, R5, R6

```python
def sub_033E(dest):                              # R4
    # Writes the 16-byte ASCII "BackUpRam Format" to mem.B[R4..R4+15].
    # Used by sub_09EA (to stage the comparison string) and by slot 2
    # BUP_Format (to write the magic into RAM scratch before commit).
    for i, ch in enumerate(b"BackUpRam Format"):
        mem.B[dest + i] = ch
```

**Purpose**: writes the 16-byte ASCII pattern
`"BackUpRam Format"` into a scratch buffer. This is the
signature header stored in the first 16 bytes of formatted
Saturn backup storage. The driver stages the pattern locally
for two internal-RAM uses:

1. **Compare** - `+$09EA` stages it, then scans internal backup
   RAM (`$00180000`) for the signature (format check)
2. **Write** - slot 2's BUP_Format stages it, then writes it
   into internal backup RAM during the format commit

The string is stored at every other byte in the actual RAM
(odd-address layout - see `+$09EA` for the 2-byte-stride scan
that consumes it).

### Sub `+$09EA` - Scan for "BackUpRam Format" signature

- **Body**: `+$09EA` to `+$0A68` (128 bytes, 64 instructions)
- **Called by**: slot 3 (BSR at +$07A8 and +$0878)
- **Inputs (via driver state)**:
  - `mem.L[driver_base+$30]` = source base address - caller sets
    to `$20180000` (cache-through alias of internal backup RAM
    at `$00180000`)
  - `mem.L[driver_base+$3C]` = scan length in bytes
- **Returns** (R0): `0` = magic string found at some offset;
  `1` = not found after full scan
- **Side effects**: writes 16 bytes of `"BackUpRam Format"` to
  stack scratch (consumed for comparison)
- **Calls**: BSR `+$033E` (load magic string into stack scratch)

Internal backup RAM is byte-mapped at **odd addresses** in the
`$00180000..$0018FFFF` window - the SH-2 sees only the low byte
of each word. This sub uses `mem.B[$20180001 + 2*i]` indexing
to walk consecutive RAM bytes.

Pool: `+$0A74 = $06000354` (driver_base slot).

```python
def sub_09EA():
    # Scans mem.L[driver_base+$30]..+(scan_length) for "BackUpRam Format".
    # Returns 0 if found at any offset, 1 if absent after full scan.
    # Internal backup RAM byte-maps at odd addresses only, so consecutive
    # RAM bytes are read via mem.B[base + 1 + i*2] (2-byte stride).

    magic = b"BackUpRam Format"
    sub_033E(stack)                              # stage magic at stack[0..15]

    ram_base    = mem.L[driver_base + 0x30]
    scan_length = mem.L[driver_base + 0x3C]

    for offset in range(scan_length - 15):
        if mem.B[ram_base + 1 + offset * 2] != magic[0]:
            continue
        # First byte matched - inner loop re-verifies all 16 from i=0.
        for i in range(16):
            if mem.B[ram_base + 1 + (offset + i) * 2] != magic[i]:
                break
        else:
            return 0                              # full match
    return 1                                      # not found
```

**Purpose**: scans the backup-RAM region addressed by
`mem.L[driver_base+$30]` for the `"BackUpRam Format"` magic
string. Returns 0 if found at any offset, 1 if absent after a
full scan. Used by slot 3 to detect whether the internal backup
RAM has been formatted before allowing read/write operations.

## External A-bus BUP cart subs (R4==2 path)

The subs below implement the cart-protocol path that the BIOS
takes when the SDK BUP library is called with `device=2`
(external A-bus BUP cartridge). They are **not** what produces
the internal save-RAM on-disk format - that's the internal-RAM
path documented in the previous section.

These subs all talk to the cart through memory-mapped
command / status / data registers loaded by `MOV.W` from
pool words `$FE02..$FE05`. The `$00A00000` value passed as
R6 to `+$22AA` is an unreachable-timeout sentinel, not a
memory address.

### Sub `+$2140` - BUP error-code cleanup + exit helper

- **Body**: `+$2140` to `+$21C4` (134 bytes)
- **Called by**: `+$22AA` (each error branch), `+$21D8`, `+$2484`,
  `+$25C8`, `+$2670`, `+$279C`, `+$2C08`, `+$2FD0`, `+$32A6`
- **Inputs**: R4 = error code word (one of
  `$0101/$0103/$0104/$0105/$0106`)
- **Returns** (R0): R4 unchanged (caller's error code passes
  through)
- **External**:
  - JSR `*$06000340` (BIOS IMS-restore function pointer)
  - BSR `+$266E` (port-2 sub entry - alternate entry into the
    body shared with sub `+$2670`)
- **Side effects**: A-bus cart control byte at `$FE02` and
  status byte at `$FE04` cleared per branch (these word loads
  loaded via `MOV.W`);
  SCU IMS restored from `driver_base+$6C` via BIOS.

Pool:
- `+$2158`, `+$21D4`: `$06000354`, `$06000340` (driver_base + BIOS slot)
- `+$21C6`, `+$21C8`: `$FE02`, `$FE04` (A-bus cart control / status)
- `+$21CA`..`+$21D2`: `$0101`, `$0103`, `$0104`, `$0105`, `$0106` (error keys)

```python
def sub_2140(error_code):                        # R4
    # Dispatch-on-error cleanup; returns R0 = error_code unchanged.
    # All non-skip branches restore SCU IMS from driver_base+$6C
    # via *$06000340.
    if error_code == 0x0101:
        bios_ims_restore()                       # JSR *$06000340
        mem.B[0xFE04] = 0                        # clear status; skip port-2 cleanup
    elif error_code in (0x0103, 0x0105):
        bios_ims_restore()
        mem.B[0xFE04] = 0                        # clear A-bus cart status
        port2_cleanup()                          # BSR +$266E
    elif error_code == 0x0104:
        mem.B[0xFE02] &= 0xEF                    # clear cart write-mode bit
        bios_ims_restore()
        mem.B[0xFE04] = 0
        port2_cleanup()
    elif error_code == 0x0106:
        mem.B[0xFE04] = 0                        # clear status, no port-2 cleanup
    else:
        port2_cleanup()                          # default: port-2 cleanup only
    return error_code
```

**Behavior**: dispatch-on-error helper. Compares caller's R4
against five BUP error codes and runs the matching cleanup
branch:

| R4 | Branch | Effect |
|----|--------|--------|
| `$0101` | +$217C | BIOS IMS restore; clear $FE04; skip port-2 cleanup; epilogue with R0 = error |
| `$0103` | +$2166 | BIOS IMS restore; clear $FE04; port-2 cleanup; epilogue |
| `$0104` | +$215C | Clear $FE02 bit 4; BIOS IMS restore; clear $FE04; port-2 cleanup; epilogue |
| `$0105` | +$2166 | Same as $0103 |
| `$0106` | +$2188 | Clear $FE04 only; skip port-2 cleanup; epilogue |
| (other) | fall-through | Skip both clears; port-2 cleanup; epilogue |

In all cases R0 = the caller's R4 (the error code is returned
unchanged so the caller can propagate it up the stack).

### Sub `+$21D8` - BUP cart-protocol command-and-data send

- **Body**: `+$21D8` to `+$22A8` (210 bytes, 105 instructions)
- **Called by**: `+$2484` (send 6-byte controller-identify), `+$279C`
  (send 38-byte BUP_Delete cmd), `+$2C08` (send 38-byte BUP_Dir cmd),
  `+$2FD0` (send 38-byte BUP_Read cmd), `+$32A6` (send 38-byte
  BUP_Write cmd)
- **Inputs**:
  - R4 = output buffer pointer (bytes to send)
  - R5 = byte count
- **Returns** (R0): `0` = success; `$0106` (retry-exhaust)
  via `+$2140`
- **Calls**:
  - BSRF `$0000126C` -> driver `+$3478` (compute write checksum
    word, deposited at stack[8])
  - BSR `+$2140` (error tail on timeout)

Pool:
- `+$2232`: `$FE02` (cart control reg)
- `+$2234`: `$0106` (retry-exhaust err)
- `+$2236`: `$FE03` (cart data reg)
- `+$2238`: `$0080` (ready-bit mask)
- `+$223A`: `$FE04` (cart status reg)
- `+$223C`: `$0000126C` (BSRF disp -> `+$3478`)

```python
def sub_21D8(buf, count):                        # R4, R5
    # Send N-byte payload + 2-byte checksum to the external A-bus
    # BUP cart. Returns 0 on success, $0106 (via +$2140) on retry-exhaust.

    checksum = sub_3478(buf, count, 0)           # BSRF +$3478

    # Enter cart-write mode: $FE02 bit 5 set, bits 2-3 preserved.
    mem.B[0xFE02] = (mem.B[0xFE02] & 0x0C) | 0x20

    for i in range(count):
        retries = 0
        while not (mem.B[0xFE04] & 0x80):        # poll ready bit
            retries += 1
            if retries > 0x7FFF:                 # signed-16 overflow
                return sub_2140(0x0106)          # retry-exhaust
        mem.B[0xFE03]  = buf[i]                  # write byte
        mem.B[0xFE04] &= 0x7F                    # ack (clear ready)

    # Send 2-byte checksum
    for byte in [checksum >> 8, checksum & 0xFF]:
        while not (mem.B[0xFE04] & 0x80): pass
        mem.B[0xFE03]  = byte
        mem.B[0xFE04] &= 0x7F

    while not (mem.B[0xFE04] & 0x04): pass       # wait for cmd-done bit 2

    # Exit cart-write mode (clear $FE02 bits 4-7).
    mem.B[0xFE02] &= 0x0F
    return 0
```

**Behavior**: sends an N-byte payload + 2-byte checksum to the
external A-bus BUP cart via cart registers at
`$FE02..$FE04`:

1. Calls `+$3478` to compute a 16-bit checksum over the buffer,
   stored at stack[8]
2. Sets `$FE02` bit 5 (cart-write enable), preserving bits 2-3
3. **Payload loop** - for each of R5 bytes:
   - poll `$FE04 bit 7` for ready (with retry-exhaust check
     via signed CMP/PZ on a 16-bit counter)
   - write byte to `$FE03`
   - clear `$FE04 bit 7` to ack
4. **Checksum loop** - send the 2 bytes at stack[8..9] same way
5. Poll `$FE04 bit 2` for cmd-complete acknowledgment
6. Clear `$FE02 bits 4-7` to return cart to idle
7. Return 0 (success) or `$0106` via `+$2140` on retry-exhaust

### Sub `+$22AA` - Cartridge BUP read with SCU IMS gate

- **Body**: `+$22AA` to `+$2482` (474 bytes, 237 instructions)
- **Called by**: `+$2484` (read 6 ID bytes), `+$2568` (read 6
  general bytes), `+$2670` (read 26 BUP-magic-check bytes),
  `+$279C` (read 6 status bytes after BUP_Delete cmd), `+$2C08`
  (read 64-byte BUP_Dir response), `+$2FD0` (read 6 status
  bytes after BUP_Read cmd), `+$32A6` (read 6 status bytes after
  BUP_Write cmd)
- **Inputs**:
  - R4 = output buffer pointer
  - R5 = byte count to read
  - R6 = poll/timeout limit (spin-count ceiling for the per-byte
    ready poll). Most callers pass `$00A00000` (e.g. `+$2670`
    from pool word +$26CC), but not all: `+$2484` passes `$1000`
    and `+$2568` passes `$01400000`. The `$01102100` XOR
    multiplier used in the read pass is separate - it is loaded
    into R10 from pool word +$235C.
- **Returns** (R0): `0` = success; one of
  `$0101/$0103/$0104/$0105` (per-step protocol errors) or
  `$0102` (post-read verify failure) via `+$2140`
- **Calls**:
  - JSR `*$06000340` twice (BIOS IMS-mask before; IMS-restore
    after; mem.L[driver_base+$6C] holds the shadow across the
    critical section)
  - BSR `+$2140` (every error branch - passes the per-branch
    pool error code in R4)
- **Hardware effects**:
  - mem.B[$FE02] = preserve bits 2-3, set bits 1+4 (`AND $0C |
    $12`) - A-bus cart command/data write enable
  - mem.B[$FE04] cleared (status reg cleared on each byte ack
    via `AND #$BF`)
  - mem.B[$FE05] read per byte - A-bus cart data register
  - mem.B[$FE02] final write (`AND #$0F`) - return cart to idle

The read protocol is a poll-step loop: for each byte, check
error bits 3/4/5 (parity / overrun / busy abort) first - each
with its own error return - then wait for `$FE04 bit 6 == 1`
(data ready; this is the last gate, looping back until set),
read one byte from `$FE05`, ack by clearing `$FE04 bit 6` via
`AND #$BF`. Three encoding modes
(R0 = 0/1/other from BSRF stack-state) drive a polynomial-XOR
decryption pass on the read byte using a multiplier in R10
(loaded from pool at +$235C). The post-read step compares a
calculated word against a stack value; mismatch returns `$0102`.

**Behavior summary**: the external A-bus BUP cart byte-read
sub. Reads
`R5+2` bytes (caller's count + 2 trailing-checksum/verify bytes)
from the cart data register at pool word `$FE05`, polling
the cart status register at pool word `$FE04` for
ready/error status on each byte. Per-byte handling switches on
the loop index:

- **Index 0**: byte goes to output buf and an echo at stack+1
- **Index 1**: byte goes to output buf, echoed at stack+2, then
  XORed against `$00FFFF00` into the running checksum at stack[0]
- **Index >= 2**: byte goes through a cursor pointer at stack[4],
  conditionally to the output, and through a 2-stage polynomial
  XOR (`stack[0] = (stack[0] << 4) XOR (byte * $01102100)`)

After the loop, stack[0] holds the computed verify checksum.
`stack[20]` points back to `stack[0]` (set to R15 at +$22D4), so
the final step reads that computed word via `mem.W[stack[20]]`
and requires it to be zero; a non-zero result returns `$0102`.
SCU IMS is masked throughout via the BIOS function pointer at
`$06000340` and restored on exit.

### Sub `+$2484` - Cartridge BUP identify (send query + read 6 bytes)

- **Body**: `+$2484` to `+$2562` (224 bytes, 112 instructions)
- **Called by**: `+$2670` (R4=17), `+$2734` (R4=32), `+$279C`
  (R4=48), `+$25C8` (R4=16), `+$2C08` (R4=64), `+$2FD0` (R4=65),
  `+$32A6` (R4=80)
- **Inputs**:
  - R4 = byte arg deposited at cmd[1] (caller-encoded sub-cmd
    discriminator - NOT a byte count; the per-caller table
    above shows the actual values)
  - R5 = port byte stored at cmd[3] (low byte of R5 is also
    overwritten into stack[24] for the R4=16 copy variant)
  - R6 = output buffer pointer (typically caller's stack)
- **Returns** (R0):
  - the response[3] byte (stack+19) on a successful magic match
  - `$0107` = response[0] (stack+16) != $20 (magic mismatch)
  - sub-error from `+$21D8` or `+$22AA`
- **Calls**:
  - BSR `+$21D8` (cart send 6 bytes from stack+16)
  - BSR `+$22AA` (cart read 6 bytes; R6 = $1000 from pool)

**Behavior**: A-bus BUP cart identify + sub-command. Builds a 6-byte command
`[$80, R4_low, 0, port_byte, 0, 0]` at stack+16. Some callers
(R4 == 16) overlay a 4-byte block from stack+24 into cmd[2..5]
to encode a multi-byte sub-cmd discriminator. Sends the command
via `+$21D8`, reads a 6-byte response back into stack+16 via
`+$22AA`, then checks the first response byte (stack+16) against
`$20` (BUP magic). On match, copies the response byte at stack+18
into the caller's output buf and returns the response byte at
stack+19. On mismatch, returns `$0107`.

**Note** about the byte at `+$253C`: `stack[4]` is set to the
command/response buffer base (stack+16) at `+$24A2` and is not
changed afterward (`+$22AA` runs in its own stack frame), so
`MOV.L @(4,R15),R0` / `MOV.B @R0,R0` reads the first response
byte at stack+16 - the BUP-magic byte, expected to be `$20`.

```python
def sub_2484(sub_cmd, port_byte, out_buf):       # R4, R5, R6
    # Build 6-byte command at stack+16: [$80, R4_low, 0, port_byte, 0, 0].
    cmd = bytearray([0x80, sub_cmd & 0xFF, 0, port_byte & 0xFF, 0, 0])

    # R4 == 16 variant: overlay a 4-byte block from stack+24 into
    # cmd[2..5] to encode a multi-byte sub-cmd (used by +$25C8 for
    # the BUP_Stat partition-descriptor path).
    if sub_cmd == 16:
        cmd[2:6] = mem.L[stack + 24].to_bytes(4)

    rc = sub_21D8(cmd, 6)                         # send 6 bytes
    if rc != 0:
        return rc
    rc = sub_22AA(stack + 16, 6, poll_limit=0x1000)  # read 6-byte response into stack+16
    if rc != 0:
        return rc

    # Magic check: response[0] (byte at stack+16) must equal $20.
    if mem.B[stack + 16] != 0x20:
        return 0x0107

    mem.B[out_buf] = mem.B[stack + 18]           # output[0] = response[2]
    return mem.B[stack + 19]                      # return response[3]
```

### Sub `+$2568` - Cartridge BUP read 6 bytes + magic-check dispatch

- **Body**: `+$2568` to `+$25B0` (74 bytes, 37 instructions -
  3 RTS exit points)
- **Called by**: `+$25C8` (BSR at +$2634), `+$2670` (BSR at
  +$26B8), `+$2732` (BSR at +$276E), `+$279C` (BSR at +$282E),
  `+$32A6` (BSR at +$3436)
- **Inputs**: R4 = output buffer pointer
- **Returns** (R0):
  - sub-error from `+$22AA` (cart read failed)
  - `$0107` (controller-response magic byte != $20)
  - post-read byte at cmd+3 (success - port byte returned)
- **Calls**: BSR `+$22AA` (cart read 6 bytes; R6=$01400000)

**Behavior**: reads 6 bytes from external A-bus BUP cart
(R6=`$01400000` is an unreachable-timeout sentinel) into
caller's stack+4..+9. Returns:
- sub-error on cart-read failure
- `$0107` if read[0] != `$20` (controller magic mismatch - indicates
  controller absent or RAM not BUP-formatted)
- on success: writes read[+2] byte to caller's output buf[0]
  and returns R0 = read[+3] (low byte of port-echo)

```python
def sub_2568(out_buf):                           # R4
    rc = sub_22AA(stack + 4, 6, poll_limit=0x01400000)  # read 6 bytes
    if rc != 0:
        return rc
    if mem.B[stack + 4] != 0x20:                  # cart magic mismatch
        return 0x0107
    mem.B[out_buf] = mem.B[stack + 4 + 2]         # post-read echo to caller
    return mem.B[stack + 4 + 3]                   # return port-echo byte
```

### Sub `+$25C8` - BUP_Stat cart-state query (slot 3 BSRF $1EE8)

- **Body**: `+$25C8` to `+$266C` (164 bytes, 82 instructions -
  6 RTS exit points)
- **Called by**: slot 3 (BSRF `$1EE8` at +$06DC -> target =
  $06DC + 4 + $1EE8 = `$25C8`). Slot 3 dispatches into this
  for the BUP-specific partition/state query path.
- **Inputs**:
  - R4 = output pointer for stat field 1 (receives read[+4])
  - R5 = output pointer for stat field 2 (receives read[+8])
  - R6 = pointer to a longword holding the 4-byte port descriptor
    on entry (dereferenced to feed `+$2484`'s R4=16 overlay), which
    also receives stat field 3 (read[+12]) on exit
- **Returns** (R0):
  - `0` = success, controller state copied to caller's outputs
  - `$0107` = first ID byte == `$FF` (controller not present at
    that port) - propagated from `+$2140` cleanup
  - `$0107` = post-magic-check stack[0] != `$00FF`
  - sub-error from `+$2484`, `+$22AA`, or `+$2568`
- **Calls**:
  - BSR `+$2484` (controller identify R4=16 - uses 4-byte copy variant)
  - BSR `+$2140` (error cleanup with R4 = $0107)
  - BSR `+$22AA` (read 18 bytes from `$00A00000`)
  - BSR `+$2568` (final magic-check read of 6 bytes)

**Behavior**: cart-state stat query - the external-cart inner
sub for **BUP_Stat** (slot 3 R4==2 path). Reads multi-field
device status from the cart. The flow:

1. Identify controller with the R4=16 copy variant of `+$2484` (which
   reads a multi-byte sub-cmd from the caller-supplied port
   descriptor at `mem.L[R6]`)
2. If first ID byte is `$FF` (controller absent): error-cleanup via
   `+$2140` -> return `$0107`
3. If identify errored: propagate
4. Read 18 bytes from `$00A00000` (cart-stat block)
5. Copy three 32-bit fields from offsets +4/+8/+12 of the read
   buffer to caller's three output pointers (stat-field-1/2/3
   = capacity / blocks / free-blocks etc.)
6. Final magic-check via `+$2568` (controller still responsive)
7. If final magic OK, return 0; if mismatch, return `$0107`

Entry point is `+$25C8` (BSRF math from slot 3 confirms: `$06DC
+ 4 + $1EE8 = $25C8`). `+$25CA` is mid-prologue (after the R14
push) and is not a callable entry.

```python
def sub_25C8(out1, out2, desc_and_out3):         # R4, R5, R6
    # Step 1: identify controller via +$2484 R4=16; the 4-byte port
    # descriptor is *R6 (passed as +$2484's R5, overlaid into its cmd).
    rc = sub_2484(16, port_byte=mem.L[desc_and_out3], out_buf=stack)
    if mem.B[stack] == 0xFF:                      # controller absent
        sub_2140(0x0107)
        return 0x0107
    if rc != 0:
        return rc

    # Step 2: read 18 bytes of cart-stat block into stack+16.
    rc = sub_22AA(stack + 16, 18, poll_limit=0x00A00000)
    if rc != 0:
        return rc

    # Step 3: copy 3 longword stat fields (read[+4]/[+8]/[+12]) to
    # the three caller pointers.
    mem.L[out1]          = mem.L[stack + 16 + 4]
    mem.L[out2]          = mem.L[stack + 16 + 8]
    mem.L[desc_and_out3] = mem.L[stack + 16 + 12]

    # Step 4: final magic-check via +$2568 (controller still alive).
    rc = sub_2568(stack)
    if rc != 0:
        return rc
    if mem.B[stack] != 0xFF:                      # post-magic mismatch
        return 0x0107
    return 0
```

### Sub `+$2670` - Port-2 BUP cart read (slot 3 BUP-magic path)

Alternate entry **`+$266E`** (used by `+$2140`'s port-2-cleanup
tail) just adds `MOV R5,R0` before falling into `+$2670` - so
callers can supply the port byte directly in R5 instead of R0.

- **Body**: `+$2670` to `+$2730` (194 bytes, 97 instructions)
- **Called by**: slot 3 (BSRF `$1FB6` at +$06B4 -> target =
  $06B4 + 4 + $1FB6 = `$266E` - so slot 3 takes the alt entry);
  `+$2140` (BSR at +$21B6, also at alt entry `+$266E`)
- **Inputs**:
  - R4 = output buffer pointer (20 bytes)
  - R0 = port byte (or R5 if entered at `+$266E`)
- **Returns** (R0):
  - `0` = success (20 bytes written to output)
  - `$0107` (early-out: stack[0] == $FF after identify)
  - `pool[$276A]` = secondary error code on type mismatch
- **Calls**:
  - BSR `+$2484` (R4=17, R5=port byte, R6=stack - identify
    controller with sub-cmd 17)
  - BSR `+$22AA` (R4=R13=stack+8, R5=26, R6=$00A00000 - read 26
    bytes of controller magic-response + data)
  - BSR `+$2568` (R4=R15 - magic-check pass)

Pool:
- `+$26C6`: `$00FF`
- `+$26C8`: `$0107`
- `+$26CC`: `$00A00000`
- `+$2768`: (referenced by +$26D2 - peripheral-type expected)
- `+$276A`: (referenced by +$26DA - error code)
- `+$276C`: (referenced by +$26EC - flag bit)

Output writing block at +$26D0:

Epilogue:

**Behavior summary**: orchestrates a 3-step peripheral read for
port 2:
1. Identify the peripheral via `+$2484` (writes 17 bytes to
   stack starting with type byte)
2. Read 26 bytes of peripheral data via `+$22AA` (uses fixed
   address `$00A00000` is the unreachable-timeout sentinel
   - unreachable-timeout sentinel for the cart-read sub)
3. Process the data via `+$2568`
4. Validate result type and unpack a 20-byte output struct
   (header + data fields) into caller's output buffer

The fixed `$00A00000` value is used as an unreachable-timeout
sentinel for `+$22AA`'s polling loop (the inner `CMP/HI R6,R5`
never fires with R6 = 10 million and R5 starting at 0). The
actual data bytes come from the A-bus cart data register
at pool word `$FE05`, not from `$00A00000`.

```python
def sub_2670(out_buf, port_byte):                # R4, R0 (or R5 at +$266E)
    # Step 1: identify controller (sub-cmd 17).
    rc = sub_2484(17, port_byte, stack)

    # The $FF (controller-absent) check is made BEFORE the rc
    # check (asm order): a $FF identify byte returns $0107 even
    # when sub_2484's rc is non-zero.
    if mem.B[stack] == 0xFF:
        return 0x0107
    if rc != 0:
        return rc

    # Step 2: read 26 bytes of peripheral magic-response + data.
    rc = sub_22AA(stack + 8, 26, poll_limit=0x00A00000)
    if rc != 0:
        return rc

    # Step 3: post-read magic-check via +$2568.
    rc = sub_2568(stack)
    if rc != 0:
        return rc

    # Step 4: validate peripheral type at stack[0] against pool[+$2768].
    if mem.B[stack] != mem.W[pool_plus_2768]:
        return mem.W[pool_plus_276A]              # type-mismatch error

    # Step 5: field-by-field unpack from the stack+8 work buffer
    # into the caller's output struct (out_buf = R4). Not a flat
    # memcpy; mixed byte/word/long stores reaching out+0..+19.
    out_buf[0]     = mem.B[stack + 12]           # @R1     (R1 = R13+4)
    out_buf[1]     = mem.B[stack + 13]           # @(1,R1)
    out_buf[2:4]   = word(mem.B[stack + 15])     # @(3,R1) -> out+2 (word, EXTU.B)
    out_buf[4:8]   = mem.L[stack + 16]           # @(8,R13)
    out_buf[8:12]  = mem.L[stack + 20]           # @(12,R13)
    out_buf[12:14] = mem.W[stack + 24]           # @(16,R13)
    out_buf[14:16] = mem.W[stack + 26]           # @(18,R13)
    out_buf[16:20] = mem.L[stack + 28]           # @(20,R13)
    return 0
```

### Sub `+$2734` - Slot-2 (BUP_Format) port-2 cart-ID poll

- **Body**: `+$2732` (`MOV R4,R0`) to `+$279A` (104 bytes). The
  prologue proper begins at `+$2734`; the preceding sub's RTS is
  at `+$272E` (delay slot `+$2730`, `MOV.L @R15+,R14`).
- **Called by**: slot 2 (BSRF `$233A` at +$03F4 -> target =
  $03F4 + 4 + $233A = `+$2732`)
- **Inputs**: R0 = port byte (saved to stack[4]; passed to
  `+$2484` as R5 = port byte, R4 = 32 sub-cmd)
- **Returns** (R0):
  - `0` = success (post-read type byte stack[0] == `$FF`, `@$27CE`)
  - `$0107` (`@$276A`) when the identify byte stack[0] == `$FF`
    (controller absent at port 2)
  - `$0107` (`@$27D0`) on final type mismatch
  - sub-call error code from `+$2484` or `+$2568`
- **Calls**: BSR `+$2484` (controller identify, R4=32), BSR
  `+$2568` (read 6 bytes + magic-check)

Pool:
- `+$2768`, `+$27CE`: `$00FF` (identify / post-read type compare)
- `+$276A`, `+$27D0`: `$0107` (controller-absent / type-mismatch error)

**Behavior**: slot 2's port-2 cart identify-and-read. Identifies the
controller via `+$2484` (sub-cmd 32); if the identify byte is `$FF`
(absent) returns `$0107`. Otherwise reads 6 bytes via `+$2568` and
final-checks the type byte against `$FF`: match returns 0, mismatch
returns `$0107`.

```python
def sub_2734(port_byte):                         # R0 = port byte (entered at +$2732)
    # Step 1: identify controller with sub-cmd 32.
    rc = sub_2484(32, port_byte, stack)
    if mem.B[stack] == 0xFF:                      # @$2768: controller absent
        return 0x0107                             # @$276A
    if rc != 0:
        return rc

    # Step 2: read 6 bytes + magic-check via +$2568.
    rc = sub_2568(stack)
    if rc != 0:
        return rc

    # Step 3: final type check.
    if mem.B[stack] == 0xFF:                      # @$27CE
        return 0                                  # match
    return 0x0107                                 # @$27D0: mismatch
```

### Sub `+$279C` - Port-2 BUP write-then-read (slot 6 inner)

- **Body**: `+$279C` to `+$2854` (186 bytes, 91 instructions)
- **Called by**: slot 6 (BSRF `$1098` at +$1700) - slot 6 maps
  to **BUP_Delete / BUP_Format**
- **Inputs**: R4 = pointer to 11 input bytes (the BUP filename /
  command parameters)
- **Returns** (R0): `0` = success; `$0107` = controller absent
  (first ID byte `$FF`) or final type-byte mismatch; the ack
  status byte (stack+11) if non-zero; non-zero sub return code on
  identify/send/read failure
- **Calls**:
  - BSR `+$2484` (controller identify, R4=48 sub-cmd)
  - BSR `+$21D8` (cart send 38 bytes)
  - BSR `+$22AA` (cart read 6 bytes; R6 = `$00A00000` poll limit)
  - BSR `+$2568` (cart read 6 bytes + magic-check post-write)

**Behavior**: BUP_Delete / BUP_Format inner. Identifies the cart
(early-out `$0107` if the ID byte is `$FF`), builds a 38-byte
command at stack+8 (opcode `$40` at cmd[0], cmd[1..37] zeroed,
then the 11-byte payload copied into cmd[4..14]), sends it, reads
a 6-byte ack into stack+8, validates the ack status byte at
stack+11 (= ack[+3]; non-zero -> return as the error code), then
runs a final `+$2568` magic-check. The final type byte at stack[0]
must equal `$FF` for success (return 0); otherwise `$0107`.

```python
def sub_279C(payload):                           # R4 = 11-byte input buffer
    # Step 1: identify controller (sub-cmd 48).
    rc = sub_2484(48, port_byte=0, out_buf=stack)
    if mem.B[stack] == 0xFF:                     # controller absent
        return 0x0107
    if rc != 0:
        return rc

    # Step 2: build 38-byte command at stack+8: cmd[0]=$40, cmd[1..37]=0,
    # then the 11-byte payload overwrites cmd[4..14].
    cmd = stack + 8
    mem.B[cmd] = 0x40                            # BUP_Delete / Format opcode
    zero(cmd + 1, 37)
    copy(cmd + 4, payload, 11)

    # Step 3: send 38 bytes, then read 6-byte ack into stack+8.
    rc = sub_21D8(cmd, 38)
    if rc != 0:
        return rc
    rc = sub_22AA(stack + 8, 6, poll_limit=0x00A00000)
    if rc != 0:
        return rc

    # Ack status byte (stack+11 = ack[+3]): non-zero is the cart error.
    if mem.B[stack + 11] != 0:
        return mem.B[stack + 11]

    # Step 4: final magic-check via +$2568 (writes type byte to stack[0]).
    rc = sub_2568(stack)
    if rc != 0:
        return rc
    if mem.B[stack] == 0xFF:                      # @$2856: success
        return 0
    return 0x0107                                 # @$2858: final mismatch
```

### Sub `+$2C08` - Slot-7 port-2 multi-packet read (BUP_Dir)

- **Body**: `+$2C08` to `+$2D7E` (376 bytes, 179 instructions)
- **Called by**: slot 7 (BSRF `$130E` at +$18F6) - slot 7 maps
  to **BUP_Dir**
- **Inputs**:
  - R4 = filename-mask pointer (<= 11 bytes, NUL-terminated)
  - R5 = output buffer base
  - R6 = pointer to a 4-byte length cap (max entries)
- **Returns** (R0):
  - `0` = clean end-of-list (final identify byte stack[0] == `$FF`)
  - `$0107` = identify byte == `$FF` at start (controller absent,
    `@$2CA0`) or final mismatch (stack[0] != `$FF` at list end,
    `@$2D80`)
  - `$0108` = `+$2860` returned the list-end sentinel (`@$2D48`)
  - sub-error from `+$2484` / `+$21D8` / `+$22AA` / `+$2860`
- **Calls**: `+$2484` (identify, R4=64; in-loop re-identify uses
  R4=0), `+$21D8` (send 38 bytes), `+$22AA` (read 6 bytes,
  R6 = `@$2D4C` = `$00A00000` poll limit), `+$2860` (decode one
  dir entry)

**Behavior**: BUP_Dir (file-list query). Identifies the cart via
`+$2484` (sub-cmd 64); early-out `$0107` if the ID byte is `$FF`
(absent). Builds a 38-byte command at stack+20: `cmd[0]=$40`,
`cmd[1..37]=0`, the filename mask copied into `cmd[4..14]` (the
NUL-terminator index is recorded at `cmd+15` = stack+35; `cmd+15`
defaults to `$0C`), and the 4-byte length cap (from `*R6`) copied
into `cmd[32..35]`. Sends via `+$21D8`, reads a 6-byte response.
Then iterates per directory entry: the output offset is
`(mem.L[driver_base+$68] >> 5) * idx * 36` (36 = BupDir
output-entry size, `0x24`); `+$2860` decodes one entry into
`out_buf + offset` and updates the continuation byte at stack+4;
the cart is re-identified (`+$2484`, sub-cmd 0) between entries.
The loop ends when stack+4 == `$FF`; it then returns 0 if
stack[0] == `$FF`, else `$0107`. A `+$2860` return of `$0108` ends
the loop with `$0108`; other `+$2860`/sub errors propagate.

```python
def sub_2C08(fname_mask, out_buf, len_cap_ptr):  # R4, R5, R6
    # Step 1: identify cart (sub-cmd 64).
    rc = sub_2484(64, port_byte=0, out_buf=stack)
    if mem.B[stack] == 0xFF:                      # @$2C9E: controller absent
        return 0x0107                             # @$2CA0
    if rc != 0:
        return rc

    # Step 2: build 38-byte query at cmd = stack+20.
    cmd = stack + 20
    mem.B[cmd] = 0x40                             # BUP_Dir command opcode
    zero(cmd + 1, 37)
    mem.B[cmd + 15] = 0x0C                        # default; overwritten by NUL index
    for i in range(11):                           # filename mask -> cmd[4..14]
        b = mem.B[fname_mask + i]
        mem.B[cmd + 4 + i] = b
        if b == 0:
            mem.B[cmd + 15] = i                   # NUL-terminator index (cmd+15 = stack+35)
            break
    copy(cmd + 32, len_cap_ptr, 4)               # 4-byte length cap -> cmd[32..35]

    # Step 3: send 38 bytes, read 6-byte response into cmd.
    rc = sub_21D8(cmd, 38)
    if rc != 0:
        return rc
    rc = sub_22AA(cmd, 6, poll_limit=0x00A00000)
    if rc != 0:
        return rc
    if mem.B[cmd + 3] != 0:                        # response status byte
        return mem.B[cmd + 3]

    # Step 4: per-entry loop until the continuation byte at stack+4 is $FF.
    mem.B[stack + 4] = 0
    idx = 0
    while mem.B[stack + 4] != 0xFF:
        off = (mem.L[driver_base + 0x68] >> 5) * idx * 36   # 36 = BupDir entry size
        rc = sub_2860(out_buf + off, len_cap_ptr, addr(stack + 4))
        if rc == 0x0108:                          # @$2D48: list-end sentinel
            sub_2484(0, 0, stack)                 # re-identify
            return 0x0108
        if rc != 0:
            return rc
        rc = sub_2484(0, 0, stack)                # re-identify between entries
        if rc != 0:
            return rc
        idx += 1

    # Step 5: clean end-of-list.
    if mem.B[stack] == 0xFF:
        return 0
    return 0x0107                                 # @$2D80
```

### Sub `+$2FD0` - Shared port-2 BUP query (BUP_Read / BUP_Verify)

- **Body**: `+$2FD0` to `+$30DA` (268 bytes, 129 instructions)
- **Called by**: slot 5 (BUP_Read), slot 8 (BUP_Verify)
- **Inputs**:
  - R4 = command payload pointer; first 11 bytes are copied into
    the command at bytes 4..14 (BUP filename field)
  - R5 = output buffer base (results scatter-written per
    callee-decided byte count)
  - R6 = byte arg (saved to mem.B[stack+12]; passed downward
    to `+$2D82` as R7)
- **Returns** (R0):
  - `0` = success, response complete
  - `$0107` = no cart at identify (ID byte `$FF`), or response-stream
    desync (data-end marker seen while cart still presents an ID)
  - cart-status byte (non-zero from response[+3]) propagated as
    error code
  - sub-call error codes
- **Calls**: `+$2484`, `+$21D8`, `+$22AA`, `+$2D82` (response
  walker - packs payload into caller's buffer with per-byte
  callback)

**Behavior**: BUP_Stat / BUP_Verify-style query. Sends a 38-byte
command (opcode `$40` at byte 0, the 11-byte payload at bytes 4..14,
all other bytes zero), reads a 6-byte ack, then iterates calling
`+$2D82` to walk the controller response into the caller's output
buffer, advancing the output cursor (R11) by the byte count `+$2D82`
reports for each chunk. Each iteration is followed by a re-identify
(sub-command 0) to keep the cart-link alive. The loop returns 0
either when the response stream's last byte and the re-identify ID
both read `$FF`, or when the re-identify ID reads `$FF` before the
data-end marker; it returns `$0107` if the data-end marker (`$FF`)
appears while the cart still presents a non-`$FF` ID.

The shared body serves both BUP_Read (slot 5: returns payload
bytes in caller's data buffer) and BUP_Verify (slot 8: returns
OK/mismatch status). The distinguishing field is the byte-arg
in R6 which `+$2D82` interprets as the operation discriminator.

```python
def sub_2FD0(payload, out_buf, op_disc):         # R4, R5, R6
    cmd = stack + 24                             # R13 = command/response buffer
    mem.B[stack + 12] = op_disc                  # save for +$2D82 (its R7)

    # Step 1: identify cart (sub-cmd 65); ID byte lands at stack[0].
    rc = sub_2484(65, port_byte=0, out_buf=stack)
    if mem.B[stack] == 0xFF:                      # no cart presenting an ID
        return 0x0107
    if rc != 0:
        return rc

    # Step 2: build 38-byte cmd: opcode $40, 11-byte payload at 4..14,
    # all other bytes zero.
    mem.B[cmd] = 0x40
    for i in range(1, 38):
        mem.B[cmd + i] = 0
    for i in range(11):
        mem.B[cmd + 4 + i] = payload[i]

    rc = sub_21D8(cmd, 38)
    if rc != 0:
        return rc
    rc = sub_22AA(cmd, 6, source_mask=0x00A00000)  # read 6-byte ack into cmd
    if rc != 0:
        return rc

    # Cart-status byte: non-zero is the cart-side error code.
    if mem.B[cmd + 3] != 0:
        return mem.B[cmd + 3]

    # Step 3: walk the response with +$2D82, advancing the output cursor
    # R11 by the bytes written each chunk; re-identify (sub-cmd 0) between
    # chunks.
    R11 = 0
    mem.B[stack + 4] = 0                          # last-byte tracker init
    while True:
        rc = sub_2D82(out_buf + R11, stack + 8, stack + 4, op_disc)
        if rc != 0:
            return rc
        R11 += mem.L[stack + 8]                   # bytes written this chunk

        rc = sub_2484(0, port_byte=0, out_buf=stack)   # re-identify, sub-cmd 0
        if rc != 0:
            return rc

        if mem.B[stack + 4] == 0xFF:              # response-stream end marker
            if mem.B[stack] != 0xFF:              # cart still IDs -> desync
                return 0x0107
            return 0
        if mem.B[stack] == 0xFF:                  # cart released mid-stream
            return 0
```

### Sub `+$32A6` - Slot-4 BUP write/commit (BUP_Write)

- **Body**: `+$32A6` to `+$346E` (458 bytes, 217 instructions)
- **Called by**: slot 4 (BSRF `$27D4` at +$0ACE -> target = $0ACE
  + 4 + $27D4 = `$32A6`)
- **Inputs**:
  - R4 = pointer to caller's dir-entry composite buffer; the write
    command is built from its fields: bytes 0..10 = filename,
    12..21 = comment, 23 = language, 24..27 = date, 28..31 = datasize
  - R5 = save-data source buffer base; block `i` is read from
    `data_buf + i * block_size`
- **Returns** (R0):
  - `0` = success
  - `$0107` = controller absent or ID mismatch at start or finish
  - controller status byte propagated via response[+3]
  - sub-call error codes
- **Calls**:
  - BSR `+$2484` (controller identify, sub-command 80)
  - BSRF `$FFFFEE2C` -> driver `+$21D8` (cart send 38 bytes;
    target = $33A8 + 4 + $FFFFEE2C = $21D8)
  - BSRF `$FFFFEEEA` -> driver `+$22AA` (cart read 6 bytes;
    target = $33BC + 4 + $FFFFEEEA = $22AA)
  - BSRF `$0000021E` -> driver `+$3610` (per-block size compute,
    division primitive - see peripheral_driver.md)
  - BSR `+$30DE` (per-block sub-routine for actual data transfer)
  - BSR `+$2568` (post-block magic-check)

**Behavior**: BUP_Write full flow. Identifies the cart, builds a
38-byte directory-entry write command from the caller's struct
(filename, comment, language, date, datasize placed at fixed
command offsets), sends it and gets a 6-byte ack. Then computes
`block_size = mem.L[mem.L[$06000354] + 104]` (bytes per block) and
`block_count = +$3610(datasize, block_size) + 1`, and iterates
calling `+$30DE` to push each block of save-data over the cart
protocol, with a `+$2568` magic-check after each block. The final
block (remaining bytes <= block_size) sets R8 to `$FF` so the
per-block sub knows to terminate. After the loop it returns 0 if the
post-loop status byte (`stack[0]`, left by the last `+$2568`) reads
`$FF`, otherwise `$0107`.

Entry point is `+$32A6` (BSRF math from the caller at +$0ACE
with R3 = $27D4 gives `$0ACE + 4 + $27D4 = $32A6`). `+$32A4`
is the delay-slot byte of the preceding sub's RTS and is not a
callable entry.

```python
def sub_32A6(entry_buf, data_buf):               # R4, R5
    cmd = stack + 12                             # R14 = command/response buffer

    # Step 1: identify cart (sub-cmd 80); ID byte lands at stack[0].
    rc = sub_2484(80, port_byte=0, out_buf=stack)
    if rc != 0:
        return rc
    if mem.B[stack] == 0xFF:                     # no cart presenting an ID
        return 0x0107

    # Step 2: build 38-byte directory-entry write command. Opcode $40,
    # then fields at fixed cmd offsets (bytes 1..3, 15, 27, 36..37 stay 0).
    mem.B[cmd] = 0x40                            # BUP_Write opcode
    for i in range(1, 38):
        mem.B[cmd + i] = 0
    for i in range(11): mem.B[cmd + 4  + i] = entry_buf[0  + i]   # filename
    for i in range(10): mem.B[cmd + 16 + i] = entry_buf[12 + i]   # comment
    mem.B[cmd + 26] = entry_buf[23]                               # language
    for i in range(4):  mem.B[cmd + 28 + i] = entry_buf[24 + i]   # date
    for i in range(4):  mem.B[cmd + 32 + i] = entry_buf[28 + i]   # datasize

    rc = sub_21D8(cmd, 38)                       # BSRF $FFFFEE2C -> +$21D8
    if rc != 0:
        return rc
    rc = sub_22AA(cmd, 6, source_mask=0x00A00000)  # BSRF $FFFFEEEA -> +$22AA
    if rc != 0:
        return rc

    if mem.B[cmd + 3] != 0:                      # cart status byte
        return mem.B[cmd + 3]

    # Step 3: block_size via double indirection; block_count rounds up.
    block_size  = mem.L[mem.L[0x06000354] + 104]
    datasize    = mem.L[entry_buf + 28]
    block_count = sub_3610(datasize, block_size) + 1   # BSRF $021E -> +$3610

    # Step 4: per-block transfer loop. `remaining` starts at datasize;
    # full blocks send block_size (terminator 0), the final block sends
    # the remaining count with terminator $FF (R8).
    remaining = datasize
    for i in range(block_count):
        if remaining > block_size:
            n, terminator = block_size, 0
            remaining -= block_size
        else:
            n, terminator = remaining, 0xFF
        rc = sub_30DE(data_buf + i * block_size, n, terminator)  # BSR +$30DE
        if rc != 0:
            return rc
        rc = sub_2568(stack)                     # BSR +$2568 post-block check
        if rc != 0:
            return rc

    # Step 5: post-loop status left in stack[0] by the last +$2568.
    if mem.B[stack] == 0xFF:
        return 0
    return 0x0107
```

### External-cart helper subs

These three helpers are leaf-callable from the external-cart
subs above; they are part of the same R4==2 path.

### Sub `+$3478` - Cart-cmd checksum prep

- **Body**: `+$3478` to `+$3558` (226 bytes, 108 instructions;
  single RTS at `+$3556`)
- **Called by**: `+$21D8` (BSRF `$0000126C` at +$2208 -> target =
  $2208 + 4 + $126C = `$3478`)
- **Inputs**:
  - R4 = command buffer ptr (set by `+$21D8` to stack[0])
  - R5 = byte count (set by `+$21D8` to stack[4])
  - R6 = 2-byte suffix appended after the message (stored at
    stack+22, fed in after the payload; caller passes 0)
- **Returns** (R0): 16-bit checksum word that `+$21D8` writes
  to stack[8] and sends as the 2-byte trailer
- **Behavior**: nibble-wise polynomial checksum over the R5-byte
  payload at R4. The 32-bit accumulator is seeded `0 ^ $00FFFF00`
  and preloaded with the first three payload bytes; each step shifts
  it left by 4 bits, and when the top byte is non-zero multiplies
  that byte by `$01102100` (the same polynomial `+$22AA` uses on the
  read path) and XORs the product back. Input bytes are shifted into
  the low byte one per two nibble-steps; after the payload, the two
  R6 suffix bytes are shifted in and the loop stops. The result is
  `(acc >> 8) & $FFFF`, which `+$21D8` writes to stack[8] and sends
  as the 2-byte trailer. Pool data: `$01102100`, `$00FFFF00`.

```python
def sub_3478(buf, count, suffix):                # R4, R5, R6
    mem.W[stack + 22] = suffix                    # 2-byte trailer (caller passes 0)

    # Seed accumulator and preload first 3 payload bytes into its low
    # 24 bits (MSB stays 0).
    acc = (buf[0] << 16) | (buf[1] << 8) | buf[2]
    src = 3                                        # next payload index (R14)
    acc ^= 0x00FFFF00

    nibble = 0      # R10: toggles each step; feed a byte every 2nd step
    suf    = 0      # R13: suffix bytes fed after payload (0,1,2)
    active = 1      # R11
    while active:
        acc = (acc << 4) & 0xFFFFFFFF
        msb = (acc >> 24) & 0xFF
        if msb != 0:
            acc ^= (msb * 0x01102100) & 0xFFFFFFFF

        if nibble != 0:                            # one input byte per 2 steps
            if src < count:
                acc = (acc & ~0xFF) | buf[src]     # shift payload byte into LSB
                src += 1
            elif suf < 2:
                acc = (acc & ~0xFF) | mem.B[stack + 22 + suf]   # suffix byte
                suf += 1
            else:
                active = 0                         # both suffix bytes done -> stop
        nibble ^= 1

    return (acc >> 8) & 0xFFFF                      # acc bytes [1],[2]
```

### Sub `+$2860` - Slot-7 per-entry decode (BUP_Dir extractor)

- **Body**: `+$2860` to `+$2BF6` (920 bytes, 440 instructions)
- **Called by**: `+$2C08` (BSR at +$2D1A) - once per directory
  entry
- **Inputs**:
  - R4 = output slot in caller's entry buffer (computed by
    `+$2C08` as `out_base + (stride/32) * idx * 36`)
  - R5 = command-staging area (stack+12 in `+$2C08`'s frame)
  - R6 = entry-cursor stack ptr
  - R7 = not read by this sub (overwritten at +$28E2 before use)
- **Returns** (R0):
  - `0` = success (entry written to output)
  - `$0108` = terminal path (writes the pending count to the
    caller's R5 buffer, then returns `$0108`)
  - `$0101`-`$0105` / `$0102` via `+$2140` on validation-failure
    paths
- **Calls**: `+$2140` (error-exit cleanup; 5 sites) and two indirect
  `JSR` through the driver pointer at `mem.L[$06000340]`
- **Behavior**: decodes one directory entry for `+$2C08`'s outer
  iteration. It reads the input byte stream from address `$FFFFFE05`
  (with control/status bytes handled at `$FFFFFE04` and `$FFFFFE02`)
  and runs each byte through a state machine that writes the decoded
  fields into the caller's output buffer at R4. Each record is 36
  bytes (`R10 + index*36`): filename, comment, language, date,
  datasize, and block-size fields. Validation failures exit through
  `+$2140` with codes `$0101`-`$0105`; a clean decode returns 0, and
  the terminal path returns `$0108`.

### Sub `+$2D82` - Shared-query response walker (BUP_Read/Verify byte unpack)

- **Body**: `+$2D82` to `+$2FC0` (576 bytes, 267 instructions)
- **Called by**: `+$2FD0` (BSR at +$308A) - once per response
  iteration. `+$2FD0` is the shared cart-protocol entry for
  slot 5 (BUP_Read) and slot 8 (BUP_Verify).
- **Inputs**:
  - R4 = output buffer base (caller's R5 + running cursor)
  - R5 = count-output pointer (caller's stack+8); the per-iteration
    byte count is written to mem.L[R5]
  - R6 = stack+4 (last-byte tracker - sub writes here)
  - R7 = byte arg (the R6 input from `+$2FD0`'s caller - used
    as operation discriminator between BUP_Read and BUP_Verify)
- **Returns** (R0): `0` on success; `$0109` on a BUP_Verify mismatch;
  `$0101`/`$0102`/`$0103`/`$0104`/`$0105` via `+$2140` on the
  validation-failure paths
- **Side effects**: updates `mem.L[stack+8]` with the number
  of bytes written this iteration (used by `+$2FD0` to advance
  its `R11` output cursor)
- **Calls**: `+$2140` (error-exit cleanup; 5 sites) and two indirect
  `JSR` through the driver pointer at `mem.L[$06000340]`
- **Behavior**: decodes one chunk of response data through the same
  byte-stream state machine as `+$2860` - reading input from address
  `$FFFFFE05` (control/status at `$FFFFFE04`/`$FFFFFE02`) and issuing
  the transfer via the indirect `mem.L[$06000340]` pointer. The R7
  discriminator selects the per-byte action: `R7 == 0` copies each
  decoded byte into the caller's output buffer (BUP_Read); `R7 != 0`
  compares each byte against the caller's buffer and sets a mismatch
  flag (BUP_Verify), returning `$0109` if any byte differs. The last
  decoded byte is written to `mem.B[R6]` (caller's stack+4); the
  caller treats `$FF` there as response-exhausted and stops looping.

## Date math helpers (used by slots 9/10)

These helpers are used by BUP_GetDate / BUP_SetDate (slots 9
and 10).

### Sub `+$1D96` - Fill days-before-month table

- **Body**: `+$1D96` to `+$1DC2` (46 bytes, 23 instructions)
- **Called by**: `+$1DC4` (BUP_GetDate day-of-year converter,
  internal BSR at +$1DDC) and slot 10 (BUP_SetDate, BSR at
  +$1F6E)
- **Inputs**: R4 = pointer to 44-byte (11 longwords) output
  table on caller's stack
- **Returns**: nothing (table written via @R4 stores)
- **Side effects**: writes 11 longwords at R4, R4+4, R4+8,
  ..., R4+40 containing days-before-month values for months
  February through December (NON-leap year):

| Index | Stored value | Months elapsed |
|-------|--------------|----------------|
| 0 | 31 | Jan |
| 1 | 59 | Jan+Feb (28) |
| 2 | 90 | +Mar (31) |
| 3 | 120 | +Apr (30) |
| 4 | 151 | +May (31) |
| 5 | 181 | +Jun (30) |
| 6 | 212 | +Jul (31) |
| 7 | 243 | +Aug (31) |
| 8 | 273 | +Sep (30) |
| 9 | 304 | +Oct (31) |
| 10 | 334 | +Nov (30) |

**Behavior**: a leaf sub that initializes a stack-resident
days-before-month table. The table is consumed by `+$1DC4`
(decode path) and slot 10 (encode path). Both date routines
must invoke `+$1D96` before doing per-month math.

```python
def sub_1D96(table_ptr):                         # R4
    # Writes 11 longwords: days-before-month for Jan, Jan+Feb, ...
    # non-leap year baseline; +$1DC4 adds +1 for months > Feb when
    # the leap flag is set.
    days_before = [31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334]
    for i, d in enumerate(days_before):
        mem.L[table_ptr + i*4] = d
```

### Sub `+$1DC4` - Day-of-year -> (month, day-of-month) converter

- **Body**: `+$1DC4` to `+$1E6E` (172 bytes, 86 instructions)
- **Called by**: slot 9 (BUP_GetDate) at +$1F18 and +$1F2A
- **Inputs**:
  - R4 = day-of-year (0-based; 0 = Jan 1 ... 365 = Dec 31 in leap)
  - R5 = `Uint8 *` month output pointer
  - R6 = `Uint8 *` day-of-month output pointer
  - R7 = leap-year flag (byte at @R15 after entry; 0 = non-leap,
    non-zero = leap)
- **Returns**: no R0 value; results written via R5/R6
- **Calls**: BSR `+$1D96` (init stack table at +$1DDC)
- **Side effects**: `*R5 = month (1-12)`, `*R6 = day-of-month (1-31)`

**Algorithm**: fills the non-leap days-before-month table with
`+$1D96`, then resolves the month. January is special-cased
(`day < 31` -> month 1, day-of-month = `day + 1`). Otherwise it scans
the table from index 1 (capped at index 11 = December): the non-leap
path stops at the first `table[i] > day` and sets day-of-month =
`day - table[i-1] + 1`; the leap path (`R7 != 0`) stops at the first
`table[i] >= day`, sets day-of-month = `day - table[i-1]`, and adds 1
when the month is February. The two boundary tests plus the February
bump let the single non-leap table serve leap years (e.g. day 59
resolves to Feb 29 in a leap year but Mar 1 in a non-leap year).

```python
def sub_1DC4(day, month_out, dom_out, leap):     # R4, R5, R6, R7
    table = stack + 4                             # 11 longwords
    sub_1D96(table)                               # non-leap days-before-month

    if day < table[0]:                            # January (day < 31)
        mem.B[month_out] = 1
        mem.B[dom_out]   = (day + 1) & 0xFF
        return

    i = 1
    if leap == 0:
        while table[i] <= day and i < 11:         # stop at first table[i] > day
            i += 1
        mem.B[month_out] = i + 1
        mem.B[dom_out]   = (day - table[i - 1] + 1) & 0xFF
    else:
        while table[i] < day and i < 11:          # stop at first table[i] >= day
            i += 1
        mem.B[month_out] = i + 1
        dom = (day - table[i - 1]) & 0xFF
        if i + 1 == 2:                            # February in a leap year
            dom = (dom + 1) & 0xFF
        mem.B[dom_out]   = dom
```

### Sub `+$355C` - Signed 32-bit division (slot 10 helper)

- **Body**: `+$355C` to `+$360F` (180 bytes; two RTS exits at `+$35F6`
  normal / `+$3602` divide-by-zero; literal pool at `+$3608`)
- **Called by**: slot 10 (BUP_SetDate, BSRF disp `$15DC` at
  +$1F7C)
- **Inputs**: R0 = divisor (signed), R1 = dividend (signed)
- **Returns**: R0 = quotient (signed); on divide-by-zero, R0 = 0 and
  the error flag at `mem.L[$06000350]` is set to `$044E`
- **Behavior**: standard SH-2 32-step signed division. Used by
  BUP_SetDate to compute `year / 4` for the 4-year-cycle
  decomposition. Signed because the year offset can be
  negative (date codes earlier than 1980-01-01 are not
  expected from games but the sub handles them).

```python
def sub_355C(divisor, dividend):                 # R0 = divisor, R1 = dividend (both signed)
    if divisor == 0:
        mem.L[0x06000350] = 0x0000044E            # set error flag
        return 0
    # SH-2 implements via DIV0S setup followed by 32 DIV1 + ROTCL
    # steps; this captures the net effect.
    return int(dividend / divisor)                # signed integer division (truncate toward 0)
```
