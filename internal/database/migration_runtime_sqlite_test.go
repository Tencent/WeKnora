//go:build sqlite_fts5

package database_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	retriever "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteRuntimeRetrieverRestartAndSkillSchema(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	opts := database.MigrationOptions{SQLiteDBPath: path, MigrationsRoot: filepath.Join(root, "migrations")}
	state, err := database.ApplyMigrations(context.Background(), "sqlite3://fixture", opts)
	require.NoError(t, err)
	require.Equal(t, 16, state.Topic3.Version)
	db, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	repo := retriever.NewSQLiteRetrieveEngineRepository(db)
	for _, dimension := range []int{3, 7} {
		vector := make([]float32, dimension)
		vector[0] = 1
		id := string(rune('a' + dimension))
		info := &types.IndexInfo{
			SourceID:        id,
			SourceType:      types.ChunkSourceType,
			ChunkID:         id,
			KnowledgeID:     "x03-knowledge",
			KnowledgeBaseID: "x03-kb",
			Content:         "测试内容",
			IsEnabled:       true,
		}
		require.NoError(
			t,
			repo.Save(context.Background(), info, map[string]any{"embedding": map[string][]float32{id: vector}}),
		)
	}
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, database.ValidateMigrationReadiness(context.Background(), "sqlite3://fixture", opts))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	again, err := database.ApplyMigrations(context.Background(), "sqlite3://fixture", opts)
	require.NoError(t, err)
	require.Equal(t, state.RunID, again.RunID)
	var count int64
	require.NoError(t, db.Table("lite_embeddings").Count(&count).Error)
	require.EqualValues(t, 2, count)
	catalog := &types.TenantSkillCatalogEntity{ID: "x03-catalog", TenantID: 7001, Name: "skill"}
	require.NoError(t, db.Create(catalog).Error)
	started := time.Now().Add(-time.Hour)
	skill := &types.TenantSkillEntity{
		ID:              "x03-install",
		TenantID:        7001,
		SandboxConfigID: "x03-sandbox",
		Name:            "skill",
		CatalogID:       catalog.ID,
		Status:          types.SkillStatusInstalling,
		InstallingSince: &started,
	}
	require.NoError(t, db.Create(skill).Error)
	var stale []types.TenantSkillEntity
	require.NoError(
		t,
		db.Where(
			"status IN ? AND installing_since IS NOT NULL AND installing_since < ?",
			[]string{types.SkillStatusInstalling, types.SkillStatusRemoving},
			time.Now(),
		).Find(
			&stale,
		).Error,
	)
	require.Len(t, stale, 1)
	require.NoError(
		t,
		db.Create(
			&types.TenantSkillSnapshotEntity{
				ID:              "x03-snapshot",
				TenantID:        7001,
				SandboxConfigID: "x03-sandbox",
				SkillID:         skill.ID,
				Trigger:         "install",
				State:           "building",
				PlannedName:     "planned",
			},
		).Error,
	)
	require.NoError(t, database.ValidateMigrationReadiness(context.Background(), "sqlite3://fixture", opts))
	require.NoError(t, db.Exec("UPDATE tenant_skills SET catalog_id='missing-catalog'").Error)
	require.ErrorContains(
		t,
		database.ValidateMigrationReadiness(
			context.Background(),
			"sqlite3://fixture",
			opts,
		),
		"skill installation",
	)
	require.NoError(t, db.Exec("UPDATE tenant_skills SET catalog_id='x03-catalog'").Error)
	require.NoError(t, db.Exec("DROP INDEX idx_sqlite_emb_source").Error)
	require.ErrorContains(
		t,
		database.ValidateMigrationReadiness(
			context.Background(),
			"sqlite3://fixture",
			opts,
		),
		"runtime structure mismatch",
	)
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX idx_sqlite_emb_source ON "+
		"lite_embeddings(source_id,source_type)").Error)
	// The replacement has different DDL quoting and remains an explicit structural mismatch.
	require.Error(t, database.ValidateMigrationReadiness(context.Background(), "sqlite3://fixture", opts))
}

func TestSQLiteUnversionedRuntimeIsRejected(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(t.TempDir(), "unowned.sqlite")
	db, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()
	retriever.NewSQLiteRetrieveEngineRepository(db)
	opts := database.MigrationOptions{
		SQLiteDBPath:   path,
		MigrationsRoot: filepath.Join(filepath.Dir(file), "../../migrations"),
	}
	_, err = database.ApplyMigrations(context.Background(), "sqlite3://fixture", opts)
	require.ErrorContains(t, err, "unversioned nonempty")
	require.False(t, db.Migrator().HasTable(database.MigrationBridgeTable))
}
