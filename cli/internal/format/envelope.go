// Package format renders command output: JSON envelope, jq, template, table.
//
// Non-TTY default = JSON envelope (ADR-5). Envelope schema is the contract
// shared with `weknora mcp serve` tools and external agents.
package format

import (
	"encoding/json"
	"io"
)

// Envelope is the canonical success/failure shape returned by every command.
type Envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
	Meta  *Meta      `json:"_meta,omitempty"`
}

// Meta carries non-payload context fields useful to agents and observability.
type Meta struct {
	Context        string   `json:"context,omitempty"`
	TenantID       uint64   `json:"tenant_id,omitempty"`
	KBID           string   `json:"kb_id,omitempty"`
	RequestID      string   `json:"request_id,omitempty"`
	NextCursor     string   `json:"next_cursor,omitempty"`
	HasMore        bool     `json:"has_more,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	AppliedFilters []string `json:"applied_filters,omitempty"`
}

// RiskLevel classifies an error by the kind of operation that produced it.
// Agents use this to decide whether to retry, escalate, or stop.
type RiskLevel string

const (
	RiskRead        RiskLevel = "read"
	RiskMutating    RiskLevel = "mutating"
	RiskDestructive RiskLevel = "destructive"
)

// ErrorBody is the failure shape. `code` is a stable namespaced ID (e.g.
// "auth.unauthenticated"); `hint` is an actionable next step; `risk` flags
// destructive intent for agent self-governance.
type ErrorBody struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Hint       string         `json:"hint,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
	Context    string         `json:"context,omitempty"`
	Risk       RiskLevel      `json:"risk,omitempty"`
	Retryable  bool           `json:"retryable,omitempty"`
	ConsoleURL string         `json:"console_url,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

// WriteEnvelope serializes env as one-line JSON to w. Used for non-TTY output
// and for `--json` per-command mode (the omnibus `--agent` mode-switch was
// removed in v0.2 ADR-3; see docs/superpowers/specs/2026-05-09).
func WriteEnvelope(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// Success constructs a success envelope.
func Success(data any, meta *Meta) Envelope {
	return Envelope{OK: true, Data: data, Meta: meta}
}

// Failure constructs a failure envelope.
func Failure(err *ErrorBody) Envelope {
	return Envelope{OK: false, Error: err}
}
