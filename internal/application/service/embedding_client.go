package service

// embedding_client.go — P2 strangler: the caller-side embedding seam over the
// unified invoke entry (design §6.4). The wire facets live in the invoke
// adapters; this file owns what the v1 internal/models/embedding package did
// ABOVE the wire:
//   - invokeEmbedder: the record-derived knobs (dimensions, dimension-override
//     gate, truncation budget) plus the v1 single-Embed empty-result retry;
//   - batchEmbedPooler: the BATCH_EMBED_SIZE sub-batch fan-out through the
//     process ants pool (v1 batch.go, behavior-identical port).
// Concurrency gating rides the executor's limiter (background-only, the same
// limiter.GateNamedN the v1 innermost wrapper used) and llm_debug / langfuse
// ride the invoke.Embed entry — no decorators here anymore.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/panjf2000/ants/v2"
)

// invokeEmbedder implements interfaces.Embedder for one model record.
type invokeEmbedder struct {
	cfg                       *invoke.ModelConfig
	dimensions                int
	supportsDimensionOverride bool
	truncatePromptTokens      int
	pooler                    interfaces.EmbedderPooler
}

// newInvokeEmbedder assembles the caller-side embedder from the shared
// constructor's ModelConfig and the record's EmbeddingParameters shard.
// Production (GetEmbeddingModel*) and test-connection paths share this.
func newInvokeEmbedder(
	cfg *invoke.ModelConfig,
	dimensions int,
	supportsDimensionOverride bool,
	truncatePromptTokens int,
	pooler interfaces.EmbedderPooler,
) *invokeEmbedder {
	return &invokeEmbedder{
		cfg:                       cfg,
		dimensions:                dimensions,
		supportsDimensionOverride: supportsDimensionOverride,
		truncatePromptTokens:      truncatePromptTokens,
		pooler:                    pooler,
	}
}

// Embed vectorizes one text. The v1 clients retried up to three times when
// the upstream answered without vectors (empty success), so the same loop
// lives here above the entry. It calls the raw entry (NOT BatchEmbed) so an
// empty success stays retriable rather than surfacing as a count error.
func (e *invokeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	for range 3 {
		resp, err := e.embedCall(ctx, []string{text})
		if err != nil {
			return nil, err
		}
		if len(resp.Vectors) > 0 {
			return resp.Vectors[0], nil
		}
	}
	return nil, fmt.Errorf("no embedding returned")
}

// BatchEmbed issues ONE embedding request carrying all texts (v1 vendor
// BatchEmbed semantics; per-vendor single-input fan-out happens inside the
// invoke.Embed entry for vendors whose API cannot take a batch) and enforces
// the v1 pooler's count contract (P2 review finding 4): a short response
// (e.g. weknoracloud/aliyun returning fewer vectors than inputs) fails
// loudly instead of silently corrupting chunk→vector alignment.
func (e *invokeEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := e.embedCall(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(resp.Vectors) != len(texts) {
		return nil, fmt.Errorf("embedding model returned %d embeddings for %d inputs",
			len(resp.Vectors), len(texts))
	}
	return resp.Vectors, nil
}

// embedCall is the raw single invoke.Embed round-trip shared by Embed's
// retry loop and BatchEmbed's validated path.
func (e *invokeEmbedder) embedCall(ctx context.Context, texts []string) (*invoke.EmbeddingResponse, error) {
	return invoke.Embed(ctx, e.cfg, &invoke.EmbeddingOptions{
		Inputs:                    texts,
		Dimensions:                e.dimensions,
		TruncatePromptTokens:      e.truncatePromptTokens,
		SupportsDimensionOverride: e.supportsDimensionOverride,
	})
}

// BatchEmbedWithPool delegates to the process pooler; threading THIS embedder
// down routes every per-sub-batch round-trip back through invoke.Embed (and
// thus through the executor's per-model slot), matching the v1 innermost
// wrapper placement. A nil pooler (test scaffolding) degrades to one direct
// call instead of panicking.
func (e *invokeEmbedder) BatchEmbedWithPool(
	ctx context.Context, model interfaces.Embedder, texts []string,
) ([][]float32, error) {
	if e.pooler == nil {
		return e.BatchEmbed(ctx, texts)
	}
	return e.pooler.BatchEmbedWithPool(ctx, model, texts)
}

// GetModelName returns the wire model name.
func (e *invokeEmbedder) GetModelName() string { return e.cfg.ModelName }

// GetDimensions returns the configured vector dimensions.
func (e *invokeEmbedder) GetDimensions() int { return e.dimensions }

// GetModelID returns the model record ID.
func (e *invokeEmbedder) GetModelID() string { return e.cfg.ModelID }

// --- pooler (v1 embedding/batch.go port) ---

type batchEmbedPooler struct {
	pool *ants.Pool
}

// NewBatchEmbedPooler builds the sub-batch pooler over the process ants pool.
func NewBatchEmbedPooler(pool *ants.Pool) interfaces.EmbedderPooler {
	return &batchEmbedPooler{pool: pool}
}

type textEmbedding struct {
	text    string
	results []float32
}

// BatchEmbedWithPool chunks the inputs into BATCH_EMBED_SIZE sub-batches
// (default 5) and submits one model.BatchEmbed call per sub-batch to the
// pool. First error wins; a count mismatch from any sub-batch fails the
// whole batch.
func (p *batchEmbedPooler) BatchEmbedWithPool(
	ctx context.Context, model interfaces.Embedder, texts []string,
) ([][]float32, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error // Record the first error that occurs
	batchSizeStr := os.Getenv("BATCH_EMBED_SIZE")
	if batchSizeStr == "" {
		batchSizeStr = "5"
	}
	batchSize, err := strconv.Atoi(batchSizeStr)
	if err != nil {
		return nil, err
	}
	textEmbeddings := utils.MapSlice(texts, func(text string) *textEmbedding {
		return &textEmbedding{text: text}
	})

	// Function to process each document chunk
	processChunk := func(texts []*textEmbedding) func() {
		return func() {
			defer wg.Done()
			// If an error has already occurred, don't continue processing
			if firstErr != nil {
				return
			}
			// Embed text
			embedding, err := model.BatchEmbed(ctx, utils.MapSlice(texts, func(text *textEmbedding) string {
				return text.text
			}))
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if len(embedding) != len(texts) {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("embedding model returned %d embeddings for %d inputs",
						len(embedding), len(texts))
				}
				mu.Unlock()
				return
			}
			// An empty vector (len 0) passes a count check but poisons the
			// vector-store insert (halfvec rejects 0-dimension rows) — treat
			// it as a sub-batch failure (2026-09-14 report).
			for i, vec := range embedding {
				if len(vec) == 0 {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("embedding model returned an empty vector for input %d", i)
					}
					mu.Unlock()
					return
				}
			}
			mu.Lock()
			for i, text := range texts {
				if text == nil {
					continue
				}
				text.results = embedding[i]
			}
			mu.Unlock()
		}
	}

	// Submit all tasks to the goroutine pool
	for _, texts := range utils.ChunkSlice(textEmbeddings, batchSize) {
		wg.Add(1)
		err := p.pool.Submit(processChunk(texts))
		if err != nil {
			return nil, err
		}
	}

	// Wait for all tasks to complete
	wg.Wait()

	// Check if any errors occurred
	if firstErr != nil {
		return nil, firstErr
	}

	results := utils.MapSlice(textEmbeddings, func(text *textEmbedding) []float32 {
		return text.results
	})
	return results, nil
}
