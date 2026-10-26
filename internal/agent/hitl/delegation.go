package hitl

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type delegationKey struct{}

// Delegation is installed by the worker, never from model arguments. Register
// durably associates a prompt with the current run before it can be answered.
type Delegation struct {
	Register func(context.Context, *types.AgentPrompt) error
	Check    func(context.Context) error
}

// WithDelegation attaches child prompt ownership checks to the execution context.
func WithDelegation(ctx context.Context, d Delegation) context.Context {
	return context.WithValue(ctx, delegationKey{}, d)
}

// CheckDelegation rejects canceled or stale child executions before prompt handling.
func CheckDelegation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d, ok := ctx.Value(delegationKey{}).(Delegation); ok && d.Check != nil {
		return d.Check(ctx)
	}
	return nil
}
