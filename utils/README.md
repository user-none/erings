# utils

Command-line tools for development and for working with the emulator's
data. Each tool is a self-contained `main` package in its own subdirectory.
Run them with `go run ./utils/<tool>` or build with `go build ./utils/<tool>`.

`disasm`, `m68kdisasm`, `extract_bioslibs`, and `statedump` use only the
standard library (plus the in-tree `core/sh2` disassembler for `disasm`,
the `go-chip-m68k` disassembler for `m68kdisasm`, and the S2 decompressor
for `statedump`). `dev_runner` is the development game runner and links
the emulator core and its UI dependencies, so it needs a display to
run. `debugger` is the GUI client for `dev_runner`'s debug server; it
needs a display but does not link the emulator core. `capture` links
the emulator core but is fully headless (no display or audio), so it
runs anywhere.

## dev_runner

A game runner that should be used for development. It is decoupled from
the UI and outputs additional information to the console, such as the
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
- C (left shoulder)
- N (right shoulder)
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
- 8 (dump the current save state, exploded per field, to a
  `dump-YYYYMMDD-HHMMSS-mmm` directory)

### Debug server

`-debug-server` starts an interactive debug server on
`127.0.0.1:5000` (off by default; `-debug-server-port` selects a
different port). It speaks a bare line protocol, so both `telnet` and
`nc` work as clients, and it is scriptable
(`echo "list" | nc localhost 5000`). The `debugger` tool is a GUI
client for the same server. One client is served at a time; a second
connection waits until the current one disconnects. Server state
(searches, watches, snapshots) lives in the tool and survives
reconnects.

Commands run between frames on the emulation loop. Addresses are Saturn
hardware addresses (hex `0x` prefix or decimal); SH-2 partition
spellings and mirror images resolve to the same canonical byte. Work
RAM Low and High are the accessible regions.

- `pause` / `resume` / `frame [n]` - execution control; `frame` runs n
  frames while paused for frame-precise stepping.
- `state` - one-line report of the pause flag, frame count, search
  width, and surviving candidate count.
- `regions` / `read <addr> [len]` - region list and hex dump.
- `watch [<addr> [w]]` / `unwatch <addr>|all` - report value changes
  each frame (width 8/16/32) to stderr and the connected client.
- `break [<addr> <op> [v] [w]]` / `unbreak <addr>|all` - pause when a
  value condition first becomes true (same operators as `filter`),
  e.g. `break 0x0605C973 eq 0` to stop on the frame health hits zero.
- `baseline [region...]` / `filter <op> [value]` / `width [8|16|32]` /
  `list [n [offset]]` / `reset` - cheat-search style memory search:
  baseline the regions, act in-game, then filter with `dec`, `inc`,
  `same`, `diff`, or `eq`/`ne`/`lt`/`gt <value>` to narrow candidates.
  Every filter re-baselines to the values it just read. `list` pages
  through the survivors with the optional offset.
- `snapshot [name]` / `snapshots` / `restore [name]` - in-memory
  machine save states for repeating an event from a fixed point.
- `prompt on|off` - disable the interactive `> ` prompt for scripted
  capture.
- `mode text|json` - response format. JSON mode emits one JSON object
  per line (a response envelope per command, plus pushed watch/break
  events) and is what the `debugger` tool uses; text mode is the
  default and resets on every new connection.
- `help` - full command list.

A typical search: `baseline`, take a hit in-game, `filter dec`, play
safely, `filter same`, take another hit, `filter dec`, then `list` and
`watch` the survivors to confirm.

### sh2.TraceFunc

The SH-2 supports a hook that allows tracing all SH-2 execution. This is
used by the PC histogram feature of `utils/dev_runner`. To use this
function for other purposes with that tool, the currently hooked-in
function needs to be changed or replaced for one-off testing. Changes to
this histogram capture should not be committed.

## debugger

A GUI client for the `dev_runner` debug server, in its own window so
keyboard focus never fights with the game. It is a viewer over the
server's command set: it holds no state of its own, rebuilds every
panel from the server's answers on connect, and clears them on
disconnect. Server state survives client reconnects, so closing and
reopening either side is routine.

Start the runner with the debug server enabled, then the debugger:

```
go run ./utils/dev_runner -debug-server -disc game.chd
go run ./utils/debugger
```

The connect panel is prefilled with `127.0.0.1:5000` (`-connect`
changes the prefill); Connect attaches, and a dropped connection
returns to the panel with the reason shown.

Panels:

- Top bar: pause / resume / step-n controls with the frame counter and
  pause state.
- Memory: a 16-row hex view with region buttons, a goto box (mirror
  and partition spellings fold like the server), mouse-wheel movement
  through the region, and a decaying highlight on bytes that changed
  between refreshes.
- Watches and Breaks: live lists with per-row remove, a quick-add row
  under each (the break row cycles the condition operator), a red
  flash when an entry fires, and click-to-jump: clicking an entry
  scrolls the hex view to its address.
- Search: the baseline / filter / width / rebase / reset workflow as
  buttons, with a paged candidate list (50 per page); clicking a
  candidate jumps the hex view to it.
- Event log: pushed watch/break traffic and command responses in their
  own scrollback, which takes all extra window height and follows the
  bottom until scrolled up.
- Command line: sends any server command verbatim, so the GUI never
  lags the server's command set.

Copy and paste: all text inputs take Ctrl/Cmd+A/C/V/X. The hex view
has hex-editor drag selection (Ctrl/Cmd+C copies the dump format,
Shift+Ctrl/Cmd+C a raw hex string) and the log has terminal-style drag
selection copied as plain text. One selection exists at a time.

## capture

A headless runner that replays a recorded session (see `internal/replay`)
against a disc with no display, as fast as possible, and writes framebuffer
screenshots at the frames the replay marked. It is for regression
comparison: run the same replay across builds and diff the images.

Usage:

```
capture [flags] <disc> <replay>
```

Both positionals are required: the disc image and the recorded replay file.

Flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `-bios` | (none) | Path to the Saturn BIOS ROM. If omitted, the HLE BIOS boots the disc. |
| `-fast-boot` | false | Skip the real BIOS boot animation (real BIOS only; no effect with the HLE BIOS). |
| `-out` | `capture_output` | Output root directory, created if missing. |
| `-load-state` | (none) | Save state to load at startup; the replay must have been recorded from this state with the same disc and BIOS. |

Example:

```
go run ./utils/capture -bios BIOS_USA.bin game.cue session.replay
```

Screenshots are written under `<out>/screenshots/` as `id_ts_framenum.png`,
where `id` is the disc's product number (`unknown` if it can't be read),
`ts` is the Unix timestamp when the run started, and `framenum` is the frame
the shot was taken on. Images are lossless PNG so pixel-exact differences
survive for comparison.

A capture log is written under `<out>/logs/` as `id_ts.log` using the same
id and timestamp. It records the run milestones (state load, replay start,
completion, any watchdog abort) and a frame stats line every region-fps
frames (one emulated second per window) in the same format `dev_runner`
prints:

```
frame 120  fps 412.50  game_fps 30.00 | fmin 1.200, fmax 15.750, favg 4.125 ms
```

Because the run is unpaced, `fps` is host throughput over the window's wall
time, while `game_fps` counts VDP1 framebuffer swaps against the window's
emulated time - the game's internal rate, independent of host speed and
directly comparable to `dev_runner` running at full speed. The frame times
are min/max/average `RunFrame` compute cost over the window, which
`dev_runner` also measures excluding its pacing wait, so they compare
directly as well. A partial final window is flushed when the replay ends so
short runs still produce stats.

The run is unattended, so a watchdog aborts it (exit code 2) if emulation
throughput collapses. Because the loop has no pacing, a healthy run produces
frames far above realtime. If fewer than 10 frames complete in a one-second
window the run is failing: no frame completing for two seconds is reported as
frozen (with a goroutine stack dump), and a sustained sub-floor rate is
reported as slow. A clean run exits 0 after the replay ends.

## statedump

Explodes a save state file into a directory of per-field binaries without
running the emulator: a `header.txt` with the decoded state header
(version, game ID, BIOS hash, CRC) and one `<field>.bin` per chunk field
under a subdirectory named after the chunk tag (`CD_MPEG/latch.slots.bin`,
`SH2M_CORE/pc.bin`, and so on). It exists to inspect states saved by any
frontend after the fact; the `dev_runner` in-session dump key
produces the same layout (plus the state file itself) through the shared
`internal/statedump` package.

Usage:

```
statedump <state-file> <output-dir>
```

Both arguments are required; the output directory is created if missing.

Example:

```
go run ./utils/statedump state-0.state exploded
```

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

## m68kdisasm

An MC68000 disassembler built on the `go-chip-m68k` package's
`Disassemble`. It decodes a raw binary in Motorola syntax and prints one
formatted line per instruction: address, raw instruction words, text.
Instructions are variable length (2-10 bytes), so `-count` counts
instructions and the sweep advances by each instruction's decoded length.
Opcodes the execution core treats as illegal print as `DC.W $xxxx`, branch
targets and PC-relative operands are resolved to absolute addresses, and
an instruction truncated by the end of the file is rendered as `DC.W`
lines, one per remaining word.

Flags (same interface as `disasm`):

| Flag | Default | Meaning |
|------|---------|---------|
| `-file` | (required) | Path to the binary to disassemble |
| `-base` | `0` | Hex base address the file is mapped at; the base plus the file size must stay within the 68000's 24-bit address space |
| `-addr` | (required unless `-all`) | Hex start address (`$`, `0x`, or bare hex) |
| `-count` | `20` | Number of instructions to disassemble |
| `-all` | false | Disassemble from `-addr` to end of file |

Examples:

```
# 30 instructions at the sound driver reset entry
go run ./utils/m68kdisasm -file bios_sounddrv.bin -addr 0x1000 -count 30

# whole file from offset 0
go run ./utils/m68kdisasm -file body.bin -addr 0 -all
```

Note: the input length must be even (instructions are word-aligned).

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
| `bios_sounddrv.bin` | `$01D000` | BIOS sound driver package ("BOOT ROM(S) V2" ver1.11): MC68EC000 driver program, area map, sound data banks, DSP effect banks; uploaded to sound RAM by sub_15D4 |
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
