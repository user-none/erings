# SCU Interrupt Handling

The exception/interrupt vector table the BIOS installs in
Work-RAM-H at `$06000000`, the SCU vector trampolines at
`$06000840+`, and the dispatcher that resolves a vector number to
a game-installed handler via the table at `$06000900`. SDK
services that interact with this table (`SYS_SETUINT`,
`SYS_GETUINT`) are in
[system_services.md](system_services.md).

## Work RAM-H Vector Table ($06000000)

After Phase 4, the interrupt vector table at $06000000 contains:

### Exception Vectors (VBR + $000 - $0FC)

Most exception vectors point to $0600094A (default handler — `RTE; NOP`).
Notable exceptions:

| VBR Offset | Vector | Target | Purpose |
|------------|--------|--------|---------|
| +$010 | 4 | $0600094E | General illegal |
| +$018 | 6 | $0600094E | Slot illegal |
| +$024 | 9 | $0600094E | CPU address error |
| +$028 | 10 | $0600094E | DMA address error |
| +$02C | 11 | $00000420 | NMI (points to BIOS ROM) |

### SCU Interrupt Vectors (VBR + $100 - $17C)

These are the external interrupt vectors used by Saturn hardware. Each vector
points to a trampoline stub in Work RAM-H. The stub pushes R0, loads
the vector number into R0, and branches to the common interrupt dispatcher.

| VBR Offset | Vector | Stub Address | SCU Source |
|------------|--------|-------------|------------|
| +$100 | 64 | $06000840 | V-Blank IN |
| +$104 | 65 | $06000846 | V-Blank OUT |
| +$108 | 66 | $060008F0 | H-Blank IN |
| +$10C | 67 | $0600084C | Timer 0 |
| +$110 | 68 | $06000852 | Timer 1 |
| +$114 | 69 | $06000858 | DSP End |
| +$118 | 70 | $0600085E | Sound Request |
| +$11C | 71 | $06000864 | System Manager |
| +$120 | 72 | $0600086A | Pad Interrupt |
| +$124 | 73 | $06000870 | Level 2 DMA |
| +$128 | 74 | $06000876 | Level 1 DMA |
| +$12C | 75 | $0600087C | Level 0 DMA |
| +$130 | 76 | $06000882 | DMA Illegal |
| +$134 | 77 | $06000888 | Sprite Draw End |

Vectors 78-79 (+$138, +$13C) point to $0600094A (default RTE handler).

### Interrupt Trampoline Mechanism

Each SCU interrupt is handled through a three-stage dispatch:

**Stage 1: Trampoline Stub** (6 bytes per vector, at $06000840+)

Each interrupt vector points to a small stub that identifies the interrupt
source and branches to the common dispatcher. For vector `V` at stub
address `S`:

```
S+0:  push R0 onto the stack
S+2:  branch to common_dispatcher at $060008F4
S+4:  load R0 = V   (executes in the BRA delay slot, before the branch lands)
```

13 SCU interrupt stubs (vectors 64-65, 67-77) form a contiguous block at
$06000840-$0600088D; each is 6 bytes (3 instructions), differing only in
the immediate `V` loaded into R0.

H-Blank IN (vector 66) is placed at $060008F0 — immediately before the
dispatcher — and uses the same push-and-load pattern (push R0; R0 = 66)
but falls through into the dispatcher at $060008F4 rather than branching.

**Stage 2: Common Dispatcher** (at $060008F4, BIOS offset $EF4)

The common handler uses R0 (vector number) to index into two tables, sets
the SR interrupt-priority level, masks the source in the IMS shadow
and SCU IMS register, then calls the game-installed handler via JSR.

Pool references used by the dispatcher (PC-relative loads from $F54..$F68):

| Pool | Value | Purpose |
|------|-------|---------|
| $F54 | $00F0 | SR mask for "all interrupts masked" (used by epilogue) |
| $F58 | $06000348 | SCU IMS shadow in WRAM-H |
| $F5C | $25FE00A0 | SCU IMS register |
| $F60 | $06000980 | Priority/mask table base |
| $F68 | $06000900 | Handler function-pointer table base |

```python
def common_dispatcher():
    # Entry: R0 = vector number (64..95). R0 was already pushed by the
    # trampoline; saved_R0 sits at the top of the stack.

    stack.push(R1, R2, R3)                      # save caller-clobbered regs
    saved_ims = mem.L[0x06000348]               # snapshot SCU IMS shadow
    stack.push(saved_ims)                       # park snapshot for epilogue
    stack.push(R4, R5)                          # remaining caller-clobbered regs

    idx       = R0 * 4                          # byte offset into both tables
    pri_entry = mem.L[0x06000980 + idx]         # priority/mask table[vector]
    sr_value  = pri_entry >> 16                 # SR.I level for nested preemption
    ims_mask  = sign_extend_word(pri_entry & 0xFFFF)

    SR = sr_value                               # allow higher-priority preemption

    ims = saved_ims | ims_mask                  # mask this source for the duration of the handler
    mem.L[0x06000348] = ims                     # update shadow
    mem.L[0x25FE00A0] = ims                     # write SCU IMS

    handler = mem.L[0x06000900 + idx]           # handler_table[vector]
    stack.push(R6, R7, PR, GBR)
    handler()                                   # JSR @handler
    # ... falls through into the epilogue below
```

The dispatcher:
1. Indexes the **priority/mask table** at $06000980 by vector number
   - High 16 bits = SR value to set (controls which higher-priority
     interrupts can preempt this handler)
   - Low 16 bits = IMS bitmask identifying this interrupt source
2. Updates the SCU interrupt mask ($06000348 shadow and $25FE00A0 IMS register)
3. Indexes the **handler function pointer table** at $06000900 by vector number
4. Calls the handler function via JSR

**Stage 3: Handler Function** (at address from $06000900 table)

The actual interrupt handler code. Games register their handlers using
the system library function SYS_SETUINT, which writes the function address
into the $06000900 table.

The default handler at $0600083C is simply `RTS; NOP` (return immediately).

**Epilogue** (after JSR returns):

```python
def common_dispatcher_epilogue():
    # Entered after the handler's RTS. Stack still holds everything
    # the dispatcher pushed, plus the saved_ims snapshot.

    stack.pop_into(GBR, PR, R7, R6, R5, R4)
    saved_ims = stack.pop()                     # snapshot from entry

    SR                = 0x00F0                  # re-mask all interrupts
    mem.L[0x06000348] = saved_ims               # restore IMS shadow
    mem.L[0x25FE00A0] = saved_ims               # restore SCU IMS

    stack.pop_into(R3, R2, R1, R0)              # R0 was pushed by trampoline
    rte()                                       # return from exception
```

The real BIOS code interleaves the pool-word loads ($F54, $F58, $F5C)
with the register pops to keep the SH-2 pipeline busy; the net effect
matches the order shown above.

### Runtime Tables

| Base | Effective SCU Slots | Purpose |
|------|---------------------|---------|
| $06000900 | $06000A00-$06000A7F | Handler function pointer table |
| $06000980 | $06000A80-$06000AFF | Priority/mask table |

Both tables are indexed by `vector_number * 4` from the table base.
Real BIOS SYS_SETUINT does `SHLL2 R4` and stores at `$06000900 + R4`
without subtracting $40, so for SCU vectors $40-$5F the effective
slots land at $06000A00-$06000A7F (handler) and $06000A80-$06000AFF
(priority/mask). Addresses below those slots ($06000900-$060009FF
for handler; $06000980-$06000A7F for priority/mask) overlap with
BIOS dispatcher code and post-dispatcher data in WRAM-H, but the
BIOS only installs handlers for vectors $40+ and the dispatcher
only indexes from vec*4, so the lower addresses are never read or
written through the runtime tables.

The handoff dump shows $06000A00-$06000A7F filled with $0600083C
(the BIOS "no handler installed" placeholder, an RTS+NOP body) and
$06000A80-$06000AFF filled with the precomputed
`(SR_I << 16) | IMS_mask` pairs. The priority/mask table controls
interrupt nesting (higher-priority interrupts can preempt lower-
priority handlers). The SCU IMS shadow at $06000348 tracks which
interrupt sources are masked.

### A-Bus / External Interrupt Vectors (VBR + $140 - $17C)

| VBR Offset | Vector | Stub Address | Source |
|------------|--------|-------------|--------|
| +$140 | 80 | $0600088E | A-Bus Interrupt |
| +$144 | 81 | $06000894 | - |
| +$148 | 82 | $0600089A | - |
| +$14C | 83 | $060008A0 | - |
| +$150 | 84 | $060008A6 | - |
| +$154 | 85 | $060008AC | - |
| +$158 | 86 | $060008B2 | - |
| +$15C | 87 | $060008B8 | - |
| +$160 | 88 | $060008BE | - |
| +$164 | 89 | $060008C4 | - |
| +$168 | 90 | $060008CA | - |
| +$16C | 91 | $060008D0 | - |
| +$170 | 92 | $060008D6 | - |
| +$174 | 93 | $060008DC | - |
| +$178 | 94 | $060008E2 | - |
| +$17C | 95 | $060008E8 | - |

