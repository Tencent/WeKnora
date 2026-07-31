package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
)

const folderIndexUpdateBatchSize = 500

// UpdateKnowledgeFolderIndex updates the denormalized folder_id stored with
// every indexed chunk for the requested documents. Relational placement is
// updated separately by the folder service after this call succeeds.
func (s *knowledgeService) UpdateKnowledgeFolderIndex(
	ctx context.Context,
	kbID string,
	knowledgeIDs []string,
	folderID string,
) error {
	if len(knowledgeIDs) == 0 {
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return fmt.Errorf("get knowledge base for folder index update: %w", err)
	}

	chunkFolderMap := make(map[string]string, folderIndexUpdateBatchSize)
	var engine *retriever.CompositeRetrieveEngine
	flush := func() error {
		if len(chunkFolderMap) == 0 {
			return nil
		}
		if engine == nil {
			engine, err = retriever.CreateRetrieveEngineForKB(
				ctx,
				s.retrieveEngine,
				s.ownership,
				kb.TenantID,
				kb.VectorStoreID,
			)
			if err != nil {
				return fmt.Errorf("resolve retrieve engine for folder index update: %w", err)
			}
		}
		if err := engine.BatchUpdateChunkFolderID(ctx, chunkFolderMap); err != nil {
			return fmt.Errorf("update chunk folder index payloads: %w", err)
		}
		clear(chunkFolderMap)
		return nil
	}

	seenKnowledge := make(map[string]struct{}, len(knowledgeIDs))
	for _, knowledgeID := range knowledgeIDs {
		if knowledgeID == "" {
			continue
		}
		if _, seen := seenKnowledge[knowledgeID]; seen {
			continue
		}
		seenKnowledge[knowledgeID] = struct{}{}

		chunks, err := s.chunkRepo.ListAllChunksByKnowledgeID(ctx, kb.TenantID, knowledgeID)
		if err != nil {
			return fmt.Errorf("list chunks for knowledge %s: %w", knowledgeID, err)
		}
		for _, chunk := range chunks {
			if chunk == nil || chunk.ID == "" {
				continue
			}
			chunkFolderMap[chunk.ID] = folderID
			if len(chunkFolderMap) >= folderIndexUpdateBatchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}
