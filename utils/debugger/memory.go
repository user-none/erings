// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/user-none/erings/internal/debugserver/responses"
	"github.com/user-none/erings/utils/debugger/client"
	"github.com/user-none/erings/utils/debugger/ui"
)

// memPollTicks is the memory window refresh cadence in 60Hz ticks
// (about 10Hz).
const memPollTicks = 6

// memRows is the number of visible hex rows. The panel reads
// memRows * 16 bytes per refresh.
const memRows = 16

// memWheelRows is how many rows one wheel notch scrolls.
const memWheelRows = 3

// memory is the memory panel state. The regions come from the server
// at connect time; curRegion indexes into them and memOffset is the
// byte offset of the visible window inside that region, 16-aligned.
type memory struct {
	regions   []responses.RegionInfo
	curRegion int
	memOffset uint32

	// wheelAccum collects fractional wheel deltas (trackpads report
	// swipes as many sub-notch events) until they amount to whole rows.
	wheelAccum float64

	hexView      *ui.HexView
	regionRow    *widget.Container
	gotoInput    *widget.TextInput
	readInFlight bool
}

// buildMemoryPanel creates the memory panel: a controls row (region
// buttons, goto entry) above the hex view.
func (a *app) buildMemoryPanel() *widget.Container {
	panel := widget.NewContainer(
		widget.ContainerOpts.BackgroundImage(image.NewNineSliceColor(ui.Surface)),
		widget.ContainerOpts.Layout(widget.NewRowLayout(
			widget.RowLayoutOpts.Direction(widget.DirectionVertical),
			widget.RowLayoutOpts.Spacing(ui.Px(6)),
			widget.RowLayoutOpts.Padding(widget.NewInsetsSimple(ui.Px(6))),
		)),
	)

	a.mem.regionRow = widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewRowLayout(
			widget.RowLayoutOpts.Direction(widget.DirectionHorizontal),
			widget.RowLayoutOpts.Spacing(ui.Px(4)),
		)),
	)
	a.buildRegionButtons()

	a.mem.gotoInput = ui.TextInput("address", 140,
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			a.gotoAddress()
		}),
	)
	a.inputs.Add(a.mem.gotoInput)

	controls := ui.HRow(ui.Px(6),
		a.mem.regionRow,
		a.mem.gotoInput,
		ui.Button("Goto", func(args *widget.ButtonClickedEventArgs) {
			a.gotoAddress()
		}),
	)
	panel.AddChild(controls)

	a.mem.hexView = ui.NewHexView(memRows)
	a.mem.hexView.GetWidget().ScrolledEvent.AddHandler(func(args interface{}) {
		ev, ok := args.(*widget.WidgetScrolledEventArgs)
		if !ok {
			return
		}
		a.scrollMemory(ev.Y)
	})
	panel.AddChild(a.mem.hexView)

	return panel
}

// buildRegionButtons fills the region selector row. It runs at panel
// build and again when the regions response arrives.
func (a *app) buildRegionButtons() {
	if a.mem.regionRow == nil {
		return
	}
	a.mem.regionRow.RemoveChildren()
	for i, r := range a.mem.regions {
		idx := i
		label := r.Name
		if i == a.mem.curRegion {
			label = "[" + label + "]"
		}
		a.mem.regionRow.AddChild(ui.Button(label, func(args *widget.ButtonClickedEventArgs) {
			if a.mem.curRegion != idx {
				a.mem.curRegion = idx
				a.mem.memOffset = 0
				a.mem.hexView.ClearSelection()
				a.buildRegionButtons()
			}
		}))
	}
}

// fetchRegions asks the server for its region table. It runs once per
// connect as part of refresh.
func (a *app) fetchRegions() {
	a.send("regions", func(r client.Response) {
		if r.Err != nil {
			a.logf("regions: error: %s", r.Err)
			return
		}
		var rl responses.RegionList
		if json.Unmarshal(r.Data, &rl) != nil {
			return
		}
		a.mem.regions = rl.Regions
		a.mem.curRegion = 0
		a.mem.memOffset = 0
		a.buildRegionButtons()
	})
}

// memWindow returns the current window's absolute address and length,
// clamped to the region end.
func (a *app) memWindow() (uint32, uint32, bool) {
	if a.mem.curRegion >= len(a.mem.regions) {
		return 0, 0, false
	}
	r := a.mem.regions[a.mem.curRegion]
	length := uint32(memRows * 16)
	if remaining := r.Size - a.mem.memOffset; length > remaining {
		length = remaining
	}
	return r.Start + a.mem.memOffset, length, true
}

// pollMemory refreshes the visible window on its cadence.
func (a *app) pollMemory() {
	if a.mem.readInFlight || a.tick%memPollTicks != 0 {
		return
	}
	addr, length, ok := a.memWindow()
	if !ok || length == 0 {
		return
	}
	a.mem.readInFlight = true
	a.send(fmt.Sprintf("read 0x%08X %d", addr, length), func(r client.Response) {
		a.mem.readInFlight = false
		if r.Err != nil {
			return
		}
		var rr responses.ReadResult
		if json.Unmarshal(r.Data, &rr) != nil {
			return
		}
		data, err := hex.DecodeString(rr.Data)
		if err != nil {
			return
		}
		if a.mem.hexView != nil {
			a.mem.hexView.SetData(rr.Addr, data)
		}
	})
}

// scrollMemory moves the window by a wheel delta (memWheelRows rows
// per notch), staying 16-aligned and inside the region. Deltas
// accumulate until they amount to a whole row, so a slow trackpad
// swipe moves one row at a time instead of truncating to nothing.
func (a *app) scrollMemory(delta float64) {
	if a.mem.curRegion >= len(a.mem.regions) {
		return
	}
	a.mem.wheelAccum += delta * memWheelRows
	rows := int(a.mem.wheelAccum)
	if rows == 0 {
		return
	}
	a.mem.wheelAccum -= float64(rows)

	r := a.mem.regions[a.mem.curRegion]
	off := int64(a.mem.memOffset) - int64(rows)*16
	maxOff := int64(r.Size) - int64(memRows*16)
	if maxOff < 0 {
		maxOff = 0
	}
	if off < 0 {
		off = 0
	} else if off > maxOff {
		off = maxOff
	}
	a.mem.memOffset = uint32(off)
}

// gotoAddress jumps the window to the entered address. The address may
// be hex with 0x or decimal, and must fall inside a known region after
// masking the SH-2 partition bits, the same normalization the server
// applies.
func (a *app) gotoAddress() {
	s := strings.TrimSpace(a.mem.gotoInput.GetText())
	if s == "" {
		return
	}
	var v uint64
	var err error
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err = strconv.ParseUint(s[2:], 16, 32)
	} else {
		v, err = strconv.ParseUint(s, 10, 32)
	}
	if err != nil {
		a.logf("goto: invalid address %q", s)
		return
	}
	if !a.jumpTo(uint32(v)) {
		a.logf("goto: 0x%08X is outside known regions", uint32(v))
	}
}

// jumpTo scrolls the memory view to addr, resolving it the same way
// the server does: mask the SH-2 partition bits, then fold mirror
// spellings onto the canonical range. It reports whether the address
// landed in a known region.
func (a *app) jumpTo(addr uint32) bool {
	physical := addr & 0x1FFFFFFF
	for i, r := range a.mem.regions {
		if physical < r.Start || physical >= r.Start+r.Window {
			continue
		}
		off := ((physical - r.Start) & (r.Size - 1)) &^ 0xF
		maxOff := uint32(0)
		if r.Size > memRows*16 {
			maxOff = r.Size - memRows*16
		}
		if off > maxOff {
			off = maxOff
		}
		a.mem.curRegion = i
		a.mem.memOffset = off
		if a.mem.hexView != nil {
			a.mem.hexView.ClearSelection()
		}
		a.buildRegionButtons()
		return true
	}
	return false
}
