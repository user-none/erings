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
programmed before $95, unlike the raw-ES titles):

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
(interpolation flags).

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

The intro can end early (user skip): display off, polling continues:

```
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
```

## First In-Game FMV

Identical bring-up to the intro movie ($93 / $92 / $94, window burst,
$94 / $96 / $95 / $9D / $9A play burst, same values), then:

```
...
$90, $9B, $9E, $9B, $9E repeats while the video plays
...
[MPEG] $AF MpegSetLsi           CR1=AF01 CR2=0006 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
...
$AF, $90, $9B, $9E, $9B, $9E repeats when close to the end
...
$90, $9B, $9E, $9B, $9E back to this sequence until the end
...
```

The $AF raw LSI access (register 6 read-back) appears only in the
end-of-stream window: the title polls it alongside the status report
to detect decode completion.
