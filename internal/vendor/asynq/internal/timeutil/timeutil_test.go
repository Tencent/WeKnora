// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package timeutil

import (
	"testing"
	"time"
)

func TestSimulatedClock(t *testing.T) {
	now := time.Now()

	tests := []struct {
		desc      string
		initTime  time.Time
		advanceBy time.Duration
		wantTime  time.Time
	}{
		{
			desc:      "advance time forward",
			initTime:  now,
			advanceBy: 30 * time.Second,
			wantTime:  now.Add(30 * time.Second),
		},
		{
			desc:      "advance time backward",
			initTime:  now,
			advanceBy: -10 * time.Second,
			wantTime:  now.Add(-10 * time.Second),
		},
	}

	for _, tc := range tests {
		c := NewSimulatedClock(tc.initTime)

		if c.Now() != tc.initTime {
			t.Errorf("%s: Before Advance; SimulatedClock.Now() = %v, want %v", tc.desc, c.Now(), tc.initTime)
		}

		c.AdvanceTime(tc.advanceBy)

		if c.Now() != tc.wantTime {
			t.Errorf("%s: After Advance; SimulatedClock.Now() = %v, want %v", tc.desc, c.Now(), tc.wantTime)
		}
	}
}
