package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiLogEntryRepository persists per-KB wiki operation events. The table
// is append-only; updates are never issued. Reads paginate newest-first
// with a stable numeric cursor.
type WikiLogEntryRepository interface {
	// AppendBatch inserts a batch of events in a single INSERT...VALUES
	// statement. All entries in a batch must belong to the same KB in
	// practice, but the repo does not enforce that — the caller owns the
	// grouping.
	AppendBatch(ctx context.Context, entries []*types.WikiLogEntry) error

	// List returns up to `limit` entries in (knowledge_base_id, id DESC)
	// order. `cursor` is the ID of the last entry from the previous page
	// ("return entries strictly older than this"); empty string means
	// "from the newest".
	//
	// The returned nextCursor is empty when no further rows remain; the
	// caller can treat that as end-of-stream.
	List(ctx context.Context, kbID string, cursor string, limit int) (entries []*types.WikiLogEntry, nextCursor string, err error)

	// DeleteByKB removes every log entry belonging to the given KB. Used
	// when a wiki KB is deleted so the log table does not accumulate
	// orphans. Safe to call even if no rows exist.
	DeleteByKB(ctx context.Context, kbID string) error
}

// WikiLogEntryService is the service layer over WikiLogEntryRepository.
// Kept intentionally thin — it mirrors the repo today because the events
// are pure bookkeeping and carry no business rules.
type WikiLogEntryService interface {
	AppendBatch(ctx context.Context, entries []*types.WikiLogEntry) error
	List(ctx context.Context, kbID string, cursor string, limit int) (*types.WikiLogEntryListResponse, error)
	DeleteByKB(ctx context.Context, kbID string) error
}
