package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// mergeChatUploads keeps the current turn's copy of an attachment ahead of
// older session copies. The current copy carries the storage URL; the stored
// message copy does not.
func mergeChatUploads(current, historical types.MessageAttachments) types.MessageAttachments {
	if len(current) == 0 && len(historical) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(current)+len(historical))
	out := make(types.MessageAttachments, 0, len(current)+len(historical))
	add := func(list types.MessageAttachments) {
		for _, att := range list {
			id := strings.TrimSpace(att.ID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			att.ID = id
			out = append(out, att)
		}
	}
	add(current)
	add(historical)
	return out
}

func hasTabularChatUpload(attachments types.MessageAttachments) bool {
	for _, att := range attachments {
		if strings.TrimSpace(att.ID) != "" && tools.TabularFileType(att.FileType) != "" {
			return true
		}
	}
	return false
}

// attachChatUploads fills the allowlist the data tools already hold and binds
// that same store to the engine so the prompt registers those IDs as handles.
func (s *agentService) attachChatUploads(
	ctx context.Context,
	engine *agent.AgentEngine,
	registry *tools.ToolRegistry,
	config *types.AgentConfig,
	sessionID string,
) {
	store := chatAttachmentStore(registry)
	if store == nil || engine == nil {
		return
	}
	store.Set(s.prepareChatTabularAttachments(ctx, config, sessionID))
	engine.BindChatAttachments(store)
}

func chatAttachmentStore(registry *tools.ToolRegistry) *tools.ChatTabularAttachments {
	if registry == nil {
		return nil
	}
	for _, name := range []string{tools.ToolDataAnalysis, tools.ToolDataSchema} {
		tool, err := registry.GetTool(name)
		if err != nil || tool == nil {
			continue
		}
		provider, ok := tool.(interface {
			ChatAttachments() *tools.ChatTabularAttachments
		})
		if !ok {
			continue
		}
		if store := provider.ChatAttachments(); store != nil {
			return store
		}
	}
	return nil
}

// prepareChatTabularAttachments turns server-resolved message attachments into
// readers for the original files. The file type comes from the temporary
// document when the row is available, and is never inferred as pdf.
func (s *agentService) prepareChatTabularAttachments(
	ctx context.Context,
	config *types.AgentConfig,
	sessionID string,
) []tools.ChatTabularAttachment {
	if s == nil || config == nil {
		return nil
	}
	tenantID := config.ChatUploadTenantID
	if tenantID == 0 {
		if id, ok := types.TenantIDFromContext(ctx); ok {
			tenantID = id
		}
	}
	out := make([]tools.ChatTabularAttachment, 0, len(config.ChatUploads))
	for _, att := range config.ChatUploads {
		item, ok := s.openableChatUpload(ctx, tenantID, sessionID, att)
		if !ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (s *agentService) openableChatUpload(
	ctx context.Context,
	tenantID uint64,
	sessionID string,
	att types.MessageAttachment,
) (tools.ChatTabularAttachment, bool) {
	id := strings.TrimSpace(att.ID)
	fileType := tools.TabularFileType(att.FileType)
	if id == "" || fileType == "" {
		return tools.ChatTabularAttachment{}, false
	}
	fileName := att.FileName
	ref := strings.TrimSpace(att.URL)

	if s != nil && s.db != nil && tenantID != 0 {
		var doc types.TemporaryDocument
		err := s.db.WithContext(ctx).
			Where("tenant_id = ? AND id = ?", tenantID, id).
			First(&doc).Error
		switch {
		case err == nil:
			rowType := tools.TabularFileType(doc.FileType)
			if rowType == "" {
				logger.Infof(ctx, "chat upload %s file type %q is not tabular", id, doc.FileType)
				return tools.ChatTabularAttachment{}, false
			}
			if !s.sessionMayReadTemporaryDocument(ctx, sessionID, &doc) {
				logger.Warnf(ctx, "chat upload %s is not readable in session %s", id, sessionID)
				return tools.ChatTabularAttachment{}, false
			}
			fileType = rowType
			if name := strings.TrimSpace(doc.FileName); name != "" {
				fileName = name
			}
			// The row is the authority for where the bytes live. A caller-supplied
			// URL must not redirect an authorized temporary document at another file.
			ref = strings.TrimSpace(doc.ResourceRef)
		case errors.Is(err, gorm.ErrRecordNotFound):
			// No row under this tenant. Do not follow a caller-supplied URL.
			logger.Warnf(ctx, "chat upload %s was not found for tenant %d", id, tenantID)
			return tools.ChatTabularAttachment{}, false
		default:
			logger.Warnf(ctx, "chat upload %s lookup failed: %v", id, err)
			return tools.ChatTabularAttachment{}, false
		}
	}
	if ref == "" || s == nil || s.fileService == nil {
		logger.Warnf(ctx, "chat upload %s has no readable file", id)
		return tools.ChatTabularAttachment{}, false
	}
	storedRef := ref
	fs := s.fileService
	return tools.ChatTabularAttachment{
		ID:       id,
		FileName: fileName,
		FileType: fileType,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			return fs.GetFile(ctx, storedRef)
		},
	}, true
}

// sessionMayReadTemporaryDocument allows the owning session and a fork whose
// copied messages still name the parent document.
func (s *agentService) sessionMayReadTemporaryDocument(
	ctx context.Context, sessionID string, doc *types.TemporaryDocument,
) bool {
	if doc == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	if doc.SessionID == sessionID {
		return true
	}
	return s.sessionMessagesReferenceAttachment(ctx, sessionID, doc.ID)
}

func (s *agentService) sessionMessagesReferenceAttachment(ctx context.Context, sessionID, documentID string) bool {
	if s == nil || s.db == nil || sessionID == "" || documentID == "" {
		return false
	}
	if strings.ContainsAny(documentID, "\"'\\%") {
		return false
	}
	payload, err := json.Marshal([]map[string]string{{"id": documentID}})
	if err != nil {
		return false
	}
	var exists bool
	err = s.db.WithContext(ctx).Raw(
		`SELECT EXISTS (
			SELECT 1 FROM messages
			WHERE session_id = ? AND deleted_at IS NULL
			  AND attachments @> ?::jsonb
		)`, sessionID, string(payload),
	).Scan(&exists).Error
	if err != nil {
		logger.Warnf(ctx, "chat upload session reference check failed: %v", err)
		return false
	}
	return exists
}
