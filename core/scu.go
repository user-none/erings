// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// BusReadWriter provides the read/write interface needed by SCU DMA.
type BusReadWriter interface {
	Read8(addr uint32) uint8
	Read16(addr uint32) uint16
	Read32(addr uint32) uint32
	Write8(addr uint32, val uint8)
	Write16(addr uint32, val uint16)
	Write32(addr uint32, val uint32)
}

// SCU implements the System Control Unit for the Sega Saturn.
// It handles DMA transfers, interrupt masking, and routing to the master SH-2.
type SCU struct {
	// DMA registers (3 levels)
	dmaR  [3]uint32 // Read address
	dmaW  [3]uint32 // Write address
	dmaC  [3]uint32 // Transfer byte count
	dmaAD [3]uint32 // Address add value
	dmaEN [3]uint32 // DMA enable
	dmaMD [3]uint32 // DMA mode
	dstp  uint32    // DMA force-stop (write-only)

	// DMA pending state per level
	dmaPending [3]bool // Armed, waiting for start factor

	// DMA deferred interrupt delay (system cycles remaining, -1 = inactive)
	dmaDelay [3]int

	// DSP microcode processor
	dsp scuDSP

	// System-to-DSP cycle accumulator. The DSP runs at half the system
	// clock rate (Saturn Overview Manual p.15). Odd system cycles are
	// carried across Tick calls so the ratio stays exact.
	dspCycleCarry uint32

	// Timer registers
	t0c  uint32 // Timer 0 compare value (write-only)
	t1s  uint32 // Timer 1 line match value (write-only)
	t1md uint32 // Timer 1 mode (write-only): bit 0 = enable, bit 8 = mode

	// Timer state
	t0cnt uint32 // Timer 0 H-Blank counter (resets at VBlank-IN)

	// Interrupt control
	ims uint32 // Interrupt Mask (write-only)
	ist uint32 // Interrupt Status (R/W, write-0-to-clear)

	// A-Bus control
	aiak uint32 // A-Bus Interrupt Acknowledge
	asr0 uint32 // A-Bus Set (CS0, CS1) (write-only)
	asr1 uint32 // A-Bus Set (CS2, Dummy) (write-only)
	aref uint32 // A-Bus Refresh (write-only)

	// SCU control
	rsel uint32 // SDRAM Select

	// Interrupt delivery callbacks (IRL is level-triggered)
	setIRL   func(level uint8, vec uint16)
	clearIRL func()

	pendingBit int // IST bit currently asserted via IRL (-1 = none)

	// Bus reference for DMA transfers
	bus BusReadWriter
}

// intEntry describes a single SCU interrupt source.
type intEntry struct {
	level uint8
	vec   uint16
}

// intTable maps IST bit position to interrupt level and vector.
// Entries for bits 14-15 are unused (level 0, vec 0).
var intTable [32]intEntry

func init() {
	intTable[0] = intEntry{0xF, 0x40}  // V-Blank-IN
	intTable[1] = intEntry{0xE, 0x41}  // V-Blank-OUT
	intTable[2] = intEntry{0xD, 0x42}  // H-Blank-IN
	intTable[3] = intEntry{0xC, 0x43}  // Timer 0
	intTable[4] = intEntry{0xB, 0x44}  // Timer 1
	intTable[5] = intEntry{0xA, 0x45}  // DSP End
	intTable[6] = intEntry{0x9, 0x46}  // Sound Request
	intTable[7] = intEntry{0x8, 0x47}  // System Manager
	intTable[8] = intEntry{0x8, 0x48}  // PAD
	intTable[9] = intEntry{0x6, 0x49}  // Level 2 DMA End
	intTable[10] = intEntry{0x6, 0x4A} // Level 1 DMA End
	intTable[11] = intEntry{0x5, 0x4B} // Level 0 DMA End
	intTable[12] = intEntry{0x3, 0x4C} // DMA Illegal
	intTable[13] = intEntry{0x2, 0x4D} // Sprite Draw End
	// 14-15 unused

	// External interrupts 0-15 (bits 16-31)
	extLevels := [16]uint8{
		0x7, 0x7, 0x7, 0x7, // Ext 0-3
		0x4, 0x4, 0x4, 0x4, // Ext 4-7
		0x1, 0x1, 0x1, 0x1, // Ext 8-11
		0x1, 0x1, 0x1, 0x1, // Ext 12-15
	}
	for i := 0; i < 16; i++ {
		intTable[16+i] = intEntry{extLevels[i], uint16(0x50 + i)}
	}
}

// NewSCU allocates a new SCU with correct initial state.
func NewSCU() *SCU {
	s := &SCU{
		ims:        0xBFFF,
		pendingBit: -1,
		dmaDelay:   [3]int{-1, -1, -1},
	}
	for i := range 3 {
		s.dmaAD[i] = 0x101
		s.dmaMD[i] = 0x07
	}
	s.dsp.scu = s
	return s
}

// Reset clears pending interrupts and DMA state to power-on defaults.
// Called during CKCHG to match real hardware behavior.
func (s *SCU) Reset() {
	s.ist = 0
	s.ims = 0xBFFF
	s.pendingBit = -1
	for i := range 3 {
		s.dmaEN[i] = 0
		s.dmaAD[i] = 0x101
		s.dmaMD[i] = 0x07
		s.dmaR[i] = 0
		s.dmaW[i] = 0
		s.dmaC[i] = 0
		s.dmaDelay[i] = -1
		s.dmaPending[i] = false
	}
	s.dspCycleCarry = 0
	if s.clearIRL != nil {
		s.clearIRL()
	}
}

// TickSystemCycles advances DMA deferred interrupt countdowns by the
// given number of system cycles. When a countdown reaches zero the
// level is closed out via finishDMA, which raises the DMA-end
// interrupt and fires any pending start-factor trigger held during
// the transfer.
func (s *SCU) TickSystemCycles(cycles uint32) {
	for lvl := range 3 {
		if s.dmaDelay[lvl] < 0 {
			continue
		}
		s.dmaDelay[lvl] -= int(cycles)
		if s.dmaDelay[lvl] > 0 {
			continue
		}
		s.finishDMA(lvl)
	}

	if s.dsp.executing {
		s.dspCycleCarry += cycles
		budget := s.dspCycleCarry >> 1
		s.dspCycleCarry &= 1
		if budget > 0 {
			s.dsp.Step(budget)
		}
	}
}

// finishDMA closes out an in-flight transfer at the given level. The
// per-level countdown is cleared, the DMA-end interrupt is raised,
// and any pending start-factor trigger held during the transfer
// fires immediately. Called from TickSystemCycles when the system-cycle budget
// elapses, and from Write when a CPU register access drains the
// level early to match the documented timing.
func (s *SCU) finishDMA(lvl int) {
	s.dmaDelay[lvl] = -1
	s.RaiseInterrupt(dmaEndBit[lvl])

	if s.dmaPending[lvl] && s.dmaEN[lvl]&1 != 0 {
		s.dmaPending[lvl] = false
		if s.dmaMD[lvl]&(1<<24) != 0 {
			s.executeIndirectDMA(lvl)
		} else {
			s.executeDMA(lvl)
		}
	}
}

// SetBus gives the SCU a reference to the system bus for DMA transfers.
func (s *SCU) SetBus(bus BusReadWriter) {
	s.bus = bus
}

// readDSTA composes the DMA Status Register from live state. Only the
// per-level "in operation" (D*MV) bits are modeled - D*WT/D*BK and the
// DSP-side flags stay zero.
func (s *SCU) readDSTA() uint32 {
	var v uint32
	for lvl := range 3 {
		if s.dmaDelay[lvl] >= 0 {
			v |= 1 << (4 + 4*lvl)
		}
	}
	return v
}

// SetIRLHandler sets the callbacks used to deliver IRL interrupts
// to the master SH-2. IRL is level-triggered: setIRL drives the
// current interrupt level, clearIRL deasserts when no interrupt
// is pending.
func (s *SCU) SetIRLHandler(set func(level uint8, vec uint16), clr func()) {
	s.setIRL = set
	s.clearIRL = clr
}

// AcknowledgeInterrupt clears the IST bit for the currently dispatched
// interrupt. Called by the SH-2 when it accepts an external interrupt.
// This prevents the same event from firing repeatedly while allowing
// the IRL to stay asserted until acknowledged.
func (s *SCU) AcknowledgeInterrupt() {
	if s.pendingBit >= 0 {
		s.ist &^= 1 << s.pendingBit
		s.pendingBit = -1
		s.checkInterrupts()
	}
}

// Read returns the 32-bit value of the SCU register at the given offset.
// Write-only registers return 0. Unmapped offsets return 0.
func (s *SCU) Read(offset uint32) uint32 {
	// DMA levels: stride 0x20, registers at +0x00 to +0x14
	if offset <= 0x54 {
		lvl := int(offset / 0x20)
		reg := offset % 0x20
		if lvl < 3 {
			switch reg {
			case 0x00:
				return s.dmaR[lvl]
			case 0x04:
				return s.dmaW[lvl]
			case 0x08:
				return s.dmaC[lvl]
			case 0x0C, 0x10, 0x14:
				return 0 // write-only
			}
		}
		return 0
	}

	switch offset {
	case 0x7C:
		return s.readDSTA()
	case 0x80:
		return s.dsp.readPPAF()
	case 0x8C:
		return s.dsp.readPDD()
	case 0xA4:
		return s.ist
	case 0xA8:
		return s.aiak
	case 0xC4:
		return s.rsel
	case 0xC8:
		return 0x00000004 // Version register
	default:
		return 0
	}
}

// ReadInternal returns the stored value for any register, including
// write-only registers. Used by bus.go for byte-write composition.
func (s *SCU) ReadInternal(offset uint32) uint32 {
	if offset <= 0x54 {
		lvl := int(offset / 0x20)
		reg := offset % 0x20
		if lvl < 3 {
			switch reg {
			case 0x00:
				return s.dmaR[lvl]
			case 0x04:
				return s.dmaW[lvl]
			case 0x08:
				return s.dmaC[lvl]
			case 0x0C:
				return s.dmaAD[lvl]
			case 0x10:
				return s.dmaEN[lvl]
			case 0x14:
				return s.dmaMD[lvl]
			}
		}
		return 0
	}

	switch offset {
	case 0x60:
		return s.dstp
	case 0x7C:
		return s.readDSTA()
	case 0x80:
		return s.dsp.readPPAFInternal()
	case 0x84:
		return 0
	case 0x88:
		return uint32(s.dsp.pdaAddr)
	case 0x8C:
		bank := (s.dsp.pdaAddr >> 6) & 3
		addr := s.dsp.pdaAddr & 0x3F
		return s.dsp.data[bank][addr]
	case 0x90:
		return s.t0c
	case 0x94:
		return s.t1s
	case 0x98:
		return s.t1md
	case 0xA0:
		return uint32(s.ims)
	case 0xA4:
		return s.ist
	case 0xA8:
		return s.aiak
	case 0xB0:
		return s.asr0
	case 0xB4:
		return s.asr1
	case 0xB8:
		return s.aref
	case 0xC4:
		return s.rsel
	case 0xC8:
		return 0x00000004
	default:
		return 0
	}
}

// Write stores a 32-bit value to the SCU register at the given offset.
// Read-only registers (DSTA, VER) are ignored.
// IST uses write-0-to-clear semantics: writing 0 clears the bit,
// writing 1 maintains current status.
func (s *SCU) Write(offset uint32, val uint32) {
	// DMA levels: stride 0x20, registers at +0x00 to +0x14
	if offset <= 0x54 {
		lvl := int(offset / 0x20)
		reg := offset % 0x20
		if lvl < 3 {
			// SCU User's Manual section 3.2 states for every per-level
			// DMA register (DnR/DnW/DnC/DnAD/DnEN/DnMD) that "the
			// register of that level prohibits writing while DMA is
			// operating". Final Spec No. 23 reinforces this for DnMD
			// (*1) and DnAD (*2) with the note that "hang up occurs
			// if rewritten". On real hardware the in-flight transfer
			// completes ahead of the CPU's next bus-side write because
			// the DMA engine and the CPU compete for the same bus at
			// system-cycle rates. Our cycle-stepped model holds the
			// per-level countdown open across that window, so a CPU
			// register write landing during it would be impossible on
			// real HW. Drain the level here so the new programming
			// arrives at an idle channel, matching the documented
			// constraint.
			if s.dmaDelay[lvl] >= 0 {
				s.finishDMA(lvl)
			}
			switch reg {
			case 0x00:
				s.dmaR[lvl] = val
			case 0x04:
				s.dmaW[lvl] = val
			case 0x08:
				s.dmaC[lvl] = val
			case 0x0C:
				s.dmaAD[lvl] = val
			case 0x10:
				s.dmaEN[lvl] = val
				if val&1 != 0 {
					s.triggerDMA(lvl)
				}
			case 0x14:
				s.dmaMD[lvl] = val
			}
		}
		return
	}

	switch offset {
	case 0x60:
		s.dstp = val
	case 0x7C:
		// DSTA - read-only, ignore
	case 0x80:
		s.dsp.writePPAF(val)
	case 0x84:
		s.dsp.writePPD(val)
	case 0x88:
		s.dsp.writePDA(val)
	case 0x8C:
		s.dsp.writePDD(val)
	case 0x90:
		s.t0c = val
	case 0x94:
		s.t1s = val
	case 0x98:
		s.t1md = val
	case 0xA0:
		s.ims = val & 0xFFFF
		s.checkInterrupts()
	case 0xA4:
		s.ist &= val
		s.checkInterrupts()
	case 0xA8:
		s.aiak = val
	case 0xB0:
		s.asr0 = val
	case 0xB4:
		s.asr1 = val
	case 0xB8:
		s.aref = val
	case 0xC4:
		s.rsel = val
	case 0xC8:
		// VER - read-only, ignore
	}
}

// RaiseInterrupt sets the given IST bit and checks for pending interrupts.
func (s *SCU) RaiseInterrupt(bit int) {
	if bit < 0 || bit > 31 {
		return
	}
	s.ist |= 1 << bit
	s.checkInterrupts()
}

// RaiseVBlankIN raises the V-Blank-IN interrupt (bit 0)
// and checks for DMA start factor 0.
func (s *SCU) RaiseVBlankIN() {
	s.RaiseInterrupt(0)
	s.checkDMATrigger(0)
}

// RaiseVBlankOUT raises the V-Blank-OUT interrupt (bit 1),
// checks for DMA start factor 1, and resets the Timer 0 counter.
func (s *SCU) RaiseVBlankOUT() {
	s.t0cnt = 0
	s.RaiseInterrupt(1)
	s.checkDMATrigger(1)
}

// RaiseHBlankIN raises the H-Blank-IN interrupt (bit 2),
// checks for DMA start factor 2, and advances SCU timers.
// line is the current scanline number (0-based).
func (s *SCU) RaiseHBlankIN(line uint16) {
	s.RaiseInterrupt(2)
	s.checkDMATrigger(2)

	if s.t1md&1 == 0 {
		return // Timers disabled
	}

	// Timer 0: increment H-Blank counter, fire when it matches T0C
	s.t0cnt = (s.t0cnt + 1) & 0x3FF
	t0Fire := s.t0cnt == s.t0c&0x3FF
	if t0Fire {
		s.RaiseTimer0()
	}

	// Timer 1: fire when line matches T1S.
	// Mode 0 (t1md bit 8 = 0): fire independently on line match.
	// Mode 1 (t1md bit 8 = 1): fire only when Timer 0 also fires.
	if uint32(line) == s.t1s&0x1FF {
		if s.t1md&(1<<8) == 0 || t0Fire {
			s.RaiseTimer1()
		}
	}
}

// RaiseTimer0 raises the Timer 0 interrupt (bit 3)
// and checks for DMA start factor 3.
func (s *SCU) RaiseTimer0() {
	s.RaiseInterrupt(3)
	s.checkDMATrigger(3)
}

// RaiseTimer1 raises the Timer 1 interrupt (bit 4)
// and checks for DMA start factor 4.
func (s *SCU) RaiseTimer1() {
	s.RaiseInterrupt(4)
	s.checkDMATrigger(4)
}

// RaiseSystemManager raises the System Manager interrupt (bit 7).
func (s *SCU) RaiseSystemManager() { s.RaiseInterrupt(7) }

// RaiseSoundRequest raises the Sound Request interrupt (bit 6)
// and checks for DMA start factor 5.
func (s *SCU) RaiseSoundRequest() {
	s.RaiseInterrupt(6)
	s.checkDMATrigger(5)
}

// RaiseSpriteDrawEnd raises the Sprite Draw End interrupt (bit 13)
// and checks for DMA start factor 6.
func (s *SCU) RaiseSpriteDrawEnd() {
	s.RaiseInterrupt(13)
	s.checkDMATrigger(6)
}

// dmaReadAdd maps the read address add value (bit 8 of AD register)
// to byte increment. Table 3.2: 0=fixed, 1=+4 bytes.
var dmaReadAdd = [2]uint32{0, 4}

// dmaWriteAdd maps the write address add value (bits 0-2 of AD register)
// to byte increment per bus write, per Table 3.3 of the SCU user
// manual. The values are the stride applied for every actual write
// transaction. On the B-Bus (16-bit), each 32-bit transfer is two
// consecutive writes, so the destination advances by 2x this value per
// 32-bit unit; on the A-Bus or CPU side the destination advances by
// this value per unit. dmaTransfer applies the bus-width factor when
// selecting the effective per-unit stride.
var dmaWriteAdd = [8]uint32{0, 2, 4, 8, 16, 32, 64, 128}

// dmaEndBit maps DMA level to IST bit for DMA-end interrupt.
// Level 0 -> bit 11, Level 1 -> bit 10, Level 2 -> bit 9.
var dmaEndBit = [3]int{11, 10, 9}

// triggerDMA checks the start factor for the given level and either
// executes immediately (factor 7) or arms the DMA as pending.
func (s *SCU) triggerDMA(lvl int) {
	factor := s.dmaMD[lvl] & 0x07
	indirect := s.dmaMD[lvl]&(1<<24) != 0
	if factor == 7 {
		if indirect {
			s.executeIndirectDMA(lvl)
		} else {
			s.executeDMA(lvl)
		}
	} else {
		s.dmaPending[lvl] = true
	}
}

// checkDMATrigger is called when an interrupt event occurs that may
// match a pending DMA start factor. The factor values are:
// 0=V-Blank-IN, 1=V-Blank-OUT, 2=H-Blank-IN, 3=Timer0, 4=Timer1,
// 5=Sound Request, 6=Sprite Draw End.
func (s *SCU) checkDMATrigger(factor uint32) {
	for lvl := range 3 {
		if !s.dmaPending[lvl] {
			continue
		}
		if s.dmaEN[lvl]&1 == 0 {
			s.dmaPending[lvl] = false
			continue
		}
		lvlFactor := s.dmaMD[lvl] & 0x07
		if lvlFactor != factor {
			continue
		}
		// Per Final Spec No. 22, a trigger that lands while DMA is
		// operating is held and fires after the current transfer
		// ends. Leave dmaPending armed; TickSystemCycles replays it.
		if s.dmaDelay[lvl] >= 0 {
			continue
		}
		s.dmaPending[lvl] = false
		if s.dmaMD[lvl]&(1<<24) != 0 {
			s.executeIndirectDMA(lvl)
		} else {
			s.executeDMA(lvl)
		}
	}
}

// dmaTransfer performs a single DMA transfer with the given parameters.
// Returns the final source and destination addresses after transfer.
//
// Per SCU User's Manual Sec 2.1, the controller has an internal 4-byte
// buffer between the read side and the write side. Aligned long-word
// reads/writes are used in the middle; byte-unit accesses are used at
// any unaligned head or tail of either side. Source and destination
// alignment are independent.
//
// writeInc is the per-write-unit stride from Table 3.3. On the B-Bus
// (16-bit device side: SCSP, VDP1, VDP2) each 32-bit unit maps to two
// bus writes, so the destination effectively advances by 2*writeInc
// per long-word unit. On the A-Bus and CPU side the destination
// advances by writeInc per long-word unit. Byte-unit writes during an
// unaligned head or tail advance the destination by 1 byte each per
// the manual; the long-word stride only governs aligned long-word
// writes.
//
// Cycle accuracy: the caller charges count/4 system cycles for the
// transfer regardless of how many bytes were sent in byte units vs
// long-word units. This is a known approximation; real hardware spends
// extra cycles on byte transfers at the head/tail boundaries.
func (s *SCU) dmaTransfer(src, dst, count, readInc, writeInc uint32) (uint32, uint32) {
	if count == 0 {
		return src, dst
	}

	dstStep := writeInc
	if isBBus(dst) {
		dstStep = writeInc * 2
	}

	// Fast path: aligned src, aligned dst, count is a whole number of
	// long-words, and the destination stride matches one long-word
	// (no sparse writes). Behavior is bit-identical to the prior
	// implementation, so existing tests and cycle accounting remain
	// valid.
	if src%4 == 0 && dst%4 == 0 && count%4 == 0 &&
		(dstStep == 4 || dstStep == 0) &&
		(readInc == 4 || readInc == 0) {
		units := count / 4
		for i := uint32(0); i < units; i++ {
			s.bus.Write32(dst, s.bus.Read32(src))
			src += readInc
			dst += dstStep
		}
		return src, dst
	}

	// Sparse-stride path: dstStep > 4 means the destination has gaps
	// between long-word writes (writeInc 8/16/32/64/128). The manual
	// does not define byte-tail behavior for sparse strides, and
	// software pairs sparse strides with aligned multiple-of-4 counts
	// (e.g. VRAM cell-data scatter). Truncate to whole long-words.
	if dstStep > 4 {
		units := count / 4
		for i := uint32(0); i < units; i++ {
			s.bus.Write32(dst, s.bus.Read32(src))
			src += readInc
			dst += dstStep
		}
		return src, dst
	}

	// Slow path: byte-streaming through a 4-byte buffer per Sec 2.1.
	// Activates whenever count is not a multiple of 4, or src or dst
	// is not long-word aligned. The read side and the write side each
	// independently choose long-word vs byte access based on their
	// own alignment.
	var buf [4]byte
	var bufLen uint32
	var bytesRead uint32
	var bytesWritten uint32

	for bytesWritten < count {
		// Read phase: top up the buffer if there are bytes left to
		// read and the buffer has space. Use a long-word read when
		// src is aligned, the buffer is empty, and at least 4 bytes
		// remain to read; otherwise read a single byte. readInc=0
		// is the fixed-source case used by A-Bus FIFO peripherals;
		// src does not advance across reads.
		if bufLen < 4 && bytesRead < count {
			srcAligned := src%4 == 0
			haveFour := count-bytesRead >= 4
			if bufLen == 0 && srcAligned && haveFour {
				w := s.bus.Read32(src)
				buf[0] = byte(w >> 24)
				buf[1] = byte(w >> 16)
				buf[2] = byte(w >> 8)
				buf[3] = byte(w)
				bufLen = 4
				bytesRead += 4
				if readInc == 4 {
					src += 4
				}
			} else {
				buf[bufLen] = s.bus.Read8(src)
				bufLen++
				bytesRead++
				if readInc == 4 {
					src++
				}
			}
		}

		// Write phase: drain the buffer FIFO-order so byte i from
		// src lands at dst+i. Use a long-word write when dst is
		// aligned, the buffer is full, and at least 4 bytes remain
		// to write; otherwise write a single byte.
		if bufLen > 0 {
			dstAligned := dst%4 == 0
			haveFour := count-bytesWritten >= 4
			if bufLen == 4 && dstAligned && haveFour {
				w := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
				s.bus.Write32(dst, w)
				bufLen = 0
				bytesWritten += 4
				if dstStep != 0 {
					dst += 4
				}
			} else {
				s.bus.Write8(dst, buf[0])
				buf[0] = buf[1]
				buf[1] = buf[2]
				buf[2] = buf[3]
				bufLen--
				bytesWritten++
				if dstStep != 0 {
					dst++
				}
			}
		}
	}

	return src, dst
}

// isBBus reports whether the given address decodes to a B-Bus device
// (SCSP, VDP1, VDP2). The B-Bus is 16 bits wide, so SCU DMA 32-bit
// transfers to these regions take two bus cycles and double the
// effective write-address stride.
func isBBus(addr uint32) bool {
	masked := addr & 0x07FFFFFF
	return masked >= 0x05A00000 && masked < 0x05FC0000
}

// executeDMA performs a direct-mode DMA transfer for the given level.
// The transfer is executed instantly (not cycle-stepped).
func (s *SCU) executeDMA(lvl int) {
	if s.bus == nil {
		return
	}

	// AD register: bit 8 = read add (0=fixed, 1=+4), bits 0-2 = write add index
	writeIdx := s.dmaAD[lvl] & 0x07
	readIdx := (s.dmaAD[lvl] >> 8) & 0x01
	readInc := dmaReadAdd[readIdx]
	writeInc := dmaWriteAdd[writeIdx]

	// Count mask: Level 0 = 20-bit (1 MB), Levels 1-2 = 12-bit (4 KB).
	// A count register of 0 means the maximum transfer size: the field
	// cannot represent its own maximum (0xFFFFF = 1 MB-1, 0xFFF = 4 KB-1),
	// so 0 denotes the full 0x100000 / 0x1000 bytes (SCU User's Manual
	// Fig 3.3/3.4). Mirrors executeIndirectDMA's count==0 handling.
	count := s.dmaC[lvl]
	if lvl == 0 {
		count &= 0xFFFFF
		if count == 0 {
			count = 0x100000
		}
	} else {
		count &= 0xFFF
		if count == 0 {
			count = 0x1000
		}
	}

	src, dst := s.dmaTransfer(s.dmaR[lvl], s.dmaW[lvl], count, readInc, writeInc)

	s.dmaR[lvl] = src
	s.dmaW[lvl] = dst

	delay := int(count / 4)
	if delay < 1 {
		delay = 1
	}
	s.dmaDelay[lvl] = delay
}

// executeIndirectDMA performs an indirect-mode DMA transfer for the given level.
// The transfer table is read from the address in the Write Address register.
// Each table entry is 3 longwords: byte count, destination, source.
// Bit 31 of the source address marks the final entry.
func (s *SCU) executeIndirectDMA(lvl int) {
	if s.bus == nil {
		return
	}

	// AD register: bit 8 = read add (0=fixed, 1=+4), bits 0-2 = write add index
	writeIdx := s.dmaAD[lvl] & 0x07
	readIdx := (s.dmaAD[lvl] >> 8) & 0x01
	readInc := dmaReadAdd[readIdx]
	writeInc := dmaWriteAdd[writeIdx]

	tableAddr := s.dmaW[lvl]

	// Level 0 supports 20-bit count, levels 1-2 support 18-bit count.
	var countMask uint32
	if lvl == 0 {
		countMask = 0xFFFFF
	} else {
		countMask = 0x3FFFF
	}

	var totalCount uint32
	for entries := 0; entries < 4096; entries++ {
		countRaw := s.bus.Read32(tableAddr)
		dstRaw := s.bus.Read32(tableAddr + 4)
		srcRaw := s.bus.Read32(tableAddr + 8)

		count := countRaw & countMask
		dst := dstRaw & 0x07FFFFFF
		src := srcRaw & 0x07FFFFFF
		last := srcRaw&0x80000000 != 0

		if count == 0 {
			if lvl == 0 {
				count = 0x100000
			} else {
				count = 0x2000
			}
		}

		s.dmaTransfer(src, dst, count, readInc, writeInc)
		totalCount += count

		if last {
			break
		}
		tableAddr += 0x0C
	}

	s.dmaW[lvl] = tableAddr

	delay := int(totalCount / 4)
	if delay < 1 {
		delay = 1
	}
	s.dmaDelay[lvl] = delay
}

// checkInterrupts finds the highest-priority unmasked pending interrupt
// and drives the IRL level to the master SH-2. When no interrupt is
// pending, the IRL line is deasserted. This models the level-triggered
// behavior of the real IRL pins.
func (s *SCU) checkInterrupts() {
	if s.setIRL == nil {
		return
	}

	// IMS bits 0-15: 1 = interrupt masked, 0 = interrupt enabled.
	// External interrupts (16-31) are not maskable.
	mask := uint32(s.ims & 0xFFFF)
	pending := s.ist & ^mask

	if pending == 0 {
		s.clearIRL()
		return
	}

	// Find highest priority pending interrupt.
	// On tie (same level), lowest bit number wins.
	bestBit := -1
	bestLevel := uint8(0)
	for i := 0; i < 32; i++ {
		if pending&(1<<i) == 0 {
			continue
		}
		entry := intTable[i]
		if entry.level == 0 && entry.vec == 0 {
			continue // unused bit
		}
		if entry.level > bestLevel {
			bestLevel = entry.level
			bestBit = i
		}
	}

	if bestBit >= 0 {
		s.pendingBit = bestBit
		s.setIRL(intTable[bestBit].level, intTable[bestBit].vec)
	} else {
		s.clearIRL()
	}
}
