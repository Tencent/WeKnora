package memory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The SQL ranking is the path production takes, and it is the one path the
// SQLite tests cannot reach: halfvec, the distance operator and the join all
// only exist on PostgreSQL. Without this, a change that breaks the query would
// pass every test and fail every real recall.
//
// Opt in with a disposable database that has pgvector installed.
func TestVectorSearchRanksInPostgres(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MEMORY_TEST_POSTGRES_DSN to run PostgreSQL integration tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlAdmin, err := admin.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlAdmin.Close()) }()
	require.NoError(t, admin.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, admin.Exec(`CREATE EXTENSION IF NOT EXISTS vector`).Error)
	schema := "memory_vec_" + uuid.NewString()[:8]
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")

	db, err := gorm.Open(
		postgres.Open(dsn+" search_path="+schema+",public"),
		&gorm.Config{Logger: logger.Discard},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	require.NoError(t, db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY)").Error)
	require.NoError(t, db.Exec("CREATE TABLE messages (id VARCHAR(36) PRIMARY KEY)").Error)
	execMemoryMigration(t, db, "../../../../migrations/versioned/000084_memory.up.sql")
	execMemoryMigration(t, db, "../../../../migrations/versioned/000094_memory_consistency.up.sql")
	// Whole-file exec: the migration is one DO block, so splitting it on
	// semicolons the way execMemoryMigration does would cut it apart.
	vectorSearchMigration, err := os.ReadFile(
		"../../../../migrations/versioned/000095_memory_vector_search.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(vectorSearchMigration)).Error)
	require.True(t, db.Migrator().HasColumn(&types.MemoryItemEmbedding{}, "embedding"),
		"the migration has to add the column the ranking sorts by")

	repo := repository.NewMemoryRepository(db)
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "alice"}
	_, err = repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)

	seed := []struct {
		content string
		kind    string
		vector  []float32
		expires *time.Time
	}{
		{content: "回答直接给结论", kind: types.MemoryKindFact, vector: []float32{1, 0, 0}},
		{content: "生产库的连接池配置", kind: types.MemoryKindFact, vector: []float32{0, 1, 0}},
		{content: "本周的评审安排", kind: types.MemoryKindTask, vector: []float32{0.99, 0.1, 0}},
	}
	past := time.Now().Add(-time.Hour)
	seed[2].expires = &past

	ids := make([]string, 0, len(seed))
	for _, row := range seed {
		item := &types.MemoryItem{
			ID: uuid.NewString(), TenantID: scope.TenantID, SubjectID: scope.SubjectID,
			Kind: row.kind, Content: row.content, Topic: row.content,
			NormalizedKey: row.content, Status: types.MemoryStatusActive,
			ValidFrom: time.Now(), ExpiresAt: row.expires,
		}
		require.NoError(t, repo.SaveItem(ctx, scope, item, ""))
		require.NoError(t, repo.UpsertItemEmbedding(ctx, scope, &types.MemoryItemEmbedding{
			ItemID: item.ID, ModelID: "embed-1",
			Dims: len(row.vector), Vector: types.EncodeEmbedding(row.vector),
		}))
		ids = append(ids, item.ID)
	}

	// The upsert has to populate the vector column, not only the blob: a row
	// the ranking cannot see is a memory recall cannot find.
	var ranked int64
	require.NoError(t, db.Table("memory_item_embeddings").
		Where("embedding IS NOT NULL").Count(&ranked).Error)
	require.EqualValues(t, len(seed), ranked)

	hits, err := repo.SearchItemsByVector(ctx, scope, interfaces.MemoryVectorQuery{
		ModelID:  "embed-1",
		Vector:   []float32{0.98, 0.2, 0},
		MinScore: minCosine,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, hits, 1, "the expired task scores highest and still must not be a match")
	require.Equal(t, ids[0], hits[0].Item.ID)
	require.Greater(t, hits[0].Score, 0.9)

	// A vector written before the column existed still has to become
	// searchable, and without spending an embedding call to rediscover a
	// number that is already stored.
	require.NoError(t, db.Exec(
		"UPDATE memory_item_embeddings SET embedding = NULL WHERE item_id = ?", ids[0]).Error)
	moved, err := repo.SyncVectorColumn(ctx, scope, 100)
	require.NoError(t, err)
	require.Equal(t, 1, moved)
	hits, err = repo.SearchItemsByVector(ctx, scope, interfaces.MemoryVectorQuery{
		ModelID: "embed-1", Vector: []float32{0.98, 0.2, 0}, MinScore: minCosine, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, ids[0], hits[0].Item.ID)
}
