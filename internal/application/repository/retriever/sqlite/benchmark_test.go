package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	benchDimension = 768
	benchRows      = 1000
)

var sqliteVecAutoOnce sync.Once

func BenchmarkSQLiteRetrieverVectorRetrieve(b *testing.B) {
	repo, cleanup := newBenchmarkRepository(b)
	defer cleanup()

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

func BenchmarkSQLiteRetrieverBatchSave(b *testing.B) {
	repo, cleanup := newBenchmarkRepository(b)
	defer cleanup()

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

func newBenchmarkRepository(b *testing.B) (*sqliteRepository, func()) {
	b.Helper()

	dbPath := filepath.Join(b.TempDir(), "sqlite-retriever-bench.db")
	sqliteVecAutoOnce.Do(sqlite_vec.Auto)
	db, err := gorm.Open(gormsqlite.Open(dbPath), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
		Logger:  gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(b, err)
	sqlDB, err := db.DB()
	require.NoError(b, err)
	require.NoError(b, sqlDB.Ping())

	repo := NewSQLiteRetrieveEngineRepository(db).(*sqliteRepository)
	return repo, func() {
		_ = sqlDB.Close()
	}
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
			Content:         "benchmark sqlite vector retrieval content",
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
