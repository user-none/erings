# Disc Security Check

The 4 KB authentication code at BIOS `$040000`, copied to
`$06020000` at boot, that validates a disc before allowing it to
run as a game. Includes the copyright-string requirement at IP.BIN
offset `$DC0`, the auth-code initialization sequence, and the
data tables at `$040438-$080000` that drive the cryptographic
challenge.

## Security Check Code ($040000)

This 4096-byte block at BIOS offset $40000 is copied to $06020000 at
runtime by sub_1A18. It contains the disc authentication logic.

### Copyright String ($040020)

```
COPYRIGHT(C) SEGA ENTERPRISES,LTD. 1994 ALL RIGHTS RESERVED
```

64 bytes at offset $020 within the block. This string must be present
in the disc's IP.BIN for the security check to pass.

### Initialization ($040000)

- **Entry**: BIOS $040000 (copied to $06020000 at runtime by sub_1A18)
- **Pool** ($040060..$040073, four init longwords plus the post-success target):

| Offset | Value | Purpose |
|--------|-------|---------|
| $040060 | $06028000 | post-success jump target |
| $040064 | $000000F0 | initial SR (all interrupts masked) |
| $040068 | $06020000 | initial GBR (security-code base = where this block runs) |
| $04006C | $06000000 | initial VBR (Work RAM-H base) |
| $040070 | $0601FFF0 | initial SP (top of auth-code stack) |

```python
def security_check_entry():
    # Self-contained block running at $06020000. The init pool at
    # $040064..$040073 holds the initial register state; the entry
    # streams the first four longwords into SR/GBR/VBR/SP via LDC.L /
    # MOV.L post-increment from a single base pointer (MOVA-loaded).
    SR  = 0x000000F0       # interrupts masked
    GBR = 0x06020000
    VBR = 0x06000000
    SP  = 0x0601FFF0

    while authenticate() == 0:      # sub_40074; returns 0 to request retry
        pass                         # retry until non-zero (success)

    jump_to(mem.L[0x040060])         # = $06028000
```

On success, jumps to $06028000 (the pool constant). This is one path
the BIOS can take to reach disc code; whether a particular game's
boot actually executes this JMP depends on what the boot state
machine selects. Some games hand off via a different dispatch that
enters the IP load window directly. See "BIOS -> Disc Handoff" under
"IP.BIN Structure and Loading".

### Authentication (sub_40074)

1. Writes 0 to SMPC IOSEL ($2010007D, the I/O-select register), SMPC EXLE ($2010007F, the external-latch register), and WRAM-H $0601FFF1
2. Reads boot state from $0600022C
3. If state == $22 or state == $02: selects auth command type 8
4. Otherwise: selects auth command type 1
5. Writes the selected command type byte to WRAM-H $0601FFF0
6. Calls sub_400CC twice (auth-step dispatcher); the second call passes R4=0
7. R0 holds the result on return (the function's RTS epilogue restores
   the caller's R14 from the stack, so R14 is not used as a return slot)

sub_400CC dispatches on R4 (command type) via a 3-way CMP/EQ chain:

- Command 0 (R4 == 0): one call `sub_40116(R4=0)`.
- Command 1 (R4 == 1): four calls in order: `sub_40116(R4=13)`,
  `sub_40116(R4=14)`, `sub_40116(R4=1)`, `sub_40116(R4=9)`. The
  return from the third call (R4=1) is saved in R14 and becomes the
  final R0 on exit.
- Command 8 (R4 == 8): one call `sub_40116(R4=8)`.
- Any other R4 value falls through to the default exit with R14=0
  (so R0 returns 0).

The labels "$0400EA", "$0400D4", and "$0400F2" are internal BT-jump
targets inside sub_400CC, not separate subroutines. All command
paths funnel through `sub_40116` with different R4 selectors.

sub_40116 looks up an entry in a table at $040400 by command key, then
dispatches to helper routines (at $06020238/$06020290/$060202A4 within
the copied block) that compare and copy the disc security-header data
referenced by the entry's offset.


## Security Check Data ($040438-$080000)

The security check authentication table at $040400 contains 6 command
entries used by sub_40116 for the disc security check:

| Index | Command | Offset |
|-------|---------|--------|
| 0 | $00 | $00000038 |
| 1 | $01 | $0000AD24 |
| 2 | $0D | $00018B54 |
| 3 | $0E | $000228B0 |
| 4 | $09 | $00028068 |
| 5 | $08 | $00034490 |
| 6 | $FF | (end marker) |

Each entry's offset field points into the security check data area
($040438-$080000), which holds the reference data the helper routines
compare and copy. The command field selects which security-check step
to execute.

The remaining ~255 KB of this region contains the authentication data
sequences themselves. This data is the input to the copy-protection
challenge issued during boot.
