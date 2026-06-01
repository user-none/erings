# BIOS Handoff State

Per-subsystem state at the moment the BIOS finishes its boot work
and the disc's IP code begins executing at `$06002100`. Values
here come from captured Work-RAM-H, register, and chip-state dumps
taken on real hardware (USA BIOS, two independent commercial
discs). The set is deterministic except for per-disc bytes
(System ID, TOC) and the IP load buffer contents.

## BIOS Handoff State

State left behind by the BIOS at the moment the master SH-2 begins
fetching from the IP load window ($06002000-$0600A000). Captured from
real BIOS_USA runs against two unrelated commercial discs via the
trace mechanism in `cmd/debug` (first master fetch in the IP load
window). Comparison of the two captures shows the BIOS handoff is
**fully deterministic** apart from a small set of disc-derived data
caches; the values below are the observed-constant portion. The
disc-derived portion consists of CD-block register state plus
~280 bytes of WRAM-H disc-info caches synthesized from the actual
disc TOC and System ID.

### SH-2 Master

All registers below are byte-for-byte identical across both captures
(BIOS-controlled, game-independent).

| Register | Value |
|----------|-------|
| PC | $06002100 (IP+$100, security-code entry within the disc's own IP) |
| R0 | $00000358 |
| R1 | $06002100 |
| R2 | $0600021C |
| R3 | $06001800 |
| R4 | $00000000 |
| R5 | $FFFF7FFF |
| R6 | $00000000 |
| R7 | $060012FE (mid-system-init-code, where the BIOS Phase 4/5 dispatch returned from) |
| R8-R14 | $00000000 |
| R15 (SP) | $06002000 (top of IP load buffer; stack grows down below it) |
| PR | $060006A8 (set by the BIOS during Phase 4/5 dispatch chain) |
| SR | $00000001 (I3-I0 = 0 - interrupts UNMASKED; T = 1; other flags 0) |
| GBR | $25D00000 (VDP1 register base - the IP code uses GBR-relative MOV to talk to VDP1 registers) |
| VBR | $06000000 (Work RAM-H base) |
| MACH | $FFFFFFFF |
| MACL | $0000004A |
| Cache (CCR) | $01 (cache enabled, all four ways purged - set in Phase 1 $000380) |

Notes:
- SR=$00000001 means interrupts are enabled (mask = 0) and T=1 at
  handoff. This is unusual; the IP's first instruction can rely on
  interrupts being unblocked.
- GBR=$25D00000 lets short GBR-displacement MOVs hit VDP1 registers
  ($25D00000-$25D00017) directly.

### SH-2 Slave

Held in reset; the BIOS never executes a slave instruction during boot.

| Register | Value |
|----------|-------|
| PC | $20000200 (the cache-through BIOS reset entry; never reached) |
| SP (R15) | $06002000 |
| SR | $000000F0 (I3-I0 = $F, masked) |
| GBR / VBR / PR / R0-R14 / MACH / MACL | $00000000 |

The slave is started by the game issuing SMPC SSH_ON ($02). At that
point it begins executing from whatever target the game has installed
in the slave's reset vectors (typically Work RAM-H + slave VBR
offset). The BIOS does not pre-populate slave vectors.

### SH-2 On-chip BSC (Bus State Controller, $FFFFFFE0-$FFFFFFF8)

Set in Phase 2 ($000478). These cannot be captured by the runtime
state dump (no public accessors yet for the SH-2 on-chip peripherals)
but the values are stable and verified from the disassembly. core/bus.go
AccessCycles() consumes them as the basis for SH-2 bus-arbitration cost.

| Register | Address | Value | Notes |
|----------|---------|-------|-------|
| BCR1 | $FFFFFFE0 | $03F1 | AHLW=3, A1LW=3, A0LW=3, DRAM=001 |
| BCR2 | $FFFFFFE4 | $00FC | A3/A2/A1 buses all 32-bit |
| WCR | $FFFFFFE8 | $5555 | IW=1, W=1 (one idle, one wait per area) |
| MCR | $FFFFFFEC | $0078 | SDRAM longword, 10-bit column, refresh enabled (cold boot); $0070 on warm |
| RTCSR | $FFFFFFF0 | $0008 | Refresh clock = CLK/4 |
| RTCOR | $FFFFFFF8 | $0036 | Refresh constant = 54 |

All writes use the $A55A write-protect key in the upper 16 bits.

### SH-2 On-chip DMAC / FRT / WDT / INTC / DIVU

Not captured by the runtime state dump. Phase 6 ($0004C8) is known
to touch SAR0 ($FFFFFE80) and CHCR0 ($FFFFFE91 = $80); the rest of
DMAC, FRT, WDT, INTC, and DIVU state at handoff is open until
accessors are added.

### SMPC

Cold-boot path complete. INTBACK output registers (OREG0-OREG31) at
$20100021-$2010005F hold the result of the BIOS's last INTBACK call:

| OREG | Address | Value | Meaning |
|------|---------|-------|---------|
| OREG0 | $20100021 | $F1 | STE=1, RESD=1 (time-set, reset-disabled flags) |
| OREG1 | $20100023 | $02 | year hundreds (BCD) |
| OREG4 | $20100029 | $F1 | day of month |
| OREG5 | $2010002B | $02 | hours |
| OREG9 | $20100033 | (area code per disc; varies per autoDetect) | per-disc |
| OREG10 | $20100035 | $35 | system status 1 |
| OREG11 | $20100037 | $40 | system status 2 |
| OREG31 | $2010005F | $10 | last command echo = INTBACK |

Status register at $20100061 = $C0 (BUSY high bits). SF clear.

Boot commands executed (per disassembly): RESDISA ($1A) in Phase 6,
CKCHG320 ($0F) in Phase 6 cold-path (or CKCHG352 ($0E) on warm-path),
INTBACK ($10) in the system code at $001140 for region detection,
RESENAB ($19) in the system code at $001202. The slave SH-2 is held
in reset throughout boot because the BIOS never issues SSHON ($02);
SSHOFF ($03) is also not issued.

### SCU ($25FE0000)

Observed register values at handoff. Some of these (T0C, T1S,
ASR0/ASR1, AREF) match the doc's sub_1800 description; others
(IMS, DMA L0 in-flight state, RSEL, VER) differ from what the doc
inferred.

| Register | Address | Value | Description |
|----------|---------|-------|-------------|
| D0R | $25FE0000 | $260FC8FC | DMA L0 read addr (CD-block firmware end) |
| D0W | $25FE0004 | $25C0C8FC | DMA L0 write addr (VDP1 VRAM area) |
| D0C | $25FE0008 | $000008FC | DMA L0 transfer count |
| D0AD | $25FE000C | $00000101 | DMA L0 address add |
| D0EN | $25FE0010 | $00000101 | DMA L0 enable bit set (transfer was kicked off) |
| D0MD | $25FE0014 | $00000007 | DMA L0 mode |
| D1MD | $25FE0034 | $00000007 | DMA L1 mode |
| D2MD | $25FE0054 | $00000007 | DMA L2 mode |
| PDA | $25FE0088 | $00000003 | DSP program data address |
| T0C | $25FE0090 | $000003FF | Timer 0 compare = 1023 |
| T1S | $25FE0094 | $000001FF | Timer 1 set value = 511 |
| IMS | $25FE00A0 | $0000FFFF | All SCU interrupts MASKED |
| IST | $25FE00A4 | $00002007 | Interrupt status latches |
| AIACK | $25FE00A8 | $00000001 | A-Bus interrupt acknowledge |
| ASR0 | $25FE00B0 | $1FF01FF0 | A-Bus set 0 (A0EWT=1, A0BW=15, A0NW=15) |
| ASR1 | $25FE00B4 | $1FF01FF0 | A-Bus set 1 (A1EWT=1, A1BW=15, A1NW=15) |
| AREF | $25FE00B8 | $0000001F | A-Bus refresh = 31 |
| RSEL | $25FE00C4 | $00000001 | RAM select (set in Phase 3) |
| VER | $25FE00C8 | $00000004 | SCU version |

IMS at $FFFF means *all* SCU interrupts are masked at handoff. The IP
code is expected to install handlers and unmask what it needs.

### VDP1 ($25C00000 / $25D00000)

Captured values - all VDP1 user-programmable registers are zero at
handoff (matching the doc's TVMR/FBCR/EWDR/EWLR but disagreeing with
its PTMR=$0002 and EWRR=$50FF claims).

| Register | Offset | Value |
|----------|--------|-------|
| TVMR | $25D00000 | $0000 |
| FBCR | $25D00002 | $0000 |
| PTMR | $25D00004 | $0000 (not $0002 - auto-plot is OFF at handoff) |
| EWDR | $25D00006 | $0000 |
| EWLR | $25D00008 | $0000 |
| EWRR | $25D0000A | $0000 (no erase region set) |
| ENDR | $25D0000C | $0000 |
| EDSR | $25D0000E | $0000 |
| LOPR | $25D00010 | $0003 |
| COPR | $25D00012 | $0000 |
| MODR | $25D00014 | $0000 |
| (?) | $25D00016 | $1100 |

VDP1 VRAM and both framebuffers contain whatever the BIOS boot
animation drew into them and are byte-identical across both captures
(the BIOS produces deterministic output during its boot animation).

### VDP2 ($25E00000 / $25F80000)

Captured register state. Note BGON=$080C (NBG2 + NBG3 + RBG0 enabled),
not $000F as the sub_1408 table claims; this reflects the actual
post-boot-animation register state, not the initial-write state.

| Register | Offset | Value | Description |
|----------|--------|-------|-------------|
| TVMD | $25F80000 | $8000 | DISP = 1 (display enabled) |
| RAMCTL | $25F8000E | $0300 | Both banks coefficient table |
| CYCA0L | $25F80010 | $7744 | VRAM cycle pattern A0 low |
| CYCA0U | $25F80012 | $FFFF | A0 upper (no access) |
| CYCA1L | $25F80014 | $FF30 | A1 low |
| CYCA1U | $25F80016 | $FFFF | A1 upper |
| CYCB0L | $25F80018 | $6655 | B0 low |
| CYCB0U | $25F8001A | $FFFF | B0 upper |
| CYCB1L | $25F8001C | $21FF | B1 low |
| CYCB1U | $25F8001E | $FFFF | B1 upper |
| BGON | $25F80020 | $080C | NBG2 + NBG3 + RBG0 enabled |
| CHCTLA | $25F80028 | $1010 | NBG0/NBG1: 1-word, 16-color, 1x1 |
| CHCTLB | $25F8002A | $1022 | NBG2/NBG3 control |
| MPABN0 | $25F80040 | $0808 | NBG0 maps A/B |
| MPCDN0 | $25F80042 | $0808 | NBG0 maps C/D |
| MPABN1 | $25F80044 | $1818 | NBG1 maps A/B |
| MPCDN1 | $25F80046 | $1818 | NBG1 maps C/D |
| MPABN2 | $25F80048 | $1C1C | NBG2 maps A/B |
| MPCDN2 | $25F8004A | $1C1C | NBG2 maps C/D |
| MPABN3 | $25F8004C | $0C0C | NBG3 maps A/B |
| MPCDN3 | $25F8004E | $0C0C | NBG3 maps C/D |
| SCXIN0 | $25F80070 | $0008 | NBG0 X = 8 |
| SCYIN0 | $25F80074 | $0040 | NBG0 Y = 64 |
| ZMXIN0 | $25F80078 | $0001 | NBG0 zoom X = 1 |
| ZMYIN0 | $25F8007C | $0001 | NBG0 zoom Y = 1 |
| SCYIN1 | $25F80084 | $000A | NBG1 Y = 10 |
| ZMXIN1 | $25F80088 | $0001 | NBG1 zoom X |
| ZMYIN1 | $25F8008C | $0001 | NBG1 zoom Y |
| SCYN2 | $25F80092 | $000A | NBG2 Y = 10 |
| WPEX0 | $25F800C4 | $013F | Window 0 end X = 319 |
| WPEY0 | $25F800C6 | $00DF | Window 0 end Y = 223 |
| WPEX1 | $25F800CC | $013F | Window 1 end X = 319 |
| WPEY1 | $25F800CE | $00DF | Window 1 end Y = 223 |
| SPCTL | $25F800E0 | $0020 | Sprite type 0, VDP1 FB on |
| CRAOFA | $25F800E4 | $3210 | Color RAM offsets |
| (RBG0) | $25F800EC | $0042 | RBG0-related (not previously documented) |
| (RBG0) | $25F800F0 | $0007 | (not previously documented) |
| (RBG0) | $25F800FA | $0607 | (not previously documented) |
| (?) | $25F80108 | $0D00 | (not previously documented) |
| (?) | $25F80110 | $0008 | |
| (?) | $25F80112 | $0008 | |
| (?) | $25F8011A | $FFC0 | |
| (?) | $25F8011C | $FFC0 | |
| (?) | $25F8011E | $FFC0 | |

VDP2 VRAM and CRAM contain BIOS-boot-animation data and are
byte-identical across both captures.

### SCSP

Captured values via byte-by-byte register read. All registers and the
512 KB sound RAM are byte-identical across both captures. Sound RAM
contents are deterministic BIOS-driven data (not zeroed). m68k is
held in reset (SCSP reset bit set). Inspect `scsp_regs.bin` (3812 B)
and `sound_ram.bin` (512 KB) from a handoff dump for the exact bytes.

### CD Block ($25890000)

Disc-specific in part (TOC/sector state varies per disc). Observed
fields for NiGHTS (BIOS_USA):

| Register | Address | Value | Notes |
|----------|---------|-------|-------|
| DATATRNS | $25890000 | $0000 | No transfer in progress |
| DATASTAT | $25890004 | $0004 | FIFO empty |
| HIRQREQ | $25890008 | $05DD | CMOK + CSCT + BFUL + PEND + MPEG + ESEL + EHST + ECPY |
| HIRQMSK | $2589000C | $0000 | No mask |
| CR1 | $25890018 | $2380 | PAUSE status + flags |
| CR2 | $2589001C | $4101 | |
| CR3 | $25890020 | $0100 | |
| CR4 | $25890024 | $017D | |

The other captured disc has different CR/HIRQ values reflecting its
own TOC. CR1 high nibble = drive status (PAUSE for both at handoff).

### Work RAM-L ($00200000-$002FFFFF)

Entire 1 MB zeroed by the Phase 4 fill table. Byte-identical across
captures.

### Work RAM-H ($06000000-$060FFFFF)

99.87% byte-identical across both captures (1047209 / 1048576 bytes
match). The disc-specific regions that vary:

| Address range | Size | What it holds |
|---------------|------|---------------|
| $060002B1-$060002B2 | 2 B | Disc info byte |
| $06000C15-$06000C68 | 84 B | System ID strings copied from disc (maker, product, version, release date) |
| $06000CF1-$06000CF2 | 2 B | Paired cache byte |
| $06001DA0-$06001E4F | 176 B | Disc TOC / sector-address table |
| $06002000-$0600A000 | 32 KB | IP load buffer (the disc's IP contents) |

Annotated layout of the BIOS-set portion (these are static across
games):

| Address | Contents |
|---------|----------|
| $06000000-$0600020F | Interrupt vector table (VBR base, 528 B from BIOS $000600) |
| $06000200 | Version string "BTR_1.000U941115" |
| $06000234 | Pointer to fill routine ($000002AC) |
| $06000238 | Pointer to copy routine ($000002BC) |
| $0600023C | Pointer to copy data table ($00000350) |
| $06000248 | SMPC area code + display mode (16-bit, $8000 = reversed-J + no PAL on NiGHTS) |
| $0600024C | Boot state - observed value is $00000000 at the handoff capture point; not "HCDM" as previously inferred |
| $06000328 | SMPC init entry pointer ($000004C8) |
| $06000840-$0600094A | SCU interrupt trampoline stubs + common dispatcher (from BIOS $820 copy) |
| $06000A00-$06000A7F | SCU user-handler effective slots (SETUINT writes here for vectors $40-$5F; base addr $06000900 + vec*4) |
| $06000A80-$06000AFF | SCU priority/mask effective slots (base addr $06000980 + vec*4) |
| $06000980 | SCU priority/mask table (128 B) - populated by SYS_SETUINT at runtime |
| $06001100-$060017FF | System init code (1792 B from BIOS $001100) |
| $06002000 | IP+0 - "SEGA SEGASATURN " (System ID hardware identifier from disc) |
| $06002040 | IP+$40 - compatible area codes ("JTU       " for NiGHTS) |
| $06002100 | IP+$100 - security code (SYS_SEC.OBJ from disc); this is where master PC starts at handoff |
| $06010000-$0602FFFF | Decompressed CD-block firmware (from BIOS $01D000) |

### Backup RAM ($00180000)

Untouched by the BIOS at boot. Contents preserved from the previous
session (host save file for emulation). Filesystem layout is managed by
the backup library, which is loaded on demand by games via BUP_Init.


### BIOS-Reserved Work RAM-H Regions

The BIOS populates these Work RAM-H regions during boot. They are part
of the handoff state and must exist for game code to function:

| Address | Size | Populated By | Purpose |
|---------|------|--------------|---------|
| $06000000-$0600020F | 528 B | Phase 4 copy of BIOS $000600 (table entry at $0338, count=$84 longs) | Master SH-2 vector table (exception + SCU vectors) plus version string "BTR_1.000U941115" at the +$200 tail |
| $06000210-$0600021F | 16 B | Phase 4 copy of BIOS $000810 (table entry at $030C, count=4 longs) | Padding / unused (all zeros) |
| $06000220-$06000AFF | 2272 B | Phase 4 copy of BIOS $000820 (table entry at $0344, count=$238 longs) | SCU interrupt trampoline stubs at $06000840+, common dispatcher at $060008F4, plus areas later overwritten at runtime (handler-pointer table at $06000900, priority/mask table at $06000980) |
| $06000234-$0600023F | 12 B | Phase 5 (overwrites Phase 4 data) | Pointers: fill routine ($000002AC), copy routine ($000002BC), copy data table ($00000350) |
| $06000248 | 4 B | sub_1140 (region detect) | Cached SMPC area code + PAL/NTSC bit |
| $0600024C | 4 B | Phase 5 + boot state machine | Boot path indicator (R7 at boot, "HCGG"/"HCDM" later) |
| $06000328 | 4 B | Phase 5 | Pointer to BIOS $0004C8 (SMPC init reentry) |
| $06000348 | 4 B | runtime | NMI / reset status flag and SCU IST shadow |
| $06000400-$060005FF | 512 B | (not populated) | SDK convention reserves this as slave SH-2 VBR base, but the BIOS does NOT install slave vectors here. Slave vector setup is the disc's responsibility after SMPC SSH_ON. |
| $06000900-$0600097F | 128 B | Phase 4 area (overwrites copy) | SCU handler function-pointer table - 32 entries x 4 B indexed by vector. Populated at runtime by SYS_SETUINT; default entries point to $0600083C |
| $06000980-$060009FF | 128 B | Phase 4 copy | First half of SCU priority/mask table - 32 entries of `(SR_I:16 << 16) \| IMS_mask:16` indexed by vector. Read by the SCU dispatcher to set nesting priority |
| $06000A00-$06000A7F | 128 B | Phase 4 copy | Default-handler table - 32 entries x 4 B, all set to $0600083C (RTS+NOP). SYS_SETSINT looks here when caller passes a null handler |
| $06000A80-$06000AFF | 128 B | Phase 4 copy | Second half of SCU priority/mask table. Observed values: $00F0FFFF, $00E0FFFE, $00D0FFFC, $00C0FFF8, $00B0FFF0, $00A0FFE0, $0090FFC0, $0080FF80 (highest-priority vectors), then $0080FF80 / $0070FE00 repeated for lower-priority vectors |
| $06000B00-$06000BFF | 256 B | Phase 4 fill (entry $0330, count=$40 longs, R4=0) | Zeroed - SDK SYS_TASSEM/CLRSEM semaphore array (indices 0-255 in caller's R4) |
| $06000C00-$06000CFF | 256 B | Disc System ID copy (BIOS reads from disc at boot) | Cached System ID header: "SEGA SEGASATURN ", maker, product number ("MK-81020"), version, release date, device info ("CD-1/1"), compatible area codes ("JTU       "), peripherals, title. Plus pointers at $06000CE0 = $00001800 (SCU init) and $06000CF0 = $06004000 (runtime). This is the per-disc portion of the BIOS handoff state - varies per game. |
| $06000D00-$06000D0F | 16 B | Phase 4 fill (extension of $06000C00 fill) | Zeroed - the workspace buffer that $06000260 points to |
| $06001000-$060010FF | 256 B | Phase 4 fill | Zeroed - slave SH-2 initial stack region (slave R15 = $06001000 per SDK convention) |
| $06001100-$060017FF | 1792 B | Phase 4 copy of BIOS $001100 (table entry at $0350, count=$1C0 longs) | System init code that runs from RAM after Phase 5 jumps to $06000680 |

After boot, $06010000 holds whichever compressed body was last loaded
(CD Block firmware on the CD-game path, or boot library on the no-game
path). The IP loads at the fixed $06002000 buffer, and the BIOS always
hands off to its security-code entry at $06002100 (IP+$100). The game's
main binary (the 1st-read file) loads at the address given in System ID
$F0, which is the part that varies by disc. See the Work RAM-H layout
under "Key Addresses Summary" for the full map.

## VDP2 Initial Register State

The BIOS writes 137 register values to VDP2 during initialization via
sub_1408. Non-zero values are listed below (all unlisted registers are
set to $0000).

| Register | Offset | Value | Description |
|----------|--------|-------|-------------|
| RAMCTL | $000E | $0300 | VRAM access mode: both banks coefficient table |
| CYCA0L | $0010 | $7744 | VRAM cycle pattern A0 low |
| CYCA0U | $0012 | $FFFF | VRAM cycle pattern A0 upper (no access) |
| CYCA1L | $0014 | $FF30 | VRAM cycle pattern A1 low |
| CYCA1U | $0016 | $FFFF | VRAM cycle pattern A1 upper (no access) |
| CYCB0L | $0018 | $6655 | VRAM cycle pattern B0 low |
| CYCB0U | $001A | $FFFF | VRAM cycle pattern B0 upper (no access) |
| CYCB1L | $001C | $21FF | VRAM cycle pattern B1 low |
| CYCB1U | $001E | $FFFF | VRAM cycle pattern B1 upper (no access) |
| BGON | $0020 | $000F | All 4 scroll screens enabled (NBG0-NBG3) |
| CHCTLA | $0028 | $1010 | NBG0/NBG1: 1-word pattern, 16-color, 1x1 cells |
| CHCTLB | $002A | $1022 | NBG2: 1-word, 16-color; NBG3: 2-word, 256-color |
| MPABN0 | $0040 | $0808 | NBG0 map plane A/B = page 8 |
| MPCDN0 | $0042 | $0808 | NBG0 map plane C/D = page 8 |
| MPABN1 | $0044 | $1818 | NBG1 map plane A/B = page 24 |
| MPCDN1 | $0046 | $1818 | NBG1 map plane C/D = page 24 |
| MPABN2 | $0048 | $1C1C | NBG2 map plane A/B = page 28 |
| MPCDN2 | $004A | $1C1C | NBG2 map plane C/D = page 28 |
| MPABN3 | $004C | $0C0C | NBG3 map plane A/B = page 12 |
| MPCDN3 | $004E | $0C0C | NBG3 map plane C/D = page 12 |
| SCXIN0 | $0070 | $0008 | NBG0 scroll X = 8 |
| SCYIN0 | $0074 | $0040 | NBG0 scroll Y = 64 |
| ZMXIN0 | $0078 | $0001 | NBG0 zoom X integer = 1 (1:1) |
| ZMYIN0 | $007C | $0001 | NBG0 zoom Y integer = 1 (1:1) |
| SCYIN1 | $0084 | $000A | NBG1 scroll Y = 10 |
| ZMXIN1 | $0088 | $0001 | NBG1 zoom X integer = 1 (1:1) |
| ZMYIN1 | $008C | $0001 | NBG1 zoom Y integer = 1 (1:1) |
| SCYN2 | $0092 | $000A | NBG2 scroll Y = 10 |
| WPEX0 | $00C4 | $013F | Window 0 end X = 319 |
| WPEY0 | $00C6 | $00DF | Window 0 end Y = 223 |
| WPEX1 | $00CC | $013F | Window 1 end X = 319 |
| WPEY1 | $00CE | $00DF | Window 1 end Y = 223 |
| SPCTL | $00E0 | $0020 | Sprite type 0, VDP1 frame buffer enabled |
| CRAOFA | $00E4 | $3210 | Color RAM offset: NBG0=0, NBG1=1, NBG2=2, NBG3=3 |

The display is configured for 320x224 NTSC mode with 4 scroll screens
active. NBG0-NBG3 use separate VRAM pages (8, 24, 28, 12) and separate
color RAM banks (0-3). Window coordinates span the full 320x224 display.

The VRAM cycle patterns allocate access slots for pattern name and
character pattern reads across the 4 VRAM banks for all 4 scroll screens.


### Work RAM-H Layout (after init)

| Address | Size | Contents |
|---------|------|----------|
| $06000000 | 528 bytes | Interrupt vector table (VBR base) |
| $06000200 | 16 bytes | Version string "BTR_1.000U941115" |
| $06000210 | 16 bytes | Copied from BIOS $000810 |
| $06000224 | 4 bytes | SMPC reset flag |
| $06000228 | 4 bytes | Cartridge detect flag |
| $0600022C | 4 bytes | Drive-status nibble (0=Busy, 1=Pause, 7=NoDisc) / Set-Clock-screen trigger ($22 or $00 from OREG0/STE logic) |
| $06000234 | 4 bytes | Fill routine pointer |
| $06000238 | 4 bytes | Copy routine pointer |
| $0600023C | 4 bytes | Copy data table pointer |
| $06000240 | 4 bytes | "2RDY" handshake / region data |
| $06000248 | 4 bytes | SMPC area code + display mode |
| $0600024C | 4 bytes | Boot state ("HCGG"/"HCDM"/etc) |
| $06000254 | 4 bytes | Game entry function pointer |
| $06000264 | 4 bytes | IP.BIN source pointer |
| $06000268 | 4 bytes | Function-pointer slot (security-copy entry sub_1A18, per Game Load Pump usage) |
| $06000278 | 4 bytes | CD Block command state |
| $06000290 | 4 bytes | CD Block sector count |
| $060002A0 | 32 bytes | Disc header area codes |
| $060002B0 | varies | CD Block status buffer |
| $06000324 | 4 bytes | Cold/warm boot indicator |
| $06000328 | 4 bytes | SMPC init entry point ($0004C8) |
| $06000340 | varies | Game entry info (function ptr + params) |
| $06000348 | 4 bytes | NMI/reset status flag |
| $060003A0 | varies | CD Block session state |
| $06000690 | varies | System main loop (runtime populated) |
| $06000840-$0600094A | ~266 bytes | Interrupt handler pointer slots |
| $06000B00 | 256 bytes | Zeroed (cleared during init) |
| $06001100 | 1792 bytes | System init code (from BIOS $001100) |
| $06002000-$0600A000 | 32 KB total | Disc IP load buffer; the IP.BIN header occupies $06002000-$060020FF and SYS_SEC.OBJ begins at $06002100 (IP+$100). See ip_bin.md for the full header layout. |
| $06002000 | 16 bytes | "SEGA SEGASATURN " hardware ID (IP+0) |
| $06002040 | 10 bytes | Compatible area codes (IP+$40) - read by the sub_1BB4 region check |
| $06002100 | varies | IP security-code start (SYS_SEC.OBJ at IP+$100); master PC starts here at handoff. Also serves as the validation comparison buffer during boot. |


