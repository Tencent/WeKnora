package service

import "github.com/Tencent/WeKnora/internal/types"

// computeChunkDiff reconciles newly-split chunks against existing chunk rows.
// Returns keptIDs (content unchanged, reuse vectors), addedChunks (new content,
// need CreateChunks+BatchIndex), removedIDs (deleted content, need vector+row cleanup).
// This is the core of non-destructive reparse (Phase 7).
func computeChunkDiff(newChunks []*types.Chunk, oldChunks []*types.Chunk) (keptIDs map[string]struct{}, addedChunks []*types.Chunk, removedIDs []string) {
	newByID := make(map[string]*types.Chunk, len(newChunks))
	for _, c := range newChunks {
		newByID[c.ID] = c
	}

	oldByID := make(map[string]*types.Chunk, len(oldChunks))
	for _, c := range oldChunks {
		oldByID[c.ID] = c
	}

	keptIDs = make(map[string]struct{})
	addedChunks = make([]*types.Chunk, 0, len(newByID))
	for id, c := range newByID {
		if _, exists := oldByID[id]; exists {
			keptIDs[id] = struct{}{}
		} else {
			addedChunks = append(addedChunks, c)
		}
	}

	for id := range oldByID {
		if _, exists := newByID[id]; !exists {
			removedIDs = append(removedIDs, id)
		}
	}

	return
}
