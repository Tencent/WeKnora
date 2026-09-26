package pluginapi

import (
	"encoding/json"
	"time"
)

// Envelope is the body of every call. Plugins must be stateless: everything
// about the tenant comes in the envelope, and credentials must not be cached
// across calls.
type Envelope struct {
	Context Context `json:"context"`
	Config  Config  `json:"config"`
	// Input is the endpoint-specific request, such as a SearchInput.
	Input json.RawMessage `json:"input,omitempty"`
}

// Context describes who is calling and until when.
type Context struct {
	TenantID  uint64 `json:"tenantId"`
	UserID    string `json:"userId,omitempty"`
	Locale    string `json:"locale,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	// Deadline is when WeKnora stops waiting; plugins should give up by then.
	Deadline *time.Time `json:"deadline,omitempty"`
	// Host lets the plugin call back into WeKnora (Host API); absent when
	// the plugin was granted no scopes.
	Host *HostAccess `json:"host,omitempty"`
	// Webhooks are this workspace's URLs of the plugin's webhooks, by
	// webhook ID, for registering them with a third party. Present only
	// when WeKnora knows its public address.
	Webhooks map[string]string `json:"webhooks,omitempty"`
}

// HostAccess is where and how a plugin calls the WeKnora Host API.
type HostAccess struct {
	URL string `json:"url"`
	// Token is a short-lived bearer token bound to this tenant and the
	// plugin's granted scopes.
	Token string `json:"token"`
}

// Config carries the three configuration scopes separately, secrets already
// decrypted. They are never merged, so a key means the same thing wherever
// it appears.
type Config struct {
	// System is the platform-wide configuration (config.system).
	System map[string]any `json:"system,omitempty"`
	// Tenant is the workspace configuration (config.tenant).
	Tenant map[string]any `json:"tenant,omitempty"`
	// Instance is the configuration of the integration instance being used:
	// a data source, a web search provider.
	Instance map[string]any `json:"instance,omitempty"`
}

// Output wraps a successful answer.
type Output struct {
	Output json.RawMessage `json:"output"`
}

// Manifest is what GET /v1/manifest answers: enough for WeKnora to check that
// the process it started is the package it installed.
type Manifest struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
	// Contributes lists the contribution IDs the process serves per
	// extension point, e.g. {"connectors": ["rss"]}.
	Contributes map[string][]string `json:"contributes"`
}

// Health is what GET /v1/health answers.
type Health struct {
	Status string `json:"status"` // "ok"
}
