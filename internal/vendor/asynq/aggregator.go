// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"sync"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/log"
)

// An aggregator is responsible for checking groups and aggregate into one task
// if any of the grouping condition is met.
type aggregator struct {
	logger *log.Logger
	broker base.Broker
	client *Client

	// channel to communicate back to the long running "aggregator" goroutine.
	done chan struct{}

	// list of queue names to check and aggregate.
	queues []string

	// Group configurations
	gracePeriod time.Duration
	maxDelay    time.Duration
	maxSize     int

	// User provided group aggregator.
	ga GroupAggregator

	// interval used to check for aggregation
	interval time.Duration

	// sema is a counting semaphore to ensure the number of active aggregating function
	// does not exceed the limit.
	sema chan struct{}
}

type aggregatorParams struct {
	logger          *log.Logger
	broker          base.Broker
	queues          []string
	gracePeriod     time.Duration
	maxDelay        time.Duration
	maxSize         int
	groupAggregator GroupAggregator
}

const (
	// Maximum number of aggregation checks in flight concurrently.
	maxConcurrentAggregationChecks = 3

	// Default interval used for aggregation checks. If the provided gracePeriod is less than
	// the default, use the gracePeriod.
	defaultAggregationCheckInterval = 7 * time.Second
)

func newAggregator(params aggregatorParams) *aggregator {
	interval := defaultAggregationCheckInterval
	if params.gracePeriod < interval {
		interval = params.gracePeriod
	}
	return &aggregator{
		logger:      params.logger,
		broker:      params.broker,
		client:      &Client{broker: params.broker},
		done:        make(chan struct{}),
		queues:      params.queues,
		gracePeriod: params.gracePeriod,
		maxDelay:    params.maxDelay,
		maxSize:     params.maxSize,
		ga:          params.groupAggregator,
		sema:        make(chan struct{}, maxConcurrentAggregationChecks),
		interval:    interval,
	}
}

func (a *aggregator) shutdown() {
	if a.ga == nil {
		return
	}
	a.logger.Debug("Aggregator shutting down...")
	// Signal the aggregator goroutine to stop.
	a.done <- struct{}{}
}

func (a *aggregator) start(wg *sync.WaitGroup) {
	if a.ga == nil {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(a.interval)
		for {
			select {
			case <-a.done:
				a.logger.Debug("Waiting for all aggregation checks to finish...")
				// block until all aggregation checks released the token
				for i := 0; i < cap(a.sema); i++ {
					a.sema <- struct{}{}
				}
				a.logger.Debug("Aggregator done")
				ticker.Stop()
				return
			case t := <-ticker.C:
				a.exec(t)
			}
		}
	}()
}

func (a *aggregator) exec(t time.Time) {
	select {
	case a.sema <- struct{}{}: // acquire token
		go a.aggregate(t)
	default:
		// If the semaphore blocks, then we are currently running max number of
		// aggregation checks. Skip this round and log warning.
		a.logger.Warnf("Max number of aggregation checks in flight. Skipping")
	}
}

func (a *aggregator) aggregate(t time.Time) {
	defer func() { <-a.sema /* release token */ }()
	for _, qname := range a.queues {
		groups, err := a.broker.ListGroups(qname)
		if err != nil {
			a.logger.Errorf("Failed to list groups in queue: %q", qname)
			continue
		}
		for _, gname := range groups {
			aggregationSetID, err := a.broker.AggregationCheck(
				qname, gname, t, a.gracePeriod, a.maxDelay, a.maxSize)
			if err != nil {
				a.logger.Errorf("Failed to run aggregation check: queue=%q group=%q", qname, gname)
				continue
			}
			if aggregationSetID == "" {
				a.logger.Debugf("No aggregation needed at this time: queue=%q group=%q", qname, gname)
				continue
			}

			// Aggregate and enqueue.
			msgs, deadline, err := a.broker.ReadAggregationSet(qname, gname, aggregationSetID)
			if err != nil {
				a.logger.Errorf("Failed to read aggregation set: queue=%q, group=%q, setID=%q",
					qname, gname, aggregationSetID)
				continue
			}
			tasks := make([]*Task, len(msgs))
			for i, m := range msgs {
				tasks[i] = NewTaskWithHeaders(m.Type, m.Payload, m.Headers)
			}
			aggregatedTask := a.ga.Aggregate(gname, tasks)
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			if _, err := a.client.EnqueueContext(ctx, aggregatedTask, Queue(qname)); err != nil {
				a.logger.Errorf("Failed to enqueue aggregated task (queue=%q, group=%q, setID=%q): %v",
					qname, gname, aggregationSetID, err)
				cancel()
				continue
			}
			if err := a.broker.DeleteAggregationSet(ctx, qname, gname, aggregationSetID); err != nil {
				a.logger.Warnf("Failed to delete aggregation set: queue=%q, group=%q, setID=%q",
					qname, gname, aggregationSetID)
			}
			cancel()
		}
	}
}
