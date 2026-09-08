package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// IsReferencedByKnowledgeBase accepts only explicit bindings to live documents.
// Text mentioning a handle or physical path is never ownership evidence.
func (r *resourceRepository) IsReferencedByKnowledgeBase(
	ctx context.Context, tenantID uint64, kbID, resourceID string,
) (bool, error) {
	if tenantID == 0 || kbID == "" || resourceID == "" {
		return false, nil
	}
	var count int64
	err := r.db.WithContext(ctx).Table("resource_bindings AS b").
		Joins("JOIN knowledges AS k ON k.id = b.owner_id AND k.tenant_id = b.tenant_id").
		Joins("JOIN knowledge_bases AS kb ON kb.id = k.knowledge_base_id "+
			"AND kb.tenant_id = k.tenant_id AND kb.deleted_at IS NULL").
		Where("b.resource_id = ? AND b.owner_type = ? AND b.tenant_id = ? "+
			"AND k.knowledge_base_id = ? AND k.deleted_at IS NULL",
			resourceID, types.ResourceOwnerKnowledge, tenantID, kbID).Count(&count).Error
	return count > 0, err
}

func (r *resourceRepository) GetMessageFileBindings(
	ctx context.Context, tenantID uint64, resourceID, messageID string,
) (*types.MessageFileBindings, error) {
	result := &types.MessageFileBindings{}
	if tenantID == 0 || resourceID == "" {
		return result, nil
	}
	err := r.db.WithContext(ctx).Table("resource_bindings AS b").
		Joins("JOIN knowledges AS k ON k.id = b.owner_id AND k.tenant_id = b.tenant_id").
		Joins("JOIN knowledge_bases AS kb ON kb.id = k.knowledge_base_id "+
			"AND kb.tenant_id = k.tenant_id AND kb.deleted_at IS NULL").
		Where("b.resource_id = ? AND b.owner_type = ? AND b.tenant_id = ? AND k.deleted_at IS NULL",
			resourceID, types.ResourceOwnerKnowledge, tenantID).
		Distinct("k.knowledge_base_id").Pluck("k.knowledge_base_id", &result.KnowledgeBaseIDs).Error
	if err != nil {
		return nil, err
	}
	if messageID != "" {
		var count int64
		err = r.db.WithContext(ctx).Model(&types.ResourceBinding{}).
			Where("resource_id = ? AND tenant_id = ? AND owner_type = ? AND owner_id = ? AND relation = ?",
				resourceID, tenantID, types.ResourceOwnerMessage, messageID, types.ResourceRelationArtifact).
			Count(&count).Error
		if err != nil {
			return nil, err
		}
		result.MessageArtifact = count > 0
	}
	return result, nil
}
