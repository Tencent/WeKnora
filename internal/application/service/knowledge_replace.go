package service

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"time"

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
//  2. point the row at it with a single UPDATE of the source columns;
//  3. ReparseKnowledge cleans the old chunks/index/graph and enqueues parsing;
//  4. only after that succeeds, delete the old file.
//
// If step 2 fails the new file is discarded. If step 3 fails the previous
// source columns are restored (marked failed, since cleanup may already have
// removed the old index) and the new file is discarded. A file is never
// deleted while the row may still reference it.
//
// Identical content under the same name and folder is not re-parsed: changed
// metadata is persisted and a DuplicateKnowledgeError carrying the knowledge
// is returned, mirroring CreateKnowledgeFromFile's duplicate contract.
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
	if existing.Type != "file" || existing.FilePath == "" {
		return nil, werrors.NewBadRequestError("only file knowledge can have its file replaced")
	}
	if existing.ParseStatus == types.ParseStatusDeleting {
		return nil, werrors.NewBadRequestError("knowledge is being deleted")
	}
	if err := s.checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}

	// Same naming rules as CreateKnowledgeFromFile: a path-qualified custom name
	// places the knowledge in that folder; without one the folder is kept.
	fileName, folderPath := file.Filename, existing.FolderPath
	if customFileName != "" {
		folderPath, fileName = types.SplitKnowledgeRelativePath(customFileName)
		if fileName == "" {
			fileName = file.Filename
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

	hash, err := calculateFileHash(file)
	if err != nil {
		return nil, err
	}
	newMetadata, err := mergeKnowledgeMetadata(existing.Metadata, metadata)
	if err != nil {
		return nil, err
	}

	if hash == existing.FileHash && safeFileName == existing.FileName && folderPath == existing.FolderPath {
		current := existing.GetMetadata()
		for k, v := range metadata {
			if current[k] != v {
				if err := s.repo.UpdateKnowledgeColumn(ctx, existing.ID, "metadata", newMetadata); err != nil {
					return nil, err
				}
				existing.Metadata = newMetadata
				break
			}
		}
		return existing, types.NewDuplicateFileError(existing)
	}

	if tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant); tenant != nil &&
		tenant.StorageQuota > 0 && tenant.StorageUsed >= tenant.StorageQuota {
		return nil, types.NewStorageQuotaExceededError()
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
	if err := s.repo.UpdateKnowledgeColumns(ctx, existing.ID, map[string]interface{}{
		"title":       title,
		"file_name":   safeFileName,
		"folder_path": folderPath,
		"file_type":   fileType,
		"file_size":   file.Size,
		"file_hash":   hash,
		"file_path":   newPath,
		"metadata":    newMetadata,
		"updated_at":  time.Now(),
	}); err != nil {
		logger.Errorf(ctx, "Failed to point knowledge %s at its replacement file: %v", existing.ID, err)
		s.discardReplacementFile(cleanupCtx, fileSvc, existing.TenantID, existing.ID, newPath)
		return nil, err
	}

	reparsed, err := s.ReparseKnowledge(ctx, existing.ID, nil)
	if err != nil {
		logger.Errorf(ctx, "Reparse after replacing the file of knowledge %s failed, restoring source: %v",
			existing.ID, err)
		if rerr := s.repo.UpdateKnowledgeColumns(cleanupCtx, existing.ID, map[string]interface{}{
			"title":         existing.Title,
			"file_name":     existing.FileName,
			"folder_path":   existing.FolderPath,
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
		}
		s.discardReplacementFile(cleanupCtx, fileSvc, existing.TenantID, existing.ID, newPath)
		return nil, err
	}

	if existing.FilePath != newPath {
		if err := fileSvc.DeleteFile(cleanupCtx, existing.FilePath); err != nil {
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
		merged[k] = v
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return types.JSON(b), nil
}
