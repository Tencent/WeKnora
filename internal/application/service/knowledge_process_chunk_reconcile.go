package service

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ChunkReconcilePlan describes the database-row diff between the currently
// active ingestion chunks and a newly parsed desired chunk set. Planning has no
// side effects: it does not mutate either input, allocate IDs, access storage,
// or perform model calls.
type ChunkReconcilePlan struct {
	Matched []ChunkMatch
	Added   []*types.Chunk
	Removed []*types.Chunk
	Legacy  []*types.Chunk
}

// BuildIngestionChunkMutation converts a bound reconcile plan into the
// repository transaction contract. Legacy rows are retired only when the new
// result is committed successfully; they are never guessed as matches.
func BuildIngestionChunkMutation(
	existing []*types.Chunk,
	plan *ChunkReconcilePlan,
	expectedAttempt ...int,
) (interfaces.IngestionChunkReconcileMutation, error) {
	if plan == nil {
		return interfaces.IngestionChunkReconcileMutation{}, fmt.Errorf("chunk reconcile plan is nil")
	}
	mutation := interfaces.IngestionChunkReconcileMutation{
		ExpectedActive: make([]interfaces.IngestionChunkSnapshot, 0, len(existing)),
		Matched:        make([]interfaces.IngestionChunkUpdate, 0, len(plan.Matched)),
		Added:          append([]*types.Chunk(nil), plan.Added...),
		RemovedIDs:     make([]string, 0, len(plan.Removed)+len(plan.Legacy)),
	}
	if len(expectedAttempt) > 0 {
		mutation.ExpectedAttempt = expectedAttempt[0]
	}
	for i, chunk := range existing {
		if chunk == nil {
			return interfaces.IngestionChunkReconcileMutation{}, fmt.Errorf("existing ingestion chunk at index %d is nil", i)
		}
		mutation.ExpectedActive = append(mutation.ExpectedActive, interfaces.IngestionChunkSnapshot{
			ID:              chunk.ID,
			StableIdentity:  chunk.StableIdentity,
			IdentityVersion: chunk.IdentityVersion,
			ChunkType:       chunk.ChunkType,
		})
	}
	for i, match := range plan.Matched {
		if match.Existing == nil || match.Desired == nil {
			return interfaces.IngestionChunkReconcileMutation{}, fmt.Errorf("chunk reconcile match at index %d is incomplete", i)
		}
		mutation.Matched = append(mutation.Matched, interfaces.IngestionChunkUpdate{
			ExistingID: match.Existing.ID,
			Desired:    match.Desired,
		})
	}
	for _, chunk := range plan.Removed {
		if chunk == nil {
			return interfaces.IngestionChunkReconcileMutation{}, fmt.Errorf("removed ingestion chunk is nil")
		}
		mutation.RemovedIDs = append(mutation.RemovedIDs, chunk.ID)
	}
	for _, chunk := range plan.Legacy {
		if chunk == nil {
			return interfaces.IngestionChunkReconcileMutation{}, fmt.Errorf("legacy ingestion chunk is nil")
		}
		mutation.RemovedIDs = append(mutation.RemovedIDs, chunk.ID)
	}
	return mutation, nil
}

func reconcileTextChunkIDs(chunks []*types.Chunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil && chunk.ChunkType == types.ChunkTypeText && chunk.ID != "" {
			ids = append(ids, chunk.ID)
		}
	}
	return ids
}

// ChunkMatch associates one active database row with the desired ingestion
// chunk that has the same stable logical identity.
type ChunkMatch struct {
	Existing *types.Chunk
	Desired  *types.Chunk
}

// PlanIngestionChunkReconcile builds a deterministic, side-effect-free diff
// for the chunk types managed by document ingestion: text and parent_text.
//
// Existing rows without StableIdentity are reported as Legacy and are never
// guessed by content, position, SeqID, or database ID. A stable identity is a
// match only when tenant, knowledge, chunk type, and identity version also
// agree. Duplicate active or desired identities fail explicitly rather than
// selecting an arbitrary row.
func PlanIngestionChunkReconcile(
	existing []*types.Chunk,
	desired []*types.Chunk,
) (*ChunkReconcilePlan, error) {
	plan := &ChunkReconcilePlan{
		Matched: make([]ChunkMatch, 0),
		Added:   make([]*types.Chunk, 0),
		Removed: make([]*types.Chunk, 0),
		Legacy:  make([]*types.Chunk, 0),
	}

	existingByIdentity := make(map[chunkReconcileIdentityKey]*types.Chunk, len(existing))
	for i, chunk := range existing {
		if err := validateReconcileChunk("existing", i, chunk); err != nil {
			return nil, err
		}
		if chunk.StableIdentity == "" {
			plan.Legacy = append(plan.Legacy, chunk)
			continue
		}

		key := reconcileIdentityKey(chunk)
		if previous, ok := existingByIdentity[key]; ok {
			return nil, fmt.Errorf(
				"duplicate active chunk stable identity %q for tenant %d knowledge %q (chunk IDs %q and %q)",
				chunk.StableIdentity,
				chunk.TenantID,
				chunk.KnowledgeID,
				previous.ID,
				chunk.ID,
			)
		}
		existingByIdentity[key] = chunk
	}

	desiredSeen := make(map[chunkReconcileIdentityKey]*types.Chunk, len(desired))
	matchedExisting := make(map[*types.Chunk]struct{}, len(desired))
	for i, chunk := range desired {
		if err := validateReconcileChunk("desired", i, chunk); err != nil {
			return nil, err
		}
		if chunk.StableIdentity == "" {
			return nil, fmt.Errorf("desired chunk at index %d has empty stable identity", i)
		}
		if chunk.IdentityVersion == "" {
			return nil, fmt.Errorf("desired chunk at index %d has empty identity version", i)
		}

		key := reconcileIdentityKey(chunk)
		if previous, ok := desiredSeen[key]; ok {
			return nil, fmt.Errorf(
				"duplicate desired chunk stable identity %q for tenant %d knowledge %q (chunk IDs %q and %q)",
				chunk.StableIdentity,
				chunk.TenantID,
				chunk.KnowledgeID,
				previous.ID,
				chunk.ID,
			)
		}
		desiredSeen[key] = chunk

		candidate, ok := existingByIdentity[key]
		if ok && reconcileChunksCompatible(candidate, chunk) {
			plan.Matched = append(plan.Matched, ChunkMatch{
				Existing: candidate,
				Desired:  chunk,
			})
			matchedExisting[candidate] = struct{}{}
			continue
		}
		plan.Added = append(plan.Added, chunk)
	}

	for _, chunk := range existing {
		if chunk.StableIdentity == "" {
			continue
		}
		if _, matched := matchedExisting[chunk]; !matched {
			plan.Removed = append(plan.Removed, chunk)
		}
	}

	return plan, nil
}

type chunkReconcileIdentityKey struct {
	tenantID       uint64
	knowledgeID    string
	stableIdentity string
}

func reconcileIdentityKey(chunk *types.Chunk) chunkReconcileIdentityKey {
	return chunkReconcileIdentityKey{
		tenantID:       chunk.TenantID,
		knowledgeID:    chunk.KnowledgeID,
		stableIdentity: chunk.StableIdentity,
	}
}

func reconcileChunksCompatible(existing, desired *types.Chunk) bool {
	return existing.StableIdentity != "" &&
		desired.StableIdentity != "" &&
		existing.StableIdentity == desired.StableIdentity &&
		existing.TenantID == desired.TenantID &&
		existing.KnowledgeID == desired.KnowledgeID &&
		existing.ChunkType == desired.ChunkType &&
		existing.IdentityVersion != "" &&
		existing.IdentityVersion == desired.IdentityVersion
}

func validateReconcileChunk(kind string, index int, chunk *types.Chunk) error {
	if chunk == nil {
		return fmt.Errorf("%s chunk at index %d is nil", kind, index)
	}
	if !isIngestionReconcileChunkType(chunk.ChunkType) {
		return fmt.Errorf(
			"%s chunk at index %d has unmanaged chunk type %q",
			kind,
			index,
			chunk.ChunkType,
		)
	}
	return nil
}

func isIngestionReconcileChunkType(chunkType types.ChunkType) bool {
	return chunkType == types.ChunkTypeText || chunkType == types.ChunkTypeParentText
}
