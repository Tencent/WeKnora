package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/inferencecache"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	wikiDocMapArtifactVersion = "v2"
	wikiStableChunkRefVersion = "v1"
	wikiDedupStateVersion     = "v1"
)

// wikiDocMapArtifact is the source-owned output of the expensive Wiki map
// phase. It deliberately stops before reduce: page state, other contributors,
// retracts and cross-document conflicts remain live inputs to reduce.
//
// SourceChunks contain stable logical refs while this value is cached. They
// are rebound to the current parse's physical chunk UUIDs before mapOneDocument
// builds SlugUpdates.
type wikiDocMapArtifact struct {
	Entities      []extractedItem `json:"entities"`
	Concepts      []extractedItem `json:"concepts"`
	Summary       string          `json:"summary"`
	Pass0Fallback bool            `json:"pass0_fallback"`
	BatchCount    int             `json:"batch_count"`
	UncitedCount  int             `json:"uncited_count"`
	NewSlugCount  int             `json:"new_slug_count"`
	DedupState    string          `json:"dedup_state"`
}

type wikiFrozenChunk struct {
	Ref           string          `json:"ref"`
	ChunkIndex    int             `json:"chunk_index"`
	StartAt       int             `json:"start_at"`
	EndAt         int             `json:"end_at"`
	ChunkType     types.ChunkType `json:"chunk_type"`
	Content       string          `json:"content"`
	ContextHeader string          `json:"context_header,omitempty"`
}

// wikiDedupCandidateState fingerprints the exact external page projection
// consumed by the dedup prompt. Pages whose source_refs all belong to the
// current document are excluded: creating or refreshing those pages is this
// document's own reduce-side effect and must not invalidate its map cache on
// the next unchanged reparse.
func wikiDedupCandidateState(pages map[string]*types.WikiPageLite, knowledgeID string) string {
	type dependency struct {
		Slug     string   `json:"slug"`
		Title    string   `json:"title"`
		PageType string   `json:"page_type"`
		Aliases  []string `json:"aliases,omitempty"`
	}
	dependencies := make([]dependency, 0, len(pages))
	for _, page := range pages {
		if page == nil || page.Slug == "" || wikiPageOnlyReferencesKnowledge(page, knowledgeID) {
			continue
		}
		aliases := append([]string(nil), page.Aliases...)
		sort.Strings(aliases)
		dependencies = append(dependencies, dependency{
			Slug:     page.Slug,
			Title:    page.Title,
			PageType: page.PageType,
			Aliases:  aliases,
		})
	}
	sort.Slice(dependencies, func(i, j int) bool {
		return dependencies[i].Slug < dependencies[j].Slug
	})
	encoded, _ := json.Marshal(struct {
		Version string       `json:"version"`
		Pages   []dependency `json:"pages"`
	}{Version: wikiDedupStateVersion, Pages: dependencies})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func wikiPageOnlyReferencesKnowledge(page *types.WikiPageLite, knowledgeID string) bool {
	if page == nil || knowledgeID == "" || len(page.SourceRefs) == 0 {
		return false
	}
	for _, sourceRef := range page.SourceRefs {
		sourceID := sourceRef
		if separator := strings.IndexByte(sourceID, '|'); separator >= 0 {
			sourceID = sourceID[:separator]
		}
		if sourceID != knowledgeID {
			return false
		}
	}
	return true
}

// wikiStableChunkRef gives cache artifacts a deterministic logical reference
// without changing the chunks table's UUID primary key. Including position
// disambiguates repeated boilerplate chunks while normalized content keeps the
// ref stable across random export-image URLs.
func wikiStableChunkRef(chunk *types.Chunk) string {
	if chunk == nil {
		return ""
	}
	payload := struct {
		Version       string          `json:"version"`
		ChunkIndex    int             `json:"chunk_index"`
		StartAt       int             `json:"start_at"`
		EndAt         int             `json:"end_at"`
		ChunkType     types.ChunkType `json:"chunk_type"`
		Content       string          `json:"content"`
		ContextHeader string          `json:"context_header,omitempty"`
	}{
		Version:       wikiStableChunkRefVersion,
		ChunkIndex:    chunk.ChunkIndex,
		StartAt:       chunk.StartAt,
		EndAt:         chunk.EndAt,
		ChunkType:     chunk.ChunkType,
		Content:       searchutil.CanonicalizeImageURLsForModel(chunk.Content),
		ContextHeader: searchutil.CanonicalizeImageURLsForModel(chunk.ContextHeader),
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return "wkchunk:" + wikiStableChunkRefVersion + ":" + hex.EncodeToString(sum[:])
}

func freezeWikiChunks(chunks []*types.Chunk) ([]wikiFrozenChunk, map[string]string, map[string]string) {
	frozen := make([]wikiFrozenChunk, 0, len(chunks))
	idToRef := make(map[string]string, len(chunks))
	refToID := make(map[string]string, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil || chunk.ID == "" {
			continue
		}
		ref := wikiStableChunkRef(chunk)
		idToRef[chunk.ID] = ref
		refToID[ref] = chunk.ID
		frozen = append(frozen, wikiFrozenChunk{
			Ref:           ref,
			ChunkIndex:    chunk.ChunkIndex,
			StartAt:       chunk.StartAt,
			EndAt:         chunk.EndAt,
			ChunkType:     chunk.ChunkType,
			Content:       searchutil.CanonicalizeImageURLsForModel(chunk.Content),
			ContextHeader: searchutil.CanonicalizeImageURLsForModel(chunk.ContextHeader),
		})
	}
	sort.Slice(frozen, func(i, j int) bool {
		if frozen[i].ChunkIndex != frozen[j].ChunkIndex {
			return frozen[i].ChunkIndex < frozen[j].ChunkIndex
		}
		if frozen[i].StartAt != frozen[j].StartAt {
			return frozen[i].StartAt < frozen[j].StartAt
		}
		return frozen[i].Ref < frozen[j].Ref
	})
	return frozen, idToRef, refToID
}

func wikiDocMapArtifactKey(
	ctx context.Context,
	chatModel chat.Chat,
	kbID string,
	content, lang string,
	chunks []*types.Chunk,
	batchCtx *WikiBatchContext,
) string {
	tenantID, _ := types.TenantIDFromContext(ctx)
	maskedContent, _ := maskImageURLs(content)
	frozenChunks, _, _ := freezeWikiChunks(chunks)
	granularity := types.WikiExtractionStandard
	var contentInstructions, extractionInstructions string
	if batchCtx != nil {
		granularity = batchCtx.ExtractionGranularity.Normalize()
		contentInstructions = batchCtx.ContentInstructions
		extractionInstructions = batchCtx.ExtractionInstructions
	}
	input := struct {
		Version                string                          `json:"version"`
		KnowledgeBaseID        string                          `json:"knowledge_base_id"`
		Language               string                          `json:"language"`
		Granularity            types.WikiExtractionGranularity `json:"granularity"`
		ContentInstructions    string                          `json:"content_instructions"`
		ExtractionInstructions string                          `json:"extraction_instructions"`
		Content                string                          `json:"content"`
		Chunks                 []wikiFrozenChunk               `json:"chunks"`
	}{
		Version:                wikiDocMapArtifactVersion,
		KnowledgeBaseID:        kbID,
		Language:               lang,
		Granularity:            granularity,
		ContentInstructions:    contentInstructions,
		ExtractionInstructions: extractionInstructions,
		Content:                maskedContent,
		Chunks:                 frozenChunks,
	}
	encoded, _ := json.Marshal(input)
	return inferencecache.Key(
		"wiki.doc.map", tenantID, chat.FingerprintOf(chatModel),
		[]byte(wikiDocMapArtifactVersion),
		[]byte(agent.WikiCandidateSlugPrompt),
		[]byte(agent.WikiKnowledgeExtractPrompt),
		[]byte(agent.WikiDeduplicationPrompt),
		[]byte(agent.WikiSummaryPrompt),
		[]byte(agent.WikiChunkCitationPrompt),
		encoded,
	)
}

func transformArtifactChunkRefs(items []extractedItem, refs map[string]string) error {
	for i := range items {
		for j, sourceChunk := range items[i].SourceChunks {
			mapped, ok := refs[sourceChunk]
			if !ok {
				return fmt.Errorf("wiki map artifact references unknown chunk %q", sourceChunk)
			}
			items[i].SourceChunks[j] = mapped
		}
	}
	return nil
}

func transformArtifactImageURLs(artifact *wikiDocMapArtifact, transform func(string) string) {
	if artifact == nil || transform == nil {
		return
	}
	artifact.Summary = transform(artifact.Summary)
	transformItems := func(items []extractedItem) {
		for i := range items {
			items[i].Name = transform(items[i].Name)
			items[i].Slug = transform(items[i].Slug)
			items[i].Description = transform(items[i].Description)
			items[i].Details = transform(items[i].Details)
			for j := range items[i].Aliases {
				items[i].Aliases[j] = transform(items[i].Aliases[j])
			}
		}
	}
	transformItems(artifact.Entities)
	transformItems(artifact.Concepts)
}

func freezeWikiDocMapArtifact(artifact *wikiDocMapArtifact, content string, idToRef map[string]string) error {
	if err := transformArtifactChunkRefs(artifact.Entities, idToRef); err != nil {
		return err
	}
	if err := transformArtifactChunkRefs(artifact.Concepts, idToRef); err != nil {
		return err
	}
	_, tokenToURL := maskImageURLs(content)
	urlToToken := make(map[string]string, len(tokenToURL))
	for token, url := range tokenToURL {
		urlToToken[url] = token
	}
	transformArtifactImageURLs(artifact, func(value string) string {
		return maskImageURLsWithState(value, urlToToken, tokenToURL)
	})
	return nil
}

func thawWikiDocMapArtifact(artifact *wikiDocMapArtifact, content string, refToID map[string]string) error {
	if err := transformArtifactChunkRefs(artifact.Entities, refToID); err != nil {
		return err
	}
	if err := transformArtifactChunkRefs(artifact.Concepts, refToID); err != nil {
		return err
	}
	_, tokenToURL := maskImageURLs(content)
	transformArtifactImageURLs(artifact, func(value string) string {
		return unmaskImageURLs(value, tokenToURL)
	})
	return nil
}

func (s *wikiIngestService) resolveWikiDocMapArtifact(
	ctx context.Context,
	chatModel chat.Chat,
	kbID, knowledgeID, content, lang string,
	chunks []*types.Chunk,
	oldPageSlugs map[string]bool,
	batchCtx *WikiBatchContext,
	wikiSpan *Span,
) (wikiDocMapArtifact, bool, error) {
	key := wikiDocMapArtifactKey(ctx, chatModel, kbID, content, lang, chunks, batchCtx)
	_, idToRef, refToID := freezeWikiChunks(chunks)
	loader := func(loadCtx context.Context) (wikiDocMapArtifact, error) {
		return s.computeWikiDocMapArtifact(
			loadCtx, chatModel, kbID, knowledgeID, content, lang,
			chunks, oldPageSlugs, batchCtx, wikiSpan,
		)
	}
	artifact, stats, err := resolveWikiDocMapArtifactValue(
		ctx, s.inferenceCache, key, content, idToRef, refToID, loader,
	)
	if stats.ReadError != nil {
		logger.Warnf(ctx, "[InferenceCache] Wiki doc map read failed, using provider: %v", stats.ReadError)
	}
	if stats.WriteError != nil {
		logger.Warnf(ctx, "[InferenceCache] Wiki doc map write failed: %v", stats.WriteError)
	}
	if err != nil {
		return wikiDocMapArtifact{}, false, err
	}
	if stats.Hit {
		candidatePages, candidateErr := s.findDedupCandidatePages(ctx, kbID, artifact.Entities, artifact.Concepts)
		if candidateErr != nil {
			return wikiDocMapArtifact{}, false, fmt.Errorf("validate wiki dedup state: %w", candidateErr)
		}
		currentDedupState := wikiDedupCandidateState(candidatePages, knowledgeID)
		if currentDedupState != artifact.DedupState {
			logger.Infof(ctx, "[InferenceCache] stage=wiki.doc.map key=%s invalidated=dedup_state old=%s new=%s",
				inferenceCacheKeyID(key), shortFingerprint(artifact.DedupState), shortFingerprint(currentDedupState))
			var invalidateErr error
			if s.inferenceCache != nil {
				invalidateErr = s.inferenceCache.Invalidate(ctx, key)
			}
			if s.inferenceCache == nil || invalidateErr != nil {
				// Cache invalidation failures must not make stale Wiki mappings
				// authoritative. Recompute directly and continue without caching.
				if invalidateErr != nil {
					logger.Warnf(ctx, "[InferenceCache] Wiki doc map invalidation failed, recomputing without cache: %v", invalidateErr)
				}
				artifact, err = loader(ctx)
				if err != nil {
					return wikiDocMapArtifact{}, false, err
				}
				return artifact, false, nil
			}
			artifact, stats, err = resolveWikiDocMapArtifactValue(
				ctx, s.inferenceCache, key, content, idToRef, refToID, loader,
			)
			if err != nil {
				return wikiDocMapArtifact{}, false, err
			}
			// A concurrent writer may have restored the stale entry after our
			// invalidation. Validate the reloaded artifact against its own
			// candidate set; extraction itself may legitimately have changed.
			reloadedPages, reloadStateErr := s.findDedupCandidatePages(ctx, kbID, artifact.Entities, artifact.Concepts)
			if reloadStateErr != nil {
				return wikiDocMapArtifact{}, false, fmt.Errorf("validate refreshed wiki dedup state: %w", reloadStateErr)
			}
			if artifact.DedupState != wikiDedupCandidateState(reloadedPages, knowledgeID) {
				artifact, err = loader(ctx)
				if err != nil {
					return wikiDocMapArtifact{}, false, err
				}
				return artifact, false, nil
			}
		}
	}
	logger.Infof(ctx, "[InferenceCache] stage=wiki.doc.map key=%s hit=%v coalesced=%v",
		inferenceCacheKeyID(key), stats.Hit, stats.Coalesced)
	return artifact, stats.Hit || stats.Coalesced, nil
}

func shortFingerprint(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func resolveWikiDocMapArtifactValue(
	ctx context.Context,
	cache inferencecache.Cache,
	key, content string,
	idToRef, refToID map[string]string,
	loader func(context.Context) (wikiDocMapArtifact, error),
) (wikiDocMapArtifact, inferencecache.Stats, error) {
	artifact, stats, err := inferencecache.ResolveJSON(ctx, cache, key,
		func(loadCtx context.Context) (wikiDocMapArtifact, error) {
			computed, loadErr := loader(loadCtx)
			if loadErr != nil {
				return wikiDocMapArtifact{}, loadErr
			}
			if freezeErr := freezeWikiDocMapArtifact(&computed, content, idToRef); freezeErr != nil {
				return wikiDocMapArtifact{}, freezeErr
			}
			return computed, nil
		})
	if err != nil {
		return wikiDocMapArtifact{}, stats, err
	}
	if err := thawWikiDocMapArtifact(&artifact, content, refToID); err != nil {
		if cache != nil {
			_ = cache.Invalidate(ctx, key)
		}
		return wikiDocMapArtifact{}, stats, fmt.Errorf("rebind wiki doc map artifact: %w", err)
	}
	return artifact, stats, nil
}

func (s *wikiIngestService) computeWikiDocMapArtifact(
	ctx context.Context,
	chatModel chat.Chat,
	kbID, knowledgeID, content, lang string,
	chunks []*types.Chunk,
	oldPageSlugs map[string]bool,
	batchCtx *WikiBatchContext,
	wikiSpan *Span,
) (wikiDocMapArtifact, error) {
	if batchCtx == nil {
		batchCtx = &WikiBatchContext{}
	}
	var (
		extractedEntities []extractedItem
		extractedConcepts []extractedItem
		slugItems         map[string]extractedItem
		dedupState        string
		pass0Failed       bool
	)
	logger.Infof(ctx, "wiki ingest: pass 0 - extracting candidate slugs for %s", knowledgeID)
	extractSpan := s.tracker().BeginSubSpan(ctx, wikiSpan, "postprocess.wiki.extract", types.SpanKindSubSpan, types.JSONMap{
		"content_chars": utf8.RuneCountInString(content),
		"old_pages":     len(oldPageSlugs),
	})
	extractedEntities, extractedConcepts, slugItems, dedupState, err := s.extractCandidateSlugs(
		ctx, chatModel, kbID, knowledgeID, content, lang, oldPageSlugs, batchCtx,
	)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: pass 0 failed for %s (%v) - falling back to legacy extractor", knowledgeID, err)
		pass0Failed = true
		extractedEntities, extractedConcepts, slugItems, dedupState, err = s.extractEntitiesAndConceptsNoUpsert(
			ctx, chatModel, kbID, knowledgeID, content, lang, oldPageSlugs, batchCtx,
		)
		if err != nil {
			s.tracker().FailSpan(ctx, extractSpan, "EXTRACT_FAILED", err.Error(), err)
			return wikiDocMapArtifact{}, err
		}
	}
	s.tracker().EndSpan(ctx, extractSpan, types.JSONMap{
		"entities":         len(extractedEntities),
		"concepts":         len(extractedConcepts),
		"pass0_fallback":   pass0Failed,
		"entities_preview": previewExtractedItems(extractedEntities, 8),
		"concepts_preview": previewExtractedItems(extractedConcepts, 8),
	})

	var summaryExtractedPages []string
	for slug := range slugItems {
		summaryExtractedPages = append(summaryExtractedPages, slug)
	}
	sort.Strings(summaryExtractedPages)
	var slugListing strings.Builder
	for _, slug := range summaryExtractedPages {
		if item, ok := slugItems[slug]; ok {
			aliases := ""
			if len(item.Aliases) > 0 {
				aliases = fmt.Sprintf(" (Aliases: %s)", strings.Join(item.Aliases, ", "))
			}
			fmt.Fprintf(&slugListing, "- [[%s]] = %s%s\n", slug, item.Name, aliases)
		} else {
			fmt.Fprintf(&slugListing, "- [[%s]]\n", slug)
		}
	}

	var (
		summaryContent string
		summaryErr     error
		citations      map[string][]string
		newSlugs       []newSlugFromCitation
		batchCount     int
	)
	summarySpan := s.tracker().BeginSubSpan(ctx, wikiSpan, "postprocess.wiki.summary", types.SpanKindSubSpan, types.JSONMap{
		"content_chars":   utf8.RuneCountInString(content),
		"extracted_slugs": len(summaryExtractedPages),
	})
	var classifySpan *Span
	if !pass0Failed {
		classifySpan = s.tracker().BeginSubSpan(ctx, wikiSpan, "postprocess.wiki.classify", types.SpanKindSubSpan, types.JSONMap{
			"chunks":     len(chunks),
			"candidates": len(extractedEntities) + len(extractedConcepts),
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		summaryContent, summaryErr = s.generateWithTemplateCached(ctx, "summary", chatModel, agent.WikiSummaryPrompt, map[string]string{
			"Content":            content,
			"Language":           lang,
			"ExtractedSlugs":     slugListing.String(),
			"CustomInstructions": batchCtx.ContentInstructions,
			"InstructionScope":   "wiki_content",
		})
		if summaryErr != nil {
			s.tracker().FailSpan(ctx, summarySpan, "SUMMARY_FAILED", summaryErr.Error(), summaryErr)
		} else {
			sumLine, sumBody := splitSummaryLine(summaryContent)
			s.tracker().EndSpan(ctx, summarySpan, types.JSONMap{
				"chars":        utf8.RuneCountInString(summaryContent),
				"summary_line": previewText(sumLine, 160),
				"body_preview": previewText(sumBody, 320),
			})
		}
	}()
	go func() {
		defer wg.Done()
		if pass0Failed {
			citations = map[string][]string{}
			return
		}
		candidatesXML := renderCandidateSlugsXML(extractedEntities, extractedConcepts)
		citations, newSlugs, batchCount = s.classifyChunkCitations(ctx, chatModel, candidatesXML, chunks, lang, batchCtx)
		s.tracker().EndSpan(ctx, classifySpan, types.JSONMap{
			"cited_slugs":      len(citations),
			"new_slugs":        len(newSlugs),
			"batches":          batchCount,
			"top_cited":        topCitedSlugs(citations, 8),
			"new_slugs_sample": previewNewSlugs(newSlugs, 8),
		})
	}()
	wg.Wait()
	if summaryErr != nil {
		return wikiDocMapArtifact{}, fmt.Errorf("generate summary: %w", summaryErr)
	}

	var uncitedCount int
	extractedEntities, extractedConcepts, uncitedCount = mergeCitationsIntoItems(
		extractedEntities, extractedConcepts, citations, newSlugs,
	)
	return wikiDocMapArtifact{
		Entities:      extractedEntities,
		Concepts:      extractedConcepts,
		Summary:       summaryContent,
		Pass0Fallback: pass0Failed,
		BatchCount:    batchCount,
		UncitedCount:  uncitedCount,
		NewSlugCount:  len(newSlugs),
		DedupState:    dedupState,
	}, nil
}
