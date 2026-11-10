package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// AgentPromptRepository persists human-in-the-loop prompts raised by agent
// runs. State changes are compare-and-set so replicas never disagree.
type AgentPromptRepository interface {
	Create(ctx context.Context, row *types.AgentPrompt) error
	// Get returns nil, nil when the row does not exist.
	Get(ctx context.Context, id string) (*types.AgentPrompt, error)
	// Transition applies t only when the row is in one of t.From and reports
	// whether this call won.
	Transition(ctx context.Context, id string, t types.AgentPromptTransition) (bool, error)
	// RenewLease extends the lease of a row that is pending, answered or
	// executing, while owner still holds it.
	RenewLease(ctx context.Context, id, owner string, until time.Time) (bool, error)
	// ListStaleLeases returns up to limit rows in one of statuses whose lease
	// ended before t: their owner replica is gone.
	ListStaleLeases(ctx context.Context, statuses []string, before time.Time, limit int) ([]*types.AgentPrompt, error)
	// ListExpired returns up to limit suspended rows that expired before t.
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*types.AgentPrompt, error)
	// ListBySession returns the session's rows in one of statuses.
	ListBySession(ctx context.Context, sessionID string, statuses []string) ([]*types.AgentPrompt, error)
}

// AgentRunSuspensionRepository persists the state of runs blocked on a
// prompt, one row per assistant message.
type AgentRunSuspensionRepository interface {
	// Save inserts or replaces the row for row.AssistantMessageID.
	Save(ctx context.Context, row *types.AgentRunSuspension) error
	// Get returns nil, nil when the message has no saved state.
	Get(ctx context.Context, assistantMessageID string) (*types.AgentRunSuspension, error)
	// Delete removes the row only while it still belongs to promptID, so a
	// newer prompt's state is never dropped by a stale caller.
	Delete(ctx context.Context, assistantMessageID, promptID string) error
}
