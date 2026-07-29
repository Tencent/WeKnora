package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

var errWikiProvenanceRepositoryUnavailable = errors.New("wiki provenance repository is unavailable")

func (s *wikiPageService) provenanceRepository() (interfaces.WikiProvenanceRepository, error) {
	repo, ok := s.repo.(interfaces.WikiProvenanceRepository)
	if !ok {
		return nil, errWikiProvenanceRepositoryUnavailable
	}
	return repo, nil
}

// GetPageWithSources implements interfaces.WikiProvenanceService without
// changing the default WikiPageService read shape.
func (s *wikiPageService) GetPageWithSources(
	ctx context.Context, kbID, slug string,
) (*types.WikiPageDetailResponse, error) {
	page, err := s.GetPageBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, err
	}
	response := &types.WikiPageDetailResponse{WikiPage: page, Blocks: []*types.WikiPageBlock{}}
	if page.CurrentBlockSetID == "" {
		return response, nil
	}

	repo, err := s.provenanceRepository()
	if err != nil {
		return nil, err
	}
	set, err := repo.GetBlockSet(ctx, kbID, page.CurrentBlockSetID)
	if err != nil {
		return nil, fmt.Errorf("get current wiki block set: %w", err)
	}
	if set.Status != types.WikiBlockSetStatusPublished || set.PageID != page.ID {
		return nil, fmt.Errorf("current wiki block set is not a published set for page %s", page.ID)
	}
	// Title/status/type/alias edits create a normal page revision but do not
	// change the sourced body or summary, so the same immutable block set may
	// legitimately remain current across those metadata-only versions. A block
	// set from the future is never valid; matching rendered text is the decisive
	// invariant for an older set that remains attached.
	if set.PageVersion > page.Version || set.RenderedContent != page.Content ||
		set.RenderedSummary != page.Summary {
		return nil, fmt.Errorf("current wiki block set does not match page content at version %d", page.Version)
	}
	if _, _, err := s.refreshWikiBlockSourceValidation(ctx, page.TenantID, kbID, set); err != nil {
		return nil, fmt.Errorf("validate current wiki sources: %w", err)
	}
	sortWikiProvenance(set)
	for _, block := range set.Blocks {
		for _, source := range block.Sources {
			if source.CitationKey == "" {
				source.CitationKey = wikiCitationKey(source)
			}
		}
	}
	response.Blocks = set.Blocks
	return response, nil
}

func (s *wikiPageService) GetCurrentPageBlockSet(
	ctx context.Context, kbID, pageID string,
) (*types.WikiPageBlockSet, error) {
	page, err := s.repo.GetByID(ctx, pageID)
	if err != nil {
		return nil, err
	}
	if page.KnowledgeBaseID != kbID {
		return nil, repository.ErrWikiPageNotFound
	}
	if page.CurrentBlockSetID == "" {
		return nil, nil
	}
	repo, err := s.provenanceRepository()
	if err != nil {
		return nil, err
	}
	return repo.GetBlockSet(ctx, kbID, page.CurrentBlockSetID)
}

func (s *wikiPageService) ListPageSlugsByKnowledgeSource(
	ctx context.Context, kbID, knowledgeID string,
) ([]string, error) {
	repo, err := s.provenanceRepository()
	if err != nil {
		return nil, err
	}
	refs, err := repo.ListBlockReferencesByKnowledge(ctx, kbID, knowledgeID)
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref == nil || ref.PageSlug == "" || ref.PageSlug == "index" {
			continue
		}
		if _, exists := seen[ref.PageSlug]; exists {
			continue
		}
		seen[ref.PageSlug] = struct{}{}
		slugs = append(slugs, ref.PageSlug)
	}
	return slugs, nil
}

// refreshWikiBlockSourceValidation compares persisted evidence with the live
// chunk rows. It mutates only the response/snapshot in memory: published block
// sets remain immutable. checked=false means this lightweight service was
// constructed without a chunk repository (primarily old tests/fakes).
func (s *wikiPageService) refreshWikiBlockSourceValidation(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	set *types.WikiPageBlockSet,
) (checked bool, allFresh bool, err error) {
	if set == nil {
		return true, true, nil
	}
	if s.chunkRepo == nil {
		return false, false, nil
	}

	chunkIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, block := range set.Blocks {
		for _, source := range block.Sources {
			if source == nil || source.ChunkID == "" {
				continue
			}
			if _, exists := seen[source.ChunkID]; exists {
				continue
			}
			seen[source.ChunkID] = struct{}{}
			chunkIDs = append(chunkIDs, source.ChunkID)
		}
	}
	sort.Strings(chunkIDs)
	chunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		return true, false, err
	}
	chunkByID := make(map[string]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			chunkByID[chunk.ID] = chunk
		}
	}

	allFresh = true
	for _, block := range set.Blocks {
		located := 0
		invalid := 0
		for _, source := range block.Sources {
			var chunk *types.Chunk
			if source != nil {
				chunk = chunkByID[source.ChunkID]
			}
			if wikiBlockSourceMatchesLiveChunk(source, chunk, tenantID, kbID) {
				source.ValidationStatus = types.WikiSourceValidationLocated
				located++
				continue
			}
			source.ValidationStatus = types.WikiSourceValidationInvalid
			invalid++
			allFresh = false
		}
		if types.NormalizeWikiEditSource(block.AuthorType) != types.WikiEditSourcePipeline ||
			!wikiBlockNeedsSource(block) {
			continue
		}
		if len(block.Sources) == 0 {
			block.ProvenanceStatus = types.WikiBlockProvenanceUnsupported
			allFresh = false
		} else if invalid > 0 {
			if located > 0 {
				block.ProvenanceStatus = types.WikiBlockProvenancePartial
			} else {
				block.ProvenanceStatus = types.WikiBlockProvenanceUnsupported
			}
		}
	}
	return true, allFresh, nil
}

func wikiBlockSourceMatchesLiveChunk(
	source *types.WikiBlockSource,
	chunk *types.Chunk,
	tenantID uint64,
	kbID string,
) bool {
	if source == nil || chunk == nil || !chunk.IsEnabled {
		return false
	}
	if source.TenantID != tenantID || source.KnowledgeBaseID != kbID {
		return false
	}
	if chunk.TenantID != tenantID || chunk.KnowledgeBaseID != kbID ||
		chunk.KnowledgeID != source.KnowledgeID || chunk.ID != source.ChunkID {
		return false
	}
	if !types.IsWikiProvenanceChunkType(chunk.ChunkType) {
		return false
	}
	if source.ChunkRevision != chunk.ContentRevision || source.ChunkContentHash == "" ||
		source.ChunkContentHash != wikiTextHash(chunk.Content) {
		return false
	}
	if source.EvidenceHash == "" ||
		source.EvidenceHash != wikiTextHash(normalizeWikiEvidence(source.Evidence)) {
		return false
	}
	_, err := validateWikiEvidenceQuote(source.Evidence, chunk.Content)
	return err == nil
}

func (s *wikiPageService) markStagedWikiBlockSetFailed(
	ctx context.Context,
	repo interfaces.WikiProvenanceRepository,
	kbID, blockSetID string,
) {
	if err := repo.MarkStagedBlockSetFailed(ctx, kbID, blockSetID); err != nil {
		logger.Warnf(ctx, "mark staged wiki block set %s failed: %v", blockSetID, err)
	}
}

// SavePageWithProvenance publishes one fully-validated page/block snapshot.
// The staged tree is written first; the repository then switches page content,
// history and the current block pointer in one transaction.
func (s *wikiPageService) SavePageWithProvenance(
	ctx context.Context,
	page *types.WikiPage,
	set *types.WikiPageBlockSet,
) (*types.WikiPage, error) {
	if page == nil || set == nil {
		return nil, errors.New("wiki page and block set are required")
	}
	if page.KnowledgeBaseID == "" || page.Slug == "" {
		return nil, errors.New("wiki page knowledge_base_id and slug are required")
	}
	repo, err := s.provenanceRepository()
	if err != nil {
		return nil, err
	}

	existing, getErr := s.repo.GetBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	exists := getErr == nil && existing != nil
	if getErr != nil && !errors.Is(getErr, repository.ErrWikiPageNotFound) {
		return nil, fmt.Errorf("get existing wiki page: %w", getErr)
	}

	now := time.Now()
	if exists {
		page.ID = existing.ID
		page.TenantID = existing.TenantID
		page.Version = existing.Version
		page.CreatedAt = existing.CreatedAt
		page.InLinks = append(types.StringArray(nil), existing.InLinks...)
		page.CurrentBlockSetID = existing.CurrentBlockSetID
	} else {
		if page.ID == "" {
			page.ID = uuid.NewString()
		}
		page.Version = 1
		if page.Status == "" {
			page.Status = types.WikiPageStatusPublished
		}
		page.CreatedAt = now
	}
	page.LastEditSource = types.WikiEditSourceFromContext(ctx)
	page.LastEditorID, _ = types.UserIDFromContext(ctx)
	page.UpdatedAt = now
	page.Content = stripWikiInlineChunkCitations(set.RenderedContent)
	page.Summary = stripWikiInlineChunkCitations(set.RenderedSummary)
	page.OutLinks = s.parseOutLinks(page.Content)
	if err := s.applyFolderToPage(ctx, page); err != nil {
		return nil, err
	}
	normalizeWikiHierarchy(page)

	set.ID = strings.TrimSpace(set.ID)
	if set.ID == "" {
		set.ID = uuid.NewString()
	}
	set.TenantID = page.TenantID
	set.KnowledgeBaseID = page.KnowledgeBaseID
	set.PageID = page.ID
	set.Status = types.WikiBlockSetStatusStaged
	set.RenderedContent = page.Content
	set.RenderedSummary = page.Summary
	if exists {
		set.PageVersion = existing.Version + 1
	} else {
		set.PageVersion = page.Version
	}
	if set.CreatedAt.IsZero() {
		set.CreatedAt = now
	}
	prepareWikiBlockSetRows(set)
	if err := validateWikiBlockSetForPublish(set); err != nil {
		return nil, err
	}
	page.SourceRefs, page.ChunkRefs = deriveWikiPageRefs(set.Blocks)

	if err := repo.SaveStagedBlockSet(ctx, set); err != nil {
		return nil, fmt.Errorf("save staged wiki block set: %w", err)
	}

	oldOutLinks := types.StringArray(nil)
	if exists {
		oldOutLinks = append(oldOutLinks, existing.OutLinks...)
		if err := repo.PublishStagedBlockSet(ctx, page, set.ID); err != nil {
			s.markStagedWikiBlockSetFailed(ctx, repo, page.KnowledgeBaseID, set.ID)
			return nil, fmt.Errorf("publish wiki block set: %w", err)
		}
		s.pruneRevisions(ctx, page.ID, page.Version)
	} else if err := repo.CreatePageWithBlockSet(ctx, page, set.ID); err != nil {
		s.markStagedWikiBlockSetFailed(ctx, repo, page.KnowledgeBaseID, set.ID)
		return nil, fmt.Errorf("create wiki page with block set: %w", err)
	}

	page.CurrentBlockSetID = set.ID
	s.removeInLinks(ctx, page.KnowledgeBaseID, page.Slug, oldOutLinks)
	s.updateInLinks(ctx, page.KnowledgeBaseID, page.Slug, page.OutLinks)
	return page, nil
}

// RemoveKnowledgeFromPage performs deterministic paragraph-granularity
// cleanup. A block is the atomic authored unit, so if the deleted document
// contributed any evidence to a block we remove that whole block rather than
// asking an LLM to guess which words came from which source.
func (s *wikiPageService) RemoveKnowledgeFromPage(
	ctx context.Context, kbID, slug, knowledgeID string,
) (bool, bool, error) {
	page, err := s.repo.GetBySlug(ctx, kbID, slug)
	if err != nil {
		return false, false, err
	}
	if page.CurrentBlockSetID == "" {
		return false, false, nil
	}
	oldOutLinks := append(types.StringArray(nil), page.OutLinks...)
	repo, err := s.provenanceRepository()
	if err != nil {
		return false, false, err
	}
	current, err := repo.GetBlockSet(ctx, kbID, page.CurrentBlockSetID)
	if err != nil {
		return true, false, err
	}

	affectedLogicalIDs := make(map[string]struct{})
	summaryAffected := false
	for _, block := range current.Blocks {
		for _, source := range block.Sources {
			if source.KnowledgeID == knowledgeID {
				affectedLogicalIDs[block.LogicalBlockID] = struct{}{}
				if block.BlockType == types.WikiBlockTypeSummary {
					summaryAffected = true
				}
				break
			}
		}
	}
	if len(affectedLogicalIDs) == 0 {
		return true, false, nil
	}

	target := &types.WikiPageBlockSet{
		ID:              uuid.NewString(),
		TenantID:        page.TenantID,
		KnowledgeBaseID: kbID,
		PageID:          page.ID,
		PageVersion:     page.Version + 1,
		Status:          types.WikiBlockSetStatusStaged,
		RenderedContent: current.RenderedContent,
		RenderedSummary: current.RenderedSummary,
		GenerationRunID: "delete:" + knowledgeID,
		CreatedAt:       time.Now(),
	}
	if err := repo.CloneBlockSetToStaged(ctx, current.ID, target); err != nil {
		return true, false, fmt.Errorf("clone wiki block set for source removal: %w", err)
	}
	stageOpen := true
	defer func() {
		if stageOpen {
			s.markStagedWikiBlockSetFailed(ctx, repo, kbID, target.ID)
		}
	}()
	cloned, err := repo.GetBlockSet(ctx, kbID, target.ID)
	if err != nil {
		return true, false, err
	}
	deleteIDs := make([]string, 0, len(affectedLogicalIDs))
	for _, block := range cloned.Blocks {
		if _, affected := affectedLogicalIDs[block.LogicalBlockID]; affected {
			deleteIDs = append(deleteIDs, block.ID)
		}
	}
	if err := repo.DeleteBlocksFromStaged(ctx, kbID, target.ID, deleteIDs); err != nil {
		return true, false, fmt.Errorf("delete sourced wiki blocks: %w", err)
	}
	remaining, err := repo.GetBlockSet(ctx, kbID, target.ID)
	if err != nil {
		return true, false, err
	}
	if !hasSubstantiveWikiBlocks(remaining.Blocks) {
		// Delete only the exact page version inspected above. A concurrent ingest
		// may have published a fresh version after we decided the old one was
		// empty; an unconditional slug delete would erase that new content.
		if err := repo.DeletePageIfVersion(ctx, kbID, page.ID, page.Version); err != nil {
			return true, false, fmt.Errorf("delete empty sourced wiki page: %w", err)
		}
		stageOpen = false
		s.removeInLinks(ctx, kbID, slug, oldOutLinks)
		return true, true, nil
	}

	content := renderStoredWikiBlocks(remaining.Blocks)
	if summaryAffected {
		page.Summary = ""
	}
	if err := repo.UpdateStagedBlockSetRender(ctx, kbID, target.ID, content, page.Summary); err != nil {
		return true, false, err
	}
	page.Content = content
	page.SourceRefs, page.ChunkRefs = deriveWikiPageRefs(remaining.Blocks)
	page.OutLinks = s.parseOutLinks(page.Content)
	page.UpdatedAt = time.Now()
	page.LastEditSource = types.WikiEditSourcePipeline
	page.LastEditorID = ""
	if err := repo.PublishStagedBlockSet(ctx, page, target.ID); err != nil {
		return true, false, err
	}
	stageOpen = false
	page.CurrentBlockSetID = target.ID
	s.removeInLinks(ctx, kbID, slug, oldOutLinks)
	s.updateInLinks(ctx, kbID, slug, page.OutLinks)
	return true, false, nil
}

func prepareWikiBlockSetRows(set *types.WikiPageBlockSet) {
	for index, block := range set.Blocks {
		if block.ID == "" {
			block.ID = uuid.NewString()
		}
		if block.LogicalBlockID == "" {
			block.LogicalBlockID = uuid.NewString()
		}
		block.BlockSetID = set.ID
		block.SortOrder = index
		if block.ContentHash == "" {
			block.ContentHash = wikiTextHash(normalizeWikiBlockText(block.Content))
		}
		if block.AuthorType == "" {
			block.AuthorType = types.WikiEditSourcePipeline
		}
		if block.ProvenanceStatus == "" {
			block.ProvenanceStatus = types.WikiBlockProvenanceUnsupported
		}
		if block.CreatedAt.IsZero() {
			block.CreatedAt = set.CreatedAt
		}
		for sourceIndex, source := range block.Sources {
			if source.ID == "" {
				source.ID = uuid.NewString()
			}
			source.BlockID = block.ID
			source.TenantID = set.TenantID
			source.KnowledgeBaseID = set.KnowledgeBaseID
			source.SortOrder = sourceIndex
			if source.EvidenceHash == "" {
				source.EvidenceHash = wikiTextHash(normalizeWikiEvidence(source.Evidence))
			}
			if source.CreatedAt.IsZero() {
				source.CreatedAt = set.CreatedAt
			}
		}
	}
}

func validateWikiBlockSetForPublish(set *types.WikiPageBlockSet) error {
	if len(set.Blocks) == 0 {
		return errors.New("wiki block set must contain at least one block")
	}
	if rendered := renderWikiMarkdownBlocks(set.Blocks); rendered != set.RenderedContent {
		return errors.New("wiki block set rendered content does not match its blocks")
	}
	seenOrder := make(map[int]struct{}, len(set.Blocks))
	foundSummary := false
	for _, block := range set.Blocks {
		if strings.TrimSpace(block.Content) == "" {
			return fmt.Errorf("wiki block %s has empty content", block.ID)
		}
		if _, exists := seenOrder[block.SortOrder]; exists {
			return fmt.Errorf("duplicate wiki block sort order %d", block.SortOrder)
		}
		seenOrder[block.SortOrder] = struct{}{}
		if block.BlockType == types.WikiBlockTypeSummary {
			if foundSummary {
				return errors.New("wiki block set contains more than one summary block")
			}
			foundSummary = true
			if strings.TrimSpace(block.Content) != strings.TrimSpace(set.RenderedSummary) {
				return errors.New("wiki summary block does not match rendered summary")
			}
		}
		if block.AuthorType == types.WikiEditSourcePipeline && wikiBlockNeedsSource(block) {
			if block.ProvenanceStatus != types.WikiBlockProvenanceVerified {
				return fmt.Errorf("pipeline wiki block %s does not have complete claim coverage", block.ID)
			}
			if len(block.Sources) == 0 {
				return fmt.Errorf("pipeline wiki block %s has no located source", block.ID)
			}
		}
		for _, source := range block.Sources {
			if source.KnowledgeID == "" || source.ChunkID == "" || strings.TrimSpace(source.Evidence) == "" {
				return fmt.Errorf("wiki block %s contains an incomplete source", block.ID)
			}
			if source.EvidenceHash == "" || source.ChunkContentHash == "" ||
				source.EvidenceHash != wikiTextHash(normalizeWikiEvidence(source.Evidence)) {
				return fmt.Errorf("wiki block %s contains incomplete or inconsistent source hashes", block.ID)
			}
			if source.ValidationStatus != types.WikiSourceValidationLocated {
				return fmt.Errorf("wiki block %s contains unvalidated evidence", block.ID)
			}
			if source.TenantID != set.TenantID || source.KnowledgeBaseID != set.KnowledgeBaseID {
				return fmt.Errorf("wiki block %s contains a cross-scope source", block.ID)
			}
		}
	}
	return nil
}

func hasSubstantiveWikiBlocks(blocks []*types.WikiPageBlock) bool {
	for _, block := range blocks {
		// Summary is stored as a sourced block but is not part of the rendered
		// page body. A summary alone must not keep an empty page alive.
		if block.BlockType != types.WikiBlockTypeSummary && wikiBlockNeedsSource(block) {
			return true
		}
	}
	return false
}

func deriveWikiPageRefs(blocks []*types.WikiPageBlock) (types.StringArray, types.StringArray) {
	var knowledgeRefs types.StringArray
	var chunkRefs types.StringArray
	seenKnowledge := make(map[string]struct{})
	seenChunk := make(map[string]struct{})
	ordered := append([]*types.WikiPageBlock(nil), blocks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].SortOrder < ordered[j].SortOrder })
	for _, block := range ordered {
		for _, source := range block.Sources {
			if source.ValidationStatus != types.WikiSourceValidationLocated {
				continue
			}
			if source.KnowledgeID != "" {
				if _, ok := seenKnowledge[source.KnowledgeID]; !ok {
					seenKnowledge[source.KnowledgeID] = struct{}{}
					knowledgeRefs = append(knowledgeRefs, source.KnowledgeID)
				}
			}
			if source.ChunkID != "" {
				if _, ok := seenChunk[source.ChunkID]; !ok {
					seenChunk[source.ChunkID] = struct{}{}
					chunkRefs = append(chunkRefs, source.ChunkID)
				}
			}
		}
	}
	return knowledgeRefs, chunkRefs
}

func renderStoredWikiBlocks(blocks []*types.WikiPageBlock) string {
	return renderWikiMarkdownBlocks(blocks)
}

func sortWikiProvenance(set *types.WikiPageBlockSet) {
	sort.SliceStable(set.Blocks, func(i, j int) bool {
		if set.Blocks[i].SortOrder == set.Blocks[j].SortOrder {
			return set.Blocks[i].ID < set.Blocks[j].ID
		}
		return set.Blocks[i].SortOrder < set.Blocks[j].SortOrder
	})
	for _, block := range set.Blocks {
		sort.SliceStable(block.Sources, func(i, j int) bool {
			if block.Sources[i].SortOrder == block.Sources[j].SortOrder {
				return block.Sources[i].ID < block.Sources[j].ID
			}
			return block.Sources[i].SortOrder < block.Sources[j].SortOrder
		})
	}
}

func wikiCitationKey(source *types.WikiBlockSource) string {
	if source == nil {
		return ""
	}
	raw := fmt.Sprintf("%s\x00%d\x00%s\x00%d\x00%s",
		source.KnowledgeID, source.KnowledgeAttempt, source.ChunkID,
		source.ChunkRevision, source.EvidenceHash)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("src-%x", sum[:8])
}

func wikiTextHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func cloneWikiBlockSetForRepublish(source *types.WikiPageBlockSet) *types.WikiPageBlockSet {
	copySet := &types.WikiPageBlockSet{
		Status:          types.WikiBlockSetStatusStaged,
		RenderedContent: source.RenderedContent,
		RenderedSummary: source.RenderedSummary,
		Blocks:          make([]*types.WikiPageBlock, 0, len(source.Blocks)),
	}
	for _, oldBlock := range source.Blocks {
		if oldBlock == nil {
			continue
		}
		block := &types.WikiPageBlock{
			LogicalBlockID:   oldBlock.LogicalBlockID,
			BlockType:        oldBlock.BlockType,
			SectionPath:      append(types.StringArray(nil), oldBlock.SectionPath...),
			SortOrder:        oldBlock.SortOrder,
			Content:          oldBlock.Content,
			ContentHash:      oldBlock.ContentHash,
			AuthorType:       oldBlock.AuthorType,
			ProvenanceStatus: oldBlock.ProvenanceStatus,
			Sources:          make([]*types.WikiBlockSource, 0, len(oldBlock.Sources)),
		}
		for _, oldSource := range oldBlock.Sources {
			if oldSource == nil {
				continue
			}
			sourceCopy := *oldSource
			sourceCopy.ID = ""
			sourceCopy.BlockID = ""
			sourceCopy.CitationKey = ""
			sourceCopy.CreatedAt = time.Time{}
			block.Sources = append(block.Sources, &sourceCopy)
		}
		copySet.Blocks = append(copySet.Blocks, block)
	}
	return copySet
}

var _ interfaces.WikiProvenanceService = (*wikiPageService)(nil)
