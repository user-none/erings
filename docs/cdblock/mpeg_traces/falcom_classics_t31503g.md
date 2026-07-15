# FALCOM CLASSICS (T-31503G, CD-2/2) MPEG Command Trace

Raw elementary-stream user of the decoder: the FMV is video-only (no
audio connection, $9A audio record $FF) with the raw video elementary
stream delivered to partition 0 (video record mode $27). Playback is
independent ($95 CR1 low byte 01) with host-synchronized decode ($94
decode timing 1), so the title steps pictures itself with $97 Out
Decoding Sync - one per picture, about every other frame for the
29.97 fps stream. Repeating poll patterns are elided with `...`
comments.

## Delivery Architecture

- The video decoder is connected to partition 0 ($9A video record:
  connection mode $27, partition 0); the audio decoder is left
  disconnected ($FF). No $64 Put Sector Data: the CD block filters
  deliver the raw video elementary stream into partition 0 directly.
- Interrupt mask $0500 ($92) enables picture-start ($000100) and
  sequence-end ($000400).
- After bring-up the title waits (no MPEG polling) while the drive
  seeks the FMV and the partition fills, then issues the play burst.
- The window is programmed in the same burst that starts playback,
  not deferred to the first picture-start interrupt.
- The clip is torn down explicitly: the connection is disconnected
  through the $9A next-slot / $9C Change Connection path rather than a
  re-init, followed by raw LSI register access.

## Bring-up

```
[MPEG] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
...
long wait with no MPEG commands while the drive seeks the FMV and the
CD block fills partition 0
...
```

## Play burst

Mode is switched to host-timed decode ($94 CR2=0100), the decode
method and play parameters are set, and the video decoder is bound to
partition 0:

```
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
```

## Window burst and display on

```
[MPEG] $97 MpegOutDecodingSync  CR1=9700 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0010 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=8011 CR4=8011
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=FFFF CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=0160 CR4=00F0
[MPEG] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A4 MpegSetVideoEffect   CR1=A400 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
```

Window values: frame-buffer position X=$10 Y=0 (source anchored 16
dots in, trimming the left edge), ratio $8011/$8011 (1:1), display
position X=$FFFF (-1, one dot left of the visible frame = at the
visible origin) Y=0, display size $160 x $F0 (352x240). The picture is
displayed full-frame at 1:1 from the visible top-left. Border color,
fade, and video effect are all zero.

## Steady-state playback

```
[MPEG] $97 MpegOutDecodingSync  CR1=9700 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
...
$97 (once per picture) plus the $90 + $9B x2 poll pair repeats while
the video plays; $97 is issued every field at first, then settles to
every other field for the 29.97 fps stream
...
```

## Teardown

The connection is disconnected through the next-slot / Change
Connection path (both records staged to $FF), then the title reads and
writes LSI registers, ending with the register-6 read-back that
carries the decoder-idle bit ($4000):

```
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
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
$90 + $9B x2 polling continues after the teardown
...
```

The $9A/$9C pair stages both records ($FF) and commits them, so the
per-layer selection byte in $9C CR2 ($FF00, not modeled) does not
change the outcome here - both layers disconnect either way. The $AE
read of register $1A and the paired $AF write precede the register-6
idle read-back (decoder-idle bit $4000) that confirms the decoder has
stopped.
