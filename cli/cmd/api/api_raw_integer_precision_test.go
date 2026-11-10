package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// TestAPI_RawBodyPreservesLargeIntegers pins the raw-passthrough path (`weknora
// api`) to the server's exact integer values. The body is decoded into `any`
// before it is re-serialized into the envelope, so decoding without
// json.Decoder.UseNumber() silently rounds integers above 2^53 through float64
// (9007199254740993 -> 9007199254740992). tenant_id and other snowflake-shaped
// ids are in that range, so an agent projecting `.data.data.tenant_id` would
// receive a different id than the server sent.
func TestAPI_RawBodyPreservesLargeIntegers(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	cli, stop := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/auth/me", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"user":{"id":"user-test","tenant_id":9007199254740993}}}`))
	})
	defer stop()

	require.NoError(t, runAPI(context.Background(), &Options{},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, cli, "GET", "/api/v1/auth/me", false))

	got := out.String()
	assert.Contains(t, got, "9007199254740993", "tenant_id must survive the envelope round-trip unchanged, got %s", got)
	assert.NotContains(t, got, "9007199254740992", "tenant_id was rounded through float64, got %s", got)
}

// TestApi_DryRunPlanPreservesLargeIntegers pins the same exactness guarantee for
// the --dry-run plan echo: an agent inspects meta.plan.body to decide whether
// the request it is about to send is the one it meant. Rounding the id in the
// preview shows a payload the CLI would never actually send.
func TestApi_DryRunPlanPreservesLargeIntegers(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	iostreams.IO.In = strings.NewReader(`{"tenant_id":9007199254740993}`)
	root := withRootHarness(NewCmd(apiDryRunFactory(t)),
		"/api/v1/knowledge-bases", "-X", "POST", "--input", "-", "--dry-run", "--format", "json")
	require.NoError(t, root.Execute(), "POST + --dry-run must succeed without SDK")

	got := out.String()
	assert.Contains(t, got, "9007199254740993", "plan.body must echo the body verbatim, got %s", got)
	assert.NotContains(t, got, "9007199254740992", "plan.body was rounded through float64, got %s", got)
}
