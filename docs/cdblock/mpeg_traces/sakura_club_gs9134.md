# SAKURA CLUB (GS-9134) MPEG Command Trace

Multi-clip movie scene, four clips played back to back. Captured from
the scene entry through the return to the game. The `[f XXXX]` prefix
is the decoder's VSYNC operation-interval counter (one tick per field,
free-running from each $93), so gaps between stamps read as elapsed
fields. Repeating poll patterns are elided with `...` comments.

## Delivery Architecture

This title is a host-demux user of the decoder: the disc mux is split
by CD block subheader filters and the video elementary stream is
rebuilt by the host and delivered with Put Sector Data ($64).

- Play ranges: clip 1 plays mux FAD $1C787 x $1A1 sectors; a second
  play of FAD $1C928 x $878 sectors covers clips 2-4 as one continuous
  range.
- Filters: audio channel 1 -> partition 0 (host-read; the audio is
  PCM-decoded on the sound CPU, not by the card), video channel 1 ->
  partition 1, video channel 2 -> partition 2 (both host-read).
- The host strips the pack layer, rebuilds the raw video elementary
  stream, and $64-puts it into partition 3. Playback setup waits until
  partition 3 holds at least 50 sectors.
- The video decoder is connected to partition 3 ($9A video record:
  connection mode $27, partition 3); the audio decoder is left
  disconnected ($FF).
- Each clip ends via the in-stream sequence end code ($B7), raising
  the sequence-end interrupt cause ($000400); the title then re-inits
  ($93) for the next clip.
- The long clips run the 200-sector buffer at capacity for most of
  their length (delivery outpaces realtime consumption), exercising
  the buffer-full flow control and the $64 WAIT-and-retry path
  (saturn_cdblock_commands.md).

## Clip 1

Scene entry and decoder bring-up:

```
[MPEG] [f 0000] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] [f 0003] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] [f 0003] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0100 CR4=0000
[MPEG] [f 0003] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 0003] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 0003] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
...
$90, $9B (audio), $9B (video) repeat once per field from [f 0045]
while the host reads the mux, rebuilds the video stream, and fills
partition 3 to the 50-sector gate
...
```

Playback start once the gate is met:

```
[MPEG] [f 007B] $91 MpegGetInterrupt     CR1=9100 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 007B] $94 MpegSetMode          CR1=94FF CR2=0100 CR3=FF00 CR4=0000
[MPEG] [f 007B] $96 MpegSetDecodeMethod  CR1=9604 CR2=0001 CR3=0000 CR4=0001
[MPEG] [f 007B] $95 MpegPlay             CR1=9501 CR2=FF00 CR3=0000 CR4=00FF
[MPEG] [f 007B] $9D MpegSetStream        CR1=9D00 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 007B] $9A MpegSetConnection    CR1=9AFF CR2=00FF CR3=0027 CR4=0003
[MPEG] [f 007C] $91 MpegGetInterrupt     CR1=9100 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 007C] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0000 CR4=0000
[MPEG] [f 007C] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=8011 CR4=8011
[MPEG] [f 007C] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=0027 CR4=0028
[MPEG] [f 007C] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=00F0 CR4=00A0
[MPEG] [f 007C] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 007C] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 007C] $A4 MpegSetVideoEffect   CR1=A400 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 007C] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
```

Notes: $95 CR1 low byte 01 selects independent playback (no A/V start
sync; the audio goes through the sound CPU instead of the card). The
$A1 window is frame-buffer position 0/0, ratio $8011/$8011 (1:1),
display position X=$27 Y=$28, display size $F0 x $A0 (240x160, the
stream's coded size at 29.97 fps). The FMV screen mode is 320x224.

Steady-state playback polling:

```
[MPEG] [f 0083] $97 MpegOutDecodingSync  CR1=9700 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 0083] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 0083] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] [f 0083] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
...
the $90 + $9B pair runs about twice per field for the whole clip;
$97 Out Decoding Sync is issued every other field
...
```

Clip end. The sequence end code in the stream raises the sequence-end
cause ($000400, in the $92 mask $0500 together with picture start
$000100); the title answers with one raw LSI write and re-inits for
the next clip:

```
[MPEG] [f 01BE] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
...
$90/$9B polling continues for 14 fields
...
[MPEG] [f 01CC] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
```

## Clips 2-4

Each clip repeats the identical sequence: $93 / $92 / $94, the
per-field $90 + $9B x2 wait loop while partition 3 refills, the $91 /
$94 / $96 / $95 / $9D / $9A play burst, the $91 / $A1 x4 / $A2-$A4 /
$A0 window burst (same values as clip 1), steady-state polling with
$97, then $AF and the next $93. Only the timings differ (field stamps
relative to each clip's own $93):

| Clip | Play burst | $AF (end) | Playback length |
|------|------------|-----------|-----------------|
| 1    | [f 007B]   | [f 01BE]  | ~5.4 s          |
| 2    | [f 00A5]   | [f 0789]  | ~29.4 s         |
| 3    | [f 0086]   | [f 0873]  | ~33.9 s         |
| 4    | [f 00FC]   | [f 04FE]  | ~17.1 s         |

Clip 2's longer pre-play wait ([f 00A5] vs [f 007B]) is the second
mux play starting: the drive seeks to FAD $1C928 before the demux
pipeline can refill partition 3.

After clip 4's $AF the title issues no further MPEG commands; the
scene exits with the decoder left initialized and the display still
enabled.

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
