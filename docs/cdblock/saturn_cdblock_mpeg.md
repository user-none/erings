# MPEG (Video CD) Cartridge Subsystem

Companion to [saturn_cdblock_firmware.md](saturn_cdblock_firmware.md),
which covers the shared context this subsystem builds on: the host
command dispatch, the interrupt/task model, the extension/hook mechanism,
and the memory map. This file documents the MPEG-specific behavior only.

Everything in the $A020-$F712 region plus DMAC2/3, ITU0-2, and vectors
76/78/88/89 serves the MPEG cartridge. On a stock unit this subsystem is
initialized and probed at boot, then never runs: task 8 waits forever,
and the DMA/capture interrupts never fire.

This is the host-facing interface for MPEG-1 video/audio decode. When a
Video CD or Movie Card cartridge is present, the game (running on the
SH-2) drives playback entirely through CD block host commands $90-$AF:
the game issues an MPEG command in the CR registers, the CD block
firmware translates it into register writes and command packets for the
two decoder LSIs on the cartridge, and reports decoder status and
interrupts back through the same CR/HIRQ path. The CD block also routes
the MPEG sector stream off the disc into the decoder. The firmware itself
does no decoding - it is the command relay and status aggregator between
the host and the cartridge silicon.

## Architecture

A structural fact that governs the rest of this document: **the CDB-106
ROM does not contain the MPEG command logic.** It contains the dispatch
plumbing, a set of default handlers, and the LSI transport, but the
authoritative command behavior lives in the cartridge's own firmware
image - a separate ROM loaded from the cartridge, not part of cdb106.bin.

The load path (firmware doc, Extension / Patch Mechanism): at boot the
CD block reads a length + image from the window at $0E000000, copies it
to buffer DRAM at $0907B000, and calls the image entry point at
$0907B004. That image populates the RAM dispatch table at $0907B008 -
**the CDB-106 ROM never writes that table itself** (the only reference to
$0907B008 in the whole ROM is the read in the dispatch tail). Every MPEG
host command dispatches through $0907B008, so with no cartridge the table
is empty and MPEG commands cannot run; $93 MpegInit even rejects unless
the image-id byte at $0907B000 is nonzero.

The ROM's $F938 table is an export directory of default handlers the
cartridge image clones into $0907B008 and selectively overrides. So the
ROM defaults documented below are the reference behavior a stock card
falls back to, not necessarily the exact behavior of any given title's
cartridge.

Scope of what cdb106.bin reveals: the command dispatch, the host CR/HIRQ
contract, the status-report assembly, the interrupt/event model, the
state variables, and the LSI transport. The command parameter, status,
and interrupt bit fields - which the firmware only moves as opaque values
- are documented in the sections below from the published CD block MPEG
interface definitions and cross-checked against the firmware where it
acts on them.

The one layer still not documented here is the internal encoding of the
raw decoder-LSI registers (the individual bits written into the
$0A100000/$0A180000 windows), which belongs to the decoder-LSI datasheet;
the host command interface built on top of it is fully covered.

### The two decoder LSIs

The cartridge carries two decoder LSIs, addressed through register
windows at $0A100000 (LSI A) and $0A180000 (LSI B) on the SH-1 bus. The
firmware talks to them two ways:

- **Register access**: direct word reads/writes into the windows (used by
  $AE/$AF raw access and the status polling).
- **4-byte command packets**: staged at SH-1 RAM $0F000840 and pushed by
  DMAC2 (to LSI A at $0A10001E) or DMAC3 (to LSI B at $0A180000).
  Completion raises vector 76 (LSI A) / 78 (LSI B).

Typical division of labor on a Video CD card: LSI A = video decode +
display, LSI B = audio decode. The firmware treats them symmetrically at
the transport level (mirrored DMAC channels, shadow blocks, status bits).

## Command dispatch and response pattern

Every MPEG command follows the same shape:

1. The SH-2 writes CR1-CR4 (command $90-$AF in CR1 high byte) and IRQ6
   fires on the SH-1 (firmware doc, Host Command Processing).
2. The dispatcher gates the command: it requires MPEG hardware present
   ($0F00027D bit 1) and MPEG active ($0F000892 bit 7), except $93 which
   skips the active check. It then jumps through the extension table at
   $0907B008 + (code & $3F - 16)*4.
3. The handler reads its parameters out of CR1-CR4 (passed in R1 =
   CR1:CR2, R2 = CR3:CR4), mutates the MPEG state RAM ($0F000840-$0F00089F)
   and/or writes the LSI register windows, and where needed stages a
   command packet for DMAC2/3.
4. Almost every handler ends by calling the **GetStatus service** (ROM
   index 68, $AEB4) to build the response CRs. So the default response to
   nearly all MPEG commands is the current MPEG status report (see MPEG
   Status Report Format below); commands that read something specific
   (Get Interrupt, Get Connection, Get picture info) override individual
   CR fields on top of that.
5. The handler returns; the IRQ6 tail writes the CRs and asserts CMOK,
   and (for the deferred/async parts) task 8 processes LSI events and
   asserts MPCM/MPST.

## MPEG Command Set ($90-$AF)

Host commands $90-$AF dispatch through the extension table
($0907B008 + (code - $90)*4). Commands $93/$AE/$AF are ROM-resident
(handlers $A060/$A158/$A100); the rest use the ROM default handlers
listed under ROM dispatch table defaults ($F938 entries 0-31), which an
extension image may override. Behavior traced from those defaults:

| Code | Handler | Traced behavior |
|------|---------|-----------------|
| $90 | $AEEA | Read decoder status via service $F89A; return it (Get Status) |
| $91 | $AEFA | Read and clear the interrupt-status long $0F000848 (Get Interrupt) |
| $92 | $AF2A | Write CR to the interrupt-mask $0F00084C (Set Interrupt Mask) |
| $93 | $A060 | Init/reset the subsystem (ROM-resident; see below) |
| $94 | $A5EC | Program LSI A parameter block $0F000854 / window $0A100000 (Set Mode) |
| $95 | $A760 | Update subsystem state $0F000890 and LSI A params; start/stop decode (Play) |
| $96 | $A834 | Configure decode method flags in $0F000890 (Set Decode Method) |
| $97 | $A99C | Out Decoding Sync (host-timed decode step; touches LSI A control +20) |
| $98 | $9D54 | Get Timecode (stages request to $09075384, service $F8EE) |
| $99 | $9E6C | Get PTS (reads work area $090752A0) |
| $9A | $AF74 | Set Connection (bind a buffer partition to a decoder input) |
| $9B | $B3B8 | Get Connection |
| $9C | $B538 | Change Connection (validates, then reconfigures the connection) |
| $9D | $B2D4 | Set Stream (stream/channel number; validates <= 31) |
| $9E | $B45C | Get Stream |
| $9F | $9E84 | Get Picture Size |
| $A0 | $AA74 | Display (display-enable + frame bank; LSI control +20) |
| $A1 | $9EE8 | Set Window (window position/size) |
| $A2 | $B688 | Set Border Color (LSI shadow +18) |
| $A3 | $B696 | Set Fade (gated on LSI-ready $0F000890 bit 1) |
| $A4 | $B6E6 | Set Video Effect (LSI shadow +24) |
| $A5 | $B7DA | Additional display attribute (window sub-parameter) |
| $A6 | $C44E | Get image / picture info |
| $AE | $A158 | Raw read of an LSI shadow register (window select by CR1) |
| $AF | $A100 | Raw write CR4 to an LSI register window |

Command codes $90-$A4 and their names are confirmed by disassembling the
host-side command builders (each writes the opcode into CR1); the earlier
firmware-only labels for $97-$9F and $A1 were corrected against them. $A5,
$A6, and the $A7-$AD range (no ROM default) were not individually
confirmed.

The command codes and names for $90-$A4 are confirmed by disassembling
the host-side command builders (each writes its opcode into CR1); the
handler addresses and register effects are from the ROM. Codes with no
default entry reject until an extension image installs a handler.

Notes on specific commands:

- **$93 MpegInit ($A060)** rejects with status $FF unless the image-id
  byte at $0907B000 is nonzero (no cartridge -> no init). Its reset path
  shuts the DMAC/capture comm units down ($A4A0), clears the request
  flags $09075388-$0907538A, and sends reset messages to task 8 (events
  $89/$8A/$8C/$81) and task 9 ($81). The actual bring-up is done by hook
  33/34 (below), which the cartridge image supplies.
- **$95 Play ($A760)** builds the decode-run mode from CR1 bits into the
  subsystem state byte at $0F000890 (bit masks for run/pause and the
  audio/video enables), reads/writes the LSI A control word at
  $0F000854+20 / window $0A100000, then returns MPEG status. This is the
  start/stop/pause of decoding.
- **$AE / $AF raw LSI access** are ROM-resident primitives: $AF writes
  CR4 into window[CR2 low byte & ~1] (window selected by CR1 bit 1,
  read-back mode by CR1 bit 0); $AE reads the RAM shadow blocks instead
  ($0F000854 for LSI A parameters, $0F000884 for the LSI B control
  shadow). These are the escape hatch a cartridge image or diagnostic
  uses to poke the LSIs directly.

The bring-up services the cartridge image installs (ROM defaults shown):

- **Hook 33 ($A53C)** programs LSI A from a parameter block at $0F000854:
  the block's words go to window A offsets 0, 2, 20, 26, 32 (values
  $88FE, $0096, $FFF3, $FF7E, $47AF, plus $01E0/1/1 lengths), and two
  config words are mirrored to $09075320.
- **Hook 34 ($A500)** resets the subsystem state: LSI B control = $8209
  (shadow $0F000884), $0F000890 = $67818022 (which sets MPEG-active bit 7
  of $0F000892), $0F000850 = $FFFFFFFF, $0F000898 = three words $0101,
  and $090752E0 = 20.

## Command Parameter Encodings

The firmware moves these parameters as opaque values; their published
meanings (part of the CD block MPEG interface definition, cross-checked
against the firmware where it acts on them) are:

**$94 Set Mode** - four byte parameters (disassembled from the
host-side command builder): CR1 low byte = operation mode (0 = normal
movie, 1 = still picture, 2 = hi-res movie (unsupported), 3 = hi-res
still, 4 = MPEG sector-buffer mode), CR2 high byte = decode timing
(0 = VSYNC-synchronized, 1 = host-synchronized), CR2 low byte = output
destination (0 = VDP2, 1 = host transfer), CR3 high byte = scan mode
(see Picture geometry below). $FF in any byte keeps the current value.

**$95 Play** - four byte parameters (disassembled from the host-side
command builder): CR1 low byte = playback mode (0 = A/V-synchronized
playback, 1 = independent playback with no A/V sync), CR2 high byte =
audio-decoder transfer mode, CR2 low byte = video-decoder transfer mode
(0 = automatic transfer, 1 = forced transfer), CR3 = 0, CR4 low byte = a
fourth parameter whose meaning is untraced (the host library always
sends $FF for it). $FF in any parameter byte keeps the current value,
matching the ROM handler's bit-7 = keep-current convention.

**$96 Set Decode** - CR1 low byte = audio mute (0x04 default/unmuted,
0x01 mute right, 0x02 mute left), CR2 = pause-time word, CR4 =
freeze-time word (CR3 = 0). Pause time: 0 = pause (frame advance),
1 = normal playback, other values = slow-playback interval. Freeze
time: 0 = freeze, 1 = normal playback, other values = strobe-playback
interval.

**$97 Out Decoding Sync** - CR2 low byte = frame bank number
(disassembled from the host-side command builder). In host-synchronized
decode timing ($94) the decoder advances one picture per command.
Stream identification, the sequence header, and the first picture
proceed without it: a traced host-synchronized title waits for the
picture-start interrupt cause before issuing its first $97, then steps
at the stream's picture rate.

**Connection commands** (Set Connection $9A, Change Connection $9C) - the
decoder connection-destination parameter (one record per audio and video
layer: connection-mode byte, layer+picture-search byte, buffer partition
number) uses these connection-mode bits: 0x01 switch on EOR, 0x02 switch
on system-end, 0x04 delete sector, 0x08 ignore PTS, 0x10 clear VBV,
0x20 clear VBV + write-back cache, 0x40 evaluate end condition before the
back aperture. Layer: 0 = system, 1 = audio/video. Picture search:
0x00 off, 0x80 video, 0xC0 video + discard audio. Partition number $FF
disconnects the layer. The system-end code is recognized at the system
layer of the stream, not by byte pattern: the 00 00 01 B9 sequence
occurring inside packet payloads does not terminate playback (traced: a
title's audio packet payload carries the pattern mid-movie and plays
through it - audio data is not start-code-free). $9A wire layout (disassembled from the host-side
command builder): CR1 low byte = audio connection-mode, CR2 = audio
layer:partition, CR3 high byte = record selector (0 = current, 1 =
next), CR3 low byte = video connection-mode, CR4 = video
layer:partition. $9B reads back the selected records in the same
layout.

**Set Stream $9D** - stream parameter (per layer): stream-mode byte
(0x01 set stream number, 0x02 identify stream number, 0x10 set channel
number, 0x20 identify channel number), stream number, channel number.
Wire layout mirrors $9A: CR1 low byte = audio stream-mode, CR2 = audio
stream:channel, CR3 high byte = record selector, CR3 low byte = video
stream-mode, CR4 = video stream:channel; $9E reads back the same
layout.

**$A0 Display** - CR2 high byte = display switch (0 = off, nonzero =
on), CR2 low byte = frame bank number.

**$A1 Set Window** - CR1 low byte = window sub-parameter selector:
0 = frame-buffer position, 1 = frame-buffer ratio, 2 = display
position, 3 = display size, 4 = display offset (all five selectors go
through this one command). CR2 low byte = change flag, CR3 = X value,
CR4 = Y value. The display position places the picture's top-left on
screen (signed values; the display size is an exclusive extent) in the
decoder's output-raster coordinates: the X origin sits one dot left of
the visible frame and the Y origin at the top of the full raster, 8
lines above a 224-line frame (see "Display-position coordinate origin"
under the Display and Window Model). The frame-buffer position anchors the
source picture: each display pixel advances the source coordinate by
the frame-buffer ratio. The ratio wire encoding (disassembled from the
host library's converter): bits 4-13 = integer magnification in units,
low nibble = a fraction table index (0, .500, .666, .750, .800, .833,
.857 for indexes 1-7; 1.000, .500, .333, .250, .200, .166, .142 for
9-15), bit 15 set = the value is the source step directly, clear = the
step is the reciprocal 1/(value+1). All three observed wire values:
$8011 (direct form, integer 1 + fraction 0 = step 1.000) = 1:1;
$0001 (reciprocal form, value 0, step 1/(0+1)) = 1:1 through the
other form; $000F (reciprocal form, nibble 15 = 1/7, step
1/(1+1/7) = 7/8) = an 8/7 enlargement.

**$A3 Set Fade** - Y gain and C gain.

**$A4 Set Video Effect** - interpolation bits: 0x01 Y-horizontal, 0x02
C-horizontal, 0x04 Y-vertical, 0x08 C-vertical. Transparent-bit mode:
0 = off, 1 = luma 64, 2 = luma 96, 3 = luma 128, 0x04 magnify transparent
area. Blur (soft-switch): 0x01 on.

**Picture geometry** - NTSC 352x240 normal / 704x480 hi-res; PAL 352x288
normal / 704x576 hi-res. Scan mode: 0 = NTSC non-interlaced, 1 = NTSC
interlaced, 2 = PAL non-interlaced, 3 = PAL interlaced. These are the
per-scan-mode maxima; a stream's encoded picture can be smaller (a
320x224 stream has been measured from a title's discs).

**Picture type** (in timecode / status): 1 = I, 2 = P, 3 = B, 4 = D.

## MPEG Status Report Format

The GetStatus service (ROM index 68, $AEB4) - which nearly every command
tail-calls - assembles the response CRs from CD status plus MPEG state:

- It first calls the CD block's own status responder (ROM index 89,
  $91B8) to seed CR1:CR2 with the drive/CD status.
- It then ORs in the MPEG fields: the mode word at $0F00089C (nibble-
  packed into the CR1 status byte), bit 3 from $0F000891 bit 0, the word
  at $0F000842 into CR2, and sets CR3:CR4 = the long at $0F000844 (the
  MPEG status/flag longword).

The full Get Status response is four words (eight bytes) with this field
layout, which matches the firmware assembly above:

| CR | High byte | Low byte |
|----|-----------|----------|
| CR1 | CD status code | MPEG operation-status byte (see below) |
| CR2 | picture-info byte | audio-status byte |
| CR3 | video-status word (16-bit) | (same word) |
| CR4 | operation-interval (VSYNC) counter word | (same word) |

(The video-status word occupies all of CR3; the VSYNC counter all of CR4.
The video-status word is the `$0F000844` long's high half, the counter its
low half - the state-RAM entry earlier calls `$0F000846` a "signal-level
shadow" but the response uses it as the operation-interval counter.)

The published bit assignments of the MPEG portion:

**MPEG operation status byte** (packed into the CR1 status area; this is
what $0F00089C feeds):

- bits 0-2, video run state: 1 = stopped, 2 = prep-1, 3 = prep-2,
  4 = transferring/playing, 5 = switching stream, 6 = recovery
- bit 3: MPEG decode stopped (0x08)
- bits 4-6, audio run state: 0x10 stopped, 0x20 prep-1, 0x30 prep-2,
  0x40 transferring/playing, 0x50 switching, 0x60 recovery

(This matches the firmware's observed $0F00089C values 4/5 =
playing/switching.)

**Video status word** (16-bit): 0x0001 decoding, 0x0002 displaying,
0x0004 paused, 0x0008 frozen, 0x0010 last picture shown, 0x0020 odd
field, 0x0040 picture updated, 0x0080 video error, 0x0100 output ready,
0x0800 first picture shown, 0x1000 video buffer-partition empty.

**Audio status byte**: 0x01 decoding, 0x08 illegal, 0x10 buffer empty,
0x20 error, 0x40 left-channel output, 0x80 right-channel output.

## Get-command Response Layouts

The other read-back commands return fixed-size records in CR1-CR4. The
command codes are confirmed from the host-side command builders; the
record layouts are the published interface definitions a host driver
parses.

**Get Timecode ($98)** - a 4-byte time record plus three side values:
hour, minute, second, and picture (frame) number; separately the buffer
bank, the picture type (1=I, 2=P, 3=B, 4=D), and the track number.

**Get PTS ($99)** - the audio presentation timestamp (a 32-bit count).

**Get Connection ($9B)** (per layer, one record for audio and one for
video) - 3 bytes: connection-mode byte, layer + picture-search byte,
buffer partition number. (Same field set the Set Connection command takes.)

**Get Stream ($9E)** (per layer) - 3 bytes: stream-mode byte, stream
number, channel number.

**Get Picture Size ($9F)** - two values: horizontal and vertical pixel
size (e.g. 352x240 / 704x480 NTSC, 352x288 / 704x576 PAL).

## Display and Window Model

MPEG video output is placed on screen through a display-window model the
host configures with the $A0-$A5 commands. The parameters (published
field set; the firmware moves them to the display LSI as opaque values):

- **Display on/off + frame bank** ($A0 Display): a display-enable switch
  and the frame-buffer bank number to show.
- **Frame-buffer window** (the source region taken from the decoded
  frame): an origin and size within the decoded picture, a magnification
  ratio (X and Y), and a separate Y/C (luma/chroma) scaling ratio.
- **Display window** (where it lands on screen): a display reference
  position, a display size, and a relative-position offset.
- **Border color** ($A2) and a luminance level / border value.
- **Fade** ($A3): independent Y gain and C gain.
- **Video effects** ($A4): horizontal/vertical interpolation for Y and C,
  a transparent-bit mode (off / luma-64 / luma-96 / luma-128 / magnify),
  horizontal and vertical mosaic ratios, and horizontal/vertical soft
  (blur) switches.
- **Output mode**: direct to VDP2 (the decoded image is composited into
  the Saturn's video via VDP2's external-image input) or transferred to
  a host buffer.

The window geometry is expressed as signed 16-bit X/Y coordinate and size
pairs; scan mode (NTSC/PAL, interlaced or not) selects the coordinate
maxima (see Command Parameter Encodings). Multiple windows can be
composited (the software model supports up to four per movie).

### Display-position coordinate origin

The display position is expressed in the decoder's output-raster
coordinates, not in the console's visible-frame coordinates. Both axes
are offset from the visible frame; the offsets were measured by
comparing commanded positions against where pictures land on hardware.

**X origin: one dot left of the visible frame.** A picture lands on
frame column X + 1. A title wanting its picture flush left programs
X = -1; titles that draw a backdrop box behind a boxed window program
X = 23 and X = 39 and the picture lands centered on the backdrop at
frame columns 24 and 40. Measured on 320-dot horizontal modes only.
352-dot titles are traced, but all of them play full-screen video,
which offers no single-dot placement reference (a one-dot error only
drops a column at a screen edge), so the offset is unmeasured in that
mode - and the 352-dot modes run a faster dot clock, so if the offset
is a fixed output-stage time delay rather than a counter definition,
it need not be one dot there.

**Y origin: the top line of the full raster.** The raster is 240 lines
(NTSC) or 256 lines (PAL), and the VDP2 vertical-resolution setting
takes its display lines from the raster center (the VDP2 manual's TVMD
VRESO section: resolution-increment lines are added to the top and
bottom without changing the screen's center), so a picture lands on
frame line Y - (raster - lines) / 2:

| Screen | Frame line |
|--------|------------|
| NTSC 224 | Y - 8 |
| NTSC 240 | Y |
| PAL 224 | Y - 16 (inferred) |
| PAL 240 | Y - 8 (inferred) |
| PAL 256 | Y (inferred) |

Traced (each title's screen mode read from its TVMD setting): titles
wanting the picture at the top of a 224-line screen program Y = 8; a
boxed window at Y = 40 on a 224-line screen lands at frame line 32; a
boxed window at Y = 49 on a 240-line screen lands at frame line 49,
centered on its game-drawn backdrop; a title centering a 288x160
window on a 320x240 screen programs (15, 40), which is dead center
under this origin; and a title showing a full-height 239-line picture
on a 224-line screen programs Y = 1, a near-centered vertical crop
(picture rows 7-230 fill the frame). The PAL rows follow from the
same center rule; no PAL title has been traced.

Whether the X offset is a one-dot output-pipeline delay or part of the
firmware's parameter definition is not observable from the host side;
the mapping above is what all traced titles satisfy.

### CR packing of the display/window commands

The host-side builders for $A0-$A4 all assemble the same 8-byte
CR1-CR4 command block (confirmed by disassembling the command builders):

- **CR1 high byte** = the command opcode ($A0-$A4).
- **CR1 low byte / CR2 high byte** = command-specific selector bytes
  (which window, which sub-parameter), written as bytes.
- **CR3 and CR4** = the 16-bit parameter values.

Per command:

- **$A0 Display** - a display-enable/switch byte and the frame-bank
  number in the CR1 low / CR2 area.
- **$A1 Set Window** - the CR1 low byte selects the sub-parameter; the
  X and Y of the coordinate/size pair are carried as the CR3 and CR4
  words. The selector numbering is pinned by the builders' five
  consecutive branch stubs, each loading its selector immediate in
  order:

  | Selector | Sub-parameter |
  |----------|---------------|
  | 0 | frame-buffer position |
  | 1 | frame-buffer ratio |
  | 2 | display position |
  | 3 | display size |
  | 4 | display offset |

  A traced title's full-screen movie setup issues frame-buffer
  position (22, 0), frame-buffer ratio (15, 1), display position
  (0, 8), and display size (320, 224) for a stream whose encoded
  picture is 320x224 (measured from the decoded frames; MPEG picture
  sizes are not fixed to the 352x240 Video CD geometry) shown 1:1 on
  a 320x224 display mode. The display size equals the picture size,
  and display position (0, 8) places the picture at the visible top of
  the 224-line frame, one dot right of flush left, per the
  display-position coordinate origin (Y = 8 is the 224-line frame's
  top raster line). The frame-buffer position 22 anchors the source
  crop: with the 7/8 source step of ratio value 15, the 320-dot
  display spans source columns 22..302 of the 320-wide picture. All
  observed $A1 issues carry 0x0001 in CR2; that byte's meaning is
  unknown.
- **$A2 Set Border Color** - the 16-bit border color in CR2.
- **$A3 Set Fade** - the Y gain and C gain bytes in CR2 (high, low).
  Gain 0/0 is observed with unfaded output, so 0 means no fade rather
  than zero output; the gain scale is otherwise unknown.
- **$A4 Set Video Effect** - the effect bytes (interpolation,
  transparent-bit mode, mosaic H/V, soft H/V), packed into the block.
  The interpolation bits occupy the CR2 high byte (observed 0x0F00
  with CR3 and CR4 zero = all four interpolation switches on,
  everything else off); where the transparent-bit mode, mosaic, and
  soft fields sit among the remaining bytes is not confirmed.

So an implementation reads the opcode from CR1's high byte, the
selector(s) from the CR1-low/CR2 bytes, and the geometry/value words from
CR3/CR4.

## Interrupts and event flow

Four SH-1 interrupts serve the MPEG card; all convert hardware events
into task-8 messages and host HIRQ asserts:

| Vector | Source | Action |
|--------|--------|--------|
| 76 | DMAC2 done (LSI A packet sent) | sets $0F000892 bit 1, messages task 8 event $89 |
| 78 | DMAC3 done (LSI B packet sent) | sets $0F000892 bit 2, messages task 8 event $8A |
| 88 | ITU2 capture (LSI cart line) | calls extension hook 56, returns |
| 89 | ITU2 capture (LSI A status change) | decodes the LSI A status word (below) |

Vector 89 ($A1A0) is the important one for host feedback. It reads the
LSI A status word at $0A100000 three times and ANDs them (debounce),
latches the events into $0F000850 masked by $0F000852, then:

- a bit-6 event calls extension hook 40; a bit-4 event calls hooks 38/39
  (the cartridge image's per-event handlers);
- certain status bits assert host HIRQ **MPCM ($1000, bit 12)** or
  **MPST ($2000, bit 13)** when enabled by the mask bytes at
  $0F00084E/$0F00084F (see the HIRQ table in saturn_cdblock_commands.md);
- pending event bits accumulate at $0F000896 and task 8 is notified with
  event $8C payload $10.

Before MPEG startup ($0F000892 bit 7 clear) vector 89 only counts capture
edges into the word at $0F00089E (used by the boot probe).

**$91 GetInterrupt ($AEFA)** reads and clears the pending interrupt-status
long at $0F000848; **$92 SetInterruptMask ($AF2A)** writes CR into the
mask at $0F00084C. Together these are the host's poll/ack interface. The
two HIRQ summary bits are: **MPCM (bit 12)** = the MPEG operation-undefined
interval ended (status has settled and is safe to read), **MPST (bit 13)**
= an MPEG interrupt-status change is pending (the host should read
GetInterrupt).

The interrupt-status long at $0F000848 is a 24-bit cause register (the
published bit assignments):

| Bit | Cause | Bit | Cause |
|-----|-------|-----|-------|
| 0x000001 | video stream ready | 0x001000 | video sector trigger bit |
| 0x000002 | video stream switch done | 0x002000 | video sector EOR bit |
| 0x000004 | video output ready | 0x004000 | audio sector trigger bit |
| 0x000008 | video output start | 0x008000 | audio sector EOR bit |
| 0x000010 | video decode error | 0x010000 | audio stream ready |
| 0x000020 | video stream data error | 0x020000 | audio stream switch done |
| 0x000040 | video buffer-partition conn error | 0x040000 | audio output ready |
| 0x000080 | next video stream data error | 0x080000 | audio output start |
| 0x000100 | picture start detected | 0x100000 | audio decode error |
| 0x000200 | GOP start detected | 0x200000 | audio stream data error |
| 0x000400 | sequence end detected | 0x400000 | audio buffer-partition conn error |
| 0x000800 | sequence start detected | 0x800000 | next audio stream data error |

## Data path

The compressed MPEG stream (video + audio, multiplexed) is a track of
sectors on the disc. It reaches the decoder through the CD block's normal
sector pipeline, not a separate channel:

1. The game sets up a filter + buffer partition (Set Filter / connection
   commands, firmware doc) so the MPEG track's sectors land in a
   partition.
2. A stream-connection MPEG command ($9A-$9E, node index 0-31) binds that
   partition (or the CD device output) to the decoder input. The
   partition does not have to be disc-fed: a traced title demuxes the
   disc stream on the host and delivers the decoder's video elementary
   stream into its partition with Put Sector Data ($64). Such a title
   runs the shared buffer pool at capacity for most of a long stream
   (delivery outpaces realtime consumption), so it depends on the
   buffer-full flow control - drive pause with BFUL, resume on freed
   space - and on $64 answering WAIT rather than REJECT when a put
   races the drive for the last free sectors (both in
   saturn_cdblock_commands.md).
3. The decoder consumes the sectors; the firmware's role is transport
   (the DMAC2/3 packet channel carries LSI commands, and the sector data
   is delivered from buffer DRAM).

Decoded output is not returned through the CR/DATATRNS path - the video
LSI drives the Saturn's video path (composited via VDP2's external image
input), and audio goes to the card's audio output. The $A0-$A5 commands
(Display, window, border color, fade, video effects) configure that
output.

Note: the exact sector-to-LSI delivery mechanism (whether the LSI reads
buffer DRAM directly or the firmware DMAs it) is not fully traced from
cdb106.bin; the connection commands and the partition binding are the
part the ROM makes visible.

## MPEG State RAM

SH-1 on-chip RAM $0F000840-$0F00089F (cleared by $A4E0 together with DRAM
$090752A0-$090753E7):

| Address | Purpose |
|---------|---------|
| $0F000840 | 4-byte LSI command packet (DMAC2/3 source) |
| $0F000844-$0F000847 | Status/flag bytes; $0F000846 = signal level shadow word |
| $0F000848 | Interrupt status long (merged into responses by hook 1, then cleared) |
| $0F00084A-$0F00084F | Event enable bytes gating the HIRQ $1000/$2000 asserts |
| $0F000850 | Latched LSI A event word ($FFFFFFFF after reset) |
| $0F000852 | Event mask shadow |
| $0F000854+ | LSI A parameter block (first word $88FE) |
| $0F000884 | LSI B control shadow ($8209) |
| $0F000890-$0F000893 | Subsystem state long ($67818022 after init) |
| $0F000892 | Bit 7 = MPEG active; bits 1/2 set by the LSI A/B packet-DMA completion ISRs (vectors 76/78) |
| $0F000896 | Pending notify bits for task 8 |
| $0F00089C | MPEG mode byte (values 4/5 observed in vector 89) |
| $0F00089E | Pre-init capture edge counter |

## Extension Dispatch Defaults ($F938 indices 0-88)

The firmware's extension/hook mechanism (RAM table at $0907B008, ROM
defaults at $F938, the trampolines and image loader) is described in the
firmware doc. Indices 0-88 default to targets in the MPEG code region; an
extension image clones them into the RAM table and selectively overrides.
Indices 0-31 are the $90-$AF command handlers already listed in the
command table above (index = code - $90). Indices 32-88 are internal MPEG
service points; only the indices below have a ROM default (the rest are
unpopulated until an extension installs a handler):

| Index | Default | Index | Default | Index | Default |
|-------|---------|-------|---------|-------|---------|
| 32 | $A032 | 42 | $ADC8 | 76 | $BAE6 |
| 33 | $A53C | 56 | $B89E | 81 | $C200 |
| 34 | $A500 | 57 | $B8F0 | 82 | $C22E |
| 35 | $AB88 | 58 | $B96C | 83 | $C24C |
| 36 | $AE78 | 59 | $B9A4 | 84 | $C21A |
| 37 | $AE3C | 60 | $B9FC | 86 | $C180 |
| 38 | $AADC | 61 | $B9C2 | 87 | $C266 |
| 39 | $AB08 | 66 | $AE9C | 88 | $B5E2 |
| 40 | $AB44 | 67 | $AEA2 | | |
| 41 | $AD50 | 68 | $AEB4 | | |

(Index 32 = $A032 is task 8's message-loop re-entry, the fall-back an
extension overrides to intercept that task.)

## MPEG Card Authentication ($E0/$E1 subcommand 1, $E2)

These host commands share the disc authentication dispatch ($C2C6/$C3AA
for $E0/$E1, see the firmware doc) but subcommand 1 acts on the cartridge:

- $E0 subcommand 1: requires MPEG hardware ($0F00027D bit 1), sets
  $0907538A = 1, and sends message $09810002 to task 9. Shares the tail
  at $C31E (immediate response = current report, $0F0007B0 bit 0 set).
- $E1 subcommand 1 (MPEG present required): returns the MPEG auth result
  words at $0F0007AC/$0F0007AE.
- Get MPEG Card Boot ROM ($E2, $C344): requires MPEG present, a loaded
  image (byte $0F0002FD), and MPEG active ($0F000892 bit 7). It validates
  the requested address/length against a $07FF window, stages it at
  $0907538C, and sends message $09810004 to task 9.

---

Copyright © 2026 by erings authors is licensed under CC BY-SA 4.0. To view a
copy of this license, visit https://creativecommons.org/licenses/by-sa/4.0/
