package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Narrow interfaces keep StorageAccessAuthorizer testable without stubbing
// entire repository / share service surfaces.

type storageKBLookup interface {
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

type storageKnowledgeLookup interface {
	ListKnowledgeBaseIDsByFilePath(ctx context.Context, tenantID uint64, filePath string) ([]string, error)
}

type storageChunkLookup interface {
	ListKnowledgeBaseIDsByStorageReference(ctx context.Context, tenantID uint64, storageRef string) ([]string, error)
}

type storageKBShareCheck interface {
	CheckTenantKBPermission(ctx context.Context, kbID string, callerTenantID uint64, callerTenantRole types.TenantRole) (types.OrgMemberRole, bool, error)
}

type storageAgentShareCheck interface {
	GetSharedAgentForTenant(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole, agentID string) (*types.CustomAgent, error)
	TenantCanAccessKBViaSomeSharedAgent(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole, kb *types.KnowledgeBase) (bool, error)
}

// StorageAccessAuthorizer decides whether an authenticated tenant may read a
// provider:// storage path. Same-tenant paths are always allowed; cross-tenant
// paths require a KB the caller can read via shared space or a shared agent.
type StorageAccessAuthorizer struct {
	kbShare    storageKBShareCheck
	agentShare storageAgentShareCheck
	kbRepo     storageKBLookup
	kgRepo     storageKnowledgeLookup
	chunkRepo  storageChunkLookup
}

// NewStorageAccessAuthorizer builds a storage access authorizer.
func NewStorageAccessAuthorizer(
	kbShare storageKBShareCheck,
	agentShare storageAgentShareCheck,
	kbRepo storageKBLookup,
	kgRepo storageKnowledgeLookup,
	chunkRepo storageChunkLookup,
) *StorageAccessAuthorizer {
	return &StorageAccessAuthorizer{
		kbShare:    kbShare,
		agentShare: agentShare,
		kbRepo:     kbRepo,
		kgRepo:     kgRepo,
		chunkRepo:  chunkRepo,
	}
}

// StorageAccessResult describes an authorized cross-tenant read.
type StorageAccessResult struct {
	OwnerTenantID uint64
}

// AuthorizeSharedStoragePath checks whether callerTenantID may read filePath
// when the path belongs to another tenant. kbID and agentID are optional hints
// that skip the DB lookup when provided by the client.
func (a *StorageAccessAuthorizer) AuthorizeSharedStoragePath(
	ctx context.Context,
	callerTenantID uint64,
	callerTenantRole types.TenantRole,
	filePath string,
	kbID string,
	agentID string,
) (*StorageAccessResult, error) {
	if a == nil {
		return nil, fmt.Errorf("storage access authorizer unavailable")
	}

	pathTenant := secutils.ParseTenantIDFromStoragePath(filePath)
	if pathTenant == 0 {
		return nil, fmt.Errorf("storage path has no tenant segment")
	}
	if pathTenant == callerTenantID {
		return &StorageAccessResult{OwnerTenantID: pathTenant}, nil
	}

	kbIDs, err := a.resolveCandidateKBIDs(ctx, pathTenant, filePath, kbID)
	if err != nil {
		return nil, err
	}
	if len(kbIDs) == 0 {
		return nil, fmt.Errorf("storage path not linked to any knowledge base")
	}

	for _, id := range kbIDs {
		if a.canReadKB(ctx, id, pathTenant, callerTenantID, callerTenantRole, agentID) {
			logger.Infof(ctx, "[storage_access] tenant %d granted read of %q via kb=%s owner=%d",
				callerTenantID, filePath, id, pathTenant)
			return &StorageAccessResult{OwnerTenantID: pathTenant}, nil
		}
	}

	return nil, fmt.Errorf("no shared access to storage path")
}

func (a *StorageAccessAuthorizer) resolveCandidateKBIDs(
	ctx context.Context,
	pathTenant uint64,
	filePath string,
	kbID string,
) ([]string, error) {
	if kbID != "" {
		if a.kbRepo == nil {
			return nil, fmt.Errorf("knowledge base lookup unavailable")
		}
		kb, err := a.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil {
			return nil, fmt.Errorf("knowledge base not found")
		}
		if kb == nil || kb.TenantID != pathTenant {
			return nil, fmt.Errorf("knowledge base tenant mismatch")
		}
		return []string{kbID}, nil
	}

	seen := make(map[string]struct{})
	add := func(ids []string) {
		for _, id := range ids {
			if id == "" {
				continue
			}
			seen[id] = struct{}{}
		}
	}

	if a.kgRepo != nil {
		fromKnowledge, err := a.kgRepo.ListKnowledgeBaseIDsByFilePath(ctx, pathTenant, filePath)
		if err != nil {
			return nil, err
		}
		add(fromKnowledge)
	}

	if a.chunkRepo != nil {
		fromChunks, err := a.chunkRepo.ListKnowledgeBaseIDsByStorageReference(ctx, pathTenant, filePath)
		if err != nil {
			return nil, err
		}
		add(fromChunks)
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out, nil
}

func (a *StorageAccessAuthorizer) canReadKB(
	ctx context.Context,
	kbID string,
	pathTenant uint64,
	callerTenantID uint64,
	callerTenantRole types.TenantRole,
	agentID string,
) bool {
	if a.kbRepo == nil {
		return false
	}
	kb, err := a.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb == nil {
		return false
	}
	if kb.TenantID != pathTenant {
		return false
	}
	if kb.TenantID == callerTenantID {
		return true
	}

	if a.kbShare != nil {
		permission, isShared, permErr := a.kbShare.CheckTenantKBPermission(ctx, kbID, callerTenantID, callerTenantRole)
		if permErr == nil && isShared && permission.HasPermission(types.OrgRoleViewer) {
			return true
		}
	}

	if a.agentShare == nil {
		return false
	}

	if agentID != "" {
		agent, err := a.agentShare.GetSharedAgentForTenant(ctx, callerTenantID, callerTenantRole, agentID)
		if err != nil || agent == nil || agent.TenantID != kb.TenantID {
			return false
		}
		switch agent.Config.KBSelectionMode {
		case "all":
			return true
		case "selected":
			for _, allowedID := range agent.Config.KnowledgeBases {
				if allowedID == kb.ID {
					return true
				}
			}
		}
		return false
	}

	can, err := a.agentShare.TenantCanAccessKBViaSomeSharedAgent(ctx, callerTenantID, callerTenantRole, kb)
	return err == nil && can
}
