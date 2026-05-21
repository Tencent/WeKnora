// Package doc — fetch.go implements `weknora doc fetch <url>`.
package doc

import (
	"cmp"
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// docFetchFields enumerates the fields surfaced for `--format json` discovery
// on `doc fetch`.
var docFetchFields = []string{
	"id", "knowledge_base_id", "tag_id", "type", "title", "description",
	"source", "channel", "parse_status", "summary_status", "enable_status",
	"embedding_model_id", "file_name", "file_type", "file_size", "file_hash",
	"file_path", "storage_size",
	"created_at", "updated_at", "processed_at", "error_message",
}

// FetchOptions holds CLI flag values for `doc fetch`.
type FetchOptions struct {
	URL              string
	Name             string // --name: FileName hint for file-vs-crawl mode detection
	Title            string // --title: display title for the new entry
	FileType         string // --file-type: extension hint for extension-less URLs
	TagID            string // --tag-id: associate the new entry with a tag
	Channel          string // --channel: ingestion-channel tag (default "api")
	EnableMultimodel *bool  // tri-state: nil = server default
}

// FetchService is the narrow SDK surface for `doc fetch`.
// *sdk.Client satisfies it.
type FetchService interface {
	CreateKnowledgeFromURL(
		ctx context.Context,
		kbID string,
		req sdk.CreateKnowledgeFromURLRequest,
	) (*sdk.Knowledge, error)
}

// NewCmdFetch builds `weknora doc fetch <url>`.
func NewCmdFetch(f *cmdutil.Factory) *cobra.Command {
	opts := &FetchOptions{}
	cmd := &cobra.Command{
		Use:   "fetch <url>",
		Short: "Fetch a remote document into a knowledge base",
		Long: `Server fetches the document at the given URL and ingests it into the resolved
knowledge base. KB resolution follows the standard 4-level chain:
--kb flag > WEKNORA_KB_ID env > .weknora/project.yaml > error.

When the URL has a known file extension (.pdf, .docx, .md, .txt) the server
automatically switches from web-page-crawl mode to file-download mode. Pass
--file-type or --name with a recognisable extension to force file-download mode
for extension-less URLs.

Server-side ingestion knobs:

  --name <name>            Override the recorded file name; also used as the
                           file-type hint when the extension is recognisable.
  --title <title>          Set the display title stored with the entry.
  --file-type <ext>        Explicit file-type hint (e.g. "pdf") for URLs
                           without an extension.
  --tag-id <id>            Associate the new entry with a tag.
  --enable-multimodel      Toggle multimodal extraction (image-in-PDF → text).
                           Unset ⇒ server default; pass true or false to override.
  --channel <name>         Override the ingestion-channel tag (default "api").`,
		Example: `  weknora doc fetch https://example.com/whitepaper.pdf
  weknora doc fetch https://example.com/no-ext --file-type pdf --title "Whitepaper"
  weknora doc fetch https://example.com/article.html --name "Q3 Article" --tag-id tag_abc
  weknora doc fetch https://example.com/report.pdf --kb my-kb --enable-multimodel`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.URL = args[0]
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Tri-state --enable-multimodel: nil = unset (server default).
			if c.Flags().Changed("enable-multimodel") {
				raw, _ := c.Flags().GetString("enable-multimodel")
				v, perr := parseTriBool(raw)
				if perr != nil {
					return perr
				}
				opts.EnableMultimodel = &v
			}
			if err := cmdutil.ValidateHTTPURL("<url>", opts.URL); err != nil {
				return err
			}
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runFetch(c.Context(), opts, fopts, cli, kbID)
		},
	}
	cmdutil.AddKBFlag(cmd)
	cmd.Flags().StringVar(&opts.Name, "name", "", "File name hint (also used as file-type hint when extension is recognisable)")
	cmd.Flags().StringVar(&opts.Title, "title", "", "Display title for the new entry")
	cmd.Flags().StringVar(&opts.FileType, "file-type", "", "File-type hint such as \"pdf\" when the URL has no extension")
	cmd.Flags().StringVar(&opts.TagID, "tag-id", "", "Tag id to associate with the new entry")
	cmd.Flags().StringVar(&opts.Channel, "channel", "", "Ingestion-channel tag recorded server-side (default \"api\")")
	cmd.Flags().String("enable-multimodel", "", "Toggle multimodal extraction (true|false); unset ⇒ server default")
	cmd.Flags().Lookup("enable-multimodel").NoOptDefVal = "true"
	cmdutil.AddFormatFlag(cmd, docFetchFields...)
	return cmd
}

// runFetch ingests a remote URL via SDK CreateKnowledgeFromURL.
func runFetch(ctx context.Context, opts *FetchOptions, fopts *cmdutil.FormatOptions, svc FetchService, kbID string) error {
	req := sdk.CreateKnowledgeFromURLRequest{
		URL:              opts.URL,
		FileName:         opts.Name,
		FileType:         opts.FileType,
		EnableMultimodel: opts.EnableMultimodel,
		Title:            opts.Title,
		TagID:            opts.TagID,
		Channel:          cmp.Or(opts.Channel, uploadChannel),
	}
	k, err := svc.CreateKnowledgeFromURL(ctx, kbID, req)
	if err != nil {
		if errors.Is(err, sdk.ErrDuplicateURL) {
			return cmdutil.Wrapf(cmdutil.CodeResourceAlreadyExists, err,
				"URL already ingested into this knowledge base")
		}
		return cmdutil.WrapHTTP(err, "fetch document from %s", opts.URL)
	}
	return renderUploadSuccess(k, fopts, "Ingested", opts.Name, opts.URL)
}
