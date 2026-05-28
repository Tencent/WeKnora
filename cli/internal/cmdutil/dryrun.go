// Package-level note:
//
// AddDryRunFlag + EmitDryRun + DryRunPlan + HandleDryRun support the
// --dry-run contract for mutation commands:
//   - Each mutation cobra command registers --dry-run via AddDryRunFlag.
//   - RunE early-exits (BEFORE ConfirmDestructive / Factory.Client() /
//     ResolveKB) when opts.DryRun is true, calling EmitDryRun with a
//     DryRunPlan describing the would-be action.
//   - The api command additionally rejects --dry-run on GET (FlagError).
//
// Side-effect suppression: the dry-run path must NOT call the SDK, write
// the keyring, write .weknora/project.yaml, or write download files.
package cmdutil

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

// DryRunPlan describes the would-be action for envelope meta.plan.
// Field tags use omitempty for api-extra fields so mutation commands
// emit only {action, args} and api commands emit {action, method, path, body}.
type DryRunPlan struct {
	Action string         `json:"action"`
	Args   map[string]any `json:"args,omitempty"`
	Method string         `json:"method,omitempty"`
	Path   string         `json:"path,omitempty"`
	Body   any            `json:"body,omitempty"`
}

// AddDryRunFlag registers --dry-run on a cobra command, binding to a bool
// pointer. A bool is sufficient because the CLI is single-tenant and
// self-hosted — there is no client/server distinction to encode.
func AddDryRunFlag(cmd *cobra.Command, dest *bool) {
	cmd.Flags().BoolVar(dest, "dry-run", false,
		"Preview the action without executing (offline; no SDK calls, no file IO)")
}

// EmitDryRun writes the dry-run envelope to w via FormatOptions.Emit, so the
// envelope inherits the standard _notice injection, TTY indent, --jq filter,
// and profile fields like every other success envelope. fopts may be nil for
// callers that have no resolved FormatOptions (tests, error paths); a JSON-mode
// default is used in that case.
//
// The envelope shape is {ok:true, meta:{dry_run:true, plan:{...}}, ...}; data
// is intentionally omitted (omitempty on Envelope.Data) since dry-run produced
// no real data.
//
// Caller must ensure NO side effects occurred before this point: no SDK call,
// no file write, no keyring touch.
func EmitDryRun(w io.Writer, fopts *FormatOptions, plan DryRunPlan) error {
	planMap, err := planToMap(plan)
	if err != nil {
		return fmt.Errorf("dry-run: encode plan: %w", err)
	}
	meta := &output.Meta{
		DryRun: true,
		Plan:   planMap,
	}
	if fopts == nil {
		fopts = &FormatOptions{Mode: FormatJSON}
	}
	// NDJSON mode is meaningless for a single dry-run envelope (the contract
	// emits exactly one object). Fall back to JSON envelope shape so meta.plan
	// is surfaced; this matches what every other mutation emit does.
	if fopts.Mode == FormatNDJSON {
		jsonOpts := *fopts
		jsonOpts.Mode = FormatJSON
		return jsonOpts.Emit(w, nil, meta)
	}
	return fopts.Emit(w, nil, meta)
}

// HandleDryRun bundles the dry-run preamble for mutation command RunE:
// FormatOptions resolution + envelope emit. Returns (handled, err) — when
// handled is true, RunE should return err immediately without doing any
// SDK / Factory.Client() / ResolveKB / ConfirmDestructive work.
//
// Pattern:
//
//	if handled, err := cmdutil.HandleDryRun(cmd, opts.DryRun, cmdutil.DryRunPlan{
//	    Action: "kb.create",
//	    Args: map[string]any{"name": opts.Name},
//	}); handled {
//	    return err
//	}
//
// The helper invokes ONLY local config reads (CheckFormatFlag,
// IsStdoutTTY) — no SDK calls, no keyring writes, no file IO. Safe to
// call before any other RunE logic.
//
// The helper re-resolves FormatOptions internally so callsites stay
// single-statement; this is intentionally redundant with the outer
// CheckFormatFlag the non-dry-run success path still needs. The duplication
// is cheap (local flag read) and is the price of consolidating the dry-run
// preamble into one helper.
func HandleDryRun(cmd *cobra.Command, dryRun bool, plan DryRunPlan) (handled bool, err error) {
	if !dryRun {
		return false, nil
	}
	fopts, err := CheckFormatFlag(cmd)
	if err != nil {
		return true, err
	}
	fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
	return true, EmitDryRun(iostreams.IO.Out, fopts, plan)
}

// planToMap converts a DryRunPlan to map[string]any so it can populate
// output.Meta.Plan (which is open-typed). Goes through json to honor the
// omitempty tags so api-only fields don't leak into mutation envelopes.
func planToMap(plan DryRunPlan) (map[string]any, error) {
	b, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
