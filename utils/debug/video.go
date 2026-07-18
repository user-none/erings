// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
)

// Maximum framebuffer dimensions for buffer allocation.
const (
	maxFBWidth  = 704
	maxFBHeight = 512
)

type sharedFramebuffer struct {
	mu           sync.Mutex
	writePixels  []byte
	readPixels   []byte
	stride       int
	activeHeight int
}

func newSharedFramebuffer(width, height int) *sharedFramebuffer {
	size := width * height * 4
	return &sharedFramebuffer{
		writePixels: make([]byte, size),
		readPixels:  make([]byte, size),
	}
}

func (sf *sharedFramebuffer) update(pixels []byte, stride, activeHeight int) {
	sf.mu.Lock()
	n := stride * activeHeight
	if n > len(sf.writePixels) {
		n = len(sf.writePixels)
	}
	if n > len(pixels) {
		n = len(pixels)
	}
	copy(sf.writePixels[:n], pixels[:n])
	sf.stride = stride
	sf.activeHeight = activeHeight
	sf.mu.Unlock()
}

func (sf *sharedFramebuffer) read() (pixels []byte, stride, activeHeight int) {
	sf.mu.Lock()
	stride = sf.stride
	activeHeight = sf.activeHeight
	n := stride * activeHeight
	if n > len(sf.writePixels) {
		n = len(sf.writePixels)
	}
	if n > 0 {
		copy(sf.readPixels[:n], sf.writePixels[:n])
	}
	pixels = sf.readPixels
	sf.mu.Unlock()
	return
}

func (g *game) Draw(screen *ebiten.Image) {
	pixels, stride, activeHeight := g.sharedFB.read()
	if activeHeight == 0 || stride == 0 {
		return
	}

	requiredLen := stride * activeHeight
	if len(pixels) < requiredLen {
		return
	}

	pixelWidth := stride / 4
	if g.offscreen == nil || g.offscreen.Bounds().Dx() != pixelWidth || g.offscreen.Bounds().Dy() != activeHeight {
		g.offscreen = ebiten.NewImage(pixelWidth, activeHeight)
	}
	g.offscreen.WritePixels(pixels[:requiredLen])

	screenW, screenH := screen.Bounds().Dx(), screen.Bounds().Dy()
	ratio := 4.0 / 3.0
	displayW := float64(screenW)
	displayH := displayW / ratio
	if displayH > float64(screenH) {
		displayH = float64(screenH)
		displayW = displayH * ratio
	}

	scaleX := displayW / float64(pixelWidth)
	scaleY := displayH / float64(activeHeight)
	scaledW := float64(pixelWidth) * scaleX
	scaledH := float64(activeHeight) * scaleY
	offsetX := (float64(screenW) - scaledW) / 2
	offsetY := (float64(screenH) - scaledH) / 2

	opts := ebiten.DrawImageOptions{}
	opts.GeoM.Scale(scaleX, scaleY)
	opts.GeoM.Translate(offsetX, offsetY)
	opts.Filter = ebiten.FilterNearest
	screen.DrawImage(g.offscreen, &opts)
}
