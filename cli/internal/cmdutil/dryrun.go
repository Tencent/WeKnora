package cmdutil

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// IsDryRun reports whether the global --dry-run flag was set on cmd or any
// of its parents. Write commands check this and skip the SDK call, returning
// an envelope with dry_run=true that describes the action that would have
// run. Read commands ignore --dry-run (no side effect to preview).
//
// Pattern from lark-cli's `--dry-run` (cmd/api/api.go DryRun field).
func IsDryRun(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	v, _ := cmd.Flags().GetBool("dry-run")
	return v
}

// EmitDryRun writes the canonical preview envelope for a write command. JSON
// mode emits SuccessWithRisk + DryRun=true; human mode prints the
// `[dry-run] would <risk.Action>` line to stdout, with " (high-risk)"
// appended automatically when risk.Level == RiskHighRiskWrite. Centralized
// so wire shape stays identical across kb create/delete, doc upload/delete,
// api, and any future write command.
func EmitDryRun(jsonOut bool, data any, meta *format.Meta, risk *format.Risk) error {
	out := iostreams.IO.Out
	if jsonOut {
		env := format.SuccessWithRisk(data, meta, risk)
		env.DryRun = true
		return format.WriteEnvelope(out, env)
	}
	line := risk.Action
	if risk.Level == format.RiskHighRiskWrite {
		line += " (high-risk)"
	}
	_, err := fmt.Fprintf(out, "[dry-run] would %s\n", line)
	return err
}
