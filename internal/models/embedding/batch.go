package embedding

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/panjf2000/ants/v2"
)

type batchEmbedder struct {
	pool *ants.Pool
}

func NewBatchEmbedder(pool *ants.Pool) EmbedderPooler {
	return &batchEmbedder{pool: pool}
}

type textEmbedding struct {
	text    string
	results []float32
}

func (e *batchEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	// Create goroutine pool for concurrent processing of document chunks
	var wg sync.WaitGroup
	var mu sync.Mutex  // For synchronizing access to error
	var firstErr error // Record the first error that occurs
	batchSizeStr := os.Getenv("BATCH_EMBED_SIZE")
	if batchSizeStr == "" {
		batchSizeStr = "5"
	}
	batchSize, err := strconv.Atoi(batchSizeStr)
	if err != nil || batchSize <= 0 {
		return nil, fmt.Errorf("BATCH_EMBED_SIZE must be a positive integer, got %q", batchSizeStr)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Wait for every submitted callback before returning, including on cancel
	// or pool submission failure. Otherwise callbacks can outlive this call.
	defer wg.Wait()
	textEmbeddings := utils.MapSlice(texts, func(text string) *textEmbedding {
		return &textEmbedding{text: text}
	})

	// Function to process each document chunk
	processChunk := func(texts []*textEmbedding) func() {
		return func() {
			defer wg.Done()
			// If an error has already occurred, don't continue processing
			mu.Lock()
			failed := firstErr != nil
			mu.Unlock()
			if failed || ctx.Err() != nil {
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
					firstErr = fmt.Errorf("embedding model returned %d embeddings for %d inputs", len(embedding), len(texts))
				}
				mu.Unlock()
				return
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
	intervalStr := os.Getenv("EMBED_BATCH_INTERVAL_MS")
	var interval time.Duration
	if intervalStr != "" {
		if n, err := strconv.Atoi(intervalStr); err == nil && n > 0 && int64(n) <= int64((1<<63-1)/time.Millisecond) {
			interval = time.Duration(n) * time.Millisecond
		}
	}
	for i, texts := range utils.ChunkSlice(textEmbeddings, batchSize) {
		mu.Lock()
		failed := firstErr != nil
		mu.Unlock()
		if failed {
			break
		}
		if i > 0 && interval > 0 {
			select {
			case <-time.After(interval):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wg.Add(1)
		err := e.pool.Submit(processChunk(texts))
		if err != nil {
			wg.Done() // No callback will run for a rejected submission.
			return nil, err
		}
	}

	// Wait for all tasks to complete
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Check if any errors occurred
	if firstErr != nil {
		return nil, firstErr
	}

	results := utils.MapSlice(textEmbeddings, func(text *textEmbedding) []float32 {
		return text.results
	})
	return results, nil
}
