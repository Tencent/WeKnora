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
	// RenewLease extends a pending row's lease while owner still holds it.
	RenewLease(ctx context.Context, id, owner string, until time.Time) (bool, error)
}
