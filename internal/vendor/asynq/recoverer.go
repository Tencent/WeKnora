// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"sync"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/errors"
	"github.com/hibiken/asynq/internal/log"
)

type recoverer struct {
	logger         *log.Logger
	broker         base.Broker
	retryDelayFunc RetryDelayFunc
	isFailureFunc  func(error) bool

	// channel to communicate back to the long running "recoverer" goroutine.
	done chan struct{}

	// list of queues to check for deadline.
	queues []string

	// poll interval.
	interval time.Duration
}

type recovererParams struct {
	logger         *log.Logger
	broker         base.Broker
	queues         []string
	interval       time.Duration
	retryDelayFunc RetryDelayFunc
	isFailureFunc  func(error) bool
}

func newRecoverer(params recovererParams) *recoverer {
	return &recoverer{
		logger:         params.logger,
		broker:         params.broker,
		done:           make(chan struct{}),
		queues:         params.queues,
		interval:       params.interval,
		retryDelayFunc: params.retryDelayFunc,
		isFailureFunc:  params.isFailureFunc,
	}
}

func (r *recoverer) shutdown() {
	r.logger.Debug("Recoverer shutting down...")
	// Signal the recoverer goroutine to stop polling.
	r.done <- struct{}{}
}

func (r *recoverer) start(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.recover()
		timer := time.NewTimer(r.interval)
		for {
			select {
			case <-r.done:
				r.logger.Debug("Recoverer done")
				timer.Stop()
				return
			case <-timer.C:
				r.recover()
				timer.Reset(r.interval)
			}
		}
	}()
}

// ErrLeaseExpired error indicates that the task failed because the worker working on the task
// could not extend its lease due to missing heartbeats. The worker may have crashed or got cutoff from the network.
var ErrLeaseExpired = errors.New("asynq: task lease expired")

func (r *recoverer) recover() {
	r.recoverLeaseExpiredTasks()
	r.recoverStaleAggregationSets()
}

func (r *recoverer) recoverLeaseExpiredTasks() {
	// Get all tasks which have expired 30 seconds ago or earlier to accommodate certain amount of clock skew.
	cutoff := time.Now().Add(-30 * time.Second)
	msgs, err := r.broker.ListLeaseExpired(cutoff, r.queues...)
	if err != nil {
		r.logger.Warnf("recoverer: could not list lease expired tasks: %v", err)
		return
	}
	for _, msg := range msgs {
		if msg.Retried >= msg.Retry {
			r.archive(msg, ErrLeaseExpired)
		} else {
			r.retry(msg, ErrLeaseExpired)
		}
	}
}

func (r *recoverer) recoverStaleAggregationSets() {
	for _, qname := range r.queues {
		if err := r.broker.ReclaimStaleAggregationSets(qname); err != nil {
			r.logger.Warnf("recoverer: could not reclaim stale aggregation sets in queue %q: %v", qname, err)
		}
	}
}

func (r *recoverer) retry(msg *base.TaskMessage, err error) {
	delay := r.retryDelayFunc(msg.Retried, err, NewTaskWithHeaders(msg.Type, msg.Payload, msg.Headers))
	retryAt := time.Now().Add(delay)
	if err := r.broker.Retry(context.Background(), msg, retryAt, err.Error(), r.isFailureFunc(err)); err != nil {
		r.logger.Warnf("recoverer: could not retry lease expired task: %v", err)
	}
}

func (r *recoverer) archive(msg *base.TaskMessage, err error) {
	if err := r.broker.Archive(context.Background(), msg, err.Error()); err != nil {
		r.logger.Warnf("recoverer: could not move task to archive: %v", err)
	}
}
