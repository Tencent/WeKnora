package doc

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
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
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, jopts *cmdutil.JSONOptions, svc ViewService, id string) error {
	doc, err := svc.GetKnowledge(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get document %q", id)
	}
	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, doc)
	}
	w := iostreams.IO.Out
	fmt.Fprintf(w, "ID:        %s\n", doc.ID)
	fmt.Fprintf(w, "NAME:      %s\n", text.KnowledgeDisplayName(doc.FileName, doc.Title, doc.ID))
	// Title is rendered as a separate line only when it adds info over
	// NAME (i.e. FileName is set AND differs from Title). When FileName
	// is empty, KnowledgeDisplayName already used Title for NAME so a
	// duplicate TITLE line would be noise.
	if doc.Title != "" && doc.FileName != "" && doc.Title != doc.FileName {
		fmt.Fprintf(w, "TITLE:     %s\n", doc.Title)
	}
	if doc.KnowledgeBaseID != "" {
		fmt.Fprintf(w, "KB:        %s\n", doc.KnowledgeBaseID)
	}
	if doc.TagID != "" {
		fmt.Fprintf(w, "TAG:       %s\n", doc.TagID)
	}
	if doc.Description != "" {
		fmt.Fprintf(w, "DESC:      %s\n", doc.Description)
	}
	if doc.FileType != "" {
		fmt.Fprintf(w, "TYPE:      %s\n", doc.FileType)
	}
	if doc.Source != "" {
		fmt.Fprintf(w, "SOURCE:    %s\n", doc.Source)
	}
	if doc.Channel != "" {
		fmt.Fprintf(w, "CHANNEL:   %s\n", doc.Channel)
	}
	if doc.FileSize > 0 {
		fmt.Fprintf(w, "SIZE:      %s\n", formatSize(doc.FileSize))
	}
	if doc.StorageSize > 0 {
		fmt.Fprintf(w, "STORAGE:   %s\n", formatSize(doc.StorageSize))
	}
	if doc.ParseStatus != "" {
		fmt.Fprintf(w, "STATUS:    %s\n", doc.ParseStatus)
	}
	if doc.SummaryStatus != "" {
		fmt.Fprintf(w, "SUMMARY:   %s\n", doc.SummaryStatus)
	}
	if doc.EnableStatus != "" {
		fmt.Fprintf(w, "ENABLED:   %s\n", doc.EnableStatus)
	}
	if doc.EmbeddingModelID != "" {
		fmt.Fprintf(w, "EMBEDDING: %s\n", doc.EmbeddingModelID)
	}
	if doc.FileHash != "" {
		// Git-SHA-style 12-char prefix is enough for de-duplication
		// while keeping the line short.
		h := doc.FileHash
		if len(h) > 12 {
			h = h[:12]
		}
		fmt.Fprintf(w, "HASH:      %s\n", h)
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
