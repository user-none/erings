// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

// dmaChan holds per-channel DMA state.
type dmaChan struct {
	sar    uint32 // Source Address Register
	dar    uint32 // Destination Address Register
	tcr    uint32 // Transfer Count Register (24-bit)
	chcr   uint32 // Channel Control Register (lower 16 bits valid)
	vcrdma uint32 // DMA Vector Register
}

// DMAC implements the SH-2 on-chip 2-channel DMA controller.
// Data is moved instantly when triggered, but the CPU stalls for
// TCR * 20 cycles to simulate bus occupation. TE and the optional
// interrupt fire when the stall countdown reaches zero.
type DMAC struct {
	ch     [2]dmaChan
	dmaor  uint16   // DMA Operation Register
	drcr   [2]uint8 // DMA Request/Response Selection Control Registers
	nextCh int      // Next channel for round-robin priority (0 or 1)
	bus    Bus      // Memory access for transfers

	stallCycles int // cycles remaining in DMA stall (-1 = inactive)
	stallCh     int // channel that completed transfer (-1 = none)
}

// Reset returns the DMAC to power-on state.
func (d *DMAC) Reset() {
	d.ch[0] = dmaChan{}
	d.ch[1] = dmaChan{}
	d.dmaor = 0
	d.drcr[0] = 0
	d.drcr[1] = 0
	d.nextCh = 1 // after reset, ch1 has priority in round-robin mode
	d.stallCycles = -1
	d.stallCh = -1
}

// Stalling returns true if the DMAC is occupying the bus.
func (d *DMAC) Stalling() bool {
	return d.stallCycles >= 0
}

// IRQAsserted returns true when the DMAC channel's transfer-end
// interrupt line is currently driven to the INTC: TE latch (bit 1)
// is set AND IE enable (bit 2) is set. Latch remains asserted until
// software clears CHCR.TE.
func (d *DMAC) IRQAsserted(ch int) bool {
	return d.ch[ch].chcr&0x06 == 0x06
}

// Tick decrements the stall countdown by one cycle. When the countdown
// reaches zero, TE is set on the completed channel. Returns the channel
// number that completed, or -1 if no completion occurred.
func (d *DMAC) Tick() int {
	if d.stallCycles < 0 {
		return -1
	}
	d.stallCycles--
	if d.stallCycles <= 0 {
		ch := d.stallCh
		d.ch[ch].chcr |= 2 // Set TE
		d.stallCycles = -1
		d.stallCh = -1
		return ch
	}
	return -1
}

// Read reads a DMAC register by full address (0xFFFFFF80-0xFFFFFFB0).
func (d *DMAC) Read(addr uint32) uint32 {
	switch addr {
	case 0xFFFFFF80:
		return d.ch[0].sar
	case 0xFFFFFF84:
		return d.ch[0].dar
	case 0xFFFFFF88:
		return d.ch[0].tcr
	case 0xFFFFFF8C:
		return d.ch[0].chcr
	case 0xFFFFFF90:
		return d.ch[1].sar
	case 0xFFFFFF94:
		return d.ch[1].dar
	case 0xFFFFFF98:
		return d.ch[1].tcr
	case 0xFFFFFF9C:
		return d.ch[1].chcr
	case 0xFFFFFFA0:
		return d.ch[0].vcrdma
	case 0xFFFFFFA8:
		return d.ch[1].vcrdma
	case 0xFFFFFFB0:
		return uint32(d.dmaor)
	}
	return 0
}

// Write writes a DMAC register by full address (0xFFFFFF80-0xFFFFFFB0).
// Returns the channel number (0 or 1) if a transfer completed and
// interrupt should be checked, or -1 otherwise.
func (d *DMAC) Write(addr uint32, val uint32) int {
	switch addr {
	case 0xFFFFFF80:
		d.ch[0].sar = val
	case 0xFFFFFF84:
		d.ch[0].dar = val
	case 0xFFFFFF88:
		d.ch[0].tcr = val & 0xFFFFFF
	case 0xFFFFFF8C:
		return d.writeCHCR(0, val)
	case 0xFFFFFF90:
		d.ch[1].sar = val
	case 0xFFFFFF94:
		d.ch[1].dar = val
	case 0xFFFFFF98:
		d.ch[1].tcr = val & 0xFFFFFF
	case 0xFFFFFF9C:
		return d.writeCHCR(1, val)
	case 0xFFFFFFA0:
		d.ch[0].vcrdma = val & 0x7F
	case 0xFFFFFFA8:
		d.ch[1].vcrdma = val & 0x7F
	case 0xFFFFFFB0:
		return d.writeDMAOR(val)
	}
	return -1
}

func (d *DMAC) writeCHCR(ch int, val uint32) int {
	chcr := val & 0xFFFF // only lower 16 bits valid
	// TE is write-0-to-clear: preserve TE unless written as 0
	if chcr&2 != 0 {
		chcr = (chcr &^ 2) | (d.ch[ch].chcr & 2)
	}
	d.ch[ch].chcr = chcr
	// A CHCR write while the channel's transfer is still occupying
	// the bus must not re-kick the engine. Matches SH-7604 HW manual
	// sec 9.5 Note 2 guidance that DE must be cleared before CHCR is
	// rewritten.
	if d.Stalling() && d.stallCh == ch {
		return -1
	}
	if d.transferReady(ch) {
		d.execute(ch)
	}
	return -1
}

func (d *DMAC) writeDMAOR(val uint32) int {
	newVal := uint16(val) & 0x000F
	// AE (bit 2) and NMIF (bit 1) are write-0-to-clear
	ae := d.dmaor & 4
	if newVal&4 == 0 {
		ae = 0
	}
	nmif := d.dmaor & 2
	if newVal&2 == 0 {
		nmif = 0
	}
	oldDME := d.dmaor & 1
	d.dmaor = (newVal & 0x09) | ae | nmif

	// Per sec 9.3.1 flowchart, clearing DME during an active transfer
	// aborts it. Cancel the stall without setting TE so no DEI
	// fires - DEI is only raised on normal completion.
	if oldDME != 0 && d.dmaor&1 == 0 && d.Stalling() {
		d.stallCycles = -1
		d.stallCh = -1
		return -1
	}

	// Don't re-kick a channel that's already occupying the bus.
	if d.Stalling() {
		return -1
	}
	return d.runReady()
}

// runReady selects and executes the highest priority ready channel.
// Returns -1; completion is deferred via the stall countdown.
// DMAOR bit 3 (PR): 0 = fixed priority (ch0 > ch1), 1 = round-robin.
func (d *DMAC) runReady() int {
	first := 0
	if d.dmaor&0x08 != 0 {
		first = d.nextCh
	}
	second := first ^ 1

	if d.transferReady(first) {
		d.execute(first)
		if d.dmaor&0x08 != 0 {
			d.nextCh = second
		}
		return -1
	}
	if d.transferReady(second) {
		d.execute(second)
		if d.dmaor&0x08 != 0 {
			d.nextCh = first
		}
		return -1
	}
	return -1
}

func (d *DMAC) transferReady(ch int) bool {
	if d.bus == nil {
		return false
	}
	// DMAOR: DME=1, AE=0, NMIF=0
	if d.dmaor&0x07 != 0x01 {
		return false
	}
	// CHCR: DE=1, TE=0
	chcr := d.ch[ch].chcr
	return chcr&1 != 0 && chcr&2 == 0
}

func (d *DMAC) execute(ch int) {
	c := &d.ch[ch]
	chcr := c.chcr

	dm := (chcr >> 14) & 3
	sm := (chcr >> 12) & 3
	ts := (chcr >> 10) & 3

	var unitSize uint32
	switch ts {
	case 0:
		unitSize = 1
	case 1:
		unitSize = 2
	case 2:
		unitSize = 4
	case 3:
		unitSize = 16
	}

	srcInc := dmacAddrInc(sm, unitSize)
	dstInc := dmacAddrInc(dm, unitSize)

	src := c.sar
	dst := c.dar
	count := c.tcr
	if count == 0 {
		count = 0x1000000
	}

	var stall uint32
	for count > 0 {
		// Accumulate per-unit bus-occupation stall based on the
		// region and size of each source and destination access.
		// Bus.AccessCycles combines SH-2 BSC and SCU A-Bus/B-Bus
		// timing per the Saturn BIOS configuration.
		stall += d.bus.AccessCycles(src, unitSize)
		stall += d.bus.AccessCycles(dst, unitSize)

		switch ts {
		case 0:
			d.bus.Write8(dst, d.bus.Read8(src))
		case 1:
			d.bus.Write16(dst, d.bus.Read16(src))
		case 2:
			d.bus.Write32(dst, d.bus.Read32(src))
		case 3:
			// 16-byte: 4 longword reads then 4 longword writes.
			// Per SH-7604 HW manual, in 16-byte transfer mode TCR
			// must be programmed as 4x the number of 16-byte
			// transfers, so each iteration of this loop consumes
			// 4 count units while moving one 16-byte block.
			b0 := d.bus.Read32(src)
			b1 := d.bus.Read32(src + 4)
			b2 := d.bus.Read32(src + 8)
			b3 := d.bus.Read32(src + 12)
			d.bus.Write32(dst, b0)
			d.bus.Write32(dst+4, b1)
			d.bus.Write32(dst+8, b2)
			d.bus.Write32(dst+12, b3)
		}
		src += srcInc
		dst += dstInc
		if ts == 3 {
			if count >= 4 {
				count -= 4
			} else {
				count = 0
			}
		} else {
			count--
		}
	}

	c.sar = src
	c.dar = dst
	c.tcr = 0

	d.stallCycles = int(stall)
	d.stallCh = ch
}

// ReadDRCR reads a DRCR register (byte access at 0xFFFFFE71-0xFFFFFE72).
func (d *DMAC) ReadDRCR(addr uint32) uint8 {
	switch addr {
	case 0xFFFFFE71:
		return d.drcr[0]
	case 0xFFFFFE72:
		return d.drcr[1]
	}
	return 0
}

// WriteDRCR writes a DRCR register (byte access at 0xFFFFFE71-0xFFFFFE72).
func (d *DMAC) WriteDRCR(addr uint32, val uint8) {
	switch addr {
	case 0xFFFFFE71:
		d.drcr[0] = val & 0x03
	case 0xFFFFFE72:
		d.drcr[1] = val & 0x03
	}
}

func dmacAddrInc(mode uint32, unit uint32) uint32 {
	switch mode {
	case 1:
		return unit
	case 2:
		return uint32(-int32(unit))
	}
	// mode 0 = fixed, mode 3 = reserved (treat as fixed)
	return 0
}
