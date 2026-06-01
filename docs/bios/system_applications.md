# System Applications (SEGA PLAYER)

The BIOS contains a built-in multimedia application suite - the audio CD
player, CD+G player, Video-CD/MPEG front end, and the System Settings /
Memory Manager screens - as a self-contained program separate from the boot
library. This program identifies itself by the ASCII string `SEGA PLAYER` at
ROM `$001A04` and is referred to here as the SEGA PLAYER shell.

The boot library at `$007000` (boot_library.md) is a separate program: the
Saturn-logo / disc-check animation. The SEGA PLAYER applications live in their
own ROM region, are loaded by a different dispatch path, and run at a different
Work-RAM-H address (`$06020000`, versus `$06010000` for the boot library).

## ROM Layout

The shell and its application modules occupy `$040000`-`$07D200`:

```
$040000  raw shell bootstrap (4 KB) - register init + setup loop, then
         JMP $06028000 (the resident shell body)
$040400  module directory (id -> offset table, see below)
$040438  app modules begin: a packed sequence of "MB" records, each a
         0x10-byte header (magic "MB", id, compressed size at +8)
         followed by a sub_1F04-compressed body at +0x10
```

The bootstrap at `$040000` is raw (uncompressed) SH-2 code. The application
bodies are compressed with the BIOS sub_1F04 LZSS format (see
bios_decompression.md).

## Module Directory ($040400)

A table of `(id, offset)` 32-bit pairs, terminated by `$FFFFFFFF`. `offset`
is relative to the directory base `$040400`; the record address is
`$040400 + offset`, and the compressed body starts 0x10 past that. The id is
the stable selector the shell uses to request a module.

| dir id | offset    | record addr | body addr | module           | decompressed |
|--------|-----------|-------------|-----------|------------------|--------------|
| 0      | `$000038` | `$040438`   | `$040448` | Video-CD / MPEG + disc security | 69,842 B |
| 1      | `$00AD24` | `$04B124`   | `$04B134` | CD+G player      | 90,559 B |
| 13     | `$018B54` | `$058F54`   | `$058F64` | shared graphics resources | 143,744 B |
| 14     | `$0228B0` | `$062CB0`   | `$062CC0` | shared data resources | 92,196 B |
| 9      | `$028068` | `$068468`   | `$068478` | player UI / common code | 182,334 B |
| 8      | `$034490` | `$074890`   | `$0748A0` | System Settings / Memory Manager | 55,911 B |

Each body carries a `COPYRIGHT(C) SEGA ENTERPRISES,LTD. 1994` header. Module
identities for ids 0, 1, and 8 are confirmed by embedded UI strings
(`SEGA SATURN SYS`/`SECURITY`/`VIDEO`/`MPEG`; `CD-G CHANNEL` in five
languages; `Memory Manager`/`System Settings`/`Cartridge Memory`). Ids 9, 13,
14 carry no UI text - 13 and 14 are graphics/data resources, 9 is code plus a
large embedded graphics blob - and their roles are inferred from content and
load grouping.

## How the Shell Is Loaded

### 1. Boot dispatch

The boot dispatcher at `$001280`-`$001306` reads a 4-character type tag from
Work-RAM-H `$0600024C` and routes on it:

| tag at $0600024C | value      | action |
|------------------|------------|--------|
| `HCGG`           | `$48434747`| load SEGA PLAYER shell (CD+G disc) |
| `HCDM`           | `$4843444D`| load SEGA PLAYER shell |
| (status)         | `$22`      | load SEGA PLAYER shell |
| (CD-game)        | -          | `BSR $0015D4` -> decompress CD filesystem ($01D000) |
| (otherwise)      | -          | `BSR $00173C` -> decompress boot library ($007000) |

The shell branch goes to `$001320`, which jumps through the handler pointer
held in the pool at `$001344` (= `$001A18`). The `HCDM` tag is the magic that
`SYS_EXECDMP` loads into R7 (`$4843444D`), so invoking `SYS_EXECDMP`
reinitializes the system and routes here; see system_services.md.

### 2. Shell copy

Handler `$001A18` block-copies the raw shell into Work-RAM-H and enters it.
Its parameters come from the pool at `$001A30`:

```
count = $400 longwords (4 KB)
src   = $00040000
dst   = $06020000
        copy, then JMP $06020000
```

The copied bootstrap at `$06020000` loads SR/GBR/VBR/SP from its register-init
block, runs a setup loop (`$040074`), and jumps to the resident shell body at
`$06028000` (pool `$040060`).

### 3. Module load

The setup code selects a module set by mode and loads each through
`$0400CC` -> `$040116`:

| mode | modules loaded (by dir id) |
|------|----------------------------|
| 0    | 0 (Video-CD) + 8 (Settings) |
| 1    | 13 (graphics) + 14 (data) + 1 (CD+G) + 9 (player UI) |
| 8    | 8 (Settings) |

`$040116` is the load-by-id routine: it walks the `$040400` directory for the
requested id, computes the module address as `$040400 + entry.offset`, and
calls sub_1F04 to decompress the body into a Work-RAM-H buffer. The shell then
runs the decompressed module.

## Relationship to Other BIOS Programs

The three compressed programs the BIOS can resident at boot are mutually
exclusive paths off the same dispatcher:

- Boot library (`$007000` -> `$06010000`): logo / disc-check animation. The
  default no-game path. See boot_library.md.
- CD filesystem (`$01D000` -> `$06010000`): CD-game boot / file access. See
  rom_layout.md.
- SEGA PLAYER shell (`$040000` -> `$06020000`): the multimedia and
  settings applications, loaded on the `HCDM` / `HCGG` / `$22` tag path
  (this document).

All application bodies are extractable with utils/extract_bioslibs
(`app_videocd.bin`, `app_cdg.bin`, `app_graphics.bin`, `app_data.bin`,
`app_playerui.bin`, `app_settings.bin`).
