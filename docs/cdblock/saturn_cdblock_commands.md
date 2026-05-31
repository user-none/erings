# Saturn CD Block Command Register Reference

CD Communication Interface PDF (ST-38-R1-121093) and CDB-106 firmware
disassembly notes.


## CD Block Registers

The CD Block registers are in the A-Bus CS2 address space
(0x05800000-0x058FFFFF, cache-through at 0x25800000-0x258FFFFF).

### CS2 Address Space Layout

The DATATRNS register and the command/status registers are at different
offsets within the CS2 space:

| CS2 Offset | Mirror     | Register | R/W | Description                    |
|------------|------------|----------|-----|--------------------------------|
| 0x18000    | 0x98000    | DATATRNS | R/W | Data Transfer FIFO             |
| 0x90008    | 0x98008    | HIRQ     | R/W | Interrupt Status Register      |
| 0x9000C    | 0x9800C    | HIRQMASK | R/W | Interrupt Mask Register        |
| 0x90018    | 0x98018    | CR1      | R/W | Command/Result Register 1      |
| 0x9001C    | 0x9801C    | CR2      | R/W | Command/Result Register 2      |
| 0x90020    | 0x98020    | CR3      | R/W | Command/Result Register 3      |
| 0x90024    | 0x98024    | CR4      | R/W | Command/Result Register 4      |

DATATRNS is at CS2 offset 0x18000 (absolute 0x25818000), mirrored at
0x98000 (absolute 0x25898000) via bit 19.

The command/status registers at 0x90000+ are mirrored at 0x98000+ via
a 0x08000 (32 KB) mirror. The register offset within the 64-byte block
is extracted as (cs2_offset & 0x3F).

Writing to CR4 (offset 0x25 within the register block) triggers command
execution.

All registers are 16-bit but can be accessed as 32-bit longwords.
DATATRNS returns consecutive FIFO words on 32-bit reads.

### DATASTAT bits

| Bit | Name | Description                               |
|-----|------|-------------------------------------------|
| 0   | DIR  | 0 = CD Block to host, 1 = host to CD Block|
| 1   | FUL  | 1 = FIFO is full                          |
| 2   | EMP  | 1 = FIFO is empty                         |

### HIRQ Flag Bits

| Bit | Name | Description                                         |
|-----|------|-----------------------------------------------------|
| 0   | CMOK | Command OK - response is set, can issue new command |
| 1   | DRDY | Data transfer preparation complete                  |
| 2   | CSCT | 1 sector read from CD-ROM confirmed                 |
| 3   | BFUL | CD buffer full                                      |
| 4   | PEND | Play ended (FAD outside play area)                  |
| 5   | DCHG | Disc changed (tray opened)                          |
| 6   | ESEL | Selector operation ended                            |
| 7   | EHST | Host I/O operation ended                            |
| 8   | ECPY | Copy/move operation ended                           |
| 9   | EFLS | File system operation ended                         |
| 10  | SCDQ | Subcode Q update                                    |
| 11  | MPED | MPEG operation ended                                |

Note: The C library PDF (ST-38-R1-121093) only documents bits 0-6 and puts
MPEG at bit 6. Production CDB-106 firmware reassigned MPEG to bit 11 (MPED)
and added ESEL/EHST/ECPY/EFLS/SCDQ at bits 6-10 for selector, host I/O,
copy, file-system, and subcode-Q completion events. The table above is the
production hardware layout; the PDF's bit 6 = MPEG is the older preliminary
spec.

Only write-0-to-clear on HIRQ (writing a 0 bit clears it, writing 1 preserves).

### Power-On Reset Values

On reset the CR registers contain "CDBLOCK" signature:

| Register | Value  | Contents         |
|----------|--------|------------------|
| CR1      | 0x0043 | status=0, 'C'    |
| CR2      | 0x4442 | 'D', 'B'         |
| CR3      | 0x4C4F | 'L', 'O'         |
| CR4      | 0x434B | 'C', 'K'         |


## CD Status Response Format

Most commands return "CD status data" in the CR registers. This is the
standard response format:

| Register | Contents                                                |
|----------|---------------------------------------------------------|
| CR1      | Status(high byte), CD flag/repeat(low byte - bits 7-4: flag, bits 3-0: repeat) |
| CR2      | CTRL/ADR(high byte), Track number(low byte)             |
| CR3      | Index(high byte), Upper FAD byte(low byte)              |
| CR4      | Lower FAD word                                          |

### Status Codes (CR1 high byte)

The CR1 high byte is structured as: status code in the low nibble + flag
bits in the high nibble.

Status code (low 5 bits):

| Value | Status   | Meaning                              |
|-------|----------|--------------------------------------|
| 0x00  | BUSY     | Status in transition                 |
| 0x01  | PAUSE    | Paused                               |
| 0x02  | STANDBY  | Drive is stopped                     |
| 0x03  | PLAY     | CD is playing                        |
| 0x04  | SEEK     | Seeking                              |
| 0x05  | SCAN     | Scanning                             |
| 0x06  | OPEN     | Tray is open                         |
| 0x07  | NODISC   | No disc                              |
| 0x08  | RETRY    | Retrying read                        |
| 0x09  | ERROR    | Read data error                      |
| 0x0A  | FATAL    | Fatal error (must reset hardware)    |
| 0xFF  | REJECT   | Command rejected (entire byte, not a code) |

Flag bits OR'd into the high nibble:
- Bit 5 (0x20): periodic response (set when the CD Block updates CR1-CR4
  on its own between commands)
- Bit 7 (0x80): WAIT (command received but not yet executed)

Note: The C library PDF (ST-38-R1-121093) documents the status codes as
non-contiguous `CdcRet` macro values (0x00, 0x10, 0x11, 0x20, 0x21, 0x22,
0x30, 0x31, 0x32, 0x33, 0x34). Those are the C library's translated return
codes, not the raw register values. The values above are what the CDB-106
firmware actually places in the CR1 high byte and what the BIOS reads.


## Periodic Response

When not handling a command the CD Block periodically updates the CR
registers with current CD status data and sets bit 5 (0x20) in the
status byte of CR1. Periodic responses do not occur between command
issue and response read.


## Command Reference

Command byte is in CR1 high byte. Parameters are packed into CR1-CR4 as
described below. HB = high byte, LB = low byte of a 16-bit register.

### 0x00 - Get CD Status

| CR1 | 0x0000 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns: CD status data. HIRQ: N/A

### 0x01 - Get Hardware Info

| CR1 | 0x0100 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB)                              |
| CR2 | Hardware flag(HB), Hardware version(LB) |
| CR3 | MPEG version(LB)                        |
| CR4 | Drive version(HB), Drive revision(LB)   |

HIRQ: N/A. MPEG version returns 0 if card not authenticated.

### 0x02 - Get TOC

| CR1 | 0x0200 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB)            |
| CR2 | TOC size in words     |
| CR3 | 0x0000                |
| CR4 | 0x0000                |

HIRQ: DRDY (when TOC data is ready for transfer via DATATRNS)

TOC is 102 entries x 4 bytes = 204 words. Each entry is 2 words:
word 0 = CTRL/ADR(HB) | upper FAD byte(LB), word 1 = lower FAD word.
Entries 0-98 = tracks 1-99, entry 99 = first track info,
entry 100 = last track info, entry 101 = lead-out.

### 0x03 - Get Session Info

| CR1 | 0x03, session number(LB) - 0 = all sessions |
| CR2 | 0x0000                                       |
| CR3 | 0x0000                                       |
| CR4 | 0x0000                                       |

Returns:

| CR1 | Status(HB)                                       |
| CR2 | 0x0000                                           |
| CR3 | Session count or number(HB), upper LBA byte(LB)  |
| CR4 | Lower LBA word                                   |

HIRQ: N/A

### 0x04 - Initialize CD System

| CR1 | 0x04, init flags(LB)             |
| CR2 | Standby time (seconds, 0=180s default, 0xFFFF=no change) |
| CR3 | 0x0000                           |
| CR4 | ECC repetitions(HB), retry count(LB) |

Init flag bits:
- Bit 0: CD Block software reset
- Bit 1: R-W subcode decode ON
- Bit 2: Recognize mode 2 subheader
- Bit 3: Form 2 read retry ON
- Bit 4: CD-ROM data read fixed speed (1=1x, 0=2x)
- Bit 5: Request for change in init flag

Returns: CD status data (briefly shows BUSY).
HIRQ: ESEL, EHST, ECPY, EFLS (only if software reset bit was set)

### 0x05 - Open Tray

| CR1 | 0x0500 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns: CD status data. HIRQ: EFLS, DCHG.
Drive goes to BUSY until disc inserted or Init command issued.
Authentication status is reset.

### 0x06 - End Data Transfer

| CR1 | 0x0600 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB), upper transfer word count byte(LB) |
| CR2 | Lower transfer word count                       |
| CR3 | 0x0000                                          |
| CR4 | 0x0000                                          |

Transfer word count is 0xFFFFFF if no transfer was in progress.
HIRQ: EHST (only for Get Sector, Get Then Delete, Put Sector transfers)

### 0x10 - Play Disc

| CR1 | 0x10, upper start position byte(LB)             |
| CR2 | Lower start position word                       |
| CR3 | Play mode(HB), upper end position byte(LB)      |
| CR4 | Lower end position word                         |

Position encoding: If bit 23 is set, value is FAD. Otherwise track number.

Play mode byte:
- Bits 6-0: Maximum repeats (0=once, 1-14=repeat count, 0x0F=endless, 0x7F=no change)
- Bit 7: Pickup movement (0=move, 1=don't move)

Returns: CD status data (changes to SEEK then PLAY).
HIRQ: CSCT set per sector read. BFUL set if buffer fills (status -> PAUSE).

### 0x11 - Seek Disc

| CR1 | 0x11, upper position byte(LB) |
| CR2 | Lower position word           |
| CR3 | 0x0000                        |
| CR4 | 0x0000                        |

Position encoding: If bit 23 set, value is FAD. Otherwise track number.
FAD 0xFFFFFF = pause at current position (stop playing).
Invalid track = status immediately changes to STANDBY.

Returns: CD status data. HIRQ: N/A

### 0x12 - Scan Disc

| CR1 | 0x12, scan direction(LB) |
| CR2 | 0x0000                   |
| CR3 | 0x0000                   |
| CR4 | 0x0000                   |

Direction: 0 = forward, 1 = reverse.
Returns: CD status data. HIRQ: unknown.

### 0x20 - Get Subcode Q/RW

| CR1 | 0x20, type(LB) - 0=Q, 1=R-W |
| CR2 | 0x0000                       |
| CR3 | 0x0000                       |
| CR4 | 0x0000                       |

Returns:

| CR1 | Status(HB)          |
| CR2 | Data size in words  |
| CR3 | 0x0000              |
| CR4 | Subcode flags       |

Size: 5 words for Q, 12 words for R-W. Data via DATATRNS.
HIRQ: DRDY (when data ready)

### 0x30 - Set CD Device Connection

| CR1 | 0x3000                   |
| CR2 | 0x0000                   |
| CR3 | Filter number(HB)        |
| CR4 | 0x0000                   |

Connects CD device output to specified filter. Must be done before
any data can be received. 0xFF = disconnect.

Returns: CD status data. HIRQ: ESEL

### 0x31 - Get CD Device Connection

| CR1 | 0x3100 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB)          |
| CR2 | 0x0000              |
| CR3 | Filter number(HB)   |
| CR4 | 0x0000              |

Default after software reset: 0xFF. HIRQ: N/A

### 0x32 - Get Last Buffer Destination

| CR1 | 0x3200 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB)          |
| CR2 | 0x0000              |
| CR3 | Buffer number(HB)   |
| CR4 | 0x0000              |

HIRQ: N/A

### 0x40 - Set Filter Range

| CR1 | 0x40, upper FAD byte(LB)                  |
| CR2 | Lower FAD word                            |
| CR3 | Filter number(HB), upper range byte(LB)   |
| CR4 | Lower range word                          |

Sets the FAD range for the specified filter. Sectors within range pass
to the true connection, others to false connection.

Returns: CD status data. HIRQ: ESEL

### 0x41 - Get Filter Range

| CR1 | 0x4100                   |
| CR2 | 0x0000                   |
| CR3 | Filter number(HB)        |
| CR4 | 0x0000                   |

Returns: FAD and range in same format as Set Filter Range.
HIRQ: ESEL

### 0x42 - Set Filter Subheader Conditions

| CR1 | 0x42, channel number(LB)                             |
| CR2 | Submode mask(HB), coding info mask(LB)               |
| CR3 | Filter number(HB), file ID(LB)                       |
| CR4 | Submode value(HB), coding info value(LB)             |

Used for Mode 2 (CD-ROM XA) sector filtering.
Filter condition: (submode & mask) == value AND (codinginfo & cimask) == civalue

Returns: CD status data. HIRQ: ESEL

### 0x43 - Get Filter Subheader Conditions

| CR1 | 0x4300                   |
| CR2 | 0x0000                   |
| CR3 | Filter number(HB)        |
| CR4 | 0x0000                   |

Returns:

| CR1 | Status(HB), channel(LB)                    |
| CR2 | Submode mask(HB), coding info mask(LB)     |
| CR3 | File ID(LB)                                |
| CR4 | Submode value(HB), coding info value(LB)   |

HIRQ: ESEL

### 0x44 - Set Filter Mode

| CR1 | 0x44, mode(LB)           |
| CR2 | 0x0000                   |
| CR3 | Filter number(HB)        |
| CR4 | 0x0000                   |

Mode bits:
- Bit 0: Valid file number (FN) selection
- Bit 1: Valid channel number (CN) selection
- Bit 2: Valid submode (SM) selection
- Bit 3: Valid coding information (CI) selection
- Bit 4: Reverse subheader status
- Bit 5: Reserved (0)
- Bit 6: Valid FAD range selection
- Bit 7: Initialize filter conditions (returns to defaults)

Note: The C library PDF (ST-38-R1-121093) documents bit 5 as "Valid sector
range" and bit 6 as reserved. The CDB-106 hardware register actually uses
bit 6 for the FAD-range condition; the C-library wrapper presumably
translated between its own bit layout and the hardware register. The
hardware layout above is what reaches the register and what the firmware
acts on.

Returns: CD status data. HIRQ: ESEL

### 0x45 - Get Filter Mode

| CR1 | 0x4500                   |
| CR2 | 0x0000                   |
| CR3 | Filter number(HB)        |
| CR4 | 0x0000                   |

Returns:

| CR1 | Status(HB), mode(LB) |
| CR2 | 0x0000                |
| CR3 | 0x0000                |
| CR4 | 0x0000                |

HIRQ: ESEL

### 0x46 - Set Filter Connection

| CR1 | 0x46, which connection to set(LB) |
| CR2 | True condition target(HB), false condition target(LB) |
| CR3 | Filter number(HB)                |
| CR4 | 0x0000                           |

Which connection bits:
- Bit 0: Set true output connector (connect to buffer partition)
- Bit 1: Set false output connector (connect to another filter)

Target values are buffer partition numbers (true) or filter numbers (false).
0xFF = disconnect.

Returns: CD status data. HIRQ: ESEL

### 0x47 - Get Filter Connection

| CR1 | 0x4700                   |
| CR2 | 0x0000                   |
| CR3 | Filter number(HB)        |
| CR4 | 0x0000                   |

Returns:

| CR1 | Status(HB)                                             |
| CR2 | True condition target(HB), false condition target(LB) |
| CR3 | 0x0000                                                |
| CR4 | 0x0000                                                |

HIRQ: ESEL

### 0x48 - Reset Selector

| CR1 | 0x48, reset flags(LB)    |
| CR2 | 0x0000                   |
| CR3 | Buffer number(HB)        |
| CR4 | 0x0000                   |

Reset flag bits:
- Bit 2: Initialize all buffer partition data
- Bit 3: Initialize all partition output connectors
- Bit 4: Initialize all filter conditions
- Bit 5: Initialize all filter input connectors (CD device connection)
- Bit 6: Initialize all true output connectors (filter N -> partition N)
- Bit 7: Initialize all false output connectors (disconnect)

Note: The C library PDF (ST-38-R1-121093) documents these as bits 0-5.
The CDB-106 hardware register uses bits 2-7; bits 0-1 are unused at the
register level. The C-library wrapper presumably translated between its
own bit layout and the hardware register.

Buffer number is only used when reset flags = 0 (reset single partition).

Returns: CD status data. HIRQ: ESEL

### 0x50 - Get Buffer Size

| CR1 | 0x5000 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB)              |
| CR2 | Block free space         |
| CR3 | Maximum selectors(HB)   |
| CR4 | Maximum blocks           |

HIRQ: N/A

### 0x51 - Get Sector Number

| CR1 | 0x5100                   |
| CR2 | 0x0000                   |
| CR3 | Buffer number(HB)        |
| CR4 | 0x0000                   |

Returns:

| CR1 | Status(HB)           |
| CR2 | 0x0000               |
| CR3 | 0x0000               |
| CR4 | Number of blocks     |

HIRQ: DRDY

### 0x52 - Calculate Actual Size

| CR1 | 0x5200                   |
| CR2 | Sector offset            |
| CR3 | Buffer number(HB)        |
| CR4 | Sector count             |

Calculates byte size of specified sector range. Result retrieved with 0x53.
Returns: CD status data. HIRQ: ESEL

### 0x53 - Get Actual Size

| CR1 | 0x5300 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB), upper calculated size byte(LB) |
| CR2 | Lower calculated size word                  |
| CR3 | 0x0000                                     |
| CR4 | 0x0000                                     |

HIRQ: ESEL

### 0x54 - Get Sector Info

| CR1 | 0x5400                   |
| CR2 | Sector number(LB)        |
| CR3 | Buffer number(HB)        |
| CR4 | 0x0000                   |

Returns:

| CR1 | Status(HB), upper FAD byte(LB)                        |
| CR2 | Lower FAD word                                        |
| CR3 | File number(HB), channel number(LB)                   |
| CR4 | Submode(HB), coding information(LB)                   |

Rejects if buffer number > max selectors or sector number > sectors in buffer.
HIRQ: N/A

### 0x55 - Execute FAD Search

| CR1 | 0x5500                                    |
| CR2 | Sector position                           |
| CR3 | Buffer number(HB), upper FAD byte(LB)     |
| CR4 | Lower FAD word                            |

Searches for sector with specified FAD in buffer partition.
Returns: CD status data. HIRQ: ESEL

### 0x56 - Get FAD Search Results

| CR1 | 0x5600 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB)                                |
| CR2 | Sector position                           |
| CR3 | Buffer number(HB), upper FAD byte(LB)     |
| CR4 | Lower FAD word                            |

HIRQ: N/A

### 0x60 - Set Sector Length

| CR1 | 0x60, get sector length type(LB) |
| CR2 | Put sector length type(HB)       |
| CR3 | 0x0000                           |
| CR4 | 0x0000                           |

Sector length types:
- 0 = 2048 bytes (user data only)
- 1 = 2336 bytes
- 2 = 2340 bytes
- 3 = 2352 bytes (full sector)

Returns: CD status data. HIRQ: ESEL

### 0x61 - Get Sector Data

| CR1 | 0x6100                   |
| CR2 | Sector offset            |
| CR3 | Buffer number(HB)        |
| CR4 | Sector count             |

Prepares sector data for transfer via DATATRNS. After transfer, call
End Data Transfer (0x06).

Returns: CD status data (briefly shows data transfer request).
HIRQ: EHST, DRDY

### 0x62 - Delete Sector Data

| CR1 | 0x6200                   |
| CR2 | Sector position          |
| CR3 | Buffer number(HB)        |
| CR4 | Sector count             |

Sector position 0xFFFF = delete from end. Sector count 0xFFFF = delete to end.
Returns: CD status data. HIRQ: EHST

### 0x63 - Get Then Delete Sector Data

| CR1 | 0x6300                   |
| CR2 | Sector offset            |
| CR3 | Buffer number(HB)        |
| CR4 | Sector count             |

Combines Get Sector Data and Delete Sector Data. After DATATRNS transfer
is complete and End Data Transfer is called, the sectors are deleted.

Returns: CD status data. HIRQ: EHST, DRDY

### 0x64 - Put Sector Data

| CR1 | 0x6400                   |
| CR2 | 0x0000                   |
| CR3 | Buffer number(HB)        |
| CR4 | Sector count             |

Writes sector data from host to CD buffer via DATATRNS.
Returns: CD status data. HIRQ: DRDY, EHST (only if allocation fails)

### 0x65 - Copy Sector Data

| CR1 | 0x65, destination filter number(LB) |
| CR2 | Sector offset                       |
| CR3 | Source buffer number(HB)            |
| CR4 | Sector count                        |

Copies sectors through filter chain (proper filtering applied).
Rejects if buffer full.

Returns: CD status data. HIRQ: ECPY

### 0x66 - Move Sector Data

| CR1 | 0x66, destination filter number(LB) |
| CR2 | Sector offset                       |
| CR3 | Source buffer number(HB)            |
| CR4 | Sector count                        |

Moves sectors through filter chain (same as copy but removes from source).
Returns: CD status data. HIRQ: ECPY

### 0x67 - Get Copy Error

| CR1 | 0x6700 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB), error code(LB) |
| CR2 | 0x0000                     |
| CR3 | 0x0000                     |
| CR4 | 0x0000                     |

HIRQ: N/A

### 0x70 - Change Directory

| CR1 | 0x7000                                          |
| CR2 | 0x0000                                          |
| CR3 | Filter number(HB), upper file ID byte(LB)       |
| CR4 | Lower file ID word                              |

File ID 0xFFFFFF = root directory. Filter number specifies which filter
the CD Block file system uses internally for reading.

Returns: CD status data. HIRQ: EFLS

### 0x71 - Read Directory

| CR1 | 0x7100                                          |
| CR2 | 0x0000                                          |
| CR3 | Filter number(HB), upper file ID byte(LB)       |
| CR4 | Lower file ID word                              |

Same format as Change Directory. Directory data stored internally,
retrievable via Get File Info.

Returns: CD status data. HIRQ: EFLS

### 0x72 - Get File System Scope

| CR1 | 0x7200 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB)                                            |
| CR2 | Number of files                                       |
| CR3 | Directory end offset(HB), upper file ID start(LB)    |
| CR4 | Lower file ID start word                              |

HIRQ: EFLS

### 0x73 - Get File Info

| CR1 | 0x7300                           |
| CR2 | 0x0000                           |
| CR3 | Upper file ID byte(LB)           |
| CR4 | Lower file ID word               |

File ID 0xFFFFFF = get all file info (transferred via DATATRNS as 12 bytes
per file: 4 bytes FAD, 4 bytes size, 1 byte unit gap, 1 byte file number,
1 byte attributes, 1 byte padding).

Single file: returns file info in CR1-CR4.
All files: data via DATATRNS.

Returns: HIRQ: DRDY

### 0x74 - Read File

| CR1 | 0x74, upper offset byte(LB)                     |
| CR2 | Lower offset word                               |
| CR3 | Filter number(HB), upper file ID byte(LB)       |
| CR4 | Lower file ID word                              |

Offset is in sector units. Filter number specifies which filter to route
the read data through.

Returns: CD status data. HIRQ: EFLS

### 0x75 - Abort File

| CR1 | 0x7500 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns: CD status data. HIRQ: EFLS


## Authentication Commands

### 0xE0 - Authenticate Device

| CR1 | 0xE000 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Starts disc authentication. Drive seeks to inner ring area for copy
protection check.

Returns: CD status data. HIRQ: EFLS (when authentication complete).
Status briefly shows BUSY, then returns to PAUSE.

### 0xE1 - Get Device Authentication Status

| CR1 | 0xE100 |
| CR2 | 0x0000 |
| CR3 | 0x0000 |
| CR4 | 0x0000 |

Returns:

| CR1 | Status(HB) |
| CR2 | Disc type / auth status |
| CR3 | 0x0000     |
| CR4 | 0x0000     |

Auth status values:
- 0x00: No disc or not authenticated
- 0x01: Audio CD (unlocked)
- 0x02: Regular data CD, not Saturn (unlocked)
- 0x03: Copied/pirated Saturn disc (locked)
- 0x04: Original Saturn disc (unlocked)

HIRQ: N/A

### 0xE2 - Get MPEG Card Boot ROM

Used for VCD check. Retrieves 2 sectors from MPEG card ROM.
Not relevant for game compatibility.


## CD Buffer Architecture

The CD Block has internal RAM for up to 200 accessible sectors (202 total,
2 reserved). Sectors are 2352 bytes each.

### Selectors (24 total, numbered 0-23)

Each selector consists of:
- **Filter**: Conditions for accepting/rejecting sectors (FAD range, subheader match)
- **Buffer partition**: Storage area for sectors that pass the filter

### Filter Chain

Sectors flow: CD Device -> Filter -> Buffer Partition

Filter outputs:
- True output: sector matches filter conditions -> connected buffer partition
- False output: sector doesn't match -> connected to another filter or discarded

Default state: filter N connected to partition N (same number), true output
connected, false output disconnected.

### Typical Setup Sequence

1. Set sector length (0x60)
2. Reset selectors (0x48)
3. Connect CD device to filter (0x30)
4. Optionally configure filter range (0x40) and mode (0x44)
5. Play disc (0x10) to start reading
6. Poll sector count (0x51) until data available
7. Get/transfer sector data (0x61 or 0x63)
8. End transfer (0x06)
