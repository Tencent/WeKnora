package service

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// BindReconciledChunkIDs replaces matched desired row IDs with their existing
// database IDs, then rewrites all ingestion references to those final IDs.
// Added rows retain the random IDs allocated while building desired chunks.
func BindReconciledChunkIDs(
	plan *ChunkReconcilePlan,
	desiredParents []*types.Chunk,
	desiredTexts []*types.Chunk,
	parsedChunks []types.ParsedChunk,
) error {
	if plan == nil {
		return fmt.Errorf("chunk reconcile plan is nil")
	}

	desired := make([]*types.Chunk, 0, len(desiredParents)+len(desiredTexts))
	desired = append(desired, desiredParents...)
	desired = append(desired, desiredTexts...)
	desiredSet := make(map[*types.Chunk]struct{}, len(desired))
	oldIDToChunk := make(map[string]*types.Chunk, len(desired))
	for i, chunk := range desired {
		if chunk == nil {
			return fmt.Errorf("desired ingestion chunk at index %d is nil", i)
		}
		if chunk.ID == "" {
			return fmt.Errorf("desired ingestion chunk at index %d has empty temporary ID", i)
		}
		if previous, duplicate := oldIDToChunk[chunk.ID]; duplicate {
			return fmt.Errorf("desired ingestion chunks %q and %q share temporary ID %q", previous.StableIdentity, chunk.StableIdentity, chunk.ID)
		}
		desiredSet[chunk] = struct{}{}
		oldIDToChunk[chunk.ID] = chunk
	}

	for i, match := range plan.Matched {
		if match.Existing == nil || match.Desired == nil {
			return fmt.Errorf("chunk reconcile match at index %d is incomplete", i)
		}
		if _, ok := desiredSet[match.Desired]; !ok {
			return fmt.Errorf("matched desired chunk %q is not in the desired ingestion set", match.Desired.StableIdentity)
		}
		if match.Existing.ID == "" {
			return fmt.Errorf("matched existing chunk %q has empty database ID", match.Existing.StableIdentity)
		}
		match.Desired.ID = match.Existing.ID
	}

	oldToFinalID := make(map[string]string, len(oldIDToChunk))
	finalIDs := make(map[string]string, len(desired))
	for oldID, chunk := range oldIDToChunk {
		if owner, duplicate := finalIDs[chunk.ID]; duplicate {
			return fmt.Errorf("desired stable identities %q and %q resolve to final chunk ID %q", owner, chunk.StableIdentity, chunk.ID)
		}
		finalIDs[chunk.ID] = chunk.StableIdentity
		oldToFinalID[oldID] = chunk.ID
	}

	for _, chunk := range desired {
		var err error
		if chunk.ParentChunkID, err = rewriteReconciledChunkReference("parent", chunk.ID, chunk.ParentChunkID, oldToFinalID); err != nil {
			return err
		}
		if chunk.PreChunkID, err = rewriteReconciledChunkReference("previous", chunk.ID, chunk.PreChunkID, oldToFinalID); err != nil {
			return err
		}
		if chunk.NextChunkID, err = rewriteReconciledChunkReference("next", chunk.ID, chunk.NextChunkID, oldToFinalID); err != nil {
			return err
		}
	}

	for i := range parsedChunks {
		if parsedChunks[i].ChunkID == "" {
			continue
		}
		finalID, ok := oldToFinalID[parsedChunks[i].ChunkID]
		if !ok {
			return fmt.Errorf("parsed chunk at index %d references unknown temporary chunk ID %q", i, parsedChunks[i].ChunkID)
		}
		parsedChunks[i].ChunkID = finalID
	}
	return nil
}

func rewriteReconciledChunkReference(kind, ownerID, reference string, oldToFinalID map[string]string) (string, error) {
	if reference == "" {
		return "", nil
	}
	finalID, ok := oldToFinalID[reference]
	if !ok {
		return "", fmt.Errorf("chunk %q has %s reference to unknown temporary chunk ID %q", ownerID, kind, reference)
	}
	return finalID, nil
}
