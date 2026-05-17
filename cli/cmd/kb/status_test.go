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

type fakeStatusSvc struct {
	kb     *sdk.KnowledgeBase
	getErr error
}

func (f *fakeStatusSvc) GetKnowledgeBase(_ context.Context, id string) (*sdk.KnowledgeBase, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.kb, nil
}

func TestRunStatus_ShallowFields(t *testing.T) {
	svc := &fakeStatusSvc{kb: &sdk.KnowledgeBase{
		ID:              "kb_x",
		KnowledgeCount:  42,
		ChunkCount:      100,
		IsProcessing:    true,
		ProcessingCount: 3,
	}}
	res, err := runStatus(context.Background(), svc, "kb_x")
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if !res.Reachable {
		t.Error("Reachable=false, want true")
	}
	if res.KnowledgeCount != 42 || res.ChunkCount != 100 || res.ProcessingCount != 3 || !res.IsProcessing {
		t.Errorf("got %+v", res)
	}
}

func TestRunStatus_Unreachable(t *testing.T) {
	svc := &fakeStatusSvc{getErr: fmt.Errorf("404 not found")}
	res, err := runStatus(context.Background(), svc, "kb_x")
	if err != nil {
		t.Fatalf("runStatus should not return err on unreachable; got %v", err)
	}
	if res.Reachable {
		t.Error("Reachable=true, want false")
	}
	if res.ID != "kb_x" {
		t.Errorf("ID=%q, want kb_x (echoed even when unreachable)", res.ID)
	}
}

func TestEmitStatus_JSON(t *testing.T) {
	var buf bytes.Buffer
	res := &StatusResult{ID: "kb_x", Reachable: true, KnowledgeCount: 5}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := emitStatus(res, fopts, &buf); err != nil {
		t.Fatalf("emitStatus: %v", err)
	}
	var got StatusResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if got.ID != "kb_x" || got.KnowledgeCount != 5 {
		t.Errorf("got %+v", got)
	}
}

func TestEmitStatus_TextHuman(t *testing.T) {
	var buf bytes.Buffer
	res := &StatusResult{ID: "kb_x", Reachable: true, KnowledgeCount: 5, ChunkCount: 20, IsProcessing: true, ProcessingCount: 1}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	if err := emitStatus(res, fopts, &buf); err != nil {
		t.Fatalf("emitStatus: %v", err)
	}
	for _, want := range []string{"kb_x", "5", "20", "true"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}
