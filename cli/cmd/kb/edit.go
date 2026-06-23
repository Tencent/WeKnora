package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbEditFields enumerates the fields surfaced for `--format json` discovery on
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
	// empty". An unset field is omitted from the SDK request - only fields the
	// user passed are sent. Server PUT semantics are "replace everything in the
	// request"; if we always sent both, an `--name` invocation would silently
	// clear the description.
	Name        *string
	Description *string
	Yes         bool // sourced from global -y/--yes persistent flag
	DryRun      bool
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
		Use:   "edit <kb-id>",
		Short: "Edit a knowledge base's name or description",
		Long: `Update a knowledge base's name and/or description. At least one of
--name / --description must be supplied; fields you omit are preserved
server-side.

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
			if c.Flag("name").Changed {
				opts.Name = &name
			}
			if c.Flag("description").Changed {
				opts.Description = &desc
			}
			id := args[0]
			// Validate pure-local "at least one mutation flag" before the
			// dry-run gate so --dry-run rejects identically to the live path
			// (industry standard: gh / kubectl / lark all validate before
			// preview). Same typed Error as runEdit so live behavior is
			// unchanged, just earlier.
			if opts.Name == nil && opts.Description == nil {
				return &cmdutil.Error{
					Code:    cmdutil.CodeInputMissingFlag,
					Message: "kb edit requires at least one of --name or --description",
					Hint:    "pass --name <name> and/or --description <desc>",
				}
			}
			planArgs := map[string]any{"kb": id}
			if opts.Name != nil {
				planArgs["name"] = *opts.Name
			}
			if opts.Description != nil {
				planArgs["description"] = *opts.Description
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "kb.edit",
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
			retryCmd := buildKBEditRetryCmd(c, id)
			if err := cmdutil.ConfirmDestructive(f.Prompter(), opts.Yes, fopts.WantsJSON(), "edit", "knowledge base", id, "kb.edit", retryCmd); err != nil {
				return err
			}
			return runEdit(c.Context(), opts, fopts, cli, id)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name (omit to leave unchanged)")
	cmd.Flags().StringVar(&desc, "description", "", "New description (omit to leave unchanged)")
	cmdutil.AddFormatFlag(cmd, kbEditFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "kb.edit")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "update a knowledge base's name or description",
		RequiredFlags: []string{"<kb-id> (positional)", "--name or --description (at least one)"},
		Examples: []string{
			"weknora kb edit kb_abc --name \"New Name\" -y",
			"weknora kb edit kb_abc --description \"Updated desc\" --format json -y",
		},
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"kb edit overwrites fields; SDK uses fetch-then-update to avoid clobbering, but malformed input can still corrupt config.",
		},
	})
	return cmd
}

// buildKBEditRetryCmd constructs a directly-executable retry argv from the
// flags the user actually set so agents can surface a precise re-run command.
func buildKBEditRetryCmd(c *cobra.Command, id string) string {
	var parts []string
	parts = append(parts, "weknora", "kb", "edit", id)
	c.Flags().Visit(func(f *pflag.Flag) {
		switch f.Name {
		case "name":
			parts = append(parts, "--name", f.Value.String())
		case "description":
			parts = append(parts, "--description", f.Value.String())
		case "format":
			parts = append(parts, "--format", f.Value.String())
		}
	})
	parts = append(parts, "-y")
	return strings.Join(parts, " ")
}

func runEdit(ctx context.Context, opts *EditOptions, fopts *cmdutil.FormatOptions, svc EditService, id string) error {
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
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, updated, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Updated knowledge base %s\n", id)
	return nil
}
