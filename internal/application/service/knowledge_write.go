package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type knowledgeBaseWriteLookup interface {
	GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error)
}

func writeResourceIDs(ids []string) ([]string, error) {
	result := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, apperrors.NewBadRequestError("resource ID cannot be empty")
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, nil
}

func writeExecutionTenant(ctx context.Context) (uint64, error) {
	tenant, ok := types.TenantIDFromContext(ctx)
	if !ok || tenant == 0 {
		return 0, apperrors.NewUnauthorizedError("workspace context unavailable")
	}
	return tenant, nil
}

// Resolve persisted bindings before consuming grants. An input object's KB or
// tenant must never select the scope used to authorize an existing resource.
func knowledgeWriteKB(
	ctx context.Context,
	lookup knowledgeBaseWriteLookup,
	knowledge *types.Knowledge,
) (*types.KnowledgeBase, error) {
	if knowledge == nil || knowledge.ID == "" || knowledge.KnowledgeBaseID == "" || knowledge.TenantID == 0 {
		return nil, apperrors.NewNotFoundError("knowledge not found")
	}
	kb, err := lookup.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if kb == nil || kb.ID != knowledge.KnowledgeBaseID || kb.TenantID != knowledge.TenantID {
		return nil, apperrors.NewForbiddenError("knowledge does not belong to its knowledge base")
	}
	return kb, nil
}

func loadKnowledgeWrite(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	lookup knowledgeBaseWriteLookup,
	id string,
) (*types.Knowledge, *types.KnowledgeBase, error) {
	tenant, err := writeExecutionTenant(ctx)
	if err != nil {
		return nil, nil, err
	}
	knowledge, err := repo.GetKnowledgeByID(ctx, tenant, id)
	if err != nil {
		return nil, nil, err
	}
	if knowledge == nil || knowledge.ID != id || knowledge.TenantID != tenant {
		return nil, nil, apperrors.NewNotFoundError("knowledge not found")
	}
	if err := access.RejectMovingKnowledge(knowledge); err != nil {
		return nil, nil, err
	}
	kb, err := knowledgeWriteKB(ctx, lookup, knowledge)
	if err != nil {
		return nil, nil, err
	}
	if _, err := requireKBWrite(ctx, kb); err != nil {
		return nil, nil, err
	}
	copyOfKnowledge := *knowledge
	return &copyOfKnowledge, kb, nil
}

// loadKnowledgeWriteBatch validates every requested ID, including missing
// entries, before any caller can apply changes. Each KB needs its own grant.
func loadKnowledgeWriteBatch(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	lookup knowledgeBaseWriteLookup,
	ids []string,
) ([]*types.Knowledge, error) {
	ids, err := writeResourceIDs(ids)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	tenant, err := writeExecutionTenant(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := repo.GetKnowledgeBatch(ctx, tenant, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*types.Knowledge, len(rows))
	for _, row := range rows {
		if row != nil && row.TenantID == tenant {
			byID[row.ID] = row
		}
	}
	checked := make(map[string]bool)
	result := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		row := byID[id]
		if row == nil {
			return nil, apperrors.NewNotFoundError("knowledge not found")
		}
		if err := access.RejectMovingKnowledge(row); err != nil {
			return nil, err
		}
		if !checked[row.KnowledgeBaseID] {
			kb, err := knowledgeWriteKB(ctx, lookup, row)
			if err != nil {
				return nil, err
			}
			if _, err := requireKBWrite(ctx, kb); err != nil {
				return nil, err
			}
			checked[row.KnowledgeBaseID] = true
		}
		copyOfRow := *row
		result = append(result, &copyOfRow)
	}
	return result, nil
}
