# Peripheral Driver

The 16 KB driver the BIOS decompresses out of `$0007D660` into a
caller-supplied buffer when PER_Init runs. Disassembled per-slot
reference: 11 function-pointer slots at the buffer head, the
per-slot function bodies, and the shared sub-routines for
peripheral packet handling and integer division/modulo. The
relocator writes the buffer base into the WRAM-H slot at
`$06000354` so game code can find the driver and dispatch through
its slot table.

The driver also contains the Backup Memory (BUP) library that
talks to backup RAM / backup cartridges via the same dispatch
table. The BUP portion — including all BUP cart-protocol subs and
the slot-to-BUP-function mapping — is documented in
[backup_library.md](backup_library.md). Only the peripheral-input
half of the driver is covered here.

## Peripheral Library (PER_*)

The peripheral library provides game code with controller / pad
input data. It has two entry points published in the SYS_*/PER_*
dispatch table:

- `$06000358` (slot) → `$0007D600` (BIOS-ROM body): PER_Init
- `$0600035C` (slot) → `$0007D660` (BIOS-ROM body): PER_GetPer
  data block

#### PER_Init ($0007D600)

Pseudo-code:

```python
def PER_Init(buf, arg1, arg2):                   # R4 = driver buffer, R5, R6
    # Decompress the 16 KB driver into the caller-supplied buffer.
    # sub_1F04(source, dest_ptr, size): dest_ptr points to a word
    # holding the output base. PER_Init stashes buf at SP and
    # passes SP as dest_ptr.
    mem.L[SP] = buf
    sub_1F04(mem.L[0x0600035C], SP, 0x4000)       # source -> $0007D660; into buf

    # Enter the freshly decompressed driver at its head (the slot-0
    # relocator), with R4 = caller R5, R5 = caller R6.
    jsr(buf, arg1, arg2)                          # JSR @R2
```

PER_Init is a load-and-enter stub, not a direct peripheral-init
function: it decompresses the compressed driver image (the
PER_GetPer data block at `$0007D660`) into the caller-supplied
buffer (caller's R4), then JSRs into the buffer head to run the
driver's slot-0 relocator. The decompressed driver persists in the
caller's buffer; the relocator publishes the buffer base to
`$06000354` so game code can dispatch through the slot table on
later calls.

**Calling-convention contract:** caller's R4 is the driver buffer
base. After decompression PER_Init does `JSR @R2` (now the
decompressed relocator) with R4 = the caller's R5 and R5 = the
caller's R6. The R5/R6 -> R4/R5 register shuffle before the JSR is
load-bearing - the relocator reads its arguments from R4/R5. The
relocator returns normally; PER_Init then RTSes to the caller.

#### PER_GetPer data block ($0007D660)

The slot at `$0600035C` holds the address of a data block at
`$0007D660`. The block is **not** directly callable as a function
in its raw form; it is the input to `sub_1F04`. Byte layout:

| Offset | Bytes | Meaning |
|--------|-------|---------|
| +0 | `10 01` | sub_1F04 header (bit 0 = compressed flag; upper bits unused / reserved) |
| +2 | `01 37` | block_count = 311 (number of main blocks) |
| +4 | `FF FF` | selector word for block 0 - all 16 bits set, so all 16 tokens are literals. A block is a selector word followed by 16 two-byte tokens; a literal token copies its word verbatim. |
| +6 | `2F E6 2F D6 2F C6 ...` | first compressed-stream payload bytes. Because the first selector is `$FFFF` (all-literal), these bytes appear verbatim in the decompressed driver and coincidentally form a valid SH-2 prologue. |

Block 0's selector is `$FFFF`, so all 16 of its tokens are
literals, and each literal copies one 2-byte word - so block 0
emits 32 verbatim bytes. Those bytes (ROM `$07D666`-`$07D685`) are
byte-identical to the first 32 bytes of the decompressed driver
(the slot-0 relocator entry, decompressed `$0000`-`$001F`). Block
1's selector word follows at ROM `$07D686` (data-block offset
+$26) and uses back-references, so from there the raw ROM bytes
are `sub_1F04` control tokens, not executable code. Refer to the
**decompressed slot 0 walkthrough below** for the real
disassembly.

## Peripheral Driver ($0007D660, decompressed)

The peripheral driver lives in BIOS ROM at $0007D660 (sub_1F04
header followed by compressed body). PER_Init decompresses the
body into the buffer at caller's R4 ($0607C000 for NiGHTS) and
JSRs into it. The decompressed image fills up to the caller's
R6 cap; the only invocation in the BIOS passes R6 = $4000
(16 KB), so the decompressed driver is 16 KB.

After PER_Init's JSR @R2 returns, the buffer is structured as:

```
+$0000  function-pointer table (11 callable slots, indices 0-10,
        44 bytes) + 1 data-pointer slot (slot index 11 = +$2C),
        48 bytes total
+$0030  relocator continuation code, then function bodies
        and PC-relative constant pools interleaved
+$3FFF  end of buffer
```

The first function in the buffer (the relocator) overwrites the
head of the buffer with the 11 function-pointer slots as it runs.
Game code holds the buffer base address and calls into the
driver via `JSR @($00,driver_base)` for slot 0, `JSR @($04,driver_base)`
for slot 1, and so on.

### Source bytes

```
ROM $0007D660: 10 01    sub_1F04 header (bit 0 set = compressed)
ROM $0007D662: 01 37    block_count = 311
ROM $0007D664: FF FF    selector of first block (all 16 tokens literal)
ROM $0007D666+: compressed stream (311 main blocks + tail)
```

Note: only bit 0 of the header is used by sub_1F04 (compressed vs.
raw). The upper bits ($1000) are reserved/arbitrary and do NOT
indicate output size. The actual decompressed size is determined
by the compressed stream's `block_count` + tail and bounded by the
caller's R6.

### Function-pointer table (slots 0-10)

Computed by walking the relocator's patch pattern (each slot N is
`pool_value[N] + anchor_pc[N]`):

| Slot | Body offset | Prologue bytes | Function purpose |
|------|-------------|----------------|------------------|
| 0 | `+$0000` | `2F E6 2F D6 …` | The relocator itself (also slot 0 of the patched table — see "Slot 0 self-reference" below) |
| 1 | `+$02C4` | `2F E6 60 43 …` | Port-2 simple query |
| 2 | `+$03AA` | `2F E6 2F D6 …` | Peripheral data read (IMS-gated) |
| 3 | `+$0672` | `2F E6 2F D6 …` | Core peripheral-data / cartridge poll |
| 4 | `+$0A78` | `2F E6 2F D6 …` | High-level packet build orchestrator |
| 5 | `+$135A` | `60 43 4F 22 …` | Lightweight query (variant) |
| 6 | `+$16DC` | `4F 22 60 43 …` | Lightweight query (variant) |
| 7 | `+$18B4` | `2F E6 60 43 …` | Port-2 BUP packet build (BUP_Dir) |
| 8 | `+$1ACC` | `4F 22 60 43 …` | Lightweight query (variant) |
| 9 | `+$1E70` | `2F E6 2F D6 …` | BUP_GetDate (packed-date decode) |
| 10 | `+$1F64` | `2F E6 6E 43 …` | BUP_SetDate (date encode) |

The driver buffer contains 97 RTS instructions total — most
functions either call sub-routines internally or contain
loop-and-branch sequences that produce additional RTS points
(epilogues at multiple early-out paths).

### Constant the relocator writes to ($06000354)

Before the slot-build loop, the relocator does:

```
MOV.L @(pool,PC),R14   ; R14 = pool value = $06000354
MOV.L R4,@R14           ; mem.L[$06000354] = caller's R4 (driver base)
```

`$06000354` is the WRAM-H slot the driver uses to publish its
own load address (the BIOS-published PER_GetPer slot itself is
`$0600035C`, see line 26). By writing the load address to
`$06000354`, the driver makes itself findable: game code that
JSRs through `@($06000354)` lands at the driver's base = at
slot 0 of the function-pointer table.

### Slot 0 self-reference

The pool value for slot 0 is `$FFFFFFE0` (-32 signed) and its
anchor PC is `$0607C020`, so the patched absolute address is
`$0607C000` = the driver base = the table itself. Slot 0 thus
points to slot 0 — a self-reference. PER_Init's `JSR @R2`
invokes this entry once at install time. If game code later
re-invokes through the slot-0 pointer the entire relocator
body re-runs, including the patch loop and port-1/port-2
init dispatch — it is idempotent but not a no-op.

### Function call style

All driver functions use **RTS** with normal SH-2 calling
convention. Game code calls the driver entries via
`JSR @table[N]` (reading the function pointer from the table at
the driver's base, then JSR'ing through it). Each function
preserves PR via `STS.L PR,@-R15` at entry and `LDS.L @R15+,PR`
+ `RTS` at exit. There is no interrupt-handler-style RTE in the
driver — peripheral data refresh is initiated by game code's
JSR (typically from inside the game's own VBLANK handler), not
by the driver receiving an IRQ directly.

### Per-function disassembly status

Each slot's function body is documented below as its
disassembly progresses. The format for each entry:

- **Slot N (`+$XXXX`)** — short name once identified.
- **Inputs** — register-passed args (R4, R5, …) and any
  in-buffer state read.
- **Outputs** — register returns and side effects (writes to
  WRAM-H slots, SMPC OREGs, etc.).
- **Disassembly** — annotated SH-2 listing for the function body.
- **Behavior** — what it does in plain language for a Go
  reimplementation.

#### Slot 0 (`+$0000`) — Driver init (relocator + first-peripheral fetch)

- **Called by**: PER_Init's `JSR @R2` at BIOS `$0007D620`.
- **Inputs**: R4 = driver buffer base; R5 = caller's peripheral
  output buffer (the R6 arg from PER_Init's caller, shuffled
  into R5).
- **Body**: `+$0000` to `+$02C2`, 708 bytes incl. RTS delay slot
  (RTS at `+$02C0`, delay slot `MOV.L @R15+,R14` at `+$02C2`;
  slot 1 starts at `+$02C4`).
- **Effects**: builds the 11-entry function-pointer table at
  R4+$00..R4+$2B; registers driver in `mem.L[$06000354]`;
  initializes driver state at `driver_base+$54..+$67`; writes
  peripheral record header to caller's R5 buffer; dispatches on
  current peripheral status (read from the A-bus-CS1 address
  `$24FFFFFF` in pool[+$14C] — see note below) to
  peripheral-type-specific init for port 1; and again for port 2.

```python
def slot_0_init(driver_base, output_buf):        # R4 = driver_base, R5 = output buf
    # Step 1: register the driver. Game code can now reach it
    # via JSR through @($06000354).
    mem.L[0x06000354] = driver_base

    # Step 2: patch the function-pointer table at driver_base+$00..+$2B.
    # Each slot N's pool entry at +$0120+4N is a relative offset; add
    # the anchor PC (where the MOVA captures it) to get the absolute
    # address of slot N's body.
    for n in range(11):                           # 11 callable slots
        offset    = mem.L[driver_base + 0x0120 + n*4]
        anchor_pc = driver_base + 0x0020 + n*8
        mem.L[driver_base + n*4] = (offset + anchor_pc) & 0xFFFFFFFF

    # Step 3: slot-11 entry is a DATA pointer (not a function). It
    # holds the per-entry working buffer base at driver_base + $78.
    mem.L[driver_base + 0x2C] = driver_base + 0x78

    # Step 4: per-port init. Loops for port 1 then port 2; structure
    # mirrors except for marker offsets ($54 vs $56) and type constants
    # ($0400 for port 1 type-$00, $0200 for port 1 type-$20, similar
    # but mirrored for port 2).
    for port in (1, 2):
        # Write the per-port peripheral record header in the caller's
        # output buffer: 8 bytes per port (type word, length word,
        # port-id word, reserved word).
        mem.W[output_buf + 0] = 1                 # type
        mem.W[output_buf + 2] = 1                 # length
        mem.B[driver_base + 0x54 + (port - 1) * 2] = 1   # port marker (provisional)

        # Read the peripheral-status byte at $24FFFFFF (A-bus CS1).
        # The top 3 bits select the dispatch arm:
        #   $00 -> digital pad path
        #   $20 -> analog / wheel path
        #   else -> unknown peripheral path
        # NOTE: $24FFFFFF is in the unmapped portion of the A-bus
        # cartridge window; what the SMPC/hardware actually returns
        # at this address has not been verified at runtime.
        status    = mem.B[0x24FFFFFF]
        type_bits = status & 0xE0

        if type_bits == 0x00:
            # Digital: type constant R8 = $0400; write 2 to record;
            # store peripheral-type-specific state at driver_base+$58
            # (port 1) / +$5C (port 2).
            write_digital_state(output_buf, driver_base, port, type_const=0x0400)
        elif type_bits == 0x20:
            # Analog: type constant R10 = $0200; write 7 to record;
            # store state at driver_base+$60 (port 1) / +$64 (port 2).
            write_analog_state(output_buf, driver_base, port, type_const=0x0200)
        else:
            write_unknown_state(output_buf, driver_base, port)

        # Convergence: call the peripheral-init helper at driver +$20C4
        # (BSRF via pool[+$0314] = $1E54). It performs additional
        # per-port init (peripheral count, button-state seed).
        sub_20C4(output_buf + 8)

        # Advance to the next port's record header.
        output_buf += 8

    # Epilogue: restore callee-save regs (R8-R14, PR) and return.
```

- `mem.L[$06000354] = driver_base` (driver registration)
- `mem.L[driver_base + N*4]` for N in [0,10]: function-pointer
  table (per the patch loop)
- `mem.L[driver_base + 11*4]` = `driver_base + $78`
- `mem.B[driver_base + $54]`: port 1 marker (1 = pad present)
- `mem.B[driver_base + $56]`: port 2 marker (read by slots 2, 3,
  5, 6, 8 via `ADD #84,R; MOV.B @(2,R),R0`)
- `mem.L[driver_base + $58]` / `+$5C` / `+$60` / `+$64`:
  peripheral-type-specific state (R8/R10 constants by peripheral
  type)
- `mem.W[caller_R5 + 0..7]`: port 1 record (type + length words)
- `mem.W[caller_R5 + 8..15]`: port 2 record (similar)
- All callee-save registers (R8-R14, PR) restored before RTS.

**Pool values (relative to driver_base)**:

```
+$0112  $0400        constant used for type-$00 (digital) state
+$0114  $0200        constant used for type-$20 (analog) state
+$0116  $1000        constant (used in dispatch sub-routine)
+$0118  $0800        constant (similar)
+$011C  $06000354    WRAM-H driver-base registration slot
                     (driver writes its load address here)
+$0120  $FFFFFFE0    slot 0 relative offset (= -32)
+$0124  $0000029C    slot 1 relative offset
+$0128  $0000037A    slot 2 relative offset
+$012C  $0000063A    slot 3 relative offset
+$0130  $00000A38    slot 4 relative offset
+$0134  $00001312    slot 5 relative offset
+$0138  $0000168C    slot 6 relative offset
+$013C  $0000185C    slot 7 relative offset
+$0140  $00001A6C    slot 8 relative offset
+$0144  $00001E08    slot 9 relative offset
+$0148  $00001EF4    slot 10 relative offset
+$014C  $24FFFFFF    peripheral-status read address (needs verification —
                    likely SMPC-area pointer; the byte read here selects
                    digital vs. analog vs. other dispatch path)
+$0240  $0800        word; written to base+$58 in the $24000003==$00A4 arm
+$0242  $0100        word; written to base+$5C in the same arm
+$0244  $00A4        word; comparand for the $24000003 status byte
+$0248  $00001E64    BSRF offset to sub +$2028 (added to anchor $0607C1C4)
+$024C  $24000001    additional A-bus CS1 status-read address
+$0250  $24000003    additional A-bus CS1 status-read address
+$0254  $00001E42    BSRF offset to sub +$2066 (added to anchor $0607C224)
+$0314  $00001E54    BSRF offset to helper +$20C4 (added to anchor $0607C270)
```

**Net effects** of slot 0 once it returns:
- The 11-entry function-pointer table at `R4+$00..R4+$2B` is
  populated with absolute addresses computed from the pool values
  plus their respective anchor PCs.
- The slot-11 data-pointer slot at `R4+$2C` holds `R4+$78`.
- `mem.L[$06000354]` is set to the driver buffer base.
- Driver-state markers at `driver_base+$54..+$67` reflect the
  peripheral type the SMPC reports.
- The caller's R5 buffer holds one peripheral record per port.

The slot-0 dispatch reading from `$24FFFFFF` is unusual - that's
inside the unmapped portion of the cache-disabled A-bus / cartridge
window and would not normally return useful bytes. The body also
reads status bytes at `$24000001` and `$24000003` (pool +$24C/+$250)
in the same window, and makes two further sub calls - `+$2028`
(BSRF pool +$248) and `+$2066` (BSRF pool +$254) - in addition to
the `+$20C4` helper. A post-relocation runtime trace would confirm
what addresses are actually accessed.

#### Slot 1 (`+$02C4`)

Port-2 BUP cart query. Documented in [backup_library.md](backup_library.md).

#### Slot 2 (`+$03AA`) — Peripheral data read / packet collection

- **Body**: `+$03AA` to `+$0670` (712 bytes)
- **Inputs**: R4 = port selector (must be 2 for fast path; non-2
  triggers a different branch via BSR `+$0388`)
- **Returns** (R0): `0` = success / loop-end; `1` = no peripheral
  present on port 2; `3` = sub returned status `$23`; `8` =
  data-mismatch path; `-1` = error code from sub `+$2732`
- **Calls**:
  - BSR `+$0388` (sub between slot bodies — see "Sub-routines")
  - BSR `+$033E` (similarly placed sub)
  - BSR `+$0672` (which is slot 3's entry — slot 2 calls slot 3)
  - BSRF with offset `$0000233A` → driver `+$2732` (peripheral
    SMPC poll routine)
  - JSR @R9 (R9 = pool[$1C4C] + anchor `$0607C3C4` =
    `$0607E010` — callback inside the driver)
  - JSR @R11-deref (R11 = `$06000340` WRAM-H slot)

```python
def slot_2_peripheral_read(port_sel):            # R4 = port selector
    # Port-2 fast path: short-circuit for cart-protocol port 2.
    if port_sel == 2:
        if mem.B[driver_base + 0x56] == 0:        # port-2 marker
            return 1                               # no peripheral

        # Call SMPC peripheral-poll sub +$2732 (BSRF disp $233A).
        # R4 = driver_base+$74 (input pointer staged in driver state).
        rc = sub_2732(mem.B[driver_base + 0x74])

        # Translate cart-poll return code.
        if rc == 0:     return 0
        if rc == 0x21:  return 1
        if rc == 0x23:  return 3
        return -1

    # Non-port-2 path (R4 != 2).
    # Step 1: stage helper data on stack.
    bitmask     = sub_0388(stack + 28)            # 8 bytes {$80..$01}
    magic_buf   = sub_033E(stack + 8)             # 16-byte "BackUpRam Format"

    # Step 2: call slot 3 (data scan + driver-state setup).
    rc = slot_3(port_sel, 0, stack + 36)
    if rc not in (0, 2):
        return rc                                  # propagate error

    # Step 3: zero per-peripheral output area in the slot-11 data
    # buffer at mem.L[driver_base+$2C].
    work_buf = mem.L[driver_base + 0x2C]
    for i in range(mem.L[driver_base + 0x38]):
        mem.B[work_buf + i] = 0

    # Step 4: mode-1 path (driver_base+$34 == 1) gates an SCU IMS
    # save + a series of callback invocations at +$2010 (the
    # ~10 ms busy-wait used between SMPC reads).
    mode = mem.L[driver_base + 0x34]
    if mode == 1:
        saved_ims = mem.L[0x06000348]
        bios_ims_call(-1)                          # JSR *$06000340, mask all

        # Mode-value sub-dispatch on driver_base+$3C:
        #   == $0200 -> write byte at data_buffer[+5]
        #   == $0100 -> write byte at data_buffer[+10]
        if mem.L[driver_base + 0x3C] == 0x0200:
            mem.B[work_buf + 5] = mem.B[stack + 30]
        elif mem.L[driver_base + 0x3C] == 0x0100:
            mem.B[work_buf + 10] = mem.B[stack + 33]

    # Common addressing: the peripheral data stream and the per-entry
    # stride between peripheral blocks both live in driver state.
    stream = mem.L[driver_base + 0x30]
    stride = mem.L[driver_base + 0x40]

    # Step 5: Pass 1 - replicate the staged sub_033E bytes (at stack+8)
    # into the odd bytes of the data stream. Outer count is
    # (driver_base+$3C >> 4); inner is a fixed 16.
    for periph in range(mem.L[driver_base + 0x3C] >> 4):
        for inner in range(16):
            mem.B[stream + (periph * 16 + inner) * 2 + 1] = stack[8 + inner]
    if mode == 1:
        sub_2010_delay()                           # ~10 ms callback, once after the pass

    # Step 6: Pass 2 - zero the odd bytes of each peripheral block,
    # starting at peripheral index 2, stepping by the stride.
    for periph in range(2, mem.L[driver_base + 0x38]):
        base = stream + stride * periph
        for i in range(mem.L[driver_base + 0x3C]):
            mem.B[base + i*2 + 1] = 0
        if mode == 1:
            sub_2010_delay()                       # once per peripheral

    # Step 7: Pass 3 - build the presence bitmap in work_buf. For each
    # peripheral (from index 2) whose stream block has any non-zero odd
    # byte, set bit (periph & 7) of work_buf[periph >> 3] using the
    # bitmask table at stack+28.
    for periph in range(2, mem.L[driver_base + 0x38]):
        base = stream + stride * periph
        for byte_idx in range(mem.L[driver_base + 0x3C]):
            if mem.B[base + byte_idx*2 + 1] != 0:
                mem.B[work_buf + (periph >> 3)] |= bitmask[periph & 7]

    # Step 8: Pass 4 - reflect the work_buf bitmap into the odd bytes of
    # peripheral block 1 (base = stream + stride). Indices within the
    # bitmap range copy work_buf; the remainder up to $3C are zeroed.
    base = stream + stride
    for j in range(mem.L[driver_base + 0x3C]):
        if j < (mem.L[driver_base + 0x38] >> 3):
            mem.B[base + j*2 + 1] = mem.B[work_buf + j]
        else:
            mem.B[base + j*2 + 1] = 0
    if mode == 1:
        sub_2010_delay()

    # Step 9: Verification pass - compare the block-1 odd bytes
    # (base = stream + stride) against the slot-11 data-buffer. On any
    # mismatch, mode 1 issues a final BIOS IMS call (R4 = saved_ims)
    # and returns 8.
    for i in range(mem.L[driver_base + 0x38] >> 3):
        if mem.B[(stream + stride) + i*2 + 1] != mem.B[work_buf + i]:
            if mode == 1:
                bios_ims_call(saved_ims)
            return 8                               # data-mismatch path

    # Step 10: final BIOS IMS-restore on mode-1 success path.
    if mode == 1:
        bios_ims_call(saved_ims)
    return 0
```

**External references made by slot 2**:

| Source | Target | Purpose |
|--------|--------|---------|
| BSR `+$0388` | sub `+$0388` (between slots) | Data-prep |
| BSR `+$033E` | sub `+$033E` (between slots) | Data-prep |
| BSR `+$0672` | slot 3 | Data scan |
| BSRF `$233A` | `+$2732` | Peripheral poll sub |
| JSR @R9 | `$0607E010` (= driver `+$2010`) | Mode-1 callback (multiple call sites) |
| JSR @R11-deref | `mem.L[$06000340]` | BIOS WRAM-H slot routine (e.g., IMS manipulation) |

**Pool constants used by slot 2** (relative offsets in driver):

```
+$040C  $00001C4C     callback offset (added to anchor $0607C3C4 = $0607E010)
+$0410  $06000340     WRAM-H BIOS function-pointer slot
+$0414  $0000233A     BSRF offset (added to $0607C3F8 = $0607E732 = driver +$2732)
+$04B8  $0200         mode constant
+$04BA  $0100         mode constant
+$04BC  $06000348     SCU IMS shadow address
```

**Driver-state fields touched by slot 2** (relative to driver_base):

```
+$2C  slot 11 = data buffer pointer (= driver_base + $78)
+$30  data-stream base pointer (read via mem.L @(48,base))
+$34  mode selector (1 = use R9-callback path)
+$38  count for inner loops (read via mem.L @(56,base))
+$3C  mode-value comparand ($0200 vs $0100 branches)
+$40  per-peripheral stride (driver_base+$40 read via @(64,R4))
+$54  port-1 marker byte
+$56  port-2 marker byte (tested for early-out)
+$74  input pointer to sub +$2732 (port-2 poll)
```

#### Slot 3 (`+$0672`) — Peripheral data fetch / cartridge poll

- **Body**: `+$0672` to `+$09E8` (888 bytes)
- **Inputs**: R4 = port selector (2 = port 2 path; else = port 1
  path); R5 = context buffer (saved to stack+28); R6 = output
  struct (held in R13)
- **Returns** (R0): `0` = success / OK; `1` = no peripheral
  present / port-2-marker-zero short-circuit; `2` = `+$1576`
  sub said retry / busy; `3` = sub returned `$24`-class code;
  `-1` = sub returned anything else
- **Calls**:
  - BSRF `$00001FB6` (added to `$0607C6B8` = `$0607E66E` =
    driver `+$266E`)
  - BSRF `$00001EE8` (added to `$0607C6E0` = `$0607E5C8` =
    driver `+$25C8`)
  - BSRF `$00001576` (added to `$0607C7BA` = `$0607DD30` =
    driver `+$1D30`)
  - BSR `+$0388` (data-prep sub between slot bodies)
  - BSR `+$09EA` (sub immediately after slot 3 body — a "wait
    for SMPC SR ready" loop, see "Sub-routines")
  - BSRF `pool[$0A6C]` and `pool[$0A70]` near end (two final
    sub calls; offsets at +$0A6C / +$0A70 in the pool)

Major register usage:
- R8 = constant 1 (used as boolean / counter init)
- R9 = data-cursor / loop variable through inner passes
- R10 = constant 4 (peripheral-data record stride?)
- R11 = boolean flag (set/cleared at various points)
- R12 = constant 0 (used as zero in many `EXTU.B R12,R..` etc.)
- R13 = `R6` arg from caller (slot 2 passes R6 = stack+36)
- R14 = `$06000354` (pool) — used to load driver_base in body

Pool values:
- `+$06F4`: `$06000354`
- `+$06F8`: `$00001FB6` (BSRF offset to driver `+$266E`)
- `+$06FC`: `$00001EE8` (BSRF offset to driver `+$25C8`)

```python
def slot_3_peripheral_fetch(port_sel, ctx_a, ctx_b):  # R4, R5, R6
    # Port-2 fast path.
    if port_sel == 2:
        if mem.B[driver_base + 0x56] == 0:        # port-2 marker
            return 1                               # no peripheral

        # First poll: BSRF disp $1FB6 -> driver +$266E
        #   R4 = stack+48 (result buffer); R5 = mem.B[driver_base+$74]
        rc = sub_266E(stack + 48, mem.B[driver_base + 0x74])
        if rc != 0:
            # Translate cart-status: $23 -> 3, $24 -> 2, $0101/$0106 -> 1
            return translate_cart_status(rc)

        # Copy the +$266E result fields into ctx_b, then stage ctx_a
        # at stack+20 for the second poll.
        mem.L[ctx_b + 0] = mem.L[stack + 52]
        mem.L[ctx_b + 4] = mem.L[stack + 56]
        mem.L[ctx_b + 8] = mem.W[stack + 60]
        mem.L[stack + 20] = ctx_a

        # Second poll: BSRF disp $1EE8 -> driver +$25C8 (multi-field reader)
        rc = sub_25C8(ctx_b + 12, ctx_b + 16, stack + 20)
        if rc == 0:
            mem.L[ctx_b + 20] = mem.L[stack + 20]
            return 0
        return translate_cart_status(rc)         # $21 -> 1, $23 -> 3, $24 -> 2

    # Non-port-2 path. The +$0888 dispatch selects sub-mode:
    #   R0 == 0 -> standard setup at +$0752 (reads driver_base+$54)
    #   R0 == 1 -> extended setup at +$07DE (reads driver_base+$55)
    #   else    -> default fail at +$0884
    bitmask = sub_0388(stack + 40)                 # 8-byte table {$80..$01}

    if sub_mode == 0:
        if mem.B[driver_base + 0x54] == 0:        # port-1 marker
            return 1                               # no peripheral
        # Standard data-stream setup: internal backup RAM base.
        mem.L[driver_base + 0x30] = 0x20180000    # cache-through alias of $00180000
        mem.L[driver_base + 0x34] = 0             # mode flag (no IRQ masking)
        mem.L[driver_base + 0x38] = 0x0200        # entry count
        mem.L[driver_base + 0x3C] = 64            # block size
        mem.L[driver_base + 0x40] = 0x0080
        mem.L[driver_base + 0x44] = 2
        mem.L[driver_base + 0x4C] = 0x20180080    # first dir entry
        mem.L[ctx_b + 0] = 0x8000                 # packet header
        mem.L[ctx_b + 4] = 0x0200                 # mirrors entry count
        mem.L[ctx_b + 8] = 64                     # mirrors block size

    elif sub_mode == 1:
        if mem.B[driver_base + 0x55] == 0:        # port-1 alt marker
            return 1
        # Extended setup: parameters supplied via driver_base+$58..+$64
        # by an upstream caller; data stream base is $24000000 (A-bus CS1).
        mem.L[driver_base + 0x38] = mem.L[driver_base + 0x58]
        mem.L[driver_base + 0x3C] = mem.L[driver_base + 0x5C]
        mem.L[driver_base + 0x44] = mem.L[driver_base + 0x60]
        mem.L[driver_base + 0x34] = mem.L[driver_base + 0x64]
        mem.L[driver_base + 0x30] = 0x24000000
        mem.L[driver_base + 0x40] = mem.L[driver_base + 0x3C] * 2
        mem.L[driver_base + 0x4C] = (mem.L[driver_base + 0x30]
                                       + mem.L[driver_base + 0x40])
        mem.L[ctx_b + 0] = mem.L[driver_base + 0x38] * mem.L[driver_base + 0x3C]
        mem.L[ctx_b + 4] = mem.L[driver_base + 0x38]
        mem.L[ctx_b + 8] = mem.L[driver_base + 0x3C]
    else:
        return 1                                   # default fail

    # Wait for SMPC SR ready via sub +$09EA (busy-wait poll).
    if sub_09EA() != 0:
        return 2                                   # retry / busy

    # Call the main poll sub: BSRF disp $1576 -> driver +$1D30
    # (counts active peripherals in the data-stream bitmap).
    file_count = sub_1D30()

    # Main collection loop. A cursor walks the data stream while a
    # peripheral counter runs from driver_base+$44 to driver_base+$38;
    # both advance in lockstep (cursor += stride, periph += 1).
    stride = mem.L[driver_base + 0x40]
    cursor = mem.L[driver_base + 0x30] + 2 * stride
    periph = mem.L[driver_base + 0x44]
    while periph < mem.L[driver_base + 0x38]:
        if mem.B[cursor + 1] & 0x80:              # status bit 7 = "active"
            # Bitmap check: bit (periph & 7) of byte at
            # mem.L[driver_base+$4C] + (periph >> 3) * 2 + 1
            bitmap_byte = mem.B[mem.L[driver_base + 0x4C]
                                 + ((periph >> 3) << 1) + 1]
            if not (bitmap_byte & bitmask[periph & 7]):
                # Active + present: walk this peripheral's data block,
                # copying its data bytes and appending an entry pointer
                # (stream + id*stride) into the slot-11 work buffer.
                collect_periph_block(cursor, periph)
        cursor += stride
        periph += 1

    # After the loop the size fields in ctx_b are filled: ctx_b+16 from
    # (stack+36 temp - matched count - file_count), ctx_b+12 derived
    # from it and the block size, and ctx_b+20 from two BSRF-disp +$3610
    # calls (32/32 unsigned division, pool +$0A6C / +$0A70).
    sub_3610(...)
    sub_3610(...)
    return 0
```

The branch target at +$0888 inspects R0 (= port selector) and
dispatches three ways: `R0 == 0` → BRA +$0752 (reads
driver_base+$54); `R0 == 1` → BT +$07DE (reads driver_base+$55);
any other value → BRA +$0884 (default fail).

Pool values used by the non-port-2 path and the port-2 dispatch:
- `+$0762`: `$0101`
- `+$0764`: `$0106`
- `+$07EE`: `$0200` (peripheral-type constant)
- `+$07F0`: `$0080`
- `+$07F2`: `$01FE`
- `+$07F4`: `$00008000`
- `+$07F8`: `$20180000` (internal backup RAM base, cache-through alias of `$00180000`)
- `+$07FC`: `$20180080` (internal backup RAM +$80 — first directory entry; see backup_library.md)
- `+$0800`: `$00001576` (BSRF offset to driver `+$1576` poll sub)
- `+$08E0`: `$24000000` (A-bus CS1 base; written to driver_base+$30
              in the R0==1 sub-branch at +$0836. The R0==0 sub-branch
              at +$0790 instead writes `$20180000` from pool +$07F8.
              The two stream bases are mutually exclusive paths.)
- `+$08E4`: `$06000354`
- `+$09B4`: `$06000354`
- `+$0A6C`: `$00002C48` (BSRF target = driver `+$3610`)
- `+$0A70`: `$00002C40` (BSRF target = driver `+$3610`, same as +$0A6C)

The main collection loop (`+$0898`-`+$0988`) is the body sketched in
the pseudocode above. Its inner per-peripheral walk (`+$08E8`-`+$0986`)
reads the entry's data bytes from the data stream (`driver_base+$30`,
not from SMPC OREGs), tests the presence bitmask `sub_0388` wrote at
`stack+40` (indexed by `peripheral_index & 7`), and appends entry
pointers (`stream + id*stride`) into the slot-11 work buffer.

**External references made by slot 3**:

| Source | Target | Purpose |
|--------|--------|---------|
| BSRF `$1FB6` | driver `+$266E` | Port-2 SMPC-style read sub |
| BSRF `$1EE8` | driver `+$25C8` | Secondary peripheral query |
| BSR `+$0388` | sub_388 | Data-prep |
| BSR `+$09EA` | sub_9EA | "Wait SMPC SR ready" |
| BSRF `$1576` | driver `+$1D30` | Main poll routine |
| BSRF pool `+$0A6C` (`$2C48`) | driver `+$3610` | Packet-validation sub |
| BSRF pool `+$0A70` (`$2C40`) | driver `+$3610` | Same target, called consecutively |

**Key hardware accesses**:
- `$20180000` (cache-through alias of `$00180000`, internal backup
  RAM region) — written to driver_base+$30 as the data stream base
  in the port-2 setup block and shared with the BUP slots
- `$20180080` (internal backup RAM +$80 — first directory entry,
  per backup_library.md)
- `$24000000` (A-bus CS1 base) — alternate stream base written by
  the port-1 path (pool +$08E0)

No SMPC OREG addresses (`$20100000-$2010007F`) appear anywhere in
the 16 KB driver image. Slot 3 does not itself issue an SMPC
INTBACK command; it reads from a caller-prepared data stream
whose base address is one of the above pool values. Whoever
populated those bytes — game-side INTBACK handler, DMA, or
backup-cart firmware — does so before slot 3 is called.

**Behavior summary**: slot 3 is the main peripheral-data fetch
function. For port 2, it calls `+$266E` and `+$25C8` (specialized
poll subs) and translates their return codes. For ports other
than 2, it sets up driver state by writing data-stream addresses
and stride values to driver_base+$30/$34/$38/$3C/$40/$44/$4C,
waits for a ready signal via `+$09EA`, calls the main poll sub
`+$1D30`, and walks the data stream into the per-port output
buffer slot 11 points to (driver_base+$78+).

#### Slot 4 (`+$0A78`) — Peripheral packet build / record commit

- **Body**: `+$0A78` to `+$0EB0` (1082 bytes incl. RTS delay slot)
- **Inputs**: R4 = port selector (2 = port-2 fast path; else
  multi-step path); R5 = stack-frame pointer (saved to R13);
  R6 = additional context (saved to stack[24]); R7 = byte arg
  (saved to mem.B[stack+0])
- **Returns** (R0): `0` = success; `1` = no peripheral / short
  path; `2` = sub-error class; `3` = `$23` sub return;
  `4` = `$31` sub return; `6` = port-2 early-fail path (also
  BUP_FOUND in the non-port-2 path: entry matched and `owsw_byte`
  nonzero); `-1` = unknown / error
- **Calls**:
  - BSR `+$18B4` (port-2 sub) — note this is **slot 7's entry
    address** (slot 4 calls slot 7's body inline)
  - BSRF `$000027D4` → driver `+$32A6`
  - BSR `+$0672` (slot 3 — called TWICE)
  - BSR `+$1434`, `+$17BA`, `+$1004`, `+$1BB4`, `+$0EBC`
  - BSRF `$00002A8C` → driver `+$3610`
  - JSR @R2-from-`$06000340` (BIOS function pointer slot — called
    twice; R4 = -1 for mask-all, then R4 = saved IMS for restore)
  - BSRF `$11CE` (pool +$0EB4) → driver `+$2010` (busy-wait)

```python
def slot_4_packet_build(port_sel, frame_ptr, ctx, owsw_byte):  # R4, R5, R6, R7
    # Port-2 fast path: route via slot 7's body (inline call) for the
    # actual cart-protocol pass, then +$32A6 for finalization.
    if port_sel == 2:
        if mem.B[driver_base + 0x56] == 0:        # port-2 marker
            return 1                               # no peripheral

        if owsw_byte != 0:
            # Call slot 7's body inline (BSR +$18B4) to build the
            # packet body. Returns nonzero on cart-side failure.
            rc = slot_7_body(...)
            if rc != 0:
                return 6                           # port-2 early-fail

        # Final cart-protocol commit via +$32A6 (BSRF disp $27D4).
        rc = sub_32A6(frame_ptr, ctx)              # R4 = frame_ptr, R5 = ctx
        # Translate return code:
        #   0 -> 0; $21 -> 1; $23 -> 3; $24 -> 2; $31 -> 4;
        #   $0101/$0106 -> 1; else -> -1
        return translate_cart_status(rc)

    # Non-port-2 path (R4 != 2).
    # Step 1: validate device + load directory state via slot 3.
    rc = slot_3(port_sel, 0, stack + 76)
    if rc != 0:
        return rc

    # Step 2: per-entry validate via +$1434 (mode 0).
    matched = validate_1434(frame_ptr, mode=0)
    if matched != 0:
        if owsw_byte != 0:
            return 6                               # BUP_FOUND (don't overwrite)
        sub_17BA(matched)                          # erase existing entry
        rc = slot_3(port_sel, 0, stack + 76)       # re-scan after erase
        if rc != 0:
            return rc

    # Step 3: block-count math via shared +$3610 (BSRF disp $2A8C,
    # signed division on mem.L[driver_base+0x3C] and mem.L[frame_ptr+28]).
    block_count = sub_3610(...) + 1                # stored as the loop bound
    stack[0]    = block_count

    if block_count > mem.L[stack + 92]:
        return 4                                   # $31-class error

    # Step 4: post-fetch sort + dir-entry commit via +$1004, then read
    # the packet-header word from the slot-11 buffer.
    sub_1004(block_count)
    packet_header = mem.W[mem.L[driver_base + 0x2C]]   # first slot-11 word

    # Step 5: pre-loop init. stack[12] = 1; loop/phase counters reset
    # (stack[16] = stack[20] = 0, phase byte stack[32] = 0); encoding
    # cursors R11/R8/R9/R10 staged at stack+4..+7.

    # Step 6: mode-1 IMS save (driver_base+$34 == 1).
    mode = mem.L[driver_base + 0x34]
    if mode == 1:
        saved_ims = mem.L[0x06000348]              # IMS shadow
        bios_ims_call(-1)                          # mask all interrupts

    # Step 7: per-entry record-build loop. Each iteration resets the
    # output cursor (R4 = 0) and writes one entry into the data buffer:
    #   out = mem.L[driver_base+0x30]
    #       + mem.W[mem.L[driver_base+0x2C] + iter*2] * mem.L[driver_base+0x40]
    # i.e. the block number from the slot-11 list times the block size.
    # The encoding phase in stack[32] is a state machine that advances
    # 0 -> 1 -> 2: phase 0 falls through into phase-1 code and phase 1
    # into phase-2 code within the same iteration, so the dispatch only
    # resumes wherever the previous iteration left off.
    #   Phase 0 (+$0C0C): write the record header - 4 marker bytes from
    #     out[1/3/5/7] (the first OR'd with $80) then fixed fields copied
    #     from frame_ptr ([0..10], [23], [12..21], [24..27], [28..31]);
    #     set phase = 1.
    #   Phase 1 (+$0CFE): copy block-list word-pairs from the slot-11
    #     buffer (driver_base+0x2C) indexed by stack[12]; set phase = 2
    #     once R4 reaches the block size (driver_base+0x40).
    #   Phase 2 (+$0D9A): copy the data payload from ctx indexed by
    #     stack[20], up to mem.L[frame_ptr+28] bytes, zero-padding the
    #     remainder up to the block size.
    # After each entry, if driver_base+0x34 == 1 the busy-wait callback
    # +$2010 (BSRF disp $11CE) runs. The loop ends when iter reaches
    # stack[0].
    iter = 0
    while iter < stack[0]:                          # stack[0] = block_count
        build_entry(iter)                           # phases 0->1->2 per above
        if mem.L[driver_base + 0x34] == 1:
            sub_2010()                              # busy-wait callback
        iter += 1

    # Step 8: mode-1 IMS restore.
    if mode == 1:
        bios_ims_call(saved_ims)                   # restore IMS shadow

    # Step 9: transform/commit via +$1BB4 (BUP_Verify-style compare).
    R13 = sub_1BB4(packet_header, ctx)

    # Step 10: optional close-out via +$0EBC if driver_base+$48 is set.
    if mem.L[driver_base + 0x48] != 0:
        rc = sub_0EBC(mem.L[driver_base + 0x48] & 0xFFFF)
        if rc != 0:
            R13 = rc                               # override return

    # Step 11: cleanup active-bit flags via +$17BA if needed.
    if packet_header != 0:
        sub_17BA(packet_header)

    return R13
```

Pool constants in slot 4:
- `+$0AF0`: `$06000354`
- `+$0AF4`: `$000027D4` (BSRF to driver +$32A6)
- `+$0B72`: `$0101`
- `+$0B74`: `$0106`
- `+$0C00`: `$00002A8C` (BSRF to driver +$3610)
- `+$0C04`: `$06000348` (IMS shadow address)
- `+$0C08`: `$06000340` (BIOS WRAM-H function ptr slot)
- `+$0EB4`: `$000011CE` (BSRF at +$0E3E → driver +$2010, the
  busy-wait callback)
- `+$0EB8`: `$06000340` (BIOS WRAM-H function-pointer slot)

**External references made by slot 4**:

| Source | Target | Purpose |
|--------|--------|---------|
| BSR `+$18B4` | **slot 7 entry** | Port-2 inline call to slot 7 |
| BSRF `$27D4` | driver `+$32A6` | Sub (peripheral query) |
| BSR `+$0672` | **slot 3 entry** (×2) | Peripheral / cart data fetch |
| BSR `+$1434`, `+$17BA`, `+$1004` | various subs | Validate / cleanup / post-fetch |
| BSR `+$1BB4` | driver `+$1BB4` | Transform sub |
| BSR `+$0EBC` | driver `+$0EBC` | Close-out (immediately after slot 4 body) |
| BSRF `$2A8C` | driver `+$3610` | Validation sub |
| JSR `*$06000340` | BIOS function | Called twice — first with R4 = -1 (mask all interrupts), second with R4 = saved IMS shadow (restore); `$06000348` is loaded only as the saved-IMS value, not as a function pointer |
| BSRF `$11CE` (pool +$0EB4) | driver `+$2010` | Busy-wait callback |

**Behavior summary**: slot 4 is a high-level "build and commit a
peripheral data record" function. For port 2 it routes via a
specialized sub (slot 7's body) and the +$32A6 sub. For other
ports it calls slot 3 twice (re-poll), then runs a per-entry
record-build loop with three sequential encoding phases (header,
block list, data payload) selected by the state byte at stack[32].
Each entry is written to the data buffer at driver_base+$30,
addressed by the block number from the slot-11 list times the
block size at driver_base+$40. Mode 1 (driver_base+$34 == 1) gates
IMS-shadow save/restore around the loop and runs the +$2010
busy-wait callback after each entry.

#### Slot 5 (`+$135A`) — Lightweight peripheral query

- **Body**: `+$135A` to `+$1432` (218 bytes — much smaller than
  slots 2/3/4; only saves PR + a 32-byte frame)
- **Inputs**: R4 = port selector (must be 2 for fast path; else
  alternate path); R5 = saved to stack[4]; R6 = saved to stack[0]
- **Returns** (R0): `0` = success; `1` = no peripheral / OK
  alternate; `2` = `$24` sub return; `3` = `$23` sub return;
  `5` = `$30` (port-2 specific success) / `validate_1434` returned 0
  (no existing entry) in non-port-2 path; `-1` = unknown error
- **Calls**:
  - BSRF `$00001C48` → driver `+$2FD0`
  - BSR `+$0672` (slot 3)
  - BSR `+$1434` (sub immediately after slot 5 body)
  - BSR `+$157C` (sub later in driver)

Early-return paths (each is "fix R15, pop PR, RTS, MOV-imm-to-R0
in delay slot"):

```
+$138C-$1392: return 0
+$139C-$13A2: return 5
+$13A4-$13AA: return 1
+$13AC-$13B2: return 3
+$13B4-$13BA: return 2
+$13BC-$13C2: return 1   (duplicate of $13A4 — used for $0101/$0106 match)
+$13C4-$13CA: return -1
```

Pool values:
- `+$1394`: `$06000354`
- `+$1398`: `$00001C48` (BSRF offset)
- `+$1426`: `$0101`
- `+$1428`: `$0106`

```python
def slot_5_query(port_sel, ctx_a, ctx_b):        # R4, R5, R6
    # Port-2 fast path
    if port_sel == 2:
        if mem.B[driver_base + 0x56] == 0:
            return 1                               # no peripheral
        rc = sub_2FD0(ctx_a, ctx_b, 0)           # BSRF disp $1C48; R4/R5/R6
        # Translate whitelist of cart-status codes:
        #   $0 -> 0;  $30 -> 5;  $21 -> 1;  $23 -> 3;  $24 -> 2;
        #   $0101 -> 1;  $0106 -> 1;  else -> -1
        return translate_slot5_status(rc)

    # Non-port-2 path
    rc = slot_3(port_sel, 0, stack + 8)
    if rc != 0:
        return rc
    matched = validate_1434(ctx_a, mode=0)
    if matched == 0:
        return 5                                   # no change (validate returns 0)
    return sub_157C(matched, ctx_b)              # R4 = matched, R5 = ctx_b; propagate its R0
```

**External references made by slot 5**:

| Source | Target | Purpose |
|--------|--------|---------|
| BSRF `$1C48` | driver `+$2FD0` | Port-2 specialized query (same target as slot 8) |
| BSR `+$0672` | **slot 3** | Peripheral / cart data fetch |
| BSR `+$1434` | sub at `+$1434` (after slot 5 body) | Validate (also called by slot 4) |
| BSR `+$157C` | sub at `+$157C` | Process / transform |

**Behavior summary**: slot 5 is a thin query wrapper. For port 2
it bottoms out at the `+$2FD0` sub and translates a small
whitelist of return codes ($0/$21/$23/$24/$30/$0101/$0106) to
result codes 0-5 + -1. For other ports it calls slot 3, then
`+$1434` (validate); if validate returns 0 (no change) it returns
5; if validate returns non-zero it calls `+$157C` (commit) and
propagates that sub's R0.

The downstream helpers (`+$2FD0`, `+$1434`, `+$157C`) are also
referenced from BUP code paths; see backup_library.md.

#### Slot 6 (`+$16DC`) — Lightweight peripheral query (variant)

- **Body**: `+$16DC` to `+$17B8` (220 bytes)
- **Inputs**: R4 = port selector (2 = fast path); R5 = saved
  to stack[0]
- **Returns** (R0): `0`, `1`, `2`, `3`, `5`, `-1` — same scheme
  as slot 5 with slightly different distribution
- **Calls**:
  - BSRF `$00001098` → driver `+$279C`
  - BSRF `$FFFFEEFC` (= signed -$1104) → **slot 3 entry** (`+$0672`)
  - BSR `+$1434` (validate sub)
  - BSR `+$17BA` (matched-entry repack sub, immediately after slot 6 body)

Pool:
- `+$1710`: `$06000354`
- `+$1714`: `$00001098` (BSRF to driver `+$279C`)
- `+$17A8`: `$0101`
- `+$17AA`: `$0106`
- `+$17AC`: `$FFFFEEFC` (BSRF to slot 3 — relative)

Dispatch at +$1748: same as slot 5 (CMP against $0, $21, $23,
$24, $30, $0101, $0106 — branches to corresponding return path).
Return paths at +$1708/$1718/$1720/$1728/$1730/$1738/$1740 each
do `ADD #28,R15; LDS.L @R15+,PR; RTS; MOV #imm,R0`.

```python
def slot_6_query(port_sel, ctx):                 # R4, R5
    # Port-2 fast path
    if port_sel == 2:
        if mem.B[driver_base + 0x56] == 0:
            return 1                               # no peripheral
        rc = sub_279C(ctx)                        # BSRF disp $1098 (slot 6's variant)
        # Same whitelist as slot 5:
        #   $0 -> 0;  $30 -> 5;  $21 -> 1;  $23 -> 3;  $24 -> 2;
        #   $0101 -> 1;  $0106 -> 1;  else -> -1
        return translate_slot5_status(rc)

    # Non-port-2 path
    rc = slot_3(port_sel, 0, stack + 4)
    if rc != 0:
        return rc
    matched = validate_1434(ctx, mode=0)
    if matched == 0:
        return 5                                   # no change
    sub_17BA(matched)                              # repack matched entry (strip status bit 7, notify)
    return 0
```

**External references made by slot 6**:

| Source | Target | Purpose |
|--------|--------|---------|
| BSRF `$1098` | driver `+$279C` | Port-2 specialized query (different from slot 5/8's `+$2FD0`) |
| BSRF `$FFFFEEFC` (relative) | **slot 3 entry** | Peripheral / cart data fetch |
| BSR `+$1434` | sub at `+$1434` | Validate (also called by slots 4 + 5) |
| BSR `+$17BA` | sub at `+$17BA` (right after slot 6) | Repack/normalize matched entry; fire notify callbacks |

**Behavior summary**: slot 6 is structurally identical to slot 5
but routes through different specialized subs. Port 2 goes
through `+$279C` (slot 6's variant) instead of `+$2FD0` (slot 5).
Both translate the same `$21/$23/$24/$30/$0101/$0106` return-code
whitelist to result codes 1-5 + -1. Non-port-2 path calls slot 3,
validates via `+$1434`, optionally calls `+$17BA`, returns 5 on
success or 0 if cleanup ran.

As with slot 5, the helpers `+$279C` / `+$1434` / `+$17BA` are
also referenced from BUP code paths; see backup_library.md.

#### Slot 7 (`+$18B4`)

Port-2 BUP packet build (BUP_Dir-orchestrator). Documented in [backup_library.md](backup_library.md).

#### Slot 8 (`+$1ACC`) — Lightweight peripheral query (variant)

- **Body**: `+$1ACC` to `+$1BA6` (220 bytes — structurally same
  as slots 5 and 6)
- **Inputs**: R4 = port selector (2 = fast path); R5 = saved to
  stack[4]; R6 = saved to stack[0]
- **Returns** (R0): `-1`, `0`, `1`, `2`, `3`, `5`, `7`, `8`. The
  port-2 dispatch produces 0/1/2/3/7/-1 (slot 8 adds `7` via
  `$0109`). The non-port-2 path can also return 5 (validate
  found no change) or propagate `+$1BB4`'s return (0, 7, or 8).
- **Calls**:
  - BSRF `$000014D6` → driver `+$2FD0`
  - BSRF `$FFFFEB02` (signed -$14FE) → **slot 3 entry**
  - BSR `+$1434` (validate)
  - BSR `+$1BB4` (sub immediately after slot 8 body — also
    called by slot 4)

Early-return paths at +$1AFE/$1B06/$1B0E/$1B16/$1B1E/$1B26/$1B2E
each do `ADD #32,R15; LDS.L @R15+,PR; RTS; MOV #imm,R0`.
Return codes by path: 0, 7, 1, 3, 2, 1, -1.

Pool:
- `+$1B38`: `$06000354`
- `+$1B3C`: `$000014D6` (BSRF to driver `+$2FD0`)
- `+$1BA8`: `$0101`
- `+$1BAA`: `$0106`
- `+$1BAC`: `$0109` (new return-code key)
- `+$1BB0`: `$FFFFEB02` (BSRF to slot 3 — relative)

Dispatch at +$1B40: CMP against $0/$21/$23/$24, then pool words
$0101/$0106/$0109, branching to corresponding early-return path.
Slot 8's distinguishing key is `$0109 → return 7`.

```python
def slot_8_query(port_sel, ctx_a, ctx_b):        # R4, R5, R6
    # Port-2 fast path — same +$2FD0 target as slot 5
    if port_sel == 2:
        if mem.B[driver_base + 0x56] == 0:
            return 1                               # no peripheral
        rc = sub_2FD0(ctx_a, ctx_b, 1)           # BSRF disp $14D6; R4/R5/R6=1 (slot 5 passes R6=0)
        # Slot 8's distinguishing whitelist (adds $0109 -> 7):
        #   $0 -> 0;  $21 -> 1;  $23 -> 3;  $24 -> 2;
        #   $0101 -> 1;  $0106 -> 1;  $0109 -> 7;  else -> -1
        return translate_slot8_status(rc)

    # Non-port-2 path
    rc = slot_3(port_sel, 0, stack + 8)
    if rc != 0:
        return rc
    matched = validate_1434(ctx_a, mode=0)
    if matched == 0:
        return 5                                   # no change
    return sub_1BB4(matched, ctx_b)              # R4 = matched, R5 = ctx_b; commit, returns 0 / 7 / 8
```

**External references made by slot 8**:

| Source | Target | Purpose |
|--------|--------|---------|
| BSRF `$14D6` | driver `+$2FD0` | Port-2 specialized query (same sub as slot 5) |
| BSRF `$FFFFEB02` (relative) | **slot 3 entry** | Peripheral / cart data fetch |
| BSR `+$1434` | sub at `+$1434` | Validate (also called by slots 4, 5, 6) |
| BSR `+$1BB4` | sub at `+$1BB4` (right after slot 8) | Transform/commit |

**Behavior summary**: slot 8 is another thin wrapper variant
matching slots 5 and 6's pattern. Distinguishing features:
- Routes port-2 calls through `+$2FD0` (same target as slot 5;
  slot 6 routes through `+$279C` instead) — two sub-routines
  shared across three slot variants
- Adds `$0109 → return 7` to the dispatch whitelist
- Non-port-2 path calls slot 3 + `+$1434` (validate); if validate
  returns 0, returns 5; if non-zero, calls `+$1BB4` (commit, the
  same sub slot 4 calls) and propagates its R0 (0, 7, or 8)

As with slots 5/6, the helpers `+$2FD0` / `+$1434` / `+$1BB4`
are also referenced from BUP code paths; see backup_library.md.

#### Slot 9 (`+$1E70`) - BUP_GetDate

Documented in [backup_library.md](backup_library.md) under
`### Slot 9 (+$1E70) - BUP_GetDate`. The slot decodes a packed
Uint32 (minutes since 1980-01-01 00:00) into a BupDate struct.

#### Slot 10 (`+$1F64`) - BUP_SetDate

Documented in [backup_library.md](backup_library.md) under
`### Slot 10 (+$1F64) - BUP_SetDate`. The slot encodes a
BupDate struct into a packed Uint32 (minutes since 1980-01-01).

### Sub-routine at `+$2010` (slot 2's R9 callback)

Immediately after slot 10's body, the driver continues at
`+$2010` with the busy-wait delay loop that slot 2 invokes via
its R9-callback (`R9 = $0607E010` computed in slot 2's prologue):

Loop body is 4 instructions (SUB / NOP / CMP/EQ / BF). With
BF-taken costing 3 cycles, each iteration is 6 cycles, so
47,000 × 6 ≈ 282,000 cycles ≈ 9.9 ms at 28.6 MHz. Used by
slot 2 between SMPC accesses.

### Driver sub-routines (between and after slot bodies)

The slot bodies call ~24 sub-routines for hardware access,
data validation, and per-byte processing. These subs live in the
buffer gaps between slot bodies and in the second 8 KB of the
driver (`+$2028` - `+$3FFF`).

#### Sub `+$0388` — Write bit-position mask table

- **Body**: `+$0388` to `+$03A8` (34 bytes incl. RTS delay slot)
- **Inputs**: R4 = destination buffer pointer (≥ 8 bytes)
- **Side effect**: writes 8 bytes
  `{$80, $40, $20, $10, $08, $04, $02, $01}` to
  `mem.B[R4..R4+7]`
- **No frame**, no PR push; RTS at +$03A6 + delay slot at +$03A8

This is the standard "bit position N → byte mask" lookup table
used by callers to convert a bit index 0-7 into the corresponding
single-bit mask value, for use in bit-by-bit packing/unpacking of
peripheral data records.

Pool used:
- `+$0408`: `$0080` (loaded as word into R3, low byte = $80 →
  first table entry)

Callers: slot 2 (BSR `+$0388` at +$042C), slot 3 (BSR `+$0388`
at +$074A).

#### Sub `+$1434` — Compare peripheral data, find changed peripheral

- **Body**: `+$1434` to `+$1576` (324 bytes incl. RTS delay slot)
- **Called by**: slots 4, 5, 6, 8 (mode selector R5 = 0, 1, or 2)
- **Inputs**:
  - R4 = caller's input buffer pointer (peripheral data to compare)
  - R5 = mode selector (byte: 0, 1, or 2)
- **Returns** (R0): `0` = success / no change; word (`EXTU.W R5`) =
  peripheral index on early-out
- **Calls**: BSRF `$FFFFEF32` → sub `+$0388` (bitmask table write)
- **Side effects**: writes to `mem.L[driver_base+$50]` on
  change detection

Pool:
- `+$14A8`: `$06000354`
- `+$14AC`: `$FFFFEF32` (BSRF offset to sub `+$0388`)
- `+$1578`: `$06000354`

**Purpose**: scans peripheral data (currently at the stream base
mem.L[driver_base+$30]) byte-by-byte against caller's input buffer
(R4). For each peripheral whose first byte has bit 7 set
(indicates "data present"), iterates through 12 bytes of data and
returns the peripheral index whose data first differs from the
caller's input (or 0 if no change). That peripheral index is
also written to `mem.L[driver_base+$50]` for later use.

Three modes (all compute `periph_ptr = mem.L[driver_base+$30] +
cur * mem.L[driver_base+$40]` via runtime `MUL.L`):
- **Mode 0** (R5 = 0): resets the peripheral counter
  `mem.L[driver_base+$50]` to `mem.L[driver_base+$44]`; a NUL
  caller byte at a mismatch does NOT stop the scan
- **Mode 1** (R5 = 1): same counter reset as mode 0, but a NUL
  caller byte at a mismatch returns the current peripheral index
- **Mode 2** (R5 = 2): pre-increments `mem.L[driver_base+$50]`
  (continues from the prior position instead of resetting);
  NUL-stop behaves as mode 1

**Behavior summary**: `+$1434` is the peripheral-change-detection
routine. It iterates through up to `driver_base+$38` peripherals
and for each, checks if any of its 12 data bytes changed since
last frame. Returns 0 on no-change, or a word value identifying
which peripheral changed first.

```python
def sub_1434(caller_buf, mode):                  # R4 = buf, R5 = mode (0/1/2)
    bitmask = [0x80 >> i for i in range(8)]      # sub_0388 at stack+4

    # Mode dispatch sets up the per-peripheral counter at
    # driver_base+$50.
    if mode == 2:
        mem.L[driver_base + 0x50] += 1            # bump counter
    else:                                          # mode 0 or 1: reset
        mem.L[driver_base + 0x50] = mem.L[driver_base + 0x44]

    # Walk peripheral records starting at the counter.
    cur        = mem.L[driver_base + 0x50]
    stride     = mem.L[driver_base + 0x40]
    periph_ptr = mem.L[driver_base + 0x30] + cur * stride
    zero_stops = (mode != 0)                     # mode 1/2: NUL caller byte stops as a change

    while cur < mem.L[driver_base + 0x38]:
        status = mem.B[periph_ptr + 1]           # bit 7 = "data present"
        if status & 0x80:
            # Per-peripheral bitmap: bit (cur & 7) of the byte at
            # driver_base[+$4C] + (cur >> 3) * 2 + 1
            bitmap_byte = mem.B[mem.L[driver_base + 0x4C]
                                + ((cur >> 3) << 1) + 1]
            if not (bitmap_byte & bitmask[cur & 7]):  # compare only when bit is clear
                # Compare 12 bytes of peripheral data against caller's buf
                for i in range(12):
                    ram_byte    = mem.B[periph_ptr + 8 + i*2 + 1]
                    caller_byte = mem.B[caller_buf + i]
                    if ram_byte == caller_byte:
                        if i == 11 or caller_byte == 0:
                            mem.L[driver_base + 0x50] = cur
                            return cur & 0xFFFF   # match (no change up to byte i)
                    else:
                        # Mismatch — a NUL caller byte in mode 1/2, or
                        # reaching byte 11, returns the peripheral index.
                        if (caller_byte == 0 and zero_stops) or i == 11:
                            mem.L[driver_base + 0x50] = cur
                            return cur & 0xFFFF
                        break                     # real change — try next peripheral

        periph_ptr += stride
        cur        += 1

    return 0                                      # full scan, no change
```

#### Sub `+$17BA` — Acknowledge peripheral data (clear bit-7 + delay)

- **Body**: `+$17BA` to `+$18B2` (250 bytes)
- **Called by**: slot 4 (twice — at +$0B54 and +$0E96) and
  slot 6 (at +$1798). Not called by slot 8.
- **Inputs**: R4 = peripheral index (word — used to compute
  SMPC offset)
- **Returns** (R0): no explicit return value (R0 carries
  whatever BIOS-function call left)
- **Calls**:
  - BSRF `$00000776` → driver `+$2010` (~10 ms busy-wait delay)
  - JSR `*$06000340` (BIOS function pointer) — twice (once on
    mode-1 entry to save IMS; once after delay)

Read+mask phase: read 4 SMPC bytes at offsets +1, +3, +5, +7
from R13; mask the first with $7F (clear bit 7); store to stack[0..3]
and save cursor pointers to stack[4]/[8]/[16]:

Write-back loop at +$183E (write the bit-7-cleared bytes back
to the SMPC region):

Pool:
- `+$1874`: `$06000354`
- `+$1878`: `$06000348` (IMS shadow)
- `+$187C`: `$06000340` (BIOS function pointer slot)
- `+$1920`: `$00000776` (BSRF to driver +$2010)
- `+$1924`: `$06000340`

**Behavior summary**: `+$17BA` is the "acknowledge peripheral
data" routine. For a specific peripheral indexed by caller's R4:
1. Read 4 status bytes from SMPC region (offsets +1, +3, +5, +7)
2. Mask off bit 7 of the first byte (clearing the "data present" flag)
3. If mode-1 active: save SCU IMS shadow + call BIOS function
   pointed by `$06000340` (typically the IMS-set-and-fetch
   routine — this masks interrupts)
4. Write the bit-7-cleared bytes back to the SMPC region
5. Zero-fill the remaining bytes of the peripheral's data block
6. Issue a ~10 ms busy-wait (`+$2010`)
7. Call BIOS function via `$06000340` (likely IMS restore)
8. Return

This is the "ack/clear cycle" that completes after each
peripheral-data read — clearing the "new data" bits so the next
read can detect fresh data.

```python
def sub_17BA(periph_idx):                        # R4 = peripheral index (word)
    # Compute peripheral's address in the data stream from
    # index * stride + base.
    periph_addr = (mem.L[driver_base + 0x30]
                   + periph_idx * mem.L[driver_base + 0x40])

    # Read the 4 status bytes at odd offsets +1/+3/+5/+7 into a stack
    # scratch array, clearing bit 7 of the first byte (deactivate
    # "data present" flag).
    scratch = [mem.B[periph_addr + 1] & 0x7F,
               mem.B[periph_addr + 3],
               mem.B[periph_addr + 5],
               mem.B[periph_addr + 7]]

    # Any nonzero mode bumps scratch[0] by +1 (with bit-7 mask).
    # Mode 1 additionally saves the SCU IMS shadow and masks interrupts.
    mode = mem.L[driver_base + 0x34]
    if mode != 0:
        scratch[0] = (scratch[0] + 1) & 0x7F
    if mode == 1:
        saved_ims = mem.L[0x06000348]             # IMS shadow
        bios_ims_call(-1)                          # JSR *$06000340, mask all

    # Write the bit-7-cleared scratch bytes back to the peripheral
    # record's odd-byte slots.
    for i, byte in enumerate(scratch):
        mem.B[periph_addr + 1 + i*2] = byte

    if mode == 1:
        # Zero-fill remaining bytes of the peripheral's stride (odd-
        # byte slots only).
        for off in range(9, mem.L[driver_base + 0x40], 2):
            mem.B[periph_addr + off] = 0

        # ~10 ms busy-wait callback to let the peripheral settle
        # before IMS-restore.
        sub_2010_delay()                          # BSRF disp $776 -> +$2010

        # Restore SCU IMS via the same BIOS function.
        bios_ims_call(saved_ims)
```

#### Sub `+$1BB4` — Peripheral data transform / commit

- **Body**: `+$1BB4` to `+$1D2E` (380 bytes incl. RTS delay slot)
- **Called by**: slot 4 (BSR at +$0E68), slot 8 (BSR at +$1B92)
- **Inputs**: R4 = word event code (written to slot 11 buffer +0);
  R5 = caller reference buffer (compared in the mode-2 pass)
- **Returns** (R0): `0` = success / no-change; `7` = byte-mismatch
  (offending peripheral index stored in `mem.L[driver_base+$48]`);
  `8` = out-of-range error
- **No PR push, no internal BSR/JSR** — leaf function

This sub is **structurally near-identical to `+$157C`** (slot 5's
commit sub). Same prologue (pushes R8-R13 + MACL, no R14/PR),
12-byte frame, same outer-loop iterating through slot 11
buffer's per-peripheral entries up to `driver_base+$38` count.

Key differences from `+$157C`:
- `+$1BB4` writes the caller's R4 word to slot 11 buffer +0
  via `MOV.W R4,@R3` (vs `+$157C` writes via `MOV.W R4,@R2`)
- `+$1BB4` writes the offending peripheral index (`word`) into
  `mem.L[driver_base+$48]` (offset $48 — error/event slot)
  at +$1CDE on a mode-2 mismatch
- Mode-2 (R9=2) branch at +$1CAC compares SMPC byte against
  caller-buffer byte and on mismatch sets R0 = 7 + writes
  peripheral index to driver_base+$48

Per-mode behavior:
- **Mode 0 (R9=0)**: identical to `+$157C` mode 0 (copy 4 bytes
  from SMPC region at offsets +61, +63, +65, +67 to stack scratch)
- **Mode 1 (R9=1)**: identical to `+$157C` mode 1
- **Mode 2 (R9=2)**: per-byte compare loop — each SMPC byte at
  periph_addr+offset (offset from +9, step +2) vs the caller byte at
  ref_buf+cmp_idx; on mismatch write the peripheral index to
  `driver_base+$48` and return 7

**Behavior summary**: `+$1BB4` walks the slot 11 buffer's
per-peripheral entries doing one of three operations based on
phase counter (R9):
1. Initial pass: copy from SMPC region to stack scratch
2. Mid pass: copy SMPC region bytes into per-peripheral buffer slot
3. Verification pass: compare SMPC bytes against caller's
   reference buffer; on first mismatch, record peripheral
   index at `driver_base+$48` and return error code 7

Slot 4 uses this as the final "commit and verify" step after
building a peripheral packet — if SMPC data drifted during the
fetch, R0 = 7 alerts the caller to re-fetch.

```python
def sub_1BB4(event_code, ref_buf):               # R4 = event code, R5 = reference buf
    mem.L[driver_base + 0x48] = 0
    work_buf = mem.L[driver_base + 0x2C]
    mem.W[work_buf] = event_code                  # publish at slot-11 +0

    mode    = 0                                   # self-progressive: 0 -> 1 -> 2
    cur     = 0                                   # outer peripheral index
    cmp_idx = 0                                   # compare-buffer cursor

    while True:
        word = mem.W[work_buf + cur * 2]
        if word == 0:
            return 0                              # end of list

        if word > mem.L[driver_base + 0x38]:
            return 8                              # out-of-range

        periph_addr = (mem.L[driver_base + 0x30]
                       + word * mem.L[driver_base + 0x40])

        if mode == 0:
            # Initial pass: copy peripheral bytes at fixed offsets
            # +61, +63, +65, +67 into stack scratch.
            stage_periph_bytes_to_scratch(periph_addr)
            mode = 1
        elif mode == 1:
            # Mid pass: copy SMPC region bytes into the per-peripheral
            # buffer slot in the slot-11 working buffer.
            commit_scratch_to_work_buf(periph_addr)
            mode = 2
        else:                                     # mode == 2: verification
            off = 9                               # SMPC data starts at stride offset +9
            while off < mem.L[driver_base + 0x40]:
                ram_byte    = mem.B[periph_addr + off]
                caller_byte = mem.B[ref_buf + cmp_idx]
                cmp_idx += 1
                if ram_byte != caller_byte:
                    mem.L[driver_base + 0x48] = word
                    return 7                      # mismatch — re-fetch needed
                off += 2

        cur += 1
```

#### Sub `+$1D30` — Count active-peripheral bits

- **Body**: `+$1D30` to `+$1D94` (102 bytes)
- **Called by**: slot 3 (via BSRF pool offset `$1576`)
- **Inputs**: none beyond driver state
- **Returns** (R0): count of bits set across the peripheral-presence
  bitmap
- **Calls**: BSRF `$FFFFE642` (signed -$19BE) → sub `+$0388`
  (bitmask table)

Pool:
- `+$1D50`: `$06000354`
- `+$1D54`: `$FFFFE642` (BSRF offset to sub `+$0388`)

**Behavior**: walks the byte stream at `mem.L[driver_base+$4C]`
(set up by slot 3 to point at `$20180080+` — internal backup RAM
cache-through region). It iterates index `i` from
`mem.L[driver_base+$44]` (start) up to `mem.L[driver_base+$38]`
(limit, exclusive), and for each `i` checks bit `(i & 7)` of the
odd byte at stride `(i >> 3) * 2 + 1` — extracts a single bit. R0
returns the count of set bits. This produces the
**active-peripheral count** from a peripheral-presence bitmap.
The bitmap lives in the internal-backup-RAM region shared with
the BUP side of the driver; populated by whatever upstream
mechanism (game-side INTBACK handler, DMA, or backup-cart
firmware) prepares the data stream that slot 3 reads.

```python
def sub_1D30():
    bitmask = [0x80 >> i for i in range(8)]      # sub_0388 at stack

    count = 0
    bitmap_base = mem.L[driver_base + 0x4C]
    limit       = mem.L[driver_base + 0x38]      # total peripheral count

    for i in range(mem.L[driver_base + 0x44], limit):
        bitmap_byte = mem.B[bitmap_base + (i >> 3) * 2 + 1]   # odd-byte
        if bitmap_byte & bitmask[i & 7]:
            count += 1

    return count
```

#### Sub `+$36B8` — 32-bit signed remainder (modulo)

- **Body**: `+$36B8` to `+$3774` (190 bytes incl. last RTS delay
  slot). Normal-exit RTS at `+$3766`; div-by-0 exit RTS at
  `+$3772`. Pool literals at `+$3778` (`$06000350`) and `+$377C`
  (`$0000044E`).
- **Called by**: slot 10 (BSRF `$172A` at +$1F8A)
- **Inputs**: R0 = divisor, R1 = dividend
- **Returns**: R0 = signed remainder (dividend mod divisor,
  carrying the dividend's sign). On divide-by-zero (R0 == 0) it
  returns 0 and writes error code `$044E` to `$06000350`.

Variant of `+$355C`: shares the same dividend sign-extension and
32-step DIV1+ROTCL loop, but instead of finalizing the quotient
it returns the remainder. It saves the dividend's sign (`MOVT R4`
after `DIV0S R2,R1` with R2 = 0, so T = the dividend's MSB), then
after the loop sign-corrects the remainder held in R3 (a
conditional one-step `DIV0S`/`SHAR`/`DIV1` fix-up followed by
`ADD R4,R3`) so the remainder carries the dividend's sign, and
returns it via `MOV R3,R0`. Used by slot 10 in a different
context.

#### Sub `+$3610` — 32-bit unsigned division (decoder/math)

- **Body**: `+$3610` to `+$36AC` (158 bytes incl. last RTS delay
  slot). Normal-exit RTS at `+$369E` (delay slot `+$36A0`);
  div-by-0 exit RTS at `+$36AA`. On divide-by-zero (R0 == 0) it
  returns R0 = 0 and writes error code `$044E` to `$06000350`.
- **Called by** (resolved via BSRF target computation):
  - slot 3 (BUP magic-string sizing) at +$09C4, +$09CC
  - slot 4 (BUP_Write outer path) at +$0B80 (disp `$2A8C`)
  - slot 7 (BUP_Dir per-packet header) at +$1A84 (disp `$1B88`)
  - slot 9 (BUP_GetDate date math) at +$1E80,
    +$1E94, +$1F00, +$1F32
  - `+$32A6` (BUP_Write per-block sizing) at +$33EE
    (disp `$0000021E`)
- **Inputs**: R0 = divisor, R1 = dividend
- **Returns**: R0 = quotient (also left in R1). R2 is
  callee-preserved (pushed at entry, popped in the RTS delay slot),
  so the remainder is not returned; use `+$36B8` for the remainder.

Standard SH-2 32-step unsigned division. The most-used math
primitive in the driver - every documented caller turns out to
be on a BUP code path (slot 3 BUP-magic scan, slot 4's
BUP_Write dispatch, slot 7 BUP_Dir, slot 9 BUP date routines,
and the BUP_Write inner sub at +$32A6). It is kept in this
file (and referenced from `backup_library.md`) because the sub
itself is generic 32/32 division with no BUP-specific
semantics.

#### Sub `+$157C` — Per-peripheral data dispatcher / commit

- **Body**: `+$157C` to `+$16DA` (352 bytes)
- **Called by**: slot 5 (non-port-2 path, after slot 3 succeeds)
- **Inputs**: R4 = word value (peripheral-event code, written to
  slot 11 buffer at offset 0); R5 = caller-supplied output buffer
  base (phase-2 destination), written directly. The internal
  `SP+0` stack word is a separate 4-byte staging budget, unrelated
  to R5.
- **Returns** (R0): `0` = success (list terminator reached); `8` =
  out-of-range. There are two `8` sites: the entry value exceeds
  `driver_base+$38` (+$15B2), or the slot write cursor exceeds it
  on the advance path (+$16A8).
- **No PR push / no BSR-JSR calls** — leaf function

Mode 0 (initial setup, at +$15DC):

Mode 1 alternate pass at +$1608 falls through into common-tail at +$1630.

Mode 2 (data-copy) at +$1668:

**Behavior summary**: `+$157C` walks the slot 11 buffer
(driver_base+$2C dereference) as an array of `(uint16 entry)`
words, stopping at the first zero word. `driver_base+$38` is the
validity bound (an entry value or the slot cursor exceeding it
returns 8), not a loop count. It dispatches on a "phase" counter
(R9) that **persists across entries** rather than cycling per
entry:
- Phase 0 (first entry): stages a fixed 4 bytes
  (`periph[61,63,65,67]`) into the `SP+0` word, then falls through
  into the phase-1 commit loop. Advances phase to 1.
- Phase 1: copies whole words straight from the peripheral
  data-stream region into `work_buf` slots indexed by the slot
  cursor (R13). Loops while the read offset stays below the record
  stride and the prior word is non-zero; when a terminating zero
  word is reached it advances phase to 2.
- Phase 2: writes peripheral data bytes into caller's R5 buffer,
  bounded by both the record stride and the staged `SP+0` budget.

It's the **slot-5-side data commit** — takes the event code R4
from caller, validates and records it, then iterates through the
peripheral list copying data from the peripheral data-stream
region into the caller-supplied buffer.

```python
def sub_157C(event_code, caller_buf):            # R4 in, R5 in
    PTR       = 0x06000354                        # fixed pointer cell (R6)
    work_buf  = mem.L[mem.L[PTR] + 0x2C]          # slot-11 word array
    count     = mem.L[mem.L[PTR] + 0x38]          # max valid entry / slot value
    data_base = mem.L[mem.L[PTR] + 0x30]          # peripheral data stream
    stride    = mem.L[mem.L[PTR] + 0x40]          # per-peripheral record size
    # base is re-dereferenced from PTR on every field access in the asm.

    mem.W[work_buf] = event_code                  # publish event code at +0 (R4)

    phase      = 0                                # R9 (persists across entries)
    slot_idx   = 1                                # R13: work_buf word write cursor
    out_cursor = 0                                # R11: caller_buf byte cursor
    stage      = bytearray(4)                     # SP+0 word: phase-2 byte budget

    entry_idx  = 0                                # R12
    while True:
        entry = mem.W[work_buf + entry_idx * 2]
        if entry == 0:
            return 0                              # end of list -> success
        if entry > count:
            return 8                              # entry value out of range

        periph = data_base + entry * stride       # R1

        if phase == 0:
            phase = 1
            stage[0] = mem.B[periph + 61]         # offsets 61,63,65,67
            stage[1] = mem.B[periph + 63]
            stage[2] = mem.B[periph + 65]
            stage[3] = mem.B[periph + 67]
            r4 = 68                               # carries into the commit loop
            phase, slot_idx, r4 = commit_loop(periph, work_buf, count,
                                              stride, slot_idx, r4, phase)
            if phase == 2:                        # commit tail hit a zero word
                out_cursor = copy_to_caller(periph, caller_buf, stride,
                                            stage, out_cursor, r4)
        elif phase == 1:
            r4 = 8
            phase, slot_idx, r4 = commit_loop(periph, work_buf, count,
                                              stride, slot_idx, r4, phase)
            if phase == 2:
                out_cursor = copy_to_caller(periph, caller_buf, stride,
                                            stage, out_cursor, r4)
        else:                                     # phase == 2 (fresh entry)
            r4 = 8
            out_cursor = copy_to_caller(periph, caller_buf, stride,
                                        stage, out_cursor, r4)

        if slot_idx > count:
            return 8                              # slot cursor out of range
        entry_idx += 1


def commit_loop(periph, work_buf, count, stride, slot_idx, r4, phase):
    # +$1630 / +$1610: copy whole words from periph into work_buf slots.
    while r4 < stride and mem.W[work_buf + (slot_idx - 1) * 2] != 0:
        r4 += 1
        mem.B[work_buf + slot_idx * 2]           = mem.B[periph + r4]  # low
        slot_idx += 1
        r4 += 2
        mem.B[work_buf + (slot_idx - 1) * 2 + 1] = mem.B[periph + r4]  # high
        r4 += 1
    if mem.W[work_buf + (slot_idx - 1) * 2] == 0:
        phase = 2                                 # +$1666
    return phase, slot_idx, r4


def copy_to_caller(periph, caller_buf, stride, stage, out_cursor, r4):
    # +$1684 / +$1674. NOTE: r4 is reset to 8 only for a fresh phase-2 entry;
    # when reached via the commit tail it keeps the loop's final r4.
    budget = int.from_bytes(stage, 'big')         # mem.L[SP+0]
    while r4 < stride and out_cursor < budget:
        r4 += 1
        mem.B[caller_buf + out_cursor] = mem.B[periph + r4]
        out_cursor += 1
        r4 += 1
    return out_cursor
```

### Driver behavior at the system level

- **Peripheral side** (slots 0, 2, 3, 4): peripheral data
  stream access through `mem.L[driver_base+$30]`, packet
  collection into the slot-11 data buffer (`driver_base+$78+`,
  pointer published at table offset +$2C), validation via
  `+$1434` / `+$1BB4`
- **Lightweight query side** (slots 1, 5, 6, 8): thin wrappers
  that dispatch to specialized port-2 subs (`+$2FD0`, `+$279C`,
  ...) and the shared validate/commit helpers; return small
  status codes (`-1`, `0`, `1`, `2`, `3`, `5`, `7`, `8`) to the
  caller
- **BUP side** (slots 7, 9, 10): slot 7 builds the port-2 BUP
  directory packet (BUP_Dir orchestrator; reuses the
  peripheral-data-stream read and `+$1434` validation but targets
  the backup subsystem and writes to the caller buffer); slots 9
  and 10 are date arithmetic (year/month/day) for BUP_GetDate /
  BUP_SetDate. Documented in backup_library.md

The slot 0 relocator is invoked once by PER_Init. Subsequent
calls from game code go through `JSR @($06000354)` (which
dereferences to the driver base) and then `JSR @($00,driver_base)`
through `JSR @($28,driver_base)` for slot dispatch.
