// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "time"

// smpcCmdTimeUsec gives SMPC command execution time in microseconds, per
// SMPC User's Manual Tables 2.1/2.2/2.3. Entries not set fall back to the
// fast-command minimum (30µs) at lookup time.
//
// INTBACK (0x10) initial-dispatch time is approximated here; the full
// peripheral-scan phase is driven separately by IREG0 continue/break
// writes (handled in continueINTBACK), so the dispatch delay only
// covers the setup of the first status reply.
var smpcCmdTimeUsec = [0x20]int{
	0x00: 30,     // MSHON
	0x02: 30,     // SSHON
	0x03: 30,     // SSHOFF
	0x06: 30,     // SNDON
	0x07: 30,     // SNDOFF
	0x08: 40,     // CDON
	0x09: 40,     // CDOFF
	0x0D: 100000, // SYSRES (100ms)
	0x0E: 100000, // CKCHG352 (100ms)
	0x0F: 100000, // CKCHG320 (100ms)
	0x10: 250,    // INTBACK initial dispatch
	0x16: 70,     // SETTIME
	0x17: 40,     // SETSMEM
	0x18: 30,     // NMIREQ
	0x19: 30,     // RESENAB
	0x1A: 30,     // RESDISA
}

// smpcUsecPerLine is the approximate scanline duration. NTSC is 63.37µs
// (263 lines × 60 Hz) and PAL is 63.90µs (313 lines × 50 Hz); the ~1%
// variance between modes is insignificant for command timing.
const smpcUsecPerLine = 64

// SMPC implements the System Manager and Peripheral Control for the Sega Saturn.
// It provides register storage and byte-level access on odd addresses within
// the 0x00100000-0x0010007F range (after bit 29 stripping).
type SMPC struct {
	comreg uint8     // Command register (write-only)
	sr     uint8     // Status register (read-only)
	sf     uint8     // Status flag (bit 0 only, R/W)
	ireg   [7]uint8  // Input registers 0-6 (write-only)
	oreg   [32]uint8 // Output registers 0-31 (read-only)

	pdr1  uint8 // Port data register 1 (7-bit, R/W)
	pdr2  uint8 // Port data register 2 (7-bit, R/W)
	ddr1  uint8 // Data direction register 1 (7-bit, write-only)
	ddr2  uint8 // Data direction register 2 (7-bit, write-only)
	iosel uint8 // I/O select (2-bit, write-only)
	exle  uint8 // External latch enable (2-bit, write-only)

	rtc         [7]uint8  // RTC data (battery-backed, matches SETTIME IREG0-6 format)
	rtcBaseTime time.Time // RTC epoch: the time rtcFrames is counted from
	rtcFrames   uint64    // Emulated frames elapsed since rtcBaseTime
	smem        [4]uint8  // SMPC memory (battery-backed, 4 bytes)

	areaCode uint8 // Configured area code (region), set externally

	intbackActive bool  // INTBACK peripheral collection in progress
	intbackP1MD   uint8 // Port 1 mode from IREG1 (2 bits)
	intbackP2MD   uint8 // Port 2 mode from IREG1 (2 bits)

	padState [2]uint16 // Active-low button data per port (0xFFFF = all released)

	sshEnabled   bool // Slave SH-2 enabled (SSHON/SSHOFF)
	soundEnabled bool // Sound CPU enabled (SNDON/SNDOFF)
	cdEnabled    bool // CD block enabled (CDON/CDOFF)
	resetEnabled bool // Reset button NMI enabled (RESENAB/RESDISA)
	dotsel       bool // Dot clock select: false=320, true=352

	scu         *SCU   // SCU reference for interrupt signaling
	masterNMI   func() // NMI to master SH-2 (NMIREQ)
	systemReset func() // System reset (SYSRES/CKCHG)
	slaveReset  func() // Slave SH-2 reset (SSHON pulses the reset line)

	// Command dispatch is deferred to mirror real SMPC behavior where
	// each command takes bounded wall-clock time. SF stays 1 until the
	// delay elapses, then dispatch runs and clears SF. Games rely on
	// this: typical pattern is to write SF=1 after COMREG and poll.
	// cmdDelay > 0 means a COMREG write is pending dispatch.
	cmdDelay int
}

// NewSMPC allocates an SMPC with correct initial state.
func NewSMPC(scu *SCU) *SMPC {
	s := &SMPC{
		scu:      scu,
		areaCode: 0x04, // North America
		padState: [2]uint16{0xFFFF, 0xFFFF},
	}
	s.initRTC()
	return s
}

// initRTC seeds the RTC base from the host system's current time.
func (s *SMPC) initRTC() {
	s.rtcBaseTime = time.Now()
	s.rtcFrames = 0
	s.timeToRTC(s.rtcBaseTime)
}

// TickFrame advances the emulated RTC by one frame. Called once per
// emulated frame so the RTC tracks emulation time, not wall clock time.
func (s *SMPC) TickFrame() {
	s.rtcFrames++
}

// TickScanline advances the SMPC command-dispatch delay by one scanline.
// When the delay reaches zero, the pending command runs (which clears
// the SF flag). Called once per scanline from the emulator main loop.
func (s *SMPC) TickScanline() {
	if s.cmdDelay <= 0 {
		return
	}
	s.cmdDelay--
	if s.cmdDelay == 0 {
		s.dispatch()
	}
}

// cmdScanlines returns the dispatch delay for the currently staged
// COMREG, converted from the per-command microsecond table into
// scanlines. Always returns at least 1 so dispatch is never immediate.
func (s *SMPC) cmdScanlines() int {
	var usec int
	if int(s.comreg) < len(smpcCmdTimeUsec) {
		usec = smpcCmdTimeUsec[s.comreg]
	}
	if usec <= 0 {
		usec = 30
	}
	lines := (usec + smpcUsecPerLine - 1) / smpcUsecPerLine
	if lines < 1 {
		lines = 1
	}
	return lines
}

// refreshRTC recomputes the RTC bytes from the base time plus elapsed
// emulation frames. NTSC = 60 fps (16.667 ms/frame).
func (s *SMPC) refreshRTC() {
	elapsed := time.Duration(s.rtcFrames) * time.Second / 60
	s.timeToRTC(s.rtcBaseTime.Add(elapsed))
}

// timeToRTC encodes a time.Time into the 7-byte BCD RTC format.
// Format: year_hi, year_lo, (weekday<<4)|month, day, hour, minute, second.
func (s *SMPC) timeToRTC(t time.Time) {
	toBCD := func(v int) uint8 { return uint8((v/10)*16 + v%10) }
	year := t.Year()
	s.rtc[0] = toBCD(year / 100)
	s.rtc[1] = toBCD(year % 100)
	s.rtc[2] = uint8(t.Weekday())<<4 | uint8(t.Month())
	s.rtc[3] = toBCD(t.Day())
	s.rtc[4] = toBCD(t.Hour())
	s.rtc[5] = toBCD(t.Minute())
	s.rtc[6] = toBCD(t.Second())
}

// Read returns the value of the SMPC register at the given byte offset.
// Even offsets return 0. Write-only registers return 0.
func (s *SMPC) Read(offset uint8) uint8 {
	if offset&1 == 0 {
		return 0
	}

	switch {
	case offset >= 0x21 && offset <= 0x5F:
		return s.oreg[(offset-0x21)/2]
	case offset == 0x61:
		return s.sr
	case offset == 0x63:
		return s.sf & 0x01
	case offset == 0x75:
		return s.readPDR(0)
	case offset == 0x77:
		return s.readPDR(1)
	default:
		return 0
	}
}

// readPDR returns the port data register value for the given port (0 or 1).
// In direct I/O mode (IOSEL bit set), input pins return controller button
// data based on the TH:TR output values written to PDR.
func (s *SMPC) readPDR(port int) uint8 {
	var pdr, ddr uint8
	var ioselBit uint8
	if port == 0 {
		pdr = s.pdr1
		ddr = s.ddr1
		ioselBit = s.iosel & 0x01
	} else {
		pdr = s.pdr2
		ddr = s.ddr2
		ioselBit = (s.iosel >> 1) & 0x01
	}

	if ioselBit == 0 {
		// SMPC polling mode - return written value
		return pdr & 0x7F
	}

	// Direct I/O mode: output pins return written value,
	// input pins return controller data
	pad := s.padState[port]
	b1 := uint8(pad >> 8) // Right,Left,Down,Up,Start,A,C,B
	b2 := uint8(pad)      // R,X,Y,Z,L,...

	// Build input value for bits 4-0 (TL, D3, D2, D1, D0)
	// TL (bit 4) is always 1 for a standard Saturn pad
	var input uint8
	thtr := pdr & 0x60
	switch thtr {
	case 0x60: // TH=1, TR=1
		input = 0x10 | (b2>>3)&0x08 | 0x04 // TL=1, D3=L, D2=1, D1=0, D0=0
	case 0x40: // TH=1, TR=0
		input = 0x10 | (b1 & 0x0F) // TL=1, D3=Start, D2=A, D1=C, D0=B
	case 0x20: // TH=0, TR=1
		input = 0x10 | (b1>>4)&0x0F // TL=1, D3=Right, D2=Left, D1=Down, D0=Up
	case 0x00: // TH=0, TR=0
		input = 0x10 | (b2>>4)&0x0F // TL=1, D3=R, D2=X, D1=Y, D0=Z
	}

	// Combine: output pins from written PDR, input pins from controller
	result := (pdr & ddr) | (input & ^ddr)
	return result & 0x7F
}

// Write stores a value to the SMPC register at the given byte offset.
// Even offsets are ignored. Read-only registers are ignored.
func (s *SMPC) Write(offset uint8, val uint8) {
	if offset&1 == 0 {
		return
	}

	switch {
	case offset >= 0x01 && offset <= 0x0D:
		s.ireg[(offset-0x01)/2] = val
		// IREG0 write triggers INTBACK continue/break
		if offset == 0x01 && s.intbackActive {
			s.continueINTBACK()
		}
	case offset == 0x1F:
		s.comreg = val
		// Defer command dispatch. Real SMPC takes microseconds
		// (or tens of milliseconds for SYSRES/CKCHG/INTBACK) to
		// process a command; during that window SF stays 1 (busy).
		// TickScanline drains the counter and runs dispatch.
		s.cmdDelay = s.cmdScanlines()
	case offset == 0x63:
		s.sf = val & 0x01
	case offset == 0x75:
		s.pdr1 = val & 0x7F
	case offset == 0x77:
		s.pdr2 = val & 0x7F
	case offset == 0x79:
		s.ddr1 = val & 0x7F
	case offset == 0x7B:
		s.ddr2 = val & 0x7F
	case offset == 0x7D:
		s.iosel = val & 0x03
	case offset == 0x7F:
		s.exle = val & 0x03
	}
}

// dispatch executes the command stored in COMREG.
func (s *SMPC) dispatch() {
	cmd := s.comreg
	s.oreg[31] = cmd
	switch cmd {
	// Type A
	case 0x18:
		s.cmdNMIREQ()
		s.masterNMI()
		s.sf = 0
	case 0x0D:
		s.cmdSYSRES()
		s.systemReset()
		s.sf = 0
	case 0x0E:
		s.cmdCKCHG352()
		s.systemReset()
		s.masterNMI()
		s.sf = 0
	case 0x0F:
		s.cmdCKCHG320()
		s.systemReset()
		s.masterNMI()
		s.sf = 0

	// Type B
	case 0x00:
		s.cmdMSHON()
		s.sf = 0
	case 0x02:
		s.cmdSSHON()
		s.sf = 0
	case 0x03:
		s.cmdSSHOFF()
		s.sf = 0
	case 0x06:
		s.cmdSNDON()
		s.sf = 0
	case 0x07:
		s.cmdSNDOFF()
		s.sf = 0
	case 0x08:
		s.cmdCDON()
		s.sf = 0
	case 0x09:
		s.cmdCDOFF()
		s.sf = 0
	case 0x19:
		s.cmdRESENAB()
		s.sf = 0
	case 0x1A:
		s.cmdRESDISA()
		s.sf = 0

	// Type C
	case 0x16:
		s.cmdSETTIME()
		s.sf = 0
	case 0x17:
		s.cmdSETSMEM()
		s.sf = 0

	// Type D
	case 0x10:
		pen := s.ireg[1]&0x08 != 0
		sysData := s.ireg[0] == 0x01
		s.cmdINTBACK()
		if sysData || pen {
			s.scu.RaiseSystemManager()
		}
		s.sf = 0

	default:
		s.sf = 0
	}

}

// SSHEnabled returns whether the slave SH-2 is enabled.
func (s *SMPC) SSHEnabled() bool { return s.sshEnabled }

// SoundEnabled returns whether the sound CPU is enabled.
func (s *SMPC) SoundEnabled() bool { return s.soundEnabled }

// Type B command implementations.

func (s *SMPC) cmdMSHON() {} // 0x00 - Master SH-2 ON (no-op, always running)

// SSHON releases the slave from reset. On real hardware this is a pulse
// on the reset line, so the slave re-enters its power-on boot sequence
// each time: PC/SP are reloaded from the vector table, registers cleared,
// and on-chip peripherals reset. Games rely on this when re-installing
// the slave entry pointer at $06000250 with SSHOFF/SSHON cycles.
func (s *SMPC) cmdSSHON() {
	s.sshEnabled = true
	s.slaveReset()
}

func (s *SMPC) cmdSSHOFF()  { s.sshEnabled = false }
func (s *SMPC) cmdSNDON()   { s.soundEnabled = true }
func (s *SMPC) cmdSNDOFF()  { s.soundEnabled = false }
func (s *SMPC) cmdCDON()    { s.cdEnabled = true }
func (s *SMPC) cmdCDOFF()   { s.cdEnabled = false }
func (s *SMPC) cmdRESENAB() { s.resetEnabled = true }
func (s *SMPC) cmdRESDISA() { s.resetEnabled = false }

// Type A command implementations.

// cmdSYSRES resets CPU and sound subsystem flags. Dot clock and CD
// block state are preserved - only CDON/CDOFF affect CD enabled.
func (s *SMPC) cmdSYSRES() {
	s.sshEnabled = false
	s.soundEnabled = false
}

// cmdCKCHG352 changes dot clock to 352 mode and resets subsystem flags.
// CD block state is NOT changed - only CDON/CDOFF affect it.
func (s *SMPC) cmdCKCHG352() {
	s.dotsel = true
	s.sshEnabled = false
	s.soundEnabled = false
}

// cmdCKCHG320 changes dot clock to 320 mode and resets subsystem flags.
// CD block state is NOT changed - only CDON/CDOFF affect it.
func (s *SMPC) cmdCKCHG320() {
	s.dotsel = false
	s.sshEnabled = false
	s.soundEnabled = false
}

// Type C command implementations.

// cmdSETTIME copies IREG0-6 into RTC storage and resets the base
// so future ticks advance from the newly set time.
func (s *SMPC) cmdSETTIME() {
	copy(s.rtc[:], s.ireg[:7])
	s.rtcBaseTime = s.rtcToTime()
	s.rtcFrames = 0
}

// rtcToTime decodes the 7-byte BCD RTC into a time.Time.
func (s *SMPC) rtcToTime() time.Time {
	fromBCD := func(v uint8) int { return int(v>>4)*10 + int(v&0x0F) }
	year := fromBCD(s.rtc[0])*100 + fromBCD(s.rtc[1])
	month := time.Month(s.rtc[2] & 0x0F)
	day := fromBCD(s.rtc[3])
	hour := fromBCD(s.rtc[4])
	min := fromBCD(s.rtc[5])
	sec := fromBCD(s.rtc[6])
	return time.Date(year, month, day, hour, min, sec, 0, time.Local)
}

// cmdSETSMEM copies IREG0-3 into SMPC memory storage.
func (s *SMPC) cmdSETSMEM() {
	copy(s.smem[:], s.ireg[:4])
}

// cmdINTBACK populates OREGs with system data and/or peripheral data.
func (s *SMPC) cmdINTBACK() {
	pen := s.ireg[1]&0x08 != 0

	if s.ireg[0] == 0x01 {
		// OREG0: status flags
		var oreg0 uint8
		oreg0 |= 0x80 // STE bit 7: RTC always initialized from host time
		if !s.resetEnabled {
			oreg0 |= 0x40 // RESD bit 6 (1 = reset disabled)
		}
		s.oreg[0] = oreg0

		// OREG1-7: RTC data (refresh from host clock)
		s.refreshRTC()
		copy(s.oreg[1:8], s.rtc[:])

		// OREG8: Cartridge code (no cartridge = 0)
		s.oreg[8] = 0

		// OREG9: Area code
		s.oreg[9] = s.areaCode

		// OREG10: System Status 1
		// b6=DOTSEL, b5=1B, b4=1B, b2=1B (always on)
		// b3=MSHNMI (0), b1=SYSRES (0), b0=SNDRES (1 when sound off)
		var ss1 uint8
		if s.dotsel {
			ss1 |= 0x40
		}
		ss1 |= 0x20 | 0x10 | 0x04 // fixed 1B signals
		if !s.soundEnabled {
			ss1 |= 0x01 // SNDRES active (sound CPU in reset)
		}
		s.oreg[10] = ss1

		// OREG11: System Status 2
		// b6=CDRES (1 when CD block off)
		var ss2 uint8
		if !s.cdEnabled {
			ss2 |= 0x40
		}
		s.oreg[11] = ss2

		// OREG12-15: SMEM
		copy(s.oreg[12:16], s.smem[:])

		if pen {
			// Peripheral data follows after system data
			s.sr = 0x60 // bit6=1(fixed), PDE=1
			s.intbackActive = true
			s.intbackP1MD = (s.ireg[1] >> 4) & 0x03
			s.intbackP2MD = (s.ireg[1] >> 6) & 0x03
		} else {
			s.sr = 0x40 // bit6=1(fixed), PDE=0
		}
	} else if pen {
		// Peripheral-only mode (IREG0=0x00, PEN=1)
		s.intbackP1MD = (s.ireg[1] >> 4) & 0x03
		s.intbackP2MD = (s.ireg[1] >> 6) & 0x03
		s.collectPeripheralData()
	}
	// IREG0=0x00 + PEN=0: no-op
}

// SetPadData sets the active-low button data for the given port (0 or 1).
// The upper 8 bits are button data byte 1 (directions + Start/A/C/B),
// the lower 8 bits are button data byte 2 (R/X/Y/Z/L + unused).
// Buttons are active-low: 0 = pressed, 1 = not pressed.
func (s *SMPC) SetPadData(port int, data uint16) {
	if port >= 0 && port < 2 {
		s.padState[port] = data
	}
}

// collectPeripheralData populates OREGs with Saturn digital pad data.
// For each port not in 0-byte mode, writes:
//   - Port status (0xF1: direct connect, 1 connector)
//   - Peripheral ID (0x02: digital device, 2 data bytes)
//   - Button data byte 1 (upper 8 bits of padState)
//   - Button data byte 2 (lower 8 bits of padState)
func (s *SMPC) collectPeripheralData() {
	idx := 0

	// Port 1 data (omitted if 0-byte mode)
	if s.intbackP1MD != 3 {
		s.oreg[idx] = 0xF1 // multitap=F, connectors=1
		s.oreg[idx+1] = 0x02
		s.oreg[idx+2] = uint8(s.padState[0] >> 8)
		s.oreg[idx+3] = uint8(s.padState[0])
		idx += 4
	}

	// Port 2 data (omitted if 0-byte mode)
	if s.intbackP2MD != 3 {
		s.oreg[idx] = 0xF1 // multitap=F, connectors=1
		s.oreg[idx+1] = 0x02
		s.oreg[idx+2] = uint8(s.padState[1] >> 8)
		s.oreg[idx+3] = uint8(s.padState[1])
	}

	// Peripheral data SR format (p66)
	var sr uint8
	sr |= 0x80                        // bit7=1 (fixed)
	sr |= 0x40                        // PDL=1 (first peripheral data)
	sr |= (s.intbackP2MD & 0x03) << 2 // P2MD echoed
	sr |= s.intbackP1MD & 0x03        // P1MD echoed
	s.sr = sr

	// All data fits in one response, command complete
	s.intbackActive = false
}

// continueINTBACK handles IREG0 continue/break signaling during INTBACK.
// Triggered by IREG0 write, not SF write.
func (s *SMPC) continueINTBACK() {
	if s.ireg[0]&0x40 != 0 {
		// Bit 6: break - terminate INTBACK
		s.intbackActive = false
		s.sf = 0
		return
	}

	if s.ireg[0]&0x80 != 0 {
		// Bit 7: continue - collect peripheral data
		s.collectPeripheralData()
		s.scu.RaiseSystemManager()
		s.sf = 0
		return
	}
}

func (s *SMPC) cmdNMIREQ() {} // 0x18 - NMI Request
