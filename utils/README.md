# utils

Command-line tools for development and for working with the emulator's
data. Each tool is a self-contained `main` package in its own subdirectory.
Run them with `go run ./utils/<tool>` or build with `go build ./utils/<tool>`.

`disasm` and `extract_bioslibs` use only the standard library (plus the
in-tree `core/sh2` disassembler for `disasm`). `debug` is the development
launcher and links the emulator core and its UI dependencies, so it needs a
display to run.

## debug

A command line launcher that should be used for development. It is decoupled
from the UI and outputs additional information to the console, such as the
frame rate and the gameplay frame rate.

It also includes a stall watchdog that detects when the emulator has entered
a stalled state (such as an infinite loop). It dumps every goroutine's stack
to stderr and reports exactly what caused the stall.

The `-cpuprofile` flag allows profiling the application using the standard
Go profiling system.

Movement keys are:

- W (up)
- S (down)
- A (left)
- D (right)
- N (left shoulder)
- M (right shoulder)
- J (A)
- K (B)
- L (C)
- U (X)
- I (Y)
- O (Z)

Additional keys:

- Enter (start)
- 0 (pause emulation)
- 9 (dump top 20 PC histogram)
- 8 (dump current memory to `dump-YYYYMMDD-HHMMSS-mmm` directory)

### sh2.TraceFunc

The SH-2 supports a hook that allows tracing all SH-2 execution. This is
used by the PC histogram feature of the `utils/debug` launcher. To use this
function for other purposes with that launcher, the currently hooked-in
function needs to be changed or replaced for one-off testing. Changes to
this histogram capture should not be committed.

## disasm

An SH-1 / SH-2 disassembler. It decodes a raw binary as SH-2 instructions
and prints one formatted line per instruction. Constant-pool words behind
PC-relative loads are shown as `.data`; register-indirect branch targets
(BSRF / BRAF / JSR / JMP) are resolved against the most recent PC-relative
load of that register and annotated with `; -> $addr`; delay-slot
instructions are marked. Output is streamed, so memory use is independent
of input size.

Flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `-file` | (required) | Path to the binary to disassemble |
| `-base` | `0` | Hex base address the file is mapped at |
| `-addr` | (required unless `-all`) | Hex start address (`$`, `0x`, or bare hex) |
| `-count` | `20` | Number of instructions to disassemble |
| `-all` | false | Disassemble from `-addr` to end of file |

Examples:

```
# 20 instructions at BIOS offset $1A18
go run ./utils/disasm -file BIOS_USA.bin -base 0 -addr 0x1A18

# whole file from offset 0
go run ./utils/disasm -file body.bin -addr 0 -all
```

Note: the input length must be even (instructions are 16-bit).

## extract_bioslibs

Extracts and decompresses the compressed bodies stored inside the US Sega
Saturn BIOS. The input is validated as the US BIOS by exact size (512 KB)
and SHA-256 before anything is written; any other image is rejected. Each
body is expanded with the BIOS LZSS format and written as a `.bin` file.

Output files (10 bodies):

| File | ROM offset | Contents |
|------|-----------|----------|
| `bios_fonts.bin` | `$005240` | Bitmap font / glyph bitmaps |
| `bootlib.bin` | `$007000` | Boot library: Saturn logo / disc-check animation |
| `bios_cdfs.bin` | `$01D000` | BIOS-internal CD filesystem driver (SH-2 host side; not the SH-1 CD-block firmware) |
| `app_videocd.bin` | `$040448` | SEGA PLAYER app: Video-CD / MPEG + disc security |
| `app_cdg.bin` | `$04B134` | SEGA PLAYER app: CD+G player |
| `app_graphics.bin` | `$058F64` | SEGA PLAYER shared graphics resources |
| `app_data.bin` | `$062CC0` | SEGA PLAYER shared data resources |
| `app_playerui.bin` | `$068478` | SEGA PLAYER player UI / common code |
| `app_settings.bin` | `$0748A0` | SEGA PLAYER app: System Settings / Memory Manager |
| `per_driver.bin` | `$07D660` | PER + BUP peripheral driver |

The `app_*` bodies belong to the SEGA PLAYER multimedia shell; see
`docs/bios/system_applications.md` for how the BIOS loads them, and
`docs/bios/bios_decompression.md` for the format (`sub_1F04`).

Usage:

```
extract_bioslibs [-out DIR] <bios.bin>
```

`-out` selects the output directory (default: current directory; created
if missing).

Example:

```
go run ./utils/extract_bioslibs -out extracted BIOS_USA.bin
```
