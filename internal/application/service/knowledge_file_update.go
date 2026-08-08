package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

var replaceableKnowledgeStatuses = map[string]struct{}{
	types.ParseStatusCompleted: {},
	types.ParseStatusFailed:    {},
	types.ParseStatusCancelled: {},
}

// CreateOrUpdateKnowledgeFromFile implements the unified HTTP upsert contract.
// An explicit KnowledgeID always wins. Without it, a unique filename match in
// the same KB is treated as an in-place update; otherwise the request creates
// a new knowledge.
func (s *knowledgeService) CreateOrUpdateKnowledgeFromFile(
	ctx context.Context,
	req *types.KnowledgeFileCreateOrUpdateRequest,
) (*types.KnowledgeFileUpsertResult, error) {
	if req == nil || req.File == nil {
		return nil, werrors.NewBadRequestError("File upload failed")
	}
	if req.KnowledgeID != "" {
		return s.UpdateKnowledgeFile(ctx, knowledgeFileUpdateRequestFromUpsert(req))
	}

	_, safeFilename, err := resolveKnowledgeFileUpdateName(req.File.Filename, req.CustomFileName)
	if err != nil {
		return nil, err
	}
	existing, err := s.findUniqueFileKnowledgeByName(ctx, req.KnowledgeBaseID, safeFilename)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		updateReq := knowledgeFileUpdateRequestFromUpsert(req)
		updateReq.KnowledgeID = existing.ID
		return s.UpdateKnowledgeFile(ctx, updateReq)
	}

	channel := req.Channel
	if channel == "" {
		channel = types.ChannelAPI
	}
	knowledge, err := s.CreateKnowledgeFromFile(
		ctx,
		req.KnowledgeBaseID,
		req.File,
		req.Metadata,
		req.EnableMultimodel,
		req.CustomFileName,
		req.TagIDs,
		channel,
		req.ProcessOverrides,
	)
	if err != nil {
		if duplicate, ok := err.(*types.DuplicateKnowledgeError); ok {
			existing := duplicate.Knowledge
			if existing == nil {
				existing = knowledge
			}
			return &types.KnowledgeFileUpsertResult{
				Action: "unchanged", Knowledge: existing,
			}, nil
		}
		return nil, err
	}
	return &types.KnowledgeFileUpsertResult{Action: "created", Knowledge: knowledge}, nil
}

func knowledgeFileUpdateRequestFromUpsert(
	req *types.KnowledgeFileCreateOrUpdateRequest,
) *types.KnowledgeFileUpdateRequest {
	return &types.KnowledgeFileUpdateRequest{
		KnowledgeBaseID:       req.KnowledgeBaseID,
		KnowledgeID:           req.KnowledgeID,
		File:                  req.File,
		CustomFileName:        req.CustomFileName,
		ExpectedFileHash:      req.ExpectedFileHash,
		ExpectedUpdateVersion: req.ExpectedUpdateVersion,
		Metadata:              req.Metadata,
		MetadataProvided:      req.MetadataProvided,
		TagIDs:                req.TagIDs,
		TagIDsProvided:        req.TagIDsProvided,
		Channel:               req.Channel,
		ChannelProvided:       req.ChannelProvided,
		ProcessOverrides:      req.ProcessOverrides,
	}
}

func resolveKnowledgeFileUpdateName(
	uploadFileName string,
	customFileName string,
) (folderPath string, safeFilename string, err error) {
	fileName := uploadFileName
	if customFileName != "" {
		folderPath, fileName = types.SplitKnowledgeRelativePath(customFileName)
		if fileName == "" {
			fileName = uploadFileName
		}
	}
	safeFilename, valid := secutils.ValidateInput(fileName)
	if !valid {
		return "", "", werrors.NewValidationError("文件名包含非法字符")
	}
	if folderPath != "" {
		safeFolderPath, folderValid := secutils.ValidateInput(folderPath)
		if !folderValid {
			return "", "", werrors.NewValidationError("文件夹路径包含非法字符")
		}
		folderPath = types.NormalizeKnowledgeFolderPath(safeFolderPath)
	}
	return folderPath, safeFilename, nil
}

func (s *knowledgeService) findUniqueFileKnowledgeByName(
	ctx context.Context, kbID string, fileName string,
) (*types.Knowledge, error) {
	if fileName == "" {
		return nil, nil
	}
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok || tenantID == 0 {
		return nil, werrors.NewUnauthorizedError("tenant context is required")
	}
	knowledges, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	var match *types.Knowledge
	for _, knowledge := range knowledges {
		if knowledge == nil ||
			knowledge.Type != "file" ||
			knowledge.FileName != fileName ||
			knowledge.FilePath == "" ||
			knowledge.ParseStatus == types.ParseStatusDeleting {
			continue
		}
		if match != nil {
			return nil, werrors.NewConflictError(
				"multiple file knowledge entries match this filename; pass knowledge_id explicitly",
			)
		}
		match = knowledge
	}
	return match, nil
}

// UpdateKnowledgeFile validates and durably stages the latest requested file.
// The coordinator claims the current knowledge only when it is safe to apply.
func (s *knowledgeService) UpdateKnowledgeFile(
	ctx context.Context,
	req *types.KnowledgeFileUpdateRequest,
) (*types.KnowledgeFileUpsertResult, error) {
	if req == nil || req.File == nil {
		return nil, werrors.NewBadRequestError("File upload failed")
	}
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok || tenantID == 0 {
		return nil, werrors.NewUnauthorizedError("tenant context is required")
	}

	existing, err := s.repo.GetKnowledgeByID(ctx, tenantID, req.KnowledgeID)
	if err != nil {
		if stderrors.Is(err, repository.ErrKnowledgeNotFound) {
			return nil, werrors.NewNotFoundError("knowledge not found")
		}
		return nil, err
	}
	if existing.KnowledgeBaseID != req.KnowledgeBaseID {
		return nil, werrors.NewConflictError("knowledge does not belong to the requested knowledge base")
	}
	if existing.Type != "file" || existing.FilePath == "" {
		return nil, werrors.NewBadRequestError("only file knowledge can be replaced")
	}
	if existing.ParseStatus == types.ParseStatusDeleting {
		return nil, werrors.NewConflictError("knowledge is being deleted")
	}
	if req.ExpectedFileHash != "" && req.ExpectedFileHash != existing.FileHash {
		return nil, werrors.NewConflictError("expected_file_hash does not match the current file")
	}

	maxSizeMB := secutils.GetMaxFileSizeMB()
	if req.File.Size > maxSizeMB*1024*1024 {
		return nil, werrors.NewBadRequestError(fmt.Sprintf("文件大小不能超过%dMB", maxSizeMB))
	}

	folderPath, safeFilename, err := resolveKnowledgeFileUpdateName(req.File.Filename, req.CustomFileName)
	if err != nil {
		return nil, err
	}
	fileType := getFileType(safeFilename)
	if IsVideoType(fileType) {
		return nil, werrors.NewBadRequestError("暂不支持上传视频文件")
	}
	if !isValidFileType(safeFilename) {
		return nil, ErrInvalidFileType
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, req.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if kb.Type == types.KnowledgeBaseTypeFAQ {
		return nil, werrors.NewBadRequestError("FAQ 知识库不支持文件上传，请使用 FAQ 导入功能")
	}
	if err := s.checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}
	if err := s.checkRetrieveEngineConfigured(ctx, kb, tenantID); err != nil {
		return nil, err
	}
	if req.TagIDsProvided {
		if err := s.validateKnowledgeTagIDs(ctx, tenantID, req.KnowledgeBaseID, req.TagIDs); err != nil {
			return nil, err
		}
	}

	storedOverrides, err := existing.ProcessOverrides()
	if err != nil {
		return nil, fmt.Errorf("parse stored process overrides: %w", err)
	}
	effectiveOverrides := storedOverrides
	if req.ProcessOverrides != nil {
		effectiveOverrides = req.ProcessOverrides
	}
	validationOverrides := effectiveOverrides
	if validationOverrides == nil {
		validationOverrides = &types.KnowledgeProcessOverrides{}
	}
	if err := ValidateProcessOverrides(ctx, kb, validationOverrides, []string{fileType}); err != nil {
		return nil, err
	}

	newHash, err := calculateFileHash(req.File)
	if err != nil {
		return nil, err
	}
	if (existing.FileUpdateState == "" || existing.FileUpdateState == types.KnowledgeFileUpdateStateIdle) &&
		newHash == existing.FileHash && safeFilename == existing.FileName && folderPath == existing.FolderPath &&
		!req.MetadataProvided && !req.TagIDsProvided && !req.ChannelProvided && req.ProcessOverrides == nil {
		s.attachTagsToKnowledge(ctx, existing)
		return &types.KnowledgeFileUpsertResult{Action: "unchanged", Knowledge: existing}, nil
	}

	exists, duplicate, err := s.repo.CheckKnowledgeExistsExcluding(
		ctx,
		tenantID,
		req.KnowledgeBaseID,
		existing.ID,
		&types.KnowledgeCheckParams{
			Type:     "file",
			FileName: safeFilename,
			FileSize: req.File.Size,
			FileHash: newHash,
		},
	)
	if err != nil {
		return nil, err
	}
	if exists {
		duplicateID := ""
		if duplicate != nil {
			duplicateID = duplicate.ID
		}
		return nil, werrors.NewConflictError(fmt.Sprintf("replacement file already exists as knowledge %s", duplicateID))
	}

	payload := types.KnowledgeFileUpdatePayload{
		TenantID:         tenantID,
		KnowledgeBaseID:  req.KnowledgeBaseID,
		KnowledgeID:      existing.ID,
		NewFileName:      safeFilename,
		NewFolderPath:    folderPath,
		NewFileType:      fileType,
		NewFileSize:      req.File.Size,
		NewFileHash:      newHash,
		Metadata:         req.Metadata,
		MetadataProvided: req.MetadataProvided,
		TagIDs:           req.TagIDs,
		TagIDsProvided:   req.TagIDsProvided,
		Channel:          req.Channel,
		ChannelProvided:  req.ChannelProvided,
		ProcessConfig:    req.ProcessOverrides,
		ProcessProvided:  req.ProcessOverrides != nil,
		Initiator:        types.TaskInitiatorFromContext(ctx),
	}
	if existing.FileUpdateState == types.KnowledgeFileUpdateResultActive ||
		existing.FileUpdateState == types.KnowledgeFileUpdateResultPending {
		slot, slotErr := s.repo.GetKnowledgeFileUpdateSlot(ctx, tenantID, existing.ID)
		if slotErr != nil {
			return nil, slotErr
		}
		latest := slot.ActivePayload
		if slot.PendingVersion != nil {
			latest = slot.PendingPayload
		}
		var accepted types.KnowledgeFileUpdatePayload
		if json.Unmarshal(latest, &accepted) == nil && sameKnowledgeFileUpdate(&accepted, &payload) {
			s.attachTagsToKnowledge(ctx, existing)
			return &types.KnowledgeFileUpsertResult{
				Action:           "unchanged",
				Knowledge:        existing,
				UpdateVersion:    slot.LatestVersion,
				UpdateState:      existing.FileUpdateState,
				AcceptedFileHash: newHash,
			}, nil
		}
	}

	fileSvc := s.resolveFileService(ctx, kb)
	if fileSvc == nil {
		return nil, fmt.Errorf("file service is not configured")
	}
	newFilePath, err := fileSvc.SaveFile(ctx, req.File, tenantID, existing.ID)
	if err != nil {
		return nil, err
	}
	cleanupStaged := func() {
		if deleteErr := fileSvc.DeleteFile(ctx, newFilePath); deleteErr != nil {
			logger.Errorf(ctx, "Failed to delete staged replacement file %s: %v", newFilePath, deleteErr)
		}
	}
	payload.NewFilePath = newFilePath
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		cleanupStaged()
		return nil, fmt.Errorf("encode knowledge file update: %w", err)
	}

	staged, err := s.repo.StageKnowledgeFileUpdate(
		ctx, tenantID, existing.ID, req.KnowledgeBaseID, types.JSON(payloadBytes), req.ExpectedUpdateVersion,
	)
	if err != nil {
		cleanupStaged()
		if stderrors.Is(err, repository.ErrKnowledgeFileUpdateVersionConflict) {
			return nil, werrors.NewConflictError("expected_update_version does not match the latest accepted version")
		}
		if stderrors.Is(err, repository.ErrKnowledgeFileUpdateDeleting) ||
			stderrors.Is(err, repository.ErrKnowledgeNotFound) {
			return nil, werrors.NewConflictError("knowledge is being deleted")
		}
		return nil, err
	}
	s.deleteStagedPayloadBestEffort(ctx, kb, staged.ReplacedPendingPayload)
	s.deleteSupersededActivePayloadBestEffort(ctx, kb, existing.FilePath, staged.ReplacedActivePayload)

	taskID, err := s.enqueueKnowledgeFileUpdate(ctx, types.KnowledgeFileUpdateTaskPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: req.KnowledgeBaseID,
		KnowledgeID:     existing.ID,
		ActiveVersion:   staged.ActiveVersion,
	}, 0)
	if err != nil {
		return nil, werrors.NewServiceUnavailableError(
			"file update was saved but the worker is temporarily unavailable; retry is safe")
	}

	existing.FileUpdateVersion = staged.Version
	existing.FileUpdateState = staged.State
	s.attachTagsToKnowledge(ctx, existing)
	return &types.KnowledgeFileUpsertResult{
		Action:           "updated",
		Knowledge:        existing,
		TaskID:           taskID,
		UpdateVersion:    staged.Version,
		UpdateState:      staged.State,
		AcceptedFileHash: newHash,
	}, nil
}

// applyKnowledgeFileUpdatePayload cleans old derived resources, switches the
// source file in place, and enqueues the normal document parser.
func (s *knowledgeService) applyKnowledgeFileUpdatePayload(
	ctx context.Context, payload *types.KnowledgeFileUpdatePayload,
) error {
	ctx = payload.Initiator.Apply(ctx)
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		return fmt.Errorf("load replacement tenant: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	ctx = withAttempt(ctx, payload.Attempt)

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("load replacement knowledge base: %w", err)
	}
	current, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		if stderrors.Is(err, repository.ErrKnowledgeNotFound) {
			s.deleteReplacementFileBestEffort(ctx, kb, payload.NewFilePath)
			return nil
		}
		return err
	}

	// A retry after the file switch only has to finish tag persistence and
	// deterministic parser enqueue. A parse failure is not retried here.
	if current.FilePath == payload.NewFilePath && current.FileHash == payload.NewFileHash {
		switch current.ParseStatus {
		case types.ParseStatusPending:
			return s.finishKnowledgeFileUpdate(ctx, kb, current, payload)
		case types.ParseStatusProcessing, types.ParseStatusFinalizing, types.ParseStatusCompleted,
			types.ParseStatusFailed, types.ParseStatusCancelled:
			return nil
		}
	}

	if current.KnowledgeBaseID != payload.KnowledgeBaseID ||
		current.ParseStatus != types.ParseStatusReplacing ||
		current.FilePath != payload.OldFilePath ||
		current.FileHash != payload.OldFileHash {
		s.deleteReplacementFileBestEffort(ctx, kb, payload.NewFilePath)
		return nil
	}

	s.dequeueKnowledgeTasks(ctx, current.ID)
	if payload.Attempt == 0 {
		if _, attempt, openErr := s.tracker().OpenAttempt(ctx, current.ID, payload.LangfuseTraceID); openErr == nil {
			payload.Attempt = attempt
			ctx = withAttempt(ctx, attempt)
		} else {
			logger.Warnf(ctx, "Open replacement attempt failed for %s: %v", current.ID, openErr)
		}
	}
	if kb.IsWikiEnabled() {
		s.prepareWikiForReparse(ctx, current)
	}
	if err := s.cleanupKnowledgeResources(ctx, current); err != nil {
		// cleanupKnowledgeResources adjusts tenant quota before returning a
		// combined error. Persisting zero prevents a retry from decrementing it
		// a second time while the remaining cleanup is retried.
		if current.StorageSize == 0 {
			_, persistErr := s.repo.UpdateApplyingKnowledgeFileColumns(
				ctx, payload.TenantID, current.ID, payload.KnowledgeBaseID,
				payload.OldFilePath, payload.OldFileHash,
				map[string]interface{}{"storage_size": 0},
			)
			if persistErr != nil {
				logger.Errorf(ctx, "Failed to persist replacement cleanup storage size: %v", persistErr)
			}
		}
		return err
	}

	metadata, err := replacementMetadata(current, payload)
	if err != nil {
		return err
	}
	updated, err := s.repo.UpdateApplyingKnowledgeFileColumns(
		ctx,
		payload.TenantID,
		current.ID,
		payload.KnowledgeBaseID,
		payload.OldFilePath,
		payload.OldFileHash,
		map[string]interface{}{
			"file_path":              payload.NewFilePath,
			"file_name":              payload.NewFileName,
			"title":                  payload.NewFileName,
			"folder_path":            payload.NewFolderPath,
			"file_type":              payload.NewFileType,
			"file_size":              payload.NewFileSize,
			"file_hash":              payload.NewFileHash,
			"metadata":               metadata,
			"channel":                replacementChannel(current.Channel, payload),
			"embedding_model_id":     kb.EmbeddingModelID,
			"storage_size":           0,
			"description":            "",
			"processed_at":           nil,
			"pending_subtasks_count": 0,
			"summary_status":         types.SummaryStatusNone,
			"enable_status":          "disabled",
			"error_message":          "",
			"parse_status":           types.ParseStatusPending,
			"updated_at":             time.Now(),
		},
	)
	if err != nil {
		return err
	}
	if !updated {
		s.deleteReplacementFileBestEffort(ctx, kb, payload.NewFilePath)
		return nil
	}

	current.FilePath = payload.NewFilePath
	current.FileName = payload.NewFileName
	current.Title = payload.NewFileName
	current.FolderPath = payload.NewFolderPath
	current.FileType = payload.NewFileType
	current.FileSize = payload.NewFileSize
	current.FileHash = payload.NewFileHash
	current.Metadata = metadata
	current.Channel = replacementChannel(current.Channel, payload)
	current.EmbeddingModelID = kb.EmbeddingModelID
	current.StorageSize = 0
	current.Description = ""
	current.ProcessedAt = nil
	current.PendingSubtasksCount = 0
	current.SummaryStatus = types.SummaryStatusNone
	current.EnableStatus = "disabled"
	current.ErrorMessage = ""
	current.ParseStatus = types.ParseStatusPending

	return s.finishKnowledgeFileUpdate(ctx, kb, current, payload)
}

const knowledgeFileUpdateWakeDelay = 5 * time.Second

// ProcessKnowledgeFileUpdate coordinates the durable active/pending slots. It
// never trusts a file payload from Redis; the active version is loaded from the
// database on every execution.
func (s *knowledgeService) ProcessKnowledgeFileUpdate(ctx context.Context, task *asynq.Task) error {
	var wake types.KnowledgeFileUpdateTaskPayload
	if err := json.Unmarshal(task.Payload(), &wake); err != nil {
		return fmt.Errorf("decode knowledge file update task: %w", err)
	}
	tenant, err := s.tenantRepo.GetTenantByID(ctx, wake.TenantID)
	if err != nil {
		return fmt.Errorf("load file update tenant: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, wake.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	slot, err := s.repo.GetKnowledgeFileUpdateSlot(ctx, wake.TenantID, wake.KnowledgeID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if slot.ActiveVersion == nil {
		return nil
	}
	if *slot.ActiveVersion != wake.ActiveVersion {
		if slot.ActiveState == types.KnowledgeFileUpdateStateFailed {
			return nil
		}
		_, err := s.enqueueKnowledgeFileUpdate(ctx, types.KnowledgeFileUpdateTaskPayload{
			TenantID:        slot.TenantID,
			KnowledgeBaseID: slot.KnowledgeBaseID,
			KnowledgeID:     slot.KnowledgeID,
			ActiveVersion:   *slot.ActiveVersion,
		}, 0)
		return err
	}
	if slot.ActiveState == types.KnowledgeFileUpdateStateFailed {
		return nil
	}
	if slot.ActiveState == types.KnowledgeFileUpdateStateRetryWait {
		claimed, err := s.repo.TransitionKnowledgeFileUpdateState(
			ctx, wake.TenantID, wake.KnowledgeID, wake.ActiveVersion,
			types.KnowledgeFileUpdateStateRetryWait, types.KnowledgeFileUpdateStateWaiting, "",
		)
		if err != nil || !claimed {
			return err
		}
		slot.ActiveState = types.KnowledgeFileUpdateStateWaiting
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, wake.KnowledgeBaseID)
	if err != nil {
		return err
	}
	current, err := s.repo.GetKnowledgeByID(ctx, wake.TenantID, wake.KnowledgeID)
	if err != nil {
		if stderrors.Is(err, repository.ErrKnowledgeNotFound) {
			return s.cancelKnowledgeFileUpdate(ctx, kb, wake.TenantID, wake.KnowledgeID)
		}
		return err
	}
	if current.ParseStatus == types.ParseStatusDeleting {
		return s.cancelKnowledgeFileUpdate(ctx, kb, wake.TenantID, wake.KnowledgeID)
	}

	var active types.KnowledgeFileUpdatePayload
	if err := json.Unmarshal(slot.ActivePayload, &active); err != nil {
		return s.failKnowledgeFileUpdate(ctx, wake, slot.ActiveState, fmt.Errorf("decode active update payload: %w", err))
	}

	if slot.ActiveState == types.KnowledgeFileUpdateStateWaiting {
		if _, terminal := replaceableKnowledgeStatuses[current.ParseStatus]; !terminal {
			if current.ParseStatus != types.ParseStatusReplacing || active.OldFilePath == "" ||
				current.FilePath != active.OldFilePath || current.FileHash != active.OldFileHash {
				return s.deferKnowledgeFileUpdate(ctx, wake)
			}
		} else {
			if active.OldFilePath == "" {
				active.OldParseStatus = current.ParseStatus
				active.OldFilePath = current.FilePath
				active.OldFileHash = current.FileHash
				active.DocumentTaskID = secutils.GenerateTaskID(
					"knowledge_file_parse", wake.TenantID, wake.KnowledgeID,
				)
				prepared, err := json.Marshal(active)
				if err != nil {
					return err
				}
				updated, err := s.repo.PrepareKnowledgeFileUpdate(
					ctx, wake.TenantID, wake.KnowledgeID, wake.ActiveVersion, types.JSON(prepared),
				)
				if err != nil || !updated {
					return err
				}
			}
			claimed, err := s.repo.ClaimKnowledgeFileUpdate(
				ctx, wake.TenantID, wake.KnowledgeID, wake.KnowledgeBaseID,
				current.ParseStatus, active.OldFilePath, active.OldFileHash,
			)
			if err != nil {
				return err
			}
			if !claimed {
				return s.deferKnowledgeFileUpdate(ctx, wake)
			}
		}

		moved, err := s.repo.TransitionKnowledgeFileUpdateState(
			ctx, wake.TenantID, wake.KnowledgeID, wake.ActiveVersion,
			types.KnowledgeFileUpdateStateWaiting, types.KnowledgeFileUpdateStateApplying, "",
		)
		if err != nil || !moved {
			return err
		}
	}

	if err := s.applyKnowledgeFileUpdatePayload(ctx, &active); err != nil {
		return s.failKnowledgeFileUpdate(ctx, wake, types.KnowledgeFileUpdateStateApplying, err)
	}

	completed, err := s.repo.CompleteKnowledgeFileUpdate(
		ctx, wake.TenantID, wake.KnowledgeID, wake.ActiveVersion,
	)
	if err != nil {
		return err
	}
	if completed.ActiveVersion != nil {
		_, err = s.enqueueKnowledgeFileUpdate(ctx, types.KnowledgeFileUpdateTaskPayload{
			TenantID:        completed.TenantID,
			KnowledgeBaseID: completed.KnowledgeBaseID,
			KnowledgeID:     completed.KnowledgeID,
			ActiveVersion:   *completed.ActiveVersion,
		}, 0)
	}
	return err
}

// RetryKnowledgeFileUpdate re-arms the retained failed active payload. The
// exact active version is guarded so a concurrent upload wins safely.
func (s *knowledgeService) RetryKnowledgeFileUpdate(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok || tenantID == 0 {
		return nil, werrors.NewUnauthorizedError("tenant context is required")
	}
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		return nil, werrors.NewConflictError("knowledge is being deleted")
	}
	slot, err := s.repo.GetKnowledgeFileUpdateSlot(ctx, tenantID, knowledgeID)
	if err != nil || slot.ActiveVersion == nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) || (err == nil && slot.ActiveVersion == nil) {
			return nil, werrors.NewConflictError("no failed file update is available")
		}
		return nil, err
	}
	if slot.ActiveState != types.KnowledgeFileUpdateStateFailed {
		return nil, werrors.NewConflictError("file update is not failed")
	}
	version := *slot.ActiveVersion
	moved, err := s.repo.TransitionKnowledgeFileUpdateState(
		ctx, tenantID, knowledgeID, version,
		types.KnowledgeFileUpdateStateFailed, types.KnowledgeFileUpdateStateWaiting, "",
	)
	if err != nil {
		return nil, err
	}
	if !moved {
		return nil, werrors.NewConflictError("file update changed; refresh and retry")
	}
	if _, err := s.enqueueKnowledgeFileUpdate(ctx, types.KnowledgeFileUpdateTaskPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: slot.KnowledgeBaseID,
		KnowledgeID:     knowledgeID,
		ActiveVersion:   version,
	}, 0); err != nil {
		_, _ = s.repo.TransitionKnowledgeFileUpdateState(
			ctx, tenantID, knowledgeID, version,
			types.KnowledgeFileUpdateStateWaiting, types.KnowledgeFileUpdateStateFailed,
			"retry enqueue failed",
		)
		return nil, werrors.NewServiceUnavailableError("file update retry is temporarily unavailable")
	}
	return s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
}

// DiscardKnowledgeFileUpdate removes the exact failed active version and its
// pending successor, then restores a claimed old source to its prior terminal
// state when the file switch had not happened yet.
func (s *knowledgeService) DiscardKnowledgeFileUpdate(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok || tenantID == 0 {
		return nil, werrors.NewUnauthorizedError("tenant context is required")
	}
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		return nil, werrors.NewConflictError("knowledge is being deleted")
	}
	slot, err := s.repo.GetKnowledgeFileUpdateSlot(ctx, tenantID, knowledgeID)
	if err != nil || slot.ActiveVersion == nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) || (err == nil && slot.ActiveVersion == nil) {
			return nil, werrors.NewConflictError("no failed file update is available")
		}
		return nil, err
	}
	if slot.ActiveState != types.KnowledgeFileUpdateStateFailed {
		return nil, werrors.NewConflictError("only a failed file update can be discarded")
	}
	cancelled, err := s.repo.CancelFailedKnowledgeFileUpdate(
		ctx, tenantID, knowledgeID, *slot.ActiveVersion,
	)
	if err != nil {
		if stderrors.Is(err, repository.ErrKnowledgeFileUpdateStateConflict) {
			return nil, werrors.NewConflictError("file update changed; refresh and retry")
		}
		return nil, err
	}

	var active types.KnowledgeFileUpdatePayload
	if json.Unmarshal(cancelled.ActivePayload, &active) == nil &&
		knowledge.ParseStatus == types.ParseStatusReplacing && active.OldFilePath != "" {
		restoreStatus := active.OldParseStatus
		if _, ok := replaceableKnowledgeStatuses[restoreStatus]; !ok {
			restoreStatus = types.ParseStatusFailed
		}
		updated, updateErr := s.repo.UpdateApplyingKnowledgeFileColumns(
			ctx, tenantID, knowledgeID, knowledge.KnowledgeBaseID,
			active.OldFilePath, active.OldFileHash,
			map[string]interface{}{
				"parse_status":  restoreStatus,
				"error_message": "",
				"updated_at":    time.Now(),
			},
		)
		if updateErr != nil {
			return nil, updateErr
		}
		if updated {
			knowledge.ParseStatus = restoreStatus
		}
	}
	kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	s.cleanupCancelledKnowledgeFileUpdate(ctx, kb, knowledge.FilePath, cancelled)
	return s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
}

func (s *knowledgeService) enqueueKnowledgeFileUpdate(
	ctx context.Context, wake types.KnowledgeFileUpdateTaskPayload, delay time.Duration,
) (string, error) {
	payload, err := json.Marshal(wake)
	if err != nil {
		return "", err
	}
	opts := []asynq.Option{
		asynq.Queue(types.QueueMaintenance),
		asynq.MaxRetry(3),
		asynq.Timeout(2 * time.Hour),
		asynq.Unique(2 * time.Hour),
	}
	if delay > 0 {
		opts = append(opts, asynq.ProcessIn(delay))
	}
	info, err := s.task.Enqueue(asynq.NewTask(types.TypeKnowledgeFileUpdate, payload), opts...)
	if stderrors.Is(err, asynq.ErrTaskIDConflict) || stderrors.Is(err, asynq.ErrDuplicateTask) {
		return "", nil
	}
	if err != nil {
		logger.Errorf(ctx, "Enqueue knowledge file update failed: knowledge_id=%s version=%d err=%v",
			wake.KnowledgeID, wake.ActiveVersion, err)
		return "", err
	}
	if info == nil {
		return "", nil
	}
	return info.ID, nil
}

func (s *knowledgeService) deferKnowledgeFileUpdate(
	ctx context.Context, wake types.KnowledgeFileUpdateTaskPayload,
) error {
	claimed, err := s.repo.TransitionKnowledgeFileUpdateState(
		ctx, wake.TenantID, wake.KnowledgeID, wake.ActiveVersion,
		types.KnowledgeFileUpdateStateWaiting, types.KnowledgeFileUpdateStateRetryWait, "",
	)
	if err != nil || !claimed {
		return err
	}
	wake.WakeSequence++
	if _, err := s.enqueueKnowledgeFileUpdate(ctx, wake, knowledgeFileUpdateWakeDelay); err != nil {
		_, restoreErr := s.repo.TransitionKnowledgeFileUpdateState(
			ctx, wake.TenantID, wake.KnowledgeID, wake.ActiveVersion,
			types.KnowledgeFileUpdateStateRetryWait, types.KnowledgeFileUpdateStateWaiting, "",
		)
		if restoreErr != nil {
			logger.Errorf(ctx, "Restore file update wait state failed: %v", restoreErr)
		}
		return err
	}
	return nil
}

func (s *knowledgeService) failKnowledgeFileUpdate(
	ctx context.Context,
	wake types.KnowledgeFileUpdateTaskPayload,
	fromState string,
	cause error,
) error {
	retried, retriedOK := asynq.GetRetryCount(ctx)
	maxRetry, maxRetryOK := asynq.GetMaxRetry(ctx)
	if !retriedOK || !maxRetryOK || retried < maxRetry {
		return cause
	}
	message := cause.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	markedFailed, err := s.repo.TransitionKnowledgeFileUpdateState(
		ctx, wake.TenantID, wake.KnowledgeID, wake.ActiveVersion,
		fromState, types.KnowledgeFileUpdateStateFailed, message,
	)
	if err != nil {
		logger.Errorf(ctx, "Mark knowledge file update failed: %v", err)
	}
	if markedFailed {
		s.restoreFailedKnowledgeFileUpdateClaim(ctx, wake)
	}
	return cause
}

func (s *knowledgeService) restoreFailedKnowledgeFileUpdateClaim(
	ctx context.Context,
	wake types.KnowledgeFileUpdateTaskPayload,
) {
	slot, err := s.repo.GetKnowledgeFileUpdateSlot(ctx, wake.TenantID, wake.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Load failed knowledge file update slot: %v", err)
		return
	}
	if slot.ActiveVersion == nil || *slot.ActiveVersion != wake.ActiveVersion {
		return
	}
	var active types.KnowledgeFileUpdatePayload
	if err := json.Unmarshal(slot.ActivePayload, &active); err != nil {
		logger.Errorf(ctx, "Decode failed knowledge file update payload: %v", err)
		return
	}
	if active.OldFilePath == "" {
		return
	}
	restoreStatus := active.OldParseStatus
	if _, ok := replaceableKnowledgeStatuses[restoreStatus]; !ok {
		restoreStatus = types.ParseStatusFailed
	}
	kbID := wake.KnowledgeBaseID
	if kbID == "" {
		kbID = active.KnowledgeBaseID
	}
	updated, err := s.repo.UpdateApplyingKnowledgeFileColumns(
		ctx,
		wake.TenantID,
		wake.KnowledgeID,
		kbID,
		active.OldFilePath,
		active.OldFileHash,
		map[string]interface{}{
			"parse_status":  restoreStatus,
			"error_message": "",
			"updated_at":    time.Now(),
		},
	)
	if err != nil {
		logger.Errorf(ctx, "Restore failed knowledge file update status: %v", err)
		return
	}
	if updated {
		logger.Infof(ctx, "Restored failed knowledge file update status: knowledge_id=%s status=%s",
			wake.KnowledgeID, restoreStatus)
	}
}

func (s *knowledgeService) cancelKnowledgeFileUpdate(
	ctx context.Context, kb *types.KnowledgeBase, tenantID uint64, knowledgeID string,
) error {
	slot, err := s.repo.CancelKnowledgeFileUpdates(ctx, tenantID, knowledgeID)
	if err != nil || slot == nil {
		return err
	}
	s.deleteStagedPayloadBestEffort(ctx, kb, slot.ActivePayload)
	s.deleteStagedPayloadBestEffort(ctx, kb, slot.PendingPayload)
	return nil
}

func (s *knowledgeService) cleanupCancelledKnowledgeFileUpdate(
	ctx context.Context,
	kb *types.KnowledgeBase,
	currentPath string,
	slot *types.KnowledgeFileUpdateSlot,
) {
	if slot == nil {
		return
	}
	// An applying retry may have switched the active payload into the current
	// source already. Leave that path for the normal delete flow to remove.
	s.deleteSupersededActivePayloadBestEffort(ctx, kb, currentPath, slot.ActivePayload)
	s.deleteStagedPayloadBestEffort(ctx, kb, slot.PendingPayload)
}

func (s *knowledgeService) deleteStagedPayloadBestEffort(
	ctx context.Context, kb *types.KnowledgeBase, raw types.JSON,
) {
	if len(raw) == 0 {
		return
	}
	var payload types.KnowledgeFileUpdatePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		logger.Errorf(ctx, "Decode staged file update payload for cleanup failed: %v", err)
		return
	}
	s.deleteReplacementFileBestEffort(ctx, kb, payload.NewFilePath)
}

func (s *knowledgeService) deleteSupersededActivePayloadBestEffort(
	ctx context.Context, kb *types.KnowledgeBase, currentPath string, raw types.JSON,
) {
	if len(raw) == 0 {
		return
	}
	var payload types.KnowledgeFileUpdatePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		logger.Errorf(ctx, "Decode superseded active file update payload failed: %v", err)
		return
	}
	if payload.NewFilePath == currentPath {
		return
	}
	s.deleteReplacementFileBestEffort(ctx, kb, payload.NewFilePath)
}

func sameKnowledgeFileUpdate(a, b *types.KnowledgeFileUpdatePayload) bool {
	if a == nil || b == nil {
		return false
	}
	return a.NewFileName == b.NewFileName &&
		a.NewFolderPath == b.NewFolderPath &&
		a.NewFileType == b.NewFileType &&
		a.NewFileSize == b.NewFileSize &&
		a.NewFileHash == b.NewFileHash &&
		a.MetadataProvided == b.MetadataProvided &&
		reflect.DeepEqual(a.Metadata, b.Metadata) &&
		a.TagIDsProvided == b.TagIDsProvided &&
		slices.Equal(a.TagIDs, b.TagIDs) &&
		a.ChannelProvided == b.ChannelProvided &&
		a.Channel == b.Channel &&
		a.ProcessProvided == b.ProcessProvided &&
		reflect.DeepEqual(a.ProcessConfig, b.ProcessConfig)
}

func replacementMetadata(
	knowledge *types.Knowledge,
	payload *types.KnowledgeFileUpdatePayload,
) (types.JSON, error) {
	metadata, err := knowledge.Metadata.Map()
	if err != nil {
		return nil, fmt.Errorf("parse knowledge metadata: %w", err)
	}
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	if payload.MetadataProvided {
		for key, value := range payload.Metadata {
			if key == "process_overrides" {
				continue
			}
			metadata[key] = value
		}
	}
	bytes, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	knowledge.Metadata = types.JSON(bytes)
	if payload.ProcessProvided {
		if err := knowledge.SetProcessOverrides(payload.ProcessConfig); err != nil {
			return nil, err
		}
	}
	return knowledge.Metadata, nil
}

func replacementChannel(current string, payload *types.KnowledgeFileUpdatePayload) string {
	if payload.ChannelProvided {
		return payload.Channel
	}
	return current
}

func (s *knowledgeService) finishKnowledgeFileUpdate(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	payload *types.KnowledgeFileUpdatePayload,
) error {
	if payload.TagIDsProvided {
		if err := s.repo.SetKnowledgeTags(ctx, knowledge.ID, payload.TagIDs); err != nil {
			return fmt.Errorf("set replacement knowledge tags: %w", err)
		}
	}
	overrides, err := knowledge.ProcessOverrides()
	if err != nil {
		return err
	}
	eff := ResolveProcessConfig(kb, overrides)
	questionCount := eff.QuestionGenerationConfig.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	lang, _ := types.LanguageFromContext(ctx)
	documentPayload := types.DocumentProcessPayload{
		TenantID:                 payload.TenantID,
		KnowledgeID:              knowledge.ID,
		KnowledgeBaseID:          knowledge.KnowledgeBaseID,
		FilePath:                 knowledge.FilePath,
		FileName:                 knowledge.FileName,
		FileType:                 knowledge.FileType,
		EnableMultimodel:         eff.EnableMultimodel,
		EnableQuestionGeneration: eff.QuestionGenerationConfig.Enabled,
		QuestionCount:            questionCount,
		Language:                 lang,
		Attempt:                  payload.Attempt,
	}
	langfuse.InjectTracing(ctx, &documentPayload)
	payloadBytes, err := json.Marshal(documentPayload)
	if err != nil {
		return err
	}
	documentTask := asynq.NewTask(types.TypeDocumentProcess, payloadBytes)
	_, err = s.task.Enqueue(
		documentTask,
		documentProcessTaskOptions(s.config, asynq.TaskID(payload.DocumentTaskID), asynq.MaxRetry(3))...,
	)
	if err != nil && !stderrors.Is(err, asynq.ErrTaskIDConflict) && !stderrors.Is(err, asynq.ErrDuplicateTask) {
		return fmt.Errorf("enqueue replacement document process: %w", err)
	}

	s.deleteReplacementFileBestEffort(ctx, kb, payload.OldFilePath)
	if slices.Contains([]string{"csv", "xlsx", "xls"}, knowledge.FileType) {
		NewDataTableSummaryTask(ctx, s.task, payload.TenantID, knowledge.ID, kb.SummaryModelID, kb.EmbeddingModelID)
	}
	recordKBActivity(ctx, s.audit, payload.TenantID, payload.KnowledgeBaseID,
		types.AuditActionKnowledgeUpdated, "knowledge", knowledge.ID, types.AuditOutcomeAccepted,
		map[string]any{
			"title":             knowledge.Title,
			"source_type":       "file",
			"file_type":         knowledge.FileType,
			"processing_status": types.ParseStatusPending,
			"trigger":           kbActivityTrigger(ctx),
		})
	return nil
}

func (s *knowledgeService) deleteReplacementFileBestEffort(
	ctx context.Context,
	kb *types.KnowledgeBase,
	filePath string,
) {
	if strings.TrimSpace(filePath) == "" {
		return
	}
	fileSvc := s.resolveFileServiceForPath(ctx, kb, filePath)
	if fileSvc == nil {
		logger.Errorf(ctx, "Cannot delete replacement file %s: file service is not configured", filePath)
		return
	}
	if err := fileSvc.DeleteFile(ctx, filePath); err != nil {
		logger.Errorf(ctx, "Failed to delete replacement file %s: %v", filePath, err)
	}
}
