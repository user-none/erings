# BIOS Sound Driver

The BIOS carries a complete MC68EC000 sound driver as part of the
compressed package at ROM $01D000 (see
[bios_decompression.md](bios_decompression.md) and
[rom_layout.md](rom_layout.md)). sub_15D4 decompresses the package to
WRAM-H, uploads it into sound RAM, and starts the 68k. The driver then
runs autonomously on the sound CPU: it scans a command area in sound
RAM that the host (SH-2) writes, plays tones and sequences through the
SCSP, manages DSP effect programs, and routes CD-DA.

The driver identifies itself with the embedded text `ver1.11 94/11/03`
and `BOOT ROM(S) V2`, placed immediately after the reset entry point
(the entry branches over it).

All addresses and behavior in this file were traced from the USA BIOS
(`BTR_1.000U941115`). The sound driver region differs between the USA
and JP images (see [identification.md](identification.md)); the JP
variant has not been traced.

All addresses in this file are 68k-side sound addresses: sound RAM at
$000000-$07FFFF, SCSP registers at $100000-$100EE3. The SH-2 sees the
same bytes at $25A00000 (sound RAM) and $25B00000 (registers).

## Upload and start (SH-2 side)

sub_15D4 ($0015D4), called from the boot state machine on all
cold-boot paths past the HEMU/HCGG early-outs:

1. Decompresses ROM $1D000 to the $06010000 staging buffer (sub_1F04).
2. SMPC SNDOFF (COMREG $07), then a fixed delay.
3. Sets SCSP MEM4MB (byte $02 to register $100400 high byte): 512 KB
   sound RAM mode.
4. Zero-fills sound RAM $0-$AFFF with per-longword read-back verify.
5. Copies the 68k program ($5180 bytes to $000000) and the area map
   ($80 bytes to $00A000), each longword verified.
6. SMPC SNDON (COMREG $06): the 68k comes out of reset and runs the
   driver from the reset vector (SSP $A000, PC $1000).
7. Posts command $08 (select area map set 0) into the slot at $700.
8. Copies the remaining data blocks while the driver initializes:
   $010000 (tone data), $018000 and $01C000 (sequence data), and four
   DSP effect banks at $020000/$022000/$024000/$026000.
9. Writes $80 into byte 4 of each of the 8 area-map records of the
   working copy at $500+8n (sets the flag bit in each record's size
   longword).
10. Posts commands $80 and $82 with a fixed delay before each (see
    "Boot command sequence").

## Sound RAM layout (driver's view)

| Address | Size | Contents |
|---------|------|----------|
| $000000 | $100 | 68k exception vectors |
| $000400 | $100 | Host flag / communication area (see "Host flags") |
| $000500 | $100 | Area map, working copy: 32 records x 8 bytes |
| $000600 | $100 | Host parameter block; word +$44 = DSP work-area spec |
| $000700 | $80 | Host command slots: 8 slots x 16 bytes |
| $000800 | $800 | Command history ring: 16-byte entries |
| $001000 | ~$4180 | Driver code (reset entry $1000) |
| $003800 | $100 | Driver status block (default position; the whole sequencer region moves with $450, see "Sequencer interrupt") |
| $003900 | $300 | Sequencer track blocks: 8 tracks x $60 bytes (status block + $100) |
| $003C00 | $180 | Gate records: 32 x $C bytes (status block + $400) |
| $006000 | $1000 | Control blocks: 8 banks x 32 channels x 16 bytes |
| $007000 | $800 | Voice state: 32 voices x $40 bytes |
| $007800 | $200 | Driver globals and tables (see below) |
| $00A000 | - | Initial supervisor stack top; area map original at $A000-$A07F |
| $010000 | $8000 | Tone bank (area map id $00) |
| $018000 | $4000 | Sequence data (area map id $10) |
| $01C000 | $4000 | Sequence data (area map id $11) |
| $020000 | $8000 | Four DSP effect bank images at $020000/$022000/$024000/$026000; the boot area map describes only the first (id $20, size $2000) |

Driver globals at $7800 (the driver keeps A6 = $6000 and addresses
these as $18xx(A6)):

| Address | Use |
|---------|-----|
| $7804 | DSP work-area pointer (from the $600 block, word +$44) |
| $7815 | Voice-handling mode of the current note-on (program flags bits 4-6) |
| $781A | Sequencer tempo accumulator (+= $A0 per timer B interrupt) |
| $7820/$7822 | Voice allocator rotors (offsets into the $78C0 table) |
| $7824/$7828 | Layer counters of the current note-on |
| $7827 | Tick counter (one per timer A period) |
| $782A | Reserved-voice budget for the allocator (init $20) |
| $782C | Pointer to the loaded effect bank's per-channel parameter records |
| $7830 | Dynamic-parameter channel count (effect bank header +$22); nonzero enables the $1B7E update every 4th tick |
| $7831 | Mono-switch shadow (last $483 value) |
| $7840 | Internal event ring write index (bytes) |
| $7841 | Internal event ring read index (bytes) |
| $7842 | Main loop pass counter |
| $7848 | Current tone bank number (set by command $87) |
| $784C | Tone bank header pointer (initialized from control block 0) |
| $786C | Mixer-map shadow copy (18 bytes, alongside $787E) |
| $787E | Effect-send shadow: 18 bytes, one per slot 0-17 MIXER low byte (commands $80/$81/$88); entries 16/17 are the CD-DA left/right values |
| $7890 | PCM pair table: 8 pairs x 2 voice numbers (defaults 0,1 / 2,3 / ... / 14,15); bit 7 = pair active (commands $85/$86/$8A) |
| $78C0 | Voice offset table: 32 words, voice-block offsets grouped by tick group |
| $7900 | Internal event ring: 64 entries x 4 bytes |
| $7A00 | Layer match list built during note-on (8 bytes per matched layer) |
| $7B00 | Dynamic effect-parameter work list ($10 bytes per channel) |

## Exception vectors

| Vector | Handler | Behavior |
|--------|---------|----------|
| Reset PC | $1000 | Driver entry |
| Bus error | $14E2 | JMP $1000 (restart) |
| Address error | $14D4 | JMP $1000 (restart) |
| Illegal / all other traps | $14DC | JMP $1000 (restart) |
| Level 2 autovector | $13A0 | Timer B: sequencer step |
| Level 5 autovector | $139C | JMP $1000 (restart) |
| Level 6 autovector | $1398 | JMP $1000 (restart) |
| Level 3/4/7 autovectors | $14DA | RTE (ignored) |

Any fault restarts the driver from scratch, and levels 5 and 6
restart it as well.

## Initialization (reset entry $1000)

```
disable interrupts (SR = $2700)
clear D0-D7/A0-A4
A6 = $6000 (work RAM base)
A7 = $A000 (stack)
A5 = $100000 (SCSP register base)

scsp_init:                          ; $321E
    MEM4MB = 1                      ; byte $02 -> $100400
    clear DSP registers $100700-$100BFF (COEF, MADRS, MPRO)
    clear all 32 slot register sets

clear work RAM $6000-$9FC3;         ; $32B2
build voice offset table at $78C0
and the PCM pair table at $7890
clear command slots $700-$73F       ; $31B0 (first 64 bytes only)
select area map set 0:              ; $1442 with D0 = 0
    copy 32 records $A000 -> $500
    clear DSP registers again

init control blocks:                ; $31C0
    for bank in 0..7:
        for channel in 0..31:
            block = $6000 + (bank*32 + channel)*16
            block.tone_bank_ptr = address of area-map record type 0 id 0
            block.program = 0

clear event ring indices; cache tone bank header pointers  ; $13D8/$140E
MEM4MB = 1 (again)
enable interrupts (SR = $2000)
fall into main loop
```

## Main loop

The main loop is paced by SCSP timer A, polled through SCIPD (no
interrupt). Timer A is reloaded with $00A7 every tick: prescale 0,
count-up from $A7 with the interrupt raised at $FF, so one tick every
88 samples (about 2.0 ms / 501 Hz at 44.1 kHz).

```
loop:
    pass_counter++                        ; $7842
    if ring_read != ring_write:           ; $7841 vs $7840
        dispatch_one_event()              ; $1EA4, see "Internal event ring"
    scan_command_slots()                  ; $11D6, see "Host command interface"
    if SCIPD bit 6 (timer A) not pending: goto loop

    ; -- timer tick --
    TIMA = $00A7; SCIRE = $0040           ; reload + acknowledge
    tick++                                ; $7827
    update_voices()                       ; $116C: 8 of the 32 voice slots
                                          ; per tick, group = tick & 3
    if (tick & 3) == 0 and effect channels active ($7830):  ; $1154
        dynamic effect-parameter update                     ; $1B7E
    d = byte[$483] ; mono switch
    if (d ^ shadow) bit 7 changed:        ; $7831
        shadow = d
        if d bit 7 set:
            slots 0-17: clear DIPAN and EFPAN (levels kept)
            slots 18-31: clear DIPAN only
            (pans forced center = mono output)
    goto loop
```

## Sequencer interrupt (level 2, timer B)

Timer B runs as a level-2 interrupt. Each interrupt reloads TIMB with
$01D4 (prescale 1, count-up from $D4 to $FF: 86 samples, about
2.0 ms) and advances the sequencer:

```
level2_handler:                     ; $13A0
    if SCIPD bit 7 (timer B) pending:
        TIMB = $01D4; SCIRE = $0080
        tempo_accumulator += $A0    ; $781A
        sequencer_step()            ; $3E70
    RTE
```

Sequence playback (note scheduling from the sequence data banks) runs
entirely in this interrupt context; the main loop only feeds it
commands and updates voice envelopes.

All sequencer code addresses its state through bases set up by $46E2
from the longword at $450 (zero at boot):

```
A4 = [$450] + $3800   ; status block
A6 = A4 + $100        ; 8 track blocks x $60 bytes
A5 = A4 + $400        ; 32 gate records x $C bytes
```

The full $450-relative region:

| Offset | Size | Contents |
|--------|------|----------|
| +$1F00 | $20 | Per-track song base pointers (8 longs) |
| +$1F20 | $1000 | Channel-stream states: 8 tracks x 32 channels x $10 |
| +$2F20 | $400 | Next-event time arrays: 8 tracks x 32 longs |
| +$3320 | $400 | Loop copies of the next-event times |
| +$3720 | $50 | Conductor-stream states: 8 tracks x 10 bytes |
| +$3800 | $100 | Status block |
| +$3900 | $300 | Track blocks: 8 x $60 |
| +$3C00 | $180 | Gate records: 32 x $C |

With $450 = 0 (the boot value) the stream-state arrays at +$1F20
onward would overlay the driver code, so sequence playback requires
the host to set $450 first; $6000 is the value that also lines the
relocatable references up with the fixed work-RAM tables (see "Host
flags").

Every 8th interrupt the handler additionally copies the SCSP monitor
registers ($100EE0/$100EE2/$100ED2-$100ED6) into the host monitor
block at [$404]+$90 ($4600), and scans the PCM pair table, writing
per-pair status to [$404]+$A0. If a pair event occurred and the host
has armed the block at [$412] (byte 0 bit 7), the driver stores the
event there and writes $0020 to MCIPD ($10042A) - raising bit 5, the
Sound Request interrupt to the SH-2 (SCU vector 70,
[scu_interrupt_handling.md](scu_interrupt_handling.md)) ($4670).

Each track block runs a state machine (state byte at +2: 0 = idle,
1/2 = playing, 3/4 = the paused counterparts of 1/2); the sequencer
commands below dispatch on the current state.

The per-track step ($42D4, run every interrupt for each of the 8
tracks) uses standard-MIDI-style timing: tempo in microseconds per
quarter note, a division (pulses per quarter note) from the song
header, and a nominal 2000 us per interrupt:

```
track_step:
    if not running (flags bit 7) or paused (bit 6): return
    tempo_countdown -= 2000                        ; +$1C
    if expired:
        fetch from the conductor stream (type 3)
        validate 200000 <= tempo <= 1499999 us/quarter
            (out of range: stop the track, status $83)
        step_period = tempo / division             ; +$14 and +$30
        tempo_countdown = delta * step_period
    if fading (bit 4), every 50 interrupts (+$35):
        step fade level (+$24) by +$26 toward the target (+$22);
        reaching a $7F00 target stops the track
    every 50 interrupts (+$34): increment the measure counter
        at [+$38] (a word in the host monitor block)
    if end-of-data (bit 0): return
    event_countdown -= 2000                        ; +$18
    while event_countdown went negative:
        dispatch the pending event (+$2C..+$2F) via $48C0
        if it was a note on: allocate a gate record ($4874)
            with the gate time scaled by step_period; +$20 pending++
        fetch the next event (type 1)
        on end-of-stream: set bit 0; the track goes idle once the
            pending-note count drains
        event_countdown += event delta * step_period
```

$48C0 converts an event to an internal-ring entry: note-on velocities
are reduced by the track volume (+$22, or the fade level +$24 while
fading) and the ring entry's bank field is the track number. Gate
records ($C bytes at A5: channel, note, velocity, remaining
microseconds, track pointer) are counted down every interrupt
($4532); expiry emits the matching note-off through the same path.
When the host has enabled event tracing (status block byte 0 bit 0),
every fetched event is also copied as a 16-byte record to the trace
buffer pointer at status block +$10 ($49B2).

## Host command interface

The host writes commands into 8 fixed slots of 16 bytes at
$700-$77F. Slot byte 0 is the command number; byte 1 is unused by the
dispatcher; arguments start at byte 2. A slot is pending while byte 0
is nonzero. The driver scans all 8 slots every main-loop pass:

```
scan_command_slots:                 ; $11D6
    for slot in $700, $710, ... $770:
        cmd = byte[slot]
        if cmd == 0: continue
        log 16 slot bytes into the history ring at $800
        execute(cmd, args at slot+2)
        word[slot] = 0              ; mark consumed; host may poll this
```

Every accepted command is also copied into the history ring at
$800-$FFF (16-byte entries; the write offset lives in the driver
status block, see "$450" under "Host flags").

### Command reference

Commands $01-$0C are sequencer commands; their handlers re-base onto
the $450-relative state (see "Sequencer interrupt"). Commands $80-$8A
are direct SCSP/driver operations. Byte values $0D-$7F and >= $91 are
ignored. Track-targeted commands take the track number in arg byte 2
(low 3 bits) and dispatch on that track's state.

| Cmd | Handler | Operation |
|-----|---------|-----------|
| $00 | - | No-op |
| $01 | $159A | Start sequence on a track: bank id (byte 3) and song number (byte 4) are validated via $1B34 (map type $10 lookup, count check, per-song offset table); byte 5 low 5 bits become the track start parameter (+$04). Rejected (carry set) on a bad bank/song. Restarts the track if it is already playing. |
| $02 | $15B4 | Stop track (byte 4 = parameter): valid in any playing state ($416C) |
| $03 | $15BE | Pause track: playing states 1/2 move to paused states 3/4, the track hold bit (flags bit 6) is set, and the track's voices are keyed off ($4144/$414C) |
| $04 | $15C4 | Resume track: paused states 3/4 move back to 1/2 and the hold bit is cleared ($4192/$419A) |
| $05 | $15CA | Set track volume: byte 3 = 7-bit level (stored inverted, $7F - level, at track +$22), byte 4 = ramp time (0 = immediate, nonzero = gradual, path depends on the track state) |
| $06 | $1594 | Rejected (error return) |
| $07 | $15D4 | Track tempo scale: signed word argument d (bytes 4-5). d >= 0: step period = base * $1000 / ($1000 + d); d < 0: step period = base * ($1000 - abs(d)) / $1000. Base period is track field +$30, result goes to +$14. |
| $08 | $15DE | Select area map set: arg byte 2 -> $1442, which copies that map set from $A000 to the working copy at $500 and clears the SCSP DSP registers |
| $09 | $15E8 | Enqueue: copies slot bytes 2-5 into the internal event ring with interrupts masked. The deferred event is dispatched from the main loop. |
| $0A | $3EF6 | Sequencer master ON (sets bit 7 of status block +2) |
| $0B | $3F04 | Sequencer master OFF (clears bit 7 of status block +2) |
| $0C | $1618 | Start sequence, mode-flag variant: same arguments and validation as $01, enters the same begin path with a mode flag set ($3DAA, D6 = $80) |
| $80 | $1634 | CD-DA send level: arg bytes 2,3 = left,right; the top 3 bits of each land in EFSDL of SCSP slots 16/17 MIXER registers ($100217/$100237). EXTS0/EXTS1 (CD-DA left/right) route through these slots' mixer settings. Forced to level-only (pan cleared) while mono is active. |
| $81 | $1678 | CD-DA pan: same registers, low 5 bits = EFPAN. Cleared while mono is active. |
| $82 | $16BC | Master volume: arg byte 2 low nibble -> MVOL ($100401). Echoed into the driver status block bytes +8/+9. |
| $83 | $16DC | Load DSP effect bank: arg byte 2 < $10 selects area-map id $2x; the bank's DSP program is loaded into the SCSP DSP ($2766, see "DSP effect bank format") |
| $84 | $16E4 | No-op |
| $85 | $16E6 | Start PCM stream pair: arg byte 2 selects a $7890 pair (low 3 bits) and a mode bit; the pair's two voices are marked active and their SCSP slot registers are written directly, with the start address taken from the word at bytes 4-5 shifted left 4. Left/right voices play a raw stereo stream. |
| $86 | $192E | Stop PCM stream pair: clears the pair's active bits, writes a stop marker into each voice block and a release command into its voice-command entry |
| $87 | $1972 | Select tone bank: map type-0 id (byte 2) and tone number (byte 3) become the current bank ($784C pointer, $7848 number); the tone header caches are rebuilt ($13D8/$140E) |
| $88 | $198E | Set effect send for one slot: byte 2 = slot index (0-17), byte 3 = EFSDL/EFPAN byte, written to the slot MIXER register low byte and shadowed at $787E+index |
| $89 | $19AC | Diagnostics, sub-command in byte 2: 0/1 = sound RAM test over the bank area (two sizes), 2 = MIDI loopback test ($55/$AA out MOBUF, compare via MIBUF), 3-5 = further test entries, 6/7 = no-op. The 16-bit result is written to $418. |
| $8A | $18A6 | PCM stream pair volume: byte 2 selects the pair, byte 3 top 3 bits = DISDL, written to both voices' MIXER high bytes with left/right pan (pan forced center when mono) |
| $8B-$8F | - | No-op |
| $90 | $1634 | Decodes as $80: the >= $91 reject comes first and the handler index uses only the low 4 bits |

## Internal event ring

The ring at $7900 (64 entries x 4 bytes; write index $7840, read
index $7841) carries MIDI channel events. Two producers feed it: host
command $09 and the sequencer's event dispatcher ($48C0). The event
number is the MIDI status high nibble masked to 3 bits, so the ring
dispatch is a MIDI channel-message dispatch. The main loop consumes
one event per pass:

```
dispatch_one_event:                 ; $1EA4
    event = ring[$7900 + read_index]; read_index += 4
    bank  = event[1] >> 5           ; 3 bits (sequencer: track number)
    chan  = event[1] & $1F          ; 5 bits
    block = $6000 + (bank*32 + chan) * 16
    args  = event[2], event[3]
    jump on event[0] & 7:
        0 ($8x) -> $2EE4  note off / note-stack retrigger
        1 ($9x) -> $290E  note on; velocity 0 is treated as note off
        2 ($Ax) -> RTS    polyphonic aftertouch (ignored)
        3 ($Bx) -> $1F2C  control change: the controller number
                          dispatches through 8 x 16 setter tables at
                          $1F40; controller 7 (volume, $2628) stores
                          the inverted level as the channel
                          attenuation and live-updates the TL of the
                          channel's sounding voices
        4 ($Cx) -> $1F00  program change (validated against the tone
                          bank's program count)
        5 ($Dx) -> RTS    channel aftertouch (ignored)
        6 ($Ex) -> $2870  pitch bend: 14-bit value centered at $2000;
                          the bend range shift comes from the
                          channel's program data
        7 ($Fx) -> RTS    system messages (ignored)
```

Each control block keeps a 3-deep note stack (bytes +$A/+$B/+$C:
current, next, pending). Note-off ($2EE4) removes a note from the
stack; if the sounding note was removed, the next stacked note is
retriggered through the note-on path ($290E).

### Note on

Note on ($290E) resolves the channel's program from the tone bank,
collects the program layers whose key range contains the note (up to
32), and assigns each to a voice. Voice allocation prefers a
same-channel same-note voice (replacing it with a keyed-off KYONEX
write) and otherwise takes voices from the rotating allocator over
the $78C0 offset table. For each assigned voice the layer image is
written to the SCSP slot registers, TL is computed from the velocity
curve plus the channel attenuation, OCT|FNS from the note (see "Tone
bank format"), and a final KYONEX+KYONB write keys the voice on.

## Tone bank format

A tone bank (area map type 0) is relocatable: all internal offsets
and the sample addresses inside layers are relative to the bank base.

| Offset | Contents |
|--------|----------|
| +$00 | Word: offset of the mixer-map table; also bounds the program offset table, so program count = (word[+0] - 8) / 2 |
| +$02 | Word: offset of the velocity-curve table (and end of the mixer maps) |
| +$04 | Word: offset of the pitch-envelope table |
| +$06 | Word: offset of the volume-envelope table |
| +$08 | Words: per-program offsets |

Mixer maps (18 bytes each, selected by command $87's second
argument): default EFSDL/EFPAN bytes for slots 0-17, copied to the
$787E/$786C shadows and written to the slot MIXER registers.

Velocity curves (10 bytes each, selected per layer by byte +$1D): a
piecewise velocity-to-level mapping. The record is a leading control
byte followed by three {base, operand, control} triplets; each
triplet's base byte doubles as its velocity threshold, and the last
segment whose base is below the velocity wins (velocity minus that
base feeds the operator). The control byte's low 3 bits select one
of 8 curve operators (add, inverted add, shift-scaled variants) and
its high nibble is the shift count. The 7-bit result scales the
layer's base total level: TL = NOT(NOT(layerTL) * result / 256) +
channel attenuation, saturating at $FF, written to the slot TL byte.

Pitch envelopes (10 bytes each, selected per layer by byte +$1E when
layer flag bit 23 is set): rate/target records run by the per-tick
voice update; voice block fields +$20/+$2C/+$2E/+$30 hold the state.

Volume envelopes (4 bytes each, selected per layer by byte +$1F when
layer flag bit 22 is set): a software envelope applied on top of the
SCSP hardware EG; voice block fields +$10 to +$1E hold the state.
Envelope byte values pass through the time scaler at $39BE.

A program:

| Offset | Contents |
|--------|----------|
| +$00 | Flags: bits 4-6 = voice-handling mode (nonzero enables the same-note replacement scan; bit 5 adds loop-control bits at key-on) |
| +$02 | Byte: layer count - 1 (bit 7 set = invalid program) |
| +$04 | Layer records, $20 bytes each |

Layer record ($20 bytes) - most of it is a direct SCSP slot register
image; the top three bits of the first register word are unused by
the hardware and carry driver flags:

| Offset | Contents |
|--------|----------|
| +$00 | Key range low (note must satisfy low <= note <= high) |
| +$01 | Key range high |
| +$02 | Long: slot registers $00-$03 (sample/loop control + start address, bank-relative; the driver adds the bank base). Bit 23 = pitch-envelope enable, bit 22 = volume-envelope enable, bit 21 = force the velocity curve (when clear and the layer has no IMXL and no DISDL, TL comes straight from +$0F with no velocity processing) |
| +$06 | Long: slot registers $04-$07 (LSA, LEA) |
| +$0A | Long: slot registers $08-$0B (envelope generator) |
| +$0E | Byte: slot register $0C high byte; bit 7 set also masks the LFO word with $FF18 |
| +$0F | Base total level (TL) |
| +$10 | Word: slot register $0E (modulation); nonzero also enables the key-on LFO computation from +$1B/+$1C |
| +$14 | Word: slot register $12 (LFO) |
| +$16 | Word: slot register $14 (ISEL/IMXL) |
| +$18 | Direct-out DISDL/DIPAN default (slot register $16 high byte); the control block pan override (+1) and the mono switch modify the pan field |
| +$19 | Base key |
| +$1A | Signed fine tune |
| +$1B/+$1C | LFO depth parameters (scaled through $2BA2) |
| +$1D | Velocity curve index |
| +$1E | Pitch envelope index |
| +$1F | Volume envelope index |

Pitch: the driver computes note + $60 - base key, plus fine tune and
any pitch-envelope offset, then splits it through an octave table at
driver address $32FE and an FNS table at $33BE into the OCT|FNS word
for slot register $10.

## Sequence data format

A sequence bank (area map type $10) begins with a 16-bit song count
and one 32-bit per-song offset. A song holds up to 32 channel
streams plus a conductor (tempo) stream; all offsets are relative to
the song base:

| Offset | Contents |
|--------|----------|
| +$00 | Word: division (pulses per quarter note), validated 24-960 |
| +$02 | Byte: stream count |
| +$04 | Byte: initial conductor state value |
| +$08 | Word: conductor stream offset |
| +$0A | Words: per-channel stream offsets (0 = channel unused) |

The conductor stream is {32-bit tempo in microseconds per quarter
note, 32-bit delta} pairs; the default tempo before the first entry
is 500000 (120 BPM). Channel streams are merged by time: each keeps
its own delta clock in division pulses, and the fetcher returns the
earliest pending event across channels, advancing all clocks by that
delta ($4A42).

Each channel event starts with one byte:

- $00-$7F: a note event; the byte is a flag field over per-channel
  running state:
  - bit 0: reuse the previous note, else a note byte follows
  - bit 1: reuse the previous velocity, else a velocity byte follows
  - bit 2: reuse the previous delta, else a delta byte follows
    (bit 3 = +256 on the new delta)
  - bit 4: reuse the previous gate time, else a gate byte follows
    (bits 5-6 = high bits x8 of the new gate)
- $80-$BF: control opcodes, dispatched through the table at driver
  address $505A:

| Opcode | Operation |
|--------|-----------|
| $81 | Skip one stream byte, clear the gate accumulator |
| $82 n | Repeat: process the previous opcode byte (state +$0A) for the next n events; their data bytes still follow in the stream |
| $83 hi lo n | Pattern call: jump to channel-stream start + 16-bit offset; the end opcode returns to the saved position after n passes |
| $84 d | Loop marker: first occurrence records the loop point, the next returns to it; each occurrence emits controller $1F to the channel and advances the clock by d |
| $8E | End of this channel: its next-event time becomes $FFFFFFFF and the fetch re-merges the remaining channels |
| $91 n v | Polyphonic aftertouch ($Ax) |
| $92 p | Program change ($Cx) |
| $93 v | Channel aftertouch ($Dx) |
| $94 l m | Pitch bend, two bytes ($Ex) |
| $95 m | Pitch bend, MSB only |
| $A0 c v | Control change, any controller ($Bx) |
| $A1 v - $A6 v | Fixed control changes: controllers 1, 2, 4, 7, $A, $B |
| $B0-$B7 | Add a fixed delta: $200, $400, $600, $800, $A00, $C00, $E00, $1000 (table at $503A) |
| $B8-$BF | Add a fixed gate time: $100, $200, $400, $600, $800, $1000, $1800, $2000 (table at $501A) |
| all others in $80-$BF | Stop the track (status $FF) |

- >= $C0: stop the track immediately - its voices are killed and the
  track status byte (+$03) is set to $FF. A song ends gracefully by
  ending every channel with $8E; the track then sets end-of-data and
  goes idle once the last gated note expires.

## DSP effect bank format

A DSP effect bank region (area map type 2) must be $2000-aligned;
command $83 rejects a record whose address has any of the low 13 bits
set ($27C8). The bank starts with a header whose traced fields are:

| Offset | Contents |
|--------|----------|
| +$20 | Bits 0-1: DSP program size code (expected size $4000 << n + $40, plus +$21 * $A00) |
| +$21 | Additional size pages |
| +$22 | Dynamic-parameter channel count -> $7830 |

Loading a bank copies its microprogram and coefficients into the SCSP
DSP registers and records the bank's per-channel parameter records
(24 bytes each) at $782C. While $7830 is nonzero, the main loop runs
the dynamic-parameter update ($1B7E) every 4th tick over the work
list at $7B00, and note-on events for matching channels refresh the
list - effect parameters that follow the notes. The DSP work area
written by the update sits at the pointer $7804 (see host flag $410).
The first bank image carries the embedded name `test.EXB`.

## Area map

The 8-byte records uploaded to $A000 (and copied to $500 at init)
describe where each data region lives in sound RAM:

```
byte 0      id: bit 7 = end-of-map marker,
            bits 6-4 = type (0 = tone bank, 1 = sequence data,
                             2 = DSP effect bank),
            bits 3-0 = number within the type
bytes 0-3   longword; low 20 bits = sound RAM address
bytes 4-7   longword; low 20 bits = region size,
            byte 4 bit 7 = flag set by the BIOS after upload
```

The BIOS-uploaded map has four records:

| id | Address | Size | Contents |
|----|---------|------|----------|
| $00 | $010000 | $8000 | Tone bank 0 |
| $10 | $018000 | $4000 | Sequence data 0 |
| $11 | $01C000 | $4000 | Sequence data 1 |
| $20 | $020000 | $2000 | DSP effect bank region |

Command $83 searches the working map for id $2x and loads that
record's contents into the SCSP DSP. The first effect bank carries the
embedded name `test.EXB`. The three additional bank images uploaded at
$022000/$024000/$026000 are not described by the boot map. $1442
reloads the working copy at $500 from the original at $A000; it runs
with set 0 at init and on host command $08.

A sequence data bank (type $10) starts with a 16-bit song count
followed by one 32-bit offset per song (relative to the bank base);
the song data follows. $1B34 performs this lookup when validating the
start-sequence commands.

## Driver data structures

Control block ($10 bytes, at $6000 + (bank*32 + channel)*$10; bank =
event byte 1 bits 5-7, channel = bits 0-4):

| Offset | Contents |
|--------|----------|
| +$00 | Program number |
| +$01 | Pan override: bit 7 set = low 5 bits replace the layer's DIPAN |
| +$02 | Channel volume attenuation, added to voice TL (controller 7 stores NOT(level) * 2) |
| +$03 | Flags (bits 6/7 used by the note on/off paths) |
| +$04 | Long: tone bank pointer |
| +$08 | Word: cleared on program change |
| +$0A/+$0B/+$0C | Note stack: current, next, pending |
| +$0D | Velocity of the current note |

Voice block ($40 bytes, at $7000 + voice*$40, 32 voices):

| Offset | Contents |
|--------|----------|
| +$00 | Flags: bit 5 = software volume envelope active, bit 6 = pitch envelope pending |
| +$01 | Channel byte (event byte 1) |
| +$02 | Note |
| +$03 | Velocity |
| +$06 | Word: control-block group (bank << 9) |
| +$08 | Long: layer address |
| +$0C | Long: pitch-envelope accumulator |
| +$10/+$14 | Longs: software envelope level / current |
| +$18 | Word: software envelope rate |
| +$1A/+$1C/+$1E | Words: software envelope parameters |
| +$20 | Word: pitch envelope current value |
| +$2C | Word: pitch envelope rate |
| +$2E | Byte: pitch envelope phase |
| +$30 | Long: pitch envelope record pointer |
| +$34 | Owner/status (bit 7 + owner id; PCM pairs mark $80 or pair number) |

Track block ($60 bytes, status block + $100 + track*$60, 8 tracks):

| Offset | Contents |
|--------|----------|
| +$00 | Flags: bit 7 running, bit 6 paused, bit 4 fading, bit 0 end-of-data |
| +$01 | Event bank id (the track number, merged into ring events) |
| +$02 | State: 0 idle, 1/2 playing, 3/4 paused |
| +$03 | Status code: 0 = running, $83 = tempo out of range, $FF = stream stop opcode |
| +$04 | Start parameter (command $01 byte 5, low 5 bits) |
| +$06 | Word: division (PPQN, from the song header) |
| +$08 | Long: tempo (microseconds per quarter note) |
| +$0C | Long: conductor delta |
| +$10 | Long: song base address |
| +$14 | Long: current step period (microseconds per pulse) |
| +$18 | Long: event countdown (microseconds) |
| +$1C | Long: tempo countdown (microseconds) |
| +$20 | Pending gated-note count |
| +$21 | Mode flags (bit 7 = deferred start, command $0C) |
| +$22 | Track volume ($7F - level); fade target |
| +$24 | Fade current level |
| +$26 | Fade step |
| +$28 | Long: gate time of the pending note (microseconds) |
| +$2C-$2F | Pending event: channel bit 4, status, data 1, data 2 |
| +$30 | Long: base step period (command $07 rescales +$14 from this) |
| +$34/+$35 | Divide-by-50 counters (measure counter / fade) |
| +$38 | Long: measure counter pointer ([$404]+$B0+track*2) |
| +$3C | Long: track status pointer ([$404]+$80+track*2) |

Status block ($100 bytes at [$450]+$3800):

| Offset | Contents |
|--------|----------|
| +$00 | Bit 0 = event trace enable |
| +$01 | Bit 7 = sequencer tick re-entry guard |
| +$02 | Bit 7 = sequencer master run flag |
| +$03/+$04 | Divide-by-8 tick counters (monitor copy / PCM pair scan) |
| +$08/+$09 | Master volume echo (command $82); +$08 also gates deferred track starts |
| +$10 | Long: event trace write pointer |
| +$14 | Long: command history ring write offset |

Gate record ($C bytes, status block + $400, 32 records):

| Offset | Contents |
|--------|----------|
| +$00 | Bit 7 = active; bit 0 = channel bit 4 |
| +$01 | Event status byte |
| +$02 | Note |
| +$03 | Velocity |
| +$04 | Long: remaining gate time (microseconds) |
| +$08 | Long: track block pointer |

Channel stream state ($10 bytes, [$450]+$1F20 + track*$200 +
channel*$10):

| Offset | Contents |
|--------|----------|
| +$00 | Word: current stream position (offset from song base) |
| +$02 | Word: loop return position (opcode $84) |
| +$04 | Word: pattern-call return position (opcode $83) |
| +$06 | Long: last delta |
| +$0A | Previous opcode byte (replayed by opcode $82) |
| +$0B | Last note |
| +$0C | Last velocity |
| +$0D | Repeat count |
| +$0E | Pattern-call pass count |

## Host flags ($400 area)

All pointers below are host-written longwords; the driver never sets
them (they are zero after the boot RAM clear).

| Address | Use |
|---------|-----|
| $404 | Pointer to a host monitor block: per-track status words at +$80, SCSP monitor registers copied to +$90 and per-PCM-pair status to +$A0 every 8th sequencer tick, per-track measure counters at +$B0 |
| $408 | Pointer to a 256-byte dump destination: a sequencer-side path ($3F62) copies a record located through the catalog at [$448] to this address |
| $410 | Bit 7 = host request: reconfigure the DSP work area from the $600 block. The driver computes base = (word[$644] & $7F) * $2000 plus $2000 << (((word[$644] >> 7) & 3) + 1), stores it at $7804, clears the first 64 bytes, and clears bit 7. |
| $412 | Pointer to a host notification block: byte 0 bit 7 arms it. On a PCM pair event the driver writes the event to +2, sets +1 to $01, and raises MCIPD bit 5 - the Sound Request interrupt to the SH-2 (SCU vector 70). |
| $418 | Result word of the command $89 diagnostics (RAM / MIDI loopback tests) |
| $448 | Pointer to a record catalog walked by $46FE ($80-flagged, 8-byte-stepped entries); source selector for the $408 dump |
| $450 | Base of the driver's relocatable data block (see the region table under "Sequencer interrupt"). The status block sits at base+$3800 and the PCM pair table is also read at base+$1890, so consistent operation expects the host to set $450 = $6000 (matching the fixed tables in work RAM); it is zero at boot, and sequence playback is unusable until the host sets it because the stream-state arrays would overlay driver code. The command-history ring stays fixed at $800-$FFF. |
| $483 | Bit 7 = mono switch. On a 0->1 change the driver forces pans center: DIPAN and EFPAN cleared on slots 0-17, DIPAN cleared on slots 18-31; commands $80/$81 also suppress pan while set. Driven by the system-settings MONO option. |

## Boot command sequence

The BIOS posts three commands into the first three slots (each
written as one longword; the remaining slot bytes are zero from the
earlier RAM clear). The first is posted right after SNDON, before the
data banks finish uploading; the other two follow the upload and the
record-flag writes, each preceded by a fixed delay:

| Slot | Longword | Effect |
|------|----------|--------|
| $700 | $08000000 | Select area map set 0 (recopy $A000 -> $500, clear the DSP) |
| $710 | $8000E0E0 | CD-DA send level: left $E0, right $E0 (maximum) |
| $720 | $82000000 | Master volume 0 |

CD-DA routing is opened at full send level while the master volume is
left at minimum; the CD player application raises MVOL when it starts
playback.
