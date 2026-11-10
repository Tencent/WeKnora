package modelcmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// modelViewFields enumerates the fields surfaced for `--format json` discovery
// on `model view`. Includes the nested `parameters` object (the single-record
// view is where callers want the full configuration).
var modelViewFields = []string{
	"id", "name", "display_name", "type", "source",
	"description", "is_default", "parameters", "created_at", "updated_at",
}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetModel(ctx context.Context, id string) (*sdk.Model, error)
}

// NewCmdView builds `weknora model view <model-id>`.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view <model-id>",
		Short: "Show a model by ID",
		Long:  `Fetch one model's full record: type, source, default flag, and parameters.`,
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
			return runView(c.Context(), fopts, cli, args[0])
		},
	}
	cmdutil.AddFormatFlag(cmd, modelViewFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "fetch one model's full record by id",
		RequiredFlags: []string{"<model-id> (positional)"},
		Examples:      []string{"weknora model view model_abc --jq .data.type"},
		Output:        "envelope.data is the Model object (id, name, display_name, type, source, is_default, parameters)",
	})
	return cmd
}

func runView(ctx context.Context, fopts *cmdutil.FormatOptions, svc ViewService, id string) error {
	m, err := svc.GetModel(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get model %q", id)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, m, nil)
	}
	w := iostreams.IO.Out
	fmt.Fprintf(w, "ID:          %s\n", m.ID)
	if label := modelLabel(*m); label != "" {
		fmt.Fprintf(w, "NAME:        %s\n", label)
	}
	fmt.Fprintf(w, "TYPE:        %s\n", m.Type)
	fmt.Fprintf(w, "SOURCE:      %s\n", m.Source)
	if m.IsDefault {
		fmt.Fprintf(w, "DEFAULT:     yes\n")
	}
	if m.Description != "" {
		fmt.Fprintf(w, "DESCRIPTION: %s\n", m.Description)
	}
	if m.CreatedAt != "" {
		fmt.Fprintf(w, "CREATED:     %s\n", m.CreatedAt)
	}
	if m.UpdatedAt != "" {
		fmt.Fprintf(w, "UPDATED:     %s\n", m.UpdatedAt)
	}
	return nil
}
