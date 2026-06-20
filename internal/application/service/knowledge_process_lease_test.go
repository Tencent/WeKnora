package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeProcessLeaseLiteSerializesByKnowledge(t *testing.T) {
	ctx := context.Background()
	svc := &knowledgeService{}

	lease, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-lease")
	require.NoError(t, err)
	defer lease.Release()

	_, err = svc.acquireKnowledgeProcessLease(ctx, 1, "kid-lease")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKnowledgeProcessLeaseBusy))

	reentrant, err := svc.acquireKnowledgeProcessLease(lease.Context, 1, "kid-lease")
	require.NoError(t, err)
	assert.Equal(t, lease.Key, reentrant.Key)
	assert.Equal(t, lease.Context, reentrant.Context)
	reentrant.Release()

	_, err = svc.acquireKnowledgeProcessLease(ctx, 1, "kid-lease")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKnowledgeProcessLeaseBusy))

	lease.Release()
	leaseAgain, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-lease")
	require.NoError(t, err)
	leaseAgain.Release()
}

func TestKnowledgeProcessLeaseLiteScopesByKnowledge(t *testing.T) {
	ctx := context.Background()
	svc := &knowledgeService{}

	leaseA, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-a")
	require.NoError(t, err)
	defer leaseA.Release()

	leaseB, err := svc.acquireKnowledgeProcessLease(ctx, 1, "kid-b")
	require.NoError(t, err)
	leaseB.Release()
}

func TestKnowledgeProcessLeaseLiteHighContentionSerializes(t *testing.T) {
	ctx := context.Background()
	svc := &knowledgeService{}

	var active int32
	var maxActive int32
	var acquired int32
	var unexpectedBusyErr int32
	const workers = 200
	const rounds = 20

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				var lease *KnowledgeProcessLease
				for {
					var err error
					lease, err = svc.acquireKnowledgeProcessLease(ctx, 1, "kid-hot")
					if err == nil {
						break
					}
					if !errors.Is(err, ErrKnowledgeProcessLeaseBusy) {
						atomic.AddInt32(&unexpectedBusyErr, 1)
						return
					}
					time.Sleep(time.Microsecond)
				}
				now := atomic.AddInt32(&active, 1)
				for {
					seen := atomic.LoadInt32(&maxActive)
					if now <= seen || atomic.CompareAndSwapInt32(&maxActive, seen, now) {
						break
					}
				}
				time.Sleep(time.Microsecond)
				atomic.AddInt32(&active, -1)
				atomic.AddInt32(&acquired, 1)
				lease.Release()
				lease.Release()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), maxActive)
	require.Equal(t, int32(workers*rounds), acquired)
	require.Zero(t, unexpectedBusyErr)
}
