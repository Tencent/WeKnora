package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// Validate the whole batch before any destructive submission. The exact KB
// grant is consumed again when ReparseKnowledge reloads each document.
func (s *knowledgeService) reparseTaskScope(
	ctx context.Context, payload types.KnowledgeListReparsePayload,
) (context.Context, []string, error) {
	if payload.TenantID == 0 {
		return ctx, nil, fmt.Errorf("invalid reparse task tenant: %w", asynq.SkipRetry)
	}
	ids, err := writeResourceIDs(payload.KnowledgeIDs)
	if err != nil {
		return ctx, nil, fmt.Errorf("invalid reparse task IDs: %w", asynq.SkipRetry)
	}
	if len(ids) == 0 {
		return ctx, ids, nil
	}
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	rows, err := s.repo.GetKnowledgeBatch(ctx, payload.TenantID, ids)
	if err != nil {
		return ctx, nil, err
	}
	kbID := payload.KnowledgeBaseID
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row == nil || row.TenantID != payload.TenantID || row.KnowledgeBaseID == "" ||
			(kbID != "" && row.KnowledgeBaseID != kbID) {
			return ctx, nil, fmt.Errorf("reparse task binding changed: %w", asynq.SkipRetry)
		}
		if err := access.RejectMovingKnowledge(row); err != nil {
			return ctx, nil, fmt.Errorf("reparse task document unavailable: %v: %w", err, asynq.SkipRetry)
		}
		// Legacy payloads can reconstruct only their current unambiguous KB.
		kbID = row.KnowledgeBaseID
		seen[row.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			return ctx, nil, fmt.Errorf("reparse task document missing: %w", asynq.SkipRetry)
		}
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return ctx, nil, err
	}
	if kb == nil || kb.ID != kbID || kb.TenantID != payload.TenantID {
		return ctx, nil, fmt.Errorf("reparse task KB binding changed: %w", asynq.SkipRetry)
	}
	ctx, err = access.WithKBTaskWrite(ctx, kb, payload.TenantID)
	return ctx, ids, err
}
