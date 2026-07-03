// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"sync/atomic"
)

const (
	biosSize   = 512 * 1024      // 512 KB
	wramHSize  = 1024 * 1024     // 1 MB
	wramLSize  = 1024 * 1024     // 1 MB
	backupSize = 32 * 1024       // 32 KB
	extRAMSize = 4 * 1024 * 1024 // 4 MB Extended RAM Cartridge

	// A-Bus CS0 Extended RAM Cartridge window.
	extRAMBase = 0x02400000
	extRAMTop  = 0x027FFFFF
	extRAMMask = extRAMSize - 1

	// A-Bus CS0 full decode range.
	cs0Base = 0x02000000
	cs0Top  = 0x03FFFFFF

	// A-Bus CS1 Cartridge ID byte.
	cartIDAddr = 0x04FFFFFF
	cartID4MB  = 0x5C
)

// Bus access locks, one per hardware arbitration domain. The Saturn
// has three buses (SCU manual Figure 1.1): the CPU-Bus carries Work
// RAM-H, Work RAM-L, backup RAM, the IPL ROM, and the SMPC; the A-Bus
// carries the cartridge and CD block; the B-Bus carries VDP1, VDP2,
// and the SCSP. The buses operate in parallel ("Using the CPU-Bus,
// the CPU can access the work area while executing the DMA of the
// A-Bus and B-Bus"), so accesses to different buses never serialize,
// while accesses on the same bus are atomic with respect to each
// other. A data access claims its bus area for the duration of the
// access - the SH7604 passes the bus at bus-cycle boundaries
// (Sec 7.10), which is exactly an access-scoped claim.
//
// The SCU's own register file is a separate domain rather than part
// of the CPU-Bus: a register write can trigger an immediate DMA
// (start factor 7) that itself performs bus accesses, so the
// acquisition order is SCU -> bus areas and never the reverse.
//
// The SMPC register file is likewise its own domain rather than part
// of the CPU-Bus, so its accesses serialize independently of the
// Work RAM lock.
//
// Every CPU access that reaches the Bus locks: the SH-2 cache model
// serves hits internally (they never get here), so by construction
// anything arriving on these methods is an external bus cycle - a
// cache miss, a line fill, a cache-through access, or a write (the
// SH-2 cache is write-through).
//
// The VDP1 drawing engine and VDP2 display rendering access their
// RAM through internal device ports, not the B-Bus, so they claim
// nothing here; their contention with B-Bus accesses is a device
// priority rule (VDP1 User's Manual Section 2.1 (Address Map, VRAM,
// p.19): "the order of priority of access of the VRAM is always:
// system controller > drawing") modeled separately.
const (
	areaNone   uint8 = iota // BIOS ROM, trigger regions, unmapped: no lock
	areaCPUBus              // Work RAM-H/L, backup RAM
	areaABus                // cartridge, extended RAM, CD block
	areaBBus                // VDP1, VDP2, SCSP
	areaSCU                 // SCU register file
	areaSMPC                // SMPC register file
	areaCount

	// Table sentinel for the 0x05Fxxxxx megabyte, which holds both
	// VDP2 CRAM/registers and the SCU registers; busAreaOf resolves it.
	areaSplitBBusSCU uint8 = 0xFF
	// Table sentinel for the 0x001xxxxx megabyte, which holds both the
	// SMPC and the backup RAM; busAreaOf resolves it.
	areaSplitSMPCBup uint8 = 0xFE
)

// busAreaTable maps masked-address megabyte (addr >> 20) to bus area.
var busAreaTable [128]uint8

func init() {
	t := &busAreaTable
	t[0x01] = areaSplitSMPCBup // SMPC (areaSMPC) + backup RAM (areaCPUBus)
	t[0x02] = areaCPUBus       // Work RAM-L
	for s := 0x20; s <= 0x59; s++ {
		t[s] = areaABus // CS0/CS1/dummy/CS2 (CD block)
	}
	for s := 0x5A; s <= 0x5E; s++ {
		t[s] = areaBBus // sound RAM, SCSP, VDP1, VDP2
	}
	t[0x5F] = areaSplitBBusSCU
	for s := 0x60; s <= 0x7F; s++ {
		t[s] = areaCPUBus // Work RAM-H
	}
}

// busAreaOf returns the lock area for a masked (addr & 0x07FFFFFF)
// address.
func busAreaOf(masked uint32) uint8 {
	a := busAreaTable[masked>>20]
	switch a {
	case areaSplitBBusSCU:
		if masked >= 0x05FE0000 {
			return areaSCU
		}
		return areaBBus
	case areaSplitSMPCBup:
		if masked < 0x00180000 {
			return areaSMPC
		}
		return areaCPUBus
	}
	return a
}

// lockArea claims the given bus area, spinning until it is free.
func (b *Bus) lockArea(area uint8) {
	if area == areaNone {
		return
	}
	l := &b.busLocks[area]
	for !l.CompareAndSwap(0, 1) {
	}
}

// unlockArea releases the given bus area.
func (b *Bus) unlockArea(area uint8) {
	if area == areaNone {
		return
	}
	b.busLocks[area].Store(0)
}

// Bus implements the Saturn system bus address decoder.
// It maps the SH-2's 32-bit address space to hardware regions.
// Peripherals not yet implemented return 0 on read and ignore writes.
type Bus struct {
	bios    []byte   // 512 KB BIOS ROM (read-only)
	wramH   []byte   // 1 MB Work RAM-H
	wramL   []byte   // 1 MB Work RAM-L
	backup  []byte   // 32 KB Backup RAM (mirrored in 64 KB range)
	extRAM  []byte   // 4 MB Extended RAM Cartridge (A-Bus CS0)
	scu     *SCU     // System Control Unit
	smpc    *SMPC    // System Manager and Peripheral Control
	vdp1    *VDP1    // Video Display Processor 1
	vdp2    *VDP2    // Video Display Processor 2
	scsp    *SCSP    // Saturn Custom Sound Processor
	cdblock *CDBlock // CD Block (A-Bus CS2)

	// MINIT/SINIT FRT-capture signals from a CPU's bus write inside
	// Clock() to its own step loop. Each is read on the same goroutine
	// that writes it, so a plain bool suffices: minitWritten lives entirely
	// on the master/main goroutine, sinitWritten on the slave/secondary
	// worker, under the MINIT->slave / SINIT->master write convention.
	minitWritten bool
	sinitWritten bool

	cdDataTRNSCache uint16 // cached DATATRNS word for byte reads

	// Per-area access locks (see the bus area comment above). Claimed
	// by spinning - a bus access is far shorter than a goroutine park.
	busLocks [areaCount]atomic.Int32

	// Inter-CPU bus contention (dual_cpu_users_guide Sec 1.2: "When the
	// slave and master CPUs compete for external access, one of the CPUs
	// is forced to wait for access and execution speed decreases"). When
	// one SH-2 accesses an area, the bus is occupied for the full
	// transaction; the other SH-2's next access to that area is charged
	// the cycles until it frees. busyUntil[area] is the frame-relative
	// system cycle at which the area's in-flight access completes,
	// read-modify-written under the area lock and reset per frame. The
	// accessing CPU's current frame cycle is passed in (CPU.frameCyc) -
	// the frame-loop timeline the barrier keeps aligned within
	// syncChunkCycles, NOT each CPU's executed count (which would lag
	// across an SSH-off gap). The full per-region transaction durations
	// the window advances by live in the package-global access-cost
	// tables in bus_timing.go.
	busyUntil [areaCount]int64

	masterPendingWrite pendingWrite
	slavePendingWrite  pendingWrite

	// ReadTrace, when non-nil, is called for every Read8/Read16/Read32.
	// Used for narrow-region debug tracing (e.g. discovering what a
	// game reads from BUP state buffers after a BUP slot returns).
	// Implementations should filter by address — the callback fires
	// for ALL reads so it's expensive when enabled.
	ReadTrace func(addr uint32, size int, value uint32)
}

// NewBus creates a new Saturn system bus with RAM allocated and the
// given subsystem references stored for address dispatch. The caller
// (typically NewEmulator) constructs the subsystems and is responsible
// for closing the SCU↔Bus loop via scu.SetBus(bus) and the
// SCSP↔CDBlock wiring via scsp.SetCDAudioSource(cdblock).
func NewBus(scu *SCU, smpc *SMPC, vdp1 *VDP1, vdp2 *VDP2, scsp *SCSP, cdblock *CDBlock) *Bus {
	b := &Bus{
		wramH:   make([]byte, wramHSize),
		wramL:   make([]byte, wramLSize),
		backup:  make([]byte, backupSize),
		extRAM:  make([]byte, extRAMSize),
		scu:     scu,
		smpc:    smpc,
		vdp1:    vdp1,
		vdp2:    vdp2,
		scsp:    scsp,
		cdblock: cdblock,
	}
	formatBackupRAM(b.backup)
	return b
}

// formatBackupRAM writes the Saturn backup RAM header so games detect
// the memory as formatted. The backup RAM array stores only the odd-byte
// data; even bytes always return 0xFF from the bus. The header is
// "BackUpRam Format" repeated 4 times. The rest is filled with 0x00.
func formatBackupRAM(ram []byte) {
	header := [16]byte{
		'B', 'a', 'c', 'k', 'U', 'p', 'R', 'a',
		'm', ' ', 'F', 'o', 'r', 'm', 'a', 't',
	}
	for rep := 0; rep < 4; rep++ {
		copy(ram[rep*16:], header[:])
	}
	for i := 0x40; i < len(ram); i++ {
		ram[i] = 0x00
	}
}

// writeWramHU32 stores a big-endian 32-bit value at the given
// Work-RAM-H offset, bypassing the full bus dispatch. Used by HLE
// BIOS service routines that need to update WRAM-H dispatch tables
// without going through Write32's address decoding.
func (b *Bus) writeWramHU32(off uint32, val uint32) {
	b.wramH[off] = uint8(val >> 24)
	b.wramH[off+1] = uint8(val >> 16)
	b.wramH[off+2] = uint8(val >> 8)
	b.wramH[off+3] = uint8(val)
}

// readWramHU32 reads a big-endian 32-bit value at the given
// Work-RAM-H offset. Companion to writeWramHU32.
func (b *Bus) readWramHU32(off uint32) uint32 {
	return uint32(b.wramH[off])<<24 |
		uint32(b.wramH[off+1])<<16 |
		uint32(b.wramH[off+2])<<8 |
		uint32(b.wramH[off+3])
}

// GetBackupRAM returns a copy of the 32 KB internal backup RAM. The
// slice holds only the odd-byte storage as the bus presents it; even
// bytes always read back as 0xFF and are not stored.
func (b *Bus) GetBackupRAM() []byte {
	out := make([]byte, len(b.backup))
	copy(out, b.backup)
	return out
}

// SetBackupRAM loads previously persisted internal backup RAM. Data of
// any size other than the 32 KB backup region is ignored so a corrupt
// or stale save cannot desynchronize the odd-byte address decode.
func (b *Bus) SetBackupRAM(data []byte) {
	if len(data) != backupSize {
		return
	}
	copy(b.backup, data)
}

// SetBIOS loads BIOS ROM data. The data must be exactly 512 KB.
func (b *Bus) SetBIOS(data []byte) error {
	if len(data) != biosSize {
		return fmt.Errorf("BIOS must be %d bytes, got %d", biosSize, len(data))
	}
	b.bios = make([]byte, biosSize)
	copy(b.bios, data)

	return nil
}

// MINITWritten returns whether MINIT was written since the last check
// and clears the flag.
func (b *Bus) MINITWritten() bool {
	v := b.minitWritten
	b.minitWritten = false
	return v
}

// SINITWritten returns whether SINIT was written since the last check
// and clears the flag.
func (b *Bus) SINITWritten() bool {
	v := b.sinitWritten
	b.sinitWritten = false
	return v
}

// Read8 reads a byte from the given address, claiming the address's
// bus area for the duration of the access.
func (b *Bus) Read8(addr uint32) uint8 {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	val := b.read8Impl(addr)
	b.unlockArea(area)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 1, uint32(val))
	}
	return val
}

func (b *Bus) read8Impl(addr uint32) uint8 {
	addr &= 0x07FFFFFF

	switch {
	case addr <= 0x0007FFFF:
		// BIOS ROM
		if b.bios != nil {
			return b.bios[addr]
		}
		return 0

	case addr >= 0x00100000 && addr <= 0x0017FFFF:
		// SMPC (128 bytes mirrored across 512 KB)
		return b.smpc.Read(uint8(addr & 0x7F))

	case addr >= 0x00180000 && addr <= 0x0018FFFF:
		// Backup RAM (32 KB mirrored, odd bytes only)
		if addr&1 == 0 {
			return 0xFF
		}
		return b.backup[(addr>>1)&0x7FFF]

	case addr >= 0x00200000 && addr <= 0x002FFFFF:
		// Work RAM-L
		return b.wramL[addr&0x0FFFFF]

	case addr >= 0x01000000 && addr <= 0x017FFFFF:
		// MINIT region (any write triggers slave FRT input capture)
		return 0

	case addr >= 0x01800000 && addr <= 0x01FFFFFF:
		// SINIT region (any write triggers master FRT input capture)
		return 0

	case addr >= cs0Base && addr <= cs0Top:
		// A-Bus CS0: Extended RAM Cartridge
		if addr >= extRAMBase && addr <= extRAMTop {
			return b.extRAM[addr&extRAMMask]
		}
		return 0xFF

	case addr >= 0x04000000 && addr <= 0x04FFFFFF:
		// A-Bus CS1: Cartridge ID
		if addr == cartIDAddr {
			return cartID4MB
		}
		return 0xFF

	case addr >= 0x05000000 && addr <= 0x057FFFFF:
		// A-Bus Dummy stub
		return 0

	case addr >= 0x05800000 && addr <= 0x058FFFFF:
		// A-Bus CS2 (CD Block)
		cs2off := addr - 0x05800000
		cs2masked := cs2off & ^uint32(0x80000)
		regOff := cs2off & 0x3F
		if cs2masked >= 0x18000 && cs2masked <= 0x18003 || regOff <= 0x03 {
			// DATATRNS: fetch one word per aligned byte pair
			if cs2off&1 == 0 {
				b.cdDataTRNSCache = b.cdblock.ReadDataTRNS()
				return uint8(b.cdDataTRNSCache >> 8)
			}
			return uint8(b.cdDataTRNSCache)
		}
		// Other registers: offset from low 6 bits
		reg := b.cdblock.Read(regOff &^ 1)
		if regOff&1 == 0 {
			return uint8(reg >> 8)
		}
		return uint8(reg)

	case addr >= 0x05A00000 && addr <= 0x05A7FFFF:
		// Sound RAM (512 KB)
		return b.scsp.ReadRAM(addr & 0x7FFFF)

	case addr >= 0x05A80000 && addr <= 0x05AFFFFF:
		// SCSP B-Bus gap between sound RAM (0x05A00000-0x05A7FFFF)
		// and SCSP registers (0x05B00000+). Unmapped on real
		// hardware: reads return 0, writes are ignored. Games
		// routinely hit this range with DMA clears and dummy-write
		// flush patterns, so logging here would flood the output.
		// The default unmapped-access warning is reserved for
		// truly unexpected addresses.
		return 0

	case addr >= 0x05B00000 && addr <= 0x05B00EE3:
		// SCSP Registers
		off := addr - 0x05B00000
		reg := b.scsp.Read(off &^ 1)
		if off&1 == 0 {
			return uint8(reg >> 8)
		}
		return uint8(reg)

	case addr >= 0x05B00EE4 && addr <= 0x05BFFFFF:
		// Unmapped SCSP range
		return 0

	case addr >= 0x05C00000 && addr <= 0x05C7FFFF:
		// VDP1 VRAM
		return b.vdp1.ReadVRAM(addr - 0x05C00000)

	case addr >= 0x05C80000 && addr <= 0x05CBFFFF:
		// VDP1 Frame Buffer
		return b.vdp1.ReadFB(addr - 0x05C80000)

	case addr >= 0x05D00000 && addr <= 0x05D00017:
		// VDP1 Registers: word access only
		fmt.Printf("[BUS] invalid 8-bit read from VDP1 register 0x%08X\n", addr)
		return 0

	case addr >= 0x05E00000 && addr <= 0x05E7FFFF:
		// VDP2 VRAM
		return b.vdp2.ReadVRAM(addr - 0x05E00000)

	case addr >= 0x05F00000 && addr <= 0x05F00FFF:
		// VDP2 CRAM
		return b.vdp2.ReadCRAM(addr - 0x05F00000)

	case addr >= 0x05F80000 && addr <= 0x05F8011F:
		// VDP2 Registers: word/long access only
		fmt.Printf("[BUS] invalid 8-bit read from VDP2 register 0x%08X\n", addr)
		return 0

	case addr >= 0x05FE0000 && addr <= 0x05FE00CF:
		// SCU Registers
		off := addr - 0x05FE0000
		aligned := off &^ 3
		shift := (3 - (off & 3)) * 8
		return uint8(b.scu.Read(aligned) >> shift)

	case addr >= 0x06000000 && addr <= 0x07FFFFFF:
		// Work RAM-H (1 MB mirrored across 32 MB)
		return b.wramH[addr&0x0FFFFF]

	default:
		fmt.Printf("[BUS] unmapped 8-bit read from 0x%08X\n", addr)
		return 0
	}
}

// Write8 writes a byte to the given address, claiming the address's
// bus area for the duration of the access.
func (b *Bus) Write8(addr uint32, val uint8) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	b.write8Impl(addr, val)
	b.unlockArea(area)
}

func (b *Bus) write8Impl(addr uint32, val uint8) {
	addr &= 0x07FFFFFF

	switch {
	case addr <= 0x0007FFFF:
		// BIOS ROM - writes ignored

	case addr >= 0x00100000 && addr <= 0x0017FFFF:
		// SMPC (128 bytes mirrored across 512 KB)
		b.smpc.Write(uint8(addr&0x7F), val)

	case addr >= 0x00180000 && addr <= 0x0018FFFF:
		// Backup RAM (32 KB mirrored, odd bytes only)
		if addr&1 != 0 {
			b.backup[(addr>>1)&0x7FFF] = val
		}

	case addr >= 0x00200000 && addr <= 0x002FFFFF:
		// Work RAM-L
		b.wramL[addr&0x0FFFFF] = val

	case addr >= 0x01000000 && addr <= 0x017FFFFF:
		// MINIT region: byte writes do not trigger
		fmt.Printf("[BUS] invalid 8-bit write to MINIT region 0x%08X = 0x%02X\n", addr, val)

	case addr >= 0x01800000 && addr <= 0x01FFFFFF:
		// SINIT region: byte writes do not trigger
		fmt.Printf("[BUS] invalid 8-bit write to SINIT region 0x%08X = 0x%02X\n", addr, val)

	case addr >= cs0Base && addr <= cs0Top:
		// A-Bus CS0: Extended RAM Cartridge
		if addr >= extRAMBase && addr <= extRAMTop {
			b.extRAM[addr&extRAMMask] = val
		}

	case addr >= 0x04000000 && addr <= 0x04FFFFFF:
		// A-Bus CS1: Cartridge ID (read-only)

	case addr >= 0x05000000 && addr <= 0x057FFFFF:
		// A-Bus Dummy stub

	case addr >= 0x05800000 && addr <= 0x058FFFFF:
		// A-Bus CS2 (CD Block)
		cs2off := addr - 0x05800000
		cs2masked := cs2off & ^uint32(0x80000)
		regOff := cs2off & 0x3F
		if cs2masked >= 0x18000 && cs2masked <= 0x18003 || regOff <= 0x03 {
			// DATATRNS byte write: accumulate, send on low byte
			if cs2off&1 == 0 {
				b.cdDataTRNSCache = (b.cdDataTRNSCache & 0x00FF) | (uint16(val) << 8)
			} else {
				b.cdDataTRNSCache = (b.cdDataTRNSCache & 0xFF00) | uint16(val)
				b.cdblock.Write(0x0000, b.cdDataTRNSCache)
			}
		} else {
			fmt.Printf("[CDBLOCK] dropped 8-bit write to 0x%08X = 0x%02X\n", addr, val)
		}

	case addr >= 0x05A00000 && addr <= 0x05A7FFFF:
		// Sound RAM (512 KB)
		b.scsp.WriteRAM(addr&0x7FFFF, val)

	case addr >= 0x05A80000 && addr <= 0x05AFFFFF:
		// SCSP B-Bus gap between sound RAM and registers.
		// See Read8 for rationale. Silent drop.

	case addr >= 0x05B00000 && addr <= 0x05B00EE3:
		// SCSP registers are 16-bit on the SCSP chip side but the SCU
		// B-bus bridge translates main-CPU byte writes into 16-bit
		// RMW cycles. BIOS startup issues a byte write to set MEM4MB
		// per Sec 1.1, so the path must succeed.
		off := addr - 0x05B00000
		aligned := off &^ 1
		cur := b.scsp.Read(aligned)
		if off&1 == 0 {
			cur = (cur & 0x00FF) | (uint16(val) << 8)
		} else {
			cur = (cur & 0xFF00) | uint16(val)
		}
		b.scsp.Write(aligned, cur)

	case addr >= 0x05B00EE4 && addr <= 0x05BFFFFF:
		// Unmapped SCSP range

	case addr >= 0x05C00000 && addr <= 0x05C7FFFF:
		// VDP1 VRAM
		b.vdp1.WriteVRAM(addr-0x05C00000, val)

	case addr >= 0x05C80000 && addr <= 0x05CBFFFF:
		// VDP1 Frame Buffer
		b.vdp1.WriteFB(addr-0x05C80000, val)

	case addr >= 0x05D00000 && addr <= 0x05D00017:
		// VDP1 Registers: word access only
		fmt.Printf("[BUS] invalid 8-bit write to VDP1 register 0x%08X = 0x%02X\n", addr, val)

	case addr >= 0x05E00000 && addr <= 0x05E7FFFF:
		// VDP2 VRAM
		b.vdp2.WriteVRAM(addr-0x05E00000, val)

	case addr >= 0x05F00000 && addr <= 0x05F00FFF:
		// VDP2 CRAM
		b.vdp2.WriteCRAM(addr-0x05F00000, val)

	case addr >= 0x05F80000 && addr <= 0x05F8011F:
		// VDP2 Registers: word/long access only
		fmt.Printf("[BUS] invalid 8-bit write to VDP2 register 0x%08X = 0x%02X\n", addr, val)

	case addr >= 0x05FE0000 && addr <= 0x05FE00CF:
		// SCU Registers (use ReadInternal for byte-write composition
		// since many SCU registers are write-only)
		off := addr - 0x05FE0000
		aligned := off &^ 3
		shift := (3 - (off & 3)) * 8
		cur := b.scu.ReadInternal(aligned)
		cur = (cur &^ (0xFF << shift)) | (uint32(val) << shift)
		b.scu.Write(aligned, cur)

	case addr >= 0x06000000 && addr <= 0x07FFFFFF:
		// Work RAM-H (1 MB mirrored across 32 MB)
		b.wramH[addr&0x0FFFFF] = val

	default:
		fmt.Printf("[BUS] unmapped 8-bit write to 0x%08X = 0x%02X\n", addr, val)
	}
}

// Read16 reads a big-endian 16-bit value from the given address,
// claiming the address's bus area for the duration of the access.
func (b *Bus) Read16(addr uint32) uint16 {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	val := b.read16Impl(addr)
	b.unlockArea(area)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 2, uint32(val))
	}
	return val
}

// ReadRMW8 begins a TAS.B read-modify-write: it claims the address's
// bus area and returns with the claim still held. WriteRMW8 completes
// the sequence and releases the claim. Per SH7604 Sec 7.10 the bus is
// not released between the read and write cycles of TAS.
func (b *Bus) ReadRMW8(addr uint32) uint8 {
	b.lockArea(busAreaOf(addr & 0x07FFFFFF))
	val := b.read8Impl(addr)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 1, uint32(val))
	}
	return val
}

// WriteRMW8 completes a TAS.B read-modify-write begun by ReadRMW8,
// writing the value and releasing the bus-area claim.
func (b *Bus) WriteRMW8(addr uint32, val uint8) {
	b.write8Impl(addr, val)
	b.unlockArea(busAreaOf(addr & 0x07FFFFFF))
}

// ReadCacheLine fills dst from the 16-byte-aligned line at base,
// holding the line's bus area across all four longwords. Per SH7604
// Sec 8.4.1 a cache line fill reads four longwords consecutively in
// one bus tenure (an SDRAM burst for Work RAM-H, Sec 7.5.3-7.5.4), so
// another bus master cannot interleave a write between them - claiming
// the area once reproduces that, where four separate Read32 calls
// would release the bus three times mid-fill and admit a torn line.
func (b *Bus) ReadCacheLine(base uint32, dst *[16]byte) {
	area := busAreaOf(base & 0x07FFFFFF)
	b.lockArea(area)
	for i := uint32(0); i < 16; i += 4 {
		v := b.read32Impl(base + i)
		dst[i] = uint8(v >> 24)
		dst[i+1] = uint8(v >> 16)
		dst[i+2] = uint8(v >> 8)
		dst[i+3] = uint8(v)
	}
	b.unlockArea(area)
}

func (b *Bus) read16Impl(addr uint32) uint16 {
	masked := addr & 0x07FFFFFF
	switch {
	case masked >= 0x06000000 && masked <= 0x07FFFFFF:
		off := masked & 0x0FFFFF
		return uint16(b.wramH[off])<<8 | uint16(b.wramH[off+1])
	case masked >= 0x00200000 && masked <= 0x002FFFFF:
		off := masked & 0x0FFFFF
		return uint16(b.wramL[off])<<8 | uint16(b.wramL[off+1])
	case masked <= 0x0007FFFF && b.bios != nil:
		return uint16(b.bios[masked])<<8 | uint16(b.bios[masked+1])
	case masked >= 0x00100000 && masked <= 0x0017FFFF:
		// SMPC: byte access only
		fmt.Printf("[BUS] invalid 16-bit read from SMPC 0x%08X\n", addr)
		return 0
	case masked >= 0x00180000 && masked <= 0x0018FFFF:
		// Backup RAM: byte access only
		fmt.Printf("[BUS] invalid 16-bit read from Backup RAM 0x%08X\n", addr)
		return 0
	case masked >= 0x01000000 && masked <= 0x017FFFFF:
		// MINIT region (trigger-only, no readable data)
		return 0
	case masked >= 0x01800000 && masked <= 0x01FFFFFF:
		// SINIT region (trigger-only, no readable data)
		return 0
	case masked >= 0x05FE0000 && masked <= 0x05FE00CF:
		off := masked - 0x05FE0000
		v := b.scu.Read(off &^ 3)
		shift := (2 - (off & 2)) * 8
		return uint16(v >> shift)
	case masked >= 0x05A00000 && masked <= 0x05A7FFFF:
		return b.scsp.ReadRAM16(masked & 0x7FFFF)
	case masked >= 0x05A80000 && masked <= 0x05AFFFFF:
		// SCSP B-Bus gap. See Read8 for rationale. Silent drop.
		return 0
	case masked >= 0x05B00000 && masked <= 0x05B00EE3:
		return b.scsp.Read((masked - 0x05B00000) &^ 1)
	case masked >= 0x05B00EE4 && masked <= 0x05BFFFFF:
		return 0
	case masked >= 0x05C00000 && masked <= 0x05C7FFFF:
		return b.vdp1.ReadVRAM16(masked - 0x05C00000)
	case masked >= 0x05C80000 && masked <= 0x05CBFFFF:
		return b.vdp1.ReadFB16(masked - 0x05C80000)
	case masked >= 0x05D00000 && masked <= 0x05D00017:
		// VDP1 Registers: word access
		return b.vdp1.Read((masked - 0x05D00000) &^ 1)
	case masked >= 0x05E00000 && masked <= 0x05E7FFFF:
		return b.vdp2.ReadVRAM16(masked - 0x05E00000)
	case masked >= 0x05F00000 && masked <= 0x05F00FFF:
		return b.vdp2.ReadCRAM16(masked - 0x05F00000)
	case masked >= 0x05F80000 && masked <= 0x05F8011F:
		// VDP2 Registers: word access
		return b.vdp2.Read((masked - 0x05F80000) &^ 1)
	case masked >= extRAMBase && masked <= extRAMTop:
		off := masked & extRAMMask
		return uint16(b.extRAM[off])<<8 | uint16(b.extRAM[off+1])
	case masked >= cs0Base && masked <= cs0Top:
		// A-Bus CS0: non-extRAM area (open bus)
		return 0xFFFF
	case masked >= 0x04000000 && masked <= 0x04FFFFFF:
		// A-Bus CS1: Cartridge ID (read-only, 16-bit bus)
		if masked&0x00FFFFFE == 0x00FFFFFE {
			return 0xFF00 | uint16(cartID4MB)
		}
		return 0xFFFF
	case masked >= 0x05000000 && masked <= 0x057FFFFF:
		// A-Bus Dummy (nothing connected)
		return 0
	case masked >= 0x05800000 && masked <= 0x058FFFFF:
		// A-Bus CS2 (CD Block)
		cs2off := masked - 0x05800000
		cs2masked := cs2off & ^uint32(0x80000)
		if cs2masked >= 0x18000 && cs2masked <= 0x18003 || (cs2off&0x3F) <= 0x03 {
			return b.cdblock.ReadDataTRNS()
		}
		return b.cdblock.Read(cs2off & 0x3E)
	default:
		fmt.Printf("[BUS] unmapped 16-bit read from 0x%08X\n", addr)
		return 0
	}
}

// Read32 reads a big-endian 32-bit value from the given address,
// claiming the address's bus area for the duration of the access.
func (b *Bus) Read32(addr uint32) uint32 {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	val := b.read32Impl(addr)
	b.unlockArea(area)
	if b.ReadTrace != nil {
		b.ReadTrace(addr, 4, val)
	}
	return val
}

func (b *Bus) read32Impl(addr uint32) uint32 {
	masked := addr & 0x07FFFFFF
	switch {
	case masked >= 0x06000000 && masked <= 0x07FFFFFF:
		off := masked & 0x0FFFFF
		return uint32(b.wramH[off])<<24 | uint32(b.wramH[off+1])<<16 |
			uint32(b.wramH[off+2])<<8 | uint32(b.wramH[off+3])
	case masked >= 0x00200000 && masked <= 0x002FFFFF:
		off := masked & 0x0FFFFF
		return uint32(b.wramL[off])<<24 | uint32(b.wramL[off+1])<<16 |
			uint32(b.wramL[off+2])<<8 | uint32(b.wramL[off+3])
	case masked <= 0x0007FFFF && b.bios != nil:
		return uint32(b.bios[masked])<<24 | uint32(b.bios[masked+1])<<16 |
			uint32(b.bios[masked+2])<<8 | uint32(b.bios[masked+3])
	case masked >= 0x00100000 && masked <= 0x0017FFFF:
		// SMPC: byte access only
		fmt.Printf("[BUS] invalid 32-bit read from SMPC 0x%08X\n", addr)
		return 0
	case masked >= 0x00180000 && masked <= 0x0018FFFF:
		// Backup RAM: byte access only
		fmt.Printf("[BUS] invalid 32-bit read from Backup RAM 0x%08X\n", addr)
		return 0
	case masked >= 0x01000000 && masked <= 0x017FFFFF:
		// MINIT region (trigger-only, no readable data)
		return 0
	case masked >= 0x01800000 && masked <= 0x01FFFFFF:
		// SINIT region (trigger-only, no readable data)
		return 0
	case masked >= 0x05FE0000 && masked <= 0x05FE00CF:
		return b.scu.Read(masked - 0x05FE0000)
	case masked >= 0x05A00000 && masked <= 0x05A7FFFF:
		return b.scsp.ReadRAM32(masked & 0x7FFFF)
	case masked >= 0x05A80000 && masked <= 0x05AFFFFF:
		// SCSP B-Bus gap. See Read8 for rationale. Silent drop.
		return 0
	case masked >= 0x05B00000 && masked <= 0x05B00EE3:
		off := (masked - 0x05B00000) &^ 3
		return uint32(b.scsp.Read(off))<<16 | uint32(b.scsp.Read(off+2))
	case masked >= 0x05B00EE4 && masked <= 0x05BFFFFF:
		return 0
	case masked >= 0x05C00000 && masked <= 0x05C7FFFF:
		return b.vdp1.ReadVRAM32(masked - 0x05C00000)
	case masked >= 0x05C80000 && masked <= 0x05CBFFFF:
		return b.vdp1.ReadFB32(masked - 0x05C80000)
	case masked >= 0x05D00000 && masked <= 0x05D00017:
		// VDP1 Registers: word access only
		fmt.Printf("[BUS] invalid 32-bit read from VDP1 register 0x%08X\n", addr)
		return 0
	case masked >= 0x05E00000 && masked <= 0x05E7FFFF:
		return b.vdp2.ReadVRAM32(masked - 0x05E00000)
	case masked >= 0x05F00000 && masked <= 0x05F00FFF:
		return b.vdp2.ReadCRAM32(masked - 0x05F00000)
	case masked >= 0x05F80000 && masked <= 0x05F8011F:
		// VDP2 Registers: long access (two 16-bit reads)
		off := (masked - 0x05F80000) &^ 3
		return uint32(b.vdp2.Read(off))<<16 | uint32(b.vdp2.Read(off+2))
	case masked >= extRAMBase && masked <= extRAMTop:
		off := masked & extRAMMask
		return uint32(b.extRAM[off])<<24 | uint32(b.extRAM[off+1])<<16 |
			uint32(b.extRAM[off+2])<<8 | uint32(b.extRAM[off+3])
	case masked >= cs0Base && masked <= cs0Top:
		// A-Bus CS0: non-extRAM area (open bus)
		return 0xFFFFFFFF
	case masked >= 0x04000000 && masked <= 0x04FFFFFF:
		// A-Bus CS1: 16-bit bus. The SCU services a longword access
		// as two 16-bit A-Bus cycles. Empty CS1 reads open-bus high;
		// the cartridge ID byte appears at 0x04FFFFFF.
		hi, lo := uint32(0xFFFF), uint32(0xFFFF)
		if masked&0x00FFFFFE == 0x00FFFFFE {
			hi = 0xFF00 | uint32(cartID4MB)
		}
		if (masked+2)&0x00FFFFFE == 0x00FFFFFE {
			lo = 0xFF00 | uint32(cartID4MB)
		}
		return hi<<16 | lo
	case masked >= 0x05000000 && masked <= 0x057FFFFF:
		// A-Bus Dummy (nothing connected)
		return 0
	case masked >= 0x05800000 && masked <= 0x058FFFFF:
		// A-Bus CS2 (CD Block)
		cs2off := masked - 0x05800000
		cs2masked := cs2off & ^uint32(0x80000)
		if cs2masked >= 0x18000 && cs2masked <= 0x18003 || (cs2off&0x3F) <= 0x03 {
			return b.cdblock.ReadDataTRNS32()
		}
		reg := b.cdblock.Read(cs2off & 0x3E)
		return uint32(reg)<<16 | uint32(reg)
	default:
		fmt.Printf("[BUS] unmapped 32-bit read from 0x%08X\n", addr)
		return 0
	}
}

// Write16 writes a big-endian 16-bit value to the given address,
// claiming the address's bus area for the duration of the access.
func (b *Bus) Write16(addr uint32, val uint16) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	b.write16Impl(addr, val)
	b.unlockArea(area)
}

func (b *Bus) write16Impl(addr uint32, val uint16) {
	masked := addr & 0x07FFFFFF
	switch {
	case masked <= 0x0007FFFF:
		// BIOS ROM - writes ignored
		return
	case masked >= 0x00100000 && masked <= 0x0017FFFF:
		// SMPC: byte access only
		fmt.Printf("[BUS] invalid 16-bit write to SMPC 0x%08X = 0x%04X\n", addr, val)
		return
	case masked >= 0x00180000 && masked <= 0x0018FFFF:
		// Backup RAM: byte access only
		fmt.Printf("[BUS] invalid 16-bit write to Backup RAM 0x%08X = 0x%04X\n", addr, val)
		return
	case masked >= 0x01000000 && masked <= 0x017FFFFF:
		// MINIT region (16-bit write triggers slave FRT input capture)
		b.minitWritten = true
		return
	case masked >= 0x01800000 && masked <= 0x01FFFFFF:
		// SINIT region (16-bit write triggers master FRT input capture)
		b.sinitWritten = true
		return
	case masked >= 0x06000000 && masked <= 0x07FFFFFF:
		off := masked & 0x0FFFFF
		b.wramH[off] = uint8(val >> 8)
		b.wramH[off+1] = uint8(val)
		return
	case masked >= 0x00200000 && masked <= 0x002FFFFF:
		off := masked & 0x0FFFFF
		b.wramL[off] = uint8(val >> 8)
		b.wramL[off+1] = uint8(val)
		return
	case masked >= 0x05FE0000 && masked <= 0x05FE00CF:
		off := masked - 0x05FE0000
		cur := b.scu.ReadInternal(off &^ 3)
		shift := (2 - (off & 2)) * 8
		mask := uint32(0xFFFF) << shift
		cur = (cur &^ mask) | (uint32(val) << shift)
		b.scu.Write(off&^3, cur)
		return
	case masked >= 0x05A00000 && masked <= 0x05A7FFFF:
		b.scsp.WriteRAM16(masked&0x7FFFF, val)
		return
	case masked >= 0x05A80000 && masked <= 0x05AFFFFF:
		// SCSP B-Bus gap. See Read8 for rationale. Silent drop.
		return
	case masked >= 0x05B00000 && masked <= 0x05B00EE3:
		b.scsp.Write((masked-0x05B00000)&^1, val)
		return
	case masked >= 0x05B00EE4 && masked <= 0x05BFFFFF:
		return
	case masked >= 0x05C00000 && masked <= 0x05C7FFFF:
		b.vdp1.WriteVRAM16(masked-0x05C00000, val)
		return
	case masked >= 0x05C80000 && masked <= 0x05CBFFFF:
		b.vdp1.WriteFB16(masked-0x05C80000, val)
		return
	case masked >= 0x05D00000 && masked <= 0x05D00017:
		// VDP1 Registers: word access
		b.vdp1.Write((masked-0x05D00000)&^1, val)
		return
	case masked >= 0x05E00000 && masked <= 0x05E7FFFF:
		b.vdp2.WriteVRAM16(masked-0x05E00000, val)
		return
	case masked >= 0x05F00000 && masked <= 0x05F00FFF:
		b.vdp2.WriteCRAM16(masked-0x05F00000, val)
		return
	case masked >= 0x05F80000 && masked <= 0x05F8011F:
		// VDP2 Registers: word access
		b.vdp2.Write((masked-0x05F80000)&^1, val)
		return
	case masked >= extRAMBase && masked <= extRAMTop:
		off := masked & extRAMMask
		b.extRAM[off] = uint8(val >> 8)
		b.extRAM[off+1] = uint8(val)
		return
	case masked >= cs0Base && masked <= cs0Top:
		// A-Bus CS0: non-extRAM area (no device, ignored)
		return
	case masked >= 0x04000000 && masked <= 0x04FFFFFF:
		// A-Bus CS1: read-only
		return
	case masked >= 0x05000000 && masked <= 0x057FFFFF:
		// A-Bus Dummy (nothing connected)
		return
	case masked >= 0x05800000 && masked <= 0x058FFFFF:
		// A-Bus CS2 (CD Block)
		cs2off := masked - 0x05800000
		cs2masked := cs2off & ^uint32(0x80000)
		if cs2masked >= 0x18000 && cs2masked <= 0x18003 || (cs2off&0x3F) <= 0x03 {
			b.cdblock.Write(0x0000, val)
		} else {
			b.cdblock.Write(cs2off&0x3E, val)
		}
		return
	default:
		fmt.Printf("[BUS] unmapped 16-bit write to 0x%08X = 0x%04X\n", addr, val)
	}
}

// Write32 writes a big-endian 32-bit value to the given address,
// claiming the address's bus area for the duration of the access.
func (b *Bus) Write32(addr uint32, val uint32) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	b.write32Impl(addr, val)
	b.unlockArea(area)
}

// DMAWrite32 performs a bus-master (SCU-DMA) 32-bit write. It mirrors
// Write32 but, like SH2Write32 for the SH-2, charges its access-class
// VDP1 draw contention for a B-Bus write: a continuous burst, so the
// 16-bit port costs vdp1DMABurstStallPerWord per word, two for a longword.
func (b *Bus) DMAWrite32(addr uint32, val uint32) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	b.write32Impl(addr, val)
	b.unlockArea(area)
	if area == areaBBus {
		b.vdp1.chargeDrawStall(addr, 2*vdp1DMABurstStallPerWord)
	}
}

// DMAWrite8 performs a bus-master (SCU-DMA) byte write. A single byte is
// a sub-word B-Bus access whose burst cost rounds to zero, so no VDP1
// draw stall is charged; the method exists so the DMA payload path uses
// one consistent bus-master write API.
func (b *Bus) DMAWrite8(addr uint32, val uint8) {
	area := busAreaOf(addr & 0x07FFFFFF)
	b.lockArea(area)
	b.write8Impl(addr, val)
	b.unlockArea(area)
}

func (b *Bus) write32Impl(addr uint32, val uint32) {
	masked := addr & 0x07FFFFFF
	switch {
	case masked <= 0x0007FFFF:
		// BIOS ROM - writes ignored
		return
	case masked >= 0x00100000 && masked <= 0x0017FFFF:
		// SMPC: byte access only
		fmt.Printf("[BUS] invalid 32-bit write to SMPC 0x%08X = 0x%08X\n", addr, val)
		return
	case masked >= 0x00180000 && masked <= 0x0018FFFF:
		// Backup RAM: byte access only
		fmt.Printf("[BUS] invalid 32-bit write to Backup RAM 0x%08X = 0x%08X\n", addr, val)
		return
	case masked >= 0x01000000 && masked <= 0x017FFFFF:
		// MINIT region: only 16-bit writes trigger
		fmt.Printf("[BUS] invalid 32-bit write to MINIT region 0x%08X = 0x%08X\n", addr, val)
		return
	case masked >= 0x01800000 && masked <= 0x01FFFFFF:
		// SINIT region: only 16-bit writes trigger
		fmt.Printf("[BUS] invalid 32-bit write to SINIT region 0x%08X = 0x%08X\n", addr, val)
		return
	case masked >= 0x06000000 && masked <= 0x07FFFFFF:
		off := masked & 0x0FFFFF
		b.wramH[off] = uint8(val >> 24)
		b.wramH[off+1] = uint8(val >> 16)
		b.wramH[off+2] = uint8(val >> 8)
		b.wramH[off+3] = uint8(val)
		return
	case masked >= 0x00200000 && masked <= 0x002FFFFF:
		off := masked & 0x0FFFFF
		b.wramL[off] = uint8(val >> 24)
		b.wramL[off+1] = uint8(val >> 16)
		b.wramL[off+2] = uint8(val >> 8)
		b.wramL[off+3] = uint8(val)
		return
	case masked >= 0x05FE0000 && masked <= 0x05FE00CF:
		b.scu.Write(masked-0x05FE0000, val)
		return
	case masked >= 0x05A00000 && masked <= 0x05A7FFFF:
		b.scsp.WriteRAM32(masked&0x7FFFF, val)
		return
	case masked >= 0x05A80000 && masked <= 0x05AFFFFF:
		// SCSP B-Bus gap. See Read8 for rationale. Silent drop.
		return
	case masked >= 0x05B00000 && masked <= 0x05B00EE3:
		off := (masked - 0x05B00000) &^ 3
		b.scsp.Write(off, uint16(val>>16))
		b.scsp.Write(off+2, uint16(val))
		return
	case masked >= 0x05B00EE4 && masked <= 0x05BFFFFF:
		return
	case masked >= 0x05C00000 && masked <= 0x05C7FFFF:
		b.vdp1.WriteVRAM32(masked-0x05C00000, val)
		return
	case masked >= 0x05C80000 && masked <= 0x05CBFFFF:
		b.vdp1.WriteFB32(masked-0x05C80000, val)
		return
	case masked >= 0x05D00000 && masked <= 0x05D00017:
		// VDP1 Registers: word access only
		fmt.Printf("[BUS] invalid 32-bit write to VDP1 register 0x%08X = 0x%08X\n", addr, val)
		return
	case masked >= 0x05E00000 && masked <= 0x05E7FFFF:
		b.vdp2.WriteVRAM32(masked-0x05E00000, val)
		return
	case masked >= 0x05F00000 && masked <= 0x05F00FFF:
		b.vdp2.WriteCRAM32(masked-0x05F00000, val)
		return
	case masked >= 0x05F80000 && masked <= 0x05F8011F:
		// VDP2 Registers: long access (two 16-bit writes)
		off := (masked - 0x05F80000) &^ 3
		b.vdp2.Write(off, uint16(val>>16))
		b.vdp2.Write(off+2, uint16(val))
		return
	case masked >= extRAMBase && masked <= extRAMTop:
		off := masked & extRAMMask
		b.extRAM[off] = uint8(val >> 24)
		b.extRAM[off+1] = uint8(val >> 16)
		b.extRAM[off+2] = uint8(val >> 8)
		b.extRAM[off+3] = uint8(val)
		return
	case masked >= cs0Base && masked <= cs0Top:
		// A-Bus CS0: non-extRAM area (no device, ignored)
		return
	case masked >= 0x04000000 && masked <= 0x04FFFFFF:
		// A-Bus CS1: read-only
		return
	case masked >= 0x05000000 && masked <= 0x057FFFFF:
		// A-Bus Dummy (nothing connected)
		return
	case masked >= 0x05800000 && masked <= 0x058FFFFF:
		// A-Bus CS2 (CD Block)
		cs2off := masked - 0x05800000
		cs2masked := cs2off & ^uint32(0x80000)
		if cs2masked >= 0x18000 && cs2masked <= 0x18003 || (cs2off&0x3F) <= 0x03 {
			b.cdblock.Write32(0x0000, val)
		} else {
			regOff := cs2off & 0x3E
			b.cdblock.Write(regOff, uint16(val>>16))
			b.cdblock.Write(regOff+2, uint16(val))
		}
		return
	default:
		fmt.Printf("[BUS] unmapped 32-bit write to 0x%08X = 0x%08X\n", addr, val)
	}
}
