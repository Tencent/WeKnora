package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestVLMImageResultCacheRepository_PutIfAbsentDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&types.VLMImageResultCache{}); err != nil {
		t.Fatal(err)
	}

	repo := NewVLMImageResultCacheRepository(db)
	miss, err := repo.GetByKey(ctx, 1, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if miss != nil {
		t.Fatalf("expected nil miss, got %+v", miss)
	}

	first := &types.VLMImageResultCache{
		TenantID:                   1,
		CacheKey:                   "cache-key",
		ImageHash:                  "image-hash",
		ModelFingerprint:           "model-fp",
		ResultType:                 types.VLMImageResultTypeOCR,
		PromptVersion:              types.VLMOCRPromptVersion,
		PromptHash:                 "prompt-hash",
		ResultCanonicalizerVersion: types.VLMOCRCanonicalizerV1,
		Content:                    "first content",
	}
	inserted, err := repo.PutIfAbsent(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("expected first insert to report inserted")
	}

	second := *first
	second.ID = ""
	second.Content = "second content"
	inserted, err = repo.PutIfAbsent(ctx, &second)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("expected duplicate PutIfAbsent to report not inserted")
	}

	got, err := repo.GetByKey(ctx, 1, "cache-key")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Content != "first content" {
		t.Fatalf("duplicate PutIfAbsent overwrote content: %+v", got)
	}
}

func TestVLMImageResultCacheRepository_EmptyContentIsHit(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&types.VLMImageResultCache{}); err != nil {
		t.Fatal(err)
	}

	repo := NewVLMImageResultCacheRepository(db)
	_, err = repo.PutIfAbsent(ctx, &types.VLMImageResultCache{
		TenantID:                   1,
		CacheKey:                   "empty",
		ImageHash:                  "image-hash",
		ModelFingerprint:           "model-fp",
		ResultType:                 types.VLMImageResultTypeOCR,
		PromptVersion:              types.VLMOCRPromptVersion,
		PromptHash:                 "prompt-hash",
		ResultCanonicalizerVersion: types.VLMOCRCanonicalizerV1,
		Content:                    "",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByKey(ctx, 1, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("empty content should still be a cache hit")
	}
	if got.Content != "" {
		t.Fatalf("expected empty content, got %q", got.Content)
	}
}
