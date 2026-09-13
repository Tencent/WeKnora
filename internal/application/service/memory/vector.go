package memory

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// embedTimeout bounds the query-side embedding call.
	//
	// Recall sits in front of every answer. Semantic matching is worth a
	// fraction of a turn; it is not worth a turn that hangs because an
	// embedding endpoint is wedged. On timeout recall falls back to keyword
	// matching, so the cost of giving up early is a slightly worse selection.
	embedTimeout = 2 * time.Second
	// embedWriteTimeout bounds the write-side call. Writes are already off the
	// response path, so this can be more generous.
	embedWriteTimeout = 10 * time.Second
	// backfillPerRun is how many missing vectors one maintenance pass fills.
	//
	// Each one costs an embedding call, so this is a rate rather than a batch
	// size. It has to outpace what a busy subject accumulates while its model
	// is unreachable, or a backlog opened by one outage never drains and the
	// affected conversations stay reachable by keyword only.
	backfillPerRun = 200
)

// embedder resolves the embedding model pinned on this workspace.
//
// Memory is one vector space per workspace. Knowledge bases each bind their
// own embedding model, so there is no "the workspace embedding model" to fall
// back to — picking the first listed one would silently mix incomparable
// spaces as models are added or deleted. Blank means semantic recall is off.
func (s *Service) embedder(_ context.Context, cfg *types.MemoryConfig) (string, bool) {
	if cfg == nil || !cfg.VectorRecallEnabled() || s.modelService == nil {
		return "", false
	}
	if cfg.EmbeddingModelID == "" {
		return "", false
	}
	return cfg.EmbeddingModelID, true
}

// embedText produces one vector, bounded and non-fatal.
func (s *Service) embedText(
	ctx context.Context, modelID, text string, timeout time.Duration,
) []float32 {
	if modelID == "" || text == "" || s.modelService == nil {
		return nil
	}
	embedder, err := s.modelService.GetEmbeddingModel(ctx, modelID)
	if err != nil || embedder == nil {
		logger.Warnf(ctx, "memory: embedding model %s unavailable: %v", modelID, err)
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	vector, err := embedder.Embed(callCtx, text)
	if err != nil {
		logger.Warnf(ctx, "memory: embed failed: %v", err)
		return nil
	}
	return vector
}
