package retriever

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/processingcache/gormstore"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProcessingCacheReopenDatabaseBindsCurrentChunk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	repo := &cacheIndexRepository{}
	for attempt, id := range []string{"old-chunk", "new-chunk"} {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { raw.Close() })
		if err := db.AutoMigrate(&gormstore.Model{}); err != nil {
			t.Fatal(err)
		}
		model := &cacheTestEmbedder{identity: "same-provider-config"}
		engine := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
		engine.SetProcessingCache(processingcache.New(gormstore.New(db), processingcache.Options{
			ReadEnabled: true, WriteEnabled: true, TTL: time.Hour, MaxBytes: 1024,
		}))
		if err := engine.Index(ctx, model, &types.IndexInfo{SourceID: id, ChunkID: id, KnowledgeID: "document", Content: "hello"}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
		if model.calls != 1-attempt {
			t.Fatalf("attempt %d made %d provider calls", attempt, model.calls)
		}
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.ids) != 2 || repo.ids[1] != "new-chunk" {
		t.Fatalf("saved IDs: %v", repo.ids)
	}
}

func TestProcessingCacheCrossDocumentReuseSurvivesSourceDeletion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "shared.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	defer raw.Close()
	if err := db.AutoMigrate(&gormstore.Model{}); err != nil {
		t.Fatal(err)
	}
	store := gormstore.New(db)
	model := &cacheTestEmbedder{identity: "shared-model"}
	engine := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &cacheIndexRepository{}}
	engine.SetProcessingCache(processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true, TTL: time.Hour}))
	index := func(tenant uint64, doc string) {
		t.Helper()
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenant)
		if err := engine.Index(ctx, model, &types.IndexInfo{SourceID: doc, ChunkID: doc, KnowledgeID: doc, Content: "hello"}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
	}
	index(1, "a")
	index(1, "b")
	if model.calls != 1 {
		t.Fatalf("same tenant computed %d times", model.calls)
	}
	if err := store.DeleteScope(context.Background(), 1, "a"); err != nil {
		t.Fatal(err)
	}
	index(1, "b")
	index(1, "c")
	if model.calls != 1 {
		t.Fatalf("source deletion discarded consumer artifact: %d", model.calls)
	}
	index(2, "d")
	if model.calls != 2 {
		t.Fatal("cross-tenant reuse")
	}
}
