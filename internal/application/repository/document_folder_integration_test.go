package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const documentFolderIntegrationDDL = `
CREATE TABLE document_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);
`

// This integration test deliberately crosses the service/repository boundary:
// the production resolver, not a test-local BFS copy, must expand the subtree
// loaded from a real SQLite repository.
func TestDocumentFolderService_ResolveSubtreeWithSQLiteRepository(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(documentFolderIntegrationDDL).Error)

	folderRepo := repository.NewDocumentFolderRepository(db)
	folderService := service.NewDocumentFolderService(folderRepo, nil, nil, nil)
	ctx := context.Background()
	for _, folder := range []*types.DocumentFolder{
		integrationFolder("eng", "", "Engineering", "Engineering", 1),
		integrationFolder("be", "eng", "Backend", "Engineering/Backend", 2),
		integrationFolder("api", "be", "API", "Engineering/Backend/API", 3),
		integrationFolder("mkt", "", "Marketing", "Marketing", 1),
	} {
		require.NoError(t, folderRepo.CreateFolder(ctx, folder))
	}

	subtree, err := folderService.ResolveSubtreeFolderIDs(ctx, "kb-int", "eng")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"eng", "be", "api"}, subtree)
	assert.NotContains(t, subtree, "mkt")
}

func integrationFolder(id, parentID, name, path string, depth int) *types.DocumentFolder {
	now := time.Now()
	return &types.DocumentFolder{
		ID:              id,
		TenantID:        1,
		KnowledgeBaseID: "kb-int",
		ParentID:        parentID,
		Name:            name,
		Path:            path,
		Depth:           depth,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}
