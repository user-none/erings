// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"strconv"
	"strings"
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

// padAssignment maps each player to an index into the connected
// gamepad list, or -1 for no pad. Keyboard input is always player 1
// and is not part of the assignment.
type padAssignment [maxPlayers]int

func parsePadFlag(name, value string) (int, error) {
	if value == "none" {
		return -1, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("-%s must be a gamepad index or \"none\", got %q", name, value)
	}
	return n, nil
}

// resolvePadAssignment turns the -p1-pad/-p2-pad flag values into a
// pad assignment. An explicitly passed index that collides with the
// other player's defaulted index steals the pad and the defaulted
// player gets none. Two explicit flags naming the same index is an
// error.
func resolvePadAssignment(p1, p2 string, p1Set, p2Set bool) (padAssignment, error) {
	var pa padAssignment
	var err error
	pa[0], err = parsePadFlag("p1-pad", p1)
	if err != nil {
		return pa, err
	}
	pa[1], err = parsePadFlag("p2-pad", p2)
	if err != nil {
		return pa, err
	}
	if pa[0] >= 0 && pa[0] == pa[1] {
		switch {
		case p1Set && p2Set:
			return pa, fmt.Errorf("-p1-pad and -p2-pad both name gamepad %d", pa[0])
		case p2Set:
			pa[0] = -1
		default:
			pa[1] = -1
		}
	}
	return pa, nil
}

// padForPlayer resolves a player's assigned gamepad index against the
// currently connected pads. Indexes are re-resolved every poll so
// hotplugged pads are picked up.
func padForPlayer(pa padAssignment, gamepadIDs []ebiten.GamepadID, player int) (ebiten.GamepadID, bool) {
	if player < 0 || player >= maxPlayers {
		return 0, false
	}
	idx := pa[player]
	if idx < 0 || idx >= len(gamepadIDs) {
		return 0, false
	}
	return gamepadIDs[idx], true
}

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
	g.logPadAssignment(gamepadIDs)

	for player := 0; player < maxPlayers; player++ {
		var buttons uint32
		if player == 0 {
			for bitID, key := range g.keyMap {
				if ebiten.IsKeyPressed(key) {
					buttons |= 1 << uint(bitID)
				}
			}
		}
		if id, ok := padForPlayer(g.padAssign, gamepadIDs, player); ok {
			for bitID, padBtn := range g.padMap {
				if ebiten.IsStandardGamepadButtonPressed(id, padBtn) {
					buttons |= 1 << uint(bitID)
				}
			}
			pollAnalogStick(&buttons, g.padMap, id)
		}
		g.sharedInput.set(player, buttons)
	}
}

// logPadAssignment prints the resolved pad-to-player mapping whenever
// it changes (startup, hotplug, disconnect).
func (g *game) logPadAssignment(gamepadIDs []ebiten.GamepadID) {
	var b strings.Builder
	for player := 0; player < maxPlayers; player++ {
		if id, ok := padForPlayer(g.padAssign, gamepadIDs, player); ok {
			fmt.Fprintf(&b, "[INPUT] pad %d %q -> player %d\n", g.padAssign[player], ebiten.GamepadName(id), player+1)
		}
	}
	if b.String() == g.lastPadLog {
		return
	}
	g.lastPadLog = b.String()
	if b.Len() > 0 {
		fmt.Print(b.String())
	} else {
		fmt.Println("[INPUT] no gamepads assigned")
	}
}
