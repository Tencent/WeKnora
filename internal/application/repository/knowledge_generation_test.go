package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupGenerationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeGeneration{}))
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func seedGenerationKnowledge(t *testing.T, db *gorm.DB, knowledgeID string) {
	t.Helper()
	require.NoError(t, db.Create(&types.Knowledge{
		ID:              knowledgeID,
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.KnowledgeTypeManual,
		ParseStatus:     types.ParseStatusCompleted,
	}).Error)
}

func makeGeneration(knowledgeID string, attempt int, state types.KnowledgeGenerationState) *types.KnowledgeGeneration {
	return &types.KnowledgeGeneration{
		ID:             uuid.NewString(),
		TenantID:       1,
		KnowledgeID:    knowledgeID,
		Attempt:        attempt,
		State:          state,
		SourceDigest:   "source",
		PipelineDigest: "pipeline",
		CreatedAt:      time.Now(),
	}
}

func TestKnowledgeGenerationActivateIfCurrent_RejectsOlderAttempt(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	oldGeneration := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateReady)
	newGeneration := makeGeneration(knowledgeID, 2, types.KnowledgeGenerationStateReady)
	require.NoError(t, repo.Create(ctx, oldGeneration))
	require.NoError(t, repo.Create(ctx, newGeneration))

	activated, err := repo.ActivateIfCurrent(ctx, oldGeneration.ID, oldGeneration.Attempt)
	require.NoError(t, err)
	assert.False(t, activated)

	var knowledge types.Knowledge
	require.NoError(t, db.First(&knowledge, "id = ?", knowledgeID).Error)
	assert.Empty(t, knowledge.ActiveGenerationID)
}

func TestKnowledgeGenerationActivateIfCurrent_ActivatesLatestAndRetiresPrevious(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	first := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateReady)
	second := makeGeneration(knowledgeID, 2, types.KnowledgeGenerationStateReady)
	require.NoError(t, repo.Create(ctx, first))
	ok, err := repo.ActivateIfCurrent(ctx, first.ID, first.Attempt)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, repo.Create(ctx, second))

	ok, err = repo.ActivateIfCurrent(ctx, second.ID, second.Attempt)
	require.NoError(t, err)
	require.True(t, ok)

	active, err := repo.GetActive(ctx, 1, knowledgeID)
	require.NoError(t, err)
	assert.Equal(t, second.ID, active.ID)

	reloadedFirst, err := repo.Get(ctx, 1, first.ID)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeGenerationStateRetired, reloadedFirst.State)
}

func TestKnowledgeGenerationMarkReadyLeavesPreviousActiveVisibleUntilActivation(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	oldActive := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateReady)
	require.NoError(t, repo.Create(ctx, oldActive))
	activated, err := repo.ActivateIfCurrent(ctx, oldActive.ID, oldActive.Attempt)
	require.NoError(t, err)
	require.True(t, activated)

	newBuilding := makeGeneration(knowledgeID, 2, types.KnowledgeGenerationStateBuilding)
	require.NoError(t, repo.Create(ctx, newBuilding))
	require.NoError(t, repo.MarkReady(ctx, newBuilding.ID, "manifest-new"))

	active, err := repo.GetActive(ctx, 1, knowledgeID)
	require.NoError(t, err)
	assert.Equal(t, oldActive.ID, active.ID, "CAS-before-crash must keep serving the previous active generation")

	var knowledge types.Knowledge
	require.NoError(t, db.First(&knowledge, "id = ?", knowledgeID).Error)
	assert.Equal(t, oldActive.ID, knowledge.ActiveGenerationID)
	assert.Equal(t, types.KnowledgeGenerationStateActive, active.State)

	reloadedNew, err := repo.Get(ctx, 1, newBuilding.ID)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeGenerationStateReady, reloadedNew.State)
	assert.Equal(t, "manifest-new", reloadedNew.ManifestDigest)
}

func TestKnowledgeGenerationActivateIfCurrent_PublishesSnapshotDescription(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	generation := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateReady)
	require.NoError(t, repo.Create(ctx, generation))
	require.NoError(t, repo.SetSnapshotDescription(ctx, generation.ID, "new staged summary"))

	var before types.Knowledge
	require.NoError(t, db.First(&before, "id = ?", knowledgeID).Error)
	assert.Empty(t, before.Description)

	activated, err := repo.ActivateIfCurrent(ctx, generation.ID, generation.Attempt)
	require.NoError(t, err)
	require.True(t, activated)

	var after types.Knowledge
	require.NoError(t, db.First(&after, "id = ?", knowledgeID).Error)
	assert.Equal(t, generation.ID, after.ActiveGenerationID)
	assert.Equal(t, "new staged summary", after.Description)
}

func TestKnowledgeGenerationMarkRetiredDoesNotRetireActiveGeneration(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	generation := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateReady)
	require.NoError(t, repo.Create(ctx, generation))
	activated, err := repo.ActivateIfCurrent(ctx, generation.ID, generation.Attempt)
	require.NoError(t, err)
	require.True(t, activated)

	require.NoError(t, repo.MarkRetired(ctx, generation.ID))

	reloaded, err := repo.Get(ctx, 1, generation.ID)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeGenerationStateActive, reloaded.State)
}

func TestKnowledgeGenerationListGCEligibleIncludesStaleBuilding(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	staleBuilding := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateBuilding)
	staleBuilding.UpdatedAt = time.Now().Add(-48 * time.Hour)
	freshBuilding := makeGeneration(knowledgeID, 2, types.KnowledgeGenerationStateBuilding)
	freshBuilding.UpdatedAt = time.Now()
	require.NoError(t, repo.Create(ctx, staleBuilding))
	require.NoError(t, repo.Create(ctx, freshBuilding))

	eligible, err := repo.ListGCEligible(ctx, time.Now().Add(-24*time.Hour), 10)
	require.NoError(t, err)

	require.Len(t, eligible, 1)
	assert.Equal(t, staleBuilding.ID, eligible[0].ID)
}

func TestKnowledgeGenerationMarkPurgedAllowsStaleBuilding(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	generation := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateBuilding)
	require.NoError(t, repo.Create(ctx, generation))

	require.NoError(t, repo.MarkPurged(ctx, generation.ID))

	reloaded, err := repo.Get(ctx, 1, generation.ID)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeGenerationStatePurged, reloaded.State)
}

func TestKnowledgeGenerationActivateIfCurrent_ConcurrentAttemptsOnlyLatestActivates(t *testing.T) {
	db := setupGenerationTestDB(t)
	repo := NewKnowledgeGenerationRepository(db)
	ctx := context.Background()
	knowledgeID := uuid.NewString()
	seedGenerationKnowledge(t, db, knowledgeID)

	first := makeGeneration(knowledgeID, 1, types.KnowledgeGenerationStateReady)
	second := makeGeneration(knowledgeID, 2, types.KnowledgeGenerationStateReady)
	require.NoError(t, repo.Create(ctx, first))
	require.NoError(t, repo.Create(ctx, second))

	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, generation := range []*types.KnowledgeGeneration{first, second} {
		wg.Add(1)
		go func(generation *types.KnowledgeGeneration) {
			defer wg.Done()
			ok, err := repo.ActivateIfCurrent(ctx, generation.ID, generation.Attempt)
			require.NoError(t, err)
			if ok {
				results <- generation.ID
			}
		}(generation)
	}
	wg.Wait()
	close(results)

	var activated []string
	for id := range results {
		activated = append(activated, id)
	}
	assert.Equal(t, []string{second.ID}, activated)
}
