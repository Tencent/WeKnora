package cmdutil

import (
	"errors"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// JSONOptions captures the resolved --json + --jq state after CheckJSONFlags.
// A non-nil value means the user requested JSON output; Fields restricts
// data.items[*] when len(Fields) > 0; JQ is a jq expression applied to the
// final envelope JSON.
type JSONOptions struct {
	Fields []string
	JQ     string
}

// Enabled reports whether the caller asked for JSON output. Convenience
// shorthand for `opts != nil`.
func (o *JSONOptions) Enabled() bool { return o != nil }

// jsonNoOptSentinel marks a bare `--json` (no comma-separated values
// after it). pflag's NoOptDefVal mechanism stores this sentinel into the
// slice; CheckJSONFlags then maps it to "no field filter" (full envelope).
//
// This is a documented divergence from gh CLI, where bare `--json` errors
// with a field-list discovery prompt. weknora's envelope is itself the
// machine-readable contract (carries typed error.code / _meta / risk),
// so `--json` bare always producing the full envelope keeps v0.3 caller
// scripts and the typical agent pipe-to-jq pattern working. Field
// discovery moves to per-command `--help` "JSON fields:" sections.
const jsonNoOptSentinel = "\x00json-no-value"

// AddJSONFlags registers --json and --jq on cmd.
//
//   - `--json`           → full envelope (no field filter)
//   - `--json id,name`   → envelope with data.items[*] / data restricted
//                          to listed fields
//   - `--jq <expr>`      → applies a jq expression after marshaling;
//                          requires --json to be set explicitly
//
// `fields` is the set of available fields the user may pass; rendered in
// the command's help. Pass nil to skip the help annotation (uncommon).
func AddJSONFlags(cmd *cobra.Command, fields []string) {
	f := cmd.Flags()
	// Backticks reserved for pflag's UnquoteUsage to extract the varname;
	// avoid them in the description so the help doesn't render the flag
	// name twice.
	f.StringSlice("json", nil, "Output JSON envelope (bare for full; --json=id,name for `fields`)")
	f.Lookup("json").NoOptDefVal = jsonNoOptSentinel
	f.StringP("jq", "q", "", "Filter JSON output using a jq `expression` (requires --json)")

	if len(fields) > 0 {
		sorted := append([]string(nil), fields...)
		sort.Strings(sorted)
		// Append to Long without overwriting per-command prose.
		hdr := "\n\nJSON fields available via `--json id,name,...`:\n  " +
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
// Bare `--json` yields Fields == nil (full envelope). Explicit field list
// yields Fields == []string{"id", "name", ...} (filter applied).
//
// gh divergence: gh errors on bare --json with a discovery prompt. weknora
// treats bare --json as "full envelope, no filter" because the envelope is
// the contract.
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
		// gh parity: plain error, exit 1 (not FlagError → exit 2).
		return nil, errors.New("cannot use `--jq` without specifying `--json`")
	}
	return nil, nil
}
