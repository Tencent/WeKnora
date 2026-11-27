package types

import "context"

type wikiPageCheckpointKey struct{}

// WikiPageCheckpoint identifies the durable operations applied by one page
// write. Only the matching page may consume these operations' slug updates.
type WikiPageCheckpoint struct {
	TenantID        uint64
	KnowledgeBaseID string
	Slug            string
	PendingOpIDs    []int64
}

// WithWikiPageCheckpoint attaches the operations to acknowledge with a page write.
func WithWikiPageCheckpoint(ctx context.Context, checkpoint WikiPageCheckpoint) context.Context {
	checkpoint.PendingOpIDs = append([]int64(nil), checkpoint.PendingOpIDs...)
	return context.WithValue(ctx, wikiPageCheckpointKey{}, checkpoint)
}

// WikiPageCheckpointFromContext returns an independent copy of the page checkpoint.
func WikiPageCheckpointFromContext(ctx context.Context) (WikiPageCheckpoint, bool) {
	checkpoint, ok := ctx.Value(wikiPageCheckpointKey{}).(WikiPageCheckpoint)
	checkpoint.PendingOpIDs = append([]int64(nil), checkpoint.PendingOpIDs...)
	return checkpoint, ok
}
