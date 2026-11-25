package doc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestDocWait_ServerCancellationIsTerminal(t *testing.T) {
	for _, message := range []string{"cancelled by user", ""} {
		t.Run(message, func(t *testing.T) {
			out, _ := iostreams.SetForTest(t)
			var cancelledPolls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					http.NotFound(w, r)
					return
				}
				id := strings.TrimPrefix(r.URL.Path, "/api/v1/knowledge/")
				status := "completed"
				if id == "cancelled-doc" {
					status = "cancelled"
					cancelledPolls.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"data":    map[string]any{"id": id, "parse_status": status, "error_message": message},
				})
			}))
			t.Cleanup(server.Close)
			f := &cmdutil.Factory{Client: func() (*sdk.Client, error) { return sdk.NewClient(server.URL), nil }}
			root := withRootHarnessDoc(NewCmdWait(f),
				"cancelled-doc", "complete-doc", "--timeout", "500ms", "--format", "json")
			err := root.Execute()
			var typed *cmdutil.Error
			if !errors.As(err, &typed) || typed.Code != cmdutil.CodeOperationFailed {
				t.Fatalf("error=%v, want operation.failed rather than timeout", err)
			}
			var envelope struct {
				Data WaitResult `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			res := envelope.Data
			if len(res.Failed) != 1 || res.Failed[0].ID != "cancelled-doc" ||
				len(res.Timeout) != 0 || strings.Join(res.Completed, ",") != "complete-doc" {
				t.Fatalf("partition=%+v, want cancelled failed and completed completed", res)
			}
			if message != "" && res.Failed[0].Message != message {
				t.Errorf("message=%q, want %q", res.Failed[0].Message, message)
			}
			if message == "" && !strings.Contains(res.Failed[0].Message, "weknora doc reparse cancelled-doc") {
				t.Errorf("missing recovery hint: %q", res.Failed[0].Message)
			}
			if cancelledPolls.Load() != 1 {
				t.Errorf("cancelled document polled %d times, want 1", cancelledPolls.Load())
			}
		})
	}
}

func TestDocList_CurrentServerStatusFiltersReachHTTP(t *testing.T) {
	for _, status := range []string{
		"pending", "processing", "finalizing", "completed", "failed", "deleting", "cancelled",
	} {
		t.Run(status, func(t *testing.T) {
			iostreams.SetForTest(t)
			var received string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received = r.URL.Query().Get("parse_status")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":[],"total":0}`))
			}))
			t.Cleanup(server.Close)
			f := &cmdutil.Factory{Client: func() (*sdk.Client, error) { return sdk.NewClient(server.URL), nil }}
			root := withRootHarnessDoc(NewCmdList(f),
				"--kb", "00000000-0000-0000-0000-000000000001",
				"--status", strings.ToUpper(status), "--format", "json")
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if received != status {
				t.Errorf("parse_status=%q, want %q", received, status)
			}
		})
	}
}

func TestWaitForDocs_FinalizingRemainsPending(t *testing.T) {
	svc := newFakeKBSvc(map[string][]string{"doc-1": {"finalizing", "completed"}})
	result, err := waitForDocs(context.Background(), []string{"doc-1"}, svc,
		WaitOptions{Timeout: 2 * time.Second, Interval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.Completed, ",") != "doc-1" || len(result.Failed) != 0 ||
		len(result.Timeout) != 0 || svc.calls["doc-1"] != 2 {
		t.Errorf("finalizing must poll until completed: %+v calls=%v", result, svc.calls)
	}
}
