package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeCheckSvc struct {
	kb         *sdk.KnowledgeBase
	getErr     error
	failedDocs []sdk.Knowledge // returned by ListKnowledgeWithFilter when ParseStatus=failed
	listErr    error
}

func (f *fakeCheckSvc) GetKnowledgeBase(_ context.Context, id string) (*sdk.KnowledgeBase, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.kb, nil
}

func (f *fakeCheckSvc) ListKnowledgeWithFilter(_ context.Context, _ string, page, pageSize int, _ sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error) {
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	// Single-page fake: return all docs at once (page 1), empty thereafter.
	if page == 1 {
		return f.failedDocs, int64(len(f.failedDocs)), nil
	}
	return nil, int64(len(f.failedDocs)), nil
}

func TestRunCheck_AggregatesFailed(t *testing.T) {
	svc := &fakeCheckSvc{
		kb: &sdk.KnowledgeBase{ID: "kb_x", KnowledgeCount: 5, ChunkCount: 20},
		failedDocs: []sdk.Knowledge{
			{ID: "d1", ParseStatus: "failed"},
			{ID: "d2", ParseStatus: "failed"},
		},
	}
	res, err := runCheck(context.Background(), svc, "kb_x")
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if res.FailedCount != 2 {
		t.Errorf("FailedCount=%d, want 2", res.FailedCount)
	}
	if !res.Reachable {
		t.Error("Reachable=false, want true")
	}
	if res.KnowledgeCount != 5 || res.ChunkCount != 20 {
		t.Errorf("got %+v", res)
	}
}

func TestRunCheck_Unreachable(t *testing.T) {
	svc := &fakeCheckSvc{getErr: fmt.Errorf("404 not found")}
	res, err := runCheck(context.Background(), svc, "kb_x")
	if err != nil {
		t.Fatalf("runCheck should not return err on unreachable; got %v", err)
	}
	if res.Reachable {
		t.Error("Reachable=true, want false")
	}
	if res.ID != "kb_x" {
		t.Errorf("ID=%q, want kb_x (echoed even when unreachable)", res.ID)
	}
}

func TestRunCheck_NoFailedDocs(t *testing.T) {
	svc := &fakeCheckSvc{
		kb:         &sdk.KnowledgeBase{ID: "kb_y", KnowledgeCount: 10},
		failedDocs: nil,
	}
	res, err := runCheck(context.Background(), svc, "kb_y")
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if res.FailedCount != 0 {
		t.Errorf("FailedCount=%d, want 0", res.FailedCount)
	}
}

func TestEmitCheck_JSON(t *testing.T) {
	var buf bytes.Buffer
	res := &CheckResult{ID: "kb_x", Reachable: true, KnowledgeCount: 5, FailedCount: 3}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := emitCheck(res, fopts, &buf); err != nil {
		t.Fatalf("emitCheck: %v", err)
	}
	var env struct {
		OK   bool        `json:"ok"`
		Data CheckResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	got := env.Data
	if got.ID != "kb_x" || got.KnowledgeCount != 5 || got.FailedCount != 3 {
		t.Errorf("got %+v", got)
	}
}

func TestEmitCheck_Text(t *testing.T) {
	var buf bytes.Buffer
	res := &CheckResult{
		ID: "kb_x", Reachable: true,
		KnowledgeCount: 5, ChunkCount: 20,
		IsProcessing: true, ProcessingCount: 1,
		FailedCount: 2,
	}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	if err := emitCheck(res, fopts, &buf); err != nil {
		t.Fatalf("emitCheck: %v", err)
	}
	for _, want := range []string{"kb_x", "5", "20", "true", "2"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
	// "Failed:" line must always appear for check
	if !strings.Contains(buf.String(), "Failed:") {
		t.Errorf("output missing 'Failed:' line:\n%s", buf.String())
	}
}
