package intentverdictcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	sdk "github.com/Tencent/WeKnora/client"
)

// ExportOptions captures `intent-verdicts export` flag state.
type ExportOptions struct {
	// JudgeModel filters rows by the judge model that produced the verdict
	// (intent_verdicts.judge_model). Empty string selects rule-layer /
	// baseline rows — verdicts with no judge attribution.
	JudgeModel string
	// Limit caps rows returned by the server (default 1000, hard cap).
	Limit int
	// Out, when set, writes the corpus document to this file instead of
	// streaming it to stdout.
	Out string
}

// ExportService is the narrow SDK surface this command depends on.
type ExportService interface {
	ExportVerdictCorpus(ctx context.Context, judgeModel string, limit int) (*sdk.VerdictCorpusExport, error)
}

// NewCmdExport builds `weknora intent-verdicts export`.
func NewCmdExport(f *cmdutil.Factory) *cobra.Command {
	opts := &ExportOptions{}
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export verdict corpus grouped by judge model (T61)",
		Long: `Export the IntentGate verdict log as a structured corpus file.

Each entry carries the original user prompt, the tool call, the verdict with
its reason, the human override from approvals, and the judge model that
produced the verdict. Filter with --judge-model to export one model tier at a
time (an empty value exports rule-layer / baseline rows).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Validate before building the client so a bad --limit surfaces
			// as input.invalid_argument (exit 5), not an auth error (exit 3).
			if err := validateExportOpts(opts); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runExport(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.JudgeModel, "judge-model", "",
		"Only export verdicts produced by this judge model ID (empty = rule-layer/baseline rows)")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 1000, "Maximum rows to export (1..1000)")
	cmd.Flags().StringVarP(&opts.Out, "out", "o", "",
		"Write the corpus document to this file instead of stdout")
	cmdutil.AddFormatFlag(cmd, "judge_model", "count", "entries")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "download the IntentGate verdict corpus for judge distillation or evaluation",
		Examples: []string{
			"weknora intent-verdicts export --judge-model builtin-kimi-coding -o corpus.json",
			"weknora intent-verdicts export --limit 500",
		},
		Output: "envelope.data is {judge_model, count, entries[]}; each entry has prompt/tool_call/verdict/reason/human_override/judge_model; with --out, data is {path, judge_model, count} and the corpus document lands on disk",
	})
	return cmd
}

// validateExportOpts checks --limit. Called from RunE before the client is
// built (so a bad value surfaces as exit 5, not an auth error) and at
// runExport's top for direct callers; idempotent.
func validateExportOpts(opts *ExportOptions) error {
	if opts.Limit < 1 || opts.Limit > 1000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 1..1000, got %d", opts.Limit),
		}
	}
	return nil
}

// corpusDocument is the on-disk / on-wire corpus shape. Kept as an explicit
// map so the file schema (prompt/tool_call/verdict/human_override/judge_model)
// is stable independently of the SDK struct layout.
type corpusDocument struct {
	JudgeModel string                  `json:"judge_model"`
	Count      int                     `json:"count"`
	Entries    []*sdk.VerdictCorpusEntry `json:"entries"`
}

func runExport(ctx context.Context, opts *ExportOptions, fopts *cmdutil.FormatOptions, svc ExportService) error {
	if err := validateExportOpts(opts); err != nil {
		return err
	}
	export, err := svc.ExportVerdictCorpus(ctx, opts.JudgeModel, opts.Limit)
	if err != nil {
		return err
	}
	doc := &corpusDocument{JudgeModel: export.JudgeModel, Count: export.Count, Entries: export.Entries}

	if opts.Out != "" {
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fmt.Errorf("encode corpus document: %w", err)
		}
		if err := os.WriteFile(opts.Out, raw, 0o644); err != nil {
			return &cmdutil.Error{
				Code:    cmdutil.CodeInputInvalidArgument,
				Message: fmt.Sprintf("write corpus file: %v", err),
			}
		}
		data := map[string]any{"path": opts.Out, "judge_model": doc.JudgeModel, "count": doc.Count}
		meta := &output.Meta{Count: output.IntPtr(doc.Count)}
		if fopts.WantsJSON() {
			return fopts.Emit(iostreams.IO.Out, data, meta)
		}
		fmt.Fprintf(iostreams.IO.Out, "exported %d verdicts (judge_model=%q) to %s\n",
			doc.Count, doc.JudgeModel, opts.Out)
		return nil
	}

	meta := &output.Meta{Count: output.IntPtr(doc.Count)}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, doc, meta)
	}
	fmt.Fprintf(iostreams.IO.Out, "%d verdicts (judge_model=%q); use --format json for the full corpus\n",
		doc.Count, doc.JudgeModel)
	return nil
}
