package interfaces

import "context"

// Embedder is the caller-side text vectorization seam (P2 port of the v1
// internal/models/embedding.Embedder). The wire facet moved to invoke.Embed +
// the adapter registry; this interface keeps the caller contract: single /
// batch calls, the pooler sub-batching path, and the metadata accessors the
// vector-store layer needs (dimensions for table provisioning, IDs for
// limiter keys and diagnostics). The service layer's invokeEmbedder implements
// it over invoke.Embed.
type Embedder interface {
	// Embed converts text to vector
	Embed(ctx context.Context, text string) ([]float32, error)

	// BatchEmbed converts multiple texts to vectors in batch
	BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)

	// GetModelName returns the model name
	GetModelName() string

	// GetDimensions returns the vector dimensions
	GetDimensions() int

	// GetModelID returns the model ID
	GetModelID() string

	EmbedderPooler
}

// EmbedderPooler sub-batches a large input list through a goroutine pool,
// calling back into model.BatchEmbed per sub-batch.
type EmbedderPooler interface {
	BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error)
}
