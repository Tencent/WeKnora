package cmdutil

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

// Typed format modes. Defined as constants to keep mode comparisons
// type-safe and prevent string drift across call sites.
const (
	FormatText   = "text"
	FormatJSON   = "json"
	FormatNDJSON = "ndjson"
)

// FormatOptions captures the resolved --format + --jq state for a command.
// Mode is one of FormatText / FormatJSON / FormatNDJSON, or "" before
// ResolveDefault has been called.
type FormatOptions struct {
	Mode string
	JQ   string
}

// AddFormatFlag registers --format and --jq on cmd. Optional fieldHints are
// appended to cmd.Long as a documentation aid for --jq projection
// (e.g., `weknora kb list --format json --jq '.[] | {id, name}'`).
//
// Uses cmd.Flags() (local) rather than a root persistent flag — only
// commands that actually honor --format register it, so cobra rejects
// --format on others with "unknown flag" rather than silently ignoring it.
func AddFormatFlag(cmd *cobra.Command, fieldHints ...string) {
	cmd.Flags().String("format", "", "Output format: text | json | ndjson (default: text in TTY, json in pipe)")
	cmd.Flags().StringP("jq", "q", "", "Filter JSON output using a jq `expression` (requires --format json|ndjson)")

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
		case "", FormatText, FormatJSON, FormatNDJSON:
			fopts.Mode = v
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
// callers to choose between the JSON emit path and human text rendering.
func (o *FormatOptions) WantsJSON() bool {
	return o.Mode == FormatJSON || o.Mode == FormatNDJSON
}

// Emit serializes v according to the resolved Mode + JQ. For ndjson, slice
// values are split element-per-line (per ndjson.org); for json, v is
// emitted as-is. When JQ is set, the jq expression runs against the
// marshaled JSON and each result becomes a line. Text mode is the caller's
// job — Emit returns an error so a missed dispatch surfaces loudly.
func (o *FormatOptions) Emit(w io.Writer, v any) error {
	switch o.Mode {
	case FormatJSON:
		return format.WriteJSONFiltered(w, v, nil, o.JQ)
	case FormatNDJSON:
		// jq output is naturally line-per-result → already valid NDJSON.
		// Without jq, split arrays per ndjson.org (one object per line).
		if o.JQ != "" {
			return format.WriteJSONFiltered(w, v, nil, o.JQ)
		}
		return format.WriteNDJSON(w, v)
	case FormatText:
		return fmt.Errorf("FormatOptions.Emit: cannot emit text mode as JSON; caller must render text separately")
	default:
		return fmt.Errorf("FormatOptions.Emit: unknown mode %q", o.Mode)
	}
}

// ResolveDefault fills in Mode using TTY detection when the user didn't pass
// --format explicitly. Pass iostreams.IO.IsStdoutTTY() as isTTY.
// No-op if Mode is already set.
//
// `--jq` without an explicit `--format` forces JSON regardless of TTY:
// the filter has no meaning in text mode, so silently dropping it would
// surprise the user. The `--jq` + `--format text` combination is caught
// up-front by CheckFormatFlag.
func (o *FormatOptions) ResolveDefault(isTTY bool) {
	if o.Mode != "" {
		return
	}
	if o.JQ != "" {
		o.Mode = FormatJSON
		return
	}
	if isTTY {
		o.Mode = FormatText
	} else {
		o.Mode = FormatJSON
	}
}
