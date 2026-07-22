package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// wikiLogEntryService persists the detailed Wiki feed and projects a bounded
// summary into the generic per-KB activity timeline.
type wikiLogEntryService struct {
	repo  interfaces.WikiLogEntryRepository
	audit interfaces.AuditLogService
}

// NewWikiLogEntryService constructs a WikiLogEntryService backed by the
// given repository.
func NewWikiLogEntryService(
	repo interfaces.WikiLogEntryRepository,
	audit interfaces.AuditLogService,
) interfaces.WikiLogEntryService {
	return &wikiLogEntryService{repo: repo, audit: audit}
}

// AppendBatch records the given events in one database round trip. Empty
// batches are a no-op (the repo handles that).
func (s *wikiLogEntryService) AppendBatch(ctx context.Context, entries []*types.WikiLogEntry) error {
	if err := s.repo.AppendBatch(ctx, entries); err != nil {
		return err
	}
	type summary struct {
		tenantID uint64
		count    int
		actions  map[string]int
	}
	byKB := make(map[string]*summary)
	for _, entry := range entries {
		if entry == nil || entry.KnowledgeBaseID == "" {
			continue
		}
		item := byKB[entry.KnowledgeBaseID]
		if item == nil {
			item = &summary{tenantID: entry.TenantID, actions: make(map[string]int)}
			byKB[entry.KnowledgeBaseID] = item
		}
		item.count++
		item.actions[entry.Action]++
	}
	for kbID, item := range byKB {
		recordKBActivity(ctx, s.audit, item.tenantID, kbID, types.AuditActionWikiContentChanged,
			"wiki", kbID, types.AuditOutcomeSuccess,
			map[string]any{"count": item.count, "actions": item.actions})
	}
	return nil
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
