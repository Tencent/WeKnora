package doc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelParseCommand(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/knowledge/report/cancel-parse", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":"report","parse_status":"cancelled"}}`)
	}))
	defer server.Close()
	f := &cmdutil.Factory{Client: func() (*sdk.Client, error) { return sdk.NewClient(server.URL), nil }}
	root := withRootHarnessDoc(NewCmd(f), "cancel-parse", "report", "--format", "json")
	require.NoError(t, root.Execute())
	assert.Contains(t, out.String(), "cancelled")
}

func TestCancelParseDryRun(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	root := withRootHarnessDoc(NewCmd(docDryRunFactory(t)), "cancel-parse", "report", "--dry-run")
	require.NoError(t, root.Execute())
	assert.Contains(t, out.String(), "doc.cancel-parse")
}
