package doc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestDocWait_JQSyntaxCheckedBeforeClient(t *testing.T) {
	for _, expression := range []string{"[", ".data.completed[]"} {
		t.Run(expression, func(t *testing.T) {
			out, _ := iostreams.SetForTest(t)
			var clientCalls, requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"data":    map[string]any{"id": "doc-1", "parse_status": "completed"},
				})
			}))
			t.Cleanup(server.Close)
			f := &cmdutil.Factory{Client: func() (*sdk.Client, error) {
				clientCalls.Add(1)
				return sdk.NewClient(server.URL), nil
			}}
			root := withRootHarnessDoc(NewCmdWait(f),
				"doc-1", "--timeout", "2s", "--format", "json", "--jq", expression)
			err := root.Execute()
			if expression == "[" {
				var typed *cmdutil.Error
				if !errors.As(err, &typed) || typed.Code != cmdutil.CodeInputInvalidArgument {
					t.Fatalf("error=%v, want input.invalid_argument", err)
				}
				if clientCalls.Load() != 0 || requests.Load() != 0 || out.Len() != 0 {
					t.Errorf("invalid syntax performed work: client=%d requests=%d stdout=%q",
						clientCalls.Load(), requests.Load(), out.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if clientCalls.Load() != 1 || requests.Load() != 1 || out.String() != "doc-1\n" {
				t.Errorf("valid projection client=%d requests=%d stdout=%q",
					clientCalls.Load(), requests.Load(), out.String())
			}
		})
	}
}

func TestDocWait_JQFormatPrecedenceBeforePolling(t *testing.T) {
	for _, expression := range []string{"[", ".data.completed[]"} {
		t.Run(expression, func(t *testing.T) {
			t.Setenv("WEKNORA_FORMAT", "text")
			out, _ := iostreams.SetForTest(t)
			var clientCalls int
			f := &cmdutil.Factory{Client: func() (*sdk.Client, error) {
				clientCalls++
				return nil, errors.New("client should not be constructed")
			}}
			root := withRootHarnessDoc(NewCmdWait(f), "doc-1", "--jq", expression)
			err := root.Execute()
			var flagError *cmdutil.FlagError
			if !errors.As(err, &flagError) || cmdutil.ExitCode(err) != 2 || clientCalls != 0 || out.Len() != 0 {
				t.Errorf("text/jq error=%v exit=%d client=%d stdout=%q",
					err, cmdutil.ExitCode(err), clientCalls, out.String())
			}
		})
	}
}
