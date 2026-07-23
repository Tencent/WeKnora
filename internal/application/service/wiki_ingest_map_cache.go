package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type wikiDocumentMapCachePayload struct {
	KnowledgeID        string          `json:"knowledge_id"`
	SummaryContent     string          `json:"summary_content"`
	Entities           []extractedItem `json:"entities"`
	Concepts           []extractedItem `json:"concepts"`
	Uncited            int             `json:"uncited"`
	NewSlugCount       int             `json:"new_slug_count"`
	Pass0Failed        bool            `json:"pass0_failed"`
	ClassifyBatchCount int             `json:"classify_batch_count"`
}

func wikiMapChatModelID(chatModel chat.Chat) string {
	if chatModel == nil {
		return "unknown"
	}
	if id := strings.TrimSpace(chatModel.GetModelID()); id != "" {
		return id
	}
	if name := strings.TrimSpace(chatModel.GetModelName()); name != "" {
		return name
	}
	return "unknown"
}

func wikiMapPromptBundleHash() string {
	return types.ContentHash(strings.Join([]string{
		agent.WikiCandidateSlugPrompt,
		agent.WikiChunkCitationPrompt,
		agent.WikiSummaryPrompt,
		agent.WikiKnowledgeExtractPrompt,
		agent.WikiDeduplicationPrompt,
	}, "\x00"), "")
}

func wikiMapCacheKey(
	knowledgeID string,
	content string,
	chatModel chat.Chat,
	lang string,
	batchCtx *WikiBatchContext,
) string {
	granularity := types.WikiExtractionStandard
	contentInstructions := ""
	extractionInstructions := ""
	if batchCtx != nil {
		granularity = batchCtx.ExtractionGranularity.Normalize()
		contentInstructions = batchCtx.ContentInstructions
		extractionInstructions = batchCtx.ExtractionInstructions
	}
	identity := strings.Join([]string{
		knowledgeID,
		types.ContentHash(content, ""),
		wikiMapChatModelID(chatModel),
		string(granularity),
		strings.TrimSpace(lang),
		types.ContentHash(contentInstructions, ""),
		types.ContentHash(extractionInstructions, ""),
		wikiMapPromptBundleHash(),
	}, "\x00")
	return fmt.Sprintf("wiki_map:%s:%s", knowledgeID, types.ContentHash(identity, ""))
}

func (s *wikiIngestService) getCachedWikiDocumentMap(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
) (*wikiDocumentMapCachePayload, bool) {
	if s.contentCacheRepo == nil {
		return nil, false
	}
	entry, err := s.contentCacheRepo.GetByKey(ctx, tenantID, types.ContentCacheKindWikiMap, cacheKey)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: wiki map cache get failed: %v", err)
		return nil, false
	}
	if entry == nil {
		return nil, false
	}
	var payload wikiDocumentMapCachePayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		logger.Warnf(ctx, "wiki ingest: wiki map cache decode failed: %v", err)
		return nil, false
	}
	return &payload, true
}

func (s *wikiIngestService) setCachedWikiDocumentMap(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
	payload *wikiDocumentMapCachePayload,
) {
	if s.contentCacheRepo == nil || payload == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: wiki map cache encode failed: %v", err)
		return
	}
	if err := s.contentCacheRepo.Upsert(ctx, &types.ContentCacheEntry{
		TenantID:  tenantID,
		CacheKind: types.ContentCacheKindWikiMap,
		CacheKey:  cacheKey,
		Payload:   types.JSON(data),
	}); err != nil {
		logger.Warnf(ctx, "wiki ingest: wiki map cache upsert failed: %v", err)
	}
}

func (p *wikiDocumentMapCachePayload) buildResultAndUpdates(
	ctx context.Context,
	batchCtx *WikiBatchContext,
	knowledgeID string,
	docTitle string,
	sourceRef string,
	content string,
	oldPageSlugs map[string]bool,
	wikiSpan *Span,
	cacheHit bool,
	chunkCount int,
	lang string,
) (*docIngestResult, []SlugUpdate) {
	summarySlug := fmt.Sprintf("summary/%s", slugify(knowledgeID))
	sumLine, sumBody := splitSummaryLine(p.SummaryContent)
	if sumBody == "" {
		sumBody = p.SummaryContent
	}
	if sumLine == "" {
		sumLine = docTitle
	}
	docSummaryLine := sumLine
	docSummary := sumBody
	if strings.TrimSpace(docSummary) == "" {
		docSummary = sumLine
	}

	slugItems := make(map[string]extractedItem, len(p.Entities)+len(p.Concepts))
	for _, item := range p.Entities {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}
	for _, item := range p.Concepts {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}

	extractedPages := make([]types.WikiLogPageRef, 0, len(slugItems)+1)
	for slug, item := range slugItems {
		title := item.Name
		if title == "" {
			title = slug
		}
		extractedPages = append(extractedPages, types.WikiLogPageRef{Slug: slug, Title: title})
	}

	updates := []SlugUpdate{{
		Slug:        summarySlug,
		Type:        types.WikiPageTypeSummary,
		DocTitle:    docTitle,
		KnowledgeID: knowledgeID,
		SourceRef:   sourceRef,
		Language:    lang,
		SummaryLine: sumLine,
		SummaryBody: sumBody,
	}}
	extractedPages = append(extractedPages, types.WikiLogPageRef{Slug: summarySlug, Title: docTitle})

	for _, item := range p.Entities {
		if item.Slug == "" {
			continue
		}
		updates = append(updates, SlugUpdate{
			Slug:         item.Slug,
			Type:         types.WikiPageTypeEntity,
			Item:         item,
			DocTitle:     docTitle,
			KnowledgeID:  knowledgeID,
			SourceRef:    sourceRef,
			Language:     lang,
			SourceChunks: item.SourceChunks,
			DocSummary:   docSummary,
		})
	}
	for _, item := range p.Concepts {
		if item.Slug == "" {
			continue
		}
		updates = append(updates, SlugUpdate{
			Slug:         item.Slug,
			Type:         types.WikiPageTypeConcept,
			Item:         item,
			DocTitle:     docTitle,
			KnowledgeID:  knowledgeID,
			SourceRef:    sourceRef,
			Language:     lang,
			SourceChunks: item.SourceChunks,
			DocSummary:   docSummary,
		})
	}

	priorContribution := ""
	if batchCtx != nil && batchCtx.SummaryContentByKnowledgeID != nil {
		priorContribution = batchCtx.SummaryContentByKnowledgeID(ctx, knowledgeID)
	}
	newSlugSet := make(map[string]bool, len(extractedPages))
	for _, ns := range extractedPages {
		newSlugSet[ns.Slug] = true
	}

	var reparseOverlap, staleCount int
	for oldSlug := range oldPageSlugs {
		if newSlugSet[oldSlug] {
			if strings.HasPrefix(oldSlug, "summary/") {
				continue
			}
			reparseOverlap++
			updates = append(updates, SlugUpdate{
				Slug:              oldSlug,
				Type:              "retract",
				RetractDocContent: priorContribution,
				DocTitle:          docTitle,
				KnowledgeID:       knowledgeID,
				Language:          lang,
			})
			continue
		}
		staleCount++
		updates = append(updates, SlugUpdate{
			Slug:              oldSlug,
			Type:              "retractStale",
			RetractDocContent: content,
			DocTitle:          docTitle,
			KnowledgeID:       knowledgeID,
			Language:          lang,
		})
	}

	citedChunkSet := make(map[string]bool)
	for _, item := range p.Entities {
		for _, id := range item.SourceChunks {
			citedChunkSet[id] = true
		}
	}
	for _, item := range p.Concepts {
		for _, id := range item.SourceChunks {
			citedChunkSet[id] = true
		}
	}

	mapStats := types.JSONMap{
		"doc_title":        previewText(docTitle, 120),
		"chunks":           chunkCount,
		"candidate_slugs":  len(slugItems),
		"cited_chunks":     len(citedChunkSet),
		"uncited_slugs":    p.Uncited,
		"new_slugs":        p.NewSlugCount,
		"updates":          len(updates),
		"reparse_slugs":    reparseOverlap,
		"stale_slugs":      staleCount,
		"extracted_pages":  len(extractedPages),
		"summary_chars":    utf8.RuneCountInString(docSummary),
		"pass0_fallback":   p.Pass0Failed,
		"classify_batches": p.ClassifyBatchCount,
		"summary_preview":  previewText(docSummaryLine, 160),
		"cache_hit":        cacheHit,
	}

	return &docIngestResult{
		KnowledgeID: knowledgeID,
		DocTitle:    docTitle,
		Summary:     docSummaryLine,
		Pages:       extractedPages,
		MapStats:    mapStats,
		WikiSpan:    wikiSpan,
	}, updates
}
