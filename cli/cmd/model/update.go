package modelcmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// UpdateOptions captures the surgical flag state for `model update`. Per-flag
// *Set bits distinguish "" (clear) from unset, matching agent/doc update.
type UpdateOptions struct {
	DisplayName string
	Description string
	BaseURL     string
	APIKeyStdin bool
	Params      []string
	Default     bool
	DryRun      bool
	StdinReader io.Reader
	flags       modelUpdateFlags
}

type modelUpdateFlags struct{ displayName, description, baseURL, def bool }

// UpdateService is the narrow SDK surface. UpdateModel is a full PUT, so the
// fetch (GetModel) is mandatory — without the baseline, any field not touched
// by a flag would clobber to its zero value.
type UpdateService interface {
	GetModel(ctx context.Context, id string) (*sdk.Model, error)
	UpdateModel(ctx context.Context, id string, req *sdk.UpdateModelRequest) (*sdk.Model, error)
}

// NewCmdUpdate builds `weknora model update <model-id>` — update a registered
// model in place (id preserved), so KBs / agents referencing it keep working.
func NewCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	opts := &UpdateOptions{}
	cmd := &cobra.Command{
		Use:   "update <model-id>",
		Short: "Update a model in place (rotate key, base URL, display name, default)",
		Long: `Update a registered model WITHOUT changing its id, so KBs / agents that
reference it keep working (unlike delete + re-create, which orphans references).
Rotate the provider key with --api-key-stdin, or change --base-url,
--display-name, --description, extra --param entries, or --default. A model's
type and source are immutable — register a new model to change them.

Reversible write: without -y/--yes in a non-TTY / JSON context it exits 10
(input.confirmation_required) without applying the change.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			id := args[0]
			opts.flags.displayName = c.Flags().Changed("display-name")
			opts.flags.description = c.Flags().Changed("description")
			opts.flags.baseURL = c.Flags().Changed("base-url")
			opts.flags.def = c.Flags().Changed("default")
			if !modelUpdateHasFlag(opts) {
				return &cmdutil.Error{
					Code:    cmdutil.CodeInputInvalidArgument,
					Message: "model update requires at least one flag",
					Hint:    "pass e.g. --display-name, --base-url, --api-key-stdin, --param, or --default",
				}
			}
			params, err := parseParams(opts.Params)
			if err != nil {
				return err
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "model.update",
				Args:   map[string]any{"model": id, "display_name": opts.DisplayName, "base_url": opts.BaseURL, "default": opts.Default, "rotate_api_key": opts.APIKeyStdin, "param_count": len(params)},
			}); handled {
				return err
			}
			yes, _ := c.Flags().GetBool("yes")
			// --api-key-stdin / --param excluded from retry_argv (stdin secret /
			// repeatable), matching agent update's multi-value exclusions.
			retry := cmdutil.BuildRetryArgv(c, []string{"weknora", "model", "update", id},
				"display-name", "description", "base-url", "default", "format")
			if err := cmdutil.ConfirmWrite(f.Prompter(), yes, fopts.WantsJSON(), "update", "model", id, "model.update", retry); err != nil {
				return err
			}
			if opts.StdinReader == nil {
				opts.StdinReader = iostreams.IO.In
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runUpdate(c.Context(), opts, fopts, cli, id, params)
		},
	}
	cmd.Flags().StringVar(&opts.DisplayName, "display-name", "", "New human-friendly name")
	cmd.Flags().StringVar(&opts.Description, "description", "", "New description")
	cmd.Flags().StringVar(&opts.BaseURL, "base-url", "", "New model API base URL")
	cmd.Flags().BoolVar(&opts.APIKeyStdin, "api-key-stdin", false, "Rotate the provider API key, read from stdin (kept out of argv / history)")
	cmd.Flags().StringArrayVar(&opts.Params, "param", nil, "Set an extra provider parameter as key=value, repeatable (value parsed as JSON)")
	cmd.Flags().BoolVar(&opts.Default, "default", false, "Mark this the default model for its type (--default=false to unset)")
	cmdutil.AddFormatFlag(cmd, modelListFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetWriteRisk(cmd, "model.update")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "update a registered model IN PLACE (id preserved, so KB/agent references keep working): rotate --api-key-stdin, change --base-url / --display-name / --description / --param, or set --default. Type and source are immutable.",
		RequiredFlags: []string{"<model-id> (positional)", "at least one update flag"},
		Examples: []string{
			`printf '%s' "$NEW_KEY" | weknora model update mdl_abc --api-key-stdin -y`,
			`weknora model update mdl_abc --base-url https://api.example.com/v1 -y`,
			`weknora model update mdl_abc --default -y`,
		},
		Output: "envelope.data is the updated Model object (id preserved; provider api key never echoed)",
		Warnings: []string{
			"Reversible write: requires explicit approval (exit 10 / input.confirmation_required) unless -y; never auto-add -y.",
			"Server-side this is an admin operation; a non-admin credential gets auth.forbidden (exit 3).",
		},
	})
	return cmd
}

func modelUpdateHasFlag(o *UpdateOptions) bool {
	return o.flags.displayName || o.flags.description || o.flags.baseURL || o.flags.def ||
		o.APIKeyStdin || len(o.Params) > 0
}

func runUpdate(ctx context.Context, opts *UpdateOptions, fopts *cmdutil.FormatOptions, svc UpdateService, id string, params map[string]any) error {
	// Fetch-then-update: UpdateModel is a full PUT, so start from the server's
	// current state and overlay only what the user changed.
	cur, err := svc.GetModel(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch model %s", id)
	}
	merged := sdk.ModelParameters{}
	for k, v := range cur.Parameters {
		merged[k] = v
	}
	for k, v := range params {
		merged[k] = v
	}
	if opts.flags.baseURL {
		merged["base_url"] = opts.BaseURL
	}
	if opts.APIKeyStdin {
		key, err := readStdinTrimmed(opts.StdinReader)
		if err != nil {
			return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read API key from stdin")
		}
		if key == "" {
			return cmdutil.NewError(cmdutil.CodeInputMissingFlag, "--api-key-stdin requires the key piped to stdin")
		}
		merged["api_key"] = key
	}

	req := &sdk.UpdateModelRequest{
		Name:        cur.Name,
		DisplayName: cur.DisplayName,
		Description: cur.Description,
		Parameters:  merged,
		IsDefault:   cur.IsDefault,
	}
	if opts.flags.displayName {
		req.DisplayName = opts.DisplayName
	}
	if opts.flags.description {
		req.Description = opts.Description
	}
	if opts.flags.def {
		req.IsDefault = opts.Default
	}

	updated, err := svc.UpdateModel(ctx, id, req)
	if err != nil {
		return cmdutil.WrapHTTP(err, "update model %s", id)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, updated, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Updated model %q (id: %s)\n", updated.Name, updated.ID)
	return nil
}

// compile-time check: the production SDK client implements UpdateService.
var _ UpdateService = (*sdk.Client)(nil)
