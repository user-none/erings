// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "fmt"

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

	minitWritten    bool
	sinitWritten    bool
	cdDataTRNSCache uint16 // cached DATATRNS word for byte reads
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

// AccessCycles returns the CPU state count consumed by a single
// bus transaction of the given size (bytes: 1, 2, 4, or 16) at the
// given address. Used by the SH-2 DMAC to accumulate per-unit
// bus-occupation stall.
//
//	SH-2 BSC  WCR  = 0x5555            (SH7604 manual Sec 7.2.3)
//	SH-2 BSC  MCR  = 0x78              (SH7604 manual Sec 7.2.4)
//	SCU       ASR0 = 0x1FF01FF0        (SCU manual Sec 3, Fig 3.24)
//	SCU       ASR1 = 0x1FF01FF0
//
// combined with the per-field formulas from the SH7604 and SCU
// user manuals. See each region's comment for the exact arithmetic.
//
// These constants also serve as the correct defaults for a future
// HLE BIOS, which would skip the real BIOS init - the values are
// what every shipped Saturn BIOS configures at boot. If a specific
// game is ever found to reprogram BSC or SCU in a way that matters
// for DMA timing, switch to reading the live register state.
//
// B-Bus costs (VDP1/VDP2/SCSP/CD) are not register-driven; they
// reflect the SCU's architectural arbitration overhead. Those
// numbers are educated estimates aligned with reference emulator
// behavior rather than decoded from registers.
func (b *Bus) AccessCycles(addr uint32, size uint32) uint32 {
	masked := addr & 0x07FFFFFF

	var base uint32
	switch {
	// CS0 partitions: BIOS ROM, SMPC, Backup RAM, Work RAM-L,
	// MINIT/SINIT. Ordinary-space with SH-2 WCR wait_CS0 = 1:
	//   states = 2 (base) + 1 (wait) = 3
	// (SH7604 Sec 7.2.3 + observed WCR=0x5555.)
	case masked < 0x02000000:
		base = 3

	// A-Bus CS0 (cartridge ROM / extended RAM).
	// SH-2 BSC: 3 states (WCR wait_CS1 = 1).
	// SCU A-Bus: 2 base + 15 waits = 17 states
	// (SCU manual Sec 3 + observed ASR0 upper half = 0x1FF0:
	//  A0EWT=1, A0BW=0xF=15, A0NW=0xF=15).
	// Total: 3 + 17 = 20 states per access.
	case masked < 0x04000000:
		base = 20

	// A-Bus CS1 (cartridge ID, A-Bus dummy high).
	// Same derivation from ASR0 lower half: A1EWT=1, A1BW=0xF=15,
	// A1NW=0xF=15. Total: 3 + 17 = 20.
	case masked < 0x05000000:
		base = 20

	// A-Bus Dummy region. SCU ASR1 observed = 0x1FF01FF0, same
	// decoding as ASR0. Total: 20.
	case masked < 0x05800000:
		base = 20

	// CD Block (B-Bus via SCU).
	// BSC 3, SCU B-Bus arbitration architectural ~7 states (not
	// register-driven; estimate from CD-block response timing).
	case masked < 0x05900000:
		base = 10

	// SCSP sound RAM (512 KB) + registers (B-Bus). SCSP runs at
	// 11.3 MHz vs SH-2 28.6 MHz; B-Bus arbitration adds synchronization
	// cost. BSC 3 + B-Bus ~9 = 12 states. The 0x05A80000-0x05AFFFFF
	// gap between sound RAM and SCSP registers is unmapped on real
	// hardware; writes drop silently and fall to the default
	// unmapped cost.
	case masked >= 0x05A00000 && masked < 0x05A80000:
		base = 12
	case masked >= 0x05B00000 && masked < 0x05C00000:
		base = 12

	// VDP1 VRAM / framebuffer / registers (B-Bus). Contends with
	// VDP1 command-list DMA. BSC 3 + B-Bus ~5 = 8 states typical
	// idle.
	case masked >= 0x05C00000 && masked < 0x05D00020:
		base = 8

	// VDP2 VRAM (B-Bus). Contends with VDP2 rendering during
	// active scanlines. We use the idle-window cost; refining to
	// active-scanline arbitration is a later improvement.
	// BSC 3 + B-Bus ~5 = 8 states.
	case masked >= 0x05E00000 && masked < 0x05E80000:
		base = 8

	// VDP2 CRAM and registers. Less contended than VRAM.
	// BSC 3 + B-Bus ~2 = 5 states.
	case masked >= 0x05F00000 && masked < 0x05F80120:
		base = 5

	// SCU registers (direct, no B-Bus arbitration).
	// BSC 3 + negligible = 5 states.
	case masked >= 0x05FE0000 && masked < 0x05FE0100:
		base = 5

	// Work RAM-H on SH-2 CS3, SDRAM mode (MCR=0x78).
	// Initial access: 3 states (matches CS3 ordinary wait=1).
	// Burst beats: 1 state each (SDRAM burst mode).
	// Base non-burst cost is 3; 16-byte burst is handled below.
	case masked >= 0x06000000 && masked < 0x08000000:
		base = 3

	default:
		// Unmapped or reserved. Nominal 4 states.
		base = 4
	}

	// 16-byte burst: 4 successive longword accesses. For Work
	// RAM-H (SDRAM) the initial longword pays the full cost, three
	// subsequent burst beats at 1 state each. For non-SDRAM
	// regions burst mode does not apply, so cost is 4 * base.
	if size == 16 {
		if masked >= 0x06000000 && masked < 0x08000000 {
			return base + 3
		}
		return base * 4
	}
	return base
}

// Read8 reads a byte from the given address.
func (b *Bus) Read8(addr uint32) uint8 {
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

// Write8 writes a byte to the given address.
func (b *Bus) Write8(addr uint32, val uint8) {
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

// Read16 reads a big-endian 16-bit value from the given address.
func (b *Bus) Read16(addr uint32) uint16 {
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

// Read32 reads a big-endian 32-bit value from the given address.
func (b *Bus) Read32(addr uint32) uint32 {
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

// Write16 writes a big-endian 16-bit value to the given address.
func (b *Bus) Write16(addr uint32, val uint16) {
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

// Write32 writes a big-endian 32-bit value to the given address.
func (b *Bus) Write32(addr uint32, val uint32) {
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
