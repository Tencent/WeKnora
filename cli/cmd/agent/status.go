package agentcmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// AgentStatusResult is the shallow health snapshot for `agent status <id>`.
//
// One HTTP call: reachable + model_id. For downstream KB reachability
// verification use 'agent check <id>' (1 + N HTTP).
//
// model_reachable is intentionally OMITTED: the server has no per-agent
// test endpoint, so we cannot verify model reachability from the agent
// resource alone.
type AgentStatusResult struct {
	ID        string `json:"id"`
	Reachable bool   `json:"reachable"`
	ModelID   string `json:"model_id,omitempty"`
}

// AgentStatusService is the narrow SDK surface needed for agent status.
type AgentStatusService interface {
	GetAgent(ctx context.Context, id string) (*sdk.Agent, error)
}

var agentStatusFields = []string{"id", "reachable", "model_id"}

func NewCmdStatus(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <agent-id>",
		Short: "Show health status of a custom agent",
		Long: `Shallow health snapshot for an agent (1 HTTP call).

Returns: reachable / model_id.

For downstream KB reachability verification use 'weknora agent check <id>'
(active verification, 1 + N HTTP). For full agent config / metadata use
'weknora agent view <id>'.`,
		Example: `  weknora agent status ag_abc
  weknora agent status ag_abc --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			res, err := runAgentStatus(c.Context(), cli, args[0])
			if err != nil {
				return err
			}
			return emitAgentStatus(res, fopts, iostreams.IO.Out)
		},
	}
	cmdutil.AddFormatFlag(cmd, agentStatusFields...)
	return cmd
}

// runAgentStatus is the testable core. Never errors for "agent not
// reachable" — Reachable=false carries that signal.
func runAgentStatus(ctx context.Context, svc AgentStatusService, id string) (*AgentStatusResult, error) {
	a, err := svc.GetAgent(ctx, id)
	if err != nil {
		return &AgentStatusResult{ID: id, Reachable: false}, nil
	}
	res := &AgentStatusResult{ID: a.ID, Reachable: true}
	if a.Config != nil {
		res.ModelID = a.Config.ModelID
	}
	return res, nil
}

// emitAgentStatus renders res per --format. Mirrors emitStatus pattern from
// cli/cmd/kb/status.go (C3).
func emitAgentStatus(res *AgentStatusResult, fopts *cmdutil.FormatOptions, w io.Writer) error {
	switch fopts.Mode {
	case cmdutil.FormatJSON, cmdutil.FormatNDJSON:
		return fopts.Emit(w, res)
	case cmdutil.FormatText, "":
		return writeAgentStatusText(w, res)
	default:
		return fmt.Errorf("unsupported --format %q for agent status", fopts.Mode)
	}
}

func writeAgentStatusText(w io.Writer, res *AgentStatusResult) error {
	fmt.Fprintf(w, "ID:           %s\n", res.ID)
	fmt.Fprintf(w, "Reachable:    %v\n", res.Reachable)
	if !res.Reachable {
		return nil
	}
	if res.ModelID != "" {
		fmt.Fprintf(w, "Model:        %s\n", res.ModelID)
	}
	return nil
}

// compile-time check: SDK client satisfies AgentStatusService.
var _ AgentStatusService = (*sdk.Client)(nil)
