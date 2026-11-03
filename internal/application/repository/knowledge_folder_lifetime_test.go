package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeFolderCannotOutliveKnowledgeBaseDeletion(t *testing.T) {
	for _, createFirst := range []bool{false, true} {
		name := "cleanup before delayed create"
		if createFirst {
			name = "create before delete and cleanup"
		}
		t.Run(name, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			repo := NewKnowledgeRepository(db)
			ctx := context.Background()
			// Model the service authorization lookup that happened before
			// the KB deletion. Its copied row must not authorize a later insert.
			var authorizedKB types.KnowledgeBase
			require.NoError(t, db.Where("id = ? AND tenant_id = ?", "kb", 1).First(&authorizedKB).Error)
			if createFirst {
				require.NoError(t, repo.CreateKnowledgeFolder(ctx, authorizedKB.TenantID, authorizedKB.ID, "empty"))
			}
			// Same soft-delete operation used by the KB repository, followed
			// by the asynchronous marker cleanup under its KB mutation lock.
			require.NoError(t, db.Where("id = ?", "kb").Delete(&types.KnowledgeBase{}).Error)
			require.NoError(t, repo.DeleteKnowledgeFolders(ctx, 1, "kb"))
			require.ErrorIs(t,
				repo.CreateKnowledgeFolder(ctx, authorizedKB.TenantID, authorizedKB.ID, "late"),
				ErrKnowledgeFolderNotFound)
			var markers int64
			require.NoError(t, db.Model(&types.KnowledgeFolder{}).
				Where("tenant_id = ? AND knowledge_base_id = ?", 1, "kb").Count(&markers).Error)
			require.Zero(t, markers)
			// Another live knowledge base is unaffected by this cleanup.
			require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "other", "empty"))
		})
	}
}

func TestCreateKnowledgeFolderRejectsMissingOrForeignKnowledgeBase(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	require.ErrorIs(t, repo.CreateKnowledgeFolder(ctx, 1, "missing", "empty"), ErrKnowledgeFolderNotFound)
	require.ErrorIs(t, repo.CreateKnowledgeFolder(ctx, 2, "kb", "empty"), ErrKnowledgeFolderNotFound)
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&count).Error)
	require.Zero(t, count)
}
