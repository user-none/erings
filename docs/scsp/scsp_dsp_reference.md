# SCSP DSP Instruction Format Reference

Source: AICA (FQ8005) Sound-block User's Manual Ver. 1.00

The AICA DSP is a superset of the SCSP DSP with the same instruction encoding
but larger register files (128 COEF vs 64, 64 MADRS vs 32, 64 slots vs 32).
This document covers only what applies to the Saturn SCSP. The AICA manual
was used as the primary source since the SCSP Users Manual lacks DSP instruction
format details.

## DSP Overview

- 128-step microprogram (MPRO), each instruction is 64 bits (4 x 16-bit words)
- Executes all 128 steps once per sample tick (44.1 KHz)
- Multiply-accumulate pipeline with 24-bit audio data path
- Ring buffer in sound RAM for delay effects (reverb, chorus, echo)
- MDEC_CT counter decrements by 1 each sample (ring buffer pointer)

## DSP Internal Registers

| Register | Width | Count | Purpose |
|----------|-------|-------|---------|
| TEMP     | 24-bit | 128  | Work buffer (ring buffer, pointer decrements each sample) |
| MEMS     | 24-bit | 32   | Sound memory input data buffer |
| MIXS     | 20-bit | 16   | Mixed slot output data buffer (input from slot mixer) |
| EFREG    | 16-bit | 16   | Effect output registers (returned to mixer) |
| COEF     | 13-bit | 64   | Filter coefficients |
| MADRS    | 16-bit | 32   | Memory address registers |
| MPRO     | 64-bit | 128  | Microprogram instructions |

### Runtime Registers (not memory-mapped)

| Register  | Width  | Purpose |
|-----------|--------|---------|
| INPUTS    | 24-bit | Input data latch (loaded from MEMS/MIXS/EXTS via IRA) |
| SFT_REG   | 26-bit | Shift register / accumulator (1D pipeline stage) |
| FRC_REG   | 13-bit | Fraction register (for interpolation / Y multiplier input) |
| Y_REG     | 24-bit | Y bus latch (loaded from INPUTS via YRL) |
| ADRS_REG  | 12-bit | Address register (for ring buffer offset) |
| MDEC_CT   | 16-bit | Ring buffer decay counter (decrements each sample, wraps by RBL) |

## DSP Memory Map (Saturn SCSP)

| Offset   | Content | Size |
|----------|---------|------|
| 0x100700 | COEF[12:0] | 128 bytes (64 entries x 2 bytes, 13-bit values) |
| 0x100780 | MADRS[16:1] | 64 bytes (32 entries x 2 bytes) |
| 0x100800 | MPRO[63:0] | 1024 bytes (128 steps x 4 words x 2 bytes) |
| 0x100C00 | TEMP[23:0] | 512 bytes (128 entries x 2 words x 2 bytes) |
| 0x100E00 | MEMS[23:0] | 128 bytes (32 entries x 2 words x 2 bytes) |
| 0x100E80 | MIXS[19:0] | 64 bytes (16 entries x 2 words x 2 bytes) |
| 0x100EC0 | EFREG[15:0] | 32 bytes (16 entries x 2 bytes) |

## Microprogram Instruction Format (64 bits)

The instruction is stored as 4 x 16-bit words in big-endian order:
MPRO[63:48], MPRO[47:32], MPRO[31:16], MPRO[15:0]. The AICA encoding uses
55 active bits; the Saturn SCSP uses fewer because MASA is 5 bits (vs 6 on
AICA, smaller MADRS) and CRA is 6 bits (vs 7 on AICA, smaller COEF). The
unused high bits in each shrunken field are encoded as zeros.

### Instruction Fields

| Field  | Width | Description |
|--------|-------|-------------|
| TRA    | 7     | TEMP read address (offset, added to MDEC_CT for ring buffer) |
| TWT    | 1     | TEMP write trigger (1 = write result to TEMP[TWA]) |
| TWA    | 7     | TEMP write address (offset, added to MDEC_CT) |
| XSEL   | 1     | X operand select: 0=TEMP data, 1=INPUTS data |
| YSEL1  | 1     | Y operand select bit 1 |
| YSEL0  | 1     | Y operand select bit 0 |
| IRA    | 6     | Input read address for INPUTS latch |
| IWT    | 1     | Input write trigger (1 = write to MEMS[IWA]) |
| IWA    | 5     | Input write address (MEMS destination) |
| TABLE  | 1     | Address mode: 0=ring buffer (add MDEC_CT), 1=table (no MDEC_CT) |
| MWT    | 1     | Memory write trigger (sound RAM write, odd steps only) |
| MRD    | 1     | Memory read trigger (sound RAM read, odd steps only) |
| EWT    | 1     | EFREG write trigger (1 = write to EFREG[EWA]) |
| EWA    | 4     | EFREG write address |
| ADRL   | 1     | Address register load (latch A_SEL output into ADRS_REG) |
| FRCL   | 1     | Fraction register load (latch F_SEL output into FRC_REG) |
| SHFT1  | 1     | Shifter control bit 1 |
| SHFT0  | 1     | Shifter control bit 0 |
| YRL    | 1     | Y register load (latch INPUTS[23:4] into Y_REG, available next step) |
| NEGB   | 1     | Negate B operand: 0=add, 1=subtract |
| ZERO   | 1     | Zero B operand: 1=set adder B input to 0 |
| BSEL   | 1     | B operand select: 0=TEMP data, 1=accumulator (SFT_REG) |
| NOFL   | 1     | No float: 1=skip floating-point conversion on memory access |
| CRA    | 6     | Coefficient read address (COEF index for Y_SEL) |
| MASA   | 5     | Memory address register select (MADRS index) |
| ADREB  | 1     | Address register enable: 0=gate ADRS_REG to 0, 1=add ADRS_REG |
| NXADR  | 1     | Next address: adds 1 to memory address (for interpolation) |

### Bit Layout (64-bit word, MSB first)

```
Bit  63    : (unused)
Bits 62-56 : TRA [6:0]
Bit  55    : TWT
Bits 54-48 : TWA [6:0]
Bit  47    : XSEL
Bits 46-45 : YSEL [1:0]
Bit  44    : (unused)
Bits 43-38 : IRA [5:0]
Bit  37    : IWT
Bits 36-32 : IWA [4:0]
Bit  31    : TABLE
Bit  30    : MWT
Bit  29    : MRD
Bit  28    : EWT
Bits 27-24 : EWA [3:0]
Bit  23    : ADRL
Bit  22    : FRCL
Bits 21-20 : SHFT [1:0]
Bit  19    : YRL
Bit  18    : NEGB
Bit  17    : ZERO
Bit  16    : BSEL
Bit  15    : NOFL
Bits 14-9  : CRA [5:0]
Bits 8-7   : (unused)
Bits 6-2   : MASA [4:0]
Bit  1     : ADREB
Bit  0     : NXADR
```

## YSEL - Y Operand Selection

| YSEL1 | YSEL0 | Input Selected |
|-------|-------|----------------|
| 0     | 0     | FRC_REG (13-bit fraction register) |
| 0     | 1     | COEF[CRA] (13-bit signed, stored in upper 13 bits of 16-bit register) |
| 1     | 0     | Y_REG[23:11] (upper 13 bits, sign-extended) |
| 1     | 1     | Y_REG[15:4] masked to 12 bits (0x0FFF), MSB forced to 0 |

Note on COEF storage: COEF registers are 16-bit in the register file but only
the upper 13 bits are significant. When reading COEF for the multiplier Y input,
use `COEF[CRA] >> 3` to get the 13-bit signed value. The lower 3 bits should be
written as 0 for compatibility.

## XSEL - X Operand Selection

| XSEL | Input Selected |
|------|----------------|
| 0    | TEMP data (24-bit, from TRA address) |
| 1    | INPUTS data (24-bit, from IRA latch) |

## IRA - Input Read Address Map

| Address    | Contents |
|------------|----------|
| 0x00-0x1F  | MEMS[0..31] (24-bit, loaded directly) |
| 0x20-0x2F  | MIXS[0..15] (20-bit, shifted left 4 to fill 24-bit INPUTS path) |
| 0x30-0x31  | EXTS[0..1] (16-bit external audio input, shifted left 8 to fill 24-bit INPUTS path) |
| 0x32-0x3F  | Reserved (reads zero on Saturn) |

When loading MIXS into INPUTS: `INPUTS = MIXS[IRA & 0xF] << 4`

## Shifter Modes

| SHFT1 | SHFT0 | Shift Amount | Overflow Protection |
|-------|-------|--------------|---------------------|
| 0     | 0     | x1 (no shift) | Saturate to 24-bit signed [-0x800000, 0x7FFFFF] |
| 0     | 1     | x2 (left shift 1) | Saturate to 24-bit signed [-0x800000, 0x7FFFFF] |
| 1     | 0     | x2 (left shift 1) | No saturation (mask to 24-bit: & 0xFFFFFF) |
| 1     | 1     | x1 (no shift) | No saturation (mask to 24-bit: & 0xFFFFFF) |

Saturation (SHFT1=0): if value > 0x7FFFFF, clamp to 0x7FFFFF; if value < -0x800000,
clamp to -0x800000. No saturation (SHFT1=1): mask result to lower 24 bits.

## Arithmetic Pipeline

Each step executes:
1. Complete any pending memory read from 2 steps ago (load result into MEMS)
2. Load INPUTS from MEMS, MIXS, or EXTS based on IRA
3. Read TEMP[(TRA + MDEC_CT) & 0x7F] for X (if XSEL=0) or B (if BSEL=0) operand
4. Select Y operand from FRC_REG, COEF, or Y_REG based on YSEL
5. Select X operand: TEMP or INPUTS based on XSEL
6. Multiply: `Product = (int64(X_signed_24) * int64(Y_signed_13)) >> 12` (25-bit result)
7. Select B: TEMP or SFT_REG based on BSEL; apply NEGB/ZERO
8. Accumulate: `SFT_REG = (Product + B) & 0x3FFFFFF` (26-bit masked)
9. Shift: apply SHFT mode to SFT_REG, output 24-bit result (ShifterOutput)
10. Optionally latch FRC_REG (FRCL), ADRS_REG (ADRL), Y_REG (YRL)
11. Optionally write TEMP[(TWA + MDEC_CT) & 0x7F] (TWT=1): write ShifterOutput
12. Optionally write MEMS[IWA] (IWT=1): write ShifterOutput
13. Optionally write EFREG[EWA] (EWT=1): `EFREG[EWA] = ShifterOutput >> 8`
14. Optionally start memory read/write to sound RAM (MRD/MWT, odd steps only)
15. Complete any pending memory write from 2 steps ago

### Multiply Precision

The multiply takes a 24-bit signed X and 13-bit signed Y operand. The full
product is 37 bits. Right-shifting by 12 produces a 25-bit signed result.
This is added to the 26-bit B operand. The sum is masked to 26 bits in
SFT_REG.

## F_SEL and A_SEL Output Selection

The SHFT1 and SHFT0 bits also control which bits of the shifter output are
latched into FRC_REG and ADRS_REG:

**F_SEL (into FRC_REG when FRCL=1):**
- SHFT0=0 or SHFT1=0: FRC_REG = shifter output bits [23:11] (upper 13 bits)
- SHFT0=1 and SHFT1=1 (interpolation mode): FRC_REG = shifter output bits [11:0] (lower 12 bits, zero-extended to 13)

**A_SEL (into ADRS_REG when ADRL=1):**
- SHFT0=0 or SHFT1=0: ADRS_REG[7:0] = INPUTS[23:16], ADRS_REG[11:8] =
  INPUTS[23] (sign extension of INPUTS treated as 24-bit signed). See
  "A_SEL INPUTS Source" below for the algorithmic form.
- SHFT0=1 and SHFT1=1 (interpolation mode): ADRS_REG = shifter output bits [23:12]

## Memory Address Calculation

```
address = MADRS[MASA]
if ADREB: address += ADRS_REG
if !TABLE: address += MDEC_CT
address += NXADR
```

When TABLE=0 (ring buffer mode):
- Address wraps within ring buffer: `address &= (RBL_size - 1)`
- RBL_size is set by the RBL register: 0=8K, 1=16K, 2=32K, 3=64K words
- RBP is in 4K-word units per SCSP User's Manual Sec 4.3 (RBP[19:13],
  "per 4K-word boundary"). Each RBP step advances the ring buffer base by
  4K words (8K bytes).
- Base word offset = RBP << 12 (in word units; equivalently, byte address
  = RBP << 13).
- Final RAM word address = `(address + (RBP << 12)) & 0x7FFFF`, where addr
  is a word index into the 256K-word sound RAM space.

When TABLE=1 (table mode):
- No MDEC_CT offset, no RBL wrap
- Full 64K word address space

## Float Conversion

When NOFL=0, sound RAM data uses a compressed 16-bit float format for
higher dynamic range in the delay buffer.

Format: 1 sign bit + 4-bit exponent + 11-bit mantissa

### int_to_dspfloat (24-bit signed -> 16-bit float, for writes)

```
sign = (value >> 23) & 1
magnitude = sign ? ~value : value  (absolute value via ones complement)
exponent = count leading zeros of magnitude in bits [22:0], clamped to 0-15
mantissa = (magnitude << exponent) >> 12, masked to 11 bits
result = (sign << 15) | (exponent << 11) | mantissa
```

### dspfloat_to_int (16-bit float -> 24-bit signed, for reads)

```
sign = (value >> 15) & 1
exponent = (value >> 11) & 0xF
mantissa = value & 0x7FF
magnitude = (mantissa << 12) >> exponent
result = sign ? ~magnitude : magnitude  (restore sign via ones complement)
```

### NOFL=1 (raw mode)

When NOFL=1, raw 16-bit data is used. On read, shift left 8 to fill 24-bit
path: `INPUTS = RAM_word << 8`. On write, shift right 8: `RAM_word = value >> 8`.

## Memory Access Timing

Memory operations (MRD, MWT) are only permitted on odd-numbered DSP steps
(steps 1, 3, 5, ...). Even steps overlap with PCM sound data reads in the
memory controller.

### Pipeline State Machine

Memory access is pipelined with a 2-step delay:
- Step N (odd): MRD=1 or MWT=1 requested, address calculated, stored as pending
- Step N+1: (pipeline in progress)
- Step N+2: Read result available in MEMS (via IWT), or write completes to RAM

Implementation uses pending flags:
- `ReadPending`: 0=none, 1=pending with float conversion, 2=pending raw (NOFL=1)
- `WritePending`: bool, with stored address and value
- At the start of each step, complete any pending read/write from 2 steps ago
  before processing the current instruction

When read and write are specified in the same step for INPUTS, TEMP, etc.,
the read is executed before the write.

## Ring Buffer Operation (RBL/RBP)

RBL and RBP are in the common control register at offset 0x402:
- RBL[1:0] (bits 9:8): Ring buffer length
  - 0: 8K words
  - 1: 16K words
  - 2: 32K words
  - 3: 64K words
- RBP[6:0] (bits 6:0): Ring buffer base pointer (1K word granularity)

The DSP access space in sound RAM is up to 64K words. The ring buffer area
and filter coefficient table area share this space. Ring buffer data uses
TABLE=0 addressing. Filter/coefficient table data uses TABLE=1 addressing.

MDEC_CT update occurs at the end of each DSP cycle (after all 128 steps):
1. If MDEC_CT == 0: reload with `0x2000 << RBL`
2. Then decrement: `MDEC_CT--`

The mask for ring buffer addressing is `(0x2000 << RBL) - 1`.

| RBL | Size | MDEC_CT reload | Mask |
|-----|------|---------------|------|
| 0   | 8K words | 0x2000 | 0x1FFF |
| 1   | 16K words | 0x4000 | 0x3FFF |
| 2   | 32K words | 0x8000 | 0x7FFF |
| 3   | 64K words | 0x10000 | 0xFFFF |

## MIXS Input From Slots

Slot output is routed to MIXS via the ISEL and IMXL registers at slot
offset 0x14:
- ISEL[3:0]: Selects which MIXS register (0-15) receives this slot's output
- IMXL[3:0]: Attenuation level for the slot's contribution to MIXS

MIXS accumulates the output from all slots that select the same MIXS index.
The DSP reads MIXS via IRA addresses 0x20-0x2F.

## EFREG Output to Mixer

The 16 EFREG outputs are returned to the output mixer. Each EFREG channel
has an associated EFSDL (level) and EFPAN (pan) setting in the DSP output
registers at 0x100EC0 (Saturn) which control how the effect return is mixed
into the final stereo output.

EFREG writes replace the current value (not accumulate):
`EFREG[EWA] = ShifterOutput >> 8` (24-bit to 16-bit)

Multiple DSP steps can write to the same EFREG index within one cycle.
The last write wins.

## Implementation Notes

### NOP Detection

A microprogram step where all 4 words are zero is a NOP. Implementations
can scan backwards from step 127 to find the last non-zero instruction and
only execute up to that point as a performance optimization.

### B Operand Sign Extension

When BSEL=0 (TEMP data), the 24-bit TEMP value must be sign-extended to
26 bits before use in the accumulator:
`B = (TEMP_24 ^ 0x800000) - 0x800000` or equivalent sign extension.

When BSEL=1 (SFT_REG), the value is already 26-bit.

### A_SEL INPUTS Source

When ADRL=1 and not in interpolation mode, A_SEL loads from INPUTS:
`ADRS_REG = (INPUTS >> 16) & 0xFFF`

The INPUTS value should be treated as 24-bit signed (sign-extended to 32-bit)
before the right shift to preserve the sign in the upper bits.
