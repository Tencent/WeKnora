// Package output defines the symmetric envelope wire contract:
// success envelopes on stdout (Envelope) and error envelopes on
// stderr (ErrorEnvelope), plus NDJSON stream helpers.
package output

import (
	"encoding/json"
	"io"
)

// Envelope is the success-path stdout envelope. See AGENTS.md
// "Stdout (success path)" for the full wire contract.
type Envelope struct {
	OK      bool           `json:"ok"`
	Data    any            `json:"data,omitempty"`
	Meta    *Meta          `json:"meta,omitempty"`
	Notice  map[string]any `json:"_notice,omitempty"`
	Profile string         `json:"profile,omitempty"`
}

// ErrorEnvelope is the error-path stderr envelope. See AGENTS.md
// "Stderr (error path)" for the full wire contract.
type ErrorEnvelope struct {
	OK     bool           `json:"ok"`
	Error  *ErrDetail     `json:"error"`
	Notice map[string]any `json:"_notice,omitempty"`
}

// Meta carries optional metadata in success envelopes.
type Meta struct {
	Count      int    `json:"count,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
	TotalCount int    `json:"total_count,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	// Successes and Failures are *int so zero is serialized when explicitly set
	// by the batch path (omitempty on *int omits only nil, not zero).
	// Non-batch commands leave these nil so they are omitted from the envelope.
	Successes *int `json:"successes,omitempty"` // batch ops
	Failures  *int `json:"failures,omitempty"`  // batch ops
	// Dry-run preview fields. Populated by EmitDryRun (cmdutil/dryrun.go)
	// when --dry-run is set on a mutation command; omitted otherwise.
	DryRun bool           `json:"dry_run,omitempty"` // true when --dry-run; omitted otherwise
	Plan   map[string]any `json:"plan,omitempty"`    // would-call shape; open map (action required, other fields command-specific)
}

// ErrDetail describes a structured error. Embedded in ErrorEnvelope.Error
// and also surfaced in batch envelope per-item failures.
type ErrDetail struct {
	Type              string      `json:"type"`
	Message           string      `json:"message"`
	Hint              string      `json:"hint,omitempty"`
	RetryCommand      string      `json:"retry_command,omitempty"`
	RetryAfterSeconds int         `json:"retry_after_seconds,omitempty"`
	Risk              *RiskDetail `json:"risk,omitempty"`
	Detail            any         `json:"detail,omitempty"`
}

// RiskDetail tags high-risk writes for the agent protocol. Surfaces in
// error.risk on confirmation_required errors.
// Level: only "destructive" is emitted; "read" / "write" slots reserved.
type RiskDetail struct {
	Level  string `json:"level"`
	Action string `json:"action"`
}

// PendingNotice, if set, returns system-level notices to inject as the
// "_notice" field on every envelope. Currently nil — Task 4.x deferred
// the registration. Tests may set this directly.
var PendingNotice func() map[string]any

// GetNotice returns the current pending notice. Nil when nothing to report.
func GetNotice() map[string]any {
	if PendingNotice == nil {
		return nil
	}
	return PendingNotice()
}

// NewEnvelope assembles a success Envelope with the given data + optional
// meta + profile, injecting the pending _notice automatically. Single source
// of construction so callers that need the envelope value (e.g. jq filtering)
// stay in sync with WriteEnvelope when fields are added.
func NewEnvelope(data any, meta *Meta, profile string) Envelope {
	return Envelope{
		OK:      true,
		Data:    data,
		Meta:    meta,
		Notice:  GetNotice(),
		Profile: profile,
	}
}

// WriteEnvelope writes a success envelope to w. Caller sets data + optional meta;
// notice is injected from GetNotice() automatically.
//
// When profile is non-empty, the envelope includes a "profile" field.
// indent: if true, output is multi-line (TTY mode); else compact (pipe mode).
func WriteEnvelope(w io.Writer, data any, meta *Meta, indent bool, profile string) error {
	return writeJSON(w, NewEnvelope(data, meta, profile), indent)
}

// WriteErrorEnvelope writes an error envelope to w (typically stderr).
func WriteErrorEnvelope(w io.Writer, err *ErrDetail, indent bool) error {
	env := ErrorEnvelope{
		OK:     false,
		Error:  err,
		Notice: GetNotice(),
	}
	return writeJSON(w, env, indent)
}

func writeJSON(w io.Writer, v any, indent bool) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}
