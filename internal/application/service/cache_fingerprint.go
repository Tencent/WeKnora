package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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

func calculateWikiMapCacheFingerprint(content, lang, synthesisModelID string, granularity types.WikiExtractionGranularity, contentInstructions, extractionInstructions string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("wiki-map-v2\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(content)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(lang)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(synthesisModelID)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(granularity.Normalize()))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(contentInstructions)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(extractionInstructions)))
	return hex.EncodeToString(h.Sum(nil))
}

func calculateSummaryCacheFingerprint(joinedChunkContents, chatModelID, prompt string, maxTokens int, language string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("summary-v1\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(joinedChunkContents)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(chatModelID)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(prompt)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(fmt.Sprintf("%d", maxTokens)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(language)))
	return hex.EncodeToString(h.Sum(nil))
}

func calculateQuestionCacheFingerprint(
	chunkContent, prevContent, nextContent, title string,
	questionCount int,
	chatModelID, prompt, customInstructions, language string,
) string {
	h := sha256.New()
	_, _ = h.Write([]byte("question-v1\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(chunkContent)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(prevContent)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(nextContent)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(title)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(fmt.Sprintf("%d", questionCount)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(chatModelID)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(prompt)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(customInstructions)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(language)))
	return hex.EncodeToString(h.Sum(nil))
}

func calculateGraphExtractFingerprint(chunkContent, modelID, promptDescription, customInstructions string, tags []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("graph-extract-v1\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(chunkContent)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(modelID)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(promptDescription)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(customInstructions)))
	_, _ = h.Write([]byte("\x00"))
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	_, _ = h.Write([]byte(strings.Join(sorted, "\x1f")))
	return hex.EncodeToString(h.Sum(nil))
}

func calculateParseCacheFingerprint(fileHash, parserEngine, fileType, url string, overrides map[string]string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("parse-v1\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(fileHash)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(parserEngine)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(fileType)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(url)))
	_, _ = h.Write([]byte("\x00"))
	if len(overrides) > 0 {
		keys := make([]string, 0, len(overrides))
		for k := range overrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = h.Write([]byte(k))
			_, _ = h.Write([]byte("="))
			_, _ = h.Write([]byte(overrides[k]))
			_, _ = h.Write([]byte("\x1f"))
		}
	}
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

type parseCacheEntry struct {
	Fingerprint string           `json:"fingerprint"`
	Result      *types.ReadResult `json:"result"`
}

type summaryCacheEntry struct {
	Fingerprint string `json:"fingerprint"`
	Text        string `json:"text"`
}

func parseCacheFromKnowledgeMetadata(metadata types.JSON, fingerprint string) (*types.ReadResult, bool) {
	if len(metadata) == 0 || strings.TrimSpace(fingerprint) == "" {
		return nil, false
	}
	var envelope struct {
		ParseCache parseCacheEntry `json:"parse_cache"`
	}
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return nil, false
	}
	if envelope.ParseCache.Fingerprint != fingerprint || envelope.ParseCache.Result == nil {
		return nil, false
	}
	if strings.TrimSpace(envelope.ParseCache.Result.MarkdownContent) == "" && len(envelope.ParseCache.Result.ImageRefs) == 0 {
		return nil, false
	}
	return envelope.ParseCache.Result, true
}

func knowledgeMetadataWithParseCache(existing types.JSON, fingerprint string, result *types.ReadResult) types.JSON {
	var metadata map[string]interface{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &metadata)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	// Drop inline image bytes from cache to keep metadata small; storage keys/refs remain.
	cached := *result
	if len(cached.ImageRefs) > 0 {
		refs := make([]types.ImageRef, len(cached.ImageRefs))
		for i, ref := range cached.ImageRefs {
			refs[i] = ref
			refs[i].ImageData = nil
		}
		cached.ImageRefs = refs
	}
	cached.AudioData = nil
	metadata["parse_cache"] = parseCacheEntry{Fingerprint: fingerprint, Result: &cached}
	b, _ := json.Marshal(metadata)
	return types.JSON(b)
}

func summaryCacheFromKnowledgeMetadata(metadata types.JSON, fingerprint string) (string, bool) {
	if len(metadata) == 0 || strings.TrimSpace(fingerprint) == "" {
		return "", false
	}
	var envelope struct {
		SummaryCache summaryCacheEntry `json:"summary_cache"`
	}
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return "", false
	}
	if envelope.SummaryCache.Fingerprint != fingerprint || strings.TrimSpace(envelope.SummaryCache.Text) == "" {
		return "", false
	}
	return envelope.SummaryCache.Text, true
}

func knowledgeMetadataWithSummaryCache(existing types.JSON, fingerprint, text string) types.JSON {
	var metadata map[string]interface{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &metadata)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["summary_cache"] = summaryCacheEntry{Fingerprint: fingerprint, Text: text}
	b, _ := json.Marshal(metadata)
	return types.JSON(b)
}
