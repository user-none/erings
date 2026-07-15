# VATLVA (T-31501G) MPEG Command Trace

Raw elementary-stream user of the decoder: the FMV is video-only (no
audio connection, $9A audio record $FF) with the raw video elementary
stream delivered to partition 0 (video record mode $27). Playback is
independent ($95 CR1 low byte 01) with host-synchronized decode ($94
decode timing 1), so the title steps pictures itself with $97 Out
Decoding Sync - one per picture, about every other frame for the
29.97 fps stream. Repeating poll patterns are elided with `...`
comments.

## FMV (played to the end)

Bring-up and play burst. Note the window is programmed after playback
starts, triggered by the first picture-start interrupt:

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
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0001 CR4=0001
[MPEG] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=8011 CR4=8011
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=0001 CR4=0001
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=015F CR4=00EF
[MPEG] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A4 MpegSetVideoEffect   CR1=A400 CR2=0F00 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
```

Window values: frame-buffer position 1/1, ratio $8011/$8011 (1:1),
display position 1/1, display size $15F x $EF. The FMV screen mode is
352x224: the full-height window overflows the 224-line frame, which
crops the picture to rows 7-230.

Steady-state playback:

```
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $97 MpegOutDecodingSync  CR1=9700 CR2=0000 CR3=0000 CR4=0000
...
$90, $9B, $9B, $90, $9B, $9B, $90, $97 repeats while the video plays
($97 once per picture, 29.97 fps stream, about every other frame)
...
video stream ends (EOR sector); sequence-end cause $000400 asserts MPST
...
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
...
$90, $9B, $9B back to this sequence after the video ends
($91 is never issued again; the game reads the end from the status
report and the $AF register-6 read-back, decoder-idle bit $4000)
...
```

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
