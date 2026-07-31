package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPagedKnowledgeByKnowledgeBaseIDFiltersFolderIDThreeStates(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := &knowledgeRepository{db: db}
	ctx := context.Background()
	kbID := uuid.NewString()
	rootKnowledgeID := uuid.NewString()
	folderKnowledgeID := uuid.NewString()
	folderID := uuid.NewString()

	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id,
			tenant_id,
			knowledge_base_id,
			folder_id,
			type,
			title,
			source,
			parse_status
		)
		VALUES
			(?, 1, ?, '', 'file', 'root document', 'root', 'completed'),
			(?, 1, ?, ?, 'file', 'folder document', 'folder', 'completed')
	`, rootKnowledgeID, kbID, folderKnowledgeID, kbID, folderID).Error)

	rootFolderID := ""
	tests := []struct {
		name      string
		folderID  *string
		wantIDs   []string
		wantTotal int64
	}{
		{
			name:      "nil returns all folders",
			wantIDs:   []string{rootKnowledgeID, folderKnowledgeID},
			wantTotal: 2,
		},
		{
			name:      "empty string returns root only",
			folderID:  &rootFolderID,
			wantIDs:   []string{rootKnowledgeID},
			wantTotal: 1,
		},
		{
			name:      "uuid returns exact folder only",
			folderID:  &folderID,
			wantIDs:   []string{folderKnowledgeID},
			wantTotal: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			knowledges, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(
				ctx,
				1,
				kbID,
				&types.Pagination{Page: 1, PageSize: 20},
				types.KnowledgeListFilter{FolderID: test.folderID},
			)

			require.NoError(t, err)
			require.Equal(t, test.wantTotal, total)
			ids := make([]string, 0, len(knowledges))
			for _, knowledge := range knowledges {
				ids = append(ids, knowledge.ID)
			}
			assert.ElementsMatch(t, test.wantIDs, ids)
		})
	}
}
