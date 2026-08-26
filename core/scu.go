// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// BusReadWriter provides the read/write interface needed by SCU DMA.
type BusReadWriter interface {
	Read8(addr uint32) uint8
	Read16(addr uint32) uint16
	Read32(addr uint32) uint32
	DMAWrite8(addr uint32, val uint8)
	DMAWrite16(addr uint32, val uint16)
	DMAWrite32(addr uint32, val uint32)
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
	t1s  uint32 // Timer 1 set data: countdown loaded at each H-Blank-IN (write-only)
	t1md uint32 // Timer 1 mode (write-only): bit 0 = enable, bit 8 = mode

	// Timer state
	t0cnt uint32 // Timer 0 H-Blank counter (resets at VBlank-OUT)

	// Timer 1 countdown in system cycles (-1 = inactive). Per SCU manual
	// Sec 2.2 the set-data register is loaded into the counter at each
	// H-Blank-IN and decremented at the dot clock (about 1/4 the system
	// clock); the Timer 1 interrupt occurs when it reaches 0. t1Gate
	// records whether expiry raises the interrupt: always in mode 0,
	// only on the Timer 0 match line in mode 1.
	t1Delay int
	t1Gate  bool

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

	// irlWithheld models undocumented SCU behavior: after the SH-2
	// acknowledges an interrupt (external vector fetch), the SCU
	// asserts no new IRL until software next writes IMS. Sources
	// raised in that span only latch in IST; the IMS write resumes
	// evaluation and asserts the highest pending source. The SCU
	// manual does not describe what happens between the vector fetch
	// and the mask write. This behavior is established by software
	// that requires it: the BIOS common interrupt dispatcher (ROM
	// $0F00) writes IMS only after a preemptible register-save/
	// vector-lookup prologue, and X-MEN VS STREET FIGHTER deadlocks
	// when V-Blank-IN preempts that prologue during a Level-0 DMA-End
	// dispatch. Because its in a V-Blank-IN chain then waits forever on
	// flag $0600A820 that only the suspended handler ($0600A780)
	// clears.
	irlWithheld bool

	// reqPending models undocumented SCU behavior. A source raised
	// while unmasked may be blocked from asserting by a held or
	// withheld assertion. That request stays committed to delivery
	// and a software IST clear does not cancel it. Established by
	// software that requires it: a title's V-Blank-IN handler raises
	// a Level-2 DMA end behind a held assertion, clears IST two
	// instructions later, and deadlocks unless the completion is
	// still delivered. Bits clear when the source asserts or is
	// acknowledged. Sources raised while masked latch only in IST.
	reqPending uint32

	// Bus reference for DMA transfers
	bus BusReadWriter

	// lockBus is the concrete system bus when available, used for the
	// areaSCU serialization lock. Nil under register-level unit tests
	// that wire a fake BusReadWriter; locking is skipped there.
	lockBus *Bus
}

// levelVec holds a single SCU interrupt source's priority level and vector.
type levelVec struct {
	level uint8
	vec   uint16
}

// bitToLevelVec maps an IST bit position to its interrupt level and vector.
// Entries for bits 14-15 are unused (level 0, vec 0).
var bitToLevelVec [32]levelVec

// vecToBit recovers the IST bit for an SCU interrupt vector (0x40-0x5F), or
// -1 for an unmapped vector. It is the inverse of bitToLevelVec's vector
// field, built at init so the two stay in sync from one definition. Note the
// reversal is not 1:1: this direction recovers only the bit, dropping the
// level that bitToLevelVec also carries.
var vecToBit [0x60]int8

func init() {
	bitToLevelVec[0] = levelVec{0xF, 0x40}  // V-Blank-IN
	bitToLevelVec[1] = levelVec{0xE, 0x41}  // V-Blank-OUT
	bitToLevelVec[2] = levelVec{0xD, 0x42}  // H-Blank-IN
	bitToLevelVec[3] = levelVec{0xC, 0x43}  // Timer 0
	bitToLevelVec[4] = levelVec{0xB, 0x44}  // Timer 1
	bitToLevelVec[5] = levelVec{0xA, 0x45}  // DSP End
	bitToLevelVec[6] = levelVec{0x9, 0x46}  // Sound Request
	bitToLevelVec[7] = levelVec{0x8, 0x47}  // System Manager
	bitToLevelVec[8] = levelVec{0x8, 0x48}  // PAD
	bitToLevelVec[9] = levelVec{0x6, 0x49}  // Level 2 DMA End
	bitToLevelVec[10] = levelVec{0x6, 0x4A} // Level 1 DMA End
	bitToLevelVec[11] = levelVec{0x5, 0x4B} // Level 0 DMA End
	bitToLevelVec[12] = levelVec{0x3, 0x4C} // DMA Illegal
	bitToLevelVec[13] = levelVec{0x2, 0x4D} // Sprite Draw End
	// 14-15 unused

	// External interrupts 0-15 (bits 16-31)
	extLevels := [16]uint8{
		0x7, 0x7, 0x7, 0x7, // Ext 0-3
		0x4, 0x4, 0x4, 0x4, // Ext 4-7
		0x1, 0x1, 0x1, 0x1, // Ext 8-11
		0x1, 0x1, 0x1, 0x1, // Ext 12-15
	}
	for i := 0; i < 16; i++ {
		bitToLevelVec[16+i] = levelVec{extLevels[i], uint16(0x50 + i)}
	}

	for i := range vecToBit {
		vecToBit[i] = -1
	}
	for bit := range bitToLevelVec {
		e := bitToLevelVec[bit]
		if e.level == 0 && e.vec == 0 {
			continue // unused bit
		}
		vecToBit[e.vec] = int8(bit)
	}
}

// NewSCU allocates a new SCU with correct initial state.
func NewSCU() *SCU {
	s := &SCU{
		ims:        0xBFFF,
		pendingBit: -1,
		dmaDelay:   [3]int{-1, -1, -1},
		t1Delay:    -1,
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
	s.irlWithheld = false
	s.reqPending = 0
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
	s.t1Delay = -1
	s.t1Gate = false
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
	s.lockIRQ()
	defer s.unlockIRQ()
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

	if s.t1Delay >= 0 {
		s.t1Delay -= int(cycles)
		if s.t1Delay <= 0 {
			s.t1Delay = -1
			if s.t1Gate {
				s.raiseTimer1()
			}
		}
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
	s.raiseInterrupt(dmaEndBit[lvl])

	if s.dmaPending[lvl] && s.dmaEN[lvl]&0x100 != 0 {
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
	s.lockBus, _ = bus.(*Bus)
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

// AcknowledgeInterrupt clears the IST bit for the interrupt the SH-2
// dispatched, identified by its vector. It clears by the accepted vector
// The acknowledge also drops the IRL line and withholds further IRL assertion
// until the next IMS write (see irlWithheld); interrupts raised meanwhile stay
// latched in IST.
func (s *SCU) AcknowledgeInterrupt(vec uint16) {
	s.lockIRQ()
	if int(vec) < len(vecToBit) {
		if bit := vecToBit[vec]; bit >= 0 {
			s.ist &^= 1 << bit
			s.reqPending &^= 1 << bit
		}
	}
	s.pendingBit = -1
	s.irlWithheld = true
	s.clearIRLLine()
	s.unlockIRQ()
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
				// SCU User's Manual Fig 3.9 + Table 3.4: the enable bit
				// (bit 8) arms the DMA. For event start factors (0-6) the
				// transfer fires when the selected signal arrives.
				if s.dmaMD[lvl]&0x07 == 7 {
					if val&1 != 0 {
						s.triggerDMA(lvl)
					}
				} else if val&0x100 != 0 {
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
		if val&1 == 0 {
			s.t1Delay = -1
		}
	case 0xA0:
		s.ims = val & 0xFFFF
		s.irlWithheld = false
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

// The SCU's interrupt and DMA state is mutated from several goroutines
// (raster raises on the main goroutine, timer/DMA/sound raises on the
// secondary worker, sprite-draw-end on the VDP walker, register access
// from either CPU), so every public entry point serializes on the
// areaSCU bus lock - the same lock the bus dispatch claims for SCU
// register access. Internal (lowercase) variants assume the lock is
// already held; the acquisition order is always SCU before the bus
// areas the DMA engine touches, and callers of the public methods
// must hold no bus-area lock (components defer raises out of their
// register-write paths to their tick paths for this reason).
//
// The lock guards the register file, not the data a DMA moves.
// executeDMA / executeIndirectDMA release areaSCU for the bus-master
// copy (which touches no register state) and reacquire it only for the
// register writeback, so a transfer arbitrates per-bus rather than
// freezing the register file for its full duration.

// lockIRQ / unlockIRQ claim the SCU's serialization domain. The nil
// check keeps register-level unit tests that never wire a bus working.
func (s *SCU) lockIRQ() {
	if s.lockBus != nil {
		s.lockBus.lockArea(areaSCU)
	}
}

func (s *SCU) unlockIRQ() {
	if s.lockBus != nil {
		s.lockBus.unlockArea(areaSCU)
	}
}

// raiseInterrupt sets the given IST bit and checks for pending
// interrupts. Callers hold the SCU lock.
func (s *SCU) raiseInterrupt(bit int) {
	if bit < 0 || bit > 31 {
		return
	}
	s.ist |= 1 << bit
	// External interrupts (bits 16-31) are not maskable; bits 0-15
	// consult IMS. Unmasked raises join the committed-delivery queue;
	// checkInterrupts consumes the bit if it asserts right away.
	if bit >= 16 || s.ims&(1<<bit) == 0 {
		s.reqPending |= 1 << bit
	}
	s.checkInterrupts()
}

// RaiseInterrupt sets the given IST bit and checks for pending interrupts.
func (s *SCU) RaiseInterrupt(bit int) {
	s.lockIRQ()
	s.raiseInterrupt(bit)
	s.unlockIRQ()
}

// RaiseVBlankIN raises the V-Blank-IN interrupt (bit 0)
// and checks for DMA start factor 0.
func (s *SCU) RaiseVBlankIN() {
	s.lockIRQ()
	s.raiseInterrupt(0)
	s.checkDMATrigger(0)
	s.unlockIRQ()
}

// RaiseVBlankOUT raises the V-Blank-OUT interrupt (bit 1),
// checks for DMA start factor 1, and resets the Timer 0 counter.
func (s *SCU) RaiseVBlankOUT() {
	s.lockIRQ()
	s.t0cnt = 0
	s.raiseInterrupt(1)
	s.checkDMATrigger(1)
	s.unlockIRQ()
}

// RaiseHBlankIN raises the H-Blank-IN interrupt (bit 2),
// checks for DMA start factor 2, and advances SCU timers.
// line is the current scanline number (0-based).
func (s *SCU) RaiseHBlankIN(line uint16) {
	s.lockIRQ()
	defer s.unlockIRQ()
	s.raiseInterrupt(2)
	s.checkDMATrigger(2)

	if s.t1md&1 == 0 {
		return // Timers disabled
	}

	// Timer 0: fire when the counter matches T0C, then increment.
	t0Fire := s.t0cnt == s.t0c&0x3FF
	s.t0cnt = (s.t0cnt + 1) & 0x3FF
	if t0Fire {
		s.raiseTimer0()
	}

	// Timer 1: the set-data value is loaded into the countdown at every
	// H-Blank-IN and decremented at the dot clock, about 1/4 the system
	// clock; the interrupt occurs when the value is 0 (SCU manual
	// Sec 2.2), so a set data of 0 fires at the H-Blank itself. Mode 0
	// (T1MD bit 8 = 0) delivers it every line - the per-scanline raster
	// interrupt - while mode 1 delivers it only on the line where
	// Timer 0 matched.
	s.t1Gate = s.t1md&(1<<8) == 0 || t0Fire
	if delay := int(s.t1s&0x1FF) * 4; delay == 0 {
		s.t1Delay = -1
		if s.t1Gate {
			s.raiseTimer1()
		}
	} else {
		s.t1Delay = delay
	}
}

// raiseTimer0 raises the Timer 0 interrupt (bit 3)
// and checks for DMA start factor 3. Callers hold the SCU lock.
func (s *SCU) raiseTimer0() {
	s.raiseInterrupt(3)
	s.checkDMATrigger(3)
}

// raiseTimer1 raises the Timer 1 interrupt (bit 4)
// and checks for DMA start factor 4. Callers hold the SCU lock.
func (s *SCU) raiseTimer1() {
	s.raiseInterrupt(4)
	s.checkDMATrigger(4)
}

// RaiseSystemManager raises the System Manager interrupt (bit 7).
func (s *SCU) RaiseSystemManager() {
	s.lockIRQ()
	s.raiseInterrupt(7)
	s.unlockIRQ()
}

// RaiseSoundRequest raises the Sound Request interrupt (bit 6)
// and checks for DMA start factor 5.
func (s *SCU) RaiseSoundRequest() {
	s.lockIRQ()
	s.raiseInterrupt(6)
	s.checkDMATrigger(5)
	s.unlockIRQ()
}

// RaiseSpriteDrawEnd raises the Sprite Draw End interrupt (bit 13)
// and checks for DMA start factor 6.
func (s *SCU) RaiseSpriteDrawEnd() {
	s.lockIRQ()
	s.raiseInterrupt(13)
	s.checkDMATrigger(6)
	s.unlockIRQ()
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
		if s.dmaEN[lvl]&0x100 == 0 {
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

	if !scuDMAAccessible(src) || !scuDMAAccessible(dst) {
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
			s.bus.DMAWrite32(dst, s.bus.Read32(src))
			src += readInc
			dst += dstStep
		}
		return src, dst
	}

	// Sparse-stride path: dstStep > 4 means the destination has gaps
	// between write stops (writeInc 4/8/16/32/64/128). The manual does
	// not define byte-tail behavior for sparse strides, and software
	// pairs sparse strides with aligned multiple-of-4 counts (e.g.
	// VRAM cell-data scatter). Truncate to whole long-words.
	if dstStep > 4 {
		units := count / 4
		if isBBus(dst) {
			// The B-Bus write unit is 16 bits (SCU manual Sec 4.5),
			// so the write-add value advances the destination per
			// halfword. Each halfword of a source long word lands at
			// its own stop.
			for i := uint32(0); i < units; i++ {
				w := s.bus.Read32(src)
				s.bus.DMAWrite16(dst, uint16(w>>16))
				dst += writeInc
				s.bus.DMAWrite16(dst, uint16(w))
				dst += writeInc
				src += readInc
			}
			return src, dst
		}
		for i := uint32(0); i < units; i++ {
			s.bus.DMAWrite32(dst, s.bus.Read32(src))
			src += readInc
			dst += dstStep
		}
		return src, dst
	}

	// Slow path: byte-streaming through the controller buffer per
	// Sec 2.1. Activates whenever count is not a multiple of 4, or
	// src or dst is not long-word aligned. Read granularity is set by
	// the source alignment alone: long-word when 4-aligned, one
	// 16-bit bus cycle when 2-aligned, byte reads only for a
	// byte-misaligned source. A readInc=0 A-Bus FIFO source (CD data
	// register) must never be byte-read: each byte read of the
	// register base consumes a full FIFO word and its other byte is
	// lost. The write side drains the buffer independently, so byte i
	// read from the source lands at dst+i.
	var buf [8]byte
	var bufLen uint32
	var bytesRead uint32
	var bytesWritten uint32

	for bytesWritten < count {
		if bytesRead < count {
			switch {
			case src%4 == 0 && count-bytesRead >= 4 && bufLen <= 4:
				w := s.bus.Read32(src)
				buf[bufLen] = byte(w >> 24)
				buf[bufLen+1] = byte(w >> 16)
				buf[bufLen+2] = byte(w >> 8)
				buf[bufLen+3] = byte(w)
				bufLen += 4
				bytesRead += 4
				if readInc == 4 {
					src += 4
				}
			case src%2 == 0 && bufLen <= 6:
				v := s.bus.Read16(src)
				buf[bufLen] = byte(v >> 8)
				buf[bufLen+1] = byte(v)
				bufLen += 2
				bytesRead += 2
				if readInc == 4 {
					src += 2
				}
			case src%2 != 0 && bufLen < 8:
				buf[bufLen] = s.bus.Read8(src)
				bufLen++
				bytesRead++
				if readInc == 4 {
					src++
				}
			}
		}

		// Write phase: use a long-word write when dst is aligned, the
		// buffer holds at least 4 bytes, and at least 4 bytes remain
		// to write; otherwise write a single byte.
		if bufLen > 0 {
			if bufLen >= 4 && dst%4 == 0 && count-bytesWritten >= 4 {
				w := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
				s.bus.DMAWrite32(dst, w)
				copy(buf[:], buf[4:])
				bufLen -= 4
				bytesWritten += 4
				if dstStep != 0 {
					dst += 4
				}
			} else {
				s.bus.DMAWrite8(dst, buf[0])
				copy(buf[:], buf[1:])
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

// scuDMAAccessible reports whether an address falls within the bus spaces
// the SCU-DMA controller can reach: the A-Bus external areas (CS0, CS1,
// CS2 and A-Bus I/O), the B-Bus (VDP1, VDP2, SCSP), and Work RAM-H
// (C-Bus). The SH-2 CPU-local low memory (boot ROM, SMPC, backup RAM,
// Work RAM-L) and the SCU register space are not reachable by DMA.
//
// SCU Final Specifications and Precautions (ST-210-110194): No. 04 states
// Work RAM-H is the only Work RAM usable by SCU-DMA (Work RAM-L cannot be
// used), and the read/write address-add-value tables in No. 16/18/19
// enumerate the reachable spaces as Work RAM-H, the A-Bus external areas,
// and the B-Bus only. A transfer touching any other region is the
// DMA-illegal condition the controller cannot perform.
func scuDMAAccessible(addr uint32) bool {
	a := addr & 0x07FFFFFF
	switch {
	case a >= 0x02000000 && a < 0x05A00000: // A-Bus external areas 1-4
		return true
	case a >= 0x05A00000 && a < 0x05FC0000: // B-Bus (VDP1, VDP2, SCSP)
		return true
	case a >= 0x06000000: // Work RAM-H (C-Bus), mirrored through 0x07FFFFFF
		return true
	default:
		return false
	}
}

// executeDMA performs a direct-mode DMA transfer for the given level.
// The transfer is executed instantly (not cycle-stepped). The caller
// holds areaSCU on entry; the copy phase drops it and reacquires it
// for the register writeback (see the domain comment above).
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

	srcAddr := s.dmaR[lvl]
	dstAddr := s.dmaW[lvl]

	// The transfer is a bus-master copy that touches no SCU register
	// state - only the locals captured above and the per-access bus
	// locks dmaTransfer claims. Drop areaSCU for its duration so the
	// register file arbitrates per-bus instead of staying frozen for the
	// whole transfer, then reacquire for the writeback below.
	s.unlockIRQ()
	src, dst := s.dmaTransfer(srcAddr, dstAddr, count, readInc, writeInc)
	s.lockIRQ()

	// Save vs Update: the D0MD read/write address update bits (D0RUP bit
	// 16, D0WUP bit 8) select whether each address register keeps its set
	// value after the transfer (Save, bit=0) or advances to the post
	// transfer end address (Update, bit=1). SCU User's Manual Sec 2.3 (DMA
	// operation, D0MD address update bits) and Fig 3.10. A game that
	// re-fires a DMA in Save mode expects the same set address each time.
	if s.dmaMD[lvl]&(1<<16) != 0 {
		s.dmaR[lvl] = src
	}
	if s.dmaMD[lvl]&(1<<8) != 0 {
		s.dmaW[lvl] = dst
	}

	delay := int(count / 4)
	if delay < 1 {
		delay = 1
	}
	s.dmaDelay[lvl] = delay
}

// executeIndirectDMA performs an indirect-mode DMA transfer for the given level.
// The transfer table is read from the address in the Write Address register.
// Each table entry is 3 longwords: byte count, destination, source.
// Bit 31 of the source address marks the final entry. The caller holds
// areaSCU on entry; the table-walk copy drops it and reacquires it for
// the register writeback (see the domain comment above).
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

	// Bus-master copy: the table walk reads entries through the bus and
	// dmaTransfer copies each block, none of which touches SCU register
	// state. Drop areaSCU for the walk and reacquire for the writeback.
	s.unlockIRQ()
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

	s.lockIRQ()

	// Indirect Save vs Update (SCU User's Manual Sec 2.3): the write
	// address update bit (D0WUP, bit 8) selects whether the table-access
	// pointer is saved at its set value (Save, re-walks the same table on
	// the next trigger) or advanced past the walked entries (Update).
	if s.dmaMD[lvl]&(1<<8) != 0 {
		s.dmaW[lvl] = tableAddr
	}

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
// clearIRLLine drops the IRL output if a CPU is wired. Callers hold
// the SCU lock.
func (s *SCU) clearIRLLine() {
	if s.clearIRL != nil {
		s.clearIRL()
	}
}

func (s *SCU) checkInterrupts() {
	if s.setIRL == nil {
		return
	}
	if s.irlWithheld {
		return
	}
	// An asserted interrupt holds its level and vector until the SH-2
	// acknowledges it. SH7604 HW manual Sec 5.6 Figure 5.9 shows a
	// request level held through pin-level changes until acceptance,
	// with a later higher-priority request waiting behind it. The
	// manual documents that hold on the SH-2 input side. Modeling it
	// here in the SCU, where software IMS/IST writes landing before
	// the acknowledge cannot retract or replace the assertion, is
	// undocumented behavior established by the same title reqPending
	// covers. Together with irlWithheld the output stage runs
	// assert -> hold -> ack -> withhold -> IMS write -> assert next.
	if s.pendingBit >= 0 {
		return
	}

	// IMS bits 0-15: 1 = interrupt masked, 0 = interrupt enabled.
	// External interrupts (16-31) are not maskable.
	mask := uint32(s.ims & 0xFFFF)
	pending := (s.ist | s.reqPending) & ^mask

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
		entry := bitToLevelVec[i]
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
		s.reqPending &^= 1 << bestBit
		s.setIRL(bitToLevelVec[bestBit].level, bitToLevelVec[bestBit].vec)
	} else {
		s.clearIRL()
	}
}
