# YUKYU NO KOBAKO (T-27806G) MPEG Command Trace

Raw elementary-stream user of the decoder, host-stepped profile:
video-only FMV ($9A audio record $FF), raw video elementary
stream in partition 0 (video record mode $27), independent playback
($95 CR1 low byte 01), host-synchronized decode ($94 decode timing 1)
stepped with $97 - one per picture, about every other frame for the
29.97 fps stream, at 320x224. Repeating poll patterns are elided with
`...` comments.

## FMV (played to the end)

Bring-up and play burst; the window is programmed after the first
picture-start interrupt:

```
[MPEG] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $91 MpegGetInterrupt     CR1=9100 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=0100 CR3=FF00 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $96 MpegSetDecodeMethod  CR1=9604 CR2=0001 CR3=0000 CR4=0001
[MPEG] $95 MpegPlay             CR1=9501 CR2=FF00 CR3=0000 CR4=00FF
[MPEG] $9D MpegSetStream        CR1=9D00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9A MpegSetConnection    CR1=9AFF CR2=00FF CR3=0027 CR4=0000
...
picture-start interrupt (cause $000100, in the $92 mask) wakes the game
...
[MPEG] $91 MpegGetInterrupt     CR1=9100 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $97 MpegOutDecodingSync  CR1=9700 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0000 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=8011 CR4=8011
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=FFFF CR4=0008
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=0140 CR4=00E0
[MPEG] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A4 MpegSetVideoEffect   CR1=A400 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
```

Window values: frame-buffer position 0/0, ratio $8011/$8011 (1:1),
display position $FFFF/8, display size $140 x $E0 (320x224).

Steady-state playback. This game re-sends an identical window burst
every frame:

```
...
$90, $9B, $9B polling with $97 about every other frame (one per
picture, 29.97 fps stream) while the video plays; the game also
re-sends an identical window burst every frame:
$A1 sub2, sub0, sub2, sub1, sub0, sub3, sub0 (display position
FFFF,0008; fb position 0,0; fb ratio 8011,8011; display size 320x224 -
values never change for the whole video)
...
video stream ends on the in-stream sequence end code (00 00 01 B7)
...
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
...
$90, $9B, $9B polling continues briefly after the video ends, then no
MPEG command follows ($91 is never issued again; no display-off and
no teardown - the decoder is left connected with the display switch
on)
...
```

## Early Exit (skipped)

Skipped mid-playback, the teardown lands in a single field right
after the last $97 step: a single-round disconnect ($9A stages
next-slot records, both partition $FF; $9C commits only the video
layer - the audio layer was never connected), the register-$1A
read/write, then one register-6 idle read-back. As on the played-out
path, no $A0 display-off is issued:

```
[MPEG] $9A MpegSetConnection    CR1=9AFF CR2=00FF CR3=0100 CR4=00FF
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9C MpegChangeConnection CR1=9C00 CR2=FF00 CR3=0000 CR4=0000
[MPEG] $AE MpegGetLsi           CR1=AE00 CR2=001A CR3=0000 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF00 CR2=001A CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
...
$90, $9B, $9B polling continues for a few groups in the same field,
then no MPEG command follows
...
```

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
