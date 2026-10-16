package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCountAllKnowledgeRetainsDeletingEntriesAndTenantScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	rows := []*types.Knowledge{
		{ID: "complete", TenantID: 1, KnowledgeBaseID: "history", ParseStatus: types.ParseStatusCompleted},
		{ID: "pending", TenantID: 1, KnowledgeBaseID: "history", ParseStatus: types.ParseStatusPending},
		{ID: "deleting", TenantID: 1, KnowledgeBaseID: "history", ParseStatus: types.ParseStatusDeleting},
		{
			ID: "removed", TenantID: 1, KnowledgeBaseID: "history",
			DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true},
		},
		{ID: "other-workspace", TenantID: 2, KnowledgeBaseID: "history", ParseStatus: types.ParseStatusCompleted},
		{ID: "ordinary-kb", TenantID: 1, KnowledgeBaseID: "documents", ParseStatus: types.ParseStatusCompleted},
	}
	require.NoError(t, db.Create(rows).Error)
	repo := NewKnowledgeRepository(db)
	count, err := repo.CountAllKnowledgeByKnowledgeBaseID(t.Context(), 1, "history")
	require.NoError(t, err)
	require.EqualValues(t, 3, count)
	displayCount, err := repo.CountKnowledgeByKnowledgeBaseID(t.Context(), 1, "history")
	require.NoError(t, err)
	require.EqualValues(t, 2, displayCount, "document-list counts intentionally hide deleting rows")

	require.NoError(t, db.Migrator().DropTable(&types.Knowledge{}))
	_, err = repo.CountAllKnowledgeByKnowledgeBaseID(t.Context(), 1, "history")
	require.Error(t, err, "database failures must not become an empty-index signal")
}
