package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// fetchKnowledgeData gets knowledge data in batch.
func (s *knowledgeBaseService) fetchKnowledgeData(ctx context.Context,
	tenantID uint64,
	knowledgeIDs []string,
) (map[string]*types.Knowledge, error) {
	knowledges, err := s.kgRepo.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id":     tenantID,
			"knowledge_ids": knowledgeIDs,
		})
		return nil, err
	}

	knowledgeMap := make(map[string]*types.Knowledge, len(knowledges))
	for _, knowledge := range knowledges {
		knowledgeMap[knowledge.ID] = knowledge
	}

	return knowledgeMap, nil
}

// fetchKnowledgeDataWithShared gets knowledge data in batch, including knowledge
// from KBs authorized for the original caller. Initial tenant-scoped rows
// require the same permission check as cross-tenant expansion.
func (s *knowledgeBaseService) fetchKnowledgeDataWithShared(ctx context.Context,
	tenantID uint64,
	knowledgeIDs []string,
) (map[string]*types.Knowledge, error) {
	rows, err := s.fetchKnowledgeData(ctx, tenantID, knowledgeIDs)
	if err != nil {
		return nil, err
	}
	permissions := kbReadPermissions(ctx, s.kbShareService)
	knowledgeMap := make(map[string]*types.Knowledge, len(rows))
	appendAllowed := func(k *types.Knowledge) {
		if k == nil {
			return
		}
		allowed, err := permissions.Check(k.KnowledgeBaseID, k.TenantID, types.OrgRoleViewer)
		if err == nil && allowed {
			knowledgeMap[k.ID] = k
		}
	}
	for _, k := range rows {
		appendAllowed(k)
	}
	for _, id := range knowledgeIDs {
		if rows[id] != nil {
			continue
		}
		k, err := s.kgRepo.GetKnowledgeByIDOnly(ctx, id)
		if err != nil && !errors.Is(err, repository.ErrKnowledgeNotFound) {
			return nil, err
		}
		if err == nil {
			appendAllowed(k)
		}
	}
	return knowledgeMap, nil
}

// listChunksByIDWithShared fetches chunks by IDs, including chunks from shared KBs the user has access to.
func (s *knowledgeBaseService) listChunksByIDWithShared(ctx context.Context,
	tenantID uint64,
	chunkIDs []string,
) ([]*types.Chunk, error) {
	rows, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		return nil, err
	}
	permissions := kbReadPermissions(ctx, s.kbShareService)
	chunks := make([]*types.Chunk, 0, len(rows))
	foundSet := make(map[string]bool)
	appendAllowed := func(c *types.Chunk) {
		if c == nil || foundSet[c.ID] {
			return
		}
		allowed, err := permissions.Check(c.KnowledgeBaseID, c.TenantID, types.OrgRoleViewer)
		if err == nil && allowed {
			chunks = append(chunks, c)
			foundSet[c.ID] = true
		}
	}
	for _, c := range rows {
		appendAllowed(c)
		if c != nil {
			foundSet[c.ID] = true
		}
	}
	missing := s.findMissingIDs(chunkIDs, func(id string) bool { return foundSet[id] })
	if len(missing) == 0 {
		return chunks, nil
	}
	crossChunks, err := s.chunkRepo.ListChunksByIDOnly(ctx, missing)
	if err != nil {
		logger.Warnf(ctx, "[listChunksByIDWithShared] Failed to fetch chunks by ID only: %v", err)
		return nil, err
	}
	for _, c := range crossChunks {
		appendAllowed(c)
	}
	return chunks, nil
}

// findMissingIDs returns IDs from the input slice that are not found by the exists predicate.
func (s *knowledgeBaseService) findMissingIDs(ids []string, exists func(string) bool) []string {
	var missing []string
	for _, id := range ids {
		if !exists(id) {
			missing = append(missing, id)
		}
	}
	return missing
}
