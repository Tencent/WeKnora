package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbCreateFields enumerates the fields surfaced for `--format json` discovery
// on `kb create`. The result is the full KnowledgeBase struct; these mirror
// its top-level json tags. Nested config objects are intentionally omitted —
// users wanting them can drop the projection or use --jq.
var kbCreateFields = []string{
	"id", "name", "type", "description",
	"is_temporary", "is_pinned",
	"embedding_model_id", "summary_model_id",
	"knowledge_count", "chunk_count",
	"is_processing", "processing_count",
	"created_at", "updated_at",
}

type CreateOptions struct {
	Name            string
	Description     string
	EmbeddingModel  string
	StorageProvider string
	DryRun          bool
}

// storageProviderValues mirrors the server enum in
// internal/types/knowledgebase.go:StorageProviderConfig.Provider.
var storageProviderValues = []string{"local", "minio", "cos", "tos", "s3", "oss", "ks3", "obs"}

// CreateService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it via duck typing.
type CreateService interface {
	CreateKnowledgeBase(ctx context.Context, kb *sdk.KnowledgeBase) (*sdk.KnowledgeBase, error)
}

// NewCmdCreate builds `weknora kb create <name>`. Positional <name> only,
// consistent with `agent create <name>`.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	opts := &CreateOptions{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new knowledge base",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Name = args[0]
			// Validate --storage-provider enum before the dry-run gate so
			// --dry-run rejects identically to the live path. ValidateEnum
			// returns input.invalid_argument (exit 5) and normalizes to the
			// canonical lowercase form — consistent with every other enum flag
			// (model --type, agent --agent-mode, message search --mode).
			// runCreate re-validates for direct-call callers.
			canonSP, err := cmdutil.ValidateEnum("storage-provider", opts.StorageProvider, storageProviderValues)
			if err != nil {
				return err
			}
			opts.StorageProvider = canonSP
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "kb.create",
				Args: map[string]any{
					"name":        opts.Name,
					"description": opts.Description,
				},
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			// --embedding-model accepts a model id or name (a UUID passes
			// through; a name resolves among Embedding models). Configuring a
			// KB's models fully is `weknora kb init`; this just pre-sets the
			// embedding model at creation.
			if opts.EmbeddingModel, err = cmdutil.ResolveModelRef(c.Context(), cli, opts.EmbeddingModel, "Embedding"); err != nil {
				return err
			}
			return runCreate(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.Description, "description", "", "Knowledge base description (optional)")
	cmd.Flags().StringVar(&opts.EmbeddingModel, "embedding-model", "", "Embedding model id or name (optional; configure models fully with `weknora kb init`)")
	cmd.Flags().StringVar(&opts.StorageProvider, "storage-provider", "",
		"Storage provider for documents in this KB: "+strings.Join(storageProviderValues, " | ")+" (optional; server default when unset)")
	cmdutil.AddFormatFlag(cmd, kbCreateFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Create a new knowledge base with the given name. Emits the created KB object with its id.",
		RequiredFlags: []string{"<name> (positional)"},
		Examples: []string{
			`weknora kb create "Eng Docs"`,
			`weknora kb create "Eng Docs" --description "engineering knowledge base"`,
			`weknora kb create "Eng Docs" --jq .data.id   # capture id to chain into doc upload --kb`,
		},
		Output: "envelope.data is the created KnowledgeBase object with id, name, type, embedding_model_id",
	})
	return cmd
}

func runCreate(ctx context.Context, opts *CreateOptions, fopts *cmdutil.FormatOptions, svc CreateService) error {
	// Trim defensively in case a caller invokes runCreate directly with
	// whitespace; the cobra layer enforces a non-empty positional from the CLI.
	if strings.TrimSpace(opts.Name) == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "knowledge base name is required")
	}

	req := &sdk.KnowledgeBase{
		Name:        opts.Name,
		Description: opts.Description,
	}
	if opts.EmbeddingModel != "" {
		req.EmbeddingModelID = opts.EmbeddingModel
	}
	if opts.StorageProvider != "" {
		canonSP, err := cmdutil.ValidateEnum("storage-provider", opts.StorageProvider, storageProviderValues)
		if err != nil {
			return err
		}
		req.StorageProviderConfig = &sdk.StorageProviderConfig{Provider: canonSP}
	}

	created, err := svc.CreateKnowledgeBase(ctx, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "create knowledge base")
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, created, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Created knowledge base %q (id: %s)\n", created.Name, created.ID)
	return nil
}
