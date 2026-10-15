package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// AssistantResourceHandler resolves IDs in the current conversation before
// opening an attachment. Neither storage URLs nor caller-selected sessions cross
// the operation boundary.
type AssistantResourceHandler struct {
	sessions  interfaces.SessionService
	messages  interfaces.MessageRepository
	documents interfaces.TemporaryDocumentService
	knowledge interfaces.KnowledgeService
}

// NewAssistantResourceHandler wires session-authorized attachment operations.
func NewAssistantResourceHandler(s interfaces.SessionService, m interfaces.MessageRepository,
	d interfaces.TemporaryDocumentService, k interfaces.KnowledgeService,
) *AssistantResourceHandler {
	return &AssistantResourceHandler{sessions: s, messages: m, documents: d, knowledge: k}
}

// AssistantAttachment is the safe metadata returned to the assistant.
type AssistantAttachment struct {
	ID        string `json:"attachment_id" extensions:"x-agent"`
	SessionID string `json:"session_id" extensions:"x-agent"`
	FileName  string `json:"file_name" extensions:"x-agent"`
	FileType  string `json:"file_type" extensions:"x-agent"`
	FileSize  int64  `json:"file_size" extensions:"x-agent"`
	Status    string `json:"status" extensions:"x-agent"`
}

func (h *AssistantResourceHandler) inventory(
	ctx context.Context,
) (context.Context, string, types.MessageAttachments, error) {
	dispatch, ok := types.InternalDispatchFromContext(ctx)
	action, attributed := types.AuditAgentActionFromContext(ctx)
	if !ok || !attributed || action.SessionID == "" {
		return ctx, "", nil, errors.NewForbiddenError("Conversation context required")
	}
	// KB access guards may have selected a shared KB's tenant. Attachments still
	// belong to the signed-in caller's conversation, never the KB owner.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, dispatch.TenantID)
	if _, err := h.sessions.GetOwnedSession(ctx, action.SessionID); err != nil {
		return ctx, "", nil, errors.NewNotFoundError("Conversation not found")
	}
	attachments, err := h.messages.GetSessionAttachments(ctx, action.SessionID)
	if err != nil {
		return ctx, "", nil, errors.NewInternalServerError("Attachment inventory unavailable")
	}
	return ctx, action.SessionID, attachments, nil
}

func (h *AssistantResourceHandler) selected(
	ctx context.Context, id string,
) (context.Context, string, *types.TemporaryDocument, error) {
	ctx, session, inventory, err := h.inventory(ctx)
	if err != nil {
		return ctx, "", nil, err
	}
	selected := false
	for _, item := range inventory {
		if item.ID == id {
			selected = true
			break
		}
	}
	if !selected {
		return ctx, "", nil, errors.NewNotFoundError("Attachment is not in this conversation")
	}
	tenant := types.MustTenantIDFromContext(ctx)
	doc, err := h.documents.Get(ctx, tenant, session, id)
	if err != nil || doc == nil || doc.TenantID != tenant ||
		(!doc.ExpiresAt.IsZero() && !doc.ExpiresAt.After(time.Now())) {
		return ctx, "", nil, errors.NewNotFoundError("Attachment unavailable or expired; upload it again")
	}
	return ctx, session, doc, nil
}

func attachmentMetadata(doc *types.TemporaryDocument, session string) AssistantAttachment {
	return AssistantAttachment{
		ID: doc.ID, SessionID: session, FileName: doc.FileName,
		FileType: doc.FileType, FileSize: doc.FileSize, Status: doc.Status,
	}
}

// ListAttachments godoc
// @Summary List the user's attachments in this conversation
// @Produce json
// @Success 200 {object} handler.DataResponse{data=[]handler.AssistantAttachment}
// @x-agent {"id":"attachment.list","risk":"read","max_items":50}
// @x-agent-hint "Only files the user attached to the current conversation. Use these attachment_id values for import or download; unavailable/expired files must be uploaded again."
// @Router /assistant/attachments [get]
func (h *AssistantResourceHandler) ListAttachments(c *gin.Context) {
	ctx, session, inventory, err := h.inventory(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]AssistantAttachment, 0)
	seen := map[string]bool{}
	for i := len(inventory) - 1; i >= 0 && len(out) < 50; i-- {
		item := inventory[i]
		if item.ID == "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		doc, loadErr := h.documents.Get(ctx, types.MustTenantIDFromContext(ctx), session, item.ID)
		if loadErr != nil || doc == nil || doc.TenantID != types.MustTenantIDFromContext(ctx) {
			continue
		}
		if !doc.ExpiresAt.IsZero() && !doc.ExpiresAt.After(time.Now()) {
			continue
		}
		out = append(out, attachmentMetadata(doc, session))
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// DownloadAttachment godoc
// @Summary Prepare an authenticated attachment download
// @Produce json
// @Param attachment_id path string true "Attachment ID from attachment.list" extensions(x-agent)
// @Success 200 {object} handler.DataResponse{data=handler.AssistantAttachment}
// @x-agent {"id":"attachment.download","risk":"read"}
// @x-agent-hint "Shows a download button for an attachment in this conversation. Download rechecks session ownership and attachment access; never returns a storage URL."
// @Router /assistant/attachments/{attachment_id} [get]
func (h *AssistantResourceHandler) DownloadAttachment(c *gin.Context) {
	_, session, doc, err := h.selected(c.Request.Context(), c.Param("attachment_id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": attachmentMetadata(doc, session)})
}

// SelectedAttachmentRequest names a file from the conversation inventory.
type SelectedAttachmentRequest struct {
	// ID of a file the user attached to this conversation.
	AttachmentID string `json:"attachment_id" binding:"required" extensions:"x-agent"`
}

// ImportKnowledge godoc
// @Summary Import a selected conversation attachment into a knowledge base
// @Accept json
// @Produce json
// @Param id path string true "Destination knowledge base ID" extensions(x-agent=knowledge_base_id)
// @Param request body handler.SelectedAttachmentRequest true "Selected attachment"
// @Success 200 {object} handler.DataResponse{data=types.Knowledge}
// @x-agent {"id":"knowledge.create_from_attachment","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Imports the original bytes of one user-selected conversation attachment, at most 20 MiB. Creates a document using the existing upload and duplicate checks, then parses in the background. No arbitrary paths or URLs."
// @Router /knowledge-bases/{id}/selected-attachment [post]
func (h *AssistantResourceHandler) ImportKnowledge(c *gin.Context) {
	var req SelectedAttachmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Select an attachment"))
		return
	}
	ctx, session, doc, err := h.selected(c.Request.Context(), req.AttachmentID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	const maxBytes = 20 << 20
	if doc.FileSize > maxBytes {
		_ = c.Error(errors.NewBadRequestError("Attachment exceeds 20 MiB"))
		return
	}
	reader, _, err := h.documents.OpenFile(ctx, types.MustTenantIDFromContext(ctx), session, doc.ID)
	if err != nil {
		_ = c.Error(errors.NewNotFoundError("Attachment unavailable"))
		return
	}
	defer func() { _ = reader.Close() }()
	file, cleanup, err := attachmentForm(reader, doc.FileName, maxBytes)
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("Attachment unavailable or too large"))
		return
	}
	defer cleanup()
	// Keep the original KB-authorized context for the write.
	knowledge, err := h.knowledge.CreateKnowledgeFromFile(
		c.Request.Context(), c.Param("id"), file, nil, nil, "", nil, "assistant", nil,
	)
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("Attachment import failed; inspect knowledge base status"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": knowledge})
}

func attachmentForm(reader io.Reader, name string, maxBytes int64) (*multipart.FileHeader, func(), error) {
	name = filepath.Base(name)
	if name == "." || strings.ContainsAny(name, "\r\n") {
		return nil, nil, fmt.Errorf("invalid attachment filename")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", filepath.Base(name))
	if err != nil {
		return nil, nil, err
	}
	n, err := io.Copy(part, io.LimitReader(reader, maxBytes+1))
	if err != nil || n > maxBytes {
		return nil, nil, fmt.Errorf("attachment exceeds budget or cannot be read")
	}
	if err = writer.Close(); err != nil {
		return nil, nil, err
	}
	form, err := multipart.NewReader(&buffer, writer.Boundary()).ReadForm(1 << 20)
	if err != nil {
		return nil, nil, err
	}
	if len(form.File["file"]) != 1 {
		_ = form.RemoveAll()
		return nil, nil, fmt.Errorf("invalid attachment form")
	}
	return form.File["file"][0], func() { _ = form.RemoveAll() }, nil
}

// ImportFAQ godoc
// @Summary Append FAQ entries from a selected JSON attachment
// @Accept json
// @Produce json
// @Param id path string true "Destination FAQ knowledge base ID" extensions(x-agent=knowledge_base_id)
// @Param request body handler.SelectedAttachmentRequest true "JSON attachment from attachment.list"
// @Success 200 {object} handler.DataResponse{data=handler.TaskIDData}
// @x-agent {"id":"faq.import_selected","risk":"destructive","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Append a JSON array in FAQ export format, at most 200 entries and 1 MiB. Exported IDs are ignored; duplicates follow existing import validation. Never replaces the whole knowledge base. Poll faq.get_import_progress."
// @Router /knowledge-bases/{id}/faq/selected-import [post]
func (h *AssistantResourceHandler) ImportFAQ(c *gin.Context) {
	var req SelectedAttachmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Select a JSON attachment"))
		return
	}
	ctx, session, doc, err := h.selected(c.Request.Context(), req.AttachmentID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !strings.EqualFold(filepath.Ext(doc.FileName), ".json") || doc.FileSize > 1<<20 {
		_ = c.Error(errors.NewBadRequestError("Choose a JSON export of at most 1 MiB"))
		return
	}
	reader, _, err := h.documents.OpenFile(ctx, types.MustTenantIDFromContext(ctx), session, doc.ID)
	if err != nil {
		_ = c.Error(errors.NewNotFoundError("Attachment unavailable"))
		return
	}
	defer func() { _ = reader.Close() }()
	payload, err := selectedFAQPayload(reader)
	if err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	task, err := h.knowledge.UpsertFAQEntries(c.Request.Context(), c.Param("id"), payload)
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("FAQ import failed; inspect the FAQ knowledge base"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": TaskIDData{TaskID: task}})
}

func selectedFAQPayload(reader io.Reader) (*types.FAQBatchUpsertPayload, error) {
	data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, fmt.Errorf("FAQ import exceeds 1 MiB or cannot be read")
	}
	var entries []types.FAQEntryPayload
	if err = json.Unmarshal(data, &entries); err != nil || len(entries) == 0 || len(entries) > 200 {
		return nil, fmt.Errorf("FAQ JSON must contain 1–200 entries")
	}
	for i := range entries {
		entries[i].ID = nil // Export IDs are source identifiers, never migration instructions.
		if strings.TrimSpace(entries[i].StandardQuestion) == "" || len(entries[i].Answers) == 0 {
			return nil, fmt.Errorf("every FAQ entry needs a question and answers")
		}
	}
	return &types.FAQBatchUpsertPayload{Mode: types.FAQBatchModeAppend, Entries: entries}, nil
}
