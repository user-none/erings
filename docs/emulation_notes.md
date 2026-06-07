# Notes

This is a tracking document of discoveries related to emulation
of the Saturn system that should be known. The goal is to track
game quirks, and minimal system requirements that inform design
and implementation decisions.


# Games

## Waku Waku 7

In the cd block it will call PlayDisc with track-mode start track=0.
This is invalid and need to be ignored.

This happens when transition to the actual fight in game play.
After the fight intro where the opposing character has their
dialog box and after the next loading screen.

## NiGHTS into Dreams

The name input menu flickers without at least partial SH-2 Multiplier
Contention modeling.

# VDP1 rendering

## EDSR.CEF (Current End Flag) and the PTM trigger source

CEF is the bit games poll to synchronize per-frame work with the
VDP1 draw cycle. Its semantics depend on which trigger started the
draw:

- PTM=01 (manual): the SH-2 writes PTMR.PTM=01 to start a draw.
  CEF clears on that write. The next end-bit fetch (or end of the
  command list) sets CEF=1.
- PTM=10 (auto): the draw "starts" at V-Blank-IN per VDP1 manual
  Sec 4.3. CEF clears at V-Blank-IN; the next end-bit sets CEF=1.
- PTM=00 (no trigger) and PTM=11 (invalid): CEF is unchanged by
  V-Blank-IN.

BEF (Before End Flag) latches the prior CEF at every V-Blank-IN
regardless of PTM, per manual Sec 4.6.

ENDR-abort and safety-limit exits do not change CEF; only the
trigger sources and end-bit fetch do.

### Burning Rangers

Uses PTM=01 for its draws. Its V-Blank-IN handler polls EDSR
mid-frame and branches on CEF to decide whether to advance its
work list. The handler depends on CEF=1 persisting across the
V-Blank boundary: if CEF were cleared unconditionally at
V-Blank-IN, the handler never observes the completed-draw state
and its work counter never decrements - the game runs as a
slideshow at roughly 1.4 fps.

This is why the V-Blank-IN CEF clear must be gated on PTM=10
rather than fire unconditionally.

### Legend of Oasis

Uses PTM=10 for the opening FMV. Master polls EDSR with the
two-stage pattern "wait for CEF=0 (draw started), then wait for
CEF=1 (draw ended)" before advancing one decoded frame. The
1->0 transition must be observable to the SH-2 within the same
frame; if it only happens after the SH-2's per-frame cycles end,
master never sees CEF=0 and parks in the first wait-loop, leaving
DISP=0 and an empty bitmap for NBG0.

This is why the CEF clear under PTM=10 has to land at V-Blank-IN
(observable to V-blank-scanline SH-2 cycles) even though the
actual command processing remains deferred to frame end.

### Waku Waku 7

Uses PTM=10 with FCM=1 manual swap, polling EDSR.CEF in its
V-Blank-OUT handler to decide whether to advance the game loop
that frame ("CEF=1 -> request next swap; CEF=0 -> wait"). On
real hardware the V-Blank-OUT IRQ assertion and VDP1's
swap-driven CEF clear race in parallel, and the SH-2 handler
typically wins the race - it reads CEF=1 from the just-completed
prior draw before VDP1 actually clears it.

If our model fires StartAutoDraw (which clears CEF) at top of
RunFrame, the SH-2 has not run any cycles yet and always loses
the race - the handler reads CEF=0, takes the wait path, and
the game's main loop iterates every other frame instead of every
frame. Visible as 30 fps gameplay with audio glitches.

The fix is to defer the PTM=10 gate's StartAutoDraw call to after
segment 0 of scanline 0 inside the per-segment loop, giving the
SH-2 ~one segment of cycles to handle V-Blank-OUT and read CEF=1
before the swap clears it. Drawing still starts at the beginning
of active scanlines for spec correctness; only the CEF-clear
moment is shifted to land after the SH-2's first opportunity to
read it.

## PTMR write timing and deferred-register latch sites

VDP1 manual Sec.3 categorizes PTMR writes as:

- PTM=01: "settings that change immediately"
- PTM=00 and PTM=10: "settings that change with the switching of
  the frame buffer"

PTM=01 writes commit to the active `ptmr` register on the
register write itself. PTM=00 and PTM=10 writes land only in
`ptmrPending`; the active `ptmr` is updated by `latchPending()`
at frame-buffer switch sites. The same latch covers EOS, DIE,
DIL, EWDR, EWLR, and EWRR per the spec's "switching of the frame
buffer" category.

Two latch sites are required:

- V-Blank-IN (`VBlankIn()`): covers the FCM=0 auto-swap path and
  the FCM=1 + FCT=1 path when the swap-arming write lands before
  line 224.
- V-Blank-OUT (`VBlankOut()`): covers the FCM=1 + FCT=1 path when
  the swap-arming write is performed by the V-Blank-OUT handler
  itself at line 0 of the new frame. Without latching at this
  site, the deferred `ptmrPending` value never reaches `ptmr`
  before the same-frame StartAutoDraw gate reads it.

### Sonic Jam (Mega Drive emulator running Sonic 1, 2, 3)

The Sonic Jam wrapper runs a per-frame "game cycle" gated on a
small set of HWRAM flags. The relevant sequence in steady-state
gameplay is:

1. V-Blank-OUT IRQ fires.
2. The wrapper's vbout handler runs at line 0 of the new frame,
   writing `PTMR = 10B` (auto-draw arm) and `FBCR = 0003` (FCM=1
   + FCT=1, manual swap requested).
3. The auto-draw must fire within the same Saturn frame so the
   wrapper's main loop receives the sprite-end IRQ, kicks a
   second manual draw via `PTMR = 01B`, and sets the flag the
   next vbout handler reads.

The V-Blank-OUT latch site exists to make this work. The vbout
handler's `PTMR = 10B` write goes into `ptmrPending`; the
subsequent `VBlankOut()` call latches it into `ptmr` and performs
the FCM=1 + FCT=1 swap, so the line-0 StartAutoDraw gate observes
`ptmr = 2` and triggers the auto-draw on the same Saturn frame.
Without this latch, `ptmr` would retain its previous value
(typically `1` from the prior cycle's manual draw) and the
auto-draw would not fire until the following frame, halving the
inner emulator's framerate.


# IP execution vs direct application loading

The application binary can be loaded and entered directly instead of
running the disc's IP first: read the 1st Read Address from System ID
+$F0, load CD file ID 2 there, set the master PC to that address, and
run. Waku Waku 7, Burning Rangers, and Bulk Slash all run fine this
way.

NiGHTS does not. Its application streams disc data during gameplay
through a CD-block helper reached via WRAM-H slot $060002DC. The IP's
tail relocates a CD-read routine into the workspace at $06000D00 and
rewrites slot $060002DC to point at it, then runs a CD-block
initialization sequence. Loading the application directly skips this,
so slot $060002DC is never set up and the game's disc reads call a
routine that does not exist.

The IP's register cleanup (XOR R0-R14, zero GBR/PR/SR, CLRMAC) runs
before this relocation work, so it is not the final register state at
the application entry; substantial setup code runs afterward.

A BIOS of some kind (real or HLE) is still needed.

# Timing

The Saturn is extremely timing dependant. While there are some
things that can be ignored a large percentage needs to approach
hardware timing.

## Japanese Bios

An issue that was seen with the Japanese bios is related to the
startup sound that plays while the opening animation plays. The
US bios would play fine but the Japanese sound was silent. It
was found that branch operations in the SH-2 where 1 cycle short.
This was the cause of no sound because it introduced a race condition
between shared memory reads with the m68k.

The SH-2 set a value which the m68k needed to read, then the SH-2
would clear that memory location. There isn't any synchronization so
with the 1 cycle short timing the memory would be cleared before
the m68k was able to read it.

## Sonic Jam

This isn't quirks for Sonic Jam but hardware timing requirements uncovered
while getting this game running. These components cannot be short cut.

An accurate SMPC command-dispatch delay based on the delay timing in the
documentation is required.

## CD Block

Games poll the cd block status and need to see the status transition otherwise
they do not believe the final status. You can't speed up CD command timing unless
all of the emulation is sped up. Otherwise, many games won't see command status
change and hang.


## DMA Stall

DMA cannot be too fast and an approximation of region-aware DMA access cycles
is required. The minimal values that have been found to be safe come from
multiple sources.

1. Register values set by the bios at start up
2. The SH-2 documentation
3. The SCU documentation


## SH-2 Bus Access Timing

Accurate SH-2 bus access timing depends on cache emulation. The SH-2 reaches
external memory over a bus with wait states, so an access costs more than a
single cycle. The cost varies by region: work RAM, cartridge (A-Bus), and the
video and sound chips (B-Bus) each respond at different speeds. The on-chip
cache is the deciding factor, since an access that hits the cache is fast
while a miss pays the full external wait state. Because each access cost is
set by that hit-or-miss outcome, the timing cannot be reproduced exactly
without modeling the cache. The net effect is a limit on how many
instructions the SH-2 executes per frame.

This matters because games are written against that real throughput.

Thunderstrike 2 drives its own frame rate: the master SH-2 finishes a frame
of work and then waits for VBlank-IN. At full speed with no wait states the
master finishes far too early, advances every frame, and the game runs at
60 fps instead of its intended 30. Realistic access timing pushes the
master's work past the deadline so it advances every other frame and runs
at 30.

NiGHTS into Dreams works its SH-2 heavily each frame, and the wait states are
part of the real cost of that work. Without them the SH-2 appears to do far
more per frame than the hardware can, and the game runs slower than its
intended rate. Restoring the wait states returns the per-frame work to a
hardware level and the game runs at full speed.


# Game FPS

Games have an effective frame rate that the game itself drives.
While the emulator runs at 60 fps, a game may draw only every
other frame and run at an effective 30 fps. The BIOS intro
animation does this and runs at 30 fps. Menus typically run at
60 fps and static loading screens at 0 fps.

Expected gameplay effective fps for various games:

Name | ID | fps
---- | -- | ---
BULK SLASH | T-14310G | 30
BURNING RANGERS | MK-81803 | 20
NIGHT WARRIORS | T-1208H | 60
NiGHTS | MK-81020 | 30
SONIC JAM | MK-81079 | 60
WAKUWAKU7 | T-1515G | 60
