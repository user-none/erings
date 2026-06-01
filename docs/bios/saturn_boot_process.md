# Sega Saturn Boot Process

Sources: Boot ROM User's Manual (ST-079B-R3), SH-1/SH-2 Programming Manual,
Dual CPU User's Guide (ST-202-R1), System Library User's Guide (ST-162-R1),
Disc Format Standards (ST-040-R4)

## BIOS ROM

- Size: 512 KB
- Address: 00000000H - 0007FFFFH (cacheable), 20000000H - 2007FFFFH (cache-through)
- Contains: Initial Program Loader, system library (compressed), backup library
  (compressed), Saturn logo animation, CD multiplayer (audio CD player),
  system settings screens, security check routines

## SH-2 Reset Behavior

On power-on or reset, the SH-2 fetches two longwords from address 00000000H:

| Address | Contents | Purpose |
|---------|----------|---------|
| 00000000H | Longword | Initial PC (program counter) |
| 00000004H | Longword | Initial SP (stack pointer) |

Since the BIOS ROM is mapped at 00000000H, these values come from the first
8 bytes of the BIOS file. The master SH-2 begins executing at the address
stored in the first longword of the BIOS.

The slave SH-2 starts in reset state and does not execute until explicitly
released by the master via the SMPC SSH_ON command (command code 02H).

## Pre-initialization Requirements

For emulation, the following state must be set before the first SH-2
instruction executes:

1. **Load BIOS ROM** (512 KB file) into 00000000H - 0007FFFFH
2. **All chip registers at power-on defaults** - Each chip manual documents
   its reset state. In general, all registers initialize to 0 on power-on.
3. **Work RAM-H** (1 MB at 06000000H): contents undefined (uninitialized)
4. **Work RAM-L** (1 MB at 00200000H): contents undefined
5. **Backup RAM** (32 KB at 00180000H): should be loaded from save file if
   available, otherwise uninitialized (simulates dead battery)
6. **VDP1/VDP2/SCU/SCSP registers**: all at power-on defaults (zeros)
7. **SMPC**: ready to accept commands, SMPC clock defaults to 320 mode
8. **CD Block**: ready, drive status depends on whether a disc is present
9. **Slave SH-2**: held in reset (does not execute)
10. **MC68EC000**: held in reset by SCSP until sound program starts it

No manual register pre-configuration is needed. The BIOS handles all
hardware initialization.

## Boot Flow

From Boot ROM User's Manual Figure 1:

```
Power ON or RESET
       |
       v
  SMPC RESET? ---Yes---> Set date and time
       |                        |
       No                       |
       |<-----------------------+
       v
  L+R buttons held? ---Yes---> System Settings screen
       |                              |
       No                              |
       |<------------------------------+
       v
  A button held? ---Yes---> Skip to Multiplayer (CD player)
       |
       No
       |
       v
  Game cartridge present? ---Yes---> Start game cartridge
       |
       No
       |
       v
  Display SEGA SATURN logo animation
       |
       v
  Valid game CD detected? ---Yes---> Start game CD
       |
       No
       |
       v
  Start Multiplayer (CD player)
```

## Boot Flow Details

### SMPC Reset Detection
On first power-on (or if battery died), the SMPC indicates a reset condition.
The BIOS displays the Set Clock screen for date and time configuration.
On subsequent boots with valid battery, this step is skipped.

### Game Cartridge Priority
A game cartridge in the A-Bus CS0 slot takes priority over a CD-ROM.
The BIOS checks for a valid cartridge header ("SEGA SEGASATURN " at
22000000H) before checking the CD drive.

### Saturn Logo and CD Check
Hardware and initialization checks are performed before the logo displays.
The CD game disc check happens in parallel with the logo animation. If a
valid Saturn game disc is recognized during the animation, the game starts
after the logo completes.

### Security Check
The BIOS verifies the CD contains a valid Saturn boot code before allowing
the game to start. The boot code format and location are defined in the
Disc Format Standards (ST-040). If the boot code check fails, the disc is
treated as an audio CD and the Multiplayer starts.

## Post-Boot CPU State

After the BIOS completes initialization and before jumping to game code:

### Master SH-2
- VBR: 06000000H (Work RAM-H base)
- PC: Game entry point (from disc or cartridge header)
- SR: Supervisor mode, interrupts configured
- Cache: Enabled

### Slave SH-2
- Held in reset until game releases it via SMPC SSH_ON
- On release, the slave executes a BIOS-installed init body in Work RAM-H
  at 06000600H (copied there from BIOS ROM during boot). That body sets the
  slave stack pointer (SP = 06001000H) and base VBR (VBR = 06000400H), then
  hands off to the IP/dispatcher. The game (or its IP/AIP) installs the
  actual slave interrupt vectors at VBR + vector offsets; the BIOS provides
  the base VBR and default SP.

### SCU
- Interrupt vectors set up in Work RAM-H at VBR + vector offsets
- Interrupt mask register configured
- A-Bus timing set per connected devices

### System Library Services
The BIOS provides compressed system libraries in ROM that games decompress
into Work RAM for use:
- System program: interrupt management, semaphores, clock change
- Backup library: save data read/write (16 KB when decompressed)
- These are loaded by the game, not pre-loaded by the BIOS

## Region Handling

Sources: SMPC User's Manual (ST-169-R1), Disc Format Standards (ST-040-R4),
Boot ROM User's Manual (ST-079B-R3), Data Cartridge Manual (ST-TECH-46)

### Area Codes

The Saturn uses single-character area codes to identify regions:

| Code | Region |
|------|--------|
| J | Japan |
| T | Asian NTSC (Taiwan, Philippines, Korea) |
| U | North America (USA, Canada), Central and South American NTSC (Brazil) |
| E | European PAL, East Asia PAL, Central and South American PAL |

### SMPC Area Code

The SMPC reports the console's region through its status register. This is a
hardware-level value determined by the SMPC firmware and cannot be changed by
software. The SMPC area code tells the BIOS what region the console is.

### Disc Area Codes

Each game disc has a "Compatible area codes" field at offset 40H in its system
ID header (10 characters). Multiple area codes can be listed - for example a
disc compatible with Japan and North America would have "JU" in this field.
The remaining characters are filled with spaces (20H).

### Region Lock Mechanism

The BIOS reads the SMPC area code to determine the console region, then reads
the disc's compatible area codes from the system ID header. If the console's
region code is not found in the disc's area code list, the game does not start.

The same mechanism applies to data cartridges - the cartridge system ID at
CS0 offset 40H contains compatible area codes checked against the SMPC region.

### Emulation Implications

For emulation, region handling can be controlled at two points:

1. **SMPC area code register**: The emulator controls what value the SMPC
   reports. Setting this to match the disc's region codes allows the game
   to pass the region check regardless of which BIOS is loaded.

2. **BIOS selection**: Using a BIOS from the same region as the game disc.
   Different region BIOS ROMs have different SMPC area codes baked into
   their respective SMPC firmware, different default languages, and
   potentially different logo animations.

The BIOS itself may also contain region-specific code paths (the Boot ROM
manual notes that Japanese boot ROMs "operate differently" for certain
functions but does not detail the differences). The Saturn logo animation
and system settings screens may differ between regions.

### NTSC vs PAL

Region also determines the video standard:
- J, T, U regions: NTSC (60 Hz, 262/263 lines per field)
- E region: PAL (50 Hz, 312/313 lines per field)

The SMPC PAL flag (reported in the VDP2 Screen Status Register bit 0) is
tied to the console region. PAL consoles run at 50 Hz which affects game
speed if the game does not account for the timing difference.

## BIOS Variants

There are multiple BIOS versions for different regions and hardware revisions.
The Boot ROM manual covers the non-Japanese version. Japanese boot ROMs
have different behavior for some screens.

Key differences between product and development (target box) versions:
- Target box checks for SCSI devices and SIMM memory
- Target box looks for IP.BIN file for boot
- Target box can start from write-once CD without System Disc

## IP.BIN (Initial Program)

The first file loaded from a Saturn game disc is IP.BIN (Initial Program).
It contains:
- System ID header (same format as the disc header area)
- Initial program code that sets up the game's execution environment
- Loaded to and executed from a fixed address in Work RAM

The format and content requirements are defined in the Disc Format Standards
(ST-040).
