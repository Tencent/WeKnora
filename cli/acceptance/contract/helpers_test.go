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
	"github.com/Tencent/WeKnora/cli/cmd/doctor"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

// TestMain pins the doctor credential-storage outcome for the whole suite.
// Otherwise the check probes the real OS keyring, which differs between
// macOS dev machines (Keychain present → ok) and Linux CI runners without
// libsecret (file fallback → warn), making golden envelopes host-dependent.
// MemStore is neither *FileStore nor a real keyring, so the doctor's
// type-switch hits the StatusOK branch.
func TestMain(m *testing.M) {
	restore := doctor.SetCredStoreFactoryForTest(func() (secrets.Store, error) {
		return secrets.NewMemStore(), nil
	})
	defer restore()
	os.Exit(m.Run())
}

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
//
// Mirrors cmd.Execute() carefully: callers expect the same envelope-printing
// behavior the real entrypoint provides. The helper (a) wires the cobra Out /
// Err sinks to the same buffers it returns (the `version` leaf and any future
// command using c.OutOrStdout would otherwise leak to os.Stdout), and (b)
// re-runs the error-envelope path so failure cases produce the JSON envelope
// the contract test compares against. Without (b), every error scenario's
// golden would be empty.
func runCmd(t *testing.T, f *cmdutil.Factory, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	out, errBuf := iostreams.SetForTest(t)
	root := cmd.NewRootCmd(f) // exported in cli/cmd/root.go (Task 16)
	root.SetArgs(args)
	root.SetContext(context.Background())
	root.SetOut(out)
	root.SetErr(errBuf)
	leaf, err := root.ExecuteC()
	if err != nil {
		err = cmd.MapCobraError(err)
		if cmd.WantsJSONOutput(leaf) {
			cmdutil.PrintErrorEnvelope(iostreams.IO.Out, err)
		} else {
			cmdutil.PrintError(iostreams.IO.Err, err)
		}
	}
	return out.String(), errBuf.String(), cmdutil.ExitCode(err)
}

// assertGolden compares got against the JSON golden file at path.
// With -update, writes got to path. Normalizes _meta.request_id to "<id>"
// before compare (only field known unstable in v0.0).
//
// CRLF normalization: Windows checkouts with the default core.autocrlf=true
// turn LF in tracked text files into CRLF on disk. The command output is
// always LF, so byte-equal would fail despite identical content. .gitattributes
// is the primary defense (forcing LF on testdata/**/*.json), but we also
// strip CR here so a misconfigured contributor checkout doesn't break the
// suite locally before they push.
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
	want = stripCR(want)
	got = stripCR(got)
	if !bytes.Equal(want, got) {
		t.Errorf("envelope mismatch for %s\nwant:\n%s\ngot:\n%s", path, want, got)
	}
}

// stripCR removes CR bytes so CRLF golden files (from Windows autocrlf
// checkout) compare equal to LF runtime output.
func stripCR(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte{'\r'}, nil)
}

// normalizeEnvelope replaces unstable fields with placeholders for stable diff.
// Currently no-op (v0.0 commands don't set _meta.request_id, so output is stable).
// Hook for future fields.
func normalizeEnvelope(b []byte) []byte {
	return b
}
