package service

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

const maxAgentKnowledgeImageBytes = 16 << 20

var errKnowledgeImageUnavailable = errors.New("knowledge image unavailable")

type agentKnowledgeImageOwners interface {
	ListKnowledgeImageOwners(context.Context, uint64, string) ([]*types.Knowledge, error)
}

type agentKnowledgeImages struct {
	catalog interfaces.ResourceCatalog
	owners  agentKnowledgeImageOwners
	shares  access.KBShareLookup
	storage interfaces.StorageBackendResolver
}

func newAgentKnowledgeImages(
	catalog interfaces.ResourceCatalog, owners agentKnowledgeImageOwners,
	shares access.KBShareLookup, storage interfaces.StorageBackendResolver,
) *agentKnowledgeImages {
	if catalog == nil || owners == nil || storage == nil {
		return nil
	}
	return &agentKnowledgeImages{catalog: catalog, owners: owners, shares: shares, storage: storage}
}

func (s *agentService) registerKnowledgeImageReader(registry *tools.ToolRegistry, cfg *types.AgentConfig) {
	if s.knowledgeImages == nil || s.knowledgeService == nil || cfg.SkillInstallMode() ||
		len(cfg.SearchTargets.GetAllKnowledgeBaseIDs()) == 0 {
		return
	}
	// Do not give a pure-chat agent a KB file reader if its retrieval tools
	// have been disabled, even if a persisted configuration still names KBs.
	_, docErr := registry.GetTool(tools.ToolReadDocument)
	_, searchErr := registry.GetTool(tools.ToolSearchKnowledge)
	_, wikiErr := registry.GetTool(tools.ToolWikiReadPage)
	if docErr != nil && searchErr != nil && wikiErr != nil {
		return
	}
	if existing, err := registry.GetTool(tools.ToolReadFile); err == nil {
		if reader, ok := existing.(*tools.ReadFileTool); ok {
			reader.WithKnowledgeImages(s.knowledgeImages, cfg.SearchTargets, s.knowledgeService)
		}
		return
	}
	reader := tools.NewReadFileTool(nil).WithKnowledgeImages(s.knowledgeImages, cfg.SearchTargets, s.knowledgeService)
	registry.RegisterTool(reader)
}

// ReadImage uses the resource's explicit document and backend bindings, not
// the execution tenant, the current default backend, or a model-supplied URL.
func (s *agentKnowledgeImages) ReadImage(
	ctx context.Context, ref string, authorize func(*types.Knowledge) bool,
) ([]byte, *types.Knowledge, error) {
	if authorize == nil {
		return nil, nil, errKnowledgeImageUnavailable
	}
	resource, err := s.catalog.Resolve(ctx, ref)
	if err != nil || resource == nil || resource.Kind != "image" || resource.TenantID == 0 ||
		resource.Size > maxAgentKnowledgeImageBytes ||
		(resource.ExpiresAt != nil && !resource.ExpiresAt.After(time.Now())) ||
		utils.ValidateKBScopedStoragePath(resource.PhysicalPath, resource.TenantID) != nil {
		return nil, nil, errKnowledgeImageUnavailable
	}
	owners, err := s.owners.ListKnowledgeImageOwners(ctx, resource.TenantID, resource.ID)
	if err != nil {
		return nil, nil, errKnowledgeImageUnavailable
	}
	permissions := access.NewKBPermissions(ctx, s.shares)
	for _, owner := range owners {
		if owner == nil || owner.TenantID != resource.TenantID || !authorize(owner) {
			continue
		}
		allowed, err := permissions.Check(owner.KnowledgeBaseID, owner.TenantID, types.OrgRoleViewer)
		if err != nil || !allowed {
			continue
		}
		data, err := s.readBoundImage(ctx, resource)
		if err != nil {
			return nil, nil, err
		}
		return data, owner, nil
	}
	return nil, nil, errKnowledgeImageUnavailable
}

func (s *agentKnowledgeImages) readBoundImage(ctx context.Context, resource *types.StoredResource) ([]byte, error) {
	backendID := resource.StorageBackendID
	if pathBackendID, _, ok := types.ParseStorageBackendPath(resource.PhysicalPath); ok {
		if backendID != "" && backendID != pathBackendID {
			return nil, errKnowledgeImageUnavailable
		}
		backendID = pathBackendID
	}
	if backendID == "" {
		// A provider alone cannot identify the backend of a legacy resource.
		return nil, errKnowledgeImageUnavailable
	}
	fs, _, err := s.storage.ResolveFileService(ctx, &types.Tenant{ID: resource.TenantID}, backendID,
		resource.Provider, storageurl.LocalStorageBaseDir())
	if err != nil || fs == nil {
		return nil, errKnowledgeImageUnavailable
	}
	reader, err := fs.GetFile(types.WithExecutionTenant(ctx, resource.TenantID), resource.PhysicalPath)
	if err != nil {
		return nil, errKnowledgeImageUnavailable
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, maxAgentKnowledgeImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxAgentKnowledgeImageBytes {
		return nil, errKnowledgeImageUnavailable
	}
	return data, nil
}
