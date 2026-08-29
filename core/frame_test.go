// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "testing"

// TestSpinLimitForProcs covers the two spin-budget regimes: a large
// budget when every frame-walk worker can run on its own host thread
// with slack, a small yield-cadence budget when threads are scarcer
// than workers.
func TestSpinLimitForProcs(t *testing.T) {
	cases := []struct {
		procs int
		want  int
	}{
		{1, 1 << 12},
		{2, 1 << 12},
		{3, 1 << 12},
		{4, 1 << 20},
		{10, 1 << 20},
	}
	for _, c := range cases {
		if got := spinLimitForProcs(c.procs); got != c.want {
			t.Errorf("spinLimitForProcs(%d) = %d, want %d", c.procs, got, c.want)
		}
	}
}
