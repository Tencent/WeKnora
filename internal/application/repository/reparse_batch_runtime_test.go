package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/logger"
)

// Trace counts both ORM queries and Raw aggregate scans.
type reparseSQLCounter struct {
	logger.Interface
	queries int
}

func (c *reparseSQLCounter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	c.queries++
	c.Interface.Trace(ctx, begin, fc, err)
}

func TestReparseBatchRuntimeAggregationIsBoundedForLargeMembership(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(&types.ReparseBatch{}, &types.ReparseBatchMember{}, &types.ReparseBatchSubmission{}),
	)
	store := NewKnowledgeRepository(db).(*knowledgeRepository).ReparseBatchStore()
	ctx := context.Background()
	ids := make([]string, 27328)
	statuses := []string{"completed", "failed", "pending", "processing", "finalizing", "cancelled", "future_state"}
	rows := make([]map[string]interface{}, 0, len(ids))
	expected := types.ReparseBatchCounts{}
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%05d", i)
		state := i % 10
		if state == 7 {
			expected.Missing++
			continue
		}
		status, tenant := "completed", 1
		var deleted interface{}
		if state < len(statuses) {
			status = statuses[state]
		}
		if state == 8 {
			deleted = time.Now()
			expected.Missing++
		}
		if state == 9 {
			tenant = 2
			expected.Missing++
		}
		switch state {
		case 0:
			expected.Completed++
		case 1:
			expected.Failed++
		case 2:
			expected.Queued++
		case 3:
			expected.Processing++
		case 4:
			expected.Finalizing++
		case 5:
			expected.Cancelled++
		case 6:
			expected.Unknown++
		}
		rows = append(
			rows,
			map[string]interface{}{
				"id":                ids[i],
				"tenant_id":         tenant,
				"knowledge_base_id": "kb-large",
				"type":              "manual",
				"title":             "Synthetic",
				"source":            "manual",
				"parse_status":      status,
				"summary_status":    "completed",
				"deleted_at":        deleted,
			},
		)
	}
	require.NoError(t, db.Table("knowledges").CreateInBatches(rows, 100).Error)
	batch := &types.ReparseBatch{
		ID:              "batch-large",
		TenantID:        1,
		KnowledgeBaseID: "kb-large",
		Mode:            "tracking_only",
		ManifestSHA256:  "large",
	}
	require.NoError(t, store.CreateReparseBatch(ctx, batch, ids))
	counter := &reparseSQLCounter{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = counter
	view, err := store.GetReparseBatchView(ctx, 1, batch.ID)
	require.NoError(t, err)
	require.Equal(t, int64(len(ids)), view.Total)
	require.Equal(t, expected, view.Counts)
	require.Equal(t, int64(len(ids))-expected.Missing, view.SummaryCounts.Completed)
	require.Equal(t, expected.Missing, view.SummaryCounts.Unknown)
	require.Equal(t, 3, counter.queries, "one lookup and two set aggregates, including Raw SQL")
	counter.queries = 0
	page, total, err := store.ListReparseBatchViews(ctx, 1, "kb-large", 0, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, page, 1)
	require.Equal(t, expected, page[0].Counts)
	require.Equal(t, 4, counter.queries, "list/count plus two aggregates independent of member count")
}

func TestReparseBatchRuntimeStatesUseCurrentKnowledge(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(&types.ReparseBatch{}, &types.ReparseBatchMember{}, &types.ReparseBatchSubmission{}),
	)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	store := repo.ReparseBatchStore()
	ctx := context.Background()
	batch := &types.ReparseBatch{
		ID:              "batch-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Mode:            "managed",
		ManifestSHA256:  "m",
	}
	ids := []string{"old-failed", "done", "submit-failed", "missing"}
	require.NoError(t, store.CreateReparseBatch(ctx, batch, ids))
	statuses := map[string]string{
		"old-failed":    types.ParseStatusFailed,
		"done":          types.ParseStatusCompleted,
		"submit-failed": types.ParseStatusPending,
	}
	for id, status := range statuses {
		require.NoError(
			t,
			db.Exec(
				`INSERT INTO knowledges
                 (id, tenant_id, knowledge_base_id, type, title, source, parse_status, summary_status)
                 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				id,
				1,
				"kb-1",
				"file",
				id,
				"test",
				status,
				types.SummaryStatusNone,
			).Error,
		)
	}
	// A pre-existing failure must not be mistaken for a failed new submission.
	require.NoError(t, store.SetReparseMemberState(ctx, batch.ID, "done", "submitted", ""))
	require.NoError(t, store.SetReparseMemberState(ctx, batch.ID, "submit-failed", "failed", "submit error"))
	view, err := store.GetReparseBatchView(ctx, 1, batch.ID)
	require.NoError(t, err)
	require.Equal(t, int64(4), view.Total)
	require.Equal(t, int64(1), view.Counts.WaitingStart)
	require.Equal(t, int64(1), view.Counts.Completed)
	require.Equal(t, int64(1), view.Counts.SubmissionFailed)
	require.Equal(t, int64(1), view.Counts.Missing)
	require.Equal(
		t,
		view.Total,
		view.Counts.WaitingStart+view.Counts.Queued+view.Counts.Processing+
			view.Counts.Finalizing+view.Counts.Completed+view.Counts.Failed+
			view.Counts.Cancelled+view.Counts.Missing+view.Counts.SubmissionFailed+view.Counts.Unknown,
	)
	// Membership remains frozen while a later rebuild changes current state.
	require.NoError(
		t,
		db.Exec(
			"UPDATE knowledges SET parse_status = 'processing', summary_status = 'completed' WHERE id = 'done'",
		).Error,
	)
	view, err = store.GetReparseBatchView(ctx, 1, batch.ID)
	require.NoError(t, err)
	require.Zero(t, view.Counts.Completed)
	require.Equal(t, int64(1), view.Counts.Processing)
	require.Equal(t, int64(1), view.SummaryCounts.Completed)
	require.NoError(t, db.Exec("UPDATE knowledges SET deleted_at = CURRENT_TIMESTAMP WHERE id = 'done'").Error)
	view, err = store.GetReparseBatchView(ctx, 1, batch.ID)
	require.NoError(t, err)
	require.Equal(t, int64(4), view.Total)
	require.Equal(t, int64(2), view.Counts.Missing)
}

func TestReparseBatchMemberViewDoesNotExposeMovedKnowledge(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(
		t,
		db.AutoMigrate(&types.ReparseBatch{}, &types.ReparseBatchMember{}, &types.ReparseBatchSubmission{}),
	)
	store := NewKnowledgeRepository(db).(*knowledgeRepository).ReparseBatchStore()
	batch := &types.ReparseBatch{
		ID:              "batch-2",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Mode:            "tracking_only",
		ManifestSHA256:  "m2",
	}
	require.NoError(t, store.CreateReparseBatch(context.Background(), batch, []string{"moved"}))
	require.NoError(
		t,
		db.Exec(
			`INSERT INTO knowledges
             (id, tenant_id, knowledge_base_id, type, title, source, parse_status)
             VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"moved",
			1,
			"other-kb",
			"file",
			"secret",
			"test",
			types.ParseStatusCompleted,
		).Error,
	)
	items, total, err := store.ListReparseBatchMemberViews(context.Background(), 1, batch.ID, "missing", 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	require.Equal(t, "missing", items[0].State)
	require.Empty(t, items[0].Title)
}
