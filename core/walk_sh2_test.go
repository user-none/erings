// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"
	"time"
)

// Frame-walk tests that run SH-2 code through RunFrame. They cover the
// core's use of the CPU stepping interface: multi-cycle instructions
// and burst DMA stalls crossing chunk and line boundaries, TAS held
// across the master/slave barrier, and MINIT/SINIT capture latency.
// Code is placed in Work RAM-H; interrupts from the SCU stay masked so
// the traces hold only the test program.

const (
	walkCode    = 0x06000000
	walkStack   = 0x06001000
	walkVBR     = 0x06000400
	walkHandler = 0x06000600

	wopNOP     = 0x0009
	wopRTE     = 0x002B
	wopBRASelf = 0xAFFE // BRA to itself
	wopTRAPA20 = 0xC320
	wopDTR3    = 0x4310 // DT R3
	wopMOVTR2  = 0x0229 // MOVT R2
	wopADDR2R3 = 0x332C // ADD R2,R3
	wopTASR1   = 0x411B // TAS.B @R1
)

func wopMOVLS(m, n uint16) uint16 { return 0x2002 | (n << 8) | (m << 4) } // MOV.L Rm,@Rn
func wopMOVWS(m, n uint16) uint16 { return 0x2001 | (n << 8) | (m << 4) } // MOV.W Rm,@Rn
func wopMOVBS(m, n uint16) uint16 { return 0x2000 | (n << 8) | (m << 4) } // MOV.B Rm,@Rn
func wopBF(disp uint8) uint16     { return 0x8B00 | uint16(disp) }
func wopBFS(disp uint8) uint16    { return 0x8F00 | uint16(disp) }
func wopBRA(disp uint16) uint16   { return 0xA000 | (disp & 0xFFF) }

type walkRec struct {
	pc     uint32
	op     uint16
	cycles uint64
}

type walkFixture struct {
	t      *testing.T
	e      *Emulator
	master []walkRec
	slave  []walkRec
}

// newWalkFixture builds an emulator with its frame workers running and
// both CPUs traced. The master starts at walkCode with interrupts
// masked; the slave stays disabled unless enableSlave is called.
func newWalkFixture(t *testing.T) *walkFixture {
	t.Helper()
	e := NewEmulator()
	go e.vdpWorker()
	go e.secondaryWorker()
	t.Cleanup(e.Close)
	f := &walkFixture{t: t, e: e}
	e.SetSH2Trace(
		func(pc uint32, op uint16) { f.master = append(f.master, walkRec{pc, op, e.master.Cycles()}) },
		func(pc uint32, op uint16) { f.slave = append(f.slave, walkRec{pc, op, e.slave.Cycles()}) },
	)
	e.master.SetPC(walkCode)
	e.master.SetReg(15, walkStack)
	e.master.SetVBR(walkVBR)
	e.master.SetSR(0xF0)
	return f
}

func (f *walkFixture) enableSlave(pc uint32) {
	f.e.smpc.sshEnabled = true
	f.e.slave.SetPC(pc)
	f.e.slave.SetReg(15, walkStack-0x400)
	f.e.slave.SetVBR(walkVBR)
	f.e.slave.SetSR(0xF0)
}

func (f *walkFixture) code(addr uint32, ops ...uint16) {
	for i, op := range ops {
		f.e.bus.Write16(addr+uint32(i)*2, op)
	}
}

// runFrames runs n frames, failing the test if the walk does not
// complete (a wait that never resolves).
func (f *walkFixture) runFrames(n int) {
	f.t.Helper()
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			f.e.RunFrame()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		f.t.Fatalf("frame walk did not complete %d frames: a wait never resolved", n)
	}
}

// checkCycleBound: a CPU that ran every frame ends within one
// instruction's worth of cycles past the frame total.
func (f *walkFixture) checkCycleBound(name string, got uint64, frames int) {
	f.t.Helper()
	total := uint64(f.e.frameTotalCycles) * uint64(frames)
	if got < total || got >= total+32 {
		f.t.Errorf("%s cycles after %d frames = %d, want in [%d, %d)", name, frames, got, total, total+32)
	}
}

// TestWalkTRAPAAndBurstDMAAcrossBoundaries: a loop of TRAPA, a TCR
// reload, and a burst DMA kick runs for two frames with four
// different paddings so the 8-cycle TRAPA and the burst stall land at
// every chunk and line alignment. Every TRAPA must be followed by the
// handler's RTE and its delay slot, every kick must be followed by the
// stall, the transfer data must arrive, and the master's cycle count
// must be exact at the frame boundary.
func TestWalkTRAPAAndBurstDMAAcrossBoundaries(t *testing.T) {
	for pad := 0; pad < 4; pad++ {
		t.Run(string(rune('0'+pad)), func(t *testing.T) {
			f := newWalkFixture(t)
			e := f.e
			setupTRAPADMALoop(f, pad)
			f.runFrames(2)

			kickPC := uint32(walkCode + 4)
			dmaStall := uint64(4 * (e.bus.AccessCycles(0x06000800, 4) + e.bus.WriteAccessCycles(0x06000900, 4)))
			traps, kicks := 0, 0
			for i, r := range f.master {
				if i+2 >= len(f.master) {
					break
				}
				switch {
				case r.pc == walkCode && r.op == wopTRAPA20:
					traps++
					h, s := f.master[i+1], f.master[i+2]
					if h.pc != walkHandler || h.op != wopRTE || s.pc != walkHandler+2 {
						t.Fatalf("TRAPA at record %d not followed by RTE and slot: %X/%04X %X/%04X", i, h.pc, h.op, s.pc, s.op)
					}
					if h.cycles-r.cycles < 8 {
						t.Fatalf("TRAPA at record %d cost %d cycles, want at least 8", i, h.cycles-r.cycles)
					}
				case r.pc == kickPC:
					kicks++
					cost := f.master[i+1].cycles - r.cycles
					if cost < 1+dmaStall {
						t.Fatalf("DMA kick at record %d cost %d cycles, want at least %d", i, cost, 1+dmaStall)
					}
				}
			}
			if traps < 100 || kicks < 100 {
				t.Errorf("loop ran %d TRAPAs and %d kicks in two frames, want at least 100 each", traps, kicks)
			}
			if got := e.bus.Read32(0x06000900); got != 0xCAFEF00D {
				t.Errorf("DMA destination = %08X, want CAFEF00D", got)
			}
			f.checkCycleBound("master", e.master.Cycles(), 2)
		})
	}
}

// TestWalkTASAcrossBarrierWithSlaveSpinning: both CPUs spin on TAS of
// one lock byte for two frames, so master TAS instructions straddle
// the chunk barrier while the slave's TAS waits on the held bus claim.
// The walk must not deadlock, exactly one TAS wins, and both CPUs run
// every cycle of both frames.
func TestWalkTASAcrossBarrierWithSlaveSpinning(t *testing.T) {
	f := newWalkFixture(t)
	e := f.e
	const lock = 0x06000A00
	spin := []uint16{wopTASR1, wopMOVTR2, wopADDR2R3, wopBRA(0xFFB), wopNOP} // BRA back to TAS
	f.code(walkCode, spin...)
	f.code(walkCode+0x200, spin...)
	e.master.SetReg(1, lock)
	f.enableSlave(walkCode + 0x200)
	e.slave.SetReg(1, lock)

	f.runFrames(2)

	m, s := e.master.Registers().R[3], e.slave.Registers().R[3]
	if m+s != 1 {
		t.Errorf("TAS wins: master %d, slave %d, want exactly one in total", m, s)
	}
	if got := e.bus.Read8(lock); got != 0x80 {
		t.Errorf("lock byte = %02X, want 80", got)
	}
	if len(f.slave) == 0 {
		t.Fatal("slave executed no instructions")
	}
	f.checkCycleBound("master", e.master.Cycles(), 2)
	f.checkCycleBound("slave", e.slave.Cycles(), 2)
}

// TestWalkMINITSINITCaptureLatency: a MINIT write on the master
// latches the slave's FRC into its ICR, and a SINIT write on the slave
// latches the master's, each within two sync chunks of the write (the
// writer's chunk end plus the receiver's barrier). The bus recognises
// the 16-bit write the BIOS and libraries use. FRC counts phi/8 on
// both CPUs from cycle 0, so ICR times 8 tracks the write cycle.
func TestWalkMINITSINITCaptureLatency(t *testing.T) {
	f := newWalkFixture(t)
	e := f.e
	masterProg := make([]uint16, 0, 24)
	for i := 0; i < 20; i++ {
		masterProg = append(masterProg, wopNOP)
	}
	masterProg = append(masterProg, wopMOVWS(0, 1), wopBRASelf, wopNOP)
	f.code(walkCode, masterProg...)
	e.master.SetReg(1, 0x01000000) // MINIT

	slaveProg := make([]uint16, 0, 44)
	for i := 0; i < 40; i++ {
		slaveProg = append(slaveProg, wopNOP)
	}
	slaveProg = append(slaveProg, wopMOVWS(0, 1), wopBRASelf, wopNOP)
	f.code(walkCode+0x200, slaveProg...)
	f.enableSlave(walkCode + 0x200)
	e.slave.SetReg(1, 0x01800000) // SINIT

	f.runFrames(1)

	findWrite := func(recs []walkRec, pc uint32) (uint64, bool) {
		for _, r := range recs {
			if r.pc == pc {
				return r.cycles, true
			}
		}
		return 0, false
	}
	masterWrite, ok := findWrite(f.master, walkCode+40)
	if !ok {
		t.Fatal("master MINIT write not traced")
	}
	slaveWrite, ok := findWrite(f.slave, walkCode+0x200+80)
	if !ok {
		t.Fatal("slave SINIT write not traced")
	}
	checkCapture := func(name string, cpuFRT func(addr uint32) uint8, writeCycle uint64) {
		t.Helper()
		if cpuFRT(0xFFFFFE11)&0x80 == 0 {
			t.Errorf("%s ICF not set", name)
			return
		}
		icr := uint64(cpuFRT(0xFFFFFE18))<<8 | uint64(cpuFRT(0xFFFFFE19))
		at := icr * 8
		if at+8 < writeCycle || at > writeCycle+2*syncChunkCycles+8 {
			t.Errorf("%s ICR = %d (cycle ~%d), want within two chunks after the write at cycle %d", name, icr, at, writeCycle)
		}
	}
	checkCapture("slave", e.slave.FRT().Read, masterWrite)
	checkCapture("master", e.master.FRT().Read, slaveWrite)
}

// TestWalkHaltedMasterWokenBySCUInterrupt: the SCU raises VBLANK-IN on
// the master goroutine when the line counter reaches the active line
// count, at master cycle activeLines * lineWidth. A master halted by
// SLEEP with the interrupt unmasked must accept it within one sync
// chunk of that cycle, with the instruction after SLEEP as the return
// address.
func TestWalkHaltedMasterWokenBySCUInterrupt(t *testing.T) {
	f := newWalkFixture(t)
	e := f.e
	e.master.SetSR(0)              // IMASK 0
	e.master.SetReg(1, 0xBFFE)     // IMS: everything masked except VBLANK-IN
	e.master.SetReg(2, 0x25FE00A0) // SCU IMS
	e.bus.Write32(walkVBR+0x40*4, walkHandler)
	f.code(walkHandler, wopNOP, wopRTE, wopNOP)
	f.code(walkCode, wopMOVLS(1, 2), 0x001B /* SLEEP */, wopNOP, wopBRASelf, wopNOP)

	f.runFrames(1)

	vbl := uint64(e.vdp2.activeLines) * uint64(e.frameLineWidth)
	for i, r := range f.master {
		if r.op != 0xFFFF {
			continue
		}
		if r.pc != walkCode+4 {
			t.Errorf("acceptance return address %X, want %X (after SLEEP)", r.pc, walkCode+4)
		}
		if r.cycles < vbl || r.cycles > vbl+syncChunkCycles {
			t.Errorf("accepted at cycle %d, want within one chunk of VBLANK-IN at %d", r.cycles, vbl)
		}
		if i+1 >= len(f.master) || f.master[i+1].pc != walkHandler {
			t.Errorf("handler did not follow the acceptance")
		}
		return
	}
	t.Fatal("no interrupt acceptance traced")
}

// TestWalkFrameBoundarySaveStateContinues: a state serialized at a
// frame boundary, where the master may be inside a multi-cycle
// instruction or a burst DMA stall, restored into a fresh emulator
// produces the same master trace over the next frame as the original.
// The four paddings move where the boundary lands in the loop.
func TestWalkFrameBoundarySaveStateContinues(t *testing.T) {
	for pad := 0; pad < 4; pad++ {
		t.Run(string(rune('0'+pad)), func(t *testing.T) {
			f1 := newWalkFixture(t)
			setupTRAPADMALoop(f1, pad)
			f1.runFrames(1)
			data, err := f1.e.Serialize()
			if err != nil {
				t.Fatalf("Serialize: %v", err)
			}

			f2 := newWalkFixture(t)
			if err := f2.e.Deserialize(data); err != nil {
				t.Fatalf("Deserialize: %v", err)
			}
			f1.master, f2.master = nil, nil
			f1.runFrames(1)
			f2.runFrames(1)

			if len(f1.master) == 0 {
				t.Fatal("original traced no instructions in the second frame")
			}
			if len(f1.master) != len(f2.master) {
				t.Fatalf("second frame: original %d records, restored %d", len(f1.master), len(f2.master))
			}
			for i := range f1.master {
				if f1.master[i] != f2.master[i] {
					t.Fatalf("record %d differs: original %+v, restored %+v", i, f1.master[i], f2.master[i])
				}
			}
			if f1.e.master.Cycles() != f2.e.master.Cycles() {
				t.Errorf("cycle counters differ: %d vs %d", f1.e.master.Cycles(), f2.e.master.Cycles())
			}
		})
	}
}

// setupTRAPADMALoop installs the TRAPA plus burst DMA loop used by the
// boundary tests, with pad NOPs in the loop body. The DMAC is set up
// for a fixed-address longword transfer of 4 units from 0x06000800 to
// 0x06000900 in burst mode; the loop reloads TCR before each kick.
func setupTRAPADMALoop(f *walkFixture, pad int) {
	e := f.e
	e.bus.Write32(0x06000800, 0xCAFEF00D)
	e.master.Write32(0xFFFFFF80, 0x06000800) // SAR
	e.master.Write32(0xFFFFFF84, 0x06000900) // DAR
	e.master.Write32(0xFFFFFFB0, 1)          // DMAOR DME
	e.master.SetReg(1, 0x0811)               // CHCR: fixed, longword, burst, DE
	e.master.SetReg(2, 0xFFFFFF8C)
	e.master.SetReg(3, 0x7FFFFFFF)
	e.master.SetReg(5, 4)
	e.master.SetReg(6, 0xFFFFFF88) // TCR
	e.bus.Write32(walkVBR+0x20*4, walkHandler)
	f.code(walkHandler, wopRTE, wopNOP)
	prog := []uint16{wopTRAPA20, wopMOVLS(5, 6), wopMOVLS(1, 2), wopDTR3}
	for i := 0; i < pad; i++ {
		prog = append(prog, wopNOP)
	}
	// BF/S back to the loop start: target = PC + 4 + disp*2.
	bfsAt := uint32(len(prog)) * 2
	disp := uint8(int8(-(int(bfsAt) + 4) / 2))
	prog = append(prog, wopBFS(disp), wopNOP, wopBRASelf, wopNOP)
	f.code(walkCode, prog...)
}

// TestWalkSlaveEnabledMidFrame: the master issues SSHON through the
// SMPC part way through the frame. The SMPC dispatches it at a later
// scanline tick on the secondary goroutine, which resets the slave; the
// walk then brings the slave's counter up to the system cycle of that
// line and steps it from there. The slave's first instruction therefore
// runs at a line boundary after the COMREG write, and at the frame end
// its counter sits at the frame total like the master's.
func TestWalkSlaveEnabledMidFrame(t *testing.T) {
	f := newWalkFixture(t)
	e := f.e
	// A BIOS image whose reset vectors send the slave to its spin loop.
	bios := make([]byte, 512*1024)
	slavePC := uint32(walkCode + 0x200)
	bios[0], bios[1], bios[2], bios[3] = byte(slavePC>>24), byte(slavePC>>16), byte(slavePC>>8), byte(slavePC)
	sp := uint32(walkStack - 0x400)
	bios[4], bios[5], bios[6], bios[7] = byte(sp>>24), byte(sp>>16), byte(sp>>8), byte(sp)
	if err := e.SetBIOS("main_bios", bios); err != nil {
		t.Fatalf("SetBIOS: %v", err)
	}
	e.master.SetPC(walkCode)
	e.master.SetReg(15, walkStack)

	// Master: count down to place the COMREG write mid-frame, then
	// issue SSHON and spin.
	e.master.SetReg(1, 0x02)       // SSHON
	e.master.SetReg(2, 0x2010001F) // SMPC COMREG
	e.master.SetReg(3, 20000)
	f.code(walkCode,
		wopDTR3,        // 0x00: loop
		wopBF(0xFD),    // 0x02: BF loop
		wopMOVBS(1, 2), // 0x04: COMREG = SSHON
		wopBRASelf,     // 0x06
		wopNOP,
	)
	f.code(slavePC, wopNOP, wopBRASelf, wopNOP)

	f.runFrames(1)

	if !e.smpc.SSHEnabled() {
		t.Fatal("slave not enabled by the end of the frame")
	}
	writeCycle, ok := uint64(0), false
	for _, r := range f.master {
		if r.pc == walkCode+4 {
			writeCycle, ok = r.cycles, true
			break
		}
	}
	if !ok {
		t.Fatal("COMREG write not traced")
	}
	total := uint64(e.frameTotalCycles)
	lineWidth := uint64(e.frameLineWidth)
	if len(f.slave) == 0 {
		t.Fatal("slave executed no instructions")
	}
	first := f.slave[0]
	if first.pc != slavePC {
		t.Errorf("slave first record pc %X, want %X", first.pc, slavePC)
	}
	if first.cycles <= writeCycle || first.cycles >= total {
		t.Errorf("slave first instruction at cycle %d, want after the COMREG write at %d and inside the frame of %d", first.cycles, writeCycle, total)
	}
	if first.cycles%lineWidth != 0 {
		t.Errorf("slave first instruction at cycle %d, want a %d-cycle line boundary (enabled at a scanline tick)", first.cycles, lineWidth)
	}
	f.checkCycleBound("slave", e.slave.Cycles(), 1)
}
