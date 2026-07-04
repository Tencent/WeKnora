package service

import (
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type wikiMapCachePayload struct {
	ExtractedEntities []extractedItem       `json:"extracted_entities"`
	ExtractedConcepts []extractedItem       `json:"extracted_concepts"`
	SummaryContent    string                `json:"summary_content"`
	Citations         map[string][]string   `json:"citations"`
	NewSlugs          []newSlugFromCitation `json:"new_slugs"`
	BatchCount        int                   `json:"batch_count"`
	Pass0Failed       bool                  `json:"pass0_failed"`
	Uncited           int                   `json:"uncited"`
}

func wikiMapContentHash(content string) string {
	return types.CacheFingerprint("wiki-map-content", map[string]any{
		"content": strings.TrimSpace(content),
	})
}

func wikiMapModelID(chatModel chat.Chat) string {
	if chatModel == nil {
		return ""
	}
	return strings.TrimSpace(chatModel.GetModelID())
}

func wikiMapConfigHash(lang string, granularity types.WikiExtractionGranularity, oldPageSlugs map[string]bool) string {
	prevSlugs := make([]string, 0, len(oldPageSlugs))
	for slug := range oldPageSlugs {
		if strings.HasPrefix(slug, "entity/") || strings.HasPrefix(slug, "concept/") {
			prevSlugs = append(prevSlugs, slug)
		}
	}
	sort.Strings(prevSlugs)
	return types.CacheFingerprint("wiki-map-config", map[string]any{
		"schema":                   types.WikiMapCacheSchemaV1,
		"language":                 strings.TrimSpace(lang),
		"granularity":              granularity.Normalize(),
		"previous_slugs":           prevSlugs,
		"max_content_runes":        maxContentForWiki,
		"max_citation_batch_runes": maxRunesPerCitationBatch,
		"candidate_prompt":         agent.WikiCandidateSlugPrompt,
		"legacy_extract_prompt":    agent.WikiKnowledgeExtractPrompt,
		"summary_prompt":           agent.WikiSummaryPrompt,
		"citation_prompt":          agent.WikiChunkCitationPrompt,
	})
}

func wikiMapCacheKey(contentHash, modelID, configHash string) string {
	return types.CacheFingerprint("wiki-map-result", map[string]any{
		"schema":       types.WikiMapCacheSchemaV1,
		"content_hash": strings.TrimSpace(contentHash),
		"model_id":     strings.TrimSpace(modelID),
		"config_hash":  strings.TrimSpace(configHash),
	})
}

func wikiSlugItemsFromExtracted(entities, concepts []extractedItem) map[string]extractedItem {
	slugItems := make(map[string]extractedItem, len(entities)+len(concepts))
	for _, item := range entities {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}
	for _, item := range concepts {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}
	return slugItems
}
