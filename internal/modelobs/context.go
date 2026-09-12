// Package modelobs records secret-free model call facts, usage, latency, and cost.
package modelobs

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// PurposeGeneral labels ordinary interactive model calls.
	PurposeGeneral = "general"
	// PurposeEvaluation labels strictly accounted evaluation model calls.
	PurposeEvaluation = "evaluation"
)

type callPolicy struct {
	purpose string
	strict  bool
}

// WithPurpose binds a stable purpose and accounting policy to model calls.
func WithPurpose(ctx context.Context, purpose string, strict bool) context.Context {
	if purpose == "" {
		purpose = PurposeGeneral
	}
	if strict {
		ctx = types.WithModelAccountingState(ctx)
	}
	return context.WithValue(ctx, types.ModelCallPolicyContextKey, callPolicy{purpose: purpose, strict: strict})
}

// WithEvaluationTask binds model calls to one evaluation task.
func WithEvaluationTask(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, types.ModelEvaluationTaskContextKey, taskID)
}

// WithApplicationCacheStatus distinguishes embedding misses and bypasses from provider cache facts.
func WithApplicationCacheStatus(ctx context.Context, status string) context.Context {
	return context.WithValue(ctx, types.ModelApplicationCacheContextKey, status)
}

func policyFromContext(ctx context.Context) callPolicy {
	if policy, ok := ctx.Value(types.ModelCallPolicyContextKey).(callPolicy); ok {
		return policy
	}
	return callPolicy{purpose: PurposeGeneral}
}

// SharingScope prevents strict calls from sharing a provider charge across tasks.
func SharingScope(ctx context.Context) string {
	if !policyFromContext(ctx).strict {
		return "general"
	}
	task, _ := ctx.Value(types.ModelEvaluationTaskContextKey).(string)
	if task == "" {
		task = types.ModelAccountingScope(ctx)
	}
	return "strict:" + task
}
