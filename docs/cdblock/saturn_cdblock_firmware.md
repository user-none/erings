# Saturn CD Block Firmware Reference (CDB-106)

Firmware image: cdb106.bin (CDB-106, 64 KB)
SHA256: 2dfb3618b5b612bb49abbf0d1d8d53016908bb81aa8c92515f0836203e0b7f1c

The firmware is 65536 bytes (64KB) and runs on the SH-1 (SH7034) processor
inside the Saturn's CD block subsystem. Meaningful ROM content ends around
$FAC0; the remainder is $FF fill. Code examples in this document are Python
pseudocode derived from the disassembly; addresses refer to the ROM image
and the SH-1 address space.

Coverage: the boot path, RTOS kernel, interrupt handlers, host command
dispatch, drive link protocol, sector pipeline, status reporting, and
command workers are traced. The following are described at survey depth
or identified but not fully instruction-traced, because they run only
with a Video CD/MPEG cartridge or are secondary: the MPEG command-handler
bodies ($C450-$F712, reached through the extension dispatch table), the
task 2 decoder-event state machine ($5E78), task 13 copy/move ($6FD8),
and the per-task RTOS parameter blocks ($14E8). The ISO 9660 directory
reader and disc-header validation are described from their entry points
and data structures rather than a full parse trace.

## Firmware Identification

| Offset | Content |
|--------|---------|
| $000400 | `"Copyright (C) Hitachi, Ltd. 1993"` (33 bytes, quote characters included in ROM) |
| $001F48 | 8 constant bytes `00 'C' 'D' 'B' 'L' 'O' 'C' 'K'` - boot signature written to CR1-CR4 |
| $008A3C | `SEGA SEGASATURN ` (disc header validation) |
| $008D04 | `SEGASYSTEM` (disc header validation) |
| $0099F4 | `Hitachi.PublicKeyCipher` (authentication crypto) |
| $00BCD0 | `SEGA` (string constant near the authentication code; specific use not traced) |


## SH-1 Memory Map

| Address Range | Size | Description |
|---------------|------|-------------|
| $00000000-$0000FFFF | 64KB | Firmware ROM (this file) |
| $05FFFE00-$05FFFFFF | 512B | SH7034 on-chip peripheral registers |
| $09000000-$0907FFFF | 512KB | CD buffer DRAM (sector buffers + firmware work areas) |
| $0A000000-$0A00001F | 32B | Host interface bridge registers (SH-1 side) |
| $0A100000+ | - | MPEG decoder LSI A interface (Video CD cartridge) |
| $0A180000+ | - | MPEG decoder LSI B interface (Video CD cartridge) |
| $0E000000+ | - | Extension ROM window (MPEG/Video CD cartridge image source) |
| $0F000000-$0F000FFF | 4KB | On-chip RAM (all runtime state) |

The 512KB at $09000000 is ordinary DRAM: the boot self-test walks and
pattern-fills the entire $09000000-$0907FFFF range, and the DRAM refresh
controller of the SH-1 BSC is configured for it. The firmware reserves the
top of this DRAM for its own structures; everything below is sector buffer
space managed per partition:

| DRAM Address | Purpose |
|--------------|---------|
| $09000000-$090001FF | Disc info: session block, TOC image, first/last track (see CD Drive Link) |
| $09000218/$09000224 | Double-buffered presentation record published for the host |
| $090752A0-$090753E7 | MPEG subsystem work area (cleared by MPEG comm reset) |
| $090752E0 | MPEG parameter word (init value 20) |
| $09075320 | Copy of MPEG LSI A config words |
| $09075374 | Extension command CR save area (written by hook $B538) |
| $0907537B | Extension command busy flag (bit 0 checked before accepting) |
| $09075388 | Extension request flag byte polled by task 9 |
| $09075800-$090758xx | Per-task resume-hook table (long per task ID, overrides resume PC) |
| $0907B000-$0907FFFF | Extension image area (loaded from $0E000000) |
| $0907B008 | Extension dispatch table (function pointer per index 0-88) |

The area $09075800-$09077BFF is cleared during boot init. The $0907B000
area is cleared unless a valid extension image loads (see Extension
Mechanism below).

### SH7034 On-Chip Peripheral Use

The firmware addresses on-chip registers GBR-relative from three bases:

| GBR Base | Registers reached |
|----------|-------------------|
| $05FFFEC0 | SCI0, SCI1, A/D converter |
| $05FFFF00 | ITU (all 5 channels), DMAC (all 4 channels) |
| $05FFFF80 | INTC, UBC, BSC/refresh, WDT, standby, ports A/B/C, TPC |

Peripheral assignment as used by this firmware:

- SCI0: synchronous serial link to the CD drive mechanism controller
  (12-byte command/status frames; see CD Drive Link).
- SCI1: transmit-only diagnostic output (factory test mode).
- DMAC0: incoming CD sector data, $0A000018 port to buffer DRAM.
- DMAC1: buffer DRAM to/from host data FIFO at $0A000000.
- DMAC2: 4-byte MPEG command packets, $0F000840 to LSI A ($0A10001E).
- DMAC3: 4-byte MPEG command packets, $0F000840 to LSI B ($0A180000).
- ITU0/ITU1/ITU2: input capture units (GRA falling edge; ITU2 also GRB)
  wired to MPEG cartridge signals. ITU0/ITU1 flags are polled; ITU2
  capture interrupts are vectors 88/89.
- ITU3: GRB compare match (vector 93) is the drive link byte-slot clock;
  TIOR3/TIER3 are armed by the report task at startup.
- ITU4: free-running time base; overflow increments a counter at
  $0F00021C; compare match interrupts are vectors 96/97.

### Host Interface Registers ($0A000000, SH-1 side)

The host interface bridge exposes the SH-2 facing registers to the SH-1.
Observed usage:

| SH-1 Address | Usage |
|--------------|-------|
| $0A000000 | Host data FIFO (repeated word reads/writes stream data; DATATRNS counterpart) |
| $0A000002 | Transfer control (observed values 1, 2, 3, 4, 8; bit 0 selects read/write direction of the sector transfer engine) |
| $0A000004 | Command-pending status. Vector 70 acks by writing 0; report publisher skips CR writes while bit 1 is set |
| $0A000006 | Decoder event status (bits 4 and 5 serviced by vector 71; ack by writing the mask of unaffected bits) |
| $0A000008 | HIRQ (read/write; boot self-test verifies with patterns $0003/$0000) |
| $0A00000A | Event interrupt mask for $0A000006 (boot self-test uses pattern $0070) |
| $0A00000C | Written 3 during init |
| $0A000010 | CR1 (read = command word from host, write = response word to host) |
| $0A000012 | CR2 (same dual behavior) |
| $0A000014 | CR3 (same dual behavior) |
| $0A000016 | CR4 (same dual behavior) |
| $0A000018 | CD sector data port (DMAC0 source) |
| $0A00001A | Sector status; bit 7 = sector pending, cleared by handler; init writes $1100 or $0000 depending on port C bit 0 |
| $0A00001C | Sector arrival status word (stored into the sector state block) |
| $0A00001E | HIRQ assert port. Observed writes: $0BE1 at boot, $0001 on command completion (CMOK), $0400 on periodic report (SCDQ) |

The exact relationship between $0A000008 and the $0A00001E assert port has
not been validated beyond the observed write patterns.

SH-2 side correspondence (A-Bus CS2): HIRQ = $05890008,
HIRQMASK = $0589000C, CR1-CR4 = $05890018/$0589001C/$05890020/$05890024.
The data transfer FIFO (DATATRNS) is at a different CS2 offset,
$05818000 (offset $18000), not in the $05890000 command-register block.
The command/response CR registers are separate latches at the same
offsets: the SH-1 reads what the SH-2 wrote and writes what the SH-2 will
read.


## ROM Vector Table ($000000-$0001C7)

114 vectors of 4 bytes. Vector numbers and sources follow the SH7034
hardware manual (section 5, table 5.3).

### CPU Exception Vectors

| Vector | Value | Purpose |
|--------|-------|---------|
| 0/1 | $00000428 / $0F001000 | Power-on PC / SP |
| 2/3 | $00000428 / $0F001000 | Manual reset PC / SP |
| 4 | $00000944 | General illegal instruction -> warm reset |
| 6 | $0000094A | Slot illegal instruction -> RTE (ignore) |
| 9 | $00000950 | CPU address error -> RTE (ignore) |
| 10 | $00000956 | DMA address error -> reset DMAC, continue |
| 11 | $000009C4 | NMI -> RTE (ignore) |
| 12 | $000009CA | User break -> warm reset |
| 5, 7, 8, 13-31 | $000009E2 | Reserved -> RTE (ignore) |

### TRAPA Vectors (RTOS system calls)

| TRAPA | Handler | Function |
|-------|---------|----------|
| 32 | $00000EF4 | Call task (rendezvous send, caller blocks for reply) |
| 33 | $00000F82 | Send message (deliver + wake target if waiting) |
| 34 | $00000FC8 | Send message + requeue target on a ready queue |
| 35 | $00000E90 | Wait for message (message returned in R11) |
| 36 | $00001DA6 | Start software timer: post message R11 after R10 ticks |
| 37 | $00001E56 | Cancel a software timer |
| 38 | $0000122E | Try-acquire lock (non-blocking, TAS on TCB+2) |
| 39 | $00001260 | Release lock (hand off to first waiter, yield) |
| 40 | $00000E6C | Create/start task (ID in R11 bits 15-8) |
| 41 | $00000F54 | Reply to caller (payload R12/R13), block self |
| 42 | $000012E2 | Acquire lock (blocking, waiter chain on TCB+12) |
| 43 | $00001C08 | Error report - handler is a no-op RTE (code in R0 discarded) |
| 44 | $00001C0E | No-op RTE |
| 45 | $00001EBC | Start software timer from interrupt context |
| 46 | $00000DA4 | Soft-restart the RTOS with the reduced task set |
| 47-63 | $000009E2 | Unused |

### Software Timers (TRAPA 36/37/45, vectors 96/97)

The kernel keeps a small sorted list of pending timers, each an
{absolute-tick, message-word} pair, timed against ITU4. TRAPA 36 inserts
a timer due R10 ticks from now (R10 is scaled by a fixed factor before
insertion) carrying message R11; TRAPA 45 is the same from an interrupt
handler (it queues into a staging slot the next TRAPA 36/scheduler pass
folds in). TRAPA 37 removes a timer. ITU4 compare-match (vectors 96 at
$1CA0, 97 at $1D48) fires when the front timer's tick is reached: it
delivers that timer's message via the normal delivery path and reloads
the compare register for the next entry. This is what wakes tasks after
timed drive operations and sector-read pacing (the sector read handler's
TRAPA 45 in vector 72 schedules the next read this way).

### Interrupt Vectors

| Vector | Source | Handler | Role |
|--------|--------|---------|------|
| 64-69 | IRQ0-IRQ5 | $0009DC | Unused -> warm reset |
| 70 | IRQ6 | $001F50 | Host command written (reads CR1-CR4, dispatches, responds) |
| 71 | IRQ7 | $005BEC | CD decoder events (status $0A000006 bits 4/5) |
| 72 | DMAC0 DEI0 | $005B48 | CD sector arrival DMA complete |
| 74 | DMAC1 DEI1 | $0024A2 | Host FIFO sector transfer DMA complete |
| 76 | DMAC2 DEI2 | $009CF4 | MPEG LSI A packet DMA complete |
| 78 | DMAC3 DEI3 | $00B4CC | MPEG LSI B packet DMA complete |
| 88 | ITU2 IMIA2 | $00B862 | MPEG cart event (calls extension hook 56) |
| 89 | ITU2 IMIB2 | $00A1A0 | MPEG cart status change (decodes LSI A status word) |
| 93 | ITU3 IMIB3 | $00974C | Drive link byte-slot clock (frame TX/RX engine) |
| 96 | ITU4 IMIA4 | $001CA0 | Software timer expiry (delivers due timer messages) |
| 97 | ITU4 IMIB4 | $001D48 | Software timer expiry (secondary compare) |
| 98 | ITU4 OVI4 | $001C80 | Time base overflow: increment counter at $0F00021C |
| 101 | SCI0 RxI0 | $00988C | Drive response byte received |
| 104-107 | SCI1 | $0009D6 | Unused -> warm reset |
| 80-86, 90, 92, 94, 100, 102, 103, 108, 109, 112, 113 | various | $0009DC | Unused -> warm reset |
| 73, 75, 77, 79, 83, 87, 91, 95, 99, 110, 111 | reserved | $0009E2 | RTE (ignore) |

Boot-time interrupt priorities ($54C): IPRA = 0, IPRB = 0,
IPRC = $8600 (DMAC0/1 = 8, DMAC2/3 = 6), IPRD = $6666
(ITU2 = ITU3 = ITU4 = SCI0 = 6), IPRE = 0, ICR = 0. IRQ6 is raised to
priority 6 (IPRB |= $0060) when the boot signature is published.

### DMA Address Error Handler ($000956)

```python
def dma_address_error():
    sr = 0xF0
    DMAOR = 0
    CHCR0 = CHCR1 = CHCR2 = CHCR3 = 0x0040
    DMAOR = 0x0001
    write16(0x0A000002, 8)
    rte()
```


## Boot Sequence ($000428)

### Phase 1: Core and Peripheral Initialization ($0428-$046A)

```python
def boot():
    SR = 0xF0
    VBR = 0
    clear_ram()                    # $534: longs $0F000000-$0F000FFF = 0
    GBR = 0x05FFFF80
    init_intc()                    # $54C: IPRA/B=0, IPRC=$8600, IPRD=$6666, IPRE=0, ICR=0
    clear_ubc()                    # $568: BARH/BARL, BAMRH/BAMRL, BBR = 0
    init_wdt()                     # $5A8: TCNT=0, TCSR=$18, RSTCSR cleared then $1F
    init_standby()                 # $5C8: RSTCSR/standby area = $0000001F
    init_bsc_refresh()             # $574: BCR:WCR1=$8000FFFF, WCR2:WCR3=$FFFF9800,
                                   #       DCR:PCR=$75000000, RCR=$80, RTCNT=$80,
                                   #       RTCOR=$80, RTCSR=$00
    init_tpc()                     # $5FA: TPMR=$F0, TPCR=$FF, NDERA/B=0, NDRA/B=0
    GBR = 0x05FFFEC0
    init_sci()                     # $610: SCI0 and SCI1: SMR=$80, BRR=99, SCR=0
    init_adc()                     # $62C: ADCSR=$0E, ADCR=$7F
    GBR = 0x05FFFF00
    init_itu()                     # $638: see below
    init_dmac()                    # $6BC: DMAOR=0, CHCR0-3=$0040, DMAOR=1
    GBR = 0x05FFFF80
```

The order above is the exact BSR sequence at $438-$466 (WDT and standby
are configured before the bus controller).

ITU init ($638): TSTR = $E0 (all counters stopped), TSNC = $E0, TMDR = 0,
port A data |= $C0, TOCR = $FF, all five TCNT = 0, all GRA/GRB (and ITU3/4
BRA/BRB) = $FFFF, all TCR = 0, all TIOR = $08, all TSR flags cleared
(write $F8), all TIER = $F8 (interrupts disabled).

### Phase 2: Hardware Self-Test ($046C-$04BA)

```python
def self_test_phase():
    init_ports()                       # $5D0 (at $46C): PADR=$F67B, PBDR=0,
                                       #   PAIOR=$5BF6, PBIOR=$3AF0, PACR1=$FF22,
                                       #   PACR2=$BF1A, PBCR1=$5A8A, PBCR2=$008A,
                                       #   CASCR=$AFFF (routes CD-block pin functions)
    for attempt in range(3):
        init_host_interface()          # $7E0: PADR bit15 set; $0A000002=2 then 8;
                                       # HIRQ/$0A00000A/$0A000004/$0A000006 = 0;
                                       # $0A00001A = $1100 if port C bit0 clear else 0;
                                       # $0A00000C = 3
        init_dram()                    # $708: delay $190 loops; RTCSR=$08 (refresh on);
                                       # touch a word every $200 through the 512KB
        err = buffer_ram_test()        # $72E: address-line walk, byte patterns
                                       # $AA/$55/$33/$CC, then full-array fill/verify
                                       # with $AA5555AA, $55AAAA55, $00000000
        write8(0x0F00027D, 0x80 if err else 0)
        write16(0x0F000000, err & 0xFFFF)
        write16(0x0F000002, err >> 16)
        if host_if_dma_test() != 0:    # $81A: HIRQ pattern test; DMAC1 loopback of
                                       # $AA5555AA from $09000000 into the host FIFO
                                       # (CHCR1=$1258|DE), read back through
                                       # $0A000000 and compare
            write8(0x0F00027D, read8(0x0F00027D) | 0x80)
        if read8(0x0F00027D) & 0x80 == 0:
            break
```

### Phase 3: Mechanism Init and Extra-Hardware Detection ($04BC-$04FC)

```python
def mpeg_cart_phase():
    init_mpeg_interfaces()         # $A374: $0A10001A=$7FFF, $0A100002=$006C,
                                   # $0A10001E=0, $0A180000=0, capture units armed
                                   # (via $A470), DMAC2/3 programmed
                                   # (SAR=$0F000840, DAR=$0A10001E/$0A180000, TCR=2,
                                   # CHCR2=$090D, CHCR3=$080D)
    checksum_rom()                 # $6F0: sum of all ROM longs -> $0F000004
    for _ in range(120000):        # constant $0001D4C0
        pass
    cart, hw_id = detect_mpeg_cart()   # $A3C8, see below
    if cart != 0:
        write8(0x0F00027D, read8(0x0F00027D) | 0x02)
    write16(0x0F000002, read16(0x0F000002) | ((hw_id & 0xFF) << 4))
    reserved_check()               # $8FC: stub, always returns 0
```

MPEG cartridge detection ($A3C8) accumulates a bitmask: bit 0 if
$0A100002 no longer reads back $006C, bits 1-3 from the ITU0/1/2 input
capture flags (whether the cartridge signal lines produced capture
events). It ends by shutting the capture units back down ($A4A0). A
nonzero result sets bit 1 of $0F00027D, which is required for the MPEG
command ranges to dispatch.

### Phase 4: Mode Select ($04FE-$050E)

```python
def mode_select():
    if read8(0x05FFFFC2) & 0x04:   # port B data, bit 2 strap
        normal_boot()              # $908
    else:
        factory_test_mode()        # $9E8
```

### Hardware status byte $0F00027D

| Bit | Meaning |
|-----|---------|
| 1 | MPEG/Video CD hardware detected (gates command ranges $90-$9F, $A0-$AF) |
| 7 | Buffer RAM or host interface self-test failed |

Detailed self-test error bits are kept in the words at $0F000000 and
$0F000002. Get Hardware Info returns the $0F00027D byte to the host in
CR2.


## Factory Test Mode ($0009E8)

Entered when port B bit 2 is strapped low. Not a CD authentication path;
it is a serial diagnostic monitor:

```python
def factory_test_mode():
    SCR1 |= 0x20                       # SCI1 transmit enable
    TDR1 = read32(0x0F000004) & 0xFF   # transmit ROM checksum byte
    SSR1 &= 0x7F
    delay(2857142)                     # constant $2B98B6
    summary = 0
    if read16(0x0F000000) != 0:
        summary |= 0x01
    summary |= read32(0x0F000000) & 0x0F
    high = read32(0x0F000000) >> 4
    if high & 0x05: summary |= 0x10
    if high & 0x02: summary |= 0x20
    if high & 0x08: summary |= 0x40
    if high & 0x10: summary |= 0x80
    TDR1 = summary                     # transmit self-test summary byte
    SSR1 &= 0x7F
    wait_port_b_bit2(timeout=200000)
    SP = 0x0F001000
    SMR1 = 0
    SCR1 = 0x30
    BRR1 = 65
    while True:                        # serial command monitor
        monitor_step_1()               # $ABC
        monitor_step_2()               # $B20
        monitor_step_3()               # $B48
        monitor_step_4()               # $B90
```


## Normal Boot ($000908)

```python
def normal_boot():
    init_command_state()       # $BA44: clear word $0F000786, long $0F0007AC,
                               #        word $0F0007B0
    init_drive_state()         # $77B8: clear word $0F000786
    init_buffer_manager()      # $19E4: calls $1AE6 (below), then clears longs
                               #        $0F000208-$0F00021C
    init_transfer_engine()     # $2398: CHCR1=$1258, $0A000002=8, $0A00000C=3,
                               #        clear $0F000248 block, sector index
                               #        $0F000258 = $FF (idle)
    init_timebase()            # $1C14: ITU4: TCR4=3, TIOR4=0, TIER4=4 (overflow
                               #        interrupt), TSTR |= $10 (start); clear
                               #        counter $0F00021C and blocks $0F000220/$0F000240
    rtos_start()               # $D5C
```

Buffer manager init ($1AE6): VBR = 0, clears the work area
$09075800-$09077BFF in buffer DRAM, clears the byte at $0F0002FF.


## RTOS

The kernel is a priority-scheduled cooperative multitasking system with
message passing, rendezvous calls, and per-TCB locks. All services are
TRAPA calls. Interrupt handlers convert hardware events into messages.

### Message Word Encoding

Most services take a message word in R11:

| Bits | Field |
|------|-------|
| 31-24 | Destination task ID |
| 23-16 | Source code (task ID, or event code with bit 7 set) |
| 15-0 | Payload |

Examples seen in interrupt handlers: $05840010 (to task 5, event $84,
payload $10) on sector arrival; $08890008 / $088A000C (to task 8, events
$89/$8A) on MPEG packet DMA completion; $06830000 (to task 6, event $83)
on drive response byte.

Delivery ($11CE): each task has a ROM parameter block (pointer table at
$14B0). The block holds message-slot pointers indexed by the source code:
task-id sources (bit 7 clear) at offset code*4, event codes (bit 7 set)
at offset (code & 127)*4. The message word is stored into the addressed
RAM slot and the destination TCB's pending count (+7) increments. Wake-up
appends the destination TCB to its ready queue. Bit-7-clear (rendezvous)
sources are additionally gated on the destination's lock-owner and task
id ($11EA-$11F6).

### Task Control Blocks

TCBs live in on-chip RAM. Each is a 24-byte header directly followed by
that task's message slot array (variable length):

| Offset | Size | Purpose |
|--------|------|---------|
| 0 | 1 | Task ID |
| 1 | 1 | State (see below) |
| 2 | 1 | Lock byte (TAS target) |
| 3 | 1 | Lock owner code |
| 4 | 1 | Wait filter byte A (last matched source) |
| 5 | 1 | Wait filter byte B |
| 6 | 1 | Ready-queue index while queued (ID*4, or $80) |
| 7 | 1 | Pending message count |
| 8 | 4 | Saved SP |
| 12 | 4 | Lock waiter chain head |
| 16 | 4 | Next-TCB link (ready queue / wait chains) |
| 20 | 4 | Rendezvous partner TCB pointer |
| 24 | 4 | Reply payload for R12 (doubles as message slot 0) |
| 28 | 4 | Reply payload for R13 (doubles as message slot 1) |

Task states and how the scheduler resumes each (handler table at $1368):

| State | Meaning | Resume action |
|-------|---------|---------------|
| 0 | Newly created | RTE (launches entry point pushed at create) |
| 1 | Waiting for message | Deliver pending message in R11, then RTE |
| 2 | Blocked in rendezvous call | Pop reply into R12/R13, then RTE |
| 3 | Blocked after reply | RTE |
| 4 | Preempted (full context on stack) | Full register restore, then RTE |
| 5 | Running | - |
| 6 | Blocked on lock | RTE |

State-1 resume ($1140) first checks the resume-hook table: if the long at
$09075800 + ID*4 is nonzero, it replaces the saved PC, letting extension
code intercept a task's message loop.

### Task Roster

Created at $D5C in the order 1, 2, 4, 12, 13, 7, 9, 8, 5, 6, 14, 11, 3
(byte table at $1350). Runtime priority is separate: each task maps to one
of 12 ready-queue heads at $0F0001D8-$0F000204 (ROM map at $13FC), scanned
lowest slot first:

Priority order (highest first): 4, 6, 5, 12, 7, 8, 2, 1, 13, 9, 14.

| ID | TCB | Stack top | Entry | Role |
|----|-----|-----------|-------|------|
| 1 | $0F000008 | $0F000CB0 | $63EC | Sector post-processing (waits for event from task 5, payload $10) |
| 2 | $0F000024 | $0F000D38 | $5E78 | Decoder event processing (event codes $80, $81, ...) |
| 3 | $0F000048 | - | - | Lock object for the report task (TRAPA 42 target) |
| 4 | $0F000060 | $0F000C28 | $5708 | Filter/selector manager (executes selector commands under the filter lock) |
| 5 | $0F000084 | $0F000BA0 | $45A4 | Sector arrival processing (receives event $84 from DEI0) |
| 6 | $0F0000B4 | $0F000A90 | $2728 | CD status report task (GBR = $0F00025C region) |
| 7 | $0F0000E8 | $0F000B18 | $77C2 | Drive control state machine |
| 8 | $0F000114 | $0F000DE0 | $A020 | MPEG LSI communication (idle without cartridge) |
| 9 | $0F000148 | $0F000E88 | $BA56 | Deferred host command work + extension hook polling |
| 11 | $0F000168 | - | - | Lock object for the filter tables (TRAPA 42 target) |
| 12 | $0F000180 | $0F000F10 | $69A0 | Data transfer service |
| 13 | $0F0001A0 | $0F000F98 | $6FD8 | Sector copy/move worker (event codes $65/$66) |
| 14 | $0F0001BC | $0F001000 | $1BF0 | Idle task (TCB shared with ID 10) |

Other kernel RAM:

| Address | Purpose |
|---------|---------|
| $0F0001D4 | Current task pointer |
| $0F0001D8-$0F000207 | 12 ready-queue heads (priority slots) |
| $0F000208-$0F00021B | Buffer manager state |
| $0F00021C | ITU4 overflow counter |
| $0F0008F8 | Interrupt stack (vectors 70, 76, 78, 101) |
| $0F000950 | Interrupt stack (vector 71) |
| $0F0009A8 | Interrupt stack (vector 72) |
| $0F0009F8 | Interrupt stack (vector 74) |

Task creation ($1068): the TCB is reset, the per-task private RAM block
(bounds at parameter block offset +108) is cleared, and an initial frame
of SR = $0020 and the ROM entry point (table at $13C0) is pushed on the
task's stack (stack tops at $1384) so state-0 resume launches it.

### Scheduler

```python
def pick_next():                       # $10B8
    for head in ready_queue_heads:     # 12 slots at $0F0001D8, priority order
        if head != 0:
            tcb = head
            unlink(tcb)
            return tcb
    error(6)                           # TRAPA 43 (no-op)
    return idle_tcb                    # $0F0001BC

def dispatch(tcb):                     # $1128
    old_state = tcb.state
    tcb.state = 5
    tcb.queue_index = 0
    current_task = tcb
    SP = tcb.saved_sp
    resume_handler[old_state]()        # table at $1368

def yield_from_interrupt():            # $103E, tail of interrupt handlers
    current.queue_index = current.id * 4
    current.state = 4
    append(ready_queue_of(current), current)
    dispatch(pick_next())
```

The idle task publishes the boot signature once, then loops:

```python
def idle_task():                       # $1BF0
    publish_boot_signature()           # $1F0C, see below
    while True:
        pass
```

Boot signature ($1F0C):

```python
def publish_boot_signature():
    write16(0x0A000004, 0)
    IPRB |= 0x0060                     # IRQ6 (host command) priority 6
    write16(0x0A000008, 0x0001)        # HIRQ CMOK
    write16(0x0A00001E, 0x0BE1)
    write16(0x0A000010, 0x0043)        # CR1 = '\0C'
    write16(0x0A000012, 0x4442)        # CR2 = 'DB'
    write16(0x0A000014, 0x4C4F)        # CR3 = 'LO'
    write16(0x0A000016, 0x434B)        # CR4 = 'CK'
```


## Host Command Processing

Commands are processed synchronously inside the IRQ6 interrupt handler.
There is no command task in the main path; deferred work is delegated by
message.

### Vector 70 Flow ($1F50)

```python
def irq6_host_command():
    save_full_context()
    current.saved_sp = SP
    SP = 0x0F0008F8
    write16(0x0A000004, 0)                       # ack
    cr12 = read16(0x0A000010) << 16 | read16(0x0A000012)
    cr34 = read16(0x0A000014) << 16 | read16(0x0A000016)
    cmd = cr12 >> 24
    if read8(0x0F0007B0) & 1 and cmd != 0x05:    # drive link down
        msg, resp12, resp34 = reject(cr12, cr34)
    else:
        handler = nibble_table[cmd >> 4]         # 16 entries at $1FF0
        msg, resp12, resp34 = handler(cmd, cr12, cr34)
    write16(0x0A000010, resp12 >> 16)
    write16(0x0A000012, resp12 & 0xFFFF)
    write16(0x0A000014, resp34 >> 16)
    write16(0x0A000016, resp34 & 0xFFFF)
    write16(0x0A00001E, 0x0001)                  # HIRQ CMOK
    if msg != 0:
        trapa33_send(msg)
    yield_from_interrupt()
```

Handlers receive the command CRs in R1/R2 and return the response CR1:CR2
in R12, CR3:CR4 in R13, and an optional message word in R11.

### First-Level Dispatch

Each nibble handler validates the command code range, then jumps through a
per-nibble table. Out-of-range codes go to the reject responder.

| Code | Command | Handler |
|------|---------|---------|
| $00 | Get Status | $9200 |
| $01 | Get Hardware Info | $9284 |
| $02 | Get TOC | $22E6 (transfer wrapper for $92A2) |
| $03 | Get Session Info | $92BC |
| $04 | Initialize CD System | $9320 |
| $05 | Open Tray | $935A |
| $06 | End Data Transfer | $22DC |
| $10 | Play Disc | $9360 |
| $11 | Seek Disc | $9400 |
| $12 | Scan Disc | $942A |
| $20 | Get Subcode Q/RW | $22EC (transfer wrapper for $9440) |
| $30 | Set CD Device Connection | $4D78 |
| $31 | Get CD Device Connection | $4D9E |
| $32 | Get Last Buffer Destination | $4DA6 |
| $40 | Set Filter Range | $7390 |
| $41 | Get Filter Range | $73A6 |
| $42 | Set Filter Subheader Conditions | $73D4 |
| $43 | Get Filter Subheader Conditions | $73EA |
| $44 | Set Filter Mode | $742C |
| $45 | Get Filter Mode | $7442 |
| $46 | Set Filter Connection | $746E |
| $47 | Get Filter Connection | $74C2 |
| $48 | Reset Selector | $7522 |
| $50 | Get Buffer Size | $7542 |
| $51 | Get Sector Number | $75A0 |
| $52 | Calculate Actual Size | $75C4 |
| $53 | Get Actual Size | $75E4 |
| $54 | Get Sector Info | $75F0 |
| $55 | Execute FAD Search | $767E |
| $56 | Get FAD Search Results | $76C8 |
| $60 | Set Sector Length | $755E |
| $61 | Get Sector Data | $22F2 (transfer wrapper for $68AC) |
| $62 | Delete Sector Data | $68CA |
| $63 | Get Then Delete Sector Data | $22F8 (transfer wrapper for $68AC) |
| $64 | Put Sector Data | $22FE (transfer wrapper for $68E0) |
| $65 | Copy Sector Data | $6F0C |
| $66 | Move Sector Data | $6F0C (shared with $65) |
| $67 | Get Copy Error | $6F48 |
| $70 | Change Directory | $9488 |
| $71 | Read Directory | $9534 |
| $72 | Get File System Scope | $9576 |
| $73 | Get File Info | $2304 (transfer wrapper for $95C8) |
| $74 | Read File | $9628 |
| $75 | Abort File | $96D2 |
| $90-$9F | MPEG commands | extension table, except $93 -> $A060 |
| $A0-$AF | MPEG commands | extension table, except $AE -> $A158, $AF -> $A100 |
| $E0 | Authenticate Device | $C2C6 |
| $E1 | Is Device Authenticated | $C3AA |
| $E2 | Get MPEG Card Boot ROM | $C344 |
| $FF | Debug/echo (sub-codes $00, $01) | $235C |

The $E0/$E1 handlers carry two subcommands: subcommand 0 acts on the
disc (via the drive task 7), subcommand 1 on the MPEG card (via task 9).

Nibble values 8, B, C, D and all codes outside the ranges above reject.

Valid code ranges enforced: $00-$06, $10-$12, $20, $30-$32, $40-$48,
$50-$56, $60-$67, $70-$75, $90-$9F, $A0-$AF, $E0-$E2, $FF.

Gating: command ranges $90-$9F and $A0-$AF require bit 1 of $0F00027D
(MPEG hardware present) and bit 7 of $0F000892, except $93 which skips the
$0F000892 check. MPEG commands other than $93/$AE/$AF dispatch through the
extension table at $0907B008 with slot = (code & $3F) - 16, so $90 ->
slot 0 through $AF -> slot 31.

### Responses

```python
def reject(cr12, cr34):                # $22B6 -> $91F8
    r12, r34 = current_report()
    return 0, or_status_byte(r12, 0xFF), r34   # status byte forced to $FF

def reject_busy(cr12, cr34):           # $91FC
    r12, r34 = current_report()
    return 0, or_status_byte(r12, 0x80), r34   # REJECT flag over current status

def get_status(cr12, cr34):            # $9200
    return 0, *current_report()

def current_report():                  # $9210
    if read8(0x0F0007B0) & 1:          # drive link down
        return 0x00FFFFFF, 0xFFFFFFFF  # status BUSY, all position fields invalid
    bank = 0x0F000274 if read8(0x0F00027C) else 0x0F00026C
    cr12, cr34 = read32(bank), read32(bank + 4)
    if state.flags74 & 0x28:           # drive command in flight
        cr12 &= 0x00FFFFFF             # status -> BUSY (0)
    elif state.flags75 & 0x08:
        cr12 = (cr12 & 0xF0000000) | 0x09FFFFFF   # status ERROR, keep flag nibble
        cr34 = 0xFFFFFFFF
    return cr12, cr34
```

Get Hardware Info returns CR2 = (hardware status byte $0F00027D) << 8 | 2
(CD block version 2) and CR3:CR4 = the long at $0F0002FC (extension image
identification).

### Disc Operation Commands ($92A2-$945C)

The disc operation workers validate parameters, store them into the
report task state block, and send it a message ($06810000 + payload).
The report task performs the actual work; the immediate response is the
current report (status possibly ORed with $40, the transfer-request
marker).

| Command | Worker | Actions | Payload sent |
|---------|--------|---------|--------------|
| Get TOC $02 | $92A2 | rejects if drive state 0; CR2 = 204 (words); status \| $40 | 0 |
| Get Session Info $03 | $92BC | reads the session block at $09000000 (byte 0 = count, byte n = first track of session n); session 0 returns the long at $09000064; session n returns first-track TOC entry as track << 24 \| (FAD - 150); empty entry falls back to [$09000214] << 8 | none |
| Initialize CD System $04 | $9320 | parameter bytes not equal to $FF are stored: init flags -> +0, standby word -> +4, ECC -> +6, retry -> +7 | 0 |
| Open Tray $05 | $935A | message only | 0 |
| Play Disc $10 | $9360 | play mode byte -> +1; start -> +8, end -> +12 (masked to 24 bits; $FFFFFF start/end resume from the working copies at +60/+64); mixed track/FAD designation rejects | 3 (track-designated) or 4 (FAD or default) |
| Seek Disc $11 | $9400 | target -> +8 (designation flag stripped) | 6 (track), 7 (FAD), 8 (target $FFFFFF = pause), 9 (target 0 = stop) |
| Scan Disc $12 | $942A | direction byte 0/1, else reject | 10 or 11 |
| Get Subcode Q $20 | $9440 | subtype 0: CR2 = 5 words, status \| $40, same tail as Get TOC; subtype 1 (R-W) jumps to $6364; else reject | 0 |

Report-task handlers for these payloads (source $81 table): 0 = $2D16,
3 = $2DD8, 4 = $3014, 6 = $3094, 7 = $30B0, 8 = $30C8, 9 = $3188,
10 = $3218, 11 = $3260. Payloads 1/2/5/12/13 are produced by other
(internal) senders.

### Data Transfer Commands

Commands that stream through the host FIFO (Get TOC, Get Subcode Q, Get
Sector Data, Get Then Delete Sector Data, Put Sector Data, Get File Info)
are wrapped ($230A): the wrapper refuses the command if the byte at
$0F00025B (transfer in progress) is set, otherwise runs the handler and
sets the flag when the response is not a reject and its status byte has
bit 6 set (the data-transfer-request marker the workers OR in via the
$91B4 responder tail). End Data Transfer ($06) clears the flag and
finalizes/starts the pending DMA via $2578.

The transfer engine itself is driven by vector 74 (DMAC1 completion):

```python
def dei1_fifo_transfer_done():         # $24A2
    CHCR1 &= 0xF8                      # disable/ack channel
    state = block_0F000256             # +1 count, +2 buffer index
    if state.count == 0:
        write16(0x0A000002, (read16(0x0A000002) & 1) | 8)
        return
    if state.index == 0xFF:
        write8(abort_flag, 0xFF)
        state.count = 0
        write16(0x0A000002, (read16(0x0A000002) & 1) | 8)
        return
    if read16(0x0A000002) & 1 == 0:    # bit 0 clear: buffer -> host (read)
        if read8(read32(0x0F00024C)) != 0x88:   # transfer-valid tag via pointer
            abort()
        addr, length = next_read_chunk()    # $6E28
        SAR1, TCR1, CHCR1 = addr, length, 0x125D
    else:                              # bit 0 set: host -> buffer (write)
        if read8(read32(0x0F00024C)) != 0x88:
            abort()
        addr, length = next_write_chunk()   # $6E86
        DAR1, TCR1, CHCR1 = addr, length, 0x435D
    state.index = chain_table[state.index]  # next buffer in partition chain
    state.count -= 1
    total_transferred += length
```

The chain table maps buffer index to next buffer index with $FF as
terminator, implementing the buffer partition linked lists.

Task 5 owns the arrival side: at startup it raises IRQ7 (decoder events)
to priority 8 (IPRB low nibble), allocates two buffers ($4EA8, ROM
dispatch index 95) and queues both into the DEI0 arrival queue at
$0F000314+12/+16 ($4BBE), then enters its message-wait loop (TRAPA 35).

### Buffer, Filter, and Partition Structures

Sector buffers live in DRAM at $09000230 + index*2352 for indices 0-199.
Incoming sectors are DMA'd as 2340 bytes (header, subheader, data, no
sync field) to buffer offset +12, so the 4-byte header (min/sec/frame/
mode) sits at +12 and mode-2 subheader at +16. The chunk calculators
($6E28 read, $6E86 write) resolve host transfer addresses and lengths
from the get/put sector-size codes at $0F0006D8/$0F0006D9:

| Size code | Bytes | Buffer offset |
|-----------|-------|---------------|
| 0 | 2048 (mode 1), 2048/2324 (mode 2 form 1/2 by submode bit 5) | +16 / +24 |
| 1 | 2336 | +16 |
| 2 | 2340 | +12 |
| 3 | 2352 | +0 |

On-chip RAM structures:

| Address | Purpose |
|---------|---------|
| $0F00034A | Free buffer count (200 max) |
| $0F00034B | Buffer-wait flag: $FF while the sector pipeline is stalled on an empty free list (see buffer exhaustion below) |
| $0F000354-$0F00041B | Buffer chain table: next buffer index per buffer, $FF = end |
| $0F000420-$0F00059F | 24 filter entries, 16 bytes each |
| $0F000600-$0F00068F | 24 partition entries, 6 bytes each (byte +2 = sector count) |
| $0F0006BC-$0F0006D9 | Selector work state; +$1C/+$1D = get/put sector size codes |
| $0F000738 | Host command parameter staging for task 4 |

Filter entry layout ($0F000420 + filter*16):

| Offset | Content |
|--------|---------|
| +0 | Mode byte (bit 7 written by a reset request clears the entry) |
| +2 | File ID (from CR3 low byte) |
| +3 | Channel number (from CR1 low byte) |
| +4/+5 | Subheader submode / coding-info masks (from CR2) |
| +6/+7 | Subheader submode / coding-info values (from CR4) |
| +8 | Range start FAD |
| +12 | Range sector count |

(Offsets confirmed from the Set Filter Subheader worker $57F0; the command
input format matches saturn_cdblock_commands.md 0x42/0x43.)

Filter connections are kept separately and manipulated through the
connection helper $5520 (ROM dispatch index 92) with node encodings
$40|n = filter true connector, $60|n = filter false connector, and
target operands $A0|n / $00|n; $5500 disconnects.

### Selector Command Handling

The IRQ6 handlers for the selector commands do parameter validation and
immediate responses only; mutations run in task 4 under the filter lock
(TRAPA 42 on task 11's TCB). The handler stages the CR parameters at
$0F000738 and sends task 4 the message $04810000 | command code; task 4
copies the staging block to $0F0006C0, executes, and asserts HIRQ ESEL
($0040 via $0A00001E). Task 4 accepts codes $40, $42, $44, $46, $48,
$52, $55 from the host path and $40-$46 from task 7.

Immediate-response selector commands observed: Get Buffer Size (CR2 =
free count, CR3 = 24 << 8, CR4 = 200), Get Sector Number (CR4 =
partition count byte), Set Sector Length (stores the two size codes),
Calculate Actual Size (validates partition/range via $6DBC: position
$FFFF = from end, count $FFFF = through end).

### Sector Pipeline

Sectors flow from the decoder to the host through a chain of tasks and
interrupts:

1. The decoder raises IRQ7 (vector 71) on a data-ready event and DMAC0
   completion (vector 72) when a sector has landed in the staging buffer.
   Vector 72 messages task 5 with event $84 payload $10.
2. Task 5 ($45A4) is the router. Its per-sector work reads the freshly
   arrived sector header (buffer +12), applies the active filters to pick
   a partition (or drop the sector), links the buffer into that
   partition's chain, updates the free-buffer count, and arms the next
   DMA target. It also handles filter/decoder control events ($80 = data
   error/reset, $81 = mode change, $84 = sector arrived with a 10-entry
   sub-dispatch on the payload, $06/$01 = coordination). The CD state
   block it works from is $0F000314.
3. Task 1 ($63EC) waits for task 5 to hand it a completed partition
   (event, payload $10) and performs post-processing/notification toward
   the host (HIRQ CSCT/DRDY/etc).
4. Task 12 ($69A0) runs the host data transfer service: it owns the
   DATATRNS DMA (DEI1, vector 74) that streams a partition's buffers out
   through $0A000000, walking the chain table via the chunk calculators.
5. Task 13 ($6FD8) is the copy/move worker for Copy/Move Sector Data
   ($65/$66), relinking buffers between partitions.

CD state block ($0F000314):

| Offset | Purpose |
|--------|---------|
| +6 | Latest sector-arrival status (low byte of $0A00001C) |
| +12 | Next sector destination buffer index ($FF = none) |
| +13/+14 | Current / previous sector buffer index |
| +16/+20/+24 | Sector destination addresses (next / current / previous) |

### Sector Arrival (vector 72, DMAC0)

```python
def dei0_sector_arrived():             # $5B48
    SP = 0x0F0009A8
    state = block_0F000314
    state.status = read16(0x0A00001C)
    shift_history(state)               # +13 -> +14, +20 -> +24
    write16(0x0A00001A, read16(0x0A00001A) & 0xFF7F)   # ack sector pending
    if state.next_index != 0xFF:       # +12/+16: queued destination
        DAR0 = state.next_addr + 12
        TCR0 = 0x0492                  # 1170 words = 2340 bytes
        CHCR0 = 0x436D                 # enable + interrupt
        state.cur_index, state.cur_addr = state.next_index, state.next_addr
        state.next_index = 0xFF
    else:
        CHCR0 = 0x4368                 # stop
    trapa45_send(0x05840010)           # notify task 5
```

### Buffer Exhaustion and Resume

Buffer allocation ($4EA8, ROM dispatch index 95) pops the free-list
head and decrements the free count at $0F00034A; it returns index $FF
when the list is empty. The free routine ($4EE4, index 97) pushes the
index back and increments the count; when the count transitions 0 -> 1
it calls the buffer-available hook ($520A).

Task 5's per-sector work re-arms the arrival queue after routing each
sector. When that allocation fails (no next buffer, CD state block +12
= $FF), the exhaustion path ($49D6) runs:

- sets the buffer-wait flag $0F00034B to $FF (once per episode),
- asserts BFUL by writing 8 to the HIRQ assert port $0A00001E,
- unless CD state block +5 bit $40 is set, messages the report task
  with event 4 payload 1 (drive stop request).

With no destination armed, the next sector arrival stops the DMA
channel (the CHCR0 = $4368 branch above) instead of storing the
sector, so playback pauses at the stalled position.

The buffer-available hook ($520A) is the resume side: if the
buffer-wait flag is set it clears it and, unless drive-state byte
$0F000319 bit 5 is set, re-arms the pending play request from the
working copy at $0F0002AC (masked into $0F0002C0) and messages the
report task with event 4 payload 0 - a play request that seeks back to
the stalled position and continues the range. With the flag clear it
instead re-arms via $0F0002B4 and sends event 5 payload 4 (gated on
$0F000319 bit 1). Because the trigger is the free count's 0 -> 1 edge,
one freed buffer is enough to start the resume; the mechanical
seek-back provides the latency before delivery restarts.

Host-side consequence: Put Sector Data allocates from the same pool
the arrival queue drains. The put handler ($68E0) answers with the
WAIT status flag (through the $91FC responder tail) when the requested
count exceeds the free count, when the count is zero, or when the
transfer-state byte $0F000724 has any of bits 1-3 set; only a buffer
number >= 24 rejects (through $91F8). The status responder tail at
$9210 is shared: entry $920E ORs $00 into the status byte (normal),
$91FC ORs $80 (WAIT), $91F8 ORs $FF (REJECT).

## Extension / Patch Mechanism

At buffer manager init the firmware attempts to load an extension image
(MPEG cartridge firmware) from the window at $0E000000:

```python
def load_extension():                  # $1B16
    length = read32(0x0E000000)
    if length == 0 or length > 0x5000:
        clear(0x0907B000, 0x09080000)
        write8(0x0F0002FD, 0)
        write16(0x0F0007AC, 0)
        return -1
    if copy_and_verify(0x0E000000, 0x0907B000, length) != 0:   # $9914
        clear(0x0907B000, 0x09080000)
        return -1
    write8(0x0F0002FD, read8(0x0907B000))        # image ID byte
    if read8(0x0907B001) < read8(0x09075845):    # image version below stored
        hook = read32(0x09075840)
        if hook != 0:
            call(hook)
    call(read32(0x0907B004))                     # image entry point
    return 0
```

ROM code reaches replaceable functions through numbered trampolines:

- Indices 0-88 read a function pointer from the RAM table at
  $0907B008 + index*4 and jump to it. Indices 0-31 are the MPEG command
  handlers ($90-$AF); 32-88 are internal service points (trampolines at
  $F80A-$F8EC).
- Indices 89-97 read from the ROM table at $F938 + index*4.

ROM code only calls RAM-table trampolines from MPEG/extension code paths
(gated on $0F00027D bit 1, $0F000892 bit 7, or the $09075388 request
flags), so the RAM table is exercised only after an extension image has
populated it. The ROM table at $F938 carries default entries for most
indices; ROM code calls those defaults directly (not through the RAM
table), so the table works as an export directory the extension clones
into $0907B008 and selectively overrides.

The default targets for indices 0-88 all live in the MPEG code region and
are tabulated in [saturn_cdblock_mpeg.md](saturn_cdblock_mpeg.md) (indices
0-31 are the $90-$AF command handlers; 32-88 are MPEG service points). One
example illustrates the hook granularity: index 32's default is $A032,
task 8's message-loop re-entry, so an extension can intercept that task's
loop and fall back to the ROM behavior.

The runtime ROM-dispatched entries (89-97) are general firmware service
points, not MPEG: 89 = $91B8 (status responder), 90 = $91FC, 91 = $55BE,
92 = $5520, 93 = $51CE, 94 = $507C, 95 = $4EA8 (buffer alloc), 96 = $50DA,
97 = $4EE4.

The extension command validation helper at $B538 refuses work while
$0F000892 has any of bits 1-3 set or while $0907537B bit 0 is set, then
saves the command CR words at $09075374.


## CD Drive Link

The CD drive mechanism controller is attached over SCI0 in synchronous
mode, exchanging fixed-size frames in both directions simultaneously.

### Frame Protocol (vector 93, $974C)

ITU3 GRB compare match is the byte-slot clock. Each interrupt handles one
slot of the frame cycle (slot indices 0-13), tracked by the byte at
$0F000311 (bit 7 = idle/resync marker):

- Slot 0 (frame start): sample the drive-ready line (port B data low
  byte, bit 2). If low, mark idle and resync. If a command is pending
  (byte $0F0002CF nonzero), copy the 12-byte command image from
  $0F0002C4 into the frame buffer at $0F000304, computing the checksum
  (bitwise NOT of the 8-bit sum of the 11 command bytes) into frame byte
  11, and clear the pending flag. Otherwise load the idle frame (11 zero
  bytes, checksum $FF).
- Slots 1-12: write frame byte (slot-1) to SCI0 TDR. From slot 2 on, also
  read SCI0 RDR (the drive's byte for the previous slot) into the receive
  buffer at $0F0002D0 + (slot-2) and add it to a running sum. The
  drive-ready line is rechecked every slot; if it drops, the frame
  restarts.
- Slot 13: read the drive's checksum byte from RDR and compare with the
  bitwise NOT of the received sum. Write the verdict as the final TX
  byte: $00 = good, $01 = bad. On success set the frame-valid flag
  ($0F0002DB = 1). Enable the SCI0 receive interrupt.

The drive then sends one byte on its own: its acknowledgment of the
frame the SH-1 transmitted. Vector 101 ($988C) receives it and writes
$00 (accepted) or $80 (refused) back into the slot-index byte $0F000311
- a refusal thereby also sets the resync marker bit - sets the received
flag at $0F000312, disables the receive interrupt again, and sends
message $06830000 (event $83) to task 6.

Buffers (all inside the report task state block, GBR = $0F00025C):

| Address | Report offset | Purpose |
|---------|---------------|---------|
| $0F0002C4-$0F0002CE | +104..+114 | Outgoing 11-byte drive command |
| $0F0002CF | +115 | Command pending flag (set by queueing, consumed at frame start) |
| $0F0002D0-$0F0002DA | +116..+126 | Received 11-byte drive status frame |
| $0F0002DB | +127 | Received frame valid (checksum passed) |
| $0F000304-$0F00030F | - | Frame image transmitted this cycle (11 bytes + checksum) |
| $0F000311 | - | Slot index; bit 7 = idle/resync; overwritten with the drive's ACK ($00/$80) |
| $0F000312 | - | In-frame / ACK-received marker |

### Drive Commands

Commands are queued by writing three longs to +104 ($415C and relatives
$4158/$3266/$326A); byte +114 carries a tag ($01 or $02) and +115 the
pending marker. Byte 0 is the opcode. Codes observed with their builders:

| Code | Parameters observed | Issued by | Pending op set |
|------|---------------------|-----------|----------------|
| $00 | none (all zero = idle/status poll) | error recovery ($3B1C) | 18 |
| $02 | bytes 1-3 = FAD | task 7 request path ($37B2) | from request |
| $03 | byte 1 = parameter (track) | pending-op override ($2BB0) | - |
| $04 | none | operation 9 sequence ($3188) | 9 |
| $05 | none | operation 10 sequence ($31D4) | 10 |
| $06 | bytes 1-3 = FAD | operation 5 sequence ($2ECC path) and op-12 variant ($3F82) | 5 |
| $08 | none | operation sequence ($3136) | 5 |
| $09 | bytes 1-3 = FAD, byte 4 = mode | seek/play start ($2DD8) and re-issue ($3F7C, FAD+30, byte 4 = $06) | 4 |
| $0C | none | drive comm restart ($2DA2) | - |

The drive's 11-byte status frame:

| Frame byte | Report offset | Content |
|------------|---------------|---------|
| 0 | +116 | Status: operation class in the upper nibble, subclass in the lower. Class 8 = error/request class; subclass 3 or bit 3 = fatal |
| 1 | +117 | Flags; low nibble 1 = position fields valid |
| 2 | +118 | Position format selector (0 = bytes 4-6 hold the position, else bytes 8-10) |
| 3-5 | +119..+121 | 24-bit FAD (TOC readout frames: entry FAD) |
| 4-6 | +120..+122 | Position as BCD min/sec/frame (when byte 2 = 0) |
| 8-10 | +124..+126 | Position as BCD min/sec/frame (when byte 2 nonzero) |

BCD MSF positions convert to FAD as M*4500 + S*75 + F ($41C6), rejected
above 359999. During TOC readout (pending op 33) frame bytes 1-2 carry
the TOC entry's ctrl/track data, bytes 3-5 its FAD, and words at +122/
+124 are captured to +144/+146 before the operation advances to op 34.

### TOC Storage (buffer DRAM)

| Address | Purpose |
|---------|---------|
| $09000064 + track*4 | TOC entry: ctrl/adr byte : 24-bit track start FAD ($421A read, $4224 write) |
| $090001F5 | Last track number (inside the A1 entry) |
| $090001F6 | Disc type byte (inside the A0 entry; $10 checked for the track-1 special case) |
| $090001F9 | First track number (inside the A0/A1 area) |
| $090001FC | TOC lead-out entry A2 (ctrl : 24-bit lead-out FAD) |
| $09000000 | Session info block: byte 0 = session count, byte n = first track of session n |
| $09000214 | Session fallback record (used when a session's TOC entry is empty) |
| $09000218/$09000224 | Double-buffered presentation record |

The TOC is the standard 102-entry image (tracks 1-99 at $09000068 +
(track-1)*4... i.e. indexed $09000064 + track*4, then A0/A1/A2 at
$090001F4/$090001F8/$090001FC), which is why Get TOC reports 204 words.
$422E maps FAD to (track, track-relative offset) by scanning TOC entries
from the first to the last track, with FAD at or beyond the lead-out
mapping to track $AA.

Byte $0F0007B0 bit 0 means the drive communication link is down; while
set, every host command except $05 is rejected and the periodic report
publishes as BUSY with invalid position (see below).


## Disc Authentication and File System

### Authentication ($E0/$E1, $C2C6/$C3AA)

Authenticate Device ($E0) starts a check and Is Authenticated ($E1)
polls the result:

- $E0 subcommand 0 (disc): stashes the CR parameters into the DRAM
  scratch at $0907ADB0 (length in CR2 capped at 23), sets the request
  byte $0907ADCB = 2, and sends message $07810008 to task 7 (drive
  control). Task 7 reads the disc's security region and validates it
  against the header signatures (`SEGA SEGASATURN ` at $8A3C, `SEGASYSTEM`
  at $8D04) through the directory reader. The result word is kept at
  $0F000786.
- The tail at $C31E: the immediate response is the current report, and
  byte $0F0007B0 (drive-status latch) bit 0 is set.
- $E1 subcommand 0 returns the disc auth result word at $0F000786 in
  CR2:CR3.

Subcommand 1 of $E0/$E1 and the whole of $E2 (Get MPEG Card Boot ROM)
authenticate/read the MPEG cartridge rather than the disc; those paths
are documented in [saturn_cdblock_mpeg.md](saturn_cdblock_mpeg.md).

The `Hitachi.PublicKeyCipher` string at $99F4 is the constant salt for a
hash/cipher routine ($99AC-$9A0C, XOR/rotate/add mixing) reached only
through computed pointers, used by the security validation.

### File System Commands ($9488-$96D2)

The ISO 9660 reader is attached to task 7 (drive control): the command
workers stage parameters and message task 7, which reads and parses
directory sectors off the disc.

| Command | Worker | Notes |
|---------|--------|-------|
| Change Directory $70 | $9488 | filter number in CR3 high byte (must be < 24; current selector state at $0F00076B must equal 4), 24-bit file ID in CR3 low byte:CR4; $FFFFFF = root; calls the directory reader $7E46 |
| Read Directory $71 | $9534 | enumerates the current directory |
| Get File System Scope $72 | $9576 | returns file count / first file id of the current directory |
| Get File Info $73 | $95C8 | transfer-wrapped; streams the directory record(s) for a file id to the host |
| Read File $74 | $9628 | starts a file read into a partition |
| Abort File $75 | $96D2 | aborts an in-progress file read |

File system work state ($0F000744-$0F000790): staged command parameters
at $0F000744, current filter at $0F000772, current directory / file
cursor and the parsed directory image the workers walk. A directory
record buffer is read into DRAM and parsed for name, FAD, size, and
attribute fields.

## MPEG (Video CD) Cartridge Subsystem

The `$A020-$F712` code region, DMAC2/3, ITU0-2, and vectors 76/78/88/89
serve the optional MPEG (Video CD / Movie Card) cartridge. On a stock
console this subsystem is initialized and probed at boot, then never
runs: task 8 waits forever and the DMA/capture interrupts never fire. It
is the firmware's relay between the host and the cartridge's two decoder
LSIs - the game drives playback through CD block host commands $90-$AF
and the firmware translates them into LSI register writes and command
packets.

Full detail (the $90-$AF command set, the LSI A/B protocol, the state
RAM at $0F000840-$0F00089F, the vector 88/89 status decode, and the
extension dispatch-table defaults for indices 0-88) is in
[saturn_cdblock_mpeg.md](saturn_cdblock_mpeg.md).

Integration points documented in this file: the memory-map rows for the
LSI windows and DRAM work areas (SH-1 Memory Map), the boot-time cartridge
probe (Boot Sequence Phase 3), the host-command dispatch rows for $90-$AF
and $E0-$E2 (Host Command Processing), and the general extension/hook
mechanism (Extension / Patch Mechanism).


## CD Status Report Generation

The host-visible CD report (status, flag/repeat, ctrl/track, index, FAD)
is maintained by task 6, whose state block is addressed GBR-relative with
GBR = $0F00025C throughout the $2700-$4500 region. The task is
event-driven: it sleeps in TRAPA 35 ($27A6) and processes mechanism events
(drive status, subcode position, command acknowledgments) as they arrive.

### Report Task State Block (GBR = $0F00025C)

| Offset | Size | Purpose |
|--------|------|---------|
| +16 | 8 | Report image bank 0 (CR1-CR4) |
| +24 | 8 | Report image bank 1 (CR1-CR4) |
| +32 | 1 | Report image bank selector |
| +44 | 1 | Presentation record bank selector (XOR-flipped per update) |
| +46 | 1 | Drive state code (4 = seek) |
| +48 | 1 | Presented ctrl/adr |
| +49 | 1 | Presented track number |
| +50 | 1 | Presented index number |
| +52 | 4 | Presented position (relative); incremented per sector during play |
| +56 | 4 | Presented position (absolute FAD); incremented per sector during play |
| +40 | 4 | Working position derived from the target at seek/play start ($2EB8) |
| +60 | 4 | Working copy of target A (byte +60 doubles as designation kind) |
| +64 | 4 | Working copy of target B (low 24 bits = track:index or FAD) |
| +68 | 4 | Seek target FAD (byte +68 bit 0 = FAD-designated) |
| +70/+71 | 2 | Target track / index parameters |
| +73 | 1 | Pending drive operation code (see below) |
| +74 | 1 | Report override flags (bits 3 and 5: drive command in flight) |
| +75 | 1 | Secondary flags (bit 3: error report, bit 4: position valid seen) |
| +96 | 4 | Requested FAD from task 7 operations |
| +104 | 24 | Drive link TX/RX buffers (see CD Drive Link) |
| +144/+146 | 4 | TOC readout capture words |

Pending drive operation codes (+73) observed: 4 (seek/play via command
$09), 5 (command $06/$08 sequences), 9 (command $04), 10 (command $05),
12 (variant re-issued as command $06), 17-22 (multi-step sequences with
their own event filter at $2828), 18 (error re-init), 33/34 (TOC
readout), $99 (error latch). Drive state codes (+46): 1, 2, 4 (seek), 6
(error/reinit), 10 (fatal).

### Event Architecture

The task sleeps in TRAPA 35; every incoming message is dispatched on its
source code, and after each handler returns the report is republished
(so SCDQ pulses on every processed event):

| Source | Meaning | Handlers |
|--------|---------|----------|
| 3 | Lock mailbox coordination (payload 1 releases lock $0306) | inline |
| 4 | Task 4 (filter manager) events, payloads 0-1 | $37D4, $380E |
| 5 | Task 5 (sector pipeline) events, payloads 0-4 | $3660-$36EC |
| 6 | Self/timer events | table at $29DC |
| 7 | Task 7 (drive control) requests, payloads 0-4 | $370E-$3788 |
| $81 | Host command requests, payloads 0-13 | $2D16, $2D8A, $2D8E, $2DD8 (seek/play), $3014, $2ECC, $3094, $30B0, $30C8, $3188, $3218, $3260, $35F4, $361E |
| $83 | Drive frame exchange complete | $3AB8 |

While a pending operation 17-22 is active, events are filtered through
the override handlers ($2B2E/$2C68/$2D00) instead.

The $83 handler validates the frame ($2C54: frame-valid flag set and no
command awaiting acknowledgment), then dispatches on the status class
(byte +116 upper nibble) and the pending operation: class 8 is the
error/request class (subclass 3 or bit 3 latches op $99 / state 10 and
reports ERROR), other classes step the active operation's state machine
(op 4 table at $3B8C: handlers $3BB0-$3ED2 for ops 4-12, op 33 at
$40E0).

At startup the task also drives two hardware lines: $0A00001A bit 6 is
set (bit 4 thereafter mirrors state flag $0F000894 bit 4), and port B
output pin PB6 ($05FFFFC3 bit 6) follows the pending-operation state
(set while op 6/12 conditions hold). Note the three port B bits with
distinct roles: PB10 ($05FFFFC2 bit 2) is the boot mode strap, PB2
($05FFFFC3 bit 2) is the drive-ready input sampled by the frame engine,
PB6 is this status output.

The presentation record (ctrl, track, index, position) is published into a
double-buffered block at $09000218/$09000224 in buffer DRAM ($4398-$43B2),
selected by the byte at +44.

At task startup ($2728-$2756) the state block is cleared and both report
image banks are set to status BUSY with track/index/FAD =
$FF/$FF/$FFFFFF. Track $FF, index $FF, FAD $FFFFFF is the standard
invalid-position report, used whenever the firmware has no trustworthy
position.

### Report Publisher ($2930-$29A2)

```python
def publish_report():
    if read8(0x0F0007B0) & 1:              # drive link down
        cr = [0x20FF, 0xFFFF, 0xFFFF, 0xFFFF]
    else:
        bank = 24 if read8(gbr + 32) else 16
        cr = load_words(gbr + bank, 4)
        cr[0] |= 0x2000                    # periodic flag (status bit 5)
        if read8(gbr + 74) & 0x28:         # drive command in flight
            cr[0] &= 0xF0FF                # status code -> BUSY, keep flag nibble
        elif read8(gbr + 75) & 0x08:
            cr[0] = (cr[0] & 0xF000) | 0x09FF   # status ERROR
            cr[1] = cr[2] = cr[3] = 0xFFFF
    with interrupts_masked():
        if read16(0x0A000004) & 2 == 0:
            write16(0x0A000010, cr[0])
            write16(0x0A000012, cr[1])
            write16(0x0A000014, cr[2])
            write16(0x0A000016, cr[3])
    write16(0x0A00001E, 0x0400)            # HIRQ SCDQ
```

The BUSY masking implements the "BUSY while changing status" rule from the
interface spec: the position fields still publish, only the status code
nibble reads 0.

### Position Presentation During Seeks

The seek-start handler ($2DD8, drive state 4 written to +46 at $2E38)
stamps the presented position with the seek destination before the
mechanism command is even issued:

- The target FAD is stored at +68 ($2E26/$2E36). A target of $FFFFFF
  resolves to the lead-out FAD (read from the A2 TOC entry at $090001FC);
  a target of 0 is clamped to FAD 150.
- $4310 (reached via the jump table at $3650) derives the presentation
  from the target: for FAD-designated targets the target's track is looked
  up from the TOC (the lead-out sentinel $AA is remapped at $432E) and the
  presented index is set to 1; for track-designated targets the presented
  track/index come directly from the target parameters ($4364-$436A). The
  presented position longs at +52/+56 are set from the target.

The presented position advances only during play: the per-sector update
increments the position longs by one FAD per sector ($43B8-$43CE). No code
path generates or reports positions between a seek's source and
destination. For the entire duration of a seek the reported position is
the destination (with the BUSY status override active until the mechanism
acknowledges the command, then status SEEK), and normal per-sector advance
resumes from the destination once play begins.

### Drive Command Queueing ($415C)

```python
def queue_drive_command(w0, w1, w2):   # w0 byte3 = opcode
    flags74 |= 0x28                    # forces published status to BUSY
    flags3 &= ~0x20
    tag = 0xFFFF02FF if use_tag2 else 0xFFFF01FF   # byte +114 tag select
    store32(gbr + 104, w0)
    store32(gbr + 108, w1)
    store32(gbr + 112, w2 & tag)       # low byte = pending marker (+115)
```

The vector-93 frame engine picks the command up at the next frame start.
A drive acknowledgment event (type $83, from the SCI0 receive interrupt)
clears the BUSY override in two stages ($27E4-$27FE: first bit 3, then
bit 5). The seek-start path sets bit 3 via $2E3C the same way.


## Peripheral Initialization Details

### SCI ($610) and A/D ($62C)

Both serial channels are configured identically at boot: SMR = $80
(synchronous mode), BRR = 99, SCR = 0 (disabled). SCI0 is later opened for
drive communication (receive interrupt enabled on demand, RDR polled by
vector 101). SCI1 is only used by factory test mode, which reprograms it
to SMR = 0, BRR = 65, SCR = $30.

$62C initializes the A/D converter: ADCSR = $0E, ADCR = $7F.

### ITU ($638, $1C14)

All five channels are parked at boot (see Phase 1). Channel 4 is started
by $1C14 as the system time base: TCR4 = 3 (internal clock / 8), TIOR4 = 0,
TIER4 = 4 (overflow interrupt only), TSTR bit 4 set. The overflow handler
(vector 98) increments the long at $0F00021C, and ITU4 compare match
(vectors 96/97) drives the software-timer list. Channel 3 GRB compare
match (vector 93) is the drive link byte-slot clock, armed by the report
task. Channels 0-2 serve the MPEG cartridge input-capture lines (ITU2
capture is vectors 88/89).

### DMAC ($6BC)

All four CHCRs are written $0040 with DMAOR toggled 0 then 1 (master
enable). Channel roles are listed in the memory map section. The DMA
address error exception resets this configuration.

### BSC / DRAM Refresh ($574, $708)

$574 programs the bus controller (BCR = $8000, WCR1 = $FFFF,
WCR2 = $FFFF, WCR3 = $9800, DCR = $7500, PCR = 0) and loads the refresh
timer (RCR = $80, RTCNT = $80, RTCOR = $80, RTCSR = 0). $708 waits 400
loops, enables the refresh timer (RTCSR = $08), then touches one word
every $200 bytes through the full 512KB DRAM to complete initialization.

### INTC ($54C), Ports ($5D0), TPC ($5FA), WDT ($5A8), Standby ($5C8)

Covered in the boot pseudocode above. Note that port A/B pin function
selection ($5D0) is what routes the DMAC request/acknowledge and IRQ6/IRQ7
pins to the CD block ASICs; port B data bit 2 doubles as the factory test
strap, and port C bit 0 selects the $0A00001A init value.
