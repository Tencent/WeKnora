package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const knowledgeFolderMigrationBaseSchema = `
PRAGMA foreign_keys = ON;
CREATE TABLE knowledge_bases (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL
);
CREATE TABLE knowledges (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    updated_at DATETIME,
    deleted_at DATETIME
);
`

func readKnowledgeFolderSQLiteMigration(t *testing.T, direction string) string {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", "sqlite", "000075_knowledge_folders."+direction+".sql")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

func TestKnowledgeFolderSQLiteMigrationRoundTrip(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(knowledgeFolderMigrationBaseSchema).Error)

	up := readKnowledgeFolderSQLiteMigration(t, "up")
	down := readKnowledgeFolderSQLiteMigration(t, "down")
	require.NoError(t, db.Exec(up).Error)

	require.NoError(t, db.Exec(`
INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1), ('kb-2', 1);
INSERT INTO knowledge_folders
    (id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
VALUES
    ('folder-1', 1, 'kb-1', NULL, 'Root', '/folder-1', 1);
INSERT INTO knowledges
    (id, tenant_id, knowledge_base_id, folder_id)
VALUES
    ('doc-1', 1, 'kb-1', 'folder-1');
`).Error)

	err = db.Exec(`
INSERT INTO knowledges
    (id, tenant_id, knowledge_base_id, folder_id)
VALUES
    ('doc-invalid', 1, 'kb-2', 'folder-1')
`).Error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid knowledge folder")

	require.NoError(t, db.Exec("DELETE FROM knowledges").Error)
	require.NoError(t, db.Exec(down).Error)
	assert.False(t, db.Migrator().HasTable("knowledge_folders"))
	assert.False(t, db.Migrator().HasColumn("knowledges", "folder_id"))

	require.NoError(t, db.Exec(up).Error)
	assert.True(t, db.Migrator().HasTable("knowledge_folders"))
	assert.True(t, db.Migrator().HasColumn("knowledges", "folder_id"))
}
