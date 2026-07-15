# WANGAN DEADHEAT PLUS REAL ARRANGE (T-9103G) MPEG Command Trace

Program-stream user of the decoder: the disc mux is delivered straight
into CD buffer partitions and both decoder layers are connected to
them ($9A: video partition 0 record mode $26, audio partition 1 record
mode $06). Playback is A/V-synchronized ($95 CR1 low byte 00) with
VSYNC-paced decode ($94 decode timing 0), so $97 Out Decoding Sync is
never issued. The title polls only $90 + $9B x2 (current then next
selector) - it never reads the stream records ($9E) and never issues
$91, even though it programs the $92 interrupt mask; end of stream is
read from the status report and the $AF register-6 read-back.
Repeating poll patterns are elided with `...` comments.

Two movies were traced. Both use the identical bring-up, window
values, and play burst below; only the stream lengths differ (first
movie about 4100 frames / 69 s, second about 650 frames / 11 s).

## Movie Bring-Up and Play Burst

The display window is programmed and the display switched on before
the play burst:

```
[MPEG] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0000 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=8011 CR4=8011
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=0000 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=0160 CR4=00F0
[MPEG] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A4 MpegSetVideoEffect   CR1=A400 CR2=0F00 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
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

The first $94 sets scan mode $01 (NTSC interlaced); the play-burst $94
sets decode timing 0 (VSYNC) and output destination 0 (VDP2). Window
values: frame-buffer position 0/0, ratio $8011/$8011 (1:1), display
position 0/0, display size $160 x $F0 (352x240 - the full VideoCD NTSC
frame, no scaling). $A4 carries $0F00 (interpolation flags). The
movie screen mode is 352x240, so the window fills the screen.

## Steady State and End of Stream

```
...
$90, $9B, $9B repeats while the video plays (about three groups per
frame, with roughly two additional standalone $90 polls per frame)
...
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
...
$AF joins every poll group over the last few frames of the stream
(4-6 frames, 10-16 polls of LSI register 6) until the decoder reads
idle
...
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
```

After the display-off, two or three more $90 + $9B x2 poll groups are
issued in the same frame and then the title goes silent - no MPEG
commands at all between movies (about 2400 frames between the first
and second here). The second movie starts over from $93 MpegInit with
the identical sequence.

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
