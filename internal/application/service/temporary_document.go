package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	temporaryDocumentDefaultTTL = 24 * time.Hour
)

var temporaryDocumentExtensions = map[string]struct{}{
	".docx": {}, ".doc": {}, ".pdf": {}, ".ppt": {}, ".pptx": {}, ".epub": {}, ".mhtml": {},
	".xmind": {},
	".xlsx":  {}, ".xls": {},
	".md": {}, ".markdown": {}, ".txt": {}, ".csv": {}, ".json": {}, ".xml": {}, ".yaml": {}, ".yml": {}, ".log": {}, ".html": {},
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {}, ".bmp": {}, ".tiff": {}, ".webp": {},
	".mp3": {}, ".wav": {}, ".m4a": {}, ".flac": {}, ".ogg": {}, ".aac": {},
}

var temporaryTextExtensions = map[string]struct{}{
	".md": {}, ".markdown": {}, ".txt": {}, ".csv": {}, ".json": {}, ".xml": {}, ".yaml": {}, ".yml": {}, ".log": {},
}

// sessionAttachmentLookup is the message-store surface Get/OpenFile need to
// authorize a parent-session temporary document after a fork. Fork copies
// attachment IDs onto the new messages but leaves the temporary_documents
// row on the parent; preview still has to work from the fork session id.
type sessionAttachmentLookup interface {
	GetSessionAttachments(ctx context.Context, sessionID string) (types.MessageAttachments, error)
}

type temporaryDocumentService struct {
	repo               interfaces.TemporaryDocumentRepository
	fileService        interfaces.FileService
	resourceCatalog    interfaces.ResourceCatalog
	documentReader     interfaces.DocumentReader
	imageResolver      *docparser.ImageResolver
	modelService       interfaces.ModelService
	tenantService      interfaces.TenantService
	taskEnqueuer       interfaces.TaskEnqueuer
	sessionAttachments sessionAttachmentLookup
}

func NewTemporaryDocumentService(
	repo interfaces.TemporaryDocumentRepository,
	fileService interfaces.FileService,
	resourceCatalog interfaces.ResourceCatalog,
	documentReader interfaces.DocumentReader,
	imageResolver *docparser.ImageResolver,
	modelService interfaces.ModelService,
	tenantService interfaces.TenantService,
	taskEnqueuer interfaces.TaskEnqueuer,
	messages interfaces.MessageRepository,
) interfaces.TemporaryDocumentService {
	return &temporaryDocumentService{
		repo: repo, fileService: fileService, resourceCatalog: resourceCatalog,
		documentReader: documentReader, imageResolver: imageResolver,
		modelService: modelService, tenantService: tenantService, taskEnqueuer: taskEnqueuer,
		sessionAttachments: messages,
	}
}

func temporaryDocumentTTL() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("WEKNORA_CHAT_ATTACHMENT_TTL_HOURS")); raw != "" {
		if hours, err := strconv.Atoi(raw); err == nil && hours > 0 {
			return time.Duration(hours) * time.Hour
		}
	}
	return temporaryDocumentDefaultTTL
}

func (s *temporaryDocumentService) Create(
	ctx context.Context,
	tenantID uint64,
	sessionID, fileName, mimeType string,
	fileSize int64,
	reader io.Reader,
	options types.TemporaryDocumentCreateOptions,
) (*types.TemporaryDocument, error) {
	if tenantID == 0 || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("invalid attachment scope")
	}
	safeName, valid := secutils.ValidateInput(fileName)
	if !valid {
		return nil, fmt.Errorf("invalid characters in file name")
	}
	baseName, err := secutils.SafeFileName(safeName)
	if err != nil {
		return nil, fmt.Errorf("unsafe file name: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(baseName))
	resourceTenantID := options.ResourceTenantID
	if resourceTenantID == 0 {
		resourceTenantID = tenantID
	}
	if !s.supportsExtension(ctx, resourceTenantID, ext) {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
	maxSize := secutils.GetMaxFileSizeMB() * 1024 * 1024
	if fileSize <= 0 || fileSize > maxSize {
		return nil, fmt.Errorf("file size must be between 1 byte and %dMB", secutils.GetMaxFileSizeMB())
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file exceeds size limit of %dMB", secutils.GetMaxFileSizeMB())
	}
	if int64(len(data)) != fileSize {
		fileSize = int64(len(data))
	}

	storageName := fmt.Sprintf("chat_attachment_%s%s", uuid.NewString()[:12], ext)
	// CleanupExpired owns the lifetime. A provider's auto-expiring bucket
	// cannot distinguish an abandoned upload from a sent conversation file.
	resourceRef, err := s.fileService.SaveBytes(ctx, data, tenantID, storageName, false)
	if err != nil {
		return nil, fmt.Errorf("save attachment: %w", err)
	}
	optionsJSON, _ := json.Marshal(options)
	document := &types.TemporaryDocument{
		ID:       uuid.NewString(),
		TenantID: tenantID, SessionID: sessionID, ResourceRef: resourceRef,
		FileName: baseName, FileType: ext, MimeType: strings.TrimSpace(mimeType), FileSize: fileSize,
		Status: types.TemporaryDocumentStatusUploaded, ExpiresAt: time.Now().Add(temporaryDocumentTTL()),
		ProcessingOptions: types.JSON(optionsJSON),
	}
	if err := s.repo.Create(ctx, document); err != nil {
		_ = s.releaseResource(ctx, document.ID, resourceRef)
		return nil, fmt.Errorf("create attachment record: %w", err)
	}
	if s.resourceCatalog != nil {
		if err := s.resourceCatalog.Bind(ctx, resourceRef, types.ResourceOwnerTemporaryDocument, document.ID, types.ResourceRelationSourceFile); err != nil {
			_ = s.repo.DeleteScoped(ctx, tenantID, sessionID, document.ID)
			_ = s.releaseResource(ctx, document.ID, resourceRef)
			return nil, fmt.Errorf("bind attachment resource: %w", err)
		}
	}
	payload, _ := json.Marshal(types.TemporaryDocumentTaskPayload{TenantID: tenantID, DocumentID: document.ID})
	queue, _ := types.QueueForTaskType(types.TypeTemporaryDocumentProcess)
	if _, err := s.taskEnqueuer.Enqueue(
		asynq.NewTask(types.TypeTemporaryDocumentProcess, payload),
		asynq.Queue(queue), asynq.MaxRetry(2), asynq.Timeout(10*time.Minute),
	); err != nil {
		_ = s.repo.MarkFailed(ctx, tenantID, document.ID, "failed to schedule document parsing")
		document.Status = types.TemporaryDocumentStatusFailed
		document.ErrorMessage = "failed to schedule document parsing"
		return document, fmt.Errorf("schedule attachment parsing: %w", err)
	}
	return document, nil
}

func (s *temporaryDocumentService) supportsExtension(ctx context.Context, tenantID uint64, ext string) bool {
	if _, ok := temporaryDocumentExtensions[ext]; ok {
		return true
	}
	if s.documentReader == nil {
		return false
	}
	var overrides map[string]string
	if tenant, err := s.tenantService.GetTenantByID(ctx, tenantID); err == nil && tenant != nil {
		overrides = tenant.ParserEngineConfig.ToOverridesMap()
	}
	engines, err := s.documentReader.ListEngines(ctx, overrides)
	if err != nil {
		return false
	}
	wanted := strings.TrimPrefix(strings.ToLower(ext), ".")
	if wanted == "" || wanted == "url" {
		return false
	}
	for _, engine := range engines {
		if !engine.Available {
			continue
		}
		for _, fileType := range engine.FileTypes {
			if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileType)), ".") == wanted {
				return true
			}
		}
	}
	return false
}

func (s *temporaryDocumentService) Get(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.TemporaryDocument, error) {
	return s.resolveInSession(ctx, tenantID, sessionID, documentID)
}

func (s *temporaryDocumentService) OpenFile(ctx context.Context, tenantID uint64, sessionID, documentID string) (io.ReadCloser, string, error) {
	document, err := s.resolveInSession(ctx, tenantID, sessionID, documentID)
	if err != nil {
		return nil, "", err
	}
	if document == nil {
		return nil, "", fmt.Errorf("attachment not found")
	}
	file, err := s.fileService.GetFile(ctx, document.ResourceRef)
	if err != nil {
		return nil, "", err
	}
	return file, document.FileName, nil
}

// resolveInSession finds a temporary document the caller may read from this
// session. Composer uploads live on temporary_documents.session_id; forked
// history keeps the parent's document id on copied messages, so a scoped miss
// falls back to tenant+id when that id appears on this session's messages.
func (s *temporaryDocumentService) resolveInSession(
	ctx context.Context, tenantID uint64, sessionID, documentID string,
) (*types.TemporaryDocument, error) {
	document, err := s.repo.GetScoped(ctx, tenantID, sessionID, documentID)
	if err != nil || document != nil {
		return document, err
	}
	document, err = s.repo.GetByID(ctx, tenantID, documentID)
	if err != nil || document == nil {
		return document, err
	}
	if document.SessionID == sessionID {
		return document, nil
	}
	ok, authErr := s.sessionReferencesAttachment(ctx, sessionID, documentID)
	if authErr != nil {
		return nil, authErr
	}
	if !ok {
		return nil, nil
	}
	return document, nil
}

func (s *temporaryDocumentService) sessionReferencesAttachment(
	ctx context.Context, sessionID, documentID string,
) (bool, error) {
	if s == nil || s.sessionAttachments == nil || sessionID == "" || documentID == "" {
		return false, nil
	}
	attachments, err := s.sessionAttachments.GetSessionAttachments(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("authorize forked attachment: %w", err)
	}
	for _, attachment := range attachments {
		if attachment.ID == documentID {
			return true, nil
		}
	}
	return false, nil
}

func (s *temporaryDocumentService) List(ctx context.Context, tenantID uint64, sessionID string) ([]*types.TemporaryDocument, error) {
	return s.repo.ListScoped(ctx, tenantID, sessionID)
}

func (s *temporaryDocumentService) Delete(ctx context.Context, tenantID uint64, sessionID, documentID string) error {
	document, err := s.repo.GetScoped(ctx, tenantID, sessionID, documentID)
	if err != nil || document == nil {
		return err
	}
	refs := map[string]struct{}{document.ResourceRef: {}}
	for _, ref := range temporaryDocumentImageRefs(document.ImageRefs) {
		refs[ref.URL] = struct{}{}
	}
	for ref := range refs {
		if err := s.releaseResource(ctx, document.ID, ref); err != nil {
			logger.Warnf(ctx, "keeping attachment resource after cleanup failure: document_id=%s err=%v",
				document.ID, err)
		}
	}
	return s.repo.DeleteScoped(ctx, tenantID, sessionID, documentID)
}

// A content-deduplicated image can also belong to another attachment or a
// knowledge entry. Drop only this document's claim, including on rollback;
// an unreadable binding count must never authorize deleting shared bytes.
func (s *temporaryDocumentService) releaseResource(ctx context.Context, documentID, ref string) error {
	if ref == "" {
		return nil
	}
	if s.resourceCatalog != nil {
		remaining, err := s.resourceCatalog.Release(ctx, ref, types.ResourceOwnerTemporaryDocument, documentID)
		if err != nil {
			return fmt.Errorf("release attachment resource: %w", err)
		}
		if remaining > 0 {
			return nil
		}
	}
	return s.fileService.DeleteFile(ctx, ref)
}

func temporaryDocumentImageRefs(raw types.JSON) []types.TemporaryDocumentImage {
	var images []types.TemporaryDocumentImage
	_ = json.Unmarshal(raw, &images)
	return images
}

func (s *temporaryDocumentService) CleanupExpired(ctx context.Context) error {
	for {
		documents, err := s.repo.ListExpired(ctx, time.Now(), 100)
		if err != nil {
			return err
		}
		if len(documents) == 0 {
			return nil
		}
		for _, document := range documents {
			if err := s.Delete(ctx, document.TenantID, document.SessionID, document.ID); err != nil {
				logger.Warnf(ctx, "cleanup temporary document failed: document_id=%s err=%v", document.ID, err)
			}
		}
		if len(documents) < 100 {
			return nil
		}
	}
}
