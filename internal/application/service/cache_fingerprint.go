package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func calculateMultimodalContentHash(imageBytes []byte, modelID, prompt string, chunkType types.ChunkType) string {
	h := sha256.New()
	_, _ = h.Write([]byte("multimodal-vlm-v1\x00"))
	_, _ = h.Write([]byte(chunkType))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(modelID)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(prompt)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write(imageBytes)
	return hex.EncodeToString(h.Sum(nil))
}

func calculateWikiMapCacheFingerprint(content, lang, synthesisModelID string, granularity types.WikiExtractionGranularity) string {
	h := sha256.New()
	_, _ = h.Write([]byte("wiki-map-v1\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(content)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(lang)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(synthesisModelID)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(granularity.Normalize()))
	return hex.EncodeToString(h.Sum(nil))
}

type wikiMapCacheEntry struct {
	Fingerprint     string                   `json:"fingerprint"`
	SummaryContent  string                   `json:"summary_content"`
	Entities        []extractedItem          `json:"entities"`
	Concepts        []extractedItem          `json:"concepts"`
	SlugItems       map[string]extractedItem `json:"slug_items"`
	Citations       map[string][]string      `json:"citations"`
	NewSlugs        []newSlugFromCitation    `json:"new_slugs"`
	Pass0Failed     bool                     `json:"pass0_failed"`
	ClassifyBatches int                      `json:"classify_batches"`
}

func wikiMapCacheFromMetadata(metadata types.JSON, fingerprint string) (*wikiMapCacheEntry, bool) {
	if len(metadata) == 0 || strings.TrimSpace(fingerprint) == "" {
		return nil, false
	}
	var envelope struct {
		MapCache wikiMapCacheEntry `json:"map_cache"`
	}
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return nil, false
	}
	if envelope.MapCache.Fingerprint != fingerprint || strings.TrimSpace(envelope.MapCache.SummaryContent) == "" {
		return nil, false
	}
	if envelope.MapCache.SlugItems == nil {
		envelope.MapCache.SlugItems = map[string]extractedItem{}
	}
	if envelope.MapCache.Citations == nil {
		envelope.MapCache.Citations = map[string][]string{}
	}
	return &envelope.MapCache, true
}

func metadataWithWikiMapCache(existing types.JSON, cache wikiMapCacheEntry) types.JSON {
	var metadata map[string]interface{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &metadata)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["map_cache"] = cache
	b, _ := json.Marshal(metadata)
	return types.JSON(b)
}
