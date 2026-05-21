package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/Tencent/WeKnora/client"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

func TestWaitCmd_Shape(t *testing.T) {
	cmd := NewCmdWait(&cmdutil.Factory{})
	if !strings.Contains(cmd.Use, "<doc-id>") {
		t.Errorf("Use=%q missing <doc-id>", cmd.Use)
	}
	if f := cmd.Flags().Lookup("timeout"); f == nil {
		t.Error("missing --timeout flag")
	}
	if f := cmd.Flags().Lookup("interval"); f == nil {
		t.Error("missing --interval flag")
	}
	// wait is always wait-all: no fail-fast option. --keep-going is
	// intentionally absent; users who want fail-fast compose with shell
	// short-circuiting (`wait id1 && wait id2`).
	if f := cmd.Flags().Lookup("keep-going"); f != nil {
		t.Error("--keep-going should not be registered; wait is always wait-all")
	}
	// --format is a persistent root flag (v0.7), not per-command — skip its lookup here.
}

func TestWaitCmd_DefaultFlagValues(t *testing.T) {
	cmd := NewCmdWait(&cmdutil.Factory{})
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	tf, _ := cmd.Flags().GetDuration("timeout")
	if tf != 10*time.Minute {
		t.Errorf("--timeout default = %v, want 10m", tf)
	}
	in, _ := cmd.Flags().GetDuration("interval")
	if in != 2*time.Second {
		t.Errorf("--interval default = %v, want 2s", in)
	}
}

// ---------------------------------------------------------------------------
// B2: waitForDocs tests
// ---------------------------------------------------------------------------

type fakeKnowledgeSvc struct {
	mu       sync.Mutex
	calls    map[string]int      // id → call count
	sequence map[string][]string // id → parse_status sequence to return
}

func newFakeKBSvc(seq map[string][]string) *fakeKnowledgeSvc {
	return &fakeKnowledgeSvc{
		calls:    make(map[string]int),
		sequence: seq,
	}
}

func (f *fakeKnowledgeSvc) GetKnowledge(_ context.Context, id string) (*sdk.Knowledge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls[id]
	f.calls[id]++
	seq, ok := f.sequence[id]
	if !ok {
		return nil, fmt.Errorf("unexpected id %q", id)
	}
	if idx >= len(seq) {
		idx = len(seq) - 1
	}
	status := seq[idx]
	k := &sdk.Knowledge{ID: id, ParseStatus: status}
	if status == "failed" {
		k.ErrorMessage = "parse error"
	}
	return k, nil
}

func TestWaitForDocs_SingleIDCompletes(t *testing.T) {
	svc := newFakeKBSvc(map[string][]string{
		"doc_x": {"processing", "processing", "completed"},
	})
	res, err := waitForDocs(context.Background(), []string{"doc_x"}, svc, WaitOptions{
		Timeout:  5 * time.Second,
		Interval: 1 * time.Millisecond, // fast for tests
	})
	if err != nil {
		t.Fatalf("waitForDocs: %v", err)
	}
	if len(res.Completed) != 1 || res.Completed[0] != "doc_x" {
		t.Errorf("Completed=%v, want [doc_x]", res.Completed)
	}
	if svc.calls["doc_x"] < 3 {
		t.Errorf("expected at least 3 polls, got %d", svc.calls["doc_x"])
	}
}

func TestWaitForDocs_SingleIDFails(t *testing.T) {
	svc := newFakeKBSvc(map[string][]string{
		"doc_x": {"failed"},
	})
	res, _ := waitForDocs(context.Background(), []string{"doc_x"}, svc, WaitOptions{
		Timeout:  5 * time.Second,
		Interval: 1 * time.Millisecond,
	})
	if len(res.Failed) != 1 || res.Failed[0].ID != "doc_x" {
		t.Errorf("Failed=%v, want [doc_x]", res.Failed)
	}
	if res.Failed[0].Message != "parse error" {
		t.Errorf("Failed[0].Message=%q, want %q", res.Failed[0].Message, "parse error")
	}
}

func TestWaitForDocs_Timeout(t *testing.T) {
	svc := newFakeKBSvc(map[string][]string{
		"doc_x": {"processing"}, // never completes
	})
	res, _ := waitForDocs(context.Background(), []string{"doc_x"}, svc, WaitOptions{
		Timeout:  20 * time.Millisecond, // tight
		Interval: 5 * time.Millisecond,
	})
	if len(res.Timeout) != 1 || res.Timeout[0] != "doc_x" {
		t.Errorf("Timeout=%v, want [doc_x]", res.Timeout)
	}
}

// ---------------------------------------------------------------------------
// B3: multi-id wait-all behavior
// ---------------------------------------------------------------------------

func TestWaitForDocs_MultiID_AllSucceed(t *testing.T) {
	svc := newFakeKBSvc(map[string][]string{
		"a": {"processing", "completed"},
		"b": {"completed"},
		"c": {"processing", "processing", "completed"},
	})
	res, err := waitForDocs(context.Background(), []string{"a", "b", "c"}, svc, WaitOptions{
		Timeout:  5 * time.Second,
		Interval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("waitForDocs: %v", err)
	}
	if len(res.Completed) != 3 {
		t.Errorf("Completed=%v (len %d), want 3", res.Completed, len(res.Completed))
	}
	if len(res.Failed) != 0 || len(res.Timeout) != 0 {
		t.Errorf("Failed=%v Timeout=%v, want both empty", res.Failed, res.Timeout)
	}
}

// TestWaitForDocs_MultiID_WaitAllOnPartialFailure verifies wait-all
// semantics: even when one id fails terminally, the remaining ids are
// still waited on until they reach their own terminal state.
func TestWaitForDocs_MultiID_WaitAllOnPartialFailure(t *testing.T) {
	svc := newFakeKBSvc(map[string][]string{
		"a": {"failed"},
		"b": {"processing", "completed"},
		"c": {"completed"},
	})
	res, _ := waitForDocs(context.Background(), []string{"a", "b", "c"}, svc, WaitOptions{
		Timeout:  5 * time.Second,
		Interval: 1 * time.Millisecond,
	})
	if len(res.Failed) != 1 || res.Failed[0].ID != "a" {
		t.Errorf("Failed=%v, want [{a, parse error}]", res.Failed)
	}
	completedSet := make(map[string]bool)
	for _, id := range res.Completed {
		completedSet[id] = true
	}
	if !completedSet["b"] || !completedSet["c"] {
		t.Errorf("Completed=%v, want both b and c present", res.Completed)
	}
}

// ---------------------------------------------------------------------------
// B5: emitWaitResult rendering tests
// ---------------------------------------------------------------------------

func TestEmitWaitResult_TextHumanSummary(t *testing.T) {
	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}
	res := &WaitResult{
		Completed: []string{"a", "b"},
		Failed:    []FailedDoc{{ID: "c", Message: "parse boom"}},
		Timeout:   []string{"d"},
	}
	if err := emitWaitResult(res, fopts, &buf); err != nil {
		t.Fatalf("emitWaitResult: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"a", "b", "c", "d", "parse boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestEmitWaitResult_JSONSingleRecord(t *testing.T) {
	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	res := &WaitResult{
		Completed: []string{"a"},
		Failed:    []FailedDoc{{ID: "b", Message: "x"}},
	}
	if err := emitWaitResult(res, fopts, &buf); err != nil {
		t.Fatalf("emitWaitResult: %v", err)
	}
	var env struct {
		OK   bool       `json:"ok"`
		Data WaitResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	got := env.Data
	if len(got.Completed) != 1 || got.Completed[0] != "a" {
		t.Errorf("Completed=%v, want [a]", got.Completed)
	}
	if len(got.Failed) != 1 || got.Failed[0].ID != "b" {
		t.Errorf("Failed=%v, want [{b, x}]", got.Failed)
	}
}

func TestEmitWaitResult_NDJSONSingleLine(t *testing.T) {
	var buf bytes.Buffer
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatNDJSON}
	res := &WaitResult{Completed: []string{"a"}}
	if err := emitWaitResult(res, fopts, &buf); err != nil {
		t.Fatalf("emitWaitResult: %v", err)
	}
	out := buf.String()
	// NDJSON: single record = single line ending with \n
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected exactly 1 newline, got %q", out)
	}
	var got WaitResult
	if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &got); err != nil {
		t.Fatalf("line not JSON: %v\n%s", err, out)
	}
	if len(got.Completed) != 1 {
		t.Error("expected Completed populated")
	}
}

// ---------------------------------------------------------------------------
// B4: WaitResult.ExitCode tests
// ---------------------------------------------------------------------------

func TestWaitResult_ExitCode(t *testing.T) {
	cases := []struct {
		name string
		res  *WaitResult
		want int
	}{
		{"all completed", &WaitResult{Completed: []string{"a"}}, 0},
		{"empty result", &WaitResult{}, 0},
		{"any failed", &WaitResult{Failed: []FailedDoc{{ID: "a"}}}, 1},
		{"timeout only", &WaitResult{Timeout: []string{"a"}}, 124},
		{"failed wins over timeout", &WaitResult{
			Failed:  []FailedDoc{{ID: "a"}},
			Timeout: []string{"b"},
		}, 1},
		{"failed wins over completed+timeout", &WaitResult{
			Completed: []string{"a"},
			Failed:    []FailedDoc{{ID: "b"}},
			Timeout:   []string{"c"},
		}, 1},
		{"timeout wins over completed", &WaitResult{
			Completed: []string{"a"},
			Timeout:   []string{"b"},
		}, 124},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.res.ExitCode(); got != tc.want {
				t.Errorf("ExitCode = %d, want %d", got, tc.want)
			}
		})
	}
}
