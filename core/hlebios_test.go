// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/user-none/erings/core/sh2"
)

// makeIPImage builds a synthetic 32 KB IP image with a valid System
// ID header and zero everywhere else. Sufficient to satisfy
// HLEBIOS.Boot.
func makeIPImage() []byte {
	ip := make([]byte, ipSectors*ipSectorSize)
	copy(ip[0:16], []byte("SEGA SEGASATURN "))
	return ip
}

// newHLEBIOSForTest constructs an HLEBIOS bound to a fresh bus and
// SH-2 pair suitable for unit testing.
func newHLEBIOSForTest() (*HLEBIOS, *Bus, *sh2.CPU, *sh2.CPU) {
	bus := newBusForTest()
	master := sh2.New(bus, true)
	slave := sh2.New(bus, false)
	return NewHLEBIOS(bus, master, slave), bus, master, slave
}

func TestHLEBootRejectsBadIP(t *testing.T) {
	h, _, _, _ := newHLEBIOSForTest()
	if err := h.Boot(nil); err == nil {
		t.Fatalf("expected error for nil IP")
	}
	bad := make([]byte, 64)
	copy(bad, []byte("NOT A SATURN DISC"))
	if err := h.Boot(bad); err == nil {
		t.Fatalf("expected error for bad System ID")
	}
}

func TestHLEBootDispatchTableWired(t *testing.T) {
	h, bus, _, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	cases := []struct {
		off  uint32
		want uint32
		name string
	}{
		{wramHSysTable + 0x00, hleSysSetUint, "SETUINT slot"},
		{wramHSysTable + 0x04, hleSysGetUint, "GETUINT slot"},
		{wramHSysTable + 0x10, hleSysSetSint, "SETSINT slot"},
		{wramHSysTable + 0x14, hleSysGetSint, "GETSINT slot"},
		{wramHSysTable + 0x30, hleSysTassem, "TASSEM slot"},
		{wramHSysTable + 0x34, hleSysClrsem, "CLRSEM slot"},
		{wramHSysTable + 0x40, hleSysSetScuim, "SETSCUIM slot"},
		{wramHSysTable + 0x44, hleSysChgScuim, "CHGSCUIM slot"},
		{wramHSysTable + 0x20, hleSysChgSysCk, "CHGSYSCK slot"},
		{wramHSysTable + 0x08, wramHNoopHandlerAddr, "unused slot points to $0600083C (no-op handler)"},
		{wramHSysTable + 0x24, 0, "slot $324 is data zero per real-BIOS handoff"},
		{wramHSysTable + 0x50, 0, "slot $350 is data zero per real-BIOS handoff"},
		{wramHSysTable + 0x54, 0, "slot $354 is data zero per real-BIOS handoff"},
		{wramHUIntTable + 0x40*4, wramHNoopHandlerAddr, "user-handler slot for vec $40 (effective $0A00)"},
		{wramHUIntTable + 0x5F*4, wramHNoopHandlerAddr, "user-handler slot for vec $5F (effective $0A7C)"},
		{wramHIPEntryPtr, ipEntry, "IP entry pointer"},
		{wramHWorkspacePtr, 0x06000000 + wramHWorkspace, "workspace pointer"},
	}
	for _, c := range cases {
		if got := bus.readWramHU32(c.off); got != c.want {
			t.Errorf("%s @+%X = %08X, want %08X", c.name, c.off, got, c.want)
		}
	}
}

func TestHLEBootSetsMasterPC(t *testing.T) {
	h, _, master, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	r := master.Registers()
	if r.PC != ipEntry {
		t.Errorf("master PC = %08X, want %08X", r.PC, ipEntry)
	}
	if r.R[15] != ipStack {
		t.Errorf("master R15 = %08X, want %08X", r.R[15], ipStack)
	}
}

func TestHLEBootWiresHooks(t *testing.T) {
	h, _, master, slave := newHLEBIOSForTest()
	if master.HLEHook != nil {
		t.Fatal("master.HLEHook should be nil before Boot")
	}
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if master.HLEHook == nil {
		t.Error("master.HLEHook not wired after Boot")
	}
	if slave.HLEHook == nil {
		t.Error("slave.HLEHook not wired after Boot")
	}
}

func TestHLEMagicAddrTrapsAndReturnsViaPR(t *testing.T) {
	// Landing on a magic address must (a) fire the registered Go
	// service and (b) act as if RTS had executed (PC := PR), without
	// the bus seeing the fetch at all. The mechanism replaces the
	// older "bus serves fake RTS+NOP" trampoline.
	h, _, master, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	const returnAddr uint32 = 0x06010000
	master.SetPR(returnAddr)
	master.SetPC(hleSysSetUint)

	// Step once. The CPU should detect PC in the magic range, invoke
	// the HLEHook (which runs the Go service for SYS_SETUINT), then
	// jump to PR. We don't need to verify the service's side effect
	// here — Boot's wiring is verified by TestHLEBootDispatchTableWired,
	// and the service body has its own dedicated test below.
	master.Clock()

	if got := master.Registers().PC; got != returnAddr {
		t.Errorf("after magic-addr trap, PC = %08X, want %08X (PR)", got, returnAddr)
	}
}

func TestHLESysSetUintService(t *testing.T) {
	// Regression: SETUINT with vec=$40 must land at $06000A00, not
	// fold to slot 0 via & 0x1F. Real BIOS does SHLL2 R4 + write at
	// base+R4*4.
	_, bus, master, _ := newHLEBIOSForTest()
	master.SetReg(4, 0x40)       // VBLANK_IN vector
	master.SetReg(5, 0x06010234) // handler address
	hleSysSetUintService(master, bus)
	if got := bus.readWramHU32(wramHUIntTable + 0x40*4); got != 0x06010234 {
		t.Errorf("SETUINT(vec=$40) did not write at $0A00: got %08X", got)
	}
	// And the low half of the table is untouched.
	if got := bus.readWramHU32(wramHUIntTable + 0); got == 0x06010234 {
		t.Errorf("SETUINT folded vec $40 onto slot 0 (mask bug regression)")
	}
}

func TestHLESysGetUintService(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	bus.writeWramHU32(wramHUIntTable+0x41*4, 0x06020000)
	master.SetReg(4, 0x41)
	hleSysGetUintService(master, bus)
	if got := master.Registers().R[0]; got != 0x06020000 {
		t.Errorf("GETUINT(vec=$41) returned %08X, want 06020000", got)
	}
}

func TestHLESysSetSintService(t *testing.T) {
	h, bus, master, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	// handler=0 on an SCU vector installs that vector's dispatcher
	// trampoline, not null. Games arm SCU interrupts this way (SFZ3
	// Level-0 DMA, vec $4B); writing null here sent the master through
	// a null vector on the next interrupt.
	wantTramp := uint32(0x06000000) + wramHIntStubBase + (0x4B-0x40)*hleIntStubStride
	master.SetReg(4, 0x4B)
	master.SetReg(5, 0)
	hleSysSetSintService(master, bus)
	if got := bus.readWramHU32(0x4B * 4); got != wantTramp {
		t.Errorf("SETSINT($4B, 0): vector = %08X, want trampoline %08X", got, wantTramp)
	}

	// handler=0 on $4E/$4F defaults to the RTE;NOP no-op, not a trampoline.
	wantNoop := uint32(0x06000000) + wramHDefaultRTE
	master.SetReg(4, 0x4E)
	master.SetReg(5, 0)
	hleSysSetSintService(master, bus)
	if got := bus.readWramHU32(0x4E * 4); got != wantNoop {
		t.Errorf("SETSINT($4E, 0): vector = %08X, want no-op %08X", got, wantNoop)
	}

	// A non-zero handler is written to the vector verbatim.
	master.SetReg(4, 0x4B)
	master.SetReg(5, 0x06043B48)
	hleSysSetSintService(master, bus)
	if got := bus.readWramHU32(0x4B * 4); got != 0x06043B48 {
		t.Errorf("SETSINT($4B, handler): vector = %08X, want 06043B48", got)
	}
}

func TestHLESysTassemService(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	master.SetReg(4, 3)
	hleSysTassemService(master, bus)
	if r0 := master.Registers().R[0]; r0 != 1 {
		t.Errorf("first TASSEM returned %d, want 1 (was free, now acquired)", r0)
	}
	if bus.wramH[wramHSemArray+3] == 0 {
		t.Errorf("TASSEM did not set semaphore byte")
	}
	hleSysTassemService(master, bus)
	if r0 := master.Registers().R[0]; r0 != 0 {
		t.Errorf("second TASSEM returned %d, want 0 (already held)", r0)
	}
}

func TestHLESysClrsemService(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	bus.wramH[wramHSemArray+9] = 0x80
	master.SetReg(4, 9)
	hleSysClrsemService(master, bus)
	if bus.wramH[wramHSemArray+9] != 0 {
		t.Errorf("CLRSEM did not clear semaphore")
	}
}

func TestHLESysSetScuimService(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	master.SetReg(4, 0x1234)
	hleSysSetScuimService(master, bus)
	// SCU IMS is write-only; verify via the shadow that SDK reads.
	if got := bus.readWramHU32(wramHIMSShadow); got != 0x1234 {
		t.Errorf("SETSCUIM shadow = %08X, want 1234", got)
	}
	if got := bus.scu.ReadInternal(0xA0); got != 0x1234 {
		t.Errorf("SETSCUIM live IMS = %08X, want 1234", got)
	}
}

func TestHLESysChgScuimService(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	bus.writeWramHU32(wramHIMSShadow, 0x00FF)
	bus.scu.Write(0xA0, 0x00FF)
	master.SetReg(4, 0xF0F0) // AND mask
	master.SetReg(5, 0x0F00) // OR mask
	hleSysChgScuimService(master, bus)
	// (0x00FF & 0xF0F0) | 0x0F00 = 0x00F0 | 0x0F00 = 0x0FF0
	if got := bus.readWramHU32(wramHIMSShadow); got != 0x0FF0 {
		t.Errorf("CHGSCUIM shadow = %08X, want 0FF0", got)
	}
}

func TestHLESysChgSysCkService(t *testing.T) {
	_, bus, master, _ := newHLEBIOSForTest()
	// Arm the timers as a game would before the clock-mode change.
	bus.scu.Write(0x90, 0x001) // T0C
	bus.scu.Write(0x98, 0x101) // T1MD: timer-enable set
	hleSysChgSysCkService(master, bus)
	if got := bus.scu.ReadInternal(0x98); got&1 != 0 {
		t.Errorf("CHGSYSCK left T1MD=%X, want timer-enable (bit 0) cleared", got)
	}
	if got := bus.scu.ReadInternal(0x90); got != 0x3FF {
		t.Errorf("CHGSYSCK T0C=%X, want 3FF", got)
	}
	if got := bus.scu.ReadInternal(0x94); got != 0x1FF {
		t.Errorf("CHGSYSCK T1S=%X, want 1FF", got)
	}
}

func TestHLEBootInitializesIMSShadow(t *testing.T) {
	h, bus, _, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if got := bus.readWramHU32(wramHIMSShadow); got != 0xBFFF {
		t.Errorf("IMS shadow at boot = %08X, want BFFF", got)
	}
}

func TestHLEDispatchInvokesService(t *testing.T) {
	// End-to-end: setting PC to a magic address and Clocking the CPU
	// once must invoke the registered service. After this Clock the
	// RTS is in flight (delay slot pending) but the side effect of
	// the service is already visible.
	h, bus, master, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	master.SetPC(hleSysSetUint)
	master.SetReg(4, 2)
	master.SetReg(5, 0xCAFEBABE)
	_ = master.Clock()
	if got := bus.readWramHU32(wramHUIntTable + 2*4); got != 0xCAFEBABE {
		t.Errorf("after Clock, slot 2 = %08X, want CAFEBABE", got)
	}
}

func TestHLEBootSetsHandoffRegisters(t *testing.T) {
	h, _, master, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	r := master.Registers()
	if r.VBR != 0x06000000 {
		t.Errorf("VBR = %08X, want 06000000", r.VBR)
	}
	if r.SR != 0x00000001 {
		t.Errorf("SR = %08X, want 00000001", r.SR)
	}
	if r.GBR != 0x25D00000 {
		t.Errorf("GBR = %08X, want 25D00000", r.GBR)
	}
}

func TestHLEDefaultRTEStub(t *testing.T) {
	h, bus, _, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	// RTE = 0x002B, NOP = 0x0009. Stored big-endian at $06000400.
	want := []byte{0x00, 0x2B, 0x00, 0x09}
	got := bus.wramH[wramHDefaultRTE : wramHDefaultRTE+4]
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("default RTE byte +%d = %02X, want %02X", i, got[i], want[i])
		}
	}
}

func TestHLEVBRTablePopulated(t *testing.T) {
	h, bus, _, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	defaultAddr := uint32(0x06000000) + wramHDefaultRTE

	// Vec 0, 5, 11, 31 - all should be default RTE.
	for _, vec := range []uint32{0, 5, 11, 31, 0x3F, 0x60, 0x7F} {
		if got := bus.readWramHU32(vec * 4); got != defaultAddr {
			t.Errorf("VBR[%02X] = %08X, want default %08X", vec, got, defaultAddr)
		}
	}
	// SCU vectors $40-$5F point to their per-vector stubs.
	for vec := uint32(0x40); vec <= 0x5F; vec++ {
		wantStub := uint32(0x06000000) + wramHIntStubBase + (vec-0x40)*hleIntStubStride
		if got := bus.readWramHU32(vec * 4); got != wantStub {
			t.Errorf("VBR[%02X] = %08X, want stub %08X", vec, got, wantStub)
		}
	}
}

func TestHLEIntStubLayout(t *testing.T) {
	h, bus, _, _ := newHLEBIOSForTest()
	if err := h.Boot(makeIPImage()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	readWord := func(off uint32) uint16 {
		return uint16(bus.wramH[off])<<8 | uint16(bus.wramH[off+1])
	}
	// Each stub is 3 instructions (6 bytes):
	//   +0  MOV.L R0,@-R15        ; push R0
	//   +2  BRA   wramHIntDispatcher
	//   +4  MOV   #vec,R0          ; delay slot
	// Verify for $40 (VBLANK_IN, first) and $5F (last).
	for _, vec := range []uint32{0x40, 0x5F} {
		base := uint32(wramHIntStubBase) + (vec-0x40)*hleIntStubStride
		if got := readWord(base + 0); got != 0x2F06 {
			t.Errorf("vec $%02X stub +0 = %04X, want 2F06 (MOV.L R0,@-R15)", vec, got)
		}
		braWord := readWord(base + 2)
		if braWord&0xF000 != 0xA000 {
			t.Errorf("vec $%02X stub +2 = %04X, want BRA (high nibble A)", vec, braWord)
		}
		// Decode signed 12-bit disp; target = (base+2) + 4 + disp*2.
		disp := int32(braWord & 0x0FFF)
		if disp&0x800 != 0 {
			disp -= 0x1000
		}
		target := int32(base+2) + 4 + disp*2
		if uint32(target) != uint32(wramHIntDispatcher) {
			t.Errorf("vec $%02X BRA target = %X, want %X (dispatcher)",
				vec, target, wramHIntDispatcher)
		}
		if got := readWord(base + 4); got != 0xE000|uint16(vec&0xFF) {
			t.Errorf("vec $%02X stub +4 = %04X, want MOV #$%02X,R0",
				vec, got, vec)
		}
	}

	// Dispatcher prologue: STS.L PR; STC.L GBR; MOV.L R1..R7,@-R15.
	wantProlog := []uint16{
		0x4F22, // STS.L PR,@-R15
		0x4F13, // STC.L GBR,@-R15
		0x2F16, 0x2F26, 0x2F36, 0x2F46, 0x2F56, 0x2F66, 0x2F76,
	}
	for i, w := range wantProlog {
		off := uint32(wramHIntDispatcher) + uint32(i)*2
		if got := readWord(off); got != w {
			t.Errorf("dispatcher prolog +%X = %04X, want %04X", i*2, got, w)
		}
	}

	// PC-relative load (patched) for the user-handler table base.
	// Format: MOV.L @(disp,PC),R1 = 0xD100 | disp.
	loadOff := uint32(wramHIntDispatcher) + uint32(len(wantProlog))*2
	loadWord := readWord(loadOff)
	if loadWord&0xFF00 != 0xD100 {
		t.Errorf("dispatcher table-load = %04X, want D1xx (MOV.L @(disp,PC),R1)", loadWord)
	}
	pcBase := ((loadOff + 4) & ^uint32(3))
	constOff := pcBase + uint32(loadWord&0xFF)*4
	if got := bus.readWramHU32(constOff); got != 0x06000000+uint32(wramHUIntTable) {
		t.Errorf("dispatcher table-base const = %08X, want %08X",
			got, 0x06000000+uint32(wramHUIntTable))
	}

	// Dispatcher epilogue: pop R7..R1, GBR, PR, R0; RTE; NOP.
	wantEpilog := []uint16{
		0x4008, // SHLL2 R0
		0x001E, // MOV.L @(R0,R1),R0
		0x400B, // JSR @R0
		0x0009, // NOP
		0x67F6, 0x66F6, 0x65F6, 0x64F6, 0x63F6, 0x62F6, 0x61F6,
		0x4F17, // LDC.L @R15+,GBR
		0x4F26, // LDS.L @R15+,PR
		0x60F6, // MOV.L @R15+,R0
		0x002B, // RTE
		0x0009, // NOP
	}
	for i, w := range wantEpilog {
		off := loadOff + 2 + uint32(i)*2
		if got := readWord(off); got != w {
			t.Errorf("dispatcher epilog +%X = %04X, want %04X", off-uint32(wramHIntDispatcher), got, w)
		}
	}
}
