// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
)

// Saturn button bit positions (d-pad bits 0-3).
const (
	btnUp    = 0
	btnDown  = 1
	btnLeft  = 2
	btnRight = 3
	btnA     = 4
	btnB     = 5
	btnC     = 6
	btnX     = 7
	btnY     = 8
	btnZ     = 9
	btnL     = 10
	btnR     = 11
	btnStart = 12
)

const maxPlayers = 2

type sharedInput struct {
	mu      sync.Mutex
	buttons [maxPlayers]uint32
}

func (si *sharedInput) set(player int, buttons uint32) {
	if player < 0 || player >= maxPlayers {
		return
	}
	si.mu.Lock()
	si.buttons[player] = buttons
	si.mu.Unlock()
}

func (si *sharedInput) read() [maxPlayers]uint32 {
	si.mu.Lock()
	result := si.buttons
	si.mu.Unlock()
	return result
}

func pollAnalogStick(buttons *uint32, padMap map[int]ebiten.StandardGamepadButton, gamepadID ebiten.GamepadID) {
	axisX := ebiten.StandardGamepadAxisValue(gamepadID, ebiten.StandardGamepadAxisLeftStickHorizontal)
	axisY := ebiten.StandardGamepadAxisValue(gamepadID, ebiten.StandardGamepadAxisLeftStickVertical)

	for bitID, padBtn := range padMap {
		switch padBtn {
		case ebiten.StandardGamepadButtonLeftLeft:
			if axisX < -0.25 {
				*buttons |= 1 << uint(bitID)
			}
		case ebiten.StandardGamepadButtonLeftRight:
			if axisX > 0.25 {
				*buttons |= 1 << uint(bitID)
			}
		case ebiten.StandardGamepadButtonLeftTop:
			if axisY < -0.25 {
				*buttons |= 1 << uint(bitID)
			}
		case ebiten.StandardGamepadButtonLeftBottom:
			if axisY > 0.25 {
				*buttons |= 1 << uint(bitID)
			}
		}
	}
}

func buildKeyMap() map[int]ebiten.Key {
	return map[int]ebiten.Key{
		btnUp:    ebiten.KeyW,
		btnDown:  ebiten.KeyS,
		btnLeft:  ebiten.KeyA,
		btnRight: ebiten.KeyD,
		btnA:     ebiten.KeyJ,
		btnB:     ebiten.KeyK,
		btnC:     ebiten.KeyL,
		btnX:     ebiten.KeyU,
		btnY:     ebiten.KeyI,
		btnZ:     ebiten.KeyO,
		btnL:     ebiten.KeyC,
		btnR:     ebiten.KeyN,
		btnStart: ebiten.KeyEnter,
	}
}

func buildPadMap() map[int]ebiten.StandardGamepadButton {
	return map[int]ebiten.StandardGamepadButton{
		btnUp:    ebiten.StandardGamepadButtonLeftTop,
		btnDown:  ebiten.StandardGamepadButtonLeftBottom,
		btnLeft:  ebiten.StandardGamepadButtonLeftLeft,
		btnRight: ebiten.StandardGamepadButtonLeftRight,
		btnA:     ebiten.StandardGamepadButtonRightBottom,
		btnB:     ebiten.StandardGamepadButtonRightRight,
		btnC:     ebiten.StandardGamepadButtonFrontTopRight,
		btnX:     ebiten.StandardGamepadButtonRightLeft,
		btnY:     ebiten.StandardGamepadButtonRightTop,
		btnZ:     ebiten.StandardGamepadButtonFrontTopLeft,
		btnL:     ebiten.StandardGamepadButtonFrontBottomLeft,
		btnR:     ebiten.StandardGamepadButtonFrontBottomRight,
		btnStart: ebiten.StandardGamepadButtonCenterRight,
	}
}

func (g *game) pollInputToShared() {
	gamepadIDs := ebiten.AppendGamepadIDs(nil)
	hasGamepad := len(gamepadIDs) > 0

	var gamepadID ebiten.GamepadID
	if hasGamepad {
		gamepadID = gamepadIDs[0]
	}

	var buttons uint32
	for bitID, key := range g.keyMap {
		if ebiten.IsKeyPressed(key) {
			buttons |= 1 << uint(bitID)
		}
	}
	if hasGamepad {
		for bitID, padBtn := range g.padMap {
			if ebiten.IsStandardGamepadButtonPressed(gamepadID, padBtn) {
				buttons |= 1 << uint(bitID)
			}
		}
		pollAnalogStick(&buttons, g.padMap, gamepadID)
	}
	g.sharedInput.set(0, buttons)

	if len(gamepadIDs) > 1 {
		var p2buttons uint32
		for bitID, padBtn := range g.padMap {
			if ebiten.IsStandardGamepadButtonPressed(gamepadIDs[1], padBtn) {
				p2buttons |= 1 << uint(bitID)
			}
		}
		pollAnalogStick(&p2buttons, g.padMap, gamepadIDs[1])
		g.sharedInput.set(1, p2buttons)
	}
}
