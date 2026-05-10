package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// CreateOptions captures `weknora kb create` flags.
type CreateOptions struct {
	Name           string
	Description    string
	EmbeddingModel string
	JSONOut        bool
	DryRun         bool
}

// CreateService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it via duck typing (ADR-4).
type CreateService interface {
	CreateKnowledgeBase(ctx context.Context, kb *sdk.KnowledgeBase) (*sdk.KnowledgeBase, error)
}

// NewCmdCreate builds `weknora kb create`.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	opts := &CreateOptions{}
	cmd := &cobra.Command{
		Use:   "create --name <name>",
		Short: "Create a new knowledge base",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			opts.DryRun = cmdutil.IsDryRun(c)
			if opts.DryRun {
				return runCreate(c.Context(), opts, nil) // service unused on dry-run
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runCreate(c.Context(), opts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Knowledge base name (required)")
	cmd.Flags().StringVar(&opts.Description, "description", "", "Knowledge base description (optional)")
	cmd.Flags().StringVar(&opts.EmbeddingModel, "embedding-model", "", "Embedding model ID (optional; server picks default when unset)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Creates a knowledge base under the active context. --name is required; --description and --embedding-model are optional. Returns data: full KnowledgeBase object including the new id.")
	return cmd
}

func runCreate(ctx context.Context, opts *CreateOptions, svc CreateService) error {
	// Validate locally before any HTTP — keeps `input.invalid_argument`
	// distinct from a server-side 400.
	if strings.TrimSpace(opts.Name) == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--name is required")
	}

	req := &sdk.KnowledgeBase{
		Name:        opts.Name,
		Description: opts.Description,
	}
	if opts.EmbeddingModel != "" {
		req.EmbeddingModelID = opts.EmbeddingModel
	}

	if opts.DryRun {
		return cmdutil.EmitDryRun(opts.JSONOut, req, nil,
			&format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("create knowledge base %q", opts.Name)})
	}

	created, err := svc.CreateKnowledgeBase(ctx, req)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "create knowledge base")
	}

	if opts.JSONOut {
		risk := &format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("created knowledge base %s", created.ID)}
		return format.WriteEnvelope(iostreams.IO.Out, format.SuccessWithRisk(created, &format.Meta{KBID: created.ID}, risk))
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Created knowledge base %q (id: %s)\n", created.Name, created.ID)
	return nil
}
