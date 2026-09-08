package types

import "context"

// LLMContentRedactedContextKey suppresses private assessment content in diagnostics.
const LLMContentRedactedContextKey ContextKey = "LLMContentRedacted"

func WithLLMContentRedacted(ctx context.Context) context.Context {
	return context.WithValue(ctx, LLMContentRedactedContextKey, true)
}

func LLMContentRedacted(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(LLMContentRedactedContextKey).(bool)
	return v
}
