package types

import "sort"

// contextCloneAcrossDetach answers one question for every context key in the
// codebase: when a request's context is detached for background work by
// logger.CloneContext, does this key travel with it?
//
// It is an exhaustive decision table rather than a list of keys to keep,
// because the two ways of getting this wrong fail in opposite directions and
// neither default is safe. Dropping a key that carries a RESTRICTION fails
// open: an agent's memory opt-out or a session's sandbox tenant that goes
// missing lets background work do what the request forbade. Keeping a key that
// carries a GRANT fails open the other way: a per-request access decision or a
// suppressed-audit marker that outlives its request widens what the background
// work may do. Since no default is universally right, every key states its
// own answer here, next to where such keys are declared, and
// TestEveryContextKeyDeclaresACloneDecision fails when a new key arrives
// without one instead of letting it opt out by silence.
//
// false is a valid, meaningful answer — it means "deliberately does not
// survive a detach" — and is not the same as being absent.
var contextCloneAcrossDetach = map[ContextKey]bool{
	// Caller identity and workspace scope. Background work runs as the same
	// principal in the same workspace, so all of this has to survive; a
	// detached goroutine that loses its tenant reads another tenant's rows or
	// none at all.
	TenantIDContextKey:    true,
	TenantInfoContextKey:  true,
	UserContextKey:        true,
	UserIDContextKey:      true,
	PrincipalContextKey:   true,
	SystemAdminContextKey: true,
	// TenantRoleContextKey: the caller's resolved role in the active tenant
	// (PR 2 #1303). Must survive for the same reason as TenantIDContextKey —
	// any handler that does `ctx := logger.CloneContext(c.Request.Context())`
	// and then reads the role via TenantRoleFromContext would otherwise see
	// the type-zero TenantRole and fall back to Viewer, blocking even Owners.
	TenantRoleContextKey: true,
	// Per-API-key operation and KB scopes: a restriction, so dropping it would
	// hand background work broader reach than the key it came from.
	TenantAPIKeyScopeContextKey: true,

	// Session scope. SessionTenantID re-scopes session/message lookups, while
	// SandboxTenantID keys the session→sandbox binding to the session owner
	// even when a shared agent borrowed another tenant. Dropping the latter
	// would silently re-key every binding onto the borrowed tenant, because
	// setupSSEStream builds its async context through CloneContext, and
	// abandon a paused MicroVM that keeps billing.
	SessionIDContextKey:       true,
	SessionTenantIDContextKey: true,
	SandboxTenantIDContextKey: true,

	// Embed callers: the anonymous visitor id isolates embed OAuth, so losing
	// it would merge visitors together.
	EmbedQueryContextKey:   true,
	EmbedVisitorContextKey: true,

	// Diagnostics and presentation that background work is expected to keep.
	// The Langfuse trace in particular must stay alive so the LLM / embedder /
	// reranker / VLM / ASR wrappers attach their generations to the trace
	// GinMiddleware opened, rather than each auto-creating an orphan.
	LoggerContextKey:        true,
	RequestIDContextKey:     true,
	LanguageContextKey:      true,
	LangfuseTraceContextKey: true,

	// The agent-level opt-out from long-term memory. Recall is gated inside
	// the QA services, but extraction, the explicit "remember this" route and
	// document affinity all run from a context descended from a CloneContext.
	// Dropping this key would let an agent that cannot read memory keep
	// writing to it.
	MemoryDisabledContextKey: true,

	// Marks model calls as coming from an asynq worker so the per-model chat
	// concurrency governor throttles them, leaving interactive chat latency
	// alone. Document ingestion detaches mid-flight — knowledge_create hands
	// processChunks to a goroutine on a cloned context, and processChunks is
	// what vectorises every chunk. Dropping the mark there would let exactly
	// the ingestion storm the governor exists to contain run past it. Keeping
	// it can only over-throttle a context that is background by definition.
	BackgroundTaskContextKey: true,
	// Marks a channel that cannot resolve an in-conversation MCP OAuth prompt
	// (an IM bot has no live client to click "Authorize"). Dropping it makes
	// the agent block on the OAuth wait for every unauthorized service instead
	// of emitting its one-shot notice, so the failure is a stalled reply.
	// A detached context has no live client either, which is what this says.
	MCPOAuthNonInteractiveContextKey: true,

	// ---- Deliberately does not survive a detach ----
	//
	// false here is a decision, not an oversight: each of these describes one
	// request, one call or one attachment rather than the caller, so carrying
	// it into detached work would describe that work wrongly.

	// Labels for a single model request, set immediately before the call by
	// every call site that has an opinion. An inherited label is worse than no
	// label, because usage is aggregated by purpose and a background call
	// would be filed under whatever its parent was doing.
	LLMCallPurposeContextKey:             false,
	LLMPromptPrefixFingerprintContextKey: false,
	// Who is authoring one wiki page write. Absent deliberately means the wiki
	// ingest pipeline, which is what detached work should look like; an
	// inherited "user" would attribute a background rewrite to a person.
	WikiEditSourceContextKey: false,
	// The parser engine resolved from one agent's ChatParserEngineRules for
	// one attachment's file type. Read by the attachment processor on the same
	// context that set it, and meaningless for anything else.
	ChatParserEngineContextKey: false,
	// The authenticated embed channel. Every reader takes it straight off the
	// request context inside the embed handler that authenticated it; nothing
	// downstream of a detach reads it.
	EmbedChannelContextKey: false,
}

// ContextKeysClonedAcrossDetach returns the keys logger.CloneContext carries
// into a detached context, sorted so the result does not depend on map order.
func ContextKeysClonedAcrossDetach() []ContextKey {
	keys := make([]ContextKey, 0, len(contextCloneAcrossDetach))
	for key, clone := range contextCloneAcrossDetach {
		if clone {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// ContextCloneDecision reports whether the key survives a detach, and whether
// any decision has been recorded for it at all. The second return value is
// what the coverage test uses to tell a deliberate false from an omission.
func ContextCloneDecision(key ContextKey) (clone bool, declared bool) {
	clone, declared = contextCloneAcrossDetach[key]
	return clone, declared
}
