package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// previewConversionTimeout bounds one docreader conversion. The first
	// preview of a large DOCX has to rasterize every embedded metafile, while
	// later previews come from the on-disk cache.
	previewConversionTimeout = 2 * time.Minute
	// previewSourceSizeLimit keeps a single conversion inside the gRPC message
	// limit (50 MiB by default) with room for the rewritten package. Larger
	// files keep the original preview path.
	previewSourceSizeLimit = 45 << 20
	// previewCacheVersion invalidates copies produced by older conversion
	// logic without touching the source hash.
	previewCacheVersion = "v1"
)

var (
	previewHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{32,64}$`)
	// previewConversionLocks de-duplicates concurrent first previews of the
	// same file inside one process. Replicas still convert independently;
	// the on-disk cache makes later requests cheap.
	previewConversionLocks sync.Map
)

// GetKnowledgePreviewFile returns the bytes the in-browser previewer should
// render. EMF/WMF media in DOCX files cannot be decoded by browsers, so when
// the feature is enabled the file is rewritten once by the docreader (vector
// media -> PNG) and cached by content hash. Every failure path falls back to
// the original upload, keeping the pre-change preview behavior.
func (s *knowledgeService) GetKnowledgePreviewFile(
	ctx context.Context, id string,
) (*types.KnowledgePreviewFile, error) {
	if !s.previewConversionEnabled() {
		return s.originalPreviewFile(ctx, id, "")
	}

	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok {
		return nil, fmt.Errorf("tenant ID missing from context")
	}
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if knowledge.IsManual() || !strings.EqualFold(filepath.Ext(knowledge.FileName), ".docx") {
		return s.originalPreviewFile(ctx, id, "")
	}
	if knowledge.FileSize > previewSourceSizeLimit {
		return s.originalPreviewFile(ctx, id, "source file too large to convert")
	}

	cacheKey := ""
	if previewHashPattern.MatchString(knowledge.FileHash) {
		cacheKey = previewCacheVersion + "-" + strings.ToLower(knowledge.FileHash)
		if cached, ok := openCachedPreview(s.previewCachePath(cacheKey)); ok {
			return &types.KnowledgePreviewFile{
				Reader:    cached,
				Filename:  knowledge.FileName,
				Converted: true,
			}, nil
		}
	}

	kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	file, err := s.resolveFileServiceForPath(ctx, kb, knowledge.FilePath).GetFile(ctx, knowledge.FilePath)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, previewSourceSizeLimit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		logger.Warnf(ctx, "preview: closing source file failed: %v", closeErr)
	}
	if len(data) > previewSourceSizeLimit {
		return s.originalPreviewFile(ctx, id, "source file too large to convert")
	}
	if cacheKey == "" {
		cacheKey = previewCacheVersion + "-" + previewContentHash(data)
		if cached, ok := openCachedPreview(s.previewCachePath(cacheKey)); ok {
			return &types.KnowledgePreviewFile{
				Reader:    cached,
				Filename:  knowledge.FileName,
				Converted: true,
			}, nil
		}
	}

	if !docxHasVectorMedia(data) {
		return &types.KnowledgePreviewFile{
			Reader:   newBytesPreview(data),
			Filename: knowledge.FileName,
		}, nil
	}

	unlock := lockPreviewConversion(cacheKey)
	defer unlock()

	// Another request may have finished the conversion while we waited.
	if cached, ok := openCachedPreview(s.previewCachePath(cacheKey)); ok {
		return &types.KnowledgePreviewFile{
			Reader:    cached,
			Filename:  knowledge.FileName,
			Converted: true,
		}, nil
	}

	converter, ok := s.documentReader.(interfaces.PreviewConverter)
	if !ok {
		logger.Warnf(ctx,
			"preview: docreader transport does not support preview conversion, serving original")
		return &types.KnowledgePreviewFile{
			Reader:         newBytesPreview(data),
			Filename:       knowledge.FileName,
			FallbackReason: "docreader transport does not support preview conversion",
		}, nil
	}

	convertCtx, cancel := context.WithTimeout(ctx, previewConversionTimeout)
	defer cancel()
	result, err := converter.ConvertPreview(convertCtx, &types.ConvertPreviewRequest{
		FileContent: data,
		FileName:    knowledge.FileName,
		FileType:    "docx",
		RequestID:   knowledge.ID,
	})
	if err != nil {
		logger.Warnf(ctx, "preview: docreader conversion failed for %s: %v", knowledge.ID, err)
		return &types.KnowledgePreviewFile{
			Reader:         newBytesPreview(data),
			Filename:       knowledge.FileName,
			FallbackReason: "docreader conversion failed",
		}, nil
	}
	if result.Error != "" {
		logger.Warnf(ctx, "preview: docreader returned conversion error for %s: %s",
			knowledge.ID, result.Error)
		return &types.KnowledgePreviewFile{
			Reader:         newBytesPreview(data),
			Filename:       knowledge.FileName,
			FallbackReason: "docreader conversion failed",
		}, nil
	}
	for _, warning := range result.Warnings {
		logger.Warnf(ctx, "preview: conversion warning for %s: %s", knowledge.ID, warning)
	}
	if !result.Converted || len(result.FileContent) == 0 {
		logger.Infof(ctx, "preview: no converted copy for %s (%s)", knowledge.ID, result.SkippedReason)
		return &types.KnowledgePreviewFile{
			Reader:   newBytesPreview(data),
			Filename: knowledge.FileName,
		}, nil
	}

	if writeErr := writePreviewCache(s.previewCachePath(cacheKey), result.FileContent); writeErr != nil {
		// A cache miss only costs another conversion; the response is fine.
		logger.Warnf(ctx, "preview: caching converted copy failed: %v", writeErr)
	}
	return &types.KnowledgePreviewFile{
		Reader:    newBytesPreview(result.FileContent),
		Filename:  knowledge.FileName,
		Converted: true,
	}, nil
}

// originalPreviewFile preserves the pre-change streaming path.
func (s *knowledgeService) originalPreviewFile(
	ctx context.Context, id string, fallbackReason string,
) (*types.KnowledgePreviewFile, error) {
	reader, filename, err := s.GetKnowledgeFile(ctx, id)
	if err != nil {
		return nil, err
	}
	return &types.KnowledgePreviewFile{
		Reader:         reader,
		Filename:       filename,
		FallbackReason: fallbackReason,
	}, nil
}

func (s *knowledgeService) previewConversionEnabled() bool {
	return s.config != nil && s.config.Preview != nil && s.config.Preview.DocxEMF
}

func (s *knowledgeService) previewCachePath(key string) string {
	dir := ""
	if s.config != nil && s.config.Preview != nil {
		dir = strings.TrimSpace(s.config.Preview.CacheDir)
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "weknora-docx-preview")
	}
	return filepath.Join(dir, key+".docx")
}

// docxHasVectorMedia only inspects the zip directory: it never inflates a
// member, so a crafted archive cannot make this check expensive.
func docxHasVectorMedia(data []byte) bool {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() || !strings.HasPrefix(entry.Name, "word/media/") {
			continue
		}
		switch strings.ToLower(path.Ext(entry.Name)) {
		case ".emf", ".wmf":
			return true
		}
	}
	return false
}

func previewContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func openCachedPreview(cachePath string) (io.ReadCloser, bool) {
	file, err := os.Open(cachePath)
	if err != nil {
		return nil, false
	}
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		_ = file.Close()
		return nil, false
	}
	return file, true
}

func writePreviewCache(cachePath string, data []byte) error {
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".preview-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, cachePath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func lockPreviewConversion(key string) func() {
	value, _ := previewConversionLocks.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// bytesReadSeekCloser lets filetransport.Serve use Range/HEAD for the
// in-memory original fallback, matching the seekable storage path.
type bytesReadSeekCloser struct {
	*bytes.Reader
}

func newBytesPreview(data []byte) io.ReadCloser {
	return &bytesReadSeekCloser{Reader: bytes.NewReader(data)}
}

func (b *bytesReadSeekCloser) Close() error { return nil }
