# IP.BIN Structure and Loading

The first 16 sectors of a Saturn data track hold the Initial
Program (IP). This file documents the IP's on-disc structure, the
System ID header at offset `$0000`, how the BIOS validates and
loads the IP into Work-RAM-H, and the BIOS-to-disc handoff
convention (PC, SR, VBR, GBR, R15). The IP's own boot-time
behavior - security screen, copyright text - is described in
[bios_font.md](bios_font.md) and
[text_drawers.md](text_drawers.md).

## IP.BIN Structure and Loading

Sources: Disc Format Standards (ST-040-R4), Boot ROM User's Manual
(ST-079B-R3), System Library User's Guide (ST-162-R1), BIOS disassembly

### IP Structure (from Disc Format Standards)

The IP (Initial Program) occupies the first 16 sectors of the CD data
track. It consists of the boot code and the Application Initial Program.

| Offset | Size | Content |
|--------|------|---------|
| $000 | $100 | System ID (hardware ID, maker, product, title, etc.) |
| $100 | $D00 | Security Code (SYS_SEC.OBJ - SEGA license display) |
| $E00 | $20-$100 | Area Code Group (SYS_ARE?.OBJ - region verification) |
| ~$F00+ | varies | Application Initial Program (AIP - game bootstrap) |

Total IP size: $1000-$8000 (4-32 KB), specified in System ID at offset $E0.

### System ID Layout (256 bytes at IP offset $000)

| Offset | Size | Field |
|--------|------|-------|
| $00 | 16 | Hardware Identifier ("SEGA SEGASATURN ") |
| $10 | 16 | Maker ID ("SEGA ENTERPRISES" or "SEGA TP <company>") |
| $20 | 10 | Product Number |
| $2A | 6 | Version ("V1.000") |
| $30 | 8 | Release Date ("YYYYMMDD") |
| $38 | 8 | Device Information ("CD-1/1 " etc.) |
| $40 | 10 | Compatible Area Symbols ("JTU " etc.) |
| $4A | 6 | Space padding |
| $50 | 16 | Compatible Peripherals ("J " = control pad) |
| $60 | 112 | Game Title |
| $D0 | 16 | Reserved ($00) |
| $E0 | 4 | IP Size (bytes, $1000-$8000) |
| $E4 | 4 | Reserved ($00) |
| $E8 | 4 | Stack-M: master SH-2 SP (0 = default $06002000) |
| $EC | 4 | Stack-S: slave SH-2 SP (0 = default $06001000) |
| $F0 | 4 | 1st Read Address (game binary load destination) |
| $F4 | 4 | 1st Read Size (ignored for CDs) |
| $F8 | 8 | Reserved ($00) |

Area symbols: J=Japan, T=Asia NTSC, U=North America, E=Europe PAL

Peripheral codes: J=Control Pad, A=Analog, M=Mouse, K=Keyboard,
S=Steering, T=Multitap

### Memory Layout During Boot

The BIOS loads IP data and places its own staging code at several Work
RAM-H addresses during boot:

**IP load buffer ($06002000-$0600A000, 32 KB):**
The BIOS reads the IP from the disc to $06002000 and uses this 32 KB
window for both System ID validation and as the execution buffer for
the disc's own code. The "SEGA SEGASATURN " check, region code check,
and security area comparison all read from this address.

The BIOS does not execute its own code from this window at any point
during boot - it is reserved exclusively as a disc-data buffer. The
first master SH-2 instruction fetched from this region is the disc's
own IP code starting to run; this is the empirical handoff signal
used to capture the BIOS-handoff state.

**Security check execution area ($06020000):**
sub_1A18 copies 4096 bytes of BIOS authentication code from BIOS ROM
$040000 to $06020000. This code sets up its own execution environment
(SR=$F0, GBR=$06020000, VBR=$06000000, SP=$0601FFF0) and authenticates
the disc via SMPC register sequences. On success, it loads R0 from a
pool entry at $06020060 (= $06028000 in BIOS_USA) and executes
`JMP @R0`.

**1st Read File mechanism:**
During the Saturn logo display (which overlaps with security code
execution), the BIOS reads file ID 2 from the disc and transfers it
to the address specified in System ID offset $F0. This allows the
game's main binary to load in parallel with the license screen
display. The Disc Format Standards (ST-040-R4) state the Saturn
logo display lasts 2-3.5 seconds depending on transfer size.

### BIOS -> Disc Handoff

Runtime capture (two independent commercial discs, BIOS_USA) shows
the BIOS hands off at **PC = $06002100 (IP+$100, security-code
entry)** for both captured games. This is the entry point of the
disc's own SYS_SEC.OBJ within the IP. The handoff target was the
same across both captures; it is BIOS-controlled, not game-controlled.

The auth code at BIOS $040000 (copied to $06020000) has a pool entry
at $040060 / $06020060 containing the constant $06028000, and ends
with `JMP @R0` at $06020014. On the captured boots the BIOS does
**not** execute that JMP - the master never reaches $06020014. The
$06028000 target is dead code on the observed path.

What the BIOS reads from the disc that affects boot:

| System ID offset | Field | Effect |
|------------------|-------|--------|
| $E0 | IP size ($1000-$8000) | How many sectors the BIOS loads as IP |
| $E8 | Stack-M | Master SP request from disc (BIOS observed to ignore this; SP at handoff = $06002000) |
| $EC | Stack-S | Slave SP request from disc (slave is held in reset) |
| $F0 | 1st Read Address | Where the game binary file (file ID 2) loads |
| $F4 | 1st Read Size | Size of the 1st Read file (ignored for CDs - actual size comes from ISO9660) |

The IP itself contains executable code starting at IP+$100 (security
code SYS_SEC.OBJ, ~3.3 KB), area code (SYS_ARE?.OBJ, ~256 B), then
the disc's AIP (Application Initial Program). The BIOS dispatches
into SYS_SEC at IP+$100; SYS_SEC then displays the SEGA license
screen and chains to SYS_ARE, which chains to the AIP. Different
games' AIP entry offsets within the IP are decided by the SDK
toolchain at game-build time.

The reliable empirical signal for "the BIOS has handed off to the
disc's code" is: master PC enters the $06002000-$0600A000 IP load
window. This is the same buffer the BIOS reads disc data into but
never executes its own code from, so the first fetch there is by
definition the disc's own code.

### CPU State at IP.BIN Entry

See [handoff_state.md](handoff_state.md) for the consolidated state
(master SH-2 registers, BSC, SCU, VDP1, VDP2, SCSP, CD Block, SMPC,
Work RAM, cache) that the BIOS leaves behind at the moment the master
SH-2 begins executing disc code in the $06002000-$0600A000 IP load
window.

## IP -> Application Handoff

The disc code reaches application code (the game binary) via a second
handoff, performed by the IP itself rather than by the BIOS. Two
hardware-defined transitions exist between power-on and the game's
first instruction:

| Transition | PC at entry | Performed by |
|---|---|---|
| BIOS -> IP | `$06002100` (SYS_SEC entry, = IP load addr + $100) | BIOS handoff |
| IP -> Application | Address read from System ID `+$F0` (1st Read Address) | IP code |

The application binary is the file at ISO9660 file ID 2 on the data
track. The IP loads it from disc to the 1st Read Address (via
`sub_1C90` -> `sub_2F48` -> CD-block commands $73 / $75) and then
JSRs to that address.

By convention the same address is both the load destination and the
master entry point. Captured NiGHTS state confirms this: System ID
`+$F0` = `$06004000`, file ID 2 = `0NIGHTS` (447 868 bytes), and the
IP's tail JSR target = `$06004000`.

Register state at the IP's tail JSR to the application:

| Field | Value | Source |
|---|---|---|
| PC | 1st Read Address (System ID `+$F0`) | IP target of `JSR` |
| R0-R14 | 0 | IP cleanup at `$06002D88` (XOR Rn,Rn) |
| R15 | unchanged from initial IP entry (`$06002000`) | IP didn't deepen stack |
| SR | 0 | IP cleanup (LDC R0,SR with R0=0) |
| GBR | 0 | IP cleanup (LDC R0,GBR with R0=0) |
| PR | 0 | IP cleanup (LDS R0,PR with R0=0) |
| MAC | 0 | IP cleanup (CLRMAC) |
| VBR | `$06000000` | unchanged from BIOS handoff |

The application's prologue typically establishes its own SR/SP
immediately (e.g. `MOV #-1,R0; AND #$0F,R0; LDC R0,SR`), so only
PC and the SP being in valid WRAM are strictly load-bearing for the
handoff to succeed.