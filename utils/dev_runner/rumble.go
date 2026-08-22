// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/user-none/eblitui/rumble"
	"github.com/user-none/erings/core"
)

// loadRumbleEngine loads a rumble file and binds it to the emulator's
// memory layout. A file that fails to parse or bind is a startup
// error.
func loadRumbleEngine(path string, emu *core.Emulator) (*rumble.Engine, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rs, err := rumble.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	sys := rumble.System{BigEndian: true}
	for _, r := range emu.Regions() {
		sys.Regions = append(sys.Regions, rumble.Region{Name: r.Name, Start: r.Start, Size: r.Size})
	}
	eng, err := rumble.NewEngine(rs, sys, maxPlayers)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return eng, nil
}

// sharedMotors hands the engine's per-frame motor states from the
// emulation goroutine to the ebiten goroutine, which owns gamepad
// vibration.
type sharedMotors struct {
	mu     sync.Mutex
	states []rumble.MotorState
}

func (sm *sharedMotors) set(states []rumble.MotorState) {
	sm.mu.Lock()
	sm.states = states
	sm.mu.Unlock()
}

func (sm *sharedMotors) read() []rumble.MotorState {
	sm.mu.Lock()
	s := sm.states
	sm.mu.Unlock()
	return s
}

// motorRefresh is how long each per-frame motor level plays. It
// outlasts the frame gap so held levels stay seamless, and bounds the
// tail after the engine goes silent for a player.
const motorRefresh = 50 * time.Millisecond

// fireMotorStates applies the engine's motor levels to each player's
// assigned gamepad, as authored with no scaling. active is the set of
// players vibrating from the previous frame; players absent from
// states are stopped. Returns the new active set. Must run on the
// ebiten goroutine.
//
// The start and stop of each player's vibration is logged. Levels are
// re-sent every frame, so only the transitions are reported, not the
// per-frame level of a vibration already running.
func fireMotorStates(states []rumble.MotorState, active map[int]bool, pa padAssignment) map[int]bool {
	gamepadIDs := ebiten.AppendGamepadIDs(nil)
	next := make(map[int]bool)
	for _, ms := range states {
		if !active[ms.Player] {
			fmt.Printf("[RUMBLE] player %d start strong=%.2f weak=%.2f\n", ms.Player, ms.Strong, ms.Weak)
		}
		// The player is tracked as vibrating even with no gamepad
		// assigned, so an unplugged player still reports its rumbles.
		next[ms.Player] = true
		id, ok := padForPlayer(pa, gamepadIDs, ms.Player-1)
		if !ok {
			continue
		}
		ebiten.VibrateGamepad(id, &ebiten.VibrateGamepadOptions{
			Duration:        motorRefresh,
			StrongMagnitude: ms.Strong,
			WeakMagnitude:   ms.Weak,
		})
	}
	for p := range active {
		if next[p] {
			continue
		}
		fmt.Printf("[RUMBLE] player %d stop\n", p)
		id, ok := padForPlayer(pa, gamepadIDs, p-1)
		if !ok {
			continue
		}
		ebiten.VibrateGamepad(id, &ebiten.VibrateGamepadOptions{
			Duration: time.Millisecond,
		})
	}
	return next
}
