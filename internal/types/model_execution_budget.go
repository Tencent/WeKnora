package types

import (
	"context"
	"time"
)

type modelExecutionBudgetKey struct{}

// ModelExecutionBudget starts at provider admission, not while waiting for CPU.
func WithModelExecutionBudget(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, modelExecutionBudgetKey{}, d)
}
func ModelExecutionBudget(ctx context.Context) time.Duration {
	d, _ := ctx.Value(modelExecutionBudgetKey{}).(time.Duration)
	return d
}
