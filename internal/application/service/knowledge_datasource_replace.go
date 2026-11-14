package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// knowledgeReplacement is local to a single creation pipeline; a successful
// persist switches the live rows before that pipeline can enqueue a worker.
type knowledgeReplacement struct {
	previous  *types.Knowledge
	metadata  map[string]string
	tagIDs    []string
	committed bool
	keepFile  bool // a failed commit/read may still have published the new path
}

func (s *knowledgeService) checkKnowledgeForCreation(ctx context.Context, tenantID uint64, kbID string,
	replacement *knowledgeReplacement, params *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	if replacement != nil {
		params.ExcludeKnowledgeID = replacement.previous.ID
	}
	return s.repo.CheckKnowledgeExists(ctx, tenantID, kbID, params)
}

func (s *knowledgeService) persistKnowledgeForCreation(ctx context.Context, knowledge *types.Knowledge,
	replacement *knowledgeReplacement,
) error {
	if replacement == nil {
		return s.repo.CreateKnowledge(ctx, knowledge)
	}
	// URL creation normally has no datasource metadata. Include it in the same
	// INSERT so a committed replacement is immediately discoverable on retry.
	metadata, err := mergeKnowledgeMetadata(knowledge.Metadata, replacement.metadata)
	if err != nil {
		return err
	}
	knowledge.Metadata = metadata
	if err := s.repo.ReplaceKnowledge(ctx, replacement.previous, knowledge, replacement.tagIDs); err != nil {
		// A commit can succeed while its acknowledgement is lost. Only discard
		// the staged file when a read confirms the previous row is still live.
		cleanupCtx := context.WithoutCancel(ctx)
		old, oldErr := s.repo.GetKnowledgeByID(cleanupCtx, replacement.previous.TenantID, replacement.previous.ID)
		if oldErr == nil && old != nil {
			return err
		}
		current, readErr := s.repo.GetKnowledgeByID(cleanupCtx, knowledge.TenantID, knowledge.ID)
		if readErr != nil || current == nil {
			replacement.keepFile = true
			return err
		}
		logger.Warnf(ctx, "Knowledge replacement %s committed despite reported error: %v", knowledge.ID, err)
	}
	replacement.committed = true
	return nil
}

// ReplaceKnowledgeFromDataSource preserves the previous row and its resources
// until validation, file storage and the replacement INSERT succeed. The DB
// transaction covers both knowledge rows and new tag relations; parsing remains asynchronous
// and cleanup of the retired row's external resources is best-effort after commit.
func (s *knowledgeService) ReplaceKnowledgeFromDataSource(ctx context.Context, knowledgeID string,
	item *types.FetchedItem, metadata map[string]string, tagIDs []string, channel string,
) (*types.Knowledge, error) {
	if item == nil || (len(item.Content) == 0 && item.URL == "") {
		return nil, fmt.Errorf("item has neither content nor URL")
	}
	plan, err := s.planKnowledgeDelete(ctx, []string{knowledgeID})
	if err != nil {
		return nil, err
	}
	previous := plan.knowledge[0]
	stored := previous.GetMetadata()
	if metadata["datasource_id"] == "" || metadata["external_id"] == "" ||
		stored["datasource_id"] != metadata["datasource_id"] || stored["external_id"] != metadata["external_id"] {
		return nil, fmt.Errorf("datasource knowledge identity changed")
	}
	if err := s.validateKnowledgeTagIDs(plan.ctx, previous.TenantID, previous.KnowledgeBaseID, tagIDs); err != nil {
		return nil, err
	}
	replacement := &knowledgeReplacement{previous: previous, metadata: metadata, tagIDs: tagIDs}
	var created *types.Knowledge
	if len(item.Content) > 0 {
		file, buildErr := bytesToFileHeader(item.Content, item.FileName)
		if buildErr != nil {
			return nil, fmt.Errorf("build file header: %w", buildErr)
		}
		if err := ValidateJSONUploadContent(item.FileName, file); err != nil {
			return nil, err
		}
		created, err = s.createKnowledgeFromFile(plan.ctx, previous.KnowledgeBaseID, file, metadata,
			nil, item.FileName, tagIDs, channel, nil, replacement)
	} else {
		created, err = s.createKnowledgeFromURL(plan.ctx, previous.KnowledgeBaseID, item.URL, item.FileName,
			"", nil, item.Title, tagIDs, channel, nil, replacement)
	}
	if !replacement.committed {
		return created, err
	}

	// Never run the delete cascade before the INSERT commits: rollback cannot
	// restore an object-store file, vector index, graph or wiki contribution.
	// The old row is already soft-deleted, so do not mark/save it again (which
	// could resurrect it). Keep the tombstone if external cleanup fails.
	plan.rowsDeleted = true
	plan.ctx = context.WithoutCancel(plan.ctx)
	if cleanupErr := s.executeKnowledgeDelete(plan, true); cleanupErr != nil {
		logger.Warnf(plan.ctx, "Replacement committed; failed to clean retired knowledge %s: %v",
			knowledgeID, cleanupErr)
	} else if cleanupErr := s.repo.HardDeleteKnowledge(plan.ctx, previous.TenantID, knowledgeID); cleanupErr != nil {
		logger.Warnf(plan.ctx, "Replacement committed; failed to remove retired knowledge %s: %v",
			knowledgeID, cleanupErr)
	}
	return created, err
}

// Replacement tags were inserted with the row transaction. Normal uploads keep
// their existing validation/persistence behavior.
func (s *knowledgeService) attachCreationTags(ctx context.Context, knowledge *types.Knowledge,
	tagIDs []string, replacement *knowledgeReplacement,
) error {
	if replacement == nil {
		return s.setAndAttachKnowledgeTags(ctx, knowledge.TenantID, knowledge.KnowledgeBaseID, knowledge, tagIDs)
	}
	s.attachTagsToKnowledge(ctx, knowledge)
	return nil
}

// Retirement used to free this storage before creation checked quota. Preserve
// that allowance without changing the shared tenant snapshot before commit.
func knowledgeCreationStorageUsed(tenant *types.Tenant, replacement *knowledgeReplacement) int64 {
	used := tenant.StorageUsed
	if replacement != nil {
		used -= replacement.previous.StorageSize
	}
	return used
}
