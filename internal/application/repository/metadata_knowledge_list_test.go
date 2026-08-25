package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMetadataFilter_KnowledgeListHonorsIDsAndNoneScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			parse_status TEXT NOT NULL DEFAULT 'completed',
			created_at DATETIME,
			deleted_at DATETIME
		)
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status, created_at)
		VALUES ('doc-a', 1, 'kb-a', 'completed', CURRENT_TIMESTAMP),
		       ('doc-b', 1, 'kb-a', 'completed', CURRENT_TIMESTAMP),
		       ('doc-other-kb', 1, 'kb-b', 'completed', CURRENT_TIMESTAMP),
		       ('doc-other-tenant', 2, 'kb-a', 'completed', CURRENT_TIMESTAMP)
	`).Error)

	repo := NewKnowledgeRepository(db)
	page := &types.Pagination{Page: 1, PageSize: 20}
	rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(
		t.Context(),
		1,
		"kb-a",
		page,
		types.KnowledgeListFilter{
			RestrictKnowledgeIDs: true,
			KnowledgeIDs:         []string{"doc-b", "doc-other-kb", "doc-other-tenant"},
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	require.Equal(t, "doc-b", rows[0].ID)

	rows, total, err = repo.ListPagedKnowledgeByKnowledgeBaseID(
		t.Context(),
		1,
		"kb-a",
		page,
		types.KnowledgeListFilter{RestrictKnowledgeIDs: true},
	)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, rows)
}
