# Boot Library

Material that lives in the boot library blob (BIOS `$007000`
compressed body, decompressed to `$06010000` on the no-game / CD
player / system-menu paths): the Saturn logo animation, the CD
multiplayer (audio CD player), and the system settings screens.
This code does not run on a CD-game boot path; the CD-block
firmware (loaded at the same address) takes its place.

## Saturn Logo Animation

The Saturn logo animation is displayed during the disc check phase. The
animation code is in the compressed system library (decompressed to
$06010000), not in the BIOS ROM directly. The animation's graphics data
(frames, tiles) is embedded in the same compressed body at BIOS $7000
and emerges in WRAM-H alongside the code after decompression.

The animation is driven by the V-Blank interrupt handler during the
main loop. The BIOS configures VDP1 (PTMR=2, auto draw) and VDP2 to
display the logo and runs the animation while simultaneously checking
the disc.

### Animation Main Loop (boot library at $06011170+)

The animation runs as a loop in the decompressed boot library. The
drive status value at $0600022C controls the main loop behavior. The
value is the lower nibble of the CD block status byte; BIOS ROM
function $003338 reads and returns the nibble during the boot-flow
disc check, and the caller writes it to $0600022C before the boot
library main loop runs (0=Busy, 1=Pause, 7=NoDisc).

Each main loop iteration at $06011170:

1. Calls BIOS function (via pointer table at $06000300) with R4=64
2. Calls boot library function at $060101D4
3. Writes frame target to $060408EC
4. Calls BIOS function with R4=65, R5=$06010E5C (VBL callback)
5. Checks drive status at $0600022C

Exit logic at $06011192:
- If drive status ($0600022C) != 0 (Pause/NoDisc): skip animation
  state updates, branch to wait loop at $0601124A, then return.
  The VBL callback still renders but animation state does not advance.
- If drive status == 0 (Busy): check disc validation at $060408CA
  - If disc validation != 0: set drive status to -1 ($06011244),
    then wait loop
  - If disc validation == 0: enter animation check loop at $0601120C
    which calls $06010E80 (VBL sync) and advances animation state

The wait loop at $0601124A compares VBL frame counter ($060408EC)
against animation duration ($060408C4, static value 0x82=130 frames).
When the counter exceeds the duration, the function returns.

On real hardware, the CD block initially reports Busy status (during
SH-1 init, ~109ms). The BIOS sets drive status=0, allowing the
animation to run. The CD block transitions to Pause after disc
detection, but by then the boot library is already running with
drive status=0.

### VBL Callback ($06010E5C)

The VBL callback runs every vertical blank regardless of the main loop
state. It:

1. Checks animation enable flag at $060408C6
2. If enabled: calls render function at $06010012
3. Calls update function at $0601000C
4. Increments VBL frame counter at $060408EC

The render function at $06010012 uses a tick counter at $06013908
to pace full renders. Each frame it subtracts the animEnable value
from the tick counter. When the counter goes negative, it calls
$06012FC0 for a full animation render. The full render path requires
SCU DSP operations for graphics decompression.

### SCU DSP Dependency

The boot library initialization path when drive status=0 (Busy) uses
the SCU DSP for graphics decompression. It polls SCU PPAF register
($25FE0080) bit 16 waiting for DSP completion. Without SCU DSP
support, the boot library hangs during initialization.

When drive status=1 (Pause), the boot library takes a shorter init
path that avoids DSP operations. The animation VBL callback still
runs but produces static output since the main loop does not advance
animation state.

### Disc Validation During Animation

The disc validation runs in parallel with the animation through a state
machine at $060003A0:

State 0 (init): Function $00002B74 checks HIRQ for EFLS ($0200).
If EFLS is set, issues the device authentication-status query (cmd
$E1) and checks for an authenticated result (response value 4); on a
successful result the dispatcher $000029D4 advances to state 1.
If EFLS is not set, returns 0 (no progress).

State 1 (check): Function $00002D4C checks disc validation progress.

The validation state machine is driven by the animation loop calling
$00001904 -> $000029D4 each frame. $000029D4 dispatches based on the
current state at $060003A0.

### HIRQ EFLS Dependency

The disc validation requires EFLS to be set in HIRQ before it can
start. EFLS should be set by a previous CD block operation (typically
InitCDSystem issued during the boot library's own initialization at
$06010000+, which runs before the animation loop starts at ~frame 90).

If EFLS is never set (HIRQ stays 0), the validation state machine
stays at state 0 forever. The animation then exits when the drive
status at $0600022C becomes non-zero (Pause/NoDisc), which takes the
wait/exit path in the main loop above. The drive-status nibble is
written by the boot-flow disc check (from $003338); $000025DC, which
polls that status under SCDQ gating, does not write $0600022C itself.

### Key State Variables

| Address | Type | Purpose |
|---------|------|---------|
| $0600022C | long | Drive status nibble (0=Busy, 1=Pause, 7=NoDisc) |
| $0600029C | long | Function pointer for animation step ($00001904) |
| $06000300 | long | BIOS function pointer table (indirect call target) |
| $060003A0 | word | Disc validation state machine (0=init, 1=checking) |
| $060408B0-$060408DF | | Boot animation state area |
| $060408C4 | word | Animation duration (static, 0x0082 = 130 frames) |
| $060408C6 | word | Animation enable flag (0=disabled, 1=enabled) |
| $060408C8 | word | Disc validation trigger flag |
| $060408CA | word | Disc validation result (0=not done, non-zero=done) |
| $060408EC | word | VBL frame counter (incremented by callback) |
| $06013908 | word | Render tick counter (paces full renders) |

### BIOS ROM Functions Used During Animation

| Address | Purpose |
|---------|---------|
| $000025DC | CD status poll loop - calls $003338, retries 3x gated by SCDQ |
| $000029D4 | Disc validation state machine dispatcher |
| $00002B74 | State 0: check EFLS, init CD system |
| $00002D4C | State 1: check validation progress |
| $000032DC | HIRQ bit test - reads HIRQ, tests bit from R4 parameter |
| $00003338 | CD status read - calls $003B6E, extracts status nibble, checks DCHG |
| $00003B6E | CD status with PERI check - calls $003BC6, checks status byte for PERI bit (0x20), returns -8 if not set |
| $00003BC6 | CR1-CR4 consistency read - reads registers twice with interrupts disabled, retries up to 100x on mismatch |
| $000042EC | Read CR1-CR4 ($25890018-$25890024) into buffer |

### Boot Library Functions

| Address | Purpose |
|---------|---------|
| $06010012 | Animation render - paces full renders via tick counter |
| $0601000C | Post-render update |
| $060101D4 | Animation init/step function |
| $06010E5C | VBL callback - render, update, increment frame counter |
| $06010E80 | VBL sync - spins until frame counter changes |
| $06011170 | Animation main loop entry |
| $06012FC0 | Full animation render (uses SCU DSP) |


## CD Multiplayer (Audio CD Player)

The CD multiplayer is the audio CD playback interface activated when:
- No game disc is detected
- An audio CD is inserted
- The boot code check fails

The CD player code is part of the compressed system library, accessed
via the SYS_EXECDMP function (function 7 in the system library).
When invoked, the entire system reinitializes to the CD player state.

The player provides playback controls (play, pause, stop, skip, scan,
repeat, shuffle) displayed as a graphical interface on screen. It
supports regular audio CDs and CD+G (CD+Graphics) discs.


## System Settings Screens

The system settings screens are displayed when:
- SMPC cold reset detected (STE=0 in OREG0): Set Clock screen
- L+R held during reset: System Settings menu

These screens handle:
- Date and time configuration (written to SMPC RTC via SETTIME command)
- Language selection (6 languages: English, German, French, Spanish,
  Italian, Japanese)
- Help window settings
- Audio output settings (stereo/mono)
- Sound effects toggle
- Backup RAM memory manager (list, delete saves)

The settings code is in the compressed system library. The graphics
(fonts, menu tiles) are in the $005240-$006F00 region.

