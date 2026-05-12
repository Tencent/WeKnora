package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type EditOptions struct {
	// Name/Description are *string so we can distinguish "unset" from "set to
	// empty". An unset field is omitted from the SDK request — only fields the
	// user passed are sent. Server PUT semantics are "replace everything in the
	// request"; if we always sent both, an `--name` invocation would silently
	// clear the description.
	Name        *string
	Description *string
	JSONOut     bool
	DryRun      bool
}

type EditService interface {
	UpdateKnowledgeBase(ctx context.Context, id string, req *sdk.UpdateKnowledgeBaseRequest) (*sdk.KnowledgeBase, error)
}

// NewCmdEdit builds `weknora kb edit <id>`. At least one of --name /
// --description must be provided.
func NewCmdEdit(f *cmdutil.Factory) *cobra.Command {
	opts := &EditOptions{}
	var name, desc string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a knowledge base's name or description",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if c.Flag("name").Changed {
				opts.Name = &name
			}
			if c.Flag("description").Changed {
				opts.Description = &desc
			}
			opts.DryRun = cmdutil.IsDryRun(c)
			if opts.DryRun {
				return runEdit(c.Context(), opts, nil, args[0])
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runEdit(c.Context(), opts, cli, args[0])
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name (omit to leave unchanged)")
	cmd.Flags().StringVar(&desc, "description", "", "New description (omit to leave unchanged)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Edits a knowledge base. At least one of --name/--description is required. Fields not passed are preserved server-side. Returns the updated KnowledgeBase.")
	return cmd
}

func runEdit(ctx context.Context, opts *EditOptions, svc EditService, id string) error {
	if opts.Name == nil && opts.Description == nil {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputMissingFlag,
			Message: "kb edit requires at least one of --name or --description",
			Hint:    "pass --name <name> and/or --description <desc>",
		}
	}

	req := &sdk.UpdateKnowledgeBaseRequest{}
	if opts.Name != nil {
		req.Name = *opts.Name
	}
	if opts.Description != nil {
		req.Description = *opts.Description
	}

	risk := &format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("edit knowledge base %s", id)}
	if opts.DryRun {
		return cmdutil.EmitDryRun(opts.JSONOut, req, &format.Meta{KBID: id}, risk)
	}

	updated, err := svc.UpdateKnowledgeBase(ctx, id, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "edit knowledge base %s", id)
	}
	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.SuccessWithRisk(updated, &format.Meta{KBID: id}, risk))
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Updated knowledge base %s\n", id)
	return nil
}
