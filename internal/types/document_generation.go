package types

import (
	"context"
	"errors"
)

// ErrDocumentGenerationSuperseded indicates that a newer producer owns document state.
var ErrDocumentGenerationSuperseded = errors.New("document generation superseded")

type (
	documentGenerationContextKey struct{}
	documentGenerationContext    struct {
		TenantID           uint64
		KnowledgeID, Token string
	}
)

// WithDocumentGeneration scopes a mutation fence to one tenant and document.
func WithDocumentGeneration(ctx context.Context, tenantID uint64, knowledgeID, token string) context.Context {
	return context.WithValue(ctx, documentGenerationContextKey{},
		documentGenerationContext{tenantID, knowledgeID, token})
}

// DocumentGenerationFromContext returns a token only for its exact document owner.
func DocumentGenerationFromContext(ctx context.Context, tenantID uint64, knowledgeID string) (string, bool) {
	generation, ok := ctx.Value(documentGenerationContextKey{}).(documentGenerationContext)
	if !ok || generation.TenantID != tenantID || generation.KnowledgeID != knowledgeID || generation.Token == "" {
		return "", false
	}
	return generation.Token, true
}

// DocumentGenerationOwnerFromContext supplies the scoped owner for repository
// APIs that accept a knowledge ID without a separate tenant argument.
func DocumentGenerationOwnerFromContext(ctx context.Context, knowledgeID string) (uint64, string, bool) {
	generation, ok := ctx.Value(documentGenerationContextKey{}).(documentGenerationContext)
	if !ok || generation.KnowledgeID != knowledgeID || generation.TenantID == 0 || generation.Token == "" {
		return 0, "", false
	}
	return generation.TenantID, generation.Token, true
}
