package kb

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// StatusResult is the health-oriented response for `kb status <id>`.
// Shallow read only: 1 HTTP call, no failed-doc aggregation.
// For deep verification including failed_count, use `kb check <id>`.
type StatusResult struct {
	ID              string `json:"id"`
	Reachable       bool   `json:"reachable"`
	KnowledgeCount  int64  `json:"knowledge_count,omitempty"`
	ChunkCount      int64  `json:"chunk_count,omitempty"`
	IsProcessing    bool   `json:"is_processing,omitempty"`
	ProcessingCount int64  `json:"processing_count,omitempty"`
}

// StatusService is the narrow SDK surface needed for kb status.
type StatusService interface {
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
}

var kbStatusFields = []string{
	"id", "reachable", "knowledge_count", "chunk_count",
	"is_processing", "processing_count",
}

// NewCmdStatus builds `weknora kb status <id>`.
func NewCmdStatus(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <kb-id>",
		Short: "Show health status of a knowledge base (shallow, 1 HTTP)",
		Long: `Show health-oriented fields for a KB.

1 HTTP call:
  reachable / knowledge_count / chunk_count / is_processing / processing_count

For deep verification including failed_count, use 'weknora kb check <id>'
(1 + N HTTP, pages the doc list with parse_status=failed).

For full metadata (config / pinned / tenant), use 'weknora kb view <id>'.`,
		Example: `  weknora kb status kb_abc
  weknora kb status kb_abc --format json`,
		Args: cobra.ExactArgs(1),
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
			res, err := runStatus(c.Context(), cli, args[0])
			if err != nil {
				return err
			}
			return emitStatus(res, fopts, iostreams.IO.Out)
		},
	}
	cmdutil.AddFormatFlag(cmd, kbStatusFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "shallow health probe of a knowledge base (one HTTP call): reachability, no failed-doc aggregation",
		RequiredFlags: []string{"<kb-id> (positional)"},
		Examples:      []string{"weknora kb status kb_abc"},
		Output:        "envelope.data is {id, reachable, ...}; use `kb check` for deep failed-doc aggregation",
	})
	return cmd
}

// runStatus is the testable core: fetch KB metadata and return a StatusResult.
// Never returns an error for "kb not reachable" (Reachable=false carries
// that signal).
func runStatus(ctx context.Context, svc StatusService, id string) (*StatusResult, error) {
	kb, err := svc.GetKnowledgeBase(ctx, id)
	if err != nil {
		return &StatusResult{ID: id, Reachable: false}, nil
	}
	return &StatusResult{
		ID:              kb.ID,
		Reachable:       true,
		KnowledgeCount:  kb.KnowledgeCount,
		ChunkCount:      kb.ChunkCount,
		IsProcessing:    kb.IsProcessing,
		ProcessingCount: kb.ProcessingCount,
	}, nil
}

// emitStatus renders res using --format options. Mirrors emitWaitResult
// pattern from cli/cmd/doc/wait.go for consistency.
func emitStatus(res *StatusResult, fopts *cmdutil.FormatOptions, w io.Writer) error {
	switch fopts.Mode {
	case cmdutil.FormatJSON, cmdutil.FormatNDJSON:
		return fopts.Emit(w, res, nil)
	case cmdutil.FormatText, "":
		return writeStatusText(w, res)
	default:
		return fmt.Errorf("unsupported --format %q for kb status", fopts.Mode)
	}
}

func writeStatusText(w io.Writer, res *StatusResult) error {
	fmt.Fprintf(w, "ID:           %s\n", res.ID)
	fmt.Fprintf(w, "Reachable:    %v\n", res.Reachable)
	if !res.Reachable {
		return nil
	}
	fmt.Fprintf(w, "Knowledge:    %d\n", res.KnowledgeCount)
	fmt.Fprintf(w, "Chunks:       %d\n", res.ChunkCount)
	fmt.Fprintf(w, "Processing:   %v (%d active)\n", res.IsProcessing, res.ProcessingCount)
	return nil
}

// compile-time check: SDK client satisfies StatusService.
var _ StatusService = (*sdk.Client)(nil)
