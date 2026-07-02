package types

// ChunkReusePlan is the outcome of diffing the chunks already stored for a
// knowledge item against the chunks produced by a fresh parse. It tells the
// reparse path what work can be skipped:
//
//   - ReuseIDs:    incoming chunks whose ID and content hash match an existing
//                  row, so their embedding is already in the vector store and
//                  must NOT be recomputed.
//   - ToEmbed:     incoming chunks that are new or whose content changed; these
//                  still need embedding + indexing.
//   - ToDeleteIDs: existing chunks that no longer appear in the new parse and
//                  should be removed from the store (chunk row + vector).
//
// This replaces the current "delete everything, recompute everything" reparse
// strategy (see issue #1679) with an incremental diff. It mirrors the existing,
// proven FAQ-import reuse logic but works on generic document chunks keyed by
// the content-addressed chunk ID produced by ChunkIDAllocator.
type ChunkReusePlan struct {
	ReuseIDs    map[string]struct{}
	ToEmbed     []*Chunk
	ToDeleteIDs []string
}

// ReuseCount returns the number of incoming chunks that can reuse an existing
// embedding. Handy for logging the cache hit rate of a reparse.
func (p ChunkReusePlan) ReuseCount() int { return len(p.ReuseIDs) }

// PlanChunkReuse diffs the existing (already stored) chunks against the freshly
// parsed incoming chunks and returns the reuse plan.
//
// A chunk is reusable only when an existing row shares both its ID and a
// non-empty, equal ContentHash. With content-addressed IDs (ChunkIDAllocator)
// unchanged content yields the same ID, so the common "edit one section, reparse"
// case keeps every untouched chunk's embedding. The ID match alone would be
// enough given the ID is content-derived; the ContentHash equality check is kept
// as a defensive integrity guard against legacy rows (e.g. UUID IDs predating
// this change, which carry no usable ContentHash and so are treated as changed).
//
// The function is pure and side-effect free so it can be unit tested in
// isolation; callers own the actual embed / delete I/O.
func PlanChunkReuse(existing, incoming []*Chunk) ChunkReusePlan {
	existingByID := make(map[string]*Chunk, len(existing))
	for _, c := range existing {
		if c != nil {
			existingByID[c.ID] = c
		}
	}

	plan := ChunkReusePlan{ReuseIDs: make(map[string]struct{}, len(incoming))}
	incomingIDs := make(map[string]struct{}, len(incoming))
	for _, c := range incoming {
		if c == nil {
			continue
		}
		incomingIDs[c.ID] = struct{}{}
		if prev, ok := existingByID[c.ID]; ok && prev.ContentHash != "" && prev.ContentHash == c.ContentHash {
			plan.ReuseIDs[c.ID] = struct{}{}
		} else {
			plan.ToEmbed = append(plan.ToEmbed, c)
		}
	}

	for _, c := range existing {
		if c == nil {
			continue
		}
		if _, stillPresent := incomingIDs[c.ID]; !stillPresent {
			plan.ToDeleteIDs = append(plan.ToDeleteIDs, c.ID)
		}
	}

	return plan
}
