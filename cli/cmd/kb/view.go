package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbViewFields enumerates the fields surfaced for `--format json` discovery on
// `kb view`. Lists the KnowledgeBase top-level json tags; nested config
// structs are omitted (use --jq for those).
var kbViewFields = []string{
	"id", "name", "type", "description",
	"is_temporary", "is_pinned",
	"embedding_model_id", "summary_model_id",
	"knowledge_count", "chunk_count",
	"is_processing", "processing_count",
	"created_at", "updated_at",
}

type ViewOptions struct{}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
}

// NewCmdView builds `weknora kb view <id>`.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	opts := &ViewOptions{}
	cmd := &cobra.Command{
		Use:   "view <kb-id>",
		Short: "Show a knowledge base by ID",
		Long:  `Fetch a knowledge base's full configuration: chunking settings, embedding / summary model IDs, knowledge_count, chunk_count.`,
		Args:  cobra.ExactArgs(1),
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
			return runView(c.Context(), opts, fopts, cli, args[0])
		},
	}
	cmdutil.AddFormatFlag(cmd, kbViewFields...)
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, fopts *cmdutil.FormatOptions, svc ViewService, id string) error {
	kb, err := svc.GetKnowledgeBase(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get knowledge base %q", id)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, kb, nil)
	}
	// Text mode: KEY: VALUE. Nested config structs (chunking_config,
	// vlm_config, etc.) are intentionally omitted from the text render —
	// those are for `--format json | jq '.chunking_config'` workflows.
	w := iostreams.IO.Out
	fmt.Fprintf(w, "ID:        %s\n", kb.ID)
	fmt.Fprintf(w, "NAME:      %s\n", kb.Name)
	if kb.Type != "" {
		fmt.Fprintf(w, "TYPE:      %s\n", kb.Type)
	}
	if kb.Description != "" {
		fmt.Fprintf(w, "DESC:      %s\n", kb.Description)
	}
	if kb.IsPinned {
		fmt.Fprintf(w, "PINNED:    yes\n")
	}
	if kb.IsTemporary {
		fmt.Fprintf(w, "TEMPORARY: yes\n")
	}
	fmt.Fprintf(w, "DOCS:      %s\n", text.Pluralize(int(kb.KnowledgeCount), "doc"))
	fmt.Fprintf(w, "CHUNKS:    %s\n", text.Pluralize(int(kb.ChunkCount), "chunk"))
	if kb.IsProcessing {
		fmt.Fprintf(w, "PROCESSING: %s\n", text.Pluralize(int(kb.ProcessingCount), "doc"))
	}
	if kb.EmbeddingModelID != "" {
		fmt.Fprintf(w, "EMBEDDING: %s\n", kb.EmbeddingModelID)
	}
	if kb.SummaryModelID != "" {
		fmt.Fprintf(w, "SUMMARY MODEL: %s\n", kb.SummaryModelID)
	}
	if !kb.CreatedAt.IsZero() {
		fmt.Fprintf(w, "CREATED:   %s\n", kb.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	if !kb.UpdatedAt.IsZero() {
		// Detail page favors absolute time; FuzzyAgo is reserved for list views.
		fmt.Fprintf(w, "UPDATED:   %s\n", kb.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}
