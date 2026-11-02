package types

import (
	"context"
	"time"
)

// KnowledgeReparseMetadataKey is an opaque, server-owned attempt identity.
const KnowledgeReparseMetadataKey = "_knowledge_reparse_claim"

type knowledgeFolderAnchorContextKey struct{}

// WithKnowledgeFolderAnchor preserves presence: an explicit empty anchor means
// the KB root, while an absent anchor is an older import caller.
func WithKnowledgeFolderAnchor(ctx context.Context, anchor string) context.Context {
	return context.WithValue(ctx, knowledgeFolderAnchorContextKey{}, anchor)
}

// KnowledgeFolderAnchorFromContext returns the anchor and whether it was set.
// An explicitly set empty anchor identifies the knowledge base root.
func KnowledgeFolderAnchorFromContext(ctx context.Context) (string, bool) {
	anchor, ok := ctx.Value(knowledgeFolderAnchorContextKey{}).(string)
	return anchor, ok
}

// KnowledgeFolderPathSnapshot is an internal, server-created placement guard.
// Its witness identifies an existing explicit or inferred ancestor before slow
// file I/O. It is never accepted from an API request or persisted in the DB.
type KnowledgeFolderPathSnapshot struct {
	TenantID        uint64
	KnowledgeBaseID string
	Path            string
	Anchor          string
	MarkerPath      string
	MarkerCreatedAt time.Time
	KnowledgeID     string
	KnowledgePath   string
}
