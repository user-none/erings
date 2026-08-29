// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"
	"time"
)

// TestFenceSpinPath covers the fast path: the target is already
// reached, so Wait returns without parking.
func TestFenceSpinPath(t *testing.T) {
	var f fence
	w := f.NewWaiter()
	f.Store(10)
	w.Wait(10)
	w.Wait(5)
	if got := f.pos.Load(); got != 10 {
		t.Fatalf("pos = %d, want 10", got)
	}
	if w.target.Load() != parkIdle {
		t.Fatal("waiter target not idle after spin-path waits")
	}
}

// TestFenceParkWake forces the park path by delaying the publisher
// well past the spin budget, then checks a below-target store does not
// release the waiter, the crossing store does, and the target returns
// to idle.
func TestFenceParkWake(t *testing.T) {
	var f fence
	w := f.NewWaiter()
	done := make(chan struct{})
	go func() {
		w.Wait(100)
		close(done)
	}()
	// Long enough that the waiter has exhausted parkSpinLimit and parked.
	time.Sleep(20 * time.Millisecond)
	f.Store(50)
	select {
	case <-done:
		t.Fatal("Wait returned before the target was reached")
	case <-time.After(10 * time.Millisecond):
	}
	f.Store(100)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the crossing store")
	}
	if w.target.Load() != parkIdle {
		t.Fatal("waiter target not released after wake")
	}
}

// TestFenceTwoWaiters parks two waiters with different targets and
// checks each is released by its own crossing, in order.
func TestFenceTwoWaiters(t *testing.T) {
	var f fence
	near := f.NewWaiter()
	far := f.NewWaiter()
	doneNear := make(chan struct{})
	doneFar := make(chan struct{})
	go func() {
		near.Wait(10)
		close(doneNear)
	}()
	go func() {
		far.Wait(20)
		close(doneFar)
	}()
	time.Sleep(20 * time.Millisecond)
	f.Store(10)
	select {
	case <-doneNear:
	case <-time.After(5 * time.Second):
		t.Fatal("near waiter did not wake at its target")
	}
	select {
	case <-doneFar:
		t.Fatal("far waiter woke before its target")
	case <-time.After(10 * time.Millisecond):
	}
	f.Store(20)
	select {
	case <-doneFar:
	case <-time.After(5 * time.Second):
		t.Fatal("far waiter did not wake at its target")
	}
}

// TestFenceCrossingWakeNotStolen is the regression test for the
// two-waiter lost-wake deadlock a shared wake channel allowed: a
// high-target waiter is parked and re-checking, a low-target waiter has
// published its target and checked the fence but not yet begun
// receiving, and the publisher makes its crossing store then goes
// quiet. The wake must reach the low waiter's own channel; the high
// waiter has no path to consume it.
func TestFenceCrossingWakeNotStolen(t *testing.T) {
	var f fence
	high := f.NewWaiter()
	low := f.NewWaiter()

	highWoke := make(chan struct{})
	go func() {
		high.Wait(1000)
		close(highWoke)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for high.target.Load() != 1000 {
		if time.Now().After(deadline) {
			t.Fatal("high waiter never parked")
		}
	}
	// Let the high waiter block in its receive.
	time.Sleep(20 * time.Millisecond)

	// Low waiter: publish the target and re-check exactly as park does,
	// then stop before receiving.
	low.target.Store(10)
	if f.pos.Load() >= 10 {
		t.Fatal("low target met before the store")
	}

	// Crossing store for the low target only; the publisher then goes
	// quiet, like the master entering its own barrier wait.
	f.Store(10)

	// Give the high waiter time to consume anything it can reach.
	time.Sleep(20 * time.Millisecond)

	select {
	case <-low.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("low waiter's wake was lost")
	}
	low.target.Store(parkIdle)

	f.Store(1000)
	select {
	case <-highWoke:
	case <-time.After(5 * time.Second):
		t.Fatal("high waiter did not wake at its own target")
	}
}
