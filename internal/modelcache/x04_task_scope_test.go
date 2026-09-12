package modelcache

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/modelobs"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type x04BlockedEmbedder struct {
	countingEmbedder
	started chan struct{}
	release chan struct{}
}

func (e *x04BlockedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	e.started <- struct{}{}
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return e.countingEmbedder.BatchEmbed(ctx, texts)
}

func TestX04StrictTasksDoNotShareProviderOwnership(t *testing.T) {
	provider := &x04BlockedEmbedder{started: make(chan struct{}, 2), release: make(chan struct{})}
	wrapped := NewCoordinator(&cacheStore{}).Wrap(&types.Model{ID: "x04-model", TenantID: 7}, provider)
	done := make(chan error, 2)
	for _, task := range []string{"x04-task-a", "x04-task-b"} {
		ctx := modelobs.WithEvaluationTask(
			modelobs.WithPurpose(context.Background(), modelobs.PurposeEvaluation, true), task,
		)
		go func() { _, err := wrapped.BatchEmbed(ctx, []string{"same"}); done <- err }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-provider.started:
		case <-time.After(time.Second):
			close(provider.release)
			t.Fatal("strict tasks incorrectly shared a provider call")
		}
	}
	close(provider.release)
	for i := 0; i < 2; i++ {
		require.NoError(t, <-done)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	require.Len(t, provider.batchInputs, 2)
}
