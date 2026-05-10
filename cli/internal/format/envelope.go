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
//
// v0.2 ADR-3 added Notice / Risk / DryRun, borrowed from lark-cli's envelope
// (https://github.com/larksuite/cli/blob/main/internal/output/envelope.go).
// All three are absent on read-only commands; write commands populate Risk
// even on success so agents can record what action ran.
type Envelope struct {
	OK     bool       `json:"ok"`
	Data   any        `json:"data,omitempty"`
	Error  *ErrorBody `json:"error,omitempty"`
	Meta   *Meta      `json:"_meta,omitempty"`
	Notice *Notice    `json:"_notice,omitempty"`
	Risk   *Risk      `json:"risk,omitempty"`
	// DryRun is intentionally NOT omitempty: AGENTS.md documents
	// `dry_run: false` as the literal default in the schema example, so
	// agents that pin the field's presence aren't surprised by it
	// disappearing on non-dry-run envelopes.
	DryRun bool `json:"dry_run"`
}

// Notice carries system-level advisories independent of the command outcome:
// CLI update available, server-CLI version skew, etc. Agents read these to
// surface upgrade prompts to users without polluting `data`.
type Notice struct {
	Update      *UpdateNotice      `json:"update,omitempty"`
	VersionSkew *VersionSkewNotice `json:"version_skew,omitempty"`
}

// UpdateNotice indicates a newer CLI version is available.
type UpdateNotice struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
}

// VersionSkewNotice indicates the server is behind the CLI within the compat
// window. `Level` mirrors doctor's status semantics: "warn" (degraded but
// functional) or "error" (out of compat).
type VersionSkewNotice struct {
	Client string `json:"client"`
	Server string `json:"server"`
	Level  string `json:"level"`
}

// Risk classifies the operation the user is performing — not the error.
// Agents inspect this on every envelope. When Level == RiskHighRiskWrite and
// the operation requires confirmation (no -y), the CLI exits 10. See
// cli/AGENTS.md "Exit codes" and lark-cli's
// skills/lark-shared/SKILL.md.
type Risk struct {
	Level  RiskLevel `json:"level"`
	Action string    `json:"action,omitempty"`
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

// RiskLevel classifies an operation. Agents use this to decide whether to
// retry, require explicit user approval, or stop. Values are aligned with
// lark-cli's risk taxonomy
// (https://github.com/larksuite/cli/blob/main/internal/output/envelope.go).
type RiskLevel string

const (
	RiskRead          RiskLevel = "read"
	RiskWrite         RiskLevel = "write"
	RiskHighRiskWrite RiskLevel = "high-risk-write"
)

// ErrorBody is the failure shape. `code` is a stable namespaced ID (e.g.
// "auth.unauthenticated"); `hint` is an actionable next step. Operation-level
// risk lives at the envelope level (Envelope.Risk), not here.
type ErrorBody struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Hint       string         `json:"hint,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
	Context    string         `json:"context,omitempty"`
	Retryable  bool           `json:"retryable,omitempty"`
	ConsoleURL string         `json:"console_url,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

// WriteEnvelope serializes env as one-line JSON to w. Used for non-TTY output
// and for `--json` per-command mode (the omnibus `--agent` mode-switch was
// removed in v0.2 ADR-3; see cli/AGENTS.md for the agent contract).
func WriteEnvelope(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// Success constructs a success envelope.
func Success(data any, meta *Meta) Envelope {
	return Envelope{OK: true, Data: data, Meta: meta}
}

// SuccessWithRisk is Success + a per-operation Risk classification. Used by
// every write command (kb create/delete, doc upload/delete, api POST/PUT/
// DELETE, ...) so an agent reading any envelope can tell what kind of
// operation produced it without parsing the data shape.
func SuccessWithRisk(data any, meta *Meta, risk *Risk) Envelope {
	return Envelope{OK: true, Data: data, Meta: meta, Risk: risk}
}

// Failure constructs a failure envelope.
func Failure(err *ErrorBody) Envelope {
	return Envelope{OK: false, Error: err}
}
