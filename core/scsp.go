// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"math"

	m68k "github.com/user-none/go-chip-m68k"
)

const (
	// MC68EC000 sound CPU clock (11.2896 MHz NTSC / PAL).
	scspM68kClockHz = 11289600

	// Sample rate (44.1 kHz, region-independent).
	scspSampleRateHz = 44100

	scspSoundRAMSize = 512 * 1024 // 512 KB
	scspRegSize      = 0xEE4      // Register space: 0x100000-0x100EE3 (byte size)
	scspRegWords     = scspRegSize / 2

	// Common control register offsets (byte offsets from register base)
	scspRegTimerA = 0x418
	scspRegTimerB = 0x41A
	scspRegTimerC = 0x41C
	scspRegSCIEB  = 0x41E
	scspRegSCIPD  = 0x420
	scspRegSCIRE  = 0x422
	scspRegSCILV0 = 0x424
	scspRegSCILV1 = 0x426
	scspRegSCILV2 = 0x428
	scspRegMCIEB  = 0x42A
	scspRegMCIPD  = 0x42C
	scspRegMCIRE  = 0x42E

	// Common control register offsets
	scspRegMVOL = 0x400 // MEM4MB(9), DAC18B(8), VER(7:4), MVOL(3:0)
	scspRegMSLC = 0x408 // MSLC(15:11), CA(10:7)

	// MIDI register offsets. Saturn does not wire the SCSP MIDI pins to
	// any connector, so 0x404 reads a constant empty-FIFO status and
	// writes to 0x406 are discarded.
	scspRegMIDI  = 0x404
	scspRegMOBUF = 0x406

	// DMA register offsets
	scspRegDMEAL  = 0x412
	scspRegDMEAH  = 0x414
	scspRegDMACtl = 0x416

	// Interrupt bit positions in SCIPD/MCIPD/SCIEB/MCIEB/SCIRE/MCIRE.
	// Bits 0-2 (INT0N/1N/2N), 3 (MIDI-IN), and 9 (MIDI-OUT) cannot fire
	// on Saturn because the corresponding pins are unconnected.
	scspIntDMA    = 1 << 4
	scspIntCPU    = 1 << 5
	scspIntTimerA = 1 << 6
	scspIntTimerB = 1 << 7
	scspIntTimerC = 1 << 8
	scspIntSample = 1 << 10

	// Envelope generator states. The SCSP has four states only; sl.active
	// distinguishes "playing" from "silent / awaiting next key-on" without
	// adding a fifth state. After release saturates, the slot stays in
	// egRelease with egLevel=egDecayEnd and egStep=0 so MSLC readback
	// reports SGC=Release as on real hardware.
	egAttack  = 0
	egDecay1  = 1
	egDecay2  = 2
	egRelease = 3

	// Phase accumulator fractional bits
	phaseFracBits = 10

	// Envelope generator fixed-point format: 10-bit integer, 10-bit fraction
	egFracBits    = 10
	egLen         = 1 << 10                   // 1024 integer levels
	egAttackStart = 0                         // Attack counts up from 0
	egAttackEnd   = (egLen << egFracBits) - 1 // Attack target
	egDecayStart  = egLen << egFracBits       // Decay counts up from here
	egDecayEnd    = (2*egLen)<<egFracBits - 1 // Decay/release target (slot off)
)

// scspSlotState holds runtime state for one of the 32 SCSP sound slots.
type scspSlotState struct {
	phase    uint32 // Fixed-point phase accumulator (integer.frac)
	egLevel  int32  // 20-bit fixed-point envelope counter (10.10 format)
	egState  uint8  // egAttack..egRelease
	egStep   int32  // Current per-sample EG step value
	egTarget int32  // Envelope counter target for current phase
	active   bool   // true when slot is producing sound
	finished bool   // true after no-loop (LPCTL=0) slot passes LEA; freezes phase/output
	loopDir  int8   // +1 forward, -1 backward (reverse/alternating loops)
	output   int16  // Last output sample (used by mixer)
	lfoPhase uint8  // 8-bit LFO phase counter (0-255 = one cycle)
	lfoStep  uint16 // Sub-sample prescaler for LFO advance
	lfoNoise uint16 // LFSR state for noise waveform
}

// egAttackStep and egDecayStep map effective rate (0-63) to per-sample step
// values for the 20-bit fixed-point envelope counter (10.10 format).
// Derived from the AICA hardware manual transition time tables.
// Effective rate = (KRS + OCT) * 2 + FNS[9] + rate * 2, clamped to 0-63.
var egAttackStep [64]int32
var egDecayStep [64]int32
var egAttackCurve [1024]uint16

func init() {
	initEGTables()
	initLFOAndPanTables()
}

func initEGTables() {
	const envRange = 1024 << 10 // 20-bit full scale (10.10 fixed point)
	const sampleRate = 44100.0

	// Attack transition times in ms from AICA manual (rate 0-63)
	// Time to go from -96dB to 0dB. 0 = infinity, special = instant.
	attackMs := [64]float64{
		0, 0, 8100, 6900, 6000, 4800, 4000, 3400,
		3000, 2400, 2000, 1700, 1500, 1200, 1000, 860,
		760, 600, 500, 430, 380, 300, 250, 220,
		190, 150, 130, 110, 95, 76, 63, 55,
		47, 38, 31, 27, 24, 19, 15, 13,
		12, 9.4, 7.9, 6.8, 6.0, 4.7, 3.8, 3.4,
		3.0, 2.4, 2.0, 1.8, 1.6, 1.3, 1.1, 0.93,
		0.85, 0.65, 0.53, 0.44, 0.40, 0.35, 0, 0,
	}

	// Decay/release transition times in ms from AICA manual (rate 0-63)
	// Time to go from 0dB to -96dB.
	decayMs := [64]float64{
		0, 0, 118200, 101300, 88600, 70900, 59100, 50700,
		44300, 35500, 29600, 25300, 22200, 17700, 14800, 12700,
		11100, 8900, 7400, 6300, 5500, 4400, 3700, 3200,
		2800, 2200, 1800, 1600, 1400, 1100, 920, 790,
		690, 550, 460, 390, 340, 270, 230, 200,
		170, 140, 110, 98, 85, 68, 57, 49,
		43, 34, 28, 25, 22, 18, 14, 12,
		11, 8.5, 7.1, 6.1, 5.4, 4.3, 3.6, 3.1,
	}

	for i := 0; i < 64; i++ {
		if attackMs[i] > 0 {
			samples := attackMs[i] * sampleRate / 1000.0
			egAttackStep[i] = int32(float64(envRange)/samples + 0.5)
			if egAttackStep[i] == 0 {
				egAttackStep[i] = 1
			}
		}
		// Rates 0-1 and 62-63 for attack: 0 = no change, handled specially

		if decayMs[i] > 0 {
			samples := decayMs[i] * sampleRate / 1000.0
			egDecayStep[i] = int32(float64(envRange)/samples + 0.5)
			if egDecayStep[i] == 0 {
				egDecayStep[i] = 1
			}
		}
	}

	// Rates 62-63 for attack = instant (complete in 1 sample)
	egAttackStep[62] = envRange
	egAttackStep[63] = envRange

	// Attack curve: non-linear (x^4) mapping from linear counter position
	// to attenuation. Attack starts loud at first then slowly approaches
	// full volume, matching hardware behavior.
	for i := 0; i < 1024; i++ {
		x := float64(1023-i) / 1023.0
		egAttackCurve[i] = uint16(math.Pow(x, 4) * 1023)
	}
	egAttackCurve[1023] = 0
}

// scspEffectiveRate computes the EG effective rate from the AICA hardware formula:
// effRate = (KRS + OCT) * 2 + FNS[9] + rate * 2, clamped to 0-63.
// KRS = 0xF means scaling off (KRS contribution = 0).
// The full formula always applies; key rate scaling can produce a non-zero
// effective rate even when the register rate value is 0.
func scspEffectiveRate(s *SCSP, slotIdx int, baseRate uint16) int {
	regA := s.slotReg(slotIdx, 0x0A)
	krs := int((regA >> 10) & 0xF)

	regPitch := s.slotReg(slotIdx, 0x10)
	oct := int((regPitch >> 11) & 0xF)
	fns9 := int((regPitch >> 9) & 1)

	// OCT is 4-bit two's complement
	if oct >= 8 {
		oct -= 16
	}

	var krsContrib int
	if krs != 0xF {
		krsContrib = (krs + oct) * 2
	}

	effRate := krsContrib + fns9 + int(baseRate)*2
	if effRate < 0 {
		effRate = 0
	}
	if effRate > 63 {
		effRate = 63
	}
	return effRate
}

// lfoFreqTable maps LFOF (0-31) to samples per LFO phase step.
// Derived from AICA manual LFO frequency table. The LFO phase has 256 steps
// per cycle, so samples_per_step = 44100 / (freq_hz * 256).
var lfoFreqTable [32]uint16

// alfosMaxAtten maps ALFOS (0-7) to maximum attenuation addition per SCSP
// User's Manual Table 4.24: 0=off, 1=0.4 dB, 2=0.8 dB, 3=1.5 dB, 4=3 dB,
// 5=6 dB, 6=12 dB, 7=24 dB. The mantissa/exponent atten model produces
// one exponent step per 64 units (= 6 dB), so 1 unit ~= 0.09375 dB.
var alfosMaxAtten = [8]uint16{0, 4, 9, 16, 32, 64, 128, 256}

// plfosTable[level][plfoIdx] is a fixed-point 16.16 phase increment
// multiplier applied per-sample when PLFOS is enabled. plfoIdx is the
// int8 PLFO waveform value offset by +128 (range 0..255). The multiplier
// encodes 2^(cents/1200) where cents = plfoSigned * maxCents[level] / 128.
// Max deviation per PLFOS level per SCSP User's Manual Table 4.24:
// 0=off, 1=+/-7, 2=+/-13.5, 3=+/-27, 4=+/-55, 5=+/-112, 6=+/-230, 7=+/-494 cents.
var plfosTable [8][256]uint32

// panLaw maps the low 4 bits of DIPAN/EFPAN to a 1.15 fixed-point
// attenuation multiplier per SCSP User's Manual Table 4.28/4.30:
// 0x00 = 0 dB, each step -3 dB through 0x0E = -42 dB, 0x0F = -inf (mute).
var panLaw [16]int32

func initLFOAndPanTables() {
	// LFO frequency table from SCSP User's Manual Table 4.21 (Hz per LFOF value)
	lfoHz := [32]float64{
		0.17, 0.19, 0.23, 0.27, 0.34, 0.39, 0.45, 0.55,
		0.68, 0.78, 0.92, 1.10, 1.39, 1.60, 1.87, 2.27,
		2.87, 3.31, 3.92, 4.79, 6.15, 7.18, 8.60, 10.80,
		14.40, 17.20, 21.50, 28.70, 43.10, 57.40, 86.10, 172.30,
	}
	for i := 0; i < 32; i++ {
		samplesPerStep := 44100.0 / (lfoHz[i] * 256.0)
		v := uint16(samplesPerStep + 0.5)
		if v == 0 {
			v = 1
		}
		lfoFreqTable[i] = v
	}

	// Pitch LFO per-level per-value lookup table. For each PLFOS level,
	// precompute a fixed-point 16.16 phase increment multiplier for every
	// possible LFO waveform value. Indexed by (level, plfoVal+128).
	plfosMaxCents := [8]float64{0, 7, 13.5, 27, 55, 112, 230, 494}
	for level := 0; level < 8; level++ {
		for idx := 0; idx < 256; idx++ {
			signed := idx - 128 // -128..+127
			cents := float64(signed) * plfosMaxCents[level] / 128.0
			mult := math.Pow(2, cents/1200.0)
			plfosTable[level][idx] = uint32(mult*65536.0 + 0.5)
		}
	}

	// Pan attenuation table per SCSP User's Manual Table 4.28/4.30.
	// Stored as 1.15 fixed point. -3 dB per step from 0x00 to 0x0E,
	// 0x0F is -inf (full mute).
	for i := 0; i < 15; i++ {
		db := -3.0 * float64(i)
		panLaw[i] = int32(math.Pow(10, db/20.0)*32768.0 + 0.5)
	}
	panLaw[15] = 0
}

// lfoWaveform computes the LFO output for a given phase and waveform type.
// Returns a signed value from -128 to +127.
func lfoWaveform(phase uint8, ws uint16, noise *uint16) int8 {
	switch ws {
	case 0: // Sawtooth
		return int8(phase)
	case 1: // Square
		if phase < 128 {
			return 127
		}
		return -128
	case 2: // Triangle
		if phase < 128 {
			return int8(int(phase)*2 - 128)
		}
		return int8(382 - int(phase)*2)
	case 3: // Noise
		// Simple LFSR-based noise, step once per LFO cycle quarter
		*noise ^= (*noise >> 3) ^ (*noise << 5)
		*noise = (*noise >> 1) | ((*noise & 1) << 15)
		return int8(*noise)
	}
	return 0
}

// CDAudioSource is the interface SCSP uses to pull CD-DA stereo samples
// for the EXTS external audio input. The CD block satisfies this interface.
// PopAudioSample returns valid=false when no sample is available; the
// SCSP treats that as silent for the current sample tick.
type CDAudioSource interface {
	PopAudioSample() (left, right int16, valid bool)
	DrainAudio()
}

// SCSP implements the Saturn Custom Sound Processor.
type SCSP struct {
	ram  []byte               // 512 KB sound RAM
	regs [scspRegWords]uint16 // All SCSP registers as 16-bit words

	m68k    *m68k.CPU       // MC68EC000 sound CPU
	m68kBus *m68kBusAdaptor // Bridge between m68k.Bus and SCSP memory
	inReset bool            // true = held in reset (not executing)
	cdAudio CDAudioSource   // CD block providing CD-DA samples for EXTS

	// Per-frame target tracking for exact sample and m68k cycle counts.
	// Emulator calls StartFrame at the start of each RunFrame to set
	// these targets; TickSystemCycles derives deltas as
	// (systemCyclesElapsed * target / systemCyclesTotal) - alreadyEmitted.
	// This guarantees the per-frame totals are hit exactly (e.g. 735
	// samples and 188,160 m68k cycles for NTSC integer 60 fps) with no
	// truncation drift.
	frameSystemCyclesTotal   int
	frameSamplesTotal        int
	frameM68kTotal           int
	frameSystemCyclesElapsed int
	frameSamplesEmitted      int
	frameM68kEmitted         int

	scu *SCU // SCU reference for interrupt signaling

	timerPrescaler [3]uint16         // Sub-sample accumulator per timer (A, B, C)
	timerCounter   [3]uint8          // Internal 8-bit up counter (not readable via registers)
	slots          [32]scspSlotState // Per-slot runtime state
	dsp            scspDSP           // DSP effects processor
	mainIntActive  bool              // Level-sensitive: true when MCIPD & MCIEB is non-zero

	mixBuffer []int16 // Interleaved stereo output (L, R, L, R, ...)
	mixPos    int     // Write position in mixBuffer

	// soundIntLevel is the last-delivered sound-CPU IRQ level. The
	// SCSP asserts a level-sensitive line; we only re-request on a
	// transition (0->N or N->M>N) to mirror real hardware.
	soundIntLevel uint8

	lfsr uint32 // 17-bit LFSR for noise generator (SSCTL=1)

	// FM slot output history: 64-entry circular buffer for phase modulation.
	// Each slot writes its attenuated output; the write cursor advances per
	// slot (32 per sample tick, wrapping every 2 ticks). MDXSL/MDYSL are
	// 6-bit offsets into this buffer relative to the write cursor.
	// A 4-stage delay pipeline separates the slot output from the history
	// buffer, matching hardware timing.
	fmHistory    [64]int16
	fmHistoryCur int      // Write cursor, advances per slot
	fmDelayer    [4]int16 // 4-slot delay pipeline
}

// m68kBusAdaptor adapts the SCSP address space for the MC68EC000 bus interface.
// The 68K sees sound RAM at 0x000000-0x07FFFF and registers at 0x100000-0x100EE3.
type m68kBusAdaptor struct {
	scsp *SCSP
}

func (sb *m68kBusAdaptor) Read8(addr uint32) uint8 {
	addr &= 0xFFFFFF
	switch {
	case addr <= 0x07FFFF:
		return sb.scsp.ReadRAM(addr)
	case addr >= 0x100000 && addr <= 0x100EE3:
		off := addr - 0x100000
		reg := sb.scsp.Read(off &^ 1)
		if off&1 == 0 {
			return uint8(reg >> 8)
		}
		return uint8(reg)
	default:
		return 0
	}
}

func (sb *m68kBusAdaptor) Read16(addr uint32) uint16 {
	addr &= 0xFFFFFF
	switch {
	case addr <= 0x07FFFF:
		return sb.scsp.ReadRAM16(addr)
	case addr >= 0x100000 && addr <= 0x100EE3:
		return sb.scsp.Read(addr - 0x100000)
	default:
		return 0
	}
}

func (sb *m68kBusAdaptor) Read32(addr uint32) uint32 {
	addr &= 0xFFFFFF
	switch {
	case addr <= 0x07FFFF:
		return uint32(sb.scsp.ReadRAM16(addr))<<16 | uint32(sb.scsp.ReadRAM16(addr+2))
	case addr >= 0x100000 && addr <= 0x100EE3:
		off := addr - 0x100000
		return uint32(sb.scsp.Read(off))<<16 | uint32(sb.scsp.Read(off+2))
	default:
		return 0
	}
}

func (sb *m68kBusAdaptor) Write8(addr uint32, val uint8) {
	addr &= 0xFFFFFF
	switch {
	case addr <= 0x07FFFF:
		sb.scsp.WriteRAM(addr, val)
	case addr >= 0x100000 && addr <= 0x100EE3:
		off := addr - 0x100000
		aligned := off &^ 1
		cur := sb.scsp.Read(aligned)
		if off&1 == 0 {
			cur = (cur & 0x00FF) | (uint16(val) << 8)
		} else {
			cur = (cur & 0xFF00) | uint16(val)
		}
		sb.scsp.Write(aligned, cur)
	}
}

func (sb *m68kBusAdaptor) Write16(addr uint32, val uint16) {
	addr &= 0xFFFFFF
	switch {
	case addr <= 0x07FFFF:
		sb.scsp.WriteRAM16(addr, val)
	case addr >= 0x100000 && addr <= 0x100EE3:
		sb.scsp.Write(addr-0x100000, val)
	}
}

func (sb *m68kBusAdaptor) Write32(addr uint32, val uint32) {
	addr &= 0xFFFFFF
	switch {
	case addr <= 0x07FFFF:
		sb.scsp.WriteRAM16(addr, uint16(val>>16))
		sb.scsp.WriteRAM16(addr+2, uint16(val))
	case addr >= 0x100000 && addr <= 0x100EE3:
		off := addr - 0x100000
		sb.scsp.Write(off, uint16(val>>16))
		sb.scsp.Write(off+2, uint16(val))
	}
}

func (sb *m68kBusAdaptor) Reset() {}

// NewSCSP creates a new SCSP with sound RAM, registers, and MC68EC000.
// The 68K is held in reset and not stepped until SMPC SNDON.
func NewSCSP(scu *SCU) *SCSP {
	s := &SCSP{
		ram:     make([]byte, scspSoundRAMSize),
		inReset: true,
		scu:     scu,
		lfsr:    1, // LFSR seed for noise generator
	}
	s.m68kBus = &m68kBusAdaptor{scsp: s}
	s.m68k = m68k.New(s.m68kBus)
	return s
}

// Reset clears all SCSP registers, slot state, timers, and DSP to
// power-on defaults. Sound RAM is NOT cleared (only on power-up).
// Called during CKCHG/SYSRES system reset.
func (s *SCSP) Reset() {
	// Clear all registers
	for i := range s.regs {
		s.regs[i] = 0
	}
	// Reset all slots to off state
	for i := range s.slots {
		s.slots[i] = scspSlotState{
			egLevel: egDecayStart, // Envelope at maximum attenuation
			egState: egRelease,
		}
	}
	// Reset timers
	for i := range s.timerPrescaler {
		s.timerPrescaler[i] = 0
		s.timerCounter[i] = 0
	}
	// Reset DSP
	s.dsp = scspDSP{}
	// Drain CD audio queue so resumed audio doesn't replay stale samples
	if s.cdAudio != nil {
		s.cdAudio.DrainAudio()
	}
	// Reset misc state
	s.mainIntActive = false
	s.lfsr = 1
	// Clear FM history
	s.fmHistory = [64]int16{}
	s.fmHistoryCur = 0
	s.fmDelayer = [4]int16{}
}

// Read returns the 16-bit register at the given byte offset from 0x100000.
func (s *SCSP) Read(offset uint32) uint16 {
	offset &^= 1
	if offset >= scspRegSize {
		return 0
	}

	switch offset {
	// MIDI status + input buffer. Saturn does not wire the MIDI pins,
	// so MIBUF is always empty (0xFF), MIEMP and MOEMP are always 1,
	// and MIFULL/MIOVF/MOFULL are always 0.
	case scspRegMIDI:
		return 0x09FF

	// MVOL: return VER=2 in bits 7:4 per SCSP User's Manual Table 1.1
	case scspRegMVOL:
		return (s.regs[offset/2] & 0xFF0F) | 0x0020

	// Timer registers return the live counter value in bits 7:0
	case scspRegTimerA:
		return (s.regs[offset/2] & 0x0700) | uint16(s.timerCounter[0])
	case scspRegTimerB:
		return (s.regs[offset/2] & 0x0700) | uint16(s.timerCounter[1])
	case scspRegTimerC:
		return (s.regs[offset/2] & 0x0700) | uint16(s.timerCounter[2])

	// MSLC/CA: return monitored slot's EG state, EG level, and call address.
	// Bits 15:11 = MSLC (written by CPU), bits 10:7 = CA, bits 6:5 = SGC (EG state),
	// bits 4:0 = EG level upper 5 bits.
	//
	// Per SCSP User's Manual (CA definition): CA is the sample count from
	// SA divided by 4096 (LSB = 4K samples). It is NOT derived from the
	// absolute memory address; it is purely a function of how many samples
	// have been played since SA.
	case scspRegMSLC:
		mslc := (s.regs[offset/2] >> 11) & 0x1F
		sl := &s.slots[mslc]
		samplePos := sl.phase >> phaseFracBits
		ca := (samplePos >> 12) & 0x0F
		// SGC = EG state (0=attack, 1=decay1, 2=decay2, 3=release)
		sgc := uint16(sl.egState & 3)
		// EG level = upper 5 bits of 10-bit attenuation
		egAtten := egLevelToAtten(sl.egLevel)
		egTop5 := egAtten >> 5
		return (s.regs[offset/2] & 0xF800) | uint16(ca)<<7 | sgc<<5 | egTop5
	}

	// Sound data stack (SOUS) per Sec 4.2 Figure 4.4: 64 words at 0x600-0x67F,
	// two generations of 32 per-slot entries. Generation A (0x600-0x63F) is
	// the most recent slot output; generation B (0x640-0x67F) is the prior
	// tick. Reads map into the fmHistory ring relative to fmHistoryCur.
	if offset >= 0x600 && offset < 0x680 {
		slot := int((offset - 0x600) >> 1)
		var idx int
		if offset < 0x640 {
			idx = (s.fmHistoryCur - 32 + slot) & 63
		} else {
			idx = (s.fmHistoryCur - 64 + (slot - 32)) & 63
		}
		return uint16(s.fmHistory[idx])
	}

	// DSP internal buffers (TEMP, MEMS, MIXS, EFREG) are updated by
	// runDSP without syncing to the main register file. CPU reads
	// of these areas must return the live DSP state.
	if offset >= dspRegTEMP {
		return s.readDSPReg(offset)
	}

	return s.regs[offset/2]
}

// Write stores a 16-bit value at the given byte offset from 0x100000.
// Certain registers have side-effects on write.
func (s *SCSP) Write(offset uint32, val uint16) {
	offset &^= 1
	if offset >= scspRegSize {
		return
	}

	switch offset {
	case scspRegMIDI:
		// Register 0x404 is read-only (status + MIBUF)
		return
	case scspRegMOBUF:
		// MIDI-OUT pins unconnected on Saturn; bytes are discarded.
		return
	case scspRegSCIRE:
		// Write-to-clear: clear corresponding bits in SCIPD
		s.regs[scspRegSCIPD/2] &^= val & 0x07FF
		s.checkSoundInterrupt()
		return
	case scspRegMCIRE:
		// Write-to-clear: clear corresponding bits in MCIPD
		s.regs[scspRegMCIPD/2] &^= val & 0x07FF
		s.checkMainInterrupt()
		return
	case scspRegSCIPD:
		// Per SCSP User's Manual Sec 4.2 page 96: only bit 5 (CPU manual)
		// is writable, and writing 0B is invalid (cannot clear via
		// direct write; use SCIRE).
		s.regs[offset/2] |= val & scspIntCPU
		s.checkSoundInterrupt()
		return
	case scspRegMCIPD:
		s.regs[offset/2] |= val & scspIntCPU
		s.checkMainInterrupt()
		return
	case scspRegSCIEB:
		s.regs[offset/2] = val & 0x07FF
		s.checkSoundInterrupt()
		return
	case scspRegMCIEB:
		s.regs[offset/2] = val & 0x07FF
		s.checkMainInterrupt()
		return
	case scspRegSCILV0, scspRegSCILV1, scspRegSCILV2:
		// SCILV priority changes can change the asserted IRQ level
		// without changing SCIPD/SCIEB; re-evaluate so the m68k sees
		// the new level immediately. The functional bits are 7:0 only
		// (used by the priority encoder via scilvBit clamp); the high
		// byte is preserved on write to match the bus's 16-bit
		// storage behavior.
		s.regs[offset/2] = val
		s.checkSoundInterrupt()
		return
	case scspRegDMACtl:
		s.regs[offset/2] = val
		if val&0x1000 != 0 { // DEXE
			s.executeDMA()
		}
		return
	case scspRegTimerA:
		s.timerPrescaler[0] = 0
		s.timerCounter[0] = uint8(val)
		s.regs[offset/2] = val
		if uint8(val) == 0xFF {
			s.regs[scspRegSCIPD/2] |= scspIntTimerA
			s.regs[scspRegMCIPD/2] |= scspIntTimerA
			s.checkSoundInterrupt()
			s.checkMainInterrupt()
		}
		return
	case scspRegTimerB:
		s.timerPrescaler[1] = 0
		s.timerCounter[1] = uint8(val)
		s.regs[offset/2] = val
		if uint8(val) == 0xFF {
			s.regs[scspRegSCIPD/2] |= scspIntTimerB
			s.regs[scspRegMCIPD/2] |= scspIntTimerB
			s.checkSoundInterrupt()
			s.checkMainInterrupt()
		}
		return
	case scspRegTimerC:
		s.timerPrescaler[2] = 0
		s.timerCounter[2] = uint8(val)
		s.regs[offset/2] = val
		if uint8(val) == 0xFF {
			s.regs[scspRegSCIPD/2] |= scspIntTimerC
			s.regs[scspRegMCIPD/2] |= scspIntTimerC
			s.checkSoundInterrupt()
			s.checkMainInterrupt()
		}
		return
	}

	// Check for KYONEX in slot register writes (offset 0x00 of any slot)
	if offset < 0x400 && offset&0x1E == 0 && val&0x1000 != 0 {
		s.regs[offset/2] = val &^ 0x1000 // Store without KYONEX bit
		s.executeKeyOnOff()
		return
	}

	s.regs[offset/2] = val

	// Sync DSP register writes
	if offset >= dspRegCOEF {
		s.syncDSPRegWrite(offset, val)
	}
}

// slotReg reads a 16-bit slot register. slotOffset is the byte offset within
// the 0x20-byte slot (0x00, 0x02, ... 0x16).
func (s *SCSP) slotReg(slot int, slotOffset uint32) uint16 {
	return s.regs[(uint32(slot)*0x20+slotOffset)/2]
}

// executeKeyOnOff processes KYONEX: scans all 32 slots for pending key on/off.
func (s *SCSP) executeKeyOnOff() {
	for i := 0; i < 32; i++ {
		sl := &s.slots[i]
		reg0 := s.slotReg(i, 0x00)
		kyonb := reg0&0x0800 != 0

		if kyonb && (sl.egState == egRelease || !sl.active) {
			sl.phase = 0
			sl.active = true
			sl.finished = false

			// Start attack. egStep is re-derived from AR on the
			// first sample tick in advanceEG; no need to pre-seed.
			sl.egState = egAttack
			sl.egLevel = egAttackStart
			sl.egTarget = egAttackEnd
			sl.loopDir = 1
			sl.output = 0
			sl.lfoPhase = 0
			sl.lfoStep = 0
			sl.lfoNoise = 1

		} else if !kyonb && sl.active && sl.egState != egRelease {
			// Key off: transition to release from current level
			if sl.egState == egAttack {
				sl.egLevel = egDecayEnd - sl.egLevel
			}
			regAOff := s.slotReg(i, 0x0A)
			rrOff := regAOff & 0x1F
			rrEffRate := scspEffectiveRate(s, i, rrOff)
			sl.egState = egRelease
			sl.egStep = egDecayStep[rrEffRate]
			sl.egTarget = egDecayEnd
		}
	}
}

// calcPhaseIncrement computes the phase accumulator increment from OCT and FNS.
// Returns a fixed-point value with phaseFracBits fractional bits.
func calcPhaseIncrement(oct, fns uint16) uint32 {
	// FNS is 10 bits, add implicit bit 10
	base := uint32(fns | 0x400)

	// OCT is 4-bit signed (0-7 positive, 8-15 = -8 to -1)
	octSigned := int(oct)
	if octSigned >= 8 {
		octSigned -= 16
	}

	if octSigned >= 0 {
		return base << uint(octSigned)
	}
	return base >> uint(-octSigned)
}

// advanceFMPipeline shifts the 4-slot FM delay pipeline by one position
// and writes the new input sample at the head. The oldest entry exits
// the pipeline into the history ring, unless writeHistory is false (STWINH
// gated by the slot producing the exiting entry). Called once per slot
// tick; every slot must invoke this exactly once so per-slot history
// alignment stays consistent across all 32 slots.
func (s *SCSP) advanceFMPipeline(sample int16, writeHistory bool) {
	if writeHistory {
		s.fmHistory[(s.fmHistoryCur-4)&63] = s.fmDelayer[3]
	}
	s.fmDelayer[3] = s.fmDelayer[2]
	s.fmDelayer[2] = s.fmDelayer[1]
	s.fmDelayer[1] = s.fmDelayer[0]
	s.fmDelayer[0] = sample
	s.fmHistoryCur = (s.fmHistoryCur + 1) & 63
}

// processSlots advances all 32 slots by one sample tick.
func (s *SCSP) processSlots() {
	for i := 0; i < 32; i++ {
		sl := &s.slots[i]
		if !sl.active {
			sl.output = 0
			s.advanceFMPipeline(0, true)
			continue
		}

		// Read slot registers
		reg0 := s.slotReg(i, 0x00)
		ssctl := (reg0 >> 7) & 3
		lpctl := (reg0 >> 5) & 3
		pcm8b := reg0&0x10 != 0
		saHigh := uint32(reg0 & 0x0F)
		saLow := uint32(s.slotReg(i, 0x02))
		sa := (saHigh << 16) | saLow
		lsa := uint32(s.slotReg(i, 0x04))
		lea := uint32(s.slotReg(i, 0x06))

		regPitch := s.slotReg(i, 0x10)
		oct := (regPitch >> 11) & 0xF
		fns := regPitch & 0x3FF

		// Advance envelope. advanceEG clears sl.active when release
		// saturates; the slot then stays in egRelease with EG=$3FF for
		// the MSLC readback, but produces no further output.
		s.advanceEG(i)

		if !sl.active {
			sl.output = 0
			s.advanceFMPipeline(0, true)
			continue
		}

		if sl.finished {
			// LPCTL=0 slot past LEA: waveform access disabled, phase frozen.
			// EG already advanced above (for release); LFO and phase update
			// are skipped since the slot can never produce output again
			// until the next key-on (which resets finished).
			sl.output = 0
			s.advanceFMPipeline(0, true)
			continue
		}

		// Advance LFO
		regLFO := s.slotReg(i, 0x12)
		lfore := regLFO&0x8000 != 0
		lfof := (regLFO >> 10) & 0x1F
		plfows := (regLFO >> 8) & 3
		plfos := (regLFO >> 5) & 7
		alfows := (regLFO >> 3) & 3
		alfos := regLFO & 7

		if lfore {
			sl.lfoPhase = 0
			sl.lfoStep = 0
		}

		if lfof > 0 || lfoFreqTable[lfof] > 0 {
			sl.lfoStep++
			if sl.lfoStep >= lfoFreqTable[lfof] {
				sl.lfoStep = 0
				sl.lfoPhase++
			}
		}

		// Calculate base phase increment from OCT/FNS.
		phaseInc := calcPhaseIncrement(oct, fns)
		// Apply pitch LFO modulation via precomputed 16.16 fixed-point
		// multiplier from plfosTable. Values from SCSP User's Manual
		// Table 4.24 (+/-7..+/-494 cents per level).
		if plfos > 0 {
			plfoVal := lfoWaveform(sl.lfoPhase, plfows, &sl.lfoNoise)
			idx := int(plfoVal) + 128 // -128..+127 -> 0..255
			mult := plfosTable[plfos][idx]
			phaseInc = uint32((uint64(phaseInc) * uint64(mult)) >> 16)
		}

		if sl.loopDir >= 0 {
			sl.phase += phaseInc
		} else {
			// Reverse playback: subtract phase increment.
			// Clamp to 0 to prevent uint32 underflow.
			if sl.phase >= phaseInc {
				sl.phase -= phaseInc
			} else {
				sl.phase = 0
			}
		}
		samplePos := sl.phase >> phaseFracBits
		phaseFrac := sl.phase & ((1 << phaseFracBits) - 1)

		// Phase modulation: two source slots from the FM history buffer
		// are summed and scaled by MDL to offset the sample read position.
		// MDL 0-4 disables modulation. Each step above 4 doubles the depth.
		// Per SCSP User's Manual Figure 4.19 / Phase Adder description,
		// the modulation phase is added to the slot's own PG phase; the
		// carry from the fractional add propagates into the integer
		// sample offset, and the low 6 bits become the interpolation
		// blend factor.
		regMod := s.slotReg(i, 0x0E)
		mdl := int((regMod >> 12) & 0xF)
		var fmSampleOff int32
		if mdl > 4 {
			mdx := int((regMod >> 6) & 0x3F)
			mdy := int(regMod & 0x3F)

			modA := int32(s.fmHistory[(s.fmHistoryCur+mdx)&63])
			modB := int32(s.fmHistory[(s.fmHistoryCur+mdy)&63])
			modTotal := modA + modB

			var scaled int32
			if mdl <= 10 {
				scaled = modTotal >> uint(10-mdl)
			} else {
				scaled = modTotal << uint(mdl-10)
			}

			combined := scaled + int32((phaseFrac>>(phaseFracBits-6))&0x3F)
			fmSampleOff = combined >> 6
			phaseFrac = uint32(combined&0x3F) << (phaseFracBits - 6)
		}

		// Handle loop boundaries
		switch lpctl {
		case 0: // No loop
			if samplePos > lea && lea > 0 {
				// Per SCSP User's Manual: WFAllowAccess becomes false and the
				// phase freezes at LEA. MSLC/CA reflects this frozen position.
				sl.phase = lea << phaseFracBits
				sl.finished = true
				sl.output = 0
				continue
			}
		case 1: // Normal loop
			if samplePos > lea && lea > lsa {
				loopLen := lea - lsa + 1
				samplePos = lsa + (samplePos-lsa)%loopLen
				sl.phase = samplePos<<phaseFracBits | (sl.phase & ((1 << phaseFracBits) - 1))
			}
		case 2: // Reverse loop: forward once to LEA, then backward LSA<->LEA
			if sl.loopDir > 0 && samplePos >= lea {
				// Reached end going forward: reverse direction
				sl.loopDir = -1
				samplePos = lea
				sl.phase = samplePos<<phaseFracBits | (sl.phase & ((1 << phaseFracBits) - 1))
			} else if sl.loopDir < 0 && samplePos <= lsa {
				// Reached start going backward: wrap to LEA (stay reversed)
				samplePos = lea
				sl.phase = samplePos<<phaseFracBits | (sl.phase & ((1 << phaseFracBits) - 1))
			}
		case 3: // Alternating loop: pingpong between LSA and LEA
			if sl.loopDir > 0 && samplePos >= lea {
				// Reached end going forward: reverse direction
				sl.loopDir = -1
				samplePos = lea
				sl.phase = samplePos<<phaseFracBits | (sl.phase & ((1 << phaseFracBits) - 1))
			} else if sl.loopDir < 0 && samplePos <= lsa {
				// Reached start going backward: reverse to forward
				sl.loopDir = 1
				samplePos = lsa
				sl.phase = samplePos<<phaseFracBits | (sl.phase & ((1 << phaseFracBits) - 1))
			}
		}

		// LPSLNK: synchronize EG attack->decay1 with loop start point
		regA := s.slotReg(i, 0x0A)
		lpslnk := regA&0x4000 != 0
		if lpslnk && sl.egState == egAttack && samplePos >= lsa {
			// Force attack->decay1 transition at loop start
			sl.egLevel = egDecayStart
			reg8LP := s.slotReg(i, 0x08)
			regALP := s.slotReg(i, 0x0A)
			d1rLP := (reg8LP >> 6) & 0x1F
			dlLP := int((regALP >> 5) & 0x1F)
			d1EffLP := scspEffectiveRate(s, i, d1rLP)
			sl.egState = egDecay1
			sl.egStep = egDecayStep[d1EffLP]
			sl.egTarget = egDecayStart + int32(dlLP<<(5+egFracBits))
		}

		// Read sample with linear interpolation and FM sample offset.
		// The FM offset can push the read position past LEA or before LSA,
		// so wrap it back into the loop region for looping slots.
		fmSamplePos := int32(samplePos) + fmSampleOff
		if lpctl == 1 && lea > lsa {
			loopLen := int32(lea - lsa + 1)
			rel := fmSamplePos - int32(lsa)
			rel = ((rel % loopLen) + loopLen) % loopLen
			fmSamplePos = int32(lsa) + rel
		} else if fmSamplePos < 0 {
			fmSamplePos = 0
		}
		var sample int16
		switch ssctl {
		case 0: // Sound RAM
			sample = s.readSlotSample(sa, uint32(fmSamplePos), pcm8b, phaseFrac, lpctl, lsa, lea, sl.loopDir)
		case 1: // Noise - 17-bit LFSR
			sample = int16(s.lfsr << 8)
		default: // 2=zero, 3=reserved
			sample = 0
		}

		// Clock LFSR once per slot (used by noise source)
		s.lfsr = (s.lfsr >> 1) | (((s.lfsr>>5)^s.lfsr)&1)<<16

		// Apply SBCTL
		sbctl := (reg0 >> 9) & 3
		if sbctl != 0 {
			us := uint16(sample)
			if sbctl&1 != 0 {
				us ^= 0x7FFF // XOR non-sign bits
			}
			if sbctl&2 != 0 {
				us ^= 0x8000 // XOR sign bit
			}
			sample = int16(us)
		}

		// Apply attenuation (EG + TL + amplitude LFO).
		// SDIR (Sound Direct): when set, bypass all attenuation.
		regC := s.slotReg(i, 0x0C)
		sdir := regC&0x100 != 0
		if !sdir {
			tl := uint16(regC & 0xFF)
			atten := s.egAtten(i) + (tl << 2)

			// Amplitude LFO depth per SCSP User's Manual Table 4.24.
			if alfos > 0 {
				alfoVal := lfoWaveform(sl.lfoPhase, alfows, &sl.lfoNoise)
				uVal := uint16(int16(alfoVal) + 128)
				atten += (uVal * alfosMaxAtten[alfos]) >> 8
			}

			if atten > 0x3FF {
				atten = 0x3FF
			}

			mantissa := int32((atten & 0x3F) ^ 0x7F)
			exponent := uint(atten>>6) + 7
			if exponent >= 24 {
				sample = 0
			} else {
				sample = int16((int32(sample) * mantissa) >> exponent)
			}
		}

		sl.output = sample

		// FM history pipeline advances once per slot tick; STWINH of the
		// slot 4 positions back gates whether the exiting entry writes
		// to fmHistory.
		stwinh4 := s.slotReg((i-4)&31, 0x0C)&0x200 != 0
		s.advanceFMPipeline(sample, !stwinh4)

		// Route slot output to DSP MIXS via ISEL/IMXL
		regDSPIn := s.slotReg(i, 0x14)
		isel := (regDSPIn >> 3) & 0xF
		imxl := regDSPIn & 0x7
		if imxl > 0 && sample != 0 {
			s.dsp.mixs[isel] += (int32(sample) << 4) >> uint(7-imxl)
		}
	}
}

// readSlotSample reads a PCM sample from sound RAM with linear interpolation.
// phaseFrac is the fractional phase (0..1023 with phaseFracBits=10).
// The next-sample address is wrapped to LSA when reading past LEA on a
// looping slot so interpolation at the loop seam uses the correct value.
func (s *SCSP) readSlotSample(sa, samplePos uint32, pcm8b bool, phaseFrac uint32, lpctl uint16, lsa, lea uint32, loopDir int8) int16 {
	nextPos := samplePos + 1
	switch lpctl {
	case 1: // Normal loop
		if samplePos >= lea && lea > lsa {
			nextPos = lsa
		}
	case 2, 3: // Reverse / alternating
		if loopDir >= 0 && samplePos >= lea && lea > lsa {
			nextPos = lea
		}
	}

	var s0, s1 int32
	if pcm8b {
		addr0 := (sa + samplePos) & 0x7FFFF
		addr1 := (sa + nextPos) & 0x7FFFF
		s0 = int32(int8(s.ram[addr0])) << 8
		s1 = int32(int8(s.ram[addr1])) << 8
	} else {
		addr0 := (sa + samplePos*2) & 0x7FFFE
		addr1 := (sa + nextPos*2) & 0x7FFFE
		s0 = int32(int16(uint16(s.ram[addr0])<<8 | uint16(s.ram[addr0+1])))
		s1 = int32(int16(uint16(s.ram[addr1])<<8 | uint16(s.ram[addr1+1])))
	}
	// 6-bit interpolation factor from top bits of fractional phase
	sia := int32((phaseFrac >> (phaseFracBits - 6)) & 0x3F)
	return int16((s0*(0x40-sia) + s1*sia) >> 6)
}

// advanceEG steps the envelope generator for a slot by one sample.
// Uses a per-sample step model with 20-bit fixed-point envelope counter.
// Attack counts up from 0 to egAttackEnd, then decay/sustain/release
// count up from egDecayStart to egDecayEnd.
func (s *SCSP) advanceEG(slotIdx int) {
	sl := &s.slots[slotIdx]

	// Re-derive egStep from the current rate register every sample.
	// The rate registers (AR/D1R/D2R/RR) and the scaling inputs
	// (KRS/OCT/FNS) are R/W; games can update them after a state
	// transition and expect the in-progress envelope to track the new
	// value. Saturated slots (egLevel at egDecayEnd) are skipped so
	// the saturate path's egStep=0 isn't repeatedly overwritten.
	if sl.egLevel < egDecayEnd {
		switch sl.egState {
		case egAttack:
			ar := s.slotReg(slotIdx, 0x08) & 0x1F
			sl.egStep = egAttackStep[scspEffectiveRate(s, slotIdx, ar)]
		case egDecay1:
			d1r := s.slotReg(slotIdx, 0x08) >> 6 & 0x1F
			sl.egStep = egDecayStep[scspEffectiveRate(s, slotIdx, d1r)]
		case egDecay2:
			d2r := s.slotReg(slotIdx, 0x08) >> 11 & 0x1F
			sl.egStep = egDecayStep[scspEffectiveRate(s, slotIdx, d2r)]
		case egRelease:
			rr := s.slotReg(slotIdx, 0x0A) & 0x1F
			sl.egStep = egDecayStep[scspEffectiveRate(s, slotIdx, rr)]
		}
	}

	sl.egLevel += sl.egStep
	if sl.egLevel >= sl.egTarget {
		reg8 := s.slotReg(slotIdx, 0x08)
		regA := s.slotReg(slotIdx, 0x0A)

		switch sl.egState {
		case egAttack:
			sl.egLevel = egDecayStart
			// Transition to Decay1. EGHOLD is applied as an
			// output-stage mask in egAtten and does not affect
			// the envelope counter or state transitions.
			d1r := (reg8 >> 6) & 0x1F
			d1EffRate := scspEffectiveRate(s, slotIdx, d1r)
			dl := int((regA >> 5) & 0x1F)
			sl.egState = egDecay1
			sl.egStep = egDecayStep[d1EffRate]
			sl.egTarget = egDecayStart + int32(dl<<(5+egFracBits))
			if sl.egStep == 0 || dl == 0 {
				// D1R=0 or DL=0: skip to Decay2
				sl.egState = egDecay2
				d2r := (reg8 >> 11) & 0x1F
				d2EffRate := scspEffectiveRate(s, slotIdx, d2r)
				sl.egStep = egDecayStep[d2EffRate]
				sl.egTarget = egDecayEnd
			}

		case egDecay1:
			// Transition to Decay2
			d2r := (reg8 >> 11) & 0x1F
			d2EffRate := scspEffectiveRate(s, slotIdx, d2r)
			sl.egState = egDecay2
			sl.egStep = egDecayStep[d2EffRate]
			sl.egTarget = egDecayEnd
			if sl.egStep == 0 {
				sl.egTarget = egDecayEnd + 1 // Sustain forever
			}

		case egDecay2, egRelease:
			// EG saturates at max attenuation. State stays at egDecay2
			// or egRelease so MSLC SGC keeps reporting the spec-correct
			// 2 or 3 (never falls back to Attack via state truncation).
			// Release saturating means the slot has finished; clear
			// active so processSlots stops emitting output and skips
			// further work. Decay2 saturating does not deactivate -
			// the slot continues to play silently until key-off.
			sl.egLevel = egDecayEnd
			sl.egStep = 0
			sl.egTarget = egDecayEnd + 1
			if sl.egState == egRelease {
				sl.active = false
			}
		}
	}
}

// egAtten returns the slot's EG attenuation, with EGHOLD applied.
// During attack with EGHOLD set the slot outputs at zero attenuation
// regardless of the envelope counter; the envelope counter still
// advances and transitions to Decay1 when attack completes, after
// which the mask no longer applies.
func (s *SCSP) egAtten(slotIdx int) uint16 {
	sl := &s.slots[slotIdx]
	if sl.egState == egAttack && s.slotReg(slotIdx, 0x08)&0x20 != 0 {
		return 0
	}
	return egLevelToAtten(sl.egLevel)
}

// egLevelToAtten converts the 20-bit EG counter to a 10-bit attenuation value.
// Attack phase (0..egAttackEnd) uses a curve, decay phase is linear.
func egLevelToAtten(level int32) uint16 {
	if level <= egAttackStart {
		return 0x3FF // Max attenuation at start of attack
	}
	if level >= egDecayEnd {
		return 0x3FF // Max attenuation at end of decay
	}
	if level < egDecayStart {
		// Attack phase: non-linear curve (fast start, slow approach to full volume)
		pos := level >> egFracBits // 0..1023
		return egAttackCurve[pos]
	}
	// Decay/sustain/release: map egDecayStart..egDecayEnd to 0..1023
	pos := (level - egDecayStart) >> egFracBits // 0..1023
	if pos > 1023 {
		pos = 1023
	}
	return uint16(pos)
}

// ResetMixBuffer prepares the mix buffer for a new frame.
func (s *SCSP) ResetMixBuffer(capacity int) {
	if cap(s.mixBuffer) < capacity {
		s.mixBuffer = make([]int16, capacity)
	} else {
		s.mixBuffer = s.mixBuffer[:capacity]
	}
	for i := range s.mixBuffer {
		s.mixBuffer[i] = 0
	}
	s.mixPos = 0
}

// MixBuffer returns the interleaved stereo audio samples produced this frame.
func (s *SCSP) MixBuffer() []int16 {
	return s.mixBuffer[:s.mixPos]
}

// sdlPanToVolume computes left and right volume multipliers from a 3-bit
// send level (DISDL/EFSDL/IMXL, Tables 4.26/4.27/4.29) and 5-bit pan value
// (DIPAN/EFPAN, Tables 4.28/4.30). Returns values in 1.14 fixed-point.
func sdlPanToVolume(level, pan uint16) (int16, int16) {
	if level == 0 {
		return 0, 0
	}

	basev := int32(0x80) << level // 1.8 base level at 0 dB

	// Per-3-dB attenuation lookup from PDF Table 4.28. Applied as
	// 1.15 fixed-point multiplier: panv = basev * panLaw[low] / 32768.
	panv := (basev * panLaw[pan&0x0F]) >> 15

	if pan&0x10 != 0 {
		// Bit 4 set: attenuate right channel, left stays at 0 dB
		return int16(basev), int16(panv)
	}
	// Bit 4 clear: attenuate left channel, right stays at 0 dB
	return int16(panv), int16(basev)
}

// mixSlots combines all 32 slot outputs into a stereo sample pair and appends
// it to the mix buffer. Called once per sample tick after processSlots().
func (s *SCSP) mixSlots() {
	var mixL, mixR int32

	for i := 0; i < 32; i++ {
		out := s.slots[i].output
		if out == 0 {
			continue
		}

		regMix := s.slotReg(i, 0x16)
		disdl := (regMix >> 13) & 7
		dipan := (regMix >> 8) & 0x1F

		volL, volR := sdlPanToVolume(disdl, dipan)
		if volL == 0 && volR == 0 {
			continue
		}

		mixL += (int32(out) * int32(volL)) >> 14
		mixR += (int32(out) * int32(volR)) >> 14
	}

	// Mix DSP effect returns (EFREG outputs) using per-slot EFSDL/EFPAN
	for i := 0; i < 16; i++ {
		ef := s.dsp.efreg[i]
		if ef == 0 {
			continue
		}

		regMix := s.slotReg(i, 0x16)
		efsdl := (regMix >> 5) & 7
		efpan := regMix & 0x1F

		volL, volR := sdlPanToVolume(efsdl, efpan)
		if volL == 0 && volR == 0 {
			continue
		}

		mixL += (int32(ef) * int32(volL)) >> 14
		mixR += (int32(ef) * int32(volR)) >> 14
	}

	// Mix EXTS (external audio input, e.g. CD-DA) direct path per
	// SCSP User's Manual Table 4.31: EXTS[0] uses slot 16 MIXER reg,
	// EXTS[1] uses slot 17 MIXER reg. Same EFSDL/EFPAN routing as EFREG.
	for i := 0; i < 2; i++ {
		sample := s.dsp.exts[i]
		if sample == 0 {
			continue
		}

		regMix := s.slotReg(16+i, 0x16)
		efsdl := (regMix >> 5) & 7
		efpan := regMix & 0x1F

		volL, volR := sdlPanToVolume(efsdl, efpan)
		if volL == 0 && volR == 0 {
			continue
		}

		mixL += (sample * int32(volL)) >> 14
		mixR += (sample * int32(volR)) >> 14
	}

	// Apply master volume using multiply-based approach.
	// MVOL is 4 bits (0-15). Base = 0x2 << (MVOL >> 1), with 25% reduction
	// when the LSB is 0. Applied as (sample * mv) >> 8.
	mvol := s.regs[scspRegMVOL/2] & 0xF
	if mvol == 0 {
		mixL = 0
		mixR = 0
	} else {
		mv := int32(0x2) << (mvol >> 1)
		if mvol&1 == 0 {
			mv -= mv >> 2
		}
		mixL = (mixL * mv) >> 8
		mixR = (mixR * mv) >> 8
	}

	// Clamp to int16 range
	if mixL > 32767 {
		mixL = 32767
	} else if mixL < -32768 {
		mixL = -32768
	}
	if mixR > 32767 {
		mixR = 32767
	} else if mixR < -32768 {
		mixR = -32768
	}

	// Append to mix buffer
	if s.mixPos+1 < len(s.mixBuffer) {
		s.mixBuffer[s.mixPos] = int16(mixL)
		s.mixBuffer[s.mixPos+1] = int16(mixR)
		s.mixPos += 2
	}
}

// StartFrame initializes per-frame target counters. Emulator calls
// this at the start of each RunFrame with the per-frame system-cycle
// budget and the target sample and m68k cycle counts for the current
// region. Subsequent TickSystemCycles calls distribute these totals
// across the segments such that the per-frame totals land exactly.
func (s *SCSP) StartFrame(systemCyclesPerFrame, samplesPerFrame, m68kCyclesPerFrame uint32) {
	s.frameSystemCyclesTotal = int(systemCyclesPerFrame)
	s.frameSamplesTotal = int(samplesPerFrame)
	s.frameM68kTotal = int(m68kCyclesPerFrame)
	s.frameSystemCyclesElapsed = 0
	s.frameSamplesEmitted = 0
	s.frameM68kEmitted = 0
}

// TickSystemCycles advances the SCSP by the given number of system
// cycles. Computes the cumulative expected sample and m68k counts
// based on system-cycles-elapsed-this-frame versus the frame target,
// and emits only the delta since the previous call. This produces
// exact per-frame totals (set by StartFrame) with zero fractional
// drift, and a smooth per-call distribution independent of how the
// emulator chooses to slice the frame into segments.
func (s *SCSP) TickSystemCycles(cycles uint32) {
	if cycles == 0 || s.frameSystemCyclesTotal == 0 {
		return
	}

	s.frameSystemCyclesElapsed += int(cycles)

	// m68k: drain the delta before sample processing so register
	// writes land before the samples they affect.
	expectedM68k := int(int64(s.frameSystemCyclesElapsed) * int64(s.frameM68kTotal) / int64(s.frameSystemCyclesTotal))
	deltaM68k := expectedM68k - s.frameM68kEmitted
	s.frameM68kEmitted = expectedM68k
	if !s.inReset && deltaM68k > 0 {
		for deltaM68k > 0 {
			used := s.m68k.StepCycles(deltaM68k)
			if used == 0 {
				break
			}
			deltaM68k -= used
		}
	}

	// Samples
	expectedSamples := int(int64(s.frameSystemCyclesElapsed) * int64(s.frameSamplesTotal) / int64(s.frameSystemCyclesTotal))
	deltaSamples := expectedSamples - s.frameSamplesEmitted
	s.frameSamplesEmitted = expectedSamples
	if deltaSamples > 0 {
		s.TickSamples(deltaSamples)
	}
}

// TickSamples advances the SCSP by the given number of 44.1 KHz sample
// ticks, updating timers, slot state, DSP, and mixer output. Does NOT
// drain the sound CPU; use TickSystemCycles for the full emulator-facing
// interface. This entry point is exposed for tests that drive SCSP by
// sample count directly.
func (s *SCSP) TickSamples(samples int) {
	if samples <= 0 {
		return
	}

	timerRegOff := [3]uint32{scspRegTimerA, scspRegTimerB, scspRegTimerC}
	timerIntBit := [3]uint16{scspIntTimerA, scspIntTimerB, scspIntTimerC}

	for i := 0; i < samples; i++ {
		// 1-sample interrupt: SCIPD/MCIPD latch the source regardless
		// of SCIEB/MCIEB enable state. SCIEB only gates whether the
		// pending bit asserts the IRQ line (handled in
		// checkSoundInterrupt / checkMainInterrupt).
		s.regs[scspRegSCIPD/2] |= scspIntSample
		s.regs[scspRegMCIPD/2] |= scspIntSample

		// Pull one stereo pair from the CD audio source into EXTS.
		// Empty queue or no source: silent for this tick.
		s.dsp.exts[0] = 0
		s.dsp.exts[1] = 0
		if s.cdAudio != nil {
			if l, r, ok := s.cdAudio.PopAudioSample(); ok {
				s.dsp.exts[0] = int32(l)
				s.dsp.exts[1] = int32(r)
			}
		}

		// Clear DSP MIXS inputs for this sample tick
		for m := range s.dsp.mixs {
			s.dsp.mixs[m] = 0
		}

		// Process all 32 slots (generates outputs, accumulates into MIXS)
		s.processSlots()

		// Run DSP effects processor (reads MIXS, writes EFREG)
		s.runDSP()

		// Mix direct slot outputs and DSP effect returns
		s.mixSlots()

		// Advance all three timers (free-running from power on).
		// Per SCSP User's Manual Sec 4.2 page 93: interrupt fires when the
		// counter reaches FFH. Formula: (255 - TIMx) * count_cycle.
		for t := 0; t < 3; t++ {
			ctl := (s.regs[timerRegOff[t]/2] >> 8) & 7
			div := uint16(1) << ctl

			s.timerPrescaler[t]++
			if s.timerPrescaler[t] >= div {
				s.timerPrescaler[t] = 0
				if s.timerCounter[t] == 0xFF {
					s.timerCounter[t] = 0
				} else {
					s.timerCounter[t]++
					if s.timerCounter[t] == 0xFF {
						s.regs[scspRegSCIPD/2] |= timerIntBit[t]
						s.regs[scspRegMCIPD/2] |= timerIntBit[t]
					}
				}
			}
		}

		s.checkSoundInterrupt()
		s.checkMainInterrupt()
	}
}

// checkSoundInterrupt evaluates SCIPD & SCIEB and delivers the highest
// priority interrupt to the MC68EC000 via auto-vectoring.
//
// Real SCSP drives the m68k IPL0/1/2 pins level-sensitively: the line
// stays asserted at the priority-encoded level while (SCIPD & SCIEB) is
// non-zero, and the m68k re-samples the line on every instruction
// boundary, re-taking the IRQ whenever SR.I drops below the asserted
// level. This routine therefore re-asserts the request on every call
// (rather than only on edge transitions) so the chip's pending-IRQ
// state stays fresh after each dispatch consumes it.
//
// Callers must invoke this on every state change that can affect the
// computed level: SCIPD set/clear, SCIEB write, SCILV0/1/2 write, SCIRE
// write, timer overflow, sample tick, DMA end.
func (s *SCSP) checkSoundInterrupt() {
	pending := s.regs[scspRegSCIPD/2] & s.regs[scspRegSCIEB/2]

	var bestLevel uint8
	if pending != 0 {
		scilv0 := s.regs[scspRegSCILV0/2]
		scilv1 := s.regs[scspRegSCILV1/2]
		scilv2 := s.regs[scspRegSCILV2/2]

		// Find the highest level among enabled+pending sources.
		// Per SCSP User's Manual Sec 4.2 Figure 4.65, SCILV0/1/2 are
		// 8-bit registers and bit 7 is shared by SCIPD bits 7-10
		// (Timer B, Timer C, MIDI-OUT, 1Fs). Higher bit numbers have
		// priority when levels tie.
		for bit := 10; bit >= 0; bit-- {
			mask := uint16(1) << uint(bit)
			if pending&mask == 0 {
				continue
			}
			scilvBit := bit
			if scilvBit > 7 {
				scilvBit = 7
			}
			scilvMask := uint16(1) << uint(scilvBit)
			lvl := uint8(0)
			if scilv0&scilvMask != 0 {
				lvl |= 1
			}
			if scilv1&scilvMask != 0 {
				lvl |= 2
			}
			if scilv2&scilvMask != 0 {
				lvl |= 4
			}
			if lvl > bestLevel {
				bestLevel = lvl
			}
		}
	}

	s.soundIntLevel = bestLevel
	if bestLevel > 0 {
		s.m68k.RequestInterrupt(bestLevel, nil)
	}
}

// checkMainInterrupt evaluates MCIPD & MCIEB and signals the SCU. Real
// SCSP holds the sound-request line to the SCU asserted while
// (MCIPD & MCIEB) is non-zero. Re-raise on every call so a missed
// edge-only delivery is not possible; the SCU's IRL handling is itself
// level-sensitive (see core/scu.go checkInterrupts) and tracks the
// pending bit until the master SH-2 acks it via AcknowledgeInterrupt.
func (s *SCSP) checkMainInterrupt() {
	pending := s.regs[scspRegMCIPD/2] & s.regs[scspRegMCIEB/2]
	active := pending != 0
	s.mainIntActive = active

	if active && s.scu != nil {
		s.scu.RaiseSoundRequest()
	}
}

// executeDMA performs an immediate DMA transfer between sound memory and registers.
func (s *SCSP) executeDMA() {
	dmeal := s.regs[scspRegDMEAL/2]
	dmeah := s.regs[scspRegDMEAH/2]
	ctl := s.regs[scspRegDMACtl/2]

	// Assemble 20-bit sound memory byte address
	dmea := uint32(dmeah>>12)<<16 | uint32(dmeal&0xFFFE)
	// Register byte offset
	drga := uint32(dmeah & 0x0FFE)
	// Word count from DTLG[11:1]
	wordCount := int((ctl & 0x0FFE) >> 1)

	dgate := ctl&0x4000 != 0
	ddir := ctl&0x2000 != 0

	for i := 0; i < wordCount; i++ {
		memAddr := (dmea + uint32(i)*2) & 0x7FFFE
		regAddr := drga + uint32(i)*2

		if regAddr >= scspRegSize {
			continue
		}

		if dgate {
			// DGATE: write zeros to destination
			if ddir {
				s.WriteRAM16(memAddr, 0)
			} else {
				s.regs[regAddr/2] = 0
			}
		} else if ddir {
			// DDIR=1: registers -> sound memory
			s.WriteRAM16(memAddr, s.regs[regAddr/2])
		} else {
			// DDIR=0: sound memory -> registers
			s.regs[regAddr/2] = s.ReadRAM16(memAddr)
		}
	}

	// Clear DEXE, preserve other bits
	s.regs[scspRegDMACtl/2] &^= 0x1000

	// Set DMA transfer end interrupt
	s.regs[scspRegSCIPD/2] |= scspIntDMA
	s.regs[scspRegMCIPD/2] |= scspIntDMA
	s.checkSoundInterrupt()
	s.checkMainInterrupt()
}

// ReadRAM reads a byte from sound RAM.
func (s *SCSP) ReadRAM(addr uint32) uint8 {
	return s.ram[addr&0x7FFFF]
}

// WriteRAM writes a byte to sound RAM.
func (s *SCSP) WriteRAM(addr uint32, val uint8) {
	s.ram[addr&0x7FFFF] = val
}

// ReadRAM16 reads a big-endian 16-bit value from sound RAM.
func (s *SCSP) ReadRAM16(addr uint32) uint16 {
	addr &= 0x7FFFE
	return uint16(s.ram[addr])<<8 | uint16(s.ram[addr+1])
}

// WriteRAM16 writes a big-endian 16-bit value to sound RAM.
func (s *SCSP) WriteRAM16(addr uint32, val uint16) {
	addr &= 0x7FFFE
	s.ram[addr] = uint8(val >> 8)
	s.ram[addr+1] = uint8(val)
}

// ReadRAM32 reads a big-endian 32-bit value from sound RAM.
func (s *SCSP) ReadRAM32(addr uint32) uint32 {
	addr &= 0x7FFFC
	return uint32(s.ram[addr])<<24 | uint32(s.ram[addr+1])<<16 |
		uint32(s.ram[addr+2])<<8 | uint32(s.ram[addr+3])
}

// WriteRAM32 writes a big-endian 32-bit value to sound RAM.
func (s *SCSP) WriteRAM32(addr uint32, val uint32) {
	addr &= 0x7FFFC
	s.ram[addr] = uint8(val >> 24)
	s.ram[addr+1] = uint8(val >> 16)
	s.ram[addr+2] = uint8(val >> 8)
	s.ram[addr+3] = uint8(val)
}

// M68KSerialize returns the sound CPU's full register/state block as
// produced by m68k.CPU.Serialize (D0-D7, A0-A7, PC, SR, USP, SSP, prevPC,
// etc.). Used by the memory dump to inspect 68K state.
func (s *SCSP) M68KSerialize() []byte {
	buf := make([]byte, m68k.SerializeSize)
	s.m68k.Serialize(buf)
	return buf
}

// InReset returns whether the 68K sound CPU is held in reset.
func (s *SCSP) InReset() bool {
	return s.inReset
}

// SetInReset sets the 68K reset state. When transitioning from reset
// to active, the 68K is reset to fetch its initial vectors.
func (s *SCSP) SetInReset(reset bool) {
	if s.inReset && !reset {
		s.m68k.Reset()
	}
	if reset {
		// Drain CD audio queue and clear EXTS so a clean buffer
		// is available when the sound CPU resumes.
		if s.cdAudio != nil {
			s.cdAudio.DrainAudio()
		}
		s.dsp.exts[0] = 0
		s.dsp.exts[1] = 0
	}
	s.inReset = reset
}

// SetCDAudioSource wires the CD block as the source for EXTS (external
// audio input). Called once at construction by the bus.
func (s *SCSP) SetCDAudioSource(src CDAudioSource) {
	s.cdAudio = src
}
