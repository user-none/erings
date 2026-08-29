// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"math"
	"sync/atomic"
)

// parkSpinLimit is how many iterations Wait busy-spins before parking.
// It is sized so every normal frame-walk wait (sub-microsecond barrier
// waits, tens-of-microseconds line waits, intra-frame component bursts)
// resolves by spinning alone; parking is reserved for host stalls.
// An iteration count rather than a duration keeps the budget portable:
// the waits are the peer goroutine's compute time, so on a slower host
// both the spin and the waits it must cover stretch together and the
// margin holds without per-machine calibration. Yielding per iteration
// instead is not an option: each runtime.Gosched re-queues the
// goroutine and wakes an idle OS thread, which dominates CPU at
// wait-loop frequency.
const parkSpinLimit = 1 << 20

// parkIdle marks a waiter as not parked.
const parkIdle = int64(math.MaxInt64)

// fence is a timeline fence: a monotonic value published by one
// goroutine and waited on by others, which block until it reaches
// their target. The unit is whatever the publisher advances by -
// system cycles for the SH-2 clocks, walked scanlines for the VDP
// walker - so a target is only meaningful against the fence it is
// registered with. Waiters are created with NewWaiter at construction,
// before the worker goroutines start; Store wakes each registered
// waiter whose target the new value crosses, and costs one target load
// per waiter when nothing is parked.
type fence struct {
	pos     atomic.Int64
	waiters []*fenceWaiter
}

// NewWaiter registers and returns a waiter on this fence. A waiter is
// owned by exactly one goroutine: only its owner calls Wait. Waiters
// must be registered before the workers start; registration is not
// synchronized against Store.
func (f *fence) NewWaiter() *fenceWaiter {
	w := &fenceWaiter{pos: &f.pos, wake: make(chan struct{}, 1)}
	w.target.Store(parkIdle)
	f.waiters = append(f.waiters, w)
	return w
}

// Store publishes a new position, waking parked waiters it crosses.
func (f *fence) Store(v int64) {
	f.pos.Store(v)
	for _, w := range f.waiters {
		if v >= w.target.Load() {
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
	}
}

// fenceWaiter is one goroutine's handle for waiting on a fence.
// target holds the position a parked owner needs (parkIdle otherwise),
// and only the owner receives from wake, so a wake can never be
// consumed by a different waiter.
//
// Wakes cannot be lost: the owner stores its target before re-checking
// the position, the publisher stores the position before loading the
// targets, so one side always observes the other. A stale token left
// by an earlier park causes at most a re-check and re-park.
type fenceWaiter struct {
	pos    *atomic.Int64
	target atomic.Int64
	wake   chan struct{}
}

// Wait holds until the fence reaches target: a bare spin for
// parkSpinLimit iterations, then a park.
func (w *fenceWaiter) Wait(target int64) {
	spins := 0
	for w.pos.Load() < target {
		spins++
		if spins >= parkSpinLimit {
			w.park(target)
			return
		}
	}
}

// park publishes the needed position and blocks on the wake channel
// until the fence reaches it.
func (w *fenceWaiter) park(target int64) {
	w.target.Store(target)
	for w.pos.Load() < target {
		<-w.wake
	}
	w.target.Store(parkIdle)
}
