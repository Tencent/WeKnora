// Copyright 2021 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"sync"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/log"
)

// A janitor is responsible for deleting expired completed tasks from the specified
// queues. It periodically checks for any expired tasks in the completed set, and
// deletes them.
type janitor struct {
	logger *log.Logger
	broker base.Broker

	// channel to communicate back to the long running "janitor" goroutine.
	done chan struct{}

	// list of queue names to check.
	queues []string

	// average interval between checks.
	avgInterval time.Duration

	// number of tasks to be deleted when janitor runs to delete the expired completed tasks.
	batchSize int
}

type janitorParams struct {
	logger    *log.Logger
	broker    base.Broker
	queues    []string
	interval  time.Duration
	batchSize int
}

func newJanitor(params janitorParams) *janitor {
	return &janitor{
		logger:      params.logger,
		broker:      params.broker,
		done:        make(chan struct{}),
		queues:      params.queues,
		avgInterval: params.interval,
		batchSize:   params.batchSize,
	}
}

func (j *janitor) shutdown() {
	j.logger.Debug("Janitor shutting down...")
	// Signal the janitor goroutine to stop.
	j.done <- struct{}{}
}

// start starts the "janitor" goroutine.
func (j *janitor) start(wg *sync.WaitGroup) {
	wg.Add(1)
	timer := time.NewTimer(j.avgInterval) // randomize this interval with margin of 1s
	go func() {
		defer wg.Done()
		for {
			select {
			case <-j.done:
				j.logger.Debug("Janitor done")
				return
			case <-timer.C:
				j.exec()
				timer.Reset(j.avgInterval)
			}
		}
	}()
}

func (j *janitor) exec() {
	for _, qname := range j.queues {
		if err := j.broker.DeleteExpiredCompletedTasks(qname, j.batchSize); err != nil {
			j.logger.Errorf("Failed to delete expired completed tasks from queue %q: %v",
				qname, err)
		}
	}
}
