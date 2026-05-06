package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// isValidFileType checks if a file type is supported
func isValidFileType(filename string) bool {
	switch strings.ToLower(getFileType(filename)) {
	case "pdf", "txt", "docx", "doc", "md", "markdown", "png", "jpg", "jpeg", "gif", "csv", "xlsx", "xls", "pptx", "ppt", "json",
		"mp3", "wav", "m4a", "flac", "ogg":
		return true
	default:
		return false
	}
}

// getFileType extracts the file extension from a filename
func getFileType(filename string) string {
	ext := strings.Split(filename, ".")
	if len(ext) < 2 {
		return "unknown"
	}
	return ext[len(ext)-1]
}

// isValidURL verifies if a URL is valid
// isValidURL 检查URL是否有效
func isValidURL(url string) bool {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return true
	}
	return false
}

// calculateFileHash calculates MD5 hash of a file
func calculateFileHash(file *multipart.FileHeader) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	// Reset file pointer for subsequent operations
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func calculateStr(strList ...string) string {
	h := md5.New()
	input := strings.Join(strList, "")
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *knowledgeService) getVLMConfig(ctx context.Context, kb *types.KnowledgeBase) (*types.DocParserVLMConfig, error) {
	if kb == nil {
		return nil, nil
	}
	// 兼容老版本：直接使用 ModelName 和 BaseURL
	if kb.VLMConfig.ModelName != "" && kb.VLMConfig.BaseURL != "" {
		return &types.DocParserVLMConfig{
			ModelName:     kb.VLMConfig.ModelName,
			BaseURL:       kb.VLMConfig.BaseURL,
			APIKey:        kb.VLMConfig.APIKey,
			InterfaceType: kb.VLMConfig.InterfaceType,
		}, nil
	}

	// 新版本：未启用或无模型ID时返回nil
	if !kb.VLMConfig.Enabled || kb.VLMConfig.ModelID == "" {
		return nil, nil
	}

	model, err := s.modelService.GetModelByID(ctx, kb.VLMConfig.ModelID)
	if err != nil {
		return nil, err
	}

	interfaceType := model.Parameters.InterfaceType
	if interfaceType == "" {
		interfaceType = "openai"
	}

	return &types.DocParserVLMConfig{
		ModelName:     model.Name,
		BaseURL:       model.Parameters.BaseURL,
		APIKey:        model.Parameters.APIKey,
		InterfaceType: interfaceType,
	}, nil
}

func (s *knowledgeService) buildStorageConfig(ctx context.Context, kb *types.KnowledgeBase) *types.DocParserStorageConfig {
	provider := kb.GetStorageProvider()
	if provider == "" {
		provider = "local"
	}

	// Backward compatibility: if legacy cos_config has full params for the chosen provider, use them.
	sc := &kb.StorageConfig
	hasKBFull := false
	switch provider {
	case "cos":
		hasKBFull = sc.SecretID != "" && sc.BucketName != ""
	case "minio":
		hasKBFull = sc.BucketName != ""
	case "local":
		hasKBFull = false
	}

	if hasKBFull {
		logger.Infof(ctx, "[storage] buildStorageConfig use legacy kb config: kb=%s provider=%s bucket=%s path_prefix=%s",
			kb.ID, provider, sc.BucketName, sc.PathPrefix)
		return &types.DocParserStorageConfig{
			Provider:        strings.ToUpper(provider),
			Region:          sc.Region,
			BucketName:      sc.BucketName,
			AccessKeyID:     sc.SecretID,
			SecretAccessKey: sc.SecretKey,
			AppID:           sc.AppID,
			PathPrefix:      sc.PathPrefix,
		}
	}

	// Merge from tenant's StorageEngineConfig.
	var out types.DocParserStorageConfig
	out.Provider = strings.ToUpper(provider)

	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant != nil && tenant.StorageEngineConfig != nil {
		sec := tenant.StorageEngineConfig
		if sec.DefaultProvider != "" && provider == "" {
			provider = strings.ToLower(strings.TrimSpace(sec.DefaultProvider))
			out.Provider = strings.ToUpper(provider)
		}
		switch provider {
		case "local":
			if sec.Local != nil {
				out.PathPrefix = sec.Local.PathPrefix
			}
		case "minio":
			if sec.MinIO != nil {
				out.BucketName = sec.MinIO.BucketName
				out.PathPrefix = sec.MinIO.PathPrefix
				if sec.MinIO.Mode == "remote" {
					out.Endpoint = sec.MinIO.Endpoint
					out.AccessKeyID = sec.MinIO.AccessKeyID
					out.SecretAccessKey = sec.MinIO.SecretAccessKey
				} else {
					out.Endpoint = os.Getenv("MINIO_ENDPOINT")
					out.AccessKeyID = os.Getenv("MINIO_ACCESS_KEY_ID")
					out.SecretAccessKey = os.Getenv("MINIO_SECRET_ACCESS_KEY")
				}
			}
		case "cos":
			if sec.COS != nil {
				out.Region = sec.COS.Region
				out.BucketName = sec.COS.BucketName
				out.AccessKeyID = sec.COS.SecretID
				out.SecretAccessKey = sec.COS.SecretKey
				out.AppID = sec.COS.AppID
				out.PathPrefix = sec.COS.PathPrefix
			}
		}
	}

	logger.Infof(ctx, "[storage] buildStorageConfig use merged tenant/global config: kb=%s provider=%s bucket=%s path_prefix=%s endpoint=%s",
		kb.ID, strings.ToLower(out.Provider), out.BucketName, out.PathPrefix, out.Endpoint)
	return &out
}

// resolveFileService returns the FileService for the given knowledge base,
// based on the KB's StorageProviderConfig (or legacy StorageConfig.Provider) and the tenant's StorageEngineConfig.
// Falls back to the global fileSvc when no tenant-level storage config is found.
func (s *knowledgeService) resolveFileService(ctx context.Context, kb *types.KnowledgeBase) interfaces.FileService {
	if kb == nil {
		logger.Infof(ctx, "[storage] resolveFileService fallback default: kb=nil")
		return s.fileSvc
	}

	provider := kb.GetStorageProvider()

	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if provider == "" && tenant != nil && tenant.StorageEngineConfig != nil {
		provider = strings.ToLower(strings.TrimSpace(tenant.StorageEngineConfig.DefaultProvider))
	}

	if provider == "" || tenant == nil || tenant.StorageEngineConfig == nil {
		logger.Infof(ctx, "[storage] resolveFileService fallback default: kb=%s provider=%q tenant_cfg=%v",
			kb.ID, provider, tenant != nil && tenant.StorageEngineConfig != nil)
		return s.fileSvc
	}

	sec := tenant.StorageEngineConfig
	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	svc, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(provider, sec, baseDir)
	if err != nil {
		logger.Errorf(ctx, "Failed to create %s file service from tenant config: %v, falling back to default", provider, err)
		return s.fileSvc
	}
	logger.Infof(ctx, "[storage] resolveFileService selected: kb=%s provider=%s", kb.ID, resolvedProvider)
	return svc
}

// resolveFileServiceForPath is like resolveFileService but adds a safety check:
// if the resolved provider doesn't match what the filePath implies, fall back to
// the provider inferred from the file path. This protects historical data when
// tenant/KB config changes but files were stored under the old provider.
func (s *knowledgeService) resolveFileServiceForPath(ctx context.Context, kb *types.KnowledgeBase, filePath string) interfaces.FileService {
	svc := s.resolveFileService(ctx, kb)
	if filePath == "" {
		return svc
	}

	inferred := types.InferStorageFromFilePath(filePath)
	if inferred == "" {
		return svc
	}

	configured := kb.GetStorageProvider()
	if configured == "" {
		tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		if tenant != nil && tenant.StorageEngineConfig != nil {
			configured = strings.ToLower(strings.TrimSpace(tenant.StorageEngineConfig.DefaultProvider))
		}
	}
	if configured == "" {
		configured = strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
	}

	if configured != "" && configured != inferred {
		logger.Warnf(ctx, "[storage] FilePath format mismatch: configured=%s inferred=%s filePath=%s, using global fallback",
			configured, inferred, filePath)
		return s.fileSvc
	}
	return svc
}

func IsImageType(fileType string) bool {
	switch fileType {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "tiff":
		return true
	default:
		return false
	}
}

// IsAudioType checks if a file type is an audio format
func IsAudioType(fileType string) bool {
	switch strings.ToLower(fileType) {
	case "mp3", "wav", "m4a", "flac", "ogg":
		return true
	default:
		return false
	}
}

// IsVideoType checks if a file type is a video format
func IsVideoType(fileType string) bool {
	switch strings.ToLower(fileType) {
	case "mp4", "mov", "avi", "mkv", "webm", "wmv", "flv":
		return true
	default:
		return false
	}
}

// downloadFileFromURL downloads a remote file to a temp file and returns its binary content.
// payloadFileName and payloadFileType are in/out pointers: if they point to an empty string,
// the function resolves the value from Content-Disposition / URL path and writes it back.
// It does NOT perform SSRF validation — callers are responsible for that.
func downloadFileFromURL(ctx context.Context, fileURL string, payloadFileName, payloadFileType *string) ([]byte, error) {
	httpClient := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for file URL: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote server returned status %d", resp.StatusCode)
	}

	// Reject oversized files early via Content-Length
	if contentLength := resp.ContentLength; contentLength > maxFileURLSize {
		return nil, fmt.Errorf("file size %d bytes exceeds limit of %d bytes (10MB)", contentLength, maxFileURLSize)
	}

	// Resolve fileName: payload > Content-Disposition > URL path
	if *payloadFileName == "" {
		if cd := resp.Header.Get("Content-Disposition"); cd != "" {
			*payloadFileName = extractFileNameFromContentDisposition(cd)
		}
	}
	if *payloadFileName == "" {
		*payloadFileName = extractFileNameFromURL(fileURL)
	}
	if *payloadFileType == "" && *payloadFileName != "" {
		*payloadFileType = getFileType(*payloadFileName)
	}

	// Stream response body into a temp file, capped at maxFileURLSize
	tmpFile, err := os.CreateTemp("", "weknora-fileurl-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	limiter := &io.LimitedReader{R: resp.Body, N: maxFileURLSize + 1}
	written, err := io.Copy(tmpFile, limiter)
	tmpFile.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	if written > maxFileURLSize {
		return nil, fmt.Errorf("file size exceeds limit of 10MB")
	}

	contentBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp file: %w", err)
	}

	return contentBytes, nil
}
