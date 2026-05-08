package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// wikiLogEntryService is a thin wrapper over WikiLogEntryRepository. The
// event layer carries no business rules today, so the service just
// forwards to the repo — the indirection exists so handlers / ingest
// batch code depend on an interface rather than a concrete repo type.
type wikiLogEntryService struct {
	repo interfaces.WikiLogEntryRepository
}

// NewWikiLogEntryService constructs a WikiLogEntryService backed by the
// given repository.
func NewWikiLogEntryService(repo interfaces.WikiLogEntryRepository) interfaces.WikiLogEntryService {
	return &wikiLogEntryService{repo: repo}
}

// AppendBatch records the given events in one database round trip. Empty
// batches are a no-op (the repo handles that).
func (s *wikiLogEntryService) AppendBatch(ctx context.Context, entries []*types.WikiLogEntry) error {
	return s.repo.AppendBatch(ctx, entries)
}

// List paginates the per-KB event feed. See repo.List for cursor semantics.
func (s *wikiLogEntryService) List(ctx context.Context, kbID string, cursor string, limit int) (*types.WikiLogEntryListResponse, error) {
	entries, nextCursor, err := s.repo.List(ctx, kbID, cursor, limit)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		// Normalise to an empty slice so clients don't need to
		// distinguish `null` from `[]`.
		entries = []*types.WikiLogEntry{}
	}
	return &types.WikiLogEntryListResponse{
		Entries:    entries,
		NextCursor: nextCursor,
	}, nil
}

// DeleteByKB removes the log feed for a KB. Called when the KB itself is
// being deleted, so no further reads happen.
func (s *wikiLogEntryService) DeleteByKB(ctx context.Context, kbID string) error {
	return s.repo.DeleteByKB(ctx, kbID)
}
