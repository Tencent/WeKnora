package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/attachment"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const defaultAgentAttachmentParseWait = 60 * time.Second

// agentSessionAttachments is attachment://'s view of one session. Documents
// are looked up by tenant and ID only after the application reader matched them against
// the session's own message attachments, so a forked session can still read
// files whose temporary_documents row stays on the parent.
type agentSessionAttachments struct {
	messages  interfaces.MessageRepository
	documents interfaces.TemporaryDocumentRepository
	files     interfaces.FileService
	tenantID  uint64
	sessionID string
}

func (a *agentSessionAttachments) SessionAttachments(ctx context.Context) (types.MessageAttachments, error) {
	return a.messages.GetSessionAttachments(ctx, a.sessionID)
}

func (a *agentSessionAttachments) TemporaryDocument(
	ctx context.Context, documentID string,
) (*types.TemporaryDocument, error) {
	return a.documents.GetByID(ctx, a.tenantID, documentID)
}

func (a *agentSessionAttachments) ReadImage(ctx context.Context, ref string) ([]byte, error) {
	if a.files == nil {
		return nil, fmt.Errorf("attachment image storage is unavailable")
	}
	// Shared agents execute with their source tenant in the model context;
	// attachment storage belongs to the conversation owner instead.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, a.tenantID)
	file, err := a.files.GetFile(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	const maxAttachmentImageBytes = 32 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAttachmentImageBytes {
		return nil, fmt.Errorf("attachment image exceeds %d bytes", maxAttachmentImageBytes)
	}
	return data, nil
}

// agentAttachmentParseWait honours the agent's own timeout, then
// WEKNORA_CHAT_ATTACHMENT_WAIT_TIMEOUT_SEC, then the default.
func agentAttachmentParseWait(config *types.AgentConfig) time.Duration {
	if config != nil && config.AttachmentParseWaitTimeoutSec > 0 {
		return time.Duration(config.AttachmentParseWaitTimeoutSec) * time.Second
	}
	if raw := strings.TrimSpace(os.Getenv("WEKNORA_CHAT_ATTACHMENT_WAIT_TIMEOUT_SEC")); raw != "" {
		if sec, err := strconv.Atoi(raw); err == nil && sec > 0 {
			return time.Duration(sec) * time.Second
		}
	}
	return defaultAgentAttachmentParseWait
}

// registerAttachmentReader adds attachment:// to read_file when the session
// has at least one stored attachment. It follows the session's content rather
// than the tool allowlist: a file the user sent must stay readable.
func (s *agentService) registerAttachmentReader(
	ctx context.Context, registry *tools.ToolRegistry, config *types.AgentConfig, sessionID string,
) *attachment.Reader {
	if s.db == nil || sessionID == "" || config.SkillInstallMode() {
		return nil
	}
	// Attachments belong to the session owner, which differs from the agent's
	// tenant when a shared agent runs in someone else's workspace.
	tenantID, ok := types.SandboxTenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil
	}
	store := &agentSessionAttachments{
		messages:  repository.NewMessageRepository(s.db),
		documents: repository.NewTemporaryDocumentRepository(s.db),
		files:     s.fileService,
		tenantID:  tenantID,
		sessionID: sessionID,
	}
	attachments, err := store.SessionAttachments(ctx)
	if err != nil {
		logger.Warnf(ctx, "attachment:// not offered: load session attachments: %v", err)
		return nil
	}
	stored := false
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.ID) != "" {
			stored = true
			break
		}
	}
	if !stored {
		return nil
	}
	var reader *tools.ReadFileTool
	if existing, err := registry.GetTool(tools.ToolReadFile); err == nil {
		if reader, _ = existing.(*tools.ReadFileTool); reader == nil {
			logger.Warnf(ctx, "Cannot attach conversation attachments: read_file is registered by another tool")
			return nil
		}
	} else {
		reader = tools.NewReadFileTool(nil)
		registry.RegisterTool(reader)
	}
	source := attachment.NewReader(store, agentAttachmentParseWait(config), store.ReadImage)
	if len(config.AttachmentSandboxCopies) > 0 {
		ownerCtx := context.WithValue(ctx, types.TenantIDContextKey, tenantID)
		inputStore, err := s.sessionSandboxInputStore(ownerCtx, sessionID, config.SandboxConfigID)
		if err != nil {
			logger.Warnf(ctx, "Attachment sandbox sync unavailable: %v", err)
		} else if inputStore != nil {
			originals := make(map[string]string, len(config.AttachmentSandboxCopies))
			for original, address := range config.AttachmentSandboxCopies {
				originals[address] = original
			}
			source.Projection = &attachmentSandboxProjection{
				store: inputStore, sessionID: sessionID, originals: originals,
			}
		}
	}
	reader.WithAttachments(source).WithAttachmentCopies(config.AttachmentSandboxCopies)
	return source
}
