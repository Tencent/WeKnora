package cmdutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

// FormatMode is the resolved --format value (typed enum).
type FormatMode string

const (
	FormatText   FormatMode = "text"
	FormatJSON   FormatMode = "json"
	FormatNDJSON FormatMode = "ndjson"
)

// DefaultFormatMode is the mode used when neither --format nor WEKNORA_FORMAT
// is set. Single source of truth shared by FormatOptions.ResolveDefault and
// cmd.resolveFormatEarly (the early cobra-parse-error path) so the two cannot
// drift on what "no flag" defaults to.
const DefaultFormatMode = FormatJSON

// FormatOptions captures the resolved --format + --jq state for a command.
// Mode is one of FormatText / FormatJSON / FormatNDJSON, or "" before
// ResolveDefault has been called.
type FormatOptions struct {
	Mode FormatMode
	JQ   string
	TTY  bool // ResolveDefault populates; Emit reads for indent decision
}

// AddFormatFlag attaches the --jq projection field hints (in cmd.Long) for
// commands that honor --format. The flags themselves are registered as
// persistent globals at the root, so this helper no longer re-registers
// them — it only appends documentation. Callers that don't need field
// hints can skip this call entirely.
func AddFormatFlag(cmd *cobra.Command, fieldHints ...string) {
	if len(fieldHints) > 0 {
		sorted := append([]string(nil), fieldHints...)
		sort.Strings(sorted)
		hdr := "\n\nJSON fields available (for --jq projection):\n  " +
			strings.Join(sorted, "\n  ")
		if cmd.Long != "" {
			cmd.Long += hdr
		} else {
			cmd.Long = strings.TrimSpace(cmd.Short) + hdr
		}
	}
}

// CheckFormatFlag resolves --format + --jq from cmd. Returns:
//   - (*FormatOptions{Mode:""}, nil)        flag not set; caller should call ResolveDefault
//   - (*FormatOptions{Mode:v,JQ:q}, nil)    valid values
//   - (nil, *FlagError)                     invalid --format, or --jq with explicit --format text
//
// --jq with --format unset is accepted: ResolveDefault below will promote
// the mode to FormatJSON so the filter has somewhere to apply.
func CheckFormatFlag(cmd *cobra.Command) (*FormatOptions, error) {
	fopts := &FormatOptions{}
	if f := cmd.Flags().Lookup("format"); f != nil {
		v := f.Value.String()
		switch v {
		case "":
			// unset; caller calls ResolveDefault
		case "text", "json", "ndjson":
			fopts.Mode = FormatMode(v)
		default:
			return nil, NewFlagError(fmt.Errorf("invalid --format %q: must be text | json | ndjson", v))
		}
	}
	if f := cmd.Flags().Lookup("jq"); f != nil {
		fopts.JQ = f.Value.String()
	}
	// --jq only meaningful for JSON-shaped output. Reject the explicit
	// `--format text --jq ...` combination; the `--jq` with --format unset
	// case is handled by ResolveDefault.
	if fopts.JQ != "" && fopts.Mode == FormatText {
		return nil, NewFlagError(errors.New("--jq requires --format json|ndjson"))
	}
	return fopts, nil
}

// WantsJSON reports whether the resolved mode is JSON or NDJSON. Used by
// callers to choose between the JSON emit path and text rendering.
func (o *FormatOptions) WantsJSON() bool {
	return o.Mode == FormatJSON || o.Mode == FormatNDJSON
}

// Emit serializes data wrapped in the success envelope (FormatJSON
// path) or as bare NDJSON lines (FormatNDJSON path). meta is optional
// (pass nil for mutation commands without batch counts).
//
// FormatJSON path: envelope is {ok:true, data:..., meta?:..., _notice?:...}.
// Indent is determined by o.TTY (populated by ResolveDefault).
// When o.JQ is set, jq evaluates against the full envelope JSON, so users
// project with ".data[]", ".meta.count", etc.
//
// FormatNDJSON path: emits one bare JSON object per line (no envelope).
// Matches the NDJSON event-passthrough contract used by streaming commands.
//
// FormatText path returns an error so a missed dispatch surfaces loudly.
func (o *FormatOptions) Emit(w io.Writer, data any, meta *output.Meta) error {
	switch o.Mode {
	case FormatJSON:
		if o.JQ != "" {
			return mapJQError(format.WriteJSONFiltered(w, output.NewEnvelope(data, meta, globalProfile), nil, o.JQ))
		}
		return output.WriteEnvelope(w, data, meta, o.TTY, globalProfile)
	case FormatNDJSON:
		if o.JQ != "" {
			return mapJQError(format.WriteJSONFiltered(w, data, nil, o.JQ))
		}
		return format.WriteNDJSON(w, data)
	case FormatText:
		return fmt.Errorf("FormatOptions.Emit: cannot emit text mode as JSON; caller must render human-readable separately")
	default:
		return fmt.Errorf("FormatOptions.Emit: unknown mode %q", o.Mode)
	}
}

// mapJQError converts a failure rooted in the user-supplied --jq expression
// (format.JQError) into a typed input.invalid_argument error (exit 5) so agents
// fix the expression instead of treating a bad --jq as a CLI bug. Non-jq errors
// (e.g. an internal serialization fault) pass through unchanged.
func mapJQError(err error) error {
	if err == nil {
		return nil
	}
	var jqe *format.JQError
	if errors.As(err, &jqe) {
		return NewError(CodeInputInvalidArgument, jqe.Error()).
			WithHint("invalid --jq expression; see https://jqlang.github.io/jq/manual/")
	}
	return err
}

// ResolveDefault fills in Mode when the caller has not explicitly set it:
//   - Mode defaults to FormatJSON
//   - TTY only affects the indent decision (auto-indent in TTY; compact in pipe)
//   - For human-readable rendering, pass --format text explicitly
func (o *FormatOptions) ResolveDefault(tty bool) {
	o.TTY = tty
	if o.Mode == "" {
		o.Mode = DefaultFormatMode
	}
}

// FromEnv reads WEKNORA_FORMAT and applies it when Mode hasn't been set
// by --format. Call between CheckFormatFlag and ResolveDefault.
//
// Invalid env values are silently ignored (the user's --format on a
// later invocation will still take precedence).
func (o *FormatOptions) FromEnv() {
	if o.Mode != "" {
		return
	}
	v := os.Getenv("WEKNORA_FORMAT")
	switch v {
	case "text", "json", "ndjson":
		o.Mode = FormatMode(v)
	}
}
