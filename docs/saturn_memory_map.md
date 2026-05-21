# Sega Saturn Memory Map

Sources: SCU User's Manual (ST-097-R5) Figures 1.3/1.5/1.6, VDP1 User's Manual
(ST-013-R3), VDP2 User's Manual (ST-058-R2), SCSP User's Manual (ST-077-R2),
System Library User's Guide (ST-162-R1), Dual CPU User's Guide (ST-202-R1)

## Bus Architecture

The Saturn has three buses interconnected by the SCU:

- **CPU-Bus**: Connects master/slave SH-2 to Work RAM, Backup RAM, IPL ROM, SMPC
- **A-Bus**: Connects external devices (cartridge, CD Block)
- **B-Bus**: Connects VDP1, VDP2, SCSP

The SCU provides CPU I/F, A-Bus I/F, and B-Bus I/F. DMA transfers operate on the
lower bus between A-Bus and B-Bus. The CPU can access A-Bus and B-Bus through
the SCU while executing from work RAM.

## Address Space Overview

The SH-2 has a 32-bit address space partitioned by bits A31-A29. Per the
SH7604 Hardware Manual Table 8.2:

| Prefix (A31-A29) | Range | Behavior |
|------------------|-------|----------|
| 000 | 00000000H-1FFFFFFFH | Cacheable area (uses cache when CCR.CE=1) |
| 001 | 20000000H-3FFFFFFFH | Cache-through area (cache bypassed) |
| 010 | 40000000H-5FFFFFFFH | Associative purge area (writes invalidate) |
| 011 | 60000000H-7FFFFFFFH | Cache address array read/write |
| 110 | C0000000H-DFFFFFFFH | Cache data array read/write |
| 111 | E0000000H-FFFFFFFFH | On-chip peripheral / I/O (cache bypassed) |

The Saturn devices populate only the low 27 bits, so each region's usable
addresses end at offset 07FFFFFFH. Adding 20000000H to any device address
accesses the same physical location but bypasses the SH-2 cache. Use the
cache-through alias (or invalidate via the associative purge area) when
reading data modified by DMA, the other CPU, or any other bus master.

## Cacheable Address Map (00000000H - 07FFFFFFH)

| Start | End | Size | Description | Bus |
|-------|-----|------|-------------|-----|
| 00000000H | 0007FFFFH | 512 KB | BIOS ROM (IPL ROM) | CPU |
| 00100000H | 0010007FH | 128 bytes | SMPC Registers | CPU |
| 00180000H | 0018FFFFH | 64 KB | Backup RAM | CPU |
| 00200000H | 002FFFFFH | 1 MB | Work RAM-L (Low) | CPU |
| 01000000H | 01000003H | 4 bytes | MINIT Region (Master SH-2 init) | CPU |
| 01800000H | 01800003H | 4 bytes | SINIT Region (Slave SH-2 init) | CPU |
| 02000000H | 03FFFFFFH | 32 MB | A-Bus CS0 (Cartridge) | A-Bus |
| 04000000H | 04FFFFFFH | 16 MB | A-Bus CS1 | A-Bus |
| 05000000H | 057FFFFFH | 8 MB | A-Bus Dummy | A-Bus |
| 05800000H | 058FFFFFH | 1 MB | A-Bus CS2 (CD Block) | A-Bus |
| 05900000H | 059FFFFFH | 1 MB | (Unspecified) | - |
| 05A00000H | 05AFFFFFH | 1 MB | SCSP Sound RAM | B-Bus |
| 05B00000H | 05B00EE3H | ~3.7 KB | SCSP Control Registers (slot + common + DSP) | B-Bus |
| 05C00000H | 05C7FFFFH | 512 KB | VDP1 VRAM | B-Bus |
| 05C80000H | 05CBFFFFH | 256 KB | VDP1 Frame Buffer | B-Bus |
| 05D00000H | 05D00017H | 24 bytes | VDP1 Registers | B-Bus |
| 05E00000H | 05E7FFFFH | 512 KB | VDP2 VRAM | B-Bus |
| 05F00000H | 05F00FFFH | 4 KB | VDP2 Color RAM | B-Bus |
| 05F80000H | 05F8011FH | 288 bytes | VDP2 Registers | B-Bus |
| 05FE0000H | 05FE00CFH | 208 bytes | SCU Registers | SCU |
| 06000000H | 060FFFFFH | 1 MB | Work RAM-H (High) | CPU |

## Cache-Through Address Map (20000000H - 27FFFFFFH)

Identical layout to the cacheable map but with 20000000H added to each address.
Access bypasses the SH-2 on-chip cache.

| Start | End | Size | Description |
|-------|-----|------|-------------|
| 20000000H | 2007FFFFH | 512 KB | BIOS ROM |
| 20100000H | 2010007FH | 128 bytes | SMPC Registers |
| 20180000H | 2018FFFFH | 64 KB | Backup RAM |
| 20200000H | 202FFFFFH | 1 MB | Work RAM-L |
| 21000000H | 21000003H | 4 bytes | MINIT (Master -> Slave FRT trigger) |
| 21800000H | 21800003H | 4 bytes | SINIT (Slave -> Master FRT trigger) |
| 22000000H | 23FFFFFFH | 32 MB | A-Bus CS0 (Cartridge) |
| 24000000H | 24FFFFFFH | 16 MB | A-Bus CS1 |
| 25000000H | 257FFFFFH | 8 MB | A-Bus Dummy |
| 25800000H | 258FFFFFH | 1 MB | A-Bus CS2 (CD Block) |
| 25A00000H | 25AFFFFFH | 1 MB | SCSP Sound RAM |
| 25B00000H | 25B00EE3H | ~3.7 KB | SCSP Control Registers |
| 25C00000H | 25C7FFFFH | 512 KB | VDP1 VRAM |
| 25C80000H | 25CBFFFFH | 256 KB | VDP1 Frame Buffer |
| 25D00000H | 25D00017H | 24 bytes | VDP1 Registers |
| 25E00000H | 25E7FFFFH | 512 KB | VDP2 VRAM |
| 25F00000H | 25F00FFFH | 4 KB | VDP2 Color RAM |
| 25F80000H | 25F8011FH | 288 bytes | VDP2 Registers |
| 25FE0000H | 25FE00CFH | 208 bytes | SCU Registers |
| 26000000H | 260FFFFFH | 1 MB | Work RAM-H |

## SH-2 Internal Registers (FFFFFE00H - FFFFFFFFH)

The SH-2's on-chip peripheral registers (FRT, WDT, SCI, DMAC, DIVU, BSC, cache
control) are mapped at the top of the address space. See the SH7604 Hardware
Manual for the full register map. These are CPU-internal and not visible on any
external bus.

## CPU-Bus Details

### BIOS ROM (00000000H, 512 KB)
- Contains Initial Program Loader (IPL), system library, CD player
- Read-only from the SH-2's perspective
- Both master and slave SH-2 fetch initial vectors from here

### SMPC Registers (00100000H, 128 bytes)
- System Manager and Peripheral Controller
- Byte-access only (odd addresses: 00100001H, 00100003H, etc.)
- Commands, status, I/O registers, RTC

### Backup RAM (00180000H, 64 KB)
- Battery-backed SRAM for save data
- Directly addressable by the SH-2

### Work RAM-L (00200000H, 1 MB)
- Low work RAM
- Connected to CPU-Bus
- Accessible by both SH-2s

### MINIT / SINIT (01000000H / 01800000H)
- A 16-bit write to MINIT (01000000H) triggers FRT Input Capture on the
  slave SH-2. The data value is ignored.
- A 16-bit write to SINIT (01800000H) triggers FRT Input Capture on the
  master SH-2. The data value is ignored.
- Byte and longword writes do not trigger the signal.
- Used for inter-CPU communication (see Dual CPU User's Guide section 1.2).
- Cache-through versions: 21000000H / 21800000H.

### Work RAM-H (06000000H, 1 MB)
- High work RAM
- Connected to CPU-Bus
- Boot ROM sets master VBR to start of Work RAM-H
- Slave VBR = Work RAM-H + 400H
- Slave SP initialized to 06001000H

## A-Bus Details

### CS0 (02000000H, 32 MB)
- Cartridge slot primary space
- ROM cartridges, RAM expansion cartridges, backup memory cartridges
- Bus timing configurable via SCU A-Bus Set Register (ASR0)

### CS1 (04000000H, 16 MB)
- Cartridge slot secondary space
- Bus timing configurable via SCU A-Bus Set Register (ASR1)

### A-Bus Dummy (05000000H, 8 MB)
- Dummy/placeholder space
- Bus timing configurable via SCU A-Bus Set Register (ASR1, Dummy section)

### CS2 (05800000H, 1 MB)
- CD Block registers
- CD communication interface (HIRQ, CR1-CR4, etc.)

## B-Bus Details

### Sound Region (05A00000H, ~1 MB)
- SCSP registers and sound RAM
- Sound RAM: 512 KB at 05A00000H (accessible by both SH-2 and 68000)
- SCSP registers: at 05B00000H area
- 68000 has its own view of the sound RAM starting at 000000H
- Main CPU access goes through the SCU B-Bus interface

### VDP1 VRAM (05C00000H, 512 KB)
- Stores command tables, character patterns, color lookup tables, Gouraud shading tables
- VDP1 addresses are relative; add 05C00000H for absolute

### VDP1 Frame Buffer (05C80000H, 256 KB)
- Two frame buffers (draw and display), swapped each frame
- CPU can read/write the draw buffer

### VDP1 Registers (05D00000H, 24 bytes)
- System registers: TV mode, frame buffer control, plot trigger, erase/write,
  draw termination, transfer end status, command address, mode status

### VDP2 VRAM (05E00000H, 512 KB)
- Stores scroll screen patterns, pattern name tables, color data
- Can be split into two 256 KB banks for simultaneous access
- VDP2 addresses are relative; add 05E00000H for absolute

### VDP2 Color RAM (05F00000H, 4 KB)
- Palette data for scroll screens
- Separate from VDP2 VRAM

### VDP2 Registers (05F80000H, 288 bytes)
- All VDP2 control registers (TV screen mode, scroll, rotation, priority,
  color calculation, windows, etc.)

### SCU Registers (05FE0000H, 208 bytes)

| Address | Size | Register |
|---------|------|----------|
| 05FE0000H | 32 bytes | Level 0 DMA Set Register |
| 05FE0020H | 32 bytes | Level 1 DMA Set Register |
| 05FE0040H | 32 bytes | Level 2 DMA Set Register |
| 05FE0060H | 16 bytes | DMA Forced-Stop Register |
| 05FE0070H | 16 bytes | DMA Status Register (DSTA) |
| 05FE0080H | 4 bytes | DSP Program Control Port (PPAF) |
| 05FE0084H | 4 bytes | DSP Program RAM Data Port (PPD) |
| 05FE0088H | 4 bytes | DSP Data RAM Address Port (PDA) |
| 05FE008CH | 4 bytes | DSP Data RAM Data Port (PDD) |
| 05FE0090H | 4 bytes | Timer 0 Compare Register (T0C) |
| 05FE0094H | 4 bytes | Timer 1 Set Data Register (T1S) |
| 05FE0098H | 4 bytes | Timer 1 Mode Register (T1MD) |
| 05FE009CH | 4 bytes | (Free) |
| 05FE00A0H | 4 bytes | Interrupt Mask Register (IMS) |
| 05FE00A4H | 4 bytes | Interrupt Status Register (IST) |
| 05FE00A8H | 4 bytes | A-Bus Interrupt Acknowledge (AIAK) |
| 05FE00ACH | 4 bytes | (Free) |
| 05FE00B0H | 8 bytes | A-Bus Set Register (ASR0, ASR1) |
| 05FE00B8H | 4 bytes | A-Bus Refresh Register (AREF) |
| 05FE00BCH | 8 bytes | (Free) |
| 05FE00C4H | 4 bytes | SCU SDRAM Select Register (RSEL) |
| 05FE00C8H | 4 bytes | SCU Version Register (VER) |
| 05FE00CCH | 4 bytes | (Free) |

## Interrupt Vector Table (Boot ROM Initial Settings)

From the System Library User's Guide:

### Master SH-2 Vectors

| Vector | Description |
|--------|-------------|
| 40H | SCU interrupt vector (range 40H-5FH) |
| 60H | SCI receive error |
| 61H | SCI receive buffer full |
| 62H | SCI send buffer empty |
| 63H | SCI send quit |
| 64H | FRT input capture (used for slave -> master signaling) |
| 65H | FRT compare match |
| 66H | FRT overflow |
| 67H | (Free) |
| 68H | WDT interval |
| 69H | BSC compare match |
| 6AH-6BH | (Free) |
| 6CH | DMACH1 (SH2 built-in) |
| 6DH | DMACH0 (SH2 built-in) |
| 6EH | DIVU (division) |
| 6FH | (Free) |

### Slave SH-2 Vectors

| Vector | Description |
|--------|-------------|
| 41H | H-Blank In (IRL2, IRL6 level) |
| 43H | V-Blank In |
| 60H-6EH | Same as master (SCI, FRT, WDT, BSC, DMA, DIVU) |
| 64H | FRT input capture (used for master -> slave signaling) |

## Notes

- Both SH-2s share the same address space and see the same physical memory
- When both CPUs access external devices simultaneously, one must wait (bus contention)
- The SH-2 cache does not support bus snooping; use cache-through addresses
  (20000000H+) or associative purge (40000000H+) when reading data written
  by the other CPU or by DMA
- Associative purge: a longword (32-bit) write to address 40000000H +
  target_address invalidates the 16-byte cache line containing that
  address. The data value is ignored. Access sizes other than longword
  are not supported (SH7604 Hardware Manual section 8.4.7)
- Work RAM-H is the primary execution area; Work RAM-L is supplementary
- The slave SH-2 starts in reset state; master releases it via SMPC SSH_ON command (02H)
