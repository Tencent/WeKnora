package doc

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// docUpdateFields enumerates the fields surfaced for `--format json` discovery
// on `doc update`. The result is the updated Knowledge object.
var docUpdateFields = []string{"id", "title", "description", "file_name", "parse_status"}

// UpdateOptions captures `doc update` flag state. Title/Description are *string
// so an unset flag is distinguishable from "set to empty" — only fields the
// user passed are changed; the rest are preserved via fetch-then-update.
type UpdateOptions struct {
	Title       *string
	Description *string
	Yes         bool // sourced from global -y/--yes persistent flag
	DryRun      bool
}

// UpdateService is the narrow SDK surface this command depends on. The server's
// UpdateKnowledge endpoint persists only title and description (other fields on
// the PUT body are ignored), so this command edits just those two; GetKnowledge
// supplies the fetch half of the fetch-then-update flow.
type UpdateService interface {
	GetKnowledge(ctx context.Context, id string) (*sdk.Knowledge, error)
	UpdateKnowledge(ctx context.Context, k *sdk.Knowledge) error
}

// NewCmdUpdate builds `weknora doc update <doc-id>`. At least one of
// --title / --description must be provided.
func NewCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	opts := &UpdateOptions{}
	var title, desc string
	cmd := &cobra.Command{
		Use:   "update <doc-id>",
		Short: "Update a document's title or description",
		Long: `Update a document's title and/or description. At least one of --title /
--description must be supplied; the document's content, file, and parse state are
untouched (only the title and description are editable — re-upload to change the
content).

AI agents: this is a high-risk write. Without -y/--yes the CLI exits 10
with input.confirmation_required. Never auto-pass -y; surface the prompt
to the user first.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			if c.Flag("title").Changed {
				opts.Title = &title
			}
			if c.Flag("description").Changed {
				opts.Description = &desc
			}
			id := args[0]
			// Validate "at least one mutation flag" before the dry-run gate so
			// --dry-run rejects identically to the live path.
			if opts.Title == nil && opts.Description == nil {
				return errNoUpdateFlag()
			}
			planArgs := map[string]any{"doc": id}
			if opts.Title != nil {
				planArgs["title"] = *opts.Title
			}
			if opts.Description != nil {
				planArgs["description"] = *opts.Description
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "doc.update",
				Args:   planArgs,
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			// Build a retry command from the flags the user actually passed so
			// agents can re-invoke with -y after explicit human approval.
			retryCmd := cmdutil.BuildRetryArgv(c, []string{"weknora", "doc", "update", id}, "title", "description", "format")
			if err := cmdutil.ConfirmWrite(f.Prompter(), opts.Yes, fopts.WantsJSON(), "update", "document", id, "doc.update", retryCmd); err != nil {
				return err
			}
			return runUpdate(c.Context(), opts, fopts, cli, id)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New title (omit to leave unchanged)")
	cmd.Flags().StringVar(&desc, "description", "", "New description (omit to leave unchanged)")
	cmdutil.AddFormatFlag(cmd, docUpdateFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetWriteRisk(cmd, "doc.update")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "update a document's title or description (content is not editable — re-upload for that)",
		RequiredFlags: []string{"<doc-id> (positional)", "--title or --description (at least one)"},
		Examples: []string{
			`weknora doc update doc_abc --title "Q3 Runbook" -y`,
			`weknora doc update doc_abc --description "archived" --format json -y`,
		},
		Output: "envelope.data is the Knowledge object with the new title/description",
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
		},
	})
	return cmd
}

func errNoUpdateFlag() error {
	return &cmdutil.Error{
		Code:    cmdutil.CodeInputMissingFlag,
		Message: "doc update requires at least one of --title or --description",
		Hint:    "pass --title <title> and/or --description <desc>",
	}
}

func runUpdate(ctx context.Context, opts *UpdateOptions, fopts *cmdutil.FormatOptions, svc UpdateService, id string) error {
	if opts.Title == nil && opts.Description == nil {
		return errNoUpdateFlag()
	}
	// Fetch-then-update: load the current record, overlay only the fields the
	// user set, and PUT the whole object. TOCTOU note: a concurrent writer could
	// change the record between the Get and Put (same window as kb update).
	current, err := svc.GetKnowledge(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch document %s", id)
	}
	if opts.Title != nil {
		current.Title = *opts.Title
	}
	if opts.Description != nil {
		current.Description = *opts.Description
	}
	if err := svc.UpdateKnowledge(ctx, current); err != nil {
		return cmdutil.WrapHTTP(err, "update document %s", id)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, current, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Updated document %s\n", id)
	return nil
}
