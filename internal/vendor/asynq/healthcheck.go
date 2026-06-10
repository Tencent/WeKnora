// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"sync"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/log"
)

// healthchecker is responsible for pinging broker periodically
// and call user provided HeathCheckFunc with the ping result.
type healthchecker struct {
	logger *log.Logger
	broker base.Broker

	// channel to communicate back to the long running "healthchecker" goroutine.
	done chan struct{}

	// interval between healthchecks.
	interval time.Duration

	// function to call periodically.
	healthcheckFunc func(error)
}

type healthcheckerParams struct {
	logger          *log.Logger
	broker          base.Broker
	interval        time.Duration
	healthcheckFunc func(error)
}

func newHealthChecker(params healthcheckerParams) *healthchecker {
	return &healthchecker{
		logger:          params.logger,
		broker:          params.broker,
		done:            make(chan struct{}),
		interval:        params.interval,
		healthcheckFunc: params.healthcheckFunc,
	}
}

func (hc *healthchecker) shutdown() {
	if hc.healthcheckFunc == nil {
		return
	}

	hc.logger.Debug("Healthchecker shutting down...")
	// Signal the healthchecker goroutine to stop.
	hc.done <- struct{}{}
}

func (hc *healthchecker) start(wg *sync.WaitGroup) {
	if hc.healthcheckFunc == nil {
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(hc.interval)
		for {
			select {
			case <-hc.done:
				hc.logger.Debug("Healthchecker done")
				timer.Stop()
				return
			case <-timer.C:
				err := hc.broker.Ping()
				hc.healthcheckFunc(err)
				timer.Reset(hc.interval)
			}
		}
	}()
}
