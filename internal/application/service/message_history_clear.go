package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Lite mode has one process and no Redis. Standard deployments share a
// renewable Redis lease so a second replica cannot index or re-enable the
// workspace while a clear is running.
var chatHistoryLocalLocks = newKeyedMutex()

func (s *messageService) withChatHistoryLock(
	ctx context.Context, tenantID uint64, fn func(context.Context) error,
) error {
	if tenantID == 0 {
		return apperrors.NewBadRequestError("Workspace is empty")
	}
	key := fmt.Sprintf("weknora-chat-history-lock:%d", tenantID)
	if s.historyRedis != nil {
		return redislock.WithRenewableLock(ctx, s.historyRedis, key, 30*time.Second, 10*time.Second, fn)
	}
	release, err := chatHistoryLocalLocks.lock(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	return fn(ctx)
}

func (s *messageService) chatHistoryTenant(ctx context.Context, tenantID uint64) (*types.Tenant, error) {
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.ID != tenantID {
		return nil, apperrors.NewForbiddenError("Chat history workspace does not match")
	}
	return tenant, nil
}

// Only a server-managed, tenant-owned history KB is a valid cleanup target.
// Neither a request body nor a stale TenantInfo pointer chooses the KB.
func (s *messageService) chatHistoryKB(
	ctx context.Context, tenantID uint64, id string,
) (*types.KnowledgeBase, error) {
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if kb == nil || kb.ID != id || kb.TenantID != tenantID || !kb.IsTemporary || kb.Name != "__chat_history__" {
		return nil, apperrors.NewForbiddenError("Not a workspace chat history knowledge base")
	}
	return kb, nil
}

func (s *messageService) saveChatHistoryConfig(
	ctx context.Context, tenantID uint64, cfg *types.ChatHistoryConfig,
) error {
	// Update this column only, not the request's snapshot of every tenant
	// setting (which could overwrite an unrelated administrator's changes).
	return s.tenantService.SetChatHistoryConfig(ctx, tenantID, cfg)
}

func (s *messageService) UpdateChatHistoryConfig(
	ctx context.Context, req *types.ChatHistoryConfig,
) (*types.ChatHistoryConfig, error) {
	if req == nil {
		return nil, apperrors.NewBadRequestError("Chat history configuration is required")
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	var cfg *types.ChatHistoryConfig
	err := s.withChatHistoryLock(ctx, tenantID, func(ctx context.Context) error {
		tenant, err := s.chatHistoryTenant(ctx, tenantID)
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
		cfg = &types.ChatHistoryConfig{Enabled: req.Enabled, EmbeddingModelID: req.EmbeddingModelID}
		if old := tenant.ChatHistoryConfig; old != nil && old.KnowledgeBaseID != "" {
			kb, err := s.chatHistoryKB(ctx, tenantID, old.KnowledgeBaseID)
			if err != nil {
				return err
			}
			if old.EmbeddingModelID == cfg.EmbeddingModelID {
				cfg.KnowledgeBaseID = old.KnowledgeBaseID
			} else {
				count, err := s.knowService.GetRepository().CountAllKnowledgeByKnowledgeBaseID(ctx, tenantID, kb.ID)
				if err != nil {
					return err
				}
				if count > 0 {
					return apperrors.NewConflictError("Clear indexed messages before changing the embedding model")
				}
			}
		}
		if cfg.Enabled && cfg.EmbeddingModelID != "" && cfg.KnowledgeBaseID == "" {
			kb, err := s.kbService.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
				Name: "__chat_history__", Type: types.KnowledgeBaseTypeDocument, IsTemporary: true,
				Description:      "Auto-managed knowledge base for chat history message indexing",
				EmbeddingModelID: cfg.EmbeddingModelID,
			})
			if err != nil {
				return err
			}
			cfg.KnowledgeBaseID = kb.ID
		}
		return s.saveChatHistoryConfig(ctx, tenantID, cfg)
	})
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *messageService) ClearChatHistoryIndex(ctx context.Context) (*types.ChatHistoryKBStats, error) {
	tenantID, _ := types.TenantIDFromContext(ctx)
	var stats *types.ChatHistoryKBStats
	err := s.withChatHistoryLock(ctx, tenantID, func(ctx context.Context) error {
		tenant, err := s.chatHistoryTenant(ctx, tenantID)
		if err != nil {
			return err
		}
		if tenant.ChatHistoryConfig == nil {
			stats = &types.ChatHistoryKBStats{}
			return nil
		}
		cfg := *tenant.ChatHistoryConfig
		if cfg.KnowledgeBaseID != "" {
			if _, err := s.chatHistoryKB(ctx, tenantID, cfg.KnowledgeBaseID); err != nil {
				return err
			}
		}
		// Persist the pause before deleting anything. On failure or cancellation
		// it stays paused; a retry can finish the remaining exact KB entries.
		cfg.Enabled = false
		if err := s.saveChatHistoryConfig(ctx, tenantID, &cfg); err != nil {
			return err
		}
		copyOfTenant := *tenant
		copyOfTenant.ChatHistoryConfig = &cfg
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, &copyOfTenant)
		if cfg.KnowledgeBaseID != "" {
			repo := s.knowService.GetRepository()
			rows, err := repo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, cfg.KnowledgeBaseID)
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(rows))
			for _, row := range rows {
				if row == nil || row.TenantID != tenantID || row.KnowledgeBaseID != cfg.KnowledgeBaseID {
					return apperrors.NewForbiddenError("Chat history index binding changed")
				}
				ids = append(ids, row.ID)
			}
			const batchSize = 200
			for start := 0; start < len(ids); start += batchSize {
				end := min(start+batchSize, len(ids))
				// Reuse deletion's worker cancellation, vector/chunk cleanup and
				// retry-safe resource release. No message/session row is deleted.
				err := deleteReferencedKnowledge(ctx, s.knowService, cfg.KnowledgeBaseID, ids[start:end])
				if err != nil {
					return err
				}
			}
			kb, err := s.chatHistoryKB(ctx, tenantID, cfg.KnowledgeBaseID)
			if err != nil {
				return err
			}
			count, err := s.knowService.GetRepository().CountAllKnowledgeByKnowledgeBaseID(ctx, tenantID, kb.ID)
			if err != nil {
				return err
			}
			if count != 0 {
				return apperrors.NewConflictError("Chat history indexes remain; retry clearing")
			}
			// Never reuse this generation: a provider request already in flight
			// can finish after deletion before the worker observes its tombstone.
			// A later enable must create a fresh KB, even with the same model.
			oldKBID := cfg.KnowledgeBaseID
			cfg.KnowledgeBaseID = ""
			if err := s.saveChatHistoryConfig(ctx, tenantID, &cfg); err != nil {
				return err
			}
			// Contents have already been synchronously removed. Removing the
			// now-empty hidden KB is metadata cleanup, not the success gate.
			if err := s.kbService.DeleteKnowledgeBase(ctx, oldKBID); err != nil {
				logger.Warnf(ctx, "Failed to remove empty chat history KB %s: %v", oldKBID, err)
			}
		}
		stats, err = s.GetChatHistoryKBStats(ctx)
		if err == nil && stats.HasIndexedMessages {
			return apperrors.NewConflictError("Chat history indexes remain; retry clearing")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return stats, nil
}
