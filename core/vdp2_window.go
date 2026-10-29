// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"github.com/user-none/erings/core/compute"
	"github.com/user-none/erings/core/compute/screen"
)

// signExtend9 sign-extends a 9-bit value (from a 16-bit register) to int.
func signExtend9(val uint16) int {
	v := int(val & 0x01FF)
	if v&0x100 != 0 {
		v |= ^0x1FF // sign extend
	}
	return v
}

// lineWindows is the row's window state built by buildLineWindows:
// the runs W0 and W1 split the row into and the control bytes of the
// six layer windows and the color calculation window.
type lineWindows struct {
	runs screen.WindowRuns
	ctl  [7]uint8 // 0-3 NBG0-3, 4 RBG0, 5 sprite, 6 color calculation
}

// winCtlCC indexes the color calculation window's control byte in
// lineWindows.ctl.
const winCtlCC = 6

// windowParamsAt gathers the window registers and display state from
// the line snapshot for the render window functions, at field parity
// field (siblingLineSetup), which under double-density interlace
// places field line y at display line 2y + field.
func (v *VDP2) windowParamsAt(field int) screen.WindowParams {
	regs := &v.frame.regs
	p := screen.WindowParams{
		WPSX:  [2]uint32{uint32(regs[vdp2WPSX0]), uint32(regs[vdp2WPSX1])},
		WPSY:  [2]uint32{uint32(regs[vdp2WPSY0]), uint32(regs[vdp2WPSY1])},
		WPEX:  [2]uint32{uint32(regs[vdp2WPEX0]), uint32(regs[vdp2WPEX1])},
		WPEY:  [2]uint32{uint32(regs[vdp2WPEY0]), uint32(regs[vdp2WPEY1])},
		LWTAU: [2]uint32{uint32(regs[vdp2LWTA0U]), uint32(regs[vdp2LWTA1U])},
		LWTAL: [2]uint32{uint32(regs[vdp2LWTA0L]), uint32(regs[vdp2LWTA1L])},
		Field: uint32(field),
		HiRes: v.frame.hiRes,
		LSMD3: v.frame.lsmd3,
		Intl2: v.frame.effIntl == 2,
	}
	return p
}

// buildLineWindows latches the layer control bytes for row y into lw,
// resolves W0 and W1 when some control byte enables them at the row's
// display line for field parity field, and splits the row into
// runs at their edges. Within a run every layer's window result is
// constant apart from the sprite window term, which the composite
// joins per pixel.
func (v *VDP2) buildLineWindows(lw *lineWindows, y, field int) {
	regs := &v.frame.regs
	lw.ctl[0] = uint8(regs[vdp2WCTLA])
	lw.ctl[1] = uint8(regs[vdp2WCTLA] >> 8)
	lw.ctl[2] = uint8(regs[vdp2WCTLB])
	lw.ctl[3] = uint8(regs[vdp2WCTLB] >> 8)
	lw.ctl[4] = uint8(regs[vdp2WCTLC])
	lw.ctl[5] = uint8(regs[vdp2WCTLC] >> 8)
	lw.ctl[winCtlCC] = uint8(regs[vdp2WCTLD] >> 8)
	var en uint8
	for i := range lw.ctl {
		en |= lw.ctl[i]
	}

	var w0, w1 screen.LineWindow
	if en&0x0A != 0 {
		p := v.windowParamsAt(field)
		vram := (*compute.VDP2VRAM)(v.vram)
		if en&0x02 != 0 {
			w0 = screen.ResolveLineWindow(vram, p, int32(y), 0)
		}
		if en&0x08 != 0 {
			w1 = screen.ResolveLineWindow(vram, p, int32(y), 1)
		}
	}
	screen.BuildWindowRuns(w0, w1, int32(v.frame.width), &lw.runs)
}
