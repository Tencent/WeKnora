package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateKnowledgeDefaultsCustomMetadataToEmptyObject(t *testing.T) {
	dsn := "file:" + uuid.New().String() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))

	repo := NewKnowledgeRepository(db)
	knowledge := &types.Knowledge{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "file",
		Title:           "document.txt",
		ParseStatus:     types.ParseStatusPending,
		EnableStatus:    "disabled",
	}

	require.NoError(t, repo.CreateKnowledge(context.Background(), knowledge))
	require.JSONEq(t, `{}`, string(knowledge.CustomMetadata))

	persisted, err := repo.GetKnowledgeByID(context.Background(), knowledge.TenantID, knowledge.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(persisted.CustomMetadata))
}
