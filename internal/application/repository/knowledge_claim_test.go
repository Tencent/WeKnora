package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func knowledgeProcessingClaimer(t *testing.T, db *gorm.DB) interfaces.KnowledgeProcessingClaimer {
	t.Helper()
	repo := NewKnowledgeRepository(db)
	claimer, ok := repo.(interfaces.KnowledgeProcessingClaimer)
	require.True(t, ok, "production knowledge repository must implement KnowledgeProcessingClaimer")
	return claimer
}

func setKnowledgeClaimState(t *testing.T, db *gorm.DB, id, enableStatus string, pendingSubtasks int) {
	t.Helper()
	require.NoError(t, db.Exec(`
		UPDATE knowledges
		SET enable_status = ?, pending_subtasks_count = ?
		WHERE id = ?
	`, enableStatus, pendingSubtasks, id).Error)
}

func reloadKnowledgeClaimState(
	t *testing.T,
	db *gorm.DB,
	id string,
) (parseStatus, enableStatus string, pendingSubtasks int) {
	t.Helper()
	row := db.Raw(`
		SELECT parse_status, enable_status, pending_subtasks_count
		FROM knowledges
		WHERE id = ?
	`, id).Row()
	require.NoError(t, row.Scan(&parseStatus, &enableStatus, &pendingSubtasks))
	return parseStatus, enableStatus, pendingSubtasks
}

func TestClaimKnowledgeProcessing_ClaimsExpectedActiveRow(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	claimer := knowledgeProcessingClaimer(t, db)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	setKnowledgeClaimState(t, db, id, "enabled", 7)

	claimed, err := claimer.ClaimKnowledgeProcessing(
		context.Background(), id, types.ParseStatusCompleted, time.Time{}, nil,
	)
	require.NoError(t, err)
	require.True(t, claimed)

	parseStatus, enableStatus, pendingSubtasks := reloadKnowledgeClaimState(t, db, id)
	assert.Equal(t, types.ParseStatusPending, parseStatus)
	assert.Equal(t, "disabled", enableStatus)
	assert.Zero(t, pendingSubtasks)
}

func TestClaimKnowledgeProcessing_RejectsStaleExpectedStatus(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	claimer := knowledgeProcessingClaimer(t, db)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	setKnowledgeClaimState(t, db, id, "enabled", 7)

	claimed, err := claimer.ClaimKnowledgeProcessing(
		context.Background(), id, types.ParseStatusFailed, time.Time{}, nil,
	)
	require.NoError(t, err)
	assert.False(t, claimed)

	parseStatus, enableStatus, pendingSubtasks := reloadKnowledgeClaimState(t, db, id)
	assert.Equal(t, types.ParseStatusCompleted, parseStatus)
	assert.Equal(t, "enabled", enableStatus)
	assert.Equal(t, 7, pendingSubtasks)
}

func TestClaimKnowledgeProcessing_RejectsStaleUpdatedAtAndPersistsValues(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	claimer := knowledgeProcessingClaimer(t, db)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	observedUpdatedAt := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET updated_at = ? WHERE id = ?", observedUpdatedAt, id,
	).Error)

	claimed, err := claimer.ClaimKnowledgeProcessing(
		context.Background(), id, types.ParseStatusCompleted,
		observedUpdatedAt.Add(-time.Second), map[string]interface{}{"description": "stale"},
	)
	require.NoError(t, err)
	assert.False(t, claimed)

	claimed, err = claimer.ClaimKnowledgeProcessing(
		context.Background(), id, types.ParseStatusCompleted,
		observedUpdatedAt, map[string]interface{}{"description": "accepted"},
	)
	require.NoError(t, err)
	require.True(t, claimed)

	var description, status string
	require.NoError(t, db.Raw(
		"SELECT description, parse_status FROM knowledges WHERE id = ?", id,
	).Row().Scan(&description, &status))
	assert.Equal(t, "accepted", description)
	assert.Equal(t, types.ParseStatusPending, status)
}

func TestClaimKnowledgeProcessing_RejectsSoftDeletedRow(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	claimer := knowledgeProcessingClaimer(t, db)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, true)
	setKnowledgeClaimState(t, db, id, "enabled", 7)

	claimed, err := claimer.ClaimKnowledgeProcessing(
		context.Background(), id, types.ParseStatusCompleted, time.Time{}, nil,
	)
	require.NoError(t, err)
	assert.False(t, claimed)

	parseStatus, enableStatus, pendingSubtasks := reloadKnowledgeClaimState(t, db, id)
	assert.Equal(t, types.ParseStatusCompleted, parseStatus)
	assert.Equal(t, "enabled", enableStatus)
	assert.Equal(t, 7, pendingSubtasks)
}

func TestClaimKnowledgeProcessing_ConcurrentExactlyOneWinner(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	claimer := knowledgeProcessingClaimer(t, db)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	setKnowledgeClaimState(t, db, id, "enabled", 7)

	const callers = 20
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := claimer.ClaimKnowledgeProcessing(
				context.Background(), id, types.ParseStatusCompleted, time.Time{}, nil,
			)
			if err != nil {
				t.Errorf("ClaimKnowledgeProcessing: %v", err)
				return
			}
			if claimed {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), winners.Load())
	parseStatus, enableStatus, pendingSubtasks := reloadKnowledgeClaimState(t, db, id)
	assert.Equal(t, types.ParseStatusPending, parseStatus)
	assert.Equal(t, "disabled", enableStatus)
	assert.Zero(t, pendingSubtasks)
}

func TestKnowledgeAttemptUpdaterRejectsStaleAndCancelledAttempts(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	updater, ok := repo.(interfaces.KnowledgeAttemptUpdater)
	require.True(t, ok, "production knowledge repository must implement KnowledgeAttemptUpdater")
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusPending, false)
	insertKnowledgeAttempt(t, db, id, 1)
	insertKnowledgeAttempt(t, db, id, 2)

	claimed, err := updater.ClaimKnowledgeAttemptProcessing(context.Background(), id, 1)
	require.NoError(t, err)
	assert.False(t, claimed, "an older worker must not claim the knowledge row")

	claimed, err = updater.ClaimKnowledgeAttemptProcessing(context.Background(), id, 2)
	require.NoError(t, err)
	require.True(t, claimed)

	updated, err := updater.UpdateKnowledgeColumnsForAttempt(
		context.Background(), id, 1, map[string]interface{}{"description": "stale"},
	)
	require.NoError(t, err)
	assert.False(t, updated)

	updated, err = updater.UpdateKnowledgeColumnsForAttempt(
		context.Background(), id, 2, map[string]interface{}{"description": "current"},
	)
	require.NoError(t, err)
	require.True(t, updated)

	require.NoError(t, db.Exec(
		"UPDATE knowledges SET parse_status = ? WHERE id = ?", types.ParseStatusCancelled, id,
	).Error)
	updated, err = updater.UpdateKnowledgeColumnsForAttempt(
		context.Background(), id, 2, map[string]interface{}{"description": "must-not-win"},
	)
	require.NoError(t, err)
	assert.False(t, updated, "cancelled knowledge must reject worker writes")

	var description string
	require.NoError(t, db.Raw(
		"SELECT description FROM knowledges WHERE id = ?", id,
	).Row().Scan(&description))
	assert.Equal(t, "current", description)
}

func TestKnowledgeAttemptMutationGuardRunsOnlyForCurrentAttempt(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	guard, ok := repo.(interfaces.KnowledgeAttemptMutationGuard)
	require.True(t, ok, "production repository must guard generation side effects")
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusProcessing, false)
	insertKnowledgeAttempt(t, db, id, 1)

	var calls atomic.Int32
	applied, err := guard.RunWithKnowledgeAttemptMutation(
		context.Background(), id, 1, []string{types.ParseStatusProcessing},
		func() error {
			calls.Add(1)
			return nil
		},
	)
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, int32(1), calls.Load())

	insertKnowledgeAttempt(t, db, id, 2)
	applied, err = guard.RunWithKnowledgeAttemptMutation(
		context.Background(), id, 1, []string{types.ParseStatusProcessing},
		func() error {
			calls.Add(1)
			return nil
		},
	)
	require.NoError(t, err)
	assert.False(t, applied)
	assert.Equal(t, int32(1), calls.Load(), "stale mutation callback must not run")
}

func TestClaimKnowledgeDeletingIsGenerationSafeAndIdempotent(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	claimer, ok := repo.(interfaces.KnowledgeDeletionClaimer)
	require.True(t, ok, "production repository must support generation-safe deletion")
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusProcessing, false)
	require.NoError(t, db.Exec("UPDATE knowledges SET tenant_id = 17 WHERE id = ?", id).Error)

	previous, claimed, err := claimer.ClaimKnowledgeDeleting(context.Background(), 17, id)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Equal(t, types.ParseStatusProcessing, previous)

	status, _, _ := reloadKnowledgeClaimState(t, db, id)
	assert.Equal(t, types.ParseStatusDeleting, status)
	previous, claimed, err = claimer.ClaimKnowledgeDeleting(context.Background(), 17, id)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Empty(t, previous)
}

func TestKnowledgeAttemptMutationBlocksAttemptCreationUntilCallbackReturns(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec("DROP TABLE knowledge_processing_spans").Error)
	require.NoError(t, db.Exec(spansTestDDL).Error)
	repo := NewKnowledgeRepository(db)
	guard := repo.(interfaces.KnowledgeAttemptMutationGuard)
	spanRepo := NewKnowledgeSpanRepository(db)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusProcessing, false)
	_, err := spanRepo.CreateAttemptRoot(context.Background(), &types.KnowledgeProcessingSpan{
		KnowledgeID: id,
		SpanID:      "root-1",
		Name:        "knowledge_processing",
		Kind:        types.SpanKindRoot,
		Status:      types.SpanStatusRunning,
	})
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		_, err := guard.RunWithKnowledgeAttemptMutation(
			context.Background(), id, 1, []string{types.ParseStatusProcessing},
			func() error {
				close(entered)
				<-release
				return nil
			},
		)
		mutationDone <- err
	}()
	<-entered

	attemptDone := make(chan error, 1)
	go func() {
		_, err := spanRepo.CreateAttemptRoot(context.Background(), &types.KnowledgeProcessingSpan{
			KnowledgeID: id,
			SpanID:      "root-2",
			Name:        "knowledge_processing",
			Kind:        types.SpanKindRoot,
			Status:      types.SpanStatusRunning,
		})
		attemptDone <- err
	}()
	select {
	case err := <-attemptDone:
		t.Fatalf("attempt creation crossed an active mutation guard: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-mutationDone)
	require.NoError(t, <-attemptDone)
}

func TestKnowledgeConditionalUpdaterRejectsStaleEditorSnapshot(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	updater, ok := repo.(interfaces.KnowledgeConditionalUpdater)
	require.True(t, ok)
	id := insertKnowledgeWithStatus(t, db, types.ManualKnowledgeStatusDraft, false)
	observed := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	require.NoError(t, db.Exec("UPDATE knowledges SET updated_at = ? WHERE id = ?", observed, id).Error)

	updated, err := updater.UpdateKnowledgeColumnsIfUnchanged(
		context.Background(), id, types.ManualKnowledgeStatusDraft, observed.Add(-time.Second),
		map[string]interface{}{"description": "stale"},
	)
	require.NoError(t, err)
	assert.False(t, updated)

	updated, err = updater.UpdateKnowledgeColumnsIfUnchanged(
		context.Background(), id, types.ManualKnowledgeStatusDraft, observed,
		map[string]interface{}{"description": "current", "updated_at": observed.Add(time.Second)},
	)
	require.NoError(t, err)
	assert.True(t, updated)
}

func TestCreateClaimedAttemptRootCommitsClaimAndGenerationTogether(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec("DROP TABLE knowledge_processing_spans").Error)
	require.NoError(t, db.Exec(spansTestDDL).Error)
	spanRepo := NewKnowledgeSpanRepository(db)
	claimer, ok := spanRepo.(KnowledgeAttemptRootClaimer)
	require.True(t, ok, "production span repository must support atomic attempt claims")

	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	_, err := spanRepo.CreateAttemptRoot(context.Background(), &types.KnowledgeProcessingSpan{
		KnowledgeID: id,
		SpanID:      "atomic-root-1",
		Name:        "knowledge_processing",
		Kind:        types.SpanKindRoot,
		Status:      types.SpanStatusDone,
	})
	require.NoError(t, err)
	observed := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	require.NoError(t, db.Exec("UPDATE knowledges SET updated_at = ? WHERE id = ?", observed, id).Error)
	now := time.Now()
	root := &types.KnowledgeProcessingSpan{
		KnowledgeID: id,
		SpanID:      "atomic-root-2",
		Name:        "knowledge_processing",
		Kind:        types.SpanKindRoot,
		Status:      types.SpanStatusRunning,
		StartedAt:   &now,
	}

	attempt, claimed, err := claimer.CreateClaimedAttemptRoot(
		context.Background(), root, types.ParseStatusCompleted, observed,
		map[string]interface{}{"description": "new revision", "updated_at": observed.Add(time.Second)},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Equal(t, 2, attempt)
	assert.Equal(t, 2, root.Attempt)

	var status, description string
	require.NoError(t, db.Raw(
		"SELECT parse_status, description FROM knowledges WHERE id = ?", id,
	).Row().Scan(&status, &description))
	assert.Equal(t, types.ParseStatusPending, status)
	assert.Equal(t, "new revision", description)

	var roots int64
	require.NoError(t, db.Model(&types.KnowledgeProcessingSpan{}).
		Where("knowledge_id = ? AND attempt = ? AND span_id = ?", id, 2, root.SpanID).
		Count(&roots).Error)
	assert.Equal(t, int64(1), roots)
}

func TestCreateClaimedAttemptRootRollsBackRootWhenCASLoses(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec("DROP TABLE knowledge_processing_spans").Error)
	require.NoError(t, db.Exec(spansTestDDL).Error)
	spanRepo := NewKnowledgeSpanRepository(db)
	claimer := spanRepo.(KnowledgeAttemptRootClaimer)

	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	_, err := spanRepo.CreateAttemptRoot(context.Background(), &types.KnowledgeProcessingSpan{
		KnowledgeID: id,
		SpanID:      "existing-root",
		Name:        "knowledge_processing",
		Kind:        types.SpanKindRoot,
		Status:      types.SpanStatusDone,
	})
	require.NoError(t, err)
	observed := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	require.NoError(t, db.Exec("UPDATE knowledges SET updated_at = ? WHERE id = ?", observed, id).Error)
	now := time.Now()
	root := &types.KnowledgeProcessingSpan{
		KnowledgeID: id,
		SpanID:      "must-not-persist",
		Name:        "knowledge_processing",
		Kind:        types.SpanKindRoot,
		Status:      types.SpanStatusRunning,
		StartedAt:   &now,
	}

	attempt, claimed, err := claimer.CreateClaimedAttemptRoot(
		context.Background(), root, types.ParseStatusCompleted, observed.Add(-time.Second), nil,
	)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Zero(t, attempt)

	var maxAttempt int
	require.NoError(t, db.Model(&types.KnowledgeProcessingSpan{}).
		Where("knowledge_id = ?", id).
		Select("COALESCE(MAX(attempt), 0)").Row().Scan(&maxAttempt))
	assert.Equal(t, 1, maxAttempt, "a lost CAS must not create an orphan generation")
}

func TestClearKnowledgeStorageUsageIsAtomicAndIdempotent(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE tenants (
			id INTEGER PRIMARY KEY,
			storage_used BIGINT NOT NULL DEFAULT 0,
			deleted_at DATETIME
		)
	`).Error)
	require.NoError(t, db.Exec("INSERT INTO tenants (id, storage_used) VALUES (1, 100)").Error)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET tenant_id = 1, storage_size = 80 WHERE id = ?", id,
	).Error)

	cleaner, ok := NewKnowledgeRepository(db).(interfaces.KnowledgeStorageUsageCleaner)
	require.True(t, ok, "production knowledge repository must implement retry-safe storage cleanup")
	released, err := cleaner.ClearKnowledgeStorageUsage(context.Background(), id, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(80), released)

	released, err = cleaner.ClearKnowledgeStorageUsage(context.Background(), id, 1)
	require.NoError(t, err)
	assert.Zero(t, released)

	var knowledgeStorage, tenantStorage int64
	require.NoError(t, db.Raw("SELECT storage_size FROM knowledges WHERE id = ?", id).Row().Scan(&knowledgeStorage))
	require.NoError(t, db.Raw("SELECT storage_used FROM tenants WHERE id = 1").Row().Scan(&tenantStorage))
	assert.Zero(t, knowledgeStorage)
	assert.Equal(t, int64(20), tenantStorage)
}

func TestClearKnowledgeStorageUsageRollsBackWithoutTenant(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE tenants (
			id INTEGER PRIMARY KEY,
			storage_used BIGINT NOT NULL DEFAULT 0,
			deleted_at DATETIME
		)
	`).Error)
	id := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET tenant_id = 9, storage_size = 50 WHERE id = ?", id,
	).Error)
	cleaner := NewKnowledgeRepository(db).(interfaces.KnowledgeStorageUsageCleaner)

	released, err := cleaner.ClearKnowledgeStorageUsage(context.Background(), id, 9)
	require.Error(t, err)
	assert.Zero(t, released)

	var storage int64
	require.NoError(t, db.Raw("SELECT storage_size FROM knowledges WHERE id = ?", id).Row().Scan(&storage))
	assert.Equal(t, int64(50), storage, "tenant update failure must roll back the knowledge marker")
}
