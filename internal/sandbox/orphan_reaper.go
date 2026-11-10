// Package sandbox: orphan sandbox reconciliation.
//
// Session sandboxes are created with onTimeout=pause + autoResume (see
// buildSessionCreateRequest), so the provider TTL PAUSES them rather than
// destroying them. When a binding is replaced — the CAS that happens when a
// workspace switches provider — or lost (Redis TTL, process restart), the old
// sandbox ID disappears from our records while the sandbox itself lingers in a
// paused state, consuming snapshot storage and money.
//
// Neither existing cleanup path covers that: session deletion works from a
// live binding, and the lifecycle's orphan cleanup is lazy, triggering only
// when someone touches the binding that has already been overwritten.
//
// This reconciliation loop closes the gap by enumerating the provider's
// sandboxes via the tenant metadata every sandbox is tagged with, comparing
// against the authoritative bindings, and deleting what is unbound. Paused
// sandboxes MUST be included: they are precisely the leaked ones.
package sandbox

import (
	"context"
	"fmt"
	"time"
)

// orphanReaperClient is the narrow slice of RemoteSandboxClient the reaper
// needs, declared separately so tests can supply a small fake.
type orphanReaperClient interface {
	List(ctx context.Context, filter RemoteListFilter) ([]RemoteSandboxSummary, error)
	Delete(ctx context.Context, sandboxID string) error
}

// OrphanReaperDeps configures one reconciliation pass for a single tenant.
type OrphanReaperDeps struct {
	Client orphanReaperClient

	// BoundIDs is the set of sandbox IDs the binding store still considers
	// owned. Anything outside this set is a deletion candidate.
	BoundIDs map[string]struct{}

	TenantID uint64

	// ConfigID scopes the sweep to one sandbox config. Empty means the
	// deployment default config. Sweeping by tenant alone would delete
	// sandboxes belonging to a sibling config on the same provider account.
	ConfigID string

	// Grace protects freshly created sandboxes that may not be bound yet.
	// Pass 0 when reaping deliberately (e.g. before a backend switch), where
	// every sandbox on the old backend is known to be abandoned.
	Grace time.Duration

	Now func() time.Time
}

// ReapOrphanSandboxes deletes unbound sandboxes for one tenant and reports how
// many were removed.
func ReapOrphanSandboxes(ctx context.Context, deps OrphanReaperDeps) (int, error) {
	if deps.Client == nil {
		return 0, fmt.Errorf("sandbox: orphan reaper requires a client")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}

	summaries, err := deps.Client.List(ctx, configSandboxFilter(deps.TenantID, deps.ConfigID))
	if err != nil {
		return 0, fmt.Errorf(
			"sandbox: list workspace %d config %q sandboxes: %w",
			deps.TenantID, NormalizeConfigID(deps.ConfigID), err)
	}

	cutoff := now().Add(-deps.Grace)
	deleted := 0
	for _, summary := range summaries {
		if _, bound := deps.BoundIDs[summary.ID]; bound {
			continue
		}
		if deps.Grace > 0 && !summary.StartedAt.IsZero() && summary.StartedAt.After(cutoff) {
			continue
		}
		if err := deps.Client.Delete(ctx, summary.ID); err != nil {
			// Keep going: one undeletable sandbox must not abort the sweep.
			continue
		}
		deleted++
	}
	return deleted, nil
}
