# CD Block Interface

The BIOS uses the CD block only during boot, to bootstrap a disc: it
confirms the CD block is present, reads the disc header and the IP
(Initial Program) off the disc into Work RAM, authenticates it, and
hands control to the loaded code. It does this entirely through bus
reads and writes to the register surface described below - the same
mechanism a game uses to reach the CD block - but these routines are
internal to the BIOS and are not exposed as a callable CD service.

After that handoff the BIOS CD routines are never entered again - a
running game drives the CD block directly through its own code over
this same register interface. The SH-1-based CD block controller that
operates the drive executes the commands in both phases; the only
thing that changes at handoff is which side issues them, from the BIOS
to the game. Everything below therefore describes a boot-time path, not
a service that running games depend on.

The hardware register surface at `$25890000` that the BIOS uses
to drive the CD block: HIRQ status / mask, CR1-CR4 command
packets, data buffer access. Includes the full command-code
catalog grouped by subsystem, the BIOS's own boot-time command
sequence, and the BIOS-internal helpers (sub_2F48, sub_2650, etc.)
that issue these commands.

### CD Game Start (sub_186C-$18E6)

1. sub_186C loads R7 with tag $4843444D ('HCDM') and tail-jumps to
   sub_0424 (prep)
2. sub_1874: Sends CD Block commands via sub_1912 to read disc
3. sub_1912 communicates with CD Block through sub_29D4
4. Validates disc header ("SEGA SEGASATURN " check)
5. sub_1A18: Copies 4096 bytes (1024 longs) from BIOS ROM $040000 to
   $06020000. This is the security check code containing the copyright
   string and disc validation logic
6. Jumps to $06020000 to run security check from Work RAM

### Game Load Pump (sub_1C90)

Entry slot $06000284 = $00001C90. Called by the disc's IP code after
its own VBLANK handler is installed. Drives the BIOS-side game-load
state machine one step at a time per call.

Full body, 48 instructions ($001C90-$001CEE), then the wrapper handler
sub_1CF4 ($001CF4-$001D0A), then the constant pool ($001D0C-$001D34). sub_1C90
reaches its indirect targets through a local thunk at $001CDE
(`MOV.L @R1,R1; JMP @R1; NOP`) that derefs R1 as a function pointer and
tail-jumps through it; sub_1CF4 does not use this thunk - it calls its
target with a direct `JSR @R1`.

WRAM-H state inputs:

| Slot | Role | Captured handoff |
|------|------|------------------|
| $06000264 | Source ptr for initial-load memcpy | (per-disc; see notes) |
| $06000268 | Pointer to sub_1A18 (security copy entry) | $00001A18 |
| $06000290 | Load-state counter / phase flag | 3 |
| $060002B0 | Runtime/IP-installed handler addr, also memcpy dst | $06004000 |
| $060002B4 | Memcpy byte count | (per-disc) |
| $060002D0 | Pointer to sub_2F48 (CD sector read) | $00002F48 |
| $06000300 | Pointer to SYS_SETUINT ($06000794) | $06000794 |
| $0600022C | Boot state byte (auth state $22/$02 etc.) | varies |

Constant pool ($001D0C-$001D34):

```
$001D0C: H'06000290   ; counter/flag slot
$001D10: H'060002B0   ; runtime handler / load dest
$001D14: H'060002B4   ; memcpy byte count slot
$001D18: H'06000264   ; memcpy src ptr slot
$001D1C: H'060002D0   ; ptr to sub_2F48
$001D20: H'06000268   ; ptr to sub_1A18
$001D24: H'0600022C   ; boot state byte
$001D28: H'00002650   ; sub_2650 (boot-state dispatcher)
$001D2C: H'06000300   ; SYS_SETUINT slot
$001D30: H'060022E4   ; IP-resident VBLANK handler entry
$001D34: H'06002D80   ; IP security-wait counter
```

Behavior on entry:

1. Unconditionally installs sub_1CF4 as the VBLANK_IN ($40) handler
   via SYS_SETUINT (R4=$40, R5=$00001CF4). This **replaces** the
   handler the IP just installed at $060022E4 with a BIOS wrapper
   that still calls the IP's handler.
2. Reads `A = mem.L[$060002B0]` (runtime/IP handler) and
   `B = mem.L[$06000290]` (state counter).

Four-way dispatch on (A, B):

| A | B | Action |
|---|---|--------|
| 0 | 0 | RTS - nothing to do yet (state not initialized) |
| 0 | nonzero | tail-call sub_2650(R4=0) - advance state machine |
| nonzero | 0 | memcpy(A & ~3 <- mem.L[$06000264], mem.L[$060002B4]); RTS - one-shot initial load |
| nonzero | nonzero | call sub_2F48(R4=A & ~3) - sector load; on R0>=0 tail-call sub_2650(R4=0); on R0<0 clear $0600022C and tail-call sub_1A18 |

The single-step shape is consistent with a per-call pump driven by the
IP's security wait loop: the IP runs sub_1C90 once, the BIOS swaps in
sub_1CF4 (which wraps the IP's VBLANK), then per-VBLANK ticks advance
the load through sub_2F48 + sub_2650 until the counter hits zero and
the IP exits the wait loop.

### VBLANK Wrapper (sub_1CF4)

Installed at vec $40 by sub_1C90. Five instructions plus an RTS with
delay-slot store:

```
$001CF4: MOV.L @($001D30),R1  ; R1 = $060022E4 (IP's VBLANK entry)
$001CF6: STS.L PR,@-R15
$001CF8: JSR @R1                ; call IP's VBLANK handler
$001CFA: NOP
$001CFC: LDS.L @R15+,PR
$001CFE: MOV.L @($001D34),R1  ; R1 = $06002D80 (IP wait counter)
$001D00: MOV.L @R1,R0
$001D02: CMP/PZ R0              ; T = (R0 >= 0)
$001D04: BT $001D08              ; skip clamp if >= 0
$001D06: XOR R0,R0              ; clamp to 0
$001D08: RTS
$001D0A: MOV.L R0,@R1            ; delay slot: store back (clamped)
```

The wrapper calls the IP's own VBLANK handler at the hard-coded
address $060022E4, then clamps the IP-side wait counter at $06002D80
to zero from below. The hard-coded $060022E4 is a fixed-protocol
convention: every Saturn IP's VBLANK handler must sit at exactly
this offset during boot.

### CD-Block Sector Read (sub_2F48)

Entry slot $060002D0 = $00002F48. Called by sub_1C90 with R4 = load
destination address ($06004000-aligned).

Dispatch on R4 at $002F48:
- R4 == 0: drop into a smaller path at $002F58 (status-only helper)
- R4 != 0: align R4 up to 4 (`R4 = (R4+3) & ~3`), then tail-branch to
  the body at $003030

The body at $003030 (function $003030-$003096) drives the per-call
file-read pump. The calls it makes, in order, are sub_3B24 (twice),
sub_3A26 (the $73 Get File Info builder, invoked with File ID 2),
sub_30A8, sub_2DA6 (with R4 = word const $0200), and sub_3AEC (the $75
Abort File builder; see catalog). The Read File command itself is $74
(sub_3A94), referenced from the chain's pool at $00302C.

Inner CD-block I/O happens in sub_4B8A and its callees. sub_4B8A
forwards to sub_41A6, an interrupt-mask wrapper (it raises the SR mask,
calls sub_41E4, then restores it). sub_41E4 performs the actual
register access, reading/writing CD-block hardware registers
($25890008 = HIRQ, $25890018-$25890024 = CR1-CR4) through the WRAM-H
shadow slots ($060003A4 mirrors HIRQ etc.). All CD-block I/O at the
BIOS layer therefore goes through the standard $25890000-$2589002F
register window.

### Boot-State Dispatcher (sub_2650)

Entry $002650, called with R4 = state index. Switch on R4:

| State | Handler | Notes |
|-------|---------|-------|
| 0 | sub_268C | Default state (BSR sub_26CA, then JSR mem.L[$26F0] with R5=0/R6=0/R4=1, then BSR sub_32F8 with R4=$40) |
| 1 | sub_2780 | R4=1 invocation |
| 2 | sub_2718 | - |
| 3 | sub_29B6 | - |

Each handler returns a new state value in R0. sub_2650 stages that
value in R4 and returns it in R0 (the RTS delay slot copies R4 back to
R0). Drives multi-phase loading transitions.

### CD-Block Register Surface (used by sub_2F48 chain)

The BIOS does **not** use an abstracted CD-block layer. The
hardware-register addresses appearing in the BIOS constant pools:

| Addr | Reg | Touched at | Notes |
|------|-----|------------|-------|
| $25890008 | HIRQ | $0040FC, $004264, $0042E0 | Read/write of completion-flag bits; shadowed at WRAM-H $060003A4 |
| $2589000C | HMASK | $004130, $004160 | HIRQ mask |
| $25890018 | CR1 | $0042E8, $004304 | Command word 1 |
| $2589001C | CR2 | $004308 | Command word 2 |
| $25890020 | CR3 | $00430C | Command word 3 |
| $25890024 | CR4 | (no pool constant; written as CR1 base + 12, read as CR3 base + 4) | Command word 4 |
| $25898000 | CD-block work RAM | $004168 | Block transfer target |

These pool constants live in the main BIOS CD driver around
$0040xx-$0043xx (the sub_41E4 / sub_42EC / sub_4A9C family), below the
security-check blob at $040000-$04FFFF. The driver loads the hardware
register addresses directly as pool constants; the WRAM-H slots it
uses are the HIRQ shadow $060003A4 and the boot-chain pointer slots in
the slot table at $06000234-$0600034C.

By the time the BIOS hands off to game code, File ID 2 has been
transferred from the disc to the address held at $060002B0
(= System ID +$F0), with the exact byte count the CD-block reports
for that file. The transfer uses CD-block commands $73 (Get File
Info) and $75 (Read File) over the register interface at
$25890008/$2589000C (HIRQ + mask) and $25890018-$25890024
(CR1-CR4).

## CD Block Register Map

Source: CD Communication Interface spec (ST-38-R1-121093) and BIOS disassembly

The command and status registers (HIRQ, HIRQ mask, CR1-CR4) are mapped
at $25890000 (A-Bus CS2 + $90000, cache-through). The data transfer
register is at a separate offset, $25818000 (mirror $25898000). All
registers are 16-bit word access.

| Address | Register | R/W | Description |
|---------|----------|-----|-------------|
| $25818000 | DATATRNS | R/W | Data transfer register (sector data I/O); mirror at $25898000 |
| $25890004 | DATASTAT | R | Data transfer status (DIR, FUL, EMP bits); address per spec, not exercised by the BIOS |
| $25890008 | HIRQREQ | R/W | Interrupt cause register (write 0 to clear) |
| $2589000C | HIRQMSK | R/W | Interrupt cause mask register |
| $25890018 | CR1 | R/W | Command/response register 1 |
| $2589001C | CR2 | R/W | Command/response register 2 |
| $25890020 | CR3 | R/W | Command/response register 3 |
| $25890024 | CR4 | R/W | Command/response register 4 |

### HIRQREQ Bits

| Bit | Mask | Name | Meaning (1=set) |
|-----|------|------|-----------------|
| 0 | $0001 | CMOK | Command complete, ready for next command |
| 1 | $0002 | DRDY | Data transfer preparation complete |
| 2 | $0004 | CSCT | 1 sector read from CD-ROM confirmed |
| 3 | $0008 | BFUL | CD buffer full |
| 4 | $0010 | PEND | Play ended (FAD outside play area, CD not playing) |
| 5 | $0020 | DCHG | Disc changed (tray opened) |
| 6 | $0040 | ESEL | Selector/filter operation complete |
| 7 | $0080 | EHST | Host I/O operation complete |
| 8 | $0100 | ECPY | Copy/move operation complete |
| 9 | $0200 | EFLS | File-system operation complete |
| 10 | $0400 | SCDQ | Subcode Q updated |
| 11 | $0800 | MPED | MPEG decode complete |
| 12 | $1000 | MPCM | MPEG command complete |
| 13 | $2000 | MPST | MPEG status changed |

Only 0 (clear) can be written to bits. Writing 1 has no effect.
The selector/buffer commands ($30, $48, $60, etc.) wait on ESEL
($0040); file-system commands ($70, $71, etc.) wait on EFLS ($0200);
sector-data commands ($61-$63) wait on EHST ($0080).

### DATASTAT Bits

| Bit | Name | Meaning |
|-----|------|---------|
| 0 | DIR | Transfer direction: 0=CD Block to host, 1=host to CD Block |
| 1 | FUL | FIFO full |
| 2 | EMP | FIFO empty |

### Command/Response Protocol

Commands are sent by writing 4 words to CR1-CR4. The command code is in
CR1 bits 15-8 (high byte). After writing CR1-CR4, the host waits for
HIRQREQ bit 0 (CMOK) to be set, then reads the 4-word response from
CR1-CR4.

Command/response time target: 50-70 us. Interrupts must be disabled during
command/response exchange.

### CD Drive Status (returned in responses)

Status occupies bits 11-8 of CR1 (the low nibble of the high byte) in
command responses. The high nibble (bits 15-12) holds status flags.

| Value | Status | Meaning |
|-------|--------|---------|
| $00 | BUSY | Status in transition |
| $01 | PAUSE | Paused |
| $02 | STANDBY | Drive stopped |
| $03 | PLAY | CD is playing |
| $04 | SEEK | Seeking |
| $05 | SCAN | Scanning |
| $06 | OPEN | Tray is open |
| $07 | NODISC | No disc |
| $08 | RETRY | Retrying read |
| $09 | ERROR | Read data error |
| $0A | FATAL | Fatal error (must reset hardware) |


## CD Block Command Codes

Commands sent via CR1-CR4. CR1 high byte = command code.

### Common CD Block (1.x)

Verified by reading the first `MOV #imm,R3` of each command builder
(the byte that becomes CR1's high byte):

| Code | Function | Name | sub | Description |
|------|----------|------|-----|-------------|
| $00 | 1.1 | Get CD Status | sub_33BA | Get current status and report |
| $01 | 1.2 | Get Hardware Info | sub_3C66 | CD Block version, hardware flags, partial RAM size |
| $02 | 1.3 | Get TOC | sub_3CB4 | Read TOC (102 entries, 408 bytes) |
| $03 | 1.4 | Get Session Info | sub_3D0E | Get session info (multisession) |
| $04 | 1.5 | Initialize CD System | sub_3D50 | Init CD Block: flags, standby time, ECC reps, retries |
| $05 | 1.6 | Open Tray | sub_3DFC | Open the disc tray |
| $06 | 1.7 | End Data Transfer | sub_3E8C | End data transfer, returns transfer size |

### CD Drive (2.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $10 | 2.1 | Play Disc | Play by FAD range or track designation |
| $11 | 2.2 | Seek Disc | Seek to FAD or track position |
| $12 | 2.3 | Scan Disc | Fast forward/reverse scan |

### Subcode (3.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $20 | 3.1 | Get Subcode | Get subchannel data; CR1 low byte selects type (0 = Q, 10 bytes; 1 = R-W, 24 bytes) |

### CD Device (4.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $30 | 4.1 | Set CD Device Connection | Set filter connection for CD device |
| $31 | 4.2 | Get CD Device Connection | Get current filter connection |
| $32 | 4.3 | Get Last Buffer Destination | Get last buffer partition with stored sector |

### Selector (5.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $40 | 5.1 | Set Filter Range | Set FAD range for filter |
| $41 | 5.2 | Get Filter Range | Get current filter FAD range |
| $42 | 5.3 | Set Filter Subheader Conditions | Set subheader match criteria |
| $43 | 5.4 | Get Filter Subheader Conditions | Get subheader match criteria |
| $44 | 5.5 | Set Filter Mode | Set filter operating mode |
| $46 | 5.7 | Set Filter Connection | Set filter chain connections |
| $48 | 5.9 | Reset Selector | Reset filter/partition |

### Buffer Information (6.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $50 | 6.1 | Get Buffer Size | Get CD buffer size (in sectors) |
| $51 | 6.2 | Get Sector Number | Get sector count in partition |
| $52 | 6.3 | Calculate Actual Size | Calculate actual data size |
| $53 | 6.4 | Get Actual Size | Get result of calculate |
| $54 | 6.5 | Get Sector Info | Get FAD, file number, channel, submode, coding info |

### Buffer I/O (7.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $60 | 7.1 | Set Sector Length | Set sector data length for transfer |
| $61 | 7.2 | Get Sector Data | Transfer sector data to host via DATATRNS |
| $62 | 7.3 | Delete Sector Data | Delete sectors from partition |
| $63 | 7.4 | Get Then Delete Sector Data | Transfer and delete |
| $64 | 7.5 | Put Sector Data | Write sector data from host |
| $65 | 7.6 | Copy Sector Data | Copy sectors between partitions |
| $66 | 7.7 | Move Sector Data | Move sectors between partitions |
| $67 | 7.8 | Get Copy Error | Get error from copy/move |

### CD Block File System (8.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $70 | 8.1 | Change Directory | Change to directory by file ID |
| $71 | 8.2 | Read Directory | Read directory and hold file info |
| $72 | 8.3 | Get File System Scope | Get file info range |
| $73 | 8.4 | Get File Info | Get file info by ID (transferred via DATATRNS) |
| $74 | 8.5 | Read File | Start reading file by filter + file ID + offset |
| $75 | 8.6 | Abort File | Abort the current file read |

### System Functions (10.x)

| Code | Function | Name | Description |
|------|----------|------|-------------|
| $E0 | 10.1 | Authenticate Disc | Check disc authentication |
| $E1 | 10.2 | Get Auth Status | Get authentication result |

### CR1-CR4 Command Packet Encoding

Derived from BIOS disassembly of each command builder function. All
registers are 16-bit. Notation: `[field:bits]` for bit fields within
a register. Response CR1 high nibble always contains drive status.

#### Common CD Block

**$00 Get CD Status** (sub_33BA)
```
CMD:  CR1=$0000  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  CR1=[stat_flags:4][status:4][cd_flags:4][repeat:4]  (high byte = status, low byte = flgrep)
      CR2=[ctrl_addr:8][track_no:8]
      CR3=[index:8][fad_hi:8]
      CR4=[fad_mid:8][fad_lo:8]  (24-bit FAD = CR3 low byte : CR4)
```

**$01 Get Hardware Info** (sub_3C66)
```
CMD:  CR1=$0100  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  CR1=[status:16]
      CR2=[hw_flag:8][version:8]
      CR3=[??:8][mpeg_ver:8]
      CR4=[drive_version:8][drive_revision:8]
```
hw_flag bit 0: connected to CD emulator (0=real drive)

**$02 Get TOC** (sub_3CB4)
```
CMD:  CR1=$0200  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  Data transfer via DATATRNS (408 bytes = 102 x 4-byte entries)
```

**$03 Get Session Info** (sub_3D0E)
```
CMD:  CR1=[$03][sesno:8]  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  CR3-CR4 = session info (32 bits)
```

**$04 Initialize CD System** (sub_3D50)
```
CMD:  CR1=[$04][initflg:8]  CR2=[standby_time:16]  CR3=$0000  CR4=[ecc_num:8][retry_num:8]
RSP:  CR1=[status:16]
```
initflg bits: 0=soft reset, 1=RW subcode, 2=mode2 subheader,
3=form2 retry, 4=fixed speed, 5=change init flag

**$06 End Data Transfer** (sub_3E8C)
```
CMD:  CR1=$0600  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  CR1=[status:8][word_count_hi:8]  CR2=[word_count_lo:16] (transfer word count = CR1 low byte : CR2, masked to 24 bits / $00FFFFFF)
```

#### CD Drive

**$10 Play Disc** (sub_3FAC)
```
FAD mode:
  CR1=[$10][$80|start_fad_hi:8]  CR2=[start_fad_lo:16]
  CR3=[repeat:8][$80|end_fad_hi:8]  CR4=[end_fad_lo:16]

Track mode:
  CR1=[$10][$00]  CR2=[start_track:8][start_index:8]
  CR3=[repeat:8][$00]  CR4=[end_track:8][end_index:8]

No change mode:
  CR1=[$10][$FF]  CR2=$FFFF  CR3=[repeat:8][$FF]  CR4=$FFFF
```
Repeat count in CR3 high byte (0-15, $F = max).

**$11 Seek Disc** (sub_406A)
```
FAD mode:
  CR1=[$11][$80|fad_hi:8]  CR2=[fad_lo:16]  CR3=$0000  CR4=$0000

Track mode:
  CR1=[$11][$00]  CR2=[track:8][index:8]  CR3=$0000  CR4=$0000

Home (stop):
  CR1=[$11][$FF]  CR2=$FFFF  CR3=$0000  CR4=$0000

Pause (no change):
  CR1=[$11][$00]  CR2=$0000  CR3=$0000  CR4=$0000
```

#### CD Device

**$30 Set CD Device Connection** (sub_3EEC)
```
CMD:  CR1=$3000  CR2=$0000  CR3=[filtno:8][$00]  CR4=$0000
HIRQ: Waits for $0040
```

#### Selector

**$48 Reset Selector** (sub_461A)
```
CMD:  CR1=[$48][flags:8]  CR2=$0000  CR3=[bufno:8][$00]  CR4=$0000
HIRQ: Waits for $0040
```

#### Buffer Information

**$50 Get Buffer Size** (sub_3410)
```
CMD:  CR1=$5000  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  CR2=[free_blocks:16]
      CR3=[max_selectors:8][??:8]
      CR4=[total_blocks:16]
```

**$51 Get Sector Number** (sub_346A)
```
CMD:  CR1=$5100  CR2=$0000  CR3=[bufno:8][$00]  CR4=$0000
RSP:  CR4=[sector_count:16]
```

#### Buffer I/O

**$60 Set Sector Length** (sub_36CE)
```
CMD:  CR1=[$60][get_len:8]  CR2=[put_len:8][$00]  CR3=$0000  CR4=$0000
HIRQ: Waits for $0040
```
get_len/put_len: sector data size selector for transfers

**$61 Get Sector Data** (sub_3710)
```
CMD:  CR1=$6100  CR2=[sector_pos:16]  CR3=[bufno:8][$00]  CR4=[sector_num:16]
HIRQ: Waits for $0080 (EHST)
```

**$62 Delete Sector Data** (sub_3768)
```
CMD:  CR1=$6200  CR2=[sector_pos:16]  CR3=[bufno:8][$00]  CR4=[sector_num:16]
HIRQ: Waits for $0080 (EHST)
```

**$63 Get Then Delete Sector Data** (sub_37B4)
```
CMD:  CR1=$6300  CR2=[sector_pos:16]  CR3=[bufno:8][$00]  CR4=[sector_num:16]
HIRQ: Waits for $0080 (EHST)
```

#### File System

**$70 Change Directory** (sub_3940)
```
CMD:  CR1=$7000  CR2=$0000  CR3=[filtno:8][fid_hi:8]  CR4=[fid_lo:16]
HIRQ: Waits for $0200
```
Note: filtno is the CR3 high byte; fid is a 24-bit value spanning CR3
low byte and CR4.

**$71 Read Directory** (sub_397C)
```
CMD:  CR1=$7100  CR2=$0000  CR3=[filtno:8][fid_hi:8]  CR4=[fid_lo:16]
HIRQ: Waits for $0200
```
Note: filtno is the CR3 high byte; fid is a 24-bit value spanning CR3
low byte and CR4.

**$72 Get File System Scope** (sub_39C4)
```
CMD:  CR1=$7200  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  CR2=[infnum:16]  CR3=[drend:8][fid_hi:8]  CR4=[fid_lo:16]
```
Note: infnum is read from CR2 (16-bit). drend (directory-end flag) is the
CR3 high byte. fid is read as a 24-bit value (CR3 low byte + CR4, masked
$00FFFFFF).

**$73 Get File Info** (sub_3A38)
```
CMD:  CR1=$7300  CR2=$0000  CR3=[$00][fid_hi:8]  CR4=[fid_lo:16]
RSP:  Data transfer (file info records, 12 bytes each)
```
Note: fid is a 24-bit value spanning CR3 low byte and CR4.

#### System Functions

**$E0 Authenticate Disc**
```
CMD:  CR1=$E000  CR2=$0000  CR3=$0000  CR4=$0000
```

**$E1 Get Auth Status**
```
CMD:  CR1=$E100  CR2=$0000  CR3=$0000  CR4=$0000
RSP:  CR2=[auth_status:16]
```

Note: the BIOS does not issue $E0/$E1 from its main code region; the
$75 Abort File command builder at sub_3AEC is sometimes mistaken for
an $E0 builder. Authentication is performed by the security-check
blob copied from BIOS ROM $040000 to WRAM-H $06020000 (see CD Game
Start).

### CDC_SysIsConnect - CD Block Presence Check

The BIOS verifies CD Block hardware presence by reading the CR result
registers (CR1-CR4 at $25890018-$25890024) without sending a command.
The function at sub_4A9C reads all 4 CR registers via sub_42EC, then
calls sub_22E8 to compare 8 bytes against a signature stored at BIOS
ROM offset $4DB0:

```
$004DB0: 00 43 44 42 4C 4F 43 4B    "\x00CDBLOCK"
```

As 16-bit register values:
- CR1 = $0043
- CR2 = $4442
- CR3 = $4C4F
- CR4 = $434B

On real hardware, the CD Block (SH-1 microcontroller) autonomously runs
an initialization sequence after power-on. When complete, it places the
"CDBLOCK" signature in the result registers and sets HIRQ flags
(CMOK, DCHG, and others). The Initialize CD System command ($04) also
produces this signature in its response.

If the comparison fails (no CD Block hardware present), the BIOS skips
disc detection entirely and proceeds without CD support.

### BIOS Boot CD Block Command Sequence

The boot CD path is driven by a state machine (the sub_2650 state
handlers, sub_29D4, and the sub_2F48 load pump) rather than a fixed
linear packet sequence; the list below is the set of CD Block commands
the boot path issues, in roughly the phase order the machine follows:

1. **CDC_SysIsConnect** - verify CD Block is present (reads CR registers, checks for "CDBLOCK" signature)
2. **Get CD Status** ($00) - poll drive status until ready
3. **Initialize CD System** ($04) - init with flags, standby time
4. **Open Tray** ($05) - tray / error-recovery handling
5. **Reset Selector** ($48) - reset filter / partition state
6. **Set CD Device Connection** ($30) - connect the CD device to a filter
7. **Get TOC** ($02) - read table of contents
8. **Get Session Info** ($03) - check session information
9. **Change Directory** ($70) - navigate to the root directory
10. **Get File System Scope** ($72) - get the file-info range
11. **Get File Info** ($73) - read IP.BIN file information (File ID 2)
12. **Read File** ($74) - start reading IP.BIN into the CD buffer
13. **Set Sector Length** ($60) - set the host transfer sector size
14. **Get Copy Error** ($67) - check the transfer result
15. **End Data Transfer** ($06) - complete the transfer
16. **Abort File** ($75) - close the file operation

The sector payload is moved into Work RAM by reading the DATATRNS
register directly, not by a Get Sector Data ($61) command. The BIOS
does not issue Read Directory ($71) during boot.

After transfer, the BIOS validates the header ("SEGA SEGASATURN "), checks
the region code, copies the security check code to Work RAM, and runs
disc authentication.


