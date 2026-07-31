package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFilterGenerationAllowedUpdatesDropsStaleWikiGeneration(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeGeneration{}))

	knowledgeID := uuid.NewString()
	activeID := uuid.NewString()
	retiredID := uuid.NewString()
	require.NoError(t, db.Create(&types.Knowledge{
		ID:                 knowledgeID,
		TenantID:           1,
		KnowledgeBaseID:    "kb-1",
		Type:               types.KnowledgeTypeManual,
		ParseStatus:        types.ParseStatusCompleted,
		ActiveGenerationID: activeID,
	}).Error)
	require.NoError(t, db.Create(&types.KnowledgeGeneration{
		ID:             activeID,
		TenantID:       1,
		KnowledgeID:    knowledgeID,
		Attempt:        2,
		State:          types.KnowledgeGenerationStateActive,
		SourceDigest:   "src",
		PipelineDigest: "pipe",
	}).Error)
	require.NoError(t, db.Create(&types.KnowledgeGeneration{
		ID:             retiredID,
		TenantID:       1,
		KnowledgeID:    knowledgeID,
		Attempt:        1,
		State:          types.KnowledgeGenerationStateRetired,
		SourceDigest:   "src-old",
		PipelineDigest: "pipe",
	}).Error)

	got := filterGenerationAllowedUpdates(ctx, repository.NewKnowledgeGenerationRepository(db), 1, []SlugUpdate{
		{Slug: "entity/old", Type: types.WikiPageTypeEntity, KnowledgeID: knowledgeID, GenerationID: retiredID, Attempt: 1},
		{Slug: "entity/current", Type: types.WikiPageTypeEntity, KnowledgeID: knowledgeID, GenerationID: activeID, Attempt: 2},
		{Slug: "entity/legacy", Type: types.WikiPageTypeEntity, KnowledgeID: knowledgeID},
	})

	require.Len(t, got, 2)
	require.Equal(t, "entity/current", got[0].Slug)
	require.Equal(t, "entity/legacy", got[1].Slug)
}
