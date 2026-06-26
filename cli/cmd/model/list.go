package modelcmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// modelListFields enumerates the fields surfaced for `--format json` discovery
// on `model list`. The nested `parameters` object is omitted — use `--jq` for
// it (and `model view` renders the full record).
var modelListFields = []string{
	"id", "name", "display_name", "type", "source",
	"description", "is_default", "created_at", "updated_at",
}

// modelTypeValues / modelSourceValues are the closed server enums the
// corresponding filter flags accept, sourced from the SDK's enumerators so the
// CLI can't drift from the SDK/server. A typo is rejected up front rather than
// silently returning an empty set (which an agent cannot distinguish from a
// genuine no-match).
var modelTypeValues = cmdutil.EnumStrings(sdk.AllModelTypes())

var modelSourceValues = cmdutil.EnumStrings(sdk.AllModelSources())

// ListOptions captures `model list` filter flag state.
type ListOptions struct {
	// Type / Source, when set, restrict output to models of that type
	// (Embedding, Rerank, KnowledgeQA, VLLM, ASR) or provider (local, openai,
	// …), matched case-insensitively. Empty shows everything.
	Type   string
	Source string
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	ListModels(ctx context.Context) ([]sdk.Model, error)
}

// NewCmdList builds `weknora model list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List models configured on the server",
		Long: `List the models configured on the server, sorted by type then name. Pass
--type to restrict to one model type (Embedding, Rerank, KnowledgeQA, VLLM, ASR).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.Type, "type", "", "Only show models of this type (Embedding, Rerank, KnowledgeQA, VLLM, ASR)")
	cmd.Flags().StringVar(&opts.Source, "source", "", "Only show models from this provider (local, remote, openai, aliyun, …)")
	cmdutil.AddFormatFlag(cmd, modelListFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "discover model ids for `agent create --model` and a KB's embedding/summary model",
		Examples: []string{
			"weknora model list",
			"weknora model list --type KnowledgeQA --format json",
			"weknora model list --source local",
		},
		Output: "envelope.data is an array of Model objects (id, name, display_name, type, source, is_default); narrow it with --type / --source",
	})
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, fopts *cmdutil.FormatOptions, svc ListService) error {
	if _, err := cmdutil.ValidateEnum("type", opts.Type, modelTypeValues); err != nil {
		return err
	}
	if _, err := cmdutil.ValidateEnum("source", opts.Source, modelSourceValues); err != nil {
		return err
	}
	items, err := svc.ListModels(ctx)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list models")
	}
	if items == nil {
		items = []sdk.Model{} // ensure JSON [] not null
	}
	hasFilter := opts.Type != "" || opts.Source != ""
	if hasFilter {
		filtered := items[:0]
		for _, m := range items {
			if opts.Type != "" && !strings.EqualFold(string(m.Type), opts.Type) {
				continue
			}
			if opts.Source != "" && !strings.EqualFold(string(m.Source), opts.Source) {
				continue
			}
			filtered = append(filtered, m)
		}
		items = filtered
	}
	// Deterministic order: by type, then label. Server return order is not
	// guaranteed, so a client-side sort keeps output stable.
	sort.Slice(items, func(i, j int) bool {
		if items[i].Type != items[j].Type {
			return items[i].Type < items[j].Type
		}
		return modelLabel(items[i]) < modelLabel(items[j])
	})

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, items, &output.Meta{Count: output.IntPtr(len(items))})
	}

	if len(items) == 0 {
		if hasFilter {
			fmt.Fprintln(iostreams.IO.Out, "(no models match the filter)")
			return nil
		}
		fmt.Fprintln(iostreams.IO.Out, "(no models)")
		return nil
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tSOURCE\tDEFAULT")
	for _, m := range items {
		def := ""
		if m.IsDefault {
			def = "default"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			m.ID, text.Truncate(40, modelLabel(m)), m.Type, m.Source, def)
	}
	return tw.Flush()
}
