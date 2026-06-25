// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/rdb"
	h "github.com/hibiken/asynq/internal/testutil"
)

func TestSyncer(t *testing.T) {
	inProgress := []*base.TaskMessage{
		h.NewTaskMessage("send_email", nil),
		h.NewTaskMessage("reindex", nil),
		h.NewTaskMessage("gen_thumbnail", nil),
	}
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)
	h.SeedActiveQueue(t, r, inProgress, base.DefaultQueueName)

	const interval = time.Second
	syncRequestCh := make(chan *syncRequest)
	syncer := newSyncer(syncerParams{
		logger:     testLogger,
		requestsCh: syncRequestCh,
		interval:   interval,
	})
	var wg sync.WaitGroup
	syncer.start(&wg)
	defer syncer.shutdown()

	for _, msg := range inProgress {
		m := msg
		syncRequestCh <- &syncRequest{
			fn: func() error {
				return rdbClient.Done(context.Background(), m)
			},
			deadline: time.Now().Add(5 * time.Minute),
		}
	}

	time.Sleep(2 * interval) // ensure that syncer runs at least once

	gotActive := h.GetActiveMessages(t, r, base.DefaultQueueName)
	if l := len(gotActive); l != 0 {
		t.Errorf("%q has length %d; want 0", base.ActiveKey(base.DefaultQueueName), l)
	}
}

func TestSyncerRetry(t *testing.T) {
	const interval = time.Second
	syncRequestCh := make(chan *syncRequest)
	syncer := newSyncer(syncerParams{
		logger:     testLogger,
		requestsCh: syncRequestCh,
		interval:   interval,
	})

	var wg sync.WaitGroup
	syncer.start(&wg)
	defer syncer.shutdown()

	var (
		mu      sync.Mutex
		counter int
	)

	// Increment the counter for each call.
	// Initial call will fail and second call will succeed.
	requestFunc := func() error {
		mu.Lock()
		defer mu.Unlock()
		if counter == 0 {
			counter++
			return fmt.Errorf("zero")
		}
		counter++
		return nil
	}

	syncRequestCh <- &syncRequest{
		fn:       requestFunc,
		errMsg:   "error",
		deadline: time.Now().Add(5 * time.Minute),
	}

	// allow syncer to retry
	time.Sleep(3 * interval)

	mu.Lock()
	if counter != 2 {
		t.Errorf("counter = %d, want 2", counter)
	}
	mu.Unlock()
}

func TestSyncerDropsStaleRequests(t *testing.T) {
	const interval = time.Second
	syncRequestCh := make(chan *syncRequest)
	syncer := newSyncer(syncerParams{
		logger:     testLogger,
		requestsCh: syncRequestCh,
		interval:   interval,
	})
	var wg sync.WaitGroup
	syncer.start(&wg)

	var (
		mu sync.Mutex
		n  int // number of times request has been processed
	)

	for i := 0; i < 10; i++ {
		syncRequestCh <- &syncRequest{
			fn: func() error {
				mu.Lock()
				n++
				mu.Unlock()
				return nil
			},
			deadline: time.Now().Add(time.Duration(-i) * time.Second), // already exceeded deadline
		}
	}

	time.Sleep(2 * interval) // ensure that syncer runs at least once
	syncer.shutdown()

	mu.Lock()
	if n != 0 {
		t.Errorf("requests has been processed %d times, want 0", n)
	}
	mu.Unlock()
}
