package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newCandidateChunkIDs returns changed/new chunk IDs that did not exist before
// the rebuild. These are the only database rows and indexes that are safe to
// delete when the current write/index attempt fails.
func newCandidateChunkIDs(oldChunks, changedNew []*types.Chunk) []string {
	oldByID := make(map[string]struct{}, len(oldChunks))
	for _, chunk := range oldChunks {
		if chunk != nil && chunk.ID != "" {
			oldByID[chunk.ID] = struct{}{}
		}
	}

	ids := make([]string, 0, len(changedNew))
	for _, chunk := range changedNew {
		if chunk == nil || chunk.ID == "" {
			continue
		}
		if _, existed := oldByID[chunk.ID]; !existed {
			ids = append(ids, chunk.ID)
		}
	}
	return ids
}

// rollbackChunkCandidateWrites restores the database state that existed before
// applying a chunk diff. It deliberately leaves stale chunks untouched: stale
// cleanup belongs to the later commit-after-success phase.
func rollbackChunkCandidateWrites(
	ctx context.Context,
	chunkService interfaces.ChunkService,
	oldChunks []*types.Chunk,
	diff chunkCandidateDiff,
) error {
	if chunkService == nil {
		return nil
	}

	var rollbackErrors []error
	if ids := newCandidateChunkIDs(oldChunks, diff.ChangedNew); len(ids) > 0 {
		if err := chunkService.DeleteChunks(ctx, ids); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("delete new candidate chunks: %w", err))
		}
	}

	restoreByID := make(map[string]*types.Chunk)
	oldByID := make(map[string]*types.Chunk, len(oldChunks))
	for _, chunk := range oldChunks {
		if chunk != nil && chunk.ID != "" {
			oldByID[chunk.ID] = chunk
		}
	}
	for _, candidate := range diff.ChangedNew {
		if candidate != nil {
			if existing := oldByID[candidate.ID]; existing != nil {
				restoreByID[existing.ID] = existing
			}
		}
	}
	for _, update := range diff.MetadataOnly {
		if update.Existing != nil && update.Existing.ID != "" {
			restoreByID[update.Existing.ID] = update.Existing
		}
	}
	for _, chunk := range restoreByID {
		if err := chunkService.UpdateChunk(ctx, chunk); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore chunk %s: %w", chunk.ID, err))
		}
	}

	return errors.Join(rollbackErrors...)
}
