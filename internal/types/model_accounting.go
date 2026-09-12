package types

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

// ErrModelAccounting is a non-retryable failure to persist a provider call.
var ErrModelAccounting = errors.New("model call accounting failed")

// Model accounting context keys identify shared failures, call policy, task linkage, cache status and request metadata.
const (
	ModelAccountingStateContextKey  ContextKey = "ModelAccountingState"
	ModelCallPolicyContextKey       ContextKey = "ModelCallPolicy"
	ModelEvaluationTaskContextKey   ContextKey = "ModelEvaluationTask"
	ModelApplicationCacheContextKey ContextKey = "ModelApplicationCache"
	ModelRequestMetadataContextKey  ContextKey = "ModelRequestMetadata"
)

type modelAccountingState struct {
	sync.Mutex
	err   error
	scope string
}

// WithModelAccountingState retains failures across retries and fallback paths.
func WithModelAccountingState(ctx context.Context) context.Context {
	if _, ok := ctx.Value(ModelAccountingStateContextKey).(*modelAccountingState); ok {
		return ctx
	}
	return context.WithValue(ctx, ModelAccountingStateContextKey, &modelAccountingState{scope: uuid.NewString()})
}

// RecordModelAccountingError retains a persistence failure in the shared scope and returns the joined error.
func RecordModelAccountingError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	err = errors.Join(ErrModelAccounting, err)
	if state, ok := ctx.Value(ModelAccountingStateContextKey).(*modelAccountingState); ok {
		state.Lock()
		state.err = errors.Join(state.err, err)
		state.Unlock()
	}
	return err
}

// ModelAccountingError returns persistence failures recorded in the shared accounting scope.
func ModelAccountingError(ctx context.Context) error {
	if state, ok := ctx.Value(ModelAccountingStateContextKey).(*modelAccountingState); ok {
		state.Lock()
		defer state.Unlock()
		return state.err
	}
	return nil
}

// ModelAccountingScope returns the shared identity used to correlate retries and fallback attempts.
func ModelAccountingScope(ctx context.Context) string {
	if state, ok := ctx.Value(ModelAccountingStateContextKey).(*modelAccountingState); ok {
		return state.scope
	}
	return ""
}
