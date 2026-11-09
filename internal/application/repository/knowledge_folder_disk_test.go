package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// This closes and reopens a real temporary database file. It verifies SQLite
// persistence and the shipped migration; it is not a PostgreSQL restore test.
func TestExplicitKnowledgeFoldersSurviveSQLiteCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folders.sqlite")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		return db
	}
	closeDB := func(db *gorm.DB) {
		connection, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, connection.Close())
	}
	up, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000052_knowledge_folders.up.sql"))
	require.NoError(t, err)
	down, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "migrations", "sqlite", "000052_knowledge_folders.down.sql",
	))
	require.NoError(t, err)
	db := open()
	require.NoError(t, db.Exec(
		`CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, deleted_at DATETIME)`,
	).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id, tenant_id) VALUES ('kb', 7)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledges(id TEXT PRIMARY KEY, tenant_id INTEGER, `+
		`knowledge_base_id TEXT, folder_path TEXT, parse_status TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(string(up)).Error)
	ctx := context.Background()
	repo := NewKnowledgeRepository(db)
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "kb", "资料"))
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "kb", "资料/空目录"))
	closeDB(db)

	db = open()
	repo = NewKnowledgeRepository(db)
	counts, err := repo.ListKnowledgeFolderCounts(ctx, 7, "kb")
	require.NoError(t, err)
	tree := types.BuildKnowledgeFolderTree(counts)
	require.Zero(t, tree.TotalDocumentCount)
	require.Len(t, tree.Folders, 1)
	require.Equal(t, "资料/空目录", tree.Folders[0].Children[0].Path)
	affected, err := repo.RenameKnowledgeFolderPath(ctx, 7, "kb", "资料/空目录", "资料/已改名")
	require.NoError(t, err)
	require.Zero(t, affected)
	closeDB(db)

	db = open()
	defer closeDB(db)
	counts, err = NewKnowledgeRepository(db).ListKnowledgeFolderCounts(ctx, 7, "kb")
	require.NoError(t, err)
	require.Equal(t, "资料/已改名", types.BuildKnowledgeFolderTree(counts).Folders[0].Children[0].Path)
	require.NoError(t, db.Exec(`INSERT INTO knowledges(id,tenant_id,knowledge_base_id,folder_path,parse_status) `+
		`VALUES('legacy-file',7,'kb','旧路径','completed')`).Error)
	require.NoError(t, db.Exec(string(down)).Error)
	var documentCount int64
	require.NoError(t, db.Table("knowledges").
		Where("id = ? AND folder_path = ?", "legacy-file", "旧路径").Count(&documentCount).Error)
	require.EqualValues(t, 1, documentCount, "rollback must preserve existing file placement")
	require.NoError(t, db.Exec(string(up)).Error)
	var markerCount int64
	require.NoError(t, db.Table("knowledge_folders").Count(&markerCount).Error)
	require.Zero(t, markerCount, "rollback intentionally loses explicit empty-directory markers")
}
