package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Persist the exact map output, including paired retractions, rather than using
// SourceRefs as an attempt marker (reparse deliberately reuses those refs).
type wikiReduceCheckpoint struct {
	Updates   []SlugUpdate
	Result    *docIngestResult
	Applied   []string
	FolderIDs []string
}

func (s *wikiIngestService) checkpointWikiReduce(
	ctx context.Context, ops []WikiPendingOp, updates map[string][]SlugUpdate,
	results []*docIngestResult, applied map[string]bool, folderIDs []string,
) error {
	writer, ok := s.pendingRepo.(interfaces.TaskPendingOpsCheckpointer)
	if !ok {
		return nil // Compatibility with alternate pending repositories.
	}
	byKnowledge := make(map[string]*docIngestResult, len(results))
	for _, result := range results {
		byKnowledge[result.KnowledgeID] = result
	}
	for i, op := range ops {
		if op.dbID == 0 || op.Resume != nil {
			continue
		}
		result := byKnowledge[op.KnowledgeID]
		if op.Op != WikiOpRetract && result == nil {
			continue // Map failed or skipped; it has no reusable output.
		}
		resume := &wikiReduceCheckpoint{Result: result, FolderIDs: folderIDs}
		if op.Resume != nil {
			resume.Applied = append(resume.Applied, op.Resume.Applied...)
		}
		for slug := range applied {
			for _, update := range updates[slug] {
				if update.KnowledgeID == op.KnowledgeID {
					resume.Applied = append(resume.Applied, slug)
					break
				}
			}
		}
		for slug, group := range updates {
			if applied[slug] {
				continue
			}
			for _, update := range group {
				if update.KnowledgeID == op.KnowledgeID {
					resume.Updates = append(resume.Updates, update)
				}
			}
		}
		op.Resume = resume
		payload, err := json.Marshal(op)
		if err != nil {
			return err
		}
		if err := writer.CheckpointPayload(ctx, op.dbID, payload); err != nil {
			return fmt.Errorf("checkpoint wiki op %d: %w", op.dbID, err)
		}
		ops[i].Resume = resume
	}
	return nil
}
