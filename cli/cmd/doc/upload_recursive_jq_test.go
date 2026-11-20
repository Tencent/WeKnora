package doc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestUploadRecursive_EmptyBatchHonorsJQ(t *testing.T) {
	for _, mode := range []string{"json", "ndjson"} {
		for _, filter := range []string{"", "{empty:(.data|length),count:.meta.count,status,ok}", "["} {
			t.Run(mode+"/"+filter, func(t *testing.T) {
				out, _ := iostreams.SetForTest(t)
				dir := t.TempDir()
				mkTree(t, dir, "only.txt")
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				}))
				t.Cleanup(server.Close)
				f := &cmdutil.Factory{Client: func() (*sdk.Client, error) { return sdk.NewClient(server.URL), nil }}
				args := []string{dir, "--recursive", "--glob", "*.pdf", "--kb", "00000000-0000-0000-0000-000000000001", "--format", mode}
				if filter != "" {
					args = append(args, "--jq", filter)
				}
				err := withRootHarnessDoc(NewCmdUpload(f), args...).Execute()
				assert.Zero(t, requests.Load(), "no match must make no SDK request")
				if filter == "[" {
					var typed *cmdutil.Error
					require.ErrorAs(t, err, &typed)
					assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
					assert.Equal(t, 5, cmdutil.ExitCode(err))
					assert.Empty(t, out.String())
					return
				}
				require.NoError(t, err)
				var result map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				if filter != "" {
					assert.Equal(t, map[string]any{"empty": float64(0), "count": float64(0), "status": "success", "ok": true}, result)
				} else {
					assert.Equal(t, true, result["ok"])
					assert.Equal(t, "success", result["status"])
					assert.Empty(t, result["data"])
					assert.Equal(t, map[string]any{"count": float64(0), "successes": float64(0), "failures": float64(0)}, result["meta"])
				}
			})
		}
	}
}
