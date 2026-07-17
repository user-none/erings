# MOON CRADLE (T-9109G) MPEG Command Trace

Program-stream user of the decoder: the disc mux is delivered straight
into CD buffer partitions and both decoder layers are connected to
them ($9A: video partition 0 record mode $26, audio partition 1 record
mode $06). Playback is A/V-synchronized ($95 CR1 low byte 00) with
VSYNC-paced decode ($94 decode timing 0), so $97 Out Decoding Sync is
never issued. The title never reads the stream records ($9E).

Three movies were traced - two opening movies and one in-game movie
that occupies only part of the screen - and all three share a single
$93 MpegInit: the subsystem is initialized once and stays up for the
whole session. The first movie was additionally traced with an early
exit (skipped). The session runs on a 320x240 screen, and all three
movies use display windows smaller than it (the opening window lands
centered).
Repeating poll patterns are elided with `...` comments, and the tracer
elides identical re-sends of $A1-$A4, so window bursts appear in the
trace only when their values change.

## Session Bring-Up

Issued once, at boot:

```
[MPEG] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
```

The bring-up $94 sets scan mode 0 (NTSC non-interlace). The MPEG
command stream then goes quiet for about five seconds until the first
play burst.

## Play Burst

Identical for all three movies:

```
[MPEG] $94 MpegSetMode          CR1=94FF CR2=0000 CR3=FF00 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $96 MpegSetDecodeMethod  CR1=9604 CR2=0001 CR3=0000 CR4=0001
[MPEG] $95 MpegPlay             CR1=9500 CR2=FF00 CR3=0000 CR4=00FF
[MPEG] $9D MpegSetStream        CR1=9D00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9A MpegSetConnection    CR1=9A06 CR2=0001 CR3=0026 CR4=C000
```

The play-burst $94 sets decode timing 0 (VSYNC) and output
destination 0 (VDP2).

## Polling Modes

The title alternates between two polling modes in 256-field periods.
In the tight mode it hammers the status report in a busy-wait, about
19 groups per field with no output-configuration commands:

```
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
...
$90, $9B, $9B, $90 repeats, about 19 groups per field
...
```

In the per-field mode it runs one fixed sequence per field: an $AF
read-back of LSI register 6 rides the first poll group and the field
ends with an $A0 display-on re-send (the per-field $A0 suggests the
whole output-configuration burst is re-sent here, with the identical
$A1-$A4 elided by the tracer):

```
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
```

The mode flips exactly when bit 8 of the operation-interval counter
(returned in CR4 of the $90 status report) toggles, so the title
apparently branches on the reported counter value. The counter's
real-hardware reset semantics are unvalidated (this trace comes from a
model that free-runs it from $93), so the observed 256-field period is
downstream of that assumption; on hardware the flip may follow a
different counter behavior.

## Opening Movies

The first movie's play burst is followed about half a second later by
the display window:

```
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0020 CR4=0028
[MPEG] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=8011 CR4=8011
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=000F CR4=0028
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=0120 CR4=00A0
[MPEG] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A4 MpegSetVideoEffect   CR1=A400 CR2=0F00 CR3=0000 CR4=0000
```

Window values: frame-buffer position $20/$28, ratio $8011/$8011
(1:1), display position $0F/$28, display size $120 x $A0 (288x160 -
smaller than the screen, displayed inset). $A4 carries $0F00
(interpolation flags).

About half a second after the window burst two $91 reads arrive two
fields apart - the same paired-read pattern the title uses at every
interrupt consumption.

About 34 seconds into playback two more $91 reads arrive two fields
apart (the reads consume whatever causes are pending, so the timing
does not necessarily mark the stream end). The display stays on and
the polling (with the per-field $A0/$AF) continues for another 11
seconds before the title switches the display off. A couple of plain
poll groups follow in the same field, then the title goes silent:

```
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
```

The second movie plays when a new game is started. The silent gap
before its play burst depends on menu timing (14-20 seconds across
runs, whether the first movie ended naturally or was skipped). The
play burst is identical and no window burst follows - the first
movie's window is reused unchanged. A pair of $91 reads two fields
apart arrives at a run-varying point during playback (5 seconds in
in one run, 39 in another, and in one run a second pair landed
immediately before the display off): the reads consume whatever
causes are pending when the game checks, so they do not mark the
stream end. The display stays on with the per-field $A0/$AF polling
running throughout, and the display off lands about 57 seconds after
the play burst in every traced run. The third movie's play burst
follows about three seconds later (polling continued through that gap
in one run and went silent in another).

## Early Exit (skipped)

Skipped a few seconds into the first movie, the teardown lands in a
single field: display off, two plain poll groups, the two-round
per-layer disconnect ($9A stages next-slot records, both partition
$FF; the first $9C commits only the audio layer, the second only the
video layer), then the register-$1A read/write:

```
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9A MpegSetConnection    CR1=9A00 CR2=00FF CR3=01FF CR4=00FF
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9C MpegChangeConnection CR1=9C00 CR2=00FF CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9A MpegSetConnection    CR1=9AFF CR2=00FF CR3=0100 CR4=00FF
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9C MpegChangeConnection CR1=9C00 CR2=FF00 CR3=0000 CR4=0000
[MPEG] $AE MpegGetLsi           CR1=AE00 CR2=001A CR3=0000 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF00 CR2=001A CR3=0000 CR4=0000
```

No register-6 idle read-back follows the teardown: the per-field
polling mode reads register 6 throughout playback anyway, and the
last of those read-backs lands just before the display-off. The $AF
register-$1A write is the final MPEG command; the title is completely
silent afterward.

## In-Game Movie

The play burst is again identical; the window burst about one second
later re-sends only the three sub-parameters that change (ratio stays
1:1, border/fade/effect keep their values):

```
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0060 CR4=003C
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=0017 CR4=0031
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=00A0 CR4=0078
```

Window values: frame-buffer position $60/$3C, display position
$17/$31, display size $A0 x $78 (160x120): a quarter-screen picture
inside the game screen, sourcing from an offset well inside the frame
buffer. The display comes back on with the next per-field $A0
(CR2=0100). The trace ends about 12 seconds later with a final
display-off while the movie screen is left.

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
