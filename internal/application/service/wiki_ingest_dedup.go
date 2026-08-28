package service

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// Pre-filtering candidate existing pages before the dedup LLM call.
//
// Without pre-filtering, the dedup prompt packs the entire entity+concept
// page corpus into <existing_pages>. On knowledge bases with 100+ pages
// this inflates input tokens and — more importantly — gives weaker LLMs
// enough rope to hallucinate merges between totally unrelated slugs just
// because the output looked plausible. Observed cases include
// "城镇登记失业人员" → "中华优秀传统文化" (zero shared characters).
//
// The filter below keeps only pages that share at least some cheap
// surface-level signal with one of the new items. Fast to compute, no
// external calls, and it only ever *removes* candidates from the prompt —
// the downstream validMerge check still guards the final write.
const (
	// dedupCandidateTopK bounds how many trigram-similar existing pages
	// each new item's similarity probe returns (the LIMIT passed to
	// FindSimilarPages, applied per query term = name + each alias). Kept
	// deliberately small: the DB already orders by similarity desc behind a
	// pg_trgm threshold, so a genuine same-entity target is essentially
	// always the top hit. A tight K keeps each item's <candidates> list in
	// the dedup prompt short, which improves the model's precision and cuts
	// tokens, at negligible recall cost.
	dedupCandidateTopK = 5

	// dedupCandidateScoreFloor is the Jaccard floor. Pairs at or above
	// this similarity are always included regardless of the top-K cap.
	// Tuned so that "城镇登记失业人员" vs "中华优秀传统文化" (Jaccard 0)
	// is excluded while "Acme Corp" vs "Acme Corporation" (Jaccard ≈ 0.5)
	// clearly passes.
	dedupCandidateScoreFloor = 0.08

	// dedupSmallCorpusBypass skips pre-filtering entirely when the
	// existing-page corpus is already small enough to fit in the prompt
	// without degrading the LLM. The filter only earns its keep on large
	// KBs; on small ones it risks cutting legitimate matches with no
	// real token savings.
	dedupSmallCorpusBypass = 25
)

// dedupSurface is the pre-computed similarity feature set for one side of
// a (new item, existing page) comparison.
type dedupSurface struct {
	// slugTokens are the kebab-case tokens from the slug base (after "/").
	// Slugs are an orthogonal signal to the surface names — Chinese pages
	// carry their pinyin here, which keeps the filter useful on purely
	// Latin-script new items too.
	slugTokens map[string]struct{}

	// nameGramSets holds one char-bigram set per surface form (name and
	// each alias). We keep them separate so the pair score is max-over-
	// surfaces — a rare alias match shouldn't be diluted by the primary
	// name disagreeing.
	nameGramSets []map[string]struct{}
}

// countEntityConceptPages returns how many of the given pages are
// entity- or concept-typed. Used only for logging the prefilter's
// reduction ratio.
func countEntityConceptPages(pages []*types.WikiPage) int {
	n := 0
	for _, p := range pages {
		if p == nil {
			continue
		}
		if p.PageType == types.WikiPageTypeEntity || p.PageType == types.WikiPageTypeConcept {
			n++
		}
	}
	return n
}

// selectDedupCandidatePages returns the subset of allPages plausibly
// related to at least one of newItems. Non-entity/concept pages are
// dropped unconditionally. The returned slice preserves the input order
// so the downstream prompt stays stable across runs.
//
// On small corpora (<= dedupSmallCorpusBypass entries) this is a no-op
// aside from the page-type filter.
func selectDedupCandidatePages(
	newItems []extractedItem,
	allPages []*types.WikiPage,
) []*types.WikiPage {
	pages := make([]*types.WikiPage, 0, len(allPages))
	for _, p := range allPages {
		if p == nil {
			continue
		}
		if p.PageType != types.WikiPageTypeEntity && p.PageType != types.WikiPageTypeConcept {
			continue
		}
		pages = append(pages, p)
	}
	if len(pages) == 0 {
		return pages
	}
	if len(newItems) == 0 || len(pages) <= dedupSmallCorpusBypass {
		return pages
	}

	pageFeats := make([]dedupSurface, len(pages))
	for i, p := range pages {
		surfaces := make([]string, 0, 1+len(p.Aliases))
		surfaces = append(surfaces, p.Title)
		surfaces = append(surfaces, []string(p.Aliases)...)
		pageFeats[i] = dedupSurface{
			slugTokens:   slugBaseTokens(p.Slug),
			nameGramSets: gramsPerSurface(surfaces),
		}
	}

	selected := make(map[int]bool, len(pages))
	for _, it := range newItems {
		surfaces := make([]string, 0, 1+len(it.Aliases))
		surfaces = append(surfaces, it.Name)
		surfaces = append(surfaces, it.Aliases...)
		itemFeat := dedupSurface{
			slugTokens:   slugBaseTokens(it.Slug),
			nameGramSets: gramsPerSurface(surfaces),
		}
		if len(itemFeat.slugTokens) == 0 && len(itemFeat.nameGramSets) == 0 {
			continue
		}

		scores := make([]struct {
			idx   int
			score float64
		}, len(pageFeats))
		for i := range pageFeats {
			scores[i].idx = i
			scores[i].score = dedupPairScore(itemFeat, pageFeats[i])
		}
		// Stable sort so ties break deterministically by original index.
		sort.SliceStable(scores, func(i, j int) bool {
			return scores[i].score > scores[j].score
		})

		topKRemaining := dedupCandidateTopK
		for _, s := range scores {
			if s.score >= dedupCandidateScoreFloor {
				selected[s.idx] = true
				continue
			}
			// Below the floor but we still owe the LLM some candidates so
			// it can decline cleanly — fill the top-K budget with the
			// highest-scoring remaining pages, as long as the score isn't
			// flatly zero (a zero score means we have nothing in common
			// with this page and including it just invites hallucination).
			if topKRemaining > 0 && s.score > 0 {
				selected[s.idx] = true
				topKRemaining--
				continue
			}
			break
		}
	}

	out := make([]*types.WikiPage, 0, len(selected))
	for i, p := range pages {
		if selected[i] {
			out = append(out, p)
		}
	}
	return out
}

// dedupMergeRejectReason validates a single LLM-proposed merge (srcSlug →
// dstSlug) against deterministic, model-independent rules. It returns an
// empty string when the merge is allowed, or a short human-readable reason
// when it must be rejected. srcCandidates is the set of existing-page slugs
// that surfaced for srcSlug's OWN similarity probe (see itemCandidates in
// deduplicateExtractedBatch).
//
// The per-item scoping check is the key guard: the dedup prompt shows the
// model a flattened union of candidates across every new item, so nothing
// stops a weak model from pairing an item with a page that was only similar
// to a *different* item (observed: entity/tencent-open → entity/hiring-agent,
// which share no trigram signal). Requiring dstSlug to be one of srcSlug's
// own candidates rejects that entire class without depending on the model
// getting the semantic judgment right.
func dedupMergeRejectReason(srcSlug, dstSlug string, srcCandidates map[string]bool) string {
	if !srcCandidates[dstSlug] {
		// Covers both an outright hallucinated target (in no candidate
		// set at all) and a real page that was only similar to another
		// item. Either way the pair lacks a similarity signal for THIS
		// item, so it is not a safe merge.
		return "target is not a similarity candidate for this item"
	}
	srcSlash := strings.Index(srcSlug, "/")
	dstSlash := strings.Index(dstSlug, "/")
	if srcSlash <= 0 || dstSlash <= 0 {
		// A type-prefixed slug must look like "entity/foo" or
		// "concept/bar". An LLM that emits an un-prefixed slug here is
		// hallucinating; reject rather than fall through the prefix-
		// equality check (which would treat both empty prefixes as a
		// match).
		return "missing type prefix"
	}
	if srcSlug[:srcSlash+1] != dstSlug[:dstSlash+1] {
		return "type mismatch: " + srcSlug[:srcSlash+1] + " vs " + dstSlug[:dstSlash+1]
	}
	return ""
}

// normalizeWikiIdentityTitle returns the conservative identity key used only
// to prevent same-type, same-title pages from being created under different
// slugs. It intentionally preserves punctuation: "寓言" and "《寓言》" can
// represent a concept and a work/chapter and must remain distinguishable.
// Removing whitespace and folding case is enough to close model formatting
// drift such as "Acme Corp" vs "acme  corp".
func normalizeWikiIdentityTitle(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(title))
}

// exactIdentityTarget returns the stable existing page for an extracted item
// when a same-type candidate has the exact normalized display title. The LLM
// remains responsible for semantic/alias matches; this deterministic fast path
// only covers the unambiguous identity invariant that one page type should not
// carry two pages with the same visible title.
func exactIdentityTarget(
	item extractedItem,
	pageType string,
	candidates map[string]bool,
	pages map[string]*types.WikiPageLite,
) string {
	identity := normalizeWikiIdentityTitle(item.Name)
	if identity == "" {
		return ""
	}
	matches := make([]string, 0, 2)
	for slug := range candidates {
		page := pages[slug]
		if page == nil || page.PageType != pageType {
			continue
		}
		if normalizeWikiIdentityTitle(page.Title) == identity {
			matches = append(matches, page.Slug)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	for _, slug := range matches {
		if slug == item.Slug {
			return slug
		}
	}
	sort.Strings(matches)
	return matches[0]
}

// claimWikiIdentitySlug reserves one slug for a normalized (KB, page type,
// title) identity before Reduce starts. Standard mode uses Redis so concurrent
// batches/processes converge; the batch-local map covers Lite mode and a Redis
// error. Existing-page resolutions are authoritative and refresh the claim.
func (s *wikiIngestService) claimWikiIdentitySlug(
	ctx context.Context,
	kbID, pageType, title, proposedSlug string,
	authoritative bool,
	batchCtx *WikiBatchContext,
) string {
	identity := normalizeWikiIdentityTitle(title)
	expectedPrefix := pageType + "/"
	if identity == "" || proposedSlug == "" || !strings.HasPrefix(proposedSlug, expectedPrefix) {
		return proposedSlug
	}

	claim := proposedSlug
	claimKey := wikiIdentityClaimPrefix + kbID + ":" + pageType + ":" + identity
	if s.redisClient != nil {
		if authoritative {
			if err := s.redisClient.Set(ctx, claimKey, proposedSlug, wikiIdentityClaimTTL).Err(); err != nil {
				logger.Warnf(ctx, "wiki ingest: identity claim refresh failed for %s: %v", proposedSlug, err)
			}
		} else {
			won, err := s.redisClient.SetNX(ctx, claimKey, proposedSlug, wikiIdentityClaimTTL).Result()
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: identity claim failed for %s: %v (using batch-local claim)", proposedSlug, err)
			} else if !won {
				if existing, getErr := s.redisClient.Get(ctx, claimKey).Result(); getErr == nil &&
					strings.HasPrefix(existing, expectedPrefix) {
					claim = existing
				} else if getErr != nil {
					logger.Warnf(ctx, "wiki ingest: identity claim read failed for %s: %v", proposedSlug, getErr)
				}
			}
		}
	}

	if batchCtx != nil {
		localKey := pageType + "\x00" + identity
		if authoritative {
			batchCtx.identityClaims.Store(localKey, claim)
		} else {
			actual, _ := batchCtx.identityClaims.LoadOrStore(localKey, claim)
			if existing, ok := actual.(string); ok && strings.HasPrefix(existing, expectedPrefix) {
				claim = existing
			}
		}
	}
	return claim
}

// mergeExtractedIdentity folds duplicate candidates that converged to the same
// slug. It preserves every alias/chunk reference and keeps the richer fallback
// text, so convergence never discards evidence before the citation/reduce pass.
func mergeExtractedIdentity(dst, src extractedItem) extractedItem {
	if dst.Name == "" {
		dst.Name = src.Name
	} else if src.Name != "" && src.Name != dst.Name {
		dst.Aliases = appendUniqueString(dst.Aliases, src.Name)
	}
	for _, alias := range src.Aliases {
		dst.Aliases = appendUniqueString(dst.Aliases, alias)
	}
	if len([]rune(src.Description)) > len([]rune(dst.Description)) {
		dst.Description = src.Description
	}
	if len([]rune(src.Details)) > len([]rune(dst.Details)) {
		dst.Details = src.Details
	}
	for _, chunkID := range src.SourceChunks {
		dst.SourceChunks = appendUniqueString(dst.SourceChunks, chunkID)
	}
	return dst
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// stabilizeExtractedIdentities applies deterministic existing-page resolutions,
// cross-batch identity claims, and same-result coalescing. mergeTargets maps an
// extracted slug to a verified existing page slug.
func (s *wikiIngestService) stabilizeExtractedIdentities(
	ctx context.Context,
	kbID, pageType string,
	items []extractedItem,
	mergeTargets map[string]string,
	batchCtx *WikiBatchContext,
) []extractedItem {
	out := make([]extractedItem, 0, len(items))
	bySlug := make(map[string]int, len(items))
	for _, item := range items {
		originalSlug := item.Slug
		authoritative := false
		if target := mergeTargets[originalSlug]; target != "" {
			item.Slug = target
			authoritative = true
		}
		item.Slug = s.claimWikiIdentitySlug(
			ctx, kbID, pageType, item.Name, item.Slug, authoritative, batchCtx,
		)
		if idx, ok := bySlug[item.Slug]; ok {
			out[idx] = mergeExtractedIdentity(out[idx], item)
			continue
		}
		bySlug[item.Slug] = len(out)
		out = append(out, item)
	}
	return out
}

// dedupPairScore is the max similarity between any surface form of a and
// b (plus the slug-token similarity). Slug and name signals live in
// different symbol spaces (ASCII pinyin vs raw surface form) so we take
// the max rather than e.g. their average.
func dedupPairScore(a, b dedupSurface) float64 {
	best := searchutil.Jaccard(a.slugTokens, b.slugTokens)
	for _, ag := range a.nameGramSets {
		for _, bg := range b.nameGramSets {
			if v := searchutil.Jaccard(ag, bg); v > best {
				best = v
			}
		}
	}
	return best
}

// slugBaseTokens returns the kebab-case tokens of a slug's base component.
// "entity/beijing-nongshang-yinxing" → {"beijing", "nongshang", "yinxing"}.
func slugBaseTokens(slug string) map[string]struct{} {
	if slug == "" {
		return nil
	}
	base := slug
	if i := strings.Index(slug, "/"); i >= 0 {
		base = slug[i+1:]
	}
	base = strings.ToLower(base)
	fields := strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || unicode.IsSpace(r)
	})
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(fields))
	for _, tok := range fields {
		if tok == "" {
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

// gramsPerSurface computes a gram set per non-empty surface form.
func gramsPerSurface(surfaces []string) []map[string]struct{} {
	out := make([]map[string]struct{}, 0, len(surfaces))
	for _, s := range surfaces {
		g := surfaceGrams(s)
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// surfaceGrams returns a character-bigram set for a surface form after
// lowercasing and stripping non-letter/digit runes. Bigrams work well
// across both CJK (where each bigram approximates a word) and Latin
// (where they catch stem overlap like "corporation" ↔ "corp"). Single-
// rune strings fall back to a 1-gram so they still contribute a signal.
func surfaceGrams(s string) map[string]struct{} {
	if s == "" {
		return nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	runes := []rune(b.String())
	if len(runes) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(runes))
	if len(runes) == 1 {
		out[string(runes)] = struct{}{}
		return out
	}
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}
