package intentverdictcmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeExportSvc struct {
	export *sdk.VerdictCorpusExport
	err    error
	gotModel string
	gotLimit int
}

func (f *fakeExportSvc) ExportVerdictCorpus(_ context.Context, judgeModel string, limit int) (*sdk.VerdictCorpusExport, error) {
	f.gotModel = judgeModel
	f.gotLimit = limit
	return f.export, f.err
}

func testExport() *sdk.VerdictCorpusExport {
	return &sdk.VerdictCorpusExport{
		JudgeModel: "builtin-kimi-coding",
		Count:      1,
		Entries: []*sdk.VerdictCorpusEntry{{
			VerdictID:      "v1",
			SessionID:      "s1",
			ToolCallID:     "call-1",
			Prompt:         "帮我把订单 A100 退款",
			ToolCall:       json.RawMessage(`{"name":"mcp_echo_echo_x","arguments":{"text":"hi"}}`),
			Verdict:        "require_approval",
			Reason:         "策略判定：需人工确认",
			Layer:          "judge",
			ModeAtDecision: "enforce",
			HumanOverride:  "approved",
			JudgeModel:     "builtin-kimi-coding",
		}},
	}
}

// TestExportJSON：默认走 stdout 的 JSON envelope，schema 关键字段
//（prompt/tool_call/verdict/human_override/judge_model）齐全。
func TestExportJSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeExportSvc{export: testExport()}
	opts := &ExportOptions{JudgeModel: "builtin-kimi-coding", Limit: 100}

	if err := runExport(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	if svc.gotModel != "builtin-kimi-coding" || svc.gotLimit != 100 {
		t.Fatalf("service args = (%q, %d), want (builtin-kimi-coding, 100)", svc.gotModel, svc.gotLimit)
	}
	got := out.String()
	for _, want := range []string{
		`"judge_model":"builtin-kimi-coding"`,
		`"prompt":"帮我把订单 A100 退款"`,
		`"tool_call":`,
		`"verdict":"require_approval"`,
		`"human_override":"approved"`,
		`"reason":"策略判定：需人工确认"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestExportJudgeModelFilter：--judge-model 原样传给服务端（分层过滤的
// [unit] 半边；服务端过滤正确性由 repository 测试覆盖）。
func TestExportJudgeModelFilter(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeExportSvc{export: &sdk.VerdictCorpusExport{JudgeModel: "", Count: 0, Entries: nil}}
	opts := &ExportOptions{JudgeModel: "", Limit: 10}

	if err := runExport(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	if svc.gotModel != "" {
		t.Fatalf("judge model = %q, want empty（规则层/baseline 行）", svc.gotModel)
	}
}

// TestExportOutFile：--out 把语料文档写盘，envelope 只报路径与计数。
func TestExportOutFile(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeExportSvc{export: testExport()}
	path := filepath.Join(t.TempDir(), "corpus.json")
	opts := &ExportOptions{JudgeModel: "builtin-kimi-coding", Limit: 100, Out: path}

	if err := runExport(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("corpus file not written: %v", err)
	}
	var doc corpusDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("corpus file is not valid JSON: %v", err)
	}
	if doc.Count != 1 || len(doc.Entries) != 1 || doc.JudgeModel != "builtin-kimi-coding" {
		t.Fatalf("corpus document = %+v", doc)
	}
	entry := doc.Entries[0]
	if entry.Prompt == "" || entry.ToolCall == nil || entry.Verdict == "" ||
		entry.HumanOverride != "approved" || entry.JudgeModel != "builtin-kimi-coding" {
		t.Fatalf("corpus entry 缺关键字段: %+v", entry)
	}
	got := out.String()
	if !strings.Contains(got, filepath.Base(path)) || !strings.Contains(got, `"count":1`) {
		t.Errorf("envelope 缺路径/计数:\n%s", got)
	}
}

// TestExportServiceError 透传服务端错误，不得包装成成功。
func TestExportServiceError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeExportSvc{err: errors.New("HTTP error 403")}
	if err := runExport(context.Background(), &ExportOptions{Limit: 10},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc); err == nil {
		t.Fatal("service error must propagate")
	}
}

// TestExportLimitValidation：--limit 越界在客户端即拒绝（exit 5 语义）。
func TestExportLimitValidation(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeExportSvc{export: testExport()}
	for _, limit := range []int{0, 1001, -3} {
		err := runExport(context.Background(), &ExportOptions{Limit: limit},
			&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc)
		var cerr *cmdutil.Error
		if !errors.As(err, &cerr) || cerr.Code != cmdutil.CodeInputInvalidArgument {
			t.Fatalf("limit=%d: err = %v, want input.invalid_argument", limit, err)
		}
	}
	if svc.gotLimit != 0 {
		t.Fatalf("invalid limit must not reach the service, got %d", svc.gotLimit)
	}
}
