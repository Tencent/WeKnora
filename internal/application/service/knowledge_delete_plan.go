package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type knowledgeCleanupKey struct{}

// Cleanup continues a previously admitted operation on exact persisted
// references. It never grants general KB write access to a session owner.
type knowledgeCleanupScope struct {
	caller   types.Caller
	tenant   uint64
	bindings map[string]string // knowledge ID -> KB ID, copied when admitted
}

func withKnowledgeCleanup(ctx context.Context, tenant uint64, bindings map[string]string) context.Context {
	ctx = types.WithExecutionTenant(ctx, tenant)
	copyOfBindings := make(map[string]string, len(bindings))
	for id, kb := range bindings {
		copyOfBindings[id] = kb
	}
	return context.WithValue(ctx, knowledgeCleanupKey{}, knowledgeCleanupScope{
		caller: types.CallerFromContext(ctx), tenant: tenant, bindings: copyOfBindings,
	})
}

// Only call with IDs obtained from an already authorized persisted relationship
// (session messages, a temporary KB, or a clone diff), never a request body.
// expectedKB additionally pins workflows with an independently known KB.
func deleteReferencedKnowledge(
	ctx context.Context,
	svc interfaces.KnowledgeService,
	expectedKB string,
	ids []string,
) error {
	tenant, err := writeExecutionTenant(ctx)
	if err != nil {
		return err
	}
	ids, err = writeResourceIDs(ids)
	if err != nil || len(ids) == 0 {
		return err
	}
	rows, err := svc.GetRepository().GetKnowledgeBatch(ctx, tenant, ids)
	if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	bindings := make(map[string]string, len(rows))
	remaining := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || !wanted[row.ID] || row.TenantID != tenant || row.KnowledgeBaseID == "" ||
			(expectedKB != "" && row.KnowledgeBaseID != expectedKB) {
			return apperrors.NewForbiddenError("cleanup resource binding changed")
		}
		bindings[row.ID] = row.KnowledgeBaseID
		remaining = append(remaining, row.ID)
	}
	if len(remaining) == 0 {
		return nil
	}
	return svc.DeleteKnowledgeList(withKnowledgeCleanup(ctx, tenant, bindings), remaining)
}

type knowledgeDeletePlan struct {
	ctx       context.Context
	ids       []string
	knowledge []*types.Knowledge
	kbs       map[string]*types.KnowledgeBase
	files     map[string]interfaces.FileService
	imageInfo []interfaces.ChunkImageInfo
}

func (s *knowledgeService) planKnowledgeDelete(ctx context.Context, ids []string) (*knowledgeDeletePlan, error) {
	ids, err := writeResourceIDs(ids)
	if err != nil {
		return nil, err
	}
	plan := &knowledgeDeletePlan{
		ctx:   ctx,
		kbs:   make(map[string]*types.KnowledgeBase),
		files: make(map[string]interfaces.FileService),
	}
	if len(ids) == 0 {
		return plan, nil
	}
	tenant, err := writeExecutionTenant(ctx)
	if err != nil {
		return nil, err
	}
	cleanup, isCleanup := ctx.Value(knowledgeCleanupKey{}).(knowledgeCleanupScope)
	if isCleanup {
		if cleanup.tenant != tenant || cleanup.caller != types.CallerFromContext(ctx) {
			return nil, apperrors.NewForbiddenError("invalid cleanup scope")
		}
		for _, id := range ids {
			if cleanup.bindings[id] == "" {
				return nil, apperrors.NewForbiddenError("knowledge outside cleanup scope")
			}
		}
	}
	rows, err := s.repo.GetKnowledgeBatch(ctx, tenant, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*types.Knowledge, len(rows))
	for _, row := range rows {
		if row != nil && row.TenantID == tenant {
			byID[row.ID] = row
		}
	}
	for _, id := range ids {
		row := byID[id]
		if row == nil {
			if isCleanup {
				// An earlier attempt already removed this exact resource.
				continue
			}
			return nil, apperrors.NewNotFoundError("knowledge not found")
		}
		if isCleanup && cleanup.bindings[id] != row.KnowledgeBaseID {
			return nil, apperrors.NewForbiddenError("cleanup resource binding changed")
		}
		if err := access.RejectMovingKnowledge(row); err != nil {
			return nil, err
		}
		if _, ok := plan.kbs[row.KnowledgeBaseID]; !ok {
			kb, err := knowledgeWriteKB(ctx, s.kbService, row)
			if err != nil {
				return nil, err
			}
			if !isCleanup {
				if _, err := requireKBWrite(ctx, kb); err != nil {
					return nil, err
				}
			}
			plan.kbs[kb.ID] = kb
		}
		copyOfRow := *row
		plan.knowledge = append(plan.knowledge, &copyOfRow)
		plan.ids = append(plan.ids, id)
	}
	if len(plan.ids) == 0 {
		return plan, nil
	}
	// Resolve owner storage/index context and all KB routing before mutations.
	ctx, err = withKBWriteTenantInfo(ctx, plan.kbs[plan.knowledge[0].KnowledgeBaseID], s.tenantRepo)
	if err != nil {
		return nil, err
	}
	plan.ctx = ctx
	for id, kb := range plan.kbs {
		plan.files[id] = s.resolveFileService(ctx, kb)
	}
	plan.imageInfo, err = s.chunkRepo.ListImageInfoByKnowledgeIDs(ctx, tenant, plan.ids)
	if err != nil {
		return nil, err
	}
	return plan, nil
}
