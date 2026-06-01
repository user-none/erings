# Disc Security Check

How the BIOS verifies that an inserted disc is a genuine Saturn disc before
running it. Authentication itself is a CD-block (SH-1) function; the main SH-2
BIOS drives it through CD-block commands, gates boot on the result, and
validates the disc's IP.BIN header and region.

## CD-block disc authentication

The disc carries a security pattern in the lead-in area. The CD block's SH-1
microcontroller reads it and performs the authentication. The main BIOS
interacts through two CD-block commands (see cd_block_interface.md):

| Command | Registers | Purpose |
|---------|-----------|---------|
| $E0 Authenticate Disc | CR1=$E000, CR2=CR3=CR4=$0000 | start authentication |
| $E1 Get Auth Status | CR1=$E100; response CR2 = auth status | read the result |

An authenticated disc reports auth-status value 4.

## Boot disc-validation state machine

During the boot animation the BIOS runs a disc-validation state machine whose
state lives at Work-RAM-H `$060003A0`, advanced each frame by the animation
loop (`$001904` -> `$000029D4`):

- State 0 (`$00002B74`): checks HIRQ for EFLS (`$0200`). If set, issues the
  auth-status query - `$00002B74` calls `$0049F4`, which builds the CD-block
  command packet and sends it through `$004B74` / `$004B86` - and checks for the
  authenticated result (value 4). On success the dispatcher `$000029D4`
  advances to state 1.
- State 1 (`$00002D4C`): checks validation progress.

EFLS must be set in HIRQ by a prior CD-block file-system operation before
validation can start; see boot_library.md ("Disc Validation During Animation").

## IP.BIN header / region validation

After the boot CD sequence reads IP.BIN into Work RAM (cd_block_interface.md,
"BIOS Boot CD Block Command Sequence"), the BIOS validates:

- the hardware identifier `SEGA SEGASATURN ` at the start of the IP header, and
- the disc's region code against the console's region.

A disc that fails authentication or header / region validation is not booted as
a game. See ip_bin.md for the IP header layout.
