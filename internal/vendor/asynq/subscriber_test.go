// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/rdb"
	"github.com/hibiken/asynq/internal/testbroker"
)

func TestSubscriber(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	tests := []struct {
		registeredID string // ID for which cancel func is registered
		publishID    string // ID to be published
		wantCalled   bool   // whether cancel func should be called
	}{
		{"abc123", "abc123", true},
		{"abc456", "abc123", false},
	}

	for _, tc := range tests {
		var mu sync.Mutex
		called := false
		fakeCancelFunc := func() {
			mu.Lock()
			defer mu.Unlock()
			called = true
		}
		cancelations := base.NewCancelations()
		cancelations.Add(tc.registeredID, fakeCancelFunc)

		subscriber := newSubscriber(subscriberParams{
			logger:       testLogger,
			broker:       rdbClient,
			cancelations: cancelations,
		})
		var wg sync.WaitGroup
		subscriber.start(&wg)
		defer subscriber.shutdown()

		// wait for subscriber to establish connection to pubsub channel
		time.Sleep(time.Second)

		if err := rdbClient.PublishCancelation(tc.publishID); err != nil {
			t.Fatalf("could not publish cancelation message: %v", err)
		}

		// wait for redis to publish message
		time.Sleep(time.Second)

		mu.Lock()
		if called != tc.wantCalled {
			if tc.wantCalled {
				t.Errorf("fakeCancelFunc was not called, want the function to be called")
			} else {
				t.Errorf("fakeCancelFunc was called, want the function to not be called")
			}
		}
		mu.Unlock()
	}
}

func TestSubscriberWithRedisDown(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic occurred: %v", r)
		}
	}()
	r := rdb.NewRDB(setup(t))
	defer r.Close()
	testBroker := testbroker.NewTestBroker(r)

	cancelations := base.NewCancelations()
	subscriber := newSubscriber(subscriberParams{
		logger:       testLogger,
		broker:       testBroker,
		cancelations: cancelations,
	})
	subscriber.retryTimeout = 1 * time.Second // set shorter retry timeout for testing purpose.

	testBroker.Sleep() // simulate a situation where subscriber cannot connect to redis.
	var wg sync.WaitGroup
	subscriber.start(&wg)
	defer subscriber.shutdown()

	time.Sleep(2 * time.Second) // subscriber should wait and retry connecting to redis.

	testBroker.Wakeup() // simulate a situation where redis server is back online.

	time.Sleep(2 * time.Second) // allow subscriber to establish pubsub channel.

	const id = "test"
	var (
		mu     sync.Mutex
		called bool
	)
	cancelations.Add(id, func() {
		mu.Lock()
		defer mu.Unlock()
		called = true
	})

	if err := r.PublishCancelation(id); err != nil {
		t.Fatalf("could not publish cancelation message: %v", err)
	}

	time.Sleep(time.Second) // wait for redis to publish message.

	mu.Lock()
	if !called {
		t.Errorf("cancel function was not called")
	}
	mu.Unlock()
}
