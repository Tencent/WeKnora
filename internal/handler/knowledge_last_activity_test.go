package handler

import (
	"context"
	"errors"
	"fmt"
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
	err    error
	asked  []string
}

func (f *fakeBacklog) QueuedWork(_ context.Context, ids []string) (map[string]bool, error) {
	f.asked = append(f.asked, ids...)
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = f.queued[id]
	}
	return out, nil
}

// Every row quiet past the stall hint gets a verdict, however many there
// are; rows still progressing are not probed.
func TestAttachLastActivityGivesEveryQuietRowAVerdict(t *testing.T) {
	now := time.Now()
	backlog := &fakeBacklog{queued: map[string]bool{"quiet-0": true}}
	h := &KnowledgeHandler{spanRepo: &lastActivitySpanRepo{}, backlog: backlog}
	rows := []*types.Knowledge{{ID: "recent", ParseStatus: types.ParseStatusProcessing, UpdatedAt: now}}
	for i := 0; i < 40; i++ {
		rows = append(rows, &types.Knowledge{
			ID: fmt.Sprintf("quiet-%d", i), ParseStatus: types.ParseStatusProcessing, UpdatedAt: now.Add(-time.Hour),
		})
	}

	h.attachLastActivity(context.Background(), rows)

	assert.Len(t, backlog.asked, 40)
	assert.Empty(t, rows[0].StallState)
	assert.Equal(t, types.StallStateQueued, rows[1].StallState)
	for _, k := range rows[2:] {
		assert.Equal(t, types.StallStateStalled, k.StallState, k.ID)
	}
}

// A failed probe leaves the verdict unknown instead of calling rows stuck.
func TestAttachLastActivityLeavesVerdictUnknownWhenProbeFails(t *testing.T) {
	h := &KnowledgeHandler{
		spanRepo: &lastActivitySpanRepo{},
		backlog:  &fakeBacklog{err: errors.New("redis down")},
	}
	rows := []*types.Knowledge{
		{ID: "quiet", ParseStatus: types.ParseStatusProcessing, UpdatedAt: time.Now().Add(-time.Hour)},
	}

	h.attachLastActivity(context.Background(), rows)

	require.NotNil(t, rows[0].LastActivityAt)
	assert.Empty(t, rows[0].StallState)
}
