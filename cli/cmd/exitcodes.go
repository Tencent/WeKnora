package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

type exitCodeRow struct {
	Code        int    `json:"code"`
	Meaning     string `json:"meaning"`
	ErrorTypes  string `json:"error_types,omitempty"`
	AgentAction string `json:"agent_action"`
}

func exitCodeRows() []exitCodeRow {
	return []exitCodeRow{
		{0, "success", "", "continue"},
		{1, "typed local.* / operation.failed / unclassified", "local.*, operation.failed, operation.cancelled, server.session_create_failed, internal.error", "read stderr, decide retry/abort"},
		{2, "flag / argument validation error (cobra parse: unknown flag, arg count, missing required flag)", "input.invalid_argument (same type as exit 5; distinguish by exit code)", "re-check weknora <cmd> --help"},
		{3, "authentication / authorization", "auth.*", "re-auth (weknora auth login), then retry"},
		{4, "resource not found", "resource.not_found", "verify the resource id"},
		{5, "invalid input value (typed validation, not a parse error)", "input.* (other than confirmation_required)", "adjust args, retry"},
		{6, "rate limited", "server.rate_limited", "back off, retry"},
		{7, "server / network error", "server.* (excl. rate_limited→6, session_create_failed→1), network.*", "transient — retry with backoff"},
		{10, "confirmation required (high-risk write)", "input.confirmation_required", "ask the human; retry with -y only after explicit approval"},
		{124, "operation timeout", "operation.timeout", "raise --timeout or check the underlying job"},
		{130, "cancelled by signal (SIGINT/SIGTERM)", "", "stop, do not retry"},
	}
}

// newCmdExitCodes builds the `weknora exit-codes` help-topic command.
// `weknora help exit-codes` renders Long for humans; running it directly
// emits the machine-readable matrix (json by default, per the agent-first
// output contract). Listed in the command tree (not hidden) so humans have a
// discoverable path to it via `weknora help`, not only the README.
func newCmdExitCodes() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exit-codes",
		Short: "Exit code matrix and the agent action for each",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			rows := exitCodeRows()
			if fopts.WantsJSON() {
				return fopts.Emit(iostreams.IO.Out, rows, &output.Meta{Count: output.IntPtr(len(rows))})
			}
			tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "CODE\tMEANING\tERROR TYPES\tAGENT ACTION")
			for _, r := range rows {
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.Code, r.Meaning, r.ErrorTypes, r.AgentAction)
			}
			return tw.Flush()
		},
	}
	cmdutil.AddFormatFlag(cmd, "code", "meaning", "error_types", "agent_action")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:  "Machine-readable exit code matrix: what each code means and what to do next.",
		Examples: []string{"weknora exit-codes", "weknora help exit-codes"},
		Output:   "envelope.data is an array of {code, meaning, error_types, agent_action}",
	})
	cmd.Long = "Exit codes and the agent action for each:\n\n" + exitCodesLongTable()
	return cmd
}

func exitCodesLongTable() string {
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	for _, r := range exitCodeRows() {
		fmt.Fprintf(tw, "  %d\t%s\t%s\n", r.Code, r.Meaning, r.AgentAction)
	}
	tw.Flush()
	return sb.String()
}
