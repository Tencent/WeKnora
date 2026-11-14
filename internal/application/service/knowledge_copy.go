package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/common/redislock"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const knowledgeCopyProgressPrefix = "knowledge_copy_progress:"

var knowledgeCopyLocks = newKeyedMutex()

// SaveKnowledgeCopyProgress stores the initial state without overwriting a
// worker that has already started, including in Lite mode.
func (s *knowledgeService) SaveKnowledgeCopyProgress(ctx context.Context, progress *types.KnowledgeMoveProgress) error {
	return s.storeKnowledgeCopyProgress(ctx, *progress, true)
}

func (s *knowledgeService) storeKnowledgeCopyProgress(
	ctx context.Context, progress types.KnowledgeMoveProgress, initial bool,
) error {
	if s.redisClient == nil {
		if initial {
			s.memCopyProgress.LoadOrStore(progress.TaskID, progress)
		} else {
			s.memCopyProgress.Store(progress.TaskID, progress)
		}
		// Match Redis expiration without deleting a newer progress update.
		time.AfterFunc(knowledgeMoveProgressTTL, func() {
			s.memCopyProgress.CompareAndDelete(progress.TaskID, progress)
		})
		return nil
	}
	raw, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	key := knowledgeCopyProgressPrefix + progress.TaskID
	if initial {
		return s.redisClient.SetNX(ctx, key, raw, knowledgeMoveProgressTTL).Err()
	}
	return s.redisClient.Set(ctx, key, raw, knowledgeMoveProgressTTL).Err()
}

func (s *knowledgeService) GetKnowledgeCopyProgress(
	ctx context.Context, taskID string,
) (*types.KnowledgeMoveProgress, error) {
	if s.redisClient == nil {
		value, ok := s.memCopyProgress.Load(taskID)
		if !ok {
			return nil, werrors.NewNotFoundError("Knowledge copy task not found")
		}
		progress := value.(types.KnowledgeMoveProgress)
		return &progress, nil
	}
	raw, err := s.redisClient.Get(ctx, knowledgeCopyProgressPrefix+taskID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, werrors.NewNotFoundError("Knowledge copy task not found")
	}
	if err != nil {
		return nil, err
	}
	var progress types.KnowledgeMoveProgress
	if err := json.Unmarshal(raw, &progress); err != nil {
		return nil, err
	}
	return &progress, nil
}

func (s *knowledgeService) ProcessKnowledgeCopy(ctx context.Context, task *asynq.Task) error {
	var payload types.KnowledgeCopyPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil || payload.TaskID == "" {
		return fmt.Errorf("invalid knowledge copy payload: %w", asynq.SkipRetry)
	}
	ids, err := writeResourceIDs(payload.KnowledgeIDs)
	if err != nil || len(ids) == 0 || len(payload.KnowledgeIDs) > 20 {
		return fmt.Errorf("invalid knowledge copy batch: %w", asynq.SkipRetry)
	}

	progress := types.KnowledgeMoveProgress{
		TaskID: payload.TaskID, SourceKBID: payload.SourceKBID, TargetKBID: payload.TargetKBID,
		Status: types.KBCloneStatusProcessing, Total: len(ids), CreatedAt: time.Now().Unix(),
	}
	ctx = types.WithExecutionTenant(payload.Initiator.Apply(ctx), payload.TenantID)
	ctx = withKBActivityTask(ctx, payload.TaskID, kbActivityTrigger(ctx))
	if previous, err := s.GetKnowledgeCopyProgress(ctx, payload.TaskID); err == nil {
		progress.CreatedAt = previous.CreatedAt
	}
	save := func(ctx context.Context) {
		progress.UpdatedAt = time.Now().Unix()
		if err := s.storeKnowledgeCopyProgress(ctx, progress, false); err != nil {
			logger.Warnf(ctx, "failed to save knowledge copy progress: %v", err)
		}
	}
	save(ctx)
	// Copy requests into one KB serialize the duplicate check and writes.
	key := fmt.Sprintf("weknora:knowledge-copy:%d:%s", payload.TenantID, payload.TargetKBID)
	run := func(ctx context.Context) error {
		return s.copyKnowledgeBatch(ctx, payload, ids, &progress, save)
	}
	if s.redisClient != nil {
		err = redislock.WithRenewableLock(ctx, s.redisClient, key, time.Minute, 15*time.Second, run)
	} else {
		var unlock func()
		unlock, err = knowledgeCopyLocks.lock(ctx, key)
		if err == nil {
			defer unlock()
			err = run(ctx)
		}
	}
	if err != nil {
		progress.Error = err.Error()
		progress.Message = "Knowledge copy will retry"
		retry, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)
		if retry >= maxRetry || errors.Is(err, asynq.SkipRetry) {
			progress.Status = types.KBCloneStatusFailed
			progress.Message = "Knowledge copy failed"
		}
	} else {
		progress.Status = types.KBCloneStatusCompleted
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	save(saveCtx)
	return err
}

func (s *knowledgeService) copyKnowledgeBatch(
	ctx context.Context,
	payload types.KnowledgeCopyPayload,
	ids []string,
	progress *types.KnowledgeMoveProgress,
	save func(context.Context),
) error {
	source, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.SourceKBID)
	if err != nil {
		return err
	}
	target, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.TargetKBID)
	if err != nil {
		return err
	}
	if source == nil || target == nil || source.ID != payload.SourceKBID || target.ID != payload.TargetKBID {
		return fmt.Errorf("invalid knowledge copy binding: %w", asynq.SkipRetry)
	}
	ctx, err = access.WithKBTransferTask(
		ctx, source, target, payload.TenantID, access.KBTransferCopy, payload.TaskID, false,
	)
	if err != nil {
		return fmt.Errorf("invalid knowledge copy scope: %v: %w", err, asynq.SkipRetry)
	}
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		return err
	}
	if tenant == nil || tenant.ID != payload.TenantID {
		return fmt.Errorf("invalid knowledge copy workspace: %w", asynq.SkipRetry)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	err = access.ValidateKBTransferCompatibility(source, target, access.KBTransferCopy, payload.Mode, tenant)
	if err != nil {
		return fmt.Errorf("%v: %w", err, asynq.SkipRetry)
	}
	rows, err := s.repo.GetKnowledgeBatch(ctx, source.TenantID, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*types.Knowledge, len(rows))
	for _, row := range rows {
		if row != nil {
			byID[row.ID] = row
		}
	}
	for _, id := range ids {
		row := byID[id]
		if validateTransferKnowledge(row, source) != nil || row.ParseStatus != types.ParseStatusCompleted ||
			(row.EmbeddingModelID != "" && row.EmbeddingModelID != source.EmbeddingModelID) {
			return fmt.Errorf(
				"knowledge %s must be completed in the source and use its embedding model: %w", id, asynq.SkipRetry,
			)
		}
		if _, err := s.transferChunks(ctx, row, source.ID); err != nil {
			return err
		}
	}

	targetRows, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, target.TenantID, target.ID)
	if err != nil {
		return err
	}
	checkpoints := make(map[string]*types.Knowledge)
	for _, row := range targetRows {
		if validateTransferKnowledge(row, target) != nil {
			return access.ErrForbidden
		}
		state, err := transferState(row)
		if err != nil {
			return err
		}
		if state != nil &&
			matchesTransfer(ctx, state, source, target, access.KBTransferCopy, state.SourceID, payload.Mode) {
			checkpoints[state.SourceID] = row
		}
	}

	var failures error
	for _, id := range ids {
		if ctx.Err() != nil {
			return errors.Join(failures, ctx.Err())
		}
		err := s.copyKnowledgeItem(ctx, byID[id], checkpoints[id], target, payload.Mode)
		if err != nil {
			failures = errors.Join(failures, fmt.Errorf("knowledge %s: %w", id, err))
			progress.Failed++
		}
		progress.Processed++
		progress.Progress = progress.Processed * 100 / progress.Total
		progress.Message = fmt.Sprintf("Processed %d/%d documents", progress.Processed, progress.Total)
		save(ctx)
	}
	if progress.Processed > progress.Failed {
		_ = requestKnowledgeBaseProfileRefresh(ctx, s.task, target, false)
	}
	return failures
}

func (s *knowledgeService) copyKnowledgeItem(
	ctx context.Context, src, checkpoint *types.Knowledge, target *types.KnowledgeBase, mode string,
) error {
	if checkpoint != nil {
		state, err := transferState(checkpoint)
		if err != nil {
			return err
		}
		if checkpoint.ParseStatus == types.ParseStatusCompleted {
			if state.Phase != "done" {
				return werrors.NewConflictError("destination document changed during copy")
			}
			if target.IsWikiAutoGenerateEnabled() {
				_, err := EnqueueWikiIngest(ctx, s.task, s.taskPendingRepo, target.TenantID, target.ID, checkpoint.ID)
				return err
			}
			return nil
		}
		if state.Phase == "done" {
			return werrors.NewConflictError("destination document changed after copy")
		}
		if err := deleteReferencedKnowledge(ctx, s, target.ID, []string{checkpoint.ID}); err != nil {
			return err
		}
	}
	exists, _, err := s.repo.CheckKnowledgeExists(ctx, target.TenantID, target.ID, &types.KnowledgeCheckParams{
		Type: src.Type, FileHash: src.FileHash, FileName: src.FileName, FileSize: src.FileSize,
		FileType: src.FileType, URL: src.Source,
	})
	if err != nil {
		return err
	}
	if exists {
		return werrors.NewConflictError("document already exists in the target knowledge base")
	}
	tenant, err := s.tenantRepo.GetTenantByID(ctx, target.TenantID)
	if err != nil {
		return err
	}
	if tenant == nil || tenant.ID != target.TenantID {
		return access.ErrForbidden
	}
	if tenant.StorageQuota > 0 && src.StorageSize > tenant.StorageQuota-tenant.StorageUsed {
		return types.NewStorageQuotaExceededError()
	}
	return s.cloneKnowledgeForTransfer(ctx, src, target, access.KBTransferCopy, mode)
}
