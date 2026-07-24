package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const (
	parseArtifactCacheTTL      = 30 * 24 * time.Hour
	parseArtifactCacheMaxBytes = 64 << 20
)

type parseArtifactConfig struct {
	ParserEngine string            `json:"parser_engine"`
	FileType     string            `json:"file_type"`
	TenantID     uint64            `json:"tenant_id"`
	FileName     string            `json:"file_name"`
	Title        string            `json:"title"`
	Overrides    map[string]string `json:"overrides,omitempty"`
}

type cachedParseArtifact struct {
	Result   *types.ReadResult `json:"result"`
	CachedAt int64             `json:"cached_at"`
}

func parseArtifactCacheKey(contentBytes []byte, cfg parseArtifactConfig) string {
	return contentcache.ParseArtifactKey(
		contentcache.ImageHash(contentBytes),
		cfg.ParserEngine,
		stableJSONHash(cfg),
	)
}

func (s *knowledgeService) getCachedParseArtifact(ctx context.Context, key string) (*types.ReadResult, bool) {
	if s.redisClient == nil {
		return nil, false
	}
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err == redis.Nil || err != nil {
		return nil, false
	}

	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		logger.Warnf(ctx, "[convert] parse artifact cache unreadable for %s: %v", key, err)
		return nil, false
	}
	defer reader.Close()

	raw, err := io.ReadAll(io.LimitReader(reader, parseArtifactCacheMaxBytes+1))
	if err != nil {
		logger.Warnf(ctx, "[convert] parse artifact cache inflate failed for %s: %v", key, err)
		return nil, false
	}
	if len(raw) > parseArtifactCacheMaxBytes {
		logger.Warnf(ctx, "[convert] parse artifact cache inflated value too large for %s", key)
		return nil, false
	}

	var cached cachedParseArtifact
	if err := json.Unmarshal(raw, &cached); err != nil {
		logger.Warnf(ctx, "[convert] parse artifact cache decode failed for %s: %v", key, err)
		return nil, false
	}
	if cached.Result == nil {
		return nil, false
	}
	if cached.Result.Metadata == nil {
		cached.Result.Metadata = map[string]string{}
	}
	if !isCacheableParseArtifact(cached.Result) {
		return nil, false
	}
	return cached.Result, true
}

func (s *knowledgeService) setCachedParseArtifact(ctx context.Context, key string, result *types.ReadResult) {
	if s.redisClient == nil || result == nil {
		return
	}
	if !isCacheableParseArtifact(result) {
		return
	}

	raw, err := json.Marshal(cachedParseArtifact{
		Result:   cloneParseArtifactForCache(result),
		CachedAt: time.Now().Unix(),
	})
	if err != nil {
		return
	}

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return
	}
	if err := writer.Close(); err != nil {
		return
	}
	if buf.Len() > parseArtifactCacheMaxBytes {
		logger.Warnf(ctx, "[convert] parse artifact cache skip %s: compressed value %d bytes exceeds limit",
			key, buf.Len())
		return
	}
	if err := s.redisClient.Set(ctx, key, buf.Bytes(), parseArtifactCacheTTL).Err(); err != nil {
		logger.Warnf(ctx, "[convert] failed to write parse artifact cache %s: %v", key, err)
	}
}

func isCacheableParseArtifact(result *types.ReadResult) bool {
	// Audio payloads can be large binary blobs and are immediately consumed by
	// the ASR path, so keep this cache focused on document parse artifacts.
	if result.IsAudio || len(result.AudioData) > 0 {
		return false
	}
	// DocReader may report an ImageDirPath that points at a worker-local temp
	// directory. Only cache image-bearing artifacts when every image carries
	// inline bytes, which ResolveAndStore can persist again on the next rebuild.
	for _, ref := range result.ImageRefs {
		if len(ref.ImageData) == 0 {
			return false
		}
	}
	return true
}

func cloneParseArtifactForCache(result *types.ReadResult) *types.ReadResult {
	if result == nil {
		return nil
	}
	clone := &types.ReadResult{
		MarkdownContent: result.MarkdownContent,
		ImageRefs:       make([]types.ImageRef, len(result.ImageRefs)),
		Metadata:        make(map[string]string, len(result.Metadata)),
		Error:           result.Error,
	}
	for i, ref := range result.ImageRefs {
		ref.ImageData = append([]byte(nil), ref.ImageData...)
		clone.ImageRefs[i] = ref
	}
	for k, v := range result.Metadata {
		clone.Metadata[k] = v
	}
	return clone
}
