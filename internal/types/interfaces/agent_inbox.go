package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// AgentInboxRepository persists and queries agent inbox events.
// Implementations must work on both PostgreSQL and SQLite.
type AgentInboxRepository interface {
	// Insert persists one event. Dedup/debounce is the caller's concern.
	Insert(ctx context.Context, evt *types.AgentInboxEvent) error
	// List returns events matching the filters, newest first.
	List(ctx context.Context, f types.InboxQueryFilters) ([]*types.AgentInboxEvent, error)
	// MarkRead marks the given event IDs as read (no-op for already-read rows).
	MarkRead(ctx context.Context, tenantID uint64, ids []string, at time.Time) error
	// CountUnread returns the number of unread events matching the filters.
	CountUnread(ctx context.Context, f types.InboxQueryFilters) (int64, error)
	// DeleteOlderThan removes events created before the cutoff.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// AgentInboxService is the agent-facing situational inbox: it ingests
// curated events (policy-gated) and serves them to the check_inbox tool.
type AgentInboxService interface {
	// Ingest applies the event policy (enabled? priority? debounce/coalesce)
	// and persists the event. Failures are logged, never propagated — inbox
	// availability must not break the originating flow.
	Ingest(ctx context.Context, evt *types.AgentInboxEvent)
	// Check returns the digest for events matching the filters and marks
	// them read. Returns the digest plus the number of remaining unread.
	Check(ctx context.Context, f types.InboxQueryFilters, markRead bool) ([]types.InboxDigest, int64, error)
	// LatestView returns the most recent user.viewed_page event for the
	// KB scope (the "current page" pointer).
	LatestView(ctx context.Context, f types.InboxQueryFilters) (*types.AgentInboxEvent, error)
	// Trail returns the most recent view events (the "page trail" used to
	// resolve references like "the page I was on before").
	Trail(ctx context.Context, f types.InboxQueryFilters, limit int) ([]*types.AgentInboxEvent, error)
	// StartRetentionSweeper launches the background cleanup loop (no-op
	// when called multiple times).
	StartRetentionSweeper(ctx context.Context)
}
