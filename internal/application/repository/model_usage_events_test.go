package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newModelUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ModelUsageEvent{}))
	require.NoError(t, db.Exec(`
		CREATE TABLE models (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			display_name TEXT NOT NULL DEFAULT '',
			is_builtin INTEGER NOT NULL DEFAULT 0,
			deleted_at DATETIME NULL
		)
	`).Error)
	return db
}

func createModelUsageEvents(t *testing.T, db *gorm.DB, count int, event types.ModelUsageEvent) {
	t.Helper()
	events := make([]types.ModelUsageEvent, 0, count)
	for i := 0; i < count; i++ {
		events = append(events, event)
	}
	require.NoError(t, db.Create(&events).Error)
}

func TestModelUsageReportReturnsEmptySlices(t *testing.T) {
	db := newModelUsageTestDB(t)
	repo := NewModelUsageRepository(db)
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	report, err := repo.Report(context.Background(), 1, types.ModelUsageQuery{
		Start:      now.Add(-time.Hour),
		End:        now,
		BucketSize: time.Minute,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.NotNil(t, report.Models)
	assert.NotNil(t, report.Timeline)
	assert.NotNil(t, report.RecentEvents)
	assert.Empty(t, report.Models)
	assert.Empty(t, report.Timeline)
	assert.Empty(t, report.RecentEvents)
}

func TestModelUsageReportFiltersTenantAndAggregates(t *testing.T) {
	db := newModelUsageTestDB(t)
	repo := NewModelUsageRepository(db)
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	createModelUsageEvents(t, db, 100, types.ModelUsageEvent{
		TenantID:         1,
		ModelID:          "model-chat",
		ModelName:        "gpt-test",
		ModelType:        types.ModelTypeKnowledgeQA,
		ModelSource:      types.ModelSourceRemote,
		Provider:         "openai",
		RequestKind:      "chat.completion",
		UsageSource:      types.ModelUsageSourceProvider,
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		InputItems:       1,
		Success:          true,
		CreatedAt:        now.Add(-time.Minute),
	})
	require.NoError(t, db.Create(&types.ModelUsageEvent{
		TenantID:    2,
		ModelID:     "other-tenant-model",
		ModelName:   "other",
		ModelType:   types.ModelTypeKnowledgeQA,
		TotalTokens: 999,
		Success:     true,
		CreatedAt:   now.Add(-time.Minute),
	}).Error)

	report, err := repo.Report(context.Background(), 1, types.ModelUsageQuery{
		Start:      now.Add(-time.Hour),
		End:        now,
		BucketSize: time.Hour,
	})
	require.NoError(t, err)
	require.Len(t, report.Models, 1)
	assert.Equal(t, int64(100), report.Summary.TotalCalls)
	assert.Equal(t, int64(1500), report.Summary.TotalTokens)
	assert.Equal(t, "model-chat", report.Models[0].ModelID)
	assert.Equal(t, int64(1500), report.Models[0].TotalTokens)
	assert.Equal(t, float64(1), report.Models[0].SuccessRate)
	require.Len(t, report.Timeline, 1)
	assert.Equal(t, int64(100), report.Timeline[0].Calls)
	assert.Equal(t, int64(1500), report.Timeline[0].TotalTokens)
	assert.Len(t, report.RecentEvents, 30)
}

func TestModelUsageTimelineSkipsBucketsBelowCallThreshold(t *testing.T) {
	db := newModelUsageTestDB(t)
	repo := NewModelUsageRepository(db)
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	baseEvent := types.ModelUsageEvent{
		TenantID:         1,
		ModelID:          "model-chat",
		ModelName:        "gpt-test",
		ModelType:        types.ModelTypeKnowledgeQA,
		ModelSource:      types.ModelSourceRemote,
		Provider:         "openai",
		RequestKind:      "chat.completion",
		UsageSource:      types.ModelUsageSourceProvider,
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		InputItems:       1,
		Success:          true,
	}

	smallBucketEvent := baseEvent
	smallBucketEvent.CreatedAt = now.Add(-90 * time.Minute)
	createModelUsageEvents(t, db, 99, smallBucketEvent)

	visibleBucketEvent := baseEvent
	visibleBucketEvent.CreatedAt = now.Add(-10 * time.Minute)
	createModelUsageEvents(t, db, 100, visibleBucketEvent)

	report, err := repo.Report(context.Background(), 1, types.ModelUsageQuery{
		Start:      now.Add(-2 * time.Hour),
		End:        now,
		BucketSize: time.Hour,
	})
	require.NoError(t, err)
	require.Len(t, report.Timeline, 1)
	assert.Equal(t, int64(100), report.Timeline[0].Calls)
	assert.Equal(t, now.Add(-time.Hour).Truncate(time.Hour), report.Timeline[0].BucketStart)
}
