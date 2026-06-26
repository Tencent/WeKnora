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
	OK bool `json:"ok"`
	// Status is the batch tri-state outcome: "success" (all items ok),
	// "partial" (some ok, some failed), or "error" (all failed). Set only by
	// batch commands; omitted on ordinary single-result envelopes. `ok` stays
	// authoritative — Status is an additive convenience for agents triaging
	// multi-target results.
	Status  string `json:"status,omitempty"`
	Data    any    `json:"data,omitempty"`
	Meta    *Meta  `json:"meta,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// ErrorEnvelope is the error-path stderr envelope. See AGENTS.md
// "Stderr (error path)" for the full wire contract.
type ErrorEnvelope struct {
	OK    bool       `json:"ok"`
	Error *ErrDetail `json:"error"`
}

// Meta carries optional metadata in success envelopes.
type Meta struct {
	// Count and TotalCount are *int so zero is serialized when explicitly set
	// by list commands (omitempty on *int omits only nil, not zero). This keeps
	// the agent contract stable: an empty list still emits count/total_count as 0
	// instead of dropping the key. Non-list / dry-run metas leave them nil so they
	// are omitted. Mirrors the Successes/Failures pointer pattern below.
	Count      *int `json:"count,omitempty"`
	HasMore    bool `json:"has_more,omitempty"`
	TotalCount *int `json:"total_count,omitempty"`
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
	Type    string `json:"type"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	// RetryArgv is a directly-executable argv array (e.g.
	// ["weknora","auth","login"]) so an agent can exec it without
	// shell-splitting or quote-handling. Distinct from the prose Hint.
	RetryArgv         []string `json:"retry_argv,omitempty"`
	RetryAfterSeconds int      `json:"retry_after_seconds,omitempty"`
	// Retryable indicates whether re-running the SAME command may succeed:
	// true for transient failures (timeouts, rate limits, transport), false
	// for deterministic ones (auth, bad input, not-found), omitted (nil) when
	// genuinely unknown.
	Retryable *bool       `json:"retryable,omitempty"`
	Risk      *RiskDetail `json:"risk,omitempty"`
	Detail    any         `json:"detail,omitempty"`
}

// RiskDetail tags high-risk writes for the agent protocol. Surfaces in
// error.risk on confirmation_required errors.
// Level: only "destructive" is emitted; "read" / "write" slots reserved.
type RiskDetail struct {
	Level  string `json:"level"`
	Action string `json:"action"`
}

// NewEnvelope assembles a success Envelope with the given data + optional
// meta + profile. Single source of construction so callers that need the
// envelope value (e.g. jq filtering) stay in sync with WriteEnvelope when
// fields are added.
func NewEnvelope(data any, meta *Meta, profile string) Envelope {
	return Envelope{
		OK:      true,
		Data:    data,
		Meta:    meta,
		Profile: profile,
	}
}

// WriteEnvelope writes a success envelope to w. Caller sets data + optional meta.
//
// When profile is non-empty, the envelope includes a "profile" field.
// indent: if true, output is multi-line (TTY mode); else compact (pipe mode).
func WriteEnvelope(w io.Writer, data any, meta *Meta, indent bool, profile string) error {
	return writeJSON(w, NewEnvelope(data, meta, profile), indent)
}

// WriteErrorEnvelope writes an error envelope to w (typically stderr).
func WriteErrorEnvelope(w io.Writer, err *ErrDetail, indent bool) error {
	env := ErrorEnvelope{
		OK:    false,
		Error: err,
	}
	return writeJSON(w, env, indent)
}

// IntPtr returns a pointer to i. Used by list commands to set Meta.Count /
// Meta.TotalCount so that a zero count still serializes (omitempty on *int
// omits only nil). Mirrors the Successes/Failures pointer pattern.
func IntPtr(i int) *int { return &i }

func writeJSON(w io.Writer, v any, indent bool) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}
