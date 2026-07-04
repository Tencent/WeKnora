package mysql

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLRetrieverIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_DSN to run MySQL retriever integration tests")
	}

	cfg, err := mysqlDriver.ParseDSN(dsn)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.DBName, "WEKNORA_MYSQL_TEST_DSN must include a database name")
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.InterpolateParams = true

	migrateCfg := *cfg
	migrateCfg.MultiStatements = true
	migrateDSN := os.Getenv("WEKNORA_MYSQL_TEST_MIGRATE_DSN")
	if migrateDSN == "" {
		migrateDSN = "mysql://" + migrateCfg.FormatDSN()
	}

	restoreWorkingDir := chdirModuleRoot(t)
	defer restoreWorkingDir()
	require.NoError(t, database.RunMigrationsWithOptions(
		migrateDSN,
		database.MigrationOptions{AutoRecoverDirty: true},
	))

	db, err := gorm.Open(gormmysql.Open(cfg.FormatDSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, sqlDB.Ping())

	tablePrefix := "weknora_it_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	t.Cleanup(func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent(tableName(tablePrefix, 3))).Error
	})

	repo := NewMySQLRetrieveEngineRepository(db, cfg.DBName, &types.IndexConfig{
		CollectionName: tablePrefix,
	})

	ctx := context.Background()
	items := []*types.IndexInfo{
		{
			ID:              "idx-1",
			SourceID:        "src-1",
			SourceType:      types.ChunkSourceType,
			ChunkID:         "chunk-1",
			KnowledgeID:     "knowledge-1",
			KnowledgeBaseID: "kb-1",
			TagID:           "tag-a",
			Content:         "alpha mysql vector keyword content",
			IsEnabled:       true,
		},
		{
			ID:              "idx-2",
			SourceID:        "src-2",
			SourceType:      types.ChunkSourceType,
			ChunkID:         "chunk-2",
			KnowledgeID:     "knowledge-2",
			KnowledgeBaseID: "kb-1",
			TagID:           "tag-b",
			Content:         "beta unrelated content",
			IsEnabled:       true,
		},
	}
	params := map[string]any{
		fieldEmbedding: map[string][]float32{
			"src-1": {1, 0, 0},
			"src-2": {0, 1, 0},
		},
	}
	require.NoError(t, repo.BatchSave(ctx, items, params))

	vector, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0, 0},
		KnowledgeBaseIDs: []string{"kb-1"},
		TopK:             2,
		Threshold:        0.5,
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, vector, 1)
	require.Len(t, vector[0].Results, 1)
	require.Equal(t, "chunk-1", vector[0].Results[0].ChunkID)
	require.GreaterOrEqual(t, vector[0].Results[0].Score, 0.5)

	keywords, err := repo.Retrieve(ctx, types.RetrieveParams{
		Query:            "mysql keyword",
		KnowledgeBaseIDs: []string{"kb-1"},
		TopK:             5,
		RetrieverType:    types.KeywordsRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, keywords, 1)
	require.NotEmpty(t, keywords[0].Results)
	require.Greater(t, keywords[0].Results[0].Score, float64(0))

	require.NoError(t, repo.CopyIndices(ctx, "kb-1", map[string]string{
		"knowledge-1": "knowledge-1-copy",
		"knowledge-2": "knowledge-2-copy",
	}, map[string]string{
		"chunk-1": "chunk-1-copy",
		"chunk-2": "chunk-2-copy",
	}, "kb-2", 3, ""))

	copied, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0, 0},
		KnowledgeBaseIDs: []string{"kb-2"},
		TopK:             2,
		Threshold:        0.5,
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, copied, 1)
	require.Len(t, copied[0].Results, 1)
	require.Equal(t, "chunk-1-copy", copied[0].Results[0].ChunkID)

	require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{"chunk-1"}, 3, ""))
	deleted, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0, 0},
		KnowledgeBaseIDs: []string{"kb-1"},
		TopK:             2,
		Threshold:        0.5,
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	require.Empty(t, deleted[0].Results)
}

func TestMySQLHeatWaveRetrieverIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_HEATWAVE_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_HEATWAVE_TEST_DSN to run MySQL HeatWave retriever integration tests")
	}

	cfg, err := mysqlDriver.ParseDSN(dsn)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.DBName, "WEKNORA_MYSQL_HEATWAVE_TEST_DSN must include a database name")
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.InterpolateParams = true

	db, err := gorm.Open(gormmysql.Open(cfg.FormatDSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, sqlDB.Ping())

	tablePrefix := "weknora_hw_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	t.Cleanup(func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent(tableName(tablePrefix, 3))).Error
	})

	repo := NewMySQLRetrieveEngineRepository(db, cfg.DBName, &types.IndexConfig{
		CollectionName: tablePrefix,
	}).(*mysqlRepository)

	ctx := context.Background()
	require.True(t, repo.detectNativeVectorType(ctx), "HeatWave test requires native VECTOR support")
	require.NotNil(t, repo.detectVectorDistance(ctx), "HeatWave test requires DISTANCE/VECTOR_DISTANCE support")

	require.NoError(t, repo.BatchSave(ctx, []*types.IndexInfo{
		{
			ID:              "hw-1",
			SourceID:        "hw-src-1",
			SourceType:      types.ChunkSourceType,
			ChunkID:         "hw-chunk-1",
			KnowledgeID:     "hw-knowledge-1",
			KnowledgeBaseID: "hw-kb-1",
			Content:         "heatwave nearest vector",
			IsEnabled:       true,
		},
		{
			ID:              "hw-2",
			SourceID:        "hw-src-2",
			SourceType:      types.ChunkSourceType,
			ChunkID:         "hw-chunk-2",
			KnowledgeID:     "hw-knowledge-2",
			KnowledgeBaseID: "hw-kb-1",
			Content:         "heatwave distant vector",
			IsEnabled:       true,
		},
	}, map[string]any{
		fieldEmbedding: map[string][]float32{
			"hw-src-1": {1, 0, 0},
			"hw-src-2": {0, 1, 0},
		},
	}))

	results, err := repo.Retrieve(ctx, types.RetrieveParams{
		Embedding:        []float32{1, 0, 0},
		KnowledgeBaseIDs: []string{"hw-kb-1"},
		TopK:             2,
		Threshold:        0.5,
		RetrieverType:    types.VectorRetrieverType,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NotEmpty(t, results[0].Results)
	require.Equal(t, "hw-chunk-1", results[0].Results[0].ChunkID)
	require.False(t, repo.vectorDistanceDisabled, "HeatWave distance path should stay enabled")
}

func chdirModuleRoot(t *testing.T) func() {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not find module root")
		dir = parent
	}

	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	return func() {
		require.NoError(t, os.Chdir(previous))
	}
}
