package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

type memoryEmbeddingCacheRepo struct {
	entries map[string]*types.EmbeddingCache
}

func (r *memoryEmbeddingCacheRepo) Get(
	_ context.Context, tenantID uint64, cacheKey string,
) (*types.EmbeddingCache, error) {
	entry := r.entries[cacheKey]
	if entry == nil || entry.TenantID != tenantID {
		return nil, nil
	}
	copy := *entry
	return &copy, nil
}

func (r *memoryEmbeddingCacheRepo) Upsert(_ context.Context, entry *types.EmbeddingCache) error {
	copy := *entry
	r.entries[entry.CacheKey] = &copy
	return nil
}

type countingEmbedder struct {
	poolCalls int
}

func (e *countingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{1, 2, 3}, nil
}

func (e *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i + 1), 2, 3}
	}
	return out, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	e.poolCalls++
	return e.BatchEmbed(ctx, texts)
}

func (e *countingEmbedder) GetModelName() string { return "test-model" }
func (e *countingEmbedder) GetDimensions() int   { return 3 }
func (e *countingEmbedder) GetModelID() string   { return "model-id" }

func TestBuildCachedEmbeddingsMissThenHit(t *testing.T) {
	repo := &memoryEmbeddingCacheRepo{entries: map[string]*types.EmbeddingCache{}}
	embedder := &countingEmbedder{}
	first := []*types.IndexInfo{{SourceID: "chunk-1", Content: "same content"}}
	_, hits, misses, err := buildCachedEmbeddings(
		context.Background(), repo, 42, embedder, "model-fingerprint", first,
	)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if hits != 0 || misses != 1 || embedder.poolCalls != 1 {
		t.Fatalf("unexpected first stats hits=%d misses=%d calls=%d", hits, misses, embedder.poolCalls)
	}
	if len(first[0].PrecomputedEmbedding) != 3 {
		t.Fatalf("first embedding not attached: %#v", first[0].PrecomputedEmbedding)
	}

	second := []*types.IndexInfo{{SourceID: "chunk-2", Content: "same content"}}
	_, hits, misses, err = buildCachedEmbeddings(
		context.Background(), repo, 42, embedder, "model-fingerprint", second,
	)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if hits != 1 || misses != 0 || embedder.poolCalls != 1 {
		t.Fatalf("cache miss on second build hits=%d misses=%d calls=%d", hits, misses, embedder.poolCalls)
	}
}

func TestEmbeddingCacheKeyScopesTenantAndModelFingerprint(t *testing.T) {
	embedder := &countingEmbedder{}
	a, _ := embeddingCacheKey(1, embedder, "model-a", "content")
	b, _ := embeddingCacheKey(2, embedder, "model-a", "content")
	c, _ := embeddingCacheKey(1, embedder, "model-b", "content")
	if a == b || a == c || b == c {
		t.Fatalf("cache keys are not isolated: %q %q %q", a, b, c)
	}
}

func TestStableChunkAndQuestionIDs(t *testing.T) {
	a := stableChunkID("knowledge", "text", "content")
	b := stableChunkID("knowledge", "text", "content")
	c := stableChunkID("knowledge", "text", "changed")
	if a != b || a == c {
		t.Fatalf("stable chunk IDs mismatch: %q %q %q", a, b, c)
	}
	q1 := stableGeneratedQuestions(a, []string{"What is cached?"})
	q2 := stableGeneratedQuestions(a, []string{"What is cached?"})
	if len(q1) != 1 || len(q2) != 1 || q1[0].ID != q2[0].ID {
		t.Fatalf("question IDs are not stable: %#v %#v", q1, q2)
	}
	if got := generatedQuestionSourceID(a, q1[0].ID); got == "" || len(got) > maxIndexSourceIDLength {
		t.Fatalf("generated question source id must fit VARCHAR(64), got %q (%d bytes)", got, len(got))
	}
	if got := generatedQuestionSourceID(a, stableChunkID("legacy-question")); got != "" {
		t.Fatalf("legacy overlong question source id must be ignored, got %q", got)
	}
}

func TestVolatileImageStorageURLsDoNotChangeCacheIdentity(t *testing.T) {
	a := `# title
![diagram](local://10000/exports/first_123.png)
<img alt="preview" src="minio://bucket/first.png">
<image url="local://10000/exports/first_123.png"><image_caption>same</image_caption></image>`
	b := `# title
![diagram](local://10000/exports/second_456.png)
<img alt="preview" src="minio://bucket/second.png">
<image url="local://10000/exports/second_456.png"><image_caption>same</image_caption></image>`

	normalizedA := normalizeCacheText(a)
	normalizedB := normalizeCacheText(b)
	if normalizedA != normalizedB {
		t.Fatalf("volatile image URLs changed cache identity:\nA=%q\nB=%q", normalizedA, normalizedB)
	}
	if normalizedA != `# title
![diagram]([image])
<img alt="preview" src="[image]">
<image url="[image]"><image_caption>same</image_caption></image>` {
		t.Fatalf("unexpected canonical image form: %q", normalizedA)
	}
	if stableChunkID("knowledge", normalizeCacheText(a)) != stableChunkID("knowledge", normalizeCacheText(b)) {
		t.Fatal("volatile image URLs changed stable chunk id")
	}

	embedder := &countingEmbedder{}
	keyA, _ := embeddingCacheKey(42, embedder, "model-fingerprint", a)
	keyB, _ := embeddingCacheKey(42, embedder, "model-fingerprint", b)
	if keyA != keyB {
		t.Fatalf("volatile image URLs changed embedding cache key: %q != %q", keyA, keyB)
	}
}

func TestPlanChunkDiffPreservesExistingIdentity(t *testing.T) {
	now := time.Now()
	created := now.Add(-time.Hour)
	existing := &types.Chunk{ID: "stable", Content: "same", ContentHash: stableContentHash("same"), SeqID: 9, CreatedAt: created}
	desired := &types.Chunk{ID: "stable", Content: "same", ContentHash: stableContentHash("same")}
	plan := planChunkDiff([]*types.Chunk{existing}, []*types.Chunk{desired}, now)
	if len(plan.toCreate) != 0 || len(plan.toDelete) != 0 || !plan.reused["stable"] {
		t.Fatalf("unexpected diff plan: %#v", plan)
	}
	if desired.SeqID != 9 || !desired.CreatedAt.Equal(created) {
		t.Fatalf("identity not preserved: seq=%d created=%s", desired.SeqID, desired.CreatedAt)
	}
}

func TestMaterializeWikiUpdatesUsesLiveState(t *testing.T) {
	additions := []SlugUpdate{{Slug: "entity/cache", Type: types.WikiPageTypeEntity, DocTitle: "old"}}
	pages := []types.WikiLogPageRef{{Slug: "entity/cache", Title: "Cache"}}
	updates, overlap, stale := materializeWikiUpdates(
		additions, pages,
		map[string]bool{"entity/cache": true, "entity/stale": true},
		"current prior contribution", "current document", "new title", "kid", "kid", "English",
	)
	if overlap != 1 || stale != 1 || len(updates) != 3 {
		t.Fatalf("unexpected reconciliation overlap=%d stale=%d updates=%#v", overlap, stale, updates)
	}
	if updates[0].DocTitle != "new title" || updates[0].KnowledgeID != "kid" {
		t.Fatalf("cached addition was not rebound: %#v", updates[0])
	}
	byType := map[string]SlugUpdate{}
	for _, update := range updates[1:] {
		byType[update.Type] = update
	}
	if byType["retract"].RetractDocContent != "current prior contribution" {
		t.Fatalf("overlap did not use live prior contribution: %#v", byType["retract"])
	}
	if byType["retractStale"].RetractDocContent != "current document" {
		t.Fatalf("stale retract did not use current content: %#v", byType["retractStale"])
	}
}

func TestArtifactCompressionRoundTrip(t *testing.T) {
	want := types.ReadResult{
		MarkdownContent: "# hello",
		ImageRefs:       []types.ImageRef{{Filename: "a.png", ImageData: []byte{1, 2, 3}}},
		Metadata:        map[string]string{"pages": "1"},
	}
	data, err := encodeArtifact(&want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got types.ReadResult
	if err := decodeArtifact(data, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip mismatch\nwant=%#v\n got=%#v", want, got)
	}
	if !isCacheableReadResult(&got) {
		t.Fatal("self-contained parse result should be cacheable")
	}
	got.ImageRefs[0].ImageData = nil
	if isCacheableReadResult(&got) {
		t.Fatal("temp-file-only image result must not be cacheable")
	}
}
