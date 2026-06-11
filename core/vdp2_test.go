// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

func TestVDP2NewDefaults(t *testing.T) {
	v := NewVDP2(NewSCU())

	// All registers should be zero
	for i := 0; i < vdp2RegCount; i++ {
		if v.regs[i] != 0 {
			t.Errorf("regs[%d] = 0x%04X, want 0", i, v.regs[i])
		}
	}

	// NTSC defaults
	if v.activeLines != 224 {
		t.Errorf("activeLines = %d, want 224", v.activeLines)
	}
	if v.linesPerFrame != linesNTSC {
		t.Errorf("linesPerFrame = %d, want %d", v.linesPerFrame, linesNTSC)
	}
	if v.systemCyclesPerLine != systemCyclesPerLine320 {
		t.Errorf("systemCyclesPerLine = %d, want %d", v.systemCyclesPerLine, systemCyclesPerLine320)
	}
	if !v.oddField {
		t.Error("oddField should be true initially")
	}
	if v.pal {
		t.Error("pal should be false initially")
	}
}

func TestVDP2RegisterRoundTrip(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Write/read TVMD (offset 0x0000)
	v.Write(0x0000, 0x8000)
	if got := v.Read(0x0000); got != 0x8000 {
		t.Errorf("TVMD = 0x%04X, want 0x8000", got)
	}

	// Write/read RAMCTL (offset 0x000E)
	v.Write(0x000E, 0x1234)
	if got := v.Read(0x000E); got != 0x1234 {
		t.Errorf("RAMCTL = 0x%04X, want 0x1234", got)
	}

	// Write/read a scroll register (offset 0x0010)
	v.Write(0x0010, 0xABCD)
	if got := v.Read(0x0010); got != 0xABCD {
		t.Errorf("reg at 0x0010 = 0x%04X, want 0xABCD", got)
	}
}

func TestVDP2TVSTATReadOnly(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Write to TVSTAT should be ignored
	v.Write(0x0004, 0xFFFF)

	// TVSTAT should reflect timing state, not written value.
	// Initial state: NTSC, odd field, lineCycle=0, vLine=0
	// Expected: ODD=1, others 0
	wantStat := uint16(tvstatODD)
	got := v.Read(0x0004)
	if got != wantStat {
		t.Errorf("TVSTAT after write = 0x%04X, want 0x%04X", got, wantStat)
	}
}

func TestVDP2HCNTVCNTReadOnly(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Writes should be ignored. HCNT/VCNT return latched snapshots;
	// before any latch the snapshots are zero.
	v.Write(0x0008, 0xFFFF)
	v.Write(0x000A, 0xFFFF)

	if got := v.Read(0x0008); got != 0 {
		t.Errorf("HCNT = 0x%04X, want 0x0000", got)
	}
	if got := v.Read(0x000A); got != 0 {
		t.Errorf("VCNT = 0x%04X, want 0x0000", got)
	}
}

func TestVDP2VRSIZEReadOnly(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.Write(0x0006, 0xFFFF)
	if got := v.Read(0x0006); got != 0 {
		t.Errorf("VRSIZE = 0x%04X, want 0x0000", got)
	}
}

func TestVDP2TVSTATPALBit(t *testing.T) {
	v := NewVDP2(NewSCU())

	if v.Read(0x0004)&tvstatPAL != 0 {
		t.Error("PAL bit should be 0 initially")
	}

	v.SetPAL(true)
	if v.Read(0x0004)&tvstatPAL == 0 {
		t.Error("PAL bit should be 1 after SetPAL(true)")
	}

	v.SetPAL(false)
	if v.Read(0x0004)&tvstatPAL != 0 {
		t.Error("PAL bit should be 0 after SetPAL(false)")
	}
}

func TestVDP2TVSTATODDBit(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Initially odd
	if v.Read(0x0004)&tvstatODD == 0 {
		t.Error("ODD bit should be 1 initially")
	}
}

func TestVDP2TVSTATVBLANKBit(t *testing.T) {
	v := NewVDP2(NewSCU())

	// vLine=0, activeLines=224 -> not in VBlank
	if v.Read(0x0004)&tvstatVBLANK != 0 {
		t.Error("VBLANK should be 0 at vLine=0")
	}

	// Advance to VBlank region
	v.vLine = 224
	if v.Read(0x0004)&tvstatVBLANK == 0 {
		t.Error("VBLANK should be 1 at vLine=224")
	}
}

func TestVDP2TVSTATHBLANKBit(t *testing.T) {
	v := NewVDP2(NewSCU())

	// lineCycle=0 -> not in HBlank
	if v.Read(0x0004)&tvstatHBLANK != 0 {
		t.Error("HBLANK should be 0 at lineCycle=0")
	}

	// Move into HBlank region
	v.lineCycle = activeSystemCycles320
	if v.Read(0x0004)&tvstatHBLANK == 0 {
		t.Error("HBLANK should be 1 at lineCycle >= activeSystemCycles")
	}
}

func TestVDP2TickAdvancesHDot(t *testing.T) {
	v := NewVDP2(NewSCU())

	v.TickSystemCycles(100)
	if v.lineCycle != 100 {
		t.Errorf("lineCycle = %d, want 100", v.lineCycle)
	}
	// HCNT reports a latched value. After Tick(100), normal 320 mode
	// dot count = lineCycle >> 2 = 25. Table 2.3 places H[8:0] in HCT[9:1],
	// so HCNT = 25 << 1 = 50.
	v.Read(0x0002) // latch via EXTEN read
	if got := v.Read(0x0008); got != 50 {
		t.Errorf("HCNT after latch = %d, want 50", got)
	}
}

func TestVDP2EndLineAdvances(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Mid-line position accumulates, then EndLine advances the line
	// counter and resets the intra-line position.
	v.TickSystemCycles(10)
	v.EndLine()
	if v.vLine != 1 {
		t.Errorf("vLine = %d, want 1", v.vLine)
	}
	if v.lineCycle != 0 {
		t.Errorf("lineCycle = %d, want 0 after EndLine", v.lineCycle)
	}
	// Latch then verify VCNT places vLine=1 in VCT[9:1] as 2.
	v.Read(0x0002)
	if got := v.Read(0x000A); got != 2 {
		t.Errorf("VCNT after latch = %d, want 2", got)
	}
}

func TestVDP2VBlankINInterrupt(t *testing.T) {
	scu := NewSCU()
	v := NewVDP2(scu)
	v.Write(0x0000, 0x8000) // DISP=1

	// Advance to line 223 (last active line, 0-indexed)
	v.vLine = 223

	// Complete the line to trigger transition to line 224
	v.EndLine()

	// IST bit 0 = V-Blank-IN
	if scu.ist&(1<<0) == 0 {
		t.Error("VBlank-IN should fire when vLine reaches activeLines")
	}
}

func TestVDP2VBlankOUTInterrupt(t *testing.T) {
	scu := NewSCU()
	v := NewVDP2(scu)
	v.Write(0x0000, 0x8000) // DISP=1

	// Place at last line of frame
	v.vLine = linesNTSC - 1

	// Complete the frame
	v.EndFrame()

	// IST bit 1 = V-Blank-OUT
	if scu.ist&(1<<1) == 0 {
		t.Error("VBlank-OUT should fire when frame wraps")
	}
	if v.vLine != 0 {
		t.Errorf("vLine = %d, want 0 after frame wrap", v.vLine)
	}
}

func TestVDP2HBlankINInterrupt(t *testing.T) {
	scu := NewSCU()
	v := NewVDP2(scu)

	// Complete one line from vLine=0 (active region)
	v.EndLine()

	// IST bit 2 = H-Blank-IN
	if scu.ist&(1<<2) == 0 {
		t.Error("HBlank-IN should fire after completing an active line")
	}
}

func TestVDP2HBlankINNotDuringVBlank(t *testing.T) {
	scu := NewSCU()
	v := NewVDP2(scu)

	// Place in VBlank region
	v.vLine = 224

	// Complete one line
	v.EndLine()

	// IST bit 2 = H-Blank-IN should NOT be set during VBlank
	if scu.ist&(1<<2) != 0 {
		t.Error("HBlank-IN should not fire during VBlank")
	}
}

func TestVDP2FullFrameWrap(t *testing.T) {
	scu := NewSCU()
	v := NewVDP2(scu)
	v.Write(0x0000, 0x8000) // DISP=1

	// Drive an entire frame the way the emulator does: EndLine for
	// every line except the last, EndFrame at the frame boundary.
	for line := 0; line < linesNTSC-1; line++ {
		v.EndLine()
	}
	v.EndFrame()

	if v.vLine != 0 {
		t.Errorf("vLine = %d, want 0 after full frame", v.vLine)
	}
	// All three interrupt types should have been raised at least once
	if scu.ist&(1<<0) == 0 {
		t.Error("VBlank-IN (IST bit 0) should be set after full frame")
	}
	if scu.ist&(1<<1) == 0 {
		t.Error("VBlank-OUT (IST bit 1) should be set after full frame")
	}
	if scu.ist&(1<<2) == 0 {
		t.Error("HBlank-IN (IST bit 2) should be set after full frame")
	}
}

func TestVDP2OddFieldToggles(t *testing.T) {
	v := NewVDP2(NewSCU())

	if !v.oddField {
		t.Error("oddField should be true initially")
	}

	// Complete one frame
	v.EndFrame()

	if v.oddField {
		t.Error("oddField should be false after first frame")
	}

	// Complete another frame
	v.EndFrame()

	if !v.oddField {
		t.Error("oddField should be true after second frame")
	}
}

func TestVDP2VRAMRoundTrip(t *testing.T) {
	v := NewVDP2(NewSCU())

	v.WriteVRAM(0, 0xAB)
	v.WriteVRAM(1, 0xCD)
	v.WriteVRAM(vdp2VRAMSize-1, 0xEF)

	if got := v.ReadVRAM(0); got != 0xAB {
		t.Errorf("VRAM[0] = 0x%02X, want 0xAB", got)
	}
	if got := v.ReadVRAM(1); got != 0xCD {
		t.Errorf("VRAM[1] = 0x%02X, want 0xCD", got)
	}
	if got := v.ReadVRAM(vdp2VRAMSize - 1); got != 0xEF {
		t.Errorf("VRAM[last] = 0x%02X, want 0xEF", got)
	}
}

func TestVDP2CRAMRoundTrip(t *testing.T) {
	v := NewVDP2(NewSCU())

	v.WriteCRAM(0, 0x12)
	v.WriteCRAM(vdp2CRAMSize-1, 0x34)

	if got := v.ReadCRAM(0); got != 0x12 {
		t.Errorf("CRAM[0] = 0x%02X, want 0x12", got)
	}
	if got := v.ReadCRAM(vdp2CRAMSize - 1); got != 0x34 {
		t.Errorf("CRAM[last] = 0x%02X, want 0x34", got)
	}
}

func TestVDP2VRAMAddressMask(t *testing.T) {
	v := NewVDP2(NewSCU())

	v.WriteVRAM(0, 0x42)
	// Address wraps: vdp2VRAMSize should map to 0
	if got := v.ReadVRAM(vdp2VRAMSize); got != 0x42 {
		t.Errorf("VRAM wrap = 0x%02X, want 0x42", got)
	}
}

func TestVDP2CRAMAddressMask(t *testing.T) {
	v := NewVDP2(NewSCU())

	v.WriteCRAM(0, 0x77)
	if got := v.ReadCRAM(vdp2CRAMSize); got != 0x77 {
		t.Errorf("CRAM wrap = 0x%02X, want 0x77", got)
	}
}

func TestVDP2DISPZeroInterruptsStillFire(t *testing.T) {
	scu := NewSCU()
	v := NewVDP2(scu)

	// DISP=0, advance past activeLines
	v.vLine = 223
	v.EndLine()

	// VBlank interrupts fire regardless of DISP - the BIOS relies on
	// receiving VBlank IN with DISP=0 during early boot initialization.
	if scu.ist&(1<<0) == 0 {
		t.Error("VBlank-IN should fire even when DISP=0")
	}
}

func TestVDP2TimingNTSC224(t *testing.T) {
	v := NewVDP2(NewSCU())

	if v.activeLines != 224 {
		t.Errorf("activeLines = %d, want 224", v.activeLines)
	}
	if v.linesPerFrame != 263 {
		t.Errorf("linesPerFrame = %d, want 263", v.linesPerFrame)
	}
}

func TestVDP2TimingNTSC240(t *testing.T) {
	v := NewVDP2(NewSCU())

	// VRESO = 01 (bits 5-4) -> 240 lines
	v.Write(0x0000, 0x0010)
	if v.activeLines != 240 {
		t.Errorf("activeLines = %d, want 240", v.activeLines)
	}
}

func TestVDP2TimingPAL256(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.SetPAL(true)

	// VRESO = 10 (bits 5-4) -> 256 lines
	v.Write(0x0000, 0x0020)
	if v.activeLines != 256 {
		t.Errorf("activeLines = %d, want 256", v.activeLines)
	}
	if v.linesPerFrame != 313 {
		t.Errorf("linesPerFrame = %d, want 313", v.linesPerFrame)
	}
}

func TestVDP2Timing320Mode(t *testing.T) {
	v := NewVDP2(NewSCU())
	if v.systemCyclesPerLine != 1708 {
		t.Errorf("systemCyclesPerLine = %d, want 1708", v.systemCyclesPerLine)
	}
}

func TestVDP2Timing352Mode(t *testing.T) {
	v := NewVDP2(NewSCU())

	// HRESO bit 0 = 1 -> 352 mode
	v.Write(0x0000, 0x0001)
	if v.systemCyclesPerLine != 1820 {
		t.Errorf("systemCyclesPerLine = %d, want 1820", v.systemCyclesPerLine)
	}
}

func TestVDP2Timing640Mode(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.Write(0x0000, 0x0002) // HRESO=010 (640 Hi-Res A)
	if v.activeWidth != 640 {
		t.Errorf("activeWidth = %d, want 640", v.activeWidth)
	}
	if !v.hiRes {
		t.Error("hiRes should be true")
	}
	if v.systemCyclesPerLine != systemCyclesPerLine320 {
		t.Errorf("systemCyclesPerLine = %d, want %d (320 clock)", v.systemCyclesPerLine, systemCyclesPerLine320)
	}
}

func TestVDP2Timing704Mode(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.Write(0x0000, 0x0003) // HRESO=011 (704 Hi-Res B)
	if v.activeWidth != 704 {
		t.Errorf("activeWidth = %d, want 704", v.activeWidth)
	}
	if !v.hiRes {
		t.Error("hiRes should be true")
	}
	if v.systemCyclesPerLine != systemCyclesPerLine352 {
		t.Errorf("systemCyclesPerLine = %d, want %d (352 clock)", v.systemCyclesPerLine, systemCyclesPerLine352)
	}
}

func TestVDP2InterlaceSingleDensity(t *testing.T) {
	v := NewVDP2(NewSCU())
	// LSMD=10 (bits 7:6), VRESO=00 (224 lines)
	v.Write(0x0000, 0x0080) // bit 7 = LSMD1
	if v.activeLines != 224 {
		t.Errorf("activeLines = %d, want 224 (single-density, no doubling)", v.activeLines)
	}
	if v.interlace != 2 {
		t.Errorf("interlace = %d, want 2", v.interlace)
	}
}

func TestVDP2InterlaceDoubleDensity(t *testing.T) {
	v := NewVDP2(NewSCU())
	// LSMD=11 (bits 7:6), VRESO=00 (224). activeLines remains per-field
	// so VBlank-IN fires correctly; double-density rendering is a scope
	// limitation (degrades to per-field output).
	v.Write(0x0000, 0x00C0)
	if v.activeLines != 224 {
		t.Errorf("activeLines = %d, want 224 (per-field)", v.activeLines)
	}
	if v.interlace != 3 {
		t.Errorf("interlace = %d, want 3", v.interlace)
	}
}

func TestVDP2InterlaceDoubleDensityPAL256(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.SetPAL(true)
	// LSMD=11 + VRESO=10 (256 PAL). activeLines per-field.
	v.Write(0x0000, 0x00E0) // bits 7:6=11 (LSMD), bits 5:4=10 (VRESO=256)
	if v.activeLines != 256 {
		t.Errorf("activeLines = %d, want 256 (per-field)", v.activeLines)
	}
}

func TestVDP2InterlaceProhibited(t *testing.T) {
	v := NewVDP2(NewSCU())
	// LSMD=01 (bit 6 only)
	v.Write(0x0000, 0x0040)
	if v.interlace != 0 {
		t.Errorf("interlace = %d, want 0 (prohibited -> non-interlace)", v.interlace)
	}
}

func TestVDP2TVSTATODDNonInterlace(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.Write(0x0000, 0x8000) // DISP=1, LSMD=00 (non-interlace)

	// ODD should always be 1 in non-interlace, regardless of oddField
	v.oddField = true
	stat := v.Read(0x0004)
	if stat&tvstatODD == 0 {
		t.Error("ODD should be 1 in non-interlace (oddField=true)")
	}

	v.oddField = false
	stat = v.Read(0x0004)
	if stat&tvstatODD == 0 {
		t.Error("ODD should still be 1 in non-interlace (oddField=false)")
	}
}

func TestVDP2TVSTATODDInterlace(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.Write(0x0000, 0x8080) // DISP=1, LSMD=10 (single-density interlace)

	v.oddField = true
	stat := v.Read(0x0004)
	if stat&tvstatODD == 0 {
		t.Error("ODD should be 1 when oddField=true in interlace")
	}

	v.oddField = false
	stat = v.Read(0x0004)
	if stat&tvstatODD != 0 {
		t.Error("ODD should be 0 when oddField=false in interlace")
	}
}

func TestVDP2HiResDoubleDensity(t *testing.T) {
	v := NewVDP2(NewSCU())
	// HRESO=010 (640) + LSMD=11 (double-density) + VRESO=00 (224)
	v.Write(0x0000, 0x00C2)
	if v.activeWidth != 640 {
		t.Errorf("activeWidth = %d, want 640", v.activeWidth)
	}
	if v.activeLines != 224 {
		t.Errorf("activeLines = %d, want 224 (per-field)", v.activeLines)
	}
	if !v.hiRes {
		t.Error("hiRes should be true")
	}
	if v.interlace != 3 {
		t.Errorf("interlace = %d, want 3", v.interlace)
	}
}

func TestVDP2TVMDWriteRecalcsTiming(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Start with defaults (224 lines, 320 mode)
	if v.activeLines != 224 {
		t.Fatalf("initial activeLines = %d, want 224", v.activeLines)
	}

	// Write TVMD with VRESO=01 (240), HRESO=1 (352)
	v.Write(0x0000, 0x0011)
	if v.activeLines != 240 {
		t.Errorf("activeLines = %d, want 240", v.activeLines)
	}
	if v.systemCyclesPerLine != 1820 {
		t.Errorf("systemCyclesPerLine = %d, want 1820", v.systemCyclesPerLine)
	}
}

func TestVDP2OutOfRangeOffset(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Reading beyond register space returns 0
	if got := v.Read(0x0200); got != 0 {
		t.Errorf("out of range read = 0x%04X, want 0", got)
	}

	// Writing beyond register space should not panic
	v.Write(0x0200, 0xFFFF)
}

func TestVDP2NewInitialState(t *testing.T) {
	v := NewVDP2(NewSCU())

	// TVMD = 0
	if v.Read(0x0000) != 0 {
		t.Errorf("TVMD = 0x%04X, want 0", v.Read(0x0000))
	}
	// TVSTAT: ODD=1
	wantStat := uint16(tvstatODD)
	if v.Read(0x0004) != wantStat {
		t.Errorf("TVSTAT = 0x%04X, want 0x%04X", v.Read(0x0004), wantStat)
	}
	// VRAM zeroed
	if v.ReadVRAM(0) != 0 {
		t.Errorf("VRAM[0] = 0x%02X, want 0", v.ReadVRAM(0))
	}
	// CRAM zeroed
	if v.ReadCRAM(0) != 0 {
		t.Errorf("CRAM[0] = 0x%02X, want 0", v.ReadCRAM(0))
	}
	// Timing state
	if v.vLine != 0 {
		t.Errorf("vLine = %d, want 0", v.vLine)
	}
	if v.lineCycle != 0 {
		t.Errorf("lineCycle = %d, want 0", v.lineCycle)
	}
	if !v.oddField {
		t.Error("oddField should be true")
	}
	if v.pal {
		t.Error("pal should be false")
	}
	// NTSC timing defaults
	if v.activeLines != 224 {
		t.Errorf("activeLines = %d, want 224", v.activeLines)
	}
	if v.linesPerFrame != linesNTSC {
		t.Errorf("linesPerFrame = %d, want %d", v.linesPerFrame, linesNTSC)
	}
	if v.systemCyclesPerLine != systemCyclesPerLine320 {
		t.Errorf("systemCyclesPerLine = %d, want %d", v.systemCyclesPerLine, systemCyclesPerLine320)
	}
	if v.activeWidth != 320 {
		t.Errorf("activeWidth = %d, want 320", v.activeWidth)
	}
}

func TestVDP2HCNTNormalModeFormat(t *testing.T) {
	v := NewVDP2(NewSCU())
	// Normal 320 mode: dot clock = SH-2/4. Position 128 dots into
	// the active area = 128 * 4 = 512 system clocks.
	v.lineCycle = 512
	v.Read(0x0002) // latch
	// dot = 512 >> 2 = 128; HCNT = (128 & 0x1FF) << 1 = 256.
	if got := v.Read(0x0008); got != 256 {
		t.Errorf("HCNT normal = 0x%04X, want 0x%04X", got, 256)
	}
	// Bit 0 must be clear (HCT0 invalid per Table 2.3).
	if v.Read(0x0008)&1 != 0 {
		t.Error("HCNT bit 0 should be clear in normal mode")
	}
}

func TestVDP2HCNTHiResModeFormat(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.Write(0x0000, 0x0002) // HRESO=010 hi-res 640
	// Hi-res 640: dot clock = SH-2/2. Position 300 dots in = 600 system clocks.
	v.lineCycle = 600
	v.Read(0x0002) // latch
	// dot = 600 >> 1 = 300; HCNT = 300 & 0x3FF = 300.
	if got := v.Read(0x0008); got != 300 {
		t.Errorf("HCNT hi-res = %d, want %d", got, 300)
	}
}

func TestVDP2VCNTNonInterlaceFormat(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.vLine = 100
	v.Read(0x0002) // latch
	// VCNT places V[8:0] in VCT[9:1]: 100 << 1 = 200.
	if got := v.Read(0x000A); got != 200 {
		t.Errorf("VCNT non-interlace = %d, want %d", got, 200)
	}
	if v.Read(0x000A)&1 != 0 {
		t.Error("VCNT bit 0 should be clear in non-interlace")
	}
}

func TestVDP2VCNTDoubleDensityFieldBit(t *testing.T) {
	v := NewVDP2(NewSCU())
	// Enable double-density interlace (LSMD=11).
	v.Write(0x0000, 0x00C0)
	v.vLine = 50

	// oddField=true (initial). Table 2.4: VCT0=0 during odd fields.
	v.Read(0x0002) // latch while odd
	vcntOdd := v.Read(0x000A)
	if vcntOdd != uint16(50<<1) {
		t.Errorf("VCNT odd field = %d, want %d", vcntOdd, 50<<1)
	}
	if vcntOdd&1 != 0 {
		t.Error("VCNT VCT0 should be 0 during odd field")
	}

	// Flip to even field and re-latch.
	v.oddField = false
	v.Read(0x0002)
	vcntEven := v.Read(0x000A)
	if vcntEven != uint16(50<<1)|1 {
		t.Errorf("VCNT even field = %d, want %d", vcntEven, (50<<1)|1)
	}
}

func TestVDP2EXTENLatchesHV(t *testing.T) {
	v := NewVDP2(NewSCU())
	v.lineCycle = 400
	v.vLine = 75

	// Latch via EXTEN read.
	v.Read(0x0002)

	// Advance live counters.
	v.lineCycle = 800
	v.vLine = 150

	// HCNT/VCNT should reflect the LATCHED snapshot (400/75), not live.
	// Normal 320 dot clock: dot = 400 >> 2 = 100; HCNT = 100 << 1 = 200.
	if got := v.Read(0x0008); got != 200 {
		t.Errorf("latched HCNT = %d, want 200", got)
	}
	if got := v.Read(0x000A); got != 150 {
		t.Errorf("latched VCNT = %d, want 150", got)
	}
}

func TestVDP2EXLTFGSetOnLatchClearedOnTVSTATRead(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Before any latch, EXLTFG should be 0.
	if v.Read(0x0004)&(1<<9) != 0 {
		t.Error("EXLTFG should be 0 before latch")
	}

	// Latch via EXTEN read. EXLTFG should be 1.
	v.Read(0x0002)
	stat := v.Read(0x0004)
	if stat&(1<<9) == 0 {
		t.Error("EXLTFG should be 1 after EXTEN read")
	}

	// Reading TVSTAT should have cleared EXLTFG.
	if v.Read(0x0004)&(1<<9) != 0 {
		t.Error("EXLTFG should be cleared on TVSTAT read")
	}
}

func TestVDP2EXTENLatchSkippedWhenEXLTEN1(t *testing.T) {
	v := NewVDP2(NewSCU())
	// Set EXLTEN=1 (bit 9 of EXTEN). External-signal latch path is
	// out of scope; CPU read of EXTEN must NOT latch.
	v.Write(0x0002, 0x0200)

	v.lineCycle = 400
	v.vLine = 75
	v.Read(0x0002) // should not latch

	// HCNT/VCNT should still reflect the startup (zero) latch state.
	if got := v.Read(0x0008); got != 0 {
		t.Errorf("HCNT should remain 0 (no latch), got %d", got)
	}
	if got := v.Read(0x000A); got != 0 {
		t.Errorf("VCNT should remain 0 (no latch), got %d", got)
	}
	// EXLTFG should also remain 0.
	if v.Read(0x0004)&(1<<9) != 0 {
		t.Error("EXLTFG should not be set when EXLTEN=1 on CPU read")
	}
}

func TestVDP2FieldBitExported(t *testing.T) {
	v := NewVDP2(NewSCU())

	// Non-interlace: always 0 regardless of oddField.
	v.regs[vdp2TVMD] = 0x8000
	v.recalcTiming()
	v.oddField = false
	if got := v.FieldBit(); got != 0 {
		t.Errorf("non-interlace even-field FieldBit = %d, want 0", got)
	}
	v.oddField = true
	if got := v.FieldBit(); got != 0 {
		t.Errorf("non-interlace odd-field FieldBit = %d, want 0", got)
	}

	// LSMD=3 even field -> 0; odd field -> 1.
	v.regs[vdp2TVMD] = 0x80C0
	v.recalcTiming()
	v.oddField = false
	if got := v.FieldBit(); got != 0 {
		t.Errorf("LSMD=3 even-field FieldBit = %d, want 0", got)
	}
	v.oddField = true
	if got := v.FieldBit(); got != 1 {
		t.Errorf("LSMD=3 odd-field FieldBit = %d, want 1", got)
	}

	// LSMD=3 + NBG mosaic enabled -> effective LSMD=2; FieldBit must be 0.
	v.regs[vdp2MZCTL] = 0x0001
	if got := v.FieldBit(); got != 0 {
		t.Errorf("LSMD=3 + mosaic FieldBit = %d, want 0 (downgrade)", got)
	}
}
