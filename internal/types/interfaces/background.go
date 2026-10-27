package interfaces

import (
	"context"
	"errors"
	"time"
)

// SessionRef identifies a session in every background-work interface.
type SessionRef struct {
	TenantID  uint64
	SessionID string
}

// External work states. The model and the UI learn only these strings; a
// feature maps its own internal states onto them at the boundary.
const (
	WorkStateRunning   = "running"
	WorkStateUnknown   = "unknown" // cheap lookup could not decide; never returned to the model
	WorkStateSucceeded = "succeeded"
	WorkStateFailed    = "failed"
	WorkStateCancelled = "cancelled"
	WorkStateLost      = "lost"
)

// IsTerminalWorkState reports whether s is one of the four terminal states.
func IsTerminalWorkState(s string) bool {
	switch s {
	case WorkStateSucceeded, WorkStateFailed, WorkStateCancelled, WorkStateLost:
		return true
	default:
		return false
	}
}

// ErrActivityLimit is returned by ActivityRegistry.Add when the session already
// holds kindCap live items of the same kind.
var ErrActivityLimit = errors.New("background: activity limit reached")

// ErrWorkNotFound is returned by a WorkHandler for an ID that does not exist in
// the given session. Handlers must use it for IDs owned by another session too.
var ErrWorkNotFound = errors.New("background: work not found in this session")

// BackgroundActivity is one live background item registered for a session.
type BackgroundActivity struct {
	ID        string // feature-prefixed, e.g. "sj_..."; opaque to shared code
	Kind      string // opaque to shared code; used only for per-kind caps and UI badges
	Title     string
	StartedAt time.Time
}

// ActivityRegistry records which background items are live in a session. Every
// entry is a lease: it disappears unless renewed before its deadline.
type ActivityRegistry interface {
	// Add registers a under a lease of length lease. If a.ID is already live it
	// only extends the deadline and does not count against kindCap. Otherwise it
	// fails with ErrActivityLimit when the session already holds kindCap live
	// items of a.Kind. kindCap <= 0 means no limit.
	Add(ctx context.Context, s SessionRef, a BackgroundActivity, lease time.Duration, kindCap int) error
	// Renew moves a live item's deadline to now+lease. It returns found=false,
	// and does nothing, when the item was removed or has expired.
	Renew(ctx context.Context, s SessionRef, id string, lease time.Duration) (found bool, err error)
	// Remove deletes the item. Removing a missing item is not an error.
	Remove(ctx context.Context, s SessionRef, id string) error
	// List returns live items only, in no particular order.
	List(ctx context.Context, s SessionRef) ([]BackgroundActivity, error)
}

// NoticeRetention is how long an unacknowledged notice stays after FinishedAt.
const NoticeRetention = 24 * time.Hour

// BackgroundNotice is one completion notice waiting to be shown to the model.
type BackgroundNotice struct {
	ID         string // equals the activity ID
	Kind       string
	Text       string // model-facing text written by the feature; the feature bounds its length
	FinishedAt time.Time
}

// NoticeInbox holds completion notices until a persisted turn acknowledges them.
type NoticeInbox interface {
	// Push stores n unless a notice with the same ID already exists. Only the
	// first push for an ID returns first=true; later ones change nothing.
	Push(ctx context.Context, s SessionRef, n BackgroundNotice) (first bool, err error)
	// Pending returns unacknowledged notices finished within NoticeRetention,
	// oldest first.
	Pending(ctx context.Context, s SessionRef) ([]BackgroundNotice, error)
	// Ack deletes the given IDs. Unknown IDs are ignored.
	Ack(ctx context.Context, s SessionRef, ids []string) error
	// Clear deletes every notice of the session.
	Clear(ctx context.Context, s SessionRef) error
}

// StatusOptions controls one WorkHandler.Status call.
type StatusOptions struct {
	// Detailed=false must be cheap: Redis, memory or a local file only, and
	// never a sandbox exec. It may return WorkStateUnknown.
	Detailed bool
	// RunID scopes incremental output reads to one agent run. Empty means the
	// read is not part of a run: return head and tail, move no read position.
	RunID string
	// OutputBudget caps output bytes for each item; 0 means the handler default.
	OutputBudget int
}

// WorkResult is one item's state as returned by a WorkHandler.
type WorkResult struct {
	ID       string
	State    string // one of the WorkState* constants
	Terminal bool
	Title    string
	Payload  any // feature-defined; serialized into tool results as-is
	// OutputFiles is the deliverable list for a finished shell job.
	// Nil means the output directory was not inspected.
	OutputFiles []string
}

// WorkHandler answers status and cancel requests for one feature's work IDs.
type WorkHandler interface {
	// Status returns one result per ID in input order. An ID that does not
	// belong to s must yield ErrWorkNotFound for the whole call or be reported
	// with State=WorkStateLost. A batch error does not confirm the states of
	// the other requested IDs or acknowledge their completion notices.
	Status(ctx context.Context, s SessionRef, ids []string, opts StatusOptions) ([]WorkResult, error)
	// Cancel requests cancellation and returns the state observed at that time.
	Cancel(ctx context.Context, s SessionRef, ids []string) ([]WorkResult, error)
}

// SessionLifecycleListener lets features react to /stop and session deletion.
type SessionLifecycleListener interface {
	OnStop(ctx context.Context, s SessionRef) error
	OnDelete(ctx context.Context, s SessionRef) error
}

// SessionKeepAliver keeps a session's sandbox from being reclaimed by its
// provider. Implementations throttle internally and never provision a sandbox.
type SessionKeepAliver interface {
	KeepAlive(ctx context.Context, s SessionRef) error
}

// WorkEventEmitter publishes the work_finished event. It is called only by the
// writer whose NoticeInbox.Push returned first=true.
type WorkEventEmitter interface {
	EmitWorkFinished(ctx context.Context, s SessionRef, parentMessageID string, r WorkResult) error
}
