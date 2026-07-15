# GUNGRIFFON - THE EURASIAN CONFLICT - (T-4502G) MPEG Command Trace

Program-stream user of the decoder: the disc mux is delivered straight
into CD buffer partitions and both decoder layers are connected to
them ($9A: video partition 0 record mode $26, audio partition 1 record
mode $06, no picture-search bits - CR4 0000). Playback is
A/V-synchronized ($95 CR1 low byte 00) with VSYNC-paced decode ($94
decode timing 0), so $97 Out Decoding Sync is never issued. The title
polls $90 + $9B x2 + $9E x2 (it reads the stream records as well as
the connections, current then next selector) at a very high rate -
about 57 poll groups per frame throughout playback. It programs the
$92 interrupt mask ($0500: picture-start + sequence-end) but never
issues $91; end of stream is read from the status report and the
register-6 read-back. Repeating poll patterns are elided with `...`
comments.

The opening FMV was traced twice from power-on: once played to the
end (about 2 m 44 s) and once exited early (skipped about 21 s in).
Bring-up and steady-state playback are identical in both runs; the
exit paths differ.

## Bring-Up and Play Burst

$93 Init is issued alone at power-on; the mask and scan mode follow a
few frames later, and the window burst, display-on, and play burst all
land in one frame. The display window is programmed and the display
switched on before playback starts:

```
[MPEG] $93 MpegInit             CR1=9300 CR2=0001 CR3=0000 CR4=0000
[MPEG] $92 MpegSetInterruptMask CR1=9200 CR2=0500 CR3=0000 CR4=0000
[MPEG] $94 MpegSetMode          CR1=94FF CR2=FFFF CR3=0100 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A100 CR2=0001 CR3=0000 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A101 CR2=0001 CR3=0001 CR4=0001
[MPEG] $A1 MpegSetWindow        CR1=A102 CR2=0001 CR3=0000 CR4=0000
[MPEG] $A1 MpegSetWindow        CR1=A103 CR2=0001 CR3=0160 CR4=00F0
[MPEG] $A2 MpegSetBorderColor   CR1=A200 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A3 MpegSetFade          CR1=A300 CR2=0000 CR3=0000 CR4=0000
[MPEG] $A4 MpegSetVideoEffect   CR1=A400 CR2=0F00 CR3=0000 CR4=0000
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0100 CR3=0000 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
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
[MPEG] $9A MpegSetConnection    CR1=9A06 CR2=0001 CR3=0026 CR4=0000
[MPEG] $90 MpegGetStatus        CR1=9000 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0000 CR4=0000
[MPEG] $9B MpegGetConnection    CR1=9B00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $9E MpegGetStream        CR1=9E00 CR2=0000 CR3=0100 CR4=0000
[MPEG] $98 MpegGetTimecode      CR1=9800 CR2=0000 CR3=0000 CR4=0000
```

The first $94 sets scan mode $01 (NTSC interlaced); the play-burst $94
sets decode timing 0 (VSYNC) and output destination 0 (VDP2). Window
values: frame-buffer position 0/0, ratio $0001/$0001 (the reciprocal
wire form of 1:1), display position 0/0, display size $160 x $F0
(352x240 - the full VideoCD NTSC frame, no scaling). $A4 carries
$0F00 (interpolation flags). The movie screen mode is 352x240, so the
window fills the screen.

$98 Get Timecode is issued exactly once, right after the play burst.
It is never re-issued, so the title does not sequence anything off the
timecode.

## Steady State

```
...
$90, $9B, $9E, $9B, $9E repeats while the video plays (about 57 poll
groups per frame)
...
```

## End of Stream (played to the end)

When the stream ends the register-6 read-back (decoder-idle bit
$4000) joins every poll group. The read-back window lasts about 32
frames (roughly half a second, about 1600 read-backs), then the
display is switched off:

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
$AF joins every poll group (about 54 per frame) for the 32-frame
read-back window
...
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
```

On this path the title never disconnects the decoder layers - no
$9A / $9C teardown and no register-$1A access. After the display-off
a few more plain
poll groups are issued in the same frame and then the title goes
completely silent - no MPEG commands over the remaining ~20 s of the
trace.

## Early Exit (skipped)

When the movie is skipped before its end, all in one frame: display
off, a two-round per-layer disconnect, then raw LSI register access
ending with a single register-6 read-back:

```
[MPEG] $A0 MpegDisplay          CR1=A000 CR2=0000 CR3=0000 CR4=0000
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
```

The layers are disconnected in two rounds using the $9C
per-layer selection byte: the first round stages next-slot records
(both partition $FF) and commits only the audio layer (CR2 $00FF -
video bit 7 set, skipped), the second re-stages and commits only the
video layer (CR2 $FF00 - audio bit 7 set, skipped). The register-$1A
read/write pair precedes the register-6 idle read-back, which is
issued exactly once on this path - the disconnect has already stopped
the decoder. The title then goes completely silent, as on the
played-to-the-end path.

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
