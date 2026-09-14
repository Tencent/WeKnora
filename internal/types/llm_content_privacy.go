package types

import "context"

// LLMContentRedactedContextKey suppresses private assessment content in diagnostics.
const LLMContentRedactedContextKey ContextKey = "LLMContentRedacted"

// WithLLMContentRedacted marks model content as private for diagnostics.
func WithLLMContentRedacted(ctx context.Context) context.Context {
	return context.WithValue(ctx, LLMContentRedactedContextKey, true)
}

// LLMContentRedacted reports whether model content must be omitted from diagnostics.
func LLMContentRedacted(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(LLMContentRedactedContextKey).(bool)
	return v
}
