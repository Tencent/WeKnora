package service

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type processingArtifactCounterGetErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

func (r processingArtifactCounterGetErrorRepository) Get(context.Context, types.ProcessingArtifactKey) (*types.ProcessingArtifact, error) {
	return nil, r.err
}

type processingArtifactCounterBatchGetErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

type processingArtifactCounterBatchPutErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

func (r processingArtifactCounterBatchPutErrorRepository) GetMany(
	ctx context.Context,
	keys []types.ProcessingArtifactKey,
) (map[types.ProcessingArtifactKey]*types.ProcessingArtifact, error) {
	return r.ProcessingArtifactRepository.(interface {
		GetMany(context.Context, []types.ProcessingArtifactKey) (map[types.ProcessingArtifactKey]*types.ProcessingArtifact, error)
	}).GetMany(ctx, keys)
}

func (r processingArtifactCounterBatchPutErrorRepository) PutManyIfAbsent(
	context.Context,
	[]*types.ProcessingArtifact,
) error {
	return r.err
}

type processingArtifactCounterBatchWinnerReadErrorRepository struct {
	interfaces.ProcessingArtifactRepository
	err error
}

func (r processingArtifactCounterBatchWinnerReadErrorRepository) GetMany(
	context.Context,
	[]types.ProcessingArtifactKey,
) (map[types.ProcessingArtifactKey]*types.ProcessingArtifact, error) {
	return nil, r.err
}

func (r processingArtifactCounterBatchWinnerReadErrorRepository) PutManyIfAbsent(
	ctx context.Context,
	artifacts []*types.ProcessingArtifact,
) error {
	return r.ProcessingArtifactRepository.(interface {
		PutManyIfAbsent(context.Context, []*types.ProcessingArtifact) error
	}).PutManyIfAbsent(ctx, artifacts)
}

type processingArtifactCounterSequentialPutFailureRepository struct {
	interfaces.ProcessingArtifactRepository
	mu         sync.Mutex
	putCount   int
	successKey types.ProcessingArtifactKey
	failureKey types.ProcessingArtifactKey
	failure    error
}

func (r *processingArtifactCounterSequentialPutFailureRepository) PutIfAbsent(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
) (bool, error) {
	r.mu.Lock()
	r.putCount++
	putCount := r.putCount
	r.mu.Unlock()
	if putCount == 2 {
		r.mu.Lock()
		r.failureKey = types.ProcessingArtifactKey{
			TenantID: artifact.TenantID, Stage: artifact.Stage,
			KeyVersion: artifact.KeyVersion, InputFingerprint: artifact.InputFingerprint,
		}
		r.mu.Unlock()
		return false, r.failure
	}
	created, err := r.ProcessingArtifactRepository.PutIfAbsent(ctx, artifact)
	if err == nil {
		r.mu.Lock()
		r.successKey = types.ProcessingArtifactKey{
			TenantID: artifact.TenantID, Stage: artifact.Stage,
			KeyVersion: artifact.KeyVersion, InputFingerprint: artifact.InputFingerprint,
		}
		r.mu.Unlock()
	}
	return created, err
}

func (r *processingArtifactCounterSequentialPutFailureRepository) outcomeKeys() (types.ProcessingArtifactKey, types.ProcessingArtifactKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.successKey, r.failureKey
}

type processingArtifactCounterInvalidationBarrierRepository struct {
	interfaces.ProcessingArtifactRepository
	entered chan<- struct{}
	release <-chan struct{}
}

func (r processingArtifactCounterInvalidationBarrierRepository) Get(
	ctx context.Context,
	key types.ProcessingArtifactKey,
) (*types.ProcessingArtifact, error) {
	artifact, err := r.ProcessingArtifactRepository.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	r.entered <- struct{}{}
	<-r.release
	return artifact, nil
}

func (r processingArtifactCounterInvalidationBarrierRepository) DeleteByIDWithResult(
	ctx context.Context,
	tenantID, id uint64,
) (bool, error) {
	return r.ProcessingArtifactRepository.DeleteByIDWithResult(ctx, tenantID, id)
}

func (r processingArtifactCounterBatchGetErrorRepository) GetMany(
	context.Context,
	[]types.ProcessingArtifactKey,
) (map[types.ProcessingArtifactKey]*types.ProcessingArtifact, error) {
	return nil, r.err
}

func (r processingArtifactCounterBatchGetErrorRepository) PutManyIfAbsent(
	context.Context,
	[]*types.ProcessingArtifact,
) error {
	return r.err
}

func newProcessingArtifactCounterStore(
	t *testing.T,
	repository interfaces.ProcessingArtifactRepository,
) (interfaces.ProcessingArtifactStore, interfaces.ProcessingArtifactCounterRegistry) {
	t.Helper()
	registry := NewProcessingArtifactCounterRegistry()
	return NewProcessingArtifactStoreWithMaxPayloadAndCounterRegistry(
		repository,
		&artifactTenantRepository{tenant: &types.Tenant{ID: 7}},
		newArtifactFakeFileService(),
		ProcessingArtifactMaxPayload,
		registry,
	), registry
}

func processingArtifactCounterValue(
	counters []types.ProcessingArtifactCounter,
	stage, outcome string,
) uint64 {
	for _, counter := range counters {
		if counter.Stage == stage && counter.Outcome == outcome {
			return counter.Count
		}
	}
	return 0
}

func processingArtifactCounterKey(t *testing.T, stage, suffix string) types.ProcessingArtifactKey {
	t.Helper()
	key, err := types.NewProcessingArtifactKey(7, stage, 1, []byte(suffix))
	require.NoError(t, err)
	return key
}

func TestProcessingArtifactCountersRecordSingleGetOutcomes(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	store, registry := newProcessingArtifactCounterStore(t, state.repository)
	key := processingArtifactServiceKey(t, "counter-single-hit")
	_, _, err := store.PutIfAbsent(context.Background(), key, []byte("value"))
	require.NoError(t, err)
	_, hit, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, hit)
	_, hit, err = store.Get(context.Background(), processingArtifactServiceKey(t, "counter-single-miss"))
	require.NoError(t, err)
	assert.False(t, hit)

	counters := registry.Snapshot()
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "hit"))
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "miss"))
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "write"))
}

func TestProcessingArtifactCountersRecordGetErrors(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "counter-get-error")
	store, registry := newProcessingArtifactCounterStore(t, processingArtifactCounterGetErrorRepository{
		ProcessingArtifactRepository: state.repository,
		err:                          errors.New("repository unavailable"),
	})

	_, _, err := store.Get(context.Background(), key)
	require.Error(t, err)
	assert.Equal(t, uint64(1), processingArtifactCounterValue(registry.Snapshot(), key.Stage, "error"))
}

func TestProcessingArtifactCountersRecordBatchRequestedCardinality(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	store, registry := newProcessingArtifactCounterStore(t, state.repository)
	hitKey := processingArtifactServiceKey(t, "counter-batch-hit")
	missKey := processingArtifactServiceKey(t, "counter-batch-miss")
	_, _, err := store.PutIfAbsent(context.Background(), hitKey, []byte("value"))
	require.NoError(t, err)

	batchStore := store.(interfaces.ProcessingArtifactBatchStore)
	values, err := batchStore.GetMany(context.Background(), []types.ProcessingArtifactKey{hitKey, missKey, hitKey})
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), values[hitKey])
	counters := registry.Snapshot()
	assert.Equal(t, uint64(2), processingArtifactCounterValue(counters, hitKey.Stage, "hit"))
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, hitKey.Stage, "miss"))
}

func TestProcessingArtifactCountersRecordBatchFailureForEveryUnresolvedKey(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "counter-batch-error")
	store, registry := newProcessingArtifactCounterStore(t, processingArtifactCounterBatchGetErrorRepository{
		ProcessingArtifactRepository: state.repository,
		err:                          errors.New("repository unavailable"),
	})

	batchStore := store.(interfaces.ProcessingArtifactBatchStore)
	_, err := batchStore.GetMany(context.Background(), []types.ProcessingArtifactKey{key, key})
	require.Error(t, err)
	assert.Equal(t, uint64(2), processingArtifactCounterValue(registry.Snapshot(), key.Stage, "error"))
}

func TestProcessingArtifactCountersDoNotDoubleCountSequentialFallback(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "counter-sequential-fallback")
	store, registry := newProcessingArtifactCounterStore(t, struct {
		interfaces.ProcessingArtifactRepository
	}{state.repository})
	_, _, err := store.PutIfAbsent(context.Background(), key, []byte("value"))
	require.NoError(t, err)

	batchStore := store.(interfaces.ProcessingArtifactBatchStore)
	_, err = batchStore.GetMany(context.Background(), []types.ProcessingArtifactKey{key})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), processingArtifactCounterValue(registry.Snapshot(), key.Stage, "hit"))
}

func TestProcessingArtifactCountersRecordOnlyEffectiveInvalidations(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "counter-invalidation")
	seedProcessingArtifact(t, state.repository, key, []byte("observed"), "", 8, artifactSHA([]byte("observed")))
	store, registry := newProcessingArtifactCounterStore(t, state.repository)

	require.NoError(t, store.Invalidate(context.Background(), key, []byte("different")))
	require.NoError(t, store.Invalidate(context.Background(), key, []byte("observed")))
	require.NoError(t, store.Invalidate(context.Background(), key, []byte("observed")))
	assert.Equal(t, uint64(1), processingArtifactCounterValue(registry.Snapshot(), key.Stage, "evicted"))
}

func TestProcessingArtifactCountersDoNotDoubleCountConcurrentInvalidations(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "counter-concurrent-invalidation")
	seedProcessingArtifact(t, state.repository, key, []byte("observed"), "", 8, artifactSHA([]byte("observed")))
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	store, registry := newProcessingArtifactCounterStore(t, processingArtifactCounterInvalidationBarrierRepository{
		ProcessingArtifactRepository: state.repository,
		entered:                      entered,
		release:                      release,
	})

	var group sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			errs <- store.Invalidate(context.Background(), key, []byte("observed"))
		}()
	}
	<-entered
	<-entered
	release <- struct{}{}
	release <- struct{}{}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, uint64(1), processingArtifactCounterValue(registry.Snapshot(), key.Stage, "evicted"))
}

func TestProcessingArtifactCountersWriteOnlyForSuccessfulCanonicalValues(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	key := processingArtifactServiceKey(t, "counter-write")
	store, registry := newProcessingArtifactCounterStore(t, state.repository)

	_, _, err := store.PutIfAbsent(context.Background(), key, []byte("value"))
	require.NoError(t, err)
	_, _, err = store.PutIfAbsent(context.Background(), key, make([]byte, ProcessingArtifactMaxPayload+1))
	require.Error(t, err)
	counters := registry.Snapshot()
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "write"))
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "error"))
}

func TestProcessingArtifactCountersRecordBatchWritesPerSuppliedKey(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	store, registry := newProcessingArtifactCounterStore(t, state.repository)
	first := processingArtifactServiceKey(t, "counter-batch-write-first")
	second := processingArtifactServiceKey(t, "counter-batch-write-second")

	batchStore := store.(interfaces.ProcessingArtifactBatchStore)
	_, err := batchStore.PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		first:  []byte("first"),
		second: []byte("second"),
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(2), processingArtifactCounterValue(registry.Snapshot(), first.Stage, "write"))
}

func TestProcessingArtifactCountersRecordBatchWriteErrorsWithoutWrites(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	store, registry := newProcessingArtifactCounterStore(t, state.repository)
	first := processingArtifactServiceKey(t, "counter-batch-error-first")
	second := processingArtifactServiceKey(t, "counter-batch-error-second")

	batchStore := store.(interfaces.ProcessingArtifactBatchStore)
	_, err := batchStore.PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		first:  make([]byte, ProcessingArtifactMaxPayload+1),
		second: []byte("second"),
	})
	require.Error(t, err)
	counters := registry.Snapshot()
	assert.Equal(t, uint64(2), processingArtifactCounterValue(counters, first.Stage, "error"))
	assert.Zero(t, processingArtifactCounterValue(counters, first.Stage, "write"))
}

func TestProcessingArtifactCountersPreserveMixedBatchWriteProgress(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	large := processingArtifactCounterKey(t, "counter.mixed.large", "large")
	inline := processingArtifactCounterKey(t, "counter.mixed.inline", "inline")
	store, registry := newProcessingArtifactCounterStore(t, processingArtifactCounterBatchPutErrorRepository{
		ProcessingArtifactRepository: state.repository,
		err:                          errors.New("inline batch failed"),
	})

	_, err := store.(interfaces.ProcessingArtifactBatchStore).PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		large:  bytes.Repeat([]byte("x"), ProcessingArtifactInlineLimit+1),
		inline: []byte("inline"),
	})
	require.Error(t, err)
	counters := registry.Snapshot()
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, large.Stage, "write"))
	assert.Zero(t, processingArtifactCounterValue(counters, large.Stage, "error"))
	assert.Zero(t, processingArtifactCounterValue(counters, inline.Stage, "write"))
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, inline.Stage, "error"))
}

func TestProcessingArtifactCountersPreserveSequentialFallbackWriteProgress(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	repository := &processingArtifactCounterSequentialPutFailureRepository{
		ProcessingArtifactRepository: state.repository,
		failure:                      errors.New("second put failed"),
	}
	store, registry := newProcessingArtifactCounterStore(t, repository)
	first := processingArtifactCounterKey(t, "counter.sequential.first", "first")
	second := processingArtifactCounterKey(t, "counter.sequential.second", "second")

	_, err := store.(interfaces.ProcessingArtifactBatchStore).PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		first:  []byte("first"),
		second: []byte("second"),
	})
	require.Error(t, err)
	successKey, failureKey := repository.outcomeKeys()
	require.NotEmpty(t, successKey.Stage)
	require.NotEmpty(t, failureKey.Stage)
	counters := registry.Snapshot()
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, successKey.Stage, "write"))
	assert.Zero(t, processingArtifactCounterValue(counters, successKey.Stage, "error"))
	assert.Zero(t, processingArtifactCounterValue(counters, failureKey.Stage, "write"))
	assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, failureKey.Stage, "error"))
}

func TestProcessingArtifactCountersRecordDurableInlineWritesBeforeWinnerReadFailure(t *testing.T) {
	state := newProcessingArtifactTestStore(t)
	first := processingArtifactCounterKey(t, "counter.winner.first", "first")
	second := processingArtifactCounterKey(t, "counter.winner.second", "second")
	store, registry := newProcessingArtifactCounterStore(t, processingArtifactCounterBatchWinnerReadErrorRepository{
		ProcessingArtifactRepository: state.repository,
		err:                          errors.New("winner read failed"),
	})

	_, err := store.(interfaces.ProcessingArtifactBatchStore).PutManyIfAbsent(context.Background(), map[types.ProcessingArtifactKey][]byte{
		first:  []byte("first"),
		second: []byte("second"),
	})
	require.Error(t, err)
	counters := registry.Snapshot()
	for _, key := range []types.ProcessingArtifactKey{first, second} {
		assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "write"))
		assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "error"))
	}
}

func TestProcessingArtifactCountersRecordStructuralRepairs(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		state := newProcessingArtifactTestStore(t)
		key := processingArtifactCounterKey(t, "counter.repair.single", "single")
		seedProcessingArtifact(t, state.repository, key, []byte("corrupt"), "", 7, artifactSHA([]byte("different")))
		store, registry := newProcessingArtifactCounterStore(t, state.repository)

		_, hit, err := store.Get(context.Background(), key)
		require.NoError(t, err)
		assert.False(t, hit)
		counters := registry.Snapshot()
		assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "miss"))
		assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "evicted"))
		assert.Zero(t, processingArtifactCounterValue(counters, key.Stage, "error"))
	})

	t.Run("duplicate batch", func(t *testing.T) {
		state := newProcessingArtifactTestStore(t)
		key := processingArtifactCounterKey(t, "counter.repair.duplicate", "duplicate")
		seedProcessingArtifact(t, state.repository, key, []byte("corrupt"), "", 7, artifactSHA([]byte("different")))
		store, registry := newProcessingArtifactCounterStore(t, state.repository)

		_, err := store.(interfaces.ProcessingArtifactBatchStore).GetMany(context.Background(), []types.ProcessingArtifactKey{key, key})
		require.NoError(t, err)
		counters := registry.Snapshot()
		assert.Equal(t, uint64(2), processingArtifactCounterValue(counters, key.Stage, "miss"))
		assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "evicted"))
		assert.Zero(t, processingArtifactCounterValue(counters, key.Stage, "error"))
	})

	t.Run("concurrent", func(t *testing.T) {
		state := newProcessingArtifactTestStore(t)
		key := processingArtifactCounterKey(t, "counter.repair.concurrent", "concurrent")
		seedProcessingArtifact(t, state.repository, key, []byte("corrupt"), "", 7, artifactSHA([]byte("different")))
		entered := make(chan struct{}, 2)
		release := make(chan struct{}, 2)
		store, registry := newProcessingArtifactCounterStore(t, processingArtifactCounterInvalidationBarrierRepository{
			ProcessingArtifactRepository: state.repository,
			entered:                      entered,
			release:                      release,
		})

		var group sync.WaitGroup
		errs := make(chan error, 2)
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				_, _, err := store.Get(context.Background(), key)
				errs <- err
			}()
		}
		<-entered
		<-entered
		release <- struct{}{}
		release <- struct{}{}
		group.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		counters := registry.Snapshot()
		assert.Equal(t, uint64(2), processingArtifactCounterValue(counters, key.Stage, "miss"))
		assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "evicted"))
		assert.Zero(t, processingArtifactCounterValue(counters, key.Stage, "error"))
	})

	t.Run("delete failure", func(t *testing.T) {
		state := newProcessingArtifactTestStore(t)
		key := processingArtifactCounterKey(t, "counter.repair.failure", "failure")
		seedProcessingArtifact(t, state.repository, key, []byte("corrupt"), "", 7, artifactSHA([]byte("different")))
		store, registry := newProcessingArtifactCounterStore(t, &artifactDeleteErrorRepository{
			ProcessingArtifactRepository: state.repository,
			err:                          errors.New("delete failed"),
		})

		_, _, err := store.Get(context.Background(), key)
		require.Error(t, err)
		counters := registry.Snapshot()
		assert.Equal(t, uint64(1), processingArtifactCounterValue(counters, key.Stage, "error"))
		assert.Zero(t, processingArtifactCounterValue(counters, key.Stage, "evicted"))
	})
}

func TestProcessingArtifactCounterSnapshotIsImmutable(t *testing.T) {
	registry := NewProcessingArtifactCounterRegistry()
	registry.Record("chunking", "hit")
	first := registry.Snapshot()
	require.Len(t, first, 1)
	first[0].Count = 0
	second := registry.Snapshot()
	assert.Equal(t, uint64(1), processingArtifactCounterValue(second, "chunking", "hit"))
}

func TestProcessingArtifactCountersRejectInvalidLabels(t *testing.T) {
	registry := NewProcessingArtifactCounterRegistry()
	registry.Record("invalid stage", "hit")
	registry.Record("chunking", "unknown")
	assert.Empty(t, registry.Snapshot())
}

func TestProcessingArtifactCountersAreSafeForConcurrentRecording(t *testing.T) {
	registry := NewProcessingArtifactCounterRegistry()
	const workers = 32
	const recordsPerWorker = 100
	var group sync.WaitGroup
	stopSnapshots := make(chan struct{})
	var snapshots sync.WaitGroup
	snapshots.Add(1)
	go func() {
		defer snapshots.Done()
		for {
			select {
			case <-stopSnapshots:
				return
			default:
				_ = registry.Snapshot()
			}
		}
	}()
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for range recordsPerWorker {
				registry.Record("chunking", "hit")
			}
		}()
	}
	group.Wait()
	close(stopSnapshots)
	snapshots.Wait()
	assert.Equal(t, uint64(workers*recordsPerWorker), processingArtifactCounterValue(registry.Snapshot(), "chunking", "hit"))
}
