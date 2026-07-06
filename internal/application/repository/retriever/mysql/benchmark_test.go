package mysql

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	benchDimension = 768
	benchRows      = 1000
)

func BenchmarkMySQLRetrieverVectorRetrieve(b *testing.B) {
	dsn := os.Getenv("WEKNORA_MYSQL_BENCH_DSN")
	if dsn == "" {
		dsn = os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	}
	if dsn == "" {
		b.Skip("set WEKNORA_MYSQL_BENCH_DSN to run MySQL retriever benchmarks")
	}

	repo, db, table := newBenchmarkRepository(b, dsn)
	defer func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent(table)).Error
	}()

	ctx := context.Background()
	query := benchmarkVector(0, benchDimension)
	items := benchmarkIndexInfos(benchRows)
	params := benchmarkIndexParams(items, benchDimension)

	require.NoError(b, repo.BatchSave(ctx, items, params))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := repo.Retrieve(ctx, benchmarkRetrieveParams(query))
		if err != nil {
			b.Fatal(err)
		}
		if len(results) != 1 || len(results[0].Results) == 0 {
			b.Fatalf("expected retrieve results, got %#v", results)
		}
	}
}

func BenchmarkMySQLRetrieverBatchSave(b *testing.B) {
	dsn := os.Getenv("WEKNORA_MYSQL_BENCH_DSN")
	if dsn == "" {
		dsn = os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	}
	if dsn == "" {
		b.Skip("set WEKNORA_MYSQL_BENCH_DSN to run MySQL retriever benchmarks")
	}

	repo, db, table := newBenchmarkRepository(b, dsn)
	defer func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent(table)).Error
	}()

	ctx := context.Background()
	items := benchmarkIndexInfos(benchRows)
	params := benchmarkIndexParams(items, benchDimension)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := repo.BatchSave(ctx, items, params); err != nil {
			b.Fatal(err)
		}
	}
}

func newBenchmarkRepository(b *testing.B, dsn string) (*mysqlRepository, *gorm.DB, string) {
	b.Helper()

	cfg, err := mysqlDriver.ParseDSN(dsn)
	require.NoError(b, err)
	require.NotEmpty(b, cfg.DBName, "WEKNORA_MYSQL_BENCH_DSN must include a database name")
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.InterpolateParams = true

	db, err := gorm.Open(gormmysql.Open(cfg.FormatDSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
		Logger:  gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(b, err)
	sqlDB, err := db.DB()
	require.NoError(b, err)
	b.Cleanup(func() {
		_ = sqlDB.Close()
	})
	require.NoError(b, sqlDB.Ping())

	tablePrefix := "weknora_bench_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	repo := NewMySQLRetrieveEngineRepository(db, cfg.DBName, nil).(*mysqlRepository)
	repo.tablePrefix = tablePrefix
	return repo, db, tableName(tablePrefix, benchDimension)
}

func benchmarkRetrieveParams(query []float32) types.RetrieveParams {
	return types.RetrieveParams{
		Embedding:        query,
		KnowledgeBaseIDs: []string{"kb-bench"},
		TopK:             10,
		Threshold:        0,
		RetrieverType:    types.VectorRetrieverType,
	}
}

func benchmarkIndexInfos(n int) []*types.IndexInfo {
	items := make([]*types.IndexInfo, n)
	for i := 0; i < n; i++ {
		items[i] = &types.IndexInfo{
			ID:              uuid.NewString(),
			SourceID:        "source-" + uuid.NewString(),
			SourceType:      types.ChunkSourceType,
			ChunkID:         "chunk-" + uuid.NewString(),
			KnowledgeID:     "knowledge-bench",
			KnowledgeBaseID: "kb-bench",
			TagID:           "tag-bench",
			Content:         "benchmark mysql vector retrieval content",
			IsEnabled:       true,
		}
	}
	return items
}

func benchmarkIndexParams(items []*types.IndexInfo, dim int) map[string]any {
	embeddings := make(map[string][]float32, len(items))
	for i, item := range items {
		embeddings[item.SourceID] = benchmarkVector(i, dim)
	}
	return map[string]any{fieldEmbedding: embeddings}
}

func benchmarkVector(seed, dim int) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32((seed+i)%23+1) / 23
	}
	return vec
}
