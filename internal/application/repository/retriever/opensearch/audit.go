package opensearch

import "context"

// AuditSink receives audit events emitted from within the driver at the
// exact moment they occur (index provisioned, reindex executed). The driver
// owns this abstraction so it imports no service package — the dependency
// arrow stays one-way (service implements AuditSink; the driver only invokes
// it). A nil sink is a no-op, so tests and the env-path (no audit service)
// need no special casing.
type AuditSink interface {
	// EmitIndexCreated fires once when the driver provisions a new k-NN
	// index. alias is the per-dimension alias (or the keyword-only index
	// name); dim is the embedding dimension (0 for the keyword-only index).
	EmitIndexCreated(ctx context.Context, alias string, dim int)
	// EmitReindexExecuted fires when CopyIndices finishes copying docs from
	// one index to another.
	EmitReindexExecuted(ctx context.Context, srcAlias, dstAlias string, docs int64)
}

// nopSink is the null-object used when no sink is configured, so emit call
// sites never need a nil check.
type nopSink struct{}

func (nopSink) EmitIndexCreated(context.Context, string, int)              {}
func (nopSink) EmitReindexExecuted(context.Context, string, string, int64) {}

var _ AuditSink = nopSink{}

// Option configures a Repository at construction time.
type Option func(*Repository)

// WithAuditSink injects an audit sink. A nil sink is ignored (the Repository
// keeps its default no-op behavior).
func WithAuditSink(s AuditSink) Option {
	return func(r *Repository) {
		if s != nil {
			r.sink = s
		}
	}
}

// auditSink returns the configured sink, or a no-op if none was set (e.g. a
// Repository built directly in tests, or constructed without WithAuditSink).
func (r *Repository) auditSink() AuditSink {
	if r.sink == nil {
		return nopSink{}
	}
	return r.sink
}
