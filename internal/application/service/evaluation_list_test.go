package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registerListFixtures(
	repository *fakeEvaluationTaskRepository,
	tenantID uint64,
	count int,
) []*types.EvaluationTaskEntity {
	sharedStart := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	entities := make([]*types.EvaluationTaskEntity, 0, count)
	for i := 0; i < count; i++ {
		entity := newPersistentLifecycleEntity(tenantID, fmt.Sprintf("list-%02d", i))
		entity.Status = types.EvaluationStatueSuccess
		entity.StartTime = sharedStart
		entity.HeartbeatAt = sharedStart
		endTime := sharedStart.Add(time.Minute)
		entity.EndTime = &endTime
		entity.LeaseExpiresAt = nil
		repository.register(entity)
		entities = append(entities, entity)
	}
	return entities
}

func TestListEvaluationsPaginatesWithoutDuplicateOrGap(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	registerListFixtures(repository, 91, 5)
	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: "list-owner"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(91))

	seen := make([]string, 0, 5)
	cursor := ""
	for page := 0; page < 4; page++ {
		result, err := service.ListEvaluations(ctx, types.EvaluationTaskListInput{PageSize: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, task := range result.Items {
			seen = append(seen, task.ID)
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	assert.Equal(t,
		[]string{"list-04", "list-03", "list-02", "list-01", "list-00"},
		seen,
		"keyset walk must cover every task once in (start_time DESC, id DESC) order",
	)
}

func TestListEvaluationsFiltersFrozenSourcesForRestrictedAPIKey(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entities := registerListFixtures(repository, 91, 5)
	for index, entity := range entities {
		source := "kb-denied"
		if index == 0 || index == 1 || index == 3 {
			source = "kb-allowed"
		}
		frozen := evaluationTaskWithSourceKnowledgeBase(t, source)
		entity.ExperimentSnapshot = frozen.ExperimentSnapshot
		entity.DatasetVersionID = frozen.DatasetVersionID
		entity.DatasetContentSHA256 = frozen.DatasetContentSHA256
		entity.ExperimentSHA256 = frozen.ExperimentSHA256
		repository.register(entity)
	}
	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: "list-owner"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(91))
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})

	first, err := service.ListEvaluations(ctx, types.EvaluationTaskListInput{PageSize: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	assert.Equal(t, []string{"list-03", "list-01"},
		[]string{first.Items[0].ID, first.Items[1].ID})
	require.NotEmpty(t, first.NextCursor)

	second, err := service.ListEvaluations(ctx, types.EvaluationTaskListInput{
		PageSize: 2, Cursor: first.NextCursor,
	})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.Equal(t, "list-00", second.Items[0].ID)
	assert.Empty(t, second.NextCursor)
}

func TestListEvaluationsDefaultsAndClampsPageSize(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: "list-owner"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(91))

	_, err := service.ListEvaluations(ctx, types.EvaluationTaskListInput{})
	require.NoError(t, err)
	assert.Equal(t, evaluationDefaultListPageSize+1, repository.lastListLimit)

	_, err = service.ListEvaluations(ctx, types.EvaluationTaskListInput{PageSize: 500})
	require.NoError(t, err)
	assert.Equal(t, evaluationMaxListPageSize+1, repository.lastListLimit)
}

func TestListEvaluationsRejectsInvalidCursors(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: "list-owner"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(91))

	encode := func(payload string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(payload))
	}
	for _, encoded := range []string{
		"not-base64!!!",
		encode("not-json"),
		encode(`{"v":99,"start_time":"2026-08-28T09:00:00Z","id":"x","filter":"f"}`),
		encode(`{"v":1,"start_time":"0001-01-01T00:00:00Z","id":"x","filter":"f"}`),
	} {
		_, err := service.ListEvaluations(ctx, types.EvaluationTaskListInput{Cursor: encoded})
		require.ErrorIs(t, err, ErrEvaluationTaskListInvalidCursor, "cursor %q", encoded)
	}
}

func TestListEvaluationsRejectsCrossFilterCursor(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	registerListFixtures(repository, 91, 3)
	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: "list-owner"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(91))

	status := types.EvaluationStatueSuccess
	first, err := service.ListEvaluations(ctx, types.EvaluationTaskListInput{Status: &status, PageSize: 2})
	require.NoError(t, err)
	require.NotEmpty(t, first.NextCursor)

	// Reusing the cursor after dropping the status filter is a client error.
	_, err = service.ListEvaluations(ctx, types.EvaluationTaskListInput{Cursor: first.NextCursor})
	require.ErrorIs(t, err, ErrEvaluationTaskListInvalidCursor)

	// The same filter accepts the cursor and continues the walk.
	second, err := service.ListEvaluations(
		ctx,
		types.EvaluationTaskListInput{Status: &status, PageSize: 2, Cursor: first.NextCursor},
	)
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.Empty(t, second.NextCursor)
}

func TestListEvaluationsRejectsUnsupportedStatus(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: "list-owner"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(91))

	unknown := types.EvaluationStatue(42)
	_, err := service.ListEvaluations(ctx, types.EvaluationTaskListInput{Status: &unknown})
	require.ErrorIs(t, err, ErrEvaluationTaskListInvalidCursor)
}

func TestReplaceEvaluationTaskLabelsNormalizesAndStoresFullSet(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(92, "labels")
	repository.register(entity)
	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: "list-owner"}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(92))

	labels, err := service.ReplaceEvaluationTaskLabels(ctx, entity.ID, []string{" Baseline ", "检索"})
	require.NoError(t, err)
	assert.Equal(t, []string{"baseline", "检索"}, labels)

	stored, err := repository.ListTaskLabels(ctx, entity.TenantID, []string{entity.ID})
	require.NoError(t, err)
	assert.Equal(t, labels, stored[entity.ID])
}
