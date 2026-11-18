package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type documentCancelAttemptRepo struct {
	*documentTestKnowledge
	afterUpdate func()
}

func (r *documentCancelAttemptRepo) UpdateKnowledgeColumns(
	ctx context.Context, id string, values map[string]interface{},
) error {
	if err := r.documentTestKnowledge.UpdateKnowledgeColumns(ctx, id, values); err != nil {
		return err
	}
	r.afterUpdate()
	return nil
}

type documentCancelAttemptTracker struct {
	SpanTracker
	latest  int
	aborted []int
}

func (t *documentCancelAttemptTracker) LatestAttempt(context.Context, string) int { return t.latest }
func (t *documentCancelAttemptTracker) AbortAttempt(_ context.Context, _ string, attempt int, _, _, _ string) {
	t.aborted = append(t.aborted, attempt)
}

func TestDocumentCancellationClosesCapturedAttempt(t *testing.T) {
	s, _, _, _ := documentTestService()
	tracker := &documentCancelAttemptTracker{latest: 7}
	s.spanTracker = tracker
	s.repo = &documentCancelAttemptRepo{
		documentTestKnowledge: s.repo.(*documentTestKnowledge),
		afterUpdate:           func() { tracker.latest = 8 },
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	_, err := s.CancelKnowledgeParse(ctx, "doc")
	require.NoError(t, err)
	require.Equal(t, []int{7}, tracker.aborted, "a new root opened after cancellation must not be aborted")
}
