package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbEditFields enumerates the fields surfaced for `--json` discovery on
// `kb edit`. The result is the updated KnowledgeBase; mirrors the kb
// top-level json tags.
var kbEditFields = []string{
	"id", "name", "type", "description",
	"is_temporary", "is_pinned",
	"embedding_model_id", "summary_model_id",
	"knowledge_count", "chunk_count",
	"is_processing", "processing_count",
	"created_at", "updated_at",
}

type EditOptions struct {
	// Name/Description are *string so we can distinguish "unset" from "set to
	// empty". An unset field is omitted from the SDK request — only fields the
	// user passed are sent. Server PUT semantics are "replace everything in the
	// request"; if we always sent both, an `--name` invocation would silently
	// clear the description.
	Name        *string
	Description *string
}

// EditService is the narrow SDK surface this command depends on. GetKnowledgeBase
// is needed for the fetch-then-update flow: the server's UpdateKnowledgeBase
// endpoint requires Name on the PUT body (UpdateKnowledgeBaseRequest.Name is
// `string`, not `*string`, and the server validates `required`), so passing
// only --description without fetching the current Name would 400.
type EditService interface {
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
	UpdateKnowledgeBase(ctx context.Context, id string, req *sdk.UpdateKnowledgeBaseRequest) (*sdk.KnowledgeBase, error)
}

// NewCmdEdit builds `weknora kb edit <id>`. At least one of --name /
// --description must be provided.
func NewCmdEdit(f *cmdutil.Factory) *cobra.Command {
	opts := &EditOptions{}
	var name, desc string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a knowledge base's name or description",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			if c.Flag("name").Changed {
				opts.Name = &name
			}
			if c.Flag("description").Changed {
				opts.Description = &desc
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runEdit(c.Context(), opts, jopts, cli, args[0])
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name (omit to leave unchanged)")
	cmd.Flags().StringVar(&desc, "description", "", "New description (omit to leave unchanged)")
	cmdutil.AddJSONFlags(cmd, kbEditFields)
	aiclient.SetAgentHelp(cmd, "Edits a knowledge base. At least one of --name/--description is required. Fields not passed are preserved server-side. Returns the updated KnowledgeBase.")
	return cmd
}

func runEdit(ctx context.Context, opts *EditOptions, jopts *cmdutil.JSONOptions, svc EditService, id string) error {
	if opts.Name == nil && opts.Description == nil {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputMissingFlag,
			Message: "kb edit requires at least one of --name or --description",
			Hint:    "pass --name <name> and/or --description <desc>",
		}
	}

	// Fetch current state so we can fill in fields the user didn't touch.
	// TOCTOU note: another writer could change Name/Description between
	// our Get and Put; matches the same race window kb pin / unpin have.
	current, err := svc.GetKnowledgeBase(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch knowledge base %s", id)
	}
	req := &sdk.UpdateKnowledgeBaseRequest{
		Name:        current.Name,
		Description: current.Description,
	}
	if opts.Name != nil {
		req.Name = *opts.Name
	}
	if opts.Description != nil {
		req.Description = *opts.Description
	}

	updated, err := svc.UpdateKnowledgeBase(ctx, id, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "edit knowledge base %s", id)
	}
	if jopts.Enabled() {
		return format.WriteJSONFiltered(iostreams.IO.Out, updated, jopts.Fields, jopts.JQ)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Updated knowledge base %s\n", id)
	return nil
}
