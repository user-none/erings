# LUNAR SILVER STAR STORY MPEG (T-27904G) MPEG Command Trace

Program-stream user of the decoder: the disc mux is delivered straight
into CD buffer partitions and both decoder layers are connected to
them ($9A: video partition 0 record mode $26, audio partition 1 record
mode $06). Playback is A/V-synchronized ($95 CR1 low byte 00) with
VSYNC-paced decode ($94 decode timing 0), so $97 Out Decoding Sync is
never issued. The title polls $90 + $9B x2 + $9E x2 (it reads the
stream records as well as the connections). Repeating poll patterns
are elided with `...` comments.

## Game Start

Decoder probe at boot, no playback:

```
[MPEG] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
```

## Intro Movie

Re-init and window setup before the play burst (the window is
programmed before $95):

```
[MPEG] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0016 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=000F CR4=0001
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=0000 CR4=0008
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=0140 CR4=00E0
[MPEG] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A4 MpegSetVideoEffect   CR1=A400 CR2=0F00 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
```

Window values: frame-buffer position $16/0, frame-buffer ratio $000F
(7/8 source step) horizontal with $0001 vertical, display position
0/8, display size $140 x $E0 (320x224). $A4 carries $0F00
(interpolation flags). The FMV screen mode is 320x224: the window
fills it, flush top.

Play burst and steady state:

```
[MPEG] $94 MpegSetMode          CR1=94FF CR2=0000 CR3=FF00 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $96 MpegSetDecodeMethod  CR1=9604 CR2=0001 CR3=0000 CR4=0001
[MPEG] $95 MpegPlay             CR1=9500 CR2=FF00 CR3=0000 CR4=00FF
[MPEG] $9D MpegSetStream        CR1=9D00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9A MpegSetConnection    CR1=9A06 CR2=0001 CR3=0026 CR4=C000
...
$90, $9B, $9E, $9B, $9E repeats while the video plays
...
```

Played to the end, the stream's own end mark stops the decoders (the
video connection mode $26 includes switch-on-system-end, and the next
records are disconnects, so the layers release their partitions with
no teardown commands). Close to the end the title appends the LSI
decoder-status read-back to each poll cycle and loops until the
decoder reports idle - the last picture has been presented, not just
the last data read (about ten cycles across four fields here, audio
having drained ahead of video):

```
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
...
$90, $9B, $9E, $9B, $9E, $AF repeats until the decoder reads idle
...
$90, $9B, $9E, $9B, $9E two plain poll cycles
...
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
```

No MPEG command follows that final poll cycle; the next MPEG activity
is the re-init for the first in-game FMV.

The intro can instead end early (user skip). The title performs by
hand the disconnect the end mark would have triggered: $9A programs a
disconnect next record and $9C switches one layer at a time (bit 7 in
the other layer's CR2 byte masks that layer's switch), cutting both
decoders off their partitions. A read-modify-write clears LSI register
$1A, a single decoder-status read-back confirms idle (decode is
already halted, so one read suffices), and the display goes off:

```
[MPEG] $9A MpegSetConnection    CR1=9A00 CR2=00FF CR3=01FF CR4=00FF
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9C MpegChangeConnection CR1=9C00 CR2=00FF CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9A MpegSetConnection    CR1=9AFF CR2=00FF CR3=0100 CR4=00FF
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9C MpegChangeConnection CR1=9C00 CR2=FF00 CR3=0000 CR4=0000
[MPEG] $AE MpegGetLsi           CR1=AE00 CR2=001A CR3=0000 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF00 CR2=001A CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
```

The whole skip teardown lands inside one field. Polling continues for
a few cycles after the display switch, then the title leaves the
movie.

## First In-Game FMV

Identical bring-up to the intro movie ($93 / $92 / $94, window burst,
$94 / $96 / $95 / $9D / $9A play burst, same values), then:

```
...
$90, $9B, $9E, $9B, $9E repeats while the video plays
...
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
...
$90, $9B, $9E, $9B, $9E, $AF repeats until the decoder reads idle
...
```

The $AF raw LSI access (register 6 read-back) appears only in the
end-of-stream window: the title polls it alongside the status report
to detect decode completion (about ten cycles across four fields
here). The close-out is then identical to the intro movie's full end:
two plain poll cycles, $A0 display off, and one final $90 / $9B / $9E
/ $9B / $9E cycle, with no MPEG command after it.

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
