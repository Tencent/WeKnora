package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListKnowledgeByKnowledgeBaseIDUnscopedIncludesSoftDeletedRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE knowledges (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		knowledge_base_id TEXT NOT NULL,
		created_at DATETIME,
		deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, created_at, deleted_at) VALUES (?, ?, ?, ?, ?)",
		"knowledge-deleted", 1, "kb-1", time.Now(), time.Now(),
	).Error)

	repo := &knowledgeRepository{db: db}
	items, err := repo.ListKnowledgeByKnowledgeBaseIDUnscoped(context.Background(), 1, "kb-1")

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "knowledge-deleted", items[0].ID)
	require.True(t, items[0].DeletedAt.Valid)
}
