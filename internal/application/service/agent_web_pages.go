package service

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	webPageRelation      = "web_page"
	maxSavedWebPageBytes = 8 << 20
)

// agentWebPages reuses the resource catalog and file drivers. A web:// address
// grants no general resource access: every read must match a web-page binding to
// an active assistant message in this exact tenant, owner and session.
type agentWebPages struct {
	db                            *gorm.DB
	catalog                       interfaces.ResourceCatalog
	files                         func(context.Context, string) (interfaces.FileService, error)
	tenantID                      uint64
	ownerID, sessionID, messageID string
}

func (s *agentWebPages) messages(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).Model(&types.Message{}).
		Joins("JOIN sessions ON sessions.id = messages.session_id").
		Where("sessions.id = ? AND sessions.tenant_id = ?", s.sessionID, s.tenantID).
		Where("sessions.user_id = ?", s.ownerID).
		Where("sessions.deleted_at IS NULL AND messages.role = ?", "assistant")
}

func (s *agentWebPages) Save(ctx context.Context, content string) (string, error) {
	if len(content) > maxSavedWebPageBytes {
		return "", fmt.Errorf("web page exceeds the saved-page size limit")
	}
	var count int64
	if err := s.messages(ctx).Where("messages.id = ?", s.messageID).Count(&count).Error; err != nil || count != 1 {
		return "", fmt.Errorf("web page message is unavailable")
	}
	fs, err := s.files(ctx, "")
	if err != nil {
		return "", err
	}
	ref, err := fs.SaveBytes(ctx, []byte(content), s.tenantID, "web-"+uuid.NewString()+".md", false)
	if err != nil {
		return "", err
	}
	handle, ok := types.ParseResourcePath(ref)
	if !ok {
		_ = fs.DeleteFile(ctx, ref)
		return "", fmt.Errorf("web page storage did not return a resource reference")
	}
	if err := s.catalog.Bind(ctx, ref, types.ResourceOwnerMessage, s.messageID, webPageRelation); err != nil {
		_ = fs.DeleteFile(ctx, ref)
		return "", err
	}
	return "web://" + handle, nil
}

func (s *agentWebPages) Read(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "web://") {
		return nil, fmt.Errorf("invalid saved web page address")
	}
	ref := types.ResourceScheme + strings.TrimPrefix(path, "web://")
	resource, err := s.catalog.Resolve(ctx, ref)
	if err != nil || resource == nil || resource.TenantID != s.tenantID {
		return nil, fmt.Errorf("saved web page is unavailable in this session")
	}
	var count int64
	err = s.messages(ctx).
		Joins("JOIN resource_bindings ON resource_bindings.owner_id = messages.id").
		Where("resource_bindings.resource_id = ? AND resource_bindings.tenant_id = ?", resource.ID, s.tenantID).
		Where("resource_bindings.owner_type = ?", types.ResourceOwnerMessage).
		Where("resource_bindings.relation = ?", webPageRelation).
		Count(&count).Error
	if err != nil || count == 0 {
		return nil, fmt.Errorf("saved web page is unavailable in this session")
	}
	fs, err := s.files(ctx, resource.StorageBackendID)
	if err != nil {
		return nil, err
	}
	reader, err := fs.GetFile(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("saved web page is no longer available")
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, maxSavedWebPageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSavedWebPageBytes {
		return nil, fmt.Errorf("saved web page exceeds the read limit")
	}
	return data, nil
}

func (s *agentService) registerWebPageFiles(
	ctx context.Context, registry *tools.ToolRegistry, config *types.AgentConfig, sessionID, messageID string,
) {
	if config == nil || !config.WebSearchEnabled {
		return
	}
	raw, err := registry.GetTool(tools.ToolWebFetch)
	fetch, ok := raw.(*tools.WebFetchTool)
	if err != nil || !ok {
		return
	}
	if rawSearch, err := registry.GetTool(tools.ToolWebSearch); err == nil {
		if search, ok := rawSearch.(*tools.WebSearchTool); ok {
			search.WithPageReader(fetch)
		}
	}
	// The handler already pins session storage to its owner tenant, independently
	// of a shared agent's provider/knowledge tenant.
	tenantID, ok := types.SandboxTenantIDFromContext(ctx)
	if !ok || tenantID == 0 || s.db == nil || sessionID == "" || messageID == "" {
		return
	}
	pages := &agentWebPages{
		db: s.db, catalog: NewResourceCatalog(repository.NewResourceRepository(s.db)),
		tenantID: tenantID, sessionID: sessionID, messageID: messageID,
		ownerID: types.SessionOwnerIDFromContext(context.WithValue(ctx, types.TenantIDContextKey, tenantID)),
		files: func(ctx context.Context, backendID string) (interfaces.FileService, error) {
			if s.storageResolver != nil {
				fs, _, err := s.storageResolver.ResolveFileService(ctx, &types.Tenant{ID: tenantID}, backendID, "", "")
				if err == nil && fs == nil {
					err = fmt.Errorf("web page storage is unavailable")
				}
				return fs, err
			}
			if s.fileService == nil {
				return nil, fmt.Errorf("web page storage is unavailable")
			}
			return s.fileService, nil
		},
	}
	fetch.WithPageSource(pages)
	if existing, err := registry.GetTool(tools.ToolReadFile); err == nil {
		if reader, ok := existing.(*tools.ReadFileTool); ok {
			reader.WithWebPages(pages)
		} else {
			logger.Warnf(ctx, "Cannot attach saved web pages: read_file is registered by another tool")
			fetch.WithPageSource(nil)
		}
	} else {
		registry.RegisterTool(tools.NewReadFileTool(nil).WithWebPages(pages))
	}
}
