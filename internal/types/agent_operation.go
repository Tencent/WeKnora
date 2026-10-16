package types

import (
	"context"
	"strings"
)

// InternalDispatch is the identity an in-process API dispatch runs as. It is
// carried only on the Go context of a request constructed by the server
// itself, so no inbound HTTP request can present it. Auth rebuilds the full
// session (user, tenant membership, role) from the database instead of
// trusting anything else on the request.
type InternalDispatch struct {
	UserID   string
	TenantID uint64
}

type internalDispatchContextKey struct{}

// WithInternalDispatch marks ctx as an in-process call made as d's user. An
// incomplete identity leaves ctx unmarked.
func WithInternalDispatch(ctx context.Context, d InternalDispatch) context.Context {
	d.UserID = strings.TrimSpace(d.UserID)
	if d.UserID == "" || d.TenantID == 0 {
		return ctx
	}
	return context.WithValue(ctx, internalDispatchContextKey{}, d)
}

// InternalDispatchFromContext returns the in-process caller ctx was marked with.
func InternalDispatchFromContext(ctx context.Context) (InternalDispatch, bool) {
	if ctx == nil {
		return InternalDispatch{}, false
	}
	d, ok := ctx.Value(internalDispatchContextKey{}).(InternalDispatch)
	if !ok || d.UserID == "" || d.TenantID == 0 {
		return InternalDispatch{}, false
	}
	return d, true
}

// AuditAgentAction attributes a mutation to an agent acting for the signed-in
// user. It is display metadata for the activity feed, never an authorization
// grant: the mutation itself is authorized as the user.
type AuditAgentAction struct {
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Operation string `json:"operation,omitempty"`
}

func (a AuditAgentAction) valid() bool {
	return a.SessionID != "" || a.AgentID != "" || a.Operation != ""
}

type auditAgentActionContextKey struct{}

// WithAuditAgentAction attaches the agent action mutations in ctx belong to.
func WithAuditAgentAction(ctx context.Context, a AuditAgentAction) context.Context {
	if !a.valid() {
		return ctx
	}
	return context.WithValue(ctx, auditAgentActionContextKey{}, a)
}

// AuditAgentActionFromContext returns the agent action attached to ctx.
func AuditAgentActionFromContext(ctx context.Context) (AuditAgentAction, bool) {
	if ctx == nil {
		return AuditAgentAction{}, false
	}
	a, ok := ctx.Value(auditAgentActionContextKey{}).(AuditAgentAction)
	if !ok || !a.valid() {
		return AuditAgentAction{}, false
	}
	return a, true
}
