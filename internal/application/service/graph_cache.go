package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"time"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

// graphCacheTTL is how long a cached graph extraction result stays valid. The
// extraction is a pure function of (chunk text, prompt, model), so the result is
// content-addressed and only needs eviction to bound Redis memory.
const graphCacheTTL = 7 * 24 * time.Hour

// graphCacheKeyPrefix namespaces graph cache entries in Redis.
const graphCacheKeyPrefix = "graph"

// graphCacheKey derives the Redis key from the chunk text, model ID, and prompt
// version.
func graphCacheKey(modelID, promptVersion, chunkText string) string {
	sum := sha256.Sum256([]byte(chunkText))
	return fmt.Sprintf("%s:%x:%s:%s", graphCacheKeyPrefix, sum[:], modelID, promptVersion)
}

// graphPromptVersion hashes the extraction template so the cache key changes
// whenever the prompt (description, custom instructions, tags, or examples)
// changes. This makes prompt/config changes safely invalidate prior results.
func graphPromptVersion(template *types.PromptTemplateStructured) string {
	if template == nil {
		return "none"
	}
	b, err := json.Marshal(template)
	if err != nil {
		return "none"
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:])
}

// graphCacheGet returns a cached extraction result for key. A miss (or any Redis
// error) returns ok=false; the caller falls back to the real extraction.
func graphCacheGet(ctx context.Context, client *redis.Client, key string) (*types.GraphData, bool) {
	if client == nil {
		return nil, false
	}
	data, err := client.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			logger.GetLogger(ctx).Warnf("graph cache get failed: %v", err)
		}
		return nil, false
	}
	var graph types.GraphData
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&graph); err != nil {
		logger.GetLogger(ctx).Warnf("graph cache decode failed: %v", err)
		return nil, false
	}
	return &graph, true
}

// graphCacheSet writes the extraction result to the cache. Failures only log a
// warning so a Redis outage degrades to recomputing on the next request.
func graphCacheSet(ctx context.Context, client *redis.Client, key string, graph *types.GraphData) {
	if client == nil || graph == nil {
		return
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(*graph); err != nil {
		logger.GetLogger(ctx).Warnf("graph cache encode failed: %v", err)
		return
	}
	if err := client.Set(ctx, key, buf.Bytes(), graphCacheTTL).Err(); err != nil {
		logger.GetLogger(ctx).Warnf("graph cache set failed: %v", err)
	}
}

// extractGraph runs the per-chunk graph extraction with a Redis cache in front.
// On a hit the cached entity/relation result is returned without calling the
// model; on a miss the extractor runs and its result is cached. Cache failures
// degrade to a normal extraction call, so a Redis outage never breaks ingestion.
func (s *ChunkExtractService) extractGraph(
	ctx context.Context,
	extractor chatpipeline.Extractor,
	content, modelID string,
	template *types.PromptTemplateStructured,
) (*types.GraphData, error) {
	if s.redisClient == nil {
		return extractor.Extract(ctx, content)
	}
	key := graphCacheKey(modelID, graphPromptVersion(template), content)
	if graph, ok := graphCacheGet(ctx, s.redisClient, key); ok {
		return graph, nil
	}
	graph, err := extractor.Extract(ctx, content)
	if err != nil {
		return nil, err
	}
	graphCacheSet(ctx, s.redisClient, key, graph)
	return graph, nil
}
