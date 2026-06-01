# Sega Saturn Cartridge Interface

Sources: Technical Bulletin #46 (ST-TECH-46) Data Cartridge Manual,
Technical Bulletin #47 (ST-TECH-47) Extended RAM Cartridge Manual,
System Library User's Guide (ST-162-R1) Backup Library,
SCU User's Manual (ST-097-R5)

## Address Space

The cartridge slot maps to the A-Bus through the SCU:

| Region | Address Range (cache-through) | Size | Function |
|--------|-------------------------------|------|----------|
| CS0 | 02000000H - 03FFFFFFH | 32 MB | Primary cartridge space (ROM data carts, RAM expansion) |
| CS1 | 04000000H - 04FFFFFFH | 16 MB | Secondary cartridge space |

CS0 base with cache-through: 22000000H
CS1 base with cache-through: 24000000H

## Cartridge Types

### 1. Data Cartridge (ROM)

Source: Technical Bulletin #46

ROM cartridge for supplemental game data. Maps to A-Bus CS0 (22000000H).
Contains a System ID header at offset 00H-FFH identifying the cartridge.

Code execution from the cartridge is prohibited - data only.

System ID structure at CS0 base (22000000H):

| Offset | Size | Field |
|--------|------|-------|
| 00H | 16 bytes | Hardware ID (always "SEGASATURN DATA") |
| 10H | 16 bytes | Manufacturer ID |
| 20H | 10 bytes | Product number |
| 2AH | 6 bytes | Version |
| 30H | 8 bytes | Release date (YYYYMMDD) |
| 38H | 8 bytes | Device information (type + capacity in Mbit) |
| 40H | 10 bytes | Compatible area codes |
| 4AH | 6 bytes | Backup RAM information |
| 50H | 16 bytes | Reserved (fill with 20H) |
| 60H | 112 bytes | Game title |
| D0H | 16 bytes | Reserved (fill with 00H) |
| E0H | 4 bytes | Reserved (fill with 00H) |
| E4H | 8 bytes | Checksum (32-bit hex) |
| ECH | 20 bytes | Reserved (fill with 00H) |

Device types in Device Information field:
- R: ROM
- S: SRAM
- D: DRAM
- F: FRAM

Checksum: Sum of 16-bit integers from offset +100H to end of cart data,
masked to 32 bits. Compared against value at +E4H.

SCU A-Bus wait setting for 150ns ROM: Write 13301FF0H to A-Bus Setting
Register (25FE00B0H). CS1 should be left at the 1FF0H set by Boot ROM.

### 2. Extended RAM Cartridge (DRAM)

Source: Technical Bulletin #47

RAM expansion cartridge using DRAM. Available in two sizes:

| Capacity | DRAM Config | Cartridge ID |
|----------|-------------|-------------|
| 8 Mbit (1 MB) | 4 Mbit x 2 | 5AH |
| 32 Mbit (4 MB) | Expandable | 5CH (Reserved) |

Memory map within CS0 (per TB47 Table 1). Both 8 Mbit and 32 Mbit variants
use the same DRAM0/DRAM1 windows; the 32 Mbit variant fills more of the
addressable space per window:

| Address (cache-through) | Bank |
|-------------------------|------|
| 22400000H - 2247FFFFH | DRAM0 |
| 22600000H - 2267FFFFH | DRAM1 |

Addressable cartridge region: 22400000H - 227FFFFFH.

Cartridge ID register: 24FFFFFFH (read-only)
- Read this address to determine cartridge type
- Same register location as "Power Memory" backup cartridges

Initialization register: 257EFFFEH (write-only, word size)
- Write 0x0001 to initialize the cartridge
- Must be word-size write of exactly 1

SIMM disable: Write LONGWORD 0 to 257FFFCH to disable

Access modes:
- Read: BYTEREAD, WORDREAD, LONGWORD ACCESS, BURSTREAD
  (BYTE/WORD/LONGWORD all perform 32-bit reads internally)
- Write: BYTEWRITE, WORDWRITE, LONGWORD WRITE (no BURSTWRITE)
- DMA Read: SH-2 DMA or SCU DMA
- DMA Write: SH-2 DMA only (not SCU DMA)

Access speed: approximately 4x Work RAM access time.

A-Bus register settings for extended RAM (per SCU User's Manual Figure 1.6;
TB47 prints these with typos which are corrected here):
- A-Bus Set Register (25FE00B0H) = 23301FF0H (CS0 and CS1)
- A-Bus Refresh Register (25FE00B8H) = 00000013H

Clock change warning: Contents of extended RAM cartridge are not guaranteed
after SYS_CHGSYSCK() system clock change. Re-initialize and retransfer data.

### 3. Backup Memory Cartridge

Source: System Library User's Guide (Backup Library)

The backup library supports three storage devices:

| Device No. | Type | unit_id | partition |
|------------|------|---------|-----------|
| 0 | Built-in memory cartridge | 1 | 1 |
| 1 | Memory cartridge or parallel interface | 2 | 1 |
| 2 | Serial interface | - | - |

Built-in backup memory: 32 KB chip capacity (per System Library Sec 1.2.3),
mapped into a 64 KB addressable region at 00180000H-0018FFFFH.
External cartridge: Accessed through A-Bus CS0.

The backup library is compressed in Boot ROM. Games decompress it to a
16 KB area and call BUP_Init() with:
- Library load address
- 8192-byte work area (Uint32 aligned)
- BupConfig[3] array for device connection info

Save data format (BupDir structure):
- filename: 12 bytes (11 ASCII chars + NUL)
- comment: 11 bytes (10 ASCII chars + NUL)
- language: 1 byte (Japanese, English, French, German, Spanish, Italian)
- date: 4 bytes (BupDate format, year offset from 1980)
- datasize: 4 bytes
- blocksize: 2 bytes

## Access Procedure

### Data Cartridge (from Tech Bulletin #46)

1. Check "SEGASATURN DATA" at hardware ID (+00H)
2. Check manufacturer ID (+10H), product number (+20H), area codes (+40H)
3. Set SCU A-Bus wait (13301FF0H to 25FE00B0H for 150ns ROM)
4. Calculate and verify checksum (sum 16-bit from +100H to end, compare +E4H)
5. Check backup RAM info and hand control to game

### Extended RAM Cartridge (from Tech Bulletin #47)

1. Read cartridge ID at 24FFFFFFH
   - Check for both 5AH (8 Mbit) and 5CH (32 Mbit)
2. If ID not verified, display error message
3. Write 0x0001 (word) to initialization address 257EFFFEH
4. Set A-Bus registers (25FE00B0H = 23301FF0H, 25FE00B8H = 00000013H)
5. Cartridge is now accessible for read/write

## Bus Characteristics

From SCU User's Manual and Technical Bulletins:

- A-Bus data width: 16-bit (mapped as 16-bit ROM/RAM)
- All A-Bus and B-Bus reads are performed as 32-bit reads regardless of
  CPU operation width (SCU errata)
- CS0 and CS1 timing is configurable via A-Bus Set Register (25FE00B0H)
- Boot ROM default for CS1: 1FF0H
- Cartridge access is slower than Work RAM (approximately 4x for DRAM carts)

## Corresponding Peripheral Character Code

For CD-ROM games that use a data cartridge, the SYSTEM ID "Corresponding
Peripheral" field (offset 50H of the CD disc header) uses "W" to indicate
the extended RAM cartridge requirement. The character is placed at offset
50H of the disc's system ID.

For data cartridges, the SYSTEM ID of the CD-ROM has an "R" appended to
indicate it corresponds to a data cartridge peripheral.
