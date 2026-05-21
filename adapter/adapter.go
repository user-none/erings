// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package adapter exposes the erings Saturn core through the eblitui
// coreif interfaces so it can run under the eblitui desktop UI.
package adapter

import (
	"strconv"
	"strings"

	"github.com/user-none/eblitui/coreif"
	"github.com/user-none/erings"
	"github.com/user-none/erings/core"
)

// Saturn input bit layout, matching core.Emulator.SetInput: d-pad on
// bits 0-3, then A..Start on bits 4-12.
const (
	btnUp = iota
	btnDown
	btnLeft
	btnRight
	btnA
	btnB
	btnC
	btnX
	btnY
	btnZ
	btnL
	btnR
	btnStart
)

func saturnButtons() []coreif.Button {
	return []coreif.Button{
		{Name: "A", ID: btnA, DefaultKey: "J", DefaultPad: "X"},
		{Name: "B", ID: btnB, DefaultKey: "K", DefaultPad: "A"},
		{Name: "C", ID: btnC, DefaultKey: "L", DefaultPad: "B"},
		{Name: "X", ID: btnX, DefaultKey: "U", DefaultPad: "L1"},
		{Name: "Y", ID: btnY, DefaultKey: "I", DefaultPad: "Y"},
		{Name: "Z", ID: btnZ, DefaultKey: "O", DefaultPad: "R1"},
		{Name: "L", ID: btnL, DefaultKey: "N", DefaultPad: "LeftTrigger"},
		{Name: "R", ID: btnR, DefaultKey: "M", DefaultPad: "RightTrigger"},
		{Name: "Start", ID: btnStart, DefaultKey: "Enter", DefaultPad: "Start"},
	}
}

// Factory creates Saturn emulator instances and provides system metadata.
type Factory struct{}

// SystemInfo returns Saturn system metadata for the UI.
func (f *Factory) SystemInfo() coreif.SystemInfo {
	return coreif.SystemInfo{
		Name:             erings.Name,
		ConsoleName:      "Sega Saturn",
		Extensions:       []string{".chd"},
		ScreenWidth:      704,
		MaxScreenHeight:  512,
		PixelAspectRatio: 1.0,
		SampleRate:       44100,
		Buttons:          saturnButtons(),
		Players:          2,
		Disc:             true,
		DataDirName:      "erings",
		CoreName:         erings.Name,
		CoreVersion:      erings.Version,
		MetadataVariants: []coreif.MetadataVariant{
			{
				Name:          "Sega Saturn",
				RDBName:       "Sega - Saturn",
				ThumbnailRepo: "Sega_-_Saturn",
			},
		},
		BIOSOptions: []coreif.BIOSOption{
			{
				Key:      "main_bios",
				Label:    "System BIOS",
				Required: true,
				Variants: []coreif.BIOSVariant{
					{
						Label:    "USA",
						SHA256:   "96e106f740ab448cf89f0dd49dfbac7fe5391cb6bd6e14ad5e3061c13330266f",
						Filename: "mpr-17933.bin",
					},
					{
						Label:    "Japan",
						SHA256:   "dcfef4b99605f872b6c3b6d05c045385cdea3d1b702906a0ed930df7bcb7deac",
						Filename: "sega_101.bin",
					},
				},
			},
		},
	}
}

// CreateEmulator creates a new Saturn emulator instance. Content is
// provided afterwards via SetDisc.
func (f *Factory) CreateEmulator() coreif.Emulator {
	return &emulator{emu: core.NewEmulator()}
}

// IP header field offsets within the sector-0 user data. Disc Format
// Standards (ST-040): raw 2352-byte sectors place user data at byte 16.
const (
	ipHardwareID  = 0x00 // 16 bytes, "SEGA SEGASATURN "
	ipProductNum  = 0x20 // 10 bytes, space padded
	ipDeviceInfo  = 0x38 // 8 bytes, "CD-n/m"
	ipGameTitle   = 0x60 // 112 bytes, space padded
	ipGameTitleSz = 0x70
)

// DiscInfo implements coreif.DiscIdentifier. It reads the IP header from
// sector 0 and returns disc-derived facts only: the product number
// (shared by every disc of a game), the disc number/total from the
// device-information field, and the game title. It contains no
// external-catalog (RDB) serial logic.
func (f *Factory) DiscInfo(disc coreif.DiscReader) (coreif.DiscInfo, bool) {
	data, err := disc.ReadSector(0)
	if err != nil || len(data) < 16+ipProductNum+10 {
		return coreif.DiscInfo{}, false
	}
	user := data[16:]
	if string(user[ipHardwareID:ipHardwareID+16]) != "SEGA SEGASATURN " {
		return coreif.DiscInfo{}, false
	}

	productNumber := strings.TrimRight(string(user[ipProductNum:ipProductNum+10]), " ")
	if productNumber == "" {
		return coreif.DiscInfo{}, false
	}

	info := coreif.DiscInfo{
		ProductNumber: productNumber,
		DiscNumber:    1,
		DiscTotal:     1,
	}

	if len(user) >= ipDeviceInfo+8 {
		if n, m, ok := parseDeviceInfo(string(user[ipDeviceInfo : ipDeviceInfo+8])); ok {
			info.DiscNumber = n
			info.DiscTotal = m
		}
	}
	if len(user) >= ipGameTitle+ipGameTitleSz {
		info.Title = strings.TrimSpace(string(user[ipGameTitle : ipGameTitle+ipGameTitleSz]))
	}
	return info, true
}

// parseDeviceInfo parses the IP device-information field ("CD-n/m",
// space padded) into a 1-based disc number and total. Returns ok=false
// when the field is absent or malformed; callers default to 1/1.
func parseDeviceInfo(field string) (disc int, total int, ok bool) {
	s := strings.TrimSpace(field)
	if !strings.HasPrefix(s, "CD-") {
		return 0, 0, false
	}
	s = s[len("CD-"):]
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return 0, 0, false
	}
	n, err1 := strconv.Atoi(strings.TrimSpace(s[:slash]))
	m, err2 := strconv.Atoi(strings.TrimSpace(s[slash+1:]))
	if err1 != nil || err2 != nil || n < 1 || m < 1 || n > m {
		return 0, 0, false
	}
	return n, m, true
}

// emulator wraps *core.Emulator to satisfy coreif.Emulator.
type emulator struct {
	emu *core.Emulator
}

func (e *emulator) RunFrame()                           { e.emu.RunFrame() }
func (e *emulator) GetFramebuffer() []byte              { return e.emu.GetFramebuffer() }
func (e *emulator) GetFramebufferStride() int           { return e.emu.GetFramebufferStride() }
func (e *emulator) GetActiveHeight() int                { return e.emu.GetActiveHeight() }
func (e *emulator) GetAudioSamples() []int16            { return e.emu.GetAudioSamples() }
func (e *emulator) SetInput(player int, buttons uint32) { e.emu.SetInput(player, buttons) }
func (e *emulator) SetOption(key string, value string)  {}
func (e *emulator) SetRom(data []byte)                  {} // Saturn is disc-only
func (e *emulator) Start()                              { e.emu.Start() }
func (e *emulator) Close()                              { e.emu.Close() }

func (e *emulator) SetDisc(disc coreif.DiscReader) {
	// coreif.DiscReader's method set is a superset of core.DiscReader
	// (both primitive: ReadSector/NumTracks/Track), so it is passed
	// straight through with no adapter type.
	e.emu.SetDisc(disc)
}

func (e *emulator) SetBIOS(key string, data []byte) error {
	return e.emu.SetBIOS(key, data)
}

func (e *emulator) GetTiming() coreif.Timing {
	t := e.emu.GetTiming()
	return coreif.Timing{FPS: t.FPS, Scanlines: t.Scanlines}
}

// HasSRAM implements coreif.BatterySaver. Every Saturn disc can write
// the 32 KB internal backup RAM and there is no per-disc battery flag,
// so the save area is always present.
func (e *emulator) HasSRAM() bool { return true }

// GetSRAM implements coreif.BatterySaver, returning a copy of the
// internal backup RAM for persistence.
func (e *emulator) GetSRAM() []byte { return e.emu.GetSRAM() }

// SetSRAM implements coreif.BatterySaver, restoring previously
// persisted internal backup RAM. Wrong-size input is ignored by the
// bus.
func (e *emulator) SetSRAM(data []byte) { e.emu.SetSRAM(data) }

// PixelAspectRatio implements coreif.AspectProvider. The Saturn's pixel
// aspect depends on the VDP2 horizontal resolution, so a static
// SystemInfo.PixelAspectRatio cannot be correct across mode switches.
func (e *emulator) PixelAspectRatio() float64 {
	return e.emu.PixelAspectRatio()
}
