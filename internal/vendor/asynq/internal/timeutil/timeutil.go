// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

// Package timeutil exports functions and types related to time and date.
package timeutil

import (
	"sync"
	"time"
)

// A Clock is an object that can tell you the current time.
//
// This interface allows decoupling code that uses time from the code that creates
// a point in time. You can use this to your advantage by injecting Clocks into interfaces
// rather than having implementations call time.Now() directly.
//
// Use RealClock() in production.
// Use SimulatedClock() in test.
type Clock interface {
	Now() time.Time
}

func NewRealClock() Clock { return &realTimeClock{} }

type realTimeClock struct{}

func (_ *realTimeClock) Now() time.Time { return time.Now() }

// A SimulatedClock is a concrete Clock implementation that doesn't "tick" on its own.
// Time is advanced by explicit call to the AdvanceTime() or SetTime() functions.
// This object is concurrency safe.
type SimulatedClock struct {
	mu sync.Mutex
	t  time.Time // guarded by mu
}

func NewSimulatedClock(t time.Time) *SimulatedClock {
	return &SimulatedClock{t: t}
}

func (c *SimulatedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *SimulatedClock) SetTime(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *SimulatedClock) AdvanceTime(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
