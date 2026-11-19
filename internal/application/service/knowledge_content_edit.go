package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// editablePlainTextTypes lists file types whose stored source can be
// rewritten by the online content editor. Binary formats (docx/pdf/xlsx/
// pptx...) stay on the replace-upload path tracked by #2615.
var editablePlainTextTypes = map[string]bool{
	"md": true, "markdown": true, "txt": true,
}

// UpdateKnowledgeContent rewrites the stored source of a plain-text file
// knowledge (md/markdown/txt) with the submitted full text, then re-parses
// the knowledge so chunks, indexes, wiki and graph follow the new content.
// The upload itself is untouched: binary formats keep ReplaceKnowledgeFile.
func (s *knowledgeService) UpdateKnowledgeContent(ctx context.Context,
	knowledgeID string, content string,
) (*types.Knowledge, error) {
	existing, kb, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, knowledgeID)
	if err != nil {
		return nil, err
	}
	if kb != nil && kb.Type == types.KnowledgeBaseTypeFAQ {
		return nil, werrors.NewBadRequestError("FAQ 知识库不支持在线编辑文件原文")
	}
	if existing.Type != "file" || existing.FilePath == "" {
		return nil, werrors.NewBadRequestError("仅支持文件类型知识的原文在线编辑")
	}
	if !editablePlainTextTypes[strings.ToLower(existing.FileType)] {
		return nil, werrors.NewBadRequestError(
			"仅支持 md/markdown/txt 等纯文本文件的原文编辑，二进制格式请使用文件替换上传)")
	}
	if existing.ParseStatus == types.ParseStatusDeleting {
		return nil, werrors.NewBadRequestError("知识正在删除，无法编辑")
	}
	if err := s.checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, werrors.NewValidationError("内容不能为空")
	}
	if len([]rune(trimmed)) > manualContentMaxLength {
		return nil, werrors.NewValidationError(
			fmt.Sprintf("内容长度超出限制（最多%d个字符）", manualContentMaxLength))
	}

	digest := sha256.Sum256([]byte(trimmed))
	hash := hex.EncodeToString(digest[:])
	if hash == existing.FileHash {
		return existing, nil
	}

	cleanupCtx := context.WithoutCancel(ctx)
	fileSvc := s.resolveFileService(ctx, kb)
	newPath, err := fileSvc.SaveBytes(ctx, []byte(trimmed), existing.TenantID, existing.FileName, false)
	if err != nil {
		logger.Errorf(ctx, "Failed to store edited source for knowledge %s: %v", existing.ID, err)
		return nil, err
	}

	sourceColumns := map[string]interface{}{
		"file_size":     int64(len(trimmed)),
		"file_hash":     hash,
		"file_path":     newPath,
		"parse_status":  types.ParseStatusPending,
		"enable_status": "disabled",
		"error_message": "",
		"updated_at":    time.Now(),
	}
	if err := s.repo.UpdateKnowledgeColumns(ctx, existing.ID, sourceColumns); err != nil {
		logger.Errorf(ctx, "Failed to point knowledge %s at its edited source: %v", existing.ID, err)
		if delErr := fileSvc.DeleteFile(cleanupCtx, newPath); delErr != nil {
			logger.Warnf(ctx, "Failed to discard edited source %s: %v", newPath, delErr)
		}
		return nil, err
	}

	reparsed, err := s.reparseKnowledge(ctx, existing.ID, nil, true)
	if err != nil {
		logger.Errorf(ctx, "Reparse after content edit of knowledge %s failed, restoring source: %v",
			existing.ID, err)
		if rerr := s.repo.UpdateKnowledgeColumns(cleanupCtx, existing.ID, map[string]interface{}{
			"file_size": existing.FileSize, "file_hash": existing.FileHash,
			"file_path": existing.FilePath, "parse_status": types.ParseStatusFailed,
			"error_message": "Content edit failed; reparse to rebuild the index",
			"updated_at":    time.Now(),
		}); rerr != nil {
			logger.Errorf(ctx, "Failed to restore the source of knowledge %s: %v", existing.ID, rerr)
		}
		if delErr := fileSvc.DeleteFile(cleanupCtx, newPath); delErr != nil {
			logger.Warnf(ctx, "Failed to discard edited source %s: %v", newPath, delErr)
		}
		return nil, err
	}

	if existing.FilePath != newPath {
		oldFileSvc := s.resolveFileServiceForPath(cleanupCtx, kb, existing.FilePath)
		if err := oldFileSvc.DeleteFile(cleanupCtx, existing.FilePath); err != nil {
			logger.Warnf(ctx, "Failed to delete replaced source %s of knowledge %s: %v",
				existing.FilePath, existing.ID, err)
		}
	}
	recordKBActivity(ctx, s.audit, existing.TenantID, existing.KnowledgeBaseID, types.AuditActionKnowledgeUpdated,
		"knowledge", existing.ID, types.AuditOutcomeAccepted, map[string]any{
			"trigger": "content_edit", "file_type": existing.FileType, "processing_status": "pending",
		})
	return reparsed, nil
}
