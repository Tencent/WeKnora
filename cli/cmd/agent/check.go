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

// AgentCheckResult is the deep-verification response for `agent check <id>`.
// Superset of AgentStatusResult: status fields + KBScopeAllReachable from
// probing each KB in agent.Config.KnowledgeBases. Verb split with
// `agent status`: status reads existing state cheaply, check actively
// verifies dependencies.
type AgentCheckResult struct {
	ID                  string `json:"id"`
	Reachable           bool   `json:"reachable"`
	ModelID             string `json:"model_id,omitempty"`
	KBScopeAllReachable *bool  `json:"kb_scope_all_reachable,omitempty"` // pointer so we can distinguish "not applicable" (e.g. agent unreachable) from "false"
}

// AgentCheckService is the narrow SDK surface needed for agent check.
type AgentCheckService interface {
	GetAgent(ctx context.Context, id string) (*sdk.Agent, error)
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
}

var agentCheckFields = []string{"id", "reachable", "model_id", "kb_scope_all_reachable"}

// NewCmdCheck builds `weknora agent check <id>`.
func NewCmdCheck(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <agent-id>",
		Short: "Verify a custom agent end-to-end (status + kb_scope reachability)",
		Long: `Active verification of a custom agent.

Performs 1 + N HTTP calls:
  1   GET /agents/{id} — reachable + model_id
  N   GET /kb/{kb_id} for each id in agent.config.knowledge_bases
      — sets kb_scope_all_reachable = true iff every probe succeeds

Use 'weknora agent status <id>' for a fast read-only health snapshot
(1 HTTP call, no KB probing). Use 'agent check' when you need to verify
the agent's downstream dependencies are all reachable.`,
		Example: `  weknora agent check ag_abc
  weknora agent check ag_abc --format json`,
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
			res, err := runAgentCheck(c.Context(), cli, args[0])
			if err != nil {
				return err
			}
			return emitAgentCheck(res, fopts, iostreams.IO.Out)
		},
	}
	cmdutil.AddFormatFlag(cmd, agentCheckFields...)
	return cmd
}

// runAgentCheck is the testable core. Never errors for "agent not
// reachable" — Reachable=false carries that signal.
func runAgentCheck(ctx context.Context, svc AgentCheckService, id string) (*AgentCheckResult, error) {
	a, err := svc.GetAgent(ctx, id)
	if err != nil {
		return &AgentCheckResult{ID: id, Reachable: false}, nil
	}
	res := &AgentCheckResult{ID: a.ID, Reachable: true}
	if a.Config != nil {
		res.ModelID = a.Config.ModelID
	}
	// Probe each KB in scope. Vacuously true when scope is empty or
	// config is nil.
	allOK := true
	if a.Config != nil {
		for _, kbID := range a.Config.KnowledgeBases {
			if _, err := svc.GetKnowledgeBase(ctx, kbID); err != nil {
				allOK = false
				break
			}
		}
	}
	res.KBScopeAllReachable = &allOK
	return res, nil
}

// emitAgentCheck renders res. Same dispatch as emitAgentStatus.
func emitAgentCheck(res *AgentCheckResult, fopts *cmdutil.FormatOptions, w io.Writer) error {
	switch fopts.Mode {
	case cmdutil.FormatJSON, cmdutil.FormatNDJSON:
		return fopts.Emit(w, res)
	case cmdutil.FormatText, "":
		return writeAgentCheckText(w, res)
	default:
		return fmt.Errorf("unsupported --format %q for agent check", fopts.Mode)
	}
}

func writeAgentCheckText(w io.Writer, res *AgentCheckResult) error {
	fmt.Fprintf(w, "ID:           %s\n", res.ID)
	fmt.Fprintf(w, "Reachable:    %v\n", res.Reachable)
	if !res.Reachable {
		return nil
	}
	if res.ModelID != "" {
		fmt.Fprintf(w, "Model:        %s\n", res.ModelID)
	}
	if res.KBScopeAllReachable != nil {
		fmt.Fprintf(w, "KB scope OK:  %v\n", *res.KBScopeAllReachable)
	}
	return nil
}

// compile-time check: SDK client satisfies AgentCheckService.
var _ AgentCheckService = (*sdk.Client)(nil)
