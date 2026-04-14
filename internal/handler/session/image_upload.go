package session

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	maxImageSize   = 10 << 20 // 10MB per image
	maxImagesCount = 5
)

// saveImageAttachments decodes base64 images from the request and saves them to
// storage. The images slice is mutated in place: URL is populated.
// This is always called when images are present. VLM analysis is handled
// separately (either in the pipeline rewrite step for RAG paths, or via
// analyzeImageAttachments for pure chat paths with non-vision models).
func (h *Handler) saveImageAttachments(ctx context.Context, images []ImageAttachment, tenantID uint64, storageProvider string) error {
	if len(images) == 0 {
		return nil
	}
	if len(images) > maxImagesCount {
		return fmt.Errorf("too many images, max %d", maxImagesCount)
	}

	fileSvc := h.resolveImageFileService(ctx, storageProvider)

	for i := range images {
		img := &images[i]
		if img.Data == "" {
			continue
		}

		imgBytes, ext, err := decodeDataURI(img.Data)
		if err != nil {
			return fmt.Errorf("decode image %d: %w", i, err)
		}
		if len(imgBytes) > maxImageSize {
			return fmt.Errorf("image %d too large (%d bytes, max %d)", i, len(imgBytes), maxImageSize)
		}

		storedName := fmt.Sprintf("chat-images/%s%s", uuid.New().String(), ext)
		fileURL, err := fileSvc.SaveBytes(ctx, imgBytes, tenantID, storedName, false)
		if err != nil {
			return fmt.Errorf("save image %d: %w", i, err)
		}
		img.URL = fileURL
	}

	return nil
}

// analyzeImageAttachments runs VLM analysis on saved images and populates Caption.
// Used as a fallback for pure chat paths where the pipeline rewrite step won't run.
// For RAG paths, image analysis is handled in the pipeline rewrite step instead.
func (h *Handler) analyzeImageAttachments(ctx context.Context, images []ImageAttachment, vlmModelID string, userQuery string) {
	if len(images) == 0 || vlmModelID == "" {
		return
	}

	vlmModel, err := h.modelService.GetVLMModel(ctx, vlmModelID)
	if err != nil {
		logger.Warnf(ctx, "No VLM model available for image analysis, skipping: %v", err)
		return
	}

	for i := range images {
		img := &images[i]
		if img.Data == "" {
			continue
		}
		imgBytes, _, decErr := decodeDataURI(img.Data)
		if decErr != nil {
			logger.Warnf(ctx, "Failed to decode image %d for VLM analysis: %v", i, decErr)
			continue
		}
		prompt := buildImageAnalysisPrompt(userQuery)
		analysis, analysisErr := vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)
		if analysisErr != nil {
			logger.Warnf(ctx, "VLM analysis failed for image %d: %v", i, analysisErr)
		} else {
			img.Caption = analysis
		}
	}
}

// buildImageAnalysisPrompt generates a context-aware VLM prompt based on the
// user's question. Instead of doing generic OCR + Caption separately, we do a
// single analysis call that is tailored to the user's intent.
func buildImageAnalysisPrompt(userQuery string) string {
	if strings.TrimSpace(userQuery) == "" {
		return "请分析这张图片的内容。如果包含文字，请提取关键文字信息；如果是自然图片，请描述其主要内容。用简洁的中文回答。"
	}
	return fmt.Sprintf(
		"用户的问题是：%s\n\n请分析图片中与用户问题相关的内容。"+
			"如果图片包含文字/文档/表格，请提取与问题相关的关键信息。"+
			"如果是自然图片/截图/图表，请描述与问题相关的视觉内容。"+
			"用简洁的中文回答，只输出分析结果。",
		userQuery,
	)
}

func decodeDataURI(dataURI string) ([]byte, string, error) {
	if !strings.HasPrefix(dataURI, "data:") {
		return nil, "", fmt.Errorf("not a data URI")
	}
	idx := strings.Index(dataURI, ";base64,")
	if idx < 0 {
		return nil, "", fmt.Errorf("unsupported data URI encoding (expected base64)")
	}
	mimeType := dataURI[5:idx]
	decoded, err := base64.StdEncoding.DecodeString(dataURI[idx+8:])
	if err != nil {
		return nil, "", fmt.Errorf("base64 decode: %w", err)
	}
	ext := mimeToExt(mimeType)
	return decoded, ext, nil
}

func mimeToExt(mime string) string {
	switch strings.ToLower(mime) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func (h *Handler) resolveImageFileService(ctx context.Context, storageProvider string) interfaces.FileService {
	if strings.TrimSpace(storageProvider) == "" {
		return h.fileService
	}

	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil || tenant.StorageEngineConfig == nil {
		return h.fileService
	}

	svc, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(storageProvider, tenant.StorageEngineConfig, "")
	if err != nil {
		logger.Warnf(ctx, "[image-storage] failed to create %s file service: %v, fallback to default", storageProvider, err)
		return h.fileService
	}
	logger.Infof(ctx, "[image-storage] using provider=%s for image uploads", resolvedProvider)
	return svc
}
