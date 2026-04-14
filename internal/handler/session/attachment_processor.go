package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

const (
	// maxTextFileLines is the line limit for inline text content; excess lines are truncated.
	maxTextFileLines = 500
	// textFileExtensions lists plain-text extensions handled by the line-based reader.
	textFileExtensions = ".txt,.md,.markdown,.json,.xml,.yaml,.yml,.csv,.log"
)

// AttachmentProcessor saves uploaded file attachments and extracts their text content
// for injection into the LLM prompt.
type AttachmentProcessor struct {
	fileService    interfaces.FileService
	documentReader interfaces.DocumentReader
	imageResolver  *docparser.ImageResolver
	modelService   interfaces.ModelService // used to obtain the ASR model
}

// NewAttachmentProcessor creates an AttachmentProcessor with the given dependencies.
func NewAttachmentProcessor(
	fileService interfaces.FileService,
	documentReader interfaces.DocumentReader,
	imageResolver *docparser.ImageResolver,
	modelService interfaces.ModelService,
) *AttachmentProcessor {
	return &AttachmentProcessor{
		fileService:    fileService,
		documentReader: documentReader,
		imageResolver:  imageResolver,
		modelService:   modelService,
	}
}

// ProcessAttachment validates, saves, and extracts content from a single uploaded file.
// Content extraction is attempted for all supported types; errors are non-fatal (logged as warnings).
func (p *AttachmentProcessor) ProcessAttachment(
	ctx context.Context,
	data []byte,
	fileName string,
	fileSize int64,
	tenantID uint64,
	asrModelID string, // optional; enables audio transcription when set
) (*types.MessageAttachment, error) {
	logger.Infof(ctx, "processing attachment: fileName=%s, fileSize=%d", secutils.SanitizeForLog(fileName), fileSize)

	// Validate filename (injection / path-traversal checks)
	safeFileName, isValid := secutils.ValidateInput(fileName)
	if !isValid {
		return nil, fmt.Errorf("invalid characters in file name")
	}

	baseName, err := secutils.SafeFileName(safeFileName)
	if err != nil {
		return nil, fmt.Errorf("unsafe file name: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(baseName))
	if ext == "" {
		ext = ".txt"
	}

	if !isValidFileType(baseName) {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	uniqueFileName := fmt.Sprintf("attachment_%s%s", uuid.New().String()[:12], ext)

	storageURL, err := p.fileService.SaveBytes(ctx, data, tenantID, uniqueFileName, false)
	if err != nil {
		return nil, fmt.Errorf("failed to save attachment: %w", err)
	}

	attachment := &types.MessageAttachment{
		URL:      storageURL,
		FileName: baseName,
		FileType: ext,
		FileSize: fileSize,
	}

	// Extract text content based on file type; errors are non-fatal.
	if p.isTextFile(ext) {
		if err := p.processTextFile(ctx, data, attachment); err != nil {
			logger.Warnf(ctx, "text file processing failed: %v", err)
			attachment.Content = fmt.Sprintf("<error><message>Failed to process text file</message><details>%v</details></error>", err)
		}
	} else if docparser.IsAudioFormat(ext) {
		if err := p.processAudioFile(ctx, data, baseName, attachment, asrModelID); err != nil {
			logger.Warnf(ctx, "audio transcription failed: %v, keeping placeholder", err)
			attachment.Content = fmt.Sprintf("<error><message>Failed to transcribe audio file</message><details>%v</details></error>", err)
		}
	} else if docparser.IsSimpleFormat(ext) {
		if err := p.processWithDocParser(ctx, data, baseName, ext, attachment, tenantID); err != nil {
			logger.Warnf(ctx, "SimpleFormatReader failed: %v", err)
			attachment.Content = fmt.Sprintf("<error><message>Failed to parse document</message><details>%v</details></error>", err)
		}
	} else {
		if err := p.processWithDocumentReader(ctx, data, baseName, ext, attachment, tenantID); err != nil {
			logger.Warnf(ctx, "DocumentReader failed: %v, keeping metadata only", err)
			attachment.Content = fmt.Sprintf("<error><message>Failed to read document</message><details>%v</details></error>", err)
		}
	}

	logger.Infof(ctx, "attachment processed: fileName=%s, truncated=%v, contentLen=%d",
		secutils.SanitizeForLog(baseName), attachment.IsTruncated, len(attachment.Content))

	return attachment, nil
}

// isTextFile reports whether ext is a plain-text extension handled line-by-line.
func (p *AttachmentProcessor) isTextFile(ext string) bool {
	return strings.Contains(textFileExtensions, ext)
}

// processTextFile reads plain-text content line by line, truncating at maxTextFileLines.
func (p *AttachmentProcessor) processTextFile(ctx context.Context, data []byte, attachment *types.MessageAttachment) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var lines []string
	lineCount := 0

	for scanner.Scan() {
		lineCount++
		if lineCount <= maxTextFileLines {
			lines = append(lines, scanner.Text())
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read file content: %w", err)
	}

	attachment.LineCount = lineCount
	attachment.Content = strings.Join(lines, "\n")

	if lineCount > maxTextFileLines {
		attachment.IsTruncated = true
		logger.Infof(ctx, "text file truncated: total=%d, kept=%d", lineCount, maxTextFileLines)
	}

	return nil
}

// processWithDocParser extracts content via SimpleFormatReader (md, csv, json, images, etc.).
func (p *AttachmentProcessor) processWithDocParser(
	ctx context.Context,
	data []byte,
	fileName string,
	fileType string,
	attachment *types.MessageAttachment,
	tenantID uint64,
) error {
	reader := &docparser.SimpleFormatReader{}
	result, err := reader.Read(ctx, &types.ReadRequest{
		FileContent: data,
		FileName:    fileName,
		FileType:    fileType,
	})
	if err != nil {
		return fmt.Errorf("SimpleFormatReader failed: %w", err)
	}

	// Resolve embedded image refs to storage URLs.
	if len(result.ImageRefs) > 0 && p.imageResolver != nil {
		updatedMarkdown, _, err := p.imageResolver.ResolveAndStore(ctx, result, p.fileService, tenantID)
		if err != nil {
			logger.Warnf(ctx, "image resolution failed: %v", err)
		} else {
			result.MarkdownContent = updatedMarkdown
		}
	}

	p.applyLineTruncation(ctx, result.MarkdownContent, attachment)
	return nil
}

// processAudioFile transcribes audio via ASR. Falls back to a placeholder when no ASR model is configured.
func (p *AttachmentProcessor) processAudioFile(
	ctx context.Context,
	data []byte,
	fileName string,
	attachment *types.MessageAttachment,
	asrModelID string,
) error {
	if asrModelID == "" || p.modelService == nil {
		attachment.Content = fmt.Sprintf("<audio_file name=\"%s\" transcription=\"unsupported\" />", fileName)
		logger.Infof(ctx, "no ASR model configured, keeping audio placeholder")
		return nil
	}

	asrInstance, err := p.modelService.GetASRModel(ctx, asrModelID)
	if err != nil {
		return fmt.Errorf("failed to get ASR model: %w", err)
	}

	logger.Infof(ctx, "starting audio transcription: fileName=%s, size=%d", fileName, len(data))
	res, err := asrInstance.Transcribe(ctx, data, fileName)
	if err != nil {
		return fmt.Errorf("audio transcription failed: %w", err)
	}
	transcript := res.Text

	p.applyLineTruncation(ctx, transcript, attachment)
	logger.Infof(ctx, "audio transcription done: textLen=%d", len(transcript))
	return nil
}

// processWithDocumentReader extracts content from complex formats (pdf, docx, xlsx, etc.).
func (p *AttachmentProcessor) processWithDocumentReader(
	ctx context.Context,
	data []byte,
	fileName string,
	fileType string,
	attachment *types.MessageAttachment,
	tenantID uint64,
) error {
	if p.documentReader == nil {
		return fmt.Errorf("DocumentReader not configured")
	}

	result, err := p.documentReader.Read(ctx, &types.ReadRequest{
		FileContent: data,
		FileName:    fileName,
		FileType:    fileType,
	})
	if err != nil {
		return fmt.Errorf("DocumentReader failed: %w", err)
	}

	// Resolve embedded image refs to storage URLs.
	if len(result.ImageRefs) > 0 && p.imageResolver != nil {
		updatedMarkdown, _, err := p.imageResolver.ResolveAndStore(ctx, result, p.fileService, tenantID)
		if err != nil {
			logger.Warnf(ctx, "image resolution failed: %v", err)
		} else {
			result.MarkdownContent = updatedMarkdown
		}
	}

	p.applyLineTruncation(ctx, result.MarkdownContent, attachment)
	return nil
}

// applyLineTruncation stores content into attachment, truncating at maxTextFileLines if needed.
func (p *AttachmentProcessor) applyLineTruncation(ctx context.Context, content string, attachment *types.MessageAttachment) {
	lines := strings.Split(content, "\n")
	lineCount := len(lines)
	attachment.LineCount = lineCount

	if lineCount > maxTextFileLines {
		attachment.Content = strings.Join(lines[:maxTextFileLines], "\n")
		attachment.IsTruncated = true
		logger.Infof(ctx, "content truncated: total=%d, kept=%d", lineCount, maxTextFileLines)
	} else {
		attachment.Content = content
	}
}

// isValidFileType reports whether fileName has a supported extension.
// Kept in sync with the frontend SUPPORTED_TYPES list in AttachmentUpload.vue.
func isValidFileType(fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return false
	}
	ext = strings.TrimPrefix(ext, ".")

	supportedTypes := []string{
		// documents
		"docx", "doc", "pdf", "ppt", "pptx",
		// spreadsheets
		"xlsx", "xls",
		// text / markup
		"md", "markdown", "txt", "csv", "json", "xml", "yaml", "yml", "log", "html",
		// images
		"jpg", "jpeg", "png", "gif", "bmp", "tiff", "webp",
		// audio
		"mp3", "wav", "m4a", "flac", "ogg", "aac",
	}

	for _, t := range supportedTypes {
		if ext == t {
			return true
		}
	}
	return false
}

// DecodeBase64Attachment decodes a base64 attachment payload, stripping any data URI prefix.
// Tries Std, URL, RawStd, and RawURL encodings in order.
func DecodeBase64Attachment(data string) ([]byte, error) {
	// Strip data URI prefix (e.g. "data:application/pdf;base64,")
	if idx := strings.Index(data, ","); idx != -1 {
		data = data[idx+1:]
	}
	data = strings.TrimSpace(data)

	for _, enc := range []struct{ e *base64.Encoding }{
		{base64.StdEncoding},
		{base64.URLEncoding},
		{base64.RawStdEncoding},
		{base64.RawURLEncoding},
	} {
		if decoded, err := enc.e.DecodeString(data); err == nil {
			return decoded, nil
		}
	}

	return nil, fmt.Errorf("base64 decode failed: unrecognised encoding")
}
