package retriever

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
	"strings"
	"testing"
)

type cacheTestStore struct {
	rows map[string]*processingcache.Record
}

func (s *cacheTestStore) Get(_ context.Context, t uint64, scope, stage, key string) (*processingcache.Record, error) {
	r := s.rows[scope+stage+key]
	if r == nil || r.TenantID != t {
		return nil, processingcache.ErrMiss
	}
	return r, nil
}
func (s *cacheTestStore) PutIfAbsent(_ context.Context, r *processingcache.Record) (*processingcache.Record, error) {
	s.rows[r.ScopeID+r.Stage+r.Key] = r
	return r, nil
}
func (s *cacheTestStore) Delete(_ context.Context, r *processingcache.Record) error {
	delete(s.rows, r.ScopeID+r.Stage+r.Key)
	return nil
}

type cacheTestEmbedder struct {
	embedding.Embedder
	calls       int
	singleCalls int
	batchCalls  int
	identity    string
	bad         bool
	short       bool
	dimension   int
}

func (e *cacheTestEmbedder) ProcessingCacheIdentity() string { return e.identity }
func (e *cacheTestEmbedder) GetDimensions() int {
	if e.dimension > 0 {
		return e.dimension
	}
	return 1
}
func (e *cacheTestEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.calls++
	e.singleCalls++
	return []float32{float32(len(text))}, nil
}
func (e *cacheTestEmbedder) BatchEmbedWithPool(_ context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	e.calls += len(texts)
	e.batchCalls++
	out := make([][]float32, len(texts))
	for i, s := range texts {
		out[i] = []float32{float32(len(s))}
		if e.bad {
			out[i] = nil
		}
	}
	if e.short {
		return out[:len(out)-1], nil
	}
	return out, nil
}
func cacheRuntime(s *cacheTestStore) *processingcache.Runtime {
	return processingcache.New(s, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
}

func TestProcessingCacheEmbeddingUsesStableImageText(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "model"}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
	service.SetProcessingCache(cacheRuntime(store))
	for _, url := range []string{"resource://old", "resource://a-much-longer-new-location"} {
		row := &types.IndexInfo{SourceID: "current", KnowledgeID: "doc", Content: "![diagram](" + url + ") <image url=\"" + url + "\">caption</image>"}
		if err := service.BatchIndex(ctx, model, []*types.IndexInfo{row}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(row.Content, url) {
			t.Fatal("modified indexed content")
		}
	}
	if model.calls != 1 {
		t.Fatalf("volatile image locations caused %d calls", model.calls)
	}
}

func TestProcessingCacheEmbeddingImageDestinationSyntax(t *testing.T) {
	service := &KeywordsVectorHybridRetrieveEngineService{}
	service.SetProcessingCache(cacheRuntime(&cacheTestStore{rows: map[string]*processingcache.Record{}}))
	for _, input := range []string{`![diagram](https://h/a(old).png "title")`, `![diagram](<https://h/a(new).png> "title")`, `![diagram](https://h/a\(new\).png "title")`} {
		if got := service.embeddingText(context.Background(), input); got != `![diagram](image "title")` {
			t.Fatalf("%s -> %s", input, got)
		}
	}
}
func TestProcessingCacheRestartScopeOrderAndCurrentIDs(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "model-a"}
	rows := []*types.IndexInfo{{SourceID: "a", ChunkID: "old", KnowledgeID: "doc-a", Content: "one"}, {SourceID: "b", KnowledgeID: "doc-b", Content: "x"}, {SourceID: "c", KnowledgeID: "doc-a", Content: "one"}}
	for run := 0; run < 2; run++ {
		repo := &recordingRepository{}
		service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
		service.SetProcessingCache(cacheRuntime(store))
		if run == 1 {
			rows[0].SourceID = "new"
			rows[0].ChunkID = "new"
		}
		if err := service.BatchIndex(ctx, model, rows, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if got := repo.embeddings[row.SourceID]; len(got) != 1 || got[0] != float32(len(row.Content)) {
				t.Fatalf("wrong current row vector: %v", repo.embeddings)
			}
		}
	}
	if model.calls != 2 {
		t.Fatalf("computed %d inputs, want 2", model.calls)
	}
	model.identity = "model-b"
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
	service.SetProcessingCache(cacheRuntime(store))
	if err := service.BatchIndex(ctx, model, rows[:1], []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatal(err)
	}
	if model.calls != 3 {
		t.Fatal("changed model identity reused old vector")
	}
}
func TestProcessingCacheInvalidVectorsNotPersisted(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
	service.SetProcessingCache(cacheRuntime(store))
	err := service.BatchIndex(ctx, &cacheTestEmbedder{identity: "a", bad: true}, []*types.IndexInfo{{KnowledgeID: "a", Content: "x"}}, []types.RetrieverType{types.VectorRetrieverType})
	if err == nil || len(store.rows) != 0 {
		t.Fatal("invalid vector was accepted or persisted")
	}
}

func TestProcessingCacheSingleIndexUsesEmbedAndStillWrites(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "model"}
	repo := &cacheIndexRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	service.SetProcessingCache(cacheRuntime(store))
	for _, id := range []string{"old", "new"} {
		if err := service.Index(ctx, model, &types.IndexInfo{SourceID: id, ChunkID: id, KnowledgeID: "doc", Content: "hello"}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
	}
	if model.calls != 1 || model.singleCalls != 1 || model.batchCalls != 0 || len(repo.ids) != 2 || repo.ids[1] != "new" {
		t.Fatalf("calls=%d single=%d batch=%d saved=%v", model.calls, model.singleCalls, model.batchCalls, repo.ids)
	}
}

type cacheIndexRepository struct {
	saveOnlyRepository
	ids []string
}

func (r *cacheIndexRepository) Save(_ context.Context, row *types.IndexInfo, params map[string]any) error {
	r.ids = append(r.ids, row.SourceID)
	vector := params["embedding"].(map[string][]float32)[row.SourceID]
	if len(vector) != 1 || vector[0] != 5 {
		return fmt.Errorf("incorrect current vector: %v", params)
	}
	return nil
}
func TestProcessingCacheMissingIdentityTenantScopeBypass(t *testing.T) {
	for _, test := range []struct {
		name, identity, scope string
		tenant                bool
	}{{"tenant", "a", "doc", false}, {"identity", "", "doc", true}, {"scope", "a", "", true}} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.tenant {
				ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
			}
			store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
			model := &cacheTestEmbedder{identity: test.identity}
			service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
			service.SetProcessingCache(cacheRuntime(store))
			for i := 0; i < 2; i++ {
				if err := service.BatchIndex(ctx, model, []*types.IndexInfo{{KnowledgeID: test.scope, Content: "x"}}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
					t.Fatal(err)
				}
			}
			if model.calls != 2 || len(store.rows) != 0 {
				t.Fatalf("bypass computed %d, persisted %d", model.calls, len(store.rows))
			}
		})
	}
}
func TestProcessingCachePrecomputedBypass(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "model"}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	service.SetProcessingCache(cacheRuntime(store))
	rows := []*types.IndexInfo{{SourceID: "image", KnowledgeID: "doc", Content: "caption"}}
	if err := service.BatchIndex(ctx, &precomputedEmbedder{Embedder: model, vectors: [][]float32{{42}}}, rows, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 || len(store.rows) != 0 || repo.embeddings["image"][0] != 42 {
		t.Fatal("image path did not bypass cache")
	}
}
func TestProcessingCacheCorruptPayloadRecomputes(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "model"}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
	service.SetProcessingCache(cacheRuntime(store))
	rows := []*types.IndexInfo{{SourceID: "a", KnowledgeID: "doc", Content: "hello"}}
	run := func() {
		t.Helper()
		if err := service.BatchIndex(ctx, model, rows, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
	}
	run()
	for _, invalid := range []string{"[null]", "[1,2]", "[1e100]", "{}", "null"} {
		for _, row := range store.rows {
			row.Payload = []byte(invalid)
			sum := sha256.Sum256(row.Payload)
			row.Checksum = hex.EncodeToString(sum[:])
		}
		before := model.calls
		run()
		if model.calls != before+1 {
			t.Fatalf("reused invalid payload %s", invalid)
		}
	}
}
func TestProcessingCacheTenantAndDocumentIsolation(t *testing.T) {
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "model"}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
	service.SetProcessingCache(cacheRuntime(store))
	for _, test := range []struct {
		tenant uint64
		doc    string
	}{{1, "a"}, {1, "b"}, {2, "b"}} {
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, test.tenant)
		if err := service.BatchIndex(ctx, model, []*types.IndexInfo{{KnowledgeID: test.doc, Content: "same"}}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
	}
	if model.calls != 3 {
		t.Fatalf("crossed isolation boundary: %d", model.calls)
	}
}

func TestProcessingCacheRejectsWrongVectorCount(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "model", short: true}
	repo := &recordingRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	service.SetProcessingCache(cacheRuntime(store))
	if err := service.BatchIndex(ctx, model, []*types.IndexInfo{{KnowledgeID: "a", Content: "x"}, {KnowledgeID: "a", Content: "y"}}, []types.RetrieverType{types.VectorRetrieverType}); err == nil {
		t.Fatal("accepted wrong vector count")
	}
	if len(store.rows) != 0 || len(repo.rows) != 0 {
		t.Fatal("invalid results were persisted")
	}
}

func TestProcessingCacheSanitizedActualText(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "a"}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
	service.SetProcessingCache(cacheRuntime(store))
	for _, payload := range []string{strings.Repeat("A", 300), strings.Repeat("B", 300)} {
		content := "before ![a](data:image/png;base64," + payload + ") after"
		if err := service.BatchIndex(ctx, model, []*types.IndexInfo{{KnowledgeID: "doc", Content: content}}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatal(err)
		}
	}
	if model.calls != 1 {
		t.Fatalf("sanitized equivalent texts computed %d times", model.calls)
	}
}
func TestProcessingCacheEffectiveDimensionInvalidates(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	model := &cacheTestEmbedder{identity: "a"}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &recordingRepository{}}
	service.SetProcessingCache(cacheRuntime(store))
	rows := []*types.IndexInfo{{KnowledgeID: "doc", Content: "x"}}
	if err := service.BatchIndex(ctx, model, rows, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatal(err)
	}
	model.dimension = 2
	if err := service.BatchIndex(ctx, model, rows, []types.RetrieverType{types.VectorRetrieverType}); err == nil || model.calls != 2 {
		t.Fatal("effective dimension reused stale vector")
	}
}

type cacheWindowEmbedder struct{ *windowEmbedder }

func (e *cacheWindowEmbedder) ProcessingCacheIdentity() string { return "window-model" }
func (e *cacheWindowEmbedder) GetDimensions() int              { return 1 }
func TestProcessingCachePreservesBoundedWindows(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	store := &cacheTestStore{rows: map[string]*processingcache.Record{}}
	repo := &windowRepository{seen: map[string]bool{}}
	model := &cacheWindowEmbedder{windowEmbedder: &windowEmbedder{repo: repo}}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	service.SetProcessingCache(cacheRuntime(store))
	rows := windowRows(437)
	for _, row := range rows {
		row.KnowledgeID = "doc"
	}
	if err := service.BatchIndex(ctx, model, rows, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatal(err)
	}
	if repo.saved != 437 || model.calls != 3 {
		t.Fatalf("saved=%d windows=%d", repo.saved, model.calls)
	}
}
