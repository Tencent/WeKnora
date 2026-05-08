// cli/acceptance/contract/helpers_test.go
package contract_test

import (
	"bytes"
	"context"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/cli/cmd"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// update is the standard Go test golden-update flag.
//   go test -update ./acceptance/contract/...
// Mirrors gh / kubectl / golang-migrate convention.
var update = flag.Bool("update", false, "update golden files")

// newTestFactory builds a Factory whose Client returns mockClient.
// Caller must NOT use t.Parallel() — see iostreams.SetForTest contract.
//
// WEKNORA_BASE_URL is set when mockServer is non-nil. v0.0 buildClient does
// not currently honor this env var (it reads from config.Host); commands that
// need the mock URL must rely on the mockClient injection above. The env
// is set anyway as a forward-affordance for any direct net/http callers
// added in PR-7+ (e.g. doctor's PingBaseURL HEAD /health).
func newTestFactory(t *testing.T, mockServer *httptest.Server, mockClient *sdk.Client) *cmdutil.Factory {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if mockServer != nil {
		t.Setenv("WEKNORA_BASE_URL", mockServer.URL)
	}
	f := cmdutil.New()
	if mockClient != nil {
		f.Client = func() (*sdk.Client, error) { return mockClient, nil }
	}
	return f
}

// runCmd executes the root command in-process and returns captured stdout/stderr.
// Replaces iostreams.IO singleton via SetForTest (auto-restored in t.Cleanup).
func runCmd(t *testing.T, f *cmdutil.Factory, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	out, errBuf := iostreams.SetForTest(t)
	root := cmd.NewRootCmd(f) // exported in cli/cmd/root.go (Task 16)
	root.SetArgs(args)
	root.SetContext(context.Background())
	err := root.Execute()
	return out.String(), errBuf.String(), cmdutil.ExitCode(err)
}

// assertGolden compares got against the JSON golden file at path.
// With -update, writes got to path. Normalizes _meta.request_id to "<id>"
// before compare (only field known unstable in v0.0).
func assertGolden(t *testing.T, got []byte, path string) {
	t.Helper()
	got = normalizeEnvelope(got)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("envelope mismatch for %s\nwant:\n%s\ngot:\n%s", path, want, got)
	}
}

// normalizeEnvelope replaces unstable fields with placeholders for stable diff.
// Currently no-op (v0.0 commands don't set _meta.request_id, so output is stable).
// Hook for future fields.
func normalizeEnvelope(b []byte) []byte {
	return b
}
