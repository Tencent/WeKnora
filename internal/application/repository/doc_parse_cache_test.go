package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDocParseCacheRepository_UpsertAndGet(t *testing.T) {
	db := setupChunkTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.DocParseCache{}))
	repo := NewDocParseCacheRepository(db)
	ctx := context.Background()

	cache := &types.DocParseCache{
		TenantID:    1,
		CacheKey:    "cache-key",
		ContentHash: "content-hash",
		Parser:      "builtin",
		ConfigHash:  "config-hash",
		SchemaVer:   types.DocParseCacheSchemaV1,
		Payload:     types.JSON(`{"MarkdownContent":"first"}`),
	}
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err := repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.JSONEq(t, `{"MarkdownContent":"first"}`, string(got.Payload))

	cache.Payload = types.JSON(`{"MarkdownContent":"updated"}`)
	require.NoError(t, repo.Upsert(ctx, cache))

	got, err = repo.GetByKey(ctx, 1, "cache-key")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.JSONEq(t, `{"MarkdownContent":"updated"}`, string(got.Payload))

	missing, err := repo.GetByKey(ctx, 2, "cache-key")
	require.NoError(t, err)
	require.Nil(t, missing)
}
