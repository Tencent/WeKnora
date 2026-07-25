package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const (
	// wikiMapCacheKeyPrefix versions the entry format; bump if it ever changes.
	wikiMapCacheKeyPrefix = "weknora:cache:wikimap:v1:"
	// defaultWikiMapCacheTTL: the per-document map is the most expensive
	// non-deterministic step of wiki ingest (LLM extraction + summary +
	// citation). Its input (document content + extraction config) is fully
	// deterministic, so keep results long enough to cover reparse / rebuild
	// cycles of the same source.
	defaultWikiMapCacheTTL = 30 * 24 * time.Hour
)

// wikiMapCache freezes the per-document "map" — the content-derived slice of
// SlugUpdate (entity / concept / summary) emitted by mapOneDocument — keyed by
// the document's content-addressed identity plus the extraction config. A hit
// lets a rebuild skip the LLM extraction / summary / citation passes entirely
// and replay the frozen map. The per-document retract/retractStale lifecycle
// updates depend on the *current* wiki state (not content) so they are
// recomputed fresh on every run and appended to the cached map — this keeps
// the cached portion purely content-derived and never stale across rebuilds.
//
// Mirrors the VLM-result cache (vlm_result_cache.go): nil-client safe so Lite
// mode (no Redis) runs exactly as before.
type wikiMapCache struct {
	client *redis.Client
	ttl    time.Duration
}

// newWikiMapCache builds the cache. TTL is overridable via
// WIKI_MAP_CACHE_TTL_DAYS (<=0 keeps the default). Safe to call with nil client.
func newWikiMapCache(client *redis.Client) *wikiMapCache {
	if client == nil {
		return nil
	}
	ttl := defaultWikiMapCacheTTL
	if v := os.Getenv("WIKI_MAP_CACHE_TTL_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			ttl = time.Duration(days) * 24 * time.Hour
		}
	}
	return &wikiMapCache{client: client, ttl: ttl}
}

// wikiMapCacheEntry is the serialized cached payload: the frozen content-map
// updates plus the lightweight bookkeeping mapOneDocument would otherwise have
// produced via LLM, so a cache hit can reconstruct a docIngestResult without
// re-running any model call. WikiSpan is intentionally excluded (runtime-only
// trace handle; recreated on each hit).
type wikiMapCacheEntry struct {
	Updates  []SlugUpdate           `json:"updates"`
	DocTitle string                 `json:"doc_title"`
	Summary  string                 `json:"summary"`
	Pages    []types.WikiLogPageRef `json:"pages"`
	MapStats types.JSONMap          `json:"map_stats"`
}

// Get returns the cached entry on a hit. Any backend trouble (including a
// missing/unparseable entry) degrades to a miss — never fails the task.
func (c *wikiMapCache) Get(ctx context.Context, key string) (*wikiMapCacheEntry, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}
	raw, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return nil, false
	}
	var e wikiMapCacheEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return nil, false
	}
	return &e, true
}

// Set stores the canonical content map, best-effort.
func (c *wikiMapCache) Set(ctx context.Context, key string, e *wikiMapCacheEntry) {
	if c == nil || c.client == nil {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = c.client.Set(ctx, key, string(b), c.ttl).Err()
}

// wikiMapCacheKey derives the content-addressed key for one document's map:
// (tenant, knowledgeID, extract-model, language, content-hash,
// instructions-hash, extraction-granularity). oldPageSlugs is deliberately NOT
// part of the key — the lifecycle (retract/retractStale) updates that depend on
// it are recomputed every run, so the cached portion stays purely content
// derived and never goes stale across rebuilds (layered invalidation matching
// the embedding / VLM caches).
func wikiMapCacheKey(tenantID uint64, knowledgeID, modelID, lang, contentHash, instructionsHash string, granularity types.WikiExtractionGranularity) string {
	parts := []string{
		strconv.FormatUint(tenantID, 10),
		knowledgeID,
		modelID,
		lang,
		contentHash,
		instructionsHash,
		fmt.Sprintf("%v", granularity),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%s%s", wikiMapCacheKeyPrefix, hex.EncodeToString(sum[:]))
}

// wikiMapContentHash returns the hex SHA-256 of the document content actually
// handed to the LLM — the content-addressed identity of the source.
func wikiMapContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// wikiMapInstructionsHash fingerprints the KB-scoped extraction guidance that
// feeds the prompts, so changing instructions invalidates the cache.
func wikiMapInstructionsHash(contentInstructions, extractionInstructions string) string {
	sum := sha256.Sum256([]byte(contentInstructions + "\x00" + extractionInstructions))
	return hex.EncodeToString(sum[:8])
}
