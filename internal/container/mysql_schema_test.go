package container

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMigrateMySQLSchema(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrateMySQLSchema(db))

	for _, table := range []string{
		"tenants", "models", "knowledge_bases", "knowledges", "chunks",
		"messages", "data_sources", "wiki_pages", "task_pending_ops",
	} {
		require.True(t, db.Migrator().HasTable(table), "missing MySQL table %s", table)
	}

	// Startup migration must be idempotent.
	require.NoError(t, migrateMySQLSchema(db))
}
