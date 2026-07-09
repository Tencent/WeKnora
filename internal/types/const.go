package types

// ContextKey defines a type for context keys to avoid string collision
type ContextKey string

const (
	// TenantIDContextKey is the context key for tenant ID
	TenantIDContextKey ContextKey = "TenantID"
	// TenantInfoContextKey is the context key for tenant information
	TenantInfoContextKey ContextKey = "TenantInfo"
	// RequestIDContextKey is the context key for request ID
	RequestIDContextKey ContextKey = "RequestID"
	// LoggerContextKey is the context key for logger
	LoggerContextKey ContextKey = "Logger"
	// UserContextKey is the context key for user information
	UserContextKey ContextKey = "User"
	// UserIDContextKey is the context key for user ID
	UserIDContextKey ContextKey = "UserID"
	// PrincipalContextKey is the context key for the terminal caller principal.
	PrincipalContextKey ContextKey = "Principal"
	// TenantAPIKeyScopeContextKey carries per-API-key operation and KB scopes.
	TenantAPIKeyScopeContextKey ContextKey = "TenantAPIKeyScope"
	// TenantRoleContextKey is the context key for the caller's TenantRole
	// in the currently active tenant (loaded by the auth middleware from
	// the tenant_members table). See TenantRoleFromContext.
	TenantRoleContextKey ContextKey = "TenantRole"
	// SessionTenantIDContextKey is the context key for session owner's tenant ID.
	// When set (e.g. in pipeline with shared agent), session/message lookups use this instead of TenantIDContextKey.
	SessionTenantIDContextKey ContextKey = "SessionTenantID"
	// EmbedQueryContextKey is the context key for embedding query text
	EmbedQueryContextKey ContextKey = "EmbedQuery"
	// LanguageContextKey is the context key for user language preference (e.g. "zh-CN", "en-US")
	LanguageContextKey ContextKey = "Language"
	// EmbedVisitorContextKey is the anonymous visitor id for embed OAuth isolation.
	EmbedVisitorContextKey ContextKey = "EmbedVisitorID"
	// LangfuseTraceContextKey carries the active Langfuse *Trace across the
	// request lifecycle. Defined here (not inside the langfuse package) so
	// that logger.CloneContext can preserve it without importing langfuse.
	LangfuseTraceContextKey ContextKey = "LangfuseTrace"
	// SystemAdminContextKey is the context key indicating whether the user is a system administrator
	SystemAdminContextKey ContextKey = "SystemAdmin"
	// BackgroundTaskContextKey marks a context whose model calls originate from
	// an asynq background worker (document parse / summary / question / graph /
	// multimodal enrichment) rather than a user-facing HTTP request. The chat
	// concurrency governor uses this to throttle only background LLM traffic,
	// leaving interactive chat latency untouched. See WithBackgroundTask.
	BackgroundTaskContextKey ContextKey = "BackgroundTask"
	// MCPOAuthNonInteractiveContextKey marks a request whose channel cannot
	// resolve an in-conversation MCP OAuth prompt (e.g. an IM bot: there is no
	// live client to click "Authorize" and call the resolve endpoint). When set,
	// the agent emits a one-shot authorization notice and continues instead of
	// blocking until the OAuth wait times out. See IsMCPOAuthNonInteractive.
	MCPOAuthNonInteractiveContextKey ContextKey = "MCPOAuthNonInteractive"
)

// String returns the string representation of the context key
func (c ContextKey) String() string {
	return string(c)
}
