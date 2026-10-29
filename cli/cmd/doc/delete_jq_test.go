package doc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestDocDelete_BatchJQProjectsActualVerdict(t *testing.T) {
	for _, mode := range []string{"json", "ndjson"} {
		for _, failures := range []int{0, 1, 2} {
			t.Run(mode+"/"+string(rune('0'+failures))+" failures", func(t *testing.T) {
				out, _ := iostreams.SetForTest(t)
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, "/api/v1/knowledge/") {
						http.NotFound(w, r)
						return
					}
					calls = append(calls, r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					if len(calls) <= failures {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"success":false,"message":"missing"}`))
						return
					}
					_, _ = w.Write([]byte(`{"success":true}`))
				}))
				t.Cleanup(server.Close)
				f := &cmdutil.Factory{
					Client:   func() (*sdk.Client, error) { return sdk.NewClient(server.URL), nil },
					Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
				}
				root := withRootHarnessDoc(NewCmdDelete(f), "doc-a", "doc-b", "-y", "--format", mode,
					"--jq", "{ok,status,ids:[.data[].id],successes:.meta.successes,failures:.meta.failures}")
				err := root.Execute()
				if failures == 0 && err != nil {
					t.Fatal(err)
				}
				if failures > 0 {
					var typed *cmdutil.Error
					if !errors.As(err, &typed) || typed.Code != cmdutil.CodeOperationFailed {
						t.Fatalf("aggregate error=%v, want operation.failed", err)
					}
				}
				var result struct {
					OK        bool     `json:"ok"`
					Status    string   `json:"status"`
					IDs       []string `json:"ids"`
					Successes int      `json:"successes"`
					Failures  int      `json:"failures"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				wantStatus := []string{"success", "partial", "error"}[failures]
				if result.OK != (failures == 0) || result.Status != wantStatus ||
					result.Successes != 2-failures || result.Failures != failures {
					t.Errorf("projected verdict=%+v, want status=%s successes=%d failures=%d",
						result, wantStatus, 2-failures, failures)
				}
				if strings.Join(result.IDs, ",") != "doc-a,doc-b" || len(calls) != 2 {
					t.Errorf("IDs=%v requests=%v, want both documents in argv order", result.IDs, calls)
				}
			})
		}
	}
}
