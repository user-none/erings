// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "sync/atomic"

// BusActivity describes what bus operation the CPU performed on a clock cycle.
type BusActivity uint8

const (
	BusNone  BusActivity = iota // No bus access
	BusRead                     // Memory read
	BusWrite                    // Memory write
	BusHeld                     // Bus held for atomic operation (TAS.B)
)

// ClockState describes what the CPU did during a Clock() call.
type ClockState struct {
	Bus          BusActivity // Bus activity this clock cycle
	LoadUseStall bool        // Next instruction stalls on this load result
	BranchTaken  bool        // A branch was resolved (taken) this clock cycle
}

// Pending op identifiers for multi-cycle instruction decomposition.
const (
	popNone      uint8 = iota
	popStall           // Generic stall cycles (BT/BF refill, SLEEP tail, MUL)
	popSTCL            // STC.L @-Rn: cycle 2 write
	popLDCL            // LDC.L @Rm+,CR: cycle 2 read, cycle 3 WB
	popMemRMW          // AND.B/OR.B/XOR.B: cycle 2 logic, cycle 3 write
	popTAS             // TAS.B: cycles 2-4 (read/held/write)
	popRTE             // RTE: cycles 2-3 (read PC, read SR)
	popTRAPA           // TRAPA: cycles 2-8
	popException       // serviceException: cycles 2-5
	popMACW            // MAC.W: cycle 2 (second read + accumulate)
	popMACL            // MAC.L: cycle 2 (second read + accumulate)
)

// CPU implements the Hitachi SH-2 processor core.
type CPU struct {
	reg    Registers
	bus    Bus
	cycles uint64

	// frameCyc is this CPU's current frame-relative system cycle, set by
	// the emulator each step. Passed into the contended bus access methods
	// so inter-CPU contention is timed against a clock both CPUs share.
	// Kept on the CPU (written only by its own goroutine) rather than the
	// shared bus to avoid false sharing on a per-cycle write.
	frameCyc int64

	ir uint16 // Current instruction register (raw opcode)

	halted    bool // Set by SLEEP, cleared by interrupt
	addrError bool // Set when address error occurs during instruction
	prevPC    uint32

	// Delayed branch state
	delayPC uint32 // Target PC for delayed branch
	inDelay bool   // Currently executing a delay slot

	// NMI is unmaskable and always level 16, vector 11. Tracked
	// independently of the IPR-level sources because it bypasses all
	// priority comparisons. Other on-chip interrupt sources are
	// latched by their peripherals (FTCSR, DVCR, CHCR.TE) and
	// surfaced through INTC.pending + peripheral IRQAsserted() queries.
	nmiPending bool

	// nmiReq holds an NMI asserted from another goroutine. It is folded
	// into nmiPending on this CPU's own thread (Clock) so the NMI state
	// is only ever mutated by the owner, like the IRL line. NMIRequest
	// sets it.
	nmiReq atomic.Bool

	// intInhibit blocks maskable-interrupt acceptance on the
	// instruction immediately following an "interrupt-disabled"
	// instruction per manual Sec 4.6.2. One-shot: set by LDC/LDC.L/
	// STC/STC.L/LDS/LDS.L/STS/STS.L handlers, cleared on the next
	// processInterrupt call. NMI is unaffected (checked above this
	// flag in processInterrupt).
	intInhibit bool

	// External IRL interrupt state (level-triggered, from SCU). Level
	// and vector are packed into one atomic word (level<<16 | vec):
	// the SCU asserts and deasserts the line from whichever thread
	// raised the interrupt, and the CPU samples it every cycle on its
	// own thread - a packed word can never yield a torn level/vector
	// pair.
	irl    atomic.Uint32
	irlAck func(vec uint16) // Called with the accepted vector when the SH-2 accepts an IRL interrupt

	// Multi-cycle micro-op state
	pendingOp    uint8  // which pending operation (popNone = ready)
	pendingStep  uint8  // sub-step within pending op (0-based)
	pendingCount uint8  // total remaining steps
	pendingN     uint8  // saved register index / sub-type
	pendingAddr  uint32 // saved address
	pendingVal   uint32 // saved value 1 (read result, SR, etc.)
	pendingVal2  uint32 // saved value 2 (PC for exception/TRAPA)
	pendingImm   uint32 // saved immediate

	// Load-use hazard tracking
	lastLoadReg  uint8  // GPR destination of previous load (0xFF = none)
	deferredOp   uint16 // opcode deferred by load-use stall
	hasDeferred  bool   // a deferred instruction is waiting
	loadUseStall bool   // set during stall cycle, read by Clock()

	// multiplierBusyUntil is the absolute cycles value at which the
	// background multiplier "mm" stages of the preceding MUL/MAC
	// instruction finish. While cycles < multiplierBusyUntil, a
	// subsequent MUL/MAC stalls until the multiplier is free. See
	// Programming Manual Section 7.2.3. Does not track stalls from
	// LDS/STS to MACH/MACL or CLRMAC; that is an accepted limitation
	// documented in the package README.
	multiplierBusyUntil uint64

	// Per-clock-cycle state set by instruction execution, read by Clock()
	stepBus     BusActivity
	branchTaken bool

	// On-chip peripherals
	intc INTC
	frt  FRT
	divu DIVU
	dmac DMAC
	wdt  WDT

	// nextPeripheralEvent is the earliest absolute CPU cycle at which
	// any on-chip peripheral (FRT or WDT) has work due. It is the cached
	// min of frt.nextEvent and wdt.nextEvent so the per-cycle deadline
	// gate is a single compare. Kept in sync via recomputeFRTEvent /
	// recomputeWDTEvent and the tail of tickPeripherals.
	nextPeripheralEvent uint64

	// On-chip cache (SH7604 manual Section 8): 4-way x 64 entries x
	// 16-byte lines. cacheData is the 4 KB data array, way-major
	// (way*1024 + entry*16 + byte), shared by cached lines and the
	// direct data-array region per Section 8.4.8 Figure 8.10. Cache
	// contents are NOT initialized by a reset (Section 8.5.1) - only
	// CCR is cleared - so none of these are touched in Reset.
	cacheData [4096]byte
	// cacheTags is entry-major so one set's four ways share a single
	// 16-byte row: tag address bits A28-A10 with the valid bit packed
	// into bit 0 (free below the tag field). An invalid line keeps its
	// tag with bit 0 clear, as hardware keeps tags across purges.
	cacheTags [64][4]uint32
	cacheLRU  [64]uint8 // 6-bit pseudo-LRU per entry (Section 8.4.5)
	ccr       uint8     // Cache Control Register (0xFFFFFE92)

	// fetchLineAddr/Way/Off memoize the last cached instruction
	// fetch's line resolution, so sequential fetches within one
	// 16-byte line (typically 7 of 8) skip the 4-way tag search. The
	// memo holds the resolved location, not data - write hits update
	// the data array in place and stay visible through it. It is
	// invalidated by anything that can change what the line address
	// resolves to: fills, purges, address-array writes, reset.
	// fetchLineInvalid (odd) never matches a real line address.
	fetchLineAddr uint32
	fetchLineWay  int
	fetchLineOff  uint32

	// busStall is bus-access wait-state debt (in CPU cycles) accumulated
	// by the current instruction's external accesses: cache misses (line
	// fills), cache-through and uncached accesses, and write-through
	// writes (the CPU waits for the internal-bus write to complete,
	// Section 8.4.2). Cache hits cost nothing (Section 8.4.1: one cycle,
	// pipelined). Drained one cycle per Clock() before the next
	// instruction executes.
	// External bus-access wait states (region cost + slave arbitration +
	// inter-CPU contention) are computed by the Bus implementation and
	// returned from the SH2* access methods.
	busStall uint32

	// sbycr is the Standby Control Register (0xFFFFFE91). Stub
	// storage only: writes persist, reads return the stored value.
	// STBY/SLEEP behavior driven by bit 7 (SBY) is NOT modeled -
	// the Saturn only uses SBYCR during CKCHG clock switching and
	// doesn't observe the standby semantics. Bit 5 is reserved per
	// manual Sec 14.2.1; writable mask is 0xDF.
	sbycr uint8

	// BSC (Bus State Controller) registers - persist across manual reset
	bcr1     uint16
	isMaster bool // true for master SH-2 (BCR1 MASTER bit = 0)

	// Trace callback: called with (pc, opcode) before each instruction
	TraceFunc func(pc uint32, op uint16)

	// HLEHook, if non-nil, is invoked from the execute loop when the
	// fetched PC falls within the HLE BIOS magic address range. The
	// hook is responsible for performing the emulated service in Go.
	// The bus returns RTS (+ NOP delay slot) at these addresses, so
	// the CPU unwinds to the caller normally after the hook returns.
	// Set by HLEBIOS.Boot when the emulator is started without a real
	// BIOS image; nil otherwise.
	HLEHook func(pc uint32)
}

// New creates a CPU connected to the given bus in power-on state.
// master indicates whether this is the master (true) or slave (false) SH-2.
// SR has all interrupts masked. Call LoadResetVectors() once the bus
// has valid data to set the initial PC and SP.
func New(bus Bus, master bool) *CPU {
	c := &CPU{bus: bus, lastLoadReg: 0xFF, isMaster: master,
		fetchLineAddr: fetchLineInvalid}
	c.reg.SR = srIMask
	c.bcr1 = 0x03F0 // initial value per hardware manual Section 7.2.1
	c.intc.Reset()
	c.frt.Reset()
	c.frt.lastSync = c.cycles
	c.recomputeFRTEvent()
	c.divu.Reset()
	c.dmac.Reset()
	c.dmac.bus = bus
	c.wdt.Reset()
	c.wdt.lastSync = c.cycles
	c.recomputeWDTEvent()
	return c
}

// LoadResetVectors reads the initial PC from address 0x00000000 and
// SP from address 0x00000004.
func (c *CPU) LoadResetVectors() {
	c.reg.PC = c.bus.Read32(0x00000000)
	c.reg.R[15] = c.bus.Read32(0x00000004)
}

// Reset returns the CPU to its power-on state, clearing programmer-visible
// registers and on-chip peripherals, then reloading PC/SP from the reset
// vectors. This is what real hardware does when the slave receives SSHON
// (after SSHOFF) - the reset line is pulsed and the CPU re-enters its
// reset-vector boot sequence. Games rely on this to re-read their
// game-supplied slave entry pointer after updating it.
func (c *CPU) Reset() {
	c.reg = Registers{}
	c.reg.SR = srIMask
	c.ir = 0
	c.halted = false
	c.addrError = false
	c.inDelay = false
	c.delayPC = 0
	c.prevPC = 0
	c.nmiPending = false
	c.nmiReq.Store(false)
	c.intInhibit = false
	c.irl.Store(0)
	c.pendingOp = popNone
	c.pendingStep = 0
	c.pendingCount = 0
	c.hasDeferred = false
	c.deferredOp = 0
	c.loadUseStall = false
	c.lastLoadReg = 0xFF
	c.multiplierBusyUntil = 0
	c.stepBus = BusNone
	c.branchTaken = false
	c.ccr = 0
	c.busStall = 0
	c.fetchLineAddr = fetchLineInvalid
	c.sbycr = 0
	c.bcr1 = 0x03F0
	c.intc.Reset()
	c.frt.Reset()
	c.frt.lastSync = c.cycles
	c.recomputeFRTEvent()
	c.divu.Reset()
	c.dmac.Reset()
	c.wdt.Reset()
	c.wdt.lastSync = c.cycles
	c.recomputeWDTEvent()
	c.LoadResetVectors()
}

// Clock advances the CPU by exactly one cycle and returns per-cycle state.
func (c *CPU) Clock() ClockState {
	c.addrError = false
	c.loadUseStall = false

	// Cycle-steal DMAC transfer: the bus write returns to the CPU after
	// each transfer unit (HM Sec 9.5, Bus Modes, Fig 9.10), so the CPU
	// keeps executing while the bus-occupation countdown runs in the
	// background.
	if c.dmac.Active() && !c.dmac.Stalling() {
		if ch := c.dmac.Tick(); ch >= 0 {
			c.routeDMACInterrupt(ch)
		}
	}

	if c.pendingOp != popNone {
		c.lastLoadReg = 0xFF
		return c.stepPending()
	}

	// Bus-access wait states: drain the stall debt the previous instruction
	// accumulated before executing the next one. Like load-use stall, no
	// interrupt window is exposed mid-access, so this returns before the
	// interrupt check below.
	if c.busStall > 0 {
		c.busStall--
		c.cycles++
		if c.peripheralsDue() {
			c.tickPeripherals()
		}
		return ClockState{}
	}

	// DMAC bus stall: CPU cannot execute while DMA transfer is in progress.
	if c.dmac.Stalling() {
		c.cycles++
		if c.peripheralsDue() {
			c.tickPeripherals()
		}
		if ch := c.dmac.Tick(); ch >= 0 {
			c.routeDMACInterrupt(ch)
		}
		return ClockState{}
	}

	c.stepBus = BusNone
	c.branchTaken = false

	// Fold a cross-thread NMI request into the on-thread NMI state
	// between instructions. Load before CAS: this runs every cycle and
	// the flag is almost never set, while a failed CAS is still a locked
	// read-modify-write that takes exclusive ownership of a cache line
	// another thread writes.
	if c.nmiReq.Load() && c.nmiReq.CompareAndSwap(true, false) {
		c.NMI()
	}

	// Don't accept interrupts during load-use stall cycles.
	// On real SH-2, pipeline stalls resolve internally without
	// exposing an interrupt window.
	if !c.hasDeferred && c.interruptPending() && c.processInterrupt() {
		c.lastLoadReg = 0xFF
		if c.peripheralsDue() {
			c.tickPeripherals()
		}
		bus := c.stepBus
		c.stepBus = BusNone
		return ClockState{
			Bus:         bus,
			BranchTaken: c.branchTaken,
		}
	}

	if c.halted {
		// SLEEP: CPU is halted until an interrupt wakes it.
		// The normal interrupt check at the top of Clock()
		// handles wake-up: processInterrupt clears halted and
		// sets up the exception. Nothing additional needed here.
		c.cycles++
		if c.peripheralsDue() {
			c.tickPeripherals()
		}
		return ClockState{}
	}

	c.execute()
	if c.peripheralsDue() {
		c.tickPeripherals()
	}
	bus := c.stepBus
	c.stepBus = BusNone
	bt := c.branchTaken
	c.branchTaken = false

	return ClockState{
		Bus:          bus,
		LoadUseStall: c.loadUseStall,
		BranchTaken:  bt,
	}
}

// stepPending continues a multi-cycle instruction in progress.
func (c *CPU) stepPending() ClockState {
	c.cycles++
	c.pendingStep++
	if c.peripheralsDue() {
		c.tickPeripherals()
	}
	var bus BusActivity

	switch c.pendingOp {
	case popStall:
		bus = BusNone
	case popSTCL:
		bus = c.stepSTCL()
	case popLDCL:
		bus = c.stepLDCL()
	case popMemRMW:
		bus = c.stepMemRMW()
	case popTAS:
		bus = c.stepTAS()
	case popRTE:
		bus = c.stepRTE()
	case popTRAPA:
		bus = c.stepTRAPA()
	case popException:
		bus = c.stepException()
	case popMACW:
		bus = c.stepMACW()
	case popMACL:
		bus = c.stepMACL()
	}

	c.pendingCount--
	if c.pendingCount == 0 {
		c.pendingOp = popNone
	}

	return ClockState{Bus: bus}
}

// setPending sets up a multi-cycle pending operation.
func (c *CPU) setPending(op uint8, count uint8) {
	c.pendingOp = op
	c.pendingStep = 0
	c.pendingCount = count
}

func (c *CPU) stepSTCL() BusActivity {
	// Cycle 2: MA write
	c.Write32(c.pendingAddr, c.pendingVal)
	return BusWrite
}

func (c *CPU) stepLDCL() BusActivity {
	reg := c.pendingN & 0x0F
	crType := c.pendingN >> 4
	switch c.pendingStep {
	case 1: // Cycle 2: MA read
		c.pendingVal = c.Read32(c.pendingAddr)
		return BusRead
	case 2: // Cycle 3: WB - write to target CR, post-increment
		switch crType {
		case 0:
			c.reg.SR = c.pendingVal & srMask
		case 1:
			c.reg.GBR = c.pendingVal
		case 2:
			c.reg.VBR = c.pendingVal
		}
		c.reg.R[reg] = c.pendingAddr + 4
	}
	return BusNone
}
func (c *CPU) stepMemRMW() BusActivity {
	switch c.pendingStep {
	case 1: // Cycle 2: EX - apply logic op
		switch c.pendingN {
		case 0: // AND
			c.pendingVal &= c.pendingImm
		case 1: // OR
			c.pendingVal |= c.pendingImm
		case 2: // XOR
			c.pendingVal ^= c.pendingImm
		}
		return BusNone
	case 2: // Cycle 3: MA - write result
		c.Write8(c.pendingAddr, uint8(c.pendingVal))
		return BusWrite
	}
	return BusNone
}

// InTAS reports whether a TAS.B read-modify-write is in progress. TAS is
// the only operation that keeps a bus claim held across cycles (SH7604
// Sec 7.10), so a scheduler running the two CPUs on separate threads must
// not park this CPU at a cross-thread sync point mid-TAS: the other CPU
// would stall on the held claim while this one waits, deadlocking. The
// scheduler lets the TAS finish (releasing the claim) before waiting.
func (c *CPU) InTAS() bool {
	return c.pendingOp == popTAS
}

func (c *CPU) stepTAS() BusActivity {
	switch c.pendingStep {
	case 1: // Cycle 2: MA read, test, set T
		val := c.tasRead8(c.pendingAddr)
		c.reg.SetTVal(val == 0)
		c.pendingVal = uint32(val)
		return BusHeld
	case 2: // Cycle 3: MA internal (modify)
		c.pendingVal |= 0x80
		return BusHeld
	case 3: // Cycle 4: MA write
		c.tasWrite8(c.pendingAddr, uint8(c.pendingVal))
		return BusHeld
	}
	return BusNone
}

// tasRead8 / tasWrite8 mirror read8 / write8 but route the external
// access through the bus's read-modify-write pair so the bus claim
// taken at TAS's read cycle is held until its write cycle completes.
// The filtering must stay symmetric with stepTAS: an address that
// resolves on-chip at the read resolves the same way at the write, so
// a claim is never left unbalanced.
func (c *CPU) tasRead8(addr uint32) uint8 {
	if isOnChip(addr) {
		v, _ := c.readOnChip(addr)
		return onChipByte(addr, v)
	}
	switch addr >> 29 {
	case 2, 3, 4, 5: // purge/address-array/reserved: no data
		return 0
	case 6: // data array
		return c.cacheData[addr&0xFFF]
	}
	// TAS reads are cache-through even in the cache area - the address
	// tag is not compared (SH7604 manual Section 8.4.4).
	v, stall := c.bus.SH2RMWRead(addr, c.frameCyc, !c.isMaster)
	c.busStall += stall
	return v
}

func (c *CPU) tasWrite8(addr uint32, val uint8) {
	if isOnChip(addr) {
		c.writeOnChip8(addr, val)
		return
	}
	switch addr >> 29 {
	case 0: // TAS's write compares the tag and updates the data array
		// on a hit (Section 8.4.4) before the memory write.
		if c.ccr&ccrCE != 0 {
			c.cacheWriteHit8(addr, val)
		}
	case 2, 3, 4, 5:
		return
	case 6:
		c.cacheData[addr&0xFFF] = val
		return
	}
	c.busStall += c.bus.SH2RMWWrite(addr, val, c.frameCyc)
}
func (c *CPU) stepRTE() BusActivity {
	switch c.pendingStep {
	case 1: // Cycle 2: MA - read PC from stack
		c.pendingVal = c.Read32(c.pendingAddr)
		c.reg.R[15] = c.pendingAddr + 4
		return BusRead
	case 2: // Cycle 3: MA - read SR from stack, set up delay branch
		c.reg.SR = c.Read32(c.pendingAddr+4) & srMask
		c.reg.R[15] = c.pendingAddr + 8
		c.delayBranch(c.pendingVal)
		return BusRead
	}
	return BusNone
}
func (c *CPU) stepTRAPA() BusActivity {
	switch c.pendingStep {
	case 1: // Cycle 2: MA write SR
		c.reg.R[15] -= 4
		c.Write32(c.reg.R[15], c.pendingVal)
		return BusWrite
	case 2: // Cycle 3: MA write PC
		c.reg.R[15] -= 4
		c.Write32(c.reg.R[15], c.pendingVal2)
		return BusWrite
	case 3: // Cycle 4: EX vector calc
		c.pendingAddr = c.reg.VBR + (c.pendingImm << 2)
		return BusNone
	case 4: // Cycle 5: MA read vector
		c.reg.PC = c.Read32(c.pendingAddr)
		return BusRead
	default: // Cycles 6-8: pipeline refill
		return BusNone
	}
}
func (c *CPU) stepException() BusActivity {
	switch c.pendingStep {
	case 1: // Cycle 2: MA write SR
		c.reg.R[15] -= 4
		c.Write32(c.reg.R[15], c.pendingVal)
		return BusWrite
	case 2: // Cycle 3: MA write PC
		c.reg.R[15] -= 4
		c.Write32(c.reg.R[15], c.pendingVal2)
		return BusWrite
	case 3: // Cycle 4: MA read vector
		c.reg.PC = c.Read32(c.reg.VBR + c.pendingAddr*4)
		return BusRead
	case 4: // Cycle 5: pipeline refill
		return BusNone
	}
	return BusNone
}
func (c *CPU) stepMACW() BusActivity {
	// If pendingCount > 1, this is a stall step for multiplier
	// contention (Section 7.2.3). The real second MA and accumulate
	// run only on the final step (pendingCount == 1 at top, before
	// stepPending decrements).
	if c.pendingCount != 1 {
		return BusNone
	}

	// Cycle 2 (final step): read @Rm, post-increment, multiply-accumulate
	m := c.pendingN >> 4

	rmVal := int16(c.Read16(c.reg.R[m]))
	c.reg.R[m] += 2

	rnVal := int16(c.pendingVal)
	product := int64(int32(rnVal) * int32(rmVal))
	acc := int64(uint64(c.reg.MACH)<<32 | uint64(c.reg.MACL))
	acc += product

	if c.reg.S() {
		if acc > 0x7FFFFFFF {
			acc = 0x7FFFFFFF
		} else if acc < -0x80000000 {
			acc = -0x80000000
		}
	}

	c.reg.MACL = uint32(acc)
	c.reg.MACH = uint32(uint64(acc) >> 32)
	// mm runs 2 cycles after the second MA.
	c.multiplierBusyUntil = c.cycles + 2
	return BusRead
}

func (c *CPU) stepMACL() BusActivity {
	// If pendingCount > 1, this is a stall step for multiplier
	// contention (Section 7.2.3). The real second MA and accumulate
	// run only on the final step (pendingCount == 1 at top, before
	// stepPending decrements).
	if c.pendingCount != 1 {
		return BusNone
	}

	// Cycle 2 (final step): read @Rm, post-increment, multiply-accumulate
	m := c.pendingN >> 4

	rmVal := int32(c.Read32(c.reg.R[m]))
	c.reg.R[m] += 4

	rnVal := int32(c.pendingVal)
	product := int64(rnVal) * int64(rmVal)
	acc := int64(uint64(c.reg.MACH)<<32 | uint64(c.reg.MACL))
	acc += product

	if c.reg.S() {
		const max48 = int64(0x7FFFFFFFFFFF)
		const min48 = -int64(0x800000000000)
		if acc > max48 {
			acc = max48
		} else if acc < min48 {
			acc = min48
		}
	}

	c.reg.MACL = uint32(acc)
	c.reg.MACH = uint32(uint64(acc) >> 32)
	// mm runs 4 cycles after the second MA.
	c.multiplierBusyUntil = c.cycles + 4
	return BusRead
}

// Cycles returns the total number of cycles executed.
func (c *CPU) Cycles() uint64 {
	return c.cycles
}

// SetFrameCyc sets this CPU's current frame-relative system cycle, used to
// time inter-CPU bus contention. The emulator calls it before each step so the
// clock advances with the CPU; callers that drive Clock() directly and care
// about bus timing must advance it too, or repeated Work-RAM accesses will
// appear to contend with themselves (a stuck clock never clears its busyUntil).
func (c *CPU) SetFrameCyc(cyc int64) {
	c.frameCyc = cyc
}

// Halted returns true if the CPU is halted (via SLEEP instruction).
func (c *CPU) Halted() bool {
	return c.halted
}

// IRL returns the raw external interrupt-request word currently driven
// by the SCU (level<<16 | vector). Counterpart to SetIRL/ClearIRL.
func (c *CPU) IRL() uint32 {
	return c.irl.Load()
}

// Registers returns a snapshot of the current register state.
func (c *CPU) Registers() Registers {
	return c.reg
}

// SetPC sets the program counter.
func (c *CPU) SetPC(pc uint32) {
	c.reg.PC = pc
}

// SetReg sets general-purpose register Rn (n = 0..15). Used by
// external code (HLE BIOS, boot setup) to seed register state
// without going through an SH-2 instruction.
func (c *CPU) SetReg(n int, v uint32) {
	c.reg.R[n] = v
}

// SetVBR sets the vector base register.
func (c *CPU) SetVBR(v uint32) {
	c.reg.VBR = v
}

// SetSR sets the status register. Reserved bits are masked off so
// no LDC-equivalent invariant is broken.
func (c *CPU) SetSR(v uint32) {
	c.reg.SR = v & srMask
}

// SetGBR sets the global base register.
func (c *CPU) SetGBR(v uint32) {
	c.reg.GBR = v
}

// SetPR sets the procedure register. Used by HLE BIOS services
// that need a bus-served RTS at a magic address to land at a
// specific PC when the magic address was reached without a
// preceding JSR (which would have set PR for them).
func (c *CPU) SetPR(v uint32) {
	c.reg.PR = v
}

// SetCCR seeds the cache control register without going through an
// on-chip register write. Used by the HLE BIOS to reproduce the real
// BIOS handoff state (docs/bios/handoff_state.md: CCR = $01, cache
// enabled with all four ways purged). The CP semantics of a real CCR
// write apply: a set CP bit purges and self-clears (SH7604 manual
// Section 8.4.6).
func (c *CPU) SetCCR(v uint8) {
	if v&ccrCP != 0 {
		v &^= ccrCP
	}
	c.cachePurge()
	c.ccr = v & 0xDF
}

// FRT returns the CPU's free-running timer module. Used by HLE BIOS
// services that need to configure FRT registers on a CPU whose own
// setup code we're replacing with Go (e.g., enabling slave FRT
// input-capture interrupts when the real-BIOS slave-init code that
// would normally configure them is being skipped).
func (c *CPU) FRT() *FRT {
	return &c.frt
}

// INTC returns the CPU's interrupt controller. Used by HLE BIOS
// services that need to set vector / priority registers on a CPU
// whose own setup code we're replacing with Go.
func (c *CPU) INTC() *INTC {
	return &c.intc
}

// inhibitInterruptNext flags that the immediately-following
// instruction must not accept a maskable interrupt. Called by the
// interrupt-disabled instruction handlers (LDC/STC/LDS/STS and
// their .L variants) per manual Sec 4.6.2. NMI is not blocked.
func (c *CPU) inhibitInterruptNext() {
	c.intInhibit = true
}

// SetIRL updates the external IRL interrupt level driven by the SCU.
// IRL interrupts are level-triggered: the level reflects the current
// state of the IRL pins and is sampled each cycle. When the SCU clears
// the interrupt source, it calls ClearIRL to deassert the request.
func (c *CPU) SetIRL(level uint8, vec uint16) {
	c.irl.Store(uint32(level)<<16 | uint32(vec))
}

// SetIRLAck sets the callback invoked when the SH-2 accepts an IRL interrupt.
// The callback receives the vector the CPU dispatched.
func (c *CPU) SetIRLAck(f func(vec uint16)) {
	c.irlAck = f
}

// ClearIRL deasserts the external IRL interrupt request.
func (c *CPU) ClearIRL() {
	c.irl.Store(0)
}

// NMI requests a non-maskable interrupt and asserts the NMIL bit
// in the ICR register so that software can observe the NMI pin state.
// NMI always has priority level 16 and uses vector 11. Tracked
// independently of the IPR-level sources via a dedicated flag.
//
// Per manual Sec 9.2.6, an NMI input also sets DMAOR.NMIF (bit 1).
// transferReady gates new transfers on dmaor&0x07 == 0x01, so
// setting NMIF here prevents any subsequent CHCR/DMAOR-triggered
// DMA start until software writes 0 to DMAOR bit 1 to clear NMIF.
func (c *CPU) NMI() {
	c.intc.SetNMIL(true)
	c.nmiPending = true
	c.dmac.dmaor |= 0x02
}

// NMIRequest asserts an NMI from another goroutine. The CPU applies it
// on its own thread in Clock, so the register/INTC state NMI touches is
// only ever mutated by the owning thread.
func (c *CPU) NMIRequest() { c.nmiReq.Store(true) }

// fetchPC reads a 16-bit instruction at PC and advances PC by 2.
func (c *CPU) fetchPC() uint16 {
	if c.reg.PC&1 != 0 {
		c.addressError()
		return 0
	}
	val := c.fetchInstr(c.reg.PC)
	c.reg.PC += 2
	return val
}

// fetchInstr services an instruction fetch. Fetches in the cache area
// go through the cache when it is enabled (mixed instruction/data
// cache, SH7604 manual Section 8.1); fetches from the data array
// region execute from the on-chip RAM; everything else is an external
// access charged its region's wait states.
func (c *CPU) fetchInstr(addr uint32) uint16 {
	switch addr >> 29 {
	case 0: // cache area (Section 8.3 Table 8.2)
		if c.ccr&ccrCE != 0 {
			return c.cacheFetch16(addr)
		}
	case 6: // data array: code running from cache RAM
		off := addr & 0xFFE
		return uint16(c.cacheData[off])<<8 | uint16(c.cacheData[off+1])
	}
	v, stall := c.bus.SH2Read16(addr, c.frameCyc, !c.isMaster)
	c.busStall += stall
	return v
}

// Read8/Read16/Read32 and Write8/Write16/Write32 perform a data access
// exactly as a MOV would: partition routing per SH7604 manual Section 8.3
// Table 8.2, cache fill/update on the cache-area path, inter-CPU bus
// contention and stall charged to this CPU, and an address error raised on
// misalignment. They are side-effecting, not neutral peeks.
//
// Read16 reads a 16-bit value with alignment check.
func (c *CPU) Read16(addr uint32) uint16 {
	if addr&1 != 0 {
		c.addressError()
		return 0
	}
	if isOnChip(addr) {
		v, _ := c.readOnChip(addr)
		return uint16(v)
	}
	switch addr >> 29 {
	case 0: // cache area
		if c.ccr&ccrCE != 0 {
			return c.cacheRead16(addr)
		}
	case 1, 7: // cache-through; I/O area
	case 2, 3, 5: // purge/address-array (no data on reads)/reserved
		return 0
	case 4, 6: // data array (Section 8.4.8); partition 4 (0x80000000) is
		// Reserved (Table 7.3) but games access it out-of-bounds, where
		// hardware aliases it to the data array.
		off := addr & 0xFFE
		return uint16(c.cacheData[off])<<8 | uint16(c.cacheData[off+1])
	}
	v, stall := c.bus.SH2Read16(addr, c.frameCyc, !c.isMaster)
	c.busStall += stall
	return v
}

// read32 reads a 32-bit value with alignment check. Partition routing
// per SH7604 manual Section 8.3 Table 8.2.
func (c *CPU) Read32(addr uint32) uint32 {
	if addr&3 != 0 {
		c.addressError()
		return 0
	}
	if isOnChip(addr) {
		v, _ := c.readOnChip(addr)
		return v
	}
	switch addr >> 29 {
	case 0: // cache area
		if c.ccr&ccrCE != 0 {
			return c.cacheRead32(addr)
		}
	case 1, 7: // cache-through; I/O area
	case 2, 5: // purge area reads / reserved
		return 0
	case 3: // address array read (Section 8.4.9, longword only)
		return c.addressArrayRead(addr)
	case 4, 6: // data array (Section 8.4.8); partition 4 (0x80000000) is
		// Reserved (Table 7.3) but games access it out-of-bounds, where
		// hardware aliases it to the data array.
		off := addr & 0xFFC
		return uint32(c.cacheData[off])<<24 |
			uint32(c.cacheData[off+1])<<16 |
			uint32(c.cacheData[off+2])<<8 |
			uint32(c.cacheData[off+3])
	}
	v, stall := c.bus.SH2Read32(addr, c.frameCyc, !c.isMaster)
	c.busStall += stall
	return v
}

// write16 writes a 16-bit value with alignment check. Partition
// routing per SH7604 manual Section 8.3 Table 8.2. Cache area writes
// are write-through (Section 8.4.2).
func (c *CPU) Write16(addr uint32, val uint16) {
	if addr&1 != 0 {
		c.addressError()
		return
	}
	if isOnChip(addr) {
		c.writeOnChip(addr, uint32(val))
		return
	}
	switch addr >> 29 {
	case 0: // cache area: update the data array on hit, always write memory
		if c.ccr&ccrCE != 0 {
			c.cacheWriteHit16(addr, val)
		}
	case 1, 7: // cache-through; I/O area
	case 2: // associative purge (Section 8.4.7)
		c.associativePurge(addr)
		return
	case 3, 4, 5: // address array is longword only; reserved
		return
	case 6: // data array (Section 8.4.8)
		off := addr & 0xFFE
		c.cacheData[off] = uint8(val >> 8)
		c.cacheData[off+1] = uint8(val)
		return
	}
	c.busStall += c.bus.SH2Write16(addr, val, c.frameCyc, !c.isMaster)
}

// write32 writes a 32-bit value with alignment check. Partition
// routing per SH7604 manual Section 8.3 Table 8.2. Cache area writes
// are write-through (Section 8.4.2).
func (c *CPU) Write32(addr uint32, val uint32) {
	if addr&3 != 0 {
		c.addressError()
		return
	}
	if isOnChip(addr) {
		c.writeOnChip(addr, val)
		return
	}
	switch addr >> 29 {
	case 0: // cache area: update the data array on hit, always write memory
		if c.ccr&ccrCE != 0 {
			c.cacheWriteHit32(addr, val)
		}
	case 1, 7: // cache-through; I/O area
	case 2: // associative purge (Section 8.4.7)
		c.associativePurge(addr)
		return
	case 3: // address array write (Section 8.4.9, longword only)
		c.addressArrayWrite(addr, val)
		return
	case 4, 5: // reserved
		return
	case 6: // data array (Section 8.4.8)
		off := addr & 0xFFC
		c.cacheData[off] = uint8(val >> 24)
		c.cacheData[off+1] = uint8(val >> 16)
		c.cacheData[off+2] = uint8(val >> 8)
		c.cacheData[off+3] = uint8(val)
		return
	}
	c.busStall += c.bus.SH2Write32(addr, val, c.frameCyc, !c.isMaster)
}

// addressError triggers a CPU address error exception.
func (c *CPU) addressError() {
	c.addrError = true
	c.serviceException(vecCPUAddr)
}

// isOnChip returns true if the address is in the on-chip peripheral area.
func isOnChip(addr uint32) bool {
	// 0xFFFF8000-0xFFFFBFFF: SDRAM mode registers
	// 0xFFFFC000-0xFFFFFDFF: Reserved
	// 0xFFFFFE00-0xFFFFFFFF: On-chip peripheral modules
	return addr >= 0xFFFF8000
}

// read8 reads a byte, checking for on-chip peripheral addresses first.
// Partition routing per SH7604 manual Section 8.3 Table 8.2.
func (c *CPU) Read8(addr uint32) uint8 {
	if isOnChip(addr) {
		v, _ := c.readOnChip(addr)
		return onChipByte(addr, v)
	}
	switch addr >> 29 {
	case 0: // cache area
		if c.ccr&ccrCE != 0 {
			return c.cacheRead8(addr)
		}
	case 1, 7: // cache-through; I/O area
	case 2, 3, 5: // purge/address-array (no data on reads)/reserved
		return 0
	// Partition 4 (0x80000000-0x9FFFFFFF) is Reserved in the address map
	// (SH7604 manual Table 7.3, note: "Do not access reserved spaces, as
	// this will cause operating errors"). Games still reach it through
	// out-of-bounds accesses, where hardware aliases it to the cache data
	// array, so it is serviced here rather than returning no data.
	case 4, 6: // data array (Section 8.4.8)
		return c.cacheData[addr&0xFFF]
	}
	v, stall := c.bus.SH2Read8(addr, c.frameCyc, !c.isMaster)
	c.busStall += stall
	return v
}

// onChipByte extracts the correct byte from an on-chip register value
// based on address parity (big-endian). FRT and DRCR registers are
// natively 8-bit and returned directly. INTC registers are 16-bit
// (even=high byte, odd=low byte). DIVU/DMAC/BSC are 32-bit.
func onChipByte(addr, v uint32) uint8 {
	switch {
	case addr >= 0xFFFFFE10 && addr <= 0xFFFFFE19:
		return uint8(v) // FRT: native 8-bit
	case addr == 0xFFFFFE71 || addr == 0xFFFFFE72:
		return uint8(v) // DRCR: native 8-bit
	case addr >= 0xFFFFFE80 && addr <= 0xFFFFFE83:
		return uint8(v) // WDT: native 8-bit reads
	case addr == 0xFFFFFE91:
		return uint8(v) // SBYCR: native 8-bit
	case addr == 0xFFFFFE92:
		return uint8(v) // CCR: native 8-bit
	case (addr >= 0xFFFFFE60 && addr <= 0xFFFFFE69) ||
		(addr >= 0xFFFFFEE0 && addr <= 0xFFFFFEE5):
		// INTC: 16-bit registers, big-endian byte select.
		// Upper bound 0xFFFFFE69 covers VCRD's low byte (VCRD is
		// at 0xFFFFFE68 as a 16-bit register).
		if addr&1 == 0 {
			return uint8(v >> 8)
		}
		return uint8(v)
	default:
		// 32-bit registers (DIVU, DMAC, BSC), big-endian byte select
		shift := (3 - (addr & 3)) * 8
		return uint8(v >> shift)
	}
}

// write8 writes a byte, checking for on-chip peripheral addresses first.
func (c *CPU) Write8(addr uint32, val uint8) {
	if isOnChip(addr) {
		c.writeOnChip8(addr, val)
		return
	}
	switch addr >> 29 {
	case 0: // cache area: update the data array on hit, always write memory
		if c.ccr&ccrCE != 0 {
			c.cacheWriteHit8(addr, val)
		}
	case 1, 7: // cache-through; I/O area
	case 2: // associative purge (Section 8.4.7)
		c.associativePurge(addr)
		return
	case 3, 4, 5: // address array is longword only; reserved
		return
	case 6: // data array (Section 8.4.8)
		c.cacheData[addr&0xFFF] = val
		return
	}
	c.busStall += c.bus.SH2Write8(addr, val, c.frameCyc, !c.isMaster)
}

// writeOnChip8 handles byte writes to on-chip peripherals with correct
// big-endian byte placement for 16-bit registers.
func (c *CPU) writeOnChip8(addr uint32, val uint8) {
	switch {
	case addr >= 0xFFFFFE10 && addr <= 0xFFFFFE19:
		// FRT: native 8-bit. Sync live state before the write so the
		// write observes the current FRC/prescaler, then recompute the
		// deadline because writes to TCR/OCR*/FRC/FTCSR can change it.
		c.frt.syncTo(c.cycles)
		c.frt.Write(addr, val)
		c.recomputeFRTEvent()
		// TIER/FTCSR writes can change the level-sensitive request line
		// (enable an already-latched flag); re-evaluate.
		if addr == 0xFFFFFE10 || addr == 0xFFFFFE11 {
			c.RefreshOnChipInterrupts()
		}
	case addr >= 0xFFFFFE80 && addr <= 0xFFFFFE83:
		// WDT registers require a 16-bit write with the A5/5A key
		// in the high byte. Byte writes silently discard.
	case addr == 0xFFFFFE91:
		// SBYCR: native 8-bit
		c.writeOnChip(addr, uint32(val))
	case addr == 0xFFFFFE92:
		// CCR: native 8-bit
		c.writeOnChip(addr, uint32(val))
	case (addr >= 0xFFFFFE60 && addr <= 0xFFFFFE69) ||
		(addr >= 0xFFFFFEE0 && addr <= 0xFFFFFEE5):
		// INTC: 16-bit registers, big-endian byte write. Upper
		// bound 0xFFFFFE69 covers VCRD's low byte.
		aligned := addr & 0xFFFFFFFE
		cur := uint16(c.intc.Read(aligned))
		if addr&1 == 0 {
			cur = (cur & 0x00FF) | (uint16(val) << 8)
		} else {
			cur = (cur & 0xFF00) | uint16(val)
		}
		c.intc.Write(aligned, cur)
		// IPRA/IPRB priority changes can make an already-pending request
		// deliverable; re-evaluate the level-sensitive on-chip sources.
		c.RefreshOnChipInterrupts()
	case addr == 0xFFFFFE71 || addr == 0xFFFFFE72:
		// DRCR: native 8-bit
		c.dmac.WriteDRCR(addr, val)
	default:
		// 32-bit registers or unhandled - pass through
		c.writeOnChip(addr, uint32(val))
	}
}

// readOnChip handles reads to the on-chip peripheral area
// (0xFFFFFE00-0xFFFFFFFF). Returns value and true if handled.
func (c *CPU) readOnChip(addr uint32) (uint32, bool) {
	switch {
	case addr >= 0xFFFFFE10 && addr <= 0xFFFFFE19:
		// FRT registers (byte access). Sync live state so FRC reads
		// reflect the current counter value.
		c.frt.syncTo(c.cycles)
		return uint32(c.frt.Read(addr)), true
	case addr == 0xFFFFFE91:
		// SBYCR - Standby Control Register (stub storage).
		return uint32(c.sbycr), true
	case addr == 0xFFFFFE92:
		// CCR - Cache Control Register (CP always reads 0)
		return uint32(c.ccr &^ 0x10), true
	case addr >= 0xFFFFFE60 && addr <= 0xFFFFFE69:
		// INTC registers (IPRB, VCRA-VCRD). Upper bound 0xFFFFFE69
		// covers VCRD's low byte for byte reads; the &^1 below
		// aligns to the 16-bit register base.
		return uint32(c.intc.Read(addr & 0xFFFFFFFE)), true
	case addr == 0xFFFFFE71 || addr == 0xFFFFFE72:
		// DMAC DRCR0/DRCR1 (byte access)
		return uint32(c.dmac.ReadDRCR(addr)), true
	case addr >= 0xFFFFFE80 && addr <= 0xFFFFFE83:
		// WDT register byte reads per manual Sec 12.1.4 Table 12.2:
		// WTCSR at 80, WTCNT at 81, 82 undefined (reads 0),
		// RSTCSR at 83 (offset +1 from its word-write address).
		// Sync live state so WTCNT reads surface the current counter
		// and WTCSR/RSTCSR reads see any pending OVF/WOVF latch.
		c.wdt.syncTo(c.cycles)
		return uint32(c.wdt.Read(addr)), true
	case addr >= 0xFFFFFEE0 && addr <= 0xFFFFFEE5:
		// INTC registers (ICR, IPRA, VCRWDT)
		return uint32(c.intc.Read(addr & 0xFFFFFFFE)), true
	case addr >= 0xFFFFFF00 && addr <= 0xFFFFFF1F:
		// DIVU registers (32-bit access). Addresses 0xFFFFFF18 and
		// 0xFFFFFF1C are undocumented non-destructive aliases of
		// DVDNTH and DVDNTL that return the last division remainder
		// and quotient. The SH-7604 manual lists 0xFFFFFF18..0xFFFFFF3F
		// as reserved, but the real hardware mirrors DVDNTH/DVDNTL
		// there and NiGHTS relies on reading the quotient from
		// 0xFFFFFF1C in its vertex projection code.
		return c.divu.Read(addr & 0xFFFFFFFC), true
	case addr >= 0xFFFFFF80 && addr <= 0xFFFFFFB0:
		// DMAC registers (32-bit access)
		return c.dmac.Read(addr & 0xFFFFFFFC), true
	case addr >= 0xFFFFFFE0 && addr <= 0xFFFFFFE3:
		// BSC BCR1: 16-bit register. Manual Sec 7.2 Table 7.2 Note 1
		// maps 32-bit access at 0xFFFFFFE0 and 16-bit read at
		// 0xFFFFFFE2; the full 4-byte slot is covered so read16/read8
		// at the +2/+3 big-endian byte positions observe the register
		// value. Longword reads return the 16-bit value in the low
		// half (bits 15..0); the upper half is implementation-defined
		// (zero here). Writes use the matching low-half layout with a
		// $A55A write-protect key in the high half (see writeOnChip).
		// Bit 15 (MASTER) is read-only: 0=master, 1=slave. The Saturn
		// BIOS boot path at $000204 reads BCR1 as a longword and tests
		// the MASTER bit via SHLL16+SHLL to select the cold-boot
		// (master) vs warm-boot (slave) path.
		val := c.bcr1
		if !c.isMaster {
			val |= 0x8000
		}
		return uint32(val), true
	}
	return 0, false
}

// writeOnChip handles writes to the on-chip peripheral area
// (0xFFFFFE00-0xFFFFFFFF). Returns true if handled.
func (c *CPU) writeOnChip(addr uint32, val uint32) bool {
	switch {
	case addr >= 0xFFFFFE10 && addr <= 0xFFFFFE19:
		// FRT registers (byte access). Sync live state before the write,
		// recompute the deadline after.
		c.frt.syncTo(c.cycles)
		c.frt.Write(addr, uint8(val))
		c.recomputeFRTEvent()
		// TIER/FTCSR writes can change the level-sensitive request line
		// (enable an already-latched flag); re-evaluate.
		if addr == 0xFFFFFE10 || addr == 0xFFFFFE11 {
			c.RefreshOnChipInterrupts()
		}
		return true
	case addr == 0xFFFFFE91:
		// SBYCR - Standby Control Register. Stub storage only; STBY
		// mode behavior is not modeled (Saturn software uses SBYCR
		// only during CKCHG clock switching). Bit 5 reserved.
		c.sbycr = uint8(val) & 0xDF
		return true
	case addr == 0xFFFFFE92:
		// CCR - Cache Control Register (Section 8.2). Writing 1 to CP
		// clears all valid bits and LRU information; CP self-clears
		// and always reads 0 (Section 8.4.6).
		v := uint8(val) & 0xDF // bit 5 reserved, always 0
		if v&ccrCP != 0 {
			c.cachePurge()
			v &^= ccrCP
		}
		c.ccr = v
		return true
	case addr >= 0xFFFFFE60 && addr <= 0xFFFFFE68:
		// INTC registers (IPRB, VCRA-VCRD)
		c.intc.Write(addr&0xFFFFFFFE, uint16(val))
		// IPRB priority changes can make an already-pending request
		// deliverable; re-evaluate the level-sensitive on-chip sources.
		c.RefreshOnChipInterrupts()
		return true
	case addr == 0xFFFFFE71 || addr == 0xFFFFFE72:
		// DMAC DRCR0/DRCR1 (byte access)
		c.dmac.WriteDRCR(addr, uint8(val))
		return true
	case addr == 0xFFFFFE80 || addr == 0xFFFFFE82:
		// WDT word-write path. Manual Sec 12.2.4 requires the
		// high byte to be 0xA5 (WTCSR / RSTCSR.WOVF clear) or
		// 0x5A (WTCNT / RSTCSR.RSTE+RSTS). WriteWord applies the
		// gate; unaligned or non-word addresses (0x81, 0x83)
		// fall through and are discarded. Sync live state before
		// the write, recompute the deadline after (WTCSR TME/CKS
		// and WTCNT writes both change it).
		c.wdt.syncTo(c.cycles)
		c.wdt.WriteWord(addr, uint16(val))
		c.recomputeWDTEvent()
		// WTCSR (0xFE80) mode/flag changes can change the level-sensitive
		// ITI request line; re-evaluate. RSTCSR (0xFE82) does not.
		if addr == 0xFFFFFE80 {
			c.RefreshOnChipInterrupts()
		}
		return true
	case addr >= 0xFFFFFEE0 && addr <= 0xFFFFFEE5:
		// INTC registers (ICR, IPRA, VCRWDT)
		c.intc.Write(addr&0xFFFFFFFE, uint16(val))
		// IPRA priority changes can make an already-pending request
		// deliverable; re-evaluate the level-sensitive on-chip sources.
		c.RefreshOnChipInterrupts()
		return true
	case addr >= 0xFFFFFF00 && addr <= 0xFFFFFF1F:
		// DIVU registers (32-bit access), including the undocumented
		// non-destructive aliases DVDNTUH/DVDNTUL at 0xFFFFFF18 and
		// 0xFFFFFF1C.
		if c.divu.Write(addr&0xFFFFFFFC, val) {
			c.routeDIVUInterrupt()
		}
		// DVCR (0xFFFFFF08) carries OVFIE; enabling it while OVF is set
		// makes the level-sensitive request active without a fresh edge.
		if addr&0xFFFFFFFC == 0xFFFFFF08 {
			c.RefreshOnChipInterrupts()
		}
		return true
	case addr >= 0xFFFFFF80 && addr <= 0xFFFFFFB0:
		// DMAC registers (32-bit access)
		dmacReg := addr & 0xFFFFFFFC
		ch := c.dmac.Write(dmacReg, val)
		if ch >= 0 {
			c.routeDMACInterrupt(ch)
		}
		// CHCR0/CHCR1 (IE) and DMAOR (DME) carry the transfer-end
		// interrupt enables; enabling one while TE is set makes the
		// level-sensitive request active without a fresh edge.
		if dmacReg == 0xFFFFFF8C || dmacReg == 0xFFFFFF9C || dmacReg == 0xFFFFFFB0 {
			c.RefreshOnChipInterrupts()
		}
		return true
	case addr == 0xFFFFFFE0:
		// BSC BCR1 - requires $A55A write-protect key in upper 16 bits
		// Bit 15 (MASTER) is read-only, preserve it on writes
		if val>>16 == 0xA55A {
			c.bcr1 = uint16(val) & 0x7FFF
		}
		return true
	}
	return false
}

// peripheralsDue reports whether any on-chip peripheral deadline has
// been reached this cycle. tickPeripherals is a no-op otherwise, so
// gating the call on this avoids the per-cycle function-call overhead
// in the common case where no deadline is due. Backed by the cached
// combined deadline so this is a single compare, inlined at the Clock
// call sites.
func (c *CPU) peripheralsDue() bool {
	return c.cycles >= c.nextPeripheralEvent
}

// syncPeripheralDeadline refreshes the cached combined peripheral
// deadline. Called wherever frt.nextEvent or wdt.nextEvent may have
// changed so peripheralsDue stays exact.
func (c *CPU) syncPeripheralDeadline() {
	c.nextPeripheralEvent = min(c.frt.nextEvent, c.wdt.nextEvent)
}

// recomputeFRTEvent recomputes the FRT deadline and refreshes the
// cached combined deadline. Use this for every CPU-initiated FRT
// reschedule so nextPeripheralEvent cannot drift.
func (c *CPU) recomputeFRTEvent() {
	c.frt.recomputeNextEvent(c.cycles)
	c.syncPeripheralDeadline()
}

// recomputeWDTEvent recomputes the WDT deadline and refreshes the
// cached combined deadline.
func (c *CPU) recomputeWDTEvent() {
	c.wdt.recomputeNextEvent(c.cycles)
	c.syncPeripheralDeadline()
}

// tickPeripherals advances on-chip peripherals by one cycle and
// routes any pending interrupts to the CPU. The FRT uses deadline
// scheduling: the common case is a single compare with no work.
func (c *CPU) tickPeripherals() {
	if c.cycles >= c.frt.nextEvent {
		if triggered := c.frt.fireDue(c.cycles); triggered != 0 {
			c.routeFRTInterrupts(triggered)
		}
	}
	if c.cycles >= c.wdt.nextEvent {
		// Overflow. Interval mode raises ITI via INTC; watchdog
		// mode is already logged in syncTo (reset unmodeled).
		if c.wdt.fireDue(c.cycles) {
			if c.wdt.wtcsr&wtcsrWTIT == 0 {
				c.routeWDTInterrupt()
			}
		}
	}
	// fireDue reschedules frt/wdt.nextEvent internally; refresh the
	// cached combined deadline so the next gate check is exact.
	c.syncPeripheralDeadline()
}

// routeFRTInterrupts marks the FRT source as possibly asserted when
// any of its flags has newly triggered. The FRT latch lives in FTCSR;
// processInterrupt resolves the sub-priority (ICI > OCIA > OCIB > OVI)
// when it scans.
func (c *CPU) routeFRTInterrupts(triggered uint8) {
	if triggered == 0 {
		return
	}
	if c.intc.frtPriority() == 0 {
		return
	}
	c.intc.AssertSource(isrcFRT)
}

// RefreshOnChipInterrupts re-evaluates the level-sensitive request line of
// every implemented on-chip interrupt source and marks the INTC pending bit
// for those currently asserting with a non-zero priority.
//
// SH7604 on-chip interrupt requests are level-sensitive: each is the logical
// AND of a status flag and its enable bit (FRT Sec 11.5, WDT Sec 12.3.2,
// DIVU Sec 10, DMAC Sec 9), held until software clears the flag, and the INTC
// samples it continuously (Sec 5). A request can become active with no fresh
// flag-set edge: by enabling an already-set flag, by a mode change exposing a
// held flag, or by raising the module priority while a flag is pending. The
// peripherals' edge callbacks (route*Interrupt) only cover the flag-set edge,
// so this is called after any write to an interrupt control/enable/mode/
// priority register to recover those level transitions. Over-marking is safe:
// processInterrupt lazily clears a pending bit whose source is no longer
// asserted.
func (c *CPU) RefreshOnChipInterrupts() {
	if c.divu.IRQAsserted() && c.intc.divuPriority() > 0 {
		c.intc.AssertSource(isrcDIVU)
	}
	if c.dmac.IRQAsserted(0) && c.intc.dmacPriority() > 0 {
		c.intc.AssertSource(isrcDMAC0)
	}
	if c.dmac.IRQAsserted(1) && c.intc.dmacPriority() > 0 {
		c.intc.AssertSource(isrcDMAC1)
	}
	if c.frt.IRQFlags() != 0 && c.intc.frtPriority() > 0 {
		c.intc.AssertSource(isrcFRT)
	}
	if c.wdt.IRQAsserted() && c.intc.wdtPriority() > 0 {
		c.intc.AssertSource(isrcWDT)
	}
}

// routeDIVUInterrupt marks the DIVU source as possibly asserted if
// the priority is configured. The DVCR.OVF latch itself is managed
// inside DIVU; this function just wakes processInterrupt.
func (c *CPU) routeDIVUInterrupt() {
	if c.intc.divuPriority() == 0 {
		return
	}
	c.intc.AssertSource(isrcDIVU)
}

// routeDMACInterrupt marks the DMAC channel source as possibly
// asserted when a transfer ends with IE set. CHCR.TE and CHCR.IE
// are the canonical state; processInterrupt consults them via
// DMAC.IRQAsserted(ch).
func (c *CPU) routeDMACInterrupt(ch int) {
	if c.dmac.ch[ch].chcr&4 == 0 {
		return
	}
	if c.intc.dmacPriority() == 0 {
		return
	}
	if ch == 0 {
		c.intc.AssertSource(isrcDMAC0)
	} else {
		c.intc.AssertSource(isrcDMAC1)
	}
}

// FRTInputCapture triggers input capture on the FRT. Called by the
// coordinator when it detects a write to MINIT/SINIT addresses.
// Sync live FRT state before the capture so ICR latches the current
// FRC value (Section 11.4.5, Figure 11.10).
func (c *CPU) FRTInputCapture() {
	c.frt.syncTo(c.cycles)
	if c.frt.InputCapture() && c.intc.frtPriority() > 0 {
		c.intc.AssertSource(isrcFRT)
	}
}

// ClearFRTInputCapture clears the FRT input-capture latch and re-evaluates
// the on-chip interrupt line. Used on a system reset (CKCHG/SYSRES): the
// peer SH-2 that drives this CPU's cross-CPU input capture is turned off,
// so a SINIT/MINIT capture latched just before the reset must not leave
// ICF asserted (the request line is the AND of ICF and TIER.ICIE and is
// otherwise held until software clears ICF).
func (c *CPU) ClearFRTInputCapture() {
	c.frt.ClearInputCapture()
	c.RefreshOnChipInterrupts()
}

// routeWDTInterrupt marks the WDT source as possibly asserted after
// an interval-mode overflow. The WTCSR.OVF latch lives in WDT;
// processInterrupt reconciles via the resolveSource query.
func (c *CPU) routeWDTInterrupt() {
	if c.intc.wdtPriority() == 0 {
		return
	}
	c.intc.AssertSource(isrcWDT)
}
