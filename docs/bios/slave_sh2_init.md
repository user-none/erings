# Slave SH-2 Init

Bring-up sequence for the slave SH-2 after the disc's IP releases
it via SMPC SSHON. Covers the slave's reset-vector path through
`$00000200`, the secondary init body at `$06000600` (copied from
BIOS `$00000C00`), and observed usage patterns in shipping games
that drive the slave as a coprocessor.

## Slave SH-2 Init

The slave SH-2 starts in reset on power-on. SMPC SSHON ($02)
releases it; SMPC SSHOFF ($03) would re-assert reset if issued. The
BIOS does not issue either command during boot, so the slave remains
in its power-on reset state until the disc's IP code issues SSHON.

Each SSHON pulses the slave's reset line: the SH-2 then re-fetches
PC from the power-on reset vector at `mem.L[$00000000]` (= `$20000200`,
cache-through P0 mirror of `$00000200`) and SP from `mem.L[$00000004]`
(= `$06002000`). So the slave enters the same code path master takes
after power-on, but with a different setup state (notably BCR1 bit
15, set differently for slave; see Phase 3 in boot_sequence.md).

### Slave path through `$00000200`

The reset vector is shared between master and slave. The fork is BCR1
bit 15: the SH-2 chip wires that bit (read-only MASTER bit) to 1 on
the slave and 0 on the master.

- **Pool** ($0002D4..$0002F4 — shared with master cold-boot):

| Offset | Value | Purpose |
|--------|-------|---------|
| $2D4 | $E0000000 | cache-flush BRAF base |
| $2D8 | $FFFFFFE0 | BCR1 register address |
| $2E8 | $06000240 | "2RDY" poll address (master → slave) |
| $2EC | $46000240 | cache-write mirror of $06000240 |
| $2F0 | $32524459 | "2RDY" magic |
| $2F4 | $06000600 | WRAM-H slave-init entry |

```python
def reset_entry():
    # Shared between master and slave; BCR1 bit 15 selects the fork.
    cache_init()                                  # sub at $000380
    bcr1 = mem.L[0xFFFFFFE0]                      # BCR1 register
    bsc_init()                                    # sub at $000478

    if (bcr1 >> 15) & 1:
        warm_boot_poll()                          # slave path at $00022A
    else:
        cold_boot_continue()                      # master path falls into Phase 4
```

For the slave, the BCR1 test passes and execution branches to the
warm-boot path at `$00022A`:

```python
def warm_boot_poll():
    # Slave entry. Wait for master to publish "2RDY" at $06000240,
    # then jump to the WRAM-H slave-init body.
    branch_through(0xE0000000)                    # BRAF cache-flush sentinel

    while True:
        # Purge the cache line for $06000240 by writing to its
        # cache-through-write mirror at $46000240, then re-read.
        mem.L[0x46000240] = 0
        if mem.L[0x06000240] == 0x32524459:       # "2RDY"
            break
        busy_wait(255)                            # DT R0 inner loop

    jump_to(0x06000600)                           # WRAM-H slave init
```

So the slave polls `$06000240` for the `"2RDY"` magic (set by
master during cold-boot init around `$0600118C`), then JMPs to
`$06000600` in WRAM-H. The `$06000600` code itself comes from a
BIOS Phase 4 copy of the source at `$00000C00`.

### `$06000600` slave init (source at BIOS `$00000C00`)

The body interleaves `XOR Rn,Rn` register-clearing instructions
between pool loads as pipeline fill; the pseudocode below flattens
that into a single "clear all GPRs except SP" step at the end.

- **Pool values used**:

| Value | Purpose |
|-------|---------|
| $06001000 | default slave SP |
| $06000400 | slave VBR (vector table base) |
| $06000244 | "2RDS" magic destination (slave → master) |
| $32524453 | "2RDS" magic value |
| $060002AC | slave SP slot (= IP System ID +$EC, Stack-S, copied here by the boot header copy) |
| $06000250 | game-supplied slave-entry slot |

```python
def slave_init():           # entered at $06000600
    SP = 0x06001000                               # default slave SP
    sub_0600071C()                                # additional init (interrupt setup, etc.)

    VBR = 0x06000400                              # slave's vector base
    mem.L[0x06000244] = 0x32524453                # publish "2RDS" (slave → master)

    GBR  = 0
    PR   = 0
    clrmac()                                      # MACL = MACH = 0

    # Slave SP from $060002AC (IP System ID +$EC, Stack-S, installed
    # there by the boot header copy). Non-zero overrides the default.
    override_sp = mem.L[0x060002AC]
    if override_sp != 0:
        SP = override_sp

    entry = mem.L[0x06000250]                     # game-supplied slave entry
    R0 = R1 = R2 = R3 = 0                         # clear all GPRs except SP
    R4 = R5 = R6 = R7 = 0
    R8 = R9 = R10 = R11 = 0
    R12 = R13 = R14 = 0
    SR = 0                                        # all interrupts enabled

    jump_to(entry)
    # If entry is the BIOS default ($06000646), slave halts in a tight
    # `BRA self` loop and serves only as an IRQ-driven coprocessor.
```

The two magic values:
- `"2RDY"` ($32524459) at `$06000240` — **master → slave**. Master
  cold boot writes this around BIOS `$0000118C` (from the Phase 4
  copy at `$0600118C`) before handing off to IP. Slave's warm-boot
  poll waits for it.
- `"2RDS"` ($32524453) at `$06000244` — **slave → master**. Slave
  init writes this at `$06000614` after VBR setup. Master code
  that needs to know slave is up reads this slot.

The game's slave entry is at `mem.L[$06000250]` (game-supplied: the
game writes it before issuing SSHON, or in some games after — the
slave wait loop accommodates either order). The slave SP at
`mem.L[$060002AC]` is normally **not** game-supplied: the boot header
copy installs it there from the IP System ID Stack-S field (+$EC). A
game can still overwrite the slot itself, but typically relies on the
value the disc header carries (e.g. PDS uses $06003700, larger than
the $06001000 default, for its coprocessor routines).

### Slave usage patterns observed in shipping games

Two patterns:

1. **Slave runs game code** (NiGHTS): game writes its slave entry
   to `mem.L[$06000250]` before issuing SSHON. Real-BIOS slave init
   ends with `JMP @mem.L[$06000250]` and slave runs game code from
   that PC for the duration.

2. **Slave acts as IRQ-driven coprocessor** (Waku Waku 7, etc.):
   game leaves `mem.L[$06000250]` at the BIOS default (`$06000646`
   halt loop) and installs an FRT Input-Capture Interrupt handler
   in the slave VBR table at `$06000400 + vec*4`. Master sends
   commands by writing the MINIT region (`$01000000-$017FFFFF`),
   which the bus translates into a slave FRT input-capture event.
   The slave's ICI handler (e.g., WW7's at `$06012AA0`) dispatches
   commands and signals master back via SINIT writes
   (`$01800000-$01FFFFFF`).

   For this pattern slave's on-chip state must have been
   pre-configured: FRT TIER.ICIE = 1, INTC VCRC.ICI = game's vector
   (`$64` in WW7), INTC IPRB FRT priority > 0, and SR.I < that
   priority. The real-BIOS slave-init chain at `$06000600` →
   `$0600071C` does this configuration before the slave reaches
   the halt loop.

