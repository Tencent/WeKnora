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

// CheckResult is the deep-verification response for `kb check <id>`.
// Superset of StatusResult: includes all status fields plus FailedCount
// aggregated by paging the doc list. Verb split with `kb status`:
// status reads existing state cheaply, check actively verifies.
type CheckResult struct {
	ID              string `json:"id"`
	Reachable       bool   `json:"reachable"`
	KnowledgeCount  int64  `json:"knowledge_count,omitempty"`
	ChunkCount      int64  `json:"chunk_count,omitempty"`
	IsProcessing    bool   `json:"is_processing,omitempty"`
	ProcessingCount int64  `json:"processing_count,omitempty"`
	FailedCount     int64  `json:"failed_count"` // always populated (no omitempty)
}

// CheckService is the narrow SDK surface needed for kb check.
type CheckService interface {
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
	ListKnowledgeWithFilter(ctx context.Context, kbID string, page, pageSize int, filter sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error)
}

var kbCheckFields = []string{
	"id", "reachable", "knowledge_count", "chunk_count",
	"is_processing", "processing_count", "failed_count",
}

// NewCmdCheck builds `weknora kb check <id>`.
func NewCmdCheck(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <kb-id>",
		Short: "Verify a knowledge base end-to-end (status + failed-doc aggregation)",
		Long: `Active verification of a knowledge base.

Performs 1 + N HTTP calls:
  1   GET /kb/{id} — reachable + counts + processing state
  N   page-walk doc list with parse_status=failed — failed_count

Use 'weknora kb status <id>' for a fast read-only health snapshot
(1 HTTP call, no failed_count). Use 'kb check' when you need
verification including failed-doc aggregation.`,
		Example: `  weknora kb check kb_abc
  weknora kb check kb_abc --format json`,
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
			res, err := runCheck(c.Context(), cli, args[0])
			if err != nil {
				return err
			}
			return emitCheck(res, fopts, iostreams.IO.Out)
		},
	}
	cmdutil.AddFormatFlag(cmd, kbCheckFields...)
	return cmd
}

// runCheck is the testable core: status fields + failed-doc aggregation.
// Never returns an error for "kb not reachable" (Reachable=false carries
// the signal).
func runCheck(ctx context.Context, svc CheckService, id string) (*CheckResult, error) {
	kb, err := svc.GetKnowledgeBase(ctx, id)
	if err != nil {
		return &CheckResult{ID: id, Reachable: false}, nil
	}
	res := &CheckResult{
		ID:              kb.ID,
		Reachable:       true,
		KnowledgeCount:  kb.KnowledgeCount,
		ChunkCount:      kb.ChunkCount,
		IsProcessing:    kb.IsProcessing,
		ProcessingCount: kb.ProcessingCount,
	}
	failed, err := aggregateFailedCount(ctx, svc, id)
	if err != nil {
		return res, fmt.Errorf("aggregate failed_count: %w", err)
	}
	res.FailedCount = failed
	return res, nil
}

// aggregateFailedCount pages the doc list with parse_status=failed and
// returns the total count. Termination uses accumulated total (not
// page*pageSize) so server-capped page sizes don't truncate.
func aggregateFailedCount(ctx context.Context, svc CheckService, kbID string) (int64, error) {
	const pageSize = 100
	var total int64
	page := 1
	for {
		docs, totalAvailable, err := svc.ListKnowledgeWithFilter(ctx, kbID, page, pageSize, sdk.KnowledgeListFilter{ParseStatus: "failed"})
		if err != nil {
			return total, err
		}
		total += int64(len(docs))
		if total >= totalAvailable || len(docs) == 0 {
			break
		}
		page++
	}
	return total, nil
}

// emitCheck renders res. Same dispatch as emitStatus.
func emitCheck(res *CheckResult, fopts *cmdutil.FormatOptions, w io.Writer) error {
	switch fopts.Mode {
	case cmdutil.FormatJSON, cmdutil.FormatNDJSON:
		return fopts.Emit(w, res)
	case cmdutil.FormatText, "":
		return writeCheckText(w, res)
	default:
		return fmt.Errorf("unsupported --format %q for kb check", fopts.Mode)
	}
}

func writeCheckText(w io.Writer, res *CheckResult) error {
	fmt.Fprintf(w, "ID:           %s\n", res.ID)
	fmt.Fprintf(w, "Reachable:    %v\n", res.Reachable)
	if !res.Reachable {
		return nil
	}
	fmt.Fprintf(w, "Knowledge:    %d\n", res.KnowledgeCount)
	fmt.Fprintf(w, "Chunks:       %d\n", res.ChunkCount)
	fmt.Fprintf(w, "Processing:   %v (%d active)\n", res.IsProcessing, res.ProcessingCount)
	fmt.Fprintf(w, "Failed:       %d\n", res.FailedCount)
	return nil
}

// compile-time check: SDK client satisfies CheckService.
var _ CheckService = (*sdk.Client)(nil)
