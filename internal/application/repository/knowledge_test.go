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

func setupKnowledgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	return db
}

func TestListKnowledgeByFileNames(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	now := time.Now()

	knowledges := []*types.Knowledge{
		{
			ID:              "target",
			TenantID:        1,
			KnowledgeBaseID: "kb-a",
			Type:            "file",
			FileName:        "docs/images/pic.png",
			FileType:        "png",
			FilePath:        "local://target",
			CreatedAt:       now,
		},
		{
			ID:              "other-file",
			TenantID:        1,
			KnowledgeBaseID: "kb-a",
			Type:            "file",
			FileName:        "docs/images/other.png",
			FileType:        "png",
			FilePath:        "local://other",
			CreatedAt:       now.Add(time.Second),
		},
		{
			ID:              "other-kb",
			TenantID:        1,
			KnowledgeBaseID: "kb-b",
			Type:            "file",
			FileName:        "docs/images/pic.png",
			FileType:        "png",
			FilePath:        "local://other-kb",
			CreatedAt:       now.Add(2 * time.Second),
		},
		{
			ID:              "other-tenant",
			TenantID:        2,
			KnowledgeBaseID: "kb-a",
			Type:            "file",
			FileName:        "docs/images/pic.png",
			FileType:        "png",
			FilePath:        "local://other-tenant",
			CreatedAt:       now.Add(3 * time.Second),
		},
	}
	for _, knowledge := range knowledges {
		require.NoError(t, db.Create(knowledge).Error)
	}

	got, err := repo.ListKnowledgeByFileNames(ctx, 1, "kb-a", []string{"docs/images/pic.png", "missing.png"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "target", got[0].ID)

	empty, err := repo.ListKnowledgeByFileNames(ctx, 1, "kb-a", nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
