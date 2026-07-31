package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type knowledgeFolderPlacementResolver struct {
	reader interfaces.KnowledgeFolderReader
}

var _ interfaces.KnowledgeFolderPlacementResolver = (*knowledgeFolderPlacementResolver)(nil)

// NewKnowledgeFolderPlacementResolver creates a read-only folder placement resolver.
func NewKnowledgeFolderPlacementResolver(
	reader interfaces.KnowledgeFolderReader,
) interfaces.KnowledgeFolderPlacementResolver {
	return &knowledgeFolderPlacementResolver{reader: reader}
}

func (r *knowledgeFolderPlacementResolver) ResolveForCreate(
	ctx context.Context,
	knowledgeBaseID string,
	rawFolderID string,
) (string, error) {
	if ctx == nil {
		return "", ErrKnowledgeFolderInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(knowledgeBaseID) == "" {
		return "", ErrKnowledgeFolderInvalidArgument
	}
	if rawFolderID == types.KnowledgeFolderRootID {
		return types.KnowledgeFolderRootID, nil
	}
	tenantID, knowledgeBaseID, err := knowledgeFolderScope(ctx, knowledgeBaseID)
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", ErrKnowledgeFolderInternal
	}
	return resolveKnowledgeFolderPlacement(
		ctx,
		r.reader,
		tenantID,
		knowledgeBaseID,
		rawFolderID,
	)
}

func resolveKnowledgeFolderPlacement(
	ctx context.Context,
	reader interfaces.KnowledgeFolderReader,
	tenantID uint64,
	knowledgeBaseID string,
	rawFolderID string,
) (string, error) {
	if ctx == nil || tenantID == 0 || strings.TrimSpace(knowledgeBaseID) == "" {
		return "", ErrKnowledgeFolderInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if rawFolderID == types.KnowledgeFolderRootID {
		return types.KnowledgeFolderRootID, nil
	}
	if rawFolderID != strings.TrimSpace(rawFolderID) {
		return "", ErrKnowledgeFolderInvalidArgument
	}
	folderID, err := normalizeKnowledgeFolderID(rawFolderID, "folder_id", false)
	if err != nil {
		return "", err
	}
	if reader == nil {
		return "", ErrKnowledgeFolderInternal
	}

	folder, err := reader.GetByID(ctx, tenantID, knowledgeBaseID, folderID)
	if err != nil {
		return "", mapKnowledgeFolderError(err)
	}
	if folder == nil ||
		folder.ID != folderID ||
		folder.TenantID != tenantID ||
		folder.KnowledgeBaseID != knowledgeBaseID ||
		folder.DeletedAt.Valid {
		return "", ErrKnowledgeFolderDataIntegrity
	}

	chain, err := loadValidatedKnowledgeFolderChain(
		ctx,
		reader,
		tenantID,
		knowledgeBaseID,
		folder,
	)
	if err != nil {
		return "", mapKnowledgeFolderError(err)
	}
	if len(chain) == 0 {
		return "", ErrKnowledgeFolderDataIntegrity
	}
	for _, candidate := range chain {
		if candidate == nil ||
			candidate.TenantID != tenantID ||
			candidate.KnowledgeBaseID != knowledgeBaseID ||
			candidate.DeletedAt.Valid {
			return "", ErrKnowledgeFolderDataIntegrity
		}
	}
	if chain[len(chain)-1].ID != folderID {
		return "", ErrKnowledgeFolderDataIntegrity
	}
	return folderID, nil
}
