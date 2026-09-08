package container

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type learningRecoveryService struct {
	interfaces.LearningService
	started chan struct{}
	stopped chan struct{}
}

func (s *learningRecoveryService) Recover(ctx context.Context) error {
	close(s.started)
	<-ctx.Done()
	close(s.stopped)
	return ctx.Err()
}

type learningCleanup struct {
	interfaces.ResourceCleaner
	clean types.CleanupFunc
}

func (c *learningCleanup) RegisterWithName(_ string, f types.CleanupFunc) { c.clean = f }

func TestLearningRecoveryCancelsAndJoinsOnShutdown(t *testing.T) {
	svc := &learningRecoveryService{started: make(chan struct{}), stopped: make(chan struct{})}
	cleaner := &learningCleanup{}
	startLearningRecovery(svc, cleaner)
	select {
	case <-svc.started:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	require.NotNil(t, cleaner.clean)
	require.NoError(t, cleaner.clean())
	select {
	case <-svc.stopped:
	default:
		t.Fatal("recovery outlived cleanup")
	}
}
