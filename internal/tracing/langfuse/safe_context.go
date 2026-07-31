package langfuse

import "context"

const preparedKnowledgeScopeHashPrefixLength = 12

type preparedKnowledgeScopeContextKey struct{}

// WithPreparedKnowledgeScope marks trace payloads that must not contain
// request content, internal identifiers, or provider errors.
func WithPreparedKnowledgeScope(ctx context.Context, executionScopeHash string) context.Context {
	if ctx == nil || executionScopeHash == "" {
		return ctx
	}
	if len(executionScopeHash) > preparedKnowledgeScopeHashPrefixLength {
		executionScopeHash = executionScopeHash[:preparedKnowledgeScopeHashPrefixLength]
	}
	return context.WithValue(
		ctx,
		preparedKnowledgeScopeContextKey{},
		executionScopeHash,
	)
}

// PreparedKnowledgeScopeHashPrefix returns the safe execution-scope summary
// carried by a prepared request.
func PreparedKnowledgeScopeHashPrefix(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	prefix, ok := ctx.Value(preparedKnowledgeScopeContextKey{}).(string)
	return prefix, ok && prefix != ""
}
