# erings

A Sega Saturn emulator.

erings is a work in progress. Pretty much all of the console's major internal
hardware components are implemented to some extent. Many games run and a number
of them at full speed. However, there are still plenty bugs to find.

The focus of erings is to play the majority of officially licensed games while
focusing on the user experience. This is not intended to be a perfect emulator
of every aspect of the Saturn. Some features and components are purposely not
supported and never will be. It might be better to think of this as
a Saturn game player than a digital Saturn console.

This is a gaming first emulator and not intended as game development tool.
You won't find an integrated debugger you can step through. You won't find
any kind of editor. The only development tools are intended for helping debug
the emulator itself.

What you will find is a clean UI allowing you organize and play games. With
a controller first navigation system. Make erings full screen, sit back and
have fun playing some amazing classic games.

## Disc formats

Several disc image formats are supported

- chd
- cue (tracks must have .bin extension)

## BIOS

A Saturn BIOS image (USA or Japan) is optional. Without one, a built-in
HLE (high-level emulation) BIOS boots the game. A real BIOS is not
included with the emulator and needs to be user sourced.

## Building

Build the desktop binary with make: `make`

On macOS, a `.app` bundle can be produced with: `make macos`

## Running

With no arguments, erings opens the UI for selecting a disc (and,
optionally, a BIOS):

```
./build/erings
```

### Direct Run

You can launch a game directly without opening the full UI. However,
features like shaders, achievements and pretty much everything else won't
be available.

To launch a game directly without going through the UI, pass a disc
image on the command line. A BIOS is optional; if omitted, the built-in
HLE BIOS boots the game:

```
./build/erings -disc /path/to/game.chd
./build/erings -disc /path/to/game.cue
```

To use a real BIOS instead, add `-bios`:

```
./build/erings -bios /path/to/bios.bin -disc /path/to/game.chd
./build/erings -bios /path/to/bios.bin -disc /path/to/game.cue
```

## UI Controls

Game controls (D-pad, buttons) are configured in the UI's input settings.

| Key | Action |
|-----|--------|
| Escape | Open / close the pause menu (also Select on a gamepad) |
| Tab | Toggle the achievement overlay |
| R (hold) | Rewind while held |
| F1 | Save state to the current slot |
| F2 | Next save state slot |
| Shift + F2 | Previous save state slot |
| F3 | Load state from the current slot |
| F4 | Cycle turbo speed (Off, 2x, 3x) |
| F11 | Toggle fullscreen |
| F12 | Screenshot |

**Note** Rewind, and save states need save state support which is a future feature

## Current State

- Runs through the eblitui desktop UI
- RetroAchievements supported
- NTSC and PAL region
- A 4MB extended RAM cartridge is always present
- Single player digital controller only (the core supports a second
  controller, but the UI does not)
- Internal backup RAM (console saves) persists between sessions

BIOS region patching is supported. Meaning you can use a US BIOS to play
Japanese games or a Japanese BIOS to play US games. Or just use the HLE BIOS
and not worry about it.

## Won't be Supported

- ROM cartridges
- HDTV mode (TVM=100)
- 31 kHz exclusive monitor mode
- Peripherals (light gun, mouse, racing wheel, keyboard, etc.)

## Planned

- Disc swapping for multi-disc games
  - Right now you have to rely on the game saving, exiting the game
    and selecting the next game from the ui
- Save states

## Game Compatibility

Compatibility is not formally tracked yet. Some games play, some
don't. Some play well, some don't.
