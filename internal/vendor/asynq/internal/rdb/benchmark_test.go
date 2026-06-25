// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package rdb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/testutil"
)

func BenchmarkEnqueue(b *testing.B) {
	r := setup(b)
	ctx := context.Background()
	msg := testutil.NewTaskMessage("task1", nil)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		b.StartTimer()

		if err := r.Enqueue(ctx, msg); err != nil {
			b.Fatalf("Enqueue failed: %v", err)
		}
	}
}

func BenchmarkEnqueueUnique(b *testing.B) {
	r := setup(b)
	ctx := context.Background()
	msg := &base.TaskMessage{
		Type:      "task1",
		Payload:   nil,
		Queue:     base.DefaultQueueName,
		UniqueKey: base.UniqueKey("default", "task1", nil),
	}
	uniqueTTL := 5 * time.Minute
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		b.StartTimer()

		if err := r.EnqueueUnique(ctx, msg, uniqueTTL); err != nil {
			b.Fatalf("EnqueueUnique failed: %v", err)
		}
	}
}

func BenchmarkSchedule(b *testing.B) {
	r := setup(b)
	ctx := context.Background()
	msg := testutil.NewTaskMessage("task1", nil)
	processAt := time.Now().Add(3 * time.Minute)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		b.StartTimer()

		if err := r.Schedule(ctx, msg, processAt); err != nil {
			b.Fatalf("Schedule failed: %v", err)
		}
	}
}

func BenchmarkScheduleUnique(b *testing.B) {
	r := setup(b)
	ctx := context.Background()
	msg := &base.TaskMessage{
		Type:      "task1",
		Payload:   nil,
		Queue:     base.DefaultQueueName,
		UniqueKey: base.UniqueKey("default", "task1", nil),
	}
	processAt := time.Now().Add(3 * time.Minute)
	uniqueTTL := 5 * time.Minute
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		b.StartTimer()

		if err := r.ScheduleUnique(ctx, msg, processAt, uniqueTTL); err != nil {
			b.Fatalf("EnqueueUnique failed: %v", err)
		}
	}
}

func BenchmarkDequeueSingleQueue(b *testing.B) {
	r := setup(b)
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		for i := 0; i < 10; i++ {
			m := testutil.NewTaskMessageWithQueue(
				fmt.Sprintf("task%d", i), nil, base.DefaultQueueName)
			if err := r.Enqueue(ctx, m); err != nil {
				b.Fatalf("Enqueue failed: %v", err)
			}
		}
		b.StartTimer()

		if _, _, err := r.Dequeue(base.DefaultQueueName); err != nil {
			b.Fatalf("Dequeue failed: %v", err)
		}
	}
}

func BenchmarkDequeueMultipleQueues(b *testing.B) {
	qnames := []string{"critical", "default", "low"}
	r := setup(b)
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		for i := 0; i < 10; i++ {
			for _, qname := range qnames {
				m := testutil.NewTaskMessageWithQueue(
					fmt.Sprintf("%s_task%d", qname, i), nil, qname)
				if err := r.Enqueue(ctx, m); err != nil {
					b.Fatalf("Enqueue failed: %v", err)
				}
			}
		}
		b.StartTimer()

		if _, _, err := r.Dequeue(qnames...); err != nil {
			b.Fatalf("Dequeue failed: %v", err)
		}
	}
}

func BenchmarkDone(b *testing.B) {
	r := setup(b)
	m1 := testutil.NewTaskMessage("task1", nil)
	m2 := testutil.NewTaskMessage("task2", nil)
	m3 := testutil.NewTaskMessage("task3", nil)
	msgs := []*base.TaskMessage{m1, m2, m3}
	zs := []base.Z{
		{Message: m1, Score: time.Now().Add(10 * time.Second).Unix()},
		{Message: m2, Score: time.Now().Add(20 * time.Second).Unix()},
		{Message: m3, Score: time.Now().Add(30 * time.Second).Unix()},
	}
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		testutil.SeedActiveQueue(b, r.client, msgs, base.DefaultQueueName)
		testutil.SeedLease(b, r.client, zs, base.DefaultQueueName)
		b.StartTimer()

		if err := r.Done(ctx, msgs[0]); err != nil {
			b.Fatalf("Done failed: %v", err)
		}
	}
}

func BenchmarkRetry(b *testing.B) {
	r := setup(b)
	m1 := testutil.NewTaskMessage("task1", nil)
	m2 := testutil.NewTaskMessage("task2", nil)
	m3 := testutil.NewTaskMessage("task3", nil)
	msgs := []*base.TaskMessage{m1, m2, m3}
	zs := []base.Z{
		{Message: m1, Score: time.Now().Add(10 * time.Second).Unix()},
		{Message: m2, Score: time.Now().Add(20 * time.Second).Unix()},
		{Message: m3, Score: time.Now().Add(30 * time.Second).Unix()},
	}
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		testutil.SeedActiveQueue(b, r.client, msgs, base.DefaultQueueName)
		testutil.SeedLease(b, r.client, zs, base.DefaultQueueName)
		b.StartTimer()

		if err := r.Retry(ctx, msgs[0], time.Now().Add(1*time.Minute), "error", true /*isFailure*/); err != nil {
			b.Fatalf("Retry failed: %v", err)
		}
	}
}

func BenchmarkArchive(b *testing.B) {
	r := setup(b)
	m1 := testutil.NewTaskMessage("task1", nil)
	m2 := testutil.NewTaskMessage("task2", nil)
	m3 := testutil.NewTaskMessage("task3", nil)
	msgs := []*base.TaskMessage{m1, m2, m3}
	zs := []base.Z{
		{Message: m1, Score: time.Now().Add(10 * time.Second).Unix()},
		{Message: m2, Score: time.Now().Add(20 * time.Second).Unix()},
		{Message: m3, Score: time.Now().Add(30 * time.Second).Unix()},
	}
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		testutil.SeedActiveQueue(b, r.client, msgs, base.DefaultQueueName)
		testutil.SeedLease(b, r.client, zs, base.DefaultQueueName)
		b.StartTimer()

		if err := r.Archive(ctx, msgs[0], "error"); err != nil {
			b.Fatalf("Archive failed: %v", err)
		}
	}
}

func BenchmarkRequeue(b *testing.B) {
	r := setup(b)
	m1 := testutil.NewTaskMessage("task1", nil)
	m2 := testutil.NewTaskMessage("task2", nil)
	m3 := testutil.NewTaskMessage("task3", nil)
	msgs := []*base.TaskMessage{m1, m2, m3}
	zs := []base.Z{
		{Message: m1, Score: time.Now().Add(10 * time.Second).Unix()},
		{Message: m2, Score: time.Now().Add(20 * time.Second).Unix()},
		{Message: m3, Score: time.Now().Add(30 * time.Second).Unix()},
	}
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		testutil.SeedActiveQueue(b, r.client, msgs, base.DefaultQueueName)
		testutil.SeedLease(b, r.client, zs, base.DefaultQueueName)
		b.StartTimer()

		if err := r.Requeue(ctx, msgs[0]); err != nil {
			b.Fatalf("Requeue failed: %v", err)
		}
	}
}

func BenchmarkCheckAndEnqueue(b *testing.B) {
	r := setup(b)
	now := time.Now()
	var zs []base.Z
	for i := -100; i < 100; i++ {
		msg := testutil.NewTaskMessage(fmt.Sprintf("task%d", i), nil)
		score := now.Add(time.Duration(i) * time.Second).Unix()
		zs = append(zs, base.Z{Message: msg, Score: score})
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		testutil.FlushDB(b, r.client)
		testutil.SeedScheduledQueue(b, r.client, zs, base.DefaultQueueName)
		b.StartTimer()

		if err := r.ForwardIfReady(base.DefaultQueueName); err != nil {
			b.Fatalf("ForwardIfReady failed: %v", err)
		}
	}
}
