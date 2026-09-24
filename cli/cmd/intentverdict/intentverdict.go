// Package intentverdictcmd holds the `weknora intent-verdicts` command tree:
// corpus export for the IntentGate data flywheel (T61, issue #24).
//
// These are read-only operator commands (Admin+ on the server). The export
// leaf downloads the (坏调用, 正确调用, 理由) corpus grouped by judge model
// for distillation / evaluation; the wire shape is defined by the server's
// /intent-verdicts/export endpoint.
package intentverdictcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora intent-verdicts` parent and registers leaves.
// Called from cli/cmd/root.go.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intent-verdicts",
		Short: "Export IntentGate verdict corpus (operator, Admin+)",
		Long: `Export the IntentGate verdict log as a distillation corpus. Every row is
one tool-call verdict: (prompt, tool_call, verdict, reason, human_override,
judge_model). Rows are grouped/filtered by judge model because verdict quality
varies with the judge model that produced it.`,
	}
	cmd.AddCommand(NewCmdExport(f))
	return cmd
}
