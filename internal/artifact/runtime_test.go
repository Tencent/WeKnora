package artifact

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryArtifactRepository struct {
	mu      sync.Mutex
	values  map[types.ProcessingArtifactLookup]*types.ProcessingArtifact
	getErr  error
	putErr  error
	deleted int
}

func newMemoryArtifactRepository() *memoryArtifactRepository {
	return &memoryArtifactRepository{
		values: make(map[types.ProcessingArtifactLookup]*types.ProcessingArtifact),
	}
}

func (r *memoryArtifactRepository) Get(
	_ context.Context,
	key types.ProcessingArtifactLookup,
) (*types.ProcessingArtifact, error) {
	values, err := r.BatchGet(context.Background(), []types.ProcessingArtifactLookup{key})
	if err != nil {
		return nil, err
	}
	value, ok := values[key]
	if !ok {
		return nil, types.ErrProcessingArtifactNotFound
	}
	return value, nil
}

func (r *memoryArtifactRepository) BatchGet(
	_ context.Context,
	keys []types.ProcessingArtifactLookup,
) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, r.getErr
	}
	result := make(map[types.ProcessingArtifactLookup]*types.ProcessingArtifact)
	for _, key := range keys {
		if value := r.values[key]; value != nil {
			copy := *value
			copy.Payload = append([]byte(nil), value.Payload...)
			result[key] = &copy
		}
	}
	return result, nil
}

func (r *memoryArtifactRepository) PutIfAbsent(
	_ context.Context,
	candidate *types.ProcessingArtifact,
) (*types.ProcessingArtifact, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.putErr != nil {
		return nil, false, r.putErr
	}
	if winner := r.values[candidate.Lookup()]; winner != nil {
		copy := *winner
		copy.Payload = append([]byte(nil), winner.Payload...)
		return &copy, false, nil
	}
	copy := *candidate
	copy.Payload = append([]byte(nil), candidate.Payload...)
	r.values[candidate.Lookup()] = &copy
	return candidate, true, nil
}

func (r *memoryArtifactRepository) PutManyIfAbsent(
	ctx context.Context,
	candidates []*types.ProcessingArtifact,
) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error) {
	for _, candidate := range candidates {
		if _, _, err := r.PutIfAbsent(ctx, candidate); err != nil {
			return nil, err
		}
	}
	keys := make([]types.ProcessingArtifactLookup, 0, len(candidates))
	for _, candidate := range candidates {
		keys = append(keys, candidate.Lookup())
	}
	return r.BatchGet(ctx, keys)
}

func (r *memoryArtifactRepository) DeleteCorrupt(
	_ context.Context,
	key types.ProcessingArtifactLookup,
	observedChecksum string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if value := r.values[key]; value != nil && value.PayloadChecksum == observedChecksum {
		delete(r.values, key)
		r.deleted++
	}
	return nil
}

func (r *memoryArtifactRepository) TouchHits(context.Context, []types.ProcessingArtifactLookup) error {
	return nil
}

func testExpected(t *testing.T) Expected {
	t.Helper()
	key, err := BuildKey(1, testKeyMaterial())
	require.NoError(t, err)
	return Expected{
		Key:   key,
		Codec: CodecJSONV1,
		Validate: func(payload []byte) error {
			if string(payload) != `{"value":"computed"}` {
				return errors.New("unexpected payload")
			}
			return nil
		},
	}
}

func TestRuntimeStoreFailureIsFailOpen(t *testing.T) {
	repository := newMemoryArtifactRepository()
	repository.getErr = errors.New("database down")
	runtime := NewRuntime(repository, nil)

	value, err := runtime.LoadOrCompute(context.Background(), testExpected(t), func(context.Context) ([]byte, error) {
		return []byte(`{"value":"computed"}`), nil
	})
	require.NoError(t, err)
	assert.Equal(t, `{"value":"computed"}`, string(value.Payload))
	assert.False(t, value.CacheHit)
}

func TestRuntimeCorruptEntrySelfHeals(t *testing.T) {
	repository := newMemoryArtifactRepository()
	expected := testExpected(t)
	corrupt, err := NewInlineArtifact(expected.Key, CodecJSONV1, []byte(`{"value":"computed"}`))
	require.NoError(t, err)
	corrupt.PayloadChecksum = SHA256Hex([]byte("wrong"))
	repository.values[expected.Key.Lookup] = corrupt
	runtime := NewRuntime(repository, nil)

	value, err := runtime.LoadOrCompute(context.Background(), expected, func(context.Context) ([]byte, error) {
		return []byte(`{"value":"computed"}`), nil
	})
	require.NoError(t, err)
	assert.Equal(t, `{"value":"computed"}`, string(value.Payload))
	assert.Equal(t, 1, repository.deleted)
}

func TestRuntimeSingleflightComputesOnce(t *testing.T) {
	repository := newMemoryArtifactRepository()
	runtime := NewRuntime(repository, nil)
	expected := testExpected(t)
	var calls atomic.Int32

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	for index := 0; index < workers; index++ {
		go func() {
			<-start
			_, err := runtime.LoadOrCompute(context.Background(), expected, func(context.Context) ([]byte, error) {
				calls.Add(1)
				time.Sleep(10 * time.Millisecond)
				return []byte(`{"value":"computed"}`), nil
			})
			results <- err
		}()
	}
	close(start)
	for index := 0; index < workers; index++ {
		require.NoError(t, <-results)
	}
	assert.Equal(t, int32(1), calls.Load())
}

func TestRuntimeRedisLeaseSuppressesCrossProcessDuplicateCompute(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	repository := newMemoryArtifactRepository()
	firstRuntime := NewRuntime(repository, nil)
	secondRuntime := NewRuntime(repository, nil)
	firstRuntime.ConfigureLease(NewRedisLease(client))
	secondRuntime.ConfigureLease(NewRedisLease(client))
	expected := testExpected(t)
	var calls atomic.Int32

	start := make(chan struct{})
	results := make(chan error, 2)
	run := func(runtime *Runtime) {
		<-start
		_, err := runtime.LoadOrCompute(
			context.Background(),
			expected,
			func(context.Context) ([]byte, error) {
				calls.Add(1)
				time.Sleep(75 * time.Millisecond)
				return []byte(`{"value":"computed"}`), nil
			},
		)
		results <- err
	}
	go run(firstRuntime)
	go run(secondRuntime)
	close(start)

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	assert.Equal(t, int32(1), calls.Load())
}
