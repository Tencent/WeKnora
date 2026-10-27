package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestReplaceKnowledgePostgresRollbackAndCommit(t *testing.T) {
	dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL replacement tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	schema := "knowledge_replace_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, adminSQL.Close())
	})
	separator := " "
	if strings.Contains(dsn, "://") {
		separator = "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
	}
	db, err := gorm.Open(postgres.Open(dsn+separator+"search_path="+schema), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeTagRelation{}))
	ctx := context.Background()
	repo := NewKnowledgeRepository(db)
	old := &types.Knowledge{ID: "old", TenantID: 7, KnowledgeBaseID: "kb", Type: "file"}
	occupied := &types.Knowledge{ID: "occupied", TenantID: 7, KnowledgeBaseID: "kb", Type: "file"}
	require.NoError(t, repo.CreateKnowledge(ctx, old))
	require.NoError(t, repo.CreateKnowledge(ctx, occupied))
	old, err = repo.GetKnowledgeByID(ctx, 7, old.ID)
	require.NoError(t, err)
	require.NoError(t, repo.SetKnowledgeTags(ctx, old.ID, []string{"old-tag"}))
	replacement := &types.Knowledge{ID: occupied.ID, TenantID: 7, KnowledgeBaseID: "kb", Type: "file"}
	require.Error(t, repo.ReplaceKnowledge(ctx, old, replacement, []string{"new-tag"}))
	retained, err := repo.GetKnowledgeByID(ctx, 7, old.ID)
	require.NoError(t, err, "failed insertion must roll back the retirement")
	require.False(t, retained.DeletedAt.Valid)
	var tags []types.KnowledgeTagRelation
	require.NoError(t, db.Where("knowledge_id = ?", old.ID).Find(&tags).Error)
	require.Len(t, tags, 1)
	require.Equal(t, "old-tag", tags[0].TagID)
	replacement.ID = "new"
	require.NoError(t, repo.ReplaceKnowledge(ctx, old, replacement, []string{"new-tag"}))
	_, err = repo.GetKnowledgeByID(ctx, 7, old.ID)
	require.Error(t, err)
	_, err = repo.GetKnowledgeByID(ctx, 7, replacement.ID)
	require.NoError(t, err)
	tags = nil
	require.NoError(t, db.Where("knowledge_id = ?", replacement.ID).Find(&tags).Error)
	require.Len(t, tags, 1)
	require.Equal(t, "new-tag", tags[0].TagID)
	replacement.ID = "stale-replacement"
	require.ErrorContains(t, repo.ReplaceKnowledge(ctx, old, replacement, nil), "knowledge changed")
}
