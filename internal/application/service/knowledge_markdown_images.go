package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	markdownImageRefPattern = regexp.MustCompile(`!\[(.*?)\]\(([^()\n]*(?:\([^)]*\)[^()\n]*)*)\)`)
	uriSchemePattern        = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

type markdownLocalImageRef struct {
	originalRef string
	fileName    string
}

func (s *knowledgeService) attachMarkdownLocalImages(
	ctx context.Context,
	kb *types.KnowledgeBase,
	payload types.DocumentProcessPayload,
	result *types.ReadResult,
) {
	if s.repo == nil || kb == nil || result == nil || !isMarkdownFileType(payload.FileType, payload.FileName) {
		return
	}

	refs := extractMarkdownLocalImageRefs(result.MarkdownContent, payload.FileName)
	if len(refs) == 0 {
		return
	}

	tenantID := payload.TenantID
	if tenantID == 0 {
		tenantID, _ = ctx.Value(types.TenantIDContextKey).(uint64)
	}

	knowledges, err := s.repo.ListKnowledgeByFileNames(ctx, tenantID, kb.ID, uniqueMarkdownImageFileNames(refs))
	if err != nil {
		logger.Warnf(ctx, "Failed to look up local markdown images for knowledge base %s: %v", kb.ID, err)
		return
	}

	knowledgeByName := make(map[string]*types.Knowledge, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge == nil || knowledge.FilePath == "" || !IsImageType(strings.ToLower(knowledge.FileType)) {
			continue
		}
		if _, exists := knowledgeByName[knowledge.FileName]; !exists {
			knowledgeByName[knowledge.FileName] = knowledge
		}
	}

	existingRefs := make(map[string]struct{}, len(result.ImageRefs))
	for _, ref := range result.ImageRefs {
		existingRefs[ref.OriginalRef] = struct{}{}
	}

	resolvedCount := 0
	for _, ref := range refs {
		if _, exists := existingRefs[ref.originalRef]; exists {
			continue
		}

		imageKnowledge := knowledgeByName[ref.fileName]
		if imageKnowledge == nil {
			continue
		}

		data, err := s.readKnowledgeImageBytes(ctx, kb, imageKnowledge)
		if err != nil {
			logger.Warnf(ctx, "Failed to read local markdown image %s: %v", ref.fileName, err)
			continue
		}
		if len(data) == 0 {
			continue
		}

		result.ImageRefs = append(result.ImageRefs, types.ImageRef{
			Filename:    path.Base(imageKnowledge.FileName),
			OriginalRef: ref.originalRef,
			MimeType:    mimeTypeForImageFile(imageKnowledge.FileName, data),
			ImageData:   data,
			IsOriginal:  true,
		})
		existingRefs[ref.originalRef] = struct{}{}
		resolvedCount++
	}

	if resolvedCount > 0 {
		logger.Infof(ctx, "Resolved %d local markdown images for knowledge %s", resolvedCount, payload.KnowledgeID)
	}
}

func (s *knowledgeService) readKnowledgeImageBytes(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
) ([]byte, error) {
	reader, err := s.resolveFileServiceForPath(ctx, kb, knowledge.FilePath).GetFile(ctx, knowledge.FilePath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func extractMarkdownLocalImageRefs(markdown string, baseFileName string) []markdownLocalImageRef {
	matches := markdownImageRefPattern.FindAllStringSubmatchIndex(markdown, -1)
	refs := make([]markdownLocalImageRef, 0, len(matches))
	for _, match := range matches {
		originalRef := strings.TrimSpace(markdown[match[4]:match[5]])
		fileName, ok := normalizeMarkdownLocalImagePath(originalRef, baseFileName)
		if !ok || !IsImageType(strings.ToLower(getFileType(fileName))) {
			continue
		}
		refs = append(refs, markdownLocalImageRef{
			originalRef: originalRef,
			fileName:    fileName,
		})
	}
	return refs
}

func normalizeMarkdownLocalImagePath(rawRef string, baseFileName string) (string, bool) {
	ref := strings.TrimSpace(rawRef)
	if strings.HasPrefix(ref, "<") && strings.HasSuffix(ref, ">") && len(ref) > 1 {
		ref = strings.TrimSpace(ref[1 : len(ref)-1])
	}
	if ref == "" {
		return "", false
	}

	ref = strings.ReplaceAll(ref, "\\", "/")
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "//") || uriSchemePattern.MatchString(ref) {
		return "", false
	}
	if idx := strings.IndexAny(ref, "?#"); idx >= 0 {
		ref = ref[:idx]
	}
	if ref == "" {
		return "", false
	}

	if decoded, err := url.PathUnescape(ref); err == nil {
		ref = decoded
	}
	ref = strings.ReplaceAll(ref, "\\", "/")
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "//") || uriSchemePattern.MatchString(ref) {
		return "", false
	}

	baseDir := path.Dir(strings.ReplaceAll(baseFileName, "\\", "/"))
	if baseDir == "." {
		baseDir = ""
	}

	fileName := path.Clean(path.Join(baseDir, ref))
	if fileName == "." || fileName == ".." || strings.HasPrefix(fileName, "../") || strings.HasPrefix(fileName, "/") {
		return "", false
	}
	return fileName, true
}

func uniqueMarkdownImageFileNames(refs []markdownLocalImageRef) []string {
	seen := make(map[string]struct{}, len(refs))
	fileNames := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, exists := seen[ref.fileName]; exists {
			continue
		}
		seen[ref.fileName] = struct{}{}
		fileNames = append(fileNames, ref.fileName)
	}
	return fileNames
}

func isMarkdownFileType(fileType string, fileName string) bool {
	ft := strings.ToLower(strings.TrimPrefix(fileType, "."))
	if ft == "" || ft == "unknown" {
		ft = strings.ToLower(getFileType(fileName))
	}
	return ft == "md" || ft == "markdown"
}

func mimeTypeForImageFile(fileName string, data []byte) string {
	switch strings.ToLower(strings.TrimPrefix(path.Ext(fileName), ".")) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "bmp":
		return "image/bmp"
	case "svg":
		return "image/svg+xml"
	case "tif", "tiff":
		return "image/tiff"
	default:
		return http.DetectContentType(data)
	}
}
