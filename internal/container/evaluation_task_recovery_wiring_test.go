package container

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluationTaskRecoveryWiringRepository struct {
	interfaces.EvaluationTaskRepository
	claimEntered chan struct{}
}

func (r *evaluationTaskRecoveryWiringRepository) ClaimExpiredTasks(
	context.Context,
	types.EvaluationTaskClaimExpiredCommand,
) ([]*types.EvaluationTaskEntity, error) {
	select {
	case r.claimEntered <- struct{}{}:
	default:
	}
	return nil, nil
}

type evaluationTaskRecoveryWiringKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
}

type evaluationTaskRecoveryWiringKnowledgeService struct {
	interfaces.KnowledgeService
}

type evaluationTaskRecoveryWiringTenantService struct {
	interfaces.TenantService
}

type evaluationTaskRecoveryWiringCleaner struct {
	mu       sync.Mutex
	names    []string
	cleanups []types.CleanupFunc
}

func (c *evaluationTaskRecoveryWiringCleaner) Register(cleanup types.CleanupFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanups = append(c.cleanups, cleanup)
}

func (c *evaluationTaskRecoveryWiringCleaner) RegisterWithName(name string, cleanup types.CleanupFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names = append(c.names, name)
	c.cleanups = append(c.cleanups, cleanup)
}

func (c *evaluationTaskRecoveryWiringCleaner) Cleanup(ctx context.Context) []error {
	c.mu.Lock()
	cleanups := append([]types.CleanupFunc(nil), c.cleanups...)
	c.mu.Unlock()

	var errs []error
	for i := len(cleanups) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			return append(errs, ctx.Err())
		default:
		}
		if err := cleanups[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func TestEvaluationTaskRecoveryRunnerWiringStartsAndRegistersShutdown(t *testing.T) {
	repository := &evaluationTaskRecoveryWiringRepository{claimEntered: make(chan struct{}, 1)}
	runner := service.NewEvaluationTaskRecoveryRunner(
		repository,
		&evaluationTaskRecoveryWiringTenantService{},
		&evaluationTaskRecoveryWiringKnowledgeBaseService{},
		&evaluationTaskRecoveryWiringKnowledgeService{},
	)
	cleaner := &evaluationTaskRecoveryWiringCleaner{}

	startEvaluationTaskRecovery(runner, cleaner)
	select {
	case <-repository.claimEntered:
	case <-time.After(time.Second):
		t.Fatal("evaluation task recovery runner did not scan during container startup")
	}

	cleaner.mu.Lock()
	names := append([]string(nil), cleaner.names...)
	cleaner.mu.Unlock()
	assert.Equal(t, []string{"EvaluationTaskRecoveryRunner"}, names)
	require.Empty(t, cleaner.Cleanup(context.Background()))
}
