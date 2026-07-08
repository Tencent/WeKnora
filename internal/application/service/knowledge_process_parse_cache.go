package service

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type parseArtifactCachePayload struct {
	Result *types.ReadResult `json:"result"`
}

// getParseArtifactCache returns a clone because the downstream image resolver
// rewrites MarkdownContent after storing images for the current knowledge.
func (s *knowledgeService) getParseArtifactCache(ctx context.Context, tenantID uint64, cacheKey string) (*types.ReadResult, bool) {
	if s.cacheRepo == nil {
		return nil, false
	}
	row, err := s.cacheRepo.Get(ctx, tenantID, types.ProcessingCacheStageParse, cacheKey)
	if err != nil {
		logger.Warnf(ctx, "parse artifact cache lookup failed: %v", err)
		return nil, false
	}
	if row == nil {
		return nil, false
	}
	var payload parseArtifactCachePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil || payload.Result == nil {
		if err != nil {
			logger.Warnf(ctx, "parse artifact cache payload invalid: %v", err)
		}
		return nil, false
	}
	return cloneReadResult(payload.Result), true
}

// putParseArtifactCache stores only successful docreader output. Parser errors
// are left uncached so transient backend failures can retry normally.
func (s *knowledgeService) putParseArtifactCache(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
	result *types.ReadResult,
	metadata map[string]string,
) {
	if s.cacheRepo == nil || result == nil || result.Error != "" {
		return
	}
	payloadBytes, err := json.Marshal(parseArtifactCachePayload{Result: cloneReadResult(result)})
	if err != nil {
		logger.Warnf(ctx, "parse artifact cache marshal failed: %v", err)
		return
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["version"] = parseArtifactVersion
	metaBytes, _ := json.Marshal(metadata)
	if err := s.cacheRepo.Upsert(ctx, &types.ProcessingCache{
		TenantID: tenantID,
		Stage:    types.ProcessingCacheStageParse,
		CacheKey: cacheKey,
		Payload:  types.JSON(payloadBytes),
		Metadata: types.JSON(metaBytes),
	}); err != nil {
		logger.Warnf(ctx, "parse artifact cache write failed: %v", err)
	}
}

// cloneReadResult keeps cached bytes and metadata isolated from callers that
// append image refs, rewrite markdown, or clear audio payloads later.
func cloneReadResult(in *types.ReadResult) *types.ReadResult {
	if in == nil {
		return nil
	}
	out := *in
	if in.ImageRefs != nil {
		out.ImageRefs = make([]types.ImageRef, len(in.ImageRefs))
		for i, ref := range in.ImageRefs {
			out.ImageRefs[i] = ref
			out.ImageRefs[i].ImageData = copyBytes(ref.ImageData)
		}
	}
	if in.Metadata != nil {
		out.Metadata = make(map[string]string, len(in.Metadata))
		for k, v := range in.Metadata {
			out.Metadata[k] = v
		}
	}
	out.AudioData = copyBytes(in.AudioData)
	return &out
}

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
