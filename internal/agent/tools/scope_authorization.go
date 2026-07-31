package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func effectiveSearchTargetTagIDs(target *types.SearchTarget) []string {
	if target == nil {
		return nil
	}
	return dedupNonEmptyStrings(append(
		append([]string(nil), target.TagIDs...), target.ScopeTagIDs...,
	))
}

// searchTargetScope returns what a SINGLE search target authorizes.
//
// Inside one target, KnowledgeIDs and tags are an intersection, never a union.
// A tag-scoped mention is built by resolving the tag relation table into
// KnowledgeIDs and intersecting that with any explicitly mentioned documents;
// TagIDs/ScopeTagIDs are kept alongside as the physical index filter and as
// the logical scope record. Treating them as an independent way to authorize a
// document would re-admit every document carrying the tag and silently undo
// that intersection. Tags therefore only authorize when the target carries no
// resolved document whitelist.
//
// Alternatives ACROSS targets remain a union; that merge happens in callers.
func searchTargetScope(target *types.SearchTarget) (knowledgeIDs, tagIDs []string) {
	if target == nil {
		return nil, nil
	}
	knowledgeIDs = dedupNonEmptyStrings(target.KnowledgeIDs)
	if target.KnowledgeIDsSet && len(knowledgeIDs) == 0 {
		// An explicitly empty document set is a hard boundary (for example,
		// a selected folder subtree with no documents), not a tag-only or
		// whole-KB scope.
		return nil, nil
	}
	if len(knowledgeIDs) > 0 {
		return knowledgeIDs, nil
	}
	return nil, effectiveSearchTargetTagIDs(target)
}

// searchTargetIsWholeKB reports whether a target grants unrestricted access to
// its knowledge base.
func searchTargetIsWholeKB(target *types.SearchTarget) bool {
	if target == nil {
		return false
	}
	knowledgeIDs, tagIDs := searchTargetScope(target)
	return !target.KnowledgeIDsSet &&
		target.Type == types.SearchTargetTypeKnowledgeBase &&
		len(knowledgeIDs) == 0 && len(tagIDs) == 0
}

// authorizeKnowledgeInSearchTargets is the shared authorization boundary for
// every Agent tool that accepts a model-visible dN/knowledge_id. Handle
// decoding is necessary but never sufficient: the durable document must also
// belong to the server-owned search scope for this Agent execution.
func authorizeKnowledgeInSearchTargets(
	ctx context.Context,
	searchTargets types.SearchTargets,
	knowledgeID string,
	knowledgeService interfaces.KnowledgeService,
) (*types.Knowledge, error) {
	knowledgeID = strings.TrimSpace(knowledgeID)
	if knowledgeID == "" {
		return nil, fmt.Errorf("knowledge_id is required")
	}
	if knowledgeService == nil {
		return nil, fmt.Errorf("knowledge service is unavailable")
	}
	knowledge, err := knowledgeService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil || knowledge == nil {
		if err == nil {
			err = fmt.Errorf("empty result")
		}
		return nil, fmt.Errorf("document %s not found: %w", knowledgeID, err)
	}
	if !searchTargets.ContainsKB(knowledge.KnowledgeBaseID) {
		return nil, fmt.Errorf("knowledge base %s is not within the current Agent scope", knowledge.KnowledgeBaseID)
	}
	allowed, err := searchTargetsAllowKnowledgeID(
		ctx, searchTargets, knowledge.ID, knowledge.KnowledgeBaseID, knowledgeService,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to validate document scope: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("document %s is not within the current @mention scope", knowledge.ID)
	}
	return knowledge, nil
}

// authorizeChunkInSearchTargets is the chunk/FAQ counterpart of
// authorizeKnowledgeInSearchTargets. A chunk ID is accepted only after the
// server resolves its owning document and validates that document against the
// full KB/document/tag scope.
func authorizeChunkInSearchTargets(
	ctx context.Context,
	searchTargets types.SearchTargets,
	chunkID string,
	chunkService interfaces.ChunkService,
	knowledgeService interfaces.KnowledgeService,
) (*types.Chunk, error) {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return nil, fmt.Errorf("chunk_id is required")
	}
	if chunkService == nil {
		return nil, fmt.Errorf("chunk service is unavailable")
	}
	chunk, err := chunkService.GetChunkByIDOnly(ctx, chunkID)
	if err != nil || chunk == nil {
		if err == nil {
			err = fmt.Errorf("empty result")
		}
		return nil, fmt.Errorf("chunk %s not found: %w", chunkID, err)
	}
	if !chunk.IsEnabled {
		return nil, fmt.Errorf("chunk %s is disabled", chunk.ID)
	}
	if !searchTargets.ContainsKB(chunk.KnowledgeBaseID) {
		return nil, fmt.Errorf("knowledge base %s is not within the current Agent scope", chunk.KnowledgeBaseID)
	}
	allowed, err := searchTargetsAllowKnowledgeID(
		ctx, searchTargets, chunk.KnowledgeID, chunk.KnowledgeBaseID, knowledgeService,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to validate chunk scope: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("chunk %s is not within the current @mention scope", chunk.ID)
	}
	return chunk, nil
}

// validateKnowledgeBaseIDsInSearchTargets rejects hallucinated, stale, or
// out-of-scope bN values after the model-context registry resolves them.
func validateKnowledgeBaseIDsInSearchTargets(searchTargets types.SearchTargets, kbIDs []string) error {
	for _, kbID := range dedupNonEmptyStrings(kbIDs) {
		if !searchTargets.ContainsKB(kbID) {
			return fmt.Errorf("knowledge base %s is not within the current Agent scope", kbID)
		}
	}
	return nil
}

// resolveAuthorizedSourceRefs validates Wiki source_refs/suspected IDs against
// the same Agent scope and rebuilds the stored "uuid|title" representation
// from server data instead of trusting a model-supplied title suffix.
func resolveAuthorizedSourceRefs(
	ctx context.Context,
	searchTargets types.SearchTargets,
	refs []string,
	knowledgeService interfaces.KnowledgeService,
) ([]string, error) {
	resolved := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		knowledgeID := strings.TrimSpace(strings.SplitN(ref, "|", 2)[0])
		if knowledgeID == "" {
			continue
		}
		knowledge, err := authorizeKnowledgeInSearchTargets(ctx, searchTargets, knowledgeID, knowledgeService)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[knowledge.ID]; exists {
			continue
		}
		seen[knowledge.ID] = struct{}{}
		title := strings.TrimSpace(knowledge.Title)
		if title == "" {
			title = strings.TrimSpace(knowledge.FileName)
		}
		if title == "" {
			resolved = append(resolved, knowledge.ID)
		} else {
			resolved = append(resolved, knowledge.ID+"|"+title)
		}
	}
	return resolved, nil
}

type knowledgeTagsFetcher func(context.Context, []string) (map[string][]*types.KnowledgeTag, error)

func searchTargetsAllowKnowledgeID(
	ctx context.Context,
	searchTargets types.SearchTargets,
	knowledgeID string,
	kbID string,
	knowledgeService interfaces.KnowledgeService,
) (bool, error) {
	if knowledgeID == "" || kbID == "" {
		return false, nil
	}

	var tagIDs []string
	matchedKB := false
	var matchedKnowledgeTagIDs []string
	for _, target := range searchTargets {
		if target == nil || target.KnowledgeBaseID != kbID {
			continue
		}
		matchedKB = true
		if searchTargetIsWholeKB(target) {
			return true, nil
		}
		if target.KnowledgeIDsSet && len(target.KnowledgeIDs) == 0 {
			continue
		}
		targetKnowledgeIDs, targetTagIDs := searchTargetScope(target)
		for _, allowedID := range targetKnowledgeIDs {
			if allowedID == knowledgeID {
				// Physical TagIDs on an explicit document whitelist represent
				// an intersection (used by FAQ folder+tag scopes). ScopeTagIDs
				// alone are only a logical trace of an already-resolved
				// document scope and must not be checked again.
				if target.KnowledgeIDsSet && len(target.TagIDs) > 0 {
					matchedKnowledgeTagIDs = append(matchedKnowledgeTagIDs, target.TagIDs...)
					continue
				}
				return true, nil
			}
		}
		if !target.KnowledgeIDsSet {
			tagIDs = append(tagIDs, targetTagIDs...)
		}
	}
	if len(matchedKnowledgeTagIDs) > 0 && knowledgeService != nil {
		matches, err := knowledgeIDsMatchingAnyTag(ctx, []string{knowledgeID}, matchedKnowledgeTagIDs, knowledgeService.GetKnowledgeTags)
		if err != nil {
			return false, err
		}
		if matches[knowledgeID] {
			return true, nil
		}
	}
	if !matchedKB || len(tagIDs) == 0 || knowledgeService == nil {
		return false, nil
	}

	matches, err := knowledgeIDsMatchingAnyTag(ctx, []string{knowledgeID}, tagIDs, knowledgeService.GetKnowledgeTags)
	if err != nil {
		return false, err
	}
	return matches[knowledgeID], nil
}

// filterSearchResultsInSearchTargets applies the same whole-KB/document/tag
// union semantics to tools whose backend can only query by KB (notably the
// knowledge graph). It batches tag lookup and rejects results without enough
// provenance instead of turning a narrow mention into whole-KB access.
func filterSearchResultsInSearchTargets(
	ctx context.Context,
	searchTargets types.SearchTargets,
	kbID string,
	results []*types.SearchResult,
	knowledgeService interfaces.KnowledgeService,
) ([]*types.SearchResult, error) {
	var explicitIDs []string
	var tagIDs []string
	matchedKB := false
	for _, target := range searchTargets {
		if target == nil || target.KnowledgeBaseID != kbID {
			continue
		}
		matchedKB = true
		if searchTargetIsWholeKB(target) {
			return results, nil
		}
		targetKnowledgeIDs, targetTagIDs := searchTargetScope(target)
		explicitIDs = append(explicitIDs, targetKnowledgeIDs...)
		tagIDs = append(tagIDs, targetTagIDs...)
	}
	if !matchedKB {
		return nil, fmt.Errorf("knowledge base %s is not within the current Agent scope", kbID)
	}

	explicitSet := make(map[string]struct{}, len(explicitIDs))
	for _, id := range dedupNonEmptyStrings(explicitIDs) {
		explicitSet[id] = struct{}{}
	}
	remainingIDs := make([]string, 0, len(results))
	for _, result := range results {
		if result == nil || result.KnowledgeID == "" {
			continue
		}
		if result.KnowledgeBaseID != "" && result.KnowledgeBaseID != kbID {
			return nil, fmt.Errorf(
				"graph result document %s belongs to knowledge base %s, expected %s",
				result.KnowledgeID, result.KnowledgeBaseID, kbID,
			)
		}
		if _, ok := explicitSet[result.KnowledgeID]; !ok {
			remainingIDs = append(remainingIDs, result.KnowledgeID)
		}
	}
	var tagMatches map[string]bool
	if len(tagIDs) > 0 {
		if knowledgeService == nil {
			return nil, fmt.Errorf("knowledge service is unavailable for tag-scoped graph filtering")
		}
		var err error
		tagMatches, err = knowledgeIDsMatchingAnyTag(
			ctx, remainingIDs, tagIDs, knowledgeService.GetKnowledgeTags,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to validate graph result scope: %w", err)
		}
	}

	filtered := make([]*types.SearchResult, 0, len(results))
	for _, result := range results {
		if result == nil || result.KnowledgeID == "" {
			continue
		}
		_, explicit := explicitSet[result.KnowledgeID]
		if explicit || tagMatches[result.KnowledgeID] {
			filtered = append(filtered, result)
		}
	}
	return filtered, nil
}

func knowledgeIDsMatchingAnyTag(
	ctx context.Context,
	knowledgeIDs []string,
	tagIDs []string,
	fetchTags knowledgeTagsFetcher,
) (map[string]bool, error) {
	result := make(map[string]bool)
	if len(knowledgeIDs) == 0 || len(tagIDs) == 0 || fetchTags == nil {
		return result, nil
	}

	uniqueKnowledgeIDs := dedupNonEmptyStrings(knowledgeIDs)
	uniqueTagIDs := dedupNonEmptyStrings(tagIDs)
	if len(uniqueKnowledgeIDs) == 0 || len(uniqueTagIDs) == 0 {
		return result, nil
	}

	tagSet := make(map[string]bool, len(uniqueTagIDs))
	for _, tagID := range uniqueTagIDs {
		tagSet[tagID] = true
	}

	tagMap, err := fetchTags(ctx, uniqueKnowledgeIDs)
	if err != nil {
		return nil, err
	}
	for _, knowledgeID := range uniqueKnowledgeIDs {
		for _, tag := range tagMap[knowledgeID] {
			if tag != nil && tagSet[tag.ID] {
				result[knowledgeID] = true
				break
			}
		}
	}
	return result, nil
}
