package modelcmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// DeleteService is the narrow SDK surface this command depends on.
type DeleteService interface {
	DeleteModel(ctx context.Context, id string) error
}

type deleteOptions struct {
	Yes    bool
	DryRun bool
}

// NewCmdDelete builds `weknora model delete <model-id>`.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &deleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <model-id>",
		Short: "Delete a model",
		Long: `Delete a model by id. High-risk write: without -y/--yes in a non-TTY / JSON
context it exits 10 (input.confirmation_required) without deleting. A KB or
agent still referencing the model will fail until repointed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			id := args[0]
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "model.delete",
				Args:   map[string]any{"model": id},
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(),
				"delete", "model", id, "model.delete", []string{"weknora", "model", "delete", id, "-y"}); err != nil {
				return err
			}
			return runDelete(c.Context(), fopts, cli, id)
		},
	}
	cmdutil.AddFormatFlag(cmd, "id", "deleted")
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "model.delete")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "delete a model by id",
		RequiredFlags: []string{"<model-id> (positional)"},
		Examples:      []string{"weknora model delete model_abc -y"},
		Output:        "envelope.data is {id, deleted:true}",
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"A knowledge base or agent still referencing this model will fail until repointed.",
		},
	})
	return cmd
}

func runDelete(ctx context.Context, fopts *cmdutil.FormatOptions, svc DeleteService, id string) error {
	if err := svc.DeleteModel(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete model %q", id)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, map[string]any{"id": id, "deleted": true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted model %s\n", id)
	return nil
}

// compile-time check: the production SDK client implements DeleteService.
var _ DeleteService = (*sdk.Client)(nil)
