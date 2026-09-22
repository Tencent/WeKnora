package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type lastActivitySpanRepo struct {
	repository.KnowledgeSpanRepository
	activity map[string]time.Time
	err      error
	asked    []string
}

func (r *lastActivitySpanRepo) LastActivity(_ context.Context, ids []string) (map[string]time.Time, error) {
	r.asked = ids
	return r.activity, r.err
}

// Only in-flight rows get last_activity_at, and a span write newer than the
// row's updated_at wins.
func TestAttachLastActivity(t *testing.T) {
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	spans := &lastActivitySpanRepo{activity: map[string]time.Time{
		"processing": base.Add(10 * time.Minute),
		"finalizing": base.Add(-time.Hour),
	}}
	h := &KnowledgeHandler{spanRepo: spans}
	rows := []*types.Knowledge{
		{ID: "processing", ParseStatus: types.ParseStatusProcessing, UpdatedAt: base},
		{ID: "finalizing", ParseStatus: types.ParseStatusFinalizing, UpdatedAt: base},
		{ID: "pending", ParseStatus: types.ParseStatusPending, UpdatedAt: base},
		{ID: "completed", ParseStatus: types.ParseStatusCompleted, UpdatedAt: base},
		nil,
	}

	h.attachLastActivity(context.Background(), rows)

	assert.Equal(t, []string{"processing", "finalizing", "pending"}, spans.asked)
	require.NotNil(t, rows[0].LastActivityAt)
	assert.Equal(t, base.Add(10*time.Minute), *rows[0].LastActivityAt)
	require.NotNil(t, rows[1].LastActivityAt)
	assert.Equal(t, base, *rows[1].LastActivityAt)
	require.NotNil(t, rows[2].LastActivityAt)
	assert.Equal(t, base, *rows[2].LastActivityAt)
	assert.Nil(t, rows[3].LastActivityAt)
}

// A failed span lookup still reports the row's own updated_at.
func TestAttachLastActivityFallsBackToUpdatedAt(t *testing.T) {
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	h := &KnowledgeHandler{spanRepo: &lastActivitySpanRepo{err: errors.New("db down")}}
	rows := []*types.Knowledge{{ID: "k", ParseStatus: types.ParseStatusProcessing, UpdatedAt: base}}

	h.attachLastActivity(context.Background(), rows)

	require.NotNil(t, rows[0].LastActivityAt)
	assert.Equal(t, base, *rows[0].LastActivityAt)
}

func TestSpansLastActivity(t *testing.T) {
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	rows := []types.KnowledgeProcessingSpan{
		{UpdatedAt: base.Add(-time.Minute)},
		{UpdatedAt: base.Add(5 * time.Minute)},
	}
	assert.Equal(t, base.Add(5*time.Minute), spansLastActivity(base, rows))
	assert.Equal(t, base, spansLastActivity(base, nil))
}

type fakeBacklog struct {
	queued map[string]bool
	asked  []string
}

func (f *fakeBacklog) QueuedWork(_ context.Context, ids []string) map[string]bool {
	f.asked = append(f.asked, ids...)
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = f.queued[id]
	}
	return out
}

// Only rows quiet past the stall hint are probed, and a backlogged one is
// told apart from a stuck one.
func TestAttachLastActivityFlagsBackloggedRows(t *testing.T) {
	now := time.Now()
	backlog := &fakeBacklog{queued: map[string]bool{"quiet-queued": true}}
	h := &KnowledgeHandler{spanRepo: &lastActivitySpanRepo{}, backlog: backlog}
	rows := []*types.Knowledge{
		{ID: "recent", ParseStatus: types.ParseStatusProcessing, UpdatedAt: now.Add(-time.Minute)},
		{ID: "quiet-queued", ParseStatus: types.ParseStatusFinalizing, UpdatedAt: now.Add(-time.Hour)},
		{ID: "quiet-stuck", ParseStatus: types.ParseStatusProcessing, UpdatedAt: now.Add(-time.Hour)},
	}

	h.attachLastActivity(context.Background(), rows)

	assert.ElementsMatch(t, []string{"quiet-queued", "quiet-stuck"}, backlog.asked)
	assert.False(t, rows[0].WaitingInQueue)
	assert.True(t, rows[1].WaitingInQueue)
	assert.False(t, rows[2].WaitingInQueue)
}
