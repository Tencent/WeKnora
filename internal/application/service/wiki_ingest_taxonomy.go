package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// wikiTaxonomyItem is one entity/concept page to be filed into the directory.
type wikiTaxonomyItem struct {
	slug     string
	title    string
	pageType string
	about    string
}

// planBatchTaxonomy assigns a directory path to every entity/concept slug in the
// batch in ONE planning pass (chunked for large batches), so the whole set lands
// on a single coherent tree that reuses existing folders. This replaces per-page,
// parallel CATEGORY invention — which couldn't converge, worst of all on the
// founding batch when the KB has no folders to anchor on. The returned map is
// keyed by slug; an entry may be an empty slice when the item is unclassifiable.
// Reduce applies these only to pages that don't already have a category.
func (s *wikiIngestService) planBatchTaxonomy(
	ctx context.Context,
	chatModel chat.Chat,
	kb *types.KnowledgeBase,
	slugUpdates map[string][]SlugUpdate,
	lang string,
) map[string][]string {
	if kb == nil {
		return nil
	}
	items := collectTaxonomyItems(slugUpdates)
	if len(items) == 0 {
		return nil
	}

	// Existing folders anchor reuse. Errors (e.g. a dialect without the query)
	// just mean the plan designs a fresh tree — no fatal dependency.
	var pool [][]string
	if s.wikiService != nil {
		paths, err := s.wikiService.ListDistinctCategoryPaths(ctx, kb.ID, wikiTaxonomyFolderPoolMax)
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: list category paths for plan failed: %v", err)
		} else {
			pool = paths
		}
	}

	// Preprocess the pool down to the folders relevant to THIS batch, so the
	// planner reuses established folders without every prompt carrying the whole
	// directory. Small/healthy taxonomies are fed whole (best recall, no cost).
	existing := s.selectRelevantFolders(ctx, kb, items, pool)

	result := make(map[string][]string, len(items))
	for start := 0; start < len(items); start += wikiTaxonomyPlanChunkSize {
		end := start + wikiTaxonomyPlanChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		tree := formatExistingTaxonomyForPrompt(existing)
		if strings.TrimSpace(tree) == "" {
			tree = wikiTaxonomyEmptyTreeHint
		}

		var itemsBlock strings.Builder
		for _, it := range chunk {
			fmt.Fprintf(&itemsBlock, "- slug: %s | title: %s | type: %s | about: %s\n",
				it.slug, it.title, it.pageType, previewText(it.about, 120))
		}

		raw, err := s.generateWithTemplate(ctx, chatModel, agent.WikiTaxonomyPlanPrompt, map[string]string{
			"ExistingTaxonomy": tree,
			"Items":            itemsBlock.String(),
			"Language":         lang,
		})
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: taxonomy plan call failed (%d items): %v", len(chunk), err)
			continue
		}

		for slug, path := range parseTaxonomyAssignments(raw) {
			clean := types.CleanWikiCategoryPath(path)
			result[slug] = clean
			if len(clean) > 0 {
				existing = append(existing, clean) // feed forward so later chunks converge
			}
		}
	}
	return result
}

// selectRelevantFolders narrows the existing folder pool to the subset worth
// showing the planner for THIS batch. A healthy navigation directory is small,
// so it is fed whole (perfect reuse recall, no embedding cost). Only once folders
// are numerous does similarity preprocessing kick in: all level-1 folders are
// always kept as coarse anchors, and each item pulls in its nearest deeper
// folders by embedding similarity. KBs without an embedding model (wiki-only)
// fall back to a capped feed-all.
func (s *wikiIngestService) selectRelevantFolders(
	ctx context.Context, kb *types.KnowledgeBase, items []wikiTaxonomyItem, pool [][]string,
) [][]string {
	if len(pool) <= wikiTaxonomyFeedAllMaxFolders {
		return pool
	}

	// Split into always-kept level-1 anchors and the deeper candidate folders
	// that similarity selects among.
	l1Seen := make(map[string]struct{})
	var l1Paths, deeper [][]string
	for _, p := range pool {
		if len(p) == 0 {
			continue
		}
		if _, ok := l1Seen[p[0]]; !ok {
			l1Seen[p[0]] = struct{}{}
			l1Paths = append(l1Paths, []string{p[0]})
		}
		if len(p) >= 2 {
			deeper = append(deeper, p)
		}
	}

	if !kb.NeedsEmbeddingModel() || strings.TrimSpace(kb.EmbeddingModelID) == "" || len(deeper) == 0 {
		return capFolders(pool, wikiTaxonomyPromptMaxPaths)
	}

	embedder, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: taxonomy plan embed model unavailable, feeding all folders: %v", err)
		return capFolders(pool, wikiTaxonomyPromptMaxPaths)
	}

	folderTexts := make([]string, len(deeper))
	for i, p := range deeper {
		folderTexts[i] = strings.Join(p, " / ")
	}
	itemTexts := make([]string, len(items))
	for i, it := range items {
		itemTexts[i] = strings.TrimSpace(it.title + " " + previewText(it.about, 120))
	}

	folderVecs, err := embedder.BatchEmbed(ctx, folderTexts)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: taxonomy plan folder embed failed, feeding all folders: %v", err)
		return capFolders(pool, wikiTaxonomyPromptMaxPaths)
	}
	itemVecs, err := embedder.BatchEmbed(ctx, itemTexts)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: taxonomy plan item embed failed, feeding all folders: %v", err)
		return capFolders(pool, wikiTaxonomyPromptMaxPaths)
	}

	selected := append(l1Paths, selectFoldersByVectors(deeper, folderVecs, itemVecs, wikiTaxonomyRelevantTopK)...)
	return capFolders(selected, wikiTaxonomyPromptMaxPaths)
}

// selectFoldersByVectors returns the deeper folders that rank in any item's
// top-K by cosine similarity, preserving the input order for determinism.
func selectFoldersByVectors(deeper [][]string, folderVecs, itemVecs [][]float32, topK int) [][]string {
	if len(deeper) != len(folderVecs) || len(itemVecs) == 0 || topK <= 0 {
		return nil
	}
	chosen := make(map[int]struct{})
	for _, iv := range itemVecs {
		type scored struct {
			idx int
			sim float64
		}
		ranking := make([]scored, 0, len(folderVecs))
		for fi, fv := range folderVecs {
			ranking = append(ranking, scored{fi, cosineSimilarity(iv, fv)})
		}
		sort.SliceStable(ranking, func(a, b int) bool { return ranking[a].sim > ranking[b].sim })
		for k := 0; k < topK && k < len(ranking); k++ {
			chosen[ranking[k].idx] = struct{}{}
		}
	}
	out := make([][]string, 0, len(chosen))
	for i := range deeper {
		if _, ok := chosen[i]; ok {
			out = append(out, deeper[i])
		}
	}
	return out
}

// cosineSimilarity returns the cosine of two equal-length vectors, or 0 for empty
// / mismatched / zero-norm inputs.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// capFolders truncates a folder list to at most max entries (max <= 0 = no cap).
func capFolders(paths [][]string, max int) [][]string {
	if max > 0 && len(paths) > max {
		return paths[:max]
	}
	return paths
}

// collectTaxonomyItems extracts the entity/concept pages from a batch's slug
// updates, in deterministic slug order so chunk boundaries are stable. Summary
// and retract-only slugs are skipped (they carry no directory category).
func collectTaxonomyItems(slugUpdates map[string][]SlugUpdate) []wikiTaxonomyItem {
	slugs := make([]string, 0, len(slugUpdates))
	for slug := range slugUpdates {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	items := make([]wikiTaxonomyItem, 0, len(slugs))
	for _, slug := range slugs {
		for _, u := range slugUpdates[slug] {
			if u.Type != types.WikiPageTypeEntity && u.Type != types.WikiPageTypeConcept {
				continue
			}
			title := strings.TrimSpace(u.Item.Name)
			if title == "" {
				title = slug
			}
			items = append(items, wikiTaxonomyItem{
				slug:     slug,
				title:    title,
				pageType: u.Type,
				about:    strings.TrimSpace(u.Item.Description),
			})
			break // one entry per slug is enough for classification
		}
	}
	return items
}

// parseTaxonomyAssignments parses the planning LLM's JSON into a slug → path map.
// Malformed output yields nil; individual entries with a blank slug are dropped.
func parseTaxonomyAssignments(raw string) map[string][]string {
	raw = cleanLLMJSON(raw)
	if raw == "" {
		return nil
	}
	var parsed struct {
		Assignments []struct {
			Slug string   `json:"slug"`
			Path []string `json:"path"`
		} `json:"assignments"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	out := make(map[string][]string, len(parsed.Assignments))
	for _, a := range parsed.Assignments {
		slug := strings.TrimSpace(a.Slug)
		if slug == "" {
			continue
		}
		out[slug] = a.Path
	}
	return out
}
