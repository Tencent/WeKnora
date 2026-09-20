package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// bindKnowledgeImageResources records the knowledge owner for images emitted
// by document ingestion. Resource authorization for an org-shared knowledge
// base relies on this explicit extracted-image binding; the resource tenant
// alone is intentionally not enough to grant a cross-tenant reader access.
func bindKnowledgeImageResources(
	ctx context.Context,
	catalog interfaces.ResourceCatalog,
	tenantID uint64,
	knowledgeID string,
	images []docparser.StoredImage,
) {
	if catalog == nil || tenantID == 0 || strings.TrimSpace(knowledgeID) == "" {
		return
	}

	for _, image := range images {
		reference := strings.TrimSpace(image.ServingURL)
		if _, ok := types.ParseResourcePath(reference); !ok {
			continue
		}

		resource, err := catalog.Resolve(ctx, reference)
		if err != nil {
			logger.Warnf(ctx, "Failed to resolve extracted image resource %s for knowledge %s: %v", reference, knowledgeID, err)
			continue
		}
		if resource == nil || resource.TenantID != tenantID {
			logger.Warnf(ctx, "Skip extracted image resource %s for knowledge %s: tenant mismatch", reference, knowledgeID)
			continue
		}

		if err := catalog.Bind(ctx, reference, types.ResourceOwnerKnowledge, knowledgeID, types.ResourceRelationExtractedImage); err != nil {
			logger.Warnf(ctx, "Failed to bind extracted image resource %s to knowledge %s: %v", reference, knowledgeID, err)
		}
	}
}
