package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

func wikiTextChunkIDs(chunks []*types.Chunk) []string {
	ids := make([]string, 0, len(chunks))
	seen := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil || !chunk.IsEnabled || chunk.ID == "" || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		if !types.IsWikiProvenanceChunkType(chunk.ChunkType) {
			continue
		}
		if _, ok := seen[chunk.ID]; ok {
			continue
		}
		seen[chunk.ID] = struct{}{}
		ids = append(ids, chunk.ID)
	}
	return ids
}

type wikiAttemptTrackerWithError interface {
	LatestAttemptWithError(ctx context.Context, knowledgeID string) (int, error)
}

func latestWikiAttempt(
	ctx context.Context, tracker SpanTracker, knowledgeID string,
) (int, error) {
	if checked, ok := tracker.(wikiAttemptTrackerWithError); ok {
		return checked.LatestAttemptWithError(ctx, knowledgeID)
	}
	// Compatibility path for test/custom trackers that implement the original
	// SpanTracker contract. Their integer result is still checked for exact
	// equality below, so an unexpected zero cannot authorize publication.
	return tracker.LatestAttempt(ctx, knowledgeID), nil
}

// filterCurrentWikiAttempts prevents an older reparse that finishes late from
// overwriting paragraph provenance produced by a newer parse attempt. A
// positive attempt must exactly match the latest persisted attempt; if the
// lookup fails, Reduce fails too so the pending operation is retried instead
// of publishing without a generation check. Retractions are generation-free
// because deleting stale provenance must remain possible after its source row
// is gone. Attempt-zero additions are dropped rather than published without a
// generation guard.
func (s *wikiIngestService) filterCurrentWikiAttempts(
	ctx context.Context, updates []SlugUpdate,
) ([]SlugUpdate, error) {
	latestByKnowledge := make(map[string]int)
	filtered := make([]SlugUpdate, 0, len(updates))
	tracker := s.tracker()
	for _, update := range updates {
		if update.Type == "retract" || update.Type == "retractStale" {
			filtered = append(filtered, update)
			continue
		}
		if update.KnowledgeID == "" || update.KnowledgeAttempt <= 0 {
			continue
		}
		latest, ok := latestByKnowledge[update.KnowledgeID]
		if !ok {
			var err error
			latest, err = latestWikiAttempt(ctx, tracker, update.KnowledgeID)
			if err != nil {
				return nil, fmt.Errorf(
					"wiki provenance: load latest attempt for knowledge %s: %w",
					update.KnowledgeID, err,
				)
			}
			latestByKnowledge[update.KnowledgeID] = latest
		}
		if latest != update.KnowledgeAttempt {
			continue
		}
		filtered = append(filtered, update)
	}
	return filtered, nil
}

// buildWikiPageBlockSet turns the final Markdown produced by the existing
// editor prompts into immutable blocks, then re-aligns every factual block to
// exact quotes from all still-current source chunks. Existing logical IDs are
// carried forward only when normalised block content is unchanged.
func (s *wikiIngestService) buildWikiPageBlockSet(
	ctx context.Context,
	chatModel chat.Chat,
	page *types.WikiPage,
	previous *types.WikiPageBlockSet,
	additions []SlugUpdate,
	retracts []SlugUpdate,
	summaryUpdate *SlugUpdate,
	tenantID uint64,
	language string,
) (*types.WikiPageBlockSet, error) {
	bodyBlocks := splitWikiMarkdownBlocks(page.Content, previous, types.WikiEditSourcePipeline)
	bodyBlocks = restoreMissingWikiManualBlocks(previous, bodyBlocks)
	bodyBlocks = dropNewExactCopiesOfWikiManualBlocks(previous, bodyBlocks)
	blocks := make([]*types.WikiPageBlock, 0, len(bodyBlocks)+1)
	summaryBlock := buildWikiSummaryBlock(page.Summary, previous, types.WikiEditSourcePipeline)
	if manualSummary := missingWikiManualSummary(previous, summaryBlock); manualSummary != nil {
		summaryBlock = cloneWikiManualBlock(manualSummary)
		page.Summary = manualSummary.Content
	}
	if summaryBlock != nil {
		blocks = append(blocks, summaryBlock)
	}
	blocks = append(blocks, bodyBlocks...)
	if len(blocks) == 0 {
		return nil, fmt.Errorf("wiki provenance: generated page %s has no content blocks", page.Slug)
	}

	retractedKnowledge := make(map[string]struct{}, len(retracts))
	for _, retract := range retracts {
		if retract.KnowledgeID != "" {
			retractedKnowledge[retract.KnowledgeID] = struct{}{}
		}
	}
	// Summary pages are wholesale replacements for one knowledge document and
	// intentionally do not carry a separate retract update. Treat their prior
	// generation as retracted so deleted old chunks cannot remain candidates.
	if summaryUpdate != nil && summaryUpdate.KnowledgeID != "" {
		retractedKnowledge[summaryUpdate.KnowledgeID] = struct{}{}
	}

	contexts := make(map[string]wikiProvenanceSourceContext)
	registerUpdate := func(update SlugUpdate) {
		if update.KnowledgeID == "" {
			return
		}
		contexts[update.KnowledgeID] = wikiProvenanceSourceContext{
			KnowledgeAttempt: update.KnowledgeAttempt,
			SourceTitle:      update.DocTitle,
		}
	}
	if summaryUpdate != nil {
		registerUpdate(*summaryUpdate)
	}
	for _, addition := range additions {
		registerUpdate(addition)
	}
	// Preserve every still-live document dependency from the previous set,
	// even when the page-edit LLM rewrote the corresponding paragraph so no
	// source row was carried forward by content matching. Otherwise a
	// multi-document page could silently forget one contributor before the
	// final alignment pass.
	addPreviousWikiSourceContexts(previous, retractedKnowledge, contexts)

	// Reuse source metadata only to discover surviving source documents and
	// their generation. The aligner revalidates every final block and replaces
	// all source rows; no old citation is trusted blindly.
	for _, block := range blocks {
		kept := block.Sources[:0]
		for _, source := range block.Sources {
			if source == nil {
				continue
			}
			if _, removed := retractedKnowledge[source.KnowledgeID]; removed {
				continue
			}
			kept = append(kept, source)
			if source.KnowledgeID != "" {
				if _, exists := contexts[source.KnowledgeID]; !exists {
					contexts[source.KnowledgeID] = wikiProvenanceSourceContext{
						KnowledgeAttempt: source.KnowledgeAttempt,
						SourceTitle:      source.DocumentTitle,
					}
				}
			}
		}
		block.Sources = kept
	}

	// User/agent-authored blocks are deliberately outside document evidence
	// alignment. They carry authorship, not a file citation, and must survive a
	// later pipeline refresh even when their prose cannot be found in any source
	// chunk. Only pipeline-authored factual blocks participate in claim mapping.
	alignableBlocks := make([]*types.WikiPageBlock, 0, len(blocks))
	for _, block := range blocks {
		if types.NormalizeWikiEditSource(block.AuthorType) == types.WikiEditSourcePipeline &&
			wikiBlockNeedsSource(block) {
			alignableBlocks = append(alignableBlocks, block)
			continue
		}
		block.Sources = nil
		block.ProvenanceStatus = types.WikiBlockProvenanceUnsupported
	}
	if len(alignableBlocks) == 0 {
		return &types.WikiPageBlockSet{
			TenantID:        page.TenantID,
			KnowledgeBaseID: page.KnowledgeBaseID,
			PageID:          page.ID,
			Status:          types.WikiBlockSetStatusStaged,
			RenderedContent: renderWikiMarkdownBlocks(blocks),
			RenderedSummary: page.Summary,
			GenerationRunID: wikiGenerationRunID(contexts),
			Blocks:          blocks,
		}, nil
	}

	if len(contexts) == 0 {
		return nil, fmt.Errorf("wiki provenance: page %s has no candidate source chunks", page.Slug)
	}
	chunks, err := s.loadLiveWikiProvenanceChunks(ctx, tenantID, contexts)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("wiki provenance: page %s candidate chunks no longer exist", page.Slug)
	}

	sourcesByBlock, err := s.alignWikiBlockSourcesForContexts(
		ctx, chatModel, alignableBlocks, chunks, contexts, language,
	)
	if err != nil {
		return nil, err
	}
	for _, block := range blocks {
		if types.NormalizeWikiEditSource(block.AuthorType) != types.WikiEditSourcePipeline ||
			!wikiBlockNeedsSource(block) {
			block.Sources = nil
			block.ProvenanceStatus = types.WikiBlockProvenanceUnsupported
			continue
		}
		block.Sources = sourcesByBlock[block.ID]
		// The aligner deterministically splits each factual block into claim
		// units and rejects the whole generation unless every unit has validated
		// contiguous evidence. Sources are merged back onto the rendered block.
		block.ProvenanceStatus = types.WikiBlockProvenanceVerified
	}

	return &types.WikiPageBlockSet{
		TenantID:        page.TenantID,
		KnowledgeBaseID: page.KnowledgeBaseID,
		PageID:          page.ID,
		Status:          types.WikiBlockSetStatusStaged,
		RenderedContent: renderWikiMarkdownBlocks(blocks),
		RenderedSummary: page.Summary,
		GenerationRunID: wikiGenerationRunID(contexts),
		Blocks:          blocks,
	}, nil
}

func isWikiManualBlock(block *types.WikiPageBlock) bool {
	if block == nil {
		return false
	}
	switch types.NormalizeWikiEditSource(block.AuthorType) {
	case types.WikiEditSourceUser, types.WikiEditSourceAgent:
		return true
	default:
		return false
	}
}

func cloneWikiManualBlock(block *types.WikiPageBlock) *types.WikiPageBlock {
	if block == nil {
		return nil
	}
	clone := *block
	clone.ID = uuid.NewString()
	clone.BlockSetID = ""
	clone.SectionPath = append(types.StringArray(nil), block.SectionPath...)
	clone.Sources = nil
	clone.ProvenanceStatus = types.WikiBlockProvenanceUnsupported
	clone.CreatedAt = time.Time{}
	return &clone
}

func missingWikiManualSummary(
	previous *types.WikiPageBlockSet, current *types.WikiPageBlock,
) *types.WikiPageBlock {
	if previous == nil {
		return nil
	}
	for _, block := range previous.Blocks {
		if block == nil || block.BlockType != types.WikiBlockTypeSummary || !isWikiManualBlock(block) {
			continue
		}
		if current != nil && current.LogicalBlockID == block.LogicalBlockID {
			return nil
		}
		return block
	}
	return nil
}

// restoreMissingWikiManualBlocks makes editor-authored prose independent of
// LLM obedience. Exact blocks returned by the model already inherit their
// logical IDs in splitWikiMarkdownBlocks. Any remaining user/agent block is
// inserted next to the nearest surviving block from the prior sequence; this
// preserves the authored unit even when the model accidentally omits it.
func restoreMissingWikiManualBlocks(
	previous *types.WikiPageBlockSet, current []*types.WikiPageBlock,
) []*types.WikiPageBlock {
	if previous == nil || len(previous.Blocks) == 0 {
		return current
	}
	result := append([]*types.WikiPageBlock(nil), current...)
	present := make(map[string]struct{}, len(result))
	for _, block := range result {
		if block != nil && block.LogicalBlockID != "" {
			present[block.LogicalBlockID] = struct{}{}
		}
	}
	indexOf := func(logicalID string) int {
		if logicalID == "" {
			return -1
		}
		for index, block := range result {
			if block != nil && block.LogicalBlockID == logicalID {
				return index
			}
		}
		return -1
	}
	previousLogicalID := func(index int) string {
		if index < 0 || index >= len(previous.Blocks) || previous.Blocks[index] == nil {
			return ""
		}
		return previous.Blocks[index].LogicalBlockID
	}

	for previousIndex, block := range previous.Blocks {
		if block == nil || block.BlockType == types.WikiBlockTypeSummary || !isWikiManualBlock(block) {
			continue
		}
		if _, exists := present[block.LogicalBlockID]; block.LogicalBlockID != "" && exists {
			continue
		}

		insertAt := len(result)
		anchored := false
		for index := previousIndex - 1; index >= 0; index-- {
			if position := indexOf(previousLogicalID(index)); position >= 0 {
				insertAt = position + 1
				anchored = true
				break
			}
		}
		if !anchored {
			for index := previousIndex + 1; index < len(previous.Blocks); index++ {
				if position := indexOf(previousLogicalID(index)); position >= 0 {
					insertAt = position
					break
				}
			}
		}

		clone := cloneWikiManualBlock(block)
		result = append(result, nil)
		copy(result[insertAt+1:], result[insertAt:])
		result[insertAt] = clone
		if clone.LogicalBlockID != "" {
			present[clone.LogicalBlockID] = struct{}{}
		}
	}
	for index, block := range result {
		if block != nil {
			block.SortOrder = index
		}
	}
	return result
}

// dropNewExactCopiesOfWikiManualBlocks removes only duplicates introduced by
// the current LLM rewrite. A pre-existing pipeline block with the same text is
// kept by logical ID, as is the same prose in another section. This makes the
// protection deterministic without guessing whether similar wording is a
// paraphrase or a genuinely new source-backed fact.
func dropNewExactCopiesOfWikiManualBlocks(
	previous *types.WikiPageBlockSet, current []*types.WikiPageBlock,
) []*types.WikiPageBlock {
	if previous == nil || len(previous.Blocks) == 0 || len(current) == 0 {
		return current
	}
	previousLogicalIDs := make(map[string]struct{}, len(previous.Blocks))
	manualKeys := make(map[string]struct{})
	for _, block := range previous.Blocks {
		if block == nil || block.BlockType == types.WikiBlockTypeSummary {
			continue
		}
		if block.LogicalBlockID != "" {
			previousLogicalIDs[block.LogicalBlockID] = struct{}{}
		}
		if isWikiManualBlock(block) {
			manualKeys[wikiBlockMatchKey(block, true)] = struct{}{}
		}
	}
	if len(manualKeys) == 0 {
		return current
	}

	result := make([]*types.WikiPageBlock, 0, len(current))
	for _, block := range current {
		if block == nil {
			continue
		}
		if !isWikiManualBlock(block) {
			_, exactManualCopy := manualKeys[wikiBlockMatchKey(block, true)]
			_, existedBefore := previousLogicalIDs[block.LogicalBlockID]
			if exactManualCopy && !existedBefore {
				continue
			}
		}
		block.SortOrder = len(result)
		result = append(result, block)
	}
	return result
}

func addPreviousWikiSourceContexts(
	previous *types.WikiPageBlockSet,
	retractedKnowledge map[string]struct{},
	contexts map[string]wikiProvenanceSourceContext,
) {
	if previous == nil {
		return
	}
	for _, block := range previous.Blocks {
		if block == nil {
			continue
		}
		for _, source := range block.Sources {
			if source == nil || source.KnowledgeID == "" {
				continue
			}
			if _, retracted := retractedKnowledge[source.KnowledgeID]; retracted {
				continue
			}
			if _, alreadyCurrent := contexts[source.KnowledgeID]; alreadyCurrent {
				continue
			}
			contexts[source.KnowledgeID] = wikiProvenanceSourceContext{
				KnowledgeAttempt: source.KnowledgeAttempt,
				SourceTitle:      source.DocumentTitle,
			}
		}
	}
}

// loadLiveWikiProvenanceChunks loads the complete current text-chunk set for
// every document that still contributes to a page. Loading only historical
// citation IDs would make unchanged paragraphs blind to other live chunks in
// the same document and could preserve a source mapping that is no longer the
// best (or only) exact evidence after a manual chunk edit.
func (s *wikiIngestService) loadLiveWikiProvenanceChunks(
	ctx context.Context,
	tenantID uint64,
	contexts map[string]wikiProvenanceSourceContext,
) ([]*types.Chunk, error) {
	knowledgeIDs := make([]string, 0, len(contexts))
	for knowledgeID := range contexts {
		if knowledgeID != "" {
			knowledgeIDs = append(knowledgeIDs, knowledgeID)
		}
	}
	sort.Strings(knowledgeIDs)

	seen := make(map[string]*types.Chunk)
	chunks := make([]*types.Chunk, 0)
	for _, knowledgeID := range knowledgeIDs {
		knowledgeChunks, err := s.chunkRepo.ListChunksByKnowledgeID(ctx, tenantID, knowledgeID)
		if err != nil {
			return nil, fmt.Errorf("wiki provenance: load live chunks for knowledge %s: %w", knowledgeID, err)
		}
		for _, chunk := range knowledgeChunks {
			if chunk == nil || !chunk.IsEnabled || chunk.ID == "" || strings.TrimSpace(chunk.Content) == "" {
				continue
			}
			if !types.IsWikiProvenanceChunkType(chunk.ChunkType) {
				continue
			}
			if chunk.KnowledgeID != knowledgeID {
				return nil, fmt.Errorf(
					"wiki provenance: chunk %s belongs to knowledge %s, expected %s",
					chunk.ID, chunk.KnowledgeID, knowledgeID,
				)
			}
			if prior, duplicate := seen[chunk.ID]; duplicate {
				if prior.KnowledgeID != chunk.KnowledgeID || prior.Content != chunk.Content {
					return nil, fmt.Errorf("wiki provenance: conflicting duplicate chunk ID %q", chunk.ID)
				}
				continue
			}
			seen[chunk.ID] = chunk
			chunks = append(chunks, chunk)
		}
	}
	return chunks, nil
}

func wikiGenerationRunID(contexts map[string]wikiProvenanceSourceContext) string {
	keys := make([]string, 0, len(contexts))
	for knowledgeID, sourceContext := range contexts {
		keys = append(keys, knowledgeID+":"+strconv.Itoa(sourceContext.KnowledgeAttempt))
	}
	sort.Strings(keys)
	value := strings.Join(keys, ",")
	if len(value) <= 64 {
		return value
	}
	return wikiTextHash(value)[:64]
}

func provenanceServiceForWikiIngest(service interfaces.WikiPageService) (interfaces.WikiProvenanceService, error) {
	provenance, ok := service.(interfaces.WikiProvenanceService)
	if !ok {
		return nil, fmt.Errorf("wiki provenance service is unavailable")
	}
	return provenance, nil
}
