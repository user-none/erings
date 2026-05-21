# SH-2 CPU Core

Clean room implementation of the Hitachi SH-2 (SH7604) processor core for
use in Sega Saturn emulation.

## Instruction Set

All SH-2 instructions are implemented:

- Arithmetic (33): ADD, ADDI, ADDC, ADDV, SUB, SUBC, SUBV, NEG, NEGC,
  CMP/EQ, CMP/GE, CMP/GT, CMP/HI, CMP/HS, CMP/PL, CMP/PZ, CMP/STR,
  CMP/EQ #imm, DIV0U, DIV0S, DIV1, MUL.L, MULS.W, MULU.W, DMULS.L,
  DMULU.L, MAC.L, MAC.W, DT, EXTS.B, EXTS.W, EXTU.B, EXTU.W
- Logic (13): AND, OR, XOR, TST, NOT, AND/OR/XOR/TST #imm,
  AND.B/OR.B/XOR.B/TST.B @(R0,GBR)
- Shift/Rotate (14): SHLL, SHLR, SHAL, SHAR, SHLL2/8/16, SHLR2/8/16,
  ROTL, ROTR, ROTCL, ROTCR
- Branch (12): BRA, BRAF, BSR, BSRF, BT, BF, BT/S, BF/S, JMP, JSR,
  RTS, RTE
- Data Transfer (39): MOV variants (direct, indexed, post-increment,
  pre-decrement, GBR-relative, PC-relative, 4-bit displacement), MOVA,
  MOVT, SWAP.B, SWAP.W, XTRCT
- System Control (30): CLRT, SETT, CLRMAC, NOP, SLEEP, LDC/STC for
  SR/GBR/VBR (direct and memory), LDS/STS for MACH/MACL/PR (direct and
  memory), TRAPA
- Bit (1): TAS.B

Group 0xF opcodes are unused on SH-2 (no FPU).

### Illegal Instructions

General illegal instructions (undefined opcodes) are dispatched to
vector 4 through the synchronous `serviceException` path: SR and PC
are pushed to the stack and PC is loaded from `VBR + 4 * 4`. The
stacked PC value is the PC after the illegal opcode because the
fetch advance is not rolled back. HM Sec 4.5.4 specifies the stacked
PC should be the start address of the undefined code; that divergence
is not modeled because an illegal instruction is typically a terminal
fault and the resulting stack frame is not used for recovery.

Slot illegal exception (vector 6, HM Sec 4.5.3) is not detected.
Undefined code placed in a delay slot is dispatched through the same
general-illegal path (vector 4) rather than the slot-illegal path.
Instructions that rewrite the PC (JMP, JSR, BRA, BSR, RTS, RTE, BT,
BF, TRAPA, BF.S, BT.S, BSRF, BRAF) placed in a delay slot are also
not detected as slot illegal; they execute normally.

## Pipeline

The SH-2 has a 5-stage pipeline. This implementation does not model the
full pipeline but does implement:

- Delayed branch execution (branch delay slots)
- Load-use hazard detection with 1-cycle stall insertion
- Store data forwarding (stores to the same register as a prior load
  do not stall)
- Multi-cycle instruction decomposition via the pending operation system
- Per-cycle bus activity tracking (BusNone, BusRead, BusWrite, BusHeld)

### Multi-Cycle Instructions

Complex instructions are decomposed into per-cycle micro-ops using the
pending operation system. Each Clock() call advances exactly one cycle.
Decomposed instructions include:

- STC.L @-Rn (2 cycles)
- LDC.L @Rm+,CR (3 cycles)
- AND.B/OR.B/XOR.B @(R0,GBR) (3 cycles: read, logic, write)
- TST.B @(R0,GBR) (3 cycles: read+test, 2 stall)
- TAS.B @Rn (4 cycles: EX, read, modify, write - bus held)
- RTE (3 cycles: EX, read PC, read SR + delay branch)
- TRAPA (8 cycles: EX, write SR, write PC, vector calc, read vector,
  3 pipeline refill)
- Interrupt exception (5 cycles: internal, write SR, write PC, read
  vector, pipeline refill)
- MAC.W/MAC.L (2 cycles: read @Rn, read @Rm + accumulate)

### Multiplier Contention

When the MA of a multiplier-type instruction (MUL/MAC) coincides with
the background multiplier stages ("mm") of a preceding multiplier-type
instruction, the second instruction's MA is extended until mm ends.
Per Programming Manual Section 7.2.3 and Table 7.1:

- MULS.W / MULU.W: mm runs 2 cycles after MA
- MUL.L / DMULS.L / DMULU.L: mm runs 4 cycles after MA
- MAC.W: mm runs 2 cycles after the second MA
- MAC.L: mm runs 4 cycles after the second MA

Only multiplier-type instructions stall on outstanding mm:

- MUL.L, MULS.W, MULU.W, DMULS.L, DMULU.L, MAC.W, MAC.L

MAC-register access instructions (LDS/STS/LDS.L/STS.L to MACH or MACL,
and CLRMAC) are listed in Table 7.1 as flat 1-cycle ops with no
contention range. They cause mm contention for a subsequent
multiplier-type instruction, but do not themselves stall on a
preceding mm.

The stall is implemented by tracking a per-CPU cycle timestamp at which
mm completes and by folding stall cycles into the follower's pending-op
count when its EX stage begins before that timestamp. The stall value
is clamped to each follower's Table 7.1 maximum extra-cycle count, so
total cycle counts match the documented ranges exactly in all pairings
(non-contending cases hit the Table 7.1 minimum; contending cases hit
the Table 7.1 maximum).

### IF/MA Bus Contention (not modeled)

Per Programming Manual Section 7.2.1, the instruction-fetch (IF) and
memory-access (MA) pipeline stages share the memory bus. When a
load/store's MA coincides with the next instruction's IF, the slot
splits into two bus cycles. On SH-2 there is an optimization:
longword-aligned code in on-chip memory has a single IF fetch two
instructions in one bus cycle; subsequent fetches consume no bus and
do not contend.

This contention is not modeled. Each Clock() call charges exactly one
cycle. Cycle counts are therefore understated for code with a high
memory-access density. System-level timing is largely unaffected
because peripheral schedulers (VDP1/VDP2/SCU/SCSP) tick on scanline or
sample boundaries rather than exact SH-2 cycles, so the internal
timing stays consistent even though absolute wall-clock speed runs
slightly fast.

If a game exhibits timing issues that correlate with memory-access-
heavy code paths (such as polling hardware registers in tight loops),
a bus-access cycle model can be added. The existing Bus.AccessCycles
interface can be extended to cost instruction fetches as well as data
accesses.

## Interrupts

- External interrupts via RequestInterrupt(level, vector)
- NMI via NMI() (level 16, always accepted)
- Interrupts accepted when level > IMASK
- IMASK set to interrupt level after servicing
- Higher priority pending interrupt replaces lower priority pending
- Interrupt clears SLEEP/halt state
- Synchronous exceptions (address error, TRAPA) handled separately

### Address error during instruction execution (not modeled correctly)

Hardware manual Sec 4.3.2 specifies that when an address error occurs
during an instruction's data access, exception handling begins **after
the failing bus cycle ends and the executing instruction completes**.
Sec 4.7 Table 4.11 specifies the stacked PC value as "address of
instruction after executed instruction." Sec 4.8.3 covers the nested
case (misaligned SP at exception entry) and requires hardware to
suppress recursive address errors so the handler can run.

The emulator does not implement any of that sequence. `cpu.write32`,
`cpu.write16`, `cpu.read32`, `cpu.read16`, and `cpu.fetchPC` all call
`addressError()` (cpu.go) which calls `serviceException(vecCPUAddr)`
**synchronously** from inside the failing bus access. Three things go
wrong as a result:

1. **The currently-executing instruction is not allowed to complete.**
   `serviceException` redirects PC to the address-error vector handler
   immediately. The HM-required final state of the instruction (R[m]
   post-increments for LDC.L / LDS.L / RTE / MAC.W / MAC.L, the WB
   write to GBR/VBR/SR/MACH/MACL/PR for LDC.L / LDS.L, the delay
   branch setup for RTE) is partly done and partly not, depending on
   which step inside the multi-cycle pending op the failure occurred.
2. **Stacked PC is wrong.** `serviceException` saves the live PC at
   the moment of the failing access. For most pending ops PC was
   already advanced past the failing instruction during fetch, so the
   stacked value resembles HM's "next instruction" but is not derived
   from a defined snapshot point and may differ for delayed branches.
3. **Misaligned SP at exception entry recurses incorrectly.** When SR
   or PC is pushed to a misaligned R15 the nested `bus.Write32` writes
   garbage at the misaligned bus address (`testBus` and the production
   buses do not enforce alignment), and the dispatch then continues
   normally. For multi-cycle pending exceptions (`popException`,
   `popTRAPA`) the pending state machine then runs its remaining
   steps, including the vector fetch, which **overwrites** the
   address-error PC redirect with the original exception's vector.
   The Sec 4.8.3 suppression is not implemented; in practice no loop
   occurs because the bus does not re-trigger an alignment check.

This divergence is intentionally left unmodeled. Reaching any of the
above paths requires the guest program to have already corrupted its
own state (misaligned SP, wild pointer dereference, garbage register
loaded into a control register and later used as an address). The
hardware's Sec 4.3.2 / 4.8.3 sequence is a crash-containment feature,
not a recovery feature: even on real hardware the resulting stack
frame is undefined and RTE-based recovery is impossible. No Saturn
BIOS or commercial title is observed to reach this path during normal
execution, so the emulator's incorrect behavior is not externally
visible during actual gameplay.

Modeling it correctly would require:

- A deferred-dispatch flag (e.g. `pendingAddrError bool`) instead of
  the synchronous `serviceException` call inside `addressError`.
- All `step*` handlers and `execute()` to drain to a clean boundary
  before the deferred dispatch fires.
- Delay-slot suppression per Sec 4.6.1 (defer one more instruction if
  the failing instruction was a delayed branch's delay slot).
- Sec 4.8.3 recursion suppression for the address-error handler's
  own stacking.

If a future Saturn title is found that depends on this path, that's
the design sketch.

## On-Chip Peripherals

### FRT (Free-Running Timer)

16-bit free-running counter with output compare and input capture.

- Configurable clock divider (fc/8, fc/32, fc/128, external)
- Output compare A/B with interrupt generation
- Counter clear on compare A match (CCLRA)
- Overflow detection with interrupt
- Input capture for inter-CPU communication (MINIT/SINIT)
- Byte-pair register access with temp register
- Registers: TIER, FTCSR, FRC, OCRA/B, TCR, TOCR, ICR
  (0xFFFFFE10-0xFFFFFE19)

### FRT Simplifications

- FTCSR flag clear protocol (manual Sec 11.2.5). Hardware requires the
  flag be read while set to 1 before a write of 0 can clear it. This
  implementation clears on any write of 0 regardless of whether the flag
  was read first. Ordinary polling sequences (read-to-check, then write
  0-to-clear) behave identically; only software that violates the two-
  step protocol sees divergent behavior.
- TCR.IEDGA input-edge select (Sec 11.2.6). The FRT has no external input
  capture pin on the Saturn; input capture is driven by MINIT/SINIT
  inter-CPU writes via `FRTInputCapture`. The IEDGA bit is storage only
  and does not affect capture behavior.
- TOCR.OLVLA / OLVLB output levels (Sec 11.2.7). No FTOA/FTOB output pin
  is modeled. The corresponding TOCR bits are currently stored as 0
  regardless of the written value; software that reads back the bits
  does not observe the write.
- FTI pin signal phasing (Sec 11.4.4 Fig 11.9). Hardware delays the
  input-capture signal by one timer-clock cycle when it coincides with
  an ICR upper-byte read. The Saturn has no FTI pin; capture is driven
  by MINIT/SINIT writes via `FRTInputCapture`. Latching is synchronous
  with the call and independent of any concurrent ICR read.

### INTC (Interrupt Controller)

Routes on-chip interrupt sources to the CPU with configurable priority
and vector numbers.

- Priority registers:
  - IPRA bits 15-12: DIVU overflow
  - IPRA bits 11-8: DMAC channel 0/1 transfer-end (channel 0 wins ties)
  - IPRA bits 7-4: WDT interval interrupt
  - IPRB bits 11-8: FRT (ICI/OCIA/OCIB/OVI sub-priority)
- Vector registers:
  - VCRC: FRT ICI (bits 14-8) and OCI (bits 6-0)
  - VCRD: FRT OVI (bits 14-8)
  - VCRWDT: WDT ITI (bits 14-8)
  - VCRDIV (DIVU, 0xFFFFFF0C), VCRDMA0/1 (DMAC, 0xFFFFFFA0/A8)
- ICR: NMIL (bit 15) tracks the NMI pin via `SetNMIL`; NMIE (bit 8)
  and VECMD (bit 0) are storage-only (see INTC Simplifications).
- Registers: 0xFFFFFE60-0xFFFFFE68, 0xFFFFFEE0-0xFFFFFEE4

### INTC Simplifications

The following documented INTC behaviors are not exercised by the Saturn
hardware we emulate and are not modeled here. Register bits that exist
for them are preserved as read/write storage only unless otherwise noted.

- SCI interrupts (IPRB bits 15-12, VCRA, VCRB). The serial communication
  interface is not modeled; the priority and vector fields are storage
  only. Hardware manual Sec 5.2.4 / 5.3.2 / 5.3.4 / 5.3.5.
- BSC refresh compare-match interrupt (VCRWDT bits 6-0). The refresh
  control unit inside the bus state controller does not raise CMI in
  this emulator; the vector field is storage only. IPRA bits 7-4 are
  used exclusively for WDT here (the manual shares them with BSC REF
  CMI). Hardware manual Sec 5.3.1 / 5.3.3.
- UBC interrupts. `isrcUBC` is reserved in the interrupt-source enum
  but no code asserts it. Hardware manual Sec 5.2.2 / Sec 6.
- SBYCR / STBY power-down (0xFFFFFE91). Stub storage with reserved-bit
  masking; STBY/SLEEP power semantics are not modeled. Hardware manual
  Sec 14.2.1.
- IRL auto-vector mode (ICR.VECMD=0, Table 5.3). The Saturn SCU
  synthesizes the vector itself and delivers it via `SetIRL(level, vec)`,
  so the INTC's internal auto-vector generator is never used. VECMD is
  storage only. Hardware manual Sec 5.2.3 / 5.3.8.
- NMI edge select (ICR.NMIE). `CPU.NMI()` unconditionally latches the
  request; the SMPC drives a single falling-edge NMI on reset-button
  press and that is the only mode used by Saturn software. NMIE is
  storage only. Hardware manual Sec 5.2.1 / 5.3.8.
- On-chip peripheral 2-cycle interrupt-recognition delay. The manual's
  usage note (Sec 5.7) requires two cycles between an on-chip source
  assertion and acceptance so software can clear a just-raised latch
  before RTE. Acceptance is immediate here; no Saturn game observed so
  far depends on the delay.
- External vector fetch bus cycle (Sec 5.2.3, Fig 5.4). The IVECF /
  D7-D0 external-vector-read path is never driven; vectors arrive on
  the `SetIRL` call directly from the SCU.

### DIVU (Division Unit)

Hardware division unit performing signed integer division.

- 32-bit / 32-bit division (write DVDNT to trigger)
- 64-bit / 32-bit division (write DVDNTL to trigger)
- Division by zero detection (overflow)
- Quotient overflow detection with saturation
- Optional overflow interrupt (OVFIE)
- Registers: DVSR, DVDNT, DVCR, VCRDIV, DVDNTH, DVDNTL
  (0xFFFFFF00-0xFFFFFF14)

### DIVU Simplifications

- Division timing (manual Sec 10.1.1 / 10.3.3). Hardware takes 39 cycles
  for a normal division and 6 cycles when overflow aborts the operation.
  This implementation completes the division instantly at register-write
  time. Software that intentionally interleaves non-DIVU work with a
  running division does not stall, but the final result appears in the
  result registers the same way.
- DVDNTL intermediate result on overflow with OVFIE=1 (Table 10.2). The
  manual says hardware leaves the operation result captured at the 6th
  overflow-detection cycle in DVDNTL. This implementation does not
  compute an intermediate result; the DVDNT write path unconditionally
  copies the dividend into DVDNTL before detecting overflow, and the
  OVFIE=1 branch returns without updating DVDNTL further, so the
  register reads back as the dividend. DVDNTH still holds the
  sign-extended dividend high half per table. With OVFIE=0 the
  saturating clamp behavior matches hardware.

### WDT (Watchdog Timer)

8-bit counter with selectable prescaler, usable as either a watchdog or
an interval timer.

- Interval timer mode (WTCSR.WT/IT=0): overflow sets WTCSR.OVF and raises
  an interval interrupt (ITI) through the INTC.
- Watchdog timer mode (WTCSR.WT/IT=1): overflow sets RSTCSR.WOVF.
- Eight prescaler selections via WTCSR.CKS (manual Table 12.3).
- A5/5A word-write protection (Sec 12.2.4) for WTCSR, WTCNT, and RSTCSR.
- Clearing WTCSR.TME resets WTCNT to 0 per Sec 12.2.2.
- Registers: WTCSR, WTCNT, RSTCSR (0xFFFFFE80-0xFFFFFE83)

### WDT Simplifications

- Watchdog-mode external reset output (Sec 12.3.1). Hardware asserts the
  WDTOVF pin for 128 system clocks and, if RSTCSR.RSTE=1, drives an
  internal reset for 512 clocks on watchdog-mode overflow. This
  implementation latches RSTCSR.WOVF and emits a log line; no pin output
  or chip reset is generated. No Saturn title reaches watchdog-mode
  overflow in normal operation.

### DMAC (Direct Memory Access Controller)

Two-channel DMA controller used by the Saturn BIOS and games for
memory-to-memory transfers.

- Per-channel SAR, DAR, TCR (24-bit), CHCR, VCRDMA.
- Shared DMAOR with DME, PR (fixed vs round-robin), AE, NMIF flags.
- Transfer sizes: byte, word, longword, 16-byte burst (CHCR.TS).
- Source/destination addressing modes: fixed, increment, decrement.
- Transfers move data atomically on register write; the CPU is stalled
  for a region-aware bus-occupation cost accumulated across each unit
  access via `Bus.AccessCycles`. Transfer-end interrupt is gated by
  CHCR.IE and routed via VCRDMAn.
- NMI sets DMAOR.NMIF, blocking new transfer starts until software
  clears it.
- Registers: SAR0/1, DAR0/1, TCR0/1, CHCR0/1, VCRDMA0/1, DMAOR, DRCR0/1
  (0xFFFFFF80-0xFFFFFFB0, DRCR at 0xFFFFFE71-0xFFFFFE72).

### DMAC Simplifications

- External DREQ / DACK pin signaling, single-address mode (CHCR.TA=1),
  acknowledge mode/level (AM, AL), DREQ edge/level detection (DS, DL),
  and the DRCR resource-select field (Sec 9.2.4, 9.2.6). The Saturn does
  not route any external DMA peripherals to the SH-2 DMAC; only
  auto-request and dual-address mode are used. The corresponding CHCR
  and DRCR bits are stored but not consulted.
- Cycle-steal vs burst-mode selection (CHCR.TB). Transfers move data
  atomically on trigger rather than interleaving with CPU access at the
  bus level. The post-transfer stall window prevents observable CPU
  activity during the transfer, so visible ordering matches burst-mode
  semantics; cycle-steal-style interleaving is not available. One
  consequence is that an NMI mid-stall (Sec 9.3.8 "Conditions for Both
  Channels Ending Simultaneously") cannot abort a partially-transferred
  block: the data has already moved by the time the stall starts, and
  CHCR.TE is latched when the stall drains. Hardware would only set TE
  if the NMI-coincident unit was the final one. The NMIF gate on future
  transfer starts is modeled correctly.
- DMA address-error generation (vector 10, Sec 9.3.2). No code path
  asserts a DMAC address error, so the exception is never taken.

## Cache

The SH-2 has a 4 KB on-chip cache (4-way set associative, 64 entries,
16 bytes per line). Cache lookup is not emulated: every memory access
goes directly to the bus. In an emulator, RAM access is already an
array index with no latency to hide, so modeling tag comparisons, LRU,
and line fills would add overhead with no benefit.

Reading current data from the bus is actually more correct than real
hardware in the dual-CPU case since there is no stale-data problem.

### What is implemented

The cache data array region (0xC0000000-0xDFFFFFFF) is backed by a
per-CPU 4 KB buffer (cacheData). Games commonly lock the cache and use
this region as fast CPU-internal scratch RAM for stacks and tight loops.
Each SH-2 has its own private buffer; the 4 KB is mirrored throughout
the region.

Other cache control regions are no-ops:
- 0x40000000-0x5FFFFFFF (associative purge): writes ignored
- 0x60000000-0x7FFFFFFF (address array): reads return 0, writes ignored
- 0x80000000-0xBFFFFFFF (reserved): reads return 0, writes ignored

CCR (0xFFFFFE92) is stored but has no functional effect beyond
read-back.

### Symptoms of missing cache emulation

If a game exhibits any of the following and other causes have been ruled
out, investigate whether full cache emulation is needed:

- Data corruption when both CPUs access the same memory region
- Graphics glitches after DMA transfers (CPU reads stale pre-DMA data)
- Game code that writes to 0x40000000+ addresses with no visible effect
- Game code that reads/writes CCR and expects cache state changes
- Game code that accesses 0x60000000+ for direct tag manipulation
- Timing-sensitive code that depends on cache hit/miss cycle counts

## Memory Access / Region Routing Simplifications

The SH-2 memory map (Hardware Manual Sec 7.1.5, Table 7.3) includes
several address ranges that the Saturn does not exercise in normal
operation. These ranges are modeled with reduced fidelity; each
entry below describes the documented hardware behavior, what erings
does, and why the simplification is safe for Saturn software.

- **D1 Cache data array mirroring** (Sec 8.4.8, Table 7.3). Hardware
  maps the 4 KB data array at 0xC0000000-0xC0000FFF and reserves the
  remainder of 0xC0001000-0xDFFFFFFF. erings mirrors the 4 KB buffer
  throughout the full 512 MB range. Saturn software accesses the
  buffer through the documented base, so the mirror is never
  observed.
- **D2 Associative-purge reserved alias** (Sec 8.4.7, Table 7.3).
  Hardware defines the associative purge region at
  0x40000000-0x47FFFFFF and reserves 0x48000000-0x5FFFFFFF. erings
  folds the two ranges into one no-op block.
- **D3 Address-array reserved alias** (Sec 8.4.9, Table 7.3).
  Hardware maps the address array at 0x60000000-0x600003FF; the rest
  of 0x60000000-0x7FFFFFFF is addressable via tag-address bit layout.
  erings treats the full 512 MB block as a single read-0 / write-
  ignored range because the cache is not modeled.
- **D4 Reserved block 0x80000000-0xBFFFFFFF** (Table 7.3). Hardware
  reserves this range. erings returns 0 on read and drops writes.
- **D5 SDRAM-mode setting area and reserved on-chip block**
  (Table 7.3). Hardware maps 0xFFFF8000-0xFFFFBFFF to synchronous
  DRAM mode registers and reserves 0xFFFFC000-0xFFFFFDFF. The Saturn
  does not wire SDRAM to the SH-2, so neither range is exercised by
  software. erings routes these through readOnChip/writeOnChip where
  they are unhandled: reads return 0 and writes are dropped.
- **D6 FMR not modeled** (Sec 3.4.2). Hardware provides the Frequency
  Modification Register at 0xFFFFFE90 for PLL multiplier selection.
  The Saturn drives the SH-2 clock from SMPC via CKCHG, so FMR is
  unused. erings does not map the register: reads return 0 and writes
  are dropped.
- **D7 BSC registers other than BCR1** (Sec 7.2). Hardware defines
  BCR2 (0xFFFFFFE4), WCR (0xFFFFFFE8), MCR (0xFFFFFFEC), RTCSR
  (0xFFFFFFF0), RTCNT (0xFFFFFFF4), and RTCOR (0xFFFFFFF8) for wait-
  state programming and DRAM refresh. The Saturn configures these
  through the SCU glue logic rather than the SH-2 BSC, and the values
  are not inspected by software. erings only models BCR1's MASTER bit;
  the remaining BSC registers return 0 on read and drop writes.
- **D8 Undefined access-size clobber on 32-bit-only on-chip registers**
  (Table 9.2 Note 3, Table 10.1 Note 1). Manual declares DVSR / DVDNT /
  DVDNTH / DVDNTL and SAR / DAR / TCR / CHCR / VCRDMA / DMAOR as
  32-bit access only; byte and word accesses are undefined. erings
  handles such accesses by routing through the 32-bit dispatch path
  with a zero-extended value, which clobbers the unaddressed bytes of
  the register. Reads return the low 16 bits (word) or the big-endian
  byte from the 32-bit value (byte). This is one valid undefined-
  behavior interpretation; Saturn software uses only longword access
  for these registers.
- **D9 Table 4.6 address-error source rows not detected** (Sec 4.3.1).
  The odd-PC and misaligned-data rows are detected; the following
  rows are not. erings services the access normally instead of
  trapping to vector 9. Reaching any row requires prior memory or
  stack corruption and no observed Saturn title exercises these
  paths.
  - Instruction fetch from on-chip peripheral space
    (`$FFFFFE00-$FFFFFFFF`).
  - PC-relative `MOV.W`/`MOV.L @(disp,PC)` and `MOVA` targeting
    cache purge (`$40000000-$5FFFFFFF`), address array
    (`$60000000-$7FFFFFFF`), or on-chip I/O space.
  - TAS.B targeting cache purge, address array, data array
    (`$C0000000-$DFFFFFFF`), or on-chip I/O space.
  - Byte access in `$FFFFFF00-$FFFFFFFF`. Services the access
    through the D8 path; see D8.
  - Longword access in `$FFFFFE00-$FFFFFEFF`. Services the access
    against the underlying 16-bit register.

## References

- SH7604 Hardware Manual
  - Section 8: Cache (pages 213-230, Tables 8.3-8.4)
- SH-1/SH-2 Programming Manual
  - Section 6: Instruction descriptions
