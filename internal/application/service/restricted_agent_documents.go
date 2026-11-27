package service

import (
	"context"
	"fmt"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// Match the explicit document selection bound used by MCP search (#4463).
const maxRestrictedAgentDocuments = 100

type scopedAgentDocumentLookup interface {
	GetKnowledgeBatchWithSharedAccess(context.Context, uint64, []string) ([]*types.Knowledge, error)
}

type scopedAgentBaseLookup interface {
	GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error)
}

func buildRestrictedAgentDocumentTargets(
	ctx context.Context, knowledge scopedAgentDocumentLookup, bases scopedAgentBaseLookup,
	tenantID uint64, ids []string,
) (types.SearchTargets, error) {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || scope.KeyID == 0 || !scope.IsKnowledgeBaseRestricted() ||
		tenantID == 0 || types.CallerFromContext(ctx).TenantID != tenantID {
		return nil, apperrors.NewForbiddenError("Document scope requires a restricted workspace API key")
	}
	if len(ids) == 0 {
		return nil, apperrors.NewBadRequestError("knowledge_ids must contain at least one document")
	}
	if len(ids) > maxRestrictedAgentDocuments {
		return nil, apperrors.NewBadRequestError(fmt.Sprintf(
			"knowledge_ids must contain at most %d document ids", maxRestrictedAgentDocuments))
	}

	requested := make(map[string]*types.Knowledge, len(ids))
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || strings.TrimSpace(id) != id {
			return nil, apperrors.NewBadRequestError("Invalid knowledge_ids")
		}
		if _, exists := requested[id]; !exists {
			requested[id] = nil
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	docs, err := knowledge.GetKnowledgeBatchWithSharedAccess(ctx, tenantID, uniqueIDs)
	if err != nil {
		return nil, fmt.Errorf("load Agent documents: %w", err)
	}
	for _, doc := range docs {
		if doc == nil || doc.TenantID == 0 || !scope.AllowsKnowledgeBase(doc.KnowledgeBaseID) ||
			doc.EnableStatus != "enabled" || doc.ParseStatus != types.ParseStatusCompleted {
			continue
		}
		if _, exists := requested[doc.ID]; exists {
			requested[doc.ID] = doc
		}
	}

	// A partial result must never be used to start a conversation or widen its scope.
	var targets types.SearchTargets
	byKB := make(map[string]*types.SearchTarget)
	for _, id := range uniqueIDs {
		doc := requested[id]
		if doc == nil {
			return nil, apperrors.NewForbiddenError("One or more requested documents are outside the readable scope")
		}
		target := byKB[doc.KnowledgeBaseID]
		if target == nil {
			target = &types.SearchTarget{
				Type:                    types.SearchTargetTypeKnowledge,
				KnowledgeBaseID:         doc.KnowledgeBaseID,
				TenantID:                doc.TenantID,
				DisableRecallThresholds: true,
			}
			byKB[doc.KnowledgeBaseID] = target
			targets = append(targets, target)
		} else if target.TenantID != doc.TenantID {
			return nil, apperrors.NewForbiddenError("One or more requested documents are outside the readable scope")
		}
		target.KnowledgeIDs = append(target.KnowledgeIDs, id)
	}
	baseIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		baseIDs = append(baseIDs, target.KnowledgeBaseID)
	}
	rows, err := bases.GetKnowledgeBasesByIDsOnly(ctx, baseIDs)
	if err != nil {
		return nil, fmt.Errorf("load Agent document libraries: %w", err)
	}
	owners := make(map[string]uint64, len(rows))
	for _, row := range rows {
		owners[row.ID] = row.TenantID
	}
	for _, target := range targets {
		if owners[target.KnowledgeBaseID] != target.TenantID {
			return nil, apperrors.NewForbiddenError("One or more requested documents are outside the readable scope")
		}
	}
	return targets, nil
}

func (s *sessionService) AuthorizeRestrictedAgentDocuments(ctx context.Context, tenantID uint64, ids []string) error {
	_, err := buildRestrictedAgentDocumentTargets(ctx, s.knowledgeService, s.knowledgeBaseService, tenantID, ids)
	return err
}
