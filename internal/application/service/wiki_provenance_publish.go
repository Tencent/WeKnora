package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

type wikiProvenancePublishService struct {
	repo interfaces.WikiProvenanceRepository
	now  func() time.Time
}

func NewWikiProvenancePublishService(
	repo interfaces.WikiProvenanceRepository,
) interfaces.WikiProvenancePublishService {
	return &wikiProvenancePublishService{repo: repo, now: time.Now}
}

func (s *wikiProvenancePublishService) Publish(
	ctx context.Context,
	request *types.WikiProvenancePublishRequest,
) (*types.WikiProvenancePublishResult, error) {
	normalized, knowledgeIDs, err := normalizeWikiPublishRequest(request)
	if err != nil {
		return nil, err
	}

	var result *types.WikiProvenancePublishResult
	err = s.repo.WithTransaction(ctx, func(tx interfaces.WikiProvenanceRepository) error {
		if err := tx.EnsureCurrentPage(ctx, &normalized.PageProjection); err != nil {
			return err
		}
		if err := tx.LockPublishScope(
			ctx,
			normalized.TenantID,
			normalized.KnowledgeBaseID,
			normalized.PageID,
			knowledgeIDs,
		); err != nil {
			return err
		}

		existing, err := tx.FindPageRevisionByPublishKey(
			ctx,
			normalized.TenantID,
			normalized.KnowledgeBaseID,
			normalized.PageID,
			normalized.IdempotencyKey,
		)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.PublishFingerprint != normalized.PageRevision.PublishFingerprint {
				return types.ErrWikiPublishIdempotencyConflict
			}
			if existing.Status != types.WikiPageRevisionPublished {
				return fmt.Errorf("idempotent page revision is not published: %s", existing.Status)
			}
			result = &types.WikiProvenancePublishResult{
				PageRevision:     existing,
				AlreadyPublished: true,
			}
			return nil
		}

		newRevisionIDs := make(map[string]types.KnowledgeRevision, len(normalized.KnowledgeRevisions))
		resolvedRevisionIDs := make(map[string]string, len(normalized.KnowledgeRevisions))
		revisionsToCreate := make([]types.KnowledgeRevision, 0, len(normalized.KnowledgeRevisions))
		for i := range normalized.KnowledgeRevisions {
			revision := &normalized.KnowledgeRevisions[i]
			existingRevision, findErr := tx.FindKnowledgeRevisionByContentHash(
				ctx,
				normalized.TenantID,
				normalized.KnowledgeBaseID,
				revision.KnowledgeID,
				revision.ContentHash,
				revision.ParseAttempt,
			)
			if findErr != nil {
				return findErr
			}
			if existingRevision != nil {
				aliasID := revision.ID
				for sourceIndex := range normalized.Sources {
					if normalized.Sources[sourceIndex].KnowledgeRevisionID == aliasID {
						normalized.Sources[sourceIndex].KnowledgeRevisionID = existingRevision.ID
					}
				}
				resolvedRevisionIDs[revision.KnowledgeID] = existingRevision.ID
				continue
			}
			revision.RevisionNo, err = tx.NextKnowledgeRevisionNo(
				ctx, normalized.TenantID, normalized.KnowledgeBaseID, revision.KnowledgeID,
			)
			if err != nil {
				return err
			}
			if err := revision.Validate(); err != nil {
				return fmt.Errorf("validate knowledge revision: %w", err)
			}
			newRevisionIDs[revision.ID] = *revision
			resolvedRevisionIDs[revision.KnowledgeID] = revision.ID
			revisionsToCreate = append(revisionsToCreate, *revision)
		}
		normalized.KnowledgeRevisions = revisionsToCreate

		for _, source := range normalized.Sources {
			if revision, ok := newRevisionIDs[source.KnowledgeRevisionID]; ok {
				if revision.KnowledgeID != source.KnowledgeID {
					return errors.New("source knowledge_id does not match its new knowledge revision")
				}
				continue
			}
			revision, err := tx.GetKnowledgeRevision(
				ctx,
				normalized.TenantID,
				normalized.KnowledgeBaseID,
				source.KnowledgeRevisionID,
			)
			if err != nil {
				return err
			}
			if revision == nil || revision.KnowledgeID != source.KnowledgeID {
				return types.ErrWikiPublishScopeNotFound
			}
			if revision.Status != types.KnowledgeRevisionPublished &&
				revision.Status != types.KnowledgeRevisionSuperseded {
				return fmt.Errorf("source knowledge revision is not readable: %s", revision.Status)
			}
		}

		normalized.PageRevision.RevisionNo, err = tx.NextPageRevisionNo(
			ctx, normalized.TenantID, normalized.KnowledgeBaseID, normalized.PageID,
		)
		if err != nil {
			return err
		}
		if err := normalized.PageRevision.Validate(); err != nil {
			return fmt.Errorf("validate page revision: %w", err)
		}

		for i := range normalized.KnowledgeRevisions {
			if err := tx.CreateKnowledgeRevision(ctx, &normalized.KnowledgeRevisions[i]); err != nil {
				return err
			}
		}
		if err := tx.CreatePageRevision(ctx, &normalized.PageRevision); err != nil {
			return err
		}
		if err := tx.CreateBlocks(ctx, normalized.Blocks); err != nil {
			return err
		}
		if err := tx.CreateBlockSources(ctx, normalized.Sources); err != nil {
			return err
		}
		pageSources := buildWikiPageSourceProjection(normalized)
		if err := tx.ReplacePageSources(
			ctx, normalized.TenantID, normalized.KnowledgeBaseID, normalized.PageID, pageSources,
		); err != nil {
			return err
		}

		publishedAt := s.now().UTC()
		for i := range normalized.KnowledgeRevisions {
			revision := &normalized.KnowledgeRevisions[i]
			if err := tx.PublishKnowledgeRevision(
				ctx,
				normalized.TenantID,
				normalized.KnowledgeBaseID,
				revision.KnowledgeID,
				revision.ID,
				publishedAt,
			); err != nil {
				return err
			}
			revision.Status = types.KnowledgeRevisionPublished
			revision.PublishedAt = &publishedAt
		}
		if err := tx.PublishPageRevision(
			ctx,
			normalized.TenantID,
			normalized.KnowledgeBaseID,
			normalized.PageID,
			normalized.PageRevision.ID,
			publishedAt,
		); err != nil {
			return err
		}
		if err := tx.UpdateCurrentPage(
			ctx,
			normalized.TenantID,
			normalized.KnowledgeBaseID,
			&normalized.PageProjection,
			&normalized.PageRevision,
			publishedAt,
		); err != nil {
			return err
		}

		normalized.PageRevision.Status = types.WikiPageRevisionPublished
		normalized.PageRevision.PublishedAt = &publishedAt
		publishedRevision := normalized.PageRevision
		result = &types.WikiProvenancePublishResult{
			PageRevision:       &publishedRevision,
			KnowledgeRevisions: resolvedRevisionIDs,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("publish wiki provenance: %w", err)
	}
	return result, nil
}

func normalizeWikiPublishRequest(
	request *types.WikiProvenancePublishRequest,
) (*types.WikiProvenancePublishRequest, []string, error) {
	if request == nil {
		return nil, nil, errors.New("wiki provenance publish request is required")
	}
	if request.TenantID == 0 || request.KnowledgeBaseID == "" || request.PageID == "" {
		return nil, nil, errors.New("tenant_id, knowledge_base_id and page_id are required")
	}
	idempotencyKey := strings.TrimSpace(request.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, nil, errors.New("idempotency_key is required")
	}
	if len(idempotencyKey) > 128 {
		return nil, nil, errors.New("idempotency_key cannot exceed 128 bytes")
	}
	if len(request.Blocks) == 0 {
		return nil, nil, errors.New("at least one wiki page block is required")
	}

	normalized := &types.WikiProvenancePublishRequest{
		TenantID:           request.TenantID,
		KnowledgeBaseID:    request.KnowledgeBaseID,
		PageID:             request.PageID,
		IdempotencyKey:     idempotencyKey,
		PageProjection:     request.PageProjection,
		KnowledgeRevisions: append([]types.KnowledgeRevision(nil), request.KnowledgeRevisions...),
		PageRevision:       request.PageRevision,
		Blocks:             append([]types.WikiPageBlock(nil), request.Blocks...),
		Sources:            append([]types.WikiBlockSource(nil), request.Sources...),
	}
	projection := &normalized.PageProjection
	if err := checkStringScope("page projection id", projection.ID, normalized.PageID); err != nil {
		return nil, nil, err
	}
	if err := checkStringScope("page projection knowledge_base_id", projection.KnowledgeBaseID, normalized.KnowledgeBaseID); err != nil {
		return nil, nil, err
	}
	if err := checkTenantScope("page projection tenant_id", projection.TenantID, normalized.TenantID); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(projection.Slug) == "" {
		return nil, nil, errors.New("page projection slug is required")
	}
	projection.ID = normalized.PageID
	projection.TenantID = normalized.TenantID
	projection.KnowledgeBaseID = normalized.KnowledgeBaseID
	projection.Status = types.WikiPageStatusPublished

	page := &normalized.PageRevision
	if err := checkStringScope("page revision page_id", page.PageID, normalized.PageID); err != nil {
		return nil, nil, err
	}
	if err := checkStringScope("page revision knowledge_base_id", page.KnowledgeBaseID, normalized.KnowledgeBaseID); err != nil {
		return nil, nil, err
	}
	if err := checkTenantScope("page revision tenant_id", page.TenantID, normalized.TenantID); err != nil {
		return nil, nil, err
	}
	// Request IDs are local aliases, not database identities. Always allocate
	// fresh immutable-row IDs so a caller can reuse the same request template
	// for a later publication without primary-key collisions.
	page.ID = uuid.NewString()
	page.TenantID = normalized.TenantID
	page.KnowledgeBaseID = normalized.KnowledgeBaseID
	page.PageID = normalized.PageID
	page.PublishKey = normalized.IdempotencyKey
	page.RevisionNo = 0
	page.Status = types.WikiPageRevisionStaged
	page.CreatedAt = time.Time{}
	page.PublishedAt = nil
	page.SupersededAt = nil
	page.DeletedAt = (types.WikiProvenancePageRevision{}).DeletedAt
	if page.ProvenanceStatus == "" {
		page.ProvenanceStatus = types.WikiProvenancePartial
	}
	if page.ContentHash == "" {
		page.ContentHash = sha256Hex(page.RenderedContent)
	}
	projection.Title = page.Title
	projection.Summary = page.Summary
	projection.Content = page.RenderedContent

	knowledgeIDs := make([]string, 0, len(normalized.KnowledgeRevisions)+len(normalized.Sources))
	newRevisionByKnowledge := make(map[string]string, len(normalized.KnowledgeRevisions))
	newRevisionByAlias := make(map[string]string, len(normalized.KnowledgeRevisions))
	newRevisionIDs := make(map[string]struct{}, len(normalized.KnowledgeRevisions))
	for i := range normalized.KnowledgeRevisions {
		revision := &normalized.KnowledgeRevisions[i]
		if revision.KnowledgeID == "" {
			return nil, nil, errors.New("new knowledge revision requires knowledge_id")
		}
		if _, duplicate := newRevisionByKnowledge[revision.KnowledgeID]; duplicate {
			return nil, nil, fmt.Errorf("more than one new revision for knowledge %s", revision.KnowledgeID)
		}
		if err := checkTenantScope("knowledge revision tenant_id", revision.TenantID, normalized.TenantID); err != nil {
			return nil, nil, err
		}
		if err := checkStringScope("knowledge revision knowledge_base_id", revision.KnowledgeBaseID, normalized.KnowledgeBaseID); err != nil {
			return nil, nil, err
		}
		aliasID := revision.ID
		if aliasID != "" {
			if _, duplicate := newRevisionByAlias[aliasID]; duplicate {
				return nil, nil, fmt.Errorf("duplicate knowledge revision alias %s", aliasID)
			}
		}
		revision.ID = uuid.NewString()
		newRevisionByAlias[aliasID] = revision.ID
		newRevisionIDs[revision.ID] = struct{}{}
		newRevisionByKnowledge[revision.KnowledgeID] = revision.ID
		revision.TenantID = normalized.TenantID
		revision.KnowledgeBaseID = normalized.KnowledgeBaseID
		revision.RevisionNo = 0
		revision.Status = types.KnowledgeRevisionStaged
		revision.CreatedAt = time.Time{}
		revision.PublishedAt = nil
		revision.SupersededAt = nil
		revision.DeletedAt = (types.KnowledgeRevision{}).DeletedAt
		knowledgeIDs = append(knowledgeIDs, revision.KnowledgeID)
	}

	blockIDs := make(map[string]string, len(normalized.Blocks))
	blockByAlias := make(map[string]string, len(normalized.Blocks))
	logicalIDs := make(map[string]struct{}, len(normalized.Blocks))
	for i := range normalized.Blocks {
		block := &normalized.Blocks[i]
		if err := checkTenantScope("wiki block tenant_id", block.TenantID, normalized.TenantID); err != nil {
			return nil, nil, err
		}
		if err := checkStringScope("wiki block knowledge_base_id", block.KnowledgeBaseID, normalized.KnowledgeBaseID); err != nil {
			return nil, nil, err
		}
		if err := checkStringScope("wiki block page_id", block.PageID, normalized.PageID); err != nil {
			return nil, nil, err
		}
		aliasID := block.ID
		if aliasID != "" {
			if _, duplicate := blockByAlias[aliasID]; duplicate {
				return nil, nil, fmt.Errorf("duplicate wiki block alias %s", aliasID)
			}
		}
		if block.LogicalBlockID == "" {
			block.LogicalBlockID = deterministicLogicalBlockID(*block)
		}
		if _, duplicate := logicalIDs[block.LogicalBlockID]; duplicate {
			return nil, nil, fmt.Errorf("duplicate logical block id %s", block.LogicalBlockID)
		}
		logicalIDs[block.LogicalBlockID] = struct{}{}
		block.ID = uuid.NewString()
		if aliasID != "" {
			blockByAlias[aliasID] = block.ID
		}
		blockIDs[block.ID] = block.LogicalBlockID
		block.TenantID = normalized.TenantID
		block.KnowledgeBaseID = normalized.KnowledgeBaseID
		block.PageID = normalized.PageID
		block.PageRevisionID = page.ID
		block.CreatedAt = time.Time{}
		block.UpdatedAt = time.Time{}
		if block.AuthorType == "" {
			block.AuthorType = types.WikiBlockAuthorGenerated
		}
		if block.ProvenanceStatus == "" {
			block.ProvenanceStatus = types.WikiProvenancePartial
		}
		if block.ContentHash == "" {
			block.ContentHash = sha256Hex(block.Content)
		}
		if err := block.Validate(); err != nil {
			return nil, nil, fmt.Errorf("validate wiki block: %w", err)
		}
	}
	for i := range normalized.Blocks {
		block := &normalized.Blocks[i]
		if block.ParentBlockID != nil {
			parentID, ok := blockByAlias[*block.ParentBlockID]
			if !ok {
				return nil, nil, fmt.Errorf("parent block %s is not part of this page revision", *block.ParentBlockID)
			}
			block.ParentBlockID = &parentID
		}
	}

	evidenceKeys := make(map[string]struct{}, len(normalized.Sources))
	for i := range normalized.Sources {
		source := &normalized.Sources[i]
		blockID, ok := blockByAlias[source.BlockID]
		if !ok {
			return nil, nil, fmt.Errorf("source block %s is not part of this page revision", source.BlockID)
		}
		source.BlockID = blockID
		if source.KnowledgeID == "" {
			return nil, nil, errors.New("wiki block source requires knowledge_id")
		}
		if source.KnowledgeRevisionID == "" {
			source.KnowledgeRevisionID = newRevisionByKnowledge[source.KnowledgeID]
		} else if persistedID, ok := newRevisionByAlias[source.KnowledgeRevisionID]; ok {
			source.KnowledgeRevisionID = persistedID
		}
		if source.KnowledgeRevisionID == "" {
			return nil, nil, errors.New("wiki block source requires knowledge_revision_id")
		}
		if err := checkTenantScope("wiki source tenant_id", source.TenantID, normalized.TenantID); err != nil {
			return nil, nil, err
		}
		if err := checkStringScope("wiki source knowledge_base_id", source.KnowledgeBaseID, normalized.KnowledgeBaseID); err != nil {
			return nil, nil, err
		}
		if err := checkStringScope("wiki source page_id", source.PageID, normalized.PageID); err != nil {
			return nil, nil, err
		}
		source.ID = uuid.NewString()
		source.TenantID = normalized.TenantID
		source.KnowledgeBaseID = normalized.KnowledgeBaseID
		source.PageID = normalized.PageID
		source.CreatedAt = time.Time{}
		if source.ChunkID == nil && source.SourceStart == 0 && source.SourceEnd == 0 {
			source.SourceStart = -1
			source.SourceEnd = -1
		}
		if source.SourceRole == "" {
			source.SourceRole = types.WikiSourceSupporting
		}
		if source.ValidationStatus == "" {
			source.ValidationStatus = types.WikiSourceValidationPending
		}
		if err := source.Validate(); err != nil {
			return nil, nil, fmt.Errorf("validate wiki block source: %w", err)
		}
		chunkID := ""
		if source.ChunkID != nil {
			chunkID = *source.ChunkID
		}
		evidenceKey := fmt.Sprintf(
			"%s|%s|%s|%d|%d",
			source.BlockID, source.KnowledgeRevisionID, chunkID, source.SourceStart, source.SourceEnd,
		)
		if _, duplicate := evidenceKeys[evidenceKey]; duplicate {
			return nil, nil, errors.New("duplicate block source evidence")
		}
		evidenceKeys[evidenceKey] = struct{}{}
		knowledgeIDs = append(knowledgeIDs, source.KnowledgeID)
	}

	page.PublishFingerprint = wikiPublishFingerprint(normalized, blockIDs, newRevisionIDs)
	return normalized, uniqueSorted(knowledgeIDs), nil
}

func checkTenantScope(name string, actual, expected uint64) error {
	if actual != 0 && actual != expected {
		return fmt.Errorf("%s is outside the publish tenant", name)
	}
	return nil
}

func checkStringScope(name, actual, expected string) error {
	if actual != "" && actual != expected {
		return fmt.Errorf("%s is outside the publish scope", name)
	}
	return nil
}

func deterministicLogicalBlockID(block types.WikiPageBlock) string {
	value := fmt.Sprintf("%d\x00%s\x00%s", block.SortOrder, block.BlockType, block.Content)
	return "block:" + sha256Hex(value)[:32]
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func wikiPublishFingerprint(
	request *types.WikiProvenancePublishRequest,
	blockLogicalIDs map[string]string,
	newRevisionIDs map[string]struct{},
) string {
	type fingerprintKnowledge struct {
		KnowledgeID string
		ContentHash string
	}
	type fingerprintBlock struct {
		LogicalID string
		ParentID  string
		Type      types.WikiBlockType
		Order     int
		Content   string
		Author    types.WikiBlockAuthorType
	}
	type fingerprintSource struct {
		Block       string
		KnowledgeID string
		Revision    string
		Chunk       string
		Start       int
		End         int
		Evidence    string
		Role        types.WikiSourceRole
		Validation  types.WikiSourceValidationStatus
	}
	payload := struct {
		Title      string
		Summary    string
		Content    string
		Provenance types.WikiProvenanceStatus
		Slug       string
		PageType   string
		Aliases    types.StringArray
		ParentSlug string
		FolderID   string
		SourceRefs types.StringArray
		ChunkRefs  types.StringArray
		Metadata   types.JSON
		Knowledge  []fingerprintKnowledge
		Blocks     []fingerprintBlock
		Sources    []fingerprintSource
	}{
		Title:      request.PageRevision.Title,
		Summary:    request.PageRevision.Summary,
		Content:    request.PageRevision.RenderedContent,
		Provenance: request.PageRevision.ProvenanceStatus,
		Slug:       request.PageProjection.Slug,
		PageType:   request.PageProjection.PageType,
		Aliases:    request.PageProjection.Aliases,
		ParentSlug: request.PageProjection.ParentSlug,
		FolderID:   request.PageProjection.FolderID,
		SourceRefs: request.PageProjection.SourceRefs,
		ChunkRefs:  request.PageProjection.ChunkRefs,
		Metadata:   request.PageProjection.PageMetadata,
	}
	for _, revision := range request.KnowledgeRevisions {
		payload.Knowledge = append(payload.Knowledge, fingerprintKnowledge{
			KnowledgeID: revision.KnowledgeID,
			ContentHash: revision.ContentHash,
		})
	}
	for _, block := range request.Blocks {
		parent := ""
		if block.ParentBlockID != nil {
			parent = blockLogicalIDs[*block.ParentBlockID]
		}
		payload.Blocks = append(payload.Blocks, fingerprintBlock{
			LogicalID: block.LogicalBlockID,
			ParentID:  parent,
			Type:      block.BlockType,
			Order:     block.SortOrder,
			Content:   block.Content,
			Author:    block.AuthorType,
		})
	}
	newRevisionKnowledge := make(map[string]string, len(request.KnowledgeRevisions))
	for _, revision := range request.KnowledgeRevisions {
		newRevisionKnowledge[revision.ID] = revision.KnowledgeID + ":" + revision.ContentHash
	}
	for _, source := range request.Sources {
		chunkID := ""
		if source.ChunkID != nil {
			chunkID = *source.ChunkID
		}
		revisionIdentity := source.KnowledgeRevisionID
		if _, ok := newRevisionIDs[source.KnowledgeRevisionID]; ok {
			revisionIdentity = "new:" + newRevisionKnowledge[source.KnowledgeRevisionID]
		}
		payload.Sources = append(payload.Sources, fingerprintSource{
			Block:       blockLogicalIDs[source.BlockID],
			KnowledgeID: source.KnowledgeID,
			Revision:    revisionIdentity,
			Chunk:       chunkID,
			Start:       source.SourceStart,
			End:         source.SourceEnd,
			Evidence:    source.EvidenceHash,
			Role:        source.SourceRole,
			Validation:  source.ValidationStatus,
		})
	}
	sort.Slice(payload.Knowledge, func(i, j int) bool {
		return payload.Knowledge[i].KnowledgeID < payload.Knowledge[j].KnowledgeID
	})
	sort.Slice(payload.Blocks, func(i, j int) bool {
		if payload.Blocks[i].Order != payload.Blocks[j].Order {
			return payload.Blocks[i].Order < payload.Blocks[j].Order
		}
		return payload.Blocks[i].LogicalID < payload.Blocks[j].LogicalID
	})
	sort.Slice(payload.Sources, func(i, j int) bool {
		left, _ := json.Marshal(payload.Sources[i])
		right, _ := json.Marshal(payload.Sources[j])
		return string(left) < string(right)
	})
	encoded, _ := json.Marshal(payload)
	return sha256Hex(string(encoded))
}

func buildWikiPageSourceProjection(
	request *types.WikiProvenancePublishRequest,
) []types.WikiPageSource {
	type aggregate struct {
		blocks     map[string]struct{}
		revisionID string
		status     types.WikiSourceValidationStatus
	}
	aggregates := make(map[string]*aggregate)
	for _, source := range request.Sources {
		entry := aggregates[source.KnowledgeID]
		if entry == nil {
			entry = &aggregate{blocks: make(map[string]struct{}), status: types.WikiSourceValidationVerified}
			aggregates[source.KnowledgeID] = entry
		}
		if source.SourceRole == types.WikiSourceSupporting &&
			source.ValidationStatus != types.WikiSourceValidationInvalid {
			entry.blocks[source.BlockID] = struct{}{}
		}
		entry.revisionID = source.KnowledgeRevisionID
		entry.status = mergeWikiSourceValidation(entry.status, source.ValidationStatus)
	}
	knowledgeIDs := make([]string, 0, len(aggregates))
	for knowledgeID := range aggregates {
		knowledgeIDs = append(knowledgeIDs, knowledgeID)
	}
	sort.Strings(knowledgeIDs)
	result := make([]types.WikiPageSource, 0, len(knowledgeIDs))
	for _, knowledgeID := range knowledgeIDs {
		entry := aggregates[knowledgeID]
		revisionID := entry.revisionID
		result = append(result, types.WikiPageSource{
			TenantID:                request.TenantID,
			KnowledgeBaseID:         request.KnowledgeBaseID,
			PageID:                  request.PageID,
			KnowledgeID:             knowledgeID,
			SupportedBlockCount:     len(entry.blocks),
			LastKnowledgeRevisionID: &revisionID,
			MappingGranularity:      types.WikiSourceMappingBlock,
			ValidationStatus:        entry.status,
		})
	}
	return result
}

func mergeWikiSourceValidation(
	current, next types.WikiSourceValidationStatus,
) types.WikiSourceValidationStatus {
	if current == types.WikiSourceValidationInvalid || next == types.WikiSourceValidationInvalid {
		return types.WikiSourceValidationInvalid
	}
	if current == types.WikiSourceValidationPending || next == types.WikiSourceValidationPending {
		return types.WikiSourceValidationPending
	}
	if current == types.WikiSourceValidationLegacyInferred ||
		next == types.WikiSourceValidationLegacyInferred {
		return types.WikiSourceValidationLegacyInferred
	}
	return types.WikiSourceValidationVerified
}

var _ interfaces.WikiProvenancePublishService = (*wikiProvenancePublishService)(nil)
