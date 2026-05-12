package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

type ViewOptions struct {
	JSONOut bool
}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
}

// NewCmdView builds `weknora kb view <id>`.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	opts := &ViewOptions{}
	cmd := &cobra.Command{
		Use:   "view <id>",
		Short: "Show a knowledge base by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runView(c.Context(), opts, cli, args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Returns details of one knowledge base by ID (config + counts).")
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, svc ViewService, id string) error {
	kb, err := svc.GetKnowledgeBase(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get knowledge base %q", id)
	}
	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(kb, nil))
	}
	// Human: KEY: VALUE
	w := iostreams.IO.Out
	fmt.Fprintf(w, "ID:        %s\n", kb.ID)
	fmt.Fprintf(w, "NAME:      %s\n", kb.Name)
	if kb.Description != "" {
		fmt.Fprintf(w, "DESC:      %s\n", kb.Description)
	}
	fmt.Fprintf(w, "DOCS:      %s\n", text.Pluralize(int(kb.KnowledgeCount), "doc"))
	fmt.Fprintf(w, "CHUNKS:    %s\n", text.Pluralize(int(kb.ChunkCount), "chunk"))
	if kb.EmbeddingModelID != "" {
		fmt.Fprintf(w, "EMBEDDING: %s\n", kb.EmbeddingModelID)
	}
	if !kb.UpdatedAt.IsZero() {
		// Detail page favors absolute time; FuzzyAgo is reserved for list views.
		fmt.Fprintf(w, "UPDATED:   %s\n", kb.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}
