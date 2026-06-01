# erings

A Sega Saturn emulator.

erings is a work in progress. Pretty much all of the console's internal
hardware components are implemented at this point. Many games run with a number
of them at full speed. Some emulator level convenience
features are not yet supported. Most games play fine but some are not full
speed and there are still plenty bugs to find in the emulation.

The goal is to have an emulator that models hardware to the point game specific
hacks and heuristics are not needed. A lot of components are modeled but
this philosophy has an impact on performance. It will struggle on lower spec systems.

The focus of erings is to play the majority of officially licensed games while
focusing on the user experience. This is not intended to be a perfect emulator
of every aspect of the Saturn. Some features and components are purposely not
supported and are excluded on purpose.

This is a gaming first emulator and not intended as game development tool.
You won't find an integrated debugger you can step through. You won't find
any kind of editor. The only development tools are intended helping debug
the emulator itself.

What you will find is a clean UI allowing you organize and play games.

## Requirements

- Games must be supplied as CHD disc images

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

To launch a game directly without going through the UI, pass a disc
image on the command line. A BIOS is optional; if omitted, the built-in
HLE BIOS boots the game:

```
./build/erings -disc /path/to/game.chd
```

To use a real BIOS instead, add `-bios`:

```
./build/erings -bios /path/to/bios.bin -disc /path/to/game.chd
```

You can build and run using: `go run ./cmd/desktop`

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

## Current State

- Runs through the eblitui desktop UI
- NTSC and PAL region
- A 4MB extended RAM cartridge is always present
- Single player digital controller only (the core supports a second
  controller, but the UI does not)
- Internal backup RAM (console saves) persists between sessions

BIOS region patching is supported. Meaning you can use a US BIOS to play
Japanese games or a Japanese BIOS to play US games.

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
- Rewind (not currently working, requires save states)
- RetroAchievements (in ui but not currently working)

## Game Compatibility

Compatibility is not formally tracked yet. Some games play, some
don't. Some play well, some don't. 
