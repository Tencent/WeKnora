package container

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type retryEnvTestRegistry struct {
	interfaces.RetrieveEngineRegistry
	mu      sync.Mutex
	engines map[types.RetrieverEngineType]interfaces.RetrieveEngineService
}

func (r *retryEnvTestRegistry) Register(svc interfaces.RetrieveEngineService) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.engines[svc.EngineType()]; exists {
		return errors.New("already registered")
	}
	r.engines[svc.EngineType()] = svc
	return nil
}

func (r *retryEnvTestRegistry) GetRetrieveEngineService(
	engineType types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	svc, exists := r.engines[engineType]
	if !exists {
		return nil, errors.New("not found")
	}
	return svc, nil
}

type retryEnvTestEngine struct {
	interfaces.RetrieveEngineService
	typ types.RetrieverEngineType
}

func (e *retryEnvTestEngine) EngineType() types.RetrieverEngineType { return e.typ }

func testRetryLogger() *logrus.Entry {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return logrus.NewEntry(log)
}

func TestRetryRegisterEnvEngineRecovers(t *testing.T) {
	registry := &retryEnvTestRegistry{
		engines: make(map[types.RetrieverEngineType]interfaces.RetrieveEngineService),
	}
	attempts := 0
	build := func(context.Context) (interfaces.RetrieveEngineService, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("milvus still starting")
		}
		return &retryEnvTestEngine{typ: types.MilvusRetrieverEngineType}, nil
	}

	retryRegisterEnvEngine(
		registry, types.MilvusRetrieverEngineType, build,
		testRetryLogger(), time.Millisecond, 2*time.Millisecond, 5,
	)

	require.Equal(t, 3, attempts)
	_, err := registry.GetRetrieveEngineService(types.MilvusRetrieverEngineType)
	require.NoError(t, err)
}

func TestRetryRegisterEnvEngineStopsWhenAnotherPathRegisteredIt(t *testing.T) {
	registry := &retryEnvTestRegistry{
		engines: map[types.RetrieverEngineType]interfaces.RetrieveEngineService{
			types.MilvusRetrieverEngineType: &retryEnvTestEngine{typ: types.MilvusRetrieverEngineType},
		},
	}
	build := func(context.Context) (interfaces.RetrieveEngineService, error) {
		t.Fatal("builder must not run when the engine is already registered")
		return nil, nil
	}

	retryRegisterEnvEngine(
		registry, types.MilvusRetrieverEngineType, build,
		testRetryLogger(), time.Millisecond, time.Millisecond, 2,
	)
}

func TestRetryRegisterEnvEngineGivesUpAfterAttempts(t *testing.T) {
	registry := &retryEnvTestRegistry{
		engines: make(map[types.RetrieverEngineType]interfaces.RetrieveEngineService),
	}
	attempts := 0
	build := func(context.Context) (interfaces.RetrieveEngineService, error) {
		attempts++
		return nil, errors.New("still unavailable")
	}

	retryRegisterEnvEngine(
		registry, types.MilvusRetrieverEngineType, build,
		testRetryLogger(), time.Millisecond, 2*time.Millisecond, 3,
	)

	require.Equal(t, 3, attempts)
	_, err := registry.GetRetrieveEngineService(types.MilvusRetrieverEngineType)
	require.Error(t, err)
}
