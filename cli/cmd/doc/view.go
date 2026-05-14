package doc

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// docViewFields enumerates the fields surfaced for `--json` discovery on
// `doc view`. Lists the Knowledge struct top-level json tags.
var docViewFields = []string{
	"id", "knowledge_base_id", "tag_id", "type", "title", "description",
	"source", "channel", "parse_status", "summary_status", "enable_status",
	"embedding_model_id", "file_name", "file_type", "file_size", "file_hash",
	"file_path", "storage_size",
	"created_at", "updated_at", "processed_at", "error_message",
}

type ViewOptions struct{}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetKnowledge(ctx context.Context, id string) (*sdk.Knowledge, error)
}

// NewCmdView builds `weknora doc view <id>`.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	opts := &ViewOptions{}
	cmd := &cobra.Command{
		Use:   "view <id>",
		Short: "Show a document by ID",
		Example: `  weknora doc view doc_abc
  weknora doc view doc_abc --json`,
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
			return runView(c.Context(), opts, jopts, cli, args[0])
		},
	}
	cmdutil.AddJSONFlags(cmd, docViewFields)
	aiclient.SetAgentHelp(cmd, "Returns the full Knowledge object for one document: id, kb_id, file metadata, parse_status, processed_at, error_message. Use this instead of paging through `doc list` when you have the doc id.")
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, jopts *cmdutil.JSONOptions, svc ViewService, id string) error {
	doc, err := svc.GetKnowledge(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get document %q", id)
	}
	if jopts.Enabled() {
		return format.WriteJSONFiltered(iostreams.IO.Out, doc, jopts.Fields, jopts.JQ)
	}
	w := iostreams.IO.Out
	fmt.Fprintf(w, "ID:        %s\n", doc.ID)
	fmt.Fprintf(w, "NAME:      %s\n", text.KnowledgeDisplayName(doc.FileName, doc.Title, doc.ID))
	if doc.KnowledgeBaseID != "" {
		fmt.Fprintf(w, "KB:        %s\n", doc.KnowledgeBaseID)
	}
	if doc.FileType != "" {
		fmt.Fprintf(w, "TYPE:      %s\n", doc.FileType)
	}
	if doc.FileSize > 0 {
		fmt.Fprintf(w, "SIZE:      %s\n", formatSize(doc.FileSize))
	}
	if doc.ParseStatus != "" {
		fmt.Fprintf(w, "STATUS:    %s\n", doc.ParseStatus)
	}
	if doc.EmbeddingModelID != "" {
		fmt.Fprintf(w, "EMBEDDING: %s\n", doc.EmbeddingModelID)
	}
	if !doc.CreatedAt.IsZero() {
		fmt.Fprintf(w, "CREATED:   %s\n", doc.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	if !doc.UpdatedAt.IsZero() {
		fmt.Fprintf(w, "UPDATED:   %s\n", doc.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	if doc.ProcessedAt != nil && !doc.ProcessedAt.IsZero() {
		fmt.Fprintf(w, "PROCESSED: %s\n", doc.ProcessedAt.Format("2006-01-02 15:04:05"))
	}
	if doc.ErrorMessage != "" {
		fmt.Fprintf(w, "ERROR:     %s\n", doc.ErrorMessage)
	}
	return nil
}
