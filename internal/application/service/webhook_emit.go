package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/workspaceevent"
)

var globalWebhookSink interfaces.WorkspaceEventSink

func setWebhookSink(s interfaces.WorkspaceEventSink) {
	if s != nil {
		globalWebhookSink = s
	}
}

func emitParseCompletedFromRepo(ctx context.Context, repo interfaces.KnowledgeRepository, knowledgeID string) {
	if globalWebhookSink == nil || repo == nil || knowledgeID == "" {
		return
	}
	k, err := repo.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil || k == nil {
		logger.Warnf(ctx, "webhook parse_completed load knowledge=%s err=%v", knowledgeID, err)
		return
	}
	if k.ParseStatus != types.ParseStatusCompleted {
		return
	}
	globalWebhookSink.Emit(ctx, types.WorkspaceEvent{
		Type:     types.EventKnowledgeParseCompleted,
		TenantID: k.TenantID,
		Data:     workspaceevent.KnowledgeDataFrom(k, false),
	})
}

func emitParseFailedFromRepo(ctx context.Context, repo interfaces.KnowledgeRepository, knowledgeID string) {
	if globalWebhookSink == nil || repo == nil || knowledgeID == "" {
		return
	}
	k, err := repo.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil || k == nil {
		return
	}
	globalWebhookSink.Emit(ctx, types.WorkspaceEvent{
		Type:     types.EventKnowledgeParseFailed,
		TenantID: k.TenantID,
		Data:     workspaceevent.KnowledgeDataFrom(k, false),
	})
}

func NotifyWebhookParseFailed(ctx context.Context, repo interfaces.KnowledgeRepository, knowledgeID string) {
	emitParseFailedFromRepo(ctx, repo, knowledgeID)
}

func (s *knowledgeService) emit(ctx context.Context, ev types.WorkspaceEvent) {
	if s == nil || s.events == nil {
		return
	}
	if ev.ActorUserID == "" {
		if uid, ok := types.UserIDFromContext(ctx); ok {
			ev.ActorUserID = uid
		}
	}
	if ev.RequestID == "" {
		if rid, ok := types.RequestIDFromContext(ctx); ok {
			ev.RequestID = rid
		}
	}
	s.events.Emit(ctx, ev)
}

func (s *knowledgeService) skipTemporaryKB(ctx context.Context, kbID string) bool {
	if kbID == "" || s.kbService == nil {
		return false
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb == nil {
		return false
	}
	return kb.IsTemporary
}

func (s *knowledgeService) emitKnowledgeCreated(ctx context.Context, k *types.Knowledge) {
	if k == nil || strings.EqualFold(k.Type, types.KnowledgeTypeFAQ) {
		return
	}
	if s.skipTemporaryKB(ctx, k.KnowledgeBaseID) {
		return
	}
	data := workspaceevent.KnowledgeDataFrom(k, false)
	s.emit(ctx, types.WorkspaceEvent{
		Type:     types.EventKnowledgeCreated,
		TenantID: k.TenantID,
		Data:     data,
	})
}

func (s *knowledgeService) emitKnowledgeDeleted(ctx context.Context, k *types.Knowledge) {
	if k == nil {
		return
	}
	if s.skipTemporaryKB(ctx, k.KnowledgeBaseID) {
		return
	}
	data := workspaceevent.KnowledgeDataFrom(k, true)
	data.ParseStatus = types.ParseStatusDeleting
	s.emit(ctx, types.WorkspaceEvent{
		Type:     types.EventKnowledgeDeleted,
		TenantID: k.TenantID,
		Data:     data,
	})
}

func (s *knowledgeService) emitKnowledgeListDeleted(ctx context.Context, list []*types.Knowledge) {
	if len(list) == 0 {
		return
	}
	if len(list) == 1 {
		s.emitKnowledgeDeleted(ctx, list[0])
		return
	}
	byKB := map[string][]*types.Knowledge{}
	for _, k := range list {
		if k == nil {
			continue
		}
		if s.skipTemporaryKB(ctx, k.KnowledgeBaseID) {
			continue
		}
		byKB[k.KnowledgeBaseID] = append(byKB[k.KnowledgeBaseID], k)
	}
	kbIDs := make([]string, 0, len(byKB))
	for kbID := range byKB {
		kbIDs = append(kbIDs, kbID)
	}
	sort.Strings(kbIDs)
	for _, kbID := range kbIDs {
		group := byKB[kbID]
		sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		tenantID := group[0].TenantID
		n := len(group)
		batchTotal := (n + types.WebhookBatchDeletedChunkSize - 1) / types.WebhookBatchDeletedChunkSize
		batchID := workspaceevent.NewDeleteBatchID()
		for i := 0; i < n; i += types.WebhookBatchDeletedChunkSize {
			end := i + types.WebhookBatchDeletedChunkSize
			if end > n {
				end = n
			}
			ids := make([]string, 0, end-i)
			for _, k := range group[i:end] {
				ids = append(ids, k.ID)
			}
			idx := i/types.WebhookBatchDeletedChunkSize + 1
			s.emit(ctx, types.WorkspaceEvent{
				Type:     types.EventKnowledgeBatchDeleted,
				TenantID: tenantID,
				Data:     workspaceevent.BatchDeletedData(tenantID, kbID, ids, n, idx, batchTotal, batchID),
			})
		}
	}
}

func (s *knowledgeService) emitParseTransition(ctx context.Context, k *types.Knowledge, prevStatus string) {
	if k == nil {
		return
	}
	if s.skipTemporaryKB(ctx, k.KnowledgeBaseID) {
		return
	}
	if prevStatus != types.ParseStatusCompleted && k.ParseStatus == types.ParseStatusCompleted {
		s.emit(ctx, types.WorkspaceEvent{
			Type:     types.EventKnowledgeParseCompleted,
			TenantID: k.TenantID,
			Data:     workspaceevent.KnowledgeDataFrom(k, false),
		})
		return
	}
	if prevStatus != types.ParseStatusFailed && k.ParseStatus == types.ParseStatusFailed {
		s.emit(ctx, types.WorkspaceEvent{
			Type:     types.EventKnowledgeParseFailed,
			TenantID: k.TenantID,
			Data:     workspaceevent.KnowledgeDataFrom(k, false),
		})
	}
}

func (s *knowledgeService) emitParseCompletedByID(ctx context.Context, knowledgeID string) {
	if s == nil || s.events == nil || knowledgeID == "" {
		return
	}
	k, err := s.repo.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil || k == nil {
		logger.Warnf(ctx, "webhook parse_completed load knowledge=%s err=%v", knowledgeID, err)
		return
	}
	if k.ParseStatus != types.ParseStatusCompleted {
		return
	}
	s.emitParseTransition(ctx, k, types.ParseStatusFinalizing)
}

func emitKBCreated(events interfaces.WorkspaceEventSink, ctx context.Context, kb *types.KnowledgeBase) {
	if events == nil || kb == nil || kb.IsTemporary {
		return
	}
	actor, _ := types.UserIDFromContext(ctx)
	req, _ := types.RequestIDFromContext(ctx)
	events.Emit(ctx, types.WorkspaceEvent{
		Type:        types.EventKBCreated,
		TenantID:    kb.TenantID,
		ActorUserID: actor,
		RequestID:   req,
		Data: types.WebhookKBData{
			Resource:        "knowledge_base",
			KnowledgeBaseID: kb.ID,
			Name:            kb.Name,
		},
	})
}

func emitKBDeleted(events interfaces.WorkspaceEventSink, ctx context.Context, kb *types.KnowledgeBase, count int64) {
	if events == nil || kb == nil {
		return
	}
	actor, _ := types.UserIDFromContext(ctx)
	req, _ := types.RequestIDFromContext(ctx)
	events.Emit(ctx, types.WorkspaceEvent{
		Type:        types.EventKBDeleted,
		TenantID:    kb.TenantID,
		ActorUserID: actor,
		RequestID:   req,
		Data: types.WebhookKBData{
			Resource:            "knowledge_base",
			KnowledgeBaseID:     kb.ID,
			Name:                kb.Name,
			CascadeKnowledge:    true,
			KnowledgeCount:      count,
			UnavailableToTenant: true,
		},
	})
}

func emitMember(events interfaces.WorkspaceEventSink, ctx context.Context, typ string, tenantID uint64, userID, role, reason, email string) {
	if events == nil || tenantID == 0 || userID == "" {
		return
	}
	actor, _ := types.UserIDFromContext(ctx)
	req, _ := types.RequestIDFromContext(ctx)
	events.Emit(ctx, types.WorkspaceEvent{
		Type:        typ,
		TenantID:    tenantID,
		ActorUserID: actor,
		RequestID:   req,
		Data: types.WebhookMemberData{
			Resource: "member",
			UserID:   userID,
			Role:     role,
			Reason:   reason,
			Email:    email,
		},
	})
}
