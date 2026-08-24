// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// State is a complete snapshot of the CPU's serializable state for
// save states. State() and SetState() are pure field copies: nothing
// is re-derived, invalidated, or recomputed on restore, so a
// round-trip covers every field with no carve-outs. Excluded are only
// per-cycle scratch set fresh before any read (ir, addrError, stepBus,
// branchTaken, frameCyc) and construction wiring (bus, callbacks,
// isMaster).
//
// The consumer serializes these fields itself (the sh2 package exposes
// state, it does not serialize); field encodings and their stability
// rules live with the serializer.
type State struct {
	Reg    Registers
	Cycles uint64
	Halted bool
	IRL    uint32 // packed level<<16 | vector, the SCU-driven line latch

	Exec  ExecState
	Cache CacheState
	INTC  INTCState
	FRT   FRTState
	DIVU  DIVUState
	DMAC  DMACState
	WDT   WDTState
}

// ExecState holds the pipeline and execution-engine state.
type ExecState struct {
	PrevPC     uint32
	DelayPC    uint32
	InDelay    bool
	NMIPending bool
	// NMIReq is the cross-goroutine NMI request not yet folded into
	// NMIPending; it can be set-but-unfolded at a frame boundary.
	NMIReq     bool
	IntInhibit bool

	PendingOp    uint8
	PendingStep  uint8
	PendingCount uint8
	PendingN     uint8
	PendingAddr  uint32
	PendingVal   uint32
	PendingVal2  uint32
	PendingImm   uint32

	LastLoadReg  uint8
	DeferredOp   uint16
	HasDeferred  bool
	LoadUseStall bool

	MultiplierBusyUntil uint64
	BusStall            uint32
	NextPeripheralEvent uint64

	// Fetch-line memo. Captured verbatim: it stays coherent because
	// the cache it resolves into is restored byte-for-byte.
	FetchLineAddr uint32
	FetchLineWay  int
	FetchLineOff  uint32

	SBYCR uint8
	BCR1  uint16
}

// CacheState holds the cache control register and all four cache
// arrays. The arrays are one unit: data restored without tags, valid
// bits, and LRU state is an incoherent cache.
type CacheState struct {
	CCR   uint8
	Data  [4096]byte
	Tag   [4][64]uint32
	Valid [4][64]bool
	LRU   [64]uint8
}

// INTCState holds the interrupt controller registers and the pending
// source bitmask.
type INTCState struct {
	IPRA    uint16
	IPRB    uint16
	VCRA    uint16
	VCRB    uint16
	VCRC    uint16
	VCRD    uint16
	ICR     uint16
	VCRWDT  uint16
	Pending uint16
}

// FRTState holds the free-running timer state, including the TEMP
// register for the 8-bit byte-pair access protocol and the FTCSR
// read-then-write-0 clear tracking, both of which span instructions.
type FRTState struct {
	FRC       uint16
	OCRA      uint16
	OCRB      uint16
	ICR       uint16
	TIER      uint8
	FTCSR     uint8
	ReadFlags uint8
	TCR       uint8
	TOCR      uint8
	Prescaler uint16
	Temp      uint8
	LastSync  uint64
	NextEvent uint64
}

// DIVUState holds the division unit registers.
type DIVUState struct {
	DVSR   uint32
	DVDNT  uint32
	DVDNTH uint32
	DVDNTL uint32
	DVCR   uint32
	VCRDIV uint32
}

// DMACChanState holds one DMA channel's registers.
type DMACChanState struct {
	SAR    uint32
	DAR    uint32
	TCR    uint32
	CHCR   uint32
	VCRDMA uint32
}

// DMACState holds the on-chip DMA controller state.
type DMACState struct {
	Ch          [2]DMACChanState
	DMAOR       uint16
	DRCR        [2]uint8
	NextCh      int
	StallCycles [2]int
	StallCh     int
}

// WDTState holds the watchdog timer state.
type WDTState struct {
	WTCSR     uint8
	WTCNT     uint8
	RSTCSR    uint8
	Prescaler uint32
	LastSync  uint64
	NextEvent uint64
}

// State returns a complete snapshot of the CPU's serializable state.
func (c *CPU) State() State {
	var s State
	s.Reg = c.reg
	s.Cycles = c.cycles
	s.Halted = c.halted
	s.IRL = c.irl.Load()

	s.Exec.PrevPC = c.prevPC
	s.Exec.DelayPC = c.delayPC
	s.Exec.InDelay = c.inDelay
	s.Exec.NMIPending = c.nmiPending
	s.Exec.NMIReq = c.nmiReq.Load()
	s.Exec.IntInhibit = c.intInhibit
	s.Exec.PendingOp = c.pendingOp
	s.Exec.PendingStep = c.pendingStep
	s.Exec.PendingCount = c.pendingCount
	s.Exec.PendingN = c.pendingN
	s.Exec.PendingAddr = c.pendingAddr
	s.Exec.PendingVal = c.pendingVal
	s.Exec.PendingVal2 = c.pendingVal2
	s.Exec.PendingImm = c.pendingImm
	s.Exec.LastLoadReg = c.lastLoadReg
	s.Exec.DeferredOp = c.deferredOp
	s.Exec.HasDeferred = c.hasDeferred
	s.Exec.LoadUseStall = c.loadUseStall
	s.Exec.MultiplierBusyUntil = c.multiplierBusyUntil
	s.Exec.BusStall = c.busStall
	s.Exec.NextPeripheralEvent = c.nextPeripheralEvent
	s.Exec.FetchLineAddr = c.fetchLineAddr
	s.Exec.FetchLineWay = c.fetchLineWay
	s.Exec.FetchLineOff = c.fetchLineOff
	s.Exec.SBYCR = c.sbycr
	s.Exec.BCR1 = c.bcr1

	s.Cache.CCR = c.ccr
	s.Cache.Data = c.cacheData
	s.Cache.Tag = c.cacheTag
	s.Cache.Valid = c.cacheValid
	s.Cache.LRU = c.cacheLRU

	s.INTC.IPRA = c.intc.ipra
	s.INTC.IPRB = c.intc.iprb
	s.INTC.VCRA = c.intc.vcra
	s.INTC.VCRB = c.intc.vcrb
	s.INTC.VCRC = c.intc.vcrc
	s.INTC.VCRD = c.intc.vcrd
	s.INTC.ICR = c.intc.icr
	s.INTC.VCRWDT = c.intc.vcrwdt
	s.INTC.Pending = c.intc.pending

	s.FRT.FRC = c.frt.frc
	s.FRT.OCRA = c.frt.ocra
	s.FRT.OCRB = c.frt.ocrb
	s.FRT.ICR = c.frt.icr
	s.FRT.TIER = c.frt.tier
	s.FRT.FTCSR = c.frt.ftcsr
	s.FRT.ReadFlags = c.frt.readFlags
	s.FRT.TCR = c.frt.tcr
	s.FRT.TOCR = c.frt.tocr
	s.FRT.Prescaler = c.frt.prescaler
	s.FRT.Temp = c.frt.temp
	s.FRT.LastSync = c.frt.lastSync
	s.FRT.NextEvent = c.frt.nextEvent

	s.DIVU.DVSR = c.divu.dvsr
	s.DIVU.DVDNT = c.divu.dvdnt
	s.DIVU.DVDNTH = c.divu.dvdnth
	s.DIVU.DVDNTL = c.divu.dvdntl
	s.DIVU.DVCR = c.divu.dvcr
	s.DIVU.VCRDIV = c.divu.vcrdiv

	for i := range c.dmac.ch {
		s.DMAC.Ch[i].SAR = c.dmac.ch[i].sar
		s.DMAC.Ch[i].DAR = c.dmac.ch[i].dar
		s.DMAC.Ch[i].TCR = c.dmac.ch[i].tcr
		s.DMAC.Ch[i].CHCR = c.dmac.ch[i].chcr
		s.DMAC.Ch[i].VCRDMA = c.dmac.ch[i].vcrdma
	}
	s.DMAC.DMAOR = c.dmac.dmaor
	s.DMAC.DRCR = c.dmac.drcr
	s.DMAC.NextCh = c.dmac.nextCh
	s.DMAC.StallCycles = c.dmac.stallCycles
	s.DMAC.StallCh = c.dmac.stallCh

	s.WDT.WTCSR = c.wdt.wtcsr
	s.WDT.WTCNT = c.wdt.wtcnt
	s.WDT.RSTCSR = c.wdt.rstcsr
	s.WDT.Prescaler = c.wdt.prescaler
	s.WDT.LastSync = c.wdt.lastSync
	s.WDT.NextEvent = c.wdt.nextEvent

	return s
}

// SetState restores a snapshot taken by State. Pure field assignment:
// the snapshot is internally consistent by construction, so nothing is
// re-derived.
func (c *CPU) SetState(s *State) {
	c.reg = s.Reg
	c.cycles = s.Cycles
	c.halted = s.Halted
	c.irl.Store(s.IRL)

	c.prevPC = s.Exec.PrevPC
	c.delayPC = s.Exec.DelayPC
	c.inDelay = s.Exec.InDelay
	c.nmiPending = s.Exec.NMIPending
	c.nmiReq.Store(s.Exec.NMIReq)
	c.intInhibit = s.Exec.IntInhibit
	c.pendingOp = s.Exec.PendingOp
	c.pendingStep = s.Exec.PendingStep
	c.pendingCount = s.Exec.PendingCount
	c.pendingN = s.Exec.PendingN
	c.pendingAddr = s.Exec.PendingAddr
	c.pendingVal = s.Exec.PendingVal
	c.pendingVal2 = s.Exec.PendingVal2
	c.pendingImm = s.Exec.PendingImm
	c.lastLoadReg = s.Exec.LastLoadReg
	c.deferredOp = s.Exec.DeferredOp
	c.hasDeferred = s.Exec.HasDeferred
	c.loadUseStall = s.Exec.LoadUseStall
	c.multiplierBusyUntil = s.Exec.MultiplierBusyUntil
	c.busStall = s.Exec.BusStall
	c.nextPeripheralEvent = s.Exec.NextPeripheralEvent
	c.fetchLineAddr = s.Exec.FetchLineAddr
	c.fetchLineWay = s.Exec.FetchLineWay
	c.fetchLineOff = s.Exec.FetchLineOff
	c.sbycr = s.Exec.SBYCR
	c.bcr1 = s.Exec.BCR1

	c.ccr = s.Cache.CCR
	c.cacheData = s.Cache.Data
	c.cacheTag = s.Cache.Tag
	c.cacheValid = s.Cache.Valid
	c.cacheLRU = s.Cache.LRU

	c.intc.ipra = s.INTC.IPRA
	c.intc.iprb = s.INTC.IPRB
	c.intc.vcra = s.INTC.VCRA
	c.intc.vcrb = s.INTC.VCRB
	c.intc.vcrc = s.INTC.VCRC
	c.intc.vcrd = s.INTC.VCRD
	c.intc.icr = s.INTC.ICR
	c.intc.vcrwdt = s.INTC.VCRWDT
	c.intc.pending = s.INTC.Pending

	c.frt.frc = s.FRT.FRC
	c.frt.ocra = s.FRT.OCRA
	c.frt.ocrb = s.FRT.OCRB
	c.frt.icr = s.FRT.ICR
	c.frt.tier = s.FRT.TIER
	c.frt.ftcsr = s.FRT.FTCSR
	c.frt.readFlags = s.FRT.ReadFlags
	c.frt.tcr = s.FRT.TCR
	c.frt.tocr = s.FRT.TOCR
	c.frt.prescaler = s.FRT.Prescaler
	c.frt.temp = s.FRT.Temp
	c.frt.lastSync = s.FRT.LastSync
	c.frt.nextEvent = s.FRT.NextEvent

	c.divu.dvsr = s.DIVU.DVSR
	c.divu.dvdnt = s.DIVU.DVDNT
	c.divu.dvdnth = s.DIVU.DVDNTH
	c.divu.dvdntl = s.DIVU.DVDNTL
	c.divu.dvcr = s.DIVU.DVCR
	c.divu.vcrdiv = s.DIVU.VCRDIV

	for i := range c.dmac.ch {
		c.dmac.ch[i].sar = s.DMAC.Ch[i].SAR
		c.dmac.ch[i].dar = s.DMAC.Ch[i].DAR
		c.dmac.ch[i].tcr = s.DMAC.Ch[i].TCR
		c.dmac.ch[i].chcr = s.DMAC.Ch[i].CHCR
		c.dmac.ch[i].vcrdma = s.DMAC.Ch[i].VCRDMA
	}
	c.dmac.dmaor = s.DMAC.DMAOR
	c.dmac.drcr = s.DMAC.DRCR
	c.dmac.nextCh = s.DMAC.NextCh
	c.dmac.stallCycles = s.DMAC.StallCycles
	c.dmac.stallCh = s.DMAC.StallCh

	c.wdt.wtcsr = s.WDT.WTCSR
	c.wdt.wtcnt = s.WDT.WTCNT
	c.wdt.rstcsr = s.WDT.RSTCSR
	c.wdt.prescaler = s.WDT.Prescaler
	c.wdt.lastSync = s.WDT.LastSync
	c.wdt.nextEvent = s.WDT.NextEvent
}
