# BIOS System Services

The BIOS exposes two parallel sets of entry points to disc/game
code: the BIOS-ROM Public Routine Table at `$0005D8` (BIOS-ROM
addresses callable directly) and the BIOS-published WRAM-H
function-pointer slots at `$06000234-$0600034C` plus the SYS_*
dispatch table at `$06000300+`. This file catalogs both, the
data tables at `$00030C` they depend on, and the per-service
function bodies (e.g. `SYS_SETUINT` at `$06000794`,
`SYS_TASSEM` at `$060007F0`, etc.) deposited in WRAM-H by the
Phase 4 copy.

## BIOS-Published Service Pointers in Work RAM-H

The BIOS leaves a set of function-pointer slots scattered through the
first ~$400 bytes of Work RAM-H. The disc's IP code reads these slots
to obtain entry points for BIOS-provided services it needs.

Each slot is a 4-byte pointer at a fixed Work-RAM-H address. The
target is BIOS-resident code that the Phase 4 copy step deposits in
Work-RAM-H ($06000220-$06000B00 range from BIOS $20000820+) or in the
Phase 4 system-init copy ($06001100-$060017FF from BIOS $20001100+).

Confirmed slots (from runtime trace + captured handoff dump):

| Slot address | Target | Purpose |
|--------------|--------|---------|
| $06000254 | $06002100 | IP entry point (the address the BIOS will JMP into the disc's IP at) |
| $06000260 | $06000D00 | Pointer to a 16-byte zero-filled workspace buffer the BIOS makes available to game code |
| $06000300 | $06000794 | "RegisterSCUHandler" - see below. First function pointer in a $06000300-$0600034F service table. |
| $06000328 | $000004C8 | Pointer to BIOS-ROM SMPC re-init entry; used by NMI handler ($000420) |

Slot at $06000300 leads a packed function-pointer table (entries every
4 bytes, with default-RTS placeholders for unimplemented slots).
Function identities below are recovered by disassembling each target
in the captured Work-RAM-H dump and matching the semantics against
the SDK's SYS_* service descriptions in ST-162-R1. Slot **values**
are verified byte-for-byte against the captured real-BIOS handoff
state in `handoff-*/wramh.bin` (identical across two independent
disc-boot captures).

| Slot | Value at handoff | Identified service | Notes |
|------|------------------|--------------------|-------|
| $06000300 | $06000794 | SYS_SETUINT | Set SCU interrupt handler in $06000900 table |
| $06000304 | $0600077C | SYS_GETUINT | Get SCU interrupt handler from $06000900 table |
| $06000308 | $0600083C | (default RTS) | reserved slot |
| $0600030C | $0600083C | (default RTS) | reserved slot |
| $06000310 | $06000784 | SYS_SETSINT | Set interrupt vector at VBR+idx*4 |
| $06000314 | $06000774 | SYS_GETSINT | Get interrupt vector at VBR+idx*4 |
| $06000318 | $0600083C | (default RTS) | reserved slot |
| $0600031C | $0600083C | (default RTS) | reserved slot |
| $06000320 | $060006B0 | SYS_CHGSYSCK | Change system clock mode (SDK dispatches SYS_CHGSYSCK through this slot). Chains through two function pointers via the $060006AA helper (pool $06000708 -> $06000328 -> $000004C8 SMPC reinit; pool $0600070C -> $0600032C -> $00001800 SCU init), then issues SMPC command $19 via data pools $06000714/$06000718; the chained $000004C8 routine performs the CKCHG that re-clocks the system. |
| **$06000324** | **$00000000** | **SYS_GETSYSCK clock-mode word (data, not a pointer)** | Holds the current system clock mode; SYS_GETSYSCK is the SDK's inline read of this location (`*(Uint32*)$06000324`). Initial value 0. Not the default-RTS sentinel; mis-populating it with $0600083C would corrupt the clock-mode read. |
| $06000328 | $000004C8 | SMPC re-init entry | NMI / soft-reset hook; can also be invoked directly by game code to drive SMPC reinit. |
| $0600032C | $00001800 | SCU register zero-fill | Same body as $060002A0. |
| $06000330 | $060007F0 | SYS_TASSEM | TAS.B at $06000B00 + R4 (semaphore acquire) |
| $06000334 | $060007FE | SYS_CLRSEM | Zero byte at $06000B00 + R4 (semaphore clear) |
| $06000338 | $0600083C | (default RTS) | reserved slot |
| $0600033C | $0600083C | (default RTS) | reserved slot |
| $06000340 | $060007B0 | SYS_SETSCUIM | Set SCU interrupt mask (writes R4 to mask shadow + IMS) |
| $06000344 | $060007C0 | SYS_CHGSCUIM | Change SCU interrupt mask: mask = (mask & R4) \| R5 |
| **$06000348** | **$FFFFFFFF** | **SCU IMS shadow** | Not a function pointer. Mirrors the value last written to the SCU IMS register so SYS_GETSCUIM / SYS_CHGSCUIM can read the current mask (the live IMS register at $25FE00A0 is write-only). Initial $FFFFFFFF is the "mask everything" placeholder before any SETSCUIM call; once a game touches the mask it tracks the live IMS. |
| $0600034C | $06000664 | SCU IST clear / AIACK ack | Writes R4 to SCU IST ($25FE00A4) clearing those bits, then writes 1 to AIACK ($25FE00A8). Used by the SCU dispatcher epilogue, not a SDK SYS_* service. |
| **$06000350** | **$00000000** | **data zero (not a pointer)** | Slot holds literal zero. |
| **$06000354** | **$00000000** | **data zero (not a pointer)** | Slot holds literal zero. Read by game code as `mem.L[$06000354]` followed by `mem.L[R+12]`; with R=0 the chain reaches the BIOS ROM reset-vector table at $0C (= manual-reset SP = $06002000). |
| $06000358 | $0007D600 | PER_Init | Peripheral subsystem init trampoline (see Peripheral Library section). |
| $0600035C | $0007D660 | PER_GetPer data block | Compressed peripheral driver image consumed by PER_Init / sub_1F04. |
| $06000360-$0600037C | $0600083C | (default RTS) | all reserved |

The IMS-shadow slot ($348 = SYS_GETSCUIM source), the clock-mode word
($324 = SYS_GETSYSCK source) and the explicit-zero slots ($350, $354)
are *data*, not function pointers; software reads them as longword
data, not as call targets.

Three SDK SYS_* services are inline reads with no separate WRAM-H code
target (SYS_GETSCUIM, SYS_GETSYSCK, SYS_PCLRMEM):

- **SYS_GETSCUIM**: there are only two pool references to the IMS
  shadow address $06000348 in the entire WRAM-H image - $060007EC
  (used by SETSCUIM/CHGSCUIM) and $06000958 (used by the SCU
  interrupt dispatcher). No third standalone "read $06000348 and
  return" function exists; the SDK implements SYS_GETSCUIM as the
  inline read `*(Uint32*)$06000348` against the shadow.
- **SYS_GETSYSCK**: the SDK implements it as the inline read
  `*(Uint32*)$06000324` of the clock-mode word the BIOS maintains at
  $06000324 (see the $06000300 table above).
- **SYS_PCLRMEM**: the SDK implements it as the inline read
  `*(Uint8*)$06000210` of the NMI-preserved memory the SDK describes
  ("8 bytes of memory on the SDRAM controlled by the Boot ROM that are
  initialized to 0 on power-on but can be saved with the reset button
  (NMI)").

12 of the 15 SDK SYS_* services dispatch through a WRAM-H function-
pointer slot: SETUINT ($300), GETUINT ($304), SETSINT ($310), GETSINT
($314), CHGSYSCK ($320), TASSEM ($330), CLRSEM ($334), SETSCUIM
($340), CHGSCUIM ($344) in the $06000300 table, plus EXECDMP
($0600026C), CHKMPEG ($06000274) and CHGUIPR ($06000280) in the lower
pointer region. The remaining 3 are inline reads of BIOS-exposed
fields rather than calls: GETSCUIM (IMS shadow $06000348), GETSYSCK
(clock-mode word $06000324) and PCLRMEM (NMI-preserved memory
$06000210).

Additional pointer slots outside the $06000300 table. Values come
from the captured real-BIOS handoff state in `handoff-*/wramh.bin`;
slots NOT listed here held `$00000000` in both captures and are
explicit data-zero locations (the BIOS doesn't put anything in
them). The full enumeration $06000234-$0600037C from one capture
is included at the end as a reference.

| Slot | Target | Purpose |
|------|--------|---------|
| $06000234 | $000002AC | BIOS-ROM sub_02AC fill routine |
| $06000238 | $000002BC | BIOS-ROM sub_02BC copy routine |
| $0600023C | $00000350 | Data pointer (head of BIOS copy-data table chain); NOT a function pointer |
| $06000240 | $32524459 ("2RDY") | Warm-boot magic value (read at Phase 3 boot-path selection) |
| $06000248 | $00008000 | Region/PAL flag word (`[reversed_area:4][pal:1][0:11]`) |
| $0600024C | $00000000 | Boot state byte ("HCDM" / "HCGG" written here at runtime) |
| $06000250 | $06000646 | Pointer to an infinite-loop trap (`BRA self; NOP`) - "should not be reached" sentinel |
| $06000254 | $06002100 | IP entry point (the BIOS's record of where it will JMP into the disc's IP) |
| $06000258 / $0600025C | $0600083C | Default no-op handler |
| $06000260 | $06000D00 | Workspace pointer (BIOS-provided 16-byte zero-filled buffer) |
| $06000264 | $00000000 | sub_1C90 memcpy source-pointer slot (data, not a function pointer) |
| $06000268 | $00001A18 | BIOS-ROM sub_1A18 (copies the $040000 SEGA PLAYER shell to $06020000; see system_applications.md) |
| $0600026C | $0000186C | SYS_EXECDMP entry (SDK dispatches SYS_EXECDMP through this slot). Tail-call thunk: loads R7 from $19C4, then jumps through the pointer at $19BC (= $000424). |
| $06000270 | $000030DC | BIOS-ROM CD-block helper |
| $06000274 | $00003174 | SYS_CHKMPEG entry (MPEG-card presence check; SDK dispatches SYS_CHKMPEG through this slot). |
| $06000280 | $06000810 | SYS_CHGUIPR entry (SDK dispatches SYS_CHGUIPR through this slot). Copies the caller's 32-longword (128-byte) interrupt-priority table from R4 into the priority table at $06000A80. |
| $06000284 | $00001C90 | BIOS-ROM helper (sub_1C90 game-load pump) |
| $06000288 | $000018A8 | BIOS-ROM helper |
| $0600028C | $00001874 | BIOS-ROM cartridge/disc header check |
| $06000290 | $00000003 | sub_1C90 state-counter slot (data, not a function pointer); initial 3 |
| $06000298 | $000031F4 | BIOS-ROM helper |
| $0600029C | $00001904 | BIOS-ROM animation step function (per "Saturn Logo Animation" section in boot_library.md) |
| $060002A0 | $00001800 | BIOS-ROM SCU register init function |
| $060002B0 | $06004000 | "1st Read Address" — populated from System ID +$F0; the load destination for File ID 2 (data, not a function pointer) |
| $060002B4 | $00000000 | sub_1C90 memcpy byte-count slot (data, not a function pointer) |
| $060002C4 | $06001120 | BIOS Phase 4 system-init-code entry (Work-RAM-H copy of BIOS $001100) |
| $060002C8 | $00001A3C | BIOS-ROM header validation |
| $060002CC | $00001912 | BIOS-ROM CD-block command dispatch (calls sub_29D4) |
| $060002D0 | $00002F48 | BIOS-ROM CD sector read |
| $060002D4 | $000018FE | BIOS-ROM helper |
| $060002D8 | $00003126 | BIOS-ROM helper |
| $060002DC | $00002650 | BIOS-ROM helper |
| $06000328 | $000004C8 | BIOS-ROM SMPC re-init entry; used by NMI handler at $000420 |
| $0600032C | $00001800 | BIOS-ROM SCU register init function |
| $06000348 | $FFFFFFFF | SCU IMS shadow placeholder (data, not a function pointer) |
| $0600034C | $06000664 | Interrupt-acknowledge helper (clears SCU IST, writes AIACK). Not a SYS_* service - used by the SCU dispatcher epilogue |
| $06000358 | $0007D600 | PER_Init entry (peripheral subsystem init trampoline) |
| $0600035C | $0007D660 | PER_GetPer data block (compressed peripheral driver image consumed by PER_Init) |

The explicit-zero data slots in this region ($244, $0244, $0278,
$027C, $0294, $02A4, $02A8, $02AC, $02B8, $02BC, $02C0, $02E0-
$02FC) are not function pointers; they hold zero at handoff. Some
of them (notably $0264, $02B0, $02B4) are read by BIOS code as
longword data during the sub_1C90 game-load pump.

The $06000268-$060002DC range is mostly a BIOS-internal **function
pointer table for BIOS-ROM helpers** - most entries point back to the
BIOS ROM itself rather than to Work-RAM-H, and are used by BIOS code
to dispatch through known function addresses. Three entries are
exceptions: $0600026C (SYS_EXECDMP), $06000274 (SYS_CHKMPEG) and
$06000280 (SYS_CHGUIPR) are game-callable SDK SYS_* services
dispatched through these slots.

## BIOS-Resident Functions in Work RAM-H

These are the actual function bodies the pointer slots target. They
are SH-2 code deposited in Work RAM-H by the BIOS's Phase 4 copies.

### $06000794 - RegisterSCUHandler

The BIOS-published service for installing a function as an SCU
interrupt handler. The SDK exposes this as SYS_SETUINT.

- **Pool**: $060007A4 = $06000900 (SCU handler table base);
  $060007AC = $0600083C (default RTS+NOP placeholder)

```python
def sys_setuint(vector_idx, handler):       # R4, R5
    if handler == 0:
        handler = 0x0600083C                # default RTS+NOP placeholder
    mem.L[0x06000900 + vector_idx * 4] = handler
```

Called from the IP at startup (e.g., NiGHTS' IP+$14 calls it with
R4 = 64 / V-Blank IN vector and R5 = the IP's own VBlank-IN handler).
The handler is stored at `$06000900 + R4*4`. For the SCU-vector range
$40-$5F that effective range is $06000A00-$06000A7F (32 entries,
128 bytes); the immediately-following 128 bytes at $06000A80-$06000AFF
hold the priority/mask table (see scu_interrupt_handling.md), not
additional handler entries.

### $0600083C - Default no-op handler

Two-instruction stub used as a placeholder handler: `RTS` followed by
`NOP` in the delay slot — returns immediately with R0 unchanged.

The unused entries of the $06000300 service table point here, as does
the SCU handler table for any vector the IP hasn't (yet) registered a
handler for.

**Why the null-substitution matters.** IP-side cleanup code calls
SETUINT with `handler=0` to "clear" SCU vector handlers between
ownership transitions (the IP clears its own VBLANK_IN/OUT handlers
before transferring to game code, e.g.
`SETUINT($40, 0)`, `SETUINT($41, 0)`). The substitution ensures the
slot ends up holding a benign RTS+NOP address ($0600083C) rather
than a null pointer, so if the corresponding SCU interrupt fires in
the window between handler-clear and the next handler-install, the
synthesized dispatch stub JSRs through a valid RTS-only routine and
returns cleanly. Without the substitution the slot would hold a
literal zero; JSR through zero is JMP @0, which on the SH-2 begins
fetching from address 0 and triggers an illegal-instruction
exception cascade. The handoff dump
(`handoff-*/wramh.bin`) confirms the post-cleanup state:
the first 5 SCU-vector slots at $06000A00-$06000A13 (vectors
$40-$44) hold $0600083C, never zero.

### $06000900 - SCU handler function-pointer table base

Base address that RegisterSCUHandler / SETUINT index by `vec * 4`.
For the SCU interrupt-vector range $40-$5F (32 vectors) the effective
handler slots land at $06000A00-$06000A7F (128 bytes, 32 entries of
4 bytes each). The SCU interrupt dispatcher at $060008F4 (per the
"Interrupt Trampoline Mechanism" section of scu_interrupt_handling.md)
reads from these same effective addresses.

The 128 bytes immediately following ($06000A80-$06000AFF) hold the
priority/mask table - not additional handler pointers. This is the
SCU-vector portion of the larger priority table at base $06000980
(read by the dispatcher via pool $06000960 as `mem.L[$06000980 +
vec_idx*4]`); $06000A80 = $06000980 + 64*4 is the offset for the
SCU range $40-$5F.

### $0600077C - SYS_GETUINT

Returns the function pointer currently registered in the SCU handler
table for the given vector index.

```python
def sys_getuint(vector_idx):                # R4
    return mem.L[0x06000900 + vector_idx * 4]
```

### $06000784 - SYS_SETSINT

Sets the contents of an SH-2 interrupt-vector-table entry. Writes to
VBR+idx*4 (not the SCU handler table at $06000900).

```python
def sys_setsint(vector_idx, handler):       # R4, R5
    if handler == 0:
        handler = mem.L[0x20000600 + vector_idx * 4]   # per-vector default
    mem.L[VBR + vector_idx * 4] = handler
```

Default-handler table pointer at pool $060007A8 = $20000600 (BIOS-ROM
cache-through alias of $0600). The table holds a per-vector default
for the first 128 of the 256 possible SH-2 vector entries (BIOS $0600-
$07FF, 512 bytes); BIOS data at $0800 onward is the version string and
unrelated content. Contents by class:

- Most slots = $0600094A (`RTE; NOP` no-op in WRAM-H)
- Some slots = $0600094E (mask-all + infinite spin, for unrecoverable exceptions)
- NMI slot ($00062C, idx 11) = $00000420 (BIOS-ROM NMI entry)
- idx 64-95 (SCU vector range $40-$5F) = per-vector trampolines at $06000840 onward, each pushing R0=vec_idx and BRA'ing into the SCU dispatcher common entry at $060008F4 (so SYS_SETSINT with handler=0 for these SCU vectors installs the BIOS dispatcher trampoline, not a no-op). The exceptions are idx 78 ($4E) and idx 79 ($4F), which default to $0600094A (the `RTE; NOP` no-op) rather than a trampoline.

### $06000774 - SYS_GETSINT

Returns the current contents of an SH-2 interrupt vector at
VBR+idx*4.

```python
def sys_getsint(vector_idx):                # R4
    return mem.L[VBR + vector_idx * 4]
```

### $060007F0 - SYS_TASSEM

TAS-based atomic acquire of a semaphore byte. The semaphore array
is the 256-byte zero-filled region at $06000B00 (Phase 4 fill).
Returns 1 if the semaphore was acquired (T=1, byte was zero before
TAS), 0 if it was already held.

```python
def sys_tassem(sema_idx):                   # R4
    # TAS via cache-through alias ($20000000 | $06000B00) to defeat cache
    # and get atomic semantics across master / slave SH-2.
    addr     = 0x20000000 | (0x06000B00 + sema_idx)
    acquired = 1 if mem.B[addr] == 0 else 0
    mem.B[addr] |= 0x80
    return acquired
```

Note the OR with $20000000: the TAS is performed via the cache-through
address alias of Work-RAM-H, defeating the cache so the atomic
semantics work on a multi-CPU system (master ↔ slave SH-2).

### $060007FE - SYS_CLRSEM

Clears a semaphore byte.

```python
def sys_clrsem(sema_idx):                   # R4
    mem.B[0x06000B00 + sema_idx] = 0        # cached alias (not cache-through)
```

Notably this clears via the cached address, while SYS_TASSEM acquires
via cache-through. For correctness under cache, software must invalidate
the affected line between modes or use only one alias consistently.

### $060007B0 - SYS_SETSCUIM

Sets the SCU interrupt mask. Updates a shadow word, writes the new
value to the live IMS register at $25FE00A0, then clears matching IST
bits and conditionally pulses AIACK via a shared tail at $060007D6
also used by SYS_CHGSCUIM.

```python
def sys_setscuim(mask):                     # R4
    saved_sr = SR
    SR = mem.W[0x06000828]                  # = $00F0, sign-extended (mask all interrupts)
    mem.L[0x06000348] = mask                # IMS shadow
    mem.L[0x25FE00A0] = mask                # live SCU IMS register
    # Shared tail with SYS_CHGSCUIM (BRA into $060007D6):
    r4_ext = sign_extend_16(mask)
    mem.L[0x25FE00A4] = r4_ext              # IST clear pattern
    if r4_ext >= 0:                         # bit 15 of mask not set
        mem.L[0x25FE00A8] = 1               # pulse AIACK
    SR = saved_sr
```

### $060007C0 - SYS_CHGSCUIM

Modifies the SCU interrupt mask: `shadow = (shadow & R4) | R5`. Reads
the shadow, applies AND-mask and OR-mask, writes back to shadow and
IMS, then falls through into the shared IST-clear / AIACK tail
described above.

```python
def sys_chgscuim(and_mask, or_mask):        # R4, R5
    saved_sr = SR
    SR = mem.W[0x06000828]                  # = $00F0, sign-extended (critical section)
    cur = mem.L[0x06000348]                 # IMS shadow
    cur = (cur & and_mask) | or_mask
    mem.L[0x06000348] = cur
    mem.L[0x25FE00A0] = cur
    # Shared tail:
    r4_ext = sign_extend_16(and_mask | or_mask)
    mem.L[0x25FE00A4] = r4_ext              # IST clear pattern
    if r4_ext >= 0:
        mem.L[0x25FE00A8] = 1               # pulse AIACK
    SR = saved_sr
```

### $06000664 - SCU IST clear / AIACK ack

A tiny SCU-dispatcher helper, not a SDK SYS_* service. Clears
selected SCU IST bits and pulses AIACK.

```python
def scu_ist_clear(ist_bits):                # R4
    # Pool $06000674 = 0x25FE00A0 (SCU IMS base).
    # IMS+$04 = IST register; IMS+$08 = A-bus interrupt acknowledge.
    mem.L[0x25FE00A4] = ist_bits            # clear matching IST bits
    mem.L[0x25FE00A8] = 1                   # pulse AIACK
    return mem.L[0x25FE00A8]                # delay-slot read into R0
```

The interrupt-acknowledge helper used by the SCU dispatcher epilogue
(also reachable through the WRAM-H slot at $0600034C).

### $060006B0 - SYS_CHGSYSCK

A short routine that chains through two BIOS function pointers via
the $060006AA helper (`MOV.L @R1,R1; JMP @R1`):

- Pool $06000708 = $06000328 -> `mem.L[$06000328]` = $000004C8 (SMPC re-init)
- Pool $0600070C = $0600032C -> `mem.L[$0600032C]` = $00001800 (SCU register init)

After both calls, the routine issues an SMPC command sequence using
two data pools: $06000718 = $20100063 (SMPC SF register) and
$06000714 = $2010001F (SMPC COMREG). It writes 1 to SF, $19 to
COMREG, DT busy-waits, then polls SF.0 for completion.

The two chained calls (SMPC re-init then SCU register init) followed
by the SMPC command implement SYS_CHGSYSCK (change system clock
mode), which the SDK dispatches through slot $06000320: the chained
$000004C8 routine performs the CKCHG that changes the clock.

### $06000810 - SYS_CHGUIPR

The SDK dispatches SYS_CHGUIPR (change user interrupt priority)
through slot $06000280, which points here. Under an all-interrupts-
masked critical section the routine copies the caller's 32-longword
(128-byte) priority table into the priority table at $06000A80.

```python
def sys_chguipr(ipr_tab):                   # R4 = pointer to priority table
    # Critical section: mask all interrupts, copy 32 longwords (128 bytes)
    # from ipr_tab to the priority table at $06000A80, restore SR.
    saved_sr = SR
    SR = mem.W[0x06000828]                  # = $00F0, sign-extended
    dest = 0x06000A80
    for _ in range(32):
        mem.L[dest] = mem.L[ipr_tab]
        ipr_tab += 4
        dest    += 4
    SR = saved_sr
```

The 128-byte destination at $06000A80 is the SCU-vector slice of the
priority/mask table ($06000A80 = $06000980 + 64*4; see the $06000900
section), so CHGUIPR installs a new user-interrupt priority table
there. (SYS_PCLRMEM is a separate service - the inline
`*(Uint8*)$06000210` read of NMI-preserved memory - not this routine.)

## BIOS ROM Public Routine Table ($0005D8-$0005FC)

A second BIOS-published API distinct from the WRAM-H dispatch table at
$06000300+. Each non-zero entry is a 32-bit BIOS-ROM address holding a
function pointer that disc IP code can JSR through directly. Verified
empirically: none of these slots have callers inside the BIOS itself
(grep across the BIOS image shows zero internal references to
$0000_05D8 / 05E4 / 05E8 / 05F8). They exist solely so the disc's
IP/SYS_SEC can reach low-level BIOS services without going through
the runtime SDK dispatch.

| Slot | Value | Target | Purpose |
|------|-------|--------|---------|
| $0005D8 | $00001D7C | sub_1D7C | Table-driven hardware initialization (read 16-bit table at PC-relative source, write to address/value pairs with inner loops of 5 stores per outer iteration; touches multiple chip address spaces). Probably the per-chip register-reset path the BIOS uses when reinitializing on CKCHG and re-exposes for the IP. |
| $0005DC | $56696D09 | (signature data, not a function pointer) |  |
| $0005E0 | $00000000 | (zero / reserved) |  |
| $0005E4 | $000050EA | sub_50EA | **Font upload to VDP2 VRAM.** Calls sub_1F04 to LZSS-decompress the 1bpp font glyphs at BIOS $5240 into WRAM-H scratch at $06000E20 (size hint $1000, actual output 3765 bytes), then BSRs to sub_5000 which performs a 1bpp→4bpp expansion via a 4-entry lookup at BIOS $5782 = `{$00, $0D, $D0, $DD}` (0=off, $D=on color index) and copies the expanded data to VDP2 VRAM at $25E00000. End result: an 8-pixel-wide bitmap font readable by VDP2's character generator for security-screen text rendering. See bios_font.md for format details. |
| $0005E8 | $0000511C | sub_511C | **Security-screen text drawer.** Two-string painter that lays out copyright/region text into the NBG0 tile map at fixed locations using the font sub_50EA uploaded. R4 = first source string (drawn as a 20-char + 12-char split at $25E06988), R5 = second source string (drawn at $25E06B14 as a single line). Reads the region/PAL flag at $06000248 bit $0800 to shift the destination by +$100 bytes. Calls the private per-char helper at $0050A0. Appears to be IP/SYS_SEC specific - no other caller has been observed (no published slot beyond this one, no literal $50A0 or $0000511C reference anywhere outside this routine). See "Security-screen text drawers" below for the full disassembly. |
| $0005EC | $00005208 | sub_5208 | Sets SR=$00F0 (mask all interrupts), loads R15 from constant $06002000 (default stack at pool $5238), then conditionally overrides R15 from `mem.L[$060002A8]` if non-zero (pool $522C). JMPs through R1 = `mem.L[$06000234]` = $000002AC (BIOS fill routine, sub_02AC) with R4=0 and R0 = $00005230 (a `[count=$04C0, dest=$06000D00]` table). Effectively: **zero-fill $04C0 longwords at $06000D00 (covering the data region $06000D00..$06002000) on a re-set stack.** Looks like a "reset and re-run system fill" entry - used after the BIOS has been re-staged or after a soft reset. |
| $0005F0 | $45E3B59B | (signature data) |  |
| $0005F4 | $BCB0E6B5 | (signature data) |  |
| $0005F8 | $00001F04 | sub_1F04 | LZSS decompress (see bios_decompression.md). Args: R4=source, R5=pointer-to-dest-pointer (callee updates), R6=size/flag. |
| $0005FC | $54CFCE5B | (signature data) |  |

The signature-data slots ($05DC, $05E0, $05F0, $05F4, $05FC) interleave
with the function pointers and contain non-pointer bytes — likely
checksums or version markers that the disc's SYS_SEC validates before
trusting the rest of the table.

### Call site in NiGHTS IP

```python
# IP code at $06002144 reaches sub_50EA via the BIOS-ROM public-routine table:
sub_50ea = mem.L[0x000005E4]                # = 0x000050EA (font upload)
sub_50ea()
```

The constants pool at $0600217C in the IP holds `$05E4, $05E8` adjacent
- so the IP is set up to walk the table making multiple BIOS calls in
sequence.

## Data Tables ($00030C-$00037F)

The initialization copy/fill tables use a packed format:

For fill (sub_02AC): `[count:32] [dest:32]`
For copy (sub_02BC): `[count:32] [dest:32] [src:32]`

Multiple tables can be chained by consecutive calls. The subroutines advance
R0 past the table entries they consume, so the next MOVA can point to the
next table or use the advanced R0 directly.


## BIOS-Published WRAM-H Function-Pointer Slots ($06000234-$0600034C)

A second BIOS-published API, parallel to the BIOS-ROM table at
$0005D8-$0005FC. The BIOS fills these WRAM-H slots with addresses
of BIOS-resident routines (some in BIOS ROM $0-$7FFFF, some in the
WRAM-H copy of BIOS code at $06000400-$06001FFF). Disc IP code
dereferences a slot and JSRs through it.

Slot inventory derived from comparing the two captured handoff
dumps (`handoff-*/wramh.bin`); the values listed are identical
across both, so this table is BIOS-deterministic. Where the target
is in BIOS ROM, the function was disassembled to the depth needed
to identify its purpose; deeper analysis is annotated as TODO.

| Slot | BIOS target | Purpose |
|------|-------------|---------|
| $0234 | BIOS $02AC (sub_02AC) | **Fill routine**. Reads `[count:32, dest:32]` from R0, writes R4 (value) `count` longwords, advances. R0 returns pointing past the table entries it consumed. Used by BIOS init and exposed for IP use. |
| $0238 | BIOS $02BC (sub_02BC) | **Copy routine**. Same as $02AC but `[count, dest, src]` and copies src→dest. |
| $023C | BIOS $00000350 | **NOT a function pointer.** A 32-bit data pointer; the value at $0350 is the head of the BIOS copy-data table chain. |
| $0250 | WRAM-H $06000646 | **System halt**. BIOS code there is just `BRA -2` (infinite loop) followed by constants. Called when BIOS wants to wedge the system (e.g., catastrophic-error path). |
| $0258 | WRAM-H $0600083C | Real BIOS sentinel: just `RTS; NOP`. |
| $025C | WRAM-H $0600083C | Same as $0258. |
| $0268 | BIOS $1A18 | **Region-init copy loop**. Reads a 3-entry table (count, dest, src) from PC-relative pool $1A30 (= $0400, $06020000, $00040000), runs a `[count, dest, src]`-style copy, then JMP @R7 where R7 was pre-loaded with the dest ($06020000) - effectively jumps into the just-copied code. |
| $026C | BIOS $186C | **SYS_EXECDMP entry**. Tail-call thunk: loads R7 from $19C4, then jumps through the pointer at $19BC (= $000424). SDK dispatches SYS_EXECDMP through this slot. |
| $0270 | BIOS $30DC | **CD-block command wrapper**. STS PR, BSR $284A (command issue), tests R0 result, returns 0/1. Predicate-style return. |
| $0274 | BIOS $3174 | **CD-block command wrapper variant** with extra args saved on stack. Calls BSR $284A like $30DC. |
| $0280 | WRAM-H $06000810 | **SYS_CHGUIPR entry**. Under an all-interrupts-masked critical section, copies the caller's 32-longword (128-byte) interrupt-priority table from R4 (post-inc) into the priority table at $06000A80 (destination set internally via MOVA). SDK dispatches SYS_CHGUIPR through this slot. |
| $0284 | BIOS $1C90 | **Interrupt-vector setup helper**. Calls a sub-helper $1CDE with R4=64 (SCU vector $40 = VBLANK_IN), then loads two pointers and tests state. Installs a BIOS-side VBLANK_IN hook used during the security screen. Required by games (e.g. NiGHTS) whose IP drives a multi-phase loading screen through this helper. |
| $0288 | BIOS $18A8 | **CD-block "wait & read" wrapper**. Similar prolog to $1874; BSR $1912 (CD-block status query), branch on result. |
| $028C | BIOS $1874 | **CD-block status predicate**. BSR $1912 then conditional return. |
| $0298 | BIOS $31F4 | **CD-block command wrapper** with 3 args saved on stack. BSR $2780 (same family as $284A). |
| $029C | BIOS $1904 | **CD-block command issue**, no return value (`MOV #-1; JMP`-style tail call). |
| $02A0 | BIOS $1800 | **SCU register init via GBR**. Sets GBR to $25FE0000 from pool $1850 (always SCU base, regardless of caller GBR), then per outer iter writes 5 zero longwords followed by one longword of $00000007 (via R6=7), advances by 2 additional longs of stride for next iter. 3 outer iterations total (24-long span). |
| $02B0 | (data) $06004000 | **NOT a function pointer.** A 32-bit data pointer to an IP-runtime area at $06004000. |
| $02C4 | WRAM-H $06001120 | **Empty at handoff** — the $06001100-$06001FFF region is zero-initialized in both captured dumps. This slot points to code that gets populated later, possibly by the IP itself or by a phase that runs after handoff. |
| $02C8 | BIOS $1A3C | **CD-block command-record comparator**. Loads 3 pointers, compares two pairs, returns equality. Helper for command-state validation. |
| $02CC | BIOS $1912 | **CD-block status query**. PR-save + JSR through pointer at $19AC, then conditional return path. The drive-ready check that most other CD wrappers (above) bounce through. |
| $02D0 | BIOS $2F48 | **Alignment helper / dispatcher**. Aligns R4 up to a 4-byte boundary, then BSR through a large stack frame. Probably a command-buffer setup. |
| $02D4 | BIOS $18FE | **CD-block command issue** (tail-call through the pointer at $19B4 = $00002F48, the same routine as slot $02D0). XORs R4 (zeroes arg). |
| $02D8 | BIOS $3126 | **CD-block command chain**. Calls $30DC twice, threading state through R13/R14. |
| $02DC | BIOS $2650 | **Switch-statement dispatcher**. BSR-and-BRA pattern across multiple targets ($2780, $2718, $29B6...). One entry per CD-block subcommand. |
| $0328 | BIOS $04C8 | **SMPC initialization**. Sets SR=$F0 (mask all interrupts), then writes the SMPC command sequence: RESDISA ($1A) to COMREG ($2010001F, loaded from pool at BIOS $05AC), followed by CKCHG320 ($0F) on cold boot or CKCHG352 ($0E) on warm. SF ($20100063, loaded from pool at BIOS $05B0) is acquired (write 1) and polled (bit 0) for completion via a DT spin loop around the RESDISA command; the CKCHG is then written to COMREG with no SF handshake. The CKCHG triggers a soft reset; control re-enters via the reset vector. |
| $032C | BIOS $1800 | Same routine as $02A0. |
| $034C | WRAM-H $06000664 | **SCU IST clear**. Loads R0=$25FE00A0 (SCU IMS), writes R4 to $25FE00A4 (SCU IST) — clearing the IST bits set in R4. Tiny routine, used for interrupt acknowledgment. |

Many of the "CD-block command" entries ($0270, $0288, $028C, $0298,
$029C, $02C8, $02CC, $02D0, $02D4, $02D8, $02DC) are
helpers used by the BIOS's pre-handoff CD-block negotiation. The IP
inherits these pointers but typically doesn't need to call them
again - disc reads after handoff are driven through the CD-block
register interface directly.

IP code is known to exercise $0284 (BIOS $1C90, interrupt setup) and
the $0234/$0238 fill/copy routines.

The non-pointer entries ($023C, $02B0) are data slots: the IP reads
them via `MOV.L @slot, Rn` and uses the value as a constant. `$023C`
holds the BIOS data-table base; `$02B0` holds the "1st Read Address"
populated from System ID +$F0.

