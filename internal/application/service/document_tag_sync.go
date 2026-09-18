package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// DocumentTagSyncService projects committed relations. Ready means bootstrap
// finished; it deliberately remains true when an incremental update fails.
type DocumentTagSyncService struct {
	db      *gorm.DB
	task    interfaces.TaskEnqueuer
	redis   *redis.Client
	active  sync.Map
	resolve func(context.Context, *types.KnowledgeBase) (interfaces.DocumentTagProjection, error)
}

func NewDocumentTagSyncService(db *gorm.DB, registry interfaces.RetrieveEngineRegistry,
	ownership retriever.TenantStoreOwnership, tenants interfaces.TenantRepository,
	task interfaces.TaskEnqueuer, redisClient *redis.Client,
) *DocumentTagSyncService {
	return &DocumentTagSyncService{
		db: db, task: task, redis: redisClient,
		resolve: func(ctx context.Context, kb *types.KnowledgeBase) (interfaces.DocumentTagProjection, error) {
			tenant, err := tenants.GetTenantByID(ctx, kb.TenantID)
			if err != nil {
				return nil, err
			}
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
			return retriever.CreateRetrieveEngineForKB(ctx, registry, ownership, kb.TenantID, kb.VectorStoreID)
		},
	}
}

func (s *DocumentTagSyncService) Sync(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return fmt.Errorf("document tag synchronization requires tenant scope")
	}
	byKB := make(map[string][]string)
	for start := 0; start < len(ids); start += 100 {
		var docs []types.Knowledge
		if err := s.db.WithContext(ctx).Unscoped().Select("id", "knowledge_base_id").
			Where("tenant_id = ? AND id IN ?", tenantID, ids[start:min(start+100, len(ids))]).Find(&docs).Error; err != nil {
			return err
		}
		for _, doc := range docs {
			byKB[doc.KnowledgeBaseID] = append(byKB[doc.KnowledgeBaseID], doc.ID)
		}
	}
	var result error
	for kbID, docIDs := range byKB {
		payload := types.DocumentTagSyncPayload{TenantID: tenantID, KnowledgeBaseID: kbID, KnowledgeIDs: docIDs}
		if err := s.sync(ctx, payload); err != nil {
			if enqueueErr := repository.EnqueueDocumentTagSync(s.task, payload); enqueueErr != nil {
				logger.Errorf(ctx, "Document tags saved but repair enqueue failed for KB %s: %v", kbID, enqueueErr)
			}
			result = errors.Join(result, fmt.Errorf("document tags saved, index synchronization failed: %w", err))
		}
	}
	return result
}

func (s *DocumentTagSyncService) loadKB(ctx context.Context, payload types.DocumentTagSyncPayload) (*types.KnowledgeBase, error) {
	if payload.TenantID == 0 || payload.KnowledgeBaseID == "" {
		return nil, fmt.Errorf("document tag task requires tenant and knowledge base")
	}
	var kb types.KnowledgeBase
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", payload.TenantID, payload.KnowledgeBaseID).First(&kb).Error
	return &kb, err
}

func (s *DocumentTagSyncService) sync(ctx context.Context, payload types.DocumentTagSyncPayload) error {
	kb, err := s.loadKB(ctx, payload)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // Deleted/mismatched KB: never redirect a task to another tenant.
	}
	if err != nil || kb.Type == types.KnowledgeBaseTypeFAQ {
		return err
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, kb.TenantID)
	projection, err := s.resolve(ctx, kb)
	if err != nil {
		return err
	}
	if !projection.SupportsDocumentTags() {
		return nil
	}
	if len(payload.KnowledgeIDs) > 0 {
		err = s.syncPages(ctx, projection, kb.ID, payload.KnowledgeIDs)
		if !kb.DocumentTagReady {
			s.enqueueBootstrap(ctx, kb)
		}
		return err
	}
	if kb.DocumentTagReady && !payload.Force {
		return nil
	}
	// Prepare schema even when there are no documents. Existing schema creation
	// may be asynchronous; returning its error lets the task retry later.
	if err := projection.SyncDocumentTags(ctx, kb.ID, nil); err != nil {
		return err
	}
	after := ""
	for {
		var ids []string
		if err := s.db.WithContext(ctx).Unscoped().Model(&types.Knowledge{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id > ?", kb.TenantID, kb.ID, after).
			Order("id").Limit(100).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		if err := projection.SyncDocumentTags(ctx, kb.ID, ids); err != nil {
			return err
		}
		after = ids[len(ids)-1]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Guard against a binding changing while the task was running. Explicit
	// storage/index replacement must reset readiness as part of maintenance.
	q := s.db.WithContext(ctx).Model(&types.KnowledgeBase{}).Where("id = ? AND tenant_id = ?", kb.ID, kb.TenantID)
	if kb.VectorStoreID == nil {
		q = q.Where("vector_store_id IS NULL")
	} else {
		q = q.Where("vector_store_id = ?", *kb.VectorStoreID)
	}
	return q.UpdateColumn("document_tag_ready", true).Error
}

func (s *DocumentTagSyncService) syncPages(ctx context.Context, projection interfaces.DocumentTagProjection, kbID string, ids []string) error {
	for start := 0; start < len(ids); start += 100 {
		if err := projection.SyncDocumentTags(ctx, kbID, ids[start:min(start+100, len(ids))]); err != nil {
			return err
		}
	}
	return nil
}

func (s *DocumentTagSyncService) enqueueBootstrap(ctx context.Context, kb *types.KnowledgeBase) {
	if err := repository.EnqueueDocumentTagSync(s.task, types.DocumentTagSyncPayload{TenantID: kb.TenantID, KnowledgeBaseID: kb.ID}); err != nil {
		logger.Warnf(ctx, "Document tag bootstrap enqueue failed for %s: %v", kb.ID, err)
	}
}

func (s *DocumentTagSyncService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.DocumentTagSyncPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: invalid document tag task: %v", asynq.SkipRetry, err)
	}
	if payload.TenantID == 0 || payload.KnowledgeBaseID == "" {
		return fmt.Errorf("%w: missing document tag task scope", asynq.SkipRetry)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if len(payload.KnowledgeIDs) > 0 {
		return s.sync(ctx, payload)
	}
	key := fmt.Sprintf("document-tags:bootstrap:%d:%s", payload.TenantID, payload.KnowledgeBaseID)
	if s.redis != nil {
		return redislock.WithRenewableLock(ctx, s.redis, key, time.Minute, 20*time.Second,
			func(ctx context.Context) error { return s.sync(ctx, payload) })
	}
	// Lite runs in one process. A later recovery sweep retries a failed owner.
	if _, loaded := s.active.LoadOrStore(key, struct{}{}); loaded {
		return nil
	}
	defer s.active.Delete(key)
	return s.sync(ctx, payload)
}

// RecoverPending repairs lost bootstrap triggers, including read-only legacy
// KBs and Lite restarts. Ready KBs are not a live consistency-monitoring queue.
func (s *DocumentTagSyncService) RecoverPending(ctx context.Context) error {
	after := ""
	for {
		var kbs []types.KnowledgeBase
		if err := s.db.WithContext(ctx).Where("type = ? AND document_tag_ready = ? AND id > ?", types.KnowledgeBaseTypeDocument, false, after).
			Order("id").Limit(100).Find(&kbs).Error; err != nil {
			return err
		}
		if len(kbs) == 0 {
			return nil
		}
		for _, kb := range kbs {
			projection, err := s.resolve(context.WithValue(ctx, types.TenantIDContextKey, kb.TenantID), &kb)
			if err != nil {
				logger.Warnf(ctx, "Document tag recovery cannot resolve KB %s: %v", kb.ID, err)
				continue
			}
			if projection.SupportsDocumentTags() {
				s.enqueueBootstrap(ctx, &kb)
			}
		}
		after = kbs[len(kbs)-1].ID
	}
}
