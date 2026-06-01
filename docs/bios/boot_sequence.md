# Boot Sequence

What the BIOS does from power-on through the moment it hands off
to the disc's IP code. Phases 1-6 run from BIOS ROM at the start;
the system code copied to Work-RAM-H at `$06001120` then drives
SMPC INTBACK, VDP1/VDP2/SCU init, the boot state machine, and the
NMI / soft-reset paths. The CD-block-specific routines invoked
from this code are documented in
[cd_block_interface.md](cd_block_interface.md); the captured
end-state is in [handoff_state.md](handoff_state.md).

## Boot Entry Point ($000200)

The master SH-2 begins execution here after power-on reset. The
slave SH-2 also enters this address when SMPC SSHON releases it
from reset; both CPUs share the same boot entry but diverge at
the warm-boot check in Phase 3.

### Phase 1: Cache Initialization ($000380)

```python
def reset_phase_1():
    cache_init()                                # sub at $000380
    R7 = 0                                      # delay slot; used later in Phase 3 BRAF
```

Subroutine at $000380:
1. Writes 0 to CCR ($FFFFFE92) four times to disable and flush the cache
2. Purges all four cache ways by writing way-select values ($00, $40, $80,
   $C0) to CCR then zeroing 64 entries in the cache address (tag) array at
   $60000000 with stride 16 (one per cache line), invalidating those lines
3. Writes 1 to CCR to enable the cache

The cache address (tag) array at $60000000 is an SH-2 internal mapping for
direct cache manipulation (the cache data array is at $C0000000); it is not
Work RAM-H.

### Phase 2: BSC Initialization ($000478)

Source: SH7604 Hardware Manual (ADE-602-085C) Section 7, Appendix B

- **Pool**: $002D4 = $E0000000 (BRAF base); $002D8 = $FFFFFFE0 (BCR1 address).

```python
def reset_phase_2():
    bcr1 = mem.L[0xFFFFFFE0]                    # BCR1
    R7   = 0xE0000000                           # set up cache-flush BRAF base
    bsc_init()                                  # sub at $000478

    # T flag set from BCR1 bit 15 via SHLL16 + SHLL trick:
    # SHLL16 doesn't touch T; SHLL captures the post-SHLL16 high bit
    # (= original BCR1 bit 15) into T for the Phase 3 branch.
    T = (bcr1 >> 15) & 1
```

Subroutine at $000478:
1. Sets GBR = $FFFFFE00 (SH-2 on-chip register base)
2. Writes BSC (Bus State Controller) registers via GBR-relative stores.
   All writes use the $A55A write-protect key in bits 31-16.

| Address | Register | Value | Key | Description |
|---------|----------|-------|-----|-------------|
| $FFFFFFE0 | BCR1 | $03F1 | $A55A | Bus Control Register 1 |
| $FFFFFFE4 | BCR2 | $00FC | $A55A | Bus Control Register 2 |
| $FFFFFFE8 | WCR | $5555 | $A55A | Wait Control Register |
| $FFFFFFEC | MCR | $0078/$0070 | $A55A | Memory Control Register (T-dependent) |
| $FFFFFFF0 | RTCSR | $0008 | $A55A | Refresh Timer Control/Status |
| $FFFFFFF8 | RTCOR | $0036 | $A55A | Refresh Time Constant |

BCR1 = $03F1:
- AHLW=3 (area hold length wait: long), A1LW=3, A0LW=3
- DRAM2-0=001 (area 2 = ordinary space; area 3 = synchronous DRAM)

BCR2 = $00FC:
- A3SZ (bits 7-6) = 11 (area 3: 32-bit bus)
- A2SZ (bits 5-4) = 11 (area 2: 32-bit bus)
- A1SZ (bits 3-2) = 11 (area 1: 32-bit bus)
- Bits 1-0 reserved; BCR2 has no A0SZ field (area 0 bus size is fixed by
  the mode input pins)

WCR = $5555:
- All areas: IW=01 (1 idle cycle between areas), W=01 (external wait with 1 wait)

MCR = $0078 (cold boot) / $0070 (warm boot):
- SZ=1 (longword data size), AMX=011 (11-bit column DRAM)
- RMD=0 (normal refresh); RASD (bit 9) = 0 in both values
- The two values differ only in bit 3, RFSH (refresh control):
  cold boot sets RFSH=1 (refresh enabled); warm boot clears it.

On warm boot the routine writes only BCR1, BCR2, WCR, and MCR ($0070),
then returns; RTCSR and RTCOR are written on the cold path only (the
refresh timer is left running across the soft reset). The cold path
also writes a word of 0 to $FFFF8888 (not key-protected; not a
documented BSC register) before RTCSR/RTCOR.

RTCSR = $0008:
- CKS=001 (refresh clock = CLK/4), CMIE=0 (no compare match interrupt)

RTCOR = $0036 (54):
- Refresh interval constant = 54 clock counts

### Phase 3: Boot Path Selection ($000212)

```python
def reset_phase_3():
    if T:                                       # T from Phase 2: BCR1 bit 15
        warm_boot()                             # branch to $00022A
    else:
        # Cold boot: write SCU RSEL = 1, then BRAF cache-flush jump
        mem.L[0x25FE00C4] = 1                   # SCU RSEL
        branch_through(R7)                      # R7 = $E0000000
        # ... falls into Phase 4
```

The T flag was derived in Phase 2 from BCR1 bit 15 (the SHLL16 + SHLL
trick described above). It determines the boot path:

- **T=0 (cold boot)**: Writes 1 to SCU RSEL ($25FE00C4), then continues to
  Phase 4 (hardware initialization)
- **T=1 (warm boot)**: Jumps to $00022A, polls for slave SH-2 readiness

### Warm Boot Path ($00022A)

```python
def warm_boot():
    branch_through(0xE0000000)                  # BRAF cache-flush sentinel
    while True:
        mem.L[0x46000240] = 0                   # purge cache line via cache-write mirror
        if mem.L[0x06000240] == 0x32524459:     # "2RDY"
            break
        busy_wait(255)                          # DT R0 inner loop
    jump_to(0x06000600)                         # Work RAM-H slave/resume code
```

Polls Work RAM-H at $06000240 for the magic value "2RDY" ($32524459),
purging the cache line each attempt. "2RDY" is written to $06000240
elsewhere during boot; this loop purges the cache line on each attempt
so the poll observes that write once it lands rather than a stale
cached copy. When found, jumps to $06000600.

### Phase 4: Hardware Initialization ($00024C)

The cold boot path uses two helper subroutines and data tables to initialize
hardware registers:

**sub_02AC (fill routine)**:
Reads {count, dest_addr} from table pointed to by R0. Fills count longwords
at dest_addr with R4.

**sub_02BC (copy routine)**:
Reads {count, dest_addr, src_addr} from table pointed to by R0. Copies count
longwords from src_addr to dest_addr.

#### Initialization Sequence

The pool from $00030C to $000380 holds individual descriptor records
that Phase 4 hands to one of the two helper routines. Verified entries:

| Pool addr | Type | Count (longs) | Dest | Src | Purpose |
|-----------|------|---------------|------|-----|---------|
| $00030C | copy | 4 | $06000210 | $20000810 | 16 bytes from BIOS to WRAM-H |
| $000318 | fill | $40000 (262144) | $00200000 | R4=0 | Zero 1 MB of WRAM-L |
| $000320 | fill | $3D800 (251904) | $0600A000 | R4=0 | Zero ~984 KB of WRAM-H above the BIOS data region |
| $000328 | fill | $2500 (9472) | $06000C00 | R4=0 | Zero $9400 bytes of WRAM-H |
| $000330 | fill | $40 (64) | $06000B00 | R4=0 | Zero 256 bytes at $06000B00 |
| $000338 | copy | $84 (132) | $06000000 | $20000600 | 528 bytes - interrupt vector table |
| $000344 | copy | $238 (568) | $06000220 | $20000820 | 2272 bytes - handler pointer block |
| $000350 | copy | $1C0 (448) | $06001100 | $20001100 | 1792 bytes - system code |
| $00035C | copy | 8 | $060002C0 | $20001100 | 32 bytes |
| $000368 | copy | 8 | $060002A0 | $06000D00 | 32 bytes (WRAM-H to WRAM-H) |
| $000374 | copy | $10 (16) | $06000380 | $06000D20 | 64 bytes (WRAM-H to WRAM-H) |

Phase 4 dispatch code at $00024C calls the helpers (sub_02AC fill,
sub_02BC copy) multiple times, each pass advancing through the
descriptor pool. The copy at $000338 populates the SH-2 exception
vector table that VBR will later point to. The system code copy at
$000350 lands the routine that runs from $06001120 after Phase 6
completes.

### Phase 5: Function Pointer Setup ($00028E)

Phase 5 writes the function-pointer and entry-point values listed
below to their Work RAM-H slots, then jumps to `$06000680` to continue
execution from RAM.

| Address | Value Stored | Purpose |
|---------|-------------|---------|
| $06000234 | $000002AC | Fill routine pointer |
| $06000238 | $000002BC | Copy routine pointer |
| $0600023C | $00000350 | Copy data table pointer |
| $06000328 | $000004C8 | SMPC/system init entry |
| $0600024C | R7 (boot state) | Boot path flag |

Then jumps to $06000680 (in Work RAM-H) to continue execution from RAM.

### Phase 6: SMPC Initialization ($0004C8)

This code runs from BIOS ROM (Work RAM-H slot $06000328 stores its
address as an entry point used by the NMI handler and by game code
that wants to re-init SMPC). The routine performs SMPC bring-up
plus master/slave reset orchestration.

- **Pool** (BIOS $0057E..$005C3, sampled values used by this routine):

| Offset | Value | Purpose |
|--------|-------|---------|
| $0580 | $00F0 | SR mask-all value |
| $0582 | $8000 | SH-2 on-chip register write value |
| $0586 | $0400 | cache-walk iteration count |
| $0588 | $0400 | cache-walk stride |
| $058C | $FFFFFE80 | SH-2 WDT WTCSR / WTCNT address |
| $0590 | $FFFFFE91 | SBYCR (Standby Control Register) address |
| $0594 | $A55A007C | BSC MCR write value (key + value) |
| $059C | $FFFFFEE0 | SH-2 on-chip register |
| $05A0 | $FFFFFFEC | BSC MCR address |
| $05A8 | $20000000 | cache-through BIOS base (VBR target) |
| $05AC | $2010001F | SMPC COMREG |
| $05B0 | $20100063 | SMPC SF |
| $05B4 | $25FE00A8 | SCU AIACK |
| $05BC | $06000324 | boot-flag destination |
| $05C0 | $26000000 | cache-through WRAM-H base |

```python
def smpc_init():                                # BIOS $0004C8
    saved_sr = SR
    SR = 0x000000F0                             # mask all interrupts

    # Acquire SMPC and issue RESDISA ($1A) to disable reset-button NMI
    mem.B[0x20100063] = 1                       # SF = 1 (acquire)
    mem.B[0x2010001F] = 0x1A                    # COMREG = RESDISA
    while True:
        busy_wait(70)                           # 70-count delay precedes every SF check
        if not (mem.B[0x20100063] & 1):         # SF bit 0 clear = command complete
            break

    # Cache-line walk over $26000000: 1024 longwords at $0400 stride
    addr = 0x26000000
    for _ in range(0x400):
        _ = mem.L[addr]
        addr += 0x400

    # Save the cold/warm boot indicator (caller's R4 bit 0)
    boot_flag = caller_R4 & 1
    mem.L[0x06000324] = boot_flag
    cold_boot = (boot_flag == 0)

    # Clear SCU AIACK / AREF
    mem.L[0x25FE00A8] = 0                       # SCU AIACK
    mem.L[0x25FE00B8] = 0                       # SCU AREF (AIACK+$10)

    # Reprogram VBR, BSC MCR/CHCR, WDT, SH-2 on-chip register
    VBR               = 0x20000000              # cache-through BIOS
    mem.L[0xFFFFFFEC] = 0xA55A007C              # BSC MCR (key + value)
    mem.B[0xFFFFFE91] = 0x80                    # SBYCR (set SBY standby bit)
    mem.W[0xFFFFFE80] = 0xA51D                  # WDT (key + value)
    mem.W[0xFFFFFEE0] = 0x8000                  # SH-2 on-chip register

    # Issue clock-change SMPC command:
    #   cold boot -> CKCHG320 ($0F), warm boot -> CKCHG352 ($0E)
    # CKCHG triggers a soft reset; control re-enters via the reset vector,
    # not via SLEEP wake. The infinite loop is a safety net.
    mem.B[0x2010001F] = 0x0F if cold_boot else 0x0E
    sleep()
    while True:
        pass                                    # $000530 safety hang
```

Step-level summary:

1. Save old SR in R6, set SR = $00F0 (mask all interrupts).
2. Acquire SMPC by writing 1 to SF ($20100063), then issue
   RESDISA ($1A) via COMREG ($2010001F) to disable the reset button
   NMI.
3. Poll SF bit 0 in a loop, re-running a ~70-iteration delay before
   each check, until it clears (command complete).
4. Loop-read 1024 longwords from cache-through WRAM-H base
   $26000000 in $0400 increments — walks the cache-line addresses to
   drain/flush.
5. Save the T flag (cold-vs-warm boot indicator) to $06000324.
6. Clear SCU AIACK ($25FE00A8) and SCU AREF ($25FE00B8); set VBR =
   $20000000 (cache-through BIOS); rewrite BSC MCR ($FFFFFFEC) = $007C
   (key $A55A); set SBYCR ($FFFFFE91) = $80 (SBY standby bit); write
   $A51D to the WDT region ($FFFFFE80) and $8000 to the SH-2
   on-chip register at $FFFFFEE0.
7. Issue a clock-change SMPC command — CKCHG320 ($0F) on cold boot
   or CKCHG352 ($0E) on warm boot — selected via the T flag
   arithmetic (`MOVT R0; ADD #14,R0` produces $0E + T, with T
   inverted earlier so cold gives $0F and warm gives $0E).
8. SLEEP, then infinite loop at $000530.

The CKCHG command triggers a system clock change that on Saturn is
observed as a soft reset — execution re-enters via the reset vector,
not by a wake from SLEEP. The infinite loop at $000530 is a safety
net for the (unexpected) case where SLEEP returns. The code at
$000534 is reached only via the NMI vector ($00002C = $20000534),
the common case being an NMI during the SLEEP at $00052E while VBR
still points at the BIOS. It is the NMI handler documented in the
NMI Handler ($000534, ROM vector) section below; it does not fall
through from $000530.

The pointer slot at $06000328 publishes $000004C8 (this Phase 6
entry point) for use by the NMI handler at $000420 (which JSRs
through it on soft reset) and by any game code that wants to drive
a SMPC re-init. Re-running it mid-game re-masks interrupts
(SR=$00F0), resets VBR to the cache-through BIOS, and re-issues
RESDISA + CKCHG.


## System Code ($001120, runs from $06001120)

The 1792 bytes copied to $06001100 contain the main system initialization
that runs after the SMPC init wakes the master SH-2. The first $20 bytes
($1100-$111F) are data, not code. Entry point is $001120 (runs at
$06001120).

### SMPC INTBACK and Region Detection ($001120-$0011A4)

Source: SMPC User's Manual (ST-169-R1) Section 2.4 INTBACK, Section 3.1

#### SMPC Register Map (byte-access only, odd addresses)

| Address | Register | R/W | Description |
|---------|----------|-----|-------------|
| $2010001F | COMREG | W | Command register |
| $20100061 | SR | R | Status register |
| $20100063 | SF | R/W | Status flag (bit 0) |
| $20100001 | IREG0 | W | Input register 0 |
| $20100003 | IREG1 | W | Input register 1 |
| $20100005 | IREG2 | W | Input register 2 |
| $20100021 | OREG0 | R | Output register 0 |
| $20100033 | OREG9 | R | Output register 9 (area code) |
| $2010005F | OREG31 | R | Output register 31 (command echo) |
| $20100075 | PDR1 | R/W | Peripheral data register 1 |
| $20100077 | PDR2 | R/W | Peripheral data register 2 |
| $20100079 | DDR1 | W | Data direction register 1 |
| $2010007B | DDR2 | W | Data direction register 2 |

#### INTBACK Command Parameters (as issued by BIOS)

The BIOS issues INTBACK in status-only mode (no peripheral data):

| Register | Value | Meaning |
|----------|-------|---------|
| SF | $01 | Acquire SMPC (set before command) |
| IREG0 | $01 | Return status data (time, area code, etc.) |
| IREG1 | $02 | PEN=0 (no peripheral data), OPE=1 (no optimize) |
| IREG2 | $F0 | Required constant |
| COMREG | $10 | INTBACK command code |

Both sub_1D38 and the inline code at $001140 use identical parameters.
sub_1D38 includes a delay loop of $36EE80 (3,600,000) iterations before
issuing the command to allow SMPC to be ready after power-on. The loop body
is DT + BF (about four SH-2 cycles per iteration), so the delay is on the
order of 14M cycles, not 3.6M.

#### INTBACK OREG Response Layout

When IREG0=$01 (status mode), the SMPC returns system information in
OREG0-OREG15:

| OREG | Address | Content |
|------|---------|---------|
| OREG0 | $20100021 | [STE:1][RESD:1][0:6] - reset/time flags |
| OREG1 | $20100023 | Year 1000s/100s place (BCD) |
| OREG2 | $20100025 | Year 10s/1s place (BCD) |
| OREG3 | $20100027 | [day_of_week:4][month:4] (hex, not BCD) |
| OREG4 | $20100029 | Day of month (BCD) |
| OREG5 | $2010002B | Hours (BCD) |
| OREG6 | $2010002D | Minutes (BCD) |
| OREG7 | $2010002F | Seconds (BCD) |
| OREG8 | $20100031 | [0:6][CTG1:1][CTG0:1] - cartridge code |
| OREG9 | $20100033 | Area code ($00-$0F) |
| OREG10 | $20100035 | System status 1 (DOTSEL, signal states) |
| OREG11 | $20100037 | System status 2 (CDRES) |
| OREG12-15 | $20100039-$3F | SMEM saved data (4 bytes) |
| OREG31 | $2010005F | Command code echo ($10 for INTBACK) |

OREG0 flags:
- STE (bit 7): 1 = SETTIME done after cold reset, 0 = not done (first boot)
- RESD (bit 6): 1 = reset disabled, 0 = reset enabled

OREG9 area codes:

| Code | Region | Countries |
|------|--------|-----------|
| $01 | Japan | Japan |
| $02 | Asia NTSC | Taiwan, Philippines |
| $04 | North America | USA, Canada, Mexico |
| $05 | Central/S. America NTSC | Brazil |
| $06 | Korea | South Korea |
| $0A | Asia PAL | East Asia, China, Middle East |
| $0C | Europe PAL | Europe, Australia, South Africa |
| $0D | Central/S. America PAL | Latin America |

OREG10 System Status 1 bits (0=OFF, 1=ON):
- bit 7: 0B signal
- bit 6: DOTSEL (0=320 mode, 1=352 mode)
- bit 5: 1B signal
- bit 4: 1B signal
- bit 3: MSHNMI signal
- bit 2: 1B signal
- bit 1: SYSRES signal
- bit 0: SNDRES signal

#### BIOS OREG Parsing ($001120-$0011A4)

The BIOS reads only two OREG values during boot:

**1. OREG9 ($20100033) - Area Code**

Read at $00116A. The BIOS calculates the OREG9 address as IREG0 base
($20100001) + 50 ($32) = $20100033.

The low 4 bits are extracted via a bit-reversal loop (4 iterations of
SHAR R3 + ROTCL R0). This reverses the bit order of the nibble:

| SMPC Area Code | Binary | Reversed | Decimal |
|----------------|--------|----------|---------|
| $01 (Japan) | 0001 | 1000 | 8 |
| $02 (Asia NTSC) | 0010 | 0100 | 4 |
| $04 (N. America) | 0100 | 0010 | 2 |
| $05 (Brazil) | 0101 | 1010 | 10 |
| $06 (Korea) | 0110 | 0110 | 6 |
| $0A (Asia PAL) | 1010 | 0101 | 5 |
| $0C (Europe PAL) | 1100 | 0011 | 3 |
| $0D (Latin America) | 1101 | 1011 | 11 |

The reversed nibble is then shifted left 12 bits (SHLL8 + SHLL2 + SHLL2)
and OR'd with the VDP2 PAL flag (bit 11). The result is stored as a
16-bit value to $06000248:

```
$06000248 = [reversed_area:4][pal:1][0:11]
```

The region lookup table at $001C40 is indexed by the original SMPC
area code value (not the reversed nibble) and holds the ASCII letter
at each populated slot:

| Index | ASCII | Region |
|-------|-------|--------|
| 1 | J ($4A) | Japan |
| 2 | T ($54) | Taiwan/Philippines |
| 4 | U ($55) | USA/Canada |
| 5 | B ($42) | Brazil |
| 6 | K ($4B) | Korea |
| 10 | A ($41) | Asia PAL |
| 12 | E ($45) | Europe |
| 13 | L ($4C) | Latin America |

The OREG9 parsing loop produces the bit-reversed nibble (8, 4, 2, 10,
6, 5, 3, 11 respectively for the eight regions above) and stores it
in $06000248 bits 15-12 along with the PAL flag at bit 11. The table
lookup itself uses the original area code value, not the reversed
form stored in $06000248.

**2. OREG0 ($20100021) - STE Flag**

Read at $001192 as byte at IREG0 base + $20 = $20100021. Bit 7 (STE)
is tested:
- STE=0 (time not set after cold reset): stores $22 to $0600022C
  (triggers Set Clock screen display)
- STE=1 (time already set): stores 0 to $0600022C (skip clock setup)

The inverted STE flag (T from TST) is stored to $06000224 as the cold
reset indicator.

**3. VDP2 PAL/NTSC Detection**

Before INTBACK, the BIOS reads VDP2 TVSTAT ($25F80004) bit 0:
- Bit 0 = 0: NTSC mode
- Bit 0 = 1: PAL mode

This is combined with the area code in $06000248 bit 11.

#### SMPC Commands Used During Boot

| Command | Code | When | Purpose |
|---------|------|------|---------|
| RESDISA | $1A | Phase 6 ($0004C8) | Disable reset-button NMI |
| CKCHG320 | $0F | Phase 6 ($0004C8) | Clock change to 320 dot mode (cold boot) |
| CKCHG352 | $0E | Phase 6 ($0004C8) | Clock change to 352 dot mode (warm boot) |
| INTBACK | $10 | System code ($001140) | System status (area code, time) for region detection |
| RESENAB | $19 | System code ($001202) | Enable reset-button NMI |

Phase 6 selects CKCHG320 or CKCHG352 based on the cold/warm boot
flag. Standard SMPC command codes: MSHON=$00, SSHON=$02, SSHOFF=$03,
SNDON=$06, SNDOFF=$07, CDON=$08, CDOFF=$09, SYSRES=$0D, CKCHG352=$0E,
CKCHG320=$0F, INTBACK=$10, SETTIME=$16, SETSMEM=$17, NMIREQ=$18,
RESENAB=$19, RESDISA=$1A.

### VDP1 Initialization (sub_13C0)

Sets GBR = $25D00000 (VDP1 register base), then writes VDP1 registers:

| GBR Offset | Register | Value |
|------------|----------|-------|
| +$00 | TVMR | $0000 |
| +$02 | FBCR | $0000 |
| +$04 | PTMR | $0002 |
| +$06 | EWDR | $0000 |
| +$08 | EWLR | $0000 |
| +$0A | EWRR | $50FF or $50DF (PAL-dependent) |

Also reads $06000248 bit 11 to determine erase region (EWRR).
Writes $8000 to $25C00000 (VDP1 VRAM command - end code).

### VDP2 Register Initialization (sub_1408)

Writes 137 ($89) register values to VDP2 at $25F80000. Register indices
and values are stored in tables at $0600154A (register index bytes) and
$06001438 (register values as 16-bit words). The loop reads a byte index,
doubles it for the register offset, and writes the corresponding value.

### SCU Initialization (sub_1800)

Sets GBR = $25FE0000 (SCU register base), then:

1. Zeros DMA Level 0/1/2 set registers (5 longs each, 3 levels); this
   clears each level's enable register (DnEN, offset $10), leaving DMA disabled
2. Writes 7 to each level's mode register (DnMD, offset $14:
   read/write address-update and DMA start-factor select)
3. Zeros DMA force-stop ($60) and the DSP Program Control Port ($80)
   (the DMA status register is at $7C and is read-only)
4. Sets A-Bus registers: ASR0 ($B0) = $1FF01FF0, ASR1 ($B4) = $1FF01FF0
5. Sets A-Bus refresh: AREF ($B8) = $1F
6. Writes 1 to A-Bus interrupt acknowledge: AIACK ($A8) = 1
7. Timer 0: T0C ($90) = $03FF
8. Timer 1: T1S ($94) = $01FF
9. Timer 1 mode: T1MD ($98) = 0
10. Loads game entry info from $06000340 (function table)
11. Sets VBR = $06000000 (Work RAM-H)
12. Jumps to game via function pointer from $06000340

The interrupt mask register (IMS at $A0) is not written by this
routine; the handoff IMS value is established elsewhere.

### Header Validation (sub_1A3C)

Validates disc/cartridge headers against "SEGA SEGASATURN " (16 bytes).

1. Checks that the data size (R6) is between $1000 and $8000 bytes
2. Reads 16 bytes from source and compares with expected header at $001B04
3. On match, copies header data:
   - 8 longs (32 bytes) from disc header offset $E0 to $060002A0
   - 64 longs (256 bytes) from $06002000 to a buffer at $06000C00
4. Compares $340 longwords (3328 bytes) of the loaded IP at $06002100
   against a BIOS reference at $20006200; on mismatch returns -4
   without doing the region lookup
5. Reads the stored value at $06000248, shifts it right 12 bits
   (SHLR8 + SHLR2 + SHLR2) to isolate the reversed area nibble
   (bits 15-12), then bit-reverses it (4x SHLR/ROTCL) to recover the
   original SMPC area code
6. Looks up ASCII region code from table at $001C40 using the area code as index
7. Looks up region string pointer from table at $001C50

### Region Check (sub_1BB4-$1C2E)

Reads the disc's area code field (10 characters from $06002040) and
compares each character against the console's region code (from SMPC
area code lookup):

Valid area code characters: J, T, U, B, K, A, E, L, space (terminator)

| Index | ASCII | Region |
|-------|-------|--------|
| 1 | J ($4A) | Japan |
| 2 | T ($54) | Taiwan/Philippines |
| 4 | U ($55) | USA/Canada |
| 5 | B ($42) | Brazil |
| 6 | K ($4B) | Korea |
| 10 | A ($41) | Asia PAL |
| 12 | E ($45) | Europe |
| 13 | L ($4C) | Latin America |

Region string pointers (in BIOS ROM, cache-through):

| Pointer | String |
|---------|--------|
| $20006F00 | "For JAPAN." |
| $20006F20 | "For TAIWAN and PHILIPINES." |
| $20006F40 | "For USA and CANADA." |
| $20006F60 | "For BRAZIL." |
| $20006F80 | "For KOREA." |
| $20006FA0 | "For ASIA PAL area." |
| $20006FC0 | "For EUROPE." |
| $20006FE0 | "For LATIN AMERICA." |

After region match, performs a security check by comparing 8 longwords
(32 bytes) from offset $E00 in the loaded IP header data (address
$06002E00) against known values. Returns 0 on success, negative on failure.

### Boot State Machine ($001296-$001332)

The main system code at $06001120 ends with a state machine that controls
the boot flow. Key states identified by magic values at $0600024C:

| Value | ASCII | Meaning |
|-------|-------|---------|
| $48454D55 | "HEMU" | Already initialized; jump directly to main loop ($06000690) |
| $48434747 | "HCGG" | Game cartridge detected |
| $4843444D | "HCDM" | CD game detected |

The state machine:
1. Checks boot state at $0600024C
2. If "HEMU": jumps to $06000690 (already initialized; main loop)
3. If "HCGG": jumps to sub_18E8 for cartridge game start
4. Calls sub_15D4 (CD Block init), then re-reads the boot state
5. If "HCDM": jumps to sub_1A18, which block-copies into the IP load
   window and jumps to $06020000 (the loaded game)
6. Otherwise: JSRs sub_1A62 (cartridge header validation, reading
   $22000000)
7. Falls through to sub_173C (decompress ROM boot-library data via
   sub_1F04 and run it at $06010000) and sub_1756 (zero the WRAM-H
   scratch at $06010000 and $060F0000), then jumps to $06000690
   (system main loop)

### NMI Handler ($000420)

The Work RAM VBR NMI vector points to $000420 in BIOS ROM:

1. Adjusts stack (+8 to discard exception frame)
2. Calls indirect through $000005D8 pool (function pointer)
3. Writes $FFFFFFFF (longword) to $06000348 (status flag)
4. Calls $0004C8 (SMPC init) to reinitialize
5. Branches back into the boot flow at $000252 (Phase 4 continuation)

This effectively performs a soft reset through the BIOS when NMI fires.

### NMI Handler ($000534, ROM vector)

The SH-2 NMI vector at $002C holds $20000534 (cache-through alias
of $000534), so this is the NMI handler. The handler does not load any SMPC
command code or addresses from its own pool; instead it operates on
the register state preserved from the code that was running when NMI
fired. The common firing context is the Phase 6 SLEEP at $00052E,
where the registers were left holding:
R1=$FFFFFE91 (SBYCR), R2=$FFFFFFEC (BSC MCR), R3=$FFFFFEE0
(SH-2 on-chip), R5=$2010001F (SMPC COMREG), R6 = old SR.

In that context the handler's sequence is:

1. Adjust stack +8 to discard the exception frame.
2. R4 = $20100063 (SMPC SF) from pool; write 1 to SF (acquire SMPC).
3. Write 0 byte to *R5 (in Phase 6 context that targets COMREG).
4. Poll word at *R3 (in Phase 6 context $FFFFFEE0) until the signed
   value goes negative (bit 15 set).
5. Write 0 byte to *R1 (in Phase 6 context, SBYCR = 0).
6. Load $A55A0078 from pool and store it to *R2 (in Phase 6 context,
   BSC MCR = $0078 with $A55A key).
7. Write 1 (word) to *R3 ($FFFFFEE0 in Phase 6 context).
8. Write 1 to SCU RSEL ($25FE00C4).
9. Read 1024 longwords from $26000000 in $0400 increments
   (cache-line walk, same idiom as Phase 6).
10. Read the boot indicator from $06000324.
11. Wait for bit 2 of VDP2 TVSTAT ($25F80004) to be set.
12. Write the boot-indicator word to VDP2 TVMD ($25F80000).
13. Write $81 to FRT TIER ($FFFFFE10).
14. Restore SR from R6 (set at Phase 6 entry) and RTS.


## Boot Flow Summary

```
Power ON / Reset
       |
       v
Phase 1: Cache purge and enable ($000380)
       |
       v
Phase 2: BSC bus timing init ($000478)
       |
       v
Phase 3: Cold/warm boot detection
       |
       +-- Warm: poll "2RDY" at $06000240, jump to Work RAM-H
       |
       v (Cold)
Phase 4: Hardware init via copy/fill tables
       - Zero Work RAM-L (1 MB)
       - Copy vector table to Work RAM-H
       - Copy system code to Work RAM-H
       |
       v
Phase 5: Store function pointers, jump to Work RAM-H
       |
       v
Phase 6: SMPC init ($0004C8)
       - RESDISA command (disable reset-button NMI)
       - Cache-line flush walk over WRAM-H
       - Set VBR, BSC MCR/CHCR, SCU AIACK/AREF, WDT
       - CKCHG320 / CKCHG352 command (forces soft reset)
       - SLEEP (control re-enters via reset vector)
       |
       v
System code ($001120 in Work RAM-H)
       - SMPC INTBACK for region detection
       - VDP1/VDP2/SCU initialization
       |
       v
Boot state machine
       |
       +-- CD Block init (sub_15D4)
       |     - SCSP init
       |     - CD Block communication setup
       |     - Disc detection
       |
       +-- Header validation ("SEGA SEGASATURN ")
       |
       +-- Region check (SMPC area code vs disc area codes)
       |
       +-- Security check / auth stub (4 KB from BIOS ROM $040000
       |   copied to $06020000 and executed; jumps to $06028000)
       |     - Copyright string comparison
       |     - SMPC authentication sequence
       |
       +-- On success: master dispatches into the IP load window
       |   $06002000-$0600A000. Entry PC is game-specific (auth path's
       |   hardcoded $06028000 is one possibility but not always used).
       |
       +-- On failure: CD multiplayer (audio CD player)
```


