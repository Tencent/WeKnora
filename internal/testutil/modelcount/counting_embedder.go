package modelcount

import (
	"context"
	"errors"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

// EmbedderCall records one request that entered CountingEmbedder.BatchEmbed.
//
// The original text is deliberately not retained. Tests only need the batch
// size and total character count to verify that a real model-interface call
// occurred.
type EmbedderCall struct {
	Operation  types.IngestionOperation
	BatchSize  int
	InputChars int
}

// EmbedderSnapshot is an immutable copy of the requests observed by
// CountingEmbedder.
//
// Snapshot values may be safely read while other goroutines continue calling
// Embed or BatchEmbed.
type EmbedderSnapshot struct {
	RequestCount    int
	BatchSizes      []int
	TotalInputItems int
	TotalInputChars int
	Dimensions      int
	Calls           []EmbedderCall
}

// CountingEmbedder is a thread-safe test implementation of embedding.Embedder.
//
// It records calls entering BatchEmbed and returns deterministic vectors
// without contacting an external embedding provider.
type CountingEmbedder struct {
	mu sync.Mutex

	modelID    string
	modelName  string
	dimensions int
	pooler     embedding.EmbedderPooler

	defaultError error

	failOnRequest int
	failError     error

	requestCount    int
	batchSizes      []int
	totalInputItems int
	totalInputChars int
	calls           []EmbedderCall
}

var _ embedding.Embedder = (*CountingEmbedder)(nil)

// CountingEmbedderOptions configures a CountingEmbedder.
type CountingEmbedderOptions struct {
	ModelID    string
	ModelName  string
	Dimensions int
	Pooler     embedding.EmbedderPooler

	// DefaultError makes every Embed or BatchEmbed request fail.
	DefaultError error

	// FailOnRequest makes the Nth BatchEmbed request fail.
	// Zero disables it.
	FailOnRequest int
	FailError     error
}

// NewCountingEmbedder creates a thread-safe test embedder.
func NewCountingEmbedder(
	options CountingEmbedderOptions,
) *CountingEmbedder {
	modelID := options.ModelID
	if modelID == "" {
		modelID = "counting-embedder"
	}

	modelName := options.ModelName
	if modelName == "" {
		modelName = "counting-embedder"
	}

	dimensions := options.Dimensions
	if dimensions <= 0 {
		dimensions = 3
	}

	failError := options.FailError
	if failError == nil {
		failError = errors.New("counting embedder configured failure")
	}

	return &CountingEmbedder{
		modelID:       modelID,
		modelName:     modelName,
		dimensions:    dimensions,
		pooler:        options.Pooler,
		defaultError:  options.DefaultError,
		failOnRequest: options.FailOnRequest,
		failError:     failError,
	}
}

// Embed converts one text into a deterministic test vector.
//
// Embed delegates to BatchEmbed so that a single-text request is counted in
// exactly the same place as a batch request.
func (c *CountingEmbedder) Embed(
	ctx context.Context,
	text string,
) ([]float32, error) {
	vectors, err := c.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	return vectors[0], nil
}

// BatchEmbed records one model-interface request and returns one deterministic
// vector for every input text.
func (c *CountingEmbedder) BatchEmbed(
	ctx context.Context,
	texts []string,
) ([][]float32, error) {
	operation := types.IngestionOperationFromContext(ctx)

	inputChars := 0
	for _, text := range texts {
		inputChars += len(text)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.requestCount++
	requestNumber := c.requestCount
	c.batchSizes = append(c.batchSizes, len(texts))
	c.totalInputItems += len(texts)
	c.totalInputChars += inputChars
	c.calls = append(c.calls, EmbedderCall{
		Operation:  operation,
		BatchSize:  len(texts),
		InputChars: inputChars,
	})

	if c.failOnRequest > 0 &&
		requestNumber == c.failOnRequest {
		return nil, c.failError
	}

	if c.defaultError != nil {
		return nil, c.defaultError
	}

	vectors := make([][]float32, len(texts))
	for i := range texts {
		vector := make([]float32, c.dimensions)

		// Use a deterministic, non-zero value so tests can distinguish
		// returned vectors from missing results.
		for dimension := range vector {
			vector[dimension] = float32(i + dimension + 1)
		}

		vectors[i] = vector
	}

	return vectors, nil
}

// BatchEmbedWithPool delegates batching to the configured pooler.
//
// When no pooler is configured, it sends the complete input directly to the
// supplied model as one BatchEmbed request.
func (c *CountingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	model embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	if model == nil {
		model = c
	}

	if c.pooler != nil {
		return c.pooler.BatchEmbedWithPool(
			ctx,
			model,
			texts,
		)
	}

	return model.BatchEmbed(ctx, texts)
}

// GetModelName returns the test model name.
func (c *CountingEmbedder) GetModelName() string {
	return c.modelName
}

// GetDimensions returns the number of dimensions in each test vector.
func (c *CountingEmbedder) GetDimensions() int {
	return c.dimensions
}

// GetModelID returns the test model ID.
func (c *CountingEmbedder) GetModelID() string {
	return c.modelID
}

// Snapshot returns an immutable copy of the current counters.
func (c *CountingEmbedder) Snapshot() EmbedderSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	batchSizes := make([]int, len(c.batchSizes))
	copy(batchSizes, c.batchSizes)

	calls := make([]EmbedderCall, len(c.calls))
	copy(calls, c.calls)

	return EmbedderSnapshot{
		RequestCount:    c.requestCount,
		BatchSizes:      batchSizes,
		TotalInputItems: c.totalInputItems,
		TotalInputChars: c.totalInputChars,
		Dimensions:      c.dimensions,
		Calls:           calls,
	}
}
