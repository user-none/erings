// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

// lerpGouraud linearly interpolates a packed Gouraud entry between start
// and end at position i out of n-1 steps. Returns a packed 15-bit value.
func lerpGouraud(start, end uint16, i, n int) uint16 {
	if n <= 1 {
		return start
	}
	sr := int(start & 0x1F)
	sg := int((start >> 5) & 0x1F)
	sb := int((start >> 10) & 0x1F)
	er := int(end & 0x1F)
	eg := int((end >> 5) & 0x1F)
	eb := int((end >> 10) & 0x1F)
	r := sr + (er-sr)*i/(n-1)
	g := sg + (eg-sg)*i/(n-1)
	b := sb + (eb-sb)*i/(n-1)
	return uint16(b)<<10 | uint16(g)<<5 | uint16(r)
}

// gouraudStepper provides fixed-point interpolation of packed Gouraud
// values, replacing per-pixel division with per-pixel addition.
type gouraudStepper struct {
	r, g, b    int // current values (fixed-point 16.16)
	dr, dg, db int // delta per step
}

func initGouraudStepper(start, end uint16, n int) gouraudStepper {
	sr := int(start & 0x1F)
	sg := int((start >> 5) & 0x1F)
	sb := int((start >> 10) & 0x1F)
	er := int(end & 0x1F)
	eg := int((end >> 5) & 0x1F)
	eb := int((end >> 10) & 0x1F)
	if n <= 1 {
		return gouraudStepper{r: sr << 16, g: sg << 16, b: sb << 16}
	}
	return gouraudStepper{
		r: sr << 16, g: sg << 16, b: sb << 16,
		dr: ((er - sr) << 16) / (n - 1),
		dg: ((eg - sg) << 16) / (n - 1),
		db: ((eb - sb) << 16) / (n - 1),
	}
}

func (gs *gouraudStepper) value() uint16 {
	r := gs.r >> 16
	g := gs.g >> 16
	b := gs.b >> 16
	if r < 0 {
		r = 0
	} else if r > 0x1F {
		r = 0x1F
	}
	if g < 0 {
		g = 0
	} else if g > 0x1F {
		g = 0x1F
	}
	if b < 0 {
		b = 0
	} else if b > 0x1F {
		b = 0x1F
	}
	return uint16(b)<<10 | uint16(g)<<5 | uint16(r)
}

func (gs *gouraudStepper) step() {
	gs.r += gs.dr
	gs.g += gs.dg
	gs.b += gs.db
}

// advance applies n steps at once. The stepper is linear, so this is
// exactly equivalent to calling step() n times (clamping happens only
// in value()). Used to fast-forward past clipped-off line pixels.
func (gs *gouraudStepper) advance(n int) {
	gs.r += gs.dr * n
	gs.g += gs.dg * n
	gs.b += gs.db * n
}
