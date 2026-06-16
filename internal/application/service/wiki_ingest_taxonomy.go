package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// wikiTaxonomyReconcileMaxPages caps how many pages a single reconcile pass
// will rewrite, so an over-eager remap cannot fan out into an unbounded write
// storm on a very large KB. Pages beyond the cap are picked up by the next run.
const wikiTaxonomyReconcileMaxPages = 5000

// wikiTaxonomyReconcilePageSize is the cursor page size used when scanning the
// KB to apply a category remap.
const wikiTaxonomyReconcilePageSize = 500

type wikiTaxonomyRemapEntry struct {
	From []string `json:"from"`
	To   []string `json:"to"`
}

type wikiTaxonomyRemapResult struct {
	Remap []wikiTaxonomyRemapEntry `json:"remap"`
}

// ReconcileTaxonomy is the manual entry point for collapsing synonymous
// category folders across a wiki knowledge base. It resolves the KB's synthesis
// chat model itself so it can be invoked outside an ingest batch (e.g. from a
// future maintenance handler).
func (s *wikiIngestService) ReconcileTaxonomy(ctx context.Context, kbID string) error {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return fmt.Errorf("wiki taxonomy reconcile: get KB: %w", err)
	}
	if !kb.IsWikiEnabled() {
		return fmt.Errorf("wiki taxonomy reconcile: KB %s is not wiki type", kbID)
	}

	synthesisModelID := ""
	if kb.WikiConfig != nil {
		synthesisModelID = kb.WikiConfig.SynthesisModelID
	}
	if synthesisModelID == "" {
		synthesisModelID = kb.SummaryModelID
	}
	if synthesisModelID == "" {
		return fmt.Errorf("wiki taxonomy reconcile: no synthesis model configured for KB %s", kbID)
	}
	chatModel, err := s.modelService.GetChatModel(ctx, synthesisModelID)
	if err != nil {
		return fmt.Errorf("wiki taxonomy reconcile: get chat model: %w", err)
	}

	return s.reconcileTaxonomy(ctx, chatModel, kbID, types.LanguageNameFromContext(ctx))
}

// reconcileTaxonomy collapses synonymous category_path folders across the KB
// onto a single canonical path. It is per-KB (cost O(folder count), not page
// count): one LLM call clusters the distinct folders, then matching pages are
// re-pointed in Go. Slugs are NEVER touched — only the navigation category.
func (s *wikiIngestService) reconcileTaxonomy(ctx context.Context, chatModel chat.Chat, kbID, lang string) error {
	if s.wikiService == nil || chatModel == nil {
		return nil
	}
	paths, err := s.wikiService.ListDistinctCategoryPaths(ctx, kbID, wikiTaxonomyPromptMaxPaths)
	if err != nil {
		return fmt.Errorf("list distinct category paths: %w", err)
	}
	if len(paths) < 2 {
		return nil // nothing to merge
	}

	remap := s.buildTaxonomyRemap(ctx, chatModel, paths, lang)
	if len(remap) == 0 {
		return nil
	}

	updated := s.applyTaxonomyRemap(ctx, kbID, remap)
	if updated > 0 {
		logger.Infof(ctx, "wiki taxonomy reconcile: re-pointed %d pages via %d rules (kb=%s)", updated, len(remap), kbID)
		if err := s.wikiService.RebuildIndexPage(ctx, kbID); err != nil {
			logger.Warnf(ctx, "wiki taxonomy reconcile: rebuild index failed: %v", err)
		}
	}
	return nil
}

// maybeReconcileTaxonomy runs a reconcile pass only when this batch introduced
// at least one category folder that was not present in startKeys (the snapshot
// taken before the batch ran). This keeps the extra LLM call off the hot path
// for batches that only reuse established folders.
func (s *wikiIngestService) maybeReconcileTaxonomy(
	ctx context.Context, chatModel chat.Chat, kbID, lang string, startKeys map[string]bool,
) {
	if s.wikiService == nil {
		return
	}
	paths, err := s.wikiService.ListDistinctCategoryPaths(ctx, kbID, wikiTaxonomyPromptMaxPaths)
	if err != nil || len(paths) < 2 {
		return
	}
	introducedNewFolder := false
	for _, p := range paths {
		if !startKeys[categoryPathKey(cleanCategoryPathParts(p))] {
			introducedNewFolder = true
			break
		}
	}
	if !introducedNewFolder {
		return
	}
	if err := s.reconcileTaxonomy(ctx, chatModel, kbID, lang); err != nil {
		logger.Warnf(ctx, "wiki taxonomy reconcile failed: %v", err)
	}
}

// snapshotCategoryKeys returns the set of canonical category keys currently in
// use, used as the "before" baseline for maybeReconcileTaxonomy.
func (s *wikiIngestService) snapshotCategoryKeys(ctx context.Context, kbID string) map[string]bool {
	out := map[string]bool{}
	if s.wikiService == nil {
		return out
	}
	paths, err := s.wikiService.ListDistinctCategoryPaths(ctx, kbID, wikiTaxonomyPromptMaxPaths)
	if err != nil {
		return out
	}
	for _, p := range paths {
		out[categoryPathKey(cleanCategoryPathParts(p))] = true
	}
	return out
}

// buildTaxonomyRemap asks the LLM to cluster synonymous folders and returns a
// map keyed by the canonical key of each non-canonical path to its replacement.
func (s *wikiIngestService) buildTaxonomyRemap(
	ctx context.Context, chatModel chat.Chat, paths [][]string, lang string,
) map[string][]string {
	taxonomyText := formatExistingTaxonomyForPrompt(paths)
	if strings.TrimSpace(taxonomyText) == "" {
		return nil
	}
	raw, err := s.generateWithTemplate(ctx, chatModel, agent.WikiTaxonomyReconcilePrompt, map[string]string{
		"CurrentTaxonomy": taxonomyText,
		"Language":        lang,
	})
	if err != nil {
		logger.Warnf(ctx, "wiki taxonomy reconcile: LLM call failed: %v", err)
		return nil
	}
	raw = cleanLLMJSON(raw)
	var parsed wikiTaxonomyRemapResult
	if jerr := json.Unmarshal([]byte(raw), &parsed); jerr != nil {
		logger.Warnf(ctx, "wiki taxonomy reconcile: parse failed: %v\nRaw: %s", jerr, raw)
		return nil
	}

	out := make(map[string][]string, len(parsed.Remap))
	for _, e := range parsed.Remap {
		from := cleanCategoryPathParts(e.From)
		to := cleanCategoryPathParts(e.To)
		if len(from) == 0 || len(to) == 0 {
			continue
		}
		fromKey := categoryPathKey(from)
		if fromKey == categoryPathKey(to) {
			continue // no-op rule
		}
		out[fromKey] = to
	}
	return out
}

// applyTaxonomyRemap scans entity/concept pages and re-points any whose
// category matches a remap rule. Returns the number of pages updated.
func (s *wikiIngestService) applyTaxonomyRemap(ctx context.Context, kbID string, remap map[string][]string) int {
	var cursor string
	var updated int
	for updated < wikiTaxonomyReconcileMaxPages {
		pages, next, err := s.wikiService.ListPagesCursor(ctx, kbID, cursor, wikiTaxonomyReconcilePageSize)
		if err != nil {
			logger.Warnf(ctx, "wiki taxonomy reconcile: list pages failed: %v", err)
			break
		}
		if len(pages) == 0 {
			break
		}
		for _, p := range pages {
			if p.PageType != types.WikiPageTypeEntity && p.PageType != types.WikiPageTypeConcept {
				continue
			}
			newPath, ok := remapCategoryPath([]string(p.CategoryPath), remap)
			if !ok {
				continue
			}
			p.CategoryPath = types.StringArray(newPath)
			p.WikiPath = "" // force recompute in normalizeWikiHierarchy
			if err := s.wikiService.UpdatePageMeta(ctx, p); err != nil {
				logger.Warnf(ctx, "wiki taxonomy reconcile: update %s failed: %v", p.Slug, err)
				continue
			}
			updated++
			if updated >= wikiTaxonomyReconcileMaxPages {
				break
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return updated
}

// remapCategoryPath returns the canonical category for current when it matches
// a remap "from" key, plus whether a change is needed. Pure (no DB) so it can
// be unit-tested in isolation.
func remapCategoryPath(current []string, remap map[string][]string) ([]string, bool) {
	cleaned := cleanCategoryPathParts(current)
	if len(cleaned) == 0 {
		return nil, false
	}
	to, ok := remap[categoryPathKey(cleaned)]
	if !ok {
		return nil, false
	}
	if categoryPathKey(cleaned) == categoryPathKey(cleanCategoryPathParts(to)) {
		return nil, false
	}
	return append([]string(nil), to...), true
}

// cleanCategoryPathParts normalizes a category path the same way the page
// hierarchy normalizer does (separator/quote cleanup, type-label stripping,
// depth cap) so remap keys match what is stored on pages.
func cleanCategoryPathParts(parts []string) []string {
	return types.CleanWikiCategoryPath(parts)
}

// categoryPathKey builds a stable comparison key for a cleaned category path.
func categoryPathKey(parts []string) string {
	return strings.Join(parts, "\u0000")
}
