package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbCreateFields enumerates the fields surfaced for `--json` discovery on
// `kb create`. The result is the full KnowledgeBase struct; these mirror its
// top-level json tags. Nested config objects are intentionally omitted —
// users wanting them can drop --json (no filter) or use --jq.
var kbCreateFields = []string{
	"id", "name", "type", "description",
	"is_temporary", "is_pinned",
	"embedding_model_id", "summary_model_id",
	"knowledge_count", "chunk_count",
	"is_processing", "processing_count",
	"created_at", "updated_at",
}

type CreateOptions struct {
	Name           string
	Description    string
	EmbeddingModel string
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
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runCreate(c.Context(), opts, jopts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Knowledge base name (required)")
	cmd.Flags().StringVar(&opts.Description, "description", "", "Knowledge base description (optional)")
	cmd.Flags().StringVar(&opts.EmbeddingModel, "embedding-model", "", "Embedding model ID (optional; server picks default when unset)")
	cmdutil.AddJSONFlags(cmd, kbCreateFields)
	_ = cmd.MarkFlagRequired("name")
	aiclient.SetAgentHelp(cmd, "Creates a knowledge base under the active context. --name is required; --description and --embedding-model are optional. Returns the full KnowledgeBase object including the new id.")
	return cmd
}

func runCreate(ctx context.Context, opts *CreateOptions, jopts *cmdutil.JSONOptions, svc CreateService) error {
	// Trim defensively in case a caller invokes runCreate directly with
	// whitespace; the cobra layer marks --name required so the empty-string
	// case is unreachable from the CLI.
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

	created, err := svc.CreateKnowledgeBase(ctx, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "create knowledge base")
	}

	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, created)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Created knowledge base %q (id: %s)\n", created.Name, created.ID)
	return nil
}
