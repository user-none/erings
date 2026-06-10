# Saturn CD Block Firmware Disassembly Notes (CDB-106)

Source: cdb106.bin disassembled with utils/disasm
SHA256: 2dfb3618b5b612bb49abbf0d1d8d53016908bb81aa8c92515f0836203e0b7f1c

## Firmware Identification

Copyright string at $000400:
`Copyright (C) Hitachi, Ltd. 1993`

Additional identification strings:
- $001F49: `CDBLOCK/`
- $008A3B: `SEGA SEGASATURN ` (disc header validation)
- $008D04: `SEGASYSTEM` (disc header validation)
- $0099F4: `Hitachi.PublicKeyCipher` (authentication crypto)
- $00BCD0: `SEGA` (disc region/signature check)

The firmware is 65536 bytes (64KB) and runs on the SH1 (SH7034) processor
inside the Saturn's CD block subsystem.


## SH1 Memory Map

| Address Range | Size | Description |
|---------------|------|-------------|
| $00000000-$0000FFFF | 64KB | Firmware ROM (this file) |
| $05FFFE00-$05FFFFFF | 512B | SH1 on-chip peripheral registers |
| $0907xxxx | varies | CDB ASIC registers and buffer space |
| $0A000000-$0A00002F | 48B | Host interface registers (SH1 side) |
| $0A100000+ | varies | CD servo/decoder controller |
| $0A180000+ | varies | CD error correction controller |
| $0F000000-$0F000FFF | 4KB | Internal SRAM |

### SH1 On-Chip Peripherals ($05FFFE00)

The SH7034 on-chip registers are accessed via GBR-relative addressing.
The firmware uses three GBR base values during initialization:

| GBR Base | Area | Purpose |
|----------|------|---------|
| $05FFFEC0 | SCI channels | Serial communication (CD mechanism) |
| $05FFFF00 | ITU timers | Interval Timer Unit (5 channels) |
| $05FFFF80 | INTC / ports | Interrupt controller, I/O ports |

### Host Interface Registers ($0A000000)

These are the CD block command/status registers as seen from the SH1 side.
The SH-2 CPUs access the same registers at $05890000 through the A-Bus CS2
interface. The firmware initializes and tests these during boot.

| SH1 Address | SH-2 Address | Register | Description |
|-------------|-------------|----------|-------------|
| $0A000008 | $05890008 | HIRQREQ | Interrupt request flags |
| $0A00000A | $0589000A | HIRQMASK | Interrupt mask |
| $0A000010 | $05890018 | CR1 | Result register 1 (response side) |
| $0A000012 | $0589001C | CR2 | Result register 2 (response side) |
| $0A000014 | $05890020 | CR3 | Result register 3 (response side) |
| $0A000016 | $05890024 | CR4 | Result register 4 (response side) |
| $0A00001E | - | - | Response notify strobe (firmware writes $0400) |

The status report publisher (see CD Status Report Generation below)
writes responses to $0A000010-$0A000016 and then strobes $0A00001E.
Command parameters written by the SH-2 are read back through the CDB
ASIC mirror at $09075374, not through this register window. The
$0A000018 word is used by the sector read path (vector 72), not as a
CR register.

### CDB ASIC Registers ($0907xxxx)

The CDB ASIC (likely YGR019B or similar) provides CD decoding, buffer
management, and the host interface bridge. The SH1 accesses its registers
in the $0907xxxx address space. Key observed addresses:

| Address | Usage |
|---------|-------|
| $09075374 | CR1/CR2 mirror (command data from SH-2) |
| $0907537B | Command pending flag (bit 0) |

The ASIC likely mirrors the host interface writes into this address space
so the SH1 firmware can read them without contention.

### Internal SRAM ($0F000000)

4KB of RAM used for all runtime state. Cleared to zero during boot.

| Address | Purpose |
|---------|---------|
| $0F000000-$0F000007 | System variables (status flags, version info) |
| $0F000008-$0F0001BF | Task Control Blocks (up to 14 tasks, ~28 bytes each) |
| $0F0001D4 | Current task pointer (scheduler) |
| $0F000314 | CD subsystem state block |
| $0F000890-$0F00089F | Command processing state |
| $0F0008F8 | Interrupt handler stack base |
| $0F0009A8 | Secondary interrupt stack base |
| $0F001000 | Top of RAM (initial stack pointer) |


## ROM Vector Table ($000000-$000187)

The SH1 fetches exception vectors from address 0. The vector table occupies
the first 392 bytes (98 vectors x 4 bytes).

### Exception Vectors ($000000-$0000BF)

| Offset | Vector | Value | Purpose |
|--------|--------|-------|---------|
| $000000 | 0 | $00000428 | Power-on PC (entry point) |
| $000004 | 1 | $0F001000 | Power-on SP (top of SRAM) |
| $000008 | 2 | $00000428 | Manual reset PC |
| $00000C | 3 | $0F001000 | Manual reset SP |
| $000010 | 4 | $00000944 | General illegal instruction |
| $000014 | 5 | $000009E2 | Reserved (default) |
| $000018 | 6 | $0000094A | Slot illegal instruction |
| $00001C | 7 | $000009E2 | Reserved (default) |
| $000020 | 8 | $000009E2 | Reserved (default) |
| $000024 | 9 | $00000950 | CPU address error |
| $000028 | 10 | $00000956 | DMA address error |
| $00002C | 11 | $000009C4 | NMI |
| $000030 | 12 | $000009CA | User break |
| $000034-$7C | 13-31 | $000009E2 | Reserved (default) |

### TRAPA Vectors ($000080-$0000FF) - RTOS System Calls

| Offset | TRAPA# | Value | Purpose |
|--------|--------|-------|---------|
| $000080 | 32 | $00000EF4 | Create/start task |
| $000084 | 33 | $00000F82 | Yield / reschedule |
| $000088 | 34 | $00000FC8 | Terminate task |
| $00008C | 35 | $00000E90 | Sleep / wait |
| $000090 | 36 | $00001DA6 | Set event flag |
| $000094 | 37 | $00001E56 | Clear event flag |
| $000098 | 38 | $0000122E | Send message |
| $00009C | 39 | $00001260 | Receive message |
| $0000A0 | 40 | $00000E6C | Change task priority |
| $0000A4 | 41 | $00000F54 | Resume task |
| $0000A8 | 42 | $000012E2 | Acquire semaphore |
| $0000AC | 43 | $00001C08 | Error handler |
| $0000B0 | 44 | $00001C0E | Clear error |
| $0000B4 | 45 | $00001EBC | Timer operation |
| $0000B8 | 46 | $00000DA4 | System info |
| $0000BC-$FC | 47-63 | $000009E2 | Unused (default) |

### External Interrupt Vectors ($000100-$000184)

| Offset | Vector | Value | Purpose |
|--------|--------|-------|---------|
| $000100-$114 | 64-69 | $000009DC | Default (reset on unhandled) |
| $000118 | 70 | $00001F50 | ITU timer - CD position tracking |
| $00011C | 71 | $00005BEC | ITU timer - CD data/frame sync |
| $000120 | 72 | $00005B48 | Sector read completion |
| $000124 | 73 | $000009E2 | Unused |
| $000128 | 74 | $000024A2 | Host command notification |
| $00012C | 75 | $000009E2 | Unused |
| $000130 | 76 | $00009CF4 | SCI receive (CD mechanism serial) |
| $000134 | 77 | $000009E2 | Unused |
| $000138 | 78 | $0000B4CC | CR4 write trigger (command execution) |
| $00013C | 79 | $000009E2 | Unused |
| $000140-$158 | 80-86 | $000009DC | Default |
| $000160 | 88 | $0000B862 | DMA transfer completion |
| $000164 | 89 | $0000A1A0 | DMA channel 2 completion |
| $000170 | 92 | $000009DC | Default |
| $000174 | 93 | $0000974C | Timer overflow |
| $000180 | 96 | $00001CA0 | Timer capture/compare |
| $000184 | 97 | $00001D48 | Timer compare match |

### Default Handlers

| Address | Code | Purpose |
|---------|------|---------|
| $000944 | NOP; BRA $0428 | Illegal instruction: warm reset |
| $00094A | NOP; RTE | Slot illegal: ignore and return |
| $000950 | NOP; RTE | CPU address error: ignore and return |
| $000956 | (see below) | DMA address error: reset interrupt priorities |
| $0009C4 | NOP; RTE | NMI: ignore and return |
| $0009CA | NOP; BRA $0428 | User break: warm reset |
| $0009DC | NOP; BRA $0428 | Unhandled interrupt: warm reset |
| $0009E2 | NOP; RTE | Default unused: ignore and return |

The DMA address error handler ($0956) clears all ITU interrupt priorities
(IPRA-IPRE) to zero, sets IPRA to $0001, and writes $0008 to
$0A000002, then returns via RTE.


## Boot Entry Point ($000428)

The SH1 begins execution here after power-on or manual reset.

### Phase 1: Core Initialization ($0428-$046A)

```
$000428: MOV.L @($510),R0     ; R0 = $000000F0
$00042A: LDC   R0,SR          ; SR = $F0 (all interrupts masked)
$00042C: SUB   R14,R14        ; R14 = 0
$00042E: LDC   R14,VBR        ; VBR = 0 (vectors at ROM base)
$000430: BSR   $000534        ; Clear SRAM ($0F000000-$0F000FFF)
$000434: MOV.L @($7DC),R0     ; R0 = $05FFFF80
$000436: LDC   R0,GBR         ; GBR = INTC/port register base
$000438: BSR   $00054C        ; Init SCI channel config
$00043C: BSR   $000568        ; Clear DMA/timer state
$000440: BSR   $0005A8        ; Configure watchdog/refresh timer
$000444: BSR   $0005C8        ; Set interrupt mask register
$000448: BSR   $000574        ; Configure DMA channels
$00044C: BSR   $0005FA        ; Configure I/O ports
$000450: MOV.L @($514),R0     ; R0 = $05FFFEC0
$000452: LDC   R0,GBR         ; GBR = SCI register base
$000454: BSR   $000610        ; Init SCI channel 0 (baud=$63, mode=$80)
$000458: BSR   $00062C        ; Init SCI channel 1 (prescaler=$0E, mask=$7F)
$00045C: MOV.L @($518),R0     ; R0 = $05FFFF00
$00045E: LDC   R0,GBR         ; GBR = ITU register base
$000460: BSR   $000638        ; Init ITU channels (5 channels, all counters cleared)
$000464: BSR   $0006BC        ; Set ITU interrupt priorities
$000468: MOV.L @($7DC),R0     ; GBR = $05FFFF80 (back to INTC)
$00046A: LDC   R0,GBR
```

Subroutine at $000534: clears SRAM by writing zero to every 4-byte word
from $0F000000 to $0F000FFF. R14 was set to 0 as the fill value.

### Phase 2: Hardware Self-Test ($046C-$04FE)

```
$00046C: BSR   $0005D0        ; Configure interrupt vectors in INTC
$000470: MOV   #3,R10         ; R10 = 3 (retry counter)
$000474: BSR   $0007E0        ; Init host interface registers
$000478: BSR   $000708        ; Additional host interface setup
$00047C: BSR   $00072E        ; Hardware self-test (returns status in R0)
```

The self-test at $00072E ($00081A internally) verifies the host interface
registers by writing test patterns to HIRQREQ/HIRQMASK and reading them
back:
1. Write $0003 to HIRQREQ ($0A000008), read back and verify bits set
2. Write $0000, verify cleared
3. Write $0070 to HIRQMASK ($0A00000A), read back and verify
4. Write $0000, verify cleared
5. Write test pattern $1258 to check additional register bits

If the test fails, the result is stored at $0F00027D with bit 7 set to
indicate hardware error. The firmware retries up to 3 times.

### Phase 3: CD Mechanism Check ($04BC-$050E)

```
$0004BC: JSR   @$0000A374     ; Init DMA for CD servo/decoder
$0004C2: BSR   $0006F0        ; Enable ITU timers
$0004C6: delay loop           ; Wait ~120000 cycles for mechanism to settle
$0004CE: JSR   @$0000A3C8     ; Check CD mechanism status (returns R0, R1)
$0004D4: test R0              ; If nonzero, set bit 1 in status byte
```

The function at $A3C8 checks:
1. DMA controller word at $0A10001E = $006C (servo initialized)
2. SCI channel 0 transmit status (bit 0 of $05FFFF11)
3. SCI channel 1 receive status (bit 1 of $05FFFF1B)
4. I/O port status (bit 0 of $05FFFF87)

Results are accumulated as a bitmask in R0 - nonzero means a subsystem
failed initialization.

### Phase 4: Branch Decision ($04FE-$050E)

```
$0004FE: MOV.L @($7DC),R1    ; GBR = $05FFFF80
$000500: LDC   R1,GBR
$000502: MOV.B @(66,GBR),R0  ; Read port status byte at $05FFFFC2
$000504: TST   #4,R0         ; Test bit 2
$000506: BF    $00050C        ; If bit 2 set: go to $0908 (normal boot)
$000508: BRA   $0009E8        ; Else: go to CD authentication path
$00050C: BRA   $000908        ; Normal boot path
```

Bit 2 of port $05FFFFC2 appears to indicate whether the CD drive is ready.
When set, the firmware enters normal operation. When clear, it enters the
CD initialization/authentication sequence.


## CD Authentication Path ($0009E8)

When the CD drive is not immediately ready, the firmware performs drive
initialization and authentication before entering the main RTOS.

```
$0009E8: MOV.L @($A98),R10   ; R10 = $05FFFEC0 (SCI base)
$0009EA: MOV.B @(10,R10),R0  ; Read SCI status
$0009EC: OR    #32,R0        ; Set bit 5 (transmit enable?)
$0009EE: MOV.B R0,@(10,R10)
```

This path:
1. Configures SCI for drive communication
2. Reads drive identification from serial interface
3. Builds a status byte from drive responses (checking various flags)
4. Waits for the CDB ASIC to signal ready (polling $05FFFFC2 bit 2)
5. Once ready, initializes the RTOS stack and enters the main system


## Normal Boot - Main Loop Initialization ($000908)

The normal boot path calls five initialization functions in sequence, then
enters the RTOS.

```
$000908: JSR   @$0000BA44     ; Init command processor state
$00090E: JSR   @$000077B8     ; Init motor/drive controller state
$000914: JSR   @$000019E4     ; Init data transfer/buffer manager
$00091A: JSR   @$00002398     ; Init CD position tracking state
$000920: JSR   @$00001C14     ; Init timer/interrupt configuration
$000926: JMP   @$00000D5C     ; Set up task table, enter RTOS
```

### Command Processor Init ($BA44)

Clears the command processing state: zeroes the status word at the HIRQ
mirror location and clears the pending command buffer.

### Motor/Drive Controller Init ($77B8)

Clears the motor state word and returns. The motor controller task handles
drive state transitions (spin-up, seek, play, pause, stop) based on
commands received from the SH-2.

### Data Transfer/Buffer Init ($19E4)

Calls $1AE6 to initialize buffer management variables, then clears the
transfer state block at the RAM location pointed to by the constant pool.
This manages the sector buffering pipeline - reading CD sectors into
internal buffers and making them available to the SH-2 via DATATRNS.

### CD Position Tracking Init ($2398)

Initializes the position tracking structure:
- Sets timer reload value to $1258
- Configures timer mode to 8 and interrupt priority to 3
- Clears the position/seek state block
- Sets default seek target to $FF (no target)

### Timer/Interrupt Configuration ($1C14)

Configures the ITU timer channels used for CD timing:
- Clears bits in ITU timer enable registers (channels 1-3)
- Zeros out the 32-byte event tracking buffer
- Configures timer prescalers: channel prescaler = 3, mode = 0, divider = 4
- Enables timer output compare (bit 4)


## RTOS Task Setup ($000D5C)

After initialization, the firmware sets up a priority-based preemptive
multitasking system and enters the scheduler.

### Task Control Blocks

TCBs are allocated in SRAM starting at $0F000008. Each TCB is approximately
28 bytes and contains:

| Offset | Size | Purpose |
|--------|------|---------|
| 0 | 1 | Task ID / state identifier |
| 1 | 1 | Task state (1=ready, 2=running, 3=waiting, 5=idle) |
| 2 | 4 | Reserved |
| 4 | 1 | Saved priority |
| 5 | 1 | Caller ID |
| 6 | 1 | Event flags |
| 7 | 1 | Wait counter |
| 8 | 4 | Saved stack pointer |
| 12-27 | varies | Task-specific state, linked list pointers |

### Task Table

The firmware creates up to 14 tasks. TCB addresses in RAM:

| Task | TCB Address | Notes |
|------|-------------|-------|
| 1 | $0F000008 | |
| 2 | $0F000024 | |
| 3 | $0F000048 | |
| 4 | $0F000060 | |
| 5 | $0F000084 | |
| 6 | $0F0000B4 | |
| 7 | $0F0000E8 | |
| 8 | $0F000114 | |
| 9 | $0F000148 | |
| 10 | $0F0001BC | |
| 11 | $0F000168 | |
| 12 | $0F000180 | |
| 13 | $0F0001A0 | |
| 14 | $0F0001BC | |

Priority ordering from initialization table at $1350:
1, 2, 4, 12, 13, 7, 9, 8, 5, 6, 14, 11, 3

### RTOS System Calls

The RTOS provides standard multitasking primitives via TRAPA instructions:

| TRAPA | Handler | Function | Notes |
|-------|---------|----------|-------|
| 32 | $0EF4 | Create/Start | Allocates TCB, sets entry point, schedules |
| 33 | $0F82 | Yield | Saves context, runs scheduler |
| 34 | $0FC8 | Terminate | Removes task from ready queue |
| 35 | $0E90 | Sleep/Wait | Moves task to wait queue, reschedules |
| 36 | $1DA6 | Set Event | Sets event flag, wakes waiting tasks |
| 37 | $1E56 | Clear Event | Clears event flag |
| 38 | $122E | Send Message | Inter-task message passing |
| 39 | $1260 | Receive Message | Blocks until message available |
| 40 | $0E6C | Change Priority | Modifies task priority, reschedules |
| 41 | $0F54 | Resume | Moves task from wait to ready queue |
| 42 | $12E2 | Acquire Semaphore | Resource locking |
| 43 | $1C08 | Error | Handles RTOS errors (sets error code) |
| 44 | $1C0E | Clear Error | Clears error state |
| 45 | $1EBC | Timer | Timer-related operation |
| 46 | $0DA4 | System Info | Returns system information |

### Scheduler ($1128)

The scheduler at $1128:
1. Reads the current task pointer from $0F0001D4
2. Loads the task state byte from TCB offset 1
3. Indexes into a function pointer table based on state
4. Sets state to 5 (idle), clears event flags
5. Saves the current task pointer back
6. JMP to the task's entry point, loading SP from TCB offset 8

The context restore path at $118C-$11B6 pops all 14 registers (R0-R13),
PR, GBR, MACH, MACL from the stack and executes RTE.


## Interrupt Handlers

### Vector 70 - CD Position Timer ($1F50)

Full context save (R0-R13, PR, GBR, MACH, MACL). This is the primary
CD position tracking interrupt.

1. Saves current task context (SP to TCB+8)
2. Switches to interrupt stack at $0F0009A8 (from constant at $22C0)
3. Clears the timer interrupt flag
4. Reads the four words at $0A000010-$0A000016 (base pointer from the
   constant at $22C8 = $0A000000, offsets 16-22) and assembles them
   into two 32-bit values via SWAP.W/XTRCT. These are the same four
   words the status report publisher writes as CR1-CR4; whether this
   handler interprets them as the published report or as a separate
   mechanism-facing latch shared at the same addresses has not been
   validated.
5. Checks status flags - if bit 0 set and the extracted byte is not
   5, calls special handler at $22B6
6. Otherwise, extracts the upper nibble of the high byte and
   dispatches through a 10-entry function table at $1FF0
7. Stores the updated values back to $0A000010-$0A000016
8. If R11 is nonzero, calls TRAPA 33 (yield) to allow task preemption

The dispatch table at $1FF0 contains 10 function pointers ($2030,
$2060, $2084, $20A0, $20C4, $2104, $2138, $2170, $22A4, $21A0),
indexed by that upper nibble. Several of the targets sub-dispatch on
the full byte against ranges that match mechanism command codes.

### Vector 72 - Sector Read Completion ($5B48)

Handles completion of a sector read from the CD decoder.

1. Switches to interrupt stack at $0F0009A8
2. Reads sector data from the CD decoder interface at $0A000018
3. Stores sector metadata into the CD state block at $0F000314
4. Configures the DMA controller ($05FFFF40) for the next sector transfer
5. Updates the position tracking state
6. Calls TRAPA 45 (timer operation) to schedule next sector

### Vector 74 - Host Command Notification ($24A2)

Handles notifications related to host (SH-2) command processing.

1. Reads status from the interrupt controller ($05FFFF8F, bits 7-3)
2. Switches to interrupt stack
3. Sets SR = $60 (enables higher-priority interrupts)
4. Reads the CD position state block
5. Checks command state and validates status byte ($88 = ready)
6. Dispatches to read or write transfer handler based on state
7. Updates sector count and transfer statistics

### Vector 76 - SCI Receive ($9CF4)

Handles serial data received from the CD mechanism controller.

1. Full context save
2. Saves task context to TCB
3. Switches to interrupt stack at $0F0008F8
4. Sets GBR = $05FFFF00 (ITU base)
5. Clears SCI receive interrupt flag (AND.B #251 at GBR+$6F)
6. Calls TRAPA 33 (yield) to wake the mechanism control task
7. Sets status flag at $0F000892
8. Jumps to the RTOS context restore at $103E

### Vector 78 - CR4 Write / Command Execution ($B4CC)

This is the primary command execution interrupt - fires when the SH-2
writes to CR4 (which triggers command processing).

1. Full context save, saves task context
2. Switches to interrupt stack at $0F0008F8
3. Sets status flag bit 2 at $0F000892
4. Sets GBR = $05FFFF00, clears timer interrupt flag
5. Calls TRAPA 33 (yield) to wake command processing task
6. Jumps to context restore at $103E

The actual command dispatch happens in the command processing task
(not directly in this interrupt handler). The interrupt merely signals
that a new command is available.

### Vector 88 - DMA Completion ($B862)

Handles DMA transfer completion for CD data.

1. Full context save
2. Calls the DMA completion handler function
3. Checks I/O port status for pending DMA
4. If secondary DMA pending, starts next transfer
5. Sets status flags in the CD state block
6. Optionally triggers a timer for the next operation

### Vector 96/97 - Timer Capture/Compare ($1CA0/$1D48)

Timer interrupts used for CD timing:
- Vector 96 handles timer capture events (tracks CD rotation timing)
- Vector 97 handles compare match events (scheduled operations)

Both follow the same pattern of saving context, switching to interrupt
stack, updating the timing state in the event buffer, and waking the
relevant task via TRAPA 33.


## Command Processing

### Command Flow

1. SH-2 writes command parameters to CR1-CR4 (writing CR4 last triggers
   the command)
2. Vector 78 interrupt fires on the SH1
3. The interrupt handler sets a flag at $0F000892 and wakes the command
   processing task
4. The command task reads CR1-CR4 from the CDB ASIC mirror at $09075374
5. Validates the command:
   - Checks error state at $0F000892 (bits 1-3 must be clear)
   - Checks command pending flag at $0907537B (bit 0 must be clear)
   - Validates channel state at $0F00089C/$0F00089D (must be 4 = ready)
6. Dispatches to the appropriate handler via function table
7. Handler sets response in CR1-CR4 and raises HIRQ flags

### Command Dispatch ($B534-$B5E0)

The command dispatch code at $B534:

```
$00B538: XOR   R10,R10        ; Clear channel flags
$00B53A: MOV.L @($B658),R3   ; R3 = $0F000892 (error state)
$00B53C: MOV.B @R3,R0
$00B53E: TST   #14,R0        ; Check error bits 1-3
$00B540: BT    $B546          ; OK if clear
$00B542: BRA   $B844          ; Error: reject command
```

After validation, the command byte (CR1 bits 15-8) and channel are
extracted. The dispatch calls:
- $F8EA: Pre-command setup
- $F89A: Command handler lookup and execution

### Response Format

The command handlers write responses back through the CDB ASIC:
- $F872: Write response CR1
- $F876: Write response CR2
- $F87A: Write response CR3-CR4
- The task returns $08810014 as a signal value to the RTOS

### Disc Validation Strings

The firmware contains disc header validation strings used during
authentication and disc type detection:
- `SEGA SEGASATURN ` at $8A3B: Standard Saturn disc signature
- `SEGASYSTEM` at $8D04: Alternative system identifier
- `SEGA` at $BCD0: Region/publisher check

The authentication handler (TRAPA vector for command $E0) uses the
`Hitachi.PublicKeyCipher` code at $99F4 for disc authentication.


## CD Status Report Generation

The host-visible CD report (status, flag/repeat, ctrl/track, index,
FAD) is maintained by a status report task whose state block is
addressed GBR-relative with GBR = $0F00025C throughout the
$2700-$4500 region. The task is event-driven: it sleeps on TRAPA 35
($27A6) and processes mechanism events (drive status, subcode
position, command acknowledgments) as they arrive.

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
| +52 | 4 | Presented position (relative) |
| +56 | 4 | Presented position (absolute FAD) |
| +68 | 4 | Seek target FAD |
| +73 | 1 | Pending drive operation code |
| +74 | 1 | Report override flags (bits 3 and 5: drive command in flight) |
| +75 | 1 | Secondary flags (bit 3: error report) |
| +104 | 12 | Queued mechanism command words |

The presentation record (ctrl, track, index, position) is published
into a double-buffered block at $09000218/$09000224 in ASIC RAM
($4398-$43B2), selected by the byte at +44.

### Report Publisher ($2930-$29A2)

Publishes the report to the host CR registers at $0A000010-$0A000016
(skipped while the word at $0A000004 has bit 1 set), then strobes
$0A00001E with $0400. Publish paths, in priority order:

1. Drive communication state invalid (bit 0 of the byte at $0F0007B0
   set): publishes CR1 = $20FF (BUSY status + periodic flag, flag
   byte $FF) with CR2/CR3 = $FFFF - track, index, and FAD invalid.
2. Otherwise the 8-byte report image is loaded (bank selected by the
   byte at +32) and $2000 is ORed into CR1 (the periodic flag, bit 5
   of the status byte). Then:
   - If byte +74 has bit 3 or bit 5 set (a drive command is in
     flight), the status code nibble of CR1 is masked to 0 (BUSY).
     The position fields publish unchanged. This implements the
     "BUSY while changing status" rule from the interface spec.
   - Else if byte +75 has bit 3 set, CR1 becomes status ERROR ($09)
     with flag byte $FF and CR2/CR3/CR4 = $FFFF.
   - Otherwise the image publishes as-is.

The boot-time report image is initialized to status BUSY with
track/index/FAD = $FF/$FF/$FFFFFF ($2748-$2756). Track = $FF,
index = $FF, FAD = $FFFFFF is the standard invalid-position report,
used whenever the firmware has no trustworthy position.

### Position Presentation During Seeks

The seek-start handler ($2DD8, drive state 4 written to +46 at
$2E38) stamps the presented position with the seek DESTINATION
before the mechanism command is even issued:

- The target FAD is stored at +68 ($2E26/$2E36). A target of
  $FFFFFF means seek-in-place (current position is read from
  $090001FC); a target of 0 is clamped to FAD 150.
- $4310 (reached via the jump table at $3650) derives the
  presentation from the target: for FAD-designated targets the
  target's track is looked up from the TOC (the lead-out sentinel
  $AA is remapped at $432E, confirming this is a track number) and
  the presented index is set to 1 (track lead); for track-designated
  targets the presented track/index come directly from the target
  parameters ($4364-$436A). The presented position longs at +52/+56
  are set from the target.

The presented position advances ONLY during play: the per-sector
update increments the position longs by one FAD per sector
($43B8-$43CE). No code path generates or reports positions between a
seek's source and destination. For the entire duration of a seek the
reported position is the destination (with the BUSY status override
active until the mechanism acknowledges the command, then status
SEEK), and normal per-sector advance resumes from the destination
once play begins.

### Drive Command Queueing ($415C)

Queueing a mechanism command stores the command words at +104,
clears the byte at +115, and sets bits 3 and 5 of byte +74 - which
forces the published status to BUSY. A mechanism acknowledgment
event (type $83) clears the override ($27E4-$27FE). The seek-start
path sets bit 3 via $2E3C the same way.


## Peripheral Initialization Details

### SCI Configuration ($0610/$062C)

Two serial channels are configured for CD mechanism communication:

**Channel 0** (GBR = $05FFFEC0, offsets 0-10):
- SMR = $80 (8-bit data, 1 stop bit, clock mode)
- BRR = $63 (baud rate divisor = 99)
- SCR = $00 (transmit/receive disabled initially)

**Channel 1** (GBR = $05FFFEC0, offsets 8-18):
- SMR = $80 (same as channel 0)
- BRR = $63 (same baud rate)
- SCR = $00 (disabled initially)

Prescaler configuration at $062C:
- Timer prescaler = $0E
- Interrupt mask = $7F

### ITU Timer Configuration ($0638)

Five timer channels are initialized with identical base configuration:

For each channel (0-4):
1. Clear counter (TCNT = $0000)
2. Set GRA = $FFFF, GRB = $FFFF (maximum period)
3. Set TCR = $00 (clock source, no prescaler)
4. Set TIOR = $08 (output compare mode)
5. Set TSR = $F8 (clear all status flags)
6. Set TIER = $F8 (clear interrupt enables)

Port configuration at $0638:
- Port A direction = $E0 (bits 7-5 output)
- Port B direction = $E0 (bits 7-5 output)
- Port C = $00

### Interrupt Priority Configuration ($06BC)

Sets interrupt priorities via IPRA-IPRE registers. These determine which
interrupts can preempt others. Values are loaded from a constant pool
at $06E4-$06EE, establishing the interrupt priority hierarchy for the
CD block's operation.

### DMA Configuration ($05D0)

Configures DMA control registers via GBR-relative stores at offsets
64-76 ($05FFFF80 + 64 = $05FFFFC0). Four 32-bit values are written
from a constant pool at $05E8-$05F6, setting up DMA source, destination,
count, and control for CD data transfers.
