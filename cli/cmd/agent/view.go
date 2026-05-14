package agentcmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// agentViewFields enumerates fields surfaced for `--json` discovery on
// `agent view`. Filter applies to the bare Agent object. Config sub-fields
// are intentionally omitted — too granular for naked projection; use
// `--jq '.config'` to reach them.
var agentViewFields = []string{
	"id", "name", "description", "avatar",
	"is_builtin", "tenant_id", "created_by",
	"created_at", "updated_at",
}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetAgent(ctx context.Context, agentID string) (*sdk.Agent, error)
}

// NewCmdView builds `weknora agent view <agent-id>`.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view <agent-id>",
		Short: "Show a custom agent's configuration",
		Long: `Renders the agent's metadata (id / name / description / created-by /
timestamps) plus a compact config summary (mode, model, allowed tools, KB
scope). Pass --json for the full Agent object including the nested config
struct — or --jq '.config' to extract just the config.`,
		Example: `  weknora agent view ag_abc
  weknora agent view ag_abc --json | jq '.config.allowed_tools'`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runView(c.Context(), jopts, cli, args[0])
		},
	}
	cmdutil.AddJSONFlags(cmd, agentViewFields)
	aiclient.SetAgentHelp(cmd, "Fetches an agent by ID. Returns the full sdk.Agent (with nested config) as a bare JSON object. Errors: resource.not_found when the agent ID does not exist or is not visible to the active tenant.")
	return cmd
}

func runView(ctx context.Context, jopts *cmdutil.JSONOptions, svc ViewService, agentID string) error {
	a, err := svc.GetAgent(ctx, agentID)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch agent %s", agentID)
	}
	if jopts.Enabled() {
		return format.WriteJSONFiltered(iostreams.IO.Out, a, jopts.Fields, jopts.JQ)
	}
	renderAgent(iostreams.IO.Out, a)
	return nil
}

// renderAgent prints a single agent in human-readable KV form. Empty
// fields are omitted (mirrors doc view / kb view); the Config block is
// compacted to its agent-mode-defining keys.
func renderAgent(w io.Writer, a *sdk.Agent) {
	fmt.Fprintf(w, "ID:           %s\n", a.ID)
	fmt.Fprintf(w, "Name:         %s\n", a.Name)
	if a.Description != "" {
		fmt.Fprintf(w, "Description:  %s\n", a.Description)
	}
	if a.IsBuiltin {
		fmt.Fprintln(w, "Builtin:      yes")
	}
	if a.CreatedBy != "" {
		fmt.Fprintf(w, "Created by:   %s\n", a.CreatedBy)
	}
	fmt.Fprintf(w, "Created at:   %s\n", a.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "Updated at:   %s\n", a.UpdatedAt.Format("2006-01-02 15:04:05"))
	if a.Config == nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Config:")
	if a.Config.AgentMode != "" {
		fmt.Fprintf(w, "  Mode:                %s\n", a.Config.AgentMode)
	}
	if a.Config.ModelID != "" {
		fmt.Fprintf(w, "  Model ID:            %s\n", a.Config.ModelID)
	}
	if a.Config.RerankModelID != "" {
		fmt.Fprintf(w, "  Rerank model ID:     %s\n", a.Config.RerankModelID)
	}
	if a.Config.KBSelectionMode != "" {
		fmt.Fprintf(w, "  KB selection mode:   %s\n", a.Config.KBSelectionMode)
	}
	if len(a.Config.KnowledgeBases) > 0 {
		fmt.Fprintf(w, "  Knowledge bases:     %v\n", a.Config.KnowledgeBases)
	}
	if len(a.Config.AllowedTools) > 0 {
		fmt.Fprintf(w, "  Allowed tools:       %v\n", a.Config.AllowedTools)
	}
	if a.Config.WebSearchEnabled {
		fmt.Fprintln(w, "  Web search:          enabled")
	}
}
