package sessioncmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

var resolveFields = []string{"pending_id", "decision", "resolved"}

// decision values sent to the server and surfaced in the result/dry-run JSON.
const (
	decisionApprove = "approve"
	decisionReject  = "reject"
)

// ResolveOptions holds the parsed flags and positional argument for
// `session tool-approval resolve`.
type ResolveOptions struct {
	PendingID    string
	Reject       bool
	Reason       string
	ModifiedArgs string
	Yes          bool
	DryRun       bool
}

// ResolveService is the narrow SDK surface this command depends on.
type ResolveService interface {
	ResolveToolApproval(ctx context.Context, pendingID string, req *sdk.ResolveToolApprovalRequest) error
}

type resolveResult struct {
	PendingID string `json:"pending_id"`
	Decision  string `json:"decision"`
	Resolved  bool   `json:"resolved"`
}

const resolveLong = `Resolve a pending tool approval raised during an agent run.

When a server-side agent run (weknora session ask) needs to call a tool
that requires approval, the stream emits a tool-approval event carrying a
pending id and the run blocks. This command unblocks it: approve (default)
lets the tool call execute, --reject cancels it. After resolving, resume
the answer with weknora session continue-stream.

--modified-args replaces the tool call arguments on approve (JSON object).
It conflicts with --reject (rejected calls never execute).

AI agents: approving a tool call is the human-approval step itself — that
is exactly what the exit-10 protocol guards. Without -y/--yes the CLI
exits 10 and writes input.confirmation_required to stderr. Surface the
pending tool call to the user and only pass -y after their explicit
go-ahead.`

// NewCmdToolApproval builds the `session tool-approval` subtree.
func NewCmdToolApproval(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool-approval",
		Short: "Resolve pending tool approvals from agent runs",
	}
	cmd.AddCommand(newCmdResolve(f))
	return cmd
}

func newCmdResolve(f *cmdutil.Factory) *cobra.Command {
	opts := &ResolveOptions{}
	cmd := &cobra.Command{
		Use:   "resolve <pending-id>",
		Short: "Approve or reject a pending tool call (high-risk write)",
		Long:  resolveLong,
		Example: `  weknora session tool-approval resolve pend_abc -y                  # approve
  weknora session tool-approval resolve pend_abc --reject --reason "wrong target" -y
  weknora session tool-approval resolve pend_abc -y --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.PendingID = args[0]
			opts.Yes, _ = c.Flags().GetBool("yes")
			// Validate inputs BEFORE the dry-run gate so --dry-run previews a
			// valid action — an invalid --modified-args (e.g. "{}") must error
			// the same way whether or not --dry-run is passed.
			if _, err := validateModifiedArgs(opts); err != nil {
				return err
			}
			dryRunArgs := map[string]any{
				"pending_id": opts.PendingID,
				"decision":   decisionOf(opts.Reject),
				"reason":     opts.Reason,
			}
			if opts.ModifiedArgs != "" {
				dryRunArgs["modified_args"] = opts.ModifiedArgs
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "session.tool_approval.resolve",
				Args:   dryRunArgs,
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runResolve(c.Context(), opts, fopts, cli, f.Prompter())
		},
	}
	cmd.Flags().BoolVar(&opts.Reject, "reject", false, "Reject the pending tool call instead of approving")
	cmd.Flags().StringVar(&opts.Reason, "reason", "", "Reason recorded with the decision")
	cmd.Flags().StringVar(&opts.ModifiedArgs, "modified-args", "", "Replace tool call arguments on approve (JSON object; conflicts with --reject)")
	cmdutil.AddFormatFlag(cmd, resolveFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "session.tool_approval.resolve")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "approve or reject a pending tool call from an agent run; then resume with session continue-stream",
		RequiredFlags: []string{"<pending-id> (positional)"},
		Examples: []string{
			"weknora session tool-approval resolve pend_abc -y",
			"weknora session tool-approval resolve pend_abc --reject -y",
		},
		Output: "envelope.data is {pending_id, decision (approve|reject), resolved:true}",
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"Approving executes the blocked tool call with the user's authority — show the user what tool/args are pending before resolving.",
		},
	})
	return cmd
}

func decisionOf(reject bool) string {
	if reject {
		return decisionReject
	}
	return decisionApprove
}

// decisionPastTense returns the past tense of the decision verb for human output.
func decisionPastTense(decision string) string {
	switch decision {
	case decisionReject:
		return "Rejected"
	default: // decisionApprove
		return "Approved"
	}
}

// retryArgOf returns the extra --reject flag element to include in the retry
// argv when the decision is reject; nil otherwise.
func retryArgOf(reject bool) []string {
	if reject {
		return []string{"--reject"}
	}
	return nil
}

// validateModifiedArgs checks the --reject/--modified-args invariants and
// returns the validated raw JSON (nil when --modified-args is unset). Shared
// by the RunE pre-dry-run gate and runResolve so both paths reject the same
// inputs identically.
func validateModifiedArgs(opts *ResolveOptions) (json.RawMessage, error) {
	if opts.Reject && opts.ModifiedArgs != "" {
		return nil, &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--modified-args conflicts with --reject (rejected calls never execute)",
		}
	}
	if opts.ModifiedArgs == "" {
		return nil, nil
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(opts.ModifiedArgs), &probe); err != nil || probe == nil {
		return nil, &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--modified-args must be a non-null JSON object",
		}
	}
	if len(probe) == 0 {
		return nil, &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--modified-args must not be an empty object (it would wipe the original tool arguments); omit the flag to keep them",
		}
	}
	return json.RawMessage(opts.ModifiedArgs), nil
}

func runResolve(ctx context.Context, opts *ResolveOptions, fopts *cmdutil.FormatOptions, svc ResolveService, p prompt.Prompter) error {
	modified, err := validateModifiedArgs(opts)
	if err != nil {
		return err
	}
	decision := decisionOf(opts.Reject)
	retryCmd := append([]string{"weknora", "session", "tool-approval", "resolve", opts.PendingID}, retryArgOf(opts.Reject)...)
	retryCmd = append(retryCmd, "-y")
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), decision, "tool call", opts.PendingID, "session.tool_approval.resolve", retryCmd); err != nil {
		return err
	}
	if err := svc.ResolveToolApproval(ctx, opts.PendingID, &sdk.ResolveToolApprovalRequest{
		Decision:     decision,
		Reason:       opts.Reason,
		ModifiedArgs: modified,
	}); err != nil {
		return cmdutil.WrapHTTP(err, "resolve tool approval %s", opts.PendingID)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, resolveResult{PendingID: opts.PendingID, Decision: decision, Resolved: true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ %s tool call %s\n", decisionPastTense(decision), opts.PendingID)
	return nil
}
