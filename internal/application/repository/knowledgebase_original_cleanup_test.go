package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFindOriginalForCleanup(t *testing.T) {
	db := setupKBTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE knowledges (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER,
		knowledge_base_id TEXT,
		file_path TEXT,
		deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}, &types.ResourceBinding{}))
	ctx := context.Background()
	repo := &knowledgeBaseRepository{db: db}
	handle := strings.Repeat("a", types.ResourceHandleLength)
	ref := types.BuildResourcePath(handle)
	resource := &types.StoredResource{
		ID: "resource", Handle: handle, TenantID: 7, StorageBackendID: "bound",
		Provider: "local", PhysicalPath: "local://7/doc.pdf", LocationHash: "location",
	}
	require.NoError(t, db.Create(resource).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, file_path) VALUES (?, ?, ?, ?)",
		"doc", 7, "kb", ref,
	).Error)
	binding := &types.ResourceBinding{
		ResourceID: resource.ID, TenantID: 7, OwnerType: types.ResourceOwnerKnowledge, OwnerID: "doc",
	}
	require.NoError(t, db.Create(binding).Error)

	found, shared, err := repo.FindOriginalForCleanup(ctx, 7, "kb", ref)
	require.NoError(t, err)
	require.False(t, shared)
	require.Equal(t, "bound", found.StorageBackendID)

	other := &types.ResourceBinding{
		ResourceID: resource.ID, TenantID: 7, OwnerType: types.ResourceOwnerKnowledge, OwnerID: "other",
	}
	require.NoError(t, db.Create(other).Error)
	_, shared, err = repo.FindOriginalForCleanup(ctx, 7, "kb", ref)
	require.NoError(t, err)
	require.True(t, shared)
	require.NoError(t, db.Delete(other).Error)

	require.NoError(t, repo.MarkOriginalDeleted(ctx, 7, resource.ID))
	found, shared, err = repo.FindOriginalForCleanup(ctx, 7, "kb", ref)
	require.NoError(t, err)
	require.False(t, shared)
	require.Equal(t, types.ResourceStateDeleted, found.State)
	require.Equal(t, resource.PhysicalPath, found.PhysicalPath)

	_, _, err = repo.FindOriginalForCleanup(ctx, 8, "kb", ref)
	require.Error(t, err)
}
