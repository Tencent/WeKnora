package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKnowledgeRepositoryCreateKnowledgePersistsInitialFolderPlacement(t *testing.T) {
	tests := []struct {
		name     string
		folderID string
	}{
		{
			name:     "root",
			folderID: types.KnowledgeFolderRootID,
		},
		{
			name:     "non-root",
			folderID: uuid.NewString(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)

			createCalls := 0
			updateCalls := 0
			require.NoError(t, db.Callback().Create().
				Before("gorm:create").
				Register("phase33:count_knowledge_create", func(*gorm.DB) {
					createCalls++
				}))
			require.NoError(t, db.Callback().Update().
				Before("gorm:update").
				Register("phase33:count_knowledge_update", func(*gorm.DB) {
					updateCalls++
				}))

			repo := NewKnowledgeRepository(db)
			tenantID := uint64(42)
			knowledgeBaseID := uuid.NewString()
			knowledge := &types.Knowledge{
				TenantID:        tenantID,
				KnowledgeBaseID: knowledgeBaseID,
				FolderID:        tt.folderID,
				Type:            "document",
				Title:           "folder placement test",
				Source:          "manual",
				ParseStatus:     "pending",
				EnableStatus:    "enabled",
			}

			require.NoError(t, repo.CreateKnowledge(context.Background(), knowledge))
			require.NotEmpty(t, knowledge.ID)
			assert.Equal(t, tenantID, knowledge.TenantID)
			assert.Equal(t, knowledgeBaseID, knowledge.KnowledgeBaseID)
			assert.Equal(t, tt.folderID, knowledge.FolderID)

			var persisted struct {
				TenantID             uint64
				KnowledgeBaseID      string
				FolderID             string
				FolderVersion        uint64
				FolderIndexedVersion uint64
			}
			require.NoError(t, db.Raw(`
				SELECT tenant_id, knowledge_base_id, folder_id,
				       folder_version, folder_indexed_version
				FROM knowledges
				WHERE id = ?
			`, knowledge.ID).Scan(&persisted).Error)

			assert.Equal(t, tenantID, persisted.TenantID)
			assert.Equal(t, knowledgeBaseID, persisted.KnowledgeBaseID)
			assert.Equal(t, tt.folderID, persisted.FolderID)
			assert.Equal(t, uint64(1), persisted.FolderVersion)
			assert.Zero(t, persisted.FolderIndexedVersion)
			assert.Equal(t, 1, createCalls)
			assert.Zero(t, updateCalls, "initial folder placement must not use a follow-up UPDATE")
		})
	}
}

func TestKnowledgeRepositoryDuplicateChecksRemainKnowledgeBaseWide(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	tenantID := uint64(42)
	knowledgeBaseID := uuid.NewString()
	folderA := uuid.NewString()

	fileKnowledge := &types.Knowledge{
		TenantID:        tenantID,
		KnowledgeBaseID: knowledgeBaseID,
		FolderID:        folderA,
		Type:            "file",
		Title:           "file",
		Source:          "upload",
		FileName:        "document.txt",
		FileSize:        7,
		FileHash:        "same-file-hash",
		ParseStatus:     "pending",
		EnableStatus:    "enabled",
	}
	require.NoError(t, repo.CreateKnowledge(ctx, fileKnowledge))

	exists, found, err := repo.CheckKnowledgeExists(
		ctx,
		tenantID,
		knowledgeBaseID,
		&types.KnowledgeCheckParams{
			Type:     "file",
			FileHash: fileKnowledge.FileHash,
		},
	)
	require.NoError(t, err)
	require.True(t, exists)
	require.NotNil(t, found)
	assert.Equal(t, fileKnowledge.ID, found.ID)
	assert.Equal(t, folderA, found.FolderID)

	exists, found, err = repo.CheckKnowledgeExists(
		ctx,
		tenantID,
		uuid.NewString(),
		&types.KnowledgeCheckParams{
			Type:     "file",
			FileHash: fileKnowledge.FileHash,
		},
	)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, found)

	exists, found, err = repo.CheckKnowledgeExists(
		ctx,
		tenantID+1,
		knowledgeBaseID,
		&types.KnowledgeCheckParams{
			Type:     "file",
			FileHash: fileKnowledge.FileHash,
		},
	)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, found)

	urlKnowledge := &types.Knowledge{
		TenantID:        tenantID,
		KnowledgeBaseID: knowledgeBaseID,
		FolderID:        folderA,
		Type:            "url",
		Title:           "URL",
		Source:          "https://phase33b.invalid/page",
		FileHash:        "same-url-hash",
		ParseStatus:     "pending",
		EnableStatus:    "enabled",
	}
	require.NoError(t, repo.CreateKnowledge(ctx, urlKnowledge))

	exists, found, err = repo.CheckKnowledgeExists(
		ctx,
		tenantID,
		knowledgeBaseID,
		&types.KnowledgeCheckParams{
			Type:     "url",
			URL:      urlKnowledge.Source,
			FileHash: urlKnowledge.FileHash,
		},
	)
	require.NoError(t, err)
	require.True(t, exists)
	require.NotNil(t, found)
	assert.Equal(t, urlKnowledge.ID, found.ID)
	assert.Equal(t, folderA, found.FolderID)
}

func TestKnowledgeRepositoryDuplicateChecksIgnoreFailedAndDeletedRows(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	tenantID := uint64(42)
	knowledgeBaseID := uuid.NewString()
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledge_bases (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			deleted_at DATETIME
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledge_folder_index_pending (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id VARCHAR(36) NOT NULL,
			knowledge_id VARCHAR(36) NOT NULL,
			target_folder_id VARCHAR(36) NOT NULL DEFAULT '',
			requested_version INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, ?)`,
		knowledgeBaseID,
		tenantID,
	).Error)

	failed := &types.Knowledge{
		TenantID:        tenantID,
		KnowledgeBaseID: knowledgeBaseID,
		FolderID:        uuid.NewString(),
		Type:            "file",
		Title:           "failed",
		Source:          "upload",
		FileHash:        "failed-hash",
		ParseStatus:     "failed",
		EnableStatus:    "enabled",
	}
	require.NoError(t, repo.CreateKnowledge(ctx, failed))

	exists, found, err := repo.CheckKnowledgeExists(
		ctx,
		tenantID,
		knowledgeBaseID,
		&types.KnowledgeCheckParams{Type: "file", FileHash: failed.FileHash},
	)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, found)

	deleted := &types.Knowledge{
		TenantID:        tenantID,
		KnowledgeBaseID: knowledgeBaseID,
		FolderID:        uuid.NewString(),
		Type:            "file",
		Title:           "deleted",
		Source:          "upload",
		FileHash:        "deleted-hash",
		ParseStatus:     "pending",
		EnableStatus:    "enabled",
	}
	require.NoError(t, repo.CreateKnowledge(ctx, deleted))
	require.NoError(t, repo.DeleteKnowledge(ctx, tenantID, deleted.ID))

	exists, found, err = repo.CheckKnowledgeExists(
		ctx,
		tenantID,
		knowledgeBaseID,
		&types.KnowledgeCheckParams{Type: "file", FileHash: deleted.FileHash},
	)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, found)
}
