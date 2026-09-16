package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newResourceBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.DataPatch{},
		&types.StoredResource{},
		&types.ResourceBinding{},
		&types.Knowledge{},
		&types.Chunk{},
	))
	return db
}

func TestBackfillKnowledgeResourceBindingsClaimsChunkHandles(t *testing.T) {
	db := newResourceBackfillDB(t)
	ctx := context.Background()
	catalog := NewResourceCatalog(repository.NewResourceRepository(db))

	mine, err := catalog.Register(ctx, 7, "local://7/exports/doc.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	theirs, err := catalog.Register(ctx, 9, "local://9/exports/other.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)

	require.NoError(t, db.Create(&types.Knowledge{ID: "kn-live", TenantID: 7, Type: "file"}).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "kn-gone", TenantID: 7, Type: "file"}).Error)
	require.NoError(t, db.Delete(&types.Knowledge{}, "id = ?", "kn-gone").Error)

	require.NoError(t, db.Create(&types.Chunk{
		ID: "c-md", TenantID: 7, KnowledgeID: "kn-live", KnowledgeBaseID: "kb",
		Content: "see ![img](" + mine + ")", ChunkType: types.ChunkTypeText,
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: "c-info", TenantID: 7, KnowledgeID: "kn-live", KnowledgeBaseID: "kb",
		ImageInfo: `[{"url":"` + mine + `"}]`, ChunkType: types.ChunkTypeText,
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: "c-foreign", TenantID: 7, KnowledgeID: "kn-live", KnowledgeBaseID: "kb",
		Content: "![x](" + theirs + ")", ChunkType: types.ChunkTypeText,
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: "c-deleted", TenantID: 7, KnowledgeID: "kn-gone", KnowledgeBaseID: "kb",
		Content: "![x](" + mine + ")", ChunkType: types.ChunkTypeText,
	}).Error)

	require.NoError(t, BackfillKnowledgeResourceBindings(ctx, db))

	refs, err := catalog.ListReferencesByOwner(ctx, types.ResourceOwnerKnowledge, "kn-live")
	require.NoError(t, err)
	require.Equal(t, []string{mine}, refs)

	gone, err := catalog.ListReferencesByOwner(ctx, types.ResourceOwnerKnowledge, "kn-gone")
	require.NoError(t, err)
	require.Empty(t, gone)

	require.NoError(t, BackfillKnowledgeResourceBindings(ctx, db))
	var patches int64
	require.NoError(t, db.Model(&types.DataPatch{}).Count(&patches).Error)
	require.EqualValues(t, 1, patches)
}

func TestBackfillKnowledgeResourceBindingsSkipsWhenAlreadyApplied(t *testing.T) {
	db := newResourceBackfillDB(t)
	ctx := context.Background()
	catalog := NewResourceCatalog(repository.NewResourceRepository(db))
	ref, err := catalog.Register(ctx, 7, "local://7/exports/skip.png", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.Knowledge{ID: "kn-1", TenantID: 7, Type: "file"}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: "c-1", TenantID: 7, KnowledgeID: "kn-1", KnowledgeBaseID: "kb",
		Content: "![x](" + ref + ")", ChunkType: types.ChunkTypeText,
	}).Error)
	require.NoError(t, db.Create(&types.DataPatch{
		ID:        types.DataPatchKnowledgeResourceBindingsV1,
		AppliedAt: time.Now().UTC(),
	}).Error)

	require.NoError(t, BackfillKnowledgeResourceBindings(ctx, db))
	refs, err := catalog.ListReferencesByOwner(ctx, types.ResourceOwnerKnowledge, "kn-1")
	require.NoError(t, err)
	require.Empty(t, refs)
}
