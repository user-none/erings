// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import "testing"

func TestOpSimple(t *testing.T) {
	tests := []struct {
		name     string
		op       uint16
		setup    func(*CPU)
		check    func(*CPU) bool
		wantDesc string
		cycles   int
	}{
		{
			name: "CLRT",
			op:   0x0008,
			setup: func(c *CPU) {
				c.reg.SetT()
			},
			check:    func(c *CPU) bool { return c.reg.T() == 0 },
			wantDesc: "T=0",
			cycles:   1,
		},
		{
			name:     "SETT",
			op:       0x0018,
			setup:    func(c *CPU) {},
			check:    func(c *CPU) bool { return c.reg.T() == 1 },
			wantDesc: "T=1",
			cycles:   1,
		},
		{
			name: "CLRMAC",
			op:   0x0028,
			setup: func(c *CPU) {
				c.reg.MACH = 0xDEADBEEF
				c.reg.MACL = 0xCAFEBABE
			},
			check:    func(c *CPU) bool { return c.reg.MACH == 0 && c.reg.MACL == 0 },
			wantDesc: "MACH=0,MACL=0",
			cycles:   1,
		},
		{
			name:     "NOP",
			op:       0x0009,
			setup:    func(c *CPU) {},
			check:    func(c *CPU) bool { return c.reg.PC == 0x12 },
			wantDesc: "PC advanced",
			cycles:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			tt.setup(cpu)
			before := cpu.cycles
			cpu.Clock()
			elapsed := int(cpu.cycles - before)
			if !tt.check(cpu) {
				t.Errorf("%s: check failed, want %s", tt.name, tt.wantDesc)
			}
			if elapsed != tt.cycles {
				t.Errorf("%s: cycles = %d, want %d", tt.name, elapsed, tt.cycles)
			}
		})
	}
}

func TestOpSLEEP(t *testing.T) {
	cpu := newDecodeTestCPU(0x001B)
	before := cpu.cycles

	// Cycle 1: EX - sets halted
	cpu.Clock()
	if !cpu.halted {
		t.Error("SLEEP: halted = false, want true")
	}

	// Cycles 2-3: stall
	cpu.Clock()
	cpu.Clock()

	elapsed := int(cpu.cycles - before)
	if elapsed != 3 {
		t.Errorf("SLEEP: cycles = %d, want 3", elapsed)
	}

	// After pending clears, subsequent steps are 1-cycle halted
	cpu.Clock()
	if int(cpu.cycles-before) != 4 {
		t.Errorf("SLEEP+halted: cycles = %d, want 4", cpu.cycles-before)
	}
}

func TestOpLDC(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		reg   int
		val   uint32
		check func(*CPU) uint32
	}{
		{"LDC_SR", 0x450E, 5, 0x3F2, func(c *CPU) uint32 { return c.reg.SR }},
		{"LDC_GBR", 0x431E, 3, 0x1000, func(c *CPU) uint32 { return c.reg.GBR }},
		{"LDC_VBR", 0x432E, 3, 0x2000, func(c *CPU) uint32 { return c.reg.VBR }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[tt.reg] = tt.val
			before := cpu.cycles
			cpu.Clock()
			got := tt.check(cpu)
			want := tt.val
			if tt.name == "LDC_SR" {
				want = tt.val & srMask
			}
			if got != want {
				t.Errorf("%s: got 0x%X, want 0x%X", tt.name, got, want)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("%s: cycles = %d, want 1", tt.name, cpu.cycles-before)
			}
		})
	}
}

func TestOpSTC(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		destR int
		setup func(*CPU)
		want  uint32
	}{
		{"STC_SR", 0x0502, 5, func(c *CPU) { c.reg.SR = 0xF0 }, 0xF0},
		{"STC_GBR", 0x0512, 5, func(c *CPU) { c.reg.GBR = 0x1000 }, 0x1000},
		{"STC_VBR", 0x0522, 5, func(c *CPU) { c.reg.VBR = 0x2000 }, 0x2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			tt.setup(cpu)
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[tt.destR] != tt.want {
				t.Errorf("%s: R[%d] = 0x%X, want 0x%X", tt.name, tt.destR, cpu.reg.R[tt.destR], tt.want)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("%s: cycles = %d, want 1", tt.name, cpu.cycles-before)
			}
		})
	}
}

func TestOpLDS(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		reg   int
		val   uint32
		check func(*CPU) uint32
	}{
		{"LDS_MACH", 0x430A, 3, 0xABCD, func(c *CPU) uint32 { return c.reg.MACH }},
		{"LDS_MACL", 0x431A, 3, 0x1234, func(c *CPU) uint32 { return c.reg.MACL }},
		{"LDS_PR", 0x432A, 3, 0x5678, func(c *CPU) uint32 { return c.reg.PR }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			cpu.reg.R[tt.reg] = tt.val
			before := cpu.cycles
			cpu.Clock()
			if tt.check(cpu) != tt.val {
				t.Errorf("%s: got 0x%X, want 0x%X", tt.name, tt.check(cpu), tt.val)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("%s: cycles = %d, want 1", tt.name, cpu.cycles-before)
			}
		})
	}
}

func TestOpSTS(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		destR int
		setup func(*CPU)
		want  uint32
	}{
		{"STS_MACH", 0x050A, 5, func(c *CPU) { c.reg.MACH = 0xABCD }, 0xABCD},
		{"STS_MACL", 0x051A, 5, func(c *CPU) { c.reg.MACL = 0x1234 }, 0x1234},
		{"STS_PR", 0x052A, 5, func(c *CPU) { c.reg.PR = 0x5678 }, 0x5678},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			tt.setup(cpu)
			before := cpu.cycles
			cpu.Clock()
			if cpu.reg.R[tt.destR] != tt.want {
				t.Errorf("%s: R[%d] = 0x%X, want 0x%X", tt.name, tt.destR, cpu.reg.R[tt.destR], tt.want)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("%s: cycles = %d, want 1", tt.name, cpu.cycles-before)
			}
		})
	}
}

func TestOpLDCM(t *testing.T) {
	tests := []struct {
		name   string
		op     uint16
		srcR   int
		val    uint32
		check  func(*CPU) uint32
		masked bool
	}{
		{"LDCM_SR", 0x4307, 3, 0x000003F2, func(c *CPU) uint32 { return c.reg.SR }, true},
		{"LDCM_GBR", 0x4317, 3, 0x00001000, func(c *CPU) uint32 { return c.reg.GBR }, false},
		{"LDCM_VBR", 0x4327, 3, 0x00002000, func(c *CPU) uint32 { return c.reg.VBR }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			addr := uint32(0x200)
			cpu.reg.R[tt.srcR] = addr
			bus := cpu.bus.(*testBus)
			bus.Write32(addr, tt.val)
			before := cpu.cycles

			// Cycle 1: EX
			cpu.Clock()
			// Cycle 2: MA read
			s2 := cpu.Clock()
			if s2.Bus != BusRead {
				t.Errorf("cycle 2: bus = %d, want BusRead", s2.Bus)
			}
			// Cycle 3: WB
			cpu.Clock()

			want := tt.val
			if tt.masked {
				want = tt.val & srMask
			}
			got := tt.check(cpu)
			if got != want {
				t.Errorf("%s: got 0x%X, want 0x%X", tt.name, got, want)
			}
			if cpu.reg.R[tt.srcR] != addr+4 {
				t.Errorf("%s: R[%d] = 0x%X, want 0x%X", tt.name, tt.srcR, cpu.reg.R[tt.srcR], addr+4)
			}
			if int(cpu.cycles-before) != 3 {
				t.Errorf("%s: cycles = %d, want 3", tt.name, cpu.cycles-before)
			}
		})
	}
}

func TestOpSTCM(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		destR int
		setup func(*CPU)
		want  uint32
	}{
		{"STCM_SR", 0x4503, 5, func(c *CPU) { c.reg.SR = 0xF0 }, 0xF0},
		{"STCM_GBR", 0x4513, 5, func(c *CPU) { c.reg.GBR = 0x1000 }, 0x1000},
		{"STCM_VBR", 0x4523, 5, func(c *CPU) { c.reg.VBR = 0x2000 }, 0x2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			tt.setup(cpu)
			addr := uint32(0x100)
			cpu.reg.R[tt.destR] = addr
			before := cpu.cycles

			// Cycle 1: EX (pre-decrement)
			cpu.Clock()
			// Cycle 2: MA write
			s2 := cpu.Clock()
			if s2.Bus != BusWrite {
				t.Errorf("cycle 2: bus = %d, want BusWrite", s2.Bus)
			}

			if cpu.reg.R[tt.destR] != addr-4 {
				t.Errorf("%s: R[%d] = 0x%X, want 0x%X", tt.name, tt.destR, cpu.reg.R[tt.destR], addr-4)
			}
			bus := cpu.bus.(*testBus)
			got := bus.Read32(addr - 4)
			if got != tt.want {
				t.Errorf("%s: mem[0x%X] = 0x%X, want 0x%X", tt.name, addr-4, got, tt.want)
			}
			if int(cpu.cycles-before) != 2 {
				t.Errorf("%s: cycles = %d, want 2", tt.name, cpu.cycles-before)
			}
		})
	}
}

func TestOpLDSM(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		srcR  int
		val   uint32
		check func(*CPU) uint32
	}{
		{"LDSM_MACH", 0x4306, 3, 0xABCD0000, func(c *CPU) uint32 { return c.reg.MACH }},
		{"LDSM_MACL", 0x4316, 3, 0x12340000, func(c *CPU) uint32 { return c.reg.MACL }},
		{"LDSM_PR", 0x4326, 3, 0x56780000, func(c *CPU) uint32 { return c.reg.PR }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			addr := uint32(0x200)
			cpu.reg.R[tt.srcR] = addr
			bus := cpu.bus.(*testBus)
			bus.Write32(addr, tt.val)
			before := cpu.cycles
			s := cpu.Clock()
			got := tt.check(cpu)
			if got != tt.val {
				t.Errorf("%s: got 0x%X, want 0x%X", tt.name, got, tt.val)
			}
			if cpu.reg.R[tt.srcR] != addr+4 {
				t.Errorf("%s: R[%d] = 0x%X, want 0x%X", tt.name, tt.srcR, cpu.reg.R[tt.srcR], addr+4)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("%s: cycles = %d, want 1", tt.name, cpu.cycles-before)
			}
			if s.Bus != BusRead {
				t.Errorf("%s: bus = %d, want BusRead", tt.name, s.Bus)
			}
		})
	}
}

func TestOpSTSM(t *testing.T) {
	tests := []struct {
		name  string
		op    uint16
		destR int
		setup func(*CPU)
		want  uint32
	}{
		{"STSM_MACH", 0x4502, 5, func(c *CPU) { c.reg.MACH = 0xABCD }, 0xABCD},
		{"STSM_MACL", 0x4512, 5, func(c *CPU) { c.reg.MACL = 0x1234 }, 0x1234},
		{"STSM_PR", 0x4522, 5, func(c *CPU) { c.reg.PR = 0x5678 }, 0x5678},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := newDecodeTestCPU(tt.op)
			tt.setup(cpu)
			addr := uint32(0x100)
			cpu.reg.R[tt.destR] = addr
			before := cpu.cycles
			s := cpu.Clock()
			if cpu.reg.R[tt.destR] != addr-4 {
				t.Errorf("%s: R[%d] = 0x%X, want 0x%X", tt.name, tt.destR, cpu.reg.R[tt.destR], addr-4)
			}
			bus := cpu.bus.(*testBus)
			got := bus.Read32(addr - 4)
			if got != tt.want {
				t.Errorf("%s: mem[0x%X] = 0x%X, want 0x%X", tt.name, addr-4, got, tt.want)
			}
			if int(cpu.cycles-before) != 1 {
				t.Errorf("%s: cycles = %d, want 1", tt.name, cpu.cycles-before)
			}
			if s.Bus != BusWrite {
				t.Errorf("%s: bus = %d, want BusWrite", tt.name, s.Bus)
			}
		})
	}
}

func TestOpTRAPA(t *testing.T) {
	// TRAPA #0x20 = 0xC320
	cpu := newDecodeTestCPU(0xC320)
	cpu.reg.VBR = 0x400
	cpu.reg.SR = 0xF0
	cpu.reg.R[15] = 0x200

	// Write vector at VBR + (0x20 << 2) = 0x400 + 0x80 = 0x480
	bus := cpu.bus.(*testBus)
	bus.Write32(0x480, 0x00000800)

	returnAddr := cpu.reg.PC + 2 // 0x12 - the instruction after TRAPA
	before := cpu.cycles

	// Step through all 8 cycles
	for i := 0; i < 8; i++ {
		cpu.Clock()
	}

	// PC should now be the vector value
	if cpu.reg.PC != 0x800 {
		t.Errorf("TRAPA: PC = 0x%X, want 0x800", cpu.reg.PC)
	}

	// R15 should have decremented by 8
	if cpu.reg.R[15] != 0x1F8 {
		t.Errorf("TRAPA: R15 = 0x%X, want 0x1F8", cpu.reg.R[15])
	}

	// SR pushed at R15+4 (original R15-4)
	gotSR := bus.Read32(0x1FC)
	if gotSR != 0xF0 {
		t.Errorf("TRAPA: pushed SR = 0x%X, want 0xF0", gotSR)
	}

	// Stacked PC should be the next instruction (return address for RTE)
	gotPC := bus.Read32(0x1F8)
	if gotPC != returnAddr {
		t.Errorf("TRAPA: pushed PC = 0x%X, want 0x%X", gotPC, returnAddr)
	}

	elapsed := int(cpu.cycles - before)
	if elapsed != 8 {
		t.Errorf("TRAPA: cycles = %d, want 8", elapsed)
	}
}

func TestOpTRAPABusActivity(t *testing.T) {
	cpu := newDecodeTestCPU(0xC320)
	cpu.reg.VBR = 0x400
	cpu.reg.SR = 0xF0
	cpu.reg.R[15] = 0x200
	bus := cpu.bus.(*testBus)
	bus.Write32(0x480, 0x00000800)

	wantBus := []BusActivity{
		BusNone,  // Cycle 1: EX
		BusWrite, // Cycle 2: write SR
		BusWrite, // Cycle 3: write PC
		BusNone,  // Cycle 4: vector calc
		BusRead,  // Cycle 5: read vector
		BusNone,  // Cycle 6: refill
		BusNone,  // Cycle 7: refill
		BusNone,  // Cycle 8: refill
	}
	for i, want := range wantBus {
		s := cpu.Clock()
		if s.Bus != want {
			t.Errorf("cycle %d: bus = %d, want %d", i+1, s.Bus, want)
		}
	}
}

// Tests for manual Sec 4.6.2 interrupt-disabled instruction rule.
// The instruction immediately following LDC/LDC.L/STC/STC.L/LDS/
// LDS.L/STS/STS.L must not accept a maskable interrupt. NMI is
// unaffected. Address errors are unaffected (they don't route
// through processInterrupt).

// TestInterruptInhibitAllVariants runs each of the 24 opcode
// handlers in the interrupt-disabled family (8 manual categories
// x 3 CR/SR targets each) and asserts intInhibit is set after.
func TestInterruptInhibitAllVariants(t *testing.T) {
	cases := []struct {
		name string
		run  func(c *CPU)
	}{
		// LDC
		{"LDCSR", func(c *CPU) { c.ir = 0x400E; opLDCSR(c) }},
		{"LDCGBR", func(c *CPU) { c.ir = 0x401E; opLDCGBR(c) }},
		{"LDCVBR", func(c *CPU) { c.ir = 0x402E; opLDCVBR(c) }},
		// LDC.L
		{"LDCMSR", func(c *CPU) { c.ir = 0x4007; opLDCMSR(c) }},
		{"LDCMGBR", func(c *CPU) { c.ir = 0x4017; opLDCMGBR(c) }},
		{"LDCMVBR", func(c *CPU) { c.ir = 0x4027; opLDCMVBR(c) }},
		// STC
		{"STCSR", func(c *CPU) { c.ir = 0x0002; opSTCSR(c) }},
		{"STCGBR", func(c *CPU) { c.ir = 0x0012; opSTCGBR(c) }},
		{"STCVBR", func(c *CPU) { c.ir = 0x0022; opSTCVBR(c) }},
		// STC.L
		{"STCMSR", func(c *CPU) { c.ir = 0x4003; opSTCMSR(c) }},
		{"STCMGBR", func(c *CPU) { c.ir = 0x4013; opSTCMGBR(c) }},
		{"STCMVBR", func(c *CPU) { c.ir = 0x4023; opSTCMVBR(c) }},
		// LDS
		{"LDSMACH", func(c *CPU) { c.ir = 0x400A; opLDSMACH(c) }},
		{"LDSMACL", func(c *CPU) { c.ir = 0x401A; opLDSMACL(c) }},
		{"LDSPR", func(c *CPU) { c.ir = 0x402A; opLDSPR(c) }},
		// LDS.L
		{"LDSMMACH", func(c *CPU) { c.ir = 0x4006; opLDSMMACH(c) }},
		{"LDSMMACL", func(c *CPU) { c.ir = 0x4016; opLDSMMACL(c) }},
		{"LDSMPR", func(c *CPU) { c.ir = 0x4026; opLDSMPR(c) }},
		// STS
		{"STSMACH", func(c *CPU) { c.ir = 0x000A; opSTSMACH(c) }},
		{"STSMACL", func(c *CPU) { c.ir = 0x001A; opSTSMACL(c) }},
		{"STSPR", func(c *CPU) { c.ir = 0x002A; opSTSPR(c) }},
		// STS.L - pre-decrement then write, need R15 to point at writable memory
		{"STSMMACH", func(c *CPU) { c.ir = 0xF002; opSTSMMACH(c) }},
		{"STSMMACL", func(c *CPU) { c.ir = 0xF012; opSTSMMACL(c) }},
		{"STSMPR", func(c *CPU) { c.ir = 0xF022; opSTSMPR(c) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := newTestBus(0x1000)
			cpu := New(bus, true)
			cpu.reg.R[15] = 0x800
			// Provide a reasonable source address for LDC.L/LDS.L paths.
			for i := range cpu.reg.R {
				cpu.reg.R[i] = 0x400
			}
			cpu.reg.R[15] = 0x800
			if cpu.intInhibit {
				t.Fatal("intInhibit should start false")
			}
			tc.run(cpu)
			if !cpu.intInhibit {
				t.Errorf("%s: intInhibit not set", tc.name)
			}
		})
	}
}

// TestInterruptInhibitAfterLDC verifies the one-shot blocks a
// pending DIVU interrupt on the processInterrupt call that follows
// LDC, then clears so the interrupt fires on the next one.
func TestInterruptInhibitAfterLDC(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	bus.Write32(0x0100+0x40*4, 0x00000300)

	// Queue a DIVU level-5 interrupt via the helper.
	assertDIVU(cpu, 5, 0x40)

	// Run LDC Rm,SR directly. Handler sets intInhibit.
	cpu.ir = 0x400E
	opLDCSR(cpu)
	if !cpu.intInhibit {
		t.Fatal("intInhibit should be set after LDC")
	}

	// First processInterrupt after LDC: inhibit consumed, no accept.
	if cpu.processInterrupt() {
		t.Error("interrupt accepted despite inhibit")
	}
	if cpu.intInhibit {
		t.Error("intInhibit should be cleared after check")
	}
	if cpu.pendingOp == popException {
		t.Error("exception dispatch should not be scheduled")
	}

	// Second processInterrupt: interrupt latch still asserted, accept.
	if !cpu.processInterrupt() {
		t.Error("interrupt should be accepted on next cycle")
	}
}

// TestInterruptInhibitAfterLDCL exercises the multi-cycle variant:
// the inhibit flag must persist across the pending cycles and be
// consumed on the first processInterrupt after the op completes.
func TestInterruptInhibitAfterLDCL(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.R[0] = 0x400
	cpu.reg.SR = 0
	bus.Write32(0x400, 0) // loaded into SR
	bus.Write32(0x0100+0x40*4, 0x00000300)

	assertDIVU(cpu, 5, 0x40)

	cpu.ir = 0x4007 // LDC.L @R0+,SR
	opLDCMSR(cpu)
	if !cpu.intInhibit {
		t.Fatal("intInhibit should be set after LDC.L")
	}

	// Drain the two pending cycles - processInterrupt is not called here.
	for cpu.pendingOp != popNone {
		cpu.stepPending()
	}
	if !cpu.intInhibit {
		t.Error("intInhibit should still be set after multi-cycle drain")
	}

	// First processInterrupt post-drain: inhibit consumed, no accept.
	if cpu.processInterrupt() {
		t.Error("interrupt accepted despite inhibit post LDC.L")
	}

	// Next processInterrupt: accept.
	if !cpu.processInterrupt() {
		t.Error("interrupt should fire one cycle after LDC.L")
	}
}

// TestNMIOverridesInhibit verifies NMI (unmaskable) still fires
// even when intInhibit is set.
func TestNMIOverridesInhibit(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	bus.Write32(0x0100+11*4, 0x00000500)

	cpu.ir = 0x400E
	opLDCSR(cpu) // set intInhibit
	cpu.NMI()

	if !cpu.intInhibit {
		t.Fatal("intInhibit should be set after LDC")
	}
	if !cpu.processInterrupt() {
		t.Error("NMI should fire despite intInhibit")
	}
	if cpu.nmiPending {
		t.Error("nmiPending should be cleared after accept")
	}
	// NMI path does NOT consume the inhibit flag - the Sec 4.6.2 rule
	// applies to the instruction after LDC, which did not yet run.
	if !cpu.intInhibit {
		t.Error("intInhibit should survive NMI (it's for the next maskable check)")
	}
}

// TestInhibitOneShot confirms the flag is a strict single-use:
// one processInterrupt call clears it, the next behaves normally.
func TestInhibitOneShot(t *testing.T) {
	bus := newTestBus(0x1000)
	cpu := New(bus, true)
	cpu.reg.VBR = 0x0100
	cpu.reg.R[15] = 0x0800
	cpu.reg.SR = 0
	bus.Write32(0x0100+0x40*4, 0x00000300)

	assertDIVU(cpu, 5, 0x40)

	cpu.ir = 0x400E
	opLDCSR(cpu)

	if cpu.processInterrupt() {
		t.Error("first check: interrupt accepted (should be inhibited)")
	}
	if cpu.intInhibit {
		t.Error("first check: flag should be cleared")
	}
	if !cpu.processInterrupt() {
		t.Error("second check: interrupt should now fire")
	}
}

// TestOpCLRTPreservesOtherBits verifies CLRT only touches the T flag
// (PM section 6.23).
func TestOpCLRTPreservesOtherBits(t *testing.T) {
	cpu := newDecodeTestCPU(0x0008)
	cpu.reg.SR = srTMask | srSMask | srMMask | srQMask
	cpu.Clock()
	var want uint32 = srSMask | srMMask | srQMask
	if cpu.reg.SR != want {
		t.Errorf("SR = 0x%08X, want 0x%08X (only T cleared)", cpu.reg.SR, want)
	}
}

// TestOpCLRTWhenAlreadyZero verifies CLRT leaves T=0 as 0.
func TestOpCLRTWhenAlreadyZero(t *testing.T) {
	cpu := newDecodeTestCPU(0x0008)
	cpu.reg.ClearT()
	cpu.Clock()
	if cpu.reg.T() != 0 {
		t.Errorf("T = %d, want 0", cpu.reg.T())
	}
}

// TestOpSETTPreservesOtherBits verifies SETT only touches the T flag
// (PM section 6.82).
func TestOpSETTPreservesOtherBits(t *testing.T) {
	cpu := newDecodeTestCPU(0x0018)
	cpu.reg.SR = srSMask | srMMask | srQMask
	cpu.Clock()
	var want uint32 = srTMask | srSMask | srMMask | srQMask
	if cpu.reg.SR != want {
		t.Errorf("SR = 0x%08X, want 0x%08X (only T set)", cpu.reg.SR, want)
	}
}

// TestOpSETTWhenAlreadyOne verifies SETT leaves T=1 as 1.
func TestOpSETTWhenAlreadyOne(t *testing.T) {
	cpu := newDecodeTestCPU(0x0018)
	cpu.reg.SetT()
	cpu.Clock()
	if cpu.reg.T() != 1 {
		t.Errorf("T = %d, want 1", cpu.reg.T())
	}
}

// TestOpCLRMACPreservesGeneralRegs verifies CLRMAC does not alter
// general-purpose registers or SR (PM section 6.22).
func TestOpCLRMACPreservesGeneralRegs(t *testing.T) {
	cpu := newDecodeTestCPU(0x0028)
	cpu.reg.MACH = 0xDEADBEEF
	cpu.reg.MACL = 0xCAFEBABE
	cpu.reg.SR = srTMask
	for i := 0; i < 16; i++ {
		cpu.reg.R[i] = uint32(0x100 + i)
	}
	cpu.Clock()
	if cpu.reg.MACH != 0 || cpu.reg.MACL != 0 {
		t.Errorf("MACH:MACL = %08X:%08X, want 0:0", cpu.reg.MACH, cpu.reg.MACL)
	}
	for i := 0; i < 16; i++ {
		if cpu.reg.R[i] != uint32(0x100+i) {
			t.Errorf("R[%d] = 0x%08X, want 0x%08X", i, cpu.reg.R[i], uint32(0x100+i))
		}
	}
	if cpu.reg.SR != srTMask {
		t.Errorf("SR = 0x%08X, want 0x%08X (unchanged)", cpu.reg.SR, srTMask)
	}
}

// TestOpNOPPreservesSR verifies NOP leaves SR completely unchanged
// (PM section 6.52).
func TestOpNOPPreservesSR(t *testing.T) {
	cpu := newDecodeTestCPU(0x0009)
	cpu.reg.SR = srTMask | srSMask | srMMask | srQMask
	before := cpu.reg.SR
	cpu.Clock()
	if cpu.reg.SR != before {
		t.Errorf("SR = 0x%08X, want 0x%08X", cpu.reg.SR, before)
	}
}

// TestOpSTCSRReflectsTBit verifies STC SR,Rn copies the full SR
// including the T bit for both values (PM section 6.87).
func TestOpSTCSRReflectsTBit(t *testing.T) {
	t.Run("stcsr_t_1", func(t *testing.T) {
		// STC SR,R5: 0x0502
		cpu := newDecodeTestCPU(0x0502)
		cpu.reg.SR = srTMask | srSMask
		cpu.Clock()
		if cpu.reg.R[5] != srTMask|srSMask {
			t.Errorf("R5 = 0x%08X, want 0x%08X", cpu.reg.R[5], srTMask|srSMask)
		}
	})
	t.Run("stcsr_t_0", func(t *testing.T) {
		cpu := newDecodeTestCPU(0x0502)
		cpu.reg.SR = srSMask
		cpu.Clock()
		if cpu.reg.R[5] != srSMask {
			t.Errorf("R5 = 0x%08X, want 0x%08X", cpu.reg.R[5], srSMask)
		}
	})
}

// TestOpSTCMWithSP verifies STC.L SR,@-R15 pre-decrements R15 (the SH-2
// stack pointer) and writes the full SR.
func TestOpSTCMWithSP(t *testing.T) {
	// STC.L SR,@-R15: 0x4F03
	cpu := newDecodeTestCPU(0x4F03)
	cpu.reg.SR = srTMask | srSMask
	cpu.reg.R[15] = 0x200
	cpu.Clock()
	cpu.Clock()
	if cpu.reg.R[15] != 0x1FC {
		t.Errorf("R15 = 0x%08X, want 0x1FC", cpu.reg.R[15])
	}
	bus := cpu.bus.(*testBus)
	got := bus.Read32(0x1FC)
	if got != srTMask|srSMask {
		t.Errorf("mem[0x1FC] = 0x%08X, want 0x%08X", got, srTMask|srSMask)
	}
}

// TestOpLDCSRMasksReservedBits verifies LDC Rm,SR clears reserved bits
// outside srMask (PM section 6.41).
func TestOpLDCSRMasksReservedBits(t *testing.T) {
	// LDC R5,SR: 0x450E
	cpu := newDecodeTestCPU(0x450E)
	cpu.reg.R[5] = 0xFFFFFFFF
	cpu.Clock()
	if cpu.reg.SR != srMask {
		t.Errorf("SR = 0x%08X, want 0x%08X (reserved bits should be 0)", cpu.reg.SR, srMask)
	}
}

// TestOpLDCMSRMasksReservedBits verifies LDC.L @Rm+,SR clears bits
// outside srMask when the loaded memory word contains all-ones.
func TestOpLDCMSRMasksReservedBits(t *testing.T) {
	// LDC.L @R3+,SR: 0x4307
	cpu := newDecodeTestCPU(0x4307)
	addr := uint32(0x200)
	cpu.reg.R[3] = addr
	bus := cpu.bus.(*testBus)
	bus.Write32(addr, 0xFFFFFFFF)
	cpu.Clock()
	cpu.Clock()
	cpu.Clock()
	if cpu.reg.SR != srMask {
		t.Errorf("SR = 0x%08X, want 0x%08X", cpu.reg.SR, srMask)
	}
	if cpu.reg.R[3] != addr+4 {
		t.Errorf("R[3] = 0x%08X, want 0x%08X", cpu.reg.R[3], addr+4)
	}
}

// TestOpTRAPAImmZero covers TRAPA #0 which reads the vector at VBR+0
// (PM section 6.116).
func TestOpTRAPAImmZero(t *testing.T) {
	// TRAPA #0: 0xC300
	cpu := newDecodeTestCPU(0xC300)
	cpu.reg.VBR = 0x400
	cpu.reg.SR = 0
	cpu.reg.R[15] = 0x200
	bus := cpu.bus.(*testBus)
	bus.Write32(0x400, 0x00000900) // vector at VBR + 0

	before := cpu.cycles
	for i := 0; i < 8; i++ {
		cpu.Clock()
	}
	if cpu.reg.PC != 0x900 {
		t.Errorf("PC = 0x%08X, want 0x900", cpu.reg.PC)
	}
	if cpu.reg.R[15] != 0x1F8 {
		t.Errorf("R15 = 0x%08X, want 0x1F8", cpu.reg.R[15])
	}
	if int(cpu.cycles-before) != 8 {
		t.Errorf("cycles = %d, want 8", cpu.cycles-before)
	}
}

// TestOpTRAPAImmMax covers TRAPA #0xFF which reads the vector at
// VBR + 0xFF*4 (PM section 6.116).
func TestOpTRAPAImmMax(t *testing.T) {
	// TRAPA #0xFF: 0xC3FF
	cpu := newDecodeTestCPU(0xC3FF)
	cpu.reg.VBR = 0x400
	cpu.reg.SR = 0
	cpu.reg.R[15] = 0x200
	bus := cpu.bus.(*testBus)
	// Vector at VBR + 0xFF*4 = 0x400 + 0x3FC = 0x7FC
	bus.Write32(0x7FC, 0x00000A00)

	for i := 0; i < 8; i++ {
		cpu.Clock()
	}
	if cpu.reg.PC != 0xA00 {
		t.Errorf("PC = 0x%08X, want 0xA00", cpu.reg.PC)
	}
}

// TestOpTRAPAPushesSRAndPC explicitly verifies both SR and the return
// address land on the stack.
func TestOpTRAPAPushesSRAndPC(t *testing.T) {
	cpu := newDecodeTestCPU(0xC310) // TRAPA #0x10
	cpu.reg.VBR = 0x400
	cpu.reg.SR = srTMask | srSMask
	cpu.reg.R[15] = 0x200
	bus := cpu.bus.(*testBus)
	bus.Write32(0x440, 0x00000B00) // VBR + 0x10*4

	returnAddr := cpu.reg.PC + 2
	for i := 0; i < 8; i++ {
		cpu.Clock()
	}
	// After TRAPA, R15 -= 8. SR pushed at R15+4 (original R15-4),
	// PC pushed at R15 (original R15-8).
	if bus.Read32(0x1FC) != srTMask|srSMask {
		t.Errorf("stacked SR = 0x%08X, want 0x%08X", bus.Read32(0x1FC), srTMask|srSMask)
	}
	if bus.Read32(0x1F8) != returnAddr {
		t.Errorf("stacked PC = 0x%08X, want 0x%08X", bus.Read32(0x1F8), returnAddr)
	}
}
