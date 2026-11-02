package service

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// ReplaceKnowledgeFile swaps the source file of an existing file knowledge in
// place. The knowledge ID, and everything keyed on it (tags, references, data
// source metadata), is preserved while the stored file, hash, size, name and
// metadata are replaced and the document is re-parsed via ReparseKnowledge.
//
// Object storage is not transactional, so the steps are ordered to keep the
// row pointing at a file that exists:
//  1. save the new file under a fresh storage path;
//  2. conditionally point the same source version at the new file;
//  3. dequeue any in-flight parse of the previous source;
//  4. ReparseKnowledge cleans the old chunks/index/graph and enqueues parsing;
//  5. only after that succeeds, delete the old file.
//
// A known CAS conflict at step 2 can discard this attempt's new file. An
// uncertain write keeps it unless a re-read proves this attempt committed in
// the same KB, in which case reparse continues. If step 4 fails the previous source
// columns are restored conditionally (marked failed, since cleanup may already
// have removed the old index), without changing the current folder. The new
// file is discarded only after successful compensation confirms ownership;
// otherwise it may be a newer replacement's rollback source and is retained.
//
// Identical content under the same name and folder is not re-parsed: changed
// metadata is persisted and a DuplicateKnowledgeError carrying this knowledge
// is returned. Unlike CreateKnowledgeFromFile, this does not treat another
// knowledge's hash as a duplicate — path identity is what connectors need.
func (s *knowledgeService) ReplaceKnowledgeFile(ctx context.Context,
	knowledgeID string, file *multipart.FileHeader, customFileName string, metadata map[string]string,
) (*types.Knowledge, error) {
	if file == nil {
		return nil, werrors.NewBadRequestError("file is required")
	}
	existing, kb, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, knowledgeID)
	if err != nil {
		return nil, err
	}
	if kb != nil && kb.Type == types.KnowledgeBaseTypeFAQ {
		return nil, werrors.NewBadRequestError("FAQ 知识库不支持文件上传，请使用 FAQ 导入功能")
	}
	if existing.Type != "file" || existing.FilePath == "" {
		return nil, werrors.NewBadRequestError("only file knowledge can have its file replaced")
	}
	if existing.ParseStatus == types.ParseStatusDeleting {
		return nil, werrors.NewBadRequestError("knowledge is being deleted")
	}
	if existing.ParseStatus == types.ParseStatusPending ||
		existing.ParseStatus == types.ParseStatusProcessing ||
		existing.ParseStatus == types.ParseStatusFinalizing {
		return nil, werrors.NewConflictError("文件正在处理中，请处理完成后再替换")
	}
	if err := s.checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}

	fileName, folderPath := file.Filename, existing.FolderPath
	if customFileName != "" {
		customFolder, customName := types.SplitKnowledgeRelativePath(customFileName)
		if customName == "" {
			customName = file.Filename
		}
		fileName = customName
		// Source replacement does not relocate documents. A same-folder
		// qualified name is accepted for connector compatibility; a different
		// path must use the explicit folder-move API first.
		if knowledgeCustomNameRelocates(customFileName) && customFolder != existing.FolderPath {
			return nil, werrors.NewBadRequestError("替换文件不能更改目录，请先使用目录移动功能")
		}
	}
	safeFileName, ok := secutils.ValidateInput(fileName)
	if !ok {
		return nil, werrors.NewValidationError("文件名包含非法字符")
	}
	if folderPath != "" {
		safeFolderPath, ok := secutils.ValidateInput(folderPath)
		if !ok {
			return nil, werrors.NewValidationError("文件夹路径包含非法字符")
		}
		folderPath = types.NormalizeKnowledgeFolderPath(safeFolderPath)
	}
	fileType := getFileType(safeFileName)

	// ReparseKnowledge reuses the stored overrides as-is, so check them against
	// the new file type here.
	overrides, err := existing.ProcessOverrides()
	if err != nil {
		return nil, err
	}
	if _, err := resolveFileImportProcessConfig(ctx, kb, fileType, overrides, nil); err != nil {
		return nil, err
	}

	if err := ValidateJSONUploadContent(safeFileName, file); err != nil {
		logger.Errorf(ctx, "Invalid JSON replacement content for knowledge %s: %v", existing.ID, err)
		return nil, err
	}

	hash, err := calculateFileHash(file)
	if err != nil {
		return nil, err
	}
	newMetadata, err := mergeKnowledgeMetadata(existing.Metadata, metadata)
	if err != nil {
		return nil, err
	}

	if hash == existing.FileHash && safeFileName == existing.FileName && folderPath == existing.FolderPath {
		values := map[string]interface{}{}
		currentMetadata := existing.GetMetadata()
		for k, v := range metadata {
			if currentMetadata[k] != v {
				values["metadata"] = newMetadata
				break
			}
		}
		current, err := s.repo.UpdateKnowledgeSourceIfCurrent(ctx, existing, values)
		if err != nil {
			return nil, knowledgeFolderError(err)
		}
		return current, types.NewDuplicateFileError(current)
	}

	fileSvc := s.resolveFileService(ctx, kb)
	newPath, err := fileSvc.SaveFile(ctx, file, existing.TenantID, existing.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to save replacement file for knowledge %s: %v", existing.ID, err)
		return nil, err
	}
	// Compensations must still run if the caller's context is cancelled
	// mid-replacement (e.g. a sync task hitting its timeout).
	cleanupCtx := context.WithoutCancel(ctx)

	title := existing.Title
	if title == existing.FileName {
		title = safeFileName
	}
	previousMetadata := existing.Metadata
	if len(previousMetadata) == 0 {
		previousMetadata = types.JSON("{}") // metadata is NOT NULL; an empty JSON writes NULL
	}

	sourceColumns := map[string]interface{}{
		"title":         title,
		"file_name":     safeFileName,
		"file_type":     fileType,
		"file_size":     file.Size,
		"file_hash":     hash,
		"file_path":     newPath,
		"metadata":      newMetadata,
		"parse_status":  types.ParseStatusPending,
		"enable_status": "disabled",
		"error_message": "",
		"updated_at":    time.Now(),
	}
	replacement, err := s.repo.UpdateKnowledgeSourceIfCurrent(ctx, existing, sourceColumns)
	if err != nil {
		current, readErr := s.repo.GetKnowledgeByID(cleanupCtx, existing.TenantID, existing.ID)
		if readErr != nil || current == nil || current.TenantID != existing.TenantID ||
			current.KnowledgeBaseID != existing.KnowledgeBaseID ||
			current.FilePath != newPath ||
			current.FileHash != hash {
			logger.Errorf(ctx, "Failed to point knowledge %s at its replacement file: %v", existing.ID, err)
			// A known CAS conflict is guaranteed not to have written this blob.
			// For an uncertain DB/commit error, another replacement may already
			// hold it as its rollback source even if the current row differs.
			if errors.Is(err, repository.ErrKnowledgeFolderChanged) {
				s.discardReplacementFile(cleanupCtx, fileSvc, existing.TenantID, existing.ID, newPath)
			} else {
				logger.Warnf(ctx, "Keeping replacement file %s after uncertain source update", newPath)
			}
			return nil, knowledgeFolderError(err)
		}
		replacement = current
		if movingErr := access.RejectMovingKnowledge(current); movingErr != nil {
			return nil, movingErr
		}
		logger.Warnf(ctx, "Source update for knowledge %s reported an error after committing; continuing reparse: %v",
			existing.ID, err)
	}

	// The reparse claim is bound to this replacement's source. It owns queue
	// cancellation and cleanup; a failed claim must not touch another attempt.
	reparsed, err := s.reparseKnowledgeSource(ctx, existing.ID, nil, false, replacement)
	if err != nil {
		logger.Errorf(ctx, "Reparse after replacing the file of knowledge %s failed, restoring source: %v",
			existing.ID, err)
		if reparsed == nil {
			logger.Warnf(ctx, "Keeping replacement file %s after reparse admission conflict", newPath)
			return nil, err
		}
		if _, rerr := s.repo.UpdateKnowledgeSourceIfCurrent(cleanupCtx, reparsed, map[string]interface{}{
			"title":         existing.Title,
			"file_name":     existing.FileName,
			"file_type":     existing.FileType,
			"file_size":     existing.FileSize,
			"file_hash":     existing.FileHash,
			"file_path":     existing.FilePath,
			"metadata":      previousMetadata,
			"parse_status":  types.ParseStatusFailed,
			"error_message": "File replacement failed; reparse to rebuild the index",
			"updated_at":    time.Now(),
		}); rerr != nil {
			logger.Errorf(ctx, "Failed to restore the source of knowledge %s: %v", existing.ID, rerr)
			// A newer replacement can need newPath as its backup. The absence
			// of a current row reference does not prove it is safe to delete.
			logger.Warnf(ctx, "Keeping replacement file %s after failed compensation", newPath)
		} else {
			s.discardReplacementFile(cleanupCtx, fileSvc, existing.TenantID, existing.ID, newPath)
		}
		return nil, err
	}

	if existing.FilePath != newPath {
		oldFileSvc := s.resolveFileServiceForPath(cleanupCtx, kb, existing.FilePath)
		if err := oldFileSvc.DeleteFile(cleanupCtx, existing.FilePath); err != nil {
			logger.Warnf(ctx, "Failed to delete replaced file %s of knowledge %s: %v",
				existing.FilePath, existing.ID, err)
		}
	}
	recordKBActivity(ctx, s.audit, existing.TenantID, existing.KnowledgeBaseID, types.AuditActionKnowledgeUpdated,
		"knowledge", existing.ID, types.AuditOutcomeAccepted, map[string]any{
			"title": title, "source_type": "file", "file_type": fileType,
			"processing_status": "pending", "trigger": kbActivityTrigger(ctx),
		})
	return reparsed, nil
}

func knowledgeCustomNameRelocates(customFileName string) bool {
	return strings.Contains(strings.ReplaceAll(customFileName, "\\", "/"), "/")
}

// discardReplacementFile deletes a replacement file that did not become the
// knowledge's source. It re-reads the row first: a write reported as failed
// may still have committed, and a knowledge pointing at a deleted file is
// worse than an orphaned blob, so the file is kept whenever the row still
// (or possibly) references it.
func (s *knowledgeService) discardReplacementFile(ctx context.Context, fileSvc interfaces.FileService,
	tenantID uint64, knowledgeID, filePath string,
) {
	current, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil || (current != nil && current.FilePath == filePath) {
		logger.Warnf(ctx, "Keeping replacement file %s: knowledge %s may still reference it (lookup error: %v)",
			filePath, knowledgeID, err)
		return
	}
	if err := fileSvc.DeleteFile(ctx, filePath); err != nil {
		logger.Warnf(ctx, "Failed to delete discarded replacement file %s: %v", filePath, err)
	}
}

// mergeKnowledgeMetadata overlays updates on the stored metadata object, so
// entries the caller does not manage (e.g. process overrides) survive.
func mergeKnowledgeMetadata(current types.JSON, updates map[string]string) (types.JSON, error) {
	merged, err := current.Map()
	if err != nil {
		return nil, err
	}
	if merged == nil { // stored JSON null
		merged = map[string]interface{}{}
	}
	for k, v := range updates {
		if k == types.KnowledgeReparseMetadataKey {
			continue
		}
		merged[k] = v
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return types.JSON(b), nil
}
