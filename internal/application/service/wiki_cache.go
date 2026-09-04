package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/redis/go-redis/v9"
)

// wikiCacheTTL bounds how long a cached per-document map product stays valid.
// 7 days mirrors the embedding/vlm/graph caches: the result is content-addressed
// and only needs eviction to bound Redis memory.
const wikiCacheTTL = 7 * 24 * time.Hour

// wikiCacheKeyPrefix namespaces wiki map-cache entries in Redis.
const wikiCacheKeyPrefix = "wiki"

// wikiMapCacheEntry is the cached per-document map product: the merged
// entity/concept items (chunk citations already baked into SourceChunks) plus
// the raw summary output. The reconcile step (retract/retractStale against the
// KB's live pages) is deliberately NOT cached — it depends on mutable page state
// and must run fresh on every ingest.
type wikiMapCacheEntry struct {
	Entities       []extractedItem
	Concepts       []extractedItem
	SummaryContent string
}

// wikiCacheKey derives the Redis key from the document content, synthesis model
// ID, prompt/config version, and extraction granularity.
func wikiCacheKey(modelID, promptVersion, granularity, content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%s:%x:%s:%s:%s", wikiCacheKeyPrefix, sum[:], modelID, promptVersion, granularity)
}

// wikiPromptVersion hashes the wiki prompt templates plus the per-KB config and
// language, so any change to the prompts, custom instructions, or language
// invalidates the cached result.
func wikiPromptVersion(lang string, batchCtx *WikiBatchContext) string {
	h := sha256.New()
	h.Write([]byte(agent.WikiCandidateSlugPrompt))
	h.Write([]byte(agent.WikiKnowledgeExtractPrompt))
	h.Write([]byte(agent.WikiSummaryPrompt))
	h.Write([]byte(agent.WikiChunkCitationPrompt))
	h.Write([]byte(lang))
	if batchCtx != nil {
		h.Write([]byte(batchCtx.ContentInstructions))
		h.Write([]byte(batchCtx.ExtractionInstructions))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// wikiCacheGet returns a cached map entry for key. A miss (or any Redis error)
// returns ok=false; the caller falls back to the real map computation.
func wikiCacheGet(ctx context.Context, client *redis.Client, key string) (*wikiMapCacheEntry, bool) {
	if client == nil {
		return nil, false
	}
	data, err := client.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			logger.GetLogger(ctx).Warnf("wiki cache get failed: %v", err)
		}
		return nil, false
	}
	var entry wikiMapCacheEntry
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&entry); err != nil {
		logger.GetLogger(ctx).Warnf("wiki cache decode failed: %v", err)
		return nil, false
	}
	return &entry, true
}

// wikiCacheSet writes the map entry to the cache. Failures only log a warning
// so a Redis outage degrades to recomputing on the next request.
func wikiCacheSet(ctx context.Context, client *redis.Client, key string, entry *wikiMapCacheEntry) {
	if client == nil || entry == nil {
		return
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(*entry); err != nil {
		logger.GetLogger(ctx).Warnf("wiki cache encode failed: %v", err)
		return
	}
	if err := client.Set(ctx, key, buf.Bytes(), wikiCacheTTL).Err(); err != nil {
		logger.GetLogger(ctx).Warnf("wiki cache set failed: %v", err)
	}
}
