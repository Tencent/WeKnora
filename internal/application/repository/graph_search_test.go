package repository

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGraphSearchLiveEvidenceSQL(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.Chunk{}))
	for _, name := range []string{
		"live", "chunk-deleted", "doc-deleted", "chunk-disabled", "doc-disabled",
		"deleting", "wrong-tenant", "wrong-kb", "wrong-parent", "indexing", "long",
	} {
		doc := &types.Knowledge{
			ID: name, TenantID: 7, KnowledgeBaseID: "kb",
			EnableStatus: "enabled", ParseStatus: types.ParseStatusCompleted, Title: "标题",
		}
		chunk := &types.Chunk{
			ID: name, TenantID: 7, KnowledgeBaseID: "kb", KnowledgeID: name,
			Content: "内容", IsEnabled: true, IndexStatus: "ready",
		}
		switch name {
		case "chunk-deleted":
			chunk.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
		case "doc-deleted":
			doc.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
		case "chunk-disabled":
			// GORM's default:true would rewrite false at insert; disable below.
		case "doc-disabled":
			doc.EnableStatus = "disabled"
		case "deleting":
			doc.ParseStatus = types.ParseStatusDeleting
		case "wrong-tenant":
			chunk.TenantID, doc.TenantID = 8, 8
		case "wrong-kb":
			chunk.KnowledgeBaseID, doc.KnowledgeBaseID = "other", "other"
		case "wrong-parent":
			doc.KnowledgeBaseID = "other"
		case "indexing":
			chunk.IndexStatus = "processing"
		case "long":
			chunk.Content = strings.Repeat("中", 2100)
			doc.Title = strings.Repeat("题", 600)
		}
		require.NoError(t, db.Create(doc).Error)
		require.NoError(t, db.Create(chunk).Error)
	}
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "chunk-disabled").
		UpdateColumn("is_enabled", false).Error)
	repo := NewGraphSearchEvidenceRepository(db)
	ids := []string{
		"live", "chunk-deleted", "doc-deleted", "chunk-disabled", "doc-disabled",
		"deleting", "wrong-tenant", "wrong-kb", "wrong-parent", "indexing", "missing", "long",
	}
	rows, err := repo.GetGraphSourceChunks(context.Background(), 7, "kb", ids)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "live", rows[0].ID)
	require.Equal(t, "内容", rows[0].Content)
	require.False(t, rows[0].ContentTruncated)
	require.Equal(t, "long", rows[1].ID)
	require.Equal(t, 2000, utf8.RuneCountInString(rows[1].Content))
	require.True(t, utf8.ValidString(rows[1].Content))
	require.True(t, rows[1].ContentTruncated)
	require.Equal(t, 512, utf8.RuneCountInString(rows[1].Title))
	require.True(t, rows[1].TitleTruncated)
	_, err = repo.GetGraphSourceChunks(context.Background(), 7, "kb", make([]string, 513))
	require.Error(t, err)
	_, err = repo.GetGraphSourceChunks(context.Background(), 0, "kb", []string{"live"})
	require.Error(t, err)
	require.NoError(t, db.Migrator().DropTable(&types.Chunk{}))
	rows, err = repo.GetGraphSourceChunks(context.Background(), 7, "kb", []string{"live"})
	require.Error(t, err)
	require.Nil(t, rows, "database failure is not an empty successful search")
}
