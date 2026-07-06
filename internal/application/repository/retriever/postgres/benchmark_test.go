package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	benchDimension = 768
	benchRows      = 1000
)

func BenchmarkPostgresRetrieverVectorRetrieve(b *testing.B) {
	dsn := os.Getenv("WEKNORA_POSTGRES_BENCH_DSN")
	if dsn == "" {
		b.Skip("set WEKNORA_POSTGRES_BENCH_DSN to run PostgreSQL retriever benchmarks")
	}

	repo, db, schema := newBenchmarkRepository(b, dsn)
	defer func() {
		_ = db.Exec("DROP SCHEMA IF EXISTS " + quotePGIdent(schema) + " CASCADE").Error
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

func BenchmarkPostgresRetrieverBatchSave(b *testing.B) {
	dsn := os.Getenv("WEKNORA_POSTGRES_BENCH_DSN")
	if dsn == "" {
		b.Skip("set WEKNORA_POSTGRES_BENCH_DSN to run PostgreSQL retriever benchmarks")
	}

	repo, db, schema := newBenchmarkRepository(b, dsn)
	defer func() {
		_ = db.Exec("DROP SCHEMA IF EXISTS " + quotePGIdent(schema) + " CASCADE").Error
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

func newBenchmarkRepository(b *testing.B, dsn string) (*pgRepository, *gorm.DB, string) {
	b.Helper()

	schema := "weknora_bench_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
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

	setupBenchmarkSchema(b, db, schema)
	repo := NewPostgresRetrieveEngineRepository(db).(*pgRepository)
	return repo, db, schema
}

func setupBenchmarkSchema(b *testing.B, db *gorm.DB, schema string) {
	b.Helper()

	require.NoError(b, db.Exec(`CREATE EXTENSION IF NOT EXISTS vector`).Error)
	require.NoError(b, db.Exec(`CREATE SCHEMA `+quotePGIdent(schema)).Error)
	require.NoError(b, db.Exec(`SET search_path TO `+quotePGIdent(schema)+`, public`).Error)
	require.NoError(b, db.Exec(`CREATE TABLE embeddings (
		id SERIAL PRIMARY KEY,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		source_id VARCHAR(64) NOT NULL,
		source_type INTEGER NOT NULL,
		chunk_id VARCHAR(64),
		knowledge_id VARCHAR(64),
		knowledge_base_id VARCHAR(64),
		tag_id VARCHAR(64),
		content TEXT,
		dimension INTEGER NOT NULL,
		embedding halfvec,
		is_enabled BOOLEAN DEFAULT TRUE
	)`).Error)
	require.NoError(b, db.Exec(`CREATE UNIQUE INDEX embeddings_unique_source ON embeddings(source_id, source_type)`).Error)
	require.NoError(b, db.Exec(`CREATE INDEX embeddings_embedding_idx_768 ON embeddings
		USING hnsw ((embedding::halfvec(768)) halfvec_cosine_ops)
		WITH (m = 16, ef_construction = 64)
		WHERE (dimension = 768)`).Error)
}

func quotePGIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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
			SourceID:        fmt.Sprintf("source-%d-%s", i, uuid.NewString()),
			SourceType:      types.ChunkSourceType,
			ChunkID:         fmt.Sprintf("chunk-%d-%s", i, uuid.NewString()),
			KnowledgeID:     "knowledge-bench",
			KnowledgeBaseID: "kb-bench",
			TagID:           "tag-bench",
			Content:         "benchmark postgres vector retrieval content",
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
	return map[string]any{"embedding": embeddings}
}

func benchmarkVector(seed, dim int) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32((seed+i)%23+1) / 23
	}
	return vec
}
