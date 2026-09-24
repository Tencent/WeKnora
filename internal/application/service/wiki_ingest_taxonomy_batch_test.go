package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/panjf2000/ants/v2"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// recordingEmbedder is a provider that records the size of every request it
// receives. It carries the REAL pooling implementation, so a caller that goes
// through BatchEmbedWithPool is split by BATCH_EMBED_SIZE exactly as in
// production, while a caller that reaches BatchEmbed directly shows up here as
// one oversized request.
type recordingEmbedder struct {
	embedding.EmbedderPooler
	mu    sync.Mutex
	sizes []int
}

func newRecordingEmbedder(t *testing.T) *recordingEmbedder {
	t.Helper()
	pool, err := ants.NewPool(4)
	if err != nil {
		t.Fatalf("ants.NewPool: %v", err)
	}
	t.Cleanup(pool.Release)
	return &recordingEmbedder{EmbedderPooler: embedding.NewBatchEmbedder(pool)}
}

func (e *recordingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.sizes = append(e.sizes, len(texts))
	e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, text := range texts {
		// One dimension per text, derived from the text itself, so a
		// misordered reassembly is visible in the result.
		out[i] = []float32{float32(len(text))}
	}
	return out, nil
}

func (e *recordingEmbedder) requestSizes() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]int(nil), e.sizes...)
}

func (e *recordingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text))}, nil
}
func (e *recordingEmbedder) GetModelName() string { return "recording" }
func (e *recordingEmbedder) GetDimensions() int   { return 1 }
func (e *recordingEmbedder) GetModelID() string   { return "recording" }

type recordingModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (s recordingModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return s.embedder, nil
}

// Taxonomy selection used to hand every folder, and every item, to the
// provider in a single request. A provider with a per-request input-count
// limit rejects that however low BATCH_EMBED_SIZE is set (#3390).
func TestSelectRelevantFoldersHonoursBatchEmbedSize(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "3")
	embedder := newRecordingEmbedder(t)
	svc := &wikiIngestService{modelService: recordingModelService{embedder: embedder}}

	// More folders than the feed-all threshold, so similarity selection runs.
	pool := make([][]string, 0, wikiTaxonomyFeedAllMaxFolders+20)
	for i := 0; i < wikiTaxonomyFeedAllMaxFolders+20; i++ {
		pool = append(pool, []string{fmt.Sprintf("L1-%d", i%4), fmt.Sprintf("deep-%d", i)})
	}
	items := make([]wikiTaxonomyItem, 0, 7)
	for i := 0; i < 7; i++ {
		items = append(items, wikiTaxonomyItem{
			slug:  fmt.Sprintf("entity/item-%d", i),
			title: fmt.Sprintf("item %d", i),
			about: "about",
		})
	}

	kb := &types.KnowledgeBase{EmbeddingModelID: "m"}
	selected := svc.selectRelevantFolders(context.Background(), kb, items, pool)

	sizes := embedder.requestSizes()
	if len(sizes) == 0 {
		t.Fatal("the provider was never called: this test no longer exercises similarity selection")
	}
	for _, size := range sizes {
		if size > 3 {
			t.Fatalf("a request carried %d inputs, above BATCH_EMBED_SIZE=3 (all sizes: %v)", size, sizes)
		}
	}
	// Both sides go through the pool: 80 folders and 7 items in threes.
	if total := sum(sizes); total != len(pool)+len(items) {
		t.Fatalf("provider saw %d inputs in total, want %d (folders+items)", total, len(pool)+len(items))
	}
	if len(selected) == 0 {
		t.Fatal("selection returned nothing")
	}
}

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}
