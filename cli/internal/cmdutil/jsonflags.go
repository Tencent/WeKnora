package cmdutil

import (
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

// JSONOptions captures the resolved --json + --jq state after CheckJSONFlags.
// A non-nil value means the user requested JSON output; Fields restricts
// each top-level object (or each element of a top-level array) to the
// listed keys; JQ is a jq expression applied to the final JSON.
type JSONOptions struct {
	Fields []string
	JQ     string
}

// Enabled reports whether the caller asked for JSON output. Convenience
// shorthand for `opts != nil`.
func (o *JSONOptions) Enabled() bool { return o != nil }

// Emit serializes v as bare JSON to w, honoring the resolved field-filter
// and jq expression. Equivalent to calling format.WriteJSONFiltered with
// o.Fields / o.JQ, but lets call sites stay free of the format import.
// Safe to call on a nil receiver in case the caller composes it with
// Enabled().
func (o *JSONOptions) Emit(w io.Writer, v any) error {
	if o == nil {
		return format.WriteJSON(w, v)
	}
	return format.WriteJSONFiltered(w, v, o.Fields, o.JQ)
}

// jsonNoOptSentinel marks a bare `--json` (no comma-separated values after
// it). pflag's NoOptDefVal mechanism stores this sentinel into the slice;
// CheckJSONFlags then maps it to "no field filter" (full payload).
//
// Field discovery moves to per-command `--help` "JSON fields available"
// sections rendered by AddJSONFlags.
const jsonNoOptSentinel = "\x00json-no-value"

// AddJSONFlags registers --json and --jq on cmd.
//
//   - `--json`           → bare JSON payload, no field filter
//   - `--json=id,name`  → each object restricted to the listed fields
//   - `--jq <expr>`      → apply a jq expression to the JSON; requires
//     --json to be set explicitly
//
// `fields` is the set of available fields the user may pass; rendered in
// the command's help. Pass nil to skip the help annotation (uncommon).
func AddJSONFlags(cmd *cobra.Command, fields []string) {
	f := cmd.Flags()
	// Backticks reserved for pflag's UnquoteUsage to extract the varname;
	// avoid them in the description so the help doesn't render the flag
	// name twice.
	f.StringSlice("json", nil, "Output bare JSON (--json=id,name to project `fields`)")
	f.Lookup("json").NoOptDefVal = jsonNoOptSentinel
	f.StringP("jq", "q", "", "Filter JSON output using a jq `expression` (requires --json)")

	if len(fields) > 0 {
		sorted := append([]string(nil), fields...)
		sort.Strings(sorted)
		// Append to Long without overwriting per-command prose.
		hdr := "\n\nJSON fields available via `--json=id,name,...`:\n  " +
			strings.Join(sorted, "\n  ")
		if cmd.Long != "" {
			cmd.Long += hdr
		} else {
			cmd.Long = strings.TrimSpace(cmd.Short) + hdr
		}
	}
}

// CheckJSONFlags resolves the --json + --jq state from cmd. Returns:
//   - (nil, nil)            neither flag set (human output mode)
//   - (*JSONOptions, nil)   --json set (possibly with --jq)
//   - (nil, error)          --jq without --json (plain error, exit 1)
//
// Bare `--json` yields Fields == nil (no field filter). Explicit field
// list yields Fields == []string{"id", "name", ...} (filter applied).
func CheckJSONFlags(cmd *cobra.Command) (*JSONOptions, error) {
	f := cmd.Flags()
	jsonFlag := f.Lookup("json")
	jqFlag := f.Lookup("jq")
	if jsonFlag == nil {
		return nil, nil
	}
	if jsonFlag.Changed {
		sv, ok := jsonFlag.Value.(pflag.SliceValue)
		if !ok {
			return nil, errors.New("internal: --json flag is not a StringSlice")
		}
		raw := sv.GetSlice()
		opts := &JSONOptions{}
		// Strip the bare-flag sentinel if present.
		for _, v := range raw {
			if v == jsonNoOptSentinel {
				continue
			}
			opts.Fields = append(opts.Fields, v)
		}
		if jqFlag != nil {
			opts.JQ = jqFlag.Value.String()
		}
		return opts, nil
	}
	if jqFlag != nil && jqFlag.Changed {
		// Plain error, exit 1 (not FlagError → exit 2): the flag itself
		// parsed fine, the combination is what's wrong.
		return nil, errors.New("cannot use `--jq` without specifying `--json`")
	}
	return nil, nil
}
