# replay

The replay package defines the on-disk format for recorded play sessions
and provides a recorder (capture) and player (playback) for it. A replay
captures a session's per-frame controller input and the frames at which a
screenshot should be taken. The debug tool records and plays sessions.

## Purpose

- Capture controller input once, then replay it repeatedly to check for
  changes across builds without reproducing the inputs by hand.
- Mark frames during recording where a screenshot is wanted.

## File format

A single JSON file.

```json
{
  "version": 1,
  "discId": "T-1234G",
  "frames": 12345,
  "input": [
    { "f": 0,   "p1": 0,    "p2": 0 },
    { "f": 120, "p1": 4096, "p2": 0 }
  ],
  "screenshots": [600, 1200, 3000]
}
```

### Fields

- `version`: format version integer (currently 1). Lets new fields be
  added later without breaking older readers.
- `discId`: the 10-character product number from the disc IP header
  (offset $20 in the IP user data). Identity check only - a reader may
  warn or refuse if the loaded disc does not match. The disc itself is
  supplied separately, not embedded.
- `frames`: total number of emulated frames recorded. Playback feeds
  recorded input for this many frames, then stops contributing.
- `input`: input change-events. Each entry is `{ f, p1, p2 }` where `f`
  is the emulated-frame index and `p1`/`p2` are the player 1 and player 2
  button bitmasks. An event is present only when the state changes, plus
  always at frame 0 to establish the baseline. The state holds until the
  next event, so a held input is a single entry.
- `screenshots`: emulated-frame indices at which a screenshot should be
  taken. Bare integers; no labels.

### Button bitmask

`p1` and `p2` use one bit per Saturn button:

| Bit | Button | Bit | Button | Bit | Button |
|-----|--------|-----|--------|-----|--------|
| 0   | Up     | 4   | A      | 9   | Z      |
| 1   | Down   | 5   | B      | 10  | L      |
| 2   | Left   | 6   | C      | 11  | R      |
| 3   | Right  | 7   | X      | 12  | Start  |
|     |        | 8   | Y      |     |        |

## Frame indexing

All frame indices are the emulated-frame counter: the number of `RunFrame`
calls. Recording counts only frames that actually run, so a paused frame
does not advance the index and the timeline is a pure `RunFrame` count.

A screenshot index `c` means the framebuffer after `RunFrame` produced
frame `c` (after `c+1` frames have run).

## Boot mode caveat

Input is keyed on the emulated-frame counter, and HLE versus real-BIOS
boot take different numbers of frames to reach the same game state (the
real BIOS boot animation runs for several seconds that the HLE path
skips). A file recorded under one boot mode has its inputs land at
different game moments if replayed under the other. The format supports
both modes, but a given recording is practically tied to the boot mode it
was made under. This is inherent to frame-keyed input. Boot mode (HLE or
real BIOS) and fast-boot are selected when a session is run; the file
does not record them.

## API

### Recording

```go
r := replay.NewRecorder()
// once per emulated frame, with the input applied to that frame:
r.RecordFrame(p1, p2)
// to mark the most-recently-completed frame as a screenshot point:
frame := r.MarkScreenshot()
// on shutdown:
err := r.Write(path, discID)
```

`RecordFrame` appends an event only when the state changes (and always on
the first frame), then advances the frame counter. `MarkScreenshot`
records the last completed frame and returns its index.

### Playback

```go
f, err := replay.Load(path)
p := replay.NewPlayer(f)
// once per emulated frame, in order from 0:
p1, p2, active := p.Next()
if p.ShouldScreenshot() {
    // take a screenshot
}
```

`Next` returns the recorded input for the next frame and advances the
player. `active` is false once `frames` is reached, at which point `p1`
and `p2` are zero and the caller should use live input only.
`ShouldScreenshot` reports whether the frame just produced by `Next` is a
marked screenshot point. The recorded input can be combined with live
input by bitwise OR so a user can press buttons during playback.
