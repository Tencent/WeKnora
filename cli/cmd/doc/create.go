// Package doc — create.go implements `weknora doc create --text "..."`.
// Allows creating a knowledge entry directly from inline text content without
// uploading a file or fetching a remote URL.
package doc

import (
	"cmp"
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// docCreateFields enumerates the fields surfaced for `--format json` discovery
// on `doc create`.
var docCreateFields = []string{
	"id", "knowledge_base_id", "tag_id", "type", "title", "description",
	"source", "channel", "parse_status", "summary_status", "enable_status",
	"created_at", "updated_at", "error_message",
}

// CreateOptions holds CLI flag values for `doc create`.
type CreateOptions struct {
	Text    string // --text (required): document text content (Markdown)
	Name    string // --name: document title
	TagID   string // --tag-id: associate with a tag
	Channel string // --channel: ingestion-channel tag (default "api")
}

// CreateService is the narrow SDK surface for `doc create`.
// *sdk.Client satisfies it.
type CreateService interface {
	CreateManualKnowledge(
		ctx context.Context,
		kbID string,
		req *sdk.CreateManualKnowledgeRequest,
	) (*sdk.Knowledge, error)
}

// NewCmdCreate builds `weknora doc create --text "..."`.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	opts := &CreateOptions{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a knowledge entry from inline text content",
		Long: `Create a new knowledge entry by passing Markdown content directly via --text.
Useful for short snippets, agent-generated content, or structured notes that
don't require a file upload or remote URL. KB resolution follows the standard
4-level chain: --kb flag > WEKNORA_KB_ID env > .weknora/project.yaml > error.

  --text <content>    Document text in Markdown format (required).
  --name <title>      Display title stored with the entry.
  --tag-id <id>       Associate the new entry with a tag.
  --channel <name>    Override the ingestion-channel tag (default "api").`,
		Example: `  weknora doc create --text "# Meeting Notes\n\nAction items: ..."
  weknora doc create --text "$(cat notes.md)" --name "Sprint Notes" --kb my-kb
  weknora doc create --text "API usage guide" --name "API Guide" --tag-id tag_abc`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runCreate(c.Context(), opts, fopts, cli, kbID)
		},
	}
	cmdutil.AddKBFlag(cmd)
	cmd.Flags().StringVar(&opts.Text, "text", "", "Document text content in Markdown format (required)")
	cmd.Flags().StringVar(&opts.Name, "name", "", "Document title")
	cmd.Flags().StringVar(&opts.TagID, "tag-id", "", "Tag id to associate with the new entry")
	cmd.Flags().StringVar(&opts.Channel, "channel", "", "Ingestion-channel tag recorded server-side (default \"api\")")
	_ = cmd.MarkFlagRequired("text")
	cmdutil.AddFormatFlag(cmd, docCreateFields...)
	return cmd
}

// runCreate creates a manual knowledge entry via SDK CreateManualKnowledge.
func runCreate(ctx context.Context, opts *CreateOptions, fopts *cmdutil.FormatOptions, svc CreateService, kbID string) error {
	// Guard against empty text: cobra's MarkFlagRequired enforces this for
	// normal CLI invocations; this check protects tests that call runCreate
	// directly and any future callers that bypass cobra.
	if opts.Text == "" {
		return cmdutil.NewFlagError(fmt.Errorf("--text is required"))
	}
	req := &sdk.CreateManualKnowledgeRequest{
		Title:   opts.Name,
		Content: opts.Text,
		TagID:   opts.TagID,
		Channel: cmp.Or(opts.Channel, uploadChannel),
	}
	k, err := svc.CreateManualKnowledge(ctx, kbID, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "create document")
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, k, nil)
	}
	displayed := opts.Name
	if displayed == "" {
		displayed = k.Title
	}
	if displayed == "" {
		displayed = k.ID
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Created %q (id: %s)\n", displayed, k.ID)
	return nil
}
